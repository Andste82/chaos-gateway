package compiler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
)

// The tests of hostname selectors (plan §2.4, §2.6, M20): the DNS-derived sets, the rules that look them up
// and where they stand in the classify chain.

func setByName(tg *Target, name string) (SetDef, bool) {
	for _, s := range tg.Nft.Sets {
		if s.Name == name {
			return s, true
		}
	}
	return SetDef{}, false
}

// classifyHostRules returns the rules of the classify chain that look up a DNS-derived set, in order.
func classifyHostRules(t *testing.T, tg *Target) []string {
	t.Helper()
	var out []string
	for _, r := range renderRules(chainOf(t, tg, ClassifyChain)) {
		if strings.Contains(r, " @dns_") {
			out = append(out, r)
		}
	}
	return out
}

func TestADNSDerivedSetIsDynamicKeyedOnTheDeviceAndTheAddressAndHasATimeout(t *testing.T) {
	w := newFaultWorld(t)
	w.addConfigRule(ruleA, `{name: by-name, source: {global: true}, destination: {hostname: Broker.Example.com.}, action: drop}`)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.DNSSets) != 1 {
		t.Fatalf("sets %+v", tg.DNSSets)
	}
	ds := tg.DNSSets[0]
	// the pattern is canonical (lower case, no dot) and the id depends on nothing else
	if ds.Pattern != "broker.example.com" || ds.ID != DNSSetID("BROKER.example.com") || !strings.HasPrefix(ds.Name, ds.ID+"_") {
		t.Errorf("%+v", ds)
	}
	set, ok := setByName(tg, ds.Name)
	if !ok || set.Type != "ipv4_addr . ipv4_addr" || strings.Join(set.Flags, ",") != "timeout" || !set.Dynamic || len(set.Elements) != 0 {
		t.Errorf("%+v", set)
	}
	// the transaction creates it as a concatenated type with a timeout, and does not fill it
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nftables []map[string]map[string]map[string]any `json:"nftables"`
	}
	if err := json.Unmarshal(tx, &doc); err != nil {
		t.Fatal(err)
	}
	created := false
	for _, c := range doc.Nftables {
		if s, ok := c["add"]["set"]; ok && s["name"] == ds.Name {
			created = true
			if fmt.Sprint(s["type"]) != "[ipv4_addr ipv4_addr]" || fmt.Sprint(s["flags"]) != "[timeout]" {
				t.Errorf("the set is not created as a concatenation with a timeout: %v", s)
			}
		}
		if _, ok := c["add"]["element"]; ok && c["add"]["element"]["name"] == ds.Name {
			t.Errorf("an apply fills a DNS-derived set: %v", c)
		}
		if f, ok := c["flush"]["set"]; ok && f["name"] == ds.Name {
			t.Errorf("an apply flushes a DNS-derived set: %v", c)
		}
	}
	if !created {
		t.Error("the transaction does not create the set")
	}
	// the rule looks the pair of the conntrack original tuple up
	rules := renderRules(chainOf(t, tg, AccessForwardChain))
	if len(rules) != 1 || !strings.Contains(rules[0], "ct original ip saddr @classify_nets_") ||
		!strings.Contains(rules[0], "ct original ip saddr . ct original ip daddr @"+ds.Name) || !strings.HasSuffix(rules[0], " drop") {
		t.Errorf("rules = %v", rules)
	}
	// the clients the proxy records are the rule's sources: every test network for the global scope
	if got := prefixList(ds.Sources); got != "10.10.0.0/24,10.20.0.0/24,10.30.0.0/24" {
		t.Errorf("sources %s", got)
	}
}

func prefixList[T interface{ String() string }](ps []T) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return strings.Join(out, ",")
}

func TestARuleAndFaultsOfOneSelectorShareOneSetAndTheSourcesAreTheirUnion(t *testing.T) {
	w := newFaultWorld(t)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, destination: {hostname: broker.example.com}, action: reject}`)
	w.overlay(`{target: {device: lab-host}, fault: {destination: {hostname: BROKER.example.com}, latency: 50ms}}`, 0)
	w.overlay(`{target: {device: esp32-43}, fault: {destination: {hostname: "*.broker.example.com"}, latency: 50ms}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.DNSSets) != 2 {
		t.Fatalf("sets %+v", tg.DNSSets)
	}
	one, ok := tg.DNSSetOf("broker.example.com")
	if !ok {
		t.Fatal("no set for broker.example.com")
	}
	if got := prefixList(one.Sources); got != "10.10.0.42/32,10.20.0.50/32" {
		t.Errorf("the clients of the exact name: %s", got)
	}
	wild, _ := tg.DNSSetOf("*.broker.example.com")
	if got := prefixList(wild.Sources); got != "10.10.0.43/32" || wild.ID == one.ID {
		t.Errorf("the clients of the wildcard: %s (%s, %s)", got, wild.ID, one.ID)
	}
	// a name, and the same name written another way, are one selector
	if _, ok := tg.DNSSetOf("Broker.Example.COM."); !ok {
		t.Error("the lookup is not canonical")
	}
}

func TestAHostnameFaultStandsInFrontOfTheLookupLevelsAndGoesToTheChainOfItsId(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {destination: {hostname: broker.example.com}, protocol: tcp, ports: [8883], latency: 50ms}}`, 0)
	w.overlay(`{target: {network: IoT}, fault: {loss: 1%}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	for _, p := range tg.Problems {
		if p.Code == "hostname_unresolved" {
			t.Errorf("a hostname fault is compiled now: %+v", p)
		}
	}
	f := faultOf(t, tg, o.Id.String(), "")
	ds, _ := tg.DNSSetOf("broker.example.com")
	rules := renderRules(chainOf(t, tg, ClassifyChain))
	first, look := -1, -1
	for i, r := range rules {
		if strings.Contains(r, "@"+ds.Name) && first < 0 {
			first = i
		}
		if strings.HasPrefix(r, "map[vmap:") && look < 0 {
			look = i
		}
	}
	if first < 0 || look < 0 || first > look {
		t.Fatalf("the hostname rule is at %d, the first lookup level at %d: %v", first, look, rules)
	}
	want := "meta l4proto tcp ct original proto-dst 8883 ct original ip saddr . ct original ip daddr @" + ds.Name + " goto " + MarkChainName(f.ID)
	if !strings.HasSuffix(rules[first], want) || !strings.Contains(rules[first], "ct original ip saddr @hsrc_") {
		t.Errorf("rule %q, want it to end with %q", rules[first], want)
	}
	// the source set holds the device's address only, and is static (refilled at every apply)
	var src SetDef
	for _, s := range tg.Nft.Sets {
		if strings.HasPrefix(s.Name, hostSrcBase) {
			src = s
		}
	}
	if strings.Join(src.Elements, ",") != "10.10.0.42/32" || src.Dynamic {
		t.Errorf("%+v", src)
	}
	// the fault is a winner like any other: the preview lists it, and the chain and the counters exist
	listed := false
	for _, e := range tg.Effective {
		listed = listed || e.Source == o.Id.String()
	}
	if !listed || !hasChain(tg, MarkChainName(f.ID)) {
		t.Errorf("effective %+v", tg.Effective)
	}
}

func TestHostnameFaultsAreTriedStrongestFirstAndAFaultOfTheWholeSourceOfAHigherRankTakesThemOut(t *testing.T) {
	w := newFaultWorld(t)
	// config, a network and a group: the weakest
	cfgNet := "b2000000-0000-4000-8000-000000000001"
	w.addConfigFault(cfgNet, `{source: {network: IoT}, destination: {hostname: "*.example.com"}, latency: 10ms}`, 0)
	grp := w.overlay(`{target: {group: sensors}, fault: {destination: {hostname: "*.example.com"}, latency: 20ms}}`, 0)
	dev := w.overlay(`{target: {device: esp32-42}, fault: {destination: {hostname: broker.example.com}, latency: 30ms}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	rules := classifyHostRules(t, tg)
	if len(rules) != 3 {
		t.Fatalf("%d host rules: %v", len(rules), rules)
	}
	// overlays before configuration (D24), then the level: device + destination (2) before group + destination (5)
	idDev := faultOf(t, tg, dev.Id.String(), "").ID
	idGrp := faultOf(t, tg, grp.Id.String(), "").ID
	idNet := faultOf(t, tg, cfgNet, "").ID
	for i, id := range []int{idDev, idGrp, idNet} {
		if !strings.HasSuffix(rules[i], " goto "+MarkChainName(id)) {
			t.Errorf("rule %d = %s, want it to go to %s", i, rules[i], MarkChainName(id))
		}
	}

	// a fault of the whole device of a higher rank (an overlay) covers every packet of it: the hostname
	// fault of the group, a lower rank, cannot take over from it for that device
	w2 := newFaultWorld(t)
	g2 := w2.overlay(`{target: {group: sensors}, fault: {destination: {hostname: "*.example.com"}, latency: 20ms}}`, 0)
	w2.overlay(`{target: {device: esp32-42}, fault: {latency: 100ms}}`, time.Second)
	tg2 := w2.compile(nil)
	if tg2.HasErrors() {
		t.Fatalf("%+v", tg2.Problems)
	}
	id2 := faultOf(t, tg2, g2.Id.String(), "").ID
	for _, set := range tg2.Nft.Sets {
		if strings.HasPrefix(set.Name, hostSrcBase) {
			// the group's members are esp32-42 and esp32-43; only the one without a fault of its own is left
			if strings.Join(set.Elements, ",") != "10.10.0.43/32" {
				t.Errorf("the sources of the group's hostname fault: %v", set.Elements)
			}
		}
	}
	if rs := classifyHostRules(t, tg2); len(rs) != 1 || !strings.HasSuffix(rs[0], " goto "+MarkChainName(id2)) {
		t.Errorf("%v", rs)
	}
}

func TestAHostnameFaultWithAQueuePerDeviceHasARulePerDevice(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {group: sensors}, fault: {destination: {hostname: broker.example.com}, rate: 1Mbit}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	rules := classifyHostRules(t, tg)
	if len(rules) != 2 {
		t.Fatalf("%v", rules)
	}
	for _, dev := range []struct{ id, addr string }{{devESP42, "10.10.0.42/32"}, {devESP43, "10.10.0.43/32"}} {
		f := faultOf(t, tg, o.Id.String(), dev.id)
		found := false
		for _, r := range rules {
			found = found || (strings.Contains(r, "ct original ip saddr "+dev.addr+" ") && strings.HasSuffix(r, " goto "+MarkChainName(f.ID)))
		}
		if !found {
			t.Errorf("no rule of device %s to %s in %v", dev.id, MarkChainName(f.ID), rules)
		}
	}
}

func TestAHostnameFaultThatImpairsNothingStillShadowsAndGoesToMarkZero(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {destination: {hostname: broker.example.com}, latency: 0ms}}`, 0)
	w.overlay(`{target: {network: IoT}, fault: {latency: 50ms}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	rules := classifyHostRules(t, tg)
	if len(rules) != 1 || !strings.HasSuffix(rules[0], " goto "+MarkChainName(0)) || !hasChain(tg, MarkChainName(0)) {
		t.Errorf("%v", rules)
	}
}

func TestAnMTUFaultThatNamesAHostnameHasARuleInTheMTULookup(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280}, destination: {hostname: example.com}}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() || len(tg.PMTU) != 1 {
		t.Fatalf("%+v %+v", tg.PMTU, tg.Problems)
	}
	for _, p := range tg.Problems {
		if p.Code == "hostname_unresolved" {
			t.Errorf("%+v", p)
		}
	}
	ds, ok := tg.DNSSetOf("example.com")
	if !ok {
		t.Fatal("no set")
	}
	rules := renderRules(chainOf(t, tg, PMTUClassifyChain))
	if len(rules) != 5 || !strings.Contains(rules[0], "ct original ip saddr . ct original ip daddr @"+ds.Name) || !strings.HasSuffix(rules[0], " goto "+tg.PMTU[0].Chain) {
		t.Errorf("%v", rules)
	}
}

// A hostname selector makes no difference to a target that has none: no set, no rule, the same hash.
func TestWithoutHostnamesNothingChanges(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, fault: {latency: 50ms}}`, 0)
	tg := w.compile(nil)
	if len(tg.DNSSets) != 0 {
		t.Errorf("%+v", tg.DNSSets)
	}
	for _, s := range tg.Nft.Sets {
		if strings.HasPrefix(s.Name, "dns_") || strings.HasPrefix(s.Name, hostSrcBase) {
			t.Errorf("set %s", s.Name)
		}
	}
	if got := domain.HostnamePattern(" A.b. "); got != "a.b" {
		t.Errorf("%q", got)
	}
}

// scenarioHostnames is the configuration of the hostname selectors that the transaction tests and the kernel
// test check: every use of a DNS-derived set next to a port forward (the responder form of the rule).
func scenarioHostnames(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.addConfigRule(ruleA, `{name: by-name, source: {global: true}, destination: {hostname: Broker.Example.com.}, protocol: tcp, ports: [8883], action: reject, reject_with: admin_prohibited}`)
	w.addConfigRule(ruleB, `{name: by-suffix, source: {group: sensors}, destination: {hostname: "*.cdn.example.net"}, action: drop, cut_existing: true}`)
	w.overlay(`{target: {device: esp32-42}, fault: {destination: {hostname: broker.example.com}, protocol: tcp, ports: [8883], latency: 50ms}}`, 0)
	w.overlay(`{target: {network: IoT}, fault: {destination: {hostname: "*.example.com"}, loss: 5%}}`, time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {destination: {hostname: broker.example.com}, rate: 1Mbit, latency: 20ms}}`, 2*time.Second)
	w.overlay(`{target: {device: lab-host}, fault: {family: mtu, mtu: {size: 1280, mode: blackhole}, destination: {hostname: example.com}}}`, 3*time.Second)
	return w.compile(func(in *Input) { in.ServiceNS = "cgsvc" })
}

func TestTheHostnameScenarioCompilesWithoutProblems(t *testing.T) {
	tg := scenarioHostnames(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.DNSSets) != 4 {
		t.Errorf("%+v", tg.DNSSets)
	}
}
