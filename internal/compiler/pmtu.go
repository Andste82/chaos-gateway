package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// This file is the MTU family of plan §2.5 (milestone M10): a maximum packet size for the traffic
// of a device, a group, a network or everything, in one of three modes.
//
//	icmp       The traffic is marked with the index of a PMTU mirror table (mark bits 17-19). The ip
//	           rule `fwmark index/0xe0000` sends it through a copy of the policy table whose routes
//	           carry `mtu lock <size>`, so the kernel itself answers a packet that does not fit
//	           with "fragmentation needed, mtu <size>", in both directions (spike S13). There are
//	           up to seven mirror tables, one per distinct size.
//	blackhole  The mark chain of the fault drops a packet longer than the size without a word (nft
//	           `meta length`): the path MTU black hole, a transfer of full-size segments stalls.
//	mss_clamp  The mark chain rewrites the MSS option of a SYN down to size minus the 40 bytes of
//	           the headers. Only TCP of the selected traffic is touched and nothing is cached
//	           anywhere else, which is why it is the mode without the side effect on other devices.
//
// The classification is the second lookup of the classify chain: its four maps (device,
// destination and port levels, like the impairment maps) are keyed on the same conntrack original
// tuple and go to a chain per MTU winner. They are jumped to, not part of the impairment lookup, so
// that a packet gets its impairment id and its PMTU mark from two independent winners (plan §2.4:
// the families resolve independently).

// The mark bits of the PMTU table index (plan §3.3).
const (
	PMTUMarkShift = 17
	// PMTUMarkMask covers bits 17-19.
	PMTUMarkMask = uint32(7) << PMTUMarkShift
	// pmtuKeep is kept when the index is written.
	pmtuKeep = ^PMTUMarkMask // 0xfff1ffff
	// PMTUMaxTables is the number of mirror tables, and so of distinct icmp sizes at once.
	PMTUMaxTables = 7
	// PMTUTableFirst is the mirror table of index 1: 100 is the policy table, 101 is free and 102
	// the service table, so the mirrors are 103 to 109 (table 110 stays unused).
	PMTUTableFirst = PolicyTable + 3
	// PMTURulePriority is before the policy rules and after the rule of the service selection.
	PMTURulePriority = PolicyRulePriority - 50
	// PMTUClassifyChain is the chain of the second lookup, jumped to from the classify chain.
	PMTUClassifyChain = "classify_pmtu"
	pmtuChainPrefix   = "pmtu_"
	// headersIPTCP is what the MSS option does not count: the IPv4 and TCP headers without options.
	headersIPTCP = 40
)

// The modes of an MTU fault.
const (
	PMTUModeICMP      = "icmp"
	PMTUModeBlackhole = "blackhole"
	PMTUModeMSSClamp  = "mss_clamp"
)

// PMTUFault is one winning MTU fault of the target.
type PMTUFault struct {
	// Key is `layer:id:mtu`: what makes the chain and the counters stable.
	Key string `json:"key"`
	// Layer is "overlay" or "config"; Source the UUID of the overlay or of the configured fault.
	Layer   string `json:"layer"`
	Source  string `json:"source"`
	Profile string `json:"profile,omitempty"`
	// Scope describes where the fault applies, for messages and explain.
	Scope string `json:"scope"`
	// Size is the maximum packet size in bytes (IP) and Mode one of the PMTUMode constants.
	Size int    `json:"size"`
	Mode string `json:"mode"`
	// Index is the mark value (1 to 7) and Table the mirror table of an icmp fault; both are 0 for
	// the other modes.
	Index int `json:"index,omitempty"`
	Table int `json:"table,omitempty"`
	// Chain is the chain a classification element goes to.
	Chain string `json:"chain"`
	// CounterUp and CounterDown count the packets classified into the fault, CounterDrop the
	// packets a black hole dropped (empty for the other modes).
	CounterUp   string `json:"counter_up"`
	CounterDown string `json:"counter_down"`
	CounterDrop string `json:"counter_drop,omitempty"`
}

// MSS is the MSS a clamp writes into a SYN.
func (f PMTUFault) MSS() int { return f.Size - headersIPTCP }

// PMTUMarkOf is the mark value of a mirror table index.
func PMTUMarkOf(index int) uint32 { return uint32(index) << PMTUMarkShift }

// PMTUTableOf is the mirror table of an index.
func PMTUTableOf(index int) int { return PMTUTableFirst + index - 1 }

func pmtuName(key string) string {
	h := sha256.Sum256([]byte("pmtu|" + key))
	return hex.EncodeToString(h[:5])
}

// assignPMTUTables gives every distinct icmp size an index from 1 to PMTUMaxTables. A size that had
// an index keeps it (its table and the marks of its traffic stay while the size stays); a new size
// takes the lowest free one. ok is false when there are more sizes than indices.
func assignPMTUTables(sizes []int, prev map[int]int) (map[int]int, bool) {
	sort.Ints(sizes)
	out := make(map[int]int, len(sizes))
	used := map[int]bool{}
	for _, s := range sizes {
		if i, had := prev[s]; had && i >= 1 && i <= PMTUMaxTables && !used[i] {
			out[s], used[i] = i, true
		}
	}
	for _, s := range sizes {
		if _, done := out[s]; done {
			continue
		}
		i := 1
		for i <= PMTUMaxTables && used[i] {
			i++
		}
		if i > PMTUMaxTables {
			return nil, false
		}
		out[s], used[i] = i, true
	}
	return out, true
}

// pmtuBuild is what compilePMTU hands to compileClassify.
type pmtuBuild struct {
	maps     []MapDef
	chains   []Chain
	counters []string
}

// compilePMTU resolves the winning MTU faults of every source, gives the icmp sizes their mirror
// tables, and builds the classification maps, the chains, the mirror routes and rules.
func (t *Target) compilePMTU(in Input, w *domain.World, sources []domain.Source, idx *domain.Index) {
	tables := make([]domain.Table, len(sources))
	var keys []string
	faults := map[string]*PMTUFault{}
	hostnames := map[string]bool{}
	cells := 0
	for i, src := range sources {
		tab, err := w.Table(src, domain.FamilyMTU)
		if err != nil {
			var big *domain.TableTooLargeError
			if errors.As(err, &big) {
				t.classificationProblem(fmt.Sprintf("the MTU faults of %s select so many different destinations and ports that the classification maps would need more than %d cells; remove some of the faults that name a destination or ports", sourceName(idx, src), domain.MaxTableCells), big.Faults)
				return
			}
			t.errorf(CodeFaultInvalid, "", "the MTU table of a source cannot be built: %v", err)
			return
		}
		if cells += tab.Cells; cells > MaxCompileCells {
			t.classificationProblem(fmt.Sprintf("the MTU classification of all sources needs more than %d cells; remove some of the faults that name a destination or ports", MaxCompileCells), nil)
			return
		}
		tables[i] = tab
		for _, c := range tab.Unresolved {
			if k := baseKey(c); !hostnames[k] {
				hostnames[k] = true
				t.warn(CodeHostnameUnresolved, "", "the MTU fault %s selects a hostname: its addresses are known at run time only (DNS-derived sets), so it does not classify traffic yet", c.ID)
			}
		}
		for _, e := range tab.Entries {
			if e.Winner == nil || e.Winner.MTU == nil {
				continue
			}
			k := baseKey(*e.Winner)
			if faults[k] != nil {
				continue
			}
			c := *e.Winner
			mode := PMTUModeICMP
			if c.MTU.Mode != nil {
				mode = string(*c.MTU.Mode)
			}
			h := pmtuName(k)
			f := &PMTUFault{Key: k, Layer: string(c.Layer), Source: c.ID, Profile: c.ProfileName, Scope: describeScope(idx, c.Scope),
				Size: c.MTU.Size, Mode: mode, Chain: pmtuChainPrefix + h,
				CounterUp: "pmtu_" + h + "_up", CounterDown: "pmtu_" + h + "_down"}
			if mode == PMTUModeBlackhole {
				f.CounterDrop = "pmtu_" + h + "_drop"
			}
			faults[k] = f
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)

	// ---- the mirror tables of the icmp sizes ---------------------------------------------------
	sizeSet := map[int]bool{}
	for _, k := range keys {
		if f := faults[k]; f.Mode == PMTUModeICMP {
			sizeSet[f.Size] = true
		}
	}
	var sizes []int
	for s := range sizeSet {
		sizes = append(sizes, s)
	}
	index, ok := assignPMTUTables(sizes, in.PMTUTables)
	if !ok {
		byKey := map[string]*Fault{}
		for _, k := range keys {
			if f := faults[k]; f.Mode == PMTUModeICMP {
				byKey[k] = &Fault{Key: k, Source: f.Source, Scope: f.Scope}
			}
		}
		t.capacityProblem(byKey, fmt.Sprintf("%d different sizes of MTU faults in icmp mode need a PMTU mirror table each, there are %d", len(sizes), PMTUMaxTables), func(f *Fault) int { return 1 })
		return
	}
	if len(index) > 0 {
		t.PMTUTables = index
	}
	for _, k := range keys {
		f := faults[k]
		if f.Mode == PMTUModeICMP {
			f.Index = index[f.Size]
			f.Table = PMTUTableOf(f.Index)
		}
		t.PMTU = append(t.PMTU, *f)
		t.Winners = append(t.Winners, k)
		t.Effective = append(t.Effective, EffectiveFault{Key: k, Layer: f.Layer, Source: f.Source, Family: domain.FamilyMTU,
			Profile: f.Profile, Scope: f.Scope, Summary: f.Summary()})
	}
	sort.Strings(t.Winners)

	// ---- the classification maps and the chains -------------------------------------------------
	ordinal := map[string]int{} // by key; chain names come back from the ordinal
	for i, k := range keys {
		ordinal[k] = i
	}
	byLevel, _, ok := t.classElements(sources, tables, func(i, j int) (int, bool) {
		e := tables[i].Entries[j]
		if e.Winner == nil || e.Winner.MTU == nil {
			return 0, false
		}
		return ordinal[baseKey(*e.Winner)], true
	}, func(id int) string { return faults[keys[id]].Chain })
	if !ok {
		return
	}
	t.pmtuBuild = &pmtuBuild{maps: classMapDefs(byLevel)}
	for _, f := range t.PMTU {
		t.pmtuBuild.chains = append(t.pmtuBuild.chains, f.chain())
		t.pmtuBuild.counters = append(t.pmtuBuild.counters, f.CounterUp, f.CounterDown)
		if f.CounterDrop != "" {
			t.pmtuBuild.counters = append(t.pmtuBuild.counters, f.CounterDrop)
		}
	}

	t.pmtuRouting()
	t.pmtuBird()
}

// chain is the mark chain of an MTU winner: it counts the packet in the counter of its direction and
// does what the mode says.
func (f PMTUFault) chain() Chain {
	rules := []Rule{
		newRule(match(ctKey("direction"), "==", "original"), counter(f.CounterUp)),
		newRule(match(ctKey("direction"), "==", "reply"), counter(f.CounterDown)),
	}
	switch f.Mode {
	case PMTUModeICMP:
		rules = append(rules, newRule(markSet(bitOr(bitAnd(meta("mark"), int64(pmtuKeep)), int64(PMTUMarkOf(f.Index))))))
	case PMTUModeBlackhole:
		rules = append(rules, newRule(match(meta("length"), ">", f.Size), counter(f.CounterDrop), verdict("drop")))
	case PMTUModeMSSClamp:
		maxseg := map[string]any{"tcp option": map[string]any{"name": "maxseg", "field": "size"}}
		syn := eq(bitAnd(payload("tcp", "flags"), "syn"), "syn")
		rules = append(rules, newRule(syn, match(maxseg, ">", f.MSS()),
			map[string]any{"mangle": map[string]any{"key": maxseg, "value": f.MSS()}}))
	}
	rules = append(rules, newRule(verdict("return")))
	return Chain{Name: f.Chain, Rules: rules}
}

// pmtuRouting writes the mirror tables: for every index in use a copy of the policy table's routes
// with the size locked in, and the rule that sends traffic marked with the index through it.
func (t *Target) pmtuRouting() {
	if len(t.PMTUTables) == 0 {
		return
	}
	type tbl struct{ index, size int }
	var tabs []tbl
	for s, i := range t.PMTUTables {
		tabs = append(tabs, tbl{i, s})
	}
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].index < tabs[j].index })
	policy := make([]executor.Route, 0, len(t.Routes))
	for _, r := range t.Routes {
		if r.Table == PolicyTable {
			policy = append(policy, r)
		}
	}
	for _, x := range tabs {
		table := PMTUTableOf(x.index)
		for _, r := range policy {
			r.Table, r.MTU = table, x.size
			t.Routes = append(t.Routes, r)
		}
		t.Rules = append(t.Rules, executor.Rule{Action: "add", Family: 4, Priority: PMTURulePriority,
			Fwmark: fmt.Sprintf("0x%x/0x%x", PMTUMarkOf(x.index), PMTUMarkMask), Table: table})
	}
}

// pmtuBird adds the mirror tables to the BIRD configuration: learned routes go into them too, each
// with the size locked in (plan §2.2.2, §2.5: one kernel protocol per table).
func (t *Target) pmtuBird() {
	if t.Bird == nil || len(t.PMTUTables) == 0 {
		return
	}
	c := t.Bird.Config
	for s, i := range t.PMTUTables {
		c.Mirrors = append(c.Mirrors, bird.Mirror{Table: PMTUTableOf(i), MTU: s})
	}
	sort.Slice(c.Mirrors, func(i, j int) bool { return c.Mirrors[i].Table < c.Mirrors[j].Table })
	text, err := c.Render()
	if err != nil {
		t.errorf(CodeRouting, "", "the BIRD configuration with the PMTU mirror tables cannot be generated: %v", err)
		return
	}
	t.Bird.Config, t.Bird.Text = c, text
}

// pmtuClassify adds the second lookup to the nftables target: the four maps, the chain that does
// the lookups and the chain of every winner. It returns the rule of the classify chain that jumps to
// the lookup chain, or false when no MTU fault is in force (the classify chain is then what it was
// before the MTU family existed).
func (t *Target) pmtuClassify() (Rule, bool) {
	if t.pmtuBuild == nil {
		return Rule{}, false
	}
	t.PMTUMaps = map[string]string{}
	look := Chain{Name: PMTUClassifyChain}
	for i, lv := range classifyLevels {
		md := t.pmtuBuild.maps[i]
		md.Name = hashMapName("pmtu_"+lv.field, md.KeyType, md.ValueType, md.Flags)
		t.Nft.Maps = append(t.Nft.Maps, md)
		t.PMTUMaps[lv.field] = md.Name
	}
	look.Rules = append(look.Rules,
		newRule(vmap(concat(ctOriginalIP("saddr"), ctOriginalIP("daddr"), meta("l4proto"), ctOriginal("proto-dst")), t.PMTUMaps["devdestport"])),
		newRule(vmap(concat(ctOriginalIP("saddr"), ctOriginalIP("daddr")), t.PMTUMaps["devdest"])),
		newRule(vmap(concat(ctOriginalIP("saddr"), meta("l4proto"), ctOriginal("proto-dst")), t.PMTUMaps["devport"])),
		newRule(vmap(ctOriginalIP("saddr"), t.PMTUMaps["dev"])),
	)
	t.Nft.Chains = append(t.Nft.Chains, look)
	t.Nft.Chains = append(t.Nft.Chains, t.pmtuBuild.chains...)
	t.Nft.Counters = append(t.Nft.Counters, t.pmtuBuild.counters...)
	sort.Strings(t.Nft.Counters)
	return newRule(map[string]any{"jump": map[string]any{"target": PMTUClassifyChain}}), true
}
