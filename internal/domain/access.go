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
// `reachable` list implies, then the default) and is used by `explain`. Access rules are not part of
// it: they take effect, and join the explanation, with milestone M9.

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
}

// AccessLayers of an explanation, as the spec names them.
const (
	AccessGatewayProtection = "gateway_protection"
	AccessMatrix            = "access_matrix"
)

// AccessExplanation is the verdict and what decided it.
type AccessExplanation struct {
	// Verdict is allow or drop.
	Verdict string
	// Layer is AccessGatewayProtection or AccessMatrix.
	Layer string
	// Reason says in words which entry or default decided.
	Reason string
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
			return AccessExplanation{"allow", AccessGatewayProtection, "management sources reach the gateway's control plane"}
		case protocol == "icmp", port == 53 && (protocol == "udp" || protocol == "tcp" || protocol == ""), port == 67 && (protocol == "udp" || protocol == ""):
			return AccessExplanation{"allow", AccessGatewayProtection, "the gateway answers ICMP echo, DNS and DHCP on a test network"}
		}
		return AccessExplanation{"drop", AccessGatewayProtection, "a test network reaches only ICMP echo, DNS and DHCP on the gateway"}
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
				return AccessExplanation{"drop", AccessMatrix, "devices under test do not reach the management network behind the uplink"}, true
			}
		}
		return AccessExplanation{}, false
	}
	for _, it := range items {
		if it.implicit {
			if g, ok := guard(); ok {
				return g
			}
		}
		verdict := "drop"
		if it.allow {
			verdict = "allow"
		}
		return AccessExplanation{verdict, AccessMatrix, it.why}
	}
	if g, ok := guard(); ok {
		return g
	}
	// the default: local test networks reach the uplink, everything else is denied
	if containsEndpoint(to, matrixEndpoint{"uplink", ""}) {
		for _, e := range from {
			if e.kind == "network" && w.Index.Networks[e.id].IsLan() {
				return AccessExplanation{"allow", AccessMatrix, "the default: a local test network reaches the uplink"}
			}
		}
	}
	return AccessExplanation{"drop", AccessMatrix, "the default: no entry allows this traffic"}
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
