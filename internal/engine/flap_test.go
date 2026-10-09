package engine_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Flapping (M10, plan §2.5): a timed blackout on the engine's clock. The tests move the fake clock
// and read the simulated kernel's netem leaves; the real kernel's loss is measured in
// integration_faults_extended_test.go.

const flapBody = "target: {network: IoT}\nfault: {flapping: {up: 20s, down: 10s}, latency: 30ms}"

// leafLoss is the loss of the netem leaf of a fault id and direction on an interface: 1 is a
// blackout.
func (h *harness) leafLoss(dev string, id int, dir compiler.Direction) float64 {
	h.t.Helper()
	parent := compiler.ClassIDOf(id, dir)
	for _, q := range h.tcTree(dev).Qdiscs {
		if q.Parent == parent && q.Netem != nil {
			return q.Netem.Loss
		}
	}
	h.t.Fatalf("%s has no leaf below %s", dev, parent)
	return 0
}

// waitLoss waits for the leaf to hold the loss (the toggle runs in the apply loop's goroutine).
func (h *harness) waitLoss(dev string, id int, dir compiler.Direction, loss float64, what string) {
	h.t.Helper()
	h.wait(func() bool { return h.leafLoss(dev, id, dir) == loss }, what)
}

func TestAFlappingFaultStartsUpAndTogglesOnTheClock(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, flapBody)
	f := h.e.Snapshot().Faults[0]
	devs := []string{"br-iot", "br-lab", "wan0"}
	for _, dev := range devs {
		if got := h.leafLoss(dev, f.ID, compiler.Upload); got != 0 {
			t.Fatalf("%s starts with loss %v, want the up phase", dev, got)
		}
	}
	flaps := h.e.Flaps()
	// one schedule per direction of the fault
	if len(flaps) != 2 || flaps[0].InDown || flaps[0].Up != 20*time.Second || flaps[0].Down != 10*time.Second {
		t.Fatalf("%+v", flaps)
	}
	h.verifyKernelWithOverlays()

	// 1 ms before the first boundary nothing has happened; at it, every direction on every interface
	// is a blackout
	h.clk.Advance(20*time.Second - time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if got := h.leafLoss("br-iot", f.ID, compiler.Upload); got != 0 {
		t.Fatalf("the blackout started early: loss %v", got)
	}
	h.clk.Advance(time.Millisecond)
	for _, dev := range devs {
		for _, dir := range []compiler.Direction{compiler.Upload, compiler.Download} {
			h.waitLoss(dev, f.ID, dir, 1, fmt.Sprintf("%s %s did not black out at 20 s", dev, dir))
		}
	}
	h.verifyKernelWithOverlays() // the compiler writes the phase the engine holds
	if fl := h.e.Flaps(); !fl[0].InDown || !fl[1].InDown {
		t.Errorf("%+v", fl)
	}

	// and back up after 10 s, down again after 20 s more
	h.clk.Advance(10 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 0, "the connection did not come back at 30 s")
	h.clk.Advance(20 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "the second blackout did not start at 50 s")
	h.verifyKernelWithOverlays()

	// every toggle was made at the moment the schedule asked for it (on the fake clock: exactly); the
	// two directions toggle together, three toggles of a pair each
	log := h.e.FlapLog()
	if len(log) != 6 {
		t.Fatalf("%d toggles: %+v", len(log), log)
	}
	start := log[0].Scheduled - 20*time.Second
	for i, want := range []time.Duration{20 * time.Second, 30 * time.Second, 50 * time.Second} {
		for _, c := range log[2*i : 2*i+2] {
			if c.Scheduled != start+want || c.Late() != 0 || c.Down != (i%2 == 0) || c.Devices != 3 {
				t.Errorf("toggle %d: %+v, want %v after the start, down=%v", i, c, want, i%2 == 0)
			}
		}
	}
}

func TestAClockThatJumpsOverFlappingBoundariesLandsInThePhaseItSays(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, flapBody)
	f := h.e.Snapshot().Faults[0]
	// 65 s: two full cycles (60 s) and 5 s into the third up phase
	h.clk.Advance(65 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if got := h.leafLoss("br-iot", f.ID, compiler.Upload); got != 0 {
		t.Errorf("65 s into 20 s up / 10 s down is an up phase, the leaf has loss %v", got)
	}
	// 78 s: 18 s into a cycle, still up; 82 s: 22 s, down
	h.clk.Advance(17 * time.Second)
	time.Sleep(50 * time.Millisecond)
	h.clk.Advance(4 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "82 s is a down phase")
	h.verifyKernelWithOverlays()
}

// A full apply while the connection is down keeps it down: the compiler writes the phase the engine
// holds, so another overlay (or a revision) does not end a blackout early.
func TestAnApplyDuringTheDownPhaseKeepsTheBlackout(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, flapBody)
	f := h.e.Snapshot().Faults[0]
	h.clk.Advance(21 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "no blackout at 21 s")
	h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 50ms}")
	if got := h.leafLoss("br-iot", f.ID, compiler.Upload); got != 1 {
		t.Errorf("an unrelated overlay ended the blackout: loss %v", got)
	}
	h.verifyKernelWithOverlays()
	// the schedule goes on from the same start: up at 30 s
	h.clk.Advance(9 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 0, "no recovery at 30 s")
}

// Changing the other parameters of a flapping fault keeps its schedule; changing its times starts it
// again, up.
func TestAReplacedFlappingKeepsItsScheduleUnlessItsTimesChange(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, flapBody)
	f := h.e.Snapshot().Faults[0]
	h.clk.Advance(21 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "no blackout at 21 s")

	// same times, other latency: still down, same schedule
	h.mustPut(alice, "target: {network: IoT}\nfault: {flapping: {up: 20s, down: 10s}, latency: 80ms}")
	if got := h.leafLoss("br-iot", f.ID, compiler.Upload); got != 1 {
		t.Fatalf("a change of the latency ended the blackout: loss %v", got)
	}
	var delay float64
	for _, q := range h.tcTree("br-iot").Qdiscs {
		if q.Parent == compiler.ClassIDOf(f.ID, compiler.Upload) {
			delay = q.Netem.Delay
		}
	}
	if delay != 0.08 {
		t.Errorf("delay %v after the change", delay)
	}
	h.verifyKernelWithOverlays()
	h.clk.Advance(9 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 0, "the schedule moved: no recovery at 30 s")

	// other times: a new flapping, it starts up at once and its first boundary is 5 s away
	h.mustPut(alice, "target: {network: IoT}\nfault: {flapping: {up: 5s, down: 5s}, latency: 80ms}")
	if got := h.leafLoss("br-iot", f.ID, compiler.Upload); got != 0 {
		t.Fatalf("a new flapping must start up, loss %v", got)
	}
	if fl := h.e.Flaps(); len(fl) != 2 || fl[0].Up != 5*time.Second || fl[0].InDown {
		t.Fatalf("%+v", fl)
	}
	h.clk.Advance(5 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "no blackout 5 s after the new start")
	h.verifyKernelWithOverlays()
}

// A fault with a rate flaps too: its queue (the shared one of the addresses no device owns here) is a
// blackout in the down phase and keeps its rate.
func TestAFlappingFaultWithARateKeepsTheRateInTheDownPhase(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, "target: {network: IoT}\nfault: {flapping: {up: 10s, down: 10s}, rate: 2Mbit}")
	f := h.e.Snapshot().Faults[0]
	h.clk.Advance(10 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "no blackout")
	for _, q := range h.tcTree("br-lab").Qdiscs {
		if q.Parent == compiler.ClassIDOf(f.ID, compiler.Download) && q.Netem.Rate != 250000 {
			t.Errorf("the rate in the down phase is %d bytes/s", q.Netem.Rate)
		}
	}
	h.verifyKernelWithOverlays()
}

func TestDeletingAFlappingFaultStopsItsSchedule(t *testing.T) {
	h := startedWithRevision(t)
	r := h.mustPut(alice, flapBody)
	if _, err := h.e.DeleteOverlay(t.Context(), r.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	if fl := h.e.Flaps(); len(fl) != 0 {
		t.Fatalf("%+v", fl)
	}
	h.clk.Advance(5 * time.Minute)
	time.Sleep(100 * time.Millisecond)
	if len(h.e.FlapLog()) != 0 {
		t.Errorf("a deleted flapping still toggles: %+v", h.e.FlapLog())
	}
	h.verifyKernelWithOverlays()
}

// A toggle that the executor fails is tried again a second later; the schedule does not get stuck and
// the phase comes out right.
func TestAFailedFlappingToggleIsTriedAgain(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, flapBody)
	f := h.e.Snapshot().Faults[0]
	var failures atomic.Int32
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "tc" && strings.Contains(stdin, "loss random 100%") && failures.Add(1) == 1 {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\nCommand failed -:1\n"}
		}
		return nil
	}
	h.clk.Advance(20 * time.Second)
	h.wait(func() bool { return failures.Load() >= 1 }, "the toggle never ran")
	time.Sleep(50 * time.Millisecond)
	if got := h.leafLoss("br-iot", f.ID, compiler.Upload); got != 0 {
		t.Fatalf("the failed toggle changed something: loss %v", got)
	}
	h.clk.Advance(time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "the retry did not toggle")
	h.k.Fail = nil
	h.verifyKernelWithOverlays()
	// two directions, each toggled by the retry, a second after the boundary
	if log := h.e.FlapLog(); len(log) != 2 || log[0].Late() != time.Second || log[1].Late() != time.Second {
		t.Errorf("the retry is a second late: %+v", log)
	}
}

// A flapping fault from a revision (the configuration), not an overlay, flaps too.
func TestAConfiguredFlappingFaultFlaps(t *testing.T) {
	h := newHarness(t)
	h.start()
	rev := h.revision(func(c *model.Configuration) {
		var f model.ConfigFault
		if err := json.Unmarshal([]byte(`{"source": {"network": "IoT"}, "flapping": {"up": "4s", "down": "2s"}, "latency": "10ms"}`), &f); err != nil {
			t.Fatal(err)
		}
		c.Faults = &map[string]model.ConfigFault{"8a1b2c3d-1111-4222-8333-444455556666": f}
	})
	h.mustApply(rev)
	f := h.e.Snapshot().Faults[0]
	h.clk.Advance(4 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 1, "the configured fault did not black out")
	h.clk.Advance(2 * time.Second)
	h.waitLoss("br-iot", f.ID, compiler.Upload, 0, "the configured fault did not come back")
}
