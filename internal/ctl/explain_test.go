package ctl

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const explainAnswer = `{
  "generation": 7,
  "source": {"ip": "10.10.0.31", "device": {"id": "00000000-0000-4000-8000-0000000000d1", "name": "esp32-42"}, "network": {"id": "00000000-0000-4000-8000-0000000000a1", "name": "IoT"}},
  "destination": {"hostname": "mqtt.example.com", "resolved": ["203.0.113.10"]},
  "access": {"verdict": "drop", "layer": "overlay_rule", "rule": "00000000-0000-4000-8000-0000000000e1", "reason": "overlay rule 1 drops tcp 8883"},
  "faults": [{"family": "impairment",
    "winner": {"family": "impairment", "id": "00000000-0000-4000-8000-0000000000f1", "layer": "overlay", "name": "bad-lte", "summary": "upload 200 ms, download 200 ms"},
    "overridden": [{"family": "impairment", "id": "00000000-0000-4000-8000-0000000000f2", "layer": "config", "name": "slow", "reason": "overlay layer wins (D24)"}]}],
  "route": {"gateway": "192.0.2.1", "interface": "eth0"}
}`

func explainGateway(t *testing.T) (run func(args ...string) (int, string, string), query func() url.Values) {
	t.Helper()
	var mu sync.Mutex
	var last url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/explain" || r.Header.Get("Authorization") != "Bearer t" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		last = r.URL.Query()
		mu.Unlock()
		if r.URL.Query().Get("device") == "ghost" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "not found", "status": 404, "code": "not_found", "detail": "no device ghost"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(explainAnswer))
	}))
	t.Cleanup(srv.Close)
	run = func(args ...string) (int, string, string) {
		mu.Lock()
		last = nil
		mu.Unlock()
		var out, errb bytes.Buffer
		code := Run(args, Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string {
			return map[string]string{"CHAOSGW_URL": srv.URL, "CHAOSGW_TOKEN": "t"}[k]
		}})
		return code, out.String(), errb.String()
	}
	query = func() url.Values { mu.Lock(); defer mu.Unlock(); return last }
	return run, query
}

// explain is a thin GET /explain: the flags are the query, the answer is shown in words or as the API's JSON.
func TestExplainAsksTheGatewayAboutOneTrafficTuple(t *testing.T) {
	run, query := explainGateway(t)

	code, out, errb := run("explain", "--device", "esp32-42", "--dst", "mqtt.example.com", "--protocol", "tcp", "--port", "8883")
	if code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if q := query(); q.Get("device") != "esp32-42" || q.Get("dst") != "mqtt.example.com" || q.Get("protocol") != "tcp" || q.Get("port") != "8883" || q.Get("src") != "" {
		t.Errorf("the query: %v", q)
	}
	for _, want := range []string{
		"esp32-42 (10.10.0.31, network IoT) -> mqtt.example.com (203.0.113.10) tcp/8883",
		"access: DROP (overlay_rule): overlay rule 1 drops tcp 8883 [rule 00000000-0000-4000-8000-0000000000e1]",
		"impairment: bad-lte [overlay] upload 200 ms, download 200 ms",
		"overridden: slow [config] (overlay layer wins (D24))",
		"(the faults take effect only when the access verdict is allow)",
		"route: via 192.0.2.1 dev eth0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}

	// the source can be an address; protocol and port are optional and are not sent when not given
	if code, _, errb = run("explain", "--src", "10.10.0.31", "--dst", "203.0.113.10"); code != 0 {
		t.Fatalf("%d %s", code, errb)
	}
	if q := query(); q.Get("src") != "10.10.0.31" || q.Get("device") != "" || q.Has("protocol") || q.Has("port") {
		t.Errorf("the query: %v", q)
	}

	// --json is the API's answer
	code, out, _ = run("explain", "--device", "esp32-42", "--dst", "203.0.113.10", "--json")
	var ex map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &ex) != nil || ex["generation"] != float64(7) || ex["access"].(map[string]any)["verdict"] != "drop" {
		t.Errorf("%d %s", code, out)
	}

	// what the gateway refuses is 2 with its explanation
	if code, _, errb = run("explain", "--device", "ghost", "--dst", "203.0.113.10"); code != ExitError || !strings.Contains(errb, "no device ghost") {
		t.Errorf("%d %s", code, errb)
	}

	// usage errors are 2 and ask nothing
	for name, args := range map[string][]string{
		"no source":        {"explain", "--dst", "203.0.113.10"},
		"two sources":      {"explain", "--device", "d", "--src", "10.0.0.1", "--dst", "203.0.113.10"},
		"no destination":   {"explain", "--device", "d"},
		"a bad protocol":   {"explain", "--device", "d", "--dst", "203.0.113.10", "--protocol", "sctp"},
		"a bad port":       {"explain", "--device", "d", "--dst", "203.0.113.10", "--port", "70000"},
		"a port of zero":   {"explain", "--device", "d", "--dst", "203.0.113.10", "--port", "0"},
		"an argument left": {"explain", "--device", "d", "--dst", "203.0.113.10", "extra"},
	} {
		if code, _, errb := run(args...); code != ExitError || query() != nil {
			t.Errorf("%s: %d %q %v", name, code, errb, query())
		}
	}
}
