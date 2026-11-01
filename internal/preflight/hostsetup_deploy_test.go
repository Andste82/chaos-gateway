package preflight

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The directories of the persistence layout (plan §3.6, M28) with the owners the containers need. The compose file mounts them
// from the host and does not let Docker make a missing one as root's.
var dataDirs = map[string]struct{ mode os.FileMode }{
	"state": {0o755}, "secrets": {0o700}, "api": {0o755}, "service": {0o755}, "tlsca": {0o755}, "captures": {0o755}, "exec": {0o755}, "kea": {0o755},
}

func TestHostSetupMakesTheDataDirectoriesAndPutsRightWhatIsWrong(t *testing.T) {
	dir := t.TempDir()
	out, err := runHostSetup(t, "--prefix", dir, "--skip-docker", "--skip-modprobe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for name, want := range dataDirs {
		fi, err := os.Stat(filepath.Join(dir, "var/lib/chaosgw", name))
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != want.mode {
			t.Errorf("%s: %v %v, want a directory with %04o", name, fi, err, want.mode)
		}
		if !strings.Contains(out, "made /var/lib/chaosgw/"+name+" (owner ") {
			t.Errorf("the setup does not say it made %s:\n%s", name, out)
		}
	}
	if strings.Contains(out, "is up to date") {
		t.Errorf("the directories are not files that are up to date: the level 2 test counts those lines:\n%s", out)
	}

	// a second run leaves them alone; a mode that was changed is put right, and the content of a directory is not touched
	secrets := filepath.Join(dir, "var/lib/chaosgw/secrets")
	if err := os.Chmod(secrets, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(secrets, "admin.json"), "{}")
	if err := os.Chmod(filepath.Join(secrets, "admin.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = runHostSetup(t, "--prefix", dir, "--skip-docker", "--skip-modprobe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Count(out, "is as it should be") != len(dataDirs)-1 || !strings.Contains(out, "made /var/lib/chaosgw/secrets (owner 65532, mode 0700)") {
		t.Errorf("the second run:\n%s", out)
	}
	if fi, _ := os.Stat(secrets); fi.Mode().Perm() != 0o700 {
		t.Errorf("secrets: %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(secrets, "admin.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("the file in the secrets: %v", fi.Mode())
	}
}

func TestHostSetupDataOnlyMakesNothingElse(t *testing.T) {
	dir := t.TempDir()
	if out, err := runHostSetup(t, "--prefix", dir, "--data-only"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	files := treeOf(t, dir)
	if len(files) != 0 {
		t.Errorf("--data-only wrote files: %v", files)
	}
	for name := range dataDirs {
		if _, err := os.Stat(filepath.Join(dir, "var/lib/chaosgw", name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, args := range [][]string{{"--data-only", "--check", "lan0"}, {"--data-only", "--remove-test-interface", "lan0"}, {"--data-only", "--test-interface", "lan0"}, {"--data-only", "--image", "x"}} {
		out, err := runHostSetup(t, append([]string{"--prefix", t.TempDir()}, args...)...)
		if exitCode(err) != 2 || !strings.Contains(out, "host-setup:") {
			t.Errorf("%v: exit %d\n%s", args, exitCode(err), out)
		}
	}
}

// dockerStub stands for `docker run --rm --entrypoint tar IMAGE -C /usr/local/share/chaosgw -cf - compose.yaml update.sh host-setup.sh env.example install.sh uninstall.sh`:
// it writes the three files of the image as a tar stream, and records its arguments.
const dockerStub = `#!/bin/sh
echo "$*" >>"$DOCKER_STUB_DIR/calls"
case "$*" in
"run --rm --entrypoint tar "*" -C /usr/local/share/chaosgw -cf - compose.yaml update.sh host-setup.sh env.example install.sh uninstall.sh")
	exec tar -cf - -C "$DOCKER_STUB_DIR/image" compose.yaml update.sh host-setup.sh env.example install.sh uninstall.sh ;;
esac
echo "docker stub: unexpected: $*" >&2
exit 1
`

func runWithDockerStub(t *testing.T, stub string, args ...string) (string, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(bin, "docker"), dockerStub)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(stub, "image")
	writeFile(t, filepath.Join(image, "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(image, "update.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(image, "host-setup.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(image, "install.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(image, "uninstall.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(image, "env.example"), "#CHAOSGW_UI_PORT=8443\n")
	cmd := exec.Command("sh", append([]string{hostSetup}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "DOCKER_STUB_DIR="+stub)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHostSetupCopiesTheFilesOfTheDeploymentOutOfTheImageAndNamesItInEnv(t *testing.T) {
	for _, tt := range []struct{ ref, name, tag string }{
		{"ghcr.io/andste82/chaos-gateway:1.2.3", "ghcr.io/andste82/chaos-gateway", "1.2.3"},
		{"chaos-gateway", "chaos-gateway", "latest"},
		{"localhost:5000/chaos-gateway", "localhost:5000/chaos-gateway", "latest"},
		{"localhost:5000/chaos-gateway:appliance", "localhost:5000/chaos-gateway", "appliance"},
	} {
		t.Run(tt.ref, func(t *testing.T) {
			stub, dir := t.TempDir(), t.TempDir()
			out, err := runWithDockerStub(t, stub, "--prefix", dir, "--skip-docker", "--skip-modprobe", "--image", tt.ref)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			install := filepath.Join(dir, "opt/chaosgw")
			for _, f := range []string{"compose.yaml", "update.sh", "host-setup.sh", "install.sh", "uninstall.sh", "env.example"} {
				if _, err := os.Stat(filepath.Join(install, f)); err != nil {
					t.Errorf("%s was not copied: %v", f, err)
				}
			}
			for _, f := range []string{"update.sh", "host-setup.sh", "install.sh", "uninstall.sh"} {
				if fi, _ := os.Stat(filepath.Join(install, f)); fi == nil || fi.Mode().Perm() != 0o755 {
					t.Errorf("%s is not executable: %v", f, fi)
				}
			}
			env, _ := os.ReadFile(filepath.Join(install, ".env"))
			if want := "CHAOSGW_IMAGE=" + tt.name + "\nCHAOSGW_VERSION=" + tt.tag + "\n"; !strings.HasSuffix(string(env), want) {
				t.Errorf(".env = %q, want it to end with %q", env, want)
			}
			if !strings.Contains(out, "host-setup: next") || !strings.Contains(out, "docker compose up -d") || !strings.Contains(out, "Setup token") {
				t.Errorf("no hint what to do next:\n%s", out)
			}
		})
	}
}

// An .env that exists holds the administrator's settings and stays as it is, whatever image the setup is run with.
func TestHostSetupKeepsAnExistingEnv(t *testing.T) {
	stub, dir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dir, "opt/chaosgw/.env"), "CHAOSGW_UI_PORT=9443\nCHAOSGW_VERSION=1.0.0\n")
	out, err := runWithDockerStub(t, stub, "--prefix", dir, "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway:2.0.0")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if env, _ := os.ReadFile(filepath.Join(dir, "opt/chaosgw/.env")); string(env) != "CHAOSGW_UI_PORT=9443\nCHAOSGW_VERSION=1.0.0\n" {
		t.Errorf(".env = %q", env)
	}
	if !strings.Contains(out, "/opt/chaosgw/.env is kept") {
		t.Errorf("the setup does not say it kept .env:\n%s", out)
	}
	// the files are those of 2.0.0 and .env names 1.0.0: the setup says so and what to do
	if !strings.Contains(out, "warning: chaos-gateway:2.0.0 is version 2.0.0, but /opt/chaosgw/.env names 1.0.0") || !strings.Contains(out, "update.sh --version 2.0.0") {
		t.Errorf("no warning for a version that differs from .env:\n%s", out)
	}
	// the same version, or no version in .env and an image without a tag (latest), is no reason to warn
	same := t.TempDir()
	writeFile(t, filepath.Join(same, "opt/chaosgw/.env"), "CHAOSGW_VERSION=\"2.0.0\"\n")
	if out, err := runWithDockerStub(t, stub, "--prefix", same, "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway:2.0.0"); err != nil || strings.Contains(out, "warning") {
		t.Errorf("the same version: %v\n%s", err, out)
	}
	unset := t.TempDir()
	writeFile(t, filepath.Join(unset, "opt/chaosgw/.env"), "CHAOSGW_UI_PORT=9443\n")
	if out, err := runWithDockerStub(t, stub, "--prefix", unset, "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway"); err != nil || strings.Contains(out, "warning") {
		t.Errorf("latest for latest: %v\n%s", err, out)
	}
	// --install-dir moves the whole deployment
	other := t.TempDir()
	if out, err := runWithDockerStub(t, stub, "--prefix", other, "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway", "--install-dir", "/srv/gw"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(other, "srv/gw/compose.yaml")); err != nil {
		t.Error(err)
	}
}

// Running the setup again with another image replaces compose.yaml, which update.sh would have kept as compose.yaml.bak: so does the setup,
// and only when the content differs.
func TestHostSetupKeepsTheComposeFileItReplaces(t *testing.T) {
	stub, dir := t.TempDir(), t.TempDir()
	install := filepath.Join(dir, "opt/chaosgw")
	writeFile(t, filepath.Join(install, "compose.yaml"), "services: {old: {}}\n")
	out, err := runWithDockerStub(t, stub, "--prefix", dir, "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway:1.0.0")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if bak, _ := os.ReadFile(filepath.Join(install, "compose.yaml.bak")); string(bak) != "services: {old: {}}\n" {
		t.Errorf("compose.yaml.bak = %q: the installed file is kept", bak)
	}
	if got, _ := os.ReadFile(filepath.Join(install, "compose.yaml")); string(got) != "services: {}\n" {
		t.Errorf("compose.yaml = %q", got)
	}
	if !strings.Contains(out, "the old one is kept as /opt/chaosgw/compose.yaml.bak") {
		t.Errorf("the setup does not say it kept the old file:\n%s", out)
	}
	// the same content again: nothing to keep, and the backup of the first replacement stays
	out, err = runWithDockerStub(t, stub, "--prefix", dir, "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway:1.0.0")
	if err != nil || strings.Contains(out, "is kept as") {
		t.Fatalf("%v\n%s", err, out)
	}
	if bak, _ := os.ReadFile(filepath.Join(install, "compose.yaml.bak")); string(bak) != "services: {old: {}}\n" {
		t.Errorf("compose.yaml.bak = %q after a run that replaced nothing", bak)
	}
	// a first installation has nothing to keep
	fresh := t.TempDir()
	if out, err := runWithDockerStub(t, stub, "--prefix", fresh, "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway:1.0.0"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(fresh, "opt/chaosgw/compose.yaml.bak")); err == nil {
		t.Error("a first installation made a backup")
	}
}

func TestHostSetupRefusesWhatIsNoImageReferenceAndAFailingCopy(t *testing.T) {
	for _, ref := range []string{"", "a b", "x;y", "name@sha256:abc", "$(id)"} {
		out, err := runHostSetup(t, "--prefix", t.TempDir(), "--skip-docker", "--skip-modprobe", "--image", ref)
		if exitCode(err) != 2 || !strings.Contains(out, "not an image reference") {
			t.Errorf("%q: exit %d\n%s", ref, exitCode(err), out)
		}
	}
	if out, err := runHostSetup(t, "--prefix", t.TempDir(), "--image"); exitCode(err) != 2 {
		t.Errorf("--image without a value: %d\n%s", exitCode(err), out)
	}
	// a docker that cannot run the image: the copy fails, the setup says so and exits 1, after it did the rest
	bin := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(bin, "docker"), "#!/bin/sh\nexit 125\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", hostSetup, "--prefix", t.TempDir(), "--skip-docker", "--skip-modprobe", "--image", "chaos-gateway")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	b, err := cmd.CombinedOutput()
	if exitCode(err) != 1 || !strings.Contains(string(b), "cannot copy the files of the deployment out of chaos-gateway") {
		t.Errorf("a failing copy: exit %d\n%s", exitCode(err), b)
	}
}
