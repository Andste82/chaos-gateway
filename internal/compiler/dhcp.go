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
// no target.
func (t *Target) compileKea(cfg *model.Configuration, idx *domain.Index) {
	var nets []string
	on := map[string]bool{}
	for id, n := range idx.Networks {
		if n.Lan != nil && n.Lan.Dhcp != nil && (n.Lan.Dhcp.Enabled == nil || *n.Lan.Dhcp.Enabled) {
			nets = append(nets, id)
			on[id] = true
		}
	}
	if len(nets) == 0 {
		return
	}
	reserved := reservingDevices(idx, on)
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
	// reservations go to the subnet of the device's network; a network with DHCP whose subnet was
	// not compiled has its error reported already
	for _, r := range reserved {
		for i := range c.Subnets {
			if c.Subnets[i].Network == r.network {
				c.Subnets[i].Reservations = append(c.Subnets[i].Reservations, r.res)
				break
			}
		}
	}
	text, err := c.Render()
	if err != nil {
		t.errorf(CodeDHCP, "", "the DHCP configuration cannot be generated: %v", err)
		return
	}
	t.Kea = &KeaTarget{Config: c, Text: text, Networks: ids}
}

// reservation is one device's DHCP reservation in its network.
type reservation struct {
	network string
	res     kea.Reservation
}

// reservingDevices returns the reservations of the devices with a fixed address, in device ID
// order. A reservation is for the device's first MAC (plan §2.7): the UI puts the MAC of the
// current lease first when it creates one. The validator has already refused a fixed address that
// is not an address, one without a network or a MAC and one outside its network's subnet, so
// those cases only skip the device. A device whose network has DHCP off gets no reservation: the
// address stays configured but nothing hands it out, which domain.Warnings reports at the device's
// fixed_ip (fixed_ip_dhcp_off), also while no network serves DHCP and there is no Kea target (#94).
func reservingDevices(idx *domain.Index, on map[string]bool) []reservation {
	ids := make([]string, 0, len(idx.Devices))
	for id := range idx.Devices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []reservation
	for _, id := range ids {
		d := idx.Devices[id]
		if d.Device == nil || d.Device.FixedIp == nil || d.Device.Identifiers == nil || d.Device.Identifiers.Macs == nil {
			continue
		}
		ip, err := netip.ParseAddr(*d.Device.FixedIp)
		macs := *d.Device.Identifiers.Macs
		_, known := idx.Networks[d.Network]
		if err != nil || len(macs) == 0 || !known {
			continue // refused by the validator
		}
		if !on[d.Network] {
			continue // DHCP off: a warning of the validation (fixed_ip_dhcp_off)
		}
		out = append(out, reservation{network: d.Network, res: kea.Reservation{MAC: strings.ToLower(macs[0]), IP: ip}})
	}
	return out
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
	} else if ps := kea.DefaultPool(br.Address, gw); len(ps) > 0 {
		sub.Pools = ps
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
// discovered devices from the observed state. The map exists even while no device is known, and a
// device keeps its numeral from one compile to the next (Input.DeviceNums, assignDeviceNums), so
// a device's address change, a new device and a device that went are all element updates of the
// single map (executor.NftAddMapElements/NftDelMapElements, plan §2.3); a full apply fills it from
// the latest observed state, like compileKea and compileRouting already do.
func (t *Target) compileIdentity(idx *domain.Index, id *domain.Identity, prev map[string]int, high int) {
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
	var ids []string
	for did := range names {
		ids = append(ids, did)
	}
	t.DeviceNums, t.DeviceNumHigh = assignDeviceNums(ids, prev, high)
	sort.Strings(ids)
	md := MapDef{KeyType: []string{"ipv4_addr"}, ValueType: "mark"}
	for _, did := range ids {
		num := strconv.Itoa(t.DeviceNums[did])
		el := append([]string(nil), addrs[did]...)
		sort.Strings(el)
		for _, a := range el {
			md.Elements = append(md.Elements, MapElement{Key: a, Value: num})
		}
	}
	md.Name = hashMapName(IdentityMapBase, md.KeyType, md.ValueType, md.Flags)
	t.identityMap = md
	t.IdentityMap = md.Name
}

// MaxDeviceNum is the highest device number: the identity map's values are marks (32 bits), and the
// bound keeps them in an int on every platform.
const MaxDeviceNum = 1<<31 - 1

// assignDeviceNums numbers the devices of the identity map from 1 (0 is "no device"). A device that
// had a number in the previous allocation keeps it. A new device takes the number above the highest
// one ever handed out (high, carried from compile to compile as Target.DeviceNumHigh), so the number
// of a device that went is not given to another device while this process runs: an element of the
// kernel that may still carry it (an apply that failed half-way, a later consumer of the map that
// copies the number into packet or connection state) never names the wrong device. Only when the
// numbers are used up does a new device take the lowest one no device has, preferring one the
// previous allocation did not hold either. With no previous allocation (the first compile of a
// process) the devices are numbered in the order of their ids.
func assignDeviceNums(ids []string, prev map[string]int, high int) (map[string]int, int) {
	sort.Strings(ids)
	high = min(max(high, 0), MaxDeviceNum)
	nums := make(map[string]int, len(ids))
	used := map[int]bool{}
	for _, d := range ids {
		if n, had := prev[d]; had && n >= 1 && n <= MaxDeviceNum && !used[n] {
			nums[d], used[n] = n, true
			high = max(high, n)
		}
	}
	prevUsed := map[int]bool{}
	for _, n := range prev {
		prevUsed[n] = true
	}
	lowest := func(avoidPrev bool) int {
		for n := 1; n <= MaxDeviceNum; n++ {
			if !used[n] && (!avoidPrev || !prevUsed[n]) {
				return n
			}
		}
		return 0
	}
	for _, d := range ids {
		if _, done := nums[d]; done {
			continue
		}
		var n int
		if high < MaxDeviceNum {
			high++
			n = high
		} else if n = lowest(true); n == 0 {
			n = lowest(false)
		}
		nums[d], used[n] = n, true
	}
	return nums, high
}
