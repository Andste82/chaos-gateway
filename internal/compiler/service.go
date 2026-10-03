package compiler

import (
	"net/netip"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// The service namespace (plan §3.3, D29): the gateway services (DNS proxy, later the TLS responder)
// run in a namespace of their own, connected to the gateway by a veth pair. Traffic that a service
// answers is routed into it through svc0, so the device's faults apply to it like to any other
// traffic.
const (
	// ServiceHostIf is the gateway's end of the pair, ServicePeerIf the end in the namespace.
	ServiceHostIf = "svc0"
	ServicePeerIf = "svc1"
	// ServiceTable routes selected traffic into the namespace, with a prohibit route as the fallback
	// so that selected traffic fails closed when the namespace is missing.
	ServiceTable = 102
	// ServiceMark is the routing-mark bit that selects a service (bit 20); the classification of M7
	// sets it.
	ServiceMark = "0x100000/0x100000"
	// ServiceRulePriority is before the rules of the policy table, so a marked packet is looked up in
	// ServiceTable first.
	ServiceRulePriority = PolicyRulePriority - 100
	// ServiceDNSPort is the port of the DNS proxy inside the namespace.
	ServiceDNSPort = 53
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
}

func (t *Target) compileService(in Input) {
	if in.ServiceNS == "" {
		return
	}
	t.Service = &ServiceNS{Name: in.ServiceNS, HostIf: ServiceHostIf, PeerIf: ServicePeerIf,
		HostCIDR: ServiceHostCIDR, PeerCIDR: ServicePeerCIDR, HolderPID: in.ServiceHolderPID}
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

// serviceForward leaves nothing to chance: a packet for the namespace's subnet that does not leave
// through svc0 (the namespace is missing) is dropped, never routed to the uplink; the queries that
// were redirected to the DNS proxy and what the services send upstream are accepted.
func (t *Target) serviceForwardGuard() []Rule {
	if t.Service == nil {
		return nil
	}
	return []Rule{newRule(eq(payload("ip", "daddr"), prefixValue(t.Service.HostCIDR.Masked())), match(meta("oifname"), "!=", t.Service.HostIf), counter("forward_drop"), verdict("drop"))}
}

func (t *Target) serviceForward(ifsCG string, uplink string) []Rule {
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
	return out
}

// serviceRedirect sends the queries to a network's gateway address (UDP and TCP port 53) to the DNS
// proxy in the namespace. The packets keep the client's source address, so the proxy sees the device.
func (t *Target) serviceRedirect(tp *topo) *Chain {
	if t.Service == nil {
		return nil
	}
	peer := t.Service.PeerCIDR.Addr().String()
	c := &Chain{Name: "prerouting", Base: &BaseChain{Type: "nat", Hook: "prerouting", Prio: -100, Policy: "accept"}}
	dnat := map[string]any{"dnat": map[string]any{"family": "ip", "addr": peer, "port": ServiceDNSPort}}
	add := func(dev string, gw netip.Addr) {
		for _, proto := range []string{"udp", "tcp"} {
			c.Rules = append(c.Rules, newRule(iifname(dev), eq(payload("ip", "daddr"), gw.String()), eq(payload(proto, "dport"), 53), dnat))
		}
	}
	for _, b := range t.Bridges {
		add(b.Name, b.Address.Addr())
	}
	for _, w := range t.WireGuard {
		add(w.Name, w.Address.Addr())
	}
	_ = tp
	return c
}

// serviceMasquerade lets the services reach the upstream resolver with the uplink's address.
func (t *Target) serviceMasquerade(uplink string) *Rule {
	if t.Service == nil || uplink == "" {
		return nil
	}
	r := newRule(eq(payload("ip", "saddr"), prefixValue(t.Service.PeerCIDR.Masked())), oifname(uplink), map[string]any{"masquerade": nil})
	return &r
}
