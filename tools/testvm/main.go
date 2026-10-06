// Command testvm runs the namespace testbed tests (build tag "testbed").
//
//	testvm preflight           can this machine run the testbed directly (level 1)?
//	testvm run [flags] [pkgs]  run the tests: directly (level 1) or in a VM (level 1b)
//	testvm sweep [-force]      remove namespaces a crashed direct run left behind
//	testvm vm <command>        a persistent VM for the fast loop: up, run, exec, status, down
//
// `run -mode auto` (the default) chooses by the preflight: where namespaces and kernel modules
// are available (a privileged container, a VM) the tests run directly; everywhere else, such as
// the unprivileged devcontainer, they run in a QEMU VM with a stock kernel. The tests are never
// skipped silently: a skip fails the run unless `-allow-skip` is given. See docs/development.md.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
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
		fmt.Fprintln(stderr, "usage: testvm preflight | testvm run [flags] [packages] | testvm vm <command>")
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
	case "sweep":
		return sweep(ctx, args[1:], stdout, stderr)
	case "vm":
		return vmCommand(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "testvm: unknown command %q\n", args[0])
		return 2
	}
}

// parseRunFlags parses the flags of "run" into a vmrun.Config (used whichever mode is chosen)
// and the requested mode string ("auto", "direct" or "vm"). It does no I/O, so it is testable on
// its own.
func parseRunFlags(args []string, stderr io.Writer) (vmrun.Config, string, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "auto", "auto, direct (level 1, needs privileges) or vm (level 1b)")
	kernel := fs.String("kernel", envOr("TESTVM_KERNEL", vmrun.DefaultKernel), "kernel release for the VM")
	mem := fs.String("mem", envOr("TESTVM_MEM", "2G"), "VM memory")
	cpus := fs.Int("cpus", 2, "VM CPUs")
	tags := fs.String("tags", "testbed", "build tags of the tests")
	runRE := fs.String("run", "", "only tests matching this regular expression")
	fs.Bool("v", false, "accepted for compatibility; direct mode is always verbose now (CC-04)")
	testTimeout := fs.Duration("test-timeout", 20*time.Minute, "timeout per test binary")
	vmTimeout := fs.Duration("vm-timeout", 60*time.Minute, "timeout of the whole VM")
	work := fs.String("work", "", "work directory shared with the VM (default: a temporary one that is kept after a failure; a named one is never removed)")
	keep := fs.Bool("keep", false, "also keep a created work directory after a successful run")
	allowSkip := fs.Bool("allow-skip", false, "allow skipped tests; otherwise any skip fails the run")
	noKVM := fs.Bool("no-kvm", false, "force software emulation in VM mode even where /dev/kvm is available")
	if err := fs.Parse(args); err != nil {
		return vmrun.Config{}, "", err
	}
	pkgs := fs.Args()
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	c := vmrun.Config{
		Kernel: *kernel, Memory: *mem, CPUs: *cpus,
		Tags: strings.Split(*tags, ","), Packages: pkgs, Run: *runRE,
		TestTimeout: *testTimeout, VMTimeout: *vmTimeout, WorkDir: *work, Keep: *keep,
		AllowSkip: *allowSkip, NoKVM: *noKVM,
	}
	return c, *mode, nil
}

func runTests(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, mode, err := parseRunFlags(args, stderr)
	if err != nil {
		return 2
	}

	useVM := false
	switch mode {
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
		fmt.Fprintf(stderr, "testvm: unknown mode %q\n", mode)
		return 2
	}

	if !useVM {
		return runDirect(ctx, cfg, stdout, stderr)
	}

	cfg.Stdout, cfg.Stderr = stdout, stderr
	summary, err := vmrun.Run(ctx, cfg)
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

// runDirect runs the test binaries on the host (level 1). It always runs them with -test.v
// (CC-04: the output must show every test, so a skip is never silently hidden) and fails unless
// every skip is allowed, the same rule vmrun.Summary.OK applies to a VM run.
func runDirect(ctx context.Context, cfg vmrun.Config, stdout, stderr io.Writer) int {
	tags := strings.Join(cfg.Tags, ",")
	units, err := vmrun.TestbedPackages(ctx, ".", cfg.Tags, cfg.Packages)
	if err != nil {
		fmt.Fprintln(stderr, "testvm:", err)
		return 2
	}
	if len(units) == 0 {
		fmt.Fprintf(stderr, "testvm: no package with tests under the tags %q matches %v\n", tags, cfg.Packages)
		return 2
	}
	goArgs := []string{"test", "-count=1", "-tags", tags, "-v"}
	if cfg.Run != "" {
		goArgs = append(goArgs, "-run", cfg.Run)
	}
	for _, u := range units {
		goArgs = append(goArgs, u.ImportPath)
	}
	cmd := exec.CommandContext(ctx, "go", goArgs...)
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(stdout, &out)
	cmd.Stderr = io.MultiWriter(stderr, &out)
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			fmt.Fprintln(stderr, "testvm:", runErr)
			return 2
		}
		code = ee.ExitCode()
	}
	counts := vmrun.ParseCounts(out.String())
	if counts.Skipped > 0 && !cfg.AllowSkip {
		fmt.Fprintf(stdout, "\nproblem: %d tests skipped (pass -allow-skip to allow)\n", counts.Skipped)
		if code == 0 {
			code = 1
		}
	}
	return code
}

var sweepNS = regexp.MustCompile(`^tb[0-9a-f]{8}-`)

// sweep removes namespaces a crashed direct (level 1) run left behind; level 1b gets a fresh VM
// and is never affected. It refuses while another testvm or test binary is still running unless
// -force is given, since that process may still be using its namespaces.
func sweep(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sweep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "sweep even while another testvm or test binary is running")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*force {
		if running, err := anotherTestvmRunning(); err != nil {
			fmt.Fprintln(stderr, "testvm:", err)
			return 2
		} else if running {
			fmt.Fprintln(stderr, "testvm: another testvm or test binary is running; pass -force to sweep anyway")
			return 2
		}
	}
	out, err := exec.CommandContext(ctx, "ip", "netns", "list").Output()
	if err != nil {
		fmt.Fprintln(stderr, "testvm:", err)
		return 2
	}
	removed := 0
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.Fields(line)
		if len(name) == 0 || !sweepNS.MatchString(name[0]) {
			continue
		}
		ns := name[0]
		if pids, err := exec.CommandContext(ctx, "ip", "netns", "pids", ns).Output(); err == nil {
			for _, p := range strings.Fields(string(pids)) {
				_ = exec.CommandContext(ctx, "kill", "-9", p).Run()
			}
		}
		if err := exec.CommandContext(ctx, "ip", "netns", "del", ns).Run(); err != nil {
			fmt.Fprintf(stderr, "testvm: removing %s: %v\n", ns, err)
			continue
		}
		fmt.Fprintln(stdout, "removed", ns)
		removed++
	}
	fmt.Fprintf(stdout, "testvm: swept %d namespace(s)\n", removed)
	return 0
}

// anotherTestvmRunning reports whether a testvm or a compiled test binary (*.test) other than
// this process is currently running.
func anotherTestvmRunning() (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := parsePID(e.Name())
		if err != nil || pid == self {
			continue
		}
		comm, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if name == "testvm" || strings.HasSuffix(name, ".test") {
			return true, nil
		}
	}
	return false, nil
}

func parsePID(s string) (int, error) {
	return strconv.Atoi(s)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
