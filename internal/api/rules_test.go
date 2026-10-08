package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// M9 over the API: configured rules in their order with state and counters behind the system rule,
// rule overlays in front of them, the capacity limit, explain naming the rule and the preview showing
// what a revision with rules does.

const (
	ruleA = "a1000000-0000-4000-8000-000000000001"
	ruleB = "a1000000-0000-4000-8000-000000000002"
	ruleC = "a1000000-0000-4000-8000-000000000003"
)

func rulesPatch() map[string]any {
	return map[string]any{
		"access_rules": map[string]any{
			ruleA: map[string]any{"name": "no-dns", "source": map[string]any{"network": "IoT"}, "protocol": "udp", "ports": []int{53}, "action": "drop"},
			ruleB: map[string]any{"name": "no-dot", "source": map[string]any{"network": "IoT"}, "protocol": "tcp", "ports": []int{853}, "action": "reject", "cut_existing": true},
			ruleC: map[string]any{"name": "off", "source": map[string]any{"global": true}, "action": "drop", "enabled": false},
		},
		"access_rule_order": []string{ruleB, ruleC, ruleA},
	}
}

func TestTheRulesAreListedInTheirOrderWithStateCountersAndTheSystemRule(t *testing.T) {
	g := ready(t)
	if r := g.apply(g.mustPatch(rulesPatch())); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	list := g.do("GET", "/rules", nil, nil, nil)
	if list.Status != 200 {
		t.Fatalf("%d %s", list.Status, list.Body)
	}
	body := list.json(t)
	items := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("%s", list.Body)
	}
	var names, states []string
	for i, it := range items {
		m := it.(map[string]any)
		names = append(names, m["config"].(map[string]any)["name"].(string))
		states = append(states, m["state"].(string))
		if int(m["position"].(float64)) != i {
			t.Errorf("position %v at %d", m["position"], i)
		}
	}
	if strings.Join(names, ",") != "no-dot,off,no-dns" || strings.Join(states, ",") != "effective,disabled,effective" {
		t.Errorf("names %v states %v", names, states)
	}
	// a rule in the packet path has counters, a disabled one has none
	if c, ok := items[0].(map[string]any)["counters"].(map[string]any); !ok || c["epoch"] == nil || c["packets"] != float64(0) {
		t.Errorf("counters %v", items[0])
	}
	if _, ok := items[1].(map[string]any)["counters"]; ok {
		t.Errorf("a disabled rule has counters: %v", items[1])
	}
	// the anti-lockout rule is listed, locked, in front
	sys := body["system_rules"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["key"] != "system:anti_lockout" || sys[0].(map[string]any)["counters"] == nil {
		t.Errorf("system rules %v", sys)
	}

	// one rule by id and by name, and an unknown one
	if r := g.do("GET", "/rules/"+ruleA, nil, nil, nil); r.Status != 200 || r.json(t)["position"] != float64(2) {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/rules/no-dot", nil, nil, nil); r.Status != 200 || r.json(t)["id"] != ruleB {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/rules/nothing", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// paging
	p := g.do("GET", "/rules?limit=2", nil, nil, nil).json(t)
	if len(p["items"].([]any)) != 2 || p["next_cursor"] == nil {
		t.Errorf("%v", p)
	}
	rest := g.do("GET", "/rules?limit=2&cursor="+p["next_cursor"].(string), nil, nil, nil).json(t)
	if len(rest["items"].([]any)) != 1 || rest["next_cursor"] != nil {
		t.Errorf("%v", rest)
	}
}

func TestACandidateRevisionShowsItsRulesWithoutCountersAndWithTheirState(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(rulesPatch())
	r := g.do("GET", "/rules?revision="+itoa(id), nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	items := r.json(t)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("%s", r.Body)
	}
	for _, it := range items {
		if _, ok := it.(map[string]any)["counters"]; ok {
			t.Errorf("a revision the kernel does not run has counters: %v", it)
		}
	}
}

func TestARuleOverlayIsListedWithItsStateAndCountersAndStandsBeforeTheConfiguredRules(t *testing.T) {
	g := ready(t)
	g.apply(g.mustPatch(rulesPatch()))
	g.mintToken("overlays")
	r := g.createOverlay(`{"target":{"network":"IoT"},"rule":{"protocol":"udp","ports":[53],"action":"reject"},"ttl":"10m"}`)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ov := r.json(t)
	if ov["kind"] != "rule" || ov["state"] != "effective" {
		t.Errorf("%v", ov)
	}
	got := g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).json(t)
	if c, ok := got["counters"].(map[string]any); !ok || c["epoch"] == nil {
		t.Errorf("%v", got)
	}
	if list := g.do("GET", "/overlays?kind=rule", nil, nil, nil).json(t)["items"].([]any); len(list) != 1 {
		t.Errorf("%v", list)
	}
	// the explanation names the overlay, then the configured rule when it is gone
	ex := g.do("GET", "/explain?src=10.10.0.77&dst=10.10.0.1&protocol=udp&port=53", nil, nil, nil)
	if ex.Status != 200 {
		t.Fatalf("%d %s", ex.Status, ex.Body)
	}
	a := ex.json(t)["access"].(map[string]any)
	if a["verdict"] != "reject" || a["layer"] != "overlay_rule" || a["rule"] != ov["id"] {
		t.Errorf("%v", a)
	}
	if d := g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil); d.Status != 204 {
		t.Fatalf("%d %s", d.Status, d.Body)
	}
	a = g.do("GET", "/explain?src=10.10.0.77&dst=10.10.0.1&protocol=udp&port=53", nil, nil, nil).json(t)["access"].(map[string]any)
	if a["verdict"] != "drop" || a["layer"] != "config_rule" || a["rule"] != ruleA {
		t.Errorf("%v", a)
	}
}

func TestTheValidationOfARuleOverlayExplainsWhatIsWrong(t *testing.T) {
	g := ready(t)
	for body, path := range map[string]string{
		`{"target":{"network":"IoT"},"rule":{"action":"allow","cut_existing":true}}`:          "/rule/cut_existing",
		`{"target":{"network":"IoT"},"rule":{"action":"reset","protocol":"udp"}}`:             "/rule/action",
		`{"target":{"network":"IoT"},"rule":{"action":"drop","protocol":"icmp","ports":[1]}}`: "/rule/protocol",
		`{"target":{"network":"Nowhere"},"rule":{"action":"drop"}}`:                           "/target/network",
	} {
		r := g.createOverlay(body)
		if r.Status != 422 || r.code(t) != "validation_failed" {
			t.Errorf("%s: %d %s", body, r.Status, r.Body)
			continue
		}
		errs := r.json(t)["errors"].([]any)
		if errs[0].(map[string]any)["path"] != path {
			t.Errorf("%s: %v", body, errs)
		}
	}
}

// The limit this test assumes is compiler.DefaultRuleLimit, 1000 rules with the overlays, the same on
// every architecture.
func TestARevisionWithMoreRulesThanTheLimitIsRefusedAtPreviewAndApply(t *testing.T) {
	g := ready(t)
	rules, order := map[string]any{}, []string{}
	for i := 0; i < compiler.DefaultRuleLimit+1; i++ {
		id := fmt.Sprintf("a1000000-0000-4000-8000-%012d", i+1)
		rules[id] = map[string]any{"source": map[string]any{"network": "IoT"}, "protocol": "udp", "ports": []int{1 + i%60000}, "action": "drop"}
		order = append(order, id)
	}
	id := g.mustPatch(map[string]any{"access_rules": rules, "access_rule_order": order})
	for _, path := range []string{"/revisions/" + itoa(id) + "/preview", "/revisions/" + itoa(id) + "/apply"} {
		r := g.do("POST", path, nil, nil, nil)
		if r.Status != 422 || r.code(t) != "capacity_exceeded" || !strings.Contains(r.json(t)["detail"].(string), "limit of 1000") {
			t.Errorf("%s: %d %s", path, r.Status, r.Body)
		}
	}
	// exactly at the limit it fits
	delete(rules, order[len(order)-1])
	id = g.mustPatch(map[string]any{"access_rules": rules, "access_rule_order": order[:len(order)-1]})
	if r := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil); r.Status != 200 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

// A rule that names a hostname is not compiled before M20: the preview says so, the list shows the rule as
// not in the packet path (P2-M9-02).
func TestARuleThatNamesAHostnameIsReportedByThePreviewAndNotInThePacketPath(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(map[string]any{
		"access_rules": map[string]any{ruleA: map[string]any{"name": "block-cdn", "source": map[string]any{"network": "IoT"},
			"destination": map[string]any{"hostname": "cdn.example.com"}, "action": "drop"}},
		"access_rule_order": []string{ruleA},
	})
	pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	var found bool
	for _, w := range pv.json(t)["warnings"].([]any) {
		if w.(map[string]any)["code"] == "hostname_unresolved" {
			found = true
		}
	}
	if !found {
		t.Errorf("no hostname_unresolved warning: %s", pv.Body)
	}
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if st := g.do("GET", "/rules/"+ruleA, nil, nil, nil).json(t)["state"]; st != "disabled" {
		t.Errorf("state %v", st)
	}
}

func TestThePreviewListsTheEffectiveRulesInOrderAndMarksTheNewOnes(t *testing.T) {
	g := ready(t)
	g.apply(g.mustPatch(rulesPatch()))
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"rule":{"protocol":"tcp","ports":[8883],"action":"reset"}}`)

	// the same revision again plus one rule: the overlay's rule stands first (the preview keeps the overlays
	// the revision does not orphan), the configured ones keep their order, only the added one is new
	patch := rulesPatch()
	patch["access_rules"].(map[string]any)["a1000000-0000-4000-8000-000000000009"] = map[string]any{
		"name": "no-ntp", "source": map[string]any{"network": "IoT"}, "protocol": "udp", "ports": []int{123}, "action": "drop"}
	patch["access_rule_order"] = []string{ruleB, ruleC, ruleA, "a1000000-0000-4000-8000-000000000009"}
	id := g.mustPatch(patch)
	pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	rules := pv.json(t)["rules"].([]any)
	var got []string
	news := map[string]bool{}
	for _, r := range rules {
		m := r.(map[string]any)
		got = append(got, m["layer"].(string)+":"+m["action"].(string))
		if m["new"] == true {
			news[m["layer"].(string)+":"+m["action"].(string)] = true
		}
	}
	// the disabled rule is not in the effective list
	if want := "overlay:reset,config:reject,config:drop,config:drop"; strings.Join(got, ",") != want {
		t.Errorf("rules %v, want %s", got, want)
	}
	if len(news) != 1 || !news["config:drop"] {
		t.Errorf("new %v", news)
	}
	first := rules[0].(map[string]any)
	if first["id"] != ov["id"] || first["key"] != "overlay:"+ov["id"].(string) || first["position"] != float64(0) || first["ports"].([]any)[0] != "8883" || first["scope"] != "network IoT" {
		t.Errorf("%v", first)
	}
	if second := rules[1].(map[string]any); second["cut_existing"] != true || second["name"] != "no-dot" {
		t.Errorf("%v", second)
	}
	// a revision without rules and without overlays has no list
	if r := g.do("POST", "/revisions/"+itoa(g.mustPatch(map[string]any{"access_rules": nil, "access_rule_order": nil}))+"/preview", nil, nil, nil); r.Status != 200 {
		t.Errorf("%d %s", r.Status, r.Body)
	} else if _, ok := r.json(t)["rules"]; !ok {
		// the overlay's rule remains: it is not a rule of the revision
		t.Errorf("the overlay's rule is missing: %s", r.Body)
	}
}
