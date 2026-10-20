//go:build testbed

package api_test

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M15 on a real kernel, over the API and on the real clock: the steps of a run are handed to the engine at their times
// (within 100 ms of the schedule where the machine is accurate, plan §2.10), the overlays of a run are in the kernel
// while it lasts and not afterwards, and an abort takes them out again.

// stepsOnTime records how late each step was handed to the engine: never early, and within 100 ms where the machine
// is accurate (native or KVM). Under software emulation the time is recorded without the bound.
//
// This is the accuracy of the timeline. The kernel takes the change of a step at some moment inside its apply, which
// reads the kernel, writes the change and reads it back: about a tenth of a second on a native kernel with this
// configuration, seconds in a virtual machine on a shared runner (CI run 39208509316: 2 to 5 s per step), so the
// moment the engine answers (`offset_ms`) says how long the apply took, not how exact the timeline is. An apply is
// logged with every step, and the schedule of the tests leaves more time between two steps than an apply takes, because
// the timeline hands a step over only after the one before it has been answered, and a step that is due while the
// apply of the one before is running is handed over late by design.
//
// The change of a step that replaces, removes or restores overlays is handed over after the timeline has read their
// final counters (P4-AUDIT-17: `start_offset_ms` contains the read), and that read is a kernel read like the apply:
// 0.6 s in the VM of the CI (run 39343558893, the step `end`), a few milliseconds on a native kernel. A step that is
// handed over more than 100 ms behind its schedule must carry the run warning `step_late` (P4-AUDIT-14), and where the
// warning names the counter read as the cause (`read for N ms first`) the read is taken off, because it is the
// observation of the kernel and not the accuracy of the timeline: what remains is the lateness of the timeline itself,
// which stays within 100 ms. A lateness the warning does not explain fails as before.
func stepsOnTime(t *testing.T, run map[string]any) {
	t.Helper()
	hi := 100 * time.Millisecond
	if !testbed.Accurate() {
		hi = 30 * time.Second
	}
	readFor := counterReads(run)
	for _, s := range run["steps"].([]any) {
		st := s.(map[string]any)
		if st["status"] != "applied" {
			t.Errorf("step %v is %v", st["id"], st["status"])
			continue
		}
		sched, err := time.ParseDuration(st["scheduled_offset"].(string))
		if err != nil {
			t.Fatal(err)
		}
		handed, ok := st["start_offset_ms"].(float64)
		if !ok {
			t.Errorf("step %v has no start_offset_ms: %v", st["id"], st)
			continue
		}
		start := time.Duration(handed) * time.Millisecond
		answered := time.Duration(st["offset_ms"].(float64)) * time.Millisecond
		t.Logf("step %v: scheduled %v, handed to the engine at %v, answered at %v (the apply took %v, of which %v ms in the queue)",
			st["id"], sched, start, answered, answered-start, st["queue_wait_ms"])
		late := start - sched
		read := readFor[fmt.Sprint(st["id"])]
		if late > hi && testbed.Accurate() {
			if _, warned := stepLateWarning(run, fmt.Sprint(st["id"])); !warned {
				t.Errorf("step %v was handed over %v late and the run has no step_late warning for it: %v", st["id"], late, run["warnings"])
			}
		}
		if read > 0 {
			t.Logf("step %v: the counters of its overlays were read for %v before the hand-over (taken off its lateness of %v)", st["id"], read, late)
			late -= read
		}
		if err := testbed.Measure(t, "step "+fmt.Sprint(st["id"])).Duration("", "handed over late", late, 0, 0, hi); err != nil {
			t.Errorf("a step was not on time (the warnings of the run: %v): %v", run["warnings"], err)
		}
	}
}

var counterReadRE = regexp.MustCompile(`the counters of the overlays that go away were read for (\d+) ms first`)

// stepLateWarning returns the message of the warning step_late of a step.
func stepLateWarning(run map[string]any, step string) (string, bool) {
	ws, _ := run["warnings"].([]any)
	for _, w := range ws {
		m, _ := w.(map[string]any)
		if m["code"] == "step_late" && m["step"] == step {
			msg, _ := m["message"].(string)
			return msg, true
		}
	}
	return "", false
}

// counterReads is, for each step whose step_late warning says that the counters were read ahead of the hand-over, how
// long the read took.
func counterReads(run map[string]any) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, s := range run["steps"].([]any) {
		id := fmt.Sprint(s.(map[string]any)["id"])
		msg, ok := stepLateWarning(run, id)
		if !ok {
			continue
		}
		if m := counterReadRE.FindStringSubmatch(msg); m != nil {
			ms, _ := strconv.Atoi(m[1])
			out[id] = time.Duration(ms) * time.Millisecond
		}
	}
	return out
}

func kernelHasFaultChain(top *testbed.Topology) bool {
	return strings.Contains(top.GW.Must("nft", "list", "table", "inet", "chaosgw"), "chain mark_")
}

func TestAScenarioRunsOnTheRealKernelAndItsStepsAreOnTime(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	if kernelHasFaultChain(top) {
		t.Fatalf("a fault before any run:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	body := inlineRun("kernel-run", iotTarget,
		step("slow", "0s", "fault", map[string]any{"latency": "120ms", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("cut", "10s", "rule", map[string]any{"action": "drop", "protocol": "icmp", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("end", "20s", "restore", true),
	)
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)

	g.waitStep(id, "slow", "applied")
	if !kernelHasFaultChain(top) {
		t.Fatalf("the fault of the first step is not in the kernel:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	delayed := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond)
	if delayed.Loss() != 0 || delayed.Median() < 100*time.Millisecond {
		t.Errorf("A reaches the server with a median of %v and a loss of %v while the fault is on", delayed.Median(), delayed.Loss())
	}

	g.waitStep(id, "cut", "applied")
	cut := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond)
	if cut.Received != 0 {
		t.Errorf("A reaches the server (%d of %d answers) after the drop rule", cut.Received, cut.Sent)
	}

	done := g.waitRun(id, "passed")
	stepsOnTime(t, done)
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
	if kernelHasFaultChain(top) {
		t.Errorf("the kernel keeps a fault after the run:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	after := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond)
	if after.Loss() != 0 || after.Median() > 60*time.Millisecond {
		t.Errorf("after the run A reaches the server with a median of %v and a loss of %v", after.Median(), after.Loss())
	}
	rep := g.do("GET", "/runs/"+id+"/report.json", nil, nil, nil).json(t)
	counters, _ := rep["counters"].([]any)
	if len(counters) == 0 {
		t.Fatalf("the report has no counters: %v", rep)
	}
	packets := map[string]float64{}
	for _, c := range counters {
		m := c.(map[string]any)
		packets[m["step"].(string)] = m["counters"].(map[string]any)["packets"].(float64)
	}
	if packets["slow"] < 6 {
		t.Errorf("three pings are at least six packets of the fault: %v", packets)
	}
	if packets["cut"] < 1 {
		t.Errorf("the pings the rule dropped are counted: %v", packets)
	}
}

func TestAnAbortOnTheRealKernelTakesTheOverlaysOfTheRunOut(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	r := g.postRun(inlineRun("aborted", iotTarget,
		step("slow", "0s", "fault", map[string]any{"latency": "120ms", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("later", "1h", "restore", true),
	), "")
	id := r.json(t)["id"].(string)
	g.waitStep(id, "slow", "applied")
	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Median() < 100*time.Millisecond {
		t.Fatalf("the fault is not on: %v", res.Median())
	}
	ab := g.do("POST", "/runs/"+id+"/abort", nil, nil, nil)
	if ab.Status != 200 || ab.json(t)["status"] != "aborted" {
		t.Fatalf("%d %s", ab.Status, ab.Body)
	}
	// the answer came after the kernel was changed
	if kernelHasFaultChain(top) {
		t.Errorf("the kernel keeps the fault of an aborted run:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Median() > 60*time.Millisecond || res.Loss() != 0 {
		t.Errorf("after the abort the median is %v, the loss %v", res.Median(), res.Loss())
	}
}

// A run whose client is gone: the lease runs out on the real monotonic clock, the run is aborted and the kernel is
// clean again. The lease starts with the run, so it is longer than an apply can take (2 to 5 s in a virtual machine on
// a shared runner): the test waits for the step to be applied before it waits for the lease to run out.
func TestAnExpiredLeaseOnTheRealKernelRemovesTheFault(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	r := g.postRun(withLease(inlineRun("leased", iotTarget,
		step("slow", "0s", "fault", map[string]any{"latency": "120ms", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("later", "1h", "restore", true),
	), "20s"), "")
	id := r.json(t)["id"].(string)
	g.waitStep(id, "slow", "applied")
	done := g.waitRun(id, "aborted")
	if done["reason"] != "lease expired" {
		t.Errorf("reason %v", done["reason"])
	}
	if kernelHasFaultChain(top) {
		t.Errorf("the kernel keeps the fault of an expired run:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
}
