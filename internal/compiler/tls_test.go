package compiler

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The TLS family (plan §2.4, §2.8, E11, M21): the cases the responder serves, and what the kernel sends to it.

func withServiceNS(in *Input) { in.ServiceNS = "cgsvc" }

func tlsRules(t *testing.T, tg *Target) []string {
	t.Helper()
	return renderRules(chainOf(t, tg, TLSChain))
}

// tlsEntries lists the entries of the responder's list as "case ports -> clients".
func tlsEntries(tg *Target) (out []string) {
	for _, c := range tg.TLSCases {
		var s []string
		for _, p := range c.Sources {
			s = append(s, p.String())
		}
		out = append(out, string(c.Case.Case)+" -> "+strings.Join(s, ","))
	}
	return out
}

func TestATLSCaseIsAnEntryOfTheResponderForTheClientsOfItsScope(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, tls: {case: expired, ports: [8883]}}`, 0)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if got := tlsEntries(tg); len(got) != 1 || got[0] != "expired -> 10.10.0.42/32" {
		t.Fatalf("%v", got)
	}
	c := tg.TLSCases[0]
	if c.Overlay != o.Id.String() || c.Key != "overlay:"+o.Id.String()+":tls" || c.Scope != "device esp32-42" || c.Profile != "" {
		t.Errorf("%+v", c)
	}
	// it is a winner: the preview lists it and the overlay is `effective`; it has no fault id and no tc class
	if len(tg.Faults) != 0 || len(tg.Winners) != 1 || tg.Winners[0] != c.Key || len(tg.Effective) != 1 || tg.Effective[0].Family != "tls" || tg.Effective[0].Summary != "tls expired" {
		t.Errorf("%+v %v %+v", tg.Faults, tg.Winners, tg.Effective)
	}
}

// What the kernel does with a case: a new connection of the device to the ports is marked in the conntrack (it stays
// selected) and for the service namespace (the packet is routed there); the later packets of a connection that is
// selected follow. Replies and what comes back from the namespace are never touched.
func TestTheKernelSelectsNewConnectionsOfTheDeviceForTheResponder(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, tls: {case: expired, ports: [8883]}}`, 0)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	c := chainOf(t, tg, TLSChain)
	if c.Base == nil || c.Base.Hook != "prerouting" || c.Base.Type != "filter" || c.Base.Prio != TLSPriority || c.Base.Prio <= ClassifyPriority {
		t.Fatalf("the chain runs right after the classification: %+v", c.Base)
	}
	rules := renderRules(c)
	want := []string{
		"meta iifname svc0 return",
		"ct direction reply return",
		"ct original ip saddr 10.10.0.42/32 meta l4proto tcp ct original proto-dst 8883 ct state new ct mark set ct mark | 0x800000 meta mark set meta mark | 0x100000",
		"ct original ip saddr 10.10.0.42/32 meta l4proto tcp ct original proto-dst 8883 ct mark & 0x800000 0x800000 meta mark set meta mark | 0x100000",
	}
	if strings.Join(rules, "\n") != strings.Join(want, "\n") {
		t.Errorf("rules:\n%s\nwant:\n%s", strings.Join(rules, "\n"), strings.Join(want, "\n"))
	}
	// the marks are the documented ones: the service mark is the one the policy rule of table 102 routes on
	if ServiceMarkBit != 0x100000 || TLSConnMarkBit != 0x800000 || fmt.Sprintf("%#x/%#x", ServiceMarkBit, ServiceMarkBit) != ServiceMark {
		t.Errorf("service mark %#x, conntrack mark %#x, ServiceMark %s", ServiceMarkBit, TLSConnMarkBit, ServiceMark)
	}
	if TLSConnMarkBit == ForwardReplyMarkBit || TLSConnMarkBit&(MarkDupBit|ServiceMarkBit) != 0 {
		t.Error("the conntrack mark bit is used by something else")
	}
}

func TestACaseWithoutPortsSelectsTheDefaultTLSPorts(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, tls: {case: self_signed}}`, 0)
	tg := w.compile(withServiceNS)
	r := tlsRules(t, tg)
	if len(r) != 4 || !strings.Contains(r[2], "ct original proto-dst { 443, 8883 }") {
		t.Fatalf("%v", r)
	}
}

// cut_existing: the first rule has no `ct state new`, so a connection that is there already is selected at its next
// packet, and there is no second rule for "the later packets".
func TestACaseThatCutsExistingConnectionsSelectsEveryPacket(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, tls: {case: handshake_reset, cut_existing: true, ports: [8883]}}`, 0)
	tg := w.compile(withServiceNS)
	r := tlsRules(t, tg)
	if len(r) != 3 {
		t.Fatalf("%v", r)
	}
	if strings.Contains(r[2], "ct state") || !strings.Contains(r[2], "ct mark set ct mark | 0x800000") || !strings.Contains(r[2], "meta mark set meta mark | 0x100000") {
		t.Errorf("%s", r[2])
	}
}

func TestTheDestinationOfACaseIsPartOfWhatTheKernelSelects(t *testing.T) {
	for _, c := range []struct{ name, dest, want string }{
		{"an address", `{cidr: 203.0.113.10}`, "ct original ip daddr 203.0.113.10/32"},
		{"a prefix", `{cidr: 203.0.113.0/24}`, "ct original ip daddr 203.0.113.0/24"},
		{"the uplink", `{uplink: true}`, "ct original ip daddr !="},
		{"a network", `{network: Lab}`, "ct original ip daddr 10.20.0.0/24"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newFaultWorld(t)
			w.overlay(`{target: {network: IoT}, tls: {case: untrusted_ca, destination: `+c.dest+`}}`, 0)
			tg := w.compile(withServiceNS)
			if tg.HasErrors() {
				t.Fatalf("%+v", tg.Problems)
			}
			r := tlsRules(t, tg)
			if len(r) != 4 || !strings.Contains(r[2], c.want) || !strings.Contains(r[3], c.want) {
				t.Fatalf("want %q in %v", c.want, r)
			}
		})
	}
}

// A hostname destination is looked up in the DNS-derived set of the selector, like the destination of a rule or
// another fault: the proxy fills it with what the device resolved.
func TestAHostnameDestinationOfACaseIsALookupInTheDNSDerivedSet(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, tls: {case: expired, destination: {hostname: "*.example.com"}}}`, 0)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	set, ok := tg.DNSSetOf("*.example.com")
	if !ok || len(set.Sources) != 1 || set.Sources[0].String() != "10.10.0.42/32" {
		t.Fatalf("the set of the selector: %+v %v", set, ok)
	}
	r := tlsRules(t, tg)
	if len(r) != 4 || !strings.Contains(r[2], "@"+set.Name) || !strings.Contains(r[3], "@"+set.Name) {
		t.Fatalf("%v", r)
	}
}

func TestTheSetOfTheUplinkIsNotDefinedTwiceWhenARuleNamesTheUplinkToo(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, rule: {action: drop, destination: {uplink: true}}}`, 0)
	w.overlay(`{target: {network: IoT}, tls: {case: expired, destination: {uplink: true}}}`, time.Second)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	seen := map[string]bool{}
	for _, s := range tg.Nft.Sets {
		if seen[s.Name] {
			t.Errorf("the set %s is defined twice", s.Name)
		}
		seen[s.Name] = true
	}
	if _, err := tg.Nft.Transaction(nil); err != nil {
		t.Fatal(err)
	}
}

// A case that applies to the network and one to a device: every source has its cases in the order of their rank, the
// sources with the same cases share an entry.
func TestSourcesWithTheSameCasesShareTheirEntriesAndTheOrderIsTheRank(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, tls: {case: untrusted_ca}}`, 0)
	w.overlay(`{target: {device: esp32-42}, tls: {case: expired, sni: [a.example.com]}}`, time.Second)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	got := tlsEntries(tg)
	// esp32-42 has both, the case with the SNI (level 1 beats level 7) first; the rest of the network the network's
	if len(got) != 3 || got[0] != "expired -> 10.10.0.42/32" || got[1] != "untrusted_ca -> 10.10.0.42/32" || !strings.HasPrefix(got[2], "untrusted_ca -> 10.10.0.0/24") {
		t.Fatalf("%v", got)
	}
}

// E5's neighbor: a device latency fault does not cancel a "TLS broken" profile on the network; the families do not compete.
func TestADeviceLatencyAndATLSBrokenProfileOnTheNetworkCombine(t *testing.T) {
	w := newFaultWorld(t)
	lat := w.overlay(`{target: {device: esp32-42}, fault: {latency: 100ms}}`, 0)
	prof := w.overlay(`{target: {network: IoT}, profile: tls-broken}`, time.Second)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.Faults) != 1 || tg.Faults[0].Source != lat.Id.String() {
		t.Errorf("the latency fault: %+v", tg.Faults)
	}
	if len(tg.TLSCases) == 0 {
		t.Fatal("no TLS case")
	}
	for _, c := range tg.TLSCases {
		if c.Overlay != prof.Id.String() || c.Profile != "tls-broken" || c.Case.Case != model.TlsCaseCaseHandshakeReset {
			t.Errorf("%+v", c)
		}
	}
	if len(tlsRules(t, tg)) != 4 {
		t.Errorf("%v", tlsRules(t, tg))
	}
}

// Without a service namespace there is no responder: the case is reported, nothing is redirected.
func TestWithoutAServiceNamespaceTheCasesAreReportedAndNothingIsRedirected(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, tls: {case: expired}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if hasChain(tg, TLSChain) {
		t.Error("a redirect without a service namespace")
	}
	found := false
	for _, p := range tg.Problems {
		found = found || (p.Code == CodeUnsupported && p.Severity == SevWarning && strings.Contains(p.Message, "TLS responder"))
	}
	if !found {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestWithoutACaseThereIsNoTLSChainAndNoAcceptTowardsTheNamespace(t *testing.T) {
	tg := newFaultWorld(t).compile(withServiceNS)
	if hasChain(tg, TLSChain) || len(tg.TLSCases) != 0 {
		t.Error("TLS parts without a case")
	}
	if i := indexOfRule(chainOf(t, tg, "forward"), "oifname svc0", "mark & 0x100000"); i >= 0 {
		t.Errorf("an accept for the service mark without a case: rule %d", i)
	}
}

// E11: an access rule decides before any redirect. The packets that the TLS case selects pass the forward chain
// towards the service namespace; the jump to the access rules stands in front of the accept, and the access rules
// judge them by the original destination (the broker), so a drop rule for tcp/8883 drops the connection before the
// responder sees it.
func TestE11AnAccessRuleDecidesBeforeTheRedirectIsAccepted(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [8883]}}`, 0)
	w.overlay(`{target: {device: esp32-42}, tls: {case: expired, ports: [8883]}}`, time.Second)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	fwd := chainOf(t, tg, "forward")
	jump := indexOfRule(fwd, "jump access_forward")
	accept := indexOfRule(fwd, "oifname svc0", "mark & 0x100000", "accept")
	if jump < 0 || accept < 0 || jump > accept {
		t.Fatalf("the access rules (%d) stand in front of the accept of the redirected packets (%d)", jump, accept)
	}
	// the rule is judged by the original tuple: it is in the access chain, which matches `ct original`
	acc := renderRules(chainOf(t, tg, AccessForwardChain))
	if len(acc) != 1 || !strings.Contains(acc[0], "ct original ip saddr") || !strings.Contains(acc[0], "ct original proto-dst 8883") || !strings.Contains(acc[0], "drop") {
		t.Errorf("%v", acc)
	}
	// and the case is still selected by the kernel: the redirect decides nothing about the access
	if len(tlsRules(t, tg)) != 4 {
		t.Errorf("%v", tlsRules(t, tg))
	}
	goldenText(t, "e11", describeTLS(tg))
}

// describeTLS is the readable form of the TLS part of a target for the golden file of E11: the responder's list, the
// chain that selects its connections and the part of the forward chain that is about them.
func describeTLS(tg *Target) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# responder's cases\n")
	for _, c := range tg.TLSCases {
		var s []string
		for _, p := range c.Sources {
			s = append(s, p.String())
		}
		fmt.Fprintf(&b, "%s  %s  scope %q  sources %s\n", c.Key, c.Case.Case, c.Scope, strings.Join(s, ","))
	}
	fmt.Fprintf(&b, "# %s\n", TLSChain)
	for _, r := range renderRules(chainOfName(tg, TLSChain)) {
		fmt.Fprintf(&b, "%s\n", r)
	}
	fmt.Fprintf(&b, "# forward (the access rules and the redirected packets)\n")
	for _, r := range renderRules(chainOfName(tg, "forward")) {
		if strings.HasPrefix(r, "jump access_") || strings.Contains(r, "oifname svc0") {
			fmt.Fprintf(&b, "%s\n", r)
		}
	}
	fmt.Fprintf(&b, "# %s\n", AccessForwardChain)
	for _, r := range renderRules(chainOfName(tg, AccessForwardChain)) {
		fmt.Fprintf(&b, "%s\n", r)
	}
	return b.String()
}

func chainOfName(tg *Target, name string) Chain {
	for _, c := range tg.Nft.Chains {
		if c.Name == name {
			return c
		}
	}
	return Chain{}
}

// scenarioTLS is the target of the nftables checks: a case for every kind of destination and the access rule that
// shares the set of the uplink.
func scenarioTLS(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, tls: {case: expired, ports: [8883]}}`, 0)
	w.overlay(`{target: {network: IoT}, tls: {case: untrusted_ca, cut_existing: true, destination: {hostname: "*.example.com"}}}`, time.Second)
	w.overlay(`{target: {group: sensors}, tls: {case: self_signed, destination: {uplink: true}}}`, 2*time.Second)
	w.overlay(`{target: {global: true}, tls: {case: handshake_stall, destination: {cidr: 203.0.113.0/24}}}`, 3*time.Second)
	w.overlay(`{target: {network: Lab}, tls: {case: hostname_mismatch, destination: {network: IoT}}}`, 4*time.Second)
	w.overlay(`{target: {network: IoT}, rule: {action: drop, destination: {uplink: true}, protocol: tcp, ports: [22]}}`, 5*time.Second)
	tg := w.compile(withServiceNS)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	return tg
}
