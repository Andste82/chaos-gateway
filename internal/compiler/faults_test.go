package compiler

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ---- scenarios -----------------------------------------------------------------------------------

// scenarioMixed is the fault scenario of the golden files: a per-device rate on a network (E9),
// a port fault on one device (E3) and an asymmetric destination fault for everyone (E7's range).
func scenarioMixed(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, fault: {latency: 150ms, jitter: 50ms, loss: 3%, rate: 2Mbit}}`, 0)
	w.overlay(`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`, time.Second)
	w.overlay(`{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/24}, download: {latency: 20ms}}}`, 2*time.Second)
	w.overlay(`{target: {device: lab-host}, fault: {flapping: {up: 20s, down: 10s}, reorder: 10%, latency: 30ms, distribution: pareto}}`, 3*time.Second)
	return w.compile(nil)
}

// scenarioNested has nested and partially overlapping selectors on the same level, the case the
// interval maps cannot hold as they are (plan §3.2): the compiler must split them.
func scenarioNested(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.overlay(`{target: {global: true}, fault: {destination: {cidr: 10.0.0.0/8}, latency: 10ms}}`, 0)
	w.overlay(`{target: {global: true}, fault: {destination: {cidr: 10.1.0.0/16}, latency: 20ms}}`, time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {protocol: tcp, port_ranges: [{from: 80, to: 443}], loss: 1%}}`, 2*time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {protocol: tcp, port_ranges: [{from: 400, to: 500}], loss: 2%}}`, 3*time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {protocol: udp, ports: [53], destination: {cidr: 198.51.100.0/25}, latency: 5ms}}`, 4*time.Second)
	return w.compile(nil)
}

// scenarioNeutral has a device fault that impairs nothing, which still shadows the network fault.
func scenarioNeutral(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, fault: {latency: 100ms}}`, 0)
	w.overlay(`{target: {device: esp32-42}, fault: {destination: {cidr: 203.0.113.0/24}, latency: 0ms}}`, time.Second)
	return w.compile(nil)
}

// scenarioShapes has every kind of netem leaf the fault family of M10 can produce, side by side on
// one interface tree: a per-device rate with a queue limit, reordering, a duplicating fault next to
// other netems (the kernel refuses a duplicating netem there, P2-M10-01), burst loss, corruption,
// a blackout and a flapping one that is in its down phase.
func scenarioShapes(t *testing.T) *Target {
	t.Helper()
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, fault: {rate: 2Mbit, queue_limit: 300}}`, 0)
	w.overlay(`{target: {device: esp32-43}, fault: {upload: {latency: 50ms, jitter: 10ms, reorder: 25%, duplicate: 5%}, download: {burst_loss: {p: 2%, r: 20%, h: 10%, k: 99%}, corrupt: 1.5%}}}`, time.Second)
	w.overlay(`{target: {device: lab-host}, fault: {destination: {cidr: 203.0.113.0/24}, blackout: true}}`, 2*time.Second)
	w.overlay(`{target: {device: lab-host}, fault: {destination: {cidr: 198.51.100.0/24}, flapping: {up: 20s, down: 10s}, latency: 30ms, duplicate: 100%}}`, 3*time.Second)
	up := w.compile(nil)
	down := map[string]bool{}
	for _, c := range up.TC.Classes {
		if c.FlapKey != "" {
			down[c.FlapKey] = true
		}
	}
	return w.compile(func(in *Input) { in.FlapPhase = func(key string, _ FlapSpec) bool { return down[key] } })
}

func faultScenarios(t *testing.T) map[string]*Target {
	return map[string]*Target{
		"faults-mixed":   scenarioMixed(t),
		"faults-nested":  scenarioNested(t),
		"faults-neutral": scenarioNeutral(t),
		"faults-shapes":  scenarioShapes(t),
		// profiles (M11): the precedence of a profile's parts against faults, and the catalogue expanded
		"faults-profiles":         scenarioProfilePrecedence(t, false),
		"faults-builtin-profiles": scenarioBuiltinProfiles(t),
	}
}

// ---- golden files -------------------------------------------------------------------------------

func goldenText(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file (run with -update to accept):\n%s", name, diffLines(string(want), got))
	}
}

// describeFaults is the readable form of a target's faults for the golden files: the ids, what
// they stand for, the tc tree of one interface, the classification maps and the counters.
func describeFaults(tg *Target) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# faults\n")
	for _, f := range tg.Faults {
		dev := f.Device
		if dev == "" {
			dev = "-"
		}
		fmt.Fprintf(&b, "id %d  %s %s  scope %q  device %s  counters %s %s\n", f.ID, f.Layer, f.Source, f.Scope, dev, f.CounterUp, f.CounterDown)
		for _, d := range []Direction{Upload, Download} {
			if n := f.netem(d); n != nil {
				fmt.Fprintf(&b, "    %s: %s\n", d, n.Summary())
			} else {
				fmt.Fprintf(&b, "    %s: unimpaired\n", d)
			}
		}
	}
	if tg.TC != nil {
		fmt.Fprintf(&b, "# tc on %s\n", strings.Join(tg.TC.Devs, " "))
		for _, l := range tg.TC.Lines(tg.TC.Devs[0]) {
			b.WriteString(l + "\n")
		}
	}
	fmt.Fprintf(&b, "# classification maps\n")
	for _, lv := range classifyLevels {
		m := findMap(tg, tg.ClassifyMaps[lv.field])
		fmt.Fprintf(&b, "%s (%s, flags %v)\n", lv.field, strings.Join(m.KeyType, " . "), m.Flags)
		for _, e := range m.Elements {
			fmt.Fprintf(&b, "    %s : %s\n", e.Key, e.Value)
		}
	}
	fmt.Fprintf(&b, "# mark chains\n")
	for _, c := range tg.Nft.Chains {
		if strings.HasPrefix(c.Name, markChainPrefix) {
			fmt.Fprintf(&b, "%s: %d rules\n", c.Name, len(c.Rules))
		}
	}
	var counters []string
	for _, c := range tg.Nft.Counters {
		if strings.HasPrefix(c, "fault_") {
			counters = append(counters, c)
		}
	}
	fmt.Fprintf(&b, "# fault counters\n%s\n", strings.Join(counters, "\n"))
	return b.String()
}

func TestGoldenFaultTargets(t *testing.T) {
	for name, tg := range faultScenarios(t) {
		t.Run(name, func(t *testing.T) {
			if tg.HasErrors() {
				t.Fatalf("%+v", tg.Problems)
			}
			goldenText(t, name, describeFaults(tg))
		})
	}
}

// The golden test of the nftables side: the transaction of the mixed scenario, with its interval
// maps, mark chains and counters.
func TestGoldenFaultNftTransaction(t *testing.T) {
	tg := scenarioMixed(t)
	tx, err := tg.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	var pretty any
	_ = json.Unmarshal(tx, &pretty)
	golden(t, "faults.nft", pretty)
}

// ---- the tc tree --------------------------------------------------------------------------------

func TestTheTCTreeHasRootDefaultClassAndOneClassPerIDAndDirection(t *testing.T) {
	tg := scenarioMixed(t)
	lines := tg.TC.Lines("br-iot")
	if lines[0] != "qdisc add dev br-iot root handle 1: htb default 1" {
		t.Errorf("root: %s", lines[0])
	}
	if lines[1] != "class replace dev br-iot parent 1: classid 1:1 htb rate 10gbit quantum 60000" {
		t.Errorf("default class: %s", lines[1])
	}
	classes := 0
	for _, f := range tg.Faults {
		for _, d := range []Direction{Upload, Download} {
			if f.netem(d) != nil {
				classes++
			}
		}
	}
	if len(tg.TC.Classes) != classes {
		t.Errorf("%d classes, want one per impaired (id, direction): %d", len(tg.TC.Classes), classes)
	}
	// 2 for the root and the default class, then class, leaf and filter per (id, direction)
	if got, want := len(lines), 2+3*classes; got != want {
		t.Errorf("%d tc commands, want %d", got, want)
	}
	if tg.TC.ClassesPerDevice() != classes+1 {
		t.Errorf("ClassesPerDevice = %d", tg.TC.ClassesPerDevice())
	}
}

// Plan §3.3: "id 0x0a: handle 0x000a0/0x1fff0 for upload, 0x100a0/0x1fff0 for download".
func TestTheFwFilterMasksFollowTheMarkLayout(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {latency: 50ms}}`, 0)
	tg := w.compile(func(in *Input) { in.FaultIDs = map[string]int{"overlay:" + o.Id.String() + ":impairment": 0x0a} })
	if len(tg.Faults) != 1 || tg.Faults[0].ID != 0x0a {
		t.Fatalf("%+v", tg.Faults)
	}
	lines := strings.Join(tg.TC.Lines("wan0"), "\n")
	for _, want := range []string{
		"filter replace dev wan0 parent 1: handle 0x000a0/0x1fff0 protocol ip prio 1 fw flowid 1:24",
		"filter replace dev wan0 parent 1: handle 0x100a0/0x1fff0 protocol ip prio 1 fw flowid 1:25",
		"qdisc replace dev wan0 parent 1:24 handle 24: netem ",
		"qdisc replace dev wan0 parent 1:25 handle 25: netem ",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q in:\n%s", want, lines)
		}
	}
	if MarkMask != 0x1fff0 {
		t.Errorf("MarkMask = %#x", MarkMask)
	}
	// the mark a class is selected by is exactly what the mark chain of the id writes
	for _, c := range tg.TC.Classes {
		if c.Mark != uint32(0x0a)<<4|uint32(c.Dir)<<16 {
			t.Errorf("class %s mark %#x", c.ClassID(), c.Mark)
		}
	}
}

func TestClassAndLeafNumbersNeverCollide(t *testing.T) {
	seen := map[int]string{}
	for id := 1; id <= MarkIDMax; id++ {
		for _, d := range []Direction{Upload, Download} {
			m := classMinor(id, d)
			if m <= TCDefaultMinor || m > 0xffff {
				t.Fatalf("id %d %s: minor %#x", id, d, m)
			}
			if prev, dup := seen[m]; dup {
				t.Fatalf("id %d %s collides with %s", id, d, prev)
			}
			seen[m] = fmt.Sprintf("id %d %s", id, d)
			// the leaf's handle (major) must not be the root's "1:"
			if c := (TCClass{Minor: m}); c.LeafHandle() == TCRootHandle {
				t.Fatalf("leaf handle of %#x is the root", m)
			}
		}
	}
}

func TestNoFaultNoTreeAndNoElements(t *testing.T) {
	w := newFaultWorld(t)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if tg.TC != nil || len(tg.Faults) != 0 || len(tg.FaultIDs) != 0 {
		t.Errorf("tc %v faults %v", tg.TC, tg.Faults)
	}
	for _, lv := range classifyLevels {
		if m := findMap(tg, tg.ClassifyMaps[lv.field]); len(m.Elements) != 0 {
			t.Errorf("%s has elements: %v", lv.field, m.Elements)
		}
	}
	if (*TCTarget)(nil).Entries("wan0", true) != nil || len(tg.TC.Lines("wan0")) != 0 {
		t.Error("a nil tree has commands")
	}
}

func TestTheTreeIsOnEveryInterfaceClassifiedTrafficLeavesThrough(t *testing.T) {
	tg := scenarioMixed(t)
	var bridges []string
	for _, b := range tg.Bridges {
		bridges = append(bridges, b.Name)
	}
	want := append(append([]string{}, bridges...), tg.Uplink.Name)
	sort.Strings(want)
	if strings.Join(tg.TC.Devs, ",") != strings.Join(want, ",") {
		t.Errorf("devs %v, want %v", tg.TC.Devs, want)
	}
	// with a service namespace its host side carries it, too, and so does every WireGuard interface
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {latency: 50ms}}`, 0)
	tg = w.compile(func(in *Input) { in.ServiceNS = "cgsvc" })
	if !contains(tg.TC.Devs, ServiceHostIf) {
		t.Errorf("devs %v lack %s", tg.TC.Devs, ServiceHostIf)
	}

	// and so does every WireGuard interface
	ww := newFaultWorldFile(t, "wireguard.yaml")
	ww.overlay(`{target: {global: true}, fault: {latency: 10ms}}`, 0)
	tg = ww.compile(func(in *Input) { in.Keys = wgKeys })
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	for _, wg := range tg.WireGuard {
		if !contains(tg.TC.Devs, wg.Name) {
			t.Errorf("devs %v lack the WireGuard interface %s", tg.TC.Devs, wg.Name)
		}
	}
	if len(tg.WireGuard) == 0 {
		t.Error("the fixture has no WireGuard interface")
	}
}

// ---- complete parameter sets --------------------------------------------------------------------

func TestEveryNetemAttributeIsWrittenWithNeutralValues(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {latency: 100ms}}`, 0)
	tg := w.compile(nil)
	f := faultOf(t, tg, o.Id.String(), "")
	const want = "netem limit 8334 delay 100ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"
	if got := f.Upload.String(); got != want {
		t.Errorf("upload:\n got %s\nwant %s", got, want)
	}
	if f.Upload.String() != f.Download.String() {
		t.Errorf("flat parameters apply to both directions: %s / %s", f.Upload, f.Download)
	}
	// a loss-only fault resets the delay; a delay-only fault resets the loss (the previous values
	// must not survive a `tc qdisc change`, spike S2)
	for _, kw := range []string{"delay", "loss", "reorder", "duplicate", "corrupt", "rate", "limit"} {
		if !strings.Contains(f.Upload.String(), kw) {
			t.Errorf("the set lacks %q", kw)
		}
	}
}

func TestNetemRendering(t *testing.T) {
	for name, tc := range map[string]struct {
		fault, want string
	}{
		"jitter and distribution":       {`{latency: 200ms, jitter: 50ms, distribution: normal}`, "netem limit 20834 delay 200ms 50ms 0% distribution normal loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"a distribution needs a jitter": {`{latency: 10ms, distribution: pareto}`, "netem limit 1000 delay 10ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"uniform is no table":           {`{latency: 10ms, jitter: 1ms, distribution: uniform}`, "netem limit 1000 delay 10ms 1ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"microseconds":                  {`{latency: 1500us}`, "netem limit 1000 delay 1500us 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"seconds":                       {`{latency: 1.5s}`, "netem limit 89478 delay 1500ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"loss with correlation":         {`{loss: 5%, loss_correlation: 25%}`, "netem limit 1000 delay 0ms 0ms 0% loss random 5% 25% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"fractions":                     {`{loss: 0.5%, corrupt: 0.01%}`, "netem limit 1000 delay 0ms 0ms 0% loss random 0.5% 0% reorder 0% 0% duplicate 0% 0% corrupt 0.01% 0% rate 0bit"},
		"a duplicate is not netem's":    {`{latency: 10ms, duplicate: 1.25%}`, "netem limit 1000 delay 10ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"burst loss defaults":           {`{burst_loss: {p: 1%, r: 30%}}`, "netem limit 1000 delay 0ms 0ms 0% loss gemodel 1% 30% 100% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"burst loss with h and k":       {`{burst_loss: {p: 1%, r: 30%, h: 10%, k: 99%}}`, "netem limit 1000 delay 0ms 0ms 0% loss gemodel 1% 30% 90% 1% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"blackout":                      {`{blackout: true}`, "netem limit 1000 delay 0ms 0ms 0% loss random 100% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"rate in kbit":                  {`{rate: 512kbit}`, "netem limit 1000 delay 0ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 512kbit"},
		"fractional Mbit":               {`{rate: 1.5Mbit}`, "netem limit 1000 delay 0ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 1500kbit"},
		"keep order is a rate":          {`{latency: 10ms, jitter: 5ms, keep_order: true}`, "netem limit 1250 delay 10ms 5ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 1Gbit"},
		"keep order, own rate":          {`{latency: 10ms, rate: 5Mbit, keep_order: true}`, "netem limit 1000 delay 10ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 5Mbit"},
		"reorder with delay":            {`{latency: 10ms, reorder: 25%}`, "netem limit 1000 delay 10ms 0ms 0% loss random 0% 0% reorder 25% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"explicit queue limit":          {`{latency: 600ms, queue_limit: 50}`, "netem limit 50 delay 600ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"flapping starts up":            {`{flapping: {up: 20s, down: 10s}}`, "netem limit 1000 delay 0ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
		"computed limit, satellite":     {`{latency: 600ms}`, "netem limit 50000 delay 600ms 0ms 0% loss random 0% 0% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newFaultWorld(t)
			o := w.overlay(`{target: {device: esp32-42}, fault: `+tc.fault+`}`, 0)
			tg := w.compile(nil)
			if tg.HasErrors() {
				t.Fatalf("%+v", tg.Problems)
			}
			// a rate, queue limit or keep order makes the id per device: look the fault up by source
			var f *Fault
			for i := range tg.Faults {
				if tg.Faults[i].Source == o.Id.String() {
					f = &tg.Faults[i]
				}
			}
			if f == nil {
				t.Fatalf("no fault: %+v", tg.Faults)
			}
			if got := f.Upload.String(); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestFlappingKeepsTheUpPhaseAndKnowsTheDownPhase(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {flapping: {up: 20s, down: 10s}, latency: 30ms}}`, 0)
	f := faultOf(t, w.compile(nil), o.Id.String(), "")
	if f.Upload.Loss != 0 || f.Upload.Flapping == nil || f.Upload.Flapping.Up != 20*time.Second || f.Upload.Flapping.Down != 10*time.Second {
		t.Fatalf("up phase: %+v", f.Upload)
	}
	down := f.Upload.Down()
	if !strings.Contains(down.String(), "loss random 100% 0%") || down.Flapping != nil || !strings.Contains(down.String(), "delay 30ms") {
		t.Errorf("down phase: %s", down)
	}
}

func TestAJitterAboveTheDelayIsClamped(t *testing.T) {
	n, err := netemFrom(&model.NetemParams{Latency: ptr("10ms"), Jitter: ptr("50ms")})
	if err != nil || n.Jitter != 10*time.Millisecond {
		t.Errorf("%+v %v", n, err)
	}
}

func TestReorderWithoutDelayIsDropped(t *testing.T) {
	n, err := netemFrom(&model.NetemParams{Reorder: ptr("10%")})
	if err != nil || n.Reorder != 0 {
		t.Errorf("%+v %v", n, err)
	}
}

func TestQueueLimitsAreComputedFromDelayAndRate(t *testing.T) {
	budget := int64(DefaultQueueBudget)
	for name, tc := range map[string]struct {
		n       Netem
		classes int
		want    int
	}{
		"no delay: netem's default":           {Netem{}, 2, 1000},
		"600 ms at the 1 Gbit cap":            {Netem{Delay: 600 * time.Millisecond}, 2, 50000},
		"jitter counts: the worst delay":      {Netem{Delay: 500 * time.Millisecond, Jitter: 100 * time.Millisecond}, 2, 50000},
		"rate instead of the cap":             {Netem{Delay: 600 * time.Millisecond, Rate: 100_000_000}, 2, 5000},
		"a slow link keeps the floor":         {Netem{Delay: 600 * time.Millisecond, Rate: 2_000_000}, 2, 1000},
		"the budget share is below the floor": {Netem{Delay: 10 * time.Second}, 200, 1000}, // 10 s * 1 Gbit = 833334 packets; share 256 MiB / 1500 / 200 = 895
	} {
		t.Run(name, func(t *testing.T) {
			if got := computedLimit(tc.n, tc.classes, budget); got != tc.want {
				t.Errorf("limit %d, want %d", got, tc.want)
			}
		})
	}
	// the budget is split among the classes, above the floor
	if got := computedLimit(Netem{Delay: 10 * time.Second}, 10, budget); got != int(budget/QueuePacketSize/10) {
		t.Errorf("share of 10 classes: %d", got)
	}
	if got := computedLimit(Netem{Delay: 600 * time.Millisecond}, 2, 1<<20); got != MinQueueLimit {
		t.Errorf("a tiny budget still leaves netem's default: %d", got)
	}
}

func TestAnExplicitQueueLimitIsNeverCapped(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {latency: 5s, queue_limit: 900000}}`, 0)
	tg := w.compile(func(in *Input) { in.QueueBudget = 1 << 20 })
	f := faultOf(t, tg, o.Id.String(), devESP42)
	if f.Upload.Limit != 900000 || !f.Upload.LimitExplicit {
		t.Errorf("%+v", f.Upload)
	}
}

// ---- directions -----------------------------------------------------------------------------------

func TestAMissingDirectionIsUnimpairedAndHasNoClass(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {upload: {latency: 80ms}}}`, 0)
	tg := w.compile(nil)
	f := faultOf(t, tg, o.Id.String(), "")
	if f.Upload == nil || f.Download != nil {
		t.Fatalf("%+v %+v", f.Upload, f.Download)
	}
	if len(tg.TC.Classes) != 1 || tg.TC.Classes[0].Dir != Upload {
		t.Errorf("classes: %+v", tg.TC.Classes)
	}
	// the id is still written for both directions: the download simply meets the default class
	m := findMap(tg, tg.ClassifyMaps["dev"])
	if len(m.Elements) != 1 || m.Elements[0].Value != MarkChainName(f.ID) {
		t.Errorf("dev map: %+v", m.Elements)
	}
}

func TestAsymmetricDirectionsHaveTheirOwnParameters(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {upload: {latency: 200ms, jitter: 20ms}, download: {burst_loss: {p: 1%, r: 30%}}}}`, 0)
	f := faultOf(t, w.compile(nil), o.Id.String(), "")
	if !strings.Contains(f.Upload.String(), "delay 200ms 20ms") || strings.Contains(f.Upload.String(), "gemodel") {
		t.Errorf("upload %s", f.Upload)
	}
	if !strings.Contains(f.Download.String(), "gemodel 1% 30% 100% 0%") || !strings.Contains(f.Download.String(), "delay 0ms 0ms") {
		t.Errorf("download %s", f.Download)
	}
}

// ---- ids ------------------------------------------------------------------------------------------

func TestFaultIDsAreStableWhileTheWinnerStays(t *testing.T) {
	w := newFaultWorld(t)
	a := w.overlay(`{target: {device: esp32-42}, fault: {latency: 50ms}}`, 0)
	first := w.compile(nil)
	idA := faultOf(t, first, a.Id.String(), "").ID

	// another fault arrives: the first keeps its id
	b := w.overlay(`{target: {device: esp32-43}, fault: {latency: 70ms}}`, time.Second)
	second := w.compile(nil)
	if got := faultOf(t, second, a.Id.String(), "").ID; got != idA {
		t.Errorf("a's id changed from %d to %d", idA, got)
	}
	idB := faultOf(t, second, b.Id.String(), "").ID
	if idB == idA {
		t.Error("two faults share an id")
	}

	// the parameters of a change: same overlay (same id of the overlay), same fault id, same
	// counters, and a different netem configuration
	w.overlays[0].Fault.Latency = ptr("500ms")
	third := w.compile(nil)
	fa := faultOf(t, third, a.Id.String(), "")
	if fa.ID != idA || !strings.Contains(fa.Upload.String(), "delay 500ms") {
		t.Errorf("after the change: %+v", fa)
	}
	if fa.CounterUp != faultOf(t, first, a.Id.String(), "").CounterUp {
		t.Error("the counters were renamed by a parameter change")
	}
}

func TestAReleasedIDIsNotHandedToTheNextFaultAtOnce(t *testing.T) {
	w := newFaultWorld(t)
	a := w.overlay(`{target: {device: esp32-42}, fault: {latency: 50ms}}`, 0)
	first := w.compile(nil)
	idA := faultOf(t, first, a.Id.String(), "").ID
	// a goes, b arrives: b must not take a's id while a's queues may still hold packets (the
	// make-before-break of M8b relies on old and new ids being different)
	w.overlays = nil
	b := w.overlay(`{target: {device: esp32-42}, fault: {latency: 60ms}}`, time.Second)
	second := w.compile(nil)
	idB := faultOf(t, second, b.Id.String(), "").ID
	if idB == idA {
		t.Errorf("b took the id %d that a released in the same step", idA)
	}
	// the id comes back once the allocation no longer remembers it
	third := w.compile(nil)
	if got := faultOf(t, third, b.Id.String(), "").ID; got != idB {
		t.Errorf("b's id changed from %d to %d", idB, got)
	}
}

func TestIDAllocationWithoutHistoryIsDeterministic(t *testing.T) {
	build := func() *Target {
		w := newFaultWorld(t)
		w.overlay(`{target: {device: esp32-42}, fault: {latency: 50ms}}`, 0)
		w.overlay(`{target: {device: esp32-43}, fault: {latency: 70ms}}`, time.Second)
		return w.compile(nil)
	}
	a, b := build(), build()
	if fmt.Sprint(a.FaultIDs) != fmt.Sprint(b.FaultIDs) || a.Hash != b.Hash {
		t.Errorf("%v / %v", a.FaultIDs, b.FaultIDs)
	}
	if got := assertSortedIDs(a.Faults); !got {
		t.Error("faults are not sorted by id")
	}
}

func assertSortedIDs(fs []Fault) bool {
	return sort.SliceIsSorted(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
}

func TestAssignFaultIDs(t *testing.T) {
	ids, ok := assignFaultIDs([]string{"c", "a", "b"}, nil, nil)
	if !ok || ids["a"] != 1 || ids["b"] != 2 || ids["c"] != 3 {
		t.Errorf("%v", ids)
	}
	// keep what was, add the new one above, skip released ids
	ids, ok = assignFaultIDs([]string{"a", "c", "d"}, map[string]int{"a": 1, "b": 2, "c": 3}, nil)
	if !ok || ids["a"] != 1 || ids["c"] != 3 || ids["d"] != 4 {
		t.Errorf("%v", ids)
	}
	// a damaged previous allocation (a duplicate, an out-of-range id) does not give two keys one id
	ids, ok = assignFaultIDs([]string{"a", "b", "c"}, map[string]int{"a": 5, "b": 5, "c": 99999}, nil)
	if !ok || len(map[int]bool{ids["a"]: true, ids["b"]: true, ids["c"]: true}) != 3 || ids["c"] > MarkIDMax || ids["c"] < 1 {
		t.Errorf("%v", ids)
	}
	// all 4095 ids can be used, then it is over
	var keys []string
	for i := 0; i < MarkIDMax; i++ {
		keys = append(keys, fmt.Sprintf("k%04d", i))
	}
	if ids, ok = assignFaultIDs(keys, nil, nil); !ok || len(ids) != MarkIDMax {
		t.Fatalf("%d ids, ok %v", len(ids), ok)
	}
	if _, ok = assignFaultIDs(append(keys, "one too many"), nil, nil); ok {
		t.Error("4096 keys got ids")
	}
	// reusing released ids when nothing else is free
	prev := map[string]int{}
	for i, k := range keys {
		prev[k] = i + 1
	}
	next := append([]string{}, keys[1:]...)
	next = append(next, "fresh")
	if ids, ok = assignFaultIDs(next, prev, nil); !ok || ids["fresh"] != 1 {
		t.Errorf("fresh got %d (ok %v), want the released 1", ids["fresh"], ok)
	}
}

// ---- per-device queues (D18) ------------------------------------------------------------------

func TestARateFaultOnANetworkGetsOneIDPerDeviceAndOneForTheUnknownOnes(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {network: IoT}, fault: {latency: 150ms, rate: 2Mbit}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	f42, f43, shared := faultOf(t, tg, o.Id.String(), devESP42), faultOf(t, tg, o.Id.String(), devESP43), faultOf(t, tg, o.Id.String(), "")
	ids := map[int]bool{f42.ID: true, f43.ID: true, shared.ID: true}
	if len(ids) != 3 || len(tg.Faults) != 3 {
		t.Fatalf("faults %+v", tg.Faults)
	}
	// every id has its own counters
	if f42.CounterUp == f43.CounterUp || f42.CounterUp == shared.CounterUp {
		t.Error("devices share a counter")
	}
	// lab-host is in Lab: the IoT fault does not reach it
	for _, f := range tg.Faults {
		if f.Device == devLab {
			t.Error("a device of another network got an id")
		}
	}
	// and the maps say which address belongs to which id
	m := findMap(tg, tg.ClassifyMaps["dev"])
	got := map[string]string{}
	for _, e := range m.Elements {
		got[e.Key] = e.Value
	}
	if got["10.10.0.42"] != MarkChainName(f42.ID) || got["10.10.0.43"] != MarkChainName(f43.ID) {
		t.Errorf("dev map: %v", got)
	}
	if got["10.10.0.0-10.10.0.41"] != MarkChainName(shared.ID) || got["10.10.0.44-10.10.0.255"] != MarkChainName(shared.ID) {
		t.Errorf("the addresses no device owns share one queue: %v", got)
	}
}

func TestAFaultWithoutRateSharesOneIDForAllDevices(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {network: IoT}, fault: {latency: 150ms, loss: 3%}}`, 0)
	tg := w.compile(nil)
	if len(tg.Faults) != 1 || tg.Faults[0].Device != "" || tg.Faults[0].Source != o.Id.String() {
		t.Fatalf("%+v", tg.Faults)
	}
	// ... and one element covers every address of the network, devices included
	m := findMap(tg, tg.ClassifyMaps["dev"])
	if len(m.Elements) != 1 || m.Elements[0].Key != "10.10.0.0/24" {
		t.Errorf("%+v", m.Elements)
	}
}

func TestAQueueLimitAndKeepOrderMakeTheIDPerDeviceToo(t *testing.T) {
	for name, fault := range map[string]string{
		"queue limit":                `{latency: 100ms, queue_limit: 200}`,
		"keep order":                 `{latency: 100ms, jitter: 50ms, keep_order: true}`,
		"rate in one direction only": `{download: {rate: 1Mbit}}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := newFaultWorld(t)
			w.overlay(`{target: {group: sensors}, fault: `+fault+`}`, 0)
			tg := w.compile(nil)
			if len(tg.Faults) != 2 { // esp32-42 and esp32-43; a group's scope does not reach addresses no device owns
				t.Errorf("%d ids: %+v", len(tg.Faults), tg.Faults)
			}
		})
	}
}

// ---- faults that impair nothing, and kinds the compiler does not handle ----------------------------

func TestAFaultThatImpairsNothingShadowsWithoutAnIDOrAClass(t *testing.T) {
	tg := scenarioNeutral(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	// one id: the network fault. The neutral device fault is a mark_0 entry for its destination.
	if len(tg.Faults) != 1 || tg.Faults[0].Scope != "network IoT" {
		t.Fatalf("%+v", tg.Faults)
	}
	m := findMap(tg, tg.ClassifyMaps["devdest"])
	if len(m.Elements) != 1 || m.Elements[0].Key != "10.10.0.42 . 203.0.113.0/24" || m.Elements[0].Value != "mark_0" {
		t.Errorf("devdest: %+v", m.Elements)
	}
	c := findChain(tg, "mark_0")
	if c == nil {
		t.Fatal("no chain mark_0")
	}
	b, _ := json.Marshal(c.Rules[0].Expr)
	if !strings.Contains(string(b), "4294901775") || strings.Contains(string(b), `"<<"`) && !strings.Contains(string(b), `[0,4]`) {
		t.Errorf("mark_0: %s", b)
	}
	for _, r := range c.Rules {
		if x, _ := json.Marshal(r.Expr); strings.Contains(string(x), "counter") {
			t.Errorf("mark_0 counts: %s", x)
		}
	}
	for _, cl := range tg.TC.Classes {
		if cl.ID == 0 {
			t.Error("a class for id 0")
		}
	}
}

func TestOnlyImpairmentFaultsCompileHere(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1200, mode: icmp}}}`, 0)
	w.overlay(`{target: {device: esp32-42}, dns: {action: servfail}}`, time.Second)
	w.overlay(`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [8883]}}`, 2*time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() || len(tg.Faults) != 0 || tg.TC != nil {
		t.Errorf("%+v %+v", tg.Problems, tg.Faults)
	}
}

func TestAHostnameSelectorIsLeftOutWithAWarning(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, fault: {destination: {hostname: broker.example.com}, latency: 50ms}}`, 0)
	w.overlay(`{target: {network: IoT}, fault: {loss: 1%}}`, time.Second)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	var warned bool
	for _, p := range tg.Problems {
		if p.Code == CodeHostnameUnresolved && strings.Contains(p.Message, o.Id.String()) && p.Severity == SevWarning {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning: %+v", tg.Problems)
	}
	for _, f := range tg.Faults {
		if f.Source == o.Id.String() {
			t.Error("the hostname fault got an id")
		}
	}
}

func TestAProfileActivationCompilesItsImpairmentPart(t *testing.T) {
	w := newFaultWorld(t)
	o := w.overlay(`{target: {device: esp32-42}, profile: bad-lte}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	f := faultOf(t, tg, o.Id.String(), devESP42) // Bad LTE has a rate: per device
	if f.Profile == "" || f.Upload.Rate == 0 {
		t.Errorf("%+v", f)
	}
}

// ---- capacity -------------------------------------------------------------------------------------

func TestTooManyClassesAreRefusedWithTheScopeThatCausedIt(t *testing.T) {
	w := newFaultWorld(t)
	small := w.overlay(`{target: {device: lab-host}, fault: {latency: 10ms}}`, 0)
	big := w.overlay(`{target: {network: IoT}, fault: {latency: 100ms, rate: 2Mbit}}`, time.Second)
	// 3 ids with 2 directions each, one for lab-host: 8 classes and the default one
	tg := w.compile(func(in *Input) { in.ClassLimit = 8 })
	var p *Problem
	for i := range tg.Problems {
		if tg.Problems[i].Code == CodeCapacityExceeded {
			p = &tg.Problems[i]
		}
	}
	if p == nil || p.Severity != SevError || !tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if p.Scope != "network IoT" {
		t.Errorf("scope %q", p.Scope)
	}
	if len(p.Faults) != 2 || p.Faults[0] != big.Id.String() || p.Faults[1] != small.Id.String() {
		t.Errorf("faults %v, want the network fault first", p.Faults)
	}
	if !strings.Contains(p.Message, "9 classes") || !strings.Contains(p.Message, "limit of 8") {
		t.Errorf("message %q", p.Message)
	}
	// at the limit it still compiles
	if tg := w.compile(func(in *Input) { in.ClassLimit = 9 }); tg.HasErrors() {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestTheClassLimitDefaultsByArchitecture(t *testing.T) {
	if d := DefaultClassLimit(); d != DefaultClassLimitX86 && d != DefaultClassLimitARM64 {
		t.Errorf("%d", d)
	}
	if DefaultClassLimitX86 != 1000 || DefaultClassLimitARM64 != 200 {
		t.Error("plan §3.3: 1000 on x86, 200 on ARM64")
	}
}

// Plan §3.3: "a rate-limited profile on a network with 250 devices needs 500 classes".
func TestARateLimitedNetworkOf250DevicesNeeds500Classes(t *testing.T) {
	w := newFaultWorld(t)
	addrs := map[string][]string{}
	for i := 0; i < 250; i++ {
		addrs[fmt.Sprintf("00000000-0000-4000-8000-%012x", 0x100+i)] = []string{fmt.Sprintf("10.10.0.%d", 2+i)}
	}
	w.setAddrs(addrs)
	w.overlay(`{target: {network: IoT}, fault: {latency: 100ms, rate: 2Mbit}}`, 0)
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	// 250 devices at two directions each, and one more queue for the few addresses of the /24 that
	// no device owns (the gateway's own address, the broadcast address ...)
	if got := len(tg.TC.Classes); got != 2*250+2 {
		t.Errorf("%d classes, want 500 and the shared queue's two", got)
	}
	if tg := w.compile(func(in *Input) { in.ClassLimit = 400 }); !tg.HasErrors() {
		t.Error("the class limit of 400 did not stop 500 classes")
	}
}

func TestMoreThan4095FaultIDsAreRefused(t *testing.T) {
	w := newFaultWorld(t)
	addrs := map[string][]string{}
	for i := 0; i < MarkIDMax+5; i++ {
		addrs[fmt.Sprintf("00000000-0000-4000-8000-%012x", 0x1000+i)] = []string{fmt.Sprintf("10.%d.%d.%d", 10+i/60000, (i/250)%250, 1+i%250)}
	}
	w.setAddrs(addrs)
	w.overlay(`{target: {global: true}, fault: {rate: 1Mbit}}`, 0)
	tg := w.compile(func(in *Input) { in.ClassLimit = 100000 })
	var p *Problem
	for i := range tg.Problems {
		if tg.Problems[i].Code == CodeCapacityExceeded {
			p = &tg.Problems[i]
		}
	}
	if p == nil || p.Scope != "global" || !strings.Contains(p.Message, "4095") {
		t.Fatalf("%+v", tg.Problems)
	}
}

// ---- the classification maps ---------------------------------------------------------------------

// parsed is an element of a classification map in a form a lookup can evaluate.
type parsedElem struct {
	src, dst        [2]uint64
	hasDst, hasPort bool
	proto           string
	portLo, portHi  int
	value           int
	key             string
}

func parseSpan(t *testing.T, s string) [2]uint64 {
	t.Helper()
	if p, err := netip.ParsePrefix(s); err == nil {
		sp := prefixSpan(p)
		return [2]uint64{sp.lo, sp.hi}
	}
	if lo, hi, ok := strings.Cut(s, "-"); ok {
		a, e1 := netip.ParseAddr(lo)
		b, e2 := netip.ParseAddr(hi)
		if e1 != nil || e2 != nil {
			t.Fatalf("bad range %q", s)
		}
		return [2]uint64{addrNum(a), addrNum(b)}
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad address %q", s)
	}
	return [2]uint64{addrNum(a), addrNum(a)}
}

func prefixSpan(p netip.Prefix) aspan {
	p = p.Masked()
	lo := addrNum(p.Addr())
	return aspan{lo, lo + (uint64(1) << (32 - p.Bits())) - 1}
}

func parseElements(t *testing.T, tg *Target, field string) []parsedElem {
	t.Helper()
	m := findMap(tg, tg.ClassifyMaps[field])
	var out []parsedElem
	for _, e := range m.Elements {
		parts := strings.Split(e.Key, " . ")
		pe := parsedElem{key: e.Key}
		pe.src = parseSpan(t, parts[0])
		rest := parts[1:]
		switch field {
		case "devdestport", "devdest":
			pe.dst, pe.hasDst = parseSpan(t, rest[0]), true
			rest = rest[1:]
		}
		if len(rest) == 2 {
			pe.hasPort, pe.proto = true, rest[0]
			lo, hi, isRange := strings.Cut(rest[1], "-")
			pe.portLo, _ = strconv.Atoi(lo)
			pe.portHi = pe.portLo
			if isRange {
				pe.portHi, _ = strconv.Atoi(hi)
			}
		} else if len(rest) != 0 {
			t.Fatalf("key %q has %d unexpected parts", e.Key, len(rest))
		}
		id, err := strconv.Atoi(strings.TrimPrefix(e.Value, markChainPrefix))
		if err != nil || !strings.HasPrefix(e.Value, markChainPrefix) {
			t.Fatalf("value %q", e.Value)
		}
		pe.value = id
		out = append(out, pe)
	}
	return out
}

func (e parsedElem) matches(src, dst netip.Addr, proto string, port int) bool {
	s, d := addrNum(src), addrNum(dst)
	if s < e.src[0] || s > e.src[1] {
		return false
	}
	if e.hasDst && (d < e.dst[0] || d > e.dst[1]) {
		return false
	}
	if e.hasPort && (e.proto != proto || port < e.portLo || port > e.portHi) {
		return false
	}
	return true
}

// lookup does what the lookup chain of the classify chain does: the four levels in order, the first
// map that has an element for the packet decides. found is false when no map has one.
func lookup(levels [][]parsedElem, src, dst netip.Addr, proto string, port int) (id int, found bool) {
	for _, els := range levels {
		for _, e := range els {
			if e.matches(src, dst, proto, port) {
				return e.value, true
			}
		}
	}
	return 0, false
}

func levelsOf(t *testing.T, tg *Target) [][]parsedElem {
	var out [][]parsedElem
	for _, lv := range classifyLevels {
		out = append(out, parseElements(t, tg, lv.field))
	}
	return out
}

// Interval maps cannot hold overlapping elements (plan §3.2). No two elements of one map may
// overlap, in the whole key space, whatever the faults' selectors look like.
func TestNoTwoElementsOfAClassificationMapOverlap(t *testing.T) {
	for name, tg := range faultScenarios(t) {
		for _, lv := range classifyLevels {
			els := parseElements(t, tg, lv.field)
			for i := range els {
				for j := i + 1; j < len(els); j++ {
					a, b := els[i], els[j]
					if overlap(a.src, b.src) && (!a.hasDst || overlap(a.dst, b.dst)) &&
						(!a.hasPort || a.proto == b.proto && a.portLo <= b.portHi && b.portLo <= a.portHi) {
						t.Errorf("%s %s: %q overlaps %q", name, lv.field, a.key, b.key)
					}
				}
			}
		}
	}
}

func overlap(a, b [2]uint64) bool { return a[0] <= b[1] && b[0] <= a[1] }

func TestNestedSelectorsAreSplitIntoDisjointPieces(t *testing.T) {
	tg := scenarioNested(t)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	m := findMap(tg, tg.ClassifyMaps["devdest"])
	var keys []string
	for _, e := range m.Elements {
		keys = append(keys, e.Key+" : "+e.Value)
	}
	// 10.0.0.0/8 minus 10.1.0.0/16 is three pieces, in canonical nft form (prefix or range)
	joined := strings.Join(keys, "\n")
	for _, want := range []string{"10.0.0.0/16", "10.1.0.0/16", "10.2.0.0-10.255.255.255"} {
		if !strings.Contains(joined, " . "+want+" : ") {
			t.Errorf("no element for %s:\n%s", want, joined)
		}
	}
	// overlapping port ranges 80-443 and 400-500 become 80-399 (1%), 400-443 (newer: 2%), 444-500 (2%)
	pm := findMap(tg, tg.ClassifyMaps["devport"])
	var ports []string
	for _, e := range pm.Elements {
		ports = append(ports, e.Key)
	}
	pj := strings.Join(ports, "\n")
	for _, want := range []string{"10.10.0.43 . tcp . 80-399", "10.10.0.43 . tcp . 400-500"} {
		if !strings.Contains(pj, want) {
			t.Errorf("no element %q:\n%s", want, pj)
		}
	}
}

// The compiled maps give, for any traffic, the fault that the precedence rules of plan §2.4 name:
// the whole chain, from the domain's Resolve through ids and interval elements to the first-match
// lookup, agrees. Random worlds, random traffic.
func TestTheCompiledLookupGivesTheResolvedWinner(t *testing.T) {
	templates := []string{
		`{target: {network: IoT}, fault: {latency: 100ms}}`,
		`{target: {network: IoT}, fault: {latency: 90ms, rate: 5Mbit}}`,
		`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`,
		`{target: {device: esp32-42}, fault: {destination: {cidr: 203.0.113.0/24}, latency: 40ms}}`,
		`{target: {device: esp32-43}, fault: {protocol: udp, port_ranges: [{from: 1000, to: 2000}], loss: 2%}}`,
		`{target: {device: esp32-43}, fault: {protocol: udp, port_ranges: [{from: 1500, to: 3000}], loss: 3%}}`,
		`{target: {group: sensors}, fault: {latency: 200ms}}`,
		`{target: {group: sensors}, fault: {destination: {cidr: 10.0.0.0/8}, loss: 1%}}`,
		`{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/24}, loss: 10%}}`,
		`{target: {global: true}, fault: {destination: {cidr: 203.0.113.64/26}, latency: 7ms}}`,
		`{target: {global: true}, fault: {destination: {cidr: 10.1.0.0/16}, latency: 20ms}}`,
		`{target: {global: true}, fault: {latency: 5ms}}`,
		`{target: {global: true}, fault: {protocol: icmp, loss: 50%}}`,
		`{target: {global: true}, fault: {protocol: tcp, port_ranges: [{from: 80, to: 443}], latency: 6ms}}`,
		`{target: {network: Lab}, fault: {blackout: true}}`,
		`{target: {device: lab-host}, fault: {destination: {uplink: true}, latency: 300ms, rate: 1Mbit}}`,
		`{target: {device: lab-host}, fault: {latency: 0ms}}`,
	}
	sources := []struct {
		subject domain.Subject
		addr    string
	}{
		{domain.Subject{Device: devESP42, IP: netip.MustParseAddr("10.10.0.42")}, "10.10.0.42"},
		{domain.Subject{Device: devESP43, IP: netip.MustParseAddr("10.10.0.43")}, "10.10.0.43"},
		{domain.Subject{Device: devLab, IP: netip.MustParseAddr("10.20.0.50")}, "10.20.0.50"},
		{domain.Subject{IP: netip.MustParseAddr("10.10.0.77")}, "10.10.0.77"},
		{domain.Subject{IP: netip.MustParseAddr("10.10.0.1")}, "10.10.0.1"},
		{domain.Subject{IP: netip.MustParseAddr("10.20.0.9")}, "10.20.0.9"},
	}
	dests := []string{"203.0.113.10", "203.0.113.70", "203.0.113.200", "198.51.100.1", "10.1.2.3", "10.0.0.1", "10.2.0.9",
		"10.10.0.42", "10.20.0.50", "8.8.8.8", "192.0.2.1", "10.255.255.255", "203.0.113.64", "203.0.113.127", "203.0.114.0"}
	traffic := []struct {
		proto string
		port  int
	}{{"tcp", 8883}, {"tcp", 443}, {"tcp", 80}, {"tcp", 79}, {"tcp", 22}, {"udp", 53}, {"udp", 1000}, {"udp", 1500}, {"udp", 2500}, {"udp", 3001},
		{"icmp", 0}, {"gre", 0}, {"tcp", 65535}, {"tcp", 1}}

	rng := rand.New(rand.NewSource(7))
	checked := 0
	for round := 0; round < 60; round++ {
		w := newFaultWorld(t)
		perm := rng.Perm(len(templates))
		n := 1 + rng.Intn(7)
		for i, k := range perm[:n] {
			w.overlay(templates[k], time.Duration(i)*time.Second)
		}
		if round%3 == 0 { // a configured fault, too
			w.addConfigFault("00000000-0000-4000-8000-0000000000c1", `{source: {group: sensors}, latency: 30ms}`, -time.Hour)
		}
		tg := w.compile(nil)
		if tg.HasErrors() {
			t.Fatalf("round %d: %+v", round, tg.Problems)
		}
		world, err := domain.NewWorld(w.cfg, w.overlays)
		if err != nil {
			t.Fatal(err)
		}
		levels := levelsOf(t, tg)
		byID := map[int]Fault{}
		for _, f := range tg.Faults {
			byID[f.ID] = f
		}
		for _, s := range sources {
			for _, d := range dests {
				for _, tr := range traffic {
					dst := netip.MustParseAddr(d)
					res := world.Resolve(domain.Query{Source: s.subject, DestIP: dst, Protocol: tr.proto, Port: tr.port})
					want := domain.Winner(res, domain.FamilyImpairment)
					id, found := lookup(levels, netip.MustParseAddr(s.addr), dst, tr.proto, tr.port)
					desc := fmt.Sprintf("round %d: %s -> %s %s/%d", round, s.addr, d, tr.proto, tr.port)
					switch {
					case want == nil && found && id != 0:
						t.Fatalf("%s: no fault expected, got id %d (%+v)", desc, id, byID[id])
					case want != nil && !found:
						// a neutral fault with no entry at all is impossible: it would have mark_0
						t.Fatalf("%s: want %s:%s, no element matches", desc, want.Layer, want.ID)
					case want != nil && id != 0:
						f := byID[id]
						if f.Source != want.ID || f.Layer != string(want.Layer) {
							t.Fatalf("%s: want %s:%s, got id %d (%s %s)", desc, want.Layer, want.ID, id, f.Layer, f.Source)
						}
						if f.Device != "" && f.Device != s.subject.Device {
							t.Fatalf("%s: id %d belongs to device %s", desc, id, f.Device)
						}
					case want != nil && id == 0:
						// mark_0: the winner must impair nothing
						r, ok := tg.resolveWinnerForTest(*want)
						if !ok || !r.neutral() {
							t.Fatalf("%s: want %s:%s (impairs), got mark_0", desc, want.Layer, want.ID)
						}
					}
					checked++
				}
			}
		}
	}
	t.Logf("%d lookups compared", checked)
}

func (t *Target) resolveWinnerForTest(c domain.Candidate) (resolvedFault, bool) {
	return t.resolveWinner(c)
}

func TestSourcesThatHaveTheSameEntriesShareElements(t *testing.T) {
	w := newFaultWorld(t)
	w.setAddrs(map[string][]string{
		devESP42: {"10.10.0.10"}, devESP43: {"10.10.0.11"}, devLab: {"10.10.0.12", "10.10.0.20"},
	})
	w.overlay(`{target: {network: IoT}, fault: {latency: 100ms}}`, 0)
	tg := w.compile(nil)
	m := findMap(tg, tg.ClassifyMaps["dev"])
	if len(m.Elements) != 1 || m.Elements[0].Key != "10.10.0.0/24" {
		t.Errorf("a network fault without a rate is one element: %+v", m.Elements)
	}
}

func TestADeviceAddressIsCarvedOutOfTheStretchItLiesIn(t *testing.T) {
	srcs := []domain.Source{
		{Device: "a", Addrs: []netip.Addr{netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("10.0.0.200")}},
		{Device: "b", Ranges: []domain.IPRange{{First: netip.MustParseAddr("10.0.0.64"), Last: netip.MustParseAddr("10.0.0.127")}}},
		{Device: "c", Ranges: []domain.IPRange{{First: netip.MustParseAddr("10.0.0.96"), Last: netip.MustParseAddr("10.0.0.111")}}},
		{Ranges: []domain.IPRange{{First: netip.MustParseAddr("10.0.0.0"), Last: netip.MustParseAddr("10.0.0.255")}}},
	}
	got := partitionSources(srcs)
	render := func(sp []aspan) string {
		var s []string
		for _, x := range sp {
			s = append(s, fmtAddrSpan(x))
		}
		return strings.Join(s, " ")
	}
	want := []string{
		"10.0.0.5 10.0.0.200",
		"10.0.0.64/27 10.0.0.112/28", // b without c's range
		"10.0.0.96/28",               // c: the more specific range wins
		"10.0.0.0-10.0.0.4 10.0.0.6-10.0.0.63 10.0.0.128-10.0.0.199 10.0.0.201-10.0.0.255", // the stretch
	}
	for i := range want {
		if r := render(got[i]); r != want[i] {
			t.Errorf("source %d: %s, want %s", i, r, want[i])
		}
	}
}

func TestMapKeysAreInNftsCanonicalForm(t *testing.T) {
	for _, tc := range []struct {
		s    aspan
		want string
	}{
		{aspan{addrNum(netip.MustParseAddr("10.0.0.5")), addrNum(netip.MustParseAddr("10.0.0.5"))}, "10.0.0.5"},
		{aspan{addrNum(netip.MustParseAddr("10.0.0.0")), addrNum(netip.MustParseAddr("10.0.0.255"))}, "10.0.0.0/24"},
		{aspan{addrNum(netip.MustParseAddr("10.0.0.1")), addrNum(netip.MustParseAddr("10.0.0.255"))}, "10.0.0.1-10.0.0.255"},
		{aspan{addrNum(netip.MustParseAddr("10.0.0.0")), addrNum(netip.MustParseAddr("10.0.1.255"))}, "10.0.0.0/23"},
		{aspan{addrNum(netip.MustParseAddr("10.0.1.0")), addrNum(netip.MustParseAddr("10.0.2.255"))}, "10.0.1.0-10.0.2.255"},
		{aspan{0, addrSpace - 1}, "0.0.0.0/0"},
	} {
		if got := fmtAddrSpan(tc.s); got != tc.want {
			t.Errorf("%v -> %s, want %s", tc.s, got, tc.want)
		}
	}
	if got := clsKey(clsRow{src: aspan{addrNum(netip.MustParseAddr("10.0.0.5")), addrNum(netip.MustParseAddr("10.0.0.5"))},
		dest: &aspan{addrNum(netip.MustParseAddr("203.0.113.0")), addrNum(netip.MustParseAddr("203.0.113.255"))},
		port: &domain.PortSel{Proto: "tcp", From: 80, To: 443}}); got != "10.0.0.5 . 203.0.113.0/24 . tcp . 80-443" {
		t.Errorf("%s", got)
	}
	// ICMP selects by protocol only: every "port"
	if got := clsKey(clsRow{src: aspan{1, 1}, port: &domain.PortSel{Proto: "icmp"}}); got != "0.0.0.1 . icmp . 0-65535" {
		t.Errorf("%s", got)
	}
	if got := clsKey(clsRow{src: aspan{1, 1}, port: &domain.PortSel{Proto: "udp", From: 53, To: 53}}); got != "0.0.0.1 . udp . 53" {
		t.Errorf("%s", got)
	}
}

// ---- mark chains and counters ------------------------------------------------------------------

func TestEveryIDHasAMarkChainWithTheGoldenMaskAndTwoCounters(t *testing.T) {
	tg := scenarioMixed(t)
	for _, f := range tg.Faults {
		c := findChain(tg, MarkChainName(f.ID))
		if c == nil {
			t.Fatalf("no chain for id %d", f.ID)
		}
		if len(c.Rules) != 4 {
			t.Fatalf("id %d: %d rules", f.ID, len(c.Rules))
		}
		set, _ := json.Marshal(c.Rules[0].Expr)
		if !strings.Contains(string(set), "4294901775") || !strings.Contains(string(set), strconv.Itoa(f.ID)) { // 0xffff000f
			t.Errorf("id %d mark rule: %s", f.ID, set)
		}
		up, _ := json.Marshal(c.Rules[1].Expr)
		down, _ := json.Marshal(c.Rules[2].Expr)
		if !strings.Contains(string(up), `"original"`) || !strings.Contains(string(up), f.CounterUp) ||
			!strings.Contains(string(down), `"reply"`) || !strings.Contains(string(down), f.CounterDown) {
			t.Errorf("id %d counters: %s / %s", f.ID, up, down)
		}
		if ret, _ := json.Marshal(c.Rules[3].Expr); string(ret) != `[{"return":null}]` {
			t.Errorf("id %d last rule: %s", f.ID, ret)
		}
		if !contains(tg.Nft.Counters, f.CounterUp) || !contains(tg.Nft.Counters, f.CounterDown) {
			t.Errorf("counters of id %d are not declared", f.ID)
		}
	}
}

// Plan §3.2: the named counters of a fault survive every apply and stay monotonic. They are named
// by the fault, not by the id, so they survive a renumbering too, and a removed fault's counters
// are deleted by the transaction.
func TestCountersFollowTheFaultNotTheID(t *testing.T) {
	w := newFaultWorld(t)
	a := w.overlay(`{target: {device: esp32-42}, fault: {latency: 50ms}}`, 0)
	first := w.compile(nil)
	fa := faultOf(t, first, a.Id.String(), "")
	// a different allocation (no history) may number it differently; the counter is the same
	w.ids = map[string]int{"something-else": 1}
	second := w.compile(nil)
	fb := faultOf(t, second, a.Id.String(), "")
	if fa.CounterUp != fb.CounterUp || fa.CounterDown != fb.CounterDown {
		t.Errorf("%s/%s vs %s/%s", fa.CounterUp, fa.CounterDown, fb.CounterUp, fb.CounterDown)
	}
	if fa.CounterUp == fa.CounterDown {
		t.Error("one counter for both directions")
	}
	// the transaction keeps a counter that stays and deletes the one of a removed fault
	w.overlays = nil
	third := w.compile(nil)
	rs := rulesetOf(first)
	tx, err := third.Nft.Transaction(rs)
	if err != nil {
		t.Fatal(err)
	}
	cmds, _ := nftCommands(tx)
	var deleted []string
	for _, c := range cmds {
		var m map[string]map[string]map[string]any
		_ = json.Unmarshal(c, &m)
		if ct := m["delete"]["counter"]; ct != nil {
			deleted = append(deleted, ct["name"].(string))
		}
	}
	sort.Strings(deleted)
	if strings.Join(deleted, ",") != fa.CounterDown+","+fa.CounterUp && strings.Join(deleted, ",") != fa.CounterUp+","+fa.CounterDown {
		t.Errorf("deleted counters %v", deleted)
	}
}

// ---- the executor accepts what the compiler emits --------------------------------------------------

func TestTheTCTreeIsAcceptedByTheExecutorsValidation(t *testing.T) {
	for name, tg := range faultScenarios(t) {
		if tg.TC == nil {
			continue
		}
		for _, dev := range tg.TC.Devs {
			op := &executor.TC{Target: executor.Target{NS: "gw"}, Entries: tg.TC.Entries(dev, true)}
			b, err := executor.Encode(op)
			if err != nil {
				t.Fatalf("%s %s: %v", name, dev, err)
			}
			if _, err := executor.Decode(b); err != nil {
				t.Errorf("%s %s: the executor refuses the tree: %v", name, dev, err)
			}
			if _, err := executor.Plan(op); err != nil {
				t.Errorf("%s %s: %v", name, dev, err)
			}
		}
	}
}

func TestTheClassificationElementsAreAcceptedByTheExecutorsValidation(t *testing.T) {
	for name, tg := range faultScenarios(t) {
		for _, lv := range classifyLevels {
			m := findMap(tg, tg.ClassifyMaps[lv.field])
			if len(m.Elements) == 0 {
				continue
			}
			var els []executor.NftMapElement
			for _, e := range m.Elements {
				els = append(els, executor.NftMapElement{Key: e.Key, Value: e.Value})
			}
			b, err := executor.Encode(&executor.NftAddMapElements{Map: m.Name, Elements: els})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executor.Decode(b); err != nil {
				t.Errorf("%s %s: the executor refuses an element: %v", name, lv.field, err)
			}
		}
	}
}

func TestTheFaultTargetsAreInTheKernelGate(t *testing.T) {
	scen := transactionScenarios(t)
	for name := range faultScenarios(t) {
		if _, ok := scen[name]; !ok {
			t.Errorf("%s is not among the transaction scenarios of the kernel gate", name)
		}
	}
}

// rulesetOf is what the kernel would list after the target was applied: the counters, chains and
// maps of the table.
func rulesetOf(tg *Target) *linux.Ruleset {
	objs := []string{`{"table":{"family":"inet","name":"chaosgw","handle":1}}`}
	for _, c := range tg.Nft.Counters {
		objs = append(objs, fmt.Sprintf(`{"counter":{"family":"inet","table":"chaosgw","name":%q,"handle":2,"packets":5,"bytes":300}}`, c))
	}
	for _, c := range tg.Nft.AllChains() {
		objs = append(objs, fmt.Sprintf(`{"chain":{"family":"inet","table":"chaosgw","name":%q,"handle":3}}`, c.Name))
	}
	for _, m := range tg.Nft.Maps {
		objs = append(objs, fmt.Sprintf(`{"map":{"family":"inet","table":"chaosgw","name":%q,"handle":4,"type":"ipv4_addr","map":"verdict"}}`, m.Name))
	}
	rs, err := linux.ParseNft([]byte(`{"nftables":[` + strings.Join(objs, ",") + `]}`))
	if err != nil {
		panic(err)
	}
	return rs
}

// ---- churn: a device that gets a new address (P2-M7-02) ----------------------------------------------

// The classification maps are keyed by address. A device that changes its address must change a
// handful of elements, not the maps: the fault ids stay (they belong to the device), the tc tree
// stays, and only the elements that name the old and the new address move.
func TestAnAddressChangeChangesAHandfulOfElements(t *testing.T) {
	w := newFaultWorld(t)
	addrs := map[string][]string{}
	devs := make([]string, 250)
	for i := range devs {
		devs[i] = fmt.Sprintf("00000000-0000-4000-8000-%012x", 0x100+i)
		addrs[devs[i]] = []string{fmt.Sprintf("10.10.0.%d", 2+i)}
	}
	w.setAddrs(addrs)
	w.overlay(`{target: {network: IoT}, fault: {latency: 100ms, rate: 2Mbit}}`, 0)
	w.overlay(`{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/24}, loss: 1%}}`, time.Second)
	w.overlay(`{target: {global: true}, fault: {protocol: tcp, ports: [8883], latency: 20ms}}`, 2*time.Second)
	before := w.compile(nil)
	if before.HasErrors() {
		t.Fatalf("%+v", before.Problems)
	}

	// device 100 moves from 10.10.0.102 to a free address of the network
	addrs[devs[100]] = []string{"10.10.0.254"}
	w.setAddrs(addrs)
	after := w.compile(nil)
	if after.HasErrors() {
		t.Fatalf("%+v", after.Problems)
	}
	if fmt.Sprint(before.FaultIDs) != fmt.Sprint(after.FaultIDs) || fmt.Sprint(before.TC) != fmt.Sprint(after.TC) {
		t.Error("an address change renumbered fault ids or changed the tc tree")
	}
	changed, total := 0, 0
	for _, m := range after.Nft.Maps {
		var old *MapDef
		for i := range before.Nft.Maps {
			if before.Nft.Maps[i].Name == m.Name {
				old = &before.Nft.Maps[i]
			}
		}
		if old == nil {
			t.Fatalf("map %s is new", m.Name)
		}
		u := DiffMap(*old, m)
		changed += len(u.Delete) + len(u.Add)
		total += len(m.Elements)
	}
	t.Logf("%d element changes of %d elements in all maps for one address change among 250 devices", changed, total)
	if changed > 12 {
		t.Errorf("%d element changes for one address change", changed)
	}
	if total < 250 {
		t.Errorf("only %d elements: the scenario does not hold per-device entries", total)
	}
}

func TestDiffMapAndTheElementTransaction(t *testing.T) {
	old := MapDef{Name: "m", KeyType: []string{"ipv4_addr", "inet_proto", "inet_service"}, ValueType: "verdict", Flags: []string{"interval"}, Elements: []MapElement{
		{"10.0.0.1 . tcp . 80", "mark_1"}, {"10.0.0.2-10.0.0.9 . udp . 53-60", "mark_2"}, {"10.0.0.20 . tcp . 1-100", "mark_3"},
	}}
	next := old
	next.Elements = []MapElement{
		{"10.0.0.1 . tcp . 80", "mark_4"},             // value changed
		{"10.0.0.2-10.0.0.9 . udp . 53-60", "mark_2"}, // same
		{"10.0.0.30 . tcp . 1-100", "mark_3"},         // key changed
	}
	u := DiffMap(old, next)
	if fmt.Sprint(u.Delete) != "[10.0.0.1 . tcp . 80 10.0.0.20 . tcp . 1-100]" || len(u.Add) != 2 || u.Add[0].Key != "10.0.0.1 . tcp . 80" || u.Add[1].Key != "10.0.0.30 . tcp . 1-100" {
		t.Errorf("%+v", u)
	}
	if !DiffMap(old, old).Empty() {
		t.Error("the same map differs")
	}
	tx, err := (Nft{Maps: []MapDef{next}}).ElementTransaction([]MapUpdate{u})
	if err != nil {
		t.Fatal(err)
	}
	cmds := commandsOf(t, tx)
	// one batch: the deletes come first, then the adds
	if len(cmds) != 2 || cmds[0]["delete"] == nil || cmds[1]["add"] == nil {
		t.Fatalf("%s", tx)
	}
	if _, err := (Nft{}).ElementTransaction([]MapUpdate{u}); err == nil {
		t.Error("an update of a map the target does not have was accepted")
	}
	if err := executor.CheckNftRuleset(tx); err != nil {
		t.Errorf("the executor's scope refuses the element transaction: %v", err)
	}
}

// A write of an overlay recompiles everything, so the compiler must not be quadratic in the
// devices: 2000 devices with a per-device queue each, and destination and port entries on top.
func TestACompileWithThousandsOfDevicesIsFast(t *testing.T) {
	w := newFaultWorld(t)
	addrs := map[string][]string{}
	for i := 0; i < 2000; i++ {
		addrs[fmt.Sprintf("00000000-0000-4000-8000-%012x", 0x100+i)] = []string{fmt.Sprintf("10.10.%d.%d", i/250, 2+i%250)}
	}
	w.setAddrs(addrs)
	w.overlay(`{target: {network: IoT}, fault: {latency: 100ms, rate: 2Mbit}}`, 0)
	w.overlay(`{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/24}, loss: 1%}}`, time.Second)
	w.overlay(`{target: {global: true}, fault: {protocol: tcp, ports: [8883], latency: 20ms}}`, 2*time.Second)
	start := time.Now()
	tg := w.compile(func(in *Input) { in.ClassLimit = 100000 })
	took := time.Since(start)
	t.Logf("2000 devices: %d ids, %d classes, compiled in %s", len(tg.Faults), len(tg.TC.Classes), took)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems[0])
	}
	if took > 10*time.Second {
		t.Errorf("compiling 2000 devices took %s", took)
	}
}

// Winners names every fault that wins for some traffic, also one that impairs nothing and so has no
// id, and leaves out one that is overridden everywhere: the API's `state` of an overlay.
func TestWinnersAreTheFaultsThatWinSomewhere(t *testing.T) {
	w := newFaultWorld(t)
	network := w.overlay(`{target: {network: IoT}, fault: {latency: 100ms}}`, 0)
	neutral := w.overlay(`{target: {device: esp32-42}, fault: {destination: {cidr: 203.0.113.0/24}, latency: 0ms}}`, time.Second)
	// two global faults at the same level: the newer one wins everywhere (D26), the older nowhere
	shadowed := w.overlay(`{target: {global: true}, fault: {loss: 1%}}`, 2*time.Second)
	newer := w.overlay(`{target: {global: true}, fault: {latency: 30ms}}`, 3*time.Second)
	tg := w.compile(nil)
	want := []string{"overlay:" + neutral.Id.String() + ":impairment", "overlay:" + network.Id.String() + ":impairment", "overlay:" + newer.Id.String() + ":impairment"}
	got := map[string]bool{}
	for _, k := range tg.Winners {
		got[k] = true
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("%s is missing from %v", k, tg.Winners)
		}
	}
	if got["overlay:"+shadowed.Id.String()+":impairment"] {
		t.Errorf("a global fault that a newer one shadows everywhere wins nowhere: %v", tg.Winners)
	}
	if len(tg.Faults) != 2 {
		t.Errorf("the network fault and the newer global one have an id: %+v", tg.Faults)
	}
	for i := 1; i < len(tg.Winners); i++ {
		if tg.Winners[i-1] >= tg.Winners[i] {
			t.Errorf("not sorted: %v", tg.Winners)
		}
	}
}

// ---- the cost of resolving many overlays -----------------------------------------------------------

// scaleOverlays adds n overlays on the network IoT; kind says what each selects: "dp" its own /24 and
// its own tcp port, "mixed" alternately its own /24 only and its own port only (those two conflict
// pairwise, so the level-1 map needs their product).
func scaleOverlays(w *faultWorld, n int, kind string) {
	for i := 0; i < n; i++ {
		dest := fmt.Sprintf("11.%d.%d.0/24", (i/256)%256, i%256)
		switch {
		case kind == "dp":
			w.overlay(fmt.Sprintf(`{target: {network: IoT}, fault: {destination: {cidr: "%s"}, protocol: tcp, ports: [%d], latency: 50ms}}`, dest, 1000+i), time.Duration(i)*time.Millisecond)
		case i%2 == 0:
			w.overlay(fmt.Sprintf(`{target: {network: IoT}, fault: {destination: {cidr: "%s"}, latency: 50ms}}`, dest), time.Duration(i)*time.Millisecond)
		default:
			w.overlay(fmt.Sprintf(`{target: {network: IoT}, fault: {protocol: tcp, ports: [%d], loss: 1%%}}`, 1000+i), time.Duration(i)*time.Millisecond)
		}
	}
}

// A token that may write overlays must not be able to stall the state owner: the compile that
// checks every write was cubic in the number of overlays that each name a destination and a port
// (4.5 ms for 10, 11.7 s for 160, 42 s for 240).
func TestManyOverlaysWithTheirOwnDestinationAndPortCompileQuickly(t *testing.T) {
	w := newFaultWorld(t)
	scaleOverlays(w, 400, "dp")
	start := time.Now()
	tg := w.compile(nil)
	took := time.Since(start)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.Faults) != 400 {
		t.Fatalf("%d faults", len(tg.Faults))
	}
	if took > 60*time.Second {
		t.Fatalf("compiling 400 overlays took %v", took)
	}
}

func TestAnOverlaySetThatNeedsTooManyClassificationCellsIsRefusedQuickly(t *testing.T) {
	w := newFaultWorld(t)
	scaleOverlays(w, 600, "mixed")
	start := time.Now()
	tg := w.compile(func(in *Input) { in.ClassLimit = 100000 })
	took := time.Since(start)
	var p *Problem
	for i := range tg.Problems {
		if tg.Problems[i].Code == CodeCapacityExceeded {
			p = &tg.Problems[i]
		}
	}
	if p == nil || !tg.HasErrors() {
		t.Fatalf("no capacity_exceeded: %+v", tg.Problems)
	}
	if !strings.Contains(p.Message, "destinations and ports") || len(p.Faults) == 0 {
		t.Errorf("%+v", p)
	}
	if took > 60*time.Second {
		t.Fatalf("the refusal took %v", took)
	}
}

func TestTooManyClassificationElementsAreRefused(t *testing.T) {
	// 100 overlays of 64 scattered ports on each of two networks: two tables of 6400 entries, which
	// the address stretches of their sources multiply (one element per stretch and entry)
	w := newFaultWorld(t)
	for i := 0; i < 200; i++ {
		var ports []string
		for k := 0; k < 64; k++ {
			ports = append(ports, fmt.Sprint(1+2*(i/2*64+k)))
		}
		network := []string{"IoT", "Lab"}[i%2]
		w.overlay(fmt.Sprintf(`{target: {network: %s}, fault: {protocol: tcp, ports: [%s], latency: 50ms}}`, network, strings.Join(ports, ",")), time.Duration(i)*time.Millisecond)
	}
	tg := w.compile(func(in *Input) { in.ClassLimit = 100000 })
	for _, p := range tg.Problems {
		if p.Code == CodeCapacityExceeded && strings.Contains(p.Message, "elements") {
			return
		}
	}
	t.Fatalf("no capacity_exceeded about elements: %+v", tg.Problems)
}

// Whatever the API accepts as a percentage or a rate must be a tc command the executor's grammar
// takes: an operation it refuses fails the whole apply (found by review: the API pattern allows any
// number of decimals and any number of digits).
func TestWhatTheAPIAcceptsIsWhatTheExecutorsTCGrammarTakes(t *testing.T) {
	for name, fault := range map[string]string{
		"many decimals of a loss":      `{loss: 5.0000000001%}`,
		"a tiny loss":                  `{loss: 0.0000000001%}`,
		"a repeating fraction":         `{loss: 33.3333333333%, duplicate: 0.1234567890123%}`,
		"burst loss with decimals":     `{burst_loss: {p: 1.00000000001%, r: 30.33333333333%, h: 10.1234567891011%, k: 99.99999999999%}}`,
		"a rate of many digits":        `{rate: 1000000000001bit}`,
		"a rate of very many digits":   `{rate: 99999999999999Gbit}`,
		"a fractional rate":            `{rate: 123456789.123456789Mbit}`,
		"a rate a trillion above that": `{rate: 99999999999999999999Gbit}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := newFaultWorld(t)
			w.overlay(`{target: {device: esp32-42}, fault: `+fault+`}`, 0)
			tg := w.compile(nil)
			if tg.HasErrors() {
				t.Fatalf("%+v", tg.Problems)
			}
			if tg.TC == nil || len(tg.TC.Classes) == 0 {
				t.Fatal("no tc tree")
			}
			for _, dev := range tg.TC.Devs {
				data, err := executor.Encode(&executor.TC{Entries: tg.TC.Entries(dev, true)})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := executor.Decode(data); err != nil {
					t.Errorf("%s: the executor refuses the tree of the fault %s: %v", dev, fault, err)
				}
			}
		})
	}
}
