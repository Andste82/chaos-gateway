package run

import (
	"errors"
	"net/netip"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file answers what a scope of a scenario stands for in the snapshot of the moment: its devices,
// whether two targets overlap (one run per target) and which overlays and connections lie in it.

func lower(s string) string { return strings.ToLower(s) }

func deref[T any](p *T) T {
	var z T
	if p != nil {
		z = *p
	}
	return z
}

// scopeKind returns which member of a scope is set.
func scopeKind(s model.Scope) string {
	switch {
	case s.Device != nil:
		return "device"
	case s.Group != nil:
		return "group"
	case s.Network != nil:
		return "network"
	case s.RemoteNetwork != nil:
		return "remote_network"
	case s.Global != nil:
		return "global"
	}
	return ""
}

// scopeName is the scope as a person reads it, from the names the snapshot knows.
func scopeName(snap *engine.Snapshot, s model.Scope) string {
	switch scopeKind(s) {
	case "device":
		for _, d := range snap.Devices {
			if lower(d.ID) == lower(*s.Device) {
				return "device " + d.Name
			}
		}
		return "device " + *s.Device
	case "group":
		if snap.Config != nil && snap.Config.Groups != nil {
			for id, g := range *snap.Config.Groups {
				if lower(id) == lower(*s.Group) {
					return "group " + g.Name
				}
			}
		}
		return "group " + *s.Group
	case "network":
		if snap.Config != nil && snap.Config.Networks != nil {
			for id, n := range *snap.Config.Networks {
				if lower(id) != lower(*s.Network) {
					continue
				}
				if name := networkName(n); name != "" {
					return "network " + name
				}
			}
		}
		return "network " + *s.Network
	}
	return scopeKind(s)
}

func networkName(n model.Network) string {
	switch disc, _ := n.Discriminator(); disc {
	case "wireguard":
		if w, err := n.AsWireGuardNetwork(); err == nil {
			return w.Name
		}
	default:
		if l, err := n.AsLanNetwork(); err == nil {
			return l.Name
		}
	}
	return ""
}

// scopeDevices returns the devices a scope covers in the snapshot: the configured and the discovered
// ones. A scope that names a device the snapshot does not know has none.
func scopeDevices(snap *engine.Snapshot, s model.Scope) []engine.DeviceState {
	var out []engine.DeviceState
	members := map[string]bool{}
	if scopeKind(s) == "group" && snap.Config != nil && snap.Config.Groups != nil {
		for id, g := range *snap.Config.Groups {
			if lower(id) == lower(*s.Group) {
				for _, m := range deref(g.Members) {
					members[lower(m)] = true
				}
			}
		}
	}
	for _, d := range snap.Devices {
		switch scopeKind(s) {
		case "device":
			if lower(d.ID) == lower(*s.Device) {
				out = append(out, d)
			}
		case "group":
			if members[lower(d.ID)] {
				out = append(out, d)
			}
		case "network":
			if lower(d.Network) == lower(*s.Network) {
				out = append(out, d)
			}
		}
	}
	return out
}

// scopeTokens is the set a scope is compared by: the ids of its devices (members of a group, whether the
// snapshot knows them or not) and, for a network, the network itself. Two targets overlap when their sets
// do; a scope that is `global` overlaps everything.
func scopeTokens(snap *engine.Snapshot, s model.Scope) map[string]bool {
	out := map[string]bool{}
	switch scopeKind(s) {
	case "global":
		out["*"] = true
	case "device":
		out["d:"+lower(*s.Device)] = true
	case "group":
		if snap.Config != nil && snap.Config.Groups != nil {
			for id, g := range *snap.Config.Groups {
				if lower(id) == lower(*s.Group) {
					for _, m := range deref(g.Members) {
						out["d:"+lower(m)] = true
					}
				}
			}
		}
		for _, d := range scopeDevices(snap, s) {
			out["d:"+lower(d.ID)] = true
		}
	case "network":
		out["n:"+lower(*s.Network)] = true
		for _, d := range scopeDevices(snap, s) {
			out["d:"+lower(d.ID)] = true
		}
	case "remote_network":
		rn := s.RemoteNetwork
		switch {
		case rn.Client != nil:
			out["d:"+lower(*rn.Client)] = true
		case rn.Link != nil:
			out["n:"+lower(*rn.Link)] = true
		}
	}
	return out
}

// overlap reports whether two token sets share a member.
func overlap(a, b map[string]bool) bool {
	if a["*"] && len(b) > 0 || b["*"] && len(a) > 0 {
		return true
	}
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// overlayTokens is the set of devices and networks an overlay acts on. A tunnel fault and a WireGuard
// action have no target: they name the client or link whose tunnel they affect.
func overlayTokens(snap *engine.Snapshot, ov model.Overlay) map[string]bool {
	if ov.Target != nil {
		return scopeTokens(snap, *ov.Target)
	}
	out := map[string]bool{}
	var client, link *string
	switch {
	case ov.Wireguard != nil:
		client, link = ov.Wireguard.Client, ov.Wireguard.Link
	case ov.Fault != nil && ov.Fault.Tunnel != nil:
		client, link = ov.Fault.Tunnel.Client, ov.Fault.Tunnel.Link
	}
	if client != nil {
		out["d:"+lower(*client)] = true
		// the client's network is hit as far as it is the client's tunnel; the network itself is not
	}
	if link != nil {
		out["n:"+lower(*link)] = true
	}
	return out
}

// ---- connections

// established reports whether a tracked connection has been established: a TCP connection past the
// handshake, any other flow that has been answered. A TCP entry in CLOSE is over, but it ended in two
// ways: after a handshake (a connection that was reset) or in place of one (a refused attempt, one SYN
// answered by a RST). Only the first one counts, and the two differ in what the originator sent: a
// refused attempt is one packet, a handshake is at least two (the SYN and the ACK or the RST that follows).
func established(f engine.Flow) bool {
	st := strings.ToUpper(f.State)
	if f.Protocol == "tcp" {
		switch st {
		case "", "SYN_SENT", "SYN_RECV", "NONE":
			return false
		case "CLOSE":
			return f.Upload.Packets >= 2 && f.Download.Packets >= 1
		}
		return true
	}
	return st != "UNREPLIED" && f.Download.Packets > 0
}

// matchFlow reports whether a connection is one the match selects: the protocol, a destination port, and
// the destination address (an address or prefix, a network, the uplink). A host name needs the resolver
// (M20) and is refused when the scenario is checked, so it never reaches here.
func matchFlow(nets *flowNets, m model.TrafficMatch, f engine.Flow) bool {
	if p := deref(m.Protocol); p != "" && p != model.ProtocolAny && string(p) != f.Protocol {
		return false
	}
	ports, ranges := deref(m.Ports), deref(m.PortRanges)
	if len(ports) > 0 || len(ranges) > 0 {
		hit := false
		for _, p := range ports {
			hit = hit || p == f.DPort
		}
		for _, r := range ranges {
			hit = hit || (f.DPort >= r.From && f.DPort <= r.To)
		}
		if !hit {
			return false
		}
	}
	d := m.Destination
	if d == nil {
		return true
	}
	switch {
	case d.Cidr != nil:
		if pfx, err := netip.ParsePrefix(*d.Cidr); err == nil {
			return pfx.Contains(f.Dst)
		}
		a, err := netip.ParseAddr(*d.Cidr)
		return err == nil && a == f.Dst
	case d.Network != nil:
		for _, p := range nets.network(*d.Network) {
			if p.Contains(f.Dst) {
				return true
			}
		}
		return false
	case d.Uplink != nil:
		return nets != nil && nets.world != nil && nets.viaUplink(f.Dst)
	}
	return false
}

// flowNets answers where an address lies for the destinations `network` and `uplink` of a check or a wait, from the
// same world the rules and the capture selector use (domain.World): the prefixes of a network are its subnet,
// the networks behind a hub's clients, a link's routes and a LAN's downstream routes, and what is not uplink is
// every network and the management prefixes. One is built per snapshot; err is set when the world cannot be built.
type flowNets struct {
	world     *domain.World
	nonUplink []netip.Prefix
	err       error
}

func newFlowNets(snap *engine.Snapshot) *flowNets {
	if snap == nil || snap.Config == nil {
		return &flowNets{err: errors.New("the gateway has no configuration")}
	}
	w, err := domain.NewWorld(snap.Config, snap.Overlays)
	if err != nil {
		return &flowNets{err: err}
	}
	return &flowNets{world: w, nonUplink: w.NonUplinkPrefixes()}
}

func (n *flowNets) network(id string) []netip.Prefix {
	if n == nil || n.world == nil {
		return nil
	}
	return n.world.NetworkPrefixes(id)
}

func (n *flowNets) viaUplink(a netip.Addr) bool {
	for _, p := range n.nonUplink {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// destNeedsNets reports whether a destination looks at where an address lies (`network` or `uplink`), which cannot
// be answered when the world of the gateway cannot be built.
func destNeedsNets(d *model.Destination) bool {
	return d != nil && (d.Network != nil || d.Uplink != nil)
}

// flowInScope reports whether the connection's initiator is in the scope's set of devices (or in a
// network scope's network).
func flowInScope(devices map[string]bool, scope model.Scope, f engine.Flow) bool {
	if scopeKind(scope) == "network" && lower(f.Network) == lower(*scope.Network) {
		return true
	}
	return f.Device != "" && devices[lower(f.Device)]
}

func idSet(ds []engine.DeviceState) map[string]bool {
	out := map[string]bool{}
	for _, d := range ds {
		out[lower(d.ID)] = true
	}
	return out
}

func ids(ds []engine.DeviceState) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, lower(d.ID))
	}
	sort.Strings(out)
	return out
}
