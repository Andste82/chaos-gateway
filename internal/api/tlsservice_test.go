package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The TLS responder over the API (plan §2.8, M21): its configuration, the decision of each connection, the outcomes
// of the handshakes and the download of the test CA.

const espTrustID = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"

// tlsGateway is a gateway with a service namespace, a device that trusts the test CA (esp32-42), one that does not (esp32-43)
// and the service token; admin is the admin's token.
func tlsGateway(t *testing.T) (g *gw, admin string) {
	t.Helper()
	g = newGW(t, func(o *options) { o.serviceNS = "cgsvc" })
	g.k.ServiceNamespace("cgsvc")
	g.finishSetup()
	id := g.mustPatch(map[string]any{
		"networks": map[string]any{iotID: map[string]any{"dhcp": map[string]any{"lease_time": "10m"}}},
		"devices": map[string]any{
			espID:      map[string]any{"name": "esp32-42", "network": iotID, "trusts_test_ca": true, "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:31"}}},
			espTrustID: map[string]any{"name": "esp32-43", "network": iotID, "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:32"}}},
		},
		"groups": map[string]any{"c0a80001-0000-4000-8000-000000000001": map[string]any{"name": "sensors", "members": []any{"esp32-42", "esp32-43"}}},
	})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31"), nb("10.10.0.32", "02:00:00:00:00:32"), nb("10.10.0.77", "02:00:00:00:00:77")})
	g.observe()
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	admin = g.token
	g.token = strings.TrimSpace(string(raw))
	return g, admin
}

// tlsServiceToken is the token of the service containers of a gateway made with tlsGateway.
func tlsServiceToken(g *gw) string {
	file := filepath.Join(g.t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		g.t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	return strings.TrimSpace(string(raw))
}

func (g *gw) asAdmin(admin string, f func()) {
	svc := g.token
	g.token = admin
	defer func() { g.token = svc }()
	f()
}

func tlsConfigOf(t *testing.T, g *gw) map[string]any {
	t.Helper()
	r := g.do("GET", "/internal/tls/config", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	return r.json(t)
}

func (g *gw) resolveTLS(client, dst, sni string) map[string]any {
	g.t.Helper()
	body := map[string]any{"client": client, "original_dst": dst}
	if sni != "" {
		body["sni"] = sni
	}
	r := g.do("POST", "/internal/tls/resolve", body, nil, nil)
	if r.Status != 200 {
		g.t.Fatalf("%d %s", r.Status, r.Body)
	}
	return r.json(g.t)
}

func TestTheTLSConfigurationHoldsTheCasesOfTheActiveOverlaysWithTheirClients(t *testing.T) {
	defer api.SetTLSPoll(300*time.Millisecond, 20*time.Millisecond)()
	g, admin := tlsGateway(t)
	// the responder's configuration is its own: users cannot read it
	g.asAdmin(admin, func() {
		if r := g.do("GET", "/internal/tls/config", nil, nil, nil); r.Status != 403 {
			t.Errorf("an admin token: %d", r.Status)
		}
	})
	if cfg := tlsConfigOf(t, g); len(cfg["cases"].([]any)) != 0 || cfg["generation"] == nil {
		t.Fatalf("a gateway without a case: %v", cfg)
	}
	var dev, prof map[string]any
	g.asAdmin(admin, func() {
		dev = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"tls":{"case":"expired","ports":[8883],"sni":["broker.example.com"]}}`)
		prof = g.mustCreateOverlay(`{"target":{"network":"IoT"},"profile":"tls-broken"}`)
	})
	cfg := tlsConfigOf(t, g)
	cases := cfg["cases"].([]any)
	if len(cases) < 2 {
		t.Fatalf("%v", cases)
	}
	var sawProfile, sawDevice bool
	for _, c := range cases {
		m := c.(map[string]any)
		switch m["overlay"] {
		case dev["id"]:
			// the device's own entry: the clients are its address, the case as it was written, what a correct device does with it
			sawDevice = sawDevice || (m["case"] == "expired" && m["expected"] == "reject" && m["ports"].([]any)[0] == float64(8883) && m["sni"].([]any)[0] == "broker.example.com" &&
				len(m["sources"].([]any)) == 1 && m["sources"].([]any)[0] == "10.10.0.31/32")
		case prof["id"]:
			sawProfile = sawProfile || (m["case"] == "handshake_reset" && m["expected"] == "any")
		}
	}
	if !sawDevice || !sawProfile {
		t.Errorf("the device's case (%v) and the tls part of the profile (%v) are in the list: %v", sawDevice, sawProfile, cases)
	}
	// the configuration changes with the overlays: a long poll ends with the new one, and without a change with 204
	gen := cfg["generation"].(float64)
	if r := g.do("GET", fmt.Sprintf("/internal/tls/config?after=%d", int64(gen)), nil, nil, nil); r.Status != 204 {
		t.Errorf("no change: %d %s", r.Status, r.Body)
	}
	g.asAdmin(admin, func() { g.mustCreateOverlay(`{"target":{"device":"esp32-43"},"tls":{"case":"self_signed"}}`) })
	next := g.do("GET", fmt.Sprintf("/internal/tls/config?after=%d", int64(gen)), nil, nil, nil)
	if next.Status != 200 || next.json(t)["generation"].(float64) <= gen {
		t.Errorf("%d %s", next.Status, next.Body)
	}
}

// The decision of a connection is the one of the precedence rules (plan §2.4): the case of the most specific scope that the
// connection (port, destination, server name) matches; no case at all means the responder passes the connection through.
func TestTheDecisionOfAConnectionFollowsThePrecedenceRulesAndTheServerName(t *testing.T) {
	g, admin := tlsGateway(t)
	var network, device, sniCase map[string]any
	g.asAdmin(admin, func() {
		network = g.mustCreateOverlay(`{"target":{"network":"IoT"},"tls":{"case":"untrusted_ca"}}`)
		device = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"tls":{"case":"expired","ports":[8883]}}`)
		sniCase = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"tls":{"case":"self_signed","ports":[8883],"sni":["*.example.org"]}}`)
	})
	decide := func(client, dst, sni string) map[string]any { return g.resolveTLS(client, dst, sni) }

	// a connection that the device's case selects: the device's case, with what the responder needs to judge the device
	got := decide("10.10.0.31", "203.0.113.10:8883", "")
	if got["overlay"] != device["id"] || got["expected"] != "reject" || got["device"] != espID || got["trusts_test_ca"] != true || got["case"].(map[string]any)["case"] != "expired" || got["generation"] == nil {
		t.Errorf("%v", got)
	}
	// the server name picks the case that names it: level 1 (device, destination) beats level 3
	got = decide("10.10.0.31", "203.0.113.10:8883", "mqtt.example.org")
	if got["overlay"] != sniCase["id"] || got["case"].(map[string]any)["case"] != "self_signed" {
		t.Errorf("%v", got)
	}
	// another port: the network's case (default ports 443 and 8883: 443 is one)
	got = decide("10.10.0.31", "203.0.113.10:443", "")
	if got["overlay"] != network["id"] || got["case"].(map[string]any)["case"] != "untrusted_ca" {
		t.Errorf("%v", got)
	}
	// a device that does not trust the test CA: the decision says so
	got = decide("10.10.0.32", "203.0.113.10:8883", "")
	if got["overlay"] != network["id"] || got["device"] != espTrustID || got["trusts_test_ca"] != false {
		t.Errorf("%v", got)
	}
	// an address no device owns, in the network: the network's case, without a device
	got = decide("10.10.0.99", "203.0.113.10:8883", "")
	if got["overlay"] != network["id"] || got["device"] != nil || got["trusts_test_ca"] != false {
		t.Errorf("%v", got)
	}
	// a port no case selects, and a client of no network: no case, the responder passes the connection through
	for _, c := range [][2]string{{"10.10.0.31", "203.0.113.10:22"}, {"192.0.2.55", "203.0.113.10:8883"}} {
		got = decide(c[0], c[1], "")
		if got["case"] != nil || got["overlay"] != nil || got["generation"] == nil {
			t.Errorf("%v: %v", c, got)
		}
	}
	// a local network reaches the uplink by default: the responder may pass such a connection through; one that the gateway would
	// drop is reset (the matrix is asked about the destination the device meant)
	if got = decide("10.10.0.31", "203.0.113.10:22", ""); got["passthrough"] != true {
		t.Errorf("%v", got)
	}
	g.asAdmin(admin, func() {
		id := g.mustPatch(map[string]any{"access_matrix": map[string]any{"entries": []any{
			map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"uplink": true}, "policy": "deny"}}}})
		if r := g.apply(id); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	})
	if got = decide("10.10.0.31", "203.0.113.10:22", ""); got["case"] != nil || got["passthrough"] != false {
		t.Errorf("the matrix keeps the network from the uplink: %v", got)
	}
	if got = decide("10.10.0.31", "203.0.113.10:8883", ""); got["case"] == nil || got["passthrough"] != nil {
		t.Errorf("a case decides whatever the matrix says: %v", got)
	}
	// an overlay that goes: its connections have no case any more
	g.asAdmin(admin, func() {
		for _, id := range []string{device["id"].(string), sniCase["id"].(string)} {
			if r := g.do("DELETE", "/overlays/"+id, nil, nil, nil); r.Status != 204 {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
		}
	})
	if got := decide("10.10.0.31", "203.0.113.10:8883", ""); got["overlay"] != network["id"] {
		t.Errorf("%v", got)
	}
}

// A destination that names a host matches the names the gateway's DNS answered the client for the address; the certificate
// of a connection without a server name is for the name the DNS answered last.
func TestAHostnameDestinationAndTheNameForAConnectionWithoutServerNameComeFromTheGatewaysDNS(t *testing.T) {
	g, admin := tlsGateway(t)
	g.asAdmin(admin, func() {
		g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"tls":{"case":"expired","destination":{"hostname":"*.example.com"}}}`)
	})
	if got := g.resolveTLS("10.10.0.31", "203.0.113.20:8883", ""); got["case"] != nil {
		t.Fatalf("an address nobody resolved: %v", got)
	}
	postDNS := func(name string, at time.Time) {
		r := g.do("POST", "/internal/dns/queries", []any{map[string]any{
			"time": at.UTC().Format(time.RFC3339Nano), "client": "10.10.0.31", "name": name, "type": "A", "rcode": "NOERROR", "answers": []any{"203.0.113.20"}}}, nil, nil)
		if r.Status != 204 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}
	now := time.Now()
	postDNS("a.example.com", now.Add(-time.Second))
	postDNS("b.example.com", now)
	got := g.resolveTLS("10.10.0.31", "203.0.113.20:8883", "")
	if got["case"] == nil || got["name"] != "b.example.com" {
		t.Errorf("the name of the newest answer is wanted: %v", got)
	}
	// with a server name the name is the client's own
	if got := g.resolveTLS("10.10.0.31", "203.0.113.20:8883", "a.example.com"); got["name"] != nil {
		t.Errorf("%v", got)
	}
	// another client was not answered the address
	if got := g.resolveTLS("10.10.0.32", "203.0.113.20:8883", ""); got["case"] != nil {
		t.Errorf("%v", got)
	}
}

func TestADecisionIsAskedForWellFormedConnectionsOnly(t *testing.T) {
	g, _ := tlsGateway(t)
	g.badRequest = true
	defer func() { g.badRequest = false }()
	for name, body := range map[string]any{
		"no client":       map[string]any{"original_dst": "203.0.113.10:8883"},
		"a name":          map[string]any{"client": "esp32-42", "original_dst": "203.0.113.10:8883"},
		"an IPv6 client":  map[string]any{"client": "fd00::1", "original_dst": "203.0.113.10:8883"},
		"no port":         map[string]any{"client": "10.10.0.31", "original_dst": "203.0.113.10"},
		"port zero":       map[string]any{"client": "10.10.0.31", "original_dst": "203.0.113.10:0"},
		"an IPv6 address": map[string]any{"client": "10.10.0.31", "original_dst": "[2001:db8::1]:443"},
		"unknown field":   map[string]any{"client": "10.10.0.31", "original_dst": "203.0.113.10:443", "bogus": 1},
	} {
		if r := g.do("POST", "/internal/tls/resolve", body, nil, nil); r.Status != 422 && r.Status != 400 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
}

// Every handshake is an event, a count for its overlay and an entry of the log; a device that completes the handshake of a
// case it must reject is a violation, and a rejection tests the case only where the firmware can tell.
func TestEveryHandshakeIsAnEventAndCountedForItsOverlay(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov map[string]any
	g.asAdmin(admin, func() {
		ov = g.mustCreateOverlay(`{"target":{"network":"IoT"},"tls":{"case":"expired","ports":[8883]}}`)
	})
	ch, cancel := g.e.Subscribe()
	defer cancel()
	now := time.Now().UTC()
	report := func(client, outcome, alert string, extra map[string]any) map[string]any {
		m := map[string]any{"time": now.Format(time.RFC3339Nano), "client": client, "original_dst": "203.0.113.10:8883", "sni": "broker.example.com",
			"overlay": ov["id"], "case": "expired", "outcome": outcome, "tls_version": "TLS 1.3"}
		if alert != "" {
			m["alert"] = alert
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	r := g.do("POST", "/internal/tls/handshakes", []any{
		report("10.10.0.31", "rejected", "certificate_expired", nil), // trusts the test CA: it tested the dates
		report("10.10.0.32", "rejected", "unknown_ca", nil),          // does not: it failed at the first check
		report("10.10.0.31", "accepted", "", nil),                    // took a certificate it must reject
		report("10.10.0.99", "client_closed", "", nil),               // never got as far, and no device
	}, nil, nil)
	if r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	var events []engine.Event
	deadline := time.After(5 * time.Second)
	for len(events) < 4 {
		select {
		case ev := <-ch:
			if ev.Type == engine.EventTLSHandshake {
				events = append(events, ev)
			}
		case <-deadline:
			t.Fatalf("%d events", len(events))
		}
	}
	type want struct {
		device, outcome   string
		violation, tested bool
	}
	for i, w := range []want{{"esp32-42", "rejected", false, true}, {"esp32-43", "rejected", false, false}, {"esp32-42", "accepted", true, false}, {"", "client_closed", false, false}} {
		d := events[i].Data
		name, _ := d["device_name"].(string)
		if name != w.device || d["outcome"] != w.outcome || d["violation"] != w.violation || d["tested"] != w.tested || d["expected"] != "reject" ||
			d["case"] != "expired" || d["overlay"] != ov["id"] || d["original_dst"] != "203.0.113.10:8883" || d["sni"] != "broker.example.com" {
			t.Errorf("event %d: %v", i, d)
		}
	}
	if sub, ok := events[0].Data["subject"].(engine.Subject); !ok || sub.Kind != "device" || sub.ID != espID {
		t.Errorf("the subject: %v", events[0].Data["subject"])
	}
	// the overlay counts the handshakes it held
	var got map[string]any
	g.asAdmin(admin, func() { got = g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).json(t) })
	if c, _ := got["counters"].(map[string]any); c == nil || c["packets"] != float64(4) || got["state"] != "effective" {
		t.Errorf("%v", got)
	}
}

// The event of a handshake is on the stream, in the shape of the spec, with the device as its subject and a line in words that
// says what a device that took the certificate did.
func TestTheStreamTellsAHandshakeInWords(t *testing.T) {
	g, admin := tlsGateway(t)
	g.asAdmin(admin, func() { g.mustCreateOverlay(`{"target":{"network":"IoT"},"tls":{"case":"self_signed"}}`) })
	g.token = admin
	st := g.openStream("", "?types=tls_handshake")
	g.token = ""
	now := time.Now().UTC()
	svc := tlsServiceToken(g)
	g.token = svc
	if r := g.do("POST", "/internal/tls/handshakes", []any{map[string]any{"time": now.Format(time.RFC3339Nano), "client": "10.10.0.31", "original_dst": "203.0.113.10:8883",
		"case": "self_signed", "outcome": "accepted", "tls_version": "TLS 1.2"}}, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ev, ok := st.until("tls_handshake", 5*time.Second)
	if !ok {
		t.Fatal("no event")
	}
	if sub, _ := ev.Data["subject"].(map[string]any); sub["kind"] != "device" || sub["id"] != espID || sub["name"] != "esp32-42" {
		t.Errorf("subject %v", ev.Data["subject"])
	}
	data, _ := ev.Data["data"].(map[string]any)
	if data["violation"] != true || data["outcome"] != "accepted" || data["tls_version"] != "TLS 1.2" || data["case"] != "self_signed" {
		t.Errorf("data %v", data)
	}
	if msg, _ := ev.Data["message"].(string); !strings.Contains(msg, "esp32-42") || !strings.Contains(msg, "203.0.113.10:8883") || !strings.Contains(msg, "self_signed") || !strings.Contains(msg, "must reject") {
		t.Errorf("message %q", msg)
	}
}

func TestHandshakeReportsAreCheckedBeforeTheyAreTaken(t *testing.T) {
	g, _ := tlsGateway(t)
	g.badRequest = true
	defer func() { g.badRequest = false }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	ok := map[string]any{"time": now, "client": "10.10.0.31", "original_dst": "203.0.113.10:8883", "outcome": "rejected"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range ok {
			m[kk] = vv
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	for name, rep := range map[string]map[string]any{
		"no time":         with("time", nil),
		"no client":       with("client", nil),
		"an IPv6 client":  with("client", "fd00::1"),
		"a bad address":   with("original_dst", "203.0.113.10"),
		"another outcome": with("outcome", "shrugged"),
	} {
		if r := g.do("POST", "/internal/tls/handshakes", []any{rep}, nil, nil); r.Status != 422 && r.Status != 400 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := g.do("POST", "/internal/tls/handshakes", []any{ok}, nil, nil); r.Status != 204 {
		t.Errorf("a good report: %d %s", r.Status, r.Body)
	}
}

// The download is the test CA's certificate, for a read token and an admin, never a key and never the unknown CA.
func TestTheTestCADownloadIsTheCertificateOnly(t *testing.T) {
	g := ready(t)
	r := g.do("GET", "/tls/ca", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-pem-file") {
		t.Errorf("content type %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); cd != `attachment; filename="chaos-gateway-test-ca.pem"` {
		t.Errorf("Content-Disposition %q", cd)
	}
	pem := string(r.Body)
	if !strings.HasPrefix(pem, "-----BEGIN CERTIFICATE-----") || strings.Count(pem, "BEGIN") != 1 || strings.Contains(pem, "PRIVATE KEY") {
		t.Errorf("%s", pem)
	}
	unknown, err := os.ReadFile(filepath.Join(g.root, "tlsca", "unknown-ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pem, strings.TrimSpace(string(unknown))) {
		t.Error("the download holds the unknown CA")
	}
	test, _ := os.ReadFile(filepath.Join(g.root, "tlsca", "test-ca.pem"))
	if pem != string(test) {
		t.Error("the download is not the test CA's certificate")
	}
	// a read token may download it; without a credential nobody may
	g.mintToken("read")
	if r := g.do("GET", "/tls/ca", nil, nil, nil); r.Status != 200 {
		t.Errorf("a read token: %d", r.Status)
	}
	g.token = ""
	if r := g.do("GET", "/tls/ca", nil, nil, func(r *http.Request) { r.Header.Del("Authorization") }); r.Status != 401 {
		t.Errorf("without a credential: %d", r.Status)
	}
}

// The health check lists the TLS responder: degraded while it has not polled for its configuration, healthy once it does,
// and disabled on a gateway without a service namespace.
func TestTheHealthCheckListsTheTLSResponder(t *testing.T) {
	defer api.SetTLSPoll(50*time.Millisecond, 10*time.Millisecond)()
	g, admin := tlsGateway(t)
	g.asAdmin(admin, func() {
		comps, _ := healthComponents(t, g)
		if comps["tls"] != "degraded" {
			t.Errorf("before the first poll: %v", comps)
		}
	})
	r := g.do("GET", "/internal/tls/config", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.asAdmin(admin, func() {
		if comps, _ := healthComponents(t, g); comps["tls"] != "healthy" {
			t.Errorf("after a poll: %v", comps)
		}
	})
	plain := ready(t)
	if comps, _ := healthComponents(t, plain); comps["tls"] != "disabled" {
		t.Errorf("without a service namespace: %v", comps)
	}
}

// explain says that the TLS responder answers a connection that a case decides, and not one that an access rule drops.
func TestExplainNamesTheTLSResponderAndStopsAtAnAccessRule(t *testing.T) {
	g, admin := tlsGateway(t)
	g.token = admin
	g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"tls":{"case":"expired","ports":[8883]}}`)
	explain := func() map[string]any {
		return g.do("GET", "/explain?device=esp32-42&dst=203.0.113.10&protocol=tcp&port=8883", nil, nil, nil).json(t)
	}
	if got := explain(); got["service"] != "tls_responder" {
		t.Fatalf("%v", got)
	}
	// E11: an access rule that drops the connection stands in front of the redirect
	g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"rule":{"action":"drop","protocol":"tcp","ports":[8883]}}`)
	got := explain()
	if got["service"] != "none" || got["access"].(map[string]any)["verdict"] != "drop" {
		t.Errorf("%v", got)
	}
}
