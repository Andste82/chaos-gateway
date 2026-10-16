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
	if !hasCounter(gone, f.Counter) || indexOfRule(chainOf(t, gone, "forward"), f.Counter) < 0 {
		t.Error("the inactive forward lost its counter or its accept rule")
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
// forward: on the entry interfaces only, to an address of the interface the packet came in on (the
// kernel's lookup follows a new address at once), DNAT through the map.
func TestTheDNATRuleStandsInThePreroutingNATChainNextToTheServiceRedirect(t *testing.T) {
	w := forwardWorld(t)
	tg := w.compile(func(in *Input) { in.ServiceNS = ServiceNSDefault })
	c := chainOf(t, tg, "prerouting")
	if c.Base == nil || c.Base.Type != "nat" || c.Base.Hook != "prerouting" || c.Base.Prio != -100 {
		t.Fatalf("the chain: %+v", c.Base)
	}
	rules := renderRules(c)
	last := rules[len(rules)-1]
	want := `meta iifname { mgmt0, wan0 } meta l4proto { tcp, udp } fib daddr . iif type local dnat ip concat to meta iifname . meta l4proto . th dport map @` + tg.ForwardMap
	if last != want {
		t.Fatalf("the DNAT rule:\n%s\nwant\n%s", last, want)
	}
	if indexOfRule(c, "dport 53") < 0 {
		t.Error("the service redirect is gone")
	}
	// without the service namespace the chain holds the DNAT alone
	if rules := renderRules(chainOf(t, w.compile(nil), "prerouting")); len(rules) != 1 || rules[0] != want {
		t.Errorf("without a service namespace: %v", rules)
	}
	// only the uplink: the interface alone, no set
	u := accessWorld(t)
	u.addForward(fwdWeb, `{protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}}`)
	if r := renderRules(chainOf(t, u.compile(nil), "prerouting")); !strings.HasPrefix(r[0], "meta iifname wan0 meta l4proto") {
		t.Errorf("one entry interface: %v", r)
	}
}

// In the forward chain each forward accepts the connections its DNAT translated (in on its entry
// interface, out to a local test network, its protocol and external ports) after the access rules and in
// front of the access matrix, and counts them; the default of the matrix keeps everything else out.
func TestTheForwardAcceptStandsAfterTheAccessRulesAndBeforeTheMatrix(t *testing.T) {
	w := forwardWorld(t)
	w.addConfigRule(ruleA, `{name: no-telnet, source: {global: true}, protocol: tcp, ports: [23], action: drop}`)
	entries := []model.MatrixEntry{matrixEntry(lanEP(labID), lanEP(iotID), model.MatrixEntryPolicyAllow)}
	w.cfg.AccessMatrix = &model.AccessMatrix{Entries: &entries}
	tg := w.compile(nil)
	fwd := chainOf(t, tg, "forward")
	rules := renderRules(fwd)
	web := forwardOf(t, tg, fwdWeb)
	rng := forwardOf(t, tg, fwdRange)
	lan := setName(t, tg, "ifs_lan")
	wantWeb := fmt.Sprintf(`meta iifname wan0 meta oifname @%s ct status dnat meta l4proto tcp ct original proto-dst 8080 counter %q last accept`, lan, web.Counter)
	wantRange := fmt.Sprintf(`meta iifname wan0 meta oifname @%s ct status dnat meta l4proto udp ct original proto-dst 8000-8003 counter %q last accept`, lan, rng.Counter)
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
	for _, f := range tg.Forwards {
		if !hasCounter(tg, f.Counter) {
			t.Errorf("the counter %s of %s is not declared", f.Counter, f.ID)
		}
	}
	// the counter follows the forward's id, not its place
	if ForwardCounterName(fwdWeb) != web.Counter || web.Counter == rng.Counter {
		t.Errorf("counters: %s %s", web.Counter, rng.Counter)
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
	tg := w.compile(nil)
	world, err := domain.NewWorld(w.cfg, w.overlays)
	if err != nil {
		t.Fatal(err)
	}
	idx, _ := domain.BuildIndex(w.cfg)
	facts := domain.ForwardFacts{Identity: w.id}
	mgmt := []netip.Prefix{pfx("192.168.56.0/24")}
	entries := map[string]netip.Addr{domain.EntryUplink: netip.MustParseAddr("203.0.113.1"), domain.EntryManagement: netip.MustParseAddr("192.168.56.1")}
	peers := []string{"198.51.100.7", "203.0.113.50", "8.8.8.8", "192.168.56.20"}
	checked, matched := 0, 0
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
	if matched < 5 || matched == checked {
		t.Fatalf("the grid is not useful: %d of %d matched", matched, checked)
	}
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
	b.WriteString("# chain prerouting\n")
	for _, c := range tg.Nft.Chains {
		if c.Name == "prerouting" {
			for _, r := range c.Rules {
				fmt.Fprintf(&b, "  %s\n", renderRule(r))
			}
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

// The replies of a forward from the management side go back to the management network: the policy
// table, which a device's packets are looked up in, gets the management subnet while such a forward is
// compiled. Without one the table is what it was (its default is the uplink).
func TestAManagementForwardRoutesItsRepliesToTheManagementNetwork(t *testing.T) {
	route := executor.Route{Action: "replace", Family: 4, Table: PolicyTable, Dst: "192.168.56.0/24", Dev: "mgmt0"}
	tg := forwardWorld(t).compile(nil)
	if !slices.Contains(tg.Routes, route) {
		t.Fatalf("no route back to the management network: %+v", tg.Routes)
	}
	w := accessWorld(t)
	w.addForward(fwdWeb, `{protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}}`)
	for _, r := range w.compile(nil).Routes {
		if r.Dev == "mgmt0" {
			t.Errorf("a route to the management network without a forward from there: %+v", r)
		}
	}
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
