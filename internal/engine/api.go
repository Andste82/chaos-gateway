package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/Andste82/chaos-gateway/internal/observer"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Apply applies a candidate revision: it is compiled, applied and verified. If that fails the
// previous revision is restored and ErrApplyFailed is returned. A revision that could lock the
// administrator out (LockoutRelevant) waits for Confirm; when the window runs out it is rolled
// back. The call returns when the change is verified, not when it is confirmed.
func (e *Engine) Apply(ctx context.Context, rev int64, opts ApplyOptions) (Applied, error) {
	reply := make(chan applyReply, 1)
	if err := e.send(ctx, cmdApply{rev: rev, opts: opts, reply: reply}); err != nil {
		return Applied{}, err
	}
	r, err := wait(ctx, e, reply)
	if err != nil {
		return Applied{}, err
	}
	return r.res, r.err
}

// Confirm confirms the revision that waits for confirmation.
func (e *Engine) Confirm(ctx context.Context, rev int64, actor model.Actor) error {
	reply := make(chan error, 1)
	if err := e.send(ctx, cmdConfirm{rev: rev, actor: actor, reply: reply}); err != nil {
		return err
	}
	return waitErr(ctx, e, reply)
}

// Rollback rolls the revision that waits for confirmation back now.
func (e *Engine) Rollback(ctx context.Context, rev int64) error {
	reply := make(chan error, 1)
	if err := e.send(ctx, cmdRollback{rev: rev, reason: "requested", reply: reply}); err != nil {
		return err
	}
	return waitErr(ctx, e, reply)
}

func waitErr(ctx context.Context, e *Engine, ch <-chan error) error {
	err, werr := wait(ctx, e, ch)
	if werr != nil {
		return werr
	}
	return err
}

// Observe tells the engine what the host looks like now (an observer found a change). The next
// apply compiles it; the call returns once the owner has taken it.
func (e *Engine) Observe(ctx context.Context, h compiler.Host) error {
	reply := make(chan struct{}, 1)
	if err := e.send(ctx, cmdObserve{host: h, reply: reply}); err != nil {
		return err
	}
	_, err := wait(ctx, e, reply)
	return err
}

// Refresh reads the host through the executor and observes it.
func (e *Engine) Refresh(ctx context.Context) error {
	h, err := apply.ReadHost(ctx, e.cfg.Exec, e.cfg.Namespace)
	if err != nil {
		return fmt.Errorf("read the host: %w", err)
	}
	return e.Observe(ctx, h)
}

// Barrier waits until the desired state of this moment has been applied (or has failed to apply)
// and returns the snapshot. Tests and the boot health check use it.
func (e *Engine) Barrier(ctx context.Context) (*Snapshot, error) {
	reply := make(chan *Snapshot, 1)
	if err := e.send(ctx, cmdBarrier{reply: reply}); err != nil {
		return nil, err
	}
	return wait(ctx, e, reply)
}

// Preview is what applying a candidate revision would do.
type Preview struct {
	Revision int64
	Base     int64
	// Domain is the change in the user's terms.
	Domain []model.DomainChange
	// Linux is the change as unified diffs of the normalized target state.
	Linux apply.LinuxDiff
	// Plan lists the operations an apply would run now, in order.
	Plan []string
	// Problems are what the compiler reports about the target; an error stops the apply.
	Problems []compiler.Problem
	// NeedsConfirmation says that the change could lock the administrator out.
	NeedsConfirmation bool
	// Target is the compiled target.
	Target *compiler.Target
	// Rules are the access rules in the order they are evaluated after the change, overlay rules first;
	// New marks the ones that are not in the kernel in this form yet.
	Rules []PreviewRule
	// References are the active overlays the revision would orphan: applying it needs force, which
	// removes them. The target is compiled without them.
	References []OverlayReference
}

// PreviewRule is a rule of the effective list a preview shows.
type PreviewRule struct {
	compiler.AccessRule
	New bool
}

// previewRules lists the rules of a compiled target and marks those that differ from the applied ones
// (a new rule, a changed selector or action; a new place in the order, or other addresses of the
// same scope, are not a change).
func previewRules(next, applied *compiler.AccessPlan) []PreviewRule {
	if next == nil {
		return nil
	}
	var out []PreviewRule
	for _, r := range next.Rules {
		pr := PreviewRule{AccessRule: r, New: true}
		if old, ok := applied.Rule(r.Key); ok {
			a, b := *old, r
			a.Position, b.Position, a.Sources, b.Sources = 0, 0, nil, nil
			pr.New = !reflect.DeepEqual(a, b)
		}
		out = append(out, pr)
	}
	return out
}

// Preview compiles a candidate against the current snapshot and compares it with the kernel,
// without changing anything (plan §2.14).
func (e *Engine) Preview(ctx context.Context, rev int64) (*Preview, error) {
	snap := e.Snapshot()
	r, cfg, err := e.cfg.Store.Get(rev)
	if err != nil {
		return nil, err
	}
	p := &Preview{Revision: rev}
	if r.Base != nil {
		p.Base = *r.Base
	}
	p.Domain = domain.Diff(snap.Config, cfg)
	if snap.Config == nil {
		p.Domain = domain.Diff(&model.Configuration{}, cfg)
	}
	p.NeedsConfirmation = LockoutRelevant(snap.Config, cfg)
	id := snap.Identity
	// the overlays as the apply would leave them: without the orphans, merged devices moved
	overlays := snap.Overlays
	if snap.Config != nil {
		orphans, merges := revisionImpact(snap.Config, cfg, snap.Overlays, id.Discovered)
		p.References = referencesOf(orphans)
		overlays = overlaysAfter(snap.Overlays, orphans, merges)
	}
	tg := compiler.Compile(e.input(cfg, snap.Host, compiler.Generation{Revision: rev, Seq: snap.Generation + 1}, &id, overlays, snap.FaultIDs, e.retirer.IDs()))
	p.Target, p.Problems = tg, tg.Problems
	p.Rules = previewRules(tg.Access, snap.Access)
	if tg.HasErrors() {
		return p, nil
	}
	if tg.Bird != nil {
		// BIRD's own parser has the last word on the configuration, and the preview shows its message
		_, err := e.cfg.Exec.Do(ctx, &executor.Bird{Action: "check", Instance: tg.Bird.Instance, Config: tg.Bird.Text, ImportTables: tg.Bird.ImportTables})
		var be *executor.BirdError
		switch {
		case errors.As(err, &be):
			p.Problems = append(p.Problems, compiler.Problem{Severity: compiler.SevError, Code: compiler.CodeRouting, Message: err.Error()})
			return p, nil
		case errors.Is(err, executor.ErrNoBirdDir):
			p.Problems = append(p.Problems, compiler.Problem{Severity: compiler.SevError, Code: compiler.CodeRouting, Message: "dynamic routing needs a BIRD directory, but the executor has none (--bird-dir)"})
			return p, nil
		case err != nil:
			return nil, err
		}
	}
	if tg.Kea != nil && e.cfg.DHCP != nil {
		// Kea's own config-test has the last word on a scope: a managed option code or bad option
		// data would otherwise only surface as DHCPError after the apply.
		if err := e.cfg.DHCP.Test(ctx, tg.Kea); err != nil {
			p.Problems = append(p.Problems, compiler.Problem{Severity: compiler.SevError, Code: compiler.CodeDHCP, Message: err.Error()})
			return p, nil
		}
	}
	state, err := apply.ReadState(ctx, e.cfg.Exec, e.cfg.Namespace, apply.WantOf(tg))
	if err != nil {
		return nil, err
	}
	// the plan the apply loop would make: stale classes stay for the grace period, the retirer's memory
	// of the distribution tables decides which leaves are made again
	plan, err := e.retirer.BuildPlan(tg, state, e.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	p.Plan = plan.Summary
	p.Linux = apply.Diff(tg, state)
	return p, nil
}

// FollowNeighbors makes the engine read the observed state when the neighbor table changes (a device
// appeared, changed its address or went): after a burst of events the poller reads once (plan §2.3,
// identity changes take a second at most). It runs until ctx ends.
func (e *Engine) FollowNeighbors(ctx context.Context, debounce time.Duration) error {
	if !e.started {
		return errors.New("engine: not started")
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-e.ctx.Done():
		case <-ctx.Done():
		}
		cancel()
	}()
	trigger, err := observer.WatchNeighbors(ctx, e.cfg.Namespace, e.cfg.Clock, debounce)
	if err != nil {
		cancel()
		return err
	}
	e.sup.Go(ctx, "engine.follow-neighbors", func(ctx context.Context) error {
		for range trigger {
			e.TriggerObserve()
		}
		return nil
	})
	return nil
}

// FollowConntrack makes the engine read the observed state when a connection appears, changes or
// closes (M6a-04): after a burst of events the poller reads once, instead of waiting for its own
// interval. It falls back to that interval alone when the engine's Exec cannot stream (apply.Local,
// some tests); a watch that ends on its own (the executor restarted, the connection broke) is
// reopened after a short backoff, the same way a dropped request connection is elsewhere. It runs
// until ctx ends.
func (e *Engine) FollowConntrack(ctx context.Context, debounce time.Duration) error {
	if !e.started {
		return errors.New("engine: not started")
	}
	w, ok := e.cfg.Exec.(apply.Watcher)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-e.ctx.Done():
		case <-ctx.Done():
		}
		cancel()
	}()
	e.sup.Go(ctx, "engine.follow-conntrack", func(ctx context.Context) error {
		for ctx.Err() == nil {
			events, stop, err := w.Watch(ctx, executor.WhatConntrack, e.cfg.Namespace)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				e.cfg.Log.Warn("cannot watch conntrack; polling alone until it reconnects", "error", err)
				select {
				case <-e.cfg.Clock.After(10 * time.Second):
					continue
				case <-ctx.Done():
					return nil
				}
			}
			e.conntrackWatching.Store(true)
			e.debounceConntrack(ctx, events, debounce)
			stop()
			e.conntrackWatching.Store(false)
			if ctx.Err() != nil {
				return nil
			}
			select {
			case <-e.cfg.Clock.After(time.Second):
			case <-ctx.Done():
				return nil
			}
		}
		return nil
	})
	return nil
}

// debounceConntrack reads from a conntrack watch until it closes or ctx ends, triggering an
// observation once per burst of events (the same debouncing internal/observer does for netlink).
func (e *Engine) debounceConntrack(ctx context.Context, events <-chan json.RawMessage, debounce time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
		}
		t := e.cfg.Clock.NewTimer(debounce)
	burst:
		for {
			select {
			case <-t.C():
				break burst
			case _, ok := <-events:
				if !ok {
					t.Stop()
					e.TriggerObserve()
					return
				}
				t.Stop()
				t = e.cfg.Clock.NewTimer(debounce)
			case <-ctx.Done():
				t.Stop()
				return
			}
		}
		e.TriggerObserve()
	}
}

// FollowHost makes the engine follow changes of the host (the uplink's address and gateway, the
// interfaces behind the configured MACs and names) through netlink events: after a burst of
// events the host is read and handed to the state owner (plan §2.2). It runs until ctx ends.
func (e *Engine) FollowHost(ctx context.Context, debounce time.Duration) error {
	if !e.started {
		return errors.New("engine: not started")
	}
	// the watcher belongs to the engine: it ends with it, and with the caller's context
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-e.ctx.Done():
		case <-ctx.Done():
		}
		cancel()
	}()
	trigger, err := observer.Watch(ctx, e.cfg.Namespace, e.cfg.Clock, debounce)
	if err != nil {
		cancel()
		return err
	}
	e.sup.Go(ctx, "engine.follow-host", func(ctx context.Context) error {
		for range trigger {
			if err := e.Refresh(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				e.cfg.Log.Warn("cannot read the host after a network event", "error", err)
			}
		}
		return nil
	})
	return nil
}
