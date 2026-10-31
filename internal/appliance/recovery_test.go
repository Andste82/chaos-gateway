//go:build appliance

package appliance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/appliance"
)

// Level 2 of M27 (plan §3.9, §3.8 "Stop", D35): the recovery and the stop of the gateway on a clean Ubuntu host with a test
// interface, in a VM, with the containers of the compose files (the executor, BIRD, the API and the holder and the DNS proxy
// of the service namespace) and the real Docker. The environment is that of TestSmokeOnCleanUbuntuHosts. Revision 1 is made by
// the setup of the API (POST /setup, then a login that confirms it and makes a token), so that the test reads GET /state: the
// fields safe_mode and safe_mode_reason that the UI's banner shows are checked here on the real stack, in safe mode and after
// it. A fault is active when the stack stops, so that the tc tree is there to be removed. There is no Kea container, so no probe
// is built: the probes' namespaces and ends and their DHCP clients are covered by the level 1 stop test, and the stack with Kea
// by the level 2 test of M28 (container deployment).
//
//   - a revision that cannot be applied at start leads to the last known good one;
//   - a gateway whose active revision fails with nothing earlier to fall back to runs in safe mode: management access only, no
//     forwarding; the safe mode ends when a revision applies;
//   - stopping the containers leaves nothing of the gateway on the host (no table, no rule or route, no bridge, veth, namespace
//     or DOCKER-USER rule, no fault), the test interface included, and starting them again rebuilds the state from the confirmed revision;
//   - `docker compose run teardown` (compose.teardown.yaml) removes what an executor that was killed left behind, and `--purge`
//     empties the data directories: the revisions, the secrets and the audit log are gone afterwards, the directories stay.
//
// The break is made on the host, not in the files of the store: a dummy interface with the name of the bridge of a network makes
// every revision with that network fail ("exists but is not a bridge: refusing to touch it"), as a host configuration that
// collides with the gateway's does.
func TestRecoveryAndStopOnCleanUbuntuHosts(t *testing.T) {
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
		t.Run(r.Name+"/three-ports", func(t *testing.T) { recovery(t, r, tar) })
	}
}

func recovery(t *testing.T, rel appliance.Release, imageTar string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
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
	seed, err := appliance.WriteSeed(ctx, dir, appliance.Seed{Hostname: "gw-r" + strings.ReplaceAll(rel.Name, ".", ""), SSHPublicKey: key.Authorized})
	if err != nil {
		t.Fatal(err)
	}
	top := appliance.Topology{Prefix: "r" + strings.ReplaceAll(rel.Name, ".", "")[:2] + "3"}
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

	// the host setup, the image, the compose files: the executor, BIRD and the API (the API container is what recovers)
	if err := vm.PutFile(ctx, "../../deploy/host-setup.sh", "/opt/chaosgw/host-setup.sh", 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := vm.Must(ctx, "sudo sh /opt/chaosgw/host-setup.sh 2>&1"); err != nil {
		t.Fatalf("the host setup failed: %v\n%s", err, out)
	}
	if err := vm.PutFile(ctx, imageTar, "/opt/chaosgw/image.tar.gz", 0o644); err != nil {
		t.Fatal(err)
	}
	must("gunzip -c /opt/chaosgw/image.tar.gz | sudo docker load")
	for _, f := range []string{"executor", "bird", "api", "dns", "teardown"} {
		if err := vm.PutFile(ctx, "../../deploy/compose."+f+".yaml", "/opt/chaosgw/compose."+f+".yaml", 0o644); err != nil {
			t.Fatal(err)
		}
	}
	compose := "sudo CHAOSGW_VERSION=" + versionOf(tag) + " docker compose -p chaosgw -f /opt/chaosgw/compose.executor.yaml -f /opt/chaosgw/compose.bird.yaml -f /opt/chaosgw/compose.api.yaml -f /opt/chaosgw/compose.dns.yaml"
	// the teardown service is under a profile and has a file of its own: the stack's project, the teardown file alone
	teardown := "sudo CHAOSGW_VERSION=" + versionOf(tag) + " docker compose -p chaosgw -f /opt/chaosgw/compose.teardown.yaml run --rm teardown"
	must(compose + " up -d")
	waitHealthy(t, ctx, vm, compose, "exec")
	waitHealthy(t, ctx, vm, compose, "api")

	// the API, called from inside the VM on the management address (the container runs in the host's network namespace)
	const adminPassword = "correct horse battery staple"
	api := func(method, path string, body any, headers ...string) (int, map[string]any) {
		t.Helper()
		cmd := "curl -sk -m 30 -o - -w '\\n%{http_code}' -X " + method + " -H 'Content-Type: application/json' -c /tmp/chaosgw.jar -b /tmp/chaosgw.jar"
		for _, h := range headers {
			cmd += " -H '" + h + "'"
		}
		var stdin io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			stdin = bytes.NewReader(raw)
			cmd += " --data-binary @-"
		}
		r, err := vm.Run(ctx, cmd+" https://"+appliance.GatewayMgmt+":8443/api/v1"+path+" 2>&1", stdin)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
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

	// revision 1 is made by the setup of the API, as an administrator would make it: the setup token is in the log of the first
	// start, the revision waits for its confirmation, a session confirms it and makes the token the rest of the test uses.
	// The network redirects the DNS of its devices to the proxy of the service namespace, so the stop has a redirect to remove.
	cfg1 := strings.Replace(appliance.GatewayConfig(false), "    address: "+appliance.GatewayLAN+"/24\n", "    address: "+appliance.GatewayLAN+"/24\n    dns: {redirect_hardcoded: true}\n", 1)
	if !strings.Contains(cfg1, "redirect_hardcoded") {
		t.Fatal("the test configuration has no DNS redirect")
	}
	var cfg1Doc map[string]any
	if err := yaml.Unmarshal([]byte(cfg1), &cfg1Doc); err != nil {
		t.Fatal(err)
	}
	var setupToken string
	for deadline := time.Now().Add(2 * time.Minute); setupToken == "" && time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if m := regexp.MustCompile(`Setup token: (\S+)`).FindStringSubmatch(output(compose + " logs --no-color api")); m != nil {
			setupToken = m[1]
		}
	}
	if setupToken == "" {
		t.Fatalf("the log of the API holds no setup token:\n%s", output(compose+" logs --no-color api"))
	}
	if st, m := api("POST", "/setup", map[string]any{"admin_password": adminPassword, "configuration": cfg1Doc}, "X-Setup-Token: "+setupToken); st != 200 || m["revision"] != float64(1) || m["status"] != "pending_confirm" {
		t.Fatalf("setup: %d %v", st, m)
	}
	st, session := api("POST", "/auth/login", map[string]any{"password": adminPassword})
	csrf, _ := session["csrf_token"].(string)
	if st != 200 || csrf == "" {
		t.Fatalf("login: %d %v", st, session)
	}
	if st, m := api("POST", "/revisions/1/confirm", nil, "X-CSRF-Token: "+csrf); st != 200 {
		t.Fatalf("confirm revision 1: %d %v", st, m)
	}
	st, created := api("POST", "/auth/tokens", map[string]any{"name": "level2", "scope": "full"}, "X-CSRF-Token: "+csrf)
	token, _ := created["token"].(string)
	if st != 201 || token == "" {
		t.Fatalf("token: %d %v", st, created)
	}
	bearer := "Authorization: Bearer " + token
	// state waits until GET /state answers and want is true of the answer; the API restarts in this test, so it may not answer
	state := func(what string, want func(map[string]any) bool) map[string]any {
		t.Helper()
		var last map[string]any
		for deadline := time.Now().Add(4 * time.Minute); time.Now().Before(deadline); time.Sleep(3 * time.Second) {
			if st, m := api("GET", "/state", nil, bearer); st == 200 {
				if last = m; want(m) {
					return m
				}
			}
		}
		t.Fatalf("%s: the last state is %v", what, last)
		return nil
	}

	// revision 2 renames the network IoT (the bridge br-iot) to Lab (br-lab). It is made with the command the appliance smoke
	// test uses, while the API container is stopped.
	cfg2 := strings.Replace(cfg1, "name: IoT", "name: Lab", 1)
	if err := vm.Put(ctx, "/opt/chaosgw/gateway2.yaml", 0o644, strings.NewReader(cfg2)); err != nil {
		t.Fatal(err)
	}
	must(compose + " stop api")
	// the routing the gateway made, before and after the offline apply: when the apply cannot verify what it made, this says what the kernel holds
	routing := func(when string) {
		t.Helper()
		t.Logf("%s:\n%s", when, output("sh -c 'ip -4 route show table all; ip -4 rule show; ip -br link; ip -4 -br addr'"))
	}
	routing("the routing before the offline apply of revision 2")
	// what `apply --dry-run` plans for the configuration the gateway runs (nothing, if the plan is steady) and for revision 2
	if err := vm.Put(ctx, "/opt/chaosgw/gateway1.yaml", 0o644, strings.NewReader(cfg1)); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"gateway1.yaml", "gateway2.yaml"} {
		t.Logf("dry run of %s:\n%s", f, output("sudo docker run --rm --user 65532:65532 --network host -v chaosgw_chaosgw-run:/run/chaosgw -v /opt/chaosgw/"+f+":/gateway.yaml:ro "+tag+
			" apply --file /gateway.yaml --dry-run --socket /run/chaosgw/exec.sock --executor-uid 0"))
	}
	// every change of a route or a rule while the apply runs, with the time: the order in which the kernel lost table 101 and rule 925
	output("sudo sh -c 'nohup ip -ts monitor route rule >/tmp/monitor.txt 2>&1 </dev/null & echo $! >/tmp/monitor.pid'")
	must(compose + " exec -T exec sh -c 'touch /run/chaosgw/trace' || true") // TEMPORARY DEBUG: the commands of the executor
	time.Sleep(time.Second)
	r2, err := vm.Run(ctx, "sudo docker run --rm --user 65532:65532 --network host -v chaosgw_chaosgw-run:/run/chaosgw -v /var/lib/chaosgw/state:/var/lib/chaosgw/state "+
		"-v /opt/chaosgw/gateway2.yaml:/gateway.yaml:ro "+tag+
		" apply --file /gateway.yaml --socket /run/chaosgw/exec.sock --state-dir /var/lib/chaosgw/state --executor-uid 0 2>&1", nil)
	time.Sleep(time.Second)
	t.Logf("the commands of the executor:\n%s", output(compose+" logs --no-color --tail 300 exec | grep TRACE | cut -c1-900"))
	t.Logf("the changes of routes and rules during the apply:\n%s", output("sudo sh -c 'kill $(cat /tmp/monitor.pid); cat /tmp/monitor.txt'"))
	if err != nil || r2.Exit != 0 {
		routing("the routing after the failed offline apply of revision 2")
		t.Fatalf("the offline apply of revision 2: %v, exit %d\n%s", err, r2.Exit, r2.Stdout)
	}
	out := r2.Stdout
	t.Logf("apply 2: %s", strings.TrimSpace(out))
	stateDir := "/var/lib/chaosgw/state" // a bind mount of the host since M28 (plan §3.6)
	if out := must("sudo cat " + stateDir + "/config.json"); !regexp.MustCompile(`"active": 2`).MatchString(out) {
		t.Fatalf("revision 2 is not the active one:\n%s", out)
	}
	// the API container applies the active revision with the service namespace (the pair svc0, the name cgsvc, the DNS redirect)
	must(compose + " up -d")
	waitHealthy(t, ctx, vm, compose, "api")
	for _, c := range []string{"ip -d link show svc0", "ip netns list | grep cgsvc"} {
		if r, err := vm.Run(ctx, c+" 2>&1", nil); err != nil || r.Exit != 0 {
			t.Fatalf("the gateway has not built the service namespace (%s), so the stop checks nothing of it:\n%v %s", c, err, r.Stdout)
		}
	}
	if out := output("sudo nft list table inet chaosgw"); !regexp.MustCompile(`(?m)dport 53.*(redirect|dnat)`).MatchString(out) {
		t.Fatalf("the table has no redirect of the DNS, so the stop checks nothing of it:\n%s", out)
	}

	client := func(args ...string) (string, error) {
		return h.Run(ctx, append(appliance.Cmd{"ip", "netns", "exec", names.NSClient}, args...))
	}
	reachesServer := func(tries int) bool {
		for i := 0; i < tries; i++ {
			if _, err := client("ping", "-c", "2", "-W", "2", "-n", appliance.ServerAddr); err == nil {
				return true
			}
			time.Sleep(2 * time.Second)
		}
		return false
	}
	if !reachesServer(10) {
		t.Fatal("the client cannot reach the server through the gateway with revision 2")
	}

	// the API reports the gateway as running: not in safe mode, revision 2 active and the last known good one
	state("the gateway runs revision 2", func(m map[string]any) bool {
		return m["safe_mode"] == false && m["safe_mode_reason"] == nil && m["active_revision"] == float64(2) && m["last_known_good"] == float64(2)
	})

	// ---- stopping the containers leaves nothing of the gateway on the host -----------------------------------------------
	// a fault is active when the stop comes, so that the tc tree on the test interface is there to be removed: the stop of the
	// API removes the overlay, the stop of the executor everything else, and the checks below can fail on a tree left behind
	if st, m := api("POST", "/overlays", map[string]any{"target": map[string]any{"network": "Lab"}, "fault": map[string]any{"latency": "20ms"}, "ttl": "30m"}, bearer); st != 201 {
		t.Fatalf("overlay: %d %v", st, m)
	}
	faultActive := false
	for deadline := time.Now().Add(time.Minute); !faultActive && time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		faultActive = strings.Contains(output("tc qdisc show"), "netem")
	}
	if !faultActive {
		t.Fatalf("no netem qdisc exists with the overlay active, so the stop checks no tc tree:\n%s", output("tc qdisc show"))
	}
	must(compose + " stop")
	nothingLeft := func(when string) {
		t.Helper()
		var left []string
		if out := output("sudo nft list tables"); strings.Contains(out, "chaosgw") {
			left = append(left, "the nftables table inet chaosgw")
		}
		for _, c := range []string{"sudo ip -j rule show", "sudo ip -j route show table all"} {
			if out := output(c); strings.Contains(out, `"protocol":"201"`) {
				left = append(left, "an entry with the protocol 201 in `"+c+"`")
			}
		}
		links := output("ip -d link show")
		for _, m := range regexp.MustCompile(`(?m)^\d+: (br-iot|br-lab|svc0|pv[0-9a-f]{8})\b`).FindAllStringSubmatch(links, -1) {
			left = append(left, "the interface "+m[1])
		}
		if ns := output("ip netns list"); strings.Contains(ns, "cgsvc") || strings.Contains(ns, "cgp-") {
			left = append(left, "a namespace of the gateway: "+strings.TrimSpace(ns))
		}
		if out := output("sudo iptables -S DOCKER-USER"); strings.Contains(out, "chaosgw") {
			left = append(left, "rules in DOCKER-USER:\n"+out)
		}
		if out := output("tc qdisc show"); regexp.MustCompile(`qdisc (htb|netem|ingress|clsact)`).MatchString(out) {
			left = append(left, "a tc tree (a fault is still active):\n"+out)
		}
		if out := output("ip -d link show lan0"); strings.Contains(out, " master ") {
			left = append(left, "the test interface is still a port of a bridge")
		}
		if len(left) > 0 {
			t.Errorf("%s: the gateway left %s", when, strings.Join(left, "; "))
		}
	}
	nothingLeft("after the stop")
	if reachesServer(2) {
		t.Error("the client still reaches the server through a stopped gateway")
	}

	// ---- a revision that cannot be applied at start: the last known good one -----------------------------------------------
	must("sudo ip link add br-lab type dummy")
	must(compose + " up -d")
	waitLog(t, ctx, vm, compose, "api", "applying the last known good revision", 4*time.Minute)
	waitBridge(t, ctx, vm, "br-iot")
	if out := must("sudo cat " + stateDir + "/config.json"); !regexp.MustCompile(`"active": 1`).MatchString(out) || !regexp.MustCompile(`"last_known_good": 1`).MatchString(out) {
		t.Errorf("revision 1 is not the active and the last known good one:\n%s", out)
	}
	if out := must("sudo cat " + stateDir + "/revisions/000002.status.json"); !strings.Contains(out, "rolled_back") {
		t.Errorf("revision 2 is not rolled back:\n%s", out)
	}
	if out := must("ip -d link show br-lab"); !strings.Contains(out, "dummy") {
		t.Errorf("the interface that collides with revision 2 was touched:\n%s", out)
	}
	// the API reports the fallback as the active revision, and no safe mode
	state("revision 1 is active after the fallback", func(m map[string]any) bool {
		return m["safe_mode"] == false && m["active_revision"] == float64(1) && m["last_known_good"] == float64(1)
	})
	if !reachesServer(15) {
		t.Error("the client cannot reach the server through the gateway with the last known good revision")
	}

	// ---- nothing to fall back to: safe mode -----------------------------------------------------------------------------
	must(compose + " stop")
	must("sudo ip link del br-lab")
	nothingLeft("after the second stop")
	must("sudo ip link add br-iot type dummy")
	must(compose + " up -d")
	waitLog(t, ctx, vm, compose, "api", "safe mode: management access only, no forwarding", 4*time.Minute)
	// management access only: a new SSH connection to the gateway works, a client of the test network reaches nothing
	if err := vm.DialFresh(ctx); err != nil {
		t.Errorf("a fresh SSH connection in safe mode: %v", err)
	}
	if out := must("ip -d link show br-iot"); !strings.Contains(out, "dummy") {
		t.Errorf("the interface that collides with revision 1 was touched:\n%s", out)
	}
	if reachesServer(3) {
		t.Error("the client reaches the server through a gateway in safe mode")
	}
	if out := output("sudo nft list table inet chaosgw"); !strings.Contains(out, "chaosgw") {
		t.Errorf("no table of the safe configuration:\n%s", out)
	}
	// the API reports it: the banner of the UI is built on safe_mode and the reason, and the revision pointers stay
	safe := state("the API reports the safe mode", func(m map[string]any) bool { return m["safe_mode"] == true })
	if reason, _ := safe["safe_mode_reason"].(string); !strings.Contains(reason, "revision 1 could not be applied") || !strings.Contains(reason, "no earlier revision that worked") {
		t.Errorf("safe_mode_reason: %q", reason)
	}
	if safe["active_revision"] != float64(1) {
		t.Errorf("active_revision in safe mode: %v", safe["active_revision"])
	}
	// the cause is removed and the process starts again: the active revision applies, the safe mode is gone
	must("sudo ip link del br-iot")
	must(compose + " restart api")
	waitBridge(t, ctx, vm, "br-iot")
	state("the safe mode is gone after the restart", func(m map[string]any) bool {
		return m["safe_mode"] == false && m["safe_mode_reason"] == nil && m["active_revision"] == float64(1)
	})
	if !reachesServer(15) {
		t.Error("the client cannot reach the server after the safe mode")
	}

	// ---- an executor that was killed: teardown removes what it left ------------------------------------------------------
	must("sudo docker update --restart=no chaosgw-exec-1")
	must(compose + " stop api")
	must("sudo docker kill chaosgw-exec-1")
	if out := output("ip -d link show br-iot"); !strings.Contains(out, "bridge") {
		t.Fatalf("a killed executor cleaned up after itself, so the test checks nothing:\n%s", out)
	}
	out = must(teardown + " 2>&1")
	if !strings.Contains(out, "Everything Chaos Gateway created in the kernel is removed.") {
		t.Errorf("teardown:\n%s", out)
	}
	nothingLeft("after teardown")
	// the directories hold the revisions, the secrets and the audit log until --purge empties them, all of them: the teardown
	// container mounts every one writable
	dirs := "/var/lib/chaosgw/"
	for _, v := range []string{"state", "secrets", "api"} {
		if out := output("sudo ls -A " + dirs + v); strings.TrimSpace(out) == "" {
			t.Fatalf("the directory %s is empty before --purge, so the test checks nothing:\n%s", v, out)
		}
	}
	out = must(teardown + " --purge 2>&1")
	if !strings.Contains(out, "Removed /var/lib/chaosgw/state") || !strings.Contains(out, "Removed /var/lib/chaosgw/secrets") || strings.Contains(out, "Not mounted in this container") {
		t.Errorf("teardown --purge:\n%s", out)
	}
	for _, v := range []string{"state", "secrets", "api", "service", "tlsca", "captures", "exec", "kea"} {
		if out := output("sudo ls -A " + dirs + v); strings.TrimSpace(out) != "" {
			t.Errorf("the directory %s is not empty after --purge:\n%s", v, out)
		}
	}
	// the directories themselves stay, with their owners: the next start needs nothing from the host setup
	if out := must("stat -c '%u:%g %a' /var/lib/chaosgw/secrets /var/lib/chaosgw/state"); strings.TrimSpace(out) != "65532:65532 700\n65532:65532 755" {
		t.Errorf("the directories after --purge:\n%s", out)
	}
}

// waitLog waits until the log of a service holds the text.
func waitLog(t *testing.T, ctx context.Context, vm *appliance.VM, compose, service, text string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		last, _ = vm.Must(ctx, compose+" logs --no-color "+service+" 2>&1")
		if strings.Contains(last, text) {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("the log of %s does not say %q:\n%s", service, text, last)
}

// waitBridge waits until the interface exists and is a bridge.
func waitBridge(t *testing.T, ctx context.Context, vm *appliance.VM, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		r, err := vm.Run(ctx, "ip -d link show "+name+" 2>&1", nil)
		if err == nil {
			last = r.Stdout
			if strings.Contains(last, "bridge") && !strings.Contains(last, "dummy") {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("%s is not a bridge:\n%s", name, last)
}
