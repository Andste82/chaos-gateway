package apply

import (
	"fmt"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The ingress side of the tunnel faults (plan §2.2.1, M10): the ingress qdisc of the uplink with one flower
// filter per fault, each redirecting the encrypted UDP of a peer to the IFB device. The interface is
// the host's (the uplink is OS-owned), so the plan is careful with what it did not put there:
//
//   - a filter is Chaos Gateway's when it is a flower filter whose action redirects to the IFB; every
//     other filter of the ingress qdisc is the host's and stays as it is,
//   - the ingress qdisc is made when it is missing (`replace` takes over one that exists) and deleted
//     again only when it held filters of ours and nothing else is left on it,
//   - the filters are changed in place (`replace` of the same handle) and only when the selector differs:
//     the counters of the redirect action restart with a changed filter.
//
// The changes stand between the tc classes that are created or changed and the nftables transaction: the
// IFB classes exist before the first packet is redirected, and the filters are the switch of the
// direction from the peer, like the transaction is the switch of the direction towards it. A class that
// loses its filter stays for the grace period of the make-before-break (retire.go).

// ownIngressFilters are the filters of the ingress qdisc that are Chaos Gateway's: flower filters that
// redirect to the IFB.
func ownIngressFilters(ing *linux.NormTree) []linux.NormFilter {
	var out []linux.NormFilter
	for _, f := range ing.Filters {
		if f.Flower != nil && f.Flower.Redirect == compiler.IFBDev {
			out = append(out, f)
		}
	}
	return out
}

// ingressDevs lists the interfaces whose ingress side the plan looks at: the uplink while the target
// has an IFB, and every interface that holds a filter of ours.
func ingressDevs(t *compiler.Target, s *State) []string {
	seen := map[string]bool{}
	var out []string
	if t.IFB != nil && t.IFB.TC != nil {
		seen[t.IFB.Uplink] = true
		out = append(out, t.IFB.Uplink)
	}
	for dev, tree := range s.TC {
		if !seen[dev] && len(ownIngressFilters(tree.Ingress())) > 0 {
			seen[dev] = true
			out = append(out, dev)
		}
	}
	sort.Strings(out)
	return out
}

// planIngress returns the tc entries that bring the ingress side to the target, with what they do in
// words.
func planIngress(t *compiler.Target, s *State) (entries []executor.TCEntry, words []string, restarted []int) {
	for _, dev := range ingressDevs(t, s) {
		live := emptyTree
		if tree := s.TC[dev]; tree != nil {
			live = tree.Ingress()
		}
		hasQdisc := len(live.Qdiscs) > 0
		ours := ownIngressFilters(live)
		foreign := len(live.Filters) - len(ours)
		liveBy := map[string]linux.NormFilter{}
		for _, f := range ours {
			liveBy[f.Key()] = f
		}
		wanted := t.IFB != nil && t.IFB.TC != nil && t.IFB.Uplink == dev
		if !wanted {
			for _, f := range ours {
				entries = append(entries, deleteFilterEntry(dev, f))
			}
			if len(ours) > 0 && foreign == 0 && hasQdisc {
				entries = append(entries, executor.TCEntry{Object: "qdisc", Action: "delete", Dev: dev, Parent: "ingress"})
			}
			if len(ours) > 0 {
				words = append(words, fmt.Sprintf("%s: %d ingress filters of the tunnel faults go", dev, len(ours)))
			}
			continue
		}
		if !hasQdisc {
			entries = append(entries, executor.TCEntry{Object: "qdisc", Action: "replace", Dev: dev, Parent: "ingress"})
		}
		var made, changed int
		keep := map[string]bool{}
		for _, c := range t.IFB.TC.Classes {
			wf := t.IFB.IngressNormFilter(c)
			keep[wf.Key()] = true
			lf, ok := liveBy[wf.Key()]
			switch {
			case !ok:
				entries = append(entries, t.IFB.IngressFilter(c))
				made++
				restarted = append(restarted, c.ID)
			case lf.Line() != wf.Line():
				entries = append(entries, t.IFB.IngressFilter(c))
				changed++
				restarted = append(restarted, c.ID)
			}
		}
		var gone int
		for _, f := range ours {
			if !keep[f.Key()] {
				entries = append(entries, deleteFilterEntry(dev, f))
				gone++
			}
		}
		if !hasQdisc || made+changed+gone > 0 {
			words = append(words, fmt.Sprintf("%s: ingress qdisc, %d filters created, %d changed in place, %d deleted", dev, made, changed, gone))
		}
	}
	return entries, words, restarted
}

// ingressProblems compares the ingress side of the state with the target's: what the target wants is
// there, and no filter of ours is left over. The host's own filters are not looked at.
func ingressProblems(t *compiler.Target, s *State, bad func(sub, format string, a ...any)) {
	for _, dev := range ingressDevs(t, s) {
		have := emptyTree
		if tree := s.TC[dev]; tree != nil {
			have = tree.Ingress()
		}
		cmp := &linux.NormTree{Dev: dev, Qdiscs: []linux.NormQdisc{}, Classes: []linux.NormClass{}, Filters: ownIngressFilters(have)}
		want := &linux.NormTree{Dev: dev, Qdiscs: []linux.NormQdisc{}, Classes: []linux.NormClass{}, Filters: []linux.NormFilter{}}
		if t.IFB != nil && t.IFB.TC != nil && t.IFB.Uplink == dev {
			want = t.IFB.IngressNorm()
			cmp.Qdiscs = have.Qdiscs
		}
		for _, d := range linux.CompareTC(want, cmp) {
			bad("tc", "%s ingress: %s", dev, d)
		}
	}
}
