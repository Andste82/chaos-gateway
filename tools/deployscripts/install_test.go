package deployscripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const installScript = "../../deploy/install.sh"

// installDockerStub stands for the docker install.sh calls. What it answers is in files of $STUB: compose (the exit status of
// `docker compose version`), pull, up and wait (the exit statuses of the pull, of `compose up -d` and of the wait for the health
// checks, default 0), hostsetup.empty (the image holds no host setup); every call is appended to calls. The host setup it hands out
// is $STUB/host-setup.sh.
const installDockerStub = `#!/bin/sh
echo "$*" >>"$STUB/calls"
status() { if [ -f "$STUB/$1" ]; then cat "$STUB/$1"; else echo 0; fi; }
case "$*" in
"compose version") exit "$(status compose)" ;;
"pull --quiet "*) exit "$(status pull)" ;;
"run --rm --entrypoint cat "*" /usr/local/share/chaosgw/host-setup.sh") [ -f "$STUB/hostsetup.empty" ] || cat "$STUB/host-setup.sh" ;;
"compose up -d --remove-orphans") exit "$(status up)" ;;
"compose up -d --wait --wait-timeout "*" exec api svcns kea bird") exit "$(status wait)" ;;
*) echo "docker stub: unexpected: $*" >&2; exit 99 ;;
esac
`

// hostSetupStub is the host setup of the image: it records its arguments, makes the compose.yaml of the installation directory and
// exits with the status in $STUB/hostsetup.
const hostSetupStub = `#!/bin/sh
echo "$*" >>"$STUB/hostsetup.args"
dir=""
while [ $# -gt 0 ]; do
	case "$1" in
	--install-dir) dir=$2; shift 2 ;;
	*) shift ;;
	esac
done
[ -z "$dir" ] || { mkdir -p "$dir" && echo "services: {}" >"$dir/compose.yaml"; }
if [ -f "$STUB/hostsetup" ]; then exit "$(cat "$STUB/hostsetup")"; fi
`

type installation struct {
	t      *testing.T
	dir    string // the installation directory
	stub   string
	bin    string
	uid    string
	extend []string
}

func newInstallation(t *testing.T) *installation {
	t.Helper()
	i := &installation{t: t, dir: filepath.Join(t.TempDir(), "opt", "chaosgw"), stub: t.TempDir(), bin: t.TempDir(), uid: "0"}
	i.put(filepath.Join(i.bin, "docker"), installDockerStub, 0o755)
	i.put(filepath.Join(i.bin, "id"), "#!/bin/sh\nif [ \"$1\" = -u ]; then echo \"$FAKE_UID\"; else echo uid=0; fi\n", 0o755)
	i.put(filepath.Join(i.stub, "host-setup.sh"), hostSetupStub, 0o644)
	return i
}

func (i *installation) put(name, content string, mode os.FileMode) {
	i.t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		i.t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		i.t.Fatal(err)
	}
}

func (i *installation) set(name, content string) { i.put(filepath.Join(i.stub, name), content, 0o644) }

// run runs install.sh with the stubs in front of the real programs and returns its output and exit status.
func (i *installation) run(args ...string) (string, int) {
	i.t.Helper()
	return i.runWithPath(i.bin+string(os.PathListSeparator)+os.Getenv("PATH"), args...)
}

func (i *installation) runWithPath(path string, args ...string) (string, int) {
	i.t.Helper()
	cmd := exec.Command("sh", append([]string{installScript, "--dir", i.dir}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+path, "STUB="+i.stub, "FAKE_UID="+i.uid, "CHAOSGW_INSTALL_HEALTH_WAIT=7", "TMPDIR="+i.t.TempDir())
	cmd.Env = append(cmd.Env, i.extend...)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &ee):
		return string(out), ee.ExitCode()
	}
	i.t.Fatal(err)
	return "", -1
}

func (i *installation) calls() []string {
	raw, _ := os.ReadFile(filepath.Join(i.stub, "calls"))
	if len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func (i *installation) index(sub string) int {
	for n, c := range i.calls() {
		if strings.Contains(c, sub) {
			return n
		}
	}
	return -1
}

func (i *installation) hostSetupArgs() string {
	raw, _ := os.ReadFile(filepath.Join(i.stub, "hostsetup.args"))
	return strings.TrimSpace(string(raw))
}

func TestAnInstallationPullsRunsTheHostSetupOfTheImageAndStartsInThatOrder(t *testing.T) {
	i := newInstallation(t)
	out, code := i.run("--test-interface", "lan0", "--test-interface", "lan1")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	order := []string{
		"pull --quiet ghcr.io/andste82/chaos-gateway:latest",
		"run --rm --entrypoint cat ghcr.io/andste82/chaos-gateway:latest /usr/local/share/chaosgw/host-setup.sh",
		"compose up -d --remove-orphans",
		"compose up -d --wait --wait-timeout 7 exec api svcns kea bird",
	}
	last := -1
	for _, want := range order {
		n := i.index(want)
		if n < 0 || n < last {
			t.Fatalf("%q is not in the calls, or out of order (%d after %d):\n%s", want, n, last, strings.Join(i.calls(), "\n"))
		}
		last = n
	}
	// the host setup is the one of the image, with the image, the directory and every test interface
	if got, want := i.hostSetupArgs(), "--image ghcr.io/andste82/chaos-gateway:latest --install-dir "+i.dir+" --test-interface lan0 --test-interface lan1"; got != want {
		t.Errorf("the host setup got %q, want %q", got, want)
	}
	for _, want := range []string{"Chaos Gateway is running", "https://<management address of this host>:8443", "grep 'Setup token'", "update.sh", "uninstall.sh"} {
		if !strings.Contains(out, want) {
			t.Errorf("the closing text does not say %q:\n%s", want, out)
		}
	}
}

func TestTheVersionAndTheImageNameTheImage(t *testing.T) {
	i := newInstallation(t)
	if out, code := i.run("--version", "1.2.3"); code != 0 || i.index("pull --quiet ghcr.io/andste82/chaos-gateway:1.2.3") < 0 {
		t.Fatalf("--version: exit %d\n%s\n%s", code, out, strings.Join(i.calls(), "\n"))
	}
	j := newInstallation(t)
	if out, code := j.run("--image", "localhost:5000/chaos-gateway:appliance"); code != 0 || j.index("pull --quiet localhost:5000/chaos-gateway:appliance") < 0 ||
		!strings.Contains(j.hostSetupArgs(), "--image localhost:5000/chaos-gateway:appliance") {
		t.Fatalf("--image: exit %d\n%s\n%s", code, out, strings.Join(j.calls(), "\n"))
	}
}

func TestAnInstallationRefusesWhatItCannotDoBeforeItChangesAnything(t *testing.T) {
	for name, tt := range map[string]struct {
		args []string
		uid  string
		want string
	}{
		"not root":               {nil, "1000", "run it as root"},
		"both image and version": {[]string{"--image", "x/y:z", "--version", "1.0.0"}, "0", "exclude each other"},
		"a version with a slash": {[]string{"--version", "1/2"}, "0", "not an image tag"},
		"an interface name":      {[]string{"--test-interface", "lan0;reboot"}, "0", "not an interface name"},
		"an image reference":     {[]string{"--image", "-v"}, "0", "not an image reference"},
		"a relative directory":   {[]string{"--dir", "opt/x"}, "0", "not an absolute directory"},
		"an unknown option":      {[]string{"--frobnicate"}, "0", "unknown option"},
		"a missing value":        {[]string{"--version"}, "0", "needs a version"},
	} {
		t.Run(name, func(t *testing.T) {
			i := newInstallation(t)
			i.uid = tt.uid
			out, code := i.run(tt.args...)
			if code != 2 || !strings.Contains(out, tt.want) {
				t.Errorf("exit %d, want 2 and %q:\n%s", code, tt.want, out)
			}
			if c := i.calls(); len(c) != 0 {
				t.Errorf("docker was called before the arguments were refused: %v", c)
			}
		})
	}
}

func TestAPullThatFailsStopsBeforeTheHostSetup(t *testing.T) {
	i := newInstallation(t)
	i.set("pull", "1")
	out, code := i.run("--version", "9.9.9")
	if code != 1 || !strings.Contains(out, "cannot pull ghcr.io/andste82/chaos-gateway:9.9.9") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if i.index("host-setup.sh") >= 0 || i.hostSetupArgs() != "" {
		t.Errorf("the host setup ran after the failed pull:\n%s", strings.Join(i.calls(), "\n"))
	}
}

func TestAHostSetupThatFindsAStepMissingStopsTheInstallationAndSaysToRunItAgain(t *testing.T) {
	i := newInstallation(t)
	i.set("hostsetup", "1")
	out, code := i.run("--test-interface", "lan0")
	if code != 1 || !strings.Contains(out, "run install.sh again") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if i.index("compose up") >= 0 {
		t.Errorf("the containers were started although the host setup failed:\n%s", strings.Join(i.calls(), "\n"))
	}
}

func TestAnImageWithoutAHostSetupIsRefused(t *testing.T) {
	i := newInstallation(t)
	i.set("hostsetup.empty", "")
	out, code := i.run()
	if code != 1 || !strings.Contains(out, "holds no host setup") || i.hostSetupArgs() != "" {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestNoPullAndNoStartSkipTheirSteps(t *testing.T) {
	i := newInstallation(t)
	out, code := i.run("--no-pull", "--no-start")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if i.index("pull") >= 0 {
		t.Errorf("--no-pull pulled:\n%s", strings.Join(i.calls(), "\n"))
	}
	if i.index("compose up") >= 0 || !strings.Contains(out, "docker compose up -d") {
		t.Errorf("--no-start started the containers, or does not say how:\n%s\n%s", out, strings.Join(i.calls(), "\n"))
	}
	if i.hostSetupArgs() == "" {
		t.Error("--no-start skipped the host setup")
	}
}

func TestContainersThatDoNotBecomeHealthyAreReportedWithExit5(t *testing.T) {
	i := newInstallation(t)
	i.set("wait", "1")
	out, code := i.run()
	if code != 5 || !strings.Contains(out, "not healthy") || strings.Contains(out, "Chaos Gateway is running") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	j := newInstallation(t)
	j.set("up", "1")
	if out, code := j.run(); code != 1 || !strings.Contains(out, "cannot start the containers") {
		t.Fatalf("a start that fails: exit %d\n%s", code, out)
	}
}

func TestWithoutATestInterfaceTheInstallationSaysSo(t *testing.T) {
	i := newInstallation(t)
	out, code := i.run()
	if code != 0 || !strings.Contains(out, "no --test-interface") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestWithoutDockerTheHostSetupOfTheReleaseInstallsIt(t *testing.T) {
	for _, tt := range []struct{ name, args, url string }{
		{"the newest release", "", "https://example.test/releases/latest/download/host-setup.sh"},
		{"a version", "1.2.3", "https://example.test/releases/download/v1.2.3/host-setup.sh"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := newInstallation(t)
			// a PATH that has no docker: the stubs of curl and id, and the few programs the script uses
			path := t.TempDir()
			for _, p := range []string{"sh", "mktemp", "rm", "sed", "cat", "chmod", "cp", "mkdir"} {
				real, err := exec.LookPath(p)
				if err != nil {
					t.Skipf("%s is not installed", p)
				}
				if err := os.Symlink(real, filepath.Join(path, p)); err != nil {
					t.Fatal(err)
				}
			}
			i.put(filepath.Join(path, "id"), "#!/bin/sh\necho 0\n", 0o755)
			// curl hands out the host setup of the release, which "installs" Docker by putting the stub into the PATH
			i.put(filepath.Join(path, "curl"), "#!/bin/sh\necho \"$*\" >>\"$STUB/curl.calls\"\nout=\"\"\nwhile [ $# -gt 0 ]; do case \"$1\" in -o) out=$2; shift 2 ;; *) shift ;; esac; done\ncp \"$STUB/bootstrap.sh\" \"$out\"\n", 0o755)
			i.set("bootstrap.sh", "#!/bin/sh\necho bootstrap >>\"$STUB/bootstrap.ran\"\ncp \""+filepath.Join(i.bin, "docker")+"\" \""+filepath.Join(path, "docker")+"\"\nchmod 0755 \""+filepath.Join(path, "docker")+"\"\n")
			i.extend = []string{"CHAOSGW_RELEASES=https://example.test/releases"}
			args := []string{}
			if tt.args != "" {
				args = append(args, "--version", tt.args)
			}
			out, code := i.runWithPath(path, args...)
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, out)
			}
			calls, _ := os.ReadFile(filepath.Join(i.stub, "curl.calls"))
			if !strings.Contains(string(calls), tt.url) {
				t.Errorf("curl was not asked for %s:\n%s", tt.url, calls)
			}
			if _, err := os.Stat(filepath.Join(i.stub, "bootstrap.ran")); err != nil {
				t.Error("the host setup of the release did not run")
			}
			if i.index("compose up -d --remove-orphans") < 0 {
				t.Errorf("the installation did not go on after Docker was there:\n%s", out)
			}
		})
	}
}

func TestWithoutDockerAndWithoutCurlTheInstallationSaysWhatToInstall(t *testing.T) {
	i := newInstallation(t)
	path := t.TempDir()
	for _, p := range []string{"sh", "mktemp", "rm", "sed"} {
		real, err := exec.LookPath(p)
		if err != nil {
			t.Skipf("%s is not installed", p)
		}
		if err := os.Symlink(real, filepath.Join(path, p)); err != nil {
			t.Fatal(err)
		}
	}
	i.put(filepath.Join(path, "id"), "#!/bin/sh\necho 0\n", 0o755)
	out, code := i.runWithPath(path)
	if code != 1 || !strings.Contains(out, "curl is needed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestTheHelpIsTheHeaderOfTheScript(t *testing.T) {
	i := newInstallation(t)
	out, code := i.run("--help")
	if code != 0 || !strings.Contains(out, "--test-interface IF") || !strings.Contains(out, "Exit status:") || strings.Contains(out, "set -eu") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
