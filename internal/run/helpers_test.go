package run_test

import (
	"context"
	"errors"
	"math"
	"net/netip"
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
	// putCalls counts the writes that came in, whatever became of them
	putCalls int
	// putDelay, when set, holds a write for as long as it returns for the step on the fake clock (an apply that takes its time)
	putDelay func(step string) time.Duration
	clk      clock.Clock
	// resetFailures is how many of the next resets fail with errTransient before they work
	resetFailures int
	// block, when set, holds every write until it is closed or the context ends; resetBlock does the same for the
	// resets (the `restore` steps and the end of a run)
	block      chan struct{}
	resetBlock chan struct{}
	seq        uint64
	subs       map[chan engine.Event]struct{}
	// events are the events published, for the replay (the newest replayMax when that is set); subBuffer is the buffer of
	// a subscriber (1024 when zero)
	events    []engine.Event
	replayMax int
	subBuffer int
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
	g.putCalls++
	block, putErr, delay := g.block, g.putErr, g.putDelay
	g.mu.Unlock()
	if delay != nil {
		if d := delay(w.Run.Step); d > 0 {
			select {
			case <-g.clk.After(d):
			case <-ctx.Done():
				return engine.OverlayResult{}, ctx.Err()
			}
		}
	}
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
		Fault: w.Request.Fault, Rule: w.Request.Rule, Dns: w.Request.Dns, Wireguard: w.Request.Wireguard, Run: w.Run, Generation: int64(g.gen)}
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

func (g *fakeGateway) ResetOverlays(ctx context.Context, owner *model.Owner, _ model.Actor) (engine.OverlayResult, error) {
	g.mu.Lock()
	hold := g.resetBlock
	g.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return engine.OverlayResult{}, ctx.Err()
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.resetErr != nil {
		return engine.OverlayResult{}, g.resetErr
	}
	if g.resetFailures > 0 {
		g.resetFailures--
		return engine.OverlayResult{}, errTransient
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

// Emit publishes an event as the bus does: it is kept for replay (the last replayMax of them, when that is set), and a
// subscriber whose buffer is full is dropped and its channel closed.
func (g *fakeGateway) Emit(typ string, data map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	ev := engine.Event{Seq: g.seq, Time: t0, Type: typ, Data: data}
	g.events = append(g.events, ev)
	if g.replayMax > 0 && len(g.events) > g.replayMax {
		g.events = g.events[len(g.events)-g.replayMax:]
	}
	for c := range g.subs {
		select {
		case c <- ev:
		default:
			delete(g.subs, c)
			close(c)
		}
	}
}

func (g *fakeGateway) Subscribe() (<-chan engine.Event, func()) {
	_, c, unsub := g.SubscribeFrom(math.MaxUint64)
	return c, unsub
}

func (g *fakeGateway) SubscribeFrom(last uint64) ([]engine.Event, <-chan engine.Event, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	size := g.subBuffer
	if size == 0 {
		size = 1024
	}
	c := make(chan engine.Event, size)
	var replay []engine.Event
	for _, ev := range g.events {
		if ev.Seq > last {
			replay = append(replay, ev)
		}
	}
	g.subs[c] = struct{}{}
	return replay, c, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if _, ok := g.subs[c]; ok {
			delete(g.subs, c)
			close(c)
		}
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
	// answered are the names the gateway's DNS answered a client for an address ("client|addr" -> names)
	answered  map[string][]string
	flowErr   error
	flowPolls int
	// block, when set, holds every read of the connections until it is closed or the read is cancelled
	block chan struct{}
	// readDelay holds every read of the counters for that long on the fake clock (a slow executor)
	readDelay time.Duration
	clk       clock.Clock
}

type query struct {
	device, name string
	at           time.Time
}

func (o *fakeObserver) Flows(ctx context.Context) ([]engine.Flow, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flowPolls++
	if o.block != nil {
		block := o.block
		o.mu.Unlock()
		select {
		case <-block:
		case <-ctx.Done():
			o.mu.Lock()
			return nil, ctx.Err()
		}
		o.mu.Lock()
	}
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

// answer records that the gateway's DNS answered the client the address for the names.
func (o *fakeObserver) answer(client, addr string, names ...string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.answered == nil {
		o.answered = map[string][]string{}
	}
	o.answered[client+"|"+addr] = append(o.answered[client+"|"+addr], names...)
}

func (o *fakeObserver) HostNames(client, addr netip.Addr) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.answered[client.String()+"|"+addr.String()]...)
}

func (o *fakeObserver) DNSClientSeen(device string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.dns[device]
}

func (o *fakeObserver) Counters(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]*model.Counter {
	o.mu.Lock()
	delay := o.readDelay
	o.mu.Unlock()
	if delay > 0 {
		select {
		case <-o.clk.After(delay):
		case <-ctx.Done():
			return nil
		}
	}
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
	gw.clk = clk
	obs := &fakeObserver{dns: map[string]bool{}, counters: map[uuid.UUID]*model.Counter{}, stepCounters: map[string]*model.Counter{}, gw: gw, clk: clk}
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

// advanceUntil moves the fake clock a second at a time, with a moment for the goroutines of the runs between the moves,
// until the condition holds: for the waits of a run that has no timer to wait for with BlockUntil.
func (h *harness) advanceUntil(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		h.clk.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
}

func (g *fakeGateway) setResetBlock(c chan struct{}) {
	g.mu.Lock()
	g.resetBlock = c
	g.mu.Unlock()
}

func (g *fakeGateway) setResetErr(err error) {
	g.mu.Lock()
	g.resetErr = err
	g.mu.Unlock()
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

// errTransient is what a reset that fails a few times answers (fakeGateway.resetFailures).
var errTransient = errors.New("apply failed, try again")

// failStep makes every overlay write fail with the message.
func failStep(msg string) func(engine.OverlayWrite) error {
	return func(engine.OverlayWrite) error { return errors.New(msg) }
}
