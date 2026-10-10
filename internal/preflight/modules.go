// Package preflight checks what the host kernel and tools must provide. It holds the one list of
// kernel modules shared by the product preflight (plan §3.4) and the test preflight (§4.5): the
// same names decide whether the namespace testbed runs directly (level 1) or in a VM (level 1b).
package preflight

// Module is a kernel module the product or the testbed needs.
type Module struct {
	// Name is the module name as modprobe knows it (underscores).
	Name string
	// Feature says what needs the module.
	Feature string
	// Milestone is the first milestone that depends on it.
	Milestone string
	// Later marks modules that the plan needs only after V1 (VLANs). They are reported but never
	// make the preflight fail.
	Later bool
}

// modules is the list of plan §3.4: sch_netem, sch_htb, cls_fw, cls_u32, cls_flower,
// sch_ingress, act_mirred, ifb, nf_conntrack, nf_tables with NAT/ct/dup/reject/numgen, veth, bridge, wireguard and,
// later, 8021q.
var modules = []Module{
	{Name: "sch_netem", Feature: "fault engine: delay, jitter, loss, rate (netem)", Milestone: "M8b"},
	{Name: "sch_htb", Feature: "fault engine: one class per active fault (HTB)", Milestone: "M8b"},
	{Name: "cls_fw", Feature: "fault engine: mark to class (fw filter)", Milestone: "M7"},
	{Name: "cls_u32", Feature: "tc filters (u32)", Milestone: "M7"},
	{Name: "cls_flower", Feature: "tunnel faults from a WireGuard peer (flower on the outer UDP)", Milestone: "M10"},
	{Name: "sch_ingress", Feature: "tunnel faults from a WireGuard peer (ingress qdisc of the uplink)", Milestone: "M10"},
	{Name: "act_mirred", Feature: "tunnel faults and capture: redirect to IFB", Milestone: "M10"},
	{Name: "ifb", Feature: "tunnel faults from a WireGuard peer (IFB device)", Milestone: "M10"},
	{Name: "nf_conntrack", Feature: "connection tracking: classification by the original tuple", Milestone: "M4"},
	{Name: "nf_conntrack_netlink", Feature: "connection tracking: reading the table (conntrack -L)", Milestone: "M6a"},
	{Name: "nf_nat", Feature: "NAT", Milestone: "M4"},
	{Name: "nf_tables", Feature: "firewall, access matrix, classification", Milestone: "M4"},
	{Name: "nft_ct", Feature: "nftables: conntrack expressions", Milestone: "M4"},
	{Name: "nft_nat", Feature: "nftables: NAT", Milestone: "M4"},
	{Name: "nft_chain_nat", Feature: "nftables: NAT chains", Milestone: "M4"},
	{Name: "nft_masq", Feature: "nftables: masquerade towards the uplink", Milestone: "M4"},
	{Name: "nft_redir", Feature: "nftables: redirect to gateway services (DNS, TLS)", Milestone: "M6b"},
	{Name: "nft_reject", Feature: "nftables: reject", Milestone: "M9"},
	{Name: "nft_reject_inet", Feature: "nftables: reject (inet family)", Milestone: "M9"},
	{Name: "nft_dup_netdev", Feature: "nftables: duplicate packets (fault duplication hook, capture)", Milestone: "M10"},
	{Name: "nft_numgen", Feature: "nftables: the random draw of a fault's duplication (numgen random)", Milestone: "M10"},
	{Name: "veth", Feature: "virtual cables: probes, service namespace, testbed", Milestone: "M1"},
	{Name: "bridge", Feature: "one bridge per test network", Milestone: "M4"},
	{Name: "wireguard", Feature: "WireGuard networks", Milestone: "M4b"},
	{Name: "8021q", Feature: "VLAN networks", Milestone: "M31", Later: true},
}

// Modules returns the full list, including modules that are needed only after V1.
func Modules() []Module { return append([]Module(nil), modules...) }

// Required returns the modules the preflight insists on.
func Required() []Module {
	var out []Module
	for _, m := range modules {
		if !m.Later {
			out = append(out, m)
		}
	}
	return out
}
