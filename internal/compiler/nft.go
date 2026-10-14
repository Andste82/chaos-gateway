package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// BaseChain holds the attributes of a chain that hooks into the packet path.
type BaseChain struct {
	Type   string `json:"type"` // filter | nat
	Hook   string `json:"hook"`
	Prio   int    `json:"prio"`
	Policy string `json:"policy"`
}

// Rule is one rule of a chain. Expr is the nftables JSON expression list; the comment is derived
// from it, so verify can recognize the rule in the kernel's own rendering.
type Rule struct {
	Expr    []any  `json:"expr"`
	Comment string `json:"comment"`
}

// Chain is a chain of the table with its rules in order.
type Chain struct {
	Name  string     `json:"name"`
	Base  *BaseChain `json:"base,omitempty"`
	Rules []Rule     `json:"rules"`
}

// SetDef is a named set. The name carries a short hash of the definition (type and flags): a set
// with the same name but another type would make `add set` fail and with it the whole
// transaction, so a changed definition creates a new set and the old one is deleted (plan §3.2).
type SetDef struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Flags []string `json:"flags,omitempty"`
	// Elements are the compiled content of a static set; an apply changes the kernel's set to them by
	// their difference (Nft.Transaction).
	Elements []string `json:"elements,omitempty"`
	// Dynamic sets (filled at run time, such as the DNS-derived address sets) are never flushed:
	// their elements survive every apply.
	Dynamic bool `json:"dynamic,omitempty"`
}

// MapDef is a named map: a key (one or more concatenated nft types) to a value (plan §3.3). Like
// SetDef its name carries a short hash of the definition, so a changed key or value type creates a
// new map instead of a failing `add map`. A full apply changes a map's elements by the difference
// with what the kernel holds (Nft.Transaction, docs/open-items.md P2-M10-09); an identity-only or
// classification-only change updates its elements directly instead (executor.NftAddMapElements,
// NftDelMapElements), the map counterpart of a SetDef's incremental element update.
type MapDef struct {
	Name string `json:"name"`
	// KeyType is the map's key: one nft type, or several for a concatenated key (the lookup chain's
	// device, destination, protocol and port, plan §3.3).
	KeyType []string `json:"key_type"`
	// ValueType is the map's data type: "mark" for a plain integer value (the identity map's device
	// numeral) or "verdict" for a map whose elements go to a chain (a classification map).
	ValueType string `json:"value_type"`
	// Flags are the map's flags; "interval" lets keys be ranges and prefixes (the classification
	// maps, whose elements are disjoint ranges, plan §3.2 "overlapping selectors").
	Flags []string `json:"flags,omitempty"`
	// Elements are the compiled content (changed by the difference at every apply). A MapElement's Key
	// joins KeyType's parts with " . " (nft's own concatenation syntax); its Value is a decimal
	// integer for ValueType "mark", or the chain an element goes to for ValueType "verdict".
	Elements []MapElement `json:"elements,omitempty"`
}

// MapElement is one key/value pair of a MapDef.
type MapElement struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// hashMapName is hashName for a MapDef: a changed key type, value type or flag creates a new map
// name (`add map` fails for an existing map of the same name with other flags).
func hashMapName(base string, keyType []string, valueType string, flags []string) string {
	h := sha256.Sum256([]byte(strings.Join(keyType, ".") + "|" + valueType + "|" + fmt.Sprint(flags)))
	return base + "_" + hex.EncodeToString(h[:3])
}

// Nft is the target of the nftables table `inet chaosgw`.
type Nft struct {
	Sets     []SetDef `json:"sets"`
	Maps     []MapDef `json:"maps,omitempty"`
	Counters []string `json:"counters"`
	Chains   []Chain  `json:"chains"`
	// Generation is the comment of the single rule of the chain `generation`.
	Generation string `json:"generation"`
}

// GenerationChain is the chain whose one rule carries the generation id (plan §2.14).
const GenerationChain = "generation"

// hashName returns base plus a short hash of the definition.
func hashName(base, typ string, flags []string) string {
	h := sha256.Sum256([]byte(typ + "|" + fmt.Sprint(flags)))
	return base + "_" + hex.EncodeToString(h[:3])
}

func exprHash(expr []any) string {
	b, _ := json.Marshal(expr)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:6])
}

func newRule(expr ...any) Rule { return Rule{Expr: expr, Comment: exprHash(expr)} }

func nftObj(kind string, fields map[string]any) any {
	fields["family"] = executor.NftFamily
	fields["table"] = executor.NftTable
	return map[string]any{kind: fields}
}

func cmd(command, kind string, fields map[string]any) any {
	return map[string]any{command: nftObj(kind, fields)}
}

// Transaction builds the nftables JSON of one apply (plan §3.2): the static structure is
// re-created (a no-op for what exists), the compiled chains are flushed and refilled, the elements of
// the static sets and maps that `current` holds are changed by their difference with the target and
// those of new ones filled (setUpdate), and objects that are in `current` but no longer in the target
// are deleted at the end. Dynamic sets and all named counters that stay are never flushed or reset.
// Without `current` (a first apply, or a check of the syntax) every set and map is flushed and filled.
func (n Nft) Transaction(current *linux.Ruleset) ([]byte, error) { return n.transaction(current, true) }

// transaction is Transaction; with diff false every static set and map is flushed and filled, as
// every apply did before P2-M10-09 (kept for the kernel test that shows what that costs).
func (n Nft) transaction(current *linux.Ruleset, diff bool) ([]byte, error) {
	var cmds []any
	cmds = append(cmds, map[string]any{"add": map[string]any{"table": map[string]any{"family": executor.NftFamily, "name": executor.NftTable}}})

	for _, c := range n.Counters {
		cmds = append(cmds, cmd("add", "counter", map[string]any{"name": c}))
	}
	for _, s := range n.Sets {
		f := map[string]any{"name": s.Name, "type": s.Type}
		if len(s.Flags) > 0 {
			f["flags"] = s.Flags
		}
		cmds = append(cmds, cmd("add", "set", f))
	}
	for _, m := range n.Maps {
		typ := any(m.KeyType[0])
		if len(m.KeyType) > 1 {
			typ = m.KeyType
		}
		f := map[string]any{"name": m.Name, "type": typ, "map": m.ValueType}
		if len(m.Flags) > 0 {
			f["flags"] = m.Flags
		}
		cmds = append(cmds, cmd("add", "map", f))
	}
	for _, c := range n.chains() {
		f := map[string]any{"name": c.Name}
		if c.Base != nil {
			f["type"], f["hook"], f["prio"], f["policy"] = c.Base.Type, c.Base.Hook, c.Base.Prio, c.Base.Policy
		}
		cmds = append(cmds, cmd("add", "chain", f))
	}
	for _, c := range n.chains() {
		cmds = append(cmds, cmd("flush", "chain", map[string]any{"name": c.Name}))
	}
	base := current
	if !diff {
		base = nil
	}
	for _, s := range n.Sets {
		if s.Dynamic {
			continue
		}
		cmds = append(cmds, setUpdate(s, base)...)
	}
	for _, m := range n.Maps {
		cmds = append(cmds, mapUpdate(m, base)...)
	}
	// a flushed chain's rules start anew; the time a rule last matched is carried over to the rule that
	// replaces it (the same chain, the same named counter), so an apply does not make "last match" never
	lasts := current.LastUsedIn()
	for _, c := range n.chains() {
		for _, r := range c.Rules {
			cmds = append(cmds, cmd("add", "rule", map[string]any{"chain": c.Name, "expr": withLast(r.Expr, c.Name, lasts), "comment": r.Comment}))
		}
	}

	// removed objects: everything in the table is ours, so what the target does not name goes
	if current != nil {
		keepChain := map[string]bool{}
		for _, c := range n.chains() {
			keepChain[c.Name] = true
		}
		keepSet, keepMap, keepCounter := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, s := range n.Sets {
			keepSet[s.Name] = true
		}
		for _, m := range n.Maps {
			keepMap[m.Name] = true
		}
		for _, c := range n.Counters {
			keepCounter[c] = true
		}
		var oldChains, oldSets, oldMaps, oldCounters []string
		for _, o := range current.Objects {
			switch {
			case o.Chain != nil && !keepChain[o.Chain.Name]:
				oldChains = append(oldChains, o.Chain.Name)
			case o.Set != nil && !keepSet[o.Set.Name]:
				oldSets = append(oldSets, o.Set.Name)
			case o.Map != nil && !keepMap[o.Map.Name]:
				oldMaps = append(oldMaps, o.Map.Name)
			case o.Counter != nil && !keepCounter[o.Counter.Name]:
				oldCounters = append(oldCounters, o.Counter.Name)
			}
		}
		sort.Strings(oldChains)
		sort.Strings(oldSets)
		sort.Strings(oldMaps)
		sort.Strings(oldCounters)
		// a removed map is emptied before any chain goes: its elements can be verdicts that name chains (the
		// MTU maps go with their last fault, together with the chains their elements lead to), and a chain
		// that a map element still names is busy
		for _, m := range oldMaps {
			cmds = append(cmds, cmd("flush", "map", map[string]any{"name": m}))
		}
		// a removed chain is emptied first: its rules may refer to sets, maps and counters that go too
		for _, c := range oldChains {
			cmds = append(cmds, cmd("flush", "chain", map[string]any{"name": c}))
		}
		for _, c := range oldChains {
			cmds = append(cmds, cmd("delete", "chain", map[string]any{"name": c}))
		}
		for _, s := range oldSets {
			cmds = append(cmds, cmd("delete", "set", map[string]any{"name": s}))
		}
		for _, m := range oldMaps {
			cmds = append(cmds, cmd("delete", "map", map[string]any{"name": m}))
		}
		for _, c := range oldCounters {
			cmds = append(cmds, cmd("delete", "counter", map[string]any{"name": c}))
		}
	}
	return json.Marshal(map[string]any{"nftables": cmds})
}

// withLast returns the expression of a rule with its `last` statement set to the time the kernel's rule
// of the same chain and named counter last matched (lasts: linux.Ruleset.LastUsedIn), so the new rule
// starts as if it had matched then. The kernel takes {"last": {"used": <ms>}} as "matched <ms> ago";
// the time between the listing and the commit is lost, so the carried time is late by at most the
// length of the apply. A rule without a `last`, or one whose predecessor never matched, is unchanged.
// The comment (the hash of the compiled expression) stays the same, so verify recognizes the rule.
func withLast(expr []any, chain string, lasts map[string]time.Duration) []any {
	if len(lasts) == 0 {
		return expr
	}
	name, at := "", -1
	for i, e := range expr {
		m, ok := e.(map[string]any)
		if !ok || len(m) != 1 {
			continue
		}
		if c, ok := m["counter"].(string); ok {
			name = c
		}
		if v, ok := m["last"]; ok && v == nil {
			at = i
		}
	}
	used, ok := lasts[linux.LastKey(chain, name)]
	if name == "" || at < 0 || !ok {
		return expr
	}
	out := append([]any(nil), expr...)
	out[at] = map[string]any{"last": map[string]any{"used": used.Milliseconds()}}
	return out
}

// setUpdate is the part of a transaction that gives a static set the elements of the target. A set
// the kernel holds already (current names it) is changed by the difference: the elements that are
// gone are deleted, the new ones added, and an element that stays is not touched. A set that is new,
// or whose listing cannot be read completely, is flushed and filled.
//
// Not flush and fill (docs/open-items.md P2-M10-09): the kernel's interval sets can miss a lookup
// while a transaction that holds old and new copies of the same element commits. A flush makes every
// element old for that moment, so a packet of a flow whose classification did not change at all
// passed unclassified now and then while another fault was written. The kernel test
// TestAFlowKeepsItsClassificationWhileOtherFaultsAreWritten measured it on the native CI runner
// (6.17.0-1022-azure): with flush and fill, 113 of 152920 packets through the single-field interval
// map (rbtree) and 67 of 159680 through the concatenated one (pipapo) in 540 transactions were not
// classified, by difference none; on 6.8.0-142-generic (the minimum supported kernel, level 1b) flush
// and fill missed 15 and 136 in 81 transactions, the difference none. Upstream has worked on such
// false negatives in 2025 ("netfilter: nft_set_pipapo: don't check genbit from packetpath lookups",
// "netfilter: nft_set_rbtree: continue traversal if element is inactive", "netfilter: nf_tables:
// restart set lookup on base_seq change"), but a kernel the gateway runs on cannot be assumed to have
// all of it. An element that is not touched is active in both generations and is always found.
func setUpdate(s SetDef, current *linux.Ruleset) []any {
	var have map[string]json.RawMessage
	ok := false
	if current != nil {
		if got := current.Set(s.Name); got != nil {
			have, ok = got.RawElements()
		}
	}
	if !ok {
		out := []any{cmd("flush", "set", map[string]any{"name": s.Name})}
		if len(s.Elements) > 0 {
			out = append(out, cmd("add", "element", map[string]any{"name": s.Name, "elem": setElements(s)}))
		}
		return out
	}
	want := make(map[string]bool, len(s.Elements))
	var add []any
	for _, e := range s.Elements {
		k := linux.NormalizeElement(e)
		if want[k] {
			continue
		}
		want[k] = true
		if _, there := have[k]; !there {
			add = append(add, setElement(s.Type, e))
		}
	}
	var gone []string
	for k := range have {
		if !want[k] {
			gone = append(gone, k)
		}
	}
	return elementCommands(s.Name, gone, have, add)
}

// mapUpdate is setUpdate for a map: a key that is gone or whose value changed is deleted, a key that
// is new or whose value changed is added, and an element that stays as it is is not touched. (An
// element whose value changes is deleted and added in the same commit: its traffic moves to another
// chain, and like a flushed element it can miss for the moment of the commit.)
func mapUpdate(m MapDef, current *linux.Ruleset) []any {
	var have map[string]json.RawMessage
	var haveVal map[string]string
	ok := false
	if current != nil {
		if got := current.Set(m.Name); got != nil {
			have, ok = got.RawKeys()
			haveVal = got.Pairs()
		}
	}
	if !ok {
		out := []any{cmd("flush", "map", map[string]any{"name": m.Name})}
		if len(m.Elements) > 0 {
			out = append(out, cmd("add", "element", map[string]any{"name": m.Name, "elem": mapElements(m)}))
		}
		return out
	}
	want := make(map[string]string, len(m.Elements))
	var add []MapElement
	var changed []string
	for _, e := range m.Elements {
		k := linux.NormalizeElement(e.Key)
		if _, dup := want[k]; dup {
			continue
		}
		want[k] = e.Value
		if _, there := have[k]; there {
			if haveVal[k] == e.Value {
				continue
			}
			changed = append(changed, k)
		}
		add = append(add, e)
	}
	gone := changed
	for k := range have {
		if _, ok := want[k]; !ok {
			gone = append(gone, k)
		}
	}
	var adds []any
	if len(add) > 0 {
		d := m
		d.Elements = add
		adds = mapElements(d)
	}
	return elementCommands(m.Name, gone, have, adds)
}

// elementCommands deletes the elements named by gone (by the JSON the kernel printed for them) and
// then adds add: in this order, because an interval set refuses an element that overlaps one that is
// still there, and both are in the same commit.
func elementCommands(name string, gone []string, raw map[string]json.RawMessage, add []any) []any {
	var out []any
	if len(gone) > 0 {
		sort.Strings(gone)
		elem := make([]any, len(gone))
		for i, k := range gone {
			elem[i] = raw[k]
		}
		out = append(out, cmd("delete", "element", map[string]any{"name": name, "elem": elem}))
	}
	if len(add) > 0 {
		out = append(out, cmd("add", "element", map[string]any{"name": name, "elem": add}))
	}
	return out
}

// chains returns the chains of the target including the generation chain, which every apply
// flushes and rewrites.
func (n Nft) chains() []Chain {
	out := append([]Chain(nil), n.Chains...)
	gen := newRule(map[string]any{"accept": nil})
	gen.Comment = n.Generation
	return append(out, Chain{Name: GenerationChain, Rules: []Rule{gen}})
}

// AllChains is chains() for callers outside the package (verify).
func (n Nft) AllChains() []Chain { return n.chains() }

func setElements(s SetDef) []any {
	var out []any
	for _, e := range s.Elements {
		out = append(out, setElement(s.Type, e))
	}
	return out
}

// setElement renders one element: prefixes of address sets become prefix objects.
func setElement(typ, e string) any {
	if typ == "ipv4_addr" {
		if addr, bits, ok := splitPrefix(e); ok {
			return map[string]any{"prefix": map[string]any{"addr": addr, "len": bits}}
		}
	}
	return e
}

// mapElements renders the compiled content of a map: each entry is a [key, value] pair
// (libnftables-json's SET_ELEM: "for mappings, an array of arrays with exactly two elements is
// expected" - confirmed against a real captured `nft -j list map`, "elem": [[9001, {"drop":
// null}], ...]). The key becomes a concatenation of its " . "-joined parts (or the bare part
// alone, for a one-field key), and the value becomes the data: a decimal number for a "mark" map,
// a goto to the chain it names for a "verdict" map.
func mapElements(m MapDef) []any {
	out := make([]any, 0, len(m.Elements))
	for _, e := range m.Elements {
		out = append(out, []any{mapKeyExpr(e.Key), mapValueExpr(m.ValueType, e.Value)})
	}
	return out
}

// mapKeyExpr renders a map key: " . "-joined parts concatenated, a single part as itself. An
// address part becomes a prefix object, like setElement.
func mapKeyExpr(key string) any {
	parts := strings.Split(key, " . ")
	if len(parts) == 1 {
		return mapKeyPart(parts[0])
	}
	out := make([]any, len(parts))
	for i, p := range parts {
		out[i] = mapKeyPart(p)
	}
	return map[string]any{"concat": out}
}

func mapKeyPart(part string) any {
	if addr, bits, ok := splitPrefix(part); ok {
		return map[string]any{"prefix": map[string]any{"addr": addr, "len": bits}}
	}
	// "first-last": an interval element (addresses or ports)
	if lo, hi, ok := strings.Cut(part, "-"); ok {
		if a, err := strconv.Atoi(lo); err == nil {
			if b, err := strconv.Atoi(hi); err == nil {
				return map[string]any{"range": []any{a, b}}
			}
		} else {
			return map[string]any{"range": []any{lo, hi}}
		}
	}
	if n, err := strconv.Atoi(part); err == nil {
		return n
	}
	return part
}

// mapValueExpr renders a map element's data per the map's value type.
func mapValueExpr(valueType, value string) any {
	if valueType == "verdict" {
		// goto, not jump: after a jump the kernel continues with the NEXT RULE of the calling chain
		// (the rest of the rule that did the lookup is not run), so the lookup chain's next level would
		// run too and overwrite the mark. A goto does not come back: the chain ends, and with it the
		// base chain's evaluation (plan §3.3 first match; proven in the VM, M8a).
		return map[string]any{"goto": map[string]any{"target": value}}
	}
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	return value
}

func splitPrefix(s string) (string, int, bool) {
	var addr string
	var bits int
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			addr = s[:i]
			if _, err := fmt.Sscanf(s[i+1:], "%d", &bits); err != nil {
				return "", 0, false
			}
			return addr, bits, true
		}
	}
	return "", 0, false
}

// MapUpdate is the change of the elements of one map between two targets: the keys to delete and
// the elements to add. A key whose value changed is in both lists (a map add refuses a key that
// still exists), so Delete runs first.
type MapUpdate struct {
	Map    string       `json:"map"`
	Delete []string     `json:"delete,omitempty"`
	Add    []MapElement `json:"add,omitempty"`
}

// Empty reports whether the update changes nothing.
func (u MapUpdate) Empty() bool { return len(u.Delete) == 0 && len(u.Add) == 0 }

// DiffMap compares the elements of two maps of the same definition. Keys and values are compared as
// the compiler wrote them, which is the canonical form nft prints.
func DiffMap(old, next MapDef) MapUpdate {
	have := make(map[string]string, len(old.Elements))
	for _, e := range old.Elements {
		have[e.Key] = e.Value
	}
	want := make(map[string]string, len(next.Elements))
	for _, e := range next.Elements {
		want[e.Key] = e.Value
	}
	u := MapUpdate{Map: next.Name}
	for k, v := range want {
		if hv, ok := have[k]; !ok {
			u.Add = append(u.Add, MapElement{Key: k, Value: v})
		} else if hv != v {
			u.Delete = append(u.Delete, k)
			u.Add = append(u.Add, MapElement{Key: k, Value: v})
		}
	}
	for k := range have {
		if _, ok := want[k]; !ok {
			u.Delete = append(u.Delete, k)
		}
	}
	sort.Slice(u.Add, func(i, j int) bool { return u.Add[i].Key < u.Add[j].Key })
	sort.Strings(u.Delete)
	return u
}

// ElementTransaction is the nftables JSON of one atomic transaction that applies the updates to the
// maps of the target: the deletes first, then the adds, in one batch, so a packet never sees a
// half-changed classification (an element that moves is deleted and added in the same commit). It
// is for interval maps, where an element cannot be added while an overlapping one still exists.
func (n Nft) ElementTransaction(updates []MapUpdate) ([]byte, error) {
	defs := map[string]MapDef{}
	for _, m := range n.Maps {
		defs[m.Name] = m
	}
	var cmds []any
	for _, u := range updates {
		if _, ok := defs[u.Map]; !ok {
			return nil, fmt.Errorf("compiler: the target has no map %s", u.Map)
		}
		if len(u.Delete) > 0 {
			keys := make([]any, len(u.Delete))
			for i, k := range u.Delete {
				keys[i] = mapKeyExpr(k)
			}
			cmds = append(cmds, cmd("delete", "element", map[string]any{"name": u.Map, "elem": keys}))
		}
	}
	for _, u := range updates {
		if len(u.Add) > 0 {
			d := defs[u.Map]
			d.Elements = u.Add
			cmds = append(cmds, cmd("add", "element", map[string]any{"name": u.Map, "elem": mapElements(d)}))
		}
	}
	return json.Marshal(map[string]any{"nftables": cmds})
}
