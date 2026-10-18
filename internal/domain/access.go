package domain

import (
	"net/netip"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file answers "would the gateway forward this packet" at the level the compiler's forward
// chain decides it (plan §2.2): the gateway's own protection for traffic to the gateway, then the
// access matrix between networks, clients, the management network and the uplink. It mirrors
// compiler.matrixRules (the more specific endpoint first, explicit entries before what a client's
// `reachable` list implies, then the default) and is used by `explain`. AccessVerdict is that level;
// AccessDecision puts the access rules (M9) in front of it, in the order of the packet path.

// AccessFacts are what the verdict needs to know about the gateway besides the configuration.
type AccessFacts struct {
	// Gateway are the addresses the gateway itself answers on (bridge and WireGuard addresses):
	// traffic to them is judged by the gateway's protection, not by the matrix.
	Gateway []netip.Addr
	// Management are the prefixes of the management network as the kernel tells it: the allowed
	// sources (the set the anti-lockout rule, the matrix endpoint `management` and the two-port guard
	// match).
	Management []netip.Prefix
	// ManagementSubnet is the management interface's subnet in the three-port topology: its guard
	// drops it as well, whether or not the allowed sources cover it (unset in the two-port topology,
	// where it is the uplink's subnet).
	ManagementSubnet netip.Prefix
	// TwoPort reports that the management network lies behind the uplink interface (plan §2.16). In
	// both topologies devices under test do not reach the management network, whatever an access rule
	// says, unless an explicit matrix entry allows it; TwoPort changes only the words of the reason.
	TwoPort bool
	// UIPort is the port of the UI and API: the gateway answers it for the management sources only,
	// and no access rule changes that (0: unknown).
	UIPort int
}

// AccessLayers of an explanation, as the spec names them.
const (
	AccessGatewayProtection = "gateway_protection"
	AccessMatrix            = "access_matrix"
	AccessOverlayRule       = "overlay_rule"
	AccessConfigRule        = "config_rule"
	// AccessPortForward is a forwarded connection that no access rule decided: the forward accepts it
	// in front of the access matrix (M35a).
	AccessPortForward = "port_forward"
)

// AccessExplanation is the verdict and what decided it.
type AccessExplanation struct {
	// Verdict is allow or drop; with access rules it is also reject or reset.
	Verdict string
	// Layer is AccessGatewayProtection, AccessMatrix, AccessOverlayRule or AccessConfigRule.
	Layer string
	// Reason says in words which entry or default decided.
	Reason string
	// Rule is the id of the access rule that decided and RuleName its name (a configured rule's);
	// empty when no rule did.
	Rule     string
	RuleName string
}

// endpoint ranks of the compiler: the more specific one is evaluated first. rankMgmtWG is the endpoint
// `management` as it stands for the WireGuard networks of the role management (P31-AUDIT-11): after the
// networks, so an entry from or towards such a network or one of its clients decides first.
const (
	rankClient = iota
	rankManagement
	rankUplink
	rankNetwork
	rankMgmtWG
	rankCount
)

type matrixEndpoint struct {
	kind string // client, network, management, uplink
	// id is the network's or the client's id; for the management network it is mgmtViaWG when the address
	// lies in a WireGuard network of the role management (or behind one of its clients), where the compiler
	// matches the endpoint by that network's interface
	id string
}

// mgmtViaWG marks the management endpoint of an address in a WireGuard network of the role management.
const mgmtViaWG = "wireguard"

func (e matrixEndpoint) rank() int {
	switch e.kind {
	case "client":
		return rankClient
	case "management":
		if e.id == mgmtViaWG {
			return rankMgmtWG
		}
		return rankManagement
	case "uplink":
		return rankUplink
	}
	return rankNetwork
}

// endpointsOf returns the matrix endpoints an address belongs to: the networks whose prefixes
// contain it, the client whose tunnel address or networks do, the management network, and the uplink
// when it is none of the others.
func (w *World) endpointsOf(ip netip.Addr, mgmt []netip.Prefix) []matrixEndpoint {
	var out []matrixEndpoint
	known := false
	for _, id := range sortedKeys(w.Index.Networks) {
		for _, p := range w.prefixes[id] {
			if p.Contains(ip) {
				out = append(out, matrixEndpoint{"network", id})
				known = true
				break
			}
		}
	}
	for _, nid := range sortedKeys(w.Index.Networks) {
		n := w.Index.Networks[nid]
		if !n.IsHub() {
			continue
		}
		for _, cid := range sortedKeys(deref(n.WG.Clients)) {
			c := deref(n.WG.Clients)[cid]
			match := false
			if a, err := netip.ParseAddr(c.Address); err == nil && a == ip {
				match = true
			}
			for _, cn := range deref(c.ClientNetworks) {
				if p, ok := parsePrefix(cn); ok && p.Contains(ip) {
					match = true
				}
			}
			if match {
				out = append(out, matrixEndpoint{"client", cid})
				known = true
			}
		}
	}
	for _, p := range mgmt {
		if p.Contains(ip) {
			out = append(out, matrixEndpoint{"management", ""})
			known = true
			break
		}
	}
	if !known {
		out = append(out, matrixEndpoint{"uplink", ""})
	}
	return out
}

// matrixEndpoints returns the matrix endpoints of a source or a destination: those of endpointsOf, where an address in a WireGuard network of the role management or behind one of its clients
// belongs to the management network by that network's interface (mgmtViaWG), whether or not the allowed
// sources contain it. Those networks are part of the management network for the guard and for the matrix
// endpoint `management`, as a destination and as a source, in both topologies (P31-AUDIT-11, decided
// 2026-10-18), as the compiler has it: it matches them by `oifname` and `iifname`, ranked after the networks,
// and leaves them out of the endpoint's other matches (the management interface, the allowed sources).
func (w *World) matrixEndpoints(ip netip.Addr, f AccessFacts) []matrixEndpoint {
	eps := w.endpointsOf(ip, f.Management)
	via := false
	for _, e := range eps {
		via = via || w.inMgmtWG(e)
	}
	if !via {
		return eps
	}
	var out []matrixEndpoint
	for _, e := range eps {
		if e.kind != "management" {
			out = append(out, e)
		}
	}
	return append(out, matrixEndpoint{"management", mgmtViaWG})
}

// findEndpoint returns the element of set that the endpoint of an entry matches: the management endpoint
// matches the management network however the address belongs to it.
func findEndpoint(set []matrixEndpoint, e matrixEndpoint) (matrixEndpoint, bool) {
	for _, x := range set {
		if x == e || (e.kind == "management" && x.kind == "management") {
			return x, true
		}
	}
	return matrixEndpoint{}, false
}

// inMgmtWG reports whether an endpoint is a WireGuard network of the role management or a client of one.
func (w *World) inMgmtWG(e matrixEndpoint) bool {
	mgmtRole := func(n *NetInfo) bool {
		return n != nil && n.WG != nil && n.WG.Role != nil && *n.WG.Role == model.WireGuardNetworkRoleManagement
	}
	switch e.kind {
	case "network":
		return mgmtRole(w.Index.Networks[e.id])
	case "client":
		for _, nid := range sortedKeys(w.Index.Networks) {
			if n := w.Index.Networks[nid]; n.IsHub() && mgmtRole(n) {
				if _, ok := deref(n.WG.Clients)[e.id]; ok {
					return true
				}
			}
		}
	}
	return false
}

func endpointOf(e model.MatrixEndpoint) (matrixEndpoint, bool) {
	switch {
	case e.Client != nil:
		return matrixEndpoint{"client", lower(*e.Client)}, true
	case e.Network != nil:
		return matrixEndpoint{"network", lower(*e.Network)}, true
	case e.Management != nil && bool(*e.Management):
		return matrixEndpoint{"management", ""}, true
	case e.Uplink != nil && bool(*e.Uplink):
		return matrixEndpoint{"uplink", ""}, true
	}
	return matrixEndpoint{}, false
}

func containsEndpoint(set []matrixEndpoint, e matrixEndpoint) bool {
	for _, x := range set {
		if x == e {
			return true
		}
	}
	return false
}

// AccessDecision decides what the gateway does with the first packet of a connection described by q
// (the source, the destination address and the original port), in the order of the packet path
// (plan §2.2): towards the gateway itself the control plane comes first (the management sources reach
// SSH and the UI port, the UI port is closed to everybody else), then the access rules, then the
// gateway's protection of the test networks; towards anything else the access rules, then the access
// matrix. The first access rule that selects the traffic decides, overlay rules before configured
// ones. A drop, reject or reset rule refuses; an allow rule accepts: in the forward path it is an
// exception to the matrix, in the input path it also opens the gateway's closed ports for its
// selector (decided by the maintainer on 2026-10-11). The one exception to the exception is the
// management guard: it stands in front of the rules and no rule opens the management network for a
// device under test, behind the uplink (P2-M9-11) and behind a management interface of its own
// (P31-M35a-10, decided on 2026-10-17). The access rules select the
// test, WireGuard and remote networks, so a management source is judged by the control-plane rule
// and the protection's default only. q.Source.IP and q.DestIP must be set.
func (w *World) AccessDecision(q Query, f AccessFacts) AccessExplanation {
	src, dst := q.Source.IP, q.DestIP
	toGateway := false
	for _, g := range f.Gateway {
		if g == dst {
			toGateway = true
		}
	}
	inMgmt := false
	for _, p := range f.Management {
		if p.Contains(src) {
			inMgmt = true
		}
	}
	if toGateway {
		switch {
		case inMgmt:
			return w.AccessVerdict(src, dst, q.Protocol, q.Port, f)
		case f.UIPort > 0 && q.Port == f.UIPort && (q.Protocol == "tcp" || q.Protocol == ""):
			return AccessExplanation{Verdict: "drop", Layer: AccessGatewayProtection, Reason: "the UI and API are reachable from the management network only; no access rule changes that"}
		}
	}
	// the management guard stands in front of the rules, as in the forward chain: no rule, an allow
	// rule included, opens the management network for a device under test
	if !toGateway {
		if g, ok := w.mgmtGuard(src, dst, f); ok {
			return g
		}
	}
	r := w.ResolveAccess(q)
	if r.Matched {
		layer := AccessConfigRule
		if r.Layer == LayerOverlay {
			layer = AccessOverlayRule
		}
		label := "the " + string(r.Layer) + " rule " + r.RuleID
		if r.Name != "" {
			label = "the rule " + r.Name + " (" + string(r.Layer) + ", " + r.RuleID + ")"
		}
		switch {
		case r.Action != "allow":
			return AccessExplanation{Verdict: r.Action, Layer: layer, Reason: label + " " + ruleVerb(r) + " this traffic", Rule: r.RuleID, RuleName: r.Name}
		case toGateway:
			return AccessExplanation{Verdict: "allow", Layer: layer, Reason: label + " allows this traffic, which opens the gateway's port for it (the gateway's protection of the test networks is not asked)", Rule: r.RuleID, RuleName: r.Name}
		}
		return AccessExplanation{Verdict: "allow", Layer: layer, Reason: label + " allows this traffic before the access matrix is asked", Rule: r.RuleID, RuleName: r.Name}
	}
	return w.AccessVerdict(src, dst, q.Protocol, q.Port, f)
}

// rejectWords says in words what an ICMP type of a reject is.
var rejectWords = map[string]string{
	string(model.PortUnreachable): "port unreachable",
	string(model.HostUnreachable): "host unreachable",
	string(model.AdminProhibited): "administratively prohibited",
	string(model.NoRoute):         "no route",
}

func ruleVerb(r AccessResult) string {
	switch r.Action {
	case "reject":
		with := r.RejectWith
		if with == "" {
			with = string(model.PortUnreachable)
		}
		return "rejects (ICMP " + rejectWords[with] + ")"
	case "reset":
		return "resets (TCP reset)"
	}
	return "drops"
}

// AccessVerdict decides what the gateway does with a packet from src to dst. The protocol ("tcp",
// "udp", "icmp" or "") and the port matter only for traffic to the gateway itself.
func (w *World) AccessVerdict(src, dst netip.Addr, protocol string, port int, f AccessFacts) AccessExplanation {
	inMgmt := func(ip netip.Addr) bool {
		for _, p := range f.Management {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}
	for _, g := range f.Gateway {
		if g != dst {
			continue
		}
		// the input chain: management sources reach the control plane; test networks are answered
		// only for DHCP, DNS and ICMP echo
		switch {
		case inMgmt(src) && (protocol == "tcp" || protocol == "") && (port == 22 || (f.UIPort > 0 && port == f.UIPort)):
			// the anti-lockout rule: exactly SSH and the UI port, which no access rule can take away
			return AccessExplanation{Verdict: "allow", Layer: AccessGatewayProtection, Reason: "management sources always reach SSH and the UI/API of the gateway (the anti-lockout rule)"}
		case inMgmt(src):
			// the protection filters the test networks only: a management source on any other
			// port is answered by the policy of the input chain (and by the services behind it)
			return AccessExplanation{Verdict: "allow", Layer: AccessGatewayProtection, Reason: "the gateway's protection covers the test networks only; a management source is not filtered outside SSH and the UI/API (nothing is guaranteed there)"}
		case protocol == "icmp", port == 53 && (protocol == "udp" || protocol == "tcp" || protocol == ""), port == 67 && (protocol == "udp" || protocol == ""):
			return AccessExplanation{Verdict: "allow", Layer: AccessGatewayProtection, Reason: "the gateway answers ICMP echo, DNS and DHCP on a test network"}
		}
		return AccessExplanation{Verdict: "drop", Layer: AccessGatewayProtection, Reason: "a test network reaches only ICMP echo, DNS and DHCP on the gateway"}
	}
	if g, ok := w.mgmtGuard(src, dst, f); ok {
		return g
	}
	from, to := w.matrixEndpoints(src, f), w.matrixEndpoints(dst, f)

	type entry struct {
		rank     int
		implicit bool
		allow    bool
		why      string
	}
	var items []entry
	add := func(a, b model.MatrixEndpoint, policy model.MatrixEntryPolicy, implicit bool, why string) {
		fe, ok1 := endpointOf(a)
		te, ok2 := endpointOf(b)
		if !ok1 || !ok2 {
			return
		}
		fm, ok1 := findEndpoint(from, fe)
		tm, ok2 := findEndpoint(to, te)
		if !ok1 || !ok2 {
			return
		}
		items = append(items, entry{rank: fm.rank()*rankCount + tm.rank(), implicit: implicit, allow: policy != model.MatrixEntryPolicyDeny, why: why})
	}
	if w.Config.AccessMatrix != nil {
		for i, e := range deref(w.Config.AccessMatrix.Entries) {
			add(e.From, e.To, e.Policy, false, "access matrix entry "+itoa(i)+" ("+string(e.Policy)+")")
		}
	}
	for _, nid := range sortedKeys(w.Index.Networks) {
		n := w.Index.Networks[nid]
		if !n.IsHub() {
			continue
		}
		for _, cid := range sortedKeys(deref(n.WG.Clients)) {
			cl := lower(cid)
			for _, r := range deref(deref(n.WG.Clients)[cid].Reachable) {
				add(model.MatrixEndpoint{Client: &cl}, r, model.MatrixEntryPolicyAllow, true, "the client's reachable list")
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].implicit != items[j].implicit {
			return !items[i].implicit
		}
		return items[i].rank < items[j].rank
	})
	if len(items) > 0 {
		// the first entry that matches decides
		it := items[0]
		verdict := "drop"
		if it.allow {
			verdict = "allow"
		}
		return AccessExplanation{Verdict: verdict, Layer: AccessMatrix, Reason: it.why}
	}
	// the default: local test networks reach the uplink, everything else is denied
	if containsEndpoint(to, matrixEndpoint{"uplink", ""}) {
		for _, e := range from {
			if e.kind == "network" && w.Index.Networks[e.id].IsLan() {
				return AccessExplanation{Verdict: "allow", Layer: AccessMatrix, Reason: "the default: a local test network reaches the uplink"}
			}
		}
	}
	return AccessExplanation{Verdict: "drop", Layer: AccessMatrix, Reason: "the default: no entry allows this traffic"}
}

// mgmtGuard is the guard of the forward chain (plan §2.16): a device under test (a packet that comes
// in on a test interface: a local test network or a WireGuard network of the role test, with its
// clients) does not reach the management network, behind the uplink interface (two-port topology) or
// behind a management interface of its own (three-port topology, P31-M35a-10, decided on 2026-10-17).
// The WireGuard networks of the role management, with their clients and the networks behind them, are
// part of the management network for the guard in both topologies (P31-AUDIT-11, decided 2026-10-18).
// It stands in front of the access rules and of the matrix; the one thing that opens it is an
// explicit matrix entry that allows the source to reach the management network, or the WireGuard network
// of the role management (or its client) the destination lies in. The clients' `reachable` lists do not:
// they are implicit entries (plan §2.16, §2.2.1).
func (w *World) mgmtGuard(src, dst netip.Addr, f AccessFacts) (AccessExplanation, bool) {
	from, to := w.matrixEndpoints(src, f), w.matrixEndpoints(dst, f)
	mgmt := matrixEndpoint{"management", ""}
	_, toMgmt := findEndpoint(to, mgmt)
	guarded := toMgmt || (!f.TwoPort && f.ManagementSubnet.IsValid() && f.ManagementSubnet.Contains(dst))
	if !guarded || !w.viaTestInterface(from) {
		return AccessExplanation{}, false
	}
	if w.Config.AccessMatrix != nil {
		for _, e := range deref(w.Config.AccessMatrix.Entries) {
			fe, ok1 := endpointOf(e.From)
			te, ok2 := endpointOf(e.To)
			// an entry to the management network, or to the WireGuard network of the role management (or the
			// client of one) the destination lies in, opens it by the destination (P31-AUDIT-11)
			opens := te == mgmt || (w.inMgmtWG(te) && containsEndpoint(to, te))
			_, fromOK := findEndpoint(from, fe)
			if ok1 && ok2 && e.Policy != model.MatrixEntryPolicyDeny && opens && fromOK {
				return AccessExplanation{}, false
			}
		}
	}
	tunnel := false
	for _, e := range to {
		tunnel = tunnel || w.inMgmtWG(e)
	}
	if tunnel {
		return AccessExplanation{Verdict: "drop", Layer: AccessMatrix, Reason: "devices under test do not reach a WireGuard network of the role management, which is part of the management network; no access rule changes that, only an access matrix entry that allows it"}, true
	}
	if f.TwoPort {
		return AccessExplanation{Verdict: "drop", Layer: AccessMatrix, Reason: "devices under test do not reach the management network behind the uplink; no access rule changes that"}, true
	}
	return AccessExplanation{Verdict: "drop", Layer: AccessMatrix, Reason: "devices under test do not reach the management network; no access rule changes that, only an access matrix entry that allows it"}, true
}

// viaTestInterface reports whether traffic from these endpoints comes in on a test interface: a
// bridge of a local test network, or a WireGuard interface whose role is not management.
func (w *World) viaTestInterface(from []matrixEndpoint) bool {
	test := func(n *NetInfo) bool {
		return n.IsLan() || (n.WG != nil && (n.WG.Role == nil || *n.WG.Role != model.WireGuardNetworkRoleManagement))
	}
	for _, e := range from {
		switch e.kind {
		case "network":
			if test(w.Index.Networks[e.id]) {
				return true
			}
		case "client":
			for _, nid := range sortedKeys(w.Index.Networks) {
				n := w.Index.Networks[nid]
				if !n.IsHub() || !test(n) {
					continue
				}
				if _, ok := deref(n.WG.Clients)[e.id]; ok {
					return true
				}
			}
		}
	}
	return false
}

// NetworkOf returns the network an address belongs to: the one whose prefix contains it most
// specifically (the longest prefix wins).
func (w *World) NetworkOf(ip netip.Addr) (string, bool) {
	best, bits := "", -1
	for _, id := range sortedKeys(w.Index.Networks) {
		for _, p := range w.prefixes[id] {
			if p.Contains(ip) && p.Bits() > bits {
				best, bits = id, p.Bits()
			}
		}
	}
	return best, bits >= 0
}
