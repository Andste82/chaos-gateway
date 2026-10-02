// Package clock provides the injectable clock used by everything that depends on time: overlay
// TTLs and leases, scenario schedules, the commit-confirm timeout (plan §2.1.1, §4.2).
//
// Two kinds of time are kept apart. Monotonic time drives TTLs, leases and schedules, because
// wall time can jump (a Raspberry Pi has no real-time clock and the wall clock moves at the
// first NTP sync). Wall time is for display and the audit log only.
package clock

import "time"

// Clock is the time source of a component. Production code uses Real, logic tests use Fake and
// advance it by hand, so TTL expiry, lease expiry and step order run in microseconds.
type Clock interface {
	// Now returns the wall-clock time. Use it for display and the audit log only.
	Now() time.Time
	// Monotonic returns the time elapsed on the monotonic clock since an arbitrary origin.
	// Deadlines (TTLs, leases, schedules) are computed from it and never jump.
	Monotonic() time.Duration

	// Sleep blocks until the monotonic clock has advanced by d.
	Sleep(d time.Duration)
	// After returns a channel that receives the wall time once d has elapsed.
	After(d time.Duration) <-chan time.Time
	// AfterFunc runs f in its own goroutine once d has elapsed.
	AfterFunc(d time.Duration, f func()) Timer
	NewTimer(d time.Duration) Timer
	// NewTicker returns a ticker that fires every d. A tick is dropped if the receiver has not
	// taken the previous one, like time.Ticker. d must be positive.
	NewTicker(d time.Duration) Ticker
}

// Timer mirrors time.Timer. After Stop or Reset returns, no stale value is left in C.
type Timer interface {
	// C is nil for timers created by AfterFunc.
	C() <-chan time.Time
	// Stop prevents the timer from firing. It reports whether it stopped a pending timer.
	Stop() bool
	// Reset re-arms the timer to fire after d. It reports whether the timer was pending.
	Reset(d time.Duration) bool
}

// Ticker mirrors time.Ticker.
type Ticker interface {
	C() <-chan time.Time
	Stop()
	Reset(d time.Duration)
}
