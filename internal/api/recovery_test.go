package api_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// brokenKernel is an executor that cannot create a bridge: every revision with a test network fails to apply, the safe
// configuration (management access only) does not need one.
type brokenKernel struct {
	apply.Exec
	fixed atomic.Bool
}

func (b *brokenKernel) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	for _, op := range ops {
		if b.fixed.Load() {
			break
		}
		if l, ok := op.(*executor.Links); ok {
			for _, e := range l.Entries {
				if e.Action == "add_bridge" {
					return executor.Outcome{}, errors.New("RTNETLINK answers: Operation not supported")
				}
			}
		}
	}
	return b.Exec.Do(ctx, ops...)
}

// inSafeMode starts a gateway whose last revision (2) and whose last known good one (1) cannot be applied, and returns it
// once GET /state says safe mode, with the kernel that broken.fixed repairs.
func inSafeMode(t *testing.T, confirm time.Duration) (*gw, *brokenKernel, map[string]any) {
	t.Helper()
	root := t.TempDir()
	g1 := newGW(t, func(o *options) { o.root = root })
	g1.finishSetup()
	r2 := g1.mustPatch(map[string]any{"settings": map[string]any{"commit_confirm_timeout": "45s"}})
	if r := g1.apply(r2); r.Status != 200 {
		t.Fatalf("apply: %d %s", r.Status, r.Body)
	}
	g1.close()

	broken := &brokenKernel{}
	g := newGW(t, func(o *options) {
		o.root = root
		o.retryDelay = 50 * time.Millisecond
		o.execWrap = func(e apply.Exec) apply.Exec { broken.Exec = e; return broken }
		if confirm > 0 {
			o.confirm = confirm
		}
	})
	g.mintToken("full")
	var st map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st = g.do("GET", "/state", nil, nil, nil).json(t)
		if st["safe_mode"] == true {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st["safe_mode"] != true {
		t.Fatalf("the gateway is not in safe mode: %v", st)
	}
	return g, broken, st
}

// A gateway whose revisions cannot be applied runs in safe mode (plan §3.9): GET /state says so and why, what needs a
// gateway that forwards is refused, and a revision that works ends it.
func TestSafeModeIsReportedAndHoldsBackWhatNeedsAForwardingGatewayUntilARevisionWorks(t *testing.T) {
	g, broken, st := inSafeMode(t, 0)
	reason, _ := st["safe_mode_reason"].(string)
	for _, want := range []string{"revision 2 could not be applied", "neither could revision 1"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason lacks %q: %s", want, reason)
		}
	}
	// the revision pointers stay: the failed revision is still the active one in the store
	if st["active_revision"] != float64(2) {
		t.Errorf("active_revision: %v, want 2", st["active_revision"])
	}

	// what needs a gateway that forwards is refused with a 503, the cause is named: every operation the gate lists
	const anID = "00000000-0000-4000-8000-000000000001"
	held := map[string]struct {
		path string
		body any
	}{
		"createOverlay":  {"/overlays", map[string]any{"target": map[string]any{"global": true}, "fault": map[string]any{"loss": "1%"}, "ttl": "1m"}},
		"renewOverlay":   {"/overlays/" + anID + "/renew", map[string]any{"ttl": "1m"}},
		"createRun":      {"/runs", inlineRun("held", iotTarget, step("s", "0s", "remove", "x"))},
		"runScenario":    {"/scenarios/" + anID + "/runs", map[string]any{}},
		"renewRun":       {"/runs/" + anID + "/renew", map[string]any{"ttl": "1m"}},
		"calibrateProbe": {"/probes/" + anID + "/calibrate", map[string]any{"host": "192.0.2.1"}},
		"startCapture":   {"/captures", map[string]any{"scope": map[string]any{"network": "IoT"}}},
		"runDiagnostic":  {"/diagnostics", map[string]any{"type": "tcp", "host": "192.0.2.1", "port": 443}},
	}
	ids := api.SafeModeHolds()
	if len(ids) != len(held) {
		t.Errorf("the gate holds %v, the test checks %d operations", ids, len(held))
	}
	for _, id := range ids {
		c, ok := held[id]
		if !ok {
			t.Errorf("the gate holds %s, which the test does not check", id)
			continue
		}
		r := g.do("POST", c.path, c.body, nil, nil)
		if r.Status != 503 || !strings.Contains(string(r.Body), "safe mode") || !strings.Contains(string(r.Body), `"unavailable"`) {
			t.Errorf("%s (POST %s): %d %s", id, c.path, r.Status, r.Body)
		}
	}
	// reading works, and so do the revisions
	for _, p := range []string{"/overlays", "/system/health", "/revisions", "/devices"} {
		if r := g.do("GET", p, nil, nil, nil); r.Status != 200 {
			t.Errorf("GET %s: %d %s", p, r.Status, r.Body)
		}
	}
	// a revision that works: the kernel is repaired, the admin clones revision 1 and applies it
	broken.fixed.Store(true)
	cl := g.do("POST", "/revisions/1/clone", nil, ifMatch(g.activeID()), nil)
	if cl.Status != 201 {
		t.Fatalf("clone: %d %s", cl.Status, cl.Body)
	}
	id := int64(cl.json(t)["id"].(float64))
	a := g.apply(id)
	if a.Status != 200 {
		t.Fatalf("apply: %d %s", a.Status, a.Body)
	}
	// the revision changes what reaches the control plane, so it waits for the confirmation (plan §2.14): the gateway is in safe
	// mode until the admin has confirmed it, and goes back to it when the window runs out
	if a.json(t)["status"] != "pending_confirm" {
		t.Fatalf("the revision does not wait for the confirmation: %s", a.Body)
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["safe_mode"] != true {
		t.Errorf("safe mode ended before the revision was confirmed: %v", st)
	}
	if cf := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); cf.Status != 200 {
		t.Fatalf("confirm: %d %s", cf.Status, cf.Body)
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["safe_mode"] == true || st["safe_mode_reason"] != nil {
		t.Errorf("safe mode did not end: %v", st)
	}
	if r := g.do("POST", "/overlays", map[string]any{"target": map[string]any{"global": true}, "fault": map[string]any{"loss": "1%"}, "ttl": "1m"}, nil, nil); r.Status != 201 {
		t.Errorf("an overlay after the safe mode: %d %s", r.Status, r.Body)
	}
}

// The way out of safe mode is a confirmed revision. One that is rolled back, by the admin or because its window ran out, leaves
// the gateway where it was: in safe mode, with the safe configuration applied, no bridge and the revision pointers unmoved,
// and a further revision still applies.
func TestARevisionThatIsNotConfirmedInSafeModeLeavesTheGatewayInSafeMode(t *testing.T) {
	g, broken, _ := inSafeMode(t, 3*time.Second)
	broken.fixed.Store(true)
	// pending clones revision 1 and applies it: it waits for the confirmation, in a window of 3 s
	pending := func() int64 {
		t.Helper()
		cl := g.do("POST", "/revisions/1/clone", nil, ifMatch(g.activeID()), nil)
		if cl.Status != 201 {
			t.Fatalf("clone: %d %s", cl.Status, cl.Body)
		}
		id := int64(cl.json(t)["id"].(float64))
		a := g.apply(id)
		if a.Status != 200 || a.json(t)["status"] != "pending_confirm" {
			t.Fatalf("apply: %d %s", a.Status, a.Body)
		}
		return id
	}
	// stillSafe waits for the rollback to apply the safe configuration again and checks that nothing else moved
	stillSafe := func(what string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			snap := g.e.Snapshot()
			if len(snap.Bridges) == 0 && snap.Applied != nil && snap.Applied.Revision == 0 && snap.LastError == "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the safe configuration is not what runs: %d bridges, applied %+v", what, len(snap.Bridges), snap.Applied)
			}
			time.Sleep(20 * time.Millisecond)
		}
		st := g.do("GET", "/state", nil, nil, nil).json(t)
		if st["safe_mode"] != true || st["safe_mode_reason"] == nil || st["active_revision"] != float64(2) || st["pending_confirm"] != nil {
			t.Errorf("%s: %v", what, st)
		}
	}

	// the admin rolls it back
	id := pending()
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["safe_mode"] != true || st["pending_confirm"] == nil {
		t.Fatalf("while the revision waits: %v", st)
	}
	if r := g.do("POST", "/revisions/"+itoa(id)+"/rollback", nil, nil, nil); r.Status != 200 {
		t.Fatalf("rollback: %d %s", r.Status, r.Body)
	}
	stillSafe("after the rollback")

	// the window runs out
	pending()
	deadline := time.Now().Add(20 * time.Second)
	for g.do("GET", "/state", nil, nil, nil).json(t)["pending_confirm"] != nil {
		if time.Now().After(deadline) {
			t.Fatal("the window of the revision never ran out")
		}
		time.Sleep(50 * time.Millisecond)
	}
	stillSafe("after the window ran out")

	// a further revision is cloned from the unmoved pointer and ends the safe mode
	cl := g.do("POST", "/revisions/1/clone", nil, ifMatch(g.activeID()), nil)
	if cl.Status != 201 {
		t.Fatalf("clone: %d %s", cl.Status, cl.Body)
	}
	next := int64(cl.json(t)["id"].(float64))
	if a := g.apply(next); a.Status != 200 {
		t.Fatalf("apply: %d %s", a.Status, a.Body)
	}
	if cf := g.do("POST", "/revisions/"+itoa(next)+"/confirm", nil, nil, nil); cf.Status != 200 {
		t.Fatalf("confirm: %d %s", cf.Status, cf.Body)
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["safe_mode"] == true || st["active_revision"] != float64(next) {
		t.Errorf("safe mode did not end: %v", st)
	}
}

// The events of the recovery reach the stream with their messages: the UI's banner refetches the state on safe_mode, and the
// activity log shows the lines. The stream replays from the start of the boot, because the events came before the client.
func TestTheEventsOfTheRecoveryAreStreamedWithTheirMessages(t *testing.T) {
	g, broken, _ := inSafeMode(t, 0)
	// the restore of the last known good revision is the engine's event at start; this gateway ended in safe mode instead
	g.e.Emit(engine.EventRolledBack, map[string]any{"revision": int64(2), "reason": "boot: RTNETLINK answers: Operation not supported", "restored": int64(1)})
	s := g.openStream(g.e.BootID()+"-0", "")
	if s.status != 200 {
		t.Fatalf("stream: %d", s.status)
	}
	entered, ok := s.until("safe_mode", 5*time.Second)
	if !ok {
		t.Fatal("no safe_mode event")
	}
	data, _ := entered.Data["data"].(map[string]any)
	reason, _ := data["reason"].(string)
	msg, _ := entered.Data["message"].(string)
	if data["active"] != true || !strings.Contains(reason, "revision 2 could not be applied") || msg != "safe mode: "+reason {
		t.Errorf("safe_mode: %+v", entered.Data)
	}
	rolled, ok := s.until("revision_rolled_back", 5*time.Second)
	if !ok {
		t.Fatal("no revision_rolled_back event")
	}
	if want := "revision 2 could not be applied at start: revision 1, the last known good one, is active again"; rolled.Data["message"] != want {
		t.Errorf("revision_rolled_back: %v, want message %q", rolled.Data, want)
	}

	broken.fixed.Store(true)
	cl := g.do("POST", "/revisions/1/clone", nil, ifMatch(g.activeID()), nil)
	if cl.Status != 201 {
		t.Fatalf("clone: %d %s", cl.Status, cl.Body)
	}
	id := int64(cl.json(t)["id"].(float64))
	if a := g.apply(id); a.Status != 200 {
		t.Fatalf("apply: %d %s", a.Status, a.Body)
	}
	if cf := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); cf.Status != 200 {
		t.Fatalf("confirm: %d %s", cf.Status, cf.Body)
	}
	left, ok := s.until("safe_mode", 5*time.Second)
	if !ok {
		t.Fatal("no event says that safe mode ended")
	}
	if d, _ := left.Data["data"].(map[string]any); d["active"] != false || left.Data["message"] != "safe mode ended: a revision was applied" {
		t.Errorf("the end of safe mode: %+v", left.Data)
	}
}

// The stop order of the API process (plan §3.11): the runs end as aborted, every overlay is removed, and the last apply is one
// without overlays. The executor removes everything else when it stops.
func TestTheStopOfTheAPIAbortsTheRunsAndRemovesEveryOverlay(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	g := newGW(t, func(o *options) { o.apiClock = clk })
	g.finishSetup()
	// a run that is waiting for its next step (its fault is an overlay), and the overlay of a token
	r := g.do("POST", "/runs", inlineRun("stopping", iotTarget,
		step("a", "0s", "fault", map[string]any{"loss": "1%"}),
		step("b", "30m", "fault", map[string]any{"loss": "2%"}),
	), nil, nil)
	if r.Status != 201 && r.Status != 202 {
		t.Fatalf("run: %d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "a", "applied")
	g.mustCreateOverlay(map[string]any{"target": map[string]any{"global": true}, "fault": map[string]any{"latency": "40ms"}, "ttl": "1h"})
	if n := len(g.e.Snapshot().Overlays); n != 2 {
		t.Fatalf("%d overlays before the stop", n)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(g.e.Snapshot().Overlays); n != 0 {
		t.Errorf("%d overlays after the stop", n)
	}
	run := g.run(id)
	if run["status"] != "aborted" || run["reason"] != "service stopping" {
		t.Errorf("the run: %v %v", run["status"], run["reason"])
	}
	// the last apply is the one without overlays: nothing of them is in the kernel
	if _, err := g.e.Barrier(ctx); err != nil {
		t.Fatal(err)
	}
	if s := g.e.Snapshot(); s.LastError != "" || len(s.Faults) != 0 {
		t.Errorf("faults in the kernel after the stop: %+v, %q", s.Faults, s.LastError)
	}
	// a second call is harmless
	if err := g.srv.Shutdown(ctx); err != nil {
		t.Errorf("the second Shutdown: %v", err)
	}
}
