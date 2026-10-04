package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type harness struct {
	t    *testing.T
	k    *kernelsim.Kernel
	ex   *executor.Executor
	st   *store.Store
	clk  *clock.Fake
	e    *engine.Engine
	base *model.Configuration
	dir  string
}

func ptr[T any](v T) *T { return &v }

func newHarness(t *testing.T) *harness {
	t.Helper()
	k := kernelsim.New()
	k.AddLink("lan0", "02:00:00:00:00:01", "veth", true)
	k.AddLink("lan1", "02:00:00:00:01:01", "veth", true)
	k.AddLink("wan0", "02:00:00:00:02:01", "veth", true)
	k.SetAddr("wan0", "203.0.113.1/24")
	k.AddLink("mgmt0", "02:00:00:00:03:01", "veth", true)
	k.SetAddr("mgmt0", "192.168.56.1/24")
	k.SetMainDefault("192.168.56.254", "mgmt0")
	k.AddDockerChain()
	ex, err := executor.New(k)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	raw, err := os.ReadFile("../compiler/testdata/gateway.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, k: k, ex: ex, st: st, clk: clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)), base: cfg, dir: dir}
	return h
}

func (h *harness) start() {
	h.t.Helper()
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: h.clk})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(e.Close)
	h.e = e
}

func (h *harness) clone() *model.Configuration {
	b, err := json.Marshal(h.base)
	if err != nil {
		h.t.Fatal(err)
	}
	var c model.Configuration
	if err := json.Unmarshal(b, &c); err != nil {
		h.t.Fatal(err)
	}
	return &c
}

// revision stores a candidate based on the active revision.
func (h *harness) revision(mod func(*model.Configuration)) int64 {
	h.t.Helper()
	cfg := h.clone()
	if mod != nil {
		mod(cfg)
	}
	r, err := h.st.Create(cfg, store.CreateOptions{IfMatch: h.st.ActiveID(), Now: h.clk.Now(), By: model.Actor{Id: "admin", Type: "user"}})
	if err != nil {
		h.t.Fatalf("create: %v", err)
	}
	return r.Id
}

func (h *harness) barrier() *engine.Snapshot {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := h.e.Barrier(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func (h *harness) apply(rev int64, opts ...engine.ApplyOptions) (engine.Applied, error) {
	h.t.Helper()
	var o engine.ApplyOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return h.e.Apply(ctx, rev, o)
}

func (h *harness) mustApply(rev int64, opts ...engine.ApplyOptions) engine.Applied {
	h.t.Helper()
	a, err := h.apply(rev, opts...)
	if err != nil {
		h.t.Fatalf("apply %d: %v", rev, err)
	}
	return a
}

// verifyKernel checks that the kernel matches the compile of the snapshot's active revision.
func (h *harness) verifyKernel() {
	h.t.Helper()
	s := h.e.Snapshot()
	ctx := context.Background()
	st, err := apply.ReadState(ctx, apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	tg := compiler.Compile(compiler.Input{Config: s.Config, Host: st.Host(), Generation: compiler.Generation{Revision: s.Applied.Revision, Seq: s.Applied.Generation}})
	st, err = apply.ReadState(ctx, apply.Local{E: h.ex}, "", apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
	if err != nil {
		h.t.Fatal(err)
	}
	if mm := apply.Verify(tg, st); len(mm) != 0 {
		h.t.Fatalf("the kernel does not match revision %d: %v", s.Revision, mm)
	}
}

func events(ch <-chan engine.Event) []string {
	var out []string
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev.Type)
		default:
			return out
		}
	}
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestTheFirstRevisionIsAppliedVerifiedAndCommitted(t *testing.T) {
	h := newHarness(t)
	h.start()
	ch, cancel := h.e.Subscribe()
	defer cancel()
	if s := h.e.Snapshot(); s.Revision != 0 || s.Applied != nil {
		t.Fatalf("an engine without a revision applies nothing: %+v", s)
	}
	rev := h.revision(nil)
	a := h.mustApply(rev)
	if a.Status != "active" || a.Revision != rev || a.Generation == 0 {
		t.Fatalf("%+v", a)
	}
	s := h.e.Snapshot()
	if s.Revision != rev || s.Applied == nil || s.Applied.Revision != rev || s.Applied.Generation != a.Generation || s.Pending != nil || s.LastError != "" {
		t.Fatalf("%+v", s)
	}
	if h.st.ActiveID() != rev {
		t.Errorf("store active %d", h.st.ActiveID())
	}
	h.verifyKernel()
	if ev := events(ch); !has(ev, engine.EventApplied) {
		t.Errorf("events %v", ev)
	}
}

func TestASecondRevisionSupersedesTheFirst(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	r2 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	a := h.mustApply(r2)
	if a.Status != "active" {
		t.Fatalf("%+v", a)
	}
	rev, _, _ := h.st.Get(r1)
	if rev.Status != "superseded" || h.st.ActiveID() != r2 {
		t.Errorf("r1 is %s, active %d", rev.Status, h.st.ActiveID())
	}
	h.verifyKernel()
	// a new default route in table 100
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	var def string
	for _, r := range st.Routes {
		if r.Table == "100" && r.Dst == "default" {
			def = r.Gateway
		}
	}
	if def != "203.0.113.20" {
		t.Errorf("the default route of table 100 goes via %q: the new gateway was not applied", def)
	}
}

func TestAFailedApplyRestoresThePreviousRevision(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	ch, cancel := h.e.Subscribe()
	defer cancel()
	// the second revision adds a network; the nftables transaction fails once it contains its bridge
	r2 := h.revision(func(c *model.Configuration) {
		n := (*c.Networks)["1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"]
		lan, _ := n.AsLanNetwork()
		lan.Name = "Extra"
		_ = n.FromLanNetwork(lan)
		(*c.Networks)["1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"] = n
	})
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(stdin, "br-extra") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	_, err := h.apply(r2)
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) || !af.Restored || af.Revision != r2 || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("got %v", err)
	}
	// the previous revision is active, the failed one is still a candidate, the kernel is restored
	if h.st.ActiveID() != r1 {
		t.Errorf("active %d", h.st.ActiveID())
	}
	if rev, _, _ := h.st.Get(r2); rev.Status != "candidate" {
		t.Errorf("r2 is %s", rev.Status)
	}
	s := h.barrier()
	if s.Revision != r1 || s.LastError != "" || s.Applied.Revision != r1 {
		t.Errorf("snapshot %+v applied %+v", s, s.Applied)
	}
	if _, exists := h.exLinks()["br-extra"]; exists {
		t.Error("the bridge of the failed revision is still there")
	}
	h.k.Fail = nil
	h.verifyKernel()
	ev := events(ch)
	if !has(ev, engine.EventApplyFailed) || !has(ev, engine.EventApplied) {
		t.Errorf("events %v", ev)
	}
}

func (h *harness) exLinks() map[string]bool {
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]bool{}
	for n := range st.Links {
		out[n] = true
	}
	return out
}

func lockoutChange(c *model.Configuration) { c.Management.UiPort = ptr(8443) }

func TestALockoutRelevantChangeNeedsConfirmationAndIsRolledBackAfterTheTimeout(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	ch, cancel := h.e.Subscribe()
	defer cancel()

	r2 := h.revision(lockoutChange)
	a := h.mustApply(r2, engine.ApplyOptions{ConfirmTimeout: 30 * time.Second})
	if a.Status != "pending_confirm" || a.ConfirmDeadline.Sub(h.clk.Now()) != 30*time.Second {
		t.Fatalf("%+v", a)
	}
	// the kernel runs the new revision, the store still names the old one active
	if !strings.Contains(h.nftText(), "22,8443") && !strings.Contains(h.nftText(), "8443") {
		t.Errorf("the new UI port is not in the kernel:\n%s", h.nftText())
	}
	if h.st.ActiveID() != r1 {
		t.Errorf("active %d", h.st.ActiveID())
	}
	if p, ok := h.st.PendingConfirm(); !ok || p.Revision != r2 {
		t.Errorf("pending %+v %v", p, ok)
	}
	if s := h.e.Snapshot(); s.Pending == nil || s.Pending.Revision != r2 || s.Pending.Previous != r1 {
		t.Errorf("snapshot %+v", s.Pending)
	}
	// a second apply during the window is refused
	r3 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	_ = r3
	// (the candidate cannot even be created on top of the pending one: the base is the active r1)

	// nobody confirms: after the window the previous revision is back
	h.clk.Advance(29 * time.Second)
	if h.e.Snapshot().Pending == nil {
		t.Fatal("rolled back too early")
	}
	h.clk.Advance(2 * time.Second)
	s := h.barrier()
	for i := 0; i < 100 && s.Pending != nil; i++ {
		time.Sleep(10 * time.Millisecond)
		s = h.barrier()
	}
	if s.Pending != nil || s.Revision != r1 {
		t.Fatalf("after the timeout: %+v", s)
	}
	// M5-22: the restore itself is marked as a rollback, not an ordinary apply
	if s.Applied == nil || !s.Applied.RolledBack || s.Applied.Revision != r1 {
		t.Errorf("Applied %+v", s.Applied)
	}
	if rev, _, _ := h.st.Get(r2); rev.Status != "rolled_back" || h.st.ActiveID() != r1 {
		t.Errorf("r2 is %s, active %d", rev.Status, h.st.ActiveID())
	}
	if strings.Contains(h.nftText(), "8443") {
		t.Errorf("the kernel still has the rolled back change:\n%s", h.nftText())
	}
	h.verifyKernel()
	ev := events(ch)
	if !has(ev, engine.EventConfirmPending) || !has(ev, engine.EventRolledBack) {
		t.Errorf("events %v", ev)
	}
	// a revision that was never confirmed never became the last known good one
	if lkg, _, err := h.st.LastKnownGood(); err != nil || lkg.Id != r1 {
		t.Errorf("last known good %+v %v", lkg, err)
	}
}

func (h *harness) nftText() string {
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	b, _ := json.Marshal(st.Nft)
	return string(b)
}

func TestConfirmingInTimeMakesTheRevisionActive(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	r2 := h.revision(lockoutChange)
	h.mustApply(r2, engine.ApplyOptions{ConfirmTimeout: 30 * time.Second})
	h.clk.Advance(10 * time.Second)
	ctx := context.Background()
	if err := h.e.Confirm(ctx, r2, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}
	s := h.e.Snapshot()
	if s.Pending != nil || s.Revision != r2 || h.st.ActiveID() != r2 {
		t.Fatalf("%+v active %d", s, h.st.ActiveID())
	}
	if lkg, _, _ := h.st.LastKnownGood(); lkg.Id != r2 {
		t.Errorf("last known good %d", lkg.Id)
	}
	// the timer is gone: nothing rolls back later
	h.clk.Advance(time.Minute)
	s = h.barrier()
	if s.Revision != r2 || !strings.Contains(h.nftText(), "8443") {
		t.Errorf("a confirmed revision was rolled back: %+v", s)
	}
	// confirming something that is not pending fails
	if err := h.e.Confirm(ctx, r2, model.Actor{Type: "user", Id: "admin"}); err == nil {
		t.Error("a second confirm must fail")
	}
}

func TestRollingBackNowAndTheDefaultWindow(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	r2 := h.revision(lockoutChange)
	a := h.mustApply(r2)
	if a.ConfirmDeadline.Sub(h.clk.Now()) != engine.DefaultConfirmTimeout {
		t.Errorf("default window %v", a.ConfirmDeadline.Sub(h.clk.Now()))
	}
	if err := h.e.Rollback(context.Background(), r2); err != nil {
		t.Fatal(err)
	}
	s := h.barrier()
	if s.Revision != r1 || s.Pending != nil || strings.Contains(h.nftText(), "8443") {
		t.Errorf("%+v", s)
	}
	// the window of the configuration's own setting
	r3 := h.revision(func(c *model.Configuration) {
		lockoutChange(c)
		c.Settings = &model.Settings{CommitConfirmTimeout: ptr("5s")}
	})
	a = h.mustApply(r3)
	if a.ConfirmDeadline.Sub(h.clk.Now()) != 5*time.Second {
		t.Errorf("settings window %v", a.ConfirmDeadline.Sub(h.clk.Now()))
	}
}

func TestASecondApplyDuringTheConfirmationWindowIsRefused(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	// two candidates on the same base
	r2 := h.revision(lockoutChange)
	r3 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	h.mustApply(r2)
	_, err := h.apply(r3)
	var cp *store.ErrConfirmPending
	if !errors.As(err, &cp) || cp.Pending != r2 {
		t.Fatalf("got %v", err)
	}
	_ = r1
}

func TestACandidateOnAnOutdatedBaseGetsARevisionConflict(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	a := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	b := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.30") })
	h.mustApply(a)
	_, err := h.apply(b)
	var rc *store.ErrRevisionConflict
	if !errors.As(err, &rc) {
		t.Fatalf("got %v", err)
	}
	// and a revision that is active already is not a candidate
	_, err = h.apply(a)
	var nc *store.ErrNotACandidate
	if !errors.As(err, &nc) {
		t.Fatalf("got %v", err)
	}
	if _, err := h.apply(999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentAppliesOfCandidatesOnTheSameBaseExactlyOneSucceeds(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	var revs []int64
	for _, gw := range []string{"203.0.113.20", "203.0.113.30", "203.0.113.40", "203.0.113.50"} {
		revs = append(revs, h.revision(func(c *model.Configuration) { c.Uplink.Gateway = &gw }))
	}
	var wg sync.WaitGroup
	results := make([]error, len(revs))
	for i, r := range revs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = h.apply(r)
		}()
	}
	wg.Wait()
	var ok, conflict int
	for _, err := range results {
		var rc *store.ErrRevisionConflict
		switch {
		case err == nil:
			ok++
		case errors.As(err, &rc):
			conflict++
		default:
			t.Errorf("unexpected error %v", err)
		}
	}
	if ok != 1 || conflict != len(revs)-1 {
		t.Fatalf("%d succeeded, %d conflicts", ok, conflict)
	}
	h.verifyKernel()
}

func TestAnObserverEventDuringAnApplyIsContainedInTheNextApply(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	base := h.e.Snapshot()

	block := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			once.Do(func() { close(entered); <-block })
		}
		return nil
	}
	r2 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	done := make(chan error, 1)
	go func() { _, err := h.apply(r2); done <- err }()
	<-entered

	// the uplink gets another address while the apply is in flight
	host := base.Host
	for i, l := range host.Links {
		if l.Name == "wan0" {
			links := append([]compiler.HostLink(nil), host.Links...)
			links[i].Addrs = []netipPrefix{mustPrefix("198.51.100.5/24")}
			host.Links = links
		}
	}
	if err := h.e.Observe(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s := h.barrier()
	// the observed state is in the applied target
	if s.Applied.Uplink.Addr.String() != "198.51.100.5/24" {
		t.Errorf("applied uplink %+v: the observation was not contained in the next apply", s.Applied.Uplink)
	}
	if s.Applied.Generation <= base.Generation+1 {
		t.Errorf("generation %d", s.Applied.Generation)
	}
}

func TestABurstOfObservationsIsCoalesced(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	base := h.e.Snapshot()
	var nfts int
	var mu sync.Mutex
	block := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			mu.Lock()
			nfts++
			mu.Unlock()
			once.Do(func() { close(entered); <-block })
		}
		return nil
	}
	// the first observation starts an apply that blocks; the others arrive meanwhile
	obs := func(addr string) {
		host := base.Host
		links := append([]compiler.HostLink(nil), host.Links...)
		for i, l := range links {
			if l.Name == "wan0" {
				links[i].Addrs = []netipPrefix{mustPrefix(addr)}
			}
		}
		host.Links = links
		if err := h.e.Observe(context.Background(), host); err != nil {
			t.Error(err)
		}
	}
	obs("198.51.100.1/24")
	<-entered
	for i := 2; i <= 40; i++ {
		obs("198.51.100." + itoa(i) + "/24")
	}
	close(block)
	s := h.barrier()
	mu.Lock()
	n := nfts
	mu.Unlock()
	if n > 3 {
		t.Errorf("%d applies for 40 observations: they must be coalesced", n)
	}
	if s.Applied.Uplink.Addr.String() != "198.51.100.40/24" {
		t.Errorf("the last observation is not applied: %v", s.Applied.Uplink.Addr)
	}
}

func TestAnUnchangedHostIsNoNewGeneration(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	s := h.e.Snapshot()
	if err := h.e.Observe(context.Background(), s.Host); err != nil {
		t.Fatal(err)
	}
	if h.e.Snapshot().Generation != s.Generation {
		t.Error("an unchanged host bumped the generation")
	}
}

func TestSnapshotsAreImmutableAndReadableWithoutLocks(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	old := h.e.Snapshot()
	before, _ := json.Marshal(old)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := h.e.Snapshot()
					_ = s.Generation
					_ = s.Host.Links
					if s.Config != nil {
						_ = s.Config.Uplink
					}
				}
			}
		}()
	}
	for i := 0; i < 3; i++ {
		gw := "203.0.113." + itoa(20+i)
		h.mustApply(h.revision(func(c *model.Configuration) { c.Uplink.Gateway = &gw }))
	}
	close(stop)
	wg.Wait()
	after, _ := json.Marshal(old)
	if string(before) != string(after) {
		t.Fatal("a published snapshot was modified")
	}
	if old.Revision != r1 || h.e.Snapshot().Revision == r1 {
		t.Errorf("revisions: old %d, now %d", old.Revision, h.e.Snapshot().Revision)
	}
}

func TestDegradedNetworksAndTheUplinkChangeEmitEvents(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.k.RemoveLink("lan1")
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.mustApply(h.revision(nil))
	ch, cancel := h.e.Subscribe()
	defer cancel()
	// the adapter comes back
	h.k.AddLink("lan1", "02:00:00:00:01:01", "veth", true)
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	ev := events(ch)
	if !has(ev, engine.EventNetworkRecovered) || !has(ev, engine.EventObservedChanged) {
		t.Errorf("events %v", ev)
	}
	// the uplink address changes
	h.k.DelAddr("wan0", "203.0.113.1/24")
	h.k.SetAddr("wan0", "203.0.113.77/24")
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	if !has(events(ch), engine.EventUplinkChanged) {
		t.Error("no uplink_changed event")
	}
	h.verifyKernel()
}

func TestADegradedNetworkIsReportedAtTheApply(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.k.RemoveLink("lan1")
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()
	h.mustApply(h.revision(nil))
	s := h.e.Snapshot()
	if len(s.Problems) != 1 || s.Problems[0].Code != compiler.CodePortMissing || s.Problems[0].Network != "Lab" {
		t.Errorf("problems %+v", s.Problems)
	}
	if !has(events(ch), engine.EventNetworkDegraded) {
		t.Error("no network_degraded event")
	}
}

func TestPreviewShowsTheChangeAndChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	r2 := h.revision(func(c *model.Configuration) {
		c.Uplink.Gateway = ptr("203.0.113.20")
		c.Management.UiPort = ptr(8443)
	})
	h.k.ClearLog()
	p, err := h.e.Preview(context.Background(), r2)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range h.k.Commands() {
		if strings.HasPrefix(c, "nft -j -f") || strings.Contains(c, "sysctl -w") || strings.Contains(c, "ip -4") {
			t.Errorf("preview ran %q", c)
		}
	}
	if !p.NeedsConfirmation {
		t.Error("a UI port change needs confirmation")
	}
	var fields []string
	for _, ch := range p.Domain {
		if ch.Fields != nil {
			for _, f := range *ch.Fields {
				fields = append(fields, f.Path)
			}
		}
	}
	if len(p.Domain) == 0 {
		t.Fatal("no domain change")
	}
	if !has(fields, "/gateway") || !has(fields, "/ui_port") {
		t.Errorf("the domain change does not name what changed: %v", fields)
	}
	if !strings.Contains(p.Linux.Routes, "+route table 100 default via 203.0.113.20 dev wan0") || !strings.Contains(p.Linux.Nftables, "8443") {
		t.Errorf("linux diff:\n%s\n%s", p.Linux.Routes, p.Linux.Nftables)
	}
	if len(p.Plan) == 0 || p.Target == nil || p.Target.HasErrors() {
		t.Errorf("plan %v", p.Plan)
	}
	// the preview matches what the apply then does
	a := h.mustApply(r2, engine.ApplyOptions{SkipConfirm: true})
	if a.Status != "active" {
		t.Fatal(a)
	}
	s := h.e.Snapshot()
	if s.Applied.Hash != p.Target.Hash {
		t.Errorf("preview target %s, applied %s", p.Target.Hash, s.Applied.Hash)
	}
	// nothing left to do afterwards
	p2, _ := h.e.Preview(context.Background(), r2)
	if p2 != nil && (p2.Linux.Routes != "" || p2.Linux.Nftables != "") {
		t.Errorf("preview after the apply:\n%s\n%s", p2.Linux.Routes, p2.Linux.Nftables)
	}
}

func TestSkipConfirmCommitsALockoutRelevantRevisionAtOnce(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	r2 := h.revision(lockoutChange)
	a := h.mustApply(r2, engine.ApplyOptions{SkipConfirm: true})
	if a.Status != "active" || h.st.ActiveID() != r2 {
		t.Fatalf("%+v", a)
	}
}

func TestTheFirstRevisionNeverNeedsConfirmation(t *testing.T) {
	h := newHarness(t)
	h.start()
	a := h.mustApply(h.revision(func(c *model.Configuration) { lockoutChange(c) }))
	if a.Status != "active" {
		t.Fatalf("%+v: there is nothing to roll back to", a)
	}
}

func TestStartAppliesTheActiveRevisionAndRollsBackAnUnconfirmedOne(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	r2 := h.revision(lockoutChange)
	h.mustApply(r2) // pending, not confirmed
	h.e.Close()

	// the machine restarts: the kernel is gone, the store remembers
	h.k.Reset()
	h.start()
	s := h.barrier()
	if s.Revision != r1 || s.Pending != nil {
		t.Fatalf("after the restart: %+v", s)
	}
	if rev, _, _ := h.st.Get(r2); rev.Status != "rolled_back" {
		t.Errorf("r2 is %s: a restart inside the confirmation window boots the previous revision", rev.Status)
	}
	h.verifyKernel()
	if s.Applied == nil || s.Applied.Revision != r1 {
		t.Errorf("applied %+v", s.Applied)
	}
}

func TestFirstApplyFailureWithoutAPreviousRevisionIsReported(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	_, err := h.apply(h.revision(nil))
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) || af.Restored {
		t.Fatalf("got %v", err)
	}
	if h.st.ActiveID() != 0 {
		t.Error("a revision that failed to apply became active")
	}
}

func TestATargetWithErrorsFailsTheApplyAndKeepsTheOldRevision(t *testing.T) {
	h := newHarness(t)
	h.start()
	r1 := h.mustApply(h.revision(nil)).Revision
	r2 := h.revision(func(c *model.Configuration) { c.Uplink.Interface = model.InterfaceRef{Name: ptr("nope0")} })
	_, err := h.apply(r2)
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) || !strings.Contains(err.Error(), "uplink interface nope0 is not present") {
		t.Fatalf("got %v", err)
	}
	if h.st.ActiveID() != r1 {
		t.Error("the active revision changed")
	}
	h.barrier()
	h.verifyKernel()
}

func TestCloseWakesWaiters(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	block := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			once.Do(func() { close(entered); <-block })
		}
		return nil
	}
	r2 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	res := make(chan error, 1)
	go func() { _, err := h.e.Apply(context.Background(), r2, engine.ApplyOptions{}); res <- err }()
	<-entered
	go func() { time.Sleep(50 * time.Millisecond); close(block) }()
	h.e.Close()
	if err := <-res; err == nil || !errors.Is(err, engine.ErrClosed) && !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := h.e.Barrier(context.Background()); !errors.Is(err, engine.ErrClosed) {
		t.Errorf("a closed engine: %v", err)
	}
}

func TestSubscribersThatDoNotReadAreDroppedWithoutDelayingOthers(t *testing.T) {
	h := newHarness(t)
	h.start()
	slow, cancelSlow := h.e.Subscribe() // never read
	defer cancelSlow()
	fast, cancelFast := h.e.Subscribe()
	defer cancelFast()
	var got int
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range fast {
			mu.Lock()
			got++
			mu.Unlock()
		}
	}()
	h.mustApply(h.revision(nil))
	base := h.e.Snapshot()
	for i := 1; i <= 300; i++ { // more events than the buffer holds
		host := base.Host
		links := append([]compiler.HostLink(nil), host.Links...)
		for j, l := range links {
			if l.Name == "wan0" {
				links[j].Addrs = []netipPrefix{mustPrefix("198.51." + itoa(i%250) + ".5/24")}
			}
		}
		host.Links = links
		if err := h.e.Observe(context.Background(), host); err != nil {
			t.Fatal(err)
		}
		if i%10 == 0 {
			time.Sleep(2 * time.Millisecond) // the fast reader gets to run; the slow one never does
		}
	}
	h.barrier()
	cancelFast()
	<-done
	// the slow subscriber's channel was closed by the bus when it fell behind
	drained := 0
	for range slow {
		drained++
	}
	if drained == 0 || drained > 256 {
		t.Errorf("slow subscriber drained %d events", drained)
	}
	mu.Lock()
	defer mu.Unlock()
	if got < 300 {
		t.Errorf("the fast subscriber got %d events", got)
	}
}

type panicExec struct {
	inner apply.Exec
	armed *bool
}

func (p panicExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if *p.armed {
		for _, op := range ops {
			if _, ok := op.(*executor.NftApply); ok {
				panic("kaboom")
			}
		}
	}
	return p.inner.Do(ctx, ops...)
}

func TestAPanicInTheApplyLoopStopsTheEngineInsteadOfHangingItsCallers(t *testing.T) {
	h := newHarness(t)
	armed := false
	e, err := engine.New(engine.Config{Store: h.st, Exec: panicExec{inner: apply.Local{E: h.ex}, armed: &armed}, Clock: h.clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.e = e
	t.Cleanup(e.Close)
	armed = true
	_, err = h.apply(h.revision(nil))
	if err == nil || !errors.Is(err, engine.ErrClosed) {
		t.Fatalf("got %v", err)
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
}

func TestCloseWithoutStartAndTwiceIsFine(t *testing.T) {
	h := newHarness(t)
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: h.clk})
	if err != nil {
		t.Fatal(err)
	}
	e.Close() // never started
	if err := e.FollowHost(context.Background(), time.Second); err == nil {
		t.Error("FollowHost before Start must fail")
	}
	h.start()
	h.e.Close()
	h.e.Close()
	if err := h.e.Start(context.Background()); err == nil {
		t.Error("a second Start must fail")
	}
}

// An observation that arrives while a revision is being applied, and an apply that then fails: the
// caller is told about the restore, not about the generation the observation created.
func TestAFailedApplyWithAnObservationInBetweenIsRestoredAndReportedRight(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	base := h.e.Snapshot()
	block := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") && strings.Contains(stdin, "203.0.113.20") == false {
			// the first transaction after the revision starts blocks, then fails
			var first bool
			once.Do(func() { first = true; close(entered); <-block })
			if first {
				return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
			}
		}
		return nil
	}
	r2 := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	done := make(chan error, 1)
	go func() { _, err := h.apply(r2); done <- err }()
	<-entered
	host := base.Host
	links := append([]compiler.HostLink(nil), host.Links...)
	for i, l := range links {
		if l.Name == "wan0" {
			links[i].Addrs = []netipPrefix{mustPrefix("198.51.100.5/24")}
		}
	}
	host.Links = links
	if err := h.e.Observe(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	close(block)
	err := <-done
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) || !af.Restored {
		t.Fatalf("got %v", err)
	}
	s := h.barrier()
	if s.Revision == r2 || h.st.ActiveID() == r2 {
		t.Error("the failed revision became active")
	}
	h.k.Fail = nil
	// the kernel runs the committed revision with the observed address
	if s.Applied.Uplink.Addr.String() != "198.51.100.5/24" || s.LastError != "" || s.Applied.Revision != s.Revision {
		t.Errorf("applied %+v, error %q", s.Applied, s.LastError)
	}
	// (the observed host here is made up, so the kernel is not compared with a compile of the real one)
}

func TestAFailedApplyWithoutAWaiterIsRetried(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	var fails int
	var mu sync.Mutex
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			mu.Lock()
			defer mu.Unlock()
			if fails < 1 {
				fails++
				return &executor.Result{Exit: 1, Stderr: "Error: transient\n"}
			}
		}
		return nil
	}
	base := h.e.Snapshot()
	host := base.Host
	links := append([]compiler.HostLink(nil), host.Links...)
	for i, l := range links {
		if l.Name == "wan0" {
			links[i].Addrs = []netipPrefix{mustPrefix("198.51.100.5/24")}
		}
	}
	host.Links = links
	if err := h.e.Observe(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	s := h.barrier()
	if s.LastError == "" {
		t.Fatalf("the apply did not fail: %+v", s)
	}
	h.clk.BlockUntil(1)
	h.clk.Advance(11 * time.Second) // the retry delay
	for i := 0; i < 200; i++ {
		s = h.barrier()
		if s.LastError == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.LastError != "" || s.Applied.Uplink.Addr.String() != "198.51.100.5/24" {
		t.Fatalf("the failed apply was not retried: %+v", s)
	}
}

func TestAFirstRevisionThatFailedIsNotAppliedAgainByLaterObservations(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	if _, err := h.apply(h.revision(nil)); err == nil {
		t.Fatal("must fail")
	}
	h.k.ClearLog()
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.k.AddLink("lan9", "02:00:00:00:09:09", "veth", true)
	if err := h.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	for _, c := range h.k.Commands() {
		if strings.HasPrefix(c, "nft -j -f") {
			t.Errorf("an uncommitted candidate was applied again: %s", c)
		}
	}
}

// M5-01 test: the generation counter is persisted across a restart instead of starting over at 1.
func TestGenerationContinuesAfterRestart(t *testing.T) {
	h := newHarness(t)
	genFile := filepath.Join(h.dir, "generation")

	e1, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: h.clk, GenerationFile: genFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := e1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.e = e1
	a := h.mustApply(h.revision(nil))
	e1.Close()

	e2, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: h.clk, GenerationFile: genFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	snap2, err := e2.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap2.Generation <= a.Generation {
		t.Fatalf("generation did not continue across the restart: %d, then %d", a.Generation, snap2.Generation)
	}
}
