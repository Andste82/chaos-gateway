package domain

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func accessWorld(t *testing.T, mod func(*model.Configuration)) *World {
	t.Helper()
	cfg, errs := Normalize(exampleConfiguration(t))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if mod != nil {
		mod(cfg)
	}
	w, err := NewWorld(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestTheAccessVerdictFollowsTheMatrixAndItsDefault(t *testing.T) {
	w := accessWorld(t, nil)
	facts := AccessFacts{Gateway: []netip.Addr{netip.MustParseAddr("10.10.0.1"), netip.MustParseAddr("10.99.0.1")},
		Management: []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24")}}
	a := netip.MustParseAddr
	for _, c := range []struct {
		name, src, dst, proto string
		port                  int
		want, layer           string
	}{
		{"a test network reaches the uplink by default", "10.10.0.5", "198.51.100.7", "tcp", 443, "allow", AccessMatrix},
		{"an entry allows the hub's network to reach IoT", "10.99.0.9", "10.10.0.9", "", 0, "allow", AccessMatrix},
		{"a client of the hub is part of the hub's network", "10.99.0.2", "10.10.0.9", "", 0, "allow", AccessMatrix},
		{"the other direction has no entry", "10.10.0.5", "10.99.0.9", "", 0, "drop", AccessMatrix},
		{"the link site-b reaches IoT", "10.255.0.1", "10.10.0.9", "", 0, "allow", AccessMatrix},
		{"the uplink does not reach a test network", "198.51.100.7", "10.10.0.5", "", 0, "drop", AccessMatrix},
		{"DNS to the gateway is answered", "10.10.0.5", "10.10.0.1", "udp", 53, "allow", AccessGatewayProtection},
		{"ICMP echo to the gateway is answered", "10.10.0.5", "10.10.0.1", "icmp", 0, "allow", AccessGatewayProtection},
		{"anything else to the gateway is dropped", "10.10.0.5", "10.10.0.1", "tcp", 22, "drop", AccessGatewayProtection},
		{"the management network reaches the control plane", "192.168.56.9", "10.10.0.1", "tcp", 22, "allow", AccessGatewayProtection},
	} {
		got := w.AccessVerdict(a(c.src), a(c.dst), c.proto, c.port, facts)
		if got.Verdict != c.want || got.Layer != c.layer || got.Reason == "" {
			t.Errorf("%s: %+v, want %s in %s", c.name, got, c.want, c.layer)
		}
	}
}

func TestAnExplicitEntryBeatsTheDefaultAndTheMoreSpecificEndpointComesFirst(t *testing.T) {
	hubClient := "lab-rA"
	deny := func(c *model.Configuration) {
		iot, _ := Normalize(exampleConfiguration(t))
		_ = iot
		idx, _ := BuildIndex(c)
		iotID, _ := idx.Resolve(KindNetwork, "IoT")
		clientID, _ := idx.Resolve(KindClient, hubClient)
		up := model.MatrixEndpointUplink(true)
		entries := append(deref(c.AccessMatrix.Entries),
			model.MatrixEntry{From: model.MatrixEndpoint{Network: &iotID}, To: model.MatrixEndpoint{Uplink: &up}, Policy: model.MatrixEntryPolicyDeny},
			model.MatrixEntry{From: model.MatrixEndpoint{Client: &clientID}, To: model.MatrixEndpoint{Uplink: &up}, Policy: model.MatrixEntryPolicyAllow})
		c.AccessMatrix.Entries = &entries
	}
	w := accessWorld(t, deny)
	a := netip.MustParseAddr
	// IoT to the uplink is denied now, the client of the hub (a more specific endpoint than the hub's
	// network) is allowed
	if got := w.AccessVerdict(a("10.10.0.5"), a("198.51.100.7"), "", 0, AccessFacts{}); got.Verdict != "drop" || got.Layer != AccessMatrix {
		t.Errorf("%+v", got)
	}
	if got := w.AccessVerdict(a("10.99.0.2"), a("198.51.100.7"), "", 0, AccessFacts{}); got.Verdict != "allow" {
		t.Errorf("%+v", got)
	}
}

func TestDevicesUnderTestDoNotReachTheManagementNetworkBehindTheUplink(t *testing.T) {
	w := accessWorld(t, nil)
	a := netip.MustParseAddr
	mgmt := []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24")}
	// with a management interface of its own the guard says why too (P31-M35a-10)
	if got := w.AccessVerdict(a("10.10.0.5"), a("192.168.56.9"), "", 0, AccessFacts{Management: mgmt}); got.Verdict != "drop" || got.Layer != AccessMatrix || !strings.Contains(got.Reason, "do not reach the management network") {
		t.Errorf("no entry: %+v", got)
	}
	// behind the uplink the guard says why
	got := w.AccessVerdict(a("10.10.0.5"), a("192.168.56.9"), "", 0, AccessFacts{Management: mgmt, TwoPort: true})
	if got.Verdict != "drop" || got.Layer != AccessMatrix || got.Reason == "" {
		t.Errorf("%+v", got)
	}
}

func TestTheNetworkOfAnAddressIsTheMostSpecificPrefix(t *testing.T) {
	w := accessWorld(t, nil)
	for ip, want := range map[string]string{"10.10.0.5": "IoT", "10.99.0.7": "lab-hub", "198.51.100.1": ""} {
		id, ok := w.NetworkOf(netip.MustParseAddr(ip))
		name := ""
		if ok {
			name = w.Index.Networks[id].Name
		}
		if name != want {
			t.Errorf("%s is in %q, want %q", ip, name, want)
		}
	}
}

// The access rules come first, in the order of the packet path: the control plane in front of them, then the
// rules, then the gateway's protection or the matrix. An allow rule is an exception to the matrix and
// also opens the gateway's closed ports for its selector, except the UI port (decided on 2026-10-11).
func TestTheAccessDecisionPutsTheRulesBeforeTheMatrixAndKeepsTheControlPlane(t *testing.T) {
	iot := ""
	rule := func(name string, proto string, ports []int, action model.AccessAction, dst *model.Destination) model.AccessRule {
		r := model.AccessRule{Name: &name, Source: model.Scope{Network: &iot}, Action: action, Destination: dst}
		if proto != "" {
			p := model.Protocol(proto)
			r.Protocol = &p
		}
		if ports != nil {
			r.Ports = &ports
		}
		return r
	}
	uuids := []string{"a1000000-0000-4000-8000-000000000001", "a1000000-0000-4000-8000-000000000002", "a1000000-0000-4000-8000-000000000003"}
	cidr := "198.51.100.0/24"
	w := accessWorld(t, func(c *model.Configuration) {
		idx, _ := BuildIndex(c)
		iot, _ = idx.Resolve(KindNetwork, "IoT")
		rules := map[string]model.AccessRule{
			uuids[0]: rule("no-dns", "udp", []int{53}, model.AccessActionDrop, nil),
			uuids[1]: rule("block-net", "tcp", nil, model.AccessActionReject, &model.Destination{Cidr: &cidr}),
			uuids[2]: rule("everything", "", nil, model.AccessActionAllow, nil),
		}
		var order []uuid.UUID
		for _, id := range uuids {
			order = append(order, uuid.MustParse(id))
		}
		c.AccessRules, c.AccessRuleOrder = &rules, &order
	})
	a := netip.MustParseAddr
	facts := AccessFacts{Gateway: []netip.Addr{a("10.10.0.1"), a("10.99.0.1")}, Management: []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24")}, UIPort: 443}
	subject := func(ip string) Subject {
		id := Subject{IP: a(ip)}
		if n, ok := w.NetworkOf(a(ip)); ok {
			id.Network = n
		}
		return id
	}
	for _, c := range []struct {
		name, src, dst, proto string
		port                  int
		verdict, layer, rule  string
	}{
		{"the first rule that matches decides: DNS to the proxy", "10.10.0.5", "10.10.0.1", "udp", 53, "drop", AccessConfigRule, uuids[0]},
		{"the same rule covers queries to the service namespace", "10.10.0.5", "169.254.100.2", "udp", 53, "drop", AccessConfigRule, uuids[0]},
		{"and queries to any other resolver", "10.10.0.5", "198.51.100.9", "udp", 53, "drop", AccessConfigRule, uuids[0]},
		{"a reject rule on a destination", "10.10.0.5", "198.51.100.9", "tcp", 443, "reject", AccessConfigRule, uuids[1]},
		{"an allow rule is an exception to the matrix (IoT does not reach 10.99.0.9)", "10.10.0.5", "10.99.0.9", "tcp", 80, "allow", AccessConfigRule, uuids[2]},
		{"another network is not selected, the matrix decides", "10.99.0.9", "10.10.0.9", "tcp", 80, "allow", AccessMatrix, ""},
		{"an allow rule opens a port of the gateway that its protection closes", "10.10.0.5", "10.10.0.1", "tcp", 22, "allow", AccessConfigRule, uuids[2]},
		{"and a port nothing listens on is opened the same way", "10.10.0.5", "10.10.0.1", "udp", 5353, "allow", AccessConfigRule, uuids[2]},
		{"the UI port is closed to a test network, whatever a rule says", "10.10.0.5", "10.10.0.1", "tcp", 443, "drop", AccessGatewayProtection, ""},
		{"the management sources reach the UI", "192.168.56.9", "10.10.0.1", "tcp", 443, "allow", AccessGatewayProtection, ""},
		{"the management sources reach SSH", "192.168.56.9", "10.10.0.1", "tcp", 22, "allow", AccessGatewayProtection, ""},
	} {
		got := w.AccessDecision(Query{Source: subject(c.src), DestIP: a(c.dst), Protocol: c.proto, Port: c.port}, facts)
		if got.Verdict != c.verdict || got.Layer != c.layer || got.Rule != c.rule || got.Reason == "" {
			t.Errorf("%s: %+v, want %s in %s by %q", c.name, got, c.verdict, c.layer, c.rule)
		}
	}
	// the anti-lockout rule names exactly SSH and the UI port; any other port of the gateway is not
	// guaranteed to a management source, and the reason says so
	for _, c := range []struct {
		port        int
		antiLockout bool
	}{{22, true}, {443, true}, {8080, false}} {
		got := w.AccessDecision(Query{Source: subject("192.168.56.9"), DestIP: a("10.10.0.1"), Protocol: "tcp", Port: c.port}, facts)
		if got.Verdict != "allow" || got.Layer != AccessGatewayProtection || strings.Contains(got.Reason, "anti-lockout") != c.antiLockout {
			t.Errorf("management source, tcp %d: %+v", c.port, got)
		}
	}
	// an overlay rule stands before the configured ones
	ovID := uuid.MustParse("0aaaaaaa-0000-4000-8000-000000000001")
	ov := model.Overlay{Id: ovID, Kind: model.OverlayKindRule, Target: &model.Scope{Network: &iot},
		Rule: &model.AccessRuleBody{Action: model.AccessActionReset, Protocol: ptrProtocol("tcp"), Ports: &[]int{8883}}, UpdatedAt: time.Now()}
	cfg := w.Config
	w2, err := NewWorld(cfg, []model.Overlay{ov})
	if err != nil {
		t.Fatal(err)
	}
	got := w2.AccessDecision(Query{Source: subject("10.10.0.5"), DestIP: a("198.51.100.9"), Protocol: "tcp", Port: 8883}, facts)
	if got.Verdict != "reset" || got.Layer != AccessOverlayRule || got.Rule != ovID.String() {
		t.Errorf("%+v", got)
	}
}

func ptrProtocol(p string) *model.Protocol { v := model.Protocol(p); return &v }

// The explanation names the ICMP type a reject answers with: the rule's own, port unreachable by default.
func TestTheExplanationOfARejectNamesItsIcmpType(t *testing.T) {
	a := netip.MustParseAddr
	for _, c := range []struct {
		with, want string
	}{{"", "port unreachable"}, {"port_unreachable", "port unreachable"}, {"host_unreachable", "host unreachable"},
		{"admin_prohibited", "administratively prohibited"}, {"no_route", "no route"}} {
		iot := ""
		id := "a1000000-0000-4000-8000-000000000001"
		w := accessWorld(t, func(cfg *model.Configuration) {
			idx, _ := BuildIndex(cfg)
			iot, _ = idx.Resolve(KindNetwork, "IoT")
			r := model.AccessRule{Source: model.Scope{Network: &iot}, Action: model.AccessActionReject}
			if c.with != "" {
				r.RejectWith = ptr(model.RejectType(c.with))
			}
			cfg.AccessRules = &map[string]model.AccessRule{id: r}
			cfg.AccessRuleOrder = &[]uuid.UUID{uuid.MustParse(id)}
		})
		got := w.AccessDecision(Query{Source: Subject{IP: a("10.10.0.5"), Network: iot}, DestIP: a("198.51.100.9"), Protocol: "tcp", Port: 443}, AccessFacts{})
		if got.Verdict != "reject" || !strings.Contains(got.Reason, "ICMP "+c.want) {
			t.Errorf("reject_with %q: %+v, want the type %q", c.with, got, c.want)
		}
	}
}

// "Network IoT" includes the networks behind IoT's downstream routes (decided by the maintainer on
// 2026-10-11): a host at 10.30.0.5 behind IoT's router is selected by a rule and by a fault for
// the network IoT, and by a destination "network IoT", without being a configured device.
func TestANetworkIncludesTheNetworksBehindItsDownstreamRoutes(t *testing.T) {
	tw := newTestWorld(t, false)
	withIoTRouter(t, tw.cfg)
	tw.overlay("target: {network: IoT}\nfault: {latency: 120ms}", 0)
	rule := tw.overlay("target: {network: IoT}\nrule: {protocol: tcp, ports: [8883], action: drop}", 0)
	toIoT := tw.overlay("target: {global: true}\nfault: {destination: {network: IoT}, loss: 4%}", 0)
	w := tw.world()
	a := netip.MustParseAddr
	host := Subject{IP: a("10.30.0.5")} // no device owns it

	if id, ok := w.NetworkOf(a("10.30.0.5")); !ok || id != idIoT {
		t.Errorf("NetworkOf(10.30.0.5) = %q, %v, want IoT", id, ok)
	}
	if got := prefixList(w.NetworkPrefixes(idIoT)); got != "10.10.0.0/24 10.30.0.0/24" {
		t.Errorf("the prefixes of IoT: %s", got)
	}
	// the source scope in the fault tables and in the rules
	if res := w.Resolve(Query{Source: host, DestIP: a("203.0.113.10"), Protocol: "tcp", Port: 443}); Winner(res, FamilyImpairment) == nil || latencyOf(*Winner(res, FamilyImpairment)) != "120ms" {
		t.Errorf("the fault of network IoT does not select a host behind its router: %+v", res)
	}
	if r := w.ResolveAccess(Query{Source: host, DestIP: a("203.0.113.10"), Protocol: "tcp", Port: 8883}); !r.Matched || r.RuleID != rule.Id.String() {
		t.Errorf("the rule of network IoT does not select a host behind its router: %+v", r)
	}
	// and as a destination: a fault for traffic to network IoT, and the destination "uplink" excludes it
	toHost := Query{Source: Subject{IP: a("10.20.0.7")}, DestIP: a("10.30.0.5"), Protocol: "udp", Port: 53}
	if win := Winner(w.Resolve(toHost), FamilyImpairment); win == nil || win.ID != toIoT.Id.String() {
		t.Errorf("a destination network IoT does not select 10.30.0.5: %+v", win)
	}
	if w.viaUplink(a("10.30.0.5")) {
		t.Error("10.30.0.5 is reached through IoT's router, not through the uplink")
	}
	// the sources of a scope for the kernel: the subnet and the route
	if got := prefixList(mustPrefixes(w, model.Scope{Network: ptrTo(idIoT)})); got != "10.10.0.0/24 10.30.0.0/24" {
		t.Errorf("the source addresses of network IoT: %s", got)
	}
	// explain: the rule decides for the host behind the router, not the matrix
	ex := w.AccessDecision(Query{Source: host, DestIP: a("203.0.113.10"), Protocol: "tcp", Port: 8883}, AccessFacts{})
	if ex.Verdict != "drop" || ex.Layer != AccessOverlayRule || ex.Rule != rule.Id.String() {
		t.Errorf("explain: %+v", ex)
	}
}

// What the gateway routes back into the network it came from (a device of IoT to a host behind IoT's downstream
// router) leaves through the bridge it came in on: the kernel accepts it in front of the management guard, the access
// rules and the matrix (`iifname br-x oifname br-x accept`), so the verdict and explain say allow, where the matrix's
// default would have said drop (P5-AUDIT-03).
func TestTrafficRoutedBackIntoItsOwnNetworkIsAllowedBeforeTheRulesAndTheMatrix(t *testing.T) {
	tw := newTestWorld(t, false)
	withIoTRouter(t, tw.cfg)
	rule := tw.overlay("target: {network: IoT}\nrule: {protocol: tcp, ports: [8883], action: drop}", 0)
	w := tw.world()
	a := netip.MustParseAddr
	facts := AccessFacts{Gateway: []netip.Addr{a("10.10.0.1")}, Management: []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24")}}
	for _, c := range []struct{ name, src, dst string }{
		{"a device of IoT to a host behind IoT's router", "10.10.0.31", "10.30.0.5"},
		{"a host behind IoT's router to a device of IoT", "10.30.0.5", "10.10.0.31"},
		{"two hosts of IoT's subnet (switched, seen with br_netfilter)", "10.10.0.31", "10.10.0.40"},
	} {
		if got := w.AccessVerdict(a(c.src), a(c.dst), "tcp", 443, facts); got.Verdict != "allow" || got.Layer != AccessMatrix || !strings.Contains(got.Reason, "same local test network") {
			t.Errorf("%s: %+v", c.name, got)
		}
		// the kernel's accept stands in front of the access rules too: the network's drop rule is not asked
		got := w.AccessDecision(Query{Source: Subject{IP: a(c.src), Network: idIoT}, DestIP: a(c.dst), Protocol: "tcp", Port: 8883}, facts)
		if got.Verdict != "allow" || got.Rule != "" {
			t.Errorf("%s, explain: %+v", c.name, got)
		}
	}
	// another network to the host behind IoT's router is still the matrix's (no entry: the default drop), and the rule still
	// decides what leaves IoT for somewhere else
	if got := w.AccessVerdict(a("10.20.0.7"), a("10.30.0.5"), "tcp", 443, facts); got.Verdict != "drop" || !strings.Contains(got.Reason, "the default") {
		t.Errorf("another network: %+v", got)
	}
	if got := w.AccessDecision(Query{Source: Subject{IP: a("10.10.0.31"), Network: idIoT}, DestIP: a("203.0.113.10"), Protocol: "tcp", Port: 8883}, facts); got.Verdict != "drop" || got.Rule != rule.Id.String() {
		t.Errorf("the uplink: %+v", got)
	}
}

func mustPrefixes(w *World, sc model.Scope) []netip.Prefix {
	p, _ := w.ScopePrefixes(sc, Identity{})
	return p
}
