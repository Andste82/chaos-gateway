package domain

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

const idSlowDNS = "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e60"

func TestOrphanedOverlaysNameTheDeletedObjectsOfTheOldConfiguration(t *testing.T) {
	tw := newTestWorld(t, false)
	onDevice := tw.overlay(`{target: {device: esp32-42}, fault: {latency: 1ms}}`, time.Second)
	onGroup := tw.overlay(`{target: {group: g2}, fault: {latency: 1ms}}`, 2*time.Second)
	onBoth := tw.overlay(`{target: {network: IoT}, fault: {destination: {network: lab-hub}, latency: 1ms}}`, 3*time.Second)
	profile := tw.overlay(`{target: {global: true}, profile: slow-dns-lte}`, 4*time.Second)
	builtin := tw.overlay(`{target: {global: true}, profile: lte}`, 5*time.Second)
	unaffected := tw.overlay(`{target: {group: sensors}, fault: {latency: 1ms}}`, 6*time.Second)

	next := clone(*tw.cfg)
	delete(*next.Devices, idESP)
	delete(*next.Groups, idG2)
	delete(*next.Profiles, idSlowDNS)
	delete(*next.Networks, idIoT)
	delete(*next.Networks, idHub)

	got := OrphanedOverlays(tw.cfg, &next, tw.overlays)
	byID := map[string][]string{}
	for _, o := range got {
		byID[o.Overlay.Id.String()] = o.Objects
	}
	want := map[string][]string{
		onDevice.Id.String(): {"/devices/" + idESP},
		onGroup.Id.String():  {"/groups/" + idG2},
		onBoth.Id.String():   {"/networks/" + idIoT, "/networks/" + idHub},
		profile.Id.String():  {"/profiles/" + idSlowDNS},
	}
	if !reflect.DeepEqual(byID, want) {
		t.Fatalf("orphans = %v\nwant     %v", byID, want)
	}
	for _, id := range []string{builtin.Id.String(), unaffected.Id.String()} {
		if _, bad := byID[id]; bad {
			t.Errorf("%s must not be orphaned", id)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Overlay.Id.String() > got[i].Overlay.Id.String() {
			t.Fatal("the result must be sorted by id")
		}
	}
}

func TestAClientOrLinkThatGoesIsAnObjectOfItsNetwork(t *testing.T) {
	tw := newTestWorld(t, false)
	tunnel := tw.overlay(`{fault: {family: tunnel, tunnel: {link: site-b}, latency: 10ms}}`, time.Second)
	wg := tw.overlay(`{wireguard: {client: lab-rA, action: disable}}`, 2*time.Second)
	next := clone(*tw.cfg)
	delete(*next.Networks, "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f") // the link
	got := OrphanedOverlays(tw.cfg, &next, tw.overlays)
	if len(got) != 1 || got[0].Overlay.Id != tunnel.Id || got[0].Objects[0] != "/networks/c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f" {
		t.Fatalf("orphans = %+v", got)
	}
	_ = wg
}

func TestNothingIsOrphanedWhenTheRevisionKeepsEveryReferencedObject(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, fault: {latency: 1ms}}`, time.Second)
	next := clone(*tw.cfg)
	delete(*next.Groups, idG2)
	if got := OrphanedOverlays(tw.cfg, &next, tw.overlays); len(got) != 0 {
		t.Fatalf("orphans = %+v", got)
	}
}

func TestAnOverlayOnADiscoveredDeviceIsNotOrphanedByARevision(t *testing.T) {
	tw := newTestWorld(t, false)
	req := requestOf(t, `{target: {global: true}, fault: {latency: 1ms}}`)
	d := discoveredID
	req.Target = &model.Scope{Device: &d}
	o, err := NewOverlay(*req, admin, mustUUID("00000000-0000-4000-8000-0000000000f1"), t0)
	if err != nil {
		t.Fatal(err)
	}
	// the discovered device is no object of the configuration: the revision cannot delete it
	if got := OrphanedOverlays(tw.cfg, tw.cfg, []model.Overlay{o}); len(got) != 0 {
		t.Fatalf("orphans = %+v", got)
	}
}

func TestAnAdoptedDeviceKeepsItsOverlays(t *testing.T) {
	// adopting a discovered device stores it under the same UUID: the overlay still resolves
	tw := newTestWorld(t, false)
	next := clone(*tw.cfg)
	(*next.Devices)[discoveredID] = model.Device{Name: "adopted"}
	req := requestOf(t, `{target: {global: true}, fault: {latency: 1ms}}`)
	d := discoveredID
	req.Target = &model.Scope{Device: &d}
	o, _ := NewOverlay(*req, admin, mustUUID("00000000-0000-4000-8000-0000000000f2"), t0)
	if got := OrphanedOverlays(tw.cfg, &next, []model.Overlay{o}); len(got) != 0 {
		t.Fatalf("orphans = %+v", got)
	}
	// and removing it again later orphans the overlay like any configured device
	if got := OrphanedOverlays(&next, tw.cfg, []model.Overlay{o}); len(got) != 1 || got[0].Objects[0] != "/devices/"+discoveredID {
		t.Fatalf("orphans = %+v", got)
	}
}

func TestADiscoveredDeviceThatAConfiguredOneNowCoversIsAMerge(t *testing.T) {
	cfg := newTestWorld(t, false).cfg
	// the device came with a randomized MAC: it is discovered under a UUID of its own
	randomized := DiscoveredDevice{ID: "aaaaaaaa-0000-4000-8000-000000000001", MACs: []string{"02:00:00:00:00:99"}, IPs: nil}
	byIP := DiscoveredDevice{ID: "aaaaaaaa-0000-4000-8000-000000000002", IPs: []netipAddr{mustAddr("10.50.0.10")}}
	other := DiscoveredDevice{ID: "aaaaaaaa-0000-4000-8000-000000000003", MACs: []string{"02:00:00:00:00:77"}}
	found := []DiscoveredDevice{randomized, byIP, other}

	// before the revision no configured device covers the randomized MAC
	next := clone(*cfg)
	esp := (*next.Devices)[idESP]
	macs := append(append([]string(nil), *esp.Identifiers.Macs...), "02:00:00:00:00:99")
	esp.Identifiers = &model.DeviceIdentifiers{Macs: &macs}
	(*next.Devices)[idESP] = esp

	got := DiscoveredMerges(&next, found)
	want := map[string]string{
		randomized.ID: idESP,
		byIP.ID:       idLab, // the lab host owns 10.50.0.10
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merges = %v, want %v", got, want)
	}
	// without the new identifier only the one that was covered by address before merges
	if got := DiscoveredMerges(cfg, found); !reflect.DeepEqual(got, map[string]string{byIP.ID: idLab}) {
		t.Fatalf("merges without the new MAC: %v", got)
	}
	if got := DiscoveredMerges(&next, nil); got != nil {
		t.Fatalf("merges = %v", got)
	}
}

func TestAdoptingADiscoveredDeviceUnderItsOwnUUIDIsNoMerge(t *testing.T) {
	cfg := newTestWorld(t, false).cfg
	next := clone(*cfg)
	macs := []string{"02:00:00:00:00:55"}
	(*next.Devices)[discoveredID] = model.Device{Name: "adopted", Identifiers: &model.DeviceIdentifiers{Macs: &macs}}
	found := []DiscoveredDevice{{ID: discoveredID, MACs: macs}}
	if got := DiscoveredMerges(&next, found); got != nil {
		t.Fatalf("merges = %v", got)
	}
}

func TestScopeContainsAnswersForTheTargetOfAnOverlay(t *testing.T) {
	tw := newTestWorld(t, false)
	w := tw.world()
	device, group, network := idESP, idSensors, idIoT
	for _, c := range []struct {
		scope model.Scope
		want  bool
	}{
		{model.Scope{Device: &device}, true},
		{model.Scope{Group: &group}, true},
		{model.Scope{Network: &network}, true},
		{model.Scope{Global: ptrTo(model.ScopeGlobal(true))}, true},
		{model.Scope{Device: &idLabCopy}, false},
	} {
		if got := w.ScopeContains(c.scope, subjectA); got != c.want {
			t.Errorf("%+v contains = %v", c.scope, got)
		}
	}
}

var idLabCopy = idLab

type netipAddr = netip.Addr

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }
