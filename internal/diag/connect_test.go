package diag

import (
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// listenTCP accepts connections on a loopback port and closes them.
func listenTCP(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", loop+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

// closedTCPPort is a port nothing listens on.
func closedTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", loop+":0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}

func TestTCPReachesAnOpenPort(t *testing.T) {
	res := run(t, runner(nil), req(model.DiagnosticRequestTypeTcp, loop, withPort(listenTCP(t))))
	if !res.Ok || res.Connect == nil || !*res.Connect.Reachable || res.Connect.RttMs == nil || *res.Connect.RttMs < 0 || res.Error != nil {
		t.Fatalf("%+v", res)
	}
	if res.Address != nil {
		t.Errorf("an address is not an address that was resolved: %s", *res.Address)
	}
	if res.DurationMs <= 0 {
		t.Errorf("duration %v", res.DurationMs)
	}
}

func TestTCPSaysWhyAPortIsNotReachable(t *testing.T) {
	res := run(t, runner(nil), req(model.DiagnosticRequestTypeTcp, loop, withPort(closedTCPPort(t))))
	if res.Ok || res.Connect == nil || *res.Connect.Reachable || *res.Connect.Detail != "connection refused" || str(res.Error) != "connection refused" {
		t.Fatalf("%+v", res)
	}
	if res.Connect.RttMs != nil {
		t.Errorf("a refused connection has no round trip time")
	}
}

func TestTCPTimesOutWhereNobodyAnswers(t *testing.T) {
	r := New(Config{Net: blackhole{}})
	res := run(t, r, req(model.DiagnosticRequestTypeTcp, "203.0.113.10", withPort(8883), withTimeout("100ms")))
	if res.Ok || *res.Connect.Detail != "timed out after 100ms" {
		t.Fatalf("%+v", res)
	}
}

func TestTCPResolvesANameThroughTheConfiguredServer(t *testing.T) {
	d := serveDNS(t, answerA(loop))
	res := run(t, runner(nil, d.Addr), req(model.DiagnosticRequestTypeTcp, "broker.test", withPort(listenTCP(t))))
	if !res.Ok || str(res.Address) != loop {
		t.Fatalf("%+v", res)
	}
	if a := d.Asked(); len(a) != 1 || a[0] != "broker.test." {
		t.Errorf("asked %v", a)
	}
}

func TestANameThatDoesNotResolveIsAResultNotAnError(t *testing.T) {
	nx := serveDNS(t, func(_ bool, q *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetRcode(q, dns.RcodeNameError)
		return m
	})
	for name, c := range map[string]struct {
		r    *Runner
		want string
	}{
		"nxdomain":    {runner(nil, nx.Addr), "cannot resolve gone.test: NXDOMAIN"},
		"no a":        {runner(nil, serveDNS(t, answerA()).Addr), "cannot resolve gone.test: the name has no A record"},
		"no server":   {runner(nil), "cannot resolve gone.test: no DNS server is configured"},
		"unreachable": {runner(nil, netip.MustParseAddrPort(loop+":"+itoa(closedTCPPort(t)))), "cannot resolve gone.test: connection refused"},
	} {
		for _, typ := range []model.DiagnosticRequestType{model.DiagnosticRequestTypeTcp, model.DiagnosticRequestTypeUdp, model.DiagnosticRequestTypePing, model.DiagnosticRequestTypeTls} {
			res := run(t, c.r, req(typ, "gone.test", withPort(1883)))
			if res.Ok || str(res.Error) != c.want {
				t.Errorf("%s %s: %+v", name, typ, res)
			}
		}
	}
}

func TestTheNextServerIsAskedOnlyWhenTheFirstCannotBeReached(t *testing.T) {
	good := serveDNS(t, answerA(loop))
	dead := netip.MustParseAddrPort(loop + ":" + itoa(closedTCPPort(t)))
	res := run(t, runner(nil, dead, good.Addr), req(model.DiagnosticRequestTypeTcp, "broker.test", withPort(listenTCP(t))))
	if !res.Ok {
		t.Errorf("a refused server is skipped: %+v", res)
	}
	// a slow or a negative answer is the answer: the second server is not asked
	silent := serveDNS(t, func(bool, *dns.Msg) *dns.Msg { return nil })
	res = run(t, runner(nil, silent.Addr, good.Addr), req(model.DiagnosticRequestTypeTcp, "broker.test", withPort(1883), withTimeout("200ms")))
	if res.Ok || !strings.Contains(str(res.Error), "timed out after 200ms") || len(good.Asked()) != 1 {
		t.Errorf("%+v asked %v", res, good.Asked())
	}
}

// ---- UDP

func echoUDP(t *testing.T, reply bool) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", loop+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply {
				_, _ = pc.WriteTo(buf[:n], from)
			}
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func TestUDPNeedsAReply(t *testing.T) {
	payload := "ping"
	res := run(t, runner(nil), req(model.DiagnosticRequestTypeUdp, loop, withPort(echoUDP(t, true)), func(r *model.DiagnosticRequest) { r.Payload = &payload }))
	if !res.Ok || !*res.Connect.Reachable || *res.Connect.Detail != "reply of 4 bytes" || res.Connect.RttMs == nil {
		t.Fatalf("%+v", res)
	}
}

func TestUDPKnowsAClosedPortFromTheICMPErrorAndASilentOneFromTheTimeout(t *testing.T) {
	pc, err := net.ListenPacket("udp", loop+":0")
	if err != nil {
		t.Fatal(err)
	}
	closed := pc.LocalAddr().(*net.UDPAddr).Port
	_ = pc.Close()
	res := run(t, runner(nil), req(model.DiagnosticRequestTypeUdp, loop, withPort(closed), withTimeout("1s")))
	if res.Ok || *res.Connect.Reachable || *res.Connect.Detail != "connection refused" {
		t.Errorf("closed: %+v", res)
	}
	res = run(t, runner(nil), req(model.DiagnosticRequestTypeUdp, loop, withPort(echoUDP(t, false)), withTimeout("150ms")))
	if res.Ok || *res.Connect.Reachable || *res.Connect.Detail != "no reply within 150ms" || !strings.Contains(str(res.Error), "open or filtered") {
		t.Errorf("silent: %+v", res)
	}
}
