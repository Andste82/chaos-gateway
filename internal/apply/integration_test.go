//go:build testbed

package apply_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// gw is the testbed gateway with the real executor (real tools, real kernel) pointed at its
// namespace, and the product's configuration applied through the apply package.
type gw struct {
	t   *testing.T
	top *testbed.Topology
	ex  *executor.Executor
	cfg *model.Configuration
	seq uint64
}

func newGateway(t *testing.T) *gw {
	t.Helper()
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false), testbed.WithGatewayBridges(false))
	ex, err := executor.New(executor.NewExecRunner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	raw, err := os.ReadFile("testdata/testbed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	return &gw{t: t, top: top, ex: ex, cfg: cfg}
}

func (g *gw) exec() apply.Exec { return apply.Local{E: g.ex} }
func (g *gw) ns() string       { return g.top.GW.Name }

func (g *gw) compile(mods ...func(*compiler.Input)) *compiler.Target {
	g.t.Helper()
	h, err := apply.ReadHost(context.Background(), g.exec(), g.ns())
	if err != nil {
		g.t.Fatal(err)
	}
	g.seq++
	in := compiler.Input{Config: g.cfg, Host: h, Generation: compiler.Generation{Revision: 1, Seq: g.seq}}
	for _, m := range mods {
		m(&in)
	}
	return compiler.Compile(in)
}

func (g *gw) apply(tg *compiler.Target) *apply.Result {
	g.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := apply.Apply(ctx, g.exec(), g.ns(), tg)
	if err != nil {
		g.t.Fatalf("apply: %v\n%s", err, g.dump())
	}
	return res
}

// dump shows the gateway's state for a failing test.
func (g *gw) dump() string {
	var b strings.Builder
	for _, c := range [][]string{{"ip", "-br", "link"}, {"ip", "-br", "addr"}, {"ip", "rule"}, {"ip", "route", "show", "table", "100"}, {"nft", "list", "ruleset"}} {
		out, _ := g.top.GW.Run(context.Background(), c[0], c[1:]...)
		b.WriteString("$ " + strings.Join(c, " ") + "\n" + out + "\n")
	}
	return b.String()
}

// listen starts a listener in ns on the port and prints what it receives.
func listen(top *testbed.Topology, ns *testbed.Namespace, proto string, port int) *testbed.Process {
	kind := "SOCK_DGRAM"
	if proto == "tcp" {
		kind = "SOCK_STREAM"
	}
	script := `import socket, sys
s = socket.socket(socket.AF_INET, socket.` + kind + `)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", ` + itoa(port) + `))
if "` + proto + `" == "tcp":
    s.listen(5)
    while True:
        c, a = s.accept()
        print("connect", a[0], flush=True)
        c.close()
else:
    while True:
        d, a = s.recvfrom(64)
        print("datagram", a[0], flush=True)
`
	return ns.Start("python3", "-c", script)
}

func itoa(n int) string { return strconv.Itoa(n) }

func sendUDP(t *testing.T, from *testbed.Namespace, dst string, port int) {
	t.Helper()
	_, _ = from.Run(context.Background(), "python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.sendto(b"x", ("`+dst+`", `+itoa(port)+`))
`)
}

// tcpConnects reports whether a TCP connection from `from` to dst:port is established within a second.
func tcpConnects(from *testbed.Namespace, dst string, port int) bool {
	_, err := from.Run(context.Background(), "python3", "-c", `import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(1.0)
try:
    s.connect(("`+dst+`", `+itoa(port)+`))
except Exception:
    sys.exit(1)
`)
	return err == nil
}

func TestFirstApplyOnARealKernel(t *testing.T) {
	g := newGateway(t)
	tg := g.compile()
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	res := g.apply(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v\n%s", res.Mismatches, g.dump())
	}
	gwns := g.top.GW
	if out := gwns.Must("ip", "-o", "link", "show", "dev", "lan0"); !strings.Contains(out, "master br-iot") {
		t.Errorf("lan0: %s", out)
	}
	if out := gwns.Must("ip", "-o", "addr", "show", "dev", "br-iot"); !strings.Contains(out, "10.10.0.1/24") {
		t.Errorf("br-iot: %s", out)
	}
	if out := gwns.Must("cat", "/proc/sys/net/ipv4/ip_forward"); out != "1" {
		t.Errorf("ip_forward %q", out)
	}
	if out := gwns.Must("cat", "/proc/sys/net/ipv6/conf/br-iot/accept_ra"); out != "0" {
		t.Errorf("accept_ra on br-iot: %q", out)
	}
	if out := gwns.Must("ethtool", "-k", "br-iot"); !strings.Contains(out, "generic-segmentation-offload: off") {
		t.Errorf("offloads on br-iot:\n%s", out)
	}
	// the management interface keeps its OS configuration
	if out := gwns.Must("ip", "route", "show", "default"); !strings.Contains(out, "via "+testbed.MgmtPeer+" dev mgmt0") {
		t.Errorf("the management default route changed: %s", out)
	}
	// the plan of the first apply is what was executed, and nothing is left to do afterwards
	p, err := apply.Preview(context.Background(), g.exec(), g.ns(), g.compile())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Errorf("preview after the apply: %v", p.Summary)
	}
}

// M4 test: a client reaches the server through the gateway.
func TestClientReachesTheServerThroughTheGateway(t *testing.T) {
	g := newGateway(t)
	g.apply(g.compile())
	for _, c := range []struct {
		name string
		from *testbed.Namespace
	}{{"A", g.top.A}, {"B", g.top.B}, {"C", g.top.C}} {
		r := testbed.MustPing(t, c.from, testbed.ServerAddr, 3, 200*time.Millisecond)
		if r.Loss() != 0 {
			t.Errorf("%s: loss %.0f%%\n%s", c.name, r.Loss()*100, g.dump())
		}
		for _, ttl := range r.TTLs {
			if ttl != 63 {
				t.Errorf("%s: ttl %d, want 63", c.name, ttl)
			}
		}
	}
	// NAT: the server sees the uplink address
	if got := udpSourceSeen(t, g.top.Server, testbed.ServerAddr, g.top.A); got != testbed.UplinkGateway {
		t.Errorf("the server saw %s, want %s", got, testbed.UplinkGateway)
	}
	// ... and the two test networks do not reach each other by default
	if r := testbed.MustPing(t, g.top.A, testbed.ClientCAddr, 2, 200*time.Millisecond); r.Received != 0 {
		t.Errorf("A reached C: the default matrix denies test network to test network")
	}
}

func udpSourceSeen(t *testing.T, to *testbed.Namespace, dst string, from *testbed.Namespace) string {
	t.Helper()
	l := to.Start("python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", 9000))
d, a = s.recvfrom(64)
print(a[0], flush=True)
`)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		sendUDP(t, from, dst, 9000)
		select {
		case <-l.Done():
			return strings.TrimSpace(l.Output())
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("nothing arrived: %q", l.Output())
	return ""
}

// M4 test: a test-network client reaches ICMP and test listeners on UDP 67 and 53 of the gateway,
// but not a listener on the UI/API port or SSH. The management network reaches all of them.
func TestGatewayProtection(t *testing.T) {
	g := newGateway(t)
	g.apply(g.compile())
	l67 := listen(g.top, g.top.GW, "udp", 67)
	l53 := listen(g.top, g.top.GW, "udp", 53)
	t53 := listen(g.top, g.top.GW, "tcp", 53)
	ui := listen(g.top, g.top.GW, "tcp", 443)
	ssh := listen(g.top, g.top.GW, "tcp", 22)
	time.Sleep(500 * time.Millisecond)

	if r := testbed.MustPing(t, g.top.A, testbed.LAN0Gateway, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Errorf("the gateway must answer ping from a test network: loss %.0f%%", r.Loss()*100)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && (!strings.Contains(l67.Output(), "datagram") || !strings.Contains(l53.Output(), "datagram")) {
		sendUDP(t, g.top.A, testbed.LAN0Gateway, 67)
		sendUDP(t, g.top.A, testbed.LAN0Gateway, 53)
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(l67.Output(), "datagram "+testbed.ClientAAddr) || !strings.Contains(l53.Output(), "datagram "+testbed.ClientAAddr) {
		t.Errorf("DHCP and DNS ports must be reachable: 67=%q 53=%q", l67.Output(), l53.Output())
	}
	// DHCP discovers are broadcasts: they must reach the server port, too
	_, _ = g.top.B.Run(context.Background(), "python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
s.sendto(b"x", ("255.255.255.255", 67))
`)
	time.Sleep(500 * time.Millisecond)
	if !strings.Contains(l67.Output(), "datagram "+testbed.ClientBAddr) {
		t.Errorf("a broadcast to UDP 67 did not arrive: %q", l67.Output())
	}
	if !tcpConnects(g.top.A, testbed.LAN0Gateway, 53) || !strings.Contains(t53.Output(), "connect") {
		t.Error("TCP 53 (DNS) must be reachable from a test network")
	}
	if tcpConnects(g.top.A, testbed.LAN0Gateway, 443) {
		t.Error("the UI/API port must not be reachable from a test network")
	}
	if tcpConnects(g.top.A, testbed.LAN0Gateway, 22) {
		t.Error("SSH must not be reachable from a test network")
	}
	if strings.Contains(ui.Output(), "connect") || strings.Contains(ssh.Output(), "connect") {
		t.Errorf("a connection reached UI %q or SSH %q", ui.Output(), ssh.Output())
	}
	// the management network always reaches the control plane (anti-lockout)
	if !tcpConnects(g.top.Mgmt, testbed.MgmtGateway, 443) || !tcpConnects(g.top.Mgmt, testbed.MgmtGateway, 22) {
		t.Errorf("the management network must reach UI and SSH\n%s", g.dump())
	}
	// the drops are counted
	out := g.top.GW.Must("nft", "list", "counter", "inet", "chaosgw", "input_drop")
	if strings.Contains(out, "packets 0 ") {
		t.Errorf("the input drops are not counted:\n%s", out)
	}
}

// M4 test: a management default route in the main table does not attract test traffic.
func TestAManagementDefaultRouteDoesNotAttractTestTraffic(t *testing.T) {
	g := newGateway(t)
	g.apply(g.compile())
	// the main table sends the Internet to the management router ...
	if out := g.top.GW.Must("ip", "route", "get", testbed.InternetAddr); !strings.Contains(out, "dev mgmt0") {
		t.Fatalf("main table: %s", out)
	}
	// ... but test traffic uses table 100 and leaves through the uplink
	if out := g.top.GW.Must("ip", "route", "get", testbed.InternetAddr, "from", testbed.ClientAAddr, "iif", "br-iot"); !strings.Contains(out, "dev wan0") || !strings.Contains(out, "via "+testbed.ServerAddr) {
		t.Errorf("test traffic: %s", out)
	}
	// the host behind the uplink router is reachable by a client; the management router does not have it
	if r := testbed.MustPing(t, g.top.A, testbed.InternetAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Errorf("A -> the Internet via the uplink: loss %.0f%%\n%s", r.Loss()*100, g.dump())
	}
}

func TestIPv6IsBlockedOnTestNetworks(t *testing.T) {
	g := newGateway(t)
	g.apply(g.compile())
	// the gateway's link-local address on the bridge: a client must not get an answer
	out := g.top.GW.Must("ip", "-6", "-o", "addr", "show", "dev", "br-iot", "scope", "link")
	f := strings.Fields(out)
	var ll string
	for i, w := range f {
		if w == "inet6" && i+1 < len(f) {
			ll = strings.Split(f[i+1], "/")[0]
		}
	}
	if ll == "" {
		t.Skipf("no link-local address on br-iot: %q", out)
	}
	if _, err := g.top.A.Run(context.Background(), "ping", "-6", "-c", "2", "-W", "1", ll+"%eth0"); err == nil {
		t.Error("the gateway answered IPv6 from a test network")
	}
}

func TestCountersSurviveEveryApplyOnARealKernel(t *testing.T) {
	g := newGateway(t)
	g.apply(g.compile())
	testbed.MustPing(t, g.top.A, testbed.ServerAddr, 5, 100*time.Millisecond)
	read := func() string {
		return g.top.GW.Must("nft", "list", "counter", "inet", "chaosgw", "nat_0b7c6a3e")
	}
	before := read()
	if strings.Contains(before, "packets 0 ") {
		t.Fatalf("no packet was counted:\n%s", before)
	}
	for i := 0; i < 3; i++ {
		g.apply(g.compile())
	}
	if after := read(); after != before {
		t.Errorf("the counter changed over applies without traffic:\n%s\n%s", before, after)
	}
	// and connectivity stays up through the applies
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 100*time.Millisecond); r.Loss() != 0 {
		t.Error("loss after the re-applies")
	}
}

// M4 test: an apply that changes a set's definition succeeds (hashed names) and the chain of a
// removed rule is gone.
func TestSetDefinitionChangeAndRemovedChainOnARealKernel(t *testing.T) {
	g := newGateway(t)
	oldDef := compiler.SetDef{Name: "dns_probe_aaaaaa", Type: "ipv4_addr", Dynamic: true}
	newDef := compiler.SetDef{Name: "dns_probe_bbbbbb", Type: "ipv6_addr", Dynamic: true}
	extra := func(tg *compiler.Target) {
		tg.Nft.Chains = append(tg.Nft.Chains, compiler.Chain{Name: "extra", Rules: []compiler.Rule{{Expr: []any{map[string]any{"counter": "extra_cnt"}, map[string]any{"accept": nil}}, Comment: "x"}}})
		tg.Nft.Counters = append(tg.Nft.Counters, "extra_cnt")
	}
	tg := g.compile(func(in *compiler.Input) { in.DynamicSets = []compiler.SetDef{oldDef} })
	extra(tg)
	g.apply(tg)
	g.top.GW.Must("nft", "add", "element", "inet", "chaosgw", "dns_probe_aaaaaa", "{ 192.0.2.7 }")
	// same compile, still the old set: the element survives
	tg = g.compile(func(in *compiler.Input) { in.DynamicSets = []compiler.SetDef{oldDef} })
	extra(tg)
	g.apply(tg)
	if out := g.top.GW.Must("nft", "list", "set", "inet", "chaosgw", "dns_probe_aaaaaa"); !strings.Contains(out, "192.0.2.7") {
		t.Errorf("the dynamic element did not survive an apply:\n%s", out)
	}
	// a new definition under a new name, and no extra chain any more
	g.apply(g.compile(func(in *compiler.Input) { in.DynamicSets = []compiler.SetDef{newDef} }))
	out := g.top.GW.Must("nft", "list", "table", "inet", "chaosgw")
	if strings.Contains(out, "dns_probe_aaaaaa") || !strings.Contains(out, "dns_probe_bbbbbb") {
		t.Errorf("sets after the definition change:\n%s", out)
	}
	if strings.Contains(out, "chain extra") || strings.Contains(out, "extra_cnt") {
		t.Errorf("the chain of the removed rule is still there:\n%s", out)
	}
}

// M4 test: verify detects a manipulated element.
func TestVerifyDetectsAManipulatedElementOnARealKernel(t *testing.T) {
	g := newGateway(t)
	tg := g.compile()
	g.apply(tg)
	var set string
	for _, s := range tg.Nft.Sets {
		if strings.HasPrefix(s.Name, "mgmt_src") {
			set = s.Name
		}
	}
	g.top.GW.Must("nft", "add", "element", "inet", "chaosgw", set, "{ 10.9.9.0/24 }")
	s, err := apply.ReadState(context.Background(), g.exec(), g.ns(), apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
	if err != nil {
		t.Fatal(err)
	}
	mm := apply.Verify(tg, s)
	if len(mm) == 0 || !strings.Contains(mm[0].String(), "set mgmt_src") {
		t.Fatalf("verify did not notice: %v", mm)
	}
	// the next apply repairs it
	g.apply(g.compile())
}

// M4 test: a changed uplink address keeps NAT working.
func TestAChangedUplinkAddressKeepsNATWorkingOnARealKernel(t *testing.T) {
	g := newGateway(t)
	g.apply(g.compile())
	if got := udpSourceSeen(t, g.top.Server, testbed.ServerAddr, g.top.A); got != testbed.UplinkGateway {
		t.Fatalf("before: the server saw %s", got)
	}
	// the old address goes first: a second address in the same prefix would be a secondary one and
	// vanish with the primary
	g.top.GW.Must("ip", "addr", "del", testbed.UplinkGateway+"/24", "dev", "wan0")
	g.top.GW.Must("ip", "addr", "add", "203.0.113.50/24", "dev", "wan0")
	tg := g.compile()
	if tg.Uplink.Addr.Addr().String() != "203.0.113.50" {
		t.Fatalf("uplink %+v", tg.Uplink)
	}
	g.apply(tg)
	if got := udpSourceSeen(t, g.top.Server, testbed.ServerAddr, g.top.A); got != "203.0.113.50" {
		t.Errorf("after: the server saw %s, want the new uplink address", got)
	}
}

// Docker (plan §3.4, spike S7): its FORWARD policy is DROP; only an accept in DOCKER-USER lets the
// test traffic through, and the product keeps that rule.
func TestDockerUserAcceptLetsTestTrafficThroughDockersForwardPolicy(t *testing.T) {
	g := newGateway(t)
	gwns := g.top.GW
	gwns.Must("iptables", "-w", "5", "-N", "DOCKER-USER")
	gwns.Must("iptables", "-w", "5", "-A", "DOCKER-USER", "-j", "RETURN")
	gwns.Must("iptables", "-w", "5", "-I", "FORWARD", "-j", "DOCKER-USER")
	gwns.Must("iptables", "-w", "5", "-P", "FORWARD", "DROP")
	g.apply(g.compile())
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Errorf("with the accept rules in DOCKER-USER the traffic passes: loss %.0f%%\n%s", r.Loss()*100, g.dump())
	}
	// remove the rules: Docker's policy drops again (this is what the rule is for)
	gwns.Must("iptables", "-w", "5", "-F", "DOCKER-USER")
	gwns.Must("iptables", "-w", "5", "-A", "DOCKER-USER", "-j", "RETURN")
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 2, 200*time.Millisecond); r.Received != 0 {
		t.Error("without the accept rules Docker's FORWARD policy must drop")
	}
	// verify notices, apply repairs
	tg := g.compile()
	s, _ := apply.ReadState(context.Background(), g.exec(), g.ns(), apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
	found := false
	for _, m := range apply.Verify(tg, s) {
		found = found || m.Subsystem == "docker"
	}
	if !found {
		t.Error("verify did not notice the missing DOCKER-USER rules")
	}
	g.apply(tg)
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Error("the apply did not repair DOCKER-USER")
	}
}

func TestRemovingANetworkOnARealKernel(t *testing.T) {
	g := newGateway(t)
	g.apply(g.compile())
	delete(*g.cfg.Networks, "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32")
	g.apply(g.compile())
	if out, err := g.top.GW.Run(context.Background(), "ip", "link", "show", "dev", "br-lab"); err == nil {
		t.Errorf("the bridge of the removed network exists: %s", out)
	}
	if out := g.top.GW.Must("ip", "-o", "link", "show", "dev", "lan1"); strings.Contains(out, "master") {
		t.Errorf("lan1 is still a bridge port: %s", out)
	}
	if r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); r.Loss() != 0 {
		t.Error("the remaining network lost connectivity")
	}
	if r := testbed.MustPing(t, g.top.C, testbed.LAN1Gateway, 2, 200*time.Millisecond); r.Received != 0 {
		t.Error("the removed network is still served")
	}
}
