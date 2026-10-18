package run_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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
// scenario captures, which arrives with M17.
func TestAStoredScenarioNeedsAllItsMilestones(t *testing.T) {
	h := newHarness(t)
	for _, ref := range []string{"mqtt-outage", "3d4e5f6a-7b8c-4d9e-0f1a-2b3c4d5e6f71"} {
		_, err := h.m.Create(context.Background(), run.CreateRequest{Scenario: ref, Queue: true, Owner: token})
		u, ok := errAs[*run.UnsupportedError](err)
		if !ok || u.Milestone != "M17" {
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
`, "M17"},
		"capture step": {`
name: c
target: {device: esp32-42}
steps: [{id: a, at: 0s, capture: start}]
`, "M17"},
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
