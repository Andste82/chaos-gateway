package engine_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

var (
	alice = model.Owner{Type: "token", Id: "11111111-1111-4111-8111-111111111111", Name: ptr("alice")}
	bob   = model.Owner{Type: "token", Id: "22222222-2222-4222-8222-222222222222", Name: ptr("bob")}
	admin = model.Owner{Type: "user", Id: "admin"}
)

func overlayRequest(t *testing.T, body string) model.OverlayRequest {
	t.Helper()
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return *req
}

// put writes an overlay and returns the result.
func (h *harness) put(owner model.Owner, body string) (engine.OverlayResult, error) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return h.e.PutOverlay(ctx, engine.OverlayWrite{Owner: owner, Request: overlayRequest(h.t, body)})
}

func (h *harness) mustPut(owner model.Owner, body string) engine.OverlayResult {
	h.t.Helper()
	r, err := h.put(owner, body)
	if err != nil {
		h.t.Fatalf("put %q: %v", body, err)
	}
	return r
}

const iotLatency = `
target: {network: IoT}
fault: {latency: 100ms}`

// started returns a harness with the engine running the first revision.
func startedWithRevision(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	return h
}

// kernelFaultChains lists the mark chains the kernel has.
func (h *harness) kernelFaultChains() []string {
	h.t.Helper()
	rs, err := apply.ReadSets(context.Background(), apply.Local{E: h.ex}, "")
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, o := range rs.Objects {
		if o.Chain != nil && strings.HasPrefix(o.Chain.Name, "mark_") && o.Chain.Name != "mark_0" {
			out = append(out, o.Chain.Name)
		}
	}
	return out
}

func TestAnOverlayWriteIsAppliedAndVerifiedBeforeItReturns(t *testing.T) {
	h := startedWithRevision(t)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	before := h.e.Snapshot()

	res := h.mustPut(alice, iotLatency)
	if !res.Created || res.Overlay.Owner.Id != alice.Id || res.Overlay.Kind != model.OverlayKindFault {
		t.Fatalf("%+v", res)
	}
	s := h.e.Snapshot()
	// the answer comes after the verify: a generation was applied that contains the change
	if s.Applied == nil || s.Applied.Generation < res.Generation || res.Generation <= before.Generation || uint64(res.Overlay.Generation) > res.Generation {
		t.Fatalf("answered with generation %d, overlay generation %d, applied %+v, before %d", res.Generation, res.Overlay.Generation, s.Applied, before.Generation)
	}
	if len(s.Overlays) != 1 || s.Overlays[0].Id != res.Overlay.Id {
		t.Fatalf("the snapshot has %+v", s.Overlays)
	}
	// the real fault id feeds the kernel: a chain, named counters, and the snapshot's fault list
	if len(s.Faults) != 1 || s.Faults[0].Layer != "overlay" || s.Faults[0].Source != res.Overlay.Id.String() || s.Faults[0].ID == 0 {
		t.Fatalf("faults %+v", s.Faults)
	}
	if chains := h.kernelFaultChains(); len(chains) != 1 || chains[0] != compiler.MarkChainName(s.Faults[0].ID) {
		t.Errorf("the kernel has chains %v for fault %+v", chains, s.Faults[0])
	}
	counters, err := h.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := counters[s.Faults[0].CounterUp]; !ok {
		t.Errorf("no counter %s in %v", s.Faults[0].CounterUp, counters)
	}
	h.verifyKernelWithOverlays()
	got := collect(ch, engine.EventOverlayCreated)
	if len(got) != 1 || got[0].Data["overlay"] != res.Overlay.Id.String() || got[0].Data["generation"].(uint64) != uint64(res.Overlay.Generation) {
		t.Errorf("events %+v", got)
	}
	if a, ok := got[0].Data["actor"].(model.Actor); !ok || a.Id != alice.Id {
		t.Errorf("the event names no actor: %+v", got[0].Data)
	}
}

// verifyKernelWithOverlays checks that the kernel matches the compile of the snapshot with its overlays.
func (h *harness) verifyKernelWithOverlays() {
	h.t.Helper()
	s := h.e.Snapshot()
	ctx := context.Background()
	st, err := apply.ReadState(ctx, apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	tg := compiler.Compile(compiler.Input{Config: s.Config, Host: st.Host(), Overlays: s.Overlays, FaultIDs: s.FaultIDs,
		Identity: &s.Identity, Generation: compiler.Generation{Revision: s.Applied.Revision, Seq: s.Applied.Generation}, ClassLimit: h.classLimit})
	st, err = apply.ReadState(ctx, apply.Local{E: h.ex}, "", apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
	if err != nil {
		h.t.Fatal(err)
	}
	if mm := apply.Verify(tg, st); len(mm) != 0 {
		h.t.Fatalf("the kernel does not match the compile of the snapshot: %v", mm)
	}
}

func TestWritingAnOverlayWithAnExistingKeyReplacesItAndKeepsTheId(t *testing.T) {
	h := startedWithRevision(t)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	first := h.mustPut(alice, iotLatency)
	h.clk.Advance(time.Second)
	second := h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 250ms, loss: 5%}")
	if second.Created {
		t.Fatal("a replacement is not a creation (HTTP 200)")
	}
	if second.Overlay.Id != first.Overlay.Id || !second.Overlay.CreatedAt.Equal(first.Overlay.CreatedAt) {
		t.Fatalf("the replacement has id %s, created %s; want %s, %s", second.Overlay.Id, second.Overlay.CreatedAt, first.Overlay.Id, first.Overlay.CreatedAt)
	}
	if !second.Overlay.UpdatedAt.After(first.Overlay.UpdatedAt) || second.Generation <= first.Generation {
		t.Errorf("updated %v -> %v, generation %d -> %d", first.Overlay.UpdatedAt, second.Overlay.UpdatedAt, first.Generation, second.Generation)
	}
	s := h.e.Snapshot()
	if len(s.Overlays) != 1 || *s.Overlays[0].Fault.Latency != "250ms" {
		t.Fatalf("%+v", s.Overlays)
	}
	// the fault keeps its id and its counters while its parameters change
	if len(s.Faults) != 1 || s.Faults[0].Upload.Delay != 250*time.Millisecond {
		t.Fatalf("%+v", s.Faults)
	}
	if got := collect(ch, engine.EventOverlayUpdated); len(got) != 1 || got[0].Data["reason"] != "replaced" {
		t.Errorf("events %+v", got)
	}
	// another owner's overlay with the same body is another overlay
	other := h.mustPut(bob, iotLatency)
	if !other.Created || other.Overlay.Id == first.Overlay.Id {
		t.Errorf("the key includes the owner: %+v", other)
	}
	h.verifyKernelWithOverlays()
}

func TestAFaultKeepsItsIdWhileItsParametersChange(t *testing.T) {
	h := startedWithRevision(t)
	a := h.mustPut(alice, iotLatency)
	id := h.e.Snapshot().Faults[0].ID
	b := h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 50ms}")
	// replacing the first one does not renumber it or move the other one
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 300ms}")
	s := h.e.Snapshot()
	ids := map[string]int{}
	for _, f := range s.Faults {
		ids[f.Source] = f.ID
	}
	if ids[a.Overlay.Id.String()] != id || len(ids) != 2 || ids[b.Overlay.Id.String()] == 0 {
		t.Errorf("ids %v, the first fault had %d", ids, id)
	}
}

func TestTheTTLRemovesAnOverlayAndAnEventSaysSo(t *testing.T) {
	h := startedWithRevision(t)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	res := h.mustPut(alice, iotLatency+"\nttl: 30s")
	if res.Overlay.ExpiresAt == nil || !res.Overlay.ExpiresAt.Equal(h.clk.Now().Add(30*time.Second)) {
		t.Fatalf("expires %v", res.Overlay.ExpiresAt)
	}
	h.clk.Advance(29 * time.Second)
	if s := h.barrier(); len(s.Overlays) != 1 {
		t.Fatalf("the overlay went early: %+v", s.Overlays)
	}
	h.clk.Advance(2 * time.Second)
	s := h.waitOverlays(0)
	if len(h.kernelFaultChains()) != 0 || len(s.Faults) != 0 {
		t.Errorf("the kernel still has the fault: %v %v", h.kernelFaultChains(), s.Faults)
	}
	got := collect(ch, engine.EventOverlayExpired)
	if len(got) != 1 || got[0].Data["reason"] != "ttl" || got[0].Data["overlay"] != res.Overlay.Id.String() {
		t.Errorf("events %+v", got)
	}
	if a, ok := got[0].Data["actor"].(model.Actor); !ok || a.Type != "system" {
		t.Errorf("an expiry is the system's: %+v", got[0].Data["actor"])
	}
	h.verifyKernelWithOverlays()
}

// waitOverlays waits until the snapshot holds n overlays and the apply is settled.
func (h *harness) waitOverlays(n int) *engine.Snapshot {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s := h.e.Snapshot(); len(s.Overlays) == n {
			return h.barrier()
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("the snapshot never held %d overlays: %+v", n, h.e.Snapshot().Overlays)
	return nil
}

func TestALeaseNeedsRenewingAndRunsOnTheMonotonicClock(t *testing.T) {
	h := startedWithRevision(t)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	res := h.mustPut(alice, iotLatency+"\nlease: 10s")
	ctx := context.Background()

	h.clk.Advance(8 * time.Second)
	// a jump of the wall clock neither shortens nor extends the lease
	h.clk.JumpWall(-time.Hour)
	renewed, err := h.e.RenewOverlay(ctx, res.Overlay.Id, &alice)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Generation != h.e.Snapshot().Generation {
		t.Errorf("renewing makes no generation: answered %d, snapshot %d", renewed.Generation, h.e.Snapshot().Generation)
	}
	if want := h.clk.Now().Add(10 * time.Second); !renewed.Overlay.LeaseExpiresAt.Equal(want) {
		t.Errorf("the renewed lease runs out %v, want %v", renewed.Overlay.LeaseExpiresAt, want)
	}
	h.clk.Advance(8 * time.Second) // 16 s after the write, 8 s after the renewal
	if s := h.barrier(); len(s.Overlays) != 1 {
		t.Fatalf("the renewed overlay is gone: %+v", s.Overlays)
	}
	if got := h.e.Snapshot().Overlays[0].LeaseExpiresAt; !got.Equal(h.clk.Now().Add(2 * time.Second)) {
		t.Errorf("the snapshot shows the lease running out at %v", got)
	}
	h.clk.Advance(3 * time.Second)
	h.waitOverlays(0)
	got := collect(ch, engine.EventOverlayExpired)
	if len(got) != 1 || got[0].Data["reason"] != "lease" {
		t.Errorf("events %+v", got)
	}
	// an overlay without a lease cannot be renewed
	noLease := h.mustPut(alice, iotLatency)
	if _, err := h.e.RenewOverlay(ctx, noLease.Overlay.Id, nil); !errors.Is(err, engine.ErrOverlayNoLease) {
		t.Errorf("renewing an overlay without a lease: %v", err)
	}
	if _, err := h.e.RenewOverlay(ctx, res.Overlay.Id, nil); !errors.Is(err, engine.ErrOverlayNotFound) {
		t.Errorf("renewing an expired overlay: %v", err)
	}
}

func TestTheEarliestDeadlineOfTwoOverlaysFiresFirst(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, iotLatency+"\nttl: 60s")
	short := h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 10ms}\nttl: 20s")
	h.clk.Advance(21 * time.Second)
	s := h.waitOverlays(1)
	if s.Overlays[0].Id == short.Overlay.Id {
		t.Fatal("the wrong overlay expired")
	}
	h.clk.Advance(40 * time.Second)
	h.waitOverlays(0)
}

func TestResetOnlyTouchesTheCallersOverlays(t *testing.T) {
	h := startedWithRevision(t)
	ctx := context.Background()
	h.mustPut(alice, iotLatency)
	h.mustPut(alice, "target: {network: Lab}\nfault: {latency: 20ms}")
	keep := h.mustPut(bob, "target: {network: Lab}\nfault: {loss: 3%}")
	ch, cancel := h.e.Subscribe()
	defer cancel()

	res, err := h.e.ResetOverlays(ctx, &alice, alice)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 {
		t.Fatalf("removed %d", res.Removed)
	}
	s := h.e.Snapshot()
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id {
		t.Fatalf("%+v", s.Overlays)
	}
	if got := collect(ch, engine.EventOverlayRemoved); len(got) != 2 || got[0].Data["reason"] != "reset" {
		t.Errorf("events %+v", got)
	}
	// a reset that finds nothing changes nothing and makes no generation
	gen := h.e.Snapshot().Generation
	res, err = h.e.ResetOverlays(ctx, &alice, alice)
	if err != nil || res.Removed != 0 || h.e.Snapshot().Generation != gen || res.Generation != gen {
		t.Errorf("an empty reset: %+v %v", res, err)
	}
	// all owners
	res, err = h.e.ResetOverlays(ctx, nil, admin)
	if err != nil || res.Removed != 1 || len(h.e.Snapshot().Overlays) != 0 {
		t.Errorf("reset of all: %+v %v", res, err)
	}
	h.verifyKernelWithOverlays()
}

func TestADeletionIsLimitedToTheOwnersOverlaysWhenAskedTo(t *testing.T) {
	h := startedWithRevision(t)
	ctx := context.Background()
	mine := h.mustPut(alice, iotLatency)
	if _, err := h.e.DeleteOverlay(ctx, mine.Overlay.Id, &bob, bob); !errors.Is(err, engine.ErrOverlayForbidden) {
		t.Errorf("deleting another owner's overlay: %v", err)
	}
	if _, err := h.e.RenewOverlay(ctx, mine.Overlay.Id, &bob); !errors.Is(err, engine.ErrOverlayForbidden) {
		t.Errorf("renewing another owner's overlay: %v", err)
	}
	if len(h.e.Snapshot().Overlays) != 1 {
		t.Fatal("a refused deletion removed the overlay")
	}
	res, err := h.e.DeleteOverlay(ctx, mine.Overlay.Id, &alice, alice)
	if err != nil || res.Overlay.Id != mine.Overlay.Id {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := h.e.DeleteOverlay(ctx, mine.Overlay.Id, nil, admin); !errors.Is(err, engine.ErrOverlayNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	// without a limit (the full scope) any overlay can go
	other := h.mustPut(bob, iotLatency)
	if _, err := h.e.DeleteOverlay(ctx, other.Overlay.Id, nil, admin); err != nil {
		t.Error(err)
	}
	h.verifyKernelWithOverlays()
}

func TestARestartDropsTheOverlays(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, iotLatency)
	h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 20ms}\nlease: 1h")
	if len(h.kernelFaultChains()) == 0 {
		t.Fatal("no fault in the kernel")
	}
	h.e.Close()
	// the same store, the same kernel, a new process
	h.start()
	s := h.barrier()
	if len(s.Overlays) != 0 || len(s.Faults) != 0 {
		t.Fatalf("overlays survived the restart: %+v %+v", s.Overlays, s.Faults)
	}
	if chains := h.kernelFaultChains(); len(chains) != 0 {
		t.Errorf("the kernel still has fault chains after the restart: %v", chains)
	}
	h.verifyKernelWithOverlays()
	h.clk.Advance(2 * time.Hour) // nothing is armed for overlays that are gone
	if len(h.barrier().Overlays) != 0 {
		t.Error("an overlay appeared")
	}
}

func TestAnOverlayThatCannotBeAppliedIsTakenBackAndItsWriterGetsApplyFailed(t *testing.T) {
	h := startedWithRevision(t)
	keep := h.mustPut(alice, iotLatency)
	before := h.e.Snapshot()
	ch, cancel := h.e.Subscribe()
	defer cancel()
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		// only the transaction that creates the second fault's chain fails; the restore does not
		if argv[0] == "nft" && strings.Contains(stdin, "mark_2") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	_, err := h.put(bob, "target: {network: Lab}\nfault: {latency: 20ms}")
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("got %v", err)
	}
	s := h.barrier()
	h.k.Fail = nil
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id {
		t.Fatalf("the failed write is still active: %+v", s.Overlays)
	}
	if s.LastError != "" {
		t.Errorf("the restore did not apply: %s", s.LastError)
	}
	if len(s.Faults) != len(before.Faults) {
		t.Errorf("faults %+v", s.Faults)
	}
	h.verifyKernelWithOverlays()
	if got := collect(ch, engine.EventOverlayCreated); len(got) != 0 {
		t.Errorf("an overlay that never became active announced itself: %+v", got)
	}
	// the owner can write it again now
	if r := h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 20ms}"); !r.Created {
		t.Errorf("%+v", r)
	}
}

func TestAnInvalidOverlayIsRefusedWithoutChangingAnything(t *testing.T) {
	h := startedWithRevision(t)
	gen := h.e.Snapshot().Generation
	for name, body := range map[string]string{
		"an unknown network":        "target: {network: Nowhere}\nfault: {latency: 10ms}",
		"jitter above latency":      "target: {network: IoT}\nfault: {latency: 10ms, jitter: 50ms}",
		"no target":                 "fault: {latency: 10ms}",
		"nothing to switch on":      "target: {network: IoT}",
		"an unknown device":         "target: {device: 8a2c9d3e-1111-4222-8333-444455556666}\nfault: {latency: 10ms}",
		"a destination hostname ok": "",
	} {
		if body == "" {
			continue
		}
		_, err := h.put(alice, body)
		var ve domain.ValidationErrors
		if !errors.As(err, &ve) || len(ve) == 0 {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if len(h.e.Snapshot().Overlays) != 0 || h.e.Snapshot().Generation != gen {
		t.Error("a refused overlay changed the state")
	}
}

func TestOverlaysOfLaterMilestonesAreUnsupported(t *testing.T) {
	h := startedWithRevision(t)
	for body, milestone := range map[string]string{
		"target: {network: IoT}\nrule: {action: drop}":                               "M9",
		"target: {network: IoT}\nprofile: bad-lte":                                   "M11",
		"target: {network: IoT}\ndns: {names: [example.com], action: nxdomain}":      "M20",
		"target: {network: IoT}\ntls: {case: expired}":                               "M21",
		"target: {network: IoT}\ndhcp: {action: silence}":                            "M23",
		"target: {network: IoT}\nfault: {family: mtu, mtu: {size: 1200}}":            "M10",
		"fault: {family: tunnel, tunnel: {client: x}, latency: 10ms}":                "M10",
		"wireguard: {action: disable, client: 8a2c9d3e-1111-4222-8333-444455556666}": "M10",
	} {
		_, err := h.put(alice, body)
		var ue *engine.UnsupportedOverlayError
		if !errors.As(err, &ue) || ue.Milestone != milestone {
			t.Errorf("%q: got %v, want unsupported until %s", body, err, milestone)
		}
	}
}

func TestAnOverlayThatExceedsTheClassLimitIsRefusedAndNothingChanges(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{ClassLimit: 6})
	h.mustApply(h.revision(nil))
	// every impaired fault takes a class per direction, and the limit of an interface counts the
	// default class too: two faults fit into 6 (5 classes), a third does not (7)
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 10ms}")
	h.mustPut(alice, "target: {network: Lab}\nfault: {latency: 10ms}")
	gen := h.e.Snapshot().Generation
	var cerr *engine.CompileError
	for i := 0; i < 6; i++ {
		_, err := h.put(bob, fmt.Sprintf("target: {network: IoT}\nfault: {latency: %dms, destination: {cidr: 198.51.100.%d/32}}", 10+i, i))
		if errors.As(err, &cerr) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if cerr == nil || len(cerr.Problems) == 0 || cerr.Problems[0].Code != compiler.CodeCapacityExceeded {
		t.Fatalf("got %v", cerr)
	}
	s := h.barrier()
	if s.LastError != "" {
		t.Errorf("a refused overlay reached the kernel: %s", s.LastError)
	}
	if s.Generation < gen {
		t.Errorf("generation went back")
	}
	h.verifyKernelWithOverlays()
	n := len(s.Overlays)
	// the refused overlay is not there, and writing a valid one still works
	if _, err := h.put(alice, "target: {network: IoT}\nfault: {latency: 11ms}"); err != nil {
		t.Errorf("a valid write after a refused one: %v", err)
	}
	if got := len(h.e.Snapshot().Overlays); got != n {
		t.Errorf("%d overlays, want %d", got, n)
	}
}

func (h *harness) startWith(cfg engine.Config) {
	h.t.Helper()
	cfg.Store, cfg.Exec, cfg.Clock = h.st, apply.Local{E: h.ex}, h.clk
	if cfg.ClassLimit == 0 {
		// not the architecture default: a burst of 200 overlays does not fit the 200 classes of ARM64
		cfg.ClassLimit = compiler.DefaultClassLimitX86
	}
	h.classLimit = cfg.ClassLimit
	e, err := engine.New(cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(e.Close)
	h.e = e
}

func TestConcurrentOverlayWritesAllLandAndTheKernelMatchesTheFinalState(t *testing.T) {
	h := startedWithRevision(t)
	const n = 40
	var wg sync.WaitGroup
	results := make([]engine.OverlayResult, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := alice
			if i%2 == 1 {
				owner = bob
			}
			results[i], errs[i] = h.put(owner, fmt.Sprintf("target: {global: true}\nfault: {latency: %dms, destination: {cidr: 198.51.%d.0/24}}", 10+i, i))
		}()
	}
	wg.Wait()
	final := h.barrier()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		// every writer got a generation that contains its change
		if results[i].Generation == 0 || results[i].Generation < uint64(results[i].Overlay.Generation) {
			t.Errorf("write %d: answered %d for an overlay of generation %d", i, results[i].Generation, results[i].Overlay.Generation)
		}
	}
	if len(final.Overlays) != n {
		t.Fatalf("%d overlays, want %d", len(final.Overlays), n)
	}
	h.verifyKernelWithOverlays()
}

func TestOverlaysAreValidatedAgainstTheActiveConfiguration(t *testing.T) {
	h := newHarness(t)
	h.start()
	if _, err := h.put(alice, iotLatency); !errors.Is(err, engine.ErrNoConfiguration) {
		t.Fatalf("an overlay before any configuration: %v", err)
	}
}

func TestExplainNamesTheWinnerPerFamilyAndTheOverriddenFaults(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 100ms}")
	dev := h.mustPut(alice, "target: {device: esp32-42}\nfault: {latency: 400ms, destination: {cidr: 203.0.113.0/24}}")
	h.mustPut(bob, "target: {global: true}\nfault: {loss: 2%}")
	h.barrier()

	ex, err := h.e.Explain(context.Background(), engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.9", Protocol: "tcp", Port: 443})
	if err != nil {
		t.Fatal(err)
	}
	var imp *engine.ExplainFamily
	for i := range ex.Faults {
		if ex.Faults[i].Family == "impairment" {
			imp = &ex.Faults[i]
		}
	}
	if imp == nil || imp.Winner == nil || imp.Winner.Id != dev.Overlay.Id || imp.Winner.Layer != "overlay" || *imp.Winner.Level != 2 {
		t.Fatalf("%+v", ex.Faults)
	}
	if len(imp.Overridden) != 2 {
		t.Errorf("overridden %+v", imp.Overridden)
	}
	if ex.Source.Device == nil || ex.Source.Device.Name != "esp32-42" || ex.Source.Network == nil || ex.Source.Network.Name != "IoT" || ex.Source.IP != "10.10.0.31" {
		t.Errorf("source %+v", ex.Source)
	}
	if ex.Kernel == nil || ex.Kernel.FaultID == 0 || ex.Kernel.MarkUpload != fmt.Sprintf("0x%08x", ex.Kernel.FaultID<<4) || ex.Kernel.MarkDownload != fmt.Sprintf("0x%08x", ex.Kernel.FaultID<<4|1<<16) {
		t.Errorf("kernel %+v", ex.Kernel)
	}
	if ex.Generation != int64(h.e.Snapshot().Generation) {
		t.Errorf("generation %d", ex.Generation)
	}
	// another destination of the same device: the network's fault wins over the global one
	ex, err = h.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.31"), Dst: "198.51.100.1"})
	if err != nil {
		t.Fatal(err)
	}
	if w := ex.Faults[0].Winner; ex.Faults[0].Family != "impairment" || w == nil || *w.Level != 8 || w.Layer != "overlay" {
		t.Errorf("%+v", ex.Faults)
	}
	if _, err := h.e.Explain(context.Background(), engine.ExplainQuery{Device: "nobody", Dst: "198.51.100.1"}); !errors.Is(err, engine.ErrUnknownDevice) {
		t.Errorf("an unknown device: %v", err)
	}
}

func TestExplainTakesTheRouteFromTheKernelAndJudgesAccess(t *testing.T) {
	h := startedWithRevision(t)
	ctx := context.Background()
	ex, err := h.e.Explain(ctx, engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.31"), Dst: "198.51.100.7", Protocol: "tcp", Port: 443})
	if err != nil {
		t.Fatal(err)
	}
	// traffic from a test network is looked up in table 100 (the policy rules decide, the kernel answers)
	if ex.Route == nil || ex.Route.Table != 100 || ex.Route.Gateway != "203.0.113.10" || ex.Route.Interface != "wan0" || ex.Route.Unreachable {
		t.Errorf("route %+v", ex.Route)
	}
	if ex.Access.Verdict != "allow" || ex.Access.Layer != "access_matrix" || ex.Access.Reason == "" {
		t.Errorf("access %+v", ex.Access)
	}
	if ex.Source.Network == nil || ex.Source.Network.Name != "IoT" || ex.Kernel != nil || len(ex.Faults) != 0 || ex.Service != "none" {
		t.Errorf("%+v", ex)
	}
	// traffic to the gateway itself is the gateway protection's business
	ex, err = h.e.Explain(ctx, engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.31"), Dst: "10.10.0.1", Protocol: "tcp", Port: 22})
	if err != nil || ex.Access.Verdict != "drop" || ex.Access.Layer != "gateway_protection" {
		t.Errorf("%+v %v", ex.Access, err)
	}
	// a hostname has no address to look up
	ex, err = h.e.Explain(ctx, engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.31"), Dst: "example.com"})
	if err != nil || ex.Route != nil || ex.Destination == nil || ex.Destination.Hostname != "example.com" {
		t.Errorf("%+v %v", ex, err)
	}
	// neither a device nor a source address: nothing to explain
	if ex, err := h.e.Explain(ctx, engine.ExplainQuery{Dst: "198.51.100.7"}); err == nil {
		t.Errorf("%+v", ex)
	}
}

// A renewal moves a deadline and nothing else; a write of another owner that fails to apply, or is
// refused after it was stored, takes the store back to an earlier checkpoint. The renewal must
// survive that, or a client that keeps its lease alive loses the overlay to somebody else's error.
func TestARenewedLeaseSurvivesAFailedApplyOfAnotherWrite(t *testing.T) {
	h := startedWithRevision(t)
	res := h.mustPut(alice, iotLatency+"\nlease: 10s")
	h.clk.Advance(8 * time.Second)
	if _, err := h.e.RenewOverlay(context.Background(), res.Overlay.Id, &alice); err != nil { // runs out at 18 s
		t.Fatal(err)
	}
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(stdin, "mark_2") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	_, err := h.put(bob, "target: {network: Lab}\nfault: {latency: 20ms}")
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) {
		t.Fatalf("got %v", err)
	}
	h.k.Fail = nil
	h.clk.Advance(3 * time.Second) // 11 s after the write: the first lease would have run out
	if s := h.barrier(); len(s.Overlays) != 1 || s.Overlays[0].Id != res.Overlay.Id {
		t.Fatalf("the renewal was undone by the failed apply of another write: %+v", s.Overlays)
	}
	if got := h.e.Snapshot().Overlays[0].LeaseExpiresAt; !got.Equal(h.clk.Now().Add(7 * time.Second)) {
		t.Errorf("the lease runs out at %v, want 7 s from now", got)
	}
	h.clk.Advance(8 * time.Second) // 19 s: past the renewed lease
	h.waitOverlays(0)
}

func TestARenewedLeaseSurvivesAWriteThatTheCompilerRefuses(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{ClassLimit: 6})
	h.mustApply(h.revision(nil))
	res := h.mustPut(alice, iotLatency+"\nlease: 10s")
	h.mustPut(alice, "target: {network: Lab}\nfault: {latency: 10ms}")
	h.clk.Advance(8 * time.Second)
	if _, err := h.e.RenewOverlay(context.Background(), res.Overlay.Id, &alice); err != nil {
		t.Fatal(err)
	}
	var cerr *engine.CompileError
	if _, err := h.put(bob, "target: {network: IoT}\nfault: {latency: 30ms, destination: {cidr: 198.51.100.1/32}}"); !errors.As(err, &cerr) {
		t.Fatalf("got %v, want capacity_exceeded", err)
	}
	h.clk.Advance(3 * time.Second)
	s := h.barrier()
	found := false
	for _, o := range s.Overlays {
		if o.Id == res.Overlay.Id {
			found = true
			if !o.LeaseExpiresAt.Equal(h.clk.Now().Add(7 * time.Second)) {
				t.Errorf("the lease runs out at %v, want 7 s from now", o.LeaseExpiresAt)
			}
		}
	}
	if !found {
		t.Fatalf("the renewal was undone by a refused write: %+v", s.Overlays)
	}
}

func TestAReplacementThatFailsToApplyKeepsTheRenewalOfTheOverlayItReplaced(t *testing.T) {
	h := startedWithRevision(t)
	res := h.mustPut(alice, iotLatency+"\nlease: 10s")
	h.clk.Advance(8 * time.Second)
	if _, err := h.e.RenewOverlay(context.Background(), res.Overlay.Id, &alice); err != nil {
		t.Fatal(err)
	}
	failed := false
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && stdin != "" && !failed { // the transaction of the replacement, not the restore
			failed = true
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	// the same key with other parameters: a replacement, which restarts the lease, then fails
	_, err := h.put(alice, "target: {network: IoT}\nfault: {latency: 250ms}\nlease: 4s")
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) {
		t.Fatalf("got %v", err)
	}
	h.k.Fail = nil
	h.clk.Advance(3 * time.Second) // 11 s: only the renewal keeps the original overlay
	s := h.barrier()
	if len(s.Overlays) != 1 || s.Overlays[0].Id != res.Overlay.Id {
		t.Fatalf("the original overlay is gone: %+v", s.Overlays)
	}
	if got := s.Overlays[0].LeaseExpiresAt; !got.Equal(h.clk.Now().Add(7 * time.Second)) {
		t.Errorf("the lease runs out at %v, want 7 s from now (10 s lease renewed at 8 s)", got)
	}
}

// A token with the overlays scope must not be able to stall the state owner: a burst of writes
// whose destination-only and port-only selectors would need more classification cells than the
// limit (their product) is answered in a bounded time, accepted until the limit and refused with
// capacity_exceeded after it, and the state stays valid.
func TestABurstOfOverlaysThatOverflowsTheClassificationIsRefusedAtTheLimit(t *testing.T) {
	h := startedWithRevision(t)
	// 16 overlays of 64 ports each are 1024 port pieces; every destination-only overlay on top
	// multiplies them (8192 cells is the limit of one table)
	const ports, dests = 16, 9
	var wg sync.WaitGroup
	errs := make([]error, ports+dests)
	start := time.Now()
	for i := 0; i < ports+dests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := model.Owner{Type: "token", Id: fmt.Sprintf("tok-%d", i)}
			if i < ports {
				var list []string
				for k := 0; k < 64; k++ {
					list = append(list, fmt.Sprint(1000+i*64+k))
				}
				_, errs[i] = h.put(owner, fmt.Sprintf("target: {network: IoT}\nfault: {loss: 1%%, protocol: tcp, ports: [%s]}", strings.Join(list, ",")))
				return
			}
			_, errs[i] = h.put(owner, fmt.Sprintf("target: {network: IoT}\nfault: {latency: 50ms, destination: {cidr: 11.0.%d.0/24}}", i))
		}()
	}
	wg.Wait()
	took := time.Since(start)
	accepted, refused := 0, 0
	for i, err := range errs {
		var cerr *engine.CompileError
		switch {
		case err == nil:
			accepted++
		case errors.As(err, &cerr) && cerr.Problems[0].Code == compiler.CodeCapacityExceeded:
			refused++
		default:
			t.Errorf("write %d: %v", i, err)
		}
	}
	if accepted == 0 || refused == 0 {
		t.Fatalf("accepted %d, refused %d: the limit was not reached or not enforced", accepted, refused)
	}
	t.Logf("%d accepted, %d refused, %v", accepted, refused, took)
	if got := len(h.barrier().Overlays); got != accepted {
		t.Errorf("%d overlays active, %d accepted", got, accepted)
	}
	h.verifyKernelWithOverlays()
}
