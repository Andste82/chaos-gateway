//go:build testbed

package api_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M15 on a real kernel, over the API and on the real clock: the steps of a run are applied at their times (within
// 100 ms of the schedule where the machine is accurate, plan §2.10), the overlays of a run are in the kernel while
// it lasts and not afterwards, and an abort takes them out again.

// stepsOnTime records how late each step was applied: never early, and within 100 ms where the machine is
// accurate (native or KVM). Under software emulation the time is recorded without the bound.
func stepsOnTime(t *testing.T, run map[string]any) {
	t.Helper()
	hi := 100 * time.Millisecond
	if !testbed.Accurate() {
		hi = 30 * time.Second
	}
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
		got := time.Duration(st["offset_ms"].(float64)) * time.Millisecond
		t.Logf("step %v: scheduled %v, applied at %v, queue wait %v ms", st["id"], sched, got, st["queue_wait_ms"])
		if err := testbed.Measure(t, "step "+fmt.Sprint(st["id"])).Duration("", "applied late", got-sched, 0, 0, hi); err != nil {
			t.Errorf("a step was not on time: %v", err)
		}
	}
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
		step("cut", "3s", "rule", map[string]any{"action": "drop", "protocol": "icmp", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("end", "6s", "restore", true),
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
// clean again.
func TestAnExpiredLeaseOnTheRealKernelRemovesTheFault(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	r := g.postRun(withLease(inlineRun("leased", iotTarget,
		step("slow", "0s", "fault", map[string]any{"latency": "120ms", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("later", "1h", "restore", true),
	), "3s"), "")
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
