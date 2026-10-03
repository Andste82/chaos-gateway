package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

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

// Nft is the target of the nftables table `inet chaosgw`.
type Nft struct {
	Sets     []SetDef `json:"sets"`
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
		keepSet, keepCounter := map[string]bool{}, map[string]bool{}
		for _, s := range n.Sets {
			keepSet[s.Name] = true
		}
		for _, c := range n.Counters {
			keepCounter[c] = true
		}
		var oldChains, oldSets, oldCounters []string
		for _, o := range current.Objects {
			switch {
			case o.Chain != nil && !keepChain[o.Chain.Name]:
				oldChains = append(oldChains, o.Chain.Name)
			case o.Set != nil && !keepSet[o.Set.Name]:
				oldSets = append(oldSets, o.Set.Name)
			case o.Map != nil && !keepSet[o.Map.Name]:
				oldSets = append(oldSets, o.Map.Name)
			case o.Counter != nil && !keepCounter[o.Counter.Name]:
				oldCounters = append(oldCounters, o.Counter.Name)
			}
		}
		sort.Strings(oldChains)
		sort.Strings(oldSets)
		sort.Strings(oldCounters)
		// a removed chain is emptied first: its rules may refer to sets and counters that go too
		for _, c := range oldChains {
			cmds = append(cmds, cmd("flush", "chain", map[string]any{"name": c}))
		}
		for _, c := range oldChains {
			cmds = append(cmds, cmd("delete", "chain", map[string]any{"name": c}))
		}
		for _, s := range oldSets {
			cmds = append(cmds, cmd("delete", "set", map[string]any{"name": s}))
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
