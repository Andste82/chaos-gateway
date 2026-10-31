//go:build appliance

package appliance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/appliance"
)

// Level 2 of M28 (plan §3.8, §3.9, §4.5): the container deployment on a clean Ubuntu host with a test interface, in a VM, with the real
// Docker, the files the product ships (the host setup with --image, compose.yaml, update.sh) and nothing of the repository's own test
// helpers but the image. The environment is that of TestSmokeOnCleanUbuntuHosts (CHAOSGW_APPLIANCE_IMAGE_TAR and the others), and the
// host needs `udhcpc` (package udhcpc) for the DHCP client of the device.
//
//   - host setup: the netplan entry cloud-init wrote for the test interface is named with its file and line by the check, and removed
//     as the hint says; the setup then makes the data directories below /var/lib/chaosgw with their owners, copies compose.yaml, update.sh
//     and .env out of the image, and ends with "the test interfaces are ready";
//   - `docker compose up -d` starts every container in the order of plan §3.8 and they become healthy; the containers other than the
//     executor run without privileges, read from the container runtime (the privileged flag, the bounding set of capabilities and
//     no-new-privileges of the process, M28); the setup token is in the log of the API; the preflight of the setup status carries the
//     checks of the executor (M28) and no failed one;
//   - the setup wizard (POST /setup with that token, a login, the confirmation of revision 1), and a device that gets its lease from the
//     Kea container, which the API configured (M6a-10): the lease event arrives over the datagram socket with the service token, the
//     lease is in GET /networks/{id}/leases, the gateway answers the device's DNS from the service namespace next to the host's own
//     systemd-resolved (M6b-06) and the device reaches the server;
//   - S7: `netplan apply`, a restart of systemd-networkd and a NetworkManager that starts on the host leave the bridge, its address, the
//     table inet chaosgw and the traffic of the device alone;
//   - a reboot: Docker brings the containers back, the gateway rebuilds the test network from the confirmed revision and the device
//     gets the same lease again; forwarding, the modules and the sysctls are those of the host setup;
//   - the holder of the service namespace: its PID file naming another real container process is refused by the executor, which does not
//     attach that process's namespace (M6b-10); the next holder heals it;
//   - the update (deploy/update.sh): during an idle gateway it replaces every container and the device gets its gateway and its lease
//     back; a configuration of a newer schema is refused before anything stops; during an active run it waits and then refuses, and with
//     --force it proceeds, and the run ends `aborted` and its fault is gone (D35: the update stops the executor, which removes the test
//     networks and rebuilds them from the confirmed revision).
//
// Both topologies run all of it but S7 and the holder's PID file: those two (netplan, networkd and NetworkManager leaving the bridge of
// the test network alone, and the reused PID) do not depend on where the management network lies and run in the three-port topology
// only (plan §4.5).
func TestContainerDeploymentOnCleanUbuntuHosts(t *testing.T) {
	amd64Only(t)
	tar := os.Getenv("CHAOSGW_APPLIANCE_IMAGE_TAR")
	if tar == "" {
		t.Skip("CHAOSGW_APPLIANCE_IMAGE_TAR is not set: build the image and save it (see .github/workflows/nightly.yml)")
	}
	if !appliance.HasKVM() && os.Getenv("CHAOSGW_APPLIANCE_ALLOW_TCG") == "" {
		t.Skip("no /dev/kvm: level 2 needs a KVM-capable machine (plan Q1); CHAOSGW_APPLIANCE_ALLOW_TCG=1 runs it emulated")
	}
	if _, err := exec.LookPath("udhcpc"); err != nil {
		t.Fatal("udhcpc is not installed: the device of the test gets its lease with it (apt-get install udhcpc)")
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
			t.Run(name, func(t *testing.T) { deployment(t, r, two, tar) })
		}
	}
}

// udhcpcScript is the script of the DHCP client of the device: it configures the interface with the lease and writes it where the test
// reads it ("ip router dns").
const udhcpcScript = `#!/bin/sh
case "$1" in
bound | renew)
	ip addr flush dev "$interface"
	ip addr add "$ip/$mask" dev "$interface"
	ip route replace default via "${router%% *}" dev "$interface"
	echo "$ip ${router%% *} ${dns%% *}" >"$LEASE_OUT"
	;;
esac
`

// dnsQuery asks a server for the A record of a name over UDP and prints the address in the answer.
const dnsQuery = `import socket, struct, sys
server, name = sys.argv[1], sys.argv[2]
q = struct.pack(">HHHHHH", 0x1234, 0x0100, 1, 0, 0, 0)
for label in name.split("."):
    q += bytes([len(label)]) + label.encode()
q += b"\x00" + struct.pack(">HH", 1, 1)
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)
s.sendto(q, (server, 53))
a, _ = s.recvfrom(512)
rcode, answers = a[3] & 15, struct.unpack(">H", a[6:8])[0]
if rcode != 0 or answers < 1:
    print("rcode=%d answers=%d" % (rcode, answers))
    sys.exit(1)
print(".".join(str(b) for b in a[-4:]))
`

// removeLan0 deletes the entry of lan0 from the netplan files, as the hint of the check says.
const removeLan0 = `import glob, yaml
for f in glob.glob("/etc/netplan/*.yaml"):
    d = yaml.safe_load(open(f))
    eth = (d.get("network") or {}).get("ethernets") or {}
    if "lan0" in eth:
        del eth["lan0"]
        open(f, "w").write(yaml.safe_dump(d))
`

func deployment(t *testing.T, rel appliance.Release, two bool, imageTar string) {
	ctx, cancel := context.WithTimeout(context.Background(), 85*time.Minute)
	defer cancel()
	tag := env("CHAOSGW_APPLIANCE_IMAGE_TAG", "chaos-gateway:appliance")
	artifacts := filepath.Join(env("CHAOSGW_APPLIANCE_ARTIFACTS", t.TempDir()), strings.ReplaceAll(t.Name(), "/", "-"))
	cache := env("CHAOSGW_APPLIANCE_CACHE", filepath.Join(os.TempDir(), "chaosgw-appliance"))
	full := !two

	img, err := appliance.Fetch(ctx, nil, cache, rel)
	if err != nil {
		t.Fatal(err)
	}
	key, err := appliance.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	seed, err := appliance.WriteSeed(ctx, dir, appliance.Seed{Hostname: "gd-" + strings.ReplaceAll(rel.Name, ".", ""), SSHPublicKey: key.Authorized, TwoPorts: two})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "d" + strings.ReplaceAll(rel.Name, ".", "")[:2] + map[bool]string{false: "3", true: "2"}[two]
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
	bg(t, h, append(append([]string(nil), hArgv(h)...), "ip", "netns", "exec", names.NSServer, "python3", script)...)

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
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
		return out
	}
	// output runs a command whose exit status does not matter and returns what it printed
	output := func(cmd string) string {
		t.Helper()
		r, err := vm.Run(ctx, cmd+" 2>&1", nil)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return r.Stdout
	}
	must("sudo cloud-init status --wait")
	// cloud-init must not write the netplan entry of the test interface again at the next boot
	must("echo 'network: {config: disabled}' | sudo tee /etc/cloud/cloud.cfg.d/99-chaosgw-no-network.cfg >/dev/null")

	// ---- the host setup, as the installation guide says: Docker and the files first, then the image, then the test interface
	if err := vm.PutFile(ctx, "../../deploy/host-setup.sh", "/opt/chaosgw/host-setup.sh", 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := vm.Must(ctx, "sudo sh /opt/chaosgw/host-setup.sh 2>&1"); err != nil {
		t.Fatalf("the host setup failed: %v\n%s", err, out)
	}
	// the data directories of plan §3.6, with their owners
	if out := must("stat -c '%n %u:%g %a' /var/lib/chaosgw/state /var/lib/chaosgw/secrets /var/lib/chaosgw/api /var/lib/chaosgw/service /var/lib/chaosgw/tlsca /var/lib/chaosgw/captures /var/lib/chaosgw/exec /var/lib/chaosgw/kea"); strings.TrimSpace(out) != strings.TrimSpace(`
/var/lib/chaosgw/state 65532:65532 755
/var/lib/chaosgw/secrets 65532:65532 700
/var/lib/chaosgw/api 65532:65532 755
/var/lib/chaosgw/service 65532:65532 755
/var/lib/chaosgw/tlsca 65532:65532 755
/var/lib/chaosgw/captures 65532:65532 755
/var/lib/chaosgw/exec 0:0 755
/var/lib/chaosgw/kea 0:0 755`) {
		t.Errorf("the data directories after the host setup:\n%s", out)
	}
	if err := vm.PutFile(ctx, imageTar, "/opt/chaosgw/image.tar.gz", 0o644); err != nil {
		t.Fatal(err)
	}
	must("gunzip -c /opt/chaosgw/image.tar.gz | sudo docker load")

	// the netplan hint: the entry cloud-init wrote for the test interface is named with its file and line, and the host setup
	// writes the networkd rule and exits 1 until the entry is gone
	r, err := vm.Run(ctx, "sudo sh /opt/chaosgw/host-setup.sh --image "+tag+" --test-interface lan0 2>&1", nil)
	if err != nil || r.Exit != 1 || !strings.Contains(r.Stdout, "FAIL  netplan: /etc/netplan/") || !strings.Contains(r.Stdout, "names lan0") ||
		!strings.Contains(r.Stdout, "delete the entry of lan0") || !strings.Contains(r.Stdout, "wrote /etc/systemd/network/10-chaosgw-lan0.network") {
		t.Fatalf("the check of the test interface before the netplan entry is gone: %v (exit %d)\n%s", err, r.Exit, r.Stdout)
	}
	if err := vm.Put(ctx, "/opt/chaosgw/remove-lan0.py", 0o644, strings.NewReader(removeLan0)); err != nil {
		t.Fatal(err)
	}
	must("sudo python3 /opt/chaosgw/remove-lan0.py")
	// the name stays: a link file keeps lan0 for the NIC across the reboot (it names the port, it does not configure it)
	must("printf '[Match]\\nMACAddress=" + appliance.MACs.LAN + "\\n\\n[Link]\\nName=lan0\\n' | sudo tee /etc/systemd/network/05-lan0.link >/dev/null")
	must("sudo netplan apply")
	out := must("sudo sh /opt/chaosgw/host-setup.sh --image " + tag + " --test-interface lan0 2>&1")
	if !strings.Contains(out, "the test interfaces are ready: lan0") {
		t.Fatalf("the test interface is not ready after the netplan entry was removed:\n%s", out)
	}
	if !strings.Contains(out, "wrote /opt/chaosgw/.env") && !strings.Contains(out, "/opt/chaosgw/.env is kept") {
		t.Errorf("the host setup says nothing of .env:\n%s", out)
	}
	must("sudo test -x /opt/chaosgw/update.sh && sudo test -s /opt/chaosgw/compose.yaml && grep -q '^CHAOSGW_VERSION=" + versionOf(tag) + "$' /opt/chaosgw/.env")
	// a second run changes nothing
	if out := must("sudo sh /opt/chaosgw/host-setup.sh --image " + tag + " --test-interface lan0 2>&1"); strings.Count(out, "is up to date") != 3 || strings.Contains(out, "made /var/lib") {
		t.Errorf("the host setup is not idempotent:\n%s", out)
	}

	// ---- up, in the order of plan §3.8
	compose := "cd /opt/chaosgw && sudo docker compose"
	must(compose + " config -q")
	must(compose + " up -d")
	for _, s := range []string{"exec", "api", "svcns", "kea", "bird"} {
		waitHealthy(t, ctx, vm, compose, s)
	}
	for _, s := range []string{"dns", "tls"} {
		waitRunning(t, ctx, vm, compose, s)
	}
	started := func(service string) string {
		return strings.TrimSpace(must("sudo docker inspect -f '{{.State.StartedAt}}' $(" + compose + " ps -a -q " + service + ")"))
	}
	for _, s := range []string{"api", "svcns", "kea", "bird"} {
		if started("exec") > started(s) {
			t.Errorf("%s started (%s) before the executor (%s)", s, started(s), started("exec"))
		}
	}

	// the containers other than the executor run without privileges, from the container runtime
	// the executor is privileged: its bounding set is the host's own, whatever the kernel's last capability is
	hostBnd := regexp.MustCompile(`CapBnd:\s+(\w+)`).FindStringSubmatch(must("grep CapBnd /proc/1/status"))
	if hostBnd == nil {
		t.Fatal("the host's bounding set of capabilities cannot be read")
	}
	for service, want := range map[string]string{
		"exec": hostBnd[1], "api": "0000000000000000", "svcns": "0000000000000000", "tls": "0000000000000000",
		"dns": "0000000000000400", "kea": "000000000000240b", "bird": "0000000000003400",
	} {
		id := strings.TrimSpace(must(compose + " ps -a -q " + service))
		privileged := strings.TrimSpace(must("sudo docker inspect -f '{{.HostConfig.Privileged}} {{.HostConfig.ReadonlyRootfs}}' " + id))
		if wantPriv := map[bool]string{true: "true true", false: "false true"}[service == "exec"]; privileged != wantPriv {
			t.Errorf("%s: privileged and read-only root are %q, want %q", service, privileged, wantPriv)
		}
		// the main process is read from the host by its PID: exec and svcns share the host's PID namespace (pid: host), where /proc/1 of
		// the container is the host's init and not the container's process
		pid := strings.TrimSpace(must("sudo docker inspect -f '{{.State.Pid}}' " + id))
		if pid == "" || pid == "0" || pid == "1" {
			t.Fatalf("%s: the host's PID of its main process is %q", service, pid)
		}
		status := must("sudo grep -E '^(CapBnd|NoNewPrivs):' /proc/" + pid + "/status")
		if m := regexp.MustCompile(`CapBnd:\s+(\w+)`).FindStringSubmatch(status); m == nil || m[1] != want {
			t.Errorf("%s: the bounding set of capabilities of its main process is %v, want %s\n%s", service, m, want, status)
		}
		if service != "exec" && !regexp.MustCompile(`NoNewPrivs:\s+1`).MatchString(status) {
			t.Errorf("%s: no-new-privileges is not set on its main process:\n%s", service, status)
		}
	}
	// the non-privileged containers write nothing outside their mounts: the root file system is read-only (checked above); the API's
	// directories are the API's user's
	must("sudo docker exec $(" + compose + " ps -q api) sh -c 'touch /var/lib/chaosgw/state/probe && rm /var/lib/chaosgw/state/probe'")

	// ---- the setup token is in the log of the API, and the setup status carries the checks of the executor
	var setupToken string
	for deadline := time.Now().Add(2 * time.Minute); setupToken == "" && time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if m := regexp.MustCompile(`Setup token: (\S+)`).FindStringSubmatch(output(compose + " logs --no-color api")); m != nil {
			setupToken = m[1]
		}
	}
	if setupToken == "" {
		t.Fatalf("the log of the API holds no setup token:\n%s", output(compose+" logs --no-color api"))
	}
	api := apiClient{t: t, ctx: ctx, vm: vm}
	st, setup := api.call("GET", "/setup", nil)
	if st != 200 || setup["completed"] != false {
		t.Fatalf("GET /setup: %d %v", st, setup)
	}
	failed := false
	have := map[string]string{}
	for _, c := range setup["preflight"].(map[string]any)["checks"].([]any) {
		m := c.(map[string]any)
		have[m["id"].(string)], _ = m["status"].(string)
		if m["status"] == "fail" {
			failed = true
			t.Errorf("a check of the preflight fails on a host that was prepared: %v", m)
		}
	}
	for _, id := range []string{"host.tools", "host.modules", "host.netns", "host.proc_sys", "host.ip_forward", "host.firewall", "host.data.captures", "interface.lan0.os_config"} {
		if _, ok := have[id]; !ok {
			t.Errorf("the preflight of the setup status has no check %s: %v", id, have)
		}
	}
	if setup["preflight"].(map[string]any)["status"] == "fail" && !failed {
		t.Errorf("the preflight fails: %v", setup["preflight"])
	}

	// ---- the setup wizard; the network has a DHCP pool and static DNS entries, the gateway the unprivileged ping for the diagnostics
	cfg := appliance.GatewayConfig(two)
	cfg = strings.Replace(cfg, "    address: "+appliance.GatewayLAN+"/24\n", "    address: "+appliance.GatewayLAN+"/24\n"+
		"    dhcp:\n      pools: [{start: 10.10.0.100, end: 10.10.0.199}]\n      lease_time: 1h\n"+
		"    dns:\n      static_entries:\n        - {name: probe.iot.test, addresses: [10.10.0.5]}\n", 1)
	if !strings.Contains(cfg, "static_entries") {
		t.Fatal("the test configuration has no DNS entry")
	}
	var cfgDoc map[string]any
	if err := yaml.Unmarshal([]byte(cfg), &cfgDoc); err != nil {
		t.Fatal(err)
	}
	const adminPassword = "correct horse battery staple"
	if st, m := api.call("POST", "/setup", map[string]any{"admin_password": adminPassword, "configuration": cfgDoc}, "X-Setup-Token: "+setupToken); st != 200 || m["revision"] != float64(1) || m["status"] != "pending_confirm" {
		t.Fatalf("setup: %d %v", st, m)
	}
	st, session := api.call("POST", "/auth/login", map[string]any{"password": adminPassword})
	csrf, _ := session["csrf_token"].(string)
	if st != 200 || csrf == "" {
		t.Fatalf("login: %d %v", st, session)
	}
	if st, m := api.call("POST", "/revisions/1/confirm", nil, "X-CSRF-Token: "+csrf); st != 200 {
		t.Fatalf("confirm revision 1: %d %v", st, m)
	}
	st, created := api.call("POST", "/auth/tokens", map[string]any{"name": "level2", "scope": "full"}, "X-CSRF-Token: "+csrf)
	token, _ := created["token"].(string)
	if st != 201 || token == "" {
		t.Fatalf("token: %d %v", st, created)
	}
	api.bearer = "Authorization: Bearer " + token
	if err := vm.Put(ctx, "/opt/chaosgw/token", 0o600, strings.NewReader(token)); err != nil {
		t.Fatal(err)
	}
	waitBridge(t, ctx, vm, "br-iot")

	// ---- the device: a lease from the Kea container, the lease event of its hook, DNS from the service namespace, the server
	lease := newDevice(t, ctx, h, names, dir)
	events := "/tmp/events.sse"
	startEventStream := func() {
		must("rm -f " + events + "; sudo sh -c 'setsid nohup timeout 900 curl -sk -N -H \"" + api.bearer + "\" \"https://" + appliance.GatewayMgmt + ":8443/api/v1/events?types=dhcp_lease\" >" + events + " 2>&1 &'")
	}
	startEventStream()
	time.Sleep(3 * time.Second)
	ip, router, dnsServer := lease.get()
	if !regexp.MustCompile(`^10\.10\.0\.(1[0-9][0-9])$`).MatchString(ip) || router != appliance.GatewayLAN || dnsServer != appliance.GatewayLAN {
		t.Fatalf("the lease of the device: ip %s, router %s, dns %s", ip, router, dnsServer)
	}
	waitFor(t, 60*time.Second, "the lease event of the hook in the stream of the API", func() bool {
		return strings.Contains(output("cat "+events), appliance.ClientMAC) // ClientMAC is the MAC of the client namespace's interface
	})
	if got := output("cat " + events); !strings.Contains(got, "dhcp_lease") || !strings.Contains(got, ip) {
		t.Errorf("the lease event does not carry the address %s:\n%s", ip, got)
	}
	st, leases := api.call("GET", "/networks/0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21/leases", nil)
	if st != 200 || !strings.Contains(fmt.Sprint(leases), ip) {
		t.Errorf("GET /networks/{id}/leases: %d %v", st, leases)
	}
	if logs := output(compose + " logs --no-color kea"); strings.Contains(logs, "chaosgw kea-hook:") {
		t.Errorf("the hook of Kea reports a failure:\n%s", logs)
	}
	// the hook's other way to the API (M6a-10): compose gives it the datagram socket, so the lease above never used the service token.
	// Run the hook in the Kea container with that socket missing: it falls back to HTTP, with the service token and the API's
	// certificate from the mount of /var/lib/chaosgw/service, and the API accepts it (a 401 or a certificate error is a failed hook)
	const viaHTTPMAC = "02:00:00:00:99:01"
	must(compose + " exec -T -e CHAOSGW_KEA_EVENTS_SOCKET=/run/chaosgw-kea-events/missing.sock -e KEA_LEASE4_ADDRESS=10.10.0.177 -e KEA_LEASE4_HWADDR=" + viaHTTPMAC +
		" -e KEA_SUBNET_ID=1 -e KEA_LEASE4_VALID_LIFETIME=60 kea /usr/share/kea/scripts/chaosgw-kea-hook lease4_select 2>&1")
	waitFor(t, 30*time.Second, "the lease event the hook posted over HTTP with the service token", func() bool {
		return strings.Contains(output("cat "+events), viaHTTPMAC)
	})
	// and with a token that is wrong the same call fails, so the check above is the token's
	if r, err := vm.Run(ctx, compose+" exec -T -e CHAOSGW_KEA_EVENTS_SOCKET=/run/chaosgw-kea-events/missing.sock -e CHAOSGW_SERVICE_TOKEN_FILE=/etc/hostname -e KEA_LEASE4_ADDRESS=10.10.0.178 -e KEA_LEASE4_HWADDR=02:00:00:00:99:02 -e KEA_SUBNET_ID=1 kea /usr/share/kea/scripts/chaosgw-kea-hook lease4_select 2>&1", nil); err != nil || r.Exit == 0 || !strings.Contains(r.Stdout, "chaosgw kea-hook: the API answered 401") {
		t.Errorf("the hook with a token the API does not know: %v exit %d\n%s", err, r.Exit, r.Stdout)
	}
	// the control socket belongs to the API's group, so the API can talk to Kea and nobody else (M6a-10)
	if got := strings.TrimSpace(must(compose + " exec -T kea stat -c '%g %a' /run/kea/kea4-ctrl.sock")); !strings.HasPrefix(got, "65532 ") {
		t.Errorf("the group and mode of Kea's control socket are %q, want group 65532", got)
	}
	if got := lease.dns(appliance.GatewayLAN, "probe.iot.test"); got != "10.10.0.5" {
		t.Errorf("the gateway's DNS answers %q for the static entry", got)
	}
	reaches := func(what string) {
		t.Helper()
		var lastErr error
		for i := 0; i < 15; i++ {
			if _, lastErr = lease.client("curl", "-s", "-m", "5", "http://"+appliance.ServerAddr+":8080/"); lastErr == nil {
				return
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("%s: the device cannot reach the server through the gateway: %v", what, lastErr)
	}
	reaches("after the lease")

	// Docker's address pools (plan §3.8, §4.5): an address of the host inside docker0's range is reported by the preflight, with the
	// fix, and the report is clean again once it is gone (the executor caches its checks for ten seconds)
	poolCheck := func() map[string]any {
		_, report := api.call("GET", "/system/preflight", nil)
		for _, c := range report["checks"].([]any) {
			if m := c.(map[string]any); m["id"] == "host.docker_pools" {
				return m
			}
		}
		return nil
	}
	if c := poolCheck(); c == nil || c["status"] != "ok" {
		t.Errorf("the address pools of Docker before a clash: %v", c)
	}
	must("sudo ip link add cgclash type dummy && sudo ip addr add 172.17.77.1/24 dev cgclash")
	waitFor(t, 60*time.Second, "the preflight to report the network on the range of docker0", func() bool {
		c := poolCheck()
		return c != nil && c["status"] == "warn" && strings.Contains(fmt.Sprint(c["message"]), "cgclash 172.17.77.0/24 overlaps docker0 172.17.0.0/16") && c["fix"] != nil
	})
	must("sudo ip link del cgclash")
	waitFor(t, 60*time.Second, "the preflight to report no clash again", func() bool {
		c := poolCheck()
		return c != nil && c["status"] == "ok"
	})

	// the DNS proxy next to the host's own systemd-resolved (M6b-06): the stub resolver holds 127.0.0.53:53 in the host's namespace, the
	// proxy listens in the service namespace and neither takes the other's port
	if out := must("systemctl is-active systemd-resolved"); strings.TrimSpace(out) != "active" {
		t.Errorf("systemd-resolved is %q", strings.TrimSpace(out))
	}
	resolvedStays := func(what string) {
		t.Helper()
		if out := output("sudo ss -lunpH 'sport = :53'"); !strings.Contains(out, "127.0.0.53") || !strings.Contains(out, "systemd-resolve") || strings.Contains(out, "chaosgw") {
			t.Errorf("%s: the UDP listeners on port 53 of the host are not systemd-resolved's alone:\n%s", what, out)
		}
		if r, err := vm.Run(ctx, "resolvectl query localhost 2>&1", nil); err != nil || r.Exit != 0 || !strings.Contains(r.Stdout, "127.0.0.1") {
			t.Errorf("%s: systemd-resolved does not answer for the host: %v %s", what, err, r.Stdout)
		}
	}
	resolvedStays("with the gateway running")
	if out := output("sudo ip netns exec cgsvc ss -lunpH 'sport = :53'"); !strings.Contains(out, "chaosgw") {
		t.Errorf("the DNS proxy does not listen on port 53 in the service namespace:\n%s", out)
	}

	// S7 (three ports): the operating system's network tools leave the gateway's interfaces, rules and traffic alone
	untouched := func(what string) {
		t.Helper()
		if out := output("ip -4 -o addr show dev br-iot"); !strings.Contains(out, appliance.GatewayLAN+"/24") {
			t.Errorf("%s: the bridge of the test network lost its address:\n%s", what, out)
		}
		if out := output("sudo nft list tables"); !strings.Contains(out, "inet chaosgw") {
			t.Errorf("%s: the table inet chaosgw is gone:\n%s", what, out)
		}
		if out := output("ip -o link show lan0"); !strings.Contains(out, "master br-iot") {
			t.Errorf("%s: lan0 is no longer a port of the bridge:\n%s", what, out)
		}
		reaches(what)
	}
	if full {
		must("sudo netplan apply")
		time.Sleep(5 * time.Second)
		untouched("after netplan apply")
		must("sudo systemctl restart systemd-networkd && sudo networkctl reload")
		time.Sleep(5 * time.Second)
		untouched("after a restart of systemd-networkd")
		if out := must("networkctl list --no-legend --no-pager lan0"); !strings.Contains(out, "unmanaged") {
			t.Errorf("systemd-networkd manages lan0 after its restart:\n%s", out)
		}
		// NetworkManager on the host: Ubuntu's default leaves ethernet and bridges to netplan's renderer
		must("sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q network-manager >/dev/null")
		must("sudo systemctl restart NetworkManager")
		time.Sleep(10 * time.Second)
		untouched("with NetworkManager running")
		if out := must("nmcli -t -f DEVICE,STATE device"); !regexp.MustCompile(`(?m)^lan0:unmanaged$`).MatchString(out) || !regexp.MustCompile(`(?m)^br-iot:unmanaged$`).MatchString(out) {
			t.Errorf("NetworkManager manages the interfaces of the gateway:\n%s", out)
		}
		resolvedStays("with NetworkManager running")
	}

	// ---- the holder of the service namespace (M6b-10, three ports)
	if full {
		holderPIDFileReusedByAnotherProcess(t, ctx, vm, compose, tag, must, output)
	}

	// ---- a reboot: Docker brings the containers back; the gateway rebuilds the test network from the confirmed revision
	bootID := strings.TrimSpace(must("cat /proc/sys/kernel/random/boot_id"))
	_, _ = vm.Run(ctx, "sudo systemctl reboot", nil)
	time.Sleep(30 * time.Second)
	if err := vm.WaitSSH(ctx, bootWait); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(must("cat /proc/sys/kernel/random/boot_id")); got == bootID {
		t.Fatal("the machine did not reboot")
	}
	must("sudo cloud-init status --wait || true")
	if got := strings.TrimSpace(must("sysctl -n net.ipv4.ip_forward")); got != "1" {
		t.Errorf("ip_forward is %q after the reboot", got)
	}
	if got := strings.TrimSpace(must("sysctl -n net.ipv4.ping_group_range")); got != "0\t2147483647" {
		t.Errorf("ping_group_range is %q after the reboot", got)
	}
	must("lsmod | grep -q '^sch_netem'")
	for _, s := range []string{"exec", "api", "svcns", "kea", "bird"} {
		waitHealthy(t, ctx, vm, compose, s)
	}
	waitBridge(t, ctx, vm, "br-iot")
	// the lease and the gateway are back: the same address, from the lease file the Kea container keeps below /var/lib/chaosgw/kea
	ip2, router2, _ := lease.get()
	if ip2 != ip || router2 != appliance.GatewayLAN {
		t.Errorf("after the reboot the device got %s via %s, want %s via %s", ip2, router2, ip, appliance.GatewayLAN)
	}
	reaches("after the reboot")
	st, state := api.call("GET", "/state", nil)
	if st != 200 || state["safe_mode"] == true {
		t.Errorf("GET /state after the reboot: %d %v", st, state)
	}
	if full {
		resolvedStays("after the reboot")
	}

	// ---- updates (deploy/update.sh)
	must("sudo docker tag " + tag + " chaos-gateway:next && sudo docker tag " + tag + " chaos-gateway:next2")
	update := func(args ...string) appliance.Result {
		t.Helper()
		r, err := vm.Run(ctx, "sudo /opt/chaosgw/update.sh --no-pull --token-file /opt/chaosgw/token "+strings.Join(args, " ")+" 2>&1", nil)
		if err != nil {
			t.Fatalf("update.sh: %v", err)
		}
		return r
	}
	image := func(service string) string {
		return strings.TrimSpace(must("sudo docker inspect -f '{{.Config.Image}}' $(" + compose + " ps -q " + service + ")"))
	}
	containerID := func(service string) string { return strings.TrimSpace(must(compose + " ps -q " + service)) }
	// settled checks what an update leaves: every container runs the version, the gateway is healthy again, and the device has its
	// gateway and its DHCP back (the same lease) and reaches the server
	settled := func(version, what string) {
		t.Helper()
		for _, s := range []string{"exec", "api", "svcns", "kea", "bird", "dns", "tls"} {
			if image(s) != "chaos-gateway:"+version {
				t.Errorf("%s: %s runs %s", what, s, image(s))
			}
		}
		must("grep -q '^CHAOSGW_VERSION=" + version + "$' /opt/chaosgw/.env")
		for _, s := range []string{"exec", "api", "svcns", "kea", "bird"} {
			waitHealthy(t, ctx, vm, compose, s)
		}
		waitBridge(t, ctx, vm, "br-iot")
		ip3, router3, _ := lease.get()
		if ip3 != ip || router3 != appliance.GatewayLAN {
			t.Errorf("%s: the device got %s via %s, want %s via %s", what, ip3, router3, ip, appliance.GatewayLAN)
		}
		reaches(what)
		if st, state := api.call("GET", "/state", nil); st != 200 || state["safe_mode"] == true {
			t.Errorf("GET /state %s: %d %v", what, st, state)
		}
	}

	// an idle gateway: no run to wait for, and every container is replaced
	{
		before := containerID("exec")
		r := update("--version next")
		if r.Exit != 0 || !strings.Contains(r.Stdout, "updated to chaos-gateway:next") || !strings.Contains(r.Stdout, "no run is active") {
			t.Fatalf("the update of an idle gateway: exit %d\n%s", r.Exit, r.Stdout)
		}
		if containerID("exec") == before {
			t.Error("the executor was not replaced by the update of an idle gateway")
		}
		settled("next", "after the update of an idle gateway")
	}

	// a configuration of a newer schema is refused before anything stops
	{
		before := containerID("exec")
		must("sudo cp /var/lib/chaosgw/state/config.json /opt/chaosgw/config.json.bak")
		must("sudo sed -i 's/\"schema_version\": 1/\"schema_version\": 7/' /var/lib/chaosgw/state/config.json")
		r := update("--version next2")
		must("sudo cp /opt/chaosgw/config.json.bak /var/lib/chaosgw/state/config.json")
		if r.Exit != 4 || !strings.Contains(r.Stdout, "schema version 7") || !strings.Contains(r.Stdout, "not updating") {
			t.Fatalf("the update to a version that cannot read the configuration: exit %d\n%s", r.Exit, r.Stdout)
		}
		if after := containerID("exec"); after != before || image("exec") != "chaos-gateway:next" {
			t.Errorf("the refused update touched the executor: %s -> %s, image %s", before, after, image("exec"))
		}
		reaches("after the refused update")
	}

	// an active run: the update waits, then refuses (exit 3); with --force it proceeds and the run ends aborted
	var runID string
	{
		body := map[string]any{"lease": "30m", "inline": map[string]any{
			"name": "update-run", "target": map[string]any{"network": "IoT"}, "preconditions": map[string]any{"skip": []string{"target_online"}},
			"steps": []any{
				map[string]any{"id": "slow", "at": "0s", "fault": map[string]any{"latency": "20ms"}},
				map[string]any{"id": "end", "at": "20m", "remove": "slow"},
			}}}
		st, run := api.call("POST", "/runs", body)
		runID, _ = run["id"].(string)
		if st != 201 && st != 202 && st != 200 || runID == "" {
			t.Fatalf("POST /runs: %d %v", st, run)
		}
		waitFor(t, 2*time.Minute, "the run to be running", func() bool {
			_, m := api.call("GET", "/runs/"+runID, nil)
			return m["status"] == "running"
		})
		if st, busy := api.call("GET", "/system/busy", nil); st != 200 || busy["busy"] != true {
			t.Fatalf("GET /system/busy during a run: %d %v", st, busy)
		}
		before := containerID("exec")
		r := update("--version next2 --timeout 10")
		if r.Exit != 3 || !strings.Contains(r.Stdout, "waiting for the runs to end") || !strings.Contains(r.Stdout, "nothing was changed") {
			t.Fatalf("the update during a run: exit %d\n%s", r.Exit, r.Stdout)
		}
		if containerID("exec") != before {
			t.Error("the update that waited for a run restarted the executor")
		}
		if _, m := api.call("GET", "/runs/"+runID, nil); m["status"] != "running" {
			t.Errorf("the run after the update that waited: %v", m["status"])
		}
	}
	execBefore := containerID("exec")
	r = update("--version next2", "--timeout 5", "--force")
	if r.Exit != 0 || !strings.Contains(r.Stdout, "updated to chaos-gateway:next2") {
		t.Fatalf("the update: exit %d\n%s", r.Exit, r.Stdout)
	}
	if containerID("exec") == execBefore {
		t.Error("the executor was not replaced by the update")
	}
	settled("next2", "after the update during a run")
	// D35: the run ended aborted, and its fault is gone with the test networks that were rebuilt
	waitFor(t, 2*time.Minute, "the run to end aborted", func() bool {
		_, m := api.call("GET", "/runs/"+runID, nil)
		return m["status"] == "aborted"
	})
	if out := output("sudo tc qdisc show dev br-iot"); strings.Contains(out, "netem") {
		t.Errorf("the fault of the aborted run is still in force:\n%s", out)
	}
	if _, busy := api.call("GET", "/system/busy", nil); busy["busy"] != false {
		t.Errorf("busy after the update: %v", busy)
	}
}

// holderPIDFileReusedByAnotherProcess is the test of M6b-10 on the real stack: the holder of the service namespace is stopped, its PID
// file names another real container process (the PID was reused, the inode is that of the old holder's namespace), and the executor
// refuses to attach that process's namespace; the next holder heals it.
func holderPIDFileReusedByAnotherProcess(t *testing.T, ctx context.Context, vm *appliance.VM, compose, tag string, must func(string) string, output func(string) string) {
	t.Helper()
	nsInode := func(path string) string {
		return strings.TrimSpace(output("sudo stat -L -c %i " + path))
	}
	service := nsInode("/run/netns/cgsvc")
	if service == "" || strings.Contains(service, "No such") {
		t.Fatalf("there is no service namespace to start from: %s", service)
	}
	pidFile := strings.Fields(must("sudo docker run --rm --user 0:0 --entrypoint cat -v chaosgw_chaosgw-svcns:/p:ro " + tag + " /p/pid"))
	if len(pidFile) != 2 {
		t.Fatalf("the PID file of the holder is %v, want \"<pid> <inode>\"", pidFile)
	}
	must(compose + " stop svcns")
	// another real container process in the host's PID namespace, in a network namespace of its own
	must("sudo docker rm -f chaosgw-pidreuse >/dev/null 2>&1; sudo docker run -d --user 0:0 --name chaosgw-pidreuse --pid host --network none --entrypoint sleep " + tag + " infinity")
	other := strings.TrimSpace(must("sudo docker inspect -f '{{.State.Pid}}' chaosgw-pidreuse"))
	otherInode := nsInode("/proc/" + other + "/ns/net")
	if otherInode == pidFile[1] || otherInode == service {
		t.Fatalf("the other process is in the namespace of the holder (inode %s): the test checks nothing", otherInode)
	}
	must("sudo docker run --rm --user 65532:65532 --entrypoint sh -v chaosgw_chaosgw-svcns:/p " + tag + " -c 'echo \"$0 $1\" >/p/pid' " + other + " " + pidFile[1])
	// the watch of the engine reads the PID file again within seconds, and the apply it triggers is refused
	waitFor(t, 3*time.Minute, "the executor to refuse the reused PID", func() bool {
		return strings.Contains(output(compose+" logs --no-color api"), "PID was reused")
	})
	if got := nsInode("/run/netns/cgsvc"); got == otherInode {
		t.Errorf("the service namespace is the namespace of the other process (inode %s): the executor attached a reused PID", got)
	}
	// the next holder writes its own PID file and heals the namespace
	must("sudo docker rm -f chaosgw-pidreuse >/dev/null")
	must(compose + " up -d --force-recreate svcns dns tls")
	for _, s := range []string{"svcns"} {
		waitHealthy(t, ctx, vm, compose, s)
	}
	waitRunning(t, ctx, vm, compose, "dns")
	waitFor(t, 3*time.Minute, "the service namespace to be attached to the new holder", func() bool {
		pid := strings.Fields(output("sudo docker run --rm --user 0:0 --entrypoint cat -v chaosgw_chaosgw-svcns:/p:ro " + tag + " /p/pid"))
		return len(pid) == 2 && nsInode("/run/netns/cgsvc") == pid[1] && strings.Contains(output("sudo ip -o link show svc0"), "svc0")
	})
}

// apiClient calls the API from inside the VM, on the management address (the container runs in the host's network namespace).
type apiClient struct {
	t      *testing.T
	ctx    context.Context
	vm     *appliance.VM
	bearer string
}

func (a *apiClient) call(method, path string, body any, headers ...string) (int, map[string]any) {
	a.t.Helper()
	cmd := "curl -sk -m 30 -o - -w '\\n%{http_code}' -X " + method + " -H 'Content-Type: application/json' -c /tmp/chaosgw.jar -b /tmp/chaosgw.jar"
	if a.bearer != "" {
		headers = append([]string{a.bearer}, headers...)
	}
	for _, h := range headers {
		cmd += " -H '" + h + "'"
	}
	var stdin io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		stdin = bytes.NewReader(raw)
		cmd += " --data-binary @-"
	}
	r, err := a.vm.Run(a.ctx, cmd+" https://"+appliance.GatewayMgmt+":8443/api/v1"+path+" 2>&1", stdin)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	out := strings.TrimRight(r.Stdout, "\n")
	i := strings.LastIndexByte(out, '\n')
	if i < 0 {
		return 0, nil
	}
	status, _ := strconv.Atoi(strings.TrimSpace(out[i+1:]))
	var m map[string]any
	_ = json.Unmarshal([]byte(out[:i]), &m)
	return status, m
}

// device is the client namespace as a device with a DHCP client.
type device struct {
	t      *testing.T
	ctx    context.Context
	h      appliance.Host
	ns     string
	script string
	out    string
}

func newDevice(t *testing.T, ctx context.Context, h appliance.Host, names appliance.Names, dir string) *device {
	t.Helper()
	d := &device{t: t, ctx: ctx, h: h, ns: names.NSClient, script: filepath.Join(dir, "udhcpc.sh"), out: filepath.Join(dir, "lease.out")}
	if err := os.WriteFile(d.script, []byte(udhcpcScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dnsq.py"), []byte(dnsQuery), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func (d *device) client(args ...string) (string, error) {
	return d.h.Run(d.ctx, append(appliance.Cmd{"ip", "netns", "exec", d.ns}, args...))
}

// get makes the device ask for a lease with the DHCP client, from an interface without an address, and returns what it got.
func (d *device) get() (ip, router, dns string) {
	d.t.Helper()
	_ = os.Remove(d.out)
	var last string
	for i := 0; i < 6; i++ {
		_, _ = d.client("ip", "addr", "flush", "dev", "eth0")
		out, err := d.h.Run(d.ctx, appliance.Cmd{"env", "LEASE_OUT=" + d.out, "ip", "netns", "exec", d.ns, "udhcpc", "-i", "eth0", "-n", "-q", "-t", "5", "-T", "2", "-s", d.script})
		last = out
		if raw, rerr := os.ReadFile(d.out); err == nil && rerr == nil {
			if f := strings.Fields(string(raw)); len(f) == 3 {
				return f[0], f[1], f[2]
			}
		}
		time.Sleep(3 * time.Second)
	}
	d.t.Fatalf("the device got no lease:\n%s", last)
	return "", "", ""
}

// dns asks a server for an A record from the device and returns the address, or what went wrong.
func (d *device) dns(server, name string) string {
	d.t.Helper()
	python := filepath.Join(filepath.Dir(d.script), "dnsq.py")
	var last string
	for i := 0; i < 10; i++ {
		out, err := d.client("python3", python, server, name)
		if err == nil {
			return strings.TrimSpace(out)
		}
		last = out
		time.Sleep(2 * time.Second)
	}
	return "no answer: " + strings.TrimSpace(last)
}

// waitFor polls a condition.
func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// waitRunning waits until a container of the compose project is running (it has no health check that can pass before the executor has
// attached the service namespace).
func waitRunning(t *testing.T, ctx context.Context, vm *appliance.VM, compose, service string) {
	t.Helper()
	waitFor(t, 3*time.Minute, service+" to run", func() bool {
		out, _ := vm.Must(ctx, compose+" ps -q "+service+" | xargs -r sudo docker inspect -f '{{.State.Status}}'")
		return strings.TrimSpace(out) == "running"
	})
}
