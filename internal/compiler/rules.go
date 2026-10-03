package compiler

import (
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
// the IPv6 block (forward), masquerade (postrouting) and the generation chain.
func (t *Target) compileNft(cfg *model.Configuration, nets map[string]*Bridge, dynamic []SetDef) {
	var testIfs []string
	for _, b := range t.Bridges {
		testIfs = append(testIfs, b.Name)
	}
	ifsTest := SetDef{Type: "ifname", Elements: testIfs}
	ifsTest.Name = hashName("ifs_test", ifsTest.Type, ifsTest.Flags)
	var srcs []string
	for _, p := range t.Management.Sources {
		srcs = append(srcs, linux.NormalizeElement(p.String()))
	}
	mgmtSrc := SetDef{Type: "ipv4_addr", Flags: []string{"interval"}, Elements: srcs}
	mgmtSrc.Name = hashName("mgmt_src", mgmtSrc.Type, mgmtSrc.Flags)

	t.Nft.Sets = []SetDef{ifsTest, mgmtSrc}
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
		newRule(iifSet(ifsTest.Name), ctState("invalid"), verdict("drop")),
		newRule(oifSet(ifsTest.Name), ctState("invalid"), verdict("drop")),
		// the V1 test networks are IPv4 only: forwarded IPv6 is dropped (plan §2.2.2)
		newRule(iifSet(ifsTest.Name), eq(meta("nfproto"), "ipv6"), counter("ipv6_drop"), verdict("drop")),
		newRule(oifSet(ifsTest.Name), eq(meta("nfproto"), "ipv6"), counter("ipv6_drop"), verdict("drop")),
	)
	forward.Rules = append(forward.Rules, t.matrixRules(cfg, nets, mgmtSrc.Name)...)
	// default matrix: local test networks may reach the uplink, everything else is denied
	uplink := t.Uplink.Name
	if uplink != "" {
		if t.Management.Name == uplink {
			// two-port topology: the management network lies behind the uplink interface and is
			// not reachable from test networks unless the matrix says so
			forward.Rules = append(forward.Rules, newRule(iifSet(ifsTest.Name), oifname(uplink), eq(payload("ip", "daddr"), setRef(mgmtSrc.Name)), counter("forward_drop"), verdict("drop")))
		}
		forward.Rules = append(forward.Rules, newRule(iifSet(ifsTest.Name), oifname(uplink), verdict("accept")))
	}
	forward.Rules = append(forward.Rules,
		newRule(iifSet(ifsTest.Name), counter("forward_drop"), verdict("drop")),
		newRule(oifSet(ifsTest.Name), counter("forward_drop"), verdict("drop")),
	)

	// ---- postrouting: masquerade per network (layer 4) -----------------------------------
	post := Chain{Name: "postrouting", Base: &BaseChain{Type: "nat", Hook: "postrouting", Prio: 100, Policy: "accept"}}
	if uplink != "" {
		for _, b := range t.Bridges {
			if !b.NAT {
				continue
			}
			name := "nat_" + shortID(b.NetworkID)
			t.Nft.Counters = append(t.Nft.Counters, name)
			post.Rules = append(post.Rules, newRule(eq(payload("ip", "saddr"), prefixValue(b.Address.Masked())), oifname(uplink), counter(name), map[string]any{"masquerade": nil}))
		}
	}
	sort.Strings(t.Nft.Counters)
	t.Nft.Chains = []Chain{forward, input, post}
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

// matrixRules turns the explicit entries of the access matrix into forward rules. Entries between
// networks, the uplink and the management network are supported; WireGuard endpoints follow with
// M4b. The management endpoint is told apart from the uplink by the source or destination
// address, so its rules come first.
func (t *Target) matrixRules(cfg *model.Configuration, nets map[string]*Bridge, mgmtSet string) []Rule {
	if cfg.AccessMatrix == nil || cfg.AccessMatrix.Entries == nil {
		return nil
	}
	resolve := func(ep model.MatrixEndpoint, from bool) (endpoint, int, bool) {
		dev := func(kind string, name string, addrSet bool) endpoint {
			var e endpoint
			if from {
				e.iif = []any{iifname(name)}
				if addrSet {
					e.iif = append(e.iif, eq(payload("ip", "saddr"), setRef(mgmtSet)))
				}
			} else {
				e.oif = []any{oifname(name)}
				if addrSet {
					e.oif = append(e.oif, eq(payload("ip", "daddr"), setRef(mgmtSet)))
				}
			}
			return e
		}
		switch {
		case ep.Network != nil:
			b := nets[*ep.Network]
			if b == nil {
				return endpoint{}, 0, false
			}
			return dev("lan", b.Name, false), 2, true
		case ep.Management != nil && bool(*ep.Management):
			if t.Management.Name == "" {
				return endpoint{}, 0, false
			}
			return dev("mgmt", t.Management.Name, true), 0, true
		case ep.Uplink != nil && bool(*ep.Uplink):
			if t.Uplink.Name == "" {
				return endpoint{}, 0, false
			}
			return dev("uplink", t.Uplink.Name, false), 1, true
		}
		return endpoint{}, 0, false
	}
	type item struct {
		rank int
		rule Rule
	}
	var items []item
	for _, e := range *cfg.AccessMatrix.Entries {
		from, fr, ok1 := resolve(e.From, true)
		to, tr, ok2 := resolve(e.To, false)
		if !ok1 || !ok2 {
			t.warn(CodeUnsupported, "", "an access matrix entry with a WireGuard client or an unknown endpoint is not compiled before M4b")
			continue
		}
		var expr []any
		expr = append(expr, from.iif...)
		expr = append(expr, to.oif...)
		v := "accept"
		if e.Policy == model.MatrixEntryPolicyDeny {
			expr = append(expr, counter("forward_drop"))
			v = "drop"
		}
		expr = append(expr, verdict(v))
		items = append(items, item{rank: fr*3 + tr, rule: newRule(expr...)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].rank < items[j].rank })
	var out []Rule
	for _, it := range items {
		out = append(out, it.rule)
	}
	return out
}
