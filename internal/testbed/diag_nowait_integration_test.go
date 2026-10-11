//go:build testbed

package testbed_test

// TEMPORARY diagnostic for P2-M8b-08: the regression test without WaitLinks must show the effect on a
// native kernel. It logs instead of failing.

import (
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

func TestDiagFirstPacketWithoutWaitLinks(t *testing.T) {
	testbed.DiagNoLinkWait = true
	defer func() { testbed.DiagNoLinkWait = false }()
	for round := 0; round < 3; round++ {
		top := testbed.NewDefault(t, testbed.WithGatewayBridges(false))
		top.GW.Addr("lan0", testbed.LAN0Gateway+"/24")
		top.GW.Addr("lan1", testbed.LAN1Gateway+"/24")
		devices := []*testbed.Namespace{top.A, top.B, top.C}
		got := make([]testbed.PingResult, len(devices))
		var wg sync.WaitGroup
		for i, ns := range devices {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got[i], _ = testbed.Ping(ctx(t), ns, testbed.ServerAddr, 1, time.Second)
			}()
		}
		wg.Wait()
		for i, ns := range devices {
			t.Logf("DIAG round %d without WaitLinks: %s received %d rtts %v", round, ns.Short, got[i].Received, got[i].RTTs)
		}
		top.Destroy()
	}
}
