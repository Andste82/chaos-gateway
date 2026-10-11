package domain

import (
	"math/rand"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

// bruteCoverage counts the cells of a source's table by asking Resolve for one traffic of every cell:
// the pieces every destination and protocol/port selector cuts, plus the addresses and the protocols
// that no selector names.
func bruteCoverage(w *World, src Source, family string) map[string]Cover {
	var spans []span
	var sels []PortSel
	var cands []Candidate
	for _, c := range w.candidates() {
		if c.Family != family || !w.scopeMatches(c.Scope, src.Subject) {
			continue
		}
		if c.Match.Destination != nil {
			s, ok := w.destSpans(c.Match.Destination)
			if !ok {
				continue // a hostname: not part of the table
			}
			spans = append(spans, s...)
		}
		s, _ := portSels(c.Match)
		sels = append(sels, s...)
		cands = append(cands, c)
	}
	var dests []netip.Addr
	for _, p := range elementary(spans, addrSpace) {
		dests = append(dests, numAddr(p.lo))
	}
	if rest := complementSpans(spans); len(rest) > 0 {
		dests = append(dests, numAddr(rest[0].lo))
	}
	type traffic struct {
		proto string
		port  int
	}
	var ports []traffic
	for _, p := range portPieces(sels) {
		ports = append(ports, traffic{p.Proto, p.From})
	}
	ports = append(ports, traffic{"gre", 0}) // no selector names it
	out := map[string]Cover{}
	for _, d := range dests {
		for _, p := range ports {
			q := Query{Source: src.Subject, DestIP: d, Protocol: p.proto, Port: p.port}
			win := Winner(w.Resolve(q), family)
			for _, c := range cands {
				if !w.candidateMatches(c, q) {
					continue
				}
				x := out[c.Key()]
				x.Matched++
				if win != nil && win.Key() == c.Key() {
					x.Won++
				}
				out[c.Key()] = x
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// The coverage of a table is exactly what Resolve gives cell by cell, for random worlds of faults on
// every kind of scope and selector, both layers, profiles and both families of a table.
func TestTheCoverageOfATableIsWhatResolveSaysCellByCell(t *testing.T) {
	subjects := map[string]Subject{
		"device A":                 subjectA,
		"lab host (device)":        {Device: idLab, IP: netip.MustParseAddr("10.50.0.10")},
		"unknown IoT host":         {IP: netip.MustParseAddr("10.10.0.77")},
		"host behind IoT's router": {IP: netip.MustParseAddr("10.30.0.5")},
		"client network":           {IP: netip.MustParseAddr("10.50.0.5")},
	}
	rng := rand.New(rand.NewSource(20261011))
	partial := 0
	for round := 0; round < 50; round++ {
		w := randomWorld(t, rng)
		for name, sub := range subjects {
			src := Source{Subject: sub, Device: sub.Device}
			for _, family := range TableFamilies {
				tab, err := w.Table(src, family)
				if err != nil {
					t.Fatal(err)
				}
				want := bruteCoverage(w, src, family)
				if !reflect.DeepEqual(tab.Coverage, want) {
					t.Fatalf("round %d, %s, %s:\ncoverage %v\nResolve  %v\nentries:\n  %v", round, name, family, tab.Coverage, want, entries(tab, nil))
				}
				for _, c := range tab.Coverage {
					if c.Won > 0 && c.Won < c.Matched {
						partial++
					}
				}
			}
		}
	}
	if partial == 0 {
		t.Fatal("no candidate won only a part of its traffic: the worlds test nothing")
	}
	t.Logf("%d partial covers", partial)
}

// A device fault that names a port takes that port from the network fault: the network fault wins the
// rest of the device's traffic, the device fault all of its own. A device fault without a selector
// takes all of it.
func TestAFaultThatIsBeatenOnAPartOfItsTrafficCoversOnlyThatPart(t *testing.T) {
	tw := newTestWorld(t, false)
	network := tw.overlay(`{target: {network: IoT}, fault: {latency: 100ms}}`, time.Second)
	port := tw.overlay(`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`, 2*time.Second)
	tab := tableOf(t, tw, sourceA, FamilyImpairment)
	n, p := tab.Coverage["overlay:"+network.Id.String()+":impairment"], tab.Coverage["overlay:"+port.Id.String()+":impairment"]
	if n.Won == 0 || n.Won >= n.Matched {
		t.Errorf("the network fault wins a part of A's traffic: %+v", n)
	}
	if p.Matched == 0 || p.Won != p.Matched {
		t.Errorf("the port fault wins all of its traffic: %+v", p)
	}

	all := tw.overlay(`{target: {device: esp32-42}, fault: {latency: 300ms}}`, 3*time.Second)
	tab = tableOf(t, tw, sourceA, FamilyImpairment)
	if n := tab.Coverage["overlay:"+network.Id.String()+":impairment"]; n.Matched == 0 || n.Won != 0 {
		t.Errorf("the network fault wins nothing of A's traffic: %+v", n)
	}
	if a := tab.Coverage["overlay:"+all.Id.String()+":impairment"]; a.Matched == 0 || a.Won >= a.Matched || a.Won == 0 {
		t.Errorf("the device fault wins all but the port: %+v", a)
	}
	// another device of the network: only the network fault applies
	other := Source{Subject: Subject{IP: netip.MustParseAddr("10.10.0.77")}}
	tab = tableOf(t, tw, other, FamilyImpairment)
	if len(tab.Coverage) != 1 {
		t.Fatalf("%v", tab.Coverage)
	}
	if n := tab.Coverage["overlay:"+network.Id.String()+":impairment"]; n.Matched == 0 || n.Won != n.Matched {
		t.Errorf("the network fault wins all of the other host's traffic: %+v", n)
	}
}

// Hand-made worlds the random ones rarely build: candidates that the build drops before it looks at a
// cell (an older fault of the same scope and selector), and destination selectors that cover every
// address (no traffic falls through to the faults without a destination).
func TestTheCoverageCountsDroppedCandidatesAndAFullDestinationCover(t *testing.T) {
	for name, overlays := range map[string][]string{
		"same port twice": {
			`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`,
			`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 6%}}`,
			`{target: {network: IoT}, fault: {latency: 100ms}}`,
		},
		"same destination twice": {
			`{target: {device: esp32-42}, fault: {destination: {cidr: 203.0.113.0/24}, loss: 5%}}`,
			`{target: {device: esp32-42}, fault: {destination: {cidr: 203.0.113.0/24}, loss: 6%}}`,
			`{target: {global: true}, fault: {protocol: udp, latency: 10ms}}`,
		},
		"same scope twice": {
			`{target: {network: IoT}, fault: {latency: 100ms}}`,
			`{target: {network: IoT}, fault: {latency: 200ms}}`,
			`{target: {device: esp32-42}, fault: {destination: {cidr: 203.0.113.0/24}, protocol: tcp, ports: [443], loss: 5%}}`,
		},
		"every address": {
			`{target: {global: true}, fault: {latency: 5ms}}`,
			`{target: {device: esp32-42}, fault: {destination: {cidr: 0.0.0.0/1}, loss: 1%}}`,
			`{target: {device: esp32-42}, fault: {destination: {cidr: 128.0.0.0/1}, loss: 2%}}`,
			`{target: {device: esp32-42}, fault: {destination: {cidr: 203.0.113.0/24}, protocol: tcp, ports: [443], loss: 3%}}`,
		},
	} {
		tw := newTestWorld(t, false)
		for i, o := range overlays {
			tw.overlay(o, time.Duration(i+1)*time.Second)
		}
		tab := tableOf(t, tw, sourceA, FamilyImpairment)
		if want := bruteCoverage(tw.world(), sourceA, FamilyImpairment); !reflect.DeepEqual(tab.Coverage, want) {
			t.Errorf("%s:\ncoverage %v\nResolve  %v", name, tab.Coverage, want)
		}
		if name == "every address" {
			// the global fault has the level-4 entry, but no traffic of A reaches it
			global := tw.overlays[0].Id.String()
			if c := tab.Coverage["overlay:"+global+":impairment"]; c.Matched == 0 || c.Won != 0 {
				t.Errorf("the global fault wins nothing of A's traffic: %+v (entries %v)", c, entries(tab, nil))
			}
		}
	}
}
