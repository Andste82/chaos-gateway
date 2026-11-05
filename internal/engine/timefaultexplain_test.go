package engine_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func (h *harness) explainTime(dst, proto string, port int) (ex *engine.Explanation, timeWinner bool) {
	h.t.Helper()
	ex, err := h.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.31"), Dst: dst, Protocol: proto, Port: port})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, f := range ex.Faults {
		timeWinner = timeWinner || (f.Family == domain.FamilyTime && f.Winner != nil)
	}
	return ex, timeWinner
}

// The kernel redirects a query to the NTP server under a time fault only to the gateway's address and to what lies outside the
// segments of the source's network; a server in the same network is switched directly. Explain says what the kernel does: no
// NTP server and no time winner for the host in the same /24.
func TestExplainReportsNoTimeFaultForAServerInsideTheSourcesNetwork(t *testing.T) {
	h := tlsHarness(t)
	h.mustPut(alice, "target: {network: IoT}\ntime: {offset: +400d}")
	h.barrier()
	if ex, won := h.explainTime("10.10.0.5", "udp", 123); ex.Service != "none" || won {
		t.Errorf("a host in the same /24: service %q, time winner %v", ex.Service, won)
	}
	// outside the segments and the gateway's own address are redirected
	if ex, won := h.explainTime("203.0.113.5", "udp", 123); ex.Service != "ntp_server" || !won {
		t.Errorf("a server outside: service %q, time winner %v", ex.Service, won)
	}
	if ex, won := h.explainTime("10.10.0.1", "udp", 123); ex.Service != "ntp_server" || !won {
		t.Errorf("the gateway: service %q, time winner %v", ex.Service, won)
	}
	// a selector that names the host inside the network does not make the kernel redirect it
	h.mustPut(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 10.10.0.5}, offset: +1y}")
	h.barrier()
	if ex, won := h.explainTime("10.10.0.5", "udp", 123); ex.Service != "none" || won {
		t.Errorf("a selector inside the network: service %q, time winner %v", ex.Service, won)
	}
}

// A time fault redirects the gateway's own address of a network whose time service is off, too (Q3): the packet is answered
// by the server under the fault, so Explain neither drops it at the gateway's protection nor says that no service answers.
func TestExplainAllowsTheGatewayAddressOfANetworkWithoutTheTimeServiceUnderATimeFault(t *testing.T) {
	h := tlsHarness(t)
	h.mustApply(h.revision(func(c *model.Configuration) {
		n := (*c.Networks)[iotID]
		lan, _ := n.AsLanNetwork()
		lan.Ntp = &model.NetworkNtp{Enabled: ptr(false)}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
	}))
	if ex, _ := h.explainTime("10.10.0.1", "udp", 123); ex.Access.Verdict != "drop" || ex.Service != "none" {
		t.Fatalf("without a fault the gateway does not answer: access %+v, service %q", ex.Access, ex.Service)
	}
	h.mustPut(alice, "target: {network: IoT}\ntime: {offset: -90s}")
	h.barrier()
	ex, won := h.explainTime("10.10.0.1", "udp", 123)
	if ex.Access.Verdict != "allow" || ex.Service != "ntp_server" || !won {
		t.Errorf("access %+v, service %q, time winner %v", ex.Access, ex.Service, won)
	}
	// only UDP 123 is redirected: the NTS port stays with the gateway's protection
	if ex, _ := h.explainTime("10.10.0.1", "tcp", 4460); ex.Access.Verdict != "drop" {
		t.Errorf("tcp 4460: %+v", ex.Access)
	}
}
