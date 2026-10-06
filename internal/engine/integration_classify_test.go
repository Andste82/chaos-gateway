//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// M7 classification tests over WireGuard (plan §3.3's acceptance list): the lookup chain runs on
// the conntrack original tuple, so it is unaffected by which interface (a local bridge, a WireGuard
// tunnel) a packet happens to leave through. See internal/apply/integration_classify_test.go for the
// NAT and two-test-network cases and the shared tc-probe mechanism (plan §3.3's own acceptance
// list: "a test tc class per id and direction"); the helpers below are the WireGuard-harness
// counterpart, since TestClassifyIDs is a compiler.Input field the engine's own apply path never
// sets (M7 resolves no real fault yet), so these tests compile and apply directly, with a second,
// independent executor, on top of the tunnel the engine already brought up.

const classifyProbeMask = 0x1fff0

func classifyHandle(id, dir int) uint32 {
	return uint32(id)<<compiler.MarkIDShift | uint32(dir)<<compiler.MarkDirectionBit
}

func classifyClassID(id, dir int) string { return fmt.Sprintf("1:%d", 100+id*2+dir) }

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

// classifyExec is a second, independent executor pointed at the same real namespace: the engine's
// own apply path never sets TestClassifyIDs (M7 has no fault resolution yet), so these tests
// compile and apply a classification-bearing target directly, after the engine has already brought
// the tunnel up through its normal path.
func classifyExec(t *testing.T) apply.Exec {
	t.Helper()
	ex, err := executor.New(executor.NewExecRunner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	return apply.Local{E: ex}
}

func addClassifyElement(t *testing.T, ns, mapName, key string, id int) {
	t.Helper()
	op := &executor.NftAddMapElements{Target: executor.Target{NS: ns}, Map: mapName,
		Elements: []executor.NftMapElement{{Key: key, Value: compiler.MarkChainName(id)}}}
	if _, err := classifyExec(t).Do(context.Background(), op); err != nil {
		t.Fatalf("add classify element %s -> %d: %v", key, id, err)
	}
}

// applyWithClassifyIDs recompiles the wgGW's current, already-applied configuration with the given
// TestClassifyIDs and applies it, on top of whatever the engine itself already brought up (the
// tunnels, the routing). It is a full, idempotent re-apply of the same desired state plus the
// classification mechanism, not an engine-tracked change: the engine's own snapshot and Barrier
// know nothing about it, so it is only ever done as the last step before a test's assertions.
func (g *wgGW) applyWithClassifyIDs(ids []int) *compiler.Target {
	g.t.Helper()
	ex := classifyExec(g.t)
	ctx := context.Background()
	host, err := apply.ReadHost(ctx, ex, g.top.GW.Name)
	if err != nil {
		g.t.Fatal(err)
	}
	_, cfg, err := g.st.Active()
	if err != nil {
		g.t.Fatal(err)
	}
	keys, err := wireguard.InterfaceKeys(cfg, g.sec)
	if err != nil {
		g.t.Fatal(err)
	}
	tg := compiler.Compile(compiler.Input{Config: cfg, Host: host, Generation: compiler.Generation{Revision: 1, Seq: 1 << 20},
		Keys: keys, TestClassifyIDs: ids})
	if tg.HasErrors() {
		g.t.Fatalf("%+v", tg.Problems)
	}
	// This executor is its own process, independent of the engine's: it has never assigned these
	// interfaces itself (assignment is in-memory, per executor, internal/executor/exec.go's
	// e.scope), so without this, apply.Apply's own BuildPlan would see every interface the engine
	// already created as one that exists but does not belong to Chaos Gateway from this
	// executor's point of view, and refuse to touch it. Assigning first, through the same
	// executor, makes the Apply below's own state read see them as already ours - exactly what
	// the engine's own first-ever apply already established in the kernel.
	if _, err := ex.Do(ctx, &executor.AssignInterfaces{Target: executor.Target{NS: g.top.GW.Name}, Devs: tg.Interfaces, OSOwned: tg.OSOwned}); err != nil {
		g.t.Fatalf("assign interfaces: %v", err)
	}
	actx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := apply.Apply(actx, ex, g.top.GW.Name, tg); err != nil {
		g.t.Fatalf("apply with classify ids: %v", err)
	}
	return tg
}

func wgIfName(t *testing.T, tg *compiler.Target, netID string) string {
	t.Helper()
	for _, w := range tg.WireGuard {
		if w.NetworkID == netID {
			return w.Name
		}
	}
	t.Fatalf("no WireGuard interface for network %s", netID)
	return ""
}

func bridgeIfName(t *testing.T, tg *compiler.Target, netID string) string {
	t.Helper()
	for _, b := range tg.Bridges {
		if b.NetworkID == netID {
			return b.Name
		}
	}
	t.Fatalf("no bridge for network %s", netID)
	return ""
}

// M7 test: classification holds for a host in a WireGuard client network, both as the initiator and
// as the destination of the connection (plan §3.3's acceptance list) - the lookup chain's maps are
// keyed on the conntrack original tuple, so the direction bit (not which interface carries the
// packet) is what tells upload from download apart on each side of the tunnel.
func TestClassificationForAWireGuardClientNetworkAsInitiatorAndAsDestination(t *testing.T) {
	g := newWGGW(t)
	g.apply(func(c *model.Configuration) {
		n := (*c.Networks)[tHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[tClient]
		iot := tIoT
		cl.Reachable = &[]model.MatrixEndpoint{{Network: &iot}}
		(*wg.Clients)[tClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[tHub] = n
	})
	conf := g.client(tHub, tClient)
	g.up(g.top.RC, "wgrA", conf.Conf)
	if !pingOK(g.top.RC, "", "10.99.0.1") {
		t.Fatalf("the tunnel is not up\n%s", g.wgShow())
	}

	const idInit, idDest = 41, 42
	tg := g.applyWithClassifyIDs([]int{idInit, idDest})
	wgIf := wgIfName(t, tg, tHub)
	iotIf := bridgeIfName(t, tg, tIoT)
	// both ids are probed on both interfaces from the start: each tc qdisc can only be added once.
	addClassifyProbe(t, g.top.GW, iotIf, []int{idInit, idDest})
	addClassifyProbe(t, g.top.GW, wgIf, []int{idInit, idDest})

	// the client network host as initiator: its own address classifies its connection to A.
	addClassifyElement(t, g.top.GW.Name, tg.ClassifyMaps["dev"], testbed.ClientNetHost, idInit)
	if !pingOK(g.top.RC, testbed.ClientNetHost, testbed.ClientAAddr) {
		t.Fatalf("the client network cannot reach A\n%s", g.wgShow())
	}
	if n := classifyProbePackets(t, g.top.GW, wgIf, idInit, 0); n == 0 {
		t.Error("the client's upload (arriving over the tunnel) was not classified")
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, idInit, 1); n == 0 {
		t.Error("A's reply (leaving through the IoT bridge, download) was not classified")
	}

	// the client network host as destination: A initiates towards it instead.
	g.delClassifyElement(tg.ClassifyMaps["dev"], testbed.ClientNetHost)
	addClassifyElement(t, g.top.GW.Name, tg.ClassifyMaps["dev"], testbed.ClientAAddr, idDest)
	if !pingOK(g.top.A, "", testbed.ClientNetHost) {
		t.Fatalf("A cannot reach the client network\n%s", g.wgShow())
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, idDest, 0); n == 0 {
		t.Error("A's upload (leaving through the IoT bridge) was not classified")
	}
	if n := classifyProbePackets(t, g.top.GW, wgIf, idDest, 1); n == 0 {
		t.Error("the client network's reply (leaving over the tunnel, download) was not classified")
	}
}

// delClassifyElement mirrors internal/apply's helper of the same shape, for the wgGW harness.
func (g *wgGW) delClassifyElement(mapName string, keys ...string) {
	g.t.Helper()
	op := &executor.NftDelMapElements{Target: executor.Target{NS: g.top.GW.Name}, Map: mapName, Keys: keys}
	if _, err := classifyExec(g.t).Do(context.Background(), op); err != nil {
		g.t.Fatalf("delete classify element %v: %v", keys, err)
	}
}

// M7 test: classification holds over a WireGuard link too (plan §3.3's acceptance list), the same
// mechanism as a hub's client tunnel.
func TestClassificationOverAWireGuardLink(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	remote, err := wireguard.LinkRemoteConfig(g.export(), tLink)
	if err != nil {
		t.Fatal(err)
	}
	g.up(g.top.Site, "wgsite", remote.Conf)
	g.top.Site.Must("ip", "route", "add", testbed.LAN0Subnet, "via", "10.255.0.0", "dev", "wgsite")
	if !g.waitHandshake("wg-site-b", 30*time.Second) {
		t.Fatalf("the link did not come up\n%s", g.wgShow())
	}

	const id = 43
	tg := g.applyWithClassifyIDs([]int{id})
	linkIf := wgIfName(t, tg, tLink)
	iotIf := bridgeIfName(t, tg, tIoT)
	addClassifyElement(t, g.top.GW.Name, tg.ClassifyMaps["dev"], testbed.ClientAAddr, id)
	addClassifyProbe(t, g.top.GW, iotIf, []int{id})
	addClassifyProbe(t, g.top.GW, linkIf, []int{id})

	if !pingOK(g.top.A, "", testbed.SiteNetHost) {
		t.Fatalf("A cannot reach the remote site over the link\n%s", g.wgShow())
	}
	if n := classifyProbePackets(t, g.top.GW, iotIf, id, 0); n == 0 {
		t.Error("A's upload (leaving through the IoT bridge) was not classified")
	}
	if n := classifyProbePackets(t, g.top.GW, linkIf, id, 1); n == 0 {
		t.Error("the site's reply (leaving over the link, download) was not classified")
	}
}
