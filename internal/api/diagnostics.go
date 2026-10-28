package api

import (
	"context"
	"crypto/x509"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/diag"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// MaxDiagnostics is how many diagnostics run at once: each holds a request and sockets for up to a minute.
const MaxDiagnostics = 8

// ActionDiagnosticRun is the audit action of a diagnostic: the decision P6-M24-01 lets the scope `overlays` point it at
// any destination, so every run is recorded. A caller can make them in numbers, so the audit log drops them first when
// it is full (audit.WithBulkActions) and a principal may start DiagnosticRate of them per second, DiagnosticBurst at once
// (P6-AUDIT-15).
const ActionDiagnosticRun = "diagnostic.run"

// The rate at which one principal may start diagnostics.
const (
	DiagnosticRate  = 5
	DiagnosticBurst = 20
)

// DiagnosticCeiling is how long a diagnostic may take, whatever its timeouts add up to (a variable for the tests, as
// ReadTimeout is).
var DiagnosticCeiling = domain.MaxDiagnosticDuration

// DiagnosticsConfig is what the diagnostics (M24) are set up with; the zero value is the production setup.
type DiagnosticsConfig struct {
	// Net is the stack the diagnostics run on: nil is the gateway's own, in Config.Namespace.
	Net diag.Net
	// Proxy is the DNS server a `dns` diagnostic without a server asks: the zero value is the DNS proxy in the
	// service namespace.
	Proxy netip.AddrPort
	// Servers returns the servers that resolve a host name, in the order they are asked: nil is the DNS proxy, then
	// the uplink's resolvers.
	Servers func() []netip.AddrPort
}

// diagnostics is the runner of the diagnostics with the roots of the test CA, made once.
type diagnostics struct {
	runner  *diag.Runner
	sysMu   sync.Once
	sys     *x509.CertPool
	running chan struct{}
	rate    runRate
}

// runRate is a token bucket per principal: DiagnosticBurst places, DiagnosticRate more every second.
type runRate struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Duration
}

// take uses one place of the principal's bucket. If there is none it reports how long until there is one.
func (r *runRate) take(key string, now time.Duration) (wait time.Duration, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buckets == nil {
		r.buckets = map[string]*bucket{}
	}
	if len(r.buckets) > 256 {
		// a bucket that has filled up again is the state of a principal that never ran: forget it
		for k, b := range r.buckets {
			if float64(DiagnosticBurst) <= b.tokens+(now-b.last).Seconds()*DiagnosticRate {
				delete(r.buckets, k)
			}
		}
	}
	b := r.buckets[key]
	if b == nil {
		b = &bucket{tokens: DiagnosticBurst, last: now}
		r.buckets[key] = b
	}
	b.tokens = min(DiagnosticBurst, b.tokens+(now-b.last).Seconds()*DiagnosticRate)
	b.last = now
	if b.tokens < 1 {
		return time.Duration((1 - b.tokens) / DiagnosticRate * float64(time.Second)), false
	}
	b.tokens--
	return 0, true
}

// principalKey tells the principals apart: a token by its id, a session by its.
func principalKey(p *Principal) string {
	if p == nil {
		return ""
	}
	return p.Kind + ":" + p.TokenID + p.SessionID
}

func (s *Server) initDiagnostics() {
	d := &diagnostics{running: make(chan struct{}, MaxDiagnostics)}
	dc := s.cfg.Diagnostics
	proxy := dc.Proxy
	if !proxy.IsValid() {
		proxy = netip.AddrPortFrom(compiler.ServicePeerCIDR.Addr(), 53)
	}
	servers := dc.Servers
	if servers == nil {
		servers = func() []netip.AddrPort { return append([]netip.AddrPort{proxy}, s.uplinkResolvers()...) }
	}
	network := dc.Net
	if network == nil {
		network = diag.HostNet{Namespace: s.cfg.Namespace}
	}
	d.runner = diag.New(diag.Config{
		Net: network, Resolvers: servers, ProxyServer: proxy, Roots: s.diagnosticRoots(d),
		Now: s.clk.Now, UserAgent: "chaos-gateway/" + s.cfg.Version,
	})
	s.diagnostics = d
}

// uplinkResolvers are the servers the DNS proxy asks itself: those the uplink names, else the host's.
func (s *Server) uplinkResolvers() []netip.AddrPort {
	var names []string
	if snap := s.cfg.Engine.Snapshot(); snap.Config != nil && snap.Config.Uplink.DnsUpstream != nil {
		names = *snap.Config.Uplink.DnsUpstream
	}
	if len(names) == 0 {
		names = s.hostResolvers()
	}
	var out []netip.AddrPort
	for _, n := range names {
		if a, err := netip.ParseAddr(n); err == nil && a.Is4() {
			out = append(out, netip.AddrPortFrom(a, 53))
		}
	}
	return out
}

// diagnosticRoots returns the function that builds the roots a certificate is verified with: the system's, and the
// test CA of the TLS responder (M21) when there is one, which signs the certificates of the cases that are meant to
// fail for a reason other than an unknown CA. It is read at every call: the CA can be replaced (M22).
func (s *Server) diagnosticRoots(d *diagnostics) func() *x509.CertPool {
	return func() *x509.CertPool {
		d.sysMu.Do(func() {
			if p, err := x509.SystemCertPool(); err == nil {
				d.sys = p
			} else {
				d.sys = x509.NewCertPool()
			}
		})
		pool := d.sys.Clone()
		if s.cfg.TLSCADir != "" {
			if pem, err := tlsresponder.TestCAPEM(s.cfg.TLSCADir); err == nil {
				pool.AppendCertsFromPEM(pem)
			}
		}
		return pool
	}
}

// RunDiagnostic implements POST /diagnostics: one diagnostic from the gateway, answered when it is done.
func (s *Server) RunDiagnostic(c *gin.Context) {
	raw, rerr := readBody(c)
	if rerr != nil {
		s.write(c, rerr)
		return
	}
	req, err := domain.DecodeDiagnosticRequest(raw)
	if err != nil {
		s.fail(c, err)
		return
	}
	select {
	case s.diagnostics.running <- struct{}{}:
		defer func() { <-s.diagnostics.running }()
	default:
		s.write(c, newProblem(model.ErrorCodeCapacityExceeded, "%d diagnostics run already: wait for one to end", MaxDiagnostics))
		return
	}
	if wait, ok := s.diagnostics.rate.take(principalKey(principalOf(c)), s.clk.Monotonic()); !ok {
		secs := int(wait.Seconds()) + 1
		s.write(c, newProblem(model.ErrorCodeRateLimited, "at most %d diagnostics a second (%d at once); try again in %d seconds", DiagnosticRate, DiagnosticBurst, secs).
			withHeader("Retry-After", strconv.Itoa(secs)))
		return
	}
	ctx, cancel := context.WithTimeout(contextOf(c), DiagnosticCeiling)
	defer cancel()
	var res model.DiagnosticResult
	if req.From != nil && req.From.Probe != nil {
		var perr *problem
		if res, perr = s.runProbeDiagnostic(ctx, req, *req.From.Probe); perr != nil {
			s.write(c, perr)
			return
		}
	} else if res, err = s.diagnostics.runner.Run(ctx, *req); err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, ActionDiagnosticRun, nil, 0, diagnosticDetail(req, res))
	c.JSON(200, res)
}

// maxDetailPart bounds each part of a request that the audit line quotes.
const maxDetailPart = 200

// diagnosticDetail is the line the audit log keeps of a diagnostic: what was really contacted (an `http` run with a
// `url` ignores `host` and `port`), where it ran from, and how it ended.
func diagnosticDetail(req *model.DiagnosticRequest, res model.DiagnosticResult) string {
	var what string
	switch {
	case req.Type == model.DiagnosticRequestTypeHttp:
		what = "http " + httpTarget(req)
	case req.Type == model.DiagnosticRequestTypeDns && req.DnsName != nil:
		what = "dns " + quoted(string(*req.DnsName)) + " @" + hostPort(req)
	default:
		what = string(req.Type) + " " + hostPort(req)
	}
	if req.Type == model.DiagnosticRequestTypeUdp && req.Payload != nil {
		what += " payload " + strconv.Itoa(len(*req.Payload)) + " bytes"
	}
	if res.Address != nil {
		what += " (" + *res.Address + ")"
	}
	if req.From != nil && req.From.Probe != nil {
		what += " from probe " + quoted(string(*req.From.Probe))
	}
	if res.Ok {
		return what + " ok"
	}
	return what + " failed"
}

func hostPort(req *model.DiagnosticRequest) string {
	if req.Port != nil {
		return req.Host + ":" + strconv.Itoa(*req.Port)
	}
	return req.Host
}

// httpTarget is the URL an `http` run asks: scheme, host, port and path, without a query (it may carry a secret).
func httpTarget(req *model.DiagnosticRequest) string {
	if req.Url == nil {
		return "http://" + hostPort(req) + "/"
	}
	u, err := url.Parse(*req.Url)
	if err != nil {
		return quoted(*req.Url)
	}
	t := u.Scheme + "://" + u.Host + u.EscapedPath()
	if u.RawQuery != "" {
		t += "?..."
	}
	return cut(t, maxDetailPart)
}

// quoted is a value of the request in the audit line: cut, and free of anything that could pass for the end of it.
func quoted(v string) string { return strconv.Quote(cut(v, maxDetailPart)) }

func cut(v string, n int) string {
	if len(v) > n {
		return v[:n] + "..."
	}
	return v
}
