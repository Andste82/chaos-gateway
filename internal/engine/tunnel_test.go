package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// Tunnel faults and WireGuard actions in the engine (M10, plan §2.2.1) on the simulated kernel: the address of the peer is
// read from the interface when the overlay is applied, a peer that moves takes its filters with it, the end of a fault keeps the
// IFB until its queues have drained, and the actions change the peers on their interfaces. The real kernel's behaviour is in
// integration_tunnel_test.go.

const (
	tunClient = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	tunLink   = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	tunBody   = "fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 20ms}, download: {latency: 50ms}}"
)

func newTunnelHarness(t *testing.T) *harness {
	t.Helper()
	h, _ := newWGHarnessFile(t, "tunnels.yaml")
	h.mustApply(h.revision(nil))
	return h
}

// peerKey is the public key the gateway holds for the peer of a network, and the interface it is on.
func (h *harness) peerOf(netID string) (iface, pub string) {
	h.t.Helper()
	for _, w := range h.e.Snapshot().WireGuardInterfaces {
		if w.NetworkID == netID && len(w.Peers) > 0 {
			return w.Name, w.Peers[0].PublicKey
		}
	}
	h.t.Fatalf("no peer on %s", netID)
	return "", ""
}

// connect lets the peer of a network handshake from an address, as the real peer's first packet would.
func (h *harness) connect(netID, endpoint string) {
	h.t.Helper()
	iface, pub := h.peerOf(netID)
	h.k.Handshake(iface, pub, h.clk.Now().Unix(), 100, 100)
	h.k.SetPeerEndpoint(iface, pub, endpoint)
}

func (h *harness) ifbUp() bool { return h.exLinks()["ifb-cgw"] }

func (h *harness) ingressFilters() []string {
	h.t.Helper()
	out, err := apply.Local{E: h.ex}.Do(context.Background(), &executor.Read{What: executor.ReadTC, Dev: "wan0"})
	if err != nil {
		h.t.Fatal(err)
	}
	var tree linux.NormTree
	if err := json.Unmarshal(out.Data[0], &tree); err != nil {
		h.t.Fatal(err)
	}
	var got []string
	for _, f := range tree.Ingress().Filters {
		got = append(got, f.Line())
	}
	return got
}

// verifyTunnelKernel is verifyKernelWithOverlays with the addresses of the peers the applied target was compiled with.
func (h *harness) verifyTunnelKernel() {
	h.t.Helper()
	s := h.e.Snapshot()
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	ends := map[string]netip.AddrPort{}
	for id, ep := range s.PeerEndpoints {
		if a, err := netip.ParseAddrPort(ep); err == nil {
			ends[id] = a
		}
	}
	keys, err := wireguard.InterfaceKeys(s.Config, h.sec)
	if err != nil {
		h.t.Fatal(err)
	}
	tg := compiler.Compile(compiler.Input{Config: s.Config, Host: st.Host(), Overlays: s.Overlays, FaultIDs: s.FaultIDs, PMTUTables: s.PMTUTables, Keys: keys,
		PeerEndpoints: ends, Identity: &s.Identity, Generation: compiler.Generation{Revision: s.Applied.Revision, Seq: s.Applied.Generation},
		ClassLimit: h.classLimit, FlapPhase: h.e.FlapPhase})
	st, err = apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.WantOf(tg))
	if err != nil {
		h.t.Fatal(err)
	}
	st.TCRetiring = map[string]bool{}
	for _, r := range h.e.RetiringTC() {
		st.TCRetiring[r.Key()] = true
	}
	if mm := apply.Verify(tg, st); len(mm) != 0 {
		h.t.Fatalf("the kernel does not match the compile of the snapshot: %v", mm)
	}
}

func TestATunnelFaultIsInTheKernelWhenTheWriteReturnsAtTheAddressTheInterfaceReports(t *testing.T) {
	h := newTunnelHarness(t)
	h.connect(wgHub, "198.51.100.2:51820")
	// no poll has run: the address comes from the interface when the overlay is applied
	res := h.mustPut(alice, tunBody)
	s := h.e.Snapshot()
	if len(s.Faults) != 1 || s.Faults[0].Tunnel == nil || s.Faults[0].Tunnel.Endpoint != "198.51.100.2:51820" || s.Faults[0].Tunnel.PeerName != "rA" {
		t.Fatalf("%+v", s.Faults)
	}
	if !h.ifbUp() {
		t.Fatal("no IFB when the write returned")
	}
	if got := h.ingressFilters(); len(got) != 1 || !strings.Contains(got[0], "198.51.100.2:51820") || !strings.Contains(got[0], "redirect=ifb-cgw") {
		t.Errorf("ingress: %v", got)
	}
	if got := s.PeerEndpoints[tunClient]; got != "198.51.100.2:51820" {
		t.Errorf("endpoints %v", s.PeerEndpoints)
	}
	if s.TunnelIFB != "ifb-cgw" || s.TunnelUplink != "wan0" {
		t.Errorf("snapshot: %q %q", s.TunnelIFB, s.TunnelUplink)
	}
	// the ids of the faults are the fault ids of the overlay
	if s.Faults[0].Source != res.Overlay.Id.String() {
		t.Errorf("%+v", s.Faults[0])
	}
	h.verifyTunnelKernel()

	// the key of the overlay replaces the fault: the id stays, the classes change in place
	id := s.Faults[0].ID
	h.mustPut(alice, "fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 25ms}, download: {latency: 55ms}}")
	if s2 := h.e.Snapshot(); len(s2.Faults) != 1 || s2.Faults[0].ID != id || s2.Faults[0].Upload.Delay != 25*time.Millisecond {
		t.Errorf("%+v", s2.Faults)
	}
	h.verifyTunnelKernel()
}

func TestATunnelFaultOfAPeerThatHasNotConnectedTakesEffectWhenItDoes(t *testing.T) {
	h := newTunnelHarness(t)
	if err := h.e.PollWireGuard(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	h.mustPut(alice, tunBody)
	s := h.e.Snapshot()
	var warned bool
	for _, p := range s.Problems {
		warned = warned || p.Code == compiler.CodeTunnelEndpointUnknown
	}
	if len(s.Faults) != 0 || h.ifbUp() || !warned {
		t.Fatalf("faults %+v, IFB %v, problems %+v", s.Faults, h.ifbUp(), s.Problems)
	}
	if ep, ok := s.PeerEndpoints[tunClient]; !ok || ep != "" {
		t.Errorf("the engine does not know that the address is wanted: %v", s.PeerEndpoints)
	}
	// the peer connects; the next poll sees it and the fault takes effect without anyone writing anything
	h.connect(wgHub, "198.51.100.2:51820")
	h.clk.BlockUntil(1)
	h.clk.Advance(5 * time.Second)
	h.wait(func() bool { return len(h.e.Snapshot().Faults) == 1 && h.ifbUp() }, "the fault to take effect when the peer connected")
	if got := h.e.Snapshot().Faults[0].Tunnel.Endpoint; got != "198.51.100.2:51820" {
		t.Errorf("endpoint %s", got)
	}
	h.verifyTunnelKernel()
}

func TestAPeerThatRoamsTakesItsTunnelFaultWithIt(t *testing.T) {
	h := newTunnelHarness(t)
	h.connect(wgHub, "198.51.100.2:51820")
	if err := h.e.PollWireGuard(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	h.mustPut(alice, tunBody)
	gen := h.e.Snapshot().Generation
	// still there: the poll finds nothing to move
	h.clk.BlockUntil(1)
	h.clk.Advance(5 * time.Second)
	time.Sleep(100 * time.Millisecond)
	if g := h.barrier().Generation; g != gen {
		t.Fatalf("a poll that found the peer where it was applied again (generation %d -> %d)", gen, g)
	}
	h.connect(wgHub, "198.51.100.9:40000")
	h.clk.Advance(5 * time.Second)
	h.wait(func() bool { return h.e.Snapshot().PeerEndpoints[tunClient] == "198.51.100.9:40000" }, "the fault to follow the peer")
	if got := h.ingressFilters(); len(got) != 1 || !strings.Contains(got[0], "198.51.100.9:40000") {
		t.Errorf("ingress: %v", got)
	}
	if f := h.e.Snapshot().Faults; len(f) != 1 || f[0].Tunnel.Endpoint != "198.51.100.9:40000" {
		t.Errorf("%+v", f)
	}
	h.verifyTunnelKernel()
}

func TestTheEndOfATunnelFaultKeepsTheIFBUntilTheQueuesHaveDrainedThenRemovesIt(t *testing.T) {
	h := newTunnelHarness(t)
	h.connect(wgHub, "198.51.100.2:51820")
	res := h.mustPut(alice, tunBody)
	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}
	if got := h.ingressFilters(); len(got) != 0 {
		t.Errorf("the filter that feeds the IFB is still there: %v", got)
	}
	if !h.ifbUp() {
		t.Fatal("the IFB went with the fault, with the packets queued in its class")
	}
	if n := len(h.e.RetiringTC()); n == 0 {
		t.Fatal("nothing waits for the retirer")
	}
	h.verifyTunnelKernel()
	h.clk.Advance(3 * time.Second)
	h.waitRetired()
	if h.ifbUp() {
		t.Error("the IFB outlived its tree")
	}
	// the next write drops it from the interfaces the executor may touch
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 10ms}")
	h.verifyTunnelKernel()
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range st.Assigned {
		if d == "ifb-cgw" {
			t.Errorf("assigned %v", st.Assigned)
		}
	}
}

func TestAFlappingTunnelFaultFlapsOnTheInterfacesAndTheIFBInStep(t *testing.T) {
	h := newTunnelHarness(t)
	h.connect(wgHub, "198.51.100.2:51820")
	h.mustPut(alice, "fault: {family: tunnel, tunnel: {client: rA}, flapping: {up: 20s, down: 10s}, latency: 30ms}")
	f := h.e.Snapshot().Faults[0]
	devs := []string{"br-iot", "wan0"}
	for _, d := range devs {
		if got := h.leafLoss(d, f.ID, compiler.Download); got != 0 {
			t.Fatalf("%s starts with loss %v", d, got)
		}
	}
	if got := h.ifbLeafLoss(f.ID); got != 0 {
		t.Fatalf("the IFB starts with loss %v", got)
	}
	h.clk.Advance(20 * time.Second)
	for _, d := range devs {
		h.waitLoss(d, f.ID, compiler.Download, 1, d+" did not black out")
	}
	h.wait(func() bool { return h.ifbLeafLoss(f.ID) == 1 }, "the IFB did not black out")
	h.verifyTunnelKernel()
	log := h.e.FlapLog()
	if len(log) != 2 || log[0].Devices != 6 { // the five interfaces of the tree (a bridge, the uplink, three WireGuard) and the IFB
		t.Errorf("%+v", log)
	}
	h.clk.Advance(10 * time.Second)
	h.wait(func() bool { return h.ifbLeafLoss(f.ID) == 0 }, "the IFB did not come back")
	h.waitLoss("wan0", f.ID, compiler.Download, 0, "the interfaces did not come back")
}

// ifbLeafLoss is the loss of the leaf of a fault's upload class on the IFB.
func (h *harness) ifbLeafLoss(id int) float64 {
	h.t.Helper()
	parent := compiler.ClassIDOf(id, compiler.Upload)
	for _, q := range h.tcTree("ifb-cgw").Qdiscs {
		if q.Parent == parent && q.Netem != nil {
			return q.Netem.Loss
		}
	}
	h.t.Fatalf("the IFB has no leaf below %s", parent)
	return 0
}

func TestTheIFBTreeCountsAgainstTheClassLimitAndAFaultThatDoesNotFitIsRefused(t *testing.T) {
	h, _ := newWGHarnessCfg(t, "tunnels.yaml", engine.Config{ClassLimit: 2}) // the default class and one class
	h.mustApply(h.revision(nil))
	h.connect(wgHub, "198.51.100.2:51820")
	h.connect(tunLink, "203.0.113.40:51821")
	h.mustPut(alice, tunBody)
	gen := h.e.Snapshot().Generation
	_, err := h.put(alice, "fault: {family: tunnel, tunnel: {link: site-b}, upload: {latency: 20ms}}")
	var ce *engine.CompileError
	if !errors.As(err, &ce) || len(ce.Problems) == 0 || ce.Problems[0].Code != compiler.CodeCapacityExceeded || !strings.Contains(ce.Problems[0].Message, "IFB") {
		t.Fatalf("got %v", err)
	}
	if h.e.Snapshot().Generation != gen || len(h.e.Snapshot().Overlays) != 1 {
		t.Error("a refused overlay changed the state")
	}
}

func TestExplainShowsTheTunnelFaultNextToTheDeviceFaultAndTheKernelIdsOfBoth(t *testing.T) {
	h := newTunnelHarness(t)
	h.connect(wgHub, "198.51.100.2:51820")
	dev := h.mustPut(alice, "target: {network: IoT}\nfault: {upload: {latency: 40ms}, download: {latency: 40ms}}")
	tun := h.mustPut(bob, tunBody)
	ex := h.explain(engine.ExplainQuery{Device: "esp32-42", Dst: "10.50.0.10", Protocol: "tcp", Port: 22})
	byFam := map[string]engine.ExplainFamily{}
	for _, f := range ex.Faults {
		byFam[f.Family] = f
	}
	imp, tf := byFam["impairment"], byFam["tunnel"]
	if imp.Winner == nil || imp.Winner.Id.String() != dev.Overlay.Id.String() || tf.Winner == nil || tf.Winner.Id.String() != tun.Overlay.Id.String() ||
		tf.Tunnel != "client:"+tunClient || tf.Winner.Layer != model.FaultRefLayerOverlay {
		t.Fatalf("%+v", ex.Faults)
	}
	if ex.Kernel == nil || len(ex.Kernel.Tunnels) != 1 || ex.Kernel.Tunnels[0].Endpoint != "198.51.100.2:51820" || ex.Kernel.Tunnels[0].Tunnel != "client:"+tunClient ||
		ex.Kernel.FaultID == 0 || ex.Kernel.Tunnels[0].FaultID == 0 || ex.Kernel.FaultID == ex.Kernel.Tunnels[0].FaultID {
		t.Errorf("%+v", ex.Kernel)
	}
	// traffic that crosses no tunnel meets no tunnel fault
	ex = h.explain(engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.10", Protocol: "tcp", Port: 443})
	for _, f := range ex.Faults {
		if f.Family == "tunnel" {
			t.Errorf("%+v", f)
		}
	}
	if ex.Kernel != nil && len(ex.Kernel.Tunnels) != 0 {
		t.Errorf("%+v", ex.Kernel)
	}
}

func TestWireGuardActionsChangeThePeersOnTheirInterfaces(t *testing.T) {
	h := newTunnelHarness(t)
	iface, pub := h.peerOf(wgHub)
	linkIface, linkPub := h.peerOf(tunLink)
	peers := func(dev string) []string { return h.k.WireGuardPeers(dev) }
	if len(peers(iface)) != 1 || len(peers(linkIface)) != 1 {
		t.Fatalf("%v %v", peers(iface), peers(linkIface))
	}

	// disable: a client and a link are taken off their interfaces, and come back with the overlay's end
	d1 := h.mustPut(alice, "wireguard: {client: rA, action: disable}")
	d2 := h.mustPut(alice, "wireguard: {link: site-b, action: disable}")
	if len(peers(iface)) != 0 || len(peers(linkIface)) != 0 {
		t.Errorf("the peers are on the interfaces: %v %v", peers(iface), peers(linkIface))
	}
	if acts := h.e.Snapshot().WGActions; len(acts) != 2 {
		t.Errorf("%+v", acts)
	}
	h.verifyTunnelKernel()
	for _, r := range []engine.OverlayResult{d1, d2} {
		if _, err := h.e.DeleteOverlay(context.Background(), r.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(peers(iface)) != 1 || len(peers(linkIface)) != 1 || peers(iface)[0] != pub || peers(linkIface)[0] != linkPub {
		t.Errorf("%v %v", peers(iface), peers(linkIface))
	}

	// key_mismatch: the peer is on the interface with a key nobody holds
	km := h.mustPut(alice, "wireguard: {client: rA, action: key_mismatch}")
	if got := peers(iface); len(got) != 1 || got[0] == pub {
		t.Errorf("the key is still the peer's: %v", got)
	}
	h.verifyTunnelKernel()
	if _, err := h.e.DeleteOverlay(context.Background(), km.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}
	if got := peers(iface); len(got) != 1 || got[0] != pub {
		t.Errorf("the key did not come back: %v", got)
	}
}

func TestBlockEndpointIsAChainAndACounterInTheKernelAndFollowsThePeer(t *testing.T) {
	h := newTunnelHarness(t)
	h.connect(wgHub, "198.51.100.2:51820")
	res := h.mustPut(alice, "wireguard: {client: rA, action: block_endpoint}")
	name := compiler.WGBlockChainPrefix + strings.ReplaceAll(res.Overlay.Id.String(), "-", "")[:8]
	if rules := h.chainRules(name); len(rules) != 1 || !strings.Contains(rules[0], "drop") {
		t.Errorf("chain %s: %v", name, rules)
	}
	for _, c := range []string{compiler.WGBlockOutChain, compiler.WGBlockInChain} {
		if len(h.chainRules(c)) != 1 {
			t.Errorf("%s: %v", c, h.chainRules(c))
		}
	}
	h.verifyTunnelKernel()
	if acts := h.e.Snapshot().WGActions; len(acts) != 1 || acts[0].Counter != name {
		t.Errorf("%+v", acts)
	}
	cs, err := h.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cs[name]; !ok {
		t.Errorf("no counter %s among %v", name, cs)
	}
	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}
	if len(h.chainRules(compiler.WGBlockOutChain)) != 0 || len(h.chainRules(name)) != 0 {
		t.Error("the block outlived its overlay")
	}
}

func TestTheCountersOfATunnelFaultAreTheEncryptedPacketsInBothDirections(t *testing.T) {
	h := newTunnelHarness(t)
	h.connect(wgHub, "198.51.100.2:51820")
	h.mustPut(alice, tunBody)
	f := h.e.Snapshot().Faults[0]
	h.k.SetIngressStats("wan0", f.ID, linux.NormStats{Packets: 40, Bytes: 5000})
	cs, err := h.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := cs[f.CounterUp]; v.Packets != 40 || v.Bytes != 5000 {
		t.Errorf("the packets from the peer: %+v", v)
	}
	if _, ok := cs[f.CounterDown]; !ok {
		t.Errorf("the packets towards the peer are an nft counter: %v", cs)
	}
}
