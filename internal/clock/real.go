package clock

import "time"

// Real is the system clock.
type Real struct {
	origin time.Time // carries a monotonic reading
}

// NewReal returns the system clock. Its monotonic origin is the moment of the call.
func NewReal() *Real { return &Real{origin: time.Now()} }

func (r *Real) Now() time.Time                         { return time.Now() }
func (r *Real) Monotonic() time.Duration               { return time.Since(r.origin) }
func (r *Real) Sleep(d time.Duration)                  { time.Sleep(d) }
func (r *Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (r *Real) AfterFunc(d time.Duration, f func()) Timer {
	return realTimer{time.AfterFunc(d, f)}
}

func (r *Real) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

func (r *Real) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTimer struct{ t *time.Timer }

func (t realTimer) C() <-chan time.Time        { return t.t.C }
func (t realTimer) Stop() bool                 { return t.t.Stop() }
func (t realTimer) Reset(d time.Duration) bool { return t.t.Reset(d) }

type realTicker struct{ t *time.Ticker }

func (t realTicker) C() <-chan time.Time   { return t.t.C }
func (t realTicker) Stop()                 { t.t.Stop() }
func (t realTicker) Reset(d time.Duration) { t.t.Reset(d) }
