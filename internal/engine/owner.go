package engine

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// desired is what the apply loop converges to. It is immutable once published.
type desired struct {
	Config     *model.Configuration
	Revision   int64
	Host       compiler.Host
	Generation uint64
}

type ownerInit struct {
	revision int64
	config   *model.Configuration
	host     compiler.Host
}

// command is a request to the state owner.
type command interface{ command() }

// ApplyOptions tune Apply.
type ApplyOptions struct {
	// SkipConfirm commits a lockout-relevant revision without the confirmation window (an
	// explicit request of the caller, e.g. the bootstrap CLI).
	SkipConfirm bool
	// ConfirmTimeout overrides the window; zero means the configuration's setting or the default.
	ConfirmTimeout time.Duration
}

// Applied is the result of Apply.
type Applied struct {
	Revision   int64
	Generation uint64
	// Status is "active", or "pending_confirm" when the change needs confirmation.
	Status          string
	ConfirmDeadline time.Time
	Duration        time.Duration
}

type applyReply struct {
	res Applied
	err error
}

type cmdApply struct {
	rev   int64
	opts  ApplyOptions
	reply chan applyReply
}

type cmdConfirm struct {
	rev   int64
	reply chan error
}

type cmdRollback struct {
	rev    int64
	reason string
	reply  chan error
}

type cmdObserve struct {
	host  compiler.Host
	reply chan struct{}
}

type cmdBarrier struct{ reply chan *Snapshot }

type cmdTimeout struct{ rev int64 }

type cmdRetry struct{}

// retryDelay is how long the owner waits before it tries a failed apply or rollback again.
const retryDelay = 10 * time.Second

func (cmdApply) command()    {}
func (cmdConfirm) command()  {}
func (cmdRollback) command() {}
func (cmdObserve) command()  {}
func (cmdBarrier) command()  {}
func (cmdTimeout) command()  {}
func (cmdRetry) command()    {}

// applyResult is what the apply loop reports about one desired state.
type applyResult struct {
	d      *desired
	target *compiler.Target
	err    error
	took   time.Duration
}

// inflight is a revision apply the state owner waits for.
type inflight struct {
	cmd     cmdApply
	d       *desired
	started time.Time
	cfg     *model.Configuration
	// failure is set when the apply failed and the previous revision is being restored;
	// restoreGen is the generation of that restore.
	failure    error
	restoreGen uint64
}

type owner struct {
	e   *Engine
	gen uint64
	// committed is the active revision: what the kernel runs when nothing else is going on.
	committed *desired
	// current is what the apply loop was told to converge to.
	current *desired
	host    compiler.Host
	// pending is the revision that waits for confirmation; the kernel runs it.
	pending *pendingState
	timer   clock.Timer

	running *inflight
	queue   []cmdApply
	// barriers wait for the apply of the latest generation.
	barriers []barrier

	// outbox holds replies that are sent after the snapshot is published: a caller that gets its
	// answer must see the state it describes
	outbox   []func()
	retrying bool
	// settled is the highest generation the apply loop has reported on, successfully or not
	settled uint64
	snap    Snapshot
	lastApp *compiler.Target
	problem map[string]bool
}

type pendingState struct {
	d        *desired
	previous int64
	deadline time.Time
}

type barrier struct {
	gen   uint64
	reply chan *Snapshot
}

func (e *Engine) runOwner(ctx context.Context, init *ownerInit) error {
	o := &owner{e: e, host: init.host, problem: map[string]bool{}}
	o.snap.Host = init.host
	if init.config != nil {
		o.gen = 1
		o.committed = &desired{Config: init.config, Revision: init.revision, Host: init.host, Generation: o.gen}
		o.current = o.committed
		o.snap.Revision, o.snap.Config, o.snap.Generation = init.revision, init.config, o.gen
		e.desired.Store(o.committed)
		e.signal()
	}
	o.publish()
	for {
		select {
		case <-ctx.Done():
			o.shutdown()
			return ctx.Err()
		case c := <-e.cmds:
			o.handle(ctx, c)
		case r := <-e.results:
			o.result(ctx, r)
		}
	}
}

func (o *owner) shutdown() {
	if o.timer != nil {
		o.timer.Stop()
	}
}

// armTimeout makes the owner roll the revision back when the confirmation window runs out. The
// timer's callback hands the command over from a goroutine of its own, so it is never lost when the
// owner is busy.
func (o *owner) armTimeout(rev int64, d time.Duration) {
	e := o.e
	o.timer = e.cfg.Clock.AfterFunc(d, func() {
		go func() {
			select {
			case e.cmds <- cmdTimeout{rev: rev}:
			case <-e.ctx.Done():
			}
		}()
	})
}

// retry applies the desired state again after a failure that nobody is waiting for.
func (o *owner) retry() {
	o.retrying = false
	if o.snap.LastError == "" || o.running != nil || o.current == nil {
		return
	}
	o.converge(o.nextDesired(o.current.Config, o.current.Revision))
}

func (o *owner) scheduleRetry() {
	if o.retrying {
		return
	}
	o.retrying = true
	e := o.e
	o.e.cfg.Clock.AfterFunc(retryDelay, func() {
		go func() {
			select {
			case e.cmds <- cmdRetry{}:
			case <-e.ctx.Done():
			}
		}()
	})
}

func (o *owner) later(f func()) { o.outbox = append(o.outbox, f) }

func (o *owner) flush() {
	for _, f := range o.outbox {
		f()
	}
	o.outbox = nil
}

func (o *owner) now() time.Time { return o.e.cfg.Clock.Now() }

func (o *owner) publish() {
	s := o.snap
	s.Host = o.host
	s.Generation = o.gen
	if o.pending != nil {
		s.Pending = &PendingInfo{Revision: o.pending.d.Revision, Previous: o.pending.previous, Deadline: o.pending.deadline}
	} else {
		s.Pending = nil
	}
	o.e.snap.Store(&s)
}

func (o *owner) event(typ string, data map[string]any) { o.e.events.publish(o.now(), typ, data) }

// converge tells the apply loop to bring the kernel to d.
func (o *owner) converge(d *desired) {
	o.current = d
	o.e.desired.Store(d)
	o.e.signal()
}

// nextDesired returns a desired state with a fresh generation for the given configuration.
func (o *owner) nextDesired(cfg *model.Configuration, rev int64) *desired {
	o.gen++
	return &desired{Config: cfg, Revision: rev, Host: o.host, Generation: o.gen}
}

func (o *owner) handle(ctx context.Context, c command) {
	defer func() { o.publish(); o.flush() }()
	switch c := c.(type) {
	case cmdApply:
		if o.running != nil {
			o.queue = append(o.queue, c)
			return
		}
		o.startApply(c)
	case cmdConfirm:
		err := o.confirm(c.rev)
		o.later(func() { c.reply <- err })
	case cmdRollback:
		err := o.rollback(c.rev, c.reason)
		o.later(func() { c.reply <- err })
	case cmdTimeout:
		if o.pending != nil && o.pending.d.Revision == c.rev {
			if err := o.rollback(c.rev, "confirmation timeout"); err != nil {
				// the rollback must happen: try again shortly
				o.e.cfg.Log.Error("cannot roll back the unconfirmed revision", "revision", c.rev, "error", err)
				o.armTimeout(c.rev, retryDelay)
			}
		}
	case cmdRetry:
		o.retry()
	case cmdObserve:
		o.observe(c.host)
		o.later(func() { c.reply <- struct{}{} })
	case cmdBarrier:
		o.barriers = append(o.barriers, barrier{gen: o.gen, reply: c.reply})
		o.releaseBarriers()
	}
}

// observe takes a changed host into the desired state: the next apply compiles the latest
// observed state, so an apply can never restore an outdated address (plan §2.3).
func (o *owner) observe(h compiler.Host) {
	if hostEqual(o.host, h) {
		return
	}
	o.host = h
	if o.current == nil {
		o.publish() // nothing is applied yet: the host is only remembered
		return
	}
	d := o.nextDesired(o.current.Config, o.current.Revision)
	o.converge(d)
	o.event(EventObservedChanged, map[string]any{"generation": d.Generation})
	o.publish()
}

// fail answers an apply that did not start and goes on with the next one in the queue.
func (o *owner) fail(c cmdApply, err error) {
	o.later(func() { c.reply <- applyReply{err: err} })
	o.startQueued()
}

func (o *owner) startApply(c cmdApply) {
	e := o.e
	rev, cfg, err := e.cfg.Store.Get(c.rev)
	if err != nil {
		o.fail(c, err)
		return
	}
	if p, ok := e.cfg.Store.PendingConfirm(); ok {
		o.fail(c, &store.ErrConfirmPending{Pending: p.Revision})
		return
	}
	if string(rev.Status) != store.StatusCandidate {
		o.fail(c, &store.ErrNotACandidate{ID: c.rev, Status: string(rev.Status), Want: store.StatusCandidate})
		return
	}
	var base int64 // a candidate of the first revision has no base
	if rev.Base != nil {
		base = *rev.Base
	}
	if active := e.cfg.Store.ActiveID(); base != active {
		o.fail(c, &store.ErrRevisionConflict{Active: active})
		return
	}
	d := o.nextDesired(cfg, c.rev)
	o.running = &inflight{cmd: c, d: d, started: o.now(), cfg: cfg}
	o.converge(d)
	o.publish()
}

// result handles what the apply loop reports.
func (o *owner) result(ctx context.Context, r applyResult) {
	if r.d.Generation > o.settled {
		o.settled = r.d.Generation
	}
	// the snapshot's applied state follows every verified apply, whatever caused it
	if r.err == nil {
		o.snap.Applied = &AppliedInfo{Generation: r.d.Generation, Revision: r.d.Revision, Hash: r.target.Hash, At: o.now(), Uplink: r.target.Uplink}
		o.snap.LastError = ""
		o.snap.Problems = r.target.Problems
		o.problemEvents(r.target)
		if o.lastApp != nil && o.lastApp.Uplink != r.target.Uplink {
			o.event(EventUplinkChanged, map[string]any{"old": o.lastApp.Uplink, "new": r.target.Uplink})
		}
		o.lastApp = r.target
		o.event(EventApplied, map[string]any{"generation": r.d.Generation, "revision": r.d.Revision, "hash": r.target.Hash})
	} else {
		o.snap.LastError = r.err.Error()
		o.event(EventApplyFailed, map[string]any{"generation": r.d.Generation, "revision": r.d.Revision, "error": r.err.Error()})
	}

	if run := o.running; run != nil {
		switch {
		case run.failure == nil && r.d.Generation >= run.d.Generation && r.err == nil:
			o.finishApply(run, r)
		case run.failure == nil && r.d.Generation >= run.d.Generation:
			// the apply failed: restore the committed revision, then tell the caller
			if o.committed == nil {
				o.running = nil
				o.current = nil // nothing committed: later host changes must not apply the failed candidate
				o.e.desired.Store(nil)
				o.later(func() { run.cmd.reply <- applyReply{err: &ErrApplyFailed{Revision: run.cmd.rev, Cause: r.err}} })
				o.startQueued()
				break
			}
			run.failure = r.err
			rd := o.nextDesired(o.committed.Config, o.committed.Revision)
			run.restoreGen = rd.Generation
			o.converge(rd)
		case run.failure != nil && r.d.Generation >= run.restoreGen:
			// the restore has been applied (or failed too): the caller gets the original error
			o.running = nil
			o.later(func() {
				run.cmd.reply <- applyReply{err: &ErrApplyFailed{Revision: run.cmd.rev, Cause: run.failure, Restored: r.err == nil}}
			})
			o.startQueued()
		}
	}
	if r.err != nil && o.running == nil && o.pending == nil {
		o.scheduleRetry()
	}
	o.publish()
	o.releaseBarriers()
	o.flush()
}

func (o *owner) problemEvents(t *compiler.Target) {
	now := map[string]bool{}
	for _, p := range t.Problems {
		if p.Code == compiler.CodePortMissing {
			now[p.Network] = true
			if !o.problem[p.Network] {
				o.event(EventNetworkDegraded, map[string]any{"network": p.Network, "reason": p.Message})
			}
		}
	}
	for n := range o.problem {
		if !now[n] {
			o.event(EventNetworkRecovered, map[string]any{"network": n})
		}
	}
	o.problem = now
}

func (o *owner) finishApply(run *inflight, r applyResult) {
	e := o.e
	c := run.cmd
	took := o.now().Sub(run.started)
	needs := o.committed != nil && !c.opts.SkipConfirm && LockoutRelevant(o.committed.Config, run.cfg)
	if needs {
		timeout := c.opts.ConfirmTimeout
		if timeout <= 0 {
			timeout = confirmTimeout(run.cfg)
		}
		deadline := o.now().Add(timeout)
		prev := e.cfg.Store.ActiveID()
		if _, err := e.cfg.Store.BeginConfirm(run.cmd.rev, o.now(), deadline); err != nil {
			// the kernel runs a revision the store cannot hold in confirmation: restore the committed one
			o.running = nil
			o.converge(o.nextDesired(o.committed.Config, o.committed.Revision))
			o.fail(c, err)
			return
		}
		o.pending = &pendingState{d: run.d, previous: prev, deadline: deadline}
		rev := run.cmd.rev
		o.armTimeout(rev, timeout)
		o.event(EventConfirmPending, map[string]any{"revision": rev, "deadline": deadline})
		o.running = nil
		res := Applied{Revision: rev, Generation: r.d.Generation, Status: "pending_confirm", ConfirmDeadline: deadline, Duration: took}
		o.later(func() { c.reply <- applyReply{res: res} })
		o.startQueued()
		return
	}
	if _, err := e.cfg.Store.Commit(c.rev, o.now()); err != nil {
		// the kernel runs a revision the store refuses to activate: restore the committed one
		o.running = nil
		if o.committed != nil {
			o.converge(o.nextDesired(o.committed.Config, o.committed.Revision))
		}
		o.fail(c, err)
		return
	}
	o.committed = run.d
	o.snap.Revision, o.snap.Config = run.d.Revision, run.d.Config
	o.running = nil
	res := Applied{Revision: c.rev, Generation: r.d.Generation, Status: "active", Duration: took}
	o.later(func() { c.reply <- applyReply{res: res} })
	o.startQueued()
}

func confirmTimeout(cfg *model.Configuration) time.Duration {
	if cfg.Settings != nil && cfg.Settings.CommitConfirmTimeout != nil {
		if d, err := time.ParseDuration(*cfg.Settings.CommitConfirmTimeout); err == nil && d > 0 {
			return d
		}
	}
	return DefaultConfirmTimeout
}

func (o *owner) startQueued() {
	if len(o.queue) == 0 || o.running != nil {
		return
	}
	next := o.queue[0]
	o.queue = o.queue[1:]
	o.startApply(next)
}

func (o *owner) confirm(rev int64) error {
	if o.pending == nil || o.pending.d.Revision != rev {
		return &store.ErrNotACandidate{ID: rev, Status: "not pending", Want: store.StatusPendingConfirm}
	}
	if _, err := o.e.cfg.Store.Confirm(rev, o.now()); err != nil {
		var exp *store.ErrConfirmExpired
		if errors.As(err, &exp) {
			_ = o.rollback(rev, "confirmation expired")
		}
		return err
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	o.committed = o.pending.d
	o.snap.Revision, o.snap.Config = o.committed.Revision, o.committed.Config
	o.pending = nil
	o.event(EventConfirmed, map[string]any{"revision": rev})
	o.publish()
	o.startQueued()
	return nil
}

// rollback ends the waiting for confirmation without confirming: the revision is rolled back and
// the previous one is applied again.
func (o *owner) rollback(rev int64, reason string) error {
	if o.pending == nil || o.pending.d.Revision != rev {
		return &store.ErrNotACandidate{ID: rev, Status: "not pending", Want: store.StatusPendingConfirm}
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	if _, err := o.e.cfg.Store.Rollback(rev, o.now()); err != nil {
		return err
	}
	o.pending = nil
	o.event(EventRolledBack, map[string]any{"revision": rev, "reason": reason})
	if o.committed != nil {
		o.converge(o.nextDesired(o.committed.Config, o.committed.Revision))
	}
	o.publish()
	o.startQueued()
	return nil
}

func (o *owner) releaseBarriers() {
	if len(o.barriers) == 0 {
		return
	}
	applied := uint64(0)
	if o.snap.Applied != nil {
		applied = o.snap.Applied.Generation
	}
	// a barrier is released when the latest generation was applied or definitively failed
	var keep []barrier
	for _, b := range o.barriers {
		if o.settledAtLeast(b.gen, applied) {
			b.reply <- o.e.snap.Load()
		} else {
			keep = append(keep, b)
		}
	}
	o.barriers = keep
}

func (o *owner) settledAtLeast(gen, applied uint64) bool {
	if o.current == nil {
		return true // nothing to apply
	}
	return o.settled >= gen || applied >= gen
}

func hostEqual(a, b compiler.Host) bool { return reflect.DeepEqual(a, b) }
