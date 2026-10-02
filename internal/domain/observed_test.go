package domain

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func lease(addr, mac string, expires time.Duration) model.DhcpLease {
	return model.DhcpLease{Ip: addr, Mac: mac, Network: mustUUID(idIoT), ExpiresAt: t0.Add(expires)}
}

const espMAC = "24:0a:c4:00:00:42"

func TestADeviceGetsItsAddressFromALeaseTheNeighborTableAndItsReservation(t *testing.T) {
	cfg := stored(t) // esp32-42: MAC 24:0a:c4:00:00:42, reservation 10.10.0.42
	id := ResolveIdentity(cfg, Observed{
		Leases:    []model.DhcpLease{lease("10.10.0.50", espMAC, time.Hour)},
		Neighbors: []Neighbor{{IP: ip("10.10.0.51"), MAC: espMAC}},
	}, nil)
	want := []netip.Addr{ip("10.10.0.42"), ip("10.10.0.50"), ip("10.10.0.51")}
	if !reflect.DeepEqual(id.Addresses[idESP], want) {
		t.Fatalf("addresses = %v, want %v", id.Addresses[idESP], want)
	}
	for _, a := range want {
		if owner, ok := id.OwnerOf(a); !ok || owner != idESP {
			t.Errorf("owner of %s = %s", a, owner)
		}
	}
	if _, ok := id.OwnerOf(ip("10.10.0.99")); ok {
		t.Error("an unknown address has no owner")
	}
}

func TestAnExpiredOrDeclinedLeaseDoesNotCount(t *testing.T) {
	cfg := stored(t)
	expired := lease("10.10.0.60", espMAC, time.Hour)
	state := model.DhcpLeaseState("expired")
	expired.State = &state
	active := lease("10.10.0.61", espMAC, time.Hour)
	activeState := model.DhcpLeaseState("active")
	active.State = &activeState
	id := ResolveIdentity(cfg, Observed{Leases: []model.DhcpLease{expired, active}}, nil)
	if _, ok := id.Owner[ip("10.10.0.60")]; ok {
		t.Error("an expired lease must not map an address")
	}
	if id.Owner[ip("10.10.0.61")] != idESP {
		t.Error("an active lease must")
	}
}

func TestLeaseMACsAreMatchedIgnoringCase(t *testing.T) {
	id := ResolveIdentity(stored(t), Observed{Leases: []model.DhcpLease{lease("10.10.0.50", "24:0A:C4:00:00:42", time.Hour)}}, nil)
	if id.Owner[ip("10.10.0.50")] != idESP {
		t.Fatal("the MAC must match whatever its case")
	}
}

func TestAWireGuardClientIsIdentifiedByItsTunnelAddressAndAHostByItsAddress(t *testing.T) {
	id := ResolveIdentity(stored(t), Observed{}, nil)
	if id.Owner[ip("10.99.0.2")] != idClient {
		t.Errorf("client address owner = %s", id.Owner[ip("10.99.0.2")])
	}
	if id.Owner[ip("10.50.0.10")] != idLab {
		t.Errorf("lab host owner = %s", id.Owner[ip("10.50.0.10")])
	}
}

func TestADeviceIdentifiedByARangeOwnsEveryAddressInIt(t *testing.T) {
	cfg := stored(t)
	devs := *cfg.Devices
	lab := devs[idLab]
	lab.Identifiers = &model.DeviceIdentifiers{Ipv4: &[]string{"10.50.0.0/24"}}
	devs[idLab] = lab
	id := ResolveIdentity(cfg, Observed{}, nil)
	if owner, ok := id.OwnerOf(ip("10.50.0.77")); !ok || owner != idLab {
		t.Fatalf("owner = %s, %v", owner, ok)
	}
	// an exact address wins over a range, and a narrower range over a wider one
	devs[idESP] = model.Device{Name: "esp32-42", Identifiers: &model.DeviceIdentifiers{
		Macs: &[]string{espMAC}, Ipv4: &[]string{"10.50.0.128/25"}}}
	id = ResolveIdentity(cfg, Observed{}, nil)
	if owner, _ := id.OwnerOf(ip("10.50.0.200")); owner != idESP {
		t.Errorf("the narrower range must win: %s", owner)
	}
	if owner, _ := id.OwnerOf(ip("10.50.0.5")); owner != idLab {
		t.Errorf("the wider range elsewhere: %s", owner)
	}
}

func TestProbesAreIdentifiedByTheirMACAndAddresses(t *testing.T) {
	obs := Observed{
		Probes: map[string]ProbeObservation{idProbe: {MAC: "02:00:00:aa:bb:cc", IPs: []netip.Addr{ip("10.10.0.250")}}},
		Leases: []model.DhcpLease{lease("10.10.0.251", "02:00:00:aa:bb:cc", time.Hour)},
	}
	id := ResolveIdentity(stored(t), obs, nil)
	if id.Owner[ip("10.10.0.250")] != idProbe || id.Owner[ip("10.10.0.251")] != idProbe {
		t.Fatalf("owners = %v", id.Owner)
	}
}

func TestTwoDevicesClaimingOneAddressTheStrongerClaimWins(t *testing.T) {
	cfg := stored(t)
	devs := *cfg.Devices
	devs[idNew] = model.Device{Name: "other", Identifiers: &model.DeviceIdentifiers{Macs: &[]string{"24:0a:c4:00:00:99"}}}
	conflict := ip("10.10.0.77")

	// a lease beats a neighbor entry
	id := ResolveIdentity(cfg, Observed{
		Leases:    []model.DhcpLease{lease("10.10.0.77", "24:0a:c4:00:00:99", time.Hour)},
		Neighbors: []Neighbor{{IP: conflict, MAC: espMAC}},
	}, nil)
	if id.Owner[conflict] != idNew {
		t.Fatalf("owner = %s", id.Owner[conflict])
	}
	if len(id.Conflicts) != 1 || id.Conflicts[0].IP != conflict || id.Conflicts[0].Winner != idNew || len(id.Conflicts[0].Devices) != 2 {
		t.Fatalf("conflicts = %+v", id.Conflicts)
	}

	// of two leases the one that expires later is the newer one
	id = ResolveIdentity(cfg, Observed{Leases: []model.DhcpLease{
		lease("10.10.0.77", espMAC, time.Hour),
		lease("10.10.0.77", "24:0a:c4:00:00:99", 2*time.Hour),
	}}, nil)
	if id.Owner[conflict] != idNew {
		t.Fatalf("the newer lease must win, owner = %s", id.Owner[conflict])
	}

	// an address named in the configuration beats a lease for another device
	devs[idNew] = model.Device{Name: "other", Identifiers: &model.DeviceIdentifiers{Ipv4: &[]string{"10.10.0.77"}}}
	id = ResolveIdentity(cfg, Observed{Leases: []model.DhcpLease{lease("10.10.0.77", espMAC, time.Hour)}}, nil)
	if id.Owner[conflict] != idNew {
		t.Fatalf("an explicit address must win, owner = %s", id.Owner[conflict])
	}
}

func TestAnOldAddressStaysMappedWhileItCarriesConnectionsUnlessItIsLeasedToAnotherDevice(t *testing.T) {
	cfg := stored(t)
	first := ResolveIdentity(cfg, Observed{Leases: []model.DhcpLease{lease("10.10.0.50", espMAC, time.Hour)}}, nil)

	// "force new IP": the device now has .60, but still has connections from .50
	moved := Observed{
		Leases:        []model.DhcpLease{lease("10.10.0.60", espMAC, 2*time.Hour)},
		ActiveSources: map[netip.Addr]bool{ip("10.10.0.50"): true},
	}
	second := ResolveIdentity(cfg, moved, &first)
	if second.Owner[ip("10.10.0.60")] != idESP || second.Owner[ip("10.10.0.50")] != idESP {
		t.Fatalf("both addresses must belong to the device: %v", second.Owner)
	}

	// once the connections are gone, the old address is released
	moved.ActiveSources = nil
	third := ResolveIdentity(cfg, moved, &second)
	if _, held := third.Owner[ip("10.10.0.50")]; held {
		t.Fatal("an address without connections must be released")
	}

	// leased to another device in the meantime: the new holder wins
	devs := *cfg.Devices
	devs[idNew] = model.Device{Name: "other", Identifiers: &model.DeviceIdentifiers{Macs: &[]string{"24:0a:c4:00:00:99"}}}
	taken := Observed{
		Leases: []model.DhcpLease{
			lease("10.10.0.60", espMAC, 2*time.Hour),
			lease("10.10.0.50", "24:0a:c4:00:00:99", 2*time.Hour),
		},
		ActiveSources: map[netip.Addr]bool{ip("10.10.0.50"): true},
	}
	fourth := ResolveIdentity(cfg, taken, &first)
	if fourth.Owner[ip("10.10.0.50")] != idNew {
		t.Fatalf("the new holder must win, owner = %s", fourth.Owner[ip("10.10.0.50")])
	}
}

func TestDiscoveredDevicesDisappearWhenAConfiguredDeviceCoversThem(t *testing.T) {
	cfg := stored(t)
	obs := Observed{Discovered: []DiscoveredDevice{
		{ID: "d3", MACs: []string{"aa:bb:cc:00:00:03"}},                  // unknown
		{ID: "d1", MACs: []string{espMAC}},                               // covered: a configured device has the MAC
		{ID: "d2", MACs: []string{espMAC, "aa:bb:cc:00:00:02"}},          // one MAC is unknown: stays
		{ID: "d4", IPs: []netip.Addr{ip("10.50.0.10")}},                  // no MAC; covered by an address
		{ID: "d5", IPs: []netip.Addr{ip("10.50.0.10"), ip("10.60.0.1")}}, // one address is unknown: stays
		{ID: "d6"}, // nothing known: stays
	}}
	got := ResolveIdentity(cfg, obs, nil).Discovered
	var ids []string
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if want := []string{"d2", "d3", "d5", "d6"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("discovered = %v, want %v", ids, want)
	}
}

func TestMergingTwoDevicesMakesTheDiscoveredEntryDisappear(t *testing.T) {
	// a device re-appears with a randomized MAC: it is discovered; merging adds its MAC to the
	// configured device, and the discovered entry is covered
	cfg := stored(t)
	obs := Observed{Discovered: []DiscoveredDevice{{ID: "new", MACs: []string{"de:ad:be:ef:00:01"}}}}
	if len(ResolveIdentity(cfg, obs, nil).Discovered) != 1 {
		t.Fatal("the new MAC is a discovered device")
	}
	devs := *cfg.Devices
	esp := devs[idESP]
	macs := append(deref(esp.Identifiers.Macs), "de:ad:be:ef:00:01")
	esp.Identifiers.Macs = &macs
	devs[idESP] = esp
	if got := ResolveIdentity(cfg, obs, nil).Discovered; len(got) != 0 {
		t.Fatalf("the merged entry must disappear: %+v", got)
	}
}

func TestDiscoveredName(t *testing.T) {
	if got := DiscoveredName("24:0A:C4:00:00:42"); got != "dev-24-0a-c4-00-00-42" {
		t.Fatalf("name = %s", got)
	}
	// the generated name satisfies the name rules of the spec
	v, _ := Schemas()
	if errs := v.Validate("Name", DiscoveredName("24:0a:c4:00:00:42")); len(errs) != 0 {
		t.Fatalf("the generated name is not a valid Name: %v", errs)
	}
}

func TestIdentityIsDeterministic(t *testing.T) {
	cfg := stored(t)
	obs := Observed{
		Leases:    []model.DhcpLease{lease("10.10.0.50", espMAC, time.Hour), lease("10.10.0.51", espMAC, time.Hour)},
		Neighbors: []Neighbor{{IP: ip("10.10.0.52"), MAC: espMAC}},
	}
	a, b := ResolveIdentity(cfg, obs, nil), ResolveIdentity(cfg, obs, nil)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same input must give the same identity")
	}
	for dev, addrs := range a.Addresses {
		for i := 1; i < len(addrs); i++ {
			if !addrs[i-1].Less(addrs[i]) {
				t.Errorf("%s: addresses are not sorted: %v", dev, addrs)
			}
		}
	}
}
