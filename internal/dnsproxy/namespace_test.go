package dnsproxy

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// M6b-01 test: the service address is there at first, then gone for three checks in a row: the
// proxy's namespace watch reports it, so the process can exit and let a restart re-join the holder's
// current namespace.
func TestTheProxyExitsWhenItsNamespaceLosesTheServiceAddress(t *testing.T) {
	want := netip.MustParseAddr("169.254.100.2")
	var mu sync.Mutex
	present := true
	addrs := func() ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		if present {
			return []netip.Addr{want}, nil
		}
		return nil, nil
	}
	setPresent := func(v bool) {
		mu.Lock()
		present = v
		mu.Unlock()
	}

	clk := clock.NewFake(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- WatchNamespace(ctx, clk, want, time.Second, time.Minute, addrs) }()
	clk.BlockUntil(1)

	advance := func() {
		clk.Advance(time.Second)
		time.Sleep(20 * time.Millisecond)
	}
	received := func() (error, bool) {
		select {
		case err := <-errCh:
			return err, true
		default:
			return nil, false
		}
	}

	// present for a couple of checks: no error
	advance()
	advance()
	if _, ok := received(); ok {
		t.Fatalf("reported missing while still present")
	}

	setPresent(false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		advance()
		if err, ok := received(); ok {
			if err == nil {
				t.Fatalf("WatchNamespace returned nil, want an error")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("WatchNamespace never reported the lost address")
		}
	}
}

// M6b-01 test: the service address never shows up at all within the grace period: the watch reports
// it without ever having seen it.
func TestTheProxyExitsWhenItsNamespaceNeverGetsTheServiceAddress(t *testing.T) {
	want := netip.MustParseAddr("169.254.100.2")
	addrs := func() ([]netip.Addr, error) { return nil, nil }

	clk := clock.NewFake(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- WatchNamespace(ctx, clk, want, time.Second, 5*time.Second, addrs) }()
	clk.BlockUntil(1)

	deadline := time.Now().Add(10 * time.Second)
	for {
		clk.Advance(time.Second)
		time.Sleep(20 * time.Millisecond)
		select {
		case err := <-errCh:
			if err == nil {
				t.Fatalf("WatchNamespace returned nil, want an error")
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("WatchNamespace never reported that the address never appeared")
		}
	}
}

// ctx cancellation stops the watch cleanly, with no error.
func TestWatchNamespaceStopsCleanlyOnCancellation(t *testing.T) {
	want := netip.MustParseAddr("169.254.100.2")
	addrs := func() ([]netip.Addr, error) { return []netip.Addr{want}, nil }
	clk := clock.NewFake(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- WatchNamespace(ctx, clk, want, time.Second, time.Minute, addrs) }()
	clk.BlockUntil(1)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("got %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WatchNamespace did not return after cancellation")
	}
}
