package domain

import (
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// Sources returns the sources of traffic the compiler builds tables for, from the identity of the
// devices (ResolveIdentity: configuration plus observed state):
//
//   - every device that has addresses, once per group of addresses in the same networks (a device
//     is normally in one network), and once per range that identifies a device (hosts behind
//     another router);
//   - the stretches of addresses that no device owns: every prefix of a network (its subnet, the
//     networks behind a hub's clients, a link's routes) and every remote network a fault names,
//     cut at the boundaries between them. Their traffic is "addresses in a network that are not
//     known as devices yet": a fault of a network or global scope reaches them, and they share
//     one queue per scope until discovery adds them (plan §2.4). The tables of such a stretch hold
//     what the device tables hold without the device and group scopes; a lookup at the device's
//     own address finds the device's table first.
//
// The order is stable: devices by UUID, then the stretches by address.
func (w *World) Sources(id Identity) []Source {
	discoveredNet := map[string]string{}
	for _, d := range id.Discovered {
		discoveredNet[d.ID] = d.Network
	}
	networkOf := func(dev string) string {
		if d, ok := w.Index.Devices[dev]; ok {
			return d.Network
		}
		return discoveredNet[dev]
	}

	var out []Source
	devices := map[string]bool{}
	for dev := range id.Addresses {
		devices[dev] = true
	}
	for _, r := range id.Ranges {
		devices[r.Device] = true
	}
	for _, dev := range sortedKeys(devices) {
		net := networkOf(dev)
		groups := map[string]*Source{}
		var order []string
		for _, ip := range id.Addresses[dev] {
			sub := Subject{Device: dev, Network: net, IP: ip}
			var nets []string
			for n := range w.subjectNetworks(sub) {
				nets = append(nets, n)
			}
			sort.Strings(nets)
			key := strings.Join(nets, ",")
			g := groups[key]
			if g == nil {
				g = &Source{Subject: sub, Device: dev}
				groups[key] = g
				order = append(order, key)
			}
			g.Addrs = append(g.Addrs, ip)
		}
		for _, key := range order {
			out = append(out, *groups[key])
		}
		for _, r := range id.Ranges {
			if r.Device == dev {
				out = append(out, Source{
					Subject: Subject{Device: dev, Network: net, IP: r.Prefix.Masked().Addr()},
					Device:  dev,
					Ranges:  []IPRange{prefixSpan(r.Prefix).ipRange()},
				})
			}
		}
	}

	for _, s := range w.unownedStretches() {
		out = append(out, Source{Subject: Subject{IP: numAddr(s.lo)}, Ranges: []IPRange{s.ipRange()}})
	}
	return out
}

// unownedStretches cuts the addresses of the networks and of the remote networks that faults name
// at their boundaries.
func (w *World) unownedStretches() []span {
	var cover []span
	for _, id := range sortedKeys(w.prefixes) {
		for _, p := range w.prefixes[id] {
			cover = append(cover, prefixSpan(p))
		}
	}
	for _, c := range w.candidates() {
		if c.Scope.RemoteNetwork != nil {
			cover = append(cover, w.remoteSpans(*c.Scope.RemoteNetwork)...)
		}
	}
	return elementary(cover, addrSpace)
}

// remoteSpans returns the addresses of a remote network, like inRemoteNetwork's test.
func (w *World) remoteSpans(r model.RemoteNetworkRef) []span {
	var out []span
	add := func(s string) {
		if p, ok := parsePrefix(s); ok {
			out = append(out, prefixSpan(p))
		}
	}
	switch {
	case r.Client != nil:
		if d, ok := w.Index.Devices[lower(*r.Client)]; ok && d.Client != nil {
			for _, cn := range deref(d.Client.ClientNetworks) {
				add(cn)
			}
		}
	case r.Link != nil:
		if n, ok := w.Index.Networks[lower(*r.Link)]; ok && n.IsLink() {
			for _, route := range deref(n.WG.Routes) {
				add(route)
			}
		}
	case r.Cidr != nil:
		add(*r.Cidr)
	}
	return out
}
