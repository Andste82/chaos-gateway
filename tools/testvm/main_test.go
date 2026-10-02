package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
	code, out, errOut := runCLI(t, "run", "-mode", "direct", "-v", fixture)
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

func TestRunVMFailsClearlyWithoutVNG(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, _, errOut := runCLI(t, "run", "-mode", "vm")
	if code != 2 || !strings.Contains(errOut, "vng") {
		t.Fatalf("code %d, stderr %q", code, errOut)
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
