//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The measurement tests of the MTU family (M10, plan §2.5, spike S13) on the real kernel: the three modes of a
// path-MTU fault written through the engine, each with a device it does not name as the control.
//
// What is asserted does not depend on the speed of the machine: a transfer completes or it stalls, the kernel
// answers or it does not, a segment is as big as it is. There is nothing statistical, so the same assertions run
// under emulation and on a native or KVM kernel.

const (
	pmtuPort  = 7000
	bulkBytes = 300_000
)

// pmtuServer is a TCP echo server in the server's namespace that prints the segment size every connection was
// negotiated with, in the order the connections come ("conn 3 mss 1348"). The device behind the gateway is
// always the gateway's address to it (NAT), so the order tells the connections apart.
const pmtuServer = `
import socket, sys, threading
srv = socket.socket(); srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind((sys.argv[1], int(sys.argv[2]))); srv.listen(16)
n = 0
def handle(c, i):
    print("conn", i, "mss", c.getsockopt(socket.IPPROTO_TCP, socket.TCP_MAXSEG), flush=True)
    try:
        while True:
            d = c.recv(65536)
            if not d:
                break
            c.sendall(d)
    except OSError:
        pass
    c.close()
while True:
    c, _ = srv.accept(); n += 1
    threading.Thread(target=handle, args=(c, n), daemon=True).start()
`

// pmtuClient sends n bytes and reads them back (the spike's bulk.py): it prints whether all came back, how many did,
// and the segment size its own side negotiated.
const pmtuClient = `
import json, socket, sys, threading, time
host, port, n, timeout = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), float(sys.argv[4])
s = socket.create_connection((host, port), timeout=5)
mss = s.getsockopt(socket.IPPROTO_TCP, socket.TCP_MAXSEG)
s.settimeout(timeout)
t0 = time.monotonic()
def send():
    try:
        s.sendall(b"x" * n)
    except OSError:
        pass
threading.Thread(target=send, daemon=True).start()
got = 0
try:
    while got < n:
        d = s.recv(65536)
        if not d:
            break
        got += len(d)
except (socket.timeout, OSError):
    pass
print(json.dumps({"ok": got == n, "received": got, "seconds": round(time.monotonic() - t0, 2), "mss": mss}))
`

type bulkResult struct {
	OK       bool    `json:"ok"`
	Received int     `json:"received"`
	Seconds  float64 `json:"seconds"`
	MSS      int     `json:"mss"`
}

// pmtuLab is the lab with the echo server running.
type pmtuLab struct {
	*real
	server *testbed.Process
}

func startPMTULab(t *testing.T) *pmtuLab {
	t.Helper()
	r := startFaultLab(t, nil)
	p := r.top.Server.Start("python3", "-c", pmtuServer, testbed.ServerAddr, fmt.Sprint(pmtuPort))
	deadline := time.Now().Add(30 * time.Second)
	for {
		if out, err := r.top.Server.Run(context.Background(), "ss", "-ltn", "sport", "=", fmt.Sprint(pmtuPort)); err == nil && strings.Contains(out, fmt.Sprint(pmtuPort)) {
			break
		}
		select {
		case <-p.Done():
			t.Fatalf("the echo server stopped: %s", p.Output())
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the echo server does not listen: %s", p.Output())
		}
	}
	return &pmtuLab{real: r, server: p}
}

// bulk transfers bulkBytes through the gateway and back.
func (l *pmtuLab) bulk(from *testbed.Namespace, timeout time.Duration) bulkResult {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout+60*time.Second)
	defer cancel()
	out, err := from.Run(ctx, "python3", "-c", pmtuClient, testbed.ServerAddr, fmt.Sprint(pmtuPort), fmt.Sprint(bulkBytes), fmt.Sprint(timeout.Seconds()))
	if err != nil {
		l.t.Fatalf("the transfer cannot be run: %v\n%s", err, out)
	}
	var res bulkResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		l.t.Fatalf("%v\n%s", err, out)
	}
	l.t.Logf("transfer: %+v", res)
	return res
}

// serverMSS is the segment sizes the server negotiated, per connection in order.
func (l *pmtuLab) serverMSS() []int {
	var out []int
	for _, line := range strings.Split(l.server.Output(), "\n") {
		var i, mss int
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "conn %d mss %d", &i, &mss); err == nil {
			out = append(out, mss)
		}
	}
	return out
}

// flushPMTU forgets what the hosts have learned about paths: the cache of the server (the gateway's NAT address is
// the destination of its answers) and of the devices.
func (l *pmtuLab) flushPMTU() {
	for _, ns := range []*testbed.Namespace{l.top.Server, l.top.A, l.top.B} {
		ns.Must("ip", "route", "flush", "cache")
	}
}

// routesIn lists the routes of a table; a table with no route does not exist for the kernel, which is an empty list.
func routesIn(ns *testbed.Namespace, table string) string {
	out, err := ns.Run(context.Background(), "ip", "-d", "route", "show", "table", table)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ping sends one DF ping of the given payload size and returns the output and whether it was answered.
func ping(from *testbed.Namespace, size int) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := from.Run(ctx, "ping", "-M", "do", "-s", fmt.Sprint(size), "-c", "2", "-W", "2", "-n", testbed.ServerAddr)
	return out, err == nil
}

// ICMP mode (spike S13): the kernel itself answers a packet that does not fit with "fragmentation needed, mtu N", in
// both directions, so the transfer of a device whose path is cut to 1280 bytes completes with smaller segments, and the
// device the fault does not name is not touched. The reduced path MTU is cached by the server for the gateway's NAT
// address, which is the documented side effect (plan §2.5, risk 23): so the control runs first, and the cache is
// flushed between the cases.
func TestAnIcmpMTUFaultMakesTheKernelAnswerLargePacketsAndTheTransferCompletes(t *testing.T) {
	l := startPMTULab(t)
	// control, before anything: B is not limited
	if out, ok := ping(l.top.B, 1400); !ok {
		t.Fatalf("B cannot send a large packet before the fault: %s", out)
	}
	before := l.bulk(l.top.B, 40*time.Second)
	if !before.OK {
		t.Fatalf("B does not complete the transfer before the fault: %+v", before)
	}
	l.flushPMTU()

	res := l.mustPut(admin, "target: {device: dev-a}\nfault: {family: mtu, mtu: {size: 1280, mode: icmp}}")
	tg := l.verifyKernel()
	if len(tg.PMTU) != 1 || tg.PMTU[0].Table != 103 {
		t.Fatalf("%+v", tg.PMTU)
	}
	// the mirror table of the kernel: the connected routes and the default route with the size locked in
	mirror := l.top.GW.Must("ip", "-d", "route", "show", "table", "103")
	if !strings.Contains(mirror, "default via "+testbed.UplinkGateway) && !strings.Contains(mirror, "default via") {
		t.Errorf("the mirror table has no default route:\n%s", mirror)
	}
	for _, line := range strings.Split(strings.TrimSpace(mirror), "\n") {
		if !strings.Contains(line, "mtu lock 1280") {
			t.Errorf("a mirror route without the locked size: %s", line)
		}
	}
	if rules := l.top.GW.Must("ip", "rule", "show"); !strings.Contains(rules, "fwmark 0x20000/0xe0000 lookup 103") {
		t.Errorf("no rule on the mark:\n%s", rules)
	}

	// A: the kernel answers a DF packet that does not fit with fragmentation needed and the size
	out, ok := ping(l.top.A, 1400)
	if ok || !strings.Contains(out, "mtu = 1280") {
		t.Errorf("A's large DF ping: answered %v, output:\n%s", ok, out)
	}
	// a packet that fits goes through
	if out, ok := ping(l.top.A, 1200); !ok {
		t.Errorf("A's small ping does not go through:\n%s", out)
	}
	// the control is not limited: its large ping goes through at once
	if out, ok := ping(l.top.B, 1400); !ok {
		t.Errorf("B, whom the fault does not name, is limited:\n%s", out)
	}
	l.flushPMTU()
	if b := l.bulk(l.top.B, 40*time.Second); !b.OK {
		t.Errorf("B's transfer: %+v", b)
	}
	l.flushPMTU()

	// A's 300 KB transfer completes: the sender learns the size from the answers and sends smaller segments
	a := l.bulk(l.top.A, 40*time.Second)
	if !a.OK || a.Received != bulkBytes {
		t.Errorf("A's transfer with the ICMP mode does not complete: %+v", a)
	}
	// the download is cut as well: the gateway answered the server, which caches the size for the NAT address
	cache := l.top.Server.Must("ip", "route", "get", testbed.UplinkGateway)
	if !strings.Contains(cache, "mtu 1280") {
		t.Errorf("the server was not told the size of the path (the download is not cut):\n%s", cache)
	}
	// the counters count the packets of the fault; no packet is dropped by it
	f := tg.PMTU[0]
	cs := l.counters()
	if cs[f.CounterUp].Packets == 0 || cs[f.CounterDown].Packets == 0 {
		t.Errorf("counters up %+v down %+v", cs[f.CounterUp], cs[f.CounterDown])
	}

	// the fault ends: the large packets pass again, and the routes of the table are gone
	l.deleteOverlay(res)
	l.flushPMTU()
	if out, ok := ping(l.top.A, 1400); !ok {
		t.Errorf("A is still limited after the fault ended:\n%s", out)
	}
	if got := routesIn(l.top.GW, "103"); got != "" {
		t.Errorf("the mirror table is left:\n%s", got)
	}
	l.verifyKernel()
}

// Black-hole mode: packets longer than the size are dropped without a word, no ICMP is sent, and a transfer of
// full-size segments stalls (the path MTU black hole of spike S13). What fits goes through, the control device is not
// touched, and the drops are counted.
func TestABlackholeDropsLargePacketsSilentlyAndTheTransferStalls(t *testing.T) {
	l := startPMTULab(t)
	res := l.mustPut(admin, "target: {device: dev-a}\nfault: {family: mtu, mtu: {size: 1280, mode: blackhole}}")
	tg := l.verifyKernel()
	f := tg.PMTU[0]
	if f.Mode != "blackhole" || f.Table != 0 || f.CounterDrop == "" {
		t.Fatalf("%+v", f)
	}
	if t0 := routesIn(l.top.GW, "103"); t0 != "" {
		t.Errorf("a black hole has a mirror table:\n%s", t0)
	}
	out, ok := ping(l.top.A, 1400)
	if ok || strings.Contains(out, "mtu") || strings.Contains(out, "Frag needed") {
		t.Errorf("A's large DF ping: answered %v (a black hole sends no ICMP):\n%s", ok, out)
	}
	if out, ok := ping(l.top.A, 1200); !ok {
		t.Errorf("A's small ping does not go through:\n%s", out)
	}
	if out, ok := ping(l.top.B, 1400); !ok {
		t.Errorf("B, whom the fault does not name, is limited:\n%s", out)
	}
	// the control completes, the device stalls: the handshake passes, the first full-size segment does not
	if b := l.bulk(l.top.B, 40*time.Second); !b.OK {
		t.Errorf("B's transfer: %+v", b)
	}
	a := l.bulk(l.top.A, 8*time.Second)
	if a.OK || a.Received >= bulkBytes {
		t.Errorf("A's transfer through a black hole completed: %+v", a)
	}
	cs := l.counters()
	if cs[f.CounterDrop].Packets == 0 {
		t.Errorf("the drops are not counted: %+v", cs[f.CounterDrop])
	}
	if cs[f.CounterUp].Packets == 0 {
		t.Errorf("the packets are not counted: %+v", cs[f.CounterUp])
	}
	// the server was never told a size: it has no cache entry for the gateway
	if cache := l.top.Server.Must("ip", "route", "get", testbed.UplinkGateway); strings.Contains(cache, "mtu") {
		t.Errorf("the black hole told the server a size:\n%s", cache)
	}

	// the overlay ends: the same device completes
	l.deleteOverlay(res)
	if a := l.bulk(l.top.A, 40*time.Second); !a.OK {
		t.Errorf("A's transfer after the black hole: %+v", a)
	}
	l.verifyKernel()
}

// MSS clamp: the SYN of the selected device is rewritten to the size, in both directions of the handshake, so its
// segments are no longer than the size. Only TCP of that device is touched: its large pings go through, the control's
// segments are full-size, and, unlike the ICMP mode, the server learns nothing about the path.
func TestAnMSSClampLimitsTheSegmentsOfTheSelectedDeviceOnly(t *testing.T) {
	l := startPMTULab(t)
	l.mustPut(admin, "target: {device: dev-a}\nfault: {family: mtu, mtu: {size: 1400, mode: mss_clamp}}")
	tg := l.verifyKernel()
	if tg.PMTU[0].MSS() != 1360 {
		t.Fatalf("%+v", tg.PMTU[0])
	}
	// the clamp is a rule of the fault's chain; nothing is routed differently
	if t0 := routesIn(l.top.GW, "103"); t0 != "" {
		t.Errorf("an MSS clamp has a mirror table:\n%s", t0)
	}

	a := l.bulk(l.top.A, 40*time.Second)
	b := l.bulk(l.top.B, 40*time.Second)
	if !a.OK || !b.OK {
		t.Fatalf("A %+v B %+v", a, b)
	}
	// the MSS option offers 1360, a connection with timestamps carries 12 bytes of options of it: the packets are 1400
	// at most. The control negotiates the full size of the path.
	if a.MSS > 1360 || a.MSS < 1300 {
		t.Errorf("A's own side negotiated %d, want at most the clamp 1360", a.MSS)
	}
	if b.MSS < 1400 {
		t.Errorf("B's own side negotiated %d, not the full size of the path", b.MSS)
	}
	got := l.serverMSS()
	if len(got) != 2 || got[0] > 1360 || got[0] < 1300 || got[1] < 1400 {
		t.Errorf("the server negotiated %v (A first, then B)", got)
	}
	// other traffic of the device is not touched: a 1472-byte ping is not cut
	if out, ok := ping(l.top.A, 1472); !ok {
		t.Errorf("A's full-size ICMP is touched by the TCP clamp:\n%s", out)
	}
	// nothing was told to the server
	if cache := l.top.Server.Must("ip", "route", "get", testbed.UplinkGateway); strings.Contains(cache, "mtu") {
		t.Errorf("the MSS clamp told the server a size:\n%s", cache)
	}
	cs := l.counters()
	if f := tg.PMTU[0]; cs[f.CounterUp].Packets == 0 || cs[f.CounterDown].Packets == 0 {
		t.Errorf("counters %+v %+v", cs[f.CounterUp], cs[f.CounterDown])
	}
}

// Two devices, two modes, two sizes at the same time: each is limited by its own fault and by nothing else, next to an
// impairment of the same device (the families resolve independently).
func TestTwoMTUFaultsAndAnImpairmentOfTheSameDeviceDoNotInterfere(t *testing.T) {
	l := startPMTULab(t)
	l.mustPut(admin, "target: {device: dev-a}\nfault: {family: mtu, mtu: {size: 1280, mode: icmp}}")
	l.mustPut(admin, "target: {device: dev-b}\nfault: {family: mtu, mtu: {size: 1000, mode: blackhole}}")
	l.mustPut(admin, "target: {device: dev-a}\nfault: {latency: 30ms}")
	l.verifyKernel()
	out, ok := ping(l.top.A, 1400)
	if ok || !strings.Contains(out, "mtu = 1280") {
		t.Errorf("A: %v\n%s", ok, out)
	}
	if out, ok := ping(l.top.B, 1200); ok {
		t.Errorf("B's 1200-byte packets pass the black hole of 1000: %s", out)
	}
	if out, ok := ping(l.top.B, 900); !ok {
		t.Errorf("B's 900-byte packets do not pass: %s", out)
	}
	// C is in no fault: nothing limits it
	if out, ok := ping(l.top.C, 1472); !ok {
		t.Errorf("C is limited: %s", out)
	}
	// and the impairment of A is still there next to the mark
	echo := testbed.StartEcho(t, l.top.Server, testbed.ServerAddr, probePort)
	res := echo.MustProbe(t, l.top.A, quietRun())
	if res.UpMedian() < 25*time.Millisecond {
		t.Errorf("the delay of A is gone: %s", res)
	}
}
