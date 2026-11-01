package engine_test

import (
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
)

// bufferedEvents returns the events of a type that the engine has published so far, the buffered ones included (what a subscriber
// that joins after the start sees: the API does).
func bufferedEvents(e *engine.Engine, typ string) []engine.Event {
	replay, _, stop := e.SubscribeFrom(0)
	defer stop()
	var out []engine.Event
	for _, ev := range replay {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// R2 of the audit of Phase 7: Store.Open rolls a revision back that waited for confirmation when the process ended, so a real
// restart finds nothing pending, and the engine published no event: no audit entry, and the setup stayed closed with an admin
// password and no configuration. The harness restarts the engine on the same Store object, which hid it.
func TestARestartInsideTheConfirmationWindowTellsThatTheRevisionWasRolledBack(t *testing.T) {
	h := newHarness(t)
	h.start()
	// the first revision of a setup waits for confirmation like any other (ForceConfirm, M5-03)
	r1 := h.mustApply(h.revision(nil), engine.ApplyOptions{ForceConfirm: true, ConfirmTimeout: time.Hour})
	if r1.Status != "pending_confirm" {
		t.Fatalf("%+v", r1)
	}

	h = reboot(h) // closes and reopens the store: Store.Open rolls the pending revision back
	h.start()
	s := h.barrier()
	if s.Revision != 0 || s.Pending != nil {
		t.Fatalf("after the restart: %+v", s)
	}
	evs := bufferedEvents(h.e, engine.EventRolledBack)
	if len(evs) != 1 || evs[0].Data["revision"] != r1.Revision || evs[0].Data["reason"] != "restart" {
		t.Fatalf("events: %+v", evs)
	}
}
