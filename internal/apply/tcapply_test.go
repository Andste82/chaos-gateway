package apply_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The tc tree in the apply, on the simulated kernel (M8b). The simulator answers like the tool does
// (internal/apply/kernelsim/tc.go); what the real kernel does with the same operations is pinned by
// the testbed tests (tc_integration_test.go) and by the kernel tests of the executor.

// tcDevs are the interfaces of the test gateway that carry the tree: the two bridges and the uplink.
var tcDevs = []string{"br-iot", "br-lab", "wan0"}

type tcEnv struct {
	*env
	ids   map[string]int
	log   []string // tc batches, one string per `tc -batch` and its lines
	clock *clock.Fake
	ret   *apply.Retirer
}

func newTCEnv(t *testing.T) *tcEnv {
	e := newEnv(t)
	devs := map[string]model.Device{}
	for i, d := range []struct{ name, addr string }{{"dev-a", "10.10.0.11"}, {"dev-b", "10.10.0.12"}, {"dev-c", "10.20.0.11"}} {
		devs[fmt.Sprintf("00000000-0000-4000-8000-0000000000d%d", i)] = model.Device{Name: d.name, Identifiers: &model.DeviceIdentifiers{Ipv4: &[]string{d.addr}}}
	}
	e.cfg.Devices = &devs
	norm, errs := domain.Normalize(e.cfg)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	e.cfg = norm
	c := clock.NewFake(time.Unix(1_700_000_000, 0))
	x := &tcEnv{env: e, ids: map[string]int{}, clock: c, ret: apply.NewRetirer(c)}
	e.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "tc" && len(argv) > 1 && strings.Contains(strings.Join(argv, " "), "-batch") {
			x.log = append(x.log, strings.TrimSpace(stdin))
		}
		return nil
	}
	return x
}

func (x *tcEnv) overlay(body string) model.Overlay {
	x.t.Helper()
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		x.t.Fatalf("%s: %v", body, err)
	}
	n, errs := domain.ValidateOverlay(x.cfg, req)
	if len(errs) != 0 {
		x.t.Fatalf("%s: %v", body, errs)
	}
	o, err := domain.NewOverlay(*n, model.Owner{Type: "user", Id: "test"}, uuid.New(), time.Unix(1_700_000_000, 0))
	if err != nil {
		x.t.Fatal(err)
	}
	return o
}

func (x *tcEnv) compileWith(overlays ...model.Overlay) *compiler.Target {
	x.t.Helper()
	id := domain.ResolveIdentity(x.cfg, domain.Observed{}, nil)
	tg := x.compile(func(_ *model.Configuration, in *compiler.Input) {
		in.Overlays, in.Identity, in.FaultIDs, in.ClassLimit = overlays, &id, x.ids, 1000
	})
	if tg.HasErrors() {
		x.t.Fatalf("%+v", tg.Problems)
	}
	x.ids = tg.FaultIDs
	return tg
}

// applyRetiring is the engine's apply: stale classes go to the retirer.
func (x *tcEnv) applyRetiring(tg *compiler.Target) *apply.Result {
	x.t.Helper()
	res, err := apply.ApplyWith(context.Background(), x.exec(), "", tg, x.ret)
	if err != nil {
		x.t.Fatalf("apply: %v\n%v", err, x.dumpTC())
	}
	return res
}

func (x *tcEnv) dumpTC() string {
	var b strings.Builder
	for _, d := range tcDevs {
		t := x.readTC(d)
		fmt.Fprintf(&b, "%s:\n  %s\n", d, strings.Join(t.Lines(), "\n  "))
	}
	return b.String()
}

func (x *tcEnv) readTC(dev string) *linux.NormTree {
	x.t.Helper()
	out, err := x.exec().Do(context.Background(), &executor.Read{What: executor.ReadTC, Dev: dev})
	if err != nil {
		x.t.Fatal(err)
	}
	var t linux.NormTree
	if err := json.Unmarshal(out.Data[0], &t); err != nil {
		x.t.Fatal(err)
	}
	return &t
}

// classes lists the class ids of the own tree of dev, the default class left out.
func (x *tcEnv) classes(dev string) string {
	var ids []string
	for _, c := range x.readTC(dev).Subtree("1:").Classes {
		if c.ID != "1:1" {
			ids = append(ids, c.ID)
		}
	}
	return strings.Join(ids, " ")
}

func (x *tcEnv) tcLog() string { return strings.Join(x.log, "\n---\n") }

// strictVerify reads the state and verifies it with nothing retiring.
func (x *tcEnv) strictVerify(tg *compiler.Target) []apply.Mismatch {
	x.t.Helper()
	s, err := apply.ReadState(context.Background(), x.exec(), "", apply.WantOf(tg))
	if err != nil {
		x.t.Fatal(err)
	}
	return apply.Verify(tg, s)
}

const (
	dev150 = `{target: {device: dev-a}, fault: {latency: 150ms, jitter: 20ms}}`
	devLat = `{target: {device: dev-a}, fault: {latency: %s}}`
)

func TestAFaultIsAppliedAndVerifiedOnEveryInterface(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(dev150)
	tg := x.compileWith(o)
	if tg.TC == nil || len(tg.TC.Classes) == 0 {
		t.Fatal("no tc tree compiled")
	}
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	for _, d := range tcDevs {
		if diff := linux.DiffTC(tg.TC.Norm(d), x.readTC(d).Subtree("1:")); len(diff) != 0 {
			t.Errorf("%s: %v", d, diff)
		}
	}
	// the port and the management interface carry no tree
	for _, d := range []string{"lan0", "lan1", "mgmt0"} {
		if q := x.readTC(d).Subtree("1:").Qdiscs; len(q) != 0 {
			t.Errorf("%s: %+v", d, q)
		}
	}
	// the tree comes before the classification: in the plan, and in the order the kernel saw
	var tcAt, nftAt = -1, -1
	for i, op := range res.Plan.Ops {
		switch op.(type) {
		case *executor.TC:
			tcAt = i
		case *executor.NftApply:
			nftAt = i
		}
	}
	if tcAt < 0 || nftAt < 0 || tcAt > nftAt {
		t.Errorf("the tc operation is at %d, the nftables transaction at %d", tcAt, nftAt)
	}
}

func TestAReApplyOfTheSameTargetTouchesNoTC(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(dev150)
	x.applyRetiring(x.compileWith(o))
	x.log = nil
	res := x.applyRetiring(x.compileWith(o))
	if !res.Plan.Empty() {
		t.Errorf("a second apply of the same target plans work: %v", res.Plan.Summary)
	}
	if len(x.log) != 0 {
		t.Errorf("a re-apply changed the tc state:\n%s", x.tcLog())
	}
}

func TestChangingTheParametersChangesTheLeafInPlace(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(fmt.Sprintf(devLat, "100ms"))
	tg := x.compileWith(o)
	x.applyRetiring(tg)
	f := tg.Faults[0]
	leaf := compiler.TCClass{ID: f.ID, Dir: compiler.Upload, Minor: 0x10 + 2*f.ID}.LeafHandle()
	seed := x.k.TCSeed("br-iot", leaf)
	if seed == 0 {
		t.Fatalf("no netem leaf %s on br-iot:\n%s", leaf, x.dumpTC())
	}
	before := x.classes("br-iot")

	// the same overlay, other parameters: same key, same id
	o2 := o
	req, err := domain.DecodeOverlayRequest([]byte(fmt.Sprintf(devLat, "600ms")), domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	n, errs := domain.ValidateOverlay(x.cfg, req)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	o2.Target, o2.Fault = n.Target, n.Fault
	tg2 := x.compileWith(o2)
	if tg2.Faults[0].ID != f.ID {
		t.Fatalf("the id moved from %d to %d", f.ID, tg2.Faults[0].ID)
	}
	x.log = nil
	res := x.applyRetiring(tg2)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if got := x.k.TCSeed("br-iot", leaf); got != seed {
		t.Errorf("the leaf was created again (seed %d, was %d)", got, seed)
	}
	if after := x.classes("br-iot"); after != before {
		t.Errorf("the classes changed: %s, was %s", after, before)
	}
	log := x.tcLog()
	if strings.Contains(log, "delete") || strings.Contains(log, " add ") {
		t.Errorf("a change in place deleted or added something:\n%s", log)
	}
	if !strings.Contains(log, "qdisc replace dev br-iot parent "+tg.TC.Classes[0].ClassID()+" handle "+leaf+" netem limit") || !strings.Contains(log, "delay 600ms") {
		t.Errorf("the leaf was not replaced with the new delay:\n%s", log)
	}
	if strings.Contains(log, "class replace") || strings.Contains(log, "filter replace") {
		t.Errorf("an unchanged class or filter was written again:\n%s", log)
	}
}

// Make before break (plan §3.2): the overlay is replaced by another one, so the id moves.
func TestAMovedIdCreatesTheNewClassesFirstAndDeletesTheOldOnesAfterTheDelay(t *testing.T) {
	x := newTCEnv(t)
	oOld := x.overlay(fmt.Sprintf(devLat, "600ms"))
	tgOld := x.compileWith(oOld)
	x.applyRetiring(tgOld)
	idOld := tgOld.Faults[0].ID
	oldClasses := x.classes("br-iot")

	oNew := x.overlay(fmt.Sprintf(devLat, "20ms"))
	tgNew := x.compileWith(oNew)
	idNew := tgNew.Faults[0].ID
	if idNew == idOld {
		t.Fatalf("the new fault took the id %d of the old one", idOld)
	}
	x.log = nil
	x.k.ClearLog()
	res := x.applyRetiring(tgNew)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	// both are there, on every interface, and the old ones are known to the retirer
	for _, d := range tcDevs {
		got := x.classes(d)
		if !strings.Contains(" "+got+" ", " "+strings.Fields(oldClasses)[0]+" ") || len(strings.Fields(got)) != len(strings.Fields(oldClasses))+len(tgNew.TC.Classes) {
			t.Errorf("%s: classes %q after the apply, old ones were %q", d, got, oldClasses)
		}
	}
	if n := len(x.ret.Pending()); n != len(tcDevs)*len(tgOld.TC.Classes) {
		t.Errorf("%d classes wait for their deletion, want %d", n, len(tcDevs)*len(tgOld.TC.Classes))
	}
	if strings.Contains(x.tcLog(), "delete") {
		t.Errorf("the apply deleted something:\n%s", x.tcLog())
	}
	// the order the kernel saw: the new classes, then the classification
	var tcAt, nftAt = -1, -1
	for i, c := range x.k.Commands() {
		switch {
		case strings.HasPrefix(c, "tc -batch"):
			tcAt = i
		case strings.HasPrefix(c, "nft") && strings.Contains(c, "-f"):
			nftAt = i
		}
	}
	if tcAt < 0 || nftAt < 0 || tcAt > nftAt {
		t.Errorf("tc at %d, nft at %d: %v", tcAt, nftAt, x.k.Commands())
	}

	// the old classes live for the largest delay (600 ms, the old fault's) plus one second
	grace := 600*time.Millisecond + time.Second
	if next, ok := x.ret.Next(); !ok || next != grace {
		t.Errorf("the next deletion is due in %v (%v), want %v", next, ok, grace)
	}
	x.clock.Advance(grace - time.Millisecond)
	if n, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil || n != 0 {
		t.Fatalf("reaped %d (%v) before the time", n, err)
	}
	x.clock.Advance(time.Millisecond)
	n, err := x.ret.Reap(context.Background(), x.exec(), "")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(tcDevs)*len(tgOld.TC.Classes) {
		t.Errorf("reaped %d classes", n)
	}
	if len(x.ret.Pending()) != 0 {
		t.Errorf("still waiting: %v", x.ret.Pending())
	}
	// what is left is exactly the target, strictly verified
	if mm := x.strictVerify(tgNew); len(mm) != 0 {
		t.Errorf("%v\n%s", mm, x.dumpTC())
	}
	for _, d := range tcDevs {
		if strings.Contains(x.classes(d), "1:"+fmt.Sprintf("%x", 0x10+2*idOld)) {
			t.Errorf("%s still has a class of the old id: %s", d, x.classes(d))
		}
	}
}

func TestADeletionWaitsForTheQueuedPacketsUpToACap(t *testing.T) {
	x := newTCEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(fmt.Sprintf(devLat, "50ms"))))
	tgNew := x.compileWith(x.overlay(fmt.Sprintf(devLat, "30ms")))
	x.applyRetiring(tgNew)
	// the old upload class holds packets: its leaf is the class's minor
	oldClass := strings.Fields(x.classes("br-iot"))[0]
	oldLeaf := strings.TrimPrefix(oldClass, "1:") + ":"
	x.k.SetTCStats("br-iot", oldLeaf, linux.NormStats{Backlog: 3000, Qlen: 2})

	x.clock.Advance(2 * time.Second)
	n, err := x.ret.Reap(context.Background(), x.exec(), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := 2*len(tcDevs) - 1; n != want {
		t.Errorf("reaped %d, want %d (both classes of every interface but the one with a queue)", n, want)
	}
	if !strings.Contains(x.classes("br-iot"), oldClass) {
		t.Errorf("the class with queued packets was deleted")
	}
	// it is looked at again every second while it holds packets
	x.clock.Advance(RetireStep)
	if n, _ := x.ret.Reap(context.Background(), x.exec(), ""); n != 0 {
		t.Errorf("reaped %d with the queue still there", n)
	}
	// the queue drains: it goes
	x.k.SetTCStats("br-iot", oldLeaf, linux.NormStats{})
	x.clock.Advance(RetireStep)
	if n, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil || n != 1 {
		t.Fatalf("reaped %d (%v) after the queue drained", n, err)
	}
	if mm := x.strictVerify(tgNew); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

func TestAQueueThatNeverDrainsIsDeletedAfterTheCap(t *testing.T) {
	x := newTCEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(fmt.Sprintf(devLat, "50ms"))))
	tgNew := x.compileWith(x.overlay(fmt.Sprintf(devLat, "30ms")))
	x.applyRetiring(tgNew)
	oldClass := strings.Fields(x.classes("wan0"))[0]
	x.k.SetTCStats("wan0", strings.TrimPrefix(oldClass, "1:")+":", linux.NormStats{Backlog: 1500, Qlen: 1})
	start := x.clock.Monotonic()
	for len(x.ret.Pending()) > 0 {
		if x.clock.Monotonic()-start > 6*time.Minute {
			t.Fatalf("still pending after %v: %v", x.clock.Monotonic()-start, x.ret.Pending())
		}
		x.clock.Advance(RetireStep)
		if _, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil {
			t.Fatal(err)
		}
	}
	// a netem with a rate can hold a queue for minutes: it is given five, then it goes with its queue
	if waited := x.clock.Monotonic() - start; waited < 5*time.Minute {
		t.Errorf("the class went after %v, before the cap of 5 minutes", waited)
	}
	if strings.Contains(x.classes("wan0"), oldClass) {
		t.Errorf("the class is still there: %s", x.classes("wan0"))
	}
}

// RetireStep is one second: the time between two looks at a class that still holds packets.
const RetireStep = time.Second

func TestAnIdThatReturnsBeforeTheDeletionKeepsItsClass(t *testing.T) {
	x := newTCEnv(t)
	oA := x.overlay(fmt.Sprintf(devLat, "50ms"))
	tgA := x.compileWith(oA)
	x.applyRetiring(tgA)
	leaf := tgA.TC.Classes[0].LeafHandle()
	seed := x.k.TCSeed("br-iot", leaf)
	if seed == 0 {
		x.t.Fatalf("leaf %s is not where the test expects it:\n%s", leaf, x.dumpTC())
	}
	// the fault goes: nothing impairs, the whole tree is stale
	tgNone := x.compileWith()
	x.applyRetiring(tgNone)
	if p := x.ret.Pending(); len(p) != len(tcDevs) || p[0].Class != "" {
		t.Fatalf("pending %+v", p)
	}
	// the same overlay is back before the time: nothing was deleted, nothing waits
	x.clock.Advance(500 * time.Millisecond)
	x.log = nil
	res := x.applyRetiring(x.compileWith(oA))
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
	if len(x.ret.Pending()) != 0 {
		t.Errorf("still pending: %+v", x.ret.Pending())
	}
	if got := x.k.TCSeed("br-iot", leaf); got != seed {
		t.Errorf("the leaf was created again")
	}
	if strings.Contains(x.tcLog(), "delete") || strings.Contains(x.tcLog(), "root") {
		t.Errorf("the apply deleted something or made the root again:\n%s", x.tcLog())
	}
}

func TestWithoutAnyFaultTheTreeGoesAfterTheDelayAndTheHostsRootComesBack(t *testing.T) {
	x := newTCEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(fmt.Sprintf(devLat, "250ms"))))
	tgNone := x.compileWith()
	if tgNone.TC != nil {
		t.Fatal("a tc tree without a fault")
	}
	res := x.applyRetiring(tgNone)
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
	for _, d := range tcDevs {
		if len(x.readTC(d).Subtree("1:").Qdiscs) == 0 {
			t.Errorf("%s lost its tree at once", d)
		}
	}
	x.clock.Advance(1250*time.Millisecond - time.Nanosecond)
	if n, _ := x.ret.Reap(context.Background(), x.exec(), ""); n != 0 {
		t.Errorf("reaped %d early", n)
	}
	x.clock.Advance(time.Nanosecond)
	if n, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil || n != len(tcDevs) {
		t.Fatalf("reaped %d (%v)", n, err)
	}
	for _, d := range tcDevs {
		if q := x.readTC(d).Qdiscs; len(q) != 1 || q[0].Kind != "noqueue" {
			t.Errorf("%s: %+v", d, q)
		}
	}
	if mm := x.strictVerify(tgNone); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// A gateway that restarted has no memory of what was retiring: the classes it finds that the target
// does not want start their time at its first apply.
func TestAfterARestartTheStaleClassesAreFoundAgainAndGoLater(t *testing.T) {
	x := newTCEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(fmt.Sprintf(devLat, "400ms"))))
	tgNew := x.compileWith(x.overlay(fmt.Sprintf(devLat, "10ms")))
	x.applyRetiring(tgNew)
	if len(x.ret.Pending()) == 0 {
		t.Fatal("nothing pending")
	}
	// the process restarts: a new retirer, the kernel as it is
	x.clock.Advance(10 * time.Second)
	x.ret = apply.NewRetirer(x.clock)
	res := x.applyRetiring(tgNew)
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
	if got := len(x.ret.Pending()); got != len(tcDevs)*len(tgNew.TC.Classes) {
		t.Fatalf("%d pending", got)
	}
	// the delay of the stale leaves in the kernel (400 ms) is what counts: 1.4 s from now
	if next, _ := x.ret.Next(); next != 1400*time.Millisecond {
		t.Errorf("next in %v", next)
	}
	x.clock.Advance(1400 * time.Millisecond)
	if n, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil || n != classesOn(tcDevs, tgNew) {
		t.Fatalf("reaped %d (%v)", n, err)
	}
	if mm := x.strictVerify(tgNew); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

func classesOn(devs []string, tg *compiler.Target) int { return len(devs) * len(tg.TC.Classes) }

// Without a retirer (the one-shot apply) the old class goes at once, after the transaction.
func TestWithoutARetirerTheOldClassesGoRightAfterTheTransaction(t *testing.T) {
	x := newTCEnv(t)
	x.apply(x.compileWith(x.overlay(fmt.Sprintf(devLat, "50ms"))))
	tgNew := x.compileWith(x.overlay(fmt.Sprintf(devLat, "30ms")))
	x.k.ClearLog()
	x.log = nil
	res := x.apply(tgNew)
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
	if mm := x.strictVerify(tgNew); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
	var nftAt, delAt = -1, -1
	for i, c := range x.k.Commands() {
		if strings.HasPrefix(c, "nft") && strings.Contains(c, "-f") {
			nftAt = i
		}
		if strings.HasPrefix(c, "tc -force -batch") {
			delAt = i
		}
	}
	if nftAt < 0 || delAt < nftAt {
		t.Errorf("nft at %d, the deletion at %d", nftAt, delAt)
	}
}

func TestATCFailureFailsTheApplyAndNothingIsHandedToTheRetirer(t *testing.T) {
	x := newTCEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(fmt.Sprintf(devLat, "50ms"))))
	pending := len(x.ret.Pending())
	tgNew := x.compileWith(x.overlay(fmt.Sprintf(devLat, "30ms")))
	nftsBefore := len(x.k.Commands())
	x.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "tc" && strings.Contains(strings.Join(argv, " "), "-batch") && strings.Contains(stdin, "class replace") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\nCommand failed -:1\n"}
		}
		return nil
	}
	_, err := apply.ApplyWith(context.Background(), x.exec(), "", tgNew, x.ret)
	var ae *apply.Error
	if !asError(err, &ae) || ae.Stage != "execute" {
		t.Fatalf("%v", err)
	}
	x.k.Fail = nil
	if len(x.ret.Pending()) != pending {
		t.Errorf("a failed apply changed what is pending")
	}
	// the nftables transaction did not run: the classification still points to the old classes
	for _, c := range x.k.Commands()[nftsBefore:] {
		if strings.HasPrefix(c, "nft") && strings.Contains(c, "-f") {
			t.Errorf("the transaction ran after the tc operation failed: %s", c)
		}
	}
}

func TestVerifyFindsWhatDiffersInTheTree(t *testing.T) {
	x := newTCEnv(t)
	tg := x.compileWith(x.overlay(dev150))
	x.applyRetiring(tg)
	if mm := x.strictVerify(tg); len(mm) != 0 {
		t.Fatalf("%v", mm)
	}
	cls := tg.TC.Classes[0]
	cases := map[string]struct {
		do   string
		want string
	}{
		"a leaf with another delay": {"qdisc replace dev br-lab parent " + cls.ClassID() + " handle " + cls.LeafHandle() + " netem limit 1000 delay 1ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit", "different"},
		"a filter that is gone":     {"filter delete dev wan0 parent 1: handle " + cls.FilterHandle() + " protocol ip prio 1 fw", "missing: filter"},
		"a class nobody asked for":  {"class replace dev br-iot parent 1: classid 1:99 htb rate 10gbit quantum 60000", "unexpected: class 1:99"},
		"the default class is gone": {"class delete dev br-lab classid 1:1", "missing: class 1:1"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			x := newTCEnv(t)
			tg := x.compileWith(x.overlay(dev150))
			x.applyRetiring(tg)
			if r, _ := x.k.Run(context.Background(), executor.Command{Tool: executor.ToolTC, Args: []string{"-force", "-batch", "-"}, Stdin: c.do + "\n"}); r.Exit != 0 {
				t.Fatalf("%s: %s", c.do, r.Stderr)
			}
			mm := x.strictVerify(tg)
			if len(mm) == 0 || !strings.Contains(fmt.Sprint(mm), c.want) {
				t.Errorf("verify says %v, want a mismatch with %q", mm, c.want)
			}
		})
	}
}

// What an apply does about damage (a leaf that was changed by hand, a class that is not ours any
// more) is to put the tree back.
func TestAnApplyRepairsAChangedTree(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(dev150)
	tg := x.compileWith(o)
	x.applyRetiring(tg)
	cls := tg.TC.Classes[0]
	for _, line := range []string{
		"qdisc replace dev br-iot parent " + cls.ClassID() + " handle " + cls.LeafHandle() + " netem limit 10 delay 1ms 0ms 0% loss random 5% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit",
		"filter delete dev br-lab parent 1: handle " + cls.FilterHandle() + " protocol ip prio 1 fw",
		"class delete dev wan0 classid 1:1",
	} {
		if r, _ := x.k.Run(context.Background(), executor.Command{Tool: executor.ToolTC, Args: []string{"-force", "-batch", "-"}, Stdin: line + "\n"}); r.Exit != 0 {
			t.Fatalf("%s: %s", line, r.Stderr)
		}
	}
	tg2 := x.compileWith(o)
	res := x.applyRetiring(tg2)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v\n%s", res.Mismatches, x.dumpTC())
	}
	if mm := x.strictVerify(tg2); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

func TestThePreviewListsTheTCWorkAndTheRetirement(t *testing.T) {
	x := newTCEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(fmt.Sprintf(devLat, "600ms"))))
	tgNew := x.compileWith(x.overlay(fmt.Sprintf(devLat, "20ms")))
	x.k.ClearLog()
	p, err := apply.Preview(context.Background(), x.exec(), "", tgNew)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(p.Summary, "\n")
	for _, want := range []string{"tc: br-iot: ", "objects created", "stay for 1.6s", "class 1:"} {
		if !strings.Contains(text, want) {
			t.Errorf("the preview lacks %q:\n%s", want, text)
		}
	}
	for _, c := range x.k.Commands() {
		if strings.HasPrefix(c, "tc -batch") || strings.HasPrefix(c, "tc -force") {
			t.Errorf("the preview changed tc: %s", c)
		}
	}
}

// The listing shows no distribution table and a change that names none keeps the old one, so the one
// update that drops a queue is the one to a uniform delay with a jitter from a leaf that may have a
// table.
func TestAChangeToAUniformJitterMakesTheLeafAgainOnlyWhereATableMayBe(t *testing.T) {
	const normal = `{target: {device: dev-a}, fault: {latency: 100ms, jitter: 20ms, distribution: normal}}`
	const uniform = `{target: {device: dev-a}, fault: {latency: 100ms, jitter: 20ms}}`
	const uniform2 = `{target: {device: dev-a}, fault: {latency: 120ms, jitter: 20ms}}`
	const noJitter = `{target: {device: dev-a}, fault: {latency: 100ms}}`
	x := newTCEnv(t)
	o := x.overlay(normal)
	tg := x.compileWith(o)
	x.applyRetiring(tg)
	leaf := tg.TC.Classes[0].LeafHandle()
	seed := x.k.TCSeed("br-iot", leaf)
	change := func(body string) *compiler.Target {
		x.t.Helper()
		n := x.overlay(body)
		o.Target, o.Fault = n.Target, n.Fault
		tg := x.compileWith(o)
		x.log = nil
		x.applyRetiring(tg)
		return tg
	}
	recreated := func() bool {
		return strings.Contains(x.tcLog(), "qdisc delete dev br-iot parent "+tg.TC.Classes[0].ClassID())
	}

	// normal -> normal with another delay: in place
	change(`{target: {device: dev-a}, fault: {latency: 110ms, jitter: 20ms, distribution: normal}}`)
	if recreated() || x.k.TCSeed("br-iot", leaf) != seed {
		t.Fatalf("a change within one table made the leaf again:\n%s", x.tcLog())
	}
	// normal -> uniform: the table has to go, so the leaf is made again (a new seed)
	change(uniform)
	if !recreated() || x.k.TCSeed("br-iot", leaf) == seed {
		t.Fatalf("the leaf kept its table:\n%s", x.tcLog())
	}
	seed = x.k.TCSeed("br-iot", leaf)
	// uniform -> uniform with another delay: in place, the apply knows it has no table
	change(uniform2)
	if recreated() || x.k.TCSeed("br-iot", leaf) != seed {
		t.Fatalf("a change of a uniform leaf made it again:\n%s", x.tcLog())
	}
	// uniform with a jitter -> no jitter -> uniform with a jitter: still no table, still in place
	change(noJitter)
	change(uniform)
	if recreated() || x.k.TCSeed("br-iot", leaf) != seed {
		t.Fatalf("%s", x.tcLog())
	}
	// normal -> no jitter (the table stays, it shapes nothing) -> uniform with a jitter: made again
	change(normal)
	if !strings.Contains(x.tcLog(), "distribution normal") || recreated() {
		t.Fatalf("a change of the table alone, invisible in the listing, was not written in place:\n%s", x.tcLog())
	}
	seed = x.k.TCSeed("br-iot", leaf)
	change(noJitter)
	if recreated() || x.k.TCSeed("br-iot", leaf) != seed {
		t.Fatalf("a change to no jitter made the leaf again:\n%s", x.tcLog())
	}
	change(uniform)
	if !recreated() {
		t.Fatalf("the table that stayed behind was not noticed:\n%s", x.tcLog())
	}
	// after a restart the apply does not know the table of a leaf it did not put there
	change(normal)
	x.ret = apply.NewRetirer(x.clock)
	seed = x.k.TCSeed("br-iot", leaf)
	change(uniform2)
	if !recreated() || x.k.TCSeed("br-iot", leaf) == seed {
		t.Fatalf("a leaf of unknown table was changed in place to uniform:\n%s", x.tcLog())
	}
	// but a leaf that already is what the target says is left alone, known or not
	x.ret = apply.NewRetirer(x.clock)
	seed = x.k.TCSeed("br-iot", leaf)
	change(uniform2)
	if len(x.log) != 0 || x.k.TCSeed("br-iot", leaf) != seed {
		t.Fatalf("an unchanged leaf was touched:\n%s", x.tcLog())
	}
}

// The plan says which leaves it makes new (their counters start at zero) and whether the nft table is
// new: what the engine's counter epochs follow.
func TestThePlanNamesTheLeavesItMakesNewAndATableThatIsNew(t *testing.T) {
	const normal = `{target: {device: dev-a}, fault: {latency: 100ms, jitter: 20ms, distribution: normal}}`
	const uniform = `{target: {device: dev-a}, fault: {latency: 100ms, jitter: 20ms}}`
	const slower = `{target: {device: dev-a}, fault: {latency: 150ms, jitter: 20ms}}`
	x := newTCEnv(t)
	o := x.overlay(normal)
	tg := x.compileWith(o)
	res := x.applyRetiring(tg)
	if !res.Plan.NftNew {
		t.Error("the first apply did not find the nft table new")
	}
	var want []string
	for _, c := range tg.TC.Classes {
		for _, d := range tcDevs {
			want = append(want, d+" "+c.ClassID())
		}
	}
	got := append([]string(nil), res.Plan.QueuesCreated...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") || len(want) != 2*len(tcDevs) {
		t.Errorf("the first plan makes %v new, want %v", got, want)
	}
	change := func(body string) *apply.Result {
		n := x.overlay(body)
		o.Target, o.Fault = n.Target, n.Fault
		return x.applyRetiring(x.compileWith(o))
	}
	// the same target again, and a change in place: no leaf is new, the table is not
	if res = x.applyRetiring(x.compileWith(o)); res.Plan.NftNew || len(res.Plan.QueuesCreated) != 0 {
		t.Errorf("a re-apply: %+v %v", res.Plan.NftNew, res.Plan.QueuesCreated)
	}
	if res = change(`{target: {device: dev-a}, fault: {latency: 120ms, jitter: 20ms, distribution: normal}}`); len(res.Plan.QueuesCreated) != 0 {
		t.Errorf("a change in place makes %v new", res.Plan.QueuesCreated)
	}
	// a table that has to go: the leaves are deleted and made again
	if res = change(uniform); len(res.Plan.QueuesCreated) != len(want) {
		t.Errorf("normal -> uniform makes %v new, want all %d leaves", res.Plan.QueuesCreated, len(want))
	}
	if res = change(slower); len(res.Plan.QueuesCreated) != 0 {
		t.Errorf("a change of a uniform leaf makes %v new", res.Plan.QueuesCreated)
	}
}
