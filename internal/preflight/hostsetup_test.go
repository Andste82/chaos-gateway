package preflight

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const hostSetup = "../../deploy/host-setup.sh"

func runHostSetup(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("sh", append([]string{hostSetup}, args...)...).CombinedOutput()
	return string(out), err
}

func scriptModules(t *testing.T, variable string) []string {
	t.Helper()
	raw, err := os.ReadFile(hostSetup)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^` + variable + `="([^"]*)"`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("no %s in the script", variable)
	}
	return strings.Fields(string(m[1]))
}

func TestHostSetupLoadsExactlyTheModulesOfThePreflight(t *testing.T) {
	var req, later []string
	for _, m := range Modules() {
		if m.Later {
			later = append(later, m.Name)
		} else {
			req = append(req, m.Name)
		}
	}
	sort.Strings(req)
	got := scriptModules(t, "REQUIRED_MODULES")
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(req, " ") {
		t.Errorf("the script loads\n  %v\nthe preflight requires\n  %v", got, req)
	}
	if g := scriptModules(t, "LATER_MODULES"); strings.Join(g, " ") != strings.Join(later, " ") {
		t.Errorf("later modules: script %v, preflight %v", g, later)
	}
}

func TestHostSetupWritesItsFilesAndIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if out, err := exec.Command("sh", "-n", hostSetup).CombinedOutput(); err != nil {
		t.Fatalf("syntax: %v\n%s", err, out)
	}
	dir := t.TempDir()
	out, err := runHostSetup(t, "--prefix", dir, "--skip-docker", "--skip-modprobe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mods, _ := os.ReadFile(filepath.Join(dir, "etc/modules-load.d/chaos-gateway.conf"))
	for _, m := range Modules() {
		if !strings.Contains(string(mods), "\n"+m.Name+"\n") {
			t.Errorf("%s is not in modules-load.d:\n%s", m.Name, mods)
		}
	}
	sysctl, _ := os.ReadFile(filepath.Join(dir, "etc/sysctl.d/90-chaos-gateway.conf"))
	if !strings.Contains(string(sysctl), "net.ipv4.ip_forward=1\n") {
		t.Errorf("%s", sysctl)
	}
	// netplan is only mentioned, never written
	if !strings.Contains(out, "netplan") {
		t.Errorf("no netplan hint:\n%s", out)
	}
	var files []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	if strings.Join(files, ",") != "etc/modules-load.d/chaos-gateway.conf,etc/sysctl.d/90-chaos-gateway.conf" {
		t.Errorf("files written: %v", files)
	}
	// the second run changes nothing and says so
	before, _ := os.Stat(filepath.Join(dir, "etc/sysctl.d/90-chaos-gateway.conf"))
	out2, err := runHostSetup(t, "--prefix", dir, "--skip-docker", "--skip-modprobe")
	if err != nil || strings.Count(out2, "is up to date") != 2 {
		t.Errorf("%v\n%s", err, out2)
	}
	after, _ := os.Stat(filepath.Join(dir, "etc/sysctl.d/90-chaos-gateway.conf"))
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged file was rewritten")
	}
}

func TestHostSetupOptions(t *testing.T) {
	if out, err := runHostSetup(t, "--help"); err != nil || !strings.Contains(out, "--prefix") {
		t.Errorf("%v\n%s", err, out)
	}
	if _, err := runHostSetup(t, "--bogus"); err == nil {
		t.Error("an unknown option is accepted")
	}
	if _, err := runHostSetup(t, "--prefix"); err == nil {
		t.Error("--prefix without a directory")
	}
	// without --prefix it insists on root; as root the test would change the machine, so only the
	// refusal is tested
	if os.Getuid() != 0 {
		if out, err := runHostSetup(t, "--skip-docker", "--skip-modprobe"); err == nil || !strings.Contains(out, "as root") {
			t.Errorf("%v\n%s", err, out)
		}
	}
}
