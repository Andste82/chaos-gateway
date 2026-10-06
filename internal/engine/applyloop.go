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
				} else if !incremental {
					e.cfg.Log.Debug("an identity update needs a full apply", "generation", d.Generation, "reason", identityStructuralChange(last.target, target))
				}
			} else if d.IdentityOnly {
				e.cfg.Log.Debug("an identity update cannot be incremental", "generation", d.Generation, "has_last", last != nil,
					"same_config", last != nil && last.d.Config == d.Config, "same_revision", last != nil && last.d.Revision == d.Revision,
					"same_host", last != nil && hostEqual(last.d.Host, d.Host))
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
	in := compiler.Input{Config: cfg, Host: host, Generation: gen, Identity: id, ServiceNS: e.cfg.ServiceNS, DefaultUIPort: e.cfg.DefaultUIPort}
	if e.cfg.ServiceHolderPID != nil {
		in.ServiceHolderPID = e.cfg.ServiceHolderPID()
		// a holder WatchService last found dead, as opposed to merely not attached yet (M6b-02), is
		// not named: compiling with 0 keeps (or creates) an empty namespace instead of the executor
		// refusing to attach a PID it cannot find, which would otherwise fail every apply and every
		// rollback until something re-creates the holder. A live replacement is still named, so the
		// usual ensure op reattaches it.
		if h := e.Snapshot().ServiceHealth; h != nil && !h.HolderExists {
			in.ServiceHolderPID = 0
		}
	}
	if e.cfg.ServiceHolderNetnsInode != nil {
		in.ServiceHolderNetnsInode = e.cfg.ServiceHolderNetnsInode()
	}
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
	// the generation of the rules did not change: an identity update only ever touches the identity
	// map's elements, so verifying it only needs that map, not the rest of the state (M6a-11).
	next.Nft.Generation = old.Nft.Generation
	rs, err := apply.ReadSets(ctx, e.cfg.Exec, e.cfg.Namespace)
	if err != nil {
		return false, err
	}
	if mm := apply.VerifyIdentityMap(next, rs); len(mm) > 0 {
		return false, fmt.Errorf("the kernel does not match after an identity update: %s", mm[0])
	}
	return true, nil
}

// identityOps compares the identity map of two targets (plan §3.3). It fails with
// errNotIncremental when anything but the map's elements differs: a different map name (a device
// was added or removed, which renumbers every DeviceNums entry) needs the full apply.
func identityOps(ns string, old, next *compiler.Target) ([]executor.Operation, error) {
	if old.IdentityMap == "" || next.IdentityMap == "" || old.IdentityMap != next.IdentityMap {
		return nil, errNotIncremental
	}
	if len(old.DeviceNums) != len(next.DeviceNums) {
		return nil, errNotIncremental
	}
	for dev, num := range next.DeviceNums {
		if old.DeviceNums[dev] != num {
			return nil, errNotIncremental
		}
	}
	oldEl := mapElementsByName(old, old.IdentityMap)
	newEl := mapElementsByName(next, next.IdentityMap)
	have := map[string]string{}
	for _, e := range oldEl {
		have[e.Key] = e.Value
	}
	want := map[string]string{}
	for _, e := range newEl {
		want[e.Key] = e.Value
	}
	var add []executor.NftMapElement
	var del []string
	for k, v := range want {
		if hv, ok := have[k]; !ok {
			add = append(add, executor.NftMapElement{Key: k, Value: v})
		} else if hv != v {
			// a map add refuses a key that already exists, even with a different value: delete it
			// first, in the same incremental request.
			del = append(del, k)
			add = append(add, executor.NftMapElement{Key: k, Value: v})
		}
	}
	for k := range have {
		if _, ok := want[k]; !ok {
			del = append(del, k)
		}
	}
	sort.Slice(add, func(i, j int) bool { return add[i].Key < add[j].Key })
	sort.Strings(del)
	var ops []executor.Operation
	tg := executor.Target{NS: ns}
	// delete before add: a key whose value changed is in both lists, and a map add refuses a key
	// that still exists.
	if len(del) > 0 {
		ops = append(ops, &executor.NftDelMapElements{Target: tg, Map: old.IdentityMap, Keys: del})
	}
	if len(add) > 0 {
		ops = append(ops, &executor.NftAddMapElements{Target: tg, Map: next.IdentityMap, Elements: add})
	}
	return ops, nil
}

func mapElementsByName(t *compiler.Target, name string) []compiler.MapElement {
	for _, m := range t.Nft.Maps {
		if m.Name == name {
			return m.Elements
		}
	}
	return nil
}

// identityStructuralChange says why identityOps refused to update the identity map in place, for the
// debug log: a changed device set renumbers the identity map's values (DeviceNums).
func identityStructuralChange(old, next *compiler.Target) string {
	switch {
	case old.IdentityMap == "" || next.IdentityMap == "":
		return fmt.Sprintf("no identity map (before: %q with %d devices, after: %q with %d devices)", old.IdentityMap, len(old.DeviceNums), next.IdentityMap, len(next.DeviceNums))
	case old.IdentityMap != next.IdentityMap:
		return "another identity map"
	case len(old.DeviceNums) != len(next.DeviceNums):
		return fmt.Sprintf("the number of devices changed from %d to %d", len(old.DeviceNums), len(next.DeviceNums))
	}
	for dev, num := range next.DeviceNums {
		if old.DeviceNums[dev] != num {
			return fmt.Sprintf("device %s was renumbered from %d to %d", dev, old.DeviceNums[dev], num)
		}
	}
	return "unknown"
}
