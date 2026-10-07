package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The tc tree in the engine (M8b): an overlay write returns when the kernel holds its classes, and
// the classes of a fault that went stay for the largest configured delay plus a second on the
// engine's clock (plan §3.2).

// tcTree reads the own tree of an interface of the simulated kernel.
func (h *harness) tcTree(dev string) *linux.NormTree {
	h.t.Helper()
	out, err := apply.Local{E: h.ex}.Do(context.Background(), &executor.Read{What: executor.ReadTC, Dev: dev})
	if err != nil {
		h.t.Fatal(err)
	}
	var t linux.NormTree
	if err := json.Unmarshal(out.Data[0], &t); err != nil {
		h.t.Fatal(err)
	}
	return t.Subtree("1:")
}

func (h *harness) tcClasses(dev string) int {
	n := 0
	for _, c := range h.tcTree(dev).Classes {
		if c.ID != "1:1" {
			n++
		}
	}
	return n
}

// waitRetired waits for the retirer of the engine to have deleted everything.
func (h *harness) waitRetired() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(h.e.RetiringTC()) > 0 {
		if time.Now().After(deadline) {
			h.t.Fatalf("still retiring: %+v", h.e.RetiringTC())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAnOverlayWriteReturnsWhenTheKernelHoldsItsTree(t *testing.T) {
	h := startedWithRevision(t)
	res := h.mustPut(alice, `
target: {network: IoT}
fault: {latency: 120ms, jitter: 10ms, loss: 1%}`)
	f := h.e.Snapshot().Faults[0]
	for _, dev := range []string{"br-iot", "br-lab", "wan0"} {
		tree := h.tcTree(dev)
		var leaf *linux.NormQdisc
		for i, q := range tree.Qdiscs {
			if q.Parent == "1:"+hex(0x10+2*f.ID) {
				leaf = &tree.Qdiscs[i]
			}
		}
		if leaf == nil || leaf.Netem == nil || leaf.Netem.Delay != 0.12 || leaf.Netem.Jitter != 0.01 || leaf.Netem.Loss != 0.01 {
			t.Errorf("%s: the upload leaf of fault %d is %+v (answered with generation %d)", dev, f.ID, leaf, res.Generation)
		}
	}
	h.verifyKernelWithOverlays()
}

func hex(n int) string {
	const digits = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 16 {
		b = append([]byte{digits[n%16]}, b...)
	}
	return string(b)
}

func TestTheClassesOfADeletedOverlayStayForTheLargestDelayPlusASecond(t *testing.T) {
	h := startedWithRevision(t)
	r := h.mustPut(alice, `
target: {network: IoT}
fault: {latency: 600ms}`)
	if got := h.tcClasses("br-iot"); got != 2 {
		t.Fatalf("%d classes after the write", got)
	}
	if _, err := h.e.DeleteOverlay(context.Background(), r.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	// the classification no longer names the fault; its classes (with the queued packets) are still
	// there, on every interface, and the engine says so
	if pending := h.e.RetiringTC(); len(pending) != 3 || pending[0].Class != "" {
		t.Fatalf("retiring %+v", pending)
	}
	for _, dev := range []string{"br-iot", "br-lab", "wan0"} {
		if h.tcClasses(dev) != 2 {
			t.Errorf("%s lost its classes at once", dev)
		}
	}
	h.verifyKernelWithOverlays()

	h.clk.Advance(1599 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if h.tcClasses("br-iot") != 2 {
		t.Fatal("the classes went before the largest delay (600 ms) plus a second")
	}
	h.clk.Advance(time.Millisecond)
	h.waitRetired()
	for _, dev := range []string{"br-iot", "br-lab", "wan0"} {
		if got := len(h.tcTree(dev).Qdiscs); got != 0 {
			t.Errorf("%s still has %d qdiscs of the tree", dev, got)
		}
	}
	h.verifyKernelWithOverlays()
}

// A fault that goes and another that comes: the new one does not take the id of the old one, whose
// classes are still in the kernel, and the old classes go once their time has passed.
func TestAFaultThatComesDoesNotTakeTheIdOfOneThatIsStillRetiring(t *testing.T) {
	h := startedWithRevision(t)
	a := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 200ms}")
	idA := h.e.Snapshot().Faults[0].ID
	if _, err := h.e.DeleteOverlay(context.Background(), a.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	if h.tcClasses("br-lab") != 2 {
		t.Fatal("the classes of the deleted fault went at once")
	}
	h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 50ms, loss: 2%}")
	faults := h.e.Snapshot().Faults
	if len(faults) != 1 || faults[0].ID == idA {
		t.Fatalf("the new fault has the id %+v, the old one had %d", faults, idA)
	}
	// both are in the kernel; the old ones are what the engine says is retiring
	if got := h.tcClasses("br-lab"); got != 4 {
		t.Errorf("%d classes on br-lab, want the old two and the new two", got)
	}
	h.verifyKernelWithOverlays()
	// the delay that counts is the largest in the kernel, the old fault's 200 ms
	h.clk.Advance(1199 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if got := h.tcClasses("br-lab"); got != 4 {
		t.Fatalf("%d classes after 1.199 s", got)
	}
	h.clk.Advance(time.Millisecond)
	h.waitRetired()
	if got := h.tcClasses("br-lab"); got != 2 {
		t.Errorf("%d classes after the old ones went", got)
	}
	h.verifyKernelWithOverlays()
}

// An executor failure of a tc operation takes the write back like every other failure (plan §3.11).
func TestAFailureOfATCOperationRevertsTheOverlayWrite(t *testing.T) {
	h := startedWithRevision(t)
	keep := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 30ms}")
	var failed atomic.Int32
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "tc" && strings.Contains(strings.Join(argv, " "), "-batch") && strings.Contains(stdin, "netem") && strings.Contains(stdin, "delay 777ms") {
			failed.Add(1)
			return &executor.Result{Exit: 1, Stderr: "Error: injected\nCommand failed -:1\n"}
		}
		return nil
	}
	_, err := h.put(bob, "target: {network: Lab}\nfault: {latency: 777ms}")
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) {
		t.Fatalf("got %v, want apply_failed", err)
	}
	if failed.Load() == 0 {
		t.Fatal("the injected failure never ran")
	}
	s := h.barrier()
	h.k.Fail = nil
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id {
		t.Fatalf("overlays after the failure: %+v", s.Overlays)
	}
	if s.LastError != "" {
		t.Errorf("the restore did not apply: %s", s.LastError)
	}
	h.verifyKernelWithOverlays()
	// and the next write works
	h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 40ms}")
	h.verifyKernelWithOverlays()
}
