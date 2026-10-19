package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/version"
)

// Captures are not operations of the request queue: a capture lives as long as the connection that opened it
// (like a watch), reads its own sockets and writes its own files, and changes nothing in the kernel's network
// configuration. The executor is the process that may open an AF_PACKET socket (plan §3.1: "capture processes
// started by the executor"); the API container that asked for the capture reads the files from the shared
// volume.

// MaxCaptures bounds the captures that run at once in one executor.
const MaxCaptures = 8

// DefaultMinFreePercent is the share of free space of the capture volume below which captures stop and
// new ones are refused (plan §3.6).
const DefaultMinFreePercent = 10.0

// CaptureInterface is an interface of a capture and the filter of its packets.
type CaptureInterface struct {
	Name   string         `json:"name"`
	Filter capture.Filter `json:"filter"`
}

// CaptureOpen asks the executor to start a capture. The directory Dir below the executor's capture root must
// exist (the caller makes it, so it owns the files); the executor makes the files in it and gives them to the
// owner of the directory.
type CaptureOpen struct {
	// Namespace is the named network namespace of the interfaces; empty for the executor's own.
	Namespace  string             `json:"namespace,omitempty"`
	Dir        string             `json:"dir"`
	Format     capture.Format     `json:"format"`
	Snaplen    int                `json:"snaplen,omitempty"`
	Interfaces []CaptureInterface `json:"interfaces"`
	// RingFiles and RingFileSize make a ring buffer; MaxSize bounds a capture that is none.
	RingFiles    int   `json:"ring_files,omitempty"`
	RingFileSize int64 `json:"ring_file_size,omitempty"`
	MaxSize      int64 `json:"max_size,omitempty"`
	// DurationMS ends the capture after this many milliseconds, zero for no bound.
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// Event kinds of a capture.
const (
	CaptureStarted = "started"
	CaptureStats   = "stats"
	CaptureStopped = "stopped"
)

// CaptureEvent is a message of the executor about a capture: it started (with its interfaces), counters
// once a second, and the end. The channel of a stream closes after the last one.
type CaptureEvent struct {
	Kind       string                  `json:"kind"`
	Interfaces []capture.InterfaceInfo `json:"interfaces,omitempty"`
	Packets    int64                   `json:"packets,omitempty"`
	Size       int64                   `json:"size,omitempty"`
	Dropped    int64                   `json:"dropped,omitempty"`
	// Files are the files that exist, oldest first.
	Files []string `json:"files,omitempty"`
	// Reason and Error come with the end: why it ended, and what failed when the reason is `error`.
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

// CaptureCtl is what the client sends on a capture connection after the request: to stop.
type CaptureCtl struct {
	// Stop ends the capture; Reason is `user` or `run_end` and says why.
	Stop   bool   `json:"stop,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// CaptureStream is a capture as its client sees it. The events arrive on Events; Stop asks for the end, which
// comes as an event, and Close releases the stream (it stops a capture that still runs).
type CaptureStream interface {
	Events() <-chan CaptureEvent
	Stop(reason string)
	Close()
}

// ErrNoCaptureDir is returned when the executor was started without a directory for captures.
var ErrNoCaptureDir = errors.New("this executor has no directory for captures")

// ErrTooManyCaptures is returned when MaxCaptures captures run.
var ErrTooManyCaptures = errors.New("too many captures are running")

// ErrDiskLow is returned for a capture on a volume with too little free space.
var ErrDiskLow = capture.ErrDiskLow

// WithCaptures lets the executor capture packets and write the files below the directory. A capture stops when the free
// space of the directory's file system falls below minFreePercent (DefaultMinFreePercent when zero).
func WithCaptures(dir string, minFreePercent float64) Option {
	return func(e *Executor) {
		e.capDir = dir
		if minFreePercent <= 0 {
			minFreePercent = DefaultMinFreePercent
		}
		e.capMinFree = minFreePercent
	}
}

// WithCaptureClock sets the clock of the captures' durations and checks (tests); the executor's clock otherwise.
func WithCaptureClock(c clock.Clock) Option { return func(e *Executor) { e.capClock = c } }

// maxCaptureBytes is the largest size a capture can name (2^62 bytes, the largest a ByteSize of the API reads); a ring
// buffer's files together stay below it, so their total does not overflow.
const maxCaptureBytes = 1 << 62

var captureDirName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ifaceName is what the kernel accepts as the name of an interface (IFNAMSIZ 16 with the terminator).
var ifaceName = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)

// Validate checks the request by itself.
func (o *CaptureOpen) Validate() error {
	if !captureDirName.MatchString(o.Dir) || o.Dir == "." || o.Dir == ".." {
		return fmt.Errorf("invalid capture directory %q", o.Dir)
	}
	if o.Namespace != "" && !nsName.MatchString(o.Namespace) {
		return fmt.Errorf("invalid namespace name %q", o.Namespace)
	}
	if o.Format != capture.PCAP && o.Format != capture.PCAPNG {
		return fmt.Errorf("unknown capture format %q", o.Format)
	}
	if o.Snaplen != 0 && (o.Snaplen < 64 || o.Snaplen > 262144) {
		return errors.New("the snap length is between 64 and 262144")
	}
	if n := len(o.Interfaces); n == 0 || n > capture.MaxInterfaces {
		return fmt.Errorf("a capture has 1 to %d interfaces", capture.MaxInterfaces)
	}
	seen := map[string]bool{}
	for _, i := range o.Interfaces {
		if !ifaceName.MatchString(i.Name) {
			return fmt.Errorf("invalid interface name %q", i.Name)
		}
		if seen[i.Name] {
			return fmt.Errorf("the interface %s is listed twice", i.Name)
		}
		seen[i.Name] = true
	}
	switch {
	case o.RingFiles != 0 && (o.RingFiles < 2 || o.RingFiles > 100):
		return errors.New("a ring buffer has 2 to 100 files")
	case o.RingFiles != 0 && o.RingFileSize <= 0:
		return errors.New("a ring buffer needs a file size")
	case o.RingFiles != 0 && o.RingFileSize > maxCaptureBytes/int64(o.RingFiles):
		return errors.New("the files of a ring buffer take more than the largest size of a capture")
	case o.RingFiles == 0 && o.RingFileSize != 0:
		return errors.New("a file size needs a ring buffer")
	case o.RingFiles != 0 && o.MaxSize != 0:
		return errors.New("a ring buffer is bounded by its files: it takes no maximum size")
	case o.MaxSize < 0 || o.DurationMS < 0:
		return errors.New("a limit is not negative")
	case o.MaxSize > maxCaptureBytes:
		return errors.New("the maximum size is larger than the largest size of a capture")
	}
	return nil
}

// OpenCapture starts a capture and returns its stream. The stream's first event is `started`. Every interface
// must be one assigned to Chaos Gateway (the scope of the executor): a capture reads whatever passes an
// interface, so it is limited like the operations that change them.
func (e *Executor) OpenCapture(ctx context.Context, o CaptureOpen) (CaptureStream, error) {
	if e.capDir == "" {
		return nil, ErrNoCaptureDir
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	for _, i := range o.Interfaces {
		if err := e.scope.need(i.Name); err != nil {
			return nil, err
		}
	}
	select {
	case e.capSlots <- struct{}{}:
	default:
		return nil, ErrTooManyCaptures
	}
	release := func() { <-e.capSlots }
	root, err := os.OpenRoot(e.capDir)
	if err != nil {
		release()
		return nil, fmt.Errorf("the capture directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	dir, err := root.OpenRoot(o.Dir)
	if err != nil {
		release()
		return nil, fmt.Errorf("the directory of the capture: %w", err)
	}
	uid, gid := -1, -1
	if fi, err := dir.Stat("."); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
	}
	cfg := capture.SessionConfig{
		Namespace: o.Namespace, Format: o.Format, Snaplen: o.Snaplen, Dir: dir, MaxSize: o.MaxSize,
		Duration: time.Duration(o.DurationMS) * time.Millisecond, UID: uid, GID: gid, App: "chaos-gateway " + version.Version,
		Clock: e.capClock, MinFreePercent: e.capMinFree, DiskFree: diskFree(e.capDir),
	}
	if o.RingFiles > 0 {
		cfg.Ring = &capture.RingConfig{Files: o.RingFiles, FileSize: o.RingFileSize}
	}
	for _, i := range o.Interfaces {
		cfg.Interfaces = append(cfg.Interfaces, capture.IfaceConfig{Name: i.Name, Filter: i.Filter})
	}
	sess, err := capture.Start(context.WithoutCancel(ctx), cfg)
	if err != nil {
		_ = dir.Close()
		release()
		return nil, err
	}
	s := &sessionStream{sess: sess, events: make(chan CaptureEvent, 16), stopCh: make(chan string, 1), closed: make(chan struct{}), ended: make(chan struct{}), clock: e.capClock, dir: dir, release: release}
	s.events <- CaptureEvent{Kind: CaptureStarted, Interfaces: sess.Interfaces(), Files: sess.Files()}
	go s.run()
	return s, nil
}

// diskFree reads the free and the total space of the file system of dir.
func diskFree(dir string) func() (uint64, uint64, error) {
	return func() (uint64, uint64, error) {
		var st unix.Statfs_t
		if err := unix.Statfs(dir, &st); err != nil {
			return 0, 0, err
		}
		return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
	}
}

// sessionStream is the CaptureStream of a session in this process.
type sessionStream struct {
	sess    *capture.Session
	events  chan CaptureEvent
	stopCh  chan string
	closed  chan struct{} // closed by Close: nobody reads the events any more
	once    sync.Once
	ended   chan struct{} // closed when run has returned
	clock   clock.Clock
	dir     *os.Root
	release func()
}

func (s *sessionStream) Events() <-chan CaptureEvent { return s.events }

// Stop asks the session to end for the reason (`user`, else `run_end`).
func (s *sessionStream) Stop(reason string) {
	select {
	case s.stopCh <- reason:
	default:
	}
}

// Close stops the session if it still runs, waits for it and releases the stream.
func (s *sessionStream) Close() {
	s.once.Do(func() { close(s.closed) })
	s.sess.Stop(capture.ReasonError)
	<-s.ended
}

// run sends the counters once a second and the end, which is always the last event.
func (s *sessionStream) run() {
	defer close(s.ended)
	defer s.release()
	defer func() { _ = s.dir.Close() }()
	tick := s.clock.NewTicker(time.Second)
	defer tick.Stop()
	defer close(s.events)
	for {
		select {
		case reason := <-s.stopCh:
			r := capture.ReasonUser
			if reason == string(capture.ReasonRunEnd) {
				r = capture.ReasonRunEnd
			}
			s.sess.Stop(r)
		case <-tick.C():
			st := s.sess.Stats()
			select {
			case s.events <- CaptureEvent{Kind: CaptureStats, Packets: st.Packets, Size: st.Size, Dropped: st.Dropped, Files: s.sess.Files()}:
			default: // a client that does not read does not hold the capture up
			}
		case <-s.sess.Done():
			st, res := s.sess.Stats(), s.sess.Result()
			ev := CaptureEvent{Kind: CaptureStopped, Packets: st.Packets, Size: st.Size, Dropped: st.Dropped, Files: s.sess.Files(), Reason: string(res.Reason)}
			if res.Err != nil {
				ev.Error = res.Err.Error()
			}
			select {
			case s.events <- ev:
			case <-s.closed:
			}
			return
		}
	}
}
