package apply

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// Plan is the execution plan of one apply: the operations of one executor request in their fixed
// order, and the same in words for the preview (plan §2.14).
type Plan struct {
	Ops     []executor.Operation
	Summary []string
}

// Empty reports whether the plan changes nothing but the generation: no link, route or
// parameter differs from the target.
func (p *Plan) Empty() bool {
	for _, o := range p.Ops {
		switch o.(type) {
		case *executor.NftApply, *executor.AssignInterfaces:
		default:
			return false
		}
	}
	return true
}

// ownProto is the protocol tag of the routes and rules the executor writes.
var ownProto = strconv.Itoa(executor.ProtoTag)

// BuildPlan compares the target with the current state and plans the difference. The order is
// fixed: interfaces and their scope, links and addresses, sysctls, offloads, routes and rules,
// the nftables transaction, DOCKER-USER (plan §2.14: "apply runs in a fixed order").
func BuildPlan(t *compiler.Target, s *State, ns string) (*Plan, error) {
	if t.HasErrors() {
		return nil, fmt.Errorf("the target has errors: %s", t.Errors()[0].Message)
	}
	tg := executor.Target{NS: ns}
	p := &Plan{}
	add := func(summary string, op executor.Operation) {
		p.Ops = append(p.Ops, op)
		p.Summary = append(p.Summary, summary)
	}
	note := func(format string, a ...any) { p.Summary = append(p.Summary, fmt.Sprintf(format, a...)) }

	oldAssigned := s.Assigned
	both := union(oldAssigned, t.Interfaces)
	if len(minus(both, oldAssigned)) > 0 || len(minus(oldAssigned, t.Interfaces)) > 0 {
		add("assign interfaces: "+strings.Join(both, ", "), &executor.AssignInterfaces{Target: tg, Devs: both})
	} else {
		add("interfaces assigned: "+strings.Join(both, ", "), &executor.AssignInterfaces{Target: tg, Devs: both})
	}
	removed := minus(oldAssigned, t.Interfaces)

	// ---- routes and rules in Chaos Gateway's tables ----------------------------------------
	// Stale routes and rules go before the links they refer to are removed (deleting a route of a
	// device that is gone fails); new ones come after the links exist.
	stale := &executor.Routing{Target: tg}
	fresh := &executor.Routing{Target: tg}
	wantRoutes := map[string]executor.Route{}
	for _, r := range t.Routes {
		wantRoutes[routeKey(r)] = r
	}
	haveRoutes := map[string]bool{}
	for _, r := range s.Routes {
		if r.Protocol != ownProto || !ownTable(r.Table) {
			continue
		}
		er := stateRoute(r)
		k := routeKey(er)
		haveRoutes[k] = true
		if _, ok := wantRoutes[k]; !ok {
			er.Action = "delete"
			stale.Routes = append(stale.Routes, er)
			note("route delete %s table %s", r.Dst, r.Table)
		}
	}
	for _, r := range t.Routes {
		if !haveRoutes[routeKey(r)] {
			fresh.Routes = append(fresh.Routes, r)
			note("route replace %s table %d%s", r.Dst, r.Table, viaDev(r))
		}
	}
	wantRules := map[string]executor.Rule{}
	for _, r := range t.Rules {
		wantRules[ruleKey(r)] = r
	}
	haveRules := map[string]bool{}
	for _, r := range s.Rules {
		if r.Protocol != ownProto {
			continue
		}
		er, ok := stateRule(r)
		if !ok {
			continue
		}
		k := ruleKey(er)
		haveRules[k] = true
		if _, ok := wantRules[k]; !ok {
			er.Action = "delete"
			stale.Rules = append(stale.Rules, er)
			note("rule delete priority %d table %d", er.Priority, er.Table)
		}
	}
	for _, r := range t.Rules {
		if !haveRules[ruleKey(r)] {
			fresh.Rules = append(fresh.Rules, r)
			note("rule add priority %d iif %s table %d", r.Priority, r.Iif, r.Table)
		}
	}
	if len(stale.Routes)+len(stale.Rules) > 0 {
		p.Ops = append(p.Ops, stale)
	}

	// ---- links ---------------------------------------------------------------------------
	var links []executor.LinkEntry
	wantBridge := map[string]bool{}
	for _, b := range t.Bridges {
		wantBridge[b.Name] = true
	}
	// bridges that are no longer wanted: release their ports, then delete them
	for _, d := range removed {
		l, ok := s.Links[d]
		if !ok || l.Kind() != "bridge" {
			continue
		}
		for _, port := range sortedLinks(s) {
			if port.Master == d && contains(both, port.Name) {
				links = append(links, executor.LinkEntry{Action: "release", Name: port.Name})
			}
		}
		links = append(links, executor.LinkEntry{Action: "delete_bridge", Name: d})
	}
	for _, b := range t.Bridges {
		bl, exists := s.Links[b.Name]
		if exists && bl.Kind() != "bridge" {
			return nil, fmt.Errorf("%s exists but is not a bridge: refusing to touch it", b.Name)
		}
		if !exists {
			links = append(links, executor.LinkEntry{Action: "add_bridge", Name: b.Name})
		}
		for _, port := range b.Ports {
			if s.Links[port].Master != b.Name {
				links = append(links, executor.LinkEntry{Action: "enslave", Name: port, Master: b.Name})
			}
		}
		for _, l := range sortedLinks(s) {
			if l.Master == b.Name && !contains(b.Ports, l.Name) && contains(both, l.Name) {
				links = append(links, executor.LinkEntry{Action: "release", Name: l.Name})
			}
		}
		want := b.Address.String()
		have := false
		for _, a := range s.Addrs[b.Name] {
			if a.Family != "inet" {
				continue
			}
			if fmt.Sprintf("%s/%d", a.Local, a.PrefixLen) == want {
				have = true
			} else {
				links = append(links, executor.LinkEntry{Action: "addr_delete", Name: b.Name, CIDR: fmt.Sprintf("%s/%d", a.Local, a.PrefixLen)})
			}
		}
		if !have || !exists {
			links = append(links, executor.LinkEntry{Action: "addr_replace", Name: b.Name, CIDR: want})
		}
		for _, d := range append([]string{b.Name}, b.Ports...) {
			if l, ok := s.Links[d]; !ok || !l.Up() {
				links = append(links, executor.LinkEntry{Action: "up", Name: d})
			}
		}
	}
	if len(links) > 0 {
		var words []string
		for _, e := range links {
			words = append(words, linkWords(e))
		}
		add("links: "+strings.Join(words, "; "), &executor.Links{Target: tg, Entries: links})
	}

	// ---- sysctls and offloads ------------------------------------------------------------
	var sysctls []executor.SysctlEntry
	for _, e := range t.Sysctls {
		if v, ok := s.Sysctl[sysctlKey(e)]; !ok || v != e.Value {
			sysctls = append(sysctls, e)
		}
	}
	if len(sysctls) > 0 {
		var words []string
		for _, e := range sysctls {
			words = append(words, fmt.Sprintf("%s%s=%d", e.Name, map[bool]string{true: "@" + e.Dev, false: ""}[e.Dev != ""], e.Value))
		}
		add("sysctl: "+strings.Join(words, ", "), &executor.Sysctl{Target: tg, Entries: sysctls})
	}
	var offloads []string
	for _, d := range t.Offloads {
		f, ok := s.Offloads[d]
		if !ok || len(f.OffloadsStillOn()) > 0 {
			offloads = append(offloads, d)
		}
	}
	if len(offloads) > 0 {
		add("offloads off: "+strings.Join(offloads, ", "), &executor.Offloads{Target: tg, Devs: offloads})
	}

	if len(fresh.Routes)+len(fresh.Rules) > 0 {
		p.Ops = append(p.Ops, fresh)
	}

	// ---- nftables: one atomic transaction -------------------------------------------------
	tx, err := t.Nft.Transaction(s.Nft)
	if err != nil {
		return nil, err
	}
	add(fmt.Sprintf("nftables: table inet chaosgw, %d chains, %d sets, %d counters, generation %q",
		len(t.Nft.AllChains()), len(t.Nft.Sets), len(t.Nft.Counters), t.Nft.Generation), &executor.NftApply{Target: tg, Ruleset: json.RawMessage(tx)})

	// ---- DOCKER-USER ----------------------------------------------------------------------
	if s.DockerUser.ChainExists {
		var missing []string
		for _, d := range t.DockerUser {
			if !contains(s.DockerUser.In, d) || !contains(s.DockerUser.Out, d) {
				missing = append(missing, d)
			}
		}
		if len(missing) > 0 || !s.DockerUser.OursFirst {
			add("DOCKER-USER accept: "+strings.Join(t.DockerUser, ", "), &executor.DockerUser{Target: tg, Action: "ensure", Devs: t.DockerUser, OptionalChain: true})
		}
	}
	if len(removed) > 0 && s.DockerUser.ChainExists {
		var stale []string
		for _, d := range removed {
			if contains(s.DockerUser.In, d) || contains(s.DockerUser.Out, d) {
				stale = append(stale, d)
			}
		}
		if len(stale) > 0 {
			add("DOCKER-USER remove: "+strings.Join(stale, ", "), &executor.DockerUser{Target: tg, Action: "remove", Devs: stale, OptionalChain: true})
		}
	}
	if len(removed) > 0 {
		add("interfaces assigned: "+strings.Join(t.Interfaces, ", "), &executor.AssignInterfaces{Target: tg, Devs: t.Interfaces})
	}
	return p, nil
}

func sortedLinks(s *State) []linux.Link {
	names := make([]string, 0, len(s.Links))
	for n := range s.Links {
		names = append(names, n)
	}
	sortStrings(names)
	out := make([]linux.Link, 0, len(names))
	for _, n := range names {
		out = append(out, s.Links[n])
	}
	return out
}

func linkWords(e executor.LinkEntry) string {
	switch e.Action {
	case "add_bridge":
		return "create bridge " + e.Name
	case "delete_bridge":
		return "delete bridge " + e.Name
	case "enslave":
		return "attach " + e.Name + " to " + e.Master
	case "release":
		return "release " + e.Name
	case "addr_replace":
		return "set " + e.CIDR + " on " + e.Name
	case "addr_delete":
		return "remove " + e.CIDR + " from " + e.Name
	}
	return e.Action + " " + e.Name
}

func viaDev(r executor.Route) string {
	s := ""
	if r.Via != "" {
		s += " via " + r.Via
	}
	if r.Dev != "" {
		s += " dev " + r.Dev
	}
	if r.Type != "" && r.Type != "unicast" {
		s += " (" + r.Type + ")"
	}
	return s
}

func ownTable(t string) bool {
	n, err := strconv.Atoi(t)
	return err == nil && n >= executor.OwnTableFirst && n <= executor.OwnTableLast
}

func routeKey(r executor.Route) string {
	typ := r.Type
	if typ == "unicast" {
		typ = ""
	}
	return fmt.Sprintf("%d|%s|%s|%s|%s", r.Table, r.Dst, r.Via, r.Dev, typ)
}

func stateRoute(r linux.Route) executor.Route {
	n, _ := strconv.Atoi(r.Table)
	fam := 4
	if strings.Contains(r.Dst, ":") || strings.Contains(r.Gateway, ":") {
		fam = 6
	}
	typ := r.Type
	if typ == "unicast" {
		typ = ""
	}
	er := executor.Route{Family: fam, Table: n, Dst: r.Dst, Via: r.Gateway, Dev: r.Dev, Type: typ}
	if typ != "" {
		er.Dev = "" // blackhole routes carry no device in the executor's model
	}
	return er
}

func ruleKey(r executor.Rule) string {
	return fmt.Sprintf("%d|%d|%s|%s|%s|%s|%s|%d", r.Family, r.Priority, r.From, r.To, r.Fwmark, r.Iif, r.Oif, r.Table)
}

// stateRule converts a rule of the kernel into the executor's form; rules with selectors the
// executor does not model are not ours to delete.
func stateRule(r linux.Rule) (executor.Rule, bool) {
	tbl, err := strconv.Atoi(r.Table)
	if err != nil || r.Action != "" {
		return executor.Rule{}, false
	}
	er := executor.Rule{Family: 4, Priority: r.Priority, Iif: r.Iif, Oif: r.Oif, Table: tbl}
	if r.Src != "" && r.Src != "all" {
		er.From = r.Src
		if r.SrcLen != nil {
			er.From = fmt.Sprintf("%s/%d", r.Src, *r.SrcLen)
		}
	}
	if r.Dst != "" && r.Dst != "all" {
		er.To = r.Dst
		if r.DstLen != nil {
			er.To = fmt.Sprintf("%s/%d", r.Dst, *r.DstLen)
		}
	}
	if r.Fwmark != "" {
		er.Fwmark = r.Fwmark
		if r.Fwmask != "" && r.Fwmask != "0xffffffff" {
			er.Fwmark += "/" + r.Fwmask
		}
	}
	if strings.Contains(r.Src+r.Dst, ":") {
		er.Family = 6
	}
	return er, true
}
