package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the effective policy of plan §3.2 for the impairment family: it asks the domain
// layer for the winning fault of every source of traffic (domain.World.Table, which applies the
// precedence rules of §2.4 and cuts overlapping selectors into disjoint pieces), gives each winner
// a stable fault id, and turns the result into what the packet path needs:
//
//   - the entries of the four classification maps (interval maps keyed on the conntrack original
//     tuple, plan §3.3), each element jumping to the chain `mark_<id>`;
//   - one chain `mark_<id>` per id that writes the id into the mark and counts the packets in two
//     named counters (upload and download);
//   - the tc tree (tc.go): one class per active (id, direction) with its netem leaf.
//
// Faults of the MTU, DNS, TLS and DHCP families and the tunnel family take no part here: they
// resolve into other mechanisms, which later milestones compile.

// Problem codes of the fault compiler.
const (
	// CodeCapacityExceeded: more fault ids or tc classes than the mark layout or the class limit
	// of an interface allow (plan §3.3). The problem names the scope that caused it.
	CodeCapacityExceeded = "capacity_exceeded"
	// CodeFaultInvalid: the parameters of a winning fault cannot be turned into a netem
	// configuration. Validation refuses such a fault before it is stored, so this is a defect.
	CodeFaultInvalid = "fault_invalid"
	// CodeHostnameUnresolved: a fault names a hostname; its addresses are known at run time only
	// (DNS-derived sets, M20), so the fault does not reach the packet path yet.
	CodeHostnameUnresolved = "hostname_unresolved"
)

// Fault is one fault id of the target: a winning fault, per matched device when it has a rate, a
// queue limit or keep order (plan §3.3, D18).
type Fault struct {
	// ID is the 12-bit id written into the mark (1 to 4095; 0 means no fault).
	ID int `json:"id"`
	// Key is what makes the id stable: the layer, the overlay or fault, the family and, for a
	// per-device fault, the device. The same key gets the same id on every compile that is given the
	// previous allocation (Input.FaultIDs).
	Key string `json:"key"`
	// Layer is "overlay" or "config"; Source the UUID of the overlay or of the configured fault.
	Layer  string `json:"layer"`
	Source string `json:"source"`
	// Profile is the name of the activated profile when the fault is one of its parts.
	Profile string `json:"profile,omitempty"`
	// Device is the device this id's queue belongs to; empty when the id is shared by every device
	// the fault matches (and by the addresses no device owns, one queue per scope, plan §2.4).
	Device string `json:"device,omitempty"`
	// Scope describes where the fault applies, for the capacity message and explain.
	Scope string `json:"scope"`
	// Upload and Download are the configurations of the two directions; nil when the fault does not
	// impair that direction (no class exists for it).
	Upload   *Netem `json:"upload,omitempty"`
	Download *Netem `json:"download,omitempty"`
	// CounterUp and CounterDown are the named nft counters of the packets classified into this id.
	CounterUp   string `json:"counter_up"`
	CounterDown string `json:"counter_down"`
}

// netem returns the configuration of a direction.
func (f Fault) netem(d Direction) *Netem {
	if d == Download {
		return f.Download
	}
	return f.Upload
}

// faultBuild is what compileFaults hands to compileClassify.
type faultBuild struct {
	maps     []MapDef
	chains   []Chain
	counters []string
}

// faultCounterName names the counter of a fault key and direction. It is derived from the key, not
// from the id, so a counter keeps its name (and its value) while the fault stays the same, however
// ids are renumbered, and a removed fault's counter is deleted with it (plan §3.2).
func faultCounterName(key string, d Direction) string {
	h := sha256.Sum256([]byte(key))
	suffix := "up"
	if d == Download {
		suffix = "down"
	}
	return "fault_" + hex.EncodeToString(h[:5]) + "_" + suffix
}

// directionParams returns the parameters of the two directions of a fault: the flat ones apply to
// both, upload and download give each its own complete set, and a missing direction is unimpaired.
func directionParams(b *model.FaultBody) (up, down *model.NetemParams) {
	if b.Upload != nil || b.Download != nil {
		return b.Upload, b.Download
	}
	flat := model.NetemParams{
		Blackout: b.Blackout, BurstLoss: b.BurstLoss, Corrupt: b.Corrupt, Distribution: b.Distribution,
		Duplicate: b.Duplicate, Flapping: b.Flapping, Jitter: b.Jitter, KeepOrder: b.KeepOrder,
		Latency: b.Latency, Loss: b.Loss, LossCorrelation: b.LossCorrelation, QueueLimit: b.QueueLimit,
		Rate: b.Rate, Reorder: b.Reorder,
	}
	return &flat, &flat
}

// perDevice reports whether a fault gets one id per matched device: it has a rate, an explicit
// queue limit or keep order in some direction (plan §3.3, D18).
func perDevice(p ...*model.NetemParams) bool {
	for _, x := range p {
		if x == nil {
			continue
		}
		if x.Rate != nil || x.QueueLimit != nil || (x.KeepOrder != nil && *x.KeepOrder) {
			return true
		}
	}
	return false
}

// resolvedFault is a winner before it has an id.
type resolvedFault struct {
	cand     domain.Candidate
	up, down *Netem
	perDev   bool
}

// neutral reports whether the fault impairs neither direction: it wins (and masks less specific
// faults) but needs no id and no class.
func (r resolvedFault) neutral() bool { return r.up == nil && r.down == nil }

func baseKey(c domain.Candidate) string {
	return string(c.Layer) + ":" + c.ID + ":" + c.Family
}

func (t *Target) resolveWinner(c domain.Candidate) (resolvedFault, bool) {
	r := resolvedFault{cand: c}
	if c.Impairment == nil {
		return r, true
	}
	pu, pd := directionParams(c.Impairment)
	r.perDev = perDevice(pu, pd)
	for _, x := range []struct {
		p   *model.NetemParams
		dst **Netem
	}{{pu, &r.up}, {pd, &r.down}} {
		if x.p == nil {
			continue
		}
		n, err := netemFrom(x.p)
		if err != nil {
			t.errorf(CodeFaultInvalid, "", "the fault %s has parameters that cannot be compiled: %v", c.ID, err)
			return r, false
		}
		if n.IsNeutral() {
			continue
		}
		*x.dst = &n
	}
	return r, true
}

// assignFaultIDs gives every key a stable id from 1 to MaxID. A key that had an id keeps it. A new
// key takes the lowest id that the previous allocation did not use at all, so an id that was
// just released is not handed to another fault while packets queued under it may still be in
// flight (make-before-break, M8b); only when none is left does it take a released one. ok is false
// when there are more keys than ids.
func assignFaultIDs(keys []string, prev map[string]int) (ids map[string]int, ok bool) {
	sort.Strings(keys)
	ids = make(map[string]int, len(keys))
	used := map[int]bool{}
	prevUsed := map[int]bool{}
	for _, id := range prev {
		prevUsed[id] = true
	}
	for _, k := range keys {
		if id, had := prev[k]; had && id >= 1 && id <= MarkIDMax && !used[id] {
			ids[k], used[id] = id, true
		}
	}
	next := func(avoidPrev bool) int {
		for id := 1; id <= MarkIDMax; id++ {
			if !used[id] && (!avoidPrev || !prevUsed[id]) {
				return id
			}
		}
		return 0
	}
	for _, k := range keys {
		if _, done := ids[k]; done {
			continue
		}
		id := next(true)
		if id == 0 {
			id = next(false)
		}
		if id == 0 {
			return nil, false
		}
		ids[k], used[id] = id, true
	}
	return ids, true
}

// Limits of the classification maps. A table is limited by domain.MaxTableCells; the tables of all
// sources that differ by MaxCompileCells; the elements of the maps by MaxClassElements.
const (
	MaxCompileCells  = 1 << 15
	MaxClassElements = 1 << 13
	maxClassRows     = 1 << 18
)

// classificationProblem reports capacity_exceeded for a limit of the classification maps.
func (t *Target) classificationProblem(msg string, faults []string) {
	t.Problems = append(t.Problems, Problem{Severity: SevError, Code: CodeCapacityExceeded, Message: msg, Faults: faults})
}

// sourceName describes a source of traffic for messages.
func sourceName(idx *domain.Index, s domain.Source) string {
	switch {
	case s.Device != "":
		if d, ok := idx.Devices[s.Device]; ok && d.Name != "" {
			return "device " + d.Name
		}
		return "device " + s.Device
	case len(s.Addrs) > 0:
		return "the source " + s.Addrs[0].String()
	case len(s.Ranges) > 0:
		return "the addresses " + s.Ranges[0].String()
	}
	return "a source"
}

// compileFaults resolves the winning impairment faults and builds the classification maps, the
// per-id chains with their counters and the tc tree.
func (t *Target) compileFaults(in Input, idx *domain.Index) {
	t.faultBuild = &faultBuild{}
	cfg := in.Config
	if !domain.IsNormalized(cfg) {
		n, errs := domain.Normalize(cfg)
		if len(errs) > 0 {
			return // a configuration with dangling references has no faults to resolve
		}
		cfg = n
	}
	w, err := domain.NewWorld(cfg, in.Overlays)
	if err != nil {
		t.errorf(CodeFaultInvalid, "", "faults cannot be resolved: %v", err)
		return
	}
	identity := domain.Identity{}
	if in.Identity != nil {
		identity = *in.Identity
	}
	sources := w.Sources(identity)

	// ---- pass 1: the winner of every entry of every source's table, and the keys they need ----
	tables := make([]domain.Table, len(sources))
	winners := map[string]resolvedFault{} // by base key
	entryKey := make([][]string, len(sources))
	keySet := map[string]bool{}
	faults := map[string]*Fault{}
	hostnames := map[string]bool{}
	cells := 0
	for i, src := range sources {
		tab, err := w.Table(src, domain.FamilyImpairment)
		if err != nil {
			var big *domain.TableTooLargeError
			if errors.As(err, &big) {
				t.classificationProblem(fmt.Sprintf("the faults of %s select so many different destinations and ports that the classification maps would need more than %d cells; remove some of the faults that name a destination or ports", sourceName(idx, src), domain.MaxTableCells), big.Faults)
				return
			}
			t.errorf(CodeFaultInvalid, "", "the table of a source cannot be built: %v", err)
			return
		}
		if cells += tab.Cells; cells > MaxCompileCells {
			t.classificationProblem(fmt.Sprintf("the classification of all sources needs more than %d cells (the work of the tables that differ); remove some of the faults that name a destination or ports", MaxCompileCells), nil)
			return
		}
		tables[i] = tab
		for _, c := range tab.Unresolved {
			if k := baseKey(c); !hostnames[k] {
				hostnames[k] = true
				t.warn(CodeHostnameUnresolved, "", "the fault %s selects a hostname: its addresses are known at run time only (DNS-derived sets), so it does not classify traffic yet", c.ID)
			}
		}
		entryKey[i] = make([]string, len(tab.Entries))
		for j, e := range tab.Entries {
			if e.Winner == nil {
				continue
			}
			bk := baseKey(*e.Winner)
			r, seen := winners[bk]
			if !seen {
				var ok bool
				if r, ok = t.resolveWinner(*e.Winner); !ok {
					return
				}
				winners[bk] = r
			}
			if r.neutral() {
				continue
			}
			key := bk
			device := ""
			if r.perDev {
				device = src.Device
				if device == "" {
					device = "shared"
				}
				key += "@" + device
				if device == "shared" {
					device = ""
				}
			}
			entryKey[i][j] = key
			if !keySet[key] {
				keySet[key] = true
				c := r.cand
				faults[key] = &Fault{Key: key, Layer: string(c.Layer), Source: c.ID, Profile: c.ProfileName, Device: device,
					Scope: describeScope(idx, c.Scope), Upload: r.up, Download: r.down,
					CounterUp: faultCounterName(key, Upload), CounterDown: faultCounterName(key, Download)}
			}
		}
	}

	for k := range winners {
		t.Winners = append(t.Winners, k)
	}
	sort.Strings(t.Winners)

	// ---- stable ids -------------------------------------------------------------------------
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	ids, ok := assignFaultIDs(keys, in.FaultIDs)
	if !ok {
		t.capacityProblem(faults, fmt.Sprintf("%d faults need an id each, the mark layout has %d", len(keys), MarkIDMax), func(f *Fault) int { return 1 })
		return
	}
	t.FaultIDs = ids
	for k, f := range faults {
		f.ID = ids[k]
		t.Faults = append(t.Faults, *f)
	}
	sort.Slice(t.Faults, func(i, j int) bool { return t.Faults[i].ID < t.Faults[j].ID })

	// ---- the tc tree and its capacity -------------------------------------------------------
	t.compileTC(in)
	if t.HasErrors() {
		return
	}

	// ---- the classification maps ------------------------------------------------------------
	t.compileClassification(sources, tables, entryKey)
}

// compileTC builds the classes of the active (id, direction)s and checks the class limit of an
// interface.
func (t *Target) compileTC(in Input) {
	classes := 0
	for _, f := range t.Faults {
		for _, d := range []Direction{Upload, Download} {
			if f.netem(d) != nil {
				classes++
			}
		}
	}
	if classes == 0 {
		return
	}
	limit := in.ClassLimit
	if limit <= 0 {
		limit = DefaultClassLimit()
	}
	// every interface carries the default class and one class per active (id, direction)
	if classes+1 > limit {
		byKey := map[string]*Fault{}
		for i := range t.Faults {
			byKey[t.Faults[i].Key] = &t.Faults[i]
		}
		t.capacityProblem(byKey, fmt.Sprintf("%d classes (one per active fault id and direction, plus the default) exceed the limit of %d per interface", classes+1, limit), func(f *Fault) int {
			n := 0
			for _, d := range []Direction{Upload, Download} {
				if f.netem(d) != nil {
					n++
				}
			}
			return n
		})
		return
	}
	budget := in.QueueBudget
	if budget <= 0 {
		budget = DefaultQueueBudget
	}
	tc := &TCTarget{Devs: t.tcDevs()}
	for i := range t.Faults {
		f := &t.Faults[i]
		for _, d := range []Direction{Upload, Download} {
			n := f.netem(d)
			if n == nil {
				continue
			}
			cfg := *n
			if !cfg.LimitExplicit {
				cfg.Limit = computedLimit(cfg, classes, budget)
			}
			// the fault keeps the configuration that is actually written
			if d == Upload {
				f.Upload = &cfg
			} else {
				f.Download = &cfg
			}
			tc.Classes = append(tc.Classes, TCClass{ID: f.ID, Dir: d, Minor: classMinor(f.ID, d), Mark: MarkOf(f.ID, d), Netem: cfg})
		}
	}
	if len(tc.Devs) > 0 {
		t.TC = tc
	}
}

// capacityProblem reports capacity_exceeded with the scope that caused it: the fault that needs
// the most (by weight), and the others that share the blame.
func (t *Target) capacityProblem(faults map[string]*Fault, what string, weight func(*Fault) int) {
	var all []*Fault
	for _, f := range faults {
		all = append(all, f)
	}
	sort.Slice(all, func(i, j int) bool {
		wi, wj := weight(all[i]), weight(all[j])
		if wi != wj {
			return wi > wj
		}
		return all[i].Key < all[j].Key
	})
	// group by the fault that makes the ids: a per-device fault of a network is one cause
	type cause struct {
		source, scope string
		n             int
	}
	var causes []cause
	seen := map[string]int{}
	for _, f := range all {
		i, ok := seen[f.Source]
		if !ok {
			i = len(causes)
			seen[f.Source] = i
			causes = append(causes, cause{source: f.Source, scope: f.Scope})
		}
		causes[i].n += weight(f)
	}
	sort.SliceStable(causes, func(i, j int) bool { return causes[i].n > causes[j].n })
	p := Problem{Severity: SevError, Code: CodeCapacityExceeded}
	if len(causes) > 0 {
		p.Scope = causes[0].scope
		p.Message = fmt.Sprintf("%s; most of them from the fault on %s (%d)", what, causes[0].scope, causes[0].n)
		for _, c := range causes {
			p.Faults = append(p.Faults, c.source)
		}
	} else {
		p.Message = what
	}
	t.Problems = append(t.Problems, p)
}

// describeScope names a scope for messages: "device esp32-42", "network IoT", "global".
func describeScope(idx *domain.Index, s model.Scope) string {
	name := func(ref *model.Ref, names func(string) (string, bool)) string {
		if n, ok := names(strings.ToLower(*ref)); ok && n != "" {
			return n
		}
		return *ref
	}
	switch {
	case s.Device != nil:
		return "device " + name(s.Device, func(id string) (string, bool) {
			if d, ok := idx.Devices[id]; ok {
				return d.Name, true
			}
			return "", false
		})
	case s.Group != nil:
		return "group " + name(s.Group, func(id string) (string, bool) { n, ok := idx.Groups[id]; return n, ok })
	case s.Network != nil:
		return "network " + name(s.Network, func(id string) (string, bool) {
			if n, ok := idx.Networks[id]; ok {
				return n.Name, true
			}
			return "", false
		})
	case s.RemoteNetwork != nil:
		r := s.RemoteNetwork
		switch {
		case r.Cidr != nil:
			return "remote network " + *r.Cidr
		case r.Client != nil:
			return "remote network of client " + *r.Client
		case r.Link != nil:
			return "remote network of link " + *r.Link
		}
		return "remote network"
	case s.Global != nil:
		return "global"
	}
	return "unknown scope"
}

// ---- the classification maps -----------------------------------------------------------------

// aspan is an inclusive range of IPv4 addresses as numbers.
type aspan struct{ lo, hi uint64 }

func addrNum(a netip.Addr) uint64 {
	b := a.As4()
	return uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
}

func numAddr(n uint64) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

const addrSpace = uint64(1) << 32

// partitionSources gives every source the addresses it classifies, as disjoint spans. The lookup
// maps are interval maps, which cannot hold overlapping elements, so the claims of the sources
// are resolved here: a device's own address beats a range that identifies a device, a smaller range
// beats a larger one (the same order as Identity.OwnerOf), and any device beats the stretches of
// addresses that no device owns. The result is indexed like sources.
func partitionSources(sources []domain.Source) [][]aspan {
	type item struct {
		s    aspan
		rank uint64
		src  int
	}
	var ranges []item
	exact := map[uint64]int{}
	for i, s := range sources {
		for _, a := range s.Addrs {
			if a.Is4() {
				if _, taken := exact[addrNum(a)]; !taken {
					exact[addrNum(a)] = i
				}
			}
		}
		for _, r := range s.Ranges {
			sp := aspan{addrNum(r.First), addrNum(r.Last)}
			rank := uint64(1) << 40 // a stretch that no device owns
			if s.Device != "" {
				rank = uint64(2)<<40 + (addrSpace - (sp.hi - sp.lo + 1)) // the smaller the range, the higher
			}
			ranges = append(ranges, item{sp, rank, i})
		}
	}
	out := make([][]aspan, len(sources))

	// elementary pieces of the ranges
	cutSet := map[uint64]bool{}
	for _, it := range ranges {
		cutSet[it.s.lo] = true
		if it.s.hi+1 < addrSpace {
			cutSet[it.s.hi+1] = true
		}
	}
	cuts := make([]uint64, 0, len(cutSet))
	for c := range cutSet {
		cuts = append(cuts, c)
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	exactAddrs := make([]uint64, 0, len(exact))
	for a := range exact {
		exactAddrs = append(exactAddrs, a)
	}
	sort.Slice(exactAddrs, func(i, j int) bool { return exactAddrs[i] < exactAddrs[j] })

	for ci, lo := range cuts {
		hi := addrSpace - 1
		if ci+1 < len(cuts) {
			hi = cuts[ci+1] - 1
		}
		best := -1
		for k, it := range ranges {
			if it.s.lo <= lo && hi <= it.s.hi && (best < 0 || it.rank > ranges[best].rank || it.rank == ranges[best].rank && it.src < ranges[best].src) {
				best = k
			}
		}
		if best < 0 {
			continue
		}
		owner := ranges[best].src
		// the exact addresses inside the piece belong to their own devices: cut them out
		start := lo
		first := sort.Search(len(exactAddrs), func(i int) bool { return exactAddrs[i] >= lo })
		for k := first; k < len(exactAddrs) && exactAddrs[k] <= hi; k++ {
			a := exactAddrs[k]
			if a > start {
				out[owner] = append(out[owner], aspan{start, a - 1})
			}
			start = a + 1
		}
		if start <= hi {
			out[owner] = append(out[owner], aspan{start, hi})
		}
	}
	for a, i := range exact {
		out[i] = append(out[i], aspan{a, a})
	}
	for i := range out {
		out[i] = mergeAspans(out[i])
	}
	return out
}

// mergeAspans sorts spans and joins the ones that touch.
func mergeAspans(in []aspan) []aspan {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].lo < in[j].lo })
	out := []aspan{in[0]}
	for _, s := range in[1:] {
		last := &out[len(out)-1]
		if s.lo <= last.hi+1 {
			if s.hi > last.hi {
				last.hi = s.hi
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// clsRow is one element of a classification map before the sources are merged.
type clsRow struct {
	level int
	src   aspan
	dest  *aspan
	port  *domain.PortSel
	id    int
}

func (r clsRow) rest() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%d", r.level, r.id)
	if r.dest != nil {
		fmt.Fprintf(&b, "|%d-%d", r.dest.lo, r.dest.hi)
	}
	if r.port != nil {
		fmt.Fprintf(&b, "|%s", r.port)
	}
	return b.String()
}

func (t *Target) compileClassification(sources []domain.Source, tables []domain.Table, entryKey [][]string) {
	spans := partitionSources(sources)
	var rows []clsRow
	needZero := false
	for i, tab := range tables {
		for j, e := range tab.Entries {
			id := 0
			if k := entryKey[i][j]; k != "" {
				id = t.FaultIDs[k]
			}
			if e.Winner == nil {
				continue
			}
			if id == 0 {
				needZero = true
			}
			var dest *aspan
			if e.Dest != nil {
				dest = &aspan{addrNum(e.Dest.First), addrNum(e.Dest.Last)}
			}
			var port *domain.PortSel
			if e.Port != nil {
				p := *e.Port
				port = &p
			}
			for _, s := range spans[i] {
				rows = append(rows, clsRow{level: e.Level, src: s, dest: dest, port: port, id: id})
			}
			if len(rows) > maxClassRows {
				t.classificationProblem(fmt.Sprintf("the classification maps would need more than %d elements before they are merged; remove some of the faults that name a destination or ports", maxClassRows), nil)
				return
			}
		}
	}
	// sources with the same entries and touching addresses become one element
	groups := map[string][]clsRow{}
	var order []string
	for _, r := range rows {
		k := r.rest()
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	var merged []clsRow
	for _, k := range order {
		g := groups[k]
		sort.Slice(g, func(i, j int) bool { return g[i].src.lo < g[j].src.lo })
		cur := g[0]
		for _, r := range g[1:] {
			if r.src.lo <= cur.src.hi+1 {
				if r.src.hi > cur.src.hi {
					cur.src.hi = r.src.hi
				}
				continue
			}
			merged = append(merged, cur)
			cur = r
		}
		merged = append(merged, cur)
	}

	if len(merged) > MaxClassElements {
		t.classificationProblem(fmt.Sprintf("the classification maps would need %d elements, the limit is %d; remove some of the faults that name a destination or ports", len(merged), MaxClassElements), nil)
		return
	}
	sort.Slice(merged, func(i, j int) bool {
		a, b := merged[i], merged[j]
		if a.level != b.level {
			return a.level < b.level
		}
		if a.src.lo != b.src.lo {
			return a.src.lo < b.src.lo
		}
		if a.dest != nil && b.dest != nil && a.dest.lo != b.dest.lo {
			return a.dest.lo < b.dest.lo
		}
		if a.port != nil && b.port != nil && *a.port != *b.port {
			return portSelLess(*a.port, *b.port)
		}
		return a.id < b.id
	})
	byLevel := map[int][]MapElement{}
	for _, r := range merged {
		byLevel[r.level] = append(byLevel[r.level], MapElement{Key: clsKey(r), Value: MarkChainName(r.id)})
	}
	t.faultBuild.maps = nil
	for _, lv := range classifyLevels {
		t.faultBuild.maps = append(t.faultBuild.maps, MapDef{KeyType: lv.key, ValueType: "verdict", Flags: []string{"interval"}, Elements: byLevel[lv.level]})
	}

	// the per-id chains and their counters
	if needZero {
		t.faultBuild.chains = append(t.faultBuild.chains, markChain(0, "", ""))
	}
	for _, f := range t.Faults {
		t.faultBuild.chains = append(t.faultBuild.chains, markChain(f.ID, f.CounterUp, f.CounterDown))
		t.faultBuild.counters = append(t.faultBuild.counters, f.CounterUp, f.CounterDown)
	}
	sort.Strings(t.faultBuild.counters)
}

func portSelLess(a, b domain.PortSel) bool {
	order := func(p string) int {
		switch p {
		case "tcp":
			return 0
		case "udp":
			return 1
		}
		return 2
	}
	if a.Proto != b.Proto {
		return order(a.Proto) < order(b.Proto)
	}
	if a.From != b.From {
		return a.From < b.From
	}
	return a.To < b.To
}

// clsKey renders the key of a classification element in the canonical form nft prints it in:
// "a . b . proto . port" with an address as itself, a prefix when the range is one, "first-last"
// otherwise, and a port range as "first-last".
func clsKey(r clsRow) string {
	parts := []string{fmtAddrSpan(r.src)}
	if r.dest != nil {
		parts = append(parts, fmtAddrSpan(*r.dest))
	}
	if r.port != nil {
		proto, lo, hi := r.port.Proto, r.port.From, r.port.To
		if proto == "icmp" {
			lo, hi = 0, 65535
		}
		parts = append(parts, proto, fmtPortRange(lo, hi))
	}
	return strings.Join(parts, " . ")
}

func fmtPortRange(lo, hi int) string {
	if lo == hi {
		return fmt.Sprint(lo)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}

// fmtAddrSpan is nft's own rendering of an interval: a single address, a prefix when the range is
// exactly one, else "first-last".
func fmtAddrSpan(s aspan) string {
	if s.lo == s.hi {
		return numAddr(s.lo).String()
	}
	size := s.hi - s.lo + 1
	if size&(size-1) == 0 && s.lo%size == 0 {
		bits := 32
		for n := size; n > 1; n >>= 1 {
			bits--
		}
		return fmt.Sprintf("%s/%d", numAddr(s.lo), bits)
	}
	return numAddr(s.lo).String() + "-" + numAddr(s.hi).String()
}
