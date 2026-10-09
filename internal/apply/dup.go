package apply

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The duplication hook in the apply (M10, P2-M10-01): the netdev table that copies the packets the
// classification flagged (executor.NftDup). It stands on the interfaces of the tc tree (Target.DupDevs)
// while some fault duplicates and nowhere else. Each interface has a base chain of the executor's own
// shape and the one rule it writes; anything else in the table, or a chain that is not what the executor
// writes, is damage the apply repairs by writing the table again.
//
// Order: the table is written before the transaction that makes the classification flag packets (so no
// flagged packet leaves an interface that cannot copy it), and deleted after the transaction that stops
// flagging them (a flagged packet that meets no hook is merely not copied, and the flag is harmless).

// dupProblems lists what is wrong with the table the kernel holds, compared with the interfaces that are to
// have the hook; empty when it is exactly right.
func dupProblems(want []string, rs *linux.Ruleset) []string {
	wantSet := map[string]bool{}
	for _, d := range want {
		wantSet[d] = true
	}
	var out []string
	have := map[string]bool{}
	for _, o := range rs.Objects {
		c := o.Chain
		if c == nil {
			continue
		}
		have[c.Dev] = true
		switch {
		case !wantSet[c.Dev]:
			out = append(out, fmt.Sprintf("the chain %s on %q is not wanted", c.Name, c.Dev))
		case c.Name != executor.DupChainName(c.Dev) || c.Type != "filter" || c.Hook != "egress" || c.Prio == nil || *c.Prio != 0 || c.Policy != "accept":
			out = append(out, fmt.Sprintf("the chain %s on %q is not the executor's (type %s, hook %s, policy %s)", c.Name, c.Dev, c.Type, c.Hook, c.Policy))
		default:
			rules := rs.Rules(c.Name)
			if len(rules) != 1 || rules[0].Comment != executor.DupRuleComment(c.Dev) {
				out = append(out, fmt.Sprintf("the chain %s on %q does not hold the one rule of the hook (%d rules)", c.Name, c.Dev, len(rules)))
			}
		}
	}
	for _, d := range want {
		if !have[d] {
			out = append(out, fmt.Sprintf("%s has no hook", d))
		}
	}
	if len(want) == 0 && len(rs.Tables()) > 0 && len(out) == 0 {
		out = append(out, "the table of the hook is there, nothing duplicates")
	}
	sort.Strings(out)
	return out
}

// dupSummary says what the apply does about the hook, for the plan's text.
func dupSummary(want []string) string {
	if len(want) == 0 {
		return "nft: the duplication hook goes"
	}
	return "nft: duplication hook on " + strings.Join(want, ", ")
}
