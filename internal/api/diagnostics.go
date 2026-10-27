package api

import (
	"context"
	"crypto/x509"
	"net/netip"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/diag"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// MaxDiagnostics is how many diagnostics run at once: each holds a request and sockets for up to a minute.
const MaxDiagnostics = 8

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
	if req.From != nil && req.From.Probe != nil {
		s.write(c, newProblem(model.ErrorCodeUnsupportedFeature, "a diagnostic from a probe is not available in this build; probes arrive with milestone M25 (see GET /capabilities)"))
		return
	}
	select {
	case s.diagnostics.running <- struct{}{}:
		defer func() { <-s.diagnostics.running }()
	default:
		s.write(c, newProblem(model.ErrorCodeCapacityExceeded, "%d diagnostics run already: wait for one to end", MaxDiagnostics))
		return
	}
	ctx, cancel := context.WithTimeout(contextOf(c), domain.MaxDiagnosticDuration)
	defer cancel()
	res, err := s.diagnostics.runner.Run(ctx, *req)
	if err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "diagnostic.run", nil, 0, diagnosticDetail(req, res))
	c.JSON(200, res)
}

// diagnosticDetail is the line the audit log keeps of a diagnostic.
func diagnosticDetail(req *model.DiagnosticRequest, res model.DiagnosticResult) string {
	what := string(req.Type) + " " + req.Host
	if req.Port != nil {
		what += ":" + strconv.Itoa(*req.Port)
	}
	if res.Ok {
		return what + " ok"
	}
	return what + " failed"
}
