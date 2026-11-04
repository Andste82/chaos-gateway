package compiler

import (
	"net/netip"
	"reflect"
	"sort"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the time family (plan §2.4, §2.18, M34, decisions D36 and D37). A time fault is behavior of the gateway's NTP
// server, which "keeps no state of its own" like the DNS proxy: it asks the API for the resolved faults (GET
// /internal/ntp/config) and answers a query with the entry of the client's sources and the port the query arrived on. The
// family resolves into two things:
//
//   - what the server answers (NTPFault): the precedence of §2.4 is applied here, per group of sources with the same ranked
//     list of faults and per port the queries of the group can arrive on (domain.TimeWinner), so the server decides nothing;
//   - what the kernel sends to the server (the NAT rules of ntpFaultRules, ntpGatewayRedirect and ntpHardcodedRedirect): UDP
//     to the NTP port is DNAT-ed to the namespace's peer address. The server cannot read the server the device asked (UDP
//     has no SO_ORIGINAL_DST, and conntrack needs CAP_NET_ADMIN, which its container does not have, D36), so every distinct
//     destination selector of an active fault is redirected to a port of its own and the server learns the destination from
//     the port. The reply leaves the namespace from that port and conntrack puts the original server's address and port back.
//
// A scope with an active time fault is redirected whether or not its network redirects hardcoded servers (Q3).

const (
	// ServiceNTPPort is the port of the NTP server inside the namespace: the gateway address, a scope without a destination
	// selector and the fallback of everything the compiler does not redirect separately arrive here.
	ServiceNTPPort = 123
	// NTPRedirectPortBase is the first of the ports of the destination selectors, one each: a selector keeps its port while
	// it is in use (allocateNTPPorts).
	NTPRedirectPortBase = 12301
	// MaxNTPDestinations is the number of distinct destination selectors that get a port of their own (D36).
	MaxNTPDestinations = 32
	// NTSKEPort is the TCP port of NTS key establishment (RFC 8915): the server cannot answer it and reports a connection
	// that arrives (plan §2.18), so the redirects that lead NTP to it lead this port, too.
	NTSKEPort = domain.NTSKEPort
	// CodeNTPDestinations is the problem code of more distinct destination selectors than MaxNTPDestinations.
	CodeNTPDestinations = "ntp_destinations"
)

// NTPFault is one entry of the server's list of resolved faults: the fault that decides for the clients of its Sources when
// a query arrives on its Port.
type NTPFault struct {
	// Key is `layer:id:time` like the keys of the other families: the id is the overlay's.
	Key string `json:"key"`
	// Overlay is the overlay (the activation, for the part of a profile); Profile the name of the profile.
	Overlay string `json:"overlay"`
	Profile string `json:"profile,omitempty"`
	// Scope says where the fault applies, for messages.
	Scope string `json:"scope"`
	// Fault is the fault as it was written.
	Fault model.TimeFault `json:"fault"`
	// Since is the instant the fault took effect: the updated_at of the overlay. `absolute` and a skew run from it.
	Since time.Time `json:"since"`
	// Sources are the clients the entry is for: the addresses of the devices and the stretches of a network that have
	// exactly the same time faults in the same order.
	Sources []netip.Prefix `json:"sources"`
	// Port is the port of the server the entry is for: ServiceNTPPort, or the port of the destination selector.
	Port int `json:"port"`
}

// NTPListenPorts are the UDP ports the server listens on: the NTP port and the redirect ports of the selectors in use.
func (t *Target) NTPListenPorts() []int {
	return append([]int{ServiceNTPPort}, t.NTPRedirectPorts...)
}

// ntpIface is an interface the clients of the test networks come in on: a bridge or a WireGuard interface.
type ntpIface struct {
	dev     string
	gateway netip.Addr
	// covers are the addresses of the sources that arrive here and of what stays inside: the network and the networks
	// behind it
	covers []netip.Prefix
	// service says that the network's time service is on: the gateway address is redirected
	service bool
	// hardcoded says that the network redirects every NTP server
	hardcoded bool
}

// ntpIfaces lists the interfaces of the test networks, the bridges first.
func (t *Target) ntpIfaces() []ntpIface {
	var out []ntpIface
	for _, b := range t.Bridges {
		covers := []netip.Prefix{b.Address.Masked()}
		for _, r := range b.Routes {
			covers = append(covers, parseAddrOrPrefix(r)...)
		}
		out = append(out, ntpIface{dev: b.Name, gateway: b.Address.Addr(), covers: domain.CollapsePrefixes(covers), service: b.NTP, hardcoded: b.RedirectNTP})
	}
	for _, w := range t.WireGuard {
		covers := []netip.Prefix{w.Address.Masked()}
		for _, p := range w.Peers {
			// the routes and not the allowed addresses: a link may allow 0.0.0.0/0, which is no network behind it
			for _, r := range p.Routes {
				covers = append(covers, parseAddrOrPrefix(r)...)
			}
		}
		// a WireGuard client has no DHCP, but it reaches the server on the tunnel address like a device of a bridge
		out = append(out, ntpIface{dev: w.Name, gateway: w.Address.Addr(), covers: domain.CollapsePrefixes(covers), service: true})
	}
	return out
}

func parseAddrOrPrefix(s string) []netip.Prefix {
	if p, err := netip.ParsePrefix(s); err == nil {
		return []netip.Prefix{p.Masked()}
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return []netip.Prefix{netip.PrefixFrom(a, a.BitLen())}
	}
	return nil
}

// ntpPorts is the allocation of the redirect ports: the destination selectors by key, and the keys that got no port.
type ntpPorts struct {
	port     map[string]int
	overflow map[string]bool
}

// redirected is the question domain.TimeWinner asks for the entry of one port: does the selector arrive on this port. A
// selector without a port of its own is served like a fault without a selector (D36), so it counts on every port.
func (p ntpPorts) redirected(port int) func(string) bool {
	return func(key string) bool { return p.overflow[key] || p.port[key] == port }
}

// compileNTP builds the server's list and the NAT rules of the fault redirects from the time faults that apply.
func (t *Target) compileNTP(in Input, w *domain.World, sources []domain.Source, idx *domain.Index) {
	groups := groupCandidates(sources, w.TimeCandidates)
	if len(groups) == 0 {
		return
	}
	// the selectors in use, over all groups, and their ports in the order of the key: a port depends on the set of
	// selectors alone, not on which devices have a fault
	byKey := map[string]domain.Candidate{}
	var keys []string
	for _, g := range groups {
		for _, c := range g.cands {
			if k := domain.TimeSelectorKey(*c.Time); k != "" {
				if _, ok := byKey[k]; !ok {
					byKey[k] = c
					keys = append(keys, k)
				}
			}
		}
	}
	ports := allocateNTPPorts(keys, in.NTPPorts)
	t.NTPPorts = ports.port
	for _, p := range ports.port {
		t.NTPRedirectPorts = append(t.NTPRedirectPorts, p)
	}
	sort.Ints(t.NTPRedirectPorts)
	if len(ports.overflow) > 0 {
		t.warn(CodeNTPDestinations, "", "the time faults name %d different destinations, but the NTP server tells at most %d apart: the %d faults for the rest are served like a fault without a destination",
			len(keys), MaxNTPDestinations, len(ports.overflow))
	}
	// the most specific sources first: the server and the kernel both take the first entry that matches, and the stretch of a
	// network contains the addresses of its devices
	groups = bySpecificity(groups)

	seen := map[string]bool{}
	for _, g := range groups {
		clients := domain.CollapsePrefixes(g.prefixes)
		// the ports the queries of the group can arrive on, 123 first
		groupPorts := []int{ServiceNTPPort}
		for _, c := range g.cands {
			if p, ok := ports.port[domain.TimeSelectorKey(*c.Time)]; ok && !containsInt(groupPorts, p) {
				groupPorts = append(groupPorts, p)
			}
		}
		sort.Ints(groupPorts[1:])
		for _, port := range groupPorts {
			c, ok := domain.TimeWinner(g.cands, ports.redirected(port))
			if !ok {
				continue
			}
			t.NTPFaults = append(t.NTPFaults, NTPFault{Key: c.Key(), Overlay: c.ID, Profile: c.ProfileName, Scope: describeScope(idx, c.Scope),
				Fault: *c.Time, Since: c.Since, Sources: clients, Port: port})
			if !seen[c.Key()] {
				seen[c.Key()] = true
				t.Winners = append(t.Winners, c.Key())
				t.Effective = append(t.Effective, EffectiveFault{Key: c.Key(), Layer: string(c.Layer), Source: c.ID, Family: c.Family,
					Profile: c.ProfileName, Scope: describeScope(idx, c.Scope), Summary: c.Summary()})
			}
		}
	}
	sort.Strings(t.Winners)
	if t.Service == nil {
		t.warn(CodeUnsupported, "", "there are time faults, which need the gateway's NTP server: there is no service namespace, so nothing is redirected")
		return
	}
	ifaces := t.ntpIfaces()
	for _, g := range groups {
		t.ntpFaultRules(ifaces, g, ports)
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// allocateNTPPorts gives every destination selector in use a port. A selector that had a port in the allocation of the last
// compile (Input.NTPPorts) keeps it, and a new one takes the lowest free port in the order of its key (not one that a selector
// released in the last compile, while another is free), so a selector that comes or goes moves no other: a device whose flow was redirected to a port is never answered under the fault of another
// selector (D36). The selectors beyond MaxNTPDestinations get no port.
func allocateNTPPorts(keys []string, prev map[string]int) ntpPorts {
	sort.Strings(keys)
	ports := ntpPorts{port: map[string]int{}, overflow: map[string]bool{}}
	taken := map[int]bool{}
	for _, k := range keys {
		if p, ok := prev[k]; ok && p >= NTPRedirectPortBase && p < NTPRedirectPortBase+MaxNTPDestinations && !taken[p] {
			ports.port[k] = p
			taken[p] = true
		}
	}
	// The port of a selector that has gone is kept away from a new one for this compile: the server learns the new
	// configuration some time after the kernel has the new rules, and a device whose query for the new selector meets the
	// old entry of that port would be answered under the fault of the selector that was removed. Only when no other port is
	// left is a freed one used.
	released := map[int]bool{}
	for _, p := range prev {
		if p >= NTPRedirectPortBase && p < NTPRedirectPortBase+MaxNTPDestinations && !taken[p] {
			released[p] = true
		}
	}
	lowest := func(avoidReleased bool) (int, bool) {
		for p := NTPRedirectPortBase; p < NTPRedirectPortBase+MaxNTPDestinations; p++ {
			if !taken[p] && (!avoidReleased || !released[p]) {
				return p, true
			}
		}
		return 0, false
	}
	for _, k := range keys {
		if _, ok := ports.port[k]; ok {
			continue
		}
		next, ok := lowest(true)
		if !ok {
			next, ok = lowest(false)
		}
		if !ok {
			ports.overflow[k] = true
			continue
		}
		ports.port[k] = next
		taken[next] = true
	}
	return ports
}

// bySpecificity orders the groups of sources so that a group whose clients lie inside a client prefix of another group
// comes before it; otherwise the order is kept. The sources of a group are address ranges (a device, a stretch of a
// network), and an address can be in the ranges of two groups: the network's stretch contains the device that has a fault of
// its own. The more specific group is the one the fault of the more specific scope belongs to.
func bySpecificity(groups []*candGroup) []*candGroup {
	clients := make([][]netip.Prefix, len(groups))
	for i, g := range groups {
		clients[i] = domain.CollapsePrefixes(g.prefixes)
	}
	// inside reports whether some prefix of a lies strictly inside some prefix of b
	inside := func(a, b []netip.Prefix) bool {
		for _, p := range a {
			for _, q := range b {
				if p.Bits() > q.Bits() && q.Contains(p.Addr()) {
					return true
				}
			}
		}
		return false
	}
	done := make([]bool, len(groups))
	out := make([]*candGroup, 0, len(groups))
	for len(out) < len(groups) {
		pick := -1
		for i := range groups {
			if done[i] {
				continue
			}
			if pick < 0 {
				pick = i // the first one left is the fallback when the containments form a cycle
			}
			blocked := false
			for j := range groups {
				if j != i && !done[j] && inside(clients[j], clients[i]) {
					blocked = true
					break
				}
			}
			if !blocked {
				pick = i
				break
			}
		}
		done[pick] = true
		out = append(out, groups[pick])
	}
	return out
}

// ntpDnat is the translation of a redirect to the server: the namespace's address and the port.
func (t *Target) ntpDnat(port int) any {
	return map[string]any{"dnat": map[string]any{"family": "ip", "addr": t.Service.PeerCIDR.Addr().String(), "port": port}}
}

// ntpFaultRules adds the redirects of one group of sources: for each destination selector of its list, in the order of the
// list, the queries that match it to the port of the selector; then, when some fault of the list has no selector (or none
// that got a port), all NTP of the group to 123. The first rule that matches decides, and the order of the list is the order
// of the ranks, so a query arrives on the port of the first fault whose selector matches the server asked.
//
// What stays inside the segments of the network (the network and the networks behind its routers) is switched or routed
// back out of the interface and is never ours to redirect (plan §2.2); the gateway's own address lies inside the network and
// has a rule of its own where the redirect would otherwise miss it.
func (t *Target) ntpFaultRules(ifaces []ntpIface, g *candGroup, ports ntpPorts) {
	type selector struct {
		cand domain.Candidate
		port int
	}
	var sels []selector
	var anyServer bool
	done := map[string]bool{}
	for _, c := range g.cands {
		k := domain.TimeSelectorKey(*c.Time)
		switch {
		case k == "" || ports.overflow[k]:
			anyServer = true
		case !done[k]:
			done[k] = true
			sels = append(sels, selector{c, ports.port[k]})
		}
	}
	clients := domain.CollapsePrefixes(g.prefixes)
	for _, ifc := range ifaces {
		var mine []netip.Prefix
		for _, p := range clients {
			for _, cv := range ifc.covers {
				if p.Overlaps(cv) {
					mine = append(mine, p)
					break
				}
			}
		}
		if len(mine) == 0 {
			continue
		}
		from := []any{iifname(ifc.dev), eq(payload("ip", "saddr"), prefixes(mine))}
		for _, s := range sels {
			t.ntpSelectorRules(ifc, from, mine, s.cand, s.port)
		}
		if anyServer {
			dnat := t.ntpDnat(ServiceNTPPort)
			dport := eq(payload("udp", "dport"), ServiceNTPPort)
			if !ifc.service {
				// the gateway's address of a network whose time service is off: no rule follows for it
				t.ntpFaultNAT = append(t.ntpFaultNAT, newRule(append(append([]any(nil), from...), eq(payload("ip", "daddr"), ifc.gateway.String()), dport, dnat)...))
			}
			t.ntpFaultNAT = append(t.ntpFaultNAT, newRule(append(append([]any(nil), from...), match(payload("ip", "daddr"), "!=", prefixes(ifc.covers)), dport, dnat)...))
		}
	}
}

// ntpSelectorRules redirects the queries one destination selector matches to its port.
func (t *Target) ntpSelectorRules(ifc ntpIface, from []any, clients []netip.Prefix, c domain.Candidate, port int) {
	ps, ranges := domain.TimePorts(*c.Time)
	dport := eq(payload("udp", "dport"), portValue(mergePorts(ps, ranges)))
	dnat := t.ntpDnat(port)
	rule := func(extra ...any) {
		parts := append(append([]any(nil), from...), extra...)
		t.ntpFaultNAT = append(t.ntpFaultNAT, newRule(append(parts, dport, dnat)...))
	}
	notInside := match(payload("ip", "daddr"), "!=", prefixes(ifc.covers))
	d := c.Match.Destination
	switch {
	case d == nil:
		// every server on those ports: the gateway's address as well, which lies inside the network
		rule(eq(payload("ip", "daddr"), ifc.gateway.String()))
		rule(notInside)
	case d.Hostname != nil:
		rule(hostLookup(t.hostSet(*d.Hostname, clients).Name), notInside)
	case d.Cidr != nil:
		var dst netip.Prefix
		if a, err := netip.ParseAddr(*d.Cidr); err == nil {
			dst = netip.PrefixFrom(a, a.BitLen())
		} else if p, err := netip.ParsePrefix(*d.Cidr); err == nil {
			dst = p.Masked()
		} else {
			return
		}
		var touches, within bool
		for _, cv := range ifc.covers {
			touches = touches || dst.Overlaps(cv)
			within = within || (cv.Bits() <= dst.Bits() && cv.Contains(dst.Addr()))
		}
		if !touches {
			rule(eq(payload("ip", "daddr"), prefixValue(dst)))
			return
		}
		if dst.Contains(ifc.gateway) {
			rule(eq(payload("ip", "daddr"), ifc.gateway.String()))
		}
		// everything the selector names lies inside the network when it is within it: only the gateway's address is ours
		if !within {
			rule(eq(payload("ip", "daddr"), prefixValue(dst)), notInside)
		}
	}
	// network and uplink are no destinations of a time fault: the validation refuses them, and a rule here would select
	// more than the fault says
}

// ntpGatewayRedirect sends the queries to a network's gateway address (UDP 123, and TCP 4460 of NTS) to the server in the
// namespace, for the networks whose time service is on (and the WireGuard networks). The packets keep the client's source
// address, so the server sees the device.
func (t *Target) ntpGatewayRedirect() []Rule {
	var rules []Rule
	for _, ifc := range t.ntpIfaces() {
		if !ifc.service {
			continue
		}
		gw := eq(payload("ip", "daddr"), ifc.gateway.String())
		rules = append(rules,
			newRule(iifname(ifc.dev), gw, eq(payload("udp", "dport"), ServiceNTPPort), t.ntpDnat(ServiceNTPPort)),
			newRule(iifname(ifc.dev), gw, eq(payload("tcp", "dport"), NTSKEPort), t.ntpDnat(NTSKEPort)))
	}
	return rules
}

// ntpHardcodedRedirect sends NTP (and NTS key establishment) to other servers than the gateway to the server for the networks
// that ask for it (`ntp.redirect_hardcoded`), after the rules for the gateway's own address and for the faults. What stays
// inside the segments of the network is never redirected.
func (t *Target) ntpHardcodedRedirect() []Rule {
	var rules []Rule
	for _, ifc := range t.ntpIfaces() {
		if !ifc.hardcoded {
			continue
		}
		other := match(payload("ip", "daddr"), "!=", prefixes(ifc.covers))
		rules = append(rules,
			newRule(iifname(ifc.dev), other, eq(payload("udp", "dport"), ServiceNTPPort), t.ntpDnat(ServiceNTPPort)),
			newRule(iifname(ifc.dev), other, eq(payload("tcp", "dport"), NTSKEPort), t.ntpDnat(NTSKEPort)))
	}
	return rules
}

// ntpRedirect is the NAT rules that lead NTP into the namespace: the redirects of the faults (the ports of the selectors
// first), then the gateway's address, then the hardcoded servers of the networks that ask. Nil without a service namespace.
func (t *Target) ntpRedirect() []Rule {
	if t.Service == nil {
		return nil
	}
	rules := append([]Rule(nil), t.ntpFaultNAT...)
	rules = append(rules, t.ntpGatewayRedirect()...)
	return append(rules, t.ntpHardcodedRedirect()...)
}

// NTPRedirectChanged reports whether the NAT rules that lead NTP into the server differ between two targets. The tracked
// flows of the clients keep the decision of the first packet (the NAT chain sees no other), so the engine deletes them when
// this is true (engine/ntpflows.go).
func NTPRedirectChanged(a, b *Target) bool {
	return !reflect.DeepEqual(a.ntpRedirect(), b.ntpRedirect())
}

// ntpForward accepts the packets that were redirected to the server: the UDP ports it listens on and the TCP port of NTS
// key establishment, which it refuses. The accept stands where the DNS accept stands (serviceForward), after the access rules,
// which judged the original tuple.
func (t *Target) ntpForward(ifsCG string) []Rule {
	if t.Service == nil {
		return nil
	}
	peer := eq(payload("ip", "daddr"), t.Service.PeerCIDR.Addr().String())
	var udp []PortRange
	for _, p := range t.NTPListenPorts() {
		udp = append(udp, PortRange{p, p})
	}
	return []Rule{
		newRule(iifSet(ifsCG), oifname(t.Service.HostIf), peer, eq(payload("udp", "dport"), portValue(udp)), verdict("accept")),
		newRule(iifSet(ifsCG), oifname(t.Service.HostIf), peer, eq(payload("tcp", "dport"), NTSKEPort), verdict("accept")),
	}
}
