package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// The recovery at start (plan §3.9, §2.14, M27). The kernel state is never the source of truth: the engine applies the
// active revision, which is a full apply against what the kernel holds and verifies it, so an apply that a power loss
// interrupted is recovered the same way as a clean start. When that apply fails (twice, to leave a transient failure alone),
// or the revision cannot be read, the engine applies the last revision that worked (Store.Fallback) and, when that works,
// makes it the active one (Store.Restore). When that fails too, or there is none, the gateway runs in safe mode: the
// configuration of domain.SafeModeConfiguration, management access only and no forwarding, the reason in the snapshot. A
// revision that is applied and verified ends the safe mode.

// EventSafeMode says that the gateway entered safe mode (data active true and the reason) or left it (active false).
const EventSafeMode = "safe_mode"

// bootApplyAttempts is how often the apply of the active revision is tried before the last known good one is: a failure
// that only a moment caused (the executor busy, a link that came up a second late) must not roll the configuration back.
const bootApplyAttempts = 2

// bootState is the start of the process until the revision it applies has been applied and verified.
type bootState struct {
	// revision and cfg are what is being applied, gen the first generation that belongs to it.
	revision int64
	cfg      *model.Configuration
	gen      uint64
	// attempts counts the applies of this revision that failed.
	attempts int
	// fallback is set while revision is the last known good one, tried because failed could not be applied.
	fallback bool
	failed   int64
	failure  string
	// failedCfg is the configuration of failed, nil when it could not be read.
	failedCfg *model.Configuration
}

// recoverUnreadable handles an active revision that cannot be read (a damaged file): there is nothing to apply, so it goes to
// the last known good revision at once.
func (o *owner) recoverUnreadable(init *ownerInit) {
	o.snap.Revision = init.revision
	if o.startFallback(init.revision, init.failure, nil) {
		return
	}
	o.enterSafeMode(&bootState{failed: init.revision, failure: init.failure}, fmt.Sprintf("revision %d cannot be read (%s) and there is no earlier revision that worked", init.revision, init.failure))
}

// startFallback applies the last known good revision because failed could not be applied or read. It reports whether there
// is one.
func (o *owner) startFallback(failed int64, failure string, failedCfg *model.Configuration) bool {
	rev, cfg, err := o.e.cfg.Store.Fallback()
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			o.e.cfg.Log.Warn("no revision to fall back to", "error", err)
		}
		return false
	}
	o.e.cfg.Log.Warn("revision cannot be applied at start: applying the last known good revision", "revision", failed, "reason", failure, "fallback", rev.Id)
	d := o.nextDesired(cfg, rev.Id)
	d.RolledBack = true
	o.boot = &bootState{revision: rev.Id, cfg: cfg, gen: d.Generation, fallback: true, failed: failed, failure: failure, failedCfg: failedCfg}
	o.committed = d
	o.snap.Revision, o.snap.Config = rev.Id, cfg
	o.converge(d)
	return true
}

// bootResult takes the result of an apply into the recovery. It reports whether the result belonged to the start and was
// handled there.
func (o *owner) bootResult(r applyResult) bool {
	b := o.boot
	if b == nil || r.d.Revision != b.revision || r.d.Generation < b.gen {
		return false
	}
	if r.err == nil {
		o.boot = nil
		if !b.fallback {
			if err := o.e.cfg.Store.MarkGood(b.revision); err != nil {
				o.e.cfg.Log.Warn("cannot mark the active revision as the last known good one", "revision", b.revision, "error", err)
			}
			return true
		}
		if _, err := o.e.cfg.Store.Restore(b.revision, o.now()); err != nil {
			// the kernel runs the restored revision; the store keeps the pointer of the failed one, and the next start repeats this
			o.e.cfg.Log.Error("cannot make the last known good revision the active one", "revision", b.revision, "error", err)
		}
		o.committed = r.d
		o.snap.Revision, o.snap.Config = r.d.Revision, r.d.Config
		o.prune(r.d.Config)
		o.event(EventRolledBack, map[string]any{"revision": b.failed, "reason": "boot: " + b.failure, "restored": b.revision})
		return true
	}
	b.attempts++
	switch {
	case b.fallback:
		o.enterSafeMode(b, fmt.Sprintf("revision %d could not be applied (%s), and neither could revision %d, the last known good one (%s)",
			b.failed, firstLine(b.failure), b.revision, firstLine(r.err.Error())))
	case b.attempts < bootApplyAttempts:
		// scheduleRetry applies it again
		b.failure = r.err.Error()
	case o.startFallback(b.revision, r.err.Error(), b.cfg):
	default:
		b.failure = r.err.Error()
		o.enterSafeMode(b, fmt.Sprintf("revision %d could not be applied (%s) and there is no earlier revision that worked", b.revision, firstLine(r.err.Error())))
	}
	return true
}

// enterSafeMode runs the gateway in safe mode: the configuration of domain.SafeModeConfiguration made of the failed revision
// (else of the last known good one) is applied, the snapshot says why. Nothing is applied when no configuration could be read.
func (o *owner) enterSafeMode(b *bootState, reason string) {
	o.boot = nil
	src := b.failedCfg
	if src == nil {
		src = b.cfg
	}
	o.safe = true
	o.snap.SafeMode, o.snap.SafeModeReason = true, reason
	o.snap.Revision = o.e.cfg.Store.ActiveID()
	// the revisions that did not apply must not be the fallback of a later start: the way out of safe mode is a newer revision,
	// which supersedes the failed one with its confirmation still set
	for _, id := range []int64{b.failed, b.revision} {
		if id == 0 {
			continue
		}
		var corrupt *store.ErrCorrupt
		// a damaged revision is skipped by the fallback anyway
		if err := o.e.cfg.Store.MarkBootFailed(id, o.now()); err != nil && !errors.As(err, &corrupt) {
			o.e.cfg.Log.Warn("cannot record that the revision failed at start", "revision", id, "error", err)
		}
	}
	o.e.cfg.Log.Error("safe mode: management access only, no forwarding", "reason", reason)
	o.event(EventSafeMode, map[string]any{"active": true, "reason": reason})
	if src == nil {
		// nothing readable names the management interface: there is nothing to compile. What the kernel holds of the gateway's
		// (after a restart of the API alone, the previous revision's bridges, table and routes) would go on forwarding, which
		// safe mode says it does not, so it is removed as the stop removes it
		o.committed, o.current = nil, nil
		o.snap.Config = nil
		o.e.desired.Store(nil)
		o.removeGatewayState()
		return
	}
	safe := domain.SafeModeConfiguration(src)
	d := o.nextDesired(safe, 0)
	o.committed = d
	o.snap.Config = safe
	o.converge(d)
}

// removeGatewayState removes everything of the gateway's from the kernel (apply.Teardown). It is bounded, and a failure is
// logged: safe mode goes on without it.
func (o *owner) removeGatewayState() {
	ctx, cancel := context.WithTimeout(o.e.ctx, teardownTimeout)
	defer cancel()
	if _, err := apply.Teardown(ctx, o.e.cfg.Exec, o.e.cfg.Namespace, o.e.cfg.ServiceNS); err != nil {
		o.e.cfg.Log.Error("safe mode: the gateway's state in the kernel was not removed", "error", err)
	}
}

// teardownTimeout bounds the removal of the gateway's state in safe mode without a configuration.
const teardownTimeout = 30 * time.Second

// leaveSafeMode ends the recovery and the safe mode: a revision was applied and verified.
func (o *owner) leaveSafeMode() {
	o.boot = nil
	if !o.safe {
		return
	}
	o.safe = false
	o.snap.SafeMode, o.snap.SafeModeReason = false, ""
	o.e.cfg.Log.Info("safe mode ended: a revision was applied")
	o.event(EventSafeMode, map[string]any{"active": false})
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
