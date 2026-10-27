package diag

import (
	"context"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func pathReq(host string, port int, mods ...func(*model.DiagnosticRequest)) model.DiagnosticRequest {
	return req(model.DiagnosticRequestTypePath, host, append([]func(*model.DiagnosticRequest){withPort(port)}, mods...)...)
}

// steps is the path of a result as "dns:ok route:ok ..." for a comparison.
func steps(t *testing.T, res model.DiagnosticResult) string {
	t.Helper()
	if res.Path == nil {
		t.Fatalf("no path: %+v", res)
	}
	var out []string
	for _, s := range *res.Path {
		mark := "ok"
		if !s.Ok {
			mark = "FAILED"
		}
		out = append(out, string(s.Step)+":"+mark)
	}
	return strings.Join(out, " ")
}

func TestPathRunsEveryStepAndReportsEach(t *testing.T) {
	ca := newPKI(t, "Test CA")
	srv := serveTLS(t, ca.good(t, "broker.test"), 0)
	d := serveDNS(t, answerA(loop))
	res := run(t, runner(ca.roots, d.Addr), pathReq("broker.test", srv.Port))
	if !res.Ok || steps(t, res) != "dns:ok route:ok tcp:ok tls:ok" || res.Error != nil || str(res.Address) != loop {
		t.Fatalf("%s %+v", steps(t, res), res)
	}
	details := *res.Path
	if !strings.Contains(*details[0].Detail, "broker.test is "+loop) || !strings.Contains(*details[1].Detail, "packets leave from "+loop) ||
		!strings.Contains(*details[2].Detail, "connected to port") || !strings.HasPrefix(*details[3].Detail, "TLS 1.") {
		t.Errorf("details: %q", []string{*details[0].Detail, *details[1].Detail, *details[2].Detail, *details[3].Detail})
	}
	if res.Tls == nil || !*res.Tls.Verified {
		t.Errorf("the TLS details belong to the path: %+v", res.Tls)
	}
}

func TestPathOfAnAddressHasNothingToResolve(t *testing.T) {
	ca := newPKI(t, "Test CA")
	srv := serveTLS(t, ca.good(t, "broker.test"), 0)
	res := run(t, runner(ca.roots), pathReq(loop, srv.Port))
	if !res.Ok || !strings.Contains(*(*res.Path)[0].Detail, "nothing to resolve") || res.Address != nil {
		t.Errorf("%+v", res)
	}
}

func TestPathStopsAtTheFirstStepThatFails(t *testing.T) {
	ca := newPKI(t, "Test CA")
	now := time.Now()
	expired := serveTLS(t, ca.leaf(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour), "broker.test"), 0)
	d := serveDNS(t, answerA(loop))
	nx := runner(ca.roots)

	cases := map[string]struct {
		r     *Runner
		req   model.DiagnosticRequest
		steps string
		err   string
	}{
		"tls": {runner(ca.roots, d.Addr), pathReq("broker.test", expired.Port), "dns:ok route:ok tcp:ok tls:FAILED", "tls: certificate has expired"},
		"tcp": {runner(ca.roots, d.Addr), pathReq("broker.test", closedTCPPort(t)), "dns:ok route:ok tcp:FAILED", "tcp: connection refused"},
		"dns": {nx, pathReq("broker.test", 8883), "dns:FAILED", "dns: cannot resolve broker.test: no DNS server is configured"},
	}
	for name, c := range cases {
		res := run(t, c.r, c.req)
		if res.Ok || steps(t, res) != c.steps || str(res.Error) != c.err {
			t.Errorf("%s: %s / %s", name, steps(t, res), str(res.Error))
		}
	}
	// the details of the certificate are there for the step that failed
	res := run(t, runner(ca.roots, d.Addr), pathReq("broker.test", expired.Port))
	if res.Tls == nil || *res.Tls.Verified || str(res.Tls.VerifyError) != "certificate has expired" {
		t.Errorf("%+v", res.Tls)
	}
}

func TestPathWithoutTLSEndsAtTCP(t *testing.T) {
	no := false
	res := run(t, runner(nil), pathReq(loop, listenTCP(t), func(r *model.DiagnosticRequest) { r.Tls = &no }))
	if !res.Ok || steps(t, res) != "dns:ok route:ok tcp:ok" {
		t.Errorf("%s %+v", steps(t, res), res)
	}
}

// noRoute is a stack with no way to the address: a datagram socket cannot even be connected.
type noRoute struct{ HostNet }

func (noRoute) DialContext(_ context.Context, network, addr string) (net.Conn, error) {
	return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}
}

func TestPathFindsNoRouteWhereThereIsNone(t *testing.T) {
	res := run(t, New(Config{Net: noRoute{}}), pathReq("198.51.100.7", 8883))
	if res.Ok || steps(t, res) != "dns:ok route:FAILED" || str(res.Error) != "route: network is unreachable" {
		t.Errorf("%s / %s", steps(t, res), str(res.Error))
	}
}
