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
	Addrs []netip.Prefix
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
			if l.Kind != "bridge" && strings.ToLower(l.MAC) == mac {
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

// FirstV4 returns the first IPv4 address of the interface.
func (l HostLink) FirstV4() (netip.Prefix, bool) {
	for _, a := range l.Addrs {
		if a.Addr().Is4() {
			return a, true
		}
	}
	return netip.Prefix{}, false
}
