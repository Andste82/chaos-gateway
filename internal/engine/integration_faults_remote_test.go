//go:build testbed

package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The measurement tests of M8b (plan §4.3) for traffic that crosses a WireGuard tunnel: between a test
// network and a WireGuard client network, and towards a network the gateway learned over BGP. The
// fault is written through the engine, as in integration_faults_test.go, and the flows are measured
// the same way.

// putFault writes an overlay through the engine and returns when it is verified in the kernel.
func putFault(t *testing.T, e *engine.Engine, body string) engine.OverlayResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := e.PutOverlay(ctx, engine.OverlayWrite{Owner: admin, Request: overlayRequest(t, body)})
	if err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return res
}

// waitReach waits until a datagram probe from the flow's namespace gets an answer, which is when the
// route, the tunnel and the access matrix let the flow through.
func waitReach(t *testing.T, f flow) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		res, err := f.echo.Probe(f.from, testbed.ProbeOptions{Src: f.src, Count: 3, Interval: 200 * time.Millisecond, Settle: time.Second})
		if err == nil && res.Replied > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s does not reach its echo: %v %v", f.name, res, err)
		}
	}
}

// Traffic between a test network and a WireGuard client network. A (IoT) opens connections to a host in
// the network behind the client, the host opens connections to A: each has a fault of its own, with
// different directions, and the fault belongs to the initiator of the connection (plan §2.4, E12).
// The tunnel interface carries one side of each, the IoT bridge the other. A's flow to the uplink and B's
// flow to the client network are named by neither fault and do not change.
func TestFaultsBetweenATestNetworkAndAWireGuardClientNetworkAreMeasuredPerDirection(t *testing.T) {
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

	echoClient := testbed.StartEcho(t, g.top.RC, testbed.ClientNetHost, probePort)
	echoA := testbed.StartEcho(t, g.top.A, testbed.ClientAAddr, probePort)
	echoServer := testbed.StartEcho(t, g.top.Server, testbed.ServerAddr, probePort)
	aToClient := flow{name: "A to the client network", from: g.top.A, echo: echoClient}
	clientToA := flow{name: "the client network to A", from: g.top.RC, src: testbed.ClientNetHost, echo: echoA}
	bToClient := flow{name: "B to the client network", from: g.top.B, echo: echoClient}
	aToServer := flow{name: "A to the server", from: g.top.A, echo: echoServer}
	waitReach(t, aToClient)
	waitReach(t, clientToA)
	beforeB, beforeServer := baseline(t, bToClient), baseline(t, aToServer)

	fromA := shape{up: 150 * time.Millisecond, upJitter: 15 * time.Millisecond, upLoss: 0.05, down: 30 * time.Millisecond}
	fromClient := shape{up: 60 * time.Millisecond, upJitter: 6 * time.Millisecond, down: 120 * time.Millisecond, downLoss: 0.03}
	putFault(t, g.e, "target: {device: dev-a}\nfault: "+fromA.yaml("destination: {cidr: 10.50.0.0/24}"))
	putFault(t, g.e, "target: {remote_network: {cidr: "+testbed.ClientNetHost+"/32}}\nfault: "+fromClient.yaml(""))

	expectImpaired(t, aToClient, fromA)
	expectImpaired(t, clientToA, fromClient)
	expectUnaffected(t, bToClient, beforeB)
	expectUnaffected(t, aToServer, beforeServer)
}

// Traffic over a route learned via BGP: the remote site announces 10.60.0.0/24, the gateway learns it
// into its routing table, and a fault on A's traffic to that prefix impairs the flow that the learned
// route carries (through the link's tunnel) in both directions. A's flow to the uplink and B's flow to
// the same prefix are not impaired.
func TestAFaultOnTrafficOverARouteLearnedByBGPIsMeasuredPerDirection(t *testing.T) {
	g := newBGPWith(t, func(c *model.Configuration) {
		routingMod(10, "")(c)
		withDevices(c)
	}, "10.60.0.0/24")
	if err := g.e.PollRouting(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if !g.established(90 * time.Second) {
		t.Fatalf("the BGP session did not come up\n%s\n%s", birdc(t, g.gwSock, "show", "protocols", "all"), birdc(t, g.siteSock, "show", "protocols", "all"))
	}
	if !g.waitRoute("10.60.0.0/24 via 10.255.0.1 dev wg-site-b", true, 60*time.Second) {
		t.Fatalf("the route is not learned\n%s", g.table100())
	}
	if line := routeLine(g.table100(), "10.60.0.0/24"); !strings.Contains(line, "proto bird") {
		t.Fatalf("the route to the site's network is not one BGP learned: %q", line)
	}
	// the gateway announces the IoT network to the site, which is how the site answers
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(g.top.Site.Must("ip", "route", "show"), "10.10.0.0/24") && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
	resolveDevices(t, g.e)

	echoSite := testbed.StartEcho(t, g.top.Site, testbed.SiteNetHost, probePort)
	echoServer := testbed.StartEcho(t, g.top.Server, testbed.ServerAddr, probePort)
	aToSite := flow{name: "A to the learned network", from: g.top.A, echo: echoSite}
	bToSite := flow{name: "B to the learned network", from: g.top.B, echo: echoSite}
	aToServer := flow{name: "A to the server", from: g.top.A, echo: echoServer}
	waitReach(t, aToSite)
	beforeB, beforeServer := baseline(t, bToSite), baseline(t, aToServer)

	putFault(t, g.e, "target: {device: dev-a}\nfault: "+standard.yaml("destination: {cidr: 10.60.0.0/24}"))
	expectImpaired(t, aToSite, standard)
	expectUnaffected(t, bToSite, beforeB)
	expectUnaffected(t, aToServer, beforeServer)
}

// routeLine returns the line of a routing table dump that starts with the prefix.
func routeLine(table, prefix string) string {
	for _, l := range strings.Split(table, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return l
		}
	}
	return ""
}
