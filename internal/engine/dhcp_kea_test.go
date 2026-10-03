package engine_test

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/kea"
)

// runKea starts the real daemon with the given configuration and returns a function that stops it.
func runKea(t *testing.T, dir string, cfg kea.Config) (stop func()) {
	t.Helper()
	cfg.Socket, cfg.Leases = filepath.Join(dir, "ctrl.sock"), filepath.Join(dir, "leases.csv")
	text, err := cfg.Render()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "kea.json")
	if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("kea-dhcp4", "-c", f)
	cmd.Env = append(os.Environ(), "KEA_CONTROL_SOCKET_DIR="+dir, "KEA_DHCP_DATA_DIR="+dir, "KEA_PIDFILE_DIR="+dir, "KEA_LOCKFILE_DIR="+dir, "KEA_LOG_FILE_DIR="+dir)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	c := &kea.Client{Socket: cfg.Socket, Timeout: 3 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Hash(context.Background()); err == nil {
			return func() { _ = cmd.Process.Kill(); <-done; _ = os.Remove(cfg.Socket) }
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

func TestTheConfigurationIsSentAgainToAKeaThatRestarted(t *testing.T) {
	if _, err := exec.LookPath("kea-dhcp4"); err != nil {
		t.Skip("kea-dhcp4 is not installed")
	}
	if os.Geteuid() != 0 {
		t.Skip("Kea opens raw sockets: it needs root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	stop := runKea(t, dir, kea.Config{})
	client := &kea.Client{Socket: filepath.Join(dir, "ctrl.sock"), Timeout: 5 * time.Second}
	k := &engine.KeaDHCP{Client: client, Base: kea.Config{Socket: client.Socket, Leases: filepath.Join(dir, "leases.csv")}}
	target := &compiler.KeaTarget{Config: kea.Config{Socket: client.Socket, Leases: filepath.Join(dir, "leases.csv"), Subnets: []kea.Subnet{{
		ID: 3, Network: "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21", Subnet: netip.MustParsePrefix("127.0.0.0/8"), Interface: "lo",
		Pools: []kea.Pool{{Start: netip.MustParseAddr("127.0.0.100"), End: netip.MustParseAddr("127.0.0.110")}}, LeaseSeconds: 600}}}}
	ctx := context.Background()
	if err := k.Apply(ctx, target); err != nil {
		t.Fatal(err)
	}
	if subs, _ := client.Subnets(ctx); len(subs) != 1 || subs[0].ID != 3 {
		t.Fatalf("%+v", subs)
	}
	// an unchanged configuration is not sent again: the hash stays
	h1, _ := client.Hash(ctx)
	if err := k.Apply(ctx, target); err != nil {
		t.Fatal(err)
	}
	if h2, _ := client.Hash(ctx); h2 != h1 {
		t.Error("an unchanged configuration was sent again")
	}
	// Kea restarts from its initial configuration (no subnets) and forgets what it was given
	stop()
	stop = runKea(t, dir, kea.Config{})
	defer stop()
	if subs, _ := client.Subnets(ctx); len(subs) != 0 {
		t.Fatalf("%+v", subs)
	}
	if err := k.Apply(ctx, target); err != nil {
		t.Fatal(err)
	}
	if subs, _ := client.Subnets(ctx); len(subs) != 1 || subs[0].ID != 3 {
		t.Errorf("the configuration is not sent again after the restart: %+v", subs)
	}
	// DHCP off: a server without scope
	if err := k.Apply(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if subs, _ := client.Subnets(ctx); len(subs) != 0 {
		t.Errorf("%+v", subs)
	}
}
