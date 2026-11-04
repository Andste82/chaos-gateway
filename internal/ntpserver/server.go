package ntpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Port is the NTP port: the gateway address and every scope without a destination selector arrive on it.
const Port = 123

// Options wires a Server.
type Options struct {
	// Clock is the host's clock, the time the server serves; production uses the real one, the tests a fake one.
	Clock clock.Clock
	// Sync says whether the host clock is synchronized; default the kernel's answer (adjtimex).
	Sync SyncState
	Log  *slog.Logger
	// Sink receives the query log in batches; nil drops the log.
	Sink Sink
	// RateLimit is the queries per second one device may ask; 0 is DefaultRateLimit, a negative value turns the limit off
	// (the speed tests).
	RateLimit float64
	// BasePort is the port the server always listens on; default 123.
	BasePort int
	// LocalAddr is the address the server listens on when it is not the wildcard: the health check reaches it from there, and
	// a probe from it is answered like one from the loopback.
	LocalAddr netip.Addr
	// OpenRetry is how long after a port of the configuration could not be opened the server tries it again; default
	// DefaultOpenRetry.
	OpenRetry time.Duration
}

// DefaultOpenRetry is how often a port that cannot be opened is tried again: the queries redirected to it get no answer
// until it opens, and a configuration that does not change would never bring another try.
const DefaultOpenRetry = 5 * time.Second

// Counters are the totals since the server started.
type Counters struct {
	Queries    int64 // client queries that were answered or deliberately not (silence), the dropped ones excluded
	Answered   int64 // replies sent
	Silenced   int64 // queries a fault left unanswered
	Dropped    int64 // queries over the device's rate limit
	Ignored    int64 // datagrams that are no client query: modes 6 and 7, short, other modes, version 0
	Unserved   int64 // client queries that arrived before the server had a configuration
	NTSKE      int64 // connections to the NTS key-establishment port
	LogDropped int64 // log entries lost because the API did not take them
}

// Server is the NTP server.
type Server struct {
	opt       Options
	cfg       atomic.Pointer[model.NtpServiceConfig]
	parsed    atomic.Pointer[parsedConfig]
	limit     *limiter
	log       *queryLog
	reconf    chan struct{}
	ready     chan struct{}
	readyOnce sync.Once

	// ntsSeen is when a device's NTS was last reported (reportNTS)
	ntsMu   sync.Mutex
	ntsSeen map[netip.Addr]time.Duration

	queries, answered, silenced, dropped, ignored, unserved, ntske atomic.Int64
}

// New returns a Server that answers nothing but its health probe until it has a configuration: it cannot know whether
// a time fault applies to the device, and the correct time sent during a fault would be wrong.
func New(o Options) *Server {
	if o.Clock == nil {
		o.Clock = &clock.Real{}
	}
	if o.Sync == nil {
		o.Sync = HostSync{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	switch {
	case o.RateLimit == 0:
		o.RateLimit = DefaultRateLimit
	case o.RateLimit < 0:
		o.RateLimit = 0
	}
	if o.BasePort <= 0 {
		o.BasePort = Port
	}
	if o.OpenRetry <= 0 {
		o.OpenRetry = DefaultOpenRetry
	}
	return &Server{opt: o, limit: newLimiter(o.RateLimit), log: newQueryLog(o.Sink, o.Clock, o.Log), reconf: make(chan struct{}, 1), ready: make(chan struct{})}
}

// SetConfig replaces the configuration; the sockets follow it (Serve opens the ports it names and closes the others).
func (s *Server) SetConfig(c *model.NtpServiceConfig) {
	s.parsed.Store(parseConfig(c))
	s.cfg.Store(c)
	select {
	case s.reconf <- struct{}{}:
	default:
	}
}

// Config returns the configuration in use; nil before the first one.
func (s *Server) Config() *model.NtpServiceConfig { return s.cfg.Load() }

// Counters returns the totals.
func (s *Server) Counters() Counters {
	return Counters{
		Queries: s.queries.Load(), Answered: s.answered.Load(), Silenced: s.silenced.Load(), Dropped: s.dropped.Load(),
		Ignored: s.ignored.Load(), Unserved: s.unserved.Load(), NTSKE: s.ntske.Load(), LogDropped: s.log.Dropped(),
	}
}

// Ready is closed when Serve listens on its base port.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// RunLog sends the query log to the sink until ctx ends.
func (s *Server) RunLog(ctx context.Context) { s.log.run(ctx) }

// Process answers one datagram that arrived from client on the server's port, received at rx (the kernel's time stamp;
// the zero time takes the clock's now). It returns the reply (nil for none) and the log entry of the query (nil for a
// datagram that is no client query). It does no I/O, so every answer kind is a table test.
//
// A datagram that is not a client query of NTP version 1 to 7 gets no answer: modes 6 and 7 (the control and private
// modes, which `monlist` abuses to amplify) among them, a datagram of less than the 48 bytes of a request, and
// anything but mode 3. A reply is 48 bytes and so never larger than the request it answers.
func (s *Server) Process(req []byte, client netip.Addr, port int, rx time.Time) (reply []byte, entry *model.NtpQueryLogEntry) {
	q, err := Parse(req)
	if err != nil || q.Mode != ModeClient || q.Version == 0 {
		s.ignored.Add(1)
		return nil, nil
	}
	if rx.IsZero() {
		rx = s.opt.Clock.Now()
	}
	// the health check asks from the server's own host; a device that sends the same timestamp is an ordinary client, so
	// the probe is no way to the real time under a fault and no way around the rate limit
	if q.Transmit == probeTransmit && (client.IsLoopback() || (s.opt.LocalAddr.IsValid() && client == s.opt.LocalAddr)) {
		return s.probeReply(q, rx), nil
	}
	cfg := s.parsed.Load()
	if cfg == nil {
		s.unserved.Add(1)
		return nil, nil
	}
	e := model.NtpQueryLogEntry{Time: rx, Client: client.String(), Port: port}
	version := int(q.Version)
	e.Version = &version
	if ok, logIt := s.limit.allow(client, s.opt.Clock.Monotonic()); !ok {
		s.dropped.Add(1)
		if !logIt {
			return nil, nil
		}
		a := model.NtpQueryLogEntryAnswerDropped
		e.Answer = &a
		return nil, &e
	}
	s.queries.Add(1)
	if hasNTSExtension(req) {
		// the client wants authenticated time: it is answered as a plain query, which it will discard
		s.reportNTS(client, port)
	}
	f := cfg.faultFor(client, port)
	// the transmit timestamp as late as possible
	tx := s.opt.Clock.Now()
	if tx.Before(rx) {
		tx = rx
	}
	r := s.answerTo(q, f, cfg, client, rx, tx)
	e.Answer = &r.answer
	if f != nil {
		id := f.overlay
		e.Fault = &id
		if f.server != "" {
			srv := f.server
			e.Server = &srv
		}
	}
	if r.reply == nil {
		s.silenced.Add(1)
		return nil, &e
	}
	s.answered.Add(1)
	if !r.served.IsZero() {
		t := r.served.UTC()
		e.ServedTime = &t
	}
	off := r.offsetMs
	e.OffsetMs = &off
	return r.reply, &e
}

// probeReply answers the health check's query with the host's time.
func (s *Server) probeReply(q Packet, rx time.Time) []byte {
	sync := s.opt.Sync.State()
	p := Packet{Version: min(q.Version, 4), Mode: ModeServer, Stratum: answerStratum, Poll: q.Poll, Precision: precision,
		Origin: q.Transmit, Receive: TimestampOf(rx), Transmit: TimestampOf(s.opt.Clock.Now())}
	if !sync.Synchronized {
		p.Leap, p.Stratum = LeapUnsynchronized, invalidStratum
	}
	return p.Marshal()
}

// listener is one open port.
type listener struct {
	pc   net.PacketConn
	done chan struct{}
}

// Serve listens on the base port and on the ports of the configuration until ctx ends, and follows the configuration:
// a port that the next one names is opened, one it no longer names is closed. A port that cannot be opened is logged and
// tried again after OpenRetry and at the next configuration; only the base port is fatal.
func (s *Server) Serve(ctx context.Context, listen ListenFunc) error {
	open := map[int]*listener{}
	closeAll := func() {
		for _, l := range open {
			_ = l.pc.Close()
		}
		for _, l := range open {
			<-l.done
		}
	}
	base, err := listen(s.opt.BasePort)
	if err != nil {
		return fmt.Errorf("the NTP port %d: %w", s.opt.BasePort, err)
	}
	open[s.opt.BasePort] = s.start(ctx, base, s.opt.BasePort)
	s.readyOnce.Do(func() { close(s.ready) })
	failed := map[int]bool{}
	for {
		s.reconcile(ctx, open, failed, listen)
		var retry <-chan time.Time
		if len(failed) > 0 {
			retry = s.opt.Clock.After(s.opt.OpenRetry)
		}
		select {
		case <-ctx.Done():
			closeAll()
			return nil
		case <-s.reconf:
		case <-retry:
		}
	}
}

// reconcile makes the open ports the wanted ones.
func (s *Server) reconcile(ctx context.Context, open map[int]*listener, failed map[int]bool, listen ListenFunc) {
	want := map[int]bool{s.opt.BasePort: true}
	if c := s.cfg.Load(); c != nil {
		for _, p := range c.Listen {
			if p >= 1 && p <= 65535 {
				want[p] = true
			}
		}
	}
	for p, l := range open {
		if !want[p] {
			_ = l.pc.Close()
			<-l.done
			delete(open, p)
		}
	}
	ports := make([]int, 0, len(want))
	for p := range want {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		if open[p] != nil {
			continue
		}
		pc, err := listen(p)
		if err != nil {
			if !failed[p] {
				s.opt.Log.Warn("cannot listen on a port of the configuration: queries redirected to it get no answer until it opens", "port", p, "error", err)
			}
			failed[p] = true
			continue
		}
		delete(failed, p)
		open[p] = s.start(ctx, pc, p)
	}
	for p := range failed {
		if !want[p] {
			delete(failed, p)
		}
	}
}

func (s *Server) start(ctx context.Context, pc net.PacketConn, port int) *listener {
	l := &listener{pc: pc, done: make(chan struct{})}
	go func() {
		defer close(l.done)
		s.serveConn(ctx, pc, port)
	}()
	return l
}

// serveConn reads from one socket until it is closed. A query is answered in the reading goroutine: building an answer
// takes microseconds, and one goroutine per port keeps the order of the queries of a device.
func (s *Server) serveConn(ctx context.Context, pc net.PacketConn, port int) {
	buf := make([]byte, 2048)
	oob := make([]byte, oobSize)
	for {
		n, from, rx, err := readPacket(pc, buf, oob)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			s.opt.Log.Debug("cannot read from the NTP socket", "port", port, "error", err)
			// a read that fails for good (not a closed socket) must not spin
			select {
			case <-ctx.Done():
				return
			case <-s.opt.Clock.After(10 * time.Millisecond):
			}
			continue
		}
		reply, e := s.Process(buf[:n], from.Addr(), port, rx)
		if reply != nil && len(reply) <= n {
			if uc, ok := pc.(*net.UDPConn); ok {
				_, _ = uc.WriteToUDPAddrPort(reply, from)
			} else {
				_, _ = pc.WriteTo(reply, net.UDPAddrFromAddrPort(from))
			}
		}
		if e != nil {
			s.log.add(*e)
		}
	}
}
