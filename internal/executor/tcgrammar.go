package executor

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The tc operation is closed in two more ways than the token allowlist of TCEntry.validate (M8b):
//
//   - Chaos Gateway's own handles. Everything the compiler builds lives in one tree: the root qdisc
//     `1:`, its classes `1:<minor>`, a leaf qdisc `<minor>:` below each class and `fw` filters on `1:`.
//     An entry that names any other handle (the operating system's root qdisc on the uplink, `8001:`
//     of an `mq`, the handle zero of a default qdisc) is refused, so a scope that is correct for the
//     interface (assigned, maybe the uplink) still cannot reach what the host put there. An ingress
//     or clsact qdisc and the filters of its `ffff:` stay allowed as before: the tunnel faults of M10
//     use them.
//   - the arguments of the kinds the compiler emits (netem, htb, the fw filter) are a grammar, not
//     a list of tokens: a keyword at most once, its values of the right shape and number. Other
//     kinds keep the token rules.
//
// The grammars accept what the compiler writes (every netem attribute, `htb rate R quantum Q`,
// `protocol ip prio 1 fw flowid C`) and what the executor's older users send, and nothing that
// names a file, a device or another object.

const (
	// ownRootHandle is the handle of the root qdisc, ownMajor the major number of the whole tree.
	ownRootHandle = "1:"
	ownMajor      = "1"
	ingressHandle = "ffff:"
)

var (
	hexNum   = regexp.MustCompile(`^[0-9a-fA-F]{1,4}$`)
	uintNum  = regexp.MustCompile(`^[0-9]{1,10}$`)
	durTok   = regexp.MustCompile(`^[0-9]{1,9}(\.[0-9]{1,6})?(s|sec|ms|msec|us|usec)$`)
	pctTok   = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,9})?%$`)
	rateTok  = regexp.MustCompile(`(?i)^[0-9]{1,12}(\.[0-9]{1,3})?(bit|kbit|mbit|gbit|tbit|bps|kbps|mbps|gbps|kibit|mibit|gibit|tibit)$`)
	sizeTok  = regexp.MustCompile(`(?i)^[0-9]{1,9}(b|k|kb|kib|m|mb|mib)?$`)
	protoTok = map[string]bool{"ip": true, "ipv6": true, "all": true}
)

// ownMinor returns the minor of a class id of the own tree ("1:24" -> "24").
func ownMinor(classID string) (string, bool) {
	maj, min, ok := strings.Cut(classID, ":")
	// minor 0 is the qdisc itself ("1:0" is "1:")
	if !ok || maj != ownMajor || !hexNum.MatchString(min) || sameHex(min, "0") {
		return "", false
	}
	return min, true
}

// sameHex compares two hex numbers by value ("0024" is "24").
func sameHex(a, b string) bool {
	x, err1 := strconv.ParseUint(a, 16, 32)
	y, err2 := strconv.ParseUint(b, 16, 32)
	return err1 == nil && err2 == nil && x == y
}

// checkOwnHandles enforces the own handle space on one entry that is not about ingress or clsact
// (those are checked before). The checks are static: they depend on the entry alone.
func (e TCEntry) checkOwnHandles() error {
	switch e.Object {
	case "qdisc":
		switch e.Parent {
		case "root":
			if e.Handle != ownRootHandle {
				return fmt.Errorf("the root qdisc is Chaos Gateway's own only with the handle %s, not %q", ownRootHandle, e.Handle)
			}
		default:
			min, ok := ownMinor(e.Parent)
			if !ok {
				return fmt.Errorf("a qdisc hangs below root, ingress, clsact or a class %s<minor>, not %q", ownRootHandle, e.Parent)
			}
			// the leaf of a class has the class's minor as its handle, which makes it unique on the
			// interface and never the root's
			if h, ok := strings.CutSuffix(e.Handle, ":"); !ok || !hexNum.MatchString(h) || !sameHex(h, min) {
				return fmt.Errorf("the qdisc below class %s has the handle %s:, not %q", e.Parent, min, e.Handle)
			}
		}
	case "class":
		if _, ok := ownMinor(e.ClassID); !ok {
			return fmt.Errorf("a class id is %s<minor>, not %q", ownRootHandle, e.ClassID)
		}
		if e.Parent != "" && e.Parent != ownRootHandle {
			if _, ok := ownMinor(e.Parent); !ok {
				return fmt.Errorf("a class hangs below %s or another class of the tree, not %q", ownRootHandle, e.Parent)
			}
		}
		if e.Parent == "" && e.Action != "delete" {
			return errors.New("a class needs a parent")
		}
	case "filter":
		if e.Parent != ownRootHandle && e.Parent != ingressHandle {
			return fmt.Errorf("a filter hangs below %s (or the ingress %s), not %q", ownRootHandle, ingressHandle, e.Parent)
		}
	}
	return nil
}

// checkTCArgs applies the grammar of the kind the entry sets up. Kinds without a grammar pass.
func (e TCEntry) checkTCArgs() error {
	if e.Action == "delete" {
		return e.checkDeleteArgs()
	}
	switch e.Object {
	case "qdisc":
		switch e.Args[0] {
		case "netem":
			return checkNetem(e.Args[1:])
		case "htb":
			return checkHTBQdisc(e.Args[1:])
		}
	case "class":
		if e.Args[0] == "htb" {
			return checkHTBClass(e.Args[1:])
		}
	case "filter":
		if hasToken(e.Args, "fw") {
			return e.checkFwFilter()
		}
		if hasToken(e.Args, "flower") {
			return e.checkFlowerFilter()
		}
		return checkFlowids(e.Args)
	}
	return nil
}

// checkFlowids requires that every `flowid` of a filter of another kind names a class of the own
// tree (the fw grammar checks its own). The nightly fuzzer found `flowid 0` passing on a u32 filter.
func checkFlowids(args []string) error {
	for i, a := range args {
		if a != "flowid" && a != "classid" {
			continue
		}
		if i+1 == len(args) {
			return fmt.Errorf("%s needs a class", a)
		}
		if _, ok := ownMinor(args[i+1]); !ok {
			return fmt.Errorf("%s %q is not a class of the tree", a, args[i+1])
		}
	}
	return nil
}

func hasToken(args []string, tok string) bool {
	for _, a := range args {
		if a == tok {
			return true
		}
	}
	return false
}

// checkDeleteArgs: a deletion names its object by the fields of the entry and carries no
// parameters, except that a filter is found by its protocol and priority.
func (e TCEntry) checkDeleteArgs() error {
	if e.Object != "filter" {
		if len(e.Args) != 0 {
			return fmt.Errorf("a %s deletion takes no arguments", e.Object)
		}
		return nil
	}
	if e.Handle == "" {
		return errors.New("a filter deletion names the filter by its handle")
	}
	a := e.Args
	if len(a) != 5 || a[0] != "protocol" || !protoTok[a[1]] || a[2] != "prio" || !uintNum.MatchString(a[3]) || (a[4] != "fw" && a[4] != "u32" && a[4] != "flower") {
		return errors.New("a filter deletion has the arguments protocol P prio N fw|u32|flower")
	}
	if a[4] == "flower" && !flowerHandle.MatchString(e.Handle) {
		return errors.New("a flower filter is named by a decimal handle")
	}
	return nil
}

// flowerHandle is the handle of a flower filter: the number that names it among the filters of its
// priority. The tunnel faults use the fault id.
var flowerHandle = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)

// checkFlowerFilter is the grammar of the two flower filters of the tunnel faults (M10, plan §2.2.1):
//
//	on the ingress qdisc of an interface (parent ffff:), the packets of the outer UDP of one peer are
//	sent to the IFB:      protocol ip prio N flower ip_proto udp src_ip A src_port P action mirred egress redirect dev ifb-cgw
//	on the root of the IFB (parent 1:), the same selector chooses the class of the fault:
//	                      protocol ip prio N flower ip_proto udp src_ip A src_port P flowid 1:M
//
// and nothing else: a flower filter is not a way to redirect traffic to another interface or to name a
// class outside the own tree.
func (e TCEntry) checkFlowerFilter() error {
	if !flowerHandle.MatchString(e.Handle) {
		return errors.New("a flower filter is named by a decimal handle")
	}
	a := e.Args
	if len(a) < 13 || a[0] != "protocol" || a[1] != "ip" || a[2] != "prio" || !uintNum.MatchString(a[3]) || a[4] != "flower" ||
		a[5] != "ip_proto" || a[6] != "udp" || a[7] != "src_ip" || a[9] != "src_port" {
		return errors.New("a flower filter has the arguments protocol ip prio N flower ip_proto udp src_ip A src_port P and an action or a flowid")
	}
	if ip, err := netip.ParseAddr(a[8]); err != nil || !ip.Is4() || ip.Zone() != "" {
		return fmt.Errorf("flower src_ip %q is not an IPv4 address", a[8])
	}
	if p, err := strconv.Atoi(a[10]); err != nil || p < 1 || p > 65535 || !uintNum.MatchString(a[10]) {
		return fmt.Errorf("flower src_port %q is not a port", a[10])
	}
	rest := a[11:]
	switch e.Parent {
	case ownRootHandle:
		if len(rest) != 2 || rest[0] != "flowid" {
			return errors.New("a flower filter below the root takes flowid C after the selector")
		}
		if _, ok := ownMinor(rest[1]); !ok {
			return fmt.Errorf("flowid %q is not a class of the tree", rest[1])
		}
	case ingressHandle:
		want := []string{"action", "mirred", "egress", "redirect", "dev", IFBName}
		if len(rest) != len(want) {
			return fmt.Errorf("a flower filter on the ingress qdisc redirects to %s: action mirred egress redirect dev %s", IFBName, IFBName)
		}
		for i := range want {
			if rest[i] != want[i] {
				return fmt.Errorf("a flower filter on the ingress qdisc redirects to %s only, not %q", IFBName, rest[i])
			}
		}
	default:
		return fmt.Errorf("a flower filter hangs below %s or the ingress %s", ownRootHandle, ingressHandle)
	}
	return nil
}

// checkFwFilter: protocol P prio N fw [flowid C]; the class is one of the own tree.
func (e TCEntry) checkFwFilter() error {
	a := e.Args
	if len(a) < 5 || a[0] != "protocol" || !protoTok[a[1]] || a[2] != "prio" || !uintNum.MatchString(a[3]) || a[4] != "fw" {
		return errors.New("a fw filter has the arguments protocol P prio N fw [flowid C]")
	}
	rest := a[5:]
	switch len(rest) {
	case 0:
	case 2:
		if rest[0] != "flowid" {
			return fmt.Errorf("unexpected %q in a fw filter", rest[0])
		}
		if _, ok := ownMinor(rest[1]); !ok {
			return fmt.Errorf("flowid %q is not a class of the tree", rest[1])
		}
	default:
		return errors.New("a fw filter takes at most flowid C after fw")
	}
	if e.Handle == "" {
		return errors.New("a fw filter needs a handle: the mark it selects")
	}
	return nil
}

// kw describes one keyword of a grammar: it takes between min and max values, each of the shape at
// its position (the last shape repeats). With words, the first value is one of them and the rest
// are at most words[first] values.
type kw struct {
	min, max int
	shapes   []*regexp.Regexp
	words    map[string]int
	// bare is the word that stands for a first value without one (`loss 1%` is `loss random 1%`)
	bare string
}

// walk checks args against the keywords of a grammar: a keyword at most once, with its values.
func walk(what string, args []string, g map[string]kw) error {
	seen := map[string]bool{}
	for i := 0; i < len(args); {
		name := args[i]
		k, ok := g[name]
		if !ok {
			return fmt.Errorf("%s: unknown or misplaced %q", what, name)
		}
		if seen[name] {
			return fmt.Errorf("%s: %s given twice", what, name)
		}
		seen[name] = true
		i++
		var vals []string
		for i < len(args) && len(vals) < k.max {
			if _, isKw := g[args[i]]; isKw && len(vals) >= k.min {
				break
			}
			vals = append(vals, args[i])
			i++
		}
		if len(vals) < k.min {
			return fmt.Errorf("%s: %s needs at least %d value(s)", what, name, k.min)
		}
		if k.words != nil {
			if k.bare != "" && pctTok.MatchString(vals[0]) {
				vals = append([]string{k.bare}, vals...)
			}
			limit, ok := k.words[vals[0]]
			if !ok {
				return fmt.Errorf("%s: %s takes one of %s, not %q", what, name, wordList(k.words), vals[0])
			}
			if len(vals) == 1 && limit > 0 {
				return fmt.Errorf("%s: %s %s needs a value", what, name, vals[0])
			}
			if len(vals)-1 > limit {
				return fmt.Errorf("%s: %s %s takes at most %d value(s)", what, name, vals[0], limit)
			}
			vals = vals[1:]
		}
		for j, v := range vals {
			shape := k.shapes[min(j, len(k.shapes)-1)]
			if !shape.MatchString(v) {
				return fmt.Errorf("%s: invalid value %q for %s", what, v, name)
			}
			if shape == pctTok {
				if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err != nil || f > 100 {
					return fmt.Errorf("%s: %q is more than 100%%", what, v)
				}
			}
		}
	}
	return nil
}

func wordList(m map[string]int) string {
	var w []string
	for k := range m {
		w = append(w, k)
	}
	sort.Strings(w)
	return strings.Join(w, ", ")
}

// maxQueueLimit bounds a netem queue: far above anything the compiler computes, well below what
// would let one qdisc take the host's memory in packets of an unchecked length.
const maxQueueLimit = 10_000_000

func checkNetem(args []string) error {
	if len(args) == 0 {
		return errors.New("netem needs parameters")
	}
	if err := walk("netem", args, map[string]kw{
		"limit":        {min: 1, max: 1, shapes: []*regexp.Regexp{uintNum}},
		"delay":        {min: 1, max: 3, shapes: []*regexp.Regexp{durTok, durTok, pctTok}},
		"distribution": {min: 1, max: 1, words: map[string]int{"normal": 0, "pareto": 0, "paretonormal": 0}},
		"loss":         {min: 1, max: 5, words: map[string]int{"random": 2, "gemodel": 4}, bare: "random", shapes: []*regexp.Regexp{pctTok}},
		"reorder":      {min: 1, max: 2, shapes: []*regexp.Regexp{pctTok}},
		"gap":          {min: 1, max: 1, shapes: []*regexp.Regexp{uintNum}},
		"duplicate":    {min: 1, max: 2, shapes: []*regexp.Regexp{pctTok}},
		"corrupt":      {min: 1, max: 2, shapes: []*regexp.Regexp{pctTok}},
		"rate":         {min: 1, max: 4, shapes: []*regexp.Regexp{rateTok, uintNum}},
		"seed":         {min: 1, max: 1, shapes: []*regexp.Regexp{regexp.MustCompile(`^[0-9]{1,20}$`)}},
		"ecn":          {min: 0, max: 0},
	}); err != nil {
		return err
	}
	for i, a := range args {
		if a == "limit" {
			if n, _ := strconv.ParseUint(args[i+1], 10, 64); n > maxQueueLimit {
				return fmt.Errorf("netem: limit %d exceeds %d packets", n, maxQueueLimit)
			}
		}
	}
	return nil
}

func checkHTBQdisc(args []string) error {
	return walk("htb", args, map[string]kw{
		"default":     {min: 1, max: 1, shapes: []*regexp.Regexp{hexNum}},
		"r2q":         {min: 1, max: 1, shapes: []*regexp.Regexp{uintNum}},
		"direct_qlen": {min: 1, max: 1, shapes: []*regexp.Regexp{uintNum}},
	})
}

func checkHTBClass(args []string) error {
	if !hasToken(args, "rate") {
		return errors.New("an htb class needs a rate")
	}
	return walk("htb class", args, map[string]kw{
		"rate":    {min: 1, max: 1, shapes: []*regexp.Regexp{rateTok}},
		"ceil":    {min: 1, max: 1, shapes: []*regexp.Regexp{rateTok}},
		"burst":   {min: 1, max: 1, shapes: []*regexp.Regexp{sizeTok}},
		"cburst":  {min: 1, max: 1, shapes: []*regexp.Regexp{sizeTok}},
		"prio":    {min: 1, max: 1, shapes: []*regexp.Regexp{regexp.MustCompile(`^[0-7]$`)}},
		"quantum": {min: 1, max: 1, shapes: []*regexp.Regexp{uintNum}},
	})
}
