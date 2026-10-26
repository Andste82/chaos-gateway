package compiler

import (
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The DHCP family (plan §2.4, §2.7, M23): the test actions are laid over the configuration of Kea.

const (
	macESP42 = "24:0a:c4:00:00:42"
	macESP43 = "24:0a:c4:00:00:43"
	macLab   = "24:0a:c4:00:01:50"
	discID   = "dddddddd-4444-4444-8444-dddddddddddd"
	macDisc  = "24:0a:c4:00:00:99"
)

// dhcpWorld is the fault fixture with DHCP on in IoT (the second half of 10.10.0.0/24, a lease time of 10 minutes), and in Lab
// when lab is set.
func dhcpWorld(t *testing.T, lab bool) *faultWorld {
	t.Helper()
	w := newFaultWorld(t)
	lease := "10m"
	withDHCP(w.cfg, iotNet, &model.DhcpScope{LeaseTime: &lease, Options: &model.DhcpOptions{Domain: ptr("lab.test"), NtpServers: &[]string{"10.10.0.5"}}})
	if lab {
		withDHCP(w.cfg, labNet, &model.DhcpScope{})
	}
	return w
}

// dhcp writes an overlay of a DHCP action, naming the discovered devices the target may be.
func (w *faultWorld) dhcp(body string, age time.Duration, discovered ...string) model.Overlay {
	w.t.Helper()
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		w.t.Fatalf("%s: %v", body, err)
	}
	norm, errs := domain.ValidateOverlay(w.cfg, req, domain.WithDiscovered(discovered...))
	if len(errs) != 0 {
		w.t.Fatalf("%s: %v", body, errs)
	}
	w.next++
	id := uuid.MustParse("00000000-0000-4000-8000-" + hex12(w.next))
	o, err := domain.NewOverlay(*norm, model.Owner{Type: "user", Id: "admin"}, id, tFault0.Add(age))
	if err != nil {
		w.t.Fatal(err)
	}
	w.overlays = append(w.overlays, o)
	return o
}

// discover makes the device an observed one, which a DHCP action may name.
func (w *faultWorld) discover() {
	w.id.Discovered = []domain.DiscoveredDevice{{ID: discID, MACs: []string{macDisc}, Network: iotNet, IPs: []netip.Addr{netip.MustParseAddr("10.10.0.99")}}}
}

func compiled(t *testing.T, w *faultWorld) *Target {
	t.Helper()
	tg := w.compile(nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if tg.Kea == nil {
		t.Fatal("no DHCP target")
	}
	return tg
}

func subnetOf(tg *Target, network string) kea.Subnet {
	for _, s := range tg.Kea.Config.Subnets {
		if s.Network == network {
			return s
		}
	}
	return kea.Subnet{}
}

func reservationFor(s kea.Subnet, mac string) (kea.Reservation, bool) {
	for _, r := range s.Reservations {
		if strings.EqualFold(r.MAC, mac) {
			return r, true
		}
	}
	return kea.Reservation{}, false
}

func classOf(tg *Target, mac string) (kea.LeaseClass, bool) {
	for _, c := range tg.Kea.Config.LeaseClasses {
		for _, m := range c.MACs {
			if m == mac {
				return c, true
			}
		}
	}
	return kea.LeaseClass{}, false
}

func addrs(as []netip.Addr) string {
	var s []string
	for _, a := range as {
		s = append(s, a.String())
	}
	return strings.Join(s, ",")
}

func TestWithoutDHCPActionsTheConfigurationOfKeaIsTheConfiguredOne(t *testing.T) {
	w := dhcpWorld(t, true)
	base := compiled(t, w)
	if len(base.Kea.Config.LeaseClasses) != 0 || len(base.Kea.Config.Silenced) != 0 || len(base.Kea.Deletions) != 0 {
		t.Fatalf("%+v", base.Kea)
	}
	// overlays of other kinds do not touch it either
	w.overlay(`{target: {device: esp32-42}, fault: {latency: 100ms}}`, 0)
	if got := compiled(t, w); got.Kea.Text != base.Kea.Text {
		t.Error("an overlay that is no DHCP action changed the configuration of Kea")
	}
}

func TestAShortLeaseOfANetworkIsTheLeaseTimeAndTheBoundsOfItsSubnet(t *testing.T) {
	w := dhcpWorld(t, true)
	o := w.dhcp(`{target: {network: IoT}, dhcp: {action: short_lease, lease_time: 7s}}`, 0)
	tg := compiled(t, w)
	s := subnetOf(tg, iotNet)
	if s.LeaseSeconds != 7 || s.LeaseMin != 7 || s.LeaseMax != 7 {
		t.Errorf("the lease time and the bounds a client may ask within: %+v", s)
	}
	if got := subnetOf(tg, labNet); got.LeaseSeconds != 3600 || got.LeaseMin != 0 || got.LeaseMax != 0 {
		t.Errorf("another network is not touched: %+v", got)
	}
	if len(tg.Kea.Config.LeaseClasses) != 0 || len(tg.Kea.Config.Silenced) != 0 {
		t.Errorf("a network's short lease needs no class: %+v", tg.Kea.Config)
	}
	// the preview lists it, it is a winner, and the rendered document carries the bounds
	key := "overlay:" + o.Id.String() + ":dhcp"
	if len(tg.Winners) != 1 || tg.Winners[0] != key || len(tg.Effective) != 1 || tg.Effective[0].Family != "dhcp" || tg.Effective[0].Summary != "dhcp short_lease 7s" || tg.Effective[0].Scope != "network IoT" {
		t.Errorf("%v %+v", tg.Winners, tg.Effective)
	}
	if !strings.Contains(tg.Kea.Text, `"min-valid-lifetime": 7`) || !strings.Contains(tg.Kea.Text, `"max-valid-lifetime": 7`) {
		t.Errorf("%s", tg.Kea.Text)
	}
	// nothing of it is in the kernel
	if len(tg.Faults) != 0 {
		t.Errorf("%+v", tg.Faults)
	}
}

func TestAShortLeaseOfADeviceIsALeaseClassOfItsMACsAndNoOtherClientHasIt(t *testing.T) {
	w := dhcpWorld(t, false)
	two := []string{macESP42, "24:0a:c4:00:00:4A"}
	d := (*w.cfg.Devices)[devESP42]
	d.Identifiers = &model.DeviceIdentifiers{Macs: &two}
	(*w.cfg.Devices)[devESP42] = d
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, 0)
	w.dhcp(`{target: {device: esp32-43}, dhcp: {action: short_lease, lease_time: 5s}}`, time.Second)
	w.dhcp(`{target: {device: lab-host}, dhcp: {action: short_lease, lease_time: 9s}}`, time.Second) // a network without DHCP
	tg := compiled(t, w)
	if s := subnetOf(tg, iotNet); s.LeaseSeconds != 600 || s.LeaseMin != 0 {
		t.Errorf("the subnet keeps its lease time: %+v", s)
	}
	cs := tg.Kea.Config.LeaseClasses
	if len(cs) != 1 || cs[0].Name != "cgw-lease-5-5-5" || cs[0].Seconds != 5 || cs[0].Min != 5 || cs[0].Max != 5 ||
		strings.Join(cs[0].MACs, ",") != "24:0a:c4:00:00:42,24:0a:c4:00:00:43,24:0a:c4:00:00:4a" {
		t.Errorf("the devices with the same time share a class, with all their MACs: %+v", cs)
	}
	if got := problemsOf(tg, CodeDHCPInactive); len(got) != 1 || !strings.Contains(got[0], "device lab-host") || !strings.Contains(got[0], "DHCP is off on the network of the device") {
		t.Errorf("the action on a device of a network without DHCP says it has no effect: %v", got)
	}
}

func problemsOf(tg *Target, code string) (out []string) {
	for _, p := range tg.Problems {
		if p.Code == code {
			out = append(out, p.Message)
		}
	}
	return out
}

func TestSetOptionsReplacesTheFieldsItNamesOfTheNetworksOptions(t *testing.T) {
	w := dhcpWorld(t, false)
	w.dhcp(`{target: {network: IoT}, dhcp: {action: set_options, options: {router: 10.10.0.9, dns_servers: [192.0.2.53, 192.0.2.54], custom: [{code: 224, value: "0a0b"}]}}}`, 0)
	tg := compiled(t, w)
	s := subnetOf(tg, iotNet)
	if s.Router.String() != "10.10.0.9" || addrs(s.DNS) != "192.0.2.53,192.0.2.54" || s.Domain != "lab.test" || addrs(s.NTP) != "10.10.0.5" || len(s.Custom) != 1 || s.Custom[0].Code != 224 {
		t.Errorf("what is not named is the configured one: %+v", s)
	}
	// an empty list sets nothing, like in a scope
	w = dhcpWorld(t, false)
	w.dhcp(`{target: {network: IoT}, dhcp: {action: set_options, options: {dns_servers: []}}}`, 0)
	if s := subnetOf(compiled(t, w), iotNet); addrs(s.DNS) != "10.10.0.1" {
		t.Errorf("the default DNS server stays: %v", s.DNS)
	}
}

func TestSetOptionsOfADeviceIsItsReservationWithTheConfiguredOptionsAndItsOwn(t *testing.T) {
	w := dhcpWorld(t, false)
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: set_options, options: {router: 10.10.0.9, domain: other.test}}}`, 0)
	tg := compiled(t, w)
	s := subnetOf(tg, iotNet)
	r, ok := reservationFor(s, macESP42)
	if !ok || r.IP.String() != "10.10.0.42" || r.Options.Router.String() != "10.10.0.9" || r.Options.Domain != "other.test" ||
		addrs(r.Options.DNS) != "10.10.0.1" || addrs(r.Options.NTP) != "10.10.0.5" {
		t.Errorf("the reservation keeps the fixed address and carries every option: %+v", r)
	}
	if r43, _ := reservationFor(s, macESP43); r43.Options.Router.IsValid() || r43.Options.Domain != "" {
		t.Errorf("another device's reservation is not changed: %+v", r43)
	}
	if s.Router.String() != "10.10.0.1" || s.Domain != "lab.test" {
		t.Errorf("the subnet keeps its options: %+v", s)
	}
	// a device that has no reservation of its own gets one that holds no address
	w = dhcpWorld(t, false)
	w.discover()
	w.dhcp(`{target: {device: `+discID+`}, dhcp: {action: set_options, options: {router: 10.10.0.9}}}`, 0, discID)
	s = subnetOf(compiled(t, w), iotNet)
	if r, ok := reservationFor(s, macDisc); !ok || r.IP.IsValid() || r.Options.Router.String() != "10.10.0.9" {
		t.Errorf("a discovered device: %+v", r)
	}
}

func TestSilenceOfANetworkIsItsInterfaceAndSilenceOfADeviceIsItsMACs(t *testing.T) {
	w := dhcpWorld(t, true)
	w.dhcp(`{target: {network: IoT}, dhcp: {action: silence}}`, 0)
	tg := compiled(t, w)
	if got := tg.Kea.Config.Silenced; len(got) != 1 || got[0].Interface != "br-iot" || len(got[0].Except) != 0 || len(got[0].MACs) != 0 {
		t.Errorf("%+v", got)
	}
	if !strings.Contains(tg.Kea.Text, `"name": "DROP"`) || !strings.Contains(tg.Kea.Text, "pkt.iface == 'br-iot'") {
		t.Errorf("%s", tg.Kea.Text)
	}

	w = dhcpWorld(t, true)
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: silence}}`, 0)
	w.dhcp(`{target: {device: esp32-43}, dhcp: {action: silence}}`, time.Second)
	tg = compiled(t, w)
	if got := tg.Kea.Config.Silenced; len(got) != 1 || got[0].Interface != "" || strings.Join(got[0].MACs, ",") != macESP42+","+macESP43 {
		t.Errorf("%+v", got)
	}
}

func TestForceNewIPMovesTheReservationOfTheDevicesFirstMAC(t *testing.T) {
	w := dhcpWorld(t, false)
	o := w.dhcp(`{target: {device: esp32-42}, dhcp: {action: force_new_ip, ip: 10.10.0.200}}`, 0)
	tg := compiled(t, w)
	s := subnetOf(tg, iotNet)
	if r, ok := reservationFor(s, macESP42); !ok || r.IP.String() != "10.10.0.200" {
		t.Errorf("the overlay's address replaces the fixed address while it lasts: %+v", r)
	}
	if r, _ := reservationFor(s, macESP43); r.IP.String() != "10.10.0.43" {
		t.Errorf("another device's reservation stays: %+v", r)
	}
	if len(tg.Effective) != 1 || tg.Effective[0].Summary != "dhcp force_new_ip 10.10.0.200" || tg.Winners[0] != "overlay:"+o.Id.String()+":dhcp" {
		t.Errorf("%v %+v", tg.Winners, tg.Effective)
	}
	// a device that is only discovered: a reservation made for it
	w = dhcpWorld(t, false)
	w.discover()
	w.dhcp(`{target: {device: `+discID+`}, dhcp: {action: force_new_ip, ip: 10.10.0.201}}`, 0, discID)
	if r, ok := reservationFor(subnetOf(compiled(t, w), iotNet), macDisc); !ok || r.IP.String() != "10.10.0.201" {
		t.Errorf("a discovered device: %+v", r)
	}
	// the reservation goes with the overlay
	w.overlays = nil
	if _, ok := reservationFor(subnetOf(compiled(t, w), iotNet), macDisc); ok {
		t.Error("the reservation of a removed overlay stays")
	}
}

// §2.4: a device has one effective action and the more specific scope wins, so an action of the network does not apply to a
// device with an action of its own: what the network's action changed is put back for it (the configured lease time, the
// configured options, no silence).
func TestAnActionOfADeviceBeatsTheNetworksAndTheDeviceKeepsWhatTheNetworksActionChanged(t *testing.T) {
	t.Run("short lease of the network, set_options of the device", func(t *testing.T) {
		w := dhcpWorld(t, false)
		w.dhcp(`{target: {network: IoT}, dhcp: {action: short_lease, lease_time: 7s}}`, 0)
		w.dhcp(`{target: {device: esp32-42}, dhcp: {action: set_options, options: {router: 10.10.0.9}}}`, time.Second)
		tg := compiled(t, w)
		if c, ok := classOf(tg, macESP42); !ok || c.Seconds != 600 || c.Min != 0 || c.Max != 0 {
			t.Errorf("the device gets the configured lease time: %+v", c)
		}
		if _, ok := classOf(tg, macESP43); ok {
			t.Error("another device is in the class")
		}
		if s := subnetOf(tg, iotNet); s.LeaseSeconds != 7 {
			t.Errorf("the network's short lease stays for the others: %+v", s)
		}
		if len(tg.Winners) != 2 {
			t.Errorf("both win somewhere: %v", tg.Winners)
		}
	})
	t.Run("set_options of the network, short lease of the device", func(t *testing.T) {
		w := dhcpWorld(t, false)
		w.dhcp(`{target: {network: IoT}, dhcp: {action: set_options, options: {router: 10.10.0.9, domain: net.test}}}`, 0)
		w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, time.Second)
		tg := compiled(t, w)
		s := subnetOf(tg, iotNet)
		if s.Router.String() != "10.10.0.9" || s.Domain != "net.test" {
			t.Errorf("the network's options: %+v", s)
		}
		r, ok := reservationFor(s, macESP42)
		if !ok || r.Options.Router.String() != "10.10.0.1" || r.Options.Domain != "lab.test" || r.IP.String() != "10.10.0.42" {
			t.Errorf("the device gets the configured options: %+v", r)
		}
		if c, ok := classOf(tg, macESP42); !ok || c.Seconds != 5 {
			t.Errorf("%+v", c)
		}
	})
	t.Run("set_options of both: the device's are the configured ones with its own, not the network's", func(t *testing.T) {
		w := dhcpWorld(t, false)
		w.dhcp(`{target: {network: IoT}, dhcp: {action: set_options, options: {router: 10.10.0.9, domain: net.test}}}`, 0)
		w.dhcp(`{target: {device: esp32-42}, dhcp: {action: set_options, options: {dns_servers: [192.0.2.53]}}}`, time.Second)
		r, _ := reservationFor(subnetOf(compiled(t, w), iotNet), macESP42)
		if r.Options.Router.String() != "10.10.0.1" || r.Options.Domain != "lab.test" || addrs(r.Options.DNS) != "192.0.2.53" {
			t.Errorf("%+v", r.Options)
		}
	})
	t.Run("silence of the network, another action of the device", func(t *testing.T) {
		w := dhcpWorld(t, false)
		w.dhcp(`{target: {network: IoT}, dhcp: {action: silence}}`, 0)
		w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, time.Second)
		w.dhcp(`{target: {device: esp32-43}, dhcp: {action: silence}}`, time.Second)
		tg := compiled(t, w)
		got := tg.Kea.Config.Silenced
		if len(got) != 2 || got[0].Interface != "br-iot" || strings.Join(got[0].Except, ",") != macESP42 || strings.Join(got[1].MACs, ",") != macESP43 {
			t.Errorf("the device with an action of its own is not silenced, the silent device is: %+v", got)
		}
	})
	t.Run("short lease of both: the device's time", func(t *testing.T) {
		w := dhcpWorld(t, false)
		w.dhcp(`{target: {network: IoT}, dhcp: {action: short_lease, lease_time: 7s}}`, 0)
		w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, time.Second)
		tg := compiled(t, w)
		if c, ok := classOf(tg, macESP42); !ok || c.Seconds != 5 || len(tg.Kea.Config.LeaseClasses) != 1 {
			t.Errorf("%+v", tg.Kea.Config.LeaseClasses)
		}
	})
	t.Run("force_new_ip with a short lease of the network", func(t *testing.T) {
		w := dhcpWorld(t, false)
		w.dhcp(`{target: {network: IoT}, dhcp: {action: short_lease, lease_time: 7s}}`, 0)
		w.dhcp(`{target: {device: esp32-42}, dhcp: {action: force_new_ip, ip: 10.10.0.200}}`, time.Second)
		tg := compiled(t, w)
		if c, ok := classOf(tg, macESP42); !ok || c.Seconds != 600 {
			t.Errorf("the device that is moved gets the configured lease time: %+v", c)
		}
		if r, _ := reservationFor(subnetOf(tg, iotNet), macESP42); r.IP.String() != "10.10.0.200" {
			t.Errorf("%+v", r)
		}
	})
}

func TestTheNewerActionOfAScopeWinsAndTheOlderIsOverridden(t *testing.T) {
	w := dhcpWorld(t, false)
	old := w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 30s}}`, 0)
	newer := w.dhcp(`{target: {device: esp32-42}, dhcp: {action: silence}}`, time.Second)
	netOld := w.dhcp(`{target: {network: IoT}, dhcp: {action: short_lease, lease_time: 7s}}`, 0)
	netNew := w.dhcp(`{target: {network: IoT}, dhcp: {action: short_lease, lease_time: 9s}}`, time.Second)
	tg := compiled(t, w)
	win := map[string]bool{}
	for _, k := range tg.Winners {
		win[k] = true
	}
	for _, o := range []model.Overlay{newer, netNew} {
		if !win["overlay:"+o.Id.String()+":dhcp"] {
			t.Errorf("%s does not win: %v", o.Id, tg.Winners)
		}
	}
	for _, o := range []model.Overlay{old, netOld} {
		if win["overlay:"+o.Id.String()+":dhcp"] {
			t.Errorf("%s wins although a newer action of its scope exists", o.Id)
		}
	}
	if s := subnetOf(tg, iotNet); s.LeaseSeconds != 9 {
		t.Errorf("%+v", s)
	}
	if got := tg.Kea.Config.Silenced; len(got) != 1 || strings.Join(got[0].MACs, ",") != macESP42 {
		t.Errorf("%+v", got)
	}
	if _, ok := classOf(tg, macESP42); ok {
		t.Error("the device that is silenced has no lease class of the overridden short lease")
	}
}

func TestAnActionThatReachesNobodySaysSo(t *testing.T) {
	// no network serves DHCP: no target, and a problem for the action
	w := newFaultWorld(t)
	w.dhcp(`{target: {network: IoT}, dhcp: {action: silence}}`, 0)
	w.dhcp(`{target: {network: IoT}, dhcp: {action: delete_lease}}`, 0)
	tg := w.compile(nil)
	if tg.Kea != nil || tg.HasErrors() {
		t.Fatalf("%+v %+v", tg.Kea, tg.Problems)
	}
	if got := problemsOf(tg, CodeDHCPInactive); len(got) != 2 || !strings.Contains(got[0], "no network serves DHCP") {
		t.Errorf("both actions say they have no effect: %v", got)
	}
	// a network that has DHCP off while another serves it, a device without a MAC, a device that is not known
	w = dhcpWorld(t, false)
	w.dhcp(`{target: {network: Lab}, dhcp: {action: short_lease, lease_time: 5s}}`, 0)
	w.dhcp(`{target: {network: Lab}, dhcp: {action: delete_lease}}`, 0)
	d := (*w.cfg.Devices)[devESP43]
	d.Identifiers = &model.DeviceIdentifiers{}
	(*w.cfg.Devices)[devESP43] = d
	w.dhcp(`{target: {device: esp32-43}, dhcp: {action: silence}}`, 0)
	w.dhcp(`{target: {device: esp32-43}, dhcp: {action: delete_lease}}`, 0)
	tg = compiled(t, w)
	if got := problemsOf(tg, CodeDHCPInactive); len(got) != 4 {
		t.Errorf("each action that changes nothing is a problem: %v", got)
	}
	for _, p := range tg.Problems {
		if p.Code == CodeDHCPInactive && p.Severity != SevWarning {
			t.Errorf("an inactive action is a warning: %+v", p)
		}
	}
	if len(tg.Winners) != 0 || len(tg.Kea.Deletions) != 0 || len(tg.Kea.Config.LeaseClasses) != 0 || len(tg.Kea.Config.Silenced) != 0 {
		t.Errorf("nothing of them is in force: %v %+v", tg.Winners, tg.Kea)
	}
	// the new address of an action the engine did not complete, and one outside the network, is no reservation
	w = dhcpWorld(t, false)
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: force_new_ip}}`, 0)
	got := compiled(t, w)
	if r, _ := reservationFor(subnetOf(got, iotNet), macESP42); len(got.Winners) != 0 || len(problemsOf(got, CodeDHCPInactive)) != 1 || r.IP.String() != "10.10.0.42" {
		t.Errorf("force_new_ip without an address: %v %v", got.Winners, got.Problems)
	}
	ip := "192.0.2.7"
	w.overlays[0].Dhcp.Ip = &ip
	if got := compiled(t, w); len(got.Winners) != 0 || len(problemsOf(got, CodeDHCPInactive)) != 1 {
		t.Errorf("force_new_ip outside the network: %v %v", got.Winners, got.Problems)
	}
}

func TestLeaseDeletionsAreListedOnceForEveryWriteOfTheOverlay(t *testing.T) {
	w := dhcpWorld(t, true)
	net := w.dhcp(`{target: {network: IoT}, dhcp: {action: delete_lease}}`, 0)
	dev := w.dhcp(`{target: {device: esp32-42}, dhcp: {action: delete_lease}}`, time.Second)
	tg := compiled(t, w)
	ds := tg.Kea.Deletions
	if len(ds) != 2 {
		t.Fatalf("%+v", ds)
	}
	iot := subnetOf(tg, iotNet).ID
	if ds[0].Overlay != net.Id.String() || ds[0].Network != iotNet || ds[0].Subnet != iot || ds[0].Device != "" || len(ds[0].MACs) != 0 {
		t.Errorf("the network's: %+v", ds[0])
	}
	if ds[1].Overlay != dev.Id.String() || ds[1].Network != iotNet || ds[1].Subnet != iot || ds[1].Device != devESP42 || strings.Join(ds[1].MACs, ",") != macESP42 {
		t.Errorf("the device's: %+v", ds[1])
	}
	if ds[0].Key == ds[1].Key || ds[0].Key == "" {
		t.Errorf("each has a key of its own: %q %q", ds[0].Key, ds[1].Key)
	}
	// a deletion is no state: it is no fault and no part of the configuration of Kea
	if len(tg.Kea.Config.LeaseClasses) != 0 || len(tg.Kea.Config.Silenced) != 0 || len(tg.Faults) != 0 {
		t.Errorf("%+v", tg.Kea.Config)
	}
	// the same overlay gives the same key at every compile; writing it again (a new time) gives a new one
	if again := compiled(t, w); again.Kea.Deletions[0].Key != ds[0].Key {
		t.Error("the key changes from one compile to the next")
	}
	w.overlays[0].UpdatedAt = w.overlays[0].UpdatedAt.Add(time.Minute)
	keys := map[string]string{}
	for _, d := range compiled(t, w).Kea.Deletions {
		keys[d.Overlay] = d.Key
	}
	if keys[net.Id.String()] == ds[0].Key || keys[dev.Id.String()] != ds[1].Key {
		t.Errorf("a new write of the overlay is a new deletion: %v", keys)
	}
	// overlays that are no state are winners too: the overlay is `effective`
	if len(tg.Winners) != 2 {
		t.Errorf("%v", tg.Winners)
	}
}

func TestTheDHCPActionsOfACompileAreDeterministic(t *testing.T) {
	build := func() string {
		w := dhcpWorld(t, true)
		w.dhcp(`{target: {device: esp32-43}, dhcp: {action: short_lease, lease_time: 5s}}`, 0)
		w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, time.Second)
		w.dhcp(`{target: {network: IoT}, dhcp: {action: silence}}`, 2*time.Second)
		w.dhcp(`{target: {network: Lab}, dhcp: {action: set_options, options: {domain: x.test}}}`, 3*time.Second)
		return compiled(t, w).Kea.Text
	}
	a := build()
	for i := 0; i < 5; i++ {
		if b := build(); a != b {
			t.Fatalf("the document differs between two compiles:\n%s\n%s", a, b)
		}
	}
}

// What the compile makes of the actions is what Kea's own check accepts (the interface is the loopback: Kea refuses an
// interface the machine does not have, and the hook script is the container's).
func TestKeaAcceptsTheConfigurationTheDHCPActionsCompileTo(t *testing.T) {
	bin, err := exec.LookPath("kea-dhcp4")
	if err != nil {
		t.Skip("kea-dhcp4 is not installed")
	}
	w := dhcpWorld(t, true)
	w.discover()
	w.dhcp(`{target: {network: IoT}, dhcp: {action: silence}}`, 0)
	w.dhcp(`{target: {network: Lab}, dhcp: {action: short_lease, lease_time: 5s}}`, 0)
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 9s}}`, time.Second)
	w.dhcp(`{target: {device: esp32-43}, dhcp: {action: force_new_ip, ip: 10.10.0.200}}`, time.Second)
	w.dhcp(`{target: {device: `+discID+`}, dhcp: {action: set_options, options: {router: 10.10.0.9, custom: [{code: 224, value: "0a0b"}]}}}`, time.Second, discID)
	tg := compiled(t, w)
	var doc map[string]any
	if err := json.Unmarshal([]byte(tg.Kea.Text), &doc); err != nil {
		t.Fatal(err)
	}
	d4 := doc["Dhcp4"].(map[string]any)
	delete(d4, "hooks-libraries")
	for _, s := range d4["subnet4"].([]any) {
		s.(map[string]any)["interface"] = "lo"
	}
	d4["interfaces-config"].(map[string]any)["interfaces"] = []any{"lo"}
	raw, _ := json.Marshal(doc)
	f := filepath.Join(t.TempDir(), "kea.json")
	if err := os.WriteFile(f, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "-t", f).CombinedOutput(); err != nil {
		t.Fatalf("kea-dhcp4 -t rejects the configuration: %v\n%s\n%s", err, out, raw)
	}
}

// P5-AUDIT-07: the network a configured device names is informational except for a fixed address, so a device configured by its
// MAC only is in the network it is seen in (the network its current addresses lie in, the newest lease first), and its DHCP
// actions act there like those of a device that names the network.
func TestADHCPActionOfAConfiguredDeviceWithoutANetworkActsInTheNetworkItIsSeenIn(t *testing.T) {
	world := func(t *testing.T, seen ...string) *faultWorld {
		w := dhcpWorld(t, true)
		d := (*w.cfg.Devices)[devESP42]
		d.Network, d.FixedIp = nil, nil
		(*w.cfg.Devices)[devESP42] = d
		w.setAddrs(map[string][]string{devESP42: seen, devESP43: {"10.10.0.43"}})
		return w
	}
	w := world(t, "10.10.0.150")
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, 0)
	w.dhcp(`{target: {device: esp32-43}, dhcp: {action: silence}}`, 0)
	tg := compiled(t, w)
	if c, ok := classOf(tg, macESP42); !ok || c.Seconds != 5 {
		t.Errorf("short_lease: no lease class for the device: %+v", tg.Kea.Config.LeaseClasses)
	}
	if got := problemsOf(tg, CodeDHCPInactive); len(got) != 0 {
		t.Errorf("the actions of the device say they have no effect: %v", got)
	}

	w = world(t, "10.10.0.150")
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: silence}}`, 0)
	if got := compiled(t, w).Kea.Config.Silenced; len(got) != 1 || strings.Join(got[0].MACs, ",") != macESP42 {
		t.Errorf("silence: the device's MAC is not in DROP: %+v", got)
	}

	w = world(t, "10.10.0.150")
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: set_options, options: {domain: x.test}}}`, 0)
	if r, ok := reservationFor(subnetOf(compiled(t, w), iotNet), macESP42); !ok || r.Options.Domain != "x.test" {
		t.Errorf("set_options: no reservation with the options: %+v", r)
	}

	w = world(t, "10.10.0.150")
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: force_new_ip, ip: 10.10.0.200}}`, 0)
	if r, ok := reservationFor(subnetOf(compiled(t, w), iotNet), macESP42); !ok || r.IP.String() != "10.10.0.200" {
		t.Errorf("force_new_ip: the reservation is not moved: %+v", r)
	}

	w = world(t, "10.10.0.150")
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: delete_lease}}`, 0)
	tg = compiled(t, w)
	if ds := tg.Kea.Deletions; len(ds) != 1 || ds[0].Network != iotNet || ds[0].Subnet != subnetOf(tg, iotNet).ID || strings.Join(ds[0].MACs, ",") != macESP42 {
		t.Errorf("delete_lease: %+v", ds)
	}

	// seen in Lab, the device's actions act in Lab; not seen at all, it is in no network and says so
	w = world(t, "10.20.0.77")
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, 0)
	tg = compiled(t, w)
	if c, ok := classOf(tg, macESP42); !ok || c.Seconds != 5 {
		t.Errorf("seen in Lab: %+v", tg.Kea.Config.LeaseClasses)
	}
	w = world(t)
	w.dhcp(`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 5s}}`, 0)
	tg = compiled(t, w)
	if got := problemsOf(tg, CodeDHCPInactive); len(got) != 1 || len(tg.Kea.Config.LeaseClasses) != 0 {
		t.Errorf("a device that is seen nowhere: %v %+v", got, tg.Kea.Config.LeaseClasses)
	}
}
