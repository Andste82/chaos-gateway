package compiler

import (
	"strings"
	"testing"
	"time"
)

// The DNS family (plan §2.4, §2.6, E5): the faults the proxy answers with, in the order the precedence rules try them.

func dnsEntries(tg *Target) (out []string) {
	for _, f := range tg.DNSFaults {
		var s []string
		for _, p := range f.Sources {
			s = append(s, p.String())
		}
		names := "*"
		if f.Fault.Names != nil {
			names = strings.Join(*f.Fault.Names, ",")
		}
		out = append(out, string(f.Fault.Action)+" "+names+" -> "+strings.Join(s, ","))
	}
	return out
}

func TestADNSFaultIsAnEntryForTheClientsOfItsScope(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, dns: {names: [broker.example.com], action: nxdomain}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if got := dnsEntries(tg); len(got) != 1 || got[0] != "nxdomain broker.example.com -> 10.10.0.42/32" {
		t.Fatalf("%v", got)
	}
	f := tg.DNSFaults[0]
	if f.Overlay != o.Id.String() || f.Key != "overlay:"+o.Id.String()+":dns" || f.Scope != "device esp32-42" {
		t.Errorf("%+v", f)
	}
	// nothing of it is in the kernel: no set, no rule, no id
	if len(tg.Faults) != 0 || len(tg.DNSSets) != 0 {
		t.Errorf("%+v %+v", tg.Faults, tg.DNSSets)
	}
	// it is a winner: the preview lists it and the overlay is `effective`
	if len(tg.Winners) != 1 || tg.Winners[0] != f.Key || len(tg.Effective) != 1 || tg.Effective[0].Family != "dns" || tg.Effective[0].Summary != "dns nxdomain" {
		t.Errorf("%v %+v", tg.Winners, tg.Effective)
	}
}

// E5: a device latency fault does not cancel a "DNS broken" profile on the network; the families do not compete.
func TestADeviceLatencyAndADNSBrokenProfileOnTheNetworkCombine(t *testing.T) {
	w := newFaultWorld(t)
	lat := w.overlay(`{target: {device: esp32-42}, fault: {latency: 100ms}}`, 0)
	prof := w.overlay(`{target: {network: IoT}, profile: dns-broken}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.Faults) != 1 || tg.Faults[0].Source != lat.Id.String() {
		t.Errorf("the latency fault: %+v", tg.Faults)
	}
	got := dnsEntries(tg)
	if len(got) != 1 || !strings.HasPrefix(got[0], "servfail * -> ") || !strings.Contains(got[0], "10.10.0.42/32") && !strings.Contains(got[0], "10.10.0.0/24") {
		t.Fatalf("%v", got)
	}
	if f := tg.DNSFaults[0]; f.Overlay != prof.Id.String() || f.Profile != "dns-broken" {
		t.Errorf("%+v", f)
	}
}

func TestSourcesWithTheSameFaultsShareTheirEntriesAndTheOrderIsTheRank(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, dns: {action: servfail}}`, 0)
	w.overlay(`{target: {device: esp32-42}, dns: {names: [a.example.com], action: nxdomain}}`, time.Second)
	w.overlay(`{target: {group: sensors}, dns: {names: ["*.example.org"], action: timeout}}`, 2*time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	got := dnsEntries(tg)
	// esp32-42 is in IoT and in the group: device + names (2), group + names (5), then the network (8);
	// esp32-43 is in the group and the network; the addresses no device owns only have the network's
	want := []string{
		"nxdomain a.example.com -> 10.10.0.42/32",
		"timeout *.example.org -> 10.10.0.42/32",
		"servfail * -> 10.10.0.42/32",
		"timeout *.example.org -> 10.10.0.43/32",
		"servfail * -> 10.10.0.43/32",
	}
	if len(got) < len(want) {
		t.Fatalf("%v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("entry %d = %q, want %q (all: %v)", i, got[i], w, got)
		}
	}
	// the stretches of the network that no device owns: the network's fault alone, in one entry for all of them
	rest := append([]string(nil), got[len(want):]...)
	if len(rest) != 1 || !strings.HasPrefix(rest[0], "servfail * -> 10.10.0.0/24") {
		t.Errorf("%v", rest)
	}
}

// At the same level the newer fault comes first (D26), an overlay before any configuration, and a fault before a part of a profile on its
// own scope.
func TestTheNewerDNSFaultComesFirstAndAFaultBeatsAProfilePartOnItsScope(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, dns: {names: [x.example.com], action: nxdomain}}`, 0)
	w.overlay(`{target: {device: esp32-42}, dns: {names: [x.example.com], action: servfail}}`, time.Second)
	tg := w.compile(nil)
	got := dnsEntries(tg)
	if len(got) < 2 || !strings.HasPrefix(got[0], "servfail") || !strings.HasPrefix(got[1], "nxdomain") {
		t.Errorf("newer first: %v", got)
	}

	w2 := newFaultWorld(t)
	w2.overlay(`{target: {device: esp32-42}, dns: {action: timeout}}`, 0)
	w2.overlay(`{target: {device: esp32-42}, profile: dns-broken}`, time.Second) // newer, but a part of a profile
	tg2 := w2.compile(nil)
	got2 := dnsEntries(tg2)
	if len(got2) < 2 || !strings.HasPrefix(got2[0], "timeout") || !strings.HasPrefix(got2[1], "servfail") {
		t.Errorf("a fault before a profile part: %v", got2)
	}
}

func TestDNSFaultsAreNotCompiledForAnOverlayThatIsGone(t *testing.T) {
	w := newFaultWorld(t)
	tg := w.compile(nil)
	if len(tg.DNSFaults) != 0 {
		t.Errorf("%+v", tg.DNSFaults)
	}
}
