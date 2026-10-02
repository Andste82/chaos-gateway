package domain

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func changeOf(t *testing.T, changes []model.DomainChange, kind, op string) model.DomainChange {
	t.Helper()
	for _, c := range changes {
		if string(c.Kind) == kind && string(c.Op) == op {
			return c
		}
	}
	t.Fatalf("no %s %s in %+v", op, kind, summaries(changes))
	return model.DomainChange{}
}

func summaries(changes []model.DomainChange) []string {
	var out []string
	for _, c := range changes {
		out = append(out, string(c.Op)+" "+string(c.Kind)+": "+c.Summary)
	}
	return out
}

func TestDiffOfEqualConfigurationsIsEmpty(t *testing.T) {
	cfg := stored(t)
	c2 := clone(*cfg)
	if got := Diff(cfg, &c2); len(got) != 0 {
		t.Fatalf("changes: %v", summaries(got))
	}
	if got := Diff(nil, nil); len(got) != 0 {
		t.Fatalf("nil and nil: %v", summaries(got))
	}
}

func TestDiffFromNothingAddsEverything(t *testing.T) {
	got := Diff(nil, stored(t))
	kinds := map[string]int{}
	for _, c := range got {
		if c.Op != "added" {
			t.Errorf("unexpected %s %s", c.Op, c.Kind)
		}
		kinds[string(c.Kind)]++
	}
	want := map[string]int{"uplink": 1, "management": 1, "network": 3, "client": 1, "link_peer": 1, "routing": 1,
		"routing_protocol": 1, "access_matrix": 1, "device": 2, "group": 2, "probe": 1, "access_rule": 2,
		"fault": 5, "profile": 1, "scenario": 1, "settings": 1}
	// access_rule counts the rule and the order
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("%s: %d added, want %d (all: %v)", k, kinds[k], n, kinds)
		}
	}
	// and the reverse removes them
	for _, c := range Diff(stored(t), nil) {
		if c.Op != "removed" {
			t.Errorf("unexpected %s %s", c.Op, c.Kind)
		}
	}
}

func TestDiffNamesTheFieldThatChanged(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	f := (*next.Faults)[idFaultNet]
	lat := "250ms"
	f.Latency = &lat
	(*next.Faults)[idFaultNet] = f

	got := Diff(base, &next)
	if len(got) != 1 {
		t.Fatalf("changes: %v", summaries(got))
	}
	c := got[0]
	if c.Op != "changed" || c.Kind != "fault" || *c.Id != idFaultNet || *c.Name != "iot-latency" || c.Path != "/faults/"+idFaultNet {
		t.Fatalf("change = %+v", c)
	}
	if want := `fault "iot-latency" (network IoT) changed: latency 100ms → 250ms`; c.Summary != want {
		t.Fatalf("summary = %q, want %q", c.Summary, want)
	}
	fs := *c.Fields
	if len(fs) != 1 || fs[0].Path != "/latency" || fs[0].Old != "100ms" || fs[0].New != "250ms" {
		t.Fatalf("fields = %+v", fs)
	}
}

func TestDiffAddedRemovedAndAFieldThatAppearsOrVanishes(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	delete(*next.Groups, idG2)
	(*next.Devices)[idNew] = model.Device{Name: "new-device", Identifiers: &model.DeviceIdentifiers{Macs: &[]string{"24:0a:c4:00:00:99"}}}
	esp := (*next.Devices)[idESP]
	esp.FixedIp = nil
	ca := true
	esp.TrustsTestCa = &ca
	(*next.Devices)[idESP] = esp

	got := Diff(base, &next)
	if c := changeOf(t, got, "group", "removed"); *c.Name != "g2" || c.Summary != `group "g2" removed` {
		t.Errorf("removed group: %+v", c)
	}
	if c := changeOf(t, got, "device", "added"); *c.Name != "new-device" || c.Summary != `device "new-device" added` {
		t.Errorf("added device: %+v", c)
	}
	c := changeOf(t, got, "device", "changed")
	if !strings.Contains(c.Summary, "fixed_ip 10.10.0.42 → (unset)") || !strings.Contains(c.Summary, "trusts_test_ca false → true") {
		t.Errorf("changed device: %s", c.Summary)
	}
}

func TestDiffRendersReferencesAsNames(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	esp := (*next.Devices)[idESP]
	hub := idHub
	esp.Network = &hub
	(*next.Devices)[idESP] = esp
	got := Diff(base, &next)
	if len(got) != 1 || !strings.Contains(got[0].Summary, "network IoT → lab-hub") {
		t.Fatalf("summary = %v", summaries(got))
	}
}

func TestDiffShowsClientsAndLinkPeersAsObjectsOfTheirOwn(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	nets := *next.Networks

	hub, _ := nets[idHub].AsWireGuardNetwork()
	c := (*hub.Clients)[idClient]
	c.Address = "10.99.0.9"
	(*hub.Clients)[idClient] = c
	(*hub.Clients)[idNew] = model.WireGuardClient{Name: "lab-rB", Address: "10.99.0.3"}
	hubNet := nets[idHub]
	_ = hubNet.FromWireGuardNetwork(hub)
	nets[idHub] = hubNet

	link, _ := nets[idLink].AsWireGuardNetwork()
	link.Peer.Address = "10.255.0.1"
	keep := "40s"
	link.Peer.Keepalive = &keep
	linkNet := nets[idLink]
	_ = linkNet.FromWireGuardNetwork(link)
	nets[idLink] = linkNet

	got := Diff(base, &next)
	if changeOf(t, got, "client", "added"); !hasPath(got, "/networks/"+idHub+"/clients/"+idNew) {
		t.Errorf("added client missing: %v", summaries(got))
	}
	ch := changeOf(t, got, "client", "changed")
	if *ch.Name != "lab-rA" || !strings.Contains(ch.Summary, "address 10.99.0.2 → 10.99.0.9") || ch.Path != "/networks/"+idHub+"/clients/"+idClient {
		t.Errorf("changed client: %+v", ch)
	}
	if lp := changeOf(t, got, "link_peer", "changed"); lp.Path != "/networks/"+idLink+"/peer" || !strings.Contains(lp.Summary, "keepalive") {
		t.Errorf("link peer: %+v", lp)
	}
	for _, c := range got {
		if c.Kind == "network" {
			t.Errorf("the network itself did not change: %s", c.Summary)
		}
	}
}

func hasPath(changes []model.DomainChange, path string) bool {
	for _, c := range changes {
		if c.Path == path {
			return true
		}
	}
	return false
}

func TestDiffOfRoutingSingletonsAndOrder(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	asn := int64(64999)
	next.Routing.Asn = &asn
	p := (*next.Routing.Protocols)[idProtocol]
	p.Bgp.NeighborAsn = 64600
	(*next.Routing.Protocols)[idProtocol] = p
	next.Uplink.Gateway = ptrTo("203.0.113.1")
	cc := "120s"
	next.Settings.CommitConfirmTimeout = &cc
	rules := *next.AccessRules
	rules[idNew] = model.AccessRule{Name: ptrTo("allow"), Source: model.Scope{Global: ptrTo(model.ScopeGlobal(true))}, Action: "allow"}
	*next.AccessRuleOrder = []uuidT{mustUUID(idNew), mustUUID(idRule)}

	got := Diff(base, &next)
	if c := changeOf(t, got, "routing", "changed"); c.Path != "/routing" || !strings.Contains(c.Summary, "asn 64512 → 64999") {
		t.Errorf("routing: %+v", c)
	}
	if c := changeOf(t, got, "routing_protocol", "changed"); *c.Name != "bgp-site-b" || !strings.Contains(c.Summary, "bgp/neighbor_asn 64513 → 64600") {
		t.Errorf("protocol: %+v", c)
	}
	if c := changeOf(t, got, "uplink", "changed"); !strings.Contains(c.Summary, "gateway (unset) → 203.0.113.1") {
		t.Errorf("uplink: %+v", c)
	}
	if c := changeOf(t, got, "settings", "changed"); !strings.Contains(c.Summary, "commit_confirm_timeout 60s → 120s") {
		t.Errorf("settings: %+v", c)
	}
	if c := changeOf(t, got, "access_rule", "added"); *c.Name != "allow" {
		t.Errorf("rule: %+v", c)
	}
	ord := hasPath(got, "/access_rule_order")
	if !ord {
		t.Errorf("the changed order must be reported: %v", summaries(got))
	}
}

func TestDiffSingletonAppearsAndDisappears(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	next.Settings = nil
	next.AccessMatrix = nil
	got := Diff(base, &next)
	if c := changeOf(t, got, "settings", "removed"); c.Summary != "settings removed" {
		t.Errorf("settings: %+v", c)
	}
	changeOf(t, got, "access_matrix", "removed")
	back := Diff(&next, base)
	changeOf(t, back, "settings", "added")
}

func TestDiffListsAtMostThreeFieldsInTheSummary(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	f := (*next.Faults)[idFaultNet]
	for _, v := range []**string{&f.Jitter, &f.Loss, &f.Rate, &f.Reorder, &f.Duplicate} {
		s := "1%"
		*v = &s
	}
	j := "20ms"
	f.Jitter = &j
	rate := "1Mbit"
	f.Rate = &rate
	(*next.Faults)[idFaultNet] = f
	got := Diff(base, &next)
	if len(got) != 1 || !strings.Contains(got[0].Summary, "… and 2 more") || len(*got[0].Fields) != 5 {
		t.Fatalf("summary = %v", summaries(got))
	}
}

func TestDiffIsSortedByKind(t *testing.T) {
	got := Diff(nil, stored(t))
	order := []string{}
	for _, c := range got {
		k := string(c.Kind)
		if len(order) == 0 || order[len(order)-1] != k {
			order = append(order, k)
		}
	}
	want := []string{"uplink", "management", "network", "client", "link_peer", "routing", "routing_protocol", "access_matrix", "device", "group", "probe", "access_rule", "fault", "profile", "scenario", "settings"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v", order)
	}
}

func TestDiffShortensLongValues(t *testing.T) {
	base := stored(t)
	next := clone(*base)
	sc := (*next.Scenarios)[idScenario]
	sc.Steps = sc.Steps[:2]
	(*next.Scenarios)[idScenario] = sc
	got := Diff(base, &next)
	if len(got) != 1 || !strings.Contains(got[0].Summary, "…") {
		t.Fatalf("summary = %v", summaries(got))
	}
}
