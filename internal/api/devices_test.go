package api_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

const espID = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"

// dhcpGateway is a gateway whose IoT network has DHCP and a configured device with a MAC.
func dhcpGateway(t *testing.T) *gw {
	t.Helper()
	g := ready(t)
	id := g.mustPatch(map[string]any{
		"networks": map[string]any{iotID: map[string]any{"dhcp": map[string]any{"lease_time": "10m"}}},
		"devices":  map[string]any{espID: map[string]any{"name": "esp32-42", "network": iotID, "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:31"}}}},
		"groups":   map[string]any{"c0a80001-0000-4000-8000-000000000001": map[string]any{"name": "sensors", "members": []any{"esp32-42"}}},
	})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	return g
}

func (g *gw) observe() {
	g.t.Helper()
	ctx, cancel := context_(10 * time.Second)
	defer cancel()
	if err := g.e.ObserveNow(ctx); err != nil {
		g.t.Fatal(err)
	}
}

func nb(ip, mac string) linux.Neighbor {
	return linux.Neighbor{Dst: ip, Dev: "br-iot", LLAddr: mac, State: []string{"REACHABLE"}}
}

func TestDevicesAreConfiguredOrDiscoveredWithTheirObservedState(t *testing.T) {
	g := dhcpGateway(t)
	g.observe()
	// before anything is seen: the configured device is known and offline
	l := g.do("GET", "/devices", nil, nil, nil).json(t)["items"].([]any)
	var names []string
	for _, d := range l {
		names = append(names, d.(map[string]any)["name"].(string))
	}
	if !strings.Contains(strings.Join(names, ","), "esp32-42") {
		t.Fatalf("%v", names)
	}
	d := g.do("GET", "/devices/esp32-42", nil, nil, nil).json(t)
	if d["id"] != espID || d["origin"] != "configured" || d["observed"].(map[string]any)["online"] != false || d["network"] != iotID || d["config"] == nil {
		t.Fatalf("%v", d)
	}
	if grp, _ := d["groups"].([]any); len(grp) != 1 {
		t.Errorf("group membership: %v", d["groups"])
	}

	// the neighbor table shows the configured device and an unknown one
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31"), nb("10.10.0.50", "02:00:00:00:00:aa")})
	g.observe()
	d = g.do("GET", "/devices/"+espID, nil, nil, nil).json(t)
	obs := d["observed"].(map[string]any)
	if obs["online"] != true || obs["addresses"].([]any)[0] != "10.10.0.31" || obs["macs"].([]any)[0] != "02:00:00:00:00:31" || obs["last_seen"] == nil {
		t.Errorf("%v", obs)
	}
	disc := g.do("GET", "/devices?origin=discovered", nil, nil, nil).json(t)["items"].([]any)
	if len(disc) != 1 {
		t.Fatalf("%v", disc)
	}
	dv := disc[0].(map[string]any)
	if dv["name"] != "dev-02-00-00-00-00-aa" || dv["origin"] != "discovered" || dv["config"] != nil || dv["network"] != iotID || dv["id"] != engine.DeviceID("02:00:00:00:00:aa") {
		t.Errorf("%v", dv)
	}
	// a discovered device can be fetched by id and by its generated name
	for _, ref := range []string{dv["id"].(string), "dev-02-00-00-00-00-aa"} {
		if r := g.do("GET", "/devices/"+ref, nil, nil, nil); r.Status != 200 {
			t.Errorf("%s: %d", ref, r.Status)
		}
	}
	// filters
	if on := g.do("GET", "/devices?online=true", nil, nil, nil).json(t)["items"].([]any); len(on) != 2 {
		t.Errorf("%d online", len(on))
	}
	if n := g.do("GET", "/devices?network=IoT&origin=configured", nil, nil, nil).json(t)["items"].([]any); len(n) != 1 {
		t.Errorf("%v", n)
	}
	if r := g.do("GET", "/devices?network=ghost", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
	if r := g.do("GET", "/devices/ghost", nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("%d", r.Status)
	}
	// pagination
	p1 := g.do("GET", "/devices?limit=1", nil, nil, nil).json(t)
	if len(p1["items"].([]any)) != 1 || p1["next_cursor"] == nil {
		t.Errorf("%v", p1)
	}
}

func TestLeasesOfANetworkAndTheirDevices(t *testing.T) {
	g := dhcpGateway(t)
	sid := 0
	for id := range g.e.Snapshot().KeaNetworks {
		sid = id
	}
	if sid == 0 {
		t.Fatal("no Kea subnet")
	}
	g.dhcp.set(
		kea.Lease{IP: "10.10.0.151", MAC: "02:00:00:00:00:aa", SubnetID: sid, ValidLft: 600, CLTT: time.Now().Unix()},
		kea.Lease{IP: "10.10.0.150", MAC: "02:00:00:00:00:31", SubnetID: sid, ValidLft: 600, CLTT: time.Now().Unix(), Hostname: "esp32-42"},
	)
	g.observe()
	items := g.do("GET", "/networks/IoT/leases", nil, nil, nil).json(t)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("%v", items)
	}
	first := items[0].(map[string]any)
	if first["ip"] != "10.10.0.150" || first["device"] != espID || first["hostname"] != "esp32-42" || first["network"] != iotID || first["state"] != "active" {
		t.Errorf("%v", first)
	}
	if r := g.do("GET", "/networks/lab-hub/leases", nil, nil, nil); r.Status != 404 {
		t.Errorf("a WireGuard network has no leases: %d", r.Status)
	}
	if r := g.do("GET", "/networks/ghost/leases", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
	p := g.do("GET", "/networks/IoT/leases?limit=1", nil, nil, nil).json(t)
	if len(p["items"].([]any)) != 1 || p["next_cursor"] == nil {
		t.Errorf("%v", p)
	}
}

func TestFlowsOfADevice(t *testing.T) {
	g := dhcpGateway(t)
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	g.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1\n" +
		"udp      17 25 src=10.10.0.99 dst=203.0.113.10 sport=1 dport=2 [UNREPLIED] src=203.0.113.10 dst=10.10.0.99 sport=2 dport=1 mark=0 use=1\n")
	all := g.do("GET", "/flows", nil, nil, nil).json(t)["items"].([]any)
	if len(all) != 2 {
		t.Fatalf("%v", all)
	}
	mine := g.do("GET", "/flows?device=esp32-42", nil, nil, nil).json(t)["items"].([]any)
	if len(mine) != 1 {
		t.Fatalf("%v", mine)
	}
	f := mine[0].(map[string]any)
	if f["device"] != espID || f["protocol"] != "tcp" || f["dport"] != float64(8883) || f["state"] != "ESTABLISHED" || f["nat_src"] != "203.0.113.1" ||
		f["upload"].(map[string]any)["bytes"] != float64(412) || f["download"].(map[string]any)["bytes"] != float64(500) {
		t.Errorf("%v", f)
	}
	if n := g.do("GET", "/flows?network=IoT", nil, nil, nil).json(t)["items"].([]any); len(n) != 2 {
		t.Errorf("%d", len(n))
	}
	if r := g.do("GET", "/flows?device=ghost", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
}

// M6a-03 test: a flow's started_at reaches the API when conntrack reports it, and is left out
// (not a zero-value timestamp) when conntrack does not.
func TestFlowStartedAtReachesTheAPI(t *testing.T) {
	g := dhcpGateway(t)
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	g.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 start=1700000000000000000 [ASSURED] mark=0 use=1\n")
	f := g.do("GET", "/flows", nil, nil, nil).json(t)["items"].([]any)[0].(map[string]any)
	if f["started_at"] != "2023-11-14T22:13:20Z" {
		t.Errorf("%v", f)
	}

	g.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1\n")
	f = g.do("GET", "/flows", nil, nil, nil).json(t)["items"].([]any)[0].(map[string]any)
	if _, ok := f["started_at"]; ok {
		t.Errorf("a started_at with no ktimestamp field: %v", f)
	}
}

// M6a-03 test: a device's active flow count reaches the devices API, and a flow redirected into the
// service namespace for DNS carries service "dns_proxy" in the flows API.
func TestDeviceFlowsActiveAndFlowService(t *testing.T) {
	g := dhcpGateway(t)
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	g.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1\n" +
		"udp      17 25 src=10.10.0.31 dst=203.0.113.10 sport=1 dport=53 packets=1 bytes=60 src=169.254.100.2 dst=10.10.0.31 sport=53 dport=1 packets=1 bytes=90 [ASSURED] mark=0 use=1\n")
	g.observe()

	d := g.do("GET", "/devices/esp32-42", nil, nil, nil).json(t)
	obs := d["observed"].(map[string]any)
	if obs["flows_active"] != float64(2) {
		t.Errorf("%v", obs)
	}

	flows := g.do("GET", "/flows?device=esp32-42", nil, nil, nil).json(t)["items"].([]any)
	var dns bool
	for _, raw := range flows {
		if f := raw.(map[string]any); f["service"] == "dns_proxy" {
			dns = true
		}
	}
	if !dns {
		t.Errorf("no dns_proxy flow in %v", flows)
	}
}

func TestLeaseEventsComeFromTheServiceOnly(t *testing.T) {
	g := dhcpGateway(t)
	sid := 0
	for id := range g.e.Snapshot().KeaNetworks {
		sid = id
	}
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	svc := strings.TrimSpace(string(raw))
	ev := map[string]any{"event": "select", "ip": "10.10.0.150", "mac": "02:00:00:00:00:AA", "subnet_id": sid, "valid_lifetime": 600, "hostname": "esp32"}

	// users cannot post lease events, even with the full scope
	// the MAC's upper case does not match the spec's pattern (lower case only): the server is
	// deliberately more lenient than the schema, which this test exercises on purpose
	g.badRequest = true
	if r := g.do("POST", "/internal/dhcp/lease-events", ev, nil, nil); r.Status != 403 {
		t.Errorf("a full token: %d", r.Status)
	}
	admin := g.token
	stream := g.openStream("", "?types=dhcp_lease")
	g.token = svc
	if r := g.do("POST", "/internal/dhcp/lease-events", ev, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.badRequest = false
	got, ok := stream.until("dhcp_lease", 5*time.Second)
	if !ok || got.Data["data"].(map[string]any)["ip"] != "10.10.0.150" || got.Data["data"].(map[string]any)["network"] != iotID || got.Data["data"].(map[string]any)["mac"] != "02:00:00:00:00:aa" {
		t.Fatalf("%+v", got)
	}
	// the service token reaches nothing else
	for _, p := range []string{"/state", "/devices", "/networks"} {
		if r := g.do("GET", p, nil, nil, nil); r.Status != 403 {
			t.Errorf("GET %s with the service token: %d", p, r.Status)
		}
	}
	// strict body
	g.badRequest = true // every body below is deliberately invalid
	for name, body := range map[string]any{
		"unknown field": map[string]any{"event": "select", "ip": "10.10.0.150", "mac": "02:00:00:00:00:aa", "subnet_id": 1, "x": 1},
		"bad event":     map[string]any{"event": "explode", "ip": "10.10.0.150", "mac": "02:00:00:00:00:aa", "subnet_id": 1},
		"bad ip":        map[string]any{"event": "select", "ip": "nope", "mac": "02:00:00:00:00:aa", "subnet_id": 1},
		"no mac":        map[string]any{"event": "select", "ip": "10.10.0.150", "subnet_id": 1},
		// M6a-22: the spec marks subnet_id required
		"no subnet_id": map[string]any{"event": "select", "ip": "10.10.0.150", "mac": "02:00:00:00:00:aa"},
	} {
		if r := g.do("POST", "/internal/dhcp/lease-events", body, nil, nil); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	g.badRequest = false
	g.token = admin
}

// M6a-09 test: a lease event posted over the Unix datagram socket reaches the engine the same way
// the HTTP path does.
func TestLeaseEventsArriveOverTheDatagramSocket(t *testing.T) {
	g := dhcpGateway(t)
	sid := 0
	for id := range g.e.Snapshot().KeaNetworks {
		sid = id
	}
	sockPath := filepath.Join(g.t.TempDir(), "chaosgw-events.sock")
	ctx, cancel := context_(10 * time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.srv.ListenKeaEvents(ctx, sockPath) }()
	// ListenKeaEvents creates the socket itself; give it a moment to exist before dialing
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the socket was never created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stream := g.openStream("", "?types=dhcp_lease")
	conn, err := net.DialTimeout("unixgram", sockPath, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"event": "select", "ip": "10.10.0.150", "mac": "02:00:00:00:00:AA", "subnet_id": sid, "valid_lifetime": 600, "hostname": "esp32"})
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	got, ok := stream.until("dhcp_lease", 5*time.Second)
	if !ok || got.Data["data"].(map[string]any)["ip"] != "10.10.0.150" || got.Data["data"].(map[string]any)["mac"] != "02:00:00:00:00:aa" {
		t.Fatalf("%+v", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("ListenKeaEvents: %v", err)
	}
}

// The device list and the device detail carry the winning fault of each family (the Faults column
// and the "With faults" filter of plan §2.17): a configured network fault, then an overlay on the
// device itself that beats it, with the overlay named among the device's overlays.
func TestADeviceCarriesTheFaultsThatWinForIt(t *testing.T) {
	g := dhcpGateway(t)
	fid := "5f1c7a9e-6d3b-4e8a-9c2f-1a2b3c4d5e6f"
	rev := g.mustPatch(map[string]any{"faults": map[string]any{fid: map[string]any{
		"name": "iot-delay", "source": map[string]any{"network": "IoT"}, "latency": "150ms",
	}}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	winner := func(d map[string]any) map[string]any {
		t.Helper()
		eff, _ := d["effective"].([]any)
		for _, e := range eff {
			if m := e.(map[string]any); m["family"] == "impairment" {
				return m
			}
		}
		t.Fatalf("no impairment winner: %v", d)
		return nil
	}
	d := g.do("GET", "/devices/esp32-42", nil, nil, nil).json(t)
	if w := winner(d); w["layer"] != "config" || w["id"] != fid || w["name"] != "iot-delay" || !strings.Contains(w["summary"].(string), "150ms") {
		t.Errorf("configured winner: %v", w)
	}
	if d["overlays"] != nil {
		t.Errorf("no overlays yet: %v", d["overlays"])
	}

	ov := g.mustCreateOverlay(map[string]any{"target": map[string]any{"device": "esp32-42"}, "fault": map[string]any{"loss": "5%"}})
	items := g.do("GET", "/devices?origin=configured", nil, nil, nil).json(t)["items"].([]any)
	var esp map[string]any
	for _, it := range items {
		if m := it.(map[string]any); m["id"] == espID {
			esp = m
		}
	}
	if esp == nil {
		t.Fatalf("%v", items)
	}
	if w := winner(esp); w["layer"] != "overlay" || w["id"] != ov["id"] {
		t.Errorf("overlay winner: %v", w)
	}
	if o, _ := esp["overlays"].([]any); len(o) != 1 || o[0] != ov["id"] {
		t.Errorf("overlays: %v", esp["overlays"])
	}
}

// #76: a fixed address inside a DHCP pool of its network is allowed; the preview warns at the device's fixed_ip,
// and the revision applies.
func TestThePreviewWarnsOfAFixedAddressInsideADHCPPool(t *testing.T) {
	g := dhcpGateway(t)
	warnings := func(id int64) []map[string]any {
		t.Helper()
		pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil)
		if pv.Status != 200 {
			t.Fatalf("%d %s", pv.Status, pv.Body)
		}
		var out []map[string]any
		for _, x := range pv.json(t)["warnings"].([]any) {
			if m := x.(map[string]any); m["code"] == "fixed_ip_in_pool" {
				out = append(out, m)
			}
		}
		return out
	}
	pools := map[string]any{iotID: map[string]any{"dhcp": map[string]any{"pools": []any{map[string]any{"start": "10.10.0.100", "end": "10.10.0.199"}}}}}

	outside := g.mustPatch(map[string]any{"networks": pools, "devices": map[string]any{espID: map[string]any{"fixed_ip": "10.10.0.42"}}})
	if ws := warnings(outside); len(ws) != 0 {
		t.Errorf("a reservation outside the pool: %v", ws)
	}
	if r := g.do("DELETE", "/revisions/"+itoa(outside), nil, nil, nil); r.Status != 204 {
		t.Fatalf("discard: %d %s", r.Status, r.Body)
	}

	inside := g.mustPatch(map[string]any{"networks": pools, "devices": map[string]any{espID: map[string]any{"fixed_ip": "10.10.0.150"}}})
	ws := warnings(inside)
	if len(ws) != 1 || ws[0]["path"] != "/devices/"+espID+"/fixed_ip" ||
		!strings.Contains(ws[0]["message"].(string), "10.10.0.150") || !strings.Contains(ws[0]["message"].(string), "pool 0 (10.10.0.100-10.10.0.199)") {
		t.Fatalf("warnings %v", ws)
	}
	if r := g.apply(inside); r.Status != 200 {
		t.Fatalf("a warning does not stop the apply: %d %s", r.Status, r.Body)
	}
}

// #94: a fixed address in a network with DHCP off makes no reservation; the preview warns at the device's
// fixed_ip also when no network serves DHCP at all (there is no Kea target then), and the revision applies.
func TestThePreviewWarnsOfAFixedAddressWhileNoNetworkServesDHCP(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(map[string]any{"devices": map[string]any{espID: map[string]any{
		"name": "esp32-42", "network": iotID, "fixed_ip": "10.10.0.42", "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:31"}},
	}}})
	pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	var ws []map[string]any
	for _, x := range pv.json(t)["warnings"].([]any) {
		ws = append(ws, x.(map[string]any))
	}
	if len(ws) != 1 || ws[0]["code"] != "fixed_ip_dhcp_off" || ws[0]["path"] != "/devices/"+espID+"/fixed_ip" ||
		!strings.Contains(ws[0]["message"].(string), "10.10.0.42") || !strings.Contains(ws[0]["message"].(string), "DHCP is off") {
		t.Fatalf("warnings %v", ws)
	}
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("a warning does not stop the apply: %d %s", r.Status, r.Body)
	}
}
