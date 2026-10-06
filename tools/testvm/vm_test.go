package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed/vmrun"
)

func runVM(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), append([]string{"vm"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVMNeedsAKnownSubcommand(t *testing.T) {
	if code, _, stderr := runVM(t); code != 2 || !strings.Contains(stderr, "usage: testvm vm") {
		t.Errorf("no subcommand: %d %q", code, stderr)
	}
	if code, _, stderr := runVM(t, "reboot"); code != 2 || !strings.Contains(stderr, `unknown command "reboot"`) {
		t.Errorf("unknown subcommand: %d %q", code, stderr)
	}
}

func TestVMStatusOfNoVMSaysDownAndExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	code, stdout, _ := runVM(t, "status", "-dir", dir)
	if code != 1 || !strings.Contains(stdout, "state:   down") {
		t.Errorf("%d %q", code, stdout)
	}
}

func TestVMStatusOfAStaleVMTellsWhatToDo(t *testing.T) {
	dir := t.TempDir()
	d := vmrun.VMDir(dir)
	if err := os.WriteFile(d.PIDFile(), []byte(vmrun.FormatPIDFile([]vmrun.ProcRef{{PID: 1 << 30, Start: 1}})), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runVM(t, "status", "-dir", dir)
	if code != 1 || !strings.Contains(stdout, "stale") || !strings.Contains(stdout, "testvm vm down") {
		t.Errorf("%d %q", code, stdout)
	}
	// down clears it, and a second down is harmless
	for i := 0; i < 2; i++ {
		if code, _, stderr := runVM(t, "down", "-dir", dir, "-now"); code != 0 {
			t.Errorf("down #%d: %d %q", i, code, stderr)
		}
	}
	if code, stdout, _ := runVM(t, "status", "-dir", dir); code != 1 || !strings.Contains(stdout, "down") {
		t.Errorf("after down: %d %q", code, stdout)
	}
}

func TestVMExecAndRunExplainThatNoVMIsUp(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"exec", "-dir", dir, "--", "true"},
		{"run", "-dir", dir, "./internal/apply"},
	} {
		code, _, stderr := runVM(t, args...)
		if code != 2 || !strings.Contains(stderr, "no VM is up") || !strings.Contains(stderr, "testvm vm up") {
			t.Errorf("%v: %d %q", args, code, stderr)
		}
	}
	if code, _, stderr := runVM(t, "exec", "-dir", dir); code != 2 || !strings.Contains(stderr, "usage: testvm vm exec") {
		t.Errorf("exec without a command: %d %q", code, stderr)
	}
}

func TestVMUpRefusesAVMThatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	d := vmrun.VMDir(dir)
	// a child, not this process: under qemu-user (the arm64 job) the emulator synthesizes
	// /proc/<own pid>/stat, only another process shows the kernel's values
	child := exec.Command("sleep", "300")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	self, err := vmrun.NewProcRef(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(d.PIDFile(), []byte(vmrun.FormatPIDFile([]vmrun.ProcRef{self})), 0o644)
	if _, err := exec.LookPath("vng"); err != nil {
		t.Skip("vng is not installed")
	}
	if !vmrun.KernelInstalled(vmrun.DefaultKernel) {
		t.Skip("the default kernel is not installed")
	}
	code, _, stderr := runVM(t, "up", "-dir", dir, "-timeout", "1s")
	if code != 2 || !strings.Contains(stderr, "already") {
		t.Errorf("%d %q", code, stderr)
	}
	// the files of the VM that is "there" were not touched
	if _, err := os.Stat(d.PIDFile()); err != nil {
		t.Error("up removed the pids file of a running VM")
	}
}

func TestFormatStatusShowsWhatAPersonNeeds(t *testing.T) {
	started := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	st := vmrun.Status{
		State:  vmrun.StateUp,
		Meta:   vmrun.Meta{Kernel: "6.8.0-142-generic", Memory: "2G", CPUs: 2, Started: started},
		Procs:  []vmrun.ProcRef{{PID: 4242, Start: 1}},
		Kernel: "6.8.0-142-generic", Queued: 2, Running: 1,
	}
	got := formatStatus("/tmp/chaosgw-vm", st, started.Add(95*time.Minute))
	for _, want := range []string{"state:   up (/tmp/chaosgw-vm)", "guest: 6.8.0-142-generic", "2G, 2 cpus, kvm=false", "uptime:  1h35m0s", "pid:     4242", "queue:   2 waiting, 1 running"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
}
