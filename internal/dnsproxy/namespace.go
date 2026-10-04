package dnsproxy

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// WatchNamespace checks, every interval, that want is one of this process's interface addresses
// (addrs, normally net.InterfaceAddrs wrapped to return netip.Addr). It returns an error once want,
// having been present at least once, is then absent on three checks in a row, or was never present
// within grace. That is how the proxy notices its network namespace was replaced (plan §3.8, S16 C3):
// the holder it joined through network_mode is gone, and this one is stale. Call it in a goroutine
// alongside Serve; it returns nil when ctx is done.
func WatchNamespace(ctx context.Context, clk clock.Clock, want netip.Addr, interval, grace time.Duration, addrs func() ([]netip.Addr, error)) error {
	ticker := clk.NewTicker(interval)
	defer ticker.Stop()
	start := clk.Now()
	seen := false
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
		}
		present := hasAddr(addrs, want)
		if present {
			seen = true
			misses = 0
			continue
		}
		if seen {
			misses++
			if misses >= 3 {
				return fmt.Errorf("the service address %s is no longer on this namespace's interfaces", want)
			}
			continue
		}
		if clk.Now().Sub(start) >= grace {
			return fmt.Errorf("the service address %s never appeared on this namespace's interfaces within %s", want, grace)
		}
	}
}

// hasAddr reports whether want is among addrs()'s result. A failing read counts as "not present": the
// namespace may be mid-replacement, and the caller's retry/grace logic already covers that.
func hasAddr(addrs func() ([]netip.Addr, error), want netip.Addr) bool {
	got, err := addrs()
	if err != nil {
		return false
	}
	for _, a := range got {
		if a == want {
			return true
		}
	}
	return false
}
