package engine_test

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// The DNS-derived address sets of the engine (plan §2.6, §3.11, M20).

const dropBroker = `
target: {global: true}
rule: {action: drop, destination: {hostname: broker.example.com}}`

func hostHarness(t *testing.T) (*harness, compiler.DNSSet) {
	t.Helper()
	h := startedWithRevision(t)
	h.mustPut(alice, dropBroker)
	var set compiler.DNSSet
	for _, s := range h.barrier().DNSSets {
		set = s
	}
	if set.ID == "" {
		t.Fatal("the rule has no DNS-derived set")
	}
	return h, set
}

func res(set string, client string, ttl time.Duration, name string, addrs ...string) engine.HostResolution {
	r := engine.HostResolution{Set: set, Client: netip.MustParseAddr(client), Name: name, TTL: ttl}
	for _, a := range addrs {
		r.Addrs = append(r.Addrs, netip.MustParseAddr(a))
	}
	return r
}

func elementCommands(rec *kernelCommands) []string {
	var out []string
	for _, c := range rec.all() {
		if strings.HasPrefix(c.argv, "nft ") && strings.Contains(c.stdin, `"element"`) {
			out = append(out, c.stdin)
		}
	}
	return out
}

// The kernel's set holds what the proxy reported when the call returns, with the lifetime max(TTL, 10 min); the same
// report again writes nothing, one that is half through its lifetime is a refresh.
func TestAReportedResolutionIsInTheKernelsSetWhenTheCallReturnsAndIsNotWrittenTwice(t *testing.T) {
	h, set := hostHarness(t)
	rec := h.recordCommands()
	ctx := context.Background()
	got, err := h.e.RecordResolutions(ctx, []engine.HostResolution{res(set.ID, "10.10.0.5", 30*time.Second, "broker.example.com", "203.0.113.7", "203.0.113.8")})
	if err != nil || got.Written != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	els := h.k.SetElements(set.Name)
	if len(els) != 2 || !strings.Contains(els[0], `"timeout":600`) || !strings.Contains(els[0], "10.10.0.5") {
		t.Fatalf("the kernel's set: %v", els)
	}
	// a short TTL does not shorten it, a long one extends it
	if _, err := h.e.RecordResolutions(ctx, []engine.HostResolution{res(set.ID, "10.10.0.6", time.Hour, "broker.example.com", "203.0.113.9")}); err != nil {
		t.Fatal(err)
	}
	if els := h.k.SetElements(set.Name); len(els) != 3 || !strings.Contains(strings.Join(els, " "), `"timeout":3600`) {
		t.Fatalf("%v", els)
	}
	before := len(elementCommands(rec))

	// the same answer again: nothing is written
	got, err = h.e.RecordResolutions(ctx, []engine.HostResolution{res(set.ID, "10.10.0.5", 30*time.Second, "broker.example.com", "203.0.113.7", "203.0.113.8")})
	if err != nil || got.Written != 0 || got.Kept != 2 || len(elementCommands(rec)) != before {
		t.Fatalf("%+v %v, %d commands (was %d)", got, err, len(elementCommands(rec)), before)
	}
	// more than half of the lifetime is gone: the element is written again, as a refresh
	h.clk.Advance(6 * time.Minute)
	got, err = h.e.RecordResolutions(ctx, []engine.HostResolution{res(set.ID, "10.10.0.5", 30*time.Second, "broker.example.com", "203.0.113.7")})
	if err != nil || got.Written != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	last := elementCommands(rec)
	if cmd := last[len(last)-1]; !strings.Contains(cmd, `"delete"`) || !strings.Contains(cmd, `"timeout":600`) {
		t.Errorf("a refresh deletes the element and adds it again: %s", cmd)
	}
	if n := len(h.k.SetElements(set.Name)); n != 3 {
		t.Errorf("%d elements", n)
	}
}

// What no rule or fault needs is not written: a set that does not exist, a client the selector is not used for.
func TestAnUnknownSetAndAClientOutsideTheSourcesAreIgnored(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, `
target: {network: IoT}
rule: {action: drop, destination: {hostname: broker.example.com}}`)
	// the selector is used for the clients of the network IoT
	set := h.barrier().DNSSets
	if len(set) != 1 {
		t.Fatalf("%+v", set)
	}
	rec := h.recordCommands()
	got, err := h.e.RecordResolutions(context.Background(), []engine.HostResolution{
		res(set[0].ID, "10.20.0.77", time.Minute, "broker.example.com", "203.0.113.7"),
		res("dns_ffffffffff", "10.10.0.77", time.Minute, "x.example.com", "203.0.113.7"),
	})
	if err != nil || got.Written != 0 || got.Ignored != 2 || len(elementCommands(rec)) != 0 {
		t.Errorf("%+v %v %v", got, err, elementCommands(rec))
	}
}

// Plan §3.10, §3.11: reports that arrive while one is on its way go together as one request to the executor.
func TestConcurrentReportsAreOneRequestToTheExecutor(t *testing.T) {
	var mu sync.Mutex
	var inflight, requests int
	h := newHarnessRunner(t, func(k *kernelsim.Kernel) executor.Runner { return slowElements{k, &mu, &requests, &inflight} })
	h.start()
	h.mustApply(h.revision(nil))
	h.mustPut(alice, dropBroker)
	set := h.barrier().DNSSets[0]
	mu.Lock()
	requests = 0
	mu.Unlock()
	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := netip.AddrFrom4([4]byte{203, 0, 113, byte(10 + i)}).String()
			if _, err := h.e.RecordResolutions(context.Background(), []engine.HostResolution{res(set.ID, "10.10.0.5", time.Minute, "broker.example.com", addr)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(h.k.SetElements(set.Name)); n != 25 {
		t.Errorf("%d elements", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests >= 25 || requests == 0 {
		t.Errorf("%d requests for %d reports: they were not batched", requests, n)
	}
}

// slowElements delays the nft commands that write set elements, so the next reports gather behind them.
type slowElements struct {
	*kernelsim.Kernel
	mu       *sync.Mutex
	requests *int
	inflight *int
}

func (s slowElements) Run(ctx context.Context, c executor.Command) (executor.Result, error) {
	if c.Tool == executor.ToolNft && strings.Contains(c.Stdin, `"element"`) && strings.Contains(c.Stdin, `"timeout"`) {
		s.mu.Lock()
		*s.requests++
		s.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return s.Kernel.Run(ctx, c)
}

// A flow to an address the set holds keeps the element: it is refreshed when half of its lifetime is gone, as long as the
// connection table shows the flow; an element without a flow runs out.
func TestAFlowKeepsAnElementAliveAndWithoutOneItRunsOut(t *testing.T) {
	h, set := hostHarness(t)
	ctx := context.Background()
	if _, err := h.e.RecordResolutions(ctx, []engine.HostResolution{
		res(set.ID, "10.10.0.5", time.Second, "broker.example.com", "203.0.113.7", "203.0.113.8")}); err != nil {
		t.Fatal(err)
	}
	rec := h.recordCommands()
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", "10.10.0.5", 40001, "203.0.113.7", 8883)))
	// half of the lifetime later the flow is read: only its element is refreshed
	h.clk.Advance(6 * time.Minute)
	if err := h.e.ObserveNow(ctx); err != nil {
		t.Fatal(err)
	}
	cmds := elementCommands(rec)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "203.0.113.7") || strings.Contains(cmds[0], "203.0.113.8") || !strings.Contains(cmds[0], `"timeout":600`) {
		t.Fatalf("%v", cmds)
	}
	// and again later: the flow is still there, the element is refreshed again; another read at once is not a write
	h.clk.Advance(6 * time.Minute)
	if err := h.e.ObserveNow(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.e.ObserveNow(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(elementCommands(rec)); n != 2 {
		t.Errorf("%d writes", n)
	}
	// the element of the address without a flow ran out in the mirror: a new report writes it as a new element
	got, err := h.e.RecordResolutions(ctx, []engine.HostResolution{res(set.ID, "10.10.0.5", time.Second, "broker.example.com", "203.0.113.8")})
	if err != nil || got.Written != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	last := elementCommands(rec)
	if strings.Contains(last[len(last)-1], `"delete"`) {
		t.Errorf("an element that ran out is added, not refreshed: %s", last[len(last)-1])
	}
}

// The names the sets hold for an address are what a rule with a hostname destination matches: explain and the
// cut of existing connections ask for them.
func TestTheNamesAClientGotForAnAddressAreKnown(t *testing.T) {
	h, set := hostHarness(t)
	if _, err := h.e.RecordResolutions(context.Background(), []engine.HostResolution{res(set.ID, "10.10.0.5", time.Minute, "broker.example.com", "203.0.113.7")}); err != nil {
		t.Fatal(err)
	}
	if got := h.e.HostNames(netip.MustParseAddr("10.10.0.5"), netip.MustParseAddr("203.0.113.7")); len(got) != 1 || got[0] != "broker.example.com" {
		t.Errorf("%v", got)
	}
	if got := h.e.HostNames(netip.MustParseAddr("10.10.0.6"), netip.MustParseAddr("203.0.113.7")); len(got) != 0 {
		t.Errorf("another client: %v", got)
	}
}

// Plan §3.11: a set update during a long plan is applied without waiting for it. The plan is held in the kernel;
// the update goes into the set beside it, and the set is still filled when the plan has ended (an apply never
// flushes it, S11).
func TestASetUpdateDuringALongPlanIsAppliedWithoutWaitingForIt(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var hold atomic.Bool
	h := newHarnessRunner(t, func(k *kernelsim.Kernel) executor.Runner {
		return holdPlans{Kernel: k, started: started, release: release, once: &once, hold: &hold}
	})
	h.start()
	h.mustApply(h.revision(nil))
	h.mustPut(alice, dropBroker)
	set := h.barrier().DNSSets[0]
	if _, err := h.e.RecordResolutions(context.Background(), []engine.HostResolution{res(set.ID, "10.10.0.5", time.Minute, "broker.example.com", "203.0.113.7")}); err != nil {
		t.Fatal(err)
	}
	hold.Store(true)
	// a plan that the kernel holds: another overlay's apply
	done := make(chan error, 1)
	go func() {
		_, err := h.put(bob, "target: {network: Lab}\nfault: {latency: 30ms}")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the plan never reached the kernel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	begin := time.Now()
	got, err := h.e.RecordResolutions(ctx, []engine.HostResolution{res(set.ID, "10.10.0.5", time.Minute, "broker.example.com", "203.0.113.8", "203.0.113.9")})
	if err != nil || got.Written != 2 {
		t.Fatalf("an update beside the plan: %+v %v after %v", got, err, time.Since(begin))
	}
	select {
	case err := <-done:
		t.Fatalf("the plan ended before it was released: %v", err)
	default:
	}
	if n := len(h.k.SetElements(set.Name)); n != 3 {
		t.Errorf("%d elements while the plan is held", n)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(h.k.SetElements(set.Name)); n != 3 {
		t.Errorf("%d elements after the plan: an apply flushed the set", n)
	}
}

// holdPlans holds the first nft transaction that creates the table (a full apply) while hold is set.
type holdPlans struct {
	*kernelsim.Kernel
	started, release chan struct{}
	once             *sync.Once
	hold             *atomic.Bool
}

func (p holdPlans) Run(ctx context.Context, c executor.Command) (executor.Result, error) {
	if p.hold.Load() && c.Tool == executor.ToolNft && strings.Contains(c.Stdin, `"table":{"family"`) {
		p.once.Do(func() { close(p.started) })
		<-p.release
	}
	return p.Kernel.Run(ctx, c)
}
