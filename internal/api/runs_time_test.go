package api_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// M34 over the API: the step `time`, the wait `ntp_query_seen` and the check `ntp_queried` of a scenario read the query log
// that the NTP server posts, on the fake clock of the API; the zero-hit warning and the counters of the run read the number
// of queries that a time fault shaped.

// timeRunGateway is a gateway with the devices esp32-42 (10.10.0.31) and esp32-43 on IoT, the service namespace and the
// token of the service containers (which posts the queries), on a fake clock. The token of the gateway is the service one;
// asAdmin(admin, ...) makes the calls of a client.
func timeRunGateway(t *testing.T) (g *gw, clk *clock.Fake, admin string) {
	t.Helper()
	clk = clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	g, admin = tlsGateway(t, func(o *options) { o.apiClock = clk })
	return g, clk, admin
}

// asService makes the calls of f with the token svc of the service containers (inside asAdmin, which swaps it for the admin's).
func (g *gw) asService(svc string, f func()) {
	cur := g.token
	g.token = svc
	defer func() { g.token = cur }()
	f()
}

// tickRun lets the fake clock run in the steps of a poll until the condition holds, the calls of the condition made as a client.
func (g *gw) tickRun(clk *clock.Fake, what string, cond func() bool) {
	g.t.Helper()
	eventuallyTrue(g.t, what, func() bool {
		if cond() {
			return true
		}
		clk.Advance(250 * time.Millisecond)
		time.Sleep(3 * time.Millisecond)
		return false
	})
}

func timeScenario(name string, steps []map[string]any, checks ...map[string]any) map[string]any {
	body := inlineRun(name, map[string]any{"device": "esp32-42"}, steps...)
	if len(checks) > 0 {
		cs := make([]any, len(checks))
		for i, c := range checks {
			cs[i] = c
		}
		body["inline"].(map[string]any)["checks"] = cs
	}
	return body
}

// The scenario of the examples over the API: the fault is served, the device asks, the wait ends, the fault is taken away and
// the check says that the device asked while the fault was in force. The fault shaped the queries, so it is no zero hit.
func TestATimeScenarioWaitsForTheQueryAndChecksItOverTheAPI(t *testing.T) {
	g, clk, admin := timeRunGateway(t)
	svc := g.token
	body := timeScenario("stale-clock",
		[]map[string]any{
			step("ahead", "0s", "time", map[string]any{"offset": "+400d"}),
			step("polled", "1s", "wait", map[string]any{"for": "ntp_query_seen", "timeout": "60s"}),
			step("back", "3s", "remove", "ahead"),
		},
		map[string]any{"ntp_queried": map[string]any{}, "window": map[string]any{"from": "ahead", "until": "back"}},
	)
	var id, overlay string
	g.asAdmin(admin, func() {
		r := g.postRun(body, "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id = r.json(t)["id"].(string)
		overlay = g.waitStep(id, "ahead", "applied")["overlay"].(string)
		ovs := g.do("GET", "/overlays/"+overlay, nil, nil, nil).json(t)
		if ovs["kind"] != "time" || ovs["time"].(map[string]any)["offset"] != "+400d" {
			t.Errorf("the overlay of the step: %v", ovs)
		}
	})
	// the device of the target asks again and again from now on (the wait is due one second into the timeline); a device that
	// is no part of the target and a query from before the step end neither the wait nor the check
	served := clk.Now().Add(400 * 24 * time.Hour)
	entry := func(client string, at time.Time) map[string]any {
		return ntpEntry(client, 123, at, map[string]any{"answer": "normal", "fault": overlay, "served_time": served.Format(time.RFC3339Nano), "offset_ms": 400 * 24 * 3600 * 1000, "server": "203.0.113.7:123"})
	}
	g.mustPostNTP(entry("10.10.0.32", clk.Now()), ntpEntry("10.10.0.31", 123, clk.Now().Add(-time.Hour), map[string]any{"answer": "normal"}))
	g.asAdmin(admin, func() {
		g.tickRun(clk, "the wait", func() bool {
			g.asService(svc, func() { g.mustPostNTP(entry("10.10.0.31", clk.Now())) })
			return stepOf(g.run(id), "polled")["status"] == "applied"
		})
		g.tickRun(clk, "the end of the run", func() bool { return g.run(id)["status"] != "running" && g.run(id)["status"] != "queued" })
		if done := g.run(id); done["status"] != "passed" {
			t.Fatalf("%v %v %v", done["status"], done["reason"], done["checks"])
		}
	})
	g.asAdmin(admin, func() {
		done := g.run(id)
		c := done["checks"].([]any)[0].(map[string]any)
		if c["status"] != "passed" || c["type"] != "ntp_queried" || !strings.Contains(c["message"].(string), "a device asked the NTP server for the time") {
			t.Errorf("the check: %v", c)
		}
		ev, _ := c["evidence"].([]any)
		if len(ev) != 1 || ev[0].(map[string]any)["kind"] != "ntp_query" || ev[0].(map[string]any)["device"] != espID ||
			ev[0].(map[string]any)["fault"] != overlay || ev[0].(map[string]any)["answer"] != "normal" ||
			ev[0].(map[string]any)["server"] != "203.0.113.7:123" || ev[0].(map[string]any)["offset_ms"] != float64(400*24*3600*1000) {
			t.Errorf("the evidence: %v", c["evidence"])
		}
		if !strings.Contains(c["message"].(string), "(answered: normal, served time +9600h0m0s off)") {
			t.Errorf("the message: %v", c["message"])
		}
		if ws := warningCodes(done); len(ws) != 0 {
			t.Errorf("warnings %v (the fault shaped the queries of the device)", done["warnings"])
		}
		if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
			t.Errorf("%d overlays left", n)
		}
	})
}

// No query: the check fails with the likely causes, the fault is a zero hit, and both are in the report and in the JUnit file.
func TestATimeFaultThatNoQueryMetFailsTheCheckAndIsAZeroHitOverTheAPI(t *testing.T) {
	g, clk, admin := timeRunGateway(t)
	body := timeScenario("silent-device",
		[]map[string]any{step("ahead", "0s", "time", map[string]any{"offset": "-1d"})},
		map[string]any{"ntp_queried": map[string]any{}, "window": map[string]any{"from": "ahead", "within": "3s"}},
	)
	g.asAdmin(admin, func() {
		r := g.postRun(body, "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id := r.json(t)["id"].(string)
		g.tickRun(clk, "the end of the run", func() bool { return g.run(id)["status"] != "running" && g.run(id)["status"] != "queued" })
		done := g.run(id)
		if done["status"] != "failed" {
			t.Fatalf("%v %v", done["status"], done["reason"])
		}
		c := done["checks"].([]any)[0].(map[string]any)
		if c["status"] != "failed" || !strings.Contains(c["message"].(string), "no device of the target asked the gateway's NTP server for the time in the window of 3s") {
			t.Errorf("the check: %v", c)
		}
		ws := warningsOf(done, "zero_hit")
		if len(ws) != 1 || ws[0]["step"] != "ahead" || ws[0]["overlay"] == nil || !strings.Contains(ws[0]["message"].(string), "the time fault of step ahead shaped no answer of the NTP server") {
			t.Errorf("warnings %v", done["warnings"])
		}
		x := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body)
		if !strings.Contains(x, `<testcase name="ntp_queried from ahead within 3s"`) || !strings.Contains(x, "warning zero_hit: the time fault of step ahead shaped no answer of the NTP server") {
			t.Errorf("the JUnit file:\n%s", x)
		}
	})
}

// The built-in preset of the time family is a profile step: the part is a time fault of the device, the queries it shaped
// count for the step and the check sees them.
func TestABuiltInTimePresetAsAProfileStepIsServedAndCountedOverTheAPI(t *testing.T) {
	g, clk, admin := timeRunGateway(t)
	body := timeScenario("preset",
		[]map[string]any{step("y2038", "0s", "profile", "time-2038")},
		map[string]any{"ntp_queried": map[string]any{}, "window": map[string]any{"from": "y2038", "within": "20s"}},
	)
	var id, overlay string
	g.asAdmin(admin, func() {
		r := g.postRun(body, "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id = r.json(t)["id"].(string)
		overlay = g.waitStep(id, "y2038", "applied")["overlay"].(string)
		ov := g.do("GET", "/overlays/"+overlay, nil, nil, nil).json(t)
		if ov["kind"] != "profile" || ov["state"] != "effective" {
			t.Errorf("the overlay of the step: %v", ov)
		}
	})
	g.mustPostNTP(ntpEntry("10.10.0.31", 123, clk.Now(), map[string]any{"answer": "normal", "fault": overlay}))
	g.asAdmin(admin, func() {
		g.tickRun(clk, "the end of the run", func() bool { return g.run(id)["status"] != "running" && g.run(id)["status"] != "queued" })
		done := g.run(id)
		if done["status"] != "passed" {
			t.Fatalf("%v %v %v", done["status"], done["reason"], done["checks"])
		}
		if ws := warningCodes(done); len(ws) != 0 {
			t.Errorf("warnings %v: the part shaped a query", done["warnings"])
		}
	})
}

// A time fault that a newer one of the same scope and device overrides is not in the packet path of the NTP server: it has no
// counters, and that is a zero hit, not a missing number.
func TestATimeFaultThatAnotherOneOverridesIsAZeroHitOverTheAPI(t *testing.T) {
	g, clk, admin := timeRunGateway(t)
	body := timeScenario("overridden",
		[]map[string]any{
			step("ahead", "0s", "time", map[string]any{"offset": "+1d"}),
			step("later", "5s", "fault", map[string]any{"latency": "10ms"}),
		},
	)
	g.asAdmin(admin, func() {
		r := g.postRun(body, "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id := r.json(t)["id"].(string)
		overlay := g.waitStep(id, "ahead", "applied")["overlay"].(string)
		// a newer time fault for the same device takes the family over: the one of the run stays in the store and in force for
		// nobody
		newer := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"-1d"}}`)
		got := g.do("GET", "/overlays/"+overlay, nil, nil, nil).json(t)
		if got["state"] != "overridden" || got["counters"] != nil {
			t.Fatalf("the fault of the run is not overridden without counters: %v", got)
		}
		_ = newer
		g.tickRun(clk, "the end of the run", func() bool { return g.run(id)["status"] != "running" && g.run(id)["status"] != "queued" })
		done := g.run(id)
		// the latency fault of the other step matched no packet either: it is not what is asserted here
		var ws []map[string]any
		for _, w := range warningsOf(done, "zero_hit") {
			if w["step"] == "ahead" {
				ws = append(ws, w)
			}
		}
		if len(ws) != 1 || ws[0]["overlay"] != overlay || !strings.Contains(ws[0]["message"].(string), "the time fault of step ahead shaped no answer of the NTP server") {
			t.Errorf("the overridden time fault is a zero hit: %v", done["warnings"])
		}
	})
}

// A time step with the key of the one before replaces its overlay, which keeps its id and its counter: the queries the first
// step's fault shaped are not the second step's hits.
func TestASecondTimeStepOfTheSameKeyIsJudgedByItsOwnQueries(t *testing.T) {
	g, clk, admin := timeRunGateway(t)
	body := timeScenario("replaced",
		[]map[string]any{
			step("first", "0s", "time", map[string]any{"offset": "+1d"}),
			step("second", "5s", "time", map[string]any{"offset": "+2d"}),
			step("end", "8s", "fault", map[string]any{"latency": "10ms"}),
		},
	)
	var id, overlay string
	g.asAdmin(admin, func() {
		r := g.postRun(body, "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id = r.json(t)["id"].(string)
		overlay = g.waitStep(id, "first", "applied")["overlay"].(string)
	})
	// the first fault shapes a query; the second shapes none
	g.mustPostNTP(ntpEntry("10.10.0.31", 123, clk.Now(), map[string]any{"answer": "normal", "fault": overlay, "offset_ms": 86400000}))
	g.asAdmin(admin, func() {
		g.tickRun(clk, "the second step", func() bool { return stepOf(g.run(id), "second")["status"] == "applied" })
		if o := stepOf(g.run(id), "second")["overlay"]; o != overlay {
			t.Fatalf("the second step has the overlay %v, the first %v: the key does not replace", o, overlay)
		}
		g.tickRun(clk, "the end of the run", func() bool { return g.run(id)["status"] != "running" && g.run(id)["status"] != "queued" })
		done := g.run(id)
		var steps []any
		for _, w := range warningsOf(done, "zero_hit") {
			steps = append(steps, w["step"])
		}
		// the latency fault of the last step matched no packet either
		if fmt.Sprint(steps) != "[second end]" && fmt.Sprint(steps) != "[end second]" {
			t.Errorf("only the second time step and the unrelated last one shaped nothing: %v", done["warnings"])
		}
	})
}
