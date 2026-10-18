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
	// Stale marks an entry the kernel has not confirmed lately (state STALE, DELAY, PROBE). It
	// only counts while the address still carries connections, or while an enabled port forward
	// sends to the device and no other device claims the address (P31-AUDIT-14).
	Stale bool
	// Seen is when the engine first saw the address with this MAC: of two confirmed entries of a
	// device, the one seen later is its more current address (a device that moved keeps its old
	// entry REACHABLE for a while). Zero when unknown.
	Seen time.Time
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
	// Current are the same addresses, the most current claim first: an explicit address, then leases
	// (the newest first), what the neighbor table confirms (the entry seen last first), a reservation, and last an address that
	// stays mapped only because it still carries connections (a stale neighbor entry, an address the
	// device had before). A forward sends its connections to the first one (ForwardStatus).
	Current map[string][]netip.Addr
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
	// lingering marks a claim that only stands because the address still carries connections (a
	// stale neighbor entry): it decides the owner like any neighbor claim, but is the least current.
	lingering bool
	// seen is when the neighbor entry behind a neighbor claim was first seen (Neighbor.Seen).
	seen time.Time
	// forwardOnly marks a stale neighbor entry without connections that stands only because a port forward
	// sends to the device: any claim of another device on the address takes it away (yieldToOthers).
	forwardOnly bool
}

// yieldToOthers drops the forwardOnly claims of an address that another device claims too: the new
// holder of an address wins over a forward target's stale entry.
func yieldToOthers(cs []claim) []claim {
	out := cs[:0:0]
	for _, c := range cs {
		if c.forwardOnly && claimedByOther(cs, c.device) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func claimedByOther(cs []claim, dev string) bool {
	for _, o := range cs {
		if o.device != dev {
			return true
		}
	}
	return false
}

// current reports whether the claim says more about the device's address now than o does
// (Identity.Current).
func (c claim) current(o claim) bool {
	if c.lingering != o.lingering {
		return !c.lingering
	}
	if c.strength == claimNeighbor && o.strength == claimNeighbor && !c.seen.Equal(o.seen) {
		return c.seen.After(o.seen)
	}
	return c.beats(o)
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
	res := Identity{Addresses: map[string][]netip.Addr{}, Current: map[string][]netip.Addr{}, Owner: map[netip.Addr]string{}}
	// best is the claim that gave each address its owner: how current the address is for the device
	best := map[netip.Addr]claim{}

	for _, id := range sortedKeys(idx.Devices) {
		d := idx.Devices[id]
		switch {
		case d.Client != nil:
			if a, ok := parseAddr(d.Client.Address); ok {
				add(a, claim{device: id, strength: claimExplicit})
			}
		case d.Device != nil:
			if d.Device.FixedIp != nil {
				if a, ok := parseAddr(*d.Device.FixedIp); ok {
					add(a, claim{device: id, strength: claimReservation})
				}
			}
			for _, s := range deref(deref(d.Device.Identifiers).Ipv4) {
				if a, ok := parseAddr(s); ok {
					add(a, claim{device: id, strength: claimExplicit})
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
				add(a, claim{device: dev, strength: claimLease, stamp: l.ExpiresAt})
			}
		}
	}
	// the devices an enabled port forward sends to: a server that only answers is the common target of a
	// forward, and it sends nothing of its own that would keep its neighbor entry confirmed
	targets := map[string]bool{}
	for _, f := range Forwards(cfg) {
		if f.Enabled && f.Device != "" {
			targets[f.Device] = true
		}
	}
	for _, n := range obs.Neighbors {
		dev, ok := macs[lower(n.MAC)]
		if !ok {
			continue
		}
		c := claim{device: dev, strength: claimNeighbor, lingering: n.Stale, seen: n.Seen}
		if n.Stale && !obs.ActiveSources[n.IP] {
			if !targets[dev] {
				continue // an old entry says nothing about who has the address now
			}
			// a forward's target keeps its stale entry as a lingering claim, so the forward keeps sending
			// there (the kernel probes the entry when a connection comes, and an entry that fails goes); it
			// gives way to any claim of another device (P31-AUDIT-14, decided 2026-10-18)
			c.forwardOnly = true
		}
		add(n.IP, c)
	}
	for _, id := range sortedKeys(obs.Probes) {
		for _, ip := range obs.Probes[id].IPs {
			add(ip, claim{device: id, strength: claimNeighbor})
		}
	}

	ips := make([]netip.Addr, 0, len(claims))
	for ip := range claims {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
	for _, ip := range ips {
		cs := yieldToOthers(claims[ip])
		if len(cs) == 0 {
			continue
		}
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].beats(cs[j]) })
		winner := cs[0].device
		res.Owner[ip] = winner
		top := cs[0]
		for _, c := range cs[1:] {
			if c.device == winner && c.current(top) {
				top = c
			}
		}
		best[ip] = top
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
					best[ip] = claim{device: dev, lingering: true}
				}
			}
		}
	}
	for ip, dev := range res.Owner {
		res.Addresses[dev] = append(res.Addresses[dev], ip)
	}
	for dev, addrs := range res.Addresses {
		sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
		cur := append([]netip.Addr(nil), addrs...)
		sort.SliceStable(cur, func(i, j int) bool {
			a, b := best[cur[i]], best[cur[j]]
			return a.current(b) && !b.current(a)
		})
		res.Current[dev] = cur
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
		if len(res.Addresses[d.ID]) > 0 {
			res.Current[d.ID] = append([]netip.Addr(nil), res.Addresses[d.ID]...)
		}
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
