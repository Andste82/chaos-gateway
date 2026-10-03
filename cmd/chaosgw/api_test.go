package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestTheAPIServerRunsFromSetupToPasswordReset(t *testing.T) {
	root := t.TempDir()
	secretsDir := filepath.Join(root, "secrets")
	_, sock, _ := startExecutorWithKeys(t, secretsDir)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	logs := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveAPI(ctx, slog.New(slog.NewTextHandler(logs, nil)), logs, apiOptions{socket: sock, execUID: uint32(os.Getuid()), stateDir: filepath.Join(root, "state"),
			secretsDir: secretsDir, dataDir: filepath.Join(root, "data"), port: port, listen: fmt.Sprintf("127.0.0.1:%d", port), poll: time.Second, confirm: time.Second})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveAPI: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the server does not stop")
		}
		time.Sleep(700 * time.Millisecond) // the netlink watcher notices the end at its next receive timeout (500 ms)
	}()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	base := fmt.Sprintf("https://127.0.0.1:%d/api/v1", port)
	call := func(method, path string, body any, hdr map[string]string) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, rd)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return res.StatusCode, m
	}
	// wait until the server answers
	deadline := time.Now().Add(20 * time.Second)
	for {
		if res, err := client.Get(base + "/system/health"); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server does not come up:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	// the container health check
	if code, _, _ := func() (int, string, string) {
		var o, e bytes.Buffer
		return apiHealth(port, fmt.Sprintf("127.0.0.1:%d", port), &o, &e), o.String(), e.String()
	}(); code != 0 {
		t.Errorf("--health: %d", code)
	}

	// the setup token is printed to the log, once
	m := regexp.MustCompile(`Setup token: (setup_\S+)`).FindStringSubmatch(logs.String())
	if m == nil {
		t.Fatalf("no setup token in the log:\n%s", logs.String())
	}
	if code, _ := call("POST", "/setup", map[string]any{"admin_password": "a long enough password", "configuration": json.RawMessage(`{}`)}, map[string]string{"X-Setup-Token": m[1]}); code != 422 {
		t.Errorf("an empty configuration: %d", code)
	}
	cfgRaw, err := os.ReadFile(wgConfigFile(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg := yamlToJSON(t, cfgRaw)
	code, res := call("POST", "/setup", map[string]any{"admin_password": "a long enough password", "configuration": cfg}, map[string]string{"X-Setup-Token": m[1]})
	if code != 200 || res["revision"] != float64(1) {
		t.Fatalf("setup: %d %v\n%s", code, res, logs.String())
	}
	code, sess := call("POST", "/auth/login", map[string]any{"password": "a long enough password"}, nil)
	if code != 200 {
		t.Fatalf("login: %d %v", code, sess)
	}
	if code, st := call("GET", "/state", nil, nil); code != 200 || st["active_revision"] != float64(1) {
		t.Errorf("%d %v", code, st)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "audit.jsonl")); err != nil {
		t.Errorf("no audit log: %v", err)
	}
	// the TLS key stays private
	if fi, err := os.Stat(filepath.Join(secretsDir, "tls", "key.pem")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", fi, err)
	}

	// `chaosgw admin reset-password` from another process ends the session
	var out, errOut bytes.Buffer
	if code := runAdmin([]string{"reset-password", "--secrets-dir", secretsDir, "--password-stdin"}, &out, &errOut, strings.NewReader("a reset password!\n")); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(filepath.Join(secretsDir, "auth", "auth.json"), later, later)
	if code, _ := call("GET", "/state", nil, nil); code != 401 {
		t.Errorf("the session survived the reset: %d", code)
	}
	if code, _ := call("POST", "/auth/login", map[string]any{"password": "a reset password!"}, nil); code != 200 {
		t.Errorf("the new password: %d", code)
	}
}

func TestResetPasswordRefusesWhatItShould(t *testing.T) {
	dir := t.TempDir()
	run := func(in string, args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := runAdmin(append([]string{"reset-password", "--secrets-dir", dir}, args...), &out, &errOut, strings.NewReader(in))
		return code, errOut.String()
	}
	if code, _ := run("", "--password-stdin"); code != 1 {
		t.Errorf("no password: %d", code)
	}
	if code, msg := run("short\n", "--password-stdin"); code != 1 || !strings.Contains(msg, "at least") {
		t.Errorf("a short password: %d %s", code, msg)
	}
	if code, msg := run("a long enough password\n", "--password-stdin"); code != 1 || !strings.Contains(msg, "finish the setup") {
		t.Errorf("before the setup: %d %s", code, msg)
	}
	if code, _ := run("x"); code != 2 {
		t.Errorf("neither flag: %d", code)
	}
	if code, _ := run("x", "--password-stdin", "--password-file", "f"); code != 2 {
		t.Errorf("both flags: %d", code)
	}
	var o, e bytes.Buffer
	if code := runAdmin(nil, &o, &e, nil); code != 2 {
		t.Errorf("no subcommand: %d", code)
	}
}

func yamlToJSON(t *testing.T, raw []byte) any {
	t.Helper()
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	var m any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestTheAPIListensOnTheManagementNetworkOnly(t *testing.T) {
	host := compiler.Host{Links: []compiler.HostLink{
		{Name: "lo", Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
		{Name: "mgmt0", Addrs: []netip.Prefix{netip.MustParsePrefix("192.168.56.1/24")}},
		{Name: "wan0", Addrs: []netip.Prefix{netip.MustParsePrefix("203.0.113.1/24")}},
		{Name: "br-lan0", Addrs: []netip.Prefix{netip.MustParsePrefix("10.10.0.1/24")}},
	}}
	cfg := &model.Configuration{Management: model.Management{Interface: model.InterfaceRef{Name: ptrOf("mgmt0")}}}
	strs := func(a []netip.Addr) string {
		var s []string
		for _, x := range a {
			s = append(s, x.String())
		}
		return strings.Join(s, ",")
	}
	// before the setup: every address of the host
	if got := strs(listenAddrs(&engine.Snapshot{Host: host}, false, "")); got != "127.0.0.1,192.168.56.1,203.0.113.1,10.10.0.1" {
		t.Errorf("before the setup: %s", got)
	}
	// after: the management interface, nothing of the uplink or the test networks
	if got := strs(listenAddrs(&engine.Snapshot{Host: host, Config: cfg}, true, "")); got != "127.0.0.1,192.168.56.1" {
		t.Errorf("after the setup: %s", got)
	}
	// set up but no active configuration (yet): fail closed, the loopback only
	if got := strs(listenAddrs(&engine.Snapshot{Host: host}, true, "")); got != "127.0.0.1" {
		t.Errorf("without a configuration: %s", got)
	}
	// a management-role WireGuard network adds its tunnel address
	snap := &engine.Snapshot{Host: host, Config: cfg, WireGuardInterfaces: []compiler.WGInterface{
		{Name: "wg-admin", Role: "management", Address: netip.MustParsePrefix("10.98.0.1/24")},
		{Name: "wg-lab", Role: "test", Address: netip.MustParsePrefix("10.99.0.1/24")},
	}}
	if got := strs(listenAddrs(snap, true, "")); got != "127.0.0.1,192.168.56.1,10.98.0.1" {
		t.Errorf("with a management tunnel: %s", got)
	}
	if got := strs(listenAddrs(snap, true, "127.0.0.5:9000")); got != "127.0.0.5" {
		t.Errorf("explicit: %s", got)
	}
}

func ptrOf[T any](v T) *T { return &v }
