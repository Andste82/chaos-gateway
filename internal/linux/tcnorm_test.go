package linux

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// The fixtures under testdata/tc were recorded from a real kernel (6.8.0-142, iproute2 6.19) by
// testdata/tc/record.sh; the golden files next to them are the normalizer's output for them. A
// change of the normalizer shows up as a diff of the golden files (`go test ./internal/linux
// -update` rewrites them), a different tool or kernel as a different recording.

func tcFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "tc", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "tc", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from the golden file (run with -update to accept):\n%s", name, got)
	}
}

func TestTheNormalizerGoldens(t *testing.T) {
	for _, c := range []struct {
		name, dev, qdisc, class, filter string
	}{
		{"tree", "dum0", "tree_qdisc.json", "tree_class.json", "tree_filter.json"},
		{"tree_stats", "dum0", "tree_qdisc_stats.json", "tree_class_stats.json", "tree_filter_stats.json"},
		{"tree_queued", "dum0", "queued_qdisc_stats.json", "queued_class_stats.json", "tree_filter.json"},
		{"tree_overflow", "dum0", "overflow_qdisc_stats.json", "queued_class_stats.json", "tree_filter.json"},
		{"odd", "dum2", "odd_qdisc.json", "odd_class.json", "odd_filter.json"},
		{"duplicate", "dum1", "tc_qdisc_dup.json", "", ""},
		{"os_mq", "v0", "os_mq.json", "", ""},
		{"os_mq_fqcodel", "v0", "os_mq_fqcodel.json", "", ""},
		{"os_noqueue", "v1", "os_noqueue.json", "", ""},
		{"os_fq_codel_ingress", "v0", "os_fq_codel_ingress.json", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var q, cl, f []byte
			q = tcFixture(t, c.qdisc)
			if c.class != "" {
				cl = tcFixture(t, c.class)
			}
			if c.filter != "" {
				f = tcFixture(t, c.filter)
			}
			tree, err := NormalizeTC(c.dev, q, cl, f)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, c.name, tree)
		})
	}
}

// A listing of all interfaces carries a "dev" per entry: the normalizer keeps those of one.
func TestTheNormalizerKeepsTheEntriesOfTheNamedInterface(t *testing.T) {
	all := tcFixture(t, "os_all_devs.json")
	for dev, want := range map[string]int{"dum0": 6, "v1": 1, "nothere": 0} {
		tree, err := NormalizeTC(dev, all, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tree.Qdiscs) != want {
			t.Errorf("%s: %d qdiscs, want %d: %+v", dev, len(tree.Qdiscs), want, tree.Qdiscs)
		}
	}
}

func norm(t *testing.T, dev, q, c, f string) *NormTree {
	t.Helper()
	tree, err := NormalizeTC(dev, tcFixture(t, q), tcFixture(t, c), tcFixture(t, f))
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestTheNormalizedTreeHasTheCanonicalIdentities(t *testing.T) {
	tree := norm(t, "dum0", "tree_qdisc.json", "tree_class.json", "tree_filter.json")
	var qd []string
	for _, q := range tree.Qdiscs {
		qd = append(qd, q.Kind+" "+q.Handle+" under "+q.Parent)
	}
	wantQ := []string{"htb 1: under root", "netem 24: under 1:24", "netem 25: under 1:25", "netem 26: under 1:26", "netem 27: under 1:27", "netem 28: under 1:28"}
	if !reflect.DeepEqual(qd, wantQ) {
		t.Errorf("qdiscs %q\nwant %q", qd, wantQ)
	}
	// classes directly below the root qdisc are printed as "root" without a parent, and their leaf
	// as a bare hex major ("0x24")
	for _, c := range tree.Classes {
		if c.Parent != "1:" || c.Kind != "htb" {
			t.Errorf("class %s: parent %q kind %q", c.ID, c.Parent, c.Kind)
		}
		if c.Leaf != strings.TrimPrefix(c.ID, "1:")+":" && c.ID != "1:1" {
			t.Errorf("class %s: leaf %q", c.ID, c.Leaf)
		}
		if c.Rate != 1250000000 || c.Ceil != 1250000000 {
			t.Errorf("class %s: rate %d ceil %d (10 Gbit/s is 1.25e9 bytes/s)", c.ID, c.Rate, c.Ceil)
		}
	}
	// the sort is by number, not by text
	if tree.Classes[0].ID != "1:1" || tree.Classes[1].ID != "1:24" {
		t.Errorf("classes are not in numeric order: %+v", tree.Classes)
	}
	// fw filters carry the mark and the mask, the header entry is gone
	if len(tree.Filters) != 5 {
		t.Fatalf("%d filters: %+v", len(tree.Filters), tree.Filters)
	}
	f := tree.Filters[0]
	if f.Kind != "fw" || f.Mark != 0xa0 || f.Mask != 0x1fff0 || f.Flowid != "1:24" || f.Parent != "1:" || f.Pref != 1 || f.Protocol != "ip" {
		t.Errorf("filter %+v", f)
	}
}

func TestTheNetemOptionsAreNormalized(t *testing.T) {
	tree := norm(t, "dum0", "tree_qdisc.json", "tree_class.json", "tree_filter.json")
	by := map[string]*NetemSpec{}
	for _, q := range tree.Qdiscs {
		if q.Netem != nil {
			by[q.Handle] = q.Netem
		}
	}
	want := map[string]NetemSpec{
		// delay 50ms 10ms, loss random 1% 25%, limit 5000
		"24:": {Limit: 5000, Delay: 0.05, Jitter: 0.01, Loss: 0.01, LossCorr: 0.25},
		// gemodel, reorder 25% (tc sets gap 1), corrupt 0.1%, rate 2mbit = 250000 byte/s
		"25:": {Limit: 1000, Delay: 0.02, Jitter: 0.005, Gemodel: &GemodelSpec{P: 0.01, R: 0.1, OneMinusH: 0.7, OneMinusK: 0.001},
			Reorder: 0.25, Gap: 1, Corrupt: 0.001, Rate: 250000},
		"26:": {Limit: 1000, Delay: 0.6},
		"27:": {Limit: 1000, Loss: 1},
		"28:": {Limit: 1000},
	}
	for h, w := range want {
		if !reflect.DeepEqual(by[h], &w) {
			t.Errorf("netem %s:\n got %+v\nwant %+v", h, by[h], &w)
		}
	}
	for _, q := range tree.Qdiscs {
		if q.Netem != nil && q.Seed == 0 {
			t.Errorf("netem %s has no seed although the tool printed one", q.Handle)
		}
	}
}

func TestOptionsThisVersionDoesNotKnowAreNotDropped(t *testing.T) {
	tree := norm(t, "dum2", "odd_qdisc.json", "odd_class.json", "odd_filter.json")
	n := tree.Qdiscs[1].Netem
	if n == nil || n.Slot == nil || n.Slot.MinDelay != 0.001 || n.Slot.MaxDelay != 0.002 || n.Slot.Packets != 10 || !n.ECN ||
		n.PacketOverhead != 20 || n.CellSize != 10 || n.CellOverhead != 5 || n.Rate != 125000 {
		t.Errorf("netem: %+v", n)
	}
	// a key the code has never seen shows up in Extra, and so in the comparison
	withNew := strings.Replace(string(tcFixture(t, "odd_qdisc.json")), `"ecn":true`, `"ecn":true,"new-attribute":{"x":1,"a":[2]}`, 1)
	tree2, err := NormalizeTC("dum2", []byte(withNew), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tree2.Qdiscs[1].Netem.Extra; got != `{"new-attribute":{"a":[2],"x":1}}` {
		t.Errorf("extra: %q", got)
	}
	if d := DiffTC(tree, mergeQdiscs(tree, tree2)); len(d) == 0 {
		t.Error("an unknown attribute makes no difference")
	}
	// the u32 filter keeps its options canonical; the hierarchy of classes is kept
	var u32 *NormFilter
	for i := range tree.Filters {
		if tree.Filters[i].Kind == "u32" && strings.Contains(tree.Filters[i].Options, `"match"`) {
			u32 = &tree.Filters[i]
		}
	}
	if u32 == nil || !strings.Contains(u32.Options, `"flowid":"1:10"`) {
		t.Errorf("u32: %+v", u32)
	}
	var c10 NormClass
	for _, c := range tree.Classes {
		if c.ID == "1:10" {
			c10 = c
		}
	}
	if c10.Parent != "1:5" || c10.Leaf != "10:" || c10.Rate != 1250000 || c10.Ceil != 2500000 {
		t.Errorf("class 1:10: %+v", c10)
	}
}

func mergeQdiscs(a, b *NormTree) *NormTree {
	c := *a
	c.Qdiscs = b.Qdiscs
	return &c
}

func TestSpecLeavesOnlyConfiguration(t *testing.T) {
	plain := norm(t, "dum0", "tree_qdisc.json", "tree_class.json", "tree_filter.json")
	withStats := norm(t, "dum0", "tree_qdisc_stats.json", "tree_class_stats.json", "tree_filter_stats.json")
	if plain.Qdiscs[1].Stats != nil || withStats.Qdiscs[1].Stats == nil || withStats.Classes[1].Stats == nil {
		t.Fatal("-s decides whether there are counters")
	}
	if plain.Qdiscs[1].Seed == 0 || plain.Qdiscs[1].Seed != withStats.Qdiscs[1].Seed {
		t.Fatal("the two listings of one recording share their seeds")
	}
	if !reflect.DeepEqual(plain.Spec(), withStats.Spec()) {
		t.Errorf("the configuration differs:\n%v", DiffTC(plain, withStats))
	}
	if d := DiffTC(plain, withStats); len(d) != 0 {
		t.Errorf("diff: %v", d)
	}
	for _, q := range plain.Spec().Qdiscs {
		if q.Seed != 0 || q.Stats != nil {
			t.Errorf("%s keeps seed or stats", q.Handle)
		}
	}
}

func TestCountersAreRead(t *testing.T) {
	tree := norm(t, "dum0", "tree_qdisc_stats.json", "tree_class_stats.json", "tree_filter_stats.json")
	by := map[string]NormQdisc{}
	for _, q := range tree.Qdiscs {
		by[q.Handle] = q
	}
	// 200 packets of 142 bytes went through 26: (600 ms), 50 into the 100 % loss of 27:
	if s := by["26:"].Stats; s == nil || s.Packets != 200 || s.Bytes != 28400 || s.Drops != 0 {
		t.Errorf("26: %+v", s)
	}
	if s := by["27:"].Stats; s == nil || s.Drops != 50 || s.Packets != 0 {
		t.Errorf("27: %+v", s)
	}
	if s := by["1:"].Stats; s == nil || s.Packets != 332 || s.Drops != 50 {
		t.Errorf("htb root: %+v", s)
	}
	var c26 NormClass
	for _, c := range tree.Classes {
		if c.ID == "1:26" {
			c26 = c
		}
	}
	if c26.Stats == nil || c26.Stats.Tokens != 19 || c26.Stats.Packets != 200 {
		t.Errorf("class 1:26: %+v", c26.Stats)
	}
	queued := norm(t, "dum0", "queued_qdisc_stats.json", "queued_class_stats.json", "tree_filter.json")
	for _, q := range queued.Qdiscs {
		if q.Handle == "24:" && (q.Stats == nil || q.Stats.Qlen != 25 || q.Stats.Backlog != 3550 || q.Stats.Packets != 0) {
			t.Errorf("24: queued: %+v", q.Stats)
		}
	}
	over := norm(t, "dum0", "overflow_qdisc_stats.json", "queued_class_stats.json", "tree_filter.json")
	for _, q := range over.Qdiscs {
		if q.Handle == "24:" && (q.Stats == nil || q.Stats.Drops != 25 || q.Stats.Overlimits != 0 || q.Netem.Limit != 10) {
			t.Errorf("24: overflow: %+v %+v", q.Stats, q.Netem)
		}
	}
}

func TestTheSeedMakesARecreatedQdiscVisible(t *testing.T) {
	// two recordings, each made after its own creation of the same handles
	a := norm(t, "dum0", "tree_qdisc.json", "tree_class.json", "tree_filter.json")
	b, err := NormalizeTC("dum0", tcFixture(t, "tree1_qdisc.json"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	seeds := map[string]uint64{}
	for _, q := range b.Qdiscs {
		seeds[q.Handle] = q.Seed
	}
	for _, q := range a.Qdiscs {
		if _, there := seeds[q.Handle]; q.Kind == "netem" && there && (q.Seed == 0 || seeds[q.Handle] == 0 || q.Seed == seeds[q.Handle]) {
			t.Errorf("%s: seed %d and %d of two creations", q.Handle, q.Seed, seeds[q.Handle])
		}
	}
}

func TestTheOwnTreeIsSeparatedFromTheOperatingSystemsQdiscs(t *testing.T) {
	mq, err := NormalizeTC("v0", tcFixture(t, "os_mq_fqcodel.json"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mq.Qdiscs) != 5 || mq.Qdiscs[0].Handle != "0:" || mq.Qdiscs[0].Parent != "8001:2" || mq.Qdiscs[3].Handle != "8001:" || mq.Qdiscs[3].Parent != "root" || mq.Qdiscs[4].Parent != "8001:1" {
		t.Errorf("%+v", mq.Qdiscs)
	}
	if n := len(mq.Subtree("1:").Qdiscs); n != 0 {
		t.Errorf("the mq tree has %d qdiscs of ours", n)
	}
	own := norm(t, "dum0", "tree_qdisc.json", "tree_class.json", "tree_filter.json")
	if sub := own.Subtree("1:"); !reflect.DeepEqual(sub, own) {
		t.Errorf("a tree that is all ours changed: %v", DiffTC(own, sub))
	}
	// OS root qdisc beside our tree: the ingress qdisc and a foreign root are not ours
	mixed := &NormTree{Dev: "x", Qdiscs: []NormQdisc{
		{Handle: "ffff:", Parent: "ingress", Kind: "ingress"},
		{Handle: "8001:", Parent: "root", Kind: "mq"},
		{Handle: "0:", Parent: "8001:1", Kind: "pfifo_fast"},
		{Handle: "1:", Parent: "root", Kind: "htb"},
		{Handle: "24:", Parent: "1:24", Kind: "netem"},
	}, Classes: []NormClass{{ID: "1:24", Parent: "1:", Kind: "htb"}}, Filters: []NormFilter{{Parent: "ffff:", Kind: "u32"}, {Parent: "1:", Kind: "fw"}}}
	sub := mixed.Subtree("1:")
	if len(sub.Qdiscs) != 2 || len(sub.Classes) != 1 || len(sub.Filters) != 1 || sub.Filters[0].Kind != "fw" {
		t.Errorf("subtree: %+v", sub)
	}
}

func TestOlderToolOutputIsRecognized(t *testing.T) {
	// the shapes iproute2 printed before 6.x: a fw filter's selector as "handle", a class's leaf as
	// "10:", no seed (the first recordings of the executor's tests)
	q := `[{"kind":"htb","handle":"1:","dev":"wan0","root":true,"refcnt":2,"options":{"r2q":10,"default":"0x10","direct_packets_stat":0,"direct_qlen":1000}},
	       {"kind":"netem","handle":"10:","dev":"wan0","parent":"1:10","refcnt":1,"options":{"limit":1000,"delay":{"delay":0.05,"jitter":0.01,"correlation":0.25},"loss-random":{"loss":0.01,"correlation":0},"ecn":false,"gap":0}},
	       {"kind":"ingress","handle":"ffff:","dev":"wan0","parent":"ffff:fff1","refcnt":2,"options":{}}]`
	c := `[{"class":"htb","handle":"1:10","dev":"wan0","parent":"1:","leaf":"10:","rate":1000000,"ceil":1000000,"prio":0}]`
	f := `[{"parent":"1:","protocol":"all","pref":49152,"kind":"fw","chain":0},{"parent":"1:","protocol":"all","pref":49152,"kind":"fw","chain":0,"options":{"handle":"0x10","flowid":"1:10"}},
	       {"parent":"1:","protocol":"ip","pref":1,"kind":"fw","chain":0,"options":{"handle":"0xa0/0x1fff0","flowid":"1:10"}}]`
	tree, err := NormalizeTC("wan0", []byte(q), []byte(c), []byte(f))
	if err != nil {
		t.Fatal(err)
	}
	if tree.Qdiscs[1].Parent != "1:10" || tree.Qdiscs[1].Netem.DelayCorr != 0.25 || tree.Qdiscs[1].Seed != 0 || tree.Qdiscs[2].Parent != "ingress" {
		t.Errorf("qdiscs: %+v", tree.Qdiscs)
	}
	if tree.Classes[0].Leaf != "10:" || tree.Classes[0].Parent != "1:" {
		t.Errorf("class: %+v", tree.Classes[0])
	}
	if len(tree.Filters) != 2 || tree.Filters[0].Mark != 0xa0 || tree.Filters[0].Mask != 0x1fff0 || tree.Filters[1].Mark != 0x10 || tree.Filters[1].Mask != math.MaxUint32 {
		t.Errorf("filters: %+v", tree.Filters)
	}
}

func TestMalformedListingsAreRefused(t *testing.T) {
	for name, c := range map[string]struct{ q, c, f string }{
		"not json":            {q: `qdisc htb 1: root`},
		"not a list":          {q: `{"kind":"htb"}`},
		"handle is not hex":   {q: `[{"kind":"htb","handle":"zz:","root":true,"options":{}}]`},
		"handle is a number":  {q: `[{"kind":"htb","handle":1,"root":true,"options":{}}]`},
		"parent is not hex":   {q: `[{"kind":"netem","handle":"1:","parent":"q:1","options":{}}]`},
		"limit is a string":   {q: `[{"kind":"netem","handle":"1:","parent":"1:1","options":{"limit":"many"}}]`},
		"delay is a number":   {q: `[{"kind":"netem","handle":"1:","parent":"1:1","options":{"delay":0.5}}]`},
		"default not hex":     {q: `[{"kind":"htb","handle":"1:","root":true,"options":{"default":"xyz"}}]`},
		"class without id":    {c: `[{"class":"htb","handle":"1:","root":true}]`},
		"class rate is text":  {c: `[{"class":"htb","handle":"1:5","root":true,"rate":"fast"}]`},
		"filter no parent":    {f: `[{"protocol":"ip","pref":1,"kind":"fw","options":{}}]`},
		"filter mark not hex": {f: `[{"parent":"1:","protocol":"ip","pref":1,"kind":"fw","options":{"fw":{"mark":"red"}}}]`},
		"counter is negative": {q: `[{"kind":"htb","handle":"1:","root":true,"options":{},"bytes":-1}]`},
	} {
		if _, err := NormalizeTC("d", []byte(c.q), []byte(c.c), []byte(c.f)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if tree, err := NormalizeTC("d", nil, []byte("  \n"), nil); err != nil || len(tree.Qdiscs) != 0 || tree.Qdiscs == nil {
		t.Errorf("empty output is an empty tree: %+v %v", tree, err)
	}
}

// What the compiler asks for and what the listing prints must meet: the values below are real
// readings of the kernel (VM session of M8b) for the requests in the comments.
func TestTheQuantizersPredictWhatTheKernelReports(t *testing.T) {
	probs := map[float64]float64{
		0.0001:     1.00001e-06,
		0.00001:    9.98843e-08,
		0.0000001:  9.31323e-10,
		33.333333:  0.333333,
		99.99999:   1,
		100:        1,
		0.5:        0.005,
		12.3456789: 0.123457,
		1:          0.01,
		0:          0,
	}
	for pct, want := range probs {
		if got := NetemProb(pct); got != want {
			t.Errorf("NetemProb(%v) = %v, the kernel printed %v", pct, got, want)
		}
	}
	times := map[time.Duration]float64{
		time.Microsecond:                         1e-06,
		1500 * time.Microsecond:                  0.0015,
		100500 * time.Microsecond:                0.1005,
		400 * time.Microsecond:                   0.0004,
		time.Second:                              1,
		2500 * time.Millisecond:                  2.5,
		600 * time.Millisecond:                   0.6,
		12*time.Second + 345678*time.Microsecond: 12.3457,
	}
	for d, want := range times {
		if got := NetemTime(d); got != want {
			t.Errorf("NetemTime(%v) = %v, want %v", d, got, want)
		}
	}
	rates := map[int64]int64{1: 0, 7: 0, 8: 1, 1000: 125, 2_500_000: 312500, 10_000_000_000: 1250000000, 1_000_000_000: 125000000, 100_000_000: 12500000, 0: 0}
	for bits, want := range rates {
		if got := NetemRate(bits); got != want {
			t.Errorf("NetemRate(%d) = %d, want %d", bits, got, want)
		}
	}
}

func TestParseTCHandle(t *testing.T) {
	for in, want := range map[string]string{"1:": "1:", "1:24": "1:24", "24:": "24:", "0x24": "24:", "0X1F": "1f:", "ffff:": "ffff:", "0:": "0:", "01:0a": "1:a", "ffff:fff1": "ffff:fff1", "0x0": "0:"} {
		if got, ok := ParseTCHandle(in); !ok || got != want {
			t.Errorf("ParseTCHandle(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "x", "1:2:3", ":", "g:1", "1:g", "0x", "root"} {
		if got, ok := ParseTCHandle(in); ok {
			t.Errorf("ParseTCHandle(%q) = %q", in, got)
		}
	}
}

func FuzzNormalizeTC(f *testing.F) {
	for _, n := range []string{"tree_qdisc_stats.json", "tree_class_stats.json", "odd_filter.json", "os_mq_fqcodel.json", "odd_class.json"} {
		b, err := os.ReadFile(filepath.Join("testdata", "tc", n))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, b, b)
	}
	f.Add([]byte(`[{"kind":"netem","handle":"1:","parent":"1:1","options":{"delay":{"delay":1e999}}}]`), []byte(`[]`), []byte(`[{"parent":"1:","options":{}}]`))
	f.Fuzz(func(t *testing.T, q, c, fl []byte) {
		tree, err := NormalizeTC("d", q, c, fl)
		if err != nil {
			return
		}
		// whatever is accepted renders, compares with itself and survives a JSON round trip
		_ = tree.Lines()
		if d := DiffTC(tree, tree); len(d) != 0 {
			t.Fatalf("a tree differs from itself: %v", d)
		}
		b, err := json.Marshal(tree)
		if err != nil {
			t.Fatal(err)
		}
		var back NormTree
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		_ = tree.Subtree("1:")
	})
}

// The flower filters of the tunnel faults (M10): listings recorded from kernel 6.8.0-142 with iproute2 6.19. The ingress
// qdisc has its filters in a listing of its own (`filter show dev X ingress`); a flower filter is named by its handle, its
// selector is the outer UDP of a peer, and the one action of an ingress filter redirects to the IFB.
func TestTheNormalizerReadsTheFlowerFiltersOfTheTunnelFaults(t *testing.T) {
	tree, err := NormalizeTCIngress("wan0", tcFixture(t, "flower_wan_qdisc.json"), nil, nil, tcFixture(t, "flower_wan_ingress.json"))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "flower_ingress", tree)
	ing := tree.Ingress()
	if len(ing.Qdiscs) != 1 || ing.Qdiscs[0].Kind != "ingress" || ing.Qdiscs[0].Handle != "ffff:" || ing.Qdiscs[0].Parent != "ingress" || len(ing.Filters) != 1 {
		t.Fatalf("ingress: %+v", ing)
	}
	f := ing.Filters[0]
	want := &FlowerSpec{Handle: 7, IPProto: "udp", SrcIP: "198.51.100.2", SrcPort: 51820, Redirect: "ifb-cgw"}
	if f.Parent != "ingress" || f.Kind != "flower" || f.Pref != 10 || f.Protocol != "ip" || *f.Flower != *want || f.Flowid != "" || f.Options != "" || f.Stats == nil {
		t.Errorf("%+v %+v", f, f.Flower)
	}
	if got := f.Line(); got != "filter parent ingress pref 10 flower handle 7 udp src=198.51.100.2:51820 redirect=ifb-cgw" {
		t.Errorf("line %q", got)
	}
	// the own tree does not include the ingress side, the root qdisc of the host stays out as well
	if own := tree.Subtree("1:"); len(own.Qdiscs) != 0 || len(own.Filters) != 0 {
		t.Errorf("own: %+v", own)
	}

	ifb, err := NormalizeTC("ifb-cgw", tcFixture(t, "flower_ifb_qdisc.json"), tcFixture(t, "flower_ifb_class.json"), tcFixture(t, "flower_ifb_filter.json"))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "flower_ifb", ifb)
	own := ifb.Subtree("1:")
	if len(own.Filters) != 1 || own.Filters[0].Parent != "1:" || own.Filters[0].Flowid != "1:12" || own.Filters[0].Flower.Handle != 7 ||
		own.Filters[0].Flower.SrcIP != "198.51.100.2" || own.Filters[0].Flower.Redirect != "" || own.Filters[0].Pref != 1 {
		t.Errorf("%+v", own.Filters)
	}
	if got := own.Filters[0].Line(); got != "filter parent 1: pref 1 flower handle 7 flowid=1:12 udp src=198.51.100.2:51820" {
		t.Errorf("line %q", got)
	}
}

// What a re-apply can change is what the comparison sees: the selector, the class and the redirect; the counters of the action
// are not configuration.
func TestAFlowerFilterIsComparedByItsSelectorClassAndRedirectNotByItsCounters(t *testing.T) {
	read := func(mod func(string) string) *NormTree {
		raw := mod(string(tcFixture(t, "flower_wan_ingress.json")))
		tree, err := NormalizeTCIngress("wan0", tcFixture(t, "flower_wan_qdisc.json"), nil, nil, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return tree
	}
	base := read(func(s string) string { return s })
	counted := read(func(s string) string {
		return strings.Replace(s, `"stats":{"bytes":0,"packets":0`, `"stats":{"bytes":9000,"packets":60`, 1)
	})
	if d := DiffTC(base.Ingress(), counted.Ingress()); len(d) != 0 {
		t.Errorf("counters differ: %v", d)
	}
	if counted.Ingress().Filters[0].Stats == nil || counted.Ingress().Filters[0].Stats.Packets != 60 || counted.Ingress().Filters[0].Stats.Bytes != 9000 {
		t.Errorf("the counters are not read: %+v", counted.Ingress().Filters[0].Stats)
	}
	if counted.Spec().Filters[0].Stats != nil {
		t.Error("the spec keeps the counters of a filter")
	}
	for name, mod := range map[string]func(string) string{
		"port":     func(s string) string { return strings.Replace(s, `"src_port":51820`, `"src_port":51821`, 1) },
		"address":  func(s string) string { return strings.Replace(s, `198.51.100.2`, `198.51.100.3`, 1) },
		"redirect": func(s string) string { return strings.Replace(s, `"to_dev":"ifb-cgw"`, `"to_dev":"ifb0"`, 1) },
		"protocol": func(s string) string { return strings.Replace(s, `"ip_proto":"udp"`, `"ip_proto":"tcp"`, 1) },
		"another key": func(s string) string {
			return strings.Replace(s, `"src_port":51820`, `"src_port":51820,"dst_ip":"10.0.0.1"`, 1)
		},
		"another action": func(s string) string {
			return strings.Replace(s, `"mirred_action":"redirect"`, `"mirred_action":"mirror"`, 1)
		},
	} {
		d := DiffTC(base.Ingress(), read(mod).Ingress())
		if len(d) != 1 || !strings.HasPrefix(d[0], "different:") {
			t.Errorf("%s: %v", name, d)
		}
	}
	// a handle is another filter
	d := DiffTC(base.Ingress(), read(func(s string) string { return strings.Replace(s, `"handle":7`, `"handle":8`, 1) }).Ingress())
	if len(d) != 2 {
		t.Errorf("handle: %v", d)
	}
}

// A filter of the host on the ingress qdisc is kept and is not a flower filter of ours.
func TestAForeignFilterOnTheIngressQdiscIsKeptApart(t *testing.T) {
	raw := `[{"parent":"ffff:","protocol":"all","pref":49152,"kind":"u32","chain":0},{"parent":"ffff:","protocol":"all","pref":49152,"kind":"u32","chain":0,"options":{"fh":"800:","ht_divisor":1}},` +
		strings.TrimPrefix(string(tcFixture(t, "flower_wan_ingress.json")), "[")
	tree, err := NormalizeTCIngress("wan0", tcFixture(t, "flower_wan_qdisc.json"), nil, nil, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, f := range tree.Ingress().Filters {
		kinds = append(kinds, f.Kind)
	}
	if len(kinds) != 2 || !(kinds[0] == "u32" && kinds[1] == "flower" || kinds[1] == "u32" && kinds[0] == "flower") {
		t.Errorf("%v", kinds)
	}
}
