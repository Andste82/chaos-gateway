package engine_test

import (
	"context"
	"testing"
	"time"
)

// #64: Kea opens its packet socket on an interface only while it is configured and skips one without
// a carrier. When a bridge Kea serves comes up again, or is created again, the engine configures Kea
// again even though the configuration did not change; a bridge going down, another interface's
// change and a reading without a change do not.
func TestTheDHCPServerIsConfiguredAgainWhenItsBridgeComesUp(t *testing.T) {
	h, f := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	refresh := func() int {
		t.Helper()
		if err := h.e.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		return f.reopened()
	}
	n := refresh()
	if refresh() != n {
		t.Fatal("a reading without a change configures the server again")
	}
	applies := f.calls()

	h.k.SetCarrier("br-iot", false)
	if refresh() != n {
		t.Error("a bridge that lost its carrier configures the server again")
	}
	h.k.SetCarrier("br-iot", true)
	if refresh() != n+1 {
		t.Fatalf("the bridge came up again and the server was not configured again (%d, want %d)", f.reopened(), n+1)
	}
	f.mu.Lock()
	last := f.reopens[len(f.reopens)-1]
	f.mu.Unlock()
	if last == nil || len(last.Config.Subnets) != 1 || last.Config.Subnets[0].Interface != "br-iot" {
		t.Errorf("the server is configured with %+v", last)
	}
	if refresh() != n+1 {
		t.Error("the server is configured again on a reading without a change")
	}

	// an interface the server does not listen on
	h.k.SetCarrier("wan0", false)
	refresh()
	h.k.SetCarrier("wan0", true)
	if refresh() != n+1 {
		t.Error("an interface without DHCP configures the server again")
	}

	// the bridge was deleted and created again: another index, the old socket is bound to nothing
	h.k.RemoveLink("br-iot")
	refresh()
	h.k.AddLink("br-iot", "02:00:00:00:00:01", "bridge", true)
	if refresh() != n+2 {
		t.Errorf("a bridge that was created again did not configure the server again (%d, want %d)", f.reopened(), n+2)
	}
	if f.calls() != applies {
		t.Errorf("the link changes ran %d applies; a reconfiguration is all they need", f.calls()-applies)
	}
}
