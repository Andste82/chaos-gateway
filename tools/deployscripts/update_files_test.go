package deployscripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A signal ends the update (the administrator cancelled it); the handler of a signal that does not exit returns to the command that
// was interrupted, and the script would go on to the restart for which it was waiting.
func TestASignalDuringTheWaitEndsTheUpdateAndNothingIsRestarted(t *testing.T) {
	for sig, want := range map[syscall.Signal]int{syscall.SIGTERM: 143, syscall.SIGINT: 130} {
		d := newDeployment(t)
		d.set("busy", "200 {\"busy\":true,\"runs_active\":1}")
		// --force goes on after 3 s: a script that survives the signal restarts the containers then
		cmd := exec.Command("sh", updateScript, "--dir", d.dir, "--timeout", "3", "--force")
		cmd.Env = append(os.Environ(), "PATH="+d.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB="+d.stub, "CHAOSGW_UPDATE_POLL=1",
			"CHAOSGW_STATE_DIR="+d.stub, "CHAOSGW_TOKEN=", "CHAOSGW_VERSION=")
		var out strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(filepath.Join(d.stub, "busy.n")); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if _, err := os.Stat(filepath.Join(d.stub, "busy.n")); err != nil {
			_ = cmd.Process.Kill()
			t.Fatalf("the script never asked about the runs:\n%s", out.String())
		}
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		err := cmd.Wait()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != want {
			t.Errorf("%v: %v, want exit %d\n%s", sig, err, want, out.String())
		}
		if d.index("compose up") >= 0 {
			t.Errorf("%v: the containers were restarted after the update was cancelled:\n%s", sig, strings.Join(d.calls(), "\n"))
		}
		if d.index("compose.yaml.bak") >= 0 {
			t.Errorf("%v: files were replaced after the update was cancelled", sig)
		}
	}
}

// The flags, mounts and health checks of the containers belong to the version: the compose.yaml of the new image replaces the installed
// one just before the restart, and the old one is kept.
func TestTheComposeFileOfTheNewImageReplacesTheInstalledOneBeforeTheRestart(t *testing.T) {
	d := newDeployment(t)
	d.write(filepath.Join(d.stub, "image", "compose.yaml"), "services: {exec: {}}\n# new\n")
	d.write(filepath.Join(d.stub, "image", "env.example"), "#CHAOSGW_NEW=1\n")
	d.write(filepath.Join(d.dir, "env.example"), "#CHAOSGW_OLD=1\n")
	d.write(filepath.Join(d.dir, "update.sh"), "#!/bin/sh\n# old\n")
	d.write(filepath.Join(d.dir, ".env"), "CHAOSGW_VERSION=1.0.0\nCHAOSGW_UI_PORT=9443\n")
	out, code := d.run()
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(d.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if got := read("compose.yaml"); got != "services: {exec: {}}\n# new\n" {
		t.Errorf("compose.yaml = %q", got)
	}
	if got := read("compose.yaml.bak"); got != "services: {}\n" {
		t.Errorf("compose.yaml.bak = %q: the old file is kept", got)
	}
	if got := read("env.example"); got != "#CHAOSGW_NEW=1\n" {
		t.Errorf("env.example = %q", got)
	}
	if got := read("update.sh"); !strings.Contains(got, "of the image") {
		t.Errorf("update.sh = %q", got)
	}
	if fi, err := os.Stat(filepath.Join(d.dir, "update.sh")); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("update.sh is not executable: %v", err)
	}
	if got := read(".env"); got != "CHAOSGW_VERSION=1.0.0\nCHAOSGW_UI_PORT=9443\n" {
		t.Errorf(".env = %q: the settings of the administrator stay", got)
	}
	// the file was checked with this installation's settings before it replaced the old one, and it was in place for the restart
	if d.index("compose -f ") < 0 || !strings.Contains(strings.Join(d.calls(), "\n"), "--project-directory "+d.dir+" config -q") {
		t.Errorf("the file of the image was not checked in the directory of the installation:\n%s", strings.Join(d.calls(), "\n"))
	}
	if !strings.Contains(out, "replaces the installed one") {
		t.Errorf("the output does not say that compose.yaml was replaced:\n%s", out)
	}
}

func TestAnUnchangedComposeFileIsNotReplacedAndAnUpdateScriptIsOnlyReplacedWhereOneIsInstalled(t *testing.T) {
	d := newDeployment(t)
	if out, code := d.run(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(d.dir, "compose.yaml.bak")); err == nil {
		t.Error("an unchanged compose.yaml was backed up and replaced")
	}
	if _, err := os.Stat(filepath.Join(d.dir, "update.sh")); err == nil {
		t.Error("update.sh was installed where there is none")
	}
}

// Nothing is replaced and nothing stops when the new image's files cannot be had or do not work with this installation, or when the
// update waits for a run and gives up.
func TestTheFilesOfTheNewImageAreCheckedBeforeAnythingChanges(t *testing.T) {
	d := newDeployment(t)
	d.write(filepath.Join(d.stub, "image", "compose.yaml"), "services: {exec: {}}\n")
	d.set("config", "1")
	out, code := d.run()
	if code != 1 || !strings.Contains(out, "is not valid") || !strings.Contains(out, "keeps running as it is") || d.index("compose up") >= 0 {
		t.Errorf("a compose.yaml that is not valid: exit %d\n%s", code, out)
	}
	if raw, _ := os.ReadFile(filepath.Join(d.dir, "compose.yaml")); string(raw) != "services: {}\n" {
		t.Errorf("compose.yaml = %q after the refusal", raw)
	}

	d = newDeployment(t)
	if err := os.RemoveAll(filepath.Join(d.stub, "image")); err != nil {
		t.Fatal(err)
	}
	out, code = d.run()
	if code != 1 || !strings.Contains(out, "cannot read compose.yaml out of chaos-gateway:latest") || d.index("compose up") >= 0 {
		t.Errorf("an image without the files: exit %d\n%s", code, out)
	}

	// the update that gives up waiting for a run changes nothing, the files included
	d = newDeployment(t)
	d.write(filepath.Join(d.stub, "image", "compose.yaml"), "services: {exec: {}}\n")
	d.set("busy", "200 {\"busy\":true,\"runs_active\":1}")
	if _, code := d.run("--timeout", "1"); code != 3 {
		t.Fatalf("exit %d", code)
	}
	if raw, _ := os.ReadFile(filepath.Join(d.dir, "compose.yaml")); string(raw) != "services: {}\n" {
		t.Errorf("compose.yaml = %q after an update that waited and gave up", raw)
	}
	// --check validates the files and replaces none
	d = newDeployment(t)
	d.write(filepath.Join(d.stub, "image", "compose.yaml"), "services: {exec: {}}\n")
	if out, code := d.run("--check"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if raw, _ := os.ReadFile(filepath.Join(d.dir, "compose.yaml")); string(raw) != "services: {}\n" {
		t.Errorf("--check replaced compose.yaml: %q", raw)
	}
}

// dns and tls report healthy only once the executor has attached the service namespace, which takes a configuration: an update of a
// gateway that is not set up yet does not wait for them, one that is set up does.
func TestAnUpdateOfAGatewayThatIsNotSetUpDoesNotWaitForTheServicesThatNeedAConfiguration(t *testing.T) {
	d := newDeployment(t)
	d.set("setup", "200 {\"completed\":false}")
	d.set("up.all", "1") // docker compose up --wait for every container would run into its time-out
	out, code := d.run()
	if code != 0 || !strings.Contains(out, "not set up yet") {
		t.Fatalf("an unconfigured gateway: exit %d\n%s", code, out)
	}
	if d.index("compose up -d --wait --wait-timeout 180 exec api svcns kea bird") < 0 || d.exact("compose up -d --wait --wait-timeout 180") >= 0 {
		t.Errorf("the five containers are waited for, the other two are not:\n%s", strings.Join(d.calls(), "\n"))
	}

	d = newDeployment(t)
	d.set("up.all", "1")
	if out, code := d.run(); code != 5 || !strings.Contains(out, "did not become healthy within 180s") {
		t.Errorf("a gateway that is set up and whose DNS proxy does not come up: exit %d\n%s", code, out)
	}
}
