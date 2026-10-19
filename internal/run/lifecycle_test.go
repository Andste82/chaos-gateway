package run_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// long is a run that stays for a while: one step now, one at 100 s.
func long(name, target string) string {
	return `
name: ` + name + `
target: ` + target + `
steps:
  - {id: a, at: 0s,   fault: {latency: 10ms}}
  - {id: b, at: 100s, fault: {loss: 1%}}
`
}

const (
	esp   = "{device: esp32-42}"
	host  = "{device: lab-host}"
	group = "{group: sensors}"
	iot   = "{network: IoT}"
)

// Aborting a run ends it as aborted and removes its overlays before the call returns; the steps that did not run
// are skipped.
func TestAbortRemovesTheOverlaysOfTheRun(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp))
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	if len(h.gw.overlays()) != 1 {
		t.Fatalf("%+v", h.gw.overlays())
	}
	got, err := h.m.Abort(context.Background(), r.Id, &token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Run.Status != model.RunStatusAborted || got.Run.Reason == nil || *got.Run.Reason != "aborted by ci" || got.Run.EndedAt == nil {
		t.Errorf("%+v", got.Run)
	}
	if got.Generation == 0 {
		t.Error("the abort says no generation removed the overlays")
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays outlived the run", n)
	}
	if s := step(got.Run, "b"); s.Status != model.RunStepStatusSkipped {
		t.Errorf("step b %+v", s)
	}
	// aborting again changes nothing
	again, err := h.m.Abort(context.Background(), r.Id, nil)
	if err != nil || again.Run.Status != model.RunStatusAborted || *again.Run.Reason != "aborted by ci" {
		t.Errorf("%v %+v", err, again.Run)
	}
}

func TestAnAbortOfAQueuedRunNeverStartsIt(t *testing.T) {
	h := newHarness(t)
	first := h.start(long("first", esp))
	h.waitStep(first.Id, "a", model.RunStepStatusApplied)
	second := h.start(long("second", esp))
	if second.Status != model.RunStatusQueued {
		t.Fatalf("a second run on the same target is %s", second.Status)
	}
	got, err := h.m.Abort(context.Background(), second.Id, nil)
	if err != nil || got.Run.Status != model.RunStatusAborted || got.Run.StartedAt != nil {
		t.Fatalf("%v %+v", err, got.Run)
	}
	for _, e := range h.gw.logged() {
		if strings.Contains(e, "second") {
			t.Errorf("the aborted run wrote %v", e)
		}
	}
	if h.get(first.Id).Status != model.RunStatusRunning {
		t.Error("aborting the queued run stopped the one that runs")
	}
}

func TestAnAbortIsLimitedToTheOwnerOfTheRunForAScopedCaller(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp))
	other := model.Actor{Type: model.ActorTypeToken, Id: "tok-2"}
	if _, err := h.m.Abort(context.Background(), r.Id, &other); !errors.Is(err, run.ErrForbidden) {
		t.Errorf("%v", err)
	}
	if _, err := h.m.Renew(r.Id, &other); !errors.Is(err, run.ErrForbidden) {
		t.Errorf("%v", err)
	}
	if _, err := h.m.Abort(context.Background(), uuid.New(), nil); !errors.Is(err, run.ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := h.m.Abort(context.Background(), r.Id, &token); err != nil {
		t.Error(err)
	}
}

// A client that disconnects does not stop its run: the run does not depend on the request that made it.
func TestADisconnectingClientDoesNotStopARun(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	r, err := h.m.Create(ctx, run.CreateRequest{Inline: inline(t, long("long", esp)), Queue: true, Owner: token})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	h.advance(100 * time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// A run with a lease needs its client to renew it; when the lease runs out the run is aborted and its
// overlays are removed (plan §2.10). The lease runs on the monotonic clock.
func TestAnExpiredLeaseAbortsTheRunAndARenewalKeepsItAlive(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp), func(c *run.CreateRequest) { c.Lease = 30 * time.Second })
	if r.LeaseExpiresAt == nil || !r.LeaseExpiresAt.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("lease %v", r.LeaseExpiresAt)
	}
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	h.clk.Advance(20 * time.Second)
	renewed, err := h.m.Renew(r.Id, &token)
	if err != nil || !renewed.LeaseExpiresAt.Equal(t0.Add(50*time.Second)) {
		t.Fatalf("%v %v", err, renewed.LeaseExpiresAt)
	}
	// the wall clock jumping does not matter
	h.clk.JumpWall(time.Hour)
	h.clk.Advance(29 * time.Second)
	// The timer fires inside Advance and cancels the run there, but the run is `running` until it has cleaned up, so the
	// status says nothing yet. A renewal does: it is refused for a run that is cancelled, so it works only if the
	// original deadline (30 s) did not fire, and it moves the deadline to 49 s + 30 s.
	want := h.clk.Now().Add(30 * time.Second)
	again, err := h.m.Renew(r.Id, &token)
	if err != nil || !again.LeaseExpiresAt.Equal(want) || again.Status != model.RunStatusRunning {
		t.Fatalf("the renewal 29 s after the last one: %v %+v", err, again)
	}
	h.clk.Advance(30*time.Second - time.Nanosecond)
	if _, err := h.m.Renew(r.Id, &token); err != nil {
		t.Fatalf("the lease ran out a nanosecond early: %v", err)
	}
	h.clk.Advance(30 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusAborted)
	if done.Reason == nil || *done.Reason != "lease expired" {
		t.Errorf("reason %v", done.Reason)
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays outlived the lease", n)
	}
	if _, err := h.m.Renew(r.Id, nil); !errors.Is(err, run.ErrEnded) {
		t.Errorf("renewing an ended run: %v", err)
	}
}

// A run whose lease has run out (or that is being aborted) is cancelled but stays `running` until it has removed its
// overlays; a renewal in that time is refused and does not give it a new lease.
func TestARunThatIsBeingAbortedCannotBeRenewed(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp), func(c *run.CreateRequest) { c.Lease = 30 * time.Second })
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	hold := make(chan struct{})
	h.gw.setResetBlock(hold) // the removal of the overlays waits, so the run is cancelled and still running
	h.clk.Advance(30 * time.Second)
	if got := h.get(r.Id); got.Status != model.RunStatusRunning {
		t.Fatalf("the run is %s while its overlays are still there", got.Status)
	}
	_, err := h.m.Renew(r.Id, &token)
	if !errors.Is(err, run.ErrEnded) || !has(err.Error(), "lease expired") {
		t.Errorf("renewing a run that is being aborted: %v", err)
	}
	if got := h.get(r.Id); !got.LeaseExpiresAt.Equal(t0.Add(30 * time.Second)) {
		t.Errorf("the lease of the run moved to %v", got.LeaseExpiresAt)
	}
	close(hold)
	done := h.waitStatus(r.Id, model.RunStatusAborted)
	if *done.Reason != "lease expired" {
		t.Errorf("reason %v", *done.Reason)
	}
}

func TestARunWithoutALeaseCannotBeRenewed(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp))
	if _, err := h.m.Renew(r.Id, nil); !errors.Is(err, run.ErrNoLease) {
		t.Errorf("%v", err)
	}
}

// The lease of a run that waits in the queue runs too: a client that has gone does not leave a run that starts later.
func TestTheLeaseOfAQueuedRunRunsOut(t *testing.T) {
	h := newHarness(t)
	first := h.start(long("first", esp))
	h.waitStep(first.Id, "a", model.RunStepStatusApplied)
	second := h.start(long("second", esp), func(c *run.CreateRequest) { c.Lease = 5 * time.Second })
	h.clk.Advance(5 * time.Second)
	done := h.waitStatus(second.Id, model.RunStatusAborted)
	if *done.Reason != "lease expired" || done.StartedAt != nil {
		t.Errorf("%+v", done)
	}
}

// One run per target: a second run on the same target waits in queued and starts when the first has ended; runs on
// disjoint targets go in parallel; with queue=false the second is refused with the run that holds the target.
func TestASecondRunOnTheSameTargetWaitsInQueued(t *testing.T) {
	h := newHarness(t)
	first := h.start(long("first", esp))
	h.waitStep(first.Id, "a", model.RunStepStatusApplied)

	second := h.start(long("second", esp))
	if second.Status != model.RunStatusQueued {
		t.Fatalf("second: %s", second.Status)
	}
	if st := h.get(second.Id).Status; st != model.RunStatusQueued {
		t.Fatalf("second is %s while the first runs", st)
	}
	// another device is another target
	other := h.start(long("other", host))
	if other.Status != model.RunStatusRunning {
		t.Fatalf("a disjoint target waits: %s", other.Status)
	}

	_, err := h.m.Create(context.Background(), run.CreateRequest{Inline: inline(t, long("refused", esp)), Queue: false, Owner: token})
	busy, ok := errAs[*run.TargetBusyError](err)
	if !ok || busy.Run != first.Id {
		t.Fatalf("queue=false: %v", err)
	}

	running, queued, ids := h.m.Busy()
	if running != 2 || queued != 1 || len(ids) != 3 {
		t.Errorf("busy %d %d %v", running, queued, ids)
	}
	if got := h.m.List(run.Filter{Status: model.RunStatusQueued}); len(got) != 1 || got[0].Id != second.Id || got[0].Steps != nil {
		t.Errorf("the list of queued runs: %+v", got)
	}

	// the first ends, the second starts
	h.clk.BlockUntil(2) // the timers of the two steps at 100 s
	h.clk.Advance(100 * time.Second)
	h.waitStatus(first.Id, model.RunStatusPassed)
	h.waitStatus(second.Id, model.RunStatusRunning, model.RunStatusPassed)
	if ru, _ := h.m.Get(second.Id); ru.StartedAt == nil {
		t.Error("the second run has no start time")
	}
	h.m.AbortOwned(context.Background(), nil, "test over")
}

// A group, a network and a device that lies in them overlap, and a run does not start ahead of an earlier one it
// would have waited for.
func TestRunsOnOverlappingTargetsWaitInTheOrderTheyCameIn(t *testing.T) {
	h := newHarness(t)
	a := h.start(long("a", esp))
	h.waitStep(a.Id, "a", model.RunStepStatusApplied)
	b := h.start(long("b", group)) // the group of esp32-42
	c := h.start(long("c", esp))   // the device again
	d := h.start(long("d", iot))   // the network the device lies in
	e := h.start(long("e", host))  // another network
	for id, want := range map[uuid.UUID]model.RunStatus{b.Id: model.RunStatusQueued, c.Id: model.RunStatusQueued, d.Id: model.RunStatusQueued, e.Id: model.RunStatusRunning} {
		if st := h.get(id).Status; st != want {
			t.Errorf("run %s is %s, want %s", id, st, want)
		}
	}
	h.clk.BlockUntil(2) // the steps of a and e at 100 s
	h.clk.Advance(100 * time.Second)
	h.waitStatus(a.Id, model.RunStatusPassed)
	// b is the first in line and stays for 100 s; c and d overlap it and stay queued
	h.waitStatus(b.Id, model.RunStatusRunning)
	for _, id := range []uuid.UUID{c.Id, d.Id} {
		if st := h.get(id).Status; st != model.RunStatusQueued {
			t.Errorf("a later run overtook b: %s", st)
		}
	}
	h.waitStep(b.Id, "a", model.RunStepStatusApplied)
	h.clk.BlockUntil(1)
	h.clk.Advance(100 * time.Second)
	h.waitStatus(b.Id, model.RunStatusPassed)
	// c is next, d waits for c (both want the device)
	h.waitStatus(c.Id, model.RunStatusRunning)
	if st := h.get(d.Id).Status; st != model.RunStatusQueued {
		t.Errorf("d: %s", st)
	}
	h.m.AbortOwned(context.Background(), nil, "test over")
}

// Reset aborts the runs of an owner, or all of them, and says how many.
func TestAbortOwnedAbortsTheRunsOfOneOwnerOrAll(t *testing.T) {
	h := newHarness(t)
	mine := h.start(long("mine", esp))
	theirs := h.start(long("theirs", host), func(c *run.CreateRequest) { c.Owner = model.Actor{Type: model.ActorTypeToken, Id: "tok-2"} })
	queued := h.start(long("queued", esp))
	h.waitStep(mine.Id, "a", model.RunStepStatusApplied)

	n, removed, gen := h.m.AbortOwned(context.Background(), &token, "reset")
	if n != 2 || gen == 0 {
		t.Errorf("aborted %d runs, generation %d", n, gen)
	}
	// the overlay of the run that had written one; the queued run had none
	if removed != 1 {
		t.Errorf("the aborted runs removed %d overlays, want 1", removed)
	}
	if h.get(mine.Id).Status != model.RunStatusAborted || h.get(queued.Id).Status != model.RunStatusAborted {
		t.Error("the owner's runs are not aborted")
	}
	if st := h.get(theirs.Id).Status; st != model.RunStatusRunning {
		t.Errorf("another owner's run is %s", st)
	}
	if n, _, _ := h.m.AbortOwned(context.Background(), nil, "reset"); n != 1 {
		t.Errorf("all: %d", n)
	}
	if st := h.get(theirs.Id).Status; st != model.RunStatusAborted {
		t.Errorf("%s", st)
	}
}

// ---- preconditions

func TestAFailedPreconditionEndsTheRunAsErrorWithTheReason(t *testing.T) {
	h := newHarness(t)
	h.gw.setOnline(idESP, false)
	r := h.start(long("offline", esp))
	done := h.waitStatus(r.Id, model.RunStatusError)
	if done.Reason == nil || *done.Reason != "precondition target_online failed: device esp32-42 is offline" {
		t.Errorf("reason %v", done.Reason)
	}
	if got := h.gw.logged(); len(got) != 1 || got[0] != "reset" {
		t.Errorf("the run wrote %v", got)
	}
	if s := step(done, "a"); s.Status != model.RunStepStatusSkipped {
		t.Errorf("%+v", s)
	}
}

func TestAScenarioMaySkipAPrecondition(t *testing.T) {
	h := newHarness(t)
	h.gw.setOnline(idESP, false)
	r := h.start(`
name: skip
target: {device: esp32-42}
preconditions: {skip: [target_online]}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
`)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

func TestAGroupOrNetworkTargetNeedsOneDeviceOnline(t *testing.T) {
	h := newHarness(t)
	h.gw.setOnline(idESP, false)
	r := h.start(long("net", iot))
	done := h.waitStatus(r.Id, model.RunStatusError)
	if !has(*done.Reason, "target_online") || !has(*done.Reason, "no device of the network IoT is online") {
		t.Errorf("%v", *done.Reason)
	}
}

func TestOverlaysOfOtherOwnersOnTheTargetFailTheBaseline(t *testing.T) {
	h := newHarness(t)
	foreign := model.Overlay{Id: uuid.New(), Kind: model.OverlayKindFault, Owner: model.Owner{Type: model.ActorTypeToken, Id: "tok-9", Name: ptr("lab")},
		Target: &model.Scope{Device: ptr(idESP)}}
	elsewhere := model.Overlay{Id: uuid.New(), Kind: model.OverlayKindFault, Owner: token, Target: &model.Scope{Device: ptr(idLab)}}
	h.gw.setForeign(elsewhere, foreign)

	r := h.start(long("clash", esp))
	done := h.waitStatus(r.Id, model.RunStatusError)
	if !has(*done.Reason, "precondition no_foreign_overlays failed") || !has(*done.Reason, foreign.Id.String()) || has(*done.Reason, elsewhere.Id.String()) {
		t.Errorf("%v", *done.Reason)
	}
	// the network the device lies in is hit by an overlay on the device; a global overlay hits everything
	for name, ov := range map[string]model.Overlay{
		"network": {Id: uuid.New(), Kind: model.OverlayKindFault, Owner: token, Target: &model.Scope{Network: ptr(idIoT)}},
		"global":  {Id: uuid.New(), Kind: model.OverlayKindFault, Owner: token, Target: &model.Scope{Global: ptr(model.ScopeGlobal(true))}},
		"group":   {Id: uuid.New(), Kind: model.OverlayKindFault, Owner: token, Target: &model.Scope{Group: ptr(idSens)}},
	} {
		h.gw.setForeign(ov)
		r := h.start(long("clash-"+name, esp))
		if done := h.waitStatus(r.Id, model.RunStatusError); !has(*done.Reason, "no_foreign_overlays") {
			t.Errorf("%s: %v", name, *done.Reason)
		}
	}
	// a tunnel overlay names its client
	h.gw.setForeign(model.Overlay{Id: uuid.New(), Kind: model.OverlayKindWireguard, Owner: token, Wireguard: &model.WireGuardAction{Client: ptr(idRA), Action: "disable"}})
	r = h.start(long("tunnel", esp))
	h.waitStatus(r.Id, model.RunStatusRunning, model.RunStatusPassed)
	h.m.AbortOwned(context.Background(), nil, "test over")
	r = h.start(long("tunnel2", "{device: lab-rA}"))
	if done := h.waitStatus(r.Id, model.RunStatusError); !has(*done.Reason, "no_foreign_overlays") {
		t.Errorf("%v", *done.Reason)
	}
}

// The baseline of a scenario with host names: the device uses the gateway's DNS.
func TestAScenarioWithHostNamesNeedsADeviceThatUsesTheGatewaysDNS(t *testing.T) {
	const named = `
name: named
target: {device: esp32-42}
steps: [{id: a, at: 0s, fault: {latency: 10ms, destination: {hostname: broker.example.com}}}]
`
	h := newHarness(t)
	r := h.start(named)
	done := h.waitStatus(r.Id, model.RunStatusError)
	if !has(*done.Reason, "precondition uses_gateway_dns failed: device esp32-42 has not asked the gateway's DNS and has no lease") {
		t.Errorf("%v", *done.Reason)
	}

	// it has asked the gateway
	h.obs.dns[idESP] = true
	h.waitStatus(h.start(named).Id, model.RunStatusPassed)

	// or has a lease of a network that hands out the gateway as the DNS server
	h.obs.dns[idESP] = false
	h.gw.edit(func(s *engine.Snapshot) {
		s.Devices[0].Lease = &model.DhcpLease{}
		s.Bridges = nil
	})
	h.waitStatus(h.start(named).Id, model.RunStatusPassed)

	// a scenario without names does not ask
	h.waitStatus(h.start(basic).Id, model.RunStatusRunning, model.RunStatusPassed)
	h.m.AbortOwned(context.Background(), nil, "test over")
}

// ---- wait steps

// A wait step stops the timeline until its condition holds; the steps after it shift by the time it waited.
func TestAWaitStepShiftsTheLaterSteps(t *testing.T) {
	h := newHarness(t)
	h.gw.setOnline(idESP, false)
	r := h.start(`
name: wait
target: {device: esp32-42}
preconditions: {skip: [target_online]}
steps:
  - {id: first, at: 0s,  fault: {latency: 10ms}}
  - {id: up,    at: 5s,  wait: {for: online, timeout: 60s}}
  - {id: later, at: 10s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`)
	h.waitStep(r.Id, "first", model.RunStepStatusApplied)
	h.advance(5 * time.Second)
	// the wait polls every 250 ms
	for i := 0; i < 28; i++ {
		h.advance(250 * time.Millisecond)
	}
	if st := h.stepStatus(r.Id, "up"); st != model.RunStepStatusPending {
		t.Fatalf("the wait ended with the device offline: %s", st)
	}
	h.clk.BlockUntil(1) // the poll at 12 s has looked, and found it offline
	h.gw.setOnline(idESP, true)
	h.advance(250 * time.Millisecond) // 12.25 s: the poll sees it
	h.waitStep(r.Id, "up", model.RunStepStatusApplied)
	if s := step(h.get(r.Id), "up"); *s.OffsetMs != 12250 {
		t.Errorf("the wait ended at %d ms", *s.OffsetMs)
	}
	// "later" was due at 10 s, and now is 7.25 s late: 17.25 s
	h.advance(4999 * time.Millisecond)
	if st := h.stepStatus(r.Id, "later"); st != model.RunStepStatusPending {
		t.Fatalf("the step after the wait was not shifted: %s", st)
	}
	h.advance(time.Millisecond)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	if s := step(done, "later"); *s.OffsetMs != 17250 {
		t.Errorf("the step after the wait was applied at %d ms", *s.OffsetMs)
	}
}

// A wait that times out ends the run as error, like a failed precondition, and removes the overlays.
func TestAWaitThatTimesOutEndsTheRunAsError(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: timeout
target: {device: esp32-42}
steps:
  - {id: first, at: 0s, fault: {latency: 10ms}}
  - {id: conn,  at: 1s, wait: {for: connected, protocol: tcp, ports: [8883], timeout: 2s}}
  - {id: never, at: 2s, fault: {loss: 1%}}
`)
	h.waitStep(r.Id, "first", model.RunStepStatusApplied)
	h.advance(time.Second)
	for i := 0; i < 8; i++ {
		h.advance(250 * time.Millisecond)
	}
	done := h.waitStatus(r.Id, model.RunStatusError)
	if !has(*done.Reason, "step conn: the wait for a new connection timed out after 2s") {
		t.Errorf("reason %v", *done.Reason)
	}
	if s := step(done, "never"); s.Status != model.RunStepStatusSkipped {
		t.Errorf("%+v", s)
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays", n)
	}
}

// `connected` wants a connection that is new since the wait began, from the target, matching the selector,
// and established.
func TestAWaitForAConnectionNeedsANewEstablishedMatchingOne(t *testing.T) {
	h := newHarness(t)
	h.obs.addFlow(engine.Flow{ID: "old", Device: idESP, Network: idIoT, Protocol: "tcp", DPort: 8883, State: "ESTABLISHED"})
	r := h.start(`
name: conn
target: {device: esp32-42}
steps:
  - {id: conn,  at: 0s, wait: {for: connected, protocol: tcp, ports: [8883], timeout: 30s}}
  - {id: after, at: 1s, fault: {latency: 10ms}}
`)
	poll := func() {
		h.advance(250 * time.Millisecond)
	}
	for i := 0; i < 3; i++ {
		poll()
	}
	for _, f := range []engine.Flow{
		{ID: "wrong-port", Device: idESP, Protocol: "tcp", DPort: 443, State: "ESTABLISHED"},
		{ID: "other-device", Device: idLab, Protocol: "tcp", DPort: 8883, State: "ESTABLISHED"},
		{ID: "handshake", Device: idESP, Protocol: "tcp", DPort: 8883, State: "SYN_SENT"},
		{ID: "udp", Device: idESP, Protocol: "udp", DPort: 8883, State: "UNREPLIED"},
	} {
		h.obs.addFlow(f)
	}
	poll()
	poll()
	if st := h.stepStatus(r.Id, "conn"); st != model.RunStepStatusPending {
		t.Fatalf("the wait ended on %s", st)
	}
	h.obs.addFlow(engine.Flow{ID: "new", Device: idESP, Network: idIoT, Protocol: "tcp", DPort: 8883, State: "ESTABLISHED"})
	poll()
	h.waitStep(r.Id, "conn", model.RunStepStatusApplied)
	h.advance(1500 * time.Millisecond)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

func TestAWaitForADNSQueryNeedsAQueryAfterTheWaitBegan(t *testing.T) {
	h := newHarness(t)
	h.obs.queries = append(h.obs.queries, query{device: idESP, name: "broker.example.com", at: t0.Add(-time.Minute)})
	r := h.start(`
name: dnswait
target: {device: esp32-42}
preconditions: {skip: [uses_gateway_dns]}
steps:
  - {id: q, at: 0s, wait: {for: dns_query, name: broker.example.com, timeout: 30s}}
`)
	h.advance(250 * time.Millisecond)
	h.advance(250 * time.Millisecond)
	if st := h.stepStatus(r.Id, "q"); st != model.RunStepStatusPending {
		t.Fatalf("a query from before the wait ended it: %s", st)
	}
	h.obs.mu.Lock()
	h.obs.queries = append(h.obs.queries, query{device: idESP, name: "broker.example.com", at: h.clk.Now()})
	h.obs.mu.Unlock()
	h.advance(250 * time.Millisecond)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// ---- the end of a run

// A run is a goroutine and a lease, not a thing the process owns: Close ends the runs that are still there.
func TestCloseAbortsTheRunsThatAreStillThere(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp))
	q := h.start(long("queued", esp))
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	h.m.Close(context.Background())
	for _, id := range []uuid.UUID{r.Id, q.Id} {
		got, err := h.m.Get(id)
		if err != nil || got.Status != model.RunStatusAborted || *got.Reason != "service stopping" {
			t.Errorf("%v %+v", err, got)
		}
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays", n)
	}
	if _, err := h.m.Create(context.Background(), run.CreateRequest{Inline: inline(t, long("x", esp)), Queue: true, Owner: token}); !errors.Is(err, run.ErrClosed) {
		t.Errorf("%v", err)
	}
}
