package engine

import (
	"net/netip"
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

// A forwarded connection's original source is the peer behind the entry interface; the device it was
// sent to is the reply's source. It counts as in use, so a device that only answers forwards keeps its
// stale neighbor entry and with it the address its forward sends to (P31-AUDIT-14). A connection that is
// not translated, a masqueraded one and the DNS proxy's service redirect name no such device.
func TestAForwardedConnectionMarksTheDeviceItWasSentToAsInUse(t *testing.T) {
	flow := func(src, dst, rsrc, rdst string) linux.Conntrack {
		return linux.Conntrack{Proto: "tcp", Original: linux.Tuple{Src: src, Dst: dst}, Reply: linux.Tuple{Src: rsrc, Dst: rdst}}
	}
	if a, ok := forwardTarget(flow("203.0.113.10", "192.168.1.2", "10.10.0.11", "203.0.113.10")); !ok || a != netip.MustParseAddr("10.10.0.11") {
		t.Fatalf("forwarded: %v %v", a, ok)
	}
	for name, f := range map[string]linux.Conntrack{
		"not translated": flow("10.10.0.11", "10.20.0.5", "10.20.0.5", "10.10.0.11"),
		"masqueraded":    flow("10.10.0.11", "198.51.100.7", "198.51.100.7", "192.168.1.2"),
		"service":        flow("10.10.0.11", "10.10.0.1", "169.254.100.2", "10.10.0.11"),
		"unparsable":     flow("10.10.0.11", "", "10.10.0.12", "10.10.0.11"),
	} {
		if a, ok := forwardTarget(f); ok {
			t.Errorf("%s: %v", name, a)
		}
	}
}
