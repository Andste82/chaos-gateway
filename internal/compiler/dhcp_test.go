package compiler

import (
	"encoding/json"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	iotNet = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	labNet = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"
	devA   = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
)

func withDHCP(cfg *model.Configuration, id string, scope *model.DhcpScope) {
	n := (*cfg.Networks)[id]
	lan, _ := n.AsLanNetwork()
	lan.Dhcp = scope
	_ = n.FromLanNetwork(lan)
	(*cfg.Networks)[id] = n
}

func addDevice(cfg *model.Configuration, id string, d model.Device) {
	if cfg.Devices == nil {
		cfg.Devices = &map[string]model.Device{}
	}
	(*cfg.Devices)[id] = d
}

func TestADhcpScopeBecomesAKeaSubnetOnTheBridge(t *testing.T) {
	tg := compileBasic(t, func(c *model.Configuration, _ *Host) { withDHCP(c, iotNet, &model.DhcpScope{}) })
	if tg.HasErrors() || tg.Kea == nil {
		t.Fatalf("%+v %+v", tg.Problems, tg.Kea)
	}
	if len(tg.Kea.Config.Subnets) != 1 {
		t.Fatalf("%+v", tg.Kea.Config.Subnets)
	}
	s := tg.Kea.Config.Subnets[0]
	if s.Network != iotNet || s.Interface != "br-iot" || s.Subnet.String() != "10.10.0.0/24" || s.LeaseSeconds != 3600 {
		t.Errorf("%+v", s)
	}
	// defaults: the second half of the subnet, the gateway as router and DNS
	if len(s.Pools) != 1 || s.Pools[0].Start.String() != "10.10.0.128" || s.Pools[0].End.String() != "10.10.0.254" || s.Router.String() != "10.10.0.1" || len(s.DNS) != 1 || s.DNS[0].String() != "10.10.0.1" {
		t.Errorf("%+v", s)
	}
	if tg.Kea.Networks[s.ID] != iotNet {
		t.Errorf("%v", tg.Kea.Networks)
	}
	if !strings.Contains(tg.Kea.Text, `"subnet": "10.10.0.0/24"`) || !strings.Contains(tg.Kea.Text, "libdhcp_run_script.so") {
		t.Errorf("%s", tg.Kea.Text)
	}
	// the subnet id does not depend on what else is configured
	again := compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{})
		withDHCP(c, labNet, &model.DhcpScope{})
	})
	if len(again.Kea.Config.Subnets) != 2 {
		t.Fatalf("%d subnets", len(again.Kea.Config.Subnets))
	}
	var iot int
	for id, n := range again.Kea.Networks {
		if n == iotNet {
			iot = id
		}
	}
	if iot != s.ID {
		t.Errorf("the subnet id of IoT changed from %d to %d when another network got DHCP", s.ID, iot)
	}
}

func TestDhcpOffMeansNoSubnet(t *testing.T) {
	off := false
	for name, scope := range map[string]*model.DhcpScope{"absent": nil, "disabled": {Enabled: &off}} {
		tg := compileBasic(t, func(c *model.Configuration, _ *Host) { withDHCP(c, iotNet, scope) })
		if tg.Kea != nil {
			t.Errorf("%s: %+v", name, tg.Kea)
		}
	}
	// one network on, one off: only the one that is on
	tg := compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{})
		withDHCP(c, labNet, &model.DhcpScope{Enabled: &off})
	})
	if tg.Kea == nil || len(tg.Kea.Config.Subnets) != 1 || tg.Kea.Config.Subnets[0].Network != iotNet {
		t.Errorf("%+v", tg.Kea)
	}
}

func TestScopeSettingsReachTheSubnet(t *testing.T) {
	lease := "30s"
	router, dom := "10.10.0.2", "lab.test"
	dns := []string{"9.9.9.9"}
	ntp := []string{"10.10.0.1"}
	tg := compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{LeaseTime: &lease,
			Pools:   &[]model.DhcpPool{{Start: "10.10.0.50", End: "10.10.0.60"}},
			Options: &model.DhcpOptions{Router: &router, Domain: &dom, DnsServers: &dns, NtpServers: &ntp, Custom: &[]model.DhcpCustomOption{{Code: 66, Value: "tftp.lab"}}}})
	})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	s := tg.Kea.Config.Subnets[0]
	if s.LeaseSeconds != 30 || s.Pools[0].Start.String() != "10.10.0.50" || s.Router.String() != "10.10.0.2" || s.DNS[0].String() != "9.9.9.9" ||
		s.NTP[0].String() != "10.10.0.1" || s.Domain != "lab.test" || len(s.Custom) != 1 || s.Custom[0].Code != 66 {
		t.Errorf("%+v", s)
	}
	bad := "soon"
	tg = compileBasic(t, func(c *model.Configuration, _ *Host) { withDHCP(c, iotNet, &model.DhcpScope{LeaseTime: &bad}) })
	if !tg.HasErrors() || tg.Errors()[0].Code != CodeDHCP {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestReservationsComeFromDevicesWithAFixedAddress(t *testing.T) {
	ip := "10.10.0.31"
	mac := []string{"02:00:00:00:00:31", "02:00:00:00:00:32"}
	tg := compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{})
		net := iotNet
		addDevice(c, devA, model.Device{Name: "esp32-42", FixedIp: &ip, Network: &net, Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
	})
	r := tg.Kea.Config.Subnets[0].Reservations
	if tg.HasErrors() || len(r) != 1 || r[0].MAC != "02:00:00:00:00:31" || r[0].IP.String() != "10.10.0.31" {
		t.Fatalf("%+v %+v", r, tg.Problems)
	}
	// DHCP off in the device's network while another network serves DHCP: no reservation and no
	// problem of the compile (the validation warns, fixed_ip_dhcp_off, #94)
	off := false
	tg = compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{Enabled: &off})
		withDHCP(c, labNet, &model.DhcpScope{})
		net := iotNet
		addDevice(c, devA, model.Device{Name: "esp32-42", FixedIp: &ip, Network: &net, Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
	})
	if len(tg.Problems) != 0 {
		t.Errorf("%+v", tg.Problems)
	}
	if len(tg.Kea.Config.Subnets) != 1 || len(tg.Kea.Config.Subnets[0].Reservations) != 0 {
		t.Errorf("%+v", tg.Kea.Config.Subnets)
	}
	// two networks with DHCP: each reservation lands in the subnet of its device's network, and the
	// MAC is written in lower case
	labIP, labMAC := "10.20.0.31", []string{"02:00:00:00:00:4A"}
	tg = compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{})
		withDHCP(c, labNet, &model.DhcpScope{})
		iot, lab := iotNet, labNet
		addDevice(c, devA, model.Device{Name: "esp32-42", FixedIp: &ip, Network: &iot, Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
		addDevice(c, "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb", model.Device{Name: "printer", FixedIp: &labIP, Network: &lab, Identifiers: &model.DeviceIdentifiers{Macs: &labMAC}})
	})
	if tg.HasErrors() || len(tg.Kea.Config.Subnets) != 2 {
		t.Fatalf("%+v %+v", tg.Kea.Config.Subnets, tg.Problems)
	}
	got := map[string]string{}
	for _, s := range tg.Kea.Config.Subnets {
		for _, r := range s.Reservations {
			got[s.Network] += r.MAC + "=" + r.IP.String()
		}
	}
	if got[iotNet] != "02:00:00:00:00:31=10.10.0.31" || got[labNet] != "02:00:00:00:00:4a=10.20.0.31" {
		t.Errorf("%v", got)
	}
}

// identityPairs returns the compiled identity map's key→value pairs (plan §3.3).
func identityPairs(tg *Target) map[string]string {
	out := map[string]string{}
	for _, m := range tg.Nft.Maps {
		if m.Name == tg.IdentityMap {
			for _, e := range m.Elements {
				out[e.Key] = e.Value
			}
		}
	}
	return out
}

func TestEveryDeviceHasAnEntryInTheIdentityMap(t *testing.T) {
	mac := []string{"02:00:00:00:00:31"}
	discID := "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	id := &domain.Identity{
		Addresses: map[string][]netip.Addr{
			devA:   {netip.MustParseAddr("10.10.0.31"), netip.MustParseAddr("10.10.0.32")},
			discID: {netip.MustParseAddr("10.10.0.99")},
		},
		Discovered: []domain.DiscoveredDevice{{ID: discID, IPs: []netip.Addr{netip.MustParseAddr("10.10.0.99")}}},
	}
	cfg := loadConfig(t, "gateway.yaml")
	addDevice(cfg, devA, model.Device{Name: "esp32-42", Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
	tg := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}, Identity: id})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.DeviceNums) != 2 {
		t.Fatalf("%v", tg.DeviceNums)
	}
	if tg.IdentityMap == "" || !strings.HasPrefix(tg.IdentityMap, "ident4_") {
		t.Fatalf("identity map name %q", tg.IdentityMap)
	}
	pairs := identityPairs(tg)
	numA := strconv.Itoa(tg.DeviceNums[devA])
	numD := strconv.Itoa(tg.DeviceNums[discID])
	if pairs["10.10.0.31"] != numA || pairs["10.10.0.32"] != numA || pairs["10.10.0.99"] != numD {
		t.Errorf("%v (devA=%s, disc=%s)", pairs, numA, numD)
	}
	// without observed state the map exists and is empty; its name and the device numbers do not
	// depend on the content
	tg2 := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 2}})
	if tg2.IdentityMap != tg.IdentityMap || tg2.DeviceNums[devA] != tg.DeviceNums[devA] {
		t.Errorf("%v %v", tg2.IdentityMap, tg2.DeviceNums)
	}
	if len(identityPairs(tg2)) != 0 {
		t.Errorf("%v", identityPairs(tg2))
	}
	// the transaction has the map, and nft's parser accepts it (nftsyntax_test runs the full check)
	tx, err := tg.Nft.Transaction(nil)
	if err != nil || !strings.Contains(string(tx), tg.IdentityMap) {
		t.Errorf("%v", err)
	}
	var doc any
	if err := json.Unmarshal(tx, &doc); err != nil {
		t.Fatal(err)
	}
}

// M6a-17 test: a discovered device's sighted address is not automatically mapped to it; only the
// resolved identity's claim is, so an address another device has already won (the stronger claim,
// plan §2.3) does not leak into the discovered device's own entries too.
func TestAnAddressIsInTheIdentityMapOnce(t *testing.T) {
	mac := []string{"02:00:00:00:00:31"}
	discID := "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	id := &domain.Identity{
		// devA's claim on 10.10.0.50 won; the discovered entry was seen with it too (a stale
		// sighting, or the losing side of the claim), but its own resolved address list is empty.
		Addresses:  map[string][]netip.Addr{devA: {netip.MustParseAddr("10.10.0.50")}},
		Discovered: []domain.DiscoveredDevice{{ID: discID, IPs: []netip.Addr{netip.MustParseAddr("10.10.0.50")}}},
	}
	cfg := loadConfig(t, "gateway.yaml")
	addDevice(cfg, devA, model.Device{Name: "esp32-42", Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
	tg := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}, Identity: id})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	pairs := identityPairs(tg)
	if pairs["10.10.0.50"] != strconv.Itoa(tg.DeviceNums[devA]) {
		t.Errorf("10.10.0.50: %v, want devA's number", pairs)
	}
	if len(pairs) != 1 {
		t.Errorf("the discovered device's sighting should not add its own entry (it lost to devA): %v", pairs)
	}
}

// P2-AUDIT-01: a device keeps its number from one compile to the next when the previous allocation is
// fed back (Input.DeviceNums, DeviceNumHigh), so a new device, even one whose id sorts before every
// other, and a device that went change nothing but elements of the identity map: the map's name, the
// other devices' numbers and every other part of the nft layout stay. A number that was handed out is
// not given to another device in the same line of compiles.
func TestADeviceKeepsItsNumberWhenAnotherOneAppearsOrGoes(t *testing.T) {
	mac := []string{"02:00:00:00:00:31"}
	cfg := loadConfig(t, "gateway.yaml")
	addDevice(cfg, devA, model.Device{Name: "esp32-42", Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
	disc := func(id, ip string) domain.DiscoveredDevice {
		return domain.DiscoveredDevice{ID: id, IPs: []netip.Addr{netip.MustParseAddr(ip)}}
	}
	identity := func(ds ...domain.DiscoveredDevice) *domain.Identity {
		id := &domain.Identity{Addresses: map[string][]netip.Addr{devA: {netip.MustParseAddr("10.10.0.31")}}, Discovered: ds}
		for _, d := range ds {
			id.Addresses[d.ID] = d.IPs
		}
		return id
	}
	// ids around devA's in sort order: the first new one sorts before it, which renumbered devA before
	early, late := "00000000-0000-4000-8000-000000000001", "ffffffff-0000-4000-8000-000000000001"
	compile := func(prev *Target, id *domain.Identity) *Target {
		in := Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}, Identity: id}
		if prev != nil {
			in.DeviceNums, in.DeviceNumHigh = prev.DeviceNums, prev.DeviceNumHigh
		}
		tg := Compile(in)
		if tg.HasErrors() {
			t.Fatalf("%+v", tg.Problems)
		}
		return tg
	}
	layout := func(tg *Target) string {
		n := tg.Nft
		n.Generation = ""
		n.Maps = append([]MapDef(nil), n.Maps...)
		for i := range n.Maps {
			n.Maps[i].Elements = nil
		}
		b, _ := json.Marshal(n)
		return string(b)
	}

	t1 := compile(nil, identity(disc(late, "10.10.0.60")))
	if t1.DeviceNums[devA] != 1 || t1.DeviceNums[late] != 2 || t1.DeviceNumHigh != 2 {
		t.Fatalf("the first compile numbers in the order of the ids: %v, high %d", t1.DeviceNums, t1.DeviceNumHigh)
	}
	t2 := compile(t1, identity(disc(late, "10.10.0.60"), disc(early, "10.10.0.70")))
	if t2.DeviceNums[devA] != 1 || t2.DeviceNums[late] != 2 || t2.DeviceNums[early] != 3 || t2.DeviceNumHigh != 3 {
		t.Fatalf("a new device renumbered the others: %v, high %d", t2.DeviceNums, t2.DeviceNumHigh)
	}
	if layout(t2) != layout(t1) {
		t.Errorf("a new device changed more than map elements:\n%s\n%s", layout(t1), layout(t2))
	}
	if p := identityPairs(t2); len(p) != 3 || p["10.10.0.70"] != "3" || p["10.10.0.31"] != "1" || p["10.10.0.60"] != "2" {
		t.Errorf("%v", p)
	}
	// late goes, and a third device appears: it does not take late's number
	other := "cccccccc-0000-4000-8000-000000000001"
	t3 := compile(t2, identity(disc(early, "10.10.0.70"), disc(other, "10.10.0.80")))
	if _, still := t3.DeviceNums[late]; still || t3.DeviceNums[devA] != 1 || t3.DeviceNums[early] != 3 || t3.DeviceNums[other] != 4 {
		t.Fatalf("%v", t3.DeviceNums)
	}
	if layout(t3) != layout(t1) {
		t.Errorf("a device that went changed more than map elements")
	}
	// the highest number is carried even when the device that had it went
	t4 := compile(t3, identity(disc(early, "10.10.0.70")))
	t5 := compile(t4, identity(disc(early, "10.10.0.70"), disc(late, "10.10.0.60")))
	if t4.DeviceNumHigh != 4 || t5.DeviceNums[late] != 5 {
		t.Errorf("high %d, the returning device got %d", t4.DeviceNumHigh, t5.DeviceNums[late])
	}
	// no device at all: the map is there, empty, under the same name
	t6 := Compile(Input{Config: loadConfig(t, "gateway.yaml"), Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}})
	if t6.IdentityMap != t1.IdentityMap || len(identityPairs(t6)) != 0 || !hasMap(t6, t6.IdentityMap) {
		t.Errorf("without devices: map %q, pairs %v", t6.IdentityMap, identityPairs(t6))
	}
}

func hasMap(tg *Target, name string) bool {
	for _, m := range tg.Nft.Maps {
		if m.Name == name {
			return true
		}
	}
	return false
}

// Once the numbers are used up, a new device takes the lowest free one, one the previous allocation
// did not hold if there is any; a number out of range in the previous allocation is not kept.
func TestDeviceNumbersAreReusedOnlyWhenNoneIsLeftAbove(t *testing.T) {
	nums, high := assignDeviceNums([]string{"a", "b", "c"}, map[string]int{"a": MaxDeviceNum, "b": 1, "gone": 2}, MaxDeviceNum)
	if nums["a"] != MaxDeviceNum || nums["b"] != 1 || nums["c"] != 3 || high != MaxDeviceNum {
		t.Errorf("%v, high %d", nums, high)
	}
	nums, high = assignDeviceNums([]string{"a", "b"}, map[string]int{"a": -4, "b": MaxDeviceNum + 1}, 0)
	if nums["a"] != 1 || nums["b"] != 2 || high != 2 {
		t.Errorf("%v, high %d", nums, high)
	}
	// two devices that claim the same number: the first id keeps it
	nums, _ = assignDeviceNums([]string{"b", "a"}, map[string]int{"a": 7, "b": 7}, 7)
	if nums["a"] != 7 || nums["b"] != 8 {
		t.Errorf("%v", nums)
	}
}
