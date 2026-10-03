package apply

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Mismatch is one difference between the target and the kernel.
type Mismatch struct {
	Subsystem string
	Detail    string
}

func (m Mismatch) String() string { return m.Subsystem + ": " + m.Detail }

// Verify compares the state read back after an apply with the target (plan §2.14): the
// generation, the elements of sets, the rules of every chain, routes and rules in Chaos Gateway's
// tables, bridges, addresses, sysctls, offloads and the DOCKER-USER accept rules. The state must
// have been read with the target's Want. An empty result means the kernel matches.
func Verify(t *compiler.Target, s *State) []Mismatch {
	var mm []Mismatch
	bad := func(sub, format string, a ...any) { mm = append(mm, Mismatch{sub, fmt.Sprintf(format, a...)}) }

	// interfaces assigned to the executor
	if got, want := strings.Join(s.Assigned, ","), strings.Join(sorted(t.Interfaces), ","); got != want {
		bad("interfaces", "assigned %q, want %q", got, want)
	}

	if t.Service != nil {
		if why := serviceDiffers(t.Service, s); why != "" {
			bad("service", "%s", why)
		}
	}

	// links
	for _, b := range t.Bridges {
		l, ok := s.Links[b.Name]
		switch {
		case !ok:
			bad("links", "bridge %s is missing", b.Name)
			continue
		case l.Kind() != "bridge":
			bad("links", "%s is not a bridge", b.Name)
		case !l.Up():
			bad("links", "bridge %s is down", b.Name)
		}
		var got []string
		for _, a := range s.Addrs[b.Name] {
			if a.Family == "inet" {
				got = append(got, fmt.Sprintf("%s/%d", a.Local, a.PrefixLen))
			}
		}
		if len(got) != 1 || got[0] != b.Address.String() {
			bad("links", "bridge %s has addresses %v, want [%s]", b.Name, got, b.Address)
		}
		var ports []string
		for _, l := range s.Links {
			if l.Master == b.Name {
				ports = append(ports, l.Name)
			}
		}
		sort.Strings(ports)
		if strings.Join(ports, ",") != strings.Join(b.Ports, ",") {
			bad("links", "bridge %s has ports %v, want %v", b.Name, ports, b.Ports)
		}
		for _, p := range b.Ports {
			if pl, ok := s.Links[p]; ok && !pl.Up() {
				bad("links", "port %s is down", p)
			}
		}
	}

	// WireGuard interfaces
	for _, w := range t.WireGuard {
		l, ok := s.Links[w.Name]
		switch {
		case !ok:
			bad("wireguard", "interface %s is missing", w.Name)
			continue
		case l.Kind() != "wireguard":
			bad("wireguard", "%s is not a WireGuard interface", w.Name)
			continue
		case !l.Up():
			bad("wireguard", "%s is down", w.Name)
		case l.MTU != w.MTU:
			bad("wireguard", "%s has the MTU %d, want %d", w.Name, l.MTU, w.MTU)
		}
		var got []string
		for _, a := range s.Addrs[w.Name] {
			if a.Family == "inet" {
				got = append(got, fmt.Sprintf("%s/%d", a.Local, a.PrefixLen))
			}
		}
		if len(got) != 1 || got[0] != w.Address.String() {
			bad("wireguard", "%s has the addresses %v, want [%s]", w.Name, got, w.Address)
		}
		info := s.WireGuard[w.Name]
		if info == nil {
			bad("wireguard", "%s could not be read", w.Name)
			continue
		}
		for _, m := range wgMismatch(w, info) {
			bad("wireguard", "%s: %s", w.Name, m)
		}
	}

	verifyBird(t, s, bad)

	// sysctls and offloads
	for _, e := range t.Sysctls {
		if v, ok := s.Sysctl[sysctlKey(e)]; !ok || v != e.Value {
			bad("sysctl", "%s is %v, want %d", sysctlKey(e), s.Sysctl[sysctlKey(e)], e.Value)
		}
	}
	for _, d := range t.Offloads {
		f, ok := s.Offloads[d]
		if !ok {
			bad("offloads", "%s was not read", d)
			continue
		}
		if on := f.OffloadsStillOn(); len(on) > 0 {
			bad("offloads", "%s still has %s on", d, strings.Join(on, ", "))
		}
	}

	// routes and rules in Chaos Gateway's tables: exactly the target
	haveRoutes := map[string]bool{}
	for _, r := range s.Routes {
		if r.Protocol == ownProto && ownTable(r.Table) {
			haveRoutes[routeKey(stateRoute(r))] = true
		}
	}
	wantRoutes := map[string]bool{}
	for _, r := range t.Routes {
		k := routeKey(r)
		wantRoutes[k] = true
		if !haveRoutes[k] {
			bad("routes", "missing %s%s table %d", r.Dst, viaDev(r), r.Table)
		}
	}
	for k := range haveRoutes {
		if !wantRoutes[k] {
			bad("routes", "unexpected route %s", k)
		}
	}
	haveRules := map[string]bool{}
	for _, r := range s.Rules {
		if r.Protocol != ownProto {
			continue
		}
		if er, ok := stateRule(r); ok {
			haveRules[ruleKey(er)] = true
		}
	}
	wantRules := map[string]bool{}
	for _, r := range t.Rules {
		k := ruleKey(r)
		wantRules[k] = true
		if !haveRules[k] {
			bad("rules", "missing rule priority %d iif %s table %d", r.Priority, r.Iif, r.Table)
		}
	}
	for k := range haveRules {
		if !wantRules[k] {
			bad("rules", "unexpected rule %s", k)
		}
	}

	// DOCKER-USER: only where the chain exists (a host without Docker has none)
	if s.DockerUser.ChainExists {
		for _, d := range t.DockerUser {
			if !contains(s.DockerUser.In, d) || !contains(s.DockerUser.Out, d) {
				bad("docker", "DOCKER-USER lacks the accept rules for %s", d)
			}
		}
		if !s.DockerUser.OursFirst {
			bad("docker", "accept rules stand behind Docker's own rules")
		}
	}

	return append(mm, verifyNft(t, s.Nft)...)
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// verifyNft compares the table with the target. Rules are recognized by the hash of their
// expression in the comment, in order; sets by name and elements (dynamic sets by name only: the
// compiler does not own their elements); counters by name only (their values are runtime data).
func verifyNft(t *compiler.Target, rs *linux.Ruleset) []Mismatch {
	var mm []Mismatch
	bad := func(format string, a ...any) { mm = append(mm, Mismatch{"nftables", fmt.Sprintf(format, a...)}) }
	if len(rs.Tables()) != 1 {
		bad("table inet chaosgw is missing")
		return mm
	}
	wantSets := map[string]compiler.SetDef{}
	for _, s := range t.Nft.Sets {
		wantSets[s.Name] = s
	}
	haveSets := map[string]*linux.NftSet{}
	for _, o := range rs.Objects {
		if o.Set != nil {
			haveSets[o.Set.Name] = o.Set
		}
	}
	for name, want := range wantSets {
		got, ok := haveSets[name]
		if !ok {
			bad("set %s is missing", name)
			continue
		}
		if want.Dynamic {
			continue
		}
		if typ := strings.Trim(string(got.Type), `"`); typ != want.Type || strings.Join(got.Flags, ",") != strings.Join(want.Flags, ",") {
			bad("set %s is %s %v, want %s %v", name, typ, got.Flags, want.Type, want.Flags)
		}
		have := got.Elements()
		wantEl := make([]string, 0, len(want.Elements))
		for _, e := range want.Elements {
			wantEl = append(wantEl, linux.NormalizeElement(e))
		}
		sort.Strings(wantEl)
		if strings.Join(have, ",") != strings.Join(wantEl, ",") {
			bad("set %s holds %v, want %v", name, have, wantEl)
		}
	}
	for name := range haveSets {
		if _, ok := wantSets[name]; !ok {
			bad("unexpected set %s", name)
		}
	}
	haveCounters := rs.Counters()
	wantCounters := sorted(t.Nft.Counters)
	if strings.Join(haveCounters, ",") != strings.Join(wantCounters, ",") {
		bad("counters %v, want %v", haveCounters, wantCounters)
	}
	wantChains := map[string]compiler.Chain{}
	for _, c := range t.Nft.AllChains() {
		wantChains[c.Name] = c
	}
	for _, o := range rs.Objects {
		if o.Chain != nil {
			if _, ok := wantChains[o.Chain.Name]; !ok {
				bad("unexpected chain %s", o.Chain.Name)
			}
		}
	}
	for name, want := range wantChains {
		have := rs.Chain(name)
		if have == nil {
			bad("chain %s is missing", name)
			continue
		}
		if want.Base != nil {
			prio := 0
			if have.Prio != nil {
				prio = *have.Prio
			}
			if have.Type != want.Base.Type || have.Hook != want.Base.Hook || prio != want.Base.Prio || have.Policy != want.Base.Policy {
				bad("chain %s is %s/%s/%d/%s, want %s/%s/%d/%s", name, have.Type, have.Hook, prio, have.Policy, want.Base.Type, want.Base.Hook, want.Base.Prio, want.Base.Policy)
			}
		} else if have.Hook != "" {
			bad("chain %s has a hook, want a regular chain", name)
		}
		var haveRules []string
		for _, r := range rs.Rules(name) {
			haveRules = append(haveRules, r.Comment)
		}
		var wantRules []string
		for _, r := range want.Rules {
			wantRules = append(wantRules, r.Comment)
		}
		if strings.Join(haveRules, ",") != strings.Join(wantRules, ",") {
			bad("chain %s has the rules %v, want %v", name, haveRules, wantRules)
		}
	}
	// generation: the comment of the one rule of the chain generation
	if g := rs.Rules(compiler.GenerationChain); len(g) != 1 || g[0].Comment != t.Nft.Generation {
		bad("generation is %v, want %q", comments(g), t.Nft.Generation)
	}
	return mm
}

func comments(r []linux.NftRule) []string {
	var out []string
	for _, x := range r {
		out = append(out, x.Comment)
	}
	return out
}
