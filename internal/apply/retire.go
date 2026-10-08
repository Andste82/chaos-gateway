package apply

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Make-before-break, the second half (plan §3.2): a tc class that no fault id uses any more is left
// standing when the nftables transaction switches the classification, so the packets queued in it
// are delivered, and is deleted by a Retirer once the largest configured delay plus one second have
// passed on the injected clock.
//
// The Retirer keeps no state that the kernel does not have. What it remembers is when it first saw
// each stale class, nothing else: every apply recomputes the stale set from the live tc state (a
// class that a fault wants again drops out of it at once, with its queue), so an apply that failed
// half-way, a restore of the previous revision or a restart of the gateway leaves nothing behind that
// the next apply does not find. After a restart the grace period starts again from the first apply:
// classes are deleted a little later than strictly needed, never earlier.

const (
	// retireBacklogCap is how long a stale class that still holds packets after its time is given:
	// a netem with a low rate can hold a queue for minutes. After it the class goes with its queue.
	retireBacklogCap = 5 * time.Minute
	// retireGiveUp is how long a deletion that keeps failing is tried before it is forgotten (the
	// next apply finds the class again if it is still there).
	retireGiveUp = 30 * time.Minute
)

type retiree struct {
	TCStale
	first, due time.Duration // monotonic
}

// Retirer deletes stale tc classes after their grace period. It is used from one goroutine at a
// time (the engine's apply loop), apply and reap alternating; the mutex protects Pending and Next
// for the callers that look at it from elsewhere.
type Retirer struct {
	clock clock.Clock
	mu    sync.Mutex
	items map[string]*retiree
	// tables is the distribution table every leaf was last given, by "interface handle": what the
	// kernel's listing cannot show (P2-M8b-02). A leaf that is not in it was not put there by this
	// process, and is treated as one that may hold a table.
	tables map[string]string
}

// NewRetirer returns a Retirer that measures time on the monotonic side of c.
func NewRetirer(c clock.Clock) *Retirer {
	return &Retirer{clock: c, items: map[string]*retiree{}, tables: map[string]string{}}
}

// Retiring is a stale class and the time left until it is deleted.
type Retiring struct {
	TCStale
	In time.Duration
}

// commit takes over the stale set of a plan that has been carried out: the classes it names are
// pending (those seen before keep their time, new ones get the plan's grace), every other one is
// forgotten.
func (r *Retirer) commit(p *Plan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Monotonic()
	items := map[string]*retiree{}
	for _, st := range p.Stale {
		if old, ok := r.items[st.Key()]; ok {
			items[st.Key()] = old
			continue
		}
		// a class of a tree that was waiting as a whole (the last fault went, then another came):
		// the time of the tree is the time of its classes, the new fault's tree is not theirs to keep
		if old := r.treeWith(st); old != nil {
			items[st.Key()] = &retiree{TCStale: st, first: old.first, due: old.due}
			continue
		}
		items[st.Key()] = &retiree{TCStale: st, first: now, due: now + p.Grace}
	}
	r.items = items
	r.tables = p.Dists
}

// treeWith returns the pending whole tree of the interface of a stale class that has the class
// (the tree was read with it): the classes of a tree that a new fault revives one by one keep the time
// their tree was given. Nil for anything else. The caller holds the mutex.
func (r *Retirer) treeWith(st TCStale) *retiree {
	if st.Class == "" {
		return nil
	}
	if t, ok := r.items[TCStale{Dev: st.Dev}.Key()]; ok {
		for _, c := range t.Classes {
			if c == st.Class {
				return t
			}
		}
	}
	return nil
}

// tableUnknown is what the table memory holds for a leaf that a failed apply may have written to: it
// may hold any table, so a leaf that is to be uniform is made again (and one that is to have a table
// gets it) even where the listing shows nothing different.
const tableUnknown = "?"

// failed is called with the plan of an apply that failed while it was carried out. The kernel may
// already hold what the plan wrote to a leaf (a new distribution table, which the listing does not
// show), but the apply did not complete, so the memory must not say what the plan intended: the leaves
// the plan wrote are marked as holding an unknown table. The restore of the previous revision then
// makes a leaf that is to be uniform again.
func (r *Retirer) failed(p *Plan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, op := range p.Ops {
		tc, ok := op.(*executor.TC)
		if !ok {
			continue
		}
		for _, e := range tc.Entries {
			if e.Object == "qdisc" && e.Parent != "root" && e.Dev != "" && e.Handle != "" {
				r.tables[e.Dev+" "+e.Handle] = tableUnknown
			}
		}
	}
}

// dists returns a copy of what is known of the distribution tables of the leaves.
func (r *Retirer) dists() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.tables))
	for k, v := range r.tables {
		out[k] = v
	}
	return out
}

// BuildPlan plans an apply of t in state s the way ApplyWith does with this retirer: the classes the
// target no longer wants stay for the grace period, and a leaf is made again only where the memory
// says a distribution table may be. It changes neither the kernel nor the retirer (a preview).
func (r *Retirer) BuildPlan(t *compiler.Target, s *State, ns string) (*Plan, error) {
	return buildPlan(t, s, ns, true, r.dists())
}

// Pending lists what waits, soonest first.
func (r *Retirer) Pending() []Retiring {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Monotonic()
	var out []Retiring
	for _, it := range r.items {
		out = append(out, Retiring{TCStale: it.TCStale, In: max(it.due-now, 0)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].In != out[j].In {
			return out[i].In < out[j].In
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

// IDs are the fault ids whose classes are still waiting for their deletion: a compile that allocates
// ids avoids them (compiler.Input.RetiringIDs).
func (r *Retirer) IDs() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[int]bool{}
	var out []int
	add := func(class string) {
		if id, _, ok := compiler.ClassIDToFault(class); ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, it := range r.items {
		add(it.Class)
		for _, c := range it.Classes {
			add(c)
		}
	}
	sort.Ints(out)
	return out
}

// Next is the time until the next deletion is due; false when nothing waits.
func (r *Retirer) Next() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Monotonic()
	var next time.Duration
	found := false
	for _, it := range r.items {
		if d := max(it.due-now, 0); !found || d < next {
			next, found = d, true
		}
	}
	return next, found
}

// Reap deletes what is due. A class that still holds queued packets is given more time (up to
// retireBacklogCap after it was first seen), one that is already gone is forgotten. It returns the
// number of classes (or trees) it deleted. The deletion of one interface does not wait for the
// others; the first error is returned after all were tried, and what failed is tried again later.
func (r *Retirer) Reap(ctx context.Context, ex Exec, ns string) (int, error) {
	r.mu.Lock()
	now := r.clock.Monotonic()
	byDev := map[string][]*retiree{}
	for _, it := range r.items {
		if it.due <= now {
			byDev[it.Dev] = append(byDev[it.Dev], it)
		}
	}
	r.mu.Unlock()
	if len(byDev) == 0 {
		return 0, nil
	}
	devs := make([]string, 0, len(byDev))
	for d := range byDev {
		devs = append(devs, d)
	}
	sort.Strings(devs)

	var firstErr error
	deleted := 0
	fail := func(its []*retiree, err error) {
		if firstErr == nil {
			firstErr = err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, it := range its {
			if now-it.first > retireGiveUp {
				delete(r.items, it.Key())
				continue
			}
			it.due = now + RetireGrace
		}
	}
	for _, dev := range devs {
		its := byDev[dev]
		sort.Slice(its, func(i, j int) bool { return its[i].Key() < its[j].Key() })
		out, err := ex.Do(ctx, read(ns, executor.ReadTC, dev))
		if err != nil {
			if gone(err) {
				r.forget(its)
				continue
			}
			fail(its, fmt.Errorf("read the tc state of %s: %w", dev, err))
			continue
		}
		tree, err := decode[*linux.NormTree](out, 0, "tc "+dev)
		if err != nil {
			fail(its, err)
			continue
		}
		live := emptyTree
		if tree != nil {
			live = tree.Subtree(compiler.TCRootHandle)
		}
		var entries []executor.TCEntry
		var going []*retiree
		for _, it := range its {
			if !stillThere(it.TCStale, live) {
				r.forget([]*retiree{it})
				continue
			}
			if queued(it.TCStale, live) && now-it.first < retireBacklogCap {
				r.mu.Lock()
				it.due = now + RetireGrace
				r.mu.Unlock()
				continue
			}
			entries = append(entries, staleEntries(it.TCStale, live)...)
			going = append(going, it)
		}
		if len(entries) == 0 {
			continue
		}
		var delErr error
		for _, op := range tcOps(executor.Target{NS: ns}, entries) {
			if _, delErr = ex.Do(ctx, op); delErr != nil {
				break
			}
		}
		if delErr != nil {
			if gone(delErr) {
				r.forget(going)
				continue
			}
			fail(going, fmt.Errorf("delete the retired tc classes of %s: %w", dev, delErr))
			continue
		}
		r.forget(going)
		deleted += len(going)
	}
	return deleted, firstErr
}

func (r *Retirer) forget(its []*retiree) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, it := range its {
		delete(r.items, it.Key())
	}
}

// gone reports whether the error says that the interface does not exist any more or is no longer
// one of Chaos Gateway's: either way nobody can delete anything on it.
func gone(err error) bool {
	var re *executor.RemoteError
	if errors.Is(err, executor.ErrOutOfScope) || errors.As(err, &re) && re.Code == executor.CodeScope {
		return true
	}
	return strings.Contains(err.Error(), "Cannot find device")
}

// stillThere reports whether the stale class (or tree) is still in the kernel's tree.
func stillThere(st TCStale, live *linux.NormTree) bool {
	if st.Class == "" {
		return len(live.Qdiscs) > 0
	}
	for _, c := range live.Classes {
		if c.ID == st.Class {
			return true
		}
	}
	return false
}

// queued reports whether a qdisc of the stale class (or of the tree) still holds packets.
func queued(st TCStale, live *linux.NormTree) bool {
	for _, q := range live.Qdiscs {
		if q.Netem == nil || q.Stats == nil || (st.Class != "" && q.Parent != st.Class) {
			continue
		}
		if q.Stats.Backlog > 0 || q.Stats.Qlen > 0 {
			return true
		}
	}
	return false
}
