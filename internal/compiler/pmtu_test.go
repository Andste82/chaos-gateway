package compiler

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The compiler's side of the MTU family (M10, plan §2.5): the three modes, the PMTU mirror tables, the
// second lookup of the classify chain, the limit of seven sizes.

func pmtuOf(t *testing.T, tg *Target, source string) PMTUFault {
	t.Helper()
	for _, f := range tg.PMTU {
		if strings.EqualFold(f.Source, source) {
			return f
		}
	}
	t.Fatalf("no MTU fault of %s in %+v", source, tg.PMTU)
	return PMTUFault{}
}

func ruleJSON(c *Chain) []string {
	var out []string
	for _, r := range c.Rules {
		b, _ := json.Marshal(r.Expr)
		out = append(out, string(b))
	}
	return out
}

func routesOf(tg *Target, table int) []executor.Route {
	var out []executor.Route
	for _, r := range tg.Routes {
		if r.Table == table {
			out = append(out, r)
		}
	}
	return out
}

// The layout of plan §3.3: the PMTU bits are 17 to 19, they overlap neither the id, the direction, the
// service selection nor the duplication bit, and the fwmark rule selects exactly them.
func TestThePMTUMarkBitsAreTheOnesOfThePlanAndOverlapNothing(t *testing.T) {
	if PMTUMarkMask != 0x000e0000 || pmtuKeep != 0xfff1ffff {
		t.Fatalf("mask %#x keep %#x", PMTUMarkMask, pmtuKeep)
	}
	for name, other := range map[string]uint32{"id": markIDMaskBits, "direction": markDirMaskBits, "duplicate": MarkDupBit, "service": 1 << 20} {
		if PMTUMarkMask&other != 0 {
			t.Errorf("the PMTU bits overlap the %s bits", name)
		}
	}
	if PMTUMarkMask&0xf != 0 || PMTUMarkMask&0xff000000 != 0 {
		t.Error("the PMTU bits touch the bits left to other software")
	}
	if PMTUMarkOf(7)&^PMTUMarkMask != 0 || PMTUMarkOf(1) != 0x20000 {
		t.Errorf("marks %#x %#x", PMTUMarkOf(1), PMTUMarkOf(7))
	}
	// the mirror tables are Chaos Gateway's own, and not the policy or service table
	for i := 1; i <= PMTUMaxTables; i++ {
		n := PMTUTableOf(i)
		if n <= ServiceTable || n > executor.OwnTableLast {
			t.Errorf("index %d gives table %d", i, n)
		}
	}
	if PMTURulePriority >= PolicyRulePriority || PMTURulePriority <= ServiceRulePriority {
		t.Errorf("priority %d is not between the service rule and the policy rules", PMTURulePriority)
	}
}

func TestAnIcmpMTUFaultMarksItsTrafficAndGetsAMirrorTableWithTheSizeLockedIn(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280, mode: icmp}}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	f := pmtuOf(t, tg, o.Id.String())
	if f.Mode != "icmp" || f.Size != 1280 || f.Index != 1 || f.Table != 103 {
		t.Fatalf("%+v", f)
	}
	if len(tg.Faults) != 0 || tg.TC != nil || len(tg.FaultIDs) != 0 {
		t.Errorf("an MTU fault is no impairment: %+v %+v", tg.Faults, tg.TC)
	}
	// the chain keeps every other bit of the mark and writes the index into bits 17-19
	rules := ruleJSON(findChain(tg, f.Chain))
	if len(rules) != 4 || !strings.Contains(rules[2], "4294049791") /* 0xfff1ffff */ || !strings.Contains(rules[2], "131072") /* 0x20000 */ || !strings.Contains(rules[3], `"return"`) {
		t.Errorf("%v", rules)
	}
	// the second lookup is jumped to before the impairment lookup, and its map holds the device
	cl := findChain(tg, ClassifyChain)
	var jumps []int
	for i, r := range ruleJSON(cl) {
		if strings.Contains(r, `"jump"`) && strings.Contains(r, PMTUClassifyChain) {
			jumps = append(jumps, i)
		}
	}
	if len(jumps) != 1 || jumps[0] != 3 { // after the guard and the two direction rules; before the four impairment lookups
		t.Errorf("the jump is at %v: %v", jumps, ruleJSON(cl))
	}
	if n := len(cl.Rules); n != 8 {
		t.Errorf("the classify chain has %d rules", n)
	}
	m := findMap(tg, tg.PMTUMaps["dev"])
	if m == nil || len(m.Elements) != 1 || m.Elements[0].Key != "10.10.0.42" || m.Elements[0].Value != f.Chain {
		t.Fatalf("%+v", m)
	}
	// the mirror table is a copy of the policy table with the size locked in; a rule selects it by mark
	policy, mirror := routesOf(tg, PolicyTable), routesOf(tg, 103)
	if len(policy) == 0 || len(mirror) != len(policy) {
		t.Fatalf("%d routes in the policy table, %d in the mirror", len(policy), len(mirror))
	}
	for i, r := range mirror {
		p := policy[i]
		if r.MTU != 1280 || r.Dst != p.Dst || r.Via != p.Via || r.Dev != p.Dev || r.Action != "replace" {
			t.Errorf("mirror %+v of %+v", r, p)
		}
		if p.MTU != 0 {
			t.Errorf("the policy route %+v carries an MTU", p)
		}
	}
	var rule *executor.Rule
	for i, r := range tg.Rules {
		if r.Table == 103 {
			rule = &tg.Rules[i]
		}
	}
	if rule == nil || rule.Fwmark != "0x20000/0xe0000" || rule.Priority != PMTURulePriority || rule.Iif != "" {
		t.Errorf("%+v", rule)
	}
	if got := tg.PMTUTables; len(got) != 1 || got[1280] != 1 {
		t.Errorf("%v", got)
	}
}

func TestABlackholeAndAnMSSClampNeedNoMirrorTableAndDoTheirWorkInTheirChain(t *testing.T) {
	w := newFaultWorld(t)
	b := w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280, mode: blackhole}}}`, 0)
	c := w.overlay(`{target: {device: esp32-43}, fault: {family: mtu, mtu: {size: 1400, mode: mss_clamp}}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.PMTUTables) != 0 {
		t.Errorf("tables %v", tg.PMTUTables)
	}
	for _, r := range tg.Routes {
		if r.Table != PolicyTable && r.Table != ServiceTable {
			t.Errorf("a route in table %d", r.Table)
		}
		if r.MTU != 0 {
			t.Errorf("a route with an MTU: %+v", r)
		}
	}
	for _, r := range tg.Rules {
		if r.Fwmark != "" && r.Table != ServiceTable {
			t.Errorf("a rule on the mark: %+v", r)
		}
	}
	bh, ms := pmtuOf(t, tg, b.Id.String()), pmtuOf(t, tg, c.Id.String())
	if bh.Index != 0 || bh.Table != 0 || ms.Index != 0 || ms.Table != 0 || bh.CounterDrop == "" || ms.CounterDrop != "" {
		t.Errorf("%+v %+v", bh, ms)
	}
	// the black hole drops what is longer than the size, counts it, and does not touch the mark
	rules := ruleJSON(findChain(tg, bh.Chain))
	if len(rules) != 4 || !strings.Contains(rules[2], `"length"`) || !strings.Contains(rules[2], `"op":"\u003e"`) || !strings.Contains(rules[2], `"right":1280`) ||
		!strings.Contains(rules[2], `"drop"`) || !strings.Contains(rules[2], bh.CounterDrop) || strings.Contains(strings.Join(rules, ""), `"mark"`) {
		t.Errorf("%v", rules)
	}
	// the clamp writes size minus the headers into the MSS option of a SYN that offers more
	if ms.MSS() != 1360 {
		t.Errorf("mss %d", ms.MSS())
	}
	rules = ruleJSON(findChain(tg, ms.Chain))
	if len(rules) != 4 || !strings.Contains(rules[2], `"maxseg"`) || !strings.Contains(rules[2], `"syn"`) || !strings.Contains(rules[2], `"right":1360`) ||
		!strings.Contains(rules[2], `"mangle"`) || !strings.Contains(rules[2], `"value":1360`) {
		t.Errorf("%v", rules)
	}
	// the counters exist
	have := map[string]bool{}
	for _, n := range tg.Nft.Counters {
		have[n] = true
	}
	for _, n := range []string{bh.CounterUp, bh.CounterDown, bh.CounterDrop, ms.CounterUp, ms.CounterDown} {
		if !have[n] {
			t.Errorf("no counter %s in %v", n, tg.Nft.Counters)
		}
	}
}

// Without an MTU fault the target is what it was before the family existed: no second lookup, no maps.
func TestWithoutAnMTUFaultNothingOfTheMTUFamilyIsCompiled(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {latency: 10ms}}`, 0)
	tg := w.compile(nil)
	if len(tg.PMTU) != 0 || len(tg.PMTUMaps) != 0 || len(tg.PMTUTables) != 0 || findChain(tg, PMTUClassifyChain) != nil {
		t.Errorf("%+v", tg.PMTU)
	}
	if n := len(findChain(tg, ClassifyChain).Rules); n != 7 {
		t.Errorf("the classify chain has %d rules", n)
	}
	for _, m := range tg.Nft.Maps {
		if strings.HasPrefix(m.Name, "pmtu_") {
			t.Errorf("map %s", m.Name)
		}
	}
}

// The families resolve independently (plan §2.4): the same device has an impairment and an MTU winner, the
// MTU winner of a device beats the one of its network, and a destination refines it.
func TestTheMTUWinnerIsResolvedOnItsOwnAndRefinedByTheSelectors(t *testing.T) {
	w := newFaultWorld(t)
	net := w.overlay(`{target: {network: IoT}, fault: {family: mtu, mtu: {size: 1400, mode: icmp}}}`, 0)
	dev := w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280, mode: blackhole}}}`, time.Second)
	dst := w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1200, mode: mss_clamp}, destination: {cidr: 203.0.113.0/24}, protocol: tcp, ports: [443]}}`, 2*time.Second)
	imp := w.overlay(`{target: {device: esp32-42}, fault: {latency: 30ms}}`, 3*time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	n, d, x := pmtuOf(t, tg, net.Id.String()), pmtuOf(t, tg, dev.Id.String()), pmtuOf(t, tg, dst.Id.String())
	f := faultOf(t, tg, imp.Id.String(), "")
	if f.ID == 0 {
		t.Fatal("the impairment has no id")
	}
	val := map[string]string{}
	for _, lv := range classifyLevels {
		for _, e := range findMap(tg, tg.PMTUMaps[lv.field]).Elements {
			val[lv.field+" "+e.Key] = e.Value
		}
	}
	// the network's addresses (and esp32-43, who has no fault of its own) get the network winner;
	// esp32-42 gets its own, and its HTTPS traffic to the destination the third
	if got := val["dev 10.10.0.42"]; got != d.Chain {
		t.Errorf("esp32-42: %q, want %s (%v)", got, d.Chain, val)
	}
	found := false
	for k, v := range val {
		if strings.HasPrefix(k, "devdestport 10.10.0.42 . 203.0.113.0/24 . tcp . 443") {
			found = true
			if v != x.Chain {
				t.Errorf("%s: %s", k, v)
			}
		}
		if strings.HasPrefix(k, "dev ") && v == n.Chain && k == "dev 10.10.0.42" {
			t.Errorf("the network's winner wins for the device: %s", k)
		}
	}
	if !found {
		t.Errorf("no entry for the destination: %v", val)
	}
	netChain := 0
	for k, v := range val {
		if strings.HasPrefix(k, "dev ") && v == n.Chain {
			netChain++
		}
	}
	if netChain == 0 {
		t.Errorf("the network's winner is nowhere: %v", val)
	}
	// both lookups are in the classify chain, the impairment one still ends it
	if findChain(tg, MarkChainName(f.ID)) == nil || findChain(tg, PMTUClassifyChain) == nil {
		t.Error("a chain is missing")
	}
	// the winners are known to the API
	for _, k := range []string{n.Key, d.Key, x.Key} {
		if i := sort.SearchStrings(tg.Winners, k); i >= len(tg.Winners) || tg.Winners[i] != k {
			t.Errorf("%s is no winner in %v", k, tg.Winners)
		}
	}
}

// Seven distinct sizes in icmp mode fit, eight are capacity_exceeded and name the scope; two faults of
// the same size share one table; the other modes do not count.
func TestEightSizesInIcmpModeAreCapacityExceededAndSevenFit(t *testing.T) {
	w := newFaultWorld(t)
	sizes := []int{1500, 1480, 1460, 1440, 1420, 1400, 1380}
	for i, s := range sizes {
		w.overlay(fmt.Sprintf(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: %d}, protocol: tcp, ports: [%d]}}`, s, 8000+i), time.Duration(i)*time.Second)
	}
	w.overlay(`{target: {device: esp32-43}, fault: {family: mtu, mtu: {size: 1380, mode: icmp}}}`, 10*time.Second) // shares a table
	w.overlay(`{target: {device: lab-host}, fault: {family: mtu, mtu: {size: 1000, mode: blackhole}}}`, 11*time.Second)
	w.overlay(`{target: {device: lab-host}, fault: {family: mtu, mtu: {size: 900, mode: mss_clamp}, protocol: tcp, ports: [1]}}`, 12*time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.PMTUTables) != 7 {
		t.Fatalf("%v", tg.PMTUTables)
	}
	seen := map[int]bool{}
	for _, i := range tg.PMTUTables {
		seen[i] = true
	}
	if len(seen) != 7 {
		t.Errorf("indices are shared: %v", tg.PMTUTables)
	}
	for i := 1; i <= 7; i++ {
		if len(routesOf(tg, PMTUTableOf(i))) != len(routesOf(tg, PolicyTable)) {
			t.Errorf("table %d has %d routes", PMTUTableOf(i), len(routesOf(tg, PMTUTableOf(i))))
		}
	}

	w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1360}, protocol: tcp, ports: [9000]}}`, 20*time.Second)
	tg = w.compile(nil)
	if !tg.HasErrors() {
		t.Fatal("the eighth size is accepted")
	}
	p := tg.Errors()[0]
	if p.Code != CodeCapacityExceeded || !strings.Contains(p.Message, "8 different sizes") || !strings.Contains(p.Message, "7") || p.Scope == "" || len(p.Faults) == 0 {
		t.Errorf("%+v", p)
	}
}

// A size that stays keeps its table while others come and go, so the marks of its traffic and its
// routes are not renumbered under running connections.
func TestASizeKeepsItsMirrorTableWhileOthersComeAndGo(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1400}}}`, 0)
	w.overlay(`{target: {device: esp32-43}, fault: {family: mtu, mtu: {size: 1300}}}`, time.Second)
	first := w.compile(nil)
	if first.PMTUTables[1300] != 1 || first.PMTUTables[1400] != 2 {
		t.Fatalf("%v", first.PMTUTables)
	}
	// a smaller size joins: it must not push the others around
	w.overlay(`{target: {device: lab-host}, fault: {family: mtu, mtu: {size: 1100}}}`, 2*time.Second)
	second := w.compile(nil)
	if second.PMTUTables[1300] != 1 || second.PMTUTables[1400] != 2 || second.PMTUTables[1100] != 3 {
		t.Fatalf("%v", second.PMTUTables)
	}
	// the first goes: the others keep theirs, and a new one takes the free index
	w.overlays = []model.Overlay{w.overlays[1], w.overlays[2]} // the 1400 fault ends
	third := w.compile(nil)
	if third.PMTUTables[1300] != 1 || third.PMTUTables[1100] != 3 || len(third.PMTUTables) != 2 {
		t.Fatalf("%v", third.PMTUTables)
	}
	w.overlay(`{target: {global: true}, fault: {family: mtu, mtu: {size: 1200}, protocol: udp, ports: [9]}}`, 3*time.Second)
	fourth := w.compile(nil)
	if fourth.PMTUTables[1200] != 2 {
		t.Fatalf("%v", fourth.PMTUTables)
	}
	// the allocation does not depend on the order of the sizes: of two sizes that claim one index the smaller keeps it
	idx, ok := assignPMTUTables([]int{1200, 1000, 1100}, map[int]int{1200: 3, 1000: 3, 1100: 7})
	if !ok || idx[1000] != 3 || idx[1100] != 7 || idx[1200] != 1 {
		t.Errorf("%v %v", idx, ok)
	}
}

// Learned routes go into the mirror tables as well, each mirror with a kernel protocol of its own and
// the size locked in; without a mirror the configuration is what it was.
func TestBirdExportsLearnedRoutesIntoEveryMirrorTable(t *testing.T) {
	plain := withRouting(t, nil)
	if strings.Contains(plain.Bird.Text, "pmtu") {
		t.Fatalf("a mirror without an MTU fault:\n%s", plain.Bird.Text)
	}
	tg := withRouting(t, func(cfg *model.Configuration, in *Input) {
		in.Overlays = append(in.Overlays, mtuOverlay(t, cfg, `{target: {global: true}, fault: {family: mtu, mtu: {size: 1280}}}`),
			mtuOverlay(t, cfg, `{target: {global: true}, fault: {family: mtu, mtu: {size: 1400, mode: icmp}, protocol: tcp, ports: [443]}}`),
			mtuOverlay(t, cfg, `{target: {global: true}, fault: {family: mtu, mtu: {size: 1000, mode: blackhole}, protocol: udp, ports: [9]}}`))
	})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.PMTUTables) != 2 {
		t.Fatalf("%v", tg.PMTUTables)
	}
	text := tg.Bird.Text
	for _, want := range []string{
		"protocol kernel gw_pmtu103 {", "kernel table 103;", "krt_mtu = 1280; krt_lock_mtu = true;",
		"protocol kernel gw_pmtu104 {", "kernel table 104;", "krt_mtu = 1400;",
		"ipv4 table pmtu103;", "protocol pipe pmtu103_pipe {", "peer table pmtu104;",
		"protocol kernel gw_table {",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "krt_mtu = 1000") {
		t.Error("a black hole has a mirror table")
	}
	// the mirrors export what the table 100 protocol exports, nothing else
	exports := regexp.MustCompile(`export where source ~ \[[^\]]*\];`).FindAllString(text, -1)
	if len(exports) != 3 || exports[0] != exports[1] || exports[1] != exports[2] {
		t.Errorf("%v", exports)
	}
	if err := birdParsesFile(t, text); err != nil {
		t.Fatalf("bird rejects it: %v\n%s", err, text)
	}
	// the mirror table is also in the routes the executor writes
	if len(routesOf(tg, 103)) == 0 || len(routesOf(tg, 104)) == 0 {
		t.Error("no static routes in the mirrors")
	}
}

func mtuOverlay(t *testing.T, cfg *model.Configuration, body string) model.Overlay {
	t.Helper()
	w := &faultWorld{t: t, cfg: cfg}
	w.next = 100 + len(body)%50
	return w.overlay(body, time.Duration(len(body))*time.Millisecond)
}

// A fault that selects a hostname cannot classify yet (M20); the compile says so and writes no chain.
func TestAnMTUFaultThatNamesAHostnameIsReportedAndNotCompiled(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280}, destination: {hostname: example.com}}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() || len(tg.PMTU) != 0 {
		t.Fatalf("%+v %+v", tg.PMTU, tg.Problems)
	}
	found := false
	for _, p := range tg.Problems {
		found = found || (p.Code == CodeHostnameUnresolved && strings.Contains(p.Message, "MTU fault"))
	}
	if !found {
		t.Errorf("%+v", tg.Problems)
	}
}

// Golden: the MTU scenario, every mode next to an impairment, with the maps, chains and routes.
func scenarioPMTU(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, fault: {family: mtu, mtu: {size: 1400, mode: icmp}}}`, 0)
	w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280, mode: icmp}}}`, time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {family: mtu, mtu: {size: 1200, mode: blackhole}, protocol: tcp, ports: [443]}}`, 2*time.Second)
	w.overlay(`{target: {device: lab-host}, fault: {family: mtu, mtu: {size: 1360, mode: mss_clamp}}}`, 3*time.Second)
	w.overlay(`{target: {device: esp32-42}, fault: {latency: 30ms}}`, 4*time.Second)
	return w.compile(nil)
}

func describePMTU(tg *Target) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# mtu faults\n")
	for _, f := range tg.PMTU {
		fmt.Fprintf(&b, "%s %s  scope %q  size %d %s  index %d table %d  chain %s  counters %s %s %s\n", f.Layer, f.Source, f.Scope, f.Size, f.Mode, f.Index, f.Table, f.Chain, f.CounterUp, f.CounterDown, f.CounterDrop)
	}
	fmt.Fprintf(&b, "# tables by size: %v\n", tg.PMTUTables)
	fmt.Fprintf(&b, "# classification maps\n")
	for _, lv := range classifyLevels {
		m := findMap(tg, tg.PMTUMaps[lv.field])
		fmt.Fprintf(&b, "%s (%s, flags %v)\n", lv.field, strings.Join(m.KeyType, " . "), m.Flags)
		for _, e := range m.Elements {
			fmt.Fprintf(&b, "    %s : %s\n", e.Key, e.Value)
		}
	}
	fmt.Fprintf(&b, "# chains\n")
	for _, name := range append([]string{ClassifyChain, PMTUClassifyChain}, chainNamesWith(tg, pmtuChainPrefix)...) {
		fmt.Fprintf(&b, "%s\n", name)
		for _, r := range ruleJSON(findChain(tg, name)) {
			fmt.Fprintf(&b, "    %s\n", r)
		}
	}
	fmt.Fprintf(&b, "# routes and rules of the mirrors\n")
	for _, r := range tg.Routes {
		if r.Table != PolicyTable && r.Table != ServiceTable {
			fmt.Fprintf(&b, "route table %d %s via %q dev %s mtu %d\n", r.Table, r.Dst, r.Via, r.Dev, r.MTU)
		}
	}
	for _, r := range tg.Rules {
		if r.Fwmark != "" && r.Table != ServiceTable {
			fmt.Fprintf(&b, "rule %d fwmark %s lookup %d\n", r.Priority, r.Fwmark, r.Table)
		}
	}
	return b.String()
}

func chainNamesWith(tg *Target, prefix string) []string {
	var out []string
	for _, c := range tg.Nft.Chains {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c.Name)
		}
	}
	sort.Strings(out)
	return out
}

func TestGoldenPMTUTarget(t *testing.T) {
	tg := scenarioPMTU(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	goldenText(t, "pmtu", describePMTU(tg))
}
