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
	// prev holds the allocations of the last target that was applied and verified: the next compile
	// hands them back, so a fault that is still there keeps its id (plan §3.3), a size its PMTU mirror
	// table and a device its number in the identity map. Its deviceHigh is the highest device number
	// any compile of this loop sent to the kernel, a failed apply's included: a number one of them may
	// have left in the identity map is not handed to another device (compiler.assignDeviceNums).
	var prev allocation
	// identityFull is when devices that came or went last took a full apply (a new device that changes
	// more than the elements of maps, such as a queue of its own under a rate fault of its network,
	// D18), and identityFullSet whether that has happened in this process; identityT wakes the loop
	// when the window after it ends (identityConvergeWindow).
	var identityFull time.Duration
	var identityFullSet bool
	var identityT clock.Timer
	var identityC <-chan time.Time
	// verified is the last target the kernel was brought to, kept across a failed apply (last is not):
	// the access rules of the next apply are compared with it to find the connections a rule cuts
	// (cut.go). Nil until the first apply of this process, which cuts nothing.
	var verified *compiler.Target
	// cutStuck reports that the window of a cut could not be closed: until the chains are flushed,
	// the cutting rules reset established connections. A full apply flushes them, an incremental one
	// tries to close the window again.
	var cutStuck error
	// the retirer deletes the tc classes of fault ids that no longer exist once the packets queued in
	// them have left (make-before-break, plan §3.2); it runs on the injected clock
	retirer := e.retirer
	var retire clock.Timer
	var retireC <-chan time.Time
	// the flapping faults toggle between applies, on the engine's clock (flap.go)
	flapT := &flapTimer{clk: e.cfg.Clock}
	defer func() {
		if retire != nil {
			retire.Stop()
		}
		if identityT != nil {
			identityT.Stop()
		}
		flapT.stop()
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
		case <-flapT.c:
			flapT.c = nil
			if last == nil {
				continue // nothing is known about the kernel: the next apply that succeeds takes over
			}
			var err error
			if last, err = e.toggleFlaps(ctx, last); err != nil {
				e.cfg.Log.Warn("toggling flapping faults failed: it is tried again", "error", err)
			}
			flapT.arm(e.flap)
			continue
		case <-identityC:
			identityT, identityC = nil, nil
		case <-e.wake:
		}
		for {
			d := e.desired.Load()
			if d == nil || d.Generation == done {
				break
			}
			before := done
			done = d.Generation
			in := e.input(d.Config, d.Host, compiler.Generation{Revision: d.Revision, Seq: d.Generation}, d.Identity, d.Overlays, prev, retirer.IDs())
			e.withPeerEndpoints(ctx, &in)
			target := compiler.Compile(in)
			start := e.cfg.Clock.Monotonic()
			var err error
			var plan *apply.Plan
			incremental := false
			if d.IdentityOnly && last != nil && last.d.Config == d.Config && last.d.Revision == d.Revision && hostEqual(last.d.Host, d.Host) {
				ops, ierr := identityOps(e.cfg.Namespace, last.target, target)
				if ierr != nil && d.Changed <= last.d.Generation && onlyDevicesCameOrWent(last.d.Identity, d.Identity) {
					// devices that came or went need a full apply this time: a burst of them (a host
					// that rotates its MAC address, a scan of the network) takes one full apply per
					// window, not one per observation (M6a-07). An address change of a device that was
					// there before is never held back, and neither is a new device that only adds
					// elements (the identityOps above succeeded), nor a desired state that also
					// carries a change that is more than the identity since last (an overlay written
					// while the loop was busy, whose own desired state the loop skipped).
					if wait := identityFull + identityConvergeWindow - start; identityFullSet && wait > 0 {
						e.cfg.Log.Debug("an identity update needs a full apply: it waits for the window", "generation", d.Generation,
							"reason", identityStructuralChange(last.target, target), "wait", wait)
						done = before // the loop comes back to this desired state, or to a newer one
						if identityT == nil {
							identityT = e.cfg.Clock.NewTimer(wait)
							identityC = identityT.C()
						}
						break
					}
					identityFull, identityFullSet = start, true
				}
				if ierr != nil {
					e.cfg.Log.Debug("an identity update needs a full apply", "generation", d.Generation, "reason", identityStructuralChange(last.target, target))
				} else if incremental, ierr = e.applyIdentity(ctx, ops, last.target, target); ierr != nil {
					e.cfg.Log.Warn("an identity update failed: applying everything", "error", ierr)
					incremental = false
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
			prev.deviceHigh = max(prev.deviceHigh, target.DeviceNumHigh)
			if err != nil {
				e.cfg.Log.Warn("apply failed", "generation", d.Generation, "revision", d.Revision, "error", err)
				last = nil
				flapT.stop()
			} else {
				last = &appliedState{d: d, target: target}
				prev.faultIDs, prev.pmtu, prev.deviceNums = target.FaultIDs, target.PMTUTables, target.DeviceNums
				if !incremental {
					// the flapping faults of the target start (or go on) from here
					e.flap.sync(target, e.cfg.Clock.Monotonic())
					flapT.arm(e.flap)
				}
			}
			dhcpErr := ""
			var cut *cutOutcome
			if err == nil && incremental && cutStuck != nil && target.Access.HasCuts() {
				if cerr := e.closeStuckWindow(ctx, target); cerr != nil {
					e.cfg.Log.Error("the cut window is still open", "error", cerr)
				} else {
					cutStuck = nil
				}
			}
			if err == nil && !incremental {
				cutStuck = nil // the full apply flushed the cut chains
				dhcpErr = e.syncDHCP(ctx, target)
				cut = e.cutExisting(ctx, verified, target)
				if cut != nil && cut.WindowOpen {
					cutStuck = cut.Err
				}
				if cut != nil && cut.Err != nil {
					e.cfg.Log.Warn("cutting the existing connections of an access rule failed: the rules are in force, the connections may live on", "generation", d.Generation, "error", cut.Err)
				}
			}
			if err == nil {
				verified = target
			}
			res := applyResult{d: d, target: target, err: err, took: took, dhcpErr: dhcpErr, plan: plan, cut: cut, cutStuck: cutStuck}
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

// allocation is what a compile hands on to the next one, so that what stays keeps its number: the
// fault ids (plan §3.3), the PMTU mirror tables by size, and the device numbers of the identity map
// with the highest one handed out (compiler.Input.DeviceNums, DeviceNumHigh).
type allocation struct {
	faultIDs   map[string]int
	pmtu       map[int]int
	deviceNums map[string]int
	deviceHigh int
}

// allocationOf is the allocations of the last applied target as the snapshot has them.
func allocationOf(s *Snapshot) allocation {
	return allocation{faultIDs: s.FaultIDs, pmtu: s.PMTUTables, deviceNums: s.DeviceNums, deviceHigh: s.DeviceNumHigh}
}

// input builds the compiler's input. The public keys of the WireGuard interfaces come from the
// secrets store; a network without one is reported by the compiler.
//
// The overlays are the active ones of the desired state (never part of a revision) and prev the
// allocations of the previous apply.
func (e *Engine) input(cfg *model.Configuration, host compiler.Host, gen compiler.Generation, id *domain.Identity, overlays []model.Overlay, prev allocation, retiring []int) compiler.Input {
	in := compiler.Input{Config: cfg, Host: host, Generation: gen, Identity: id, ServiceNS: e.cfg.ServiceNS, DefaultUIPort: e.cfg.DefaultUIPort,
		Overlays: overlays, FaultIDs: prev.faultIDs, PMTUTables: prev.pmtu, DeviceNums: prev.deviceNums, DeviceNumHigh: prev.deviceHigh,
		RetiringIDs: retiring, ClassLimit: e.classLimitOf(cfg), RuleLimit: e.cfg.RuleLimit, FlapPhase: e.flap.phase}
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

// withPeerEndpoints gives the compiler the addresses the WireGuard peers are reached at, when something
// in the input selects a peer's encrypted UDP by its address (tunnel faults, blocked endpoints). They are
// read from the interfaces right before the compile, so a fault that is written answers for the peer's
// address of this moment, not of the last poll. A failed read leaves the input as it is: the peers are
// then reached at their configured endpoints, and the next apply tries again.
func (e *Engine) withPeerEndpoints(ctx context.Context, in *compiler.Input) {
	if !compiler.NeedsPeerEndpoints(in.Config, in.Overlays) {
		return
	}
	eps, err := e.readPeerEndpoints(ctx)
	if err != nil {
		e.cfg.Log.Warn("cannot read the addresses of the WireGuard peers: tunnel faults use the endpoints known", "error", err)
		return
	}
	in.PeerEndpoints = eps
	e.cfg.Log.Debug("the peers are reached at", "endpoints", eps)
}

type appliedState struct {
	d      *desired
	target *compiler.Target
}

// errNotIncremental means the change is more than elements of existing sets.
var errNotIncremental = fmt.Errorf("the change is not only set elements")

// applyIdentity brings the identity and classification maps of the kernel from old to next with the
// incremental element operations identityOps made of them (plan §2.3, §3.11): the executor takes
// them before queued plans and runs them one at a time with plans. The result is verified like every
// apply; the generation rule of the chain `generation` keeps naming the last full apply.
func (e *Engine) applyIdentity(ctx context.Context, ops []executor.Operation, old, next *compiler.Target) (bool, error) {
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
// commit). A device that came or went is the same kind of change, since the others keep their
// numbers (compiler.Input.DeviceNums): its addresses are elements of the identity map, and of the
// classification maps where a fault of its group or network covers it. It fails with
// errNotIncremental when anything but the elements of maps differs: another identity map, a device
// that is numbered differently, a fault that appeared or went (other ids, chains, counters, tc
// classes, such as the queue of its own a new device gets from a rate fault on its network, D18).
func identityOps(ns string, old, next *compiler.Target) ([]executor.Operation, error) {
	if old.IdentityMap == "" && next.IdentityMap == "" && sameFaultStructure(old, next) && noMapElementsDiffer(old, next) {
		// neither target has an identity map (the compiler builds one for every target it finishes):
		// there is nothing in the kernel to update, which must not cost a full apply
		return nil, nil
	}
	if old.IdentityMap == "" || next.IdentityMap == "" || old.IdentityMap != next.IdentityMap {
		return nil, errNotIncremental
	}
	if _, _, renumbered := renumberedDevice(old, next); renumbered {
		return nil, errNotIncremental
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
			PMTU     []compiler.PMTUFault
			TC       *compiler.TCTarget
			IFB      *compiler.IFBTarget
			Classify map[string]string
		}{n, t.Faults, t.FaultIDs, t.PMTU, t.TC, t.IFB, t.ClassifyMaps})
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

// renumberedDevice finds a device that both targets know under different numbers. The compiler
// keeps a device's number while the allocation is handed on (compiler.Input.DeviceNums), so this
// happens only after a compile without it.
func renumberedDevice(old, next *compiler.Target) (dev string, from int, found bool) {
	for d, num := range next.DeviceNums {
		if was, ok := old.DeviceNums[d]; ok && was != num {
			return d, was, true
		}
	}
	return "", 0, false
}

// onlyDevicesCameOrWent reports whether the identity changed by devices that came or went alone:
// every device known before and after has the same addresses. Nothing observed yet (nil) is no
// device.
func onlyDevicesCameOrWent(old, next *domain.Identity) bool {
	if next == nil {
		return false
	}
	if old == nil {
		old = &domain.Identity{}
	}
	came := false
	for dev, now := range next.Addresses {
		was, ok := old.Addresses[dev]
		if !ok {
			came = true
			continue
		}
		if !sameAddrs(was, now) {
			return false
		}
	}
	return came || len(old.Addresses) != len(next.Addresses)
}

// identityStructuralChange says why identityOps refused to update the maps in place, for the debug
// log.
func identityStructuralChange(old, next *compiler.Target) string {
	switch {
	case old.IdentityMap == "" || next.IdentityMap == "":
		return fmt.Sprintf("no identity map (before: %q with %d devices, after: %q with %d devices)", old.IdentityMap, len(old.DeviceNums), next.IdentityMap, len(next.DeviceNums))
	case old.IdentityMap != next.IdentityMap:
		return "another identity map"
	}
	if dev, from, ok := renumberedDevice(old, next); ok {
		return fmt.Sprintf("device %s was renumbered from %d to %d", dev, from, next.DeviceNums[dev])
	}
	if !sameFaultStructure(old, next) {
		return fmt.Sprintf("the faults, their ids, the maps or the tc tree changed (devices before: %d, after: %d)", len(old.DeviceNums), len(next.DeviceNums))
	}
	return "unknown"
}
