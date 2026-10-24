package engine

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// This file is the engine's side of the DNS-derived address sets (plan §2.6, §3.11, M20): what the DNS
// proxy answered a device goes into the kernel's set of every hostname selector the name matches,
// before the answer is sent, so the device's first packet already matches.
//
//   - The proxy reports resolutions (POST /internal/dns/resolutions; RecordResolutions). The engine
//     keeps a mirror of what the sets hold (client, address, the time each element runs out) and writes
//     only what is new or about to run out: a name that is asked for every second costs one write per
//     lifetime, not one per query.
//   - The writes of concurrent reports are one executor request (group commit): while one request is on
//     its way, the next reports gather and go together. The request is the executor's NftDNSElements,
//     which takes no turn behind a plan (its own netlink connection), so a set update during a long plan
//     is applied without waiting for it.
//   - One exception: a plan that replaces a set (its definition changed, so its name did, plan §3.2)
//     deletes it and creates a new one. Updates for that set wait for the plan, and then go into the
//     new set (the report names the selector, not the set: begin and end of the apply loop).
//   - The lifetime of an element is max(the TTL of the answer, 10 minutes) and is refreshed while the
//     connection table still shows flows to the address (Refresh, called after every read of the
//     connections): a long-lived connection keeps its fault past a short TTL, and past 10 minutes.

// MinHostLifetime is the least time an element of a DNS-derived set lives (plan §2.6: "max(DNS TTL, 10 min)").
const MinHostLifetime = 10 * time.Minute

// hostWriteTimeout bounds one request to the executor.
const hostWriteTimeout = 10 * time.Second

// HostResolution is what the DNS proxy reports: the addresses a client got for a name, as the answer
// had them. Set is the id of the selector's set (compiler.DNSSet.ID).
type HostResolution struct {
	Set    string
	Client netip.Addr
	Name   string
	Addrs  []netip.Addr
	TTL    time.Duration
}

// RecordResult says what a report came to.
type RecordResult struct {
	// Written are the elements this report put into a set (new ones and refreshes), Kept the ones the
	// set holds with enough time left, Ignored the ones that no rule or fault needs: a set that does not
	// exist (any more), a client outside the sources of the selector.
	Written, Kept, Ignored int
}

type hostKey struct{ client, addr netip.Addr }

type hostElem struct {
	expires time.Time
	// life is the lifetime the element was written with
	life  time.Duration
	names []string
}

type hostSetInfo struct {
	name    string
	pattern string
	sources []netip.Prefix
}

// hostSets is the mirror and the writer. It is safe for concurrent use; its mutex is never held while the
// executor is asked.
type hostSets struct {
	e *Engine

	mu    sync.Mutex
	sets  map[string]hostSetInfo // by set id: what the applied target has
	elems map[string]map[hostKey]*hostElem
	// gates hold the ids of the sets a plan in flight replaces: a channel that is closed when it is through
	gates map[string]chan struct{}

	qmu      sync.Mutex
	queue    []*hostWrite
	flushing bool
}

func newHostSets(e *Engine) *hostSets {
	return &hostSets{e: e, sets: map[string]hostSetInfo{}, elems: map[string]map[hostKey]*hostElem{}, gates: map[string]chan struct{}{}}
}

// hostWrite is one request for the executor: the elements it wants written by set id.
type hostWrite struct {
	id    map[string][]executor.DNSElement
	names map[string]map[hostKey][]string
	done  chan error
}

func lifetimeOf(ttl time.Duration) time.Duration {
	life := max(ttl, MinHostLifetime)
	if limit := time.Duration(executor.MaxDNSTimeout) * time.Second; life > limit {
		life = limit
	}
	return life.Truncate(time.Second)
}

// RecordResolutions writes what the proxy answered into the DNS-derived sets and returns when the
// kernel holds it (the proxy sends its answer after that). An unknown set or a client outside its sources is
// ignored, not an error: the proxy works from a configuration that may be a moment behind.
func (e *Engine) RecordResolutions(ctx context.Context, rs []HostResolution) (RecordResult, error) {
	return e.hosts.record(ctx, rs)
}

func (h *hostSets) record(ctx context.Context, rs []HostResolution) (RecordResult, error) {
	var res RecordResult
	if !h.e.started {
		return res, ErrClosed
	}
	// a set that a plan in flight replaces is not written to before the plan is through: the report then
	// goes into the new set
	if err := h.waitGates(ctx, rs); err != nil {
		return res, err
	}
	now := h.e.cfg.Clock.Now()
	w := &hostWrite{id: map[string][]executor.DNSElement{}, names: map[string]map[hostKey][]string{}, done: make(chan error, 1)}
	h.mu.Lock()
	for _, r := range rs {
		info, ok := h.sets[r.Set]
		if !ok || !inPrefixes(info.sources, r.Client) {
			res.Ignored += len(r.Addrs)
			continue
		}
		life := lifetimeOf(r.TTL)
		for _, a := range r.Addrs {
			if !a.Is4() || a.IsUnspecified() || a.IsMulticast() {
				res.Ignored++
				continue
			}
			k := hostKey{r.Client, a}
			cur := h.elems[r.Set][k]
			if cur != nil && cur.expires.Sub(now) > life/2 && cur.expires.Sub(now) > 0 {
				h.addName(cur, r.Name)
				res.Kept++
				continue
			}
			w.id[r.Set] = append(w.id[r.Set], executor.DNSElement{Client: r.Client.String(), Addr: a.String(), TimeoutSeconds: int(life / time.Second), Replace: cur != nil && cur.expires.After(now)})
			if w.names[r.Set] == nil {
				w.names[r.Set] = map[hostKey][]string{}
			}
			w.names[r.Set][k] = append(w.names[r.Set][k], r.Name)
			res.Written++
		}
	}
	h.mu.Unlock()
	if res.Written == 0 {
		return res, nil
	}
	if err := h.write(ctx, w); err != nil {
		res.Written = 0
		return res, err
	}
	return res, nil
}

func (h *hostSets) addName(el *hostElem, name string) {
	if name == "" {
		return
	}
	for _, n := range el.names {
		if n == name {
			return
		}
	}
	if len(el.names) < 8 {
		el.names = append(el.names, name)
	}
}

func inPrefixes(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// waitGates waits for the plans that replace a set the report names.
func (h *hostSets) waitGates(ctx context.Context, rs []HostResolution) error {
	for {
		h.mu.Lock()
		var wait chan struct{}
		for _, r := range rs {
			if g, ok := h.gates[r.Set]; ok {
				wait = g
				break
			}
		}
		h.mu.Unlock()
		if wait == nil {
			return nil
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		case <-h.e.ctx.Done():
			return errors.New("the engine is stopping")
		}
	}
}

// write hands a request to the group commit and waits for the answer of the executor.
func (h *hostSets) write(ctx context.Context, w *hostWrite) error {
	h.qmu.Lock()
	h.queue = append(h.queue, w)
	start := !h.flushing
	if start {
		h.flushing = true
	}
	h.qmu.Unlock()
	if start {
		h.e.sup.Go(h.e.ctx, "engine.hostsets", func(context.Context) error { h.flush(); return nil })
	}
	select {
	case err := <-w.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// flush writes what has gathered as one request, again and again until nothing is left: reports that
// arrive while a request is on its way go together in the next.
func (h *hostSets) flush() {
	for {
		h.qmu.Lock()
		batch := h.queue
		h.queue = nil
		if len(batch) == 0 {
			h.flushing = false
			h.qmu.Unlock()
			return
		}
		h.qmu.Unlock()
		err := h.writeBatch(batch)
		for _, w := range batch {
			w.done <- err
		}
	}
}

func (h *hostSets) writeBatch(batch []*hostWrite) error {
	// the sets as they are named now (an apply that replaced one has ended: the gate held the writers back)
	type pair struct {
		el    executor.DNSElement
		names []string
		k     hostKey
	}
	merged := map[string]map[string]*pair{} // set id, client|addr
	for _, w := range batch {
		for id, els := range w.id {
			if merged[id] == nil {
				merged[id] = map[string]*pair{}
			}
			for _, el := range els {
				key := el.Client + "|" + el.Addr
				k := hostKey{netip.MustParseAddr(el.Client), netip.MustParseAddr(el.Addr)}
				if p := merged[id][key]; p != nil {
					p.el.TimeoutSeconds = max(p.el.TimeoutSeconds, el.TimeoutSeconds)
					p.el.Replace = p.el.Replace || el.Replace
					p.names = append(p.names, w.names[id][k]...)
					continue
				}
				merged[id][key] = &pair{el: el, names: append([]string(nil), w.names[id][k]...), k: k}
			}
		}
	}
	h.mu.Lock()
	var update executor.NftDNSElements
	update.NS = h.e.cfg.Namespace
	var ids []string
	for id := range merged {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		info, ok := h.sets[id]
		if !ok {
			continue // the set went with an apply since the report
		}
		u := executor.DNSSetUpdate{Set: info.name}
		keys := make([]string, 0, len(merged[id]))
		for k := range merged[id] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			u.Elements = append(u.Elements, merged[id][k].el)
		}
		update.Updates = append(update.Updates, u)
	}
	h.mu.Unlock()
	if len(update.Updates) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(h.e.ctx, hostWriteTimeout)
	defer cancel()
	if _, err := h.e.cfg.Exec.Do(ctx, &update); err != nil {
		h.e.cfg.Log.Warn("writing DNS-derived set elements failed", "error", err)
		return fmt.Errorf("the DNS-derived sets could not be updated: %w", err)
	}
	now := h.e.cfg.Clock.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, m := range merged {
		if _, ok := h.sets[id]; !ok {
			continue
		}
		if h.elems[id] == nil {
			h.elems[id] = map[hostKey]*hostElem{}
		}
		for _, p := range m {
			life := time.Duration(p.el.TimeoutSeconds) * time.Second
			el := h.elems[id][p.k]
			if el == nil {
				el = &hostElem{}
				h.elems[id][p.k] = el
			}
			el.expires, el.life = now.Add(life), life
			for _, n := range p.names {
				h.addName(el, n)
			}
		}
	}
	return nil
}

// begin holds the writes for the sets that the plan about to run replaces (a set of the applied target
// whose name the new target changes), and returns what to call when the plan is through.
func (h *hostSets) begin(next *compiler.Target) (end func(applied *compiler.Target, kernel *linux.Ruleset)) {
	h.mu.Lock()
	gate := make(chan struct{})
	var held []string
	for _, s := range next.DNSSets {
		if old, ok := h.sets[s.ID]; ok && old.name != s.Name {
			h.gates[s.ID] = gate
			held = append(held, s.ID)
		}
	}
	h.mu.Unlock()
	return func(applied *compiler.Target, kernel *linux.Ruleset) {
		if applied != nil {
			h.applied(applied, kernel)
		}
		h.mu.Lock()
		for _, id := range held {
			if h.gates[id] == gate {
				delete(h.gates, id)
			}
		}
		h.mu.Unlock()
		close(gate)
	}
}

// applied makes the sets of a target that is in the kernel the sets updates go to. A set whose name changed is
// a new, empty one: what the mirror held is gone (and the event dns_set_epoch says so); one that went is
// forgotten. After a restart the mirror is empty while the kernel still holds the elements of the sets that
// stay: they are taken from the listing the apply read back (kernel), with the time they have left.
func (h *hostSets) applied(t *compiler.Target, kernel *linux.Ruleset) {
	now := h.e.cfg.Clock.Now()
	var epochs []map[string]any
	h.mu.Lock()
	next := make(map[string]hostSetInfo, len(t.DNSSets))
	for _, s := range t.DNSSets {
		next[s.ID] = hostSetInfo{name: s.Name, pattern: s.Pattern, sources: s.Sources}
		old, had := h.sets[s.ID]
		switch {
		case had && old.name != s.Name:
			delete(h.elems, s.ID)
			epochs = append(epochs, map[string]any{"set": s.ID, "pattern": s.Pattern, "name": s.Name, "previous": old.name})
		case !had && kernel != nil:
			if set := kernel.Set(s.Name); set != nil {
				h.adopt(s.ID, set, now)
			}
		}
	}
	for id := range h.sets {
		if _, ok := next[id]; !ok {
			delete(h.elems, id)
		}
	}
	h.sets = next
	h.mu.Unlock()
	for _, d := range epochs {
		h.e.Emit(EventDNSSetEpoch, d)
	}
}

// adopt takes the elements a set holds in the kernel into the mirror.
func (h *hostSets) adopt(id string, set *linux.NftSet, now time.Time) {
	for _, te := range set.TimedElements() {
		client, addr, ok := strings.Cut(te.Key, " . ")
		if !ok {
			continue
		}
		c, err1 := netip.ParseAddr(client)
		a, err2 := netip.ParseAddr(addr)
		if err1 != nil || err2 != nil {
			continue
		}
		left := te.Expires
		if left <= 0 {
			left = te.Timeout
		}
		if left <= 0 {
			continue
		}
		if h.elems[id] == nil {
			h.elems[id] = map[hostKey]*hostElem{}
		}
		h.elems[id][hostKey{c, a}] = &hostElem{expires: now.Add(left), life: max(te.Timeout, left)}
	}
}

// Refresh extends the lifetime of the elements whose address the connection table still shows a flow
// to (plan §2.6): a flow of a client to an address in a set keeps the element while it lives. An element
// with less than half of its lifetime left is written again with its full lifetime. It runs after every
// read of the connections.
func (h *hostSets) refresh(ctx context.Context, flows []FlowCounters) {
	now := h.e.cfg.Clock.Now()
	w := &hostWrite{id: map[string][]executor.DNSElement{}, names: map[string]map[hostKey][]string{}, done: make(chan error, 1)}
	h.mu.Lock()
	if len(h.elems) == 0 {
		h.mu.Unlock()
		return
	}
	// forget what ran out
	for id, m := range h.elems {
		for k, el := range m {
			if !el.expires.After(now) {
				delete(m, k)
			}
		}
		if len(m) == 0 {
			delete(h.elems, id)
		}
	}
	seen := map[hostKey]bool{}
	for _, f := range flows {
		k := hostKey{f.Src, f.Dst}
		if seen[k] {
			continue
		}
		seen[k] = true
		for id, m := range h.elems {
			el := m[k]
			if el == nil || el.expires.Sub(now) > el.life/2 {
				continue
			}
			life := max(el.life, MinHostLifetime)
			w.id[id] = append(w.id[id], executor.DNSElement{Client: k.client.String(), Addr: k.addr.String(), TimeoutSeconds: int(life / time.Second), Replace: true})
			if w.names[id] == nil {
				w.names[id] = map[hostKey][]string{}
			}
			w.names[id][k] = nil
		}
	}
	h.mu.Unlock()
	if len(w.id) == 0 {
		return
	}
	if err := h.write(ctx, w); err != nil {
		h.e.cfg.Log.Warn("refreshing DNS-derived set elements failed: it is tried again with the next reading", "error", err)
	}
}

// names returns the names the client got for the address through the gateway's DNS, as the sets hold
// them: what a rule with a hostname destination matches against.
func (h *hostSets) names(client, addr netip.Addr) []string {
	now := h.e.cfg.Clock.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	ids := make([]string, 0, len(h.elems))
	for id := range h.elems {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		el := h.elems[id][hostKey{client, addr}]
		if el == nil || !el.expires.After(now) {
			continue
		}
		for _, n := range el.names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		// the selector itself names the address too, whatever name the answer carried
		if p := h.sets[id].pattern; p != "" && !strings.HasPrefix(p, "*.") && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// HostNames returns the names a client got for an address from the gateway's DNS that some rule or fault
// selects by name (the DNS-derived sets hold them): the name the answer carried, and the exact selectors
// the address is in the set of. Empty for an address no hostname selector covers.
func (e *Engine) HostNames(client, addr netip.Addr) []string { return e.hosts.names(client, addr) }

// HostAddrs returns the addresses the sets hold for names the selector matches, for any client, sorted.
func (e *Engine) HostAddrs(pattern string) []netip.Addr { return e.hosts.addrs(pattern) }

func (h *hostSets) addrs(pattern string) []netip.Addr {
	now := h.e.cfg.Clock.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[netip.Addr]bool{}
	for id, m := range h.elems {
		for k, el := range m {
			if !el.expires.After(now) {
				continue
			}
			names := el.names
			if p := h.sets[id].pattern; len(names) == 0 && p != "" {
				names = []string{p}
			}
			for _, n := range names {
				if domain.HostnameMatches(pattern, n) {
					seen[k.addr] = true
					break
				}
			}
		}
	}
	out := make([]netip.Addr, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

// HostnameMatches reports whether a selector (an exact name or `*.suffix`) matches one of the names.
func hostnameMatchesAny(pattern string, names []string) bool {
	for _, n := range names {
		if domain.HostnameMatches(pattern, n) {
			return true
		}
	}
	return false
}
