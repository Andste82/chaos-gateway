package apply

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// LinuxDiff is the preview's view of the Linux changes: unified diffs of the normalized state per
// subsystem, empty when nothing changes (plan §2.14, the `Preview.linux` object of the API).
type LinuxDiff struct {
	// Nftables covers the table inet chaosgw: sets with their elements, counters, chains and their
	// rules (the generation chain is left out, it changes at every apply).
	Nftables string
	// Routes covers what the plan sets up outside nftables: bridges, ports and addresses, sysctls,
	// offloads, policy rules and the routes in Chaos Gateway's tables.
	Routes string
	// WireGuard covers the WireGuard interfaces: address, MTU, listen port and peers (without
	// private keys).
	WireGuard string
	// Bird is the running BIRD configuration text against the target's (or, with routing switched
	// off, the idle text); empty when the executor has no BIRD directory.
	Bird string
}

// line is one line of a normalized rendering. Key decides equality; Text is what the diff shows
// (a rule is recognized by its hash in the kernel, but shown with its expression when it is new).
type line struct{ Key, Text string }

// Diff compares the target with the state and renders both normalized.
func Diff(t *compiler.Target, s *State) LinuxDiff {
	return LinuxDiff{
		Nftables:  unified(nftLines(s.Nft), targetNftLines(t)),
		Routes:    unified(hostLines(s, t), targetHostLines(t)),
		WireGuard: unified(wgLines(s, t), targetWGLines(t)),
		Bird:      birdDiff(t, s),
	}
}

// birdDiff compares the running BIRD configuration with the target's, or, when the target has no
// routing, with the idle configuration that withdraws everything.
func birdDiff(t *compiler.Target, s *State) string {
	if s.Bird == nil {
		return ""
	}
	want := idleText
	if t.Bird != nil {
		want = t.Bird.Text
	}
	return unified(textLines(s.Bird.Config), textLines(want))
}

func textLines(text string) []line {
	if text == "" {
		return nil
	}
	parts := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := make([]line, len(parts))
	for i, p := range parts {
		out[i] = line{p, p}
	}
	return out
}

func targetNftLines(t *compiler.Target) []line {
	var out []line
	add := func(s string) { out = append(out, line{s, s}) }
	for _, c := range t.Nft.Counters {
		add("counter " + c)
	}
	for _, s := range t.Nft.Sets {
		add(fmt.Sprintf("set %s type %s%s", s.Name, s.Type, flagText(s.Flags)))
		if !s.Dynamic {
			els := make([]string, 0, len(s.Elements))
			for _, e := range s.Elements {
				els = append(els, linux.NormalizeElement(e))
			}
			sort.Strings(els)
			for _, e := range els {
				add("  element " + e)
			}
		}
	}
	for _, c := range t.Nft.AllChains() {
		if c.Name == compiler.GenerationChain {
			continue
		}
		add("chain " + c.Name + baseText(c.Base))
		for _, r := range c.Rules {
			b, _ := json.Marshal(r.Expr)
			out = append(out, line{Key: "  rule " + r.Comment, Text: "  rule " + r.Comment + " " + string(b)})
		}
	}
	return out
}

func baseText(b *compiler.BaseChain) string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf(" type %s hook %s prio %d policy %s", b.Type, b.Hook, b.Prio, b.Policy)
}

func flagText(f []string) string {
	if len(f) == 0 {
		return ""
	}
	return " flags " + strings.Join(f, ",")
}

func nftLines(rs *linux.Ruleset) []line {
	var out []line
	add := func(s string) { out = append(out, line{s, s}) }
	for _, c := range rs.Counters() {
		add("counter " + c)
	}
	var names []string
	byName := map[string]*linux.NftSet{}
	for _, o := range rs.Objects {
		if o.Set != nil {
			names = append(names, o.Set.Name)
			byName[o.Set.Name] = o.Set
		}
	}
	sort.Strings(names)
	for _, n := range names {
		s := byName[n]
		typ := strings.Trim(string(s.Type), `"`)
		add(fmt.Sprintf("set %s type %s%s", n, typ, flagText(s.Flags)))
		if dynamicName(n) {
			continue
		}
		for _, e := range s.Elements() {
			add("  element " + e)
		}
	}
	var chains []string
	for _, o := range rs.Objects {
		if o.Chain != nil && o.Chain.Name != compiler.GenerationChain {
			chains = append(chains, o.Chain.Name)
		}
	}
	sort.Strings(chains)
	for _, n := range chains {
		c := rs.Chain(n)
		var b *compiler.BaseChain
		if c.Hook != "" {
			prio := 0
			if c.Prio != nil {
				prio = *c.Prio
			}
			b = &compiler.BaseChain{Type: c.Type, Hook: c.Hook, Prio: prio, Policy: c.Policy}
		}
		add("chain " + n + baseText(b))
		for _, r := range rs.Rules(n) {
			add("  rule " + r.Comment)
		}
	}
	return out
}

// dynamicName reports whether a set is filled at run time; its elements are not part of the diff.
func dynamicName(n string) bool { return strings.HasPrefix(n, "dns_") }

func targetHostLines(t *compiler.Target) []line {
	var out []line
	add := func(s string) { out = append(out, line{s, s}) }
	add("sysctl ip_forward=1")
	for _, b := range t.Bridges {
		add("bridge " + b.Name + " up")
		add("  address " + b.Address.String())
		for _, p := range b.Ports {
			add("  port " + p)
		}
	}
	for _, e := range t.Sysctls {
		if e.Dev != "" {
			add(fmt.Sprintf("sysctl %s:%s=%d", e.Name, e.Dev, e.Value))
		}
	}
	for _, d := range t.Offloads {
		add("offloads off " + d)
	}
	var rl, rt []string
	for _, r := range t.Rules {
		rl = append(rl, fmt.Sprintf("rule %d iif %s lookup %d", r.Priority, r.Iif, r.Table))
	}
	for _, r := range t.Routes {
		rt = append(rt, fmt.Sprintf("route table %d %s%s", r.Table, r.Dst, viaDev(r)))
	}
	sort.Strings(rl)
	sort.Strings(rt)
	for _, l := range append(rl, rt...) {
		add(l)
	}
	for _, d := range t.DockerUser { // sorted by the compiler
		add("docker-user accept " + d)
	}
	return out
}

func targetWGLines(t *compiler.Target) []line {
	var out []line
	add := func(s string) { out = append(out, line{s, s}) }
	for _, w := range t.WireGuard {
		add(fmt.Sprintf("wireguard %s up", w.Name))
		add(fmt.Sprintf("  address %s mtu %d port %d", w.Address, w.MTU, w.ListenPort))
		peers := append([]compiler.WGPeer(nil), w.Peers...)
		sort.Slice(peers, func(i, j int) bool { return peers[i].PublicKey < peers[j].PublicKey })
		for _, p := range peers {
			add(peerLine(p.PublicKey, p.AllowedIPs, p.Keepalive, p.Endpoint, p.PresharedKeyRef != ""))
		}
	}
	return out
}

func hostLines(s *State, t *compiler.Target) []line {
	var out []line
	add := func(l string) { out = append(out, line{l, l}) }
	if v, ok := s.Sysctl["ip_forward"]; ok && v == 1 {
		add("sysctl ip_forward=1")
	}
	ours := map[string]bool{}
	for _, d := range union(s.Assigned, t.Interfaces) {
		ours[d] = true
	}
	var names []string
	for n, l := range s.Links {
		if ours[n] && l.Kind() == "bridge" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		l := s.Links[n]
		state := "down"
		if l.Up() {
			state = "up"
		}
		add("bridge " + n + " " + state)
		for _, a := range s.Addrs[n] {
			if a.Family == "inet" {
				add(fmt.Sprintf("  address %s/%d", a.Local, a.PrefixLen))
			}
		}
		var ports []string
		for _, o := range s.Links {
			if o.Master == n {
				ports = append(ports, o.Name)
			}
		}
		sort.Strings(ports)
		for _, p := range ports {
			add("  port " + p)
		}
	}
	for _, e := range t.Sysctls {
		if e.Dev == "" {
			continue
		}
		if v, ok := s.Sysctl[sysctlKey(e)]; ok {
			add(fmt.Sprintf("sysctl %s:%s=%d", e.Name, e.Dev, v))
		}
	}
	for _, d := range t.Offloads {
		if f, ok := s.Offloads[d]; ok && len(f.OffloadsStillOn()) == 0 {
			add("offloads off " + d)
		}
	}
	var rl, rt []string
	for _, r := range s.Rules {
		if er, ok := stateRule(r); ok && r.Protocol == ownProto {
			rl = append(rl, fmt.Sprintf("rule %d iif %s lookup %d", er.Priority, er.Iif, er.Table))
		}
	}
	for _, r := range s.Routes {
		if r.Protocol == ownProto && ownTable(r.Table) {
			er := stateRoute(r)
			rt = append(rt, fmt.Sprintf("route table %d %s%s", er.Table, er.Dst, viaDev(er)))
		}
	}
	sort.Strings(rl)
	sort.Strings(rt)
	for _, l := range append(rl, rt...) {
		add(l)
	}
	var du []string
	for _, d := range s.DockerUser.In {
		if contains(s.DockerUser.Out, d) {
			du = append(du, "docker-user accept "+d)
		}
	}
	sort.Strings(du)
	for _, l := range du {
		add(l)
	}
	return out
}

func wgLines(s *State, t *compiler.Target) []line {
	var out []line
	add := func(l string) { out = append(out, line{l, l}) }
	for _, w := range t.WireGuard {
		l, ok := s.Links[w.Name]
		info := s.WireGuard[w.Name]
		if !ok || info == nil {
			continue
		}
		state := "down"
		if l.Up() {
			state = "up"
		}
		add(fmt.Sprintf("wireguard %s %s", w.Name, state))
		for _, a := range s.Addrs[w.Name] {
			if a.Family == "inet" {
				add(fmt.Sprintf("  address %s/%d mtu %d port %d", a.Local, a.PrefixLen, l.MTU, info.ListenPort))
			}
		}
		peers := append([]linux.WGPeerInfo(nil), info.Peers...)
		sort.Slice(peers, func(i, j int) bool { return peers[i].PublicKey < peers[j].PublicKey })
		for _, p := range peers {
			// the kernel knows a roaming peer's endpoint: it is part of the diff only when the target names one
			ep := p.Endpoint
			for _, wp := range w.Peers {
				if wp.PublicKey == p.PublicKey && wp.Endpoint == "" {
					ep = ""
				}
			}
			add(peerLine(p.PublicKey, p.AllowedIPs, p.Keepalive, ep, p.HasPresharedKey))
		}
	}
	return out
}

// unified renders a unified-style diff of two line lists, three lines of context; empty when equal.
func unified(a, b []line) string {
	ops := lcsDiff(a, b)
	changed := false
	for _, o := range ops {
		if o.kind != ' ' {
			changed = true
		}
	}
	if !changed {
		return ""
	}
	const ctx = 3
	keep := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		for j := i - ctx; j <= i+ctx; j++ {
			if j >= 0 && j < len(ops) {
				keep[j] = true
			}
		}
	}
	var b2 strings.Builder
	skipped := false
	for i, o := range ops {
		if !keep[i] {
			skipped = true
			continue
		}
		if skipped {
			b2.WriteString("@@\n")
			skipped = false
		}
		b2.WriteString(string(o.kind) + o.text + "\n")
	}
	return b2.String()
}

type diffOp struct {
	kind byte
	text string
}

// lcsDiff is the classic longest-common-subsequence diff; the inputs are a few hundred lines.
func lcsDiff(a, b []line) []diffOp {
	n, m := len(a), len(b)
	t := make([][]int, n+1)
	for i := range t {
		t[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i].Key == b[j].Key {
				t[i][j] = t[i+1][j+1] + 1
			} else if t[i+1][j] >= t[i][j+1] {
				t[i][j] = t[i+1][j]
			} else {
				t[i][j] = t[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i].Key == b[j].Key:
			ops = append(ops, diffOp{' ', a[i].Text})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			ops = append(ops, diffOp{'-', a[i].Text})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j].Text})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i].Text})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j].Text})
	}
	return ops
}

func peerLine(pub string, allowed []string, keepalive int, endpoint string, psk bool) string {
	a := append([]string(nil), allowed...)
	sort.Strings(a)
	line := fmt.Sprintf("  peer %s allowed %s", pub[:8], strings.Join(a, ","))
	if keepalive > 0 {
		line += fmt.Sprintf(" keepalive %d", keepalive)
	}
	if endpoint != "" {
		line += " endpoint " + endpoint
	}
	if psk {
		line += " psk"
	}
	return line
}
