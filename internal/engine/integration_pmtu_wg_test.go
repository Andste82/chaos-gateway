//go:build testbed

package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
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

// The three modes with a TCP transfer through a tunnel (plan risk 29: "PMTU tests through tunnels in M10"; spike S13 with a
// WireGuard interface of MTU 1420 on the way). A device of the lab sends 300 KB to a host in the network behind a WireGuard
// client and gets them back. Each mode does what it does on the uplink path, and the control device, which no fault names, goes
// through the same tunnel and is never held:
//   - icmp 1280: the transfer completes and its segments are no bigger than the size allows;
//   - blackhole 1280: the handshake passes, the first full-size segment (the tunnel carries 1420) does not, the transfer stalls
//     and the drops are counted;
//   - mss_clamp 1400: the transfer completes with the MSS of the clamp on both sides, the tunnel's own clamp (to the 1420 of the
//     interface) does not undo it.
//
// Nothing here is statistical, so the assertions are the same under emulation and on a native or KVM kernel.
func TestPMTUFaultsOfTheThreeModesHoldATCPTransferThroughATunnel(t *testing.T) {
	l := newTunnelLab(t)
	server := startPMTUServer(t, l.top.RC, testbed.ClientNetHost)
	flush := func() {
		for _, ns := range []*testbed.Namespace{l.top.RC, l.top.A, l.top.B} {
			ns.Must("ip", "route", "flush", "cache") // a host that was told a size keeps it
		}
	}
	bulk := func(from *testbed.Namespace, timeout time.Duration) bulkResult {
		t.Helper()
		return bulkTo(t, from, testbed.ClientNetHost, timeout)
	}
	// the mss of the connections the server has seen so far, A's or B's, in the order they were made
	lastMSS := func(n int) int {
		t.Helper()
		got := negotiatedMSS(server)
		if len(got) != n {
			t.Fatalf("the server saw %d connections (%v), want %d", len(got), got, n)
		}
		return got[n-1]
	}
	counters := func() map[string]engine.CounterValue {
		t.Helper()
		cs, err := l.e.ReadCounters(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	pmtuOf := func(ov engine.OverlayResult) compiler.PMTUFault {
		t.Helper()
		for _, f := range l.e.Snapshot().PMTU {
			if f.Source == ov.Overlay.Id.String() {
				return f
			}
		}
		t.Fatalf("no MTU fault of the overlay %s: %+v", ov.Overlay.Id, l.e.Snapshot().PMTU)
		return compiler.PMTUFault{}
	}
	end := func(ov engine.OverlayResult) {
		t.Helper()
		if _, err := l.e.DeleteOverlay(context.Background(), ov.Overlay.Id, nil, admin); err != nil {
			t.Fatal(err)
		}
		flush()
	}

	// before any fault both devices complete, with the segments the tunnel allows (1420 - 40, less the timestamps)
	if a := bulk(l.top.A, 40*time.Second); !a.OK {
		t.Fatalf("A does not complete the transfer before the fault: %+v", a)
	}
	full := lastMSS(1)
	if b := bulk(l.top.B, 40*time.Second); !b.OK {
		t.Fatalf("B does not complete the transfer before the fault: %+v", b)
	}
	if full < 1300 || full > 1380 {
		t.Fatalf("the segments through the tunnel are %d bytes, the tunnel's MTU of 1420 allows 1368 to 1380", full)
	}
	flush()
	conns := 2

	// ---- icmp: the kernel answers what does not fit, and the transfer completes
	icmp := putFault(t, l.e, "target: {device: dev-a}\nfault: {family: mtu, mtu: {size: 1280, mode: icmp}}")
	f := pmtuOf(icmp)
	if !strings.Contains(l.top.GW.Must("ip", "-d", "route", "show", "table", fmt.Sprint(f.Table)), "dev wg-lab-hub") {
		t.Errorf("the mirror table has no route into the tunnel:\n%s", routesIn(l.top.GW, fmt.Sprint(f.Table)))
	}
	if out, ok := pingDF(l.top.A, testbed.ClientNetHost, 1350); ok || !strings.Contains(out, "mtu = 1280") {
		t.Errorf("a packet of A that fits the tunnel but not the fault: answered %v\n%s", ok, out)
	}
	if out, ok := pingDF(l.top.B, testbed.ClientNetHost, 1350); !ok {
		t.Errorf("B, whom the fault does not name, is limited in the tunnel:\n%s", out)
	}
	flush()
	if a := bulk(l.top.A, 40*time.Second); !a.OK || a.Received != bulkBytes {
		t.Errorf("A's transfer through the tunnel with the ICMP mode does not complete: %+v", a)
	}
	conns++
	if got := lastMSS(conns); got > 1240 {
		t.Errorf("A's segments through the tunnel are %d bytes, the fault allows 1240 (1280 - 40)", got)
	}
	if b := bulk(l.top.B, 40*time.Second); !b.OK {
		t.Errorf("B's transfer: %+v", b)
	}
	conns++
	if got := lastMSS(conns); got != full {
		t.Errorf("B, whom the fault does not name, negotiated %d where it negotiated %d before", got, full)
	}
	if cs := counters(); cs[f.CounterUp].Packets == 0 || cs[f.CounterDown].Packets == 0 {
		t.Errorf("the packets of the fault are not counted: up %+v down %+v", cs[f.CounterUp], cs[f.CounterDown])
	}
	end(icmp)

	// ---- black hole: the transfer stalls, the control completes, the drops are counted
	hole := putFault(t, l.e, "target: {device: dev-a}\nfault: {family: mtu, mtu: {size: 1280, mode: blackhole}}")
	f = pmtuOf(hole)
	if out, ok := pingDF(l.top.A, testbed.ClientNetHost, 1350); ok || strings.Contains(out, "mtu") {
		t.Errorf("a large packet of A into the tunnel: answered %v (a black hole sends no ICMP)\n%s", ok, out)
	}
	if out, ok := pingDF(l.top.A, testbed.ClientNetHost, 1200); !ok {
		t.Errorf("a small packet of A does not go through the tunnel:\n%s", out)
	}
	control := bulk(l.top.B, 40*time.Second)
	if !control.OK {
		t.Errorf("B's transfer through the tunnel next to a black hole: %+v", control)
	}
	conns++
	dropsBefore := counters()[f.CounterDrop].Packets
	stalled := bulk(l.top.A, 8*time.Second)
	conns++
	cs := counters()
	assertStalled(t, "A's transfer through a black hole in the tunnel", stalled, control, 8*time.Second, dropsBefore, cs[f.CounterDrop].Packets)
	if cs[f.CounterDrop].Packets == 0 {
		t.Errorf("the drops of the black hole are not counted: %+v", cs[f.CounterDrop])
	}
	end(hole)
	if a := bulk(l.top.A, 40*time.Second); !a.OK {
		t.Errorf("A's transfer after the black hole ended: %+v", a)
	}
	conns++

	// ---- MSS clamp: the segments of the selected device are smaller, on both sides, and nothing else is touched
	clamp := putFault(t, l.e, "target: {device: dev-a}\nfault: {family: mtu, mtu: {size: 1400, mode: mss_clamp}}")
	f = pmtuOf(clamp)
	a := bulk(l.top.A, 40*time.Second)
	conns++
	if !a.OK {
		t.Fatalf("A's transfer through the tunnel with the clamp: %+v", a)
	}
	if a.MSS > f.MSS() || a.MSS < f.MSS()-60 {
		t.Errorf("A's own side negotiated %d, want at most the clamp %d", a.MSS, f.MSS())
	}
	if got := lastMSS(conns); got > f.MSS() || got < f.MSS()-60 {
		t.Errorf("the server negotiated %d with A, want at most the clamp %d (the tunnel's own clamp is to %d)", got, f.MSS(), 1420-40)
	}
	b := bulk(l.top.B, 40*time.Second)
	conns++
	if !b.OK {
		t.Errorf("B's transfer: %+v", b)
	}
	if got := lastMSS(conns); got != full {
		t.Errorf("B, whom the clamp does not name, negotiated %d where it negotiated %d before", got, full)
	}
	if out, ok := pingDF(l.top.A, testbed.ClientNetHost, 1392); !ok {
		t.Errorf("a large ICMP packet of A is touched by the TCP clamp:\n%s", out)
	}
	end(clamp)
}
