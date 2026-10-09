package clock

import (
	"testing"
	"time"
)

func TestRealMonotonicNeverGoesBackwards(t *testing.T) {
	c := NewReal()
	prev := c.Monotonic()
	for range 1000 {
		cur := c.Monotonic()
		if cur < prev {
			t.Fatalf("Monotonic went backwards: %v -> %v", prev, cur)
		}
		prev = cur
	}
}

// `&clock.Real{}` is the clock most of the code makes; its monotonic time must move (it stood still at the
// largest duration while its origin was the zero time, which froze every deadline computed from it:
// found by the flapping of M10, which toggles on it).
func TestTheZeroValueOfRealIsAClockWhoseMonotonicTimeMoves(t *testing.T) {
	c := &Real{}
	a := c.Monotonic()
	time.Sleep(20 * time.Millisecond)
	b := c.Monotonic()
	if a < 0 || a > time.Second {
		t.Errorf("the first reading is %v: the origin is the first reading, not the zero time", a)
	}
	if b-a < 15*time.Millisecond || b-a > time.Second {
		t.Errorf("20 ms passed and the clock moved by %v", b-a)
	}
}

func TestRealTimerAndTickerFire(t *testing.T) {
	c := NewReal()
	select {
	case <-c.NewTimer(5 * time.Millisecond).C():
	case <-time.After(2 * time.Second):
		t.Fatal("timer did not fire")
	}
	tk := c.NewTicker(5 * time.Millisecond)
	defer tk.Stop()
	for range 2 {
		select {
		case <-tk.C():
		case <-time.After(2 * time.Second):
			t.Fatal("ticker did not fire")
		}
	}
	done := make(chan struct{})
	c.AfterFunc(5*time.Millisecond, func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AfterFunc did not run")
	}
}
