package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
)

func hasProblem(p *engine.Preview, code string) bool {
	for _, x := range p.Problems {
		if x.Severity == compiler.SevError && x.Code == code {
			return true
		}
	}
	return false
}

// Issue #64: `netplan apply` takes the uplink's address away for a moment. An apply in that moment
// waits for the observer to report the address back instead of failing at once.
func TestAnApplyWaitsForTheUplinkAddressTheObserverReportsBack(t *testing.T) {
	h := newHarness(t)
	h.k.DelAddr("wan0", "203.0.113.1/24")
	h.start()
	rev := h.revision(nil)
	if p, err := h.e.Preview(context.Background(), rev); err != nil || !hasProblem(p, compiler.CodeUplinkNoAddress) {
		t.Fatalf("preview without the uplink's address: %v %+v", err, p)
	}

	armed := h.clk.Pending()
	done := make(chan *engine.Preview, 1)
	go func() {
		p, err := h.e.PreviewAwaitingUplink(context.Background(), rev, 10*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- p
	}()
	h.clk.BlockUntil(armed + 1) // it waits, with its deadline armed
	select {
	case <-done:
		t.Fatal("the preview did not wait for the uplink's address")
	default:
	}
	// an unrelated change of the host: it compiles again and goes on waiting
	h.k.AddLink("lan9", "02:00:00:00:09:01", "veth", true)
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("the preview stopped waiting at a change that did not bring the address")
	case <-time.After(50 * time.Millisecond):
	}
	// the address is back: the observer reports it, and the preview compiles without the problem
	h.k.SetAddr("wan0", "203.0.113.1/24")
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-done:
		if p == nil || hasProblem(p, compiler.CodeUplinkNoAddress) {
			t.Fatalf("preview %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the preview did not see the address the observer reported")
	}
	h.mustApply(rev)
}

// The wait is bounded: an uplink without an address after max still fails the apply with the
// compiler's problem, and a candidate that has another error does not wait at all.
func TestAnApplyWaitsForTheUplinkAddressOnlyForABoundedTime(t *testing.T) {
	h := newHarness(t)
	h.k.DelAddr("wan0", "203.0.113.1/24")
	h.start()
	rev := h.revision(nil)
	armed := h.clk.Pending()
	done := make(chan *engine.Preview, 1)
	go func() {
		p, err := h.e.PreviewAwaitingUplink(context.Background(), rev, 10*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- p
	}()
	h.clk.BlockUntil(armed + 1)
	h.clk.Advance(10 * time.Second)
	select {
	case p := <-done:
		if p == nil || !hasProblem(p, compiler.CodeUplinkNoAddress) {
			t.Fatalf("after the wait: %+v", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait has no bound")
	}

	// the uplink interface is not there at all: nothing to wait for
	h.k.RemoveLink("wan0")
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := h.e.PreviewAwaitingUplink(context.Background(), rev, time.Hour)
	if err != nil || !hasProblem(p, compiler.CodeUplinkMissing) {
		t.Fatalf("a missing uplink: %v %+v", err, p)
	}
}
