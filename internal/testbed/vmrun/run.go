package vmrun

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// DefaultKernel is the oldest supported kernel, the Ubuntu 24.04 GA kernel (plan D1).
const DefaultKernel = "6.8.0-142-generic"

// Config describes one VM test run.
type Config struct {
	// Dir is the module root: where `go list` and `go test -c` run.
	Dir string
	// Kernel is the release of an installed kernel (/boot/vmlinuz-<release>).
	Kernel string
	// Memory and CPUs size the VM.
	Memory string
	CPUs   int
	// Tags are the build tags of the tests; the testbed tests use "testbed".
	Tags []string
	// Packages are the packages to test; they default to ./...
	Packages []string
	// Run is an optional -test.run expression.
	Run string
	// TestTimeout limits each test binary in the guest; VMTimeout limits the whole VM.
	TestTimeout time.Duration
	VMTimeout   time.Duration
	// WorkDir is the share between host and guest. Empty means a fresh directory below the
	// temporary directory. It is removed afterwards unless Keep is set.
	WorkDir string
	Keep    bool
	// AllowSkip allows skipped tests; otherwise Summary.OK reports a skip as a failure (CC-04:
	// a skip must never pass silently).
	AllowSkip bool
	// NoKVM forces software emulation even where /dev/kvm is available (M1-03: a weekly check
	// that the emulated branches, which the development VPS always runs, still work elsewhere).
	NoKVM bool
	// Stdout receives the guest's console and the summary; Stderr the host-side progress.
	Stdout, Stderr io.Writer
}

func (c *Config) defaults() {
	if c.Dir == "" {
		c.Dir = "."
	}
	if c.Kernel == "" {
		c.Kernel = DefaultKernel
	}
	if c.Memory == "" {
		c.Memory = "2G"
	}
	if c.CPUs == 0 {
		c.CPUs = 2
	}
	if len(c.Tags) == 0 {
		c.Tags = []string{"testbed"}
	}
	if len(c.Packages) == 0 {
		c.Packages = []string{"./..."}
	}
	if c.TestTimeout == 0 {
		c.TestTimeout = 20 * time.Minute
	}
	if c.VMTimeout == 0 {
		c.VMTimeout = 60 * time.Minute
	}
	if c.Stdout == nil {
		c.Stdout = io.Discard
	}
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
}

// HasKVM reports whether the machine offers hardware virtualization to this process.
func HasKVM() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// KernelInstalled reports whether the kernel image of release exists.
func KernelInstalled(release string) bool {
	_, err := os.Stat("/boot/vmlinuz-" + release)
	return err == nil
}

// VNGArgs returns the arguments of vng that boot the guest and run the guest script. The
// command line of the guest kernel is limited, so --exec only names the script.
func VNGArgs(c Config, kvm bool) []string {
	args := []string{"-r", c.Kernel}
	if !kvm {
		args = append(args, "--disable-kvm")
	}
	return append(args,
		"--memory", c.Memory,
		"--cpus", strconv.Itoa(c.CPUs),
		// the explicit guest=host form: with a bare absolute path vng computes a relative guest
		// path and rejects it ("path must be defined inside a valid overlay")
		"--rwdir="+c.WorkDir+"="+c.WorkDir,
		"--exec", "sh "+ShellQuote(filepath.Join(c.WorkDir, "guest.sh")),
	)
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

// WithPTY returns the command line that gives cmd a pseudo-terminal when there is none: vng
// refuses to start without one (in scripts and CI). script(1) provides it and passes the exit
// code of the command through.
func WithPTY(cmd []string, haveTTY bool) []string {
	if haveTTY {
		return cmd
	}
	quoted := make([]string, len(cmd))
	for i, a := range cmd {
		quoted[i] = ShellQuote(a)
	}
	return []string{"script", "-qec", strings.Join(quoted, " "), "/dev/null"}
}

// goList runs `go list -e` and returns, per package, its import path, directory and the number
// of test files. Packages without any Go file under the tags are reported with 0 test files.
func goList(ctx context.Context, c Config, tags []string, patterns []string) ([]Unit, map[string]int, error) {
	args := []string{"list", "-e"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, "-f", `{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\t"}}{{len .TestGoFiles}}{{"\t"}}{{len .XTestGoFiles}}`)
	args = append(args, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = c.Dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var units []Unit
	counts := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 4 {
			return nil, nil, fmt.Errorf("vmrun: unexpected go list line %q", sc.Text())
		}
		n1, _ := strconv.Atoi(f[2])
		n2, _ := strconv.Atoi(f[3])
		units = append(units, Unit{Name: UnitName(f[0]), ImportPath: f[0], Dir: f[1]})
		counts[f[0]] = n1 + n2
	}
	return units, counts, sc.Err()
}

// TestbedPackages returns the packages among patterns that have tests only under the build tags:
// the tests that need the namespace testbed. Plain unit tests belong to level 0 and are not run
// again in a VM, where they would be slow and some of them (the runner's own) cannot work.
func TestbedPackages(ctx context.Context, dir string, tags, patterns []string) ([]Unit, error) {
	c := Config{Dir: dir}
	tagged, withTags, err := goList(ctx, c, tags, patterns)
	if err != nil {
		return nil, err
	}
	_, without, err := goList(ctx, c, nil, patterns)
	if err != nil {
		return nil, err
	}
	var units []Unit
	for _, u := range tagged {
		if withTags[u.ImportPath] > without[u.ImportPath] {
			units = append(units, u)
		}
	}
	return units, nil
}

// buildUnits compiles the test binary of every unit into workDir/bin.
func buildUnits(ctx context.Context, c Config, units []Unit) error {
	bin := filepath.Join(c.WorkDir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	for i := range units {
		units[i].Binary = filepath.Join(bin, units[i].Name+".test")
		cmd := exec.CommandContext(ctx, "go", "test", "-c", "-tags", strings.Join(c.Tags, ","),
			"-o", units[i].Binary, units[i].ImportPath)
		cmd.Dir = c.Dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go test -c %s: %w\n%s", units[i].ImportPath, err, out)
		}
	}
	return nil
}

// Run builds the test binaries, boots one VM, runs them all in it and returns the summary. The
// error is non-nil when the run could not be carried out completely (the VM hit its timeout, the
// run was cancelled); the summary then still holds whatever the guest recorded. Failing tests are
// reported in the Summary only.
//
// A work directory that Run created is removed after a successful run; after a failure or an
// error it is kept and its path is printed, so the output can be inspected. A directory the
// caller named is never removed, but its results of earlier runs are cleared before the boot:
// stale files must never make a dead VM look like a pass.
func Run(ctx context.Context, c Config) (Summary, error) {
	c.defaults()
	if _, err := exec.LookPath("vng"); err != nil {
		return Summary{}, errors.New("vng (virtme-ng) is not installed; it is part of the devcontainer image")
	}
	if !KernelInstalled(c.Kernel) {
		return Summary{}, fmt.Errorf("kernel %s is not installed (/boot/vmlinuz-%s)", c.Kernel, c.Kernel)
	}
	created := false
	if c.WorkDir == "" {
		dir, err := os.MkdirTemp("", "testvm-")
		if err != nil {
			return Summary{}, err
		}
		c.WorkDir, created = dir, true
	} else if err := os.MkdirAll(c.WorkDir, 0o755); err != nil {
		return Summary{}, err
	}
	abs, err := filepath.Abs(c.WorkDir)
	if err != nil {
		return Summary{}, err
	}
	c.WorkDir = abs
	for _, stale := range []string{"results", "bin", "guest.sh"} {
		if err := os.RemoveAll(filepath.Join(c.WorkDir, stale)); err != nil {
			return Summary{}, err
		}
	}

	summary, err := run(ctx, c)
	if created && !c.Keep {
		if err == nil && summary.OK() {
			_ = os.RemoveAll(c.WorkDir)
		} else {
			fmt.Fprintf(c.Stderr, "vmrun: the work directory is kept for inspection: %s\n", c.WorkDir)
		}
	}
	return summary, err
}

func run(ctx context.Context, c Config) (Summary, error) {
	units, err := TestbedPackages(ctx, c.Dir, c.Tags, c.Packages)
	if err != nil {
		return Summary{}, err
	}
	if len(units) == 0 {
		return Summary{}, fmt.Errorf("no package with tests under the tags %v matches %v", c.Tags, c.Packages)
	}
	fmt.Fprintf(c.Stderr, "vmrun: building %d test binaries\n", len(units))
	if err := buildUnits(ctx, c, units); err != nil {
		return Summary{}, err
	}

	kvm := HasKVM() && !c.NoKVM
	script := GuestScript(GuestOptions{
		WorkDir: c.WorkDir, Emulated: !kvm, Run: c.Run, TestTimeout: c.TestTimeout.String(),
	}, units)
	if err := os.WriteFile(filepath.Join(c.WorkDir, "guest.sh"), []byte(script), 0o755); err != nil {
		return Summary{}, err
	}

	cmdline := WithPTY(append([]string{"vng"}, VNGArgs(c, kvm)...), IsTerminal(os.Stdin) && IsTerminal(os.Stdout))
	fmt.Fprintf(c.Stderr, "vmrun: booting %s (kvm=%v); this takes minutes without KVM\n", c.Kernel, kvm)
	vmCtx, cancel := context.WithTimeout(ctx, c.VMTimeout)
	defer cancel()
	cmd := exec.CommandContext(vmCtx, cmdline[0], cmdline[1:]...)
	cmd.Dir = c.Dir
	cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
	cmd.Stdin = nil
	// QEMU is a grandchild (vng -> virtme-run -> qemu, and script(1) puts it behind a pty):
	// killing the direct child alone would leave the VM running, so a timeout or Ctrl-C kills
	// the whole process tree. WaitDelay stops Wait from hanging on pipes the orphans still hold.
	cmd.Cancel = func() error { killTree(cmd.Process.Pid); return nil }
	cmd.WaitDelay = 10 * time.Second
	runErr := cmd.Run()

	summary, err := Collect(os.DirFS(c.WorkDir), units)
	summary.AllowSkip = c.AllowSkip
	if err != nil {
		return summary, err
	}
	switch {
	case runErr != nil && errors.Is(vmCtx.Err(), context.DeadlineExceeded):
		return summary, fmt.Errorf("the VM did not finish within %v", c.VMTimeout)
	case runErr != nil && ctx.Err() != nil:
		return summary, fmt.Errorf("cancelled: %w", ctx.Err())
	}
	return summary, nil
}

// descendants returns the pids of all processes below pid, deepest last.
func descendants(pid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, e := range entries {
		p, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// "pid (comm) state ppid ...": comm may contain spaces and parentheses
		stat := string(raw)
		rest := stat[strings.LastIndexByte(stat, ')')+1:]
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			continue
		}
		if ppid, err := strconv.Atoi(fields[1]); err == nil {
			children[ppid] = append(children[ppid], p)
		}
	}
	var out []int
	queue := []int{pid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, ch := range children[cur] {
			out = append(out, ch)
			queue = append(queue, ch)
		}
	}
	return out
}

// killTree kills pid and everything below it.
func killTree(pid int) {
	tree := descendants(pid)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	for _, p := range tree {
		_ = syscall.Kill(p, syscall.SIGKILL)
	}
}
