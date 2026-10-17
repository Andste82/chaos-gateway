package engine

import (
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// A neighbor entry keeps the time it was first read for as long as it stays in the table, so a device's
// new address is seen later than the old one that is still REACHABLE; an entry that leaves the table and
// comes back counts as new.
func TestANeighborEntryKeepsWhenItWasFirstSeen(t *testing.T) {
	e := &Engine{}
	old := linux.Neighbor{Dst: "10.10.0.11", Dev: "br0", LLAddr: "02:00:00:00:00:0a", State: []string{"REACHABLE"}}
	moved := linux.Neighbor{Dst: "10.10.0.61", Dev: "br0", LLAddr: "02:00:00:00:00:0A", State: []string{"REACHABLE"}}
	t0 := time.Unix(1000, 0)
	if s := e.neighborsSeen([]linux.Neighbor{old}, t0); !s[neighKey(old)].Equal(t0) {
		t.Fatalf("first read: %v", s)
	}
	s := e.neighborsSeen([]linux.Neighbor{old, moved}, t0.Add(2*time.Second))
	if !s[neighKey(old)].Equal(t0) || !s[neighKey(moved)].Equal(t0.Add(2*time.Second)) {
		t.Fatalf("second read: %v", s)
	}
	e.neighborsSeen([]linux.Neighbor{moved}, t0.Add(3*time.Second))
	s = e.neighborsSeen([]linux.Neighbor{old, moved}, t0.Add(4*time.Second))
	if !s[neighKey(old)].Equal(t0.Add(4*time.Second)) || !s[neighKey(moved)].Equal(t0.Add(2*time.Second)) {
		t.Fatalf("the old entry came back: %v", s)
	}
}
