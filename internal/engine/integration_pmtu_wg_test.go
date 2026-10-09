//go:build testbed

package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The MTU family with WireGuard (M10, plan §2.5): a path-MTU fault applies to the routes towards a client network and to
// the routes BIRD learned over a link, because the mirror tables hold the same routes as the policy table.

func (g *wgGW) putOverlay(body string) engine.OverlayResult {
	g.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := g.e.PutOverlay(ctx, engine.OverlayWrite{Owner: admin, Request: overlayRequest(g.t, body)})
	if err != nil {
		g.t.Fatalf("%s: %v", body, err)
	}
	return res
}

func (g *wgGW) deleteOverlay(res engine.OverlayResult) {
	g.t.Helper()
	if _, err := g.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, admin); err != nil {
		g.t.Fatal(err)
	}
}

// pingDF sends two pings with the DF bit and a payload of size bytes.
func pingDF(from *testbed.Namespace, dst string, size int) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := from.Run(ctx, "ping", "-M", "do", "-s", fmt.Sprint(size), "-c", "2", "-W", "2", "-n", dst)
	return out, err == nil
}

// A device that talks to a network behind a WireGuard client meets the path MTU of the fault on its way into the tunnel: a
// packet that fits the tunnel (1420) but not the fault (1280) gets the kernel's answer with the fault's size, and what
// fits goes through. The fault is in the mirror of the table of the tunnel route, with no special case for tunnels.
func TestAPMTUFaultAppliesToTrafficThroughATunnel(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	conf := g.client(tHub, tClient)
	g.up(g.top.RC, "wgrA", conf.Conf)
	if !pingOK(g.top.RC, "", "10.99.0.1") {
		t.Fatalf("the tunnel does not work\n%s", g.wgShow())
	}
	// before the fault 1350 bytes of payload (1378 on the wire) fit the tunnel of 1420
	if out, ok := pingDF(g.top.A, testbed.ClientNetHost, 1350); !ok {
		t.Fatalf("A cannot send 1378 bytes into the tunnel before the fault:\n%s", out)
	}
	res := g.putOverlay("target: {network: IoT}\nfault: {family: mtu, mtu: {size: 1280, mode: icmp}}")
	// the tunnel route is in the mirror table, with the size locked in
	mirror := g.top.GW.Must("ip", "-d", "route", "show", "table", "103")
	if !strings.Contains(mirror, "dev wg-lab-hub") || !strings.Contains(mirror, "mtu lock 1280") {
		t.Errorf("the mirror table has no route into the tunnel:\n%s", mirror)
	}
	out, ok := pingDF(g.top.A, testbed.ClientNetHost, 1350)
	if ok || !strings.Contains(out, "mtu = 1280") {
		t.Errorf("a packet that fits the tunnel but not the fault: answered %v\n%s", ok, out)
	}
	if out, ok := pingDF(g.top.A, testbed.ClientNetHost, 1200); !ok {
		t.Errorf("a packet that fits does not go through the tunnel:\n%s", out)
	}
	// the fault ends: the packet goes through again
	g.deleteOverlay(res)
	g.top.A.Must("ip", "route", "flush", "cache") // the device learned the size from the answers
	if out, ok := pingDF(g.top.A, testbed.ClientNetHost, 1350); !ok {
		t.Errorf("A is still limited after the fault ended:\n%s", out)
	}
}

// BIRD exports the routes it learned into the mirror tables as well, one kernel protocol per table, with the size locked in
// (plan §2.2.2: learned routes go only into Chaos Gateway's tables, §2.5). A traffic of a device towards a learned network is
// therefore limited like one towards a configured route, a size that comes keeps the session, and the learned routes of a
// table go with its size.
func TestLearnedRoutesAreExportedIntoThePMTUMirrorTablesToo(t *testing.T) {
	g := newBGP(t, 10, "10.60.0.0/24")
	if err := g.e.PollRouting(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if !g.established(90 * time.Second) {
		t.Fatalf("the BGP session did not come up\n%s", birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	if !g.waitRoute("10.60.0.0/24 via 10.255.0.1 dev wg-site-b", true, 30*time.Second) {
		t.Fatalf("the learned route is not in table 100\n%s", g.table100())
	}
	table := func(n string) string { return routesIn(g.top.GW, n) }
	waitIn := func(n, want string, present bool) bool {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(table(n), want) == present {
				return true
			}
			time.Sleep(time.Second)
		}
		return false
	}

	// a size: the learned route is in the mirror table with the size locked in, next to the routes of the executor
	one := g.putOverlay("target: {global: true}\nfault: {family: mtu, mtu: {size: 1280, mode: icmp}}")
	if !waitIn("103", "10.60.0.0/24 via 10.255.0.1 dev wg-site-b proto bird", true) {
		t.Fatalf("BIRD did not export the learned route into the mirror table\n%s\n%s", table("103"), birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	for _, line := range strings.Split(strings.TrimSpace(table("103")), "\n") {
		if !strings.Contains(line, "mtu lock 1280") {
			t.Errorf("a route of the mirror table without the locked size: %s", line)
		}
	}
	if !strings.Contains(table("103"), "dev br-iot") {
		t.Errorf("the executor's routes are not in the mirror table\n%s", table("103"))
	}
	// the learned network is limited like a configured one: a packet bigger than the size gets the answer of the kernel
	out, ok := pingDF(g.top.A, testbed.SiteNetHost, 1350)
	if ok || !strings.Contains(out, "mtu = 1280") {
		t.Errorf("traffic to a learned network is not limited: answered %v\n%s", ok, out)
	}

	// a second size: a mirror table of its own is fed, the session stays
	two := g.putOverlay("target: {global: true}\nfault: {family: mtu, mtu: {size: 1400, mode: icmp}, protocol: udp, ports: [9]}")
	if !waitIn("104", "10.60.0.0/24 via 10.255.0.1 dev wg-site-b proto bird", true) {
		t.Fatalf("no learned route in the second mirror table\n%s", table("104"))
	}
	if !strings.Contains(table("104"), "mtu lock 1400") || !g.establishedNow() {
		t.Errorf("table 104:\n%s\nsession established: %v", table("104"), g.establishedNow())
	}
	if !strings.Contains(table("103"), "10.60.0.0/24") {
		t.Errorf("the first table lost the learned route when the second came\n%s", table("103"))
	}

	// the first size goes: its table is empty again, the other keeps its routes
	g.deleteOverlay(one)
	if !waitIn("103", "10.60.0.0/24", false) {
		t.Errorf("the learned route stays in the table of a size that went\n%s", table("103"))
	}
	if got := strings.TrimSpace(table("103")); got != "" {
		t.Errorf("table 103 is not empty:\n%s", got)
	}
	if !strings.Contains(table("104"), "10.60.0.0/24") || !g.establishedNow() {
		t.Errorf("table 104:\n%s", table("104"))
	}
	g.deleteOverlay(two)
	if !waitIn("104", "10.60.0.0/24", false) {
		t.Errorf("table 104 keeps the route\n%s", table("104"))
	}
	// the main table never gets a learned route (plan §2.2.2)
	if strings.Contains(g.top.GW.Must("ip", "route", "show"), "10.60.0.0/24") {
		t.Errorf("a learned route is in the main table")
	}
}
