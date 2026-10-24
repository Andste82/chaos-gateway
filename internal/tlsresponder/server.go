package tlsresponder

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ConnInfo is what the responder knows of a connection when it asks for the decision.
type ConnInfo struct {
	Client netip.Addr
	// Dst is the original destination: where the device meant to go.
	Dst netip.AddrPort
	// SNI is the server name of the ClientHello, empty without one.
	SNI string
}

// Decision is the answer of the API: which case decides a connection. Case is nil when none does, and the connection is
// passed through.
type Decision struct {
	// Generation is the generation of the configuration the decision belongs to.
	Generation int64
	Overlay    string
	Case       *model.TlsCase
	Expected   string
	Device     string
	// TrustsTestCA is whether the device's firmware trusts the test CA.
	TrustsTestCA bool
	// Name is what to certify when the ClientHello has no server name.
	Name string
	// Passthrough says, without a case, whether the connection may be passed on: the gateway would have forwarded it.
	Passthrough bool
}

// Resolver decides the connections. The API implements it behind an HTTP call (APIClient).
type Resolver interface {
	Resolve(ctx context.Context, c ConnInfo) (Decision, error)
}

// Options wires a Server.
type Options struct {
	Clock clock.Clock
	Log   *slog.Logger
	// Issuer makes the certificates; required.
	Issuer *Issuer
	// Resolver decides the connections; required.
	Resolver Resolver
	// Sink receives the outcomes of the handshakes in batches; nil drops them.
	Sink Sink
	// OriginalDst reads where a redirected connection was going; default OriginalDst (SO_ORIGINAL_DST).
	OriginalDst func(net.Conn) (netip.AddrPort, error)
	// Dial connects to the destination of a connection that is passed through; default a plain dial.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// HelloTimeout is how long a connection may take to send its ClientHello (default 10 s), HandshakeTimeout how long a
	// handshake with a certificate may take (default 10 s), StallFor how long handshake_stall holds the connection
	// (default 30 s), ResolveTimeout how long the decision may take (default 3 s) and DialTimeout how long a
	// passed-through connection may take to reach its destination (default 5 s).
	HelloTimeout, HandshakeTimeout, StallFor, ResolveTimeout, DialTimeout time.Duration
}

// Server is the TLS responder.
type Server struct {
	opt Options
	rep *reporter

	cfgMu   sync.Mutex
	cfg     *model.TlsServiceConfig
	active  map[string]bool // the overlays of the configuration
	held    map[*held]struct{}
	ready   chan struct{}
	readyOn sync.Once

	// Counters for tests and diagnostics.
	connections, passedThrough, resolveErrors, resets, denied atomic.Int64
}

// held is a connection a case holds: the overlay that decided it, and the generation of the decision.
type held struct {
	conn    net.Conn
	overlay string
	gen     int64
}

// New returns a Server. It passes every connection through to its destination, or resets it when it cannot say, until it
// has a configuration: the decision is the API's.
func New(o Options) *Server {
	if o.Clock == nil {
		o.Clock = &clock.Real{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.OriginalDst == nil {
		o.OriginalDst = OriginalDst
	}
	if o.Dial == nil {
		o.Dial = (&net.Dialer{}).DialContext
	}
	for _, d := range []struct {
		p *time.Duration
		v time.Duration
	}{{&o.HelloTimeout, 10 * time.Second}, {&o.HandshakeTimeout, 10 * time.Second}, {&o.StallFor, 30 * time.Second}, {&o.ResolveTimeout, 3 * time.Second}, {&o.DialTimeout, 5 * time.Second}} {
		if *d.p <= 0 {
			*d.p = d.v
		}
	}
	return &Server{opt: o, rep: newReporter(o.Sink, o.Clock, o.Log), held: map[*held]struct{}{}, active: map[string]bool{}, ready: make(chan struct{})}
}

// Connections is the number of connections accepted, PassedThrough the number passed on to their destination and
// ResolveErrors the number that could not be decided (and were reset, never passed on).
func (s *Server) Connections() int64   { return s.connections.Load() }
func (s *Server) PassedThrough() int64 { return s.passedThrough.Load() }
func (s *Server) ResolveErrors() int64 { return s.resolveErrors.Load() }

// Denied is the number of connections that no case decided and that were reset instead of passed through, because the
// gateway would not have forwarded them.
func (s *Server) Denied() int64 { return s.denied.Load() }

// Resets is the number of connections reset because the case that held them was removed.
func (s *Server) Resets() int64 { return s.resets.Load() }

// SetConfig replaces the configuration: the overlays that hold a case. A connection that a case of an overlay holds
// and that the configuration of a later generation does not hold any more is reset ("reset on removal", plan §2.8): the
// device meets a failed connection and goes on to the real server, instead of staying with the responder for as long as
// it would.
func (s *Server) SetConfig(c *model.TlsServiceConfig) {
	active := map[string]bool{}
	for _, cs := range c.Cases {
		active[strings.ToLower(cs.Overlay.String())] = true
	}
	s.cfgMu.Lock()
	s.cfg, s.active = c, active
	var gone []*held
	for h := range s.held {
		if h.overlay != "" && !active[h.overlay] && h.gen < c.Generation {
			gone = append(gone, h)
			delete(s.held, h)
		}
	}
	s.cfgMu.Unlock()
	for _, h := range gone {
		s.resets.Add(1)
		reset(h.conn)
	}
}

// Config returns the configuration in use; nil before the first one.
func (s *Server) Config() *model.TlsServiceConfig {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.cfg
}

// RunReports sends the reports of the handshakes to the sink until ctx ends.
func (s *Server) RunReports(ctx context.Context) { s.rep.run(ctx) }

// Ready is closed when Serve listens.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Serve answers on addr until ctx ends.
func (s *Server) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.ServeOn(ctx, ln)
}

// ServeOn answers on the listener until ctx ends and closes it.
func (s *Server) ServeOn(ctx context.Context, ln net.Listener) error {
	s.readyOn.Do(func() { close(s.ready) })
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		s.closeHeld()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(ctx, c)
		}()
	}
}

func (s *Server) closeHeld() {
	s.cfgMu.Lock()
	var all []*held
	for h := range s.held {
		all = append(all, h)
	}
	s.cfgMu.Unlock()
	for _, h := range all {
		_ = h.conn.Close()
	}
}

func (s *Server) hold(c net.Conn, overlay string, gen int64) func() {
	h := &held{conn: c, overlay: strings.ToLower(overlay), gen: gen}
	s.cfgMu.Lock()
	s.held[h] = struct{}{}
	s.cfgMu.Unlock()
	return func() {
		s.cfgMu.Lock()
		delete(s.held, h)
		s.cfgMu.Unlock()
	}
}

// reset closes a connection with a reset instead of a FIN.
func reset(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = c.Close()
}

// Probe connects to the responder and returns when it has accepted the connection, which it then drops: a responder
// that accepts at all is up (the health check of the container).
func Probe(addr string) error {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	return c.Close()
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	s.connections.Add(1)
	defer func() { _ = c.Close() }()
	client := clientAddr(c.RemoteAddr())
	dst, err := s.opt.OriginalDst(c)
	if err != nil {
		// not a connection that was redirected to us (the health check, a probe): there is nothing it could mean
		s.opt.Log.Debug("a connection without an original destination", "client", client, "error", err)
		return
	}
	raw, sni, herr := readClientHello(c, s.opt.Clock.Now().Add(s.opt.HelloTimeout))
	info := ConnInfo{Client: client, Dst: dst, SNI: sni}
	rctx, cancel := context.WithTimeout(ctx, s.opt.ResolveTimeout)
	dec, err := s.opt.Resolver.Resolve(rctx, info)
	cancel()
	if err != nil {
		// without the decision the connection is not passed on: it would let a device that a case is meant for reach the
		// server it must not reach (the gateway fails closed, plan §3.3)
		s.resolveErrors.Add(1)
		s.opt.Log.Warn("cannot decide a connection: it is reset", "client", client, "dst", dst, "error", err)
		reset(c)
		return
	}
	if dec.Case == nil {
		switch {
		case herr != nil:
			// nothing to pass on: it never said anything
		case !dec.Passthrough:
			// the gateway would not have forwarded this connection (the access matrix keeps the device from the server): the redirect
			// must not be the way through
			s.denied.Add(1)
			s.opt.Log.Debug("a connection that no case decides and the gateway would not forward is reset", "client", client, "dst", dst)
			reset(c)
		default:
			s.passThrough(ctx, c, raw, info)
		}
		return
	}
	release := s.hold(c, dec.Overlay, dec.Generation)
	defer release()
	s.serve(ctx, c, raw, herr, info, dec)
}

func clientAddr(a net.Addr) netip.Addr {
	if t, ok := a.(*net.TCPAddr); ok {
		ip, _ := netip.AddrFromSlice(t.IP)
		return ip.Unmap()
	}
	return netip.Addr{}
}

// passThrough relays a connection that no case decides to the destination the device meant, unchanged: the bytes of the
// ClientHello that were read first, then whatever follows in both directions.
func (s *Server) passThrough(ctx context.Context, c net.Conn, hello []byte, info ConnInfo) {
	s.passedThrough.Add(1)
	dctx, cancel := context.WithTimeout(ctx, s.opt.DialTimeout)
	up, err := s.opt.Dial(dctx, "tcp", info.Dst.String())
	cancel()
	if err != nil {
		s.opt.Log.Debug("cannot reach the destination of a connection that no case decides", "client", info.Client, "dst", info.Dst, "error", err)
		reset(c)
		return
	}
	defer func() { _ = up.Close() }()
	release := s.hold(c, "", 0)
	defer release()
	if _, err := up.Write(hello); err != nil {
		reset(c)
		return
	}
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(up, c)
	go pipe(c, up)
	<-done
	<-done
}

// prefixConn replays the bytes that were read before the TLS stack takes the connection over.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (p *prefixConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}
