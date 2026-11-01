package deployscripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const uninstallScript = "../../deploy/uninstall.sh"

// uninstallDockerStub stands for the docker uninstall.sh calls. What it answers is in files of $STUB: stop, down, teardown and rm (the
// exit statuses of `compose stop`, `compose down`, of the teardown service and of `image rm`, default 0); every call is appended to calls.
const uninstallDockerStub = `#!/bin/sh
echo "$*" >>"$STUB/calls"
status() { if [ -f "$STUB/$1" ]; then cat "$STUB/$1"; else echo 0; fi; }
case "$*" in
"compose version") echo "Docker Compose version v2.24.6" ;;
"compose config --images") printf 'ghcr.io/andste82/chaos-gateway:1.0.0\nghcr.io/andste82/chaos-gateway:1.0.0\n' ;;
"compose stop") exit "$(status stop)" ;;
"compose down -v --remove-orphans") exit "$(status down)" ;;
"compose --profile teardown run --rm teardown"*) exit "$(status teardown)" ;;
"image rm "*) exit "$(status rm)" ;;
*) echo "docker stub: unexpected: $*" >&2; exit 99 ;;
esac
`

// removalHostSetup stands for host-setup.sh in the installation directory: --remove-test-interface IF removes the rule file of IF.
const removalHostSetup = `#!/bin/sh
echo "host-setup $*" >>"$STUB/calls"
if [ "$1" = --remove-test-interface ]; then
	prefix=""
	[ "${3:-}" = --prefix ] && prefix=$4
	rm -f "$prefix/etc/systemd/network/10-chaosgw-$2.network"
fi
`

type removal struct {
	t      *testing.T
	prefix string
	dir    string // the installation directory
	stub   string
	bin    string
	uid    string
}

func newRemoval(t *testing.T) *removal {
	t.Helper()
	r := &removal{t: t, prefix: t.TempDir(), stub: t.TempDir(), bin: t.TempDir(), uid: "0"}
	r.dir = filepath.Join(r.prefix, "opt", "chaosgw")
	r.put(filepath.Join(r.bin, "docker"), uninstallDockerStub, 0o755)
	r.put(filepath.Join(r.bin, "id"), "#!/bin/sh\necho \"$FAKE_UID\"\n", 0o755)
	r.put(filepath.Join(r.dir, "compose.yaml"), "services: {}\n", 0o644)
	r.put(filepath.Join(r.dir, ".env"), "CHAOSGW_VERSION=1.0.0\n", 0o644)
	r.put(filepath.Join(r.dir, "host-setup.sh"), removalHostSetup, 0o755)
	r.put(r.data("state", "config.json"), "{}\n", 0o600)
	r.put(r.data("secrets", "key"), "secret\n", 0o600)
	r.put(r.file("etc/systemd/network/10-chaosgw-lan0.network"), "[Match]\nName=lan0\n\n[Link]\nUnmanaged=yes\n", 0o644)
	r.put(r.file("etc/modules-load.d/chaos-gateway.conf"), "nf_tables\n", 0o644)
	r.put(r.file("etc/sysctl.d/90-chaos-gateway.conf"), "net.ipv4.ip_forward=1\n", 0o644)
	return r
}

func (r *removal) put(name, content string, mode os.FileMode) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		r.t.Fatal(err)
	}
}

func (r *removal) file(rel string) string { return filepath.Join(r.prefix, rel) }
func (r *removal) data(parts ...string) string {
	return filepath.Join(append([]string{r.prefix, "var/lib/chaosgw"}, parts...)...)
}
func (r *removal) set(name, content string) { r.put(filepath.Join(r.stub, name), content, 0o644) }
func (r *removal) exists(name string) bool  { _, err := os.Stat(name); return err == nil }
func (r *removal) calls() []string {
	raw, _ := os.ReadFile(filepath.Join(r.stub, "calls"))
	if len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func (r *removal) index(sub string) int {
	for n, c := range r.calls() {
		if strings.Contains(c, sub) {
			return n
		}
	}
	return -1
}

// run runs uninstall.sh below the prefix, so no root is needed, unless a test says otherwise (withPrefix false).
func (r *removal) run(withPrefix bool, args ...string) (string, int) {
	r.t.Helper()
	full := []string{uninstallScript, "--dir", r.dir}
	if withPrefix {
		full = append(full, "--prefix", r.prefix)
	}
	cmd := exec.Command("sh", append(full, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB="+r.stub, "FAKE_UID="+r.uid)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &ee):
		return string(out), ee.ExitCode()
	}
	r.t.Fatal(err)
	return "", -1
}

func TestUninstallStopsRemovesTheKernelAndGivesTheInterfacesBackButKeepsTheDataAndTheFiles(t *testing.T) {
	r := newRemoval(t)
	out, code := r.run(true)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// stop first (the executor cleans up, and the teardown refuses while one serves), the teardown, then down -v (the teardown made the volumes
	// again), then the rules of networkd
	stop, teardown, down, hs := r.index("compose stop"), r.index("compose --profile teardown run --rm teardown"), r.index("compose down -v --remove-orphans"), r.index("host-setup --remove-test-interface lan0")
	if stop < 0 || teardown < stop || down < teardown || hs < down {
		t.Fatalf("stop %d, teardown %d, down %d, networkd rule %d: not in that order:\n%s", stop, teardown, down, hs, strings.Join(r.calls(), "\n"))
	}
	if r.index("--purge") >= 0 || r.index("image rm") >= 0 {
		t.Errorf("a plain uninstall purged or removed images:\n%s", strings.Join(r.calls(), "\n"))
	}
	if r.exists(r.file("etc/systemd/network/10-chaosgw-lan0.network")) {
		t.Error("the networkd rule of lan0 is still there")
	}
	for _, keep := range []string{r.data("state", "config.json"), r.data("secrets", "key"), filepath.Join(r.dir, "compose.yaml"), filepath.Join(r.dir, ".env"),
		r.file("etc/modules-load.d/chaos-gateway.conf"), r.file("etc/sysctl.d/90-chaos-gateway.conf")} {
		if !r.exists(keep) {
			t.Errorf("%s was deleted by an uninstall without --purge", keep)
		}
	}
	for _, want := range []string{"stopped and out of the kernel", "install.sh brings it back", "--purge deletes all of it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the closing text does not say %q:\n%s", want, out)
		}
	}
}

func TestPurgeDeletesTheDataTheFilesTheImagesAndTheHostConfigurationAfterTheTeardownEmptiedTheDirectories(t *testing.T) {
	r := newRemoval(t)
	out, code := r.run(true, "--purge", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if r.index("compose --profile teardown run --rm teardown --purge") < 0 {
		t.Errorf("the teardown was not run with --purge:\n%s", strings.Join(r.calls(), "\n"))
	}
	if r.index("image rm ghcr.io/andste82/chaos-gateway:1.0.0") < 0 {
		t.Errorf("the image was not removed (once, though compose names it twice):\n%s", strings.Join(r.calls(), "\n"))
	}
	for _, gone := range []string{r.data(), r.dir, r.file("etc/systemd/network/10-chaosgw-lan0.network"),
		r.file("etc/modules-load.d/chaos-gateway.conf"), r.file("etc/sysctl.d/90-chaos-gateway.conf")} {
		if r.exists(gone) {
			t.Errorf("%s is still there after --purge", gone)
		}
	}
	if !strings.Contains(out, "is gone from this host") {
		t.Errorf("the closing text:\n%s", out)
	}
}

func TestPurgeWithoutYesAsksAndWithoutATerminalChangesNothing(t *testing.T) {
	r := newRemoval(t)
	out, code := r.run(true, "--purge")
	if code != 2 || !strings.Contains(out, "add --yes") || !strings.Contains(out, "deletes, for good") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if c := r.calls(); len(c) != 0 {
		t.Errorf("docker was called before the answer: %v", c)
	}
	if !r.exists(r.data("state", "config.json")) || !r.exists(filepath.Join(r.dir, "compose.yaml")) {
		t.Error("something was deleted without an answer")
	}
}

func TestAFailedTeardownKeepsTheDataAndTheDeploymentAndSaysSo(t *testing.T) {
	r := newRemoval(t)
	r.set("teardown", "1")
	out, code := r.run(true, "--purge", "--yes")
	if code != 1 || !strings.Contains(out, "the teardown could not remove everything") || !strings.Contains(out, "the data and "+r.dir+" stay") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !r.exists(r.data("state", "config.json")) || !r.exists(filepath.Join(r.dir, "compose.yaml")) {
		t.Error("the data or the deployment was deleted although the teardown failed: a second run needs the executor's state in them")
	}
	// the other steps went on
	if r.exists(r.file("etc/systemd/network/10-chaosgw-lan0.network")) {
		t.Error("the networkd rule was not removed after the failed teardown")
	}
}

func TestAFailedStopIsReportedAndTheTeardownStillRuns(t *testing.T) {
	r := newRemoval(t)
	r.set("stop", "1")
	out, code := r.run(true)
	if code != 1 || !strings.Contains(out, "docker compose stop failed") || r.index("compose --profile teardown run --rm teardown") < 0 || r.index("compose down -v") < 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, strings.Join(r.calls(), "\n"))
	}
}

func TestWithoutAComposeFileThereAreNoContainersToStopAndTheRulesStillGo(t *testing.T) {
	r := newRemoval(t)
	if err := os.Remove(filepath.Join(r.dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	out, code := r.run(true)
	if code != 0 || !strings.Contains(out, "no containers to stop") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if c := r.calls(); r.index("compose down") >= 0 || r.index("teardown") >= 0 {
		t.Errorf("containers were stopped although there is no compose.yaml: %v", c)
	}
	if r.exists(r.file("etc/systemd/network/10-chaosgw-lan0.network")) {
		t.Error("the networkd rule is still there")
	}
}

func TestWithoutTheHostSetupTheRuleFilesAreRemovedDirectly(t *testing.T) {
	r := newRemoval(t)
	r.put(r.file("etc/systemd/network/10-chaosgw-lan1.network"), "[Link]\nUnmanaged=yes\n", 0o644)
	if err := os.Remove(filepath.Join(r.dir, "host-setup.sh")); err != nil {
		t.Fatal(err)
	}
	out, code := r.run(true)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, n := range []string{"lan0", "lan1"} {
		if r.exists(r.file("etc/systemd/network/10-chaosgw-" + n + ".network")) {
			t.Errorf("the rule of %s is still there", n)
		}
	}
	if r.index("host-setup") >= 0 {
		t.Errorf("a host setup that is not there was called: %v", r.calls())
	}
}

func TestEveryTestInterfaceIsGivenBack(t *testing.T) {
	r := newRemoval(t)
	r.put(r.file("etc/systemd/network/10-chaosgw-lan1.network"), "[Link]\nUnmanaged=yes\n", 0o644)
	r.put(r.file("etc/systemd/network/10-chaosgw-eth2.network"), "[Link]\nUnmanaged=yes\n", 0o644)
	r.put(r.file("etc/systemd/network/10-other.network"), "[Match]\nName=wan0\n", 0o644)
	if out, code := r.run(true); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, n := range []string{"lan0", "lan1", "eth2"} {
		if r.index("host-setup --remove-test-interface "+n) < 0 {
			t.Errorf("%s was not given back:\n%s", n, strings.Join(r.calls(), "\n"))
		}
	}
	if !r.exists(r.file("etc/systemd/network/10-other.network")) {
		t.Error("a rule that is not Chaos Gateway's was deleted")
	}
}

func TestUninstallRefusesWhatItMustNotDeleteBeforeItChangesAnything(t *testing.T) {
	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"the root":          {[]string{"--dir", "/", "--purge", "--yes"}, "not a directory of a deployment"},
		"an empty path":     {[]string{"--dir", "", "--purge", "--yes"}, "not a directory of a deployment"},
		"a relative path":   {[]string{"--dir", "opt/chaosgw", "--purge", "--yes"}, "not an absolute directory"},
		"a parent":          {[]string{"--dir", "/opt/chaosgw/../..", "--purge", "--yes"}, "no .. in the directory"},
		"an unknown option": {[]string{"--wipe"}, "unknown option"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRemoval(t)
			cmd := exec.Command("sh", append([]string{uninstallScript, "--prefix", r.prefix}, tt.args...)...)
			cmd.Env = append(os.Environ(), "PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB="+r.stub, "FAKE_UID=0")
			out, err := cmd.CombinedOutput()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(string(out), tt.want) {
				t.Fatalf("%v: want exit 2 and %q:\n%s", err, tt.want, out)
			}
			if c := r.calls(); len(c) != 0 || !r.exists(r.data("state", "config.json")) {
				t.Errorf("something was done before the refusal: %v", c)
			}
		})
	}
}

func TestUninstallNeedsRootWithoutAPrefix(t *testing.T) {
	r := newRemoval(t)
	r.uid = "1000"
	out, code := r.run(false)
	if code != 2 || !strings.Contains(out, "run it as root") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if c := r.calls(); len(c) != 0 {
		t.Errorf("docker was called without root: %v", c)
	}
}

func TestADirectoryWithoutAComposeFileOrEnvIsNotDeletedByPurge(t *testing.T) {
	r := newRemoval(t)
	other := filepath.Join(r.prefix, "home", "someone")
	r.put(filepath.Join(other, "notes.txt"), "mine\n", 0o644)
	cmd := exec.Command("sh", uninstallScript, "--dir", other, "--prefix", r.prefix, "--purge", "--yes")
	cmd.Env = append(os.Environ(), "PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB="+r.stub, "FAKE_UID=0")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "holds no compose.yaml or .env: it is not deleted") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !r.exists(filepath.Join(other, "notes.txt")) {
		t.Error("a directory that is no deployment was deleted")
	}
}

func TestTheUninstallHelpIsTheHeaderOfTheScript(t *testing.T) {
	r := newRemoval(t)
	out, code := r.run(false, "--help")
	if code != 0 || !strings.Contains(out, "--purge") || !strings.Contains(out, "Exit status:") || strings.Contains(out, "set -eu") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
