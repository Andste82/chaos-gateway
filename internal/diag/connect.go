package diag

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// target resolves the host of the request. A name that resolves puts the address it resolved to into the result; one
// that does not ends the diagnostic with the error in the result.
func (r *Runner) target(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) (netip.Addr, bool) {
	addr, _, err := r.resolveHost(ctx, req.Host, timeout)
	if err != nil {
		res.Error = ptr(err.Error())
		return addr, false
	}
	if req.Host != addr.String() {
		res.Address = ptr(addr.String())
	}
	return addr, true
}

// tcp is the diagnostic `tcp`: a connection is made and closed again.
func (r *Runner) tcp(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) {
	addr, ok := r.target(ctx, req, timeout, res)
	if !ok {
		return
	}
	c := newOf(res.Connect)
	res.Connect = c
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t0 := r.clk.Monotonic()
	conn, err := r.dial(dctx, "tcp", addr, port(req, 0))
	rtt := r.clk.Monotonic() - t0
	if err != nil {
		c.Reachable, c.Detail = ptr(false), ptr(failure(err, timeout))
		res.Error = ptr(*c.Detail)
		return
	}
	_ = conn.Close()
	c.Reachable, c.RttMs, c.Detail = ptr(true), ptr(ms(rtt)), ptr("connected")
	res.Ok = true
}

// udp is the diagnostic `udp`: a datagram is sent and a reply awaited. A UDP port cannot be told to be open without a
// reply: a closed one answers with ICMP port unreachable, which is the only other answer there is.
func (r *Runner) udp(ctx context.Context, req *model.DiagnosticRequest, timeout time.Duration, res *model.DiagnosticResult) {
	addr, ok := r.target(ctx, req, timeout, res)
	if !ok {
		return
	}
	c := newOf(res.Connect)
	res.Connect = c
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := r.dial(dctx, "udp", addr, port(req, 0))
	if err != nil {
		c.Reachable, c.Detail = ptr(false), ptr(failure(err, timeout))
		res.Error = ptr(*c.Detail)
		return
	}
	defer func() { _ = conn.Close() }()
	dl, _ := dctx.Deadline()
	_ = conn.SetDeadline(dl)
	stop := context.AfterFunc(dctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	var payload []byte
	if req.Payload != nil {
		payload = []byte(*req.Payload)
	}
	t0 := r.clk.Monotonic()
	if _, err := conn.Write(payload); err != nil {
		c.Reachable, c.Detail = ptr(false), ptr(failure(err, timeout))
		res.Error = ptr(*c.Detail)
		return
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	rtt := r.clk.Monotonic() - t0
	switch {
	case err == nil:
		c.Reachable, c.RttMs, c.Detail = ptr(true), ptr(ms(rtt)), ptr(fmt.Sprintf("reply of %d bytes", n))
		res.Ok = true
	case isTimeout(err):
		c.Reachable = ptr(false)
		c.Detail = ptr(fmt.Sprintf("no reply within %s", timeout))
		res.Error = ptr(*c.Detail + ": the port is open or filtered, or the service ignores this datagram (a closed port answers with ICMP port unreachable)")
	default:
		c.Reachable, c.Detail = ptr(false), ptr(failure(err, timeout))
		res.Error = ptr(*c.Detail)
	}
}
