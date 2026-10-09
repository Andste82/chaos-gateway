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
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// The measurement tests of the tunnel faults and the WireGuard actions (M10, plan §2.2.1, §4.3, spike S15) on the real kernel.
// A tunnel fault impairs the encrypted UDP of one peer, whatever runs inside the tunnel: the flows that cross the tunnel are
// measured with the probes of the other fault tests (the directions of the flow are those of its initiator, the directions of the
// fault those of the remote side: upload is what the client sends, download what it receives), and the flows that do not cross
// it show nothing. What is asserted where is the rule of integration_faults_test.go: the functional assertions always, the
// accuracy ones (§4.3) with native execution or KVM.

// tunnelLab is the testbed with a WireGuard client behind which a host lives (10.50.0.10), the devices of the fault tests, an
// echo in every place a flow starts or ends, and the engine on the real clock polling the peers.
type tunnelLab struct {
	*wgGW
	toClient, bToClient, clientToA, aToServer, bToServer, underlay flow
}

func newTunnelLab(t *testing.T) *tunnelLab {
	t.Helper()
	g := newWGGW(t)
	g.apply(func(c *model.Configuration) {
		withDevices(c)
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
	resolveDevices(t, g.e)
	if err := g.e.PollWireGuard(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	echoClient := testbed.StartEcho(t, g.top.RC, testbed.ClientNetHost, probePort)
	echoA := testbed.StartEcho(t, g.top.A, testbed.ClientAAddr, probePort)
	echoServer := testbed.StartEcho(t, g.top.Server, testbed.ServerAddr, probePort)
	echoGW := testbed.StartEcho(t, g.top.GW, testbed.UplinkGateway, probePort)
	l := &tunnelLab{wgGW: g,
		toClient:  flow{name: "A to the client network", from: g.top.A, echo: echoClient},
		bToClient: flow{name: "B to the client network", from: g.top.B, echo: echoClient},
		clientToA: flow{name: "the client network to A", from: g.top.RC, src: testbed.ClientNetHost, echo: echoA},
		aToServer: flow{name: "A to the server", from: g.top.A, echo: echoServer},
		bToServer: flow{name: "B to the server", from: g.top.B, echo: echoServer},
		// the encrypted UDP of the tunnel and this flow cross the same wire and the same interface, from the same host; the
		// flow is not the tunnel's (another port), so a tunnel fault does not touch it
		underlay: flow{name: "the client machine to the gateway's uplink address", from: g.top.RC, echo: echoGW}}
	waitReach(t, l.toClient)
	waitReach(t, l.clientToA)
	return l
}

// tunnelQueues sums the netem queues of a tunnel fault in one direction: the IFB's for the upload, the interfaces' for the download.
func (l *tunnelLab) tunnelQueues(f compiler.Fault, dir compiler.Direction) (sent, drops int64) {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	qs, err := l.e.ReadQueues(ctx)
	if err != nil {
		l.t.Fatal(err)
	}
	found := false
	for _, dev := range l.e.Snapshot().TCDevs {
		if st, ok := qs[engine.QueueKey(dev, compiler.ClassIDOf(f.ID, dir))]; ok {
			sent += int64(st.Packets)
			drops += int64(st.Drops)
			found = true
		}
	}
	if !found {
		l.t.Fatalf("no queue of fault %d %s in %v", f.ID, dir, qs)
	}
	return sent, drops
}

// isolated is expectUnaffected for the tests of this file. With native execution or KVM it is expectUnaffected (which has the
// flakiness policy of §4.3 for its accuracy assertions). Under emulation a flow that the fault does not name is sometimes slowed by the
// load of the machine itself (a flow measured at 13 ms before a fault was measured at 41 ms and at 397 ms in the same run, with the
// fault on another flow), so the functional assertion gets the same rule: a failed attempt is measured once more, only a second
// failure fails.
func isolated(t *testing.T, f flow, before testbed.ProbeResult) {
	t.Helper()
	if testbed.Accurate() {
		expectUnaffected(t, f, before)
		return
	}
	for attempt := 1; ; attempt++ {
		res := f.run(t, quietRun())
		t.Logf("%s, unaffected (attempt %d): %s", f.name, attempt, res)
		const limit = 25 * time.Millisecond
		clean := res.UpLoss() == 0 && res.DownLoss() == 0 && res.Delivered > 0 &&
			res.UpMedian() <= before.UpMedian()+limit && res.DownMedian() <= before.DownMedian()+limit
		if clean {
			return
		}
		if attempt == 2 {
			t.Errorf("%s: a flow the fault does not name is affected: %s (before: %s)", f.name, res, before)
			return
		}
	}
}

func tunnelFaultOf(t *testing.T, s *engine.Snapshot, o model.Overlay) compiler.Fault {
	t.Helper()
	f := faultOfOverlay(t, s, o)
	if f.Tunnel == nil {
		t.Fatalf("fault %d is not a tunnel fault", f.ID)
	}
	return f
}

// A tunnel fault impairs everything inside its tunnel, both directions told apart, and nothing else: the flows of two devices
// into the client's network and the flow the client's network starts are impaired as configured (latency, loss; the spread of a
// jitter is the fault tests'), a flow to the server through the same uplink interface, and a flow from the client's machine to
// the gateway's uplink address, are not. The overlay counts the encrypted packets of both directions, the queues of both
// sides hold them, and the handshake goes on.
func TestATunnelFaultImpairsEverythingInTheTunnelAndNothingElse(t *testing.T) {
	l := newTunnelLab(t)
	beforeServer, beforeUnderlay := baseline(t, l.aToServer), baseline(t, l.underlay)
	// (the time of a write is logged next to that of a write that has no tunnel: the answer comes when the kernel holds the change)
	t0 := time.Now()
	ov := putFault(t, l.e, "fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 30ms}, download: {latency: 150ms, loss: 5%}}")
	tTunnel := time.Since(t0)
	f := tunnelFaultOf(t, l.e.Snapshot(), ov.Overlay)
	t0 = time.Now()
	plain := putFault(t, l.e, "target: {device: dev-b}\nfault: {latency: 5ms}")
	t.Logf("the write of the tunnel fault took %v, the write of a device fault %v", tTunnel.Round(time.Millisecond), time.Since(t0).Round(time.Millisecond))
	if _, err := l.e.DeleteOverlay(context.Background(), plain.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}

	// towards the client machine is the download of the tunnel: the flow that starts at A has it as its upload
	towards := shape{up: 150 * time.Millisecond, upLoss: 0.05, down: 30 * time.Millisecond}
	expectImpaired(t, l.toClient, towards)
	expectImpaired(t, l.bToClient, towards)
	expectImpaired(t, l.clientToA, shape{up: 30 * time.Millisecond, down: 150 * time.Millisecond, downLoss: 0.05})
	isolated(t, l.aToServer, beforeServer)
	isolated(t, l.underlay, beforeUnderlay)

	// the packets the tunnel carried are counted, both ways, and the queues of both sides saw them
	cs, err := l.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cs[f.CounterDown].Packets == 0 || cs[f.CounterUp].Packets == 0 {
		t.Errorf("counters: towards the peer %+v, from the peer %+v", cs[f.CounterDown], cs[f.CounterUp])
	}
	if sent, drops := l.tunnelQueues(f, compiler.Download); sent == 0 || drops == 0 {
		t.Errorf("towards the peer: %d sent, %d dropped (5 %% loss)", sent, drops)
	}
	if sent, _ := l.tunnelQueues(f, compiler.Upload); sent == 0 {
		t.Error("nothing went through the class of the IFB")
	}
	// the fault is in the kernel the way the compiler says it is
	if !strings.Contains(l.top.GW.Must("ip", "-d", "link", "show", "dev", "ifb-cgw"), "ifb") {
		t.Error("no IFB")
	}
	if out := l.top.GW.Must("tc", "filter", "show", "dev", "wan0", "ingress"); !strings.Contains(out, "flower") || !strings.Contains(out, testbed.RemoteClientAddr) {
		t.Errorf("the ingress filter:\n%s", out)
	}
	if out := l.top.GW.Must("nft", "list", "chain", "inet", "chaosgw", compiler.TunnelOutChain); !strings.Contains(out, "hook output") {
		t.Errorf("the output hook:\n%s", out)
	}
	if st := l.e.Snapshot().WireGuard[tClient]; !st.Online {
		t.Errorf("the client went offline: %+v", st)
	}
}

// A tunnel fault of a link impairs the traffic over that link and no other tunnel: the gateway initiates the link (its endpoint is
// configured), so the packets of the remote site come from the endpoint the link names; the traffic of the same device over the client's
// tunnel, which crosses the same interface, is not touched. The directions are the remote site's.
func TestATunnelFaultOfALinkImpairsTheTrafficOverItAndNotTheOtherTunnel(t *testing.T) {
	l := newTunnelLab(t)
	remote, err := wireguardLinkRemote(l.wgGW)
	if err != nil {
		t.Fatal(err)
	}
	l.up(l.top.Site, "wgsite", remote)
	// the remote side routes the gateway's networks through the link
	l.top.Site.Must("ip", "route", "add", "10.10.0.0/24", "via", "10.255.0.0", "dev", "wgsite")
	if !l.waitHandshake("wg-site-b", 60*time.Second) {
		t.Fatalf("the link did not come up\n%s\n%s", l.wgShow(), l.top.Site.Must("wg", "show"))
	}
	echoSite := testbed.StartEcho(t, l.top.Site, testbed.SiteNetHost, probePort)
	toSite := flow{name: "A to the remote site's network", from: l.top.A, echo: echoSite}
	waitReach(t, toSite)
	beforeClient := baseline(t, l.toClient)

	putFault(t, l.e, "fault: {family: tunnel, tunnel: {link: site-b}, upload: {latency: 30ms}, download: {latency: 150ms, loss: 5%}}")
	expectImpaired(t, toSite, shape{up: 150 * time.Millisecond, upLoss: 0.05, down: 30 * time.Millisecond})
	isolated(t, l.toClient, beforeClient)
	isolated(t, l.underlay, baseline(t, l.underlay))
}

// E10 on the real kernel (plan §2.4): the fault of a device and the fault of the tunnel its traffic crosses add up. A has 40 ms
// in each direction, the tunnel of the client 50 ms: A's flow into the client's network has 90 ms in each direction, B's flow
// through the same tunnel 50, A's flow to the server 40, and the flow the client's network starts to A has the tunnel's 50 alone
// (a device fault belongs to the connections the device initiates, E12).
func TestE10TheDeviceFaultAndTheTunnelFaultOfTheClientAddUp(t *testing.T) {
	l := newTunnelLab(t)
	putFault(t, l.e, "target: {device: dev-a}\nfault: {upload: {latency: 40ms}, download: {latency: 40ms}}")
	putFault(t, l.e, "fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 50ms}, download: {latency: 50ms}}")
	expectImpaired(t, l.toClient, shape{up: 90 * time.Millisecond, down: 90 * time.Millisecond})
	expectImpaired(t, l.bToClient, shape{up: 50 * time.Millisecond, down: 50 * time.Millisecond})
	expectImpaired(t, l.aToServer, shape{up: 40 * time.Millisecond, down: 40 * time.Millisecond})
	expectImpaired(t, l.clientToA, shape{up: 50 * time.Millisecond, down: 50 * time.Millisecond})
}

// A blackout of the tunnel cuts everything inside it, in the direction it is written for: the upload alone lets the requests of
// A's flow reach the client's machine and loses the answers, the download alone loses the requests, both lose everything, in
// the flows of both devices and the flow the client's network starts, the tunnel's own pings included; the flows outside do not
// notice, the queues and the counters count the drops, and the tunnel is back when the overlay is gone (WireGuard keeps its
// session through a blackout, and a new handshake follows the first packets that need one).
func TestATunnelBlackoutCutsTheTunnelInTheDirectionItIsWrittenForAndNothingElse(t *testing.T) {
	l := newTunnelLab(t)
	beforeServer, beforeUnderlay := baseline(t, l.aToServer), baseline(t, l.underlay)
	probe := testbed.ProbeOptions{Count: 60, Interval: 25 * time.Millisecond, Settle: settle()}

	up := putFault(t, l.e, "fault: {family: tunnel, tunnel: {client: rA}, upload: {blackout: true}}")
	res := l.toClient.run(t, probe)
	t.Logf("upload blackout, A to the client network: %s", res)
	if res.Delivered == 0 || res.Replied != 0 {
		t.Errorf("a blackout of the upload: the requests reach the client machine and no answer comes back, got %s", res)
	}
	f := tunnelFaultOf(t, l.e.Snapshot(), up.Overlay)
	if _, drops := l.tunnelQueues(f, compiler.Upload); drops == 0 {
		t.Error("the queue of the IFB counted no drop")
	}
	isolated(t, l.aToServer, beforeServer)

	putFault(t, l.e, "fault: {family: tunnel, tunnel: {client: rA}, download: {blackout: true}}")
	res = l.toClient.run(t, probe)
	t.Logf("download blackout, A to the client network: %s", res)
	if res.Delivered != 0 || res.Replied != 0 {
		t.Errorf("a blackout of the download loses the requests: %s", res)
	}
	if _, drops := l.tunnelQueues(f, compiler.Download); drops == 0 {
		t.Error("the queue of the interfaces counted no drop")
	}

	both := putFault(t, l.e, "fault: {family: tunnel, tunnel: {client: rA}, blackout: true}")
	for _, fl := range []flow{l.toClient, l.bToClient, l.clientToA} {
		if r := fl.run(t, probe); r.Delivered != 0 || r.Replied != 0 {
			t.Errorf("%s: %s got through a blackout of the tunnel", fl.name, r)
		}
	}
	if pingOK(l.top.RC, "", "10.99.0.1") {
		t.Error("the tunnel's own ping works through a blackout of the tunnel")
	}
	isolated(t, l.aToServer, beforeServer)
	isolated(t, l.bToServer, baseline(t, l.bToServer))
	isolated(t, l.underlay, beforeUnderlay)
	cs, err := l.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bf := tunnelFaultOf(t, l.e.Snapshot(), both.Overlay)
	if cs[bf.CounterDown].Packets == 0 || cs[bf.CounterUp].Packets == 0 {
		t.Errorf("the blackout does not count the packets it swallowed: %+v %+v", cs[bf.CounterDown], cs[bf.CounterUp])
	}

	// the end: the tunnel comes back without anyone touching the client
	if _, err := l.e.DeleteOverlay(context.Background(), both.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for !pingOK(l.top.A, "", testbed.ClientNetHost) {
		if time.Now().After(deadline) {
			t.Fatalf("the tunnel did not come back\n%s\n%s", l.wgShow(), l.top.RC.Must("wg", "show"))
		}
	}
	// and carries the flows again as if nothing had happened
	for _, fl := range []flow{l.toClient, l.bToClient, l.clientToA} {
		if r := fl.run(t, quietRun()); r.Delivered == 0 || r.Replied == 0 || r.UpLoss() != 0 || r.DownLoss() != 0 {
			t.Errorf("%s after the blackout: %s", fl.name, r)
		}
	}
}

// A flapping tunnel is up, then dead for the down time, and so on, on both sides of the tunnel in step: the packets of the flow
// into the tunnel see outages of the down time, a flow outside sees none, and every toggle of the engine (the interfaces' tree and the
// IFB together) is within the tolerance of its time (plan §2.10).
func TestAFlappingTunnelBlacksOutOnSchedule(t *testing.T) {
	l := newTunnelLab(t)
	up, down, iv, count := 3*time.Second, 2*time.Second, 10*time.Millisecond, 1900
	if !testbed.Accurate() {
		up, down, iv, count = 6*time.Second, 4*time.Second, 50*time.Millisecond, 700
	}
	l.putTunnelFlapping(up, down)
	opts := testbed.ProbeOptions{Count: count, Interval: iv, Settle: settle()}
	runB := l.bToServer.echo.Begin(l.bToServer.from, opts)
	measure := func() testbed.ProbeResult { return l.toClient.run(t, opts) }
	res := measure()
	resB, err := runB.Stop()
	if err != nil {
		t.Fatal(err)
	}
	quiet(t, "B to the server during the flapping of the tunnel", resB)

	outages := testbed.Outages(res.UpLost(), iv, int(down/iv)/2)
	for i, o := range outages {
		t.Logf("outage %d: from probe %d (%v after the first), %v long", i+1, o.First, o.Start, o.Len)
	}
	if res.Delivered == 0 {
		t.Fatalf("nothing got through in the up phases: %s", res)
	}
	var toggles []engine.FlapChange
	for _, c := range l.e.FlapLog() {
		if strings.HasSuffix(c.Key, "|upload") {
			toggles = append(toggles, c)
		}
	}
	if len(toggles) < 4 {
		t.Fatalf("%d toggles in a run of %v: %+v", len(toggles), time.Duration(count)*iv, toggles)
	}
	for i, c := range toggles {
		if c.Down != (i%2 == 0) {
			t.Errorf("toggle %d goes %v, the first one is the start of a blackout", i, c.Down)
		}
		if i > 0 {
			want := down
			if c.Down {
				want = up
			}
			if got := c.Scheduled - toggles[i-1].Scheduled; got != want {
				t.Errorf("toggle %d is scheduled %v after the previous one, want %v", i, got, want)
			}
		}
		if c.Late() < 0 {
			t.Errorf("toggle %d committed %v before its time", i, -c.Late())
		}
		if testbed.Accurate() && c.Late() > engine.FlapTolerance {
			t.Errorf("toggle %d committed %v after its time, the tolerance is %v", i, c.Late(), engine.FlapTolerance)
		}
		if c.Devices < 2 {
			t.Errorf("toggle %d changed the leaves on %d interfaces: the IFB or the tree was left out", i, c.Devices)
		}
	}
	if testbed.Accurate() {
		checkStat(t, "the flapping tunnel", res, measure, func(x testbed.ProbeResult) error {
			return testbed.CheckFlaps("the flapping tunnel", testbed.Outages(x.UpLost(), iv, int(down/iv)/2), x.Sent, up, down, iv, engine.FlapTolerance, 3)
		})
		return
	}
	var whole int
	for _, o := range outages {
		if o.First > 0 && o.Last < res.Sent-1 {
			whole++
			if o.Len < down/2 || o.Len > 2*down {
				t.Errorf("an outage of %v where %v is configured", o.Len, down)
			}
		}
	}
	if whole < 2 {
		t.Errorf("%d complete outages in the run (%d outages, %d lost of %d)", whole, len(outages), len(res.UpLost()), res.Sent)
	}
}

func (l *tunnelLab) putTunnelFlapping(up, down time.Duration) {
	l.t.Helper()
	putFault(l.t, l.e, fmt.Sprintf("fault: {family: tunnel, tunnel: {client: rA}, flapping: {up: %ds, down: %ds}}", int(up.Seconds()), int(down.Seconds())))
}

// A client that moves (another source port, as behind a NAT that was rebooted) takes its tunnel fault with it: the engine sees the new
// address at its next poll and moves the filters, and the flow is impaired again, without anyone writing anything.
func TestAClientThatMovesTakesItsTunnelFaultWithIt(t *testing.T) {
	l := newTunnelLab(t)
	putFault(t, l.e, "fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 30ms}, download: {latency: 150ms}}")
	expectImpaired(t, l.toClient, shape{up: 150 * time.Millisecond, down: 30 * time.Millisecond})
	old := l.e.Snapshot().PeerEndpoints[tClient]
	if !strings.HasPrefix(old, testbed.RemoteClientAddr+":") {
		t.Fatalf("the tunnel fault was compiled for %q", old)
	}
	// the client listens on another port; its next packet reaches the gateway from there
	l.top.RC.Must("wg", "set", "wgrA", "listen-port", "40123")
	// (an apply takes minutes on an emulated machine, and the engine does not start another while one is on its way)
	deadline := time.Now().Add(8 * time.Minute)
	for l.e.Snapshot().PeerEndpoints[tClient] != testbed.RemoteClientAddr+":40123" {
		if time.Now().After(deadline) {
			logs := l.logs.String()
			if len(logs) > 6000 {
				logs = logs[len(logs)-6000:]
			}
			t.Fatalf("the engine did not follow the client: %q, last error %q\n%s\n%s", l.e.Snapshot().PeerEndpoints[tClient], l.e.Snapshot().LastError, l.wgShow(), logs)
		}
		pingOK(l.top.RC, "", "10.99.0.1") // traffic from the new port makes the gateway learn the endpoint
		time.Sleep(500 * time.Millisecond)
	}
	if out := l.top.GW.Must("tc", "filter", "show", "dev", "wan0", "ingress"); !strings.Contains(out, "40123") || strings.Contains(out, "src_port 51820") {
		t.Errorf("the ingress filter did not move:\n%s", out)
	}
	waitReach(t, l.toClient)
	expectImpaired(t, l.toClient, shape{up: 150 * time.Millisecond, down: 30 * time.Millisecond})
	expectImpaired(t, l.clientToA, shape{up: 30 * time.Millisecond, down: 150 * time.Millisecond})
}

// The IFB comes with the first tunnel fault that impairs the packets from a peer and goes with the last, after the packets queued
// in it have drained. The host's own ingress qdisc on the uplink keeps its filter. An IFB, a tree and a filter that a gateway
// that died left behind are cleaned up by the first apply of the next one.
func TestTheIFBAndItsFiltersAreCreatedAndRemovedWithTheTunnelFaultsAndLeftoversAreCleanedUp(t *testing.T) {
	g := newWGGW(t)
	gw := g.top.GW
	// what a gateway that died left: the IFB, a tree on it and a filter that feeds it
	gw.Must("ip", "link", "add", "name", "ifb-cgw", "type", "ifb")
	gw.Must("ip", "link", "set", "ifb-cgw", "up")
	gw.Must("tc", "qdisc", "add", "dev", "ifb-cgw", "root", "handle", "1:", "htb", "default", "1")
	gw.Must("tc", "class", "add", "dev", "ifb-cgw", "parent", "1:", "classid", "1:1", "htb", "rate", "10gbit")
	gw.Must("tc", "qdisc", "add", "dev", "wan0", "ingress")
	gw.Must("tc", "filter", "add", "dev", "wan0", "parent", "ffff:", "handle", "5", "protocol", "ip", "prio", "10", "flower", "ip_proto", "udp",
		"src_ip", "192.0.2.77", "src_port", "9999", "action", "mirred", "egress", "redirect", "dev", "ifb-cgw")
	// and a filter of the host, which is not ours
	gw.Must("tc", "filter", "add", "dev", "wan0", "parent", "ffff:", "handle", "99", "protocol", "ip", "prio", "30", "flower", "ip_proto", "tcp",
		"src_ip", "192.0.2.88", "src_port", "9", "action", "drop")

	g.apply(nil)
	deadline := time.Now().Add(30 * time.Second)
	for strings.Contains(gw.Must("ip", "-o", "link", "show"), "ifb-cgw") {
		if time.Now().After(deadline) {
			t.Fatal("the IFB of the gateway that died is still there")
		}
		time.Sleep(200 * time.Millisecond)
	}
	ing := gw.Must("tc", "filter", "show", "dev", "wan0", "ingress")
	if strings.Contains(ing, "192.0.2.77") || !strings.Contains(ing, "192.0.2.88") {
		t.Errorf("the leftover filter is still there, or the host's is gone:\n%s", ing)
	}

	// the first fault that needs it makes the IFB
	conf := g.client(tHub, tClient)
	g.up(g.top.RC, "wgrA", conf.Conf)
	if !pingOK(g.top.RC, "", "10.99.0.1") {
		t.Fatalf("the tunnel is not up\n%s", g.wgShow())
	}
	res := putFault(t, g.e, "fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 20ms}}")
	if out := gw.Must("ip", "-d", "-o", "link", "show", "dev", "ifb-cgw"); !strings.Contains(out, "ifb") {
		t.Errorf("no IFB:\n%s", out)
	}
	if out := gw.Must("tc", "-s", "class", "show", "dev", "ifb-cgw"); !strings.Contains(out, "class htb") {
		t.Errorf("no tree on the IFB:\n%s", out)
	}
	// a second apply (an unrelated write) leaves it alone
	seed := gw.Must("tc", "qdisc", "show", "dev", "ifb-cgw")
	putFault(t, g.e, "target: {network: IoT}\nfault: {latency: 5ms}")
	if got := gw.Must("tc", "qdisc", "show", "dev", "ifb-cgw"); got != seed {
		t.Errorf("an unrelated write changed the IFB:\n%s\n%s", seed, got)
	}

	// the last one takes it away, after its grace period
	if _, err := g.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}
	if ing := gw.Must("tc", "filter", "show", "dev", "wan0", "ingress"); strings.Contains(ing, "198.51") || strings.Contains(ing, testbed.RemoteClientAddr) {
		t.Errorf("the filter that feeds the IFB is still there:\n%s", ing)
	}
	deadline = time.Now().Add(30 * time.Second)
	for strings.Contains(gw.Must("ip", "-o", "link", "show"), "ifb-cgw") {
		if time.Now().After(deadline) {
			t.Fatalf("the IFB outlived its last fault: %+v", g.e.RetiringTC())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if ing := gw.Must("tc", "filter", "show", "dev", "wan0", "ingress"); !strings.Contains(ing, "192.0.2.88") {
		t.Errorf("the host's filter went with ours:\n%s", ing)
	}
}

// The WireGuard actions on the real kernel: disable takes the peer off the interface (the tunnel is dead, the engine says
// the peer is offline), key_mismatch leaves the peer on the interface with a key the client cannot handshake with, and
// block_endpoint drops the encrypted UDP of the peer in both directions and counts what it dropped. Each is gone with its
// overlay, and the tunnel comes back without anyone touching the client. Nothing else is touched: the client machine's other
// traffic and a flow of a device to the server go on.
func TestWireGuardActionsCutTheTunnelAndEndWithTheirOverlay(t *testing.T) {
	l := newTunnelLab(t)
	beforeServer, beforeUnderlay := baseline(t, l.aToServer), baseline(t, l.underlay)
	ch, cancel := l.e.Subscribe()
	defer cancel()

	back := func(what string) {
		t.Helper()
		deadline := time.Now().Add(120 * time.Second)
		for !pingOK(l.top.A, "", testbed.ClientNetHost) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the tunnel did not come back\n%s\n%s", what, l.wgShow(), l.top.RC.Must("wg", "show"))
			}
		}
	}
	cut := func(what string, body string) model.Overlay {
		t.Helper()
		res := putFault(t, l.e, body)
		// let a ping that was in flight go, then nothing may get through
		time.Sleep(time.Second)
		if pingOK(l.top.A, "", testbed.ClientNetHost) || pingOK(l.top.RC, "", "10.99.0.1") {
			t.Errorf("%s: the tunnel still carries traffic", what)
		}
		isolated(t, l.aToServer, beforeServer)
		isolated(t, l.underlay, beforeUnderlay)
		return res.Overlay
	}
	end := func(o model.Overlay) {
		t.Helper()
		if _, err := l.e.DeleteOverlay(context.Background(), o.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
			t.Fatal(err)
		}
	}
	hubIf := "wg-lab-hub"

	// disable
	o := cut("disable", "wireguard: {client: rA, action: disable}")
	if peers := strings.TrimSpace(l.top.GW.Must("wg", "show", hubIf, "peers")); peers != "" {
		t.Errorf("the peer is still on the interface: %s", peers)
	}
	if _, ok := waitEvent(ch, engine.EventPeerOffline, time.Minute); !ok {
		t.Error("no wireguard_peer_offline event")
	}
	end(o)
	back("disable")

	// key_mismatch
	was := strings.TrimSpace(l.top.GW.Must("wg", "show", hubIf, "peers"))
	o = cut("key_mismatch", "wireguard: {client: rA, action: key_mismatch}")
	if now := strings.TrimSpace(l.top.GW.Must("wg", "show", hubIf, "peers")); now == was || now == "" {
		t.Errorf("the key on the interface is %q, the client's is %q", now, was)
	}
	end(o)
	if now := strings.TrimSpace(l.top.GW.Must("wg", "show", hubIf, "peers")); now != was {
		t.Errorf("the key did not come back: %q, want %q", now, was)
	}
	back("key_mismatch")

	// block_endpoint
	o = cut("block_endpoint", "wireguard: {client: rA, action: block_endpoint}")
	if peers := strings.TrimSpace(l.top.GW.Must("wg", "show", hubIf, "peers")); peers != was {
		t.Errorf("a blocked endpoint leaves the peer on the interface: %q", peers)
	}
	var act compiler.WGActionInfo
	for _, a := range l.e.Snapshot().WGActions {
		if a.Action == "block_endpoint" {
			act = a
		}
	}
	cs, err := l.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if act.Counter == "" || cs[act.Counter].Packets == 0 {
		t.Errorf("the block dropped nothing it counted: %+v %+v", act, cs[act.Counter])
	}
	end(o)
	back("block_endpoint")
	if st := l.e.Snapshot().WireGuard[tClient]; st.Name == "" {
		t.Errorf("no status of the client: %+v", l.e.Snapshot().WireGuard)
	}
}

// A blackout of the tunnel of a BGP link takes the learned routes away and brings them back, and the time it takes is reported
// (plan §2.2.1, S15: "a tunnel blackout withdrew the routes after 6 s (hold time 9 s); after restore they were back in 2.5 s").
// The session goes down after the hold time (9 s) has run out at the one side and the other, the route is withdrawn from the table
// of the gateway, and the events of the routing poll carry the times. The figures are logged; the assertions are the bounds the
// protocol timers give, wide for an emulated kernel.
func TestATunnelBlackoutOnABGPLinkWithdrawsTheLearnedRoutesAndTheReconvergenceIsReported(t *testing.T) {
	g := newBGP(t, 10, "10.60.0.0/24")
	ch, cancel := g.e.Subscribe()
	defer cancel()
	if err := g.e.PollRouting(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !g.established(90 * time.Second) {
		t.Fatalf("the BGP session did not come up\n%s", birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	if !g.waitRoute("10.60.0.0/24 via 10.255.0.1 dev wg-site-b", true, 60*time.Second) {
		t.Fatalf("the learned route is not in table 100\n%s", g.table100())
	}
	waitRoutingState(ch, "up", 30*time.Second)

	// The write answers when the kernel runs the fault, which on a slow or emulated machine is long after the first packet of the
	// tunnel was dropped (the blackout takes hold in the middle of the apply). The times are therefore taken from the call of
	// the write, from its answer, and from the events of the routing poll, and the bounds are the protocol timers': the
	// session is dropped when the hold time (9 s) has run since the last keepalive (every 3 s), so between 6 and 9 s after the
	// blackout began, which was after the call and before the answer.
	hold := 9 * time.Second
	called := time.Now()
	black := putFault(t, g.e, "fault: {family: tunnel, tunnel: {link: site-b}, blackout: true}")
	answered := time.Now()
	limit := hold + 20*time.Second
	if !testbed.Accurate() {
		limit = hold + 60*time.Second
	}
	if !g.waitRoute("10.60.0.0/24", false, limit) {
		t.Fatalf("the route is still in the table %v after the blackout was written\n%s\n%s", time.Since(called), g.table100(), birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	withdrawn := time.Now()
	down, ok := waitRoutingState(ch, "down", 30*time.Second)
	if !ok {
		t.Error("no routing_session_changed event (down)")
	}
	t.Logf("tunnel blackout of the BGP link (hold time %v): the write took %.1f s; the routes were withdrawn %.1f s after the call of the write and %.1f s after its answer; the session-down event came %.1f s after the call",
		hold, answered.Sub(called).Seconds(), withdrawn.Sub(called).Seconds(), withdrawn.Sub(answered).Seconds(), down.Time.Sub(called).Seconds())
	if min := hold * 2 / 3; withdrawn.Sub(called) < min {
		t.Errorf("the routes were withdrawn %v after the blackout was written, before the hold time (at least %v) can have run out", withdrawn.Sub(called), min)
	}
	if max := answered.Sub(called) + limit; withdrawn.Sub(called) > max {
		t.Errorf("withdrawn %v after the call, the write took %v and the hold time is %v", withdrawn.Sub(called), answered.Sub(called), hold)
	}
	if accurate := testbed.Accurate(); accurate && withdrawn.Sub(answered) > hold+5*time.Second {
		t.Errorf("withdrawn %v after the answer of the write, the hold time is %v", withdrawn.Sub(answered), hold)
	}
	// the learned prefix is not reachable
	if pingOK(g.top.A, "", testbed.SiteNetHost) {
		t.Error("A reaches the site's network through a dead tunnel")
	}

	// the end of the blackout: the session and the route come back; the time is the re-convergence
	endCalled := time.Now()
	if _, err := g.e.DeleteOverlay(context.Background(), black.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
		t.Fatal(err)
	}
	ended := time.Now()
	if !g.waitRoute("10.60.0.0/24 via 10.255.0.1 dev wg-site-b", true, 120*time.Second) {
		t.Fatalf("the route did not come back\n%s\n%s", g.table100(), birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	back := time.Now()
	up, ok := waitRoutingState(ch, "up", 30*time.Second)
	if !ok {
		t.Error("no routing_session_changed event (up)")
	}
	t.Logf("re-convergence: the routes were back %.1f s after the call that ended the blackout and %.1f s after its answer; the session-up event came %.1f s after the call; the session was down for %.1f s",
		back.Sub(endCalled).Seconds(), back.Sub(ended).Seconds(), up.Time.Sub(endCalled).Seconds(), up.Time.Sub(down.Time).Seconds())
	if back.Sub(ended) > 90*time.Second {
		t.Errorf("re-convergence took %v after the blackout ended", back.Sub(ended))
	}
	if !pingOK(g.top.A, "", testbed.SiteNetHost) {
		t.Errorf("A does not reach the site's network again\n%s", g.table100())
	}
}

// wireguardLinkRemote is the configuration of the remote side of the link site-b, as the product exports it.
func wireguardLinkRemote(g *wgGW) (string, error) {
	remote, err := wireguard.LinkRemoteConfig(g.export(), tLink)
	if err != nil {
		return "", err
	}
	return remote.Conf, nil
}
