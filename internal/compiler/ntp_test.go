package compiler

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The time family (plan §2.4, §2.18, M34, D36): the faults the NTP server answers with, and what the kernel sends to it.

const labNetID = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"

func withNetworkNTP(id string, ntp *model.NetworkNtp) func(*model.Configuration) {
	return func(cfg *model.Configuration) {
		n := (*cfg.Networks)[id]
		lan, _ := n.AsLanNetwork()
		lan.Ntp = ntp
		_ = n.FromLanNetwork(lan)
		(*cfg.Networks)[id] = n
	}
}

func compileTime(t *testing.T, w *faultWorld, service bool) *Target {
	t.Helper()
	var mod func(*Input)
	if service {
		mod = withServiceNS
	}
	tg := w.compile(mod)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	return tg
}

// ntpNAT is the rules of the NAT chain that lead into the NTP server (UDP 123 and the redirect ports, TCP 4460).
func ntpNAT(t *testing.T, tg *Target) []string {
	t.Helper()
	var out []string
	for _, c := range tg.Nft.Chains {
		if c.Name != "prerouting" {
			continue
		}
		for _, r := range renderRules(c) {
			if !strings.Contains(r, "dport 53 ") {
				out = append(out, r)
			}
		}
	}
	return out
}

// ntpEntries lists the entries of the server's list as "overlay-suffix:port -> sources".
func ntpEntries(tg *Target) (out []string) {
	for _, f := range tg.NTPFaults {
		var s []string
		for _, p := range f.Sources {
			s = append(s, p.String())
		}
		out = append(out, fmt.Sprintf("%s:%d -> %s", f.Overlay[len(f.Overlay)-3:], f.Port, strings.Join(s, ",")))
	}
	return out
}

func eqLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n%s\nwant:\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestATimeFaultIsAnEntryOfTheServerForTheClientsOfItsScope(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, time: {offset: +400d, skew_ppm: 500}}`, time.Minute)
	tg := compileTime(t, w, true)
	if len(tg.NTPFaults) != 1 {
		t.Fatalf("%+v", tg.NTPFaults)
	}
	f := tg.NTPFaults[0]
	if f.Overlay != o.Id.String() || f.Key != "overlay:"+o.Id.String()+":time" || f.Scope != "device esp32-42" || f.Profile != "" || f.Port != 123 ||
		*f.Fault.Offset != "+400d" || *f.Fault.SkewPpm != 500 || len(f.Sources) != 1 || f.Sources[0] != netip.MustParsePrefix("10.10.0.42/32") {
		t.Errorf("%+v", f)
	}
	// the instant the fault took effect is the overlay's updated_at, so that absolute and skew run from it
	if !f.Since.Equal(o.UpdatedAt) || !f.Since.Equal(tFault0.Add(time.Minute)) {
		t.Errorf("since %v, overlay updated at %v", f.Since, o.UpdatedAt)
	}
	// it is a winner: the preview lists it and the overlay is `effective`; it has no fault id and no tc class
	if len(tg.Faults) != 0 || len(tg.Winners) != 1 || tg.Winners[0] != f.Key || len(tg.Effective) != 1 || tg.Effective[0].Family != "time" || tg.Effective[0].Summary != "time offset +400d, skew 500 ppm" {
		t.Errorf("%+v %v %+v", tg.Faults, tg.Winners, tg.Effective)
	}
	if len(tg.NTPRedirectPorts) != 0 {
		t.Errorf("a fault without a destination selector has no port of its own: %v", tg.NTPRedirectPorts)
	}
	if got := tg.NTPListenPorts(); len(got) != 1 || got[0] != 123 {
		t.Errorf("listen %v", got)
	}
}

func TestNoTimeFaultNoEntryAndNoRedirectBeyondTheGatewayAddress(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {latency: 50ms}}`, 0)
	tg := compileTime(t, w, true)
	if len(tg.NTPFaults) != 0 || len(tg.NTPRedirectPorts) != 0 {
		t.Errorf("%+v %v", tg.NTPFaults, tg.NTPRedirectPorts)
	}
	// the gateway address of every network is the server's (the time service is on by default), over UDP, and its NTS key
	// establishment port which the server refuses and reports; the latency fault of the device does not add to it
	eqLines(t, "NAT rules", ntpNAT(t, tg), []string{
		"meta iifname br-iot ip daddr 10.10.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-iot ip daddr 10.10.0.1 tcp dport 4460 dnat ip to 169.254.100.2:4460",
		"meta iifname br-lab ip daddr 10.20.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-lab ip daddr 10.20.0.1 tcp dport 4460 dnat ip to 169.254.100.2:4460",
	})
	// and the accepts towards the namespace, next to the DNS ones
	var accepts []string
	for _, r := range renderRules(chainOf(t, tg, "forward")) {
		if strings.Contains(r, "oifname svc0 ip daddr 169.254.100.2") && !strings.Contains(r, "dport 53") {
			accepts = append(accepts, r)
		}
	}
	eqLines(t, "forward accepts", accepts, []string{
		"meta iifname @ifs_cg_bf4feb meta oifname svc0 ip daddr 169.254.100.2 udp dport 123 accept",
		"meta iifname @ifs_cg_bf4feb meta oifname svc0 ip daddr 169.254.100.2 tcp dport 4460 accept",
	})
}

// A scope with an active time fault is redirected whether or not its network redirects (Q3): the redirect of the fault
// is a rule of the device's sources, for every server outside the network's segments; the gateway's own address lies inside
// the network and has a rule of its own where the network's time service is off and no rule of the network follows.
func TestAScopeWithATimeFaultIsRedirectedWhetherOrNotItsNetworkRedirects(t *testing.T) {
	w := newFaultWorld(t)
	withNetworkNTP(iotNet, &model.NetworkNtp{Enabled: ptr(false)})(w.cfg)
	w.overlay(`{target: {device: esp32-42}, time: {offset: -90s}}`, 0)
	tg := compileTime(t, w, true)
	eqLines(t, "NAT rules", ntpNAT(t, tg), []string{
		// the device's rules: the gateway address (the network's own rule is not there), every server outside the segments
		"meta iifname br-iot ip saddr 10.10.0.42/32 ip daddr 10.10.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-iot ip saddr 10.10.0.42/32 ip daddr != { 10.10.0.0/24, 10.30.0.0/24 } udp dport 123 dnat ip to 169.254.100.2:123",
		// the other network keeps its gateway rules, the network without the time service has none
		"meta iifname br-lab ip daddr 10.20.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-lab ip daddr 10.20.0.1 tcp dport 4460 dnat ip to 169.254.100.2:4460",
	})
	for _, b := range tg.Bridges {
		if b.NetworkID == iotNet && (b.NTP || b.RedirectNTP) {
			t.Errorf("%+v", b)
		}
	}
	// with the time service on the network's own rule covers the gateway address, and the device's rule is only the rest
	w2 := newFaultWorld(t)
	w2.overlay(`{target: {device: esp32-42}, time: {offset: -90s}}`, 0)
	tg = compileTime(t, w2, true)
	got := ntpNAT(t, tg)
	if len(got) != 5 || got[0] != "meta iifname br-iot ip saddr 10.10.0.42/32 ip daddr != { 10.10.0.0/24, 10.30.0.0/24 } udp dport 123 dnat ip to 169.254.100.2:123" {
		t.Errorf("%v", got)
	}
}

// redirect_hardcoded: every server outside the network's segments, after the rules of the faults and of the gateway's address,
// for the networks that ask only.
func TestRedirectHardcodedRedirectsEveryServerOfTheNetworksThatAsk(t *testing.T) {
	w := newFaultWorld(t)
	withNetworkNTP(iotNet, &model.NetworkNtp{RedirectHardcoded: ptr(true)})(w.cfg)
	tg := compileTime(t, w, true)
	eqLines(t, "NAT rules", ntpNAT(t, tg), []string{
		"meta iifname br-iot ip daddr 10.10.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-iot ip daddr 10.10.0.1 tcp dport 4460 dnat ip to 169.254.100.2:4460",
		"meta iifname br-lab ip daddr 10.20.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-lab ip daddr 10.20.0.1 tcp dport 4460 dnat ip to 169.254.100.2:4460",
		// what stays inside the network and the network behind its router is not redirected (plan §2.2)
		"meta iifname br-iot ip daddr != { 10.10.0.0/24, 10.30.0.0/24 } udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-iot ip daddr != { 10.10.0.0/24, 10.30.0.0/24 } tcp dport 4460 dnat ip to 169.254.100.2:4460",
	})
	var iot Bridge
	for _, b := range tg.Bridges {
		if b.NetworkID == iotNet {
			iot = b
		}
	}
	if !iot.RedirectNTP || !iot.NTP {
		t.Errorf("%+v", iot)
	}
	// a fault of the same network goes in front of the hardcoded rule
	w.overlay(`{target: {device: esp32-42}, time: {offset: -90s}}`, 0)
	tg = compileTime(t, w, true)
	got := ntpNAT(t, tg)
	if len(got) != 7 || !strings.Contains(got[0], "ip saddr 10.10.0.42/32") || !strings.Contains(got[len(got)-2], "udp dport 123") || !strings.Contains(got[len(got)-2], "ip daddr != {") {
		t.Errorf("%v", got)
	}
}

// The destination selectors of the faults get a port of their own, in the order of their key, and the server's list has an
// entry for every port a query of the sources can arrive on, the winner of that port.
func TestEachDistinctDestinationSelectorGetsAPortOfItsOwn(t *testing.T) {
	w := newFaultWorld(t)
	a := w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.5}, offset: +1y}}`, 0)
	b := w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 203.0.113.5}, offset: +2y}}`, time.Minute)
	c := w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 198.51.100.0/24}, ports: [123, 5123], answer: silence}}`, 2*time.Minute)
	tg := compileTime(t, w, true)
	// the same selector (destination and ports) of two scopes is one port; the key orders them: 198.51.100.0/24 before 203.0.113.5
	if got := fmt.Sprint(tg.NTPRedirectPorts); got != "[12301 12302]" {
		t.Fatalf("redirect ports %s", got)
	}
	suffix := func(o model.Overlay) string { return o.Id.String()[len(o.Id.String())-3:] }
	eqLines(t, "entries", ntpEntries(tg), []string{
		// esp32-42 has the one selector in its list: the entry for 123 is empty (no fault answers a query that arrives there), the port of the selector decides
		suffix(a) + ":12302 -> 10.10.0.42/32",
		suffix(c) + ":12301 -> 10.10.0.43/32",
		suffix(b) + ":12302 -> 10.10.0.43/32",
	})
	eqLines(t, "NAT rules", ntpNAT(t, tg)[:5], []string{
		"meta iifname br-iot ip saddr 10.10.0.42/32 ip daddr 203.0.113.5/32 udp dport 123 dnat ip to 169.254.100.2:12302",
		"meta iifname br-iot ip saddr 10.10.0.43/32 ip daddr 198.51.100.0/24 udp dport { 123, 5123 } dnat ip to 169.254.100.2:12301",
		"meta iifname br-iot ip saddr 10.10.0.43/32 ip daddr 203.0.113.5/32 udp dport 123 dnat ip to 169.254.100.2:12302",
		"meta iifname br-iot ip daddr 10.10.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname br-iot ip daddr 10.10.0.1 tcp dport 4460 dnat ip to 169.254.100.2:4460",
	})
	if got := fmt.Sprint(tg.NTPListenPorts()); got != "[123 12301 12302]" {
		t.Errorf("listen %s", got)
	}
	// the accept towards the namespace lists the ports the server listens on
	var found bool
	for _, r := range renderRules(chainOf(t, tg, "forward")) {
		found = found || strings.Contains(r, "oifname svc0 ip daddr 169.254.100.2 udp dport { 123, 12301, 12302 } accept")
	}
	if !found {
		t.Errorf("no accept for the ports: %v", renderRules(chainOf(t, tg, "forward")))
	}
}

// The ports follow the order of the selectors' keys, not the order of the overlays or of the devices that have them.
func TestThePortsDoNotMoveWithTheDevicesThatHaveAFault(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.5}, offset: +1y}}`, 0)
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 198.51.100.7}, offset: +1y}}`, 0)
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 192.0.2.7}, offset: +1y}}`, time.Second)
	tg := compileTime(t, w, true)
	ports := map[string]int{}
	for _, r := range ntpNAT(t, tg) {
		for _, d := range []string{"203.0.113.5", "198.51.100.7", "192.0.2.7"} {
			if strings.Contains(r, "ip daddr "+d+"/32") {
				p, err := strconv.Atoi(r[strings.LastIndex(r, ":")+1:])
				if err != nil {
					t.Fatalf("%s: %v", r, err)
				}
				ports[d] = p
			}
		}
	}
	if ports["192.0.2.7"] != 12301 || ports["198.51.100.7"] != 12302 || ports["203.0.113.5"] != 12303 {
		t.Errorf("%v", ports)
	}
}

// At most 32 distinct destinations get a port; beyond that the compiler warns and the faults for the rest are served like a
// fault without a destination selector (D36): they are on port 123 and the redirect is the one of the scope without a selector.
func TestMoreThan32DestinationsWarnAndTheRestIsServedLikeNoSelector(t *testing.T) {
	w := newFaultWorld(t)
	for i := 0; i < 33; i++ {
		w.overlay(fmt.Sprintf(`{target: {device: esp32-42}, time: {destination: {cidr: 198.51.100.%d}, offset: +%dh}}`, i+1, i+1), time.Duration(i)*time.Second)
	}
	tg := compileTime(t, w, true)
	if len(tg.NTPRedirectPorts) != MaxNTPDestinations || tg.NTPRedirectPorts[0] != NTPRedirectPortBase || tg.NTPRedirectPorts[31] != NTPRedirectPortBase+31 {
		t.Fatalf("ports %v", tg.NTPRedirectPorts)
	}
	var warned bool
	for _, p := range tg.Problems {
		warned = warned || (p.Severity == SevWarning && p.Code == CodeNTPDestinations && strings.Contains(p.Message, "33 different destinations"))
	}
	if !warned {
		t.Errorf("%+v", tg.Problems)
	}
	// the key orders the selectors as text, so 198.51.100.9 is the last of the 33 and has no port
	var onDefault *NTPFault
	for i, f := range tg.NTPFaults {
		if f.Port == 123 {
			onDefault = &tg.NTPFaults[i]
		}
	}
	if onDefault == nil {
		t.Fatalf("the fault without a port is on 123: %+v", tg.NTPFaults)
	}
	nat := ntpNAT(t, tg)
	var catchAll bool
	for _, r := range nat {
		catchAll = catchAll || (strings.HasPrefix(r, "meta iifname br-iot ip saddr 10.10.0.42/32 ip daddr != {") && strings.HasSuffix(r, "udp dport 123 dnat ip to 169.254.100.2:123"))
	}
	if !catchAll {
		t.Errorf("no redirect of the scope without a selector: %v", nat)
	}
	if len(tg.NTPFaults) != 33 {
		t.Errorf("%d entries: one for each port of the 32 and the fault served like no selector on 123", len(tg.NTPFaults))
	}
}

// Overlapping scopes: the device over the group over the network, the newer first at the same level (plan §2.4), and a fault
// for one server does not hide a less specific one for the others.
func TestOverlappingScopesFollowThePrecedenceOfTheFamily(t *testing.T) {
	w := newFaultWorld(t)
	net := w.overlay(`{target: {network: IoT}, time: {offset: +1d}}`, 0)
	grp := w.overlay(`{target: {group: sensors}, time: {offset: +2d}}`, time.Minute)
	dev := w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.5}, offset: +3d}}`, 2*time.Minute)
	tg := compileTime(t, w, true)
	suffix := func(o model.Overlay) string { return o.Id.String()[len(o.Id.String())-3:] }
	// esp32-42: the device's fault decides on the port of its server and the group's on every other; esp32-43 has the group's;
	// the rest of the network the network's
	eqLines(t, "entries", ntpEntries(tg), []string{
		suffix(grp) + ":123 -> 10.10.0.42/32",
		suffix(dev) + ":12301 -> 10.10.0.42/32",
		suffix(grp) + ":123 -> 10.10.0.43/32",
		suffix(net) + ":123 -> 10.10.0.0/24,10.30.0.0/24",
	})
	// the winners are the three faults; the network's fault wins somewhere (the rest of the network)
	if len(tg.Winners) != 3 || len(tg.Effective) != 3 {
		t.Errorf("%v %+v", tg.Winners, tg.Effective)
	}
	// a newer fault at the same level wins on the same scope
	w.overlay(`{target: {group: sensors}, time: {offset: +4d}}`, 3*time.Minute)
	tg = w.compile(withServiceNS)
	if len(tg.Winners) != 3 {
		t.Fatalf("%v", tg.Winners)
	}
	for _, f := range tg.NTPFaults {
		if f.Scope == "group sensors" && *f.Fault.Offset != "+4d" {
			t.Errorf("the newer group fault does not win: %+v", f)
		}
	}
	// a fault that wins nowhere is not a winner: the older group fault is hidden by the newer one
	for _, k := range tg.Winners {
		if k == "overlay:"+grp.Id.String()+":time" {
			t.Errorf("the hidden fault is a winner: %v", tg.Winners)
		}
	}
}

// A hostname selector redirects the addresses of its DNS-derived set: the set is the proxy's to fill, and the rule looks the
// client and the server asked up in it.
func TestAHostnameSelectorLooksTheServerUpInTheDNSDerivedSet(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {group: sensors}, time: {destination: {hostname: time.example.org}, answer: kiss_of_death, kiss_code: DENY}}`, 0)
	tg := compileTime(t, w, true)
	if len(tg.DNSSets) != 1 || tg.DNSSets[0].Pattern != "time.example.org" {
		t.Fatalf("%+v", tg.DNSSets)
	}
	set := tg.DNSSets[0].Name
	nat := ntpNAT(t, tg)
	want := "meta iifname br-iot ip saddr { 10.10.0.42/32, 10.10.0.43/32 } ct original ip saddr . ct original ip daddr @" + set +
		" ip daddr != { 10.10.0.0/24, 10.30.0.0/24 } udp dport 123 dnat ip to 169.254.100.2:12301"
	if len(nat) == 0 || nat[0] != want {
		t.Errorf("%v\nwant %s", nat, want)
	}
	if len(tg.NTPFaults) != 1 || tg.NTPFaults[0].Port != 12301 || tg.NTPFaults[0].Fault.KissCode == nil {
		t.Errorf("%+v", tg.NTPFaults)
	}
}

// The destination selector of a fault that lies inside the network is not redirected (it is switched or routed back out of the
// interface), except for the gateway's own address; a prefix larger than the network is redirected without the network.
func TestASelectorInsideTheNetworkIsRedirectedForTheGatewayAddressOnly(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 10.10.0.0/24}, offset: +1d}}`, 0)
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 10.10.0.99}, offset: +1d}}`, 0)
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 10.0.0.0/8}, offset: +2d}}`, time.Second)
	tg := compileTime(t, w, true)
	var forDevice []string
	for _, r := range ntpNAT(t, tg) {
		if strings.Contains(r, "ip saddr") {
			forDevice = append(forDevice, r)
		}
	}
	eqLines(t, "device rules", forDevice, []string{
		// the whole network as the selector: only the gateway's address is ours
		"meta iifname br-iot ip saddr 10.10.0.42/32 ip daddr 10.10.0.1 udp dport 123 dnat ip to 169.254.100.2:12302",
		// 10.10.0.99 lies inside the network and has no rule; the prefix that is larger than the network contains the gateway
		// (a rule of its own) and the rest of it outside the network's segments
		"meta iifname br-iot ip saddr 10.10.0.43/32 ip daddr 10.10.0.1 udp dport 123 dnat ip to 169.254.100.2:12301",
		"meta iifname br-iot ip saddr 10.10.0.43/32 ip daddr 10.0.0.0/8 ip daddr != { 10.10.0.0/24, 10.30.0.0/24 } udp dport 123 dnat ip to 169.254.100.2:12301",
	})
}

// The time part of an activated profile is an entry with the profile's name.
func TestTheTimePartOfAProfileIsAnEntryWithTheProfilesName(t *testing.T) {
	w := newFaultWorld(t)
	const profID = "6d7e8f9a-0b1c-4d2e-8f3a-4b5c6d7e8f9a"
	profiles := map[string]model.Profile{profID: {Name: "clock-1970", Parts: model.ProfileParts{Time: &model.TimeFault{Absolute: ptr(time.Unix(0, 0).UTC())}}}}
	w.cfg.Profiles = &profiles
	o := w.overlay(`{target: {network: IoT}, profile: clock-1970}`, 0)
	tg := compileTime(t, w, true)
	if len(tg.NTPFaults) != 1 {
		t.Fatalf("%+v", tg.NTPFaults)
	}
	f := tg.NTPFaults[0]
	if f.Profile != "clock-1970" || f.Overlay != o.Id.String() || f.Fault.Absolute == nil || f.Port != 123 {
		t.Errorf("%+v", f)
	}
	if len(tg.Effective) != 1 || tg.Effective[0].Profile != "clock-1970" || tg.Effective[0].Family != "time" {
		t.Errorf("%+v", tg.Effective)
	}
}

// WireGuard networks: the gateway address (the tunnel address) is the server's, and a fault of the network redirects from the
// tunnel interface, outside the tunnel network and the networks behind its clients (plan §2.2.1).
func TestAWireGuardNetworkIsRedirectedFromItsTunnelInterface(t *testing.T) {
	w := newFaultWorldFile(t, "wireguard.yaml")
	w.overlay(`{target: {network: lab-hub}, time: {offset: +400d}}`, 0)
	tg := w.compile(func(in *Input) { in.Keys = wgKeys; in.ServiceNS = "cgsvc" })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	nat := ntpNAT(t, tg)
	for _, want := range []string{
		"meta iifname wg-lab-hub ip saddr { 10.50.0.0/24, 10.99.0.0/24 } ip daddr != { 10.50.0.0/24, 10.99.0.0/24 } udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname wg-lab-hub ip daddr 10.99.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname wg-admin ip daddr 10.98.0.1 udp dport 123 dnat ip to 169.254.100.2:123",
		"meta iifname wg-site-b ip daddr 10.255.0.0 tcp dport 4460 dnat ip to 169.254.100.2:4460",
	} {
		if !contains(nat, want) {
			t.Errorf("no rule %q in\n%s", want, strings.Join(nat, "\n"))
		}
	}
	// the faults of other networks do not leak into the link: only the hub's sources have a redirect from the hub
	for _, r := range nat {
		if strings.Contains(r, "ip saddr") && !strings.Contains(r, "iifname wg-lab-hub") {
			t.Errorf("a fault redirect from another interface: %s", r)
		}
	}
	if len(tg.NTPFaults) != 1 || tg.NTPFaults[0].Scope != "network lab-hub" {
		t.Errorf("%+v", tg.NTPFaults)
	}
}

// Fail closed: without a service namespace nothing is redirected and nothing is accepted towards a namespace that is not
// there, the faults are reported, and the network that asks for the time service or the redirect is told that it has none.
func TestWithoutAServiceNamespaceNothingIsRedirectedAndTheProblemIsReported(t *testing.T) {
	w := newFaultWorld(t)
	withNetworkNTP(iotNet, &model.NetworkNtp{Enabled: ptr(true), RedirectHardcoded: ptr(true)})(w.cfg)
	w.overlay(`{target: {device: esp32-42}, time: {offset: +400d}}`, 0)
	tg := compileTime(t, w, false)
	for _, c := range tg.Nft.Chains {
		if c.Name == "prerouting" {
			t.Errorf("a redirect without a service namespace: %v", renderRules(c))
		}
	}
	for _, r := range renderRules(chainOf(t, tg, "forward")) {
		if strings.Contains(r, "169.254.100.2") {
			t.Errorf("an accept towards a namespace that is not there: %s", r)
		}
	}
	var fault, network bool
	for _, p := range tg.Problems {
		if p.Severity != SevWarning || p.Code != CodeUnsupported {
			continue
		}
		fault = fault || strings.Contains(p.Message, "time faults") && p.Network == ""
		network = network || p.Network == "IoT" && strings.Contains(p.Message, "NTP server")
	}
	if !fault || !network {
		t.Errorf("%+v", tg.Problems)
	}
	for _, b := range tg.Bridges {
		if b.NTP {
			t.Errorf("the time service is on without a service namespace: %+v", b)
		}
	}
	// the entry is still the server's: the server is not there, but the preview and explain describe the fault
	if len(tg.NTPFaults) != 1 {
		t.Errorf("%+v", tg.NTPFaults)
	}
	// a network that did not ask is not told anything
	plain := compileTime(t, newFaultWorld(t), false)
	if len(plain.Problems) != 0 {
		t.Errorf("%+v", plain.Problems)
	}
}

// Kea hands out the gateway address as the NTP server (option 42) where the network's time service is on and the service
// namespace exists; a scope that names servers keeps them; an action of the DHCP family replaces whatever is there.
func TestKeaHandsOutTheGatewayAddressAsNTPServerWhereTheServiceIsOn(t *testing.T) {
	ntpOf := func(tg *Target, network string) []netip.Addr {
		t.Helper()
		if tg.Kea == nil {
			t.Fatal("no DHCP target")
		}
		for _, s := range tg.Kea.Config.Subnets {
			if s.Network == network {
				return s.NTP
			}
		}
		t.Fatalf("no subnet of %s", network)
		return nil
	}
	cases := []struct {
		name    string
		scope   *model.DhcpScope
		ntp     *model.NetworkNtp
		service bool
		want    string
	}{
		{"the default", &model.DhcpScope{}, nil, true, "[10.10.0.1]"},
		{"the default with the block", &model.DhcpScope{}, &model.NetworkNtp{Enabled: ptr(true)}, true, "[10.10.0.1]"},
		{"named servers are kept", &model.DhcpScope{Options: &model.DhcpOptions{NtpServers: &[]string{"192.0.2.9"}}}, nil, true, "[192.0.2.9]"},
		{"an empty list names none", &model.DhcpScope{Options: &model.DhcpOptions{NtpServers: &[]string{}}}, nil, true, "[10.10.0.1]"},
		{"the time service is off", &model.DhcpScope{}, &model.NetworkNtp{Enabled: ptr(false)}, true, "[]"},
		{"named servers of a network whose service is off", &model.DhcpScope{Options: &model.DhcpOptions{NtpServers: &[]string{"192.0.2.9"}}}, &model.NetworkNtp{Enabled: ptr(false)}, true, "[192.0.2.9]"},
		{"no service namespace", &model.DhcpScope{}, nil, false, "[]"},
	}
	for _, c := range cases {
		w := newFaultWorld(t)
		withDHCP(w.cfg, iotNet, c.scope)
		if c.ntp != nil {
			withNetworkNTP(iotNet, c.ntp)(w.cfg)
		}
		tg := compileTime(t, w, c.service)
		if got := fmt.Sprint(ntpOf(tg, iotNet)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if tg.Kea != nil && c.want == "[10.10.0.1]" && !strings.Contains(tg.Kea.Text, "ntp-servers") {
			t.Errorf("%s: the rendered configuration has no option 42", c.name)
		}
	}
}

func TestADHCPActionOverridesTheDefaultNTPServer(t *testing.T) {
	w := newFaultWorld(t)
	withDHCP(w.cfg, iotNet, &model.DhcpScope{})
	w.overlay(`{target: {network: IoT}, dhcp: {action: set_options, options: {ntp_servers: [192.0.2.77]}}}`, 0)
	w.overlay(`{target: {device: esp32-42}, dhcp: {action: set_options, options: {domain: lab.test}}}`, time.Second)
	tg := compileTime(t, w, true)
	sub := -1
	for i, s := range tg.Kea.Config.Subnets {
		if s.Network == iotNet {
			sub = i
		}
	}
	if sub < 0 {
		t.Fatal("no subnet")
	}
	// the action replaces the default of the network
	if got := fmt.Sprint(tg.Kea.Config.Subnets[sub].NTP); got != "[192.0.2.77]" {
		t.Errorf("subnet: %s", got)
	}
	// a device's action that names no servers sees the network's configured options, which hold the default: the action of
	// the network does not apply to it (the device's wins)
	var dev []string
	for _, r := range tg.Kea.Config.Subnets[sub].Reservations {
		dev = append(dev, fmt.Sprint(r.Options.NTP))
	}
	if len(dev) == 0 || dev[0] != "[10.10.0.1]" {
		t.Errorf("device reservations: %v", dev)
	}
}

// The compiled target is the same on every compile of the same input, so a hash that did not change needs no apply.
func TestTheTimeFamilyCompilesDeterministically(t *testing.T) {
	build := func() *Target {
		w := newFaultWorld(t)
		w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.5}, offset: +1y}}`, 0)
		w.overlay(`{target: {group: sensors}, time: {destination: {hostname: time.example.org}, answer: silence}}`, time.Minute)
		w.overlay(`{target: {network: IoT}, time: {leap: insert}}`, 2*time.Minute)
		return compileTime(t, w, true)
	}
	a, b := build(), build()
	if a.Hash != b.Hash || a.Hash == "" {
		t.Errorf("%q %q", a.Hash, b.Hash)
	}
	// and a time fault changes the hash
	plain := compileTime(t, newFaultWorld(t), true)
	if plain.Hash == a.Hash {
		t.Error("the hash does not cover the time faults")
	}
}

// scenarioNTP is the target the parser test and the kernel test check: a device with a fault for every server and one for
// a server, a group with a hostname selector, a network with ports, redirect_hardcoded on one network and a WireGuard hub.
func scenarioNTP(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	withNetworkNTP(labNetID, &model.NetworkNtp{RedirectHardcoded: ptr(true)})(w.cfg)
	w.overlay(`{target: {device: esp32-42}, time: {offset: +400d, skew_ppm: 50}}`, 0)
	w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.0/24}, absolute: '2038-01-19T03:14:07Z'}}`, time.Second)
	w.overlay(`{target: {group: sensors}, time: {destination: {hostname: '*.pool.example.org'}, answer: kiss_of_death, kiss_code: RATE}}`, 2*time.Second)
	w.overlay(`{target: {network: IoT}, time: {ports: [123, 5123], leap: delete}}`, 3*time.Second)
	w.overlay(`{target: {device: lab-host}, time: {answer: silence}}`, 4*time.Second)
	tg := compileTime(t, w, true)
	return tg
}

// scenarioNTPWireGuard is the time family on a WireGuard hub and a link next to a bridge.
func scenarioNTPWireGuard(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorldFile(t, "wireguard.yaml")
	w.overlay(`{target: {network: lab-hub}, time: {offset: -1d}}`, 0)
	w.overlay(`{target: {network: site-b}, time: {destination: {cidr: 203.0.113.9}, absolute: '1970-01-01T00:00:00Z'}}`, time.Second)
	w.overlay(`{target: {network: IoT}, time: {leap: insert}}`, 2*time.Second)
	tg := w.compile(func(in *Input) { in.Keys = wgKeys; in.ServiceNS = "cgsvc" })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	return tg
}

func TestTheNTPWireGuardScenarioCompiles(t *testing.T) {
	goldenText(t, "ntp-wireguard", describeNTP(scenarioNTPWireGuard(t)))
}

func TestTheNTPScenarioCompiles(t *testing.T) {
	tg := scenarioNTP(t)
	goldenText(t, "ntp", describeNTP(tg))
}

// describeNTP is the readable form of the NTP part of a target for the golden file: the server's list, the NAT rules that
// lead into it and the accepts towards the namespace.
func describeNTP(tg *Target) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# server's faults\n")
	for _, f := range tg.NTPFaults {
		var s []string
		for _, p := range f.Sources {
			s = append(s, p.String())
		}
		fmt.Fprintf(&b, "%s  port %d  %s  scope %q  sources %s\n", f.Key, f.Port, tg.faultSummary(f), f.Scope, strings.Join(s, ","))
	}
	fmt.Fprintf(&b, "# listen %v\n", tg.NTPListenPorts())
	fmt.Fprintf(&b, "# prerouting (without DNS)\n")
	for _, c := range tg.Nft.Chains {
		if c.Name != "prerouting" {
			continue
		}
		for _, r := range renderRules(c) {
			if !strings.Contains(r, "dport 53 ") {
				fmt.Fprintf(&b, "%s\n", r)
			}
		}
	}
	fmt.Fprintf(&b, "# forward (the accepts towards the namespace)\n")
	for _, r := range renderRules(chainOfName(tg, "forward")) {
		if strings.Contains(r, "oifname svc0 ip daddr 169.254.100.2") && !strings.Contains(r, "dport 53 ") {
			fmt.Fprintf(&b, "%s\n", r)
		}
	}
	return b.String()
}

func (t *Target) faultSummary(f NTPFault) string {
	for _, e := range t.Effective {
		if e.Key == f.Key {
			return e.Summary
		}
	}
	return "?"
}

// A selector keeps its port while it is in use, whatever comes or goes around it: the ports of the last compile are handed
// back (Input.NTPPorts), a new selector takes the lowest free port, and a port that was freed is taken again.
func TestASelectorKeepsItsPortWhenAnotherSelectorComesOrGoes(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.5}, offset: +1y}}`, 0)
	var prev map[string]int
	compile := func() map[string]int {
		tg := w.compile(func(in *Input) { withServiceNS(in); in.NTPPorts = prev })
		if tg.HasErrors() {
			t.Fatalf("%+v", tg.Problems)
		}
		prev = tg.NTPPorts
		ports := map[string]int{}
		for _, r := range ntpNAT(t, tg) {
			for _, d := range []string{"203.0.113.5", "198.51.100.7", "192.0.2.7"} {
				if strings.Contains(r, "ip daddr "+d+"/32") {
					p, err := strconv.Atoi(r[strings.LastIndex(r, ":")+1:])
					if err != nil {
						t.Fatalf("%s: %v", r, err)
					}
					ports[d] = p
				}
			}
		}
		return ports
	}
	if got := compile(); got["203.0.113.5"] != 12301 {
		t.Fatalf("%v", got)
	}
	// 192.0.2.7 and 198.51.100.7 sort before 203.0.113.5, and neither takes its port
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 192.0.2.7}, offset: +1y}}`, time.Second)
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 198.51.100.7}, offset: +1y}}`, 2*time.Second)
	got := compile()
	if got["203.0.113.5"] != 12301 || got["192.0.2.7"] != 12302 || got["198.51.100.7"] != 12303 {
		t.Fatalf("%v", got)
	}
	// the first selector goes: the others stay where they are, and the next new one takes the freed port
	w.overlays = w.overlays[1:]
	got = compile()
	if _, ok := got["203.0.113.5"]; ok || got["192.0.2.7"] != 12302 || got["198.51.100.7"] != 12303 {
		t.Fatalf("%v", got)
	}
	w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 203.0.113.5}, offset: +2y}}`, 3*time.Second)
	got = compile()
	if got["203.0.113.5"] != 12301 || got["192.0.2.7"] != 12302 || got["198.51.100.7"] != 12303 {
		t.Fatalf("%v", got)
	}
	// without the last allocation the ports follow the order of the keys again
	prev = nil
	if got = compile(); got["192.0.2.7"] != 12301 || got["198.51.100.7"] != 12302 || got["203.0.113.5"] != 12303 {
		t.Fatalf("%v", got)
	}
}

// The first entry that matches decides, in the server and in the kernel, so the device that has a fault of its own comes
// before the group of the rest of its network, whatever the order of the devices' ids is: the network's stretch contains the
// device's address.
func TestAFaultOfADeviceComesBeforeTheFaultOfItsNetworkInTheListAndTheRules(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, time: {offset: +1d}}`, 0)
	w.overlay(`{target: {device: esp32-43}, time: {offset: +5d}}`, time.Minute)
	tg := compileTime(t, w, true)
	entries := ntpEntries(tg)
	if len(entries) != 2 || !strings.HasSuffix(entries[0], "-> 10.10.0.43/32") || !strings.Contains(entries[1], "10.10.0.0/24") {
		t.Fatalf("the device comes first: %v", entries)
	}
	nat := ntpNAT(t, tg)
	var device, network = -1, -1
	for i, r := range nat {
		if strings.Contains(r, "ip saddr 10.10.0.43/32") && strings.Contains(r, "udp dport 123 dnat") && device < 0 {
			device = i
		}
		if strings.Contains(r, "ip saddr {") && strings.Contains(r, "10.10.0.0/24") && strings.Contains(r, "udp dport 123 dnat") && network < 0 {
			network = i
		}
	}
	if device < 0 || network < 0 || device > network {
		t.Errorf("the redirect of the device (%d) comes before the one of the network (%d): %v", device, network, nat)
	}
}

// A selector that is new in the compile in which another one goes does not take the port of the one that went: the server
// learns the new configuration some time after the kernel has the rules, and until then its entry of that port is the old
// selector's fault. The freed port is used when no other is left.
func TestANewSelectorDoesNotTakeThePortOfOneThatWentInTheSameCompile(t *testing.T) {
	got := allocateNTPPorts([]string{"b"}, map[string]int{"a": 12301})
	if got.port["b"] != 12302 {
		t.Errorf("b took %d, want 12302 (12301 was released by a)", got.port["b"])
	}
	// a generation later the port is free again
	if got = allocateNTPPorts([]string{"b", "c"}, map[string]int{"b": 12302}); got.port["b"] != 12302 || got.port["c"] != 12301 {
		t.Errorf("%v", got.port)
	}
	// every port is released: 32 new selectors still get the 32 ports
	prev := map[string]int{}
	var keys []string
	for i := range MaxNTPDestinations {
		prev[fmt.Sprintf("old%02d", i)] = NTPRedirectPortBase + i
		keys = append(keys, fmt.Sprintf("new%02d", i))
	}
	got = allocateNTPPorts(keys, prev)
	if len(got.port) != MaxNTPDestinations || len(got.overflow) != 0 {
		t.Errorf("%d ports, %d overflowing", len(got.port), len(got.overflow))
	}
}

// A previous allocation is only trusted inside the range of the redirect ports and once per port: an entry outside the range
// (the base port 123, a port next to the range) or a port two selectors claim would have the server listen on 123 twice or on a
// port the forward accept does not cover.
func TestAPreviousAllocationOutsideTheRangeOrWithADuplicateIsNotReused(t *testing.T) {
	prev := map[string]int{"base": 123, "above": NTPRedirectPortBase + MaxNTPDestinations, "below": NTPRedirectPortBase - 1, "zero": 0,
		"far": 12400, "dupA": 12305, "dupB": 12305, "ok": 12310}
	keys := []string{"base", "above", "below", "zero", "far", "dupA", "dupB", "ok"}
	got := allocateNTPPorts(keys, prev)
	seen := map[int]string{}
	for k, p := range got.port {
		if p < NTPRedirectPortBase || p >= NTPRedirectPortBase+MaxNTPDestinations {
			t.Errorf("%s got the port %d outside the range", k, p)
		}
		if o, dup := seen[p]; dup {
			t.Errorf("%s and %s share the port %d", o, k, p)
		}
		seen[p] = k
	}
	if len(got.port) != len(keys) {
		t.Errorf("%d of %d selectors have a port: %v", len(got.port), len(keys), got.port)
	}
	if got.port["ok"] != 12310 {
		t.Errorf("a valid previous port is kept: %d", got.port["ok"])
	}
	if got.port["dupA"] != 12305 && got.port["dupB"] != 12305 {
		t.Errorf("one of the two claims is kept: %v", got.port)
	}
}
