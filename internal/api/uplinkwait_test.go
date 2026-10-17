package api_test

import (
	"context"
	"testing"
	"time"
)

// takeUplinkAddress removes the uplink's address, as `netplan apply` does for a moment, and lets the
// engine observe it (the netlink observer's part in production).
func (g *gw) takeUplinkAddress() {
	g.t.Helper()
	g.k.DelAddr("wan0", "203.0.113.1/24")
	if err := g.e.Refresh(context.Background()); err != nil {
		g.t.Fatal(err)
	}
}

// returnUplinkAddressAfter gives the uplink its address back after d, observed like the removal; the
// test waits for it before it ends.
func (g *gw) returnUplinkAddressAfter(d time.Duration) {
	done := make(chan struct{})
	g.t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		time.Sleep(d)
		g.k.SetAddr("wan0", "203.0.113.1/24")
		if err := g.e.Refresh(context.Background()); err != nil {
			g.t.Error(err)
		}
	}()
}

// Issue #64: an apply that comes while the uplink has lost its address for a moment waits for the
// address instead of failing with "the uplink wan0 has no IPv4 address".
func TestAnApplyWaitsForTheUplinkAddressInsteadOfFailing(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.20"}})
	g.takeUplinkAddress()
	g.returnUplinkAddressAfter(300 * time.Millisecond)
	start := time.Now()
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("apply: %d %s", r.Status, r.Body)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Error("the apply did not wait for the address")
	}
}

// The setup (the wizard's apply) waits the same way.
func TestTheSetupWaitsForTheUplinkAddressInsteadOfFailing(t *testing.T) {
	g := newGW(t)
	g.takeUplinkAddress()
	// later than the setup takes to reach its apply (the keys it provisions)
	g.returnUplinkAddressAfter(2 * time.Second)
	r := g.do("POST", "/setup", fixtureJSON(t), map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 {
		t.Fatalf("setup: %d %s", r.Status, r.Body)
	}
}
