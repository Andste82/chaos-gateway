package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
func (e *Engine) Confirm(ctx context.Context, rev int64) error {
	reply := make(chan error, 1)
	if err := e.send(ctx, cmdConfirm{rev: rev, reply: reply}); err != nil {
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
	tg := compiler.Compile(e.input(cfg, snap.Host, compiler.Generation{Revision: rev, Seq: snap.Generation + 1}))
	p.Target, p.Problems = tg, tg.Problems
	if tg.HasErrors() {
		return p, nil
	}
	if tg.Bird != nil {
		// BIRD's own parser has the last word on the configuration, and the preview shows its message
		_, err := e.cfg.Exec.Do(ctx, &executor.Bird{Action: "check", Instance: tg.Bird.Instance, Config: tg.Bird.Text, ImportTables: tg.Bird.ImportTables})
		var be *executor.BirdError
		if err != nil && (errors.As(err, &be) || strings.Contains(err.Error(), "bird: ")) {
			p.Problems = append(p.Problems, compiler.Problem{Severity: compiler.SevError, Code: compiler.CodeRouting, Message: err.Error()})
			return p, nil
		} else if err != nil {
			return nil, err
		}
	}
	state, err := apply.ReadState(ctx, e.cfg.Exec, e.cfg.Namespace, apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads, BirdInstance: compiler.BirdInstance})
	if err != nil {
		return nil, err
	}
	plan, err := apply.BuildPlan(tg, state, e.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	p.Plan = plan.Summary
	p.Linux = apply.Diff(tg, state)
	return p, nil
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
