package engine_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// An apply that fails after its nft transaction has swapped the NAT rules leaves the kernel on the redirects of the failed
// target for a moment, and a device that keeps its source port and polls in that window is redirected by them. The verified
// target is still the old one, and the restore brings the old redirects back, so comparing the two finds no change: the engine
// remembers the failed target and deletes the tracked NTP flows of its redirects on the next success.
func TestAFailedApplyAfterTheNATRulesWereSwappedStillDeletesTheTrackedNTPFlowsOnTheRestore(t *testing.T) {
	h := tlsHarness(t)
	h.k.SetConntrack(ntpFlowsText)
	all := "203.0.113.10 203.0.113.11 203.0.113.12 203.0.113.13 203.0.113.14 203.0.113.9"
	if got := h.trackedDestinations(); got != all {
		t.Fatalf("flows before: %s", got)
	}

	// the command after the transaction that carries the redirect to the port of the selector fails, once
	var mu sync.Mutex
	armed, failed := false, false
	h.k.SetFail(func(argv []string, stdin string) *executor.Result {
		mu.Lock()
		defer mu.Unlock()
		if failed {
			return nil
		}
		if armed {
			failed = true
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		if argv[0] == "nft" && strings.Contains(stdin, "12301") {
			armed = true
		}
		return nil
	})
	if _, err := h.put(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 203.0.113.11}, ports: [5123], offset: +400d}"); err == nil {
		t.Fatal("the write succeeded: the injected failure did not stop the apply")
	}
	h.barrier()
	mu.Lock()
	ran := armed && failed
	mu.Unlock()
	if !ran {
		t.Fatal("the failure was not injected after the transaction with the redirect")
	}
	// the overlay is gone and the restore has brought the old redirects back: the flows the failed target decided are gone
	// (203.0.113.9, .10 and .11), the others stay
	if got, want := h.trackedDestinations(), "203.0.113.12 203.0.113.13 203.0.113.14"; got != want {
		t.Errorf("after the restore: %s, want %s", got, want)
	}
}
