package compiler

import (
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/bird"

	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ---- nftables expression builders ----------------------------------------------------------

func meta(key string) map[string]any { return map[string]any{"meta": map[string]any{"key": key}} }
func payload(proto, field string) map[string]any {
	return map[string]any{"payload": map[string]any{"protocol": proto, "field": field}}
}
func ctKey(key string) map[string]any { return map[string]any{"ct": map[string]any{"key": key}} }

func match(left any, op string, right any) any {
	return map[string]any{"match": map[string]any{"op": op, "left": left, "right": right}}
}

func eq(left, right any) any    { return match(left, "==", right) }
func inSet(left, right any) any { return match(left, "in", right) }
func anon(vals ...any) any      { return map[string]any{"set": vals} }
func setRef(name string) string { return "@" + name }
func counter(name string) any   { return map[string]any{"counter": name} }
func verdict(v string) any      { return map[string]any{v: nil} }
func gotoChain(name string) any { return map[string]any{"goto": map[string]any{"target": name}} }

// lastStmt is the `last` statement (kernel 5.17+): the kernel notes when the rule last matched and
// lists it as `last used <ms>` (plan §2.17, the rule editor's "last match", P3-M13-01). It is compiled
// as "never used"; a transaction carries the time of the rule it replaces over (Nft.Transaction).
func lastStmt() any           { return map[string]any{"last": nil} }
func iifname(name string) any { return eq(meta("iifname"), name) }
func oifname(name string) any { return eq(meta("oifname"), name) }
func iifSet(set string) any   { return eq(meta("iifname"), setRef(set)) }
func oifSet(set string) any   { return eq(meta("oifname"), setRef(set)) }
func ctState(states ...string) any {
	list := make([]any, len(states))
	for i, s := range states {
		list[i] = s
	}
	return inSet(ctKey("state"), list)
}

// ctOriginal is a ct key read in the connection's original direction, unaffected by NAT (plan
// §3.3): the same for a reply as for the packet that opened the connection.
func ctOriginal(key string) any {
	return map[string]any{"ct": map[string]any{"key": key, "dir": "original"}}
}

// ctOriginalIP is ctOriginal for an address field: the "inet" table family needs the protocol
// qualifier to tell an IPv4 from an IPv6 address (nft's `ct original ip saddr`). The qualifier is
// part of the ct key itself ("ip saddr", "ip daddr"), not a separate "family" field: nft's JSON
// parser (src/parser_json.c's json_parse_ct_expr) only ever reads "key" and "dir" for a ct
// expression, so a "family" field (as libnftables-json(5) describes for "rt"/"xfrm" expressions,
// not "ct") is silently ignored, and the bare key "saddr"/"daddr" leaves the protocol version
// undetermined ("cannot determine ip protocol version, use \"ip saddr\" or \"ip6 saddr\"
// instead" - confirmed against nftables' own source, src/ct.c's ct_templates: NFT_CT_SRC_IP's
// token is the literal string "ip saddr").
func ctOriginalIP(key string) any {
	return map[string]any{"ct": map[string]any{"key": "ip " + key, "dir": "original"}}
}

// ctReply and ctReplyIP are ctOriginal and ctOriginalIP in the reply direction: for a connection whose
// destination was translated, the reply comes from the translated address and port (a port forward's
// target, M35a).
func ctReply(key string) any { return map[string]any{"ct": map[string]any{"key": key, "dir": "reply"}} }
func ctReplyIP(key string) any {
	return map[string]any{"ct": map[string]any{"key": "ip " + key, "dir": "reply"}}
}

func bitAnd(a, b any) any { return map[string]any{"&": []any{a, b}} }
func bitOr(a, b any) any  { return map[string]any{"|": []any{a, b}} }
func lshift(a, b any) any { return map[string]any{"<<": []any{a, b}} }

// numgenRandom is `numgen random mod N`: a uniformly random number below N for every packet.
func numgenRandom(mod int64) any {
	return map[string]any{"numgen": map[string]any{"mode": "random", "mod": mod}}
}

// concat is a concatenated key expression (plan §3.3's lookup chain): several fields matched or
// looked up together, e.g. device, destination, protocol and port.
func concat(parts ...any) any { return map[string]any{"concat": parts} }

// markSet is "meta mark set <value>" (the MSS clamp rule already uses the same "mangle"
// statement, for the TCP MSS option instead of the mark).
func markSet(value any) any {
	return map[string]any{"mangle": map[string]any{"key": meta("mark"), "value": value}}
}

// vmap is a verdict-map lookup statement: it goes to the chain the matching key names, or, when
// no key matches, lets the rule fall through to the next one (plan §3.3's first-match lookup
// chain) — confirmed against nft's own parser (`nft -c`), since a map lookup cannot be combined
// with other expressions such as the bitwise ops that write the fault id (`-- Expression type map
// not allowed in context`): every classification level is its own small goto target chain instead.
func vmap(key any, mapName string) any {
	return map[string]any{"vmap": map[string]any{"key": key, "data": setRef(mapName)}}
}

// ---- endpoints of the access matrix --------------------------------------------------------

type endpoint struct {
	iif, oif []any
}

// compileNft builds the table `inet chaosgw`: gateway protection (input), the access matrix and
// the IPv6 block (forward), masquerade (postrouting), the MSS clamp on WireGuard interfaces and
// the generation chain.
func (t *Target) compileNft(cfg *model.Configuration, tp *topo, dynamic []SetDef) {
	mkSet := func(base string, elems []string) SetDef {
		s := SetDef{Type: "ifname", Elements: elems}
		s.Name = hashName(base, s.Type, s.Flags)
		return s
	}
	ifsLan := mkSet("ifs_lan", tp.lan)    // local test networks: what may reach the uplink by default
	ifsTest := mkSet("ifs_test", tp.test) // untrusted: gateway protection applies
	if t.Service != nil {
		tp.all = append(append([]string{}, tp.all...), t.Service.HostIf)
		sort.Strings(tp.all)
	}
	ifsCG := mkSet("ifs_cg", tp.all) // every interface of a network of Chaos Gateway
	var srcs []string
	for _, p := range t.Management.Sources {
		srcs = append(srcs, linux.NormalizeElement(p.String()))
	}
	mgmtSrc := SetDef{Type: "ipv4_addr", Flags: []string{"interval"}, Elements: srcs}
	mgmtSrc.Name = hashName("mgmt_src", mgmtSrc.Type, mgmtSrc.Flags)

	t.Nft.Sets = []SetDef{ifsLan, ifsTest, ifsCG, mgmtSrc}
	var ifsWG SetDef
	if len(tp.wg) > 0 {
		ifsWG = mkSet("ifs_wg", tp.wg)
		t.Nft.Sets = append(t.Nft.Sets, ifsWG)
	}
	t.Nft.Sets = append(t.Nft.Sets, dynamic...)
	t.Nft.Sets = append(t.Nft.Sets, t.accessSets...)
	t.Nft.Sets = append(t.Nft.Sets, t.hostSetDefs...)
	if t.identityMap.Name != "" {
		t.Nft.Maps = append(t.Nft.Maps, t.identityMap)
	}
	if t.ForwardMap != "" {
		t.Nft.Maps = append(t.Nft.Maps, t.forwardMap)
	}
	sort.Slice(t.Nft.Sets, func(i, j int) bool { return t.Nft.Sets[i].Name < t.Nft.Sets[j].Name })
	t.Nft.Counters = []string{"forward_drop", "input_drop", "ipv6_drop", AntiLockoutCounter}
	if t.Access != nil {
		for _, r := range t.Access.Rules {
			t.Nft.Counters = append(t.Nft.Counters, r.Counter)
		}
	}
	for _, f := range t.Forwards {
		t.Nft.Counters = append(t.Nft.Counters, f.Counter)
	}

	// ---- input: gateway protection (plan §2.2 layer 1) -------------------------------------
	input := Chain{Name: "input", Base: &BaseChain{Type: "filter", Hook: "input", Prio: 0, Policy: "accept"}}
	if t.Access.HasCuts() {
		// the window of "also cut existing connections" runs in front of everything (access.go)
		input.Rules = append(input.Rules, newRule(map[string]any{"jump": map[string]any{"target": CutInputChain}}))
	}
	input.Rules = append(input.Rules,
		newRule(ctState("established", "related"), verdict("accept")),
		newRule(iifname("lo"), verdict("accept")),
	)
	// the gateway services answer through svc0 and reach only the internal API (these rules stand
	// before the UI port rule below)
	input.Rules = append(input.Rules, t.serviceInput()...)
	antiLockoutMatch := []any{match(meta("iifname"), "!=", setRef(ifsTest.Name)), eq(payload("ip", "saddr"), setRef(mgmtSrc.Name)), eq(payload("tcp", "dport"), controlPorts(t.Management.UIPort))}
	antiLockout := newRule(append(append([]any(nil), antiLockoutMatch...), counter(AntiLockoutCounter), lastStmt(), verdict("accept"))...)
	if t.Access != nil {
		t.Access.guard = antiLockoutMatch
		t.Access.describePath(t)
	}
	input.Rules = append(input.Rules,
		// anti-lockout: the management sources always reach the control plane; nothing below
		// and no access rule can take this away. A device on a test network that claims a
		// management address does not count: the rule is for what does not come from there.
		antiLockout,
		// the UI and API are reachable from the management network only (plan §2.2); SSH is left to
		// the operating system
		newRule(eq(payload("tcp", "dport"), t.Management.UIPort), counter("input_drop"), verdict("drop")),
	)
	// the access rules (access.go): after the anti-lockout rule and the UI port, before the routing
	// protocols and the gateway's protection of the test networks, so a rule can block DNS or BGP
	if t.Access != nil {
		input.Rules = append(input.Rules, newRule(map[string]any{"jump": map[string]any{"target": AccessInputChain}}))
	}
	// the routing protocols the gateway runs answer on their link's interface (plan §2.2.2): BGP,
	// OSPF and Babel are the only traffic besides DHCP, DNS and ping that a test-role link may send to
	// the gateway
	if t.Bird != nil {
		for _, p := range t.Bird.Config.Protocols {
			switch p.Type {
			case "bgp":
				input.Rules = append(input.Rules, newRule(iifname(p.Interface), eq(payload("tcp", "dport"), bird.BGPPort), verdict("accept")))
			case "ospf":
				input.Rules = append(input.Rules, newRule(iifname(p.Interface), eq(meta("l4proto"), bird.OSPFProtocol), verdict("accept")))
			case "babel":
				input.Rules = append(input.Rules, newRule(iifname(p.Interface), eq(payload("udp", "dport"), bird.BabelPort), verdict("accept")))
			}
		}
	}
	input.Rules = append(input.Rules,
		// test networks: only DHCP, DNS and ICMP echo are answered
		newRule(iifSet(ifsTest.Name), eq(payload("ip", "protocol"), "icmp"), eq(payload("icmp", "type"), "echo-request"), verdict("accept")),
		newRule(iifSet(ifsTest.Name), eq(payload("udp", "dport"), 67), verdict("accept")),
		newRule(iifSet(ifsTest.Name), eq(payload("udp", "dport"), 53), verdict("accept")),
		newRule(iifSet(ifsTest.Name), eq(payload("tcp", "dport"), 53), verdict("accept")),
		newRule(iifSet(ifsTest.Name), counter("input_drop"), verdict("drop")),
	)

	// ---- forward: IPv6 block and the access matrix (layers 2) ------------------------------
	forward := Chain{Name: "forward", Base: &BaseChain{Type: "filter", Hook: "forward", Prio: 0, Policy: "accept"}}
	if t.Access.HasCuts() {
		forward.Rules = append(forward.Rules, newRule(map[string]any{"jump": map[string]any{"target": CutForwardChain}}))
	}
	forward.Rules = append(forward.Rules,
		newRule(ctState("established", "related"), verdict("accept")),
	)
	// a packet for the service namespace's subnet that does not leave through svc0 is dropped, so
	// selected traffic fails closed when the namespace is missing
	forward.Rules = append(forward.Rules, t.serviceForwardGuard()...)
	// with br_netfilter loaded, traffic that is switched inside one test network passes the
	// forward hook with the same interface in and out: it is never ours to impair or drop (plan §2.2)
	for _, b := range t.Bridges {
		forward.Rules = append(forward.Rules, newRule(iifname(b.Name), oifname(b.Name), verdict("accept")))
	}
	forward.Rules = append(forward.Rules,
		newRule(iifSet(ifsCG.Name), ctState("invalid"), verdict("drop")),
		newRule(oifSet(ifsCG.Name), ctState("invalid"), verdict("drop")),
		// the V1 networks are IPv4 only: forwarded IPv6 is dropped (plan §2.2.2)
		newRule(iifSet(ifsCG.Name), eq(meta("nfproto"), "ipv6"), counter("ipv6_drop"), verdict("drop")),
		newRule(oifSet(ifsCG.Name), eq(meta("nfproto"), "ipv6"), counter("ipv6_drop"), verdict("drop")),
	)
	explicit, implicit, mgmtOpen := t.matrixRules(cfg, tp, mgmtSrc.Name)
	uplink := t.Uplink.Name
	// the management guard (plan §2.16): devices under test (a packet that comes in on a test
	// interface: a local test network or a WireGuard network of the role test) do not open a
	// connection into the management network. The guard stands in front of the access rules, as the
	// anti-lockout rule does for the control plane: no rule, an allow rule included, opens the
	// management network for them. Only an explicit matrix entry that allows the traffic does: it
	// returns from the guard chain (the matrix entries stay where they were, behind the rules). The
	// replies, of a forward from the management side too, are accepted before it (established).
	guard := Chain{Name: MgmtGuardChain}
	guard.Rules = append(guard.Rules, mgmtOpen...)
	drop := func(m ...any) Rule {
		return newRule(append(append([]any{iifSet(ifsTest.Name)}, m...), counter("forward_drop"), verdict("drop"))...)
	}
	if t.twoPort() {
		// two-port topology: the management network lies behind the uplink interface, told apart
		// from the uplink by its addresses
		guard.Rules = append(guard.Rules, drop(oifname(uplink), eq(payload("ip", "daddr"), setRef(mgmtSrc.Name))))
	} else {
		// three-port topology: the policy table 100 has no route to the management network, but routing
		// alone does not keep a device out (P31-M35a-10, decided on 2026-10-17): without its default
		// (the uplink lost its address) a packet falls through to the main table, which knows the
		// management subnet, and a router behind the uplink may know it too. So the guard drops by the
		// destination, whatever interface it leaves through, and anything that leaves through the
		// management interface.
		guard.Rules = append(guard.Rules, drop(eq(payload("ip", "daddr"), setRef(mgmtSrc.Name))))
		if sub := t.Management.Subnet; sub.IsValid() && !coveredBy(sub, t.Management.Sources) {
			guard.Rules = append(guard.Rules, drop(eq(payload("ip", "daddr"), prefixValue(sub))))
		}
		if t.Management.Name != "" {
			guard.Rules = append(guard.Rules, drop(oifname(t.Management.Name)))
		}
	}
	// both topologies: the WireGuard networks of role management are part of the management network
	// (P31-AUDIT-11, decided 2026-10-18), whatever lies behind their clients
	for _, w := range tp.mgmtWG {
		guard.Rules = append(guard.Rules, drop(oifname(w)))
	}
	forward.Rules = append(forward.Rules, newRule(map[string]any{"jump": map[string]any{"target": MgmtGuardChain}}))
	// the access rules stand in front of the access matrix: an allow rule is an exception to it, a
	// drop, reject or reset rule decides before the matrix is asked (plan §2.2, §2.4)
	if t.Access != nil {
		forward.Rules = append(forward.Rules, newRule(map[string]any{"jump": map[string]any{"target": AccessForwardChain}}))
	}
	// DNS over TLS of the networks that block it (plan §2.6): after the rules, so an allow rule is an exception
	dot := t.dotBlock()
	if dot != nil {
		forward.Rules = append(forward.Rules, newRule(map[string]any{"jump": map[string]any{"target": DoTChain}}))
	}
	// the port forwards (forward.go): a forwarded connection that no access rule decided is accepted
	// here, in front of the access matrix, so the forward itself is the allowance (plan §2.2 layer 4)
	forward.Rules = append(forward.Rules, t.forwardAccepts(ifsLan.Name)...)
	forward.Rules = append(forward.Rules, explicit...)
	// default matrix: local test networks may reach the uplink, everything else is denied
	forward.Rules = append(forward.Rules, implicit...)
	forward.Rules = append(forward.Rules, t.serviceForward(ifsCG.Name, uplink)...)
	if uplink != "" {
		forward.Rules = append(forward.Rules, newRule(iifSet(ifsLan.Name), oifname(uplink), verdict("accept")))
	}
	forward.Rules = append(forward.Rules,
		newRule(iifSet(ifsCG.Name), counter("forward_drop"), verdict("drop")),
		newRule(oifSet(ifsCG.Name), counter("forward_drop"), verdict("drop")),
	)

	// ---- postrouting: masquerade per network (layer 4) -----------------------------------
	post := Chain{Name: "postrouting", Base: &BaseChain{Type: "nat", Hook: "postrouting", Prio: 100, Policy: "accept"}}
	if uplink != "" {
		for _, n := range tp.nat {
			name := "nat_" + shortID(n.id)
			t.Nft.Counters = append(t.Nft.Counters, name)
			match := eq(payload("ip", "saddr"), prefixes(n.prefixes))
			if n.dev != "" {
				match = iifname(n.dev)
			}
			post.Rules = append(post.Rules, newRule(match, oifname(uplink), counter(name), map[string]any{"masquerade": nil}))
		}
	}
	if r := t.serviceMasquerade(uplink); r != nil {
		post.Rules = append(post.Rules, *r)
	}
	sort.Strings(t.Nft.Counters)
	t.Nft.Chains = []Chain{forward, input, post, guard}
	if dot != nil {
		t.Nft.Chains = append(t.Nft.Chains, *dot)
	}
	if t.Access != nil {
		t.Nft.Chains = append(t.Nft.Chains, t.Access.chain(AccessForwardChain), t.Access.chain(AccessInputChain))
		if t.Access.HasCuts() {
			// empty until the engine opens a window (CutRules)
			t.Nft.Chains = append(t.Nft.Chains, Chain{Name: CutForwardChain}, Chain{Name: CutInputChain})
		}
	}
	// the prerouting NAT chain: the service redirect (M6b), the counting rules and the DNAT of the port
	// forwards (M35a)
	if c := t.prerouting(tp); c != nil {
		t.Nft.Chains = append(t.Nft.Chains, *c)
	}
	// the mark of the replies of a forward from the management side (forward.go)
	if c := t.forwardReplyChain(ifsLan.Name); c != nil {
		t.Nft.Chains = append(t.Nft.Chains, *c)
	}

	// ---- MSS clamp on WireGuard interfaces (plan §2.2.1) ----------------------------------
	if len(tp.wg) > 0 {
		clamp := map[string]any{"mangle": map[string]any{
			"key":   map[string]any{"tcp option": map[string]any{"name": "maxseg", "field": "size"}},
			"value": map[string]any{"rt": map[string]any{"key": "mtu"}},
		}}
		syn := eq(map[string]any{"&": []any{payload("tcp", "flags"), "syn"}}, "syn")
		mss := Chain{Name: "mss", Base: &BaseChain{Type: "filter", Hook: "forward", Prio: -150, Policy: "accept"}}
		mss.Rules = append(mss.Rules,
			newRule(oifSet(ifsWG.Name), syn, counter("mss_clamp"), clamp),
			newRule(iifSet(ifsWG.Name), syn, counter("mss_clamp"), clamp),
		)
		t.Nft.Chains = append(t.Nft.Chains, mss)
		t.Nft.Counters = append(t.Nft.Counters, "mss_clamp")
		sort.Strings(t.Nft.Counters)
	}

	// ---- classification (plan §3.3, M7): the fault-id/direction mark on prerouting, for test,
	// WireGuard and remote-network traffic only ---------------------------------------------
	t.compileClassify()
	sort.Slice(t.Nft.Maps, func(i, j int) bool { return t.Nft.Maps[i].Name < t.Nft.Maps[j].Name })
	sort.Slice(t.Nft.Sets, func(i, j int) bool { return t.Nft.Sets[i].Name < t.Nft.Sets[j].Name })

	// sorted by name, as the kernel lists them: the preview diff compares line by line
	sort.Slice(t.Nft.Chains, func(i, j int) bool { return t.Nft.Chains[i].Name < t.Nft.Chains[j].Name })
}

// prefixes is the value of an address match: one prefix, or a set of them.
func prefixes(p []netip.Prefix) any {
	if len(p) == 1 {
		return prefixValue(p[0])
	}
	vals := make([]any, len(p))
	for i, x := range p {
		vals[i] = prefixValue(x)
	}
	return anon(vals...)
}

// controlPorts is SSH and the UI port as one match value: a set, or the port alone when they are
// the same.
func controlPorts(ui int) any {
	if ui == 22 {
		return 22
	}
	return anon(22, ui)
}

func shortID(id string) string {
	out := make([]byte, 0, 8)
	for i := 0; i < len(id) && len(out) < 8; i++ {
		if id[i] != '-' {
			out = append(out, id[i])
		}
	}
	return string(out)
}

// coveredBy reports whether one of ps contains all of p.
// twoPort reports the two-port topology: the management network lies behind the uplink interface
// (plan §2.16).
func (t *Target) twoPort() bool {
	return t.Uplink.Name != "" && t.Management.Name == t.Uplink.Name
}

func coveredBy(p netip.Prefix, ps []netip.Prefix) bool {
	for _, q := range ps {
		if q.Bits() <= p.Bits() && q.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

func prefixValue(p netip.Prefix) any {
	return map[string]any{"prefix": map[string]any{"addr": p.Addr().String(), "len": p.Bits()}}
}

// The ranks of the matrix endpoints: the more specific endpoint is evaluated first. rankMgmtWG is what the
// endpoint `management` stands for through the WireGuard networks of the role management (P31-AUDIT-11): it
// comes after the network rank, so an entry towards (or from) such a network or one of its clients decides
// before an entry towards (or from) the management network as a whole.
const (
	rankClient = iota
	rankManagement
	rankUplink
	rankNetwork
	rankMgmtWG
	rankCount
)

// matrixRules turns the access matrix into forward rules: the explicit entries first, then the
// implicit allows of the clients' `reachable` lists. mgmtOpen are the explicit entries that allow
// traffic to the management network, as rules that return from the management guard. Networks (bridges and WireGuard interfaces),
// single WireGuard clients with the networks behind them, the uplink and the management network are
// endpoints. The more specific endpoint comes first: a client before a network, the management
// network (told apart from the uplink by its addresses) before the uplink, and what the management network
// stands for through its WireGuard networks of the role management after the networks (rankMgmtWG).
func (t *Target) matrixRules(cfg *model.Configuration, tp *topo, mgmtSet string) (explicit, implicit, mgmtOpen []Rule) {
	resolve := func(ep model.MatrixEndpoint, from bool) (endpoint, int, bool) {
		dev := func(name string, addrs any) endpoint {
			var e endpoint
			side, field := "oifname", "daddr"
			if from {
				side, field = "iifname", "saddr"
			}
			m := eq(meta(side), name)
			parts := []any{m}
			if addrs != nil {
				parts = append(parts, eq(payload("ip", field), addrs))
			}
			if from {
				e.iif = parts
			} else {
				e.oif = parts
			}
			return e
		}
		switch {
		case ep.Client != nil:
			cm, ok := tp.clients[*ep.Client]
			if !ok {
				return endpoint{}, 0, false
			}
			return dev(cm.dev, prefixes(cm.prefixes)), rankClient, true
		case ep.Network != nil:
			d, ok := tp.netDev[*ep.Network]
			if !ok {
				return endpoint{}, 0, false
			}
			return dev(d, nil), rankNetwork, true
		case ep.Management != nil && bool(*ep.Management):
			if t.Management.Name == "" {
				return endpoint{}, 0, false
			}
			if !from && !t.twoPort() {
				// three-port topology: the management network as a destination is told by its addresses
				// only. The policy table has no route to it (plan §2.2), so a packet that an entry lets
				// through leaves through the uplink (a router behind it may know the management network),
				// not through the management interface (P31-AUDIT-01). What leaves through a WireGuard network
				// of the role management is left out: its tunnel subnet is one of the allowed sources, but it is
				// matched by the interface with rankMgmtWG, after an entry towards the network itself.
				oif := []any{eq(payload("ip", "daddr"), setRef(mgmtSet))}
				if len(tp.mgmtWG) > 0 {
					oif = append(oif, match(meta("oifname"), "!=", ifnames(tp.mgmtWG)))
				}
				return endpoint{oif: oif}, rankManagement, true
			}
			return dev(t.Management.Name, setRef(mgmtSet)), rankManagement, true
		case ep.Uplink != nil && bool(*ep.Uplink):
			if t.Uplink.Name == "" {
				return endpoint{}, 0, false
			}
			return dev(t.Uplink.Name, nil), rankUplink, true
		}
		return endpoint{}, 0, false
	}
	type item struct {
		rank     int
		implicit bool
		rule     Rule
	}
	var items []item
	add := func(from, to model.MatrixEndpoint, policy model.MatrixEntryPolicy, implicit bool) {
		f, fr, ok1 := resolve(from, true)
		d, dr, ok2 := resolve(to, false)
		if !ok1 || !ok2 {
			t.warn(CodeUnsupported, "", "an access matrix entry with an endpoint that does not exist (or is not available) is not compiled")
			return
		}
		toMgmt := to.Management != nil && bool(*to.Management)
		fromMgmt := from.Management != nil && bool(*from.Management)
		type ranked struct {
			e    endpoint
			rank int
		}
		srcs, dests := []ranked{{f, fr}}, []ranked{{d, dr}}
		// the WireGuard networks of role management are part of the management network (P31-AUDIT-11,
		// decided 2026-10-18): the endpoint also stands for what leaves through their interfaces and for what
		// comes in through them, in both topologies, ranked after the networks (rankMgmtWG)
		if toMgmt {
			for _, w := range tp.mgmtWG {
				dests = append(dests, ranked{endpoint{oif: []any{oifname(w)}}, rankMgmtWG})
			}
		}
		if fromMgmt {
			for _, w := range tp.mgmtWG {
				srcs = append(srcs, ranked{endpoint{iif: []any{iifname(w)}}, rankMgmtWG})
			}
		}
		if !implicit && (toMgmt || fromMgmt) && len(tp.mgmtWG) > 0 {
			// before 2026-10-18 the endpoint stood for the management interface and the allowed sources only:
			// an entry kept from then covers more now, so the compiler says so (the upgrade note of plan §2.16)
			t.warn(CodeMatrixManagementWG, "", "the access matrix entry %s -> %s also covers the WireGuard %s of the role management %s, which %s part of the management network (P31-AUDIT-11)",
				t.endpointName(from), t.endpointName(to), pluralNetworks(len(tp.mgmtWG)), t.mgmtWGNames(), isAre(len(tp.mgmtWG)))
		}
		if implicit && (toMgmt || tp.inMgmtWG(to)) && from.Client != nil && !slices.Contains(tp.mgmtWG, tp.clients[*from.Client].dev) {
			// a test client's `reachable` list is no explicit entry: the management guard drops what it would open
			t.warn(CodeReachableGuarded, "", "the client %s names %s in its reachable list, but the management guard drops a device under test there: only an explicit access matrix entry opens it",
				t.endpointName(from), t.endpointName(to))
		}
		// an explicit entry that allows traffic to the management network, or to a WireGuard network of
		// role management or one of its clients, returns from the management guard by its destination
		opens := !implicit && policy != model.MatrixEntryPolicyDeny && (toMgmt || tp.inMgmtWG(to))
		for j, s := range srcs {
			for i, d := range dests {
				var expr []any
				expr = append(expr, s.e.iif...)
				expr = append(expr, d.e.oif...)
				v := "accept"
				if policy == model.MatrixEntryPolicyDeny {
					expr = append(expr, counter("forward_drop"))
					v = "drop"
				}
				// the guard drops only what comes in on a test interface: what comes in through a WireGuard
				// network of the role management needs no return
				if opens && j == 0 {
					mgmtOpen = append(mgmtOpen, newRule(append(append([]any(nil), expr...), verdict("return"))...))
					if sub := t.Management.Subnet; i == 0 && toMgmt && !t.twoPort() && sub.IsValid() && !coveredBy(sub, t.Management.Sources) {
						// the three-port guard drops the management subnet too: the entry opens what the guard
						// drops (P31-AUDIT-01)
						mgmtOpen = append(mgmtOpen, newRule(append(append([]any(nil), s.e.iif...), eq(payload("ip", "daddr"), prefixValue(sub)), verdict("return"))...))
					}
				}
				expr = append(expr, verdict(v))
				items = append(items, item{rank: s.rank*rankCount + d.rank, implicit: implicit, rule: newRule(expr...)})
			}
		}
	}
	if cfg.AccessMatrix != nil && cfg.AccessMatrix.Entries != nil {
		for _, e := range *cfg.AccessMatrix.Entries {
			add(e.From, e.To, e.Policy, false)
		}
	}
	for _, r := range tp.reach {
		c := r.client
		add(model.MatrixEndpoint{Client: &c}, r.ep, model.MatrixEntryPolicyAllow, true)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].implicit != items[j].implicit {
			return !items[i].implicit // explicit entries win over what a client's list implies
		}
		return items[i].rank < items[j].rank
	})
	for _, it := range items {
		if it.implicit {
			implicit = append(implicit, it.rule)
		} else {
			explicit = append(explicit, it.rule)
		}
	}
	return explicit, implicit, mgmtOpen
}

// ifnames is the value of an interface name match: one name, or a set of them.
func ifnames(names []string) any {
	if len(names) == 1 {
		return names[0]
	}
	vals := make([]any, len(names))
	for i, n := range names {
		vals[i] = n
	}
	return anon(vals...)
}

// CodeMatrixManagementWG is the warning for an explicit access matrix entry from or towards the management
// network while WireGuard networks of the role management exist: since 2026-10-18 the endpoint covers them
// too (P31-AUDIT-11), so an entry written before covers more than it did (plan §2.16).
const CodeMatrixManagementWG = "matrix_management_wireguard"

// CodeReachableGuarded is the warning for a client of a test-role WireGuard network that names the management
// network, a WireGuard network of the role management or one of its clients in its `reachable` list: the list is
// an implicit entry, and the management guard drops that traffic until an explicit matrix entry opens it.
const CodeReachableGuarded = "reachable_guarded"

// endpointName names a matrix endpoint in a message: the network's or the client's name.
func (t *Target) endpointName(ep model.MatrixEndpoint) string {
	switch {
	case ep.Client != nil:
		for _, w := range t.WireGuard {
			for _, p := range w.Peers {
				if p.ID == *ep.Client {
					return "client " + p.Name + " (" + w.NetworkName + ")"
				}
			}
		}
		return "client " + *ep.Client
	case ep.Network != nil:
		for _, b := range t.Bridges {
			if b.NetworkID == *ep.Network {
				return "network " + b.NetworkName
			}
		}
		for _, w := range t.WireGuard {
			if w.NetworkID == *ep.Network {
				return "network " + w.NetworkName
			}
		}
		return "network " + *ep.Network
	case ep.Management != nil && bool(*ep.Management):
		return "management"
	case ep.Uplink != nil && bool(*ep.Uplink):
		return "uplink"
	}
	return "?"
}

// mgmtWGNames lists the names of the WireGuard networks of the role management.
func (t *Target) mgmtWGNames() string {
	var names []string
	for _, w := range t.WireGuard {
		if w.Role == "management" {
			names = append(names, w.NetworkName)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func pluralNetworks(n int) string {
	if n == 1 {
		return "network"
	}
	return "networks"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
