package overlay

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The worked examples of plan §2.4 once more, with the overlays coming from the store: what the
// resolution sees is what was written, replaced, expired or reset, in the order it happened.

const (
	idLab      = "2e3f4a5b-6c7d-4e8f-9a0b-1c2d3e4f5a6b" // device lab-host, 10.50.0.10
	idSensors  = "3f4a5b6c-7d8e-4f9a-0b1c-2d3e4f5a6b7c"
	idFaultNet = "7d8e9f0a-1b2c-4d3e-4f5a-6b7c8d9e0f1a" // iot-latency, 100 ms on the network IoT
	idFaultDev = "8e9f0a1b-2c3d-4e4f-5a6b-7c8d9e0f1a2b" // esp-latency, 20 ms / 20 ms on esp32-42
)

var (
	subjectA = domain.Subject{Device: idESP, IP: netip.MustParseAddr("10.10.0.42")}
	srcA     = domain.Source{Subject: subjectA, Device: idESP}
)

func (f *fixture) world() *domain.World {
	f.t.Helper()
	w, err := domain.NewWorld(f.cfg, f.store.Overlays())
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

func toServer(proto string, port int) domain.Query {
	return domain.Query{
		Source: subjectA, DestIP: netip.MustParseAddr("203.0.113.10"),
		DestNames: []string{"broker.example.com"}, Protocol: proto, Port: port,
	}
}

func (f *fixture) impairment(q domain.Query) *domain.Candidate {
	f.t.Helper()
	return domain.Winner(f.world().Resolve(q), domain.FamilyImpairment)
}

func latency(c *domain.Candidate) string {
	if c == nil || c.Impairment == nil || c.Impairment.Latency == nil {
		return ""
	}
	return *c.Impairment.Latency
}

// dropFaults removes the configured faults but the ones a test names.
func (f *fixture) keepFaults(keep ...string) {
	kept := map[string]model.ConfigFault{}
	for _, id := range keep {
		kept[id] = (*f.cfg.Faults)[id]
	}
	f.cfg.Faults = &kept
}

func TestE1TheOverlayBeatsTheConfiguredFaultOfTheDeviceAndGoesWithTheTTL(t *testing.T) {
	f := newFixture(t)
	f.keepFaults(idFaultDev)
	f.put(admin, `{target: {network: IoT}, fault: {blackout: true}, ttl: 5m}`)
	win := f.impairment(toServer("tcp", 8883))
	if win == nil || win.Layer != domain.LayerOverlay || win.Impairment.Blackout == nil || !*win.Impairment.Blackout {
		t.Fatalf("winner = %+v", win)
	}
	// the network comes back when the TTL runs out: the configured device fault applies again
	f.clk.Advance(5 * time.Minute)
	if got := f.store.Expire(); len(got) != 1 {
		t.Fatalf("expired %d", len(got))
	}
	win = f.impairment(toServer("tcp", 8883))
	if win == nil || win.ID != idFaultDev || win.Layer != domain.LayerConfig {
		t.Fatalf("after the TTL: %+v", win)
	}
}

func TestE2TheDeviceFaultIsMoreSpecificThanTheNetworkFault(t *testing.T) {
	f := newFixture(t)
	win := f.impairment(domain.Query{Source: subjectA})
	if win == nil || win.ID != idFaultDev {
		t.Fatalf("winner = %+v", win)
	}
}

func TestE3AndE4TheMoreSpecificOverlayWinsOnItsTrafficOnly(t *testing.T) {
	f := newFixture(t)
	f.keepFaults()
	group := f.put(admin, `{target: {group: sensors}, fault: {latency: 200ms}}`).Overlay
	port := f.put(admin, `{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}}`).Overlay
	e3 := f.impairment(toServer("tcp", 8883))
	if e3.ID != port.Id.String() || latency(e3) != "" || *e3.Impairment.Loss != "5%" {
		t.Fatalf("E3 winner = %+v", e3)
	}
	ntp := toServer("udp", 123)
	ntp.DestNames = []string{"pool.ntp.org"}
	if e4 := f.impairment(ntp); e4.ID != group.Id.String() || latency(e4) != "200ms" {
		t.Fatalf("E4 winner = %+v", e4)
	}
}

func TestE5DifferentFamiliesCombine(t *testing.T) {
	f := newFixture(t)
	f.keepFaults()
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}}`)
	f.put(admin, `{target: {network: IoT}, profile: dns-broken}`)
	q := toServer("tcp", 8883)
	q.DNSName = "broker.example.com"
	res := f.world().Resolve(q)
	if latency(domain.Winner(res, domain.FamilyImpairment)) != "100ms" {
		t.Fatal("the latency must stay")
	}
	if d := domain.Winner(res, domain.FamilyDNS); d == nil || d.DNS.Action != "servfail" {
		t.Fatalf("dns winner = %+v", d)
	}
}

func TestE6TheNewerEntryWinsAndRewritingAnOverlayMakesItNewer(t *testing.T) {
	f := newFixture(t)
	f.keepFaults()
	older := f.put(admin, `{target: {group: sensors}, fault: {latency: 30ms}}`).Overlay
	f.clk.Advance(time.Second)
	newer := f.put(admin, `{target: {group: g2}, fault: {latency: 60ms}}`).Overlay
	if win := f.impairment(toServer("tcp", 443)); win.ID != newer.Id.String() {
		t.Fatalf("winner = %s, want the newer %s", win.ID, newer.Id)
	}
	// writing the older one again replaces it, keeps its id, and makes it the newer one
	f.clk.Advance(time.Second)
	again := f.put(admin, `{target: {group: sensors}, fault: {latency: 31ms}}`)
	if again.Overlay.Id != older.Id || again.Type != Updated {
		t.Fatalf("%+v", again)
	}
	if win := f.impairment(toServer("tcp", 443)); win.ID != older.Id.String() || latency(win) != "31ms" {
		t.Fatalf("winner = %+v", win)
	}
	// and even without any time passing a later write is the newer one
	last := f.put(admin, `{target: {group: g2}, fault: {latency: 61ms}}`)
	if win := f.impairment(toServer("tcp", 443)); win.ID != last.Overlay.Id.String() {
		t.Fatalf("winner = %+v", win)
	}
}

func TestE7ADestinationBeatsGlobal(t *testing.T) {
	f := newFixture(t)
	f.keepFaults()
	loss := f.put(admin, `{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/24}, loss: 10%}}`).Overlay
	f.put(admin, `{target: {global: true}, fault: {latency: 5ms}}`)
	win := f.impairment(toServer("tcp", 443))
	if win.ID != loss.Id.String() || latency(win) != "" {
		t.Fatalf("winner = %+v", win)
	}
}

func TestE8AFaultBeatsAProfilePartOnTheSameScope(t *testing.T) {
	f := newFixture(t)
	f.keepFaults()
	f.put(admin, `{target: {device: esp32-42}, profile: bad-lte}`)
	f.clk.Advance(time.Second)
	fault := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 300ms}}`).Overlay
	win := f.impairment(toServer("tcp", 443))
	if win.ID != fault.Id.String() || latency(win) != "300ms" || win.Impairment.Rate != nil {
		t.Fatalf("winner = %+v", win)
	}
	// without the fault the profile part applies again
	if _, err := f.store.Delete(fault.Id); err != nil {
		t.Fatal(err)
	}
	if win := f.impairment(toServer("tcp", 443)); win == nil || !win.IsProfilePart() || *win.Impairment.Rate != "2Mbit" {
		t.Fatalf("winner = %+v", win)
	}
}

// E9 (plan §2.4, M10): "Bad LTE" on the network reaches every device of the network, A and a device that
// is only known by its address, with the profile's full rate each; and it goes with its overlay.
func TestE9ABadLTEOverlayOnTheNetworkReachesEveryDeviceOfIt(t *testing.T) {
	f := newFixture(t)
	f.keepFaults()
	o := f.put(admin, `{target: {network: IoT}, profile: bad-lte}`).Overlay
	b := domain.Subject{IP: netip.MustParseAddr("10.10.0.77")}
	for name, s := range map[string]domain.Subject{"A": subjectA, "B": b} {
		win := domain.Winner(f.world().Resolve(domain.Query{Source: s, DestIP: netip.MustParseAddr("203.0.113.10"), Protocol: "tcp", Port: 443}), domain.FamilyImpairment)
		if win == nil || win.ID != o.Id.String() || *win.Impairment.Rate != "2Mbit" || win.ProfileName != "bad-lte" {
			t.Errorf("%s: winner = %+v", name, win)
		}
	}
	if _, err := f.store.Delete(o.Id); err != nil {
		t.Fatal(err)
	}
	if win := f.impairment(toServer("tcp", 443)); win != nil {
		t.Errorf("the profile outlived its overlay: %+v", win)
	}
}

func TestE12TheInitiatorsScopeDecides(t *testing.T) {
	f := newFixture(t)
	f.keepFaults()
	f.put(admin, `{target: {network: IoT}, fault: {latency: 100ms}}`)
	lab := domain.Query{
		Source: domain.Subject{Device: idLab, IP: netip.MustParseAddr("10.50.0.10")},
		DestIP: netip.MustParseAddr("10.10.0.42"), Protocol: "tcp", Port: 8883,
	}
	if win := f.impairment(lab); win != nil {
		t.Fatalf("the IoT fault must not apply to the lab host's connection: %+v", win)
	}
	if win := f.impairment(toServer("tcp", 443)); latency(win) != "100ms" {
		t.Fatal("A must be impaired")
	}
}

func TestAResetReturnsToTheConfiguration(t *testing.T) {
	f := newFixture(t)
	f.keepFaults(idFaultDev)
	mine := token("t")
	f.put(mine, `{target: {network: IoT}, fault: {blackout: true}}`)
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 900ms}}`)
	if win := f.impairment(toServer("tcp", 443)); latency(win) != "900ms" {
		t.Fatalf("the device overlay is more specific than the network's: %+v", win)
	}
	f.store.Reset(&mine)
	win := f.impairment(toServer("tcp", 443))
	if latency(win) != "900ms" {
		t.Fatalf("the admin's overlay must stay: %+v", win)
	}
	f.store.Reset(nil)
	if win := f.impairment(toServer("tcp", 443)); win == nil || win.ID != idFaultDev {
		t.Fatalf("back on the configuration: %+v", win)
	}
}

func TestTheTableFollowsTheStore(t *testing.T) {
	f := newFixture(t)
	f.keepFaults(idFaultDev)
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], loss: 5%}, ttl: 1m}`).Overlay
	tab, err := f.world().Table(srcA, domain.FamilyImpairment)
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Entries) != 2 || tab.Entries[0].Level != 3 || tab.Entries[0].Winner.ID != ov.Id.String() || tab.Entries[1].Level != 4 || tab.Entries[1].Winner.ID != idFaultDev {
		t.Fatalf("entries = %v", tab.Entries)
	}
	f.clk.Advance(time.Minute)
	f.store.Expire()
	tab, _ = f.world().Table(srcA, domain.FamilyImpairment)
	if len(tab.Entries) != 1 || tab.Entries[0].Level != 4 {
		t.Fatalf("after the expiry: %v", tab.Entries)
	}
}
