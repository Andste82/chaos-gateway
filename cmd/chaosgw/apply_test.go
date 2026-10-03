package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// startExecutor serves an executor on a socket, backed by a simulated kernel.
func startExecutor(t *testing.T) (*kernelsim.Kernel, string, *executor.Executor) {
	t.Helper()
	k := kernelsim.New()
	k.AddLink("lan0", "02:00:00:00:00:01", "veth", true)
	k.AddLink("lan1", "02:00:00:00:01:01", "veth", true)
	k.AddLink("wan0", "02:00:00:00:02:01", "veth", true)
	k.SetAddr("wan0", "203.0.113.1/24")
	k.AddLink("mgmt0", "02:00:00:00:03:01", "veth", true)
	k.SetAddr("mgmt0", "192.168.56.1/24")
	k.SetMainDefault("192.168.56.254", "mgmt0")
	k.AddDockerChain()
	ex, err := executor.New(k)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "cgx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "e.sock")
	l, err := executor.Listen(sock, 0o660, -1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	srv := &executor.Server{Exec: ex, Auth: executor.AllowUIDs(uint32(os.Getuid()))}
	go func() { defer wg.Done(); _ = srv.Serve(ctx, l) }()
	t.Cleanup(func() { cancel(); wg.Wait(); ex.Close() })
	return k, sock, ex
}

func uid() string { return itoaUID(os.Getuid()) }

func itoaUID(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func configFile(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../internal/compiler/testdata/gateway.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyFileConfiguresTheGateway(t *testing.T) {
	k, sock, ex := startExecutor(t)
	code, out, errOut := runCmd("apply", "--file", configFile(t), "--socket", sock, "--executor-uid", uid())
	if code != 0 || !strings.Contains(out, "applied and verified: 2 networks, uplink wan0 via 203.0.113.10") {
		t.Fatalf("code %d\nstdout %s\nstderr %s", code, out, errOut)
	}
	s, err := apply.ReadState(context.Background(), apply.Local{E: ex}, "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Links["br-iot"].Kind() != "bridge" || s.Links["lan0"].Master != "br-iot" {
		t.Errorf("the gateway is not configured: %+v", s.Links)
	}
	_ = k
}

func TestApplyFileDryRunChangesNothing(t *testing.T) {
	k, sock, _ := startExecutor(t)
	code, out, errOut := runCmd("apply", "--file", configFile(t), "--socket", sock, "--executor-uid", uid(), "--dry-run")
	if code != 0 || !strings.Contains(out, "would apply:") || !strings.Contains(out, "create bridge br-iot") || !strings.Contains(out, "+bridge br-iot up") {
		t.Fatalf("code %d\n%s\n%s", code, out, errOut)
	}
	for _, c := range k.Commands() {
		if strings.Contains(c, "link add") || strings.HasPrefix(c, "nft -j -f") || strings.Contains(c, "sysctl -w") {
			t.Errorf("dry run ran %q", c)
		}
	}
}

func TestApplyFileReportsAnInvalidConfigurationWithItsPaths(t *testing.T) {
	_, sock, _ := startExecutor(t)
	p := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(p, []byte("schema_version: 1\nuplink: {interface: {name: wan0}}\nmanagement: {interface: {name: mgmt0}}\nnetworks:\n  0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21: {type: lan, name: A, interfaces: [{name: lan0}], address: 10.10.0.1/33}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCmd("apply", "--file", p, "--socket", sock, "--executor-uid", uid())
	if code != 1 || !strings.Contains(errOut, "not a valid configuration") || !strings.Contains(errOut, "/networks/") {
		t.Fatalf("code %d: %s", code, errOut)
	}
}

func TestApplyFileFailures(t *testing.T) {
	_, sock, _ := startExecutor(t)
	for name, c := range map[string]struct {
		args []string
		code int
		msg  string
	}{
		"no file":           {[]string{"apply"}, 2, "usage"},
		"missing file":      {[]string{"apply", "--file", "/nonexistent.yaml", "--socket", sock}, 1, "no such file"},
		"no executor":       {[]string{"apply", "--file", configFile(t), "--socket", sock + ".none"}, 1, "cannot reach the executor"},
		"dry run and state": {[]string{"apply", "--file", "x", "--dry-run", "--state-dir", "y"}, 2, "exclude each other"},
		"extra argument":    {[]string{"apply", "--file", "x", "extra"}, 2, "usage"},
	} {
		code, _, errOut := runCmd(c.args...)
		if code != c.code || !strings.Contains(errOut, c.msg) {
			t.Errorf("%s: code %d, %q", name, code, errOut)
		}
	}
}

func TestApplyFileWithAnUplinkThatIsMissingChangesNothing(t *testing.T) {
	k, sock, _ := startExecutor(t)
	k.RemoveLink("wan0")
	code, _, errOut := runCmd("apply", "--file", configFile(t), "--socket", sock, "--executor-uid", uid())
	if code != 1 || !strings.Contains(errOut, "uplink interface wan0 is not present") || !strings.Contains(errOut, "nothing was changed") {
		t.Fatalf("code %d: %s", code, errOut)
	}
}

func TestApplyFileThroughTheStoreCreatesTheActiveRevision(t *testing.T) {
	_, sock, ex := startExecutor(t)
	state := t.TempDir()
	for want := int64(1); want <= 2; want++ {
		code, out, errOut := runCmd("apply", "--file", configFile(t), "--socket", sock, "--executor-uid", uid(), "--state-dir", state)
		if code != 0 || !strings.Contains(out, "is active") {
			t.Fatalf("run %d: code %d\n%s\n%s", want, code, out, errOut)
		}
		st, err := store.Open(state)
		if err != nil {
			t.Fatal(err)
		}
		if st.ActiveID() != want {
			t.Errorf("run %d: active revision %d", want, st.ActiveID())
		}
		_ = st.Close()
	}
	s, _ := apply.ReadState(context.Background(), apply.Local{E: ex}, "", apply.Want{})
	if s.Links["br-lab"].Kind() != "bridge" {
		t.Error("the gateway is not configured")
	}
}
