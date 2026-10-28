package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/calibrate"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/probe"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// Probes and calibration (plan §2.12, M25). A probe is a virtual client in a network namespace of the gateway, a port of its
// network's bridge; the executor makes it and runs its DHCP client (internal/probe), the engine observes its address like a
// device's. The API lists the probes, runs a diagnostic from one (the executor runs it in the probe's namespace: the API
// has no right to enter one) and calibrates one: the self-test "measure this profile".

// probeUnavailable is the answer for a probe the host does not have yet or that has no address yet.
func probeUnavailable(name, why string) *problem {
	return newProblem(model.ErrorCodeUnavailable, "the probe %q %s", name, why)
}

// activeProbe resolves a reference to a probe of the active configuration.
type activeProbe struct {
	id, name string
	cfg      model.Probe
	network  string
	ns       string
	bridge   *compiler.Bridge
}

func (s *Server) findProbe(ref string) (activeProbe, *problem) {
	snap := s.cfg.Engine.Snapshot()
	if snap.Config == nil {
		return activeProbe{}, notFound("probe", ref)
	}
	idx, _ := domain.BuildIndex(snap.Config)
	id, ok := idx.Resolve(domain.KindDevice, ref)
	d := idx.Devices[id]
	if !ok || d == nil || d.Origin != domain.OriginProbe {
		return activeProbe{}, notFound("probe", ref)
	}
	p := activeProbe{id: id, name: d.Name, cfg: *d.Probe, network: d.Network, ns: executor.ProbeNSPrefix + compiler.ProbeShort(id)}
	for i := range snap.Bridges {
		if snap.Bridges[i].NetworkID == d.Network {
			p.bridge = &snap.Bridges[i]
		}
	}
	return p, nil
}

// readyProbe is the executor's view of the probe, which must exist and hold an address.
func (s *Server) readyProbe(ctx context.Context, p activeProbe) (executor.ProbeNSState, *problem) {
	var states []executor.ProbeNSState
	if _, err := readInto(ctx, s.cfg.Exec, &executor.Read{Target: executor.Target{NS: s.cfg.Namespace}, What: executor.ReadProbes}, &states); err != nil {
		return executor.ProbeNSState{}, newProblem(model.ErrorCodeUnavailable, "cannot read the probes: %v", err)
	}
	for _, st := range states {
		if st.Name != p.ns {
			continue
		}
		if len(st.Addrs) == 0 {
			return st, probeUnavailable(p.name, "has no address yet: it gets one by DHCP from its network, which needs DHCP switched on")
		}
		return st, nil
	}
	return executor.ProbeNSState{}, probeUnavailable(p.name, "is not built yet: apply the revision that has it")
}

// readInto runs one Read and decodes its data.
func readInto(ctx context.Context, ex interface {
	Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error)
}, r *executor.Read, v any) (executor.Outcome, error) {
	out, err := ex.Do(ctx, r)
	if err != nil {
		return out, err
	}
	if len(out.Data) != 1 {
		return out, errors.New("the executor returned no data")
	}
	return out, json.Unmarshal(out.Data[0], v)
}

// ListProbes implements GET /probes: the probes of the revision with what the gateway observes of them.
func (s *Server) ListProbes(c *gin.Context, params model.ListProbesParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	type probeView struct {
		ID       string         `json:"id"`
		Config   model.Probe    `json:"config"`
		Observed deviceObserved `json:"observed"`
	}
	var all []probeView
	for _, d := range s.deviceViews(v.cfg, false) {
		if d.Origin != string(domain.OriginProbe) {
			continue
		}
		p := (*v.cfg.Probes)[d.ID]
		all = append(all, probeView{ID: d.ID, Config: p, Observed: d.Observed})
	}
	items, next, err := page(all, func(p probeView) string { return sortKey(p.Config.Name, p.ID) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// runProbeDiagnostic runs a diagnostic from a probe: the executor runs it in the probe's namespace.
func (s *Server) runProbeDiagnostic(ctx context.Context, req *model.DiagnosticRequest, ref string) (model.DiagnosticResult, *problem) {
	p, perr := s.findProbe(ref)
	if perr != nil {
		return model.DiagnosticResult{}, perr
	}
	if !s.diagnosing.start(p.id) {
		return model.DiagnosticResult{}, newProblem(model.ErrorCodeTargetBusy, "a diagnostic from the probe %q is running already", p.name)
	}
	defer s.diagnosing.end(p.id)
	if _, perr := s.readyProbe(ctx, p); perr != nil {
		return model.DiagnosticResult{}, perr
	}
	op := &executor.ProbeRun{Target: executor.Target{NS: s.cfg.Namespace}, Probe: p.ns, Kind: executor.ProbeDiagnostic, UserAgent: "chaos-gateway/" + s.cfg.Version}
	cp := *req
	cp.From = nil
	op.Request = &cp
	if p.bridge != nil {
		// a device asks the gateway of its network (the DHCP lease of the probe names the servers when it has any: the executor
		// prefers them)
		gw := netip.AddrPortFrom(p.bridge.Address.Addr(), 53).String()
		op.Servers, op.Proxy = []string{gw}, gw
	}
	if p.cfg.TrustsTestCa != nil && *p.cfg.TrustsTestCa && s.cfg.TLSCADir != "" {
		if pem, err := tlsresponder.TestCAPEM(s.cfg.TLSCADir); err == nil {
			op.RootsPEM = string(pem)
		}
	}
	out, err := s.cfg.Exec.Do(ctx, op)
	if err != nil {
		return model.DiagnosticResult{}, s.problemFor(err)
	}
	var res model.DiagnosticResult
	if len(out.Data) != 1 || json.Unmarshal(out.Data[0], &res) != nil {
		return model.DiagnosticResult{}, newProblem(model.ErrorCodeInternal, "the executor returned no result")
	}
	return res, nil
}

// calibrations are the probes whose calibration runs; a probe is calibrated by one caller at a time.
type calibrations struct {
	mu      sync.Mutex
	running map[string]bool
}

func (c *calibrations) start(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running[id] {
		return false
	}
	if c.running == nil {
		c.running = map[string]bool{}
	}
	c.running[id] = true
	return true
}

func (c *calibrations) end(id string) {
	c.mu.Lock()
	delete(c.running, id)
	c.mu.Unlock()
}

// The datagrams of a calibration: one every 10 ms (2000 in the 20 s the loss statistic needs), at most 20000 in a run.
const (
	calibrationInterval = 10 * time.Millisecond
	calibrationBaseline = 100 // datagrams of the unimpaired reference, 10 ms apart
	calibrationMaxEcho  = executor.MaxProbeEchoCount
	calibrationSettle   = 3 * time.Second
	// calibrationMaxSettle is the longest wait for the last answers (the executor takes at most a minute).
	calibrationMaxSettle = time.Minute
	// calibrationSlack is the time the call may take beyond its duration: the overlay's apply, the reference and the settle times.
	calibrationSlack = 2 * time.Minute
)

// settleFor is how long the echo run waits for the answers after the last datagram: three seconds, or longer when the
// parameters delay a round trip by more (the last answers are on their way for the round trip and its jitter, and a
// datagram that is still on the way is no lost one).
func settleFor(exp calibrate.Expectation) time.Duration {
	wait := time.Duration((2*(exp.AddedRTTMs+exp.JitterMs) + 1000) * float64(time.Millisecond))
	return min(max(calibrationSettle, wait), calibrationMaxSettle)
}

// CalibrateProbe implements POST /probes/{probeId}/calibrate: the self-test of plan §2.12.
func (s *Server) CalibrateProbe(c *gin.Context, probeID model.Ref) {
	raw, rerr := readBody(c)
	if rerr != nil {
		s.write(c, rerr)
		return
	}
	req, err := domain.DecodeCalibrationRequest(raw)
	if err != nil {
		s.fail(c, err)
		return
	}
	p, perr := s.findProbe(probeID)
	if perr != nil {
		s.write(c, perr)
		return
	}
	snap := s.cfg.Engine.Snapshot()
	var profile *model.Profile
	var profileID string
	if req.Profile != nil {
		id, pr, _, found := domain.ProfileRef(snap.Config, *req.Profile)
		if !found {
			s.write(c, notFound("profile", *req.Profile))
			return
		}
		if pr.Parts.Impairment == nil {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "the profile %q has no impairment part: there is nothing to measure (the calibration measures delay, loss and rate)", pr.Name))
			return
		}
		profile, profileID = &pr, id
	}
	if !s.calibrating.start(p.id) {
		s.write(c, newProblem(model.ErrorCodeTargetBusy, "a calibration of the probe %q is running already", p.name))
		return
	}
	defer s.calibrating.end(p.id)

	duration := domain.CalibrationDuration(req)
	// the call is the caller's to wait for, but the overlay it makes is removed whatever happens
	ctx, cancel := context.WithTimeout(contextOf(c), duration+calibrationSlack)
	defer cancel()
	if _, perr := s.readyProbe(ctx, p); perr != nil {
		s.write(c, perr)
		return
	}
	host, port := req.Host, domain.CalibrationPort(req)
	ns := executor.Target{NS: s.cfg.Namespace}

	// the unimpaired reference: the gateway's own traffic to the same server meets no fault of a device
	ref, err := s.echo(ctx, &executor.ProbeRun{Target: ns, Kind: executor.ProbeEcho, Host: host, Port: port, Count: calibrationBaseline,
		IntervalUs: int(calibrationInterval / time.Microsecond), SettleMs: 1000})
	if err == nil {
		err = completed(ctx, ref, calibrationBaseline)
	}
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "the gateway cannot reach the echo server %s:%d: %v", host, port, err))
		return
	}

	// what to expect, and the overlay that makes it so
	var imp *model.ImpairmentParams
	var note string
	switch {
	case profile != nil:
		imp = profile.Parts.Impairment
	case req.Fault != nil:
		imp = req.Fault
	default:
		imp, note = s.currentImpairment(p, host, port)
	}
	exp, err := calibrate.ExpectFor(imp, probe.WireBytes(probe.DefaultEchoSize))
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the parameters cannot be measured: %v", err))
		return
	}
	if profile != nil || req.Fault != nil {
		id, perr := s.calibrationOverlay(c, p, profileID, req, duration)
		if perr != nil {
			s.write(c, perr)
			return
		}
		defer func() {
			// removed even when the caller has gone: a profile left on a probe would impair it for good (its time to live
			// ends it otherwise, but later)
			if _, err := s.cfg.Engine.DeleteOverlay(context.WithoutCancel(contextOf(c)), id, nil, actorOf(principalOf(c))); err != nil {
				s.log.Warn("cannot remove the overlay of a calibration: it ends with its time to live", "probe", p.name, "overlay", id, "error", err)
			}
		}()
	}

	// the probe's run: the echo, and with a rate limit the throughput of a TCP echo for a third of the duration
	echoFor := duration
	var rateFor time.Duration
	if exp.RateBps > 0 {
		rateFor = max(duration/3, 2*time.Second)
		echoFor = duration - rateFor
	}
	count := int(echoFor / calibrationInterval)
	interval := calibrationInterval
	if count > calibrationMaxEcho {
		count, interval = calibrationMaxEcho, echoFor/calibrationMaxEcho
	}
	m := calibrate.Measurement{BaselineUs: ref.RTTUs}
	run, err := s.echo(ctx, &executor.ProbeRun{Target: ns, Probe: p.ns, Kind: executor.ProbeEcho, Host: host, Port: port, Count: count,
		IntervalUs: int(interval / time.Microsecond), SettleMs: int(settleFor(exp) / time.Millisecond)})
	if err == nil {
		err = completed(ctx, run, count)
	}
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "the probe cannot reach the echo server %s:%d: %v", host, port, err))
		s.recordAborted(c, p, profile, req, err)
		return
	}
	m.Sent, m.Received, m.RTTUs = run.Sent, run.Received, run.RTTUs
	if rateFor > 0 {
		var rr struct {
			RateBps int64 `json:"rate_bps"`
		}
		if _, err := readRun(ctx, s.cfg.Exec, &executor.ProbeRun{Target: ns, Probe: p.ns, Kind: executor.ProbeRate, Host: host, Port: port, DurationMs: int(rateFor / time.Millisecond)}, &rr); err == nil && ctx.Err() == nil {
			m.RateBps, m.RateMeasured = rr.RateBps, true
		} else if ctx.Err() != nil {
			// the caller is gone or the time is up: what was measured so far is no verdict
			s.write(c, newProblem(model.ErrorCodeUnavailable, "the calibration was cancelled: %v", ctx.Err()))
			s.recordAborted(c, p, profile, req, ctx.Err())
			return
		} else {
			note = strings.TrimSpace(note + " The throughput could not be measured: " + err.Error() + ".")
		}
	}
	res := calibrate.Evaluate(exp, m)
	if note != "" {
		d := ""
		if res.Detail != nil {
			d = *res.Detail
		}
		d = strings.TrimSpace(d + " " + note)
		res.Detail = &d
	}
	s.record(c, "probe.calibrate", &audit.Object{Kind: "probe", ID: p.id}, 0, fmt.Sprintf("%s: %s, %s", p.name, calibrationWhat(profile, req), map[bool]string{true: "within tolerance", false: "out of tolerance"}[res.WithinTolerance]))
	c.JSON(200, res)
}

func calibrationWhat(profile *model.Profile, req *model.CalibrationRequest) string {
	switch {
	case profile != nil:
		return "profile " + profile.Name
	case req.Fault != nil:
		return "fault parameters"
	}
	return "current faults"
}

// recordAborted writes the audit entry of a calibration that ended without a verdict.
func (s *Server) recordAborted(c *gin.Context, p activeProbe, profile *model.Profile, req *model.CalibrationRequest, why error) {
	s.record(c, "probe.calibrate", &audit.Object{Kind: "probe", ID: p.id}, 0, fmt.Sprintf("%s: %s, no verdict: %v", p.name, calibrationWhat(profile, req), why))
}

// completed says whether an echo run is whole: a run that the caller's cancellation or a failing socket cut short (the
// executor answers with what it has) is no measurement to judge.
func completed(ctx context.Context, r echoResult, count int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("the run was cancelled: %w", err)
	}
	if r.Sent != count {
		return fmt.Errorf("the run was cut short after %d of %d datagrams", r.Sent, count)
	}
	return nil
}

// echo runs an echo run and decodes its result.
func (s *Server) echo(ctx context.Context, op *executor.ProbeRun) (echoResult, error) {
	var r echoResult
	_, err := readRun(ctx, s.cfg.Exec, op, &r)
	return r, err
}

type echoResult struct {
	Sent     int     `json:"sent"`
	Received int     `json:"received"`
	RTTUs    []int64 `json:"rtt_us"`
}

func readRun(ctx context.Context, ex interface {
	Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error)
}, op *executor.ProbeRun, v any) (executor.Outcome, error) {
	out, err := ex.Do(ctx, op)
	if err != nil {
		return out, err
	}
	if len(out.Data) != 1 {
		return out, errors.New("the executor returned no result")
	}
	return out, json.Unmarshal(out.Data[0], v)
}

// calibrationOverlay activates the profile or the fault on the probe for the measurement. The overlay carries a TTL, so that
// one that outlives this process is removed anyway.
func (s *Server) calibrationOverlay(c *gin.Context, p activeProbe, profileID string, req *model.CalibrationRequest, duration time.Duration) (uuid.UUID, *problem) {
	ow := model.OverlayRequest{Target: &model.Scope{Device: &p.id}}
	ttl := (duration + calibrationSlack + time.Minute).Round(time.Second).String()
	ow.Ttl = &ttl
	if profileID != "" {
		ow.Profile = &profileID
	} else {
		var fb model.FaultBody
		b, _ := json.Marshal(req.Fault)
		if err := json.Unmarshal(b, &fb); err != nil {
			return uuid.Nil, newProblem(model.ErrorCodeValidationFailed, "the fault parameters cannot be activated: %v", err)
		}
		ow.Fault = &fb
	}
	pr := principalOf(c)
	res, err := s.cfg.Engine.PutOverlay(context.WithoutCancel(contextOf(c)), engine.OverlayWrite{Owner: ownerOf(pr), Request: ow, Actor: actorOf(pr)})
	if err != nil {
		return uuid.Nil, s.problemFor(err)
	}
	return res.Overlay.Id, nil
}

// currentImpairment is the impairment that wins for the probe's traffic to the echo server now, nil when none does.
func (s *Server) currentImpairment(p activeProbe, host string, port int) (*model.ImpairmentParams, string) {
	snap := s.cfg.Engine.Snapshot()
	world, err := domain.NewWorld(snap.Config, snap.Overlays)
	if err != nil {
		return nil, ""
	}
	sub := domain.Subject{Device: strings.ToLower(p.id), Network: strings.ToLower(p.network)}
	if as := snap.Identity.Addresses[sub.Device]; len(as) > 0 {
		sub.IP = as[0]
	}
	dst, _ := netip.ParseAddr(host)
	for _, r := range world.Resolve(domain.Query{Source: sub, DestIP: dst, Protocol: "udp", Port: port}) {
		if r.Family != "impairment" || r.Winner == nil || r.Winner.Impairment == nil {
			continue
		}
		var imp model.ImpairmentParams
		b, _ := json.Marshal(r.Winner.Impairment)
		if json.Unmarshal(b, &imp) != nil {
			continue
		}
		return &imp, "Measured against the fault that wins for the probe's traffic to the echo server now."
	}
	return nil, "No fault impairs the probe's traffic to the echo server now: the expectation is the unimpaired path."
}
