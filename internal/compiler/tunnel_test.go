package compiler

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The compiler's side of the tunnel faults and the WireGuard actions (M10, plan §2.2.1): a tunnel fault is a fault of
// its own family, found by the endpoint of its peer, with a class in the tree of the interfaces (towards the peer) and one
// in the tree of the IFB (from the peer); the actions change the peer on its interface or block its endpoint.

const (
	// the address the client rA is seen at, and the endpoint the link site-b is configured with
	epClientA = "198.51.100.2:51820"
	epLinkB   = "203.0.113.40:51821"
)

func newTunnelWorld(t *testing.T) *faultWorld {
	t.Helper()
	w := newFaultWorldFile(t, "tunnels.yaml")
	return w
}

// compileTunnel compiles with the keys of the WireGuard networks and the endpoints the peers are seen at (client id to
// "ip:port").
func compileTunnel(w *faultWorld, ends map[string]string) *Target {
	return w.compile(func(in *Input) {
		in.Keys = wgKeys
		if len(ends) > 0 {
			in.PeerEndpoints = map[string]netip.AddrPort{}
		}
		for id, ep := range ends {
			in.PeerEndpoints[id] = netip.MustParseAddrPort(ep)
		}
	})
}

func tunnelFault(t *testing.T, tg *Target, source string) Fault {
	t.Helper()
	f := faultOf(t, tg, source, "")
	if f.Tunnel == nil {
		t.Fatalf("fault %d is not a tunnel fault: %+v", f.ID, f)
	}
	return f
}

// A tunnel fault is found by the endpoint of its peer. Towards the peer (download) it is a class of the tree of the
// interfaces that the output hook selects by a mark; from the peer (upload) it is a class of the tree of the IFB device
// that a flower filter on the peer's address and port selects, fed by a flower filter on the ingress qdisc of the uplink.
func TestATunnelFaultHasAClassOnEachSideOfTheTunnelAndIsFoundByTheEndpointOfItsPeer(t *testing.T) {
	w := newTunnelWorld(t)
	o := w.overlay(`{fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 20ms}, download: {latency: 50ms, loss: 1%}}}`, 0)
	tg := compileTunnel(w, map[string]string{clientA: epClientA})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	f := tunnelFault(t, tg, o.Id.String())
	if f.Tunnel.Tunnel != "client:"+clientA || f.Tunnel.Endpoint != epClientA || f.Tunnel.Interface != "wg-lab-hub" || f.Scope != "tunnel of client rA" {
		t.Errorf("%+v %q", f.Tunnel, f.Scope)
	}
	if f.Upload == nil || f.Upload.Delay != 20*time.Millisecond || f.Download == nil || f.Download.Delay != 50*time.Millisecond || f.Download.Loss != 1 {
		t.Fatalf("up %v down %v", f.Upload, f.Download)
	}
	if !tg.hasWinner("overlay:" + o.Id.String() + ":tunnel") {
		t.Errorf("the fault is not among the winners: %v", tg.Winners)
	}

	// towards the peer: the tree of the interfaces has the download class only
	if tg.TC == nil || len(tg.TC.Classes) != 1 || tg.TC.Classes[0].ID != f.ID || tg.TC.Classes[0].Dir != Download || tg.TC.Classes[0].Endpoint != "" {
		t.Fatalf("tree of the interfaces: %+v", tg.TC)
	}
	// from the peer: the tree of the IFB has the upload class, selected by the endpoint
	if tg.IFB == nil || tg.IFB.Dev != IFBDev || tg.IFB.Uplink != "wan0" || len(tg.IFB.TC.Classes) != 1 {
		t.Fatalf("IFB: %+v", tg.IFB)
	}
	c := tg.IFB.TC.Classes[0]
	if c.ID != f.ID || c.Dir != Upload || c.Endpoint != epClientA || c.Netem.Delay != 20*time.Millisecond || strings.Join(tg.IFB.TC.Devs, ",") != IFBDev {
		t.Errorf("IFB class: %+v", c)
	}
	wantFilter := "filter replace dev ifb-cgw parent 1: handle " + fmt.Sprint(f.ID) + " protocol ip prio 1 flower ip_proto udp src_ip 198.51.100.2 src_port 51820 flowid " + c.ClassID()
	got := tg.IFB.TC.Lines(IFBDev)
	if !contains(got, wantFilter) {
		t.Errorf("no flower filter on the IFB:\n%s\nwant %s", strings.Join(got, "\n"), wantFilter)
	}
	ing := tg.IFB.IngressEntries()
	if len(ing) != 2 || ing[0].Object != "qdisc" || ing[0].Parent != "ingress" || ing[0].Dev != "wan0" {
		t.Fatalf("ingress entries: %+v", ing)
	}
	if got := strings.Join(ing[1].Args, " "); ing[1].Dev != "wan0" || ing[1].Parent != "ffff:" || ing[1].Handle != fmt.Sprint(f.ID) ||
		got != "protocol ip prio 10 flower ip_proto udp src_ip 198.51.100.2 src_port 51820 action mirred egress redirect dev ifb-cgw" {
		t.Errorf("ingress filter: %+v", ing[1])
	}
	if !contains(tg.Interfaces, IFBDev) {
		t.Errorf("the IFB is not assigned: %v", tg.Interfaces)
	}

	// the output hook: one base chain, one lookup by peer address and port, a chain of the fault that writes id and
	// direction and counts; the lookup chain of the traffic inside the tunnel knows nothing of it
	out := findChain(tg, TunnelOutChain)
	if out == nil || out.Base == nil || out.Base.Hook != "output" || out.Base.Type != "filter" || out.Base.Prio != TunnelOutPriority {
		t.Fatalf("%+v", out)
	}
	var tm *MapDef
	for i := range tg.Nft.Maps {
		if strings.HasPrefix(tg.Nft.Maps[i].Name, "tun_out_") {
			tm = &tg.Nft.Maps[i]
		}
	}
	if tm == nil || len(tm.Elements) != 1 || tm.Elements[0].Key != "198.51.100.2 . 51820" || tm.Elements[0].Value != tunnelChainName(f.ID) ||
		strings.Join(tm.KeyType, ",") != "ipv4_addr,inet_service" || tm.ValueType != "verdict" {
		t.Fatalf("map: %+v", tm)
	}
	if rules := ruleJSON(out); len(rules) != 1 || !strings.Contains(rules[0], `"udp"`) || !strings.Contains(rules[0], tm.Name) || !strings.Contains(rules[0], `"daddr"`) || !strings.Contains(rules[0], `"dport"`) {
		t.Errorf("lookup: %v", rules)
	}
	chain := findChain(tg, tunnelChainName(f.ID))
	if chain == nil || chain.Base != nil {
		t.Fatalf("%+v", chain)
	}
	rules := ruleJSON(chain)
	// mark = (mark & 0xfffe000f | id << 4) | 0x10000: the id of the fault and the direction bit of a packet towards the peer
	wantMark := fmt.Sprintf(`[{"mangle":{"key":{"meta":{"key":"mark"}},"value":{"|":[{"|":[{"\u0026":[{"meta":{"key":"mark"}},%d]},{"\u003c\u003c":[%d,4]}]},65536]}}}]`, markKeepOnTunnelWrite, f.ID)
	if len(rules) != 3 || rules[0] != wantMark || !strings.Contains(rules[1], f.CounterDown) || !strings.Contains(rules[2], `"return"`) {
		t.Errorf("tunnel chain:\n%s\nwant %s", strings.Join(rules, "\n"), wantMark)
	}
	if markKeepOnTunnelWrite != 0xfffe000f || markKeepOnTunnelWrite&markDirMaskBits != 0 {
		t.Errorf("the mask that clears id and direction is %#x", markKeepOnTunnelWrite)
	}
	// no mark_<id> chain: the prerouting lookup does not classify the encrypted UDP
	if findChain(tg, MarkChainName(f.ID)) != nil {
		t.Errorf("the tunnel fault has a chain in the classification of the traffic inside the tunnel")
	}
	// only the counter of the download is an nft counter; the upload is counted by tc (the ingress filter)
	if !contains(tg.Nft.Counters, f.CounterDown) || contains(tg.Nft.Counters, f.CounterUp) {
		t.Errorf("counters: %v", tg.Nft.Counters)
	}
}

// dropOverlay removes an overlay from the fixture, as its deletion does.
func (w *faultWorld) dropOverlay(id string) {
	var keep []model.Overlay
	for _, o := range w.overlays {
		if o.Id.String() != id {
			keep = append(keep, o)
		}
	}
	w.overlays = keep
}

func (t *Target) hasWinner(key string) bool { return contains(t.Winners, key) }

// A tunnel fault that impairs one direction only has a class on that side only.
func TestATunnelFaultOfOneDirectionHasOneClass(t *testing.T) {
	w := newTunnelWorld(t)
	w.overlay(`{fault: {family: tunnel, tunnel: {link: site-b}, download: {blackout: true}}}`, 0)
	tg := compileTunnel(w, nil) // the link names its endpoint
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if tg.IFB != nil || tg.TC == nil || len(tg.TC.Classes) != 1 || tg.TC.Classes[0].Netem.Loss != 100 {
		t.Fatalf("IFB %+v TC %+v", tg.IFB, tg.TC)
	}
	if contains(tg.Interfaces, IFBDev) {
		t.Errorf("an IFB that nothing uses is assigned: %v", tg.Interfaces)
	}
	if got := tg.Endpoints; len(got) != 1 || got[linkID] != epLinkB {
		t.Errorf("endpoints: %v", got)
	}
}

// A peer with no known address cannot be found among the encrypted UDP: the fault is not compiled and the compiler says so;
// it takes effect when the address is known. A client that has connected is found at the address the interface reports, a
// link at the endpoint it is configured with unless the interface reports another.
func TestATunnelFaultOfAPeerWithoutAnAddressWaitsForIt(t *testing.T) {
	w := newTunnelWorld(t)
	o := w.overlay(`{fault: {family: tunnel, tunnel: {client: rA}, latency: 30ms}}`, 0)
	tg := compileTunnel(w, nil)
	if tg.HasErrors() || tg.IFB != nil || tg.TC != nil || len(tg.Faults) != 0 {
		t.Fatalf("a fault with nowhere to go is compiled: %+v %+v", tg.Faults, tg.Problems)
	}
	var warned bool
	for _, p := range tg.Problems {
		warned = warned || p.Code == CodeTunnelEndpointUnknown && p.Severity == SevWarning && strings.Contains(p.Message, "rA")
	}
	if !warned {
		t.Errorf("no warning: %+v", tg.Problems)
	}
	if v, ok := tg.Endpoints[clientA]; !ok || v != "" {
		t.Errorf("the engine must learn that the address is wanted: %v", tg.Endpoints)
	}
	if !tg.hasWinner("overlay:" + o.Id.String() + ":tunnel") {
		t.Errorf("the fault wins (and is in the store): %v", tg.Winners)
	}
	// the address is known now
	tg = compileTunnel(w, map[string]string{clientA: epClientA})
	if f := tunnelFault(t, tg, o.Id.String()); f.Tunnel.Endpoint != epClientA {
		t.Errorf("%+v", f.Tunnel)
	}
	// an observed address of the link beats the configured one
	w2 := newTunnelWorld(t)
	o2 := w2.overlay(`{fault: {family: tunnel, tunnel: {link: site-b}, latency: 30ms}}`, 0)
	tg = compileTunnel(w2, map[string]string{linkID: "198.51.100.77:40000"})
	if f := tunnelFault(t, tg, o2.Id.String()); f.Tunnel.Endpoint != "198.51.100.77:40000" {
		t.Errorf("%+v", f.Tunnel)
	}
}

// Tunnel faults are resolved per tunnel: overlays before the configuration, the newest wins, parameters are not merged.
func TestTunnelFaultsAreResolvedPerTunnelOverlayBeforeConfigurationNewestFirst(t *testing.T) {
	w := newTunnelWorld(t)
	w.addConfigFault("5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b", `{family: tunnel, tunnel: {client: rA}, latency: 10ms, name: cfg}`, 0)
	first := w.overlay(`{fault: {family: tunnel, tunnel: {client: rA}, latency: 40ms, loss: 5%}}`, time.Second)
	newer := w.overlay(`{fault: {family: tunnel, tunnel: {client: rA}, download: {latency: 60ms}}}`, 2*time.Second)
	tg := compileTunnel(w, map[string]string{clientA: epClientA})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.Faults) != 1 {
		t.Fatalf("one tunnel, one winner: %+v", tg.Faults)
	}
	f := tunnelFault(t, tg, newer.Id.String())
	// the winner's complete set: download 60 ms, upload not impaired at all (no merge with the 40 ms / 5 % of the older one)
	if f.Upload != nil || f.Download == nil || f.Download.Delay != 60*time.Millisecond || f.Download.Loss != 0 {
		t.Errorf("up %v down %v", f.Upload, f.Download)
	}
	if tg.IFB != nil {
		t.Errorf("the winner does not impair the packets from the peer: %+v", tg.IFB)
	}
	for _, key := range []string{"overlay:" + first.Id.String() + ":tunnel", "config:5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b:tunnel"} {
		if tg.hasWinner(key) {
			t.Errorf("%s is overridden but wins", key)
		}
	}
}

// E10 (plan §2.4): a device fault in the configuration and a tunnel fault in an overlay stack, because they are faults of
// two families: the device's connection into the tunnel is classified by the lookup chain of the traffic and gets the netem of
// its class on the interface the packet leaves through, the encrypted packets of the tunnel get the other class where they
// leave. Neither fault is in the way of the other: they have ids and classes of their own, and the chains of the one
// know nothing of the other. The golden file e10 shows the target.
func TestE10ADeviceFaultAndATunnelFaultStack(t *testing.T) {
	w := newTunnelWorld(t)
	w.addConfigFault("5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b", `{name: a-40, source: {device: esp32-42}, upload: {latency: 40ms}, download: {latency: 40ms}}`, 0)
	o := w.overlay(`{fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 50ms}, download: {latency: 50ms}}}`, time.Second)
	tg := compileTunnel(w, map[string]string{clientA: epClientA})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	dev := faultOf(t, tg, "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b", "")
	tun := tunnelFault(t, tg, o.Id.String())
	if dev.ID == tun.ID || dev.Tunnel != nil {
		t.Fatalf("device %+v tunnel %+v", dev, tun)
	}
	// both fault ids are classified in their own way
	if findChain(tg, MarkChainName(dev.ID)) == nil || findChain(tg, tunnelChainName(tun.ID)) == nil || findChain(tg, MarkChainName(tun.ID)) != nil {
		t.Error("the chains of the two faults are not what they should be")
	}
	m := findMap(tg, tg.ClassifyMaps["dev"])
	var devMapped bool
	for _, e := range m.Elements {
		if strings.Contains(e.Key, "10.10.0.42") && e.Value == MarkChainName(dev.ID) {
			devMapped = true
		}
		if e.Value == tunnelChainName(tun.ID) {
			t.Errorf("the classification of the traffic holds the tunnel fault: %+v", e)
		}
	}
	if !devMapped {
		t.Errorf("the device is not classified into its fault: %+v", m.Elements)
	}
	// the world resolves the two families independently, and the traffic of A to a host behind rA crosses the tunnel of rA
	world, err := domain.NewWorld(w.cfg, w.overlays)
	if err != nil {
		t.Fatal(err)
	}
	q := domain.Query{Source: domain.Subject{Device: devESP42, IP: netip.MustParseAddr("10.10.0.42")}, DestIP: netip.MustParseAddr("10.50.0.10"), Protocol: "tcp", Port: 22}
	res := world.Resolve(q)
	if win := domain.Winner(res, domain.FamilyImpairment); win == nil || win.ID != "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b" {
		t.Errorf("impairment winner %+v", win)
	}
	crossed := world.TunnelsCrossed(q.Source.IP, q.DestIP)
	if len(crossed) != 1 || crossed[0] != "client:"+clientA {
		t.Fatalf("crossed %v", crossed)
	}
	tr, ok := world.TunnelFaultOf(crossed[0])
	if !ok || tr.Winner.ID != o.Id.String() {
		t.Errorf("tunnel winner %+v", tr)
	}
	// the same host reached from a device of the network IoT in the other direction crosses it as the source
	if got := world.TunnelsCrossed(netip.MustParseAddr("10.50.0.10"), netip.MustParseAddr("10.10.0.42")); len(got) != 1 || got[0] != "client:"+clientA {
		t.Errorf("crossed (source behind the client) %v", got)
	}
	// traffic that stays inside the test network crosses no tunnel
	if got := world.TunnelsCrossed(netip.MustParseAddr("10.10.0.42"), netip.MustParseAddr("203.0.113.10")); len(got) != 0 {
		t.Errorf("crossed %v", got)
	}
	goldenText(t, "e10", describeFaults(tg)+describeTunnels(tg))
}

// describeTunnels is the readable form of the tunnel part of a target for the golden files.
func describeTunnels(tg *Target) string {
	var b strings.Builder
	b.WriteString("# tunnel faults\n")
	for _, f := range tg.Faults {
		if f.Tunnel != nil {
			fmt.Fprintf(&b, "id %d  %s  %s  endpoint %s on %s\n", f.ID, f.Tunnel.Tunnel, f.Scope, f.Tunnel.Endpoint, f.Tunnel.Interface)
		}
	}
	if tg.IFB != nil {
		fmt.Fprintf(&b, "# ifb %s, ingress of %s\n", tg.IFB.Dev, tg.IFB.Uplink)
		for _, l := range tg.IFB.TC.Lines(tg.IFB.Dev) {
			b.WriteString(l + "\n")
		}
		for _, e := range tg.IFB.IngressEntries() {
			fmt.Fprintf(&b, "%s %s dev %s %s %s %s\n", e.Object, e.Action, e.Dev, e.Parent, e.Handle, strings.Join(e.Args, " "))
		}
	}
	for _, c := range tg.Nft.Chains {
		if c.Base != nil && c.Base.Hook != "prerouting" && c.Base.Hook != "input" && c.Base.Hook != "forward" && c.Base.Hook != "postrouting" || strings.HasPrefix(c.Name, TunnelChainPrefix) || strings.HasPrefix(c.Name, WGBlockChainPrefix) {
			fmt.Fprintf(&b, "chain %s", c.Name)
			if c.Base != nil {
				fmt.Fprintf(&b, " (%s hook %s priority %d)", c.Base.Type, c.Base.Hook, c.Base.Prio)
			}
			b.WriteString("\n")
			for _, r := range ruleJSON(&c) {
				fmt.Fprintf(&b, "    %s\n", r)
			}
		}
	}
	for _, m := range tg.Nft.Maps {
		if strings.HasPrefix(m.Name, "tun_out_") || strings.HasPrefix(m.Name, "wgblk_") {
			fmt.Fprintf(&b, "map %s (%s)\n", m.Name, strings.Join(m.KeyType, " . "))
			for _, e := range m.Elements {
				fmt.Fprintf(&b, "    %s : %s\n", e.Key, e.Value)
			}
		}
	}
	for _, a := range tg.WGActions {
		fmt.Fprintf(&b, "action %s %s %s counter %q\n", a.Action, a.Tunnel, a.PeerName, a.Counter)
	}
	return b.String()
}

// A flapping tunnel fault flaps on both sides, in step: the classes of the interfaces and of the IFB carry flap keys of the
// fault, and the phase the engine reports is written into both.
func TestAFlappingTunnelFaultFlapsOnBothSides(t *testing.T) {
	w := newTunnelWorld(t)
	o := w.overlay(`{fault: {family: tunnel, tunnel: {link: site-b}, flapping: {up: 6s, down: 4s}}}`, 0)
	up := compileTunnel(w, nil)
	if up.HasErrors() {
		t.Fatalf("%+v", up.Problems)
	}
	if up.TC == nil || up.IFB == nil {
		t.Fatalf("TC %+v IFB %+v", up.TC, up.IFB)
	}
	f := tunnelFault(t, up, o.Id.String())
	keys := map[string]bool{}
	for _, tr := range up.TCTrees() {
		for _, c := range tr.Classes {
			if c.FlapKey == "" || c.Down || c.Netem.Flapping == nil || c.Netem.Flapping.Up != 6*time.Second {
				t.Errorf("class %+v", c)
			}
			keys[c.FlapKey] = true
		}
	}
	if !keys[FlapKey(f.Key, Upload)] || !keys[FlapKey(f.Key, Download)] || len(keys) != 2 {
		t.Errorf("flap keys %v", keys)
	}
	// in the down phase both leaves hold the blackout
	down := compileTunnelPhase(w, true)
	for _, tr := range down.TCTrees() {
		for _, c := range tr.Classes {
			if !c.Down || !strings.Contains(c.Config().String(), "loss random 100%") {
				t.Errorf("class %s in the down phase: %s", c.ClassID(), c.Config())
			}
		}
	}
}

func compileTunnelPhase(w *faultWorld, down bool) *Target {
	return w.compile(func(in *Input) {
		in.Keys = wgKeys
		in.FlapPhase = func(string, FlapSpec) bool { return down }
	})
}

// The IFB tree has a class limit of its own, counted like an interface's: the default class and one class per tunnel fault.
func TestTheIFBTreeIsBoundByTheClassLimit(t *testing.T) {
	w := newTunnelWorld(t)
	w.overlay(`{fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 20ms}}}`, 0)
	w.overlay(`{fault: {family: tunnel, tunnel: {link: site-b}, upload: {latency: 20ms}}}`, time.Second)
	ok := w.compile(func(in *Input) {
		in.Keys = wgKeys
		in.PeerEndpoints = map[string]netip.AddrPort{clientA: netip.MustParseAddrPort(epClientA)}
		in.ClassLimit = 3 // the default class and two faults
	})
	if ok.HasErrors() || ok.IFB == nil || len(ok.IFB.TC.Classes) != 2 {
		t.Fatalf("%+v", ok.Problems)
	}
	bad := w.compile(func(in *Input) {
		in.Keys = wgKeys
		in.PeerEndpoints = map[string]netip.AddrPort{clientA: netip.MustParseAddrPort(epClientA)}
		in.ClassLimit = 2
	})
	var found bool
	for _, p := range bad.Problems {
		found = found || p.Code == CodeCapacityExceeded && p.Severity == SevError && strings.Contains(p.Message, "IFB") && p.Scope != ""
	}
	if !found {
		t.Errorf("%+v", bad.Problems)
	}
}

// ---- the WireGuard actions ----------------------------------------------------------------------------

func peersOf(tg *Target, network string) []WGPeer {
	for _, w := range tg.WireGuard {
		if w.NetworkName == network {
			return w.Peers
		}
	}
	return nil
}

// disable takes the peer off its interface: a client is gone with its networks (they are not routed any more), and a link
// is down; the action is effective, and a disable of a peer that is off already is not.
func TestDisableTakesAPeerOffItsInterfaceAndItsRoutesWithIt(t *testing.T) {
	w := newTunnelWorld(t)
	before := compileTunnel(w, nil)
	o1 := w.overlay(`{wireguard: {client: rA, action: disable}}`, 0)
	o2 := w.overlay(`{wireguard: {link: site-b, action: disable}}`, time.Second)
	tg := compileTunnel(w, nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(peersOf(before, "lab-hub")) != 1 || len(peersOf(before, "site-b")) != 1 {
		t.Fatalf("the fixture has no peers: %+v", before.WireGuard)
	}
	if len(peersOf(tg, "lab-hub")) != 0 || len(peersOf(tg, "site-b")) != 0 {
		t.Errorf("peers are still on their interfaces: %+v", tg.WireGuard)
	}
	for _, r := range tg.Routes {
		if r.Dst == "10.50.0.0/24" || r.Dst == "10.60.0.0/24" {
			t.Errorf("a route of a disabled peer: %+v", r)
		}
	}
	var got []string
	for _, a := range tg.WGActions {
		got = append(got, a.Action+" "+a.Tunnel+" "+a.Overlay)
	}
	want := []string{"disable client:" + clientA + " " + o1.Id.String(), "disable link:" + linkID + " " + o2.Id.String()}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("actions %v, want %v", got, want)
	}
	// the interfaces themselves stay: only the peers are gone
	for _, name := range []string{"wg-lab-hub", "wg-site-b"} {
		if !contains(tg.Interfaces, name) {
			t.Errorf("%s is gone", name)
		}
	}
}

// A client that is disabled in the configuration is off its interface already: the action changes nothing and is not
// reported as effective.
func TestDisablingAClientThatIsOffAlreadyChangesNothing(t *testing.T) {
	w := newTunnelWorld(t)
	w.overlay(`{wireguard: {client: rB, action: disable}}`, 0)
	tg := compileTunnel(w, nil)
	if len(tg.WGActions) != 0 {
		t.Errorf("%+v", tg.WGActions)
	}
}

// key_mismatch gives the gateway a key for the peer that nobody holds the private key of, and the same one at every
// compile of the same overlay, so that a re-apply keeps what the kernel holds.
func TestKeyMismatchReplacesThePublicKeyByOneDerivedFromTheOverlay(t *testing.T) {
	w := newTunnelWorld(t)
	orig := peersOf(compileTunnel(w, nil), "lab-hub")[0]
	o := w.overlay(`{wireguard: {client: rA, action: key_mismatch}}`, 0)
	a := compileTunnel(w, nil)
	b := compileTunnel(w, nil)
	pa, pb := peersOf(a, "lab-hub")[0], peersOf(b, "lab-hub")[0]
	if pa.PublicKey == orig.PublicKey || pa.PublicKey != pb.PublicKey || pa.Action != "key_mismatch" {
		t.Fatalf("original %s, compiled %s and %s, action %q", orig.PublicKey, pa.PublicKey, pb.PublicKey, pa.Action)
	}
	if k, err := base64.StdEncoding.DecodeString(pa.PublicKey); err != nil || len(k) != 32 {
		t.Errorf("not a public key: %v", err)
	}
	// everything else of the peer is as it was: the addresses, the networks, the preshared key
	if strings.Join(pa.AllowedIPs, ",") != strings.Join(orig.AllowedIPs, ",") || pa.PresharedKeyRef != orig.PresharedKeyRef || strings.Join(pa.Routes, ",") != strings.Join(orig.Routes, ",") {
		t.Errorf("%+v vs %+v", pa, orig)
	}
	if orig.Action != "" {
		t.Errorf("the action is on the peer without an overlay: %q", orig.Action)
	}
	// another overlay, another key
	w.dropOverlay(o.Id.String())
	w.overlay(`{wireguard: {client: rA, action: key_mismatch}}`, time.Hour)
	c := peersOf(compileTunnel(w, nil), "lab-hub")[0]
	if c.PublicKey == pa.PublicKey {
		t.Error("two overlays derive the same key")
	}
	// the link has the action as well
	w2 := newTunnelWorld(t)
	w2.overlay(`{wireguard: {link: site-b, action: key_mismatch}}`, 0)
	lp := peersOf(compileTunnel(w2, nil), "site-b")[0]
	if lp.Action != "key_mismatch" || lp.PublicKey == "sckuQf8RqKUj8wQ5b3zY+eRVCD0nfD1C6ns80SJSqxM=" {
		t.Errorf("%+v", lp)
	}
}

// block_endpoint drops the encrypted UDP of the peer in both directions, by a verdict map keyed on peer address, peer port
// and the port of the interface, at raw priority. The counter of the overlay counts what it dropped. A peer with no address
// cannot be blocked yet.
func TestBlockEndpointDropsTheEncryptedUDPOfThePeerInBothDirections(t *testing.T) {
	w := newTunnelWorld(t)
	o := w.overlay(`{wireguard: {client: rA, action: block_endpoint}}`, 0)
	tg := compileTunnel(w, map[string]string{clientA: epClientA})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	out, in := findChain(tg, WGBlockOutChain), findChain(tg, WGBlockInChain)
	if out == nil || in == nil || out.Base.Hook != "output" || in.Base.Hook != "input" || out.Base.Prio != -300 || in.Base.Prio != -300 {
		t.Fatalf("out %+v in %+v", out, in)
	}
	var maps = map[string]MapDef{}
	for _, m := range tg.Nft.Maps {
		if strings.HasPrefix(m.Name, "wgblk_") {
			maps[strings.TrimSuffix(strings.TrimPrefix(m.Name, "wgblk_"), m.Name[strings.LastIndex(m.Name, "_"):])] = m
		}
	}
	if len(maps) != 2 {
		t.Fatalf("maps %+v", maps)
	}
	chain := WGBlockChainPrefix + shortID(o.Id.String())
	for side, m := range maps {
		if len(m.Elements) != 1 || m.Elements[0].Key != "198.51.100.2 . 51820 . 51820" || m.Elements[0].Value != chain || strings.Join(m.KeyType, ",") != "ipv4_addr,inet_service,inet_service" {
			t.Errorf("%s: %+v", side, m)
		}
	}
	if c := findChain(tg, chain); c == nil || len(c.Rules) != 1 || !strings.Contains(ruleJSON(c)[0], `"drop"`) || !strings.Contains(ruleJSON(c)[0], chain) {
		t.Errorf("%+v", c)
	}
	if !contains(tg.Nft.Counters, chain) {
		t.Errorf("counters %v", tg.Nft.Counters)
	}
	if len(tg.WGActions) != 1 || tg.WGActions[0].Counter != chain || tg.WGActions[0].Action != "block_endpoint" {
		t.Errorf("%+v", tg.WGActions)
	}
	goldenText(t, "wgblock", describeTunnels(tg))

	// no address, no block: the compiler warns, the overlay is not at work
	tg = compileTunnel(w, nil)
	if findChain(tg, WGBlockOutChain) != nil || len(tg.WGActions) != 0 {
		t.Errorf("a block without an address is compiled: %+v", tg.WGActions)
	}
	var warned bool
	for _, p := range tg.Problems {
		warned = warned || p.Code == CodeTunnelEndpointUnknown
	}
	if !warned || tg.HasErrors() {
		t.Errorf("%+v", tg.Problems)
	}
	// a link that names its endpoint is blocked at it
	w2 := newTunnelWorld(t)
	w2.overlay(`{wireguard: {link: site-b, action: block_endpoint}}`, 0)
	l := compileTunnel(w2, nil)
	var el []MapElement
	for _, m := range l.Nft.Maps {
		if strings.HasPrefix(m.Name, "wgblk_out_") {
			el = m.Elements
		}
	}
	if len(el) != 1 || el[0].Key != "203.0.113.40 . 51821 . 51821" {
		t.Errorf("%+v", el)
	}
}

// Without tunnel faults and WireGuard actions the target has none of it: no IFB, no output hook, no extra map, and the
// golden files of the other families do not change (they are checked by their own tests).
func TestWithoutTunnelFaultsThereIsNoTunnelMachinery(t *testing.T) {
	w := newTunnelWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {latency: 30ms}}`, 0)
	tg := compileTunnel(w, map[string]string{clientA: epClientA})
	if tg.IFB != nil || tg.Endpoints != nil || len(tg.WGActions) != 0 || findChain(tg, TunnelOutChain) != nil || findChain(tg, WGBlockOutChain) != nil || contains(tg.Interfaces, IFBDev) {
		t.Errorf("%+v", tg.IFB)
	}
	for _, m := range tg.Nft.Maps {
		if strings.HasPrefix(m.Name, "tun_out") || strings.HasPrefix(m.Name, "wgblk") {
			t.Errorf("map %s", m.Name)
		}
	}
}

// The tunnel scenario of the kernel gates: both directions, a flapping one in its down phase, a blocked endpoint, a disabled
// peer and a key mismatch.
func scenarioTunnel(t *testing.T) *Target {
	t.Helper()
	w := newTunnelWorld(t)
	w.addConfigFault("5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b", `{name: a-40, source: {device: esp32-42}, upload: {latency: 40ms}, download: {latency: 40ms}}`, 0)
	w.overlay(`{fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 20ms, jitter: 5ms}, download: {latency: 50ms, loss: 1%}}}`, time.Second)
	w.overlay(`{fault: {family: tunnel, tunnel: {link: site-b}, flapping: {up: 6s, down: 4s}, burst_loss: {p: 2%, r: 20%}}}`, 2*time.Second)
	w.overlay(`{wireguard: {client: rA, action: block_endpoint}}`, 3*time.Second)
	w.overlay(`{wireguard: {link: site-b, action: key_mismatch}}`, 4*time.Second)
	up := compileTunnel(w, map[string]string{clientA: epClientA})
	down := map[string]bool{}
	for _, tr := range up.TCTrees() {
		for _, c := range tr.Classes {
			if c.FlapKey != "" {
				down[c.FlapKey] = true
			}
		}
	}
	return w.compile(func(in *Input) {
		in.Keys = wgKeys
		in.PeerEndpoints = map[string]netip.AddrPort{clientA: netip.MustParseAddrPort(epClientA)}
		in.FlapPhase = func(key string, _ FlapSpec) bool { return down[key] }
	})
}

func TestGoldenTunnelTarget(t *testing.T) {
	tg := scenarioTunnel(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	goldenText(t, "tunnel", describeFaults(tg)+describeTunnels(tg))
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := executorCheck(tx); err != nil {
		t.Fatal(err)
	}
}
