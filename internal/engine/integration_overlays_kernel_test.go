//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M8a acceptance tests on a real kernel (plan M8a "Tests"): the overlays of the engine reach the
// nftables maps and the tc tree, the counters of a fault are the kernel's, the burst of 200 writes
// is applied in a few transactions and ends in the compile of the final snapshot, and a counter
// read does not wait for a transaction that is in flight. The tests with a simulated kernel are in
// coalesce_test.go and overlays_test.go; they cannot tell what the real nft and tc accept.

// kernelGate wraps the executor's runner in front of the real kernel. It counts the nft
// transactions (every apply is one) and, while armed, holds them in the executor's single writer;
// reads (nft list, tc show, ip route get) pass at any time.
type kernelGate struct {
	inner executor.Runner

	mu      sync.Mutex
	writes  int
	hold    chan struct{}
	entered chan struct{}
	// fail, when set, makes the nft transactions it names fail the way the kernel's nft would,
	// without reaching the kernel
	fail func(c executor.Command) bool
}

func (g *kernelGate) wrap(inner executor.Runner) executor.Runner {
	g.inner = inner
	return g
}

func (g *kernelGate) Run(ctx context.Context, c executor.Command) (executor.Result, error) {
	if c.Tool == executor.ToolNft && c.Stdin != "" {
		g.mu.Lock()
		g.writes++
		hold, entered, fail := g.hold, g.entered, g.fail
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
		if fail != nil && fail(c) {
			return executor.Result{Exit: 1, Stderr: "Error: injected\n"}, nil
		}
	}
	return g.inner.Run(ctx, c)
}

// arm makes the next nft transaction wait until release is called.
func (g *kernelGate) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hold, g.entered = make(chan struct{}), make(chan struct{}, 1)
}

func (g *kernelGate) waitEntered(t *testing.T) {
	t.Helper()
	g.mu.Lock()
	entered := g.entered
	g.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(2 * time.Minute):
		t.Fatal("the executor never reached the held transaction")
	}
}

func (g *kernelGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hold != nil {
		close(g.hold)
		g.hold = nil
	}
}

func (g *kernelGate) setFail(f func(c executor.Command) bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fail = f
}

func (g *kernelGate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.writes
}

// target compiles the engine's snapshot with its overlays, the way the apply loop does, from the
// host the kernel has now.
func (r *real) target() *compiler.Target {
	r.t.Helper()
	s := r.e.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	host, err := apply.ReadHost(ctx, apply.Local{E: r.ex}, r.top.GW.Name)
	if err != nil {
		r.t.Fatal(err)
	}
	tg := compiler.Compile(compiler.Input{Config: s.Config, Host: host, Overlays: s.Overlays, FaultIDs: s.FaultIDs,
		Identity: &s.Identity, Generation: compiler.Generation{Revision: s.Applied.Revision, Seq: s.Applied.Generation}})
	if tg.HasErrors() {
		r.t.Fatalf("the snapshot does not compile: %+v", tg.Problems)
	}
	return tg
}

// verifyKernel checks that the real kernel is the compile of the final snapshot.
func (r *real) verifyKernel() *compiler.Target {
	r.t.Helper()
	tg := r.target()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := apply.ReadState(ctx, apply.Local{E: r.ex}, r.top.GW.Name, apply.WantOf(tg))
	if err != nil {
		r.t.Fatal(err)
	}
	if mm := apply.Verify(tg, st); len(mm) != 0 {
		r.t.Fatalf("the kernel does not match the compile of the snapshot: %v", mm)
	}
	return tg
}

// installTC puts the compiled tc tree on the interfaces it names, as the fault engine will (M8b).
func (r *real) installTC(tg *compiler.Target) {
	r.t.Helper()
	if tg.TC == nil {
		r.t.Fatal("the target has no tc tree")
	}
	for _, dev := range tg.TC.Devs {
		if _, err := r.ex.Do(context.Background(), &executor.TC{Target: executor.Target{NS: r.top.GW.Name}, Entries: tg.TC.Entries(dev, true)}); err != nil {
			r.t.Fatalf("install the tc tree on %s: %v\n%s", dev, err, strings.Join(tg.TC.Lines(dev), "\n"))
		}
	}
}

// classTotal sums the packets of a class over the interfaces the tree is on (the packets of one
// direction leave through one of them).
func (r *real) classTotal(tg *compiler.Target, c compiler.TCClass) int64 {
	r.t.Helper()
	var n int64
	for _, dev := range tg.TC.Devs {
		out := r.top.GW.Must("tc", "-j", "-s", "class", "show", "dev", dev)
		var entries []map[string]any
		if err := json.Unmarshal([]byte(out), &entries); err != nil {
			r.t.Fatalf("parse tc class show dev %s: %v\n%s", dev, err, out)
		}
		for _, e := range entries {
			if h, _ := e["handle"].(string); h == c.ClassID() {
				if p, ok := tcPacketCount(e); ok {
					n += p
				}
			}
		}
	}
	return n
}

func (r *real) counters() map[string]engine.CounterValue {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cs, err := r.e.ReadCounters(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	return cs
}

func (r *real) put(owner model.Owner, body string) (engine.OverlayResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return r.e.PutOverlay(ctx, engine.OverlayWrite{Owner: owner, Request: overlayRequest(r.t, body)})
}

func (r *real) mustPut(owner model.Owner, body string) engine.OverlayResult {
	r.t.Helper()
	res, err := r.put(owner, body)
	if err != nil {
		r.t.Fatalf("%s: %v", body, err)
	}
	return res
}

func faultOfOverlay(t *testing.T, s *engine.Snapshot, o model.Overlay) compiler.Fault {
	t.Helper()
	for _, f := range s.Faults {
		if f.Source == o.Id.String() {
			return f
		}
	}
	t.Fatalf("overlay %s has no fault in %+v", o.Id, s.Faults)
	return compiler.Fault{}
}

func classOfFault(t *testing.T, tg *compiler.Target, f compiler.Fault, dir compiler.Direction) compiler.TCClass {
	t.Helper()
	for _, c := range tg.TC.Classes {
		if c.ID == f.ID && c.Dir == dir {
			return c
		}
	}
	t.Fatalf("no tc class for fault %d %s", f.ID, dir)
	return compiler.TCClass{}
}

// An overlay written through the engine is a complete setup in the real kernel: the mark chain, the
// classification map elements, the named counters and the tc class of each direction. Traffic of
// the overlay's scope is classified into it, traffic of another scope and other destinations of the
// same scope are not, and a replacement keeps the id and with it the counters.
func TestAnOverlayShapesTheTrafficOfItsScopeOnARealKernel(t *testing.T) {
	r := newReal(t, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	iot := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 2ms, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	lab := r.mustPut(admin, "target: {network: Lab}\nfault: {latency: 3ms, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	snap := r.e.Snapshot()
	fi, fl := faultOfOverlay(t, snap, iot.Overlay), faultOfOverlay(t, snap, lab.Overlay)
	if fi.ID == fl.ID || fi.CounterUp == fl.CounterUp {
		t.Fatalf("the two faults share an id or a counter: %+v %+v", fi, fl)
	}

	tg := r.verifyKernel()
	nft := r.nft()
	for _, f := range []compiler.Fault{fi, fl} {
		for _, want := range []string{"chain " + compiler.MarkChainName(f.ID), "counter " + f.CounterUp, "counter " + f.CounterDown} {
			if !strings.Contains(nft, want) {
				t.Errorf("the kernel has no %q:\n%s", want, nft)
			}
		}
	}
	r.installTC(tg)
	iotUp, iotDown := classOfFault(t, tg, fi, compiler.Upload), classOfFault(t, tg, fi, compiler.Download)
	labUp, labDown := classOfFault(t, tg, fl, compiler.Upload), classOfFault(t, tg, fl, compiler.Download)

	if res := testbed.MustPing(t, r.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 {
		t.Fatalf("A cannot reach the server:\n%s", nft)
	}
	cs := r.counters()
	if cs[fi.CounterUp].Packets < 3 || cs[fi.CounterDown].Packets < 3 {
		t.Errorf("the counters of the IoT fault: up %+v, down %+v", cs[fi.CounterUp], cs[fi.CounterDown])
	}
	if cs[fl.CounterUp].Packets != 0 || cs[fl.CounterDown].Packets != 0 {
		t.Errorf("A's traffic was counted in the Lab fault: %+v %+v", cs[fl.CounterUp], cs[fl.CounterDown])
	}
	if n := r.classTotal(tg, iotUp); n < 3 {
		t.Errorf("the tc class of the IoT fault's upload counted %d packets", n)
	}
	if n := r.classTotal(tg, iotDown); n < 3 {
		t.Errorf("the tc class of the IoT fault's download counted %d packets", n)
	}
	if n := r.classTotal(tg, labUp) + r.classTotal(tg, labDown); n != 0 {
		t.Errorf("the Lab fault's classes counted %d packets of the IoT network", n)
	}

	// another destination of the same network: the fault does not name it
	before := r.counters()[fi.CounterUp].Packets
	if res := testbed.MustPing(t, r.top.A, testbed.ServerAddr2, 3, 200*time.Millisecond); res.Loss() != 0 {
		t.Fatalf("A cannot reach the second server")
	}
	if after := r.counters()[fi.CounterUp].Packets; after != before {
		t.Errorf("traffic to a destination the fault does not name was classified: %d -> %d", before, after)
	}

	// the other network's device reaches the server through its own fault
	if res := testbed.MustPing(t, r.top.C, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 {
		t.Fatalf("C cannot reach the server")
	}
	cs = r.counters()
	if cs[fl.CounterUp].Packets < 3 || cs[fl.CounterDown].Packets < 3 || cs[fi.CounterUp].Packets != before {
		t.Errorf("after C's pings: Lab up %+v down %+v, IoT up %+v (was %d)", cs[fl.CounterUp], cs[fl.CounterDown], cs[fi.CounterUp], before)
	}
	if n := r.classTotal(tg, labUp); n < 3 {
		t.Errorf("the Lab fault's upload class counted %d packets", n)
	}

	// a replacement keeps the fault's id, its counters and its chain; the kernel is the compile of
	// the new snapshot
	again := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 5ms, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	if again.Created || again.Overlay.Id != iot.Overlay.Id {
		t.Fatalf("%+v", again)
	}
	if f := faultOfOverlay(t, r.e.Snapshot(), again.Overlay); f.ID != fi.ID || f.CounterUp != fi.CounterUp {
		t.Errorf("the replacement renumbered the fault: %+v, was %+v", f, fi)
	}
	if got := r.counters()[fi.CounterUp].Packets; got != before {
		t.Errorf("a replacement changed the packet counter: %d, was %d", got, before)
	}
	r.verifyKernel()
}

// plan M8a: 200 concurrent overlay writes need far fewer applies than writes, every writer gets a
// generation that contains its change, and the final kernel state equals the compile of the final
// snapshot. The first write's apply is held in the executor so that the others arrive meanwhile;
// the transaction is a real nft one, with 200 mark chains in it.
func TestTwoHundredConcurrentOverlayWritesOnARealKernelEndInTheCompileOfTheFinalSnapshot(t *testing.T) {
	gate := &kernelGate{}
	r := newRealRunner(t, gate.wrap, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	events, cancel := r.e.Subscribe()
	defer cancel()
	base := gate.count()
	const n = 200
	body := func(i int) string {
		return fmt.Sprintf("target: {global: true}\nfault: {latency: %dms, destination: {cidr: 198.51.%d.%d/32}}", 10+i%90, i/250, i%250)
	}
	owners := []model.Owner{alice, bob}

	type outcome struct {
		res engine.OverlayResult
		err error
	}
	out := make([]outcome, n)
	var wg sync.WaitGroup
	write := func(i int) {
		defer wg.Done()
		out[i].res, out[i].err = r.put(owners[i%2], body(i))
	}
	gate.arm()
	wg.Add(1)
	go write(0)
	gate.waitEntered(t)
	for i := 1; i < n; i++ {
		wg.Add(1)
		go write(i)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for len(r.e.Snapshot().Overlays) < n {
		if time.Now().After(deadline) {
			t.Fatalf("the state owner accepted %d of %d overlays", len(r.e.Snapshot().Overlays), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	gate.release()
	wg.Wait()

	final, err := r.e.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	applies := gate.count() - base
	t.Logf("%d writes took %d nft transactions", n, applies)
	if applies > 3 {
		t.Errorf("%d writes needed %d applies, want at most 3", n, applies)
	}
	applied := appliedGenerations(collect(events, engine.EventApplied))
	for i, o := range out {
		if o.err != nil {
			t.Fatalf("write %d: %v", i, o.err)
		}
		if !o.res.Created || !applied[o.res.Generation] || o.res.Generation < uint64(o.res.Overlay.Generation) || o.res.Generation > final.Applied.Generation {
			t.Errorf("write %d: created %v, answered generation %d for a change of generation %d (applied %v, final %d)",
				i, o.res.Created, o.res.Generation, o.res.Overlay.Generation, applied, final.Applied.Generation)
		}
	}
	if len(final.Overlays) != n || len(final.Faults) != n {
		t.Fatalf("%d overlays and %d faults, want %d", len(final.Overlays), len(final.Faults), n)
	}

	tg := r.verifyKernel()
	if got := strings.Count(r.nft(), "chain mark_"); got != n {
		t.Errorf("the kernel has %d mark chains, want %d", got, n)
	}
	// the tc tree of the final snapshot, 200 faults with a class per direction, is accepted too
	r.installTC(tg)
}

// plan M8a: a counter read completes while a long plan runs. The writer holds a real nft
// transaction (an overlay is being applied); the reads that the API and explain make, the counters
// of the kernel and the route lookup, return the kernel's real values without waiting for it.
func TestACounterReadCompletesWhileAPlanIsHeldInTheWriterOnARealKernel(t *testing.T) {
	gate := &kernelGate{}
	r := newRealRunner(t, gate.wrap, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	first := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 2ms, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	f := faultOfOverlay(t, r.e.Snapshot(), first.Overlay)
	if res := testbed.MustPing(t, r.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 {
		t.Fatal("A cannot reach the server")
	}

	gate.arm()
	done := make(chan error, 1)
	go func() {
		_, err := r.put(admin, "target: {network: Lab}\nfault: {loss: 1%}")
		done <- err
	}()
	gate.waitEntered(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	cs, err := r.e.ReadCounters(ctx)
	if err != nil {
		t.Fatalf("the counter read failed while a plan was running: %v", err)
	}
	if cs[f.CounterUp].Packets < 3 {
		t.Errorf("the read does not show the kernel's counters: %+v", cs[f.CounterUp])
	}
	if _, err := r.e.RouteFor(ctx, testbed.ServerAddr, testbed.ClientAAddr, ""); err != nil {
		t.Errorf("the route read failed while a plan was running: %v", err)
	}
	t.Logf("the reads took %v beside the held transaction", time.Since(start))
	select {
	case err := <-done:
		t.Fatalf("the write finished (%v) before the transaction was released: the read did not run beside it", err)
	default:
	}
	gate.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Minute):
		t.Fatal("the held write never finished")
	}
	r.verifyKernel()
}

// plan M8a, on a real kernel: an injected executor failure reverts the whole batch, every writer
// waiting for it gets apply_failed, the overlay that was there stays, and the kernel is the compile
// of the snapshot again. The transactions that fail are the ones that add more than the one fault
// chain of the overlay that is kept; the restore of the old state is a transaction the real kernel
// takes.
func TestAFailedBatchIsRevertedOnARealKernelAndEveryWriterGetsApplyFailed(t *testing.T) {
	gate := &kernelGate{}
	r := newRealRunner(t, gate.wrap, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	keep := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 30ms, destination: {cidr: 192.0.2.0/24}}")
	events, cancel := r.e.Subscribe()
	defer cancel()
	gate.setFail(func(c executor.Command) bool { return len(addsAFaultChain.FindAllString(c.Stdin, -1)) > 1 })

	const n = 20
	errs := make([]error, n)
	var wg sync.WaitGroup
	write := func(i int) {
		defer wg.Done()
		_, errs[i] = r.put(alice, fmt.Sprintf("target: {global: true}\nfault: {latency: %dms, destination: {cidr: 198.51.100.%d/32}}", 10+i, i))
	}
	gate.arm()
	wg.Add(1)
	go write(0)
	gate.waitEntered(t)
	for i := 1; i < n; i++ {
		wg.Add(1)
		go write(i)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for len(r.e.Snapshot().Overlays) < n+1 {
		if time.Now().After(deadline) {
			t.Fatalf("the state owner accepted %d of %d overlays", len(r.e.Snapshot().Overlays), n+1)
		}
		time.Sleep(5 * time.Millisecond)
	}
	gate.release()
	wg.Wait()
	for i, err := range errs {
		var af *engine.ErrApplyFailed
		if !errors.As(err, &af) {
			t.Errorf("write %d: got %v, want apply_failed", i, err)
		}
	}
	gate.setFail(nil)
	s, err := r.e.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id || s.LastError != "" {
		t.Fatalf("after the failed batch: %+v, last error %q", s.Overlays, s.LastError)
	}
	if got := collect(events, engine.EventOverlayCreated); len(got) != 0 {
		t.Errorf("overlays that never became active announced themselves: %d events", len(got))
	}
	r.verifyKernel()
	if got := strings.Count(r.nft(), "chain mark_"); got != 1 {
		t.Errorf("the kernel has %d mark chains after the revert, want the one of the kept overlay", got)
	}
	// the next write is applied as usual
	if res := r.mustPut(bob, "target: {global: true}\nfault: {loss: 2%}"); !res.Created {
		t.Errorf("%+v", res)
	}
	r.verifyKernel()
}
