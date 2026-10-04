//go:build testbed

package observer

import (
	"context"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// TestNeighborChangesTrigger exercises WatchNeighbors over a real netlink socket in a real
// namespace (TestEventsAreDebouncedIntoOneTrigger, in netlink_test.go, only covers the debounce
// logic through a fake source): adding a neighbor entry triggers exactly one notification.
func TestNeighborChangesTrigger(t *testing.T) {
	bed := testbed.New(t)
	ns := bed.Add("n")
	ns.Must("ip", "link", "add", "dummy0", "type", "dummy")
	ns.Must("ip", "link", "set", "dummy0", "up")

	ch, err := WatchNeighbors(context.Background(), ns.Name, &clock.Real{}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ns.Must("ip", "neigh", "add", "10.0.0.1", "lladdr", "02:00:00:00:00:01", "dev", "dummy0", "nud", "permanent")
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no trigger after adding a neighbor")
	}

	// a second, unrelated add is also reported (not just the first ever change)
	ns.Must("ip", "neigh", "add", "10.0.0.2", "lladdr", "02:00:00:00:00:02", "dev", "dummy0", "nud", "permanent")
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no trigger for the second neighbor")
	}
}
