package kea

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleConfig() Config {
	return Config{Script: HookScript, Subnets: []Subnet{{
		ID: 7, Network: "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", Subnet: netip.MustParsePrefix("10.10.0.1/24"), Interface: "br-iot",
		Pools:        []Pool{{netip.MustParseAddr("10.10.0.128"), netip.MustParseAddr("10.10.0.254")}},
		LeaseSeconds: 600, Router: netip.MustParseAddr("10.10.0.1"), DNS: []netip.Addr{netip.MustParseAddr("10.10.0.1")},
		NTP: []netip.Addr{netip.MustParseAddr("10.10.0.1")}, Domain: "lab.test",
		Reservations: []Reservation{{MAC: "02:00:00:00:00:31", IP: netip.MustParseAddr("10.10.0.31"), Hostname: "esp32-42"}},
	}}}
}

func TestRenderIsDeterministicJSONWithOneSubnetPerNetwork(t *testing.T) {
	a, err := sampleConfig().Render()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := sampleConfig().Render()
	if a != b {
		t.Error("the rendering is not deterministic")
	}
	var doc struct {
		Dhcp4 struct {
			Interfaces struct{ Interfaces []string } `json:"interfaces-config"`
			Subnet4    []map[string]any              `json:"subnet4"`
			Hooks      []map[string]any              `json:"hooks-libraries"`
		} `json:"Dhcp4"`
	}
	if err := json.Unmarshal([]byte(a), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Dhcp4.Subnet4) != 1 || doc.Dhcp4.Subnet4[0]["subnet"] != "10.10.0.0/24" || doc.Dhcp4.Subnet4[0]["interface"] != "br-iot" || doc.Dhcp4.Subnet4[0]["id"] != float64(7) {
		t.Errorf("%v", doc.Dhcp4.Subnet4)
	}
	if strings.Join(doc.Dhcp4.Interfaces.Interfaces, ",") != "br-iot" {
		t.Errorf("%v", doc.Dhcp4.Interfaces)
	}
	for _, want := range []string{`"pool": "10.10.0.128 - 10.10.0.254"`, `"hw-address": "02:00:00:00:00:31"`, `"ip-address": "10.10.0.31"`, `"name": "routers"`, `"data": "10.10.0.1"`, `"valid-lifetime": 600`, "libdhcp_run_script.so", HookScript} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %s in\n%s", want, a)
		}
	}
	// without a script the hook is not loaded; without subnets Kea listens on nothing
	c := sampleConfig()
	c.Script, c.Subnets = "", nil
	empty, err := c.Render()
	if err != nil || strings.Contains(empty, "run_script") || !strings.Contains(empty, `"interfaces": []`) {
		t.Errorf("%v\n%s", err, empty)
	}
}

// #64: Kea opens its sockets only while it is configured and skips an interface that is down or
// has no carrier; the configuration lets it try again on its own for a while.
func TestTheConfigurationLetsKeaRetryTheSocketsItCouldNotOpen(t *testing.T) {
	doc, err := sampleConfig().Document()
	if err != nil {
		t.Fatal(err)
	}
	ic := doc["Dhcp4"].(jsonMap)["interfaces-config"].(jsonMap)
	if ic["service-sockets-max-retries"] != SocketRetries || ic["service-sockets-retry-wait-time"] != int64(5000) || ic["re-detect"] != true {
		t.Errorf("%v", ic)
	}
	if SocketRetries*SocketRetryWait < time.Minute {
		t.Errorf("Kea gives up after %v", SocketRetries*SocketRetryWait)
	}
}

func TestChecksRefuseWhatKeaWouldRefuse(t *testing.T) {
	mod := func(f func(*Subnet)) error {
		c := sampleConfig()
		f(&c.Subnets[0])
		_, err := c.Render()
		return err
	}
	for name, f := range map[string]func(*Subnet){
		"a pool outside the subnet": func(s *Subnet) { s.Pools[0].End = netip.MustParseAddr("10.20.0.5") },
		"an inverted pool":          func(s *Subnet) { s.Pools[0].Start, s.Pools[0].End = s.Pools[0].End, s.Pools[0].Start },
		"no pool":                   func(s *Subnet) { s.Pools = nil },
		"no interface":              func(s *Subnet) { s.Interface = "" },
		"no lease time":             func(s *Subnet) { s.LeaseSeconds = 0 },
		"a reservation outside":     func(s *Subnet) { s.Reservations[0].IP = netip.MustParseAddr("10.99.0.1") },
		"id 0":                      func(s *Subnet) { s.ID = 0 },
	} {
		if err := mod(f); err == nil {
			t.Errorf("%s is accepted", name)
		}
	}
	c := sampleConfig()
	c.Subnets = append(c.Subnets, c.Subnets[0])
	if _, err := c.Render(); err == nil {
		t.Error("two subnets with one id")
	}
}

func TestSubnetIDsAreStableAndDoNotCollide(t *testing.T) {
	a := SubnetID("0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", nil)
	if a < 1 || SubnetID("0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", map[int]bool{}) != a {
		t.Fatalf("%d", a)
	}
	if b := SubnetID("0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", map[int]bool{a: true}); b == a || b < 1 {
		t.Errorf("a collision gives the same id: %d", b)
	}
	if SubnetID("1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32", nil) == a {
		t.Error("two networks share an id")
	}
}

// poolsText is "a - b, c - d" for a list of pools ("" for none).
func poolsText(ps []Pool) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = p.Start.String() + " - " + p.End.String()
	}
	return strings.Join(s, ", ")
}

func TestDefaultPoolIsTheSecondHalf(t *testing.T) {
	gw := func(a string) (netip.Prefix, netip.Addr) {
		p := netip.MustParsePrefix(a)
		return p, p.Addr()
	}
	for _, c := range []struct{ address, want string }{
		{"10.10.0.1/24", "10.10.0.128 - 10.10.0.254"},
		// the gateway at the start of the second half is skipped
		{"10.10.0.128/24", "10.10.0.129 - 10.10.0.254"},
		// a gateway inside the second half splits it around its address (#110)
		{"10.10.0.200/24", "10.10.0.128 - 10.10.0.199, 10.10.0.201 - 10.10.0.254"},
		// a gateway on the last usable address ends the pool before it
		{"10.10.0.254/24", "10.10.0.128 - 10.10.0.253"},
		{"192.168.1.1/29", "192.168.1.4 - 192.168.1.6"},
		{"192.168.1.5/29", "192.168.1.4 - 192.168.1.4, 192.168.1.6 - 192.168.1.6"},
		// a /30 has no useful pool
		{"10.10.0.1/30", ""},
	} {
		if got := poolsText(DefaultPool(gw(c.address))); got != c.want {
			t.Errorf("%s: %q, want %q", c.address, got, c.want)
		}
	}
}

// defaultPoolCase is a row of testdata/default_pool.json: a network address (the gateway's own address with
// the prefix length), where the gateway lies in the subnet, and the default pools (empty: none). The web
// app's effectivePools (web/src/lib/networks.test.ts) runs the same table, so the browser names the pools
// the gateway hands out (#95, #110).
type defaultPoolCase struct {
	Address   string `json:"address"`
	GatewayAt string `json:"gateway_at"`
	Pools     []struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"pools"`
}

func TestDefaultPoolForEverySubnetSizeAndGatewayPosition(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "default_pool.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []defaultPoolCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	sizes := map[int]bool{}
	for _, c := range cases {
		p := netip.MustParsePrefix(c.Address)
		sizes[p.Bits()] = true
		got := DefaultPool(p, p.Addr())
		want := make([]string, len(c.Pools))
		for i, w := range c.Pools {
			want[i] = w.Start + " - " + w.End
		}
		if poolsText(got) != strings.Join(want, ", ") {
			t.Errorf("%s (gateway at the %s): pools %q, want %q", c.Address, c.GatewayAt, poolsText(got), strings.Join(want, ", "))
		}
		for _, q := range got {
			if q.Start.Compare(p.Addr()) <= 0 && p.Addr().Compare(q.End) <= 0 {
				t.Errorf("%s (gateway at the %s): the pool %s - %s holds the gateway's address", c.Address, c.GatewayAt, q.Start, q.End)
			}
		}
	}
	for bits := 16; bits <= 30; bits++ {
		if !sizes[bits] {
			t.Errorf("the table has no /%d", bits)
		}
	}
}

func TestTheHookEnvironmentBecomesAnEvent(t *testing.T) {
	env := map[string]string{"KEA_LEASE4_ADDRESS": "10.10.0.150", "KEA_LEASE4_HWADDR": "02:00:00:00:00:AA", "KEA_LEASE4_HOSTNAME": "esp32", "KEA_SUBNET_ID": "7", "KEA_LEASE4_VALID_LIFETIME": "600", "KEA_LEASE4_CLIENT_ID": "01:02"}
	ev, err := EventFromHook("lease4_renew", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if ev.Name != "renew" || ev.IP.String() != "10.10.0.150" || ev.MAC != "02:00:00:00:00:aa" || ev.SubnetID != 7 || ev.ValidLifetime != 600 || ev.Hostname != "esp32" {
		t.Errorf("%+v", ev)
	}
	get := func(k string) string { return env[k] }
	if _, err := EventFromHook("lease4_bogus", get); err == nil {
		t.Error("an unknown hook point")
	}
	delete(env, "KEA_LEASE4_ADDRESS")
	if _, err := EventFromHook("lease4_select", get); err == nil {
		t.Error("an event without an address")
	}
}

func TestKeaAcceptsTheRenderedConfiguration(t *testing.T) {
	bin, err := exec.LookPath("kea-dhcp4")
	if err != nil {
		t.Skip("kea-dhcp4 is not installed")
	}
	dir := t.TempDir()
	cfg := sampleConfig()
	cfg.Subnets[0].Interface = "lo" // Kea refuses interfaces the machine does not have
	cfg.Script = ""                 // the hook script is the container's
	text, err := cfg.Render()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "kea.json")
	if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-t", f).CombinedOutput()
	if err != nil {
		t.Fatalf("kea-dhcp4 -t rejects the configuration: %v\n%s\n%s", err, out, text)
	}
	// a configuration Kea rejects is caught by the same check
	bad := strings.Replace(text, `"routers"`, `"no-such-option"`, 1)
	_ = os.WriteFile(f, []byte(bad), 0o644)
	if out, err := exec.Command(bin, "-t", f).CombinedOutput(); err == nil {
		t.Errorf("the check accepts an unknown option:\n%s", out)
	}
}

func TestAMissingSocketIsAnError(t *testing.T) {
	c := &Client{Socket: filepath.Join(t.TempDir(), "none.sock"), Timeout: time.Second}
	if _, err := c.Hash(context.Background()); err == nil || !strings.Contains(err.Error(), "control socket") {
		t.Errorf("%v", err)
	}
}

func TestACommittedHookCarriesEveryLease(t *testing.T) {
	env := map[string]string{"LEASES4_SIZE": "2",
		"LEASES4_AT0_ADDRESS": "10.0.0.5", "LEASES4_AT0_HWADDR": "02:00:00:00:00:AA", "LEASES4_AT0_SUBNET_ID": "7",
		"LEASES4_AT1_ADDRESS": "10.0.0.6", "LEASES4_AT1_HWADDR": "02:00:00:00:00:bb"}
	evs, err := EventsFromHook("leases4_committed", func(k string) string { return env[k] })
	if err != nil || len(evs) != 2 || evs[0].IP.String() != "10.0.0.5" || evs[0].MAC != "02:00:00:00:00:aa" || evs[0].SubnetID != 7 || evs[1].IP.String() != "10.0.0.6" {
		t.Fatalf("%+v %v", evs, err)
	}
	if _, err := EventsFromHook("leases4_committed", func(string) string { return "" }); err == nil {
		t.Error("no size")
	}
}

func TestLeaseClassesSilenceAndOptionReservationsAreRendered(t *testing.T) {
	c := sampleConfig()
	c.Subnets[0].LeaseSeconds, c.Subnets[0].LeaseMin, c.Subnets[0].LeaseMax = 8, 8, 8
	c.Subnets[0].Reservations = []Reservation{
		{MAC: "02:00:00:00:00:31", IP: netip.MustParseAddr("10.10.0.31")},
		{MAC: "02:00:00:00:00:32", Options: Options{Router: netip.MustParseAddr("10.10.0.9"), Domain: "x.test"}},
	}
	c.LeaseClasses = []LeaseClass{
		{Name: "b", MACs: []string{"02:00:00:00:00:41"}, Seconds: 600},
		{Name: "a", MACs: []string{"02:00:00:00:00:43", "02:00:00:00:00:42"}, Seconds: 5, Min: 5, Max: 5},
	}
	c.Silenced = []Silence{{Interface: "br-iot", Except: []string{"02:00:00:00:00:44", "02:00:00:00:00:45"}}, {MACs: []string{"02:00:00:00:00:46"}}}
	doc, err := c.Document()
	if err != nil {
		t.Fatal(err)
	}
	d4 := doc["Dhcp4"].(jsonMap)
	sub := d4["subnet4"].([]any)[0].(jsonMap)
	if sub["valid-lifetime"] != 8 || sub["min-valid-lifetime"] != 8 || sub["max-valid-lifetime"] != 8 {
		t.Errorf("a subnet with bounds: %v", sub)
	}
	resv := sub["reservations"].([]any)
	if r := resv[0].(jsonMap); r["ip-address"] != "10.10.0.31" || r["option-data"] != nil {
		t.Errorf("a reservation of an address: %v", r)
	}
	if r := resv[1].(jsonMap); r["ip-address"] != nil || len(r["option-data"].([]any)) != 2 {
		t.Errorf("a reservation of options only has no address: %v", r)
	}
	classes := d4["client-classes"].([]any)
	if len(classes) != 3 {
		t.Fatalf("%v", classes)
	}
	a, b, drop := classes[0].(jsonMap), classes[1].(jsonMap), classes[2].(jsonMap)
	if a["name"] != "a" || a["test"] != "pkt4.mac == 0x020000000042 or pkt4.mac == 0x020000000043" || a["valid-lifetime"] != 5 || a["min-valid-lifetime"] != 5 || a["max-valid-lifetime"] != 5 {
		t.Errorf("a class with bounds, sorted by name, its clients sorted: %v", a)
	}
	if b["name"] != "b" || b["min-valid-lifetime"] != 5 || b["max-valid-lifetime"] != 3600 {
		t.Errorf("a class without bounds takes the defaults of a subnet: %v", b)
	}
	if drop["name"] != "DROP" || drop["test"] != "(pkt.iface == 'br-iot' and not (pkt4.mac == 0x020000000044 or pkt4.mac == 0x020000000045)) or (pkt4.mac == 0x020000000046)" {
		t.Errorf("the class DROP is the union of what is silenced: %v", drop)
	}
	// nothing silenced and no class: Kea takes no empty list
	c.LeaseClasses, c.Silenced = nil, []Silence{{Interface: ""}}
	doc, _ = c.Document()
	if _, ok := doc["Dhcp4"].(jsonMap)["client-classes"]; ok {
		t.Error("an empty class list is rendered, which Kea refuses")
	}
}

func TestChecksRefuseABadLeaseClassOrSilence(t *testing.T) {
	for name, mod := range map[string]func(*Config){
		"a class name with a blank": func(c *Config) {
			c.LeaseClasses = []LeaseClass{{Name: "a b", MACs: []string{"02:00:00:00:00:41"}, Seconds: 5}}
		},
		"two classes of one name": func(c *Config) {
			lc := LeaseClass{Name: "a", MACs: []string{"02:00:00:00:00:41"}, Seconds: 5}
			c.LeaseClasses = []LeaseClass{lc, lc}
		},
		"a class without a client":   func(c *Config) { c.LeaseClasses = []LeaseClass{{Name: "a", Seconds: 5}} },
		"a class without a time":     func(c *Config) { c.LeaseClasses = []LeaseClass{{Name: "a", MACs: []string{"02:00:00:00:00:41"}}} },
		"a class with a bad MAC":     func(c *Config) { c.LeaseClasses = []LeaseClass{{Name: "a", MACs: []string{"nope"}, Seconds: 5}} },
		"a silenced client, bad MAC": func(c *Config) { c.Silenced = []Silence{{MACs: []string{"02:00:00:00:00"}}} },
		"an exception, bad MAC":      func(c *Config) { c.Silenced = []Silence{{Interface: "x", Except: []string{"zz"}}} },
		"a reservation outside": func(c *Config) {
			c.Subnets[0].Reservations = []Reservation{{MAC: "02:00:00:00:00:31", IP: netip.MustParseAddr("192.0.2.1")}}
		},
	} {
		c := sampleConfig()
		mod(&c)
		if _, err := c.Document(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Kea's own check takes what the test actions add to the configuration: classes with lease times, the class DROP with an
// expression on the interface, and reservations that name no address.
func TestKeaAcceptsTheClassesAndReservationsOfTheTestActions(t *testing.T) {
	bin, err := exec.LookPath("kea-dhcp4")
	if err != nil {
		t.Skip("kea-dhcp4 is not installed")
	}
	cfg := sampleConfig()
	cfg.Subnets[0].Interface = "lo"
	cfg.Subnets[0].LeaseSeconds, cfg.Subnets[0].LeaseMin, cfg.Subnets[0].LeaseMax = 8, 8, 8
	cfg.Subnets[0].Reservations = []Reservation{
		{MAC: "02:00:00:00:00:31", IP: netip.MustParseAddr("10.10.0.31"), Options: Options{DNS: []netip.Addr{netip.MustParseAddr("192.0.2.53")}, Custom: []CustomOption{{Code: 224, Value: "0a0b"}}}},
		{MAC: "02:00:00:00:00:32", Options: Options{Router: netip.MustParseAddr("10.10.0.9"), Domain: "x.test", NTP: []netip.Addr{netip.MustParseAddr("10.10.0.2")}}},
	}
	cfg.LeaseClasses = []LeaseClass{{Name: "cgw-a", MACs: []string{"02:00:00:00:00:41", "02:00:00:00:00:42"}, Seconds: 5, Min: 5, Max: 5}}
	cfg.Silenced = []Silence{{Interface: "lo", Except: []string{"02:00:00:00:00:44"}}, {MACs: []string{"02:00:00:00:00:46"}}}
	cfg.Script = ""
	text, err := cfg.Render()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "kea.json")
	if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "-t", f).CombinedOutput(); err != nil {
		t.Fatalf("kea-dhcp4 -t rejects the configuration: %v\n%s\n%s", err, out, text)
	}
}
