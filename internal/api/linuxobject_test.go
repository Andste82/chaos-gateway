package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// P3-M14-03 (plan §2.17 "Show Linux view" on a device, an access rule and a fault): GET
// /state/linux/{kind}/{id} cuts the kernel state down to what the compiler made for one object, by the
// names and keys it gave them: a rule's rules in both hooks with their counter and `last`, its named
// counter and source set; a fault's mark chain with its counters, the classification elements that go to
// it and its tc classes and netem queues; a device's identity map element and the classification elements
// of its address. Nothing of another object is in it, and an object the kernel holds nothing of is empty.
func TestTheLinuxViewOfOneRuleFaultOrDeviceShowsWhatTheCompilerMadeForIt(t *testing.T) {
	g := dhcpGateway(t)
	if r := g.apply(g.mustPatch(rulesPatch())); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31"), nb("10.10.0.50", "02:00:00:00:00:aa")})
	g.observe()
	ov := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"fault":{"latency":"50ms","rate":"2Mbit"}}`)
	other := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"destination":{"cidr":"203.0.113.0/24"},"loss":"1%"}}`)
	g.mintToken("read")
	view := func(kind, id string) (keys []string, nft, tc string) {
		t.Helper()
		r := g.do("GET", "/state/linux/"+kind+"/"+id, nil, nil, nil)
		if r.Status != 200 {
			t.Fatalf("%s %s: %d %s", kind, id, r.Status, r.Body)
		}
		v := r.json(t)
		if v["kind"] != kind || v["id"] != id || v["revision"].(float64) < 1 || v["read_at"] == nil {
			t.Errorf("%s %s: %v", kind, id, v)
		}
		for _, k := range v["keys"].([]any) {
			keys = append(keys, k.(string))
		}
		return keys, v["nftables"].(string), v["tc"].(string)
	}
	has := func(keys []string, want string) bool {
		for _, k := range keys {
			if strings.HasPrefix(k, want) {
				return true
			}
		}
		return false
	}
	snap := g.e.Snapshot()

	// a rule: its rules in both hooks, its counter and its source set, no other rule
	b, ok := snap.Access.Rule("config:" + ruleB)
	a, _ := snap.Access.Rule("config:" + ruleA)
	if !ok {
		t.Fatal("the rule is not compiled")
	}
	keys, nft, tc := view("rule", ruleB)
	t.Logf("rule:\n%v\n%s", keys, nft)
	for _, want := range []string{
		"counter " + b.Counter + " { packets ",
		"chain access_forward {",
		"chain access_input {",
		fmt.Sprintf("counter name %q last used never reject with icmpx port-unreachable", b.Counter),
		"set " + b.SourceSet + " {",
	} {
		if !strings.Contains(nft, want) {
			t.Errorf("rule: no %q in\n%s", want, nft)
		}
	}
	if strings.Count(nft, fmt.Sprintf("counter name %q", b.Counter)) != 2 || strings.Contains(nft, a.Counter) || tc != "" {
		t.Errorf("rule: another rule's or a tc object is shown:\n%s\n%s", nft, tc)
	}
	if !has(keys, "counter "+b.Counter) || !has(keys, "set "+b.SourceSet) || !has(keys, "rule access_input ") {
		t.Errorf("rule keys: %v", keys)
	}
	if keys, nft, _ := view("rule", compiler.AntiLockoutRule.Key); !has(keys, "counter anti_lockout") || !strings.Contains(nft, `counter name "anti_lockout" last used never accept`) {
		t.Errorf("the anti-lockout rule: %v\n%s", keys, nft)
	}

	// a fault: its mark chain, counters, classification elements and queues, not the other fault's
	var mine, theirs []compiler.Fault
	for _, f := range snap.Faults {
		switch f.Source {
		case ov["id"]:
			mine = append(mine, f)
		case other["id"]:
			theirs = append(theirs, f)
		}
	}
	if len(mine) == 0 || len(theirs) == 0 {
		t.Fatalf("faults: %+v", snap.Faults)
	}
	keys, nft, tc = view("fault", ov["id"].(string))
	t.Logf("fault:\n%v\n%s\n%s", keys, nft, tc)
	for _, f := range mine {
		chain := compiler.MarkChainName(f.ID)
		if !has(keys, "chain "+chain) || !strings.Contains(nft, "chain "+chain+" {") || !strings.Contains(nft, "goto "+chain) {
			t.Errorf("fault: chain %s or an element going to it is missing", chain)
		}
		for _, c := range []string{f.CounterUp, f.CounterDown} {
			if !strings.Contains(nft, "counter "+c+" {") {
				t.Errorf("fault: no counter %s", c)
			}
		}
		class := compiler.ClassIDOf(f.ID, compiler.Upload)
		if !strings.Contains(tc, "class "+class) || !strings.Contains(tc, "netem") || !strings.Contains(tc, "sent ") || !has(keys, "class ") || !has(keys, "qdisc ") {
			t.Errorf("fault: no class %s with its netem queue and counters in\n%s", class, tc)
		}
	}
	for _, f := range theirs {
		if strings.Contains(nft, compiler.MarkChainName(f.ID)+" ") || strings.Contains(tc, "class "+compiler.ClassIDOf(f.ID, compiler.Upload)+" ") {
			t.Errorf("fault: the other fault's mark %d is shown", f.ID)
		}
	}

	// a device: its identity element and the classification of its address, nothing of another device
	keys, nft, _ = view("device", espID)
	t.Logf("device:\n%v\n%s", keys, nft)
	num := snap.DeviceNums[espID]
	if num == 0 || !has(keys, "map ident4_") || !strings.Contains(nft, fmt.Sprintf("10.10.0.31 : %d", num)) || !has(keys, "map cls_") {
		t.Errorf("device: no identity element 10.10.0.31 : %d or no classification element in %v", num, keys)
	}
	if strings.Contains(nft, "10.10.0.50") {
		t.Error("device: another device's address is shown")
	}
	// by name too
	if byName, _, _ := view("device", "esp32-42"); len(byName) != len(keys) {
		t.Errorf("by name: %v, by id: %v", byName, keys)
	}

	// nothing of an object the kernel does not hold
	for _, k := range []string{"rule", "fault", "device"} {
		if keys, nft, tc := view(k, "aaaaaaaa-0000-4000-8000-000000000000"); len(keys) != 0 || nft != "" || tc != "" {
			t.Errorf("%s that is not there: %v %q %q", k, keys, nft, tc)
		}
	}
	if keys, _, _ := view("rule", ruleC); len(keys) != 0 {
		t.Errorf("a disabled rule: %v", keys)
	}
	// a configured rule by its name
	if byName, _, _ := view("rule", "no-dot"); !has(byName, "counter "+b.Counter) {
		t.Errorf("the rule by its name: %v", byName)
	}
}

// The view finds a rule by its name without asking which of several: the configuration cannot hold two
// access rules of one name (ignoring case), nor a rule whose name is the form of an id (review B5 round 1).
func TestTheLinuxViewFindsARuleByANameThatOnlyOneRuleCanCarry(t *testing.T) {
	g := dhcpGateway(t)
	p := rulesPatch()
	rules := p["access_rules"].(map[string]any)
	ruleD := "a1000000-0000-4000-8000-000000000004"
	ruleE := "a1000000-0000-4000-8000-000000000005"
	rules[ruleD] = map[string]any{"name": "NO-DOT", "source": map[string]any{"network": "IoT"}, "protocol": "tcp", "ports": []int{854}, "action": "drop"}
	rules[ruleE] = map[string]any{"name": ruleA, "source": map[string]any{"network": "IoT"}, "protocol": "tcp", "ports": []int{855}, "action": "drop"}
	p["access_rule_order"] = []string{ruleB, ruleC, ruleA, ruleD, ruleE}
	r := g.patch(p)
	if r.Status != 409 || !strings.Contains(string(r.Body), `"duplicate_name"`) || !strings.Contains(string(r.Body), `"name_is_uuid"`) ||
		!strings.Contains(string(r.Body), "/access_rules/"+ruleD+"/name") || !strings.Contains(string(r.Body), "/access_rules/"+ruleE+"/name") {
		t.Errorf("two rules of one name, a name of an id's form: %d %s", r.Status, r.Body)
	}
}
