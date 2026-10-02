package domain

import (
	"net"
	"net/netip"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// identity is one way a device is recognized: a MAC or an IPv4 address or range.
type identity struct {
	path string
	of   string
}

func (v *validator) devices() {
	macs := map[string]identity{}
	var ranges []struct {
		p  netip.Prefix
		id identity
	}
	// WireGuard clients are devices too, identified by their tunnel address
	for _, id := range sortedKeys(v.idx.Devices) {
		d := v.idx.Devices[id]
		if d.Origin != OriginClient {
			continue
		}
		if a, ok := parseAddr(d.Client.Address); ok {
			ranges = append(ranges, struct {
				p  netip.Prefix
				id identity
			}{netip.PrefixFrom(a, 32), identity{d.Path + "/address", "WireGuard client " + quote(d.Name)}})
		}
	}

	fixed := map[string]identity{} // network ID + address → device
	for _, id := range sortedKeys(deref(v.cfg.Devices)) {
		dev := deref(v.cfg.Devices)[id]
		path := schema.Pointer("/devices", id)
		me := identity{path, "device " + quote(dev.Name)}

		// a device without identifiers is allowed: it can be adopted before its MAC is known
		ids := deref(dev.Identifiers)
		for i, m := range deref(ids.Macs) {
			mp := schema.Pointer(path+"/identifiers/macs", itoa(i))
			hw, err := net.ParseMAC(m)
			if err != nil || len(hw) != 6 || hw[0]&1 != 0 {
				v.add(mp, CodeInvalidMAC, "%q must be a unicast MAC address", m)
				continue
			}
			if other, dup := macs[m]; dup {
				v.add(mp, CodeDuplicateIdentifier, "the MAC %s already identifies %s (%s)", m, other.of, other.path)
				continue
			}
			macs[m] = me
		}
		for i, s := range deref(ids.Ipv4) {
			ip := schema.Pointer(path+"/identifiers/ipv4", itoa(i))
			p, ok := parsePrefix(s)
			if !ok {
				a, aok := parseAddr(s)
				if !aok {
					v.add(ip, CodeInvalidAddress, "%q is not an IPv4 address or prefix", s)
					continue
				}
				p = netip.PrefixFrom(a, 32)
			} else if p != p.Masked() {
				v.add(ip, CodeHostBitsSet, "%s has host bits set; the range is %s", s, p.Masked())
				continue
			}
			// ranges may be nested (the most specific one wins, plan §2.3); the same prefix twice,
			// or one address of two devices, is ambiguous
			for _, o := range ranges {
				if p == o.p {
					v.add(ip, CodeDuplicateIdentifier, "%s already identifies %s (%s)", p, o.id.of, o.id.path)
				}
			}
			ranges = append(ranges, struct {
				p  netip.Prefix
				id identity
			}{p, identity{ip, me.of}})
		}

		if dev.FixedIp != nil {
			v.fixedIP(path, id, dev, fixed)
		}
	}
}

func (v *validator) fixedIP(path, id string, dev model.Device, fixed map[string]identity) {
	fp := path + "/fixed_ip"
	ip, ok := parseAddr(*dev.FixedIp)
	if !ok {
		v.add(fp, CodeInvalidAddress, "must be an IPv4 address")
		return
	}
	if dev.Network == nil {
		v.add(fp, CodeFixedIPRequires, "a fixed address is a DHCP reservation in a network: set the network")
		return
	}
	nw, known := v.idx.Networks[*dev.Network]
	if !known {
		return // reported as an unknown reference
	}
	if !nw.IsLan() {
		v.add(fp, CodeFixedIPRequires, "a DHCP reservation needs a local test network; %q is a WireGuard network", nw.Name)
		return
	}
	if len(deref(deref(dev.Identifiers).Macs)) == 0 {
		v.add(fp, CodeFixedIPRequires, "the reservation is for the device's first MAC address: the device has none")
	}
	subnet, _ := networkSubnet(nw)
	gw, _ := parsePrefix(nw.Lan.Address)
	switch {
	case !usableHost(subnet, ip):
		v.add(fp, CodeOutsideSubnet, "%s is not a host address of %s (%s)", ip, nw.Name, subnet)
	case ip == gw.Addr():
		v.add(fp, CodeDuplicateAddress, "%s is the gateway's own address in %s", ip, nw.Name)
	}
	key := nw.ID + "/" + ip.String()
	if other, dup := fixed[key]; dup {
		v.add(fp, CodeDuplicateAddress, "%s is already reserved for %s (%s)", ip, other.of, other.path)
	}
	fixed[key] = identity{path, "device " + quote(dev.Name)}
}

func (v *validator) groups() {
	for _, id := range sortedKeys(deref(v.cfg.Groups)) {
		g := deref(v.cfg.Groups)[id]
		seen := map[string]bool{}
		for i, m := range deref(g.Members) {
			p := schema.Pointer(schema.Pointer("/groups", id)+"/members", itoa(i))
			if seen[m] {
				v.add(p, CodeDuplicateMember, "%q is listed twice", v.idx.NameOf(KindDevice, m))
			}
			seen[m] = true
		}
	}
}

func (v *validator) probes() {
	for _, id := range sortedKeys(deref(v.cfg.Probes)) {
		p := deref(v.cfg.Probes)[id]
		path := schema.Pointer("/probes", id) + "/network"
		if n, ok := v.idx.Networks[p.Network]; ok && !n.IsLan() {
			v.add(path, CodeProbeNetwork, "a probe is a port of a local test network's bridge; %q is a WireGuard network", n.Name)
		}
	}
}

// endpointKey identifies a matrix endpoint for the duplicate check.
func endpointKey(e model.MatrixEndpoint) string {
	switch {
	case e.Network != nil:
		return "network:" + *e.Network
	case e.Client != nil:
		return "client:" + *e.Client
	case e.Uplink != nil:
		return "uplink"
	case e.Management != nil:
		return "management"
	}
	return ""
}

func (v *validator) accessMatrix() {
	if v.cfg.AccessMatrix == nil {
		return
	}
	seen := map[string]int{}
	for i, e := range deref(v.cfg.AccessMatrix.Entries) {
		path := schema.Pointer("/access_matrix/entries", itoa(i))
		from, to := endpointKey(e.From), endpointKey(e.To)
		if from == "" || to == "" {
			continue // the schema reports an endpoint without exactly one property
		}
		if from == to {
			v.add(path, CodeMatrixSelf, "an entry from an endpoint to itself has no effect")
		}
		key := from + " -> " + to
		if first, dup := seen[key]; dup {
			v.add(path, CodeMatrixDuplicate, "the same pair is already set by entry %d", first)
		} else {
			seen[key] = i
		}
	}
}
