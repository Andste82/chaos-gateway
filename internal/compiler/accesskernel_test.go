//go:build testbed

package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/preflight"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// This file runs the nftables ruleset of the access rules on the real kernel with real traffic
// (spike S3's behavior matrix at the level of the compiler's output, plan M9): a device behind
// `br-iot`, a server behind `wan0`, a management host behind `mgmt0`, all in namespaces, and a
// gateway namespace that gets the transaction the compiler produced. No executor and no engine are
// involved; the engine's own tests (apply, cut windows with conntrack deletion, DNS through the
// service namespace) stand on top of these.

// accessBed is the namespaces of the behavior tests.
type accessBed struct {
	t               *testing.T
	gw, dev, srv, m *testbed.Namespace
	// dev2 is a second device on the same network; only the bridged bed has it
	dev2    *testbed.Namespace
	w       *faultWorld
	current *linux.Ruleset
}

// newAccessBed builds the topology of the compiler's fixture: IoT (10.10.0.0/24) on br-iot with the
// device esp32-42 at 10.10.0.42, the uplink wan0 (203.0.113.1/24) with the server at
// 203.0.113.10, the management interface mgmt0 (192.168.56.1/24) with a host at 192.168.56.2. The
// gateway's ports carry the names the compiler uses, so the compiled ruleset sees what it expects.
func newAccessBed(t *testing.T) *accessBed { return newAccessBedWith(t, false) }

// newBridgedAccessBed is the bed with a real bridge br-iot on the gateway and a second device on it
// (10.10.0.43), with br_netfilter on: traffic between the two devices is switched, and still passes
// the forward hook with the bridge as input and output interface, as it does on a Docker host.
func newBridgedAccessBed(t *testing.T) *accessBed { return newAccessBedWith(t, true) }

func newAccessBedWith(t *testing.T, bridged bool) *accessBed {
	t.Helper()
	bed := testbed.New(t)
	b := &accessBed{t: t, gw: bed.Add("gw"), dev: bed.Add("dev"), srv: bed.Add("srv"), m: bed.Add("mgmt"), w: accessWorld(t)}
	if bridged {
		// the switched traffic shows in the forward hook only with br_netfilter, which the
		// product does not need and the testbed does not load by itself
		if err := preflight.Load(context.Background(), []preflight.Module{{Name: "br_netfilter"}}); err != nil {
			t.Fatalf("the bridged bed needs br_netfilter: %v", err)
		}
		b.dev2 = bed.Add("dev2")
		b.gw.Bridge("br-iot", "10.10.0.1/24")
		bed.Link(testbed.End{NS: b.gw, If: "p-dev", Master: "br-iot"}, testbed.End{NS: b.dev, If: "eth0", Addr: "10.10.0.42/24"})
		bed.Link(testbed.End{NS: b.gw, If: "p-dev2", Master: "br-iot"}, testbed.End{NS: b.dev2, If: "eth0", Addr: "10.10.0.43/24"})
		b.gw.Sysctl("net.bridge.bridge-nf-call-iptables", "1")
		b.dev2.Route("default", "via", "10.10.0.1")
		b.dev2.Start("python3", "-c", serverScript)
	} else {
		bed.Link(testbed.End{NS: b.gw, If: "br-iot", Addr: "10.10.0.1/24"}, testbed.End{NS: b.dev, If: "eth0", Addr: "10.10.0.42/24"})
	}
	bed.Link(testbed.End{NS: b.gw, If: "wan0", Addr: "203.0.113.1/24"}, testbed.End{NS: b.srv, If: "eth0", Addr: "203.0.113.10/24"})
	bed.Link(testbed.End{NS: b.gw, If: "mgmt0", Addr: "192.168.56.1/24"}, testbed.End{NS: b.m, If: "eth0", Addr: "192.168.56.2/24"})
	b.dev.Route("default", "via", "10.10.0.1")
	b.srv.Route("10.10.0.0/24", "via", "203.0.113.1")
	b.m.Route("default", "via", "192.168.56.1")
	b.gw.Sysctl("net.ipv4.ip_forward", "1")
	b.srv.Start("python3", "-c", serverScript)
	b.m.Start("python3", "-c", serverScript)
	b.gw.Start("python3", "-c", gatewayScript)
	b.waitListening(b.srv, "8883", "22", "7050")
	b.waitListening(b.m, "22")
	b.waitListening(b.gw, "443", "22")
	if bridged {
		b.waitListening(b.dev2, "8883")
	}
	return b
}

// serverScript listens on tcp 8883, 22 and 7050 (echo) and on udp 5353 (echo).
const serverScript = `
import socket, threading, time
def tcp(port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(50)
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
for p in (8883, 22, 7050):
    threading.Thread(target=tcp, args=(p,), daemon=True).start()
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.bind(("0.0.0.0", 5353))
while True:
    d, a = u.recvfrom(100); u.sendto(d, a)
`

// gatewayScript is the gateway's own services: the control plane (tcp 443 and 22) and a DNS
// responder (udp 53).
const gatewayScript = `
import socket, threading
def tcp(port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(50)
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
for p in (443, 22):
    threading.Thread(target=tcp, args=(p,), daemon=True).start()
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.bind(("0.0.0.0", 53))
while True:
    d, a = u.recvfrom(100); u.sendto(d, a)
`

// tryScript makes one attempt: tcp or udp, address, port, timeout; it prints ok, refused, reset or
// timeout.
const tryScript = `
import errno, socket, sys, time
proto, ip, port, t = sys.argv[1], sys.argv[2], int(sys.argv[3]), float(sys.argv[4])
try:
    if proto == "tcp":
        s = socket.socket(); s.settimeout(t); s.connect((ip, port)); s.sendall(b"hi"); r = s.recv(10)
    else:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(t); s.connect((ip, port)); s.send(b"hi"); r = s.recv(10)
    print("ok" if r == b"hi" else "bad")
except ConnectionRefusedError: print("refused")
except ConnectionResetError: print("reset")
except socket.timeout: print("timeout")
except OSError as e: print("error-" + errno.errorcode.get(e.errno, str(e.errno)))
`

// holdScript keeps one TCP connection busy: a byte every 100 ms, one line per answer, until the
// connection breaks.
const holdScript = `
import socket, sys, time
s = socket.socket(); s.settimeout(3); s.connect((sys.argv[1], int(sys.argv[2])))
print("connected", flush=True)
while True:
    try:
        s.sendall(b"x"); r = s.recv(10)
        print("ok" if r == b"x" else "bad", flush=True)
    except Exception as e:
        print(type(e).__name__, flush=True); break
    time.sleep(0.1)
`

func (b *accessBed) waitListening(ns *testbed.Namespace, ports ...string) {
	b.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for _, p := range ports {
		for {
			out, _ := ns.Run(context.Background(), "ss", "-ltn", "sport = :"+p)
			if strings.Contains(out, ":"+p) {
				break
			}
			if time.Now().After(deadline) {
				b.t.Fatalf("nothing listens on %s in %s: %s", p, ns.Short, out)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// apply compiles the fixture and applies the transaction to the gateway, replacing what is there.
func (b *accessBed) apply() *Target {
	b.t.Helper()
	tg := b.w.compile(nil)
	if tg.HasErrors() {
		b.t.Fatalf("problems: %+v", tg.Problems)
	}
	tx, err := tg.Nft.Transaction(b.current)
	if err != nil {
		b.t.Fatal(err)
	}
	b.gw.MustStdin(string(tx), "nft", "-j", "-f", "-")
	out := b.gw.Must("nft", "-j", "list", "ruleset")
	if b.current, err = linux.ParseNft([]byte(out)); err != nil {
		b.t.Fatal(err)
	}
	return tg
}

// try makes one attempt from ns.
func (b *accessBed) try(ns *testbed.Namespace, proto, ip string, port int) string {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := ns.Run(ctx, "python3", "-c", tryScript, proto, ip, fmt.Sprint(port), "2")
	if err != nil {
		b.t.Fatalf("%v", err)
	}
	return out
}

func (b *accessBed) expect(ns *testbed.Namespace, proto, ip string, port int, want string) {
	b.t.Helper()
	if got := b.try(ns, proto, ip, port); got != want {
		b.t.Errorf("%s %s:%d from %s: %s, want %s\n%s", proto, ip, port, ns.Short, got, want, b.gw.Must("nft", "list", "chain", "inet", "chaosgw", "access_forward"))
	}
}

// counter reads the packets of a named counter of the table.
func (b *accessBed) counter(name string) int {
	b.t.Helper()
	out := b.gw.Must("nft", "-j", "list", "counter", "inet", "chaosgw", name)
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Packets int `json:"packets"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		b.t.Fatal(err)
	}
	for _, o := range doc.Nftables {
		if o.Counter != nil {
			return o.Counter.Packets
		}
	}
	b.t.Fatalf("no counter %s", name)
	return 0
}

// S3 C0-C3 on the compiled rules: without a rule the connection works; an established connection
// continues when a drop rule arrives (C2); new connections are dropped (C1), refused (reject) or
// reset (C3); the rule's counter counts them and nothing else.
func TestAccessRulesOnTheRealKernelChangeNewConnectionsOnly(t *testing.T) {
	b := newAccessBed(t)
	b.apply()
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "ok")

	hold := b.dev.Start("python3", "-c", holdScript, "203.0.113.10", "8883")
	waitOutput(t, hold, "ok")

	b.w.addConfigRule(ruleA, `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: drop}`)
	tg := b.apply()
	counter := tg.Access.Rules[0].Counter

	// C1: a new connection hangs and is counted; the other ports are not touched
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "timeout")
	if n := b.counter(counter); n == 0 {
		t.Error("the counter of the dropping rule counts nothing")
	}
	b.expect(b.dev, "tcp", "203.0.113.10", 22, "ok")
	// C2: the connection from before the rule is established, so it continues
	time.Sleep(time.Second)
	if out := hold.Output(); strings.Contains(out, "Error") || strings.Contains(out, "timeout") {
		t.Errorf("the established connection broke: %s", out)
	}
	hold.Stop()

	// reject on tcp: the device is told at once (ICMP port unreachable)
	b.w.cfg = b.replaceRule(ruleA, `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: reject}`)
	b.apply()
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "refused")
	// C3: tcp reset
	b.w.cfg = b.replaceRule(ruleA, `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: reset}`)
	b.apply()
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "refused")
	// udp reject: ICMP port unreachable reaches the device as a refusal
	b.w.cfg = b.replaceRule(ruleA, `{name: no-echo, source: {device: esp32-42}, protocol: udp, ports: [5353], action: reject}`)
	b.apply()
	b.expect(b.dev, "udp", "203.0.113.10", 5353, "refused")
	b.w.cfg = b.replaceRule(ruleA, `{name: no-echo, source: {device: esp32-42}, protocol: udp, ports: [5353], action: drop}`)
	b.apply()
	b.expect(b.dev, "udp", "203.0.113.10", 5353, "timeout")
	// and without the rule everything works again
	b.w.cfg = b.removeRule(ruleA)
	b.apply()
	b.expect(b.dev, "udp", "203.0.113.10", 5353, "ok")
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "ok")
}

// Rules are evaluated in order, first match wins; overlay rules stand in front of the configured
// ones; an allow rule is an exception to the access matrix.
func TestAccessRuleOrderAndOverlaysOnTheRealKernel(t *testing.T) {
	b := newAccessBed(t)
	b.w.addConfigRule(ruleA, `{name: allow-22, source: {network: IoT}, protocol: tcp, ports: [22], action: allow}`)
	b.w.addConfigRule(ruleB, `{name: drop-iot, source: {network: IoT}, destination: {uplink: true}, action: drop}`)
	b.apply()
	b.expect(b.dev, "tcp", "203.0.113.10", 22, "ok")        // the first rule allows
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "timeout") // the second drops
	b.expect(b.dev, "udp", "203.0.113.10", 5353, "timeout")

	// an overlay in front of the configuration: reject 22 for the device
	b.w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: reject, protocol: tcp, ports: [22]}}`, time.Hour)
	tg := b.apply()
	b.expect(b.dev, "tcp", "203.0.113.10", 22, "refused")
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "timeout")
	if tg.Access.Rules[0].Layer != "overlay" {
		t.Fatalf("order: %+v", tg.Access.Rules)
	}
	// a newer overlay that allows it wins over the older one
	b.w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: allow, protocol: tcp, ports: [22], destination: {uplink: true}}}`, 2*time.Hour)
	b.apply()
	b.expect(b.dev, "tcp", "203.0.113.10", 22, "ok")
	// the older overlay rejects it everywhere else (its destination is not the uplink)
	b.expect(b.dev, "tcp", "192.168.56.2", 22, "refused")
}

// The gateway's own services: a rule drops DNS to the gateway before the gateway answers it, an
// allow rule opens a port the gateway's protection closes (but not the UI port), and the control
// plane stays reachable for the management sources whatever the rules say (the anti-lockout rule).
func TestAccessRulesInInputAndTheAntiLockoutRuleOnTheRealKernel(t *testing.T) {
	b := newAccessBed(t)
	b.apply()
	b.expect(b.dev, "udp", "10.10.0.1", 53, "ok") // the gateway answers DNS on a test network
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
	b.expect(b.dev, "tcp", "10.10.0.1", 22, "timeout")
	b.expect(b.m, "tcp", "192.168.56.1", 443, "ok")

	b.w.addConfigRule(ruleA, `{name: no-dns, source: {device: esp32-42}, protocol: udp, ports: [53], action: drop}`)
	tg := b.apply()
	b.expect(b.dev, "udp", "10.10.0.1", 53, "timeout")
	if b.counter(tg.Access.Rules[0].Counter) == 0 {
		t.Error("the DNS rule counts nothing")
	}

	// everything dropped, everybody: the management host still reaches SSH and the UI, a device
	// reaches nothing, and an allow rule does not open the gateway's UI port to a device
	b.w.addConfigRule(ruleB, `{name: allow-ui, source: {network: IoT}, protocol: tcp, ports: [443, 22], action: allow}`)
	b.w.addConfigRule(ruleC, `{name: drop-all, source: {global: true}, action: drop}`)
	b.w.ruleOverlay(`{target: {global: true}, rule: {action: reject}}`, time.Hour)
	b.apply()
	b.expect(b.m, "tcp", "192.168.56.1", 443, "ok")
	b.expect(b.m, "tcp", "192.168.56.1", 22, "ok")
	if b.counter(AntiLockoutCounter) == 0 {
		t.Error("the anti-lockout rule counts nothing")
	}
	b.expect(b.dev, "tcp", "10.10.0.1", 22, "refused") // the overlay rejects it before anything else
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "refused")
	// the UI port is dropped for everybody but the management sources before the rules are asked:
	// no rule, not even a reject, changes what a device sees there
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
	// the same gateway without the overlay: the allow rule accepts SSH for the device, which the
	// protection would drop, and still not the UI port
	b.w.overlays = nil
	b.apply()
	b.expect(b.dev, "tcp", "10.10.0.1", 22, "ok")
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
}

// An allow rule in the input path accepts (decided by the maintainer on 2026-10-11): it opens the
// gateway's own ports that the protection of the test networks closes, for the rule's selector and
// nothing else. The two rules in front of the access rules stay: the UI/API port is closed to a
// test network whatever a rule says, and the management sources reach SSH and the UI/API whatever
// the rules say.
func TestAnAllowRuleOpensTheGatewaysClosedPortsButNeverTheUIAndNeverLocksOutManagement(t *testing.T) {
	b := newAccessBed(t)
	tg := b.apply()
	// explain (domain.World.AccessDecision) and the kernel agree on every probe: an allowed packet
	// reaches the gateway's stack (answered, or refused by a closed port), a dropped one hangs
	agree := func(stage string) {
		t.Helper()
		world, err := domain.NewWorld(b.w.cfg, b.w.overlays)
		if err != nil {
			t.Fatal(err)
		}
		facts := domain.AccessFacts{Gateway: []netip.Addr{netip.MustParseAddr("10.10.0.1")},
			Management: tg.Management.Sources, UIPort: tg.Management.UIPort}
		for _, p := range []struct {
			ns         *testbed.Namespace
			src, proto string
			port       int
		}{
			{b.dev, "10.10.0.42", "tcp", 22}, {b.dev, "10.10.0.42", "tcp", 8080}, {b.dev, "10.10.0.42", "tcp", tg.Management.UIPort},
			{b.dev, "10.10.0.42", "udp", 53}, {b.dev, "10.10.0.42", "udp", 5353},
			{b.m, "192.168.56.2", "tcp", 22}, {b.m, "192.168.56.2", "tcp", tg.Management.UIPort}, {b.m, "192.168.56.2", "tcp", 8080},
		} {
			src := netip.MustParseAddr(p.src)
			sub := domain.Subject{IP: src}
			if dev, ok := b.w.id.OwnerOf(src); ok {
				sub.Device = dev
			}
			if n, ok := world.NetworkOf(src); ok {
				sub.Network = n
			}
			ex := world.AccessDecision(domain.Query{Source: sub, DestIP: netip.MustParseAddr("10.10.0.1"), Protocol: p.proto, Port: p.port}, facts)
			got := b.try(p.ns, p.proto, "10.10.0.1", p.port)
			ok := map[string]bool{"allow": got == "ok" || got == "refused", "drop": got == "timeout", "reject": got == "refused" || got == "reset"}[ex.Verdict]
			if !ok {
				t.Errorf("%s, %s %s/%d: the kernel gave %q, explain says %+v", stage, p.src, p.proto, p.port, got, ex)
			}
		}
	}
	// the protection of the test network: DNS is answered, everything else is dropped
	b.expect(b.dev, "udp", "10.10.0.1", 53, "ok")
	b.expect(b.dev, "tcp", "10.10.0.1", 22, "timeout")
	b.expect(b.dev, "tcp", "10.10.0.1", 8080, "timeout")
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
	agree("without rules")

	// an allow rule for SSH and the UI port: SSH opens, the UI port does not
	b.w.addConfigRule(ruleA, `{name: allow-gw, source: {device: esp32-42}, protocol: tcp, ports: [22, 443], action: allow}`)
	tg = b.apply()
	b.expect(b.dev, "tcp", "10.10.0.1", 22, "ok")
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
	b.expect(b.dev, "tcp", "10.10.0.1", 8080, "timeout") // not selected by the rule
	if b.counter(tg.Access.Rules[0].Counter) == 0 {
		t.Error("the allow rule counts nothing")
	}
	agree("an allow rule for SSH and the UI")

	// allow everything from everybody: a port nothing listens on is open now (the gateway's kernel
	// answers with a refusal instead of the protection's silence), the UI port is still closed
	b.w.addConfigRule(ruleB, `{name: allow-all, source: {global: true}, action: allow}`)
	tg = b.apply()
	b.expect(b.dev, "tcp", "10.10.0.1", 8080, "refused")
	b.expect(b.dev, "udp", "10.10.0.1", 5353, "refused")
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
	agree("an allow rule for everything")

	// a drop or reject rule that stands first still wins over the allow rule behind it
	b.w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [22]}}`, time.Hour)
	tg = b.apply()
	b.expect(b.dev, "tcp", "10.10.0.1", 22, "timeout")
	agree("a drop overlay in front")

	// the anti-lockout rule: the newest rule drops everything from everybody, and the management
	// host reaches SSH and the UI/API all the same
	b.w.ruleOverlay(`{target: {global: true}, rule: {action: drop}}`, 2*time.Hour)
	tg = b.apply()
	b.expect(b.m, "tcp", "192.168.56.1", 22, "ok")
	b.expect(b.m, "tcp", "192.168.56.1", 443, "ok")
	if b.counter(AntiLockoutCounter) == 0 {
		t.Error("the anti-lockout rule counts nothing")
	}
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
	agree("a drop rule for everybody")
}

// icmpScript sends one UDP datagram and prints the ICMP error that comes back as "type/code" (read
// from the socket's error queue, so a code that Linux maps to the same errno as another is still
// told apart), or "none".
const icmpScript = `
import socket, struct, sys, time
ip, port = sys.argv[1], int(sys.argv[2])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_IP, 11, 1)  # IP_RECVERR
s.setblocking(False)
s.connect((ip, port)); s.send(b"hi")
end = time.time() + 3
while time.time() < end:
    try:
        _, anc, _, _ = s.recvmsg(100, 256, socket.MSG_ERRQUEUE)
    except BlockingIOError:
        time.sleep(0.05); continue
    for level, typ, data in anc:
        if level == socket.SOL_IP and typ == 11:
            _, origin, t, c = struct.unpack("=IBBB", data[:7])
            if origin == 2:  # SO_EE_ORIGIN_ICMP
                print("%d/%d" % (t, c)); sys.exit()
print("none")
`

// icmp returns the ICMP error "type/code" that a datagram from ns gets back.
func (b *accessBed) icmp(ns *testbed.Namespace, ip string, port int) string {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := ns.Run(ctx, "python3", "-c", icmpScript, ip, fmt.Sprint(port))
	if err != nil {
		b.t.Fatalf("%v", err)
	}
	return strings.TrimSpace(out)
}

// A reject answers with the ICMP error the rule names, in the forward path (a datagram to the
// server behind the uplink) and in the input path (a datagram to the gateway): the sender reads
// the type and the code off the wire, and a rule without a type answers with port unreachable.
func TestARejectAnswersWithTheIcmpErrorTheRuleNamesOnTheRealKernel(t *testing.T) {
	b := newAccessBed(t)
	for i, c := range []struct{ name, with, want string }{
		{"default", "", "3/3"},                // port unreachable
		{"port", "port_unreachable", "3/3"},   // port unreachable
		{"host", "host_unreachable", "3/1"},   // host unreachable
		{"admin", "admin_prohibited", "3/13"}, // communication administratively prohibited
		{"route", "no_route", "3/0"},          // network unreachable
	} {
		body := fmt.Sprintf("{name: %s, source: {device: esp32-42}, protocol: udp, ports: [%d], action: reject", c.name, 5001+i)
		if c.with != "" {
			body += ", reject_with: " + c.with
		}
		b.w.addConfigRule(fmt.Sprintf("a1000000-0000-4000-8000-00000000010%d", i), body+"}")
	}
	b.apply()
	for i, want := range []string{"3/3", "3/3", "3/1", "3/13", "3/0"} {
		port := 5001 + i
		if got := b.icmp(b.dev, "203.0.113.10", port); got != want {
			t.Errorf("forward, udp %d: ICMP %s, want %s", port, got, want)
		}
		if got := b.icmp(b.dev, "10.10.0.1", port); got != want {
			t.Errorf("input, udp %d: ICMP %s, want %s", port, got, want)
		}
	}
	// a port no rule names: the gateway's protection drops the datagram to the gateway silently
	if got := b.icmp(b.dev, "10.10.0.1", 5999); got != "none" {
		t.Errorf("the protection answered a datagram no rule names: ICMP %s", got)
	}
	// TCP: administratively prohibited arrives as the sender's "no route to host" error
	b.w.cfg = b.replaceRule("a1000000-0000-4000-8000-000000000100", `{name: tcp-admin, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: reject, reject_with: admin_prohibited}`)
	b.apply()
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "error-EHOSTUNREACH")
}

// The cut window (spike S3, C6): a rule that cuts resets the packets of the connections that
// exist, once, towards the device; a connection that an earlier rule owns is left alone; the server
// side stays half-open; closing the window (emptying the chains) leaves the rule as it was, which
// drops new connections.
func TestACutWindowResetsEstablishedConnectionsOnTheRealKernel(t *testing.T) {
	b := newAccessBed(t)
	b.w.addConfigRule(ruleA, `{name: keep-ssh, source: {device: esp32-42}, protocol: tcp, ports: [22], action: allow}`)
	b.apply()

	// two connections exist before the cutting rule does
	mqtt := b.dev.Start("python3", "-c", holdScript, "203.0.113.10", "8883")
	ssh := b.dev.Start("python3", "-c", holdScript, "203.0.113.10", "22")
	waitOutput(t, mqtt, "ok")
	waitOutput(t, ssh, "ok")

	b.w.addConfigRule(ruleB, `{name: cut-tcp, source: {device: esp32-42}, protocol: tcp, action: drop, cut_existing: true}`)
	tg := b.apply()
	if !tg.Access.HasCuts() {
		t.Fatal("the rule must be able to cut")
	}
	// established traffic is accepted first: nothing happens without a window
	time.Sleep(time.Second)
	if out := mqtt.Output(); strings.Contains(out, "Error") {
		t.Fatalf("the connection broke without a window: %s", out)
	}
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "timeout")

	// the window: the connection of rule B is reset, the one rule A owns is not
	cut := func(keys ...string) {
		tx, err := tg.Access.CutTransaction(keys)
		if err != nil {
			t.Fatal(err)
		}
		b.gw.MustStdin(string(tx), "nft", "-j", "-f", "-")
	}
	cut("config:" + ruleB)
	waitOutput(t, mqtt, "ConnectionResetError")
	cut() // the window is over
	time.Sleep(time.Second)
	if out := ssh.Output(); strings.Contains(out, "Error") || strings.Contains(out, "bad") {
		t.Errorf("the connection that rule A owns was cut: %s", out)
	}
	// the server side is half-open, as in a real outage
	if out := b.srv.Must("ss", "-tn", "state", "established", "sport = :8883"); !strings.Contains(out, ":8883") {
		t.Errorf("the server side of the cut connection is gone: %q", out)
	}
	// the chains are empty again; the rule drops new connections as before
	if out := b.gw.Must("nft", "list", "chain", "inet", "chaosgw", CutForwardChain); strings.Contains(out, "reject") {
		t.Errorf("the window is still open: %s", out)
	}
	b.expect(b.dev, "tcp", "203.0.113.10", 8883, "timeout")
	ssh.Stop()
}

// replaceRule returns the configuration with the body of a rule replaced.
func (b *accessBed) replaceRule(id, body string) *model.Configuration {
	b.t.Helper()
	b.removeRule(id)
	b.w.addConfigRule(id, body)
	return b.w.cfg
}

// removeRule returns the configuration without the rule.
func (b *accessBed) removeRule(id string) *model.Configuration {
	b.t.Helper()
	cfg := *b.w.cfg
	rules := map[string]model.AccessRule{}
	for k, v := range *cfg.AccessRules {
		if k != id {
			rules[k] = v
		}
	}
	var order []uuid.UUID
	for _, u := range *cfg.AccessRuleOrder {
		if u.String() != id {
			order = append(order, u)
		}
	}
	cfg.AccessRules, cfg.AccessRuleOrder = &rules, &order
	b.w.cfg = &cfg
	return &cfg
}

func waitOutput(t *testing.T, p *testbed.Process, want string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for !strings.Contains(p.Output(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output: %s", want, p.Output())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
