package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the part of a run that records packets (plan §2.10, §2.11, M17): `capture: true` records the
// target's traffic for the whole run, a `capture` step starts or stops the recording of a (narrower) target. The
// captures are attached to the run (`Capture.run`) and offered as its artifacts. A capture that cannot be made, or
// ends before the run does (a disk below 10 % free space, a lost interface), never ends the run: the run gets the
// warning `capture_missing`, and the report says what is missing.

// captureTimeout bounds the calls to the capture manager that the timeline or the end of a run waits for.
const captureTimeout = 30 * time.Second

// runCapture is a capture of a run: made by a `capture: true` (step is empty) or by a step. The goroutine of the run
// is the only one that touches the list.
type runCapture struct {
	id      uuid.UUID
	step    string
	key     string // the target, for the steps that stop it
	running bool   // as far as the run knows
}

// targetKey is the target of a step as a string: two steps that name the same device, group or network have the
// same key (the scenario of a run is resolved, so references are UUIDs).
func targetKey(s model.Scope) string {
	raw, _ := json.Marshal(s)
	return strings.ToLower(string(raw))
}

// captureScopeOf turns the target of a run into the scope of a capture: a device or a network as it is, a group as
// a selector with the group as its source.
func captureScopeOf(s model.Scope) (model.CaptureScope, error) {
	var cs model.CaptureScope
	switch {
	case s.Device != nil:
		cs.Device = s.Device
	case s.Network != nil:
		cs.Network = s.Network
	case s.Group != nil:
		raw, err := json.Marshal(map[string]any{"selector": map[string]any{"source": s}})
		if err != nil {
			return cs, err
		}
		if err := json.Unmarshal(raw, &cs); err != nil {
			return cs, err
		}
	default:
		return cs, errors.New("a capture records a device, a group or a network")
	}
	return cs, nil
}

// startCapture starts a capture of the target, attached to the run.
func (m *Manager) startCapture(r *runState, step string, target model.Scope) (model.Capture, error) {
	scope, err := captureScopeOf(target)
	if err != nil {
		return model.Capture{}, err
	}
	name := r.rec.Scenario.Name
	if step != "" {
		name += " " + step
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), captureTimeout)
	defer cancel()
	cp, err := m.cfg.Captures.Start(ctx, scope, name, r.id)
	if err != nil {
		return model.Capture{}, err
	}
	r.caps = append(r.caps, &runCapture{id: cp.Id, step: step, key: targetKey(target), running: true})
	return cp, nil
}

// missingCapture records that the run lacks a capture it was meant to have.
func (m *Manager) missingCapture(r *runState, step, why string) {
	w := model.RunWarning{Code: model.CaptureMissing, Message: why}
	if step != "" {
		w.Step = &step
	}
	m.addWarning(r, w)
}

// startRunCapture starts the capture of `capture: true`, before the timeline: the run goes on without it when it
// cannot.
func (m *Manager) startRunCapture(r *runState) {
	if r.sc.Capture == nil || !*r.sc.Capture {
		return
	}
	if _, err := m.startCapture(r, "", r.sc.Target); err != nil {
		m.log.Warn("a run cannot capture", "run", r.id, "error", err)
		m.missingCapture(r, "", "the capture of the run could not be started: "+err.Error())
	}
}

// captureStep starts or stops the recording of the target of a `capture` step. A step that has nothing to do is
// skipped with the reason; a capture that cannot start is skipped and reported, and the run goes on.
func (m *Manager) captureStep(r *runState, pos int, st *model.Step, start time.Duration) error {
	target := r.sc.Target
	if st.Target != nil {
		target = *st.Target
	}
	key := targetKey(target)
	if *st.Capture == model.Start {
		for _, c := range r.caps {
			if c.running && c.step != "" && c.key == key {
				return m.skipStep(r, pos, st, start, fmt.Sprintf("the step %s records this target already", c.step))
			}
		}
		if _, err := m.startCapture(r, st.Id, target); err != nil {
			m.log.Warn("a run cannot capture", "run", r.id, "step", st.Id, "error", err)
			m.missingCapture(r, st.Id, fmt.Sprintf("the capture of step %s could not be started: %v", st.Id, err))
			return m.skipStep(r, pos, st, start, "the capture could not be started: "+err.Error())
		}
		m.applied(r, pos, st, start, nil, nil)
		return nil
	}
	stopped := 0
	for _, c := range r.caps {
		if !c.running || c.step == "" || c.key != key {
			continue
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), captureTimeout)
		_, err := m.cfg.Captures.Stop(ctx, c.id, model.CaptureStopReasonUser)
		cancel()
		if err != nil {
			m.log.Warn("a run cannot stop a capture", "run", r.id, "capture", c.id, "error", err)
		}
		c.running = false
		stopped++
	}
	if stopped == 0 {
		return m.skipStep(r, pos, st, start, "no capture of this target is running")
	}
	m.applied(r, pos, st, start, nil, nil)
	return nil
}

// finishCaptures ends the captures of the run that still run (the reason is `run_end`), says of each that ended
// early that its traffic is missing, and keeps the artifacts of them all.
func (m *Manager) finishCaptures(r *runState) {
	if m.cfg.Captures == nil || len(r.caps) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), captureTimeout)
	defer cancel()
	var final []model.Capture
	for _, c := range r.caps {
		cp, err := m.cfg.Captures.Get(c.id)
		if err != nil {
			m.log.Warn("a capture of a run is gone", "run", r.id, "capture", c.id, "error", err)
			m.missingCapture(r, c.step, fmt.Sprintf("the capture %s is gone: %v", c.id, err))
			continue
		}
		if cp.Status == model.CaptureStatusRunning {
			if cp, err = m.cfg.Captures.Stop(ctx, c.id, model.CaptureStopReasonRunEnd); err != nil {
				m.log.Warn("a run cannot stop a capture", "run", r.id, "capture", c.id, "error", err)
				m.missingCapture(r, c.step, fmt.Sprintf("the capture %s could not be stopped: %v", c.id, err))
				continue
			}
		}
		c.running = false
		if reason := deref(cp.StopReason); reason != model.CaptureStopReasonRunEnd && reason != model.CaptureStopReasonUser {
			why := fmt.Sprintf("the capture %s ended before the run (%s)", c.id, reason)
			if cp.Message != nil {
				why += ": " + *cp.Message
			}
			m.missingCapture(r, c.step, why+"; what came after is not recorded")
		}
		final = append(final, cp)
	}
	m.mu.Lock()
	r.capArts = captureArtifacts(final)
	m.mu.Unlock()
}

// captureArtifacts lists captures as artifacts of a run.
func captureArtifacts(caps []model.Capture) []model.Artifact {
	var out []model.Artifact
	for _, cp := range caps {
		a := model.Artifact{Kind: model.ArtifactKindCapture, Url: "/api/v1/captures/" + cp.Id.String() + "/download", Capture: ptr(cp.Id), SizeBytes: cp.SizeBytes}
		out = append(out, a)
		if deref(cp.Keylog) {
			out = append(out, model.Artifact{Kind: model.ArtifactKindKeylog, Url: "/api/v1/captures/" + cp.Id.String() + "/download?part=keylog", Capture: ptr(cp.Id)})
		}
	}
	return out
}
