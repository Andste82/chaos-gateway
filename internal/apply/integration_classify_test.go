//go:build testbed

package apply_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The classification tests of plan §3.3 (M7), driven since M8a by real faults: overlays on the
// devices of the testbed are resolved by the domain layer, the compiler gives each winner its id,
// fills the lookup maps and builds the tc tree, and the tests install that tree and read the
// counters of its classes (plan: "a test tc class per (id, direction), per-class counters increase
// only for matching traffic"). Applying the tc tree is not part of apply.Apply yet (M8b), so
// installTC does it with the executor, the way the fault engine will.

// classifyDevices are the devices the tests give faults to: A and B in the IoT network, C in Lab,
// and the management peer, which is no test traffic at all.
var classifyDevices = []struct{ name, addr string }{
	{"dev-a", testbed.ClientAAddr},
	{"dev-b", testbed.ClientBAddr},
	{"dev-c", testbed.ClientCAddr},
	{"mgmt-peer", testbed.MgmtPeer},
}

// withDevices adds the devices to the configuration (identified by their address, so the identity
// resolves them without any observation) and returns their UUIDs by name.
func (g *gw) withDevices() map[string]string {
	g.t.Helper()
	devs := map[string]model.Device{}
	if g.cfg.Devices != nil {
		devs = *g.cfg.Devices
	}
	for i, d := range classifyDevices {
		devs[fmt.Sprintf("00000000-0000-4000-8000-0000000000d%d", i)] = model.Device{
			Name: d.name, Identifiers: &model.DeviceIdentifiers{Ipv4: &[]string{d.addr}},
		}
	}
	g.cfg.Devices = &devs
	norm, errs := domain.Normalize(g.cfg)
	if len(errs) != 0 {
		g.t.Fatalf("normalize: %v", errs)
	}
	g.cfg = norm
	ids := map[string]string{}
	for id, d := range *g.cfg.Devices {
		ids[d.Name] = id
	}
	return ids
}

// overlay creates an overlay from a YAML request body.
func (g *gw) overlay(body string) model.Overlay {
	g.t.Helper()
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		g.t.Fatalf("%s: %v", body, err)
	}
	norm, errs := domain.ValidateOverlay(g.cfg, req)
	if len(errs) != 0 {
		g.t.Fatalf("%s: %v", body, errs)
	}
	o, err := domain.NewOverlay(*norm, model.Owner{Type: "user", Id: "test"}, uuid.New(), time.Now())
	if err != nil {
		g.t.Fatal(err)
	}
	return o
}

// compileFaults compiles the configuration with the overlays, with the identity the configuration
// itself gives (the devices' addresses).
func (g *gw) compileFaults(overlays ...model.Overlay) *compiler.Target {
	g.t.Helper()
	id := domain.ResolveIdentity(g.cfg, domain.Observed{}, nil)
	return g.compile(func(in *compiler.Input) { in.Overlays, in.Identity = overlays, &id })
}

// faultOf returns the fault id of an overlay, for the device (empty: the fault's shared id).
func faultOf(t *testing.T, tg *compiler.Target, o model.Overlay, device string) compiler.Fault {
	t.Helper()
	for _, f := range tg.Faults {
		if f.Source == o.Id.String() && f.Device == device {
			return f
		}
	}
	t.Fatalf("no fault id for overlay %s (device %q) in %+v", o.Id, device, tg.Faults)
	return compiler.Fault{}
}

func classOf(t *testing.T, tg *compiler.Target, id int, dir compiler.Direction) compiler.TCClass {
	t.Helper()
	if tg.TC != nil {
		for _, c := range tg.TC.Classes {
			if c.ID == id && c.Dir == dir {
				return c
			}
		}
	}
	t.Fatalf("no tc class for id %d %s", id, dir)
	return compiler.TCClass{}
}

// installTC puts the target's tc tree on the interfaces. A root that is already there stays (HTB
// cannot be changed in place), everything else is replaced, so it can be called again after the
// target changed, which adds the classes of new ids.
func (g *gw) installTC(tg *compiler.Target, devs ...string) {
	g.t.Helper()
	if tg.TC == nil {
		g.t.Fatal("the target has no tc tree")
	}
	if len(devs) == 0 {
		devs = tg.TC.Devs
	}
	for _, dev := range devs {
		out, err := g.exec().Do(context.Background(), &executor.Read{Target: executor.Target{NS: g.ns()}, What: executor.ReadQdiscs, Dev: dev})
		if err != nil {
			g.t.Fatalf("read the qdiscs of %s: %v", dev, err)
		}
		var qs []linux.Qdisc
		if err := json.Unmarshal(out.Data[0], &qs); err != nil {
			g.t.Fatal(err)
		}
		hasRoot := false
		for _, q := range qs {
			hasRoot = hasRoot || q.Kind == "htb" && q.Root
		}
		if _, err := g.exec().Do(context.Background(), &executor.TC{Target: executor.Target{NS: g.ns()}, Entries: tg.TC.Entries(dev, !hasRoot)}); err != nil {
			g.t.Fatalf("install the tc tree on %s: %v\n%s", dev, err, strings.Join(tg.TC.Lines(dev), "\n"))
		}
	}
}

// classPackets reads the packet count of the class of (id, direction) on dev.
func classPackets(t *testing.T, ns *testbed.Namespace, dev string, c compiler.TCClass) int64 {
	t.Helper()
	out := ns.Must("tc", "-j", "-s", "class", "show", "dev", dev)
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse tc -j -s class show dev %s: %v\n%s", dev, err, out)
	}
	for _, e := range entries {
		if h, _ := e["handle"].(string); h != c.ClassID() {
			continue
		}
		if n, ok := tcPacketCount(e); ok {
			return n
		}
		t.Fatalf("class %s has no packet count: %+v", c.ClassID(), e)
	}
	t.Fatalf("class %s not found on %s in:\n%s", c.ClassID(), dev, out)
	return -1
}

// tcPacketCount finds the packet counter in a tc -j -s entry: directly as "packets", or nested
// under "stats"/"stats2" depending on the iproute2 version.
func tcPacketCount(e map[string]any) (int64, bool) {
	if v, ok := e["packets"].(float64); ok {
		return int64(v), true
	}
	for _, k := range []string{"stats", "stats2"} {
		if s, ok := e[k].(map[string]any); ok {
			if n, ok := tcPacketCount(s); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// counterPackets reads a named nft counter of the table (the counters of a fault id, plan §3.2).
func counterPackets(t *testing.T, ns *testbed.Namespace, name string) int64 {
	t.Helper()
	out := ns.Must("nft", "-j", "list", "counter", "inet", "chaosgw", name)
	var doc struct {
		Nftables []struct {
			Counter *struct{ Packets int64 } `json:"counter"`
		}
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parse counter %s: %v\n%s", name, err, out)
	}
	for _, o := range doc.Nftables {
		if o.Counter != nil {
			return o.Counter.Packets
		}
	}
	t.Fatalf("no counter %s in %s", name, out)
	return -1
}

// bridgeIf returns the interface name of the bridge owning network netID.
func bridgeIf(t *testing.T, tg *compiler.Target, netID string) string {
	t.Helper()
	for _, b := range tg.Bridges {
		if b.NetworkID == netID {
			return b.Name
		}
	}
	t.Fatalf("no bridge for network %s", netID)
	return ""
}

const (
	classifyIotID = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21" // "IoT", lan0, A/B
	classifyLabID = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32" // "Lab", lan1, C
)

// M7 test: with a test tc class per (id, direction), per-class counters increase only for matching
// traffic, in both directions, behind NAT (plan §3.3's acceptance list). A's upload to the server
// leaves through wan0 (after NAT); the server's reply leaves back through the IoT bridge towards A.
func TestClassificationMarksOnlyMatchingTrafficBehindNAT(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	o := g.overlay(`{target: {device: dev-a}, fault: {latency: 1ms}}`)
	tg := g.compileFaults(o)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	g.apply(tg)
	g.installTC(tg)
	f := faultOf(t, tg, o, "")
	iotIf := bridgeIf(t, tg, classifyIotID)
	up, down := classOf(t, tg, f.ID, compiler.Upload), classOf(t, tg, f.ID, compiler.Download)

	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach the server: loss %.0f%%\n%s", r.Loss()*100, g.dump())
	}
	if n := classPackets(t, g.top.GW, "wan0", up); n == 0 {
		t.Error("the upload class on wan0 saw no packets")
	}
	if n := classPackets(t, g.top.GW, iotIf, down); n == 0 {
		t.Error("the download class on the IoT bridge saw no packets")
	}
	if n := counterPackets(t, g.top.GW, f.CounterUp); n == 0 {
		t.Error("the fault's upload counter counted nothing")
	}
	if n := counterPackets(t, g.top.GW, f.CounterDown); n == 0 {
		t.Error("the fault's download counter counted nothing")
	}
	// B is in the same network, but the fault is A's: its traffic must not be counted either.
	if r := testbed.MustPing(t, g.top.B, testbed.ServerAddr, 2, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("B cannot reach the server: %s", g.dump())
	}
	before := classPackets(t, g.top.GW, "wan0", up)
	beforeCounter := counterPackets(t, g.top.GW, f.CounterUp)
	if r := testbed.MustPing(t, g.top.B, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("%s", g.dump())
	}
	if after := classPackets(t, g.top.GW, "wan0", up); after != before {
		t.Errorf("B's traffic (no fault of its own) was counted: %d -> %d", before, after)
	}
	if after := counterPackets(t, g.top.GW, f.CounterUp); after != beforeCounter {
		t.Errorf("B's traffic was counted by A's fault counter: %d -> %d", beforeCounter, after)
	}
	// the "wrong" direction classes stay at 0 for A's own traffic too.
	if n := classPackets(t, g.top.GW, "wan0", down); n != 0 {
		t.Errorf("the download class on wan0 counted %d packets: upload traffic does not leave there", n)
	}
	if n := classPackets(t, g.top.GW, iotIf, up); n != 0 {
		t.Errorf("the upload class on the IoT bridge counted %d packets: A's upload leaves through wan0", n)
	}
}

// M7 test: classification holds across two test networks (plan §3.3, spike S11): the same (id,
// direction) mapping applies on both bridges, although the IoT bridge carries A's reply traffic
// (download) while the Lab bridge carries A's own request traffic (upload) toward C — the
// motivating case for the direction bit (plan §3.3: "the second network's interface carries both
// its own devices' downloads and the first network's uploads towards it").
func TestClassificationAcrossTwoTestNetworks(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	g.cfg.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{
		{From: model.MatrixEndpoint{Network: ptr(classifyIotID)}, To: model.MatrixEndpoint{Network: ptr(classifyLabID)}, Policy: model.MatrixEntryPolicyAllow},
		{From: model.MatrixEndpoint{Network: ptr(classifyLabID)}, To: model.MatrixEndpoint{Network: ptr(classifyIotID)}, Policy: model.MatrixEntryPolicyAllow},
	}}
	o := g.overlay(`{target: {device: dev-a}, fault: {latency: 1ms}}`)
	tg := g.compileFaults(o)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	g.apply(tg)
	g.installTC(tg)
	f := faultOf(t, tg, o, "")
	iotIf := bridgeIf(t, tg, classifyIotID)
	labIf := bridgeIf(t, tg, classifyLabID)
	up, down := classOf(t, tg, f.ID, compiler.Upload), classOf(t, tg, f.ID, compiler.Download)

	if r := testbed.MustPing(t, g.top.A, testbed.ClientCAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach C: loss %.0f%%\n%s", r.Loss()*100, g.dump())
	}
	if n := classPackets(t, g.top.GW, labIf, up); n == 0 {
		t.Error("A's upload (egressing the Lab bridge towards C) was not counted")
	}
	if n := classPackets(t, g.top.GW, iotIf, down); n == 0 {
		t.Error("C's reply (egressing the IoT bridge back to A, download) was not counted")
	}
	if n := classPackets(t, g.top.GW, labIf, down); n != 0 {
		t.Errorf("the Lab bridge's download class counted %d: nothing of A's leaves there in that direction", n)
	}
	if n := classPackets(t, g.top.GW, iotIf, up); n != 0 {
		t.Errorf("the IoT bridge's upload class counted %d: A's upload leaves through the Lab bridge here", n)
	}
}

// M7 test: non-test traffic (the gateway's own management traffic) keeps its mark untouched. The
// management peer is given a fault of its own, so the lookup maps do hold an element for its
// address: the proof is that the guard (only test, WireGuard and remote-network traffic is
// classified, plan §3.3) is what protects it, not merely the absence of a matching element.
func TestNonTestTrafficKeepsItsMarkUntouched(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	oPeer := g.overlay(`{target: {device: mgmt-peer}, fault: {latency: 1ms}}`)
	oA := g.overlay(`{target: {device: dev-a}, fault: {latency: 1ms}}`)
	tg := g.compileFaults(oPeer, oA)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	peer, a := faultOf(t, tg, oPeer, ""), faultOf(t, tg, oA, "")
	if m := tg.Nft.Maps; !hasKey(m, tg.ClassifyMaps["dev"], testbed.MgmtPeer) {
		t.Fatalf("the management peer has no element in the maps, so the guard is not what is tested: %+v", m)
	}
	g.apply(tg)
	// the management interface is where the peer's packets leave
	g.installTC(tg, append(tg.TC.Devs, "mgmt0")...)
	peerUp, peerDown := classOf(t, tg, peer.ID, compiler.Upload), classOf(t, tg, peer.ID, compiler.Download)

	if r := testbed.MustPing(t, g.top.Mgmt, testbed.MgmtGateway, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("management cannot reach the gateway: %s", g.dump())
	}
	if n := classPackets(t, g.top.GW, "mgmt0", peerUp); n != 0 {
		t.Errorf("management traffic's mark was written (upload class got %d packets) although it is not test traffic", n)
	}
	if n := classPackets(t, g.top.GW, "mgmt0", peerDown); n != 0 {
		t.Errorf("management traffic's mark was written (download class got %d packets) although it is not test traffic", n)
	}
	if n := counterPackets(t, g.top.GW, peer.CounterUp) + counterPackets(t, g.top.GW, peer.CounterDown); n != 0 {
		t.Errorf("the management peer's fault counted %d packets", n)
	}
	// meanwhile A, which IS test traffic, still gets classified with its own id on its own
	// interface, proving the probe and the map elements both work; only the guard tells them apart.
	iotIf := bridgeIf(t, tg, classifyIotID)
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("%s", g.dump())
	}
	if n := classPackets(t, g.top.GW, iotIf, classOf(t, tg, a.ID, compiler.Download)); n == 0 {
		t.Error("A's own (test) traffic was not classified: the setup itself is broken")
	}
}

func hasKey(maps []compiler.MapDef, name, key string) bool {
	for _, m := range maps {
		if m.Name != name {
			continue
		}
		for _, e := range m.Elements {
			if e.Key == key {
				return true
			}
		}
	}
	return false
}

// M7 test: a change of the maps moves an established connection to its new class at once (plan
// §3.3, "per packet, not per connection"): the classification maps hold the resolved id for every
// packet, so an existing conntrack entry is not cached onto the old id (S10's own finding). Here the
// overlay of A is replaced by another one (another id); the new target is a full apply, whose
// transaction swaps the maps and the mark chains atomically (plan §3.2).
func TestAMapChangeMovesAnEstablishedConnectionToItsNewClass(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	oOld := g.overlay(`{target: {device: dev-a}, fault: {latency: 1ms}}`)
	tgOld := g.compileFaults(oOld)
	if tgOld.HasErrors() {
		t.Fatalf("%+v", tgOld.Problems)
	}
	g.apply(tgOld)
	g.installTC(tgOld)
	iotIf := bridgeIf(t, tgOld, classifyIotID)
	fOld := faultOf(t, tgOld, oOld, "")
	oldUp, oldDown := classOf(t, tgOld, fOld.ID, compiler.Upload), classOf(t, tgOld, fOld.ID, compiler.Download)

	// one long-lived ping, so the same conntrack entry (the same ICMP id, the same "connection") -
	// not a fresh one each time - spans the change below.
	ping := g.top.A.Start("ping", "-i", "0.2", "-w", "600", "-n", testbed.ServerAddr)
	t.Cleanup(ping.Stop)
	waitClassPackets(t, g.top.GW, "wan0", oldUp, 5*time.Second, "the connection was never classified under the old id")
	oldBefore := classPackets(t, g.top.GW, iotIf, oldDown)

	// the overlay is replaced: another overlay (another fault) on the same device. Make before
	// break (plan §3.2): the new classes exist before the maps point to them.
	oNew := g.overlay(`{target: {device: dev-a}, fault: {latency: 2ms}}`)
	tgNew := g.compileFaults(oNew)
	if tgNew.HasErrors() {
		t.Fatalf("%+v", tgNew.Problems)
	}
	fNew := faultOf(t, tgNew, oNew, "")
	if fNew.ID == fOld.ID {
		t.Fatalf("the new fault took the old id %d: the classes could not be told apart", fOld.ID)
	}
	g.installTC(tgNew)
	g.apply(tgNew)

	newUp, newDown := classOf(t, tgNew, fNew.ID, compiler.Upload), classOf(t, tgNew, fNew.ID, compiler.Download)
	waitClassPackets(t, g.top.GW, "wan0", newUp, 5*time.Second, "the same connection's later packets were never classified under the new id")
	if n := classPackets(t, g.top.GW, iotIf, newDown); n == 0 {
		t.Error("the connection's later reply packets were not classified under the new id")
	}
	// the old class still exists (the fault engine removes it later) but nothing new arrives in it
	time.Sleep(500 * time.Millisecond)
	settled := classPackets(t, g.top.GW, iotIf, oldDown)
	time.Sleep(1 * time.Second)
	if n := classPackets(t, g.top.GW, iotIf, oldDown); n != settled {
		t.Errorf("the old class kept counting after the maps pointed elsewhere: %d -> %d (it had %d when the change began)", settled, n, oldBefore)
	}
}

// waitClassPackets polls until the class on dev has seen at least one packet.
func waitClassPackets(t *testing.T, ns *testbed.Namespace, dev string, c compiler.TCClass, d time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if classPackets(t, ns, dev, c) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// twoLevelFaults gives A a fault of its own for all its traffic (level 4: the "dev" map) and
// another for its traffic to the server (level 2: the "devdest" map). Both entries exist in the
// kernel at the same time with different ids, and for A's traffic to the server both lookups would
// match: only the order of the chain decides.
func (g *gw) twoLevelFaults() (tg *compiler.Target, def, specific compiler.Fault) {
	g.t.Helper()
	g.withDevices()
	oDef := g.overlay(`{target: {device: dev-a}, fault: {latency: 1ms}}`)
	oSpec := g.overlay(`{target: {device: dev-a}, fault: {destination: {cidr: ` + testbed.ServerAddr + `}, latency: 2ms}}`)
	tg = g.compileFaults(oDef, oSpec)
	if tg.HasErrors() {
		g.t.Fatalf("%+v", tg.Problems)
	}
	def, specific = faultOf(g.t, tg, oDef, ""), faultOf(g.t, tg, oSpec, "")
	if !hasKey(tg.Nft.Maps, tg.ClassifyMaps["devdest"], testbed.ClientAAddr+" . "+testbed.ServerAddr) || !hasKey(tg.Nft.Maps, tg.ClassifyMaps["dev"], testbed.ClientAAddr) {
		g.t.Fatalf("the two levels do not both hold an entry for A: %+v", tg.Nft.Maps)
	}
	return tg, def, specific
}

// M7 carry-over (plan §3.3, "first match": S11 verified levels 2 and 4 together): two levels of the
// lookup chain hold conflicting entries for the same traffic at the same time in the real kernel,
// and the more specific one wins, for exactly the traffic it names.
func TestTheMoreSpecificLevelWinsWhenTwoLevelsHoldEntries(t *testing.T) {
	g := newGateway(t)
	tg, def, specific := g.twoLevelFaults()
	g.apply(tg)
	g.installTC(tg)
	iotIf := bridgeIf(t, tg, classifyIotID)
	defUp, defDown := classOf(t, tg, def.ID, compiler.Upload), classOf(t, tg, def.ID, compiler.Download)
	specUp, specDown := classOf(t, tg, specific.ID, compiler.Upload), classOf(t, tg, specific.ID, compiler.Download)

	// to the server: the device+destination entry (level 2) wins over the device entry (level 4)
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach the server: %s", g.dump())
	}
	if n := classPackets(t, g.top.GW, "wan0", specUp); n == 0 {
		t.Errorf("A's upload to the server did not go through the more specific fault's class\ndef %+v\nspec %+v\n%s\n%s", def, specific, g.dump(), g.top.GW.Must("tc", "-s", "class", "show", "dev", "wan0"))
	}
	if n := classPackets(t, g.top.GW, iotIf, specDown); n == 0 {
		t.Error("the server's reply did not go through the more specific fault's class")
	}
	if n := classPackets(t, g.top.GW, "wan0", defUp) + classPackets(t, g.top.GW, iotIf, defDown); n != 0 {
		t.Errorf("the device-level fault's classes counted %d packets of traffic the more specific level names", n)
	}

	// to another server: only the device entry names it
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr2, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach the second server: %s", g.dump())
	}
	if n := classPackets(t, g.top.GW, "wan0", defUp); n == 0 {
		t.Error("A's upload to the other server did not go through the device-level fault's class")
	}
	if n := classPackets(t, g.top.GW, iotIf, defDown); n == 0 {
		t.Error("the other server's reply did not go through the device-level fault's class")
	}
	// ... and nothing of it landed in the more specific class
	if n := classPackets(t, g.top.GW, "wan0", specUp); n > 3+1 {
		t.Errorf("the more specific class counted %d upload packets: more than the 3 of the ping to the server it names", n)
	}
}

// The regression guard of the test above: the chain's order is what makes the specific level win.
// A chain with the levels reordered (the device level first) classifies the same traffic into the
// less specific fault, so the test above fails for it, and so it would for any change of the
// order. Here the reordered chain is applied for real to show exactly that.
func TestAReorderedLookupChainIsCaught(t *testing.T) {
	g := newGateway(t)
	tg, def, specific := g.twoLevelFaults()
	c := chainOf(tg, compiler.ClassifyChain)
	// rules: guard, two direction writes, then the four levels in the order devdestport, devdest,
	// devport, dev: the device level goes first
	if len(c.Rules) != 7 {
		t.Fatalf("%d rules in the classify chain", len(c.Rules))
	}
	c.Rules[3], c.Rules[4], c.Rules[5], c.Rules[6] = c.Rules[6], c.Rules[3], c.Rules[4], c.Rules[5]
	g.apply(tg)
	g.installTC(tg)
	defUp := classOf(t, tg, def.ID, compiler.Upload)
	specUp := classOf(t, tg, specific.ID, compiler.Upload)

	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach the server: %s", g.dump())
	}
	if n := classPackets(t, g.top.GW, "wan0", defUp); n == 0 {
		t.Error("with the device level first, the traffic should have gone through the device-level fault: the reordering is not what it takes to fail")
	}
	if n := classPackets(t, g.top.GW, "wan0", specUp); n != 0 {
		t.Errorf("with the device level first, the more specific fault still counted %d packets", n)
	}
}

func chainOf(tg *compiler.Target, name string) *compiler.Chain {
	for i := range tg.Nft.Chains {
		if tg.Nft.Chains[i].Name == name {
			return &tg.Nft.Chains[i]
		}
	}
	return nil
}

// Plan §3.2: the named counters of a fault survive every apply and stay monotonic. A full apply
// flushes the maps and the chains, not the counters; one of a removed fault goes with it.
func TestTheCountersOfAFaultSurviveAnApplyAndGoWithTheFault(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	o := g.overlay(`{target: {device: dev-a}, fault: {latency: 1ms}}`)
	tg := g.compileFaults(o)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	g.apply(tg)
	f := faultOf(t, tg, o, "")
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach the server: %s", g.dump())
	}
	before := counterPackets(t, g.top.GW, f.CounterUp)
	if before == 0 {
		t.Fatal("the counter counted nothing")
	}
	// the same faults again: a full apply (new generation) that rewrites chains and maps
	tg2 := g.compileFaults(o)
	if tg2.Hash != tg.Hash {
		t.Fatalf("the same input compiled to another target: %s / %s", tg.Hash, tg2.Hash)
	}
	g.apply(tg2)
	if after := counterPackets(t, g.top.GW, f.CounterUp); after < before {
		t.Errorf("the counter went back from %d to %d", before, after)
	}
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 2, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("%s", g.dump())
	}
	if after := counterPackets(t, g.top.GW, f.CounterUp); after <= before {
		t.Errorf("the counter did not go on counting after the apply: %d -> %d", before, after)
	}
	// the fault is removed: its counters are deleted with it
	g.apply(g.compileFaults())
	if out, err := g.top.GW.Run(context.Background(), "nft", "list", "counter", "inet", "chaosgw", f.CounterUp); err == nil {
		t.Errorf("the counter of the removed fault is still there: %s", out)
	}
}
