// Command testvm runs the namespace testbed tests (build tag "testbed").
//
//	testvm preflight           can this machine run the testbed directly (level 1)?
//	testvm run [flags] [pkgs]  run the tests: directly (level 1) or in a VM (level 1b)
//
// `run -mode auto` (the default) chooses by the preflight: where namespaces and kernel modules
// are available (a privileged container, a VM) the tests run directly; everywhere else, such as
// the unprivileged devcontainer, they run in a QEMU VM with a stock kernel. The tests are never
// skipped silently. See docs/development.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Andste82/chaos-gateway/internal/preflight"
	"github.com/Andste82/chaos-gateway/internal/testbed/vmrun"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: testvm preflight | testvm run [flags] [packages]")
		return 2
	}
	switch args[0] {
	case "preflight":
		rep, err := preflight.Run(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "testvm:", err)
			return 2
		}
		fmt.Fprint(stdout, rep)
		if !rep.OK() {
			return 1
		}
		return 0
	case "run":
		return runTests(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "testvm: unknown command %q\n", args[0])
		return 2
	}
}

func runTests(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "auto", "auto, direct (level 1, needs privileges) or vm (level 1b)")
	kernel := fs.String("kernel", envOr("TESTVM_KERNEL", vmrun.DefaultKernel), "kernel release for the VM")
	mem := fs.String("mem", envOr("TESTVM_MEM", "2G"), "VM memory")
	cpus := fs.Int("cpus", 2, "VM CPUs")
	tags := fs.String("tags", "testbed", "build tags of the tests")
	runRE := fs.String("run", "", "only tests matching this regular expression")
	verbose := fs.Bool("v", false, "verbose output in direct mode")
	testTimeout := fs.Duration("test-timeout", 20*time.Minute, "timeout per test binary")
	vmTimeout := fs.Duration("vm-timeout", 60*time.Minute, "timeout of the whole VM")
	work := fs.String("work", "", "work directory shared with the VM (default: a temporary one that is kept after a failure; a named one is never removed)")
	keep := fs.Bool("keep", false, "also keep a created work directory after a successful run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pkgs := fs.Args()
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}

	useVM := false
	switch *mode {
	case "vm":
		useVM = true
	case "direct":
	case "auto":
		rep, err := preflight.Run(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "testvm:", err)
			return 2
		}
		useVM = !rep.OK()
		if useVM {
			fmt.Fprintf(stderr, "testvm: the namespace testbed cannot run here, using a VM (level 1b):\n  %s\n",
				strings.Join(rep.Problems(), "\n  "))
		} else {
			fmt.Fprintln(stderr, "testvm: running directly (level 1)")
		}
	default:
		fmt.Fprintf(stderr, "testvm: unknown mode %q\n", *mode)
		return 2
	}

	if !useVM {
		units, err := vmrun.TestbedPackages(ctx, ".", strings.Split(*tags, ","), pkgs)
		if err != nil {
			fmt.Fprintln(stderr, "testvm:", err)
			return 2
		}
		if len(units) == 0 {
			fmt.Fprintf(stderr, "testvm: no package with tests under the tags %q matches %v\n", *tags, pkgs)
			return 2
		}
		goArgs := []string{"test", "-count=1", "-tags", *tags}
		if *verbose {
			goArgs = append(goArgs, "-v")
		}
		if *runRE != "" {
			goArgs = append(goArgs, "-run", *runRE)
		}
		for _, u := range units {
			goArgs = append(goArgs, u.ImportPath)
		}
		cmd := exec.CommandContext(ctx, "go", goArgs...)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return ee.ExitCode()
			}
			fmt.Fprintln(stderr, "testvm:", err)
			return 2
		}
		return 0
	}

	summary, err := vmrun.Run(ctx, vmrun.Config{
		Kernel: *kernel, Memory: *mem, CPUs: *cpus,
		Tags: strings.Split(*tags, ","), Packages: pkgs, Run: *runRE,
		TestTimeout: *testTimeout, VMTimeout: *vmTimeout, WorkDir: *work, Keep: *keep,
		Stdout: stdout, Stderr: stderr,
	})
	if len(summary.Packages) > 0 {
		// also after a timeout or a cancel: whatever the guest recorded is worth seeing
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
