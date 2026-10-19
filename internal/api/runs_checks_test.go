package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// M16 over the API: the checks of a scenario are evaluated against the connections of the (simulated) kernel and
// the DNS log the proxy posts, on the fake clock of the API; the report, the JUnit file and the events carry the
// results; a fault or rule that matched no packet is a zero-hit warning.

// checksGateway is a gateway with the configured device esp32-42 (10.10.0.31 on IoT) and a token of the DNS proxy,
// on a fake clock.
func checksGateway(t *testing.T) (g *gw, clk *clock.Fake, service string) {
	t.Helper()
	clk = clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	g = newGW(t, func(o *options) { o.apiClock = clk })
	g.finishSetup()
	id := g.mustPatch(map[string]any{
		"networks": map[string]any{iotID: map[string]any{"dhcp": map[string]any{"lease_time": "10m"}}},
		"devices":  map[string]any{espID: map[string]any{"name": "esp32-42", "network": iotID, "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:31"}}}},
	})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	return g, clk, strings.TrimSpace(string(raw))
}

// ct is a line of `conntrack -L`: a connection of esp32-42 to the server.
func ct(state string, sport, dport int, dst string, packets int) string {
	flag := "[ASSURED]"
	if state == "SYN_SENT" {
		flag = "[UNREPLIED]"
	}
	return fmt.Sprintf("tcp      6 431999 %s src=10.10.0.31 dst=%s sport=%d dport=%d packets=%d bytes=%d src=%s dst=203.0.113.1 sport=%d dport=%d packets=%d bytes=%d %s mark=0 use=1\n",
		state, dst, sport, dport, packets, packets*60, dst, dport, sport, packets/2, packets*30, flag)
}

// afterRead lets the fake clock run until the checks have listed the connections once more.
func (g *gw) afterRead(clk *clock.Fake) {
	g.t.Helper()
	n := g.k.ConntrackReads()
	deadline := time.Now().Add(30 * time.Second)
	for g.k.ConntrackReads() <= n {
		if time.Now().After(deadline) {
			g.t.Fatal("the checks do not read the connections")
		}
		clk.Advance(250 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
	}
}

func (g *gw) postQuery(service string, at time.Time, client, name string) {
	g.t.Helper()
	admin := g.token
	g.token = service
	defer func() { g.token = admin }()
	body := []any{map[string]any{"time": at.UTC().Format(time.RFC3339Nano), "client": client, "name": name, "type": "A", "rcode": "NOERROR", "protocol": "udp"}}
	if r := g.do("POST", "/internal/dns/queries", body, nil, nil); r.Status != 204 {
		g.t.Fatalf("%d %s", r.Status, r.Body)
	}
}

func checkStatuses(run map[string]any) []string {
	var out []string
	for _, c := range run["checks"].([]any) {
		out = append(out, c.(map[string]any)["status"].(string))
	}
	return out
}

func (g *gw) waitCheck(id string, i int, status string) map[string]any {
	g.t.Helper()
	var c map[string]any
	eventuallyTrue(g.t, fmt.Sprintf("check %d %s", i, status), func() bool {
		c = g.run(id)["checks"].([]any)[i].(map[string]any)
		return c["status"] == status
	})
	return c
}

// The scenario of plan §2.10 with a check of every type this build evaluates: cut, restore, and what the device does
// around it. The connections are those of the simulated kernel, the query is the proxy's.
func TestChecksOfEveryTypeAreEvaluatedOverTheAPI(t *testing.T) {
	g, clk, service := checksGateway(t)
	body := inlineRun("outage", map[string]any{"device": "esp32-42"},
		step("cut", "0s", "rule", map[string]any{"action": "drop", "protocol": "tcp", "ports": []int{8883}}),
		step("restore", "2s", "restore", true))
	sc := body["inline"].(map[string]any)
	sc["preconditions"] = map[string]any{"skip": []string{"target_online", "uses_gateway_dns"}}
	window := func(from string, kv ...string) map[string]any {
		w := map[string]any{"from": from}
		for i := 0; i < len(kv); i += 2 {
			w[kv[i]] = kv[i+1]
		}
		return w
	}
	sc["checks"] = []any{
		map[string]any{"reconnected": map[string]any{"protocol": "tcp", "ports": []int{8883}}, "window": window("restore", "within", "20s"), "name": "reconnects to the broker"},
		map[string]any{"no_connection": map[string]any{"protocol": "tcp", "ports": []int{1883}}, "window": window("cut", "until", "restore")},
		map[string]any{"dns_query_seen": map[string]any{"name": "broker.example.com"}, "window": window("restore", "within", "20s")},
		map[string]any{"traffic_not_seen": map[string]any{"destination": map[string]any{"cidr": "198.51.100.0/24"}}, "window": window("cut", "until", "restore")},
	}
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	if got := checkStatuses(r.json(t)); strings.Join(got, ",") != "pending,pending,pending,pending" {
		t.Errorf("a new run has its checks pending: %v", got)
	}
	g.waitStep(id, "cut", "applied")

	// while the rule is on: a handshake that goes nowhere and traffic to the broker's net are allowed
	g.afterRead(clk) // the baseline of the windows of `cut`
	g.k.SetConntrack(ct("SYN_SENT", 40001, 1883, "203.0.113.10", 1))
	g.afterRead(clk)
	g.afterRead(clk)
	if got := checkStatuses(g.run(id)); got[1] != "pending" || got[3] != "pending" {
		t.Fatalf("checks after the handshake: %v", got)
	}

	// the restore closes the two windows that end with it: nothing was established, nothing went to the other net
	for g.run(id)["steps"].([]any)[1].(map[string]any)["status"] != "applied" {
		clk.Advance(250 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
	}
	g.waitCheck(id, 1, "passed")
	g.waitCheck(id, 3, "passed")
	if got := checkStatuses(g.run(id)); got[0] != "pending" || got[2] != "pending" || g.run(id)["status"] != "running" {
		t.Fatalf("the run ended, or the windows after the restore are decided: %v", got)
	}

	// the device reconnects and asks for the broker
	g.afterRead(clk) // the baseline of the windows of `restore`
	g.k.SetConntrack(ct("SYN_SENT", 40001, 1883, "203.0.113.10", 1) + ct("ESTABLISHED", 40002, 8883, "203.0.113.10", 8))
	g.postQuery(service, clk.Now(), "10.10.0.31", "broker.example.com")
	g.afterRead(clk)
	c0 := g.waitCheck(id, 0, "passed")
	g.waitCheck(id, 2, "passed")
	if c0["name"] != "reconnects to the broker" || c0["window_start"] == nil || c0["window_end"] == nil || len(c0["evidence"].([]any)) != 1 ||
		!strings.Contains(c0["message"].(string), "203.0.113.10:8883") {
		t.Errorf("check 0: %v", c0)
	}
	done := g.waitRun(id, "passed")
	if got := checkStatuses(done); strings.Join(got, ",") != "passed,passed,passed,passed" {
		t.Errorf("checks %v", got)
	}

	// the report, the JUnit file and the events
	rep := g.do("GET", "/runs/"+id+"/report.json", nil, nil, nil).json(t)
	if len(rep["checks"].([]any)) != 4 {
		t.Errorf("report %v", rep["checks"])
	}
	xml := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body)
	for _, want := range []string{`tests="5"`, `failures="0"`, `<testcase name="reconnects to the broker"`,
		`<testcase name="no_connection tcp/1883, from cut until restore"`, `<testcase name="dns_query_seen broker.example.com, from restore within 20s"`} {
		if !strings.Contains(xml, want) {
			t.Errorf("no %s in the JUnit file:\n%s", want, xml)
		}
	}
	checkEvents := 0
	for _, line := range strings.Split(strings.TrimSpace(string(g.do("GET", "/runs/"+id+"/events", nil, nil, nil).Body)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		if e["type"] == "run_check" {
			checkEvents++
			if !strings.Contains(e["message"].(string), "passed") {
				t.Errorf("event %v", e)
			}
		}
	}
	if checkEvents != 4 {
		t.Errorf("%d run_check events in the event file of the run, want 4", checkEvents)
	}
}

// A check whose window closes without what it waits for fails the run: `failed`, with the check in the report.
func TestAFailedCheckFailsTheRunAndIsAFailureInTheJUnitFile(t *testing.T) {
	g, clk, _ := checksGateway(t)
	body := inlineRun("never-back", iotTarget,
		step("cut", "0s", "rule", map[string]any{"action": "drop", "protocol": "tcp", "ports": []int{8883}}),
		step("restore", "1s", "restore", true))
	body["inline"].(map[string]any)["checks"] = []any{
		map[string]any{"reconnected": map[string]any{"protocol": "tcp", "ports": []int{8883}}, "window": map[string]any{"from": "restore", "within": "3s"}},
	}
	id := g.postRun(body, "").json(t)["id"].(string)
	g.k.SetConntrack(ct("ESTABLISHED", 40002, 8883, "203.0.113.10", 8)) // there before the window: no reconnection
	eventuallyTrue(t, "the run to fail", func() bool {
		clk.Advance(250 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
		return g.run(id)["status"] == "failed"
	})
	done := g.run(id)
	if !strings.Contains(done["reason"].(string), `check "reconnected tcp/8883, from restore within 3s" failed`) {
		t.Errorf("reason %v", done["reason"])
	}
	xml := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body)
	if !strings.Contains(xml, `failures="1"`) || !strings.Contains(xml, `<failure message="no connection matching tcp/8883 was established`) {
		t.Errorf("the JUnit file:\n%s", xml)
	}
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// A fault that matched no packet is a warning of the report and of the JUnit output; the rule that was hit is not. With
// `zero_hit: error` the run that would have passed fails.
func TestAFaultThatMatchedNoPacketIsAZeroHitWarningOverTheAPI(t *testing.T) {
	g, clk := readyRuns(t)
	hit := func(name, zero string) string {
		body := inlineRun(name, iotTarget,
			step("slow", "0s", "fault", map[string]any{"latency": "100ms"}),
			step("cut", "3s", "rule", map[string]any{"action": "drop", "protocol": "tcp", "ports": []int{8883}}),
			step("end", "6s", "restore", true))
		if zero != "" {
			body["inline"].(map[string]any)["zero_hit"] = zero
		}
		id := g.postRun(body, "").json(t)["id"].(string)
		g.waitStep(id, "slow", "applied")
		// the fault is hit, the rule is not (the simulated kernel counts what a test bumps)
		for _, f := range g.e.Snapshot().Faults {
			g.k.BumpCounter(f.CounterUp, 4)
		}
		clk.Advance(3 * time.Second)
		g.waitStep(id, "cut", "applied")
		clk.Advance(3 * time.Second)
		return id
	}
	id := hit("zero-hit", "")
	done := g.waitRun(id, "passed")
	// (a step_late is no part of the question: the engine of this test runs on the real clock)
	ws := warningsOf(done, "zero_hit")
	if len(warningCodes(done)) != 1 || len(ws) != 1 || ws[0]["step"] != "cut" || ws[0]["overlay"] == nil {
		t.Fatalf("warnings %v", done["warnings"])
	}
	if x := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body); !strings.Contains(x, "warning zero_hit: the rule of step cut matched no packet") || strings.Contains(x, "<failure") {
		t.Errorf("the JUnit file:\n%s", x)
	}

	id = hit("zero-hit-strict", "error")
	done = g.waitRun(id, "failed")
	if !strings.Contains(done["reason"].(string), "zero hits: the overlay of step cut matched no packet") {
		t.Errorf("reason %v", done["reason"])
	}
	if x := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body); !strings.Contains(x, `<testcase name="zero hits of step cut"`) {
		t.Errorf("the JUnit file:\n%s", x)
	}
}

// `*.suffix` of a `dns_query_seen` check is the spec's Hostname: the subdomains of the suffix, not the suffix itself.
func TestAWildcardDNSCheckMatchesTheSubdomainsAndNotTheSuffix(t *testing.T) {
	g, clk, service := checksGateway(t)
	body := inlineRun("wild", map[string]any{"device": "esp32-42"}, step("a", "0s", "fault", map[string]any{"latency": "10ms"}))
	sc := body["inline"].(map[string]any)
	sc["preconditions"] = map[string]any{"skip": []string{"target_online", "uses_gateway_dns"}}
	sc["checks"] = []any{map[string]any{"dns_query_seen": map[string]any{"name": "*.example.com"}, "window": map[string]any{"from": "start", "within": "1h"}}}
	id := g.postRun(body, "").json(t)["id"].(string)
	g.waitStep(id, "a", "applied")
	tick := func(n int) {
		for i := 0; i < n; i++ {
			clk.Advance(250 * time.Millisecond)
			time.Sleep(3 * time.Millisecond)
		}
	}
	g.postQuery(service, clk.Now(), "10.10.0.31", "example.com")
	tick(8)
	if c := g.run(id)["checks"].([]any)[0].(map[string]any); c["status"] != "pending" {
		t.Fatalf("the suffix itself decided the check: %v", c)
	}
	g.postQuery(service, clk.Now(), "10.10.0.31", "broker.example.com")
	eventuallyTrue(t, "the check to pass", func() bool {
		tick(1)
		return g.run(id)["checks"].([]any)[0].(map[string]any)["status"] == "passed"
	})
	g.waitRun(id, "passed")
}
