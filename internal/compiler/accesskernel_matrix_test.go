//go:build testbed

package compiler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The remaining acceptance tests of M9 that need only the compiler's ruleset on a real kernel (plan
// M9 "Tests"): the verdict of each action on the wire, the per-rule counters, the anti-lockout rule
// against a rule that really selects the management host, and IPv6. They use the namespaces of
// accesskernel_test.go. The functional assertions hold under emulation; nothing here is statistical.

// sniffScript prints every ICMP message and every TCP segment that reaches the namespace, so that a test
// can tell a TCP reset from an ICMP port unreachable (a client reports both as "connection refused").
const sniffScript = `
import select, socket
icmp = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP)
tcp = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_TCP)
print("ready", flush=True)
while True:
    r, _, _ = select.select([icmp, tcp], [], [])
    for s in r:
        d, a = s.recvfrom(2048)
        ihl = (d[0] & 15) * 4
        if s is icmp:
            print("icmp", d[ihl], d[ihl + 1], flush=True)
        else:
            f = d[ihl + 13]
            print("tcp", "RST" if f & 4 else ("SYNACK" if f & 0x12 == 0x12 else "other"), a[0], flush=True)
`

// udpBurstScript sends n datagrams from one socket to each port given and prints done.
const udpBurstScript = `
import socket, sys, time
ip, n = sys.argv[1], int(sys.argv[2])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for port in sys.argv[3:]:
    for _ in range(n):
        s.sendto(b"x", (ip, int(port)))
        time.sleep(0.05)
print("done", flush=True)
`

// The verdicts, seen on the wire. reject answers a TCP SYN and a UDP datagram with an ICMP port
// unreachable (P2-M9-03) and never with a TCP segment; reset answers with a TCP RST from the address
// the device meant and with no ICMP; drop answers with nothing. A client reports both refusals alike,
// so this is the one place where the difference is checked.
func TestRejectResetAndDropAnswerWithTheirOwnPacketsOnTheWire(t *testing.T) {
	b := newAccessBed(t)
	b.apply()
	sniff := func() *testbed.Process {
		p := b.dev.Start("python3", "-c", sniffScript)
		waitOutput(t, p, "ready")
		return p
	}

	first := true
	run := func(name, rule, proto string, port int, want string, icmp, rst bool) {
		t.Helper()
		if first {
			b.w.addConfigRule(ruleA, rule)
			first = false
		} else {
			b.w.cfg = b.replaceRule(ruleA, rule)
		}
		b.apply()
		s := sniff()
		defer s.Stop()
		b.expect(b.dev, proto, "203.0.113.10", port, want)
		time.Sleep(500 * time.Millisecond)
		out := s.Output()
		if got := strings.Contains(out, "icmp 3 3"); got != icmp {
			t.Errorf("%s: an ICMP port unreachable on the wire = %v, want %v\n%s", name, got, icmp, out)
		}
		if got := strings.Contains(out, "tcp RST 203.0.113.10"); got != rst {
			t.Errorf("%s: a TCP reset from the server's address on the wire = %v, want %v\n%s", name, got, rst, out)
		}
		if !icmp && !rst && strings.Contains(out, "icmp") {
			t.Errorf("%s: ICMP on the wire where there should be none\n%s", name, out)
		}
	}
	run("reject tcp", `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: reject}`, "tcp", 8883, "refused", true, false)
	run("reset tcp", `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: reset}`, "tcp", 8883, "refused", false, true)
	run("drop tcp", `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: drop}`, "tcp", 8883, "timeout", false, false)
	run("reject udp", `{name: no-echo, source: {device: esp32-42}, protocol: udp, ports: [5353], action: reject}`, "udp", 5353, "refused", true, false)
	run("drop udp", `{name: no-echo, source: {device: esp32-42}, protocol: udp, ports: [5353], action: drop}`, "udp", 5353, "timeout", false, false)
}

// Every rule has a counter of its own that counts the packets the rule decided and nothing else: two
// drop rules count their own datagrams exactly, the anti-lockout rule's counter and the counter of a
// rule that does not match stay where they were, and an allow rule counts the first packet of a
// connection and not the established traffic that runs through it afterwards (P2-M9-07).
func TestEachRuleHasACounterOfItsOwnThatCountsWhatItDecided(t *testing.T) {
	b := newAccessBed(t)
	b.w.addConfigRule(ruleA, `{name: drop-a, source: {device: esp32-42}, protocol: udp, ports: [5353], action: drop}`)
	b.w.addConfigRule(ruleB, `{name: drop-b, source: {device: esp32-42}, protocol: udp, ports: [5354], action: drop}`)
	b.w.addConfigRule(ruleC, `{name: drop-other, source: {device: lab-host}, protocol: udp, ports: [5353], action: drop}`)
	b.w.addConfigRule(ruleD, `{name: allow-ssh, source: {device: esp32-42}, protocol: tcp, ports: [22], action: allow}`)
	tg := b.apply()
	byName := map[string]string{}
	for _, r := range tg.Access.Rules {
		byName[r.Name] = r.Counter
	}
	if len(byName) != 4 {
		t.Fatalf("four rules, four counters: %+v", tg.Access.Rules)
	}
	seen := map[string]bool{}
	for _, c := range byName {
		if seen[c] {
			t.Fatalf("two rules share a counter: %v", byName)
		}
		seen[c] = true
	}
	before := b.counter(AntiLockoutCounter)

	// 5 datagrams to 5353 and 3 to 5354 from one socket: each is the first packet of a connection that
	// is never established, so each is judged, and counted, by the rule that drops it
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, c := range []struct {
		port string
		n    string
	}{{"5353", "5"}, {"5354", "3"}} {
		if out, err := b.dev.Run(ctx, "python3", "-c", udpBurstScript, "203.0.113.10", c.n, c.port); err != nil || !strings.Contains(out, "done") {
			t.Fatalf("%v %q", err, out)
		}
	}
	for name, want := range map[string]int{"drop-a": 5, "drop-b": 3, "drop-other": 0, "allow-ssh": 0} {
		if got := b.counter(byName[name]); got != want {
			t.Errorf("the counter of %s = %d, want %d", name, got, want)
		}
	}

	// an allowed connection: its first packet is counted by the allow rule once; while the connection
	// runs, the established packets are accepted before the rules and the counter does not grow
	hold := b.dev.Start("python3", "-c", holdScript, "203.0.113.10", "22")
	waitOutput(t, hold, "ok")
	first := b.counter(byName["allow-ssh"])
	if first != 1 {
		t.Errorf("the allow rule counted %d packets for the first packet of a connection, want 1", first)
	}
	time.Sleep(time.Second)
	if n := strings.Count(hold.Output(), "ok"); n < 3 {
		t.Fatalf("the connection does not run: %s", hold.Output())
	}
	if got := b.counter(byName["allow-ssh"]); got != first {
		t.Errorf("the allow rule's counter grew from %d to %d while an established connection ran", first, got)
	}
	hold.Stop()
	if got := b.counter(AntiLockoutCounter); got != before {
		t.Errorf("the anti-lockout counter moved from %d to %d without management traffic", before, got)
	}
}

// The anti-lockout rule against rules that really select the management host (a device whose address is
// the host's): UDP towards the gateway shows that the rules are in force (dropped, rejected), while the
// SSH and UI ports stay reachable and an established SSH connection is not reset by a cut window.
func TestTheAntiLockoutRuleHoldsAgainstRulesThatSelectTheManagementHost(t *testing.T) {
	b := newAccessBed(t)
	b.w.setAddrs(map[string][]string{devESP42: {"10.10.0.42"}, devESP43: {"10.10.0.43"}, devLab: {"192.168.56.2"}})
	b.apply()
	b.expect(b.m, "udp", "192.168.56.1", 53, "ok") // without rules the host reaches the gateway's DNS responder
	ssh := b.m.Start("python3", "-c", holdScript, "192.168.56.1", "22")
	waitOutput(t, ssh, "ok")

	b.w.addConfigRule(ruleA, `{name: drop-host, source: {device: lab-host}, action: drop, cut_existing: true}`)
	b.w.addConfigRule(ruleB, `{name: reset-ui, source: {device: lab-host}, protocol: tcp, ports: [22, 443], action: reset, cut_existing: true}`)
	b.w.ruleOverlay(`{target: {device: lab-host}, rule: {action: reject}}`, time.Hour)
	b.w.ruleOverlay(`{target: {device: lab-host}, rule: {action: drop, protocol: tcp, ports: [22, 443], cut_existing: true}}`, 2*time.Hour)
	tg := b.apply()

	// the rules select the host: UDP is refused (the newest rule that matches is the reject overlay)
	b.expect(b.m, "udp", "192.168.56.1", 53, "refused")
	// and still: the control plane
	b.expect(b.m, "tcp", "192.168.56.1", 22, "ok")
	b.expect(b.m, "tcp", "192.168.56.1", 443, "ok")
	if b.counter(AntiLockoutCounter) == 0 {
		t.Error("the anti-lockout rule counted nothing")
	}
	// the reject overlay decided the UDP datagram; the control-plane connections reached no rule
	if b.counter(tg.Access.Rules[1].Counter) == 0 {
		t.Error("the reject overlay, which decided the host's UDP, counted nothing")
	}
	if n := b.counter(tg.Access.Rules[0].Counter); n != 0 {
		t.Errorf("the overlay that drops SSH and the UI counted %d packets: the control plane reached it", n)
	}

	// a cut window with every rule in it: the established SSH connection of the host is not reset
	var keys []string
	for _, r := range tg.Access.Rules {
		keys = append(keys, r.Key)
	}
	tx, err := tg.Access.CutTransaction(keys)
	if err != nil {
		t.Fatal(err)
	}
	b.gw.MustStdin(string(tx), "nft", "-j", "-f", "-")
	time.Sleep(1500 * time.Millisecond)
	if out := ssh.Output(); strings.Contains(out, "Error") || strings.Contains(out, "bad") {
		t.Errorf("the window cut the management host's SSH connection: %s", out)
	}
	b.expect(b.m, "tcp", "192.168.56.1", 22, "ok") // a new one gets through the open window too
	closing, err := tg.Access.CutTransaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	b.gw.MustStdin(string(closing), "nft", "-j", "-f", "-")
	ssh.Stop()
}

// tryScript6 is tryScript for TCP over IPv6.
const tryScript6 = `
import socket, sys
ip, port, t = sys.argv[1], int(sys.argv[2]), float(sys.argv[3])
try:
    s = socket.socket(socket.AF_INET6); s.settimeout(t); s.connect((ip, port)); s.sendall(b"hi"); r = s.recv(10)
    print("ok" if r == b"hi" else "bad")
except ConnectionRefusedError: print("refused")
except ConnectionResetError: print("reset")
except socket.timeout: print("timeout")
`

// serverScript6 echoes on tcp 8883 over IPv6.
const serverScript6 = `
import socket, threading
s = socket.socket(socket.AF_INET6); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
s.bind(("::", 8883)); s.listen(50)
while True:
    c, _ = s.accept()
    def h(c):
        try:
            while True:
                d = c.recv(100)
                if not d: break
                c.sendall(d)
        except Exception: pass
    threading.Thread(target=h, args=(c,), daemon=True).start()
`

// IPv6 (plan §2.2, D7): test networks are IPv4-only in V1, forwarded IPv6 is dropped before the rules,
// and no rule can open it: an allow rule that selects everything leaves IPv6 blocked, the rules never
// see an IPv6 packet (their counters count the IPv4 connection and only it), and the ruleset with
// IPv6 selectors absent loads and runs next to live IPv6 traffic. The first step shows that the path
// is there: without a ruleset the IPv6 connection works.
func TestForwardedIPv6StaysBlockedWhateverTheRulesAllow(t *testing.T) {
	b := newAccessBed(t)
	b.srv.Start("python3", "-c", serverScript6)
	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if out, _ := b.srv.Run(context.Background(), "ss", "-ltn", "sport = :8883"); strings.Count(out, "LISTEN") >= 2 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("the IPv6 server does not listen: %s", out)
		}
	}
	b.gw.Sysctl("net.ipv6.conf.all.forwarding", "1")
	b.gw.Must("ip", "-6", "addr", "add", "fd00:10::1/64", "dev", "br-iot", "nodad")
	b.dev.Must("ip", "-6", "addr", "add", "fd00:10::42/64", "dev", "eth0", "nodad")
	b.dev.Must("ip", "-6", "route", "add", "default", "via", "fd00:10::1")
	b.gw.Must("ip", "-6", "addr", "add", "fd00:20::1/64", "dev", "wan0", "nodad")
	b.srv.Must("ip", "-6", "addr", "add", "fd00:20::10/64", "dev", "eth0", "nodad")
	b.srv.Must("ip", "-6", "route", "add", "fd00:10::/64", "via", "fd00:20::1")
	// permanent neighbors: the ruleset closes the gateway's input to neighbor discovery on a test
	// network, and what is under test is the forward path
	neigh := func(on *testbed.Namespace, addr string, peer *testbed.Namespace, peerIf, dev string) {
		on.Must("ip", "-6", "neigh", "replace", addr, "lladdr", strings.TrimSpace(peer.MAC(peerIf)), "dev", dev, "nud", "permanent")
	}
	neigh(b.dev, "fd00:10::1", b.gw, "br-iot", "eth0")
	neigh(b.gw, "fd00:10::42", b.dev, "eth0", "br-iot")
	neigh(b.gw, "fd00:20::10", b.srv, "eth0", "wan0")
	neigh(b.srv, "fd00:20::1", b.gw, "wan0", "eth0")

	try6 := func() string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		out, err := b.dev.Run(ctx, "python3", "-c", tryScript6, "fd00:20::10", "8883", "2")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	if got := try6(); got != "ok" {
		t.Fatalf("the IPv6 path does not work without a ruleset: %s", got)
	}

	b.w.addConfigRule(ruleA, `{name: allow-all, source: {global: true}, action: allow}`)
	b.w.ruleOverlay(`{target: {network: IoT}, rule: {action: allow}}`, time.Hour)
	tg := b.apply()
	if got := try6(); got != "timeout" {
		t.Errorf("an allow rule opened forwarded IPv6: %s", got)
	}
	if b.counter("ipv6_drop") == 0 {
		t.Error("the IPv6 block counted nothing")
	}
	for _, r := range tg.Access.Rules {
		if n := b.counter(r.Counter); n != 0 {
			t.Errorf("rule %s counted %d packets of IPv6 traffic", r.Key, n)
		}
	}
	// the same rules judge IPv4 as ever
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "ok")
	if n := b.counter(tg.Access.Rules[0].Counter); n == 0 {
		t.Error("the allow overlay counted nothing for the IPv4 connection")
	}
	if got := try6(); got != "timeout" {
		t.Errorf("IPv6 after an IPv4 connection: %s", got)
	}
}

// The cut window runs in front of the established accept, so it has to skip by itself what the hooks
// let no rule judge (plan §2.2: switched traffic is "never ours to impair or drop"; the gateway's own
// connections). On a bridged bed with br_netfilter, an established TCP connection between two devices of
// one network and a connection the gateway holds to its own bridge address (loopback, original source in
// the network the rule selects) both survive a window of a rule that cuts that whole network; a
// connection of the device through the gateway is reset by the same window, so the window did open.
func TestACutWindowLeavesSwitchedAndGatewayOriginatedTrafficAlone(t *testing.T) {
	b := newBridgedAccessBed(t)
	b.apply()
	switched := b.dev.Start("python3", "-c", holdScript, "10.10.0.43", "8883")
	local := b.gw.Start("python3", "-c", holdScript, "10.10.0.1", "443")
	routed := b.dev.Start("python3", "-c", holdScript, "203.0.113.10", "8883")
	for _, p := range []*testbed.Process{switched, local, routed} {
		waitOutput(t, p, "ok")
	}

	b.w.addConfigRule(ruleA, `{name: cut-iot, source: {network: IoT}, protocol: tcp, action: drop, cut_existing: true}`)
	tg := b.apply()
	if !tg.Access.HasCuts() {
		t.Fatal("the rule must be able to cut")
	}
	// the bridge must really show the switched traffic to the forward hook for this test to mean
	// anything: a counter in front of everything proves that the connection passes the hook
	b.gw.MustStdin(`add counter inet chaosgw probe_sw
add chain inet chaosgw probe_switched { type filter hook forward priority -10; }
add rule inet chaosgw probe_switched iifname "br-iot" oifname "br-iot" counter name "probe_sw"
`, "nft", "-f", "-")
	tx, err := tg.Access.CutTransaction([]string{"config:" + ruleA})
	if err != nil {
		t.Fatal(err)
	}
	b.gw.MustStdin(string(tx), "nft", "-j", "-f", "-")
	for i := 0; i < 50 && b.counter("probe_sw") == 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if n := b.counter("probe_sw"); n == 0 {
		t.Fatal("the switched connection does not pass the forward hook: br_netfilter is not on, the test proves nothing")
	}
	waitOutput(t, routed, "ConnectionResetError") // the window is open and resets what it is meant to
	time.Sleep(time.Second)
	closing, err := tg.Access.CutTransaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	b.gw.MustStdin(string(closing), "nft", "-j", "-f", "-")
	for name, p := range map[string]*testbed.Process{"the switched connection of two devices": switched, "the gateway's own connection over loopback": local} {
		if out := p.Output(); strings.Contains(out, "Error") || strings.Contains(out, "bad") || strings.Contains(out, "timeout") {
			t.Errorf("%s was reset by the cut window: %s", name, out)
		}
	}
	switched.Stop()
	local.Stop()
	routed.Stop()
}
