// Package diag runs the diagnostics of plan §2.12 (M24) from the gateway's own network stack: ping, a TCP or UDP
// check, a DNS lookup, an HTTP(S) request, the details of a TLS handshake, and the path that strings DNS, route, TCP and
// TLS together. Each returns a structured result; a target that fails is a result with `ok: false`, not an error.
// Traceroute, path MTU and iperf3 follow after V1.
package diag

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Config is what a Runner works with.
type Config struct {
	// Net is the stack the sockets are made on; nil is the process's own.
	Net Net
	// Resolvers returns the DNS servers that resolve a name, in the order they are asked: a server is left out for
	// the next one only when it cannot be reached at all (nothing listens, no route), never for a slow or a negative
	// answer, which is the answer. Nil: none, and a name cannot be resolved.
	Resolvers func() []netip.AddrPort
	// ProxyServer is the server a `dns` diagnostic without a server asks: the gateway's DNS proxy.
	ProxyServer netip.AddrPort
	// Roots returns the certificates that verify a server's certificate; nil is the system's.
	Roots func() *x509.CertPool
	// Now is the time a certificate is verified at and `started_at` is taken from; nil is the wall clock.
	Now func() time.Time
	// UserAgent is the User-Agent of the HTTP requests.
	UserAgent string
}

// Runner runs diagnostics. It keeps no state, so any number can run at once.
type Runner struct {
	cfg Config
	net Net
	// clk times the sockets: the real clock, whatever the clock of the caller is (a deadline is the kernel's)
	clk clock.Clock
}

// New creates a Runner.
func New(cfg Config) *Runner {
	r := &Runner{cfg: cfg, net: cfg.Net, clk: &clock.Real{}}
	if r.net == nil {
		r.net = HostNet{}
	}
	if cfg.Now == nil {
		r.cfg.Now = r.clk.Now
	}
	if cfg.UserAgent == "" {
		r.cfg.UserAgent = "chaos-gateway-diagnostics"
	}
	return r
}

// ErrUnsupported is returned for a request the gateway cannot run: a type this build does not have.
var ErrUnsupported = errors.New("diag: unsupported diagnostic")

// Run runs the diagnostic the request names (validated by domain.ValidateDiagnosticRequest) and returns its result. It
// ends with ctx; what is not finished by then fails like a timeout.
func (r *Runner) Run(ctx context.Context, req model.DiagnosticRequest) (model.DiagnosticResult, error) {
	start := r.cfg.Now()
	t0 := r.clk.Monotonic()
	res := model.DiagnosticResult{Type: string(req.Type), From: ptr("gateway"), StartedAt: start.UTC()}
	timeout := domain.DiagnosticTimeout(&req)
	switch req.Type {
	case model.DiagnosticRequestTypePing:
		r.ping(ctx, &req, timeout, &res)
	case model.DiagnosticRequestTypeTcp:
		r.tcp(ctx, &req, timeout, &res)
	case model.DiagnosticRequestTypeUdp:
		r.udp(ctx, &req, timeout, &res)
	case model.DiagnosticRequestTypeDns:
		r.dnsLookup(ctx, &req, timeout, &res)
	case model.DiagnosticRequestTypeHttp:
		r.httpGet(ctx, &req, timeout, &res)
	case model.DiagnosticRequestTypeTls:
		r.tlsDetails(ctx, &req, timeout, &res)
	case model.DiagnosticRequestTypePath:
		r.path(ctx, &req, timeout, &res)
	default:
		return res, fmt.Errorf("%w: %s", ErrUnsupported, req.Type)
	}
	res.DurationMs = ms(r.clk.Monotonic() - t0)
	return res, nil
}

func ptr[T any](v T) *T { return &v }

// ms is a duration in milliseconds, to a microsecond.
func ms(d time.Duration) float32 {
	return float32(math.Round(float64(d)/float64(time.Microsecond)) / 1000)
}

// port is the request's port or the default.
func port(req *model.DiagnosticRequest, def int) int {
	if req.Port != nil {
		return *req.Port
	}
	return def
}

// literal reports whether the host is an IPv4 address.
func literal(host string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(host)
	return a, err == nil && a.Is4()
}

// failure is a connection or request error in a sentence: the kernel's words for a socket error, the time for a
// timeout.
func failure(err error, timeout time.Duration) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return fmt.Sprintf("timed out after %s", timeout)
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return strings.TrimPrefix(oe.Err.Error(), "x509: ")
	}
	return err.Error()
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// unreachable reports whether err says nobody can be asked there at all: nothing listens, there is no route.
func unreachable(err error) bool {
	for _, e := range []error{syscall.ECONNREFUSED, syscall.ENETUNREACH, syscall.EHOSTUNREACH} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
