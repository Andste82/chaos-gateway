package apply_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
)

// The PMTU mirror tables in the apply (M10, plan §2.5), on the simulated kernel: the routes with the size
// locked in, the rule on the mark, a size that changes in place, removal, and damage found by the verify.
// What the real kernel does with the same commands is pinned by the testbed tests of the engine.

const (
	mtuIcmp1280 = `{target: {device: dev-a}, fault: {family: mtu, mtu: {size: 1280, mode: icmp}}}`
	mtuIcmp1400 = `{target: {device: dev-a}, fault: {family: mtu, mtu: {size: 1400, mode: icmp}}}`
)

func countOf(ints []int, v int) int {
	n := 0
	for _, i := range ints {
		if i == v {
			n++
		}
	}
	return n
}

func (x *tcEnv) ruleLines() string {
	x.t.Helper()
	s, err := apply.ReadState(context.Background(), x.exec(), "", apply.WantOf(x.compileWith()))
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	for _, r := range s.Rules {
		if r.Fwmark != "" {
			out = append(out, fmt.Sprintf("%d %s/%s -> %s", r.Priority, r.Fwmark, r.Fwmask, r.Table))
		}
	}
	return strings.Join(out, "; ")
}

func TestAnIcmpMTUFaultIsAppliedWithItsMirrorTableAndVerified(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(mtuIcmp1280)
	tg := x.compileWith(o)
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	mirror := x.k.RouteMTUs("103")
	policy := x.k.RouteMTUs("100")
	if len(mirror) == 0 || len(mirror) != len(policy) || countOf(mirror, 1280) != len(mirror) || countOf(policy, 0) != len(policy) {
		t.Errorf("mirror %v, policy %v", mirror, policy)
	}
	if got := x.ruleLines(); got != "950 0x20000/0xe0000 -> 103" {
		t.Errorf("%s", got)
	}
	sum := strings.Join(res.Plan.Summary, "\n")
	if !strings.Contains(sum, "route replace 10.10.0.0/24 table 103 dev br-iot mtu lock 1280") || !strings.Contains(sum, "rule add priority 950 fwmark 0x20000/0xe0000 iif  table 103") {
		t.Errorf("the plan does not say it:\n%s", sum)
	}
	// a second apply of the same target changes nothing in the routing
	x.k.ClearLog()
	tg2 := x.compileWith(o)
	x.applyRetiring(tg2)
	for _, c := range x.k.Commands() {
		if strings.HasPrefix(c, "ip -4 -force -batch") || strings.Contains(c, "route replace") {
			t.Errorf("a re-apply writes routes: %s", c)
		}
	}
	if mm := x.strictVerify(tg2); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// A new size takes the free table, and the route of a table whose size changes is replaced in place:
// never deleted and added, so there is no moment without the route.
func TestAChangedSizeReplacesTheRoutesOfTheMirrorTableInPlace(t *testing.T) {
	x := newTCEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(mtuIcmp1280)))
	tg := x.compileWith(x.overlay(mtuIcmp1400))
	if tg.PMTUTables[1400] != 1 {
		t.Fatalf("%v", tg.PMTUTables)
	}
	x.k.ClearLog()
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	mirror := x.k.RouteMTUs("103")
	if len(mirror) == 0 || countOf(mirror, 1400) != len(mirror) {
		t.Errorf("%v", mirror)
	}
	for _, c := range x.k.Commands() {
		if strings.Contains(c, "route del") {
			t.Errorf("a route is deleted: %s", c)
		}
	}
	sum := strings.Join(res.Plan.Summary, "\n")
	if !strings.Contains(sum, "table 103 dev br-iot mtu lock 1400") || strings.Contains(sum, "route delete") || strings.Contains(sum, "rule add") || strings.Contains(sum, "rule delete") {
		t.Errorf("%s", sum)
	}
}

func TestTheMirrorTablesAndTheRuleGoWhenTheLastIcmpFaultGoes(t *testing.T) {
	x := newTCEnv(t)
	keep := x.overlay(`{target: {device: dev-b}, fault: {family: mtu, mtu: {size: 1200, mode: blackhole}}}`)
	x.applyRetiring(x.compileWith(keep, x.overlay(mtuIcmp1280)))
	if len(x.k.RouteMTUs("103")) == 0 {
		t.Fatal("no mirror")
	}
	tg := x.compileWith(keep)
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if n := len(x.k.RouteMTUs("103")); n != 0 {
		t.Errorf("%d routes are left in the mirror", n)
	}
	if got := x.ruleLines(); got != "" {
		t.Errorf("the rule is left: %s", got)
	}
	if mm := x.strictVerify(tg); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// A mirror whose routes lost their size, or a route that went, is found by the verify and repaired by the next
// apply.
func TestAMirrorTableThatWasChangedBehindOurBackIsFoundAndRepaired(t *testing.T) {
	x := newTCEnv(t)
	o := x.overlay(mtuIcmp1280)
	tg := x.compileWith(o)
	x.applyRetiring(tg)
	x.k.SetRouteMTU("103", 1500)
	mm := x.strictVerify(tg)
	if len(mm) == 0 {
		t.Fatal("the changed size is not found")
	}
	found := false
	for _, m := range mm {
		found = found || (m.Subsystem == "routes" && strings.Contains(m.Detail, "mtu lock 1280"))
	}
	if !found {
		t.Errorf("%v", mm)
	}
	res := x.applyRetiring(x.compileWith(o))
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if got := x.k.RouteMTUs("103"); countOf(got, 1280) != len(got) {
		t.Errorf("not repaired: %v", got)
	}
}

// The preview of an MTU fault says what it does to routing: the mirror routes and the rule on the mark.
func TestThePreviewOfAnIcmpMTUFaultShowsTheMirrorRoutesAndTheRuleOnTheMark(t *testing.T) {
	x := newTCEnv(t)
	tg := x.compileWith(x.overlay(mtuIcmp1280))
	s, err := apply.ReadState(context.Background(), x.exec(), "", apply.WantOf(tg))
	if err != nil {
		t.Fatal(err)
	}
	d := apply.Diff(tg, s)
	if !strings.Contains(d.Routes, "+rule 950 fwmark 0x20000/0xe0000 iif  lookup 103") || !strings.Contains(d.Routes, "+route table 103 10.10.0.0/24 dev br-iot mtu lock 1280") {
		t.Errorf("%s", d.Routes)
	}
}

// The last MTU fault takes its maps, its lookup chain and its chains along: a map whose elements name chains is
// emptied before the chains go (the kernel refuses to delete a chain a map element still names, found on the real
// kernel), and the routing of the mirror goes with it.
func TestTheLastMTUFaultTakesItsMapsAndChainsAlong(t *testing.T) {
	x := newTCEnv(t)
	for _, body := range []string{mtuIcmp1280, `{target: {device: dev-a}, fault: {family: mtu, mtu: {size: 1300, mode: mss_clamp}}}`, `{target: {device: dev-b}, fault: {family: mtu, mtu: {size: 1200, mode: blackhole}}}`} {
		tg := x.compileWith(x.overlay(body))
		res := x.applyRetiring(tg)
		if len(res.Mismatches) != 0 {
			t.Fatalf("%v", res.Mismatches)
		}
		if !strings.Contains(x.nftJSON(), "classify_pmtu") {
			t.Fatalf("no lookup chain for %s", body)
		}
		tg = x.compileWith()
		res = x.applyRetiring(tg)
		if len(res.Mismatches) != 0 {
			t.Fatalf("after %s: %v", body, res.Mismatches)
		}
		if j := x.nftJSON(); strings.Contains(j, "classify_pmtu") || strings.Contains(j, "pmtu_") {
			t.Errorf("objects of the MTU family are left after %s", body)
		}
		if n := len(x.k.RouteMTUs("103")); n != 0 {
			t.Errorf("%d mirror routes are left", n)
		}
		if mm := x.strictVerify(tg); len(mm) != 0 {
			t.Errorf("%v", mm)
		}
	}
}
