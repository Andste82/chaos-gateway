package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// M23c: `chaosctl intercept set`, `intercept flows` and `tls ca` against the API on the simulated kernel with the real engine (the gateway of the
// interception tests: the device esp32-42 on IoT, the service token that the proxy reports with, the admin token that chaosctl uses). The proxy
// itself is a client of the API here, as in internal/api/intercept_test.go.

// ctlAs runs chaosctl with the admin token.
func ctlAs(g *gw, admin string, args ...string) ctlOut {
	g.t.Helper()
	var r ctlOut
	g.asAdmin(admin, func() { r = g.ctl(args...) })
	return r
}

func TestChaosctlMakesInterceptionCasesWithTheirRules(t *testing.T) {
	g, admin := tlsGateway(t)
	var ids []string
	for _, tc := range []struct {
		args []string
		want map[string]any
	}{
		{[]string{"intercept", "set", "--device", "esp32-42", "--ttl", "5m",
			"--rule", "host=api.example.com;path=/v1/;status=503", "--rule", "delay=250ms", "--rule", "host=cdn.example.net;throttle=64kbit",
			"--rule", "method=POST;block", "--rule", "host=flaky.example.com;abort",
			"--rule", "request-header=X-Chaos: on;remove-request-header=Cookie;response-header=Cache-Control:no-store;replace=ok=>fail"},
			map[string]any{"case": "intercept", "intercept": map[string]any{"http_rules": []any{
				map[string]any{"action": "status", "status": 503.0, "match": map[string]any{"host": "api.example.com", "path_prefix": "/v1/"}},
				map[string]any{"action": "delay", "delay": "250ms"},
				map[string]any{"action": "throttle", "rate": "64kbit", "match": map[string]any{"host": "cdn.example.net"}},
				map[string]any{"action": "block", "match": map[string]any{"method": "POST"}},
				map[string]any{"action": "abort", "match": map[string]any{"host": "flaky.example.com"}},
				map[string]any{"action": "modify", "modify": map[string]any{
					"request_headers":       map[string]any{"X-Chaos": "on", "Cookie": ""},
					"response_headers":      map[string]any{"Cache-Control": "no-store"},
					"response_body_replace": []any{map[string]any{"search": "ok", "replace": "fail"}}}},
			}}}},
		// the selection of the case, the settings, and nothing but the case when only watching
		{[]string{"intercept", "set", "--network", "IoT", "--port", "443,8443", "--sni", "api.example.com", "--to", "uplink", "--cut-existing",
			"--no-keylog", "--allow-quic", "--upstream", "insecure"},
			map[string]any{"case": "intercept", "ports": []any{443.0, 8443.0}, "sni": []any{"api.example.com"}, "destination": map[string]any{"uplink": true}, "cut_existing": true,
				"intercept": map[string]any{"keylog": false, "block_quic": false, "upstream": "insecure"}}},
		{[]string{"intercept", "set", "--group", "sensors"}, map[string]any{"case": "intercept"}},
	} {
		r := ctlAs(g, admin, tc.args...)
		if r.code != 0 {
			t.Fatalf("%v: %+v", tc.args, r)
		}
		var ov map[string]any
		g.asAdmin(admin, func() { ov = overlayOf(t, g, r.out) })
		if ov["kind"] != "tls" || !reflect.DeepEqual(ov["tls"], tc.want) {
			t.Errorf("%v made %v, want %v", tc.args, ov["tls"], tc.want)
		}
		ids = append(ids, ov["id"].(string))
	}
	g.asAdmin(admin, func() {
		if ov := g.do("GET", "/overlays/"+ids[0], nil, nil, nil).json(t); ov["ttl"] != "5m" || ov["target"].(map[string]any)["device"] != espID {
			t.Errorf("%v", ov)
		}
	})

	// what the command line can see without the gateway
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"intercept", "set"}, "exactly one target"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--upstream", "maybe"}, "is not one of verify, own_ca, insecure"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--rule", "block;abort"}, "--rule 1"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--rule", "host=a.example.com"}, "no action"},
		{[]string{"intercept", "set", "--device", "esp32-42", "extra"}, "unknown command"},
		{[]string{"tls", "set", "intercept", "--device", "esp32-42"}, "chaosctl intercept set"},
	} {
		if r := ctlAs(g, admin, tc.args...); r.code != 2 || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want exit 2 and %q", tc.args, r, tc.want)
		}
	}
	// what only the gateway knows is its refusal with the pointer of the rule
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"intercept", "set", "--device", "esp32-42", "--rule", "block", "--rule", "status=700"}, "/tls/intercept/http_rules/1/status"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--rule", "delay=0s"}, "/tls/intercept/http_rules/0/delay"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--rule", "throttle=1bit"}, "/tls/intercept/http_rules/0/rate"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--rule", "method=GET POST;block"}, "/tls/intercept/http_rules/0/match/method"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--rule", "request-header=Bad Name:x"}, "/tls/intercept/http_rules/0/modify/request_headers/Bad Name"},
		{[]string{"intercept", "set", "--device", "esp32-42", "--sni", "not a name!"}, "/tls/sni/0"},
	} {
		g.badRequest = true
		r := ctlAs(g, admin, tc.args...)
		g.badRequest = false
		if r.code != 2 || !strings.Contains(r.err, "422") || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want 422 and %q", tc.args, r, tc.want)
		}
	}
	if r := ctlAs(g, admin, append([]string{"overlay", "delete"}, ids...)...); r.code != 0 || len(overlayIDsOfKindAs(t, g, admin, "tls")) != 0 {
		t.Errorf("%+v", r)
	}
}

func overlayIDsOfKindAs(t *testing.T, g *gw, admin, kind string) (ids []string) {
	g.asAdmin(admin, func() { ids = overlayIDsOfKind(t, g, kind) })
	return ids
}

func TestChaosctlListsTheExchangesOfTheProxyWithTheFiltersOfTheAPI(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov map[string]any
	g.asAdmin(admin, func() { ov = g.mustCreateOverlay(interceptRules) })
	t0 := time.Now().Add(-time.Hour)
	faulted := map[string]any{"overlay": ov["id"], "actions": []string{"delay", "status"}}
	if r := g.postFlows(
		flowReport("10.10.0.31", "api.example.com", "/v1/a", 503, t0, faulted),
		flowReport("10.10.0.31", "api.example.com", "/v1/b", 200, t0.Add(time.Second), nil),
		flowReport("10.10.0.32", "cdn.example.net", "/img", 200, t0.Add(2*time.Second), nil),
		map[string]any{"time": t0.Add(3 * time.Second).UTC().Format(time.RFC3339Nano), "client": "10.10.0.99", "original_dst": "203.0.113.10:8883", "protocol": "tcp",
			"host": "broker.example.com", "request_bytes": 100, "response_bytes": 20, "error": "the server did not answer"},
		map[string]any{"time": t0.Add(4 * time.Second).UTC().Format(time.RFC3339Nano), "client": "10.10.0.31", "original_dst": "203.0.113.10:443", "protocol": "websocket",
			"method": "GET", "host": "ws.example.com", "path": "/socket", "status": 101, "messages": 7},
	); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}

	// the table: newest first, the device by its name where the gateway knows it and the client's address where not
	r := ctlAs(g, admin, "intercept", "flows")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 6 || !strings.HasPrefix(lines[0], "TIME") {
		t.Fatalf("%q", r.out)
	}
	for i, want := range []string{"GET ws.example.com/socket", "broker.example.com", "GET cdn.example.net/img", "GET api.example.com/v1/b", "GET api.example.com/v1/a"} {
		if !strings.Contains(lines[i+1], want) {
			t.Errorf("line %d: %q, want %q", i+1, lines[i+1], want)
		}
	}
	if !strings.Contains(lines[1], "esp32-42") || !strings.Contains(lines[1], "websocket") || !strings.Contains(lines[1], "101") || !strings.Contains(lines[1], "0/0 7 msg") {
		t.Errorf("a WebSocket: %q", lines[1])
	}
	if !strings.Contains(lines[2], "10.10.0.99") || !strings.Contains(lines[2], "[the server did not answer]") || !strings.Contains(lines[2], "100/20") {
		t.Errorf("a stream of an unknown client: %q", lines[2])
	}
	if !strings.Contains(lines[3], "esp32-43") || !strings.Contains(lines[5], "delay,status") || !strings.Contains(lines[5], "503") {
		t.Errorf("%q", r.out)
	}

	count := func(args ...string) int {
		r := ctlAs(g, admin, append([]string{"intercept", "flows", "--json"}, args...)...)
		if r.code != 0 {
			t.Fatalf("%v: %+v", args, r)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(r.out), &page); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, r.out)
		}
		return len(page.Items)
	}
	for _, tc := range []struct {
		args []string
		want int
	}{
		{nil, 5},
		{[]string{"--device", "esp32-42"}, 3},
		{[]string{"--device", "esp32-43"}, 1},
		{[]string{"--host", "api.example.com"}, 2},
		{[]string{"--host", "*.example.com"}, 4},
		{[]string{"--faulted"}, 1},
		{[]string{"--since", "59m"}, 0},
		{[]string{"--since", t0.Add(1500 * time.Millisecond).UTC().Format(time.RFC3339Nano)}, 3},
		{[]string{"--limit", "2"}, 2},
		// the cursor is followed to the end with --all, a page at a time
		{[]string{"--limit", "2", "--all"}, 5},
	} {
		if got := count(tc.args...); got != tc.want {
			t.Errorf("%v: %d exchanges, want %d", tc.args, got, tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--limit", "0"}, "--limit"},
		{[]string{"--limit", "501"}, "--limit"},
		{[]string{"--since", "yesterday"}, "--since"},
		{[]string{"--device", "nobody"}, "404"},
	} {
		if r := ctlAs(g, admin, append([]string{"intercept", "flows"}, tc.args...)...); r.code != 2 || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want exit 2 and %q", tc.args, r, tc.want)
		}
	}
	// a token that only reads sees the log
	g.asAdmin(admin, func() {
		g.mintToken("read")
		if r := g.ctl("intercept", "flows", "--faulted"); r.code != 0 || !strings.Contains(r.out, "/v1/a") {
			t.Errorf("%+v", r)
		}
	})
}

func TestChaosctlDownloadsAndReplacesTheTestCA(t *testing.T) {
	g := ready(t)
	admin := g.token
	dir := filepath.Join(g.root, "tlsca")
	onDisk := func() []byte {
		b, _ := os.ReadFile(filepath.Join(dir, "test-ca.pem"))
		return b
	}
	old := onDisk()
	r := g.ctl("tls", "ca")
	if r.code != 0 || r.out != string(old) || !strings.Contains(r.out, "BEGIN CERTIFICATE") || strings.Contains(r.out, "PRIVATE KEY") {
		t.Fatalf("the download is the certificate the gateway serves: %+v", r)
	}
	// a file (the certificate is public: readable by everyone), replaced whole
	out := filepath.Join(t.TempDir(), "test-ca.pem")
	if err := os.WriteFile(out, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := g.ctl("tls", "ca", "--out", out); r.code != 0 || r.out != "" {
		t.Fatalf("%+v", r)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, old) {
		t.Errorf("the file holds %q", got)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode())
	}

	// the replacement asks to be confirmed, and changes nothing without it
	if r := g.ctl("tls", "ca", "regenerate"); r.code != 2 || !strings.Contains(r.err, "--yes") || !bytes.Equal(onDisk(), old) {
		t.Errorf("%+v", r)
	}
	if r := g.ctl("tls", "ca", "extra"); r.code != 2 {
		t.Errorf("%+v", r)
	}
	// a token without the scope full is refused by the gateway
	g.mintToken("overlays")
	if r := g.ctl("tls", "ca", "regenerate", "--yes"); r.code != 2 || !strings.Contains(r.err, "403") || !bytes.Equal(onDisk(), old) {
		t.Errorf("%+v", r)
	}
	g.token = admin
	r = g.ctl("tls", "ca", "regenerate", "--yes")
	if r.code != 0 || r.out == string(old) || r.out != string(onDisk()) || !strings.Contains(r.out, "BEGIN CERTIFICATE") || !strings.Contains(r.err, "install the new certificate") {
		t.Fatalf("the new certificate is what the gateway serves from now on: %+v", r)
	}
	if r := g.ctl("tls", "ca"); r.out != string(onDisk()) {
		t.Errorf("the download is the new certificate: %+v", r)
	}
	if r := g.ctl("tls", "ca", "regenerate", "--yes", "--out", out); r.code != 0 || r.out != "" {
		t.Fatalf("%+v", r)
	} else if got, _ := os.ReadFile(out); !bytes.Equal(got, onDisk()) || bytes.Equal(got, old) {
		t.Errorf("the file holds the newest certificate: %q", got)
	}
}
