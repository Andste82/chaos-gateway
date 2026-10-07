//go:build testbed

package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The tests of M8b (plan "Tests") about changing faults while traffic flows: updating one fault does not
// disturb the others, changing the parameters of a 600 ms fault under load loses no queued packet,
// switching a device to a new fault id loses no packet's classification, and a write returns only after
// the kernel holds the tree, or is taken back when a tc operation fails. All through the engine's overlay
// writes, so the apply loop, the retirer and the verify are the ones of the product. (The apply package's
// tests do the same one level down, with the plans in their hands.)

// streamRun is a probe run that goes on while the test changes faults.
func streamRun(f flow) *testbed.Running {
	return f.echo.Begin(f.from, testbed.ProbeOptions{Src: f.src, Interval: 20 * time.Millisecond, Settle: 3 * time.Second})
}

// waitDelivered waits until the echo has had n datagrams of the run.
func waitDelivered(t *testing.T, run *testbed.Running, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for run.Delivered() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the stream does not flow: %d delivered", run.Delivered())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Updating one fault does not disturb the others: while C's flow runs through its own fault, the fault
// of A is changed in place, made again (its distribution goes back to uniform, which needs a new netem),
// joined by a fault for B and removed. C's flow loses no packet, keeps its delay, its queues keep their
// epoch and count exactly the packets of the flow, and the named counters of its fault do too.
func TestUpdatingOneFaultDoesNotDisturbTheOthers(t *testing.T) {
	r := startFaultLab(t, nil)
	_, _, c := serverFlows(t, r.top)
	cShape := shape{up: 40 * time.Millisecond, upJitter: 4 * time.Millisecond, down: 15 * time.Millisecond}

	x := r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: 100ms, jitter: 10ms, distribution: normal}, download: {latency: 20ms}}")
	y := r.mustPut(admin, "target: {device: dev-c}\nfault: "+cShape.yaml(""))
	fy := faultOfOverlay(t, r.e.Snapshot(), y.Overlay)
	_, _, _, upEpochs := r.queueSum(fy, compiler.Upload)
	_, _, _, downEpochs := r.queueSum(fy, compiler.Download)
	if len(upEpochs) != 1 || len(downEpochs) != 1 {
		t.Fatalf("the queues of the fault were made in more than one generation: %v %v", upEpochs, downEpochs)
	}
	r.verifyKernel()

	run := streamRun(c)
	waitDelivered(t, run, 20)

	// 1 a change in place, 2 a netem made again, 3 a new fault with new classes on every interface,
	// 4 a removal: the classes of the removed fault stay for the largest delay plus a second
	r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: 120ms, jitter: 10ms, distribution: normal}, download: {latency: 25ms}}")
	r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: 120ms, jitter: 10ms}, download: {latency: 25ms}}")
	r.mustPut(admin, "target: {device: dev-b}\nfault: {latency: 70ms}")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := r.e.DeleteOverlay(ctx, x.Overlay.Id, &admin, admin); err != nil {
		t.Fatal(err)
	}
	r.verifyKernel()

	res, err := run.Stop()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("C during the changes of the other faults: %s", res)
	if res.Sent < 40 {
		t.Fatalf("the stream was too short to say anything: %s", res)
	}
	if res.UpLoss() != 0 || res.DownLoss() != 0 {
		t.Errorf("C lost packets while other faults changed: %s", res)
	}
	if got := res.UpMedian(); got < cShape.up-cShape.upJitter-time.Millisecond || got > cShape.up+25*time.Millisecond {
		t.Errorf("C's upload delay is %v while the fault says %v: it followed another fault", got, cShape.up)
	}
	if got := res.DownMedian(); got < cShape.down-time.Millisecond || got > cShape.down+25*time.Millisecond {
		t.Errorf("C's download delay is %v while the fault says %v", got, cShape.down)
	}
	up, upDrops, backlog, upNow := r.queueSum(fy, compiler.Upload)
	down, downDrops, _, downNow := r.queueSum(fy, compiler.Download)
	if !sameSet(upNow, upEpochs) || !sameSet(downNow, downEpochs) {
		t.Errorf("the queues of C's fault were made again: epochs %v %v, were %v %v", upNow, downNow, upEpochs, downEpochs)
	}
	if up != int64(res.Sent) || down != int64(res.Delivered) || upDrops != 0 || downDrops != 0 || backlog != 0 {
		t.Errorf("C's queues: up %d (flow sent %d), down %d (flow delivered %d), drops %d %d, backlog %d", up, res.Sent, down, res.Delivered, upDrops, downDrops, backlog)
	}
	cs := r.counters()
	if got := cs[fy.CounterUp].Packets; got != int64(res.Sent) {
		t.Errorf("the upload counter of C's fault is %d, the flow sent %d", got, res.Sent)
	}
	if got := cs[fy.CounterDown].Packets; got != int64(res.Delivered) {
		t.Errorf("the download counter of C's fault is %d, the flow delivered %d", got, res.Delivered)
	}
	if testbed.Accurate() {
		measure := func() testbed.ProbeResult { return c.run(t, impairedRun()) }
		// the stream is the first attempt only when it was long enough for the median (N >= 200, plan §4.3)
		fresh := &res
		if res.Sent < 200 {
			fresh = nil
		}
		testbed.Statistically(t, "C's delay after the changes", func() error {
			cur := fresh
			if cur == nil {
				again := measure()
				t.Logf("C measured again: %s", again)
				cur = &again
			}
			fresh = nil
			return joinErrs(
				testbed.CheckLatency("C upload", cur.UpMedian(), cShape.up),
				testbed.CheckLatency("C download", cur.DownMedian(), cShape.down),
				testbed.CheckSpread("C upload", cur.Up, cShape.upJitter))
		})
	}
}

// Changing the parameters of a 600 ms fault under load loses no queued packet (plan M8b): the fault drops
// 5 % itself, and through a change to a longer and to a shorter delay every packet sent is either
// delivered or counted as a drop of the fault's own queue (sent == delivered + drops). The queue is
// the same one throughout (no new epoch), and packets that were queued leave at the time they were given.
func TestChangingAFaultOf600msThroughTheEngineUnderLoadLosesNoQueuedPacket(t *testing.T) {
	r := startFaultLab(t, nil)
	a, _, _ := serverFlows(t, r.top)
	w := r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: 600ms, loss: 5%}}")
	f := faultOfOverlay(t, r.e.Snapshot(), w.Overlay)
	_, _, _, epochs := r.queueSum(f, compiler.Upload)

	run := streamRun(a)
	waitDelivered(t, run, 10)
	for _, lat := range []string{"900ms", "250ms", "600ms"} {
		// packets are queued in the fault when the change comes: 600 ms of a stream of 50 per second
		if _, _, backlog, _ := r.queueSum(f, compiler.Upload); backlog == 0 {
			t.Fatalf("nothing is queued in the fault when it is changed to %s", lat)
		}
		r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: "+lat+", loss: 5%}}")
	}
	res, err := run.Stop()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("A through the changes: %s", res)
	sent, drops, backlog, now := r.queueSum(f, compiler.Upload)
	if res.Sent < 100 || drops == 0 {
		t.Fatalf("the stream was too short (%d sent) or the fault dropped nothing (%d): nothing to conserve", res.Sent, drops)
	}
	if want := int64(res.Delivered) + drops; int64(res.Sent) != want {
		t.Errorf("sent %d, delivered %d, drops counted by the fault %d: %d packets lost by the changes", res.Sent, res.Delivered, drops, int64(res.Sent)-want)
	}
	if sent != int64(res.Delivered) || backlog != 0 {
		t.Errorf("the queue says %d sent and %d queued, the echo got %d", sent, backlog, res.Delivered)
	}
	if res.Replied != res.Delivered {
		t.Errorf("%d delivered, %d answers back: the download lost packets that no fault drops", res.Delivered, res.Replied)
	}
	if !sameSet(now, epochs) {
		t.Errorf("the queue was made again by a change of its parameters: epochs %v, were %v", now, epochs)
	}
	r.verifyKernel()
	if testbed.Accurate() {
		// the drops are the configured 5 % of what was sent; the stream is the first attempt only when it
		// was long enough for the interval (N >= 2000, plan §4.3), otherwise every attempt is a fresh run
		first := res.Sent >= 2000
		testbed.Statistically(t, "loss of the 600 ms fault", func() error {
			if first {
				first = false
				return testbed.CheckLoss("upload", int(drops), res.Sent, 0.05)
			}
			again := a.run(t, impairedRun())
			t.Logf("A measured again: %s", again)
			return testbed.CheckLoss("upload", again.Sent-again.Delivered, again.Sent, 0.05)
		})
	}
}

// Switching a device to a new fault id (make before break, plan §3.2): the device has a fault, then a
// more specific one wins for its stream (a new id); the stream loses no packet and none of its packets
// goes unclassified (the class of the old and of the new id together counted every one), the packets
// queued in the old fault leave, and when the old fault is removed its classes stay while a queued
// packet may be in them and go after.
func TestSwitchingADeviceToANewFaultIdThroughTheEngineLosesNoPacketAndTheOldClassesGoLater(t *testing.T) {
	r := startFaultLab(t, nil)
	a, _, _ := serverFlows(t, r.top)
	old := r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: 400ms}}")
	fOld := faultOfOverlay(t, r.e.Snapshot(), old.Overlay)

	run := streamRun(a)
	waitDelivered(t, run, 10)
	if _, _, backlog, _ := r.queueSum(fOld, compiler.Upload); backlog == 0 {
		t.Fatal("nothing is queued in the old fault when the new one comes")
	}
	// the new fault names a destination, which wins over the old one for this stream (plan §2.4: the
	// device with a destination before the device)
	neu := r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: 100ms}, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	snap := r.e.Snapshot()
	fNew := faultOfOverlay(t, snap, neu.Overlay)
	if fNew.ID == fOld.ID {
		t.Fatalf("the new fault took the id of the old one: %d", fNew.ID)
	}
	tg := r.verifyKernel()
	oldUp, newUp := classOfFault(t, tg, fOld, compiler.Upload), classOfFault(t, tg, fNew, compiler.Upload)
	deadline := time.Now().Add(2 * time.Minute)
	for r.classTotal(tg, newUp) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stream was never classified into the new fault")
		}
		time.Sleep(100 * time.Millisecond)
	}
	oldNow := r.classTotal(tg, oldUp)
	oldCounted := r.counters()[fOld.CounterUp].Packets
	time.Sleep(time.Second)
	if later := r.classTotal(tg, oldUp); later != oldNow {
		t.Errorf("the old class still takes packets after the switch: %d -> %d", oldNow, later)
	}

	// the old fault goes: its id is free, its classes are held for the largest delay (400 ms) plus a
	// second, and then deleted
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := r.e.DeleteOverlay(ctx, old.Overlay.Id, &admin, admin); err != nil {
		t.Fatal(err)
	}
	if len(r.e.RetiringTC()) == 0 {
		t.Log("the classes of the removed fault were deleted already (the apply took longer than the delay)")
	}
	r.verifyKernel()
	for len(r.e.RetiringTC()) > 0 {
		if time.Now().After(deadline.Add(2 * time.Minute)) {
			t.Fatalf("the classes of the removed fault are never deleted: %+v", r.e.RetiringTC())
		}
		time.Sleep(200 * time.Millisecond)
	}

	res, err := run.Stop()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("A across the switch: %s", res)
	if res.Sent < 100 {
		t.Fatalf("the stream was too short: %s", res)
	}
	if res.UpLoss() != 0 || res.DownLoss() != 0 {
		t.Errorf("the switch lost packets: %s", res)
	}
	// every packet of the stream was counted by the old or by the new class (they count at enqueue):
	// none went unclassified in between. The old class's total was read before the stream ended, and
	// the old class is gone now, so the totals are those read while both existed: the old one up to
	// the switch, the new one at the end.
	inNew := r.classTotal(tg, newUp)
	newCounted := r.counters()[fNew.CounterUp].Packets
	if oldNow+inNew != int64(res.Sent) || oldCounted+newCounted != int64(res.Sent) {
		t.Errorf("the stream sent %d datagrams: the classes counted %d (old, before the switch) and %d (new), the faults' counters %d and %d\n%s",
			res.Sent, oldNow, inNew, oldCounted, newCounted, r.top.GW.Must("tc", "-s", "class", "show", "dev", "wan0"))
	}
	out := r.top.GW.Must("tc", "class", "show", "dev", "wan0")
	if strings.Contains(out, oldUp.ClassID()+" ") {
		t.Errorf("the class of the removed fault is still on wan0:\n%s", out)
	}
	r.verifyKernel()
}

// An overlay write returns only after the kernel holds its tree and the apply verified it (plan §2.15
// "writes return after verification"): a write whose tc operation is held in the executor does not return,
// and when the operation goes on it returns with the fault in the kernel. And when the tc operation fails,
// the write is taken back like every other failure: apply_failed, the overlay gone, the kernel the compile
// of the snapshot again, the next write works.
func TestAnOverlayWriteWaitsForTheKernelsTreeAndAFailedTCOperationTakesItBack(t *testing.T) {
	gate := &kernelGate{}
	r := newRealRunner(t, gate.wrap, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	keep := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 30ms, destination: {cidr: 192.0.2.0/24}}")
	has777 := func(c executor.Command) bool { return strings.Contains(c.Stdin, "delay 777ms") }

	// held: the write waits
	entered := gate.holdTC(has777)
	type outcome struct {
		res engine.OverlayResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.put(admin, "target: {network: Lab}\nfault: {latency: 777ms, destination: {cidr: 192.0.2.0/24}}")
		done <- outcome{res, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Minute):
		t.Fatal("the apply never reached the tc operation")
	}
	select {
	case o := <-done:
		t.Fatalf("the write returned (%v) while the kernel has not got the tree yet", o.err)
	case <-time.After(3 * time.Second):
	}
	if strings.Contains(r.top.GW.Must("tc", "qdisc", "show", "dev", "wan0"), "777") {
		t.Fatal("the held operation has reached the kernel")
	}
	gate.releaseTC()
	o := <-done
	if o.err != nil {
		t.Fatal(o.err)
	}
	if !strings.Contains(r.top.GW.Must("tc", "qdisc", "show", "dev", "wan0"), "delay 777ms") {
		t.Errorf("the write returned but the kernel has no 777 ms netem:\n%s", r.top.GW.Must("tc", "qdisc", "show", "dev", "wan0"))
	}
	r.verifyKernel()
	if o.res.Generation == 0 || r.e.Snapshot().Applied.Generation < o.res.Generation {
		t.Errorf("the write's generation %d is not applied (applied %d)", o.res.Generation, r.e.Snapshot().Applied.Generation)
	}

	// failed: the write is refused and everything is as before
	if _, err := r.e.DeleteOverlay(context.Background(), o.res.Overlay.Id, &admin, admin); err != nil {
		t.Fatal(err)
	}
	gate.failTC(func(c executor.Command) bool { return strings.Contains(c.Stdin, "delay 888ms") })
	_, err := r.put(admin, "target: {network: Lab}\nfault: {latency: 888ms, destination: {cidr: 192.0.2.0/24}}")
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) {
		t.Fatalf("got %v, want apply_failed", err)
	}
	s, err := r.e.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gate.failTC(nil)
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id || s.LastError != "" {
		t.Fatalf("after the failed write: %d overlays, last error %q", len(s.Overlays), s.LastError)
	}
	if strings.Contains(r.top.GW.Must("tc", "qdisc", "show", "dev", "wan0"), "888") {
		t.Error("the failed write left its netem in the kernel")
	}
	r.verifyKernel()
	if res := r.mustPut(admin, "target: {network: Lab}\nfault: {latency: 40ms, destination: {cidr: 192.0.2.0/24}}"); !res.Created {
		t.Errorf("%+v", res)
	}
	r.verifyKernel()
}
