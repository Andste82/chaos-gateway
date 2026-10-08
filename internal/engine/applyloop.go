package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
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
	// ids is the allocation of fault ids of the last target that was applied and verified: the next
	// compile hands it back, so a fault that is still there keeps its id (plan §3.3)
	var ids map[string]int
	// verified is the last target the kernel was brought to, kept across a failed apply (last is not):
	// the access rules of the next apply are compared with it to find the connections a rule cuts
	// (cut.go). Nil until the first apply of this process, which cuts nothing.
	var verified *compiler.Target
	// the retirer deletes the tc classes of fault ids that no longer exist once the packets queued in
	// them have left (make-before-break, plan §3.2); it runs on the injected clock
	retirer := e.retirer
	var retire clock.Timer
	var retireC <-chan time.Time
	defer func() {
		if retire != nil {
			retire.Stop()
		}
	}()
	arm := func() {
		if retire != nil {
			retire.Stop()
			retire, retireC = nil, nil
		}
		if d, ok := retirer.Next(); ok {
			retire = e.cfg.Clock.NewTimer(d)
			retireC = retire.C()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retireC:
			retireC = nil
			n, err := retirer.Reap(ctx, e.cfg.Exec, e.cfg.Namespace)
			if err != nil {
				e.cfg.Log.Warn("deleting retired tc classes failed: it is tried again", "error", err)
			} else if n > 0 {
				e.cfg.Log.Debug("retired tc classes deleted", "count", n)
			}
			arm()
			continue
		case <-e.wake:
		}
		for {
			d := e.desired.Load()
			if d == nil || d.Generation == done {
				break
			}
			done = d.Generation
			target := compiler.Compile(e.input(d.Config, d.Host, compiler.Generation{Revision: d.Revision, Seq: d.Generation}, d.Identity, d.Overlays, ids, retirer.IDs()))
			start := e.cfg.Clock.Monotonic()
			var err error
			var plan *apply.Plan
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
				var res *apply.Result
				res, err = apply.ApplyWith(ctx, e.cfg.Exec, e.cfg.Namespace, target, retirer)
				if res != nil {
					plan = res.Plan
				}
				arm()
			}
			took := e.cfg.Clock.Monotonic() - start
			if err != nil {
				e.cfg.Log.Warn("apply failed", "generation", d.Generation, "revision", d.Revision, "error", err)
				last = nil
			} else {
				last = &appliedState{d: d, target: target}
				ids = target.FaultIDs
			}
			dhcpErr := ""
			var cut *cutOutcome
			if err == nil && !incremental {
				dhcpErr = e.syncDHCP(ctx, target)
				cut = e.cutExisting(ctx, verified, target)
				if cut != nil && cut.Err != nil {
					e.cfg.Log.Warn("cutting the existing connections of an access rule failed: the rules are in force, the connections may live on", "generation", d.Generation, "error", cut.Err)
				}
			}
			if err == nil {
				verified = target
			}
			res := applyResult{d: d, target: target, err: err, took: took, dhcpErr: dhcpErr, plan: plan, cut: cut}
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
//
// The overlays are the active ones of the desired state (never part of a revision) and ids the
// allocation of fault ids of the previous apply.
func (e *Engine) input(cfg *model.Configuration, host compiler.Host, gen compiler.Generation, id *domain.Identity, overlays []model.Overlay, ids map[string]int, retiring []int) compiler.Input {
	in := compiler.Input{Config: cfg, Host: host, Generation: gen, Identity: id, ServiceNS: e.cfg.ServiceNS, DefaultUIPort: e.cfg.DefaultUIPort,
		Overlays: overlays, FaultIDs: ids, RetiringIDs: retiring, ClassLimit: e.cfg.ClassLimit, RuleLimit: e.cfg.RuleLimit}
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
	// the generation of the rules did not change: an identity update only ever touches the elements
	// of maps (the identity map and the classification maps that name the same addresses), so
	// verifying it only needs the maps, not the rest of the state (M6a-11).
	next.Nft.Generation = old.Nft.Generation
	rs, err := apply.ReadSets(ctx, e.cfg.Exec, e.cfg.Namespace)
	if err != nil {
		return false, err
	}
	if mm := apply.VerifyMaps(next, rs); len(mm) > 0 {
		return false, fmt.Errorf("the kernel does not match after an identity update: %s", mm[0])
	}
	return true, nil
}

// identityOps compares the maps of two targets (plan §3.3). The identity map is updated with
// element operations of its own; the classification maps, whose elements are keyed by the same
// addresses and so follow a device that gets a new one, in one atomic transaction (an interval map
// cannot take an element that overlaps one that is still there, so deletes and adds must be one
// commit). It fails with errNotIncremental when anything but the elements of maps differs: a
// different map name (a device was added or removed, which renumbers every DeviceNums entry), a
// fault that appeared or went (other ids, chains, counters, tc classes).
func identityOps(ns string, old, next *compiler.Target) ([]executor.Operation, error) {
	if old.IdentityMap == "" && next.IdentityMap == "" && sameFaultStructure(old, next) && noMapElementsDiffer(old, next) {
		// no device is known before or after (the identity map is only compiled for a known device):
		// there is nothing in the kernel to update, which must not cost a full apply
		return nil, nil
	}
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
	if !sameFaultStructure(old, next) {
		return nil, errNotIncremental
	}
	var ops []executor.Operation
	tg := executor.Target{NS: ns}
	var cls []compiler.MapUpdate
	for _, m := range next.Nft.Maps {
		before := mapByName(old, m.Name)
		if before == nil {
			return nil, errNotIncremental
		}
		u := compiler.DiffMap(*before, m)
		if u.Empty() {
			continue
		}
		if m.Name != next.IdentityMap {
			cls = append(cls, u)
			continue
		}
		// delete before add: a key whose value changed is in both lists, and a map add refuses a
		// key that still exists.
		if len(u.Delete) > 0 {
			ops = append(ops, &executor.NftDelMapElements{Target: tg, Map: old.IdentityMap, Keys: u.Delete})
		}
		if len(u.Add) > 0 {
			ops = append(ops, &executor.NftAddMapElements{Target: tg, Map: next.IdentityMap, Elements: toExecElements(u.Add)})
		}
	}
	if len(cls) > 0 {
		tx, err := next.Nft.ElementTransaction(cls)
		if err != nil {
			return nil, errNotIncremental
		}
		ops = append(ops, &executor.NftApply{Target: tg, Ruleset: tx})
	}
	return ops, nil
}

func toExecElements(in []compiler.MapElement) []executor.NftMapElement {
	out := make([]executor.NftMapElement, len(in))
	for i, e := range in {
		out[i] = executor.NftMapElement{Key: e.Key, Value: e.Value}
	}
	return out
}

func mapByName(t *compiler.Target, name string) *compiler.MapDef {
	for i := range t.Nft.Maps {
		if t.Nft.Maps[i].Name == name {
			return &t.Nft.Maps[i]
		}
	}
	return nil
}

// sameFaultStructure reports whether two targets differ in nothing but the elements of maps: the
// same sets, chains and counters, the same maps (name, key, value and flags), the same fault ids
// with the same configurations and the same tc tree.
func sameFaultStructure(old, next *compiler.Target) bool {
	strip := func(t *compiler.Target) string {
		n := t.Nft
		n.Generation = ""
		n.Maps = append([]compiler.MapDef(nil), n.Maps...)
		for i := range n.Maps {
			n.Maps[i].Elements = nil
		}
		b, _ := json.Marshal(struct {
			Nft      compiler.Nft
			Faults   []compiler.Fault
			FaultIDs map[string]int
			TC       *compiler.TCTarget
			Classify map[string]string
		}{n, t.Faults, t.FaultIDs, t.TC, t.ClassifyMaps})
		return string(b)
	}
	return strip(old) == strip(next)
}

// noMapElementsDiffer reports whether no map has other elements in the new target.
func noMapElementsDiffer(old, next *compiler.Target) bool {
	for _, m := range next.Nft.Maps {
		b := mapByName(old, m.Name)
		if b == nil || !compiler.DiffMap(*b, m).Empty() {
			return false
		}
	}
	return true
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
	case !sameFaultStructure(old, next):
		return "the faults, their ids or the tc tree changed"
	}
	for dev, num := range next.DeviceNums {
		if old.DeviceNums[dev] != num {
			return fmt.Sprintf("device %s was renumbered from %d to %d", dev, old.DeviceNums[dev], num)
		}
	}
	return "unknown"
}
