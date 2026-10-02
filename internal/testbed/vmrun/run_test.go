package vmrun

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVNGArgs(t *testing.T) {
	c := Config{Kernel: "6.8.0-142-generic", Memory: "2G", CPUs: 2, WorkDir: "/work dir"}
	got := VNGArgs(c, false)
	want := []string{"-r", "6.8.0-142-generic", "--disable-kvm", "--memory", "2G", "--cpus", "2",
		"--rwdir=/work dir=/work dir", "--exec", "sh '/work dir/guest.sh'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q", got)
	}
	for _, a := range VNGArgs(c, true) {
		if a == "--disable-kvm" {
			t.Fatal("--disable-kvm with KVM available")
		}
	}
	// the guest kernel command line is short: --exec must only name the script
	if exec := got[len(got)-1]; len(exec) > 200 {
		t.Fatalf("--exec argument is %d bytes", len(exec))
	}
}

func TestWithPTY(t *testing.T) {
	cmd := []string{"vng", "-r", "6.8", "--exec", "sh 'a b'"}
	if got := WithPTY(cmd, true); !reflect.DeepEqual(got, cmd) {
		t.Fatalf("with a terminal the command must stay: %q", got)
	}
	got := WithPTY(cmd, false)
	if got[0] != "script" || got[1] != "-qec" || got[3] != "/dev/null" {
		t.Fatalf("script wrapper = %q", got)
	}
	if want := `'vng' '-r' '6.8' '--exec' 'sh '\''a b'\'''`; got[2] != want {
		t.Fatalf("quoted command = %s, want %s", got[2], want)
	}
}

func TestDefaults(t *testing.T) {
	var c Config
	c.defaults()
	if c.Kernel != DefaultKernel || c.Memory != "2G" || c.CPUs != 2 || c.Tags[0] != "testbed" ||
		c.Packages[0] != "./..." || c.TestTimeout <= 0 || c.VMTimeout <= 0 {
		t.Fatalf("defaults = %+v", c)
	}
}

// fakeVNG installs a stand-in for vng that runs the --exec command on the host, like the guest
// would. It lets the whole runner (build, script, results) be tested in seconds without a VM.
func fakeVNG(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = --exec ]; then cmd=\"$2\"; fi\n  shift\ndone\n" + body
	if err := os.WriteFile(filepath.Join(dir, "vng"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func fixtureConfig(t *testing.T) (Config, *bytes.Buffer) {
	t.Helper()
	// `make test-arm64` runs this test binary under qemu-user with GOARCH=arm64; the fixture's
	// test binary is built and run natively, so the nested go commands must not inherit that
	t.Setenv("GOARCH", "")
	// the runner only checks that the kernel image exists; the fake vng ignores the kernel
	kernel := ""
	if m, _ := filepath.Glob("/boot/vmlinuz-*"); len(m) > 0 {
		kernel = strings.TrimPrefix(filepath.Base(m[0]), "vmlinuz-")
	}
	if kernel == "" {
		t.Skip("no installed kernel image: Run() checks for one")
	}
	var out bytes.Buffer
	return Config{
		Dir: ".", Kernel: kernel, Packages: []string{"./testdata/fixture"},
		WorkDir: filepath.Join(t.TempDir(), "work"), Stdout: &out, Stderr: &out,
		TestTimeout: time.Minute, VMTimeout: time.Minute,
	}, &out
}

func TestRunWithFakeVMAllPass(t *testing.T) {
	fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
	c, out := fixtureConfig(t)
	c.Keep = true
	s, err := Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if !s.OK() {
		t.Fatalf("expected success: %v\n%s", s.Problems(), out)
	}
	if got := s.Totals(); got.Failed != 0 || got.Passed < 4 || got.Skipped != 1 {
		t.Fatalf("totals = %+v\n%s", got, s.Packages[0].Output)
	}
	if !strings.Contains(s.Packages[0].Output, "TestWorkingDirectoryIsThePackageDirectory") {
		t.Fatalf("output lacks the test names:\n%s", s.Packages[0].Output)
	}
	// the work directory is kept on request and holds the guest script and the results
	for _, f := range []string{"guest.sh", "results/done", "bin"} {
		if _, err := os.Stat(filepath.Join(c.WorkDir, f)); err != nil {
			t.Errorf("work directory lacks %s", f)
		}
	}
}

func TestRunRemovesTheWorkDirectoryUnlessKept(t *testing.T) {
	fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
	c, _ := fixtureConfig(t)
	if _, err := Run(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("work directory was not removed: %v", err)
	}
}

func TestRunWithFakeVMFailingTestFailsTheRun(t *testing.T) {
	fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
	t.Setenv("VMRUN_FIXTURE_FAIL", "1")
	c, out := fixtureConfig(t)
	s, err := Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if s.OK() {
		t.Fatal("a failing test must fail the run")
	}
	if got := s.Totals().Failed; got != 1 {
		t.Fatalf("failed = %d, want 1", got)
	}
	if !strings.Contains(s.Packages[0].Output, "failing as requested") {
		t.Fatalf("the failure message must be in the output:\n%s", s.Packages[0].Output)
	}
}

func TestRunWithFakeVMThatDiesEarlyFailsTheRun(t *testing.T) {
	fakeVNG(t, "echo 'kernel panic' >&2\nexit 1\n")
	c, _ := fixtureConfig(t)
	s, err := Run(context.Background(), c)
	if err != nil {
		t.Fatalf("a dying VM is a failed run, not a runner error: %v", err)
	}
	if s.OK() || s.Finished {
		t.Fatal("a VM without results must fail the run")
	}
	if !strings.Contains(strings.Join(s.Problems(), "\n"), "ended before the tests finished") {
		t.Fatalf("problems = %v", s.Problems())
	}
}

func TestRunPassesTheEmulationFlagWithoutKVM(t *testing.T) {
	if HasKVM() {
		t.Skip("this machine has KVM: the guest is not emulated")
	}
	fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
	t.Setenv("VMRUN_FIXTURE_WANT_EMULATED", "1")
	c, out := fixtureConfig(t)
	s, err := Run(context.Background(), c)
	if err != nil || !s.OK() {
		t.Fatalf("emulation flag not seen by the guest: %v %v\n%s", err, s.Problems(), out)
	}
}

func TestRunFailsClearlyWithoutVNG(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Run(context.Background(), Config{Kernel: "6.8.0-142-generic"})
	if err == nil || !strings.Contains(err.Error(), "vng") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunFailsClearlyForAMissingKernel(t *testing.T) {
	fakeVNG(t, "exit 0\n")
	_, err := Run(context.Background(), Config{Kernel: "0.0.0-does-not-exist"})
	if err == nil || !strings.Contains(err.Error(), "0.0.0-does-not-exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunWithNoMatchingPackageIsAnError(t *testing.T) {
	fakeVNG(t, "exit 0\n")
	c, _ := fixtureConfig(t)
	c.Tags = []string{"nonexistenttag"} // the fixture's tests need the testbed tag
	if _, err := Run(context.Background(), c); err == nil || !strings.Contains(err.Error(), "no package with tests") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsTerminalIsFalseForAFile(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if IsTerminal(f) {
		t.Fatal("/dev/null is not a terminal")
	}
}

func TestTestbedPackagesListsOnlyPackagesWithTaggedTests(t *testing.T) {
	// this package's own tests are untagged; the fixture's need the testbed tag
	got, err := TestbedPackages(context.Background(), ".", []string{"testbed"}, []string{"./...", "./testdata/fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, u := range got {
		paths = append(paths, u.ImportPath)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/vmrun/testdata/fixture") {
		t.Fatalf("packages = %v, want only the fixture", paths)
	}
}
