package domain

import (
	"net/netip"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// ---- port forwards (plan §2.2 layer 4, M35a) ---------------------------------------------

// Ports a forward never takes on its own: SSH of the gateway and BGP.
const (
	sshPort = 22
	bgpPort = 179
)

// portForwards checks the forwards (M35a): ranges that end after they start and have the same length,
// no two forwards on the same protocol with overlapping external ports, none on a port of the control
// plane or of the gateway's own services, the entry side `management` only in the three-port topology,
// and a target in a local test network that is neither the gateway nor in the management network.
func (v *validator) portForwards() {
	fwds := deref(v.cfg.PortForwards)
	twoPort := sameInterface(v.cfg.Management.Interface, v.cfg.Uplink.Interface)
	uiPort := v.opts.uiPort
	if v.cfg.Management.UiPort != nil {
		uiPort = *v.cfg.Management.UiPort
	}
	bgp := v.bgpRuns()

	type placed struct {
		path string
		f    Forward
	}
	var seen []placed
	for _, id := range sortedKeys(fwds) {
		cfgF := fwds[id]
		path := schema.Pointer("/port_forwards", id)
		f := ForwardOf(id, cfgF)
		rangesOK := v.forwardRanges(path, cfgF, f)

		if f.Entry == EntryManagement && twoPort {
			v.add(path+"/entry", CodeManagementIsUplink, "the management interface is the uplink interface (two-port topology): enter on the uplink instead")
		}

		// ports of the gateway itself
		ext := path + "/external_port"
		if rangesOK {
			if uiPort > 0 && f.Covers(uiPort) {
				v.add(ext, CodeReservedPort, "port %d is the UI/API port of the gateway", uiPort)
			}
			if f.Protocol == "udp" {
				for _, nid := range sortedKeys(v.idx.Networks) {
					if n := v.idx.Networks[nid]; n.WG != nil && f.Covers(n.WG.ListenPort) {
						v.add(ext, CodeReservedPort, "udp/%d is the listen port of the WireGuard network %q", n.WG.ListenPort, n.Name)
					}
				}
			}
			if f.Protocol == "tcp" && f.Covers(sshPort) && (f.Entry == EntryManagement || twoPort) {
				v.add(ext, CodeReservedPort, "tcp/22 is the gateway's SSH on the management interface")
			}
			if f.Protocol == "tcp" && f.Covers(bgpPort) && bgp != "" {
				v.add(ext, CodeReservedPort, "tcp/179 is BGP while the routing protocol %q runs", bgp)
			}
			for _, o := range seen {
				if o.f.Protocol == f.Protocol && f.ExtFrom <= o.f.ExtTo && o.f.ExtFrom <= f.ExtTo {
					v.add(ext, CodeOverlappingForward, "%s/%s overlaps the forward %s (%s/%s)", f.Protocol, f.ExternalPorts(), o.path, o.f.Protocol, o.f.ExternalPorts())
				}
			}
			seen = append(seen, placed{path, f})
		}

		v.forwardTarget(path+"/target", cfgF.Target)
	}
}

// forwardRanges checks that each range ends after it starts and lies in 1..65535, and that the target
// range is as long as the external one. It reports whether the external range is usable.
func (v *validator) forwardRanges(path string, c model.PortForward, f Forward) bool {
	if f.ExtTo < f.ExtFrom {
		v.add(path+"/external_port_end", CodeInvalidPortRange, "the range %d-%d ends before it starts", f.ExtFrom, f.ExtTo)
		return false
	}
	switch {
	case c.TargetPortEnd != nil && f.TgtTo < f.TgtFrom:
		v.add(path+"/target_port_end", CodeInvalidPortRange, "the range %d-%d ends before it starts", f.TgtFrom, f.TgtTo)
	case c.TargetPortEnd != nil && f.TgtTo-f.TgtFrom != f.ExtTo-f.ExtFrom:
		v.add(path+"/target_port_end", CodePortRangeLength, "the target range %s has %d ports, the external range %s has %d: a range is shifted one to one",
			f.TargetPorts(), f.TgtTo-f.TgtFrom+1, f.ExternalPorts(), f.ExtTo-f.ExtFrom+1)
	case f.TgtTo > 65535:
		p := path + "/target_port"
		if c.TargetPort == nil {
			p = path + "/external_port_end"
		}
		v.add(p, CodeInvalidPortRange, "the target range %d-%d ends beyond port 65535", f.TgtFrom, f.TgtTo)
	}
	return true
}

// forwardTarget checks that the target lies in a local test network: a configured device or a probe (not
// a WireGuard client, and not a device configured in another kind of network), or an address that is a
// host of a local test network, not the gateway's own address there and not in the management network.
func (v *validator) forwardTarget(path string, t model.PortForwardTarget) {
	if t.Device != nil {
		d, ok := v.idx.Devices[lower(*t.Device)]
		if !ok {
			return // an unknown reference is reported by the reference check
		}
		switch {
		case d.Origin == OriginClient:
			v.add(path+"/device", CodeTargetKind, "%q is a WireGuard client; a forward goes to a device in a local test network", d.Name)
		case d.Network != "":
			if n, ok := v.idx.Networks[lower(d.Network)]; ok && !n.IsLan() {
				v.add(path+"/device", CodeTargetKind, "%q lives in the WireGuard network %q; a forward goes to a device in a local test network", d.Name, n.Name)
			}
		}
		return
	}
	if t.Address == nil {
		return
	}
	a, ok := parseAddr(*t.Address)
	if !ok {
		v.add(path+"/address", CodeInvalidAddress, "%q is not an IPv4 address", *t.Address)
		return
	}
	for _, s := range deref(v.cfg.Management.AllowedSources) {
		if p, ok := parsePrefix(s); ok && p.Contains(a) {
			v.add(path+"/address", CodeTargetKind, "%s lies in the management network %s", a, p)
			return
		}
	}
	for _, id := range sortedKeys(v.idx.Networks) {
		n := v.idx.Networks[id]
		if gw, ok := networkGateway(n); ok && gw == a {
			v.add(path+"/address", CodeTargetKind, "%s is the gateway's own address in the network %q", a, n.Name)
			return
		}
	}
	if _, ok := v.idx.lanOf(a); !ok {
		v.add(path+"/address", CodeTargetKind, "%s lies in no local test network (or is its network or broadcast address)", a)
	}
}

// bgpRuns returns the name of an enabled BGP protocol of the routing configuration, empty when none
// runs. While one does, 179/tcp stays the gateway's on every entry side: a link's tunnel, and so its
// BGP neighbor, may come in through the uplink or the management interface (decided 2026-10-16).
func (v *validator) bgpRuns() string {
	if v.cfg.Routing == nil {
		return ""
	}
	ps := deref(v.cfg.Routing.Protocols)
	for _, id := range sortedKeys(ps) {
		if p := ps[id]; p.Type == model.RoutingProtocolTypeBgp && isEnabled(p.Enabled) {
			return p.Name
		}
	}
	return ""
}

// networkGateway is the gateway's own address in a network: the address of a local test network's
// bridge, or of a WireGuard network's interface.
func networkGateway(n *NetInfo) (a netip.Addr, ok bool) {
	s := ""
	switch {
	case n.Lan != nil:
		s = n.Lan.Address
	case n.WG != nil:
		s = n.WG.Address
	}
	p, ok := parsePrefix(s)
	if !ok {
		return a, false
	}
	return p.Addr(), true
}
