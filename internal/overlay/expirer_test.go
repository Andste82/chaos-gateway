package overlay

import (
	"sync"
	"testing"
	"time"
)

// reaper plays the state owner's part: on every timer it expires what is due, "publishes" the
// events and rearms.
type reaper struct {
	mu     sync.Mutex
	events []string
	ex     *Expirer
	s      *Store
}

func newReaper(f *fixture) *reaper {
	r := &reaper{s: f.store}
	r.ex = NewExpirer(f.store, f.clk, func() {
		changes := r.s.Expire()
		r.mu.Lock()
		for _, c := range changes {
			r.events = append(r.events, c.Event()+":"+c.Reason)
		}
		r.mu.Unlock()
		r.ex.Rearm()
	})
	return r
}

func (r *reaper) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func TestTheExpirerRemovesOverlaysAtTheirDeadlinesAndEmitsEvents(t *testing.T) {
	f := newFixture(t)
	r := newReaper(f)
	defer r.ex.Stop()

	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 1ms}, ttl: 30s}`)
	r.ex.Rearm()
	f.put(admin, `{target: {network: IoT}, fault: {latency: 1ms}, lease: 10s}`) // earlier than the pending timer
	r.ex.Rearm()

	f.clk.Advance(9 * time.Second)
	if got := r.seen(); len(got) != 0 {
		t.Fatalf("events too early: %v", got)
	}
	f.clk.Advance(time.Second)
	if got := r.seen(); len(got) != 1 || got[0] != "overlay_expired:lease" {
		t.Fatalf("events = %v", got)
	}
	f.clk.Advance(20 * time.Second)
	if got := r.seen(); len(got) != 2 || got[1] != "overlay_expired:ttl" {
		t.Fatalf("events = %v", got)
	}
	if f.store.Len() != 0 {
		t.Fatalf("%d overlays left", f.store.Len())
	}
	f.clk.Advance(time.Hour)
	if len(r.seen()) != 2 {
		t.Fatal("the reaper fires without anything due")
	}
}

func TestARenewedLeaseMovesTheTimer(t *testing.T) {
	f := newFixture(t)
	r := newReaper(f)
	defer r.ex.Stop()
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 1ms}, lease: 10s}`).Overlay
	r.ex.Rearm()
	for i := 0; i < 3; i++ {
		f.clk.Advance(8 * time.Second)
		if _, err := f.store.Renew(ov.Id); err != nil {
			t.Fatal(err)
		}
		r.ex.Rearm()
	}
	if got := r.seen(); len(got) != 0 {
		t.Fatalf("events = %v", got)
	}
	f.clk.Advance(10 * time.Second)
	if got := r.seen(); len(got) != 1 {
		t.Fatalf("events = %v", got)
	}
}

func TestADeadlineThatHasPassedFiresAtOnce(t *testing.T) {
	f := newFixture(t)
	r := newReaper(f)
	defer r.ex.Stop()
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 1ms}, ttl: 5s}`)
	f.clk.Advance(time.Minute) // nobody rearmed
	r.ex.Rearm()
	// a timer for a deadline in the past runs on its own goroutine, at once
	deadline := time.Now().Add(2 * time.Second)
	for len(r.seen()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := r.seen(); len(got) != 1 {
		t.Fatalf("events = %v", got)
	}
}

func TestAStoppedExpirerStaysQuiet(t *testing.T) {
	f := newFixture(t)
	r := newReaper(f)
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 1ms}, ttl: 5s}`)
	r.ex.Rearm()
	r.ex.Stop()
	r.ex.Rearm()
	f.clk.Advance(time.Minute)
	if got := r.seen(); len(got) != 0 {
		t.Fatalf("events = %v", got)
	}
}
