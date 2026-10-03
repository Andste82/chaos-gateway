package apply

import (
	"encoding/json"
	"fmt"
	"net/netip"
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
	wgWant := map[string]compiler.WGInterface{}
	for _, w := range t.WireGuard {
		wgWant[w.Name] = w
	}

	// ---- the service namespace: the pair of interfaces and the namespace's side of it -----------
	if op, why := serviceOp(t, s, tg); op != nil {
		add("service namespace: "+why, op)
	}

	// ---- routes and rules in Chaos Gateway's tables ----------------------------------------
	// Stale routes and rules go before the links they refer to are removed (deleting a route of a
	// device that is gone fails); new ones come after the links exist.
	stale := &executor.Routing{Target: tg}
	late := &executor.Routing{Target: tg}
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
			// a route of a device that is about to go is deleted first (deleting it afterwards
			// fails); every other stale route goes after the new ones exist
			if contains(removed, er.Dev) {
				stale.Routes = append(stale.Routes, er)
			} else {
				late.Routes = append(late.Routes, er)
			}
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
			if contains(removed, er.Iif) || contains(removed, er.Oif) {
				stale.Rules = append(stale.Rules, er)
			} else {
				late.Rules = append(late.Rules, er)
			}
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
	// the pair goes after the routes that lead through it
	for _, d := range removed {
		if l, ok := s.Links[d]; ok && d == compiler.ServiceHostIf && l.Kind() == "veth" && t.Service == nil {
			add("service namespace: delete "+d, &executor.ServiceNS{Target: tg, Action: "delete", Name: compiler.ServiceNSDefault, HostIf: compiler.ServiceHostIf, PeerIf: compiler.ServicePeerIf,
				HostCIDR: compiler.ServiceHostCIDR.String(), PeerCIDR: compiler.ServicePeerCIDR.String()})
		}
	}

	// ---- links ---------------------------------------------------------------------------
	var links, ups []executor.LinkEntry
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
	// WireGuard interfaces that are no longer wanted go; the ones that are wanted are created or
	// synchronized before the links step gives them addresses
	var wgOps []executor.Operation
	var wgWords []string
	for _, d := range removed {
		if l, ok := s.Links[d]; ok && l.Kind() == "wireguard" {
			wgOps = append(wgOps, &executor.WireGuard{Target: tg, Action: "delete", Name: d})
			wgWords = append(wgWords, "delete "+d)
		}
	}
	for _, w := range t.WireGuard {
		l, exists := s.Links[w.Name]
		if exists && l.Kind() != "wireguard" {
			return nil, fmt.Errorf("%s exists but is not a WireGuard interface: refusing to touch it", w.Name)
		}
		if exists && !contains(oldAssigned, w.Name) {
			return nil, fmt.Errorf("the WireGuard interface %s exists but does not belong to Chaos Gateway: refusing to take it over", w.Name)
		}
		if why := wgDiffers(w, l, s.WireGuard[w.Name], exists); why != "" {
			wgOps = append(wgOps, wgEnsure(tg, w))
			wgWords = append(wgWords, fmt.Sprintf("%s %s (%d peers)", why, w.Name, len(w.Peers)))
		}
		if !exists {
			links = append(links, executor.LinkEntry{Action: "addr_replace", Name: w.Name, CIDR: w.Address.String()})
		} else {
			links = append(links, addrEntries(s, w.Name, w.Address.String())...)
		}
		if !exists || !l.Up() {
			ups = append(ups, executor.LinkEntry{Action: "up", Name: w.Name})
		}
	}
	if len(wgOps) > 0 {
		p.Ops = append(p.Ops, wgOps...)
		p.Summary = append(p.Summary, "wireguard: "+strings.Join(wgWords, "; "))
	}
	for _, b := range t.Bridges {
		bl, exists := s.Links[b.Name]
		if exists && bl.Kind() != "bridge" {
			return nil, fmt.Errorf("%s exists but is not a bridge: refusing to touch it", b.Name)
		}
		if exists && !contains(oldAssigned, b.Name) {
			return nil, fmt.Errorf("the bridge %s exists but does not belong to Chaos Gateway: refusing to take it over", b.Name)
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
			// a port that another bridge of the target wants is moved by enslaving it there
			if l.Master == b.Name && !contains(b.Ports, l.Name) && contains(both, l.Name) && !inAnyBridge(t, l.Name) {
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
				ups = append(ups, executor.LinkEntry{Action: "up", Name: d})
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
	// links come up last: no router advertisement is accepted in the time between a new bridge
	// and its sysctls
	if len(ups) > 0 {
		var words []string
		for _, e := range ups {
			words = append(words, "up "+e.Name)
		}
		add("links: "+strings.Join(words, "; "), &executor.Links{Target: tg, Entries: ups})
	}

	// the executor adds before it deletes within one operation: no gap between old and new
	fresh.Routes = append(fresh.Routes, late.Routes...)
	fresh.Rules = append(fresh.Rules, late.Rules...)
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
		if !s.DockerUser.OursFirst {
			// a rule of Docker's stands in front of ours: take ours out and put them in front again
			add("DOCKER-USER remove (to reposition): "+strings.Join(t.DockerUser, ", "), &executor.DockerUser{Target: tg, Action: "remove", Devs: t.DockerUser, OptionalChain: true})
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
	if err := planBird(t, s, p); err != nil {
		return nil, err
	}
	if len(removed) > 0 {
		add("interfaces assigned: "+strings.Join(t.Interfaces, ", "), &executor.AssignInterfaces{Target: tg, Devs: t.Interfaces})
	}
	return p, nil
}

// serviceOp returns the operation that makes the service namespace what the target wants, with the
// reason, or nil when it is as wanted.
func serviceOp(t *compiler.Target, s *State, tg executor.Target) (executor.Operation, string) {
	if t.Service == nil {
		return nil, ""
	}
	why := serviceDiffers(t.Service, s)
	if why == "" {
		return nil, ""
	}
	return &executor.ServiceNS{Target: tg, Action: "ensure", Name: t.Service.Name, HostIf: t.Service.HostIf, PeerIf: t.Service.PeerIf,
		HostCIDR: t.Service.HostCIDR.String(), PeerCIDR: t.Service.PeerCIDR.String(), HolderPID: t.Service.HolderPID}, why
}

// serviceDiffers says what is wrong with the service namespace; empty when nothing is.
func serviceDiffers(w *compiler.ServiceNS, s *State) string {
	switch {
	case s.Service == nil || !s.Service.Exists:
		return "namespace " + w.Name + " is missing"
	}
	l, ok := s.Links[w.HostIf]
	switch {
	case !ok:
		return w.HostIf + " is missing"
	case l.Kind() != "veth":
		return w.HostIf + " is not a veth"
	case !l.Up():
		return w.HostIf + " is down"
	}
	var have []string
	for _, a := range s.Addrs[w.HostIf] {
		if a.Family == "inet" {
			have = append(have, fmt.Sprintf("%s/%d", a.Local, a.PrefixLen))
		}
	}
	if len(have) != 1 || have[0] != w.HostCIDR.String() {
		return fmt.Sprintf("%s has addresses %v, want %s", w.HostIf, have, w.HostCIDR)
	}
	switch {
	case len(s.Service.PeerAddrs) != 1 || s.Service.PeerAddrs[0] != w.PeerCIDR.String():
		return fmt.Sprintf("%s in %s has addresses %v, want %s", w.PeerIf, w.Name, s.Service.PeerAddrs, w.PeerCIDR)
	case !s.Service.PeerUp:
		return w.PeerIf + " in " + w.Name + " is down"
	case s.Service.DefaultVia != w.HostCIDR.Addr().String():
		return fmt.Sprintf("the default route in %s goes via %q, want %s", w.Name, s.Service.DefaultVia, w.HostCIDR.Addr())
	}
	return ""
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
	// `ip -j` prints a host route as the bare address
	return fmt.Sprintf("%d|%s|%s|%s|%s", r.Table, strings.TrimSuffix(r.Dst, "/32"), r.Via, r.Dev, typ)
}

func inAnyBridge(t *compiler.Target, port string) bool {
	for _, b := range t.Bridges {
		if contains(b.Ports, port) {
			return true
		}
	}
	return false
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

// addrEntries makes want the only IPv4 address of dev.
func addrEntries(s *State, dev, want string) []executor.LinkEntry {
	var out []executor.LinkEntry
	have := false
	for _, a := range s.Addrs[dev] {
		if a.Family != "inet" {
			continue
		}
		c := fmt.Sprintf("%s/%d", a.Local, a.PrefixLen)
		if c == want {
			have = true
		} else {
			out = append(out, executor.LinkEntry{Action: "addr_delete", Name: dev, CIDR: c})
		}
	}
	if !have {
		out = append(out, executor.LinkEntry{Action: "addr_replace", Name: dev, CIDR: want})
	}
	return out
}

func wgEnsure(tg executor.Target, w compiler.WGInterface) *executor.WireGuard {
	op := &executor.WireGuard{Target: tg, Action: "ensure", Name: w.Name, ListenPort: w.ListenPort, MTU: w.MTU, KeyRef: w.KeyRef}
	for _, p := range w.Peers {
		op.Peers = append(op.Peers, executor.WGPeer{
			PublicKey: p.PublicKey, PresharedKeyRef: p.PresharedKeyRef, AllowedIPs: p.AllowedIPs, Keepalive: p.Keepalive, Endpoint: p.Endpoint,
		})
	}
	return op
}

// wgDiffers says why the kernel's interface does not match the target ("create", "update"), or ""
// when it does. Unchanged peers are left alone: a re-apply must not touch an established tunnel.
func wgDiffers(w compiler.WGInterface, l linux.Link, info *linux.WGInfo, exists bool) string {
	if !exists || info == nil {
		return "create"
	}
	if l.MTU != w.MTU || len(wgMismatch(w, info)) > 0 {
		return "update"
	}
	return ""
}

func ipEndpoint(ep string) bool {
	host, _, ok := strings.Cut(ep, ":")
	if !ok {
		return false
	}
	_, err := netip.ParseAddr(host)
	return err == nil
}

// wgMismatch lists what differs between an interface of the target and the kernel's.
func wgMismatch(w compiler.WGInterface, info *linux.WGInfo) []string {
	var out []string
	if info.ListenPort != w.ListenPort {
		out = append(out, fmt.Sprintf("listen port %d, want %d", info.ListenPort, w.ListenPort))
	}
	if info.PublicKey != w.PublicKey {
		out = append(out, "the interface key differs from the stored one")
	}
	have := map[string]linux.WGPeerInfo{}
	for _, p := range info.Peers {
		have[p.PublicKey] = p
	}
	want := map[string]bool{}
	for _, p := range w.Peers {
		want[p.PublicKey] = true
		h, ok := have[p.PublicKey]
		if !ok {
			out = append(out, fmt.Sprintf("peer %s is missing", p.Name))
			continue
		}
		if strings.Join(sorted(h.AllowedIPs), ",") != strings.Join(sorted(p.AllowedIPs), ",") {
			out = append(out, fmt.Sprintf("peer %s has the allowed ips %v, want %v", p.Name, h.AllowedIPs, p.AllowedIPs))
		}
		if h.Keepalive != p.Keepalive {
			out = append(out, fmt.Sprintf("peer %s has the keepalive %d, want %d", p.Name, h.Keepalive, p.Keepalive))
		}
		// a host name is resolved by `wg` and the kernel reports the address: only an address
		// literal can be compared
		if ipEndpoint(p.Endpoint) && h.Endpoint != p.Endpoint {
			out = append(out, fmt.Sprintf("peer %s has the endpoint %q, want %q", p.Name, h.Endpoint, p.Endpoint))
		}
		if h.HasPresharedKey != (p.PresharedKeyRef != "") {
			out = append(out, fmt.Sprintf("peer %s: preshared key present %v, want %v", p.Name, h.HasPresharedKey, p.PresharedKeyRef != ""))
		}
	}
	for pub := range have {
		if !want[pub] {
			out = append(out, "a peer that the target does not have: "+pub[:8])
		}
	}
	return out
}
