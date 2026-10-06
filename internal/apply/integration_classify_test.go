//go:build testbed

package apply_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M7 test infrastructure: a test tc class per (id, direction), plan §3.3's own acceptance list ("a
// test tc class per id and direction") and tc topology (§3.3: "an HTB root ... one class per active
// (id, direction) ... selected by a fw filter with mask", example handle 0x000a0/0x1fff0 for upload
// and 0x100a0/0x1fff0 for download of id 0x0a). The compiler's own tc compilation is M8b's job
// (plan.md's milestone table: "the tc -j normalizer ... comes with fault verify in M8b"), so the
// class and the filter are set up directly here, the way M7's own classification map elements are
// (TestClassifyIDs's doc comment on compiler.Input): the mechanism is exercised with a chosen test
// id before any real fault exists.

// classifyProbeMask covers exactly the bits classification writes: the 12-bit fault id (bits 4-15)
// and the direction bit (16), plan §3.3's own 0x1fff0.
const classifyProbeMask = 0x1fff0

// classifyHandle is the tc fw filter handle for (id, direction): the same bits the classify chain's
// mark-writing chains set (compiler.MarkIDShift, compiler.MarkDirectionBit).
func classifyHandle(id, dir int) uint32 {
	return uint32(id)<<compiler.MarkIDShift | uint32(dir)<<compiler.MarkDirectionBit
}

func classifyClassID(id, dir int) string { return fmt.Sprintf("1:%d", 100+id*2+dir) }

// addClassifyProbe installs, on dev, an htb root with a default class and one class per (id,
// direction) pair in ids, each selected by an fw filter on exactly the mark bits classification
// writes. It is removed again at the end of the test.
func addClassifyProbe(t *testing.T, ns *testbed.Namespace, dev string, ids []int) {
	t.Helper()
	ns.Must("tc", "qdisc", "add", "dev", dev, "root", "handle", "1:", "htb", "default", "1")
	ns.Must("tc", "class", "add", "dev", dev, "parent", "1:", "classid", "1:1", "htb", "rate", "1000mbit")
	for _, id := range ids {
		for dir := 0; dir < 2; dir++ {
			classid := classifyClassID(id, dir)
			ns.Must("tc", "class", "add", "dev", dev, "parent", "1:", "classid", classid, "htb", "rate", "1000mbit")
			handle := classifyHandle(id, dir)
			ns.Must("tc", "filter", "add", "dev", dev, "parent", "1:", "protocol", "ip", "prio", "1",
				"handle", fmt.Sprintf("%#x/%#x", handle, classifyProbeMask), "fw", "classid", classid)
		}
	}
	t.Cleanup(func() { _, _ = ns.Run(context.Background(), "tc", "qdisc", "del", "dev", dev, "root") })
}

// classifyProbePackets reads the packet count of the (id, direction) class on dev.
func classifyProbePackets(t *testing.T, ns *testbed.Namespace, dev string, id, dir int) int64 {
	t.Helper()
	out := ns.Must("tc", "-j", "-s", "class", "show", "dev", dev)
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("parse tc -j -s class show dev %s: %v\n%s", dev, err, out)
	}
	want := classifyClassID(id, dir)
	for _, e := range entries {
		if h, _ := e["handle"].(string); h != want {
			continue
		}
		if n, ok := tcPacketCount(e); ok {
			return n
		}
		t.Fatalf("class %s has no packet count: %+v", want, e)
	}
	t.Fatalf("class %s not found in:\n%s", want, out)
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

// addClassifyElement adds, through the executor's incremental map operation (plan §3.3, M7's own
// nft_add_map_elements), one element of a classification map that jumps to the per-id mark chain
// TestClassifyIDs created. It is the same operation M8a will use to drive the map with a real
// fault's winning id.
func (g *gw) addClassifyElement(mapName, key string, id int) {
	g.t.Helper()
	op := &executor.NftAddMapElements{Target: executor.Target{NS: g.ns()}, Map: mapName,
		Elements: []executor.NftMapElement{{Key: key, Value: compiler.MarkChainName(id)}}}
	if _, err := g.exec().Do(context.Background(), op); err != nil {
		g.t.Fatalf("add classify element %s -> %d: %v", key, id, err)
	}
}

func (g *gw) delClassifyElement(mapName string, keys ...string) {
	g.t.Helper()
	op := &executor.NftDelMapElements{Target: executor.Target{NS: g.ns()}, Map: mapName, Keys: keys}
	if _, err := g.exec().Do(context.Background(), op); err != nil {
		g.t.Fatalf("delete classify element %v: %v", keys, err)
	}
}

// bridgeIf returns the interface name of the bridge owning network netID (the classify chain's
// guard and lookup chain do not care which physical interface a packet leaves through; this is
// only how the test picks where to look for each direction's tc class).
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
	const id = 11
	tg := g.compile(func(in *compiler.Input) { in.TestClassifyIDs = []int{id} })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	g.apply(tg)
	iotIf := bridgeIf(t, tg, classifyIotID)
	g.addClassifyElement(tg.ClassifyMaps["dev"], testbed.ClientAAddr, id)

	addClassifyProbe(t, g.top.GW, "wan0", []int{id})
	addClassifyProbe(t, g.top.GW, iotIf, []int{id})

	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach the server: loss %.0f%%\n%s", r.Loss()*100, g.dump())
	}
	if n := classifyProbePackets(t, g.top.GW, "wan0", id, 0); n == 0 {
		t.Error("the upload class on wan0 saw no packets")
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, id, 1); n == 0 {
		t.Error("the download class on the IoT bridge saw no packets")
	}
	// B never got a map entry: its traffic (same network, same server) must not be counted either.
	if r := testbed.MustPing(t, g.top.B, testbed.ServerAddr, 2, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("B cannot reach the server: %s", g.dump())
	}
	before := classifyProbePackets(t, g.top.GW, "wan0", id, 0)
	if r := testbed.MustPing(t, g.top.B, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("%s", g.dump())
	}
	if after := classifyProbePackets(t, g.top.GW, "wan0", id, 0); after != before {
		t.Errorf("B's traffic (not in the classification map) was counted: %d -> %d", before, after)
	}
	// the "wrong" direction classes stay at 0 for A's own traffic too.
	if n := classifyProbePackets(t, g.top.GW, "wan0", id, 1); n != 0 {
		t.Errorf("the download class on wan0 counted %d packets: upload traffic does not leave there", n)
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, id, 0); n != 0 {
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
	const id = 12
	g.cfg.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{
		{From: model.MatrixEndpoint{Network: ptr(classifyIotID)}, To: model.MatrixEndpoint{Network: ptr(classifyLabID)}, Policy: model.MatrixEntryPolicyAllow},
		{From: model.MatrixEndpoint{Network: ptr(classifyLabID)}, To: model.MatrixEndpoint{Network: ptr(classifyIotID)}, Policy: model.MatrixEntryPolicyAllow},
	}}
	tg := g.compile(func(in *compiler.Input) { in.TestClassifyIDs = []int{id} })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	g.apply(tg)
	iotIf := bridgeIf(t, tg, classifyIotID)
	labIf := bridgeIf(t, tg, classifyLabID)
	g.addClassifyElement(tg.ClassifyMaps["dev"], testbed.ClientAAddr, id)

	addClassifyProbe(t, g.top.GW, iotIf, []int{id})
	addClassifyProbe(t, g.top.GW, labIf, []int{id})

	if r := testbed.MustPing(t, g.top.A, testbed.ClientCAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("A cannot reach C: loss %.0f%%\n%s", r.Loss()*100, g.dump())
	}
	if n := classifyProbePackets(t, g.top.GW, labIf, id, 0); n == 0 {
		t.Error("A's upload (egressing the Lab bridge towards C) was not counted")
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, id, 1); n == 0 {
		t.Error("C's reply (egressing the IoT bridge back to A, download) was not counted")
	}
	if n := classifyProbePackets(t, g.top.GW, labIf, id, 1); n != 0 {
		t.Errorf("the Lab bridge's download class counted %d: nothing of A's leaves there in that direction", n)
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, id, 0); n != 0 {
		t.Errorf("the IoT bridge's upload class counted %d: A's upload leaves through the Lab bridge here", n)
	}
}

// M7 test: non-test traffic (the gateway's own management traffic) keeps its mark untouched. The
// management peer's address is deliberately given a classification-map entry too, so the proof is
// that the guard (only test, WireGuard and remote-network traffic is classified, plan §3.3) is what
// protects it, not merely the absence of a matching map element.
func TestNonTestTrafficKeepsItsMarkUntouched(t *testing.T) {
	g := newGateway(t)
	const id = 13
	tg := g.compile(func(in *compiler.Input) { in.TestClassifyIDs = []int{id} })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	g.apply(tg)
	g.addClassifyElement(tg.ClassifyMaps["dev"], testbed.MgmtPeer, id)
	addClassifyProbe(t, g.top.GW, "mgmt0", []int{id})

	if r := testbed.MustPing(t, g.top.Mgmt, testbed.MgmtGateway, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("management cannot reach the gateway: %s", g.dump())
	}
	if n := classifyProbePackets(t, g.top.GW, "mgmt0", id, 0); n != 0 {
		t.Errorf("management traffic's mark was written (upload class got %d packets) although it is not test traffic", n)
	}
	if n := classifyProbePackets(t, g.top.GW, "mgmt0", id, 1); n != 0 {
		t.Errorf("management traffic's mark was written (download class got %d packets) although it is not test traffic", n)
	}
	// meanwhile A, which IS test traffic, still gets classified with the very same id on its own
	// interface, proving the probe and the map element both work; only the guard tells them apart.
	iotIf := bridgeIf(t, tg, classifyIotID)
	g.addClassifyElement(tg.ClassifyMaps["dev"], testbed.ClientAAddr, id)
	addClassifyProbe(t, g.top.GW, iotIf, []int{id})
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Fatalf("%s", g.dump())
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, id, 1); n == 0 {
		t.Error("A's own (test) traffic was not classified: the probe setup itself is broken")
	}
}

// M7 test: a map change moves an established connection to its new class at once (plan §3.3, "per
// packet, not per connection"): the classification maps hold the resolved id for every packet, so
// an existing conntrack entry is not cached onto the old id (S10's own finding, which is why the
// identity map and the classification maps are both element updates rather than something cached
// per connection).
func TestAMapChangeMovesAnEstablishedConnectionToItsNewClass(t *testing.T) {
	g := newGateway(t)
	const idOld, idNew = 21, 22
	tg := g.compile(func(in *compiler.Input) { in.TestClassifyIDs = []int{idOld, idNew} })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	g.apply(tg)
	iotIf := bridgeIf(t, tg, classifyIotID)
	addClassifyProbe(t, g.top.GW, "wan0", []int{idOld, idNew})
	addClassifyProbe(t, g.top.GW, iotIf, []int{idOld, idNew})

	g.addClassifyElement(tg.ClassifyMaps["dev"], testbed.ClientAAddr, idOld)
	// one long-lived ping, so the same conntrack entry (the same ICMP id, the same "connection") -
	// not a fresh one each time - spans the map change below.
	ping := g.top.A.Start("ping", "-i", "0.2", "-w", "8", "-n", testbed.ServerAddr)
	t.Cleanup(ping.Stop)

	waitClassifyPackets(t, g.top.GW, "wan0", idOld, 0, 5*time.Second, "the connection was never classified under the old id")
	oldBefore := classifyProbePackets(t, g.top.GW, iotIf, idOld, 1)

	// the map changes while the same ping (the same connection) is still running: delete then add,
	// exactly as the identity map's own incremental updates do (engine's identityOps).
	g.delClassifyElement(tg.ClassifyMaps["dev"], testbed.ClientAAddr)
	g.addClassifyElement(tg.ClassifyMaps["dev"], testbed.ClientAAddr, idNew)

	waitClassifyPackets(t, g.top.GW, "wan0", idNew, 0, 5*time.Second, "the same connection's later packets were never classified under the new id")
	if n := classifyProbePackets(t, g.top.GW, iotIf, idNew, 1); n == 0 {
		t.Error("the connection's later reply packets were not classified under the new id")
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, idOld, 1); n != oldBefore {
		t.Errorf("the old class kept counting after the map pointed elsewhere: %d -> %d", oldBefore, n)
	}
}

// waitClassifyPackets polls until the (id, direction) class on dev has seen at least one packet.
func waitClassifyPackets(t *testing.T, ns *testbed.Namespace, dev string, id, dir int, d time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if classifyProbePackets(t, ns, dev, id, dir) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
