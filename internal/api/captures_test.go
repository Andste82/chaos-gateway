package api_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/capturemgr"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// M17 over the API: captures on the simulated kernel with the real engine, the real manager of captures and a fake of
// the executor's side that writes real capture files and applies the real filters (internal/executor and
// internal/capture test the AF_PACKET sockets on the loopback interface). Every response is checked against the spec.

const (
	macESP   = "02:00:00:00:00:31"
	macOther = "02:00:00:00:00:aa"
	macGW    = "02:00:00:00:00:01"
)

// capGateway is a gateway with the configured device esp32-42 (10.10.0.31, 02:00:00:00:00:31, on the network IoT, whose
// bridge is br-iot) and a fake clock.
func capGateway(t *testing.T, mods ...func(*options)) (*gw, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	g := newGW(t, append([]func(*options){func(o *options) { o.apiClock = clk }}, mods...)...)
	g.finishSetup()
	id := g.mustPatch(map[string]any{
		"devices": map[string]any{espID: map[string]any{"name": "esp32-42", "network": iotID, "identifiers": map[string]any{"macs": []any{macESP}}}},
		"groups":  map[string]any{"bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb": map[string]any{"name": "sensors", "members": []any{espID}}},
	})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", macESP)})
	g.observe()
	return g, clk
}

// udp builds an Ethernet frame with an IPv4 UDP datagram; tcp one with a TCP segment.
func udpFrame(smac, dmac, sip, dip string, sport, dport uint16, payload int) []byte {
	return ipFrame(smac, dmac, sip, dip, 17, sport, dport, payload)
}

func tcpFrame(smac, dmac, sip, dip string, sport, dport uint16, payload int) []byte {
	return ipFrame(smac, dmac, sip, dip, 6, sport, dport, payload)
}

func ipFrame(smac, dmac, sip, dip string, proto byte, sport, dport uint16, payload int) []byte {
	l4 := make([]byte, 8+payload)
	if proto == 6 {
		l4 = make([]byte, 20+payload)
		l4[12] = 5 << 4
	}
	binary.BigEndian.PutUint16(l4[0:], sport)
	binary.BigEndian.PutUint16(l4[2:], dport)
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(20+len(l4)))
	ip[8], ip[9] = 64, proto
	copy(ip[12:], net.ParseIP(sip).To4())
	copy(ip[16:], net.ParseIP(dip).To4())
	hdr := make([]byte, 14)
	d, _ := net.ParseMAC(dmac)
	s, _ := net.ParseMAC(smac)
	copy(hdr[0:], d)
	copy(hdr[6:], s)
	binary.BigEndian.PutUint16(hdr[12:], 0x0800)
	return append(append(hdr, ip...), l4...)
}

func arpFrame(smac string) []byte {
	f := make([]byte, 60)
	copy(f[0:], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	s, _ := net.ParseMAC(smac)
	copy(f[6:], s)
	f[12], f[13] = 0x08, 0x06
	return f
}

// rawFrame builds a raw IP packet, as a WireGuard interface carries.
func rawPacket(sip, dip string, sport, dport uint16) []byte {
	return udpFrame(macGW, macGW, sip, dip, sport, dport, 20)[14:]
}

func (g *gw) startCapture(body any) resp {
	g.t.Helper()
	return g.do("POST", "/captures", body, nil, nil)
}

func (g *gw) mustStartCapture(body any) map[string]any {
	g.t.Helper()
	r := g.startCapture(body)
	if r.Status != 201 {
		g.t.Fatalf("POST /captures: %d %s", r.Status, r.Body)
	}
	c := r.json(g.t)
	if r.Header.Get("Location") != "/api/v1/captures/"+c["id"].(string) {
		g.t.Errorf("Location %q", r.Header.Get("Location"))
	}
	return c
}

func (g *gw) capture(id string) map[string]any {
	g.t.Helper()
	r := g.do("GET", "/captures/"+id, nil, nil, nil)
	if r.Status != 200 {
		g.t.Fatalf("GET /captures/%s: %d %s", id, r.Status, r.Body)
	}
	return r.json(g.t)
}

func (g *gw) stopCapture(id string) map[string]any {
	g.t.Helper()
	r := g.do("POST", "/captures/"+id+"/stop", nil, nil, nil)
	if r.Status != 200 {
		g.t.Fatalf("stop: %d %s", r.Status, r.Body)
	}
	return r.json(g.t)
}

// download fetches the capture and reads its packets.
func (g *gw) download(id string, query string) (resp, []capture.Packet) {
	g.t.Helper()
	r := g.do("GET", "/captures/"+id+"/download"+query, nil, nil, nil)
	if r.Status != 200 {
		return r, nil
	}
	rd, err := capture.NewReader(bytes.NewReader(r.Body))
	if err != nil {
		g.t.Fatalf("the download is no capture file: %v", err)
	}
	var out []capture.Packet
	for {
		p, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return r, out
		}
		if err != nil {
			g.t.Fatalf("after %d packets: %v", len(out), err)
		}
		out = append(out, p)
	}
}

// frames returns the data of the packets.
func frames(ps []capture.Packet) [][]byte {
	out := make([][]byte, len(ps))
	for i, p := range ps {
		out[i] = p.Data
	}
	return out
}

func sameFrames(t *testing.T, what string, got [][]byte, want ...[]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d packets, want %d", what, len(got), len(want))
		return
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("%s: packet %d is not the frame that was sent", what, i)
		}
	}
}

// ---- the contents

// A capture of a device holds exactly the frames of its Ethernet address in both directions, ARP included, and
// nothing of others; it can be downloaded while it runs and after it has stopped, and deleted.
func TestACaptureOfADeviceHoldsExactlyItsFramesAndCanBeDownloadedWhileItRuns(t *testing.T) {
	g, _ := capGateway(t)
	c := g.mustStartCapture(map[string]any{"name": "esp", "scope": map[string]any{"device": "esp32-42"}})
	id := c["id"].(string)
	if c["status"] != "running" || c["format"] != "pcapng" || c["name"] != "esp" || c["started_at"] == nil {
		t.Fatalf("%v", c)
	}
	ifs := c["interfaces"].([]any)
	if len(ifs) != 1 || ifs[0].(map[string]any)["name"] != "br-iot" || ifs[0].(map[string]any)["link_type"] != "ethernet" || !strings.Contains(ifs[0].(map[string]any)["filter"].(string), macESP) {
		t.Errorf("interfaces %v", ifs)
	}
	o := g.cap.Last()
	if len(o.Interfaces) != 1 || o.Interfaces[0].Name != "br-iot" || len(o.Interfaces[0].Filter.MACs) != 1 || o.Interfaces[0].Filter.MACs[0] != macESP ||
		o.Dir != id || o.Format != capture.PCAPNG || o.MaxSize != 1<<30 || o.RingFiles != 0 {
		t.Errorf("the request to the executor: %+v", o)
	}

	up := tcpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 40000, 8883, 30)
	down := tcpFrame(macGW, macESP, "203.0.113.10", "10.10.0.31", 8883, 40000, 30)
	other := udpFrame(macOther, macGW, "10.10.0.50", "203.0.113.10", 5000, 53, 10)
	otherDown := udpFrame(macGW, macOther, "203.0.113.10", "10.10.0.50", 53, 5000, 10)
	arp := arpFrame(macESP)
	for _, f := range [][]byte{up, other, down, otherDown, arp} {
		g.cap.Frame("br-iot", f, false)
	}
	g.cap.Frame("wan0", up, false) // another interface is none of its business

	// while it runs: the complete records of the files so far
	g.cap.Tick()
	eventuallyTrue(t, "the counters of the capture", func() bool { return g.capture(id)["packets"] == float64(3) })
	r, pkts := g.download(id, "")
	if r.Header.Get("Content-Type") != "application/x-pcapng" || !strings.Contains(r.Header.Get("Content-Disposition"), `filename="esp.pcapng"`) {
		t.Errorf("headers %v", r.Header)
	}
	sameFrames(t, "download while running", frames(pkts), up, down, arp)
	if got := g.capture(id); got["size_bytes"].(float64) <= 0 || got["status"] != "running" {
		t.Errorf("%v", got)
	}

	stopped := g.stopCapture(id)
	if stopped["status"] != "stopped" || stopped["stop_reason"] != "user" || stopped["stopped_at"] == nil || stopped["packets"] != float64(3) {
		t.Fatalf("%v", stopped)
	}
	if g.cap.Running() != 0 {
		t.Error("the executor still runs the capture")
	}
	_, pkts = g.download(id, "")
	sameFrames(t, "download after the stop", frames(pkts), up, down, arp)
	if again := g.stopCapture(id); again["status"] != "stopped" || again["stop_reason"] != "user" {
		t.Errorf("a second stop: %v", again)
	}

	// the capture is listed, and deleted with its files
	l := g.do("GET", "/captures", nil, nil, nil).json(t)["items"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["id"] != id {
		t.Errorf("list %v", l)
	}
	if r := g.do("DELETE", "/captures/"+id, nil, nil, nil); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/captures/"+id, nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("a deleted capture: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(g.cap.dir, id)); !os.IsNotExist(err) {
		t.Errorf("the directory of the capture is still there: %v", err)
	}
}

// A selector records the traffic of its source to its destination, protocol and ports, request and answer.
func TestASelectorCaptureHoldsOnlyTheTrafficItNames(t *testing.T) {
	g, _ := capGateway(t)
	c := g.mustStartCapture(map[string]any{"scope": map[string]any{"selector": map[string]any{
		"source": map[string]any{"group": "sensors"}, "protocol": "tcp", "ports": []int{8883},
		"destination": map[string]any{"cidr": "203.0.113.0/24"}}}})
	id := c["id"].(string)
	f := g.cap.Last().Interfaces[0].Filter
	if f.Protocol != capture.TCP || len(f.Ports) != 1 || f.Ports[0].From != 8883 || len(f.Peers) != 1 || f.Peers[0].String() != "203.0.113.0/24" || len(f.MACs) != 1 {
		t.Errorf("the filter: %+v", f)
	}
	mqtt := tcpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 40000, 8883, 20)
	mqttDown := tcpFrame(macGW, macESP, "203.0.113.10", "10.10.0.31", 8883, 40000, 20)
	for _, fr := range [][]byte{
		mqtt,
		tcpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 40001, 443, 20),    // another port
		udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 40000, 8883, 20),   // another protocol
		tcpFrame(macESP, macGW, "10.10.0.31", "198.51.100.7", 40000, 8883, 20),   // another peer
		tcpFrame(macOther, macGW, "10.10.0.50", "203.0.113.10", 40000, 8883, 20), // another device
		mqttDown,
		arpFrame(macESP), // no IP packet
	} {
		g.cap.Frame("br-iot", fr, false)
	}
	g.stopCapture(id)
	_, pkts := g.download(id, "")
	sameFrames(t, "selector", frames(pkts), mqtt, mqttDown)
}

// The destination `uplink` excludes the networks of the configuration; a network as the destination is its prefixes.
func TestTheDestinationOfASelectorMayBeTheUplinkOrANetwork(t *testing.T) {
	g, _ := capGateway(t)
	g.mustStartCapture(map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"device": "esp32-42"}, "destination": map[string]any{"uplink": true}}}})
	f := g.cap.Last().Interfaces[0].Filter
	has := func(ps []string, want string) bool {
		for _, p := range ps {
			if p == want {
				return true
			}
		}
		return false
	}
	var not []string
	for _, p := range f.NotPeers {
		not = append(not, p.String())
	}
	if len(f.Peers) != 0 || !has(not, "10.10.0.0/24") {
		t.Errorf("uplink: peers %v, not peers %v", f.Peers, not)
	}
	g.mustStartCapture(map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"device": "esp32-42"}, "destination": map[string]any{"network": "IoT"}}}})
	f = g.cap.Last().Interfaces[0].Filter
	if len(f.NotPeers) != 0 || len(f.Peers) != 1 || f.Peers[0].String() != "10.10.0.0/24" {
		t.Errorf("network: %+v", f)
	}
}

// A network is its bridge, a WireGuard network its interface with raw IP packets, the uplink its interface, and
// the selector `global` every test network; a pcap file cannot hold the mix.
func TestNetworksAndTheUplinkAreCapturedOnTheirInterfaces(t *testing.T) {
	g, _ := capGateway(t)
	cases := []struct {
		scope map[string]any
		want  []string
		link  string
	}{
		{map[string]any{"network": "IoT"}, []string{"br-iot"}, "ethernet"},
		{map[string]any{"network": "lab-hub"}, []string{"wg-lab-hub"}, "raw"},
		{map[string]any{"uplink": true}, []string{"wan0"}, "ethernet"},
	}
	for _, c := range cases {
		cp := g.mustStartCapture(map[string]any{"scope": c.scope})
		o := g.cap.Last()
		var names []string
		for _, i := range o.Interfaces {
			names = append(names, i.Name)
			if i.Filter.String() != "all packets" {
				t.Errorf("%v: the filter of %s is %s", c.scope, i.Name, i.Filter.String())
			}
		}
		ifs := cp["interfaces"].([]any)
		if strings.Join(names, ",") != strings.Join(c.want, ",") || ifs[0].(map[string]any)["link_type"] != c.link {
			t.Errorf("%v: interfaces %v (%v)", c.scope, names, ifs)
		}
		g.stopCapture(cp["id"].(string))
	}
	// a WireGuard interface carries raw IP: a capture of it holds the packets as they are
	hub := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "lab-hub"}})
	pkt := rawPacket("10.99.0.2", "10.99.0.1", 5000, 53)
	g.cap.Frame("wg-lab-hub", pkt, false)
	g.stopCapture(hub["id"].(string))
	r, pkts := g.download(hub["id"].(string), "")
	if r.Status != 200 || len(pkts) != 1 || !bytes.Equal(pkts[0].Data, pkt) {
		t.Errorf("the packet of the WireGuard interface: %d %d", r.Status, len(pkts))
	}
	// global spans the bridges and the WireGuard interfaces of the role test, not the management interface
	g.mustStartCapture(map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"global": true}, "protocol": "udp"}}})
	var names []string
	for _, i := range g.cap.Last().Interfaces {
		names = append(names, i.Name)
	}
	if strings.Join(names, ",") != "br-iot,wg-lab-hub,wg-site-b" {
		t.Errorf("global: %v", names)
	}
	r = g.startCapture(map[string]any{"format": "pcap", "scope": map[string]any{"selector": map[string]any{"source": map[string]any{"global": true}}}})
	if r.Status != 422 || r.code(t) != "validation_failed" || !strings.Contains(string(r.Body), `"/format"`) {
		t.Errorf("pcap over Ethernet and raw IP: %d %s", r.Status, r.Body)
	}
}

// A device on a WireGuard network is selected by its addresses.
func TestADeviceOfAWireGuardNetworkIsSelectedByItsAddress(t *testing.T) {
	g, _ := capGateway(t)
	snapDevices := g.e.Snapshot().Devices
	var rA string
	for _, d := range snapDevices {
		if d.Name == "rA" {
			rA = d.ID
		}
	}
	if rA == "" {
		t.Fatalf("the WireGuard client rA is not a device of the gateway: %+v", snapDevices)
	}
	g.mustStartCapture(map[string]any{"scope": map[string]any{"device": rA}})
	o := g.cap.Last()
	if o.Interfaces[0].Name != "wg-lab-hub" || len(o.Interfaces[0].Filter.Hosts) == 0 || len(o.Interfaces[0].Filter.MACs) != 0 {
		t.Errorf("%+v", o.Interfaces)
	}
}

// ---- requests that are refused

func TestACaptureRequestThatDoesNotFitIsRefusedWithItsPointer(t *testing.T) {
	g, _ := capGateway(t)
	g.badRequest = true
	defer func() { g.badRequest = false }()
	for name, c := range map[string]struct {
		body any
		path string
	}{
		"an unknown device":     {map[string]any{"scope": map[string]any{"device": "ghost"}}, "/scope/device"},
		"an unknown network":    {map[string]any{"scope": map[string]any{"network": "ghost"}}, "/scope/network"},
		"an unknown group":      {map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"group": "ghost"}}}}, "/scope/selector/source/group"},
		"a source that is none": {map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"remote_network": map[string]any{"cidr": "10.50.0.0/24"}}}}}, "/scope/selector/source"},
		"a ring and a maximum":  {map[string]any{"scope": map[string]any{"uplink": true}, "max_size": "1MiB", "ring_buffer": map[string]any{"files": 3, "file_size": "1MiB"}}, "/max_size"},
		"a tiny file":           {map[string]any{"scope": map[string]any{"uplink": true}, "max_size": "1KiB"}, "/max_size"},
		"a duration of nothing": {map[string]any{"scope": map[string]any{"uplink": true}, "duration": "0s"}, "/duration"},
		"ports and no protocol": {map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"global": true}, "ports": []int{80}}}}, "/scope/selector/protocol"},
		"no scope":              {map[string]any{"name": "x"}, ""},
		"two scopes":            {map[string]any{"scope": map[string]any{"uplink": true, "network": "IoT"}}, "/scope"},
		"an unknown field":      {map[string]any{"scope": map[string]any{"uplink": true}, "nope": 1}, "/nope"},
	} {
		r := g.startCapture(c.body)
		if r.Status != 422 || r.code(t) != "validation_failed" || !strings.Contains(string(r.Body), `"`+c.path) {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	// a host the gateway's DNS has not answered an address for: nothing to capture
	if r := g.startCapture(map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"global": true}, "destination": map[string]any{"hostname": "broker.example.com"}}}}); r.Status != 422 || r.code(t) != "validation_failed" ||
		!strings.Contains(string(r.Body), "unresolved_hostname") || !strings.Contains(string(r.Body), "/scope/selector/destination/hostname") {
		t.Errorf("a host name: %d %s", r.Status, r.Body)
	}
	// the admin tunnel (a WireGuard network of the role management) is recorded by no scope
	for name, scope := range map[string]map[string]any{
		"the network":                  {"network": "admin"},
		"a selector with it as source": {"selector": map[string]any{"source": map[string]any{"network": "admin"}}},
	} {
		r := g.startCapture(map[string]any{"scope": scope})
		if r.Status != 422 || r.code(t) != "validation_failed" || !strings.Contains(string(r.Body), "management_network") {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if g.cap.Running() != 0 || len(g.cap.Opened()) != 0 {
		t.Errorf("a refused request reached the executor: %+v", g.cap.Opened())
	}
	if entries, _ := os.ReadDir(g.cap.dir); len(entries) != 0 {
		t.Errorf("a refused request left %d directories", len(entries))
	}
}

func TestWhatTheExecutorRefusesComesBackAsTheErrorOfItsKind(t *testing.T) {
	g, _ := capGateway(t)
	for name, c := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"too many captures": {executor.ErrTooManyCaptures, 422, "capacity_exceeded"},
		"a full disk":       {executor.ErrDiskLow, 422, "capacity_exceeded"},
		"no capture volume": {executor.ErrNoCaptureDir, 422, "unsupported_feature"},
		"out of scope":      {executor.ErrOutOfScope, 422, "validation_failed"},
		"a dead executor":   {errors.New("connection refused"), 503, "unavailable"},
	} {
		g.cap.Fail(c.err)
		r := g.startCapture(map[string]any{"scope": map[string]any{"uplink": true}})
		if r.Status != c.status || r.code(t) != c.code {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if entries, _ := os.ReadDir(g.cap.dir); len(entries) != 0 {
		t.Errorf("refused captures left %d directories", len(entries))
	}
}

func TestOnlyEightCapturesRunAtOnce(t *testing.T) {
	g, _ := capGateway(t)
	for i := 0; i < capturemgr.MaxRunning; i++ {
		g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}})
	}
	r := g.startCapture(map[string]any{"scope": map[string]any{"uplink": true}})
	if r.Status != 422 || r.code(t) != "capacity_exceeded" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	items := g.do("GET", "/captures", nil, nil, nil).json(t)["items"].([]any)
	g.stopCapture(items[0].(map[string]any)["id"].(string))
	g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}})
}

func TestACaptureThatRunsCannotBeDeleted(t *testing.T) {
	g, _ := capGateway(t)
	id := g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}})["id"].(string)
	if r := g.do("DELETE", "/captures/"+id, nil, nil, nil); r.Status != 422 || r.code(t) != "validation_failed" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	g.stopCapture(id)
	if r := g.do("DELETE", "/captures/"+id, nil, nil, nil); r.Status != 204 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	for _, p := range []string{"", "/stop", "/download"} {
		method := "GET"
		if p == "/stop" {
			method = "POST"
		}
		if r := g.do(method, "/captures/"+id+p, nil, nil, nil); r.Status != 404 {
			t.Errorf("%s %s: %d", method, p, r.Status)
		}
	}
}

// ---- limits

// A ring buffer keeps the newest files; the download is one file with the complete records of all of them.
func TestARingBufferCaptureKeepsTheNewestFilesAndDownloadsAsOne(t *testing.T) {
	g, _ := capGateway(t)
	c := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}, "ring_buffer": map[string]any{"files": 3, "file_size": "64KiB"}})
	id := c["id"].(string)
	o := g.cap.Last()
	if o.RingFiles != 3 || o.RingFileSize != 64<<10 || o.MaxSize != 0 {
		t.Errorf("%+v", o)
	}
	var last []byte
	for i := 0; i < 400; i++ {
		last = udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", uint16(1000+i), 53, 1000)
		g.cap.Frame("br-iot", last, false)
	}
	g.stopCapture(id)
	_, pkts := g.download(id, "")
	if len(pkts) == 0 || len(pkts) >= 400 || !bytes.Equal(pkts[len(pkts)-1].Data, last) {
		t.Fatalf("%d packets of 400: the ring must keep the newest, and not all", len(pkts))
	}
	entries, _ := os.ReadDir(filepath.Join(g.cap.dir, id))
	files := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "capture-") {
			files++
		}
	}
	if files != 3 {
		t.Errorf("%d files in the ring", files)
	}
}

// The size limit ends a capture with the reason size; the file never passes it.
func TestAMaximumSizeEndsTheCaptureWithTheReasonSize(t *testing.T) {
	g, _ := capGateway(t)
	id := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}, "max_size": "64KiB", "format": "pcap"})["id"].(string)
	if o := g.cap.Last(); o.MaxSize != 64<<10 || o.Format != capture.PCAP {
		t.Errorf("%+v", o)
	}
	for i := 0; i < 300; i++ {
		g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", uint16(1000+i), 53, 400), false)
	}
	eventuallyTrue(t, "the capture stops", func() bool { return g.capture(id)["status"] == "stopped" })
	got := g.capture(id)
	if got["stop_reason"] != "size" || got["size_bytes"].(float64) > 64<<10 || got["size_bytes"].(float64) < 60<<10 {
		t.Errorf("%v", got)
	}
	r, pkts := g.download(id, "")
	if r.Header.Get("Content-Type") != "application/vnd.tcpdump.pcap" || !strings.HasSuffix(r.Header.Get("Content-Disposition"), `.pcap"`) || len(pkts) == 0 || len(r.Body) > 64<<10 {
		t.Errorf("%v: %d packets, %d bytes", r.Header, len(pkts), len(r.Body))
	}
}

// A capture without any limit stops at 1 GiB, and the duration is passed on to the executor.
func TestTheDefaultLimitAndTheDurationReachTheExecutor(t *testing.T) {
	g, _ := capGateway(t)
	g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}, "duration": "90s", "snaplen": 128})
	o := g.cap.Last()
	if o.MaxSize != 1<<30 || o.DurationMS != 90_000 || o.Snaplen != 128 {
		t.Errorf("%+v", o)
	}
	id := g.cap.streams()[0].key
	g.cap.End("duration", "")
	eventuallyTrue(t, "the capture ends", func() bool { return g.capture(id)["status"] == "stopped" })
	if got := g.capture(id); got["stop_reason"] != "duration" {
		t.Errorf("%v", got)
	}
}

// Quota enforcement (plan §3.6): the oldest stopped captures are deleted to make room for a new one, a new capture
// cannot take more than the room that is left, and a running capture that makes the total pass the quota ends with
// the reason size.
func TestTheQuotaDeletesTheOldestStoppedCapturesAndLimitsTheRunningOnes(t *testing.T) {
	g, clk := capGateway(t, func(o *options) { o.captureQuota = 300 << 10 })
	fill := func(id string, n int) {
		for i := 0; i < n; i++ {
			g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", uint16(1000+i), 53, 900), false)
		}
		g.stopCapture(id)
	}
	// two captures of about 130 KiB each
	var ids []string
	for i := 0; i < 2; i++ {
		clk.Advance(time.Second)
		id := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}, "max_size": "130KiB"})["id"].(string)
		fill(id, 200)
		ids = append(ids, id)
	}
	var second float64
	for i, id := range ids {
		s := g.capture(id)["size_bytes"].(float64)
		if s < 120<<10 || s > 130<<10 {
			t.Fatalf("capture %s has %v bytes", id, s)
		}
		if i == 1 {
			second = s
		}
	}
	// a third needs room: 260 KiB are used, the quota is 300 KiB, a capture needs 64 KiB at the least. The oldest goes,
	// the newer stays, and the third gets the space that is left
	clk.Advance(time.Second)
	third := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}, "max_size": "200KiB"})["id"].(string)
	if r := g.do("GET", "/captures/"+ids[0], nil, nil, nil); r.Status != 404 {
		t.Errorf("the oldest capture is not deleted: %d", r.Status)
	}
	if _, err := os.Stat(filepath.Join(g.cap.dir, ids[0])); !os.IsNotExist(err) {
		t.Errorf("its files are still there: %v", err)
	}
	if r := g.do("GET", "/captures/"+ids[1], nil, nil, nil); r.Status != 200 {
		t.Errorf("the newer capture is gone: %d", r.Status)
	}
	if o := g.cap.Last(); o.MaxSize != 300<<10-int64(second) {
		t.Errorf("the executor was given a maximum of %d bytes, the space left is %d", o.MaxSize, 300<<10-int64(second))
	}
	// the limit is the room that is left: the capture ends with the reason size
	for i := 0; i < 400; i++ {
		g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", uint16(1000+i), 53, 900), false)
	}
	eventuallyTrue(t, "the third capture ends", func() bool { return g.capture(third)["status"] == "stopped" })
	got := g.capture(third)
	if got["stop_reason"] != "size" || !strings.Contains(got["message"].(string), "quota") {
		t.Errorf("%v", got)
	}
	var total float64
	for _, it := range g.do("GET", "/captures", nil, nil, nil).json(t)["items"].([]any) {
		total += it.(map[string]any)["size_bytes"].(float64)
	}
	if total > 300<<10 {
		t.Errorf("the captures take %v bytes, more than the quota of %d", total, 300<<10)
	}
	// a ring buffer or a maximum size above the quota itself is no use at all
	g.badRequest = true
	defer func() { g.badRequest = false }()
	for _, body := range []map[string]any{
		{"scope": map[string]any{"uplink": true}, "max_size": "1GiB"},
		{"scope": map[string]any{"uplink": true}, "ring_buffer": map[string]any{"files": 10, "file_size": "64MiB"}},
	} {
		if r := g.startCapture(body); r.Status != 422 || r.code(t) != "validation_failed" || !strings.Contains(string(r.Body), "quota") {
			t.Errorf("%v: %d %s", body, r.Status, r.Body)
		}
	}
}

// Two captures that run at once may together pass the quota: the one that makes the total pass it ends with the
// reason size, and keeps what it recorded; the other goes on.
func TestRunningCapturesThatTogetherPassTheQuotaAreStopped(t *testing.T) {
	g, _ := capGateway(t, func(o *options) { o.captureQuota = 200 << 10 })
	a := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}, "max_size": "150KiB"})["id"].(string)
	b := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}, "max_size": "150KiB"})["id"].(string)
	for i := 0; i < 130; i++ {
		g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", uint16(1000+i), 53, 900), false)
	}
	g.cap.Tick()
	eventuallyTrue(t, "one of the captures is stopped by the quota", func() bool {
		return g.capture(a)["status"] == "stopped" || g.capture(b)["status"] == "stopped"
	})
	running := 0
	for _, id := range []string{a, b} {
		got := g.capture(id)
		switch got["status"] {
		case "running":
			running++
		case "stopped":
			if got["stop_reason"] != "size" || !strings.Contains(got["message"].(string), "quota") || got["size_bytes"].(float64) < 100<<10 {
				t.Errorf("%v", got)
			}
			if r, pkts := g.download(id, ""); r.Status != 200 || len(pkts) < 100 {
				t.Errorf("what the stopped capture recorded: %d, %d packets", r.Status, len(pkts))
			}
		}
	}
	if running != 1 {
		t.Errorf("%d captures still run, want the other one", running)
	}
}

// The quota is a setting of the configuration (settings.retention.captures_quota).
func TestTheQuotaIsASettingOfTheConfiguration(t *testing.T) {
	g, _ := capGateway(t)
	id := g.mustPatch(map[string]any{"settings": map[string]any{"retention": map[string]any{"captures_quota": "200KiB"}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}})
	if o := g.cap.Last(); o.MaxSize != 200<<10 {
		t.Errorf("the default limit is the quota of the configuration, not 1 GiB: %d", o.MaxSize)
	}
}

// ---- the disk

// Disk low (plan §3.6): a capture is refused below 10 % free space; a running one stops with the reason disk_low,
// and the event says so.
func TestALowDiskRefusesNewCapturesAndStopsRunningOnes(t *testing.T) {
	g, _ := capGateway(t)
	sub, cancel := g.e.Subscribe()
	defer cancel()
	id := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}})["id"].(string)
	g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 1, 53, 10), false)
	g.cap.freePc.Store(4)
	g.cap.End("disk_low", "")
	eventuallyTrue(t, "the capture stops", func() bool { return g.capture(id)["status"] == "stopped" })
	got := g.capture(id)
	if got["stop_reason"] != "disk_low" || !strings.Contains(got["message"].(string), "10 %") || got["packets"] != float64(1) {
		t.Errorf("%v", got)
	}
	if r, pkts := g.download(id, ""); r.Status != 200 || len(pkts) != 1 {
		t.Errorf("what was recorded stays: %d, %d packets", r.Status, len(pkts))
	}
	r := g.startCapture(map[string]any{"scope": map[string]any{"uplink": true}})
	if r.Status != 422 || r.code(t) != "capacity_exceeded" || !strings.Contains(string(r.Body), "free space") {
		t.Errorf("a capture on a full disk: %d %s", r.Status, r.Body)
	}
	if len(g.cap.Opened()) != 1 {
		t.Errorf("the refused capture reached the executor: %d requests", len(g.cap.Opened()))
	}
	types := map[string]map[string]any{}
loop:
	for {
		select {
		case ev := <-sub:
			types[ev.Type] = ev.Data
		default:
			break loop
		}
	}
	if types["capture_started"] == nil || types["capture_stopped"]["reason"] != "disk_low" || types["disk_low"] == nil {
		t.Errorf("events %v", types)
	}
	// with space again, it works
	g.cap.freePc.Store(60)
	g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}})
}

// M23a: the screen of the captures shows what the captures take of the quota and of the volume. The numbers are the
// quota of the configuration, the sum of the sizes of the captures (a running one as the executor last reported it), the
// free space of the volume against the limit and the captures that run against the most that run at once.
func TestTheStorageOfTheCapturesIsTheQuotaTheSizesAndTheFreeSpace(t *testing.T) {
	g, _ := capGateway(t, func(o *options) { o.captureQuota = 300 << 10 })
	storage := func() map[string]any {
		t.Helper()
		r := g.do("GET", "/captures/storage", nil, nil, nil)
		if r.Status != 200 {
			t.Fatalf("GET /captures/storage: %d %s", r.Status, r.Body)
		}
		return r.json(t)
	}
	s := storage()
	if s["quota_bytes"] != float64(300<<10) || s["used_bytes"] != float64(0) || s["running"] != float64(0) || s["max_running"] != float64(capturemgr.MaxRunning) ||
		s["disk_low"] != false || s["min_free_percent"] != float64(10) || s["free_percent"] != float64(60) || s["volume_total_bytes"] != float64(1000) {
		t.Fatalf("an empty volume: %v", s)
	}
	id := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}})["id"].(string)
	for i := 0; i < 20; i++ {
		g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", uint16(1000+i), 53, 900), false)
	}
	if s := storage(); s["running"] != float64(1) {
		t.Errorf("a running capture: %v", s)
	}
	size := g.stopCapture(id)["size_bytes"].(float64)
	if size < 18<<10 {
		t.Fatalf("the capture has %v bytes", size)
	}
	if s := storage(); s["used_bytes"] != size || s["running"] != float64(0) {
		t.Errorf("a stopped capture of %v bytes: %v", size, s)
	}
	// the free space against the limit: below 10 % the volume is low
	g.cap.freePc.Store(4)
	if s := storage(); s["disk_low"] != true || s["free_percent"] != float64(4) {
		t.Errorf("a full volume: %v", s)
	}
	g.cap.freePc.Store(10)
	if s := storage(); s["disk_low"] != false {
		t.Errorf("exactly at the limit is not below it: %v", s)
	}
	// deleting the capture gives its size back
	if r := g.do("DELETE", "/captures/"+id, nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d", r.Status)
	}
	if s := storage(); s["used_bytes"] != float64(0) {
		t.Errorf("after the delete: %v", s)
	}
	// the token that only reads may read it
	g.token = ""
	g.mintToken("read")
	if r := g.do("GET", "/captures/storage", nil, nil, nil); r.Status != 200 {
		t.Errorf("read: %d", r.Status)
	}
}

// ---- tokens, idempotency, restarts, no volume

func TestCapturesFollowTheScopesOfTokens(t *testing.T) {
	g, _ := capGateway(t)
	admin := g.token
	id := g.mustStartCapture(map[string]any{"scope": map[string]any{"uplink": true}})["id"].(string)
	g.cap.Frame("wan0", udpFrame(macGW, macGW, "203.0.113.1", "203.0.113.10", 1, 2, 5), false)
	g.stopCapture(id)

	g.token = ""
	g.mintToken("read")
	if r := g.do("GET", "/captures", nil, nil, nil); r.Status != 200 {
		t.Errorf("list with read: %d", r.Status)
	}
	if r, pkts := g.download(id, ""); r.Status != 200 || len(pkts) != 1 {
		t.Errorf("download with read: %d", r.Status)
	}
	g.badRequest = true
	for _, c := range []struct{ method, path string }{{"POST", "/captures"}, {"POST", "/captures/" + id + "/stop"}, {"DELETE", "/captures/" + id}} {
		var body any
		if c.method == "POST" && c.path == "/captures" {
			body = map[string]any{"scope": map[string]any{"uplink": true}}
		}
		if r := g.do(c.method, c.path, body, nil, nil); r.Status != 403 {
			t.Errorf("%s %s with read: %d", c.method, c.path, r.Status)
		}
	}
	g.badRequest = false

	g.token = ""
	g.mintToken("overlays")
	if r := g.startCapture(map[string]any{"scope": map[string]any{"uplink": true}}); r.Status != 201 {
		t.Errorf("start with overlays: %d %s", r.Status, r.Body)
	}
	// the key log is for the scope full
	if r := g.do("GET", "/captures/"+id+"/download?part=keylog", nil, nil, nil); r.Status != 403 {
		t.Errorf("key log with overlays: %d %s", r.Status, r.Body)
	}
	g.token = admin
	if r := g.do("GET", "/captures/"+id+"/download?part=keylog", nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("a capture has no key log before M22: %d %s", r.Status, r.Body)
	}
}

func TestAnIdempotencyKeyMakesOneCapture(t *testing.T) {
	g, _ := capGateway(t)
	hdr := map[string]string{"Idempotency-Key": "capture-1"}
	body := map[string]any{"scope": map[string]any{"uplink": true}}
	a := g.do("POST", "/captures", body, hdr, nil)
	b := g.do("POST", "/captures", body, hdr, nil)
	if a.Status != 201 || b.Status != 201 || a.json(t)["id"] != b.json(t)["id"] || len(g.cap.Opened()) != 1 {
		t.Errorf("%d %d, %d requests to the executor", a.Status, b.Status, len(g.cap.Opened()))
	}
	g.badRequest = true
	defer func() { g.badRequest = false }()
	if r := g.do("POST", "/captures", map[string]any{"scope": map[string]any{"network": "IoT"}}, hdr, nil); r.Status != 422 || r.code(t) != "idempotency_conflict" {
		t.Errorf("the key with another request: %d %s", r.Status, r.Body)
	}
}

// A capture of an earlier process ended with it: after a restart it is failed, with the files it left.
func TestARestartEndsACaptureThatWasRunningAsFailed(t *testing.T) {
	root := t.TempDir()
	g, _ := capGateway(t, func(o *options) { o.root = root })
	id := g.mustStartCapture(map[string]any{"scope": map[string]any{"network": "IoT"}})["id"].(string)
	g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 1, 2, 5), false)
	// the process ends without stopping it: the executor's side closes the file, the record says running
	g.cap.streams()[0].end("error", "closed")
	eventuallyTrue(t, "the capture is failed", func() bool { return g.capture(id)["status"] == "failed" })
	dir := filepath.Join(root, "captures", id)
	// the API shows the end before the record is written: the record is changed back to running only once it says failed,
	// or the write that is still to come puts failed over the change
	var rec []byte
	eventuallyTrue(t, "the record says failed", func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "capture.json"))
		rec = b
		return err == nil && strings.Contains(string(b), `"status": "failed"`)
	})
	running := strings.Replace(string(rec), `"status": "stopped"`, `"status": "running"`, 1)
	running = strings.Replace(running, `"status": "failed"`, `"status": "running"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "capture.json"), []byte(running), 0o640); err != nil {
		t.Fatal(err)
	}
	g.close()

	g2 := newGW(t, func(o *options) { o.root = root })
	g2.token = ""
	g2.mintToken("full")
	got := g2.capture(id)
	if got["status"] != "failed" || got["stop_reason"] != "error" || !strings.Contains(got["message"].(string), "restarted") || got["stopped_at"] == nil {
		t.Fatalf("%v", got)
	}
	if r, pkts := g2.download(id, ""); r.Status != 200 || len(pkts) != 1 {
		t.Errorf("the file the capture left: %d, %d packets", r.Status, len(pkts))
	}
}

// A gateway whose executor has no capture volume answers the operations of captures with unsupported_feature, and its
// capabilities say that it cannot record.
func TestADeploymentWithoutACaptureVolumeCannotCapture(t *testing.T) {
	g := newGW(t, func(o *options) { o.noCaptures = true })
	g.finishSetup()
	g.badRequest = true
	defer func() { g.badRequest = false }()
	for _, c := range []struct{ method, path string }{{"GET", "/captures"}, {"GET", "/captures/storage"}, {"POST", "/captures"}, {"GET", "/captures/" + espID}, {"DELETE", "/captures/" + espID}, {"GET", "/captures/" + espID + "/download"}} {
		var body any
		if c.method == "POST" {
			body = map[string]any{"scope": map[string]any{"uplink": true}}
		}
		if r := g.do(c.method, c.path, body, nil, nil); r.Status != 422 || r.code(t) != "unsupported_feature" || !strings.Contains(string(r.Body), "capture volume") {
			t.Errorf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	if strings.Contains(strings.Join(toStrings(caps["step_types"].([]any)), ","), "capture") || strings.Contains(strings.Join(toStrings(caps["features"].([]any)), ","), "captures") {
		t.Errorf("capabilities %v", caps)
	}
	// a scenario that records is refused instead of run without its capture
	for _, sc := range []map[string]any{
		{"name": "c", "target": map[string]any{"network": "IoT"}, "capture": true, "steps": []any{step("a", "0s", "fault", map[string]any{"latency": "10ms"})}},
		{"name": "c", "target": map[string]any{"network": "IoT"}, "steps": []any{step("a", "0s", "capture", "start")}},
	} {
		r := g.postRun(map[string]any{"inline": sc}, "")
		if r.Status != 422 || r.code(t) != "unsupported_feature" || !strings.Contains(string(r.Body), "capture volume") {
			t.Errorf("%d %s", r.Status, r.Body)
		}
	}
}

// A selector that names a host captures the traffic to the addresses the gateway's DNS answered for it (plan §2.6, M20):
// those of the name asked, and of the names of its CNAME chain.
func TestTheHostOfASelectorIsTheAddressesTheGatewaysDNSAnsweredForIt(t *testing.T) {
	g, _ := capGateway(t)
	admin := g.token
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	now := time.Now().UTC().Truncate(time.Second)
	entry := func(name string, answers ...any) map[string]any {
		return map[string]any{"time": now.Format(time.RFC3339), "client": "10.10.0.31", "name": name, "type": "A", "rcode": "NOERROR", "protocol": "udp", "answers": answers}
	}
	g.token = strings.TrimSpace(string(raw))
	if r := g.do("POST", "/internal/dns/queries", []any{
		entry("broker.example.com", "203.0.113.10", "203.0.113.11"),
		entry("www.example.net", "edge.cdn.example.org", "198.51.100.20"),
		entry("other.example.com", "198.51.100.99"),
	}, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.token = admin
	body := func(host string) map[string]any {
		return map[string]any{"scope": map[string]any{"selector": map[string]any{"source": map[string]any{"global": true}, "destination": map[string]any{"hostname": host}}}}
	}
	g.mustStartCapture(body("broker.example.com"))
	var got []string
	for _, p := range g.cap.Last().Interfaces[0].Filter.Peers {
		got = append(got, p.String())
	}
	if strings.Join(got, ",") != "203.0.113.10/32,203.0.113.11/32" {
		t.Errorf("the peers of the exact name: %v", got)
	}
	g.mustStartCapture(body("*.cdn.example.org")) // a name of a CNAME chain
	got = nil
	for _, p := range g.cap.Last().Interfaces[0].Filter.Peers {
		got = append(got, p.String())
	}
	if strings.Join(got, ",") != "198.51.100.20/32" {
		t.Errorf("the peers of the suffix of a CNAME target: %v", got)
	}
}
