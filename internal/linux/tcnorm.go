package linux

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The normalized form of `tc -j` output (plan §3.2, M8b).
//
// `tc -j` prints what the kernel holds in the tool's own idioms: fractions as `%g` floats with six
// significant digits, times in seconds, rates in bytes per second, handles as "1:24" or "0x24"
// depending on the object, a class under the root as `"root":true`, a bare header entry before each
// filter, and, since iproute2 6.x, a random `seed` per netem instance. None of that can be compared
// with what the compiler asks for as it stands. NormalizeTC turns the three listings of one
// interface (qdiscs, classes, filters) into a NormTree in which

//   - every object has one canonical identity (handle "24:", class "1:24", parent "root" or "1:"),
//   - every number is what the kernel reports for the value requested (see NetemProb, NetemTime and
//     NetemRate, which compute that for the compiler's side),
//   - what is not configuration (counters, backlog, seed, burst, reference counts) is kept apart,
//     so Spec() leaves exactly what a re-apply could change, and
//   - an option this code does not know stays in Extra (a canonical JSON string) instead of being
//     dropped, so a new attribute makes a difference visible rather than invisible.
//
// The tool prints no `dev` when it is asked for one interface; the caller names it.

// NormTree is the tc state of one interface.
type NormTree struct {
	Dev     string       `json:"dev"`
	Qdiscs  []NormQdisc  `json:"qdiscs"`
	Classes []NormClass  `json:"classes"`
	Filters []NormFilter `json:"filters"`
}

// NormQdisc is one qdisc. Parent is "root", "ingress", "clsact" or a class ("1:24").
type NormQdisc struct {
	Handle string `json:"handle"`
	Parent string `json:"parent"`
	Kind   string `json:"kind"`
	// HTB and Netem are set for those kinds. Options is the canonical form of the options of every
	// other kind.
	HTB     *HTBSpec   `json:"htb,omitempty"`
	Netem   *NetemSpec `json:"netem,omitempty"`
	Options string     `json:"options,omitempty"`

	// Seed is the PRNG seed of a netem instance: the kernel draws one when the qdisc is created. Kernels
	// 6.8 and 7.0 keep it through every change, 6.17 draws a new one at each, so it does not say that a
	// qdisc was re-created (its counters starting again do). It is not configuration and not part of
	// Spec(). Zero when the tool does not print one.
	Seed uint64 `json:"seed,omitempty"`
	// Stats is set when the listing was made with `-s`.
	Stats *NormStats `json:"stats,omitempty"`
}

// HTBSpec is the configuration of an HTB qdisc.
type HTBSpec struct {
	Default    uint32 `json:"default"`
	R2Q        int    `json:"r2q"`
	DirectQlen int    `json:"direct_qlen"`
}

// NetemSpec is the configuration of a netem qdisc in the units the kernel reports: probabilities
// as fractions (0.01 is one percent), times in seconds, the rate in bytes per second. The values
// have been through the tool's six-digit printing already (Round6), so two NetemSpecs are equal
// exactly when the kernel holds the same configuration. A neutral attribute is zero, as the tool
// prints nothing for it.
type NetemSpec struct {
	Limit int `json:"limit"`

	Delay     float64 `json:"delay"`
	Jitter    float64 `json:"jitter"`
	DelayCorr float64 `json:"delay_corr"`

	// Loss and LossCorr are the random loss model; Gemodel replaces it.
	Loss     float64      `json:"loss"`
	LossCorr float64      `json:"loss_corr"`
	Gemodel  *GemodelSpec `json:"gemodel,omitempty"`

	Reorder     float64 `json:"reorder"`
	ReorderCorr float64 `json:"reorder_corr"`
	// Gap is the reorder distance; tc sets 1 whenever a reorder probability is given.
	Gap int `json:"gap"`

	Duplicate     float64 `json:"duplicate"`
	DuplicateCorr float64 `json:"duplicate_corr"`
	Corrupt       float64 `json:"corrupt"`
	CorruptCorr   float64 `json:"corrupt_corr"`

	// Rate is in bytes per second, 0 for none.
	Rate           int64 `json:"rate"`
	PacketOverhead int   `json:"packet_overhead"`
	CellSize       int   `json:"cell_size"`
	CellOverhead   int   `json:"cell_overhead"`

	Slot *SlotSpec `json:"slot,omitempty"`
	ECN  bool      `json:"ecn"`

	// Extra holds options this version does not know, as canonical JSON.
	Extra string `json:"extra,omitempty"`
}

// GemodelSpec is the Gilbert-Elliott loss model as tc names its parameters: p, r, 1-h and 1-k.
type GemodelSpec struct {
	P         float64 `json:"p"`
	R         float64 `json:"r"`
	OneMinusH float64 `json:"one_minus_h"`
	OneMinusK float64 `json:"one_minus_k"`
}

// SlotSpec is netem's slot configuration.
type SlotSpec struct {
	MinDelay float64 `json:"min_delay"`
	MaxDelay float64 `json:"max_delay"`
	Packets  int     `json:"packets"`
	Bytes    int     `json:"bytes"`
	Extra    string  `json:"extra,omitempty"`
}

// NormClass is one class. Parent is the qdisc ("1:") or class ("1:5") above it, Leaf the handle of
// the qdisc below it ("24:", empty when it has none of its own).
type NormClass struct {
	ID     string `json:"id"`
	Parent string `json:"parent"`
	Kind   string `json:"kind"`
	Leaf   string `json:"leaf,omitempty"`

	// Rate and Ceil are in bytes per second, Prio is HTB's priority. Burst and Cburst are what the
	// kernel computed from the rate (and the tool's clock): reported, never compared. The quantum
	// is not reported by the kernel at all.
	Rate   int64 `json:"rate"`
	Ceil   int64 `json:"ceil"`
	Prio   int   `json:"prio"`
	Burst  int   `json:"burst,omitempty"`
	Cburst int   `json:"cburst,omitempty"`

	Extra string     `json:"extra,omitempty"`
	Stats *NormStats `json:"stats,omitempty"`
}

// NormFilter is one filter. For `fw`, Mark and Mask are the packet mark bits it selects (an
// "0xa0/0x1fff0" handle) and Flowid the class; other kinds keep their options canonical in Options.
type NormFilter struct {
	Parent   string `json:"parent"`
	Protocol string `json:"protocol"`
	Pref     int    `json:"pref"`
	Kind     string `json:"kind"`
	Mark     uint32 `json:"mark,omitempty"`
	Mask     uint32 `json:"mask,omitempty"`
	Flowid   string `json:"flowid,omitempty"`
	Options  string `json:"options,omitempty"`
	// Flower is set for a flower filter (the tunnel faults of M10): the selector on the outer UDP,
	// the handle that names the filter and, on an ingress qdisc, the device it redirects to.
	Flower *FlowerSpec `json:"flower,omitempty"`
	// Stats are the counters of the redirect action of an ingress flower filter, set when the
	// listing was made with `-s`: the packets the filter matched.
	Stats *NormStats `json:"stats,omitempty"`
}

// FlowerSpec is a flower filter the way Chaos Gateway writes it: the protocol, source address and
// source port of the outer UDP of a WireGuard peer, and, on the ingress qdisc, the one action `mirred
// egress redirect dev <IFB>`. Anything else the listing shows is kept in NormFilter.Options and
// FlowerSpec.Extra, so that it is visible as a difference.
type FlowerSpec struct {
	Handle  int    `json:"handle"`
	IPProto string `json:"ip_proto,omitempty"`
	SrcIP   string `json:"src_ip,omitempty"`
	SrcPort int    `json:"src_port,omitempty"`
	// Redirect is the device of the mirred redirect action; empty for a filter that selects a class.
	Redirect string `json:"redirect,omitempty"`
	// Extra is the canonical form of the keys and the actions this code does not know.
	Extra string `json:"extra,omitempty"`
}

// NormStats are the counters of a qdisc or class, as `tc -s -j` prints them. They only grow
// (Backlog, Qlen and the HTB tokens excepted) until the object is created again. For netem,
// Drops counts everything the qdisc dropped: the loss it was configured to produce and the packets
// that did not fit into its limit (Overlimits stays 0).
type NormStats struct {
	Bytes      uint64 `json:"bytes"`
	Packets    uint64 `json:"packets"`
	Drops      uint64 `json:"drops"`
	Overlimits uint64 `json:"overlimits"`
	Requeues   uint64 `json:"requeues"`
	Backlog    uint64 `json:"backlog"`
	Qlen       uint64 `json:"qlen"`
	// HTB classes only
	Lended   uint64 `json:"lended,omitempty"`
	Borrowed uint64 `json:"borrowed,omitempty"`
	Giants   uint64 `json:"giants,omitempty"`
	Tokens   int64  `json:"tokens,omitempty"`
	Ctokens  int64  `json:"ctokens,omitempty"`
	// DirectPackets is the HTB qdisc's count of packets that went to no class
	DirectPackets uint64 `json:"direct_packets,omitempty"`
}

// Round6 rounds to six significant digits, which is what iproute2 prints for a float (`%g`).
func Round6(f float64) float64 {
	r, err := strconv.ParseFloat(strconv.FormatFloat(f, 'g', 6, 64), 64)
	if err != nil {
		return f
	}
	return r
}

// NetemProb is what `tc -j` prints for a probability the compiler asks for in percent: tc stores
// it as a 32-bit fraction (rint(p/100 * 0xffffffff)) and the listing divides it back and prints
// six digits. A request of 99.99999 % therefore reads back as 1, which is the same configuration.
func NetemProb(percent float64) float64 {
	u := math.Round(percent / 100 * math.MaxUint32)
	u = math.Min(math.Max(u, 0), math.MaxUint32)
	return Round6(u / math.MaxUint32)
}

// NetemTime is what `tc -j` prints, in seconds, for a delay or jitter. The kernel keeps
// nanoseconds, tc takes microseconds at best, and the listing prints six digits.
func NetemTime(d time.Duration) float64 { return Round6(float64(d.Microseconds()) / 1e6) }

// NetemRate is the rate in bytes per second the kernel holds for a rate in bit/s (tc divides by
// eight and truncates, so 7 bit/s is no rate at all).
func NetemRate(bitsPerSecond int64) int64 { return bitsPerSecond / 8 }

// ParseTCHandle reads a handle or class id the way the listings print it: "1:", "1:24", "24:" and
// "0x24" (a bare hex number is a major, as in a class's `leaf`). It returns the canonical form.
func ParseTCHandle(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		maj, min := s[:i], s[i+1:]
		if !isHex(maj) || (min != "" && !isHex(min)) {
			return "", false
		}
		return strings.ToLower(trimZeros(maj)) + ":" + strings.ToLower(trimZeros(min)), true
	}
	h := strings.TrimPrefix(strings.ToLower(s), "0x")
	if !isHex(h) {
		return "", false
	}
	return trimZeros(h) + ":", true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// trimZeros drops leading zeros of a hex number but keeps a "0".
func trimZeros(s string) string {
	t := strings.TrimLeft(s, "0")
	if t == "" && s != "" {
		return "0"
	}
	return t
}

type rawObj = map[string]json.RawMessage

// NormalizeTC turns the output of `tc -j [-s] qdisc|class|filter show dev <dev>` into a NormTree.
// An empty output is an empty list (some versions print nothing for it). An entry that names
// another interface is ignored, so the output of a listing of all interfaces can be passed too.
func NormalizeTC(dev string, qdiscs, classes, filters []byte) (*NormTree, error) {
	return NormalizeTCIngress(dev, qdiscs, classes, filters, nil)
}

// NormalizeTCIngress is NormalizeTC with the listing of the filters of the ingress qdisc
// (`tc -j filter show dev <dev> ingress`), which `filter show` does not include: its filters get
// the parent "ingress". The tunnel faults of M10 put flower filters there.
func NormalizeTCIngress(dev string, qdiscs, classes, filters, ingress []byte) (*NormTree, error) {
	t := &NormTree{Dev: dev, Qdiscs: []NormQdisc{}, Classes: []NormClass{}, Filters: []NormFilter{}}
	qs, err := rawList("tc qdisc", qdiscs)
	if err != nil {
		return nil, err
	}
	for _, o := range qs {
		if !forDev(o, dev) {
			continue
		}
		q, err := normQdisc(o)
		if err != nil {
			return nil, fmt.Errorf("tc qdisc: %w", err)
		}
		t.Qdiscs = append(t.Qdiscs, q)
	}
	cs, err := rawList("tc class", classes)
	if err != nil {
		return nil, err
	}
	for _, o := range cs {
		if !forDev(o, dev) {
			continue
		}
		c, err := normClass(o)
		if err != nil {
			return nil, fmt.Errorf("tc class: %w", err)
		}
		t.Classes = append(t.Classes, c)
	}
	fs, err := rawList("tc filter", filters)
	if err != nil {
		return nil, err
	}
	is, err := rawList("tc filter ingress", ingress)
	if err != nil {
		return nil, err
	}
	for _, o := range append(fs, is...) {
		if !forDev(o, dev) {
			continue
		}
		if _, ok := o["options"]; !ok {
			continue // the bare header entry tc prints before each filter
		}
		f, err := normFilter(o)
		if err != nil {
			return nil, fmt.Errorf("tc filter: %w", err)
		}
		t.Filters = append(t.Filters, f)
	}
	t.sort()
	return t, nil
}

// Sorted puts the objects in the order NormalizeTC returns them (by number) and returns the tree.
func (t *NormTree) Sorted() *NormTree {
	t.sort()
	return t
}

func (t *NormTree) sort() {
	sort.Slice(t.Qdiscs, func(i, j int) bool {
		a, b := t.Qdiscs[i], t.Qdiscs[j]
		if a.Handle != b.Handle {
			return handleLess(a.Handle, b.Handle)
		}
		return a.Parent < b.Parent
	})
	sort.Slice(t.Classes, func(i, j int) bool { return handleLess(t.Classes[i].ID, t.Classes[j].ID) })
	sort.Slice(t.Filters, func(i, j int) bool {
		a, b := t.Filters[i], t.Filters[j]
		switch {
		case a.Parent != b.Parent:
			return handleLess(a.Parent, b.Parent)
		case a.Pref != b.Pref:
			return a.Pref < b.Pref
		case a.Mark != b.Mark:
			return a.Mark < b.Mark
		case a.Mask != b.Mask:
			return a.Mask < b.Mask
		}
		return a.Options < b.Options
	})
}

// handleLess orders handles by their numbers, so "2:" comes before "10:" and "1:5" before "1:24".
func handleLess(a, b string) bool {
	am, an := splitHandle(a)
	bm, bn := splitHandle(b)
	if am != bm {
		return am < bm
	}
	if an != bn {
		return an < bn
	}
	return a < b
}

func splitHandle(h string) (uint64, uint64) {
	maj, min, _ := strings.Cut(h, ":")
	a, _ := strconv.ParseUint(maj, 16, 32)
	b, _ := strconv.ParseUint(min, 16, 32)
	return a, b
}

func rawList(what string, data []byte) ([]rawObj, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var out []rawObj
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", what, err)
	}
	return out, nil
}

func forDev(o rawObj, dev string) bool {
	raw, ok := o["dev"]
	if !ok {
		return true
	}
	var d string
	return json.Unmarshal(raw, &d) != nil || d == dev
}

// field helpers: a missing key is the zero value, a key of the wrong type is an error.
func getStr(o rawObj, key string) (string, error) {
	raw, ok := o[key]
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return s, nil
}

func getNum(o rawObj, key string) (float64, error) {
	raw, ok := o[key]
	if !ok {
		return 0, nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return f, nil
}

func getInt(o rawObj, key string) (int64, error) {
	raw, ok := o[key]
	if !ok {
		return 0, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if i, err := n.Int64(); err == nil {
		return i, nil
	}
	f, err := n.Float64()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return int64(f), nil
}

func getUint(o rawObj, key string) (uint64, error) {
	raw, ok := o[key]
	if !ok {
		return 0, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	u, err := strconv.ParseUint(n.String(), 10, 64)
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil || f < 0 {
			return 0, fmt.Errorf("%s: %q is not a counter", key, n.String())
		}
		return uint64(f), nil
	}
	return u, nil
}

func getBool(o rawObj, key string) (bool, error) {
	raw, ok := o[key]
	if !ok {
		return false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}

func getObj(o rawObj, key string) (rawObj, error) {
	raw, ok := o[key]
	if !ok {
		return nil, nil
	}
	var m rawObj
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return m, nil
}

// canonical renders the given keys of an object as sorted compact JSON, "" for none.
func canonical(o rawObj, skip ...string) (string, error) {
	rest := rawObj{}
	for k, v := range o {
		rest[k] = v
	}
	for _, k := range skip {
		delete(rest, k)
	}
	if len(rest) == 0 {
		return "", nil
	}
	return canonicalValue(rest)
}

func canonicalValue(v any) (string, error) {
	b, err := json.Marshal(v) // maps are marshalled with sorted keys
	if err != nil {
		return "", err
	}
	// re-decode and encode so that nested raw objects are sorted and compact too
	var x any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&x); err != nil {
		return "", err
	}
	out, err := json.Marshal(x)
	return string(out), err
}

func normQdisc(o rawObj) (NormQdisc, error) {
	var q NormQdisc
	var err error
	if q.Kind, err = getStr(o, "kind"); err != nil {
		return q, err
	}
	h, err := getStr(o, "handle")
	if err != nil {
		return q, err
	}
	var ok bool
	if q.Handle, ok = ParseTCHandle(h); !ok {
		return q, fmt.Errorf("handle %q", h)
	}
	root, err := getBool(o, "root")
	if err != nil {
		return q, err
	}
	parent, err := getStr(o, "parent")
	if err != nil {
		return q, err
	}
	switch {
	case root:
		q.Parent = "root"
	case parent == "ffff:fff1":
		q.Parent = "ingress"
	case parent == "ffff:fff3":
		q.Parent = "clsact"
	default:
		if q.Parent, ok = ParseTCHandle(parent); !ok {
			return q, fmt.Errorf("qdisc %s: parent %q", q.Handle, parent)
		}
	}
	opts, err := getObj(o, "options")
	if err != nil {
		return q, err
	}
	switch q.Kind {
	case "htb":
		if q.HTB, err = normHTBQdisc(opts); err != nil {
			return q, fmt.Errorf("qdisc %s: %w", q.Handle, err)
		}
	case "netem":
		if q.Netem, q.Seed, err = normNetem(opts); err != nil {
			return q, fmt.Errorf("qdisc %s: %w", q.Handle, err)
		}
	default:
		if q.Options, err = canonical(opts); err != nil {
			return q, err
		}
	}
	if _, has := o["bytes"]; has {
		s, err := normStats(o)
		if err != nil {
			return q, fmt.Errorf("qdisc %s: %w", q.Handle, err)
		}
		if q.Kind == "htb" {
			if s.DirectPackets, err = getUint(opts, "direct_packets_stat"); err != nil {
				return q, err
			}
		}
		q.Stats = &s
	}
	return q, nil
}

func normHTBQdisc(opts rawObj) (*HTBSpec, error) {
	var s HTBSpec
	d, err := getStr(opts, "default")
	if err != nil {
		return nil, err
	}
	if d != "" {
		n, err := strconv.ParseUint(strings.TrimPrefix(d, "0x"), 16, 32)
		if err != nil {
			return nil, fmt.Errorf("default %q", d)
		}
		s.Default = uint32(n)
	}
	r2q, err := getInt(opts, "r2q")
	if err != nil {
		return nil, err
	}
	dq, err := getInt(opts, "direct_qlen")
	if err != nil {
		return nil, err
	}
	s.R2Q, s.DirectQlen = int(r2q), int(dq)
	return &s, nil
}

// netemKnown are the option keys normNetem consumes; anything else lands in Extra.
var netemKnown = []string{"limit", "delay", "loss-random", "loss-gemodel", "reorder", "duplicate", "corrupt", "rate", "slot", "seed", "ecn", "gap"}

func normNetem(opts rawObj) (*NetemSpec, uint64, error) {
	var n NetemSpec
	var err error
	lim, err := getInt(opts, "limit")
	if err != nil {
		return nil, 0, err
	}
	n.Limit = int(lim)
	pair := func(key, a, b string) (float64, float64, error) {
		m, err := getObj(opts, key)
		if err != nil || m == nil {
			return 0, 0, err
		}
		x, err := getNum(m, a)
		if err != nil {
			return 0, 0, err
		}
		y, err := getNum(m, b)
		return Round6(x), Round6(y), err
	}
	if m, err := getObj(opts, "delay"); err != nil {
		return nil, 0, err
	} else if m != nil {
		d, err1 := getNum(m, "delay")
		j, err2 := getNum(m, "jitter")
		c, err3 := getNum(m, "correlation")
		if err := firstErr(err1, err2, err3); err != nil {
			return nil, 0, err
		}
		n.Delay, n.Jitter, n.DelayCorr = Round6(d), Round6(j), Round6(c)
	}
	if n.Loss, n.LossCorr, err = pair("loss-random", "loss", "correlation"); err != nil {
		return nil, 0, err
	}
	if m, err := getObj(opts, "loss-gemodel"); err != nil {
		return nil, 0, err
	} else if m != nil {
		p, e1 := getNum(m, "p")
		r, e2 := getNum(m, "r")
		h, e3 := getNum(m, "1-h")
		k, e4 := getNum(m, "1-k")
		if err := firstErr(e1, e2, e3, e4); err != nil {
			return nil, 0, err
		}
		n.Gemodel = &GemodelSpec{P: Round6(p), R: Round6(r), OneMinusH: Round6(h), OneMinusK: Round6(k)}
	}
	if n.Reorder, n.ReorderCorr, err = pair("reorder", "reorder", "correlation"); err != nil {
		return nil, 0, err
	}
	if n.Duplicate, n.DuplicateCorr, err = pair("duplicate", "duplicate", "correlation"); err != nil {
		return nil, 0, err
	}
	if n.Corrupt, n.CorruptCorr, err = pair("corrupt", "corrupt", "correlation"); err != nil {
		return nil, 0, err
	}
	if m, err := getObj(opts, "rate"); err != nil {
		return nil, 0, err
	} else if m != nil {
		r, e1 := getInt(m, "rate")
		po, e2 := getInt(m, "packetoverhead")
		cs, e3 := getInt(m, "cellsize")
		co, e4 := getInt(m, "celloverhead")
		if err := firstErr(e1, e2, e3, e4); err != nil {
			return nil, 0, err
		}
		n.Rate, n.PacketOverhead, n.CellSize, n.CellOverhead = r, int(po), int(cs), int(co)
	}
	if m, err := getObj(opts, "slot"); err != nil {
		return nil, 0, err
	} else if m != nil {
		lo, e1 := getNum(m, "min-delay")
		hi, e2 := getNum(m, "max-delay")
		pk, e3 := getInt(m, "packets")
		by, e4 := getInt(m, "bytes")
		if err := firstErr(e1, e2, e3, e4); err != nil {
			return nil, 0, err
		}
		ex, err := canonical(m, "min-delay", "max-delay", "packets", "bytes")
		if err != nil {
			return nil, 0, err
		}
		n.Slot = &SlotSpec{MinDelay: Round6(lo), MaxDelay: Round6(hi), Packets: int(pk), Bytes: int(by), Extra: ex}
	}
	if n.ECN, err = getBool(opts, "ecn"); err != nil {
		return nil, 0, err
	}
	gap, err := getInt(opts, "gap")
	if err != nil {
		return nil, 0, err
	}
	n.Gap = int(gap)
	seed, err := getUint(opts, "seed")
	if err != nil {
		return nil, 0, err
	}
	if n.Extra, err = canonical(opts, netemKnown...); err != nil {
		return nil, 0, err
	}
	return &n, seed, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func normStats(o rawObj) (NormStats, error) {
	var s NormStats
	var errs []error
	u := func(dst *uint64, key string) {
		v, err := getUint(o, key)
		*dst = v
		errs = append(errs, err)
	}
	u(&s.Bytes, "bytes")
	u(&s.Packets, "packets")
	u(&s.Drops, "drops")
	u(&s.Overlimits, "overlimits")
	u(&s.Requeues, "requeues")
	u(&s.Backlog, "backlog")
	u(&s.Qlen, "qlen")
	u(&s.Lended, "lended")
	u(&s.Borrowed, "borrowed")
	u(&s.Giants, "giants")
	var err1, err2 error
	s.Tokens, err1 = getInt(o, "tokens")
	s.Ctokens, err2 = getInt(o, "ctokens")
	errs = append(errs, err1, err2)
	return s, firstErr(errs...)
}

func normClass(o rawObj) (NormClass, error) {
	var c NormClass
	var err error
	if c.Kind, err = getStr(o, "class"); err != nil {
		return c, err
	}
	h, err := getStr(o, "handle")
	if err != nil {
		return c, err
	}
	var ok bool
	if c.ID, ok = ParseTCHandle(h); !ok || strings.HasSuffix(c.ID, ":") {
		return c, fmt.Errorf("class id %q", h)
	}
	root, err := getBool(o, "root")
	if err != nil {
		return c, err
	}
	parent, err := getStr(o, "parent")
	if err != nil {
		return c, err
	}
	switch {
	case root:
		// a class directly below the root qdisc: the qdisc has the class's major number
		c.Parent = c.ID[:strings.IndexByte(c.ID, ':')+1]
	default:
		if c.Parent, ok = ParseTCHandle(parent); !ok {
			return c, fmt.Errorf("class %s: parent %q", c.ID, parent)
		}
	}
	if leaf, err := getStr(o, "leaf"); err != nil {
		return c, err
	} else if leaf != "" {
		if c.Leaf, ok = ParseTCHandle(leaf); !ok {
			return c, fmt.Errorf("class %s: leaf %q", c.ID, leaf)
		}
	}
	rate, e1 := getInt(o, "rate")
	ceil, e2 := getInt(o, "ceil")
	prio, e3 := getInt(o, "prio")
	burst, e4 := getInt(o, "burst")
	cburst, e5 := getInt(o, "cburst")
	if err := firstErr(e1, e2, e3, e4, e5); err != nil {
		return c, fmt.Errorf("class %s: %w", c.ID, err)
	}
	c.Rate, c.Ceil, c.Prio, c.Burst, c.Cburst = rate, ceil, int(prio), int(burst), int(cburst)
	if c.Extra, err = canonical(o, "class", "handle", "root", "parent", "leaf", "rate", "ceil", "prio", "burst", "cburst", "stats", "dev"); err != nil {
		return c, err
	}
	if st, err := getObj(o, "stats"); err != nil {
		return c, err
	} else if st != nil {
		s, err := normStats(st)
		if err != nil {
			return c, fmt.Errorf("class %s: %w", c.ID, err)
		}
		c.Stats = &s
	}
	return c, nil
}

func normFilter(o rawObj) (NormFilter, error) {
	var f NormFilter
	var err error
	if f.Kind, err = getStr(o, "kind"); err != nil {
		return f, err
	}
	if f.Protocol, err = getStr(o, "protocol"); err != nil {
		return f, err
	}
	pref, err := getInt(o, "pref")
	if err != nil {
		return f, err
	}
	f.Pref = int(pref)
	parent, err := getStr(o, "parent")
	if err != nil {
		return f, err
	}
	var ok bool
	switch parent {
	case "":
		return f, fmt.Errorf("filter without a parent")
	case "ffff:fff1", "ffff:":
		f.Parent = "ingress"
	case "ffff:fff3":
		f.Parent = "clsact"
	default:
		if f.Parent, ok = ParseTCHandle(parent); !ok {
			return f, fmt.Errorf("filter parent %q", parent)
		}
	}
	opts, err := getObj(o, "options")
	if err != nil {
		return f, err
	}
	if f.Kind == "flower" {
		return normFlower(f, opts)
	}
	if f.Kind != "fw" {
		f.Options, err = canonical(opts)
		return f, err
	}
	// iproute2 6.19 prints the selector as "fw":{"mark","mask"}; older versions as "handle" with
	// the mask appended after a slash or not at all (all bits).
	f.Mask = math.MaxUint32
	if m, err := getObj(opts, "fw"); err != nil {
		return f, err
	} else if m != nil {
		mark, err := getStr(m, "mark")
		if err != nil {
			return f, err
		}
		mask, err := getStr(m, "mask")
		if err != nil {
			return f, err
		}
		if f.Mark, err = hex32(mark); err != nil {
			return f, err
		}
		if mask != "" {
			if f.Mask, err = hex32(mask); err != nil {
				return f, err
			}
		}
	} else if h, err := getStr(opts, "handle"); err != nil {
		return f, err
	} else if h != "" {
		mark, mask, _ := strings.Cut(h, "/")
		if f.Mark, err = hex32(mark); err != nil {
			return f, err
		}
		if mask != "" {
			if f.Mask, err = hex32(mask); err != nil {
				return f, err
			}
		}
	}
	flow, err := getStr(opts, "classid")
	if err != nil {
		return f, err
	}
	if flow == "" {
		if flow, err = getStr(opts, "flowid"); err != nil {
			return f, err
		}
	}
	if flow != "" {
		if f.Flowid, ok = ParseTCHandle(flow); !ok {
			return f, fmt.Errorf("filter flowid %q", flow)
		}
	}
	f.Options, err = canonical(opts, "fw", "handle", "classid", "flowid")
	return f, err
}

// normFlower reads the options of a flower filter: handle, the keys, the class it selects or the
// redirect action. A key or an action that is not the one Chaos Gateway writes lands in Extra.
func normFlower(f NormFilter, opts rawObj) (NormFilter, error) {
	spec := &FlowerSpec{}
	h, err := getInt(opts, "handle")
	if err != nil {
		return f, err
	}
	spec.Handle = int(h)
	flow, err := getStr(opts, "classid")
	if err != nil {
		return f, err
	}
	if flow != "" {
		var ok bool
		if f.Flowid, ok = ParseTCHandle(flow); !ok {
			return f, fmt.Errorf("filter flowid %q", flow)
		}
	}
	extra := rawObj{}
	if keys, err := getObj(opts, "keys"); err != nil {
		return f, err
	} else {
		for k, v := range keys {
			switch k {
			case "eth_type":
				var s string
				if json.Unmarshal(v, &s) != nil || s != "ipv4" {
					extra["key:"+k] = v
				}
			case "ip_proto":
				if err := json.Unmarshal(v, &spec.IPProto); err != nil {
					return f, fmt.Errorf("flower ip_proto: %w", err)
				}
			case "src_ip":
				if err := json.Unmarshal(v, &spec.SrcIP); err != nil {
					return f, fmt.Errorf("flower src_ip: %w", err)
				}
			case "src_port":
				if err := json.Unmarshal(v, &spec.SrcPort); err != nil {
					return f, fmt.Errorf("flower src_port: %w", err)
				}
			default:
				extra["key:"+k] = v
			}
		}
	}
	if raw, ok := opts["actions"]; ok {
		var acts []rawObj
		if err := json.Unmarshal(raw, &acts); err != nil {
			return f, fmt.Errorf("flower actions: %w", err)
		}
		for i, a := range acts {
			kind, _ := getStr(a, "kind")
			act, _ := getStr(a, "mirred_action")
			dir, _ := getStr(a, "direction")
			to, _ := getStr(a, "to_dev")
			if i == 0 && kind == "mirred" && act == "redirect" && dir == "egress" && to != "" {
				spec.Redirect = to
				if st, err := getObj(a, "stats"); err == nil && st != nil {
					s, err := normStats(st)
					if err != nil {
						return f, fmt.Errorf("flower action stats: %w", err)
					}
					f.Stats = &s
				}
				continue
			}
			b, _ := canonicalValue(a)
			q, _ := json.Marshal(b)
			extra[fmt.Sprintf("action:%d", i)] = q
		}
	}
	if len(extra) > 0 {
		spec.Extra, err = canonical(extra)
		if err != nil {
			return f, err
		}
	}
	f.Flower = spec
	f.Options, err = canonical(opts, "handle", "classid", "keys", "actions", "not_in_hw", "in_hw", "in_hw_count")
	return f, err
}

func hex32(s string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a 32-bit hex number", s)
	}
	return uint32(n), nil
}

// Spec returns a copy of the tree with everything that is not configuration removed: counters,
// seeds, the burst sizes the kernel derives from the rate, and the HTB root's r2q and direct queue
// length (the kernel's defaults, the latter taken from the interface's queue length). Two trees are the same
// configuration exactly when their Specs are deeply equal (and their Lines are).
func (t *NormTree) Spec() *NormTree {
	if t == nil {
		return nil
	}
	c := &NormTree{Dev: t.Dev, Qdiscs: make([]NormQdisc, len(t.Qdiscs)), Classes: make([]NormClass, len(t.Classes)), Filters: append([]NormFilter{}, t.Filters...)}
	for i, q := range t.Qdiscs {
		q.Seed, q.Stats = 0, nil
		if q.HTB != nil {
			// what an HTB qdisc created with only a default class takes from the kernel and the interface
			h := *q.HTB
			h.R2Q, h.DirectQlen = 0, 0
			q.HTB = &h
		}
		c.Qdiscs[i] = q
	}
	for i, cl := range t.Classes {
		cl.Burst, cl.Cburst, cl.Stats = 0, 0, nil
		c.Classes[i] = cl
	}
	for i := range c.Filters {
		c.Filters[i].Stats = nil
	}
	return c
}

// Lines renders the configuration, one line per object, in a stable order. It is the text the
// verification prints for a difference and the form golden files of the normalizer can use.
func (t *NormTree) Lines() []string {
	var out []string
	for _, q := range t.Qdiscs {
		out = append(out, q.Line())
	}
	for _, c := range t.Classes {
		out = append(out, c.Line())
	}
	for _, f := range t.Filters {
		out = append(out, f.Line())
	}
	return out
}

// Key identifies the object among those of its kind on the interface.
func (q NormQdisc) Key() string { return "qdisc " + q.Handle + " parent " + q.Parent }

// Key identifies the class.
func (c NormClass) Key() string { return "class " + c.ID }

// Key identifies the filter: its parent, priority and selector.
func (f NormFilter) Key() string {
	if f.Flower != nil {
		return fmt.Sprintf("filter parent %s pref %d flower handle %d", f.Parent, f.Pref, f.Flower.Handle)
	}
	return fmt.Sprintf("filter parent %s pref %d %s %#x/%#x%s", f.Parent, f.Pref, f.Kind, f.Mark, f.Mask, optSuffix(f.Options))
}

func optSuffix(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}

// Line is the qdisc's configuration as text.
func (q NormQdisc) Line() string {
	s := q.Key() + " " + q.Kind
	switch {
	case q.HTB != nil:
		s += fmt.Sprintf(" default=%#x", q.HTB.Default)
		if q.HTB.R2Q != 0 || q.HTB.DirectQlen != 0 {
			s += fmt.Sprintf(" r2q=%d direct_qlen=%d", q.HTB.R2Q, q.HTB.DirectQlen)
		}
	case q.Netem != nil:
		s += " " + q.Netem.String()
	case q.Options != "":
		s += " " + q.Options
	}
	return s
}

// Line is the class's configuration as text.
func (c NormClass) Line() string {
	s := fmt.Sprintf("%s parent %s %s rate=%d ceil=%d prio=%d", c.Key(), c.Parent, c.Kind, c.Rate, c.Ceil, c.Prio)
	if c.Leaf != "" {
		s += " leaf=" + c.Leaf
	}
	if c.Extra != "" {
		s += " " + c.Extra
	}
	return s
}

// Line is the filter's configuration as text.
func (f NormFilter) Line() string {
	s := f.Key()
	if f.Flowid != "" {
		s += " flowid=" + f.Flowid
	}
	if fl := f.Flower; fl != nil {
		s += fmt.Sprintf(" %s src=%s:%d", fl.IPProto, fl.SrcIP, fl.SrcPort)
		if fl.Redirect != "" {
			s += " redirect=" + fl.Redirect
		}
		if fl.Extra != "" {
			s += " " + fl.Extra
		}
		s += optSuffix(f.Options)
	}
	return s
}

// String is the netem configuration as text, every attribute spelled out.
func (n NetemSpec) String() string {
	g := func(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }
	s := fmt.Sprintf("limit=%d delay=%s jitter=%s delay_corr=%s", n.Limit, g(n.Delay), g(n.Jitter), g(n.DelayCorr))
	if n.Gemodel != nil {
		s += fmt.Sprintf(" gemodel=%s/%s/%s/%s", g(n.Gemodel.P), g(n.Gemodel.R), g(n.Gemodel.OneMinusH), g(n.Gemodel.OneMinusK))
	} else {
		s += fmt.Sprintf(" loss=%s loss_corr=%s", g(n.Loss), g(n.LossCorr))
	}
	s += fmt.Sprintf(" reorder=%s/%s gap=%d duplicate=%s/%s corrupt=%s/%s rate=%d",
		g(n.Reorder), g(n.ReorderCorr), n.Gap, g(n.Duplicate), g(n.DuplicateCorr), g(n.Corrupt), g(n.CorruptCorr), n.Rate)
	if n.PacketOverhead != 0 || n.CellSize != 0 || n.CellOverhead != 0 {
		s += fmt.Sprintf(" overhead=%d/%d/%d", n.PacketOverhead, n.CellSize, n.CellOverhead)
	}
	if n.Slot != nil {
		s += fmt.Sprintf(" slot=%s/%s/%d/%d%s", g(n.Slot.MinDelay), g(n.Slot.MaxDelay), n.Slot.Packets, n.Slot.Bytes, optSuffix(n.Slot.Extra))
	}
	if n.ECN {
		s += " ecn"
	}
	return s + optSuffix(n.Extra)
}

// Subtree returns the part of the tree below the root qdisc with the given handle ("1:"): the
// qdisc itself, every class and qdisc under it, and the filters that hang off it. What is left is
// the interface's own tree of Chaos Gateway; the operating system's root qdisc and an ingress
// qdisc are not part of it. The result shares nothing with t.
func (t *NormTree) Subtree(rootHandle string) *NormTree {
	major, _, _ := strings.Cut(rootHandle, ":")
	own := func(h string) bool { m, _, _ := strings.Cut(h, ":"); return m == major }
	out := &NormTree{Dev: t.Dev, Qdiscs: []NormQdisc{}, Classes: []NormClass{}, Filters: []NormFilter{}}
	for _, q := range t.Qdiscs {
		if q.Handle == rootHandle && q.Parent == "root" || q.Parent != "root" && own(q.Parent) {
			out.Qdiscs = append(out.Qdiscs, q)
		}
	}
	for _, c := range t.Classes {
		if own(c.ID) {
			out.Classes = append(out.Classes, c)
		}
	}
	for _, f := range t.Filters {
		if own(f.Parent) {
			out.Filters = append(out.Filters, f)
		}
	}
	return out
}

// Ingress returns the part of the tree that hangs off the ingress qdisc: the qdisc and its filters.
// Empty when the interface has none. The result shares nothing with t.
func (t *NormTree) Ingress() *NormTree {
	out := &NormTree{Dev: t.Dev, Qdiscs: []NormQdisc{}, Classes: []NormClass{}, Filters: []NormFilter{}}
	for _, q := range t.Qdiscs {
		if q.Parent == "ingress" {
			out.Qdiscs = append(out.Qdiscs, q)
		}
	}
	for _, f := range t.Filters {
		if f.Parent == "ingress" {
			out.Filters = append(out.Filters, f)
		}
	}
	return out
}

// TCDelta is one difference between a wanted and an observed tree.
type TCDelta struct {
	// Kind is "missing", "unexpected" or "different".
	Kind string
	// Key identifies the object (NormQdisc.Key, NormClass.Key, NormFilter.Key).
	Key string
	// Want and Have are the configuration as text (Line); empty on the side that has none.
	Want, Have string
	// Class is the class the object belongs to: the class itself, a qdisc below it, a filter that
	// selects it ("1:24"); empty for the root qdisc and for objects that belong to no class.
	Class string
}

func (d TCDelta) String() string {
	switch d.Kind {
	case "missing":
		return "missing: " + d.Want
	case "unexpected":
		return "unexpected: " + d.Have
	}
	return fmt.Sprintf("different: want %s, have %s", d.Want, d.Have)
}

// CompareTC compares a wanted and an observed tree, configuration only (Spec), and returns the
// differences sorted by key.
func CompareTC(want, have *NormTree) []TCDelta {
	w, h := index(want.Spec()), index(have.Spec())
	var out []TCDelta
	for k, wl := range w {
		hl, ok := h[k]
		switch {
		case !ok:
			out = append(out, TCDelta{Kind: "missing", Key: k, Want: wl.line, Class: wl.class})
		case hl.line != wl.line:
			out = append(out, TCDelta{Kind: "different", Key: k, Want: wl.line, Have: hl.line, Class: wl.class})
		}
	}
	for k, hl := range h {
		if _, ok := w[k]; !ok {
			out = append(out, TCDelta{Kind: "unexpected", Key: k, Have: hl.line, Class: hl.class})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// DiffTC lists the differences between a wanted and an observed tree, comparing configuration only
// (Spec). Each entry names the object and says what is missing, unexpected or different; the
// result is sorted and empty when the two are the same.
func DiffTC(want, have *NormTree) []string {
	var out []string
	for _, d := range CompareTC(want, have) {
		out = append(out, d.String())
	}
	sort.Strings(out)
	return out
}

type indexed struct{ line, class string }

func index(t *NormTree) map[string]indexed {
	m := map[string]indexed{}
	for _, q := range t.Qdiscs {
		c := ""
		if q.Parent != "root" && strings.Contains(q.Parent, ":") && !strings.HasPrefix(q.Parent, "ffff") {
			c = q.Parent
		}
		m[q.Key()] = indexed{q.Line(), c}
	}
	for _, c := range t.Classes {
		m[c.Key()] = indexed{c.Line(), c.ID}
	}
	for _, f := range t.Filters {
		m[f.Key()] = indexed{f.Line(), f.Flowid}
	}
	return m
}
