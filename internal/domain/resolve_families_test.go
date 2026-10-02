package domain

import (
	"net/netip"
	"testing"
	"time"
)

func dnsQuery(name string) Query { return Query{Source: subjectA, DNSName: name} }

func TestDNSNamesCountAsADestinationForTheLevel(t *testing.T) {
	tw := newTestWorld(t, false)
	named := tw.overlay(`{target: {device: esp32-42}, dns: {names: [broker.example.com], action: nxdomain}}`, time.Second)
	general := tw.overlay(`{target: {device: esp32-42}, dns: {action: servfail}}`, 2*time.Second)
	w := tw.world()

	win := mustWinner(t, w.Resolve(dnsQuery("broker.example.com")), FamilyDNS)
	if win.ID != named.Id.String() || win.Level != 2 || win.DNS.Action != "nxdomain" {
		t.Fatalf("winner = %s level %d", win.ID, win.Level)
	}
	other := mustWinner(t, w.Resolve(dnsQuery("other.example.com")), FamilyDNS)
	if other.ID != general.Id.String() || other.Level != 4 {
		t.Fatalf("other name: winner = %s level %d", other.ID, other.Level)
	}
	// without a name in the query only the fault for all names applies
	if g := mustWinner(t, w.Resolve(Query{Source: subjectA}), FamilyDNS); g.ID != general.Id.String() {
		t.Fatalf("no name: winner = %s", g.ID)
	}
}

func TestDNSWildcardNames(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {group: sensors}, dns: {names: ['*.iot.example.com'], action: servfail}}`, time.Second)
	w := tw.world()
	for name, want := range map[string]bool{"a.iot.example.com": true, "A.B.IOT.example.com": true, "iot.example.com": false, "example.com": false} {
		if got := Winner(w.Resolve(dnsQuery(name)), FamilyDNS) != nil; got != want {
			t.Errorf("%s: match = %v, want %v", name, got, want)
		}
	}
}

func TestDNSFromAProfileOnANetworkLosesToADeviceFault(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {network: IoT}, profile: dns-broken}`, time.Second)
	dev := tw.overlay(`{target: {device: esp32-42}, dns: {action: delay, delay: 800ms}}`, 2*time.Second)
	win := mustWinner(t, tw.world().Resolve(dnsQuery("x.test")), FamilyDNS)
	if win.ID != dev.Id.String() {
		t.Fatalf("winner = %+v", win)
	}
}

func TestTLSDefaultsToThePortsOf443And8883(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, tls: {case: untrusted_ca}}`, time.Second)
	w := tw.world()
	for _, tt := range []struct {
		proto string
		port  int
		want  bool
	}{{"tcp", 443, true}, {"tcp", 8883, true}, {"tcp", 8080, false}, {"udp", 443, false}, {"", 0, false}} {
		q := toServer(tt.proto, tt.port)
		if got := Winner(w.Resolve(q), FamilyTLS) != nil; got != tt.want {
			t.Errorf("%s/%d: match = %v, want %v", tt.proto, tt.port, got, tt.want)
		}
	}
}

func TestTLSExplicitPortsDestinationAndSNI(t *testing.T) {
	tw := newTestWorld(t, false)
	general := tw.overlay(`{target: {device: esp32-42}, tls: {case: expired, ports: [8883]}}`, time.Second)
	sni := tw.overlay(`{target: {device: esp32-42}, tls: {case: self_signed, ports: [8883], sni: [broker.example.com]}}`, 2*time.Second)
	w := tw.world()

	q := toServer("tcp", 8883)
	q.SNI = "broker.example.com"
	if win := mustWinner(t, w.Resolve(q), FamilyTLS); win.ID != sni.Id.String() || win.Level != 1 {
		t.Fatalf("with a matching SNI: %s level %d", win.ID, win.Level)
	}
	q.SNI = "other.example.com"
	if win := mustWinner(t, w.Resolve(q), FamilyTLS); win.ID != general.Id.String() || win.Level != 3 {
		t.Fatalf("with another SNI: %s level %d", win.ID, win.Level)
	}
	q.SNI = ""
	if win := mustWinner(t, w.Resolve(q), FamilyTLS); win.ID != general.Id.String() {
		t.Fatalf("without an SNI the SNI case must not apply: %s", win.ID)
	}
}

func TestTLSBrokenProfileResetsTheHandshakeOnTheTLSPorts(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {network: IoT}, profile: tls-broken}`, time.Second)
	win := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyTLS)
	if win.TLS.Case != "handshake_reset" || win.ProfileName != "tls-broken" {
		t.Fatalf("winner = %+v", win)
	}
	if Winner(tw.world().Resolve(toServer("tcp", 80)), FamilyTLS) != nil {
		t.Error("port 80 is not a TLS port")
	}
}

func TestDHCPActionsOnADeviceBeatThoseOnTheNetwork(t *testing.T) {
	tw := newTestWorld(t, false)
	net := tw.overlay(`{target: {network: IoT}, dhcp: {action: short_lease, lease_time: 30s}}`, time.Second)
	dev := tw.overlay(`{target: {device: esp32-42}, dhcp: {action: silence}}`, 2*time.Second)
	res := tw.world().Resolve(Query{Source: subjectA})
	win := mustWinner(t, res, FamilyDHCP)
	if win.ID != dev.Id.String() || win.Level != 4 {
		t.Fatalf("winner = %s level %d", win.ID, win.Level)
	}
	if o := familyResult(t, res, FamilyDHCP).Overridden; len(o) != 1 || o[0].ID != net.Id.String() || o[0].Level != 8 {
		t.Fatalf("overridden = %+v", o)
	}
	// another device of the network only gets the network's action
	other := Query{Source: Subject{IP: netip.MustParseAddr("10.10.0.77")}}
	if w := mustWinner(t, tw.world().Resolve(other), FamilyDHCP); w.ID != net.Id.String() {
		t.Fatalf("other device: winner = %s", w.ID)
	}
}

func TestMTUResolvesAmongItsOwnFamily(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultMTU, `{family: mtu, source: {network: IoT}, mtu: {size: 1400, mode: icmp}}`, 0)
	dev := tw.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280, mode: mss_clamp}}}`, time.Second)
	tw.overlay(`{target: {device: esp32-42}, fault: {latency: 10ms}}`, 2*time.Second) // another family
	res := tw.world().Resolve(toServer("tcp", 443))
	if w := mustWinner(t, res, FamilyMTU); w.ID != dev.Id.String() || w.MTU.Size != 1280 {
		t.Fatalf("mtu winner = %+v", w)
	}
	if latencyOf(mustWinner(t, res, FamilyImpairment)) != "10ms" {
		t.Error("the impairment must be unaffected by the mtu fault")
	}
}

func TestAProfileWithSeveralPartsCompetesInEachFamily(t *testing.T) {
	tw := newTestWorld(t, false)
	// the example's custom profile has an impairment part and a dns part
	tw.overlay(`{target: {device: esp32-42}, profile: slow-dns-lte}`, time.Second)
	res := tw.world().Resolve(Query{Source: subjectA, DNSName: "x.test"})
	imp := mustWinner(t, res, FamilyImpairment)
	dns := mustWinner(t, res, FamilyDNS)
	if imp.ProfileName != "slow-dns-lte" || latencyOf(imp) != "50ms" || dns.ProfileName != "slow-dns-lte" || dns.DNS.Action != "delay" {
		t.Fatalf("impairment %+v dns %+v", imp, dns)
	}
}

func TestAnUnknownProfileOverlayIsIgnoredByTheResolution(t *testing.T) {
	tw := newTestWorld(t, false)
	o := tw.overlay(`{target: {device: esp32-42}, profile: lte}`, time.Second)
	nope := "no-such-profile"
	o.Profile = &nope
	tw.overlays[0] = o
	if got := tw.world().Resolve(Query{Source: subjectA}); len(got) != 0 {
		t.Fatalf("results = %+v", got)
	}
}

// ---- tunnel faults ------------------------------------------------------------------------

func TestTunnelFaultsAreResolvedPerTunnelOverlayFirstThenNewest(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultTun, `{family: tunnel, tunnel: {link: site-b}, latency: 70ms}`, time.Hour)
	tw.configFault(idFaultMTU, `{family: tunnel, tunnel: {client: lab-rA}, latency: 10ms}`, 0)
	tw.configFault(idFaultAny, `{family: tunnel, tunnel: {client: lab-rA}, latency: 20ms}`, time.Minute)
	ov := tw.overlay(`{fault: {family: tunnel, tunnel: {link: site-b}, blackout: true}}`, time.Second)
	got := tw.world().ResolveTunnels()
	if len(got) != 2 {
		t.Fatalf("tunnels = %+v", got)
	}
	// "client:..." sorts before "link:..."
	client, link := got[0], got[1]
	if client.Winner.ID != idFaultAny || len(client.Overridden) != 1 || client.Overridden[0].Reason != "newer" {
		t.Fatalf("client tunnel = %+v", client)
	}
	if link.Winner.ID != ov.Id.String() || link.Winner.Layer != LayerOverlay || link.Overridden[0].Reason != "overlay layer wins (D24)" {
		t.Fatalf("link tunnel = %+v", link)
	}
}

func TestADisabledTunnelFaultIsIgnored(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultTun, `{family: tunnel, tunnel: {link: site-b}, latency: 70ms, enabled: false}`, 0)
	if got := tw.world().ResolveTunnels(); len(got) != 0 {
		t.Fatalf("tunnels = %+v", got)
	}
}

// ---- access rules -------------------------------------------------------------------------

func TestAccessRulesOverlaysFirstNewestFirstThenConfiguredInOrder(t *testing.T) {
	tw := newTestWorld(t, false)
	// the example's rule rejects tcp/853 for the network IoT
	oldDrop := tw.overlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [853]}}`, time.Second)
	newAllow := tw.overlay(`{target: {device: esp32-42}, rule: {action: allow, protocol: tcp, ports: [853]}}`, time.Minute)
	w := tw.world()
	dot := toServer("tcp", 853)

	r := w.ResolveAccess(dot)
	if !r.Matched || r.Action != "allow" || r.RuleID != newAllow.Id.String() || r.Layer != LayerOverlay {
		t.Fatalf("the newest overlay rule must win: %+v", r)
	}
	// without overlays the configured rule decides
	tw.overlays = nil
	r = tw.world().ResolveAccess(dot)
	if !r.Matched || r.Action != "reject" || r.Layer != LayerConfig || r.RuleID != idRule || r.Name != "no-dot" {
		t.Fatalf("configured rule: %+v", r)
	}
	_ = oldDrop
	// traffic the rules do not cover has no verdict here: the matrix decides
	if r := tw.world().ResolveAccess(toServer("tcp", 443)); r.Matched {
		t.Fatalf("unexpected match: %+v", r)
	}
}

func TestConfiguredAccessRulesKeepTheirOrderAndSkipDisabledOnes(t *testing.T) {
	tw := newTestWorld(t, false)
	rules := *tw.cfg.AccessRules
	first := rules[idRule]
	first.Enabled = ptrTo(false)
	rules[idRule] = first
	broad := first
	broad.Enabled = nil
	broad.Name = ptrTo("allow-all")
	broad.Action = "allow"
	broad.Protocol = nil
	broad.Ports = nil
	rules[idNew] = broad
	order := tw.cfg.AccessRuleOrder
	*order = []uuidT{mustUUID(idRule), mustUUID(idNew)}
	r := tw.world().ResolveAccess(toServer("tcp", 853))
	if !r.Matched || r.RuleID != idNew || r.Action != "allow" {
		t.Fatalf("the disabled rule must be skipped: %+v", r)
	}
	// reorder: the broad rule first
	*order = []uuidT{mustUUID(idNew), mustUUID(idRule)}
	if r := tw.world().ResolveAccess(toServer("tcp", 853)); r.RuleID != idNew {
		t.Fatalf("order: %+v", r)
	}
}

func TestAnAccessRuleCarriesCutExisting(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [8883], cut_existing: true}}`, time.Second)
	if r := tw.world().ResolveAccess(toServer("tcp", 8883)); !r.Matched || !r.CutExisting {
		t.Fatalf("access = %+v", r)
	}
}

// ---- the API's view -----------------------------------------------------------------------

func TestCandidateRefCarriesWhatTheExplainEndpointShows(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultNet, `{name: iot-latency, source: {network: IoT}, latency: 100ms, jitter: 20ms}`, time.Hour)
	res := tw.world().Resolve(Query{Source: subjectA})
	win := mustWinner(t, res, FamilyImpairment)
	ref := win.Ref("")
	if ref.Layer != "config" || ref.Family != "impairment" || ref.Id.String() != idFaultNet || deref(ref.Name) != "iot-latency" ||
		deref(ref.Level) != 8 || deref(ref.Summary) != "latency 100ms ± 20ms" || ref.Since == nil || ref.Reason != nil || ref.Profile != nil {
		t.Fatalf("ref = %+v", ref)
	}

	tw2 := newTestWorld(t, false)
	tw2.overlay(`{target: {device: esp32-42}, profile: bad-lte}`, time.Second)
	win2 := mustWinner(t, tw2.world().Resolve(Query{Source: subjectA}), FamilyImpairment)
	ref2 := win2.Ref("level 4 beats level 8")
	if ref2.Layer != "overlay" || ref2.Profile == nil || deref(ref2.Profile.Name) != "bad-lte" || deref(ref2.Reason) != "level 4 beats level 8" ||
		deref(ref2.Summary) != "latency 150ms ± 50ms, loss 3%, rate 2Mbit" {
		t.Fatalf("ref = %+v", ref2)
	}
}

func TestSummaries(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, fault: {upload: {latency: 200ms}, download: {burst_loss: {p: 1%, r: 30%}, rate: 1Mbit}}}`, time.Second)
	tw.overlay(`{target: {device: lab-host}, fault: {blackout: true}}`, time.Second)
	tw.overlay(`{target: {device: lab-host}, fault: {flapping: {up: 20s, down: 10s}}}`, 2*time.Second)
	tw.overlay(`{target: {device: lab-host}, fault: {family: mtu, mtu: {size: 1280, mode: blackhole}}}`, 3*time.Second)
	tw.overlay(`{target: {device: lab-host}, dns: {action: servfail}}`, 4*time.Second)
	tw.overlay(`{target: {device: lab-host}, tls: {case: expired}}`, 5*time.Second)
	tw.overlay(`{target: {device: lab-host}, dhcp: {action: silence}}`, 6*time.Second)
	got := map[string]string{}
	for _, c := range tw.world().candidates() {
		got[c.Family+"/"+c.Summary()] = ""
	}
	for _, want := range []string{
		"impairment/upload latency 200ms, download burst loss, rate 1Mbit", "impairment/blackout", "impairment/flapping 20s up/10s down",
		"mtu/mtu 1280 (blackhole)", "dns/dns servfail", "tls/tls expired", "dhcp/dhcp silence",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("no candidate with the summary %q in %v", want, got)
		}
	}
}
