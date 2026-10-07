package compiler

import (
	"fmt"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The wanted tree in the normalized form of `tc -j` output (linux.NormTree), which is what
// verification compares the interface with (M8b). The conversions below say, attribute by attribute,
// what the kernel reports for what the compiler asks for; TestEveryCompiledTCTreeIsAcceptedByTheKernel
// checks them against a real kernel for every compiled scenario.

// tcClassRateBytes is TCClassRate (10 Gbit/s) as the class listing prints it: bytes per second.
const tcClassRateBytes = 1_250_000_000

// Norm is the configuration of the netem qdisc as the listing shows it. Two things are not visible:
// the distribution table (a qdisc keeps its table through a change, so it is not part of the
// comparison; the apply remembers which table it gave a leaf and makes the leaf again to go back to
// uniform), and the flapping phase, which is the fault engine's business and not a netem attribute.
func (n Netem) Norm() linux.NetemSpec {
	s := linux.NetemSpec{
		Limit:         n.Limit,
		Delay:         linux.NetemTime(n.Delay),
		Jitter:        linux.NetemTime(n.Jitter),
		Reorder:       linux.NetemProb(n.Reorder),
		Duplicate:     linux.NetemProb(n.Duplicate),
		Corrupt:       linux.NetemProb(n.Corrupt),
		Rate:          linux.NetemRate(n.Rate),
		ReorderCorr:   0,
		DelayCorr:     0,
		DuplicateCorr: 0,
		CorruptCorr:   0,
	}
	if g := n.Gemodel; g != nil {
		s.Gemodel = &linux.GemodelSpec{P: linux.NetemProb(g.P), R: linux.NetemProb(g.R),
			OneMinusH: linux.NetemProb(g.LossBad), OneMinusK: linux.NetemProb(g.LossGood)}
	} else {
		s.Loss, s.LossCorr = linux.NetemProb(n.Loss), linux.NetemProb(n.LossCorr)
	}
	// tc sets the reorder distance to 1 whenever the reorder probability is not zero
	if s.Reorder > 0 {
		s.Gap = 1
	}
	return s
}

// Norm returns the tree of one interface as the kernel reports it once it is applied: the HTB root
// with its default class, a class per active (id, direction) with its netem leaf, and the fw
// filters. It is the own tree only (linux.NormTree.Subtree("1:") of what the interface holds);
// R2Q and the direct queue length of the root are the kernel's defaults and not compared (Spec).
func (tc *TCTarget) Norm(dev string) *linux.NormTree {
	t := &linux.NormTree{Dev: dev, Qdiscs: []linux.NormQdisc{}, Classes: []linux.NormClass{}, Filters: []linux.NormFilter{}}
	if tc == nil || len(tc.Classes) == 0 {
		return t
	}
	t.Qdiscs = append(t.Qdiscs, linux.NormQdisc{Handle: TCRootHandle, Parent: "root", Kind: "htb",
		HTB: &linux.HTBSpec{Default: TCDefaultMinor}})
	t.Classes = append(t.Classes, linux.NormClass{ID: fmt.Sprintf("1:%x", TCDefaultMinor), Parent: TCRootHandle, Kind: "htb",
		Rate: tcClassRateBytes, Ceil: tcClassRateBytes})
	for _, c := range tc.Classes {
		spec := c.Netem.Norm()
		t.Classes = append(t.Classes, linux.NormClass{ID: c.ClassID(), Parent: TCRootHandle, Kind: "htb", Leaf: c.LeafHandle(),
			Rate: tcClassRateBytes, Ceil: tcClassRateBytes})
		t.Qdiscs = append(t.Qdiscs, linux.NormQdisc{Handle: c.LeafHandle(), Parent: c.ClassID(), Kind: "netem", Netem: &spec})
		t.Filters = append(t.Filters, linux.NormFilter{Parent: TCRootHandle, Protocol: "ip", Pref: 1, Kind: "fw",
			Mark: c.Mark, Mask: MarkMask, Flowid: c.ClassID()})
	}
	return t.Sorted()
}
