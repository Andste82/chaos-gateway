//go:build appliance

package appliance_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/appliance"
)

// Environment of the level 2 tests:
//
//	CHAOSGW_APPLIANCE_IMAGE_TAR   the container image as `docker save | gzip` (required)
//	CHAOSGW_APPLIANCE_IMAGE_TAG   its tag, default chaos-gateway:appliance
//	CHAOSGW_APPLIANCE_RELEASES    comma-separated Ubuntu releases, default "24.04,26.04"
//	CHAOSGW_APPLIANCE_CACHE       where cloud images are kept, default a directory below the user cache
//	CHAOSGW_APPLIANCE_ARTIFACTS   where the logs of every run go, default the test's temporary directory
//	CHAOSGW_APPLIANCE_ALLOW_TCG   run without /dev/kvm (very slow)
func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func TestSmokeOnCleanUbuntuHosts(t *testing.T) {
	tar := os.Getenv("CHAOSGW_APPLIANCE_IMAGE_TAR")
	if tar == "" {
		t.Skip("CHAOSGW_APPLIANCE_IMAGE_TAR is not set: build the image and save it (see .github/workflows/nightly.yml)")
	}
	if !appliance.HasKVM() && os.Getenv("CHAOSGW_APPLIANCE_ALLOW_TCG") == "" {
		t.Skip("no /dev/kvm: level 2 needs a KVM-capable machine (plan Q1); CHAOSGW_APPLIANCE_ALLOW_TCG=1 runs it emulated")
	}
	for _, rel := range strings.Split(env("CHAOSGW_APPLIANCE_RELEASES", "24.04,26.04"), ",") {
		r, ok := appliance.Releases[strings.TrimSpace(rel)]
		if !ok {
			t.Fatalf("unknown release %q", rel)
		}
		for _, two := range []bool{false, true} {
			name := r.Name + "/three-ports"
			if two {
				name = r.Name + "/two-ports"
			}
			t.Run(name, func(t *testing.T) { smoke(t, r, two, tar) })
		}
	}
}

// bg runs a command on the host in the background; it is killed at the end of the test.
func bg(t *testing.T, h appliance.Host, argv ...string) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Run(context.Background(), appliance.Cmd{"pkill", "-f", strings.Join(argv[len(argv)-1:], " ")})
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

// serverScript answers every request with the address it came from: through a masquerading
// gateway that is the gateway's uplink address.
const serverScript = `import http.server

class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = ("client=%s\n" % self.client_address[0]).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass

http.server.HTTPServer(("203.0.113.10", 8080), H).serve_forever()
`

func gatewayConfig(two bool) string {
	mgmt := appliance.MACs.Mgmt
	if two {
		mgmt = appliance.MACs.Uplink // the management network lives behind the uplink interface
	}
	return fmt.Sprintf(`schema_version: 1
uplink:
  interface: {mac: '%s'}
  gateway: %s
management:
  interface: {mac: '%s'}
  allowed_sources: [%s]
networks:
  0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21:
    type: lan
    name: IoT
    interfaces: [{mac: '%s'}]
    address: %s/24
`, appliance.MACs.Uplink, appliance.ServerAddr, mgmt, appliance.MgmtNetwork, appliance.MACs.LAN, appliance.GatewayLAN)
}

func smoke(t *testing.T, rel appliance.Release, two bool, imageTar string) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	tag := env("CHAOSGW_APPLIANCE_IMAGE_TAG", "chaos-gateway:appliance")
	artifacts := filepath.Join(env("CHAOSGW_APPLIANCE_ARTIFACTS", t.TempDir()), strings.ReplaceAll(t.Name(), "/", "-"))
	cache := env("CHAOSGW_APPLIANCE_CACHE", filepath.Join(os.TempDir(), "chaosgw-appliance"))

	img, err := appliance.Fetch(ctx, nil, cache, rel)
	if err != nil {
		t.Fatal(err)
	}
	key, err := appliance.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	hostname := "gw-" + strings.ReplaceAll(rel.Name, ".", "")
	seed, err := appliance.WriteSeed(ctx, dir, appliance.Seed{Hostname: hostname, SSHPublicKey: key.Authorized, TwoPorts: two})
	if err != nil {
		t.Fatal(err)
	}

	// the host side: bridges, taps, the server and the client namespaces
	prefix := "a" + strings.ReplaceAll(rel.Name, ".", "")[:2] + map[bool]string{false: "3", true: "2"}[two]
	top := appliance.Topology{Prefix: prefix, TwoPorts: two}
	if err := top.Validate(); err != nil {
		t.Fatal(err)
	}
	h := appliance.Host{}
	plan := top.Plan()
	if err := h.Up(ctx, plan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Down(context.Background(), plan) })
	names := top.Names()
	script := filepath.Join(dir, "server.py")
	if err := os.WriteFile(script, []byte(serverScript), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := append(append([]string(nil), hArgv(h)...), "ip", "netns", "exec", names.NSServer, "python3", script)
	bg(t, h, argv...)

	// the gateway VM
	vm, err := appliance.Boot(ctx, h, dir, img, seed, top.NICs(), appliance.GatewayMgmt, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		lctx, lcancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer lcancel()
		if err := appliance.CollectLogs(lctx, vm, artifacts); err != nil {
			t.Logf("collecting the logs: %v", err)
		}
		vm.Close()
	})
	bootWait := 6 * time.Minute
	if !appliance.HasKVM() {
		bootWait = 25 * time.Minute
	}
	if err := vm.WaitSSH(ctx, bootWait); err != nil {
		t.Fatal(err)
	}
	must := func(cmd string) string {
		t.Helper()
		out, err := vm.Must(ctx, cmd)
		if err != nil {
			t.Fatalf("%v", err)
		}
		return out
	}
	must("sudo cloud-init status --wait")
	t.Logf("booted: %s", strings.TrimSpace(must("grep PRETTY_NAME /etc/os-release; uname -r")))

	// netplan owns the uplink and management interfaces, as on a real host
	if out := must("ip -4 -o addr show"); !strings.Contains(out, appliance.GatewayUp+"/24") || !strings.Contains(out, appliance.GatewayMgmt+"/24") {
		t.Fatalf("netplan did not configure the OS-owned interfaces:\n%s", out)
	}

	// the host setup: Docker, modules, forwarding
	if err := vm.PutFile(ctx, "../../deploy/host-setup.sh", "/opt/chaosgw/host-setup.sh", 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := vm.Must(ctx, "sudo sh /opt/chaosgw/host-setup.sh 2>&1"); err != nil {
		t.Fatalf("the host setup failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(must("sysctl -n net.ipv4.ip_forward")); got != "1" {
		t.Errorf("ip_forward is %q after the host setup", got)
	}
	must("test -s /etc/modules-load.d/chaos-gateway.conf && test -s /etc/sysctl.d/90-chaos-gateway.conf")
	must("sudo docker compose version")
	// a second run changes nothing
	if out := must("sudo sh /opt/chaosgw/host-setup.sh 2>&1"); strings.Count(out, "is up to date") != 2 {
		t.Errorf("the host setup is not idempotent:\n%s", out)
	}

	// the image and the executor container (plan §3.8)
	if err := vm.PutFile(ctx, imageTar, "/opt/chaosgw/image.tar.gz", 0o644); err != nil {
		t.Fatal(err)
	}
	must("gunzip -c /opt/chaosgw/image.tar.gz | sudo docker load")
	if err := vm.PutFile(ctx, "../../deploy/compose.executor.yaml", "/opt/chaosgw/compose.executor.yaml", 0o644); err != nil {
		t.Fatal(err)
	}
	compose := "sudo CHAOSGW_VERSION=" + versionOf(tag) + " docker compose -p chaosgw -f /opt/chaosgw/compose.executor.yaml"
	must(compose + " up -d")
	waitHealthy(t, ctx, vm, compose)

	// the minimal deployment: `chaosgw apply --file` through the executor's socket
	if err := vm.Put(ctx, "/opt/chaosgw/gateway.yaml", 0o644, strings.NewReader(gatewayConfig(two))); err != nil {
		t.Fatal(err)
	}
	out := must("sudo docker run --rm --network host -v chaosgw_chaosgw-run:/run/chaosgw -v /opt/chaosgw/gateway.yaml:/gateway.yaml:ro " + tag +
		" apply --file /gateway.yaml --socket /run/chaosgw/exec.sock 2>&1")
	t.Logf("apply: %s", strings.TrimSpace(out))

	// Docker's FORWARD policy is DROP: the executor keeps accept rules for its interfaces in DOCKER-USER
	if out := must("sudo iptables -S DOCKER-USER"); !strings.Contains(out, "ACCEPT") {
		t.Errorf("no accept rule in DOCKER-USER:\n%s", out)
	}

	// traffic from the client namespace through the VM to the server namespace
	client := func(args ...string) (string, error) {
		return h.Run(ctx, append(appliance.Cmd{"ip", "netns", "exec", names.NSClient}, args...))
	}
	var pingErr error
	for i := 0; i < 10; i++ { // ARP and the first packets after the apply
		if _, pingErr = client("ping", "-c", "2", "-W", "2", "-n", appliance.ServerAddr); pingErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if pingErr != nil {
		t.Fatalf("the client cannot reach the server through the gateway: %v", pingErr)
	}
	body, err := client("curl", "-s", "-m", "10", fmt.Sprintf("http://%s:8080/", appliance.ServerAddr))
	if err != nil {
		t.Fatalf("%v", err)
	}
	// the gateway masquerades towards the uplink: the server sees the gateway's address
	if strings.TrimSpace(body) != "client="+appliance.GatewayUp {
		t.Errorf("the server saw %q, want the gateway's uplink address %s (NAT)", strings.TrimSpace(body), appliance.GatewayUp)
	}
	// the management access survived the apply (anti-lockout): SSH still works
	must("true")
}

func hArgv(h appliance.Host) []string {
	if os.Getuid() == 0 {
		return nil
	}
	return []string{"sudo", "-n"}
}

func versionOf(tag string) string {
	if i := strings.LastIndex(tag, ":"); i >= 0 {
		return tag[i+1:]
	}
	return "latest"
}

func waitHealthy(t *testing.T, ctx context.Context, vm *appliance.VM, compose string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		out, _ := vm.Must(ctx, compose+" ps -q exec | xargs -r sudo docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}'")
		last = strings.TrimSpace(out)
		if last == "running healthy" {
			return
		}
		time.Sleep(3 * time.Second)
	}
	logs, _ := vm.Must(ctx, compose+" logs --tail 100 exec")
	t.Fatalf("the executor container is not healthy (%q):\n%s", last, logs)
}
