//go:build testbed && browser

package api_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// webDir is the web app's directory, with what the browser test needs: the build that the gateway
// serves, the Playwright CLI and its browser. A missing piece fails the test with what to run; it
// is never skipped (the CI job prepares all three, docs/development.md "Web UI").
func webDir(t *testing.T) (dir, browsers string) {
	t.Helper()
	dir, err := filepath.Abs("../../web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist", "index.html")); err != nil {
		t.Fatalf("the web app is not built (%v): run `make build-web` first", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", ".bin", "playwright")); err != nil {
		t.Fatalf("Playwright is not installed (%v): run `cd web && npm ci` first", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("no node in PATH: %v", err)
	}
	// Playwright's own default is ~/.cache/ms-playwright; the guest of level 1b runs without HOME, as
	// root, on the file system of the devcontainer, where root installed the browser
	candidates := []string{os.Getenv("PLAYWRIGHT_BROWSERS_PATH")}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".cache", "ms-playwright"))
	}
	candidates = append(candidates, "/root/.cache/ms-playwright")
	for _, c := range candidates {
		if m, _ := filepath.Glob(filepath.Join(c, "chromium*")); c != "" && len(m) > 0 {
			return dir, c
		}
	}
	t.Fatalf("no Chromium for Playwright in %v: run `cd web && npx playwright install chromium` first", candidates)
	return "", ""
}

// M12 test (plan §5): an E2E flow in the testbed — first start with the setup token → assign
// interfaces → see a discovered device. The gateway runs on the real executor in the testbed's
// gateway namespace and serves the built web app; Chromium walks through the setup wizard in
// web/e2e-testbed/first-start.spec.ts while device A sends traffic through the gateway, until the
// device list shows A as discovered.
func TestFirstStartInTheBrowserUntilADiscoveredDevice(t *testing.T) {
	web, browsers := webDir(t)
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false), testbed.WithGatewayBridges(false))
	g := newGW(t, func(o *options) {
		o.runner = executor.NewExecRunner()
		o.namespace = top.GW.Name
		o.ui = os.DirFS(filepath.Join(web, "dist"))
		// the person in the browser confirms; under emulation the apply alone takes minutes
		o.confirm = 5 * time.Minute
	})
	g.client.Timeout = 6 * time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	if err := g.e.PollObserved(ctx, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// device A talks to its gateway address until the test ends: once the setup made the network,
	// the gateway sees it in its neighbor table
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_, _ = top.A.Run(ctx, "ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}()

	runBrowser(ctx, t, web, browsers, "first-start.spec.ts", map[string]string{
		"CHAOSGW_URL":            g.ts.URL,
		"CHAOSGW_SETUP_TOKEN":    g.setup,
		"CHAOSGW_LAN_ADDRESS":    testbed.LAN0Gateway + "/24",
		"CHAOSGW_UPLINK_GATEWAY": testbed.ServerAddr,
		"CHAOSGW_DEVICE_MAC":     testbed.ClientAMAC,
		"CHAOSGW_DEVICE_IP":      testbed.ClientAAddr,
	}, func() string { return "neighbors of the gateway:\n" + top.GW.Must("ip", "neigh") })

	// what the browser saw is what the gateway holds: the setup is done, revision 1 confirmed, A known
	g.mintToken("read")
	st := g.do("GET", "/state", nil, nil, nil).json(t)
	if st["active_revision"] != float64(1) || st["pending_confirm"] != nil {
		t.Errorf("state after the setup in the browser: %v", st)
	}
	devs := g.do("GET", "/devices?origin=discovered", nil, nil, nil)
	if !strings.Contains(string(devs.Body), testbed.ClientAMAC) {
		t.Errorf("device A is not a discovered device in the API: %s", devs.Body)
	}
}

// runBrowser runs one spec of web/e2e-testbed with Playwright against the gateway; env names the
// gateway and what the spec needs. On a failure it logs the page as the browser had it (its
// accessibility tree: every message the UI showed) and what `more` says about the testbed.
func runBrowser(ctx context.Context, t *testing.T, web, browsers, spec string, env map[string]string, more func() string) {
	t.Helper()
	out := t.TempDir()
	cmd := exec.CommandContext(ctx, filepath.Join(web, "node_modules", ".bin", "playwright"), "test", "--config", "playwright.testbed.config.ts", spec)
	cmd.Dir = web
	cmd.Env = append(os.Environ(),
		"PLAYWRIGHT_BROWSERS_PATH="+browsers,
		"PLAYWRIGHT_OUTPUT_DIR="+filepath.Join(out, "results"),
		// the checkout may be read-only (the VM of level 1b): everything written goes to the test's directory
		"HOME="+out,
		"TMPDIR="+out,
		"CI=1",
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	res, err := cmd.CombinedOutput()
	t.Logf("playwright %s:\n%s", spec, res)
	if err != nil {
		pages, _ := filepath.Glob(filepath.Join(out, "results", "*", "error-context.md"))
		for _, p := range pages {
			if b, rerr := os.ReadFile(p); rerr == nil {
				t.Logf("%s:\n%s", filepath.Base(filepath.Dir(p)), b)
			}
		}
		t.Fatalf("the browser flow %s failed: %v\n%s", spec, err, more())
	}
}

// M13 test (plan §5): create a fault in the UI, then verify the measured effect in the testbed — and,
// on the way, the other three: validation errors are shown, a conflicting change made through the API
// triggers the conflict dialog, the faults screen marks an overridden fault. The gateway runs on the
// real executor with the testbed's configuration and serves the built web app; devices A and B of the
// network IoT send traffic, so the gateway knows them. In web/e2e-testbed/faults.spec.ts Chromium logs
// in, adds a configured fault of 30 ms on the network IoT (the unapplied-changes bar, the preview), meets
// the revision a token applied meanwhile (the conflict dialog: reload and reapply), applies, adds a
// fault of 100 ms on device A (after a validation error), and finds the network's fault overridden by
// it on the faults screen. Then the Go test measures: A's round trip carries 2 × 100 ms, B's 2 × 30 ms.
func TestAFaultCreatedInTheBrowserTakesEffectOnTheWire(t *testing.T) {
	web, browsers := webDir(t)
	g, top := newBedGW(t, func(o *options) { o.ui = os.DirFS(filepath.Join(web, "dist")) })

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	if err := g.e.PollObserved(ctx, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// A and B talk to their gateway address, so the gateway knows them as devices
	for _, ns := range []*testbed.Namespace{top.A, top.B} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_, _ = ns.Run(ctx, "ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}()
	}
	baseA := testbed.MustPing(t, top.A, testbed.ServerAddr, 10, 200*time.Millisecond).Median()
	baseB := testbed.MustPing(t, top.B, testbed.ServerAddr, 10, 200*time.Millisecond).Median()

	// the token of "someone else": a test that changes the configuration through the API meanwhile
	_, token, err := g.au.CreateToken("ci-runner", auth.ScopeFull, 0)
	if err != nil {
		t.Fatal(err)
	}
	runBrowser(ctx, t, web, browsers, "faults.spec.ts", map[string]string{
		"CHAOSGW_URL":            g.ts.URL,
		"CHAOSGW_ADMIN_PASSWORD": adminPassword,
		"CHAOSGW_TOKEN":          token,
		"CHAOSGW_DEVICE_MAC":     testbed.ClientAMAC,
	}, func() string { return "overlays:\n" + string(g.do("GET", "/overlays", nil, nil, nil).Body) })

	// what the browser did is in the gateway: the configured fault of the network, the overlay of A
	g.mintToken("read")
	faults := string(g.do("GET", "/faults", nil, nil, nil).Body)
	if !strings.Contains(faults, `"iot-delay"`) || !strings.Contains(faults, `"30ms"`) {
		t.Errorf("the configured fault is not in the active revision: %s", faults)
	}
	ovs := g.do("GET", "/overlays?kind=fault", nil, nil, nil).json(t)["items"].([]any)
	if len(ovs) != 1 || ovs[0].(map[string]any)["fault"].(map[string]any)["latency"] != "100ms" {
		t.Fatalf("the overlay of device A: %v", ovs)
	}
	if groups := string(g.do("GET", "/groups", nil, nil, nil).Body); !strings.Contains(groups, "api-made") {
		t.Errorf("the change the token made meanwhile is lost: %s", groups)
	}

	// the measured effect: 100 ms each way for A (its own fault wins), 30 ms each way for B (the network's)
	a := testbed.MustPing(t, top.A, testbed.ServerAddr, 20, 200*time.Millisecond)
	b := testbed.MustPing(t, top.B, testbed.ServerAddr, 20, 200*time.Millisecond)
	t.Logf("median round trip: A %v (before %v), B %v (before %v)", a.Median(), baseA, b.Median(), baseB)
	if d := a.Median() - baseA; d < 180*time.Millisecond || d > 400*time.Millisecond {
		t.Errorf("device A's round trip grew by %v, want about 200 ms (2 × 100 ms)", d)
	}
	if d := b.Median() - baseB; d < 50*time.Millisecond || d > 150*time.Millisecond {
		t.Errorf("device B's round trip grew by %v, want about 60 ms (2 × 30 ms of the network's fault)", d)
	}
}

// M14 test (plan §5): create a WireGuard client in the UI, scan its QR in the test (decoded content
// equals the download) and bring the tunnel up in the testbed — and, on the way, commit-confirm: an
// unconfirmed change is rolled back after the timeout and the UI shows it. The gateway runs on the real
// executor with the testbed's configuration (the hubs lab-hub and admin, the latter with the role
// management) and serves the built web app. In web/e2e-testbed/wireguard.spec.ts Chromium adds the client
// rC to lab-hub (the next free address 10.99.0.3 proposed), applies it, decodes the QR code of its
// configuration, checks it against the download and saves the file; this test brings the tunnel up from
// that file in the remote client's namespace and pings through it, and the browser waits until the client
// list shows rC online. Then it adds a client to admin, applies (the revision waits for confirmation) and
// waits until the gateway rolls back at the deadline and the UI says so. Back in Go: rC is in the active
// revision and its tunnel carries traffic, the management hub's client is gone, nothing waits.
func TestAWireGuardClientCreatedInTheBrowserBringsUpItsTunnel(t *testing.T) {
	web, browsers := webDir(t)
	g, top := newBedGW(t, func(o *options) {
		o.ui = os.DirFS(filepath.Join(web, "dist"))
		// long enough for the browser to see the countdown, short enough to wait for its end
		o.confirm = 90 * time.Second
	})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	if err := g.e.PollWireGuard(ctx, time.Second); err != nil {
		t.Fatal(err)
	}

	// once the browser saved the configuration, bring the tunnel up from it in the remote client's
	// namespace and keep traffic in it, so the gateway sees the handshake
	conf := filepath.Join(t.TempDir(), "wgrc.conf")
	up := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if _, err := os.Stat(conf); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		if err := os.Chmod(conf, 0o600); err != nil {
			up <- err
			return
		}
		_, err := top.RC.Run(ctx, "wg-quick", "up", conf)
		up <- err
		if err != nil {
			return
		}
		for ctx.Err() == nil {
			_, _ = top.RC.Run(ctx, "ping", "-c", "1", "-W", "1", "-n", "10.99.0.1")
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}()

	runBrowser(ctx, t, web, browsers, "wireguard.spec.ts", map[string]string{
		"CHAOSGW_URL":            g.ts.URL,
		"CHAOSGW_ADMIN_PASSWORD": adminPassword,
		"CHAOSGW_CONF_OUT":       conf,
	}, func() string {
		return "wireguard of the gateway:\n" + top.GW.Must("wg", "show") + "\nclients of lab-hub:\n" + string(g.do("GET", "/networks/lab-hub/clients", nil, nil, nil).Body)
	})
	select {
	case err := <-up:
		if err != nil {
			t.Fatalf("wg-quick up with the configuration the browser saved: %v", err)
		}
	default:
		t.Fatal("the browser flow ended without the tunnel brought up from the saved configuration")
	}

	// the configuration the browser saved is the client's: its tunnel carries traffic
	b, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, "Address = 10.99.0.3/32") || !strings.Contains(s, "PrivateKey = ") || strings.Contains(s, "<PRIVATE_KEY>") {
		t.Errorf("the saved configuration:\n%s", b)
	}
	if !pingOK(top.RC, "", "10.99.0.1") {
		t.Errorf("no traffic through the tunnel of the saved configuration\n%s\n%s", top.GW.Must("wg", "show"), top.RC.Must("wg", "show"))
	}
	g.mintToken("read")
	c := g.do("GET", "/networks/lab-hub/clients/rC", nil, nil, nil)
	if c.Status != 200 {
		t.Fatalf("client rC in the API: %d %s", c.Status, c.Body)
	}
	if v := c.json(t); v["config"].(map[string]any)["address"] != "10.99.0.3" || v["status"].(map[string]any)["online"] != true {
		t.Errorf("client rC in the API: %s", c.Body)
	}
	// the change nobody confirmed is gone, and nothing waits any more
	if r := g.do("GET", "/networks/admin/clients/adm2", nil, nil, nil); r.Status != 404 {
		t.Errorf("the rolled back client adm2: %d %s", r.Status, r.Body)
	}
	st := g.do("GET", "/state", nil, nil, nil).json(t)
	if st["pending_confirm"] != nil {
		t.Errorf("state after the rollback: %v", st)
	}
}
