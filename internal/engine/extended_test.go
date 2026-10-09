package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Extended faults in the engine (M10) on the simulated kernel: the duplication hook is in the kernel when a
// write returns and gone with the last duplicating fault, and the preview of a per-device rate that does not
// fit the class limit is capacity_exceeded with the scope.

// hookOn reports whether an interface of the simulated kernel carries the duplication hook.
func (h *harness) hookOn(dev string) bool {
	h.t.Helper()
	for _, d := range h.k.DupDevs() {
		if d == dev {
			return true
		}
	}
	return false
}

func TestADuplicatingOverlayHasItsHookInTheKernelWhenTheWriteReturnsAndLosesItWithTheLastOne(t *testing.T) {
	h := startedWithRevision(t)
	keep := h.mustPut(alice, "target: {network: Lab}\nfault: {latency: 40ms}")
	for _, dev := range []string{"br-iot", "br-lab", "wan0"} {
		if h.hookOn(dev) {
			t.Fatalf("%s has a hook before anything duplicates", dev)
		}
	}
	dup := h.mustPut(bob, "target: {network: IoT}\nfault: {upload: {duplicate: 7%}, download: {latency: 5ms}}")
	for _, dev := range []string{"br-iot", "br-lab", "wan0"} {
		if !h.hookOn(dev) {
			t.Errorf("%s has no hook when the write returns", dev)
		}
	}
	// the netem leaves of the tree never duplicate: the draw is in the mark chain
	f := faultOfSource(t, h.e.Snapshot(), dup.Overlay.Id.String())
	chain := strings.Join(h.chainRules(compiler.MarkChainName(f.ID)), "\n")
	if !strings.Contains(chain, "numgen") || !strings.Contains(chain, "70000000") { // 7 % of 10^9 is 70000000
		t.Errorf("the mark chain of the duplicating fault:\n%s", chain)
	}
	for _, q := range h.tcTree("br-lab").Qdiscs {
		if q.Netem != nil && q.Netem.Duplicate != 0 {
			t.Errorf("the leaf %s duplicates: %+v", q.Handle, q.Netem)
		}
	}
	h.verifyKernelWithOverlays()

	// changing the probability changes the chain, not the tree
	h.mustPut(bob, "target: {network: IoT}\nfault: {upload: {duplicate: 12%}, download: {latency: 5ms}}")
	if chain := strings.Join(h.chainRules(compiler.MarkChainName(f.ID)), "\n"); !strings.Contains(chain, "120000000") {
		t.Errorf("the new probability is not in the chain:\n%s", chain)
	}
	h.verifyKernelWithOverlays()

	if _, err := h.e.DeleteOverlay(context.Background(), dup.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	for _, dev := range []string{"br-iot", "br-lab", "wan0"} {
		if h.hookOn(dev) {
			t.Errorf("%s still has the hook after the last duplicating fault went", dev)
		}
	}
	// the tree of the fault that stays is untouched
	if h.tcClasses("br-lab") < 2 {
		t.Errorf("the other fault lost its classes")
	}
	_ = keep
	h.verifyKernelWithOverlays()
}

// The per-device rate of the plan (D18, M10): the class limit counts a class per device and direction, and a
// revision that does not fit is capacity_exceeded in the preview, naming the scope that needs the classes.
func TestThePreviewOfAPerDeviceRateThatDoesNotFitTheClassLimitIsCapacityExceeded(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{ClassLimit: 50})
	// 30 known devices in the network IoT, then a rate on the network: 31 queues (the devices and the
	// addresses no device owns) in each direction and the default class are 63 classes on an interface
	withDevices := func(c *model.Configuration) {
		devs := map[string]model.Device{}
		for i := 0; i < 30; i++ {
			ip := fmt.Sprintf("10.10.0.%d", 20+i)
			devs[fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)] = model.Device{Name: fmt.Sprintf("dev-%02d", i), Identifiers: &model.DeviceIdentifiers{Ipv4: &[]string{ip}}}
		}
		c.Devices = &devs
	}
	h.mustApply(h.revision(withDevices))
	h.observe() // the engine learns the addresses of the configured devices from the host's neighbors and its own config
	if got := len(h.e.Snapshot().Identity.Addresses); got != 30 {
		t.Fatalf("the engine knows the addresses of %d devices, want 30", got)
	}
	rev := h.revision(func(c *model.Configuration) {
		withDevices(c)
		var f model.ConfigFault
		if err := json.Unmarshal([]byte(`{"source": {"network": "IoT"}, "rate": "2Mbit", "latency": "100ms"}`), &f); err != nil {
			t.Fatal(err)
		}
		c.Faults = &map[string]model.ConfigFault{"8a1b2c3d-1111-4222-8333-444455556666": f}
	})
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	var cap *compiler.Problem
	for i := range p.Problems {
		if p.Problems[i].Code == compiler.CodeCapacityExceeded {
			cap = &p.Problems[i]
		}
	}
	if cap == nil || cap.Severity != compiler.SevError {
		t.Fatalf("the preview of 31 per-device queues at a limit of 50 classes: %+v", p.Problems)
	}
	if cap.Scope != "network IoT" || !strings.Contains(cap.Message, "limit of 50") || !strings.Contains(cap.Message, "63 classes") {
		t.Errorf("%+v", cap)
	}
	if len(p.Plan) != 0 {
		t.Errorf("a preview that is refused plans work: %v", p.Plan)
	}
	// and the apply is refused the same way, the kernel stays as it was
	if _, err := h.apply(rev); err == nil {
		t.Error("the apply of a revision that does not fit was accepted")
	}
	if h.tcClasses("br-iot") != 0 {
		t.Error("a refused revision reached the kernel")
	}
	// without the rate it is one fault id and two classes
	rev2 := h.revision(func(c *model.Configuration) {
		withDevices(c)
		var f model.ConfigFault
		if err := json.Unmarshal([]byte(`{"source": {"network": "IoT"}, "latency": "100ms"}`), &f); err != nil {
			t.Fatal(err)
		}
		c.Faults = &map[string]model.ConfigFault{"8a1b2c3d-1111-4222-8333-444455556666": f}
	})
	if p, err := h.e.Preview(context.Background(), rev2); err != nil || len(p.Problems) != 0 {
		t.Errorf("%v %+v", err, p.Problems)
	}
}

func faultOfSource(t *testing.T, s *engine.Snapshot, source string) compiler.Fault {
	t.Helper()
	for _, f := range s.Faults {
		if f.Source == source {
			return f
		}
	}
	t.Fatalf("no fault of %s in %+v", source, s.Faults)
	return compiler.Fault{}
}
