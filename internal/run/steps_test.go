package run_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

const basic = `
name: basic
target: {device: esp32-42}
steps:
  - {id: normal,  at: 0s,  profile: normal}
  - {id: slow,    at: 10s, fault: {latency: 200ms, jitter: 50ms}}
  - {id: lossy,   at: 20s, fault: {latency: 200ms, jitter: 50ms, loss: 10%}}
  - {id: cut,     at: 30s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 45s, restore: true}
`

// The steps run in the order of the timeline, not of the list: by their offset, and in list order at the same
// offset (plan §2.10); each is applied at its time on the monotonic clock and records when.
func TestStepsRunInTheOrderOfTheTimelineAtTheirTimes(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: order
target: {device: esp32-42}
steps:
  - {id: last,   at: 20s, rule: {action: drop, protocol: tcp, ports: [8884]}}
  - {id: second, at: 10s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: first,  at: 0s,  profile: normal}
  - {id: third,  at: 10s, fault: {loss: 1%}}
`)
	if r.Status != model.RunStatusRunning && r.Status != model.RunStatusPassed {
		t.Fatalf("a run on a free target starts at once: %s", r.Status)
	}
	h.waitStep(r.Id, "first", model.RunStepStatusApplied)
	if got := h.stepStatus(r.Id, "second"); got != model.RunStepStatusPending {
		t.Fatalf("the step at 10 s ran at 0 s: %s", got)
	}
	h.advance(9999 * time.Millisecond)
	// not yet: nothing may have run a millisecond early
	if got := h.stepStatus(r.Id, "second"); got != model.RunStepStatusPending {
		t.Fatalf("a step ran a millisecond early: %s", got)
	}
	h.advance(time.Millisecond)
	h.waitStep(r.Id, "third", model.RunStepStatusApplied)
	h.advance(10 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)

	if got := h.gw.logged(); strings.Join(got, ",") != "put first,put second,put third,put last,reset" {
		t.Errorf("the overlays were written in the order %v", got)
	}
	for id, off := range map[string]int{"first": 0, "second": 10000, "third": 10000, "last": 20000} {
		s := step(done, id)
		if s.OffsetMs == nil || *s.OffsetMs != off {
			t.Errorf("step %s was applied at %v ms, scheduled for %d", id, s.OffsetMs, off)
		}
		if s.AppliedAt == nil || s.Generation == nil || s.Overlay == nil || s.QueueWaitMs == nil {
			t.Errorf("step %s lacks its time stamp, generation, overlay or queue wait: %+v", id, s)
		}
	}
	// the report lists the steps in the order of the timeline
	var ids []string
	for _, s := range *done.Steps {
		ids = append(ids, s.Id)
	}
	if strings.Join(ids, ",") != "first,second,third,last" {
		t.Errorf("the steps of the report are %v", ids)
	}
	if len(h.gw.overlays()) != 0 {
		t.Errorf("a run that ended left overlays: %+v", h.gw.overlays())
	}
}

// Every step has the generation it was applied in; they grow along the timeline.
func TestEveryStepRecordsTheGenerationItWasAppliedIn(t *testing.T) {
	h := newHarness(t)
	r := h.start(basic)
	for _, d := range []time.Duration{10, 10, 10, 15} {
		h.advance(d * time.Second)
	}
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	var last int64
	for _, s := range *done.Steps {
		if s.Generation == nil || *s.Generation <= last {
			t.Errorf("step %s has generation %v after %d", s.Id, s.Generation, last)
		} else {
			last = *s.Generation
		}
	}
}

// A step with the key of an earlier one (same kind, target and selector) replaces its overlay: the
// parameters are complete, never merged (plan §2.10).
func TestAStepWithTheKeyOfAnEarlierOneReplacesItsOverlay(t *testing.T) {
	h := newHarness(t)
	r := h.start(basic)
	h.waitStep(r.Id, "normal", model.RunStepStatusApplied)
	h.advance(10 * time.Second)
	h.waitStep(r.Id, "slow", model.RunStepStatusApplied)
	h.advance(10 * time.Second)
	h.waitStep(r.Id, "lossy", model.RunStepStatusApplied)

	if got := h.gw.logged(); strings.Join(got, ",") != "put normal,put slow,replace lossy" {
		t.Fatalf("the writes were %v", got)
	}
	cur := h.get(r.Id)
	if *step(cur, "slow").Overlay != *step(cur, "lossy").Overlay {
		t.Errorf("the replacement is another overlay: %v and %v", step(cur, "slow").Overlay, step(cur, "lossy").Overlay)
	}
	faults := 0
	for _, ov := range h.gw.overlays() {
		if ov.Kind == model.OverlayKindFault {
			faults++
			if ov.Fault.Loss == nil || *ov.Fault.Loss != "10%" || ov.Run.Step != "lossy" {
				t.Errorf("the overlay is not the complete parameter set of the last step: %+v %+v", ov.Fault, ov.Run)
			}
		}
	}
	if faults != 1 {
		t.Errorf("%d fault overlays", faults)
	}
	h.advance(10 * time.Second)
	h.advance(15 * time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// The overlays belong to the run, with the step that wrote them; an owner of the run's kind is how they
// are told from a token's.
func TestTheOverlaysOfAStepAreOwnedByTheRun(t *testing.T) {
	h := newHarness(t)
	r := h.start(basic)
	h.waitStep(r.Id, "normal", model.RunStepStatusApplied)
	ovs := h.gw.overlays()
	if len(ovs) != 1 {
		t.Fatalf("%+v", ovs)
	}
	if ovs[0].Owner.Type != model.ActorTypeRun || ovs[0].Owner.Id != r.Id.String() || ovs[0].Run == nil || ovs[0].Run.Id != r.Id || ovs[0].Run.Step != "normal" {
		t.Errorf("owner %+v run %+v", ovs[0].Owner, ovs[0].Run)
	}
	if r.Owner != token {
		t.Errorf("the run is owned by %+v, not by who started it", r.Owner)
	}
	h.m.AbortOwned(context.Background(), nil, "test over")
}

// `remove` deletes the overlay under the key of an earlier step, `restore` all of the run's.
func TestRemoveDeletesOneStepsOverlayAndRestoreAllOfThem(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: remove
target: {device: esp32-42}
steps:
  - {id: lat,    at: 0s,  fault: {latency: 100ms}}
  - {id: drop,   at: 0s,  rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: undo,   at: 5s,  remove: lat}
  - {id: again,  at: 6s,  remove: lat}
  - {id: all,    at: 10s, restore: true}
  - {id: after,  at: 11s, fault: {loss: 5%}}
`)
	h.waitStep(r.Id, "drop", model.RunStepStatusApplied)
	if n := len(h.gw.overlays()); n != 2 {
		t.Fatalf("%d overlays", n)
	}
	h.advance(5 * time.Second)
	h.waitStep(r.Id, "undo", model.RunStepStatusApplied)
	if ovs := h.gw.overlays(); len(ovs) != 1 || ovs[0].Kind != model.OverlayKindRule {
		t.Fatalf("after remove: %+v", ovs)
	}
	h.advance(time.Second)
	h.waitStep(r.Id, "again", model.RunStepStatusSkipped)
	if s := step(h.get(r.Id), "again"); s.Error == nil || !has(*s.Error, "gone already") {
		t.Errorf("a second remove says %v", s.Error)
	}
	h.advance(4 * time.Second)
	h.waitStep(r.Id, "all", model.RunStepStatusApplied)
	if n := len(h.gw.overlays()); n != 0 {
		t.Fatalf("restore left %d overlays", n)
	}
	h.advance(time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// A step may narrow the target of the scenario to a device of its group, and the overlay it writes has the
// narrowed target; a tunnel fault or a WireGuard action has none.
func TestAStepNarrowsTheTargetAndATunnelStepHasNone(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: narrow
target: {network: lab-hub}
steps:
  - {id: one,  at: 0s, target: {device: lab-rA}, fault: {latency: 30ms}}
  - {id: net,  at: 0s, fault: {latency: 40ms}}
  - {id: peer, at: 0s, wireguard: {client: lab-rA, action: disable}}
`)
	h.waitStep(r.Id, "peer", model.RunStepStatusApplied)
	w := h.gw.written()
	if len(w) != 3 {
		t.Fatalf("%d writes", len(w))
	}
	if w[0].Request.Target == nil || w[0].Request.Target.Device == nil || *w[0].Request.Target.Device != idRA {
		t.Errorf("the narrowed target is %+v", w[0].Request.Target)
	}
	if w[1].Request.Target == nil || w[1].Request.Target.Network == nil || *w[1].Request.Target.Network != idHub {
		t.Errorf("the inherited target is %+v", w[1].Request.Target)
	}
	if w[2].Request.Target != nil {
		t.Errorf("a WireGuard action has no target: %+v", w[2].Request.Target)
	}
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// A step that waits behind other changes for more than 100 ms gets the warning step_late; at 100 ms it does
// not. The report has every step's queue wait (plan §2.10, §3.11).
func TestAStepThatWaitsMoreThan100msGetsTheWarningStepLate(t *testing.T) {
	h := newHarness(t)
	h.gw.queueWait = 100 * time.Millisecond
	r := h.start(`
name: late
target: {device: esp32-42}
steps:
  - {id: on-time, at: 0s, fault: {latency: 10ms}}
  - {id: late,    at: 5s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: gone,    at: 6s, remove: late}
`)
	h.waitStep(r.Id, "on-time", model.RunStepStatusApplied)
	h.gw.mu.Lock()
	h.gw.queueWait = 130 * time.Millisecond
	h.gw.mu.Unlock()
	h.advance(5 * time.Second)
	h.waitStep(r.Id, "late", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)

	for id, ms := range map[string]int{"on-time": 100, "late": 130, "gone": 130} {
		if s := step(done, id); s.QueueWaitMs == nil || *s.QueueWaitMs != ms {
			t.Errorf("step %s queue wait %v, want %d", id, s.QueueWaitMs, ms)
		}
	}
	ws := *done.Warnings
	if len(ws) != 2 || ws[0].Code != model.StepLate || *ws[0].Step != "late" || ws[1].Code != model.StepLate || *ws[1].Step != "gone" {
		t.Fatalf("warnings %+v", ws)
	}
	if !has(ws[0].Message, "130 ms") {
		t.Errorf("the warning says %q", ws[0].Message)
	}
}

// A step that falls due while the apply of the one before is still running is handed over late; more than 100 ms behind
// its schedule it gets the warning step_late too, one for the step, naming the step it waited for (plan §2.10).
func TestAStepHandedOverMoreThan100msLateGetsTheWarningStepLate(t *testing.T) {
	h := newHarness(t)
	h.gw.putDelay = func(step string) time.Duration {
		if step == "slow" {
			return 3 * time.Second
		}
		return 0
	}
	r := h.start(`
name: behind
target: {device: esp32-42}
steps:
  - {id: slow,  at: 0s, fault: {latency: 10ms}}
  - {id: after, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`)
	h.clk.BlockUntil(1) // the write of the first step is in the executor
	h.clk.Advance(3 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)

	after := step(done, "after")
	if after.StartOffsetMs == nil || *after.StartOffsetMs != 3000 || *after.ScheduledOffset != "1s" {
		t.Fatalf("the step behind the apply: %+v", after)
	}
	if after.QueueWaitMs == nil || *after.QueueWaitMs != 0 {
		t.Errorf("queue wait %v: the executor was not busy with the step, the timeline was", after.QueueWaitMs)
	}
	ws := warnings(done)
	if len(ws) != 1 || ws[0].Code != model.StepLate || ws[0].Step == nil || *ws[0].Step != "after" {
		t.Fatalf("warnings %+v", ws)
	}
	for _, want := range []string{"2000 ms late", "due at 1000 ms", "handed over at 3000 ms", "the step slow before it was still being applied until 3000 ms"} {
		if !has(ws[0].Message, want) {
			t.Errorf("the warning %q does not say %q", ws[0].Message, want)
		}
	}
	if ws[0].Overlay != nil {
		t.Errorf("a step that was not delayed by the executor names the overlay %v", ws[0].Overlay)
	}
}

// A step that is late for both reasons has one warning with both causes.
func TestAStepLateForBothReasonsHasOneWarning(t *testing.T) {
	h := newHarness(t)
	h.gw.queueWait = 130 * time.Millisecond // (the fake gateway reports it for every write)
	h.gw.putDelay = func(step string) time.Duration {
		if step == "slow" {
			return 3 * time.Second
		}
		return 0
	}
	r := h.start(`
name: both
target: {device: esp32-42}
steps:
  - {id: slow,   at: 0s, fault: {latency: 10ms}}
  - {id: behind, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`)
	h.clk.BlockUntil(1)
	h.clk.Advance(3 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	byStep := map[string][]model.RunWarning{}
	for _, w := range warnings(done) {
		byStep[*w.Step] = append(byStep[*w.Step], w)
	}
	if len(byStep) != 2 || len(byStep["slow"]) != 1 || len(byStep["behind"]) != 1 {
		t.Fatalf("one warning for each of the two steps, got %+v", warnings(done))
	}
	if m := byStep["slow"][0].Message; has(m, "handed to the executor") || !has(m, "waited 130 ms") {
		t.Errorf("the step that was on time: %q", m)
	}
	m := byStep["behind"][0]
	if !has(m.Message, "handed to the executor 2000 ms late") || !has(m.Message, "waited 130 ms for the executor") || m.Overlay == nil {
		t.Errorf("the step that is late twice: %+v", m)
	}
}

// 100 ms late is not late, as for the queue wait.
func TestAStepHandedOver100msLateIsNotLate(t *testing.T) {
	h := newHarness(t)
	h.gw.putDelay = func(step string) time.Duration {
		if step == "slow" {
			return 1100 * time.Millisecond
		}
		return 0
	}
	r := h.start(`
name: edge
target: {device: esp32-42}
steps:
  - {id: slow, at: 0s, fault: {latency: 10ms}}
  - {id: edge, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`)
	h.clk.BlockUntil(1)
	h.clk.Advance(1100 * time.Millisecond)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	if s := step(done, "edge"); *s.StartOffsetMs != 1100 || len(warnings(done)) != 0 {
		t.Errorf("start %v, warnings %+v", *s.StartOffsetMs, warnings(done))
	}
}

// A clock that jumps over the time of a step hands it over late, and the warning says the timeline woke late, not that
// an apply was running.
func TestAStepTheTimelineReachesLateSaysSo(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: jump
target: {device: esp32-42}
steps:
  - {id: one, at: 0s, fault: {latency: 10ms}}
  - {id: two, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`)
	h.waitStep(r.Id, "one", model.RunStepStatusApplied)
	h.advance(5 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := warnings(done)
	if len(ws) != 1 || *ws[0].Step != "two" || !has(ws[0].Message, "4000 ms late") || !has(ws[0].Message, "the timeline reached the step 4000 ms late") {
		t.Fatalf("warnings %+v", ws)
	}
}

// The counters that are read before a step takes an overlay away are on the timeline: the step is handed to the engine
// after them, `start_offset_ms` says so (and not the moment before the read), and a read that takes more than 100 ms
// makes the step late, with that as the cause.
func TestTheReadOfCountersBeforeAStepIsPartOfItsHandOver(t *testing.T) {
	for name, back := range map[string]string{
		"restore": "restore: true",
		"remove":  "remove: one",
		"replace": "fault: {latency: 20ms}", // the key of the first step: it replaces the overlay and ends its counters
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.obs.mu.Lock()
			h.obs.readDelay = 1500 * time.Millisecond
			h.obs.mu.Unlock()
			r := h.start(`
name: slowread
target: {device: esp32-42}
steps:
  - {id: one,  at: 0s, fault: {latency: 10ms}}
  - {id: back, at: 1s, ` + back + `}
`)
			h.waitStep(r.Id, "one", model.RunStepStatusApplied)
			h.advance(time.Second) // the step is due; its read of the counters begins
			h.clk.BlockUntil(1)
			if st := h.stepStatus(r.Id, "back"); st != model.RunStepStatusPending {
				t.Fatalf("the step is %s while the counters are read", st)
			}
			h.clk.Advance(1500 * time.Millisecond)
			h.waitStep(r.Id, "back", model.RunStepStatusApplied)
			// (a run that ends with an overlay reads its counters once more, slowly, at the end)
			h.advanceUntil("the end of the run", func() bool { return h.get(r.Id).Status == model.RunStatusPassed })
			done := h.get(r.Id)
			got := step(done, "back")
			if got.StartOffsetMs == nil || *got.StartOffsetMs != 2500 {
				t.Errorf("start_offset_ms %v, want 2500: the moment the change goes to the engine, after the read", got.StartOffsetMs)
			}
			ws := warnings(done)
			if len(ws) != 1 || *ws[0].Step != "back" || !has(ws[0].Message, "1500 ms late") || !has(ws[0].Message, "the counters of the overlays that go away were read for 1500 ms first") {
				t.Fatalf("warnings %+v", ws)
			}
		})
	}
}

// A step the engine refuses ends the run as error with the reason; the steps after it do not run, and
// whatever the run wrote is removed.
func TestAStepThatCannotBeExecutedEndsTheRunAsError(t *testing.T) {
	h := newHarness(t)
	h.gw.putErr = func(w engine.OverlayWrite) error {
		if w.Run.Step == "bad" {
			return fmt.Errorf("class limit reached")
		}
		return nil
	}
	r := h.start(`
name: bad
target: {device: esp32-42}
steps:
  - {id: ok,    at: 0s, fault: {latency: 10ms}}
  - {id: bad,   at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: never, at: 2s, fault: {loss: 1%}}
`)
	h.waitStep(r.Id, "ok", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusError)
	if done.Reason == nil || !has(*done.Reason, "step bad: class limit reached") {
		t.Errorf("reason %v", done.Reason)
	}
	if s := step(done, "bad"); s.Status != model.RunStepStatusError || s.Error == nil {
		t.Errorf("step bad %+v", s)
	}
	if s := step(done, "never"); s.Status != model.RunStepStatusSkipped {
		t.Errorf("step never %+v", s)
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// The overlays of a run that ends are gone even when the engine refuses the removal at first.
func TestTheRunRetriesTheRemovalOfItsOverlaysAndFailsIfItCannot(t *testing.T) {
	h := newHarness(t)
	h.gw.resetErr = fmt.Errorf("apply failed")
	r := h.start(`
name: stuck
target: {device: esp32-42}
steps:
  - {id: one, at: 0s, fault: {latency: 10ms}}
`)
	// three attempts, a second apart
	for i := 0; i < 2; i++ {
		h.advance(time.Second)
	}
	done := h.waitStatus(r.Id, model.RunStatusError)
	if done.Reason == nil || !has(*done.Reason, "could not be removed") {
		t.Errorf("reason %v", done.Reason)
	}
}

// A removal of the overlays that does not work is part of the end of the run whatever its status: an abort answers with the
// problem and not as a clean abort, the reason of the aborted, lease-expired and failed runs names it, and the manager
// keeps trying until the overlays are gone (plan §2.10: either way, its overlays are removed).
func TestARunWhoseOverlaysCannotBeRemovedSaysSoWhateverItsEnd(t *testing.T) {
	stuck := func(h *harness) { h.gw.setResetErr(fmt.Errorf("apply failed")) }
	removed := func(t *testing.T, h *harness, id uuid.UUID, status model.RunStatus, reasonStart string) {
		t.Helper()
		h.advanceUntil("the end of the run", func() bool { return h.get(id).Status == status })
		got := h.get(id)
		if got.Reason == nil || !strings.HasPrefix(*got.Reason, reasonStart) || !has(*got.Reason, "; the overlays of the run could not be removed: apply failed") {
			t.Errorf("reason %v", got.Reason)
		}
		if n := len(h.gw.overlays()); n != 1 {
			t.Fatalf("%d overlays are left, want the one the run could not remove", n)
		}
		// the overlays carry no lease of their own: the manager removes them once the engine lets it
		h.gw.setResetErr(nil)
		h.advanceUntil("the retry of the removal", func() bool { return len(h.gw.overlays()) == 0 })
	}

	t.Run("abort", func(t *testing.T) {
		h := newHarness(t)
		r := h.start(long("long", esp))
		h.waitStep(r.Id, "a", model.RunStepStatusApplied)
		stuck(h)
		type answer struct {
			a   run.Abort
			err error
		}
		ch := make(chan answer, 1)
		go func() {
			a, err := h.m.Abort(context.Background(), r.Id, &token)
			ch <- answer{a, err}
		}()
		var got answer
		h.advanceUntil("the answer of the abort", func() bool {
			select {
			case got = <-ch:
				return true
			default:
				return false
			}
		})
		ce, ok := errAs[*run.CleanupError](got.err)
		if !ok || ce.Run.Status != model.RunStatusAborted || ce.Run.Id != r.Id {
			t.Fatalf("an abort whose overlays stay answered %v", got.err)
		}
		removed(t, h, r.Id, model.RunStatusAborted, "aborted by ci")
	})

	t.Run("lease", func(t *testing.T) {
		h := newHarness(t)
		r := h.start(long("long", esp), func(c *run.CreateRequest) { c.Lease = 30 * time.Second })
		h.waitStep(r.Id, "a", model.RunStepStatusApplied)
		stuck(h)
		h.clk.Advance(30 * time.Second)
		removed(t, h, r.Id, model.RunStatusAborted, "lease expired")
	})

	t.Run("error", func(t *testing.T) {
		h := newHarness(t)
		h.gw.putErr = func(w engine.OverlayWrite) error {
			if w.Run.Step == "bad" {
				return fmt.Errorf("class limit reached")
			}
			return nil
		}
		r := h.start(`
name: bad
target: {device: esp32-42}
steps:
  - {id: ok,  at: 0s, fault: {latency: 10ms}}
  - {id: bad, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`)
		h.waitStep(r.Id, "ok", model.RunStepStatusApplied)
		stuck(h)
		h.advance(time.Second)
		removed(t, h, r.Id, model.RunStatusError, "step bad: class limit reached")
	})
}

// A removal that works on a later attempt is no failure: the run keeps its verdict.
func TestARemovalOfTheOverlaysThatWorksOnALaterAttemptKeepsTheVerdict(t *testing.T) {
	h := newHarness(t)
	h.gw.resetFailures = 1
	r := h.start(`
name: flaky
target: {device: esp32-42}
steps:
  - {id: one, at: 0s, fault: {latency: 10ms}}
`)
	h.advanceUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	done := h.get(r.Id)
	if done.Status != model.RunStatusPassed || done.Reason != nil {
		t.Errorf("%s %v", done.Status, done.Reason)
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// Parameters change a stored or inline scenario before it is checked; an inline scenario may leave out what
// they give.
func TestParametersSetTheTargetOfAnInlineScenario(t *testing.T) {
	h := newHarness(t)
	sc := inline(t, `
name: param
target: {device: lab-host}
steps:
  - {id: lat, at: 0s, fault: {latency: 10ms}}
`)
	r, err := h.m.Create(context.Background(), run.CreateRequest{Inline: sc, Queue: true, Owner: token,
		Parameters: map[string]string{"target.device": "esp32-42", "steps.lat.fault.latency": "250ms"}})
	if err != nil {
		t.Fatal(err)
	}
	h.waitStatus(r.Id, model.RunStatusPassed)
	w := h.gw.written()
	if len(w) != 1 || *w[0].Request.Target.Device != idESP || *w[0].Request.Fault.Latency != "250ms" {
		t.Fatalf("%+v", w[0].Request)
	}
	got := h.get(r.Id)
	if got.Snapshot == nil || *got.Snapshot.Target.Device != idESP || got.Target.Device == nil || *got.Target.Device != idESP {
		t.Errorf("the snapshot is the scenario as executed: %+v", got.Snapshot)
	}
	if !got.Scenario.Inline || got.Scenario.Id != nil || got.Scenario.Name != "param" {
		t.Errorf("scenario %+v", got.Scenario)
	}
}

func TestAParameterThatNamesNothingIsRefused(t *testing.T) {
	h := newHarness(t)
	_, err := h.m.Create(context.Background(), run.CreateRequest{Inline: inline(t, basic), Queue: true, Owner: token,
		Parameters: map[string]string{"steps.nope.fault.latency": "5ms"}})
	if err == nil || !has(err.Error(), "/parameters/steps.nope.fault.latency") {
		t.Fatalf("%v", err)
	}
}

// A stored scenario is found by name or UUID in the active revision, and run as it is; the example's
// scenario captures, which a gateway without a capture volume cannot (M17).
func TestAStoredScenarioThatCapturesNeedsACaptureVolume(t *testing.T) {
	h := newHarness(t)
	for _, ref := range []string{"mqtt-outage", "3d4e5f6a-7b8c-4d9e-0f1a-2b3c4d5e6f71"} {
		_, err := h.m.Create(context.Background(), run.CreateRequest{Scenario: ref, Queue: true, Owner: token})
		u, ok := errAs[*run.UnsupportedError](err)
		if !ok || u.Milestone != "" || !has(err.Error(), "capture volume") {
			t.Errorf("%s: %v", ref, err)
		}
	}
	_, err := h.m.Create(context.Background(), run.CreateRequest{Scenario: "nope", Queue: true, Owner: token})
	if err == nil || !has(err.Error(), "no such scenario") {
		t.Errorf("%v", err)
	}
}

func TestStepTypesAndChecksOfLaterMilestonesAreRefused(t *testing.T) {
	h := newHarness(t)
	for name, tc := range map[string]struct{ yaml, milestone string }{
		"tls check": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
checks: [{tls_rejected: {protocol: tcp, ports: [8883]}, window: {from: a, within: 30s}}]
`, "M21"},
		"check for a host name": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
checks: [{reconnected: {destination: {hostname: broker.example.com}}, window: {from: a, within: 30s}}]
`, "M20"},
		"check that excepts a host name": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
checks: [{traffic_not_seen: {except: [{hostname: broker.example.com}]}, window: {from: a, within: 30s}}]
`, "M20"},
		"capture flag": {`
name: c
target: {device: esp32-42}
capture: true
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
`, ""},
		"capture step": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, capture: start}, {id: b, at: 1s, capture: stop}]
`, ""},
		"dns step": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, dns: {names: [broker.example.com], action: servfail}}]
`, "M20"},
		"tls step": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, tls: {case: untrusted_ca}}]
`, "M21"},
		"dhcp step": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, dhcp: {action: silence}}]
`, "M23"},
		"wait for a host name": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, wait: {for: connected, destination: {hostname: broker.example.com}, timeout: 5s}}]
`, "M20"},
	} {
		sc, err := domain.DecodeScenario([]byte(tc.yaml), domain.FormatYAML)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = h.m.Create(context.Background(), run.CreateRequest{Inline: sc, Queue: true, Owner: token})
		u, ok := errAs[*run.UnsupportedError](err)
		if !ok || u.Milestone != tc.milestone {
			t.Errorf("%s: %v, want milestone %s", name, err, tc.milestone)
		}
	}
	if got := len(h.m.List(run.Filter{})); got != 0 {
		t.Errorf("a refused scenario made %d runs", got)
	}
}

// A scenario that does not fit the active revision is refused with the problems, and makes no run.
func TestAScenarioWithAnUnknownReferenceIsRefused(t *testing.T) {
	h := newHarness(t)
	_, err := h.m.Create(context.Background(), run.CreateRequest{Queue: true, Owner: token, Inline: inline(t, `
name: x
target: {device: nobody}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
`)})
	if err == nil || !has(err.Error(), "unknown_reference") {
		t.Fatalf("%v", err)
	}
	if got := len(h.m.List(run.Filter{})); got != 0 {
		t.Errorf("%d runs", got)
	}
}
