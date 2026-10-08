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
	// Management are the prefixes of the management network (allowed sources and the management
	// interface's subnet).
	Management []netip.Prefix
	// TwoPort reports that the management network lies behind the uplink interface: devices under
	// test do not reach it unless an explicit entry says so (plan §2.16).
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

// endpoint ranks of the compiler: the more specific one is evaluated first.
const (
	rankClient = iota
	rankManagement
	rankUplink
	rankNetwork
)

type matrixEndpoint struct {
	kind string // client, network, management, uplink
	id   string
}

func (e matrixEndpoint) rank() int {
	switch e.kind {
	case "client":
		return rankClient
	case "management":
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
// (plan §2.2): towards the gateway itself the control plane (management sources, the UI port for
// everybody else) comes first, then the access rules, then the gateway's protection of the test
// networks; towards anything else the access rules, then the access matrix. The first access rule that
// selects the traffic decides, overlay rules before configured ones. A drop, reject or reset rule
// refuses; an allow rule is an exception to the matrix in the forward path, and in the input path it
// leaves the decision to the gateway's protection (a rule cannot open the gateway; open item P2-M9-01).
// q.Source.IP and q.DestIP must be set.
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
			return AccessExplanation{Verdict: r.Action, Layer: layer, Reason: label + " " + ruleVerb(r.Action) + " this traffic", Rule: r.RuleID, RuleName: r.Name}
		case !toGateway:
			return AccessExplanation{Verdict: "allow", Layer: layer, Reason: label + " allows this traffic before the access matrix is asked", Rule: r.RuleID, RuleName: r.Name}
		}
		// an allow rule towards the gateway: the gateway's protection decides
		a := w.AccessVerdict(src, dst, q.Protocol, q.Port, f)
		a.Reason = label + " allows it, which leaves the decision to the gateway's protection: " + a.Reason
		return a
	}
	return w.AccessVerdict(src, dst, q.Protocol, q.Port, f)
}

func ruleVerb(action string) string {
	switch action {
	case "reject":
		return "rejects (ICMP port unreachable)"
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
		case inMgmt(src):
			return AccessExplanation{Verdict: "allow", Layer: AccessGatewayProtection, Reason: "management sources reach the gateway's control plane"}
		case protocol == "icmp", port == 53 && (protocol == "udp" || protocol == "tcp" || protocol == ""), port == 67 && (protocol == "udp" || protocol == ""):
			return AccessExplanation{Verdict: "allow", Layer: AccessGatewayProtection, Reason: "the gateway answers ICMP echo, DNS and DHCP on a test network"}
		}
		return AccessExplanation{Verdict: "drop", Layer: AccessGatewayProtection, Reason: "a test network reaches only ICMP echo, DNS and DHCP on the gateway"}
	}
	from, to := w.endpointsOf(src, f.Management), w.endpointsOf(dst, f.Management)

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
		if !ok1 || !ok2 || !containsEndpoint(from, fe) || !containsEndpoint(to, te) {
			return
		}
		items = append(items, entry{rank: fe.rank()*4 + te.rank(), implicit: implicit, allow: policy != model.MatrixEntryPolicyDeny, why: why})
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
	// the two-port guard stands after the explicit entries and before everything implicit
	guard := func() (AccessExplanation, bool) {
		if !f.TwoPort || !containsEndpoint(to, matrixEndpoint{"management", ""}) {
			return AccessExplanation{}, false
		}
		for _, e := range from {
			if e.kind == "network" && w.Index.Networks[e.id].IsLan() {
				return AccessExplanation{Verdict: "drop", Layer: AccessMatrix, Reason: "devices under test do not reach the management network behind the uplink"}, true
			}
		}
		return AccessExplanation{}, false
	}
	if len(items) > 0 {
		// the first entry that matches decides; what a client's reachable list implies stands behind the guard
		it := items[0]
		if it.implicit {
			if g, ok := guard(); ok {
				return g
			}
		}
		verdict := "drop"
		if it.allow {
			verdict = "allow"
		}
		return AccessExplanation{Verdict: verdict, Layer: AccessMatrix, Reason: it.why}
	}
	if g, ok := guard(); ok {
		return g
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
