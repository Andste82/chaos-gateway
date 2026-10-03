package engine

import (
	"context"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/wireguard"

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
			target := compiler.Compile(e.input(d.Config, d.Host, compiler.Generation{Revision: d.Revision, Seq: d.Generation}))
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

// input builds the compiler's input. The public keys of the WireGuard interfaces come from the
// secrets store; a network without one is reported by the compiler.
func (e *Engine) input(cfg *model.Configuration, host compiler.Host, gen compiler.Generation) compiler.Input {
	in := compiler.Input{Config: cfg, Host: host, Generation: gen}
	if e.cfg.Secrets != nil {
		keys, err := wireguard.InterfaceKeys(cfg, e.cfg.Secrets)
		if err != nil {
			e.cfg.Log.Warn("a WireGuard key is missing", "error", err)
		}
		in.Keys = keys
	}
	return in
}
