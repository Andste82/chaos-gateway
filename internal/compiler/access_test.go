package compiler

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ---- the packet path ----------------------------------------------------------------------------

func TestWithoutRulesThereIsNoAccessMachinery(t *testing.T) {
	tg := accessWorld(t).compile(nil)
	if tg.Access != nil {
		t.Fatalf("access = %+v", tg.Access)
	}
	for _, name := range []string{AccessForwardChain, AccessInputChain, CutForwardChain, CutInputChain} {
		if hasChain(tg, name) {
			t.Errorf("chain %s without a rule", name)
		}
	}
	for _, name := range []string{"forward", "input"} {
		if i := indexOfRule(chainOf(t, tg, name), "jump"); i >= 0 {
			t.Errorf("%s jumps somewhere without a rule: %s", name, renderRule(chainOf(t, tg, name).Rules[i]))
		}
	}
}

func TestARuleIsEvaluatedAfterTheProtectionsAndBeforeTheMatrixAndTheGatewaysAnswers(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: no-dns, source: {device: esp32-42}, protocol: udp, ports: [53], action: drop}`)
	tg := w.compile(nil)

	fwd := chainOf(t, tg, "forward")
	jump := indexOfRule(fwd, "jump "+AccessForwardChain)
	if jump < 0 {
		t.Fatalf("forward does not run the access rules: %v", renderRules(fwd))
	}
	for _, before := range []string{"ct state { established, related } accept", "ct state { invalid } drop", "meta nfproto ipv6"} {
		if i := indexOfRule(fwd, before); i < 0 || i > jump {
			t.Errorf("forward: %q must stand before the access rules (at %d, rules at %d)\n%v", before, i, jump, renderRules(fwd))
		}
	}
	// the matrix and the default stand behind the rules
	for _, after := range []string{"counter \"forward_drop\" drop"} {
		if i := indexOfRule(fwd, after); i < jump {
			t.Errorf("forward: %q must stand behind the access rules (at %d, rules at %d)", after, i, jump)
		}
	}

	in := chainOf(t, tg, "input")
	jump = indexOfRule(in, "jump "+AccessInputChain)
	if jump < 0 {
		t.Fatalf("input does not run the access rules: %v", renderRules(in))
	}
	anti := indexOfRule(in, `counter "anti_lockout" accept`)
	ui := indexOfRule(in, `counter "input_drop" drop`)
	dns := indexOfRule(in, "udp dport 53 accept")
	last := indexOfRule(in, `iifname @ifs_test`, `counter "input_drop" drop`)
	if anti < 0 || anti >= ui || ui >= jump || jump >= dns || dns >= last {
		t.Errorf("input order: anti-lockout %d, UI port %d, access rules %d, DNS answer %d, last drop %d\n%v", anti, ui, jump, dns, last, renderRules(in))
	}

	rule := chainOf(t, tg, AccessForwardChain).Rules
	want := fmt.Sprintf(`ct original ip saddr @%s meta l4proto udp ct original proto-dst 53 counter %q drop`, scopeSetName(model.Scope{Device: ptr(devESP42)}), ruleCounterName("config:"+ruleA))
	if len(rule) != 1 || renderRule(rule[0]) != want {
		t.Fatalf("access_forward = %v\nwant %s", renderRules(chainOf(t, tg, AccessForwardChain)), want)
	}
}

func renderRules(c Chain) []string {
	out := make([]string, len(c.Rules))
	for i, r := range c.Rules {
		out[i] = renderRule(r)
	}
	return out
}

// The anti-lockout rule and everything that stands in front of the access rules is what it is
// without rules: no rule, however broad, and no overlay changes it, adds to it or goes before it
// (plan §2.4, risk 20).
func TestTheAntiLockoutRuleCannotBeOverriddenByAnyRuleOrOverlay(t *testing.T) {
	bare := accessWorld(t).compile(nil)

	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: everything, source: {global: true}, action: drop}`)
	w.addConfigRule(ruleB, `{name: the-ui, source: {global: true}, protocol: tcp, ports: [22, 443], action: reset}`)
	w.ruleOverlay(`{target: {global: true}, rule: {action: reject}}`, time.Second)
	w.ruleOverlay(`{target: {network: IoT}, rule: {action: drop, protocol: tcp, ports: [22, 443], cut_existing: true}}`, 2*time.Second)
	tg := w.compile(nil)

	for _, name := range []string{"input", "forward"} {
		b, g := renderRules(chainOf(t, bare, name)), renderRules(chainOf(t, tg, name))
		jump := -1
		for i, r := range g {
			if strings.HasPrefix(r, "jump access_") {
				jump = i
				break
			}
		}
		if jump < 0 {
			t.Fatalf("%s: no jump to the access rules", name)
		}
		// what stands in front of the jump is a prefix of the bare chain (a cut jump may add one
		// rule at the very top; it is checked below)
		var front []string
		for _, r := range g[:jump] {
			if !strings.HasPrefix(r, "jump cut_") {
				front = append(front, r)
			}
		}
		if len(front) > len(b) {
			t.Fatalf("%s: more rules in front of the access rules than without rules:\n%v\n%v", name, front, b)
		}
		for i := range front {
			if front[i] != b[i] {
				t.Errorf("%s: rule %d in front of the access rules changed:\n  with rules    %s\n  without rules %s", name, i, front[i], b[i])
			}
		}
	}
	in := renderRules(chainOf(t, tg, "input"))
	anti := -1
	for i, r := range in {
		if strings.Contains(r, `counter "anti_lockout" accept`) {
			anti = i
		}
	}
	// the only rules that may stand in front of it are the cut jump, established, loopback and the
	// service namespace's answers
	for i, r := range in[:anti] {
		if !strings.HasPrefix(r, "jump cut_") && !strings.Contains(r, "established") && !strings.Contains(r, "iifname lo") && !strings.Contains(r, "svc") {
			t.Errorf("input rule %d stands in front of the anti-lockout rule: %s", i, r)
		}
	}
	// the cut window of the input hook starts with the anti-lockout match: the control plane is never reset
	_, cutIn := tg.Access.CutRules([]string{tg.Access.Rules[0].Key, tg.Access.Rules[1].Key, tg.Access.Rules[2].Key, tg.Access.Rules[3].Key})
	if len(cutIn) < 2 || renderRule(cutIn[0]) != "meta iifname lo return" || !strings.Contains(renderRule(cutIn[1]), "@mgmt_src") || !strings.HasSuffix(renderRule(cutIn[1]), "return") {
		t.Fatalf("the cut chain of input must start with loopback and the anti-lockout match: %v", cutIn)
	}
}

func TestOverlayRulesComeBeforeConfigurationRulesNewestFirst(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: first, source: {global: true}, protocol: tcp, ports: [1], action: drop}`)
	w.addConfigRule(ruleB, `{name: second, source: {global: true}, protocol: tcp, ports: [2], action: drop}`)
	old := w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [3]}}`, time.Second)
	newer := w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: allow, protocol: tcp, ports: [4]}}`, time.Minute)
	tg := w.compile(nil)

	var keys []string
	for _, r := range tg.Access.Rules {
		keys = append(keys, r.Key)
		if r.Position != len(keys)-1 {
			t.Errorf("rule %s has position %d", r.Key, r.Position)
		}
	}
	want := []string{"overlay:" + newer.Id.String(), "overlay:" + old.Id.String(), "config:" + ruleA, "config:" + ruleB}
	if strings.Join(keys, " ") != strings.Join(want, " ") {
		t.Fatalf("order = %v\nwant    %v", keys, want)
	}
	// the chains list them in this order
	c := chainOf(t, tg, AccessForwardChain)
	for i, r := range tg.Access.Rules {
		if !strings.Contains(renderRule(c.Rules[i]), r.Counter) {
			t.Errorf("rule %d of the chain is not %s: %s", i, r.Key, renderRule(c.Rules[i]))
		}
	}
}

func TestADisabledRuleIsNotCompiledAndTheOrderOfTheConfigurationIsKept(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: off, source: {global: true}, action: drop, enabled: false}`)
	w.addConfigRule(ruleB, `{name: b, source: {global: true}, protocol: tcp, ports: [2], action: drop}`)
	w.addConfigRule(ruleC, `{name: c, source: {global: true}, protocol: tcp, ports: [3], action: drop}`)
	tg := w.compile(nil)
	if n := len(tg.Access.Rules); n != 2 || tg.Access.Rules[0].Name != "b" || tg.Access.Rules[1].Name != "c" {
		t.Fatalf("rules = %+v", tg.Access.Rules)
	}
	if tg.Access.Rules[0].Position != 0 || tg.Access.Rules[1].Position != 1 {
		t.Errorf("positions count the compiled rules: %+v", tg.Access.Rules)
	}
}

// ---- verdicts -------------------------------------------------------------------------------------

func TestEveryActionHasItsVerdictInForwardAndInput(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {global: true}, protocol: tcp, ports: [1], action: allow}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: tcp, ports: [2], action: drop}`)
	w.addConfigRule(ruleC, `{source: {global: true}, protocol: udp, ports: [3], action: reject}`)
	w.addConfigRule(ruleD, `{source: {global: true}, protocol: tcp, ports: [4], action: reset}`)
	tg := w.compile(nil)
	for _, c := range []struct {
		chain string
		want  [4]string
	}{
		// an allow rule accepts in forward (an exception to the matrix) and returns in input (the
		// gateway's protection of the test networks stays what it is; P2-M9-01)
		{AccessForwardChain, [4]string{"accept", "drop", "reject with icmpx port-unreachable", "reject with tcp reset"}},
		{AccessInputChain, [4]string{"return", "drop", "reject with icmpx port-unreachable", "reject with tcp reset"}},
	} {
		rules := renderRules(chainOf(t, tg, c.chain))
		for i, want := range c.want {
			if !strings.HasSuffix(rules[i], `" `+want) {
				t.Errorf("%s rule %d = %s, want the verdict %q", c.chain, i, rules[i], want)
			}
		}
	}
}

// ---- selectors --------------------------------------------------------------------------------------

func TestRulesMatchTheOriginalTupleAndNeverThePacketsOwnAddresses(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, destination: {cidr: 203.0.113.0/24}, protocol: tcp, ports: [8883], action: drop}`)
	w.addConfigRule(ruleB, `{source: {network: IoT}, destination: {network: Lab}, action: reject}`)
	w.addConfigRule(ruleC, `{source: {group: sensors}, destination: {uplink: true}, protocol: icmp, action: drop}`)
	tg := w.compile(nil)
	for _, name := range []string{AccessForwardChain, AccessInputChain} {
		for _, r := range chainOf(t, tg, name).Rules {
			s := js(r.Expr)
			if strings.Contains(s, `"payload"`) || strings.Contains(s, `"iifname"`) || strings.Contains(s, `"oifname"`) {
				t.Errorf("%s: a rule looks at the packet as it is now, not at the original tuple: %s", name, renderRule(r))
			}
			if !strings.Contains(s, `"dir":"original"`) {
				t.Errorf("%s: a rule without the original tuple: %s", name, renderRule(r))
			}
		}
	}
	rules := renderRules(chainOf(t, tg, AccessForwardChain))
	for i, want := range []string{
		"ct original ip daddr 203.0.113.0/24 meta l4proto tcp ct original proto-dst 8883",
		"ct original ip daddr 10.20.0.0/24 counter",
		"ct original ip daddr != @" + nonUplinkSetName() + " meta l4proto icmp counter",
	} {
		if !strings.Contains(rules[i], want) {
			t.Errorf("rule %d = %s\nwant it to contain %s", i, rules[i], want)
		}
	}
	// "uplink" is everything outside the networks and the management network
	var up SetDef
	for _, s := range tg.Nft.Sets {
		if s.Name == nonUplinkSetName() {
			up = s
		}
	}
	if strings.Join(up.Elements, ",") != "10.10.0.0/24,10.20.0.0/24,192.168.56.0/24" {
		t.Errorf("non-uplink set = %v", up.Elements)
	}
}

func TestPortsAndRangesAreMergedIntoDisjointIntervals(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {global: true}, protocol: tcp, ports: [443, 80, 80, 8001], port_ranges: [{from: 8000, to: 8100}, {from: 8050, to: 8200}, {from: 79, to: 79}], action: drop}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: udp, port_ranges: [{from: 1000, to: 2000}], action: drop}`)
	tg := w.compile(nil)
	rules := renderRules(chainOf(t, tg, AccessForwardChain))
	if !strings.Contains(rules[0], "ct original proto-dst { 79-80, 443, 8000-8200 }") {
		t.Errorf("ports = %s", rules[0])
	}
	if !strings.Contains(rules[1], "ct original proto-dst 1000-2000") {
		t.Errorf("a single range = %s", rules[1])
	}
}

func TestAHostnameRuleIsLeftOutWithAWarning(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: by-name, source: {global: true}, destination: {hostname: broker.example.com}, action: drop}`)
	w.addConfigRule(ruleB, `{name: plain, source: {global: true}, protocol: tcp, ports: [2], action: drop}`)
	tg := w.compile(nil)
	if len(tg.Access.Rules) != 1 || tg.Access.Rules[0].Name != "plain" {
		t.Fatalf("rules = %+v", tg.Access.Rules)
	}
	found := false
	for _, p := range tg.Problems {
		if p.Code == CodeHostnameUnresolved && p.Severity == SevWarning && strings.Contains(p.Message, "broker.example.com") {
			found = true
		}
	}
	if !found || tg.HasErrors() {
		t.Fatalf("problems = %+v", tg.Problems)
	}
}

// ---- scopes ----------------------------------------------------------------------------------------

func sourcesOf(tg *Target, key string) []string {
	r, ok := tg.Access.Rule(key)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range r.Sources {
		out = append(out, p.String())
	}
	return out
}

func TestScopesExpandToTheAddressesTheyCoverRightNow(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, action: drop}`)
	w.addConfigRule(ruleB, `{source: {group: sensors}, action: drop}`)
	w.addConfigRule(ruleC, `{source: {network: Lab}, action: drop}`)
	tg := w.compile(nil)
	for key, want := range map[string]string{
		"config:" + ruleA: "10.10.0.42/32",
		"config:" + ruleB: "10.10.0.42/32 10.10.0.43/32",
		"config:" + ruleC: "10.20.0.0/24",
	} {
		if got := strings.Join(sourcesOf(tg, key), " "); got != want {
			t.Errorf("%s: sources %q, want %q", key, got, want)
		}
	}
	// the global scope is the classification's set of test, WireGuard and remote networks: never the
	// management network or the uplink
	w = accessWorld(t)
	w.addConfigRule(ruleA, `{source: {global: true}, action: drop}`)
	tg = w.compile(nil)
	r := tg.Access.Rules[0]
	if !r.AnySource || r.SourceSet != tg.ClassifyNets || len(r.Sources) != 0 {
		t.Fatalf("global rule = %+v (classify set %s)", r, tg.ClassifyNets)
	}
	if tg.Access.Winner(Tuple{Src: netip.MustParseAddr("192.168.56.5"), Dst: netip.MustParseAddr("10.10.0.1"), Proto: "tcp", Port: 443}) != nil {
		t.Error("a global rule must not select the management network")
	}
	if tg.Access.Winner(Tuple{Src: netip.MustParseAddr("10.10.0.77"), Dst: netip.MustParseAddr("203.0.113.9"), Proto: "tcp", Port: 443}) == nil {
		t.Error("a global rule selects an address of a test network that no device owns")
	}
}

func TestARuleThatFollowsADeviceFollowsItsAddressAndKeepsItsCounter(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, protocol: tcp, ports: [8883], action: drop}`)
	before := w.compile(nil)
	w.setAddrs(map[string][]string{devESP42: {"10.10.0.142"}, devESP43: {"10.10.0.43"}, devLab: {"10.20.0.50"}})
	after := w.compile(nil)
	if got := strings.Join(sourcesOf(after, "config:"+ruleA), " "); got != "10.10.0.142/32" {
		t.Fatalf("sources after the address change: %s", got)
	}
	a, b := before.Access.Rules[0], after.Access.Rules[0]
	if a.Counter != b.Counter || a.SourceSet != b.SourceSet {
		t.Errorf("the counter and the set keep their names: %+v / %+v", a, b)
	}
	// only the elements of the source set differ between the two targets
	if before.Nft.Chains == nil || js(before.Nft.Chains) != js(after.Nft.Chains) {
		t.Error("the chains must not depend on the addresses")
	}
	// a device without an address: the rule stays, with an empty set
	w.setAddrs(map[string][]string{devESP43: {"10.10.0.43"}})
	none := w.compile(nil)
	if len(none.Access.Rules) != 1 || len(none.Access.Rules[0].Sources) != 0 {
		t.Fatalf("rules = %+v", none.Access.Rules)
	}
	for _, s := range none.Nft.Sets {
		if s.Name == none.Access.Rules[0].SourceSet && len(s.Elements) != 0 {
			t.Errorf("set = %+v", s)
		}
	}
	if none.Access.Winner(Tuple{Src: netip.MustParseAddr("10.10.0.43"), Dst: netip.MustParseAddr("203.0.113.10"), Proto: "tcp", Port: 8883}) != nil {
		t.Error("an empty rule selects nothing")
	}
}

func TestTheCounterOfARuleFollowsTheRuleNotItsPlace(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {global: true}, protocol: tcp, ports: [1], action: drop}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: tcp, ports: [2], action: drop}`)
	first := w.compile(nil)
	counters := map[string]string{}
	for _, r := range first.Access.Rules {
		counters[r.Key] = r.Counter
	}
	// the order changes, an overlay comes in front
	order := *w.cfg.AccessRuleOrder
	w.cfg.AccessRuleOrder = &[]uuid.UUID{order[1], order[0]}
	w.ruleOverlay(`{target: {global: true}, rule: {action: drop, protocol: udp}}`, time.Hour)
	second := w.compile(nil)
	if second.Access.Rules[1].Key != "config:"+ruleB {
		t.Fatalf("order = %+v", second.Access.Rules)
	}
	for _, r := range second.Access.Rules {
		if c, ok := counters[r.Key]; ok && c != r.Counter {
			t.Errorf("%s: the counter changed from %s to %s", r.Key, c, r.Counter)
		}
	}
	// every counter is a counter of the table, and a removed rule's counter goes with it
	for _, r := range second.Access.Rules {
		found := false
		for _, c := range second.Nft.Counters {
			found = found || c == r.Counter
		}
		if !found {
			t.Errorf("counter %s of %s is not in the table", r.Counter, r.Key)
		}
	}
	for _, c := range []string{ruleCounterName("config:" + ruleA), ruleCounterName("config:" + ruleB)} {
		w2 := accessWorld(t)
		tg := w2.compile(nil)
		for _, have := range tg.Nft.Counters {
			if have == c {
				t.Errorf("counter %s survives without its rule", c)
			}
		}
	}
}

// ---- capacity -------------------------------------------------------------------------------------

func TestTooManyRulesAreRefusedAndTheOverlaysThatCausedItAreNamed(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {global: true}, protocol: tcp, ports: [1], action: drop}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: tcp, ports: [2], action: drop}`)
	o1 := w.ruleOverlay(`{target: {global: true}, rule: {action: drop, protocol: udp, ports: [1]}}`, time.Second)
	// the limit is named: three rules fit, a fourth does not
	atLimit := w.compile(func(in *Input) { in.RuleLimit = 3 })
	if atLimit.HasErrors() || len(atLimit.Access.Rules) != 3 {
		t.Fatalf("three rules at a limit of three: %+v", atLimit.Problems)
	}
	o2 := w.ruleOverlay(`{target: {global: true}, rule: {action: drop, protocol: udp, ports: [2]}}`, 2*time.Second)
	tg := w.compile(func(in *Input) { in.RuleLimit = 3 })
	if !tg.HasErrors() {
		t.Fatal("four rules at a limit of three must be refused")
	}
	p := tg.Errors()[0]
	if p.Code != CodeCapacityExceeded || !strings.Contains(p.Message, "4 access rules") || !strings.Contains(p.Message, "limit of 3") {
		t.Errorf("problem = %+v", p)
	}
	if strings.Join(p.Faults, " ") != o1.Id.String()+" "+o2.Id.String() && strings.Join(p.Faults, " ") != o2.Id.String()+" "+o1.Id.String() {
		t.Errorf("the problem names %v, want the two overlays", p.Faults)
	}
	if tg.Access != nil {
		t.Error("a refused target carries no rules")
	}
	// the default limit is one thousand rules, on every architecture
	if DefaultRuleLimit != 1000 || MaxRuleElements != 65536 {
		t.Errorf("the limits are named in api/openapi.yaml (AccessRule): %d rules, %d addresses", DefaultRuleLimit, MaxRuleElements)
	}
}

func TestTooManySourceAddressesAreRefusedAndTheBiggestRulesAreNamed(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: sensors, source: {group: sensors}, protocol: tcp, ports: [1], action: drop}`)
	w.addConfigRule(ruleB, `{name: one, source: {device: esp32-42}, protocol: tcp, ports: [2], action: drop}`)
	if tg := w.compile(func(in *Input) { in.RuleElementLimit = 3 }); tg.HasErrors() {
		t.Fatalf("three elements at a limit of three: %+v", tg.Problems)
	}
	tg := w.compile(func(in *Input) { in.RuleElementLimit = 2 })
	if !tg.HasErrors() || tg.Errors()[0].Code != CodeCapacityExceeded || !strings.Contains(tg.Errors()[0].Message, "3 address elements") {
		t.Fatalf("problems = %+v", tg.Problems)
	}
	if got := tg.Errors()[0].Faults; len(got) == 0 || got[0] != ruleA {
		t.Errorf("the biggest rule comes first: %v", got)
	}
}

// ---- cutting existing connections -------------------------------------------------------------------

func TestTheCutChainsExistOnlyWhenARuleCanCut(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {global: true}, protocol: tcp, ports: [1], action: drop}`)
	w.addConfigRule(ruleB, `{source: {global: true}, protocol: tcp, ports: [2], action: allow, cut_existing: false}`)
	tg := w.compile(nil)
	if hasChain(tg, CutForwardChain) || hasChain(tg, CutInputChain) || tg.Access.HasCuts() {
		t.Fatalf("no rule cuts, yet the chains are there: %v", chainNames(tg))
	}
	w.addConfigRule(ruleC, `{source: {global: true}, protocol: tcp, ports: [3], action: drop, cut_existing: true}`)
	tg = w.compile(nil)
	for _, name := range []string{"forward", "input"} {
		c := chainOf(t, tg, name)
		if r := renderRule(c.Rules[0]); r != "jump cut_"+name {
			t.Errorf("%s starts with %q: the window runs in front of the established accept", name, r)
		}
	}
	for _, name := range []string{CutForwardChain, CutInputChain} {
		if c := chainOf(t, tg, name); len(c.Rules) != 0 || c.Base != nil {
			t.Errorf("%s must be empty outside a window: %+v", name, c)
		}
	}
}

func TestACutWindowResetsOnlyEstablishedTCPOfTheOriginalDirection(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, protocol: tcp, ports: [8883], action: drop, cut_existing: true}`)
	tg := w.compile(nil)
	key := "config:" + ruleA
	fwd, in := tg.Access.CutRules([]string{key})
	set := scopeSetName(model.Scope{Device: ptr(devESP42)})
	want := fmt.Sprintf(`ct original ip saddr @%s meta l4proto tcp ct original proto-dst 8883 ct state { established } ct direction original reject with tcp reset`, set)
	// switched traffic of a bridge and loopback are skipped first, as the hooks do before the rules
	if got := renderRules(Chain{Rules: fwd}); len(got) != 3 || got[0] != "meta iifname br-iot meta oifname br-iot return" ||
		got[1] != "meta iifname br-lab meta oifname br-lab return" || got[2] != want {
		t.Fatalf("forward window = %v\nwant %s", got, want)
	}
	if got := renderRules(Chain{Rules: in}); len(got) != 3 || got[0] != "meta iifname lo return" ||
		!strings.Contains(got[1], "@mgmt_src") || !strings.HasSuffix(got[1], " return") || got[2] != want {
		t.Fatalf("input window = %v", got)
	}
	// no window without keys, none for a key that does not cut
	if f, i := tg.Access.CutRules(nil); f != nil || i != nil {
		t.Error("no keys, no window")
	}
	if f, _ := tg.Access.CutRules([]string{"config:" + ruleD}); f != nil {
		t.Error("an unknown key opens no window")
	}
}

// A connection belongs to the first rule that selects it. A cut of a later rule must not touch a
// connection that an earlier rule owns, whether that rule allows, drops or cuts itself.
func TestACutWindowLeavesTheConnectionsOfEarlierRulesAlone(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: keep-ntp, source: {global: true}, protocol: tcp, ports: [123], action: allow}`)
	w.addConfigRule(ruleB, `{name: old-drop, source: {global: true}, protocol: tcp, ports: [443], action: drop}`)
	w.addConfigRule(ruleC, `{name: cut-iot, source: {network: IoT}, action: reject, cut_existing: true}`)
	w.addConfigRule(ruleD, `{name: later, source: {global: true}, action: drop}`)
	tg := w.compile(nil)
	fwd, _ := tg.Access.CutRules([]string{"config:" + ruleC})
	got := renderRules(Chain{Rules: fwd})
	if len(got) != 5 {
		t.Fatalf("window = %v", got)
	}
	// the switched traffic of the bridges comes first, then the rules
	for i, b := range []string{"br-iot", "br-lab"} {
		if want := "meta iifname " + b + " meta oifname " + b + " return"; got[i] != want {
			t.Errorf("window[%d] = %s, want %s", i, got[i], want)
		}
	}
	got = got[2:]
	for i, r := range tg.Access.Rules[:2] {
		if !strings.HasSuffix(got[i], " return") || !strings.Contains(got[i], "ct original proto-dst") {
			t.Errorf("rule %d (%s) must return first: %s", i, r.Name, got[i])
		}
	}
	// the cutting rule has no protocol: it resets TCP, other protocols return from the chain
	if !strings.HasSuffix(got[2], "reject with tcp reset") || !strings.Contains(got[2], "meta l4proto tcp ct state { established } ct direction original") {
		t.Errorf("the cut: %s", got[2])
	}
	// a rule behind the last cut is not in the window at all (nothing follows the cut)
	for _, g := range got {
		if strings.Contains(g, "ct original proto-dst") == false && strings.HasSuffix(g, "return") {
			t.Errorf("the rule behind the cut is in the window: %s", g)
		}
	}
	// a cut of the rule that cuts but is not asked to is no window
	tg2 := tg
	if f, _ := tg2.Access.CutRules([]string{"config:" + ruleA}); f != nil {
		t.Error("an allow rule has no cut")
	}
}

func TestACutTransactionReplacesTheChainsAndClosesTheWindowWithoutKeys(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, protocol: tcp, action: reset, cut_existing: true}`)
	tg := w.compile(nil)
	open, err := tg.Access.CutTransaction([]string{"config:" + ruleA})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := tg.Access.CutTransaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	// the executor's scope: only objects of the table inet chaosgw
	for _, tx := range [][]byte{open, closed} {
		if err := executor.CheckNftRuleset(tx); err != nil {
			t.Errorf("the executor refuses a cut transaction: %v", err)
		}
	}
	var o, c struct {
		Nftables []map[string]map[string]map[string]any `json:"nftables"`
	}
	if err := json.Unmarshal(open, &o); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(closed, &c); err != nil {
		t.Fatal(err)
	}
	var verbs []string
	for _, cmd := range o.Nftables {
		for verb, obj := range cmd {
			for kind, f := range obj {
				verbs = append(verbs, fmt.Sprintf("%s %s %v", verb, kind, f["chain"]))
				if f["table"] != "chaosgw" || f["family"] != "inet" {
					t.Errorf("a command outside the table: %v", cmd)
				}
			}
		}
	}
	// flush forward, the two bridges and the rule, flush input, loopback, the guard and the rule
	if strings.Join(verbs, "|") != "flush chain <nil>|add rule cut_forward|add rule cut_forward|add rule cut_forward|flush chain <nil>|add rule cut_input|add rule cut_input|add rule cut_input" {
		t.Errorf("open = %v", verbs)
	}
	if len(c.Nftables) != 2 {
		t.Errorf("closing flushes the two chains: %s", closed)
	}
}

// ---- the Go evaluation is the kernel's -----------------------------------------------------------------

// Winner is the Go mirror of the nftables chain, and the domain layer's ResolveAccess is the
// specification: the same rule must win for any traffic of any source the fixture knows.
func TestTheCompiledRulesDecideLikeTheDomainLayer(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: sensors-8883, source: {group: sensors}, destination: {cidr: 203.0.113.0/24}, protocol: tcp, ports: [8883], action: drop}`)
	w.addConfigRule(ruleB, `{name: iot-to-lab, source: {network: IoT}, destination: {network: Lab}, action: reject}`)
	w.addConfigRule(ruleC, `{name: lab-up, source: {network: Lab}, destination: {uplink: true}, protocol: udp, port_ranges: [{from: 100, to: 200}], action: drop}`)
	w.addConfigRule(ruleD, `{name: everyone-icmp, source: {global: true}, protocol: icmp, action: reject}`)
	w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: allow, protocol: tcp, ports: [8883]}}`, time.Hour)
	w.ruleOverlay(`{target: {network: IoT}, rule: {action: drop, destination: {cidr: 198.51.100.7}}}`, 2*time.Hour)
	tg := w.compile(nil)

	world, err := domain.NewWorld(w.cfg, w.overlays)
	if err != nil {
		t.Fatal(err)
	}
	srcs := []string{"10.10.0.42", "10.10.0.43", "10.10.0.99", "10.10.0.200", "10.20.0.50", "10.20.0.77"}
	dsts := []string{"203.0.113.10", "10.20.0.50", "10.10.0.1", "10.10.0.42", "198.51.100.7", "198.51.100.8", "192.168.56.5", "8.8.8.8"}
	traffic := []struct {
		proto string
		port  int
	}{{"tcp", 8883}, {"tcp", 443}, {"udp", 150}, {"udp", 53}, {"icmp", 0}, {"tcp", 0}}
	checked, matched := 0, 0
	for _, s := range srcs {
		src := netip.MustParseAddr(s)
		sub := domain.Subject{IP: src}
		if dev, ok := w.id.OwnerOf(src); ok {
			sub.Device = dev
		}
		if n, ok := world.NetworkOf(src); ok {
			sub.Network = n
		}
		for _, d := range dsts {
			dst := netip.MustParseAddr(d)
			for _, tr := range traffic {
				want := world.ResolveAccess(domain.Query{Source: sub, DestIP: dst, Protocol: tr.proto, Port: tr.port})
				got := tg.Access.Winner(Tuple{Src: src, Dst: dst, Proto: tr.proto, Port: tr.port})
				wantKey, gotKey := "", ""
				if want.Matched {
					wantKey = string(want.Layer) + ":" + want.RuleID
					matched++
				}
				if got != nil {
					gotKey = got.Key
				}
				if wantKey != gotKey {
					t.Errorf("%s -> %s %s/%d: the domain layer says %q, the compiled rules say %q", s, d, tr.proto, tr.port, wantKey, gotKey)
				}
				checked++
			}
		}
	}
	if matched < 50 || matched == checked {
		t.Fatalf("the grid is not useful: %d of %d matched", matched, checked)
	}
}

// The same for the remote networks: the networks behind a WireGuard client and the routes of a link.
func TestTheCompiledRulesDecideLikeTheDomainLayerForRemoteNetworks(t *testing.T) {
	w := newFaultWorldFile(t, "wireguard.yaml")
	w.setAddrs(map[string][]string{})
	w.addConfigRule(ruleA, `{name: ra-drop, source: {remote_network: {client: rA}}, destination: {network: IoT}, action: drop}`)
	w.addConfigRule(ruleB, `{name: site-b, source: {remote_network: {link: site-b}}, protocol: tcp, ports: [22], action: reset}`)
	w.addConfigRule(ruleC, `{name: hub, source: {network: lab-hub}, action: reject}`)
	w.addConfigRule(ruleD, `{name: iot-to-site-b, source: {network: IoT}, destination: {cidr: 10.60.0.0/24}, action: allow}`)
	tg := w.compile(func(in *Input) { in.Keys = wgKeys })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	for key, want := range map[string]string{
		"config:" + ruleA: "10.50.0.0/24",
		"config:" + ruleB: "10.60.0.0/24",
		"config:" + ruleC: "10.50.0.0/24 10.99.0.0/24",
	} {
		if got := strings.Join(sourcesOf(tg, key), " "); got != want {
			t.Errorf("%s: sources %q, want %q", key, got, want)
		}
	}
	world, err := domain.NewWorld(w.cfg, w.overlays)
	if err != nil {
		t.Fatal(err)
	}
	checked, matched := 0, 0
	for _, s := range []string{"10.50.0.9", "10.60.0.5", "10.99.0.2", "10.99.0.77", "10.10.0.5"} {
		for _, d := range []string{"10.10.0.1", "10.60.0.9", "10.50.0.1", "203.0.113.5", "192.168.56.2"} {
			for _, tr := range []struct {
				proto string
				port  int
			}{{"tcp", 22}, {"tcp", 80}, {"udp", 53}, {"icmp", 0}} {
				src, dst := netip.MustParseAddr(s), netip.MustParseAddr(d)
				sub := domain.Subject{IP: src}
				if n, ok := world.NetworkOf(src); ok {
					sub.Network = n
				}
				want := world.ResolveAccess(domain.Query{Source: sub, DestIP: dst, Protocol: tr.proto, Port: tr.port})
				got := tg.Access.Winner(Tuple{Src: src, Dst: dst, Proto: tr.proto, Port: tr.port})
				wantKey, gotKey := "", ""
				if want.Matched {
					wantKey = string(want.Layer) + ":" + want.RuleID
					matched++
				}
				if got != nil {
					gotKey = got.Key
				}
				if wantKey != gotKey {
					t.Errorf("%s -> %s %s/%d: the domain layer says %q, the compiled rules say %q", s, d, tr.proto, tr.port, wantKey, gotKey)
				}
				checked++
			}
		}
	}
	if matched < 20 || matched == checked {
		t.Fatalf("the grid is not useful: %d of %d matched", matched, checked)
	}
}

// ---- golden --------------------------------------------------------------------------------------

func scenarioAccess(t *testing.T) *Target {
	t.Helper()
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: no-dot, source: {network: IoT}, protocol: tcp, ports: [853], action: reject}`)
	w.addConfigRule(ruleB, `{name: lab-to-iot, source: {network: Lab}, destination: {network: IoT}, action: allow}`)
	w.addConfigRule(ruleC, `{name: iot-up-udp, source: {group: sensors}, destination: {uplink: true}, protocol: udp, port_ranges: [{from: 1000, to: 2000}], ports: [53], action: drop}`)
	w.addConfigRule(ruleD, `{name: no-bgp, source: {global: true}, protocol: tcp, ports: [179], action: reset}`)
	w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [8883], destination: {cidr: 203.0.113.10}}}`, time.Second)
	return w.compile(nil)
}

func scenarioAccessCut(t *testing.T) *Target {
	t.Helper()
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: keep-ntp, source: {global: true}, protocol: udp, ports: [123], action: allow}`)
	w.addConfigRule(ruleB, `{name: cut-iot, source: {network: IoT}, action: reject, cut_existing: true}`)
	w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: reset, protocol: tcp, ports: [8883], cut_existing: true}}`, time.Second)
	return w.compile(nil)
}

func accessScenarios(t *testing.T) map[string]*Target {
	return map[string]*Target{
		"access":     scenarioAccess(t),
		"access-cut": scenarioAccessCut(t),
	}
}

func TestGoldenAccessRules(t *testing.T) {
	for name, tg := range accessScenarios(t) {
		t.Run(name, func(t *testing.T) {
			if tg.HasErrors() || len(tg.Problems) != 0 {
				t.Fatalf("problems: %+v", tg.Problems)
			}
			goldenText(t, name, describeAccess(tg))
		})
	}
}

func TestGoldenAccessNftTransaction(t *testing.T) {
	tx, err := scenarioAccess(t).Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	var pretty any
	_ = json.Unmarshal(tx, &pretty)
	golden(t, "access.nft", pretty)
}

// The plan knows what the packet path never lets a rule judge: the bridges' switched traffic and what
// the gateway opened itself. A device talking to the gateway is input traffic and is judged.
func TestThePlanKnowsWhatNoRuleJudges(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: cut-iot, source: {network: IoT}, action: drop, cut_existing: true}`)
	tg := w.compile(nil)
	p := tg.Access
	if got := strings.Join(p.Switched, ","); got != "br-iot,br-lab" {
		t.Errorf("switched bridges = %s", got)
	}
	addr := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	for _, c := range []struct {
		src, dst string
		not      bool
	}{
		{"10.10.0.1", "10.10.0.42", true},     // the gateway opened it (BGP to a device)
		{"10.10.0.42", "10.10.0.43", true},    // two devices of IoT: switched
		{"10.20.0.50", "10.20.0.51", true},    // two devices of Lab
		{"10.10.0.42", "10.20.0.50", false},   // routed between the networks
		{"10.10.0.42", "203.0.113.10", false}, // towards the uplink
		{"10.10.0.42", "10.10.0.1", false},    // to the gateway: input, judged
		{"203.0.113.10", "10.10.0.42", false}, // from outside
	} {
		if got := p.NotJudged(Tuple{Src: addr(c.src), Dst: addr(c.dst), Proto: "tcp", Port: 80}); got != c.not {
			t.Errorf("%s -> %s: not judged = %v, want %v", c.src, c.dst, got, c.not)
		}
	}
	if (*AccessPlan)(nil).NotJudged(Tuple{Src: addr("10.10.0.1"), Dst: addr("10.10.0.42")}) {
		t.Error("no plan, no exclusions")
	}
}

// The name of a source set is a hash of the scope; the sets of two scopes that collide must never be
// shared, and the same scope twice shares its set.
func TestASourceSetNameIsLongAndACollisionIsRefused(t *testing.T) {
	name := scopeSetName(model.Scope{Device: ptr(devESP42)})
	if len(name) < len("asrc_")+24 {
		t.Errorf("the set name %q has fewer than 96 bits of the scope's hash", name)
	}
	sets := map[string]SetDef{}
	a := SetDef{Name: "asrc_x", Elements: []string{"10.10.0.42"}}
	if added, err := addSourceSet(sets, a); !added || err != nil {
		t.Fatalf("%v %v", added, err)
	}
	if added, err := addSourceSet(sets, a); added || err != nil {
		t.Errorf("the same scope twice: %v %v", added, err)
	}
	if _, err := addSourceSet(sets, SetDef{Name: "asrc_x", Elements: []string{"10.10.0.43"}}); err == nil {
		t.Error("a set name used by two scopes with different addresses must be refused")
	}
}
