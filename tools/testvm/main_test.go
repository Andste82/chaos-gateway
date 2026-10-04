package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed/vmrun"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

const fixture = "./internal/testbed/vmrun/testdata/fixture"

func TestUsageAndUnknownCommands(t *testing.T) {
	if code, _, errOut := runCLI(t); code != 2 || !strings.Contains(errOut, "usage: testvm") {
		t.Errorf("no arguments: code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "frobnicate"); code != 2 || !strings.Contains(errOut, `unknown command "frobnicate"`) {
		t.Errorf("unknown command: code %d, stderr %q", code, errOut)
	}
}

func TestPreflightReportsTheKernelAndTheVerdict(t *testing.T) {
	code, out, _ := runCLI(t, "preflight")
	if !strings.HasPrefix(out, "kernel ") || !strings.Contains(out, "module sch_netem") {
		t.Fatalf("output:\n%s", out)
	}
	ok := strings.Contains(out, "ok: the namespace testbed can run here")
	if ok != (code == 0) || (!ok && code != 1) {
		t.Fatalf("exit code %d does not match the verdict:\n%s", code, out)
	}
}

func TestRunRejectsAnUnknownMode(t *testing.T) {
	if code, _, errOut := runCLI(t, "run", "-mode", "bogus"); code != 2 || !strings.Contains(errOut, `unknown mode "bogus"`) {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestRunDirectRunsThePackagesWithTaggedTests(t *testing.T) {
	t.Setenv("GOARCH", "") // see vmrun's fixtureConfig: nested go commands build natively
	t.Chdir("../..")
	code, out, errOut := runCLI(t, "run", "-mode", "direct", "-allow-skip", fixture)
	if code != 0 {
		t.Fatalf("code %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "TestWorkingDirectoryIsThePackageDirectory") {
		t.Fatalf("the tagged tests did not run:\n%s", out)
	}
}

func TestRunDirectPassesTheFailureOn(t *testing.T) {
	t.Setenv("GOARCH", "")
	t.Chdir("../..")
	t.Setenv("VMRUN_FIXTURE_FAIL", "1")
	if code, _, _ := runCLI(t, "run", "-mode", "direct", fixture); code == 0 {
		t.Fatal("a failing test must give a non-zero exit code")
	}
}

func TestRunDirectWithoutMatchingPackagesIsAnError(t *testing.T) {
	t.Chdir("../..")
	code, _, errOut := runCLI(t, "run", "-mode", "direct", "-tags", "nonexistenttag", fixture)
	if code != 2 || !strings.Contains(errOut, "no package with tests") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

// fakeVNG installs a stand-in for vng that runs its given body instead of booting a real VM,
// mirroring vmrun's own test helper of the same purpose (unexported there, so duplicated here).
func fakeVNG(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = --exec ]; then cmd=\"$2\"; fi\n  shift\ndone\n" + body
	if err := os.WriteFile(filepath.Join(dir, "vng"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// M1-06 test: the runner's exit code in VM mode is 0 only when every package ran and passed, 1
// when a test or the VM failed, and 2 when the run could not be carried out at all.
func TestRunVMExitCodes(t *testing.T) {
	t.Setenv("GOARCH", "") // see vmrun's fixtureConfig: nested go commands must build natively
	t.Chdir("../..")
	if m, _ := filepath.Glob("/boot/vmlinuz-*"); len(m) == 0 {
		t.Skip("no installed kernel image: vmrun.Run checks for one")
	}

	t.Run("pass", func(t *testing.T) {
		fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
		code, out, errOut := runCLI(t, "run", "-mode", "vm", "-allow-skip", fixture)
		if code != 0 {
			t.Fatalf("code %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}
	})

	t.Run("failing package", func(t *testing.T) {
		fakeVNG(t, "sh -c \"$cmd\"\nexit $?\n")
		t.Setenv("VMRUN_FIXTURE_FAIL", "1")
		code, _, _ := runCLI(t, "run", "-mode", "vm", "-allow-skip", fixture)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
	})

	t.Run("run not carried out", func(t *testing.T) {
		fakeVNG(t, "sleep 5\n") // never touches $cmd: the guest never boots
		code, _, _ := runCLI(t, "run", "-mode", "vm", "-vm-timeout", "1s", fixture)
		if code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
	})
}

func TestRunVMFailsClearlyWithoutVNG(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, _, errOut := runCLI(t, "run", "-mode", "vm")
	if code != 2 || !strings.Contains(errOut, "vng") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

// M1-09 test: every "run" flag that maps onto vmrun.Config lands there correctly; -mode is
// returned on its own, since it decides direct vs. VM rather than configuring either.
func TestParseRunFlags(t *testing.T) {
	var errb bytes.Buffer
	cfg, mode, err := parseRunFlags([]string{
		"-mode", "vm", "-kernel", "1.2.3-generic", "-mem", "4G", "-cpus", "4",
		"-tags", "testbed,extra", "-run", "TestFoo", "-test-timeout", "5s", "-vm-timeout", "10s",
		"-work", "/tmp/work", "-keep", "-allow-skip", "-no-kvm", "./a", "./b",
	}, &errb)
	if err != nil {
		t.Fatalf("err = %v, stderr %q", err, errb.String())
	}
	if mode != "vm" {
		t.Errorf("mode = %q", mode)
	}
	want := vmrun.Config{
		Kernel: "1.2.3-generic", Memory: "4G", CPUs: 4,
		Tags: []string{"testbed", "extra"}, Packages: []string{"./a", "./b"}, Run: "TestFoo",
		TestTimeout: 5 * time.Second, VMTimeout: 10 * time.Second, WorkDir: "/tmp/work", Keep: true,
		AllowSkip: true, NoKVM: true,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("config = %+v\nwant   = %+v", cfg, want)
	}
}

func TestParseRunFlagsDefaultsFromEnv(t *testing.T) {
	t.Setenv("TESTVM_KERNEL", "9.9.9-generic")
	t.Setenv("TESTVM_MEM", "8G")
	var errb bytes.Buffer
	cfg, _, err := parseRunFlags(nil, &errb)
	if err != nil {
		t.Fatalf("err = %v, stderr %q", err, errb.String())
	}
	if cfg.Kernel != "9.9.9-generic" || cfg.Memory != "8G" {
		t.Fatalf("config = %+v", cfg)
	}
	if len(cfg.Packages) != 1 || cfg.Packages[0] != "./..." {
		t.Fatalf("default packages = %v", cfg.Packages)
	}
}

// M1-07 test: the sweep command's namespace filter matches only testbed namespaces.
func TestSweepNamespaceFilter(t *testing.T) {
	for _, name := range []string{"tb1a2b3c4d-gw", "tb00000000-a", "tbdeadbeef-"} {
		if !sweepNS.MatchString(name) {
			t.Errorf("%q should match", name)
		}
	}
	for _, name := range []string{"tb1a2b3c4-gw", "other-ns", "tbXYZWXYZW-gw", "", "notb12345678-gw"} {
		if sweepNS.MatchString(name) {
			t.Errorf("%q should not match", name)
		}
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("TESTVM_UNIT", "from-env")
	if got := envOr("TESTVM_UNIT", "default"); got != "from-env" {
		t.Errorf("got %q", got)
	}
	if got := envOr("TESTVM_UNSET_VARIABLE", "default"); got != "default" {
		t.Errorf("got %q", got)
	}
}
