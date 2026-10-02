package domain

import (
	"time"

	"net/netip"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// ---- traffic selectors ------------------------------------------------------------------

func protocolOf(m model.TrafficMatch) string {
	if m.Protocol == nil {
		return "any"
	}
	return string(*m.Protocol)
}

// match validates the destination part of a selector (plan §2.4).
func (v *validator) match(path string, m model.TrafficMatch) {
	proto := protocolOf(m)
	hasPorts := len(deref(m.Ports)) > 0 || len(deref(m.PortRanges)) > 0
	if hasPorts && proto != "tcp" && proto != "udp" {
		v.add(path+"/protocol", CodePortsRequireProtocol, "ports and port ranges need the protocol tcp or udp, not %s", proto)
	}
	for i, r := range deref(m.PortRanges) {
		if r.To < r.From {
			v.add(schema.Pointer(path+"/port_ranges", itoa(i)), CodeInvalidPortRange, "the range %d-%d ends before it starts", r.From, r.To)
		}
	}
	if m.Destination != nil && m.Destination.Cidr != nil {
		v.cidrOrAddr(path+"/destination/cidr", *m.Destination.Cidr)
	}
}

// cidrOrAddr checks an IPv4 address or a prefix with zero host bits.
func (v *validator) cidrOrAddr(path, s string) {
	if _, ok := parseAddr(s); ok {
		return
	}
	v.cidrOK(path, s)
}

func (v *validator) accessRuleBody(path string, b model.AccessRuleBody) {
	m := convert[model.TrafficMatch](b)
	v.match(path, m)
	proto := protocolOf(m)
	if b.Action == "reset" && proto != "tcp" {
		v.add(path+"/action", CodeResetRequiresTCP, "a TCP reset needs the protocol tcp, not %s", proto)
	}
	if b.CutExisting != nil && *b.CutExisting && b.Action == "allow" {
		v.add(path+"/cut_existing", CodeCutWithAllow, "cutting established connections only makes sense for drop, reject and reset")
	}
	if b.CutExisting != nil && *b.CutExisting && proto != "tcp" && proto != "any" {
		v.add(path+"/cut_existing", CodeCutRequiresTCP, "cutting established connections works on TCP; the rule's protocol is %s", proto)
	}
}

func (v *validator) accessRules() {
	rules := deref(v.cfg.AccessRules)
	for _, id := range sortedKeys(rules) {
		r := rules[id]
		path := schema.Pointer("/access_rules", id)
		v.accessRuleBody(path, convert[model.AccessRuleBody](r))
		v.scope(path+"/source", &r.Source, scopeOpts{})
	}

	// the order lists each rule exactly once
	seen := map[string]bool{}
	for i, uid := range deref(v.cfg.AccessRuleOrder) {
		p := schema.Pointer("/access_rule_order", itoa(i))
		id := uid.String()
		key := lower(id)
		switch _, exists := rules[key]; {
		case !exists:
			v.add(p, CodeUnknownReference, "there is no access rule %s", quote(id))
		case seen[key]:
			v.add(p, CodeRuleOrder, "the rule is listed twice")
		}
		seen[key] = true
	}
	for _, id := range sortedKeys(rules) {
		if !seen[id] {
			v.add("/access_rule_order", CodeRuleOrder, "the order lacks the rule %s", quote(id))
		}
	}
}

// ---- routing ----------------------------------------------------------------------------

// Routing tables that an external routing daemon must not write to: the system tables and the
// tables of Chaos Gateway itself (policy routing 100, PMTU mirrors, service namespace, plan
// §2.2, §3.3). The exact numbers of the last two are fixed in M4; this range leaves room.
const (
	reservedTableFirst = 100
	reservedTableLast  = 110
)

func reservedTable(t int) bool {
	return (t >= reservedTableFirst && t <= reservedTableLast) || t == 253 || t == 254 || t == 255
}

func (v *validator) routing() {
	r := v.cfg.Routing
	if r == nil {
		return
	}
	if r.RouterId != nil {
		if a, ok := parseAddr(*r.RouterId); !ok || a.IsUnspecified() || a.IsMulticast() {
			v.add("/routing/router_id", CodeInvalidAddress, "the router id must be a unicast IPv4 address")
		}
	}
	if r.External != nil && r.External.Enabled != nil && *r.External.Enabled {
		switch {
		case r.External.Table == nil:
			v.add("/routing/external/table", CodeMissingField, "external routing needs the kernel table to import from")
		case reservedTable(*r.External.Table):
			v.add("/routing/external/table", CodeInvalidTable, "table %d belongs to the system or to Chaos Gateway", *r.External.Table)
		}
	}
	if r.External != nil {
		v.importFilter("/routing/external/import", r.External.Import)
	}

	perLink := map[string]string{} // link + type
	for _, id := range sortedKeys(deref(r.Protocols)) {
		p := deref(r.Protocols)[id]
		path := schema.Pointer("/routing/protocols", id)
		v.protocol(path, p)
		key := p.Link + "/" + string(p.Type)
		if other, dup := perLink[key]; dup {
			v.add(path+"/type", CodeDuplicateRoutingKind, "link %q already runs a %s protocol (%s)", v.idx.NameOf(KindLink, p.Link), p.Type, other)
		}
		perLink[key] = path
	}
}

func (v *validator) protocol(path string, p model.RoutingProtocol) {
	// only the settings of the protocol's own type
	for name, present := range map[string]bool{"bgp": p.Bgp != nil, "ospf": p.Ospf != nil, "babel": p.Babel != nil} {
		if present && name != string(p.Type) {
			v.add(path+"/"+name, CodeProtocolSettings, "%s settings on a %s protocol", name, p.Type)
		}
	}
	link := v.idx.Networks[p.Link]
	switch p.Type {
	case "bgp":
		if p.Bgp == nil {
			v.add(path+"/bgp", CodeMissingField, "a BGP protocol needs its settings (at least the neighbor AS)")
		} else {
			v.bgp(path+"/bgp", *p.Bgp, link)
		}
		if v.cfg.Routing.Asn == nil {
			v.add("/routing/asn", CodeMissingField, "BGP needs the local AS number")
		}
	case "ospf":
		if p.Ospf != nil {
			hello := orDuration(p.Ospf.HelloInterval, 10*time.Second)
			dead := orDuration(p.Ospf.DeadInterval, 40*time.Second)
			if hello < time.Second {
				v.add(path+"/ospf/hello_interval", CodeTimers, "the hello interval must be at least 1s")
			}
			if dead <= hello {
				v.add(path+"/ospf/dead_interval", CodeTimers, "the dead interval %v must be longer than the hello interval %v", dead, hello)
			}
		}
	case "babel":
		if p.Babel != nil && orDuration(p.Babel.HelloInterval, 4*time.Second) < time.Second {
			v.add(path+"/babel/hello_interval", CodeTimers, "the hello interval must be at least 1s")
		}
	}
	for i, a := range deref(p.Announce) {
		if a.Cidr != nil {
			v.cidrOK(schema.Pointer(path+"/announce", itoa(i))+"/cidr", *a.Cidr)
		}
	}
	v.importFilter(path+"/import", p.Import)
}

func orDuration(d *model.Duration, def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	got, ok := parseDuration(*d)
	if !ok {
		return def
	}
	return got
}

func (v *validator) bgp(path string, b model.BgpSettings, link *NetInfo) {
	hold := orDuration(b.HoldTime, 90*time.Second)
	keep := orDuration(b.KeepaliveTime, 30*time.Second)
	if keep < time.Second {
		v.add(path+"/keepalive_time", CodeTimers, "the keepalive time must be at least 1s")
	}
	if hold < 3*keep {
		v.add(path+"/hold_time", CodeTimers, "the hold time %v must be at least three keepalive intervals (%v)", hold, 3*keep)
	}
	if b.NeighborAddress != nil && link != nil && link.IsLink() {
		subnet, _ := networkSubnet(link)
		gw, _ := parsePrefix(link.WG.Address)
		a, ok := parseAddr(*b.NeighborAddress)
		if !ok || !subnet.Contains(a) || a == gw.Addr() {
			v.add(path+"/neighbor_address", CodeOutsideSubnet, "the neighbor must be the peer in the link's transfer network %s", subnet)
		}
	}
}

func (v *validator) importFilter(path string, f *model.ImportFilter) {
	if f == nil {
		return
	}
	for i, e := range deref(f.AllowedPrefixes) {
		p := schema.Pointer(path+"/allowed_prefixes", itoa(i))
		pre, ok := v.cidrOK(p+"/prefix", e.Prefix)
		if ok && e.MaxLength != nil && *e.MaxLength < pre.Bits() {
			v.add(p+"/max_length", CodeInvalidPrefixLength, "max_length %d is shorter than the prefix %s", *e.MaxLength, pre)
		}
	}
}

var _ = netip.Prefix{}
