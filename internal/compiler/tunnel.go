package compiler

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Tunnel faults and WireGuard actions (plan §2.2.1, spike S15, M10).
//
// A tunnel fault impairs the encrypted UDP of one WireGuard peer, a hub client or the remote side of a
// link, whatever runs inside the tunnel. The two directions are two mechanisms, both on the underlay:
//
//	towards the peer (download)   the gateway's own packets: a base chain on the output hook looks the
//	                              packet's destination (peer address and port) up in a verdict map, the
//	                              chain it goes to writes the fault id and the direction bit into the
//	                              mark, and the fw filters of the interface's tc tree do the rest
//	from the peer (upload)        the ingress qdisc of the uplink redirects the peer's packets (a flower
//	                              filter on the outer source address and port) to the IFB device, where
//	                              a flower filter chooses the class of the fault: an HTB class with a
//	                              netem leaf, like every other fault
//
// Upload and download are named as the rest of the product names them: upload is what the remote side
// sends, download what it receives. The encrypted packets have a conntrack entry of their own, so the
// classification of the traffic inside the tunnel (the prerouting chain) never meets them and the two
// stack (plan §2.4, E10).
//
// The peer is found by its endpoint: the address and port its packets come from and go to. For a hub
// client that is the address the client was last seen at (the engine reads it from the interface right
// before it compiles, Input.PeerEndpoints), for a link the observed one or, without, the endpoint the
// link is configured with. A peer whose endpoint is not known cannot be told from other UDP traffic;
// its fault is not compiled and the compiler says so (CodeTunnelEndpointUnknown).

// Names of the objects of the tunnel faults.
const (
	// IFBDev is the IFB device the encrypted UDP from a peer is redirected to.
	IFBDev = executor.IFBName
	// IngressPref and IFBFlowerPref are the priorities of the flower filters on the ingress qdisc of the
	// uplink and on the root of the IFB.
	IngressPref   = 10
	IFBFlowerPref = 1
	// IngressHandle is the handle of an ingress qdisc.
	IngressHandle = "ffff:"

	// TunnelOutChain is the base chain on the output hook that classifies the packets towards a peer.
	TunnelOutChain = "tunnel_out"
	// TunnelOutPriority is mangle (-150), where the rest of the classification stands.
	TunnelOutPriority = -150
	// WGBlockOutChain and WGBlockInChain are the base chains that drop the encrypted UDP to and from a peer
	// whose endpoint an overlay blocks. They stand at raw priority (-300): the blocked packets are dropped
	// before connection tracking sees them.
	WGBlockOutChain = "wg_block_out"
	WGBlockInChain  = "wg_block_in"
	wgBlockPriority = -300
	// TunnelChainPrefix and WGBlockChainPrefix start the names of the chains the maps go to.
	TunnelChainPrefix  = "tun_"
	WGBlockChainPrefix = "wgblock_"

	// markKeepOnTunnelWrite is what a tunnel fault's chain keeps of the mark: it clears the id and the
	// direction bit, which the chain writes itself (towards the peer is always the download direction).
	// The packets of the output hook belong to the gateway, so there is no direction from a connection
	// to keep (the conntrack entry of the encrypted UDP says "original" or "reply" by who spoke first).
	markKeepOnTunnelWrite = ^(markIDMaskBits | markDirMaskBits) // 0xfffe000f
)

// Problem codes of tunnel faults and WireGuard actions.
const (
	// CodeTunnelEndpointUnknown: a tunnel fault or a blocked endpoint names a peer whose endpoint is not
	// known (it has never connected, and the link names none). The mechanism cannot select its packets,
	// so it does nothing until the endpoint is known. A warning.
	CodeTunnelEndpointUnknown = "tunnel_endpoint_unknown"
	// CodeTunnelEndpointShared: two tunnels are seen at the same address and port (a stale endpoint, or a NAT
	// that gave both the same mapping). Their packets cannot be told apart, so the fault or blocked endpoint of the
	// later tunnel (in the order of the tunnel keys) is not compiled; the earlier one is.
	CodeTunnelEndpointShared = "tunnel_endpoint_shared"
)

// TunnelInfo says which tunnel a fault of family tunnel impairs and where its packets are found.
type TunnelInfo struct {
	// Tunnel is "client:<id>" or "link:<id>".
	Tunnel string `json:"tunnel"`
	// Peer is the id of the client (or of the link network, whose only peer is the remote side) and
	// PeerName its name.
	Peer     string `json:"peer"`
	PeerName string `json:"peer_name"`
	// Interface is the WireGuard interface the peer is on.
	Interface string `json:"interface"`
	// Endpoint is "ip:port" the peer's encrypted UDP comes from and goes to.
	Endpoint string `json:"endpoint"`
}

// WGActionInfo is a WireGuard-action overlay at work.
type WGActionInfo struct {
	// Overlay is the id of the overlay and Action disable, key_mismatch or block_endpoint.
	Overlay string `json:"overlay"`
	Action  string `json:"action"`
	// Tunnel is "client:<id>" or "link:<id>"; Peer and PeerName name the peer.
	Tunnel   string `json:"tunnel"`
	Peer     string `json:"peer"`
	PeerName string `json:"peer_name"`
	// Counter is the nft counter of the packets a blocked endpoint dropped, empty for the other actions.
	Counter string `json:"counter,omitempty"`
}

// IFBTarget is the IFB device with its tree and the ingress filters that feed it: what the packets from
// a peer pass through before the gateway's stack sees them.
type IFBTarget struct {
	// Dev is the IFB device and Uplink the interface whose ingress qdisc redirects into it.
	Dev    string `json:"dev"`
	Uplink string `json:"uplink"`
	// TC is the tree of the IFB: an HTB root and a class with a netem leaf per tunnel fault, selected by
	// a flower filter on the peer's endpoint (TCClass.Endpoint). It has the same shape as the trees of
	// the interfaces; only the filters differ.
	TC *TCTarget `json:"tc"`
}

// tunnelPeer is a peer a tunnel fault or a WireGuard action can name.
type tunnelPeer struct {
	key, id, name, iface string
	listen               int
	ep                   netip.AddrPort // zero when not known
}

// tunnelKeyOfClient and tunnelKeyOfLink are the identities of the tunnels, as the domain layer names them.
func tunnelKeyOfClient(id string) string { return "client:" + strings.ToLower(id) }
func tunnelKeyOfLink(id string) string   { return "link:" + strings.ToLower(id) }

// UsableEndpoint is the endpoint a mechanism of this package can select a peer's packets by: an IPv4
// address and a port (the flower filters, the verdict maps and the executor's grammar are IPv4 only; an
// IPv4 address in its IPv6 form is unmapped). Any other endpoint, an IPv6 one included, is not usable and
// gives the zero value: the peer is treated like one whose address is not known.
func UsableEndpoint(ep netip.AddrPort) netip.AddrPort {
	if !ep.IsValid() {
		return netip.AddrPort{}
	}
	a := ep.Addr().Unmap()
	if !a.Is4() || ep.Port() == 0 {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(a, ep.Port())
}

// UsableEndpointString is UsableEndpoint for the text `wg show` reports; "" when it is not usable.
func UsableEndpointString(s string) string {
	ep, err := netip.ParseAddrPort(s)
	if err != nil {
		return ""
	}
	if u := UsableEndpoint(ep); u.IsValid() {
		return u.String()
	}
	return ""
}

// peerEndpoint is the endpoint a peer is reached at: the one observed, else the configured one when
// it is an address (a host name is not resolved here). A peer that is observed at an endpoint that is
// not usable (IPv6) has none: the configured one is not where its packets go.
func peerEndpoint(in Input, p WGPeer) netip.AddrPort {
	if ep, ok := in.PeerEndpoints[p.ID]; ok && ep.IsValid() {
		return UsableEndpoint(ep)
	}
	if p.Endpoint != "" {
		if ep, err := netip.ParseAddrPort(p.Endpoint); err == nil {
			return UsableEndpoint(ep)
		}
	}
	return netip.AddrPort{}
}

// tunnelPeers lists the peers on the interfaces of the target by tunnel key. A peer that is not on
// its interface (a disabled client, a peer an overlay removed) is not among them: there is no tunnel.
func (t *Target) tunnelPeers(in Input) map[string]tunnelPeer {
	out := map[string]tunnelPeer{}
	for _, w := range t.WireGuard {
		for _, p := range w.Peers {
			key := tunnelKeyOfClient(p.ID)
			if w.Kind == "link" {
				key = tunnelKeyOfLink(p.ID)
			}
			out[key] = tunnelPeer{key: key, id: p.ID, name: p.Name, iface: w.Name, listen: w.ListenPort, ep: peerEndpoint(in, p)}
		}
	}
	return out
}

// NeedsPeerEndpoints reports whether the configuration and the overlays contain something that selects a
// peer's packets by its endpoint: a tunnel fault or an overlay that blocks an endpoint. The engine reads
// the endpoints from the interfaces only then.
func NeedsPeerEndpoints(cfg *model.Configuration, overlays []model.Overlay) bool {
	for _, f := range deref2(cfg.Faults) {
		if f.Family != nil && *f.Family == model.ConfigFaultFamilyTunnel && (f.Enabled == nil || *f.Enabled) {
			return true
		}
	}
	for _, o := range overlays {
		switch {
		case o.Kind == model.OverlayKindFault && o.Fault != nil && o.Fault.Family != nil && *o.Fault.Family == model.FaultBodyFamilyTunnel:
			return true
		case o.Kind == model.OverlayKindWireguard && o.Wireguard != nil && o.Wireguard.Action == model.BlockEndpoint:
			return true
		}
	}
	return false
}

// ---- WireGuard actions ---------------------------------------------------------------------------

// wgActions are the WireGuard-action overlays (plan §2.2.1) by peer: the overlay that asks for the action.
// The newest overlay of an action counts, which only matters for the key mismatch (its key derives from it).
type wgActions struct {
	disable, keyMismatch, block map[string]string // tunnel key -> overlay id
}

func wgActionsOf(overlays []model.Overlay) wgActions {
	a := wgActions{disable: map[string]string{}, keyMismatch: map[string]string{}, block: map[string]string{}}
	// newest last, so that it wins
	sorted := append([]model.Overlay(nil), overlays...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].UpdatedAt.Before(sorted[j].UpdatedAt) })
	for _, o := range sorted {
		if o.Kind != model.OverlayKindWireguard || o.Wireguard == nil {
			continue
		}
		var key string
		switch {
		case o.Wireguard.Client != nil:
			key = tunnelKeyOfClient(*o.Wireguard.Client)
		case o.Wireguard.Link != nil:
			key = tunnelKeyOfLink(*o.Wireguard.Link)
		default:
			continue
		}
		switch o.Wireguard.Action {
		case model.Disable:
			a.disable[key] = o.Id.String()
		case model.KeyMismatch:
			a.keyMismatch[key] = o.Id.String()
		case model.BlockEndpoint:
			a.block[key] = o.Id.String()
		}
	}
	return a
}

// mismatchedKey is the public key the gateway holds for a peer while a key-mismatch overlay is active: a
// key nobody has the private key of, derived from the overlay and the key it replaces, so that compiling
// the same state twice gives the same interface and a re-apply keeps what the kernel holds.
func mismatchedKey(overlayID, publicKey string) string {
	h := sha256.Sum256([]byte("chaosgw key_mismatch\x00" + overlayID + "\x00" + publicKey))
	return base64.StdEncoding.EncodeToString(h[:])
}

// ---- tunnel faults ---------------------------------------------------------------------------------

// resolveTunnelFaults resolves the tunnel faults (one winner per tunnel, overlays before configuration,
// the newest wins) and adds the winners that impair something to faults. winners are the keys of the
// faults that win; every winner is in it, also one that impairs nothing or names a tunnel that is not up.
func (t *Target) resolveTunnelFaults(in Input, w *domain.World, faults map[string]*Fault, keySet, winners map[string]bool) bool {
	results := w.ResolveTunnels()
	if len(results) == 0 {
		return true
	}
	peers := t.tunnelPeers(in)
	claimed := map[netip.AddrPort]string{} // endpoint -> tunnel that selects its packets
	for _, r := range results {
		c := r.Winner
		key := baseKey(c)
		winners[key] = true
		peer, ok := peers[r.Tunnel]
		if !ok {
			continue // no such peer on an interface: nothing to impair
		}
		pu, pd := directionParams(c.Impairment)
		var up, down *Netem
		for _, x := range []struct {
			p   *model.NetemParams
			dst **Netem
		}{{pu, &up}, {pd, &down}} {
			if x.p == nil {
				continue
			}
			n, err := netemFrom(x.p)
			if err != nil {
				t.errorf(CodeFaultInvalid, "", "the tunnel fault %s has parameters that cannot be compiled: %v", c.ID, err)
				return false
			}
			if n.IsNeutral() {
				continue
			}
			// a tunnel fault never has a per-device queue: no rate, no queue limit (validation)
			*x.dst = &n
		}
		if up == nil && down == nil {
			continue
		}
		t.noteEndpoint(peer)
		if !peer.ep.IsValid() {
			t.warn(CodeTunnelEndpointUnknown, "", "the tunnel fault %s names %s, whose address is not known (it has not connected yet and no endpoint is configured, or it is seen at an address that is not IPv4, which this mechanism does not select): it takes effect when the peer is seen at an IPv4 address", c.ID, peer.name)
			continue
		}
		if other, taken := claimed[peer.ep]; taken {
			t.warn(CodeTunnelEndpointShared, "", "the tunnel fault %s names %s, which is seen at %s like %s: the packets of the two cannot be told apart, so only the fault of %s is compiled", c.ID, peer.name, peer.ep, other, other)
			continue
		}
		claimed[peer.ep] = peer.name
		f := &Fault{Key: key, Layer: string(c.Layer), Source: c.ID, Scope: describeTunnel(peer),
			Upload: up, Download: down,
			CounterUp: faultCounterName(key, Upload), CounterDown: faultCounterName(key, Download),
			Tunnel: &TunnelInfo{Tunnel: peer.key, Peer: peer.id, PeerName: peer.name, Interface: peer.iface, Endpoint: peer.ep.String()}}
		faults[key] = f
		keySet[key] = true
	}
	return true
}

// noteEndpoint records the endpoint a mechanism of this target selects the peer's packets by.
func (t *Target) noteEndpoint(p tunnelPeer) {
	if t.Endpoints == nil {
		t.Endpoints = map[string]string{}
	}
	t.Endpoints[p.id] = ""
	if p.ep.IsValid() {
		t.Endpoints[p.id] = p.ep.String()
	}
}

// describeTunnel names a tunnel for messages: "tunnel of client rA".
func describeTunnel(p tunnelPeer) string {
	if strings.HasPrefix(p.key, "link:") {
		return "tunnel of link " + p.name
	}
	return "tunnel of client " + p.name
}

// compileTunnelTC builds the IFB tree of the tunnel faults and checks its class limit; the classes of the
// direction towards the peer are part of the interfaces' tree (compileTC). It runs after the ids exist.
func (t *Target) compileTunnelTC(in Input) (*TCTarget, bool) {
	var classes []TCClass
	for i := range t.Faults {
		f := &t.Faults[i]
		if f.Tunnel == nil || f.Upload == nil {
			continue
		}
		cfg := *f.Upload
		classes = append(classes, TCClass{ID: f.ID, Dir: Upload, Minor: classMinor(f.ID, Upload), Mark: MarkOf(f.ID, Upload),
			Netem: cfg, Endpoint: f.Tunnel.Endpoint})
	}
	if len(classes) == 0 {
		return nil, true
	}
	limit := in.ClassLimit
	if limit <= 0 {
		limit = DefaultClassLimit()
	}
	if len(classes)+1 > limit {
		byKey := map[string]*Fault{}
		for i := range t.Faults {
			byKey[t.Faults[i].Key] = &t.Faults[i]
		}
		t.capacityProblem(byKey, fmt.Sprintf("%d classes (one per tunnel fault, plus the default) exceed the limit of %d for the IFB device", len(classes)+1, limit), func(f *Fault) int {
			if f.Tunnel != nil && f.Upload != nil {
				return 1
			}
			return 0
		})
		return nil, false
	}
	budget := in.QueueBudget
	if budget <= 0 {
		budget = DefaultQueueBudget
	}
	for i := range classes {
		c := &classes[i]
		if !c.Netem.LimitExplicit {
			c.Netem.Limit = computedLimit(c.Netem, len(classes), budget)
		}
		if c.Netem.Flapping != nil {
			c.FlapKey = FlapKey(t.faultByID(c.ID).Key, Upload)
			c.Down = in.FlapPhase != nil && in.FlapPhase(c.FlapKey, *c.Netem.Flapping)
		}
		// the fault keeps the configuration that is written
		n := c.Netem
		t.faultByID(c.ID).Upload = &n
	}
	return &TCTarget{Devs: []string{IFBDev}, Classes: classes}, true
}

func (t *Target) faultByID(id int) *Fault {
	for i := range t.Faults {
		if t.Faults[i].ID == id {
			return &t.Faults[i]
		}
	}
	return &Fault{}
}

// ---- the IFB and its ingress filters --------------------------------------------------------------

// IngressEntries returns the tc commands of the ingress side: the ingress qdisc of the uplink and a
// flower filter per tunnel fault that redirects the peer's encrypted UDP to the IFB. `replace` makes what
// is missing and changes what exists in place.
func (b *IFBTarget) IngressEntries() []executor.TCEntry {
	if b == nil || b.TC == nil {
		return nil
	}
	es := []executor.TCEntry{{Object: "qdisc", Action: "replace", Dev: b.Uplink, Parent: "ingress"}}
	for _, c := range b.TC.Classes {
		es = append(es, b.IngressFilter(c))
	}
	return es
}

// IngressFilter is the filter on the ingress qdisc that sends the packets of one tunnel fault to the IFB.
func (b *IFBTarget) IngressFilter(c TCClass) executor.TCEntry {
	ep, _ := netip.ParseAddrPort(c.Endpoint)
	return executor.TCEntry{Object: "filter", Action: "replace", Dev: b.Uplink, Parent: IngressHandle, Handle: strconv.Itoa(c.ID),
		Args: []string{"protocol", "ip", "prio", strconv.Itoa(IngressPref), "flower", "ip_proto", "udp",
			"src_ip", ep.Addr().String(), "src_port", strconv.Itoa(int(ep.Port())),
			"action", "mirred", "egress", "redirect", "dev", b.Dev}}
}

// IngressNorm is the ingress side as the listing of the uplink shows it: the qdisc and the flower filters
// that redirect to the IFB. Filters of anyone else are not part of it.
func (b *IFBTarget) IngressNorm() *linux.NormTree {
	t := &linux.NormTree{Dev: "", Qdiscs: []linux.NormQdisc{}, Classes: []linux.NormClass{}, Filters: []linux.NormFilter{}}
	if b == nil || b.TC == nil {
		return t
	}
	t.Dev = b.Uplink
	t.Qdiscs = append(t.Qdiscs, linux.NormQdisc{Handle: IngressHandle, Parent: "ingress", Kind: "ingress"})
	for _, c := range b.TC.Classes {
		t.Filters = append(t.Filters, b.IngressNormFilter(c))
	}
	return t.Sorted()
}

// IngressNormFilter is the normalized form of IngressFilter.
func (b *IFBTarget) IngressNormFilter(c TCClass) linux.NormFilter {
	ep, _ := netip.ParseAddrPort(c.Endpoint)
	return linux.NormFilter{Parent: "ingress", Protocol: "ip", Pref: IngressPref, Kind: "flower",
		Flower: &linux.FlowerSpec{Handle: c.ID, IPProto: "udp", SrcIP: ep.Addr().String(), SrcPort: int(ep.Port()), Redirect: b.Dev}}
}

// ---- nftables ------------------------------------------------------------------------------------------

// tunnelChainName is the chain a tunnel fault's map element goes to.
func tunnelChainName(id int) string { return fmt.Sprintf("%s%d", TunnelChainPrefix, id) }

// tunnelChain writes the id and the direction (towards the peer: download) of a tunnel fault into the
// mark and counts the packet.
func tunnelChain(f Fault) Chain {
	rules := []Rule{newRule(markSet(bitOr(bitOr(bitAnd(meta("mark"), int64(markKeepOnTunnelWrite)), lshift(f.ID, MarkIDShift)), int64(markDirMaskBits))))}
	if f.Download != nil {
		rules = append(rules, newRule(counter(f.CounterDown)))
	}
	rules = append(rules, newRule(verdict("return")))
	return Chain{Name: tunnelChainName(f.ID), Rules: rules}
}

// outAddrKey is "ip . port" of an endpoint, in the form of a two-field map key.
func outAddrKey(ep netip.AddrPort) string {
	return ep.Addr().String() + " . " + strconv.Itoa(int(ep.Port()))
}

// compileTunnelNft adds the classification of the packets towards the peers: the map of endpoints, the
// chain on the output hook and the chain of each tunnel fault; and the chains that drop the packets
// of blocked endpoints. It runs with the other chains of the classification.
func (t *Target) compileTunnelNft(in Input) {
	defer func() {
		sort.Strings(t.Nft.Counters)
		sort.Slice(t.Nft.Maps, func(i, j int) bool { return t.Nft.Maps[i].Name < t.Nft.Maps[j].Name })
		sort.Slice(t.Nft.Chains, func(i, j int) bool { return t.Nft.Chains[i].Name < t.Nft.Chains[j].Name })
	}()
	// ---- towards the peer: the fault ids ----
	var elems []MapElement
	for _, f := range t.Faults {
		if f.Tunnel == nil || f.Download == nil {
			continue
		}
		ep, _ := netip.ParseAddrPort(f.Tunnel.Endpoint)
		elems = append(elems, MapElement{Key: outAddrKey(ep), Value: tunnelChainName(f.ID)})
		t.Nft.Chains = append(t.Nft.Chains, tunnelChain(f))
		t.Nft.Counters = append(t.Nft.Counters, f.CounterDown)
	}
	if len(elems) > 0 {
		sort.Slice(elems, func(i, j int) bool { return elems[i].Key < elems[j].Key })
		md := MapDef{KeyType: []string{"ipv4_addr", "inet_service"}, ValueType: "verdict", Elements: elems}
		md.Name = hashMapName("tun_out", md.KeyType, md.ValueType, md.Flags)
		t.Nft.Maps = append(t.Nft.Maps, md)
		t.Nft.Chains = append(t.Nft.Chains, Chain{Name: TunnelOutChain,
			Base: &BaseChain{Type: "filter", Hook: "output", Prio: TunnelOutPriority, Policy: "accept"},
			Rules: []Rule{newRule(match(meta("l4proto"), "==", "udp"),
				vmap(concat(payload("ip", "daddr"), payload("udp", "dport")), md.Name))}})
	}

	// ---- blocked endpoints ----
	acts := wgActionsOf(in.Overlays)
	if len(acts.block) == 0 {
		return
	}
	peers := t.tunnelPeers(in)
	var outEl, inEl []MapElement
	keys := make([]string, 0, len(acts.block))
	for k := range acts.block {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	blocked := map[string]string{} // map key -> peer whose packets it selects
	for _, k := range keys {
		peer, ok := peers[k]
		if !ok {
			continue // the peer is not on an interface: there is nothing to block
		}
		t.noteEndpoint(peer)
		if !peer.ep.IsValid() {
			t.warn(CodeTunnelEndpointUnknown, "", "the overlay that blocks the endpoint of %s cannot select its packets, because the address of %s is not known (it has not connected yet and no endpoint is configured, or it is seen at an address that is not IPv4, which this mechanism does not select): it takes effect when the peer is seen at an IPv4 address", peer.name, peer.name)
			continue
		}
		mapKey := peer.ep.Addr().String() + " . " + strconv.Itoa(int(peer.ep.Port())) + " . " + strconv.Itoa(peer.listen)
		if other, taken := blocked[mapKey]; taken {
			t.warn(CodeTunnelEndpointShared, "", "the overlay that blocks the endpoint of %s names an address that %s is seen at too (%s, interface port %d): the packets of the two cannot be told apart, so only the block of %s is compiled", peer.name, other, peer.ep, peer.listen, other)
			continue
		}
		blocked[mapKey] = peer.name
		short := shortID(acts.block[k])
		name := WGBlockChainPrefix + short
		cname := WGBlockChainPrefix + short
		t.Nft.Chains = append(t.Nft.Chains, Chain{Name: name, Rules: []Rule{newRule(counter(cname), verdict("drop"))}})
		t.Nft.Counters = append(t.Nft.Counters, cname)
		t.WGActions = append(t.WGActions, WGActionInfo{Overlay: acts.block[k], Action: "block_endpoint", Tunnel: k, Peer: peer.id, PeerName: peer.name, Counter: cname})
		outEl = append(outEl, MapElement{Key: mapKey, Value: name})
		inEl = append(inEl, MapElement{Key: mapKey, Value: name})
	}
	if len(outEl) == 0 {
		return
	}
	for _, d := range []struct {
		chain, hook, side, mapBase string
		el                         []MapElement
		fields                     []any
	}{
		{WGBlockOutChain, "output", "out", "wgblk_out", outEl, []any{payload("ip", "daddr"), payload("udp", "dport"), payload("udp", "sport")}},
		{WGBlockInChain, "input", "in", "wgblk_in", inEl, []any{payload("ip", "saddr"), payload("udp", "sport"), payload("udp", "dport")}},
	} {
		sort.Slice(d.el, func(i, j int) bool { return d.el[i].Key < d.el[j].Key })
		md := MapDef{KeyType: []string{"ipv4_addr", "inet_service", "inet_service"}, ValueType: "verdict", Elements: d.el}
		md.Name = hashMapName(d.mapBase, md.KeyType, md.ValueType, md.Flags)
		t.Nft.Maps = append(t.Nft.Maps, md)
		t.Nft.Chains = append(t.Nft.Chains, Chain{Name: d.chain,
			Base:  &BaseChain{Type: "filter", Hook: d.hook, Prio: wgBlockPriority, Policy: "accept"},
			Rules: []Rule{newRule(match(meta("l4proto"), "==", "udp"), vmap(concat(d.fields...), md.Name))}})
	}
	sort.Strings(t.Nft.Counters)
}
