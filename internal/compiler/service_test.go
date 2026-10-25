package compiler

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func withService(t *testing.T) *Target {
	t.Helper()
	return compileWG(t, func(_ *model.Configuration, in *Input) { in.ServiceNS = "cgsvc" })
}

func TestNoServiceNamespaceWithoutAName(t *testing.T) {
	tg := compileWG(t, nil)
	if tg.Service != nil {
		t.Fatalf("%+v", tg.Service)
	}
	for _, c := range tg.Nft.Chains {
		if c.Name == "prerouting" {
			t.Error("a DNS redirect without a service namespace")
		}
	}
	for _, r := range tg.Routes {
		if r.Table == ServiceTable {
			t.Errorf("route in table %d without a service namespace", ServiceTable)
		}
	}
}

func TestTheServiceNamespaceIsRoutedAndFailsClosed(t *testing.T) {
	tg := withService(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if tg.Service == nil || tg.Service.Name != "cgsvc" || tg.Service.HostCIDR.String() != "169.254.100.1/30" || tg.Service.PeerCIDR.String() != "169.254.100.2/30" {
		t.Fatalf("%+v", tg.Service)
	}
	// svc0 belongs to Chaos Gateway, its sysctls are set, offloads are left alone (a veth has none to switch)
	found := false
	for _, n := range tg.Interfaces {
		found = found || n == "svc0"
	}
	if !found {
		t.Errorf("svc0 is not assigned: %v", tg.Interfaces)
	}
	for _, o := range tg.Offloads {
		if o == "svc0" {
			t.Error("offloads of a veth")
		}
	}
	got := map[string]bool{}
	for _, r := range tg.Routes {
		got[routeLine(r)] = true
	}
	for _, want := range []string{
		"100 169.254.100.0/30 dev svc0",
		"102 default via 169.254.100.2 dev svc0 metric 0",
		"102 default prohibit metric 4096",
	} {
		if !got[want] {
			t.Errorf("missing route %q in %v", want, got)
		}
	}
	rules := map[string]bool{}
	for _, r := range tg.Rules {
		rules[ruleLine(r)] = true
	}
	for _, want := range []string{
		"900 fwmark 0x100000/0x100000 lookup 102",
		"1000 iif svc0 lookup 100",
	} {
		if !rules[want] {
			t.Errorf("missing rule %q in %v", want, rules)
		}
	}
	// the fallback has the higher metric: the prohibit route only applies when the route into the
	// namespace is gone
	var viaMetric, prohibitMetric int
	for _, r := range tg.Routes {
		if r.Table == ServiceTable && r.Metric != nil {
			if r.Type == "prohibit" {
				prohibitMetric = *r.Metric
			} else {
				viaMetric = *r.Metric
			}
		}
	}
	if viaMetric >= prohibitMetric {
		t.Errorf("metrics %d and %d", viaMetric, prohibitMetric)
	}

	raw, _ := json.Marshal(tg.Nft)
	text := string(raw)
	// queries to each network's gateway address are redirected, over UDP and TCP, for the bridges
	// and the WireGuard networks
	pre := chainByName(t, tg, "prerouting")
	if pre.Base == nil || pre.Base.Hook != "prerouting" || pre.Base.Type != "nat" {
		t.Fatalf("%+v", pre.Base)
	}
	want := len(tg.Bridges)*2 + len(tg.WireGuard)*2
	if len(pre.Rules) != want || want == 0 {
		t.Errorf("%d redirect rules for %d bridges and %d WireGuard networks", len(pre.Rules), len(tg.Bridges), len(tg.WireGuard))
	}
	for _, r := range pre.Rules {
		b, _ := json.Marshal(r.Expr)
		if !strings.Contains(string(b), `"dnat":{"addr":"169.254.100.2","family":"ip","port":53}`) {
			t.Errorf("not a redirect into the namespace: %s", b)
		}
	}
	for _, b := range tg.Bridges {
		if !strings.Contains(text, b.Address.Addr().String()) {
			t.Errorf("no redirect for %s", b.Address.Addr())
		}
	}
	// the guard stands before every accept of the forward chain, after the established ones
	fwd := chainByName(t, tg, "forward")
	idx := -1
	for i, r := range fwd.Rules {
		b, _ := json.Marshal(r.Expr)
		if strings.Contains(string(b), `169.254.100.0/30`) || strings.Contains(string(b), `"prefix":{"addr":"169.254.100.0","len":30}`) {
			if strings.Contains(string(b), `"!="`) {
				idx = i
				break
			}
		}
	}
	if idx < 0 || idx > 2 {
		t.Errorf("no fail-closed guard at the start of the forward chain (index %d)", idx)
	}
	// the services reach the API's port and nothing else of the gateway
	in := chainByName(t, tg, "input")
	accept, drop := -1, -1
	for i, r := range in.Rules {
		b, _ := json.Marshal(r.Expr)
		s := string(b)
		if strings.Contains(s, `"svc0"`) && strings.Contains(s, "accept") {
			accept = i
		}
		if strings.Contains(s, `"svc0"`) && strings.Contains(s, "drop") {
			drop = i
		}
	}
	if accept < 0 || drop != accept+1 {
		t.Errorf("service input rules: accept %d, drop %d", accept, drop)
	}
	// the services reach the uplink with its address
	post := chainByName(t, tg, "postrouting")
	b, _ := json.Marshal(post.Rules)
	if !strings.Contains(string(b), "169.254.100.0") {
		t.Errorf("no masquerade for the services: %s", b)
	}
}

// What a service opens towards the test networks and the tunnels (P5-M21-02): the forward chain accepts svc0 towards the interfaces
// of the test networks (the bridges and the WireGuard networks of the role test) and never towards the management interface or the
// WireGuard networks of the role management; the namespace's address is masqueraded on the way out like at the uplink.
func TestAServiceReachesTheTestNetworksAndTheTunnelsButNotTheManagementNetwork(t *testing.T) {
	tg := withService(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	var test SetDef
	for _, s := range tg.Nft.Sets {
		if strings.HasPrefix(s.Name, "ifs_test_") {
			test = s
		}
	}
	members := map[string]bool{}
	for _, e := range test.Elements {
		members[e] = true
	}
	if len(tg.Bridges) == 0 || len(tg.WireGuard) == 0 {
		t.Fatalf("the configuration has %d bridges and %d WireGuard networks", len(tg.Bridges), len(tg.WireGuard))
	}
	for _, b := range tg.Bridges {
		if !members[b.Name] {
			t.Errorf("the bridge %s is not a destination of the namespace: %v", b.Name, test.Elements)
		}
	}
	for _, w := range tg.WireGuard {
		if got, want := members[w.Name], w.Role != "management"; got != want {
			t.Errorf("the WireGuard network %s (role %q) is a destination of the namespace: %v, want %v", w.Name, w.Role, got, want)
		}
	}
	if members[tg.Management.Name] || members[tg.Uplink.Name] {
		t.Errorf("the management interface or the uplink is in the set of the test networks: %v", test.Elements)
	}
	fwd := renderRules(chainByName(t, tg, "forward"))
	accept := indexOfLine(fwd, "meta iifname svc0 meta oifname @"+test.Name+" accept")
	up := indexOfLine(fwd, "meta iifname svc0 meta oifname "+tg.Uplink.Name+" accept")
	if accept < 0 || up < 0 {
		t.Fatalf("the accept towards the test networks (%d) or the uplink (%d) is missing:\n%s", accept, up, strings.Join(fwd, "\n"))
	}
	// it stands behind the access rules, the guard of the management network and the matrix, which judge the original connection
	for _, before := range []string{"jump mgmt_guard", "ct state { established, related } accept"} {
		if i := indexOfLine(fwd, before); i < 0 || i > accept {
			t.Errorf("%q (index %d) does not stand before the accept (index %d)", before, i, accept)
		}
	}
	for _, line := range fwd {
		if strings.Contains(line, "meta iifname svc0") && strings.Contains(line, "oifname "+tg.Management.Name) {
			t.Errorf("the namespace is accepted towards the management interface: %s", line)
		}
	}
	// the namespace's address is translated on the way out, towards the uplink and towards the test networks
	post := renderRules(chainByName(t, tg, "postrouting"))
	if indexOfLine(post, "ip saddr 169.254.100.0/30 meta oifname "+tg.Uplink.Name+" masquerade") < 0 {
		t.Errorf("the namespace is not masqueraded towards the uplink:\n%s", strings.Join(post, "\n"))
	}
	if indexOfLine(post, "ip saddr 169.254.100.0/30 meta oifname @"+test.Name+" masquerade") < 0 {
		t.Errorf("the namespace is not masqueraded towards the test networks:\n%s", strings.Join(post, "\n"))
	}
	// no namespace, nothing of it
	plain := compileWG(t, nil)
	for _, line := range append(renderRules(chainByName(t, plain, "forward")), renderRules(chainByName(t, plain, "postrouting"))...) {
		if strings.Contains(line, "svc0") || strings.Contains(line, "169.254.100") {
			t.Errorf("a rule for the service namespace without one: %s", line)
		}
	}
}

func indexOfLine(lines []string, want string) int {
	for i, l := range lines {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

func TestTheServiceNamespaceIsPartOfTheHash(t *testing.T) {
	a, b := compileWG(t, nil), withService(t)
	if a.Hash == b.Hash {
		t.Error("the same hash with and without the namespace")
	}
	c := compileWG(t, func(_ *model.Configuration, in *Input) { in.ServiceNS = "cgsvc"; in.ServiceHolderPID = 4 })
	if c.Hash == b.Hash {
		t.Error("a new holder does not change the hash: the namespace would not be attached again")
	}
}

func chainByName(t *testing.T, tg *Target, name string) Chain {
	t.Helper()
	for _, c := range tg.Nft.Chains {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no chain %s", name)
	return Chain{}
}

func routeLine(r executor.Route) string {
	s := strings.TrimSpace(strings.Join([]string{strconv.Itoa(r.Table), r.Dst}, " "))
	if r.Via != "" {
		s += " via " + r.Via
	}
	if r.Type != "" && r.Type != "unicast" {
		s += " " + r.Type
	}
	if r.Dev != "" {
		s += " dev " + r.Dev
	}
	if r.Metric != nil {
		s += " metric " + strconv.Itoa(*r.Metric)
	}
	return s
}

func ruleLine(r executor.Rule) string {
	s := strconv.Itoa(r.Priority)
	if r.Fwmark != "" {
		s += " fwmark " + r.Fwmark
	}
	if r.Iif != "" {
		s += " iif " + r.Iif
	}
	if r.To != "" {
		s += " to " + r.To
	}
	return s + " lookup " + strconv.Itoa(r.Table)
}

func TestTheServicesAndTheManagementNetworkReachThePortTheAPIListensOn(t *testing.T) {
	// the configuration names no ui_port: the port of the API stands in
	tg := compileWG(t, func(c *model.Configuration, in *Input) {
		in.ServiceNS = "cgsvc"
		in.DefaultUIPort = 8443
		c.Management.UiPort = nil
	})
	if tg.Management.UIPort != 8443 {
		t.Errorf("ui port %d", tg.Management.UIPort)
	}
	raw, _ := json.Marshal(chainByName(t, tg, "input").Rules)
	if !strings.Contains(string(raw), "8443") || strings.Contains(string(raw), `"dport"},"right":443`) {
		t.Errorf("%s", raw)
	}
	// a configured port wins
	port := 9443
	tg = compileWG(t, func(c *model.Configuration, in *Input) { in.DefaultUIPort = 8443; c.Management.UiPort = &port })
	if tg.Management.UIPort != 9443 {
		t.Errorf("ui port %d", tg.Management.UIPort)
	}
}
