package kernelsim

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The tc state of an interface: what the compiler's tree needs and nothing else. The tool answers the
// way iproute2 6.19 does for `tc -s -j qdisc|class|filter show dev X` (the listings are the ones
// recorded from a real kernel, internal/linux/testdata/tc), and a batch of `qdisc|class|filter
// add|replace|change|delete` lines is applied one line at a time with the answers the kernel gives for
// the cases the fault engine meets (docs/development.md, "tc operations on the kernel"): an HTB root
// cannot be changed once it exists, a root of the host is taken over by `replace` (not `add`), a
// class that a filter selects cannot be deleted, deleting what is not there is an error that a
// `-force` batch goes on after, a netem `replace` keeps the queue of the qdisc and its seed.
//
// What it does not do: queue packets (backlog and counters are what a test sets with SetTCStats),
// keep the attributes a `change` is not given (the compiler's sets are complete), and it knows only
// the kinds htb and netem, the filter kind fw and the host's `noqueue`.

type simTC struct {
	root    *simQdisc // nil: the host's noqueue
	leaves  map[string]*simQdisc
	classes map[string]*simClass
	filters []*simFilter
}

type simQdisc struct {
	kind, handle, parent string
	htbDefault           uint64
	netem                linux.NetemSpec
	seed                 uint64
	table                string // the distribution table: what the listing does not show
	stats                linux.NormStats
}

type simClass struct {
	id, parent, leaf string
	rate, ceil       int64
	prio             int
	stats            linux.NormStats
}

type simFilter struct {
	parent, proto, kind string
	pref                int
	mark, mask          uint32
	flowid              string
}

func (k *Kernel) tcOf(l *link) *simTC {
	if l.tc == nil {
		l.tc = &simTC{leaves: map[string]*simQdisc{}, classes: map[string]*simClass{}}
	}
	return l.tc
}

// SetTCStats sets the counters of the qdisc with the handle (a leaf "24:" or the root "1:") of an
// interface, for tests that need a queue that holds packets or counters that moved.
func (k *Kernel) SetTCStats(dev, handle string, s linux.NormStats) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l := k.links[dev]
	if l == nil || l.tc == nil {
		return
	}
	if l.tc.root != nil && l.tc.root.handle == handle {
		l.tc.root.stats = s
		return
	}
	for _, q := range l.tc.leaves {
		if q.handle == handle {
			q.stats = s
		}
	}
}

// TCTable returns the distribution table the netem qdisc with the handle holds ("" for none): the
// listing does not show it, the kernel keeps it across a change that names none.
func (k *Kernel) TCTable(dev, handle string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	l := k.links[dev]
	if l == nil || l.tc == nil {
		return ""
	}
	for _, q := range l.tc.leaves {
		if q.handle == handle {
			return q.table
		}
	}
	return ""
}

// netemTable is the distribution table the netem arguments name.
func netemTable(a []string) string {
	for i := 0; i+1 < len(a); i++ {
		if a[i] == "distribution" {
			return a[i+1]
		}
	}
	return ""
}

// TCSeed returns the seed of the qdisc with the handle: it changes when the qdisc is created again.
func (k *Kernel) TCSeed(dev, handle string) uint64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	l := k.links[dev]
	if l == nil || l.tc == nil {
		return 0
	}
	for _, q := range l.tc.leaves {
		if q.handle == handle {
			return q.seed
		}
	}
	return 0
}

func (k *Kernel) tcCmd(c executor.Command) (executor.Result, error) {
	a := c.Args
	force := false
	for len(a) > 0 && strings.HasPrefix(a[0], "-") && a[0] != "-batch" && a[0] != "-j" && a[0] != "-s" {
		if a[0] == "-force" {
			force = true
		}
		a = a[1:]
	}
	if len(a) >= 2 && a[0] == "-batch" {
		return k.tcBatch(strings.Split(strings.TrimSpace(c.Stdin), "\n"), force)
	}
	for len(a) > 0 && (a[0] == "-s" || a[0] == "-j") {
		a = a[1:]
	}
	// <kind> show dev X
	if len(a) == 4 && a[1] == "show" && a[2] == "dev" {
		return k.tcShow(a[0], a[3])
	}
	if len(a) == 2 && a[1] == "show" {
		return executor.Result{Exit: 1, Stderr: "the simulated tc lists one interface at a time\n"}, nil
	}
	return executor.Result{Exit: 1, Stderr: "the simulated tc does not know: " + strings.Join(c.Args, " ") + "\n"}, nil
}

func (k *Kernel) tcBatch(lines []string, force bool) (executor.Result, error) {
	var stderr strings.Builder
	exit := 0
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if msg := k.tcLine(f); msg != "" {
			stderr.WriteString(msg + "\n")
			fmt.Fprintf(&stderr, "Command failed -:%d\n", i+1)
			exit = 1
			if !force {
				break
			}
		}
	}
	return executor.Result{Stderr: stderr.String(), Exit: exit}, nil
}

// tcLine runs one line and returns the tool's error message, empty on success.
func (k *Kernel) tcLine(f []string) string {
	if len(f) < 4 || f[2] != "dev" {
		return "Error: malformed line"
	}
	object, action, dev := f[0], f[1], f[3]
	l := k.links[dev]
	if l == nil {
		return fmt.Sprintf("Cannot find device %q", dev)
	}
	tc := k.tcOf(l)
	// the words after the device up to the kind: root | parent P, handle H, classid C
	var parent, handle, classid string
	rest := f[4:]
	for len(rest) > 0 {
		switch rest[0] {
		case "root":
			parent, rest = "root", rest[1:]
			continue
		case "parent":
			if len(rest) < 2 {
				return "Error: malformed line"
			}
			parent, rest = rest[1], rest[2:]
			continue
		case "handle":
			if len(rest) < 2 {
				return "Error: malformed line"
			}
			handle, rest = rest[1], rest[2:]
			continue
		case "classid":
			if len(rest) < 2 {
				return "Error: malformed line"
			}
			classid, rest = rest[1], rest[2:]
			continue
		}
		break
	}
	switch object {
	case "qdisc":
		return k.tcQdisc(tc, action, parent, handle, rest)
	case "class":
		return k.tcClass(tc, action, parent, classid, rest)
	case "filter":
		return k.tcFilter(tc, action, parent, handle, rest)
	}
	return "Error: unknown object " + object
}

func (k *Kernel) tcQdisc(tc *simTC, action, parent, handle string, args []string) string {
	if action == "delete" {
		if parent == "root" {
			if tc.root == nil || tc.root.handle != handle {
				return "Error: Invalid handle."
			}
			tc.root, tc.leaves, tc.classes, tc.filters = nil, map[string]*simQdisc{}, map[string]*simClass{}, nil
			return ""
		}
		q := tc.leaves[parent]
		if q == nil || q.handle != handle {
			return "Error: Failed to find qdisc with specified handle."
		}
		delete(tc.leaves, parent)
		if c := tc.classes[parent]; c != nil {
			c.leaf = ""
		}
		return ""
	}
	if len(args) == 0 {
		return "Error: a qdisc needs a kind"
	}
	kind, args := args[0], args[1:]
	if parent == "root" {
		if kind != "htb" || handle != "1:" {
			return "Error: the simulated tc takes only an htb root 1:"
		}
		if tc.root != nil {
			// the kernel refuses every change of an HTB root, and `add` of a second one
			if action == "add" {
				return "Error: Exclusivity flag on, cannot modify."
			}
			return "Error: Change operation not supported by specified qdisc."
		}
		q := &simQdisc{kind: "htb", handle: handle, parent: "root", htbDefault: 0}
		for i := 0; i+1 < len(args); i += 2 {
			if args[i] == "default" {
				n, err := strconv.ParseUint(args[i+1], 16, 32)
				if err != nil {
					return "Error: invalid default"
				}
				q.htbDefault = n
			}
		}
		tc.root = q
		return ""
	}
	if tc.root == nil {
		return "Error: Parent Qdisc doesn't exists."
	}
	if kind != "netem" {
		return "Error: the simulated tc takes only netem leaves"
	}
	if tc.classes[parent] == nil {
		return "Error: Parent Qdisc doesn't exists."
	}
	old := tc.leaves[parent]
	switch {
	case action == "add" && old != nil:
		return "Error: Exclusivity flag on, cannot modify."
	case action == "change" && old == nil:
		return "Error: Cannot find specified qdisc on specified device."
	}
	spec, err := parseNetem(args)
	if err != nil {
		return "Error: " + err.Error()
	}
	table := netemTable(args)
	if old != nil {
		// a change in place: the queue, the counters and the seed stay; so does the distribution
		// table when the change names none
		old.netem = spec
		if table != "" {
			old.table = table
		}
		return ""
	}
	k.tcSeed++
	tc.leaves[parent] = &simQdisc{kind: "netem", handle: handle, parent: parent, netem: spec, seed: k.tcSeed*7919 + 1, table: table}
	tc.classes[parent].leaf = handle
	return ""
}

func (k *Kernel) tcClass(tc *simTC, action, parent, classid string, args []string) string {
	if action == "delete" {
		c := tc.classes[classid]
		if c == nil {
			return "Error: Specified class not found."
		}
		for _, f := range tc.filters {
			if f.flowid == classid {
				return "Error: HTB class in use"
			}
		}
		delete(tc.classes, classid)
		delete(tc.leaves, classid)
		return ""
	}
	if tc.root == nil || parent != tc.root.handle {
		return "Error: Parent Qdisc doesn't exists."
	}
	if len(args) == 0 || args[0] != "htb" {
		return "Error: the simulated tc takes only htb classes"
	}
	args = args[1:]
	var rate, ceil int64
	for i := 0; i+1 < len(args); i += 2 {
		bits, err := parseRateBits(args[i+1])
		switch args[i] {
		case "rate":
			rate = bits / 8
			if err != nil {
				return "Error: invalid rate"
			}
		case "ceil":
			ceil = bits / 8
		}
	}
	if rate == 0 {
		return "Error: HTB: invalid rate"
	}
	if ceil == 0 {
		ceil = rate
	}
	old := tc.classes[classid]
	switch {
	case action == "add" && old != nil:
		return "Error: Exclusivity flag on, cannot modify."
	case action == "change" && old == nil:
		return "Error: Specified class not found."
	}
	if old != nil {
		old.rate, old.ceil = rate, ceil
		return ""
	}
	tc.classes[classid] = &simClass{id: classid, parent: parent, rate: rate, ceil: ceil}
	return ""
}

func (k *Kernel) tcFilter(tc *simTC, action, parent, handle string, args []string) string {
	if tc.root == nil || parent != tc.root.handle {
		if action == "delete" {
			return "Error: Cannot find specified filter chain."
		}
		return "Error: Parent Qdisc doesn't exists."
	}
	// protocol P prio N fw [flowid C]
	f := &simFilter{parent: parent}
	for len(args) > 0 {
		switch {
		case args[0] == "protocol" && len(args) > 1:
			f.proto, args = args[1], args[2:]
		case args[0] == "prio" && len(args) > 1:
			n, err := strconv.Atoi(args[1])
			if err != nil {
				return "Error: invalid prio"
			}
			f.pref, args = n, args[2:]
		case args[0] == "fw":
			f.kind, args = "fw", args[1:]
		case args[0] == "flowid" && len(args) > 1:
			f.flowid, args = args[1], args[2:]
		default:
			return "Error: the simulated tc does not know " + args[0]
		}
	}
	mark, mask, _ := strings.Cut(handle, "/")
	m, err1 := strconv.ParseUint(strings.TrimPrefix(mark, "0x"), 16, 32)
	n := uint64(0xffffffff)
	var err2 error
	if mask != "" {
		n, err2 = strconv.ParseUint(strings.TrimPrefix(mask, "0x"), 16, 32)
	}
	if err1 != nil || err2 != nil || f.kind != "fw" {
		return "Error: invalid filter handle"
	}
	f.mark, f.mask = uint32(m), uint32(n)
	idx := -1
	for i, x := range tc.filters {
		if x.pref == f.pref && x.proto == f.proto && x.mark == f.mark && x.mask == f.mask {
			idx = i
		}
	}
	switch action {
	case "delete":
		if idx < 0 {
			return "Error: Specified filter handle not found."
		}
		tc.filters = append(tc.filters[:idx], tc.filters[idx+1:]...)
		return ""
	case "add":
		if idx >= 0 {
			return "Error: Filter with specified priority/protocol not found."
		}
	}
	if tc.classes[f.flowid] == nil {
		return "Error: Specified class not found."
	}
	if idx >= 0 {
		tc.filters[idx] = f
		return ""
	}
	tc.filters = append(tc.filters, f)
	return ""
}

// ---- reading ----------------------------------------------------------------------------------

func (k *Kernel) tcShow(kind, dev string) (executor.Result, error) {
	l := k.links[dev]
	if l == nil {
		return executor.Result{Exit: 1, Stderr: fmt.Sprintf("Cannot find device %q\n", dev)}, nil
	}
	tc := k.tcOf(l)
	var out []any
	switch kind {
	case "qdisc":
		if tc.root == nil {
			out = append(out, map[string]any{"kind": "noqueue", "handle": "0:", "root": true, "refcnt": 2, "options": map[string]any{}})
			break
		}
		root := map[string]any{"kind": "htb", "handle": tc.root.handle, "root": true, "refcnt": 2,
			"options": map[string]any{"r2q": 10, "default": fmt.Sprintf("%#x", tc.root.htbDefault), "direct_packets_stat": tc.root.stats.DirectPackets, "direct_qlen": 1000}}
		addStats(root, tc.root.stats)
		out = append(out, root)
		for _, parent := range sortedKeys(tc.leaves) {
			q := tc.leaves[parent]
			m := map[string]any{"kind": "netem", "handle": q.handle, "parent": parent, "options": netemJSON(q)}
			addStats(m, q.stats)
			out = append(out, m)
		}
	case "class":
		for _, id := range sortedKeys(tc.classes) {
			c := tc.classes[id]
			m := map[string]any{"class": "htb", "handle": c.id, "root": true, "prio": c.prio, "rate": c.rate, "ceil": c.ceil, "burst": 1680, "cburst": 1680}
			if c.leaf != "" {
				m["leaf"] = "0x" + strings.TrimSuffix(c.leaf, ":")
			}
			s := map[string]any{}
			addStats(s, c.stats)
			s["lended"], s["borrowed"], s["giants"], s["tokens"], s["ctokens"] = 0, 0, 0, 20, 20
			m["stats"] = s
			out = append(out, m)
		}
	case "filter":
		for _, f := range tc.filters {
			h := map[string]any{"parent": f.parent, "protocol": f.proto, "pref": f.pref, "kind": f.kind, "chain": 0}
			out = append(out, h)
			e := map[string]any{"parent": f.parent, "protocol": f.proto, "pref": f.pref, "kind": f.kind, "chain": 0,
				"options": map[string]any{"fw": map[string]any{"mark": fmt.Sprintf("%#x", f.mark), "mask": fmt.Sprintf("%#x", f.mask)}, "classid": f.flowid}}
			out = append(out, e)
		}
	default:
		return executor.Result{Exit: 1, Stderr: "unknown object " + kind + "\n"}, nil
	}
	if out == nil {
		out = []any{}
	}
	return jsonOut(out)
}

func addStats(m map[string]any, s linux.NormStats) {
	m["bytes"], m["packets"], m["drops"], m["overlimits"], m["requeues"], m["backlog"], m["qlen"] =
		s.Bytes, s.Packets, s.Drops, s.Overlimits, s.Requeues, s.Backlog, s.Qlen
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// numeric order of the minors ("1:5" before "1:24")
	sortHandles(keys)
	return keys
}

func sortHandles(s []string) {
	less := func(a, b string) bool {
		_, an, _ := strings.Cut(a, ":")
		_, bn, _ := strings.Cut(b, ":")
		x, _ := strconv.ParseUint(an, 16, 32)
		y, _ := strconv.ParseUint(bn, 16, 32)
		if x != y {
			return x < y
		}
		return a < b
	}
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// netemJSON is the options of a netem qdisc the way `tc -j` prints them: an attribute that is
// neutral is not there.
func netemJSON(q *simQdisc) map[string]any {
	n := q.netem
	o := map[string]any{"limit": n.Limit}
	if n.Delay != 0 || n.Jitter != 0 {
		o["delay"] = map[string]any{"delay": n.Delay, "jitter": n.Jitter, "correlation": n.DelayCorr}
	}
	switch {
	case n.Gemodel != nil:
		o["loss-gemodel"] = map[string]any{"p": n.Gemodel.P, "r": n.Gemodel.R, "1-h": n.Gemodel.OneMinusH, "1-k": n.Gemodel.OneMinusK}
	case n.Loss != 0:
		o["loss-random"] = map[string]any{"loss": n.Loss, "correlation": n.LossCorr}
	}
	if n.Reorder != 0 {
		o["reorder"] = map[string]any{"reorder": n.Reorder, "correlation": n.ReorderCorr}
	}
	if n.Duplicate != 0 {
		o["duplicate"] = map[string]any{"duplicate": n.Duplicate, "correlation": n.DuplicateCorr}
	}
	if n.Corrupt != 0 {
		o["corrupt"] = map[string]any{"corrupt": n.Corrupt, "correlation": n.CorruptCorr}
	}
	if n.Rate != 0 {
		o["rate"] = map[string]any{"rate": n.Rate, "packetoverhead": 0, "cellsize": 0, "celloverhead": 0}
	}
	o["seed"], o["ecn"], o["gap"] = q.seed, false, n.Gap
	return o
}

// ---- parsing the arguments the compiler writes ------------------------------------------------

// parseNetem reads `limit N delay D J C [distribution X] loss random P C | gemodel p r 1-h 1-k
// reorder P C duplicate P C corrupt P C rate R`; an attribute that is not given is neutral.
func parseNetem(a []string) (linux.NetemSpec, error) {
	var n linux.NetemSpec
	n.Limit = 1000
	take := func(i, want int) ([]string, error) {
		if i+want >= len(a) {
			return nil, fmt.Errorf("%s needs %d values", a[i], want)
		}
		return a[i+1 : i+1+want], nil
	}
	pct := func(s string) (float64, error) {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil || v < 0 || v > 100 {
			return 0, fmt.Errorf("%q is not a percentage", s)
		}
		return linux.NetemProb(v), nil
	}
	for i := 0; i < len(a); {
		switch a[i] {
		case "limit":
			v, err := take(i, 1)
			if err != nil {
				return n, err
			}
			if n.Limit, err = strconv.Atoi(v[0]); err != nil {
				return n, err
			}
			i += 2
		case "delay":
			v, err := take(i, 3)
			if err != nil {
				return n, err
			}
			d, err1 := parseDur(v[0])
			j, err2 := parseDur(v[1])
			c, err3 := pct(v[2])
			if err1 != nil || err2 != nil || err3 != nil {
				return n, fmt.Errorf("bad delay %v", v)
			}
			n.Delay, n.Jitter, n.DelayCorr = linux.NetemTime(d), linux.NetemTime(j), c
			i += 4
		case "distribution":
			if _, err := take(i, 1); err != nil {
				return n, err
			}
			i += 2
		case "loss":
			if i+1 >= len(a) {
				return n, fmt.Errorf("loss needs a model")
			}
			switch a[i+1] {
			case "random":
				v, err := take(i+1, 2)
				if err != nil {
					return n, err
				}
				p, err1 := pct(v[0])
				c, err2 := pct(v[1])
				if err1 != nil || err2 != nil {
					return n, fmt.Errorf("bad loss %v", v)
				}
				n.Loss, n.LossCorr, n.Gemodel = p, c, nil
				i += 4
			case "gemodel":
				v, err := take(i+1, 4)
				if err != nil {
					return n, err
				}
				var g [4]float64
				for x := range g {
					if g[x], err = pct(v[x]); err != nil {
						return n, err
					}
				}
				n.Gemodel = &linux.GemodelSpec{P: g[0], R: g[1], OneMinusH: g[2], OneMinusK: g[3]}
				n.Loss, n.LossCorr = 0, 0
				i += 6
			default:
				return n, fmt.Errorf("loss model %q", a[i+1])
			}
		case "reorder", "duplicate", "corrupt":
			v, err := take(i, 2)
			if err != nil {
				return n, err
			}
			p, err1 := pct(v[0])
			c, err2 := pct(v[1])
			if err1 != nil || err2 != nil {
				return n, fmt.Errorf("bad %s %v", a[i], v)
			}
			switch a[i] {
			case "reorder":
				n.Reorder, n.ReorderCorr = p, c
				if p > 0 {
					n.Gap = 1
				}
			case "duplicate":
				n.Duplicate, n.DuplicateCorr = p, c
			default:
				n.Corrupt, n.CorruptCorr = p, c
			}
			i += 3
		case "rate":
			v, err := take(i, 1)
			if err != nil {
				return n, err
			}
			bits, err := parseRateBits(v[0])
			if err != nil {
				return n, err
			}
			n.Rate = linux.NetemRate(bits)
			i += 2
		default:
			return n, fmt.Errorf("the simulated netem does not know %q", a[i])
		}
	}
	return n, nil
}

func parseDur(s string) (time.Duration, error) {
	for _, u := range []struct {
		suffix string
		unit   time.Duration
	}{{"usec", time.Microsecond}, {"us", time.Microsecond}, {"msec", time.Millisecond}, {"ms", time.Millisecond}, {"sec", time.Second}, {"s", time.Second}} {
		if v, ok := strings.CutSuffix(s, u.suffix); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, err
			}
			return time.Duration(f * float64(u.unit)), nil
		}
	}
	return 0, fmt.Errorf("%q is not a duration", s)
}

func parseRateBits(s string) (int64, error) {
	l := strings.ToLower(s)
	for _, u := range []struct {
		suffix string
		mul    float64
	}{{"gbit", 1e9}, {"mbit", 1e6}, {"kbit", 1e3}, {"bit", 1}} {
		if v, ok := strings.CutSuffix(l, u.suffix); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, err
			}
			return int64(f * u.mul), nil
		}
	}
	return 0, fmt.Errorf("%q is not a rate", s)
}
