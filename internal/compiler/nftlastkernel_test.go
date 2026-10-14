//go:build testbed

package compiler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// P3-M13-01 on the real kernel: the access rules and the anti-lockout rule are listed with their `last`
// statement as never used after a first apply, and the kernel takes the time a transaction carries over
// ({"last": {"used": <ms>}}) and counts on from it: the rule that replaces a rule that matched 1.5 s ago
// is listed as used 1.5 s ago and more, its twin in the other hook (which did not match) as never used.
// The traffic side (a real match sets the time) is TestARulesLastMatchIsTheKernelsAndAnApplyKeepsIt in
// internal/api.
func TestTheKernelListsTheLastMatchAndTakesItOverFromTheRuleItReplaces(t *testing.T) {
	bed := testbed.New(t)
	ns := bed.Add("nftlast")
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, protocol: udp, ports: [53], action: drop}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: tcp, ports: [2], action: reset}`)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	list := func() *linux.Ruleset {
		t.Helper()
		rs, err := linux.ParseNft([]byte(ns.Must("nft", "-j", "list", "table", "inet", "chaosgw")))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	ns.MustStdin(string(tx), "nft", "-j", "-f", "-")
	rs := list()
	a := tg.Access.Rules[0].Counter
	withLast := 0
	for _, c := range []string{AccessForwardChain, AccessInputChain, "input"} {
		for _, r := range rs.Rules(c) {
			used, set, has := r.Last()
			if !has {
				continue
			}
			withLast++
			if set {
				t.Errorf("%s %s: a rule nothing matched is listed as used %s ago", c, r.CounterName(), used)
			}
		}
	}
	if withLast != 5 { // two rules in two hooks, the anti-lockout rule
		t.Fatalf("rules listed with last: %d\n%s", withLast, ns.Must("nft", "list", "table", "inet", "chaosgw"))
	}

	// the listing as it would be had the forward rule of a matched 1.5 s ago
	for i, o := range rs.Objects {
		if o.Rule == nil || o.Rule.Chain != AccessForwardChain || o.Rule.CounterName() != a {
			continue
		}
		for j, raw := range o.Rule.Expr {
			var st map[string]json.RawMessage
			if json.Unmarshal(raw, &st) == nil {
				if _, ok := st["last"]; ok {
					rs.Objects[i].Rule.Expr[j] = json.RawMessage(`{"last":{"used":1500}}`)
				}
			}
		}
	}
	start := time.Now()
	tx, err = tg.Nft.Transaction(rs)
	if err != nil {
		t.Fatal(err)
	}
	ns.MustStdin(string(tx), "nft", "-j", "-f", "-")
	got := list()
	lasts := got.LastUsedIn()
	used, ok := lasts[linux.LastKey(AccessForwardChain, a)]
	if !ok || used < 1500*time.Millisecond || used > 1500*time.Millisecond+time.Since(start)+time.Second {
		t.Errorf("the carried last match is listed as %v (%v), want 1.5 s and the time since", used, ok)
	}
	if len(lasts) != 1 {
		t.Errorf("only the rule that matched has a time: %v", lasts)
	}
	if got.LastUsed()[a] != used {
		t.Errorf("the counter's last match is its most recent rule's: %v", got.LastUsed())
	}
	// the rules are the compiled ones: verify recognizes them by their comment
	for _, c := range []string{AccessForwardChain, AccessInputChain} {
		have := got.Rules(c)
		want := chainOf(t, tg, c).Rules
		if len(have) != len(want) {
			t.Fatalf("%s: %d rules, want %d", c, len(have), len(want))
		}
		for i := range want {
			if have[i].Comment != want[i].Comment {
				t.Errorf("%s rule %d: comment %q, want %q", c, i, have[i].Comment, want[i].Comment)
			}
		}
	}
}
