package compiler

import (
	"strings"
	"testing"

	"os"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	hubID    = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	adminID  = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"
	linkID   = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	clientA  = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	clientB  = "5d6e7f80-9a1b-4c2d-8e3f-4a5b6c7d8e9f"
	pubHub   = "ov3zCc517NpPaIPJUL0t2dYjNw0kOoERebHfxWS6YW0="
	pubAdmin = "f0hqF2PfgXJaiBxECr4K5bNLUJAZEoJ2VRZc0WB2qhQ="
	pubLink  = "miaYWhpFycB9c97CyvAQIRquPycwnSZeInykQdnr/Qk="
)

var wgKeys = map[string]string{hubID: pubHub, adminID: pubAdmin, linkID: pubLink}

func loadWG(t *testing.T) *model.Configuration {
	t.Helper()
	raw, err := os.ReadFile("testdata/wireguard.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	// a stored configuration names objects by UUID only
	cfg, errs := domain.Normalize(cfg)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return cfg
}

func compileWG(t *testing.T, mod func(*model.Configuration, *Input)) *Target {
	t.Helper()
	cfg := loadWG(t)
	in := Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 2, Seq: 5}, Keys: wgKeys}
	if mod != nil {
		mod(cfg, &in)
	}
	return Compile(in)
}

func wgByName(t *testing.T, tg *Target, name string) WGInterface {
	t.Helper()
	for _, w := range tg.WireGuard {
		if w.NetworkName == name {
			return w
		}
	}
	t.Fatalf("no WireGuard network %s in %+v", name, tg.WireGuard)
	return WGInterface{}
}

func TestGoldenWireGuard(t *testing.T) {
	tg := compileWG(t, nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	golden(t, "wireguard", tg)
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := executorCheck(tx); err != nil {
		t.Fatal(err)
	}
}

func TestWireGuardInterfaces(t *testing.T) {
	tg := compileWG(t, nil)
	hub := wgByName(t, tg, "lab-hub")
	if hub.Name != "wg-lab-hub" || hub.Kind != "hub" || hub.Role != "test" || hub.MTU != 1420 || hub.ListenPort != 51820 || hub.KeyRef != hubID || hub.PublicKey != pubHub || !hub.NAT {
		t.Errorf("%+v", hub)
	}
	if hub.Address.String() != "10.99.0.1/24" {
		t.Errorf("address %v", hub.Address)
	}
	// the disabled client is not on the interface; the enabled one has its tunnel address and the network behind it
	if len(hub.Peers) != 1 {
		t.Fatalf("%d peers", len(hub.Peers))
	}
	p := hub.Peers[0]
	if p.ID != clientA || p.Name != "rA" || strings.Join(p.AllowedIPs, ",") != "10.99.0.2/32,10.50.0.0/24" || p.Keepalive != 0 || p.Endpoint != "" || p.PresharedKeyRef != clientA {
		t.Errorf("%+v", p)
	}
	link := wgByName(t, tg, "site-b")
	if link.Kind != "link" || link.MTU != 1380 || len(link.Peers) != 1 {
		t.Fatalf("%+v", link)
	}
	lp := link.Peers[0]
	if strings.Join(lp.AllowedIPs, ",") != "0.0.0.0/0" || lp.Endpoint != "203.0.113.40:51821" || lp.Keepalive != DefaultKeepalive || strings.Join(lp.Routes, ",") != "10.60.0.0/24" {
		t.Errorf("a link carries 0.0.0.0/0 and the routing table decides; the gateway initiates and keeps it open: %+v", lp)
	}
	if adm := wgByName(t, tg, "admin"); adm.Role != "management" || len(adm.Peers) != 0 {
		t.Errorf("%+v", adm)
	}
	if len(tg.Interfaces) != 7 || !contains(tg.Interfaces, "wg-lab-hub") || !contains(tg.Interfaces, "wg-site-b") || !contains(tg.Interfaces, "wg-admin") {
		t.Errorf("interfaces %v", tg.Interfaces)
	}
	if contains(tg.Offloads, "wg-lab-hub") {
		t.Error("a WireGuard interface has no offloads to switch off")
	}
	var ra []string
	for _, s := range tg.Sysctls {
		if s.Name == "accept_ra" {
			ra = append(ra, s.Dev)
		}
	}
	if !contains(ra, "wg-lab-hub") || !contains(ra, "wg-site-b") {
		t.Errorf("router advertisements stay off on WireGuard interfaces: %v", ra)
	}
	if !contains(tg.DockerUser, "wg-lab-hub") || contains(tg.DockerUser, "wg0") {
		t.Errorf("DOCKER-USER %v", tg.DockerUser)
	}
}

func TestWireGuardRoutesAndRules(t *testing.T) {
	tg := compileWG(t, nil)
	have := map[string]bool{}
	for _, r := range tg.Routes {
		have[r.Dst+" dev "+r.Dev+" via "+r.Via] = true
		if r.Table != PolicyTable {
			t.Errorf("%+v", r)
		}
	}
	for _, want := range []string{
		"10.99.0.0/24 dev wg-lab-hub via ", // the connected route of the hub
		"10.50.0.0/24 dev wg-lab-hub via ", // the network behind the client
		"10.255.0.0/31 dev wg-site-b via ", // the transfer net of the link
		"10.60.0.0/24 dev wg-site-b via ",  // the static route via the link
		"10.98.0.0/24 dev wg-admin via ",
	} {
		if !have[want] {
			t.Errorf("no route %q in %v", want, have)
		}
	}
	// the network behind a disabled client is not routed
	var iifs []string
	for _, r := range tg.Rules {
		iifs = append(iifs, r.Iif)
		if r.Priority != PolicyRulePriority {
			t.Errorf("%+v", r)
		}
	}
	for _, want := range []string{"br-iot", "wg-lab-hub", "wg-site-b", "wg-admin"} {
		if !contains(iifs, want) {
			t.Errorf("no policy rule for %s: %v", want, iifs)
		}
	}
}

func TestSetsFollowTheRolesOfTheNetworks(t *testing.T) {
	tg := compileWG(t, nil)
	elems := map[string][]string{}
	for _, s := range tg.Nft.Sets {
		for _, base := range []string{"ifs_lan", "ifs_test", "ifs_cg", "ifs_wg", "mgmt_src"} {
			if strings.HasPrefix(s.Name, base+"_") {
				elems[base] = s.Elements
			}
		}
	}
	want := map[string]string{
		"ifs_lan":  "br-iot",
		"ifs_test": "br-iot,wg-lab-hub,wg-site-b", // the management hub is not untrusted
		"ifs_cg":   "br-iot,wg-admin,wg-lab-hub,wg-site-b",
		"ifs_wg":   "wg-admin,wg-lab-hub,wg-site-b",
		"mgmt_src": "10.98.0.0/24,192.168.56.0/24", // the tunnel subnet of the management hub reaches the control plane
	}
	for k, w := range want {
		if got := strings.Join(elems[k], ","); got != w {
			t.Errorf("%s: %q, want %q", k, got, w)
		}
	}
	if len(tg.Management.Sources) != 2 {
		t.Errorf("%v", tg.Management.Sources)
	}
	// without WireGuard no WireGuard set and no clamp chain exist
	plain := compileBasic(t, nil)
	for _, s := range plain.Nft.Sets {
		if strings.HasPrefix(s.Name, "ifs_wg") {
			t.Errorf("%s", s.Name)
		}
	}
	for _, c := range plain.Nft.Chains {
		if c.Name == "mss" {
			t.Error("a clamp chain without WireGuard")
		}
	}
}

func TestTheClientsReachableListBecomesAccessEntriesAfterTheExplicitOnes(t *testing.T) {
	tg := compileWG(t, nil)
	fw := chainOf(t, tg, "forward")
	var rules []string
	for _, r := range fw.Rules {
		s := js(r.Expr)
		if strings.Contains(s, `"wg-lab-hub"`) || (strings.Contains(s, `"br-iot"`) && strings.Contains(s, `"oifname"`) && !sameBridge(s)) {
			rules = append(rules, s)
		}
	}
	// explicit: IoT -> lab-hub first; implicit: rA -> IoT, rA -> uplink
	if len(rules) < 3 {
		t.Fatalf("%d rules:\n%s", len(rules), strings.Join(rules, "\n"))
	}
	if !strings.Contains(rules[0], `"iifname"`) || !strings.Contains(rules[0], `"right":"br-iot"`) || !strings.Contains(rules[0], `"right":"wg-lab-hub"`) {
		t.Errorf("the explicit entry comes first: %s", rules[0])
	}
	// the client endpoint matches its tunnel address and the network behind it
	var toIoT, toUplink string
	for _, r := range rules[1:] {
		if strings.Contains(r, `"10.99.0.2"`) && strings.Contains(r, `"10.50.0.0"`) && strings.Contains(r, `"right":"br-iot"`) {
			toIoT = r
		}
		if strings.Contains(r, `"10.99.0.2"`) && strings.Contains(r, `"right":"wan0"`) {
			toUplink = r
		}
	}
	if toIoT == "" || toUplink == "" {
		t.Errorf("implicit entries missing:\n%s", strings.Join(rules, "\n"))
	}
	for _, r := range []string{toIoT, toUplink} {
		if !strings.Contains(r, `"accept"`) {
			t.Errorf("an implicit entry allows: %s", r)
		}
	}
	// nothing allows the reverse (IoT -> client networks) beyond the explicit hub entry, and there is no entry for rB
	for _, r := range rules {
		if strings.Contains(r, "10.99.0.3") {
			t.Errorf("the disabled client has an entry: %s", r)
		}
	}
}

func TestAnExplicitDenyBeatsTheImplicitAllow(t *testing.T) {
	tg := compileWG(t, func(cfg *model.Configuration, in *Input) {
		c := clientA
		cfg.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{
			matrixEntry(model.MatrixEndpoint{Client: &c}, lanEP("0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"), model.MatrixEntryPolicyDeny),
		}}
	})
	var deny, allow = -1, -1
	for i, r := range chainOf(t, tg, "forward").Rules {
		s := js(r.Expr)
		if strings.Contains(s, `"10.99.0.2"`) && strings.Contains(s, `"right":"br-iot"`) {
			if strings.Contains(s, `"drop"`) {
				deny = i
			} else {
				allow = i
			}
		}
	}
	if deny < 0 || allow < 0 || deny > allow {
		t.Errorf("deny %d, allow %d: first match wins", deny, allow)
	}
}

func TestMasqueradeTowardsTheUplinkOnlyAndForTheNetworksBehindClients(t *testing.T) {
	tg := compileWG(t, nil)
	post := chainOf(t, tg, "postrouting")
	var hub, link string
	for _, r := range post.Rules {
		s := js(r.Expr)
		if !strings.Contains(s, `"oifname"`) || !strings.Contains(s, `"wan0"`) || !strings.Contains(s, "masquerade") {
			t.Errorf("a NAT rule that is not masquerade towards the uplink: %s", s)
		}
		if strings.Contains(s, `"10.99.0.0"`) {
			hub = s
		}
		if strings.Contains(s, `"wg-site-b"`) {
			link = s
		}
	}
	if hub == "" || !strings.Contains(hub, `"10.50.0.0"`) {
		t.Errorf("the hub is masqueraded together with the network behind its client: %s", hub)
	}
	// a link is routed, not addressed from a fixed prefix: everything coming in through it is
	// masqueraded, whatever route brought it there (a static route, or one a routing protocol
	// learned), so the match is by interface, not by source prefix.
	if link == "" || !strings.Contains(link, `"iifname"`) || strings.Contains(link, `"saddr"`) {
		t.Errorf("a link masquerades by interface, not by source prefix: %s", link)
	}
	// NAT off
	off := compileWG(t, func(cfg *model.Configuration, in *Input) {
		n := (*cfg.Networks)[hubID]
		wg, _ := n.AsWireGuardNetwork()
		f := false
		wg.Nat = &f
		_ = n.FromWireGuardNetwork(wg)
		(*cfg.Networks)[hubID] = n
	})
	for _, r := range chainOf(t, off, "postrouting").Rules {
		if strings.Contains(js(r.Expr), `"10.99.0.0"`) {
			t.Error("nat: false must switch masquerade off")
		}
	}
}

func TestTheMSSIsClampedOnWireGuardInterfaces(t *testing.T) {
	mss := chainOf(t, compileWG(t, nil), "mss")
	if mss.Base.Hook != "forward" || mss.Base.Prio != -150 || len(mss.Rules) != 2 {
		t.Fatalf("%+v", mss)
	}
	var in, out bool
	for _, r := range mss.Rules {
		s := js(r.Expr)
		if !strings.Contains(s, "ifs_wg") || !strings.Contains(s, `"maxseg"`) || !strings.Contains(s, `"mtu"`) || !strings.Contains(s, `"syn"`) {
			t.Errorf("%s", s)
		}
		in = in || strings.Contains(s, `"iifname"`)
		out = out || strings.Contains(s, `"oifname"`)
	}
	if !in || !out {
		t.Error("both directions need the clamp: the SYN and the SYN-ACK")
	}
}

func TestAManagementHubReachesTheControlPlaneAndATestHubDoesNot(t *testing.T) {
	tg := compileWG(t, nil)
	var testSet, mgmtSrc string
	for _, s := range tg.Nft.Sets {
		if strings.HasPrefix(s.Name, "ifs_test") {
			testSet = s.Name
		}
		if strings.HasPrefix(s.Name, "mgmt_src") {
			mgmtSrc = s.Name
		}
	}
	in := chainOf(t, tg, "input")
	var anti Rule
	for _, r := range in.Rules {
		if s := js(r.Expr); strings.Contains(s, mgmtSrc) && strings.Contains(s, `"accept"`) {
			anti = r
		}
	}
	s := js(anti.Expr)
	if !strings.Contains(s, `"op":"!="`) || !strings.Contains(s, testSet) {
		t.Errorf("the control plane is open for the management sources unless they come in on a test interface: %s", s)
	}
}

func TestWireGuardKeyProblems(t *testing.T) {
	missing := compileWG(t, func(cfg *model.Configuration, in *Input) { in.Keys = map[string]string{hubID: pubHub} })
	var n int
	for _, p := range missing.Errors() {
		if p.Code == CodeWireGuardKey {
			n++
		}
	}
	if n != 2 {
		t.Errorf("a network without an interface key cannot be compiled: %+v", missing.Problems)
	}
	noClientKey := compileWG(t, func(cfg *model.Configuration, in *Input) {
		n := (*cfg.Networks)[hubID]
		wg, _ := n.AsWireGuardNetwork()
		c := (*wg.Clients)[clientA]
		c.Key.PublicKey = nil
		(*wg.Clients)[clientA] = c
		_ = n.FromWireGuardNetwork(wg)
		(*cfg.Networks)[hubID] = n
	})
	if !noClientKey.HasErrors() || !strings.Contains(noClientKey.Errors()[0].Message, "has no public key") {
		t.Errorf("%+v", noClientKey.Problems)
	}
}

func TestADisabledLinkPeerIsNotOnTheInterface(t *testing.T) {
	tg := compileWG(t, func(cfg *model.Configuration, in *Input) {
		n := (*cfg.Networks)[linkID]
		wg, _ := n.AsWireGuardNetwork()
		f := false
		wg.Peer.Enabled = &f
		_ = n.FromWireGuardNetwork(wg)
		(*cfg.Networks)[linkID] = n
	})
	if l := wgByName(t, tg, "site-b"); len(l.Peers) != 0 {
		t.Errorf("%+v", l.Peers)
	}
}

func TestTheTargetHoldsNoPrivateKeys(t *testing.T) {
	tg := compileWG(t, nil)
	b := js(tg)
	// the public keys are in it, as the compiler is given them; everything else is a reference
	if !strings.Contains(b, pubHub) {
		t.Error("the interface's public key is part of the target")
	}
	if strings.Contains(strings.ToLower(b), "private") {
		t.Error("the word private appears in the target")
	}
}

func TestWireGuardInterfaceNames(t *testing.T) {
	used := map[string]bool{}
	for in, want := range map[string]string{"Site B": "wg-site-b", "lab hub": "wg-lab-hub", "a-very-long-network-name": "wg-a-very-long-"} {
		if got := wgName("0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", in, used); got != want || len(got) > 15 {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	a := wgName("11111111-0000-4000-8000-000000000000", "X", used)
	b := wgName("22222222-0000-4000-8000-000000000000", "x", used)
	if a == b {
		t.Errorf("both got %q", a)
	}
}

func TestRepliesFromTheUplinkToNetworksBehindTunnelsAndRoutersFindTable100(t *testing.T) {
	tg := compileWG(t, nil)
	var to []string
	for _, r := range tg.Rules {
		if r.To != "" {
			to = append(to, r.To)
			if r.Iif != "" || r.Table != PolicyTable || r.Priority != PolicyRulePriority {
				t.Errorf("%+v", r)
			}
		}
	}
	if strings.Join(to, ",") != "10.50.0.0/24,10.60.0.0/24" {
		t.Errorf("destination rules %v: the network behind the client and the link's static route", to)
	}
}

func TestADeviceUnderTestCannotReachTheManagementNetworkBehindTheUplinkEvenWithAFullTunnel(t *testing.T) {
	tg := compileWG(t, func(cfg *model.Configuration, in *Input) {
		cfg.Management.Interface = model.InterfaceRef{Name: ptr("wan0")}
	})
	guard, implicit := -1, -1
	for i, r := range chainOf(t, tg, "forward").Rules {
		s := js(r.Expr)
		if strings.Contains(s, "mgmt_src") && strings.Contains(s, "ifs_test") && strings.Contains(s, "drop") {
			guard = i
		}
		if strings.Contains(s, `"10.99.0.2"`) && strings.Contains(s, `"right":"wan0"`) && strings.Contains(s, "accept") {
			implicit = i
		}
	}
	if guard < 0 || implicit < 0 || guard > implicit {
		t.Fatalf("guard %d, implicit client -> uplink %d: the guard comes first", guard, implicit)
	}
}

func TestTheMSSClampIsCounted(t *testing.T) {
	tg := compileWG(t, nil)
	if !contains(tg.Nft.Counters, "mss_clamp") {
		t.Errorf("counters %v", tg.Nft.Counters)
	}
	if contains(compileBasic(t, nil).Nft.Counters, "mss_clamp") {
		t.Error("a counter without a clamp")
	}
}
