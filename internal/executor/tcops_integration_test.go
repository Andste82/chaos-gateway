//go:build testbed

package executor_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// tc operations on the kernel (M8b). These tests pin what the fault engine relies on, as measured
// in the persistent VM before any code was written around it (kernel 6.8.0-142, iproute2 6.19; the
// table is in docs/development.md "tc operations on the kernel"): which attributes survive a
// `change`, what `replace` keeps of a qdisc and of a class, what a deletion drops, what the kernel
// answers when an object is not there. They run on dummy interfaces and need no accuracy.

// tcDev creates a dummy interface in the gateway namespace, assigns it, and puts the tree of the
// compiler on it: the HTB root 1: with the default class 1:1.
func (g *gateway) tcDev(name string) {
	g.t.Helper()
	g.top.GW.Must("ip", "link", "add", name, "type", "dummy")
	g.top.GW.Must("ip", "link", "set", name, "up")
	g.top.GW.Must("ip", "addr", "add", "10.9.0.1/24", "dev", name)
	g.top.GW.Must("ip", "neigh", "add", "10.9.0.2", "lladdr", "02:00:00:00:00:02", "dev", name, "nud", "permanent")
	g.must(&executor.AssignInterfaces{Devs: []string{"wan0", "lan0", "lan1", "br-lan0", "br-lan1", name}})
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{
		{Object: "qdisc", Action: "add", Dev: name, Parent: "root", Handle: "1:", Args: []string{"htb", "default", "1"}},
		htbClass(name, "1:1", "replace"),
	}})
}

func htbClass(dev, id, action string) executor.TCEntry {
	return executor.TCEntry{Object: "class", Action: action, Dev: dev, Parent: "1:", ClassID: id, Args: []string{"htb", "rate", "10gbit", "quantum", "60000"}}
}

func leaf(dev string, minor int, action string, netem ...string) executor.TCEntry {
	return executor.TCEntry{Object: "qdisc", Action: action, Dev: dev, Parent: fmt.Sprintf("1:%x", minor), Handle: fmt.Sprintf("%x:", minor), Args: append([]string{"netem"}, netem...)}
}

func fwFilter(dev string, mark uint32, minor int, action string) executor.TCEntry {
	return executor.TCEntry{Object: "filter", Action: action, Dev: dev, Parent: "1:", Handle: fmt.Sprintf("0x%05x/0x1fff0", mark),
		Args: []string{"protocol", "ip", "prio", "1", "fw", "flowid", fmt.Sprintf("1:%x", minor)}}
}

// withClass is the three entries of one (id, direction): class, leaf, filter.
func withClass(dev string, minor int, mark uint32, netem ...string) []executor.TCEntry {
	return []executor.TCEntry{htbClass(dev, fmt.Sprintf("1:%x", minor), "replace"), leaf(dev, minor, "replace", netem...), fwFilter(dev, mark, minor, "replace")}
}

func (g *gateway) do(entries ...executor.TCEntry) error {
	g.t.Helper()
	_, err := g.c.Do(context.Background(), &executor.TC{Target: tgt(g.ns), Entries: entries})
	return err
}

func (g *gateway) tcTree(dev string) *linux.NormTree {
	g.t.Helper()
	tree := read[linux.NormTree](g.t, g, executor.Read{What: executor.ReadTC, Dev: dev})
	return &tree
}

func (g *gateway) netem(dev string, minor int) (linux.NormQdisc, bool) {
	for _, q := range g.tcTree(dev).Qdiscs {
		if q.Handle == fmt.Sprintf("%x:", minor) {
			return q, true
		}
	}
	return linux.NormQdisc{}, false
}

const sendScript = `import socket, sys, time
mark, n, pps, src = int(sys.argv[1], 0), int(sys.argv[2]), float(sys.argv[3]), sys.argv[4]
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_MARK, mark)
s.bind((src, 0))
t0 = time.time()
for i in range(n):
    s.sendto(b"x" * 100, ("10.9.0.2", 9))
    d = t0 + (i + 1) / pps - time.time()
    if d > 0:
        time.sleep(d)
`

// send puts n packets with the given mark on the dummy interface's address, pps packets a second.
func (g *gateway) send(mark uint32, n int, pps int) {
	g.t.Helper()
	g.top.GW.Must("python3", "-c", sendScript, fmt.Sprintf("%#x", mark), fmt.Sprint(n), fmt.Sprint(pps), "10.9.0.1")
}

// eventually polls until cond holds, for up to d.
func eventually(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func remoteErr(err error) *executor.RemoteError {
	var re *executor.RemoteError
	if errors.As(err, &re) {
		return re
	}
	return nil
}

// A complete netem change: every attribute the compiler emits, the way it emits it.
func fullNetem(limit string, delay, jitter string, loss string, extra ...string) []string {
	a := []string{"limit", limit, "delay", delay, jitter, "0%", "loss", "random", loss, "0%", "reorder", "0%", "0%", "duplicate", "0%", "0%", "corrupt", "0%", "0%", "rate", "0bit"}
	return append(a, extra...)
}

// ---- what a change keeps ----------------------------------------------------------------------------

// `tc qdisc change` sends the attributes of netem's base structure (limit, delay, jitter, loss,
// duplicate and the reorder gap) every time, with their defaults for what is not given, and the
// optional attributes (the three correlations, reorder, corrupt, rate, the loss model, the
// distribution table) only when they are given: those stay as they were. The listing hides the
// correlation of a loss or a duplicate while its probability is 0, so a kept correlation shows only
// next to a probability. That is why the compiler always writes every attribute and every
// correlation (plan §3.2); the table is what the kernel did for one attribute at a time (VM session
// of M8b, kernel 6.8.0-142, iproute2 6.19).
func TestAChangeKeepsWhatItIsNotGivenForTheOptionalAttributes(t *testing.T) {
	g := startGateway(t)
	g.tcDev("dtc0")
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{htbClass("dtc0", "1:24", "replace")}})
	full := []string{"limit", "5000", "delay", "100ms", "20ms", "30%", "distribution", "normal", "loss", "random", "10%", "50%",
		"reorder", "25%", "40%", "duplicate", "5%", "30%", "corrupt", "2%", "20%", "rate", "5mbit"}
	set := func(netem ...string) linux.NetemSpec {
		t.Helper()
		if err := g.do(leaf("dtc0", 0x24, "replace", netem...)); err != nil {
			t.Fatalf("%v: %v", netem, err)
		}
		q, ok := g.netem("dtc0", 0x24)
		if !ok {
			t.Fatal("no leaf")
		}
		return *q.Netem
	}
	wantFull := linux.NetemSpec{Limit: 5000, Delay: 0.1, Jitter: 0.02, DelayCorr: 0.3, Loss: 0.1, LossCorr: 0.5, Reorder: 0.25, ReorderCorr: 0.4, Gap: 1,
		Duplicate: 0.05, DuplicateCorr: 0.3, Corrupt: 0.02, CorruptCorr: 0.2, Rate: 625000}
	if have := set(full...); !reflect.DeepEqual(have, wantFull) {
		t.Fatalf("the full set:\n have %s\n want %s", have, wantFull)
	}
	kept := linux.NetemSpec{Limit: 1000, Reorder: 0.25, ReorderCorr: 0.4, Corrupt: 0.02, CorruptCorr: 0.2, Rate: 625000}
	with := func(f func(n *linux.NetemSpec)) linux.NetemSpec { n := kept; f(&n); return n }
	for name, c := range map[string]struct {
		args []string
		want linux.NetemSpec
	}{
		// delay is rewritten, jitter reset, the delay's correlation kept; loss, duplicate and the gap reset
		"delay only": {[]string{"delay", "10ms"}, with(func(n *linux.NetemSpec) { n.Delay, n.DelayCorr = 0.01, 0.3 })},
		// the correlation of the loss is kept when only the loss is given
		"loss without a correlation":      {[]string{"loss", "random", "1%"}, with(func(n *linux.NetemSpec) { n.Loss, n.LossCorr = 0.01, 0.5 })},
		"loss with a correlation of zero": {[]string{"loss", "random", "1%", "0%"}, with(func(n *linux.NetemSpec) { n.Loss = 0.01 })},
		"a loss model": {[]string{"loss", "gemodel", "1%", "10%", "70%", "0.1%"}, with(func(n *linux.NetemSpec) {
			n.Gemodel = &linux.GemodelSpec{P: 0.01, R: 0.1, OneMinusH: 0.7, OneMinusK: 0.001}
		})},
		"reorder 0":        {[]string{"reorder", "0%", "0%"}, with(func(n *linux.NetemSpec) { n.Reorder, n.ReorderCorr = 0, 0 })},
		"corrupt 0":        {[]string{"corrupt", "0%", "0%"}, with(func(n *linux.NetemSpec) { n.Corrupt, n.CorruptCorr = 0, 0 })},
		"rate 0bit":        {[]string{"rate", "0bit"}, with(func(n *linux.NetemSpec) { n.Rate = 0 })},
		"limit only":       {[]string{"limit", "100"}, with(func(n *linux.NetemSpec) { n.Limit = 100 })},
		"duplicate 0 only": {[]string{"duplicate", "0%"}, kept},
		// gap is part of the base structure: the reorder probability that stays has a gap of 0 and does nothing
	} {
		set(full...)
		if have := set(c.args...); !reflect.DeepEqual(have, c.want) {
			t.Errorf("%s:\n have %s\n want %s", name, have, c.want)
		}
	}
	// the complete neutral set is the only change that resets everything
	if n := set(fullNetem("1000", "0ms", "0ms", "0%")...); !reflect.DeepEqual(n, linux.NetemSpec{Limit: 1000}) {
		t.Errorf("a complete neutral set leaves %s", n)
	}
	// ... also after a loss model: the random loss of the complete set takes the model out
	set("limit", "1000", "delay", "0ms", "0ms", "0%", "loss", "gemodel", "1%", "10%", "70%", "0.1%", "reorder", "0%", "0%", "duplicate", "0%", "0%", "corrupt", "0%", "0%", "rate", "0bit")
	if n := set(fullNetem("1000", "0ms", "0ms", "0%")...); !reflect.DeepEqual(n, linux.NetemSpec{Limit: 1000}) {
		t.Errorf("the model stays after a complete set: %s", n)
	}
}

// ---- what replace keeps ---------------------------------------------------------------------------------

// `replace` of a netem leaf of the same kind is a change in place: the queue (packets that wait for
// their delay), the counters and the seed stay, packets already queued keep the release time they
// got, and a lower limit does not shorten the queue. `replace` of an HTB class keeps its leaf and its
// counters. This is the in-place update of plan §3.2.
func TestReplacingALeafOrAClassKeepsTheQueueAndTheCounters(t *testing.T) {
	g := startGateway(t)
	g.tcDev("dtc0")
	// a delay far longer than the steps below take, even in an emulated VM
	const old = "25s"
	g.must(&executor.TC{Target: tgt(g.ns), Entries: withClass("dtc0", 0x24, 0xa0, fullNetem("1000", old, "0ms", "0%")...)})
	before, _ := g.netem("dtc0", 0x24)
	if before.Seed == 0 {
		t.Fatal("no seed printed: the tool is too old for this test")
	}
	g.send(0xa0, 30, 200)
	sent := time.Now()
	// a change of the delay, and a lower limit than the queue holds; then the same for the class
	// (identical parameters, as every apply writes them) and a change of it
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{leaf("dtc0", 0x24, "replace", fullNetem("10", "100ms", "0ms", "0%")...)}})
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{htbClass("dtc0", "1:24", "replace"), htbClass("dtc0", "1:24", "change")}})
	// the new delay is a tenth of a second: packets that were released by it would be gone by now
	time.Sleep(time.Second)
	tree := g.tcTree("dtc0")
	var q linux.NormQdisc
	var cl linux.NormClass
	for _, x := range tree.Qdiscs {
		if x.Handle == "24:" {
			q = x
		}
	}
	for _, c := range tree.Classes {
		if c.ID == "1:24" {
			cl = c
		}
	}
	if elapsed := time.Since(sent); elapsed > 20*time.Second {
		t.Fatalf("the steps took %s, nearly as long as the %s the packets are held for: the measurement below would mean nothing", elapsed, old)
	}
	if q.Seed != before.Seed {
		t.Errorf("the qdisc was created again: seed %d, was %d", q.Seed, before.Seed)
	}
	if q.Netem.Delay != 0.1 || q.Netem.Limit != 10 {
		t.Errorf("the change did not take: %+v", q.Netem)
	}
	if q.Stats.Qlen != 30 || q.Stats.Drops != 0 {
		t.Errorf("replace touched the queue, or the packets left at the new delay instead of the one they were given: %+v", q.Stats)
	}
	if cl.Leaf != "24:" || cl.Stats == nil || cl.Stats.Qlen != 30 {
		t.Errorf("the class lost its leaf or its queue: %+v %+v", cl, cl.Stats)
	}
	eventually(t, 60*time.Second, "the queue to drain", func() bool { q, _ = g.netem("dtc0", 0x24); return q.Stats.Qlen == 0 })
	if q.Stats.Packets != 30 || q.Stats.Drops != 0 {
		t.Errorf("30 packets queued, %+v delivered", q.Stats)
	}
	// packets that arrive over the lower limit are the qdisc's own drops: 3 s of delay and a limit of
	// 10, 25 arrive at once, 10 wait and 15 are dropped; the counters go on from where they were
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{leaf("dtc0", 0x24, "replace", fullNetem("10", "3s", "0ms", "0%")...)}})
	g.send(0xa0, 25, 2000)
	eventually(t, 60*time.Second, "the burst to be over", func() bool { q, _ = g.netem("dtc0", 0x24); return q.Stats.Qlen == 0 })
	if q.Stats.Drops != 15 || q.Stats.Packets != 40 || q.Seed != before.Seed {
		t.Errorf("25 packets into a limit of 10: %+v seed %d", q.Stats, q.Seed)
	}
}

// The kind of a qdisc cannot be changed under its handle; the way to another kind is deleting it,
// which drops what is queued, and creating one: the counters start again and so does the seed.
func TestDeletingALeafDropsItsQueueAndAKindCannotBeChangedInPlace(t *testing.T) {
	g := startGateway(t)
	g.tcDev("dtc0")
	g.must(&executor.TC{Target: tgt(g.ns), Entries: withClass("dtc0", 0x24, 0xa0, fullNetem("1000", "4s", "0ms", "0%")...)})
	g.send(0xa0, 20, 500)
	before, _ := g.netem("dtc0", 0x24)
	if before.Stats.Qlen != 20 {
		t.Fatalf("%+v", before.Stats)
	}
	// replace with another kind under the same handle: refused
	err := g.do(executor.TCEntry{Object: "qdisc", Action: "replace", Dev: "dtc0", Parent: "1:24", Handle: "24:", Args: []string{"pfifo", "limit", "100"}})
	if re := remoteErr(err); re == nil || !strings.Contains(re.Message, "Invalid qdisc name") {
		t.Errorf("a change of kind: %v", err)
	}
	if q, _ := g.netem("dtc0", 0x24); q.Stats.Qlen != 20 {
		t.Errorf("the refused change touched the queue: %+v", q.Stats)
	}
	// delete and create again
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{
		{Object: "qdisc", Action: "delete", Dev: "dtc0", Parent: "1:24", Handle: "24:"},
		leaf("dtc0", 0x24, "add", fullNetem("1000", "4s", "0ms", "0%")...),
	}})
	after, ok := g.netem("dtc0", 0x24)
	if !ok || after.Seed == before.Seed || after.Stats.Qlen != 0 || after.Stats.Packets != 0 || after.Stats.Drops != 0 {
		t.Errorf("the new qdisc starts again: %+v seed %d (was %d)", after.Stats, after.Seed, before.Seed)
	}
	time.Sleep(2 * time.Second)
	if q, _ := g.netem("dtc0", 0x24); q.Stats.Packets != 0 {
		t.Errorf("packets of the deleted queue came out of the new one: %+v", q.Stats)
	}
}

// ---- deleting ----------------------------------------------------------------------------------------------

// A class that a filter selects cannot be deleted ("HTB class in use"): the filter goes first. The
// class takes its leaf with it. A deletion of what is gone is a success, in any combination.
func TestDeleteOrderAndDeletionsOfWhatIsGone(t *testing.T) {
	g := startGateway(t)
	g.tcDev("dtc0")
	g.must(&executor.TC{Target: tgt(g.ns), Entries: withClass("dtc0", 0x24, 0xa0, fullNetem("1000", "10ms", "0ms", "0%")...)})
	del := func(es ...executor.TCEntry) error { return g.do(es...) }
	delClass := executor.TCEntry{Object: "class", Action: "delete", Dev: "dtc0", ClassID: "1:24"}
	delFilter := executor.TCEntry{Object: "filter", Action: "delete", Dev: "dtc0", Parent: "1:", Handle: "0x000a0/0x1fff0", Args: []string{"protocol", "ip", "prio", "1", "fw"}}
	delLeaf := executor.TCEntry{Object: "qdisc", Action: "delete", Dev: "dtc0", Parent: "1:24", Handle: "24:"}

	err := del(delClass)
	if re := remoteErr(err); re == nil || !strings.Contains(re.Message, "HTB class in use") {
		t.Fatalf("a class with a filter: %v", err)
	}
	if err := del(delFilter, delClass); err != nil {
		t.Fatalf("filter first, then the class: %v", err)
	}
	tree := g.tcTree("dtc0")
	if len(tree.Classes) != 1 || len(tree.Qdiscs) != 1 || len(tree.Filters) != 0 {
		t.Errorf("the class took its leaf with it: %s", strings.Join(tree.Lines(), "\n"))
	}
	// everything is gone: each deletion alone, and all of them together, still succeed
	for name, es := range map[string][]executor.TCEntry{
		"class": {delClass}, "filter": {delFilter}, "leaf": {delLeaf},
		"all":  {delFilter, delLeaf, delClass},
		"root": {{Object: "qdisc", Action: "delete", Dev: "dtc0", Parent: "root", Handle: "1:"}, {Object: "qdisc", Action: "delete", Dev: "dtc0", Parent: "root", Handle: "1:"}},
	} {
		if err := del(es...); err != nil {
			t.Errorf("%s of what is gone: %v", name, err)
		}
	}
	if tree := g.tcTree("dtc0"); len(tree.Classes) != 0 || len(tree.Qdiscs) != 1 || tree.Qdiscs[0].Kind == "htb" {
		t.Errorf("after the root's deletion the default qdisc is back: %s", strings.Join(tree.Lines(), "\n"))
	}
	// a failure in the middle of a run of deletions is a failure, and the lines behind it still ran
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{{Object: "qdisc", Action: "add", Dev: "dtc0", Parent: "root", Handle: "1:", Args: []string{"htb", "default", "1"}}, htbClass("dtc0", "1:1", "replace")}})
	g.must(&executor.TC{Target: tgt(g.ns), Entries: withClass("dtc0", 0x24, 0xa0, fullNetem("1000", "10ms", "0ms", "0%")...)})
	g.must(&executor.TC{Target: tgt(g.ns), Entries: withClass("dtc0", 0x26, 0xb0, fullNetem("1000", "10ms", "0ms", "0%")...)})
	delClass26 := executor.TCEntry{Object: "class", Action: "delete", Dev: "dtc0", ClassID: "1:26"}
	delFilter26 := executor.TCEntry{Object: "filter", Action: "delete", Dev: "dtc0", Parent: "1:", Handle: "0x000b0/0x1fff0", Args: []string{"protocol", "ip", "prio", "1", "fw"}}
	if err := del(delClass, delFilter26, delClass26); err == nil {
		t.Errorf("a class that is still selected was deleted without an error")
	}
	if tree := g.tcTree("dtc0"); len(tree.Classes) != 2 || len(tree.Filters) != 1 {
		t.Errorf("1:24 is still selected and stays; the lines behind its failure removed filter and class 26:\n%s", strings.Join(tree.Lines(), "\n"))
	}
}

// ---- the root ------------------------------------------------------------------------------------------------

// An HTB root cannot be changed once it exists, and `replace` is the same operation to the kernel, so a
// root that is in place is left alone. Over a root the host put there (an mq with its queues, here), `add`
// is refused and `replace` takes it over; deleting our root brings the default qdisc back. The
// executor never names the host's handles: it cannot delete the mq.
func TestTheRootCannotBeChangedAndAForeignRootIsReplacedNotAdded(t *testing.T) {
	g := startGateway(t)
	g.top.GW.Must("ip", "link", "add", "dmq0", "numtxqueues", "4", "numrxqueues", "4", "type", "veth", "peer", "name", "dmq1")
	g.top.GW.Must("ip", "link", "set", "dmq0", "up")
	g.must(&executor.AssignInterfaces{Devs: []string{"wan0", "dmq0"}})
	g.top.GW.Must("tc", "qdisc", "replace", "dev", "dmq0", "root", "handle", "8001:", "mq")
	root := func(action string, default_ string) executor.TCEntry {
		return executor.TCEntry{Object: "qdisc", Action: action, Dev: "dmq0", Parent: "root", Handle: "1:", Args: []string{"htb", "default", default_}}
	}
	if err := g.do(root("add", "1")); err == nil || !strings.Contains(err.Error(), "NLM_F_REPLACE needed to override") {
		t.Fatalf("add over the host's root: %v", err)
	}
	if err := g.do(root("replace", "1")); err != nil {
		t.Fatalf("replace over the host's root: %v", err)
	}
	if tree := g.tcTree("dmq0"); tree.Qdiscs[0].Kind != "htb" || tree.Qdiscs[0].Handle != "1:" || len(tree.Qdiscs) != 1 {
		t.Errorf("our root is in: %s", strings.Join(tree.Lines(), "\n"))
	}
	for _, e := range []executor.TCEntry{root("replace", "1"), root("change", "1"), root("replace", "2"), root("add", "1")} {
		err := g.do(e)
		if e.Action == "replace" && e.Args[2] == "1" {
			// the kernel refuses even an identical replace of an HTB root: the apply leaves a root it finds alone
			if err == nil || !strings.Contains(err.Error(), "Change operation not supported by specified qdisc") {
				t.Errorf("%s of an HTB root in place: %v", e.Action, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s %v of an HTB root in place was accepted", e.Action, e.Args)
		}
	}
	if err := g.do(executor.TCEntry{Object: "qdisc", Action: "delete", Dev: "dmq0", Parent: "root", Handle: "1:"}); err != nil {
		t.Fatal(err)
	}
	if tree := g.tcTree("dmq0"); tree.Qdiscs[0].Kind == "htb" || tree.Qdiscs[0].Kind == "mq" {
		t.Errorf("the default qdisc is back: %s", strings.Join(tree.Lines(), "\n"))
	}
	// the host's qdisc is out of reach of the operation
	g.top.GW.Must("tc", "qdisc", "replace", "dev", "dmq0", "root", "handle", "8001:", "mq")
	_, err := g.c.Do(context.Background(), &executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{{Object: "qdisc", Action: "delete", Dev: "dmq0", Parent: "root", Handle: "8001:"}}})
	if err == nil {
		t.Error("the executor deleted the host's mq")
	}
	if out := g.top.GW.Must("tc", "qdisc", "show", "dev", "dmq0"); !strings.Contains(out, "mq 8001:") {
		t.Errorf("the mq is gone: %s", out)
	}
}

// ---- the distribution ------------------------------------------------------------------------------------

// The distribution table of a netem qdisc is not shown by tc and is kept through every change and
// replace that does not name one: a fault that goes back to a uniform delay needs the qdisc to be created
// again (the apply makes the leaf again). Measured as the share of round trips outside the band delay +- jitter (plus a margin
// for noise): a uniform jitter has none, a normal one about a third.
func TestADistributionSurvivesAChangeAndOnlyANewQdiscDropsIt(t *testing.T) {
	bed := testbed.New(t)
	c, s := bed.Add("dc"), bed.Add("ds")
	c.Must("ip", "link", "add", "dv0", "type", "veth", "peer", "name", "dv1", "netns", s.Name)
	for _, p := range []struct {
		ns   *testbed.Namespace
		dev  string
		addr string
	}{{c, "dv0", "10.8.0.1/24"}, {s, "dv1", "10.8.0.2/24"}} {
		p.ns.Must("ip", "addr", "add", p.addr, "dev", p.dev)
		p.ns.Must("ip", "link", "set", p.dev, "up")
	}
	cl, _ := startExecutor(t)
	if _, err := cl.Do(context.Background(), &executor.AssignInterfaces{Devs: []string{"dv0"}}); err != nil {
		t.Fatal(err)
	}
	do := func(es ...executor.TCEntry) {
		t.Helper()
		if _, err := cl.Do(context.Background(), &executor.TC{Target: tgt(c.Name), Entries: es}); err != nil {
			t.Fatal(err)
		}
	}
	root := executor.TCEntry{Object: "qdisc", Action: "add", Dev: "dv0", Parent: "root", Handle: "1:", Args: []string{"htb", "default", "24"}}
	class := htbClass("dv0", "1:24", "replace")
	withDist := func(action string, dist ...string) executor.TCEntry {
		a := []string{"limit", "1000", "delay", "200ms", "100ms", "0%"}
		if len(dist) > 0 {
			a = append(a, "distribution", dist[0])
		}
		a = append(a, "loss", "random", "0%", "0%", "reorder", "0%", "0%", "duplicate", "0%", "0%", "corrupt", "0%", "0%", "rate", "0bit")
		return leaf("dv0", 0x24, action, a...)
	}
	// the share of 150 echoes that took longer than delay + jitter (+ 10 ms of noise) or less than
	// delay - jitter (- 10 ms); the traffic is the default class, so it goes through the leaf
	outside := func() float64 {
		t.Helper()
		r := testbed.MustPing(t, c, "10.8.0.2", 150, 20*time.Millisecond)
		n := 0
		for _, rtt := range r.RTTs {
			if rtt < 90*time.Millisecond || rtt > 310*time.Millisecond {
				n++
			}
		}
		if r.Received < 140 {
			t.Fatalf("%d of 150 echoes answered", r.Received)
		}
		return float64(n) / float64(r.Received)
	}
	// a statistical assertion that fails is repeated once, and only a second failure fails the test (plan §4.3)
	twice := func(what string, ok func(share float64) bool) {
		t.Helper()
		var shares []float64
		for i := 0; i < 2; i++ {
			sh := outside()
			if ok(sh) {
				return
			}
			shares = append(shares, sh)
		}
		t.Errorf("%s: shares outside the band %v", what, shares)
	}
	do(root, class, withDist("add", "normal"))
	twice("a normal distribution has a third of its samples outside the band", func(s float64) bool { return s > 0.15 })
	do(withDist("change"))
	twice("a change without a distribution keeps the table", func(s float64) bool { return s > 0.15 })
	do(withDist("replace"))
	twice("a replace without a distribution keeps the table", func(s float64) bool { return s > 0.15 })
	do(withDist("replace", "pareto"))
	twice("another table replaces it", func(s float64) bool { return s > 0.02 })
	do(executor.TCEntry{Object: "qdisc", Action: "delete", Dev: "dv0", Parent: "1:24", Handle: "24:"}, withDist("add"))
	twice("a new qdisc is uniform again", func(s float64) bool { return s < 0.10 })
}
