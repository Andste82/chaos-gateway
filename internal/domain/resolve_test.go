package domain

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// The specificity levels of plan §2.4. Ten faults, one per level, all matching the same traffic;
// removing the winner promotes the next level.
func TestEveryPrecedenceLevelWinsOverTheLessSpecificOnes(t *testing.T) {
	levels := []struct {
		level int
		yaml  string
	}{
		{1, `{target: {device: esp32-42}, fault: {destination: {hostname: broker.example.com}, protocol: tcp, ports: [8883], latency: 1ms}}`},
		{2, `{target: {device: esp32-42}, fault: {destination: {hostname: broker.example.com}, latency: 2ms}}`},
		{3, `{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], latency: 3ms}}`},
		{4, `{target: {device: esp32-42}, fault: {latency: 4ms}}`},
		{5, `{target: {group: sensors}, fault: {destination: {uplink: true}, latency: 5ms}}`},
		{6, `{target: {group: sensors}, fault: {latency: 6ms}}`},
		{7, `{target: {network: IoT}, fault: {protocol: tcp, latency: 7ms}}`},
		{8, `{target: {network: IoT}, fault: {latency: 8ms}}`},
		{9, `{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/24}, latency: 9ms}}`},
		{10, `{target: {global: true}, fault: {latency: 10ms}}`},
	}
	// created in an order that disagrees with the levels: specificity, not age, decides
	tw := newTestWorld(t, false)
	ids := map[int]string{}
	for i := len(levels) - 1; i >= 0; i-- {
		o := tw.overlay(levels[i].yaml, time.Duration(len(levels)-i)*time.Second)
		ids[levels[i].level] = o.Id.String()
	}
	for want := 1; want <= 10; want++ {
		w := tw.world()
		res := w.Resolve(toServer("tcp", 8883))
		win := mustWinner(t, res, FamilyImpairment)
		if win.Level != want || latencyOf(win) != fmt.Sprintf("%dms", want) {
			t.Fatalf("with levels %d..10 present the winner is level %d (%s), want level %d", want, win.Level, latencyOf(win), want)
		}
		r := familyResult(t, res, FamilyImpairment)
		if got := len(r.Overridden); got != 10-want {
			t.Fatalf("%d overridden, want %d", got, 10-want)
		}
		for _, o := range r.Overridden {
			if o.Reason != fmt.Sprintf("level %d beats level %d", want, o.Level) {
				t.Errorf("reason for level %d = %q", o.Level, o.Reason)
			}
		}
		// drop the winner
		var rest = tw.overlays[:0]
		for _, o := range tw.overlays {
			if o.Id.String() != ids[want] {
				rest = append(rest, o)
			}
		}
		tw.overlays = rest
	}
}

// ---- the worked examples of plan §2.4 ----------------------------------------------------

func TestE1AnOverlayBeatsAMoreSpecificConfiguredFault(t *testing.T) {
	tw := newTestWorld(t, true) // the example has esp-latency: device A, 20 ms
	off := tw.overlay(`{target: {network: IoT}, fault: {blackout: true}}`, time.Second)
	res := tw.world().Resolve(toServer("tcp", 8883))
	win := mustWinner(t, res, FamilyImpairment)
	if win.ID != off.Id.String() || win.Layer != LayerOverlay || win.Impairment.Blackout == nil || !*win.Impairment.Blackout {
		t.Fatalf("winner = %+v", win)
	}
	r := familyResult(t, res, FamilyImpairment)
	if got := overriddenReason(t, r, idFaultDev); got != "overlay layer wins (D24)" {
		t.Errorf("reason = %q", got)
	}
}

func TestE2TheDeviceFaultBeatsTheNetworkFault(t *testing.T) {
	tw := newTestWorld(t, true)                        // iot-latency 100 ms (network), esp-latency 20 ms (device)
	res := tw.world().Resolve(Query{Source: subjectA}) // A's traffic in general
	win := mustWinner(t, res, FamilyImpairment)
	if win.ID != idFaultDev || win.Level != 4 {
		t.Fatalf("winner = %s level %d", win.ID, win.Level)
	}
	if got := overriddenReason(t, familyResult(t, res, FamilyImpairment), idFaultNet); got != "level 4 beats level 8" {
		t.Errorf("reason = %q", got)
	}
}

func TestE3AndE4TheMoreSpecificOverlayWinsAndTheOtherTrafficKeepsTheGroupFault(t *testing.T) {
	tw := newTestWorld(t, false)
	group := tw.overlay(`{target: {group: sensors}, fault: {latency: 200ms}}`, time.Second)
	port := tw.overlay(`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`, 2*time.Second)
	w := tw.world()

	e3 := mustWinner(t, w.Resolve(toServer("tcp", 8883)), FamilyImpairment)
	if e3.ID != port.Id.String() || lossOf(e3) != "5%" || latencyOf(e3) != "" {
		t.Fatalf("E3: winner %s loss %s latency %s: no merging, only the loss", e3.ID, lossOf(e3), latencyOf(e3))
	}
	ntp := toServer("udp", 123)
	ntp.DestNames = []string{"pool.ntp.org"}
	e4 := mustWinner(t, w.Resolve(ntp), FamilyImpairment)
	if e4.ID != group.Id.String() || latencyOf(e4) != "200ms" {
		t.Fatalf("E4: winner %s latency %s", e4.ID, latencyOf(e4))
	}
}

func TestE5DifferentFamiliesCombine(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, fault: {latency: 100ms}}`, time.Second)
	dns := tw.overlay(`{target: {network: IoT}, profile: dns-broken}`, 2*time.Second)
	q := toServer("tcp", 8883)
	q.DNSName = "broker.example.com"
	res := tw.world().Resolve(q)
	if latencyOf(mustWinner(t, res, FamilyImpairment)) != "100ms" {
		t.Error("the latency must stay")
	}
	d := mustWinner(t, res, FamilyDNS)
	if d.DNS.Action != "servfail" || d.ID != dns.Id.String() || !d.IsProfilePart() || d.ProfileName != "dns-broken" {
		t.Fatalf("dns winner = %+v", d)
	}
}

func TestE6TheNewerOfTwoGroupFaultsWins(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultNet, `{source: {group: sensors}, latency: 30ms}`, 0)
	tw.configFault(idFaultDev, `{source: {group: g2}, latency: 60ms}`, time.Hour)
	res := tw.world().Resolve(toServer("tcp", 443))
	win := mustWinner(t, res, FamilyImpairment)
	if win.ID != idFaultDev || latencyOf(win) != "60ms" {
		t.Fatalf("winner = %s %s", win.ID, latencyOf(win))
	}
	if got := overriddenReason(t, familyResult(t, res, FamilyImpairment), idFaultNet); got != "newer at the same level" {
		t.Errorf("reason = %q", got)
	}
}

func TestE7ADestinationBeatsGlobal(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultAny, `{source: {global: true}, destination: {cidr: 203.0.113.0/24}, loss: 10%}`, 0)
	tw.configFault(idFaultNet, `{source: {global: true}, latency: 5ms}`, time.Hour)
	win := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment)
	if win.ID != idFaultAny || win.Level != 9 || lossOf(win) != "10%" || latencyOf(win) != "" {
		t.Fatalf("winner = %s level %d", win.ID, win.Level)
	}
	// traffic to another destination only gets the global fault
	other := toServer("tcp", 443)
	other.DestIP = netip.MustParseAddr("198.51.100.7")
	if w := mustWinner(t, tw.world().Resolve(other), FamilyImpairment); w.ID != idFaultNet || w.Level != 10 {
		t.Fatalf("winner for the other destination = %s level %d", w.ID, w.Level)
	}
}

func TestE8AFaultBeatsAProfilePartOnTheSameScope(t *testing.T) {
	tw := newTestWorld(t, false)
	// the profile was activated last, so "newer wins" would pick it: the rule is stronger
	fault := tw.overlay(`{target: {device: esp32-42}, fault: {latency: 300ms}}`, time.Second)
	profile := tw.overlay(`{target: {device: esp32-42}, profile: bad-lte}`, time.Minute)
	res := tw.world().Resolve(toServer("tcp", 8883))
	win := mustWinner(t, res, FamilyImpairment)
	if win.ID != fault.Id.String() || latencyOf(win) != "300ms" || win.Impairment.Rate != nil {
		t.Fatalf("winner = %+v", win)
	}
	if got := overriddenReason(t, familyResult(t, res, FamilyImpairment), profile.Id.String()); got != "a fault beats a profile part at the same scope" {
		t.Errorf("reason = %q", got)
	}
}

func TestAProfilePartOnAMoreSpecificScopeBeatsAFault(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {network: IoT}, fault: {latency: 300ms}}`, time.Minute)
	profile := tw.overlay(`{target: {device: esp32-42}, profile: lte}`, time.Second)
	win := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment)
	if win.ID != profile.Id.String() || win.ProfileName != "lte" || latencyOf(win) != "50ms" {
		t.Fatalf("winner = %+v", win)
	}
}

func TestE9AProfileOnANetworkReachesEveryDeviceOfIt(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {network: IoT}, profile: bad-lte}`, time.Second)
	w := tw.world()
	other := Subject{IP: netip.MustParseAddr("10.10.0.77")} // a discovered device, not configured
	for name, s := range map[string]Subject{"A": subjectA, "B": other} {
		win := mustWinner(t, w.Resolve(Query{Source: s}), FamilyImpairment)
		if win.ProfileName != "bad-lte" || deref(win.Impairment.Rate) != "2Mbit" {
			t.Errorf("%s: winner = %+v", name, win)
		}
	}
}

func TestE10ImpairmentAndTunnelFaultsStack(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultDev, `{source: {device: esp32-42}, latency: 40ms}`, 0)
	tun := tw.overlay(`{fault: {family: tunnel, tunnel: {client: lab-rA}, latency: 50ms}}`, time.Second)
	w := tw.world()
	if win := mustWinner(t, w.Resolve(toServer("tcp", 443)), FamilyImpairment); latencyOf(win) != "40ms" {
		t.Fatalf("impairment = %s", latencyOf(win))
	}
	tunnels := w.ResolveTunnels()
	if len(tunnels) != 1 || tunnels[0].Tunnel != "client:"+idClient || tunnels[0].Winner.ID != tun.Id.String() {
		t.Fatalf("tunnels = %+v", tunnels)
	}
}

func TestE11AnAccessRuleDecidesBeforeAnyRedirect(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [8883]}}`, time.Second)
	tls := tw.overlay(`{target: {device: esp32-42}, tls: {case: expired, ports: [8883]}}`, 2*time.Second)
	w := tw.world()
	q := toServer("tcp", 8883)
	if r := w.ResolveAccess(q); !r.Matched || r.Action != "drop" || r.Layer != LayerOverlay {
		t.Fatalf("access = %+v", r)
	}
	// the TLS case is a candidate as well; the compiler puts the access rule first
	if win := mustWinner(t, w.Resolve(q), FamilyTLS); win.ID != tls.Id.String() {
		t.Fatalf("tls winner = %+v", win)
	}
}

func TestE12TheInitiatorsScopeDecides(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {network: IoT}, fault: {latency: 100ms}}`, time.Second)
	w := tw.world()
	// a lab host behind the WireGuard client opens a connection to A: its own scope decides
	lab := Query{Source: Subject{Device: idLab, IP: netip.MustParseAddr("10.50.0.10")}, DestIP: netip.MustParseAddr("10.10.0.42"), Protocol: "tcp", Port: 8883}
	if win := Winner(w.Resolve(lab), FamilyImpairment); win != nil {
		t.Fatalf("the IoT fault must not apply to the lab host's connection: %+v", win)
	}
	// A's own connections do get it
	if win := mustWinner(t, w.Resolve(toServer("tcp", 443)), FamilyImpairment); latencyOf(win) != "100ms" {
		t.Fatal("A must be impaired")
	}
}

// ---- ordering rules -----------------------------------------------------------------------

func TestOverlaysWinWhateverTheLevel(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultDev, `{source: {device: esp32-42}, protocol: tcp, ports: [8883], destination: {hostname: broker.example.com}, latency: 1ms}`, 0)
	o := tw.overlay(`{target: {global: true}, fault: {latency: 99ms}}`, time.Second)
	res := tw.world().Resolve(toServer("tcp", 8883))
	if win := mustWinner(t, res, FamilyImpairment); win.ID != o.Id.String() {
		t.Fatalf("an overlay at level 10 must beat a configured fault at level 1: %+v", win)
	}
}

func TestNewerOverlayAtTheSameLevelWinsAndATieIsBrokenByID(t *testing.T) {
	tw := newTestWorld(t, false)
	old := tw.overlay(`{target: {device: esp32-42}, fault: {latency: 10ms}}`, time.Second)
	newer := tw.overlay(`{target: {device: esp32-42}, fault: {latency: 20ms, jitter: 1ms}}`, 2*time.Second)
	res := tw.world().Resolve(toServer("tcp", 443))
	if win := mustWinner(t, res, FamilyImpairment); win.ID != newer.Id.String() {
		t.Fatalf("winner = %s", win.ID)
	}
	if got := overriddenReason(t, familyResult(t, res, FamilyImpairment), old.Id.String()); got != "newer at the same level" {
		t.Errorf("reason = %q", got)
	}

	tie := newTestWorld(t, false)
	a := tie.overlay(`{target: {device: esp32-42}, fault: {latency: 10ms}}`, time.Second)
	b := tie.overlay(`{target: {device: esp32-42}, fault: {latency: 20ms}}`, time.Second) // same time
	res = tie.world().Resolve(toServer("tcp", 443))
	win := mustWinner(t, res, FamilyImpairment)
	if win.ID != a.Id.String() { // lower id
		t.Fatalf("the lower id must win a tie, got %s (b=%s)", win.ID, b.Id)
	}
	if !strings.Contains(overriddenReason(t, familyResult(t, res, FamilyImpairment), b.Id.String()), "tie") {
		t.Error("the reason must say it was a tie")
	}
}

func TestResolutionIsDeterministicWhateverTheInputOrder(t *testing.T) {
	tw := newTestWorld(t, false)
	for i := 0; i < 6; i++ {
		tw.overlay(fmt.Sprintf(`{target: {device: esp32-42}, fault: {latency: %dms}}`, i+1), time.Duration(i%2)*time.Second)
	}
	first := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment).ID
	for i, j := 0, len(tw.overlays)-1; i < j; i, j = i+1, j-1 {
		tw.overlays[i], tw.overlays[j] = tw.overlays[j], tw.overlays[i]
	}
	if second := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment).ID; second != first {
		t.Fatalf("the winner depends on the order of the overlays: %s vs %s", first, second)
	}
}

func TestParametersAreNeverMerged(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultNet, `{source: {network: IoT}, latency: 100ms, loss: 3%}`, 0)
	tw.configFault(idFaultDev, `{source: {device: esp32-42}, rate: 1Mbit}`, 0)
	win := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment)
	n := convertNetem(*win.Impairment)
	if win.ID != idFaultDev || n.Latency != nil || n.Loss != nil || deref(n.Rate) != "1Mbit" {
		t.Fatalf("the winner must carry its own complete parameter set: %+v", n)
	}
}

func TestADisabledConfiguredFaultIsIgnored(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault(idFaultDev, `{source: {device: esp32-42}, latency: 5ms, enabled: false}`, 0)
	if win := Winner(tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment); win != nil {
		t.Fatalf("winner = %+v", win)
	}
}

func TestOverlayResultsFollowTheOverlayLayerFirst(t *testing.T) {
	tw := newTestWorld(t, true)
	tw.overlay(`{target: {device: esp32-42}, fault: {latency: 7ms}}`, time.Second)
	r := familyResult(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment)
	if len(r.Overridden) < 2 {
		t.Fatalf("overridden = %+v", r.Overridden)
	}
	for _, o := range r.Overridden {
		if o.Layer != LayerConfig {
			t.Errorf("only configured faults lose against the overlay here, got %+v", o)
		}
	}
}

// ---- selectors ----------------------------------------------------------------------------

func TestDestinationSelectors(t *testing.T) {
	tests := []struct {
		name  string
		dest  string
		query func(q Query) Query
		match bool
	}{
		{"uplink: an address outside every network", `{uplink: true}`, func(q Query) Query { return q }, true},
		{"uplink: an address in a test network", `{uplink: true}`, func(q Query) Query { q.DestIP = netip.MustParseAddr("10.10.0.99"); return q }, false},
		{"uplink: an address behind a WireGuard client", `{uplink: true}`, func(q Query) Query { q.DestIP = netip.MustParseAddr("10.50.0.10"); return q }, false},
		{"network: inside", `{network: IoT}`, func(q Query) Query { q.DestIP = netip.MustParseAddr("10.10.0.99"); return q }, true},
		{"network: outside", `{network: IoT}`, func(q Query) Query { return q }, false},
		{"network: a hub includes the networks behind its clients", `{network: lab-hub}`, func(q Query) Query { q.DestIP = netip.MustParseAddr("10.50.0.10"); return q }, true},
		{"cidr: inside", `{cidr: 203.0.113.0/24}`, func(q Query) Query { return q }, true},
		{"cidr: outside", `{cidr: 198.51.100.0/24}`, func(q Query) Query { return q }, false},
		{"cidr: a single address", `{cidr: 203.0.113.10}`, func(q Query) Query { return q }, true},
		{"cidr: another single address", `{cidr: 203.0.113.11}`, func(q Query) Query { return q }, false},
		{"hostname: exact", `{hostname: broker.example.com}`, func(q Query) Query { return q }, true},
		{"hostname: exact, other case", `{hostname: BROKER.example.com}`, func(q Query) Query { return q }, true},
		{"hostname: another name", `{hostname: other.example.com}`, func(q Query) Query { return q }, false},
		{"hostname: wildcard matches a subdomain", `{hostname: '*.example.com'}`, func(q Query) Query { return q }, true},
		{"hostname: wildcard does not match the suffix itself", `{hostname: '*.example.com'}`, func(q Query) Query { q.DestNames = []string{"example.com"}; return q }, false},
		{"hostname: no name resolved", `{hostname: broker.example.com}`, func(q Query) Query { q.DestNames = nil; return q }, false},
		{"a destination selector needs a destination in the query", `{cidr: 203.0.113.0/24}`, func(q Query) Query { q.DestIP = netip.Addr{}; return q }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tw := newTestWorld(t, false)
			tw.overlay(`{target: {device: esp32-42}, fault: {destination: `+tt.dest+`, latency: 5ms}}`, time.Second)
			got := Winner(tw.world().Resolve(tt.query(toServer("tcp", 443))), FamilyImpairment) != nil
			if got != tt.match {
				t.Fatalf("match = %v, want %v", got, tt.match)
			}
		})
	}
}

func TestProtocolAndPortSelectors(t *testing.T) {
	tests := []struct {
		name  string
		sel   string
		proto string
		port  int
		match bool
	}{
		{"tcp matches tcp", `protocol: tcp`, "tcp", 443, true},
		{"tcp does not match udp", `protocol: tcp`, "udp", 443, false},
		{"a protocol needs a protocol in the query", `protocol: tcp`, "", 0, false},
		{"explicit any matches everything", `protocol: any`, "udp", 53, true},
		{"a listed port", `protocol: tcp, ports: [80, 8883]`, "tcp", 8883, true},
		{"an unlisted port", `protocol: tcp, ports: [80, 8883]`, "tcp", 443, false},
		{"a port range, inside", `protocol: udp, port_ranges: [{from: 5000, to: 5010}]`, "udp", 5005, true},
		{"a port range, at its end", `protocol: udp, port_ranges: [{from: 5000, to: 5010}]`, "udp", 5010, true},
		{"a port range, outside", `protocol: udp, port_ranges: [{from: 5000, to: 5010}]`, "udp", 5011, false},
		{"a port selector needs a port in the query", `protocol: tcp, ports: [80]`, "tcp", 0, false},
		{"icmp", `protocol: icmp`, "icmp", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tw := newTestWorld(t, false)
			tw.overlay(`{target: {device: esp32-42}, fault: {`+tt.sel+`, latency: 5ms}}`, time.Second)
			q := toServer(tt.proto, tt.port)
			got := Winner(tw.world().Resolve(q), FamilyImpairment) != nil
			if got != tt.match {
				t.Fatalf("match = %v, want %v", got, tt.match)
			}
		})
	}
}

func TestScopeSelectors(t *testing.T) {
	tests := []struct {
		name   string
		target string
		src    Subject
		match  bool
	}{
		{"device", `{device: esp32-42}`, subjectA, true},
		{"another device", `{device: lab-host}`, subjectA, false},
		{"group member", `{group: sensors}`, subjectA, true},
		{"group, not a member", `{group: sensors}`, Subject{Device: idLab}, false},
		{"network, by configuration", `{network: IoT}`, Subject{Device: idESP}, true},
		{"network, by address of a device nobody configured", `{network: IoT}`, Subject{IP: netip.MustParseAddr("10.10.0.200")}, true},
		{"network, by the network the observed state names", `{network: IoT}`, Subject{Network: idIoT}, true},
		{"network, another one", `{network: IoT}`, Subject{IP: netip.MustParseAddr("10.20.0.5")}, false},
		{"a hub includes its client", `{network: lab-hub}`, Subject{Device: idClient}, true},
		{"a hub includes a host behind a client", `{network: lab-hub}`, Subject{Device: idLab, IP: netip.MustParseAddr("10.50.0.10")}, true},
		{"remote network of a client", `{remote_network: {client: lab-rA}}`, Subject{IP: netip.MustParseAddr("10.50.0.10")}, true},
		{"remote network of a client: the client itself is not behind itself", `{remote_network: {client: lab-rA}}`, Subject{Device: idClient, IP: netip.MustParseAddr("10.99.0.2")}, false},
		{"remote network needs an address", `{remote_network: {client: lab-rA}}`, Subject{Device: idLab}, false},
		{"remote network by prefix", `{remote_network: {cidr: 10.50.0.0/25}}`, Subject{IP: netip.MustParseAddr("10.50.0.10")}, true},
		{"remote network by prefix, outside", `{remote_network: {cidr: 10.50.0.128/25}}`, Subject{IP: netip.MustParseAddr("10.50.0.10")}, false},
		{"global", `{global: true}`, Subject{IP: netip.MustParseAddr("10.10.0.5")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tw := newTestWorld(t, false)
			// remote_network cidr needs a known remote prefix; the example has the client network
			tw.overlay(`{target: `+tt.target+`, fault: {latency: 5ms}}`, time.Second)
			got := Winner(tw.world().Resolve(Query{Source: tt.src}), FamilyImpairment) != nil
			if got != tt.match {
				t.Fatalf("match = %v, want %v", got, tt.match)
			}
		})
	}
}

func TestARemoteNetworkViaALinkRoute(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.cfg.Routing = nil // no dynamic routing: only static link routes are known
	nets := *tw.cfg.Networks
	n := nets[idLink]
	wg, _ := n.AsWireGuardNetwork()
	routes := []string{"10.60.0.0/24"}
	wg.Routes = &routes
	_ = n.FromWireGuardNetwork(wg)
	nets[idLink] = n
	tw.overlay(`{target: {remote_network: {link: site-b}}, fault: {latency: 5ms}}`, time.Second)
	w := tw.world()
	if Winner(w.Resolve(Query{Source: Subject{IP: netip.MustParseAddr("10.60.0.9")}}), FamilyImpairment) == nil {
		t.Error("a host behind the link must match")
	}
	if Winner(w.Resolve(Query{Source: Subject{IP: netip.MustParseAddr("10.61.0.9")}}), FamilyImpairment) != nil {
		t.Error("an address outside the link's routes must not match")
	}
}

func TestADiscoveredDeviceCanBeTargetedByItsUUID(t *testing.T) {
	tw := newTestWorld(t, false)
	// ValidateOverlay refuses an unknown device, so build the overlay directly: discovered
	// devices are not in the configuration but have UUIDs of their own (plan §2.1.1)
	discovered := "11111111-2222-4333-8444-555555555555"
	o := tw.overlay(`{target: {global: true}, fault: {latency: 5ms}}`, time.Second)
	dev := discovered
	o.Target = &model_scope{Device: &dev}
	tw.overlays[0] = o
	w := tw.world()
	if Winner(w.Resolve(Query{Source: Subject{Device: discovered}}), FamilyImpairment) == nil {
		t.Error("the discovered device must match its own overlay")
	}
	if Winner(w.Resolve(Query{Source: subjectA}), FamilyImpairment) != nil {
		t.Error("another device must not match")
	}
}
