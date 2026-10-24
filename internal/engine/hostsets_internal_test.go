package engine

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// recordingExec keeps the DNS-set updates it gets (none reaches a kernel) and passes everything else on to the
// executor over the simulated kernel.
type recordingExec struct {
	inner   apply.Exec
	mu      sync.Mutex
	updates []executor.DNSSetUpdate
	// block, when set, holds every DNS-set request until it is closed; entered is told that one arrived
	block   chan struct{}
	entered chan struct{}
}

func (r *recordingExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	var dns bool
	for _, op := range ops {
		if d, ok := op.(*executor.NftDNSElements); ok {
			dns = true
			r.mu.Lock()
			r.updates = append(r.updates, d.Updates...)
			r.mu.Unlock()
		}
	}
	if dns {
		if r.block != nil {
			r.entered <- struct{}{}
			<-r.block
		}
		return executor.Outcome{}, nil
	}
	return r.inner.Do(ctx, ops...)
}

func (r *recordingExec) sets() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, u := range r.updates {
		out = append(out, u.Set)
	}
	return out
}

func hostEngine(t *testing.T) (*Engine, *recordingExec, *clock.Fake) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	k := kernelsim.New()
	k.AddLink("lan0", "02:00:00:00:00:01", "veth", true)
	k.AddLink("wan0", "02:00:00:00:02:01", "veth", true)
	k.SetAddr("wan0", "203.0.113.1/24")
	real, err := executor.New(k)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(real.Close)
	ex := &recordingExec{inner: apply.Local{E: real}}
	clk := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	e, err := New(Config{Store: st, Exec: ex, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, ex, clk
}

func targetWithSet(name string) *compiler.Target {
	return &compiler.Target{DNSSets: []compiler.DNSSet{{ID: "dns_0123456789", Name: name, Pattern: "broker.example.com",
		Sources: []netip.Prefix{netip.MustParsePrefix("10.10.0.0/24")}}}}
}

func report(e *Engine, addr string) error {
	_, err := e.RecordResolutions(context.Background(), []HostResolution{{Set: "dns_0123456789", Client: netip.MustParseAddr("10.10.0.5"),
		Name: "broker.example.com", TTL: time.Minute, Addrs: []netip.Addr{netip.MustParseAddr(addr)}}})
	return err
}

// Plan §3.11: updates for a set that the running plan replaces wait for the plan and then go into the new set;
// updates for another set, or while no plan replaces anything, do not wait.
func TestAnUpdateForASetThePlanReplacesWaitsAndLandsInTheNewSet(t *testing.T) {
	e, ex, _ := hostEngine(t)
	e.hosts.applied(targetWithSet("dns_0123456789_aaaaaa"), nil)
	if err := report(e, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if got := ex.sets(); len(got) != 1 || got[0] != "dns_0123456789_aaaaaa" {
		t.Fatalf("%v", got)
	}

	// the plan about to run replaces the set: its definition changed, so its name did
	next := targetWithSet("dns_0123456789_bbbbbb")
	events, cancel := e.Subscribe()
	defer cancel()
	end := e.hosts.begin(next)
	done := make(chan error, 1)
	go func() { done <- report(e, "203.0.113.8") }()
	select {
	case err := <-done:
		t.Fatalf("an update for a set the plan replaces did not wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := ex.sets(); len(got) != 1 {
		t.Fatalf("written while the plan runs: %v", got)
	}
	end(next, nil) // the plan is through
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := ex.sets(); len(got) != 2 || got[1] != "dns_0123456789_bbbbbb" {
		t.Errorf("the update went into %v, want the new set", got)
	}
	// the new set starts empty: what the mirror held is gone, and the event says so
	select {
	case ev := <-events:
		if ev.Type != EventDNSSetEpoch || ev.Data["name"] != "dns_0123456789_bbbbbb" || ev.Data["previous"] != "dns_0123456789_aaaaaa" {
			t.Errorf("%+v", ev)
		}
	case <-time.After(time.Second):
		t.Error("no dns_set_epoch event")
	}
	// an address that was in the old set is written again: the mirror does not claim the new set has it
	if err := report(e, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if got := ex.sets(); len(got) != 3 {
		t.Errorf("%v", got)
	}
	// a plan that replaces nothing holds nothing back
	end2 := e.hosts.begin(next)
	if err := report(e, "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	end2(next, nil)
}

// A plan that failed leaves the sets as they were: the held updates go on into the set the kernel still has.
func TestAFailedPlanHoldsNothingBackAndKeepsTheOldSet(t *testing.T) {
	e, ex, _ := hostEngine(t)
	e.hosts.applied(targetWithSet("dns_0123456789_aaaaaa"), nil)
	end := e.hosts.begin(targetWithSet("dns_0123456789_bbbbbb"))
	done := make(chan error, 1)
	go func() { done <- report(e, "203.0.113.8") }()
	time.Sleep(50 * time.Millisecond)
	end(nil, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := ex.sets(); len(got) != 1 || got[0] != "dns_0123456789_aaaaaa" {
		t.Errorf("%v", got)
	}
}

// After a restart the kernel still holds the elements of a set that stays: they are taken from the listing,
// with the time they have left, so a flow keeps refreshing them and a report is not written again.
func TestTheElementsTheKernelHoldsAreAdoptedAfterARestart(t *testing.T) {
	e, ex, _ := hostEngine(t)
	rs, err := linux.ParseNft([]byte(`{"nftables":[{"set":{"family":"inet","table":"chaosgw","name":"dns_0123456789_aaaaaa","type":["ipv4_addr","ipv4_addr"],"flags":["timeout"],
		"elem":[{"elem":{"val":{"concat":["10.10.0.5","203.0.113.7"]},"timeout":600,"expires":590}}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	e.hosts.applied(targetWithSet("dns_0123456789_aaaaaa"), rs)
	if err := report(e, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if got := ex.sets(); len(got) != 0 {
		t.Errorf("an element the kernel holds was written again: %v", got)
	}
	if got := e.HostNames(netip.MustParseAddr("10.10.0.5"), netip.MustParseAddr("203.0.113.7")); len(got) != 1 || got[0] != "broker.example.com" {
		t.Errorf("%v", got)
	}
}

// A request that was on its way while a plan replaced the set went into the set that is gone: the mirror must not
// claim the new set holds the element, or the device's address would stay out of it for half a lifetime.
func TestARequestThatWasOnItsWayWhileAPlanReplacedTheSetIsNotRememberedAsWritten(t *testing.T) {
	e, ex, _ := hostEngine(t)
	e.hosts.applied(targetWithSet("dns_0123456789_aaaaaa"), nil)
	ex.block, ex.entered = make(chan struct{}), make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() { done <- report(e, "203.0.113.8") }()
	<-ex.entered
	next := targetWithSet("dns_0123456789_bbbbbb")
	end := e.hosts.begin(next)
	end(next, nil) // the plan is through while the request has not returned
	close(ex.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ex.block = nil
	if err := report(e, "203.0.113.8"); err != nil {
		t.Fatal(err)
	}
	if got := ex.sets(); len(got) != 2 || got[0] != "dns_0123456789_aaaaaa" || got[1] != "dns_0123456789_bbbbbb" {
		t.Errorf("the address is not in the new set: %v", got)
	}
}

// One operation takes at most MaxDNSElements elements and MaxDNSSets sets: a larger batch goes in several requests
// that together hold every element once.
func TestALargeBatchOfElementsIsSplitIntoRequestsTheExecutorTakes(t *testing.T) {
	mk := func(n int) []executor.DNSElement {
		els := make([]executor.DNSElement, n)
		for i := range els {
			els[i] = executor.DNSElement{Client: "10.10.0.5", Addr: netip.AddrFrom4([4]byte{203, 0, byte(i >> 8), byte(i)}).String(), TimeoutSeconds: 600}
		}
		return els
	}
	var u executor.NftDNSElements
	u.Updates = []executor.DNSSetUpdate{
		{Set: "dns_0000000001_aaaaaa", Elements: mk(executor.MaxDNSElements - 3)},
		{Set: "dns_0000000002_aaaaaa", Elements: mk(10)},
	}
	parts := splitDNSUpdates(u)
	if len(parts) != 2 {
		t.Fatalf("%d requests", len(parts))
	}
	count := map[string]int{}
	for _, p := range parts {
		total := 0
		for _, up := range p.Updates {
			total += len(up.Elements)
			count[up.Set] += len(up.Elements)
		}
		if total > executor.MaxDNSElements || len(p.Updates) > executor.MaxDNSSets {
			t.Errorf("a request with %d elements in %d sets", total, len(p.Updates))
		}
	}
	if count["dns_0000000001_aaaaaa"] != executor.MaxDNSElements-3 || count["dns_0000000002_aaaaaa"] != 10 {
		t.Errorf("%v", count)
	}
	if got := splitDNSUpdates(executor.NftDNSElements{}); len(got) != 0 {
		t.Errorf("%v", got)
	}
}
