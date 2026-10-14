package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// lastOf returns the `last` statement of a compiled rule and the named counter it counts into.
func lastOf(expr []any) (counter string, last any, has bool) {
	for _, e := range expr {
		m, ok := e.(map[string]any)
		if !ok || len(m) != 1 {
			continue
		}
		if c, ok := m["counter"].(string); ok {
			counter = c
		}
		if v, ok := m["last"]; ok {
			last, has = v, true
		}
	}
	return counter, last, has
}

// P3-M13-01: every access rule (in both hooks) and the anti-lockout rule note when they last matched with
// the `last` statement, right after their named counter and before the verdict; no other rule does.
func TestEveryAccessRuleAndTheAntiLockoutRuleNoteTheirLastMatch(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, protocol: udp, ports: [53], action: drop}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: tcp, ports: [2], action: reset}`)
	w.ruleOverlay(`{target: {network: IoT}, rule: {action: reject}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	want := map[string]bool{}
	for _, r := range tg.Access.Rules {
		want[r.Counter] = true
	}
	if len(want) != 3 {
		t.Fatalf("rules: %+v", tg.Access.Rules)
	}
	seen := map[string]int{}
	for _, c := range tg.Nft.AllChains() {
		for _, r := range c.Rules {
			name, last, has := lastOf(r.Expr)
			if !has {
				if c.Name == AccessForwardChain || c.Name == AccessInputChain {
					t.Errorf("%s: an access rule without last: %s", c.Name, renderRule(r))
				}
				continue
			}
			if last != nil {
				t.Errorf("%s: a compiled rule starts as never used, not %v", c.Name, last)
			}
			switch {
			case (c.Name == AccessForwardChain || c.Name == AccessInputChain) && want[name]:
			case c.Name == "input" && name == AntiLockoutCounter:
			default:
				t.Errorf("%s: a rule that is no access rule notes its last match: %s", c.Name, renderRule(r))
			}
			if s := renderRule(r); !strings.Contains(s, fmt.Sprintf("counter %q last ", name)) {
				t.Errorf("last does not follow the counter: %s", s)
			}
			seen[c.Name]++
		}
	}
	if seen[AccessForwardChain] != 3 || seen[AccessInputChain] != 3 || seen["input"] != 1 {
		t.Errorf("rules with last by chain: %v", seen)
	}
}

// An apply flushes the compiled chains and fills them anew; the rule that replaces a rule of the same
// chain and named counter starts with the time its predecessor last matched ({"last": {"used": <ms>}}),
// so an apply never resets the rule editor's "last match". A rule whose predecessor did not match, a new
// rule and every rule of a first apply start as never used; the comment stays the hash of the compiled
// expression, so verify recognizes the rule.
func TestATransactionCarriesTheLastMatchOverToTheRuleThatReplacesIt(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, protocol: udp, ports: [53], action: drop}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: tcp, ports: [2], action: reset}`)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	a, b := tg.Access.Rules[0].Counter, tg.Access.Rules[1].Counter
	rule := func(chain, counter, last string) string {
		return fmt.Sprintf(`{"rule":{"family":"inet","table":"chaosgw","chain":%q,"handle":9,"comment":"x","expr":[{"counter":%q},{"last":%s},{"drop":null}]}}`, chain, counter, last)
	}
	current, err := linux.ParseNft([]byte(`{"nftables":[` + strings.Join([]string{
		rule(AccessForwardChain, a, `{"used":1500}`),
		rule(AccessInputChain, a, `null`),
		rule(AccessForwardChain, b, `{"used":90061}`),
		rule("input", AntiLockoutCounter, `{"used":42}`),
		// a rule of another chain with the same counter carries nothing to the access chains
		rule("forward", b, `{"used":7}`),
	}, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}

	added := func(tx []byte) map[string]any {
		t.Helper()
		var doc struct {
			Nftables []map[string]map[string]map[string]any `json:"nftables"`
		}
		if err := json.Unmarshal(tx, &doc); err != nil {
			t.Fatal(err)
		}
		comments := map[string]string{}
		for _, c := range tg.Nft.AllChains() {
			for _, r := range c.Rules {
				if n, _, has := lastOf(r.Expr); has {
					comments[c.Name+" "+n] = r.Comment
				}
			}
		}
		out := map[string]any{}
		for _, cmd := range doc.Nftables {
			r, ok := cmd["add"]["rule"]
			if !ok {
				continue
			}
			expr, _ := r["expr"].([]any)
			name, last, has := lastOf(expr)
			if !has {
				continue
			}
			k := r["chain"].(string) + " " + name
			if r["comment"] != comments[k] {
				t.Errorf("%s: comment %v, the compiled rule's is %s", k, r["comment"], comments[k])
			}
			out[k] = last
		}
		return out
	}
	tx, err := tg.Nft.Transaction(current)
	if err != nil {
		t.Fatal(err)
	}
	got := added(tx)
	for k, want := range map[string]string{
		AccessForwardChain + " " + a:  `{"used":1500}`,
		AccessInputChain + " " + a:    `null`,
		AccessForwardChain + " " + b:  `{"used":90061}`,
		AccessInputChain + " " + b:    `null`,
		"input " + AntiLockoutCounter: `{"used":42}`,
	} {
		if js(got[k]) != want {
			t.Errorf("%s: last %s, want %s", k, js(got[k]), want)
		}
	}
	if len(got) != 5 {
		t.Errorf("rules with last: %v", got)
	}
	// the compiled target is not changed by it: the next transaction without a kernel state starts anew
	first, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range added(first) {
		if v != nil {
			t.Errorf("first apply: %s starts with %v", k, js(v))
		}
	}
	// nft's parser takes the carried statement (the kernel is the next step: unprivileged it refuses)
	nft, err := exec.LookPath("nft")
	if err != nil {
		return
	}
	cmd := exec.Command(nft, "-j", "-c", "-f", "-")
	cmd.Stdin = bytes.NewReader(tx)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil && !strings.Contains(stderr.String(), "Operation not permitted") {
		t.Errorf("nft rejects the transaction with a carried last match:\n%s", stderr.String())
	}
}
