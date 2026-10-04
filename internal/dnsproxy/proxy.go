package dnsproxy

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Exchanger sends a query to one upstream resolver. The proxy uses the miekg client; tests use a fake.
type Exchanger interface {
	Exchange(ctx context.Context, m *dns.Msg, network, server string) (*dns.Msg, error)
}

// Options wires a Server.
type Options struct {
	Clock clock.Clock
	Log   *slog.Logger
	// Upstream defaults to a real client.
	Upstream Exchanger
	// Sink receives the query log in batches; nil drops the log.
	Sink Sink
	// Timeout is how long one upstream resolver may take; default 2 s.
	Timeout time.Duration
	// CacheEntries bounds the cache; default 4096.
	CacheEntries int
	// UpstreamPort is the port of the upstream resolvers; default 53.
	UpstreamPort string
}

// Server is the DNS proxy.
type Server struct {
	opt   Options
	cfg   atomic.Pointer[model.DnsServiceConfig]
	cache *cache
	log   *queryLog
	// Counters for tests and diagnostics.
	queries, upstreamQueries atomic.Int64
	ready                    chan struct{}
	readyOnce                sync.Once
}

// New returns a Server that answers SERVFAIL until it has a configuration.
func New(o Options) *Server {
	if o.Clock == nil {
		o.Clock = &clock.Real{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Second
	}
	if o.CacheEntries <= 0 {
		o.CacheEntries = 4096
	}
	if o.UpstreamPort == "" {
		o.UpstreamPort = "53"
	}
	if o.Upstream == nil {
		o.Upstream = &client{}
	}
	return &Server{opt: o, cache: newCache(o.CacheEntries, o.Clock), log: newQueryLog(o.Sink, o.Clock, o.Log), ready: make(chan struct{})}
}

// SetConfig replaces the configuration. A new generation drops the cache: a changed upstream or
// static entry must not be hidden by an old answer.
func (s *Server) SetConfig(c *model.DnsServiceConfig) {
	old := s.cfg.Swap(c)
	if old == nil || old.Generation != c.Generation {
		s.cache.flush()
	}
}

// Config returns the configuration in use; nil before the first one.
func (s *Server) Config() *model.DnsServiceConfig { return s.cfg.Load() }

// Queries is the number of queries answered, UpstreamQueries the number sent upstream.
func (s *Server) Queries() int64         { return s.queries.Load() }
func (s *Server) UpstreamQueries() int64 { return s.upstreamQueries.Load() }

// Serve answers on addr (UDP and TCP) until ctx ends.
func (s *Server) Serve(ctx context.Context, addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = pc.Close()
		return err
	}
	return s.ServeOn(ctx, pc, ln)
}

// ServeOn answers on the sockets it is given until ctx ends and closes them.
func (s *Server) ServeOn(ctx context.Context, pc net.PacketConn, ln net.Listener) error {
	udp := &dns.Server{PacketConn: pc, Net: "udp", Handler: s, UDPSize: 4096}
	tcp := &dns.Server{Listener: ln, Net: "tcp", Handler: s, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	errc := make(chan error, 2)
	started := make(chan struct{}, 2)
	udp.NotifyStartedFunc = func() { started <- struct{}{} }
	tcp.NotifyStartedFunc = func() { started <- struct{}{} }
	go func() { errc <- udp.ActivateAndServe() }()
	go func() { errc <- tcp.ActivateAndServe() }()
	var firstErr error
	for ready := 0; ready < 2 && firstErr == nil; {
		select {
		case <-started:
			ready++
		case firstErr = <-errc:
		case <-ctx.Done():
			firstErr = ctx.Err()
		}
	}
	if firstErr == nil {
		s.readyOnce.Do(func() { close(s.ready) })
		select {
		case <-ctx.Done():
		case firstErr = <-errc:
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = udp.ShutdownContext(sctx)
	_ = tcp.ShutdownContext(sctx)
	if ctx.Err() != nil {
		return nil
	}
	return firstErr
}

// Ready is closed when Serve listens on UDP and TCP.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// RunLog sends the query log to the sink until ctx ends.
func (s *Server) RunLog(ctx context.Context) { s.log.run(ctx) }

// ServeDNS implements dns.Handler.
func (s *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	start := s.opt.Clock.Now()
	s.queries.Add(1)
	_, isTCP := w.LocalAddr().(*net.TCPAddr)
	client := clientAddr(w.RemoteAddr())
	ctx, cancel := context.WithTimeout(context.Background(), 3*s.opt.Timeout)
	defer cancel()
	resp, e := s.answer(ctx, r, client)
	fitEDNS(resp, r)
	if !isTCP {
		resp.Truncate(udpSize(r))
	}
	_ = w.WriteMsg(resp)
	e.Time = start
	e.Client = client.String()
	proto := model.DnsQueryLogEntryProtocol("udp")
	if isTCP {
		proto = "tcp"
	}
	e.Protocol = &proto
	ms := float32(float64(s.opt.Clock.Now().Sub(start)) / float64(time.Millisecond))
	e.DurationMs = &ms
	if e.Name == probeName {
		return // the container's health check is not a query of a device
	}
	s.log.add(e)
}

func clientAddr(a net.Addr) netip.Addr {
	switch v := a.(type) {
	case *net.UDPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	case *net.TCPAddr:
		ip, _ := netip.AddrFromSlice(v.IP)
		return ip.Unmap()
	}
	return netip.Addr{}
}

// maxUDP caps what the proxy sends over UDP whatever the client advertises: a larger datagram is
// fragmented on the tunnel's and the veth's MTUs (the DNS flag day 2020 size).
const maxUDP = 1232

// udpSize is the largest answer the client takes over UDP: its EDNS0 size (capped), else 512.
func udpSize(r *dns.Msg) int {
	if o := r.IsEdns0(); o != nil && int(o.UDPSize()) > 512 {
		if s := int(o.UDPSize()); s < maxUDP {
			return s
		}
		return maxUDP
	}
	return dns.MinMsgSize
}

// fitEDNS makes the reply's OPT record the one this client is owed: none for a client without EDNS
// (RFC 6891: a reply has an OPT only when the query had one), ours for a client with it. DNSSEC
// records go to a client that did not set the DO bit never.
func fitEDNS(resp, r *dns.Msg) {
	resp.Extra = dropType(resp.Extra, dns.TypeOPT)
	o := r.IsEdns0()
	if o == nil {
		resp.Answer = dropType(resp.Answer, dns.TypeRRSIG)
		resp.Ns = dropType(resp.Ns, dns.TypeRRSIG, dns.TypeNSEC, dns.TypeNSEC3)
		return
	}
	resp.SetEdns0(uint16(udpSize(r)), o.Do())
	if !o.Do() {
		resp.Answer = dropType(resp.Answer, dns.TypeRRSIG)
		resp.Ns = dropType(resp.Ns, dns.TypeRRSIG, dns.TypeNSEC, dns.TypeNSEC3)
	}
}

func dropType(rrs []dns.RR, types ...uint16) []dns.RR {
	out := rrs[:0:0]
	for _, rr := range rrs {
		drop := false
		for _, t := range types {
			drop = drop || rr.Header().Rrtype == t
		}
		if !drop {
			out = append(out, rr)
		}
	}
	return out
}

// doBit reports whether the query asks for DNSSEC records.
func doBit(r *dns.Msg) bool {
	o := r.IsEdns0()
	return o != nil && o.Do()
}

const (
	staticTTL   = 60
	negativeTTL = 30
)

// answer builds the reply to one query and the log entry that describes it.
func (s *Server) answer(ctx context.Context, r *dns.Msg, client netip.Addr) (*dns.Msg, model.DnsQueryLogEntry) {
	var e model.DnsQueryLogEntry
	cfg := s.cfg.Load()
	fail := func(rcode int) *dns.Msg {
		m := new(dns.Msg)
		m.SetRcode(r, rcode)
		m.RecursionAvailable = true
		e.Rcode = dns.RcodeToString[rcode]
		return m
	}
	if len(r.Question) != 1 || r.Opcode != dns.OpcodeQuery {
		if len(r.Question) > 0 {
			e.Name, e.Type = strings.TrimSuffix(r.Question[0].Name, "."), dns.TypeToString[r.Question[0].Qtype]
		}
		return fail(dns.RcodeFormatError), e
	}
	q := r.Question[0]
	name := strings.ToLower(q.Name)
	e.Name, e.Type = strings.TrimSuffix(name, "."), dns.TypeToString[q.Qtype]
	if q.Qtype == 0 {
		e.Type = "TYPE0"
	}
	if cfg == nil {
		return fail(dns.RcodeServerFailure), e // not registered yet: nothing is answered wrongly
	}
	if q.Qclass != dns.ClassINET {
		return fail(dns.RcodeNotImplemented), e
	}
	if strings.HasSuffix(name, ".invalid.") || name == "invalid." {
		return fail(dns.RcodeNameError), e // RFC 6761: never forwarded
	}
	// the V1 networks are IPv4 only: a device that gets an IPv6 address, directly or as a SVCB/HTTPS
	// ipv6hint, would bypass the faults. What rcode an AAAA query gets still has to come from whether
	// the name itself exists (static or upstream): a missing name is NXDOMAIN, an existing one with no
	// AAAA record is NOERROR/NODATA.
	strip := cfg.StripAaaa == nil || *cfg.StripAaaa
	if m, ok := s.static(cfg, r, name, q, client); ok {
		if q.Qtype == dns.TypeAAAA && strip {
			t := true
			e.StrippedAaaa = &t
		}
		e.Rcode = dns.RcodeToString[m.Rcode]
		e.Answers = answers(m)
		return m, e
	}
	key := cacheKey(name, q.Qtype, doBit(r))
	if m, ok := s.cache.get(key, r); ok {
		c := true
		e.Cached = &c
		e.Rcode = dns.RcodeToString[m.Rcode]
		e.Answers = answers(m)
		return m, e
	}
	m, err := s.forward(ctx, cfg, r)
	if err != nil {
		s.opt.Log.Debug("no upstream answer", "name", e.Name, "error", err)
		return fail(dns.RcodeServerFailure), e
	}
	removed := false
	if strip {
		removed = stripAAAA(m)
		stripIPv6Hint(m)
	}
	if removed || (strip && q.Qtype == dns.TypeAAAA) {
		t := true
		e.StrippedAaaa = &t
	}
	m.RecursionAvailable = true
	s.cache.put(key, m)
	e.Rcode = dns.RcodeToString[m.Rcode]
	e.Answers = answers(m)
	return m, e
}

func answers(m *dns.Msg) *[]string {
	if len(m.Answer) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.Answer))
	for _, rr := range m.Answer {
		switch v := rr.(type) {
		case *dns.A:
			out = append(out, v.A.String())
		case *dns.CNAME:
			out = append(out, strings.TrimSuffix(v.Target, "."))
		default:
			out = append(out, strings.TrimPrefix(rr.String(), rr.Header().String()))
		}
	}
	return &out
}

// static answers an entry of the configuration: A queries get the addresses, every other type gets
// an empty answer (the name exists).
func (s *Server) static(cfg *model.DnsServiceConfig, r *dns.Msg, name string, q dns.Question, client netip.Addr) (*dns.Msg, bool) {
	plain := strings.TrimSuffix(name, ".")
	var found *model.DnsStaticEntry
	for _, n := range networksFor(cfg, client) {
		if n.StaticEntries == nil {
			continue
		}
		for i := range *n.StaticEntries {
			if strings.EqualFold((*n.StaticEntries)[i].Name, plain) {
				found = &(*n.StaticEntries)[i]
			}
		}
	}
	if found == nil {
		return nil, false
	}
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true
	m.Authoritative = true
	if q.Qtype == dns.TypeA || q.Qtype == dns.TypeANY {
		for _, a := range found.Addresses {
			if ip, err := netip.ParseAddr(a); err == nil && ip.Is4() {
				m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: staticTTL}, A: net.IP(ip.AsSlice())})
			}
		}
	}
	return m, true
}

// networksFor returns the networks whose client range holds the client; every network when none
// does (a client behind a router, or a range the configuration leaves out).
func networksFor(cfg *model.DnsServiceConfig, client netip.Addr) []networkView {
	var all, hit []networkView
	for _, n := range cfg.Networks {
		v := networkView{StaticEntries: n.StaticEntries}
		all = append(all, v)
		if n.Clients != nil {
			if p, err := netip.ParsePrefix(*n.Clients); err == nil && p.Contains(client) {
				hit = append(hit, v)
			}
		}
	}
	if len(hit) > 0 {
		return hit
	}
	return all
}

type networkView struct{ StaticEntries *[]model.DnsStaticEntry }

// forward asks the upstream resolvers one after the other; a truncated UDP answer is asked again
// over TCP, so the device gets the whole answer (or, over UDP, the truncation it asks for).
func (s *Server) forward(ctx context.Context, cfg *model.DnsServiceConfig, r *dns.Msg) (*dns.Msg, error) {
	if len(cfg.Upstream) == 0 {
		return nil, errNoUpstream
	}
	var last error
	for _, up := range cfg.Upstream {
		server := net.JoinHostPort(up, s.opt.UpstreamPort)
		q := r.Copy()
		q.Id = dns.Id()
		s.upstreamQueries.Add(1)
		uctx, cancel := context.WithTimeout(ctx, s.opt.Timeout)
		resp, err := s.opt.Upstream.Exchange(uctx, q, "udp", server)
		cancel()
		if err == nil && resp.Truncated {
			tctx, tcancel := context.WithTimeout(ctx, s.opt.Timeout) // the TCP attempt has a time of its own
			resp, err = s.opt.Upstream.Exchange(tctx, q, "tcp", server)
			tcancel()
		}
		if err != nil {
			last = err
			continue
		}
		resp.Id = r.Id
		return resp, nil
	}
	return nil, last
}

type proxyError string

func (e proxyError) Error() string { return string(e) }

const errNoUpstream = proxyError("no upstream resolver")

// stripAAAA removes the AAAA records of an answer; it reports whether it removed any.
func stripAAAA(m *dns.Msg) bool {
	removed := false
	filter := func(rrs []dns.RR) []dns.RR {
		out := rrs[:0:0]
		for _, rr := range rrs {
			if rr.Header().Rrtype == dns.TypeAAAA {
				removed = true
				continue
			}
			out = append(out, rr)
		}
		return out
	}
	m.Answer = filter(m.Answer)
	m.Extra = filter(m.Extra)
	return removed
}

// stripIPv6Hint removes the ipv6hint parameter from every SVCB/HTTPS record of the answer (RFC 9460):
// the V1 networks are IPv4 only, so the hint would point at an address nothing can route to.
func stripIPv6Hint(m *dns.Msg) {
	filter := func(rrs []dns.RR) {
		for _, rr := range rrs {
			var svcb *dns.SVCB
			switch v := rr.(type) {
			case *dns.SVCB:
				svcb = v
			case *dns.HTTPS:
				svcb = &v.SVCB
			default:
				continue
			}
			out := svcb.Value[:0:0]
			for _, kv := range svcb.Value {
				if kv.Key() != dns.SVCB_IPV6HINT {
					out = append(out, kv)
				}
			}
			svcb.Value = out
		}
	}
	filter(m.Answer)
	filter(m.Extra)
}

type client struct{}

func (c *client) Exchange(ctx context.Context, m *dns.Msg, network, server string) (*dns.Msg, error) {
	cl := &dns.Client{Net: network}
	resp, _, err := cl.ExchangeContext(ctx, m, server)
	return resp, err
}

const probeName = "health.invalid"

// Probe sends a query for a reserved name to the proxy at addr and succeeds when it answers in any
// way (the health check of the container: a proxy without a configuration answers SERVFAIL).
func Probe(addr string) error {
	m := new(dns.Msg)
	m.SetQuestion(probeName+".", dns.TypeA)
	c := &dns.Client{Net: "udp", Timeout: 2 * time.Second}
	_, _, err := c.Exchange(m, addr)
	return err
}
