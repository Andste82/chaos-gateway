//go:build testbed

package api_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M17 on a real kernel, over the API: the executor captures with AF_PACKET sockets in the namespace of the gateway.
// The ground truth of each test is a capture on the interface of the device itself (spike S9: "exact against a
// capture on the device itself"). For a device, a selector and a run it is made by tcpdump in the device's namespace,
// a program that shares no code with the capture engine, with a filter written by hand in the tcpdump language; for a
// WireGuard interface it is a session of the engine itself. Wireshark's tshark reads the files the API serves.

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

// tcpdumpTruth is a capture by `tcpdump -w` in a namespace. tcpdump shares no code with the engine under test (the
// AF_PACKET reader, the generator of the BPF filter, the writers and the reader of the files), so a fault of one of them
// cannot cancel out against itself the way it does when a second session of the engine is the ground truth. The filter
// is an expression in the tcpdump language that is written by hand, not generated from the Filter that is tested.
type tcpdumpTruth struct {
	t      *testing.T
	cmd    *exec.Cmd
	log    syncBuffer
	path   string
	done   chan error
	exited bool
}

// startTcpdump starts tcpdump on the interface of the namespace and returns when it listens: the packets that come
// after that are in its file.
func startTcpdump(t *testing.T, ns *testbed.Namespace, iface, expr string) *tcpdumpTruth {
	t.Helper()
	if _, err := exec.LookPath("tcpdump"); err != nil {
		t.Fatalf("the ground truth of the capture tests is tcpdump, which is not installed (the devcontainer image has it): %v", err)
	}
	c := &tcpdumpTruth{t: t, path: filepath.Join(t.TempDir(), "truth.pcap"), done: make(chan error, 1)}
	// -Z root: tcpdump drops to the user "tcpdump" after it opened the socket, which may not write to the directory of
	// the test; -U and --immediate-mode hand every packet on at once
	c.cmd = ns.Command(context.Background(), "tcpdump", "-n", "-i", iface, "-U", "-s", "0", "-Z", "root", "--immediate-mode", "-w", c.path, expr)
	c.cmd.Stdout, c.cmd.Stderr = &c.log, &c.log
	c.cmd.WaitDelay = 5 * time.Second
	if err := c.cmd.Start(); err != nil {
		t.Fatalf("start tcpdump in %s: %v", ns.Short, err)
	}
	go func() { c.done <- c.cmd.Wait() }()
	t.Cleanup(c.kill)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(c.log.String(), "listening on") {
		select {
		case err := <-c.done:
			c.exited = true
			t.Fatalf("tcpdump on %s of %s ended before it listened: %v\n%s", iface, ns.Short, err, c.log.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("tcpdump on %s of %s does not listen after 10 s:\n%s", iface, ns.Short, c.log.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return c
}

func (c *tcpdumpTruth) kill() {
	if c.exited {
		return
	}
	c.exited = true
	_ = c.cmd.Process.Kill()
	<-c.done
}

// stop ends tcpdump the way a user does (SIGINT, so that it writes what it has) and returns the packets of its file.
func (c *tcpdumpTruth) stop() []capture.Packet {
	c.t.Helper()
	time.Sleep(300 * time.Millisecond) // the last packets reach tcpdump
	_ = c.cmd.Process.Signal(syscall.SIGINT)
	select {
	case err := <-c.done:
		c.exited = true
		if err != nil {
			c.t.Fatalf("tcpdump ended with an error: %v\n%s", err, c.log.String())
		}
	case <-time.After(10 * time.Second):
		c.kill()
		c.t.Fatalf("tcpdump does not end after SIGINT:\n%s", c.log.String())
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		c.t.Fatalf("the file of tcpdump: %v\n%s", err, c.log.String())
	}
	return readPackets(c.t, b)
}

// tsharkFields runs tshark over a capture file and returns the value of a field for each packet that passes the display
// filter. A file that tshark rejects, or any other failure of tshark, fails the test with tshark's own message; only
// a missing tshark skips it (the devcontainer image has one).
func tsharkFields(t *testing.T, file []byte, display, field string) []string {
	t.Helper()
	exe, err := exec.LookPath("tshark")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			t.Skip("tshark is not installed: the file was not checked with Wireshark's dissector")
		}
		t.Fatalf("tshark: %v", err)
	}
	args := []string{"-r", writeTemp(t, file), "-T", "fields", "-e", field}
	if display != "" {
		args = append(args, "-Y", display)
	}
	cmd := exec.Command(exe, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("tshark does not read the capture file: %v\n%s", err, stderr.String())
	}
	return strings.Fields(string(out))
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
		onlyGot, onlyWant := multisetDiff(got, want)
		t.Errorf("%s: %d packets, the capture on the device itself has %d\nonly in the capture:\n%s\nonly on the device:\n%s",
			what, len(got), len(want), frameLines(onlyGot), frameLines(onlyWant))
		return
	}
	for i := range want {
		if got[i] != want[i] {
			onlyGot, onlyWant := multisetDiff(got, want)
			t.Errorf("%s: packet %d of the sorted captures differs from the one of the device\nonly in the capture:\n%s\nonly on the device:\n%s",
				what, i, frameLines(onlyGot), frameLines(onlyWant))
			return
		}
	}
}

// multisetDiff returns the frames that one of two sorted multisets holds more often than the other.
func multisetDiff(got, want []string) (onlyGot, onlyWant []string) {
	count := map[string]int{}
	for _, f := range got {
		count[f]++
	}
	for _, f := range want {
		count[f]--
	}
	for f, n := range count {
		for ; n > 0; n-- {
			onlyGot = append(onlyGot, f)
		}
		for ; n < 0; n++ {
			onlyWant = append(onlyWant, f)
		}
	}
	sort.Strings(onlyGot)
	sort.Strings(onlyWant)
	return onlyGot, onlyWant
}

func frameLines(frames []string) string {
	if len(frames) == 0 {
		return "  (none)"
	}
	var b strings.Builder
	for i, f := range frames {
		if i == 20 {
			fmt.Fprintf(&b, "  and %d more\n", len(frames)-i)
			break
		}
		fmt.Fprintf(&b, "  %s\n", frameSummary([]byte(f)))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// frameSummary says what a frame is in a line: the addresses and ports of an IPv4 packet, the operation of an ARP
// packet, and the first bytes of anything else.
func frameSummary(b []byte) string {
	if len(b) < 14 {
		return fmt.Sprintf("short frame % x", b)
	}
	eth := fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x > %02x:%02x:%02x:%02x:%02x:%02x", b[6], b[7], b[8], b[9], b[10], b[11], b[0], b[1], b[2], b[3], b[4], b[5])
	switch binary.BigEndian.Uint16(b[12:14]) {
	case 0x0806:
		if len(b) >= 42 {
			return fmt.Sprintf("%s ARP op %d, sender %d.%d.%d.%d, target %d.%d.%d.%d", eth, b[21], b[28], b[29], b[30], b[31], b[38], b[39], b[40], b[41])
		}
	case 0x0800:
		if len(b) >= 34 {
			ip := fmt.Sprintf("%s IPv4 %d.%d.%d.%d > %d.%d.%d.%d proto %d, %d bytes", eth, b[26], b[27], b[28], b[29], b[30], b[31], b[32], b[33], b[23], len(b))
			if (b[23] == 6 || b[23] == 17) && len(b) >= 38 {
				ip += fmt.Sprintf(", ports %d > %d", binary.BigEndian.Uint16(b[34:36]), binary.BigEndian.Uint16(b[36:38]))
				if b[23] == 6 && len(b) >= 48 {
					ip += fmt.Sprintf(", tcp flags %#02x", b[47])
				}
			}
			return ip
		}
	}
	n := min(len(b), 48)
	return fmt.Sprintf("%s ethertype %#04x, %d bytes: % x", eth, binary.BigEndian.Uint16(b[12:14]), len(b), b[:n])
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
	truth := startTcpdump(t, top.A, "eth0", "ether host "+strings.ToLower(testbed.ClientAMAC))
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
	// Wireshark's dissector reads every packet of the file the API serves
	if n := len(tsharkFields(t, r.Body, "", "frame.number")); n != len(pkts) {
		t.Errorf("tshark reads %d packets of the download, which holds %d", n, len(pkts))
	}
}

// A selector holds the packets of its source to its destination, protocol and port: the TCP connections of A to the
// broker, from the SYN to the FIN, in both directions.
func TestASelectorCaptureHoldsTheConnectionsItNamesOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	truth := startTcpdump(t, top.A, "eth0", "ether host "+strings.ToLower(testbed.ClientAMAC)+" and tcp port 8883")
	c := g.mustStartCapture(map[string]any{"scope": map[string]any{"selector": map[string]any{
		"source": map[string]any{"device": clientA}, "protocol": "tcp", "ports": []int{8883}, "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}}})
	makeTraffic(t, top)
	g.stopCapture(c["id"].(string))
	want := multiset(truth.stop(), ipOrARP)
	dl, pkts := g.download(c["id"].(string), "")
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
	// Wireshark finds exactly these packets in the file by their port
	if n := len(tsharkFields(t, dl.Body, "tcp.port == 8883", "frame.number")); n != len(pkts) {
		t.Errorf("tshark finds %d packets of port 8883 in the download, which holds %d", n, len(pkts))
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
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	// the interface blocks of a pcapng file are read with the first packet: the reader knows no interface before
	if _, err := rd.Next(); err != nil {
		t.Fatalf("the first packet of the file: %v", err)
	}
	if ifs := rd.Interfaces(); len(ifs) != 1 || ifs[0].Link != capture.LinkRaw || ifs[0].Name != "wg-lab-hub" {
		t.Errorf("interfaces of the file: %+v", ifs)
	}
	sameMultiset(t, "WireGuard capture", multiset(pkts, icmp), want)
	// Wireshark's dissector finds the ping of the client in the file the API serves
	if src := tsharkFields(t, r.Body, "icmp", "ip.src"); !slices.Contains(src, "10.99.0.4") {
		t.Errorf("Wireshark does not find the ping of the client in the file: %q", src)
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

// captureWarnings are the codes of the warnings of a run that say something about its captures or its steps. A
// step_late says that the engine was busy with another change when the step came (it waited more than 100 ms, which
// the identity update after a first sighting of the device or a slow apply in a virtual machine does) and is no
// statement about the capture.
func captureWarnings(run map[string]any) []string {
	var out []string
	for _, c := range warningCodes(run) {
		if c != "step_late" {
			out = append(out, c)
		}
	}
	return out
}

// A run with `capture: true` records its target for the whole run on the real kernel and offers the file in its
// report.
func TestARunWithCaptureTrueAttachesTheTrafficOfItsTargetOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	body := inlineRun("recorded", map[string]any{"device": clientA},
		step("normal", "0s", "profile", "normal"),
		step("end", "6s", "restore", true))
	body["inline"].(map[string]any)["capture"] = true
	// the ground truth runs from before the run to after its end, so that it holds everything the capture of the run
	// holds, and the traffic of the test is all that either has (nothing else of the device is on the wire for those
	// seconds)
	truth := startTcpdump(t, top.A, "eth0", "ether host "+strings.ToLower(testbed.ClientAMAC))
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "normal", "applied")
	makeTraffic(t, top)
	run := g.waitRun(id, "passed", "failed", "error")
	want := multiset(truth.stop(), ipOrARP)
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
	if len(captureWarnings(run)) != 0 {
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
	if codes := captureWarnings(run); len(codes) != 1 || codes[0] != "capture_missing" {
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
