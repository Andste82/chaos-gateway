package engine

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is what the DHCP test actions (plan §2.7, M23) need from the engine beyond the compiler: the address that
// force_new_ip moves a device to, chosen when the action is written, and the deletion of leases (delete_lease), which acts once
// and so cannot be part of the configuration of the DHCP server.

// EventDHCPLeasesDeleted says that the action delete_lease deleted leases of the DHCP server: the overlay, the network
// and, for a device's action, the device, with the number of leases that were deleted.
const EventDHCPLeasesDeleted = "dhcp_leases_deleted"

// completeDHCPAction fills in what only the engine knows when an action is written: the address of force_new_ip. A request
// that names one is checked, one that does not gets a free address of the pools of the device's network. Every write of the
// action chooses again, so writing it again moves the device again. The request has passed ValidateOverlay.
func (o *owner) completeDHCPAction(cfg *model.Configuration, req *model.OverlayRequest) error {
	if req.Dhcp == nil || req.Dhcp.Action != model.ForceNewIp || req.Target == nil || req.Target.Device == nil {
		return nil
	}
	bad := func(path, format string, a ...any) error {
		return domain.ValidationErrors{{Path: path, Code: domain.CodeInvalidDHCPAction, Message: fmt.Sprintf(format, a...)}}
	}
	id := strings.ToLower(*req.Target.Device)
	idx, _ := domain.BuildIndex(cfg)
	dev, ok := o.dhcpDevice(idx, id)
	if !ok || len(dev.macs) == 0 {
		return bad("/target", "the device has no MAC address, so the DHCP server cannot reserve an address for it")
	}
	n := idx.Networks[dev.network]
	if n == nil || n.Lan == nil {
		return bad("/target", "the device is not in a local test network")
	}
	scope := n.Lan.Dhcp
	if scope == nil || (scope.Enabled != nil && !*scope.Enabled) {
		return bad("/target", "DHCP is off on the network %q of the device", n.Name)
	}
	gw, err := netip.ParsePrefix(n.Lan.Address)
	if err != nil {
		return bad("/target", "the network %q has no valid address", n.Name)
	}
	subnet := gw.Masked()
	taken := o.dhcpTaken(idx, id, gw)
	if req.Dhcp.Ip != nil {
		ip, err := netip.ParseAddr(*req.Dhcp.Ip)
		switch {
		case err != nil || !subnet.Contains(ip):
			return bad("/dhcp/ip", "the address is not inside the network %q (%s)", n.Name, subnet)
		case taken[ip] && !dev.holds(ip):
			return bad("/dhcp/ip", "the address belongs to another host or to the network itself")
		}
		return nil
	}
	pools, err := compiler.ScopePools(gw, scope)
	if err != nil || len(pools) == 0 {
		return bad("/target", "the network %q has no pool to take an address from", n.Name)
	}
	ip, ok := freeAddress(pools, taken)
	if !ok {
		return bad("/target", "no address of the pools of the network %q is free", n.Name)
	}
	d := *req.Dhcp
	s := ip.String()
	d.Ip = &s
	req.Dhcp = &d
	return nil
}

// dhcpDevice is what the engine knows of a device the DHCP server sees: its network and MAC addresses, from the configuration for a
// configured device (it need not have been seen) and from what was observed for a discovered one, and the addresses it holds.
type dhcpDevice struct {
	network string
	macs    []string
	addrs   []netip.Addr
}

func (d dhcpDevice) holds(ip netip.Addr) bool { return slices.Contains(d.addrs, ip) }

func (o *owner) dhcpDevice(idx *domain.Index, id string) (dhcpDevice, bool) {
	var out dhcpDevice
	if d, ok := idx.Devices[id]; ok && d.Device != nil {
		out.network = d.Network
		if d.Device.Identifiers != nil && d.Device.Identifiers.Macs != nil {
			out.macs = *d.Device.Identifiers.Macs
		}
	}
	for _, d := range o.snap.Devices {
		if !strings.EqualFold(d.ID, id) {
			continue
		}
		out.addrs = d.Addresses
		if out.network == "" {
			out.network = d.Network
		}
		if len(out.macs) == 0 {
			out.macs = d.MACs
		}
	}
	return out, out.network != "" || len(out.macs) > 0
}

// dhcpTaken are the addresses of a network that a device is not moved to: the gateway, the network and broadcast
// addresses, the addresses the DHCP server leased (whoever holds them), those of every known device, the fixed addresses of
// the configuration and the addresses the actions of this kind move other devices to (an action on this device is replaced
// or competes with the one being written, so its address is free again).
func (o *owner) dhcpTaken(idx *domain.Index, device string, gw netip.Prefix) map[netip.Addr]bool {
	taken := map[netip.Addr]bool{gw.Addr(): true}
	subnet := gw.Masked()
	taken[subnet.Addr()] = true
	if b, ok := lastAddress(subnet); ok {
		taken[b] = true
	}
	for _, l := range o.snap.Leases {
		if l.State != nil && *l.State == "expired" {
			continue
		}
		if a, err := netip.ParseAddr(l.Ip); err == nil {
			taken[a] = true
		}
	}
	for _, d := range o.snap.Devices {
		for _, a := range d.Addresses {
			taken[a] = true
		}
	}
	for _, d := range idx.Devices {
		if d.Device != nil && d.Device.FixedIp != nil {
			if a, err := netip.ParseAddr(*d.Device.FixedIp); err == nil {
				taken[a] = true
			}
		}
	}
	for _, ov := range o.ov.list {
		if ov.Target != nil && ov.Target.Device != nil && strings.EqualFold(*ov.Target.Device, device) {
			continue
		}
		if ov.Kind == model.OverlayKindDhcp && ov.Dhcp != nil && ov.Dhcp.Ip != nil {
			if a, err := netip.ParseAddr(*ov.Dhcp.Ip); err == nil {
				taken[a] = true
			}
		}
	}
	return taken
}

// lastAddress is the broadcast address of a prefix.
func lastAddress(p netip.Prefix) (netip.Addr, bool) {
	if !p.Addr().Is4() {
		return netip.Addr{}, false
	}
	a := p.Masked().Addr().As4()
	host := 32 - p.Bits()
	for i := 3; i >= 0 && host > 0; i-- {
		bits := min(host, 8)
		a[i] |= byte(1<<bits - 1)
		host -= bits
	}
	return netip.AddrFrom4(a), true
}

// freeAddress is the last address of the pools that is not taken, going down from the end of the last pool: the DHCP server
// allocates from the start of a pool, so the address a device is moved to is the one it takes last.
func freeAddress(pools []kea.Pool, taken map[netip.Addr]bool) (netip.Addr, bool) {
	for i := len(pools) - 1; i >= 0; i-- {
		for a := pools[i].End; a.IsValid() && !a.Less(pools[i].Start); a = a.Prev() {
			if !taken[a] {
				return a, true
			}
		}
	}
	return netip.Addr{}, false
}

// deleteLeases does the deletions of leases a target asks for that were not done yet, and forgets the ones the target no
// longer lists. It returns the first failure, with the deletions after it left for the next try: the DHCP server that was down
// when the action was written does them when it is back, while the overlay of the action exists.
func (e *Engine) deleteLeases(ctx context.Context, t *compiler.KeaTarget) error {
	e.dhcp.mu.Lock()
	if e.dhcp.deleted == nil {
		e.dhcp.deleted = map[string]bool{}
	}
	listed := map[string]bool{}
	var pending []compiler.LeaseDeletion
	if t != nil {
		for _, d := range t.Deletions {
			listed[d.Key] = true
			if !e.dhcp.deleted[d.Key] {
				pending = append(pending, d)
			}
		}
	}
	for k := range e.dhcp.deleted {
		if !listed[k] {
			delete(e.dhcp.deleted, k)
		}
	}
	e.dhcp.mu.Unlock()
	for _, d := range pending {
		n, err := e.cfg.DHCP.DeleteLeases(ctx, d.Subnet, d.MACs)
		if err != nil {
			return &leaseDeletionError{err}
		}
		e.dhcp.mu.Lock()
		e.dhcp.deleted[d.Key] = true
		e.dhcp.mu.Unlock()
		data := map[string]any{"overlay": d.Overlay, "network": d.Network, "count": n, "subject": Subject{Kind: "overlay", ID: d.Overlay}}
		if d.Device != "" {
			data["device"] = d.Device
		}
		e.Emit(EventDHCPLeasesDeleted, data)
	}
	return nil
}
