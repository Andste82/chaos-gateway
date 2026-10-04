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

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/domain"
)

// What Chaos Gateway owns. Anything outside is out of scope for the executor (plan §2.16).
const (
	// NftFamily and NftTable name the only nftables table operations may touch.
	NftFamily = "inet"
	NftTable  = "chaosgw"
	// ProtoTag marks the routes and rules the executor writes (`proto`/`protocol` of iproute2).
	// Rules are only ever deleted together with this tag, so foreign rules are out of reach.
	ProtoTag = 201
	// OwnTableFirst..OwnTableLast are the routing tables Chaos Gateway uses (plan §2.2.2: 100 policy,
	// 102 service, the PMTU mirrors of M10, ...). domain.OwnTableFirst/Last are the same range,
	// reserved against external routing daemons.
	OwnTableFirst = domain.OwnTableFirst
	OwnTableLast  = domain.OwnTableLast
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

func (o NftDelElements) validate() error {
	return NftAddElements{Target: o.Target, Set: o.Set, Elements: o.Elements}.validate()
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

var sysctlLimits = map[string]struct {
	perDev bool
	max    int
}{"ip_forward": {false, 1}, "nf_conntrack_acct": {false, 1}, "accept_ra": {true, 2}, "disable_ipv6": {true, 1}}

func checkSysctl(name, dev string) error {
	lim, ok := sysctlLimits[name]
	if !ok {
		return fmt.Errorf("sysctl %q is not one the executor sets", name)
	}
	if lim.perDev != (dev != "") {
		return fmt.Errorf("sysctl %s: dev is %s", name, map[bool]string{true: "required", false: "not allowed"}[lim.perDev])
	}
	if dev != "" {
		return checkDev(dev)
	}
	return nil
}

var (
	wgKey      = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)
	uuidRef    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hostName   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	maxWGPeers = 4096
)

func (o WireGuard) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := oneOf("action", o.Action, "ensure", "delete"); err != nil {
		return err
	}
	if err := checkDev(o.Name); err != nil {
		return err
	}
	if o.Action == "delete" {
		if o.ListenPort != 0 || o.MTU != 0 || o.KeyRef != "" || len(o.Peers) != 0 {
			return errors.New("delete takes the name only")
		}
		return nil
	}
	if o.ListenPort < 1 || o.ListenPort > 65535 {
		return fmt.Errorf("listen port %d out of range", o.ListenPort)
	}
	if o.MTU != 0 && (o.MTU < 1280 || o.MTU > 9000) {
		return fmt.Errorf("mtu %d out of range 1280-9000", o.MTU)
	}
	if !uuidRef.MatchString(o.KeyRef) {
		return fmt.Errorf("key_ref %q is not a UUID", o.KeyRef)
	}
	if len(o.Peers) > maxWGPeers {
		return fmt.Errorf("%d peers exceed the limit of %d", len(o.Peers), maxWGPeers)
	}
	seen := map[string]bool{}
	for i, p := range o.Peers {
		if err := p.validate(); err != nil {
			return fmt.Errorf("peers[%d]: %w", i, err)
		}
		if seen[p.PublicKey] {
			return fmt.Errorf("peers[%d]: the public key is used twice", i)
		}
		seen[p.PublicKey] = true
	}
	return nil
}

// ipv6Default is the one IPv6 allowed ip a WireGuard peer may carry (M4c-05).
var ipv6Default = netip.PrefixFrom(netip.IPv6Unspecified(), 0)

func (p WGPeer) validate() error {
	if !wgKey.MatchString(p.PublicKey) {
		return errors.New("invalid public key")
	}
	if p.PresharedKeyRef != "" && !uuidRef.MatchString(p.PresharedKeyRef) {
		return fmt.Errorf("preshared_key_ref %q is not a UUID", p.PresharedKeyRef)
	}
	if len(p.AllowedIPs) > 1024 {
		return errors.New("too many allowed ips")
	}
	for _, a := range p.AllowedIPs {
		pf, err := netip.ParsePrefix(a)
		if err != nil || pf.Masked() != pf || (!pf.Addr().Is4() && pf != ipv6Default) {
			// a link's peer may also carry Babel's wire protocol (M4c-05), which needs its IPv6
			// link-local traffic, multicast included, to actually reach it: ::/0 is the one IPv6
			// exception, same idea as the fe80::/64 one in LinkEntry.validate.
			return fmt.Errorf("allowed ip %q is not an IPv4 network prefix (or ::/0)", a)
		}
	}
	if p.Keepalive < 0 || p.Keepalive > 65535 {
		return fmt.Errorf("keepalive %d out of range", p.Keepalive)
	}
	if p.Endpoint != "" {
		host, port, ok := strings.Cut(p.Endpoint, ":")
		n, err := strconv.Atoi(port)
		if !ok || err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("endpoint %q is not host:port", p.Endpoint)
		}
		if a, err := netip.ParseAddr(host); err == nil {
			if !a.Is4() {
				return fmt.Errorf("endpoint %q: only IPv4 addresses", p.Endpoint)
			}
		} else if !hostName.MatchString(host) {
			return fmt.Errorf("endpoint %q: invalid host", p.Endpoint)
		}
	}
	return nil
}

var instanceRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)

func (o Bird) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if o.NS != "" {
		return errors.New("the BIRD instance is not namespaced: its files and its control socket are paths")
	}
	if err := oneOf("action", o.Action, "check", "apply"); err != nil {
		return err
	}
	if !instanceRE.MatchString(o.Instance) {
		return fmt.Errorf("invalid instance name %q", o.Instance)
	}
	if o.Config == "" {
		return errors.New("no configuration")
	}
	if len(o.ImportTables) > 8 {
		return errors.New("too many import tables")
	}
	for _, t := range o.ImportTables {
		if t < 1 || t > 252 || (t >= OwnTableFirst && t <= OwnTableLast) {
			return fmt.Errorf("import table %d is not usable", t)
		}
	}
	return bird.CheckText(o.Config, o.allowedTables())
}

// allowedTables are the kernel tables the configuration may name.
func (o Bird) allowedTables() []int {
	var t []int
	for n := OwnTableFirst; n <= OwnTableLast; n++ {
		t = append(t, n)
	}
	return append(t, o.ImportTables...)
}

func (o Sysctl) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if len(o.Entries) == 0 || len(o.Entries) > maxEntries {
		return fmt.Errorf("%d entries, want 1..%d", len(o.Entries), maxEntries)
	}
	for i, e := range o.Entries {
		if err := checkSysctl(e.Name, e.Dev); err != nil {
			return fmt.Errorf("entries[%d]: %w", i, err)
		}
		if e.Value < 0 || e.Value > sysctlLimits[e.Name].max {
			return fmt.Errorf("entries[%d]: value %d out of range for %s", i, e.Value, e.Name)
		}
	}
	return nil
}

func (o ServiceNS) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := oneOf("action", o.Action, "ensure", "delete"); err != nil {
		return err
	}
	if !nsName.MatchString(o.Name) {
		return fmt.Errorf("invalid namespace name %q", o.Name)
	}
	if o.NS != "" && o.NS == o.Name {
		return errors.New("the service namespace is not the namespace it is attached to")
	}
	for _, d := range []string{o.HostIf, o.PeerIf} {
		if err := checkDev(d); err != nil {
			return err
		}
	}
	if o.HostIf != ServiceHostIf || o.PeerIf != ServicePeerIf {
		return fmt.Errorf("the pair is %s and %s", ServiceHostIf, ServicePeerIf)
	}
	if o.HolderPID < 0 || o.HolderPID > 1<<22 {
		return fmt.Errorf("holder_pid %d out of range", o.HolderPID)
	}
	h, err := netip.ParsePrefix(o.HostCIDR)
	if err != nil {
		return fmt.Errorf("host_cidr: %w", err)
	}
	p, err := netip.ParsePrefix(o.PeerCIDR)
	if err != nil {
		return fmt.Errorf("peer_cidr: %w", err)
	}
	ll := netip.MustParsePrefix("169.254.100.0/24")
	for _, a := range []netip.Prefix{h, p} {
		if !a.Addr().Is4() || !ll.Contains(a.Addr()) || a.Bits() < 24 || a.Bits() > 30 {
			return fmt.Errorf("%s is not an address of 169.254.100.0/24 with a prefix of /24 to /30", a)
		}
	}
	if h.Masked() != p.Masked() || h.Addr() == p.Addr() {
		return errors.New("the two addresses must be different and in one subnet")
	}
	return nil
}

func (o Links) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if len(o.Entries) == 0 || len(o.Entries) > maxEntries {
		return fmt.Errorf("%d entries, want 1..%d", len(o.Entries), maxEntries)
	}
	for i, e := range o.Entries {
		if err := e.validate(); err != nil {
			return fmt.Errorf("entries[%d]: %w", i, err)
		}
	}
	return nil
}

func (e LinkEntry) validate() error {
	if err := oneOf("action", e.Action, "add_bridge", "delete_bridge", "enslave", "release", "up", "down", "addr_replace", "addr_delete"); err != nil {
		return err
	}
	if err := checkDev(e.Name); err != nil {
		return err
	}
	if (e.Action == "enslave") != (e.Master != "") {
		return errors.New("master is given for enslave and only for it")
	}
	if e.Master != "" {
		if err := checkDev(e.Master); err != nil {
			return err
		}
		if e.Master == e.Name {
			return errors.New("an interface cannot be its own master")
		}
	}
	isAddr := e.Action == "addr_replace" || e.Action == "addr_delete"
	if isAddr != (e.CIDR != "") {
		return errors.New("cidr is given for addr_replace and addr_delete and only for them")
	}
	if isAddr {
		p, err := netip.ParsePrefix(e.CIDR)
		// an IPv4 address, or an IPv6 link-local one for a Babel link (M4c-05) — nothing else, since
		// the executor's scope is Chaos Gateway's own addressing, not arbitrary IPv6.
		if err != nil || p.Addr().Zone() != "" || (!p.Addr().Is4() && (p.Bits() != 64 || !p.Addr().IsLinkLocalUnicast())) {
			return fmt.Errorf("%q is not an IPv4 address or an fe80::/64 link-local address with prefix length", e.CIDR)
		}
	}
	return nil
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
	if err := checkDevs(o.OSOwned, true); err != nil {
		return err
	}
	assigned := make(map[string]bool, len(o.Devs))
	for _, d := range o.Devs {
		assigned[d] = true
	}
	for _, d := range o.OSOwned {
		if !assigned[d] {
			return fmt.Errorf("os_owned %s is not in devs", d)
		}
	}
	return nil
}

func (o Read) validate() error {
	if err := o.Target.validate(); err != nil {
		return err
	}
	if err := oneOf("what", o.What, ReadLinks, ReadAddrs, ReadRoutes, ReadRules, ReadNft, ReadQdiscs, ReadClasses, ReadFilters, ReadOffloads, ReadSysctl, ReadAssigned, ReadDockerUser, ReadWireGuard, ReadBird, ReadNeighbors, ReadConntrack, ReadServiceNS); err != nil {
		return err
	}
	if o.What == ReadServiceNS {
		if !nsName.MatchString(o.Service) || o.PID < 0 || o.PID > 1<<22 {
			return fmt.Errorf("invalid service namespace %q or pid %d", o.Service, o.PID)
		}
	} else if o.Service != "" || o.PID != 0 {
		return errors.New("service and pid are only for service_ns reads")
	}
	if o.Dev != "" {
		if err := checkDev(o.Dev); err != nil {
			return err
		}
	}
	if o.What == ReadSysctl {
		if err := checkSysctl(o.Name, o.Dev); err != nil {
			return err
		}
	} else if o.Name != "" {
		return errors.New("name is only for sysctl reads")
	}
	if o.What == ReadBird {
		if !instanceRE.MatchString(o.Instance) {
			return fmt.Errorf("invalid instance name %q", o.Instance)
		}
	} else if o.Instance != "" {
		return errors.New("instance is only for bird reads")
	}
	if (o.What == ReadOffloads || o.What == ReadWireGuard) && o.Dev == "" {
		return errors.New(o.What + " needs a dev")
	}
	if o.Table != "" && (o.What != ReadRoutes || !readTbl.MatchString(o.Table)) {
		return fmt.Errorf("invalid table %q", o.Table)
	}
	return nil
}
