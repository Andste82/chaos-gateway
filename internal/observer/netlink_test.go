package observer

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestWatchWithoutANamespaceNeedsNoPrivileges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := Watch(ctx, "", &clock.Real{}, 20*time.Millisecond)
	if err != nil {
		t.Skipf("no netlink here: %v", err)
	}
	// nothing happens: nothing is reported (the host's own changes are possible, so only check the end)
	cancel()
	select {
	case _, ok := <-ch:
		_ = ok
	case <-time.After(3 * time.Second):
		t.Fatal("the channel did not close after the context ended")
	}
	for range ch {
	}
}

func TestWatchInAnUnknownNamespaceFails(t *testing.T) {
	if _, err := Watch(context.Background(), "cgx-no-such-namespace", &clock.Real{}, time.Millisecond); err == nil {
		t.Fatal("watching a namespace that does not exist must fail")
	}
}

func TestEventsAreDebouncedIntoOneTrigger(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("no ip")
	}
	// the dummy device needs privileges the unprivileged container lacks: this is covered by the
	// testbed test; here only the debounce logic is exercised through a fake source
	events := make(chan struct{}, 1)
	out := make(chan struct{}, 1)
	clk := clock.NewFake(time.Unix(0, 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		debounce(ctx, clk, 50*time.Millisecond, events, out)
	}()
	for i := 0; i < 20; i++ {
		events <- struct{}{}
		time.Sleep(time.Millisecond)
	}
	clk.BlockUntil(1)
	clk.Advance(60 * time.Millisecond)
	select {
	case <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("no trigger after the burst")
	}
	select {
	case <-out:
		t.Fatal("a second trigger for one burst")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	<-done
}
