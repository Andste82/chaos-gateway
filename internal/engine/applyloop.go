package engine

import (
	"context"
	"fmt"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/wireguard"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
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
	// last is the last desired state that was applied and verified, with its target: an identity
	// change is applied against it incrementally
	var last *appliedState
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
			target := compiler.Compile(e.input(d.Config, d.Host, compiler.Generation{Revision: d.Revision, Seq: d.Generation}, d.Identity))
			start := e.cfg.Clock.Monotonic()
			var err error
			incremental := false
			if d.IdentityOnly && last != nil && last.d.Config == d.Config && last.d.Revision == d.Revision && hostEqual(last.d.Host, d.Host) {
				var ierr error
				if incremental, ierr = e.applyIdentity(ctx, last.target, target); ierr != nil {
					e.cfg.Log.Warn("an identity update failed: applying everything", "error", ierr)
					incremental = false
				}
			}
			if !incremental {
				_, err = apply.Apply(ctx, e.cfg.Exec, e.cfg.Namespace, target)
			}
			took := e.cfg.Clock.Monotonic() - start
			if err != nil {
				e.cfg.Log.Warn("apply failed", "generation", d.Generation, "revision", d.Revision, "error", err)
				last = nil
			} else {
				last = &appliedState{d: d, target: target}
			}
			dhcpErr := ""
			if err == nil && !incremental {
				dhcpErr = e.syncDHCP(ctx, target)
			}
			res := applyResult{d: d, target: target, err: err, took: took, dhcpErr: dhcpErr}
			if err == nil && incremental && last != nil {
				res.dhcpErr = e.dhcp.errorNow()
			}
			select {
			case e.results <- res:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// input builds the compiler's input. The public keys of the WireGuard interfaces come from the
// secrets store; a network without one is reported by the compiler.
func (e *Engine) input(cfg *model.Configuration, host compiler.Host, gen compiler.Generation, id *domain.Identity) compiler.Input {
	in := compiler.Input{Config: cfg, Host: host, Generation: gen, Identity: id}
	if e.cfg.Secrets != nil {
		keys, err := wireguard.InterfaceKeys(cfg, e.cfg.Secrets)
		if err != nil {
			e.cfg.Log.Warn("a WireGuard key is missing", "error", err)
		}
		in.Keys = keys
	}
	return in
}

type appliedState struct {
	d      *desired
	target *compiler.Target
}

// errNotIncremental means the change is more than elements of existing sets.
var errNotIncremental = fmt.Errorf("the change is not only set elements")

// applyIdentity updates the device sets of the kernel to the new target with incremental element
// operations (plan §2.3, §3.11): the executor takes them before queued plans and runs them one at a
// time with plans. It reports false (and no error) when the new target needs more than elements: a
// device that has no set yet (a new one) needs the full apply. The result is verified like every
// apply; the generation rule of the chain `generation` keeps naming the last full apply.
func (e *Engine) applyIdentity(ctx context.Context, old, next *compiler.Target) (bool, error) {
	ops, err := identityOps(e.cfg.Namespace, old, next)
	if err != nil {
		return false, nil // a structural change: the full apply handles it
	}
	if len(ops) > 0 {
		if _, err := e.cfg.Exec.Do(ctx, ops...); err != nil {
			return false, err
		}
	}
	// the generation of the rules did not change: verify against the target as it stands in the kernel
	next.Nft.Generation = old.Nft.Generation
	st, err := apply.ReadState(ctx, e.cfg.Exec, e.cfg.Namespace, apply.WantOf(next))
	if err != nil {
		return false, err
	}
	if mm := apply.Verify(next, st); len(mm) > 0 {
		return false, fmt.Errorf("the kernel does not match after an identity update: %s", mm[0])
	}
	return true, nil
}

// identityOps compares the device sets of two targets. It fails with errNotIncremental when the sets
// differ in anything but their elements.
func identityOps(ns string, old, next *compiler.Target) ([]executor.Operation, error) {
	if len(old.DeviceSets) != len(next.DeviceSets) {
		return nil, errNotIncremental
	}
	oldSets := map[string]compiler.SetDef{}
	for _, s := range old.Nft.Sets {
		oldSets[s.Name] = s
	}
	var names []string
	for dev, name := range next.DeviceSets {
		if old.DeviceSets[dev] != name {
			return nil, errNotIncremental
		}
		names = append(names, name)
	}
	sort.Strings(names)
	newSets := map[string]compiler.SetDef{}
	for _, s := range next.Nft.Sets {
		newSets[s.Name] = s
	}
	var ops []executor.Operation
	tg := executor.Target{NS: ns}
	for _, name := range names {
		a, b := oldSets[name], newSets[name]
		if a.Name == "" || b.Name == "" {
			return nil, errNotIncremental
		}
		have := map[string]bool{}
		for _, x := range a.Elements {
			have[x] = true
		}
		want := map[string]bool{}
		for _, x := range b.Elements {
			want[x] = true
		}
		var add, del []string
		for _, x := range b.Elements {
			if !have[x] {
				add = append(add, x)
			}
		}
		for _, x := range a.Elements {
			if !want[x] {
				del = append(del, x)
			}
		}
		if len(add) > 0 {
			ops = append(ops, &executor.NftAddElements{Target: tg, Set: name, Elements: add})
		}
		if len(del) > 0 {
			ops = append(ops, &executor.NftDelElements{Target: tg, Set: name, Elements: del})
		}
	}
	return ops, nil
}
