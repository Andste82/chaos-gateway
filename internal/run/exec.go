package run

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// cleanupTimeout bounds the removal of a run's overlays at its end: the run does not end until they are
// gone, so it must end in the end.
const cleanupTimeout = 2 * time.Minute

// cleanupAttempts is how often the removal is tried when the engine refuses it (a failed apply).
const cleanupAttempts = 3

// stepError is a step that the engine could not execute: the run ends as `error`.
type stepError struct {
	step string
	err  error
}

func (e *stepError) Error() string { return fmt.Sprintf("step %s: %v", e.step, e.err) }
func (e *stepError) Unwrap() error { return e.err }

// preconditionError is a baseline check that failed (plan §2.10).
type preconditionError struct {
	id  model.PreconditionId
	why string
}

func (e *preconditionError) Error() string {
	return fmt.Sprintf("precondition %s failed: %s", e.id, e.why)
}

// execute is the goroutine of a running run: its preconditions, its timeline and, whatever happens, the
// removal of its overlays.
func (m *Manager) execute(r *runState) {
	defer m.runWG.Done()
	status, reason := model.RunStatusError, "internal error"
	defer func() {
		if p := recover(); p != nil {
			m.log.Error("a run panicked", "run", r.id, "panic", p)
			status, reason = model.RunStatusError, fmt.Sprintf("internal error: %v", p)
		}
		m.finish(r, status, reason)
	}()
	status, reason = m.timeline(r)
}

// outcome turns the error that stopped a run into its end: an abort or an expired lease is `aborted`
// with its reason, anything else is `error`.
func (m *Manager) outcome(r *runState, err error) (model.RunStatus, string) {
	var cause *abortCause
	if errors.As(context.Cause(r.ctx), &cause) {
		return model.RunStatusAborted, cause.reason
	}
	return model.RunStatusError, err.Error()
}

// timeline checks the baseline and runs the steps in the order of the timeline. The steps after a `wait`
// are shifted by the time it waited.
func (m *Manager) timeline(r *runState) (model.RunStatus, string) {
	if err := r.ctx.Err(); err != nil {
		return m.outcome(r, err)
	}
	if err := m.preconditions(r); err != nil {
		return m.outcome(r, err)
	}
	start := m.clk.Monotonic()
	var shift time.Duration
	for pos, idx := range r.order {
		st := &r.sc.Steps[idx]
		at, _ := time.ParseDuration(st.At)
		due := start + at + shift
		if err := m.sleepUntil(r.ctx, due); err != nil {
			return m.outcome(r, err)
		}
		var err error
		switch domain.StepKind(st) {
		case "remove":
			err = m.removeStep(r, pos, st, start)
		case "restore":
			err = m.restoreStep(r, pos, st, start)
		case "wait":
			err = m.waitStep(r, pos, st, start, due)
			shift = max(shift, m.clk.Monotonic()-start-at)
		default:
			err = m.overlayStep(r, pos, st, start)
		}
		if err != nil {
			return m.outcome(r, err)
		}
	}
	return model.RunStatusPassed, ""
}

// sleepUntil waits until the monotonic clock reaches due, or the run is cancelled.
func (m *Manager) sleepUntil(ctx context.Context, due time.Duration) error {
	d := due - m.clk.Monotonic()
	if d <= 0 {
		return ctx.Err()
	}
	t := m.clk.NewTimer(d)
	if due <= m.clk.Monotonic() { // the clock moved between the reading and the timer
		t.Stop()
		return ctx.Err()
	}
	select {
	case <-t.C():
		return ctx.Err()
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}

// ---- steps

// setStep changes the record of the step at a position of the timeline.
func (m *Manager) setStep(r *runState, pos int, f func(*model.RunStep)) {
	m.mu.Lock()
	steps := *r.rec.Steps
	f(&steps[pos])
	m.mu.Unlock()
}

// addWarning adds a warning to the run.
func (m *Manager) addWarning(r *runState, w model.RunWarning) {
	m.mu.Lock()
	var ws []model.RunWarning
	if r.rec.Warnings != nil {
		ws = *r.rec.Warnings
	}
	ws = append(ws, w)
	r.rec.Warnings = &ws
	m.mu.Unlock()
}

// applied records a step as applied and announces it.
func (m *Manager) applied(r *runState, pos int, st *model.Step, start time.Duration, res *engine.OverlayResult, overlayID *uuid.UUID) {
	now, off := m.clk.Now(), m.clk.Monotonic()-start
	var queueMs *int
	if res != nil {
		queueMs = ptr(int(res.QueueWait / time.Millisecond))
	}
	m.setStep(r, pos, func(s *model.RunStep) {
		s.Status = model.RunStepStatusApplied
		s.AppliedAt, s.OffsetMs, s.QueueWaitMs = &now, ptr(int(off/time.Millisecond)), queueMs
		if res != nil {
			s.Generation = ptr(int64(res.Generation))
		}
		if overlayID != nil {
			s.Overlay = overlayID
		}
	})
	if res != nil {
		m.mu.Lock()
		r.generation = max(r.generation, res.Generation)
		m.mu.Unlock()
		if res.QueueWait > StepLateThreshold {
			m.addWarning(r, model.RunWarning{Code: model.StepLate, Step: &st.Id, Overlay: overlayID,
				Message: fmt.Sprintf("step %s waited %d ms for the executor behind other changes (more than %d ms)", st.Id, res.QueueWait.Milliseconds(), StepLateThreshold.Milliseconds())})
		}
	}
	m.save(r)
	data := map[string]any{"step": st.Id, "status": string(model.RunStepStatusApplied), "offset_ms": int(off / time.Millisecond)}
	if res != nil {
		data["generation"] = int64(res.Generation)
	}
	m.emit(r, engine.EventRunStep, data)
}

// failStep records a step the engine could not execute.
func (m *Manager) failStep(r *runState, pos int, st *model.Step, start time.Duration, err error) error {
	off := m.clk.Monotonic() - start
	if r.ctx.Err() != nil {
		return err // an abort, not a failure of the step
	}
	m.setStep(r, pos, func(s *model.RunStep) {
		s.Status, s.Error, s.OffsetMs = model.RunStepStatusError, ptr(err.Error()), ptr(int(off/time.Millisecond))
	})
	m.save(r)
	m.emit(r, engine.EventRunStep, map[string]any{"step": st.Id, "status": string(model.RunStepStatusError), "error": err.Error()})
	return &stepError{step: st.Id, err: err}
}

// overlayStep writes the overlay of a step (profile, fault, rule, WireGuard action). The overlay store
// keeps the key (owner, kind, target, selector), so a step with the key of an earlier one replaces its
// overlay and a step's parameters are never merged with the earlier ones.
func (m *Manager) overlayStep(r *runState, pos int, st *model.Step, start time.Duration) error {
	req, err := domain.StepRequest(r.sc, st)
	if err != nil {
		return m.failStep(r, pos, st, start, err)
	}
	runID := r.id
	res, err := m.cfg.Gateway.PutOverlay(r.ctx, engine.OverlayWrite{Owner: r.owner, Run: &model.OverlayRunRef{Id: runID, Step: st.Id}, Request: req})
	if err != nil {
		return m.failStep(r, pos, st, start, err)
	}
	id := res.Overlay.Id
	r.overlays[st.Id] = id
	r.own(id, st.Id)
	m.applied(r, pos, st, start, &res, &id)
	return nil
}

// removeStep deletes the overlay under the key of an earlier step.
func (m *Manager) removeStep(r *runState, pos int, st *model.Step, start time.Duration) error {
	id, ok := r.overlays[*st.Remove]
	if !ok {
		return m.skipStep(r, pos, st, start, fmt.Sprintf("step %s wrote no overlay", *st.Remove))
	}
	m.captureCounters(r)
	res, err := m.cfg.Gateway.DeleteOverlay(r.ctx, id, &r.owner, r.owner)
	if errors.Is(err, engine.ErrOverlayNotFound) {
		return m.skipStep(r, pos, st, start, "the overlay is gone already")
	}
	if err != nil {
		return m.failStep(r, pos, st, start, err)
	}
	r.release(id)
	m.applied(r, pos, st, start, &res, &id)
	return nil
}

// restoreStep removes every overlay of the run.
func (m *Manager) restoreStep(r *runState, pos int, st *model.Step, start time.Duration) error {
	m.captureCounters(r)
	res, err := m.cfg.Gateway.ResetOverlays(r.ctx, &r.owner, r.owner)
	if err != nil {
		return m.failStep(r, pos, st, start, err)
	}
	r.releaseAll()
	m.applied(r, pos, st, start, &res, nil)
	return nil
}

// skipStep records a step that had nothing to do.
func (m *Manager) skipStep(r *runState, pos int, st *model.Step, start time.Duration, why string) error {
	off := m.clk.Monotonic() - start
	m.setStep(r, pos, func(s *model.RunStep) {
		s.Status, s.Error, s.OffsetMs = model.RunStepStatusSkipped, ptr(why), ptr(int(off/time.Millisecond))
	})
	m.save(r)
	m.emit(r, engine.EventRunStep, map[string]any{"step": st.Id, "status": string(model.RunStepStatusSkipped), "error": why})
	return nil
}

// ---- the overlays a run owns, and their counters

// ownedOverlay is an overlay a step of the run wrote: the first step that did, whether it is still there, and
// its counter as last read.
type ownedOverlay struct {
	step     string
	alive    bool
	counters *model.Counter
}

func (r *runState) own(id uuid.UUID, step string) {
	if r.owned == nil {
		r.owned = map[uuid.UUID]*ownedOverlay{}
	}
	if o, ok := r.owned[id]; ok {
		o.alive = true
		return
	}
	r.owned[id] = &ownedOverlay{step: step, alive: true}
	r.ownedOrder = append(r.ownedOrder, id)
}

func (r *runState) release(id uuid.UUID) {
	if o, ok := r.owned[id]; ok {
		o.alive = false
	}
}

func (r *runState) releaseAll() {
	for _, o := range r.owned {
		o.alive = false
	}
	clear(r.overlays)
}

// captureCounters reads the counters of the overlays of the run that are still there, before something
// removes them: the report holds the final counters of every overlay the run created.
func (m *Manager) captureCounters(r *runState) {
	var alive []uuid.UUID
	for _, id := range r.ownedOrder {
		if r.owned[id].alive {
			alive = append(alive, id)
		}
	}
	if len(alive) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 10*time.Second)
	defer cancel()
	for id, c := range m.cfg.Observer.Counters(ctx, alive) {
		if o, ok := r.owned[id]; ok && c != nil {
			o.counters = c
		}
	}
}

// counterRecords are the final counters of the overlays the run created, in the order of the steps.
func (r *runState) counterRecords() []counterRecord {
	var out []counterRecord
	for _, id := range r.ownedOrder {
		if o := r.owned[id]; o.counters != nil {
			out = append(out, counterRecord{Step: o.step, Overlay: id, Counters: *o.counters})
		}
	}
	return out
}

// ---- the end of a run

// finish removes the run's overlays, which is the last thing a run does whatever the reason it ends, and
// records the end. A run whose overlays cannot be removed has not ended well: it is `error`.
func (m *Manager) finish(r *runState, status model.RunStatus, reason string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), cleanupTimeout)
	defer cancel()
	m.captureCounters(r)
	var cleanErr error
	for attempt := 0; attempt < cleanupAttempts; attempt++ {
		res, err := m.cfg.Gateway.ResetOverlays(ctx, &r.owner, r.owner)
		if err == nil {
			cleanErr = nil
			m.mu.Lock()
			r.generation = max(r.generation, res.Generation)
			m.mu.Unlock()
			break
		}
		cleanErr = err
		if errors.Is(err, engine.ErrClosed) {
			break // the engine is gone: so are the overlays
		}
		m.log.Warn("cannot remove the overlays of a run", "run", r.id, "attempt", attempt+1, "error", err)
		if attempt == cleanupAttempts-1 {
			break
		}
		select {
		case <-m.clk.After(time.Second):
		case <-ctx.Done():
		}
	}
	if cleanErr != nil && !errors.Is(cleanErr, engine.ErrClosed) && (status == model.RunStatusPassed || status == model.RunStatusFailed) {
		status, reason = model.RunStatusError, "the overlays of the run could not be removed: "+cleanErr.Error()
	}
	r.releaseAll()
	m.mu.Lock()
	m.finishLocked(r, status, reason, r.counterRecords())
	m.mu.Unlock()
	r.cancel(nil)
	m.afterFinish(r)
}

// ---- preconditions

// preconditions checks the baseline before the first step (plan §2.10): the target is online, it uses the
// gateway's DNS when the scenario has DNS or host name steps, and no overlay of another owner acts on it.
// A scenario skips the ones it names.
func (m *Manager) preconditions(r *runState) error {
	skip := map[model.PreconditionId]bool{}
	if r.sc.Preconditions != nil {
		for _, id := range deref(r.sc.Preconditions.Skip) {
			skip[id] = true
		}
	}
	snap := m.cfg.Gateway.Snapshot()
	devs := scopeDevices(snap, r.sc.Target)
	var online []engine.DeviceState
	for _, d := range devs {
		if d.Online {
			online = append(online, d)
		}
	}
	name := scopeName(snap, r.sc.Target)
	if !skip["target_online"] {
		switch {
		case scopeKind(r.sc.Target) == "device" && len(devs) == 0:
			return &preconditionError{"target_online", name + " is not known to the gateway"}
		case scopeKind(r.sc.Target) == "device" && !devs[0].Online:
			return &preconditionError{"target_online", name + " is offline"}
		case len(online) == 0:
			return &preconditionError{"target_online", "no device of the " + name + " is online"}
		}
	}
	if !skip["uses_gateway_dns"] && domain.ScenarioUsesDNS(r.sc) {
		check := online
		if scopeKind(r.sc.Target) == "device" {
			check = devs
		}
		for _, d := range check {
			if why := m.notUsingGatewayDNS(snap, d); why != "" {
				return &preconditionError{"uses_gateway_dns", fmt.Sprintf("device %s %s", d.Name, why)}
			}
		}
	}
	if !skip["no_foreign_overlays"] {
		tokens := scopeTokens(snap, r.sc.Target)
		var foreign []string
		for _, ov := range snap.Overlays {
			if ov.Owner.Type == model.ActorTypeRun && ov.Owner.Id == r.id.String() {
				continue
			}
			if overlap(tokens, overlayTokens(snap, ov)) {
				foreign = append(foreign, fmt.Sprintf("%s (%s of %s %s)", ov.Id, ov.Kind, ov.Owner.Type, ownerLabel(ov.Owner)))
			}
		}
		if len(foreign) > 0 {
			sort.Strings(foreign)
			more := ""
			if len(foreign) > 3 {
				more = fmt.Sprintf(" and %d more", len(foreign)-3)
				foreign = foreign[:3]
			}
			return &preconditionError{"no_foreign_overlays", fmt.Sprintf("the overlays %s%s act on the %s", strings.Join(foreign, ", "), more, name)}
		}
	}
	return nil
}

func ownerLabel(o model.Owner) string {
	if o.Name != nil && *o.Name != "" {
		return *o.Name
	}
	return o.Id
}

// notUsingGatewayDNS returns why a device does not use the gateway's DNS, "" when it does: it has asked
// the DNS proxy, or its DHCP lease comes from a network that hands out the gateway as the DNS server.
func (m *Manager) notUsingGatewayDNS(snap *engine.Snapshot, d engine.DeviceState) string {
	if m.cfg.Observer.DNSClientSeen(d.ID) {
		return ""
	}
	if d.Lease == nil {
		return "has not asked the gateway's DNS and has no lease from the gateway"
	}
	gateway := ""
	for _, b := range snap.Bridges {
		if strings.EqualFold(b.NetworkID, d.Network) {
			gateway = b.Address.Addr().String()
		}
	}
	if snap.Config != nil && snap.Config.Networks != nil {
		for id, n := range *snap.Config.Networks {
			if !strings.EqualFold(id, d.Network) {
				continue
			}
			lan, err := n.AsLanNetwork()
			if err != nil || lan.Dhcp == nil || lan.Dhcp.Options == nil || lan.Dhcp.Options.DnsServers == nil || len(*lan.Dhcp.Options.DnsServers) == 0 {
				return ""
			}
			for _, s := range *lan.Dhcp.Options.DnsServers {
				if s == gateway {
					return ""
				}
			}
			return "has not asked the gateway's DNS, and its network's DHCP hands out other DNS servers"
		}
	}
	return ""
}

// ---- wait steps

// waitStep stops the timeline until the condition of a `wait` step holds. A timeout ends the run as `error`,
// like a failed precondition.
func (m *Manager) waitStep(r *runState, pos int, st *model.Step, start, due time.Duration) error {
	w := st.Wait
	timeout, _ := time.ParseDuration(w.Timeout)
	target := r.sc.Target
	if st.Target != nil {
		target = *st.Target
	}
	waitStart := m.clk.Monotonic()
	since := m.clk.Now()
	var known map[string]bool
	if w.For == model.WaitConditionForConnected {
		flows, err := m.cfg.Observer.Flows(r.ctx)
		if err != nil {
			m.log.Debug("cannot read the connections", "error", err)
		}
		known = map[string]bool{}
		for _, f := range flows {
			known[f.ID] = true
		}
	}
	for {
		ok, why := m.condition(r, w, target, known, since)
		if ok {
			m.applied(r, pos, st, start, nil, nil)
			return nil
		}
		left := timeout - (m.clk.Monotonic() - waitStart)
		if left <= 0 {
			return m.failStep(r, pos, st, start, fmt.Errorf("the wait for %s timed out after %s: %s", describeWait(w), w.Timeout, why))
		}
		if err := m.sleepUntil(r.ctx, m.clk.Monotonic()+min(waitPoll, left)); err != nil {
			return err
		}
	}
}

func describeWait(w *model.WaitCondition) string {
	switch w.For {
	case model.WaitConditionForOnline:
		return "the device to be online"
	case model.WaitConditionForConnected:
		return "a new connection"
	}
	return "a DNS query for " + deref(w.Name)
}

// condition evaluates a wait condition for the target against what the gateway sees now. The second
// result says what is missing.
func (m *Manager) condition(r *runState, w *model.WaitCondition, target model.Scope, known map[string]bool, since time.Time) (bool, string) {
	snap := m.cfg.Gateway.Snapshot()
	devs := scopeDevices(snap, target)
	switch w.For {
	case model.WaitConditionForOnline:
		for _, d := range devs {
			if d.Online {
				return true, ""
			}
		}
		return false, "no device of the " + scopeName(snap, target) + " is online"
	case model.WaitConditionForDnsQuery:
		if m.cfg.Observer.DNSQuerySeen(ids(devs), deref(w.Name), since) {
			return true, ""
		}
		return false, "no query seen"
	case model.WaitConditionForConnected:
		flows, err := m.cfg.Observer.Flows(r.ctx)
		if err != nil {
			return false, "cannot read the connections: " + err.Error()
		}
		set := idSet(devs)
		match := model.TrafficMatch{Destination: w.Destination, PortRanges: w.PortRanges, Ports: w.Ports, Protocol: w.Protocol}
		for _, f := range flows {
			if !known[f.ID] && flowInScope(set, target, f) && matchFlow(snap, match, f) && established(f) {
				return true, ""
			}
		}
		return false, "no new connection established"
	}
	return false, "unknown condition"
}
