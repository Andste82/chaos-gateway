package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the access rules of plan §2.2 and §2.4 (milestone M9): the ordered list of allow,
// drop, reject and TCP reset rules, overlay rules first, compiled into the chains `access_forward`
// and `access_input` and matched on the conntrack original tuple, so a redirected connection (the
// DNS proxy, a TLS case) is judged by the destination the device meant.
//
// Where the rules stand in the packet path.
//
//	input     established/related, loopback, the service namespace, the ANTI-LOCKOUT rule (management
//	          sources reach the control plane), the UI port for everybody else -> the access rules ->
//	          the routing protocols, then the gateway's protection of the test networks (DHCP, DNS,
//	          ping, drop). Everything above the rules cannot be taken away by any rule or overlay.
//	forward   established/related, the service guard, switched traffic, invalid, IPv6 -> the access
//	          rules -> the access matrix -> the service rules and the default.
//
// Established traffic is accepted before the rules, so a rule changes new connections only
// (spike S3, C2; plan D11). "Also cut existing connections" is a time-limited chain in front of
// the established accept (CutRules), run by the engine, which also deletes the conntrack entries
// of the cut connections (Target.Access.Winner tells which entries a rule owns).
//
// An allow rule means "this rule decides, and the answer is yes". In forward that is an accept: the
// rule is an exception to the access matrix. In input it is a return: the gateway's protection of
// the test networks stays what it is (it answers DHCP, DNS and ping, nothing else), a rule cannot
// open a port of the gateway. docs/open-items.md P2-M9-01 records the choice.
//
// The sources of a rule are address sets (`asrc_*`), refilled at every apply from the identity of
// the devices, like the other sets. A rule whose scope has no address yet stays in the chain with
// an empty set, so its counter exists and the rule takes effect when the first address appears.

// Names of the chains and the counter of the access rules.
const (
	AccessForwardChain = "access_forward"
	AccessInputChain   = "access_input"
	CutForwardChain    = "cut_forward"
	CutInputChain      = "cut_input"
	// AntiLockoutCounter counts the packets the anti-lockout rule lets in.
	AntiLockoutCounter = "anti_lockout"
)

// Limits of the access rules (plan §3.3 style: the compiler refuses what it cannot carry and says
// who caused it).
const (
	// DefaultRuleLimit is the number of access rules, overlay rules included, one compile accepts.
	// A rule is evaluated for every new connection, so the chain is a linear scan: 1000 rules cost
	// microseconds, not milliseconds, on the smallest supported hardware.
	DefaultRuleLimit = 1000
	// MaxRuleElements is the number of address elements all source sets of the rules together may
	// hold (a group of 250 devices in 200 rules is 50000).
	MaxRuleElements = 1 << 16
)

// SystemRule is a rule the compiler adds that no configuration and no overlay can override. It is
// listed first, locked, in the rule list of the API (plan §2.17).
type SystemRule struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Counter     string `json:"counter"`
}

// AntiLockoutRule describes the system rule "Control plane access" (plan §2.16, risk 20).
var AntiLockoutRule = SystemRule{
	Key:         "system:anti_lockout",
	Name:        "Control plane access",
	Description: "The management sources always reach SSH and the UI/API of the gateway. It is evaluated before every access rule and cannot be overridden.",
	Counter:     AntiLockoutCounter,
}

// PortRange is an inclusive range of ports; a single port has From == To.
type PortRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// AccessRule is one compiled rule in the order it is evaluated.
type AccessRule struct {
	// Key identifies the rule: layer and id (`overlay:<uuid>`, `config:<uuid>`). The counter is
	// derived from it, so the counter stays while the rule stays (an overlay that is written again
	// keeps its id).
	Key   string `json:"key"`
	Layer string `json:"layer"`
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	// Position is the 0-based place in the effective order: overlay rules (newest first), then the
	// configured rules in their order.
	Position    int    `json:"position"`
	Action      string `json:"action"`
	CutExisting bool   `json:"cut_existing,omitempty"`
	// Scope and Destination describe the selector for messages.
	Scope       string `json:"scope"`
	Destination string `json:"destination,omitempty"`
	Protocol    string `json:"protocol"`
	// AnySource is the global scope: every source in the test, WireGuard and remote networks.
	AnySource bool `json:"any_source,omitempty"`
	// Sources are the addresses of the source scope right now (empty for AnySource).
	Sources   []netip.Prefix `json:"sources,omitempty"`
	SourceSet string         `json:"source_set"`
	// DestKind is "" (any destination), "prefixes" (Dest) or "uplink" (everything outside the
	// networks and the management network).
	DestKind string         `json:"dest_kind,omitempty"`
	Dest     []netip.Prefix `json:"dest,omitempty"`
	Ports    []PortRange    `json:"ports,omitempty"`
	Counter  string         `json:"counter"`
}

// Cuts reports whether the rule can cut existing connections.
func (r AccessRule) Cuts() bool { return r.CutExisting && r.Action != "allow" }

// AccessPlan is the compiled rule list.
type AccessPlan struct {
	// Rules are the effective rules in evaluation order.
	Rules []AccessRule `json:"rules"`
	// GlobalSources are the prefixes the global scope covers: the test, WireGuard and remote networks.
	GlobalSources []netip.Prefix `json:"global_sources,omitempty"`
	// NonUplink are the prefixes a destination "uplink" excludes.
	NonUplink []netip.Prefix `json:"non_uplink,omitempty"`
	// guard is the match of the anti-lockout rule: the cut chain of the input hook starts with it.
	guard []any
}

// Tuple is the conntrack original tuple of a packet, the unit the rules judge.
type Tuple struct {
	Src, Dst netip.Addr
	// Proto is tcp, udp, icmp or another protocol name.
	Proto string
	// Port is the original destination port; 0 for protocols without ports.
	Port int
}

func inPrefixes(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Matches reports whether the rule selects the tuple. It is the Go mirror of the nftables rule.
func (p *AccessPlan) Matches(r *AccessRule, t Tuple) bool {
	if r.AnySource {
		if !inPrefixes(p.GlobalSources, t.Src) {
			return false
		}
	} else if !inPrefixes(r.Sources, t.Src) {
		return false
	}
	switch r.DestKind {
	case "prefixes":
		if !inPrefixes(r.Dest, t.Dst) {
			return false
		}
	case "uplink":
		if inPrefixes(p.NonUplink, t.Dst) {
			return false
		}
	}
	if r.Protocol != "any" && t.Proto != r.Protocol {
		return false
	}
	if len(r.Ports) > 0 {
		ok := false
		for _, pr := range r.Ports {
			if t.Port >= pr.From && t.Port <= pr.To && t.Port != 0 {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Winner returns the first rule that selects the tuple, nil when none does (the access matrix
// decides). This is what the kernel does for a new connection, and what decides which connections a
// cut owns.
func (p *AccessPlan) Winner(t Tuple) *AccessRule {
	if p == nil {
		return nil
	}
	for i := range p.Rules {
		if p.Matches(&p.Rules[i], t) {
			return &p.Rules[i]
		}
	}
	return nil
}

// HasCuts reports whether any rule can cut existing connections: only then the packet path has the
// cut chains.
func (p *AccessPlan) HasCuts() bool {
	if p == nil {
		return false
	}
	for _, r := range p.Rules {
		if r.Cuts() {
			return true
		}
	}
	return false
}

// Rule returns the rule of a key.
func (p *AccessPlan) Rule(key string) (*AccessRule, bool) {
	if p == nil {
		return nil, false
	}
	for i := range p.Rules {
		if p.Rules[i].Key == key {
			return &p.Rules[i], true
		}
	}
	return nil, false
}

// ---- compiling the rules -----------------------------------------------------------------

// ruleCounterName names the counter of a rule. It is derived from the rule's key, not from its
// position, so a counter keeps its name (and its value) while the rule stays, however the order
// changes, and a removed rule's counter is deleted with it (plan §3.2).
func ruleCounterName(key string) string {
	h := sha256.Sum256([]byte(key))
	return "rule_" + hex.EncodeToString(h[:5])
}

func scopeSetName(sc model.Scope) string {
	b, _ := json.Marshal(sc)
	h := sha256.Sum256(b)
	return hashName("asrc_"+hex.EncodeToString(h[:4]), "ipv4_addr", []string{"interval"})
}

// nonUplinkSetName is the set of destinations a rule with the destination "uplink" excludes.
func nonUplinkSetName() string {
	return hashName("anon_uplink", "ipv4_addr", []string{"interval"})
}

// mergePorts turns the ports and ranges of a selector into sorted disjoint ranges (an nftables
// interval set refuses overlapping elements).
func mergePorts(ports []int, ranges []model.PortRange) []PortRange {
	var all []PortRange
	for _, p := range ports {
		all = append(all, PortRange{p, p})
	}
	for _, r := range ranges {
		all = append(all, PortRange{r.From, r.To})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].From != all[j].From {
			return all[i].From < all[j].From
		}
		return all[i].To < all[j].To
	})
	var out []PortRange
	for _, r := range all {
		if n := len(out); n > 0 && r.From <= out[n-1].To+1 {
			if r.To > out[n-1].To {
				out[n-1].To = r.To
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func describeDestination(idx *domain.Index, d *model.Destination) string {
	if d == nil {
		return ""
	}
	switch {
	case d.Uplink != nil:
		return "uplink"
	case d.Network != nil:
		if n, ok := idx.Networks[strings.ToLower(*d.Network)]; ok && n.Name != "" {
			return "network " + n.Name
		}
		return "network " + *d.Network
	case d.Cidr != nil:
		return *d.Cidr
	case d.Hostname != nil:
		return "host " + *d.Hostname
	}
	return ""
}

// candidateRule is a rule before it has a position.
type candidateRule struct {
	layer domain.Layer
	id    string
	name  string
	scope model.Scope
	body  model.AccessRuleBody
}

// accessCandidates lists the enabled rules in evaluation order: overlay rules, newest first (the
// same order domain.World.ResolveAccess uses), then the configured rules in their order.
func accessCandidates(cfg *model.Configuration, overlays []model.Overlay) []candidateRule {
	type ov struct {
		c     candidateRule
		since int64
	}
	var os []ov
	for _, o := range overlays {
		if o.Kind != "rule" || o.Rule == nil || o.Target == nil {
			continue
		}
		os = append(os, ov{candidateRule{domain.LayerOverlay, o.Id.String(), "", *o.Target, *o.Rule}, o.UpdatedAt.UnixNano()})
	}
	sort.SliceStable(os, func(i, j int) bool {
		if os[i].since != os[j].since {
			return os[i].since > os[j].since
		}
		return os[i].c.id < os[j].c.id
	})
	var out []candidateRule
	for _, o := range os {
		out = append(out, o.c)
	}
	rules := map[string]model.AccessRule{}
	if cfg.AccessRules != nil {
		rules = *cfg.AccessRules
	}
	if cfg.AccessRuleOrder != nil {
		for _, uid := range *cfg.AccessRuleOrder {
			id := uid.String()
			r, ok := rules[id]
			if !ok || (r.Enabled != nil && !*r.Enabled) {
				continue
			}
			name := ""
			if r.Name != nil {
				name = *r.Name
			}
			body := model.AccessRuleBody{Action: r.Action, CutExisting: r.CutExisting,
				Destination: r.Destination, Ports: r.Ports, PortRanges: r.PortRanges, Protocol: r.Protocol}
			out = append(out, candidateRule{domain.LayerConfig, id, name, r.Source, body})
		}
	}
	return out
}

// compileAccess resolves the rules against the identity of the devices and builds the plan.
func (t *Target) compileAccess(in Input, idx *domain.Index) {
	cfg := in.Config
	if !domain.IsNormalized(cfg) {
		n, errs := domain.Normalize(cfg)
		if len(errs) > 0 {
			return // a configuration with dangling references has no rules to resolve
		}
		cfg = n
	}
	cands := accessCandidates(cfg, in.Overlays)
	if len(cands) == 0 {
		return
	}
	w, err := domain.NewWorld(cfg, in.Overlays)
	if err != nil {
		t.errorf(CodeFaultInvalid, "", "access rules cannot be resolved: %v", err)
		return
	}
	identity := domain.Identity{}
	if in.Identity != nil {
		identity = *in.Identity
	}
	limit := in.RuleLimit
	if limit <= 0 {
		limit = DefaultRuleLimit
	}
	if len(cands) > limit {
		t.ruleCapacityProblem(cands, fmt.Sprintf("%d access rules (overlays included) exceed the limit of %d", len(cands), limit))
		return
	}

	plan := &AccessPlan{GlobalSources: parsePrefixes(t.classifyNets()), NonUplink: w.NonUplinkPrefixes()}
	sets := map[string]SetDef{}
	elements := 0
	for _, c := range cands {
		b := c.body
		if b.Destination != nil && b.Destination.Hostname != nil {
			// the addresses of a name are known at run time only (DNS-derived sets, M20): the rule
			// would match nothing, which for a drop or reject rule means traffic gets through the
			// operator thinks is blocked. It stays out of the chain and the preview says so.
			t.warn(CodeHostnameUnresolved, "", "the access rule %s selects the hostname %s: its addresses are known at run time only (DNS-derived sets), so the rule does not apply yet", c.id, *b.Destination.Hostname)
			continue
		}
		key := string(c.layer) + ":" + c.id
		r := AccessRule{Key: key, Layer: string(c.layer), ID: c.id, Name: c.name, Position: len(plan.Rules),
			Action: string(b.Action), CutExisting: b.CutExisting != nil && *b.CutExisting,
			Scope: describeScope(idx, c.scope), Destination: describeDestination(idx, b.Destination),
			Protocol: "any", Counter: ruleCounterName(key)}
		if b.Protocol != nil {
			r.Protocol = string(*b.Protocol)
		}
		prefixes, all := w.ScopePrefixes(c.scope, identity)
		if all {
			r.AnySource = true
			r.SourceSet = classifyNetsName()
		} else {
			r.Sources = prefixes
			r.SourceSet = scopeSetName(c.scope)
			if _, ok := sets[r.SourceSet]; !ok {
				s := SetDef{Name: r.SourceSet, Type: "ipv4_addr", Flags: []string{"interval"}}
				for _, p := range prefixes {
					s.Elements = append(s.Elements, linux.NormalizeElement(p.String()))
				}
				sets[r.SourceSet] = s
				elements += len(s.Elements)
			}
		}
		if d := b.Destination; d != nil {
			switch {
			case d.Uplink != nil:
				r.DestKind = "uplink"
			case d.Network != nil:
				r.DestKind, r.Dest = "prefixes", w.NetworkPrefixes(*d.Network)
			case d.Cidr != nil:
				r.DestKind = "prefixes"
				if a, err := netip.ParseAddr(*d.Cidr); err == nil {
					r.Dest = []netip.Prefix{netip.PrefixFrom(a, 32)}
				} else if p, err := netip.ParsePrefix(*d.Cidr); err == nil {
					r.Dest = []netip.Prefix{p.Masked()}
				}
			}
		}
		if r.Protocol == "tcp" || r.Protocol == "udp" {
			r.Ports = mergePorts(deref(b.Ports), deref(b.PortRanges))
		}
		plan.Rules = append(plan.Rules, r)
	}
	elementLimit := in.RuleElementLimit
	if elementLimit <= 0 {
		elementLimit = MaxRuleElements
	}
	if elements > elementLimit {
		t.Problems = append(t.Problems, Problem{Severity: SevError, Code: CodeCapacityExceeded, Scope: "access rules",
			Message: fmt.Sprintf("the sources of the access rules need %d address elements, more than the limit of %d; narrow the scopes of the rules that name big groups or networks", elements, elementLimit),
			Faults:  biggestSources(plan.Rules)})
		return
	}
	if len(plan.Rules) == 0 {
		return
	}
	t.Access = plan
	names := make([]string, 0, len(sets))
	for n := range sets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t.accessSets = append(t.accessSets, sets[n])
	}
	for _, r := range plan.Rules {
		if r.DestKind == "uplink" {
			s := SetDef{Name: nonUplinkSetName(), Type: "ipv4_addr", Flags: []string{"interval"}}
			for _, p := range plan.NonUplink {
				s.Elements = append(s.Elements, linux.NormalizeElement(p.String()))
			}
			t.accessSets = append(t.accessSets, s)
			break
		}
	}
}

func parsePrefixes(in []string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range in {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// biggestSources names the rules with the most source addresses, the biggest first.
func biggestSources(rules []AccessRule) []string {
	rs := append([]AccessRule(nil), rules...)
	sort.SliceStable(rs, func(i, j int) bool { return len(rs[i].Sources) > len(rs[j].Sources) })
	var out []string
	for _, r := range rs {
		if len(out) == 10 || len(r.Sources) == 0 {
			break
		}
		out = append(out, r.ID)
	}
	return out
}

// ruleCapacityProblem reports capacity_exceeded for too many rules. It names the overlay rules
// first (the newest is the one that tipped the balance), then the configured rules.
func (t *Target) ruleCapacityProblem(cands []candidateRule, msg string) {
	p := Problem{Severity: SevError, Code: CodeCapacityExceeded, Message: msg, Scope: "access rules"}
	for _, c := range cands {
		if c.layer == domain.LayerOverlay && len(p.Faults) < 20 {
			p.Faults = append(p.Faults, c.id)
		}
	}
	t.Problems = append(t.Problems, p)
}

// ---- the nftables rules ------------------------------------------------------------------

// matchExpr is the selector of a rule as nftables expressions on the conntrack original tuple.
func (p *AccessPlan) matchExpr(r AccessRule) []any {
	var e []any
	e = append(e, eq(ctOriginalIP("saddr"), setRef(r.SourceSet)))
	switch r.DestKind {
	case "prefixes":
		e = append(e, eq(ctOriginalIP("daddr"), prefixes(r.Dest)))
	case "uplink":
		e = append(e, match(ctOriginalIP("daddr"), "!=", setRef(nonUplinkSetName())))
	}
	if r.Protocol != "any" {
		e = append(e, eq(meta("l4proto"), r.Protocol))
	}
	if len(r.Ports) > 0 {
		e = append(e, eq(ctOriginal("proto-dst"), portValue(r.Ports)))
	}
	return e
}

func portValue(ports []PortRange) any {
	item := func(p PortRange) any {
		if p.From == p.To {
			return p.From
		}
		return map[string]any{"range": []any{p.From, p.To}}
	}
	if len(ports) == 1 {
		return item(ports[0])
	}
	vals := make([]any, len(ports))
	for i, p := range ports {
		vals[i] = item(p)
	}
	return anon(vals...)
}

// rejectICMP is the statement of the action reject. icmpx names the same error for IPv4 (ICMP) and
// IPv6 (ICMPv6): the rule needs no variant per family.
func rejectICMP() any {
	return map[string]any{"reject": map[string]any{"type": "icmpx", "expr": "port-unreachable"}}
}

func rejectTCPReset() any {
	return map[string]any{"reject": map[string]any{"type": "tcp reset"}}
}

// verdictFor is the statement that ends a rule in a hook. An allow rule accepts in forward and
// returns in input (see the file comment).
func verdictFor(action, hook string) any {
	switch action {
	case "allow":
		if hook == "input" {
			return verdict("return")
		}
		return verdict("accept")
	case "drop":
		return verdict("drop")
	case "reject":
		return rejectICMP()
	default: // reset
		return rejectTCPReset()
	}
}

// chain builds the access chain of a hook.
func (p *AccessPlan) chain(name, hook string) Chain {
	c := Chain{Name: name}
	for _, r := range p.Rules {
		expr := p.matchExpr(r)
		expr = append(expr, counter(r.Counter), verdictFor(r.Action, hook))
		c.Rules = append(c.Rules, newRule(expr...))
	}
	return c
}

// CutRules builds the rules of the cut chains for a window in which the rules of the given keys cut
// the connections that exist (spike S3, C6). The chains run in front of the established accept. The
// rules are the effective list again, in its order:
//
//   - a rule that cuts: its selector with `ct state established`, the original direction and
//     TCP, then `reject with tcp reset`: the device gets the reset for its next packet and can
//     reconnect, the server side stays half-open as in a real outage. Packets of the rule's
//     selector that are not TCP, and the reply direction, return from the chain;
//   - every other rule: its selector, then `return`. The connection belongs to the first rule that
//     selects it; if that rule does not cut, a later cutting rule must not touch it.
//
// The input chain starts with the anti-lockout rule's match and a return, so the control plane is
// never reset.
func (p *AccessPlan) CutRules(keys []string) (forward, input []Rule) {
	if p == nil {
		return nil, nil
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	last := -1
	for i, r := range p.Rules {
		if want[r.Key] && r.Cuts() {
			last = i
		}
	}
	if last < 0 {
		return nil, nil
	}
	var body []Rule
	for i, r := range p.Rules {
		if i > last {
			break
		}
		expr := p.matchExpr(r)
		if want[r.Key] && r.Cuts() && (r.Protocol == "tcp" || r.Protocol == "any") {
			cut := append([]any(nil), expr...)
			if r.Protocol == "any" {
				cut = append(cut, eq(meta("l4proto"), "tcp"))
			}
			cut = append(cut, ctState("established"), eq(ctKey("direction"), "original"), rejectTCPReset())
			body = append(body, newRule(cut...))
		}
		if i < last {
			body = append(body, newRule(append(append([]any(nil), expr...), verdict("return"))...))
		}
	}
	forward = body
	if p.guard != nil {
		input = append(input, newRule(append(append([]any(nil), p.guard...), verdict("return"))...))
	}
	input = append(input, body...)
	return forward, input
}

// CutTransaction is the nftables JSON that opens a cut window: it replaces the content of the cut
// chains. Without keys it closes the window (empties the chains).
func (p *AccessPlan) CutTransaction(keys []string) ([]byte, error) {
	fwd, in := p.CutRules(keys)
	var cmds []any
	for _, c := range []struct {
		name  string
		rules []Rule
	}{{CutForwardChain, fwd}, {CutInputChain, in}} {
		cmds = append(cmds, cmd("flush", "chain", map[string]any{"name": c.name}))
		for _, r := range c.rules {
			cmds = append(cmds, cmd("add", "rule", map[string]any{"chain": c.name, "expr": r.Expr, "comment": r.Comment}))
		}
	}
	return json.Marshal(map[string]any{"nftables": cmds})
}
