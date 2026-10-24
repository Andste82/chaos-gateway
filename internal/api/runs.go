package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// ---- the engine of runs (M15)

// initRuns starts the manager of the runs: their records are in the directory `runs` of the API's state
// directory (a directory of its own for the life of the server when there is none), and what an earlier
// process left is taken over (a run that was running ended as `aborted`).
func (s *Server) initRuns() error {
	dir := ""
	if s.cfg.StateDir != "" {
		dir = filepath.Join(s.cfg.StateDir, "runs")
	} else {
		tmp, err := os.MkdirTemp("", "chaosgw-runs-")
		if err != nil {
			return err
		}
		dir, s.runsTemp = tmp, tmp
	}
	var caps run.Captures // without a capture volume, a scenario that records is refused
	if s.captures != nil {
		caps = captureAdapter{s}
	}
	m, err := run.New(run.Config{Clock: s.clk, Dir: dir, Gateway: s.cfg.Engine, Observer: runObserver{s}, Captures: caps, Log: s.log, Retention: s.cfg.RunRetention, RetentionOf: s.runRetention, MaxRuns: s.cfg.RunMax, EncodeEvent: s.encodeRunEvent})
	if err != nil {
		return err
	}
	if err := m.Recover(); err != nil {
		m.Close(context.Background())
		return err
	}
	s.runs = m
	return nil
}

// runRetention is how long the record of an ended run is kept: the setting `settings.retention.runs` of the active
// configuration, else (0, so that the manager uses) the default of the service, the flag `--run-retention`.
func (s *Server) runRetention() time.Duration {
	if snap := s.cfg.Engine.Snapshot(); snap != nil && snap.Config != nil && snap.Config.Settings != nil && snap.Config.Settings.Retention != nil {
		if r := snap.Config.Settings.Retention.Runs; r != nil {
			if d, err := time.ParseDuration(*r); err == nil && d > 0 {
				return d
			}
		}
	}
	return 0
}

// closeRuns aborts the runs that are left, which removes their overlays through the engine, and waits for them.
func (s *Server) closeRuns() {
	if s.runs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.runs.Close(ctx)
	if s.runsTemp != "" {
		_ = os.RemoveAll(s.runsTemp)
	}
}

// encodeRunEvent is the line of an event in the event file of a run: the event as the API shows it.
func (s *Server) encodeRunEvent(ev engine.Event) ([]byte, bool) {
	pe, ok := s.toPublic(ev)
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(pe)
	return b, err == nil
}

// runObserver is what the runs read of the gateway besides its snapshot.
type runObserver struct{ s *Server }

// hostnameMatches is the match of a `Hostname` of the spec: an exact name, or `*.suffix`, which matches the
// subdomains of the suffix and not the suffix itself (the query log's filter by name is looser: it includes it).
func hostnameMatches(pattern, name string) bool {
	if rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSuffix(pattern, ".")), "*."); ok {
		return strings.HasSuffix(strings.ToLower(strings.TrimSuffix(name, ".")), "."+rest)
	}
	return nameMatches(pattern, name)
}

func (o runObserver) Flows(ctx context.Context) ([]engine.Flow, error) {
	return o.s.cfg.Engine.Flows(ctx)
}

func (o runObserver) DNSQuerySeen(devices []string, name string, since time.Time) bool {
	want := map[string]bool{}
	for _, d := range devices {
		want[strings.ToLower(d)] = true
	}
	o.s.dns.logMu.Lock()
	defer o.s.dns.logMu.Unlock()
	for i := len(o.s.dns.entries) - 1; i >= 0; i-- {
		q := o.s.dns.entries[i].e
		if q.Time.Before(since) {
			continue // the log is in the order of arrival, not of the time of the query: a slow query is logged after the ones that began later
		}
		if q.Device != nil && want[strings.ToLower(q.Device.String())] && hostnameMatches(name, q.Name) {
			return true
		}
	}
	return false
}

func (o runObserver) DNSQueries(devices []string, name string, since time.Time) []run.DNSQuery {
	want := map[string]bool{}
	for _, d := range devices {
		want[strings.ToLower(d)] = true
	}
	var out []run.DNSQuery
	o.s.dns.logMu.Lock()
	defer o.s.dns.logMu.Unlock()
	for i := len(o.s.dns.entries) - 1; i >= 0; i-- {
		q := o.s.dns.entries[i].e
		if q.Time.Before(since) {
			continue // the log is in the order of arrival, not of the time of the query: a slow query is logged after the ones that began later
		}
		if q.Device != nil && want[strings.ToLower(q.Device.String())] && hostnameMatches(name, q.Name) {
			out = append(out, run.DNSQuery{Device: strings.ToLower(q.Device.String()), Name: q.Name, Type: q.Type, Time: q.Time})
		}
	}
	slices.Reverse(out)
	return out
}

func (o runObserver) HostNames(client, addr netip.Addr) []string { return o.s.hostNames(client, addr) }

func (o runObserver) DNSClientSeen(device string) bool {
	o.s.dns.logMu.Lock()
	defer o.s.dns.logMu.Unlock()
	for _, q := range o.s.dns.entries {
		if q.e.Device != nil && strings.EqualFold(q.e.Device.String(), device) {
			return true
		}
	}
	return false
}

func (o runObserver) Counters(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]*model.Counter {
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	oc := o.s.readOverlayContext(ctx, true)
	out := map[uuid.UUID]*model.Counter{}
	if oc.countersFailed {
		return out
	}
	for _, ov := range oc.snap.Overlays {
		if !want[ov.Id] {
			continue
		}
		c := oc.overlayView(ov).Counters
		if c == nil && (ov.Kind == model.OverlayKindFault || ov.Kind == model.OverlayKindRule || ov.Kind == model.OverlayKindProfile) {
			// the counters were read and the overlay has none: it is not in the packet path (a rule that
			// is disabled, a fault another one overrides), so it matched nothing
			c = &model.Counter{}
		}
		if c != nil {
			out[ov.Id] = c
		}
	}
	return out
}

// runLimit is the owner a caller that may only touch its own runs is limited to, nil for no limit (as for
// overlays: the scope full and the admin may touch every run).
func runLimit(p *Principal) *model.Actor {
	if !limitedToOwn(p) {
		return nil
	}
	o := ownerOf(p)
	return &o
}

// ---- scenarios

func scenarioViews(cfg *model.Configuration) []model.ScenarioView {
	var out []model.ScenarioView
	for id, sc := range deref(cfg.Scenarios) {
		u, err := uuid.Parse(id)
		if err != nil {
			continue
		}
		out = append(out, model.ScenarioView{Id: u, Config: sc})
	}
	sort.Slice(out, func(i, j int) bool {
		return sortKey(out[i].Config.Name, out[i].Id.String()) < sortKey(out[j].Config.Name, out[j].Id.String())
	})
	return out
}

// ListScenarios implements GET /scenarios.
func (s *Server) ListScenarios(c *gin.Context, params model.ListScenariosParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	items, next, err := page(scenarioViews(v.cfg), func(sc model.ScenarioView) string {
		return sortKey(sc.Config.Name, sc.Id.String())
	}, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// GetScenario implements GET /scenarios/{scenarioId}: by UUID or name, as JSON or, with `Accept
// application/yaml`, as the YAML file `chaosctl run -f` takes.
func (s *Server) GetScenario(c *gin.Context, scenarioId model.Ref, params model.GetScenarioParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	for _, sc := range scenarioViews(v.cfg) {
		if !strings.EqualFold(sc.Id.String(), scenarioId) && !strings.EqualFold(sc.Config.Name, scenarioId) {
			continue
		}
		if wantsYAML(c) {
			raw, err := json.Marshal(sc.Config)
			if err != nil {
				s.fail(c, err)
				return
			}
			var doc any
			if err := json.Unmarshal(raw, &doc); err != nil {
				s.fail(c, err)
				return
			}
			out, err := yaml.Marshal(doc)
			if err != nil {
				s.fail(c, err)
				return
			}
			c.Data(200, "application/yaml", out)
			return
		}
		c.JSON(200, sc)
		return
	}
	s.write(c, notFound("scenario", scenarioId))
}

// wantsYAML reports whether the client asks for YAML before JSON.
func wantsYAML(c *gin.Context) bool {
	for _, part := range strings.Split(c.GetHeader("Accept"), ",") {
		switch strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) {
		case "application/yaml", "application/x-yaml", "text/yaml":
			return true
		case "application/json":
			return false
		}
	}
	return false
}

// ---- runs

// startRun makes the run a request asks for and answers with it.
func (s *Server) startRun(c *gin.Context, req run.CreateRequest, queue *model.Queue) {
	req.Queue = queue == nil || *queue
	p := principalOf(c)
	req.Owner = ownerOf(p)
	r, err := s.runs.Create(contextOf(c), req)
	if err != nil {
		s.failRun(c, err)
		return
	}
	s.record(c, "run.create", &audit.Object{Kind: "run", ID: r.Id.String(), Name: r.Scenario.Name}, 0, string(r.Status))
	c.Header("Location", "/api/v1/runs/"+r.Id.String())
	c.JSON(http.StatusCreated, r)
}

// failRun is fail for the problems of a run request: the list of validation errors is about the scenario and the
// request, not about a configuration.
func (s *Server) failRun(c *gin.Context, err error) {
	var ve domain.ValidationErrors
	if errors.As(err, &ve) {
		errs := validationErrors(ve)
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the run request or its scenario is not valid (%d problems)", len(errs)).
			with(func(b *model.Problem) { b.Errors = &errs }))
		return
	}
	s.fail(c, err)
}

func leaseOf(d *model.Duration) time.Duration {
	if d == nil {
		return 0
	}
	v, _ := time.ParseDuration(*d) // checked by the decoder
	return v
}

// CreateRun implements POST /runs: a stored scenario by `scenario`, or one sent with the request by `inline`.
func (s *Server) CreateRun(c *gin.Context, params model.CreateRunParams) {
	raw, rerr := readBody(c)
	if rerr != nil {
		s.write(c, rerr)
		return
	}
	req, err := domain.DecodeRunRequest(raw)
	if err != nil {
		s.failRun(c, err)
		return
	}
	cr := run.CreateRequest{Inline: req.Inline, Lease: leaseOf(req.Lease)}
	if req.Scenario != nil {
		cr.Scenario = *req.Scenario
	}
	if req.Parameters != nil {
		cr.Parameters = *req.Parameters
	}
	s.startRun(c, cr, params.Queue)
}

// RunScenario implements POST /scenarios/{scenarioId}/runs.
func (s *Server) RunScenario(c *gin.Context, scenarioId model.Ref, params model.RunScenarioParams) {
	raw, rerr := readBody(c)
	if rerr != nil {
		s.write(c, rerr)
		return
	}
	opts, err := domain.DecodeRunOptions(raw)
	if err != nil {
		s.failRun(c, err)
		return
	}
	cr := run.CreateRequest{Scenario: scenarioId, Lease: leaseOf(opts.Lease)}
	if opts.Parameters != nil {
		cr.Parameters = *opts.Parameters
	}
	s.startRun(c, cr, params.Queue)
}

// ListRuns implements GET /runs: newest first, without the timelines.
func (s *Server) ListRuns(c *gin.Context, params model.ListRunsParams) {
	f := run.Filter{}
	if params.Status != nil {
		f.Status = *params.Status
	}
	if params.Scenario != nil {
		f.Scenario = *params.Scenario
	}
	all := s.runs.List(f)
	// the order of the manager is the order of creation; the cursor is the key of the item before the page
	items, next, err := page(all, func(r model.Run) string { return runKey(r) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// runKey sorts newest first: the creation time counted down from a bound, then the id.
func runKey(r model.Run) string {
	return padInt(int64(1)<<62-r.CreatedAt.UnixNano()) + "/" + r.Id.String()
}

func padInt(n int64) string {
	s := itoa(n)
	return strings.Repeat("0", 20-len(s)) + s
}

// GetRun implements GET /runs/{runId}.
func (s *Server) GetRun(c *gin.Context, runId model.RunId) {
	r, err := s.runs.Get(runId)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, r)
}

// AbortRun implements POST /runs/{runId}/abort. It answers when the run has ended and its overlays are gone.
func (s *Server) AbortRun(c *gin.Context, runId model.RunId) {
	p := principalOf(c)
	res, err := s.runs.Abort(context.WithoutCancel(contextOf(c)), runId, runLimit(p))
	if err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "run.abort", &audit.Object{Kind: "run", ID: runId.String(), Name: res.Run.Scenario.Name}, 0, string(res.Run.Status))
	c.Header("Chaos-Generation", itoa(int64(res.Generation)))
	c.JSON(200, res.Run)
}

// RenewRun implements POST /runs/{runId}/renew, the heartbeat of a run with a lease.
func (s *Server) RenewRun(c *gin.Context, runId model.RunId) {
	r, err := s.runs.Renew(runId, runLimit(principalOf(c)))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, r)
}

// GetRunReportJson implements GET /runs/{runId}/report.json.
func (s *Server) GetRunReportJson(c *gin.Context, runId model.RunId) {
	rep, err := s.runs.Report(runId)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, rep)
}

// GetRunReportJunit implements GET /runs/{runId}/report.xml.
func (s *Server) GetRunReportJunit(c *gin.Context, runId model.RunId) {
	raw, err := s.runs.JUnit(runId)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.Data(200, "application/xml", raw)
}

// GetRunEvents implements GET /runs/{runId}/events: the events of the run as JSON lines.
func (s *Server) GetRunEvents(c *gin.Context, runId model.RunId) {
	lines, err := s.runs.Events(runId)
	if err != nil {
		s.fail(c, err)
		return
	}
	var b strings.Builder
	for _, l := range lines {
		b.Write(l)
		b.WriteByte('\n')
	}
	c.Data(200, "application/jsonl", []byte(b.String()))
}

// GetBusy implements GET /system/busy: whether runs are queued or running, for a script that updates the
// gateway.
func (s *Server) GetBusy(c *gin.Context) {
	running, queued, ids := s.runs.Busy()
	body := model.Busy{Busy: running+queued > 0, RunsActive: &running, RunsQueued: &queued}
	if len(ids) > 0 {
		body.RunIds = ptr(toUuids(ids))
	}
	c.JSON(200, body)
}

func toUuids(ids []uuid.UUID) []openapi_types.UUID {
	out := make([]openapi_types.UUID, len(ids))
	copy(out, ids)
	return out
}

// ---- revisions

// runReferences are the queued and running runs that applying a candidate would orphan.
func (s *Server) runReferences(candidate *model.Configuration) []run.Reference {
	snap := s.cfg.Engine.Snapshot()
	if snap.Config == nil || candidate == nil {
		return nil
	}
	var found []string
	for _, d := range snap.Devices {
		if d.Origin == model.DeviceOriginDiscovered {
			found = append(found, d.ID)
		}
	}
	return s.runs.Orphaned(snap.Config, candidate, found...)
}

// runBlockingReferences are the runs as the spec's BlockingReference.
func runBlockingReferences(refs []run.Reference) []model.BlockingReference {
	out := make([]model.BlockingReference, 0, len(refs))
	for _, r := range refs {
		owner := r.Run.Owner
		out = append(out, model.BlockingReference{Kind: model.BlockingReferenceKindRun, Id: r.Run.Id, Owner: &owner, Object: r.Object})
	}
	return out
}
