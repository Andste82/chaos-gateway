package engine

import (
	"net/netip"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(types(ev), EventDeviceOnline) {
		t.Errorf("%v", types(ev))
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
	_, states, ev := tr.step(cfg, observation{At: t0.Add(2 * time.Second)})
	if !strings.Contains(types(ev), EventDeviceOffline) {
		t.Errorf("%v", types(ev))
	}
	for _, s := range states {
		if s.ID == tDev && (s.Online || s.LastSeen.IsZero() || !s.LastSeen.Equal(t0.Add(time.Second))) {
			t.Errorf("%+v", s)
		}
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
	}
}

// M6a-03 test: a device's active flow count and byte rates come from the conntrack traffic the
// observation carries for its addresses; the rate needs two samples, since conntrack counters are
// cumulative for the life of a connection.
func TestDeviceTrafficRatesAndActiveFlowCount(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	ip := addr("10.10.0.31")
	stateOf := func(states []DeviceState) *DeviceState {
		for i := range states {
			if states[i].ID == tDev {
				return &states[i]
			}
		}
		return nil
	}

	_, states, _ := tr.step(cfg, observation{At: t0.Add(time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.31", macCf)},
		Traffic: map[netip.Addr]AddrTraffic{ip: {Upload: Traffic{Packets: 10, Bytes: 1000}, Download: Traffic{Packets: 5, Bytes: 500}, Flows: 2}}})
	st := stateOf(states)
	if st == nil || st.FlowsActive != 2 || st.UploadBps != 0 || st.DownloadBps != 0 {
		t.Fatalf("first sample has no previous one to rate against: %+v", st)
	}

	_, states, _ = tr.step(cfg, observation{At: t0.Add(3 * time.Second), Neighbors: []domain.Neighbor{neigh("10.10.0.31", macCf)},
		Traffic: map[netip.Addr]AddrTraffic{ip: {Upload: Traffic{Packets: 30, Bytes: 5000}, Download: Traffic{Packets: 15, Bytes: 2500}, Flows: 1}}})
	st = stateOf(states)
	// two seconds elapsed: (5000-1000)/2 = 2000 B/s upload, (2500-500)/2 = 1000 B/s download
	if st == nil || st.FlowsActive != 1 || st.UploadBps != 2000 || st.DownloadBps != 1000 {
		t.Fatalf("%+v", st)
	}
}

func TestHostsBehindARouterAreDiscoveredByAddress(t *testing.T) {
	tr := newTracker()
	cfg := trackerConfig(t)
	tr.step(cfg, observation{At: t0})
	_, states, ev := tr.step(cfg, observation{At: t0.Add(time.Second), Active: map[netip.Addr]bool{addr("10.50.0.10"): true}, UnknownSources: []netip.Addr{addr("10.50.0.10")}})
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
