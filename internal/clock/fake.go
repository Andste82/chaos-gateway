package clock

import (
	"container/heap"
	"sync"
	"time"
)

// Fake is a clock that only moves when the test advances it. All timers are driven by Advance,
// in order of their deadlines; nothing depends on real time.
//
// The wall clock and the monotonic clock are separate on purpose: JumpWall moves Now() like an
// NTP step would and fires no timer, so tests can show that TTLs and schedules ignore it.
type Fake struct {
	mu      sync.Mutex
	mono    time.Duration
	wall    time.Time // wall time at mono == 0
	timers  timerHeap
	seq     uint64
	waiters []waiter
}

type waiter struct {
	n  int
	ch chan struct{}
}

// NewFake returns a fake clock whose wall time starts at start and whose monotonic time at 0.
func NewFake(start time.Time) *Fake { return &Fake{wall: start} }

func (f *Fake) Now() time.Time           { f.mu.Lock(); defer f.mu.Unlock(); return f.nowLocked() }
func (f *Fake) Monotonic() time.Duration { f.mu.Lock(); defer f.mu.Unlock(); return f.mono }

func (f *Fake) nowLocked() time.Time { return f.wall.Add(f.mono) }

// JumpWall moves the wall clock by d without touching the monotonic clock or any timer.
func (f *Fake) JumpWall(d time.Duration) {
	f.mu.Lock()
	f.wall = f.wall.Add(d)
	f.mu.Unlock()
}

// Advance moves both clocks forward by d and fires every timer that falls due, in deadline
// order. Each timer sees the clock at its own deadline while it fires; AfterFunc callbacks run
// synchronously, so a test can assert on their effects as soon as Advance returns.
func (f *Fake) Advance(d time.Duration) {
	if d < 0 {
		panic("clock: Advance with negative duration")
	}
	f.mu.Lock()
	target := f.mono + d
	for f.timers.Len() > 0 && f.timers[0].when <= target {
		t := f.timers[0]
		if t.when > f.mono {
			f.mono = t.when
		}
		if t.period > 0 {
			t.when += t.period
			heap.Fix(&f.timers, t.index)
		} else {
			heap.Pop(&f.timers)
		}
		now := f.nowLocked()
		if t.fn != nil {
			f.mu.Unlock()
			t.fn()
			f.mu.Lock()
			continue
		}
		select {
		case t.c <- now:
		default: // the receiver is behind: drop the tick like time.Ticker
		}
	}
	if target > f.mono { // a concurrent Advance may already have gone further
		f.mono = target
	}
	f.mu.Unlock()
}

// BlockUntil blocks until at least n timers, tickers or sleepers are pending. Tests that start
// a goroutine which sleeps use it to avoid advancing the clock before the goroutine has
// registered its timer.
func (f *Fake) BlockUntil(n int) {
	f.mu.Lock()
	if f.timers.Len() >= n {
		f.mu.Unlock()
		return
	}
	w := waiter{n: n, ch: make(chan struct{})}
	f.waiters = append(f.waiters, w)
	f.mu.Unlock()
	<-w.ch
}

// Pending returns the number of pending timers, tickers and sleepers.
func (f *Fake) Pending() int { f.mu.Lock(); defer f.mu.Unlock(); return f.timers.Len() }

// Sleep, like time.Sleep, returns at once for a duration of zero or less.
func (f *Fake) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	<-f.After(d)
}

func (f *Fake) After(d time.Duration) <-chan time.Time { return f.NewTimer(d).C() }

// NewTimer returns a timer. As with time.NewTimer, a duration of zero or less fires at once,
// without an Advance.
func (f *Fake) NewTimer(d time.Duration) Timer {
	t := &fakeTimer{clock: f, ft: &ftimer{c: make(chan time.Time, 1), index: -1}}
	if d <= 0 {
		t.ft.c <- f.Now()
		return t
	}
	f.arm(t.ft, d, 0)
	return t
}

// AfterFunc runs fn once d has elapsed. The fake runs it synchronously inside Advance (the real
// clock runs it in its own goroutine), so fn must not wait for the goroutine that called
// Advance. A duration of zero or less runs fn in a new goroutine at once.
func (f *Fake) AfterFunc(d time.Duration, fn func()) Timer {
	t := &fakeTimer{clock: f, ft: &ftimer{fn: fn, index: -1}}
	if d <= 0 {
		go fn()
		return t
	}
	f.arm(t.ft, d, 0)
	return t
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	t := &fakeTicker{clock: f, ft: &ftimer{c: make(chan time.Time, 1), index: -1}}
	f.arm(t.ft, d, d)
	return t
}

// arm schedules t to fire after d (and then every period when period > 0).
func (f *Fake) arm(t *ftimer, d, period time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armLocked(t, d, period)
}

func (f *Fake) armLocked(t *ftimer, d, period time.Duration) {
	if d < 0 {
		d = 0
	}
	f.seq++
	t.seq = f.seq
	t.when = f.mono + d
	t.period = period
	heap.Push(&f.timers, t)
	for i := 0; i < len(f.waiters); {
		if f.timers.Len() >= f.waiters[i].n {
			close(f.waiters[i].ch)
			f.waiters = append(f.waiters[:i], f.waiters[i+1:]...)
			continue
		}
		i++
	}
}

// stopLocked removes t from the heap and drains a stale value. It reports whether t was pending.
func (f *Fake) stopLocked(t *ftimer) bool {
	pending := t.index >= 0 && t.index < f.timers.Len() && f.timers[t.index] == t
	if pending {
		heap.Remove(&f.timers, t.index)
	}
	if t.c != nil {
		select {
		case <-t.c:
		default:
		}
	}
	return pending
}

type fakeTimer struct {
	clock *Fake
	ft    *ftimer
}

func (t *fakeTimer) C() <-chan time.Time {
	if t.ft.c == nil {
		return nil
	}
	return t.ft.c
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	return t.clock.stopLocked(t.ft)
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	pending := t.clock.stopLocked(t.ft)
	t.clock.armLocked(t.ft, d, 0)
	return pending
}

type fakeTicker struct {
	clock *Fake
	ft    *ftimer
}

func (t *fakeTicker) C() <-chan time.Time { return t.ft.c }

func (t *fakeTicker) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.clock.stopLocked(t.ft)
}

func (t *fakeTicker) Reset(d time.Duration) {
	if d <= 0 {
		panic("clock: non-positive interval for Ticker.Reset")
	}
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.clock.stopLocked(t.ft)
	t.clock.armLocked(t.ft, d, d)
}

// ftimer is one entry of the timer heap.
type ftimer struct {
	when   time.Duration // monotonic deadline
	period time.Duration // > 0 for tickers
	seq    uint64        // registration order, breaks ties between equal deadlines
	index  int           // position in the heap, -1 when not queued
	c      chan time.Time
	fn     func()
}

type timerHeap []*ftimer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if h[i].when != h[j].when {
		return h[i].when < h[j].when
	}
	return h[i].seq < h[j].seq
}
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *timerHeap) Push(x any) {
	t := x.(*ftimer)
	t.index = len(*h)
	*h = append(*h, t)
}
func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = nil
	t.index = -1
	*h = old[:n-1]
	return t
}

var (
	_ Clock = (*Real)(nil)
	_ Clock = (*Fake)(nil)
)
