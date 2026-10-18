package engine_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// waitReopens waits until the DHCP server was configured again n times in all.
func waitReopens(t *testing.T, f *fakeDHCP, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.reopened() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the server was configured again %d times, want %d", f.reopened(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// #64: Kea opens its packet socket on an interface only while it is configured and skips one without
// a carrier. When a bridge Kea serves comes up again, is created again, or had its carrier go and come
// back between two readings of the host, the engine configures Kea again even though the configuration
// did not change; a bridge going down, another interface's change, a reading without a change and the
// first reading after the apply that created the bridge do not.
func TestTheDHCPServerIsConfiguredAgainWhenItsBridgeComesUp(t *testing.T) {
	h, f := dhcpHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	refresh := func() int {
		t.Helper()
		if err := h.e.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		return h.e.DHCPReopensScheduled()
	}
	// the host is read before the apply, which creates the bridge and sends the configuration with it
	// running: the reading after it has nothing to do
	refresh()
	h.mustApply(h.revision(withDHCPAndDevice))
	if n := refresh(); n != 0 {
		t.Fatalf("the reading after the apply that created the bridge configures the server again (%d)", n)
	}
	if refresh() != 0 {
		t.Fatal("a reading without a change configures the server again")
	}
	applies := f.calls()

	h.k.SetCarrier("br-iot", false)
	if refresh() != 0 {
		t.Error("a bridge that lost its carrier configures the server again")
	}
	h.k.SetCarrier("br-iot", true)
	if refresh() != 1 {
		t.Fatal("the bridge came up again and the server was not configured again")
	}
	waitReopens(t, f, 1)
	f.mu.Lock()
	last := f.reopens[len(f.reopens)-1]
	f.mu.Unlock()
	if last == nil || len(last.Config.Subnets) != 1 || last.Config.Subnets[0].Interface != "br-iot" {
		t.Errorf("the server is configured with %+v", last)
	}
	if refresh() != 1 {
		t.Error("the server is configured again on a reading without a change")
	}

	// the carrier went and came back between two readings: both see the bridge running, the kernel's
	// count of carrier changes tells the flap
	h.k.SetCarrier("br-iot", false)
	h.k.SetCarrier("br-iot", true)
	if refresh() != 2 {
		t.Fatal("a flap between two readings did not configure the server again")
	}
	waitReopens(t, f, 2)

	// an interface the server does not listen on
	h.k.SetCarrier("wan0", false)
	refresh()
	h.k.SetCarrier("wan0", true)
	if refresh() != 2 {
		t.Error("an interface without DHCP configures the server again")
	}

	// the bridge was deleted and created again: another index, the old socket is bound to nothing
	h.k.RemoveLink("br-iot")
	refresh()
	h.k.AddLink("br-iot", "02:00:00:00:00:01", "bridge", true)
	if refresh() != 3 {
		t.Error("a bridge that was created again did not configure the server again")
	}
	waitReopens(t, f, 3)
	if f.calls() != applies {
		t.Errorf("the link changes ran %d applies; a reconfiguration is all they need", f.calls()-applies)
	}
}

// #64 review: configuring the server again does not hold up the reading of the host. A DHCP server that
// does not answer leaves Refresh, and with it the uplink address and the entry addresses the forwards
// follow, as fast as without one; the server is configured again once it answers.
func TestADHCPServerThatHangsDoesNotHoldUpTheReadingOfTheHost(t *testing.T) {
	h, f := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	f.mu.Lock()
	f.block = release
	f.mu.Unlock()
	defer close(release)

	h.k.SetCarrier("br-iot", false)
	h.k.SetCarrier("br-iot", true)
	h.k.SetAddr("wan0", "203.0.113.7/24")
	for i := range 3 {
		quick, done := context.WithTimeout(ctx, 2*time.Second)
		err := h.e.Refresh(quick)
		done()
		if err != nil {
			t.Fatalf("reading %d waited for the DHCP server: %v", i, err)
		}
	}
	waitReopens(t, f, 1) // it is in Reopen, and hangs there
	if up, _ := h.e.Snapshot().Host.Link("wan0"); !slices.ContainsFunc(up.Addrs, func(a compiler.HostAddr) bool { return a.Prefix.String() == "203.0.113.7/24" }) {
		t.Errorf("the uplink's new address was not observed while the DHCP server hangs: %+v", up)
	}
}

// #64 review: an apply that creates a deleted bridge again leaves the DHCP configuration as it was, so
// Apply would send nothing and Kea keep its socket on the old bridge. The apply compares its read-back
// with the last reading and configures the server again (once: the reading after it has nothing to
// do). The same holds for a bridge that was down at the last reading and came up before the apply.
func TestAnApplyThatCreatesTheBridgeAgainConfiguresTheServerAgain(t *testing.T) {
	h, f := dhcpHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	refresh := func() {
		t.Helper()
		if err := h.e.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	h.mustApply(h.revision(withDHCPAndDevice))
	refresh()
	if n := f.reopened(); n != 0 {
		t.Fatalf("the first apply configured the server again (%d)", n)
	}

	h.k.RemoveLink("br-iot")
	refresh()
	h.mustApply(h.revision(withDHCPAndDevice)) // the same DHCP configuration; the apply creates br-iot
	if n := f.reopened(); n != 1 {
		t.Fatalf("the apply that created the bridge again configured the server again %d times, want 1", n)
	}
	refresh()
	if n := h.e.DHCPReopensScheduled(); n != 0 {
		t.Errorf("the reading after that apply configures the server again (%d)", n)
	}

	h.k.SetCarrier("br-iot", false)
	refresh()
	h.k.SetCarrier("br-iot", true)
	h.mustApply(h.revision(withDHCPAndDevice))
	if n := f.reopened(); n != 2 {
		t.Fatalf("an apply after the bridge came up configured the server again %d times in all, want 2", n)
	}

	// an apply without a change of the served links only applies
	h.mustApply(h.revision(withDHCPAndDevice))
	if n := f.reopened(); n != 2 {
		t.Errorf("an apply without a link change configured the server again (%d)", n)
	}
}
