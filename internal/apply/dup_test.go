package apply_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// The duplication hook in the apply (M10, P2-M10-01): the netdev table that copies the packets the
// classification flagged. It is written before the transaction that flags packets, verified like the
// rest, repaired when it is damaged, left alone when it is right, and removed after the last duplicating
// fault is gone.

const dupFault = `{target: {device: dev-a}, fault: {upload: {latency: 20ms, duplicate: 10%}}}`

func (x *tcEnv) dupDevs() string { return strings.Join(x.k.DupDevs(), " ") }

func TestADuplicatingFaultGetsItsHookOnEveryInterfaceOfTheTreeBeforeTheClassificationFlagsAnything(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(dupFault)
	tg := x.compileWith(o)
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if got := x.dupDevs(); got != "br-iot br-lab wan0" {
		t.Errorf("the hook is on %q", got)
	}
	// it is written before the transaction that makes packets ask for a copy
	var dupAt, nftAt = -1, -1
	for i, op := range res.Plan.Ops {
		switch op.(type) {
		case *executor.NftDup:
			dupAt = i
		case *executor.NftApply:
			nftAt = i
		}
	}
	if dupAt < 0 || nftAt < 0 || dupAt > nftAt {
		t.Errorf("the hook is operation %d, the transaction %d", dupAt, nftAt)
	}
	if !strings.Contains(strings.Join(res.Plan.Summary, "\n"), "nft: duplication hook on br-iot, br-lab, wan0") {
		t.Errorf("the plan does not say it: %v", res.Plan.Summary)
	}
	// a second apply of the same target writes nothing for it (the transaction of the main table does: the
	// generation rule changes with every compile)
	tg2 := x.compileWith(o)
	res2 := x.applyRetiring(tg2)
	for _, op := range res2.Plan.Ops {
		if _, ok := op.(*executor.NftDup); ok {
			t.Errorf("a re-apply writes the hook again: %v", res2.Plan.Summary)
		}
	}
	if mm := x.strictVerify(tg2); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// The hook goes with the last duplicating fault, after the transaction that stopped the flagging, while the
// tree of the other fault stays.
func TestTheHookGoesAfterTheTransactionThatStopsFlaggingWhenTheLastDuplicatingFaultGoes(t *testing.T) {
	x := newTCEnv(t)
	keep := x.overlay(`{target: {device: dev-b}, fault: {latency: 30ms}}`)
	dup := x.overlay(dupFault)
	x.applyRetiring(x.compileWith(keep, dup))
	if x.dupDevs() == "" {
		t.Fatal("no hook")
	}
	tg2 := x.compileWith(keep)
	if len(tg2.DupDevs) != 0 {
		t.Fatalf("the second target still duplicates: %v", tg2.DupDevs)
	}
	x.k.ClearLog()
	res := x.applyRetiring(tg2)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if got := x.dupDevs(); got != "" {
		t.Errorf("the hook is still on %q", got)
	}
	var dupAt, nftAt = -1, -1
	for i, op := range res.Plan.Ops {
		switch o := op.(type) {
		case *executor.NftDup:
			dupAt = i
			if len(o.Devs) != 0 {
				t.Errorf("the deletion names %v", o.Devs)
			}
		case *executor.NftApply:
			nftAt = i
		}
	}
	if dupAt < 0 || nftAt < 0 || dupAt < nftAt {
		t.Errorf("the hook is deleted at %d, the transaction at %d", dupAt, nftAt)
	}
	if !strings.Contains(strings.Join(res.Plan.Summary, "\n"), "the duplication hook goes") {
		t.Errorf("%v", res.Plan.Summary)
	}
	x.clock.Advance(1e12)
	if _, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil {
		t.Fatal(err)
	}
	if mm := x.strictVerify(tg2); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// A hook that was damaged behind the gateway's back (a chain gone, a rule that is not the executor's, a table
// that is left when nothing duplicates) is found by the verify and repaired by the next apply.
func TestADamagedOrLeftOverHookIsFoundByTheVerifyAndRepaired(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(dupFault)
	tg := x.compileWith(o)
	x.applyRetiring(tg)

	for name, damage := range map[string]func(){
		"a chain is gone":       func() { x.k.BreakDup("wan0", true) },
		"the rule is not ours":  func() { x.k.BreakDup("br-lab", false) },
		"the table was removed": func() { x.k.ResetDup() },
	} {
		damage()
		mm := x.strictVerify(tg)
		if len(mm) == 0 || !strings.Contains(mm[0].String(), "duplication hook") {
			t.Fatalf("%s: the verify says %v", name, mm)
		}
		res := x.applyRetiring(tg)
		if len(res.Mismatches) != 0 {
			t.Fatalf("%s: %v", name, res.Mismatches)
		}
		if got := x.dupDevs(); got != "br-iot br-lab wan0" {
			t.Errorf("%s: not repaired: %q", name, got)
		}
	}

	// the tree goes and the hook with it; a hook that is left over afterwards (put back behind the back) is found
	// and deleted by the next apply, which has nothing else to do
	tgNone := x.compileWith()
	x.applyRetiring(tgNone)
	x.clock.Advance(1e12)
	if _, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil {
		t.Fatal(err)
	}
	if got := x.dupDevs(); got != "" {
		t.Fatalf("the hook outlived the fault: %q", got)
	}
	if _, err := x.exec().Do(context.Background(), &executor.NftDup{Devs: []string{"br-iot"}}); err != nil {
		t.Fatal(err)
	}
	if mm := x.strictVerify(tgNone); len(mm) == 0 || !strings.Contains(mm[0].String(), "not wanted") {
		t.Fatalf("a hook nobody wants passes the verify: %v", mm)
	}
	if res := x.applyRetiring(tgNone); len(res.Mismatches) != 0 || x.dupDevs() != "" {
		t.Errorf("%v, hook on %q", res.Mismatches, x.dupDevs())
	}
}

// The compiler's target names the hook only while some class duplicates, and the preview says what the apply
// would do about it.
func TestThePreviewSaysWhatItDoesAboutTheHook(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(dupFault)
	p, err := apply.Preview(context.Background(), x.exec(), "", x.compileWith(o))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Summary, "\n"), "nft: duplication hook on br-iot, br-lab, wan0") {
		t.Errorf("%v", p.Summary)
	}
	if x.dupDevs() != "" {
		t.Error("a preview changed the kernel")
	}
}
