package diag

import (
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func dnsReq(host, name string, port int) model.DiagnosticRequest {
	return req(model.DiagnosticRequestTypeDns, host, withPort(port), func(r *model.DiagnosticRequest) { r.DnsName = &name })
}

func TestDNSAsksTheServerTheHostNames(t *testing.T) {
	d := serveDNS(t, answerA("203.0.113.10", "203.0.113.11"))
	res := run(t, runner(nil), dnsReq(loop, "broker.example.com", int(d.Addr.Port())))
	if !res.Ok || res.Dns == nil || *res.Dns.Rcode != "NOERROR" || *res.Dns.Server != d.Addr.String() || *res.Dns.Name != "broker.example.com" {
		t.Fatalf("%+v", res)
	}
	if got := strings.Join(*res.Dns.Answers, ","); got != "203.0.113.10,203.0.113.11" {
		t.Errorf("answers %s", got)
	}
	if res.Dns.RttMs == nil || *res.Dns.RttMs < 0 {
		t.Errorf("rtt %v", res.Dns.RttMs)
	}
}

func TestDNSWithoutANameAsksTheGatewaysProxyForTheHost(t *testing.T) {
	d := serveDNS(t, answerA("10.10.0.20"))
	r := New(Config{ProxyServer: d.Addr})
	res := run(t, r, req(model.DiagnosticRequestTypeDns, "sensor.lab"))
	if !res.Ok || *res.Dns.Name != "sensor.lab" || *res.Dns.Server != d.Addr.String() {
		t.Fatalf("%+v", res)
	}
	// the proxy's address is not known (no service namespace): a sentence, not a guess
	res = run(t, runner(nil), req(model.DiagnosticRequestTypeDns, "sensor.lab"))
	if res.Ok || !strings.Contains(str(res.Error), "DNS proxy is not known") {
		t.Errorf("%+v", res)
	}
}

func TestDNSServerGivenByNameIsResolvedFirst(t *testing.T) {
	resolver := serveDNS(t, answerA(loop))
	target := serveDNS(t, answerA("203.0.113.10"))
	res := run(t, runner(nil, resolver.Addr), dnsReq("ns.test", "broker.example.com", int(target.Addr.Port())))
	if !res.Ok || str(res.Address) != loop || *res.Dns.Server != target.Addr.String() {
		t.Fatalf("%+v", res)
	}
}

func TestDNSFailuresAreResults(t *testing.T) {
	reply := func(rcode int) func(bool, *dns.Msg) *dns.Msg {
		return func(_ bool, q *dns.Msg) *dns.Msg {
			m := new(dns.Msg)
			m.SetRcode(q, rcode)
			return m
		}
	}
	for name, c := range map[string]struct {
		handler func(bool, *dns.Msg) *dns.Msg
		rcode   string
		err     string
	}{
		"nxdomain":  {reply(dns.RcodeNameError), "NXDOMAIN", "NXDOMAIN"},
		"servfail":  {reply(dns.RcodeServerFailure), "SERVFAIL", "SERVFAIL"},
		"refused":   {reply(dns.RcodeRefused), "REFUSED", "REFUSED"},
		"no record": {answerA(), "NOERROR", "NOERROR, but no A record in the answer"},
	} {
		d := serveDNS(t, c.handler)
		res := run(t, runner(nil), dnsReq(loop, "x.example.com", int(d.Addr.Port())))
		if res.Ok || *res.Dns.Rcode != c.rcode || str(res.Error) != c.err {
			t.Errorf("%s: %+v / %s", name, res, str(res.Error))
		}
	}
}

func TestDNSTimeoutAndUnreachableServer(t *testing.T) {
	silent := serveDNS(t, func(bool, *dns.Msg) *dns.Msg { return nil })
	short := dnsReq(loop, "x.example.com", int(silent.Addr.Port()))
	to := "150ms"
	short.Timeout = &to
	res := run(t, runner(nil), short)
	if res.Ok || res.Dns == nil || res.Dns.Rcode != nil || str(res.Error) != silent.Addr.String()+" did not answer within 150ms" {
		t.Errorf("silent: %+v / %s", res, str(res.Error))
	}
	gone := run(t, runner(nil), dnsReq(loop, "x.example.com", closedTCPPort(t)))
	if gone.Ok || str(gone.Error) != "connection refused" {
		t.Errorf("unreachable: %+v / %s", gone, str(gone.Error))
	}
}

func TestDNSAnswerWithACNAMEShowsTheChain(t *testing.T) {
	d := serveDNS(t, func(_ bool, q *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(q)
		m.Answer = []dns.RR{
			&dns.CNAME{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60}, Target: "lb.example.com."},
			&dns.A{Hdr: dns.RR_Header{Name: "lb.example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("203.0.113.10").To4()},
		}
		return m
	})
	res := run(t, runner(nil), dnsReq(loop, "broker.example.com", int(d.Addr.Port())))
	if !res.Ok || strings.Join(*res.Dns.Answers, ",") != "CNAME lb.example.com,203.0.113.10" {
		t.Errorf("%+v", res)
	}
}

func TestDNSRetriesATruncatedAnswerOverTCP(t *testing.T) {
	d := serveDNS(t, func(udp bool, q *dns.Msg) *dns.Msg {
		if udp {
			m := new(dns.Msg)
			m.SetReply(q)
			m.Truncated = true
			return m
		}
		return answerA("203.0.113.10")(udp, q)
	})
	res := run(t, runner(nil), dnsReq(loop, "big.example.com", int(d.Addr.Port())))
	if !res.Ok || strings.Join(*res.Dns.Answers, ",") != "203.0.113.10" {
		t.Fatalf("%+v", res)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.udp != 1 || d.tcp != 1 {
		t.Errorf("udp %d tcp %d", d.udp, d.tcp)
	}
}

func TestDNSIgnoresAnAnswerToAnotherQuestion(t *testing.T) {
	d := serveDNS(t, func(_ bool, q *dns.Msg) *dns.Msg {
		m := answerA("203.0.113.10")(true, q)
		m.Id++
		return m
	})
	res := run(t, runner(nil), dnsReq(loop, "x.example.com", int(d.Addr.Port())))
	if res.Ok || !strings.Contains(str(res.Error), "does not belong to the question") {
		t.Errorf("%+v", res)
	}
}
