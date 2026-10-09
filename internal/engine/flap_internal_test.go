package engine

import (
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// The schedule arithmetic of the flapper: boundaries are start + k × (up + down) and + up, computed from
// the start, so no amount of lateness moves the next one.

func entry(start time.Duration, up, down time.Duration) *flapEntry {
	return &flapEntry{spec: compiler.FlapSpec{Up: up, Down: down}, start: start}
}

func TestTheBoundariesOfAFlappingAreMultiplesOfTheCycleFromItsStart(t *testing.T) {
	const s = time.Second
	e := entry(100*s, 20*s, 10*s)
	for _, c := range []struct {
		now      time.Duration // after the start
		down     bool
		last     time.Duration // the latest boundary at or before now, after the start
		next     time.Duration // the earliest after it
		scenario string
	}{
		{0, false, 0, 20, "at the start"},
		{19999 * time.Millisecond, false, 0, 20, "just before the first blackout"},
		{20 * s, true, 20, 30, "at the first blackout"},
		{29999 * time.Millisecond, true, 20, 30, "just before the connection returns"},
		{30 * s, false, 30, 50, "at the return"},
		{49 * s, false, 30, 50, "late in the second up phase"},
		{50 * s, true, 50, 60, "the second blackout"},
		{3600 * s, false, 3600, 3620, "an hour in: exactly a multiple of the cycle"},
		{3619 * s, false, 3600, 3620, "an hour in, late in the up phase"},
		{3625 * s, true, 3620, 3630, "an hour in, in the down phase"},
	} {
		now := e.start + c.now
		if got := e.wantDown(now); got != c.down {
			t.Errorf("%s: down %v, want %v", c.scenario, got, c.down)
		}
		if got := e.lastBoundary(now) - e.start; got != c.last*s {
			t.Errorf("%s: last boundary %v, want %v", c.scenario, got, c.last*s)
		}
		if got := e.nextBoundary(now) - e.start; got != c.next*s {
			t.Errorf("%s: next boundary %v, want %v", c.scenario, got, c.next*s)
		}
	}
	// before the start (a clock that read earlier than the sync) is the first up phase
	if e.wantDown(e.start - time.Second) {
		t.Error("before the start the connection is up")
	}
}

func TestADueFlappingIsTheOneWhoseKernelPhaseIsNotTheSchedulesAndALateTimerDoesNotMoveTheNextBoundary(t *testing.T) {
	f := newFlapper()
	tg := &compiler.Target{TC: &compiler.TCTarget{Classes: []compiler.TCClass{
		{ID: 1, Dir: compiler.Upload, FlapKey: "k|upload", Netem: compiler.Netem{Flapping: &compiler.FlapSpec{Up: 10 * time.Second, Down: 5 * time.Second}}},
		{ID: 2, Dir: compiler.Upload, FlapKey: "k|upload", Netem: compiler.Netem{Flapping: &compiler.FlapSpec{Up: 10 * time.Second, Down: 5 * time.Second}}},
		{ID: 3, Dir: compiler.Upload}, // does not flap
	}}}
	f.sync(tg, 1000*time.Second)
	if len(f.entries) != 1 {
		t.Fatalf("%+v", f.entries)
	}
	if d, ok := f.next(1000 * time.Second); !ok || d != 10*time.Second {
		t.Fatalf("next in %v (%v), want the end of the up phase", d, ok)
	}
	if got := f.due(1009 * time.Second); len(got) != 0 {
		t.Errorf("due before the boundary: %+v", got)
	}
	// the timer is a second late: the toggle is due, and says the boundary it was due at
	got := f.due(1011 * time.Second)
	if len(got) != 1 || !got[0].down || got[0].scheduled != 1010*time.Second {
		t.Fatalf("%+v", got)
	}
	if d, _ := f.next(1011 * time.Second); d != 0 {
		t.Errorf("a due toggle is due now, not in %v", d)
	}
	f.committed(got, 1011*time.Second, 3)
	// the next boundary is the one of the schedule, 1015, not 15 s after the late toggle
	if d, _ := f.next(1011 * time.Second); d != 4*time.Second {
		t.Errorf("the next boundary is in %v, want 4 s (1015)", d)
	}
	// a clock that jumped over a whole cycle (31 s in, 1 s into the third cycle: up) while the kernel is
	// in the down phase: one toggle, into the phase the schedule says
	if got := f.due(1031 * time.Second); len(got) != 1 || got[0].down || got[0].scheduled != 1030*time.Second {
		t.Errorf("%+v", got)
	}
	// the same times again keep the schedule, new times start over, a gone fault is forgotten
	f.sync(tg, 2000*time.Second)
	if f.entries["k|upload"].start != 1000*time.Second {
		t.Error("a sync with the same times moved the start")
	}
	tg.TC.Classes[0].Netem.Flapping = &compiler.FlapSpec{Up: time.Second, Down: time.Second}
	tg.TC.Classes[1].Netem.Flapping = &compiler.FlapSpec{Up: time.Second, Down: time.Second}
	f.sync(tg, 2000*time.Second)
	if e := f.entries["k|upload"]; e.start != 2000*time.Second || e.down {
		t.Errorf("%+v", e)
	}
	f.sync(&compiler.Target{}, 3000*time.Second)
	if len(f.entries) != 0 {
		t.Error("a flapping that is gone is still scheduled")
	}
	if _, ok := f.next(3000 * time.Second); ok {
		t.Error("nothing to schedule")
	}
}

func TestThePhaseTheCompilerAsksForIsTheKernelsAndOnlyForTheSameTimes(t *testing.T) {
	f := newFlapper()
	spec := compiler.FlapSpec{Up: 10 * time.Second, Down: 5 * time.Second}
	if f.phase("k|upload", spec) {
		t.Error("a flapping the flapper does not know is up")
	}
	f.entries["k|upload"] = &flapEntry{spec: spec, down: true}
	if !f.phase("k|upload", spec) {
		t.Error("the kernel is in the down phase")
	}
	if f.phase("k|upload", compiler.FlapSpec{Up: 11 * time.Second, Down: 5 * time.Second}) {
		t.Error("other times are a new flapping: up")
	}
}
