package engine

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// Flapping (plan §2.5): "intermittent connectivity" is a timed blackout, up for `up`, then down
// (netem loss 100 %) for `down`, and so on, starting with up. The phase is not part of the
// configuration: it is the clock's. The flapper below is the apply loop's schedule for it.
//
//   - A flapping fault is a set of tc classes that share a flap key (compiler.TCClass.FlapKey: the
//     fault and the direction, not the device, so a per-device fault flaps in step on all of its
//     devices). Its leaves hold the up configuration or Netem.Down(); the compiler writes whichever
//     phase the flapper reports (Input.FlapPhase), so a full apply never fights the schedule.
//   - The schedule starts when an apply that contains the flapping has been verified: a write that
//     is answered starts its fault's up phase at that moment. A fault whose flapping times change is a
//     new flapping and starts up again; one whose other parameters change keeps its phase.
//   - The boundaries are start + k × (up + down) and start + k × (up + down) + up on the injected
//     clock, computed from the start, never by adding up timer delays, so a late timer does not move
//     the next one. A boundary toggles the leaves of every class of the flapping on every interface of
//     the tree in one tc batch per interface (`replace` of the netem leaf: its queue, counters and seed
//     stay, the loss model is the only difference).
//   - The toggle runs in the apply loop's goroutine, between applies, so it never overlaps one. A
//     boundary that falls into an apply is made right after it (the phase is then the one the clock
//     says, not the one that was missed). Plan §2.10 gives scenario steps ±100 ms on a native or KVM
//     machine; FlapTolerance is the same figure for a boundary, measured from the schedule to the
//     moment the executor answered.
//
// The tolerance is a statement about the engine, not about the packets: a probe stream sees a boundary
// within its own spacing as well (internal/engine/integration_faults_extended_test.go).

// FlapTolerance is the largest delay between a flapping boundary on the schedule and the moment the
// toggle was committed that the engine promises on a native or KVM machine (plan §2.10).
const FlapTolerance = 100 * time.Millisecond

// flapRetry is how long the schedule waits before it tries a toggle that failed again.
const flapRetry = time.Second

// flapLogSize is the number of toggles the flapper remembers.
const flapLogSize = 1024

type flapEntry struct {
	spec  compiler.FlapSpec
	start time.Duration // monotonic: the start of the first up phase
	down  bool          // the phase the kernel holds
	since time.Duration // monotonic: when it entered that phase
}

// FlapState is a flapping fault as the engine sees it now.
type FlapState struct {
	// Key is the flap key (compiler.FlapKey).
	Key      string
	Up, Down time.Duration
	// InDown is the phase the kernel holds, Since the time it entered it and Next the time of the
	// next boundary (wall clock, derived from the monotonic schedule).
	InDown bool
	Since  time.Time
	Next   time.Time
}

// FlapChange is one toggle: the phase a flapping entered, when the schedule said so and when the
// executor had answered (both monotonic).
type FlapChange struct {
	Key       string
	Down      bool
	Scheduled time.Duration
	Committed time.Duration
	// Devices is the number of interfaces whose leaves were toggled.
	Devices int
}

// Late is how far behind the schedule the toggle was committed.
func (c FlapChange) Late() time.Duration { return c.Committed - c.Scheduled }

type flapper struct {
	mu      sync.Mutex
	entries map[string]*flapEntry
	log     []FlapChange
	failed  bool // the last toggle failed: do not retry in a loop
}

func newFlapper() *flapper { return &flapper{entries: map[string]*flapEntry{}} }

// phase is compiler.Input.FlapPhase: the phase the kernel holds for a flapping with this spec; a
// flapping the flapper does not know, or knows with other times, is up.
func (f *flapper) phase(key string, spec compiler.FlapSpec) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.entries[key]
	return e != nil && e.spec == spec && e.down
}

// sync takes over the flapping faults of a target the kernel was brought to: a flapping that is
// there with the same times keeps its schedule, a new or changed one starts at now (in the phase the
// target holds), one that is gone is forgotten.
func (f *flapper) sync(t *compiler.Target, now time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	if t != nil && t.TC != nil {
		for _, c := range t.TC.Classes {
			if c.FlapKey == "" || c.Netem.Flapping == nil {
				continue
			}
			seen[c.FlapKey] = true
			spec := *c.Netem.Flapping
			if e := f.entries[c.FlapKey]; e != nil && e.spec == spec {
				e.down = c.Down
				continue
			}
			f.entries[c.FlapKey] = &flapEntry{spec: spec, start: now, down: c.Down, since: now}
		}
	}
	for k := range f.entries {
		if !seen[k] {
			delete(f.entries, k)
		}
	}
	if len(f.entries) == 0 {
		f.failed = false
	}
}

// cycle position of an entry at now: the start of the current cycle and the offset inside it.
func (e *flapEntry) position(now time.Duration) (cycleStart, offset time.Duration) {
	cycle := e.spec.Up + e.spec.Down
	elapsed := now - e.start
	if elapsed < 0 || cycle <= 0 {
		return e.start, 0
	}
	n := elapsed / cycle
	return e.start + n*cycle, elapsed - n*cycle
}

// wantDown is the phase the schedule says at now.
func (e *flapEntry) wantDown(now time.Duration) bool {
	_, off := e.position(now)
	return off >= e.spec.Up
}

// lastBoundary is the moment of the latest boundary at or before now (the start for the first
// up phase); nextBoundary the earliest one after it.
func (e *flapEntry) lastBoundary(now time.Duration) time.Duration {
	cs, off := e.position(now)
	if off >= e.spec.Up {
		return cs + e.spec.Up
	}
	return cs
}

func (e *flapEntry) nextBoundary(now time.Duration) time.Duration {
	cs, off := e.position(now)
	if off < e.spec.Up {
		return cs + e.spec.Up
	}
	return cs + e.spec.Up + e.spec.Down
}

// flip is one flapping that has to change phase.
type flip struct {
	key       string
	down      bool
	scheduled time.Duration
	spec      compiler.FlapSpec
}

// due lists the flappings whose kernel phase is not the one the schedule says at now.
func (f *flapper) due(now time.Duration) []flip {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []flip
	for k, e := range f.entries {
		if w := e.wantDown(now); w != e.down {
			out = append(out, flip{key: k, down: w, scheduled: e.lastBoundary(now), spec: e.spec})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// next is the time until the schedule has something to do, false when there is no flapping.
func (f *flapper) next(now time.Duration) (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best time.Duration
	found := false
	for _, e := range f.entries {
		d := time.Duration(0)
		if e.wantDown(now) == e.down {
			d = e.nextBoundary(now) - now
		} else if f.failed {
			d = flapRetry
		}
		if !found || d < best {
			best, found = d, true
		}
	}
	return best, found
}

// committed records a toggle that the kernel answered.
func (f *flapper) committed(fl []flip, now time.Duration, devices int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = false
	for _, x := range fl {
		if e := f.entries[x.key]; e != nil {
			e.down, e.since = x.down, now
		}
		f.log = append(f.log, FlapChange{Key: x.key, Down: x.down, Scheduled: x.scheduled, Committed: now, Devices: devices})
	}
	if over := len(f.log) - flapLogSize; over > 0 {
		f.log = append([]FlapChange(nil), f.log[over:]...)
	}
}

func (f *flapper) failedToggle() {
	f.mu.Lock()
	f.failed = true
	f.mu.Unlock()
}

// FlapPhase reports whether the flapping with this key and times is in its down phase in the kernel
// now: compiler.Input.FlapPhase for a caller that compiles what the engine compiles.
func (e *Engine) FlapPhase(key string, spec compiler.FlapSpec) bool { return e.flap.phase(key, spec) }

// Flaps lists the flapping faults the engine runs, in the order of their keys.
func (e *Engine) Flaps() []FlapState {
	mono, wall := e.cfg.Clock.Monotonic(), e.cfg.Clock.Now()
	e.flap.mu.Lock()
	defer e.flap.mu.Unlock()
	out := make([]FlapState, 0, len(e.flap.entries))
	for k, x := range e.flap.entries {
		out = append(out, FlapState{Key: k, Up: x.spec.Up, Down: x.spec.Down, InDown: x.down,
			Since: wall.Add(x.since - mono), Next: wall.Add(x.nextBoundary(mono) - mono)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// FlapLog lists the toggles of the flapping faults, oldest first (the last flapLogSize of them), with
// the time the schedule asked for and the time the executor had answered.
func (e *Engine) FlapLog() []FlapChange {
	e.flap.mu.Lock()
	defer e.flap.mu.Unlock()
	return append([]FlapChange(nil), e.flap.log...)
}

// toggleFlaps brings the leaves of the flappings that are due to the phase the schedule says, in the
// kernel and in the target the apply loop holds. It returns the target to hold (the same one when
// nothing changed) and the error of a failed toggle, after which the schedule tries again.
func (e *Engine) toggleFlaps(ctx context.Context, last *appliedState) (*appliedState, error) {
	if last == nil || last.target == nil || last.target.TC == nil {
		return last, nil
	}
	now := e.cfg.Clock.Monotonic()
	fl := e.flap.due(now)
	if len(fl) == 0 {
		return last, nil
	}
	want := map[string]bool{}
	for _, x := range fl {
		want[x.key] = x.down
	}
	tc := *last.target.TC
	tc.Classes = append([]compiler.TCClass(nil), tc.Classes...)
	var changed []compiler.TCClass
	for i, c := range tc.Classes {
		if down, ok := want[c.FlapKey]; ok && c.FlapKey != "" {
			tc.Classes[i].Down = down
			changed = append(changed, tc.Classes[i])
		}
	}
	tg := executor.Target{NS: e.cfg.Namespace}
	var ops []executor.Operation
	for _, dev := range tc.Devs {
		var entries []executor.TCEntry
		for _, c := range changed {
			entries = append(entries, executor.TCEntry{Object: "qdisc", Action: "replace", Dev: dev, Parent: c.ClassID(), Handle: c.LeafHandle(), Args: c.Config().Args()})
		}
		for len(entries) > 0 {
			n := min(len(entries), executor.MaxTCEntries)
			ops = append(ops, &executor.TC{Target: tg, Entries: entries[:n:n]})
			entries = entries[n:]
		}
	}
	if len(ops) > 0 {
		if _, err := e.cfg.Exec.Do(ctx, ops...); err != nil {
			e.flap.failedToggle()
			return last, fmt.Errorf("toggle %d flapping faults: %w", len(fl), err)
		}
	}
	e.flap.committed(fl, e.cfg.Clock.Monotonic(), len(tc.Devs))
	nt := *last.target
	nt.TC = &tc
	return &appliedState{d: last.d, target: &nt}, nil
}

// flapTimer arms the apply loop's timer for the next boundary of a flapping.
type flapTimer struct {
	clk clock.Clock
	t   clock.Timer
	c   <-chan time.Time
}

func (ft *flapTimer) stop() {
	if ft.t != nil {
		ft.t.Stop()
		ft.t, ft.c = nil, nil
	}
}

func (ft *flapTimer) arm(f *flapper) {
	ft.stop()
	if d, ok := f.next(ft.clk.Monotonic()); ok {
		ft.t = ft.clk.NewTimer(d)
		ft.c = ft.t.C()
	}
}
