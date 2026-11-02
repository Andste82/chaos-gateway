package engine_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// R3 of the audit of Phase 7: the last known good revision was tried once, so a failure that only a moment caused sent the
// gateway to safe mode, as the active revision's second try was meant to prevent.
func TestATransientFailureOfTheLastKnownGoodRevisionIsTriedAgainBeforeSafeMode(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
	var failures atomic.Int32
	h.k.SetFail(func(argv []string, stdin string) *executor.Result {
		text := strings.Join(argv, " ") + stdin
		switch {
		case strings.Contains(text, "203.0.113.20"): // revision 2 does not apply
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Network is unreachable\n"}
		case strings.Contains(text, "203.0.113.10") && failures.Add(1) == 1: // revision 1 fails once
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Device or resource busy\n"}
		}
		return nil
	})
	h.start()
	h.retryUntil(func() bool {
		s := h.e.Snapshot()
		return s.SafeMode || (s.Revision == r1 && s.Applied != nil && s.LastError == "")
	}, "revision 1 is applied or safe mode begins")
	s := h.e.Snapshot()
	if s.SafeMode {
		t.Fatalf("safe mode although revision %d works: %s", r1, s.SafeModeReason)
	}
	if !s.Applied.RolledBack || h.st.ActiveID() != r1 {
		t.Errorf("%+v, active %d", s.Applied, h.st.ActiveID())
	}
	if rev, _, err := h.st.Get(r2); err != nil || rev.Status != store.StatusRolledBack {
		t.Errorf("revision 2: %+v %v", rev, err)
	}
}

// downExec is an executor that cannot be reached while down is set: the operations that create a bridge fail the way a call
// through a socket that is gone does.
type downExec struct {
	apply.Exec
	down atomic.Bool
	// via, when set, makes the calls with a route through that gateway fail the same way: the executor goes away at the moment
	// the revision with that gateway is applied
	via atomic.Value
	// refused counts the calls that failed because the executor was down
	refused atomic.Int32
}

func (d *downExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if via, _ := d.via.Load().(string); via != "" {
		for _, op := range ops {
			rt, ok := op.(*executor.Routing)
			if !ok {
				continue
			}
			for _, r := range rt.Routes {
				if r.Via == via {
					d.refused.Add(1)
					return executor.Outcome{}, fmt.Errorf("dial /run/chaosgw/exec.sock: %w", io.ErrUnexpectedEOF)
				}
			}
		}
	}
	if d.down.Load() {
		for _, op := range ops {
			if l, ok := op.(*executor.Links); ok {
				for _, e := range l.Entries {
					if e.Action == "add_bridge" {
						d.refused.Add(1)
						return executor.Outcome{}, fmt.Errorf("dial /run/chaosgw/exec.sock: %w", io.ErrUnexpectedEOF)
					}
				}
			}
		}
	}
	return d.Exec.Do(ctx, ops...)
}

func bootFailedMark(t *testing.T, h *harness, id int64) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.dir, "revisions", fmt.Sprintf("%06d.status.json", id)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), "boot_failed_at")
}

// startDown is a start with an executor that cannot be reached until up is called.
func startDown(t *testing.T, h *harness) *downExec {
	t.Helper()
	down := &downExec{Exec: apply.Local{E: h.ex}}
	down.down.Store(true)
	e, err := engine.New(engine.Config{Store: h.st, Exec: down, Clock: h.clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e = e
	return down
}

// P7-M27-05: a start that fails because the executor cannot be reached says nothing about the revision. The gateway does not
// fall back to an older revision (Store.Restore would make it the active one for good), does not enter safe mode (that needs
// the executor too) and marks no revision; it applies the same revision again, with a growing delay, until the executor is back.
func TestARevisionIsNotReplacedByAnOlderOneWhileTheExecutorIsNotThere(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
	down := startDown(t, h)

	h.retryUntil(func() bool { return down.refused.Load() >= 3 }, "three tries")
	s := h.e.Snapshot()
	if s.SafeMode {
		t.Fatalf("safe mode although the revision was never refused: %s", s.SafeModeReason)
	}
	if s.LastError == "" || s.Revision != r2 || h.st.ActiveID() != r2 {
		t.Errorf("the snapshot should say that the last apply failed for revision %d: %+v, active %d", r2, s, h.st.ActiveID())
	}
	for _, id := range []int64{r1, r2} {
		if bootFailedMark(t, h, id) {
			t.Errorf("revision %d is marked as not applying at start although the executor could not be reached", id)
		}
	}
	if rev, _, err := h.st.Get(r2); err != nil || rev.Status == store.StatusRolledBack {
		t.Errorf("revision %d: %+v %v", r2, rev, err)
	}

	// the executor is back: the same revision is applied, nothing was rolled back
	down.down.Store(false)
	h.retryUntil(func() bool {
		s := h.e.Snapshot()
		return s.Applied != nil && s.LastError == "" && s.Revision == r2
	}, "revision 2 is applied")
	if s := h.e.Snapshot(); s.SafeMode || s.Applied.RolledBack || h.st.ActiveID() != r2 {
		t.Errorf("%+v, active %d", s, h.st.ActiveID())
	}
	if rev, _, err := h.st.Get(r2); err != nil || rev.Status == store.StatusRolledBack {
		t.Errorf("revision %d: %+v %v", r2, rev, err)
	}
}

// The same holds for the last known good revision: when the active revision is refused by the kernel and the executor is away
// when the fallback is applied, the fallback is tried again until the executor is back, and only then made the active one.
func TestTheLastKnownGoodRevisionIsAppliedAgainWhileTheExecutorIsNotThereAndOnlyThenMadeActive(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
	down := &downExec{Exec: apply.Local{E: h.ex}}
	// the kernel refuses revision 2, and the executor is gone when revision 1, the fallback, is applied
	down.via.Store("203.0.113.10")
	h.k.SetFail(func(argv []string, stdin string) *executor.Result {
		if strings.Contains(strings.Join(argv, " ")+stdin, "203.0.113.20") {
			return &executor.Result{Exit: 2, Stderr: "RTNETLINK answers: Network is unreachable\n"}
		}
		return nil
	})
	e, err := engine.New(engine.Config{Store: h.st, Exec: down, Clock: h.clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e = e

	h.retryUntil(func() bool { return down.refused.Load() >= 3 || h.e.Snapshot().SafeMode }, "the fallback finds the executor gone three times")
	s := h.e.Snapshot()
	if s.SafeMode {
		t.Fatalf("safe mode although revision %d never failed: %s", r1, s.SafeModeReason)
	}
	if s.Revision != r1 || h.st.ActiveID() != r2 {
		t.Errorf("the fallback should be applied again and not yet active: snapshot revision %d, active %d", s.Revision, h.st.ActiveID())
	}
	if bootFailedMark(t, h, r1) {
		t.Errorf("revision %d is marked as not applying at start", r1)
	}

	down.via.Store("")
	h.retryUntil(func() bool {
		s := h.e.Snapshot()
		return s.Applied != nil && s.LastError == "" && s.Revision == r1 && h.st.ActiveID() == r1
	}, "revision 1 is applied and active")
	if rev, _, err := h.st.Get(r2); err != nil || rev.Status != store.StatusRolledBack {
		t.Errorf("revision 2: %+v %v", rev, err)
	}
}

// A kernel that refuses the revisions is another matter: both are marked as not applying at start, and the gateway enters safe mode.
func TestAStartThatFailsBecauseTheKernelRefusesTheRevisionsMarksThemAndEntersSafeMode(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
	failBridges(h)
	h.start()
	h.retryUntil(func() bool { return h.e.Snapshot().SafeMode }, "safe mode")
	for _, id := range []int64{r1, r2} {
		if !bootFailedMark(t, h, id) {
			t.Errorf("revision %d is not marked although the kernel refused it", id)
		}
	}
}
