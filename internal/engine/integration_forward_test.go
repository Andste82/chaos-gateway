//go:build testbed

package engine_test

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M35a on the real kernel through the engine (plan §5 M35a, Tests, level 1): the default topology with its
// three ports (uplink wan0, the test networks, management mgmt0). Device A (found by its MAC) listens on tcp
// 8883 and 7050 and on udp 5353; the server behind the uplink and the management host reach it through
// the forwards; the engine follows A's address and the uplink's; a fault on A does not impair a forwarded
// connection; a rule on A cuts an established one. The compiler's kernel tests (forwardkernel_test.go) cover
// the packet path on its own: ranges, the rule actions, the counters against the matrix's drop.

const (
	fwDevA   = "c3333333-0000-4000-8000-000000000001"
	fwTCP    = "c3333333-0000-4000-8000-0000000000a1"
	fwUDP    = "c3333333-0000-4000-8000-0000000000a2"
	fwMgmt   = "c3333333-0000-4000-8000-0000000000a3"
	fwIoT    = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	uplinkIP = testbed.UplinkGateway
)

// withForwards gives the configuration device A (by its MAC) and three forwards to it: tcp 8080 -> 8883
// and udp 5353 from behind the uplink, tcp 8443 -> 7050 from the management network.
func withForwards(c *model.Configuration) {
	macs := []string{testbed.ClientAMAC}
	devs := map[string]model.Device{}
	if c.Devices != nil {
		devs = *c.Devices
	}
	devs[fwDevA] = model.Device{Name: "fw-a", Network: ptrS(fwIoT), Identifiers: &model.DeviceIdentifiers{Macs: &macs}}
	c.Devices = &devs
	port := func(p int) *int { return &p }
	target := model.PortForwardTarget{Device: ptrS(fwDevA)}
	c.PortForwards = &map[string]model.PortForward{
		fwTCP:  {Name: ptrS("mqtt"), Protocol: model.PortForwardProtocolTcp, Entry: model.PortForwardEntryUplink, ExternalPort: 8080, TargetPort: port(8883), Target: target},
		fwUDP:  {Name: ptrS("echo"), Protocol: model.PortForwardProtocolUdp, Entry: model.PortForwardEntryUplink, ExternalPort: 5353, Target: target},
		fwMgmt: {Name: ptrS("mgmt"), Protocol: model.PortForwardProtocolTcp, Entry: model.PortForwardEntryManagement, ExternalPort: 8443, TargetPort: port(7050), Target: target},
	}
}

// startForwardLab applies the configuration, follows the host as the product does (the poller every
// 200 ms, the neighbor watcher, the watcher of the host's addresses) and waits until A's forwards are
// active in the kernel.
func startForwardLab(t *testing.T, mod func(*model.Configuration)) *real {
	t.Helper()
	r := newReal(t, nil, realClock)
	if _, err := r.apply(r.revision(func(c *model.Configuration) {
		withForwards(c)
		if mod != nil {
			mod(c)
		}
	}), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.e.PollObserved(context.Background(), 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := r.e.FollowNeighbors(context.Background(), 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := r.e.FollowHost(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	r.top.A.Start("python3", "-c", accessServerScript)
	r.top.A.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	waitForward(t, r.e, fwTCP, testbed.ClientAAddr, 30*time.Second)
	return r
}

// waitForward waits until the applied forward sends its connections to addr.
func waitForward(t *testing.T, e *engine.Engine, id, addr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		for _, f := range e.Snapshot().Forwards {
			if f.ID == id && f.Active && f.Target.String() == addr {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the forward %s does not go to %s: %+v", id, addr, e.Snapshot().Forwards)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// tryAt makes one attempt from ns to ip:port (tcp or udp) with a timeout in seconds.
func tryAt(ns *testbed.Namespace, proto, ip string, port int, timeout float64) string {
	out, _ := ns.Run(context.Background(), "python3", "-c", accessTry, proto, ip, strconv.Itoa(port), strconv.FormatFloat(timeout, 'f', -1, 64))
	return strings.TrimSpace(out)
}

// eventually tries until the attempt answers ok and returns how long that took.
func eventually(t *testing.T, ns *testbed.Namespace, proto, ip string, port int, d time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for {
		if tryAt(ns, proto, ip, port, 0.3) == "ok" {
			return time.Since(start)
		}
		if time.Since(start) > d {
			t.Fatalf("%s %s:%d from %s never answered", proto, ip, port, ns.Short)
		}
	}
}

// convergence records how long a forward took to follow a change: the plan's target is one second; the
// bound is P2-M7-03's three seconds for an element update on the real kernel, and under software
// emulation the time is recorded without the bound.
func convergence(t *testing.T, flow string, took time.Duration) {
	t.Helper()
	t.Logf("%s: the forward followed after %v", flow, took.Round(time.Millisecond))
	hi := 3 * time.Second
	if !testbed.Accurate() {
		hi = time.Minute
	}
	if err := testbed.Measure(t, flow).Duration("", "forward convergence", took, time.Second, 0, hi); err != nil {
		t.Errorf("the forward followed too late: %v", err)
	}
}

// The forwards reach A's TCP and UDP listeners from behind the uplink and its TCP listener from the
// management network (whose replies go back through the management interface, not the uplink), and the
// counters count the connections. A's new address is followed within a second, by an element update.
func TestForwardsReachTheDeviceFromBothSidesAndFollowItsAddress(t *testing.T) {
	r := startForwardLab(t, nil)
	eventually(t, r.top.Server, "tcp", uplinkIP, 8080, 30*time.Second)
	if got := tryAt(r.top.Server, "udp", uplinkIP, 5353, 2); got != "ok" {
		t.Errorf("udp through the forward: %s", got)
	}
	if got := tryAt(r.top.Mgmt, "tcp", testbed.MgmtGateway, 8443, 2); got != "ok" {
		t.Errorf("tcp from the management network: %s", got)
	}
	// the matrix keeps everything else from the uplink out of the test network
	if got := tryAt(r.top.Server, "tcp", testbed.ClientAAddr, 8883, 1); got == "ok" {
		t.Errorf("the server reaches the device without a forward")
	}
	cs, err := r.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fwTCP, fwUDP, fwMgmt} {
		if v := cs[compiler.ForwardCounterName(id)]; v.Packets == 0 {
			t.Errorf("the counter of %s counts nothing: %+v", id, v)
		}
	}

	// A moves to another address
	r.top.A.Must("ip", "addr", "flush", "dev", "eth0")
	r.top.A.Must("ip", "addr", "add", "10.10.0.61/24", "dev", "eth0")
	r.top.A.Must("ip", "route", "add", "default", "via", testbed.LAN0Gateway)
	start := time.Now()
	r.top.A.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	waitForward(t, r.e, fwTCP, "10.10.0.61", 30*time.Second)
	eventually(t, r.top.Server, "tcp", uplinkIP, 8080, 30*time.Second)
	convergence(t, "A's address change", time.Since(start))
}

// A new address of the uplink (a new DHCP lease) is followed within a second: the kernel's lookup of the
// interface's addresses takes it without any change of the ruleset.
func TestAForwardFollowsANewUplinkAddress(t *testing.T) {
	r := startForwardLab(t, nil)
	eventually(t, r.top.Server, "tcp", uplinkIP, 8080, 30*time.Second)
	// the old address goes first: the kernel deletes a subnet's secondary addresses with its primary one
	r.top.GW.Must("ip", "addr", "del", uplinkIP+"/24", "dev", "wan0")
	r.top.GW.Must("ip", "addr", "add", "203.0.113.2/24", "dev", "wan0")
	took := eventually(t, r.top.Server, "tcp", "203.0.113.2", 8080, 30*time.Second)
	convergence(t, "the uplink's address change", took)
	// the engine takes the new address into the forward's status
	deadline := time.Now().Add(30 * time.Second)
	for {
		facts, addrs := r.e.ForwardFacts(r.e.Snapshot().Config)
		if facts.Entry["uplink"] && addrs["uplink"].String() == "203.0.113.2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the engine does not see the uplink's new address: %v", addrs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// rttScript measures the round trip of one byte over a TCP connection, five times, and prints the
// smallest in milliseconds.
const rttScript = `
import socket, sys, time
s = socket.socket(); s.settimeout(10); s.connect((sys.argv[1], int(sys.argv[2])))
best = None
for _ in range(5):
    t = time.monotonic(); s.sendall(b"x"); s.recv(10); d = (time.monotonic() - t) * 1000
    best = d if best is None else min(best, d)
print(int(best))
`

func rtt(t *testing.T, ns *testbed.Namespace, ip string, port int) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := ns.Run(ctx, "python3", "-c", rttScript, ip, strconv.Itoa(port))
	if err != nil {
		t.Fatalf("rtt from %s: %v: %s", ns.Short, err, out)
	}
	ms, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("rtt from %s: %q", ns.Short, out)
	}
	return time.Duration(ms) * time.Millisecond
}

// A fault on the device impairs what the device initiates, but not a connection that reaches it through a
// forward (M35b): the classification never marks it. The forwarded connection is measured between two
// measurements of A's own connection that show the fault in force. A's neighbor entry may go stale during
// a slow apply, but A keeps its address while a forward sends to it (P31-AUDIT-14), so its fault keeps
// selecting it and one measurement is enough.
func TestADeviceFaultDoesNotImpairAForwardedConnection(t *testing.T) {
	r := startForwardLab(t, nil)
	r.top.Server.Start("python3", "-c", accessServerScript)
	eventually(t, r.top.Server, "tcp", uplinkIP, 8080, 30*time.Second)
	if _, err := r.e.PutOverlay(context.Background(), engine.OverlayWrite{Owner: model.Owner{Type: "user", Id: "test"},
		Request: overlayRequest(t, "target: {device: "+fwDevA+"}\nfault: {latency: 400ms}")}); err != nil {
		t.Fatal(err)
	}
	// A's own connection works through its fault (an attempt waits longer than the fault's 400 ms)
	deadline := time.Now().Add(30 * time.Second)
	for tryAt(r.top.A, "tcp", testbed.ServerAddr, 8883, 10) != "ok" {
		if time.Now().After(deadline) {
			t.Fatalf("A does not reach the server through its fault (identity %v)", r.e.Snapshot().Identity.Addresses)
		}
	}
	ownBefore := rtt(t, r.top.A, testbed.ServerAddr, 8883)
	fwd := rtt(t, r.top.Server, uplinkIP, 8080)
	ownAfter := rtt(t, r.top.A, testbed.ServerAddr, 8883)
	if ownBefore < 400*time.Millisecond || ownAfter < 400*time.Millisecond {
		t.Fatalf("A's fault is not in force around the measurement: A's own round trips %v and %v (identity %v)", ownBefore, ownAfter, r.e.Snapshot().Identity.Addresses)
	}
	if fwd >= 200*time.Millisecond {
		t.Errorf("a forwarded connection to A is delayed by A's fault: %v (A's own: %v, %v)", fwd, ownBefore, ownAfter)
	}
}

// P31-AUDIT-14 (decided 2026-10-18, option (a)): A is known only from the neighbor table (by its MAC, a static
// address, no lease). With a short reachable time on its bridge its entry goes stale (STALE, or DELAY when the
// gateway sends to A; a long first-probe delay keeps it there instead of PROBE and REACHABLE again), and with its
// connections gone nothing on the address is in use; the forward keeps A as its target all the same, and a new
// forwarded connection reaches A.
func TestAForwardReachesAnIdleDeviceWhoseNeighborEntryIsStale(t *testing.T) {
	r := startForwardLab(t, nil)
	bridge := ""
	for _, b := range r.e.Snapshot().Bridges {
		if strings.EqualFold(b.NetworkID, fwIoT) {
			bridge = b.Name
		}
	}
	r.top.GW.Must("sysctl", "-w", "net.ipv4.neigh."+bridge+".base_reachable_time_ms=500")
	r.top.GW.Must("sysctl", "-w", "net.ipv4.neigh."+bridge+".delay_first_probe_time=60")
	// the states the engine reads as stale (linux.Neighbor.Stale)
	stale := func(out string) bool {
		return strings.Contains(out, "STALE") || strings.Contains(out, "DELAY") || strings.Contains(out, "PROBE")
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		// A's own connections (the ping of the lab's start) go, so its address is no active source
		_, _ = r.top.GW.Run(context.Background(), "conntrack", "-D", "-s", testbed.ClientAAddr)
		_, _ = r.top.GW.Run(context.Background(), "conntrack", "-D", "-d", testbed.ClientAAddr)
		out := r.top.GW.Must("ip", "neigh", "show", testbed.ClientAAddr, "dev", bridge)
		if stale(out) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A's neighbor entry does not go stale: %s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// several readings of the host (every 200 ms) see the stale entry without a connection
	time.Sleep(2 * time.Second)
	if out := r.top.GW.Must("ip", "neigh", "show", testbed.ClientAAddr, "dev", bridge); !stale(out) {
		t.Fatalf("A's neighbor entry is not stale any more, the test proves nothing: %s", out)
	}
	var st *string
	for _, f := range r.e.Snapshot().Forwards {
		if f.ID == fwTCP {
			s := f.Target.String()
			if !f.Active {
				s = "inactive: " + f.Reason
			}
			st = &s
		}
	}
	if st == nil || *st != testbed.ClientAAddr {
		t.Fatalf("the forward to A with A's entry stale: %v (identity %v)", st, r.e.Snapshot().Identity.Addresses)
	}
	if got := tryAt(r.top.Server, "tcp", uplinkIP, 8080, 5); got != "ok" {
		t.Errorf("a new forwarded connection to the idle A: %s", got)
	}
}

// "Also cut existing connections" of a rule on the device resets an established forwarded connection and
// deletes its tracked flow; a new one meets the rule. A forwarded connection to another port of the device
// stays.
func TestARuleOnTheDeviceCutsAnEstablishedForwardedConnection(t *testing.T) {
	r := startForwardLab(t, nil)
	eventually(t, r.top.Server, "tcp", uplinkIP, 8080, 30*time.Second)
	hold := r.top.Server.Start("python3", "-c", accessHold, uplinkIP, "8080")
	keep := r.top.Mgmt.Start("python3", "-c", accessHold, testbed.MgmtGateway, "8443")
	waitText(t, hold, "ok", 60*time.Second)
	waitText(t, keep, "ok", 60*time.Second)
	if _, err := r.e.PutOverlay(context.Background(), engine.OverlayWrite{Owner: model.Owner{Type: "user", Id: "test"},
		Request: overlayRequest(t, "target: {device: "+fwDevA+"}\nrule: {protocol: tcp, ports: [8883], action: drop, cut_existing: true}")}); err != nil {
		t.Fatal(err)
	}
	waitText(t, hold, "ConnectionResetError", 30*time.Second)
	if got := tryAt(r.top.Server, "tcp", uplinkIP, 8080, 2); got != "timeout" {
		t.Errorf("a new forwarded connection after the rule: %s", got)
	}
	before := strings.Count(keep.Output(), "ok")
	time.Sleep(time.Second)
	if out := keep.Output(); strings.Count(out, "ok") == before || strings.Contains(out, "Error") {
		t.Errorf("the forwarded connection to another port stopped: %s", out)
	}
}

// P31-AUDIT-13 (decided 2026-10-18, option (b)): a connection that came in through a management-side forward
// lives on after the forward is deleted (D11), and its replies still go back through the management interface:
// in the three-port topology the chain fwd_reply, the policy rule 925 and table 101 stay without any
// management forward, so a reply whose conntrack mark has the bit never takes the uplink. A probe table counts
// what of the connection leaves through the uplink.
func TestTheRepliesOfAManagementForwardedConnectionStayOffTheUplinkAfterTheForwardIsDeleted(t *testing.T) {
	r := startForwardLab(t, nil)
	eventually(t, r.top.Mgmt, "tcp", testbed.MgmtGateway, 8443, 30*time.Second)
	r.top.GW.Must("nft", "add", "table", "inet", "probe13")
	r.top.GW.Must("nft", "add", "chain", "inet", "probe13", "post", "{ type filter hook postrouting priority 0 ; policy accept ; }")
	r.top.GW.Must("nft", "add", "rule", "inet", "probe13", "post", "oifname", "wan0", "ct", "status", "dnat", "ct", "original", "proto-dst", "8443", "counter")
	hold := r.top.Mgmt.Start("python3", "-c", accessHold, testbed.MgmtGateway, "8443")
	waitText(t, hold, "ok", 60*time.Second)

	if _, err := r.apply(r.revision(func(c *model.Configuration) {
		withForwards(c)
		delete(*c.PortForwards, fwMgmt)
	}), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range r.e.Snapshot().Forwards {
		if f.ID == fwMgmt {
			t.Fatalf("the management forward is still applied: %+v", f)
		}
	}
	if out := r.top.GW.Must("ip", "rule", "show", "priority", strconv.Itoa(compiler.ForwardReplyRulePriority)); !strings.Contains(out, "lookup "+strconv.Itoa(compiler.ForwardReplyTable)) {
		t.Fatalf("the policy rule of the marked replies went with the last management forward: %q", out)
	}
	before := strings.Count(hold.Output(), "ok")
	time.Sleep(2 * time.Second)
	if out := hold.Output(); strings.Count(out, "ok") <= before+5 || strings.Contains(out, "Error") || strings.Contains(out, "timeout") {
		t.Errorf("the connection through the deleted forward stopped getting its replies: %s", out)
	}
	if out := r.top.GW.Must("nft", "list", "chain", "inet", "probe13", "post"); !strings.Contains(out, "packets 0 ") {
		t.Errorf("replies of the management-forwarded connection left through the uplink:\n%s", out)
	}
}

// allowAllForA is an access allow rule for device A without a destination, protocol or port: the
// broadest rule there is for A.
func allowAllForA(c *model.Configuration) {
	name, dev := "a-anywhere", fwDevA
	withConfigRules(map[string]model.AccessRule{cfgRuleA: {Name: &name, Source: model.Scope{Device: &dev}, Action: model.AccessActionAllow}}, cfgRuleA)(c)
}

// A forward from the management side does not make the management network reachable for a device
// (P31-M35a-10, option (c)): the policy table that a device's packets are looked up in has no route to
// the management subnet; only the replies of a forwarded connection go back through the management
// interface, by their mark. So even with an allow rule without a destination A cannot open a connection
// to a host of the management network while the management forward works (and the management guard in
// front of the access rules would drop it if the routing let it through, see below). And the forwards' counters
// count their connections although that allow rule, not the forward, accepts them (P31-M35a-09).
func TestAnAllowAllRuleDoesNotOpenTheManagementNetworkWhileAManagementForwardWorks(t *testing.T) {
	r := startForwardLab(t, allowAllForA)
	r.top.Mgmt.Start("python3", "-c", accessServerScript)
	eventually(t, r.top.GW, "tcp", testbed.MgmtPeer, 7050, 30*time.Second) // the management host listens
	eventually(t, r.top.Mgmt, "tcp", testbed.MgmtGateway, 8443, 30*time.Second)
	if got := tryAt(r.top.Mgmt, "tcp", testbed.MgmtGateway, 8443, 2); got != "ok" {
		t.Errorf("tcp from the management network through the forward: %s", got)
	}
	for _, port := range []int{7050, 8883} {
		if got := tryAt(r.top.A, "tcp", testbed.MgmtPeer, port, 2); got == "ok" {
			t.Errorf("A reaches the management host on tcp %d with an allow rule", port)
		}
	}
	if got := tryAt(r.top.A, "udp", testbed.MgmtPeer, 5353, 2); got == "ok" {
		t.Error("A reaches the management host over udp with an allow rule")
	}
	// the allow rule itself works: A reaches the server behind the uplink
	r.top.Server.Start("python3", "-c", accessServerScript)
	eventually(t, r.top.A, "tcp", testbed.ServerAddr, 8883, 30*time.Second)

	// the forwarded connections the allow rule accepted are counted by the forwards
	eventually(t, r.top.Server, "tcp", uplinkIP, 8080, 30*time.Second)
	cs, err := r.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fwTCP, fwMgmt} {
		if v := cs[compiler.ForwardCounterName(id)]; v.Packets == 0 || v.LastUsed == nil {
			t.Errorf("the counter of %s counts nothing the allow rule accepted: %+v", id, v)
		}
	}
}

// A forward from the management side answers a peer behind a router of the management network
// (P31-M35a-05, option (c)): its replies are routed by their mark through a table with the management
// interface's subnet and its default gateway. The testbed's management host is the gateway's default
// router on the management interface (192.168.56.254); an address on its loopback outside the
// management subnet stands for a host behind that router, as the remote sites' loopbacks do behind the
// uplink. A device does not reach that address: nothing but the replies of the forwards is routed to the
// management side.
func TestAManagementForwardAnswersAPeerBehindARouterOfTheManagementNetwork(t *testing.T) {
	r := startForwardLab(t, nil)
	r.top.Mgmt.Addr("lo", mgmtRemoteHost+"/32")
	eventually(t, r.top.Mgmt, "tcp", testbed.MgmtGateway, 8443, 30*time.Second)
	out, _ := r.top.Mgmt.Run(context.Background(), "python3", "-c", tryFromScript, mgmtRemoteHost, "tcp", testbed.MgmtGateway, "8443", "3")
	if got := strings.TrimSpace(out); got != "ok" {
		t.Errorf("tcp from %s behind the management router through the forward: %s", mgmtRemoteHost, got)
	}
	r.top.Mgmt.Start("python3", "-c", accessServerScript)
	if got := tryAt(r.top.A, "tcp", mgmtRemoteHost, 7050, 2); got == "ok" {
		t.Errorf("A reaches %s behind the management router", mgmtRemoteHost)
	}
}

// mgmtRemoteHost is an address behind the management network's router (the management host's loopback).
const mgmtRemoteHost = "172.16.56.10"

// tryFromScript is accessTry from a given source address: source, tcp or udp, address, port, timeout.
const tryFromScript = `
import socket, sys
src, proto, ip, port, t = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), float(sys.argv[5])
try:
    if proto == "tcp":
        s = socket.socket(); s.settimeout(t); s.bind((src, 0)); s.connect((ip, port)); s.sendall(b"hi"); r = s.recv(10)
    else:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(t); s.bind((src, 0)); s.connect((ip, port)); s.send(b"hi"); r = s.recv(10)
    print("ok" if r == b"hi" else "bad")
except ConnectionRefusedError: print("refused")
except ConnectionResetError: print("reset")
except socket.timeout: print("timeout")
`

// withoutPolicyDefault takes the uplink's default route out of the policy table 100, as the kernel does
// when the uplink loses its address (an expired DHCP lease: the routes via the device go, and the next
// compile fails with uplink_no_gateway), and gives the management host a route back to the local test
// networks, so only the gateway's own filter can keep a device out of the management network: a device's
// packet to it falls past the policy rules to the main table, which knows the management subnet.
func withoutPolicyDefault(t *testing.T, r *real) {
	t.Helper()
	r.top.GW.Must("ip", "route", "del", "default", "table", strconv.Itoa(compiler.PolicyTable))
	r.top.Mgmt.Route("10.0.0.0/8", "via", testbed.MgmtGateway)
	bridge := ""
	for _, b := range r.e.Snapshot().Bridges {
		if strings.EqualFold(b.NetworkID, fwIoT) {
			bridge = b.Name
		}
	}
	if out := r.top.GW.Must("ip", "route", "get", testbed.MgmtPeer, "from", testbed.ClientAAddr, "iif", bridge); !strings.Contains(out, "dev mgmt0") {
		t.Fatalf("A's packets to the management host must now leave through mgmt0: %s", out)
	}
}

// policyDefaultStillGone fails the test when something put the policy table's default back: then the
// probes would prove nothing about the guard.
func policyDefaultStillGone(t *testing.T, r *real) {
	t.Helper()
	if out := r.top.GW.Must("ip", "route", "show", "default", "table", strconv.Itoa(compiler.PolicyTable)); strings.TrimSpace(out) != "" {
		t.Fatalf("the policy table has a default again, the probes prove nothing: %s", out)
	}
}

// The management guard of the three-port topology (P31-M35a-10, decided on 2026-10-17): routing alone does
// not keep a device out of the management network. Without the policy table's default a device's packet to
// the management host falls through to the main table and leaves through mgmt0, and the management host
// can answer; an allow rule without a destination would carry it there. The guard in front of the access
// rules drops it (and counts it), `explain` names the guard, not the rule, and the management forward
// still answers: its replies are established and routed by their mark.
func TestWithoutTheUplinkDefaultAnAllowAllRuleStillDoesNotOpenTheManagementNetwork(t *testing.T) {
	r := startForwardLab(t, allowAllForA)
	r.top.Mgmt.Start("python3", "-c", accessServerScript)
	eventually(t, r.top.GW, "tcp", testbed.MgmtPeer, 7050, 30*time.Second) // the management host listens
	withoutPolicyDefault(t, r)

	before := r.counters()["forward_drop"].Packets
	for _, port := range []int{7050, 8883} {
		if got := tryAt(r.top.A, "tcp", testbed.MgmtPeer, port, 2); got == "ok" {
			t.Errorf("A reaches the management host on tcp %d without the policy default", port)
		}
	}
	if got := tryAt(r.top.A, "udp", testbed.MgmtPeer, 5353, 2); got == "ok" {
		t.Error("A reaches the management host over udp without the policy default")
	}
	policyDefaultStillGone(t, r)
	if after := r.counters()["forward_drop"].Packets; after <= before {
		t.Errorf("the guard counted nothing: forward_drop %d -> %d", before, after)
	}
	ex, err := r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(testbed.ClientAAddr), Dst: testbed.MgmtPeer, Protocol: "tcp", Port: 7050})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Access.Verdict != "drop" || ex.Access.Layer != "access_matrix" || ex.Access.Rule != "" {
		t.Errorf("explain = %+v, want the guard (a drop in the access matrix, no rule)", ex.Access)
	}
	// the management forward still answers
	if got := tryAt(r.top.Mgmt, "tcp", testbed.MgmtGateway, 8443, 2); got != "ok" {
		t.Errorf("tcp from the management network through the forward: %s", got)
	}
	policyDefaultStillGone(t, r)
}

// The one thing that opens the management network for a device in the three-port topology is an explicit
// entry of the access matrix, as in the two-port one (plan §2.16): with "IoT -> management: allow" and the
// same lost policy default, A reaches the management host, and `explain` agrees with the kernel.
func TestAMatrixEntryOpensTheManagementNetworkBehindAManagementInterfaceOfItsOwn(t *testing.T) {
	r := startForwardLab(t, func(c *model.Configuration) {
		allowAllForA(c)
		entries := []model.MatrixEntry{}
		if c.AccessMatrix != nil && c.AccessMatrix.Entries != nil {
			entries = *c.AccessMatrix.Entries
		}
		iot, m := fwIoT, model.MatrixEndpointManagement(true)
		entries = append(entries, model.MatrixEntry{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Management: &m}, Policy: model.MatrixEntryPolicyAllow})
		c.AccessMatrix = &model.AccessMatrix{Entries: &entries}
	})
	r.top.Mgmt.Start("python3", "-c", accessServerScript)
	eventually(t, r.top.GW, "tcp", testbed.MgmtPeer, 7050, 30*time.Second)
	withoutPolicyDefault(t, r)
	if got := tryAt(r.top.A, "tcp", testbed.MgmtPeer, 7050, 3); got != "ok" {
		t.Errorf("A does not reach the management host with a matrix entry: %s", got)
	}
	policyDefaultStillGone(t, r)
	ex, err := r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(testbed.ClientAAddr), Dst: testbed.MgmtPeer, Protocol: "tcp", Port: 7050})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Access.Verdict != "allow" {
		t.Errorf("explain = %+v, want allow", ex.Access)
	}
	if got := tryAt(r.top.Mgmt, "tcp", testbed.MgmtGateway, 8443, 2); got != "ok" {
		t.Errorf("tcp from the management network through the forward: %s", got)
	}
}

// The normal path of an explicit matrix entry to the management network in the three-port topology
// (P31-AUDIT-01): the policy table keeps its default, so A's packet to a management address leaves through
// the uplink, to a router there that knows the management network (here the server holds the address).
// The guard's return and the entry match the destination, not the management interface, so the entry
// opens it; with allowed sources narrower than the management subnet the entry opens the rest of the
// subnet too, which the guard drops as well. `explain` agrees in both cases.
func TestAMatrixEntryOpensTheManagementNetworkThroughTheUplink(t *testing.T) {
	const behindUplink = "192.168.56.77"
	entry := func(c *model.Configuration) {
		iot, m := fwIoT, model.MatrixEndpointManagement(true)
		c.AccessMatrix = &model.AccessMatrix{Entries: &[]model.MatrixEntry{{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Management: &m}, Policy: model.MatrixEntryPolicyAllow}}}
	}
	r := startForwardLab(t, entry)
	r.top.Server.Addr("lo", behindUplink+"/32")
	r.top.Server.Start("python3", "-c", accessServerScript)
	eventually(t, r.top.Server, "tcp", behindUplink, 7050, 30*time.Second) // the server listens (the gateway itself would ask mgmt0)
	bridge := ""
	for _, b := range r.e.Snapshot().Bridges {
		if strings.EqualFold(b.NetworkID, fwIoT) {
			bridge = b.Name
		}
	}
	if out := r.top.GW.Must("ip", "route", "get", behindUplink, "from", testbed.ClientAAddr, "iif", bridge); !strings.Contains(out, "dev wan0") {
		t.Fatalf("A's packets to %s must leave through the uplink: %s", behindUplink, out)
	}
	check := func(what string) {
		t.Helper()
		if got := tryAt(r.top.A, "tcp", behindUplink, 7050, 3); got != "ok" {
			t.Errorf("%s: A does not reach %s through the uplink with a matrix entry: %s", what, behindUplink, got)
		}
		ex, err := r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(testbed.ClientAAddr), Dst: behindUplink, Protocol: "tcp", Port: 7050})
		if err != nil {
			t.Fatal(err)
		}
		if ex.Access.Verdict != "allow" {
			t.Errorf("%s: explain = %+v, want allow", what, ex.Access)
		}
	}
	check("the default allowed sources")
	if _, err := r.apply(r.revision(func(c *model.Configuration) {
		withForwards(c)
		entry(c)
		c.Management.AllowedSources = &[]string{testbed.MgmtPeer + "/32"}
	}), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if out := r.top.GW.Must("nft", "list", "chain", "inet", "chaosgw", compiler.MgmtGuardChain); !strings.Contains(out, "ip daddr 192.168.56.0/24 return") {
		t.Fatalf("the guard does not open the management subnet for the entry:\n%s", out)
	}
	check("narrow allowed sources")
}
