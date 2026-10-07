//go:build testbed

package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

type applyExec = apply.Exec

var errInjected = errors.New("injected executor failure")

func asApplyFailed(err error, target **engine.ErrApplyFailed) bool { return errors.As(err, target) }

// M8a on a real kernel: an overlay written through the state owner reaches the kernel's nftables
// (the mark chain of its fault id, the named counters, the classification map elements), a
// replacement changes the elements, and a delete, a reset and a TTL take everything away again. The
// tc tree is not applied by the engine before M8b; the tests that install it are in
// integration_classify_test.go.
func TestOverlaysReachTheRealKernelAndLeaveItAgain(t *testing.T) {
	r := newReal(t, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	owner := model.Owner{Type: "user", Id: "admin"}
	put := func(body string) engine.OverlayResult {
		t.Helper()
		res, err := r.e.PutOverlay(ctx, engine.OverlayWrite{Owner: owner, Request: overlayRequest(t, body)})
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return res
	}
	if strings.Contains(r.nft(), "mark_") && !strings.Contains(r.nft(), "chain mark_0") && strings.Count(r.nft(), "chain mark_") > 0 {
		t.Fatalf("faults before any overlay:\n%s", r.nft())
	}

	res := put("target: {network: IoT}\nfault: {latency: 120ms, destination: {cidr: 203.0.113.0/24}}\nttl: 90s")
	snap := r.e.Snapshot()
	if len(snap.Faults) != 1 {
		t.Fatalf("faults %+v", snap.Faults)
	}
	f := snap.Faults[0]
	nft := r.nft()
	for _, want := range []string{"chain " + compiler.MarkChainName(f.ID), "counter " + f.CounterUp, "counter " + f.CounterDown, "203.0.113.0/24"} {
		if !strings.Contains(nft, want) {
			t.Errorf("the kernel has no %q:\n%s", want, nft)
		}
	}
	if _, err := r.e.ReadCounters(ctx); err != nil {
		t.Error(err)
	}

	// a replacement keeps the id, and with it the fault id, the chain and the counters
	again := put("target: {network: IoT}\nfault: {latency: 200ms, destination: {cidr: 203.0.113.0/24}}\nttl: 90s")
	if again.Created || again.Overlay.Id != res.Overlay.Id {
		t.Fatalf("%+v", again)
	}
	if s := r.e.Snapshot(); len(s.Faults) != 1 || s.Faults[0].ID != f.ID || s.Faults[0].CounterUp != f.CounterUp {
		t.Fatalf("faults after the replacement: %+v, before %+v", s.Faults, f)
	}
	if nft := r.nft(); !strings.Contains(nft, "chain "+compiler.MarkChainName(f.ID)) {
		t.Errorf("after the replacement:\n%s", nft)
	}

	// another destination is another overlay (the selector is part of the key), with its own id and
	// elements; the other owner's overlay on the other network is a third
	second := put("target: {network: IoT}\nfault: {latency: 50ms, destination: {cidr: 198.51.100.0/24}}")
	if !second.Created || second.Overlay.Id == res.Overlay.Id {
		t.Fatalf("%+v", second)
	}
	other := model.Owner{Type: "token", Id: "33333333-3333-4333-8333-333333333333"}
	if _, err := r.e.PutOverlay(ctx, engine.OverlayWrite{Owner: other, Request: overlayRequest(t, "target: {network: Lab}\nfault: {loss: 3%}")}); err != nil {
		t.Fatal(err)
	}
	if nft := r.nft(); !strings.Contains(nft, "198.51.100.0/24") || !strings.Contains(nft, "203.0.113.0/24") || strings.Count(nft, "chain mark_") != 3 {
		t.Errorf("three faults:\n%s", nft)
	}

	// the TTL of the first (the fake clock): its elements and its chain go, the others stay
	r.clk.Advance(91 * time.Second)
	deadline := time.Now().Add(time.Minute)
	for len(r.e.Snapshot().Overlays) != 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := r.e.Barrier(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.e.Snapshot().Overlays; len(got) != 2 {
		t.Fatalf("after the TTL: %+v", got)
	}
	if nft := r.nft(); strings.Contains(nft, "203.0.113.0/24") || !strings.Contains(nft, "198.51.100.0/24") || strings.Count(nft, "chain mark_") != 2 {
		t.Errorf("after the TTL of the first overlay:\n%s", nft)
	}

	// reset of all owners leaves the kernel as it was before any overlay
	if rr, err := r.e.ResetOverlays(ctx, nil, owner); err != nil || rr.Removed != 2 {
		t.Fatalf("%+v %v", rr, err)
	}
	if strings.Contains(r.nft(), "chain mark_") {
		t.Errorf("faults after a reset:\n%s", r.nft())
	}
}

// An overlay the kernel refuses (here: the executor is made to fail) is taken back, and the kernel
// is left as it was.
func TestAnOverlayTheExecutorFailsOnIsTakenBackOnARealKernel(t *testing.T) {
	failNext := make(chan struct{}, 1)
	r := newReal(t, func(inner applyExec) applyExec {
		return &failingExec{inner: inner, fail: func(ops []executor.Operation) error {
			select {
			case <-failNext:
				return errInjected
			default:
				return nil
			}
		}}
	})
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	owner := model.Owner{Type: "user", Id: "admin"}
	before := r.nft()
	failNext <- struct{}{}
	_, err := r.e.PutOverlay(ctx, engine.OverlayWrite{Owner: owner, Request: overlayRequest(t, "target: {network: IoT}\nfault: {latency: 120ms}")})
	var af *engine.ErrApplyFailed
	if !asApplyFailed(err, &af) {
		t.Fatalf("got %v", err)
	}
	snap, err := r.e.Barrier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Overlays) != 0 || snap.LastError != "" {
		t.Errorf("%+v %q", snap.Overlays, snap.LastError)
	}
	if strings.Contains(r.nft(), "chain mark_") {
		t.Errorf("the kernel kept a fault of an overlay that was taken back:\n%s", r.nft())
	}
	_ = before
}
