package vmrun

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
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
		// the fixture's TestSkips skips on purpose; most tests here are not about that, so they
		// allow it and TestCollectSkipsFailUnlessAllowed below tests the default separately.
		AllowSkip: true,
	}, &out
}

// M1-08 test: a skip fails the run unless explicitly allowed, the same way a FAIL does.
func TestCollectSkipsFailUnlessAllowed(t *testing.T) {
	fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
	c, out := fixtureConfig(t)
	c.Run = "TestSkips"
	c.AllowSkip = false
	s, err := Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if s.OK() {
		t.Fatal("a skip must fail the run by default")
	}
	if probs := strings.Join(s.Problems(), "\n"); !strings.Contains(probs, "1 tests skipped") {
		t.Fatalf("problems = %q", probs)
	}
	c.AllowSkip = true
	s, err = Run(context.Background(), c)
	if err != nil || !s.OK() {
		t.Fatalf("AllowSkip must let the same run pass: %v %v\n%s", err, s.Problems(), out)
	}
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

func TestRunRemovesAWorkDirectoryItCreatedAfterSuccess(t *testing.T) {
	fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	c, _ := fixtureConfig(t)
	c.WorkDir = "" // let Run create it
	if s, err := Run(context.Background(), c); err != nil || !s.OK() {
		t.Fatalf("run: %v %v", err, s.Problems())
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("the work directory was not removed: %v", left)
	}
}

func TestRunKeepsACreatedWorkDirectoryAfterAFailureAndSaysWhere(t *testing.T) {
	fakeVNG(t, "echo boom >&2\nexit 1\n")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	c, out := fixtureConfig(t)
	c.WorkDir = ""
	if s, err := Run(context.Background(), c); err != nil || s.OK() {
		t.Fatalf("run: %v ok=%v", err, s.OK())
	}
	left, _ := os.ReadDir(tmp)
	if len(left) != 1 || !strings.Contains(out.String(), "kept for inspection: "+filepath.Join(tmp, left[0].Name())) {
		t.Fatalf("left %v, output:\n%s", left, out)
	}
}

func TestRunNeverRemovesADirectoryTheCallerNamedButClearsStaleResults(t *testing.T) {
	fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
	c, _ := fixtureConfig(t)
	if err := os.MkdirAll(filepath.Join(c.WorkDir, "results"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a finished earlier run: it must not make a dead VM look like a pass
	stale := map[string]string{"done": "done\n", "x.exit": "0\n"}
	for name, body := range stale {
		if err := os.WriteFile(filepath.Join(c.WorkDir, "results", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	precious := filepath.Join(c.WorkDir, "keep.txt")
	if err := os.WriteFile(precious, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeVNG(t, "exit 1\n") // the new VM dies at boot
	s, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if s.OK() || s.Finished {
		t.Fatalf("stale results made a dead VM look like a pass: %v", s.Problems())
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatal("a directory the caller named must never be removed")
	}
	if _, err := os.Stat(filepath.Join(c.WorkDir, "results", "x.exit")); !os.IsNotExist(err) {
		t.Fatal("stale results were not cleared")
	}
}

func alive(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	stat := string(raw)
	rest := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
	return len(rest) > 0 && rest[0] != "Z" // a zombie is dead, only not yet reaped
}

// A VM that does not finish is killed with everything it started, and the run says so.
func TestRunTimeoutKillsTheWholeProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	fakeVNG(t, "sleep 300 &\necho $! > '"+pidFile+"'\nwait\n")
	c, _ := fixtureConfig(t)
	c.VMTimeout = 2 * time.Second
	start := time.Now()
	s, err := Run(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(start); took > 60*time.Second {
		t.Fatalf("the timeout took %v", took)
	}
	if s.OK() || len(s.Packages) == 0 {
		t.Fatalf("a timed-out run must return its (failed) summary: %+v", s)
	}
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("process %d of the VM survived the timeout", pid)
	}
}

func TestRunCancelKillsTheVMToo(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	fakeVNG(t, "sleep 300 &\necho $! > '"+pidFile+"'\nwait\n")
	c, _ := fixtureConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 200; i++ { // wait until the fake VM runs
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
	}()
	_, err := Run(ctx, c)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
	raw, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("process %d survived the cancel", pid)
	}
}

func TestDescendantsFindsAllLevels(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & sleep 30 & wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { killTree(cmd.Process.Pid); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	var got []int
	for time.Now().Before(deadline) {
		if got = descendants(cmd.Process.Pid); len(got) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) < 2 {
		t.Fatalf("descendants = %v, want the two sleeps", got)
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

// M1-03 test: -no-kvm (Config.NoKVM) forces software emulation regardless of whether this
// machine actually has /dev/kvm, both in the vng command line and in the guest's own flag.
func TestNoKVMForcesEmulationAndDisablesKVMInTheVNGCommandLine(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "vng-args")
	// capture the full command line before fakeVNG's own wrapper shifts it away, then run $cmd
	// (set by that wrapper) as usual so the guest still produces results.
	vngDir := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n" +
		"while [ $# -gt 0 ]; do\n  if [ \"$1\" = --exec ]; then cmd=\"$2\"; fi\n  shift\ndone\n" +
		"sh -c \"$cmd\"\nexit $?\n"
	if err := os.WriteFile(filepath.Join(vngDir, "vng"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", vngDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VMRUN_FIXTURE_WANT_EMULATED", "1")
	c, out := fixtureConfig(t)
	c.NoKVM = true
	s, err := Run(context.Background(), c)
	if err != nil || !s.OK() {
		t.Fatalf("emulation flag not seen by the guest: %v %v\n%s", err, s.Problems(), out)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--disable-kvm") {
		t.Fatalf("vng command line lacks --disable-kvm: %s", args)
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
