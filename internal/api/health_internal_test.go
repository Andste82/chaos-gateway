package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// M5-13 test: concurrent callers of the unauthenticated health check share one in-flight executor
// probe, and a repeated check inside the cache window does not probe again.
func TestHealthProbeCacheDeduplicatesConcurrentCallers(t *testing.T) {
	var h healthProbeCache
	var calls atomic.Int64
	start := make(chan struct{})
	probe := func(ctx context.Context) error {
		calls.Add(1)
		<-start // held open until every caller has had a chance to join the in-flight probe
		return nil
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.check(context.Background(), now, probe); err != nil {
				t.Error(err)
			}
		}()
	}
	// give every goroutine a chance to reach the probe (or the cache) before releasing it
	time.Sleep(50 * time.Millisecond)
	close(start)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("the executor was probed %d times for 50 concurrent callers", n)
	}

	// still within the 2s window: no new probe
	if err := h.check(context.Background(), now.Add(1900*time.Millisecond), probe); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("probed again inside the cache window: %d calls", n)
	}

	// past the window: probes again
	if err := h.check(context.Background(), now.Add(2001*time.Millisecond), probe); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("did not probe again past the cache window: %d calls", n)
	}
}
