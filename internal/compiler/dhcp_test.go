package compiler

import (
	"encoding/json"
	"net/netip"
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
	// without a MAC there is nothing to reserve for: a warning, not an error
	tg = compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{})
		addDevice(c, devA, model.Device{Name: "esp32-42", FixedIp: &ip})
	})
	if tg.HasErrors() || len(tg.Kea.Config.Subnets[0].Reservations) != 0 || len(tg.Problems) != 1 || tg.Problems[0].Code != CodeDHCP {
		t.Errorf("%+v", tg.Problems)
	}
	// a fixed address outside every network with DHCP
	far := "172.30.0.5"
	tg = compileBasic(t, func(c *model.Configuration, _ *Host) {
		withDHCP(c, iotNet, &model.DhcpScope{})
		addDevice(c, devA, model.Device{Name: "far", FixedIp: &far, Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
	})
	if tg.HasErrors() || len(tg.Kea.Config.Subnets[0].Reservations) != 0 || len(tg.Problems) == 0 {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestEveryDeviceHasASetOfItsAddresses(t *testing.T) {
	mac := []string{"02:00:00:00:00:31"}
	id := &domain.Identity{
		Addresses:  map[string][]netip.Addr{devA: {netip.MustParseAddr("10.10.0.31"), netip.MustParseAddr("10.10.0.32")}},
		Discovered: []domain.DiscoveredDevice{{ID: "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb", IPs: []netip.Addr{netip.MustParseAddr("10.10.0.99")}}},
	}
	cfg := loadConfig(t, "gateway.yaml")
	addDevice(cfg, devA, model.Device{Name: "esp32-42", Identifiers: &model.DeviceIdentifiers{Macs: &mac}})
	tg := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}, Identity: id})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if len(tg.DeviceSets) != 2 {
		t.Fatalf("%v", tg.DeviceSets)
	}
	elems := map[string][]string{}
	for _, s := range tg.Nft.Sets {
		elems[s.Name] = s.Elements
	}
	a, d := tg.DeviceSets[devA], tg.DeviceSets["bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"]
	if !strings.HasPrefix(a, "dev_aaaaaaaa_") || strings.Join(elems[a], ",") != "10.10.0.31,10.10.0.32" || strings.Join(elems[d], ",") != "10.10.0.99" {
		t.Errorf("%v %v", tg.DeviceSets, elems)
	}
	// without observed state the sets exist and are empty; the name does not depend on the content
	tg2 := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 2}})
	if tg2.DeviceSets[devA] != a || len(elems[a]) == 0 {
		t.Errorf("%v", tg2.DeviceSets)
	}
	for _, s := range tg2.Nft.Sets {
		if s.Name == a && len(s.Elements) != 0 {
			t.Errorf("%v", s.Elements)
		}
	}
	// the transaction has the sets, and nft's parser accepts them (nftsyntax_test runs the full check)
	tx, err := tg.Nft.Transaction(nil)
	if err != nil || !strings.Contains(string(tx), a) {
		t.Errorf("%v", err)
	}
	var doc any
	if err := json.Unmarshal(tx, &doc); err != nil {
		t.Fatal(err)
	}
}
