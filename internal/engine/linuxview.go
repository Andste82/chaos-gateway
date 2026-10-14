package engine

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// LinuxView is the kernel state of the applied target as text, read when asked (plan §2.17
// "Technical view": the compiled configuration with its live counters).
type LinuxView struct {
	Revision   int64
	Generation uint64
	At         time.Time
	apply.LinuxText
}

// LinuxView reads the kernel state Chaos Gateway owns through the executor (read operations only)
// and renders it: the nftables table, the tc trees, the bridges, policy rules and routes, and the
// WireGuard interfaces of the last applied target.
func (e *Engine) LinuxView(ctx context.Context) (*LinuxView, error) {
	snap := e.Snapshot()
	st, err := apply.ReadState(ctx, e.cfg.Exec, e.cfg.Namespace, apply.Want{TCDevs: snap.TCDevs})
	if err != nil {
		return nil, err
	}
	bridges := make([]string, 0, len(snap.Bridges))
	for _, b := range snap.Bridges {
		bridges = append(bridges, b.Name)
	}
	wg := make([]string, 0, len(snap.WireGuardInterfaces))
	for _, w := range snap.WireGuardInterfaces {
		wg = append(wg, w.Name)
	}
	return &LinuxView{Revision: snap.Revision, Generation: snap.Generation, At: e.cfg.Clock.Now(), LinuxText: apply.Render(st, bridges, wg)}, nil
}

// Kinds of the objects LinuxObjectView shows.
const (
	LinuxObjectDevice = "device"
	LinuxObjectRule   = "rule"
	LinuxObjectFault  = "fault"
)

// LinuxObject is what the kernel holds for one object of the configuration (plan §2.17 "Show Linux
// view", docs/open-items.md P3-M14-03), read when asked: the kernel objects the compiler made for it, found
// by the names and keys the last applied target gave them.
type LinuxObject struct {
	Revision   int64
	Generation uint64
	At         time.Time
	apply.ObjectText
}

// LinuxObjectView reads the kernel state and cuts it down to one object:
//
//   - a device (its id): the elements of the identity map with the device's numeral, the elements of the
//     classification maps (impairment and MTU) whose source covers one of its addresses, the chains they go
//     to, and the tc classes and netem leaves of the faults that have a queue of their own for the device;
//   - an access rule (the id of a configured rule or of a rule overlay, or the key of the anti-lockout
//     rule): its rule in both hooks with its counter and `last` statement, the named counter and the
//     set of its sources;
//   - a fault (the id of a configured fault or of an overlay): for every fault id the compiler gave it, the
//     chain that writes the id with its counters, the classification elements that go to it, and the tc
//     classes, netem leaves and filters of its queues (a tunnel fault's ingress filter too); for an MTU
//     fault its chain, counters and elements.
//
// An object the applied target gives nothing in the kernel (an unknown id, a disabled or overridden one)
// has no keys and empty texts.
func (e *Engine) LinuxObjectView(ctx context.Context, kind, id string) (*LinuxObject, error) {
	snap := e.Snapshot()
	sel, err := objectSel(snap, kind, id)
	if err != nil {
		return nil, err
	}
	devs := append([]string(nil), snap.TCDevs...)
	if snap.TunnelUplink != "" && len(sel.Flowers) > 0 {
		devs = append(devs, snap.TunnelUplink)
	}
	st, err := apply.ReadState(ctx, e.cfg.Exec, e.cfg.Namespace, apply.Want{TCDevs: devs})
	if err != nil {
		return nil, err
	}
	return &LinuxObject{Revision: snap.Revision, Generation: snap.Generation, At: e.cfg.Clock.Now(), ObjectText: apply.RenderObject(st, sel)}, nil
}

// objectSel names the kernel objects of one object of the snapshot's applied target.
func objectSel(snap *Snapshot, kind, id string) (apply.ObjectSel, error) {
	sel := apply.ObjectSel{Counters: map[string]bool{}, Chains: map[string]bool{}, Sets: map[string]bool{}, Classes: map[string]bool{}, Flowers: map[int]bool{}}
	lower := strings.ToLower(id)
	switch kind {
	case LinuxObjectRule:
		if lower == compiler.AntiLockoutRule.Key {
			sel.Counters[compiler.AntiLockoutRule.Counter] = true
			return sel, nil
		}
		for _, key := range []string{"config:" + lower, "overlay:" + lower} {
			if r, ok := snap.Access.Rule(key); ok {
				sel.Counters[r.Counter] = true
				if r.SourceSet != "" {
					sel.Sets[r.SourceSet] = true
				}
			}
		}
	case LinuxObjectFault:
		marks := map[string]bool{}
		for _, f := range snap.Faults {
			if !strings.EqualFold(f.Source, id) {
				continue
			}
			addFault(sel, f)
			marks[compiler.MarkChainName(f.ID)] = true
		}
		for _, f := range snap.PMTU {
			if !strings.EqualFold(f.Source, id) {
				continue
			}
			sel.Chains[f.Chain] = true
			marks[f.Chain] = true
			for _, c := range []string{f.CounterUp, f.CounterDown, f.CounterDrop} {
				if c != "" {
					sel.Counters[c] = true
				}
			}
		}
		sel.Element = func(_, _, value string) bool { return marks[value] }
	case LinuxObjectDevice:
		did := ""
		for d := range snap.DeviceNums {
			if strings.EqualFold(d, id) {
				did = d
			}
		}
		if did == "" {
			return sel, nil
		}
		num := strconv.Itoa(snap.DeviceNums[did])
		addrs := snap.Identity.Addresses[did]
		sel.Element = func(m, key, value string) bool {
			switch {
			case strings.HasPrefix(m, compiler.IdentityMapBase+"_"):
				return value == num
			case strings.HasPrefix(m, compiler.ClassifyMapBase), strings.HasPrefix(m, compiler.PMTUMapBase):
				src, _, _ := strings.Cut(key, " . ")
				for _, a := range addrs {
					if covers(src, a) {
						return true
					}
				}
			}
			return false
		}
		// the faults that have a queue of their own for the device (a rate, a queue limit, keep order); the
		// elements above name the chains of the others
		for _, f := range snap.Faults {
			if strings.EqualFold(f.Device, did) {
				addFault(sel, f)
			}
		}
	default:
		return sel, fmt.Errorf("unknown kind of object %q", kind)
	}
	return sel, nil
}

// addFault selects a fault id: the chain that writes it with its counters and the tc objects of its queues.
func addFault(sel apply.ObjectSel, f compiler.Fault) {
	sel.Chains[compiler.MarkChainName(f.ID)] = true
	for _, c := range []string{f.CounterUp, f.CounterDown} {
		if c != "" {
			sel.Counters[c] = true
		}
	}
	if f.Upload != nil {
		sel.Classes[compiler.ClassIDOf(f.ID, compiler.Upload)] = true
	}
	if f.Download != nil {
		sel.Classes[compiler.ClassIDOf(f.ID, compiler.Download)] = true
	}
	if f.Tunnel != nil && f.Upload != nil {
		sel.Flowers[f.ID] = true
	}
}

// covers reports whether a key of a map (an address, a prefix "a/n" or a range "a-b", as
// linux.NftSet.Pairs gives it) holds the address.
func covers(key string, a netip.Addr) bool {
	if p, err := netip.ParsePrefix(key); err == nil {
		return p.Contains(a)
	}
	if lo, hi, ok := strings.Cut(key, "-"); ok {
		l, err1 := netip.ParseAddr(lo)
		h, err2 := netip.ParseAddr(hi)
		return err1 == nil && err2 == nil && l.Compare(a) <= 0 && a.Compare(h) <= 0
	}
	x, err := netip.ParseAddr(key)
	return err == nil && x == a
}
