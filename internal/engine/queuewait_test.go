package engine_test

import (
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
)

// A write that arrives while an apply is in flight waits for the next one; the result says how long it waited
// before the apply loop took it up (OverlayResult.QueueWait: the queue wait of a scenario step, plan §2.10, §3.11).
// A write that finds the loop idle waits for nothing.
func TestAWriteBehindAnApplyInFlightReportsItsQueueWait(t *testing.T) {
	h, g := gatedHarness(t, engine.Config{})
	idle := h.mustPut(alice, globalLatency(0))
	// the executor's own queue is on its real clock: a few microseconds here
	if idle.QueueWait > 50*time.Millisecond {
		t.Errorf("a write that found the apply loop idle waited %v", idle.QueueWait)
	}

	g.arm()
	first := make(chan writeOutcome, 1)
	go func() { r, err := h.put(alice, globalLatency(1)); first <- writeOutcome{r, err} }()
	g.waitEntered(t)
	second := make(chan writeOutcome, 1)
	go func() { r, err := h.put(bob, globalLatency(2)); second <- writeOutcome{r, err} }()
	h.waitAccepted(3)
	h.clk.Advance(250 * time.Millisecond)
	g.release()
	a, b := <-first, <-second
	if a.err != nil || b.err != nil {
		t.Fatalf("%v %v", a.err, b.err)
	}
	if a.res.QueueWait > 50*time.Millisecond {
		t.Errorf("the write that started the apply waited %v", a.res.QueueWait)
	}
	if b.res.QueueWait < 250*time.Millisecond || b.res.QueueWait > 300*time.Millisecond {
		t.Errorf("the write behind the apply waited %v, want 250ms", b.res.QueueWait)
	}
	// a deletion reports it too
	h.barrier()
	del, err := h.e.DeleteOverlay(t.Context(), b.res.Overlay.Id, nil, alice)
	if err != nil || del.QueueWait > 50*time.Millisecond {
		t.Errorf("%v %v", err, del.QueueWait)
	}
}
