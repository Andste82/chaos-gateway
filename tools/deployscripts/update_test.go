// Package deployscripts tests the shell scripts of deploy/ that run on the host (plan §3.8, §3.9, M28) against stubs of the tools
// they call: docker, curl. The scripts are checked with shellcheck in CI; these tests check what they decide.
package deployscripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const updateScript = "../../deploy/update.sh"

// dockerStub stands for the docker the update script calls. What it answers is in files of $STUB: ps (exists: the executor
// runs), apips (exists: the API container runs), oldimage (the image the executor runs, default chaos-gateway:old; its id is
// sha256:old, and the id of the image the tag names after the pull is sha256:new), pull, schema, config (the check of a compose file), up (the start), up.wait (the wait for exec, api, svcns, kea and bird),
// up.all (the wait for every container) (their exit status, default 0), schema.out (what the schema check prints), image/ (the
// files of the deployment in the image); every call is appended to calls, with the CHAOSGW_VERSION it saw.
const dockerStub = `#!/bin/sh
echo "[${CHAOSGW_VERSION:-}] $*" >>"$STUB/calls"
status() { if [ -f "$STUB/$1" ]; then cat "$STUB/$1"; else echo 0; fi; }
case "$*" in
"compose version") echo "Docker Compose version v2.24.6" ;;
"compose config --images") echo "chaos-gateway:${CHAOSGW_VERSION:-latest}" ;;
"compose ps -q exec") [ -f "$STUB/ps" ] && echo cid-exec; exit 0 ;;
"compose ps -q api") [ -f "$STUB/apips" ] && echo cid-api; exit 0 ;;
"compose ps") echo "NAME STATUS"; exit 0 ;;
"inspect -f {{.Config.Image}} cid-exec") if [ -f "$STUB/oldimage" ]; then cat "$STUB/oldimage"; else echo "chaos-gateway:old"; fi ;;
"inspect -f {{.Image}} cid-exec") echo sha256:old ;;
"image inspect -f {{.Id}} "*) echo sha256:new ;;
"pull --quiet "*) exit "$(status pull)" ;;
"run --rm "*" schema-check "*) [ -f "$STUB/schema.out" ] && cat "$STUB/schema.out" >&2; exit "$(status schema)" ;;
"run --rm --entrypoint tar "*) tar -C "$STUB/image" -cf - compose.yaml env.example update.sh ;;
"compose -f "*" config -q") exit "$(status config)" ;;
"compose up -d --remove-orphans") exit "$(status up)" ;;
"compose up -d --wait --wait-timeout "*" exec api svcns kea bird") exit "$(status up.wait)" ;;
"compose up -d --wait --wait-timeout "*) exit "$(status up.all)" ;;
"compose exec -T api chaosgw version") echo "chaosgw 2.0.0 (abc)" ;;
*) echo "docker stub: unexpected: $*" >&2; exit 99 ;;
esac
`

// curlStub stands for curl: the answers of the API are in files of $STUB: busy (one line per call, "CODE BODY", the last one
// repeats), setup and health (the same), and a call is appended to curl.calls with its arguments; what it is given on its standard
// input (-K -, the configuration) is appended to curl.stdin.
const curlStub = `#!/bin/sh
echo "$*" >>"$STUB/curl.calls"
out="" ; url=""
while [ $# -gt 0 ]; do
	case "$1" in
	-o) out=$2; shift 2 ;;
	-K) [ "$2" = - ] && cat >>"$STUB/curl.stdin"; shift 2 ;;
	--cacert) echo "$2" >>"$STUB/curl.cacert"; shift 2 ;;
	-*k*) echo k >>"$STUB/curl.insecure"; shift ;;
	-w | -m | -H) shift 2 ;;
	-*) shift ;;
	*) url=$1; shift ;;
	esac
done
case "$url" in
*/system/busy)
	n=$(cat "$STUB/busy.n" 2>/dev/null || echo 0)
	n=$((n + 1)); echo "$n" >"$STUB/busy.n"
	line=$(sed -n "${n}p" "$STUB/busy"); [ -n "$line" ] || line=$(tail -n 1 "$STUB/busy")
	;;
*/setup) line=$(cat "$STUB/setup" 2>/dev/null || echo '200 {"completed":true}') ;;
*/system/health) line=$(cat "$STUB/health" 2>/dev/null || echo '200 {"status":"ok"}') ;;
*) echo "curl stub: unexpected: $url" >&2; exit 99 ;;
esac
code=${line%% *}
# real curl prints the status through -w even when it gets no answer: exit 7 is "connection refused", 28 "timed out"
[ "$code" != 000 ] || { printf 000; exit 7; }
[ "$code" != timeout ] || { printf 000; exit 28; }
printf '%s' "${line#* }" >"$out"
printf '%s' "$code"
`

type deployment struct {
	t    *testing.T
	dir  string // the directory of compose.yaml and .env
	stub string
	bin  string
	env  []string
}

func newDeployment(t *testing.T) *deployment {
	t.Helper()
	d := &deployment{t: t, dir: t.TempDir(), stub: t.TempDir(), bin: filepath.Join(t.TempDir(), "bin")}
	d.write(filepath.Join(d.dir, "compose.yaml"), "services: {}\n")
	// the image carries the same compose.yaml, unless a test says otherwise
	d.write(filepath.Join(d.stub, "image", "compose.yaml"), "services: {}\n")
	d.write(filepath.Join(d.stub, "image", "env.example"), "#CHAOSGW_UI_PORT=8443\n")
	d.write(filepath.Join(d.stub, "image", "update.sh"), "#!/bin/sh\n# the update.sh of the image\n")
	d.write(filepath.Join(d.bin, "docker"), dockerStub)
	d.write(filepath.Join(d.bin, "curl"), curlStub)
	for _, f := range []string{"docker", "curl"} {
		if err := os.Chmod(filepath.Join(d.bin, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d.set("busy", "200 {\"busy\":false}")
	d.set("ps", "")
	d.write(filepath.Join(d.stub, "api.pem"), "-----BEGIN CERTIFICATE-----\n")
	return d
}

func (d *deployment) write(name, content string) {
	d.t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d *deployment) set(name, content string) { d.write(filepath.Join(d.stub, name), content) }

// run runs update.sh and returns its output and exit status.
func (d *deployment) run(args ...string) (string, int) {
	d.t.Helper()
	cmd := exec.Command("sh", append([]string{updateScript, "--dir", d.dir}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+d.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB="+d.stub, "CHAOSGW_UPDATE_POLL=0",
		"CHAOSGW_STATE_DIR="+d.stub, "CHAOSGW_API_CERT="+filepath.Join(d.stub, "api.pem"), "CHAOSGW_TOKEN=", "CHAOSGW_VERSION=")
	cmd.Env = append(cmd.Env, d.env...)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &ee):
		return string(out), ee.ExitCode()
	}
	d.t.Fatal(err)
	return "", -1
}

func (d *deployment) calls() []string {
	raw, _ := os.ReadFile(filepath.Join(d.stub, "calls"))
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// index returns the position of the first call that contains sub, or -1.
func (d *deployment) index(sub string) int {
	for i, c := range d.calls() {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

// exact returns the position of the first call that is exactly call (after the version it saw), or -1.
func (d *deployment) exact(call string) int {
	for i, c := range d.calls() {
		if _, rest, ok := strings.Cut(c, "] "); ok && rest == call {
			return i
		}
	}
	return -1
}

func TestAnUpdateOfAnIdleGatewayPullsChecksTheSchemaAndRestartsInThatOrder(t *testing.T) {
	d := newDeployment(t)
	out, code := d.run()
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	pull, schema, up := d.index("pull --quiet chaos-gateway:latest"), d.index("schema-check --state-dir "+d.stub), d.index("compose up -d --remove-orphans")
	files, wait, all := d.index("run --rm --entrypoint tar chaos-gateway:latest"), d.index("compose up -d --wait --wait-timeout 180 exec api svcns kea bird"), d.exact("compose up -d --wait --wait-timeout 180")
	if pull < 0 || schema < 0 || up < 0 || pull >= schema || schema >= files || files >= up || wait <= up || all <= wait {
		t.Errorf("pull %d, schema check %d, files of the image %d, up %d, wait %d, wait for all %d: want them in this order\n%s", pull, schema, files, up, wait, all, strings.Join(d.calls(), "\n"))
	}
	// the new image checks the configuration, read-only and with no network and no capability
	check := d.calls()[schema]
	for _, want := range []string{"--network none", "--read-only", "--cap-drop ALL", "--user 65532:65532", d.stub + ":" + d.stub + ":ro", "--entrypoint chaosgw chaos-gateway:latest"} {
		if !strings.Contains(check, want) {
			t.Errorf("the schema check lacks %q: %s", want, check)
		}
	}
	for _, want := range []string{"running: chaos-gateway:old; target: chaos-gateway:latest", "no run is active", "updated to chaos-gateway:latest: chaosgw 2.0.0", "health: {\"status\":\"ok\"}"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
}

func TestTheVersionIsUsedForThePullAndWrittenIntoEnvOnlyWhenTheUpdateSucceeded(t *testing.T) {
	d := newDeployment(t)
	d.write(filepath.Join(d.dir, ".env"), "CHAOSGW_IMAGE=ghcr.io/x/chaos-gateway\nCHAOSGW_VERSION=1.0.0\nCHAOSGW_UI_PORT=9443\n")
	if out, code := d.run("--version", "1.1.0"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if d.index("[1.1.0] compose config --images") < 0 || d.index("[1.1.0] compose up") < 0 || d.index("pull --quiet chaos-gateway:1.1.0") < 0 {
		t.Errorf("the version did not reach docker compose:\n%s", strings.Join(d.calls(), "\n"))
	}
	env, _ := os.ReadFile(filepath.Join(d.dir, ".env"))
	if string(env) != "CHAOSGW_IMAGE=ghcr.io/x/chaos-gateway\nCHAOSGW_VERSION=1.1.0\nCHAOSGW_UI_PORT=9443\n" {
		t.Errorf(".env = %q", env)
	}
	// a version in a file that has none is added; a failed update writes nothing
	d2 := newDeployment(t)
	d2.set("up", "1")
	if _, code := d2.run("--version", "3.0.0"); code != 5 {
		t.Errorf("a failed restart: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(d2.dir, ".env")); err == nil {
		t.Error("a failed update wrote .env")
	}
	d3 := newDeployment(t)
	if _, code := d3.run("--version", "3.0.0"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if env, _ := os.ReadFile(filepath.Join(d3.dir, ".env")); string(env) != "CHAOSGW_VERSION=3.0.0\n" {
		t.Errorf(".env = %q", env)
	}
}

// An update ends the active runs as aborted, so it waits for them (plan §3.9): it asks GET /system/busy until it says no.
func TestAnUpdateWaitsUntilNoRunIsActive(t *testing.T) {
	d := newDeployment(t)
	d.set("busy", "200 {\"busy\":true,\"runs_active\":1}\n200 {\"busy\":true,\"runs_active\":1}\n200 {\"busy\":false}")
	out, code := d.run()
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if n, _ := os.ReadFile(filepath.Join(d.stub, "busy.n")); strings.TrimSpace(string(n)) != "3" {
		t.Errorf("the script asked %s times, want 3", n)
	}
	if !strings.Contains(out, `waiting for the runs to end: {"busy":true,"runs_active":1}`) || strings.Count(out, "waiting for the runs to end") != 1 {
		t.Errorf("the output must say once what it waits for:\n%s", out)
	}
	if d.index("compose up") < 0 {
		t.Error("no restart")
	}
}

func TestAnUpdateWhileARunIsActiveChangesNothingUnlessForced(t *testing.T) {
	d := newDeployment(t)
	d.set("busy", "200 {\"busy\":true,\"runs_active\":1}")
	out, code := d.run("--timeout", "1")
	if code != 3 || !strings.Contains(out, "containers were not touched") || strings.Contains(out, "nothing was changed") || !strings.Contains(out, "--force") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if d.index("compose up") >= 0 {
		t.Errorf("the containers were restarted under an active run:\n%s", strings.Join(d.calls(), "\n"))
	}
	// --force goes on after the time is up, and says the run ends aborted
	out, code = d.run("--timeout", "1", "--force")
	if code != 0 || !strings.Contains(out, "still active after 1s; --force: it ends aborted") || d.index("compose up") < 0 {
		t.Errorf("--force: exit %d\n%s", code, out)
	}
	// --no-wait does not wait at all
	d2 := newDeployment(t)
	d2.set("busy", "200 {\"busy\":true,\"runs_active\":2}")
	out, code = d2.run("--no-wait")
	if code != 0 || !strings.Contains(out, "--no-wait: it ends aborted") || d2.index("compose up") < 0 {
		t.Errorf("--no-wait: exit %d\n%s", code, out)
	}
}

// The new image decides whether it can read the configuration, before anything is stopped (plan §3.9, downgrade).
func TestAnUpdateToAVersionThatCannotReadTheConfigurationIsRefusedBeforeAnythingStops(t *testing.T) {
	d := newDeployment(t)
	d.set("schema", "1")
	d.set("schema.out", "chaosgw schema-check: store: /var/lib/chaosgw/state/config.json has schema version 7, this version supports up to 1: install a newer Chaos Gateway\n")
	out, code := d.run("--version", "0.9.0")
	if code != 4 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "schema version 7") || !strings.Contains(out, "not updating") || !strings.Contains(out, "keeps running as it is") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	if d.index("compose up") >= 0 || d.index("busy") >= 0 {
		t.Errorf("the schema is checked before anything else happens:\n%s", strings.Join(d.calls(), "\n"))
	}
	if _, err := os.Stat(filepath.Join(d.stub, "busy.n")); err == nil {
		t.Error("the runs were asked about after the refusal")
	}
}

func TestTheBusyQuestionNeedsATokenAndAUnsetUpGatewayHasNoRuns(t *testing.T) {
	d := newDeployment(t)
	d.set("busy", "401 {}")
	out, code := d.run()
	if code != 1 || !strings.Contains(out, "scope read") || d.index("compose up") >= 0 {
		t.Errorf("no token: exit %d\n%s", code, out)
	}
	// the gateway is not set up yet: there is no run, and no token to ask with
	d.set("setup", "200 {\"completed\":false}")
	if out, code := d.run(); code != 0 || !strings.Contains(out, "no run is active") {
		t.Errorf("not set up: exit %d\n%s", code, out)
	}
	// the token goes to the API as a bearer token, from the file or from the variable
	d2 := newDeployment(t)
	d2.write(filepath.Join(d2.dir, "token"), "secret-token-1\n")
	if out, code := d2.run("--token-file", filepath.Join(d2.dir, "token")); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	d3 := newDeployment(t)
	d3.env = []string{"CHAOSGW_TOKEN=secret-token-2"}
	if out, code := d3.run(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// ... on curl's standard input, as a configuration: the arguments of a process are readable by every user of the host
	for dep, token := range map[*deployment]string{d2: "secret-token-1", d3: "secret-token-2"} {
		calls, _ := os.ReadFile(filepath.Join(dep.stub, "curl.calls"))
		stdin, _ := os.ReadFile(filepath.Join(dep.stub, "curl.stdin"))
		if !strings.Contains(string(stdin), `header = "Authorization: Bearer `+token+`"`) || !strings.Contains(string(calls), "-K - ") ||
			!strings.Contains(string(calls), "https://127.0.0.1:8443/api/v1/system/busy") {
			t.Errorf("the token %s was not sent:\n%s\n%s", token, calls, stdin)
		}
		if strings.Contains(string(calls), token) || strings.Contains(strings.Join(dep.calls(), "\n"), token) {
			t.Errorf("the token %s is in the arguments of a command:\n%s", token, calls)
		}
	}
	// a token that could end the line of the curl configuration is refused
	d4 := newDeployment(t)
	d4.env = []string{"CHAOSGW_TOKEN=a\"\nurl = \"file:///etc/passwd"}
	if out, code := d4.run(); code != 1 || !strings.Contains(out, "characters") || d4.index("compose up") >= 0 {
		t.Errorf("a token with a quote: exit %d\n%s", code, out)
	}
	if _, code := newDeployment(t).run("--token-file", "/nonexistent"); code != 1 {
		t.Errorf("a token file that cannot be read: exit %d", code)
	}
}

func TestAnUpdateOfAGatewayWhoseAPIDoesNotAnswerFindsNoRun(t *testing.T) {
	d := newDeployment(t)
	d.set("busy", "000 ")
	out, code := d.run()
	if code != 0 || !strings.Contains(out, "nothing listens on 127.0.0.1:8443: no run is active") {
		t.Errorf("exit %d\n%s", code, out)
	}
	d.env = []string{"CHAOSGW_UI_PORT=9443"}
	d.run()
	raw, _ := os.ReadFile(filepath.Join(d.stub, "curl.calls"))
	if !strings.Contains(string(raw), "https://127.0.0.1:9443/api/v1/system/busy") {
		t.Errorf("the port of the installation is not used:\n%s", raw)
	}
}

// The API listens on management.ui_port of the configuration when that is set, else on CHAOSGW_UI_PORT (cmd/chaosgw/api.go): a refused
// connection on the port of the .env is no proof that no run is active while the API container runs.
func TestARefusedConnectionWhileTheAPIContainerRunsIsNotAGatewayWithoutARun(t *testing.T) {
	d := newDeployment(t)
	d.set("busy", "000 ")
	d.set("apips", "")
	out, code := d.run()
	if code != 1 || !strings.Contains(out, "the API container runs, but nothing listens on 127.0.0.1:8443") || !strings.Contains(out, "management.ui_port") || d.index("compose up") >= 0 {
		t.Errorf("a refused connection and a running API container: exit %d\n%s", code, out)
	}
	if d.exact("compose ps -q api") < 0 {
		t.Errorf("the API container was not asked for:\n%s", strings.Join(d.calls(), "\n"))
	}
	if out, code := d.run("--force"); code != 0 || !strings.Contains(out, "--force: going on") || d.index("compose up") < 0 {
		t.Errorf("--force: exit %d\n%s", code, out)
	}
	// --no-wait does not ask, as for every answer that is none
	if out, code := d.run("--no-wait"); code != 0 {
		t.Errorf("--no-wait: exit %d\n%s", code, out)
	}
	// the API container does not run: nothing listens because nothing runs
	n := newDeployment(t)
	n.set("busy", "000 ")
	if out, code := n.run(); code != 0 || !strings.Contains(out, "nothing listens on 127.0.0.1:8443: no run is active") {
		t.Errorf("no API container: exit %d\n%s", code, out)
	}
}

// The pull moves the tag before the wait (the schema check needs the new image), so a stop leaves the tag on the new image under the
// old containers: the messages of every way out before the restart say so and how to go back, and none says that nothing was changed.
func TestAnUpdateThatStopsAfterThePullSaysThatTheTagMovedAndHowToGoBack(t *testing.T) {
	want := []string{"chaos-gateway:latest now names the new image (sha256:new)", "run the old one (sha256:old)", "docker compose up -d of a single service", "docker tag sha256:old chaos-gateway:latest"}
	check := func(what, out string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: the output lacks %q:\n%s", what, w, out)
			}
		}
		if strings.Contains(out, "nothing was changed") {
			t.Errorf("%s: claims that nothing was changed:\n%s", what, out)
		}
	}
	busy := newDeployment(t)
	busy.set("oldimage", "chaos-gateway:latest")
	busy.set("busy", "200 {\"busy\":true,\"runs_active\":1}")
	out, code := busy.run("--timeout", "1")
	if code != 3 {
		t.Fatalf("a run is active: exit %d\n%s", code, out)
	}
	check("exit 3", out)

	chk := newDeployment(t)
	chk.set("oldimage", "chaos-gateway:latest")
	out, code = chk.run("--check")
	if code != 0 {
		t.Fatalf("--check: exit %d\n%s", code, out)
	}
	check("--check", out)

	// the tag the containers run does not move when another version is pulled: .env keeps naming the old one until the update succeeds
	other := newDeployment(t)
	if out, code := other.run("--check", "--version", "2.0.0"); code != 0 || strings.Contains(out, "now names the new image") {
		t.Errorf("another version: exit %d\n%s", code, out)
	}
}

// A call that gets no answer is not a gateway that has no run: an API that is up and slow (curl exit 28), or whose certificate is not the
// published one, may have a run active, and the update must not restart the containers under it.
func TestAnAPIThatListensAndDoesNotAnswerInTimeStopsTheUpdate(t *testing.T) {
	d := newDeployment(t)
	d.set("busy", "timeout ")
	out, code := d.run()
	if code != 1 || !strings.Contains(out, "did not answer in time") || d.index("compose up") >= 0 {
		t.Errorf("a timeout: exit %d\n%s", code, out)
	}
	if out, code := d.run("--force"); code != 0 || !strings.Contains(out, "--force: going on") || d.index("compose up") < 0 {
		t.Errorf("a timeout with --force: exit %d\n%s", code, out)
	}
	// an answer after a few polls is still a run to wait for
	d2 := newDeployment(t)
	d2.set("busy", "200 {\"busy\":true}\n200 {\"busy\":false}")
	if out, code := d2.run(); code != 0 || !strings.Contains(out, "waiting for the runs to end") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// The token goes only to the API that shows the certificate it published; without that file nothing secret is sent, and the call is
// the unauthenticated one.
func TestTheTokenGoesOnlyToTheAPIWithThePublishedCertificate(t *testing.T) {
	d := newDeployment(t)
	d.env = []string{"CHAOSGW_TOKEN=secret-token-3"}
	if out, code := d.run(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	cacert, _ := os.ReadFile(filepath.Join(d.stub, "curl.cacert"))
	stdin, _ := os.ReadFile(filepath.Join(d.stub, "curl.stdin"))
	insecure, _ := os.ReadFile(filepath.Join(d.stub, "curl.insecure"))
	if !strings.Contains(string(cacert), filepath.Join(d.stub, "api.pem")) || len(insecure) != 0 || !strings.Contains(string(stdin), "secret-token-3") {
		t.Errorf("with the certificate: cacert %q, insecure %q, stdin %q", cacert, insecure, stdin)
	}

	n := newDeployment(t)
	n.env = []string{"CHAOSGW_TOKEN=secret-token-4", "CHAOSGW_API_CERT=" + filepath.Join(n.stub, "missing.pem")}
	n.set("busy", "401 ")
	out, code := n.run()
	stdin, _ = os.ReadFile(filepath.Join(n.stub, "curl.stdin"))
	if code != 1 || !strings.Contains(out, "it is sent only when") || strings.Contains(string(stdin), "secret-token-4") || n.index("compose up") >= 0 {
		t.Errorf("without the certificate: exit %d, token sent %q\n%s", code, stdin, out)
	}
	if calls, _ := os.ReadFile(filepath.Join(n.stub, "curl.calls")); strings.Contains(string(calls), "-K") || !strings.Contains(string(calls), "-ksS") {
		t.Errorf("without the certificate the call is the unauthenticated one:\n%s", calls)
	}
}

// docker compose reads the port from .env, so the script asks the API on that port too, and the environment wins over the file.
func TestThePortOfTheEnvFileIsUsedForTheBusyQuestion(t *testing.T) {
	d := newDeployment(t)
	d.write(filepath.Join(d.dir, ".env"), "CHAOSGW_VERSION=1.0.0\nCHAOSGW_UI_PORT=9443\n")
	if out, code := d.run(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	raw, _ := os.ReadFile(filepath.Join(d.stub, "curl.calls"))
	if !strings.Contains(string(raw), "https://127.0.0.1:9443/api/v1/system/busy") || strings.Contains(string(raw), ":8443") {
		t.Errorf("the port of .env is not used:\n%s", raw)
	}
	d.env = []string{"CHAOSGW_UI_PORT=7443"}
	d.run()
	raw, _ = os.ReadFile(filepath.Join(d.stub, "curl.calls"))
	if !strings.Contains(string(raw), "https://127.0.0.1:7443/api/v1/system/busy") {
		t.Errorf("the port of the environment is not used:\n%s", raw)
	}
	d.env = []string{"CHAOSGW_UI_PORT=abc"}
	if _, code := d.run(); code != 1 {
		t.Errorf("a port that is no number: exit %d", code)
	}
}

func TestCheckStopsBeforeTheRestart(t *testing.T) {
	d := newDeployment(t)
	out, code := d.run("--check", "--no-pull")
	if code != 0 || d.index("compose up") >= 0 || d.index("pull") >= 0 || !strings.Contains(out, "--check: the containers would be restarted") {
		t.Errorf("exit %d\n%s\n%s", code, out, strings.Join(d.calls(), "\n"))
	}
}

func TestAnUpdateThatDoesNotComeUpSaysHowToGoBack(t *testing.T) {
	d := newDeployment(t)
	d.set("up.wait", "1")
	out, code := d.run()
	if code != 5 || !strings.Contains(out, "did not become healthy within 180s") || !strings.Contains(out, "chaos-gateway:old") {
		t.Errorf("exit %d\n%s", code, out)
	}
	d = newDeployment(t)
	d.set("up", "1")
	if out, code := d.run(); code != 5 || !strings.Contains(out, "did not start") || !strings.Contains(out, "chaos-gateway:old") || d.index("--wait") >= 0 {
		t.Errorf("containers that do not start: exit %d\n%s", code, out)
	}
	d = newDeployment(t)
	d.set("health", "503 {}")
	if out, code := d.run(); code != 5 || !strings.Contains(out, "health check with 503") {
		t.Errorf("an API that is unhealthy: exit %d\n%s", code, out)
	}
	d = newDeployment(t)
	d.set("pull", "1")
	if out, code := d.run(); code != 1 || !strings.Contains(out, "cannot pull") || d.index("compose up") >= 0 {
		t.Errorf("a pull that fails: exit %d\n%s", code, out)
	}
}

func TestTheOptionsAreChecked(t *testing.T) {
	d := newDeployment(t)
	for _, args := range [][]string{{"--bogus"}, {"--version"}, {"--version", "a b"}, {"--version", "../x"}, {"--timeout", "soon"}, {"--timeout"}, {"--token-file"}, {"--dir"}} {
		if out, code := d.run(args...); code != 2 || !strings.HasPrefix(out, "update: ") {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
	if len(d.calls()) > 1 || d.calls()[0] != "" {
		t.Errorf("a refused option reached docker: %v", d.calls())
	}
	out, code := d.run("--help")
	if code != 0 || !strings.Contains(out, "Chaos Gateway update") || !strings.Contains(out, "--no-pull") || strings.Contains(out, "set -eu") {
		t.Errorf("--help: exit %d\n%s", code, out)
	}
	empty := t.TempDir()
	d.dir = empty
	if out, code := d.run(); code != 1 || !strings.Contains(out, "has no compose.yaml") {
		t.Errorf("no compose.yaml: exit %d\n%s", code, out)
	}
}
