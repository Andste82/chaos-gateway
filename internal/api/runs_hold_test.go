package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// holdWrites holds every write to the kernel while it is armed, so that an apply stays in flight.
type holdWrites struct {
	inner   apply.Exec
	mu      sync.Mutex
	hold    chan struct{}
	entered chan struct{}
}

func (h *holdWrites) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	h.mu.Lock()
	hold, entered := h.hold, h.entered
	h.mu.Unlock()
	if hold != nil {
		for _, op := range ops {
			if _, read := op.(*executor.Read); read {
				continue
			}
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-hold:
			case <-ctx.Done():
			}
			break
		}
	}
	return h.inner.Do(ctx, ops...)
}

func (h *holdWrites) arm() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hold, h.entered = make(chan struct{}), make(chan struct{}, 1)
}

func (h *holdWrites) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hold != nil {
		close(h.hold)
		h.hold = nil
	}
}

// A step that has to wait behind the apply of a revision for more than 100 ms says so: its queue wait is in the
// report and the run has the warning step_late (plan §2.10).
func TestAStepBehindARevisionApplyWaitsAndTheRunSaysSo(t *testing.T) {
	gate := &holdWrites{}
	g := newGW(t, func(o *options) { o.execWrap = func(e apply.Exec) apply.Exec { gate.inner = e; return gate } })
	g.finishSetup()
	rev := g.mustPatch(map[string]any{"faults": map[string]any{
		"5c6d7e8f-9a0b-4c1d-8e2f-3a4b5c6d7e8f": map[string]any{"name": "cfg-latency", "source": map[string]any{"network": "IoT"}, "latency": "10ms"},
	}})

	gate.arm()
	applied := make(chan resp, 1)
	go func() { applied <- g.apply(rev) }()
	select {
	case <-gate.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the apply never reached the executor")
	}

	r := g.postRun(inlineRun("behind", iotTarget, step("a", "0s", "fault", map[string]any{"latency": "20ms"})), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	time.Sleep(400 * time.Millisecond) // the step waits for the apply in flight
	if s := stepOf(g.run(id), "a"); s["status"] != "pending" {
		t.Fatalf("the step was applied behind a held apply: %v", s)
	}
	gate.release()
	if ap := <-applied; ap.Status != 200 {
		t.Fatalf("%d %s", ap.Status, ap.Body)
	}
	done := g.waitRun(id, "passed")
	a := stepOf(done, "a")
	if wait, _ := a["queue_wait_ms"].(float64); wait < 300 {
		t.Errorf("the step waited %v ms behind a held apply", a["queue_wait_ms"])
	}
	// (the simulated kernel matches no packet, so the fault is also a zero hit)
	ws, _ := done["warnings"].([]any)
	late := 0
	for _, w := range ws {
		if m := w.(map[string]any); m["code"] == "step_late" && m["step"] == "a" {
			late++
		}
	}
	if late != 1 {
		t.Fatalf("warnings %v", done["warnings"])
	}
	// the JUnit file carries it
	x := g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil)
	if !strings.Contains(string(x.Body), "warning step_late") {
		t.Errorf("%s", x.Body)
	}
}
