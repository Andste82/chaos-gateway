package domain

import (
	"fmt"
	"math/rand"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func tableOf(t *testing.T, tw *testWorld, src Source, family string) Table {
	t.Helper()
	tab, err := tw.world().Table(src, family)
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

var sourceA = Source{Subject: subjectA, Device: idESP, Addrs: []netip.Addr{subjectA.IP}}

// entries prints a table with the winners named by the ids of the overlays and faults given.
func entries(tab Table, names map[string]string) []string {
	var out []string
	for _, e := range tab.Entries {
		s := e.String()
		for id, name := range names {
			s = strings.ReplaceAll(s, id, name)
		}
		out = append(out, s)
	}
	return out
}

func wantEntries(t *testing.T, tab Table, names map[string]string, want ...string) {
	t.Helper()
	got := entries(tab, names)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// ---- the worked examples as lookup entries ------------------------------------------------

func TestTableE1TheOverlayTakesTheDeviceFaultsPlace(t *testing.T) {
	tw := newTestWorld(t, true) // esp-latency (device, 20 ms) and iot-latency are configured
	off := tw.overlay(`{target: {network: IoT}, fault: {blackout: true}}`, time.Second)
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	// the configured device fault (level 4) never shows: the overlay wins for all of A's traffic
	wantEntries(t, tab, map[string]string{off.Id.String(): "offline"}, "L4 * -> overlay:offline")
}

func TestTableE2TheDeviceFaultIsTheDefaultOfTheDevice(t *testing.T) {
	tw := newTestWorld(t, true)
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	wantEntries(t, tab, map[string]string{idFaultDev: "device", idFaultNet: "network"}, "L4 * -> config:device")
	// a device that is only in the network gets the network fault
	other := Source{Subject: Subject{IP: netip.MustParseAddr("10.10.0.77")}}
	tab = tableOf(t, tw, other, FamilyImpairment)
	wantEntries(t, tab, map[string]string{idFaultDev: "device", idFaultNet: "network"}, "L4 * -> config:network")
}

func TestTableE3AndE4ThePortFaultWinsOnItsPortAndTheGroupFaultOnTheRest(t *testing.T) {
	tw := newTestWorld(t, false)
	group := tw.overlay(`{target: {group: sensors}, fault: {latency: 200ms}}`, time.Second)
	port := tw.overlay(`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`, 2*time.Second)
	names := map[string]string{group.Id.String(): "group", port.Id.String(): "port"}
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	wantEntries(t, tab, names, "L3 tcp/8883 -> overlay:port", "L4 * -> overlay:group")

	e3 := toServer("tcp", 8883)
	if w, ok := tab.Lookup(e3); !ok || w.ID != port.Id.String() {
		t.Fatalf("E3: %v %v", w, ok)
	}
	ntp := toServer("udp", 123)
	if w, ok := tab.Lookup(ntp); !ok || w.ID != group.Id.String() {
		t.Fatalf("E4: %v %v", w, ok)
	}
}

func TestTableE6TheNewerGroupFaultIsTheDefault(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault("00000000-0000-4000-8000-0000000000c1", `{source: {group: sensors}, latency: 30ms}`, 0)
	tw.configFault("00000000-0000-4000-8000-0000000000c2", `{source: {group: g2}, latency: 60ms}`, time.Second)
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	wantEntries(t, tab, nil, "L4 * -> config:00000000-0000-4000-8000-0000000000c2")
}

func TestTableE7ADestinationBeatsGlobal(t *testing.T) {
	tw := newTestWorld(t, true)
	tab := tableOf(t, tw, Source{Subject: Subject{IP: netip.MustParseAddr("10.10.0.77")}}, FamilyImpairment)
	names := map[string]string{idFaultNet: "network"}
	// the network fault (100 ms) is level 8, the destination fault (level 9, global, 203.0.113.0/24)
	// is less specific: the network fault wins everywhere, so the table has no entry for the range
	wantEntries(t, tab, names, "L4 * -> config:network")

	// without the network fault the destination fault has its range and global 5 ms the rest
	tw2 := newTestWorld(t, false)
	loss := "00000000-0000-4000-8000-0000000000d1"
	five := "00000000-0000-4000-8000-0000000000d2"
	tw2.configFault(loss, `{source: {global: true}, destination: {cidr: 203.0.113.0/24}, loss: 10%}`, 0)
	tw2.configFault(five, `{source: {global: true}, latency: 5ms}`, 0)
	tab = tableOf(t, tw2, sourceA, FamilyImpairment)
	wantEntries(t, tab, map[string]string{loss: "loss", five: "five"}, "L2 203.0.113.0-203.0.113.255 -> config:loss", "L4 * -> config:five")
	if w, _ := tab.Lookup(toServer("tcp", 443)); w.ID != loss {
		t.Fatalf("A -> 203.0.113.10 = %v", w)
	}
}

func TestTableE8AFaultBeatsTheProfilePartOfItsScope(t *testing.T) {
	tw := newTestWorld(t, false)
	profile := tw.overlay(`{target: {device: esp32-42}, profile: bad-lte}`, time.Second)
	fault := tw.overlay(`{target: {device: esp32-42}, fault: {latency: 300ms}}`, 2*time.Second)
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	wantEntries(t, tab, map[string]string{profile.Id.String(): "bad-lte", fault.Id.String(): "latency"}, "L4 * -> overlay:latency")
}

func TestTableE12TheLabHostIsNotImpairedByTheIoTFault(t *testing.T) {
	tw := newTestWorld(t, false)
	iot := tw.overlay(`{target: {network: IoT}, fault: {latency: 100ms}}`, time.Second)
	lab := Source{Subject: Subject{Device: idLab, IP: netip.MustParseAddr("10.50.0.10")}, Device: idLab}
	if tab := tableOf(t, tw, lab, FamilyImpairment); len(tab.Entries) != 0 {
		t.Fatalf("the lab host has entries: %v", entries(tab, nil))
	}
	wantEntries(t, tableOf(t, tw, sourceA, FamilyImpairment), map[string]string{iot.Id.String(): "iot"}, "L4 * -> overlay:iot")
}

// ---- the shape of the entries ---------------------------------------------------------------

func TestAnOverlayOverridesAMoreSpecificConfiguredFaultAtItsKeys(t *testing.T) {
	tw := newTestWorld(t, false)
	cfgPort := "00000000-0000-4000-8000-0000000000e1"
	tw.configFault(cfgPort, `{source: {device: esp32-42}, protocol: tcp, ports: [8883], destination: {cidr: 203.0.113.0/24}, latency: 1ms}`, 0)
	slow := tw.overlay(`{target: {network: IoT}, fault: {latency: 500ms}}`, time.Second)
	// the overlay wins everywhere, so the configured fault leaves no entry at all
	wantEntries(t, tableOf(t, tw, sourceA, FamilyImpairment), map[string]string{slow.Id.String(): "slow"}, "L4 * -> overlay:slow")

	// an overlay that names only a destination wins there; the configured fault keeps the rest
	dest := tw.overlay(`{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/25}, latency: 9ms}}`, 2*time.Second)
	tw.overlays = tw.overlays[1:] // drop the network overlay
	names := map[string]string{cfgPort: "cfg", dest.Id.String(): "dest"}
	wantEntries(t, tableOf(t, tw, sourceA, FamilyImpairment), names,
		"L1 203.0.113.128-203.0.113.255 tcp/8883 -> config:cfg", // the half of the range that dest does not cover
		"L2 203.0.113.0-203.0.113.127 -> overlay:dest",
	)
}

func TestUplinkIsEverythingThatNoNetworkOwns(t *testing.T) {
	tw := newTestWorld(t, false)
	up := tw.overlay(`{target: {device: esp32-42}, fault: {destination: {uplink: true}, latency: 40ms}}`, time.Second)
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	var covered []string
	for _, e := range tab.Entries {
		if e.Level != 2 || e.Winner.ID != up.Id.String() {
			t.Fatalf("entry %v", e)
		}
		covered = append(covered, e.Dest.String())
	}
	// the networks of the example (10.10.0.0/24, 10.99.0.0/24, 10.255.0.0/31, the client network
	// 10.50.0.0/24) and the management network are not uplink traffic
	for _, ip := range []string{"10.10.0.5", "10.99.0.2", "10.255.0.1", "10.50.0.10", "192.168.88.7"} {
		q := Query{Source: subjectA, DestIP: netip.MustParseAddr(ip)}
		if w, ok := tab.Lookup(q); ok && w != nil {
			t.Errorf("%s is not uplink traffic but matches: %v", ip, covered)
		}
	}
	for _, ip := range []string{"203.0.113.10", "8.8.8.8", "0.0.0.0", "255.255.255.255"} {
		q := Query{Source: subjectA, DestIP: netip.MustParseAddr(ip)}
		if w, ok := tab.Lookup(q); !ok || w == nil || w.ID != up.Id.String() {
			t.Errorf("%s is uplink traffic: %v", ip, w)
		}
	}
}

func TestPortAndProtocolPiecesAreDisjointAndMerged(t *testing.T) {
	tw := newTestWorld(t, false)
	a := tw.overlay(`{target: {device: esp32-42}, fault: {protocol: tcp, port_ranges: [{from: 1000, to: 2000}], latency: 1ms}}`, time.Second)
	b := tw.overlay(`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [1500], latency: 2ms}}`, 2*time.Second)
	ic := tw.overlay(`{target: {device: esp32-42}, fault: {protocol: icmp, latency: 3ms}}`, 3*time.Second)
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	wantEntries(t, tab, map[string]string{a.Id.String(): "a", b.Id.String(): "b", ic.Id.String(): "icmp"},
		"L3 tcp/1000-1499 -> overlay:a", "L3 tcp/1500 -> overlay:b", "L3 tcp/1501-2000 -> overlay:a", "L3 icmp -> overlay:icmp")
}

func TestHostnameFaultsAreLeftForTheDNSSets(t *testing.T) {
	tw := newTestWorld(t, true) // small-mtu names broker.example.com
	tab := tableOf(t, tw, sourceA, FamilyMTU)
	if len(tab.Entries) != 0 || len(tab.Unresolved) != 1 || tab.Unresolved[0].Name != "small-mtu" {
		t.Fatalf("entries %v unresolved %+v", entries(tab, nil), tab.Unresolved)
	}
}

func TestOnlyFamiliesSelectedByDestinationAndPortHaveATable(t *testing.T) {
	tw := newTestWorld(t, false)
	for _, f := range []string{FamilyDNS, FamilyTLS, FamilyDHCP, FamilyTunnel} {
		if _, err := tw.world().Table(sourceA, f); err == nil {
			t.Errorf("%s: no error", f)
		}
	}
}

func TestEqualTablesHaveEqualSignatures(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {network: IoT}, fault: {protocol: tcp, ports: [8883], latency: 10ms}}`, time.Second)
	a := tableOf(t, tw, Source{Subject: Subject{IP: netip.MustParseAddr("10.10.0.77")}}, FamilyImpairment)
	b := tableOf(t, tw, Source{Subject: Subject{IP: netip.MustParseAddr("10.10.0.78")}}, FamilyImpairment)
	c := tableOf(t, tw, Source{Subject: Subject{IP: netip.MustParseAddr("10.99.0.50")}}, FamilyImpairment)
	if a.Signature() != b.Signature() || a.Signature() == "" || c.Signature() != "" {
		t.Fatalf("signatures %q %q %q", a.Signature(), b.Signature(), c.Signature())
	}
}

func TestIPRangePrefixes(t *testing.T) {
	cases := map[string][]string{
		"10.0.0.0-10.0.0.255":         {"10.0.0.0/24"},
		"10.0.0.1-10.0.0.6":           {"10.0.0.1/32", "10.0.0.2/31", "10.0.0.4/31", "10.0.0.6/32"},
		"0.0.0.0-255.255.255.255":     {"0.0.0.0/0"},
		"203.0.113.128-203.0.113.255": {"203.0.113.128/25"},
		"10.255.0.1-10.255.0.1":       {"10.255.0.1/32"},
	}
	for in, want := range cases {
		lo, hi, _ := strings.Cut(in, "-")
		r := IPRange{netip.MustParseAddr(lo), netip.MustParseAddr(hi)}
		var got []string
		for _, p := range r.Prefixes() {
			got = append(got, p.String())
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: %v, want %v", in, got, want)
		}
	}
}

// ---- the sources ---------------------------------------------------------------------------

func TestSourcesAreTheDevicesAndTheStretchesNoDeviceOwns(t *testing.T) {
	tw := newTestWorld(t, false)
	w := tw.world()
	id := ResolveIdentity(tw.cfg, Observed{Neighbors: []Neighbor{{IP: netip.MustParseAddr("10.10.0.42"), MAC: "24:0a:c4:00:00:42", Network: idIoT}}}, nil)
	srcs := w.Sources(id)
	var devices, stretches []Source
	for _, s := range srcs {
		if s.Device != "" {
			devices = append(devices, s)
		} else {
			stretches = append(stretches, s)
		}
	}
	var found bool
	for _, d := range devices {
		if d.Device == idESP {
			found = true
			if len(d.Addrs) != 1 || d.Addrs[0] != netip.MustParseAddr("10.10.0.42") || d.Subject.IP != d.Addrs[0] {
				t.Errorf("esp32-42: %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("esp32-42 is not a source: %+v", devices)
	}
	// every network prefix is covered by the stretches: 10.10.0.0/24, 10.99.0.0/24, 10.255.0.0/31, 10.50.0.0/24
	covered := func(ip string) bool {
		a := netip.MustParseAddr(ip)
		for _, s := range stretches {
			for _, r := range s.Ranges {
				if r.Contains(a) {
					return true
				}
			}
		}
		return false
	}
	for _, ip := range []string{"10.10.0.1", "10.10.0.254", "10.99.0.77", "10.255.0.1", "10.50.0.200"} {
		if !covered(ip) {
			t.Errorf("%s is in no stretch", ip)
		}
	}
	for _, ip := range []string{"203.0.113.10", "192.168.88.1", "10.11.0.1"} {
		if covered(ip) {
			t.Errorf("%s is covered but belongs to no network", ip)
		}
	}
}

func TestARemoteNetworkThatAFaultNamesCutsItsOwnStretch(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {remote_network: {cidr: 10.50.0.0/28}}, fault: {latency: 5ms}}`, time.Second)
	srcs := tw.world().Sources(Identity{})
	var cut bool
	for _, s := range srcs {
		if len(s.Ranges) == 1 && s.Ranges[0].First == netip.MustParseAddr("10.50.0.0") && s.Ranges[0].Last == netip.MustParseAddr("10.50.0.15") {
			cut = true
			if w, _ := tableOf(t, tw, s, FamilyImpairment).Lookup(Query{}); w == nil {
				t.Error("the remote network's stretch must carry the fault")
			}
		}
	}
	if !cut {
		t.Fatalf("no stretch for the remote network: %+v", srcs)
	}
}

func TestADeviceInTwoNetworksIsTwoSources(t *testing.T) {
	tw := newTestWorld(t, false)
	w := tw.world()
	id := Identity{Addresses: map[string][]netip.Addr{
		idESP: {netip.MustParseAddr("10.10.0.42"), netip.MustParseAddr("10.10.0.43"), netip.MustParseAddr("10.99.0.9")},
	}}
	var got []Source
	for _, s := range w.Sources(id) {
		if s.Device == idESP {
			got = append(got, s)
		}
	}
	if len(got) != 2 || len(got[0].Addrs)+len(got[1].Addrs) != 3 {
		t.Fatalf("sources = %+v", got)
	}
}

// ---- the property: the first-match chain gives what Resolve says --------------------------

var propDests = []string{
	"", "203.0.113.0/24", "203.0.113.10", "203.0.0.0/16", "203.0.113.128/25", "10.10.0.0/16", "0.0.0.0/0", "10.0.0.0/8",
	"192.168.88.0/24", "10.99.0.0/24", "10.50.0.0/28", "8.8.8.8",
}

var propScopes = []string{
	`{device: esp32-42}`, `{group: sensors}`, `{group: g2}`, `{network: IoT}`, `{network: lab-hub}`, `{global: true}`,
	`{remote_network: {client: lab-rA}}`, `{remote_network: {cidr: 10.50.0.0/28}}`, `{device: lab-host}`,
}

// randomFault builds a body of a fault: a random destination and protocol/port selector and a
// latency that identifies it.
func randomFault(rng *rand.Rand, n int) map[string]any {
	body := map[string]any{"latency": fmt.Sprintf("%dms", n+1)}
	switch d := propDests[rng.Intn(len(propDests))]; {
	case d == "":
	case rng.Intn(6) == 0:
		body["destination"] = map[string]any{"uplink": true}
	case rng.Intn(8) == 0:
		body["destination"] = map[string]any{"network": []string{"IoT", "lab-hub"}[rng.Intn(2)]}
	case rng.Intn(10) == 0:
		body["destination"] = map[string]any{"hostname": "broker.example.com"}
	default:
		body["destination"] = map[string]any{"cidr": d}
	}
	switch rng.Intn(6) {
	case 0:
		body["protocol"] = "tcp"
	case 1:
		body["protocol"] = "udp"
	case 2:
		body["protocol"] = "icmp"
	case 3:
		body["protocol"] = "tcp"
		body["ports"] = [][]int{{8883}, {443, 8883}, {53}, {1500}}[rng.Intn(4)]
	case 4:
		body["protocol"] = []string{"tcp", "udp"}[rng.Intn(2)]
		body["port_ranges"] = []map[string]int{{"from": 1000, "to": 2000}, {"from": 1, "to": 1023}, {"from": 8000, "to": 9000}}[rng.Intn(3):][:1]
	}
	return body
}

func randomWorld(t *testing.T, rng *rand.Rand) *World {
	t.Helper()
	cfg, errs := Normalize(exampleConfiguration(t))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	faults := map[string]model.ConfigFault{}
	cfg.Faults = &faults
	scopeIDs := map[string]string{}
	for k, v := range map[string]string{"esp32-42": idESP, "sensors": idSensors, "g2": idG2, "IoT": idIoT, "lab-hub": idHub, "lab-rA": "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "lab-host": idLab} {
		scopeIDs[k] = v
	}
	resolveScope := func(s string) model.Scope {
		for name, id := range scopeIDs {
			s = strings.ReplaceAll(s, name, id)
		}
		doc, err := ParseDocument([]byte(s), FormatYAML)
		if err != nil {
			t.Fatal(err)
		}
		var sc model.Scope
		if err := decodeInto(doc, &sc); err != nil {
			t.Fatal(err)
		}
		return sc
	}
	resolveBody := func(body map[string]any) map[string]any {
		if d, ok := body["destination"].(map[string]any); ok {
			if n, ok := d["network"].(string); ok {
				body["destination"] = map[string]any{"network": scopeIDs[n]}
			}
		}
		return body
	}
	var overlays []model.Overlay
	n := 0
	for i, count := 0, 2+rng.Intn(7); i < count; i++ {
		n++
		scope := resolveScope(propScopes[rng.Intn(len(propScopes))])
		since := t0.Add(time.Duration(rng.Intn(6)) * time.Second) // collisions on purpose: ties are broken by id
		body := resolveBody(randomFault(rng, n))
		if rng.Intn(5) == 0 {
			body["family"] = "mtu"
			delete(body, "latency")
			body["mtu"] = map[string]any{"size": 1200 + n}
		}
		id := mustUUID("00000000-0000-4000-8000-" + padHex(n))
		layer := rng.Intn(3)
		if layer == 0 { // configured
			faults[id.String()] = convert[model.ConfigFault](mergeMaps(body, map[string]any{"source": scope, "created_at": since}))
			continue
		}
		var req model.OverlayRequest
		if layer == 1 && rng.Intn(4) == 0 {
			profile := []string{"ad24af2d-f184-5d04-b8c4-01f24c86ddcf", "39c3e0cb-b84a-5104-8954-c629612acf8e"}[rng.Intn(2)]
			req = model.OverlayRequest{Target: &scope, Profile: &profile}
		} else {
			req = convert[model.OverlayRequest](map[string]any{"target": scope, "fault": body})
		}
		o, err := NewOverlay(req, model.Owner{Type: "user", Id: "admin"}, id, since)
		if err != nil {
			t.Fatal(err)
		}
		overlays = append(overlays, o)
	}
	w, err := NewWorld(cfg, overlays)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

var propIPs = []string{
	"0.0.0.0", "8.8.8.8", "10.0.0.1", "10.10.0.5", "10.10.1.1", "10.11.0.1", "10.50.0.5", "10.50.0.15", "10.50.0.16", "10.50.0.200",
	"10.99.0.2", "10.255.0.1", "10.255.0.3", "192.168.88.7", "203.0.0.1", "203.0.112.255", "203.0.113.0", "203.0.113.10", "203.0.113.127",
	"203.0.113.128", "203.0.113.255", "203.0.114.0", "203.1.0.0", "255.255.255.255",
}

var propTraffic = []struct {
	proto string
	ports []int
}{
	{"tcp", []int{1, 53, 443, 999, 1000, 1500, 1501, 2000, 2001, 8000, 8883, 9000, 9001, 65535}},
	{"udp", []int{1, 53, 123, 1000, 1500, 2000, 8000, 9001}},
	{"icmp", []int{0}},
	{"", []int{0}},
	{"gre", []int{0}},
}

func TestTheLookupChainGivesWhatResolveSays(t *testing.T) {
	subjects := map[string]Subject{
		"device A":           subjectA,
		"lab host (device)":  {Device: idLab, IP: netip.MustParseAddr("10.50.0.10")},
		"unknown IoT host":   {IP: netip.MustParseAddr("10.10.0.77")},
		"client network":     {IP: netip.MustParseAddr("10.50.0.5")},
		"client network far": {IP: netip.MustParseAddr("10.50.0.99")},
		"hub address":        {IP: netip.MustParseAddr("10.99.0.50")},
	}
	rng := rand.New(rand.NewSource(20261007))
	checked := 0
	for round := 0; round < 60; round++ {
		w := randomWorld(t, rng)
		for name, sub := range subjects {
			src := Source{Subject: sub, Device: sub.Device}
			for _, family := range TableFamilies {
				tab, err := w.Table(src, family)
				if err != nil {
					t.Fatal(err)
				}
				for _, ip := range propIPs {
					for _, tr := range propTraffic {
						for _, port := range tr.ports {
							q := Query{Source: sub, DestIP: netip.MustParseAddr(ip), Protocol: tr.proto, Port: port}
							want := Winner(w.Resolve(q), family)
							got, _ := tab.Lookup(q)
							checked++
							if (want == nil) != (got == nil) || (want != nil && (want.ID != got.ID || want.Layer != got.Layer || want.Family != got.Family)) {
								t.Fatalf("round %d, %s, %s, traffic %s %s:%d\nResolve: %s\nLookup:  %s\nentries:\n  %s",
									round, name, family, ip, tr.proto, port, winnerName(want), winnerName(got), strings.Join(entries(tab, nil), "\n  "))
							}
						}
					}
				}
			}
		}
	}
	t.Logf("%d lookups compared", checked)
}
