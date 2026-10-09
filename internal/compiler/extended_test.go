package compiler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The compiler's side of the extended faults (M10): per-device rate (E9), duplication by the egress hook,
// the phase of a flapping, and the shapes side by side.

// E9 (plan §2.4): an overlay "Bad LTE" on the network IoT, and A and B download in parallel: 2 Mbit/s
// each. The compiler gives every device of the network a fault id and a class of its own in each
// direction, each with the rate of the profile, so that the two devices do not share a queue; the
// addresses of the network that no device owns share one more. Golden: faults-shapes.golden.txt holds a
// per-device rate next to the other shapes.
func TestE9ABadLTEProfileOnANetworkGivesEveryDeviceItsOwnQueueWithTheFullRate(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {network: IoT}, profile: bad-lte}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	// the golden file e9 is what the kernel gets: a fault id per device and one for the addresses no device owns, each with the
	// profile's rate in both directions, a class of its own on every interface, and the device addresses classified into their ids
	goldenText(t, "e9", describeFaults(tg))
	a := faultOf(t, tg, o.Id.String(), devESP42)
	b := faultOf(t, tg, o.Id.String(), devESP43)
	shared := faultOf(t, tg, o.Id.String(), "")
	if a.ID == b.ID || a.ID == shared.ID || b.ID == shared.ID {
		t.Fatalf("the devices share an id: %d %d %d", a.ID, b.ID, shared.ID)
	}
	for _, f := range []Fault{a, b, shared} {
		for _, d := range []Direction{Upload, Download} {
			n := f.netem(d)
			if n == nil || n.Rate != 2_000_000 {
				t.Errorf("fault %d %s: %+v, want the full 2 Mbit/s", f.ID, d, n)
			}
		}
	}
	// the download of each device has its own class, with a leaf of its own, on every interface
	got := map[string]bool{}
	for _, c := range tg.TC.Classes {
		if c.Dir == Download && c.Netem.Rate == 2_000_000 {
			got[c.ClassID()] = true
		}
	}
	for _, f := range []Fault{a, b} {
		if !got[ClassIDOf(f.ID, Download)] {
			t.Errorf("no download class for fault %d: %v", f.ID, got)
		}
	}
	// and each device's address is classified into its own id
	m := findMap(tg, tg.ClassifyMaps["dev"])
	var toA, toB bool
	for _, e := range m.Elements {
		switch e.Key {
		case "10.10.0.42":
			toA = e.Value == MarkChainName(a.ID)
		case "10.10.0.43":
			toB = e.Value == MarkChainName(b.ID)
		}
	}
	if !toA || !toB {
		t.Errorf("the devices are not classified into their ids: %+v", m.Elements)
	}
}

// ---- duplication ----------------------------------------------------------------------------------

func compileDup(t *testing.T, fault string) (*Target, Fault) {
	t.Helper()
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-43}, fault: `+fault+`}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	return tg, faultOf(t, tg, o.Id.String(), "")
}

func markRules(t *testing.T, tg *Target, f Fault) []string {
	t.Helper()
	c := findChain(tg, MarkChainName(f.ID))
	if c == nil {
		t.Fatalf("no chain for fault %d", f.ID)
	}
	var out []string
	for _, r := range c.Rules {
		b, _ := json.Marshal(r.Expr)
		out = append(out, string(b))
	}
	return out
}

// The probability of a duplicate is a draw in the mark chain, per packet and direction; the netem leaf
// of the class never duplicates, because the kernel refuses that next to any other netem.
func TestADuplicatingFaultDrawsInTheMarkChainAndItsLeafNeverDuplicates(t *testing.T) {
	tg, f := compileDup(t, `{upload: {latency: 20ms, duplicate: 10%}, download: {duplicate: 0.5%}}`)
	rules := markRules(t, tg, f)
	var up, down string
	for _, r := range rules {
		switch {
		case strings.Contains(r, `"numgen"`) && strings.Contains(r, `"original"`):
			up = r
		case strings.Contains(r, `"numgen"`) && strings.Contains(r, `"reply"`):
			down = r
		}
	}
	// 10 % of a modulus of 10^9, and 0.5 % of it; each sets the duplication bit and keeps the others
	for what, want := range map[string][2]string{"upload": {up, `"right":100000000`}, "download": {down, `"right":5000000`}} {
		if want[0] == "" || !strings.Contains(want[0], want[1]) || !strings.Contains(want[0], `"mod":1000000000`) ||
			!strings.Contains(want[0], `"mangle"`) || !strings.Contains(want[0], `"|"`) || !strings.Contains(want[0], "2097152") { // 1<<21
			t.Errorf("%s draw: %s (all: %v)", what, want[0], rules)
		}
	}
	if strings.Contains(strings.Join(rules, "\n"), "4294901775") == false {
		t.Errorf("the id is still written with the golden mask: %v", rules)
	}
	for _, c := range tg.TC.Classes {
		if c.ID == f.ID && strings.Contains(c.Netem.String(), "duplicate 0% 0%") == false {
			t.Errorf("the leaf of %s duplicates: %s", c.ClassID(), c.Netem)
		}
	}
	if !tg.TC.HasDup() {
		t.Error("the tree has no duplication hook")
	}
}

// 100 % is every packet: no draw, the bit is set. A fault without a duplicate has no rule for it and
// its tree has no hook.
func TestAHundredPercentDuplicateSetsTheBitWithoutADrawAndNoDuplicateHasNoHook(t *testing.T) {
	tg, f := compileDup(t, `{upload: {duplicate: 100%}}`)
	rules := markRules(t, tg, f)
	if len(rules) != 5 || strings.Contains(strings.Join(rules, ""), "numgen") || !strings.Contains(rules[1], `"original"`) || !strings.Contains(rules[1], "2097152") {
		t.Errorf("%v", rules)
	}
	tg, f = compileDup(t, `{upload: {latency: 20ms}}`)
	if rules := markRules(t, tg, f); len(rules) != 4 {
		t.Errorf("a fault that does not duplicate has %d rules: %v", len(rules), rules)
	}
	if tg.TC.HasDup() {
		t.Error("a tree without a duplicating fault has the hook")
	}
	for _, dev := range tg.TC.Devs {
		for _, l := range tg.TC.Lines(dev) {
			if strings.Contains(l, "clsact") || strings.Contains(l, "mirred") {
				t.Errorf("%s: %s", dev, l)
			}
		}
	}
}

// Every interface of the tree gets the hook, and only those: the target names them (the table itself is the
// executor's, executor.NftDup), and the tree's tc lines have nothing of it.
func TestEveryInterfaceOfTheTreeGetsTheDuplicationHook(t *testing.T) {
	tg, _ := compileDup(t, `{upload: {duplicate: 10%}}`)
	if len(tg.TC.Devs) < 3 {
		t.Fatalf("%v", tg.TC.Devs)
	}
	if strings.Join(tg.DupDevs, " ") != strings.Join(tg.TC.Devs, " ") {
		t.Errorf("the hook is on %v, the tree on %v", tg.DupDevs, tg.TC.Devs)
	}
	for _, dev := range tg.TC.Devs {
		for _, l := range tg.TC.Lines(dev) {
			if strings.Contains(l, "clsact") || strings.Contains(l, "mirred") || strings.Contains(l, "egress") {
				t.Errorf("%s: the hook is not a tc object: %s", dev, l)
			}
		}
	}
	// a target without a duplicating fault has no hook, and a fault that duplicates nothing neither
	plain, _ := compileDup(t, `{upload: {latency: 20ms}}`)
	if len(plain.DupDevs) != 0 {
		t.Errorf("%v", plain.DupDevs)
	}
	if none := newFaultWorld(t).compile(nil); len(none.DupDevs) != 0 {
		t.Errorf("%v", none.DupDevs)
	}
}

func TestTheDuplicationBitIsAReservedRoutingMarkBitThatNothingElseUses(t *testing.T) {
	if MarkDupBit != 1<<21 {
		t.Fatalf("%#x", MarkDupBit)
	}
	// bits 21 to 23 are reserved for further routing marks (plan §3.3); bit 20 selects the service
	// namespace, the id and the direction are below: no overlap
	for name, other := range map[string]uint32{"id": markIDMaskBits, "direction": markDirMaskBits, "pmtu": 7 << 17, "service": 1 << 20} {
		if MarkDupBit&other != 0 {
			t.Errorf("the duplication bit overlaps the %s bits", name)
		}
	}
	if MarkMask&MarkDupBit != 0 {
		t.Error("the fw filters of the classes would look at the duplication bit")
	}
	if dupThreshold(100) != DupResolution || dupThreshold(0) != 0 || dupThreshold(50) != DupResolution/2 || dupThreshold(0.0000001) != 1 {
		t.Errorf("thresholds %d %d %d %d", dupThreshold(100), dupThreshold(0), dupThreshold(50), dupThreshold(0.0000001))
	}
}

// ---- flapping -------------------------------------------------------------------------------------

func TestAFlappingClassHoldsTheBlackoutInTheDownPhaseAndKeepsEverythingElse(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {upload: {latency: 30ms, rate: 5Mbit, flapping: {up: 20s, down: 10s}}, download: {latency: 5ms}}}`, 0)
	up := w.compile(nil)
	var asked []string
	down := w.compile(func(in *Input) {
		in.FlapPhase = func(key string, f FlapSpec) bool {
			asked = append(asked, key)
			return f.Up == 20*time.Second && f.Down == 10*time.Second
		}
	})
	f := faultOf(t, up, o.Id.String(), devESP42)
	var upClass, downClass, upDown, downDown TCClass
	for _, c := range up.TC.Classes {
		if c.ID == f.ID {
			if c.Dir == Upload {
				upClass = c
			} else {
				downClass = c
			}
		}
	}
	for _, c := range down.TC.Classes {
		if c.ID == f.ID && c.Dir == Upload {
			upDown = c
		} else if c.ID == f.ID {
			downDown = c
		}
	}
	// only the direction that flaps is asked about, and it is up unless the engine says otherwise
	if len(asked) != 1 || !strings.HasSuffix(asked[0], ":impairment|upload") || strings.Contains(asked[0], "@") {
		t.Errorf("the phase was asked for %v: one flap key per fault and direction, without the device", asked)
	}
	if upClass.Down || upClass.FlapKey != asked[0] || downClass.FlapKey != "" {
		t.Errorf("up: %+v download: %+v", upClass, downClass)
	}
	if !upDown.Down || downDown.Down {
		t.Errorf("down phase: %+v / %+v", upDown, downDown)
	}
	// the down phase is the up configuration with loss 100 %: delay, rate and limit stay
	want := "netem limit 1000 delay 30ms 0ms 0% loss random 100% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 5Mbit"
	if got := upDown.Config().String(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if got := upClass.Config().String(); !strings.Contains(got, "loss random 0% 0%") || !strings.Contains(got, "rate 5Mbit") {
		t.Errorf("up phase: %s", got)
	}
	// and the tree the verifier compares with says the same, in the listing's form
	for _, q := range down.TC.Norm("br-iot").Qdiscs {
		if q.Parent == upDown.ClassID() && (q.Netem.Loss != 1 || q.Netem.Delay != 0.03 || q.Netem.Rate != 625000) {
			t.Errorf("%+v", q.Netem)
		}
	}
}

// A per-device fault flaps in step on all of its devices: the flap key is the fault's, not the device's.
func TestAllDevicesOfAPerDeviceFlappingFaultShareOneFlapKey(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, fault: {rate: 2Mbit, flapping: {up: 5s, down: 5s}}}`, 0)
	tg := w.compile(nil)
	keys := map[string][]int{}
	for _, c := range tg.TC.Classes {
		keys[c.FlapKey] = append(keys[c.FlapKey], c.ID)
	}
	if len(keys) != 2 { // upload and download
		t.Fatalf("%v", keys)
	}
	for k, ids := range keys {
		if len(ids) != 3 { // two devices and the shared queue of the unowned addresses
			t.Errorf("%s: classes of the fault ids %v, want one per device and the shared one", k, ids)
		}
	}
	if FlapKey("overlay:x:impairment@dev", Download) != "overlay:x:impairment|download" || FlapKey("overlay:x:impairment", Upload) != "overlay:x:impairment|upload" {
		t.Error("FlapKey")
	}
}

// ---- every shape side by side -----------------------------------------------------------------------

func TestEveryShapeOfExtendedFaultsCompilesToItsLeaf(t *testing.T) {
	tg := scenarioShapes(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	lines := strings.Join(tg.TC.Lines("br-iot"), "\n")
	for what, want := range map[string]string{
		"rate and queue limit":         "netem limit 300 delay 0ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 2Mbit",
		"reorder with a delay":         "delay 50ms 10ms 0% loss random 0% 0% reorder 25% 0%",
		"burst loss and corrupt":       "loss gemodel 2% 20% 90% 1% reorder 0% 0% duplicate 0% 0% corrupt 1.5% 0%",
		"blackout":                     "limit 1000 delay 0ms 0ms 0% loss random 100% 0%",
		"flapping, down, with a delay": "limit 2500 delay 30ms 0ms 0% loss random 100% 0%",
		"duplicate leaves netem alone": "delay 50ms 10ms 0% loss random 0% 0% reorder 25% 0% duplicate 0% 0%",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("%s: no %q in\n%s", what, want, lines)
		}
	}
	if len(tg.DupDevs) != len(tg.TC.Devs) {
		t.Errorf("the hook is on %v", tg.DupDevs)
	}
	// every duplicate is a draw or, at 100 %, a plain flag
	var draws, flags int
	for _, c := range tg.Nft.Chains {
		if !strings.HasPrefix(c.Name, "mark_") {
			continue
		}
		for _, r := range c.Rules {
			b, _ := json.Marshal(r.Expr)
			switch s := string(b); {
			case strings.Contains(s, "numgen"):
				draws++
			case strings.Contains(s, "2097152"):
				flags++
			}
		}
	}
	if draws != 1 || flags != 2 { // 5 % upload of esp32-43; 100 % both directions of the lab host
		t.Errorf("%d draws and %d flags", draws, flags)
	}
}

// The mark chains of the shapes scenario with their rules in full, in the nftables JSON the executor
// sends: the id write with the golden mask, the draws and flags of the duplicating faults, the counters.
func TestGoldenShapesMarkChains(t *testing.T) {
	tg := scenarioShapes(t)
	var b strings.Builder
	for _, c := range tg.Nft.Chains {
		if !strings.HasPrefix(c.Name, markChainPrefix) {
			continue
		}
		b.WriteString(c.Name + "\n")
		for _, r := range c.Rules {
			j, _ := json.Marshal(r.Expr)
			b.WriteString("    " + string(j) + "\n")
		}
	}
	goldenText(t, "faults-shapes-marks", b.String())
}
