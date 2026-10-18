package run_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/goleak"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	idESP  = "1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a" // device esp32-42
	idLab  = "2e3f4a5b-6c7d-4e8f-9a0b-1c2d3e4f5a6b" // device lab-host
	idIoT  = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21" // network IoT
	idHub  = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54" // network lab-hub
	idRA   = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d" // WireGuard client lab-rA
	idSens = "3f4a5b6c-7d8e-4f9a-0b1c-2d3e4f5a6b7c" // group sensors
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func testConfig(t *testing.T) *model.Configuration {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", "configuration.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	norm, errs := domain.Normalize(cfg)
	if len(errs) != 0 {
		t.Fatalf("normalize: %v", errs)
	}
	return norm
}

// fakeGateway is the engine as the runs see it: overlays in a map by their key, a snapshot the test
// edits, and an event bus.
type fakeGateway struct {
	mu   sync.Mutex
	snap *engine.Snapshot
	byID map[uuid.UUID]model.Overlay
	key  map[string]uuid.UUID
	gen  uint64
	// log is what the runs did, in order: "put <step>", "replace <step>", "delete <step>", "reset".
	log []string
	// writes are the overlay writes as the runs made them
	writes []engine.OverlayWrite
	// queueWait is what a write reports; putErr and resetErr fail a write or a reset
	queueWait time.Duration
	putErr    func(w engine.OverlayWrite) error
	resetErr  error
	// block, when set, holds every write until it is closed or the context ends
	block chan struct{}
	seq   uint64
	subs  map[chan engine.Event]struct{}
	// foreign are overlays of other owners that the snapshot shows besides the runs'
	foreign []model.Overlay
}

func newGateway(cfg *model.Configuration) *fakeGateway {
	g := &fakeGateway{byID: map[uuid.UUID]model.Overlay{}, key: map[string]uuid.UUID{}, subs: map[chan engine.Event]struct{}{}}
	devices := []engine.DeviceState{
		{ID: idESP, Name: "esp32-42", Origin: model.DeviceOriginConfigured, Network: idIoT, Online: true},
		{ID: idLab, Name: "lab-host", Origin: model.DeviceOriginConfigured, Network: idHub, Online: true},
		{ID: idRA, Name: "lab-rA", Origin: model.DeviceOriginWireguardClient, Network: idHub, Online: true},
	}
	g.snap = &engine.Snapshot{Config: cfg, Revision: 3, Generation: 10, Devices: devices}
	return g
}

func (g *fakeGateway) Snapshot() *engine.Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snap
}

// edit replaces the snapshot with a changed copy.
func (g *fakeGateway) edit(f func(s *engine.Snapshot)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := *g.snap
	c.Devices = append([]engine.DeviceState(nil), g.snap.Devices...)
	c.Overlays = append([]model.Overlay(nil), g.snap.Overlays...)
	f(&c)
	g.snap = &c
}

func (g *fakeGateway) setOnline(id string, online bool) {
	g.edit(func(s *engine.Snapshot) {
		for i := range s.Devices {
			if s.Devices[i].ID == id {
				s.Devices[i].Online = online
			}
		}
	})
}

func (g *fakeGateway) setForeign(ovs ...model.Overlay) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.foreign = ovs
	g.publishOverlays()
}

func (g *fakeGateway) publishOverlays() {
	list := append([]model.Overlay(nil), g.foreign...)
	for _, ov := range g.byID {
		list = append(list, ov)
	}
	c := *g.snap
	c.Overlays = list
	g.snap = &c
}

func (g *fakeGateway) PutOverlay(ctx context.Context, w engine.OverlayWrite) (engine.OverlayResult, error) {
	g.mu.Lock()
	block, putErr := g.block, g.putErr
	g.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return engine.OverlayResult{}, ctx.Err()
		}
	}
	if putErr != nil {
		if err := putErr(w); err != nil {
			return engine.OverlayResult{}, err
		}
	}
	key, err := domain.OverlayKey(w.Owner, &w.Request)
	if err != nil {
		return engine.OverlayResult{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gen++
	id, replaced := g.key[key]
	if !replaced {
		id = uuid.New()
	}
	kind, _ := domain.OverlayKindOf(&w.Request)
	ov := model.Overlay{Id: id, Kind: model.OverlayKind(kind), Owner: w.Owner, Target: w.Request.Target, Profile: w.Request.Profile,
		Fault: w.Request.Fault, Rule: w.Request.Rule, Wireguard: w.Request.Wireguard, Run: w.Run, Generation: int64(g.gen)}
	g.byID[id], g.key[key] = ov, id
	verb := "put"
	if replaced {
		verb = "replace"
	}
	g.log = append(g.log, verb+" "+w.Run.Step)
	g.writes = append(g.writes, w)
	g.publishOverlays()
	return engine.OverlayResult{Overlay: ov, Created: !replaced, Generation: g.gen, QueueWait: g.queueWait}, nil
}

func (g *fakeGateway) DeleteOverlay(_ context.Context, id uuid.UUID, by *model.Owner, _ model.Actor) (engine.OverlayResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ov, ok := g.byID[id]
	if !ok {
		return engine.OverlayResult{}, engine.ErrOverlayNotFound
	}
	if by != nil && (by.Type != ov.Owner.Type || by.Id != ov.Owner.Id) {
		return engine.OverlayResult{}, engine.ErrOverlayForbidden
	}
	g.gen++
	delete(g.byID, id)
	for k, v := range g.key {
		if v == id {
			delete(g.key, k)
		}
	}
	step := ""
	if ov.Run != nil {
		step = ov.Run.Step
	}
	g.log = append(g.log, "delete "+step)
	g.publishOverlays()
	return engine.OverlayResult{Overlay: ov, Generation: g.gen, QueueWait: g.queueWait}, nil
}

func (g *fakeGateway) ResetOverlays(_ context.Context, owner *model.Owner, _ model.Actor) (engine.OverlayResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.resetErr != nil {
		return engine.OverlayResult{}, g.resetErr
	}
	n := 0
	for id, ov := range g.byID {
		if owner == nil || (owner.Type == ov.Owner.Type && owner.Id == ov.Owner.Id) {
			delete(g.byID, id)
			for k, v := range g.key {
				if v == id {
					delete(g.key, k)
				}
			}
			n++
		}
	}
	g.gen++
	g.log = append(g.log, "reset")
	g.publishOverlays()
	return engine.OverlayResult{Removed: n, Generation: g.gen, QueueWait: g.queueWait}, nil
}

func (g *fakeGateway) Emit(typ string, data map[string]any) {
	g.mu.Lock()
	g.seq++
	ev := engine.Event{Seq: g.seq, Time: t0, Type: typ, Data: data}
	var subs []chan engine.Event
	for c := range g.subs {
		subs = append(subs, c)
	}
	g.mu.Unlock()
	for _, c := range subs {
		select {
		case c <- ev:
		default:
		}
	}
}

func (g *fakeGateway) Subscribe() (<-chan engine.Event, func()) {
	c := make(chan engine.Event, 1024)
	g.mu.Lock()
	g.subs[c] = struct{}{}
	g.mu.Unlock()
	return c, func() {
		g.mu.Lock()
		delete(g.subs, c)
		g.mu.Unlock()
	}
}

func (g *fakeGateway) overlays() []model.Overlay {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []model.Overlay
	for _, ov := range g.byID {
		out = append(out, ov)
	}
	return out
}

func (g *fakeGateway) logged() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.log...)
}

// fakeObserver is what the gateway sees besides the snapshot.
type fakeObserver struct {
	mu       sync.Mutex
	flows    []engine.Flow
	dns      map[string]bool // device -> asked the gateway's DNS
	queries  []query
	counters map[uuid.UUID]*model.Counter
	// gw and stepCounters give the counter of the overlay that a step wrote, whatever its id
	gw           *fakeGateway
	stepCounters map[string]*model.Counter
	flowErr      error
	flowPolls    int
}

type query struct {
	device, name string
	at           time.Time
}

func (o *fakeObserver) Flows(context.Context) ([]engine.Flow, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flowPolls++
	if o.flowErr != nil {
		return nil, o.flowErr
	}
	return append([]engine.Flow(nil), o.flows...), nil
}

func (o *fakeObserver) addFlow(f engine.Flow) {
	o.mu.Lock()
	o.flows = append(o.flows, f)
	o.mu.Unlock()
}

func (o *fakeObserver) DNSQuerySeen(devices []string, name string, since time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, q := range o.queries {
		for _, d := range devices {
			if q.device == d && q.name == name && !q.at.Before(since) {
				return true
			}
		}
	}
	return false
}

func (o *fakeObserver) DNSQueries(devices []string, name string, since time.Time) []run.DNSQuery {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []run.DNSQuery
	for _, q := range o.queries {
		for _, d := range devices {
			if q.device == d && q.name == name && !q.at.Before(since) {
				out = append(out, run.DNSQuery{Device: q.device, Name: q.name, Type: "A", Time: q.at})
			}
		}
	}
	return out
}

func (o *fakeObserver) DNSClientSeen(device string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.dns[device]
}

func (o *fakeObserver) Counters(_ context.Context, ids []uuid.UUID) map[uuid.UUID]*model.Counter {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := map[uuid.UUID]*model.Counter{}
	for _, id := range ids {
		if c, ok := o.counters[id]; ok {
			cc := *c
			out[id] = &cc
			continue
		}
		if o.gw == nil {
			continue
		}
		o.gw.mu.Lock()
		ov, ok := o.gw.byID[id]
		o.gw.mu.Unlock()
		if !ok || ov.Run == nil {
			continue
		}
		if c, ok := o.stepCounters[ov.Run.Step]; ok {
			cc := *c
			out[id] = &cc
		}
	}
	return out
}

// harness is a manager on a fake clock, a fake gateway and a directory of its own.
type harness struct {
	t   *testing.T
	clk *clock.Fake
	gw  *fakeGateway
	obs *fakeObserver
	m   *run.Manager
	dir string
}

type option func(*run.Config)

func newHarness(t *testing.T, opts ...option) *harness {
	t.Helper()
	return newHarnessIn(t, t.TempDir(), opts...)
}

func newHarnessIn(t *testing.T, dir string, opts ...option) *harness {
	t.Helper()
	clk := clock.NewFake(t0)
	gw := newGateway(testConfig(t))
	obs := &fakeObserver{dns: map[string]bool{}, counters: map[uuid.UUID]*model.Counter{}, stepCounters: map[string]*model.Counter{}, gw: gw}
	cfg := run.Config{Clock: clk, Dir: dir, Gateway: gw, Observer: obs}
	for _, o := range opts {
		o(&cfg)
	}
	m, err := run.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, clk: clk, gw: gw, obs: obs, m: m, dir: dir}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m.Close(ctx)
	})
	return h
}

// eventually polls until the condition holds. The runs execute in goroutines of their own; the tests keep
// time with the fake clock and use real time only to let those goroutines catch up.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

var token = model.Actor{Type: model.ActorTypeToken, Id: "tok-1", Name: ptr("ci")}

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T) T {
	var z T
	if p != nil {
		z = *p
	}
	return z
}

// inline parses a scenario given as YAML.
func inline(t *testing.T, yaml string) *model.Scenario {
	t.Helper()
	sc, err := domain.DecodeScenario([]byte(yaml), domain.FormatYAML)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	return sc
}

// start makes a run of an inline scenario for the default token.
func (h *harness) start(yaml string, mod ...func(*run.CreateRequest)) model.Run {
	h.t.Helper()
	req := run.CreateRequest{Inline: inline(h.t, yaml), Queue: true, Owner: token}
	for _, f := range mod {
		f(&req)
	}
	r, err := h.m.Create(context.Background(), req)
	if err != nil {
		h.t.Fatalf("create: %v", err)
	}
	return r
}

func (h *harness) get(id uuid.UUID) model.Run {
	h.t.Helper()
	r, err := h.m.Get(id)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) waitStatus(id uuid.UUID, want ...model.RunStatus) model.Run {
	h.t.Helper()
	var r model.Run
	eventually(h.t, "run status "+string(want[0]), func() bool {
		r = h.get(id)
		for _, w := range want {
			if r.Status == w {
				return true
			}
		}
		return false
	})
	return r
}

func (h *harness) stepStatus(id uuid.UUID, step string) model.RunStepStatus {
	for _, s := range *h.get(id).Steps {
		if s.Id == step {
			return s.Status
		}
	}
	h.t.Fatalf("no step %s", step)
	return ""
}

func (h *harness) waitStep(id uuid.UUID, step string, want model.RunStepStatus) {
	h.t.Helper()
	eventually(h.t, "step "+step+" "+string(want), func() bool { return h.stepStatus(id, step) == want })
}

// advance moves the fake clock once the run has armed the timer for its next step.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	h.clk.BlockUntil(1)
	h.clk.Advance(d)
}

func step(r model.Run, id string) model.RunStep {
	for _, s := range *r.Steps {
		if s.Id == id {
			return s
		}
	}
	panic("no step " + id)
}

func contains(s []string, want string) bool {
	for _, x := range s {
		if x == want {
			return true
		}
	}
	return false
}

func has(s, sub string) bool { return strings.Contains(s, sub) }

func errAs[T error](err error) (T, bool) {
	var t T
	ok := errors.As(err, &t)
	return t, ok
}

func (g *fakeGateway) written() []engine.OverlayWrite {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]engine.OverlayWrite(nil), g.writes...)
}

// failStep makes every overlay write fail with the message.
func failStep(msg string) func(engine.OverlayWrite) error {
	return func(engine.OverlayWrite) error { return errors.New(msg) }
}
