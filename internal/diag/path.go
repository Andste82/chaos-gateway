package diag

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// path is the diagnostic `path`: DNS, route, TCP and TLS one after the other, each reported, up to the first one that
// fails ("DNS ✓  Route ✓  TCP ✓  TLS ✗ (certificate expired)", plan §2.12). Each step has the timeout to itself.
func (r *Runner) path(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) {
	steps := newOf(res.Path)
	res.Path = steps
	*steps = emptyOf(steps)
	step := func(name model.DiagnosticResultPathStep, ok bool, detail string) bool {
		e := elemOf(steps)
		e.Step, e.Ok, e.Detail = name, ok, ptr(detail)
		*steps = append(*steps, e)
		if !ok {
			res.Error = ptr(string(name) + ": " + detail)
		}
		return ok
	}

	// dns
	addr, lk, err := r.resolveHost(ctx, req.Host, timeout)
	switch {
	case err != nil:
		step(model.DiagnosticResultPathStepDns, false, err.Error())
		return
	case lk == nil:
		step(model.DiagnosticResultPathStepDns, true, req.Host+" is an address, there is nothing to resolve")
	default:
		res.Address = ptr(addr.String())
		step(model.DiagnosticResultPathStepDns, true, fmt.Sprintf("%s is %s (asked %s, %s ms)", req.Host, addr, lk.Server, fmtMs(lk.RTT)))
	}

	// route: a datagram socket that is connected sends nothing, but the kernel has to find the way to the address
	rconn, err := r.dial(ctx, "udp", addr, port(req, 443))
	if err != nil {
		step(model.DiagnosticResultPathStepRoute, false, failure(err, timeout))
		return
	}
	local := rconn.LocalAddr().String()
	if ua, ok := rconn.LocalAddr().(*net.UDPAddr); ok {
		local = ua.IP.String()
	}
	_ = rconn.Close()
	step(model.DiagnosticResultPathStepRoute, true, fmt.Sprintf("a route to %s exists, the packets leave from %s", addr, local))

	// tcp
	dctx, cancel := context.WithTimeout(ctx, timeout)
	t0 := r.clk.Monotonic()
	conn, err := r.dial(dctx, "tcp", addr, port(req, 443))
	rtt := r.clk.Monotonic() - t0
	cancel()
	if err != nil {
		step(model.DiagnosticResultPathStepTcp, false, failure(err, timeout))
		return
	}
	defer func() { _ = conn.Close() }()
	step(model.DiagnosticResultPathStepTcp, true, fmt.Sprintf("connected to port %d in %s ms", port(req, 443), fmtMs(rtt)))

	// tls
	if req.Tls != nil && !*req.Tls {
		res.Ok = true
		return
	}
	sni, verify := serverNames(req)
	info, err := r.handshake(ctx, conn, sni, verify, timeout)
	if err != nil {
		step(model.DiagnosticResultPathStepTls, false, "handshake failed: "+failure(err, timeout))
		return
	}
	fillTLS(res, info)
	if !info.Verified {
		step(model.DiagnosticResultPathStepTls, false, info.VerifyError)
		return
	}
	step(model.DiagnosticResultPathStepTls, true, info.Version+", "+info.Cipher)
	res.Ok = true
}

func fmtMs(d time.Duration) string { return fmt.Sprintf("%.1f", float64(d)/float64(time.Millisecond)) }
