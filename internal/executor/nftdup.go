package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// The duplication hook (M10, P2-M10-01). A fault that duplicates packets flags them in the classification
// (mark bit 21, written by the fault's mark chain with the fault's probability); the table below copies
// the flagged packets on their way out: one base chain per interface of the tc tree, hooked into the
// egress path of the interface (netdev family, kernel 5.16 and later), that clears the flag and sends a
// clone of the packet to the same interface (`dup to`). The clone passes the whole egress path again, tc
// included, so it meets the same class as the original, with its delay, loss and rate.
//
// The table is not the table `inet chaosgw` of NftApply, and its content is not free: the operation takes
// a list of interfaces and the executor writes the one rule the table may hold. The interfaces must be
// assigned to Chaos Gateway, like the ones of a tc operation.
//
// A first design used a tc hook (a clsact egress filter with `mirred egress mirror` to the same
// interface). It worked on kernel 6.8 and did nothing on 7.0, which refuses to mirror a packet back to the
// device it leaves through (the action counts an overlimit and the packet is not copied); nft's `dup`
// to the same interface works on both.
const (
	// NftDupFamily and NftDupTable name the table of the hook.
	NftDupFamily = "netdev"
	NftDupTable  = "chaosgw_dup"

	// DupMarkBit is the mark bit that asks for a copy (bit 21: plan §3.3), and dupMarkClear the mask that
	// clears it.
	DupMarkBit   uint32 = 1 << 21
	dupMarkClear uint32 = ^DupMarkBit
)

// NftDup sets the duplication hook to exactly the given interfaces: it creates the table with a chain and
// the rule for each, replaces whatever was there (in one nftables transaction: no packet meets half of
// it) and, with no interface, removes the table.
type NftDup struct {
	Target
	Devs []string `json:"devs"`
}

func (NftDup) OpType() string { return TypeNftDup }
func (NftDup) Mutates() bool  { return true }

func (o NftDup) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := checkDevs(o.Devs, true); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range o.Devs {
		if seen[d] {
			return fmt.Errorf("interface %q is listed twice", d)
		}
		seen[d] = true
	}
	return nil
}

// DupChainName is the chain of an interface.
func DupChainName(dev string) string { return "egress_" + dev }

// DupRuleExpr is the one rule of the chain of dev, as nft's JSON: if the flag is set, clear it (so that
// neither the packet nor its copy is copied again), then send a clone of the packet to the interface.
func DupRuleExpr(dev string) []any {
	return []any{
		map[string]any{"match": map[string]any{"op": "==",
			"left":  map[string]any{"&": []any{map[string]any{"meta": map[string]any{"key": "mark"}}, int64(DupMarkBit)}},
			"right": int64(DupMarkBit)}},
		map[string]any{"mangle": map[string]any{"key": map[string]any{"meta": map[string]any{"key": "mark"}},
			"value": map[string]any{"&": []any{map[string]any{"meta": map[string]any{"key": "mark"}}, int64(dupMarkClear)}}}},
		map[string]any{"dup": map[string]any{"addr": dev}},
	}
}

// DupRuleComment is the comment of the rule of dev: a hash of its expression, so that a read-back can
// tell the rule from another one without comparing expressions (the way the rules of the main table are
// recognized).
func DupRuleComment(dev string) string {
	b, _ := json.Marshal(DupRuleExpr(dev))
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:6])
}

// DupTransaction is the nftables JSON that makes the table what the interfaces say. The table is added
// (a no-op when it exists), deleted and added again in one transaction, which replaces its content
// atomically; with no interface it ends deleted.
func DupTransaction(devs []string) ([]byte, error) {
	devs = append([]string(nil), devs...)
	sort.Strings(devs)
	table := map[string]any{"family": NftDupFamily, "name": NftDupTable}
	cmds := []any{
		map[string]any{"add": map[string]any{"table": table}},
		map[string]any{"delete": map[string]any{"table": table}},
	}
	if len(devs) > 0 {
		cmds = append(cmds, map[string]any{"add": map[string]any{"table": table}})
	}
	for _, d := range devs {
		cmds = append(cmds,
			map[string]any{"add": map[string]any{"chain": map[string]any{"family": NftDupFamily, "table": NftDupTable,
				"name": DupChainName(d), "type": "filter", "hook": "egress", "dev": d, "prio": 0, "policy": "accept"}}},
			map[string]any{"add": map[string]any{"rule": map[string]any{"family": NftDupFamily, "table": NftDupTable,
				"chain": DupChainName(d), "comment": DupRuleComment(d), "expr": DupRuleExpr(d)}}})
	}
	return json.Marshal(map[string]any{"nftables": cmds})
}

func planNftDup(o *NftDup) ([]Step, error) {
	tx, err := DupTransaction(o.Devs)
	if err != nil {
		return nil, err
	}
	return []Step{{Cmd: Command{Tool: ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: string(tx), NS: o.NS}}}, nil
}
