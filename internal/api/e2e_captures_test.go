//go:build testbed

package api_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M17 on a real kernel, over the API: the executor captures with AF_PACKET sockets in the namespace of the gateway.
// The ground truth of each test is a capture on the interface of the device itself (spike S9: "exact against a
// capture on the device itself"), made by a session of the same package in the device's namespace.

const clientA = "client-a"

// bedCapture is a testbed gateway whose configuration knows the device client-a (the testbed's A, by its Ethernet
// address) on the network IoT, with the broker of the checks tests running on the server.
func bedCapture(t *testing.T, mods ...func(*options)) (*gw, *testbed.Topology) {
	t.Helper()
	g, top := newBedGW(t, mods...)
	id := g.mustPatch(map[string]any{"devices": map[string]any{espID: map[string]any{"name": clientA, "network": iotID,
		"identifiers": map[string]any{"macs": []any{testbed.ClientAMAC}}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	startBroker(t, top)
	// the gateway knows the device once it has seen it
	if _, err := top.A.Run(context.Background(), "ping", "-c", "1", "-W", "2", testbed.ServerAddr); err != nil {
		t.Fatalf("A does not reach the server: %v", err)
	}
	g.observe()
	return g, top
}

// truthCapture records on an interface of a namespace with a session of its own; stop returns the packets.
type truthCapture struct {
	t   *testing.T
	s   *capture.Session
	dir string
}

func startTruth(t *testing.T, ns *testbed.Namespace, iface string, f capture.Filter) *truthCapture {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	s, err := capture.Start(context.Background(), capture.SessionConfig{Namespace: ns.Name, Interfaces: []capture.IfaceConfig{{Name: iface, Filter: f}},
		Format: capture.PCAPNG, Dir: root, UID: -1, GID: -1})
	if err != nil {
		t.Fatalf("the ground truth on %s of %s: %v", iface, ns.Short, err)
	}
	t.Cleanup(func() { s.Stop(capture.ReasonUser) })
	return &truthCapture{t: t, s: s, dir: dir}
}

func (c *truthCapture) stop() []capture.Packet {
	c.t.Helper()
	c.s.Stop(capture.ReasonUser)
	var out []capture.Packet
	for _, name := range c.s.Files() {
		b, err := os.ReadFile(filepath.Join(c.dir, name))
		if err != nil {
			c.t.Fatal(err)
		}
		out = append(out, readPackets(c.t, b)...)
	}
	return out
}

func readPackets(t *testing.T, b []byte) []capture.Packet {
	t.Helper()
	r, err := capture.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var out []capture.Packet
	for {
		p, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
}

// multiset sorts the frames of the packets that pass keep, so that two captures of the same traffic compare equal
// whatever order the interfaces saw it in.
func multiset(ps []capture.Packet, keep func([]byte) bool) []string {
	var out []string
	for _, p := range ps {
		if keep == nil || keep(p.Data) {
			out = append(out, string(p.Data))
		}
	}
	sort.Strings(out)
	return out
}

// ipOrARP keeps IPv4 packets and ARP: the IPv6 chatter of the namespaces (router solicitations, multicast listeners)
// is sent when the kernel decides to, and the interfaces need not see the same of it.
func ipOrARP(b []byte) bool {
	if len(b) < 14 {
		return false
	}
	t := binary.BigEndian.Uint16(b[12:14])
	return t == 0x0800 || t == 0x0806
}

func sameMultiset(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(want) == 0 {
		t.Fatalf("%s: the ground truth is empty: the test proves nothing", what)
	}
	if len(got) != len(want) {
		t.Errorf("%s: %d packets, the capture on the device itself has %d", what, len(got), len(want))
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: packet %d of the sorted captures differs from the one of the device", what, i)
			return
		}
	}
}

// traffic makes a device open a connection to the broker and send a few datagrams to the server.
const udpScript = `
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for i in range(5): s.sendto(b"datagram-%d" % i, (sys.argv[1], 7000))
`

func makeTraffic(t *testing.T, top *testbed.Topology) {
	t.Helper()
	for _, ns := range []*testbed.Namespace{top.A, top.B} {
		if out, _ := ns.Run(context.Background(), "python3", "-c", tryScript, testbed.ServerAddr); strings.TrimSpace(out) != "ok" {
			t.Fatalf("%s cannot reach the broker: %s", ns.Short, out)
		}
		if _, err := ns.Run(context.Background(), "python3", "-c", udpScript, testbed.ServerAddr); err != nil {
			t.Fatal(err)
		}
		if _, err := ns.Run(context.Background(), "ping", "-c", "2", "-W", "1", testbed.ServerAddr); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(500 * time.Millisecond) // the last packets reach the sockets
}

// The capture of a device holds exactly the frames of the capture on the device itself: its packets and the
// answers to it, ARP included, with the addresses before NAT, and nothing of device B.
func TestACaptureOfADeviceIsExactlyTheCaptureOnTheDeviceItselfOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	truth := startTruth(t, top.A, "eth0", capture.Filter{MACs: []string{testbed.ClientAMAC}})
	c := g.mustStartCapture(map[string]any{"name": "a", "scope": map[string]any{"device": clientA}})
	id := c["id"].(string)
	ifs := c["interfaces"].([]any)
	if ifs[0].(map[string]any)["name"] != "br-iot" {
		t.Fatalf("interfaces %v", ifs)
	}
	makeTraffic(t, top)
	g.stopCapture(id)
	want := multiset(truth.stop(), ipOrARP)

	r, pkts := g.download(id, "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	sameMultiset(t, "device capture", multiset(pkts, ipOrARP), want)
	// before NAT: the addresses of the test network, never the gateway's address on the uplink
	for _, p := range pkts {
		if bytes.Contains(p.Data, []byte{203, 0, 113, 1}) {
			t.Errorf("a frame carries the address of the uplink: the capture is after NAT")
			break
		}
		if bytes.Contains(p.Data, []byte{2, 0, 0, 0, 0, 0x0c}) {
			t.Errorf("a frame of device B is in the capture of device A")
			break
		}
	}
	if got := g.capture(id); got["packets"].(float64) < 10 || got["dropped"] != nil && got["dropped"].(float64) != 0 {
		t.Errorf("%v", got)
	}
}

// A selector holds the packets of its source to its destination, protocol and port: the TCP connections of A to the
// broker, from the SYN to the FIN, in both directions.
func TestASelectorCaptureHoldsTheConnectionsItNamesOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	truth := startTruth(t, top.A, "eth0", capture.Filter{MACs: []string{testbed.ClientAMAC}, Protocol: capture.TCP, Ports: []capture.PortRange{{From: 8883, To: 8883}}})
	c := g.mustStartCapture(map[string]any{"scope": map[string]any{"selector": map[string]any{
		"source": map[string]any{"device": clientA}, "protocol": "tcp", "ports": []int{8883}, "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}}})
	makeTraffic(t, top)
	g.stopCapture(c["id"].(string))
	want := multiset(truth.stop(), ipOrARP)
	_, pkts := g.download(c["id"].(string), "")
	sameMultiset(t, "selector capture", multiset(pkts, ipOrARP), want)
	syn, fin := false, false
	for _, p := range pkts {
		if len(p.Data) >= 14+20+14 && p.Data[12] == 8 && p.Data[13] == 0 {
			flags := p.Data[14+20+13]
			syn, fin = syn || flags&0x02 != 0, fin || flags&0x01 != 0
		}
	}
	if !syn || !fin {
		t.Errorf("the connection is not in the capture from its SYN to its FIN (syn %v, fin %v)", syn, fin)
	}
}

// The uplink can only be captured as a whole and shows the packets after NAT.
func TestTheUplinkCaptureShowsThePacketsAfterNATOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	c := g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}})
	makeTraffic(t, top)
	g.stopCapture(c["id"].(string))
	_, pkts := g.download(c["id"].(string), "")
	fromGW, fromDevice := 0, 0
	for _, p := range pkts {
		if bytes.Contains(p.Data, []byte{203, 0, 113, 1}) {
			fromGW++
		}
		if bytes.Contains(p.Data, []byte{10, 10, 0, 11}) {
			fromDevice++
		}
	}
	if fromGW == 0 || fromDevice != 0 {
		t.Errorf("%d packets of the gateway's uplink address, %d of the device's own: the uplink shows the packets after NAT", fromGW, fromDevice)
	}
}

// A WireGuard network is captured on its interface, as raw IP packets: a pcapng file with the link type RAW, and the
// packets are those the remote client sees in the tunnel.
func TestAWireGuardNetworkIsCapturedAsRawIPOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	id := g.mustPatch(map[string]any{"networks": map[string]any{hubID: map[string]any{"clients": map[string]any{
		"6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f": map[string]any{"name": "rC", "address": "10.99.0.4", "keepalive": "1s", "key": map[string]any{"mode": "generated"}},
	}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	conf := filepath.Join(t.TempDir(), "wgrc.conf")
	if err := os.WriteFile(conf, g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil).Body, 0o600); err != nil {
		t.Fatal(err)
	}
	top.RC.Must("wg-quick", "up", conf)
	if !pingOK(top.RC, "", "10.99.0.1") {
		t.Fatal("the tunnel does not come up")
	}
	truth := startTruth(t, top.RC, "wgrc", capture.Filter{})
	c := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "lab-hub"}})
	ifs := c["interfaces"].([]any)
	if ifs[0].(map[string]any)["name"] != "wg-lab-hub" || ifs[0].(map[string]any)["link_type"] != "raw" {
		t.Fatalf("interfaces %v", ifs)
	}
	if _, err := top.RC.Run(context.Background(), "ping", "-c", "4", "-W", "1", "-i", "0.2", "10.99.0.1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	g.stopCapture(c["id"].(string))
	icmp := func(b []byte) bool {
		return len(b) > 20 && b[0]>>4 == 4 && b[9] == 1 && (bytes.Equal(b[12:16], []byte{10, 99, 0, 4}) || bytes.Equal(b[16:20], []byte{10, 99, 0, 4}))
	}
	want := multiset(truth.stop(), icmp)
	r, pkts := g.download(c["id"].(string), "")
	if r.Status != 200 {
		t.Fatalf("%d", r.Status)
	}
	rd, err := capture.NewReader(bytes.NewReader(r.Body))
	if err != nil || rd.Interfaces() == nil {
		t.Fatalf("reader: %v", err)
	}
	_, _ = rd.Next()
	if ifs := rd.Interfaces(); len(ifs) != 1 || ifs[0].Link != capture.LinkRaw || ifs[0].Name != "wg-lab-hub" {
		t.Errorf("interfaces of the file: %+v", ifs)
	}
	sameMultiset(t, "WireGuard capture", multiset(pkts, icmp), want)
	if out, err := exec.Command("tshark", "-r", writeTemp(t, r.Body), "-Y", "icmp", "-T", "fields", "-e", "ip.src").Output(); err == nil && !strings.Contains(string(out), "10.99.0.4") {
		t.Errorf("Wireshark does not find the ping of the client in the file: %q", out)
	}
}

func writeTemp(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.pcapng")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A run with `capture: true` records its target for the whole run on the real kernel and offers the file in its
// report.
func TestARunWithCaptureTrueAttachesTheTrafficOfItsTargetOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	body := inlineRun("recorded", map[string]any{"device": clientA},
		step("normal", "0s", "profile", "normal"),
		step("end", "6s", "restore", true))
	body["inline"].(map[string]any)["capture"] = true
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "normal", "applied")
	truth := startTruth(t, top.A, "eth0", capture.Filter{MACs: []string{testbed.ClientAMAC}})
	makeTraffic(t, top)
	want := multiset(truth.stop(), ipOrARP)
	run := g.waitRun(id, "passed", "failed", "error")
	if run["status"] != "passed" {
		t.Fatalf("%v %v", run["status"], run["reason"])
	}
	arts := artifactsOfKind(run, "capture")
	if len(arts) != 1 {
		t.Fatalf("artifacts %v", run["artifacts"])
	}
	capID := arts[0]["capture"].(string)
	if cp := g.capture(capID); cp["run"] != id || cp["stop_reason"] != "run_end" || cp["status"] != "stopped" {
		t.Errorf("%v", cp)
	}
	dl := g.do("GET", arts[0]["url"].(string)[len("/api/v1"):], nil, nil, nil)
	if dl.Status != 200 {
		t.Fatalf("%d", dl.Status)
	}
	sameMultiset(t, "capture of the run", multiset(readPackets(t, dl.Body), ipOrARP), want)
	if len(warningCodes(run)) != 0 {
		t.Errorf("warnings %v", run["warnings"])
	}
}

// Low disk stops captures but not the run (plan §3.6): the volume of the captures is a small file system, the
// traffic fills it, the executor stops the capture, and the run ends as passed with the warning and the file.
func TestALowDiskStopsTheCaptureButNotTheRunOnTheRealKernel(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "captures")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mount", "-t", "tmpfs", "-o", "size=4m", "tmpfs", dir).CombinedOutput(); err != nil {
		t.Fatalf("cannot mount a small file system for the captures: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", "-l", dir).Run() })
	g, top := bedCapture(t, func(o *options) { o.root = root })

	body := inlineRun("lowdisk", map[string]any{"device": clientA},
		step("normal", "0s", "profile", "normal"),
		step("end", "15s", "restore", true))
	body["inline"].(map[string]any)["capture"] = true
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "normal", "applied")
	// more traffic than the volume holds: 8 MB of echo through the gateway
	const bulk = `
import socket, sys
s = socket.socket(); s.settimeout(10); s.connect((sys.argv[1], 8883))
sent = got = 0
chunk = b"x" * 1400
while sent < 6000000:
    s.sendall(chunk); sent += len(chunk)
    try:
        got += len(s.recv(65536))
    except Exception: pass
print("done")
`
	if out, _ := top.A.Run(context.Background(), "python3", "-c", bulk, testbed.ServerAddr); !strings.Contains(out, "done") {
		t.Logf("the bulk transfer: %s", out)
	}
	run := g.waitRun(id, "passed", "failed", "error")
	if run["status"] != "passed" {
		t.Fatalf("the run ended as %v (%v)", run["status"], run["reason"])
	}
	if codes := warningCodes(run); len(codes) != 1 || codes[0] != "capture_missing" {
		t.Fatalf("warnings %v", run["warnings"])
	}
	arts := artifactsOfKind(run, "capture")
	if len(arts) != 1 {
		t.Fatalf("artifacts %v", run["artifacts"])
	}
	cp := g.capture(arts[0]["capture"].(string))
	if cp["stop_reason"] != "disk_low" || cp["status"] != "stopped" || cp["size_bytes"].(float64) <= 0 {
		t.Errorf("%v", cp)
	}
	if _, pkts := g.download(arts[0]["capture"].(string), ""); len(pkts) == 0 {
		t.Error("what was recorded before the disk ran low is gone")
	}
	// and a new capture is refused on that volume while it stays full
	rr := g.startCapture(map[string]any{"scope": map[string]any{"uplink": true}})
	if rr.Status != 201 && (rr.Status != 422 || rr.code(t) != "capacity_exceeded") {
		t.Errorf("%d %s", rr.Status, rr.Body)
	}
}

// The executor refuses a capture on a volume below its limit, also when the API's own check passes.
func TestTheExecutorRefusesACaptureOnAVolumeBelowItsLimitOnTheRealKernel(t *testing.T) {
	g, _ := bedCapture(t, func(o *options) { o.captureMinFree = 100.1 })
	g.badRequest = true
	defer func() { g.badRequest = false }()
	r := g.startCapture(map[string]any{"scope": map[string]any{"network": "IoT"}})
	if r.Status != 422 || r.code(t) != "capacity_exceeded" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if entries, _ := os.ReadDir(filepath.Join(g.root, "captures")); len(entries) != 0 {
		t.Errorf("the refused capture left %d directories", len(entries))
	}
}
