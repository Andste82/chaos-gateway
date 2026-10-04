package engine

import (
	"testing"
	"time"
)

// M5-15 benchmark: publish must stay cheap once the replay window is full, not copy it on every
// call.
func BenchmarkPublishFullWindow(b *testing.B) {
	bus := newBus()
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < ReplayMax; i++ {
		bus.publish(t, "x", nil)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t = t.Add(time.Millisecond)
		bus.publish(t, "x", nil)
	}
}
