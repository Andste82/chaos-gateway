package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	// Elements are the compiled content of a static set, flushed and refilled at every apply.
	Elements []string `json:"elements,omitempty"`
	// Dynamic sets (filled at run time, such as the DNS-derived address sets) are never flushed:
	// their elements survive every apply.
	Dynamic bool `json:"dynamic,omitempty"`
}

// MapDef is a named map: a key (one or more concatenated nft types) to a value (plan §3.3). Like
// SetDef its name carries a short hash of the definition, so a changed key or value type creates a
// new map instead of a failing `add map`. A map is always flushed and refilled at a full apply,
// like the Phase 1 per-device sets it replaces for device identity (dhcp.go); an identity-only or
// classification-only change updates its elements directly instead (executor.NftAddMapElements,
// NftDelMapElements), the map counterpart of a SetDef's incremental element update.
type MapDef struct {
	Name string `json:"name"`
	// KeyType is the map's key: one nft type, or several for a concatenated key (the lookup chain's
	// device, destination, protocol and port, plan §3.3).
	KeyType []string `json:"key_type"`
	// ValueType is the map's data type: "mark" for a plain integer value (the identity map's device
	// numeral) or "verdict" for a map whose elements jump to a chain (a classification map).
	ValueType string `json:"value_type"`
	// Elements are the compiled content, flushed and refilled at every apply. A MapElement's Key
	// joins KeyType's parts with " . " (nft's own concatenation syntax); its Value is a decimal
	// integer for ValueType "mark", or the chain an element jumps to for ValueType "verdict".
	Elements []MapElement `json:"elements,omitempty"`
}

// MapElement is one key/value pair of a MapDef.
type MapElement struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// hashMapName is hashName for a MapDef: a changed key or value type creates a new map name.
func hashMapName(base string, keyType []string, valueType string) string {
	h := sha256.Sum256([]byte(strings.Join(keyType, ".") + "|" + valueType))
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
// re-created (a no-op for what exists), the compiled chains and sets are flushed and refilled,
// and objects that are in `current` but no longer in the target are deleted at the end. Dynamic
// sets and all named counters that stay are never flushed or reset.
func (n Nft) Transaction(current *linux.Ruleset) ([]byte, error) {
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
		cmds = append(cmds, cmd("add", "map", map[string]any{"name": m.Name, "type": typ, "map": m.ValueType}))
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
	for _, s := range n.Sets {
		if s.Dynamic {
			continue
		}
		cmds = append(cmds, cmd("flush", "set", map[string]any{"name": s.Name}))
		if len(s.Elements) > 0 {
			cmds = append(cmds, cmd("add", "element", map[string]any{"name": s.Name, "elem": setElements(s)}))
		}
	}
	for _, m := range n.Maps {
		cmds = append(cmds, cmd("flush", "map", map[string]any{"name": m.Name}))
		if len(m.Elements) > 0 {
			cmds = append(cmds, cmd("add", "element", map[string]any{"name": m.Name, "elem": mapElements(m)}))
		}
	}
	for _, c := range n.chains() {
		for _, r := range c.Rules {
			cmds = append(cmds, cmd("add", "rule", map[string]any{"chain": c.Name, "expr": r.Expr, "comment": r.Comment}))
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

// mapElements renders the compiled content of a map: each MapElement's Key becomes a concatenation
// of its " . "-joined parts (or the bare part alone, for a one-field key), and its Value becomes
// the data: a decimal number for a "mark" map, a jump to the chain it names for a "verdict" map.
func mapElements(m MapDef) []any {
	out := make([]any, 0, len(m.Elements))
	for _, e := range m.Elements {
		out = append(out, map[string]any{"elem": map[string]any{"key": mapKeyExpr(e.Key), "val": mapValueExpr(m.ValueType, e.Value)}})
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
	if n, err := strconv.Atoi(part); err == nil {
		return n
	}
	return part
}

// mapValueExpr renders a map element's data per the map's value type.
func mapValueExpr(valueType, value string) any {
	if valueType == "verdict" {
		return map[string]any{"jump": map[string]any{"target": value}}
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
