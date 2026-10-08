//go:build testbed

package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M9 on the real kernel through the engine: a rule overlay written to the engine is in the kernel when
// the write is answered; a new connection of its scope is refused, the ones of other devices and
// other ports are not; "also cut existing connections" resets a TCP connection of the device and deletes
// the tracked flow of a UDP stream, so that its next packet is a new connection and meets the rule; the
// rule's counter counts what it decided; the TTL takes the rule away and the device is let through
// again. The lab is the one of the fault tests (devices A and B in IoT, C in Lab, a server behind the
// uplink). The functional assertions hold under emulation; nothing here is statistical.

// accessServerScript echoes on tcp 8883 and 7050 and on udp 5353.
const accessServerScript = `
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
for p in (8883, 7050):
    threading.Thread(target=tcp, args=(p,), daemon=True).start()
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.bind(("0.0.0.0", 5353))
while True:
    d, a = u.recvfrom(100); u.sendto(d, a)
`

// accessTry makes one attempt (tcp or udp) and prints ok, refused, reset or timeout.
const accessTry = `
import socket, sys
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

// accessHold keeps a TCP connection busy: one byte every 100 ms (or every argv[3] seconds), one line per
// answer, until it breaks.
const accessHold = `
import socket, sys, time
pause = float(sys.argv[3]) if len(sys.argv) > 3 else 0.1
s = socket.socket(); s.settimeout(3); s.connect((sys.argv[1], int(sys.argv[2])))
print("connected", flush=True)
while True:
    try:
        s.sendall(b"x"); r = s.recv(10)
        print("ok" if r == b"x" else "bad", flush=True)
    except Exception as e:
        print(type(e).__name__, flush=True); break
    time.sleep(pause)
`

// accessUDPStream sends a datagram every 100 ms from one socket (one tracked flow) and prints ok when
// the echo comes back within 300 ms and lost when it does not.
const accessUDPStream = `
import socket, sys, time
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(0.3); s.connect((sys.argv[1], int(sys.argv[2])))
print("started", flush=True)
while True:
    s.send(b"x")
    try:
        s.recv(10); print("ok", flush=True)
    except socket.timeout:
        print("lost", flush=True)
    time.sleep(0.1)
`

func waitText(t *testing.T, p *testbed.Process, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !strings.Contains(p.Output(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output: %s", want, p.Output())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func try(top *testbed.Topology, from *testbed.Namespace, proto string, port string) string {
	out, _ := from.Run(context.Background(), "python3", "-c", accessTry, proto, testbed.ServerAddr, port, "2")
	return strings.TrimSpace(out)
}

func TestARuleOverlayRefusesNewConnectionsAndCutsExistingOnesOfItsDeviceOnly(t *testing.T) {
	r := startFaultLab(t, nil)
	top := r.top
	top.Server.Start("python3", "-c", accessServerScript)
	deadline := time.Now().Add(60 * time.Second)
	for try(top, top.A, "tcp", "8883") != "ok" || try(top, top.A, "udp", "5353") != "ok" {
		if time.Now().After(deadline) {
			t.Fatal("the server does not answer")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// A and B hold a TCP connection each, A streams UDP
	holdA := top.A.Start("python3", "-c", accessHold, testbed.ServerAddr, "8883")
	holdB := top.B.Start("python3", "-c", accessHold, testbed.ServerAddr, "8883")
	udpA := top.A.Start("python3", "-c", accessUDPStream, testbed.ServerAddr, "5353")
	for _, p := range []*testbed.Process{holdA, holdB, udpA} {
		waitText(t, p, "ok", 60*time.Second)
	}

	// the rule: A may not talk to the server, and what exists is cut: a reset for the TCP connection
	// (protocol any), the deletion of the tracked flow for the UDP stream
	res, err := r.e.PutOverlay(context.Background(), engine.OverlayWrite{Owner: model.Owner{Type: "user", Id: "test"},
		Request: overlayRequest(t, "target: {device: "+flDevA+"}\nrule: {destination: {cidr: "+testbed.ServerAddr+"/32}, action: drop, cut_existing: true}")})
	if err != nil {
		t.Fatal(err)
	}

	// the established TCP connection of A is reset; B's goes on
	waitText(t, holdA, "ConnectionResetError", 30*time.Second)
	before := strings.Count(holdB.Output(), "ok")
	time.Sleep(2 * time.Second)
	if strings.Contains(holdB.Output(), "Error") || strings.Count(holdB.Output(), "ok") <= before {
		t.Errorf("B's connection was disturbed by A's rule: %s", holdB.Output())
	}
	// A's UDP flow was deleted from the conntrack table: its next datagram is a new connection and meets the rule
	waitText(t, udpA, "lost", 30*time.Second)
	// new connections: A is refused everything towards the server, B and C are not
	for _, c := range []struct{ proto, port string }{{"tcp", "8883"}, {"tcp", "7050"}, {"udp", "5353"}} {
		if got := try(top, top.A, c.proto, c.port); got != "timeout" {
			t.Errorf("A %s/%s: %s, want timeout (dropped)", c.proto, c.port, got)
		}
	}
	for _, c := range []struct {
		name  string
		ns    *testbed.Namespace
		proto string
		port  string
	}{{"B", top.B, "tcp", "8883"}, {"C", top.C, "tcp", "8883"}, {"B", top.B, "udp", "5353"}} {
		if got := try(top, c.ns, c.proto, c.port); got != "ok" {
			t.Errorf("%s %s/%s: %s, want ok", c.name, c.proto, c.port, got)
		}
	}

	// the counters count what the rules decided (the SYNs and datagrams that were dropped)
	counters, err := r.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := r.e.Snapshot()
	var seen int
	for _, rule := range s.Access.Rules {
		if v := counters[rule.Counter]; v.Packets > 0 {
			seen++
		}
	}
	if seen == 0 {
		t.Errorf("no rule counted a packet: %+v", counters)
	}
	// the window is closed and the cut chains are empty
	if out := top.GW.Must("nft", "list", "chain", "inet", "chaosgw", compiler.CutForwardChain); strings.Contains(out, "reject") {
		t.Errorf("the cut window is still open:\n%s", out)
	}

	// the rules go: A is let through again
	if _, err := r.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	if got := try(top, top.A, "tcp", "8883"); got != "ok" {
		t.Errorf("A after the rules: %s", got)
	}
	waitText(t, udpA, "ok", 10*time.Second)
	holdB.Stop()
	udpA.Stop()
}

// A rule with a TTL takes effect for new connections at once and is gone when the TTL has run out.
func TestARuleOverlayWithATTLRefusesAndThenLetsThrough(t *testing.T) {
	r := startFaultLab(t, nil)
	top := r.top
	top.Server.Start("python3", "-c", accessServerScript)
	deadline := time.Now().Add(60 * time.Second)
	for try(top, top.C, "tcp", "7050") != "ok" {
		if time.Now().After(deadline) {
			t.Fatal("the server does not answer")
		}
		time.Sleep(200 * time.Millisecond)
	}
	res, err := r.e.PutOverlay(context.Background(), engine.OverlayWrite{Owner: model.Owner{Type: "user", Id: "test"},
		Request: overlayRequest(t, "target: {network: Lab}\nrule: {protocol: tcp, ports: [7050], action: reject}\nttl: 4s")})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	// reject answers at once, with an ICMP error that the client reports as a refusal
	if got := try(top, top.C, "tcp", "7050"); got != "refused" {
		t.Errorf("C: %s, want refused", got)
	}
	if got := try(top, top.A, "tcp", "7050"); got != "ok" {
		t.Errorf("A is in another network: %s", got)
	}
	// the overlay expires: its rule is out of the kernel and C is let through
	deadline = time.Now().Add(60 * time.Second)
	for len(r.e.Snapshot().Overlays) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the overlay %s never expired", res.Overlay.Id)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := r.e.Barrier(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := try(top, top.C, "tcp", "7050"); got != "ok" {
		t.Errorf("C after the TTL: %s", got)
	}
	if time.Since(start) < 3*time.Second {
		t.Errorf("the overlay expired after %v: before its TTL", time.Since(start))
	}
}
