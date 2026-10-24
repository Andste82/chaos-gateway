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
