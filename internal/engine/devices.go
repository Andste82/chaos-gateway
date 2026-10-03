package engine

import (
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Device events (the spec's EventType).
const (
	EventDeviceDiscovered      = "device_discovered"
	EventDeviceOnline          = "device_online"
	EventDeviceOffline         = "device_offline"
	EventDeviceIdentityChanged = "device_identity_changed"
	EventDHCPLease             = "dhcp_lease"
)

// discoveredNamespace derives the UUID of a discovered device from its MAC address (or address), so
// it is the same after a restart and adopting it keeps its overlays (plan §2.3).
var discoveredNamespace = uuid.MustParse("6b9a1d2e-4f3c-5a77-9c0e-0d2f6a1b3c45")

// DeviceID is the UUID of the discovered device with the given MAC address.
func DeviceID(mac string) string {
	return uuid.NewSHA1(discoveredNamespace, []byte("mac/"+strings.ToLower(mac))).String()
}

func ipDeviceID(ip netip.Addr) string {
	return uuid.NewSHA1(discoveredNamespace, []byte("ip/"+ip.String())).String()
}

// DeviceState is what the gateway knows about a device right now: configured or discovered.
type DeviceState struct {
	ID      string
	Name    string
	Origin  model.DeviceOrigin
	Network string
	MACs    []string
	// Addresses are the addresses that belong to the device now (the identity).
	Addresses []netip.Addr
	Online    bool
	LastSeen  time.Time
	// Lease is the device's DHCP lease, when it has one.
	Lease   *model.DhcpLease
	Sources []string
}

// observation is one reading of everything the gateway sees.
type observation struct {
	At        time.Time
	Leases    []model.DhcpLease
	Neighbors []domain.Neighbor
	// Active are the source addresses that have connections (conntrack).
	Active map[netip.Addr]bool
	// Peers are the WireGuard peers by client id.
	Peers map[string]PeerStatus
	// UnknownSources are source addresses of connections that no neighbor, lease or configuration explains
	// (hosts behind a router, in a client network): they become discovered devices by address.
	UnknownSources []netip.Addr
}

// trackerEvent is what a step reports.
type trackerEvent struct {
	Type string
	Data map[string]any
}

// forgetAfter is how long a discovered device that is not seen stays known.
const forgetAfter = 24 * time.Hour

// maxDiscovered bounds the registry.
const maxDiscovered = 1024

// tracker keeps the devices the gateway has seen and works out identity and online state (plan
// §2.3). It is a pure state machine: observations in, state and events out. The state owner holds
// one.
type tracker struct {
	registry map[string]*domain.DiscoveredDevice // by device id
	prev     *domain.Identity
	devices  map[string]*DeviceState
	started  bool
}

func newTracker() *tracker {
	return &tracker{registry: map[string]*domain.DiscoveredDevice{}, devices: map[string]*DeviceState{}}
}

func (t *tracker) see(id string, mac string, ip netip.Addr, network, source string, at time.Time) {
	d := t.registry[id]
	if d == nil {
		if len(t.registry) >= maxDiscovered {
			return
		}
		d = &domain.DiscoveredDevice{ID: id}
		t.registry[id] = d
	}
	d.LastSeen = at
	if network != "" {
		d.Network = network
	}
	if mac != "" && !contains(d.MACs, mac) {
		d.MACs = append(d.MACs, mac)
	}
	if ip.IsValid() && !containsAddr(d.IPs, ip) {
		d.IPs = append(d.IPs, ip)
	}
	if !contains(d.Sources, source) {
		d.Sources = append(d.Sources, source)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func containsAddr(l []netip.Addr, a netip.Addr) bool {
	for _, x := range l {
		if x == a {
			return true
		}
	}
	return false
}

// step takes an observation and returns the identity, the state of all devices and the events that
// the change from the last step causes. cfg is the committed configuration (normalized).
func (t *tracker) step(cfg *model.Configuration, obs observation) (domain.Identity, []DeviceState, []trackerEvent) {
	// the current sightings replace the addresses of the registry; the MACs and ids stay
	for _, d := range t.registry {
		d.IPs = nil
		d.Sources = nil
	}
	seenNow := map[string]bool{}
	// a device that has a fresh entry is known by that one: its stale entries (the address it had
	// before) say nothing any more, unless that address still carries connections
	fresh := map[string]bool{}
	for _, n := range obs.Neighbors {
		if n.MAC != "" && !n.Stale {
			fresh[strings.ToLower(n.MAC)] = true
		}
	}
	for _, n := range obs.Neighbors {
		if n.MAC == "" {
			continue
		}
		mac := strings.ToLower(n.MAC)
		if n.Stale && fresh[mac] && !obs.Active[n.IP] {
			continue
		}
		id := DeviceID(mac)
		t.see(id, mac, n.IP, n.Network, "neighbor", obs.At)
		delete(t.registry, ipDeviceID(n.IP)) // a host known by address only is this device
		seenNow[id] = !n.Stale || seenNow[id]
	}
	for _, l := range obs.Leases {
		if l.State != nil && *l.State != "active" {
			continue
		}
		mac := strings.ToLower(l.Mac)
		ip, err := netip.ParseAddr(l.Ip)
		if err != nil {
			continue
		}
		t.see(DeviceID(mac), mac, ip, l.Network.String(), "dhcp", obs.At)
		delete(t.registry, ipDeviceID(ip))
	}
	for _, ip := range obs.UnknownSources {
		t.see(ipDeviceID(ip), "", ip, "", "conntrack", obs.At)
		seenNow[ipDeviceID(ip)] = true
	}
	// the devices behind WireGuard clients are the clients themselves (configured), not discovered
	for id, d := range t.registry {
		if obs.At.Sub(d.LastSeen) > forgetAfter {
			delete(t.registry, id)
		}
	}
	disc := make([]domain.DiscoveredDevice, 0, len(t.registry))
	for _, d := range t.registry {
		cp := *d
		sort.Strings(cp.MACs)
		sort.Slice(cp.IPs, func(i, j int) bool { return cp.IPs[i].Less(cp.IPs[j]) })
		sort.Strings(cp.Sources)
		disc = append(disc, cp)
	}
	sort.Slice(disc, func(i, j int) bool { return disc[i].ID < disc[j].ID })

	peers := map[string]model.WireGuardPeerStatus{}
	for id, p := range obs.Peers {
		ps := model.WireGuardPeerStatus{Online: p.Online}
		peers[id] = ps
	}
	o := domain.Observed{Leases: obs.Leases, Neighbors: obs.Neighbors, Peers: peers, Discovered: disc, ActiveSources: obs.Active}
	id := domain.ResolveIdentity(cfg, o, t.prev)
	idx, _ := domain.BuildIndex(cfg)

	// ---- device states
	next := map[string]*DeviceState{}
	leaseOf := map[string]model.DhcpLease{}
	for _, l := range obs.Leases {
		if l.State == nil || *l.State == "active" {
			leaseOf[strings.ToLower(l.Mac)] = l
		}
	}
	online := func(addrs []netip.Addr, macs []string, peer *PeerStatus) bool {
		if peer != nil && peer.Online {
			return true
		}
		for _, a := range addrs {
			if obs.Active[a] {
				return true
			}
			for _, n := range obs.Neighbors {
				// a confirmed entry: the kernel keeps STALE entries for ever on a quiet network, they
				// do not say that the device is there now
				if n.IP == a && !n.Stale {
					return true
				}
			}
		}
		_ = macs
		return false
	}
	for _, did := range sortedDeviceIDs(idx) {
		d := idx.Devices[did]
		st := &DeviceState{ID: did, Name: d.Name, Origin: model.DeviceOrigin(d.Origin), Network: d.Network, Addresses: id.Addresses[did]}
		if d.Device != nil && d.Device.Identifiers != nil && d.Device.Identifiers.Macs != nil {
			for _, m := range *d.Device.Identifiers.Macs {
				st.MACs = append(st.MACs, strings.ToLower(m))
			}
		}
		var peer *PeerStatus
		if p, ok := obs.Peers[did]; ok {
			peer = &p
		}
		st.Online = online(st.Addresses, st.MACs, peer)
		for _, m := range st.MACs {
			if l, ok := leaseOf[m]; ok {
				lc := l
				st.Lease = &lc
			}
		}
		next[did] = st
	}
	for _, d := range id.Discovered {
		st := &DeviceState{ID: d.ID, Name: domain.DiscoveredName(firstOr(d.MACs, d.ID)), Origin: model.DeviceOriginDiscovered, Network: d.Network,
			MACs: d.MACs, Addresses: id.Addresses[d.ID], Sources: d.Sources}
		if len(d.MACs) == 0 && len(d.IPs) > 0 {
			st.Name = "dev-" + strings.ReplaceAll(d.IPs[0].String(), ".", "-")
		}
		st.Online = online(st.Addresses, st.MACs, nil) || seenNow[d.ID]
		for _, m := range st.MACs {
			if l, ok := leaseOf[m]; ok {
				lc := l
				st.Lease = &lc
			}
		}
		next[d.ID] = st
	}
	for did, st := range next {
		if p := t.devices[did]; p != nil {
			st.LastSeen = p.LastSeen
		}
		if st.Online {
			st.LastSeen = obs.At
		}
	}

	// ---- events: what changed since the last step. The first step only learns.
	var events []trackerEvent
	if t.started {
		for _, did := range sortedStateIDs(next) {
			st, p := next[did], t.devices[did]
			if p == nil {
				if st.Origin == model.DeviceOriginDiscovered {
					events = append(events, trackerEvent{EventDeviceDiscovered, deviceData(st)})
				}
				continue
			}
			if !sameAddrs(p.Addresses, st.Addresses) {
				d := deviceData(st)
				d["old_addresses"] = addrStrings(p.Addresses)
				d["new_addresses"] = addrStrings(st.Addresses)
				events = append(events, trackerEvent{EventDeviceIdentityChanged, d})
			}
			switch {
			case !p.Online && st.Online:
				events = append(events, trackerEvent{EventDeviceOnline, deviceData(st)})
			case p.Online && !st.Online:
				events = append(events, trackerEvent{EventDeviceOffline, deviceData(st)})
			}
		}
	}
	t.started = true
	t.prev = &id
	t.devices = next

	states := make([]DeviceState, 0, len(next))
	for _, did := range sortedStateIDs(next) {
		states = append(states, *next[did])
	}
	return id, states, events
}

func deviceData(st *DeviceState) map[string]any {
	d := map[string]any{"device": st.ID, "name": st.Name, "origin": string(st.Origin), "addresses": addrStrings(st.Addresses)}
	if st.Network != "" {
		d["network"] = st.Network
	}
	return d
}

func addrStrings(a []netip.Addr) []string {
	out := make([]string, len(a))
	for i, x := range a {
		out[i] = x.String()
	}
	return out
}

func sameAddrs(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstOr(l []string, def string) string {
	if len(l) > 0 {
		return l[0]
	}
	return def
}

func sortedDeviceIDs(idx *domain.Index) []string {
	out := make([]string, 0, len(idx.Devices))
	for id := range idx.Devices {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sortedStateIDs(m map[string]*DeviceState) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
