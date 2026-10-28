package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// M26a: `chaosctl probe list`, `probe add` and `probe remove` against the API on the simulated kernel with the real engine (a
// subtest of TestChaosctlOverlays: one gateway with the configured device esp32-42 on IoT). Add and remove make a revision and
// apply it, which the test reads back from the API and the simulated kernel; the subtest leaves the gateway as it found it.

// probeIDsOf is the ids of the probes the API lists, by name.
func probeIDsOf(t *testing.T, g *gw) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range g.do("GET", "/probes", nil, nil, nil).json(t)["items"].([]any) {
		m := p.(map[string]any)
		out[sub(m, "config")["name"].(string)] = m["id"].(string)
	}
	return out
}

func ctlManagesProbes(t *testing.T, g *gw, clk *clock.Fake) {
	before := g.do("GET", "/revisions/active", nil, nil, nil).json(t)["id"].(float64)

	// no probe yet: the list is empty and says how to make one
	if r := g.ctl("probe", "list"); r.code != 0 || r.out != "" || !strings.Contains(r.err, "probe add") {
		t.Errorf("no probes: %+v", r)
	}

	// add makes a revision and applies it: the probe is a probe of the configuration, a port of the network, a device
	r := g.ctl("probe", "add", "ci-probe", "--network", "IoT", "--trusts-test-ca")
	if r.code != 0 || !strings.HasPrefix(r.out, "added probe ci-probe (") || !strings.Contains(r.out, "in revision") {
		t.Fatalf("add: %+v", r)
	}
	if strings.Contains(r.err, "warning") {
		t.Errorf("a warning for a network with DHCP: %q", r.err)
	}
	ids := probeIDsOf(t, g)
	id := ids["ci-probe"]
	if id == "" || !strings.Contains(r.out, id) {
		t.Fatalf("the probe is listed under the id add printed: %v, %q", ids, r.out)
	}
	cfg := g.do("GET", "/revisions/active", nil, nil, nil).json(t)
	if got := cfg["id"].(float64); got != before+1 {
		t.Errorf("one revision more: %v after %v", got, before)
	}
	probe := sub(sub(sub(cfg, "configuration"), "probes"), id)
	if probe["name"] != "ci-probe" || probe["network"] != iotID || probe["trusts_test_ca"] != true {
		t.Errorf("the network by name is stored as its UUID: %v", probe)
	}
	// the executor built the probe: its namespace and the end of its pair that is a port of the bridge
	ns, hostIf := "cgp-"+id[:8], "pv"+id[:8]
	if !g.k.HasLink(hostIf) {
		t.Errorf("the pair %s of the probe is not there", hostIf)
	}

	// list: name, network by name, no address yet; then the address its DHCP client got
	r = g.ctl("probe", "list")
	if r.code != 0 || !strings.Contains(r.out, "ci-probe") || !strings.Contains(r.out, "IoT") || !strings.Contains(r.out, "no address yet") || !strings.Contains(r.out, id) {
		t.Errorf("list before the address: %+v", r)
	}
	if _, err := g.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, NS: ns, Args: []string{"addr", "replace", "10.10.0.88/24", "dev", "eth0"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.e.ObserveNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := g.ctl("probe", "list"); r.code != 0 || !strings.Contains(r.out, "10.10.0.88") || strings.Contains(r.out, "no address yet") {
		t.Errorf("list with the address: %+v", r)
	}
	r = g.ctl("probe", "list", "--json")
	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Config struct {
				Name string `json:"name"`
			} `json:"config"`
			Observed struct {
				Addresses []string `json:"addresses"`
			} `json:"observed"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.out), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].Observed.Addresses[0] != "10.10.0.88" {
		t.Errorf("--json is the API's list: %+v %v", r, err)
	}

	// add refuses what the gateway refuses, with its message, and leaves no candidate behind
	revisions := func() int { return len(g.do("GET", "/revisions", nil, nil, nil).json(t)["items"].([]any)) }
	n := revisions()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"probe", "add", "CI-probe", "--network", "IoT"}, "name_taken"},
		{[]string{"probe", "add", "esp32-42", "--network", "IoT"}, "name_taken"},
		{[]string{"probe", "add", "other", "--network", "no-such-network"}, "validation_failed"},
	} {
		if r := g.ctl(tc.args...); r.code != 2 || !strings.Contains(r.err, tc.want) || r.out != "" {
			t.Errorf("%v: %+v", tc.args, r)
		}
	}
	if got := revisions(); got != n {
		t.Errorf("a refused add stores no revision: %d, was %d", got, n)
	}
	if r := g.ctl("probe", "add", "no-network"); r.code != 2 || !strings.Contains(r.err, "--network") {
		t.Errorf("a probe without a network: %+v", r)
	}
	if r := g.ctl("probe", "add"); r.code != 2 {
		t.Errorf("a probe without a name: %+v", r)
	}

	// an access rule that names the probe keeps it: the gateway refuses the removal with the reason, and the candidate is discarded
	patch := g.mustPatch(map[string]any{"access_rules": map[string]any{"a1000000-0000-4000-8000-000000000001": map[string]any{
		"name": "probe-no-dns", "source": map[string]any{"device": id}, "protocol": "udp", "ports": []any{53}, "action": "drop"}}, "access_rule_order": []any{"a1000000-0000-4000-8000-000000000001"}})
	if r := g.apply(patch); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	n = revisions()
	if r := g.ctl("probe", "remove", "ci-probe"); r.code != 2 || !strings.Contains(r.err, "/access_rules/") || !strings.Contains(r.err, "there is no device") {
		t.Errorf("a probe a rule names: %+v", r)
	}
	if got := revisions(); got != n || probeIDsOf(t, g)["ci-probe"] != id {
		t.Errorf("a refused removal changes nothing and stores no revision: %d revisions, was %d", got, n)
	}
	patch = g.mustPatch(map[string]any{"access_rules": map[string]any{"a1000000-0000-4000-8000-000000000001": nil}, "access_rule_order": []any{}})
	if r := g.apply(patch); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}

	// a scenario that targets the probe, or a step of it, keeps it too: a scenario is a file of its own and chaosctl does not change it
	scn := "5c000000-0000-4000-8000-000000000001"
	for name, target := range map[string]map[string]any{
		"the scenario's target": {"device": id},
		"a step's target":       {"network": "IoT"},
	} {
		sc := map[string]any{"name": "probe-outage", "target": map[string]any{"network": "IoT"},
			"steps": []any{map[string]any{"id": "cut", "at": "0s", "fault": map[string]any{"latency": "100ms"}}}}
		if name == "the scenario's target" {
			sc["target"] = target
		} else {
			sc["steps"] = []any{map[string]any{"id": "cut", "at": "0s", "target": map[string]any{"device": id}, "fault": map[string]any{"latency": "100ms"}}}
		}
		if r := g.apply(g.mustPatch(map[string]any{"scenarios": map[string]any{scn: sc}})); r.Status != 200 {
			t.Fatalf("%s: %d %s", name, r.Status, r.Body)
		}
		n = revisions()
		if r := g.ctl("probe", "remove", "ci-probe"); r.code != 2 || !strings.Contains(r.err, "/scenarios/") || r.out != "" {
			t.Errorf("a probe a scenario names (%s): %+v", name, r)
		}
		if got := revisions(); got != n || probeIDsOf(t, g)["ci-probe"] != id {
			t.Errorf("a refused removal changes nothing and stores no revision (%s): %d revisions, was %d", name, got, n)
		}
		if r := g.apply(g.mustPatch(map[string]any{"scenarios": map[string]any{scn: nil}})); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}

	// an overlay on the probe blocks the removal until --force takes it along
	ov := g.do("POST", "/overlays", map[string]any{"target": map[string]any{"device": id}, "fault": map[string]any{"latency": "20ms"}}, nil, nil)
	if ov.Status != 201 && ov.Status != 200 {
		t.Fatalf("overlay: %d %s", ov.Status, ov.Body)
	}
	if r := g.ctl("probe", "remove", "ci-probe"); r.code != 2 || !strings.Contains(r.err, "used by overlay") {
		t.Errorf("a probe an overlay targets: %+v", r)
	}
	r = g.ctl("probe", "remove", "CI-PROBE", "--force")
	if r.code != 0 || !strings.HasPrefix(r.out, "removed probe ci-probe ("+id+")") || !strings.Contains(r.err, "removed 1 overlay") {
		t.Errorf("remove --force: %+v", r)
	}
	if _, ok := probeIDsOf(t, g)["ci-probe"]; ok {
		t.Error("the probe is still listed")
	}
	if len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)) != 0 {
		t.Error("the overlay of the probe is still there")
	}
	// the executor took it down
	if g.k.HasLink(hostIf) {
		t.Errorf("the pair %s of the removed probe is still there", hostIf)
	}

	// remove by id, and a probe that does not exist
	r = g.ctl("probe", "add", "second", "--network", "IoT", "--json")
	var added struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	if err := json.Unmarshal([]byte(r.out), &added); r.code != 0 || err != nil || added.ID == "" || added.Revision == 0 {
		t.Fatalf("add --json: %+v %v", r, err)
	}
	if r := g.ctl("probe", "remove", added.ID); r.code != 0 || !strings.Contains(r.out, "removed probe second") {
		t.Errorf("remove by id: %+v", r)
	}
	if r := g.ctl("probe", "remove", "second"); r.code != 2 || !strings.Contains(r.err, `there is no probe "second"`) {
		t.Errorf("remove of a probe that is gone: %+v", r)
	}

	// a network with DHCP switched off gives the probe no address: add still adds it and says so (P6-AUDIT-67)
	dhcp := func(on bool) {
		t.Helper()
		if r := g.apply(g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"dhcp": map[string]any{"enabled": on}}}})); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}
	dhcp(false)
	for _, network := range []string{"IoT", iotID} {
		r = g.ctl("probe", "add", "no-address", "--network", network)
		if r.code != 0 || !strings.HasPrefix(r.out, "added probe no-address (") || !strings.Contains(r.err, "warning: DHCP is off in the network IoT: the probe will get no address") {
			t.Errorf("add in a network without DHCP (%s): %+v", network, r)
		}
		if r := g.ctl("probe", "remove", "no-address"); r.code != 0 {
			t.Errorf("remove: %+v", r)
		}
	}
	dhcp(true)

	// listing needs the scope read, adding and removing the scope full: a token for overlays lists and is refused the rest
	admin := g.token
	g.token = g.mintToken("overlays")
	if r := g.ctl("probe", "list"); r.code != 0 {
		t.Errorf("list with the overlays scope: %+v", r)
	}
	if r := g.ctl("probe", "add", "denied", "--network", "IoT"); r.code != 2 || !strings.Contains(r.err, "forbidden") {
		t.Errorf("add with the overlays scope: %+v", r)
	}
	g.token = admin
	if _, ok := probeIDsOf(t, g)["denied"]; ok {
		t.Error("a probe was made with the overlays scope")
	}
}
