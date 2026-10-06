package compiler

import (
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// CodeDHCP is the problem code of DHCP scopes that cannot be compiled.
const CodeDHCP = "dhcp"

// KeaTarget is the DHCP configuration of the gateway: one Kea subnet per network with DHCP on, bound
// to the network's bridge (plan §2.7). The engine hands Text's document to Kea with `config-set`.
type KeaTarget struct {
	Config kea.Config `json:"config"`
	// Text is the rendered configuration.
	Text string `json:"text"`
	// Networks maps a Kea subnet id to the UUID of its network: lease events carry the id.
	Networks map[int]string `json:"networks"`
}

// defaultLease is the lease time of a scope that names none (the spec's default).
const defaultLease = time.Hour

// compileKea builds the DHCP configuration. Without a network that has DHCP switched on, there is
// still a target (no subnets): Kea then answers nobody.
func (t *Target) compileKea(cfg *model.Configuration, idx *domain.Index) {
	var nets []string
	for id, n := range idx.Networks {
		if n.Lan != nil && n.Lan.Dhcp != nil && (n.Lan.Dhcp.Enabled == nil || *n.Lan.Dhcp.Enabled) {
			nets = append(nets, id)
		}
	}
	if len(nets) == 0 {
		return
	}
	sort.Slice(nets, func(i, j int) bool {
		a, b := idx.Networks[nets[i]], idx.Networks[nets[j]]
		if a.Name != b.Name {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return nets[i] < nets[j]
	})
	c := kea.Config{Script: kea.HookScript}
	taken := map[int]bool{}
	byNet := map[string]int{}
	ids := map[int]string{}
	for _, id := range nets {
		n := idx.Networks[id]
		var br *Bridge
		for i := range t.Bridges {
			if t.Bridges[i].NetworkID == id {
				br = &t.Bridges[i]
			}
		}
		if br == nil {
			continue // the network was not compiled (its error is reported already)
		}
		sid := kea.SubnetID(id, taken)
		taken[sid] = true
		byNet[id], ids[sid] = sid, id
		sub, ok := t.keaSubnet(sid, id, n, br)
		if !ok {
			continue
		}
		c.Subnets = append(c.Subnets, sub)
	}
	// reservations: a device with a fixed address gets it on its network, for its first MAC
	for did, d := range idx.Devices {
		if d.Device == nil || d.Device.FixedIp == nil || *d.Device.FixedIp == "" {
			continue
		}
		ip, err := netip.ParseAddr(*d.Device.FixedIp)
		if err != nil {
			t.errorf(CodeDHCP, "", "the fixed address %q of device %q is not valid", *d.Device.FixedIp, d.Name)
			continue
		}
		var mac string
		if d.Device.Identifiers != nil && d.Device.Identifiers.Macs != nil && len(*d.Device.Identifiers.Macs) > 0 {
			mac = strings.ToLower((*d.Device.Identifiers.Macs)[0])
		}
		if mac == "" {
			t.warn(CodeDHCP, "", "device %q has a fixed address but no MAC address: the reservation needs one", d.Name)
			continue
		}
		placed := false
		for i := range c.Subnets {
			sub := &c.Subnets[i]
			if (d.Network == "" || d.Network == sub.Network) && sub.Subnet.Contains(ip) {
				sub.Reservations = append(sub.Reservations, kea.Reservation{MAC: mac, IP: ip})
				placed = true
				break
			}
		}
		if !placed {
			t.warn(CodeDHCP, "", "the fixed address %s of device %q is in none of the networks with DHCP: no reservation is made", ip, d.Name)
		}
		_ = did
	}
	text, err := c.Render()
	if err != nil {
		t.errorf(CodeDHCP, "", "the DHCP configuration cannot be generated: %v", err)
		return
	}
	t.Kea = &KeaTarget{Config: c, Text: text, Networks: ids}
}

func (t *Target) keaSubnet(sid int, id string, n *domain.NetInfo, br *Bridge) (kea.Subnet, bool) {
	scope := n.Lan.Dhcp
	sub := kea.Subnet{ID: sid, Network: id, Subnet: br.Address.Masked(), Interface: br.Name, LeaseSeconds: int(defaultLease.Seconds())}
	gw := br.Address.Addr()
	if scope.LeaseTime != nil {
		d, err := time.ParseDuration(*scope.LeaseTime)
		if err != nil || d < time.Second {
			t.errorf(CodeDHCP, n.Name, "the lease time %q of network %q is not a duration of at least a second", *scope.LeaseTime, n.Name)
			return sub, false
		}
		sub.LeaseSeconds = int(d.Seconds())
	}
	if scope.Pools != nil && len(*scope.Pools) > 0 {
		for _, p := range *scope.Pools {
			a, e1 := netip.ParseAddr(p.Start)
			b, e2 := netip.ParseAddr(p.End)
			if e1 != nil || e2 != nil {
				t.errorf(CodeDHCP, n.Name, "a pool of network %q is not valid", n.Name)
				return sub, false
			}
			sub.Pools = append(sub.Pools, kea.Pool{Start: a, End: b})
		}
	} else if p, ok := kea.DefaultPool(br.Address, gw); ok {
		sub.Pools = []kea.Pool{p}
	} else {
		t.errorf(CodeDHCP, n.Name, "network %q is too small for a DHCP pool: name one", n.Name)
		return sub, false
	}
	sub.Router, sub.DNS = gw, []netip.Addr{gw}
	if o := scope.Options; o != nil {
		if o.Router != nil {
			if a, err := netip.ParseAddr(*o.Router); err == nil {
				sub.Router = a
			}
		}
		if o.DnsServers != nil && len(*o.DnsServers) > 0 {
			sub.DNS = nil
			for _, s := range *o.DnsServers {
				if a, err := netip.ParseAddr(s); err == nil {
					sub.DNS = append(sub.DNS, a)
				}
			}
		}
		if o.NtpServers != nil {
			for _, s := range *o.NtpServers {
				if a, err := netip.ParseAddr(s); err == nil {
					sub.NTP = append(sub.NTP, a)
				}
			}
		}
		if o.Domain != nil {
			sub.Domain = *o.Domain
		}
		if o.Custom != nil {
			for _, c := range *o.Custom {
				sub.Custom = append(sub.Custom, kea.CustomOption{Code: c.Code, Value: c.Value})
			}
		}
	}
	return sub, true
}

// compileIdentity builds the identity map (plan §3.3): every known device's current addresses
// mapped to its numeral, in one nftables map (`ident4`) instead of the Phase 1 per-device address
// sets M6a-07 deferred replacing (`dev_<id>`, one `nft add set` and one verified object per
// device). Known devices are configured devices, WireGuard clients and probes from the index, and
// discovered devices from the observed state. A device's address change is then one incremental
// element update of the single map (executor.NftAddMapElements/NftDelMapElements), the map
// counterpart of what the old per-device sets already did one set at a time; a full apply fills it
// from the latest observed state, like compileKea and compileRouting already do.
func (t *Target) compileIdentity(idx *domain.Index, id *domain.Identity) {
	addrs := map[string][]string{}
	names := map[string]bool{}
	for did := range idx.Devices {
		names[did] = true
		if id != nil {
			for _, a := range id.Addresses[did] {
				addrs[did] = append(addrs[did], a.String())
			}
		}
	}
	if id != nil {
		for _, d := range id.Discovered {
			names[d.ID] = true
			// id.Addresses is the resolved identity (the stronger claim wins, §2.3); d.IPs is only
			// what was sighted, which can still include an address another device has already won.
			for _, a := range id.Addresses[d.ID] {
				addrs[d.ID] = append(addrs[d.ID], a.String())
			}
		}
	}
	if len(names) == 0 {
		return
	}
	var ids []string
	for did := range names {
		ids = append(ids, did)
	}
	sort.Strings(ids)
	t.DeviceNums = map[string]int{}
	md := MapDef{KeyType: []string{"ipv4_addr"}, ValueType: "mark"}
	for num, did := range ids {
		t.DeviceNums[did] = num + 1 // 0 is reserved for "no device"
		el := append([]string(nil), addrs[did]...)
		sort.Strings(el)
		for _, a := range el {
			md.Elements = append(md.Elements, MapElement{Key: a, Value: strconv.Itoa(num + 1)})
		}
	}
	md.Name = hashMapName("ident4", md.KeyType, md.ValueType)
	t.identityMap = md
	t.IdentityMap = md.Name
}
