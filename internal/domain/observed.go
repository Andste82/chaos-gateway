package domain

import (
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the observed state (plan §2.1.1, §2.3): what the gateway sees, as opposed to what
// is configured. It is neither versioned nor an overlay; its addresses are an input of the
// compiler ("which IP currently belongs to which device").

// Neighbor is an entry of the ARP table.
type Neighbor struct {
	IP        netip.Addr
	MAC       string
	Interface string
	Network   string // UUID of the network the interface belongs to
	// Stale marks an entry the kernel has not confirmed lately (state STALE, DELAY, FAILED). It
	// only counts while the address still carries connections.
	Stale bool
}

// ProbeObservation is what is known about a probe: the MAC of its veth and its addresses.
type ProbeObservation struct {
	MAC string
	IPs []netip.Addr
}

// DiscoveredDevice is a device that appeared on the network and is not configured. It has a
// UUID of its own, so overlays can target it, and can be adopted with one click (plan §2.3).
type DiscoveredDevice struct {
	ID       string
	MACs     []string
	IPs      []netip.Addr
	Network  string
	LastSeen time.Time
	// Sources are where it was seen: dhcp, neighbor, conntrack, wireguard.
	Sources []string
}

// DiscoveredName is the generated name of a discovered device: dev-<mac>, with dashes.
func DiscoveredName(mac string) string {
	return "dev-" + strings.ReplaceAll(strings.ToLower(mac), ":", "-")
}

// Observed is a snapshot of what the gateway sees.
type Observed struct {
	Leases     []model.DhcpLease
	Neighbors  []Neighbor
	Probes     map[string]ProbeObservation          // by probe UUID
	Peers      map[string]model.WireGuardPeerStatus // by client or link UUID
	Discovered []DiscoveredDevice
	// ActiveSources are source addresses that still have connections (conntrack): the address of
	// a device that was re-addressed stays mapped to it while they last.
	ActiveSources map[netip.Addr]bool
}

// IdentityRange is a device identified by an address range (devices behind another router, hosts
// in a client network): every address in the range belongs to the device.
type IdentityRange struct {
	Prefix netip.Prefix
	Device string
}

// IdentityConflict is an address that several devices claim; Winner got it.
type IdentityConflict struct {
	IP      netip.Addr
	Devices []string
	Winner  string
}

// Identity answers which device an address belongs to right now. Faults stay attached to the
// device when it gets a new address, because the compiler keys them by device and takes the
// addresses from here (plan §2.3).
type Identity struct {
	// Addresses are the current addresses of each device of the device namespace, sorted.
	Addresses map[string][]netip.Addr
	// Owner maps an address to its device.
	Owner map[netip.Addr]string
	// Ranges are devices identified by prefixes.
	Ranges []IdentityRange
	// Discovered are the observed devices that no configured device covers.
	Discovered []DiscoveredDevice
	// Conflicts are addresses claimed by more than one device.
	Conflicts []IdentityConflict
}

// OwnerOf returns the device an address belongs to: an exact match first, then the most
// specific range.
func (id Identity) OwnerOf(ip netip.Addr) (string, bool) {
	if d, ok := id.Owner[ip]; ok {
		return d, true
	}
	best, bits := "", -1
	for _, r := range id.Ranges {
		if r.Prefix.Contains(ip) && r.Prefix.Bits() > bits {
			best, bits = r.Device, r.Prefix.Bits()
		}
	}
	return best, bits >= 0
}

// claim strengths: an explicit address in the configuration beats a lease, which beats what the
// neighbor table says, which beats a mere reservation.
const (
	claimReservation = iota + 1
	claimNeighbor
	claimLease
	claimExplicit
)

type claim struct {
	device   string
	strength int
	stamp    time.Time // for leases: when they expire; a later expiry is a newer lease
}

func (c claim) beats(o claim) bool {
	if c.strength != o.strength {
		return c.strength > o.strength
	}
	if !c.stamp.Equal(o.stamp) {
		return c.stamp.After(o.stamp)
	}
	return c.device < o.device
}

// deviceMACs maps the MAC addresses of the devices of the namespace to the device. The first
// configured device that lists a MAC keeps it, and a probe never takes over the MAC of a
// configured device.
func deviceMACs(idx *Index, probes map[string]ProbeObservation) map[string]string {
	macs := map[string]string{}
	for _, id := range sortedKeys(idx.Devices) {
		d := idx.Devices[id]
		if d.Device != nil {
			for _, m := range deref(deref(d.Device.Identifiers).Macs) {
				if _, taken := macs[m]; !taken {
					macs[m] = id
				}
			}
		}
	}
	for _, id := range sortedKeys(probes) {
		if m := lower(probes[id].MAC); m != "" {
			if _, taken := macs[m]; !taken {
				macs[m] = id
			}
		}
	}
	return macs
}

// ResolveIdentity works out the addresses of all devices from the configuration and the
// observed state. prev is the result of the last resolution: an address that a device had and
// that still carries connections stays mapped to it, unless it now belongs to another device.
// The configuration must be normalized.
func ResolveIdentity(cfg *model.Configuration, obs Observed, prev *Identity) Identity {
	idx, _ := BuildIndex(cfg)
	macs := deviceMACs(idx, obs.Probes)

	claims := map[netip.Addr][]claim{}
	add := func(ip netip.Addr, c claim) {
		if ip.IsValid() {
			claims[ip] = append(claims[ip], c)
		}
	}
	res := Identity{Addresses: map[string][]netip.Addr{}, Owner: map[netip.Addr]string{}}

	for _, id := range sortedKeys(idx.Devices) {
		d := idx.Devices[id]
		switch {
		case d.Client != nil:
			if a, ok := parseAddr(d.Client.Address); ok {
				add(a, claim{id, claimExplicit, time.Time{}})
			}
		case d.Device != nil:
			if d.Device.FixedIp != nil {
				if a, ok := parseAddr(*d.Device.FixedIp); ok {
					add(a, claim{id, claimReservation, time.Time{}})
				}
			}
			for _, s := range deref(deref(d.Device.Identifiers).Ipv4) {
				if a, ok := parseAddr(s); ok {
					add(a, claim{id, claimExplicit, time.Time{}})
				} else if p, ok := parsePrefix(s); ok {
					res.Ranges = append(res.Ranges, IdentityRange{p.Masked(), id})
				}
			}
		}
	}
	for _, l := range obs.Leases {
		if l.State != nil && *l.State != "active" {
			continue
		}
		if dev, ok := macs[lower(l.Mac)]; ok {
			if a, ok := parseAddr(l.Ip); ok {
				add(a, claim{dev, claimLease, l.ExpiresAt})
			}
		}
	}
	for _, n := range obs.Neighbors {
		if n.Stale && !obs.ActiveSources[n.IP] {
			continue // an old entry says nothing about who has the address now
		}
		if dev, ok := macs[lower(n.MAC)]; ok {
			add(n.IP, claim{dev, claimNeighbor, time.Time{}})
		}
	}
	for _, id := range sortedKeys(obs.Probes) {
		for _, ip := range obs.Probes[id].IPs {
			add(ip, claim{id, claimNeighbor, time.Time{}})
		}
	}

	ips := make([]netip.Addr, 0, len(claims))
	for ip := range claims {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
	for _, ip := range ips {
		cs := claims[ip]
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].beats(cs[j]) })
		winner := cs[0].device
		res.Owner[ip] = winner
		var rivals []string
		seen := map[string]bool{}
		for _, c := range cs {
			if !seen[c.device] {
				seen[c.device] = true
				rivals = append(rivals, c.device)
			}
		}
		if len(rivals) > 1 {
			sort.Strings(rivals)
			res.Conflicts = append(res.Conflicts, IdentityConflict{IP: ip, Devices: rivals, Winner: winner})
		}
	}

	// an old address stays mapped while it still carries connections, unless it is another
	// device's now (the new holder wins)
	if prev != nil {
		for _, dev := range sortedKeys(prev.Addresses) {
			for _, ip := range prev.Addresses[dev] {
				if _, taken := res.Owner[ip]; !taken && obs.ActiveSources[ip] {
					res.Owner[ip] = dev
				}
			}
		}
	}
	for ip, dev := range res.Owner {
		res.Addresses[dev] = append(res.Addresses[dev], ip)
	}
	for _, addrs := range res.Addresses {
		sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
	}
	sort.Slice(res.Ranges, func(i, j int) bool {
		a, b := res.Ranges[i], res.Ranges[j]
		if a.Prefix.Addr() != b.Prefix.Addr() {
			return a.Prefix.Addr().Less(b.Prefix.Addr())
		}
		return a.Prefix.Bits() < b.Prefix.Bits()
	})
	res.Discovered = uncovered(obs.Discovered, macs, res)
	// discovered devices own the addresses nobody else has
	for _, d := range res.Discovered {
		for _, ip := range d.IPs {
			if _, taken := res.Owner[ip]; !taken {
				res.Owner[ip] = d.ID
				res.Addresses[d.ID] = append(res.Addresses[d.ID], ip)
			}
		}
		sort.Slice(res.Addresses[d.ID], func(i, j int) bool { return res.Addresses[d.ID][i].Less(res.Addresses[d.ID][j]) })
	}
	return res
}

// uncovered returns the discovered devices that no configured device covers: all their MACs
// belong to a configured device, or (without a MAC) all their addresses do. Merging two devices
// after a randomized MAC works this way: the entry whose identifiers are now covered disappears
// (plan §2.3, Device in the spec).
func uncovered(found []DiscoveredDevice, macs map[string]string, id Identity) []DiscoveredDevice {
	var out []DiscoveredDevice
	for _, d := range found {
		covered := false
		if len(d.MACs) > 0 {
			covered = true
			for _, m := range d.MACs {
				if _, ok := macs[lower(m)]; !ok {
					covered = false
				}
			}
		} else if len(d.IPs) > 0 {
			covered = true
			for _, ip := range d.IPs {
				if _, ok := id.OwnerOf(ip); !ok {
					covered = false
				}
			}
		}
		if !covered {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
