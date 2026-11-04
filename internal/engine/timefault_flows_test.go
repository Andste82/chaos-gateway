package engine_test

import (
	"context"
	"testing"
)

// A flow that the kernel redirected into the service namespace carries the service that answers it, told by the port the reply
// comes from: the DNS proxy's, or one of the NTP server's (123, a port of a destination selector, NTS key establishment).
func TestFlowsRedirectedToTheNTPServerCarryItsService(t *testing.T) {
	h := tlsHarness(t)
	h.k.SetConntrack(
		"udp      17 25 src=10.10.0.31 dst=203.0.113.50 sport=3 dport=53 packets=1 bytes=60 src=169.254.100.2 dst=10.10.0.31 sport=53 dport=3 packets=1 bytes=90 [ASSURED] mark=0 use=1\n" +
			"udp      17 25 src=10.10.0.31 dst=203.0.113.51 sport=4 dport=123 packets=1 bytes=76 src=169.254.100.2 dst=10.10.0.31 sport=123 dport=4 packets=1 bytes=76 [ASSURED] mark=0 use=1\n" +
			"udp      17 25 src=10.10.0.31 dst=203.0.113.52 sport=5 dport=123 packets=1 bytes=76 src=169.254.100.2 dst=10.10.0.31 sport=12304 dport=5 packets=1 bytes=76 [ASSURED] mark=0 use=1\n" +
			"tcp      6 25 SYN_SENT src=10.10.0.31 dst=203.0.113.53 sport=6 dport=4460 packets=1 bytes=60 src=169.254.100.2 dst=10.10.0.31 sport=4460 dport=6 packets=1 bytes=60 mark=0 use=1\n" +
			"udp      17 25 src=10.10.0.31 dst=203.0.113.54 sport=7 dport=123 packets=1 bytes=76 src=203.0.113.54 dst=10.10.0.31 sport=123 dport=7 packets=1 bytes=76 [ASSURED] mark=0 use=1\n")
	flows, err := h.e.Flows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range flows {
		got[f.Dst.String()] = f.Service
	}
	want := map[string]string{
		"203.0.113.50": "dns_proxy",
		"203.0.113.51": "ntp_server",
		"203.0.113.52": "ntp_server",
		"203.0.113.53": "ntp_server",
		// the reply comes from the server asked: not redirected
		"203.0.113.54": "",
	}
	for dst, svc := range want {
		if s, ok := got[dst]; !ok || s != svc {
			t.Errorf("%s: service %q (listed %v), want %q", dst, s, ok, svc)
		}
	}
}
