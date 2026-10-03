package engine

import (
	"context"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
)

func (e *Engine) signal() {
	select {
	case e.wake <- struct{}{}:
	default: // a wake-up is already pending: the loop will see the latest desired state
	}
}

// runApplyLoop compiles the latest desired state, applies it and verifies it. Whatever the state
// owner decided while an apply was running is contained in the next one: the loop always takes the
// newest desired state, so a burst of changes costs one or two applies, not one per change.
func (e *Engine) runApplyLoop(ctx context.Context) error {
	var done uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.wake:
		}
		for {
			d := e.desired.Load()
			if d == nil || d.Generation == done {
				break
			}
			done = d.Generation
			target := compiler.Compile(compiler.Input{
				Config: d.Config, Host: d.Host,
				Generation: compiler.Generation{Revision: d.Revision, Seq: d.Generation},
			})
			start := e.cfg.Clock.Monotonic()
			_, err := apply.Apply(ctx, e.cfg.Exec, e.cfg.Namespace, target)
			took := e.cfg.Clock.Monotonic() - start
			if err != nil {
				e.cfg.Log.Warn("apply failed", "generation", d.Generation, "revision", d.Revision, "error", err)
			}
			select {
			case e.results <- applyResult{d: d, target: target, err: err, took: took}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
