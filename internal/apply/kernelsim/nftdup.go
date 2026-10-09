package kernelsim

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// The netdev table of the duplication hook (executor.NftDup): base chains on the egress hook of an
// interface, each with its rules. The simulator keeps what the transaction of the executor says and
// prints it the way `nft -j list` does (netdev base chains carry their interface as "dev"). It refuses a
// chain on an interface that does not exist, as the kernel does.

type dupChain struct {
	dev, typ, hook, policy string
	prio                   int
	rules                  []simRule
}

type simRule struct {
	comment string
	expr    json.RawMessage
}

type dupTable struct {
	chains map[string]*dupChain
}

func (t *dupTable) clone() *dupTable {
	c := &dupTable{chains: map[string]*dupChain{}}
	for n, ch := range t.chains {
		cc := *ch
		cc.rules = append([]simRule(nil), ch.rules...)
		c.chains[n] = &cc
	}
	return c
}

func (t *dupTable) list() any {
	h := 0
	next := func() int { h++; return h }
	out := []any{
		map[string]any{"metainfo": map[string]any{"version": "1.1.6", "json_schema_version": 1}},
		map[string]any{"table": map[string]any{"family": "netdev", "name": "chaosgw_dup", "handle": next()}},
	}
	var names []string
	for n := range t.chains {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := t.chains[n]
		out = append(out, map[string]any{"chain": map[string]any{"family": "netdev", "table": "chaosgw_dup", "name": n, "handle": next(),
			"dev": c.dev, "type": c.typ, "hook": c.hook, "prio": c.prio, "policy": c.policy}})
		for _, r := range c.rules {
			out = append(out, map[string]any{"rule": map[string]any{"family": "netdev", "table": "chaosgw_dup", "chain": n, "handle": next(),
				"comment": r.comment, "expr": r.expr}})
		}
	}
	return map[string]any{"nftables": out}
}

// DupDevs lists the interfaces that have a chain of the duplication hook.
func (k *Kernel) DupDevs() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.dup == nil {
		return nil
	}
	var out []string
	for _, c := range k.dup.chains {
		out = append(out, c.dev)
	}
	sort.Strings(out)
	return out
}

// BreakDup changes the hook behind the caller's back, for the tests of the verify: the rule of the chain
// of dev loses its comment (a rule that is not the executor's), or the chain is deleted.
func (k *Kernel) BreakDup(dev string, deleteChain bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.dup == nil {
		return
	}
	for n, c := range k.dup.chains {
		if c.dev != dev {
			continue
		}
		if deleteChain {
			delete(k.dup.chains, n)
		} else {
			c.rules = []simRule{{comment: "damaged", expr: json.RawMessage(`[]`)}}
		}
	}
}

// ResetDup removes the table of the hook behind the caller's back.
func (k *Kernel) ResetDup() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.dup = nil
}

func (k *Kernel) dupApply(stdin string) (executor.Result, error) {
	var doc struct {
		Nftables []map[string]map[string]map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(stdin), &doc); err != nil {
		return executor.Result{Exit: 1, Stderr: "Error: syntax error: " + err.Error() + "\n"}, nil
	}
	work := k.dup
	if work != nil {
		work = work.clone()
	}
	fail := func(i int, format string, a ...any) (executor.Result, error) {
		return executor.Result{Exit: 1, Stderr: fmt.Sprintf("Error: "+format+"\n(command %d)\n", append(a, i)...)}, nil
	}
	for i, item := range doc.Nftables {
		for command, objs := range item {
			for kind, f := range objs {
				if str(f, "family") != "netdev" {
					return fail(i, "the simulated netdev table takes netdev objects only")
				}
				switch {
				case kind == "table" && command == "add":
					if work == nil {
						work = &dupTable{chains: map[string]*dupChain{}}
					}
				case kind == "table" && command == "delete":
					if work == nil {
						return fail(i, "No such file or directory")
					}
					work = nil
				case kind == "chain" && command == "add":
					if work == nil {
						return fail(i, "No such file or directory")
					}
					dev := str(f, "dev")
					l := k.links[dev]
					if l == nil {
						return fail(i, "Could not process rule: No such file or directory (device %q)", dev)
					}
					if str(f, "hook") != "egress" || str(f, "type") != "filter" {
						return fail(i, "the simulated netdev chain is a filter on the egress hook")
					}
					var prio int
					_ = json.Unmarshal(f["prio"], &prio)
					work.chains[str(f, "name")] = &dupChain{dev: dev, typ: "filter", hook: "egress", policy: str(f, "policy"), prio: prio}
				case kind == "rule" && command == "add":
					if work == nil || work.chains[str(f, "chain")] == nil {
						return fail(i, "No such file or directory")
					}
					c := work.chains[str(f, "chain")]
					c.rules = append(c.rules, simRule{comment: str(f, "comment"), expr: f["expr"]})
				default:
					return fail(i, "the simulated netdev table does not know %s %s", command, kind)
				}
			}
		}
	}
	k.dup = work
	return executor.Result{}, nil
}
