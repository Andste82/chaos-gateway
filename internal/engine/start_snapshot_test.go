package engine_test

import "testing"

// The engine publishes its first snapshot before Start returns: a caller that asks for the snapshot or
// a preview right after Start sees the host, not the empty snapshot of a state owner that has not
// run yet (which made a preview report "the uplink interface is not present").
func TestTheSnapshotRightAfterStartHoldsTheHost(t *testing.T) {
	for i := 0; i < 30; i++ {
		h := newHarness(t)
		h.start()
		if s := h.e.Snapshot(); len(s.Host.Links) == 0 {
			t.Fatalf("attempt %d: the snapshot right after Start has no host", i)
		}
	}
}
