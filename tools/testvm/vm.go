package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/testbed/vmrun"
)

const vmUsage = `usage: testvm vm <command>

  up [flags]               boot the persistent VM in the background (minutes without KVM)
  exec [flags] -- cmd...   run a command in the guest (one argument: a shell command line)
  run [flags] [packages]   build the testbed tests and run them in the VM (the fast loop)
  down [-now]              power the VM off and remove its markers
  status                   up, starting, stale or down; kernel, uptime, queue

All commands take -dir (default $TESTVM_VM_DIR or ` + vmrun.DefaultVMDir + `). Jobs run one at a time.`

// vmCommand dispatches "testvm vm <command>".
func vmCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, vmUsage)
		return 2
	}
	switch args[0] {
	case "up":
		return vmUp(ctx, args[1:], stdout, stderr)
	case "exec":
		return vmExec(ctx, args[1:], stdout, stderr)
	case "run":
		return vmRun(ctx, args[1:], stdout, stderr)
	case "down":
		return vmDown(ctx, args[1:], stdout, stderr)
	case "status":
		return vmStatus(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "testvm vm: unknown command %q\n%s\n", args[0], vmUsage)
		return 2
	}
}

// vmDirFlag registers -dir on fs.
func vmDirFlag(fs *flag.FlagSet) *string {
	return fs.String("dir", string(vmrun.DefaultDir()), "directory of the persistent VM, shared with the guest")
}

func vmUp(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vm up", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := vmDirFlag(fs)
	kernel := fs.String("kernel", envOr("TESTVM_KERNEL", vmrun.DefaultKernel), "kernel release for the VM")
	mem := fs.String("mem", envOr("TESTVM_MEM", "2G"), "VM memory")
	cpus := fs.Int("cpus", 2, "VM CPUs")
	noKVM := fs.Bool("no-kvm", false, "force software emulation even where /dev/kvm is available")
	timeout := fs.Duration("timeout", 30*time.Minute, "how long to wait for the guest to become ready")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	err := vmrun.Up(ctx, vmrun.UpConfig{
		Dir: vmrun.VMDir(*dir), Kernel: *kernel, Memory: *mem, CPUs: *cpus, NoKVM: *noKVM,
		Timeout: *timeout, Stderr: stderr,
	})
	if err != nil {
		fmt.Fprintln(stderr, "testvm:", err)
		return 2
	}
	fmt.Fprintf(stdout, `The VM runs in the background; jobs run one at a time.
  testvm vm run -run TestX ./internal/apply   build and run testbed tests (make vm-test ARGS='-run TestX ./internal/apply')
  testvm vm exec -- nft list ruleset           run a command in the guest (make vm-exec CMD='...')
  testvm vm status | testvm vm down            (make vm-status | make vm-down)
`)
	return 0
}

func vmExec(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vm exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := vmDirFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Minute, "kill the command in the guest after this long")
	cwd := fs.String("C", "", "directory to run in (default: the current directory; the guest sees the host's files)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: testvm vm exec [flags] -- command [args...]")
		return 2
	}
	rc, err := vmrun.Exec(ctx, vmrun.ExecConfig{
		Dir: vmrun.VMDir(*dir), Command: fs.Args(), Cwd: *cwd, Timeout: *timeout, Stdout: stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, "testvm:", err)
		return 2
	}
	return rc
}

func vmRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	// the flags that mean the same as for "testvm run"; -mode, -work, -mem and the like have no
	// meaning here (the VM is already booted)
	fs := flag.NewFlagSet("vm run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := vmDirFlag(fs)
	tags := fs.String("tags", "testbed", "build tags of the tests")
	runRE := fs.String("run", "", "only tests matching this regular expression")
	testTimeout := fs.Duration("test-timeout", 20*time.Minute, "timeout per test binary")
	vmTimeout := fs.Duration("vm-timeout", 60*time.Minute, "timeout of the whole run")
	keep := fs.Bool("keep", false, "keep the run directory after a passing run")
	allowSkip := fs.Bool("allow-skip", false, "allow skipped tests; otherwise any skip fails the run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pkgs := fs.Args()
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	cfg := vmrun.Config{
		Tags: strings.Split(*tags, ","), Packages: pkgs, Run: *runRE,
		TestTimeout: *testTimeout, VMTimeout: *vmTimeout, Keep: *keep, AllowSkip: *allowSkip,
		Stdout: stdout, Stderr: stderr,
	}
	summary, err := vmrun.RunInVM(ctx, vmrun.VMDir(*dir), cfg)
	if len(summary.Packages) > 0 {
		fmt.Fprint(stdout, "\n"+summary.Report())
	}
	if err != nil {
		fmt.Fprintln(stderr, "testvm:", err)
		return 2
	}
	if !summary.OK() {
		return 1
	}
	return 0
}

func vmDown(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vm down", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := vmDirFlag(fs)
	now := fs.Bool("now", false, "kill the VM at once instead of asking the guest to power off")
	grace := fs.Duration("grace", 90*time.Second, "how long to wait for the guest to power off before killing it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := vmrun.Down(ctx, nil, vmrun.VMDir(*dir), *now, *grace, stdout); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		fmt.Fprintln(stderr, "testvm:", err)
		return 2
	}
	return 0
}

func vmStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vm status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := vmDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	st, err := vmrun.VMDir(*dir).Status()
	if err != nil {
		fmt.Fprintln(stderr, "testvm:", err)
		return 2
	}
	fmt.Fprint(stdout, formatStatus(*dir, st, clock.NewReal().Now()))
	if st.State != vmrun.StateUp {
		return 1
	}
	return 0
}

// formatStatus renders the status for people.
func formatStatus(dir string, st vmrun.Status, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "state:   %s (%s)\n", st.State, dir)
	if st.State == vmrun.StateDown {
		return b.String()
	}
	fmt.Fprintf(&b, "kernel:  %s (guest: %s)\n", st.Meta.Kernel, orDash(st.Kernel))
	fmt.Fprintf(&b, "vm:      %s, %d cpus, kvm=%v\n", st.Meta.Memory, st.Meta.CPUs, st.Meta.KVM)
	if st.State == vmrun.StateUp || st.State == vmrun.StateStarting {
		if !st.Meta.Started.IsZero() {
			fmt.Fprintf(&b, "uptime:  %v\n", now.Sub(st.Meta.Started).Round(time.Second))
		}
	}
	if len(st.Procs) > 0 {
		fmt.Fprintf(&b, "pid:     %d\n", st.Procs[0].PID)
	}
	if st.State == vmrun.StateUp {
		fmt.Fprintf(&b, "queue:   %d waiting, %d running\n", st.Queued, st.Running)
	}
	if st.State == vmrun.StateStale {
		b.WriteString("note:    the VM process is gone; `testvm vm down` clears the files\n")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
