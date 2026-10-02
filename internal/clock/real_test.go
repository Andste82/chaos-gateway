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
