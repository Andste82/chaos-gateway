//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// M7 classification tests over WireGuard (plan §3.3's acceptance list): the lookup chain runs on
// the conntrack original tuple, so it is unaffected by which interface (a local bridge, a WireGuard
// tunnel) a packet happens to leave through. See internal/apply/integration_classify_test.go for the
// NAT and two-test-network cases. Since M8a the ids are real: overlays are resolved into faults,
// the compiler gives each its id, and the tests read the counters of the classes of the compiled tc
// tree, which they install with a second, independent executor on top of the tunnel the engine
// already brought up (applying the tc tree is not part of the engine's apply path before M8b).

// classifyExec is a second, independent executor pointed at the same real namespace.
func classifyExec(t *testing.T) apply.Exec {
	t.Helper()
	ex, err := executor.New(executor.NewExecRunner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	return apply.Local{E: ex}
}

// overlay creates an overlay from a YAML request body against the engine's active configuration.
func (g *wgGW) overlay(body string) model.Overlay {
	g.t.Helper()
	_, cfg, err := g.st.Active()
	if err != nil {
		g.t.Fatal(err)
	}
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		g.t.Fatalf("%s: %v", body, err)
	}
	norm, errs := domain.ValidateOverlay(cfg, req)
	if len(errs) != 0 {
		g.t.Fatalf("%s: %v", body, errs)
	}
	o, err := domain.NewOverlay(*norm, model.Owner{Type: "user", Id: "test"}, uuid.New(), time.Now())
	if err != nil {
		g.t.Fatal(err)
	}
	return o
}

// applyWithFaults recompiles the wgGW's current, already-applied configuration with the overlays
// and applies it, on top of whatever the engine itself already brought up (the tunnels, the
// routing), then installs the compiled tc tree. It is a full, idempotent re-apply of the same
// desired state plus the faults, not an engine-tracked change: the engine's own snapshot and
// Barrier know nothing about it, so it is only ever done as the last step before a test's
// assertions.
func (g *wgGW) applyWithFaults(overlays ...model.Overlay) *compiler.Target {
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
		Keys: keys, Overlays: overlays})
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
		g.t.Fatalf("apply with faults: %v", err)
	}
	if tg.TC != nil {
		for _, dev := range tg.TC.Devs {
			if _, err := ex.Do(ctx, &executor.TC{Target: executor.Target{NS: g.top.GW.Name}, Entries: tg.TC.Entries(dev, true)}); err != nil {
				g.t.Fatalf("install the tc tree on %s: %v\n%s", dev, err, strings.Join(tg.TC.Lines(dev), "\n"))
			}
		}
	}
	return tg
}

// faultClass returns the tc class of an overlay's fault (the shared id) and direction.
func faultClass(t *testing.T, tg *compiler.Target, o model.Overlay, dir compiler.Direction) compiler.TCClass {
	t.Helper()
	for _, f := range tg.Faults {
		if f.Source != o.Id.String() || f.Device != "" {
			continue
		}
		for _, c := range tg.TC.Classes {
			if c.ID == f.ID && c.Dir == dir {
				return c
			}
		}
	}
	t.Fatalf("no tc class for overlay %s %s in %+v", o.Id, dir, tg.Faults)
	return compiler.TCClass{}
}

// classPackets reads the packet count of a class on dev.
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

	// the client network host as initiator (a fault on its address), A as initiator (a fault on its
	// network): two ids, so both directions of both connections can be told apart
	oInit := g.overlay(`{target: {remote_network: {cidr: ` + testbed.ClientNetHost + `/32}}, fault: {latency: 1ms}}`)
	oDest := g.overlay(`{target: {network: ` + tIoT + `}, fault: {latency: 2ms}}`)
	tg := g.applyWithFaults(oInit, oDest)
	wgIf := wgIfName(t, tg, tHub)
	iotIf := bridgeIfName(t, tg, tIoT)

	if !pingOK(g.top.RC, testbed.ClientNetHost, testbed.ClientAAddr) {
		t.Fatalf("the client network cannot reach A\n%s", g.wgShow())
	}
	// egress is measured on the destination side's own interface, like
	// TestClassificationAcrossTwoTestNetworks: the client's upload (dir 0) heads towards A, so it
	// egresses the IoT bridge; A's reply (dir 1) heads back towards the client, so it egresses the
	// WireGuard interface.
	if n := classPackets(t, g.top.GW, iotIf, faultClass(t, tg, oInit, compiler.Upload)); n == 0 {
		t.Error("the client's upload (egressing the IoT bridge towards A) was not classified")
	}
	if n := classPackets(t, g.top.GW, wgIf, faultClass(t, tg, oInit, compiler.Download)); n == 0 {
		t.Error("A's reply (egressing over the tunnel back to the client, download) was not classified")
	}

	// the client network host as destination: A initiates towards it instead.
	if !pingOK(g.top.A, "", testbed.ClientNetHost) {
		t.Fatalf("A cannot reach the client network\n%s", g.wgShow())
	}
	// this time A initiates: A's upload (dir 0) heads towards the client, egressing the WireGuard
	// interface; the client's reply (dir 1) heads back towards A, egressing the IoT bridge.
	if n := classPackets(t, g.top.GW, wgIf, faultClass(t, tg, oDest, compiler.Upload)); n == 0 {
		t.Error("A's upload (egressing over the tunnel towards the client) was not classified")
	}
	if n := classPackets(t, g.top.GW, iotIf, faultClass(t, tg, oDest, compiler.Download)); n == 0 {
		t.Error("the client network's reply (egressing the IoT bridge back to A, download) was not classified")
	}
	// initiator semantics (plan §2.4, E12): the connection the client opened is not impaired by the
	// IoT network's fault, although A is its destination
	if n := classPackets(t, g.top.GW, iotIf, faultClass(t, tg, oDest, compiler.Upload)); n != 0 {
		t.Errorf("the IoT network's upload class counted %d packets of a connection the client network initiated", n)
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

	o := g.overlay(`{target: {network: ` + tIoT + `}, fault: {latency: 1ms}}`)
	tg := g.applyWithFaults(o)
	linkIf := wgIfName(t, tg, tLink)
	iotIf := bridgeIfName(t, tg, tIoT)

	if !pingOK(g.top.A, "", testbed.SiteNetHost) {
		t.Fatalf("A cannot reach the remote site over the link\n%s", g.wgShow())
	}
	// A initiates: A's upload (dir 0) heads towards the site, egressing the link interface; the
	// site's reply (dir 1) heads back towards A, egressing the IoT bridge.
	if n := classPackets(t, g.top.GW, linkIf, faultClass(t, tg, o, compiler.Upload)); n == 0 {
		t.Error("A's upload (egressing over the link towards the site) was not classified")
	}
	if n := classPackets(t, g.top.GW, iotIf, faultClass(t, tg, o, compiler.Download)); n == 0 {
		t.Error("the site's reply (egressing the IoT bridge back to A, download) was not classified")
	}
}
