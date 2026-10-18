//go:build testbed

package kea

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests in this file need the real Kea as root (startKea): they carry the build tag testbed, so the
// testbed runner (level 1 and 1b) runs them, and both testbed jobs name them with -expect-pass
// ($KEA_LEVEL1, .github/workflows/ci.yml), so a run in which one skips fails, instead of a level-0 run on
// a machine without Kea skipping them unnoticed (I64-05).

// #64: the real Kea takes the retries: an interface it cannot open a socket on (here the loopback,
// which Kea never serves) leaves it retrying instead of failed for good.
func TestKeaRetriesASocketItCouldNotOpen(t *testing.T) {
	cfg := sampleConfig()
	cfg.Subnets[0].Interface = "lo"
	cfg.Subnets[0].Subnet = netip.MustParsePrefix("127.0.0.0/8")
	cfg.Subnets[0].Pools = []Pool{{netip.MustParseAddr("127.0.0.100"), netip.MustParseAddr("127.0.0.200")}}
	cfg.Subnets[0].Reservations = nil
	cfg.Subnets[0].Router = netip.MustParseAddr("127.0.0.1")
	cfg.Subnets[0].DNS, cfg.Subnets[0].NTP = nil, nil
	cfg.Script = ""
	c := startKea(t, cfg)
	a, err := c.Do(context.Background(), "status-get", nil)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Sockets struct {
			Status string   `json:"status"`
			Errors []string `json:"errors"`
		} `json:"sockets"`
	}
	if err := json.Unmarshal(a.Arguments, &st); err != nil {
		t.Fatal(err)
	}
	if st.Sockets.Status != "retrying" || len(st.Sockets.Errors) == 0 || !strings.Contains(st.Sockets.Errors[0], "lo") {
		t.Errorf("%s", a.Arguments)
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

// M6a-19 test: Leases pages through lease4-get-page instead of asking for everything in one
// lease4-get-all call. leasePageLimit is lowered so the real Kea answers three leases in two pages
// rather than needing thousands of real leases to prove the loop works.
func TestLeasesArePagedPastTheLimit(t *testing.T) {
	cfg := sampleConfig()
	cfg.Subnets[0].Interface = "lo"
	cfg.Subnets[0].Subnet = netip.MustParsePrefix("127.0.0.0/8")
	cfg.Subnets[0].Pools = []Pool{{netip.MustParseAddr("127.0.0.100"), netip.MustParseAddr("127.0.0.200")}}
	cfg.Subnets[0].Reservations = nil
	cfg.Subnets[0].Router = netip.MustParseAddr("127.0.0.1")
	cfg.Subnets[0].DNS, cfg.Subnets[0].NTP = nil, nil
	cfg.Script = ""
	c := startKea(t, cfg)
	ctx := context.Background()

	addrs := []string{"127.0.0.101", "127.0.0.102", "127.0.0.103"}
	for i, ip := range addrs {
		mac := fmt.Sprintf("02:00:00:00:00:%02x", i)
		if _, err := c.Do(ctx, "lease4-add", map[string]any{"ip-address": ip, "hw-address": mac, "valid-lft": 300, "subnet-id": 7}); err != nil {
			t.Fatal(err)
		}
	}

	old := leasePageLimit
	leasePageLimit = 2
	t.Cleanup(func() { leasePageLimit = old })

	leases, err := c.Leases(ctx)
	if err != nil || len(leases) != len(addrs) {
		t.Fatalf("%+v %v", leases, err)
	}
	got := map[string]bool{}
	for _, l := range leases {
		got[l.IP] = true
	}
	for _, ip := range addrs {
		if !got[ip] {
			t.Errorf("lease %s is missing: %+v", ip, leases)
		}
	}
}
