package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The churn of docs/open-items.md P2-M10-09: two targets that differ in the faults of other devices
// and networks while two flows keep their own fault. esp32-42 (10.10.0.42) has a device fault, which
// the single-field interval map `dev` (an rbtree in the kernel) classifies; lab-host (10.20.0.50) a
// device fault with a destination, which the concatenated interval map `devdest` (pipapo)
// classifies. The second target adds a fault for the network IoT (stretches of the `dev` map right
// next to esp32-42's element), one for esp32-43 and one for the network Lab with a destination (an
// element of lab-host in `devdest` next to its own).
type classifyChurn struct {
	a, b    *Target
	dev42   Fault // esp32-42's fault, the same in a and b
	devdest Fault // lab-host's fault, the same in a and b
}

// churnDest is where lab-host's flow goes: its fault names it as the destination.
const churnDest = "10.20.0.1"

func newClassifyChurn(t *testing.T) classifyChurn {
	t.Helper()
	w := newFaultWorld(t)
	o42 := w.overlay(`{target: {device: esp32-42}, fault: {latency: 30ms}}`, 0)
	oLab := w.overlay(`{target: {device: lab-host}, fault: {latency: 40ms, destination: {cidr: `+churnDest+`/32}}}`, time.Second)
	base := append([]model.Overlay(nil), w.overlays...)
	w.overlay(`{target: {network: IoT}, fault: {latency: 50ms}}`, 2*time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {loss: 1%}}`, 3*time.Second)
	w.overlay(`{target: {network: Lab}, fault: {latency: 20ms, destination: {cidr: 198.51.100.0/24}}}`, 4*time.Second)
	full := append([]model.Overlay(nil), w.overlays...)

	w.overlays = base
	a := w.compile(nil)
	w.overlays = full
	b := w.compile(nil)
	w.overlays = base
	a2 := w.compile(nil)
	for _, tg := range []*Target{a, b, a2} {
		if tg.HasErrors() {
			t.Fatalf("%+v", tg.Problems)
		}
	}
	c := classifyChurn{a: a, b: b, dev42: faultOf(t, a, o42.Id.String(), ""), devdest: faultOf(t, a, oLab.Id.String(), "")}
	for _, tg := range []*Target{b, a2} {
		if f := faultOf(t, tg, o42.Id.String(), ""); f.ID != c.dev42.ID || f.CounterUp != c.dev42.CounterUp {
			t.Fatalf("esp32-42's fault is %d (%s) in one target and %d (%s) in the other", c.dev42.ID, c.dev42.CounterUp, f.ID, f.CounterUp)
		}
		if f := faultOf(t, tg, oLab.Id.String(), ""); f.ID != c.devdest.ID || f.CounterUp != c.devdest.CounterUp {
			t.Fatalf("lab-host's fault is %d (%s) in one target and %d (%s) in the other", c.devdest.ID, c.devdest.CounterUp, f.ID, f.CounterUp)
		}
	}
	return c
}

// listingOf is the listing the kernel gives of a target that was applied: the structure of rulesetOf
// and the elements of the sets and maps, in the forms nft prints them in (the forms the compiler
// writes, which verify compares).
func listingOf(tg *Target) *linux.Ruleset {
	objs := []string{`{"table":{"family":"inet","name":"chaosgw","handle":1}}`}
	for _, c := range tg.Nft.Counters {
		objs = append(objs, fmt.Sprintf(`{"counter":{"family":"inet","table":"chaosgw","name":%q,"handle":2}}`, c))
	}
	for _, c := range tg.Nft.AllChains() {
		objs = append(objs, fmt.Sprintf(`{"chain":{"family":"inet","table":"chaosgw","name":%q,"handle":3}}`, c.Name))
	}
	for _, s := range tg.Nft.Sets {
		el, _ := json.Marshal(setElements(s))
		objs = append(objs, fmt.Sprintf(`{"set":{"family":"inet","table":"chaosgw","name":%q,"handle":4,"type":%q,"elem":%s}}`, s.Name, s.Type, el))
	}
	for _, m := range tg.Nft.Maps {
		el, _ := json.Marshal(mapElements(m))
		objs = append(objs, fmt.Sprintf(`{"map":{"family":"inet","table":"chaosgw","name":%q,"handle":5,"map":%q,"elem":%s}}`, m.Name, m.ValueType, el))
	}
	rs, err := linux.ParseNft([]byte(`{"nftables":[` + strings.Join(objs, ",") + `]}`))
	if err != nil {
		panic(err)
	}
	return rs
}

// elementModel holds the elements of the sets and maps of a table in their normalized forms: a set
// element maps to "", a map key to its value.
type elementModel map[string]map[string]string

func modelOf(tg *Target) elementModel {
	m := elementModel{}
	for _, s := range tg.Nft.Sets {
		if s.Dynamic {
			continue
		}
		m[s.Name] = map[string]string{}
		for _, e := range s.Elements {
			m[s.Name][linux.NormalizeElement(e)] = ""
		}
	}
	for _, d := range tg.Nft.Maps {
		m[d.Name] = map[string]string{}
		for _, e := range d.Elements {
			m[d.Name][linux.NormalizeElement(e.Key)] = e.Value
		}
	}
	return m
}

// run carries out the element commands of a transaction on the model like the kernel would: a flush
// empties, a delete of an element that is not there fails (as nft does), an add of a key that is
// there with another value fails.
func (m elementModel) run(t *testing.T, tx []byte, maps map[string]bool) (touched map[string]map[string]bool) {
	t.Helper()
	touched = map[string]map[string]bool{}
	touch := func(name, k string) {
		if touched[name] == nil {
			touched[name] = map[string]bool{}
		}
		touched[name][k] = true
	}
	for _, c := range commandsOf(t, tx) {
		for command, objs := range c {
			for kind, obj := range objs {
				name, _ := obj["name"].(string)
				switch {
				case command == "flush" && (kind == "set" || kind == "map"):
					for k := range m[name] {
						touch(name, k)
					}
					m[name] = map[string]string{}
				case kind == "element":
					raw, _ := json.Marshal(obj["elem"])
					var elems []json.RawMessage
					if err := json.Unmarshal(raw, &elems); err != nil {
						t.Fatal(err)
					}
					s := &linux.NftSet{Elem: elems}
					switch {
					case command == "delete":
						for _, k := range s.Elements() {
							if _, ok := m[name][k]; !ok {
								t.Fatalf("%s: delete of %s, which is not there", name, k)
							}
							delete(m[name], k)
							touch(name, k)
						}
					case command == "add" && maps[name]:
						for k, v := range s.Pairs() {
							if hv, ok := m[name][k]; ok && hv != v {
								t.Fatalf("%s: add of %s -> %s while it is there with %s", name, k, v, hv)
							}
							m[name][k] = v
							touch(name, k)
						}
					case command == "add":
						for _, k := range s.Elements() {
							m[name][k] = ""
							touch(name, k)
						}
					}
				}
			}
		}
	}
	return touched
}

func sameModel(a, b elementModel) string {
	var diffs []string
	for name, want := range b {
		have := a[name]
		for k, v := range want {
			if hv, ok := have[k]; !ok || hv != v {
				diffs = append(diffs, fmt.Sprintf("%s: %s is %q, want %q", name, k, hv, v))
			}
		}
		for k := range have {
			if _, ok := want[k]; !ok {
				diffs = append(diffs, fmt.Sprintf("%s: %s should be gone", name, k))
			}
		}
	}
	sort.Strings(diffs)
	return strings.Join(diffs, "\n")
}

// A full apply changes the elements of the sets and maps the kernel holds by their difference
// (P2-M10-09): it flushes no set or map that exists, the elements that stay are not touched (in
// particular those of the two flows whose faults did not change), and the result is the new target's
// elements exactly. Back again the same.
func TestATransactionChangesOnlyTheElementsThatDiffer(t *testing.T) {
	c := newClassifyChurn(t)
	maps := map[string]bool{}
	for _, m := range c.a.Nft.Maps {
		maps[m.Name] = true
	}
	for _, step := range []struct {
		name     string
		from, to *Target
	}{{"a to b", c.a, c.b}, {"b to a", c.b, c.a}} {
		t.Run(step.name, func(t *testing.T) {
			tx, err := step.to.Nft.Transaction(listingOf(step.from))
			if err != nil {
				t.Fatal(err)
			}
			if err := executorCheck(tx); err != nil {
				t.Fatal(err)
			}
			for _, cmd := range commandsOf(t, tx) {
				if obj, ok := cmd["flush"]["set"]; ok {
					t.Errorf("set %v is flushed", obj["name"])
				}
				if obj, ok := cmd["flush"]["map"]; ok {
					t.Errorf("map %v is flushed", obj["name"])
				}
			}
			m := modelOf(step.from)
			touched := m.run(t, tx, maps)
			if d := sameModel(m, modelOf(step.to)); d != "" {
				t.Errorf("the kernel would hold other elements than the target:\n%s", d)
			}
			// what stays is not touched: every element that is the same before and after
			before, after := modelOf(step.from), modelOf(step.to)
			for name, els := range touched {
				for k := range els {
					if v, ok := before[name][k]; ok {
						if av, ok := after[name][k]; ok && av == v {
							t.Errorf("%s: %s stays %s but is touched", name, k, v)
						}
					}
				}
			}
			dev, devdest := c.a.ClassifyMaps["dev"], c.a.ClassifyMaps["devdest"]
			if touched[dev]["10.10.0.42"] || touched[devdest]["10.20.0.50 . "+churnDest] || touched[c.a.ClassifyNets]["10.10.0.0/24"] {
				t.Errorf("the elements of the flows that keep their fault are touched: %v", touched)
			}
			if len(touched[dev]) == 0 || len(touched[devdest]) == 0 {
				t.Errorf("the churn changes nothing next to the flows' elements: %v", touched)
			}
		})
	}
}

// A set or map the transaction cannot base a difference on is flushed and filled: one the kernel
// does not have yet, and one whose listing holds an element that cannot be read.
func TestATransactionFlushesWhatItCannotDiff(t *testing.T) {
	c := newClassifyChurn(t)
	tx, err := c.b.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	cmds := commandsOf(t, tx)
	for _, s := range c.b.Nft.Sets {
		if !s.Dynamic && find(cmds, "flush", "set", s.Name) < 0 {
			t.Errorf("set %s is not flushed on a first apply", s.Name)
		}
	}
	for _, m := range c.b.Nft.Maps {
		if find(cmds, "flush", "map", m.Name) < 0 {
			t.Errorf("map %s is not flushed on a first apply", m.Name)
		}
	}

	rs := listingOf(c.a)
	dev := c.a.ClassifyMaps["dev"]
	got := rs.Set(dev)
	got.Elem = append(got.Elem, json.RawMessage(`"not a pair"`))
	rs.Set(c.a.ClassifyNets).Elem = append(rs.Set(c.a.ClassifyNets).Elem, json.RawMessage(`{`))
	tx, err = c.b.Nft.Transaction(rs)
	if err != nil {
		t.Fatal(err)
	}
	cmds = commandsOf(t, tx)
	if find(cmds, "flush", "map", dev) < 0 || find(cmds, "delete", "element", dev) >= 0 {
		t.Errorf("a map with an element that cannot be read is not flushed and filled")
	}
	if find(cmds, "flush", "set", c.a.ClassifyNets) < 0 {
		t.Errorf("a set with an element that cannot be read is not flushed and filled")
	}
	if devdest := c.a.ClassifyMaps["devdest"]; find(cmds, "flush", "map", devdest) >= 0 {
		t.Errorf("the other maps are flushed too")
	}
}
