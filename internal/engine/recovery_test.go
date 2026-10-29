package engine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// The recovery at start (plan §3.9, §2.14, M27) on the simulated kernel: the last known good revision, safe mode, the
// recompile after an interrupted apply and the degraded network. The real tools and a real restart are the level 1 and 2 tests.

// failBridges makes the kernel refuse to create a bridge: every revision with a test network fails to apply, while the safe
// configuration (no network) applies.
func failBridges(h *harness) {
	h.k.SetFail(func(argv []string, stdin string) *executor.Result {
		if strings.Contains(strings.Join(argv, " ")+stdin, "type bridge") {
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Operation not supported\n"}
		}
		return nil
	})
}

// failGateway makes the kernel refuse the uplink gateway 203.0.113.20, which only the second revision of twoRevisions names.
func failGateway(h *harness) {
	h.k.SetFail(func(argv []string, stdin string) *executor.Result {
		if strings.Contains(strings.Join(argv, " ")+stdin, "203.0.113.20") {
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Network is unreachable\n"}
		}
		return nil
	})
}

// twoRevisions applies two revisions one after the other (the second names the uplink gateway 203.0.113.20) and returns their
// ids. The engine is running afterwards.
func twoRevisions(h *harness) (r1, r2 int64) {
	h.t.Helper()
	h.start()
	r1 = h.mustApply(h.revision(nil)).Revision
	r2 = h.mustApply(h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })).Revision
	return r1, r2
}

// reboot is a restart of the whole gateway: the engine and the store are closed, the kernel is a fresh one (the machine
// restarted), and the store is opened again from its directory. The old engine is gone; start the new one.
func reboot(old *harness) *harness {
	old.t.Helper()
	return rebootWith(old, nil)
}

// rebootWith is reboot with a change of the store's directory while the process is down (a pointer set by hand).
func rebootWith(old *harness, edit func(dir string)) *harness {
	old.t.Helper()
	old.e.Close()
	if err := old.st.Close(); err != nil {
		old.t.Fatal(err)
	}
	if edit != nil {
		edit(old.dir)
	}
	h := newHarness(old.t)
	if err := h.st.Close(); err != nil {
		old.t.Fatal(err)
	}
	st, err := store.Open(old.dir)
	if err != nil {
		old.t.Fatal(err)
	}
	old.t.Cleanup(func() { _ = st.Close() })
	h.st, h.dir = st, old.dir
	return h
}

// restart is a restart of the process on the same machine: the kernel keeps what it holds.
func restart(old *harness) *harness {
	old.t.Helper()
	old.e.Close()
	if err := old.st.Close(); err != nil {
		old.t.Fatal(err)
	}
	st, err := store.Open(old.dir)
	if err != nil {
		old.t.Fatal(err)
	}
	old.t.Cleanup(func() { _ = st.Close() })
	h := *old
	h.st = st
	return &h
}

// retryOnce lets the owner try a failed apply again: the retry waits retryDelay on the engine's clock.
func (h *harness) retryOnce() {
	h.t.Helper()
	h.wait(func() bool { return h.e.Snapshot().LastError != "" }, "the first apply fails")
	h.clk.Advance(11 * time.Second)
}

func TestABrokenRevisionAtStartLeadsToTheLastKnownGoodRevision(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
	failGateway(h)
	if err := h.st.MarkGood(r2); err != nil {
		t.Fatal(err)
	}
	h.start()
	events, stop := h.e.Subscribe()
	defer stop()
	h.retryOnce()
	h.wait(func() bool { s := h.e.Snapshot(); return s.Revision == r1 && s.Applied != nil && s.LastError == "" }, "revision 1 is applied")

	s := h.e.Snapshot()
	if s.SafeMode || s.SafeModeReason != "" {
		t.Fatalf("safe mode: %+v", s)
	}
	if s.Applied.Revision != r1 || !s.Applied.RolledBack {
		t.Errorf("applied: %+v", s.Applied)
	}
	// the store follows: revision 1 is the active and the last known good revision, revision 2 is rolled back
	if h.st.ActiveID() != r1 {
		t.Errorf("active %d, want %d", h.st.ActiveID(), r1)
	}
	if lkg, _, err := h.st.LastKnownGood(); err != nil || lkg.Id != r1 {
		t.Errorf("last known good: %+v %v", lkg, err)
	}
	if rev, _, err := h.st.Get(r2); err != nil || rev.Status != store.StatusRolledBack {
		t.Errorf("revision 2: %+v %v", rev, err)
	}
	h.k.SetFail(nil)
	h.verifyKernel()
	var rolled *engine.Event
	for {
		select {
		case ev := <-events:
			if ev.Type == engine.EventRolledBack {
				ev := ev
				rolled = &ev
			}
			continue
		default:
		}
		break
	}
	if rolled == nil || rolled.Data["revision"] != r2 || rolled.Data["restored"] != r1 || !strings.HasPrefix(rolled.Data["reason"].(string), "boot") {
		t.Fatalf("event: %+v", rolled)
	}
	// the next revision is based on the restored one
	r3 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.30") })
	if a := h.mustApply(r3); a.Status != "active" {
		t.Errorf("%+v", a)
	}
}

func TestATransientFailureAtStartIsTriedAgainBeforeAnythingIsRolledBack(t *testing.T) {
	h := newHarness(t)
	_, r2 := twoRevisions(h)
	h = reboot(h)
	var failures atomic.Int32
	h.k.SetFail(func(argv []string, stdin string) *executor.Result {
		if strings.Contains(strings.Join(argv, " ")+stdin, "203.0.113.20") && failures.Add(1) == 1 {
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Device or resource busy\n"}
		}
		return nil
	})
	h.start()
	h.retryOnce()
	h.wait(func() bool { s := h.e.Snapshot(); return s.Applied != nil && s.LastError == "" }, "revision 2 is applied")
	if s := h.e.Snapshot(); s.Revision != r2 || s.Applied.Revision != r2 || s.SafeMode || s.Applied.RolledBack {
		t.Fatalf("%+v", s)
	}
	if h.st.ActiveID() != r2 {
		t.Errorf("active %d", h.st.ActiveID())
	}
}

func TestWhenTheLastKnownGoodRevisionFailsToo_TheGatewayRunsInSafeMode(t *testing.T) {
	h := newHarness(t)
	_, r2 := twoRevisions(h)
	h = reboot(h)
	failBridges(h)
	h.start()
	events, stop := h.e.Subscribe()
	defer stop()
	h.retryOnce()
	h.wait(func() bool { return h.e.Snapshot().SafeMode }, "safe mode")

	s := h.e.Snapshot()
	for _, want := range []string{"revision 2 could not be applied", "neither could revision 1", "last known good"} {
		if !strings.Contains(s.SafeModeReason, want) {
			t.Errorf("the reason lacks %q: %s", want, s.SafeModeReason)
		}
	}
	h.wait(func() bool { s := h.e.Snapshot(); return s.Applied != nil && s.LastError == "" }, "the safe configuration is applied")
	s = h.e.Snapshot()
	// management access only, no forwarding: no network, no bridge, no route between networks
	if s.Config == nil || s.Config.Networks != nil || s.Config.Devices != nil || len(s.Bridges) != 0 {
		t.Errorf("the safe configuration holds more than the management: %+v", s.Config)
	}
	if s.Config.Management.Interface.Name == nil || *s.Config.Management.Interface.Name != "mgmt0" {
		t.Errorf("management: %+v", s.Config.Management)
	}
	for name, l := range h.exLinksDetail() {
		if l == "bridge" {
			t.Errorf("the bridge %s exists in safe mode", name)
		}
	}
	// the kernel runs the safe target, which verifies
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil || len(st.Nft.Tables()) == 0 {
		t.Fatalf("no table of the gateway in safe mode: %v", err)
	}
	if h.st.ActiveID() != r2 {
		t.Errorf("the store keeps the pointer: %d", h.st.ActiveID())
	}
	var safe *engine.Event
	for {
		select {
		case ev := <-events:
			if ev.Type == engine.EventSafeMode {
				ev := ev
				safe = &ev
			}
			continue
		default:
		}
		break
	}
	if safe == nil || safe.Data["active"] != true || safe.Data["reason"] != s.SafeModeReason {
		t.Errorf("event: %+v", safe)
	}

	// a revision that works ends it: the failure is gone, the admin clones revision 1 and applies it
	h.k.SetFail(nil)
	if _, cfg, err := h.st.Get(1); err != nil {
		t.Fatal(err)
	} else {
		cand, err := h.st.Create(cfg, store.CreateOptions{IfMatch: h.st.ActiveID(), Now: h.clk.Now(), By: model.Actor{Id: "admin", Type: "user"}})
		if err != nil {
			t.Fatal(err)
		}
		if a := h.mustApply(cand.Id); a.Status != "active" {
			t.Fatalf("%+v", a)
		}
	}
	if s := h.e.Snapshot(); s.SafeMode || s.SafeModeReason != "" || len(s.Bridges) == 0 {
		t.Errorf("safe mode did not end: %+v", s)
	}
	end := false
	for !end {
		select {
		case ev := <-events:
			end = ev.Type == engine.EventSafeMode && ev.Data["active"] == false
		default:
			t.Fatal("no event says that safe mode ended")
		}
	}
	h.verifyKernel()
}

func (h *harness) exLinksDetail() map[string]string {
	h.t.Helper()
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]string{}
	for n, l := range st.Links {
		out[n] = l.Kind()
	}
	return out
}

// A revision that applies at start becomes the last known good one (docs/development.md): the pointer, which a crash or an
// older store left on an earlier revision, is moved by the start, not only by the confirmation.
func TestARevisionThatAppliesAtStartBecomesTheLastKnownGoodOne(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = rebootWith(h, func(dir string) {
		p := filepath.Join(dir, "config.json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		old := fmt.Sprintf(`"last_known_good": %d`, r2)
		if !strings.Contains(string(raw), old) {
			t.Fatalf("no last_known_good in the pointer file:\n%s", raw)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(raw), old, fmt.Sprintf(`"last_known_good": %d`, r1), 1)), 0o640); err != nil {
			t.Fatal(err)
		}
	})
	if lkg, _, err := h.st.LastKnownGood(); err != nil || lkg.Id != r1 {
		t.Fatalf("the test did not set the pointer back: %+v %v", lkg, err)
	}
	h.start()
	h.wait(func() bool {
		s := h.e.Snapshot()
		return s.Applied != nil && s.Applied.Revision == r2 && s.LastError == ""
	}, "revision 2 is applied")
	h.wait(func() bool { lkg, _, err := h.st.LastKnownGood(); return err == nil && lkg.Id == r2 }, "revision 2 is the last known good one")
	if h.st.ActiveID() != r2 || h.e.Snapshot().SafeMode {
		t.Errorf("active %d, %+v", h.st.ActiveID(), h.e.Snapshot())
	}
}

// A revision that did not apply at start is not the fallback of a later start, even after a newer revision superseded it with
// its confirmation still set: the way out of safe mode is a new revision, and when that one fails at start the gateway does
// not go back to a revision that is known not to apply.
func TestARevisionThatFailedAtStartIsNotTheFallbackOfALaterStart(t *testing.T) {
	h := newHarness(t)
	twoRevisions(h)
	h = reboot(h)
	failBridges(h)
	h.start()
	h.retryOnce()
	h.wait(func() bool { return h.e.Snapshot().SafeMode }, "safe mode")
	h.wait(func() bool { s := h.e.Snapshot(); return s.Applied != nil && s.LastError == "" }, "the safe configuration is applied")

	// the administrator's way out: revision 1 with another gateway, which works once the kernel does
	h.k.SetFail(nil)
	_, cfg, err := h.st.Get(1)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Uplink.Gateway = ptr("203.0.113.30")
	cand, err := h.st.Create(cfg, store.CreateOptions{IfMatch: h.st.ActiveID(), Now: h.clk.Now(), By: model.Actor{Id: "admin", Type: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	if a := h.mustApply(cand.Id); a.Status != "active" {
		t.Fatalf("%+v", a)
	}
	if h.e.Snapshot().SafeMode {
		t.Fatal("safe mode did not end")
	}

	// the new revision fails at the next start. Revision 2 was confirmed once and is superseded, so without the mark it would
	// be the fallback; it did not apply at the start before, and neither did revision 1
	h = reboot(h)
	h.k.SetFail(func(argv []string, stdin string) *executor.Result {
		if strings.Contains(strings.Join(argv, " ")+stdin, "203.0.113.30") {
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Network is unreachable\n"}
		}
		return nil
	})
	h.start()
	h.retryOnce()
	h.wait(func() bool { return h.e.Snapshot().SafeMode }, "safe mode")
	if r := h.e.Snapshot().SafeModeReason; !strings.Contains(r, "revision 3 could not be applied") || !strings.Contains(r, "no earlier revision that worked") {
		t.Errorf("reason: %s", r)
	}
	if h.st.ActiveID() != cand.Id {
		t.Errorf("active %d", h.st.ActiveID())
	}
}

func TestAFirstRevisionThatFailsAtStartHasNothingToFallBackToAndRunsInSafeMode(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	h = reboot(h)
	failBridges(h)
	h.start()
	h.retryOnce()
	h.wait(func() bool { return h.e.Snapshot().SafeMode }, "safe mode")
	if r := h.e.Snapshot().SafeModeReason; !strings.Contains(r, "revision 1 could not be applied") || !strings.Contains(r, "no earlier revision that worked") {
		t.Errorf("reason: %s", r)
	}
	if h.st.ActiveID() != 1 {
		t.Errorf("active %d", h.st.ActiveID())
	}
}

func TestADamagedActiveRevisionLeadsToTheLastKnownGoodRevisionWithoutApplyingIt(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
	damage(t, h, r2)
	h.start()
	h.wait(func() bool { s := h.e.Snapshot(); return s.Revision == r1 && s.Applied != nil && s.LastError == "" }, "revision 1 is applied")
	if s := h.e.Snapshot(); s.SafeMode || !s.Applied.RolledBack {
		t.Fatalf("%+v", s)
	}
	if h.st.ActiveID() != r1 {
		t.Errorf("active %d", h.st.ActiveID())
	}
	h.verifyKernel()
}

func TestADamagedFirstRevisionRunsInSafeModeAndTouchesNothing(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	h = reboot(h)
	damage(t, h, r1)
	h.start()
	s := h.e.Snapshot()
	if !s.SafeMode || !strings.Contains(s.SafeModeReason, "revision 1 cannot be read") || !strings.Contains(s.SafeModeReason, "no earlier revision") {
		t.Fatalf("%+v", s)
	}
	if s.Config != nil || s.Applied != nil {
		t.Errorf("nothing readable names the management interface: config %v, applied %+v", s.Config, s.Applied)
	}
	if links := h.exLinksDetail(); links["br-iot"] != "" {
		t.Errorf("the kernel was touched: %v", links)
	}
}

// A revision written by a newer version is no damaged file (plan §3.9, downgrade): the start is refused with the version that is
// needed, no older revision is applied in its place and the store's pointers stay where they were.
func TestARevisionOfANewerSchemaRefusesTheStartInsteadOfFallingBack(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
	p := filepath.Join(h.dir, "revisions", fmt.Sprintf("%06d.json", r2))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"schema_version": 1`) {
		t.Fatalf("the revision has no schema version to raise:\n%s", raw)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(raw), `"schema_version": 1`, `"schema_version": 7`, 1)), 0o640); err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: h.clk})
	if err != nil {
		t.Fatal(err)
	}
	err = e.Start(context.Background())
	var newer *store.ErrNewerSchema
	if !errors.As(err, &newer) || newer.Found != 7 {
		e.Close()
		t.Fatalf("Start: %v: want the refusal of a newer schema", err)
	}
	e.Close()
	if h.st.ActiveID() != r2 {
		t.Errorf("active %d, want %d: the pointer moved", h.st.ActiveID(), r2)
	}
	if st, err := os.ReadFile(filepath.Join(h.dir, "revisions", fmt.Sprintf("%06d.status.json", r2))); err != nil || strings.Contains(string(st), "rolled_back") {
		t.Errorf("the revision of the newer version was marked: %v\n%s", err, st)
	}
	if h.st.ActiveID() == r1 {
		t.Error("the older revision became the active one")
	}
	if links := h.exLinksDetail(); links["br-iot"] != "" {
		t.Errorf("the kernel was touched: %v", links)
	}
}

// damage edits the configuration of a revision by hand, which its checksum notices.
func damage(t *testing.T, h *harness, id int64) {
	t.Helper()
	if err := h.st.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(h.dir, "revisions", fmt.Sprintf("%06d.json", id))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(raw), "203.0.113", "203.0.114", 1)), 0o640); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h.st = st
}

func TestAnInterruptedApplyIsRecoveredByRecompilingTheCommittedRevision(t *testing.T) {
	h := newHarness(t)
	twoRevisions(h)
	// a power loss in the middle of an apply: the table is gone, a bridge is gone and a route of the revision before is back
	h.e.Close()
	h.k.RemoveLink("br-iot")
	h.k.RemoveLink("br-lab")
	if _, err := h.ex.Do(context.Background(), &executor.NftApply{Ruleset: []byte(`{"nftables":[{"add":{"table":{"family":"inet","name":"chaosgw"}}},{"delete":{"table":{"family":"inet","name":"chaosgw"}}}]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ex.Do(context.Background(), &executor.Routing{Routes: []executor.Route{{Action: "replace", Family: 4, Table: 100, Dst: "default", Via: "203.0.113.10", Dev: "wan0"}}}); err != nil {
		t.Fatal(err)
	}
	h = restart(h)
	h.start()
	h.wait(func() bool { s := h.e.Snapshot(); return s.Applied != nil && s.LastError == "" }, "the committed revision is applied")
	h.verifyKernel()
	if s := h.e.Snapshot(); s.SafeMode || s.Applied.RolledBack {
		t.Errorf("%+v", s)
	}
	for _, r := range h.mustReadState().Routes {
		if r.Table == "100" && r.Dst == "default" && r.Gateway == "203.0.113.10" {
			t.Errorf("the route of the interrupted apply is still there: %+v", r)
		}
	}
}

func (h *harness) mustReadState() *apply.State {
	h.t.Helper()
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func TestAMissingTestInterfaceAtStartDegradesTheNetworkAndIsNoSafeMode(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	h = reboot(h)
	h.k.RemoveLink("lan1")
	h.start()
	h.wait(func() bool { s := h.e.Snapshot(); return s.Applied != nil && s.LastError == "" }, "the revision is applied")
	s := h.e.Snapshot()
	if s.SafeMode || s.Applied.RolledBack || len(s.Problems) != 1 || s.Problems[0].Network != "Lab" {
		t.Fatalf("%+v", s)
	}
	if h.st.ActiveID() != 1 {
		t.Errorf("active %d", h.st.ActiveID())
	}
}

func TestUnpluggingATestInterfaceDegradesItsNetworkWithoutSafeMode(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	events, stop := h.e.Subscribe()
	defer stop()
	h.k.RemoveLink("lan1")
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	s := h.e.Snapshot()
	if s.SafeMode || len(s.Problems) != 1 || s.Problems[0].Code != "port_missing" || s.Problems[0].Network != "Lab" {
		t.Fatalf("%+v", s)
	}
	if !has(drain(events), engine.EventNetworkDegraded) {
		t.Error("no network_degraded event")
	}
	// the other network keeps working: its bridge is still there
	if h.exLinksDetail()["br-iot"] != "bridge" {
		t.Error("the network that kept its port lost its bridge")
	}
	// plugged in again, the network is applied again
	h.k.AddLink("lan1", "02:00:00:00:01:01", "veth", true)
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	if s := h.e.Snapshot(); len(s.Problems) != 0 || s.SafeMode {
		t.Errorf("%+v", s)
	}
}

func drain(ch <-chan engine.Event) []string {
	var out []string
	for {
		select {
		case ev := <-ch:
			out = append(out, ev.Type)
		default:
			return out
		}
	}
}

// ownerPanicClock panics in the state owner, in startApply, once armed: the clock is read there for the start of an apply.
type ownerPanicClock struct {
	*clock.Fake
	armed *atomic.Bool
}

func (c ownerPanicClock) Now() time.Time {
	if c.armed.Load() {
		pcs := make([]uintptr, 16)
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
		for {
			f, more := frames.Next()
			if strings.HasSuffix(f.Function, "(*owner).startApply") {
				panic("kaboom in the state owner")
			}
			if !more {
				break
			}
		}
	}
	return c.Fake.Now()
}

// A panic in the state owner ends the engine (plan §3.11): its callers get an error instead of waiting, Done tells the process
// to end, and the restarted process recompiles the committed revision, not the one that was being applied.
func TestAPanicInTheStateOwnerEndsTheEngineAndTheNextStartRecompilesTheCommittedRevision(t *testing.T) {
	h := newHarness(t)
	r1 := func() int64 { h.start(); return h.mustApply(h.revision(nil)).Revision }()
	h.e.Close()

	armed := &atomic.Bool{}
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: ownerPanicClock{h.clk, armed}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.e = e
	t.Cleanup(e.Close)
	h.wait(func() bool { s := e.Snapshot(); return s.Applied != nil && s.LastError == "" }, "the first apply")
	armed.Store(true)
	r2 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	if _, err := h.apply(r2); err == nil || !errors.Is(err, engine.ErrClosed) {
		t.Fatalf("an apply while the state owner panics: %v", err)
	}
	select {
	case <-e.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done is not closed: the process cannot tell that the engine is gone")
	}
	var panicked bool
	for _, hl := range e.Health() {
		panicked = panicked || hl.State == "panicked"
	}
	if !panicked {
		t.Errorf("health %+v", e.Health())
	}

	// the process restarts: the same store and the same kernel, a new engine
	h = restart(h)
	h.start()
	h.wait(func() bool { s := h.e.Snapshot(); return s.Applied != nil && s.LastError == "" }, "the committed revision is applied")
	if s := h.e.Snapshot(); s.Revision != r1 || s.Applied.Revision != r1 || s.SafeMode {
		t.Fatalf("%+v", s)
	}
	if h.st.ActiveID() != r1 {
		t.Errorf("active %d", h.st.ActiveID())
	}
	h.verifyKernel()
	// the revision that was being applied is a candidate still: nothing of it was committed
	if rev, _, err := h.st.Get(r2); err != nil || rev.Status != store.StatusCandidate {
		t.Errorf("revision %d: %+v %v", r2, rev, err)
	}
}

// The last known good revision is applied at start, so it needs the WireGuard keys it names: the prune of the secrets after
// the commit of a later revision keeps them although that revision does not use them.
func TestTheLastKnownGoodRevisionKeepsItsWireGuardKeysWhenALaterRevisionDropsTheNetwork(t *testing.T) {
	h, sec := newWGHarness(t)
	r1 := h.mustApply(h.revision(nil)).Revision
	// dropping the networks waits for confirmation; the confirmed revision is the active one and the secrets are pruned
	r2 := h.mustApply(h.revision(func(c *model.Configuration) {
		c.Networks, c.AccessMatrix = nil, nil
		c.Uplink.Gateway = ptr("203.0.113.20")
	})).Revision
	if err := h.e.Confirm(context.Background(), r2, model.Actor{Id: "admin", Type: "user"}); err != nil {
		t.Fatal(err)
	}
	if ids, err := sec.IDs(); err != nil || !slices.Contains(ids, wgHub) {
		t.Fatalf("the key of the network of revision %d was pruned with revision %d: %v %v", r1, r2, ids, err)
	}

	// the machine restarts (nothing of the gateway's is left in the kernel) and revision 2 cannot be applied
	h.e.Close()
	if _, err := apply.Teardown(context.Background(), apply.Local{E: h.ex}, "", ""); err != nil {
		t.Fatal(err)
	}
	h = restart(h)
	failGateway(h)
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: h.clk, Secrets: sec})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e = e
	h.retryOnce()
	h.wait(func() bool {
		s := h.e.Snapshot()
		return s.SafeMode || (s.Revision == r1 && s.Applied != nil && s.LastError == "")
	}, "revision 1 is applied or safe mode begins")
	s := h.e.Snapshot()
	if s.SafeMode {
		t.Fatalf("safe mode although revision %d worked: %s", r1, s.SafeModeReason)
	}
	if !s.Applied.RolledBack || len(s.WireGuardInterfaces) != 3 {
		t.Errorf("%+v", s)
	}
	if h.st.ActiveID() != r1 {
		t.Errorf("active %d, want %d", h.st.ActiveID(), r1)
	}
}

// A restart of the process alone leaves the previous revision's objects in the kernel; safe mode without a readable
// configuration must not leave them forwarding.
func TestSafeModeWithoutAReadableRevisionRemovesWhatTheKernelStillHolds(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	if h.exLinksDetail()["br-iot"] != "bridge" || len(h.mustReadState().Nft.Tables()) == 0 {
		t.Fatal("the revision is not in the kernel")
	}
	h = restart(h)
	damage(t, h, r1)
	h.start()
	if s := h.e.Snapshot(); !s.SafeMode || s.Config != nil {
		t.Fatalf("%+v", s)
	}
	if links := h.exLinksDetail(); links["br-iot"] != "" || links["br-lab"] != "" {
		t.Errorf("the bridges of the revision before still forward in safe mode: %v", links)
	}
	if st := h.mustReadState(); len(st.Nft.Tables()) != 0 {
		t.Errorf("the table of the gateway is still there: %v", st.Nft.Tables())
	}
}
