package domain

import (
	"net/netip"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file expands the selectors of an access rule into the address sets the compiler writes
// into nftables (plan §2.2, M9). The expansion is the compiler's counterpart of scopeMatches and
// destinationMatches: for a source address the two agree, which the compiler's tests check
// against ResolveAccess.

// CollapsePrefixes returns the prefixes sorted, masked and without duplicates or prefixes that
// another one contains, so the result is disjoint (an nftables interval set refuses overlapping
// elements). Invalid prefixes are dropped.
func CollapsePrefixes(in []netip.Prefix) []netip.Prefix {
	ps := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		if p.IsValid() && p.Addr().Is4() {
			ps = append(ps, p.Masked())
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if c := ps[i].Addr().Compare(ps[j].Addr()); c != 0 {
			return c < 0
		}
		return ps[i].Bits() < ps[j].Bits() // the wider prefix first
	})
	var out []netip.Prefix
	for _, p := range ps {
		if n := len(out); n > 0 && out[n-1].Contains(p.Addr()) && out[n-1].Bits() <= p.Bits() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// deviceAddresses returns the addresses and ranges a device owns right now.
func deviceAddresses(id Identity, dev string) []netip.Prefix {
	var out []netip.Prefix
	for _, a := range id.Addresses[dev] {
		if a.Is4() {
			out = append(out, netip.PrefixFrom(a, 32))
		}
	}
	for _, r := range id.Ranges {
		if r.Device == dev {
			out = append(out, r.Prefix)
		}
	}
	return out
}

// ScopePrefixes returns the source addresses a scope covers right now, as disjoint prefixes,
// from the identity of the devices (configuration plus observed state). A device or group covers
// the addresses of its devices, a network its prefixes (the subnet, the networks behind a hub's
// clients, a link's routes) and the addresses of the devices that belong to it, a remote network
// its prefixes. The global scope has no list: every source of traffic matches, which the compiler
// limits to the test, WireGuard and remote networks (all is true).
func (w *World) ScopePrefixes(sc model.Scope, id Identity) (prefixes []netip.Prefix, all bool) {
	var out []netip.Prefix
	switch scopeKind(&sc) {
	case "device":
		out = deviceAddresses(id, lower(*sc.Device))
	case "group":
		for _, m := range deref(deref(w.Config.Groups)[lower(*sc.Group)].Members) {
			out = append(out, deviceAddresses(id, lower(m))...)
		}
	case "network":
		net := lower(*sc.Network)
		out = append(out, w.prefixes[net]...)
		// a device that belongs to the network without an address inside its prefixes
		member := map[string]bool{}
		for dev, d := range w.Index.Devices {
			if d.Network == net {
				member[dev] = true
			}
		}
		for _, d := range id.Discovered {
			if d.Network == net {
				member[d.ID] = true
			}
		}
		for dev := range member {
			out = append(out, deviceAddresses(id, dev)...)
		}
	case "remote_network":
		for _, s := range w.remoteSpans(*sc.RemoteNetwork) {
			out = append(out, spansToPrefixes(s)...)
		}
	case "global":
		return nil, true
	}
	return CollapsePrefixes(out), false
}

// NetworkPrefixes returns the prefixes of a network (the destination "network" of a rule).
func (w *World) NetworkPrefixes(network string) []netip.Prefix {
	return CollapsePrefixes(w.prefixes[lower(network)])
}

// NonUplinkPrefixes returns what is not routed via the uplink: the prefixes of every network and of
// the management network. A rule with the destination uplink matches the rest (viaUplink).
func (w *World) NonUplinkPrefixes() []netip.Prefix {
	var out []netip.Prefix
	for _, id := range sortedKeys(w.prefixes) {
		out = append(out, w.prefixes[id]...)
	}
	out = append(out, w.mgmt...)
	return CollapsePrefixes(out)
}
