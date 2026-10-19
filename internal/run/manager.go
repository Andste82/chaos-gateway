package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Manager owns the runs: it makes them, queues them, starts them, and keeps their records.
type Manager struct {
	cfg Config
	clk clock.Clock
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // the recorder
	runWG  sync.WaitGroup // the goroutines of the running runs

	mu     sync.Mutex
	runs   map[uuid.UUID]*runState
	nextN  uint64
	closed bool
}

// runState is one run. The record, the lease timer and the counters are guarded by Manager.mu; the
// rest is set when the run is made and never changes, or belongs to the goroutine that executes it.
type runState struct {
	id    uuid.UUID
	n     uint64 // creation order
	sc    *model.Scenario
	owner model.Owner // the run as the owner of its overlays
	actor model.Actor // who started it
	lease time.Duration
	order []int // the timeline: indexes of sc.Steps by position

	rec        model.Run
	leaseTimer clock.Timer
	counters   []counterRecord
	generation uint64 // of the last change of the run's overlays, for Abort
	cleanupErr error  // why the overlays could not be removed when the run ended, nil when they were

	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{} // closed when the run has ended and cleaned up

	recorded     chan struct{} // closed when run_finished is in the event file
	recordedOnce sync.Once

	fileMu  sync.Mutex
	seq     uint64 // of the last record handed to save, written is the last one on disk
	written uint64

	// overlays maps a step to the overlay it wrote or replaced, owned has every overlay the run wrote (in
	// the order of ownedOrder) with its counters; the goroutine of the run only
	overlays   map[string]uuid.UUID
	byKey      map[string]uuid.UUID // the overlay under the key (owner, kind, target, selector) of the steps so far
	owned      map[uuid.UUID]*ownedOverlay
	ownedOrder []uuid.UUID

	// caps are the captures of the run (the goroutine of the run only), capArts their artifacts once the run has ended
	// (guarded by Manager.mu)
	caps    []*runCapture
	capArts []model.Artifact

	// moments are the moments at which the timeline began and reached each step (and "start"), guarded by Manager.mu;
	// progress wakes the goroutine of the checks when it did (capacity one: a wake that is already pending is enough)
	moments  map[string]stepMoments
	progress chan struct{}
}

// abortCause is why a run is cancelled; context.Cause(run.ctx) returns it.
type abortCause struct{ reason string }

func (c *abortCause) Error() string { return c.reason }

// New creates a manager. Call Recover once the engine runs, to take over what an earlier process left.
func New(cfg Config) (*Manager, error) {
	if cfg.Clock == nil || cfg.Gateway == nil || cfg.Observer == nil || cfg.Dir == "" {
		return nil, errors.New("run: the clock, the gateway, the observer and the directory are required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Retention <= 0 {
		cfg.Retention = DefaultRetention
	}
	if cfg.MaxRuns <= 0 {
		cfg.MaxRuns = DefaultMaxRuns
	}
	if cfg.NewID == nil {
		cfg.NewID = uuid.New
	}
	if cfg.EncodeEvent == nil {
		cfg.EncodeEvent = defaultEncode
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{cfg: cfg, clk: cfg.Clock, log: cfg.Log, runs: map[uuid.UUID]*runState{}}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.startRecorder()
	return m, nil
}

func defaultEncode(ev engine.Event) ([]byte, bool) {
	b, err := json.Marshal(map[string]any{"type": ev.Type, "time": ev.Time.UTC(), "data": ev.Data})
	return b, err == nil
}

// Close aborts the runs that are queued or running (their overlays are removed through the engine), waits
// for them and stops the recorder. The manager answers ErrClosed afterwards.
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	var active []*runState
	for _, r := range m.runs {
		if !ended(r.rec.Status) {
			active = append(active, r)
		}
	}
	m.mu.Unlock()
	for _, r := range active {
		m.abortNoWait(r, &abortCause{"service stopping"})
	}
	// every run goroutine, also one that has ended its run and is announcing it, before the recorder stops
	idle := make(chan struct{})
	go func() { m.runWG.Wait(); close(idle) }()
	select {
	case <-idle:
	case <-ctx.Done():
	}
	m.cancel()
	m.wg.Wait()
}

// Recover takes over what an earlier process left: the records of its runs are loaded, a run that was
// queued or running ends as `aborted` with the reason `service restart` (its overlays died with the
// process: the kernel is recompiled from the committed revision, plan §2.1.1), and records older than the
// retention are deleted.
func (m *Manager) Recover() error {
	idsOnDisk, err := listRunDirs(m.cfg.Dir)
	if err != nil {
		return err
	}
	now := m.clk.Now()
	for _, id := range idsOnDisk {
		f, err := readRunFile(m.cfg.Dir, id)
		if err != nil {
			m.log.Warn("cannot read the record of a run: it is left alone", "run", id, "error", err)
			continue
		}
		if endedBefore(f.Run, now.Add(-m.cfg.Retention)) {
			if err := removeRun(m.cfg.Dir, id); err != nil {
				m.log.Warn("cannot delete an expired run", "run", id, "error", err)
			}
			if m.cfg.Captures != nil {
				m.cfg.Captures.DeleteRun(id)
			}
			continue
		}
		r := &runState{id: id, rec: f.Run, counters: f.Counters, done: closedChan(), recorded: make(chan struct{})}
		if f.Run.Snapshot != nil {
			r.sc = f.Run.Snapshot
		}
		m.mu.Lock()
		m.nextN++
		r.n = m.nextN
		m.runs[id] = r
		m.mu.Unlock()
		if ended(f.Run.Status) {
			// its end was recorded by the process that made it
			r.recordedOnce.Do(func() { close(r.recorded) })
			continue
		}
		m.mu.Lock()
		failRemaining(&r.rec, "the service restarted")
		failPendingChecks(&r.rec, model.RunStatusAborted, "service restart")
		r.rec.Status, r.rec.Reason, r.rec.EndedAt = model.RunStatusAborted, ptr("service restart"), ptr(now)
		r.rec.LeaseExpiresAt = nil
		if m.cfg.Captures != nil {
			// the captures the process left (they ended with it) are the artifacts of the run
			r.capArts = captureArtifacts(m.cfg.Captures.OfRun(id))
		}
		r.rec.Artifacts = artifactsOf(r)
		m.mu.Unlock()
		m.save(r)
		m.cfg.Gateway.Emit(engine.EventRunFinished, runEventData(r, model.Actor{Type: model.ActorTypeSystem, Id: "system"}, map[string]any{"status": string(model.RunStatusAborted), "reason": "service restart"}))
	}
	return nil
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func ptr[T any](v T) *T { return &v }

// ---- creating a run

// Create makes a run of a stored or inline scenario. The scenario is changed by the parameters,
// checked against the active revision and stored with the run as it will be executed. The run starts at
// once when no queued or running run has a target that overlaps its own, else it waits in `queued`
// (CreateRequest.Queue) or is refused with *TargetBusyError.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (model.Run, error) {
	snap := m.cfg.Gateway.Snapshot()
	if snap == nil || snap.Config == nil {
		return model.Run{}, engine.ErrNoConfiguration
	}
	sc, stored, err := scenarioOf(snap.Config, req)
	if err != nil {
		return model.Run{}, err
	}
	sc, err = domain.ParameterizeScenario(sc, req.Parameters)
	if err != nil {
		return model.Run{}, err
	}
	resolved, verrs := domain.ResolveScenario(snap.Config, sc, domain.WithDiscovered(discoveredIDs(snap)...))
	if len(verrs) > 0 {
		return model.Run{}, domain.ValidationErrors(verrs)
	}
	if err := m.supported(snap.Config, resolved); err != nil {
		return model.Run{}, err
	}

	id := m.cfg.NewID()
	r := &runState{id: id, sc: resolved, actor: req.Owner, lease: req.Lease, order: domain.TimelineOrder(resolved.Steps),
		overlays: map[string]uuid.UUID{}, byKey: map[string]uuid.UUID{},
		moments: map[string]stepMoments{}, progress: make(chan struct{}, 1), done: make(chan struct{}), recorded: make(chan struct{})}
	name := resolved.Name
	r.owner = model.Owner{Type: model.ActorTypeRun, Id: id.String(), Name: &name}
	r.ctx, r.cancel = context.WithCancelCause(m.ctx)
	now := m.clk.Now()
	r.rec = model.Run{Id: id, Revision: snap.Revision, Target: resolved.Target, Owner: req.Owner, Status: model.RunStatusQueued, CreatedAt: now, Snapshot: resolved}
	r.rec.Scenario.Name, r.rec.Scenario.Inline = name, stored == nil
	if stored != nil {
		r.rec.Scenario.Id = stored
	}
	steps := make([]model.RunStep, 0, len(r.order))
	for _, i := range r.order {
		st := resolved.Steps[i]
		step := model.RunStep{Id: st.Id, Status: model.RunStepStatusPending, ScheduledOffset: ptr(st.At)}
		if k := domain.StepKind(&st); k != "" {
			step.Type = ptr(model.RunStepType(k))
		}
		steps = append(steps, step)
	}
	r.rec.Steps = &steps
	if cs := deref(resolved.Checks); len(cs) > 0 {
		results := make([]model.CheckResult, len(cs))
		for i := range cs {
			results[i] = model.CheckResult{Index: i, Name: checkName(snap.Config, &cs[i]), Type: model.CheckResultType(domain.CheckKind(&cs[i])), Status: model.CheckResultStatusPending}
		}
		r.rec.Checks = &results
	}

	tokens := scopeTokens(snap, resolved.Target)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		r.cancel(nil)
		return model.Run{}, ErrClosed
	}
	var busy *runState
	active := 0
	for _, o := range m.runs {
		if ended(o.rec.Status) {
			continue
		}
		active++
		if (busy == nil || o.n < busy.n) && overlap(tokens, scopeTokens(snap, o.rec.Target)) {
			busy = o
		}
	}
	switch {
	case busy != nil && !req.Queue:
		e := &TargetBusyError{Run: busy.id, Scenario: busy.rec.Scenario.Name}
		m.mu.Unlock()
		r.cancel(nil)
		return model.Run{}, e
	case active >= m.cfg.MaxRuns:
		m.mu.Unlock()
		r.cancel(nil)
		return model.Run{}, ErrTooManyRuns
	}
	m.nextN++
	r.n = m.nextN
	if err := os.MkdirAll(runDir(m.cfg.Dir, id), 0o700); err != nil {
		m.mu.Unlock()
		r.cancel(nil)
		return model.Run{}, err
	}
	if req.Lease > 0 {
		exp := now.Add(req.Lease)
		r.rec.LeaseExpiresAt = &exp
		r.leaseTimer = m.clk.AfterFunc(req.Lease, func() { m.abortNoWait(r, &abortCause{"lease expired"}) })
	}
	m.runs[id] = r
	m.mu.Unlock()

	m.save(r)
	if busy != nil {
		m.emit(r, engine.EventRunQueued, map[string]any{"behind": busy.id.String()})
	}
	m.schedule()
	return m.Get(id)
}

// scenarioOf returns a copy of the scenario of the request: a stored one of the active revision (and its
// UUID), or the inline one.
func scenarioOf(cfg *model.Configuration, req CreateRequest) (*model.Scenario, *uuid.UUID, error) {
	if req.Inline != nil {
		sc := *req.Inline
		return &sc, nil, nil
	}
	for id, sc := range deref(cfg.Scenarios) {
		if strings.EqualFold(id, req.Scenario) || strings.EqualFold(sc.Name, req.Scenario) {
			u, err := uuid.Parse(id)
			if err != nil {
				return nil, nil, err
			}
			c := sc
			return &c, &u, nil
		}
	}
	return nil, nil, fmt.Errorf("%w %q in the active revision", ErrNoScenario, req.Scenario)
}

// checkHostname returns a host name that a check names as a destination or an exception, "" when it names none: the
// resolver that tells which addresses belong to a name arrives with M20.
func checkHostname(c *model.Check) string {
	for _, m := range []*model.TrafficMatch{c.Reconnected, c.NoConnection, c.TrafficSeen} {
		if m != nil && m.Destination != nil && m.Destination.Hostname != nil {
			return *m.Destination.Hostname
		}
	}
	if n := c.TrafficNotSeen; n != nil {
		if n.Destination != nil && n.Destination.Hostname != nil {
			return *n.Destination.Hostname
		}
		for _, d := range deref(n.Except) {
			if d.Hostname != nil {
				return *d.Hostname
			}
		}
	}
	return ""
}

func discoveredIDs(snap *engine.Snapshot) []string {
	var out []string
	for _, d := range snap.Devices {
		if d.Origin == model.DeviceOriginDiscovered {
			out = append(out, d.ID)
		}
	}
	return out
}

// supported refuses a scenario that needs a milestone this build does not have (the spec's
// `unsupported_feature`): a step type or a check type, capturing, a host name where the resolver is needed, an
// overlay whose kind or profile is not available.
func (m *Manager) supported(cfg *model.Configuration, sc *model.Scenario) error {
	if m.cfg.Captures == nil {
		if sc.Capture != nil && *sc.Capture {
			return &UnsupportedError{What: "capture: true (the gateway has no capture volume: the executor needs a captures directory)"}
		}
		for i := range sc.Steps {
			if domain.StepKind(&sc.Steps[i]) == "capture" {
				return &UnsupportedError{What: fmt.Sprintf("a capture step (%s; the gateway has no capture volume: the executor needs a captures directory)", sc.Steps[i].Id)}
			}
		}
	}
	for i := range deref(sc.Checks) {
		c := &(*sc.Checks)[i]
		kind := domain.CheckKind(c)
		if kind == "tls_rejected" || kind == "tls_accepted" {
			return &UnsupportedError{What: fmt.Sprintf("a %s check (check %d)", kind, i), Milestone: "M21"}
		}
		if host := checkHostname(c); host != "" {
			return &UnsupportedError{What: fmt.Sprintf("a check (check %d) for the host name %s", i, host), Milestone: "M20"}
		}
	}
	for i := range sc.Steps {
		st := &sc.Steps[i]
		switch domain.StepKind(st) {
		case "wait":
			if d := st.Wait.Destination; d != nil && d.Hostname != nil {
				return &UnsupportedError{What: fmt.Sprintf("a wait step (%s) for a host name", st.Id), Milestone: "M20"}
			}
		}
		if !domain.StepCreatesOverlay(st) {
			continue
		}
		req, err := domain.StepRequest(sc, st)
		if err != nil {
			return err
		}
		var unsup *engine.UnsupportedOverlayError
		if err := engine.CheckOverlaySupported(&req); errors.As(err, &unsup) {
			return &UnsupportedError{What: fmt.Sprintf("%s (step %s)", unsup.What, st.Id), Milestone: unsup.Milestone}
		}
		if req.Profile != nil {
			if p, _, ok := domain.ProfileByID(cfg, *req.Profile); ok {
				if parts := domain.UnavailableParts(p); len(parts) > 0 {
					return &UnsupportedError{What: fmt.Sprintf("the profile %s (step %s; it has a %s part)", p.Name, st.Id, strings.ToUpper(parts[0].Family)), Milestone: parts[0].Milestone}
				}
			}
		}
	}
	return nil
}

// ---- starting runs

// schedule starts the queued runs whose target is free: that no running run and no run queued before them
// overlaps (a run does not overtake an earlier one it would have to wait for if it came first).
func (m *Manager) schedule() {
	snap := m.cfg.Gateway.Snapshot()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	var live []*runState
	for _, r := range m.runs {
		if !ended(r.rec.Status) {
			live = append(live, r)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].n < live[j].n })
	var held []map[string]bool
	var start []*runState
	now := m.clk.Now()
	for _, r := range live {
		tokens := scopeTokens(snap, r.rec.Target)
		blocked := false
		for _, h := range held {
			if overlap(tokens, h) {
				blocked = true
				break
			}
		}
		held = append(held, tokens)
		if r.rec.Status != model.RunStatusQueued || blocked {
			continue
		}
		r.rec.Status, r.rec.StartedAt = model.RunStatusRunning, ptr(now)
		start = append(start, r)
		m.runWG.Add(1)
	}
	m.mu.Unlock()
	for _, r := range start {
		m.save(r)
		m.emit(r, engine.EventRunStarted, nil)
		go m.execute(r)
	}
}

// ---- reading

// view returns the run as the API shows it, with slices of its own. The caller holds m.mu.
func (r *runState) view() model.Run {
	out := r.rec
	out.Steps = cloneSlice(r.rec.Steps)
	out.Checks = cloneSlice(r.rec.Checks)
	out.Warnings = cloneSlice(r.rec.Warnings)
	out.Artifacts = cloneSlice(r.rec.Artifacts)
	return out
}

func cloneSlice[T any](p *[]T) *[]T {
	if p == nil {
		return nil
	}
	c := append([]T(nil), *p...)
	return &c
}

// Get returns a run.
func (m *Manager) Get(id uuid.UUID) (model.Run, error) {
	m.mu.Lock()
	r, ok := m.runs[id]
	if !ok {
		m.mu.Unlock()
		return model.Run{}, ErrNotFound
	}
	v := r.view()
	m.mu.Unlock()
	if ended(v.Status) {
		v = m.sized(v)
	}
	return v, nil
}

// List returns the runs newest first, without their timeline (steps, checks and snapshot), selected by f.
func (m *Manager) List(f Filter) []model.Run {
	m.mu.Lock()
	var rs []*runState
	for _, r := range m.runs {
		if f.Status != "" && r.rec.Status != f.Status {
			continue
		}
		if f.Scenario != "" && !ofScenario(r.rec, f.Scenario) {
			continue
		}
		rs = append(rs, r)
	}
	// newest first by creation time, then by id: the order the API pages by (its cursor is a key of that order)
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i].rec.CreatedAt, rs[j].rec.CreatedAt
		if !a.Equal(b) {
			return a.After(b)
		}
		return rs[i].id.String() < rs[j].id.String()
	})
	out := make([]model.Run, 0, len(rs))
	for _, r := range rs {
		v := r.view()
		v.Steps, v.Checks, v.Snapshot = nil, nil, nil
		out = append(out, v)
	}
	m.mu.Unlock()
	return out
}

// ofScenario reports whether a run is of the scenario with that name or UUID.
func ofScenario(r model.Run, ref string) bool {
	if strings.EqualFold(r.Scenario.Name, ref) {
		return true
	}
	return r.Scenario.Id != nil && strings.EqualFold(r.Scenario.Id.String(), ref)
}

// Busy counts the runs that are running and queued and names them (for update scripts).
func (m *Manager) Busy() (running, queued int, ids []uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var live []*runState
	for _, r := range m.runs {
		switch r.rec.Status {
		case model.RunStatusRunning:
			running++
		case model.RunStatusQueued:
			queued++
		default:
			continue
		}
		live = append(live, r)
	}
	sort.Slice(live, func(i, j int) bool { return live[i].n < live[j].n })
	for _, r := range live {
		ids = append(ids, r.id)
	}
	return running, queued, ids
}

// sameActor reports whether two actors are the same: the type and the id decide, not the name.
func sameActor(a, b model.Actor) bool { return a.Type == b.Type && a.Id == b.Id }

// ---- aborting, renewing

// Abort ends a queued or running run as `aborted` and returns when its overlays are gone. A caller that may
// only touch its own runs passes by; a run that has ended is returned as it is.
func (m *Manager) Abort(ctx context.Context, id uuid.UUID, by *model.Actor) (Abort, error) {
	m.mu.Lock()
	r, ok := m.runs[id]
	switch {
	case !ok:
		m.mu.Unlock()
		return Abort{}, ErrNotFound
	case by != nil && !sameActor(*by, r.rec.Owner):
		m.mu.Unlock()
		return Abort{}, ErrForbidden
	case ended(r.rec.Status):
		v := r.view()
		m.mu.Unlock()
		return Abort{Run: v}, nil
	}
	m.mu.Unlock()
	reason := "aborted"
	if by != nil && by.Name != nil && *by.Name != "" {
		reason = "aborted by " + *by.Name
	} else if by != nil {
		reason = "aborted by " + by.Id
	}
	m.abortNoWait(r, &abortCause{reason})
	select {
	case <-r.done:
	case <-ctx.Done():
		return Abort{}, ctx.Err()
	}
	m.mu.Lock()
	v, gen, cleanup := r.view(), r.generation, r.cleanupErr
	m.mu.Unlock()
	if cleanup != nil {
		return Abort{Run: m.sized(v)}, &CleanupError{Run: m.sized(v), Err: cleanup}
	}
	return Abort{Run: m.sized(v), Generation: gen}, nil
}

// AbortOwned aborts every queued or running run of an owner (every run when by is nil) and returns how many
// it aborted and the newest generation that removed overlays: `POST /reset`.
func (m *Manager) AbortOwned(ctx context.Context, by *model.Actor, reason string) (int, uint64) {
	m.mu.Lock()
	var rs []*runState
	for _, r := range m.runs {
		if !ended(r.rec.Status) && (by == nil || sameActor(*by, r.rec.Owner)) {
			rs = append(rs, r)
		}
	}
	m.mu.Unlock()
	for _, r := range rs {
		m.abortNoWait(r, &abortCause{reason})
	}
	var gen uint64
	for _, r := range rs {
		select {
		case <-r.done:
		case <-ctx.Done():
			return len(rs), gen
		}
		m.mu.Lock()
		gen = max(gen, r.generation)
		m.mu.Unlock()
	}
	return len(rs), gen
}

// abortNoWait ends a run for the reason and returns at once: a queued run is finished here, a running one
// is cancelled and finishes itself. It is what a lease that runs out calls, on a timer that must not wait.
func (m *Manager) abortNoWait(r *runState, cause *abortCause) {
	m.mu.Lock()
	switch r.rec.Status {
	case model.RunStatusQueued:
		m.finishLocked(r, model.RunStatusAborted, cause.reason, nil)
		m.mu.Unlock()
		r.cancel(cause)
		m.afterFinish(r)
	case model.RunStatusRunning:
		m.mu.Unlock()
		r.cancel(cause)
	default:
		m.mu.Unlock()
	}
}

// Renew restarts the lease of a run: the heartbeat of a client that wants the run to go on.
func (m *Manager) Renew(id uuid.UUID, by *model.Actor) (model.Run, error) {
	m.mu.Lock()
	r, ok := m.runs[id]
	switch {
	case !ok:
		m.mu.Unlock()
		return model.Run{}, ErrNotFound
	case by != nil && !sameActor(*by, r.rec.Owner):
		m.mu.Unlock()
		return model.Run{}, ErrForbidden
	case ended(r.rec.Status):
		m.mu.Unlock()
		return model.Run{}, ErrEnded
	case r.lease <= 0 || r.leaseTimer == nil:
		m.mu.Unlock()
		return model.Run{}, ErrNoLease
	}
	r.leaseTimer.Reset(r.lease)
	exp := m.clk.Now().Add(r.lease)
	r.rec.LeaseExpiresAt = &exp
	v := r.view()
	m.mu.Unlock()
	m.save(r)
	return v, nil
}

// ---- revisions

// Reference is a queued or running run that a revision would orphan, with the object that is deleted.
type Reference struct {
	Run    model.Run
	Object string
}

// Orphaned returns the queued and running runs that applying next in place of current would orphan (plan
// §2.1.1): those whose scenario refers to an object of current that next does not have.
func (m *Manager) Orphaned(current, next *model.Configuration, discovered ...string) []Reference {
	m.mu.Lock()
	var rs []*runState
	for _, r := range m.runs {
		if !ended(r.rec.Status) && r.sc != nil {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].n < rs[j].n })
	m.mu.Unlock()
	var out []Reference
	for _, r := range rs {
		for _, obj := range domain.OrphanedScenario(current, next, r.sc, domain.WithDiscovered(discovered...)) {
			m.mu.Lock()
			v := r.view()
			m.mu.Unlock()
			v.Steps, v.Checks, v.Snapshot = nil, nil, nil
			out = append(out, Reference{Run: v, Object: obj})
		}
	}
	return out
}

// AbortReferences aborts the runs of references, for the revision that deletes what they refer to
// (`apply?force=true`), and returns their ids in the order the runs were made. It returns when their overlays are gone.
func (m *Manager) AbortReferences(ctx context.Context, refs []Reference, revision int64) []uuid.UUID {
	seen := map[uuid.UUID]string{}
	for _, ref := range refs {
		if _, ok := seen[ref.Run.Id]; !ok {
			seen[ref.Run.Id] = ref.Object
		}
	}
	var rs []*runState
	m.mu.Lock()
	for id := range seen {
		if r, ok := m.runs[id]; ok {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].n < rs[j].n })
	m.mu.Unlock()
	ids := make([]uuid.UUID, 0, len(rs))
	for _, r := range rs {
		ids = append(ids, r.id)
		m.abortNoWait(r, &abortCause{fmt.Sprintf("revision %d deletes %s, which the run refers to", revision, seen[r.id])})
	}
	for _, r := range rs {
		select {
		case <-r.done:
		case <-ctx.Done():
			return ids
		}
	}
	return ids
}

// ---- finishing a run

// failRemaining marks the steps that did not run as skipped, with the reason.
func failRemaining(rec *model.Run, why string) {
	if rec.Steps == nil {
		return
	}
	steps := *rec.Steps
	for i := range steps {
		if steps[i].Status == model.RunStepStatusPending {
			steps[i].Status = model.RunStepStatusSkipped
			steps[i].Error = ptr("not run: " + why)
		}
	}
}

// failPendingChecks says of the checks that were not decided when the run ended why they were not.
func failPendingChecks(rec *model.Run, status model.RunStatus, reason string) {
	if rec.Checks == nil {
		return
	}
	why := "not decided: the run ended as " + string(status)
	if reason != "" {
		why += " (" + reason + ")"
	}
	cs := *rec.Checks
	for i := range cs {
		if cs[i].Status == model.CheckResultStatusPending {
			cs[i].Message = ptr(why)
		}
	}
}

// finishLocked records the end of a run: its status, reason and time, the steps that did not run, the
// artifacts. The caller holds m.mu and calls afterFinish once it has released it.
func (m *Manager) finishLocked(r *runState, status model.RunStatus, reason string, counters []counterRecord) {
	if ended(r.rec.Status) {
		return
	}
	if r.leaseTimer != nil {
		r.leaseTimer.Stop()
	}
	failRemaining(&r.rec, reason)
	failPendingChecks(&r.rec, status, reason)
	r.rec.Status, r.rec.EndedAt = status, ptr(m.clk.Now())
	r.rec.LeaseExpiresAt = nil
	if reason != "" {
		r.rec.Reason = &reason
	}
	r.counters = counters
	r.rec.Artifacts = artifactsOf(r)
}

// afterFinish writes the record, announces the end of the run and starts the runs that were waiting for
// it. The run's `done` is closed last, so whoever waits for it sees the record and the event.
func (m *Manager) afterFinish(r *runState) {
	m.save(r)
	m.mu.Lock()
	status, reason := string(r.rec.Status), deref(r.rec.Reason)
	m.mu.Unlock()
	data := map[string]any{"status": status}
	if reason != "" {
		data["reason"] = reason
	}
	m.emit(r, engine.EventRunFinished, data)
	// the event file has the end of the run before anyone is told it ended
	m.awaitRecorded(r)
	m.save(r)
	close(r.done)
	m.prune()
	m.schedule()
}

// artifactsOf lists what an ended run offers for download (without the size of the event file, which
// is read when the run is read).
func artifactsOf(r *runState) *[]model.Artifact {
	base := "/api/v1/runs/" + r.id.String()
	out := []model.Artifact{
		{Kind: model.ArtifactKindReportJson, Url: base + "/report.json"},
		{Kind: model.ArtifactKindReportXml, Url: base + "/report.xml"},
		{Kind: model.ArtifactKindEvents, Url: base + "/events"},
	}
	return ptr(append(out, r.capArts...))
}

// prune deletes the records of runs that ended before the retention time.
func (m *Manager) prune() {
	cut := m.clk.Now().Add(-m.cfg.Retention)
	m.mu.Lock()
	var gone []uuid.UUID
	for id, r := range m.runs {
		if endedBefore(r.rec, cut) {
			gone = append(gone, id)
			delete(m.runs, id)
		}
	}
	m.mu.Unlock()
	for _, id := range gone {
		if err := removeRun(m.cfg.Dir, id); err != nil {
			m.log.Warn("cannot delete an expired run", "run", id, "error", err)
		}
		if m.cfg.Captures != nil {
			m.cfg.Captures.DeleteRun(id)
		}
	}
}

// ---- records and events

// save writes the record of a run. Records are written in the order they were taken: one that is overtaken
// by a newer one is dropped.
func (m *Manager) save(r *runState) {
	m.mu.Lock()
	r.seq++
	seq := r.seq
	f := runFile{Version: fileVersion, Run: r.view(), Counters: append([]counterRecord(nil), r.counters...)}
	m.mu.Unlock()
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		m.log.Error("cannot encode a run", "run", r.id, "error", err)
		return
	}
	r.fileMu.Lock()
	defer r.fileMu.Unlock()
	if seq < r.written {
		return
	}
	if err := os.MkdirAll(runDir(m.cfg.Dir, r.id), 0o700); err != nil {
		m.log.Error("cannot write a run", "run", r.id, "error", err)
		return
	}
	if err := writeFileAtomic(runDirFile(m.cfg.Dir, r.id), raw); err != nil {
		m.log.Error("cannot write a run", "run", r.id, "error", err)
		return
	}
	r.written = seq
}

func runDirFile(dir string, id uuid.UUID) string { return filepath.Join(runDir(dir, id), runFileName) }

// runEventData is the data of an event of a run.
func runEventData(r *runState, actor model.Actor, extra map[string]any) map[string]any {
	name := r.rec.Scenario.Name
	data := map[string]any{
		"run": r.id.String(), "name": name, "actor": actor,
		"subject": engine.Subject{Kind: "run", ID: r.id.String(), Name: name},
	}
	maps.Copy(data, extra)
	return data
}

// emit announces something about a run on the event bus.
func (m *Manager) emit(r *runState, typ string, extra map[string]any) {
	m.cfg.Gateway.Emit(typ, runEventData(r, r.actor, extra))
}
