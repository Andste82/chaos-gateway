package apply

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The tc tree in the apply (plan §3.2, M8b). The compiler's tree is the same on every interface it
// is installed on; what differs is what the interface holds now. The plan below compares the two
// per interface, object by object (linux.CompareTC on the normalized form), and orders the
// difference:
//
//   - before the nftables transaction: whatever creates or changes (root, classes, netem leaves,
//     filters). A class of a fault id that is new exists on ALL interfaces before the classification
//     starts to mark packets with it, so no packet is classified into a class that is not there.
//     A class that exists is changed in place (`replace` of its leaf and class): queue, counters and
//     the release time of queued packets stay (docs/development.md, "tc operations on the kernel").
//   - after it: whatever the target no longer wants. A class that nothing selects any more still holds
//     the packets that were queued in it when the transaction switched the classification; they are
//     delivered when the class lives for the largest configured delay plus one second after the
//     switch. The plan therefore does not delete it: it lists it as stale (Plan.Stale) and the caller
//     decides when it goes (Retirer, driven by the engine's clock). Without a retirer the plan
//     deletes it at once (BuildPlan: one-shot applies and tests).
//
// Structural damage (a root of another kind or default, a class whose leaf has another handle or
// kind, a filter that the target does not have) is repaired at once, before the changes: the kernel
// cannot change those in place and the objects are not ones a fault put there.

// TCStale is something the target no longer wants and that still stands.
type TCStale struct {
	Dev string
	// Class is the class ("1:24") whose id is no longer used; its netem leaf and its filters go with
	// it. Empty means the whole tree of Chaos Gateway on the interface (no fault impairs anything,
	// or the interface is no longer one the tree belongs on): the root qdisc goes, and everything
	// below it with it.
	Class string
	// Classes are the classes of the tree at the time (set with a whole tree only), for the fault ids
	// they stand for.
	Classes []string
}

// Key identifies the stale object.
func (s TCStale) Key() string {
	if s.Class == "" {
		return s.Dev + " " + compiler.TCRootHandle
	}
	return s.Dev + " " + s.Class
}

func (s TCStale) String() string {
	if s.Class == "" {
		return "the tree of " + s.Dev
	}
	return "class " + s.Class + " of " + s.Dev
}

// tcBatch is the tc work on one interface.
type tcBatch struct {
	dev     string
	words   []string
	entries []executor.TCEntry
}

// tcOps turns entries into executor operations of at most executor.MaxTCEntries entries each, in
// order: the operations run one after the other, so the order of the entries is kept.
func tcOps(tg executor.Target, entries []executor.TCEntry) []executor.Operation {
	var ops []executor.Operation
	for len(entries) > 0 {
		n := min(len(entries), executor.MaxTCEntries)
		ops = append(ops, &executor.TC{Target: tg, Entries: entries[:n:n]})
		entries = entries[n:]
	}
	return ops
}

// tcPlan is the tc part of a plan.
type tcPlan struct {
	// before are the entries that run before the nftables transaction, one batch per interface:
	// repairs, then changes. The batches are separate operations (an interface at the class limit
	// has about three thousand entries, the executor takes at most executor.MaxTCEntries in one).
	before []tcBatch
	// stale is what the target no longer wants.
	stale []TCStale
	// grace is the time the stale objects live after the switch: the largest delay (with jitter) of
	// any netem leaf, in the kernel or in the target, plus one second.
	grace time.Duration
	// dists is the distribution table each leaf of the target holds after the plan ("dev handle" ->
	// "normal", "" for none); a leaf whose table is not known is not in it.
	dists map[string]string
	// created are the classes ("dev class") whose netem leaf the plan makes new: a leaf that did not
	// exist, or one that is deleted and made again. Its counters start at zero; a leaf that is changed
	// in place keeps them (what the engine's counter epochs follow).
	created []string
	// immediate are the stale objects that go at once because the interface leaves Chaos Gateway's
	// control, or because the apply has no retirer: they are in stale as well.
	immediate map[string]bool
}

// RetireGrace is what is added to the largest configured delay: the time the packets in a retired
// class need to leave it, plus a margin for the packets that were in the stack, already classified,
// when the transaction switched (plan §3.2: "largest configured delay plus 1 s").
const RetireGrace = time.Second

var emptyTree = &linux.NormTree{Qdiscs: []linux.NormQdisc{}, Classes: []linux.NormClass{}, Filters: []linux.NormFilter{}}

func ownTree(s *State, dev string) *linux.NormTree {
	if t := s.TC[dev]; t != nil {
		return t.Subtree(compiler.TCRootHandle)
	}
	return emptyTree
}

// planTC compares the tree of every interface with the target's.
func planTC(t *compiler.Target, s *State, removed []string, mem map[string]string) *tcPlan {
	p := &tcPlan{immediate: map[string]bool{}, dists: map[string]string{}}
	devs := map[string]bool{}
	for _, d := range t.TCCandidates() {
		devs[d] = true
	}
	for d := range s.TC {
		devs[d] = true
	}
	names := make([]string, 0, len(devs))
	for d := range devs {
		names = append(names, d)
	}
	sort.Strings(names)

	wantsTree := map[string]*compiler.TCTarget{}
	var maxDelay float64
	for _, tr := range t.TCTrees() {
		for _, d := range tr.Devs {
			wantsTree[d] = tr
		}
		for _, c := range tr.Classes {
			maxDelay = max(maxDelay, linux.NetemTime(c.Netem.Delay+c.Netem.Jitter))
		}
	}
	for _, d := range names {
		for _, q := range ownTree(s, d).Qdiscs {
			if q.Netem != nil {
				maxDelay = max(maxDelay, q.Netem.Delay+q.Netem.Jitter)
			}
		}
	}
	p.grace = time.Duration(maxDelay*float64(time.Second)) + RetireGrace

	for _, dev := range names {
		live := ownTree(s, dev)
		l, ok := s.Links[dev]
		if tr := wantsTree[dev]; tr != nil {
			p.planDev(tr, dev, live, mem)
			continue
		}
		if len(live.Qdiscs) == 0 {
			continue
		}
		st := TCStale{Dev: dev}
		for _, c := range live.Classes {
			st.Classes = append(st.Classes, c.ID)
		}
		p.stale = append(p.stale, st)
		if contains(removed, dev) {
			// the interface leaves Chaos Gateway's control in this apply: nobody can delete it later
			// (the executor's scope no longer covers it). A bridge or WireGuard interface that is
			// deleted takes its tree along.
			if ok && l.Kind() != "bridge" && l.Kind() != "wireguard" && l.Kind() != "veth" && l.Kind() != "ifb" {
				p.immediate[st.Key()] = true
			} else {
				p.stale = p.stale[:len(p.stale)-1]
			}
		}
	}
	return p
}

// planDev plans one interface that is to hold the tree.
func (p *tcPlan) planDev(tc *compiler.TCTarget, dev string, live *linux.NormTree, mem map[string]string) {
	want := tc.Norm(dev)
	var repair, change []executor.TCEntry
	var words []string
	var created, changed int

	liveClass := map[string]linux.NormClass{}
	for _, c := range live.Classes {
		liveClass[c.ID] = c
	}
	liveLeaf := map[string]linux.NormQdisc{}
	var root *linux.NormQdisc
	for i, q := range live.Qdiscs {
		switch {
		case q.Handle == compiler.TCRootHandle && q.Parent == "root":
			root = &live.Qdiscs[i]
		default:
			liveLeaf[q.Parent] = q
		}
	}
	liveFilter := map[string]linux.NormFilter{}
	for _, f := range live.Filters {
		liveFilter[f.Key()] = f
	}
	wantClass := map[string]linux.NormClass{}
	for _, c := range want.Classes {
		wantClass[c.ID] = c
	}
	wantLeaf := map[string]linux.NormQdisc{}
	for _, q := range want.Qdiscs {
		wantLeaf[q.Parent] = q
	}
	wantFilter := map[string]linux.NormFilter{}
	wantFilterOf := map[string]linux.NormFilter{}
	for _, f := range want.Filters {
		wantFilter[f.Key()] = f
		wantFilterOf[f.Flowid] = f
	}
	deleteFilter := func(f linux.NormFilter) executor.TCEntry { return deleteFilterEntry(dev, f) }

	// the root: HTB cannot be changed in place, so a root of another kind or default class is
	// taken away and made again
	if root != nil && (root.Kind != "htb" || root.HTB == nil || root.HTB.Default != compiler.TCDefaultMinor) {
		repair = append(repair, executor.TCEntry{Object: "qdisc", Action: "delete", Dev: dev, Parent: "root", Handle: compiler.TCRootHandle})
		words = append(words, dev+": the root qdisc is not Chaos Gateway's (it is replaced)")
		root, live = nil, emptyTree
		liveClass, liveLeaf, liveFilter = map[string]linux.NormClass{}, map[string]linux.NormQdisc{}, map[string]linux.NormFilter{}
	}
	if root == nil {
		// `replace` creates the root and takes over whatever root the host put there (an mq, a
		// noqueue); `add` fails on the former
		change = append(change, executor.TCEntry{Object: "qdisc", Action: "replace", Dev: dev, Parent: "root", Handle: compiler.TCRootHandle,
			Args: []string{"htb", "default", fmt.Sprintf("%x", compiler.TCDefaultMinor)}})
		created++
	}

	// the classes of the target, in the compiler's order (the default class first)
	defID := fmt.Sprintf("1:%x", compiler.TCDefaultMinor)
	classEntry := func(id string) executor.TCEntry {
		return executor.TCEntry{Object: "class", Action: "replace", Dev: dev, Parent: compiler.TCRootHandle, ClassID: id,
			Args: []string{"htb", "rate", compiler.TCClassRate, "quantum", compiler.TCClassQuantum}}
	}
	if lc, ok := liveClass[defID]; !ok {
		change = append(change, classEntry(defID))
		created++
	} else if lc.Line() != wantClass[defID].Line() {
		change = append(change, classEntry(defID))
		changed++
	}
	for _, c := range tc.Classes {
		id := c.ClassID()
		wc := wantClass[id]
		lc, have := liveClass[id]
		ll, haveLeaf := liveLeaf[id]
		// a class that is not what the kernel can change in place: another parent or kind, or a
		// leaf that is not the compiler's netem
		if have && (lc.Parent != wc.Parent || lc.Kind != wc.Kind || haveLeaf && (ll.Handle != c.LeafHandle() || ll.Kind != "netem")) {
			for _, f := range live.Filters {
				if f.Flowid == id {
					repair = append(repair, deleteFilter(f))
					delete(liveFilter, f.Key())
				}
			}
			repair = append(repair, executor.TCEntry{Object: "class", Action: "delete", Dev: dev, ClassID: id})
			words = append(words, dev+": class "+id+" is not the compiler's (it is made again)")
			have, haveLeaf = false, false
		}
		switch {
		case !have:
			change = append(change, classEntry(id))
			created++
		case lc.Line() != wc.Line():
			change = append(change, classEntry(id))
			changed++
		}
		wl := wantLeaf[id]
		leafEntry := executor.TCEntry{Object: "qdisc", Action: "replace", Dev: dev, Parent: id, Handle: c.LeafHandle(), Args: c.Config().Args()}
		dkey := dev + " " + c.LeafHandle()
		last, known := mem[dkey]
		dist := c.Netem.Distribution
		// The listing does not show a distribution table, so a change of the table alone is
		// invisible in it: the table the apply last gave the leaf is what tells (P2-M8b-02). A table
		// only shapes a jitter, so for a target without one the table does not matter.
		tableDiffers := haveLeaf && known && last != dist && (dist != "" || c.Netem.Jitter > 0)
		switch {
		case !haveLeaf:
			change = append(change, leafEntry)
			created++
			p.created = append(p.created, dev+" "+id)
			p.dists[dkey] = dist
		case ll.Line() != wl.Line() || tableDiffers:
			// A change that names no table keeps the old one, so a leaf that is to be uniform (and has a
			// jitter) while it may hold a table is made again: the one update that drops a queue. A leaf
			// whose table the apply does not know (it did not put it there) is treated as one that has a
			// table. A leaf that is to have a table gets it in place.
			switch {
			case dist == "" && c.Netem.Jitter > 0 && (!known || last != ""):
				repair = append(repair, executor.TCEntry{Object: "qdisc", Action: "delete", Dev: dev, Parent: id, Handle: c.LeafHandle()})
				words = append(words, dev+": leaf "+c.LeafHandle()+" is made again (it may hold a distribution table, the target has none)")
				p.created = append(p.created, dev+" "+id)
				p.dists[dkey] = ""
			case dist != "":
				p.dists[dkey] = dist
			case known:
				p.dists[dkey] = last // the table stays through a change that names none
			}
			change = append(change, leafEntry)
			changed++
		case known:
			p.dists[dkey] = last
		}
		wf := wantFilterOf[id]
		filterEntry := c.FilterEntry(dev)
		if lf, ok := liveFilter[wf.Key()]; !ok {
			change = append(change, filterEntry)
			created++
		} else if lf.Line() != wf.Line() {
			change = append(change, filterEntry)
			changed++
		}
	}

	// what the target does not have: a class (with its leaf and filters) that no id uses any more is
	// stale and goes later; a filter that selects no class of the target and no stale one is not a
	// fault's and goes now
	staleClass := map[string]bool{}
	for _, c := range live.Classes {
		if _, ok := wantClass[c.ID]; !ok {
			staleClass[c.ID] = true
			p.stale = append(p.stale, TCStale{Dev: dev, Class: c.ID})
		}
	}
	for _, f := range live.Filters {
		if _, ok := wantFilter[f.Key()]; ok || staleClass[f.Flowid] {
			continue
		}
		if _, ok := liveFilter[f.Key()]; !ok {
			continue // deleted above with its class
		}
		repair = append(repair, deleteFilter(f))
		words = append(words, dev+": filter "+f.Key()+" is not the target's (it goes)")
	}

	if created+changed > 0 {
		words = append(words, fmt.Sprintf("%s: %d objects created, %d changed in place", dev, created, changed))
	}
	if entries := append(repair, change...); len(entries) > 0 {
		p.before = append(p.before, tcBatch{dev: dev, words: words, entries: entries})
	}
}

// deleteFilterEntry is the command that deletes a filter of the own tree as the listing shows it: an fw
// filter is named by its mark, a flower filter by its handle.
func deleteFilterEntry(dev string, f linux.NormFilter) executor.TCEntry {
	e := executor.TCEntry{Object: "filter", Action: "delete", Dev: dev, Parent: f.Parent,
		Args: []string{"protocol", f.Protocol, "prio", strconv.Itoa(f.Pref), f.Kind}}
	if f.Flower != nil {
		e.Handle = strconv.Itoa(f.Flower.Handle)
		if f.Parent == "ingress" {
			e.Parent = compiler.IngressHandle
		}
	} else {
		e.Handle = fmt.Sprintf("0x%05x/0x%05x", f.Mark, f.Mask)
	}
	return e
}

// staleEntries are the entries that delete stale objects, from the tree of the interface as it is
// now: the filters that select a class, then the class (its leaf goes with it); for a whole tree the
// root qdisc.
func staleEntries(st TCStale, live *linux.NormTree) []executor.TCEntry {
	if st.Class == "" {
		return []executor.TCEntry{{Object: "qdisc", Action: "delete", Dev: st.Dev, Parent: "root", Handle: compiler.TCRootHandle}}
	}
	var es []executor.TCEntry
	for _, f := range live.Filters {
		if f.Flowid == st.Class {
			es = append(es, deleteFilterEntry(st.Dev, f))
		}
	}
	return append(es, executor.TCEntry{Object: "class", Action: "delete", Dev: st.Dev, ClassID: st.Class})
}

// verifyTC compares the trees the state holds with the target's: every interface that is to hold
// the tree has exactly it, every other one holds none of Chaos Gateway's. What the apply left
// standing for the delayed deletion (State.TCRetiring) is accepted as unexpected.
func verifyTC(t *compiler.Target, s *State, bad func(sub, format string, a ...any)) {
	devs := map[string]bool{}
	for _, d := range t.TCCandidates() {
		devs[d] = true
	}
	for d := range s.TC {
		devs[d] = true
	}
	wantsTree := map[string]*compiler.TCTarget{}
	for _, tr := range t.TCTrees() {
		for _, d := range tr.Devs {
			wantsTree[d] = tr
		}
	}
	names := make([]string, 0, len(devs))
	for d := range devs {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, dev := range names {
		want := emptyTree
		if tr := wantsTree[dev]; tr != nil {
			want = tr.Norm(dev)
		}
		for _, d := range linux.CompareTC(want, ownTree(s, dev)) {
			if d.Kind == "unexpected" && (s.TCRetiring[dev+" "+compiler.TCRootHandle] || d.Class != "" && s.TCRetiring[dev+" "+d.Class]) {
				continue
			}
			bad("tc", "%s: %s", dev, d)
		}
	}
	ingressProblems(t, s, bad)
}
