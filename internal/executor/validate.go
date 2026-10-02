package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// What Chaos Gateway owns. Anything outside is out of scope for the executor (plan §2.16).
const (
	// NftFamily and NftTable name the only nftables table operations may touch.
	NftFamily = "inet"
	NftTable  = "chaosgw"
	// ProtoTag marks the routes and rules the executor writes (`proto`/`protocol` of iproute2).
	// Rules are only ever deleted together with this tag, so foreign rules are out of reach.
	ProtoTag = 201
	// OwnTableFirst..OwnTableLast are the routing tables Chaos Gateway uses (plan §2.2.2: 100 and the
	// PMTU mirror, 102 service, ...). The domain validation reserves the same range.
	OwnTableFirst = 100
	OwnTableLast  = 110
	// DockerUserChain is the one foreign chain with an own operation; the comment marks our rules.
	DockerUserChain   = "DOCKER-USER"
	DockerUserComment = "chaosgw"
)

const (
	maxEntries  = 4096
	maxDevs     = 256
	maxTCArgs   = 64
	maxNftItems = 100000
)

var (
	devName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,14}$`)
	setName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	tcHandl = regexp.MustCompile(`^[0-9a-fA-F]{1,4}:[0-9a-fA-F]{0,4}$`)
	tcToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:%/-]{0,63}$`)
	readTbl = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
	// devices that can never be assigned: loopback and Docker's own
	foreignDev = regexp.MustCompile(`^(lo|docker.*|br-[0-9a-f]{12}|veth[0-9a-f]{7})$`)
)

// tc keywords that must not appear among the arguments: they would override the fields that
// were validated separately, or load code or files.
var tcForbidden = map[string]bool{
	"parent": true, "handle": true, "classid": true, "block": true, "ingress_block": true, "egress_block": true,
	"bpf": true, "ebpf": true, "cbpf": true, "obj": true, "object-file": true, "object-pinned": true,
	"pinned": true, "bytecode": true, "bytecode-file": true, "exec": true, "verbose": true,
}

// qdisc and class kinds the executor sets up
var tcKinds = map[string]bool{
	"netem": true, "htb": true, "tbf": true, "fq_codel": true, "prio": true, "pfifo": true,
	"bfifo": true, "sfq": true, "pfifo_fast": true, "red": true, "cake": true, "fq": true,
}

func checkDev(name string) error {
	if !devName.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid interface name %q", name)
	}
	return nil
}

func checkDevs(devs []string, allowEmpty bool) error {
	if len(devs) == 0 && !allowEmpty {
		return errors.New("no interfaces given")
	}
	if len(devs) > maxDevs {
		return fmt.Errorf("%d interfaces exceed the limit of %d", len(devs), maxDevs)
	}
	for _, d := range devs {
		if err := checkDev(d); err != nil {
			return err
		}
	}
	return nil
}

func oneOf(what, v string, allowed ...string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%s %q is not one of %s", what, v, strings.Join(allowed, ", "))
}

func (o NftApply) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	return CheckNftRuleset(o.Ruleset)
}

// nft object kinds an apply may carry, and the commands that may wrap them.
var (
	nftKinds    = map[string]bool{"table": true, "chain": true, "rule": true, "set": true, "map": true, "element": true, "counter": true, "quota": true, "limit": true}
	nftCommands = map[string]bool{"add": true, "create": true, "replace": true, "insert": true, "delete": true, "flush": true}
)

// CheckNftRuleset enforces the nftables scope: every object of the document belongs to the table
// `inet chaosgw`. It also rejects anything it does not understand (`flush ruleset`, other tables,
// metainfo): the scope is an allowlist.
func CheckNftRuleset(raw json.RawMessage) error {
	// Exact, case-sensitive keys only: encoding/json matches struct fields case-insensitively and
	// nft does not, so a decoder into structs could see other values than nft does.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("ruleset is not an nftables JSON document: %w", err)
	}
	if len(doc) != 1 {
		return errors.New("ruleset must be an object with the single key nftables")
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(doc["nftables"], &items); err != nil {
		return fmt.Errorf("ruleset is not an nftables JSON document: %w", err)
	}
	if len(items) == 0 {
		return errors.New("ruleset has no commands")
	}
	if len(items) > maxNftItems {
		return fmt.Errorf("ruleset has %d commands, the limit is %d", len(items), maxNftItems)
	}
	for i, item := range items {
		if len(item) != 1 {
			return fmt.Errorf("nftables[%d]: want exactly one command or object, got %d keys", i, len(item))
		}
		for key, val := range item {
			object := val
			kind := key
			if nftCommands[key] {
				var inner map[string]json.RawMessage
				if err := json.Unmarshal(val, &inner); err != nil || len(inner) != 1 {
					return fmt.Errorf("nftables[%d]: %s needs exactly one object", i, key)
				}
				for k, v := range inner {
					kind, object = k, v
				}
			}
			if !nftKinds[kind] {
				return fmt.Errorf("nftables[%d]: %q is outside the allowed scope (table %s %s)", i, kind, NftFamily, NftTable)
			}
			if err := checkNftObject(kind, object); err != nil {
				return fmt.Errorf("nftables[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func checkNftObject(kind string, object json.RawMessage) error {
	var o map[string]json.RawMessage
	if err := json.Unmarshal(object, &o); err != nil {
		return fmt.Errorf("%s: %w", kind, err)
	}
	// a key that differs from a significant one only in case would be ignored by nft
	for k := range o {
		for _, sig := range []string{"family", "name", "table"} {
			if k != sig && strings.EqualFold(k, sig) {
				return fmt.Errorf("%s has the key %q; keys are case-sensitive", kind, k)
			}
		}
	}
	str := func(key string) (string, bool) {
		var s string
		return s, json.Unmarshal(o[key], &s) == nil && o[key] != nil
	}
	if f, ok := str("family"); !ok || f != NftFamily {
		return fmt.Errorf("%s is not in family %s", kind, NftFamily)
	}
	key := "table"
	if kind == "table" {
		key = "name" // a table object names itself
	}
	if t, ok := str(key); !ok || t != NftTable {
		return fmt.Errorf("%s is outside table %s %s", kind, NftFamily, NftTable)
	}
	return nil
}

func (o NftAddElements) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if !setName.MatchString(o.Set) {
		return fmt.Errorf("invalid set name %q", o.Set)
	}
	if len(o.Elements) == 0 || len(o.Elements) > maxNftItems {
		return fmt.Errorf("%d elements, want 1..%d", len(o.Elements), maxNftItems)
	}
	if o.TimeoutSeconds < 0 || o.TimeoutSeconds > 30*24*3600 {
		return fmt.Errorf("timeout %d s out of range", o.TimeoutSeconds)
	}
	for _, e := range o.Elements {
		if !validElement(e) {
			return fmt.Errorf("invalid set element %q (want an address, a prefix or a MAC address)", e)
		}
	}
	return nil
}

func validElement(e string) bool {
	if a, err := netip.ParseAddr(e); err == nil {
		return a.Zone() == ""
	}
	if _, err := netip.ParsePrefix(e); err == nil {
		return true
	}
	hw, err := net.ParseMAC(e)
	return err == nil && len(hw) == 6
}

func parseAddrOrPrefix(s string, family int) (string, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		if p.Addr().Is4() != (family == 4) {
			return "", fmt.Errorf("%q is not an IPv%d prefix", s, family)
		}
		return p.String(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" || a.Is4() != (family == 4) {
		return "", fmt.Errorf("%q is not an IPv%d address or prefix", s, family)
	}
	return a.String(), nil
}

func checkTable(t int) error {
	if t < OwnTableFirst || t > OwnTableLast {
		return fmt.Errorf("routing table %d is outside Chaos Gateway's tables %d-%d", t, OwnTableFirst, OwnTableLast)
	}
	return nil
}

func (o Routing) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if len(o.Routes)+len(o.Rules) == 0 {
		return errors.New("no routes or rules")
	}
	if len(o.Routes)+len(o.Rules) > maxEntries {
		return fmt.Errorf("more than %d entries", maxEntries)
	}
	for i, r := range o.Routes {
		if err := r.validate(); err != nil {
			return fmt.Errorf("routes[%d]: %w", i, err)
		}
	}
	for i, r := range o.Rules {
		if err := r.validate(); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
	}
	return nil
}

func (r Route) validate() error {
	if err := oneOf("action", r.Action, "replace", "delete"); err != nil {
		return err
	}
	if r.Family != 4 && r.Family != 6 {
		return fmt.Errorf("family %d is not 4 or 6", r.Family)
	}
	if err := checkTable(r.Table); err != nil {
		return err
	}
	if r.Dst != "default" {
		if _, err := parseAddrOrPrefix(r.Dst, r.Family); err != nil {
			return err
		}
	}
	if r.Via != "" {
		a, err := netip.ParseAddr(r.Via)
		if err != nil || a.Zone() != "" || a.Is4() != (r.Family == 4) {
			return fmt.Errorf("via %q is not an IPv%d address", r.Via, r.Family)
		}
	}
	if r.Dev != "" {
		if err := checkDev(r.Dev); err != nil {
			return err
		}
	}
	typ := r.Type
	if typ == "" {
		typ = "unicast"
	}
	if err := oneOf("type", typ, "unicast", "blackhole", "unreachable", "prohibit"); err != nil {
		return err
	}
	if typ == "unicast" && r.Action == "replace" && r.Via == "" && r.Dev == "" {
		return errors.New("a unicast route needs via or dev")
	}
	if typ != "unicast" && (r.Via != "" || r.Dev != "") {
		return fmt.Errorf("a %s route takes neither via nor dev", typ)
	}
	if r.Metric != nil && (*r.Metric < 0 || *r.Metric > 1<<30) {
		return fmt.Errorf("metric %d out of range", *r.Metric)
	}
	return nil
}

func (r Rule) validate() error {
	if err := oneOf("action", r.Action, "add", "delete"); err != nil {
		return err
	}
	if r.Family != 4 && r.Family != 6 {
		return fmt.Errorf("family %d is not 4 or 6", r.Family)
	}
	// 0, 32766 and 32767 are the kernel's local, main and default rules
	if r.Priority < 1 || r.Priority > 32765 {
		return fmt.Errorf("priority %d outside 1-32765", r.Priority)
	}
	if err := checkTable(r.Table); err != nil {
		return err
	}
	for _, s := range []string{r.From, r.To} {
		if s == "" {
			continue
		}
		if _, err := parseAddrOrPrefix(s, r.Family); err != nil {
			return err
		}
	}
	if r.Fwmark != "" {
		if _, _, err := parseMark(r.Fwmark); err != nil {
			return err
		}
	}
	for _, d := range []string{r.Iif, r.Oif} {
		if d != "" {
			if err := checkDev(d); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseMark parses "value" or "value/mask", each decimal or 0x-hex.
var markRE = regexp.MustCompile(`^(0[xX][0-9a-fA-F]{1,8}|[0-9]{1,10})(/(0[xX][0-9a-fA-F]{1,8}|[0-9]{1,10}))?$`)

func parseMark(s string) (value uint32, mask *uint32, err error) {
	if !markRE.MatchString(s) {
		return 0, nil, fmt.Errorf("invalid fwmark %q", s)
	}
	v, m, hasMask := strings.Cut(s, "/")
	n, err := strconv.ParseUint(v, 0, 32)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid fwmark %q", s)
	}
	value = uint32(n)
	if hasMask {
		k, err := strconv.ParseUint(m, 0, 32)
		if err != nil {
			return 0, nil, fmt.Errorf("invalid fwmark mask in %q", s)
		}
		mk := uint32(k)
		mask = &mk
	}
	return value, mask, nil
}

func (o TC) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if len(o.Entries) == 0 || len(o.Entries) > maxEntries {
		return fmt.Errorf("%d tc entries, want 1..%d", len(o.Entries), maxEntries)
	}
	for i, e := range o.Entries {
		if err := e.validate(); err != nil {
			return fmt.Errorf("entries[%d]: %w", i, err)
		}
	}
	return nil
}

func (e TCEntry) validate() error {
	if err := oneOf("object", e.Object, "qdisc", "class", "filter"); err != nil {
		return err
	}
	if err := oneOf("action", e.Action, "add", "replace", "change", "delete"); err != nil {
		return err
	}
	if err := checkDev(e.Dev); err != nil {
		return err
	}
	switch e.Parent {
	case "", "root", "ingress", "clsact":
	default:
		if !tcHandl.MatchString(e.Parent) {
			return fmt.Errorf("invalid parent %q", e.Parent)
		}
	}
	if e.ClassID != "" && (e.Object != "class" || !tcHandl.MatchString(e.ClassID) || strings.HasSuffix(e.ClassID, ":")) {
		return fmt.Errorf("invalid classid %q (only classes have one, as major:minor)", e.ClassID)
	}
	if e.Object == "class" && e.Handle != "" {
		return errors.New("a class is named by classid, not handle")
	}
	if e.Object == "class" && e.ClassID == "" {
		return errors.New("a class needs a classid")
	}
	if e.Handle != "" && !tcHandl.MatchString(e.Handle) && !validFilterHandle(e.Handle) {
		return fmt.Errorf("invalid handle %q", e.Handle)
	}
	if len(e.Args) > maxTCArgs {
		return fmt.Errorf("%d arguments exceed the limit of %d", len(e.Args), maxTCArgs)
	}
	for _, a := range e.Args {
		if !tcToken.MatchString(a) || strings.Contains(a, "..") {
			return fmt.Errorf("invalid argument %q", a)
		}
		if tcForbidden[strings.ToLower(a)] {
			return fmt.Errorf("argument %q is not allowed", a)
		}
	}
	if e.Parent == "ingress" || e.Parent == "clsact" {
		if e.Object != "qdisc" || len(e.Args) != 0 || e.Handle != "" {
			return fmt.Errorf("%s is a qdisc without arguments or handle", e.Parent)
		}
		return nil
	}
	if e.Object == "qdisc" && e.Action != "delete" && (len(e.Args) == 0 || !tcKinds[e.Args[0]]) {
		return fmt.Errorf("a qdisc needs a kind, one of %s", kindList())
	}
	if e.Object == "class" && e.Action != "delete" && (len(e.Args) == 0 || !tcKinds[e.Args[0]]) {
		return fmt.Errorf("a class needs a kind, one of %s", kindList())
	}
	if e.Object != "filter" && e.Action != "delete" && e.Parent == "" {
		return errors.New("a qdisc or class needs a parent (root or a handle)")
	}
	if e.Object == "filter" && e.Action != "delete" && e.Parent == "" {
		return errors.New("a filter needs a parent")
	}
	if e.Object == "filter" && (e.Parent == "root") {
		return errors.New("a filter hangs below a handle, not root")
	}
	return nil
}

var filterHandle = regexp.MustCompile(`^(0x[0-9a-fA-F]{1,8}|[0-9]{1,10}|[0-9a-fA-F]{1,3}::?[0-9a-fA-F]{1,3})$`)

// validFilterHandle accepts the handle forms filters use: a number (fw), or u32's 800::801.
func validFilterHandle(h string) bool { return filterHandle.MatchString(h) }

func kindList() string {
	var k []string
	for name := range tcKinds {
		k = append(k, name)
	}
	sort.Strings(k)
	return strings.Join(k, ", ")
}

func (o Offloads) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	return checkDevs(o.Devs, false)
}

func (o DockerUser) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := oneOf("action", o.Action, "ensure", "remove"); err != nil {
		return err
	}
	return checkDevs(o.Devs, false)
}

func (o AssignInterfaces) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := checkDevs(o.Devs, true); err != nil {
		return err
	}
	for _, d := range o.Devs {
		if foreignDev.MatchString(d) {
			return fmt.Errorf("%s is not a Chaos Gateway interface (loopback and Docker's devices cannot be assigned)", d)
		}
	}
	return nil
}

func (o Read) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := oneOf("what", o.What, ReadLinks, ReadAddrs, ReadRoutes, ReadRules, ReadNft, ReadQdiscs, ReadClasses, ReadFilters, ReadOffloads); err != nil {
		return err
	}
	if o.Dev != "" {
		if err := checkDev(o.Dev); err != nil {
			return err
		}
	}
	if (o.What == ReadOffloads) && o.Dev == "" {
		return errors.New("offloads need a dev")
	}
	if o.Table != "" && (o.What != ReadRoutes || !readTbl.MatchString(o.Table)) {
		return fmt.Errorf("invalid table %q", o.Table)
	}
	return nil
}
