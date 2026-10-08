package domain

import (
	"net/netip"
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
	// with a management interface of its own the management network is just another destination
	if got := w.AccessVerdict(a("10.10.0.5"), a("192.168.56.9"), "", 0, AccessFacts{Management: mgmt}); got.Verdict != "drop" {
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
// cannot open the gateway.
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
		{"an allow rule does not open the gateway", "10.10.0.5", "10.10.0.1", "tcp", 22, "drop", AccessGatewayProtection, ""},
		{"the UI port is closed to a test network, whatever a rule says", "10.10.0.5", "10.10.0.1", "tcp", 443, "drop", AccessGatewayProtection, ""},
		{"the management sources reach the control plane", "192.168.56.9", "10.10.0.1", "tcp", 443, "allow", AccessGatewayProtection, ""},
	} {
		got := w.AccessDecision(Query{Source: subject(c.src), DestIP: a(c.dst), Protocol: c.proto, Port: c.port}, facts)
		if got.Verdict != c.verdict || got.Layer != c.layer || got.Rule != c.rule || got.Reason == "" {
			t.Errorf("%s: %+v, want %s in %s by %q", c.name, got, c.verdict, c.layer, c.rule)
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
