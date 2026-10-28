package engine_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Explain asks for a device as its most current address, not the lowest one (P6-AUDIT-58): a device that was re-addressed
// keeps its old address mapped while it carries connections, and the old address sorts first.
func TestExplainAsksForADeviceAsItsMostCurrentAddress(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.clk.Advance(time.Minute)
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg), neighbor("10.10.0.32", macCfg)})
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", "10.10.0.31", 40001, "203.0.113.10", 8883)))
	h.observe()
	h.barrier()
	s := h.e.Snapshot()
	if cur := s.Identity.Current[devID]; len(cur) != 2 || cur[0].String() != "10.10.0.32" || s.Identity.Addresses[devID][0].String() != "10.10.0.31" {
		t.Fatalf("the setup does not order the addresses differently: %v, sorted %v", cur, s.Identity.Addresses[devID])
	}
	if got := h.explain(engine.ExplainQuery{Device: devID, Dst: "203.0.113.10", Protocol: "tcp", Port: 8883}).Source.IP; got != "10.10.0.32" {
		t.Errorf("explain asks as %s, want the most current address 10.10.0.32", got)
	}
	// an address given with the device is asked as it is
	if got := h.explain(engine.ExplainQuery{Device: devID, Src: netip.MustParseAddr("10.10.0.31"), Dst: "203.0.113.10", Protocol: "tcp", Port: 8883}).Source.IP; got != "10.10.0.31" {
		t.Errorf("explain with an address asks as %s, want 10.10.0.31", got)
	}
}
