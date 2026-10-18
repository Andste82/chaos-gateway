package capturemgr

import (
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Plan is what a scope of a capture stands for in the gateway as it is applied: the interfaces to read and the filter
// of each (plan §2.11, spike S9).
type Plan struct {
	Interfaces []executor.CaptureInterface
	// Links is the link type each interface has: bridges and the uplink are Ethernet, WireGuard interfaces carry raw IP.
	Links []capture.LinkType
}

// Mixed reports whether the interfaces have more than one link type.
func (p Plan) Mixed() bool {
	for _, l := range p.Links {
		if l != p.Links[0] {
			return true
		}
	}
	return false
}

func lower(s string) string { return strings.ToLower(s) }

func deref[T any](p *T) T {
	var z T
	if p != nil {
		z = *p
	}
	return z
}

func invalid(path, code, format string, args ...any) error {
	return domain.ValidationErrors{{Path: path, Code: code, Message: fmt.Sprintf(format, args...)}}
}

// resolver turns the scope of a request into a plan with the snapshot of the moment.
type resolver struct {
	snap *engine.Snapshot
}

// Resolve turns a scope into the interfaces and filters to read. A scope that names nothing the gateway knows, or
// whose interface is not applied yet, is a validation error with the pointer into the request; one that needs a
// later milestone is an *UnsupportedError.
func Resolve(snap *engine.Snapshot, scope model.CaptureScope) (Plan, error) {
	r := resolver{snap}
	switch {
	case scope.Network != nil:
		return r.network("/scope/network", *scope.Network)
	case scope.Device != nil:
		return r.device("/scope/device", *scope.Device)
	case scope.Uplink != nil:
		return r.uplink()
	case scope.Selector != nil:
		return r.selector(scope)
	}
	return Plan{}, invalid("/scope", domain.CodeMissingField, "the scope names a network, a device, a selector or the uplink")
}

// ---- networks and interfaces

// networkID returns the id of the network a reference names, with its kind.
func (r resolver) networkID(path, ref string) (id string, wg bool, err error) {
	if r.snap.Config != nil && r.snap.Config.Networks != nil {
		for nid, n := range *r.snap.Config.Networks {
			name := ""
			isWG := false
			if disc, _ := n.Discriminator(); disc == "wireguard" {
				if w, e := n.AsWireGuardNetwork(); e == nil {
					name, isWG = w.Name, true
				}
			} else if l, e := n.AsLanNetwork(); e == nil {
				name = l.Name
			}
			if lower(nid) == lower(ref) || (name != "" && lower(name) == lower(ref)) {
				return nid, isWG, nil
			}
		}
	}
	return "", false, invalid(path, domain.CodeUnknownReference, "there is no network %q", ref)
}

// interfacesOf returns the interfaces of a network, with their link types: the bridge of a LAN, the WireGuard
// interfaces of a WireGuard network.
func (r resolver) interfacesOf(path, id string, wg bool) ([]string, []capture.LinkType, error) {
	var names []string
	var links []capture.LinkType
	if wg {
		for _, w := range r.snap.WireGuardInterfaces {
			if lower(w.NetworkID) == lower(id) {
				names, links = append(names, w.Name), append(links, capture.LinkRaw)
			}
		}
	} else {
		for _, b := range r.snap.Bridges {
			if lower(b.NetworkID) == lower(id) {
				names, links = append(names, b.Name), append(links, capture.LinkEthernet)
			}
		}
	}
	if len(names) == 0 {
		return nil, nil, invalid(path, "no_interface", "the network has no interface on the gateway (it is not applied, or its interface is missing: see GET /networks)")
	}
	return names, links, nil
}

func (r resolver) network(path, ref string) (Plan, error) {
	id, wg, err := r.networkID(path, ref)
	if err != nil {
		return Plan{}, err
	}
	names, links, err := r.interfacesOf(path, id, wg)
	if err != nil {
		return Plan{}, err
	}
	var p Plan
	for i, n := range names {
		p.Interfaces = append(p.Interfaces, executor.CaptureInterface{Name: n})
		p.Links = append(p.Links, links[i])
	}
	return p, nil
}

func (r resolver) uplink() (Plan, error) {
	if r.snap.Applied == nil || r.snap.Applied.Uplink.Name == "" {
		return Plan{}, invalid("/scope/uplink", "no_interface", "the gateway has not applied a configuration with an uplink yet")
	}
	return Plan{Interfaces: []executor.CaptureInterface{{Name: r.snap.Applied.Uplink.Name}}, Links: []capture.LinkType{capture.LinkEthernet}}, nil
}

// ---- devices

func (r resolver) deviceByRef(path, ref string) (engine.DeviceState, error) {
	for _, d := range r.snap.Devices {
		if lower(d.ID) == lower(ref) {
			return d, nil
		}
	}
	for _, d := range r.snap.Devices {
		if lower(d.Name) == lower(ref) {
			return d, nil
		}
	}
	return engine.DeviceState{}, invalid(path, domain.CodeUnknownReference, "there is no device %q", ref)
}

// hosts returns the addresses of a device as prefixes of a single address.
func hosts(d engine.DeviceState) []netip.Prefix {
	var out []netip.Prefix
	for _, a := range d.Addresses {
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out
}

// local is the filter of the local side for the devices of one interface: their Ethernet addresses on an Ethernet
// interface (pre-NAT frames of the device, ARP and DHCP included), else their addresses. A device that has no
// Ethernet address (behind a router) is selected by its addresses on an Ethernet interface too.
func local(ds []engine.DeviceState, link capture.LinkType) capture.Filter {
	var f capture.Filter
	seenMAC := map[string]bool{}
	for _, d := range ds {
		if link == capture.LinkEthernet && len(d.MACs) > 0 {
			for _, m := range d.MACs {
				if !seenMAC[lower(m)] {
					seenMAC[lower(m)] = true
					f.MACs = append(f.MACs, lower(m))
				}
			}
			continue
		}
		f.Hosts = append(f.Hosts, hosts(d)...)
	}
	sort.Strings(f.MACs)
	f.Hosts = slices.Compact(sortedPrefixes(f.Hosts))
	return f
}

func sortedPrefixes(ps []netip.Prefix) []netip.Prefix {
	sort.Slice(ps, func(i, j int) bool { return ps[i].String() < ps[j].String() })
	return ps
}

// ofDevices builds the plan for a set of devices: the interface of each network they are in, with the filter of
// the devices there.
func (r resolver) ofDevices(path string, ds []engine.DeviceState) (Plan, error) {
	byNet := map[string][]engine.DeviceState{}
	var order []string
	for _, d := range ds {
		if _, ok := byNet[lower(d.Network)]; !ok {
			order = append(order, lower(d.Network))
		}
		byNet[lower(d.Network)] = append(byNet[lower(d.Network)], d)
	}
	sort.Strings(order)
	var p Plan
	for _, netID := range order {
		group := byNet[netID]
		id, wg, err := r.networkID(path, netID)
		if err != nil {
			return Plan{}, invalid(path, "no_interface", "the network of the device (%s) is not known", group[0].Network)
		}
		names, links, err := r.interfacesOf(path, id, wg)
		if err != nil {
			return Plan{}, err
		}
		for i, n := range names {
			f := local(group, links[i])
			if len(f.MACs) == 0 && len(f.Hosts) == 0 {
				return Plan{}, invalid(path, "no_address", "the device %s has no Ethernet address and no address yet: nothing identifies its packets", group[0].Name)
			}
			p.Interfaces = append(p.Interfaces, executor.CaptureInterface{Name: n, Filter: f})
			p.Links = append(p.Links, links[i])
		}
	}
	return p, nil
}

func (r resolver) device(path, ref string) (Plan, error) {
	d, err := r.deviceByRef(path, ref)
	if err != nil {
		return Plan{}, err
	}
	return r.ofDevices(path, []engine.DeviceState{d})
}

// groupDevices returns the devices of a group the snapshot knows.
func (r resolver) groupDevices(path, ref string) ([]engine.DeviceState, error) {
	if r.snap.Config != nil && r.snap.Config.Groups != nil {
		for id, g := range *r.snap.Config.Groups {
			if lower(id) != lower(ref) && lower(g.Name) != lower(ref) {
				continue
			}
			var out []engine.DeviceState
			for _, m := range deref(g.Members) {
				for _, d := range r.snap.Devices {
					if lower(d.ID) == lower(m) {
						out = append(out, d)
					}
				}
			}
			if len(out) == 0 {
				return nil, invalid(path, "no_interface", "no device of the group %s is known to the gateway", g.Name)
			}
			return out, nil
		}
	}
	return nil, invalid(path, domain.CodeUnknownReference, "there is no group %q", ref)
}

// ---- selectors

func (r resolver) selector(scope model.CaptureScope) (Plan, error) {
	sel := scope.Selector
	const base = "/scope/selector"
	var p Plan
	var err error
	src := sel.Source
	switch {
	case src.Device != nil:
		p, err = r.device(base+"/source/device", *src.Device)
	case src.Group != nil:
		var ds []engine.DeviceState
		if ds, err = r.groupDevices(base+"/source/group", *src.Group); err == nil {
			p, err = r.ofDevices(base+"/source/group", ds)
		}
	case src.Network != nil:
		p, err = r.network(base+"/source/network", *src.Network)
	case src.Global != nil:
		p, err = r.global()
	default:
		return Plan{}, invalid(base+"/source", domain.CodeTargetKind, "the source of a capture selector is a device, a group, a network or global")
	}
	if err != nil {
		return Plan{}, err
	}
	peers, notPeers, err := r.destination(base+"/destination", sel.Destination)
	if err != nil {
		return Plan{}, err
	}
	proto := capture.AnyProtocol
	if sel.Protocol != nil && *sel.Protocol != model.ProtocolAny {
		proto = capture.Protocol(*sel.Protocol)
	}
	var ports []capture.PortRange
	for _, n := range deref(sel.Ports) {
		ports = append(ports, capture.PortRange{From: uint16(n), To: uint16(n)})
	}
	for _, pr := range deref(sel.PortRanges) {
		ports = append(ports, capture.PortRange{From: uint16(pr.From), To: uint16(pr.To)})
	}
	for i := range p.Interfaces {
		f := &p.Interfaces[i].Filter
		f.Peers, f.NotPeers, f.Protocol, f.Ports = peers, notPeers, proto, ports
	}
	return p, nil
}

// global is every test network: all bridges and the WireGuard interfaces of the role test.
func (r resolver) global() (Plan, error) {
	var p Plan
	for _, b := range r.snap.Bridges {
		p.Interfaces = append(p.Interfaces, executor.CaptureInterface{Name: b.Name})
		p.Links = append(p.Links, capture.LinkEthernet)
	}
	for _, w := range r.snap.WireGuardInterfaces {
		if w.Role == "management" {
			continue
		}
		p.Interfaces = append(p.Interfaces, executor.CaptureInterface{Name: w.Name})
		p.Links = append(p.Links, capture.LinkRaw)
	}
	if len(p.Interfaces) == 0 {
		return Plan{}, invalid("/scope/selector/source/global", "no_interface", "the gateway has no test network applied")
	}
	return p, nil
}

// destination returns the peer side of a selector: the prefixes it names, or for the destination `uplink` the
// prefixes it is not in. A host name arrives with M20.
func (r resolver) destination(path string, d *model.Destination) (peers, notPeers []netip.Prefix, err error) {
	switch {
	case d == nil:
		return nil, nil, nil
	case d.Hostname != nil:
		return nil, nil, &UnsupportedError{What: "a host name as the destination of a capture", Milestone: "M20"}
	case d.Cidr != nil:
		p, perr := netip.ParsePrefix(*d.Cidr)
		if perr != nil {
			a, aerr := netip.ParseAddr(*d.Cidr)
			if aerr != nil {
				return nil, nil, invalid(path+"/cidr", domain.CodeInvalidAddress, "%q is not an address or a prefix", *d.Cidr)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		return []netip.Prefix{p.Masked()}, nil, nil
	case d.Network != nil:
		id, _, nerr := r.networkID(path+"/network", *d.Network)
		if nerr != nil {
			return nil, nil, nerr
		}
		w, werr := domain.NewWorld(r.snap.Config, nil)
		if werr != nil {
			return nil, nil, werr
		}
		out := w.NetworkPrefixes(id)
		if len(out) == 0 {
			return nil, nil, invalid(path+"/network", "no_interface", "the network has no prefix")
		}
		return out, nil, nil
	case d.Uplink != nil:
		// the addresses outside every configured network and the management network, as the access rules judge them
		w, werr := domain.NewWorld(r.snap.Config, nil)
		if werr != nil {
			return nil, nil, werr
		}
		return nil, w.NonUplinkPrefixes(), nil
	}
	return nil, nil, nil
}
