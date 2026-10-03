package compiler

import (
	"github.com/Andste82/chaos-gateway/internal/bird"
	"net/netip"
	"sort"

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
func iifname(name string) any   { return eq(meta("iifname"), name) }
func oifname(name string) any   { return eq(meta("oifname"), name) }
func iifSet(set string) any     { return eq(meta("iifname"), setRef(set)) }
func oifSet(set string) any     { return eq(meta("oifname"), setRef(set)) }
func ctState(states ...string) any {
	list := make([]any, len(states))
	for i, s := range states {
		list[i] = s
	}
	return inSet(ctKey("state"), list)
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
	ifsCG := mkSet("ifs_cg", tp.all)      // every interface of a network of Chaos Gateway
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
	sort.Slice(t.Nft.Sets, func(i, j int) bool { return t.Nft.Sets[i].Name < t.Nft.Sets[j].Name })
	t.Nft.Counters = []string{"forward_drop", "input_drop", "ipv6_drop"}

	// ---- input: gateway protection (plan §2.2 layer 1) -------------------------------------
	input := Chain{Name: "input", Base: &BaseChain{Type: "filter", Hook: "input", Prio: 0, Policy: "accept"}}
	input.Rules = append(input.Rules,
		newRule(ctState("established", "related"), verdict("accept")),
		newRule(iifname("lo"), verdict("accept")),
		// anti-lockout: the management sources always reach the control plane; nothing below
		// and no access rule can take this away. A device on a test network that claims a
		// management address does not count: the rule is for what does not come from there.
		newRule(match(meta("iifname"), "!=", setRef(ifsTest.Name)), eq(payload("ip", "saddr"), setRef(mgmtSrc.Name)), eq(payload("tcp", "dport"), controlPorts(t.Management.UIPort)), verdict("accept")),
		// the UI and API are reachable from the management network only (plan §2.2); SSH is left to
		// the operating system
		newRule(eq(payload("tcp", "dport"), t.Management.UIPort), counter("input_drop"), verdict("drop")),
	)
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
	forward.Rules = append(forward.Rules,
		newRule(ctState("established", "related"), verdict("accept")),
	)
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
	explicit, implicit := t.matrixRules(cfg, tp, mgmtSrc.Name)
	forward.Rules = append(forward.Rules, explicit...)
	// default matrix: local test networks may reach the uplink, everything else is denied
	uplink := t.Uplink.Name
	if uplink != "" && t.Management.Name == uplink {
		// two-port topology: the management network lies behind the uplink interface. Devices under
		// test cannot reach it (plan §2.16) unless an explicit entry says so: this guard stands after
		// the explicit entries and before the allows that follow from a client's reachable list and
		// before the default.
		forward.Rules = append(forward.Rules, newRule(iifSet(ifsTest.Name), oifname(uplink), eq(payload("ip", "daddr"), setRef(mgmtSrc.Name)), counter("forward_drop"), verdict("drop")))
	}
	forward.Rules = append(forward.Rules, implicit...)
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
			post.Rules = append(post.Rules, newRule(eq(payload("ip", "saddr"), prefixes(n.prefixes)), oifname(uplink), counter(name), map[string]any{"masquerade": nil}))
		}
	}
	sort.Strings(t.Nft.Counters)
	t.Nft.Chains = []Chain{forward, input, post}

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

func prefixValue(p netip.Prefix) any {
	return map[string]any{"prefix": map[string]any{"addr": p.Addr().String(), "len": p.Bits()}}
}

// matrixRules turns the access matrix into forward rules: the explicit entries first, then the
// implicit allows of the clients' `reachable` lists. Networks (bridges and WireGuard interfaces),
// single WireGuard clients with the networks behind them, the uplink and the management network are
// endpoints. The more specific endpoint comes first: a client before a network, the management
// network (told apart from the uplink by its addresses) before the uplink.
func (t *Target) matrixRules(cfg *model.Configuration, tp *topo, mgmtSet string) (explicit, implicit []Rule) {
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
			return dev(cm.dev, prefixes(cm.prefixes)), 0, true
		case ep.Network != nil:
			d, ok := tp.netDev[*ep.Network]
			if !ok {
				return endpoint{}, 0, false
			}
			return dev(d, nil), 3, true
		case ep.Management != nil && bool(*ep.Management):
			if t.Management.Name == "" {
				return endpoint{}, 0, false
			}
			return dev(t.Management.Name, setRef(mgmtSet)), 1, true
		case ep.Uplink != nil && bool(*ep.Uplink):
			if t.Uplink.Name == "" {
				return endpoint{}, 0, false
			}
			return dev(t.Uplink.Name, nil), 2, true
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
		var expr []any
		expr = append(expr, f.iif...)
		expr = append(expr, d.oif...)
		v := "accept"
		if policy == model.MatrixEntryPolicyDeny {
			expr = append(expr, counter("forward_drop"))
			v = "drop"
		}
		expr = append(expr, verdict(v))
		items = append(items, item{rank: fr*4 + dr, implicit: implicit, rule: newRule(expr...)})
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
	return explicit, implicit
}
