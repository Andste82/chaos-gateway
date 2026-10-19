package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// M17: the captures of a run over the API, with the real engine, runs and manager of captures; the executor's side is
// the fake of capture_fake_test.go.

var espTarget = map[string]any{"device": "esp32-42"}

// capturing is the body of a run of a scenario that records the whole run.
func capturing(name string, steps ...map[string]any) map[string]any {
	body := inlineRun(name, espTarget, steps...)
	body["inline"].(map[string]any)["capture"] = true
	return body
}

func artifactsOfKind(run map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, a := range run["artifacts"].([]any) {
		if m := a.(map[string]any); m["kind"] == kind {
			out = append(out, m)
		}
	}
	return out
}

// warningCodes are the codes of the warnings of a run, without step_late: the engine of these tests runs on the real
// clock, so any step whose first apply waits more than 100 ms (under -race it does now and then) gets that warning,
// and the tests are about the others. step_late has its deterministic tests in internal/run.
func warningCodes(run map[string]any) []string {
	var out []string
	for _, w := range warningsOf(run, "") {
		if c := w["code"].(string); c != "step_late" {
			out = append(out, c)
		}
	}
	return out
}

// warningsOf are the warnings of a run with the code (all of them for ""), in the order the run recorded them.
func warningsOf(run map[string]any, code string) []map[string]any {
	var out []map[string]any
	ws, _ := run["warnings"].([]any)
	for _, w := range ws {
		if m := w.(map[string]any); code == "" || m["code"] == code {
			out = append(out, m)
		}
	}
	return out
}

// `capture: true`: the capture holds the target's traffic for the whole run and no other, ends with the run, belongs
// to it and is offered in its report; the events of the run tell when it started and stopped.
func TestARunWithCaptureTrueAttachesTheCaptureOfItsTargetToTheReport(t *testing.T) {
	g, clk := capGateway(t)
	r := g.postRun(capturing("rec",
		step("slow", "0s", "profile", "normal"),
		step("end", "10s", "restore", true)), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "slow", "applied")
	if o := g.cap.Last(); len(o.Interfaces) != 1 || o.Interfaces[0].Name != "br-iot" || o.Interfaces[0].Filter.MACs[0] != macESP {
		t.Fatalf("the capture of the run: %+v", o)
	}
	up := tcpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 40000, 8883, 30)
	down := tcpFrame(macGW, macESP, "203.0.113.10", "10.10.0.31", 8883, 40000, 30)
	g.cap.Frame("br-iot", up, false)
	g.cap.Frame("br-iot", udpFrame(macOther, macGW, "10.10.0.50", "203.0.113.10", 1, 2, 5), false)
	g.cap.Frame("br-iot", down, false)
	if g.cap.Running() != 1 {
		t.Fatalf("%d captures run", g.cap.Running())
	}

	clk.BlockUntil(1)
	clk.Advance(10 * time.Second)
	run := g.waitRun(id, "passed")
	if g.cap.Running() != 0 {
		t.Error("the capture still runs after the run")
	}
	arts := artifactsOfKind(run, "capture")
	if len(arts) != 1 || arts[0]["capture"] == nil || !strings.HasPrefix(arts[0]["url"].(string), "/api/v1/captures/") || arts[0]["size_bytes"].(float64) <= 0 {
		t.Fatalf("artifacts %v", run["artifacts"])
	}
	if len(warningCodes(run)) != 0 {
		t.Errorf("warnings %v", run["warnings"])
	}

	// the artifact is the capture: the file with exactly the frames of the device, stopped by the end of the run
	capID := arts[0]["capture"].(string)
	if want := "/api/v1/captures/" + capID + "/download"; arts[0]["url"] != want {
		t.Errorf("url %v, want %s", arts[0]["url"], want)
	}
	cp := g.capture(capID)
	if cp["run"] != id || cp["status"] != "stopped" || cp["stop_reason"] != "run_end" {
		t.Errorf("%v", cp)
	}
	_, pkts := g.download(capID, "")
	sameFrames(t, "the capture of the run", frames(pkts), up, down)
	list := g.do("GET", "/captures?run="+id, nil, nil, nil).json(t)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != capID {
		t.Errorf("captures of the run: %v", list)
	}
	if other := g.do("GET", "/captures?run="+espID, nil, nil, nil).json(t)["items"].([]any); len(other) != 0 {
		t.Errorf("captures of another run: %v", other)
	}
	// the report has it, and the events of the run
	rep := g.do("GET", "/runs/"+id+"/report.json", nil, nil, nil).json(t)
	if len(artifactsOfKind(rep, "capture")) != 1 {
		t.Errorf("report artifacts %v", rep["artifacts"])
	}
	ev := string(g.do("GET", "/runs/"+id+"/events", nil, nil, nil).Body)
	if !strings.Contains(ev, `"capture_started"`) || !strings.Contains(ev, `"capture_stopped"`) || !strings.Contains(ev, capID) {
		t.Errorf("events of the run:\n%s", ev)
	}
}

// `capture: start` and `capture: stop` record a part of the run.
func TestCaptureStepsOverTheAPIRecordOnlyTheirWindow(t *testing.T) {
	g, clk := capGateway(t)
	r := g.postRun(inlineRun("window", espTarget,
		step("normal", "0s", "profile", "normal"),
		step("rec", "5s", "capture", "start"),
		step("halt", "15s", "capture", "stop"),
		step("end", "20s", "restore", true)), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "normal", "applied")
	before := udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 1, 53, 5)
	g.cap.Frame("br-iot", before, false)
	if g.cap.Running() != 0 {
		t.Fatal("a capture runs before its step")
	}
	clk.BlockUntil(1)
	clk.Advance(5 * time.Second)
	g.waitStep(id, "rec", "applied")
	inside := udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 2, 53, 5)
	g.cap.Frame("br-iot", inside, false)
	clk.BlockUntil(1)
	clk.Advance(10 * time.Second)
	g.waitStep(id, "halt", "applied")
	if g.cap.Running() != 0 {
		t.Fatal("the capture runs after its stop")
	}
	g.cap.Frame("br-iot", udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 3, 53, 5), false)
	clk.BlockUntil(1)
	clk.Advance(5 * time.Second)
	run := g.waitRun(id, "passed")
	arts := artifactsOfKind(run, "capture")
	if len(arts) != 1 {
		t.Fatalf("artifacts %v", run["artifacts"])
	}
	capID := arts[0]["capture"].(string)
	if cp := g.capture(capID); cp["stop_reason"] != "user" || cp["run"] != id {
		t.Errorf("%v", cp)
	}
	_, pkts := g.download(capID, "")
	sameFrames(t, "the window", frames(pkts), inside)
}

// Low disk stops the capture but not the run: the run passes, says that its capture is missing from then on, and
// still offers what was recorded.
func TestALowDiskStopsTheCaptureOfARunButNotTheRun(t *testing.T) {
	g, clk := capGateway(t)
	r := g.postRun(capturing("lowdisk",
		step("slow", "0s", "profile", "normal"),
		step("end", "10s", "restore", true)), "")
	id := r.json(t)["id"].(string)
	g.waitStep(id, "slow", "applied")
	rec := udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 1, 53, 5)
	g.cap.Frame("br-iot", rec, false)
	g.cap.freePc.Store(3)
	g.cap.End("disk_low", "")
	clk.BlockUntil(1)
	clk.Advance(10 * time.Second)
	run := g.waitRun(id, "passed")
	if codes := warningCodes(run); len(codes) != 1 || codes[0] != "capture_missing" {
		t.Fatalf("warnings %v", run["warnings"])
	}
	w := warningsOf(run, "capture_missing")[0]["message"].(string)
	if !strings.Contains(w, "disk_low") || !strings.Contains(w, "10 %") {
		t.Errorf("the warning: %s", w)
	}
	arts := artifactsOfKind(run, "capture")
	if len(arts) != 1 {
		t.Fatalf("artifacts %v", run["artifacts"])
	}
	_, pkts := g.download(arts[0]["capture"].(string), "")
	sameFrames(t, "what was recorded before the disk ran low", frames(pkts), rec)
	// the JUnit file carries the warning
	if x := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body); !strings.Contains(x, "capture_missing") {
		t.Errorf("JUnit:\n%s", x)
	}
	if run["status"] != "passed" || len(g.e.Snapshot().Overlays) != 0 {
		t.Errorf("run %v, %d overlays", run["status"], len(g.e.Snapshot().Overlays))
	}
}

// A run on a full disk cannot start its capture: it runs without, with the warning.
func TestARunWhoseCaptureCannotStartRunsWithoutItAndSaysSo(t *testing.T) {
	g, clk := capGateway(t)
	g.cap.freePc.Store(2)
	r := g.postRun(capturing("nodisk",
		step("slow", "0s", "profile", "normal"),
		step("end", "10s", "restore", true)), "")
	id := r.json(t)["id"].(string)
	g.waitStep(id, "slow", "applied")
	clk.BlockUntil(1)
	clk.Advance(10 * time.Second)
	run := g.waitRun(id, "passed")
	if codes := warningCodes(run); len(codes) != 1 || codes[0] != "capture_missing" || !strings.Contains(warningsOf(run, "capture_missing")[0]["message"].(string), "free space") {
		t.Errorf("warnings %v", run["warnings"])
	}
	if len(artifactsOfKind(run, "capture")) != 0 || len(g.cap.Opened()) != 0 {
		t.Errorf("artifacts %v, %d requests", run["artifacts"], len(g.cap.Opened()))
	}
}

// The tunnel of the management network is not recorded, for a run neither: the run goes on without the capture and says
// so, and nothing reaches the executor.
func TestARunWhoseTargetIsAManagementNetworkIsNotRecorded(t *testing.T) {
	g, _ := capGateway(t)
	body := inlineRun("admin", map[string]any{"network": "admin"}, step("slow", "0s", "profile", "normal"))
	body["inline"].(map[string]any)["capture"] = true
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	run := g.waitRun(r.json(t)["id"].(string), "passed", "failed", "error")
	var missing string
	for _, w := range run["warnings"].([]any) {
		if m := w.(map[string]any); m["code"] == "capture_missing" {
			missing, _ = m["message"].(string)
		}
	}
	if !strings.Contains(missing, "management") {
		t.Errorf("warnings %v: the run does not say that the management network is not recorded", run["warnings"])
	}
	if len(artifactsOfKind(run, "capture")) != 0 || len(g.cap.Opened()) != 0 {
		t.Errorf("artifacts %v, %d requests", run["artifacts"], len(g.cap.Opened()))
	}
}

// An executor that does not answer is no reason to end the run either.
func TestARunGoesOnWhenTheExecutorRefusesItsCapture(t *testing.T) {
	g, clk := capGateway(t)
	g.cap.Fail(executor.ErrTooManyCaptures)
	r := g.postRun(capturing("refused", step("slow", "0s", "profile", "normal")), "")
	_ = clk
	id := r.json(t)["id"].(string)
	run := g.waitRun(id, "passed")
	if codes := warningCodes(run); len(codes) != 1 || codes[0] != "capture_missing" {
		t.Errorf("warnings %v", run["warnings"])
	}
}

// An abort ends the capture with the run and the run keeps what it recorded.
func TestAnAbortedRunStopsItsCapture(t *testing.T) {
	g, _ := capGateway(t)
	r := g.postRun(capturing("abort",
		step("slow", "0s", "profile", "normal"),
		step("end", "100s", "restore", true)), "")
	id := r.json(t)["id"].(string)
	g.waitStep(id, "slow", "applied")
	rec := udpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 1, 53, 5)
	g.cap.Frame("br-iot", rec, false)
	if ab := g.do("POST", "/runs/"+id+"/abort", nil, nil, nil); ab.Status != 200 {
		t.Fatalf("%d %s", ab.Status, ab.Body)
	}
	run := g.run(id)
	arts := artifactsOfKind(run, "capture")
	if run["status"] != "aborted" || len(arts) != 1 || g.cap.Running() != 0 {
		t.Fatalf("%v %v", run["status"], run["artifacts"])
	}
	_, pkts := g.download(arts[0]["capture"].(string), "")
	sameFrames(t, "the capture of an aborted run", frames(pkts), rec)
}
