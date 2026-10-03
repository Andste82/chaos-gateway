package kea

import (
	"context"
	"encoding/json"
	"errors"
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

func TestDefaultPoolIsTheSecondHalf(t *testing.T) {
	p, ok := DefaultPool(netip.MustParsePrefix("10.10.0.1/24"), netip.MustParseAddr("10.10.0.1"))
	if !ok || p.Start.String() != "10.10.0.128" || p.End.String() != "10.10.0.254" {
		t.Errorf("%v %v", p, ok)
	}
	// the gateway in the second half is skipped
	p, _ = DefaultPool(netip.MustParsePrefix("10.10.0.200/24"), netip.MustParseAddr("10.10.0.128"))
	if p.Start.String() != "10.10.0.129" {
		t.Errorf("%v", p)
	}
	if _, ok := DefaultPool(netip.MustParsePrefix("10.10.0.0/30"), netip.MustParseAddr("10.10.0.1")); ok {
		t.Error("a /30 has no useful pool")
	}
	if p, ok := DefaultPool(netip.MustParsePrefix("192.168.1.1/29"), netip.MustParseAddr("192.168.1.1")); !ok || p.Start.String() != "192.168.1.4" || p.End.String() != "192.168.1.6" {
		t.Errorf("%v %v", p, ok)
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

// startKea runs the real daemon on the loopback with the paths below dir (Kea restricts them to
// /run/kea and /var/lib/kea unless the environment says otherwise).
func startKea(t *testing.T, cfg Config) *Client {
	t.Helper()
	bin, err := exec.LookPath("kea-dhcp4")
	if err != nil {
		t.Skip("kea-dhcp4 is not installed")
	}
	if os.Geteuid() != 0 {
		t.Skip("Kea opens raw sockets: it needs root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg.Socket, cfg.Leases = filepath.Join(dir, "ctrl.sock"), filepath.Join(dir, "leases.csv")
	text, err := cfg.Render()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "kea.json")
	if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", f)
	cmd.Env = append(os.Environ(), "KEA_CONTROL_SOCKET_DIR="+dir, "KEA_DHCP_DATA_DIR="+dir, "KEA_PIDFILE_DIR="+dir, "KEA_LOCKFILE_DIR="+dir, "KEA_LOG_FILE_DIR="+dir)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	c := &Client{Socket: cfg.Socket, Timeout: 5 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Hash(context.Background()); err == nil {
			return c
		}
		select {
		case <-done:
			t.Fatalf("kea exited:\n%s", out.String())
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("kea does not answer:\n%s", out.String())
	return nil
}

func TestTheClientDrivesARealKea(t *testing.T) {
	cfg := sampleConfig()
	cfg.Subnets[0].Interface = "lo"
	cfg.Subnets[0].Subnet = netip.MustParsePrefix("127.0.0.0/8")
	cfg.Subnets[0].Pools = []Pool{{netip.MustParseAddr("127.0.0.100"), netip.MustParseAddr("127.0.0.200")}}
	cfg.Subnets[0].Reservations = nil
	cfg.Subnets[0].Router = netip.MustParseAddr("127.0.0.1")
	cfg.Subnets[0].DNS = nil
	cfg.Subnets[0].NTP = nil
	cfg.Script = "" // the hook script path is restricted, and this test has no API to notify
	c := startKea(t, cfg)
	ctx := context.Background()

	subs, err := c.Subnets(ctx)
	if err != nil || len(subs) != 1 || subs[0].ID != 7 || subs[0].Interface != "lo" {
		t.Fatalf("%+v %v", subs, err)
	}
	h1, err := c.Hash(ctx)
	if err != nil || h1 == "" {
		t.Fatal(err)
	}

	// config-set replaces the configuration at run time: a reservation and another pool
	cfg2 := cfg
	cfg2.Socket, cfg2.Leases = c.Socket, strings.TrimSuffix(c.Socket, "ctrl.sock")+"leases.csv"
	cfg2.Subnets = append([]Subnet(nil), cfg.Subnets...)
	cfg2.Subnets[0].Pools = []Pool{{netip.MustParseAddr("127.0.0.10"), netip.MustParseAddr("127.0.0.20")}}
	cfg2.Subnets[0].Reservations = []Reservation{{MAC: "02:00:00:00:00:31", IP: netip.MustParseAddr("127.0.0.31")}}
	doc, err := cfg2.Document()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, doc); err != nil {
		t.Fatalf("%v", err)
	}
	subs, _ = c.Subnets(ctx)
	if len(subs) != 1 || len(subs[0].Reservations) != 1 || subs[0].Reservations[0].IP != "127.0.0.31" || !strings.Contains(subs[0].Pools[0].Pool, "127.0.0.10") {
		t.Errorf("%+v", subs)
	}
	if h2, _ := c.Hash(ctx); h2 == h1 {
		t.Error("the hash did not change")
	}
	// a configuration Kea rejects leaves the running one in place
	bad := map[string]any{"Dhcp4": map[string]any{"subnet4": []any{map[string]any{"id": 1, "subnet": "not a subnet"}}}}
	err = c.Apply(ctx, bad)
	var ke *Error
	if !errors.As(err, &ke) || ke.Result == 0 {
		t.Fatalf("%v", err)
	}
	if subs, _ := c.Subnets(ctx); len(subs) != 1 || subs[0].ID != 7 {
		t.Errorf("a rejected configuration replaced the running one: %+v", subs)
	}

	// leases
	if leases, err := c.Leases(ctx); err != nil || len(leases) != 0 {
		t.Errorf("%v %v", leases, err)
	}
	if _, err := c.Do(ctx, "lease4-add", map[string]any{"ip-address": "127.0.0.15", "hw-address": "02:00:00:00:00:aa", "valid-lft": 300, "subnet-id": 7}); err != nil {
		t.Fatal(err)
	}
	leases, err := c.Leases(ctx)
	if err != nil || len(leases) != 1 || leases[0].IP != "127.0.0.15" || leases[0].MAC != "02:00:00:00:00:aa" || leases[0].SubnetID != 7 || !leases[0].ExpiresAt().After(time.Now()) {
		t.Fatalf("%+v %v", leases, err)
	}
	if err := c.DeleteLease(ctx, "127.0.0.15"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteLease(ctx, "127.0.0.15"); err != nil {
		t.Errorf("deleting a lease that is gone is fine: %v", err)
	}
	if leases, _ := c.Leases(ctx); len(leases) != 0 {
		t.Errorf("%+v", leases)
	}
}

func TestAMissingSocketIsAnError(t *testing.T) {
	c := &Client{Socket: filepath.Join(t.TempDir(), "none.sock"), Timeout: time.Second}
	if _, err := c.Hash(context.Background()); err == nil || !strings.Contains(err.Error(), "control socket") {
		t.Errorf("%v", err)
	}
}

func TestACommittedHookCarriesEveryLease(t *testing.T) {
	env := map[string]string{"KEA_LEASES4_SIZE": "2",
		"KEA_LEASES4_AT0_ADDRESS": "10.0.0.5", "KEA_LEASES4_AT0_HWADDR": "02:00:00:00:00:AA", "KEA_LEASES4_AT0_SUBNET_ID": "7",
		"KEA_LEASES4_AT1_ADDRESS": "10.0.0.6", "KEA_LEASES4_AT1_HWADDR": "02:00:00:00:00:bb"}
	evs, err := EventsFromHook("leases4_committed", func(k string) string { return env[k] })
	if err != nil || len(evs) != 2 || evs[0].IP.String() != "10.0.0.5" || evs[0].MAC != "02:00:00:00:00:aa" || evs[0].SubnetID != 7 || evs[1].IP.String() != "10.0.0.6" {
		t.Fatalf("%+v %v", evs, err)
	}
	if _, err := EventsFromHook("leases4_committed", func(string) string { return "" }); err == nil {
		t.Error("no size")
	}
}
