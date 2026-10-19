package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
)

// M15: scenarios and runs over the API, on the simulated kernel with the real engine. The clock of the API
// (and with it the schedule of the runs) is a fake one the tests move. Every response is checked against the spec.

// runClock makes a gateway whose runs keep time with a fake clock.
func readyRuns(t *testing.T) (*gw, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	g := newGW(t, func(o *options) { o.apiClock = clk })
	g.finishSetup()
	return g, clk
}

func eventuallyTrue(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (g *gw) postRun(body any, query string) resp {
	g.t.Helper()
	return g.do("POST", "/runs"+query, body, nil, nil)
}

func (g *gw) run(id string) map[string]any {
	g.t.Helper()
	r := g.do("GET", "/runs/"+id, nil, nil, nil)
	if r.Status != 200 {
		g.t.Fatalf("GET /runs/%s: %d %s", id, r.Status, r.Body)
	}
	return r.json(g.t)
}

func (g *gw) waitRun(id string, statuses ...string) map[string]any {
	g.t.Helper()
	var r map[string]any
	eventuallyTrue(g.t, "run "+id+" "+statuses[0], func() bool {
		r = g.run(id)
		for _, s := range statuses {
			if r["status"] == s {
				return true
			}
		}
		return false
	})
	return r
}

func stepOf(run map[string]any, id string) map[string]any {
	for _, s := range run["steps"].([]any) {
		if m := s.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func (g *gw) waitStep(id, step, status string) map[string]any {
	g.t.Helper()
	var s map[string]any
	eventuallyTrue(g.t, "step "+step+" "+status, func() bool {
		s = stepOf(g.run(id), step)
		return s != nil && s["status"] == status
	})
	return s
}

func (g *gw) overlaysOf(runID string) []any {
	g.t.Helper()
	return g.do("GET", "/overlays?owner="+runID, nil, nil, nil).json(g.t)["items"].([]any)
}

// inlineRun is the body of a run of a scenario that is sent with the request.
func inlineRun(name string, target map[string]any, steps ...map[string]any) map[string]any {
	st := make([]any, len(steps))
	for i, s := range steps {
		st[i] = s
	}
	return map[string]any{"inline": map[string]any{
		"name": name, "target": target, "preconditions": map[string]any{"skip": []string{"target_online"}}, "steps": st,
	}}
}

func step(id, at string, kv ...any) map[string]any {
	m := map[string]any{"id": id, "at": at}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

var iotTarget = map[string]any{"network": "IoT"}

// A run from a file: its steps are applied at their times, its overlays are the run's, the kernel has them while
// the run lasts and not afterwards, and the report, the JUnit file and the events are there at the end. The active
// revision does not change.
func TestARunFromAFileAppliesItsStepsAndLeavesAReport(t *testing.T) {
	g, clk := readyRuns(t)
	rev := g.activeID()
	r := g.postRun(inlineRun("api-run", iotTarget,
		step("slow", "0s", "fault", map[string]any{"latency": "100ms"}),
		step("cut", "10s", "rule", map[string]any{"action": "drop", "protocol": "tcp", "ports": []int{8883}}),
		step("end", "20s", "restore", true),
	), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	run := r.json(t)
	id := run["id"].(string)
	if r.Header.Get("Location") != "/api/v1/runs/"+id || run["scenario"].(map[string]any)["inline"] != true {
		t.Errorf("%v %v", r.Header, run)
	}
	if owner := run["owner"].(map[string]any); owner["type"] != "token" {
		t.Errorf("owner %v", owner)
	}

	g.waitStep(id, "slow", "applied")
	ovs := g.overlaysOf(id)
	if len(ovs) != 1 {
		t.Fatalf("overlays of the run: %v", ovs)
	}
	ov := ovs[0].(map[string]any)
	if ov["kind"] != "fault" || ov["owner"].(map[string]any)["type"] != "run" || ov["owner"].(map[string]any)["id"] != id ||
		ov["run"].(map[string]any)["id"] != id || ov["run"].(map[string]any)["step"] != "slow" {
		t.Errorf("the overlay of a step: %v", ov)
	}
	if n := len(g.e.Snapshot().Faults); n != 1 {
		t.Errorf("the kernel has %d faults while the run lasts", n)
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["runs_active"] != float64(1) {
		t.Errorf("state %v", st)
	}
	if b := g.do("GET", "/system/busy", nil, nil, nil).json(t); b["busy"] != true || b["runs_active"] != float64(1) || len(b["run_ids"].([]any)) != 1 {
		t.Errorf("busy %v", b)
	}

	clk.Advance(10 * time.Second)
	cut := g.waitStep(id, "cut", "applied")
	if cut["offset_ms"] != float64(10000) || cut["generation"] == nil || cut["queue_wait_ms"] == nil {
		t.Errorf("step %v", cut)
	}
	if n := len(g.overlaysOf(id)); n != 2 {
		t.Errorf("%d overlays after the rule", n)
	}
	clk.Advance(10 * time.Second)
	done := g.waitRun(id, "passed")
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
	if n := len(g.e.Snapshot().Faults); n != 0 {
		t.Errorf("the kernel keeps %d faults", n)
	}
	if done["ended_at"] == nil || done["started_at"] == nil || len(done["artifacts"].([]any)) != 3 {
		t.Errorf("%v", done)
	}
	if g.activeID() != rev {
		t.Errorf("a run changed the active revision: %d, was %d", g.activeID(), rev)
	}

	rep := g.do("GET", "/runs/"+id+"/report.json", nil, nil, nil)
	if rep.Status != 200 {
		t.Fatalf("%d %s", rep.Status, rep.Body)
	}
	body := rep.json(t)
	if len(body["steps"].([]any)) != 3 || body["snapshot"] == nil {
		t.Errorf("%v", body)
	}
	for _, s := range body["steps"].([]any) {
		m := s.(map[string]any)
		if m["queue_wait_ms"] == nil || m["generation"] == nil || m["applied_at"] == nil {
			t.Errorf("a step of the report lacks its timing: %v", m)
		}
	}
	if counters, _ := body["counters"].([]any); len(counters) == 0 {
		t.Errorf("the report has no counters of the overlays: %v", body)
	}
	xmlRep := g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil)
	if xmlRep.Status != 200 || !strings.HasPrefix(xmlRep.Header.Get("Content-Type"), "application/xml") ||
		!strings.Contains(string(xmlRep.Body), `<testsuite name="api-run"`) {
		t.Errorf("%d %s", xmlRep.Status, xmlRep.Body)
	}
	ev := g.do("GET", "/runs/"+id+"/events", nil, nil, nil)
	if ev.Status != 200 || !strings.HasPrefix(ev.Header.Get("Content-Type"), "application/jsonl") {
		t.Fatalf("%d %s", ev.Status, ev.Body)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(string(ev.Body)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		if e["run"] != id {
			t.Errorf("an event of another run: %s", line)
		}
		types = append(types, e["type"].(string))
	}
	joined := strings.Join(types, ",")
	for _, want := range []string{"run_started", "overlay_created", "run_step", "overlay_removed", "run_finished"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %s in the events of the run: %s", want, joined)
		}
	}
	if types[0] != "run_started" || types[len(types)-1] != "run_finished" {
		t.Errorf("the events are not in order: %s", joined)
	}

	// the lists
	list := g.do("GET", "/runs?scenario=api-run&status=passed", nil, nil, nil).json(t)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["steps"] != nil {
		t.Errorf("the list of runs has the timeline, or misses the run: %v", list)
	}
	if n := len(g.do("GET", "/runs?status=running", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("%d running", n)
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["runs_active"] != float64(0) {
		t.Errorf("state %v", st)
	}
	// the audit log
	e, _, _ := g.log.List(auditFilter(), "", 20)
	var actions []string
	for _, x := range e {
		actions = append(actions, x.Action)
	}
	if !strings.Contains(strings.Join(actions, ","), "run.create") {
		t.Errorf("audit actions %v", actions)
	}
}

// A run goes on when the client that started it is gone: the request that made it is not what the run is.
func TestARunKeepsGoingWhenTheRequestIsOver(t *testing.T) {
	g, clk := readyRuns(t)
	// The request carries a context that is cancelled, and its connection closed, as soon as the
	// response is read: the server sees the request end while the run is still waiting for step b.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := g.do("POST", "/runs", inlineRun("go-on", iotTarget,
		step("a", "0s", "fault", map[string]any{"latency": "20ms"}),
		step("b", "30s", "fault", map[string]any{"loss": "1%"}),
	), nil, func(req *http.Request) { *req = *req.WithContext(ctx) })
	id := r.json(t)["id"].(string)
	cancel()
	g.client.CloseIdleConnections()
	g.waitStep(id, "a", "applied")
	clk.Advance(30 * time.Second)
	g.waitRun(id, "passed")
}

func TestAStoredScenarioIsListedFetchedAsYAMLAndRunByName(t *testing.T) {
	g, clk := readyRuns(t)
	scID := "3d4e5f6a-7b8c-4d9e-0f1a-2b3c4d5e6f71"
	rev := g.mustPatch(map[string]any{"scenarios": map[string]any{scID: map[string]any{
		"name": "stored", "target": iotTarget, "preconditions": map[string]any{"skip": []string{"target_online"}},
		"steps": []any{step("lat", "0s", "fault", map[string]any{"latency": "50ms"}), step("done", "5s", "restore", true)},
	}}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}

	list := g.do("GET", "/scenarios", nil, nil, nil).json(t)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != scID || list[0].(map[string]any)["config"].(map[string]any)["name"] != "stored" {
		t.Fatalf("%v", list)
	}
	for _, ref := range []string{scID, "stored"} {
		if r := g.do("GET", "/scenarios/"+ref, nil, nil, nil); r.Status != 200 || r.json(t)["id"] != scID {
			t.Errorf("%s: %d %s", ref, r.Status, r.Body)
		}
	}
	y := g.do("GET", "/scenarios/stored", nil, map[string]string{"Accept": "application/yaml"}, nil)
	if y.Status != 200 || !strings.HasPrefix(y.Header.Get("Content-Type"), "application/yaml") || !strings.Contains(string(y.Body), "name: stored") {
		t.Fatalf("%d %s %s", y.Status, y.Header.Get("Content-Type"), y.Body)
	}
	if r := g.do("GET", "/scenarios/nope", nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("%d %s", r.Status, r.Body)
	}

	// the YAML is a scenario file: the import format reads it back
	if sc, err := domain.DecodeScenario(y.Body, domain.FormatYAML); err != nil || sc.Name != "stored" || len(sc.Steps) != 2 {
		t.Fatalf("the YAML of a scenario does not read back: %v", err)
	}
	// a run of the stored scenario, with a parameter that changes the latency
	r := g.do("POST", "/scenarios/stored/runs", map[string]any{"parameters": map[string]string{"steps.lat.fault.latency": "75ms"}, "lease": "1m"}, nil, nil)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	run := r.json(t)
	id := run["id"].(string)
	if run["scenario"].(map[string]any)["id"] != scID || run["scenario"].(map[string]any)["inline"] != false || run["lease_expires_at"] == nil {
		t.Errorf("%v", run)
	}
	g.waitStep(id, "lat", "applied")
	ov := g.overlaysOf(id)[0].(map[string]any)
	if ov["fault"].(map[string]any)["latency"] != "75ms" {
		t.Errorf("the parameter did not reach the overlay: %v", ov)
	}
	g.waitRun(id, "running")
	clk.Advance(5 * time.Second)
	done := g.waitRun(id, "passed")
	if done["snapshot"].(map[string]any)["steps"].([]any)[0].(map[string]any)["fault"].(map[string]any)["latency"] != "75ms" {
		t.Errorf("the snapshot is not the scenario as executed: %v", done["snapshot"])
	}
	// the stored scenario itself is unchanged
	got := g.do("GET", "/scenarios/stored", nil, nil, nil).json(t)["config"].(map[string]any)
	if got["steps"].([]any)[0].(map[string]any)["fault"].(map[string]any)["latency"] != "50ms" {
		t.Errorf("the run changed the stored scenario: %v", got)
	}
	// a run by name through POST /runs
	r2 := g.postRun(map[string]any{"scenario": "stored"}, "")
	if r2.Status != 201 {
		t.Fatalf("%d %s", r2.Status, r2.Body)
	}
	g.do("POST", "/runs/"+r2.json(t)["id"].(string)+"/abort", nil, nil, nil)
}

func TestARunIsRefusedForWhatTheBuildDoesNotHaveAndForWhatDoesNotFit(t *testing.T) {
	g, _ := readyRuns(t)
	tls := inlineRun("c", iotTarget, step("a", "0s", "fault", map[string]any{"latency": "10ms"}))
	tls["inline"].(map[string]any)["checks"] = []any{map[string]any{
		"tls_rejected": map[string]any{"protocol": "tcp", "ports": []int{8883}}, "window": map[string]any{"from": "a", "within": "30s"}}}
	if r := g.postRun(tls, ""); r.Status != 422 || r.code(t) != "unsupported_feature" || !strings.Contains(string(r.Body), "M21") {
		t.Errorf("a TLS check: %d %s", r.Status, r.Body)
	}
	named := inlineRun("c", iotTarget, step("a", "0s", "fault", map[string]any{"latency": "10ms"}))
	named["inline"].(map[string]any)["checks"] = []any{map[string]any{
		"reconnected": map[string]any{"destination": map[string]any{"hostname": "broker.example.com"}}, "window": map[string]any{"from": "a", "within": "30s"}}}
	if r := g.postRun(named, ""); r.Status != 422 || r.code(t) != "unsupported_feature" || !strings.Contains(string(r.Body), "M20") {
		t.Errorf("a check for a host name: %d %s", r.Status, r.Body)
	}
	dns := inlineRun("d", iotTarget, step("a", "0s", "dns", map[string]any{"action": "servfail", "names": []string{"x.example.com"}}))
	if r := g.postRun(dns, ""); r.Status != 422 || r.code(t) != "unsupported_feature" || !strings.Contains(string(r.Body), "M20") {
		t.Errorf("dns: %d %s", r.Status, r.Body)
	}
	bad := inlineRun("b", map[string]any{"device": "nobody"}, step("a", "0s", "fault", map[string]any{"latency": "10ms"}))
	r := g.postRun(bad, "")
	if r.Status != 422 || r.code(t) != "validation_failed" || !strings.Contains(string(r.Body), "unknown_reference") {
		t.Errorf("unknown device: %d %s", r.Status, r.Body)
	}
	for name, body := range map[string]any{
		"neither":       map[string]any{},
		"both":          map[string]any{"scenario": "x", "inline": inlineRun("x", iotTarget, step("a", "0s", "restore", true))["inline"]},
		"unknown field": map[string]any{"scenario": "x", "bogus": 1},
		"short lease":   map[string]any{"scenario": "x", "lease": "500ms"},
		"bad parameter": map[string]any{"inline": inlineRun("x", iotTarget, step("a", "0s", "restore", true))["inline"], "parameters": map[string]string{"nope.nope": "1"}},
	} {
		g.badRequest = true
		r := g.postRun(body, "")
		g.badRequest = false
		if r.Status != 422 || r.code(t) != "validation_failed" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := g.postRun(map[string]any{"scenario": "nope"}, ""); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("unknown scenario: %d %s", r.Status, r.Body)
	}
	if n := len(g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("a refused run made %d runs", n)
	}
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	steps := strings.Join(toStrings(caps["step_types"].([]any)), ",")
	if steps != "profile,fault,rule,wireguard,capture,wait,remove,restore" ||
		strings.Join(toStrings(caps["check_types"].([]any)), ",") != "reconnected,no_connection,traffic_seen,traffic_not_seen,dns_query_seen" {
		t.Errorf("capabilities %v", caps)
	}
	if feats := strings.Join(toStrings(caps["features"].([]any)), ","); !strings.Contains(feats, "runs") || !strings.Contains(feats, "checks") || !strings.Contains(feats, "captures") {
		t.Errorf("features %v", caps["features"])
	}
}

func toStrings(in []any) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = v.(string)
	}
	return out
}

// One run per target: the second run on the target waits in queued, with queue=false it is refused with 409
// target_busy, and aborting the first starts the second.
func TestASecondRunOnTheSameTargetWaitsAndTheQueueFalseRefusesIt(t *testing.T) {
	g, _ := readyRuns(t)
	body := func(name string) map[string]any {
		return inlineRun(name, iotTarget, step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true))
	}
	first := g.postRun(body("first"), "").json(t)["id"].(string)
	g.waitStep(first, "a", "applied")
	second := g.postRun(body("second"), "")
	if second.Status != 201 || second.json(t)["status"] != "queued" {
		t.Fatalf("%d %s", second.Status, second.Body)
	}
	secondID := second.json(t)["id"].(string)
	r := g.postRun(body("third"), "?queue=false")
	if r.Status != 409 || r.code(t) != "target_busy" || !strings.Contains(string(r.Body), first) {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if b := g.do("GET", "/system/busy", nil, nil, nil).json(t); b["runs_active"] != float64(1) || b["runs_queued"] != float64(1) {
		t.Errorf("busy %v", b)
	}
	if n := len(g.overlaysOf(secondID)); n != 0 {
		t.Errorf("the queued run has %d overlays", n)
	}

	ab := g.do("POST", "/runs/"+first+"/abort", nil, nil, nil)
	if ab.Status != 200 || ab.json(t)["status"] != "aborted" || ab.Header.Get("Chaos-Generation") == "" {
		t.Fatalf("%d %s", ab.Status, ab.Body)
	}
	if n := len(g.overlaysOf(first)); n != 0 {
		t.Errorf("the aborted run left %d overlays", n)
	}
	g.waitStep(secondID, "a", "applied")
	if g.run(secondID)["status"] != "running" {
		t.Error("the queued run did not start")
	}
	g.do("POST", "/runs/"+secondID+"/abort", nil, nil, nil)
	if n := len(g.e.Snapshot().Overlays); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// A failed precondition ends the run as error with the reason; here nothing is online on the network.
func TestAFailedPreconditionEndsTheRunAsErrorOverTheAPI(t *testing.T) {
	g, _ := readyRuns(t)
	r := g.postRun(map[string]any{"inline": map[string]any{
		"name": "baseline", "target": iotTarget, "steps": []any{step("a", "0s", "fault", map[string]any{"latency": "10ms"})}}}, "")
	id := r.json(t)["id"].(string)
	done := g.waitRun(id, "error")
	if !strings.Contains(done["reason"].(string), "precondition target_online failed") {
		t.Errorf("reason %v", done["reason"])
	}
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays", n)
	}
	x := g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil)
	if x.Status != 200 || !strings.Contains(string(x.Body), `type="run_error"`) {
		t.Errorf("%d %s", x.Status, x.Body)
	}
}

func TestTheLeaseOfARunIsRenewedOverTheAPIAndExpires(t *testing.T) {
	g, clk := readyRuns(t)
	r := g.postRun(withLease(inlineRun("leased", iotTarget,
		step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true)), "30s"), "")
	id := r.json(t)["id"].(string)
	g.waitStep(id, "a", "applied")
	clk.Advance(20 * time.Second)
	rn := g.do("POST", "/runs/"+id+"/renew", nil, nil, nil)
	if rn.Status != 200 || rn.json(t)["lease_expires_at"] != "2026-10-18T12:00:50Z" {
		t.Fatalf("%d %s", rn.Status, rn.Body)
	}
	clk.Advance(29 * time.Second)
	if g.run(id)["status"] != "running" {
		t.Fatal("the run ended inside its lease")
	}
	// The lease timer cancels the run inside Advance, but the run says `running` until it has cleaned up, so the status
	// proves nothing yet; a renewal is refused for a cancelled run, so this one works only if the first deadline (30 s)
	// was moved, and it moves the deadline again, to 12:01:19.
	rn = g.do("POST", "/runs/"+id+"/renew", nil, nil, nil)
	if rn.Status != 200 || rn.json(t)["lease_expires_at"] != "2026-10-18T12:01:19Z" {
		t.Fatalf("the renewal inside the lease: %d %s", rn.Status, rn.Body)
	}
	clk.Advance(30 * time.Second)
	done := g.waitRun(id, "aborted")
	if done["reason"] != "lease expired" {
		t.Errorf("reason %v", done["reason"])
	}
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays", n)
	}
	if r := g.do("POST", "/runs/"+id+"/renew", nil, nil, nil); r.Status != 422 || r.code(t) != "validation_failed" {
		t.Errorf("renewing an ended run: %d %s", r.Status, r.Body)
	}
	plain := g.postRun(inlineRun("nolease", iotTarget, step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true)), "").json(t)["id"].(string)
	if r := g.do("POST", "/runs/"+plain+"/renew", nil, nil, nil); r.Status != 422 {
		t.Errorf("renewing a run without a lease: %d %s", r.Status, r.Body)
	}
	g.do("POST", "/runs/"+plain+"/abort", nil, nil, nil)
}

// The runs that are queued or running are limited; one more is `capacity_exceeded` (422) and frees up when a run ends.
func TestTooManyRunsAreRefusedAsCapacityExceeded(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	g := newGW(t, func(o *options) { o.apiClock, o.maxRuns = clk, 1 })
	g.finishSetup()
	hold := func(name string) resp {
		return g.postRun(inlineRun(name, iotTarget, step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true)), "?queue=true")
	}
	first := hold("first")
	if first.Status != 201 {
		t.Fatalf("%d %s", first.Status, first.Body)
	}
	id := first.json(t)["id"].(string)
	if r := hold("second"); r.Status != 422 || r.code(t) != "capacity_exceeded" {
		t.Fatalf("a second run with a limit of 1: %d %s", r.Status, r.Body)
	}
	if n := len(g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any)); n != 1 {
		t.Errorf("the refused run left %d runs", n)
	}
	g.do("POST", "/runs/"+id+"/abort", nil, nil, nil)
	if r := hold("third"); r.Status != 201 {
		t.Errorf("after the first run ended: %d %s", r.Status, r.Body)
	} else {
		g.do("POST", "/runs/"+r.json(t)["id"].(string)+"/abort", nil, nil, nil)
	}
}

// The record of an ended run is kept as long as `settings.retention.runs` of the active configuration says (the flag
// --run-retention, 30 days, is only the default for a configuration that does not set it), and a change of the setting
// counts for the runs that ended before it.
func TestTheRetentionOfRunsIsTheSettingOfTheActiveConfiguration(t *testing.T) {
	g, clk := readyRuns(t)
	finished := func(name string) string {
		t.Helper()
		id := g.postRun(inlineRun(name, iotTarget, step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "0s", "restore", true)), "").json(t)["id"].(string)
		g.waitRun(id, "passed")
		return id
	}
	gone := func(id string) bool { return g.do("GET", "/runs/"+id, nil, nil, nil).Status == 404 }
	// the run says `passed` a moment before the manager has pruned, so a record that must stay is watched for a while and
	// one that must go is waited for
	stays := func(id, why string) {
		t.Helper()
		for i := 0; i < 30; i++ {
			if gone(id) {
				t.Fatal(why)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	setRetention := func(d string) {
		t.Helper()
		if r := g.apply(g.mustPatch(map[string]any{"settings": map[string]any{"retention": map[string]any{"runs": d}}})); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}

	// longer than the 30 days of the default: the record outlives them
	setRetention("2160h")
	first := finished("first")
	clk.Advance(31 * 24 * time.Hour)
	second := finished("second") // the end of a run prunes
	stays(first, "the record of a run was deleted after 31 days, with a retention of 90 days")

	// shorter: both older runs go when the next one ends
	setRetention("24h")
	clk.Advance(25 * time.Hour)
	third := finished("third")
	eventuallyTrue(t, "the records older than the retention of 24 h to go", func() bool { return gone(first) && gone(second) })
	stays(third, "the record of the run that just ended was deleted")
}

func withLease(body map[string]any, lease string) map[string]any {
	body["lease"] = lease
	return body
}

// Tokens: reading needs read, starting a run needs overlays, and a token with overlays aborts and renews its own
// runs only; reset takes the caller's runs and owner=all needs full.
func TestRunsFollowTheTokenScopesAndResetStopsTheCallersRuns(t *testing.T) {
	g, _ := readyRuns(t)
	admin := g.token
	mkRun := func(name string, target map[string]any) string {
		r := g.postRun(inlineRun(name, target, step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true)), "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		return r.json(t)["id"].(string)
	}
	g.mintToken("read")
	if r := g.postRun(inlineRun("x", iotTarget, step("a", "0s", "restore", true)), ""); r.Status != 403 {
		t.Errorf("a read-only token starts a run: %d", r.Status)
	}
	if r := g.do("GET", "/runs", nil, nil, nil); r.Status != 200 {
		t.Errorf("a read-only token lists runs: %d", r.Status)
	}
	readTok := g.token

	g.mintToken("overlays")
	mine := mkRun("mine", iotTarget)
	g.waitStep(mine, "a", "applied")
	other := g.mintToken("overlays")
	_ = other
	if r := g.do("POST", "/runs/"+mine+"/abort", nil, nil, nil); r.Status != 403 || r.code(t) != "forbidden" {
		t.Errorf("aborting another token's run: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/runs/"+mine+"/renew", nil, nil, nil); r.Status != 403 {
		t.Errorf("renewing another token's run: %d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/runs/"+mine, nil, nil, nil); r.Status != 200 {
		t.Errorf("reading another token's run: %d", r.Status)
	}
	// reset takes only the caller's runs (this token has none)
	if r := g.do("POST", "/reset", nil, nil, nil); r.Status != 200 || r.json(t)["aborted_runs"] != float64(0) {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if g.run(mine)["status"] != "queued" && g.run(mine)["status"] != "running" {
		t.Errorf("another token's reset stopped the run: %v", g.run(mine)["status"])
	}
	if r := g.do("POST", "/reset?owner=all", nil, nil, nil); r.Status != 403 {
		t.Errorf("owner=all with the scope overlays: %d", r.Status)
	}
	// the admin token (scope full) aborts any run; reset?owner=all aborts the rest
	g.token = admin
	own := g.postRun(inlineRun("own", map[string]any{"network": "lab-hub"}, step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true)), "").json(t)["id"].(string)
	g.waitStep(own, "a", "applied")
	r := g.do("POST", "/reset", nil, nil, nil)
	if r.Status != 200 || r.json(t)["aborted_runs"] != float64(1) || r.json(t)["removed_overlays"] != float64(1) {
		t.Fatalf("reset of the admin token: %d %s", r.Status, r.Body)
	}
	if st := g.run(own); st["status"] != "aborted" {
		t.Errorf("%v", st["status"])
	}
	if g.run(mine)["status"] != "running" {
		t.Errorf("the other token's run was stopped by the admin's own reset")
	}
	r = g.do("POST", "/reset?owner=all", nil, nil, nil)
	if r.Status != 200 || r.json(t)["aborted_runs"] != float64(1) || r.json(t)["removed_overlays"] != float64(1) {
		t.Fatalf("reset of all: %d %s", r.Status, r.Body)
	}
	if st := g.run(mine); st["status"] != "aborted" {
		t.Errorf("%v", st["status"])
	}
	_ = readTok
	if n := len(g.e.Snapshot().Overlays); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// A repeated POST /runs with the same Idempotency-Key is the same run.
func TestAnIdempotencyKeyMakesOneRun(t *testing.T) {
	g, _ := readyRuns(t)
	body := inlineRun("once", iotTarget, step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true))
	h := map[string]string{"Idempotency-Key": "run-key-1"}
	a := g.do("POST", "/runs", body, h, nil)
	b := g.do("POST", "/runs", body, h, nil)
	if a.Status != 201 || b.Status != 201 || a.json(t)["id"] != b.json(t)["id"] {
		t.Fatalf("%d %s / %d %s", a.Status, a.Body, b.Status, b.Body)
	}
	if n := len(g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any)); n != 1 {
		t.Errorf("%d runs", n)
	}
	g.do("POST", "/runs/"+a.json(t)["id"].(string)+"/abort", nil, nil, nil)
}

// A revision that deletes an object a run refers to is refused with the run listed, the preview says so, and
// forcing it aborts the run (plan §2.1.1).
func TestARevisionThatDeletesWhatARunRefersToIsRefusedUnlessForced(t *testing.T) {
	g, _ := readyRuns(t)
	devID := "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	add := g.mustPatch(map[string]any{"devices": map[string]any{devID: map[string]any{
		"name": "esp32-42", "identifiers": map[string]any{"macs": []string{"02:00:00:00:00:31"}},
	}}})
	if r := g.apply(add); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	runBody := inlineRun("refers", map[string]any{"device": "esp32-42"},
		step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true))
	id := g.postRun(runBody, "").json(t)["id"].(string)
	g.waitStep(id, "a", "applied")
	queued := g.postRun(inlineRun("waits", map[string]any{"device": "esp32-42"}, step("a", "0s", "restore", true)), "").json(t)["id"].(string)

	del := g.mustPatch(map[string]any{"devices": map[string]any{devID: nil}})
	pv := g.do("POST", "/revisions/"+itoa(del)+"/preview", nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	prefs, _ := pv.json(t)["references"].([]any)
	if len(prefs) != 3 { // the run, the queued run, and the run's overlay
		t.Fatalf("preview references %s", pv.Body)
	}
	r := g.apply(del)
	if r.Status != 422 || r.code(t) != "validation_failed" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	kinds := map[string]int{}
	ids := map[string]bool{}
	for _, x := range r.json(t)["references"].([]any) {
		ref := x.(map[string]any)
		kinds[ref["kind"].(string)]++
		ids[ref["id"].(string)] = true
		if ref["object"] != "/devices/"+devID {
			t.Errorf("reference %v", ref)
		}
	}
	if kinds["run"] != 2 || kinds["overlay"] != 1 || !ids[id] || !ids[queued] {
		t.Errorf("references %v %v", kinds, ids)
	}
	if g.run(id)["status"] != "running" || g.activeID() != add {
		t.Error("a refused revision changed something")
	}

	r = g.do("POST", "/revisions/"+itoa(del)+"/apply?force=true", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// the result names the runs it aborted, in the order they were made; their overlays went with them
	if got, _ := r.json(t)["aborted_runs"].([]any); len(got) != 2 || got[0] != id || got[1] != queued {
		t.Errorf("aborted_runs %v, want [%s %s]", got, id, queued)
	}
	for _, rid := range []string{id, queued} {
		got := g.run(rid)
		if got["status"] != "aborted" || !strings.Contains(got["reason"].(string), "deletes /devices/"+devID) {
			t.Errorf("run %s: %v %v", rid, got["status"], got["reason"])
		}
	}
	if n := len(g.e.Snapshot().Overlays); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// A forced apply that the engine refuses at once aborts no run: with another revision waiting for confirmation the runs
// go on and the answer is the engine's own (confirm_pending), also without force, and so they do when the candidate is
// gone. (The not-a-candidate and base checks of the engine have their own test, TestCheckApplicable.)
func TestARefusedForcedApplyLeavesTheRunsAlone(t *testing.T) {
	g, _ := readyRuns(t)
	devID := "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	add := g.mustPatch(map[string]any{"devices": map[string]any{devID: map[string]any{
		"name": "esp32-42", "identifiers": map[string]any{"macs": []string{"02:00:00:00:00:31"}},
	}}})
	if r := g.apply(add); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := g.postRun(inlineRun("refers", map[string]any{"device": "esp32-42"},
		step("a", "0s", "fault", map[string]any{"latency": "10ms"}), step("b", "1h", "restore", true)), "").json(t)["id"].(string)
	g.waitStep(id, "a", "applied")

	// both candidates are based on the active revision: the first deletes the device, the second waits for confirmation
	del := g.mustPatch(map[string]any{"devices": map[string]any{devID: nil}})
	lock := g.mustPatch(map[string]any{"management": map[string]any{"allowed_sources": []any{"192.168.56.0/24", "172.16.0.0/12"}}})
	if r := g.apply(lock); r.Status != 200 || r.json(t)["status"] != "pending_confirm" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	stays := func(when string) {
		t.Helper()
		if got := g.run(id); got["status"] != "running" || stepOf(got, "a")["status"] != "applied" {
			t.Errorf("%s: the run is %v", when, got["status"])
		}
		if n := len(g.overlaysOf(id)); n != 1 {
			t.Errorf("%s: %d overlays of the run", when, n)
		}
	}
	for _, q := range []string{"?force=true", ""} {
		if r := g.do("POST", "/revisions/"+itoa(del)+"/apply"+q, nil, nil, nil); r.Status != 409 || r.code(t) != "confirm_pending" {
			t.Fatalf("apply%s with a revision waiting for confirmation: %d %s", q, r.Status, r.Body)
		}
		stays("pending confirmation" + q)
	}

	// confirmed, the active revision moved on: the store reclaimed the candidate, so it is gone and the runs stay
	if r := g.do("POST", "/revisions/"+itoa(lock)+"/confirm", nil, nil, nil); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/revisions/"+itoa(del)+"/apply?force=true", nil, nil, nil); r.Status != 404 {
		t.Fatalf("apply of a reclaimed candidate: %d %s", r.Status, r.Body)
	}
	stays("reclaimed candidate")
	g.do("POST", "/runs/"+id+"/abort", nil, nil, nil)
}

// The runs of an earlier process: one that was running ends as aborted when the next process starts.
func TestARestartEndsARunThatWasRunningAsAborted(t *testing.T) {
	root := t.TempDir()
	g := newGW(t, func(o *options) { o.root = root })
	g.finishSetup()
	id := "0b1c2d3e-4f5a-4b6c-8d7e-9f0a1b2c3d4e"
	dir := filepath.Join(root, "api", "runs", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := `{"schema_version":1,"run":{"id":"` + id + `","scenario":{"name":"crashed","inline":true},"revision":2,"target":{"network":"0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"},
	"owner":{"type":"token","id":"t1","name":"ci"},"status":"running","created_at":"2026-10-18T11:00:00Z","started_at":"2026-10-18T11:00:00Z",
	"steps":[{"id":"a","status":"applied"},{"id":"b","status":"pending"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
	g.close()

	g2 := newGW(t, func(o *options) { o.root = root })
	g2.token = ""
	g2.mintToken("full")
	r := g2.run(id)
	if r["status"] != "aborted" || r["reason"] != "service restart" || r["ended_at"] == nil {
		t.Fatalf("%v", r)
	}
	if stepOf(r, "b")["status"] != "skipped" || stepOf(r, "a")["status"] != "applied" {
		t.Errorf("%v", r["steps"])
	}
	if rep := g2.do("GET", "/runs/"+id+"/report.json", nil, nil, nil); rep.Status != 200 {
		t.Errorf("the report of the aborted run: %d %s", rep.Status, rep.Body)
	}
	if st := g2.do("GET", "/state", nil, nil, nil).json(t); st["runs_active"] != float64(0) {
		t.Errorf("%v", st)
	}
}

// Every step type of this build runs against the real engine: a profile, a fault, a rule and a WireGuard action
// are the run's overlays, `remove` takes one away and `restore` the rest.
func TestEveryStepTypeOfThisBuildRunsOverTheAPI(t *testing.T) {
	g, clk := readyRuns(t)
	r := g.postRun(inlineRun("kinds", map[string]any{"network": "lab-hub"},
		step("prof", "0s", "profile", "lte"),
		step("lat", "0s", "fault", map[string]any{"latency": "40ms"}),
		step("drop", "0s", "rule", map[string]any{"action": "drop", "protocol": "tcp", "ports": []int{8883}}),
		step("peer", "0s", "wireguard", map[string]any{"client": "rA", "action": "disable"}),
		step("undo", "5s", "remove", "lat"),
		step("all", "10s", "restore", true),
	), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "peer", "applied")
	kinds := map[string]bool{}
	for _, o := range g.overlaysOf(id) {
		kinds[o.(map[string]any)["kind"].(string)] = true
	}
	if len(kinds) != 4 || !kinds["profile"] || !kinds["fault"] || !kinds["rule"] || !kinds["wireguard"] {
		t.Errorf("the overlays of the run: %v", kinds)
	}
	clk.Advance(5 * time.Second)
	g.waitStep(id, "undo", "applied")
	if n := len(g.overlaysOf(id)); n != 3 {
		t.Errorf("%d overlays after the remove", n)
	}
	clk.Advance(5 * time.Second)
	done := g.waitRun(id, "passed")
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays after the restore", n)
	}
	types := map[string]string{}
	for _, s := range done["steps"].([]any) {
		m := s.(map[string]any)
		types[m["id"].(string)] = m["type"].(string)
	}
	if types["prof"] != "profile" || types["lat"] != "fault" || types["drop"] != "rule" || types["peer"] != "wireguard" || types["undo"] != "remove" || types["all"] != "restore" {
		t.Errorf("%v", types)
	}
}
