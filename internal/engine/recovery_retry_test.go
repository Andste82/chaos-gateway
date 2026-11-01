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
}

func (d *downExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if d.down.Load() {
		for _, op := range ops {
			if l, ok := op.(*executor.Links); ok {
				for _, e := range l.Entries {
					if e.Action == "add_bridge" {
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

// R3: a start that fails because the executor cannot be reached says nothing about the revisions, so none of them is marked as
// not applying at start (the next start, with the executor back, tries them again), where a revision the kernel refuses is.
func TestAStartThatFailsBecauseTheExecutorIsNotThereMarksNoRevision(t *testing.T) {
	h := newHarness(t)
	r1, r2 := twoRevisions(h)
	h = reboot(h)
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
	h.retryUntil(func() bool { return h.e.Snapshot().SafeMode }, "safe mode")
	for _, id := range []int64{r1, r2} {
		if bootFailedMark(t, h, id) {
			t.Errorf("revision %d is marked as not applying at start although the executor could not be reached", id)
		}
	}
	if rev, _, err := h.st.Fallback(); err != nil || rev.Id != r1 {
		t.Errorf("the fallback of the next start: %+v %v", rev, err)
	}

	// the same start with a kernel that refuses the revisions marks them
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
