//go:build testbed

package apply_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The tc tree in the apply, on the real kernel (M8b). The simulator's tests (tcapply_test.go) say what
// the apply decides; these say what the kernel does with it: that the tree it builds verifies, that a
// re-apply changes nothing the kernel can tell, that a change of parameters keeps the qdisc and its
// queue (same seed, no loss), and that traffic whose fault id moves loses no packet.

// kernelTree reads the own tree of an interface of the gateway, with counters.
func (g *gw) kernelTree(dev string) *linux.NormTree {
	g.t.Helper()
	out, err := g.exec().Do(context.Background(), &executor.Read{Target: executor.Target{NS: g.ns()}, What: executor.ReadTC, Dev: dev})
	if err != nil {
		g.t.Fatal(err)
	}
	var t linux.NormTree
	if err := json.Unmarshal(out.Data[0], &t); err != nil {
		g.t.Fatal(err)
	}
	return t.Subtree("1:")
}

// seeds returns the seed of every netem qdisc of the interface by handle: the identity of the
// qdisc, which changes when it is created again.
func (g *gw) seeds(dev string) map[string]uint64 {
	m := map[string]uint64{}
	for _, q := range g.kernelTree(dev).Qdiscs {
		if q.Netem != nil {
			m[q.Handle] = q.Seed
		}
	}
	return m
}

func (g *gw) strictVerify(tg *compiler.Target) []apply.Mismatch {
	g.t.Helper()
	s, err := apply.ReadState(context.Background(), g.exec(), g.ns(), apply.WantOf(tg))
	if err != nil {
		g.t.Fatal(err)
	}
	return apply.Verify(tg, s)
}

// stream is a numbered UDP stream from a device to the server: the sender numbers its datagrams and
// sends one every 20 ms until it is told to stop, the receiver counts the distinct numbers it gets.
// Packet conservation is sent == received once the stream has stopped and what was queued has left.
// Unlike a ping run of a fixed length it lasts as long as the test needs, however long the apply
// takes (minutes on the emulated kernel).
type stream struct {
	t        *testing.T
	send     *testbed.Process
	recv     *testbed.Process
	stopFile string
}

func startStream(t *testing.T, from, to *testbed.Namespace, dst string, port int) *stream {
	t.Helper()
	st := &stream{t: t, stopFile: filepath.Join(t.TempDir(), "stop")}
	st.recv = to.Start("python3", "-c", `import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", `+itoa(port)+`))
seen, last = set(), -1
print("ready", flush=True)
while True:
    d, a = s.recvfrom(64)
    seen.add(int(d))
    if len(seen) != last:
        last = len(seen)
        print("received", last, flush=True)
`)
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(st.recv.Output(), "ready") {
		if time.Now().After(deadline) {
			t.Fatal("the receiver did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	st.send = from.Start("python3", "-c", `import socket, time, os
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
i = 0
while not os.path.exists("`+st.stopFile+`"):
    s.sendto(str(i).encode(), ("`+dst+`", `+itoa(port)+`))
    i += 1
    time.sleep(0.02)
print("sent", i, flush=True)
`)
	t.Cleanup(func() { st.send.Stop(); st.recv.Stop() })
	return st
}

// numberAfter returns the number that follows the last occurrence of word in out, -1 if none.
func numberAfter(out, word string) int {
	n := -1
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == word {
			if v, err := strconv.Atoi(f[1]); err == nil {
				n = v
			}
		}
	}
	return n
}

// received is the number of distinct datagrams the receiver has so far.
func (st *stream) received() int { return numberAfter(st.recv.Output(), "received") }

// waitFlowing waits until the receiver has seen at least n datagrams.
func (st *stream) waitFlowing(n int) {
	st.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for st.received() < n {
		if time.Now().After(deadline) {
			st.t.Fatalf("the stream does not flow: %d received\n%s", st.received(), st.recv.Output())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// finish stops the sender, lets what is queued leave (settle is longer than the longest delay of the
// test) and returns the datagrams sent and received.
func (st *stream) finish(settle time.Duration) (sent, received int) {
	st.t.Helper()
	if err := os.WriteFile(st.stopFile, nil, 0o644); err != nil {
		st.t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for numberAfter(st.send.Output(), "sent") < 0 {
		if time.Now().After(deadline) {
			st.t.Fatalf("the sender did not stop: %s", st.send.Output())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(settle)
	return numberAfter(st.send.Output(), "sent"), st.received()
}

func TestTheTreeOfAFaultIsAppliedVerifiedAndAReApplyChangesNothing(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	o := g.overlay(`{target: {device: dev-a}, fault: {upload: {latency: 120ms, jitter: 15ms, loss: 1%}, download: {latency: 30ms}}}`)
	tg := g.compileFaults(o)
	if tg.HasErrors() || tg.TC == nil {
		t.Fatalf("%+v", tg.Problems)
	}
	res := g.apply(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v\n%s", res.Mismatches, g.dump())
	}
	for _, dev := range tg.TC.Devs {
		if diff := linux.DiffTC(tg.TC.Norm(dev), g.kernelTree(dev)); len(diff) != 0 {
			t.Errorf("%s: %v", dev, diff)
		}
	}
	// the management interface and the ports carry nothing of it
	for _, dev := range []string{"mgmt0", "lan0", "lan1"} {
		if q := g.kernelTree(dev).Qdiscs; len(q) != 0 {
			t.Errorf("%s: %+v", dev, q)
		}
	}
	seeds := map[string]map[string]uint64{}
	for _, dev := range tg.TC.Devs {
		seeds[dev] = g.seeds(dev)
	}

	// the same target again: nothing to do for tc, and the kernel cannot tell
	tg2 := g.compileFaults(o)
	p, err := apply.Preview(context.Background(), g.exec(), g.ns(), tg2)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range p.Ops {
		if _, isTC := op.(*executor.TC); isTC {
			t.Errorf("a re-apply plans tc work: %v", p.Summary)
		}
	}
	if res := g.apply(tg2); len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	for _, dev := range tg.TC.Devs {
		if now := g.seeds(dev); fmt.Sprint(now) != fmt.Sprint(seeds[dev]) {
			t.Errorf("%s: a qdisc was created again: %v -> %v", dev, seeds[dev], now)
		}
	}
}

// What the plan promises (§4.3): changing the parameters of a 600 ms fault while packets are queued in
// it loses none of them. The kernel keeps the queue of a netem that is changed, and the packets that
// are in it leave at the time they were given.
func TestChangingAFaultOf600msUnderLoadLosesNoPacket(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	o := g.overlay(`{target: {device: dev-a}, fault: {upload: {latency: 600ms}}}`)
	tg := g.compileFaults(o)
	g.apply(tg)
	f := faultOf(t, tg, o, "")
	up := classOf(t, tg, f.ID, compiler.Upload)
	seed := g.seeds("wan0")[up.LeafHandle()]
	if seed == 0 {
		t.Fatalf("no netem leaf %s on wan0", up.LeafHandle())
	}

	st := startStream(t, g.top.A, g.top.Server, testbed.ServerAddr, 9300)
	st.waitFlowing(5)
	// the change comes while packets are queued: first longer, then shorter than what is queued
	for i, lat := range []string{"900ms", "50ms"} {
		changed := g.overlayChange(o, `{target: {device: dev-a}, fault: {upload: {latency: `+lat+`}}}`)
		tgc := g.compileFaults(changed)
		if faultOf(t, tgc, changed, "").ID != f.ID {
			t.Fatalf("change %d moved the id", i)
		}
		if r := g.apply(tgc); len(r.Mismatches) != 0 {
			t.Fatalf("%v", r.Mismatches)
		}
	}
	sent, got := st.finish(2 * time.Second)
	if sent == 0 || sent != got {
		t.Errorf("sent %d, received %d: the change lost packets\n%s", sent, got, g.top.GW.Must("tc", "-s", "qdisc", "show", "dev", "wan0"))
	}
	if now := g.seeds("wan0")[up.LeafHandle()]; now != seed {
		t.Errorf("the leaf was created again (seed %d, was %d)", now, seed)
	}
	for _, q := range g.kernelTree("wan0").Qdiscs {
		if q.Netem != nil && q.Stats != nil && q.Stats.Drops != 0 {
			t.Errorf("qdisc %s dropped %d packets: no loss is configured, so the change dropped them", q.Handle, q.Stats.Drops)
		}
	}
}

// Make before break on the real kernel: a device's fault is replaced by another one (its id moves)
// while it sends. No packet is lost, the old classes stay while packets may be queued in them and go
// when their time has come.
func TestMovingADeviceToANewFaultIdLosesNoPacketAndTheOldClassesGoLater(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	oOld := g.overlay(`{target: {device: dev-a}, fault: {upload: {latency: 400ms}}}`)
	tgOld := g.compileFaults(oOld)
	g.applyRetiring(tgOld)
	fOld := faultOf(t, tgOld, oOld, "")
	oldUp := classOf(t, tgOld, fOld.ID, compiler.Upload)

	st := startStream(t, g.top.A, g.top.Server, testbed.ServerAddr, 9301)
	st.waitFlowing(5)
	if got := classPackets(t, g.top.GW, "wan0", oldUp); got == 0 {
		t.Error("the old class carried no packet before the move")
	}

	oNew := g.overlay(`{target: {device: dev-a}, fault: {upload: {latency: 100ms}}}`)
	tgNew := g.compileFaults(oNew)
	fNew := faultOf(t, tgNew, oNew, "")
	if fNew.ID == fOld.ID {
		t.Fatalf("the new fault took the id %d of the old one", fOld.ID)
	}
	newUp := classOf(t, tgNew, fNew.ID, compiler.Upload)
	if r := g.applyRetiring(tgNew); len(r.Mismatches) != 0 {
		t.Fatalf("%v", r.Mismatches)
	}
	// the old class is still there, and the retirer knows it: the upload class of the one fault, on
	// every interface the tree is on
	if pending := g.ret.Pending(); len(pending) != 3 {
		t.Errorf("%d classes wait for their deletion, want 3 (one per interface): %+v", len(pending), pending)
	}
	// the stream goes on, now through the new class; nothing reaches the old one any more
	waitClassPackets(t, g.top.GW, "wan0", newUp, time.Minute, "the stream was never classified into the new class")
	oldNow := classPackets(t, g.top.GW, "wan0", oldUp)
	time.Sleep(time.Second)
	if later := classPackets(t, g.top.GW, "wan0", oldUp); later != oldNow {
		t.Errorf("the old class still takes packets after the switch: %d -> %d", oldNow, later)
	}
	sent, got := st.finish(2 * time.Second)
	if sent == 0 || sent != got {
		t.Errorf("sent %d, received %d: the move lost packets\n%s", sent, got, g.top.GW.Must("tc", "-s", "class", "show", "dev", "wan0"))
	}
	// no packet lost its classification on the way: every datagram of the stream was counted by the old
	// or the new class (the classes count at enqueue, and nothing but the stream is A's traffic
	// through them). The default class sees other traffic of the gateway now and then, so it says
	// nothing.
	if inOld, inNew := classPackets(t, g.top.GW, "wan0", oldUp), classPackets(t, g.top.GW, "wan0", newUp); inOld+inNew != int64(sent) {
		t.Errorf("the stream sent %d datagrams, the old class counted %d and the new one %d", sent, inOld, inNew)
	}

	// the time: the old fault's 400 ms plus a second have long passed (the apply took longer on an
	// emulated kernel); the retirer deletes what waits, and the unit tests pin that it waits for the time
	next, ok := g.ret.Next()
	if !ok {
		t.Fatal("nothing waits")
	}
	time.Sleep(next + 100*time.Millisecond)
	n2, err := g.ret.Reap(context.Background(), g.exec(), g.ns())
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 3 {
		t.Errorf("reaped %d classes, want 3: %+v\n%s", n2, g.ret.Pending(), g.top.GW.Must("tc", "-s", "qdisc", "show", "dev", "wan0"))
	}
	if mm := g.strictVerify(tgNew); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
	out := g.top.GW.Must("tc", "class", "show", "dev", "wan0")
	if strings.Contains(out, oldUp.ClassID()+" ") {
		t.Errorf("the old class is still there:\n%s", out)
	}
}

// Without any fault the tree goes (after its time), and the interface has the queue it had before.
func TestWithoutFaultsTheTreeGoesAndTheHostsQueueComesBack(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	o := g.overlay(`{target: {device: dev-a}, fault: {latency: 20ms}}`)
	g.applyRetiring(g.compileFaults(o))
	before := g.top.GW.Must("tc", "qdisc", "show", "dev", "br-iot")
	if !strings.Contains(before, "htb 1:") {
		t.Fatalf("no tree:\n%s", before)
	}
	tgNone := g.compileFaults()
	if tgNone.TC != nil {
		t.Fatal("a tree without faults")
	}
	ret := apply.NewRetirer(&clock.Real{})
	g.ret = ret
	g.applyRetiring(tgNone)
	next, ok := ret.Next()
	if !ok {
		t.Fatal("nothing retires")
	}
	time.Sleep(next + 100*time.Millisecond)
	if n, err := ret.Reap(context.Background(), g.exec(), g.ns()); err != nil || n != 3 {
		t.Fatalf("reaped %d (%v)", n, err)
	}
	for _, dev := range []string{"br-iot", "br-lab", "wan0"} {
		out := g.top.GW.Must("tc", "qdisc", "show", "dev", dev)
		if strings.Contains(out, "htb") || strings.Contains(out, "netem") {
			t.Errorf("%s:\n%s", dev, out)
		}
	}
	if mm := g.strictVerify(tgNone); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// overlayChange is the same overlay with other parameters: the key, and so the fault id, stay.
func (g *gw) overlayChange(o model.Overlay, body string) model.Overlay {
	n := g.overlay(body)
	o.Target, o.Fault = n.Target, n.Fault
	return o
}

// outsideShare is the share of the round trips that are further than tol from want.
func outsideShare(r testbed.PingResult, want, tol time.Duration) float64 {
	if len(r.RTTs) == 0 {
		return 0
	}
	n := 0
	for _, d := range r.RTTs {
		if d < want-tol || d > want+tol {
			n++
		}
	}
	return float64(n) / float64(len(r.RTTs))
}

// P2-M8b-02: a netem that has a normal table keeps it through every change that names
// none, so a fault that goes back to a uniform jitter makes the leaf again. The delay of the traffic
// follows: with a normal table a fifth of the round trips are further from the delay than the jitter,
// uniform ones are never.
func TestTheDistributionOfAFaultFollowsAChangeBackToUniform(t *testing.T) {
	g := newGateway(t)
	g.withDevices()
	const delay, jitter = 100 * time.Millisecond, 20 * time.Millisecond
	o := g.overlay(`{target: {device: dev-a}, fault: {upload: {latency: 100ms, jitter: 20ms, distribution: normal}}}`)
	tg := g.compileFaults(o)
	g.applyRetiring(tg)
	f := faultOf(t, tg, o, "")
	leaf := classOf(t, tg, f.ID, compiler.Upload).LeafHandle()
	seed := g.seeds("wan0")[leaf]

	measure := func() float64 {
		r := testbed.MustPing(t, g.top.A, testbed.ServerAddr, 150, 30*time.Millisecond)
		if r.Received < 140 {
			t.Fatalf("received %d of %d", r.Received, r.Sent)
		}
		return outsideShare(r, delay, jitter+5*time.Millisecond)
	}
	if s := measure(); s < 0.08 {
		t.Errorf("a normal distribution with sigma = jitter should put about a fifth of the round trips outside delay ± (jitter + 5 ms), %.0f%% were", s*100)
	}

	// the same fault, uniform: the apply has to make the leaf again, because the table survives a change
	changed := g.overlayChange(o, `{target: {device: dev-a}, fault: {upload: {latency: 100ms, jitter: 20ms}}}`)
	tg2 := g.compileFaults(changed)
	if res := g.applyRetiring(tg2); len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if now := g.seeds("wan0")[leaf]; now == seed || now == 0 {
		t.Fatalf("the leaf was not made again (seed %d, was %d)", now, seed)
	}
	share := measure()
	if testbed.Accurate() && share > 0 {
		// flakiness policy of plan §4.3: repeat once, a second failure fails
		if share = measure(); share > 0 {
			t.Errorf("%.0f%% of the round trips of a uniform jitter of ±20 ms are outside delay ± 25 ms", share*100)
		}
	} else if !testbed.Accurate() {
		t.Logf("emulated: %.0f%% of the round trips outside delay ± 25 ms (not asserted)", share*100)
	}
}
