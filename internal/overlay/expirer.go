package overlay

import (
	"sync"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// Expirer arms a timer on the injected clock for the store's next deadline. When it fires, the
// callback runs (in its own goroutine on the real clock, synchronously inside Advance on the fake
// one); it must hand the work to the state owner, which calls Store.Expire, publishes the changes
// and calls Rearm. Rearm is also needed after every write, since a new overlay can have the
// earliest deadline.
type Expirer struct {
	store *Store
	clk   clock.Clock
	fire  func()

	mu    sync.Mutex
	timer clock.Timer
	done  bool
}

// NewExpirer returns an Expirer that is not armed yet; call Rearm.
func NewExpirer(s *Store, c clock.Clock, fire func()) *Expirer {
	return &Expirer{store: s, clk: c, fire: fire}
}

// Rearm replaces the pending timer by one for the store's current next deadline, or by none when
// no overlay has a deadline. A timer that fires although it was replaced is harmless: Expire finds
// nothing due.
func (e *Expirer) Rearm() {
	next, ok := e.store.NextDeadline()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	if !ok || e.done {
		return
	}
	d := next - e.clk.Monotonic()
	if d < 0 {
		d = 0
	}
	e.timer = e.clk.AfterFunc(d, e.fire)
}

// Stop disarms the timer for good.
func (e *Expirer) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.done = true
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
}
