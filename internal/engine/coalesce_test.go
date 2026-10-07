package engine_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Coalescing apply (plan §3.11 item 4) and the reader pool: the tests count what reaches the kernel
// and hold the executor's writer in the middle of an apply, so the order of events is fixed.

// writeGate wraps the kernel simulator the executor runs against. It counts the nft transactions
// (every apply is one) and, while armed, holds them in the executor's single writer.
type writeGate struct {
	inner executor.Runner

	mu      sync.Mutex
	writes  int
	hold    chan struct{}
	entered chan struct{}
}

func newGate(k *kernelsim.Kernel) *writeGate { return &writeGate{inner: k} }

func (g *writeGate) Run(ctx context.Context, c executor.Command) (executor.Result, error) {
	if c.Tool == executor.ToolNft && c.Stdin != "" {
		g.mu.Lock()
		g.writes++
		hold, entered := g.hold, g.entered
		g.mu.Unlock()
		if hold != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
	}
	return g.inner.Run(ctx, c)
}

// arm makes the next nft transactions wait until release is called; waitEntered returns once one
// of them is held.
func (g *writeGate) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hold, g.entered = make(chan struct{}), make(chan struct{}, 1)
}

func (g *writeGate) waitEntered(t *testing.T) {
	t.Helper()
	g.mu.Lock()
	entered := g.entered
	g.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the executor never reached the held transaction")
	}
}

func (g *writeGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hold != nil {
		close(g.hold)
		g.hold = nil
	}
}

func (g *writeGate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.writes
}

// gatedHarness is a started engine with its first revision applied and a gate in front of the kernel.
func gatedHarness(t *testing.T, cfg engine.Config) (*harness, *writeGate) {
	t.Helper()
	var g *writeGate
	h := newHarnessRunner(t, func(k *kernelsim.Kernel) executor.Runner { g = newGate(k); return g })
	h.startWith(cfg)
	h.mustApply(h.revision(nil))
	return h, g
}

// waitAccepted waits until the state owner has accepted n overlays: from then on a held apply
// cannot contain them, so the next one does.
func (h *harness) waitAccepted(n int) {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for len(h.e.Snapshot().Overlays) < n {
		if time.Now().After(deadline) {
			h.t.Fatalf("the state owner accepted %d of %d overlays", len(h.e.Snapshot().Overlays), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func globalLatency(i int) string {
	return fmt.Sprintf("target: {global: true}\nfault: {latency: %dms, destination: {cidr: 198.51.%d.%d/32}}", 10+i%90, i/250, i%250)
}

type writeOutcome struct {
	res engine.OverlayResult
	err error
}

// putAll writes the bodies concurrently, alternating the owner, and returns the outcomes in order.
func (h *harness) putAll(bodies []string) []writeOutcome {
	out := make([]writeOutcome, len(bodies))
	var wg sync.WaitGroup
	for i, body := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := alice
			if i%2 == 1 {
				owner = bob
			}
			out[i].res, out[i].err = h.put(owner, body)
		}()
	}
	wg.Wait()
	return out
}

func appliedGenerations(events []engine.Event) map[uint64]bool {
	gens := map[uint64]bool{}
	for _, ev := range events {
		if g, ok := ev.Data["generation"].(uint64); ok {
			gens[g] = true
		}
	}
	return gens
}

// 200 concurrent writes are applied together (plan §3.11 item 4): while one apply is in flight the
// writes that arrive go into the next, so the kernel sees two transactions instead of 200. Every
// writer gets the generation that was actually applied, which contains its change, and the kernel
// ends up as the compile of the final snapshot.
func TestTwoHundredConcurrentOverlayWritesCostFarFewerApplies(t *testing.T) {
	h, g := gatedHarness(t, engine.Config{})
	events, cancel := h.e.Subscribe()
	defer cancel()
	base := g.count()
	const n = 200
	bodies := make([]string, n)
	for i := range bodies {
		bodies[i] = globalLatency(i)
	}

	// the first write starts an apply that waits in the executor; the other 199 arrive meanwhile
	g.arm()
	first := make(chan writeOutcome, 1)
	go func() { r, err := h.put(alice, bodies[0]); first <- writeOutcome{r, err} }()
	g.waitEntered(t)
	rest := make(chan []writeOutcome, 1)
	go func() { rest <- h.putAll(bodies[1:]) }()
	h.waitAccepted(n)
	g.release()
	all := append([]writeOutcome{<-first}, <-rest...)

	final := h.barrier()
	applies := g.count() - base
	t.Logf("%d writes took %d applies", n, applies)
	if applies > 3 {
		t.Errorf("%d writes needed %d applies, want at most 3", n, applies)
	}
	applied := appliedGenerations(collect(events, engine.EventApplied))
	for i, o := range all {
		if o.err != nil {
			t.Fatalf("write %d: %v", i, o.err)
		}
		if !o.res.Created {
			t.Errorf("write %d did not create", i)
		}
		// the generation the writer gets is one that was applied and verified, and it is not older
		// than the generation of the change
		if !applied[o.res.Generation] || o.res.Generation < uint64(o.res.Overlay.Generation) {
			t.Errorf("write %d: answered with generation %d for a change of generation %d (applied: %v)", i, o.res.Generation, o.res.Overlay.Generation, applied)
		}
	}
	if len(final.Overlays) != n {
		t.Fatalf("%d overlays, want %d", len(final.Overlays), n)
	}
	for _, o := range all {
		if final.Applied.Generation < o.res.Generation {
			t.Fatalf("the snapshot applied generation %d, a writer was told %d", final.Applied.Generation, o.res.Generation)
		}
	}
	h.verifyKernelWithOverlays()
}

// The same burst without anyone holding the executor: the writers do not wait for each other, and
// the owner checks a burst with a few compiles, not one per write. The plan's target (§3.10: 100
// writes within 1 to 2 s) is one of real hardware; the bound here is looser, because this runs on
// the simulated kernel under the race detector on whatever CI machine, and it guards against the
// cost growing with the square of the burst, which is what one compile per write was.
func TestAHundredWritesWithoutAHeldExecutorAreQuick(t *testing.T) {
	h, g := gatedHarness(t, engine.Config{})
	base := g.count()
	bodies := make([]string, 100)
	for i := range bodies {
		bodies[i] = globalLatency(i)
	}
	start := time.Now()
	out := h.putAll(bodies)
	took := time.Since(start)
	for i, o := range out {
		if o.err != nil {
			t.Fatalf("write %d: %v", i, o.err)
		}
	}
	t.Logf("100 writes in %v with %d applies", took, g.count()-base)
	if took > 5*time.Second {
		t.Errorf("100 concurrent writes took %v", took)
	}
	h.barrier()
	h.verifyKernelWithOverlays()
}

var addsAFaultChain = regexp.MustCompile(`"add":\{"chain":\{"family":"inet","name":"mark_[1-9]`)

// An executor failure takes the whole batch back (plan §3.11 item 5): every writer waiting for it
// gets apply_failed, no overlay stays, no event announces one, and the kernel is as before.
func TestAFailedApplyRevertsTheWholeBatchAndEveryWriterGetsApplyFailed(t *testing.T) {
	h, g := gatedHarness(t, engine.Config{})
	keep := h.mustPut(admin, "target: {network: IoT}\nfault: {latency: 30ms, destination: {cidr: 192.0.2.0/24}}")
	events, cancel := h.e.Subscribe()
	defer cancel()
	// every transaction that has more than the one fault chain that is there fails
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && len(addsAFaultChain.FindAllString(stdin, -1)) > 1 {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	const n = 30
	bodies := make([]string, n)
	for i := range bodies {
		bodies[i] = globalLatency(i)
	}
	g.arm()
	first := make(chan writeOutcome, 1)
	go func() { r, err := h.put(alice, bodies[0]); first <- writeOutcome{r, err} }()
	g.waitEntered(t)
	rest := make(chan []writeOutcome, 1)
	go func() { rest <- h.putAll(bodies[1:]) }()
	h.waitAccepted(n + 1)
	g.release()
	all := append([]writeOutcome{<-first}, <-rest...)
	for i, o := range all {
		var af *engine.ErrApplyFailed
		if !errors.As(o.err, &af) {
			t.Errorf("write %d: got %v, want apply_failed", i, o.err)
		}
	}
	s := h.barrier()
	h.k.Fail = nil
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id {
		t.Fatalf("overlays after the failed batch: %+v", s.Overlays)
	}
	if s.LastError != "" {
		t.Errorf("the restore did not apply: %s", s.LastError)
	}
	if got := collect(events, engine.EventOverlayCreated); len(got) != 0 {
		t.Errorf("overlays that never became active announced themselves: %d events", len(got))
	}
	h.verifyKernelWithOverlays()
	// the next write is applied as usual
	if r := h.mustPut(bob, globalLatency(500)); !r.Created {
		t.Errorf("%+v", r)
	}
	h.verifyKernelWithOverlays()
}

// A write the compiler refuses (capacity_exceeded) is rejected up front and changes nothing; the
// valid writes that run at the same time are applied (plan §3.11 item 3).
func TestAnInvalidWriteDoesNotAffectConcurrentValidWrites(t *testing.T) {
	h, g := gatedHarness(t, engine.Config{ClassLimit: 12})
	// fill the interface's classes
	var kept []model.Overlay
	for i := 0; ; i++ {
		r, err := h.put(alice, fmt.Sprintf("target: {network: IoT}\nfault: {latency: %dms, destination: {cidr: 198.51.100.%d/32}}", 10+i, i))
		var cerr *engine.CompileError
		if errors.As(err, &cerr) {
			if cerr.Problems[0].Code != compiler.CodeCapacityExceeded {
				t.Fatalf("got %v", cerr.Problems[0])
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		kept = append(kept, r.Overlay)
		if i > 50 {
			t.Fatal("the classes never ran out")
		}
	}
	if len(kept) < 3 {
		t.Fatalf("only %d overlays fit", len(kept))
	}
	h.barrier()
	base := g.count()

	// at capacity: a replacement needs no new class and is valid however the writes interleave, a
	// new overlay needs one and never is
	var bodies []string
	for i := range kept {
		bodies = append(bodies, fmt.Sprintf("target: {network: IoT}\nfault: {latency: %dms, destination: {cidr: 198.51.100.%d/32}}", 100+i, i))
	}
	for i := 0; i < 20; i++ {
		bodies = append(bodies, fmt.Sprintf("target: {network: IoT}\nfault: {latency: 40ms, destination: {cidr: 203.0.113.%d/32}}", i))
	}
	g.arm()
	first := make(chan writeOutcome, 1)
	go func() { r, err := h.put(alice, bodies[0]); first <- writeOutcome{r, err} }()
	g.waitEntered(t)
	rest := make(chan []writeOutcome, 1)
	go func() { rest <- h.putAllAs(alice, bodies[1:]) }()
	// the refused writes answer at once, the valid ones wait for the held apply
	h.waitReplacements(len(kept), 100)
	g.release()
	all := append([]writeOutcome{<-first}, <-rest...)
	for i, o := range all {
		if i < len(kept) {
			if o.err != nil || o.res.Created || o.res.Overlay.Id != kept[i].Id {
				t.Errorf("replacement %d: %+v %v", i, o.res, o.err)
			}
			continue
		}
		var cerr *engine.CompileError
		if !errors.As(o.err, &cerr) || cerr.Problems[0].Code != compiler.CodeCapacityExceeded {
			t.Errorf("new overlay %d: got %v, want capacity_exceeded", i, o.err)
		}
	}
	s := h.barrier()
	if s.LastError != "" {
		t.Errorf("a refused write reached the kernel: %s", s.LastError)
	}
	if len(s.Overlays) != len(kept) {
		t.Errorf("%d overlays, want %d", len(s.Overlays), len(kept))
	}
	t.Logf("%d writes took %d applies", len(bodies), g.count()-base)
	h.verifyKernelWithOverlays()
}

func (h *harness) putAllAs(owner model.Owner, bodies []string) []writeOutcome {
	out := make([]writeOutcome, len(bodies))
	var wg sync.WaitGroup
	for i, body := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].res, out[i].err = h.put(owner, body)
		}()
	}
	wg.Wait()
	return out
}

// waitReplacements waits until the snapshot shows n overlays with a latency of at least min ms.
func (h *harness) waitReplacements(n, min int) {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got := 0
		for _, o := range h.e.Snapshot().Overlays {
			if o.Fault != nil && o.Fault.Latency != nil {
				if d, err := time.ParseDuration(*o.Fault.Latency); err == nil && d >= time.Duration(min)*time.Millisecond {
					got++
				}
			}
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("the state owner took %d of %d replacements", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Reads do not wait behind writes (plan §3.11): while an apply is held in the executor's writer, the
// counters can be read, and the snapshot is current.
func TestACounterReadCompletesWhileALongPlanRuns(t *testing.T) {
	h, g := gatedHarness(t, engine.Config{})
	h.mustPut(alice, iotLatency)
	g.arm()
	written := make(chan writeOutcome, 1)
	go func() {
		r, err := h.put(bob, "target: {network: Lab}\nfault: {latency: 20ms}")
		written <- writeOutcome{r, err}
	}()
	g.waitEntered(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	counters, err := h.e.ReadCounters(ctx)
	if err != nil {
		t.Fatalf("a counter read beside the held apply: %v", err)
	}
	if len(counters) == 0 {
		t.Error("no counters were read")
	}
	select {
	case o := <-written:
		t.Fatalf("the held write ended before it was released: %+v", o)
	default:
	}
	g.release()
	if o := <-written; o.err != nil {
		t.Fatal(o.err)
	}
	h.verifyKernelWithOverlays()
}

// Two writes of one key that are checked together are still a create and a replacement, in the order
// the state owner took them, with one overlay and one id as the result.
func TestTwoWritesOfOneKeyInOneBatchAreACreateAndAReplacement(t *testing.T) {
	h, g := gatedHarness(t, engine.Config{})
	events, cancel := h.e.Subscribe()
	defer cancel()
	g.arm()
	held := make(chan writeOutcome, 1)
	go func() { r, err := h.put(alice, globalLatency(1)); held <- writeOutcome{r, err} }()
	g.waitEntered(t)
	same := func(ms int) string {
		return fmt.Sprintf("target: {network: IoT}\nfault: {latency: %dms, destination: {cidr: 192.0.2.0/24}}", ms)
	}
	burst := make(chan []writeOutcome, 1)
	go func() { burst <- h.putAllAs(bob, []string{same(30), same(40), same(50)}) }()
	h.waitAccepted(2)
	g.release()
	out := <-burst
	if o := <-held; o.err != nil {
		t.Fatal(o.err)
	}
	created := 0
	for _, o := range out {
		if o.err != nil {
			t.Fatal(o.err)
		}
		if o.res.Created {
			created++
		}
		if o.res.Overlay.Id != out[0].res.Overlay.Id {
			t.Errorf("the writes made different overlays: %v and %v", o.res.Overlay.Id, out[0].res.Overlay.Id)
		}
	}
	if created != 1 {
		t.Errorf("%d of the three writes created the overlay, want 1", created)
	}
	s := h.barrier()
	if len(s.Overlays) != 2 {
		t.Errorf("%d overlays, want 2", len(s.Overlays))
	}
	if got := len(collect(events, engine.EventOverlayCreated)); got != 2 {
		t.Errorf("%d overlay_created events, want 2", got)
	}
	h.verifyKernelWithOverlays()
}
