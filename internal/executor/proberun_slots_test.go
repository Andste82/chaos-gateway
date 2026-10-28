package executor

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/diag"
)

// blockedNet is a network stack whose connections hang until the run is cancelled (a blackholed address).
type blockedNet struct{ dialing atomic.Int32 }

func (b *blockedNet) DialContext(ctx context.Context, _, addr string) (net.Conn, error) {
	b.dialing.Add(1)
	<-ctx.Done()
	return nil, fmt.Errorf("connect %s: %w", addr, ctx.Err())
}

func (b *blockedNet) ListenICMP() (net.PacketConn, bool, error) {
	return nil, false, fmt.Errorf("no ICMP")
}

// Probe runs last up to minutes: if they took reader slots, a few of them would stall every read of an apply and of the
// observation (P6-AUDIT-02).
func TestProbeRunsThatFillTheirSlotsDoNotStarveTheReads(t *testing.T) {
	bn := &blockedNet{}
	e := newExec(t, probeRunner(), WithProbeNet(func(string) diag.Net { return bn }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// more runs than there are slots and more than there are reader slots
	for i := 0; i < readerSlots+2; i++ {
		go func() {
			_, _ = e.Do(ctx, mustDecode(t, `{"type":"probe_run","probe":"cgp-3a1f7c52","kind":"diagnostic","request":{"type":"tcp","host":"203.0.113.9","port":80,"timeout":"10s"}}`))
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for bn.dialing.Load() < probeRunSlots {
		if time.Now().After(deadline) {
			t.Fatalf("%d probe runs started", bn.dialing.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := bn.dialing.Load(); n != probeRunSlots {
		t.Errorf("%d probe runs went at once, want %d", n, probeRunSlots)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer rcancel()
	start := time.Now()
	if _, err := e.Do(rctx, mustDecode(t, `{"type":"read","what":"links"}`)); err != nil {
		t.Fatalf("a read behind the probe runs: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the read waited %v", time.Since(start))
	}
}
