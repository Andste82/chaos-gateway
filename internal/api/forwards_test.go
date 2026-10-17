package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// M35a over the API: the port forwards of a revision with their status, the validation with the pointer
// of the refused field, the preview's list and its note on faults, and explain for a forwarded connection.

const (
	fwdWeb   = "f0000000-0000-4000-8000-000000000001"
	fwdRange = "f0000000-0000-4000-8000-000000000002"
	fwdOff   = "f0000000-0000-4000-8000-000000000003"
	fwdNew   = "f0000000-0000-4000-8000-000000000009"
)

func forwardsPatch() map[string]any {
	return map[string]any{"port_forwards": map[string]any{
		fwdWeb:   map[string]any{"name": "web", "protocol": "tcp", "entry": "uplink", "external_port": 8080, "target": map[string]any{"device": "esp32-42"}, "target_port": 80},
		fwdRange: map[string]any{"name": "range", "protocol": "udp", "entry": "management", "external_port": 5000, "external_port_end": 5010, "target": map[string]any{"address": "10.10.0.20"}, "target_port": 6000},
		fwdOff:   map[string]any{"name": "off", "protocol": "tcp", "entry": "uplink", "external_port": 9000, "target": map[string]any{"address": "10.10.0.21"}, "enabled": false},
	}}
}

// forwardStatus is the status of a forward in a list, by name.
func forwardStatus(t *testing.T, items []any, name string) map[string]any {
	t.Helper()
	for _, it := range items {
		m := it.(map[string]any)
		if m["config"].(map[string]any)["name"] == name {
			return m["status"].(map[string]any)
		}
	}
	t.Fatalf("no forward %s in %v", name, items)
	return nil
}

func TestPortForwardsAreListedWithTheirStatus(t *testing.T) {
	g := dhcpGateway(t)
	if r := g.apply(g.mustPatch(forwardsPatch())); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.observe()
	list := g.do("GET", "/port-forwards", nil, nil, nil)
	if list.Status != 200 {
		t.Fatalf("%d %s", list.Status, list.Body)
	}
	items := list.json(t)["items"].([]any)
	var names []string
	for _, it := range items {
		names = append(names, it.(map[string]any)["config"].(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "off,range,web" {
		t.Fatalf("names %v", names)
	}
	// the device has no address yet: its forward is inactive and says why
	if st := forwardStatus(t, items, "web"); st["state"] != "inactive" || st["reason"] != "device_without_address" || st["message"] != "inactive: the device has no address" || st["target_address"] != nil {
		t.Errorf("web %v", st)
	}
	if st := forwardStatus(t, items, "range"); st["state"] != "active" || st["target_address"] != "10.10.0.20" || st["reason"] != nil {
		t.Errorf("range %v", st)
	}
	if st := forwardStatus(t, items, "off"); st["state"] != "inactive" || st["reason"] != "disabled" {
		t.Errorf("off %v", st)
	}

	// the device appears: the forward follows its address
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	web := g.do("GET", "/port-forwards/web", nil, nil, nil)
	if web.Status != 200 {
		t.Fatalf("%d %s", web.Status, web.Body)
	}
	wb := web.json(t)
	if st := wb["status"].(map[string]any); wb["id"] != fwdWeb || st["state"] != "active" || st["target_address"] != "10.10.0.31" {
		t.Errorf("web %v", wb)
	}
	// stored with the device's UUID, by id as by name
	if dev := wb["config"].(map[string]any)["target"].(map[string]any)["device"]; dev != espID {
		t.Errorf("target %v", dev)
	}
	if r := g.do("GET", "/port-forwards/"+fwdRange, nil, nil, nil); r.Status != 200 || r.json(t)["config"].(map[string]any)["name"] != "range" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/port-forwards/nothing", nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// paging
	p := g.do("GET", "/port-forwards?limit=2", nil, nil, nil).json(t)
	if len(p["items"].([]any)) != 2 || p["next_cursor"] == nil {
		t.Fatalf("%v", p)
	}
	rest := g.do("GET", "/port-forwards?limit=2&cursor="+p["next_cursor"].(string), nil, nil, nil).json(t)
	if len(rest["items"].([]any)) != 1 || rest["next_cursor"] != nil {
		t.Errorf("%v", rest)
	}
	// a candidate revision shows its own forwards
	cand := g.mustPatch(map[string]any{"port_forwards": map[string]any{fwdOff: nil}})
	if c := g.do("GET", "/port-forwards?revision="+itoa(cand), nil, nil, nil).json(t)["items"].([]any); len(c) != 2 {
		t.Errorf("candidate %v", c)
	}
	if a := g.do("GET", "/port-forwards", nil, nil, nil).json(t)["items"].([]any); len(a) != 3 {
		t.Errorf("active %v", a)
	}
	// no forwards: an empty list
	h := ready(t)
	if l := h.do("GET", "/port-forwards", nil, nil, nil).json(t)["items"].([]any); len(l) != 0 {
		t.Errorf("%v", l)
	}
}

func TestARefusedForwardIsReportedAtItsField(t *testing.T) {
	g := dhcpGateway(t)
	bgp := map[string]any{"asn": 64512, "protocols": map[string]any{"f1000000-0000-4000-8000-000000000001": map[string]any{
		"name": "bgp-b", "type": "bgp", "link": "site-b", "bgp": map[string]any{"neighbor_asn": 64513}}}}
	fwd := func(f map[string]any) map[string]any {
		if f["target"] == nil {
			f["target"] = map[string]any{"address": "10.10.0.20"}
		}
		return map[string]any{"port_forwards": map[string]any{fwdNew: f}}
	}
	at := "/port_forwards/" + fwdNew
	for _, c := range []struct {
		name  string
		patch map[string]any
		path  string
		code  string
	}{
		// the API listens on the compiler's default port in this harness: 443
		{"UI/API port", fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 443}), at + "/external_port", "reserved_port"},
		{"WireGuard listen port", fwd(map[string]any{"protocol": "udp", "entry": "uplink", "external_port": 51820}), at + "/external_port", "reserved_port"},
		{"SSH on the management side", fwd(map[string]any{"protocol": "tcp", "entry": "management", "external_port": 22}), at + "/external_port", "reserved_port"},
		{"BGP port while BGP runs", merge(fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 179}), map[string]any{"routing": bgp}), at + "/external_port", "reserved_port"},
		{"overlapping", merge(forwardsPatch(), fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 8000, "external_port_end": 8100})), at + "/external_port", "overlapping_forward"},
		{"ranges of different length", fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 8000, "external_port_end": 8010, "target_port": 9000, "target_port_end": 9001}), at + "/target_port_end", "port_range_length"},
		{"target in the management network", fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 9100, "target": map[string]any{"address": "192.168.56.9"}}), at + "/target/address", "invalid_target"},
		{"target on the gateway", fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 9100, "target": map[string]any{"address": "10.10.0.1"}}), at + "/target/address", "invalid_target"},
		{"target a WireGuard client", fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 9100, "target": map[string]any{"device": "rA"}}), at + "/target/device", "invalid_target"},
		{"management in the two-port topology", merge(fwd(map[string]any{"protocol": "tcp", "entry": "management", "external_port": 9100}),
			map[string]any{"management": map[string]any{"interface": map[string]any{"name": "wan0"}}}), at + "/entry", "management_is_uplink"},
		{"an unknown protocol", fwd(map[string]any{"protocol": "icmp", "entry": "uplink", "external_port": 9100}), at + "/protocol", "enum"},
	} {
		r := g.patch(c.patch)
		if r.Status != 422 || r.code(t) != "validation_failed" {
			t.Errorf("%s: %d %s", c.name, r.Status, r.Body)
			continue
		}
		found := false
		for _, e := range r.json(t)["errors"].([]any) {
			m := e.(map[string]any)
			found = found || (m["path"] == c.path && m["code"] == c.code)
		}
		if !found {
			t.Errorf("%s: want %s at %s, got %s", c.name, c.code, c.path, r.Body)
		}
	}
	// a valid one is taken, and a port range is fine
	if r := g.patch(fwd(map[string]any{"protocol": "tcp", "entry": "uplink", "external_port": 8000, "external_port_end": 8010, "target_port": 9000})); r.Status != 201 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

// merge adds the keys of b to a (b's top-level keys replace a's).
func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if m, ok := v.(map[string]any); ok {
			if old, ok := out[k].(map[string]any); ok {
				v = merge(old, m)
			}
		}
		out[k] = v
	}
	return out
}

func TestThePreviewListsTheForwardsAndSaysThatFaultsDoNotApply(t *testing.T) {
	g := dhcpGateway(t)
	patch := merge(forwardsPatch(), map[string]any{"faults": map[string]any{"f2000000-0000-4000-8000-000000000001": map[string]any{
		"name": "slow-esp", "source": map[string]any{"device": "esp32-42"}, "latency": "200ms"}}})
	id := g.mustPatch(patch)
	pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	body := pv.json(t)
	fwds, _ := body["port_forwards"].([]any)
	// the disabled forward is not listed
	if len(fwds) != 2 {
		t.Fatalf("port_forwards %v", body["port_forwards"])
	}
	r := fwds[0].(map[string]any)
	if r["name"] != "range" || r["external_ports"] != "5000-5010" || r["target_ports"] != "6000-6010" || r["target"] != "10.10.0.20" ||
		r["entry"] != "management" || r["protocol"] != "udp" || r["faults_apply"] != false || r["new"] != true {
		t.Errorf("range %v", r)
	}
	w := fwds[1].(map[string]any)
	if w["name"] != "web" || w["target"] != "device esp32-42" || w["external_ports"] != "8080" || w["target_ports"] != "80" {
		t.Errorf("web %v", w)
	}
	note := ""
	for _, x := range body["warnings"].([]any) {
		m := x.(map[string]any)
		if m["code"] == "faults_not_on_forwards" {
			if m["path"] != "/port_forwards/"+fwdWeb {
				t.Errorf("note at %v", m["path"])
			}
			note = m["message"].(string)
		}
	}
	if !strings.Contains(note, "faults do not apply to forwarded connections (M35b)") || !strings.Contains(note, "slow-esp") || !strings.Contains(note, "web") {
		t.Errorf("note %q in %v", note, body["warnings"])
	}
	// the domain diff names the forwards
	kinds := fmt.Sprint(body["domain"])
	if !strings.Contains(kinds, "port_forward") || !strings.Contains(kinds, `port forward "web" (tcp/8080 from the uplink to device esp32-42) added`) {
		t.Errorf("domain %v", body["domain"])
	}
	// a forward is not lockout-relevant
	if body["requires_confirm"] != false {
		t.Errorf("requires_confirm %v", body["requires_confirm"])
	}
	// applied, an unchanged forward is no longer new
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	next := g.mustPatch(map[string]any{"port_forwards": map[string]any{fwdRange: map[string]any{"target_port": 7000}}})
	fwds = g.do("POST", "/revisions/"+itoa(next)+"/preview", nil, nil, nil).json(t)["port_forwards"].([]any)
	if fwds[0].(map[string]any)["new"] != true || fwds[1].(map[string]any)["new"] != nil {
		t.Errorf("new %v", fwds)
	}
}

func TestExplainNamesTheForwardAndTheRuleThatDecidesAForwardedConnection(t *testing.T) {
	g := dhcpGateway(t)
	if r := g.apply(g.mustPatch(forwardsPatch())); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	// a peer behind the uplink to the uplink's address (203.0.113.1 in this harness) on the forward's port
	explain := func() map[string]any {
		r := g.do("GET", "/explain?src=198.51.100.7&dst=203.0.113.1&protocol=tcp&port=8080", nil, nil, nil)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		return r.json(t)
	}
	ex := explain()
	fw, ok := ex["forward"].(map[string]any)
	if !ok || fw["id"] != fwdWeb || fw["target_ip"] != "10.10.0.31" || fw["target_port"] != float64(80) || fw["entry"] != "uplink" ||
		fw["note"] != "faults do not apply to forwarded connections (M35b)" || fw["device"].(map[string]any)["name"] != "esp32-42" {
		t.Fatalf("forward %v", ex["forward"])
	}
	if a := ex["access"].(map[string]any); a["verdict"] != "allow" || a["layer"] != "port_forward" || a["rule"] != nil {
		t.Errorf("access %v", a)
	}
	if fs := ex["faults"].([]any); len(fs) != 0 {
		t.Errorf("faults %v", fs)
	}
	// an existing rule "device → uplink: drop" decides it, with the device as responder
	rule := map[string]any{"access_rules": map[string]any{ruleA: map[string]any{"name": "offline", "source": map[string]any{"device": "esp32-42"},
		"destination": map[string]any{"uplink": true}, "action": "drop"}}, "access_rule_order": []string{ruleA}}
	if r := g.apply(g.mustPatch(rule)); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if a := explain()["access"].(map[string]any); a["verdict"] != "drop" || a["layer"] != "config_rule" || a["rule"] != ruleA {
		t.Errorf("access with the rule %v", a)
	}
	// another port, or traffic that is not forwarded, is explained as before
	plain := g.do("GET", "/explain?src=198.51.100.7&dst=203.0.113.1&protocol=tcp&port=8081", nil, nil, nil).json(t)
	if plain["forward"] != nil {
		t.Errorf("not forwarded: %v", plain["forward"])
	}
	// the device goes: the forward is inactive and nothing is forwarded
	g.k.SetNeighbors(nil)
	g.observe()
	ex = explain()
	if st := ex["forward"].(map[string]any)["status"].(map[string]any); st["state"] != "inactive" || st["reason"] != "device_without_address" {
		t.Errorf("status %v", st)
	}
	if a := ex["access"].(map[string]any); a["verdict"] != "drop" {
		t.Errorf("access of an inactive forward %v", a)
	}
}

func TestTheCapabilitiesNamePortForwarding(t *testing.T) {
	g := ready(t)
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	if !strings.Contains(fmt.Sprint(caps["features"]), "port_forwards") {
		t.Errorf("%v", caps["features"])
	}
}

// The counter of a forward counts its connections (P31-M35a-09, option (b)): it is the counter of the
// forward's counting rule in the prerouting NAT chain, which sees the first packet of each connection
// before any access rule, with the epoch of the forward and the time the last connection arrived (the
// rule's `last`). No rule of the forward chain counts into it. Only the revision the kernel runs has
// counters, and only for the forwards in its packet path (a disabled one is not).
func TestAForwardCountsItsConnections(t *testing.T) {
	g := dhcpGateway(t)
	if r := g.apply(g.mustPatch(forwardsPatch())); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.observe()
	res, err := g.k.Run(context.Background(), executor.Command{Tool: executor.ToolNft, Args: []string{"-j", "list", "table", "inet", "chaosgw"}})
	if err != nil || res.Exit != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	rs, err := linux.ParseNft([]byte(res.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fwdWeb, fwdRange} {
		var chains []string
		for _, o := range rs.Objects {
			if o.Rule != nil && o.Rule.CounterName() == compiler.ForwardCounterName(id) {
				_, _, last := o.Rule.Last()
				chains = append(chains, fmt.Sprintf("%s last=%v", o.Rule.Chain, last))
			}
		}
		if fmt.Sprint(chains) != "[prerouting last=true]" {
			t.Errorf("the rules that count into the counter of %s: %v", id, chains)
		}
	}
	g.k.BumpCounter(compiler.ForwardCounterName(fwdRange), 3)
	items := g.do("GET", "/port-forwards", nil, nil, nil).json(t)["items"].([]any)
	counters := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		if c, ok := m["counters"].(map[string]any); ok {
			counters[m["config"].(map[string]any)["name"].(string)] = c
		}
	}
	if c := counters["range"]; c == nil || c["packets"] != float64(3) || c["bytes"] != float64(300) || c["epoch"] == nil || c["last_match"] == nil {
		t.Errorf("range %v", c)
	}
	// the device of web has no address yet: its forward is in the kernel, inactive, and counts
	if c := counters["web"]; c == nil || c["packets"] != float64(0) || c["last_match"] != nil {
		t.Errorf("web %v", c)
	}
	if c, ok := counters["off"]; ok {
		t.Errorf("a disabled forward has a counter: %v", c)
	}
	// a candidate revision has none
	cand := g.mustPatch(map[string]any{"port_forwards": map[string]any{fwdOff: nil}})
	for _, it := range g.do("GET", "/port-forwards?revision="+itoa(cand), nil, nil, nil).json(t)["items"].([]any) {
		if c, ok := it.(map[string]any)["counters"]; ok {
			t.Errorf("a candidate's forward has a counter: %v", c)
		}
	}
	if r := g.do("GET", "/port-forwards/range", nil, nil, nil).json(t); r["counters"].(map[string]any)["packets"] != float64(3) {
		t.Errorf("by name %v", r["counters"])
	}
}
