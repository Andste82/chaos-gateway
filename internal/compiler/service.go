package compiler

import (
	"net/netip"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// DoTChain is the chain of the networks that block DNS over TLS (plan §2.6, `dns.block_dot`): TCP port 853 of
// their devices is dropped, after the access rules (an allow rule is an exception) and before the access matrix.
const DoTChain = "dot_block"

// dotCounterName names the counter of the DoT block of a network.
func dotCounterName(networkID string) string { return "dot_" + shortID(networkID) }

// dotBlock builds the chain that drops DNS over TLS of the networks that ask for it; nil when none does.
func (t *Target) dotBlock() *Chain {
	var c Chain
	for _, b := range t.Bridges {
		if !b.BlockDoT {
			continue
		}
		t.Nft.Counters = append(t.Nft.Counters, dotCounterName(b.NetworkID))
		c.Rules = append(c.Rules, newRule(iifname(b.Name), eq(meta("l4proto"), "tcp"), eq(payload("tcp", "dport"), 853), counter(dotCounterName(b.NetworkID)), verdict("drop")))
	}
	if len(c.Rules) == 0 {
		return nil
	}
	c.Name = DoTChain
	return &c
}

// The service namespace (plan §3.3, D29): the gateway services (DNS proxy, later the TLS responder)
// run in a namespace of their own, connected to the gateway by a veth pair. Traffic that a service
// answers is routed into it through svc0, so the device's faults apply to it like to any other
// traffic.
const (
	// ServiceHostIf is the gateway's end of the pair, ServicePeerIf the end in the namespace.
	ServiceHostIf = executor.ServiceHostIf
	ServicePeerIf = executor.ServicePeerIf
	// ServiceTable routes selected traffic into the namespace, with a prohibit route as the fallback
	// so that selected traffic fails closed when the namespace is missing.
	ServiceTable = 102
	// ServiceMark is the routing-mark bit that selects a service (bit 20). M7's classification
	// mechanism never touches it (its masks only ever read or write bits 4-16, see classify.go's
	// MarkKeepOnIDWrite/MarkKeepOnDirectionWrite): writing it for real redirected traffic is
	// deferred to the milestone with the real redirect-interaction test (docs/open-items.md
	// P2-M7-01).
	ServiceMark = "0x100000/0x100000"
	// ServiceRulePriority is before the rules of the policy table, so a marked packet is looked up in
	// ServiceTable first.
	ServiceRulePriority = PolicyRulePriority - 100
	// ServiceDNSPort is the port of the DNS proxy inside the namespace.
	ServiceDNSPort = 53
	// ServiceNSDefault is the name of the service namespace when nothing else is configured.
	ServiceNSDefault = "cgsvc"
)

// The two addresses of the pair: link-local, so they never clash with a test network.
var (
	ServiceHostCIDR = netip.MustParsePrefix("169.254.100.1/30")
	ServicePeerCIDR = netip.MustParsePrefix("169.254.100.2/30")
)

// ServiceNS is the compiled service namespace.
type ServiceNS struct {
	Name     string       `json:"name"`
	HostIf   string       `json:"host_if"`
	PeerIf   string       `json:"peer_if"`
	HostCIDR netip.Prefix `json:"host_cidr"`
	PeerCIDR netip.Prefix `json:"peer_cidr"`
	// HolderPID is the process whose namespace becomes the service namespace when it has to be
	// created; 0 creates an empty one.
	HolderPID int `json:"holder_pid,omitempty"`
	// HolderNetnsInode is the inode of HolderPID's namespace as the holder itself reported it; see
	// executor.ServiceNS.HolderNetnsInode (M6b-10).
	HolderNetnsInode uint64 `json:"holder_netns_inode,omitempty"`
}

func (t *Target) compileService(in Input) {
	if in.ServiceNS == "" {
		return
	}
	t.Service = &ServiceNS{Name: in.ServiceNS, HostIf: ServiceHostIf, PeerIf: ServicePeerIf,
		HostCIDR: ServiceHostCIDR, PeerCIDR: ServicePeerCIDR, HolderPID: in.ServiceHolderPID, HolderNetnsInode: in.ServiceHolderNetnsInode}
}

// serviceRouting adds the routes and rules of the namespace: the pair's subnet and the traffic the
// services send upstream (iif svc0) use the policy table; table 102 leads into the namespace.
func (t *Target) serviceRouting(add func(executor.Route)) {
	if t.Service == nil {
		return
	}
	add(executor.Route{Dst: t.Service.HostCIDR.Masked().String(), Dev: t.Service.HostIf})
	zero := 0
	prohibitMetric := 4096
	t.Routes = append(t.Routes,
		executor.Route{Action: "replace", Family: 4, Table: ServiceTable, Dst: "default", Via: t.Service.PeerCIDR.Addr().String(), Dev: t.Service.HostIf, Metric: &zero},
		executor.Route{Action: "replace", Family: 4, Table: ServiceTable, Dst: "default", Type: "prohibit", Metric: &prohibitMetric},
	)
	t.Rules = append(t.Rules,
		executor.Rule{Action: "add", Family: 4, Priority: ServiceRulePriority, Fwmark: ServiceMark, Table: ServiceTable},
		executor.Rule{Action: "add", Family: 4, Priority: PolicyRulePriority, Iif: t.Service.HostIf, Table: PolicyTable},
	)
}

// serviceInput accepts what the services in the namespace need from the gateway: the internal API
// (configuration, query log). Everything else from the namespace is dropped.
func (t *Target) serviceInput() []Rule {
	if t.Service == nil {
		return nil
	}
	peer := t.Service.PeerCIDR.Addr().String()
	return []Rule{
		newRule(iifname(t.Service.HostIf), eq(payload("ip", "saddr"), peer), eq(payload("tcp", "dport"), t.Management.UIPort), verdict("accept")),
		newRule(iifname(t.Service.HostIf), counter("input_drop"), verdict("drop")),
	}
}

// serviceForwardGuard leaves nothing to chance: a packet for the namespace's subnet that does not leave
// through svc0 (the namespace is missing) is dropped, never routed to the uplink; the queries that
// were redirected to the DNS proxy and what the services send out are accepted by serviceForward.
func (t *Target) serviceForwardGuard() []Rule {
	if t.Service == nil {
		return nil
	}
	return []Rule{newRule(eq(payload("ip", "daddr"), prefixValue(t.Service.HostCIDR.Masked())), match(meta("oifname"), "!=", t.Service.HostIf), counter("forward_drop"), verdict("drop"))}
}

// serviceForward accepts the queries that were redirected to the DNS proxy and what the services send out of the namespace:
// towards the uplink, and towards the test networks and the WireGuard tunnels of the test role (ifsTest), where the servers
// are that a connection passed on by the TLS responder or the interception proxy has to reach (plan M21, P5-M21-02). The rule
// stands after the access rules and the matrix: they cannot judge the connection (it comes in on svc0, from the namespace's
// address), so the API judged the original connection before a service opened this one (`AccessDecision`). The management
// interface and the WireGuard networks of the role management are not among the interfaces it accepts; in the two-port
// topology the management network lies behind the uplink and the accept towards the uplink lets the namespace reach it: only
// `AccessDecision` (its management guard) refuses a connection there (a guard by address here would also block a DNS
// upstream in the management network; plan §3.3, P5-AUDIT-17).
func (t *Target) serviceForward(ifsCG, ifsTest string, uplink string) []Rule {
	if t.Service == nil {
		return nil
	}
	peer := t.Service.PeerCIDR.Addr().String()
	var out []Rule
	for _, proto := range []string{"udp", "tcp"} {
		out = append(out, newRule(iifSet(ifsCG), oifname(t.Service.HostIf), eq(payload("ip", "daddr"), peer), eq(payload(proto, "dport"), ServiceDNSPort), verdict("accept")))
	}
	if uplink != "" {
		out = append(out, newRule(iifname(t.Service.HostIf), oifname(uplink), verdict("accept")))
	}
	out = append(out, newRule(iifname(t.Service.HostIf), oifSet(ifsTest), verdict("accept")))
	return out
}

// prerouting is the NAT chain of the prerouting hook: the service redirect, then the counting rules and
// the DNAT of the port forwards (forward.go). Nil when there is neither.
func (t *Target) prerouting(tp *topo) *Chain {
	rules := append(append(t.serviceRedirect(tp), t.forwardCounts()...), t.forwardDNAT()...)
	if len(rules) == 0 {
		return nil
	}
	return &Chain{Name: "prerouting", Base: &BaseChain{Type: "nat", Hook: "prerouting", Prio: -100, Policy: "accept"}, Rules: rules}
}

// serviceRedirect sends the queries to a network's gateway address (UDP and TCP port 53) to the DNS
// proxy in the namespace. The packets keep the client's source address, so the proxy sees the device.
func (t *Target) serviceRedirect(tp *topo) []Rule {
	if t.Service == nil {
		return nil
	}
	peer := t.Service.PeerCIDR.Addr().String()
	var rules []Rule
	dnat := map[string]any{"dnat": map[string]any{"family": "ip", "addr": peer, "port": ServiceDNSPort}}
	add := func(dev string, gw netip.Addr) {
		for _, proto := range []string{"udp", "tcp"} {
			rules = append(rules, newRule(iifname(dev), eq(payload("ip", "daddr"), gw.String()), eq(payload(proto, "dport"), 53), dnat))
		}
	}
	for _, b := range t.Bridges {
		add(b.Name, b.Address.Addr())
	}
	for _, w := range t.WireGuard {
		add(w.Name, w.Address.Addr())
	}
	// DNS to other resolvers (plan §2.6, `dns.redirect_hardcoded`): the same redirect for any destination, after the
	// rules for the gateway's own address. What stays inside the segments of the network (the network and the
	// networks behind its routers: a resolver of the lab on the same bridge) is switched or routed back out of the
	// bridge and is never ours to redirect (plan §2.2). The access rules judge a redirected query by the resolver the
	// device meant (the conntrack original tuple), so a rule that drops it still does.
	for _, b := range t.Bridges {
		if !b.RedirectDNS {
			continue
		}
		local := []netip.Prefix{b.Address.Masked()}
		for _, r := range b.Routes {
			if p, err := netip.ParsePrefix(r); err == nil {
				local = append(local, p.Masked())
			} else if a, err := netip.ParseAddr(r); err == nil {
				local = append(local, netip.PrefixFrom(a, a.BitLen()))
			}
		}
		local = domain.CollapsePrefixes(local)
		for _, proto := range []string{"udp", "tcp"} {
			rules = append(rules, newRule(iifname(b.Name), match(payload("ip", "daddr"), "!=", prefixes(local)), eq(payload(proto, "dport"), 53), dnat))
		}
	}
	_ = tp
	return rules
}

// serviceMasquerade lets the services reach the upstream resolver, and the servers a passed-on connection goes to, with the
// address of the interface the connection leaves through: the uplink's and, towards the test networks and the WireGuard
// tunnels of the test role (ifsTest), the gateway's own address in that network. A server there knows a route to the gateway
// (a tunnel's AllowedIPs carry its tunnel address, a host's default route leads to it), not one to the namespace's link-local
// address.
func (t *Target) serviceMasquerade(uplink, ifsTest string) []Rule {
	if t.Service == nil {
		return nil
	}
	src := eq(payload("ip", "saddr"), prefixValue(t.Service.PeerCIDR.Masked()))
	masq := map[string]any{"masquerade": nil}
	var out []Rule
	if uplink != "" {
		out = append(out, newRule(src, oifname(uplink), masq))
	}
	out = append(out, newRule(src, oifSet(ifsTest), masq))
	return out
}
