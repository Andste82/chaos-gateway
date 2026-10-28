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
	// Online is whether the device has given evidence of being there within offlineAfter; LastSeen is
	// when it last did (plan §2.3).
	Online   bool
	LastSeen time.Time
	// Lease is the device's DHCP lease, when it has one.
	Lease   *model.DhcpLease
	Sources []string
	// TrafficKnown is set once the traffic below is known: conntrack has been read twice, and the last
	// read did not fail.
	TrafficKnown bool
	// FlowsActive is the number of tracked connections where the device is a party, as of the last
	// conntrack read.
	FlowsActive int
	// UploadBps and DownloadBps are byte rates over the last rateWindow, from the device's own point of
	// view (plan §2.3).
	UploadBps   int64
	DownloadBps int64
}

// observation is one reading of everything the gateway sees.
type observation struct {
	At        time.Time
	Leases    []model.DhcpLease
	Neighbors []domain.Neighbor
	// Probes are the MAC and the addresses of the probes' interfaces, by probe id (plan §2.12): the executor reads them in the
	// probes' namespaces. A probe that is not made yet (or whose read failed) is not in it.
	Probes map[string]domain.ProbeObservation
	// Active are the source addresses that have connections (conntrack). It is carried over when
	// conntrack was not read; it decides identity, not online state.
	Active map[netip.Addr]bool
	// Peers are the WireGuard peers by client id.
	Peers map[string]PeerStatus
	// UnknownSources are source addresses of connections that no neighbor, lease or configuration explains
	// (hosts behind a router, in a client network): they become discovered devices by address.
	UnknownSources []netip.Addr
	// ConntrackRead is set when this observation read conntrack; Flows are then its connections. A
	// ticked observation that reused the last read, or whose read failed, has neither.
	ConntrackRead bool
	Flows         []FlowCounters
	// Ended are the final counters of the connections the conntrack watch reported destroyed since
	// the previous read, oldest first (issue #91); set only with ConntrackRead.
	Ended []FlowCounters
	// ObserveError is set when reading the connections failed; the snapshot carries it so the API and
	// the health check can tell a stale reading from a fresh one with nothing in it.
	ObserveError string
}

// FlowCounters is one tracked connection's cumulative counters: Up is what Src sent, Down what it
// received. Dst is the other party inside the gateway's view: the original destination, or the device a
// port forward translated the connection to.
type FlowCounters struct {
	Key      string
	Src, Dst netip.Addr
	Up, Down Traffic
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

// offlineAfter is how long a device stays online after its last evidence unless Config.OfflineAfter
// says otherwise (plan §2.3): one quiet observation, a neighbor entry going through DELAY and PROBE or
// a connection that ends does not make it offline.
const offlineAfter = 60 * time.Second

// rateWindow is the span the byte rates are averaged over: about one conntrack read while the watch
// streams (conntrackFallback), so the rate is that of the last read, not of the last 100 ms.
const rateWindow = 10 * time.Second

// tracker keeps the devices the gateway has seen and works out identity and online state (plan
// §2.3). It is a pure state machine: observations in, state and events out. The state owner holds
// one.
type tracker struct {
	registry map[string]*domain.DiscoveredDevice // by device id
	prev     *domain.Identity
	devices  map[string]*DeviceState
	started  bool
	// offlineAfter is how long a device stays online after its last evidence.
	offlineAfter time.Duration
	// flows are the connections' counters as of the last conntrack read (lastRead), to turn counters
	// that are cumulative for the life of a connection into what moved since.
	flows    map[string]FlowCounters
	lastRead time.Time
	// gone are the connections the last read no longer saw and no destroy event had reported, with
	// their counters as of the read before it: the watch can report a destroy only after the read
	// that already missed the connection, and what that read counted must not count again (issue
	// #91). They are kept for one read.
	gone map[string]FlowCounters
	// traffic holds each device's byte deltas of the reads within rateWindow.
	traffic map[string][]trafficSample
	// leases is the expiry of each MAC's active lease as last seen: a new or renewed lease is evidence.
	leases map[string]time.Time
	// flowsActive is the number of tracked connections that have a device as a party, each counted once, and flowsKnown
	// whether it is known (the same conditions as DeviceState.TrafficKnown).
	flowsActive int
	flowsKnown  bool
}

// trafficSample is what one device sent and received between two conntrack reads.
type trafficSample struct {
	from, to         time.Time
	upload, download int64
}

// addrDelta is one address's share of a conntrack read.
type addrDelta struct {
	upload, download int64
	flows            int
	// sent is set when the address sent something since the previous read, or, on the first read,
	// started a connection that is still tracked.
	sent bool
}

func newTracker() *tracker {
	return &tracker{registry: map[string]*domain.DiscoveredDevice{}, devices: map[string]*DeviceState{}, offlineAfter: offlineAfter, flows: map[string]FlowCounters{},
		traffic: map[string][]trafficSample{}, leases: map[string]time.Time{}}
}

// discovered returns every device the registry holds, whatever configuration covers it, by id: the
// input of domain.ResolveIdentity, which leaves out the ones a configuration covers.
func (t *tracker) discovered() []domain.DiscoveredDevice {
	disc := make([]domain.DiscoveredDevice, 0, len(t.registry))
	for _, d := range t.registry {
		cp := *d
		sort.Strings(cp.MACs)
		sort.Slice(cp.IPs, func(i, j int) bool { return cp.IPs[i].Less(cp.IPs[j]) })
		sort.Strings(cp.Sources)
		disc = append(disc, cp)
	}
	sort.Slice(disc, func(i, j int) bool { return disc[i].ID < disc[j].ID })
	return disc
}

// readFlows turns a conntrack read into what moved per address since the previous read. Each
// connection counts by itself: one that is new since the previous read brings all its counters, so a
// long connection that ends does not hide the traffic of the others. One that ended since the
// previous read is gone from this one; the final counters of its destroy event (obs.Ended) bring what
// it moved after the previous read, or all of it when it started after that read (issue #91). The
// first read only learns the counters.
func (t *tracker) readFlows(obs observation) map[netip.Addr]*addrDelta {
	out := map[netip.Addr]*addrDelta{}
	get := func(a netip.Addr) *addrDelta {
		d := out[a]
		if d == nil {
			d = &addrDelta{}
			out[a] = d
		}
		return d
	}
	first := t.lastRead.IsZero()
	// since returns what f moved since p, the counters it had when it was last read, if known. Smaller
	// counters under a known key are a new connection with the same tuple: all of it is new.
	since := func(f, p FlowCounters, known bool) (up, down Traffic) {
		up, down = f.Up, f.Down
		switch {
		case known && p.Up.Bytes <= up.Bytes && p.Down.Bytes <= down.Bytes && p.Up.Packets <= up.Packets && p.Down.Packets <= down.Packets:
			up = Traffic{up.Packets - p.Up.Packets, up.Bytes - p.Up.Bytes}
			down = Traffic{down.Packets - p.Down.Packets, down.Bytes - p.Down.Bytes}
		case first:
			up, down = Traffic{}, Traffic{}
		}
		return up, down
	}
	add := func(f FlowCounters, up, down Traffic, active bool) *addrDelta {
		n := 0
		if active {
			n = 1
		}
		s := get(f.Src)
		s.upload += up.Bytes
		s.download += down.Bytes
		s.flows += n
		s.sent = s.sent || up.Packets > 0
		if f.Dst.IsValid() {
			d := get(f.Dst)
			d.upload += down.Bytes
			d.download += up.Bytes
			d.flows += n
			d.sent = d.sent || down.Packets > 0
		}
		return s
	}
	if !first {
		for _, f := range obs.Ended {
			// a destroy reported only after the read that already missed the connection finds its
			// counters among the gone ones
			p, known := t.flows[f.Key]
			if !known {
				p, known = t.gone[f.Key]
			}
			up, down := since(f, p, known)
			add(f, up, down, false)
			// those counters are used up: a connection with the same tuple in this read, or a later
			// destroy of one, is a new connection and brings all of its own
			delete(t.flows, f.Key)
			delete(t.gone, f.Key)
		}
	}
	next := make(map[string]FlowCounters, len(obs.Flows))
	for _, f := range obs.Flows {
		next[f.Key] = f
		p, known := t.flows[f.Key]
		up, down := since(f, p, known)
		s := add(f, up, down, true)
		s.sent = s.sent || (first && !known)
	}
	gone := map[string]FlowCounters{}
	for k, f := range t.flows {
		if _, ok := next[k]; !ok {
			gone[k] = f
		}
	}
	t.flows, t.gone = next, gone
	return out
}

// applyTraffic fills st's flow count and byte rates. A read adds the device's deltas to its window;
// an observation without a read keeps what the last one found, and a failed read makes it unknown.
func (t *tracker) applyTraffic(st *DeviceState, obs observation, deltas map[netip.Addr]*addrDelta, prev *DeviceState) {
	if !obs.ConntrackRead {
		if obs.ObserveError == "" && prev != nil {
			st.TrafficKnown, st.FlowsActive, st.UploadBps, st.DownloadBps = prev.TrafficKnown, prev.FlowsActive, prev.UploadBps, prev.DownloadBps
		}
		return
	}
	if t.lastRead.IsZero() {
		return // the first read: nothing to rate against yet
	}
	sample := trafficSample{from: t.lastRead, to: obs.At}
	for _, a := range st.Addresses {
		if d := deltas[a]; d != nil {
			sample.upload += d.upload
			sample.download += d.download
			st.FlowsActive += d.flows
		}
	}
	window := append(t.traffic[st.ID], sample)
	for len(window) > 1 && !window[0].to.After(obs.At.Add(-rateWindow)) {
		window = window[1:]
	}
	t.traffic[st.ID] = window
	var up, down int64
	for _, s := range window {
		up += s.upload
		down += s.download
	}
	if dt := obs.At.Sub(window[0].from).Seconds(); dt > 0 {
		st.UploadBps = int64(float64(up) / dt)
		st.DownloadBps = int64(float64(down) / dt)
	}
	st.TrafficKnown = true
}

// countFlows counts the connections of the last conntrack read that have a device as a party. A connection between two devices
// is one connection, whereas it is a flow of each of the devices (DeviceState.FlowsActive), so the sum over the devices is not
// the total. An observation without a read keeps the count, and a failed one makes it unknown.
func (t *tracker) countFlows(obs observation, id domain.Identity) {
	switch {
	case !obs.ConntrackRead:
		if obs.ObserveError != "" {
			t.flowsKnown = false
		}
	case t.lastRead.IsZero():
		// the first read: the devices' traffic is not known yet either
	default:
		seen := make(map[string]bool, len(obs.Flows))
		n := 0
		for _, f := range obs.Flows {
			if seen[f.Key] {
				continue
			}
			seen[f.Key] = true
			_, src := id.Owner[f.Src]
			_, dst := id.Owner[f.Dst]
			if src || dst {
				n++
			}
		}
		t.flowsActive, t.flowsKnown = n, true
	}
}

// leaseNews returns the MACs whose active lease is new or renewed since the last step: a device that
// asks the DHCP server for an address is there. The leases of the first step are not news.
func (t *tracker) leaseNews(obs observation, first bool) map[string]bool {
	news := map[string]bool{}
	for _, l := range obs.Leases {
		if l.State != nil && *l.State != "active" {
			continue
		}
		mac := strings.ToLower(l.Mac)
		if was, ok := t.leases[mac]; !first && (!ok || !was.Equal(l.ExpiresAt)) {
			news[mac] = true
		}
		t.leases[mac] = l.ExpiresAt
	}
	for mac, exp := range t.leases {
		if obs.At.Sub(exp) > forgetAfter {
			delete(t.leases, mac)
		}
	}
	return news
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

// originSources reports where a configured, WireGuard client or probe device's identity comes from
// (plan §2.3, spec DeviceObserved.sources): its origin always gives one, and a matching DHCP lease
// or a fresh neighbor sighting adds another, the same as a discovered device's.
func originSources(origin model.DeviceOrigin, macs []string, addrs []netip.Addr, leaseOf map[string]model.DhcpLease, neighbors []domain.Neighbor) []string {
	var out []string
	switch origin {
	case model.DeviceOriginConfigured:
		out = append(out, "config")
	case model.DeviceOriginWireguardClient:
		out = append(out, "wireguard")
	case model.DeviceOriginProbe:
		out = append(out, "probe")
	}
	for _, m := range macs {
		if _, ok := leaseOf[m]; ok {
			out = append(out, "dhcp")
			break
		}
	}
	for _, a := range addrs {
		found := false
		for _, n := range neighbors {
			if n.IP == a && !n.Stale {
				out = append(out, "neighbor")
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	return out
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
	}
	// the devices behind WireGuard clients are the clients themselves (configured), not discovered
	for id, d := range t.registry {
		if obs.At.Sub(d.LastSeen) > forgetAfter {
			delete(t.registry, id)
		}
	}
	disc := t.discovered()

	peers := map[string]model.WireGuardPeerStatus{}
	for id, p := range obs.Peers {
		ps := model.WireGuardPeerStatus{Online: p.Online}
		peers[id] = ps
	}
	o := domain.Observed{Leases: obs.Leases, Neighbors: obs.Neighbors, Probes: obs.Probes, Peers: peers, Discovered: disc, ActiveSources: obs.Active}
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
	var deltas map[netip.Addr]*addrDelta
	if obs.ConntrackRead {
		deltas = t.readFlows(obs)
	}
	leaseNews := t.leaseNews(obs, !t.started)
	// evidence that the device is there now (plan §2.3). Not the mere existence of a connection: an
	// established TCP entry outlives an unplugged device by days.
	evidence := func(addrs []netip.Addr, macs []string, peer *PeerStatus) bool {
		if peer != nil && peer.Online {
			return true
		}
		for _, m := range macs {
			if leaseNews[m] {
				return true
			}
		}
		for _, a := range addrs {
			if d := deltas[a]; d != nil && d.sent {
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
		return false
	}
	seen := map[string]bool{}
	for _, did := range sortedDeviceIDs(idx) {
		d := idx.Devices[did]
		st := &DeviceState{ID: did, Name: d.Name, Origin: model.DeviceOrigin(d.Origin), Network: d.Network, Addresses: id.Addresses[did]}
		if d.Device != nil && d.Device.Identifiers != nil && d.Device.Identifiers.Macs != nil {
			for _, m := range *d.Device.Identifiers.Macs {
				st.MACs = append(st.MACs, strings.ToLower(m))
			}
		}
		if p, ok := obs.Probes[did]; ok && p.MAC != "" {
			st.MACs = append(st.MACs, strings.ToLower(p.MAC))
		}
		var peer *PeerStatus
		if p, ok := obs.Peers[did]; ok {
			peer = &p
		}
		seen[did] = evidence(st.Addresses, st.MACs, peer)
		for _, m := range st.MACs {
			if l, ok := leaseOf[m]; ok {
				lc := l
				st.Lease = &lc
			}
		}
		st.Sources = originSources(st.Origin, st.MACs, st.Addresses, leaseOf, obs.Neighbors)
		next[did] = st
	}
	for _, d := range id.Discovered {
		st := &DeviceState{ID: d.ID, Name: domain.DiscoveredName(firstOr(d.MACs, d.ID)), Origin: model.DeviceOriginDiscovered, Network: d.Network,
			MACs: d.MACs, Addresses: id.Addresses[d.ID], Sources: d.Sources}
		if len(d.MACs) == 0 && len(d.IPs) > 0 {
			st.Name = "dev-" + strings.ReplaceAll(d.IPs[0].String(), ".", "-")
		}
		seen[d.ID] = evidence(st.Addresses, st.MACs, nil) || seenNow[d.ID]
		for _, m := range st.MACs {
			if l, ok := leaseOf[m]; ok {
				lc := l
				st.Lease = &lc
			}
		}
		next[d.ID] = st
	}
	// the network of a device that does not declare one: that of its DHCP lease, else the one its address
	// lies in, so that every consumer (the scope of a run, a capture, a wait) agrees on where it is
	var world *domain.World
	for _, st := range next {
		if st.Network != "" {
			continue
		}
		if st.Lease != nil && st.Lease.Network != uuid.Nil {
			st.Network = strings.ToLower(st.Lease.Network.String())
			continue
		}
		if world == nil {
			world, _ = domain.NewWorld(cfg, nil)
		}
		if world == nil {
			break
		}
		for _, a := range st.Addresses {
			if n, ok := world.NetworkOf(a); ok {
				st.Network = n
				break
			}
		}
	}
	for did, st := range next {
		p := t.devices[did]
		if p != nil {
			st.LastSeen = p.LastSeen
		}
		if seen[did] {
			st.LastSeen = obs.At
		}
		st.Online = !st.LastSeen.IsZero() && obs.At.Sub(st.LastSeen) < t.offlineAfter
		t.applyTraffic(st, obs, deltas, p)
	}
	t.countFlows(obs, id)
	if obs.ConntrackRead {
		t.lastRead = obs.At
	}
	for did := range t.traffic {
		if _, ok := next[did]; !ok {
			delete(t.traffic, did)
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
