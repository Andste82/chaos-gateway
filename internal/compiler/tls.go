package compiler

import (
	"net/netip"
	"slices"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the TLS family (plan §2.4, §2.8, M21). A TLS case is behavior of the TLS responder, which
// terminates the connection itself, so the family resolves into two things:
//
//   - what the kernel sends to the responder (the chain tls_redirect): a connection that one of the cases selects
//     by its source, destination and port is routed unchanged into the service namespace (mark bit 20, table 102,
//     plan §3.3), where nftables redirects it to the responder's port; the responder reads the original destination
//     with SO_ORIGINAL_DST. The selection is made once, for a new connection (conntrack mark bit TLSConnMarkBit),
//     and every later packet of the connection follows it for as long as the case selects the connection
//     ("new connections" of plan §2.8: what was established before the case stays as it is). A case with
//     `cut_existing` selects the established connections, too: their next packet reaches a responder that knows no
//     such connection and is answered with a reset, which is the cut. The access rules stand in front of all of it:
//     they judge the redirected packets by the original tuple in the forward chain before the packets are accepted
//     towards the namespace (E11).
//   - what the responder does with a connection (TLSCase): which case decides it depends on what only the connection
//     knows (the server name of its ClientHello, the names the device resolved), so the responder asks the API for
//     the decision of each connection, and the compiler only lists the cases with the clients they apply to.
//
// The case `intercept` (M22) is selected like every other: the responder hands the connection to the interception
// proxy instead of answering it. Its option block_quic (on unless the case turns it off) adds one thing to the kernel
// side: the UDP packets to port 443 of the selected clients are dropped, so that a client that tries QUIC falls back
// to the TCP connection the proxy can intercept (tlsBlockQUIC).

const (
	// TLSChain is the prerouting chain that selects the connections for the responder.
	TLSChain = "tls_redirect"
	// TLSPriority runs right after the classification (plan §3.3: "right after classification"), so the service
	// mark is written next to the fault id and before the routing decision.
	TLSPriority = ClassifyPriority + 5
	// TLSConnMarkBit is bit 23 of the conntrack mark (bit 22 is the port forward's, forward.go): the connection was
	// selected for the responder. The packet mark is per packet; this one is what keeps a connection selected.
	TLSConnMarkBit = uint32(1) << 23
	// ServiceTLSPort is the port of the TLS responder inside the service namespace: the redirect there ends on it.
	ServiceTLSPort = executor.ServiceTLSPort
	// ServiceMarkBit is ServiceMark as a number: bit 20 of the packet mark.
	ServiceMarkBit = uint32(1) << 20
	// ServiceRedirectChain is the chain inside the service namespace that redirects what arrives for another
	// address to the responder (written by the executor when it makes the namespace, plan §3.3).
	ServiceRedirectChain = executor.ServiceRedirectChain
)

// TLSCase is one entry of the responder's list of resolved cases: a TLS overlay, or the tls part of an activated
// profile, for the clients of its Sources.
type TLSCase struct {
	// Key is `layer:id:tls` like the keys of the other families: the id is the overlay's.
	Key string `json:"key"`
	// Overlay is the overlay (the activation, for the part of a profile); Profile the name of the profile.
	Overlay string `json:"overlay"`
	Profile string `json:"profile,omitempty"`
	// Scope says where the case applies, for messages.
	Scope string `json:"scope"`
	// Case is the case as it was written.
	Case model.TlsCase `json:"case"`
	// Sources are the clients the entry is for: the addresses of the devices and the stretches of a network that
	// have exactly the same cases in the same order.
	Sources []netip.Prefix `json:"sources"`
}

// compileTLS builds the responder's list and the selection of the kernel from the TLS cases that apply.
func (t *Target) compileTLS(w *domain.World, sources []domain.Source, identity domain.Identity, idx *domain.Index) {
	seen := map[string]bool{}
	var cands []domain.Candidate
	for _, g := range groupCandidates(sources, w.TLSCandidates) {
		clients := domain.CollapsePrefixes(g.prefixes)
		for _, c := range g.cands {
			if c.TLS == nil {
				continue
			}
			t.TLSCases = append(t.TLSCases, TLSCase{Key: c.Key(), Overlay: c.ID, Profile: c.ProfileName,
				Scope: describeScope(idx, c.Scope), Case: *c.TLS, Sources: clients})
			if !seen[c.Key()] {
				seen[c.Key()] = true
				cands = append(cands, c)
				t.Winners = append(t.Winners, c.Key())
				t.Effective = append(t.Effective, EffectiveFault{Key: c.Key(), Layer: string(c.Layer), Source: c.ID, Family: c.Family,
					Profile: c.ProfileName, Scope: describeScope(idx, c.Scope), Summary: c.Summary()})
			}
		}
	}
	sort.Strings(t.Winners)
	if len(cands) == 0 {
		return
	}
	if t.Service == nil {
		t.warn(CodeUnsupported, "", "there are TLS cases, which need the gateway's TLS responder: there is no service namespace, so nothing is redirected")
		return
	}
	domain.RankOrder(cands)
	for _, c := range cands {
		t.tlsSelect(w, identity, c)
	}
}

// tlsMatch is what selects the traffic of a case by source and destination, without the protocol and the ports; false
// when nothing of the case's scope has an address yet (the case selects nothing).
func (t *Target) tlsMatch(w *domain.World, identity domain.Identity, c domain.Candidate) ([]any, bool) {
	sources, all := w.ScopePrefixes(c.Scope, identity)
	var sel []any
	if all {
		sel = append(sel, eq(ctOriginalIP("saddr"), setRef(classifyNetsName())))
	} else if len(sources) == 0 {
		return nil, false // nothing of the scope has an address yet
	} else {
		sel = append(sel, eq(ctOriginalIP("saddr"), prefixes(domain.CollapsePrefixes(sources))))
	}
	if d := c.Match.Destination; d != nil {
		switch {
		case d.Hostname != nil:
			clients := sources
			if all {
				clients = parsePrefixes(t.classifyNets())
			}
			sel = append(sel, hostLookup(t.hostSet(*d.Hostname, clients).Name))
		case d.Uplink != nil:
			s := prefixSet(nonUplinkSetName(), w.NonUplinkPrefixes())
			t.tlsSets = append(t.tlsSets, s)
			sel = append(sel, match(ctOriginalIP("daddr"), "!=", setRef(s.Name)))
		case d.Network != nil:
			sel = append(sel, eq(ctOriginalIP("daddr"), prefixes(w.NetworkPrefixes(*d.Network))))
		case d.Cidr != nil:
			if a, err := netip.ParseAddr(*d.Cidr); err == nil {
				sel = append(sel, eq(ctOriginalIP("daddr"), prefixValue(netip.PrefixFrom(a, 32))))
			} else if p, err := netip.ParsePrefix(*d.Cidr); err == nil {
				sel = append(sel, eq(ctOriginalIP("daddr"), prefixValue(p.Masked())))
			}
		}
	}
	return sel, true
}

// tlsSelect adds the rules that select the connections of one case: for a new connection the selection (the
// conntrack mark and the service mark of the packet), for the later packets the service mark of a connection that is
// selected. With cut_existing the first rule selects every packet, so a connection that is already there is
// selected at its next packet.
func (t *Target) tlsSelect(w *domain.World, identity domain.Identity, c domain.Candidate) {
	sel, ok := t.tlsMatch(w, identity, c)
	if !ok {
		return
	}
	ports, ranges := domain.TLSPorts(*c.TLS)
	sel = append(sel, eq(meta("l4proto"), "tcp"), eq(ctOriginal("proto-dst"), portValue(mergePorts(ports, ranges))))

	serviceMark := markSet(bitOr(meta("mark"), int64(ServiceMarkBit)))
	connMark := ctMarkSet(bitOr(ctKey("mark"), int64(TLSConnMarkBit)))
	first := append([]any(nil), sel...)
	cut := c.TLS.CutExisting != nil && *c.TLS.CutExisting
	if !cut {
		first = append(first, match(ctKey("state"), "in", "new"))
	}
	t.tlsRules = append(t.tlsRules, newRule(append(first, connMark, serviceMark)...))
	if !cut {
		later := append(append([]any(nil), sel...), eq(bitAnd(ctKey("mark"), int64(TLSConnMarkBit)), int64(TLSConnMarkBit)))
		t.tlsRules = append(t.tlsRules, newRule(append(later, serviceMark)...))
	}
	if c.TLS.Case == model.TlsCaseCaseIntercept && blockQUIC(c.TLS) {
		t.tlsBlockQUIC(w, identity, c)
	}
}

// blockQUIC says whether an interception case drops QUIC: the option is on unless the case turns it off.
func blockQUIC(c *model.TlsCase) bool {
	return c.Intercept == nil || c.Intercept.BlockQuic == nil || *c.Intercept.BlockQuic
}

// QUICPort is the UDP port QUIC (HTTP/3) is dropped on while an interception case is in force.
const QUICPort = 443

// tlsBlockQUIC adds the rule that drops the UDP packets to port 443 of the clients (and the destination) an
// interception case selects: a client that tries QUIC meets no answer and falls back to TCP, which the proxy
// intercepts. QUIC itself is not intercepted (plan §2.8).
func (t *Target) tlsBlockQUIC(w *domain.World, identity domain.Identity, c domain.Candidate) {
	sel, ok := t.tlsMatch(w, identity, c)
	if !ok {
		return
	}
	sel = append(sel, eq(meta("l4proto"), "udp"), eq(ctOriginal("proto-dst"), QUICPort), verdict("drop"))
	t.tlsDrops = append(t.tlsDrops, newRule(sel...))
}

// tlsChain is the chain that selects the connections for the responder; nil when no case selects anything. The
// packets that come back from the responder (they enter through svc0) and the replies of every connection are never
// touched: only the packets of the original direction of what a device opened.
func (t *Target) tlsChain() *Chain {
	if len(t.tlsRules) == 0 || t.Service == nil {
		return nil
	}
	rules := []Rule{
		newRule(iifname(t.Service.HostIf), verdict("return")),
		newRule(match(ctKey("direction"), "==", "reply"), verdict("return")),
	}
	rules = append(rules, t.tlsDrops...)
	rules = append(rules, t.tlsRules...)
	return &Chain{Name: TLSChain, Base: &BaseChain{Type: "filter", Hook: "prerouting", Prio: TLSPriority, Policy: "accept"}, Rules: rules}
}

// tlsForward accepts, after the access rules, the packets that were selected for the responder and leave towards
// the service namespace: whatever the access matrix says about the connection to the real server, the answer comes
// from the gateway (the access rules have judged the original tuple before, E11).
func (t *Target) tlsForward(ifsCG string) []Rule {
	if len(t.tlsRules) == 0 || t.Service == nil {
		return nil
	}
	return []Rule{newRule(iifSet(ifsCG), oifname(t.Service.HostIf), eq(bitAnd(meta("mark"), int64(ServiceMarkBit)), int64(ServiceMarkBit)), verdict("accept"))}
}

// tlsExtraSets are the sets the TLS rules need besides the ones of the access rules, without those that the access
// rules define already.
func (t *Target) tlsExtraSets(have []SetDef) []SetDef {
	var out []SetDef
	for _, s := range t.tlsSets {
		if !slices.ContainsFunc(have, func(h SetDef) bool { return h.Name == s.Name }) &&
			!slices.ContainsFunc(out, func(h SetDef) bool { return h.Name == s.Name }) {
			out = append(out, s)
		}
	}
	return out
}
