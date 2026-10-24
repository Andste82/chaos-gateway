package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the compiled side of hostname selectors (plan §2.4, §2.6, milestone M20). The kernel
// matches addresses, so a hostname selector is matched against DNS-derived address sets: one set per
// selector (exact name or `*.suffix`), keyed on the requesting device's address and the address the
// DNS proxy returned for the name, `ipv4_addr . ipv4_addr`, with a timeout on every element. The
// proxy reports what it answered before it sends the answer (internal API), the executor writes it
// into the set (a persistent netlink connection, concurrent with the plans), and the rules look the
// pair up like any other match:
//
//	ct original ip saddr . ct original ip daddr @dns_<hash>
//
// The set is keyed on the conntrack original tuple, as everything else, so both directions of a
// connection, a redirected one and one behind NAT find their element. The sets are dynamic
// (SetDef.Dynamic): an apply creates one that is missing and never flushes or refills one that stays,
// so the elements outlive every apply (S11). The name of a set carries a short hash of its definition
// (plan §3.2): a definition that changes creates a new set, the old one is deleted, and the elements
// that were reported for it are gone with it (the engine holds the updates for that set back until
// the apply is through and then writes them into the new one).
//
// Where the rules stand. A fault that names a hostname is not part of a classification table (its
// addresses are not known): domain.Table lists such candidates in Table.Unresolved, in the order of
// their rank. Their rules stand in front of the four lookup levels, strongest first, and `goto` the
// chain of the fault's id like a lookup element does, so a packet that matches one never reaches the
// levels. An access rule stands where its place in the list is, and only its destination match is
// the set lookup. What a rule or fault selects is an address, so a CDN address that another name
// shares is selected too, and a device that does not use the gateway's DNS has no element at all
// (plan §2.6).

// The DNS-derived sets.
const (
	// DNSSetBase starts the name of every DNS-derived set; the executor's operation for their elements
	// accepts no other set (executor.DNSSetName).
	DNSSetBase = "dns_"
	// DNSSetType is the type of the sets: the requesting device, then the address in the answer.
	DNSSetType = "ipv4_addr . ipv4_addr"

	// hostSrcBase starts the name of the source set of a hostname fault: the addresses of the devices
	// it applies to.
	hostSrcBase = "hsrc_"
)

// DNSSet is one DNS-derived address set of the target: what the proxy reports for a hostname selector
// and where the engine's updates for it go.
type DNSSet struct {
	// ID is the opaque id the proxy reports a resolution under (the API's `set`): the same for a
	// selector as long as the selector exists, whatever the definition of the set is.
	ID string `json:"id"`
	// Name is the nftables set as the target holds it now (ID and a hash of the definition).
	Name string `json:"name"`
	// Pattern is the selector, in its canonical form (domain.HostnamePattern).
	Pattern string `json:"pattern"`
	// Sources are the clients whose resolutions of the name are recorded: the sources of the rules and
	// faults that use the selector. An address outside them is not written into the set (nothing would
	// look it up).
	Sources []netip.Prefix `json:"sources,omitempty"`
}

// DNSSetID is the id of the set of a hostname selector: DNSSetBase and ten hexadecimal digits of the
// hash of the canonical pattern. Nothing but the pattern is in it, so the proxy, the engine and the
// compiler agree on it without a table.
func DNSSetID(pattern string) string {
	h := sha256.Sum256([]byte(domain.HostnamePattern(pattern)))
	return DNSSetBase + hex.EncodeToString(h[:5])
}

// dnsSetFlags are the flags of every DNS-derived set: every element carries the lifetime it was
// written with (plan §2.6: max(DNS TTL, 10 min)).
var dnsSetFlags = []string{"timeout"}

// dnsSetName is the nftables name of a DNS-derived set: the id with a hash of the definition.
func dnsSetName(id string) string { return hashName(id, DNSSetType, dnsSetFlags) }

// hostBuild collects what the compilation of faults and rules needs from the DNS-derived sets: which
// selectors are in use and the clients each is used for, and the source sets of the hostname faults.
type hostBuild struct {
	sets    map[string]*dnsSetBuild // by canonical pattern
	srcSets map[string]SetDef       // by name
}

type dnsSetBuild struct {
	set     DNSSet
	sources []netip.Prefix
}

// hostSet registers the use of a hostname selector for the clients (the sources of a rule or a fault)
// and returns the set that holds its addresses.
func (t *Target) hostSet(pattern string, clients []netip.Prefix) DNSSet {
	if t.host == nil {
		t.host = &hostBuild{sets: map[string]*dnsSetBuild{}, srcSets: map[string]SetDef{}}
	}
	p := domain.HostnamePattern(pattern)
	b := t.host.sets[p]
	if b == nil {
		id := DNSSetID(p)
		b = &dnsSetBuild{set: DNSSet{ID: id, Name: dnsSetName(id), Pattern: p}}
		t.host.sets[p] = b
	}
	b.sources = append(b.sources, clients...)
	return b.set
}

// hostSourceSet registers the source set of a hostname fault (the addresses of the devices it applies
// to) and returns its name. The name is derived from the key of the fault, so a fault that stays keeps
// its set.
func (t *Target) hostSourceSet(key string, spans []aspan) string {
	if t.host == nil {
		t.host = &hostBuild{sets: map[string]*dnsSetBuild{}, srcSets: map[string]SetDef{}}
	}
	h := sha256.Sum256([]byte("hostsrc|" + key))
	s := SetDef{Type: "ipv4_addr", Flags: []string{"interval"}}
	s.Name = hashName(hostSrcBase+hex.EncodeToString(h[:6]), s.Type, s.Flags)
	for _, p := range spanPrefixes(spans) {
		s.Elements = append(s.Elements, p.String())
	}
	t.host.srcSets[s.Name] = s
	return s.Name
}

// spanPrefixes is the shortest list of prefixes that covers the spans.
func spanPrefixes(spans []aspan) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range spans {
		out = append(out, domain.IPRange{First: numAddr(s.lo), Last: numAddr(s.hi)}.Prefixes()...)
	}
	return out
}

// finishHostnames turns the registry into the sets of the target: Target.DNSSets for the engine and
// the proxy, and the definitions compileNft puts into the table (the dynamic sets of the selectors and
// the static source sets of the hostname faults).
func (t *Target) finishHostnames() {
	if t.host == nil {
		return
	}
	patterns := make([]string, 0, len(t.host.sets))
	for p := range t.host.sets {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	for _, p := range patterns {
		b := t.host.sets[p]
		b.set.Sources = domain.CollapsePrefixes(b.sources)
		t.DNSSets = append(t.DNSSets, b.set)
		t.hostSetDefs = append(t.hostSetDefs, SetDef{Name: b.set.Name, Type: DNSSetType, Flags: dnsSetFlags, Dynamic: true})
	}
	names := make([]string, 0, len(t.host.srcSets))
	for n := range t.host.srcSets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t.hostSetDefs = append(t.hostSetDefs, t.host.srcSets[n])
	}
}

// DNSSetOf returns the set of a hostname selector in the target.
func (t *Target) DNSSetOf(pattern string) (DNSSet, bool) {
	id := DNSSetID(pattern)
	for _, s := range t.DNSSets {
		if s.ID == id {
			return s, true
		}
	}
	return DNSSet{}, false
}

// hostLookup is the match of a DNS-derived set: the requesting device and the address the connection
// goes to, both of the conntrack original tuple. The responder form of a forwarded connection (plan
// §2.4, D34) looks up the device that answers (the reply source) and the peer that opened it.
func hostLookup(set string) any {
	return match(concat(ctOriginalIP("saddr"), ctOriginalIP("daddr")), "==", setRef(set))
}

func hostLookupResponder(set string) any {
	return match(concat(ctReplyIP("saddr"), ctOriginalIP("saddr")), "==", setRef(set))
}

// hostTraffic are the matches of the protocol and the ports of a hostname selector's traffic part.
func hostTraffic(m model.TrafficMatch) []any {
	var e []any
	proto := ""
	if m.Protocol != nil && *m.Protocol != "any" {
		proto = string(*m.Protocol)
		e = append(e, eq(meta("l4proto"), proto))
	}
	if proto == "tcp" || proto == "udp" {
		var ports []int
		if m.Ports != nil {
			ports = *m.Ports
		}
		var ranges []model.PortRange
		if m.PortRanges != nil {
			ranges = *m.PortRanges
		}
		if merged := mergePorts(ports, ranges); len(merged) > 0 {
			e = append(e, eq(ctOriginal("proto-dst"), portValue(merged)))
		}
	}
	return e
}

// hostRules are the rules a hostname fault stands for, before the id it goes to is known.
type hostRule struct {
	// key is the key of the fault id the rule goes to, "" for a winner that impairs nothing (mark_0).
	key string
	// expr are the matches of the rule; the goto follows.
	expr []any
}

// isHostname reports whether a candidate selects a hostname.
func isHostname(c domain.Candidate) bool {
	return c.Match.Destination != nil && c.Match.Destination.Hostname != nil
}

// hostFaultRules turns the hostname candidates of the sources' tables into the rules that stand in
// front of the lookup levels: one rule per fault (and per device, for a fault that has a queue for
// each), strongest first, matching the source, the traffic and the DNS-derived set of the selector.
// register is compileFaults' way to give a winner its fault key; it is called for every source the
// candidate applies to, so the key of a per-device fault names the device.
func (t *Target) hostFaultRules(tables []domain.Table, sources []domain.Source, register func(domain.Candidate, domain.Source) (string, bool)) ([]hostRule, bool) {
	type use struct {
		c    domain.Candidate
		srcs []int
	}
	uses := map[string]*use{}
	for i, tab := range tables {
		for _, c := range tab.Unresolved {
			k := baseKey(c)
			u := uses[k]
			if u == nil {
				u = &use{c: c}
				uses[k] = u
			}
			u.srcs = append(u.srcs, i)
		}
	}
	if len(uses) == 0 {
		return nil, true
	}
	keys := make([]string, 0, len(uses))
	for k := range uses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cands := make([]domain.Candidate, 0, len(keys))
	for _, k := range keys {
		cands = append(cands, uses[k].c)
	}
	domain.RankOrder(cands)

	spans := partitionSources(sources)
	var out []hostRule
	for _, c := range cands {
		bk := baseKey(c)
		u := uses[bk]
		byKey := map[string][]aspan{}
		var all []aspan
		for _, i := range u.srcs {
			if len(spans[i]) == 0 {
				continue // a source without an address has nothing to match yet
			}
			key, ok := register(c, sources[i])
			if !ok {
				return nil, false
			}
			byKey[key] = append(byKey[key], spans[i]...)
			all = append(all, spans[i]...)
		}
		if len(all) == 0 {
			continue
		}
		set := t.hostSet(*c.Match.Destination.Hostname, spanPrefixes(mergeAspans(all)))
		groups := make([]string, 0, len(byKey))
		for k := range byKey {
			groups = append(groups, k)
		}
		sort.Strings(groups)
		for _, k := range groups {
			sp := mergeAspans(byKey[k])
			var from any
			if strings.Contains(k, "@") {
				// a queue for each device: the rule names the device's own addresses
				from = prefixes(spanPrefixes(sp))
			} else {
				from = setRef(t.hostSourceSet(bk, sp))
			}
			expr := []any{eq(ctOriginalIP("saddr"), from)}
			expr = append(expr, hostTraffic(c.Match)...)
			expr = append(expr, hostLookup(set.Name))
			out = append(out, hostRule{key: k, expr: expr})
		}
	}
	return out, true
}
