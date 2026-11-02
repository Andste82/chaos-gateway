//go:build appliance

package appliance_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/appliance"
)

// Level 2 of the installation scripts (plan §3.8, M28/M30; deploy/install.sh, deploy/uninstall.sh): the way an operator uses them on a
// clean Ubuntu host with a test interface, in a VM with the real Docker, with nothing of the repository on the host but the two scripts,
// the host setup and the image (that stands in for the release and for ghcr.io: the scripts are served from the VM's own loopback, as
// the documented `curl -fsSL .../install.sh | sudo sh -s -- ...` takes them from the release, and the image is loaded and not pulled).
//
//   - a host without Docker: install.sh fetches the host setup of the release and installs Docker with it; the image is not there
//     yet, so it stops at the step that needs it with the message that says so, and Docker is installed;
//   - the netplan entry of the test interface: install.sh stops (exit 1) with the check's hint, and nothing is started;
//   - after the entry is gone: install.sh ends with "Chaos Gateway is running", the five containers that can be healthy are, the files
//     of the deployment are in /opt/chaosgw, and a second run changes nothing;
//   - the setup wizard makes revision 1 and the bridge of the test network exists;
//   - uninstall.sh: the containers and their volumes are gone, nothing of the gateway is left in the kernel, the networkd rule of the
//     test interface is gone, and the data, the files and the image stay;
//   - install.sh again: the gateway is back with its revision, without a new setup;
//   - uninstall.sh --purge --yes: the data, the files, the image and the host configuration of the host setup are gone, Docker stays.
//
// The environment is that of TestContainerDeploymentOnCleanUbuntuHosts. One topology (three ports) is enough: nothing here depends on
// where the management network lies.
func TestInstallAndUninstallOnCleanUbuntuHosts(t *testing.T) {
	amd64Only(t)
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
		t.Run(r.Name+"/three-ports", func(t *testing.T) { installUninstall(t, r, tar, "") })
	}
}

// TestInstallFromTheReleaseOnCleanUbuntuHosts is the same course with a published release instead of the files of the checkout and a
// loaded image: install.sh comes from the GitHub release (the documented `curl -fsSL .../releases/download/vX/install.sh | sudo sh -s --
// --version X ...`), fetches the host setup of that release to install Docker, pulls the image from ghcr.io, and everything after is as
// above. It runs only when CHAOSGW_APPLIANCE_RELEASE_VERSION names a version that is published (1.0.0-rc.1; the workflow release-install.yml
// sets it), and needs the VM to reach github.com and ghcr.io.
func TestInstallFromTheReleaseOnCleanUbuntuHosts(t *testing.T) {
	amd64Only(t)
	version := os.Getenv("CHAOSGW_APPLIANCE_RELEASE_VERSION")
	if version == "" {
		t.Skip("CHAOSGW_APPLIANCE_RELEASE_VERSION is not set: name a published version (see .github/workflows/release-install.yml)")
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`).MatchString(version) {
		t.Fatalf("CHAOSGW_APPLIANCE_RELEASE_VERSION %q is not a version", version)
	}
	if !appliance.HasKVM() && os.Getenv("CHAOSGW_APPLIANCE_ALLOW_TCG") == "" {
		t.Skip("no /dev/kvm: level 2 needs a KVM-capable machine (plan Q1); CHAOSGW_APPLIANCE_ALLOW_TCG=1 runs it emulated")
	}
	for _, rel := range strings.Split(env("CHAOSGW_APPLIANCE_RELEASES", "24.04,26.04"), ",") {
		r, ok := appliance.Releases[strings.TrimSpace(rel)]
		if !ok {
			t.Fatalf("unknown release %q", rel)
		}
		t.Run(r.Name+"/three-ports", func(t *testing.T) { installUninstall(t, r, "", version) })
	}
}

// leftoversOfTheGateway lists what a stopped or removed gateway has left in the kernel of the host.
func leftoversOfTheGateway(output func(string) string) []string {
	var left []string
	if out := output("sudo nft list tables"); strings.Contains(out, "chaosgw") {
		left = append(left, "the nftables table inet chaosgw")
	}
	for _, c := range []string{"sudo ip -j rule show", "sudo ip -j route show table all"} {
		if out := output(c); strings.Contains(out, `"protocol":"201"`) {
			left = append(left, "an entry with the protocol 201 in `"+c+"`")
		}
	}
	for _, m := range regexp.MustCompile(`(?m)^\d+: (br-iot|br-lab|svc0|pv[0-9a-f]{8})\b`).FindAllStringSubmatch(output("ip -d link show"), -1) {
		left = append(left, "the interface "+m[1])
	}
	if ns := output("ip netns list"); strings.Contains(ns, "cgsvc") || strings.Contains(ns, "cgp-") {
		left = append(left, "a namespace of the gateway: "+strings.TrimSpace(ns))
	}
	if out := output("sudo iptables -S DOCKER-USER"); strings.Contains(out, "chaosgw") {
		left = append(left, "rules in DOCKER-USER:\n"+out)
	}
	if out := output("ip -d link show lan0"); strings.Contains(out, " master ") {
		left = append(left, "the test interface is still a port of a bridge")
	}
	return left
}

// installUninstall is the course of both tests. With a version it installs that published release (the files from GitHub, the image from
// ghcr.io), else the files of the checkout, served from the VM's loopback, and the image of imageTar.
func installUninstall(t *testing.T, rel appliance.Release, imageTar, version string) {
	ctx, cancel := context.WithTimeout(context.Background(), 85*time.Minute)
	defer cancel()
	released := version != ""
	tag := env("CHAOSGW_APPLIANCE_IMAGE_TAG", "chaos-gateway:appliance")
	if released {
		tag = "ghcr.io/andste82/chaos-gateway:" + version
	}
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
	seed, err := appliance.WriteSeed(ctx, dir, appliance.Seed{Hostname: "gi-" + strings.ReplaceAll(rel.Name, ".", ""), SSHPublicKey: key.Authorized})
	if err != nil {
		t.Fatal(err)
	}
	top := appliance.Topology{Prefix: "i" + strings.ReplaceAll(rel.Name, ".", "")[:2] + "3"}
	if err := top.Validate(); err != nil {
		t.Fatal(err)
	}
	h := appliance.Host{}
	plan := top.Plan()
	if err := h.Up(ctx, plan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Down(context.Background(), plan) })
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
	output := func(cmd string) string {
		t.Helper()
		r, err := vm.Run(ctx, cmd+" 2>&1", nil)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return r.Stdout
	}
	// run runs a command whose exit status is part of the test
	run := func(cmd string) appliance.Result {
		t.Helper()
		r, err := vm.Run(ctx, cmd+" 2>&1", nil)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return r
	}
	must("sudo cloud-init status --wait")
	must("echo 'network: {config: disabled}' | sudo tee /etc/cloud/cloud.cfg.d/99-chaosgw-no-network.cfg >/dev/null")
	if r := run("command -v docker"); r.Exit == 0 {
		t.Fatalf("the host is not clean: Docker is installed (%s)", strings.TrimSpace(r.Stdout))
	}

	var install string
	if released {
		// the published release: install.sh from the GitHub release, which fetches the host setup of that release for Docker and pulls
		// the image from ghcr.io; the first run is the one that stops at the netplan entry
		install = "curl -fsSL https://github.com/Andste82/chaos-gateway/releases/download/v" + version + "/install.sh | sudo sh -s -- --version " + version + " --test-interface lan0"
	} else {
		// the files of this test go to /opt/install-test: vm.Put makes the directory of a file with mode 0755, and /tmp would lose its
		// sticky bit and be closed to the ssh user (the cookie jar of the API calls is kept there)
		// ---- what the release serves: install.sh and the host setup, from the VM's own loopback
		for _, f := range []string{"install.sh", "host-setup.sh"} {
			if err := vm.PutFile(ctx, "../../deploy/"+f, "/srv/release/latest/download/"+f, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		must("sudo sh -c 'setsid nohup python3 -m http.server 8099 --bind 127.0.0.1 --directory /srv/release >/tmp/release-server.log 2>&1 &'")
		waitFor(t, time.Minute, "the release server to answer", func() bool {
			return run("curl -fsS -o /dev/null http://127.0.0.1:8099/latest/download/install.sh").Exit == 0
		})
		const releases = "http://127.0.0.1:8099"
		install = "curl -fsSL " + releases + "/latest/download/install.sh | sudo CHAOSGW_RELEASES=" + releases + " sh -s -- --image " + tag + " --no-pull --test-interface lan0"

		// ---- no Docker: the host setup of the release installs it, and install.sh stops at the image, which is not on the host yet
		r := run(install)
		if r.Exit != 1 || !strings.Contains(r.Stdout, "docker is not installed: fetching the host setup of the release") ||
			!strings.Contains(r.Stdout, "cannot read the host setup out of "+tag) {
			t.Fatalf("install.sh on a host without Docker and without the image: exit %d\n%s", r.Exit, r.Stdout)
		}
		if r := run("command -v docker && sudo docker compose version"); r.Exit != 0 {
			t.Fatalf("Docker is not installed after install.sh:\n%s", r.Stdout)
		}
		if err := vm.PutFile(ctx, imageTar, "/opt/install-test/image.tar.gz", 0o644); err != nil {
			t.Fatal(err)
		}
		must("gunzip -c /opt/install-test/image.tar.gz | sudo docker load")
	}

	// ---- the netplan entry of the test interface: install.sh stops with the hint of the check and starts nothing
	r := run(install)
	if r.Exit != 1 || !strings.Contains(r.Stdout, "FAIL  netplan: /etc/netplan/") || !strings.Contains(r.Stdout, "delete the entry of lan0") ||
		!strings.Contains(r.Stdout, "the host setup found a step missing") {
		t.Fatalf("install.sh before the netplan entry is gone: exit %d\n%s", r.Exit, r.Stdout)
	}
	if released {
		// this was the first run on a host without Docker: it fetched the host setup of the release and pulled the image
		if !strings.Contains(r.Stdout, "docker is not installed: fetching the host setup of the release") || !strings.Contains(r.Stdout, "pulling "+tag) {
			t.Errorf("the first install.sh of the release did not install Docker and pull the image:\n%s", r.Stdout)
		}
		if r := run("command -v docker && sudo docker image inspect " + tag); r.Exit != 0 {
			t.Fatalf("Docker or the image is missing after the first install.sh:\n%s", r.Stdout)
		}
	}
	if out := strings.TrimSpace(output("sudo docker ps -q")); out != "" {
		t.Errorf("install.sh started containers although the host setup found a step missing:\n%s", output("sudo docker ps"))
	}
	if err := vm.Put(ctx, "/opt/install-test/remove-lan0.py", 0o644, strings.NewReader(removeLan0)); err != nil {
		t.Fatal(err)
	}
	must("sudo python3 /opt/install-test/remove-lan0.py")
	must("printf '[Match]\\nMACAddress=" + appliance.MACs.LAN + "\\n\\n[Link]\\nName=lan0\\n' | sudo tee /etc/systemd/network/05-lan0.link >/dev/null")
	must("sudo netplan apply")

	// ---- the installation
	compose := "cd /opt/chaosgw && sudo docker compose"
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, c := range []string{compose + " ps -a", compose + " logs --no-color --tail 60 api", compose + " logs --no-color --tail 40 exec", "ls -ld /tmp", "cat /tmp/chaosgw.jar"} {
			if r, err := vm.Run(context.Background(), c+" 2>&1", nil); err == nil {
				t.Logf("%s:\n%s", c, r.Stdout)
			}
		}
	})
	r = run(install)
	if r.Exit != 0 || !strings.Contains(r.Stdout, "install: Chaos Gateway is running.") {
		t.Fatalf("install.sh: exit %d\n%s", r.Exit, r.Stdout)
	}
	for _, s := range []string{"exec", "api", "svcns", "kea", "bird"} {
		waitHealthy(t, ctx, vm, compose, s)
	}
	for _, f := range []string{"compose.yaml", ".env", "install.sh", "update.sh", "uninstall.sh", "host-setup.sh"} {
		if r := run("sudo test -s /opt/chaosgw/" + f); r.Exit != 0 {
			t.Errorf("/opt/chaosgw/%s is not there after install.sh", f)
		}
	}
	must("grep -q '^CHAOSGW_VERSION=" + versionOf(tag) + "$' /opt/chaosgw/.env")
	must("sudo test -s /etc/systemd/network/10-chaosgw-lan0.network")
	if out := must("stat -c '%n %u:%g' /var/lib/chaosgw/state /var/lib/chaosgw/secrets /var/lib/chaosgw/exec"); !strings.Contains(out, "state 65532:65532") ||
		!strings.Contains(out, "secrets 65532:65532") || !strings.Contains(out, "exec 0:0") {
		t.Errorf("the data directories after install.sh:\n%s", out)
	}
	// a second run changes nothing
	execBefore := strings.TrimSpace(must(compose + " ps -q exec"))
	if r := run(install); r.Exit != 0 || !strings.Contains(r.Stdout, "install: Chaos Gateway is running.") {
		t.Fatalf("the second install.sh: exit %d\n%s", r.Exit, r.Stdout)
	}
	if after := strings.TrimSpace(must(compose + " ps -q exec")); after != execBefore {
		t.Errorf("the second install.sh replaced the executor: %s -> %s", execBefore, after)
	}

	// ---- the setup wizard makes revision 1
	var setupToken string
	for deadline := time.Now().Add(2 * time.Minute); setupToken == "" && time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if m := regexp.MustCompile(`Setup token: (\S+)`).FindStringSubmatch(output(compose + " logs --no-color api")); m != nil {
			setupToken = m[1]
		}
	}
	if setupToken == "" {
		t.Fatalf("the log of the API holds no setup token:\n%s", output(compose+" logs --no-color api"))
	}
	var cfgDoc map[string]any
	if err := yaml.Unmarshal([]byte(appliance.GatewayConfig(false)), &cfgDoc); err != nil {
		t.Fatal(err)
	}
	api := apiClient{t: t, ctx: ctx, vm: vm}
	const adminPassword = "correct horse battery staple"
	if st, m := api.call("POST", "/setup", map[string]any{"admin_password": adminPassword, "configuration": cfgDoc}, "X-Setup-Token: "+setupToken); st != 200 || m["revision"] != float64(1) {
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
	waitBridge(t, ctx, vm, "br-iot")
	if !strings.Contains(output("sudo nft list tables"), "chaosgw") {
		t.Fatalf("the gateway made no nftables table after the setup:\n%s", output("sudo nft list tables"))
	}

	// ---- uninstall.sh: out of the kernel and off the host's containers; the data, the files and the image stay
	r = run("sudo sh /opt/chaosgw/uninstall.sh")
	if r.Exit != 0 || !strings.Contains(r.Stdout, "Chaos Gateway is stopped and out of the kernel") {
		t.Fatalf("uninstall.sh: exit %d\n%s", r.Exit, r.Stdout)
	}
	if left := leftoversOfTheGateway(output); len(left) > 0 {
		t.Errorf("after uninstall.sh the gateway left %s", strings.Join(left, "; "))
	}
	if out := strings.TrimSpace(output("sudo docker ps -aq")); out != "" {
		t.Errorf("containers remain after uninstall.sh:\n%s", output("sudo docker ps -a"))
	}
	if out := output("sudo docker volume ls -q"); strings.Contains(out, "chaosgw") {
		t.Errorf("volumes of the gateway remain after uninstall.sh:\n%s", out)
	}
	if r := run("test -e /etc/systemd/network/10-chaosgw-lan0.network"); r.Exit == 0 {
		t.Error("the networkd rule of lan0 remains after uninstall.sh")
	}
	for _, p := range []string{"/var/lib/chaosgw/state/config.json", "/opt/chaosgw/compose.yaml"} {
		if r := run("sudo test -s " + p); r.Exit != 0 {
			t.Errorf("%s is gone after uninstall.sh without --purge", p)
		}
	}
	if r := run("sudo docker image inspect " + tag); r.Exit != 0 {
		t.Errorf("the image is gone after uninstall.sh without --purge:\n%s", r.Stdout)
	}

	// ---- install.sh again: the gateway is back with its revision and no new setup
	reinstall := "sudo sh /opt/chaosgw/install.sh --image " + tag + " --no-pull --test-interface lan0"
	if released {
		reinstall = "sudo sh /opt/chaosgw/install.sh --version " + version + " --test-interface lan0"
	}
	r = run(reinstall)
	if r.Exit != 0 || !strings.Contains(r.Stdout, "install: Chaos Gateway is running.") {
		t.Fatalf("install.sh after uninstall.sh: exit %d\n%s", r.Exit, r.Stdout)
	}
	waitBridge(t, ctx, vm, "br-iot")
	st, session = api.call("POST", "/auth/login", map[string]any{"password": adminPassword})
	if st != 200 {
		t.Fatalf("login after the reinstall: %d %v (the setup was not kept)", st, session)
	}
	if st, state := api.call("GET", "/state", nil); st != 200 || state["active_revision"] != float64(1) || state["safe_mode"] != false {
		t.Errorf("the state after the reinstall: %d %v", st, state)
	}

	// ---- uninstall.sh --purge --yes: all of it is gone, Docker stays
	r = run("sudo sh /opt/chaosgw/uninstall.sh --purge --yes")
	if r.Exit != 0 || !strings.Contains(r.Stdout, "Chaos Gateway is gone from this host") {
		t.Fatalf("uninstall.sh --purge --yes: exit %d\n%s", r.Exit, r.Stdout)
	}
	if left := leftoversOfTheGateway(output); len(left) > 0 {
		t.Errorf("after uninstall.sh --purge the gateway left %s", strings.Join(left, "; "))
	}
	for _, p := range []string{"/var/lib/chaosgw", "/opt/chaosgw", "/etc/modules-load.d/chaos-gateway.conf", "/etc/sysctl.d/90-chaos-gateway.conf", "/etc/systemd/network/10-chaosgw-lan0.network"} {
		if r := run("sudo test -e " + p); r.Exit == 0 {
			t.Errorf("%s remains after uninstall.sh --purge", p)
		}
	}
	if r := run("sudo docker image inspect " + tag); r.Exit == 0 {
		t.Error("the image remains after uninstall.sh --purge")
	}
	if r := run("command -v docker"); r.Exit != 0 {
		t.Error("Docker is gone after uninstall.sh --purge: it must stay")
	}
}
