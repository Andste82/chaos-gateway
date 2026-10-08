//go:build testbed

package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
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
	w               *faultWorld
	current         *linux.Ruleset
}

// newAccessBed builds the topology of the compiler's fixture: IoT (10.10.0.0/24) on br-iot with the
// device esp32-42 at 10.10.0.42, the uplink wan0 (203.0.113.1/24) with the server at
// 203.0.113.10, the management interface mgmt0 (192.168.56.1/24) with a host at 192.168.56.2. The
// gateway's ports carry the names the compiler uses, so the compiled ruleset sees what it expects.
func newAccessBed(t *testing.T) *accessBed {
	t.Helper()
	bed := testbed.New(t)
	b := &accessBed{t: t, gw: bed.Add("gw"), dev: bed.Add("dev"), srv: bed.Add("srv"), m: bed.Add("mgmt"), w: accessWorld(t)}
	bed.Link(testbed.End{NS: b.gw, If: "br-iot", Addr: "10.10.0.1/24"}, testbed.End{NS: b.dev, If: "eth0", Addr: "10.10.0.42/24"})
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
import socket, sys, time
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
// allow rule does not open anything the gateway's protection closes, and the control plane stays
// reachable for the management sources whatever the rules say (the anti-lockout rule).
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
	// the same gateway without the overlay: the allow rule returns to the protection, which drops it
	b.w.overlays = nil
	b.apply()
	b.expect(b.dev, "tcp", "10.10.0.1", 22, "timeout")
	b.expect(b.dev, "tcp", "10.10.0.1", 443, "timeout")
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
