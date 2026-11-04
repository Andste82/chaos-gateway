package engine_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Tracked flows of the device: a poll to a server on the NTP port that was not redirected, a poll that was redirected into the
// server (to 123 and to the port of a selector), a flow of the DNS proxy, a TCP flow to a port that is no NTP, and a UDP flow
// to some other port.
const ntpFlowsText = "udp      17 25 src=10.10.0.31 dst=203.0.113.9 sport=123 dport=123 packets=2 bytes=152 src=203.0.113.9 dst=10.10.0.31 sport=123 dport=123 packets=2 bytes=152 [ASSURED] mark=0 use=1\n" +
	"udp      17 25 src=10.10.0.31 dst=203.0.113.10 sport=40001 dport=123 packets=2 bytes=152 src=169.254.100.2 dst=10.10.0.31 sport=123 dport=40001 packets=2 bytes=152 [ASSURED] mark=0 use=1\n" +
	"udp      17 25 src=10.10.0.31 dst=203.0.113.11 sport=40002 dport=5123 packets=2 bytes=152 src=169.254.100.2 dst=10.10.0.31 sport=12301 dport=40002 packets=2 bytes=152 [ASSURED] mark=0 use=1\n" +
	"udp      17 25 src=10.10.0.31 dst=203.0.113.12 sport=40003 dport=53 packets=1 bytes=60 src=169.254.100.2 dst=10.10.0.31 sport=53 dport=40003 packets=1 bytes=90 [ASSURED] mark=0 use=1\n" +
	"tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.13 sport=40004 dport=443 packets=6 bytes=412 src=203.0.113.13 dst=10.10.0.31 sport=443 dport=40004 packets=4 bytes=500 [ASSURED] mark=0 use=1\n" +
	"udp      17 25 src=10.10.0.31 dst=203.0.113.14 sport=40005 dport=9999 packets=1 bytes=60 src=203.0.113.14 dst=10.10.0.31 sport=9999 dport=40005 packets=1 bytes=60 [ASSURED] mark=0 use=1\n"

func (h *harness) trackedDestinations() string {
	h.t.Helper()
	flows, err := h.e.Flows(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, f := range flows {
		out = append(out, f.Dst.String())
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// The redirect is NAT, and a flow keeps the decision of its first packet: a device that keeps its source port and polls
// every few seconds would go on to the real server when a fault arrives, and to the server when the fault ends. So an apply
// that changes the redirects of NTP deletes the tracked NTP flows (the flows to an NTP port and the ones redirected into the
// server), and an apply that leaves them alone deletes nothing.
func TestAChangeOfTheNTPRedirectsDeletesTheTrackedNTPFlows(t *testing.T) {
	h := tlsHarness(t)
	h.k.SetConntrack(ntpFlowsText)
	all := "203.0.113.10 203.0.113.11 203.0.113.12 203.0.113.13 203.0.113.14 203.0.113.9"
	if got := h.trackedDestinations(); got != all {
		t.Fatalf("flows before: %s", got)
	}

	// an overlay that does not touch the NTP redirects (a latency fault of the network) deletes nothing
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 100ms}")
	h.barrier()
	if got := h.trackedDestinations(); got != all {
		t.Errorf("a fault of another family deleted flows: %s", got)
	}

	// a time fault arrives: the redirect rule is new, and the NTP flows are gone (203.0.113.9, .10 and .11), the others stay
	res := h.mustPut(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 203.0.113.11}, ports: [5123], offset: +400d}")
	h.barrier()
	rest := "203.0.113.12 203.0.113.13 203.0.113.14"
	if got := h.trackedDestinations(); got != rest {
		t.Errorf("after the fault arrived: %s, want %s", got, rest)
	}

	// the device polls again and is redirected to the port of the selector; the fault ends and its flows go
	h.k.SetConntrack(ntpFlowsText)
	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	if got := h.trackedDestinations(); got != rest {
		t.Errorf("after the fault ended: %s, want %s", got, rest)
	}
}

// The ports of the selectors are handed from one apply to the next: a selector that sorts before another and arrives later
// does not move the other's port (a flow that was redirected to it, and the server's configuration, stay valid).
func TestASelectorThatArrivesLaterDoesNotMoveThePortOfAnother(t *testing.T) {
	h := tlsHarness(t)
	first := h.mustPut(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 203.0.113.5}, offset: +1y}")
	h.barrier()
	second := h.mustPut(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 198.51.100.7}, offset: +2y}")
	s := h.barrier()
	byOverlay := map[string]int{}
	for _, f := range s.NTPFaults {
		byOverlay[f.Overlay] = f.Port
	}
	if byOverlay[first.Overlay.Id.String()] != 12301 || byOverlay[second.Overlay.Id.String()] != 12302 {
		t.Errorf("the first selector keeps 12301 and the later one takes 12302: %v", byOverlay)
	}
}

func ntpTextFlow(src, dst string, sport, dport int, replySrc string, replyPort int) string {
	return fmt.Sprintf("udp      17 25 src=%s dst=%s sport=%d dport=%d packets=2 bytes=152 src=%s dst=%s sport=%d dport=%d packets=2 bytes=152 [ASSURED] mark=0 use=1\n",
		src, dst, sport, dport, replySrc, src, replyPort, sport)
}

// The deletion goes in batches of the most the executor takes at once, and every stale flow is in one of them; a flow without a
// source or a destination port (a record the kernel keeps for a protocol that has none) is never named for deletion.
func TestEveryStaleNTPFlowIsDeletedAlsoWhenThereAreMoreThanOneBatch(t *testing.T) {
	h := tlsHarness(t)
	var b strings.Builder
	const n = executor.MaxConntrackFlows + 1
	for i := range n {
		b.WriteString(ntpTextFlow("10.10.0.31", fmt.Sprintf("198.18.%d.%d", i/250, 1+i%250), 20000+i%40000, 123, "169.254.100.2", 123))
	}
	b.WriteString(ntpTextFlow("10.10.0.31", "203.0.113.20", 0, 123, "203.0.113.20", 123))
	b.WriteString(ntpTextFlow("10.10.0.31", "203.0.113.21", 40021, 0, "203.0.113.21", 0))
	h.k.SetConntrack(b.String())
	h.mustPut(alice, "target: {device: esp32-42}\ntime: {offset: +400d}")
	h.barrier()
	if got := h.trackedDestinations(); got != "203.0.113.20 203.0.113.21" {
		flows, _ := h.e.Flows(context.Background())
		t.Errorf("%d flows are left, want the two without a port: %s", len(flows), got)
	}
}

// A connection that ends on the NTP server is told by the port the reply comes from: the NTP port and the 32 ports of the
// selectors are the server's, the ports next to them are not.
func TestAFlowIsLabelledNTPServerOnlyForThePortsOfTheServer(t *testing.T) {
	h := tlsHarness(t)
	var b strings.Builder
	base := compiler.NTPRedirectPortBase
	ports := map[string]int{"203.0.113.1": 123, "203.0.113.2": base, "203.0.113.3": base + compiler.MaxNTPDestinations - 1,
		"203.0.113.4": base + compiler.MaxNTPDestinations, "203.0.113.5": base - 1, "203.0.113.6": 124}
	for dst, p := range ports {
		b.WriteString(ntpTextFlow("10.10.0.31", dst, 40000, 5123, "169.254.100.2", p))
	}
	h.k.SetConntrack(b.String())
	flows, err := h.e.Flows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"203.0.113.1": "ntp_server", "203.0.113.2": "ntp_server", "203.0.113.3": "ntp_server",
		"203.0.113.4": "dns_proxy", "203.0.113.5": "dns_proxy", "203.0.113.6": "dns_proxy"}
	if len(flows) != len(want) {
		t.Fatalf("%d flows", len(flows))
	}
	for _, f := range flows {
		if f.Service != want[f.Dst.String()] {
			t.Errorf("the flow to %s (reply from port %d) is %q, want %q", f.Dst, ports[f.Dst.String()], f.Service, want[f.Dst.String()])
		}
	}
}

// The ports of the selectors are part of the snapshot, and what the engine compiles on top of it (the preview of a revision, the
// check of an overlay) is given them: a selector that sorts before another and came later would take the other's port in a
// compile that does not know the allocation, and the preview would show rules the apply does not make.
func TestThePreviewOfARevisionKeepsThePortsOfTheSelectorsInUse(t *testing.T) {
	h := tlsHarness(t)
	h.mustPut(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 203.0.113.5}, offset: +1y}")
	h.barrier()
	h.mustPut(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 198.51.100.7}, offset: +2y}")
	s := h.barrier()
	want := map[int]bool{12301: true, 12302: true}
	if len(s.NTPPorts) != 2 {
		t.Fatalf("the snapshot has the allocation %v", s.NTPPorts)
	}
	for k, p := range s.NTPPorts {
		if !want[p] {
			t.Fatalf("%s has the port %d", k, p)
		}
	}
	p, err := h.e.Preview(context.Background(), h.revision(withDHCPAndDevice))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Target.NTPPorts) != 2 {
		t.Fatalf("the preview has the allocation %v, problems %v, faults %d, overlays %d", p.Target.NTPPorts, p.Problems, len(p.Target.NTPFaults), len(s.Overlays))
	}
	for k, port := range s.NTPPorts {
		if p.Target.NTPPorts[k] != port {
			t.Errorf("%s: the preview gives port %d, the applied target has %d", k, p.Target.NTPPorts[k], port)
		}
	}
}
