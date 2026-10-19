package run_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// M17: the captures of a run. The manager of captures is a fake that keeps the captures in a table; what a
// capture records is tested in internal/capture, internal/executor, internal/capturemgr and over the API.

// fakeCaptures is run.Captures: it makes captures that run until they are stopped, and writes what it did into the
// log of the gateway, so that the tests see the order of the steps and the captures.
type fakeCaptures struct {
	mu       sync.Mutex
	gw       *fakeGateway
	caps     map[uuid.UUID]*model.Capture
	order    []uuid.UUID
	starts   []startCall
	stops    []stopCall
	deleted  []uuid.UUID
	startErr error
}

type startCall struct {
	scope model.CaptureScope
	name  string
	run   uuid.UUID
}

type stopCall struct {
	id     uuid.UUID
	reason model.CaptureStopReason
}

func newFakeCaptures(gw *fakeGateway) *fakeCaptures {
	return &fakeCaptures{gw: gw, caps: map[uuid.UUID]*model.Capture{}}
}

func (f *fakeCaptures) Start(_ context.Context, scope model.CaptureScope, name string, runID uuid.UUID) (model.Capture, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return model.Capture{}, f.startErr
	}
	c := &model.Capture{Id: uuid.New(), Name: &name, Scope: scope, Status: model.CaptureStatusRunning, StartedAt: t0, Run: &runID, SizeBytes: ptr(int64(1000))}
	f.caps[c.Id] = c
	f.order = append(f.order, c.Id)
	f.starts = append(f.starts, startCall{scope, name, runID})
	f.gw.note("capture start")
	return *c, nil
}

func (f *fakeCaptures) Stop(_ context.Context, id uuid.UUID, reason model.CaptureStopReason) (model.Capture, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.caps[id]
	if !ok {
		return model.Capture{}, errors.New("no such capture")
	}
	if c.Status == model.CaptureStatusRunning {
		c.Status, c.StopReason, c.StoppedAt = model.CaptureStatusStopped, &reason, ptr(t0)
		f.stops = append(f.stops, stopCall{id, reason})
		f.gw.note(fmt.Sprintf("capture stop %s", reason))
	}
	return *c, nil
}

func (f *fakeCaptures) Get(id uuid.UUID) (model.Capture, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.caps[id]; ok {
		return *c, nil
	}
	return model.Capture{}, errors.New("no such capture")
}

func (f *fakeCaptures) OfRun(runID uuid.UUID) []model.Capture {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Capture
	for _, id := range f.order {
		if c := f.caps[id]; c.Run != nil && *c.Run == runID {
			out = append(out, *c)
		}
	}
	return out
}

func (f *fakeCaptures) DeleteRun(runID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, runID)
}

// endEarly ends the running capture n (counted from 0) the way the executor does: disk low, a lost interface.
func (f *fakeCaptures) endEarly(n int, reason model.CaptureStopReason, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.caps[f.order[n]]
	c.Status, c.StopReason, c.StoppedAt, c.Message = model.CaptureStatusStopped, &reason, ptr(t0), &msg
	if reason == model.CaptureStopReasonError {
		c.Status = model.CaptureStatusFailed
	}
}

func (f *fakeCaptures) startedCalls() []startCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]startCall(nil), f.starts...)
}

func (f *fakeCaptures) stoppedCalls() []stopCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]stopCall(nil), f.stops...)
}

func (f *fakeCaptures) capture(n int) model.Capture {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.caps[f.order[n]]
}

func (g *fakeGateway) note(s string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.log = append(g.log, s)
}

// capHarness is a harness whose runs can capture.
func capHarness(t *testing.T) (*harness, *fakeCaptures) {
	t.Helper()
	var fc *fakeCaptures
	h := newHarness(t, func(c *run.Config) {
		fc = newFakeCaptures(c.Gateway.(*fakeGateway))
		c.Captures = fc
	})
	return h, fc
}

func warnings(r model.Run) []model.RunWarning { return deref(r.Warnings) }

func capArtifacts(r model.Run) []model.Artifact {
	var out []model.Artifact
	for _, a := range deref(r.Artifacts) {
		if a.Kind == model.ArtifactKindCapture {
			out = append(out, a)
		}
	}
	return out
}

// `capture: true` records the target for the whole run: the capture starts before the first step, ends with
// the reason run_end after the last, is attached to the run and is one of its artifacts.
func TestACaptureTrueRecordsTheTargetForTheWholeRun(t *testing.T) {
	h, fc := capHarness(t)
	r := h.start(`
name: whole
target: {device: esp32-42}
capture: true
steps:
  - {id: slow, at: 0s,  fault: {latency: 100ms}}
  - {id: end,  at: 10s, restore: true}
`)
	h.waitStep(r.Id, "slow", model.RunStepStatusApplied)
	if got := fc.startedCalls(); len(got) != 1 || got[0].run != r.Id || got[0].scope.Device == nil || *got[0].scope.Device != idESP || got[0].name != "whole" {
		t.Fatalf("starts: %+v", got)
	}
	if c := fc.capture(0); c.Status != model.CaptureStatusRunning {
		t.Fatalf("the capture does not run while the run does: %+v", c)
	}
	h.advance(10 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	if calls := fc.stoppedCalls(); len(calls) != 1 || calls[0].reason != model.CaptureStopReasonRunEnd {
		t.Fatalf("stops: %+v", calls)
	}
	// the capture is there before the first overlay and gone after the last step
	log := strings.Join(h.gw.logged(), ",")
	if !strings.HasPrefix(log, "capture start,put slow") || !strings.Contains(log, "capture stop run_end") {
		t.Errorf("the order of the captures and the steps: %s", log)
	}
	arts := capArtifacts(done)
	c := fc.capture(0)
	if len(arts) != 1 || arts[0].Capture == nil || *arts[0].Capture != c.Id || arts[0].Url != "/api/v1/captures/"+c.Id.String()+"/download" || arts[0].SizeBytes == nil || *arts[0].SizeBytes != 1000 {
		t.Errorf("artifacts: %+v", deref(done.Artifacts))
	}
	if len(warnings(done)) != 0 {
		t.Errorf("warnings: %+v", warnings(done))
	}
	// the report has it, too
	rep, err := h.m.Report(r.Id)
	if err != nil || len(capArtifacts(model.Run{Artifacts: rep.Artifacts})) != 1 {
		t.Errorf("report: %v %+v", err, deref(rep.Artifacts))
	}
}

// The scope of the capture is the target of the run: a network as it is, a group as a selector with the group as its
// source.
func TestTheCaptureOfARunHasTheScopeOfTheTarget(t *testing.T) {
	for name, tc := range map[string]struct {
		target string
		check  func(model.CaptureScope) bool
	}{
		"device":  {"{device: esp32-42}", func(s model.CaptureScope) bool { return s.Device != nil && *s.Device == idESP }},
		"network": {"{network: IoT}", func(s model.CaptureScope) bool { return s.Network != nil && *s.Network == idIoT }},
		"group": {"{group: sensors}", func(s model.CaptureScope) bool {
			return s.Selector != nil && s.Selector.Source.Group != nil && *s.Selector.Source.Group == idSens
		}},
	} {
		h, fc := capHarness(t)
		r := h.start(fmt.Sprintf("name: x\ntarget: %s\ncapture: true\nsteps: [{id: a, at: 0s, fault: {latency: 10ms}}]\n", tc.target))
		h.waitStatus(r.Id, model.RunStatusPassed)
		got := fc.startedCalls()
		if len(got) != 1 || !tc.check(got[0].scope) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

// `capture: start` and `capture: stop` record a part of the run: the capture of the step's target (narrowing the
// scenario's) runs between them and ends with the reason user, and the run has it as an artifact.
func TestCaptureStepsRecordAPartOfTheRun(t *testing.T) {
	h, fc := capHarness(t)
	r := h.start(`
name: part
target: {network: IoT}
steps:
  - {id: normal, at: 0s,  profile: normal}
  - {id: rec,    at: 5s,  capture: start, target: {device: esp32-42}}
  - {id: cut,    at: 10s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: halt,   at: 15s, capture: stop,  target: {device: esp32-42}}
  - {id: end,    at: 20s, restore: true}
`)
	h.waitStep(r.Id, "normal", model.RunStepStatusApplied)
	if len(fc.startedCalls()) != 0 {
		t.Fatal("a capture runs before its step")
	}
	h.advance(5 * time.Second)
	h.waitStep(r.Id, "rec", model.RunStepStatusApplied)
	if got := fc.startedCalls(); len(got) != 1 || got[0].scope.Device == nil || *got[0].scope.Device != idESP || got[0].name != "part rec" {
		t.Fatalf("starts: %+v", got)
	}
	h.advance(10 * time.Second)
	h.waitStep(r.Id, "halt", model.RunStepStatusApplied)
	if calls := fc.stoppedCalls(); len(calls) != 1 || calls[0].reason != model.CaptureStopReasonUser {
		t.Fatalf("stops: %+v", calls)
	}
	h.advance(5 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	if len(fc.stoppedCalls()) != 1 {
		t.Errorf("the end of the run stopped a capture that was stopped: %+v", fc.stoppedCalls())
	}
	if step(done, "rec").Type == nil || *step(done, "rec").Type != model.RunStepTypeCapture || step(done, "rec").Overlay != nil {
		t.Errorf("the step: %+v", step(done, "rec"))
	}
	if len(capArtifacts(done)) != 1 || len(warnings(done)) != 0 {
		t.Errorf("artifacts %+v, warnings %+v", deref(done.Artifacts), warnings(done))
	}
	// the capture of the step ended between the steps, not with the run
	log := strings.Join(h.gw.logged(), ",")
	if !strings.Contains(log, "put cut,capture stop user") || !strings.Contains(log, "capture start,put cut") {
		t.Errorf("order: %s", log)
	}
}

// A capture still running when the run ends is stopped with it; the two kinds of capture of one run are two artifacts.
func TestACaptureStepWithoutAStopEndsWithTheRunAndAWholeRunCaptureIsNotStoppedByAStep(t *testing.T) {
	h, fc := capHarness(t)
	r := h.start(`
name: both
target: {device: esp32-42}
capture: true
steps:
  - {id: rec,  at: 0s,  capture: start}
  - {id: halt, at: 5s,  capture: stop}
  - {id: rec2, at: 6s,  capture: start}
  - {id: end,  at: 10s, restore: true}
`)
	h.advance(5 * time.Second)
	h.advance(time.Second)
	h.advance(4 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	if got := len(fc.startedCalls()); got != 3 {
		t.Fatalf("%d captures, want the whole run's and two of steps", got)
	}
	calls := fc.stoppedCalls()
	if len(calls) != 3 {
		t.Fatalf("stops %+v", calls)
	}
	reasons := map[model.CaptureStopReason]int{}
	for _, c := range calls {
		reasons[c.reason]++
	}
	if reasons[model.CaptureStopReasonUser] != 1 || reasons[model.CaptureStopReasonRunEnd] != 2 {
		t.Errorf("reasons %v", reasons)
	}
	if len(capArtifacts(done)) != 3 {
		t.Errorf("artifacts %+v", deref(done.Artifacts))
	}
}

// Starting a capture of a target that one already records, and stopping where none runs, change nothing.
func TestCaptureStepsThatHaveNothingToDoAreSkipped(t *testing.T) {
// `POST /captures/{id}/stop` ends any capture, a run's too. The reason `user` is the run's own only for a capture that its
// `capture: stop` step ended; for the capture of the whole run, or of a step whose stop has not run, the rest of the run
// is not recorded and the run says so.
func TestACaptureThatAUserStopsBeforeTheRunEndsIsMissing(t *testing.T) {
	h, fc := capHarness(t)
	r := h.start(`
name: stopped
target: {device: esp32-42}
capture: true
steps:
  - {id: rec,  at: 0s,  capture: start}
  - {id: halt, at: 8s,  capture: stop}
  - {id: end,  at: 10s, restore: true}
`)
	h.waitStep(r.Id, "rec", model.RunStepStatusApplied)
	fc.endEarly(0, model.CaptureStopReasonUser, "") // someone stops the capture of the whole run at 0 s
	h.advance(8 * time.Second)
	h.waitStep(r.Id, "halt", model.RunStepStatusApplied)
	h.advance(2 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := warnings(done)
	if len(ws) != 1 || ws[0].Code != model.CaptureMissing || ws[0].Step != nil || !has(ws[0].Message, "stopped by a user before the run ended") {
		t.Fatalf("warnings %+v", ws)
	}
	if len(capArtifacts(done)) != 2 {
		t.Errorf("what was recorded is still offered: %+v", deref(done.Artifacts))
	}
}

// The same for a capture of a step that a user stops before the step that would: the stop step finds it stopped and the
// run says it was not the step's.
func TestACaptureOfAStepThatAUserStopsBeforeItsStopStepIsMissing(t *testing.T) {
	h, fc := capHarness(t)
	r := h.start(`
name: stopped
target: {device: esp32-42}
steps:
  - {id: rec,  at: 0s, capture: start}
  - {id: halt, at: 8s, capture: stop}
`)
	h.waitStep(r.Id, "rec", model.RunStepStatusApplied)
	fc.endEarly(0, model.CaptureStopReasonUser, "")
	h.advance(8 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := warnings(done)
	if len(ws) != 1 || ws[0].Code != model.CaptureMissing || ws[0].Step == nil || *ws[0].Step != "rec" ||
		!has(ws[0].Message, "stopped by a user before the step halt stopped it") {
		t.Fatalf("warnings %+v", ws)
	}
	if calls := fc.stoppedCalls(); len(calls) != 0 {
		t.Errorf("the run stopped a capture that had ended: %+v", calls)
	}
	if s := step(done, "halt"); s.Status != model.RunStepStatusApplied {
		t.Errorf("the stop step %+v", s)
	}
}

	h, fc := capHarness(t)
	r := h.start(`
name: idle
target: {device: esp32-42}
steps:
  - {id: a, at: 0s, capture: start}
  - {id: b, at: 1s, capture: start}
  - {id: c, at: 2s, capture: stop}
  - {id: d, at: 3s, capture: stop}
`)
	h.advance(time.Second)
	h.advance(time.Second)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	for id, want := range map[string]model.RunStepStatus{"a": model.RunStepStatusApplied, "b": model.RunStepStatusSkipped, "c": model.RunStepStatusApplied, "d": model.RunStepStatusSkipped} {
		if got := step(done, id).Status; got != want {
			t.Errorf("step %s is %s, want %s", id, got, want)
		}
	}
	if e := step(done, "b").Error; e == nil || !strings.Contains(*e, "already") {
		t.Errorf("step b: %v", e)
	}
	if e := step(done, "d").Error; e == nil || !strings.Contains(*e, "no capture") {
		t.Errorf("step d: %v", e)
	}
	if len(fc.startedCalls()) != 1 || len(capArtifacts(done)) != 1 {
		t.Errorf("starts %d, artifacts %+v", len(fc.startedCalls()), deref(done.Artifacts))
	}
}

// A capture that cannot be started never ends the run: the run says which capture is missing.
func TestACaptureThatCannotStartIsAWarningAndTheRunGoesOn(t *testing.T) {
	h, fc := capHarness(t)
	fc.startErr = errors.New("the volume of the captures has less than 10 % free space")
	r := h.start(`
name: lowdisk
target: {device: esp32-42}
capture: true
steps:
  - {id: rec, at: 0s, capture: start}
  - {id: slow, at: 1s, fault: {latency: 100ms}}
`)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := warnings(done)
	if len(ws) != 2 {
		t.Fatalf("warnings %+v", ws)
	}
	for _, w := range ws {
		if w.Code != model.CaptureMissing || !strings.Contains(w.Message, "free space") {
			t.Errorf("warning %+v", w)
		}
	}
	if ws[1].Step == nil || *ws[1].Step != "rec" {
		t.Errorf("the warning of a step names it: %+v", ws[1])
	}
	if s := step(done, "rec"); s.Status != model.RunStepStatusSkipped || s.Error == nil {
		t.Errorf("step rec: %+v", s)
	}
	if step(done, "slow").Status != model.RunStepStatusApplied {
		t.Errorf("the next step did not run: %+v", step(done, "slow"))
	}
	if len(capArtifacts(done)) != 0 {
		t.Errorf("artifacts %+v", deref(done.Artifacts))
	}
}

// A capture that stops before the run ends (a disk below 10 % free, a lost interface) leaves what it recorded
// as an artifact and the warning that the rest is missing; the run passes.
func TestACaptureThatEndsEarlyLeavesItsFileAndAWarning(t *testing.T) {
	for reason, msg := range map[model.CaptureStopReason]string{
		model.CaptureStopReasonDiskLow: "the volume of the captures has less than 10 % free space",
		model.CaptureStopReasonError:   "interface br-iot is gone",
		model.CaptureStopReasonSize:    "the captures take more than their quota",
	} {
		h, fc := capHarness(t)
		r := h.start(`
name: early
target: {device: esp32-42}
capture: true
steps:
  - {id: slow, at: 0s,  fault: {latency: 100ms}}
  - {id: end,  at: 10s, restore: true}
`)
		h.waitStep(r.Id, "slow", model.RunStepStatusApplied)
		fc.endEarly(0, reason, msg)
		h.advance(10 * time.Second)
		done := h.waitStatus(r.Id, model.RunStatusPassed)
		ws := warnings(done)
		if len(ws) != 1 || ws[0].Code != model.CaptureMissing || !strings.Contains(ws[0].Message, string(reason)) || !strings.Contains(ws[0].Message, msg) {
			t.Errorf("%s: warnings %+v", reason, ws)
		}
		if len(capArtifacts(done)) != 1 {
			t.Errorf("%s: artifacts %+v", reason, deref(done.Artifacts))
		}
		if len(fc.stoppedCalls()) != 0 {
			t.Errorf("%s: the run stopped a capture that had ended: %+v", reason, fc.stoppedCalls())
		}
	}
}

// An abort, an error and a lease that runs out end the capture too, and the run keeps the file.
func TestCapturesEndWithARunWhateverEndsIt(t *testing.T) {
	h, fc := capHarness(t)
	r := h.start(`
name: aborted
target: {device: esp32-42}
capture: true
steps:
  - {id: slow, at: 0s,  fault: {latency: 100ms}}
  - {id: late, at: 30s, restore: true}
`)
	h.waitStep(r.Id, "slow", model.RunStepStatusApplied)
	if _, err := h.m.Abort(context.Background(), r.Id, nil); err != nil {
		t.Fatal(err)
	}
	done := h.waitStatus(r.Id, model.RunStatusAborted)
	if calls := fc.stoppedCalls(); len(calls) != 1 || calls[0].reason != model.CaptureStopReasonRunEnd {
		t.Errorf("stops after an abort: %+v", calls)
	}
	if len(capArtifacts(done)) != 1 {
		t.Errorf("artifacts after an abort: %+v", deref(done.Artifacts))
	}

	// a step that fails
	h.gw.putErr = failStep("compile failed")
	r2 := h.start(`
name: failing
target: {device: esp32-42}
capture: true
steps: [{id: slow, at: 0s, fault: {latency: 100ms}}]
`)
	done = h.waitStatus(r2.Id, model.RunStatusError)
	if len(fc.stoppedCalls()) != 2 || len(capArtifacts(done)) != 1 {
		t.Errorf("stops %+v, artifacts %+v", fc.stoppedCalls(), deref(done.Artifacts))
	}
}

// A run that was running when the process ended finds the captures that process left (they ended with it).
func TestARestartAttachesTheCapturesOfARunThatWasRunning(t *testing.T) {
	dir := t.TempDir()
	running := record(model.RunStatusRunning, nil)
	writeRecord(t, dir, running)
	var fc *fakeCaptures
	h := newHarnessIn(t, dir, func(c *run.Config) {
		fc = newFakeCaptures(c.Gateway.(*fakeGateway))
		c.Captures = fc
	})
	fc.mu.Lock()
	id := uuid.New()
	reason := model.CaptureStopReasonError
	fc.caps[id] = &model.Capture{Id: id, Status: model.CaptureStatusFailed, StopReason: &reason, Run: &running.Id, SizeBytes: ptr(int64(77))}
	fc.order = append(fc.order, id)
	fc.mu.Unlock()
	if err := h.m.Recover(); err != nil {
		t.Fatal(err)
	}
	got := h.get(running.Id)
	arts := capArtifacts(got)
	if got.Status != model.RunStatusAborted || len(arts) != 1 || *arts[0].Capture != id {
		t.Errorf("%+v", got)
	}
}

// The captures of a run are deleted with its record after the retention time.
func TestTheCapturesOfAnExpiredRunAreDeletedWithIt(t *testing.T) {
	dir := t.TempDir()
	old := t0.Add(-31 * 24 * time.Hour)
	expired := record(model.RunStatusFailed, &old)
	writeRecord(t, dir, expired)
	var fc *fakeCaptures
	h := newHarnessIn(t, dir, func(c *run.Config) {
		fc = newFakeCaptures(c.Gateway.(*fakeGateway))
		c.Captures = fc
	})
	if err := h.m.Recover(); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.deleted) != 1 || fc.deleted[0] != expired.Id {
		t.Errorf("deleted %v", fc.deleted)
	}
}
