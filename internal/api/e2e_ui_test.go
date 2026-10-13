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

	out := t.TempDir()
	cmd := exec.CommandContext(ctx, filepath.Join(web, "node_modules", ".bin", "playwright"), "test", "--config", "playwright.testbed.config.ts")
	cmd.Dir = web
	cmd.Env = append(os.Environ(),
		"CHAOSGW_URL="+g.ts.URL,
		"CHAOSGW_SETUP_TOKEN="+g.setup,
		"CHAOSGW_LAN_ADDRESS="+testbed.LAN0Gateway+"/24",
		"CHAOSGW_UPLINK_GATEWAY="+testbed.ServerAddr,
		"CHAOSGW_DEVICE_MAC="+testbed.ClientAMAC,
		"CHAOSGW_DEVICE_IP="+testbed.ClientAAddr,
		"PLAYWRIGHT_BROWSERS_PATH="+browsers,
		"PLAYWRIGHT_OUTPUT_DIR="+filepath.Join(out, "results"),
		// the checkout may be read-only (the VM of level 1b): everything written goes to the test's directory
		"HOME="+out,
		"TMPDIR="+out,
		"CI=1",
	)
	res, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", res)
	if err != nil {
		// the page as the browser had it when the step failed (its accessibility tree: every
		// message the UI showed, e.g. the gateway's error after the apply)
		pages, _ := filepath.Glob(filepath.Join(out, "results", "*", "error-context.md"))
		for _, p := range pages {
			if b, rerr := os.ReadFile(p); rerr == nil {
				t.Logf("%s:\n%s", filepath.Base(filepath.Dir(p)), b)
			}
		}
		t.Fatalf("the browser flow failed: %v\nneighbors of the gateway:\n%s", err, top.GW.Must("ip", "neigh"))
	}

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
