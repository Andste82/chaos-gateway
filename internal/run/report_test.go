package run_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// The report has every step with its time, generation and queue wait, the warnings, and the final counters of
// every overlay the run created, also of those a remove or restore step took away before the end.
func TestTheReportHasAllStepsTheirQueueWaitAndTheFinalCounters(t *testing.T) {
	h := newHarness(t)
	h.gw.queueWait = 7 * time.Millisecond
	r := h.start(`
name: report
target: {device: esp32-42}
steps:
  - {id: lat,  at: 0s, fault: {latency: 100ms}}
  - {id: drop, at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: off,  at: 3s, remove: lat}
  - {id: loss, at: 4s, fault: {loss: 5%}}
`)
	h.waitStep(r.Id, "drop", model.RunStepStatusApplied)
	cur := h.get(r.Id)
	latID, dropID := *step(cur, "lat").Overlay, *step(cur, "drop").Overlay
	h.obs.mu.Lock()
	h.obs.counters[latID] = &model.Counter{Packets: 12, Bytes: 1200, Epoch: 3}
	h.obs.counters[dropID] = &model.Counter{Packets: 0, Bytes: 0, Epoch: 3}
	h.obs.mu.Unlock()
	h.advance(3 * time.Second)
	h.waitStep(r.Id, "off", model.RunStepStatusApplied)
	// the overlay of "lat" is gone; its counter was read before it went
	h.obs.mu.Lock()
	delete(h.obs.counters, latID)
	h.obs.counters[dropID] = &model.Counter{Packets: 4, Bytes: 240, Epoch: 3}
	h.obs.mu.Unlock()
	h.advance(time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)

	rep, err := h.m.Report(r.Id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != model.RunStatusPassed || len(*rep.Steps) != 4 || rep.Snapshot == nil || rep.Revision != 3 {
		t.Fatalf("%+v", rep)
	}
	for _, s := range *rep.Steps {
		if s.QueueWaitMs == nil || *s.QueueWaitMs != 7 || s.Generation == nil || s.AppliedAt == nil || s.OffsetMs == nil || s.ScheduledOffset == nil {
			t.Errorf("step %s: %+v", s.Id, s)
		}
	}
	got := map[string]model.Counter{}
	for _, c := range *rep.Counters {
		got[*c.Step] = *c.Counters
	}
	if len(got) != 2 || got["lat"].Packets != 12 || got["drop"].Packets != 4 || got["lat"].Epoch != 3 {
		t.Errorf("counters %+v", got)
	}
	if rep.Artifacts == nil || len(*rep.Artifacts) != 3 {
		t.Fatalf("artifacts %+v", rep.Artifacts)
	}
	for _, a := range *rep.Artifacts {
		if !strings.HasPrefix(a.Url, "/api/v1/runs/"+r.Id.String()+"/") {
			t.Errorf("artifact %+v", a)
		}
		if a.Kind == model.ArtifactKindEvents && (a.SizeBytes == nil || *a.SizeBytes == 0) {
			t.Errorf("the events artifact has no size: %+v", a)
		}
	}
}

func TestTheReportOfARunThatHasNotEndedIsNotThereYet(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp))
	if _, err := h.m.Report(r.Id); !errors.Is(err, run.ErrNotEnded) {
		t.Errorf("%v", err)
	}
	if _, err := h.m.JUnit(r.Id); !errors.Is(err, run.ErrNotEnded) {
		t.Errorf("%v", err)
	}
	if _, err := h.m.Report(uuid.New()); !errors.Is(err, run.ErrNotFound) {
		t.Errorf("%v", err)
	}
}

type junit struct {
	XMLName  xml.Name `xml:"testsuites"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Errors   int      `xml:"errors,attr"`
	Suites   []struct {
		Name       string `xml:"name,attr"`
		ID         string `xml:"id,attr"`
		Tests      int    `xml:"tests,attr"`
		Errors     int    `xml:"errors,attr"`
		Properties []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"properties>property"`
		Cases []struct {
			Name  string `xml:"name,attr"`
			Error *struct {
				Message string `xml:"message,attr"`
				Type    string `xml:"type,attr"`
			} `xml:"error"`
		} `xml:"testcase"`
		Out string `xml:"system-out"`
	} `xml:"testsuite"`
}

func parseJUnit(t *testing.T, raw []byte) junit {
	t.Helper()
	var j junit
	if err := xml.Unmarshal(raw, &j); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	return j
}

// The JUnit report of CI: one suite per run, a test case for the run, one for each step that failed.
func TestTheJUnitReportOfAPassedRunHasOneSuccessfulCase(t *testing.T) {
	h := newHarness(t)
	h.gw.queueWait = 150 * time.Millisecond
	r := h.start(`
name: junit.x
target: {device: esp32-42}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
`)
	h.waitStatus(r.Id, model.RunStatusPassed)
	raw, err := h.m.JUnit(r.Id)
	if err != nil {
		t.Fatal(err)
	}
	j := parseJUnit(t, raw)
	if j.Tests != 1 || j.Failures != 0 || j.Errors != 0 || len(j.Suites) != 1 {
		t.Fatalf("%s", raw)
	}
	s := j.Suites[0]
	if s.Name != "junit.x" || s.ID != r.Id.String() || len(s.Cases) != 1 || s.Cases[0].Name != "run" || s.Cases[0].Error != nil {
		t.Errorf("%+v", s)
	}
	if !has(s.Out, "warning step_late") || !has(s.Out, "step a (fault): applied at +0 ms") {
		t.Errorf("output:\n%s", s.Out)
	}
	var props []string
	for _, p := range s.Properties {
		props = append(props, p.Name+"="+p.Value)
	}
	if !contains(props, "status=passed") || !contains(props, "run="+r.Id.String()) {
		t.Errorf("%v", props)
	}
}

func TestTheJUnitReportOfARunThatEndedAsErrorHasAnErrorCase(t *testing.T) {
	h := newHarness(t)
	h.gw.setOnline(idESP, false)
	bad := h.start(long("bad", esp))
	h.waitStatus(bad.Id, model.RunStatusError)
	raw, _ := h.m.JUnit(bad.Id)
	j := parseJUnit(t, raw)
	if j.Errors != 1 || j.Suites[0].Cases[0].Error == nil || j.Suites[0].Cases[0].Error.Type != "run_error" ||
		!has(j.Suites[0].Cases[0].Error.Message, "precondition target_online failed") {
		t.Errorf("%s", raw)
	}
}

func TestAStepErrorIsATestCaseOfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.gw.mu.Lock()
	h.gw.putErr = failStep(`boom <&> "x"`)
	h.gw.mu.Unlock()
	r := h.start(long("boom", esp))
	h.waitStatus(r.Id, model.RunStatusError)
	raw, _ := h.m.JUnit(r.Id)
	j := parseJUnit(t, raw)
	var names []string
	for _, c := range j.Suites[0].Cases {
		names = append(names, c.Name)
	}
	if j.Errors != 2 || strings.Join(names, ",") != "run,step a" || !has(j.Suites[0].Cases[1].Error.Message, `boom <&> "x"`) {
		t.Errorf("%v\n%s", names, raw)
	}
}

func TestAnAbortedRunIsAnErrorInTheJUnitReport(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp))
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	if _, err := h.m.Abort(context.Background(), r.Id, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := h.m.JUnit(r.Id)
	j := parseJUnit(t, raw)
	if j.Errors != 1 || j.Suites[0].Cases[0].Error.Type != "run_aborted" || !has(j.Suites[0].Cases[0].Error.Message, "aborted") {
		t.Errorf("%s", raw)
	}
}

// The events of a run are in its file: the run's own and those of its overlays, in order, and the file is
// complete when the run is over.
func TestTheEventsOfARunAreKeptInItsFile(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: events
target: {device: esp32-42}
steps:
  - {id: a, at: 0s, fault: {latency: 10ms}}
  - {id: b, at: 1s, restore: true}
`)
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	h.advance(time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
	lines, err := h.m.Events(r.Id)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, l := range lines {
		var ev struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(l, &ev); err != nil {
			t.Fatalf("%v: %s", err, l)
		}
		if ev.Data["run"] != r.Id.String() {
			t.Errorf("an event of another run: %s", l)
		}
		types = append(types, ev.Type)
	}
	if strings.Join(types, ",") != "run_started,run_step,run_step,run_finished" {
		t.Errorf("events %v", types)
	}
	if _, err := h.m.Events(uuid.New()); !errors.Is(err, run.ErrNotFound) {
		t.Errorf("%v", err)
	}
}

// ---- restart, retention

func writeRecord(t *testing.T, dir string, r model.Run) {
	t.Helper()
	d := filepath.Join(dir, r.Id.String())
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "run": r})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "run.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func record(status model.RunStatus, ended *time.Time) model.Run {
	id := uuid.New()
	steps := []model.RunStep{{Id: "a", Status: model.RunStepStatusApplied}, {Id: "b", Status: model.RunStepStatusPending}}
	r := model.Run{Id: id, Revision: 3, Target: model.Scope{Device: ptr(idESP)}, Owner: token, Status: status, CreatedAt: t0.Add(-time.Hour), Steps: &steps, EndedAt: ended}
	r.Scenario.Name = "old"
	return r
}

// A restart ends a run that was running or queued as aborted (its overlays died with the process); finished
// runs are there with their reports, and what is older than the retention is gone.
func TestARestartEndsARunningRunAsAbortedAndKeepsTheFinishedOnes(t *testing.T) {
	dir := t.TempDir()
	running, queued := record(model.RunStatusRunning, nil), record(model.RunStatusQueued, nil)
	// a check that was not decided when the process died
	running.Checks = &[]model.CheckResult{{Index: 0, Name: "reconnects", Type: model.Reconnected, Status: model.CheckResultStatusPending}}
	ended := t0.Add(-time.Minute)
	passed := record(model.RunStatusPassed, &ended)
	oldEnd := t0.Add(-31 * 24 * time.Hour)
	expired := record(model.RunStatusFailed, &oldEnd)
	for _, r := range []model.Run{running, queued, passed, expired} {
		writeRecord(t, dir, r)
	}
	if err := os.MkdirAll(filepath.Join(dir, uuid.NewString()), 0o700); err != nil { // a run without a record
		t.Fatal(err)
	}
	junk := filepath.Join(dir, uuid.NewString())
	if err := os.MkdirAll(junk, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "run.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newHarnessIn(t, dir)
	if err := h.m.Recover(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{running.Id, queued.Id} {
		got := h.get(id)
		if got.Status != model.RunStatusAborted || got.Reason == nil || *got.Reason != "service restart" || got.EndedAt == nil {
			t.Errorf("%+v", got)
		}
		if s := step(got, "b"); s.Status != model.RunStepStatusSkipped {
			t.Errorf("%+v", s)
		}
		if step(got, "a").Status != model.RunStepStatusApplied {
			t.Errorf("a step that ran is not what it was: %+v", step(got, "a"))
		}
		if _, err := h.m.Report(id); err != nil {
			t.Errorf("the report of an aborted run: %v", err)
		}
	}
	// the check says why it has no result
	if cs := deref(h.get(running.Id).Checks); len(cs) != 1 || cs[0].Status != model.CheckResultStatusPending ||
		cs[0].Message == nil || *cs[0].Message != "not decided: the run ended as aborted (service restart)" {
		t.Errorf("the check of the run the restart ended: %+v", cs)
	}
	if got := h.get(passed.Id); got.Status != model.RunStatusPassed {
		t.Errorf("%+v", got)
	}
	if _, err := h.m.Get(expired.Id); !errors.Is(err, run.ErrNotFound) {
		t.Errorf("a run past the retention is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, expired.Id.String())); !os.IsNotExist(err) {
		t.Errorf("its directory is still there: %v", err)
	}
	if got := h.m.List(run.Filter{}); len(got) != 3 {
		t.Errorf("%d runs", len(got))
	}
	// the end of the run is an event of the new process, and in the run's file
	eventually(t, "the events of the restart", func() bool {
		lines, _ := h.m.Events(running.Id)
		return len(lines) == 1 && has(string(lines[0]), "service restart")
	})
}

// A run that ended is read back from disk after a restart as it was.
func TestAFinishedRunSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	h := newHarnessIn(t, dir)
	r := h.start(basicShort)
	h.waitStatus(r.Id, model.RunStatusPassed)
	before, _ := h.m.Report(r.Id)
	h.m.Close(context.Background())

	h2 := newHarnessIn(t, dir)
	if err := h2.m.Recover(); err != nil {
		t.Fatal(err)
	}
	after, err := h2.m.Report(r.Id)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Errorf("the report changed over the restart:\n%s\n%s", a, b)
	}
	if lines, _ := h2.m.Events(r.Id); len(lines) < 3 {
		t.Errorf("%d events", len(lines))
	}
}

const basicShort = `
name: short
target: {device: esp32-42}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
`

// The record of a run past the retention is deleted when the next run ends.
func TestOldRecordsAreDeletedAsRunsEnd(t *testing.T) {
	h := newHarness(t)
	r := h.start(basicShort)
	h.waitStatus(r.Id, model.RunStatusPassed)
	h.clk.Advance(31 * 24 * time.Hour)
	r2 := h.start(basicShort)
	h.waitStatus(r2.Id, model.RunStatusPassed)
	if _, err := h.m.Get(r.Id); !errors.Is(err, run.ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, r.Id.String())); !os.IsNotExist(err) {
		t.Errorf("%v", err)
	}
}

// ---- revisions

// A revision that deletes an object a queued or running run refers to orphans it; forcing the revision
// aborts the run.
func TestARevisionThatDeletesWhatARunRefersToOrphansIt(t *testing.T) {
	h := newHarness(t)
	r := h.start(long("long", esp))
	q := h.start(long("queued", group))
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)

	cfg := h.gw.Snapshot().Config
	raw, _ := json.Marshal(cfg)
	var next model.Configuration
	if err := json.Unmarshal(raw, &next); err != nil {
		t.Fatal(err)
	}
	devices := map[string]model.Device{}
	for id, d := range *next.Devices {
		if id != idESP {
			devices[id] = d
		}
	}
	next.Devices = &devices
	groups := map[string]model.Group{}
	for id, g := range *next.Groups {
		if id != idSens {
			groups[id] = g
		}
	}
	next.Groups = &groups

	refs := h.m.Orphaned(cfg, &next)
	got := map[uuid.UUID][]string{}
	for _, ref := range refs {
		got[ref.Run.Id] = append(got[ref.Run.Id], ref.Object)
	}
	if len(got[r.Id]) != 1 || got[r.Id][0] != "/devices/"+idESP || len(got[q.Id]) != 1 || got[q.Id][0] != "/groups/"+idSens {
		t.Fatalf("%v", got)
	}
	if refs[0].Run.Steps != nil || refs[0].Run.Owner != token {
		t.Errorf("a reference carries the run without its timeline: %+v", refs[0].Run)
	}
	if again := h.m.Orphaned(cfg, cfg); len(again) != 0 {
		t.Errorf("%v", again)
	}

	if n := h.m.AbortReferences(context.Background(), refs, 4); len(n) != 2 {
		t.Errorf("aborted %d", n)
	}
	done := h.get(r.Id)
	if done.Status != model.RunStatusAborted || !has(*done.Reason, "revision 4 deletes /devices/"+idESP) {
		t.Errorf("%+v", done)
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays", n)
	}
}
