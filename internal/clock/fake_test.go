package clock

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var epoch = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func recv(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case v := <-ch:
		return v
	default:
		t.Fatal("expected a value on the channel")
		return time.Time{}
	}
}

func empty(t *testing.T, ch <-chan time.Time) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected value %v", v)
	default:
	}
}

func TestFakeStartsAtGivenTime(t *testing.T) {
	c := NewFake(epoch)
	if !c.Now().Equal(epoch) || c.Monotonic() != 0 {
		t.Fatalf("Now=%v Monotonic=%v", c.Now(), c.Monotonic())
	}
}

func TestFakeAdvanceMovesBothClocks(t *testing.T) {
	c := NewFake(epoch)
	c.Advance(90 * time.Second)
	if want := epoch.Add(90 * time.Second); !c.Now().Equal(want) {
		t.Fatalf("Now = %v, want %v", c.Now(), want)
	}
	if c.Monotonic() != 90*time.Second {
		t.Fatalf("Monotonic = %v", c.Monotonic())
	}
}

func TestFakeTimerFiresAtDeadline(t *testing.T) {
	c := NewFake(epoch)
	tm := c.NewTimer(10 * time.Second)
	c.Advance(9*time.Second + 999*time.Millisecond)
	empty(t, tm.C())
	c.Advance(time.Millisecond)
	if got := recv(t, tm.C()); !got.Equal(epoch.Add(10 * time.Second)) {
		t.Fatalf("fired at %v", got)
	}
	empty(t, tm.C()) // one-shot
}

func TestFakeTimersFireInDeadlineOrderAndSeeTheirOwnTime(t *testing.T) {
	c := NewFake(epoch)
	var order []string
	var seen []time.Duration
	for _, s := range []struct {
		name string
		d    time.Duration
	}{{"c", 30 * time.Second}, {"a", 10 * time.Second}, {"b", 20 * time.Second}} {
		c.AfterFunc(s.d, func() {
			order = append(order, s.name)
			seen = append(seen, c.Monotonic())
		})
	}
	c.Advance(time.Minute)
	if got := order[0] + order[1] + order[2]; got != "abc" {
		t.Fatalf("order = %q", got)
	}
	if seen[0] != 10*time.Second || seen[1] != 20*time.Second || seen[2] != 30*time.Second {
		t.Fatalf("callbacks saw clock %v", seen)
	}
	if c.Monotonic() != time.Minute {
		t.Fatalf("Monotonic = %v after Advance", c.Monotonic())
	}
}

func TestFakeEqualDeadlinesFireInRegistrationOrder(t *testing.T) {
	c := NewFake(epoch)
	var order []int
	for i := range 5 {
		c.AfterFunc(time.Second, func() { order = append(order, i) })
	}
	c.Advance(time.Second)
	for i, v := range order {
		if v != i {
			t.Fatalf("order = %v", order)
		}
	}
}

func TestFakeCallbackMayArmAnotherTimerWithinTheSameAdvance(t *testing.T) {
	c := NewFake(epoch)
	var fired []time.Duration
	c.AfterFunc(5*time.Second, func() {
		fired = append(fired, c.Monotonic())
		c.AfterFunc(5*time.Second, func() { fired = append(fired, c.Monotonic()) })
	})
	c.Advance(12 * time.Second)
	if len(fired) != 2 || fired[0] != 5*time.Second || fired[1] != 10*time.Second {
		t.Fatalf("fired = %v", fired)
	}
}

func TestFakeStopPreventsFiring(t *testing.T) {
	c := NewFake(epoch)
	tm := c.NewTimer(time.Second)
	if !tm.Stop() {
		t.Fatal("Stop of a pending timer must report true")
	}
	if tm.Stop() {
		t.Fatal("second Stop must report false")
	}
	c.Advance(time.Hour)
	empty(t, tm.C())
}

func TestFakeStopAfterFireDrainsTheChannel(t *testing.T) {
	c := NewFake(epoch)
	tm := c.NewTimer(time.Second)
	c.Advance(time.Second)
	if tm.Stop() {
		t.Fatal("Stop of a fired timer must report false")
	}
	empty(t, tm.C()) // no stale value after Stop (Go 1.23 timer semantics)
}

func TestFakeResetRearmsFromNow(t *testing.T) {
	c := NewFake(epoch)
	tm := c.NewTimer(10 * time.Second)
	c.Advance(6 * time.Second)
	if !tm.Reset(10 * time.Second) {
		t.Fatal("Reset of a pending timer must report true")
	}
	c.Advance(9 * time.Second)
	empty(t, tm.C())
	c.Advance(time.Second)
	recv(t, tm.C())
	if tm.Reset(time.Second) {
		t.Fatal("Reset of a fired timer must report false")
	}
	c.Advance(time.Second)
	recv(t, tm.C())
}

func TestFakeAfterFuncStopAndReset(t *testing.T) {
	c := NewFake(epoch)
	var n atomic.Int32
	tm := c.AfterFunc(time.Second, func() { n.Add(1) })
	if tm.C() != nil {
		t.Fatal("AfterFunc timers have no channel")
	}
	tm.Stop()
	c.Advance(time.Minute)
	if n.Load() != 0 {
		t.Fatal("stopped AfterFunc ran")
	}
	tm.Reset(time.Second)
	c.Advance(time.Second)
	if n.Load() != 1 {
		t.Fatalf("n = %d, want 1", n.Load())
	}
}

func TestFakeTickerFiresPeriodically(t *testing.T) {
	c := NewFake(epoch)
	tk := c.NewTicker(10 * time.Second)
	defer tk.Stop()
	for i := 1; i <= 3; i++ {
		c.Advance(10 * time.Second)
		if got := recv(t, tk.C()); !got.Equal(epoch.Add(time.Duration(i) * 10 * time.Second)) {
			t.Fatalf("tick %d at %v", i, got)
		}
	}
}

func TestFakeTickerDropsTicksOfASlowReceiver(t *testing.T) {
	c := NewFake(epoch)
	tk := c.NewTicker(time.Second)
	c.Advance(10 * time.Second) // ten ticks, nobody reading
	recv(t, tk.C())             // channel holds exactly one
	empty(t, tk.C())
}

func TestFakeTickerStopAndReset(t *testing.T) {
	c := NewFake(epoch)
	tk := c.NewTicker(time.Second)
	tk.Stop()
	c.Advance(time.Minute)
	empty(t, tk.C())
	tk.Reset(5 * time.Second)
	c.Advance(4 * time.Second)
	empty(t, tk.C())
	c.Advance(time.Second)
	recv(t, tk.C())
}

func TestFakeWallJumpDoesNotMoveTimers(t *testing.T) {
	c := NewFake(epoch)
	tm := c.NewTimer(time.Minute)
	c.JumpWall(24 * time.Hour) // NTP sync on a device without a real-time clock
	if want := epoch.Add(24 * time.Hour); !c.Now().Equal(want) {
		t.Fatalf("Now = %v, want %v", c.Now(), want)
	}
	if c.Monotonic() != 0 {
		t.Fatalf("Monotonic = %v after a wall jump", c.Monotonic())
	}
	empty(t, tm.C())
	c.Advance(time.Minute)
	recv(t, tm.C())
	// the timer still fires exactly one minute of monotonic time later; Now includes the jump
	if want := epoch.Add(24*time.Hour + time.Minute); !c.Now().Equal(want) {
		t.Fatalf("Now = %v, want %v", c.Now(), want)
	}
}

func TestFakeNegativeAndZeroDurations(t *testing.T) {
	c := NewFake(epoch)
	tm := c.NewTimer(-time.Second) // fires on the next Advance, even Advance(0)
	c.Advance(0)
	recv(t, tm.C())
	defer func() {
		if recover() == nil {
			t.Fatal("Advance with a negative duration must panic")
		}
	}()
	c.Advance(-time.Second)
}

func TestFakeSleepWakesWhenAdvanced(t *testing.T) {
	c := NewFake(epoch)
	done := make(chan time.Duration, 1)
	go func() {
		c.Sleep(30 * time.Second)
		done <- c.Monotonic()
	}()
	c.BlockUntil(1)
	select {
	case <-done:
		t.Fatal("Sleep returned before the clock advanced")
	default:
	}
	c.Advance(30 * time.Second)
	if got := <-done; got != 30*time.Second {
		t.Fatalf("woke at %v", got)
	}
}

func TestFakeBlockUntilCountsPendingTimers(t *testing.T) {
	c := NewFake(epoch)
	ready := make(chan struct{})
	go func() {
		c.BlockUntil(2)
		close(ready)
	}()
	c.NewTimer(time.Second)
	select {
	case <-ready:
		t.Fatal("BlockUntil(2) returned with one timer")
	case <-time.After(20 * time.Millisecond):
	}
	c.NewTicker(time.Second)
	<-ready
	if c.Pending() != 2 {
		t.Fatalf("Pending = %d", c.Pending())
	}
}

func TestFakeConcurrentUse(t *testing.T) {
	c := NewFake(epoch)
	var wg sync.WaitGroup
	var fired atomic.Int32
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm := c.AfterFunc(time.Second, func() { fired.Add(1) })
			_ = c.Now()
			_ = c.Monotonic()
			tm.Reset(2 * time.Second)
		}()
	}
	wg.Wait()
	c.Advance(time.Second)
	if fired.Load() != 0 {
		t.Fatalf("%d timers fired after being reset to 2s", fired.Load())
	}
	c.Advance(time.Second)
	if fired.Load() != 20 {
		t.Fatalf("fired = %d, want 20", fired.Load())
	}
}
