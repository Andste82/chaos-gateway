package apply

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// ObjectSel names the kernel objects the compiler made for one object of the configuration (plan §2.17
// "Technical view": "Show Linux view" on a device, an access rule or a fault, docs/open-items.md
// P3-M14-03), by the names and keys the compiler gave them. The selection works on what the kernel holds,
// so the view shows the live state with its counters, not the target.
type ObjectSel struct {
	// Counters are named counters: each is shown with every rule that counts into it, in its chain.
	Counters map[string]bool
	// Chains are shown whole, Sets with all their elements.
	Chains map[string]bool
	Sets   map[string]bool
	// Element decides which elements of a map are shown: the map's name, the element's key and value as
	// linux.NftSet.Pairs gives them (an address, a prefix, a range or a concatenation joined with " . ";
	// a number or the chain a verdict goes to). Nil shows none.
	Element func(m, key, value string) bool
	// Classes are tc class ids ("1:22"): the class on every interface, the netem leaf below it and the
	// filters that lead to it.
	Classes map[string]bool
	// Flowers are the ingress flower filters by handle (a tunnel fault's filter on the uplink).
	Flowers map[int]bool
}

// ObjectText is the part of the kernel state an ObjectSel names, rendered as the technical view renders
// the whole (Render): the nftables part in nft syntax, the tc part one line per object led by the
// interface. Keys lists what was found, one name per kernel object ("counter rule_ab12", "chain mark_7",
// "map ident4_4c1a7e 10.10.0.5", "class br-iot 1:20"); empty when the kernel holds nothing of it.
type ObjectText struct {
	Keys         []string
	Nftables, TC string
}

// RenderObject renders what the selection names in the state read by ReadState.
func RenderObject(s *State, sel ObjectSel) ObjectText {
	var out ObjectText
	if s == nil {
		return out
	}
	if s.Nft != nil {
		rs, keys := selectNft(s.Nft, sel)
		out.Keys = append(out.Keys, keys...)
		if len(keys) > 0 {
			out.Nftables = linux.RenderNft(rs, maxSetElements)
		}
	}
	tc, keys := selectTC(s, sel)
	out.TC = tc
	out.Keys = append(out.Keys, keys...)
	return out
}

// selectNft returns the ruleset cut down to the selection: the table, the selected counters, sets and map
// elements, and the chains with the rules that are selected (a whole chain, or the rules that count into
// a selected counter).
func selectNft(rs *linux.Ruleset, sel ObjectSel) (*linux.Ruleset, []string) {
	out := &linux.Ruleset{JSONSchemaVersion: rs.JSONSchemaVersion}
	var keys []string
	// the chains with a selected rule
	ruleChains := map[string]bool{}
	for _, o := range rs.Objects {
		if o.Rule != nil && (sel.Chains[o.Rule.Chain] || sel.Counters[o.Rule.CounterName()]) {
			ruleChains[o.Rule.Chain] = true
		}
	}
	for _, o := range rs.Objects {
		switch {
		case o.Table != nil:
			out.Objects = append(out.Objects, o)
		case o.Counter != nil && sel.Counters[o.Counter.Name]:
			out.Objects = append(out.Objects, o)
			keys = append(keys, "counter "+o.Counter.Name)
		case o.Set != nil && sel.Sets[o.Set.Name]:
			out.Objects = append(out.Objects, o)
			keys = append(keys, "set "+o.Set.Name)
		case o.Map != nil && sel.Element != nil:
			m := *o.Map
			m.Elem = nil
			for _, raw := range o.Map.Elem {
				for k, v := range (&linux.NftSet{Elem: []json.RawMessage{raw}}).Pairs() {
					if sel.Element(o.Map.Name, k, v) {
						m.Elem = append(m.Elem, raw)
						keys = append(keys, fmt.Sprintf("map %s %s", o.Map.Name, k))
					}
				}
			}
			if len(m.Elem) > 0 {
				out.Objects = append(out.Objects, linux.NftObject{Map: &m})
			}
		case o.Chain != nil && (sel.Chains[o.Chain.Name] || ruleChains[o.Chain.Name]):
			out.Objects = append(out.Objects, o)
			if sel.Chains[o.Chain.Name] {
				keys = append(keys, "chain "+o.Chain.Name)
			}
		case o.Rule != nil && (sel.Chains[o.Rule.Chain] || sel.Counters[o.Rule.CounterName()]):
			out.Objects = append(out.Objects, o)
			if !sel.Chains[o.Rule.Chain] {
				keys = append(keys, fmt.Sprintf("rule %s %s", o.Rule.Chain, o.Rule.Comment))
			}
		}
	}
	return out, keys
}

// selectTC renders the selected classes, their netem leaves and the filters that lead to them on every
// interface the state holds a tree of, and the selected ingress filters.
func selectTC(s *State, sel ObjectSel) (string, []string) {
	if len(sel.Classes) == 0 && len(sel.Flowers) == 0 {
		return "", nil
	}
	devs := make([]string, 0, len(s.TC))
	for d := range s.TC {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	var b strings.Builder
	var keys []string
	for _, d := range devs {
		if s.TC[d] == nil {
			continue
		}
		t := s.TC[d].Sorted()
		for _, c := range t.Classes {
			if sel.Classes[c.ID] {
				fmt.Fprintf(&b, "%s: %s\n", d, c.Line())
				if c.Stats != nil {
					fmt.Fprintf(&b, "%s:   %s\n", d, statsText(*c.Stats))
				}
				keys = append(keys, fmt.Sprintf("class %s %s", d, c.ID))
			}
		}
		for _, q := range t.Qdiscs {
			if sel.Classes[q.Parent] {
				fmt.Fprintf(&b, "%s: %s\n", d, q.Line())
				if q.Stats != nil {
					fmt.Fprintf(&b, "%s:   %s\n", d, statsText(*q.Stats))
				}
				keys = append(keys, fmt.Sprintf("qdisc %s %s", d, q.Handle))
			}
		}
		for _, f := range t.Filters {
			if sel.Classes[f.Flowid] || (f.Flower != nil && sel.Flowers[f.Flower.Handle]) {
				fmt.Fprintf(&b, "%s: %s\n", d, f.Line())
				if f.Stats != nil {
					fmt.Fprintf(&b, "%s:   matched %d pkt %d bytes\n", d, f.Stats.Packets, f.Stats.Bytes)
				}
				keys = append(keys, fmt.Sprintf("filter %s %s pref %d", d, f.Parent, f.Pref))
			}
		}
	}
	return b.String(), keys
}
