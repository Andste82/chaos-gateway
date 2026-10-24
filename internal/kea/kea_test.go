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
