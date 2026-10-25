package api_test

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// The interception proxy over the API (plan §2.8, M22): its rules in the configuration, the exchanges and secrets it reports, the log of the
// exchanges, the health of the proxy, the replacement of the test CA, and the key log that decrypts a capture. The proxy itself is a client
// of this API in these tests; the real mitmproxy is the subject of internal/tlsresponder (mitm_test.go) and of the level 1 tests.

const interceptRules = `{"target":{"device":"esp32-42"},"tls":{"case":"intercept","ports":[443],"intercept":{"http_rules":[
	{"match":{"host":"api.example.com","path_prefix":"/v1"},"action":"status","status":503},
	{"action":"delay","delay":"250ms"}]}}}`

func (g *gw) postFlows(flows ...map[string]any) resp {
	g.t.Helper()
	arr := make([]any, len(flows))
	for i, f := range flows {
		arr[i] = f
	}
	return g.do("POST", "/internal/intercept/flows", arr, nil, nil)
}

func flowReport(client, host, path string, status int, at time.Time, extra map[string]any) map[string]any {
	m := map[string]any{"time": at.UTC().Format(time.RFC3339Nano), "client": client, "original_dst": "203.0.113.10:443", "protocol": "http",
		"method": "GET", "host": host, "path": path, "status": status, "request_bytes": 0, "response_bytes": 12, "duration_ms": 3.5}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// The proxy fetches its rules like the responder fetches its cases: the entry of an interception case carries the settings as they were
// written. It says who it is, so that the health check can tell the two apart.
func TestTheProxyFetchesTheRulesOfTheInterceptionCasesAndIsTheOneTheHealthCheckSees(t *testing.T) {
	defer api.SetTLSPoll(300*time.Millisecond, 20*time.Millisecond)()
	g, admin := tlsGateway(t)
	g.asAdmin(admin, func() {
		comps, _ := healthComponents(t, g)
		if comps["tls_proxy"] != "disabled" {
			t.Errorf("a gateway that has never seen the proxy: %v", comps)
		}
	})
	var ov map[string]any
	g.asAdmin(admin, func() { ov = g.mustCreateOverlay(interceptRules) })
	cfg := g.do("GET", "/internal/tls/config?consumer=proxy", nil, nil, nil)
	if cfg.Status != 200 {
		t.Fatalf("%d %s", cfg.Status, cfg.Body)
	}
	var entry map[string]any
	for _, c := range cfg.json(t)["cases"].([]any) {
		if m := c.(map[string]any); m["overlay"] == ov["id"] {
			entry = m
		}
	}
	if entry == nil || entry["case"] != "intercept" || entry["expected"] != "any" || entry["ports"].([]any)[0] != float64(443) {
		t.Fatalf("%v", entry)
	}
	rules := entry["intercept"].(map[string]any)["http_rules"].([]any)
	if len(rules) != 2 || rules[0].(map[string]any)["status"] != float64(503) || rules[0].(map[string]any)["match"].(map[string]any)["path_prefix"] != "/v1" || rules[1].(map[string]any)["delay"] != "250ms" {
		t.Errorf("%v", rules)
	}
	g.badRequest = true
	if r := g.do("GET", "/internal/tls/config?consumer=somebody", nil, nil, nil); r.Status != 400 {
		t.Errorf("an unknown consumer: %d %s", r.Status, r.Body)
	}
	g.badRequest = false
	// the proxy polled, the responder did not: the proxy is healthy, the responder is not
	g.asAdmin(admin, func() {
		comps, _ := healthComponents(t, g)
		if comps["tls_proxy"] != "healthy" || comps["tls"] != "degraded" {
			t.Errorf("%v", comps)
		}
	})
}

// The upstream policy of a case (P5-M22-03) is part of the settings the proxy fetches, for each value, and a case without it carries none (the
// proxy then applies the installation's default). A value the spec does not know and the policy on a certificate case are refused when the
// overlay is written.
func TestTheUpstreamPolicyOfACaseReachesTheProxyAndAnInvalidOneIsRefused(t *testing.T) {
	defer api.SetTLSPoll(300*time.Millisecond, 20*time.Millisecond)()
	g, admin := tlsGateway(t)
	const body = `{"target":%s,"tls":{"case":"intercept","ports":[443],"intercept":{%s}}}`
	// one overlay per policy, on devices of their own: an overlay of the same scope would replace the one before it
	devices := map[string]string{"verify": `{"device":"esp32-42"}`, "own_ca": `{"device":"esp32-43"}`, "insecure": `{"group":"sensors"}`, "": `{"network":"IoT"}`}
	ids := map[string]any{}
	g.asAdmin(admin, func() {
		g.badRequest = true
		defer func() { g.badRequest = false }()
		for name, doc := range map[string]string{
			"an unknown policy":              fmt.Sprintf(body, `{"device":"esp32-42"}`, `"upstream":"trust_everyone"`),
			"a policy that is not text":      fmt.Sprintf(body, `{"device":"esp32-42"}`, `"upstream":true`),
			"a policy on a certificate case": `{"target":{"device":"esp32-42"},"tls":{"case":"expired","intercept":{"upstream":"insecure"}}}`,
			"the word default":               fmt.Sprintf(body, `{"device":"esp32-42"}`, `"upstream":"default"`),
		} {
			if r := g.createOverlay(doc); r.Status != 400 && r.Status != 422 {
				t.Errorf("%s: %d %s", name, r.Status, r.Body)
			}
		}
		g.badRequest = false
		for u, dev := range devices {
			settings := `"block_quic":true`
			if u != "" {
				settings = `"upstream":"` + u + `"`
			}
			ov := g.mustCreateOverlay(fmt.Sprintf(body, dev, settings))
			ids[u] = ov["id"]
			if got, _ := ov["tls"].(map[string]any)["intercept"].(map[string]any)["upstream"]; (got == nil) != (u == "") || (u != "" && got != u) {
				t.Errorf("the overlay of %q says %v", u, got)
			}
		}
	})
	cfg := g.do("GET", "/internal/tls/config?consumer=proxy", nil, nil, nil).json(t)
	got := map[any]map[string]any{}
	for _, c := range cfg["cases"].([]any) {
		m := c.(map[string]any)
		got[m["overlay"]] = m["intercept"].(map[string]any)
	}
	for u := range devices {
		v, ok := got[ids[u]]["upstream"]
		if (u == "") == ok || (ok && v != u) {
			t.Errorf("a case with the policy %q reaches the proxy with %v", u, got[ids[u]])
		}
	}
}

// A case that names the proxy while no proxy polls is a problem the health check names (the connections of the case are reset), and a gateway
// without a case does not mind a proxy that is not there.
func TestAnInterceptionCaseWithoutAProxyThatPollsMakesTheHealthCheckDegraded(t *testing.T) {
	g, admin := tlsGateway(t)
	g.asAdmin(admin, func() {
		g.mustCreateOverlay(interceptRules)
		body := g.do("GET", "/system/health", nil, nil, nil).json(t)
		var detail string
		for _, c := range body["components"].([]any) {
			if m := c.(map[string]any); m["name"] == "tls_proxy" {
				detail, _ = m["detail"].(string)
				if m["status"] != "degraded" {
					t.Errorf("%v", m)
				}
			}
		}
		if !strings.Contains(detail, "interception case") || !strings.Contains(detail, "reset") {
			t.Errorf("the detail says %q", detail)
		}
	})
}

// A connection of the case intercept gets `passthrough` with its case: the proxy's own connection to the destination leaves through the service
// path, which the access matrix does not see, so the responder hands the connection over only when the gateway would have forwarded it.
func TestTheDecisionOfAnInterceptedConnectionSaysWhetherTheGatewayWouldForwardIt(t *testing.T) {
	g, admin := tlsGateway(t)
	g.asAdmin(admin, func() { g.mustCreateOverlay(interceptRules) })
	got := g.resolveTLS("10.10.0.31", "203.0.113.10:443", "api.example.com")
	c, _ := got["case"].(map[string]any)
	if c == nil || c["case"] != "intercept" || got["expected"] != "any" || got["passthrough"] != true || got["trusts_test_ca"] != true || got["device"] != espID {
		t.Fatalf("%v", got)
	}
	if rules := c["intercept"].(map[string]any)["http_rules"].([]any); len(rules) != 2 {
		t.Errorf("the case as it was written: %v", c)
	}
	g.asAdmin(admin, func() {
		id := g.mustPatch(map[string]any{"access_matrix": map[string]any{"entries": []any{
			map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"uplink": true}, "policy": "deny"}}}})
		if r := g.apply(id); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	})
	got = g.resolveTLS("10.10.0.31", "203.0.113.10:443", "api.example.com")
	if got["case"] == nil || got["passthrough"] != false {
		t.Errorf("the matrix keeps the network from the uplink, so the proxy must not be the way through: %v", got)
	}
	// a certificate case never gets the field: the responder answers it itself
	g.asAdmin(admin, func() {
		g.mustCreateOverlay(`{"target":{"device":"esp32-43"},"tls":{"case":"expired","ports":[8883]}}`)
	})
	if got := g.resolveTLS("10.10.0.32", "203.0.113.10:8883", ""); got["case"] == nil || got["passthrough"] != nil {
		t.Errorf("%v", got)
	}
}

// The handshakes of the proxy are events like those of the responder, with the case intercept, and expect nothing of the device: a device
// that trusts the test CA takes the certificate, one that does not rejects it, and neither is a violation. The responder's report of the
// hand-over counts the connection for the overlay, the reports of the proxy do not count it again.
func TestTheHandshakesOfTheProxyAreEventsAndTheHandOverCountsTheConnectionOnce(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov map[string]any
	g.asAdmin(admin, func() { ov = g.mustCreateOverlay(interceptRules) })
	ch, cancel := g.e.Subscribe()
	defer cancel()
	now := time.Now().UTC()
	report := func(client, outcome, alert string) map[string]any {
		m := map[string]any{"time": now.Format(time.RFC3339Nano), "client": client, "original_dst": "203.0.113.10:443", "sni": "api.example.com",
			"overlay": ov["id"], "case": "intercept", "outcome": outcome}
		if alert != "" {
			m["alert"] = alert
		}
		return m
	}
	r := g.do("POST", "/internal/tls/handshakes", []any{
		report("10.10.0.31", "intercepted", ""), // the responder handed the connection over
		report("10.10.0.31", "accepted", ""),    // the proxy: the device took its certificate
		report("10.10.0.32", "intercepted", ""),
		report("10.10.0.32", "rejected", "unknown_ca"), // a device that does not trust the test CA
		report("10.10.0.99", "proxy_unavailable", ""),  // the proxy was not there
		report("10.10.0.31", "server_unreachable", ""), // the proxy could not reach the server
	}, nil, nil)
	if r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	var events []engine.Event
	deadline := time.After(5 * time.Second)
	for len(events) < 6 {
		select {
		case ev := <-ch:
			if ev.Type == engine.EventTLSHandshake {
				events = append(events, ev)
			}
		case <-deadline:
			t.Fatalf("%d events", len(events))
		}
	}
	for i, outcome := range []string{"intercepted", "accepted", "intercepted", "rejected", "proxy_unavailable", "server_unreachable"} {
		d := events[i].Data
		if d["outcome"] != outcome || d["case"] != "intercept" || d["expected"] != "any" || d["violation"] != false || d["tested"] != false || d["overlay"] != ov["id"] {
			t.Errorf("event %d: %v", i, d)
		}
	}
	var got map[string]any
	g.asAdmin(admin, func() { got = g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).json(t) })
	// four connections were held: two handed over, one that could not be and one to a server that is down
	if c, _ := got["counters"].(map[string]any); c == nil || c["packets"] != float64(4) {
		t.Errorf("%v", got)
	}
	// the checks read the same log: the device that took the proxy's certificate is an accepted handshake
	if hs := api.TLSHandshakeOutcomes(g.srv, "10.10.0.31"); strings.Join(hs, ",") != "intercepted,accepted,server_unreachable" {
		t.Errorf("%v", hs)
	}
}

// The exchanges the proxy reports are kept and listed newest first, with the device the observed state says the client is, and filtered by
// device, host, time and whether a rule acted. A rule that acts announces itself with an event, not for every request.
func TestTheExchangesOfTheProxyAreLoggedListedAndAnnouncedWhenARuleActs(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov map[string]any
	g.asAdmin(admin, func() { ov = g.mustCreateOverlay(interceptRules) })
	ch, cancel := g.e.Subscribe()
	defer cancel()
	t0 := time.Now().Add(-time.Minute)
	faulted := map[string]any{"overlay": ov["id"], "actions": []string{"delay", "status"}}
	if r := g.postFlows(
		flowReport("10.10.0.31", "api.example.com", "/v1/a", 503, t0, faulted),
		flowReport("10.10.0.31", "api.example.com", "/v1/b", 503, t0.Add(time.Second), faulted),
		flowReport("10.10.0.31", "cdn.example.net", "/img", 200, t0.Add(2*time.Second), map[string]any{"overlay": ov["id"]}),
		flowReport("10.10.0.32", "api.example.com", "/v1/c", 200, t0.Add(3*time.Second), nil),
		map[string]any{"time": t0.Add(4 * time.Second).UTC().Format(time.RFC3339Nano), "client": "10.10.0.99", "original_dst": "203.0.113.10:8883", "protocol": "tcp", "host": "broker.example.com", "request_bytes": 100, "response_bytes": 20},
	); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// an event for the first request a rule acted on, not for the second (the same overlay and device within the interval)
	select {
	case ev := <-ch:
		if ev.Type != engine.EventHTTPFaultTriggered || ev.Data["host"] != "api.example.com" || ev.Data["device_name"] != "esp32-42" || ev.Data["overlay"] != ov["id"] {
			t.Errorf("%+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	select {
	case ev := <-ch:
		t.Errorf("a second event for the same overlay and device: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	g.asAdmin(admin, func() {
		list := func(query string) []map[string]any {
			r := g.do("GET", "/intercept/flows"+query, nil, nil, nil)
			if r.Status != 200 {
				t.Fatalf("%s: %d %s", query, r.Status, r.Body)
			}
			var out []map[string]any
			for _, it := range r.json(t)["items"].([]any) {
				out = append(out, it.(map[string]any))
			}
			return out
		}
		all := list("")
		if len(all) != 5 || all[0]["protocol"] != "tcp" || all[0]["device"] != nil || all[4]["path"] != "/v1/a" {
			t.Fatalf("newest first: %v", all)
		}
		if all[1]["device"] != espTrustID || all[4]["device"] != espID || all[4]["status"] != float64(503) || all[4]["actions"].([]any)[1] != "status" || all[4]["overlay"] != ov["id"] {
			t.Errorf("the device the client is: %v", all)
		}
		if got := list("?device=esp32-42"); len(got) != 3 {
			t.Errorf("by device: %v", got)
		}
		if r := g.do("GET", "/intercept/flows?device=nobody", nil, nil, nil); r.Status != 404 {
			t.Errorf("a device that does not exist: %d %s", r.Status, r.Body)
		}
		if got := list("?host=cdn.example.net"); len(got) != 1 || got[0]["path"] != "/img" {
			t.Errorf("by host: %v", got)
		}
		// as the filter of the DNS log does, `*.suffix` is the suffix itself and everything below it
		if got := list("?host=*.example.com"); len(got) != 4 { // api (three) and broker
			t.Errorf("%v", got)
		}
		if got := list("?host=*.example.net"); len(got) != 1 || got[0]["path"] != "/img" {
			t.Errorf("%v", got)
		}
		if got := list("?host=*.other.org"); len(got) != 0 {
			t.Errorf("%v", got)
		}
		if got := list("?faulted=true"); len(got) != 2 {
			t.Errorf("faulted: %v", got)
		}
		if got := list("?faulted=false"); len(got) != 3 {
			t.Errorf("not faulted: %v", got)
		}
		if got := list("?since=" + t0.Add(1500*time.Millisecond).UTC().Format(time.RFC3339Nano)); len(got) != 3 {
			t.Errorf("since: %v", got)
		}
		// pages
		first := g.do("GET", "/intercept/flows?limit=2", nil, nil, nil).json(t)
		next, _ := first["next_cursor"].(string)
		if len(first["items"].([]any)) != 2 || next == "" {
			t.Fatalf("%v", first)
		}
		second := g.do("GET", "/intercept/flows?limit=2&cursor="+next, nil, nil, nil).json(t)
		if got := second["items"].([]any); len(got) != 2 || got[0].(map[string]any)["path"] == first["items"].([]any)[0].(map[string]any)["path"] {
			t.Errorf("%v", second)
		}
	})
}

// A report that is not well formed is refused as a whole; the proxy's interface is for the service token only.
func TestTheExchangesOfTheProxyAreValidatedAndForTheServiceOnly(t *testing.T) {
	g, admin := tlsGateway(t)
	now := time.Now()
	g.badRequest = true
	for name, f := range map[string]map[string]any{
		"no time":           {"client": "10.10.0.31", "original_dst": "203.0.113.10:443", "protocol": "http"},
		"a name":            {"time": now.UTC().Format(time.RFC3339), "client": "esp32-42", "original_dst": "203.0.113.10:443", "protocol": "http"},
		"no port":           {"time": now.UTC().Format(time.RFC3339), "client": "10.10.0.31", "original_dst": "203.0.113.10", "protocol": "http"},
		"an unknown kind":   {"time": now.UTC().Format(time.RFC3339), "client": "10.10.0.31", "original_dst": "203.0.113.10:443", "protocol": "quic"},
		"an unknown action": {"time": now.UTC().Format(time.RFC3339), "client": "10.10.0.31", "original_dst": "203.0.113.10:443", "protocol": "http", "actions": []string{"explode"}},
	} {
		if r := g.postFlows(f); r.Status != 422 && r.Status != 400 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	g.badRequest = false
	if r := g.do("GET", "/intercept/flows", nil, nil, nil); r.Status != 403 {
		t.Errorf("the service token reads the log: %d", r.Status)
	}
	g.asAdmin(admin, func() {
		for _, path := range []string{"/internal/intercept/flows", "/internal/intercept/keylog"} {
			if r := g.do("POST", path, []any{}, nil, nil); r.Status != 403 {
				t.Errorf("%s with an admin token: %d", path, r.Status)
			}
		}
		if r := g.do("GET", "/intercept/flows", nil, nil, nil); r.Status != 200 {
			t.Errorf("an admin token reads the log: %d", r.Status)
		}
		g.mintToken("read")
		if r := g.do("GET", "/intercept/flows", nil, nil, nil); r.Status != 200 {
			t.Errorf("a read token reads the log: %d", r.Status)
		}
	})
}

// The test CA is replaced on request: the download is the new certificate, the files of the CA directory follow, the unknown CA stays, the
// request needs the scope full and is in the audit log.
func TestTheTestCAIsReplacedOnRequestAndNothingElseOfTheCAsChanges(t *testing.T) {
	g := ready(t)
	admin := g.token
	dir := filepath.Join(g.root, "tlsca")
	oldTest, _ := os.ReadFile(filepath.Join(dir, "test-ca.pem"))
	oldUnknown, _ := os.ReadFile(filepath.Join(dir, "unknown-ca.pem"))
	oldKey, _ := os.ReadFile(filepath.Join(dir, "test-ca.key"))
	if len(oldTest) == 0 {
		t.Fatal("no test CA")
	}
	g.mintToken("overlays")
	if r := g.do("POST", "/tls/ca/regenerate", nil, nil, nil); r.Status != 403 {
		t.Errorf("a token without the scope full: %d", r.Status)
	}
	g.token = admin
	r := g.do("POST", "/tls/ca/regenerate", nil, nil, nil)
	if r.Status != 200 || !strings.Contains(r.Header.Get("Content-Disposition"), "chaos-gateway-test-ca.pem") {
		t.Fatalf("%d %s %v", r.Status, r.Body, r.Header)
	}
	newPEM := r.Body
	if bytes.Equal(newPEM, oldTest) || !bytes.Contains(newPEM, []byte("BEGIN CERTIFICATE")) || bytes.Contains(newPEM, []byte("PRIVATE KEY")) {
		t.Errorf("the response is not a new certificate: %s", newPEM)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "test-ca.pem")); !bytes.Equal(got, newPEM) {
		t.Error("the file is not what was returned")
	}
	if key, _ := os.ReadFile(filepath.Join(dir, "test-ca.key")); bytes.Equal(key, oldKey) {
		t.Error("the key is the old one")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "unknown-ca.pem")); !bytes.Equal(got, oldUnknown) {
		t.Error("the unknown CA changed")
	}
	if _, err := tlsresponder.LoadCAs(dir); err != nil {
		t.Errorf("the CAs do not load as a pair: %v", err)
	}
	if dl := g.do("GET", "/tls/ca", nil, nil, nil); !bytes.Equal(dl.Body, newPEM) {
		t.Error("the download is not the new certificate")
	}
	found := false
	for _, e := range g.do("GET", "/audit?limit=100", nil, nil, nil).json(t)["items"].([]any) {
		found = found || e.(map[string]any)["action"] == "tls.ca_regenerate"
	}
	if !found {
		t.Error("the replacement is not in the audit log")
	}
}

// ---- the key log

// tcpSegment builds the Ethernet frame of a TCP segment.
func tcpSegment(smac, dmac, sip, dip string, sport, dport uint16, seq, ack uint32, flags byte, payload []byte) []byte {
	l4 := make([]byte, 20+len(payload))
	binary.BigEndian.PutUint16(l4[0:], sport)
	binary.BigEndian.PutUint16(l4[2:], dport)
	binary.BigEndian.PutUint32(l4[4:], seq)
	binary.BigEndian.PutUint32(l4[8:], ack)
	l4[12] = 5 << 4
	l4[13] = flags
	binary.BigEndian.PutUint16(l4[14:], 65535)
	copy(l4[20:], payload)
	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:], uint16(20+len(l4)))
	ip[8], ip[9] = 64, 6
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

// recorder turns a TCP connection of a client into the frames a capture on the gateway would hold: the handshake of the TCP connection, the
// segments of both directions with their sequence numbers.
type recorder struct {
	mu      sync.Mutex
	frames  [][]byte
	cseq    uint32
	sseq    uint32
	started bool
}

const (
	flagFIN, flagSYN, flagPSH, flagACK = 0x01, 0x02, 0x08, 0x10
	tcpClientPort                      = 40443
)

func (r *recorder) start() {
	r.cseq, r.sseq = 1000, 5000
	f := func(up bool, seq, ack uint32, flags byte) {
		if up {
			r.frames = append(r.frames, tcpSegment(macESP, macGW, "10.10.0.31", "203.0.113.10", tcpClientPort, 443, seq, ack, flags, nil))
		} else {
			r.frames = append(r.frames, tcpSegment(macGW, macESP, "203.0.113.10", "10.10.0.31", 443, tcpClientPort, seq, ack, flags, nil))
		}
	}
	f(true, r.cseq, 0, flagSYN)
	f(false, r.sseq, r.cseq+1, flagSYN|flagACK)
	f(true, r.cseq+1, r.sseq+1, flagACK)
	r.cseq, r.sseq = r.cseq+1, r.sseq+1
	r.started = true
}

func (r *recorder) data(up bool, b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		r.start()
	}
	for len(b) > 0 {
		n := min(len(b), 1400)
		if up {
			r.frames = append(r.frames, tcpSegment(macESP, macGW, "10.10.0.31", "203.0.113.10", tcpClientPort, 443, r.cseq, r.sseq, flagPSH|flagACK, b[:n]))
			r.cseq += uint32(n)
		} else {
			r.frames = append(r.frames, tcpSegment(macGW, macESP, "203.0.113.10", "10.10.0.31", 443, tcpClientPort, r.sseq, r.cseq, flagPSH|flagACK, b[:n]))
			r.sseq += uint32(n)
		}
		b = b[n:]
	}
}

type recConn struct {
	net.Conn
	rec *recorder
}

func (c *recConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.rec.data(true, b[:n])
	return n, err
}

func (c *recConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.rec.data(false, b[:n])
	return n, err
}

// tlsExchange is a TLS 1.3 request of a client to a server on the loopback, recorded as the frames of a device's connection; it returns the
// frames and the lines of the key log the client wrote (the secrets of the connection, which the proxy's log holds as well: see
// internal/tlsresponder, TestTheKeyLogHoldsTheSecretsOfTheDevicesConnection).
func tlsExchange(t *testing.T, path, body string) (frames [][]byte, keylog []string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
	srv.StartTLS()
	defer srv.Close()
	roots := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	var kl bytes.Buffer
	raw, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	tc := tls.Client(&recConn{Conn: raw, rec: rec}, &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS13, KeyLogWriter: &kl, NextProtos: []string{"http/1.1"}})
	_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(tc, "GET %s HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n", path); err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(tc); !bytes.Contains(got, []byte(body)) {
		t.Fatalf("the response: %q", got)
	}
	_ = tc.Close()
	for _, l := range strings.Split(kl.String(), "\n") {
		if strings.TrimSpace(l) != "" {
			keylog = append(keylog, l)
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.frames, keylog
}

func (g *gw) postKeylog(client string, lines []string) resp {
	g.t.Helper()
	return g.do("POST", "/internal/intercept/keylog", []any{map[string]any{"client": client, "lines": lines}}, nil, nil)
}

// tsharkReads is what tshark finds in the HTTP of the capture it decrypts with the key log: the requests and the bodies. Empty without the
// log, since the capture holds only TLS.
func tsharkReads(t *testing.T, capture, keylog []byte) string {
	t.Helper()
	dir := t.TempDir()
	cf := filepath.Join(dir, "capture.pcapng")
	if err := os.WriteFile(cf, capture, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-r", cf}
	if keylog != nil {
		kf := filepath.Join(dir, "keylog.txt")
		if err := os.WriteFile(kf, keylog, 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-o", "tls.keylog_file:"+kf)
	}
	args = append(args, "-Y", "http", "-T", "fields", "-e", "http.request.uri", "-e", "http.file_data")
	out, err := exec.Command("tshark", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tshark: %v\n%s", err, out)
	}
	// http.file_data is hex of the body
	var decoded []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "\t")
		for i, p := range parts {
			if i == len(parts)-1 && len(p) > 0 {
				if b, err := hex.DecodeString(strings.ReplaceAll(p, ":", "")); err == nil {
					p = string(b)
				}
			}
			decoded = append(decoded, p)
		}
	}
	return strings.Join(decoded, " ")
}

// The plan's test: the key log decrypts the run's capture. A run that records its device, a TLS connection of the device (its frames are
// put on the interface), the secrets of that connection given to the gateway as the proxy gives them, and the capture of the run opened with
// them in Wireshark's engine: the request and the response are there. Without the key log the capture shows TLS only.
func TestTheKeyLogDecryptsTheCaptureOfARun(t *testing.T) {
	if _, err := exec.LookPath("tshark"); err != nil {
		t.Skip("tshark is not installed")
	}
	g, clk := capGateway(t)
	svc := tlsServiceToken(g)
	admin := g.token
	r := g.postRun(capturing("rec", step("hold", "0s", "profile", "normal"), step("end", "10s", "restore", true)), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "hold", "applied")

	frames, keylog := tlsExchange(t, "/telemetry/temperature", "SECRET-BODY-12345")
	if len(keylog) < 3 {
		t.Fatalf("the client wrote %d lines", len(keylog))
	}
	for _, f := range frames {
		g.cap.Frame("br-iot", f, false)
	}
	g.token = svc
	if r := g.postKeylog("10.10.0.31", keylog); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.token = admin

	clk.BlockUntil(1)
	clk.Advance(10 * time.Second)
	run := g.waitRun(id, "passed")
	caps := artifactsOfKind(run, "capture")
	logs := artifactsOfKind(run, "keylog")
	if len(caps) != 1 || len(logs) != 1 || logs[0]["capture"] != caps[0]["capture"] || !strings.HasSuffix(logs[0]["url"].(string), "?part=keylog") {
		t.Fatalf("the report offers the capture and its key log: %v", run["artifacts"])
	}
	capID := caps[0]["capture"].(string)
	cp := g.capture(capID)
	if cp["keylog"] != true || cp["sensitive"] != true {
		t.Errorf("a capture that holds secrets says so: %v", cp)
	}
	capture := g.do("GET", "/captures/"+capID+"/download", nil, nil, nil)
	kl := g.do("GET", "/captures/"+capID+"/download?part=keylog", nil, nil, nil)
	if capture.Status != 200 || kl.Status != 200 || kl.Header.Get("Content-Type") != "text/plain" || !strings.Contains(kl.Header.Get("Content-Disposition"), "-keylog.txt") {
		t.Fatalf("%d %d %v", capture.Status, kl.Status, kl.Header)
	}
	for _, l := range keylog {
		if !strings.Contains(string(kl.Body), l+"\n") {
			t.Errorf("the key log lacks %q", l)
		}
	}
	if got := tsharkReads(t, capture.Body, nil); strings.Contains(got, "SECRET-BODY") || strings.Contains(got, "/telemetry") {
		t.Errorf("tshark reads the capture without a key log: %q", got)
	}
	if got := tsharkReads(t, capture.Body, kl.Body); !strings.Contains(got, "/telemetry/temperature") || !strings.Contains(got, "SECRET-BODY-12345") {
		t.Errorf("tshark does not read the capture with the key log of the gateway: %q", got)
	}
}

// Only the captures that cover the device get its secrets, and only while they run; the download of the log, and of a capture that holds one,
// is for the scope full, and a capture without secrets has no log.
func TestTheKeyLogGoesToTheRunningCapturesThatCoverTheDeviceOnly(t *testing.T) {
	g, _ := capGateway(t)
	svc := tlsServiceToken(g)
	admin := g.token
	device := g.mustStartCapture(map[string]any{"name": "dev", "scope": map[string]any{"device": "esp32-42"}})["id"].(string)
	network := g.mustStartCapture(map[string]any{"name": "net", "scope": map[string]any{"network": "IoT"}})["id"].(string)
	elsewhere := g.mustStartCapture(map[string]any{"name": "else", "scope": map[string]any{"network": "lab-hub"}})["id"].(string)
	uplink := g.mustStartCapture(map[string]any{"name": "up", "scope": map[string]any{"uplink": true}})["id"].(string)
	selector := g.mustStartCapture(map[string]any{"name": "sel", "scope": map[string]any{"selector": map[string]any{"source": map[string]any{"group": "sensors"}, "protocol": "tcp", "ports": []int{8883}}}})["id"].(string)
	stopped := g.mustStartCapture(map[string]any{"name": "old", "scope": map[string]any{"device": "esp32-42"}})["id"].(string)
	g.stopCapture(stopped)

	g.token = svc
	if r := g.postKeylog("10.10.0.31", []string{"CLIENT_RANDOM aa bb", "SERVER_TRAFFIC_SECRET_0 aa cc"}); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// a device that is no device of the configuration, in the network
	if r := g.postKeylog("10.10.0.77", []string{"CLIENT_RANDOM dd ee"}); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.token = admin

	keylogOf := func(id string) (string, int) {
		r := g.do("GET", "/captures/"+id+"/download?part=keylog", nil, nil, nil)
		return string(r.Body), r.Status
	}
	for name, id := range map[string]string{"the device": device, "the network": network, "the uplink": uplink, "the selector": selector} {
		body, status := keylogOf(id)
		if status != 200 || !strings.Contains(body, "CLIENT_RANDOM aa bb\n") || !strings.Contains(body, "SERVER_TRAFFIC_SECRET_0 aa cc\n") {
			t.Errorf("the capture of %s: %d %q", name, status, body)
		}
		if g.capture(id)["keylog"] != true || g.capture(id)["sensitive"] != true {
			t.Errorf("%s: %v", name, g.capture(id))
		}
	}
	// the device capture does not hold the secrets of the other address; the network's does
	if body, _ := keylogOf(device); strings.Contains(body, "dd ee") {
		t.Errorf("the capture of one device holds the secrets of another: %q", body)
	}
	if body, _ := keylogOf(network); !strings.Contains(body, "CLIENT_RANDOM dd ee\n") {
		t.Errorf("the capture of the network lacks the secrets of its other client: %q", body)
	}
	for name, id := range map[string]string{"another network": elsewhere, "a capture that has ended": stopped} {
		if r := g.do("GET", "/captures/"+id+"/download?part=keylog", nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
		if c := g.capture(id); c["keylog"] != false || c["sensitive"] != false {
			t.Errorf("%s: %v", name, c)
		}
	}

	// the key log and a capture that holds one are for the scope full
	g.mintToken("read")
	for _, q := range []string{"?part=keylog", ""} {
		if r := g.do("GET", "/captures/"+device+"/download"+q, nil, nil, nil); r.Status != 403 {
			t.Errorf("a read token downloads %q: %d", q, r.Status)
		}
	}
	if r := g.do("GET", "/captures/"+elsewhere+"/download", nil, nil, nil); r.Status != 200 {
		t.Errorf("a capture without secrets is for every reader: %d", r.Status)
	}
	// after the capture ends the log is still there, and it takes no more
	g.token = admin
	g.stopCapture(device)
	g.token = svc
	_ = g.postKeylog("10.10.0.31", []string{"CLIENT_RANDOM late late"})
	g.token = admin
	if body, _ := keylogOf(device); !strings.Contains(body, "CLIENT_RANDOM aa bb\n") || strings.Contains(body, "late") {
		t.Errorf("the log of a capture that ended: %q", body)
	}
	// and a delete takes the log with the capture
	if r := g.do("DELETE", "/captures/"+device, nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(g.cap.dir, device, "keylog.txt")); !os.IsNotExist(err) {
		t.Errorf("the key log stays after the capture: %v", err)
	}
}

// The batches of the proxy are checked: the client is an IPv4 address and a line is a line.
func TestTheKeyLogOfTheProxyIsValidated(t *testing.T) {
	g, _ := capGateway(t)
	g.token = tlsServiceToken(g)
	g.badRequest = true
	for name, body := range map[string]any{
		"a name":     []any{map[string]any{"client": "esp32-42", "lines": []string{"x"}}},
		"IPv6":       []any{map[string]any{"client": "fd00::1", "lines": []string{"x"}}},
		"a break":    []any{map[string]any{"client": "10.10.0.31", "lines": []string{"x\ny"}}},
		"no lines":   []any{map[string]any{"client": "10.10.0.31"}},
		"no client":  []any{map[string]any{"lines": []string{"x"}}},
		"not a list": map[string]any{"client": "10.10.0.31", "lines": []string{"x"}},
	} {
		if r := g.do("POST", "/internal/intercept/keylog", body, nil, nil); r.Status != 422 && r.Status != 400 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	g.badRequest = false
	// nothing runs: the secrets are of no use and are dropped without an error
	if r := g.postKeylog("10.10.0.31", []string{"CLIENT_RANDOM a b"}); r.Status != 204 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

// The gateway lists the interception among its features.
func TestTheCapabilitiesListTheInterception(t *testing.T) {
	g := ready(t)
	r := g.do("GET", "/capabilities", nil, nil, nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"tls.intercept"`) {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}
