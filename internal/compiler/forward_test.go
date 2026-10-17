package compiler

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	fwdWeb   = "f1000000-0000-4000-8000-000000000001"
	fwdRange = "f1000000-0000-4000-8000-000000000002"
	fwdAddr  = "f1000000-0000-4000-8000-000000000003"
	fwdMgmt  = "f1000000-0000-4000-8000-000000000004"
	fwdOff   = "f1000000-0000-4000-8000-000000000005"
)

// addForward adds a port forward (a YAML body) to the configuration of the world.
func (w *faultWorld) addForward(id, body string) {
	w.t.Helper()
	doc, err := domain.ParseDocument([]byte(body), domain.FormatYAML)
	if err != nil {
		w.t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	var f model.PortForward
	if err := json.Unmarshal(raw, &f); err != nil {
		w.t.Fatal(err)
	}
	cfg := *w.cfg
	fwds := map[string]model.PortForward{}
	if cfg.PortForwards != nil {
		for k, v := range *cfg.PortForwards {
			fwds[k] = v
		}
	}
	fwds[id] = f
	cfg.PortForwards = &fwds
	norm, errs := domain.Normalize(&cfg)
	if len(errs) != 0 {
		w.t.Fatalf("normalize: %v", errs)
	}
	if errs := domain.Validate(norm); len(errs) != 0 {
		w.t.Fatalf("validate: %v", errs)
	}
	w.cfg = norm
}

// forwardWorld has four forwards on the uplink of the routed gateway and the management side: a device
// with a port changed, a range shifted one to one, an address, the management side, and a disabled one.
func forwardWorld(t *testing.T) *faultWorld {
	t.Helper()
	w := accessWorld(t)
	w.addForward(fwdWeb, `{name: web, protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}, target_port: 80}`)
	w.addForward(fwdRange, `{name: range, protocol: udp, entry: uplink, external_port: 8000, external_port_end: 8003, target: {device: lab-host}, target_port: 9000}`)
	w.addForward(fwdAddr, `{name: addr, protocol: tcp, entry: uplink, external_port: 2222, target: {address: 10.10.0.77}, target_port: 22}`)
	w.addForward(fwdMgmt, `{name: mgmt, protocol: tcp, entry: management, external_port: 8443, target: {device: esp32-43}, target_port: 443}`)
	w.addForward(fwdOff, `{name: off, protocol: tcp, entry: uplink, external_port: 9999, target: {device: esp32-42}, enabled: false}`)
	return w
}

func forwardOf(t *testing.T, tg *Target, id string) PortForward {
	t.Helper()
	f, ok := tg.Forward(id)
	if !ok {
		t.Fatalf("the forward %s is not compiled: %+v", id, tg.Forwards)
	}
	return *f
}

func forwardMap(t *testing.T, tg *Target) MapDef {
	t.Helper()
	for _, m := range tg.Nft.Maps {
		if m.Name == tg.ForwardMap {
			return m
		}
	}
	t.Fatalf("no DNAT map in %v", tg.Nft.Maps)
	return MapDef{}
}

func elementsOf(m MapDef) []string {
	var out []string
	for _, e := range m.Elements {
		out = append(out, e.Key+" : "+e.Value)
	}
	return out
}

// The DNAT map holds one element per forwarded port: the entry interface, the protocol and the external
// port to the target's address now and the target port, a range shifted one to one. A device target
// sends to the device's address, an address target to the address; a disabled forward has nothing.
func TestTheDNATMapSendsEveryForwardedPortToTheTargetsCurrentAddress(t *testing.T) {
	tg := forwardWorld(t).compile(nil)
	if tg.HasErrors() {
		t.Fatalf("problems: %+v", tg.Problems)
	}
	want := []string{
		"mgmt0 . tcp . 8443 : 10.10.0.43 . 443",
		"wan0 . tcp . 2222 : 10.10.0.77 . 22",
		"wan0 . tcp . 8080 : 10.10.0.42 . 80",
		"wan0 . udp . 8000 : 10.20.0.50 . 9000",
		"wan0 . udp . 8001 : 10.20.0.50 . 9001",
		"wan0 . udp . 8002 : 10.20.0.50 . 9002",
		"wan0 . udp . 8003 : 10.20.0.50 . 9003",
	}
	m := forwardMap(t, tg)
	if got := elementsOf(m); !slices.Equal(got, want) {
		t.Fatalf("the DNAT map:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !reflect.DeepEqual(m.KeyType, []string{"ifname", "inet_proto", "inet_service"}) || m.ValueType != "ipv4_addr . inet_service" || len(m.Flags) != 0 {
		t.Errorf("the map's types: %+v", m)
	}
	if _, ok := tg.Forward(fwdOff); ok {
		t.Error("a disabled forward is compiled")
	}
	for _, id := range []string{fwdWeb, fwdRange, fwdAddr, fwdMgmt} {
		if f := forwardOf(t, tg, id); !f.Active || f.Reason != "" || !f.Target.IsValid() {
			t.Errorf("%s: %+v", id, f)
		}
	}
	if f := forwardOf(t, tg, fwdMgmt); f.Iface != "mgmt0" || f.Entry != domain.EntryManagement {
		t.Errorf("the management side's forward enters on %q", f.Iface)
	}
}

// A forward to a device follows the device's address: a new address changes only the elements of the
// DNAT map, nothing else of the target, so the engine updates them like the identity map's (M7). A device
// without an address leaves its forward compiled but inactive, without elements, with its reason.
func TestADeviceForwardFollowsTheAddressByElementsOnly(t *testing.T) {
	w := forwardWorld(t)
	before := w.compile(nil)
	w.setAddrs(map[string][]string{devESP42: {"10.10.0.142"}, devESP43: {"10.10.0.43"}, devLab: {"10.20.0.50"}})
	after := w.compile(nil)
	if got := elementsOf(forwardMap(t, after)); !slices.Contains(got, "wan0 . tcp . 8080 : 10.10.0.142 . 80") {
		t.Fatalf("the forward did not follow the device: %v", got)
	}
	if !sameButMapElements(before, after) {
		t.Error("a device's new address changes more than the elements of maps")
	}

	w.setAddrs(map[string][]string{devESP43: {"10.10.0.43"}, devLab: {"10.20.0.50"}})
	gone := w.compile(nil)
	f := forwardOf(t, gone, fwdWeb)
	if f.Active || f.Reason != domain.ForwardDeviceNoAddress || f.Target.IsValid() {
		t.Errorf("a forward to a device without an address: %+v", f)
	}
	for _, e := range elementsOf(forwardMap(t, gone)) {
		if strings.HasPrefix(e, "wan0 . tcp . 8080 ") {
			t.Errorf("an element is left for a device without an address: %s", e)
		}
	}
	if !sameButMapElements(before, gone) {
		t.Error("a device that lost its address changes more than the elements of maps")
	}
	if !hasCounter(gone, f.Counter) || indexOfRule(chainOf(t, gone, "prerouting"), f.Counter) < 0 ||
		indexOfRule(chainOf(t, gone, "forward"), "ct original proto-dst 8080 accept") < 0 {
		t.Error("the inactive forward lost its counter, its counting rule or its accept rule")
	}
}

// sameButMapElements compares the nftables part of two targets without the elements of their maps and
// the access sets whose addresses follow the devices.
func sameButMapElements(a, b *Target) bool {
	strip := func(t *Target) string {
		n := t.Nft
		n.Generation = ""
		n.Maps = append([]MapDef(nil), n.Maps...)
		for i := range n.Maps {
			n.Maps[i].Elements = nil
		}
		raw, _ := json.Marshal(n)
		return string(raw)
	}
	return strip(a) == strip(b)
}

func hasCounter(tg *Target, name string) bool { return slices.Contains(tg.Nft.Counters, name) }

// The prerouting NAT chain (priority -100, next to the service redirect) has one DNAT rule for every
// forward, after the counting rules of the forwards: on the entry interfaces only, to an address of the
// interface the packet came in on (the kernel's lookup follows a new address at once), DNAT through the
// map.
func TestTheDNATRuleStandsInThePreroutingNATChainNextToTheServiceRedirect(t *testing.T) {
	w := forwardWorld(t)
	tg := w.compile(func(in *Input) { in.ServiceNS = ServiceNSDefault })
	c := chainOf(t, tg, "prerouting")
	if c.Base == nil || c.Base.Type != "nat" || c.Base.Hook != "prerouting" || c.Base.Prio != -100 {
		t.Fatalf("the chain: %+v", c.Base)
	}
	rules := renderRules(c)
	last := rules[len(rules)-1]
	want := `meta iifname { mgmt0, wan0 } meta nfproto ipv4 meta l4proto { tcp, udp } fib daddr . iif type local dnat ip concat to meta iifname . meta l4proto . th dport map @` + tg.ForwardMap
	if last != want {
		t.Fatalf("the DNAT rule:\n%s\nwant\n%s", last, want)
	}
	if indexOfRule(c, "dport 53") < 0 {
		t.Error("the service redirect is gone")
	}
	// without the service namespace the chain holds the counting rules and the DNAT alone
	if rules := renderRules(chainOf(t, w.compile(nil), "prerouting")); len(rules) != len(tg.Forwards)+1 || rules[len(rules)-1] != want {
		t.Errorf("without a service namespace: %v", rules)
	}
	// only the uplink: the interface alone, no set
	u := accessWorld(t)
	u.addForward(fwdWeb, `{protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}}`)
	if r := renderRules(chainOf(t, u.compile(nil), "prerouting")); !strings.HasPrefix(r[len(r)-1], "meta iifname wan0 meta nfproto ipv4 meta l4proto { tcp, udp }") {
		t.Errorf("one entry interface: %v", r)
	}
}

// The counter of a forward counts connections (P31-M35a-09, option (b)): one counting rule per forward in
// the prerouting NAT chain, in front of the DNAT rule, IPv4 only (P31-AUDIT-02), in on the forward's entry interface, to an address
// of that interface, its protocol and external ports, with the forward's counter and `last`. A NAT chain
// sees the first packet of a connection only, and the rule stands before every access rule, so it counts
// each connection that arrives through the forward whichever rule accepts or drops it afterwards. No
// other rule counts into the forward's counter: not its accept rule, nor an allow rule that accepts its
// connections.
func TestEachForwardCountsItsConnectionsInTheNATChainInFrontOfTheDNAT(t *testing.T) {
	w := forwardWorld(t)
	w.addConfigRule(ruleA, `{name: all, source: {device: esp32-42}, action: allow}`)
	tg := w.compile(nil)
	c := chainOf(t, tg, "prerouting")
	rules := renderRules(c)
	dnat := indexOfRule(c, "dnat ip concat")
	for _, f := range tg.Forwards {
		ports := fmt.Sprint(f.ExtFrom)
		if f.ExtTo != f.ExtFrom {
			ports = fmt.Sprintf("%d-%d", f.ExtFrom, f.ExtTo)
		}
		mark := ""
		if f.Entry == domain.EntryManagement {
			mark = "ct mark set ct mark | 0x400000 "
		}
		want := fmt.Sprintf(`meta iifname %s meta nfproto ipv4 meta l4proto %s fib daddr . iif type local th dport %s %scounter %q last`, f.Iface, f.Protocol, ports, mark, f.Counter)
		i := slices.Index(rules, want)
		if i < 0 || i > dnat {
			t.Errorf("the counting rule of %s (at %d, the DNAT at %d):\nwant %s\n%s", f.Name, i, dnat, want, strings.Join(rules, "\n"))
			continue
		}
		if !hasCounter(tg, f.Counter) {
			t.Errorf("the counter %s of %s is not declared", f.Counter, f.ID)
		}
		for _, ch := range tg.Nft.Chains {
			for _, r := range ch.Rules {
				if strings.Contains(renderRule(r), fmt.Sprintf("counter %q", f.Counter)) && (ch.Name != "prerouting" || r.Comment != c.Rules[i].Comment) {
					t.Errorf("the counter of %s is also in %s: %s", f.Name, ch.Name, renderRule(r))
				}
			}
		}
	}
	// the counter follows the forward's id, not its place
	if web, rng := forwardOf(t, tg, fwdWeb), forwardOf(t, tg, fwdRange); ForwardCounterName(fwdWeb) != web.Counter || web.Counter == rng.Counter {
		t.Errorf("counters: %s %s", web.Counter, rng.Counter)
	}
}

// In the forward chain each forward accepts the connections its DNAT translated (in on its entry
// interface, out to a local test network, its protocol and external ports) after the access rules and in
// front of the access matrix; the default of the matrix keeps everything else out. The accept rule
// counts nothing: the forward's counter is in the NAT chain.
func TestTheForwardAcceptStandsAfterTheAccessRulesAndBeforeTheMatrix(t *testing.T) {
	w := forwardWorld(t)
	w.addConfigRule(ruleA, `{name: no-telnet, source: {global: true}, protocol: tcp, ports: [23], action: drop}`)
	entries := []model.MatrixEntry{matrixEntry(lanEP(labID), lanEP(iotID), model.MatrixEntryPolicyAllow)}
	w.cfg.AccessMatrix = &model.AccessMatrix{Entries: &entries}
	tg := w.compile(nil)
	fwd := chainOf(t, tg, "forward")
	rules := renderRules(fwd)
	lan := setName(t, tg, "ifs_lan")
	wantWeb := fmt.Sprintf(`meta iifname wan0 meta oifname @%s ct status dnat meta l4proto tcp ct original proto-dst 8080 accept`, lan)
	wantRange := fmt.Sprintf(`meta iifname wan0 meta oifname @%s ct status dnat meta l4proto udp ct original proto-dst 8000-8003 accept`, lan)
	iWeb, iRange := slices.Index(rules, wantWeb), slices.Index(rules, wantRange)
	if iWeb < 0 || iRange < 0 {
		t.Fatalf("the accept rules are missing:\n%s", strings.Join(rules, "\n"))
	}
	jump := indexOfRule(fwd, "jump "+AccessForwardChain)
	matrix := indexOfRule(fwd, "meta iifname br-lab meta oifname br-iot accept")
	drop := indexOfRule(fwd, `counter "forward_drop" drop`)
	if jump < 0 || matrix < 0 || drop < 0 || jump > iWeb || iWeb > matrix || iRange > matrix || matrix > drop {
		t.Errorf("order: access %d, forwards %d/%d, matrix %d, drop %d", jump, iWeb, iRange, matrix, drop)
	}
}

func setName(t *testing.T, tg *Target, base string) string {
	t.Helper()
	for _, s := range tg.Nft.Sets {
		if strings.HasPrefix(s.Name, base+"_") {
			return s.Name
		}
	}
	t.Fatalf("no set %s", base)
	return ""
}

// Every rule, configured and overlay, matches forwarded connections with the target as responder right
// after its own form, with the same counter and verdict: the reply source against the rule's source, the
// peer (the original source) against its destination, the reply's source port (the target port) against
// its ports. A rule for ICMP cannot select a forwarded connection and has no responder form; the input
// chain never sees one.
func TestEveryRuleMatchesForwardedConnectionsWithTheTargetAsResponder(t *testing.T) {
	w := forwardWorld(t)
	w.addConfigRule(ruleA, `{name: web-down, source: {device: esp32-42}, destination: {uplink: true}, protocol: tcp, ports: [80], action: reset, cut_existing: true}`)
	w.addConfigRule(ruleB, `{name: lab-from-office, source: {network: Lab}, destination: {cidr: 198.51.100.0/24}, action: reject}`)
	w.addConfigRule(ruleC, `{name: no-ping, source: {global: true}, protocol: icmp, action: drop}`)
	w.ruleOverlay(`{target: {group: sensors}, rule: {action: drop}}`, time.Second)
	tg := w.compile(nil)
	if !tg.Access.Responder {
		t.Fatal("the plan does not match forwarded connections")
	}
	rules := renderRules(chainOf(t, tg, AccessForwardChain))
	ov, a, b, c := tg.Access.Rules[0], tg.Access.Rules[1], tg.Access.Rules[2], tg.Access.Rules[3]
	want := []string{
		fmt.Sprintf(`ct original ip saddr @%s counter %q last drop`, ov.SourceSet, ov.Counter),
		fmt.Sprintf(`ct status dnat ct reply ip saddr @%s counter %q last drop`, ov.SourceSet, ov.Counter),
		fmt.Sprintf(`ct original ip saddr @%s ct original ip daddr != @%s meta l4proto tcp ct original proto-dst 80 counter %q last reject with tcp reset`, a.SourceSet, nonUplinkSetName(), a.Counter),
		fmt.Sprintf(`ct status dnat ct reply ip saddr @%s ct original ip saddr != @%s meta l4proto tcp ct reply proto-src 80 counter %q last reject with tcp reset`, a.SourceSet, nonUplinkSetName(), a.Counter),
		fmt.Sprintf(`ct original ip saddr @%s ct original ip daddr 198.51.100.0/24 counter %q last reject with icmpx port-unreachable`, b.SourceSet, b.Counter),
		fmt.Sprintf(`ct status dnat ct reply ip saddr @%s ct original ip saddr 198.51.100.0/24 counter %q last reject with icmpx port-unreachable`, b.SourceSet, b.Counter),
		fmt.Sprintf(`ct original ip saddr @%s meta l4proto icmp counter %q last drop`, c.SourceSet, c.Counter),
	}
	if !slices.Equal(rules, want) {
		t.Fatalf("access_forward:\n%s\nwant\n%s", strings.Join(rules, "\n"), strings.Join(want, "\n"))
	}
	for _, r := range renderRules(chainOf(t, tg, AccessInputChain)) {
		if strings.Contains(r, "ct status dnat") {
			t.Errorf("the input chain has a responder form: %s", r)
		}
	}
}

// "Also cut existing connections" reaches forwarded connections too: the cut window of the forward chain
// has the responder form of the cutting rule (the reset goes to the peer that opened the connection) and
// the responder forms of the rules before it as returns; the input chain's window has none.
func TestACutWindowAlsoResetsForwardedConnections(t *testing.T) {
	w := forwardWorld(t)
	w.addConfigRule(ruleA, `{name: keep-ntp, source: {global: true}, protocol: udp, ports: [123], action: allow}`)
	w.addConfigRule(ruleB, `{name: web-down, source: {device: esp32-42}, protocol: tcp, ports: [80], action: drop, cut_existing: true}`)
	tg := w.compile(nil)
	b := tg.Access.Rules[1]
	fwd, in := tg.Access.CutRules([]string{b.Key})
	f := renderRules(Chain{Rules: fwd})
	wantCut := fmt.Sprintf(`ct status dnat ct reply ip saddr @%s meta l4proto tcp ct reply proto-src 80 ct state { established } ct direction original reject with tcp reset`, b.SourceSet)
	wantReturn := fmt.Sprintf(`ct status dnat ct reply ip saddr @%s meta l4proto udp ct reply proto-src 123 return`, classifyNetsName())
	if !slices.Contains(f, wantCut) || !slices.Contains(f, wantReturn) {
		t.Fatalf("cut_forward:\n%s", strings.Join(f, "\n"))
	}
	if slices.Index(f, wantReturn) > slices.Index(f, wantCut) {
		t.Error("the earlier rule's responder return stands behind the cut")
	}
	for _, r := range renderRules(Chain{Rules: in}) {
		if strings.Contains(r, "ct status dnat") {
			t.Errorf("cut_input: %s", r)
		}
	}
}

// The Go mirror of the rules decides a forwarded connection like the domain layer that explain asks:
// the first rule whose responder form selects it, overlay rules first.
func TestTheCompiledRulesDecideForwardedConnectionsLikeTheDomainLayer(t *testing.T) {
	w := forwardWorld(t)
	w.addConfigRule(ruleA, `{name: web-up, source: {device: esp32-42}, destination: {uplink: true}, protocol: tcp, ports: [80], action: drop}`)
	w.addConfigRule(ruleB, `{name: lab-office, source: {network: Lab}, destination: {cidr: 198.51.100.0/24}, protocol: udp, port_ranges: [{from: 9001, to: 9002}], action: reject}`)
	w.addConfigRule(ruleC, `{name: sensors-mgmt, source: {group: sensors}, destination: {cidr: 192.168.56.0/24}, protocol: tcp, action: reset}`)
	w.addConfigRule(ruleD, `{name: ssh, source: {global: true}, protocol: tcp, ports: [22], action: allow}`)
	w.ruleOverlay(`{target: {device: esp32-43}, rule: {action: drop, protocol: tcp, ports: [443]}}`, time.Second)
	checked, matched := forwardedGrid(t, w, w.compile(nil))
	if matched < 5 || matched == checked {
		t.Fatalf("the grid is not useful: %d of %d matched", matched, checked)
	}
}

// A peer in the management network is not "uplink" for the responder form (plan §2.4), also when
// allowed_sources is unset (the default: the interface's subnet) or names less than the subnet: a
// device's "offline" rule (device → uplink: drop) leaves the management side's forward alone, in the
// kernel's set as in the domain layer that explain asks.
func TestAnUplinkRuleDoesNotMatchAManagementPeerOfAForwardedConnection(t *testing.T) {
	for name, sources := range map[string]*[]string{"unset": nil, "narrower": ptr([]string{"192.168.56.5/32"})} {
		t.Run(name, func(t *testing.T) {
			w := forwardWorld(t)
			w.cfg.Management.AllowedSources = sources
			w.addConfigRule(ruleA, `{name: offline, source: {device: esp32-43}, destination: {uplink: true}, action: drop}`)
			w.addConfigRule(ruleB, `{name: web-offline, source: {device: esp32-42}, destination: {uplink: true}, protocol: tcp, action: drop}`)
			tg := w.compile(nil)
			if tg.HasErrors() {
				t.Fatalf("problems: %+v", tg.Problems)
			}
			if r := tg.Access.Winner(ForwardedTuple(netip.MustParseAddr("192.168.56.20"), netip.MustParseAddr("192.168.56.1"), "tcp", 8443, netip.MustParseAddr("10.10.0.43"), 443)); r != nil {
				t.Errorf("the rule %s (%s) matches a forwarded connection from the management network", r.Name, r.ID)
			}
			if r := tg.Access.Winner(ForwardedTuple(netip.MustParseAddr("198.51.100.7"), netip.MustParseAddr("203.0.113.1"), "tcp", 8080, netip.MustParseAddr("10.10.0.42"), 80)); r == nil || r.ID != ruleB {
				t.Errorf("a forwarded connection from behind the uplink: %+v, want %s", r, ruleB)
			}
			if !slices.Contains(tg.Access.PeerNonUplink, pfx("192.168.56.0/24")) {
				t.Errorf("the responder form's exclusion %v lacks the management subnet", tg.Access.PeerNonUplink)
			}
			var set *SetDef
			for i := range tg.Nft.Sets {
				if tg.Nft.Sets[i].Name == peerNonUplinkSetName() {
					set = &tg.Nft.Sets[i]
				}
			}
			if set == nil || !slices.Contains(set.Elements, "192.168.56.0/24") {
				t.Fatalf("the set %s: %+v", peerNonUplinkSetName(), set)
			}
			ref, orig := 0, 0
			for _, r := range renderRules(chainOf(t, tg, AccessForwardChain)) {
				if strings.Contains(r, "ct status dnat") && strings.Contains(r, "@"+peerNonUplinkSetName()) {
					ref++
				}
				if !strings.Contains(r, "ct status dnat") && strings.Contains(r, "@"+peerNonUplinkSetName()) {
					orig++
				}
			}
			if ref == 0 || orig != 0 {
				t.Errorf("responder forms on the peer set: %d, original forms: %d\n%s", ref, orig, strings.Join(renderRules(chainOf(t, tg, AccessForwardChain)), "\n"))
			}
			if checked, matched := forwardedGrid(t, w, tg); matched == 0 || matched == checked {
				t.Fatalf("the grid is not useful: %d of %d matched", matched, checked)
			}
		})
	}
}

// forwardedGrid compares the compiled rules with the domain layer for every enabled forward, a few
// peers and every external port, with the management facts the engine passes explain (the sources and
// the interface's subnet). It returns how many tuples it checked and how many a rule decided.
func forwardedGrid(t *testing.T, w *faultWorld, tg *Target) (checked, matched int) {
	t.Helper()
	world, err := domain.NewWorld(w.cfg, w.overlays)
	if err != nil {
		t.Fatal(err)
	}
	idx, _ := domain.BuildIndex(w.cfg)
	facts := domain.ForwardFacts{Identity: w.id}
	mgmt := append(append([]netip.Prefix(nil), tg.Management.Sources...), tg.Management.Subnet)
	entries := map[string]netip.Addr{domain.EntryUplink: netip.MustParseAddr("203.0.113.1"), domain.EntryManagement: netip.MustParseAddr("192.168.56.1")}
	peers := []string{"198.51.100.7", "203.0.113.50", "8.8.8.8", "192.168.56.20", "192.168.56.5"}
	for _, f := range domain.Forwards(w.cfg) {
		if !f.Enabled {
			continue
		}
		st := idx.ForwardStatus(f, facts)
		dev, _ := w.id.OwnerOf(st.Target)
		for _, p := range peers {
			peer := netip.MustParseAddr(p)
			for port := f.ExtFrom; port <= f.ExtTo; port++ {
				q := domain.ForwardedQuery{Forward: f, Target: st.Target, Network: st.Network, Device: dev, Peer: peer, Protocol: f.Protocol, Port: port}
				want := world.ForwardedDecision(q, domain.AccessFacts{Management: mgmt})
				got := tg.Access.Winner(ForwardedTuple(peer, entries[f.Entry], f.Protocol, port, st.Target, f.TargetPortOf(port)))
				wantID, gotID := want.Rule, ""
				if got != nil {
					gotID = got.ID
					matched++
				}
				if wantID != gotID {
					t.Errorf("%s from %s to %d: the domain layer says %q (%s), the compiled rules %q", f.Name, p, port, wantID, want.Reason, gotID)
				}
				checked++
			}
		}
	}
	return checked, matched
}

// A configuration without a forward compiles exactly what it compiled before M35a: no DNAT map, no
// prerouting rule, no accept, no responder form, the same hash. The golden files of every other scenario
// (routed, access, faults, ...) are unchanged; this checks the same for a disabled forward.
func TestWithoutAForwardInThePacketPathNothingChanges(t *testing.T) {
	w := accessWorld(t)
	w.addConfigRule(ruleA, `{name: web-down, source: {device: esp32-42}, protocol: tcp, ports: [80], action: drop, cut_existing: true}`)
	without := w.compile(nil)
	w.addForward(fwdOff, `{name: off, protocol: tcp, entry: uplink, external_port: 9999, target: {device: esp32-42}, enabled: false}`)
	disabled := w.compile(nil)
	if disabled.ForwardMap != "" || len(disabled.Forwards) != 0 || disabled.Access.Responder {
		t.Fatalf("a disabled forward is compiled: %+v", disabled.Forwards)
	}
	a, _ := json.Marshal(without.Nft)
	b, _ := json.Marshal(disabled.Nft)
	if string(a) != string(b) {
		t.Errorf("a disabled forward changes the ruleset:\n%s", diffLines(string(a), string(b)))
	}
	if without.Hash != disabled.Hash {
		t.Error("a disabled forward changes the hash")
	}
	if hasChain(without, "prerouting") {
		t.Error("a prerouting chain without a service and a forward")
	}
}

// `management` in the two-port topology is the uplink twice: the validation refuses it, and a
// configuration that has it anyway compiles nothing for it.
func TestAManagementForwardInTheTwoPortTopologyIsNotCompiled(t *testing.T) {
	w := accessWorld(t)
	w.addForward(fwdMgmt, `{protocol: tcp, entry: management, external_port: 8443, target: {device: esp32-43}}`)
	w.cfg.Management.Interface = model.InterfaceRef{Name: ptr("wan0")}
	w.cfg.Management.AllowedSources = nil
	tg := w.compile(nil)
	if len(tg.Forwards) != 0 || tg.ForwardMap != "" {
		t.Fatalf("compiled: %+v", tg.Forwards)
	}
}

// An entry side without an address keeps its forward in the ruleset, inactive with its reason; the
// kernel's lookup of the entry address takes it into effect when the address comes.
func TestAForwardOfAnEntryWithoutAddressIsInactive(t *testing.T) {
	w := forwardWorld(t)
	tg := w.compile(func(in *Input) {
		for i := range in.Host.Links {
			if in.Host.Links[i].Name == "mgmt0" {
				in.Host.Links[i].Addrs = nil
			}
		}
	})
	f := forwardOf(t, tg, fwdMgmt)
	if f.Active || f.Reason != domain.ForwardEntryWithoutAddress {
		t.Errorf("%+v", f)
	}
	if w := forwardOf(t, tg, fwdWeb); !w.Active {
		t.Errorf("the uplink's forward: %+v", w)
	}
}

// The DOCKER-USER accept rules cover the path of a forward from its entry side to the target's bridge
// (plan §3.4): a packet to a bridge is accepted by the rule of its output interface.
func TestDockerUserCoversThePathOfAForward(t *testing.T) {
	tg := forwardWorld(t).compile(nil)
	for _, f := range tg.Forwards {
		var bridge string
		for _, b := range tg.Bridges {
			if b.Address.Masked().Contains(f.Target) {
				bridge = b.Name
			}
		}
		if bridge == "" || !slices.Contains(tg.DockerUser, bridge) {
			t.Errorf("%s: the bridge %q of the target %s has no DOCKER-USER rule (%v)", f.ID, bridge, f.Target, tg.DockerUser)
		}
	}
}

// An interface name with a dash is a name in a map key, not a range; the parts of a concatenated value
// are rendered like a key's.
func TestMapElementsWithInterfaceNamesAndConcatenatedValues(t *testing.T) {
	m := MapDef{Name: "m", KeyType: forwardMapKey, ValueType: forwardMapValue,
		Elements: []MapElement{{Key: "br-up . tcp . 80", Value: "10.0.0.5 . 8080"}}}
	got, _ := json.Marshal(mapElements(m))
	if want := `[[{"concat":["br-up","tcp",80]},{"concat":["10.0.0.5",8080]}]]`; string(got) != want {
		t.Errorf("elements %s, want %s", got, want)
	}
	if got, _ := json.Marshal(mapKeyPart("10.0.0.1-10.0.0.9")); string(got) != `{"range":["10.0.0.1","10.0.0.9"]}` {
		t.Errorf("an address range: %s", got)
	}
	if got, _ := json.Marshal(mapValueType(forwardMapValue)); string(got) != `["ipv4_addr","inet_service"]` {
		t.Errorf("the value type: %s", got)
	}
	// what nft lists is read back in the same form (verify, and the element difference of an apply)
	listed := `{"nftables":[{"map":{"family":"inet","table":"chaosgw","name":"m","type":["ifname","inet_proto","inet_service"],"map":["ipv4_addr","inet_service"],"elem":[[{"concat":["br-up","tcp",80]},{"concat":["10.0.0.5",8080]}]]}}]}`
	rs := parseRuleset(t, listed)
	if p := rs.Set("m").Pairs(); !reflect.DeepEqual(p, map[string]string{"br-up . tcp . 80": "10.0.0.5 . 8080"}) {
		t.Errorf("pairs %v", p)
	}
	tx, err := (Nft{Maps: []MapDef{m}}).Transaction(rs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tx), `"element"`) {
		t.Errorf("an unchanged element is written again: %s", tx)
	}
}

func scenarioForward(t *testing.T) *Target {
	t.Helper()
	w := forwardWorld(t)
	w.addConfigRule(ruleA, `{name: web-down, source: {device: esp32-42}, destination: {uplink: true}, protocol: tcp, ports: [80], action: reset, cut_existing: true}`)
	w.addConfigRule(ruleB, `{name: lab-office, source: {network: Lab}, destination: {cidr: 198.51.100.0/24}, action: reject}`)
	w.ruleOverlay(`{target: {group: sensors}, rule: {action: drop, protocol: udp}}`, time.Second)
	return w.compile(func(in *Input) { in.ServiceNS = ServiceNSDefault })
}

// describeForwards is the readable form of the forwarding side of a target for the golden file.
func describeForwards(tg *Target) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# forwards\n")
	for _, f := range tg.Forwards {
		fmt.Fprintf(&b, "%s  %s  %s/%d-%d on %s (%s) -> %s:%d-%d  active %v %s  counter %s\n", f.ID, f.Name, f.Protocol, f.ExtFrom, f.ExtTo, f.Iface, f.Entry, f.Target, f.TgtFrom, f.TgtTo, f.Active, f.Reason, f.Counter)
	}
	fmt.Fprintf(&b, "# map %s\n", tg.ForwardMap)
	for _, m := range tg.Nft.Maps {
		if m.Name == tg.ForwardMap {
			for _, e := range m.Elements {
				fmt.Fprintf(&b, "  %s : %s\n", e.Key, e.Value)
			}
		}
	}
	var chains []string
	for _, c := range tg.Nft.Chains {
		chains = append(chains, c.Name)
	}
	sort.Strings(chains)
	for _, name := range []string{"prerouting", ForwardReplyChain} {
		fmt.Fprintf(&b, "# chain %s\n", name)
		for _, c := range tg.Nft.Chains {
			if c.Name == name {
				for _, r := range c.Rules {
					fmt.Fprintf(&b, "  %s\n", renderRule(r))
				}
			}
		}
	}
	// the routing of the replies of a management-side forward, and the policy table without the
	// management network
	for _, table := range []int{PolicyTable, ForwardReplyTable} {
		fmt.Fprintf(&b, "# table %d\n", table)
		for _, r := range routesOfTable(tg, table) {
			s := r.Dst
			if r.Type != "" {
				s = r.Type + " " + s
			}
			if r.Via != "" {
				s += " via " + r.Via
			}
			if r.Dev != "" {
				s += " dev " + r.Dev
			}
			if r.Metric != nil {
				s += fmt.Sprintf(" metric %d", *r.Metric)
			}
			fmt.Fprintf(&b, "  %s\n", s)
		}
	}
	b.WriteString("# fwmark rules\n")
	for _, r := range tg.Rules {
		if r.Fwmark != "" {
			fmt.Fprintf(&b, "  %d fwmark %s lookup %d\n", r.Priority, r.Fwmark, r.Table)
		}
	}
	b.WriteString(describeAccess(tg))
	fwd, _ := tg.Access.CutRules([]string{tg.Access.Rules[1].Key})
	b.WriteString("# cut_forward window of " + tg.Access.Rules[1].Name + "\n")
	for _, r := range fwd {
		fmt.Fprintf(&b, "  %s\n", renderRule(r))
	}
	return b.String()
}

func TestGoldenForwards(t *testing.T) {
	tg := scenarioForward(t)
	if tg.HasErrors() || len(tg.Problems) != 0 {
		t.Fatalf("problems: %+v", tg.Problems)
	}
	goldenText(t, "forward", describeForwards(tg))
}

func TestGoldenForwardNftTransaction(t *testing.T) {
	tx, err := scenarioForward(t).Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	var pretty any
	_ = json.Unmarshal(tx, &pretty)
	golden(t, "forward.nft", pretty)
}

// The replies of a forward from the management side go back through the management interface by their
// mark, not by a route of the policy table (P31-M35a-05 and -10, option (c)). Table 100, which every
// packet of a device is looked up in, never has the management subnet, so a device cannot open a
// connection into the management network whatever the access rules allow. The counting rule of the
// management forward sets bit 22 of the conntrack mark, the chain fwd_reply (prerouting, -140) copies it
// onto the packet mark of the replies from the test networks, and the rule `fwmark 0x400000/0x400000
// lookup 101` (priority 925, before table 100's rules) sends them to table 101: the management subnet,
// the default gateway of the management interface (a peer behind a router of the management network)
// and a prohibit fallback, so such a reply never takes the uplink. Without a forward from the management
// side none of it is compiled; without a default route of the management interface table 101 has the
// subnet and the fallback.
func TestAManagementForwardRoutesItsRepliesToTheManagementNetwork(t *testing.T) {
	tg := forwardWorld(t).compile(nil)
	for _, r := range tg.Routes {
		if r.Table == PolicyTable && (r.Dev == "mgmt0" || r.Dst == "192.168.56.0/24") {
			t.Errorf("the policy table routes to the management network: %+v", r)
		}
	}
	zero, prohibit := 0, 4096
	want := []executor.Route{
		{Action: "replace", Family: 4, Table: 101, Dst: "192.168.56.0/24", Dev: "mgmt0"},
		{Action: "replace", Family: 4, Table: 101, Dst: "default", Via: "192.168.56.254", Dev: "mgmt0", Metric: &zero},
		{Action: "replace", Family: 4, Table: 101, Dst: "default", Type: "prohibit", Metric: &prohibit},
	}
	if got := routesOfTable(tg, 101); !reflect.DeepEqual(got, want) {
		t.Errorf("table 101: %+v, want %+v", got, want)
	}
	rule := executor.Rule{Action: "add", Family: 4, Priority: 925, Fwmark: "0x400000/0x400000", Table: 101}
	if !slices.Contains(tg.Rules, rule) {
		t.Errorf("no policy rule for the marked replies: %+v", tg.Rules)
	}
	c := chainOf(t, tg, ForwardReplyChain)
	if c.Base == nil || c.Base.Type != "filter" || c.Base.Hook != "prerouting" || c.Base.Prio != -140 {
		t.Errorf("the chain %s: %+v", ForwardReplyChain, c.Base)
	}
	restore := fmt.Sprintf("meta iifname @%s ct direction reply ct mark & 0x400000 0x400000 meta mark set meta mark | 0x400000", setName(t, tg, "ifs_lan"))
	if got := renderRules(c); !slices.Equal(got, []string{restore}) {
		t.Errorf("the chain %s:\n%s\nwant\n%s", ForwardReplyChain, strings.Join(got, "\n"), restore)
	}
	// the conntrack mark is set by the management forward's rule alone
	var marking []string
	for _, r := range renderRules(chainOf(t, tg, "prerouting")) {
		if strings.Contains(r, "ct mark set") {
			marking = append(marking, r)
		}
	}
	if len(marking) != 1 || !strings.HasPrefix(marking[0], "meta iifname mgmt0 meta nfproto ipv4 meta l4proto tcp fib daddr . iif type local th dport 8443 ct mark set ct mark | 0x400000") {
		t.Errorf("the rules that set the conntrack mark: %v", marking)
	}

	// without a default route of the management interface: the subnet and the fallback
	noGW := forwardWorld(t).compile(func(in *Input) {
		var kept []HostRoute
		for _, r := range in.Host.Defaults {
			if r.Dev != "mgmt0" {
				kept = append(kept, r)
			}
		}
		in.Host.Defaults = kept
	})
	if got := routesOfTable(noGW, 101); !reflect.DeepEqual(got, []executor.Route{want[0], want[2]}) {
		t.Errorf("table 101 without a management gateway: %+v", got)
	}

	// without a forward from the management side (none, or a disabled one): nothing of it
	w := accessWorld(t)
	w.addForward(fwdWeb, `{protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}}`)
	w.addForward(fwdMgmt, `{protocol: tcp, entry: management, external_port: 8443, target: {device: esp32-43}, enabled: false}`)
	plain := w.compile(nil)
	for _, r := range plain.Routes {
		if r.Dev == "mgmt0" || r.Table == 101 {
			t.Errorf("a route of the management side without a forward from there: %+v", r)
		}
	}
	for _, r := range plain.Rules {
		if r.Fwmark == ForwardReplyMark || r.Table == 101 {
			t.Errorf("a rule of the management side without a forward from there: %+v", r)
		}
	}
	for _, ch := range plain.Nft.Chains {
		if ch.Name == ForwardReplyChain || indexOfRule(ch, "ct mark") >= 0 {
			t.Errorf("the chain %s has the mark of the management side without a forward from there", ch.Name)
		}
	}
}

// Table 101 knows the management interface's connected subnet and its default gateway only: without a
// default route of the management interface a peer outside the subnet (behind a static route of the
// management interface) gets no reply, and the compiler says so with a warning (P31-M35a-05). With a
// default route, or without a forward from the management side, there is no such warning.
func TestAManagementForwardWithoutAManagementDefaultRouteWarnsThatItsRepliesReachTheSubnetOnly(t *testing.T) {
	warnings := func(tg *Target) []Problem {
		var out []Problem
		for _, p := range tg.Problems {
			if p.Code == CodeForwardRepliesSubnetOnly {
				out = append(out, p)
			}
		}
		return out
	}
	noGW := forwardWorld(t).compile(func(in *Input) {
		var kept []HostRoute
		for _, r := range in.Host.Defaults {
			if r.Dev != "mgmt0" {
				kept = append(kept, r)
			}
		}
		in.Host.Defaults = kept
	})
	got := warnings(noGW)
	if len(got) != 1 || got[0].Severity != SevWarning || !strings.Contains(got[0].Message, "the replies of management forwards reach only 192.168.56.0/24") {
		t.Errorf("warnings %+v", got)
	}
	if noGW.HasErrors() {
		t.Errorf("a warning must not stop the apply: %+v", noGW.Problems)
	}
	if got := warnings(forwardWorld(t).compile(nil)); len(got) != 0 {
		t.Errorf("a warning although the management interface has a default route: %+v", got)
	}
	w := accessWorld(t)
	w.addForward(fwdWeb, `{protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}}`)
	plain := w.compile(func(in *Input) {
		var kept []HostRoute
		for _, r := range in.Host.Defaults {
			if r.Dev != "mgmt0" {
				kept = append(kept, r)
			}
		}
		in.Host.Defaults = kept
	})
	if got := warnings(plain); len(got) != 0 {
		t.Errorf("a warning without a forward from the management side: %+v", got)
	}
}

// The bit of the replies of a management-side forward is bit 22 of the mark layout (plan §3.3, bits 22-23
// reserved for further routing marks): no fault id, direction, PMTU index, service or duplication bit,
// and outside the bits left to other software. The classification keeps it when it writes the id or the
// direction, and the policy rule's mark is the bit under a mask of the bit alone.
func TestTheReplyMarkOfAManagementForwardIsAFreeRoutingBit(t *testing.T) {
	bit := ForwardReplyMarkBit
	if bit != 0x400000 {
		t.Fatalf("the bit is %#x, want bit 22", bit)
	}
	taken := markIDMaskBits | markDirMaskBits | PMTUMarkMask | 0x100000 | MarkDupBit
	if bit&taken != 0 || bit&0xff00000f != 0 {
		t.Errorf("the bit %#x collides with %#x or with the bits of other software", bit, taken)
	}
	if MarkKeepOnIDWrite&bit == 0 || MarkKeepOnDirectionWrite&bit == 0 {
		t.Error("the classification clears the bit")
	}
	if ForwardReplyMark != fmt.Sprintf("%#x/%#x", bit, bit) || ServiceMark == ForwardReplyMark {
		t.Errorf("the fwmark %s", ForwardReplyMark)
	}
	if ForwardReplyTable != 101 || ForwardReplyTable == ServiceTable || ForwardReplyTable >= PMTUTableFirst ||
		ForwardReplyTable < executor.OwnTableFirst || ForwardReplyTable > executor.OwnTableLast {
		t.Errorf("the table %d", ForwardReplyTable)
	}
	if ForwardReplyRulePriority <= ServiceRulePriority || ForwardReplyRulePriority >= PMTURulePriority || PMTURulePriority >= PolicyRulePriority {
		t.Errorf("the priority %d", ForwardReplyRulePriority)
	}
}

// routesOfTable returns the routes of a routing table, in the target's order.
func routesOfTable(tg *Target, table int) []executor.Route {
	var out []executor.Route
	for _, r := range tg.Routes {
		if r.Table == table {
			out = append(out, r)
		}
	}
	return out
}

// The compiler is the backstop of the validation: a forward the validation refuses (here written into the
// configuration around it) is left out with a warning, so it never takes a port of the control plane, and
// the others are compiled. Of two forwards that overlap, the one later in the order (by name) is left out.
func TestAForwardTheValidationRefusesIsLeftOutWithAWarning(t *testing.T) {
	for _, c := range []struct{ name, body, why string }{
		{"UI port", `{protocol: tcp, entry: uplink, external_port: 440, external_port_end: 450, target: {device: esp32-42}}`, "UI/API port"},
		{"SSH on the management side", `{protocol: tcp, entry: management, external_port: 22, target: {device: esp32-42}}`, "SSH"},
		{"overlap", `{name: zz, protocol: tcp, entry: management, external_port: 8079, external_port_end: 8081, target: {device: esp32-43}}`, "overlaps"},
		{"outside the test networks", `{protocol: tcp, entry: uplink, external_port: 7000, target: {address: 203.0.113.50}}`, "no local test network"},
		{"in the management network", `{protocol: tcp, entry: uplink, external_port: 7000, target: {address: 192.168.56.9}}`, "no local test network"},
		{"ranges of different length", `{protocol: tcp, entry: uplink, external_port: 7000, external_port_end: 7002, target_port: 9000, target_port_end: 9001, target: {device: esp32-42}}`, "as long as"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := accessWorld(t)
			w.addForward(fwdWeb, `{name: web, protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}}`)
			var f model.PortForward
			doc, _ := domain.ParseDocument([]byte(c.body), domain.FormatYAML)
			raw, _ := json.Marshal(doc)
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			cfg := *w.cfg
			fwds := map[string]model.PortForward{fwdWeb: (*cfg.PortForwards)[fwdWeb], fwdRange: f}
			cfg.PortForwards = &fwds
			n, errs := domain.Normalize(&cfg)
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			w.cfg = n
			tg := w.compile(nil)
			if _, ok := tg.Forward(fwdRange); ok {
				t.Fatal("the refused forward is compiled")
			}
			if _, ok := tg.Forward(fwdWeb); !ok {
				t.Error("the valid forward is left out too")
			}
			var warned bool
			for _, p := range tg.Problems {
				warned = warned || (p.Code == CodeForwardRefused && p.Severity == SevWarning && strings.Contains(p.Message, fwdRange) && strings.Contains(p.Message, c.why))
			}
			if !warned || tg.HasErrors() {
				t.Errorf("problems: %+v", tg.Problems)
			}
		})
	}
}

// A device with an old address that lingers on next to its new one (Identity.Current puts the new one
// first) gets its forwarded connections at the new address, even when the old one sorts lower.
func TestTheDNATMapSendsToTheDevicesMostCurrentAddress(t *testing.T) {
	w := forwardWorld(t)
	w.setAddrs(map[string][]string{devESP42: {"10.10.0.11", "10.10.0.61"}, devESP43: {"10.10.0.43"}, devLab: {"10.20.0.50"}})
	w.id.Current = map[string][]netip.Addr{devESP42: {netip.MustParseAddr("10.10.0.61"), netip.MustParseAddr("10.10.0.11")}}
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("problems: %+v", tg.Problems)
	}
	if got := elementsOf(forwardMap(t, tg)); !slices.Contains(got, "wan0 . tcp . 8080 : 10.10.0.61 . 80") {
		t.Fatalf("the DNAT map: %v", got)
	}
	if f := forwardOf(t, tg, fwdWeb); f.Target != netip.MustParseAddr("10.10.0.61") {
		t.Errorf("the forward's target: %+v", f)
	}
}
