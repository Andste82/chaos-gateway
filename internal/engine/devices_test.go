package engine

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	tIoT  = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	tDev  = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	macA  = "02:00:00:00:00:aa"
	macB  = "02:00:00:00:00:bb"
	macCf = "02:00:00:00:00:31"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func trackerConfig(t *testing.T) *model.Configuration {
	t.Helper()
	cfg := &model.Configuration{}
	macs := []string{macCf}
	cfg.Devices = &map[string]model.Device{tDev: {Name: "esp32-42", Identifiers: &model.DeviceIdentifiers{Macs: &macs}}}
	return cfg
}

func neigh(ip, mac string) domain.Neighbor {
	return domain.Neighbor{IP: addr(ip), MAC: mac, Interface: "br-iot", Network: tIoT}
}

func types(ev []trackerEvent) string {
	var s []string
	for _, e := range ev {
		s = append(s, e.Type)
	}
	return strings.Join(s, ",")
}

func TestADeviceAppearsAsDiscoveredWithAStableId(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	// the first step only learns
	_, _, ev := tr.step(cfg, observation{At: t0})
	if len(ev) != 0 {
		t.Fatalf("%v", types(ev))
	}
	id, states, ev := tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.50", macA)}})
	if types(ev) != "device_discovered,device_online" && types(ev) != "device_discovered" {
		t.Fatalf("%v", types(ev))
	}
	var found *DeviceState
	for i := range states {
		if states[i].Origin == model.DeviceOriginDiscovered {
			found = &states[i]
		}
	}
	if found == nil || found.ID != DeviceID(macA) || found.Name != "dev-02-00-00-00-00-aa" || len(found.Addresses) != 1 || found.Addresses[0].String() != "10.10.0.50" || !found.Online || found.Network != tIoT {
		t.Fatalf("%+v", found)
	}
	if id.Owner[addr("10.10.0.50")] != DeviceID(macA) {
		t.Errorf("%v", id.Owner)
	}
	// the same device again: no event, same id
	_, _, ev = tr.step(cfg, observation{At: t0.Add(2 * time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.50", macA)}})
	if len(ev) != 0 {
		t.Errorf("%v", types(ev))
	}
	// another device, another id
	if DeviceID(macB) == DeviceID(macA) || DeviceID(strings.ToUpper(macA)) != DeviceID(macA) {
		t.Error("the id is not a function of the MAC")
	}
}

func TestAConfiguredDeviceIsNotDiscoveredAndGetsItsLease(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	state := model.DhcpLeaseState("active")
	lease := model.DhcpLease{Ip: "10.10.0.31", Mac: macCf, ExpiresAt: t0.Add(time.Hour), State: &state}
	_, states, ev := tr.step(cfg, observation{At: t0.Add(time.Second), Leases: []model.DhcpLease{lease}, Neighbors: []domain.Neighbor{neigh("10.10.0.31", macCf)}})
	for _, e := range ev {
		if e.Type == EventDeviceDiscovered {
			t.Errorf("a configured device is announced as discovered")
		}
	}
	var d *DeviceState
	for i := range states {
		if states[i].ID == tDev {
			d = &states[i]
		}
		if states[i].Origin == model.DeviceOriginDiscovered {
			t.Errorf("a discovered entry for a configured device: %+v", states[i])
		}
	}
	if d == nil || !d.Online || d.Lease == nil || d.Lease.Ip != "10.10.0.31" || len(d.Addresses) != 1 || d.Origin != model.DeviceOriginConfigured {
		t.Fatalf("%+v", d)
	}
	// M6a-15: a configured device reports where its identity comes from, same as a discovered one
	if !contains(d.Sources, "config") || !contains(d.Sources, "dhcp") || !contains(d.Sources, "neighbor") {
		t.Errorf("sources %v", d.Sources)
	}
	if !strings.Contains(types(ev), EventDeviceOnline) {
		t.Errorf("%v", types(ev))
	}
}

// P4-AUDIT-05: a configured device that declares no network is in the network of its lease, else in the one its address
// lies in, so a network scope, a capture and a wait agree with explain on where it is.
func TestAConfiguredDeviceWithoutANetworkIsInTheNetworkOfItsLeaseOrAddress(t *testing.T) {
	cfg := trackerConfig(t)
	lan := model.LanNetwork{Type: model.LanNetworkTypeLan, Name: "IoT", Address: "10.10.0.1/24", Interfaces: []model.InterfaceRef{}}
	var n model.Network
	if err := n.FromLanNetwork(lan); err != nil {
		t.Fatal(err)
	}
	cfg.Networks = &map[string]model.Network{tIoT: n}
	state := model.DhcpLeaseState("active")
	for name, obs := range map[string]observation{
		"lease":   {Leases: []model.DhcpLease{{Ip: "10.10.0.31", Mac: macCf, ExpiresAt: t0.Add(time.Hour), State: &state, Network: uuid.MustParse(tIoT)}}},
		"address": {Neighbors: []domain.Neighbor{{IP: addr("10.10.0.31"), MAC: macCf, Interface: "br-iot"}}},
	} {
		tr := newTracker()
		tr.step(cfg, observation{At: t0})
		obs.At = t0.Add(time.Second)
		_, states, _ := tr.step(cfg, obs)
		var d *DeviceState
		for i := range states {
			if states[i].ID == tDev {
				d = &states[i]
			}
		}
		if d == nil || d.Network != tIoT {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func TestAnAddressChangeIsAnIdentityEventAndFaultsStayOnTheDevice(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.31", macCf)}})
	// the device gets a new address (a forced new IP): the neighbor table shows the new one
	id, _, ev := tr.step(cfg, observation{At: t0.Add(2 * time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.77", macCf)}})
	var change *trackerEvent
	for i := range ev {
		if ev[i].Type == EventDeviceIdentityChanged {
			change = &ev[i]
		}
	}
	if change == nil || change.Data["device"] != tDev {
		t.Fatalf("%v", types(ev))
	}
	if old, _ := change.Data["old_addresses"].([]string); strings.Join(old, ",") != "10.10.0.31" {
		t.Errorf("%v", change.Data)
	}
	if nw, _ := change.Data["new_addresses"].([]string); strings.Join(nw, ",") != "10.10.0.77" {
		t.Errorf("%v", change.Data)
	}
	// the device is the same one, with the new address
	if id.Owner[addr("10.10.0.77")] != tDev {
		t.Errorf("%v", id.Owner)
	}
}

func TestTheOldAddressStaysWhileItHasConnections(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.31", macCf)}})
	id, _, _ := tr.step(cfg, observation{At: t0.Add(2 * time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.77", macCf)},
		Active: map[netip.Addr]bool{addr("10.10.0.31"): true}})
	if id.Owner[addr("10.10.0.31")] != tDev || id.Owner[addr("10.10.0.77")] != tDev {
		t.Errorf("both addresses belong to the device while the old one has connections: %v", id.Owner)
	}
	id, _, _ = tr.step(cfg, observation{At: t0.Add(3 * time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.77", macCf)}})
	if _, ok := id.Owner[addr("10.10.0.31")]; ok {
		t.Errorf("the old address is still mapped without connections: %v", id.Owner)
	}
}

func TestADeviceGoesOfflineWhenItsEntryDisappears(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.31", macCf)}})
	// issue #79: one observation without evidence is not offline yet (the address goes with the entry)
	_, states, ev := tr.step(cfg, observation{At: t0.Add(2 * time.Second)})
	if strings.Contains(types(ev), EventDeviceOffline) || !stateOf(states, tDev).Online {
		t.Fatalf("offline right after the entry went: %v %+v", types(ev), stateOf(states, tDev))
	}
	_, states, ev = tr.step(cfg, observation{At: t0.Add(time.Second + offlineAfter)})
	if !strings.Contains(types(ev), EventDeviceOffline) {
		t.Errorf("%v", types(ev))
	}
	if s := stateOf(states, tDev); s.Online || !s.LastSeen.Equal(t0.Add(time.Second)) {
		t.Errorf("%+v", s)
	}
}

func stateOf(states []DeviceState, id string) *DeviceState {
	for i := range states {
		if states[i].ID == id {
			return &states[i]
		}
	}
	return nil
}

func staleNeigh(ip, mac string) domain.Neighbor {
	n := neigh(ip, mac)
	n.Stale = true
	return n
}

// issue #79: a neighbor entry that goes STALE, DELAY and PROBE between two confirmations, as it does
// for a device that talks only through the gateway, does not flap the device; it goes offline once
// nothing confirms it for offlineAfter, and each transition is one event.
func TestANeighborEntryBetweenConfirmationsDoesNotFlap(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	// the lease keeps the address with the device while its entry is stale
	active := model.DhcpLeaseState("active")
	leases := []model.DhcpLease{{Ip: "10.10.0.31", Mac: macCf, ExpiresAt: t0.Add(time.Hour), State: &active}}
	tr.step(cfg, observation{At: t0, Leases: leases})
	var all []string
	for s := 1; s <= 120; s++ {
		n := staleNeigh("10.10.0.31", macCf)
		if s%40 == 1 { // confirmed every 40 s, STALE/DELAY/PROBE in between
			n = neigh("10.10.0.31", macCf)
		}
		_, states, ev := tr.step(cfg, observation{At: t0.Add(time.Duration(s) * time.Second), Neighbors: []domain.Neighbor{n}, Leases: leases})
		if types(ev) != "" {
			all = append(all, types(ev))
		}
		if !stateOf(states, tDev).Online {
			t.Fatalf("offline at %d s", s)
		}
	}
	if strings.Join(all, ";") != EventDeviceOnline {
		t.Errorf("events %v, want one device_online", all)
	}
	// the device is unplugged: the stale entry stays, nothing confirms it
	last := t0.Add(81 * time.Second)
	var offAt time.Duration
	for s := 121; s <= 200 && offAt == 0; s++ {
		at := t0.Add(time.Duration(s) * time.Second)
		_, _, ev := tr.step(cfg, observation{At: at, Neighbors: []domain.Neighbor{staleNeigh("10.10.0.31", macCf)}, Leases: leases})
		if strings.Contains(types(ev), EventDeviceOffline) {
			offAt = at.Sub(last)
		}
	}
	if offAt != offlineAfter {
		t.Errorf("offline %v after the last confirmation, want %v", offAt, offlineAfter)
	}
}

// issue #79: an established connection that outlives an unplugged device does not keep it online; a
// connection the device sends on does.
func TestAConnectionKeepsADeviceOnlineOnlyWhileItSends(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	ip := addr("10.10.0.31")
	active := map[netip.Addr]bool{ip: true}
	flow := func(up, down int64) []FlowCounters {
		return []FlowCounters{{Key: "tcp/a", Src: ip, Dst: addr("203.0.113.10"), Up: Traffic{up, up * 100}, Down: Traffic{down, down * 100}}}
	}
	tr.step(cfg, observation{At: t0, ConntrackRead: true, Active: active, Flows: flow(1, 1)})
	tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{staleNeigh("10.10.0.31", macCf)}, ConntrackRead: true, Active: active, Flows: flow(5, 5)})
	// from here on the device sends nothing; the server's retransmissions are its download
	for s := 2; s <= 70; s++ {
		_, states, _ := tr.step(cfg, observation{At: t0.Add(time.Duration(s) * time.Second), Neighbors: []domain.Neighbor{staleNeigh("10.10.0.31", macCf)},
			ConntrackRead: true, Active: active, Flows: flow(5, int64(5+s))})
		st := stateOf(states, tDev)
		if want := time.Duration(s)*time.Second < time.Second+offlineAfter; st.Online != want {
			t.Fatalf("at %d s online %v, want %v", s, st.Online, want)
		}
	}
	// it sends again: online at once
	_, states, ev := tr.step(cfg, observation{At: t0.Add(71 * time.Second), ConntrackRead: true, Active: active, Flows: flow(6, 80)})
	if !stateOf(states, tDev).Online || !strings.Contains(types(ev), EventDeviceOnline) {
		t.Errorf("%v %+v", types(ev), stateOf(states, tDev))
	}
}

// issue #79: a device that asks the DHCP server for a lease, or renews it, is there.
func TestANewOrRenewedLeaseIsEvidence(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	active := model.DhcpLeaseState("active")
	lease := func(exp time.Time) []model.DhcpLease {
		return []model.DhcpLease{{Ip: "10.10.0.31", Mac: macCf, ExpiresAt: exp, State: &active}}
	}
	_, states, _ := tr.step(cfg, observation{At: t0, Leases: lease(t0.Add(time.Hour))})
	if stateOf(states, tDev).Online {
		t.Fatal("a lease the gateway finds at start says nothing about now")
	}
	_, states, _ = tr.step(cfg, observation{At: t0.Add(time.Minute), Leases: lease(t0.Add(time.Hour))})
	if stateOf(states, tDev).Online {
		t.Fatal("an unchanged lease is no evidence")
	}
	_, states, ev := tr.step(cfg, observation{At: t0.Add(30 * time.Minute), Leases: lease(t0.Add(90 * time.Minute))})
	if !stateOf(states, tDev).Online || !strings.Contains(types(ev), EventDeviceOnline) {
		t.Errorf("a renewal: %v %+v", types(ev), stateOf(states, tDev))
	}
}

func TestWireGuardClientsAreOnlineWithTheirPeer(t *testing.T) {
	tr := newTracker()
	cfg := &model.Configuration{}
	hub := "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	client := "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	wg := model.WireGuardNetwork{Type: model.WireGuardNetworkTypeWireguard, Kind: model.Hub, Name: "lab-hub", Address: "10.99.0.1/24", ListenPort: 51820,
		Clients: &map[string]model.WireGuardClient{client: {Name: "rA", Address: "10.99.0.2"}}}
	var n model.Network
	if err := n.FromWireGuardNetwork(wg); err != nil {
		t.Fatal(err)
	}
	cfg.Networks = &map[string]model.Network{hub: n}
	tr.step(cfg, observation{At: t0})
	_, states, ev := tr.step(cfg, observation{At: t0.Add(time.Second), Peers: map[string]PeerStatus{client: {Online: true}}})
	if !strings.Contains(types(ev), EventDeviceOnline) {
		t.Errorf("%v", types(ev))
	}
	for _, s := range states {
		if s.ID == client && (!s.Online || s.Origin != model.DeviceOriginWireguardClient || len(s.Addresses) != 1 || s.Addresses[0].String() != "10.99.0.2") {
			t.Errorf("%+v", s)
		}
		// M6a-15
		if s.ID == client && !contains(s.Sources, "wireguard") {
			t.Errorf("sources %v", s.Sources)
		}
	}
}

// M6a-03 test: a device's active flow count and byte rates come from the conntrack connections the
// observation carries for its addresses; the rate needs two reads, since conntrack counters are
// cumulative for the life of a connection.
func TestDeviceTrafficRatesAndActiveFlowCount(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	ip, srv := addr("10.10.0.31"), addr("203.0.113.10")
	nb := []domain.Neighbor{neigh("10.10.0.31", macCf)}

	_, states, _ := tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: nb, ConntrackRead: true, Flows: []FlowCounters{
		{Key: "a", Src: ip, Dst: srv, Up: Traffic{10, 800}, Down: Traffic{5, 400}},
		{Key: "b", Src: ip, Dst: srv, Up: Traffic{1, 200}, Down: Traffic{1, 100}}}})
	if st := stateOf(states, tDev); st == nil || st.TrafficKnown {
		t.Fatalf("the first read has nothing to rate against: %+v", st)
	}

	// two seconds later "b" has ended and "c" is new: a's growth and all of c count, b's end does not
	// make anything negative
	_, states, _ = tr.step(cfg, observation{At: t0.Add(3 * time.Second), Neighbors: nb, ConntrackRead: true, Flows: []FlowCounters{
		{Key: "a", Src: ip, Dst: srv, Up: Traffic{30, 4800}, Down: Traffic{15, 2400}},
		{Key: "c", Src: srv, Dst: ip, Up: Traffic{2, 1000}, Down: Traffic{2, 200}}}})
	st := stateOf(states, tDev)
	// upload (4000 + 200) / 2 = 2100 B/s, download (2000 + 1000) / 2 = 1500 B/s
	if st == nil || !st.TrafficKnown || st.FlowsActive != 2 || st.UploadBps != 2100 || st.DownloadBps != 1500 {
		t.Fatalf("%+v", st)
	}
}

// issue #79: observations that do not read conntrack (the watch streams, or the read failed) neither
// reset the baseline nor spike the next read; a device that moves nothing has a known rate of 0.
func TestTrafficRatesSurviveObservationsWithoutAConntrackRead(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	ip, srv := addr("10.10.0.31"), addr("203.0.113.10")
	nb := []domain.Neighbor{neigh("10.10.0.31", macCf)}
	// a long connection that has moved 500 MB before the gateway started, then a steady 1 MB/s upload
	const mb = 1 << 20
	read := func(s int) observation {
		return observation{At: t0.Add(time.Duration(s) * time.Second), Neighbors: nb, ConntrackRead: true,
			Flows: []FlowCounters{{Key: "mqtt", Src: ip, Dst: srv, Up: Traffic{int64(s), 500*mb + int64(s)*mb}, Down: Traffic{int64(s), 1000}}}}
	}
	tr.step(cfg, read(0))
	for s := 1; s <= 60; s++ {
		obs := observation{At: t0.Add(time.Duration(s) * time.Second), Neighbors: nb}
		switch {
		case s%10 == 0:
			obs = read(s) // the 10 s fallback of the watch
		case s == 35:
			obs.ObserveError = "conntrack: netlink error"
		}
		_, states, _ := tr.step(cfg, obs)
		st := stateOf(states, tDev)
		if s < 10 {
			if st.TrafficKnown {
				t.Fatalf("at %d s a rate before the second read: %+v", s, st)
			}
			continue
		}
		if s >= 35 && s < 40 {
			if st.TrafficKnown {
				t.Fatalf("at %d s the rate is known after a failed read: %+v", s, st)
			}
			continue
		}
		if !st.TrafficKnown || st.UploadBps < mb*8/10 || st.UploadBps > mb*12/10 {
			t.Fatalf("at %d s upload %d B/s, want about 1 MB/s: %+v", s, st.UploadBps, st)
		}
	}

	// the transfer stops: the rate falls to a known 0
	tr.step(cfg, observation{At: t0.Add(70 * time.Second), Neighbors: nb, ConntrackRead: true,
		Flows: []FlowCounters{{Key: "mqtt", Src: ip, Dst: srv, Up: Traffic{60, 560 * mb}, Down: Traffic{60, 1000}}}})
	_, states, _ := tr.step(cfg, observation{At: t0.Add(80 * time.Second), Neighbors: nb, ConntrackRead: true,
		Flows: []FlowCounters{{Key: "mqtt", Src: ip, Dst: srv, Up: Traffic{60, 560 * mb}, Down: Traffic{60, 1000}}}})
	if st := stateOf(states, tDev); !st.TrafficKnown || st.UploadBps != 0 || st.DownloadBps != 0 || st.FlowsActive != 1 {
		t.Errorf("%+v", st)
	}
}

// issue #79: the rate is averaged over rateWindow, so reads 100 ms apart (conntrack events) do not
// turn a burst into the rate the UI shows for the next seconds.
func TestTrafficRatesAreAveragedOverTheWindow(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	ip, srv := addr("10.10.0.31"), addr("203.0.113.10")
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	read := func(ms int, up int64) observation {
		return observation{At: at(ms), Neighbors: []domain.Neighbor{neigh("10.10.0.31", macCf)}, ConntrackRead: true,
			Flows: []FlowCounters{{Key: "a", Src: ip, Dst: srv, Up: Traffic{up, up}}}}
	}
	tr.step(cfg, read(0, 0))
	for ms := 1000; ms <= 9000; ms += 1000 {
		tr.step(cfg, read(ms, int64(ms))) // 1000 B/s
	}
	// 100 ms later a burst of 10 000 bytes
	_, states, _ := tr.step(cfg, read(9100, 19000))
	st := stateOf(states, tDev)
	// the window holds the reads since 0.1 s, 9 000 + 10 000 bytes over 9.1 s: about 2088 B/s, not 100 000
	if st.UploadBps < 1900 || st.UploadBps > 2300 {
		t.Errorf("%+v", st)
	}
}

func TestHostsBehindARouterAreDiscoveredByAddress(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	ip := addr("10.50.0.10")
	_, states, ev := tr.step(cfg, observation{At: t0.Add(time.Second), Active: map[netip.Addr]bool{ip: true}, UnknownSources: []netip.Addr{ip},
		ConntrackRead: true, Flows: []FlowCounters{{Key: "a", Src: ip, Dst: addr("203.0.113.10"), Up: Traffic{1, 60}}}})
	if types(ev) != "device_discovered,device_online" && types(ev) != "device_discovered" {
		t.Fatalf("%v", types(ev))
	}
	var d *DeviceState
	for i := range states {
		if states[i].Origin == model.DeviceOriginDiscovered {
			d = &states[i]
		}
	}
	if d == nil || d.Name != "dev-10-50-0-10" || len(d.MACs) != 0 || !d.Online || d.Addresses[0].String() != "10.50.0.10" {
		t.Errorf("%+v", d)
	}
}

func TestAConfiguredEntryCoversADiscoveredOneAfterAMerge(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	// a randomized MAC appears as a new discovered device
	_, states, _ := tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.60", macB)}})
	count := func() (n int) {
		for _, s := range states {
			if s.Origin == model.DeviceOriginDiscovered {
				n++
			}
		}
		return
	}
	if count() != 1 {
		t.Fatalf("%d discovered", count())
	}
	// merging: the configured device takes the new MAC as an identifier; the entry disappears
	macs := []string{macCf, macB}
	d := (*cfg.Devices)[tDev]
	d.Identifiers = &model.DeviceIdentifiers{Macs: &macs}
	(*cfg.Devices)[tDev] = d
	_, states, _ = tr.step(cfg, observation{At: t0.Add(2 * time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.60", macB)}})
	if count() != 0 {
		t.Errorf("the discovered entry stays after the merge: %+v", states)
	}
	for _, s := range states {
		if s.ID == tDev && (len(s.Addresses) != 1 || s.Addresses[0].String() != "10.10.0.60") {
			t.Errorf("%+v", s)
		}
	}
}

func TestForgottenDevicesAndTheRegistryBound(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.50", macA)}})
	_, states, _ := tr.step(cfg, observation{At: t0.Add(forgetAfter + time.Minute)})
	for _, s := range states {
		if s.Origin == model.DeviceOriginDiscovered {
			t.Errorf("a device that was not seen for a day is still known: %+v", s)
		}
	}
}

func TestAReservedAddressStaysWithItsDevice(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	ip := "10.10.0.31"
	d := (*cfg.Devices)[tDev]
	d.FixedIp = &ip
	(*cfg.Devices)[tDev] = d
	id, _, _ := tr.step(cfg, observation{At: t0})
	if id.Owner[addr("10.10.0.31")] != tDev {
		t.Errorf("a reservation maps the address to the device even before it is seen: %v", id.Owner)
	}
}

// #78: removing an adopted device from the configuration brings it back as discovered under the same
// UUID, as long as the MAC it is seen with is the one its id was derived from (adopting keeps the
// discovered id, which is a function of the first MAC). Overlays that target it keep working. A device
// seen only with another of its MACs comes back under that MAC's id.
func TestARemovedAdoptionComesBackAsDiscoveredUnderTheSameID(t *testing.T) {
	for name, tc := range map[string]struct {
		seen   string
		sameID bool
	}{
		"seen with the MAC it was discovered by": {seen: macA, sameID: true},
		"seen only with another of its MACs":     {seen: macB, sameID: false},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newTracker()
			id := DeviceID(macA)
			macs := []string{macA, macB}
			adopted := &model.Configuration{Devices: &map[string]model.Device{id: {Name: "esp32-ab12", Identifiers: &model.DeviceIdentifiers{Macs: &macs}}}}
			obs := func(at time.Duration) observation {
				return observation{At: t0.Add(at), Neighbors: []domain.Neighbor{neigh("10.10.0.50", tc.seen)}}
			}
			tr.step(adopted, observation{At: t0})
			_, states, _ := tr.step(adopted, obs(time.Second))
			if len(states) != 1 || states[0].ID != id || states[0].Origin != model.DeviceOriginConfigured {
				t.Fatalf("adopted: %+v", states)
			}
			// the adoption is removed: the next observation finds the device again as discovered
			_, states, _ = tr.step(&model.Configuration{}, obs(2*time.Second))
			if len(states) != 1 || states[0].Origin != model.DeviceOriginDiscovered {
				t.Fatalf("removed: %+v", states)
			}
			if got := states[0].ID == id; got != tc.sameID {
				t.Errorf("id %s, adopted as %s (same id expected: %v)", states[0].ID, id, tc.sameID)
			}
			if states[0].ID != DeviceID(tc.seen) {
				t.Errorf("id %s, not the one of the MAC it is seen with", states[0].ID)
			}
		})
	}
}
