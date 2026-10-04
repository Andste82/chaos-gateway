package compiler

import (
	"net/netip"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// HostLink is an interface of the host as the compiler sees it: the observed state that decides
// which device a configured MAC or name refers to (plan §3.4).
type HostLink struct {
	Name  string
	MAC   string
	Kind  string // bridge, veth, ... empty for physical devices
	Addrs []HostAddr
}

// HostAddr is one address of a HostLink. Secondary marks an address added after the interface
// already had one of the same family: the kernel's own address order then depends on history
// (DHCP renewals, manual changes), not on anything the configuration says.
type HostAddr struct {
	Prefix    netip.Prefix
	Secondary bool
}

// HostRoute is a default route of the main routing table.
type HostRoute struct {
	Dev     string
	Gateway netip.Addr
	Metric  int
}

// Host is what the compiler needs to know about the machine: its interfaces with their addresses
// and the default routes of the main table. It is observed state; internal/apply reads it.
type Host struct {
	Links    []HostLink
	Defaults []HostRoute
}

// Link returns the interface of that name.
func (h Host) Link(name string) (HostLink, bool) {
	for _, l := range h.Links {
		if l.Name == name {
			return l, true
		}
	}
	return HostLink{}, false
}

// Resolve finds the interface an InterfaceRef names. The MAC wins over the name: an interface that
// was renamed is still found, and the compiler uses its current name. A reference that carries a
// MAC never falls back to the name (it could be another device); bridges are not candidates, since
// a bridge takes the MAC of its first port.
func (h Host) Resolve(ref model.InterfaceRef) (HostLink, bool) {
	if ref.Mac != nil && *ref.Mac != "" {
		mac := strings.ToLower(*ref.Mac)
		for _, l := range h.Links {
			if !sharesParentMAC(l.Kind) && strings.ToLower(l.MAC) == mac {
				return l, true
			}
		}
		return HostLink{}, false
	}
	if ref.Name != nil {
		return h.Link(*ref.Name)
	}
	return HostLink{}, false
}

// sharesParentMAC reports whether devices of that kind carry the MAC of another interface: a
// bridge takes the MAC of its first port, a VLAN, macvlan or bond that of its parent.
func sharesParentMAC(kind string) bool {
	switch kind {
	case "bridge", "vlan", "macvlan", "macvtap", "bond":
		return true
	}
	return false
}

// DefaultGateway returns the gateway of the default route of the main table on dev (lowest metric).
func (h Host) DefaultGateway(dev string) (netip.Addr, bool) {
	var best *HostRoute
	for i, r := range h.Defaults {
		if r.Dev == dev && (best == nil || r.Metric < best.Metric) {
			best = &h.Defaults[i]
		}
	}
	if best == nil || !best.Gateway.IsValid() {
		return netip.Addr{}, false
	}
	return best.Gateway, true
}

// FirstV4 returns the interface's IPv4 address: a non-secondary one if there is one, else the
// lowest. The kernel's own address order (what a plain "first" would follow) depends on history,
// not on anything the configuration says, and reorders unpredictably across a DHCP renewal or a
// reboot.
func (l HostLink) FirstV4() (netip.Prefix, bool) {
	var best *HostAddr
	for i := range l.Addrs {
		a := &l.Addrs[i]
		if !a.Prefix.Addr().Is4() {
			continue
		}
		switch {
		case best == nil:
			best = a
		case best.Secondary && !a.Secondary:
			best = a
		case best.Secondary == a.Secondary && a.Prefix.Addr().Less(best.Prefix.Addr()):
			best = a
		}
	}
	if best == nil {
		return netip.Prefix{}, false
	}
	return best.Prefix, true
}
