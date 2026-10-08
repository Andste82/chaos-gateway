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

// An apply that comes while classes wait for their deletion does not move the deletion: the old
// classes go at the time they were due, on the fake clock, and the new fault's classes stay.
func TestAnApplyBeforeTheDeletionFiresLeavesItsTimeAlone(t *testing.T) {
	h := startedWithRevision(t)
	a := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 200ms}")
	if _, err := h.e.DeleteOverlay(context.Background(), a.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	// due in 1.2 s (200 ms + 1 s)
	h.clk.Advance(700 * time.Millisecond)
	h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 50ms, loss: 2%}")
	var left time.Duration
	for _, p := range h.e.RetiringTC() {
		if p.Class != "" {
			left = p.In
			break
		}
	}
	if left != 500*time.Millisecond {
		for _, p := range h.e.RetiringTC() {
			t.Logf("%s %v", p.Key(), p.In)
		}
		t.Fatalf("the classes of the deleted fault are due in %v after the next apply, want 500 ms (%+v)", left, h.e.RetiringTC())
	}
	h.verifyKernelWithOverlays()
	h.clk.Advance(499 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if got := h.tcClasses("br-iot"); got != 4 {
		t.Fatalf("%d classes 1 ms before the deletion, want the old two and the new two", got)
	}
	h.clk.Advance(time.Millisecond)
	h.waitRetired()
	if got := h.tcClasses("br-iot"); got != 2 {
		t.Errorf("%d classes after the deletion, want the new fault's two", got)
	}
	h.verifyKernelWithOverlays()
}

// A fault that is written again while its old classes wait gets other ids (make-before-break: the
// packets queued in the old classes are not mixed with the new ones), and the old classes still go at
// the time they were due.
func TestAFaultThatComesBackBeforeTheDeletionHasNewClassesAndTheOldOnesGoOnTime(t *testing.T) {
	h := startedWithRevision(t)
	a := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 200ms}")
	id := h.e.Snapshot().Faults[0].ID
	if _, err := h.e.DeleteOverlay(context.Background(), a.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(400 * time.Millisecond)
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 200ms}")
	if got := h.e.Snapshot().Faults[0].ID; got == id {
		t.Fatalf("the fault came back with the id %d of the classes that are still retiring", id)
	}
	if got := h.tcClasses("br-iot"); got != 4 {
		t.Fatalf("%d classes, want the old two and the new two", got)
	}
	h.clk.Advance(799 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if got := h.tcClasses("br-iot"); got != 4 {
		t.Fatalf("%d classes 1 ms before the old ones are due", got)
	}
	h.clk.Advance(time.Millisecond)
	h.waitRetired()
	if got := h.tcClasses("br-iot"); got != 2 {
		t.Errorf("%d classes, want the fault's two", got)
	}
	h.verifyKernelWithOverlays()
}

// A restart with classes that wait for their deletion: the new engine finds them in its first apply
// and deletes them one grace period after that, on its own clock.
func TestAfterARestartTheLeftoverClassesAreDeletedByTheNextFullApply(t *testing.T) {
	h := startedWithRevision(t)
	a := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 300ms}")
	if _, err := h.e.DeleteOverlay(context.Background(), a.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	if h.tcClasses("br-iot") != 2 || len(h.e.RetiringTC()) == 0 {
		t.Fatal("the classes did not wait")
	}
	h.e.Close()
	// the same kernel, a new process: it knows nothing of what was waiting
	h.start()
	h.barrier()
	if h.tcClasses("br-iot") != 2 {
		t.Fatal("the new engine deleted the classes before their time")
	}
	if len(h.e.RetiringTC()) == 0 {
		t.Fatal("the new engine does not know that the classes are leftovers")
	}
	h.clk.Advance(1299 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if h.tcClasses("br-iot") != 2 {
		t.Fatal("the classes went before the largest delay (300 ms) plus a second")
	}
	h.clk.Advance(time.Millisecond)
	h.waitRetired()
	for _, dev := range tcDevs {
		if got := len(h.tcTree(dev).Qdiscs); got != 0 {
			t.Errorf("%s still has %d qdiscs of the tree", dev, got)
		}
	}
	h.verifyKernelWithOverlays()
}

// The preview names the tc work the apply loop would do: the classes of a fault that went stay for
// the grace period (they are not deleted right after the transaction), as the plan of ApplyWith with
// the engine's retirer says.
func TestThePreviewPlansTheTCTreeAsTheApplyLoopDoes(t *testing.T) {
	h := startedWithRevision(t)
	rev := h.e.Snapshot().Applied.Revision
	r := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 600ms}")
	if _, err := h.e.DeleteOverlay(context.Background(), r.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	if len(h.e.RetiringTC()) == 0 {
		t.Fatal("nothing is retiring: the test proves nothing")
	}
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(p.Plan, "\n")
	if strings.Contains(text, "tc: delete") || !strings.Contains(text, "stay for 1.6s") {
		t.Errorf("the preview plans the tc deletion as the one-shot plan does:\n%s", text)
	}
}

// A write returns only after the tc part of the verify, too: when the kernel's tree differs from the
// target after the apply's operations all went through (here a leaf is changed behind the engine's back
// right after the nftables transaction), the write fails as apply_failed at the verify stage and the
// batch is reverted like every other failure.
func TestADriftedTreeFoundByTheVerifyFailsTheWriteAndRevertsIt(t *testing.T) {
	h := startedWithRevision(t)
	keep := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 30ms}")
	var armed, drifted atomic.Int32
	armed.Store(1)
	h.k.After = func(argv []string, stdin string) {
		if argv[0] != "nft" || !strings.Contains(strings.Join(argv, " "), "-f") || !armed.CompareAndSwap(1, 0) {
			return
		}
		// every operation of the apply went through; now one leaf of the new fault is changed by hand
		for _, q := range h.tcTree("br-iot").Qdiscs {
			if q.Netem != nil && q.Netem.Delay == 0.777 {
				line := "qdisc replace dev br-iot parent " + q.Parent + " handle " + q.Handle + " netem limit 1000 delay 1ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"
				if r, _ := h.k.Run(context.Background(), executor.Command{Tool: executor.ToolTC, Args: []string{"-force", "-batch", "-"}, Stdin: line + "\n"}); r.Exit != 0 {
					t.Errorf("%s: %s", line, r.Stderr)
				}
				drifted.Add(1)
				return
			}
		}
	}
	_, err := h.put(bob, "target: {network: Lab}\nfault: {latency: 777ms}")
	h.k.After = nil
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) {
		t.Fatalf("got %v, want apply_failed", err)
	}
	if drifted.Load() == 0 {
		t.Fatal("the kernel was never changed behind the engine's back: the test proves nothing")
	}
	var ae *apply.Error
	if !errors.As(err, &ae) || ae.Stage != "verify" || !strings.Contains(ae.Error(), "tc") {
		t.Errorf("the failure is %v, want one of the verify stage about the tc tree", err)
	}
	s := h.barrier()
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id {
		t.Fatalf("overlays after the failure: %+v", s.Overlays)
	}
	if s.LastError != "" {
		t.Errorf("the restore did not apply: %s", s.LastError)
	}
	h.verifyKernelWithOverlays()
}
