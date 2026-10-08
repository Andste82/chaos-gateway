//go:build testbed

package engine_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The netem queue statistics and their epochs on a real kernel (M8b), and the measurement behind the
// queue limit of the compiler (P2-M8a-02).

// queueSum adds the readings of the queue of a fault and direction over the interfaces of the tree
// (the packets of one direction leave through one of them).
func (r *real) queueSum(f compiler.Fault, dir compiler.Direction) (sent, drops, backlog int64, epochs map[int64]bool) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	qs, err := r.e.ReadQueues(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	epochs = map[int64]bool{}
	class := compiler.ClassIDOf(f.ID, dir)
	snap := r.e.Snapshot()
	for _, dev := range snap.TCDevs {
		key := engine.QueueKey(dev, class)
		st, ok := qs[key]
		if !ok {
			r.t.Fatalf("no queue %q in %v", key, qs)
		}
		sent += int64(st.Packets)
		drops += int64(st.Drops)
		backlog += int64(st.Qlen)
		epochs[snap.QueueEpochs[key]] = true
	}
	return
}

// The queue of a fault counts the packets of its scope and what its loss dropped, equal to what the
// class counts; the queue of another fault does not move; a change of the parameters keeps queue,
// counters and epoch; a leaf that has to be made again starts a new epoch at zero.
func TestTheQueuesCountTheKernelsPacketsAndKeepOrRestartTheirEpoch(t *testing.T) {
	r := newReal(t, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	iot := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 2ms, jitter: 1ms, distribution: normal, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	lab := r.mustPut(admin, "target: {network: Lab}\nfault: {latency: 3ms, loss: 100%, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	snap := r.e.Snapshot()
	fi, fl := faultOfOverlay(t, snap, iot.Overlay), faultOfOverlay(t, snap, lab.Overlay)
	tg := r.verifyKernel()

	for _, f := range []compiler.Fault{fi, fl} {
		for _, dir := range []compiler.Direction{compiler.Upload, compiler.Download} {
			if sent, drops, backlog, _ := r.queueSum(f, dir); sent+drops+backlog != 0 {
				t.Fatalf("fault %d queue %d has traffic before any: %d %d %d", f.ID, dir, sent, drops, backlog)
			}
		}
	}
	_, _, _, iotEpochs := r.queueSum(fi, compiler.Upload)
	_, _, _, labEpochs := r.queueSum(fl, compiler.Upload)
	if len(iotEpochs) != 1 || len(labEpochs) != 1 {
		t.Fatalf("the queues of one fault were made in more than one generation: %v %v", iotEpochs, labEpochs)
	}

	const n = 6
	res := testbed.MustPing(t, r.top.A, testbed.ServerAddr, n, 200*time.Millisecond)
	if res.Loss() != 0 {
		t.Fatalf("A lost packets to the server")
	}
	time.Sleep(time.Second) // what is queued has left
	up, upDrops, backlog, _ := r.queueSum(fi, compiler.Upload)
	down, downDrops, _, _ := r.queueSum(fi, compiler.Download)
	if up < n || down < n || upDrops != 0 || downDrops != 0 || backlog != 0 {
		t.Errorf("after %d pings the IoT queues hold up %d (drops %d), down %d (drops %d), backlog %d", n, up, upDrops, down, downDrops, backlog)
	}
	if want := r.classTotal(tg, classOfFault(t, tg, fi, compiler.Upload)); want != up {
		t.Errorf("the queue says %d packets sent, the class %d", up, want)
	}
	// isolation: the other fault's queues did not move
	for _, dir := range []compiler.Direction{compiler.Upload, compiler.Download} {
		if sent, drops, _, _ := r.queueSum(fl, dir); sent != 0 || drops != 0 {
			t.Errorf("the Lab queue (direction %d) moved for IoT traffic: sent %d, drops %d", dir, sent, drops)
		}
	}

	// the Lab fault drops everything: the queue counts exactly the packets that were sent
	res = testbed.MustPing(t, r.top.C, testbed.ServerAddr, n, 200*time.Millisecond)
	if res.Received != 0 {
		t.Fatalf("C received %d replies through a fault with 100%% loss", res.Received)
	}
	if sent, drops, _, _ := r.queueSum(fl, compiler.Upload); sent != 0 || drops != int64(res.Sent) {
		t.Errorf("the Lab upload queue: sent %d, dropped %d, want 0 and %d", sent, drops, res.Sent)
	}
	_, labDrops, _, _ := r.queueSum(fl, compiler.Upload)
	if sent, _, _, _ := r.queueSum(fi, compiler.Upload); sent != up {
		t.Errorf("the IoT queue moved for Lab traffic: %d, was %d", sent, up)
	}

	// a change of the parameters of the IoT fault keeps its leaf: counters and epoch stay
	r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 4ms, jitter: 1ms, loss: 0%, distribution: normal, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	if got, _, _, ep := r.queueSum(fi, compiler.Upload); got != up || !sameSet(ep, iotEpochs) {
		t.Errorf("a change in place: %d packets (was %d), epochs %v (were %v)", got, up, ep, iotEpochs)
	}
	// back to uniform: the table has to go, the leaf is made again, its counters start at zero in a
	// new epoch, and the Lab queue does not notice
	w := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 4ms, jitter: 1ms, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	got, _, _, ep := r.queueSum(fi, compiler.Upload)
	if got != 0 || sameSet(ep, iotEpochs) || !ep[int64(w.Generation)] {
		t.Errorf("a leaf made again: %d packets, epochs %v, the write was generation %d (old epochs %v)", got, ep, w.Generation, iotEpochs)
	}
	if _, d, _, ep := r.queueSum(fl, compiler.Upload); d != labDrops || !sameSet(ep, labEpochs) {
		t.Errorf("the Lab queue changed with the IoT one: drops %d (was %d), epochs %v (were %v)", d, labDrops, ep, labEpochs)
	}
	r.verifyKernel()
}

func sameSet(a, b map[int64]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// preload sends n echo requests back to back (ping -l) from the namespace and returns what came back.
func preload(t *testing.T, ns *testbed.Namespace, dst string, n int, wait time.Duration) testbed.PingResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, _ := ns.Run(ctx, "ping", "-n", "-q", "-l", strconv.Itoa(n), "-c", strconv.Itoa(n), "-i", "0.2", "-W", fmt.Sprintf("%d", int(wait.Seconds())+1), dst)
	res, err := testbed.ParsePing(out)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// P2-M8a-02: what a queue limit does with real traffic. A 600 ms fault holds every packet for 600 ms,
// so at a few thousand packets per second more than netem's default of 1000 are in the queue at once.
// The compiler's computed limit (delay x rate, here the 1 Gbit/s cap) must hold the whole burst; the
// explicit limit of 1000 drops the rest, and the drops are the fault's own: the packets sent are the
// packets received plus the drops of the queue (conservation).
func TestTheComputedQueueLimitHoldsABurstThatTheDefaultLimitOfNetemDrops(t *testing.T) {
	const burst = 4000
	r := newReal(t, nil)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	// the fault of the IoT network has no queue limit: the compiler computes it; the one of Lab names 1000
	iot := r.mustPut(admin, "target: {network: IoT}\nfault: {latency: 600ms, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	lab := r.mustPut(admin, "target: {network: Lab}\nfault: {latency: 600ms, queue_limit: 1000, destination: {cidr: "+testbed.ServerAddr+"/32}}")
	snap := r.e.Snapshot()
	fi, fl := faultOfOverlay(t, snap, iot.Overlay), faultOfOverlay(t, snap, lab.Overlay)
	tg := r.verifyKernel()
	limitOf := func(f compiler.Fault) int {
		c := classOfFault(t, tg, f, compiler.Upload)
		return c.Netem.Limit
	}
	computed, explicit := limitOf(fi), limitOf(fl)
	t.Logf("queue limits: computed %d packets, explicit %d", computed, explicit)
	if explicit != 1000 || computed < burst {
		t.Fatalf("the limits are %d (computed) and %d (named)", computed, explicit)
	}

	// The replies of a burst are counted by the queues, not by ping: the burst of replies overruns ping's
	// own receive buffer (about half of them are lost there on the 4000-packet burst, with no drop in the
	// kernel's queues), so what ping received says nothing about the fault.

	// IoT: the whole burst fits
	res := preload(t, r.top.A, testbed.ServerAddr, burst, 20*time.Second)
	time.Sleep(2 * time.Second)
	sent, drops, backlog, _ := r.queueSum(fi, compiler.Upload)
	dSent, dDrops, dBacklog, _ := r.queueSum(fi, compiler.Download)
	t.Logf("computed limit: ping sent %d, received %d; upload queue sent %d drops %d, download queue sent %d drops %d", res.Sent, res.Received, sent, drops, dSent, dDrops)
	// The upload queue is the one under test: it takes the whole burst and holds all of it. What it sends
	// out is released in a burst, and a burst of thousands reaches the receive queue of the next hop
	// (netdev_max_backlog, 1000 per CPU) faster than the CPU empties it when the environment is slow
	// (nested virtualisation, seen on the hosted runners): such packets are lost between the queues, not
	// in them. So the download queue holds what reaches it (no drop, nothing left) and cannot have more
	// than the upload queue sent; ping cannot have more than the download queue sent (P2-M8b-07).
	if sent != int64(res.Sent) || drops != 0 || dDrops != 0 || backlog != 0 || dBacklog != 0 {
		t.Errorf("a burst of %d packets through a limit of %d: upload sent %d dropped %d (backlog %d), download dropped %d (backlog %d)", burst, computed, sent, drops, backlog, dDrops, dBacklog)
	}
	if dSent > sent || res.Received > int(dSent) {
		t.Errorf("a burst of %d packets: the upload queue sent %d, the download queue %d, ping got %d", burst, sent, dSent, res.Received)
	}

	// Lab: netem's limit of 1000 drops what does not fit while the first 1000 wait
	res = preload(t, r.top.C, testbed.ServerAddr, burst, 20*time.Second)
	time.Sleep(2 * time.Second)
	sent, drops, _, _ = r.queueSum(fl, compiler.Upload)
	dSent, dDrops, _, _ = r.queueSum(fl, compiler.Download)
	t.Logf("limit 1000: ping sent %d, received %d; upload queue sent %d drops %d, download queue sent %d drops %d", res.Sent, res.Received, sent, drops, dSent, dDrops)
	if drops == 0 {
		t.Errorf("a burst of %d packets through a limit of 1000 lost nothing: the packets were sent more slowly than 1667 per second", burst)
	}
	// conservation: every packet of the burst reached the upload queue and left it or was dropped there;
	// of what left it, the download queue got at most what the echo server answered and the path let
	// through (see above), and left it or dropped it
	if sent+drops != int64(res.Sent) {
		t.Errorf("upload: sent %d + dropped %d != the %d packets of the burst", sent, drops, res.Sent)
	}
	if dSent+dDrops > sent {
		t.Errorf("download: sent %d + dropped %d are more than the %d packets that left the upload queue", dSent, dDrops, sent)
	}
	if res.Received > int(dSent) {
		t.Errorf("ping got %d replies, the download queue sent %d", res.Received, dSent)
	}
	r.verifyKernel()
}
