package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// StopReason is why a capture ended; the names are those of `Capture.stop_reason` in the spec.
type StopReason string

// The reasons a session ends for. The run that attaches a capture stops it with ReasonRunEnd, which the session
// only passes on.
const (
	ReasonUser     StopReason = "user"
	ReasonDuration StopReason = "duration"
	ReasonSize     StopReason = "size"
	ReasonDiskLow  StopReason = "disk_low"
	ReasonRunEnd   StopReason = "run_end"
	ReasonError    StopReason = "error"
)

// MaxInterfaces is how many interfaces one capture reads.
const MaxInterfaces = 16

// maxSnaplen is the largest snap length (the spec's maximum).
const maxSnaplen = 262144

// IfaceConfig is an interface of a session and the filter of its packets.
type IfaceConfig struct {
	Name   string
	Filter Filter
}

// RingConfig makes a ring buffer: Files files of about FileSize bytes.
type RingConfig struct {
	Files    int
	FileSize int64
}

// SessionConfig is a capture to start.
type SessionConfig struct {
	// Namespace is the named network namespace of the interfaces, empty for the process's own.
	Namespace  string
	Interfaces []IfaceConfig
	Format     Format
	// Snaplen cuts each packet to this length, 262144 when zero.
	Snaplen int
	// Dir is the directory the files are made in; Base the start of their names ("capture" when empty).
	Dir  *os.Root
	Base string
	// Ring makes a ring buffer; MaxSize bounds a capture that is none (headers included, zero for no bound).
	Ring    *RingConfig
	MaxSize int64
	// Duration ends the capture after this time, zero for no bound.
	Duration time.Duration
	// UID and GID own the files, negative for the process's own.
	UID, GID int
	// DiskFree reads the free and the total space of the file system of Dir; MinFreePercent is the share of free space
	// below which the capture stops with ReasonDiskLow (plan §3.6: 10). Zero or a nil function: no check.
	DiskFree       func() (free, total uint64, err error)
	MinFreePercent float64
	// App is written into the section header of a pcapng file.
	App string
	// Clock times the duration and the checks; the real clock when nil.
	Clock clock.Clock
}

// Stats are the counters of a running session.
type Stats struct {
	// Packets are the packets written, Size the bytes of the files that exist and Dropped the packets the kernel
	// dropped because the reader was too slow.
	Packets, Size, Dropped int64
}

// Result is how a session ended.
type Result struct {
	Reason StopReason
	// Err says what failed when the reason is ReasonError.
	Err error
}

// checkEvery is how often a session looks at the disk and flushes its file.
const checkEvery = 250 * time.Millisecond

// recvTimeout is how long a read waits for a packet before it looks at the stop request again; a stopped session
// reads until the socket has been quiet this long, so the packets that arrived before the stop are in the file.
const recvTimeout = 100 * time.Millisecond

// Session is a running capture: one AF_PACKET socket per interface, each read by a goroutine of its own, and the
// packets in the files of one Output.
type Session struct {
	cfg    SessionConfig
	clk    clock.Clock
	out    *Output
	ifaces []InterfaceInfo
	socks  []*packetSocket

	stopping atomic.Bool
	once     sync.Once
	reason   StopReason
	err      error
	rmu      sync.Mutex

	dropped atomic.Int64
	readers sync.WaitGroup
	done    chan struct{}
	cancel  context.CancelFunc
}

// Start opens the interfaces and starts reading. The files exist when Start returns; after a failure the directory
// may hold an empty file, which the caller deletes with it.
// The session runs until Stop, its duration, a limit or an error; Done is closed then.
func Start(ctx context.Context, cfg SessionConfig) (*Session, error) {
	if len(cfg.Interfaces) == 0 || len(cfg.Interfaces) > MaxInterfaces {
		return nil, fmt.Errorf("a capture has 1 to %d interfaces", MaxInterfaces)
	}
	if cfg.Snaplen == 0 {
		cfg.Snaplen = maxSnaplen
	}
	if cfg.Snaplen < 64 || cfg.Snaplen > maxSnaplen {
		return nil, errors.New("the snap length is between 64 and 262144")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.NewReal()
	}
	s := &Session{cfg: cfg, clk: cfg.Clock, done: make(chan struct{})}
	if cfg.DiskFree != nil && cfg.MinFreePercent > 0 {
		if free, total, err := cfg.DiskFree(); err == nil && total > 0 && float64(free)*100/float64(total) < cfg.MinFreePercent {
			return nil, ErrDiskLow
		}
	}
	for _, ic := range cfg.Interfaces {
		ps, err := openPacketSocket(cfg.Namespace, ic.Name, ic.Filter, cfg.Snaplen)
		if err != nil {
			for _, o := range s.socks {
				o.close()
			}
			return nil, fmt.Errorf("interface %s: %w", ic.Name, err)
		}
		s.socks = append(s.socks, ps)
		s.ifaces = append(s.ifaces, InterfaceInfo{Name: ic.Name, Link: ps.link, Snaplen: cfg.Snaplen})
	}
	ocfg := OutputConfig{Root: cfg.Dir, Format: cfg.Format, Base: cfg.Base, Ifaces: s.ifaces, App: cfg.App, MaxSize: cfg.MaxSize, UID: cfg.UID, GID: cfg.GID}
	if cfg.Ring != nil {
		ocfg.Files, ocfg.FileSize = cfg.Ring.Files, cfg.Ring.FileSize
	}
	out, err := NewOutput(ocfg)
	if err != nil {
		for _, o := range s.socks {
			o.close()
		}
		return nil, err
	}
	s.out = out
	// the sockets are bound: the kernel queues packets from now on. A session whose files could not be made has
	// not started, so the packets are read only from here.
	for _, ps := range s.socks {
		if err := ps.bind(); err != nil {
			for _, o := range s.socks {
				o.close()
			}
			_ = out.Close()
			return nil, fmt.Errorf("interface %s: %w", ps.name, err)
		}
	}
	rctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	for i, ps := range s.socks {
		s.readers.Add(1)
		go s.read(i, ps)
	}
	go s.control(rctx)
	return s, nil
}

// ErrDiskLow is returned by Start when the file system has less free space than the session's limit.
var ErrDiskLow = errors.New("capture: the disk is almost full")

// Interfaces returns the interfaces of the session with the link type each turned out to have.
func (s *Session) Interfaces() []InterfaceInfo { return append([]InterfaceInfo(nil), s.ifaces...) }

// Files returns the names of the files that exist, oldest first.
func (s *Session) Files() []string { return s.out.Files() }

// Stats returns the counters.
func (s *Session) Stats() Stats {
	return Stats{Packets: s.out.Packets(), Size: s.out.Size(), Dropped: s.dropped.Load()}
}

// Flush hands the packets read so far to the files.
func (s *Session) Flush() error { return s.out.Flush() }

// Done is closed when the session has ended and its files are closed.
func (s *Session) Done() <-chan struct{} { return s.done }

// Result returns how the session ended; valid once Done is closed.
func (s *Session) Result() Result {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	return Result{Reason: s.reason, Err: s.err}
}

// Stop ends the session for the reason (the first reason counts) and returns when its files are closed. The
// packets that reached the sockets before the call are in the files.
func (s *Session) Stop(reason StopReason) {
	s.end(reason, nil)
	<-s.done
}

func (s *Session) end(reason StopReason, err error) {
	s.once.Do(func() {
		s.rmu.Lock()
		s.reason, s.err = reason, err
		s.rmu.Unlock()
		s.stopping.Store(true)
		s.cancel()
	})
}

// control looks at the clock: the duration, the disk, the flushing of the file.
func (s *Session) control(ctx context.Context) {
	defer func() {
		s.readers.Wait()
		if err := s.out.Close(); err != nil {
			// the first reason stays, but the error that the last block could not be written must not get lost
			s.end(ReasonError, err)
			s.rmu.Lock()
			if s.err == nil {
				s.err = err
			}
			s.rmu.Unlock()
		}
		for _, ps := range s.socks {
			ps.close()
		}
		close(s.done)
	}()
	tick := s.clk.NewTicker(checkEvery)
	defer tick.Stop()
	var dur <-chan time.Time
	if s.cfg.Duration > 0 {
		t := s.clk.NewTimer(s.cfg.Duration)
		defer t.Stop()
		dur = t.C()
	}
	for {
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !s.stopping.Load() {
				s.end(ReasonError, err)
			}
			return
		case <-dur:
			s.end(ReasonDuration, nil)
			return
		case <-tick.C():
			if err := s.out.Flush(); err != nil {
				s.fail(err)
				return
			}
			if s.cfg.DiskFree != nil && s.cfg.MinFreePercent > 0 {
				if free, total, err := s.cfg.DiskFree(); err == nil && total > 0 && float64(free)*100/float64(total) < s.cfg.MinFreePercent {
					s.end(ReasonDiskLow, nil)
					return
				}
			}
		}
	}
}

// fail ends the session for a write error: a full disk is the disk-low reason.
func (s *Session) fail(err error) {
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		s.end(ReasonDiskLow, err)
		return
	}
	s.end(ReasonError, err)
}

// read is the goroutine of one interface.
func (s *Session) read(idx int, ps *packetSocket) {
	defer s.readers.Done()
	buf := make([]byte, maxSnaplen)
	oob := make([]byte, 256)
	n, after := 0, 0
	for {
		pkt, ok, err := ps.recv(buf, oob)
		if err != nil {
			s.fail(fmt.Errorf("interface %s: %w", ps.name, err))
			s.addDrops(ps)
			return
		}
		if !ok {
			// quiet for a while: that ends a stopped session, else look at the drops and go on
			s.addDrops(ps)
			if s.stopping.Load() {
				return
			}
			continue
		}
		if s.stopping.Load() {
			// a stopped session takes what was queued when it stopped, not what keeps coming
			if after++; after > drainLimit {
				s.addDrops(ps)
				return
			}
		}
		if ps.userFilter != nil {
			if !ps.userFilter.Match(pkt.Data) {
				continue
			}
			if len(pkt.Data) > s.cfg.Snaplen {
				pkt.Data = pkt.Data[:s.cfg.Snaplen]
			}
		}
		pkt.Iface = idx
		if pkt.Time.IsZero() {
			pkt.Time = s.clk.Now()
		}
		switch err := s.out.Write(pkt); {
		case err == nil:
		case errors.Is(err, ErrSizeLimit):
			s.end(ReasonSize, nil)
		default:
			s.fail(err)
		}
		if n++; n%4096 == 0 {
			s.addDrops(ps)
		}
	}
}

// drainLimit bounds the packets a stopped session still reads: the queue of a socket holds fewer.
const drainLimit = 1 << 16

func (s *Session) addDrops(ps *packetSocket) {
	if d := ps.drops(); d > 0 {
		s.dropped.Add(d)
	}
}

// ---- the AF_PACKET socket

type packetSocket struct {
	name    string
	fd      int
	ifindex int
	link    LinkType
	// userFilter is set when the kernel filter was too large and the packets are filtered in user space
	userFilter *Matcher
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// openPacketSocket makes the socket in the namespace and sets it up, but does not bind it: until it is bound, no
// packet is queued, so the filter is in place before the first one.
func openPacketSocket(ns, name string, f Filter, snaplen int) (*packetSocket, error) {
	var ps *packetSocket
	err := inNamespace(ns, func() error {
		fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			return err
		}
		p := &packetSocket{name: name, fd: fd}
		if err := p.setup(f, snaplen); err != nil {
			p.close()
			return err
		}
		ps = p
		return nil
	})
	return ps, err
}

func (p *packetSocket) setup(f Filter, snaplen int) error {
	req, err := unix.NewIfreq(p.name)
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(p.fd, unix.SIOCGIFINDEX, req); err != nil {
		if errors.Is(err, unix.ENODEV) {
			return errors.New("there is no such interface")
		}
		return err
	}
	p.ifindex = int(req.Uint32())
	hw, _ := unix.NewIfreq(p.name)
	if err := unix.IoctlIfreq(p.fd, unix.SIOCGIFHWADDR, hw); err != nil {
		return err
	}
	switch hw.Uint16() {
	case 1, 772: // ARPHRD_ETHER, ARPHRD_LOOPBACK
		p.link = LinkEthernet
	case 65534, 519: // ARPHRD_NONE (WireGuard), ARPHRD_RAWIP
		p.link = LinkRaw
	default:
		return fmt.Errorf("the interface has the hardware type %d, which cannot be captured", hw.Uint16())
	}
	m, err := f.Matcher(p.link)
	if err != nil {
		return err
	}
	prog, err := m.BPF(snaplen)
	if errors.Is(err, ErrTooLarge) {
		// every packet of the interface reaches the reader, which filters them and cuts them to the snap length
		p.userFilter = m
		prog, err = (&Matcher{link: p.link}).BPF(maxSnaplen)
	}
	if err != nil {
		return err
	}
	if err := p.attach(prog); err != nil {
		return fmt.Errorf("the kernel refuses the filter: %w", err)
	}
	// the kernel's own time stamp and the original length of a packet that the filter cut
	if err := unix.SetsockoptInt(p.fd, unix.SOL_SOCKET, unix.SO_TIMESTAMPNS, 1); err != nil {
		return err
	}
	if err := unix.SetsockoptInt(p.fd, unix.SOL_PACKET, unix.PACKET_AUXDATA, 1); err != nil {
		return err
	}
	// a large queue, so that a burst does not cost packets; the forced size ignores the system's limit when the
	// process may, else the system's limit applies
	if err := unix.SetsockoptInt(p.fd, unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, 16<<20); err != nil {
		_ = unix.SetsockoptInt(p.fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 16<<20)
	}
	tv := unix.NsecToTimeval(int64(recvTimeout))
	return unix.SetsockoptTimeval(p.fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
}

// attach installs the classic BPF program on the socket.
func (p *packetSocket) attach(prog []bpf.Instruction) error {
	raw, err := bpf.Assemble(prog)
	if err != nil {
		return err
	}
	filter := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		filter[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	fprog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	return unix.SetsockoptSockFprog(p.fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &fprog)
}

// bind attaches the socket to the interface: the kernel queues packets from now on.
func (p *packetSocket) bind() error {
	return unix.Bind(p.fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ALL), Ifindex: p.ifindex})
}

func (p *packetSocket) close() {
	if p.fd >= 0 {
		_ = unix.Close(p.fd)
		p.fd = -1
	}
}

// recv reads one packet; ok is false when the socket was quiet for the receive timeout.
func (p *packetSocket) recv(buf, oob []byte) (Packet, bool, error) {
	n, oobn, _, from, err := unix.Recvmsg(p.fd, buf, oob, 0)
	if err != nil {
		switch {
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
			return Packet{}, false, nil
		case errors.Is(err, unix.ENETDOWN), errors.Is(err, unix.ENXIO):
			return Packet{}, false, fmt.Errorf("the interface is gone: %w", err)
		}
		return Packet{}, false, err
	}
	pkt := Packet{Data: append([]byte(nil), buf[:n]...), OrigLen: n}
	if ll, ok := from.(*unix.SockaddrLinklayer); ok {
		pkt.Outgoing = ll.Pkttype == unix.PACKET_OUTGOING
	}
	if msgs, err := unix.ParseSocketControlMessage(oob[:oobn]); err == nil {
		for _, m := range msgs {
			switch {
			case m.Header.Level == unix.SOL_SOCKET && m.Header.Type == unix.SCM_TIMESTAMPNS && len(m.Data) >= int(unsafe.Sizeof(unix.Timespec{})):
				ts := *(*unix.Timespec)(unsafe.Pointer(&m.Data[0]))
				pkt.Time = time.Unix(0, unix.TimespecToNsec(ts))
			case m.Header.Level == unix.SOL_PACKET && m.Header.Type == unix.PACKET_AUXDATA && len(m.Data) >= int(unsafe.Sizeof(unix.TpacketAuxdata{})):
				aux := *(*unix.TpacketAuxdata)(unsafe.Pointer(&m.Data[0]))
				if int(aux.Len) >= n {
					pkt.OrigLen = int(aux.Len)
				}
			}
		}
	}
	return pkt, true, nil
}

// drops returns the packets the kernel dropped since the last call.
func (p *packetSocket) drops() int64 {
	st, err := unix.GetsockoptTpacketStats(p.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
	if err != nil {
		return 0
	}
	return int64(st.Drops)
}

// ---- namespaces

// inNamespace runs f with the calling thread in the named network namespace (/run/netns/<name>), which the
// sockets f makes keep. The thread goes back to its own namespace afterwards; a thread that cannot is not given
// back to the Go runtime.
func inNamespace(name string, f func() error) error {
	if name == "" {
		return f()
	}
	runtime.LockOSThread()
	own, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		runtime.UnlockOSThread()
		return err
	}
	defer func() { _ = own.Close() }()
	target, err := os.Open("/run/netns/" + name)
	if err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("the network namespace %s: %w", name, err)
	}
	defer func() { _ = target.Close() }()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("cannot enter the network namespace %s: %w", name, err)
	}
	ferr := f()
	if err := unix.Setns(int(own.Fd()), unix.CLONE_NEWNET); err != nil {
		// the thread stays locked: it ends with this goroutine instead of running other work in the wrong namespace
		return errors.Join(ferr, fmt.Errorf("cannot leave the network namespace %s: %w", name, err))
	}
	runtime.UnlockOSThread()
	return ferr
}
