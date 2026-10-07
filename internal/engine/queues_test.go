package engine_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Queue statistics and their epochs (M8b): the counters of a netem leaf per fault and direction, and
// the epoch that says whether two readings may be subtracted.

var tcDevs = []string{"br-iot", "br-lab", "wan0"}

// leafOf is the class and the handle of the leaf of a fault id and direction.
func leafOf(id int, dir compiler.Direction) (class, handle string) {
	class = compiler.ClassIDOf(id, dir)
	return class, strings.TrimPrefix(class, "1:") + ":"
}

func (h *harness) setLeafStats(dev string, id int, dir compiler.Direction, s linux.NormStats) {
	h.t.Helper()
	_, handle := leafOf(id, dir)
	h.k.SetTCStats(dev, handle, s)
}

func (h *harness) queueEpoch(dev string, id int, dir compiler.Direction) int64 {
	h.t.Helper()
	class, _ := leafOf(id, dir)
	e, ok := h.e.Snapshot().QueueEpochs[engine.QueueKey(dev, class)]
	if !ok {
		h.t.Fatalf("no epoch for %s %s: %v", dev, class, h.e.Snapshot().QueueEpochs)
	}
	return e
}

func TestTheQueuesOfAFaultAreReadWithTheCountersOfTheKernel(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, `
target: {network: IoT}
fault: {latency: 120ms, loss: 1%}`)
	f := h.e.Snapshot().Faults[0]
	h.setLeafStats("br-iot", f.ID, compiler.Upload, linux.NormStats{Bytes: 14200, Packets: 100, Drops: 4, Overlimits: 1, Backlog: 2900, Qlen: 2})
	h.setLeafStats("br-iot", f.ID, compiler.Download, linux.NormStats{Bytes: 700, Packets: 5})

	qs, err := h.e.ReadQueues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	up, down := leafKey("br-iot", f.ID, compiler.Upload), leafKey("br-iot", f.ID, compiler.Download)
	if got := qs[up]; got.Packets != 100 || got.Bytes != 14200 || got.Drops != 4 || got.Overlimits != 1 || got.Qlen != 2 || got.Backlog != 2900 {
		t.Errorf("upload queue of br-iot: %+v", got)
	}
	if got := qs[down]; got.Packets != 5 || got.Bytes != 700 {
		t.Errorf("download queue of br-iot: %+v", got)
	}
	// every interface of the tree has the queues of the fault; the others have no traffic
	for _, dev := range tcDevs {
		for _, dir := range []compiler.Direction{compiler.Upload, compiler.Download} {
			if _, ok := qs[leafKey(dev, f.ID, dir)]; !ok {
				t.Errorf("no queue for %s, direction %d", dev, dir)
			}
		}
	}
	if got := qs[leafKey("br-lab", f.ID, compiler.Upload)]; got.Packets != 0 || got.Drops != 0 {
		t.Errorf("an interface the traffic did not use has %+v", got)
	}
	// the default class and the root are not queues of a fault
	for k := range qs {
		if strings.HasSuffix(k, " 1:1") || strings.HasSuffix(k, " 1:") {
			t.Errorf("%q is no queue of a fault", k)
		}
	}
}

func leafKey(dev string, id int, dir compiler.Direction) string {
	class, _ := leafOf(id, dir)
	return engine.QueueKey(dev, class)
}

// A change of the parameters keeps the leaf, so its counters go on and its epoch stays; a leaf that
// is made again starts a new epoch, also when the fault keeps its id (the one change that drops a queue:
// a table that has to go, P2-M8a-05).
func TestAQueueKeepsItsEpochThroughAChangeAndStartsAnotherWhenTheLeafIsMadeAgain(t *testing.T) {
	h := startedWithRevision(t)
	const body = `
target: {network: IoT}
fault: {latency: 100ms, jitter: 20ms, distribution: normal}`
	r := h.mustPut(alice, body)
	f := h.e.Snapshot().Faults[0]
	first := h.queueEpoch("br-iot", f.ID, compiler.Upload)
	if first != int64(r.Generation) {
		t.Errorf("the epoch of a queue is the generation that made it: %d, the write was generation %d", first, r.Generation)
	}
	h.setLeafStats("br-iot", f.ID, compiler.Upload, linux.NormStats{Packets: 77, Bytes: 7700})

	// another delay and loss, the same table: in place
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 300ms, jitter: 20ms, loss: 2%, distribution: normal}")
	if got := h.queueEpoch("br-iot", f.ID, compiler.Upload); got != first {
		t.Errorf("a change in place started the epoch %d (was %d)", got, first)
	}
	if h.e.Snapshot().Faults[0].ID != f.ID {
		t.Fatal("the fault changed its id")
	}
	qs, err := h.e.ReadQueues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := qs[leafKey("br-iot", f.ID, compiler.Upload)]; got.Packets != 77 {
		t.Errorf("the counters did not survive the change: %+v", got)
	}

	// normal -> uniform: the leaf is deleted and made again
	r = h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 300ms, jitter: 20ms, loss: 2%}")
	if h.e.Snapshot().Faults[0].ID != f.ID {
		t.Fatal("the fault changed its id")
	}
	second := h.queueEpoch("br-iot", f.ID, compiler.Upload)
	if second != int64(r.Generation) || second <= first {
		t.Errorf("the epoch after the leaf was made again is %d, want the generation %d of the write (was %d)", second, r.Generation, first)
	}
	// the leaf of the other direction was made again as well, and the interfaces all
	for _, dev := range tcDevs {
		if got := h.queueEpoch(dev, f.ID, compiler.Download); got != second {
			t.Errorf("%s download: epoch %d, want %d", dev, got, second)
		}
	}
	// and a change that keeps the (uniform) leaf keeps this epoch
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 310ms, jitter: 20ms, loss: 2%}")
	if got := h.queueEpoch("br-iot", f.ID, compiler.Upload); got != second {
		t.Errorf("epoch %d after a change in place, want %d", got, second)
	}
}

// Updating one fault leaves the queues and the epochs of the others alone.
func TestUpdatingOneFaultLeavesTheQueuesOfTheOthersAlone(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 100ms, jitter: 10ms, distribution: normal}")
	h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 50ms}")
	var a, b compiler.Fault
	for _, f := range h.e.Snapshot().Faults {
		if f.Scope != "" && strings.Contains(strings.ToLower(f.Scope), "iot") {
			a = f
		} else {
			b = f
		}
	}
	if a.ID == 0 || b.ID == 0 {
		t.Fatalf("faults %+v", h.e.Snapshot().Faults)
	}
	h.setLeafStats("br-lab", b.ID, compiler.Upload, linux.NormStats{Packets: 900, Bytes: 90000, Drops: 7})
	epochB := h.queueEpoch("br-lab", b.ID, compiler.Upload)
	// the first one is changed three times, once so that its leaf is made again
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 200ms, jitter: 10ms, distribution: normal}")
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 200ms, jitter: 10ms}")
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 250ms, jitter: 10ms, loss: 3%}")

	qs, err := h.e.ReadQueues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := qs[leafKey("br-lab", b.ID, compiler.Upload)]; got.Packets != 900 || got.Drops != 7 {
		t.Errorf("the counters of the other fault changed: %+v", got)
	}
	if got := h.queueEpoch("br-lab", b.ID, compiler.Upload); got != epochB {
		t.Errorf("the epoch of the other fault changed from %d to %d", epochB, got)
	}
}

// A fault that goes and comes back has new queues, with a new epoch.
func TestAQueueOfAFaultThatCameBackIsANewOne(t *testing.T) {
	h := startedWithRevision(t)
	r := h.mustPut(alice, iotLatency)
	f := h.e.Snapshot().Faults[0]
	first := h.queueEpoch("br-iot", f.ID, compiler.Upload)
	if _, err := h.e.DeleteOverlay(context.Background(), r.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	if got := h.e.Snapshot().QueueEpochs; len(got) != 0 {
		t.Errorf("a queue of a fault that is gone has an epoch: %v", got)
	}
	h.clk.Advance(1100 * time.Millisecond)
	h.waitRetired()
	h.mustPut(alice, iotLatency)
	if got := h.queueEpoch("br-iot", h.e.Snapshot().Faults[0].ID, compiler.Upload); got <= first {
		t.Errorf("the queue of a fault that came back has the epoch %d (was %d)", got, first)
	}
}

// What an apply that failed did to the leaves is not known: the queues of the fault that were there
// get new epochs, so no reading across it is subtracted.
func TestAFailedApplyStartsNewEpochsForTheQueuesThatWereThere(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 30ms}")
	f := h.e.Snapshot().Faults[0]
	before := h.queueEpoch("br-iot", f.ID, compiler.Upload)
	var failed atomic.Int32
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "tc" && strings.Contains(strings.Join(argv, " "), "-batch") && strings.Contains(stdin, "delay 777ms") {
			failed.Add(1)
			return &executor.Result{Exit: 1, Stderr: "Error: injected\nCommand failed -:1\n"}
		}
		return nil
	}
	if _, err := h.put(bob, "target: {network: Lab}\nfault: {latency: 777ms}"); err == nil {
		t.Fatal("the write succeeded")
	}
	h.barrier()
	h.k.Fail = nil
	if failed.Load() == 0 {
		t.Fatal("the injected failure never ran")
	}
	if got := h.queueEpoch("br-iot", f.ID, compiler.Upload); got <= before {
		t.Errorf("the epoch is %d after a failed apply (was %d)", got, before)
	}
}

// The epoch of the nft counters as a whole: set by the first apply, kept by every later one, and new
// for a gateway that has restarted (it cannot tell what it finds).
func TestTheCounterEpochOfTheStateChangesWithARestartOnly(t *testing.T) {
	h := newHarness(t)
	genFile := filepath.Join(t.TempDir(), "generation")
	h.startWith(engine.Config{GenerationFile: genFile})
	if got := h.e.Snapshot().CounterEpoch; got != 0 {
		t.Errorf("the counter epoch is %d before any apply", got)
	}
	h.mustApply(h.revision(nil))
	epoch := h.e.Snapshot().CounterEpoch
	if epoch == 0 {
		t.Fatal("the first apply did not set the counter epoch")
	}
	h.mustPut(alice, iotLatency)
	h.mustPut(alice, "target: {network: IoT}\nfault: {latency: 200ms}")
	if got := h.e.Snapshot().CounterEpoch; got != epoch {
		t.Errorf("the counter epoch changed from %d to %d without a restart", epoch, got)
	}
	gen := h.e.Snapshot().Generation
	h.e.Close()
	h.startWith(engine.Config{GenerationFile: genFile})
	h.barrier()
	if got := h.e.Snapshot().CounterEpoch; got == 0 || got == epoch {
		t.Errorf("after a restart the counter epoch is %d (was %d at generation %d)", got, epoch, gen)
	}
}
