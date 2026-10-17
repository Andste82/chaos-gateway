package preflight

import (
	"errors"
	"fmt"
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

// fakeInterface adds an interface to the sysfs below root: its MAC address, its driver (a link to the
// driver's directory, as the kernel has it) and whether it has a carrier.
func fakeInterface(t *testing.T, root, name, mac, driver string, carrier bool) {
	t.Helper()
	dir := filepath.Join(root, "sys/class/net", name)
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if mac != "" {
		writeFile(t, filepath.Join(dir, "address"), mac+"\n")
	}
	c := "0\n"
	if carrier {
		c = "1\n"
	}
	writeFile(t, filepath.Join(dir, "carrier"), c)
	if driver != "" {
		target := filepath.Join(root, "sys/bus/drivers", driver)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "device/driver")); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exitCode is the exit status of a run of the script (0 without an error, -1 when it did not run).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// treeOf lists the files below root with their content.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			raw, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			out[rel] = string(raw)
		}
		return nil
	})
	return out
}

// Issue #64 items 1 and 2: before the wizard, `host-setup.sh --check IF` says per test interface what is
// still missing (the netplan entry with its file and line, the systemd-networkd file that configures it
// with the fix, the link) and exits 1 until nothing is; it changes nothing. `--test-interface IF` writes the
// rule that keeps systemd-networkd off the interface and checks it; `--remove-test-interface IF` removes the
// rule again, and only the host setup's own.
func TestHostSetupChecksATestInterfaceAndKeepsNetworkdOffIt(t *testing.T) {
	root := t.TempDir()
	fakeInterface(t, root, "eth0", "2c:cf:67:00:00:01", "macb", true)
	fakeInterface(t, root, "wlan0", "2c:cf:67:00:00:02", "brcmfmac", true)
	netplan := "network:\n  version: 2\n  ethernets:\n    eth0: {optional: true, dhcp4: false, dhcp6: false}\n  # eth0 was the lab port\n  wifis:\n    wlan0: {dhcp4: true}\n"
	writeFile(t, filepath.Join(root, "etc/netplan/50-cloud-init.yaml"), netplan)
	writeFile(t, filepath.Join(root, "run/systemd/network/zzzz-dracut-default.network"), "[Match]\nKind=!*\nType=!loopback\n\n[Network]\nDHCP=yes\n")
	writeFile(t, filepath.Join(root, "run/systemd/network/10-netplan-wlan0.network"), "[Match]\nName=wlan0\n\n[Network]\nDHCP=ipv4\n")
	writeFile(t, filepath.Join(root, "usr/lib/systemd/network/80-container-ve.network"), "[Match]\nName=ve-*\nDriver=veth\n")
	before := treeOf(t, root)

	out, err := runHostSetup(t, "--prefix", root, "--check", "eth0")
	if exitCode(err) != 1 {
		t.Fatalf("check with the netplan entry and the networkd default: %v\n%s", err, out)
	}
	for _, want := range []string{
		"ok    link: eth0 is up and has a carrier (MAC 2c:cf:67:00:00:01, driver macb)",
		"FAIL  netplan: /etc/netplan/50-cloud-init.yaml names eth0 (line 4)",
		`fix: delete the entry of eth0 from /etc/netplan/50-cloud-init.yaml (an entry that only turns DHCP off still hands the port to the operating system), then run "sudo netplan apply"`,
		"ok    NetworkManager: no connection is bound to eth0",
		"FAIL  systemd-networkd: /run/systemd/network/zzzz-dracut-default.network configures eth0",
		"fix: sudo sh host-setup.sh --test-interface eth0 (writes /etc/systemd/network/10-chaosgw-eth0.network with [Link] Unmanaged=yes and reloads systemd-networkd)",
		"2 step(s) missing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if fmt.Sprint(treeOf(t, root)) != fmt.Sprint(before) {
		t.Error("the check changed a file")
	}

	// the rule: written once, then up to date; the netplan entry is still missing
	out, err = runHostSetup(t, "--prefix", root, "--skip-docker", "--skip-modprobe", "--test-interface", "eth0")
	rule := filepath.Join(root, "etc/systemd/network/10-chaosgw-eth0.network")
	raw, _ := os.ReadFile(rule)
	if exitCode(err) != 1 || !strings.Contains(out, "wrote "+rule) ||
		!strings.Contains(out, "ok    systemd-networkd: /etc/systemd/network/10-chaosgw-eth0.network leaves eth0 alone") ||
		!strings.Contains(out, "1 step(s) missing") {
		t.Errorf("--test-interface: %v\n%s", err, out)
	}
	if !strings.HasSuffix(string(raw), "[Match]\nName=eth0\n\n[Link]\nUnmanaged=yes\n") || !strings.HasPrefix(string(raw), "# Written by the Chaos Gateway host setup") {
		t.Errorf("the rule:\n%s", raw)
	}
	out, _ = runHostSetup(t, "--prefix", root, "--skip-docker", "--skip-modprobe", "--test-interface", "eth0")
	if !strings.Contains(out, rule+" is up to date") {
		t.Errorf("second run:\n%s", out)
	}

	// without the netplan entry everything is there; no carrier is a warning, not a missing step
	writeFile(t, filepath.Join(root, "etc/netplan/50-cloud-init.yaml"), strings.Replace(netplan, "    eth0: {optional: true, dhcp4: false, dhcp6: false}\n", "", 1))
	writeFile(t, filepath.Join(root, "sys/class/net/eth0/carrier"), "0\n")
	out, err = runHostSetup(t, "--prefix", root, "--check", "eth0")
	if err != nil || !strings.Contains(out, "ok    netplan: no file names eth0") || !strings.Contains(out, "warn  link: eth0 has no carrier") ||
		!strings.Contains(out, "the test interfaces are ready: eth0") {
		t.Errorf("check after the steps: %v\n%s", err, out)
	}

	// the undo removes the rule, and the check fails again
	out, err = runHostSetup(t, "--prefix", root, "--remove-test-interface", "eth0")
	if _, statErr := os.Stat(rule); err != nil || !os.IsNotExist(statErr) || !strings.Contains(out, "removed "+rule) {
		t.Errorf("--remove-test-interface: %v %v\n%s", err, statErr, out)
	}
	if _, err := runHostSetup(t, "--prefix", root, "--check", "eth0"); exitCode(err) != 1 {
		t.Error("the check passes without the rule")
	}
	// a file of that name the host setup did not write stays
	writeFile(t, rule, "[Match]\nName=eth0\n\n[Link]\nUnmanaged=yes\n")
	if out, err := runHostSetup(t, "--prefix", root, "--remove-test-interface", "eth0"); exitCode(err) != 1 || !strings.Contains(out, "was not written by the host setup") {
		t.Errorf("a foreign file: %v\n%s", err, out)
	}
	if _, err := os.Stat(rule); err != nil {
		t.Error("a foreign file was removed")
	}

	// a file that comes before the rule must be changed itself; a NetworkManager connection is named; an
	// interface the host does not have fails
	writeFile(t, filepath.Join(root, "etc/systemd/network/05-lab.network"), "[Match]\nMACAddress=2C:CF:67:00:00:01\n\n[Network]\nDHCP=yes\n")
	writeFile(t, filepath.Join(root, "etc/NetworkManager/system-connections/Wired connection 1.nmconnection"), "[connection]\nid=Wired connection 1\ntype=ethernet\ninterface-name=eth0\n")
	out, err = runHostSetup(t, "--prefix", root, "--check", "eth0", "--check", "eth9")
	for _, want := range []string{
		"FAIL  systemd-networkd: /etc/systemd/network/05-lab.network configures eth0",
		`fix: change /etc/systemd/network/05-lab.network so that its [Match] section does not select eth0, or delete it, then run "sudo networkctl reload"`,
		"FAIL  NetworkManager: the connection /etc/NetworkManager/system-connections/Wired connection 1.nmconnection is bound to eth0",
		"host-setup: check of the test interface eth9",
		"FAIL  link: eth9 is not an interface of this host",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if exitCode(err) != 1 {
		t.Errorf("exit: %v", err)
	}
}

// The new options refuse what is no interface name and do not mix the check, the undo and the setup.
func TestHostSetupTestInterfaceOptions(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"--check"},
		{"--check", "../etc"},
		{"--check", "eth0 eth1"},
		{"--test-interface", "a-name-that-is-too-long"},
		{"--remove-test-interface", ""},
		{"--check", "eth0", "--remove-test-interface", "eth0"},
		{"--check", "eth0", "--test-interface", "eth0"},
		{"--remove-test-interface", "eth0", "--test-interface", "eth1"},
	} {
		if out, err := runHostSetup(t, append([]string{"--prefix", root}, args...)...); exitCode(err) != 2 {
			t.Errorf("%v: %v\n%s", args, err, out)
		}
	}
	if len(treeOf(t, root)) != 0 {
		t.Error("a refused run wrote a file")
	}
	if out, _ := runHostSetup(t, "--help"); !strings.Contains(out, "--test-interface IF") || !strings.Contains(out, "--check IF") || !strings.Contains(out, "--remove-test-interface IF") {
		t.Errorf("help:\n%s", out)
	}
}

// The host setup's check and the setup wizard's preflight evaluate a systemd-networkd [Match] section alike
// (networkdMatchCases), and both ignore a file without a condition.
func TestHostSetupEvaluatesNetworkdLikeThePreflight(t *testing.T) {
	for _, c := range networkdMatchCases {
		root := t.TempDir()
		fakeInterface(t, root, c.name, c.mac, c.driver, true)
		writeFile(t, filepath.Join(root, "etc/systemd/network/a.network"), "[Match]\n"+c.match+"\n\n[Network]\nDHCP=yes\n")
		out, _ := runHostSetup(t, "--prefix", root, "--check", c.name)
		if got := strings.Contains(out, "FAIL  systemd-networkd: /etc/systemd/network/a.network configures "+c.name); got != c.want {
			t.Errorf("%q selects %s (%s, %s): %v, want %v\n%s", c.match, c.name, c.mac, c.driver, got, c.want, out)
		}
	}
	for _, raw := range networkdIgnored {
		root := t.TempDir()
		fakeInterface(t, root, "eth0", "", "", true)
		writeFile(t, filepath.Join(root, "etc/systemd/network/a.network"), raw)
		if out, _ := runHostSetup(t, "--prefix", root, "--check", "eth0"); !strings.Contains(out, "ok    systemd-networkd: no .network file selects eth0") {
			t.Errorf("%q:\n%s", raw, out)
		}
	}
}
