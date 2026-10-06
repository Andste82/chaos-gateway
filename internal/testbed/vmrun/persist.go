package vmrun

// A persistent VM: the cold boot of a software-emulated VM takes minutes, so the fast loop boots
// it once and keeps it. The guest runs a small serve loop that takes jobs from a queue directory
// in the share between host and guest; the host puts a job there, waits for its exit code and
// streams its output. Jobs run one at a time, in the order they were queued.
//
// Everything is plain files in one directory (default /tmp/chaosgw-vm):
//
//	serve.sh          the guest's serve loop (the one thing vng executes)
//	meta.json         what was booted (kernel, memory, cpus, whether KVM is used)
//	pids              the host processes of the VM, each with its start time (see ProcRef)
//	console.log       the guest console
//	ready             written by the guest when the loop runs; holds the guest kernel release
//	down              written by the host to make the loop end (the VM powers off)
//	q/<id>.job        a queued job (written as <id>.tmp, then renamed: never half written)
//	q/<id>.run        the job the guest is running or has run
//	q/<id>.out        its output, written while it runs
//	q/<id>.rc         its exit code, written after it ended
//	runs/<id>/        the test binaries and results of one `vm run`

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/preflight"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// DefaultVMDir is where the persistent VM lives unless TESTVM_VM_DIR or -dir says otherwise.
const DefaultVMDir = "/tmp/chaosgw-vm"

// VMDirEnv overrides DefaultVMDir.
const VMDirEnv = "TESTVM_VM_DIR"

// DefaultDir returns the directory of the persistent VM.
func DefaultDir() VMDir {
	if v := os.Getenv(VMDirEnv); v != "" {
		return VMDir(v)
	}
	return VMDir(DefaultVMDir)
}

// VMDir is the directory of one persistent VM, shared read-write with the guest.
type VMDir string

// Queue is the job queue.
func (d VMDir) Queue() string { return filepath.Join(string(d), "q") }

// Runs holds the per-run directories of `vm run`.
func (d VMDir) Runs() string { return filepath.Join(string(d), "runs") }

// ServeScript is the guest's serve loop.
func (d VMDir) ServeScript() string { return filepath.Join(string(d), "serve.sh") }

// ReadyFile marks that the guest's loop runs.
func (d VMDir) ReadyFile() string { return filepath.Join(string(d), "ready") }

// DownFile asks the guest's loop to end.
func (d VMDir) DownFile() string { return filepath.Join(string(d), "down") }

// MetaFile describes the booted VM.
func (d VMDir) MetaFile() string { return filepath.Join(string(d), "meta.json") }

// PIDFile lists the host processes of the VM.
func (d VMDir) PIDFile() string { return filepath.Join(string(d), "pids") }

// ConsoleLog is the guest console.
func (d VMDir) ConsoleLog() string { return filepath.Join(string(d), "console.log") }

// Meta says what was booted.
type Meta struct {
	Kernel string `json:"kernel"`
	Memory string `json:"memory"`
	CPUs   int    `json:"cpus"`
	// KVM is true when the VM uses hardware virtualization; the tests then run with real timing.
	KVM     bool      `json:"kvm"`
	Started time.Time `json:"started"`
}

// Emulated reports that the VM runs without hardware virtualization.
func (m Meta) Emulated() bool { return !m.KVM }

// ---- host processes ------------------------------------------------------------------------

// ProcRef names a process of the VM. The start time guards against a reused pid: a recorded pid
// whose process has a different start time is somebody else's and must never be killed.
type ProcRef struct {
	PID   int
	Start uint64
}

// parseProcStat extracts the state and the start time (clock ticks since boot) from the content
// of /proc/<pid>/stat. The command name may contain spaces and parentheses, so the fields are
// counted after the last closing parenthesis.
func parseProcStat(stat string) (state byte, start uint64, err error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, 0, errors.New("vmrun: unexpected /proc stat format")
	}
	f := strings.Fields(stat[i+1:])
	// f[0] is field 3 (state); the start time is field 22
	if len(f) < 20 || len(f[0]) == 0 {
		return 0, 0, errors.New("vmrun: short /proc stat line")
	}
	start, err = strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("vmrun: start time: %w", err)
	}
	return f[0][0], start, nil
}

// procStat reads the state and start time of a process.
func procStat(pid int) (byte, uint64, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, err
	}
	return parseProcStat(string(raw))
}

// NewProcRef records the process pid.
func NewProcRef(pid int) (ProcRef, error) {
	_, start, err := procStat(pid)
	return ProcRef{PID: pid, Start: start}, err
}

// Alive reports whether the recorded process still runs: the pid exists, it is the same process
// (same start time) and it is not a zombie.
func (p ProcRef) Alive() bool {
	state, start, err := procStat(p.PID)
	if err != nil || start != p.Start {
		return false
	}
	return state != 'Z' && state != 'X'
}

// FormatPIDFile is the content of the pids file: one "pid start" line per process.
func FormatPIDFile(refs []ProcRef) string {
	var b strings.Builder
	for _, r := range refs {
		fmt.Fprintf(&b, "%d %d\n", r.PID, r.Start)
	}
	return b.String()
}

// ParsePIDFile reads what FormatPIDFile wrote.
func ParsePIDFile(s string) ([]ProcRef, error) {
	var refs []ProcRef
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("vmrun: bad pids line %q", line)
		}
		pid, err1 := strconv.Atoi(f[0])
		start, err2 := strconv.ParseUint(f[1], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("vmrun: bad pids line %q", line)
		}
		refs = append(refs, ProcRef{PID: pid, Start: start})
	}
	return refs, nil
}

// ---- the guest's serve loop -----------------------------------------------------------------

// ServeOptions are the settings that end up in the serve script.
type ServeOptions struct {
	Dir VMDir
	// Emulated says the VM runs without hardware virtualization (testbed.EmulatedEnv).
	Emulated bool
}

// guestPath is the PATH of the guest, the same for the test runs and for ad-hoc commands.
const guestPath = "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// serveLoop is the guest's loop: it needs W (the VM directory) and Q (its queue) to be set. A job is
// renamed to .run first, so a job is never started twice; its exit code is written last, through a
// rename, so the host never reads a partial number.
const serveLoop = `while [ ! -e "$W/down" ]; do
  for f in "$Q"/*.job; do
    [ -e "$f" ] || continue
    id=${f##*/}
    id=${id%.job}
    mv "$f" "$Q/$id.run"
    sh "$Q/$id.run" > "$Q/$id.out" 2>&1 < /dev/null
    rc=$?
    echo $rc > "$Q/$id.rc.tmp"
    mv "$Q/$id.rc.tmp" "$Q/$id.rc"
  done
  sleep 0.2
done
sync
exit 0
`

// ServeScript returns the script the guest executes: it loads the same kernel modules as the
// one-shot guest script, reports ready and then runs the jobs of the queue one after the other
// until the down marker appears. The job directory is read with a plain glob so no tool beyond
// the shell and sleep is needed.
func ServeScript(o ServeOptions) string {
	var b strings.Builder
	w := ShellQuote(string(o.Dir))
	b.WriteString("#!/bin/sh\n# generated by internal/testbed/vmrun (persistent VM)\nset -u\n")
	if o.Emulated {
		fmt.Fprintf(&b, "export %s=1\n", testbed.EmulatedEnv)
	}
	fmt.Fprintf(&b, "export PATH=%s\n", guestPath)
	fmt.Fprintf(&b, "modprobe %s 2>/dev/null || true\n", strings.Join(preflight.ModprobeArgs(preflight.Modules()), " "))
	fmt.Fprintf(&b, "W=%s\nQ=\"$W/q\"\nmkdir -p \"$Q\"\nrm -f \"$W/down\" \"$W/ready\"\n", w)
	b.WriteString("uname -r > \"$W/ready.tmp\" && mv \"$W/ready.tmp\" \"$W/ready\"\n")
	b.WriteString(serveLoop)
	return b.String()
}

// ---- the job protocol -------------------------------------------------------------------------

var jobSeq atomic.Uint64

// NewJobID returns an id that sorts in the order jobs are created: the time in nanoseconds,
// zero padded, then the pid and a sequence number so that ids from several processes (or the
// same nanosecond) stay unique.
func NewJobID(now time.Time, pid int, seq uint64) string {
	return fmt.Sprintf("%019d-%06d-%04d", now.UnixNano(), pid%1000000, seq%10000)
}

func nextJobID(clk clock.Clock) string { return NewJobID(clk.Now(), os.Getpid(), jobSeq.Add(1)) }

// realClock returns clk, or the real clock where none was given.
func realClock(clk clock.Clock) clock.Clock {
	if clk == nil {
		return clock.NewReal()
	}
	return clk
}

// WriteJob queues a job. The file appears under its final name in one step (written as .tmp and
// renamed), so the guest never runs a half-written script.
func WriteJob(queue, id, script string) error {
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(queue, id+".tmp")
	if err := os.WriteFile(tmp, []byte(script), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(queue, id+".job"))
}

// WaitJob waits until the guest wrote the exit code of the job and returns it. The output of the
// job is copied to out while it grows. When ctx ends first, the job keeps running in the guest.
func WaitJob(ctx context.Context, clk clock.Clock, queue, id string, out io.Writer, poll time.Duration) (int, error) {
	clk = realClock(clk)
	outFile := filepath.Join(queue, id+".out")
	rcFile := filepath.Join(queue, id+".rc")
	var off int64
	flush := func() {
		f, err := os.Open(outFile)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return
		}
		n, _ := io.Copy(out, f)
		off += n
	}
	for {
		flush()
		if raw, err := os.ReadFile(rcFile); err == nil {
			flush() // what the guest wrote between the last flush and its exit code
			rc, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				return 0, fmt.Errorf("vmrun: job %s: bad exit code %q", id, strings.TrimSpace(string(raw)))
			}
			return rc, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-clk.After(poll):
		}
	}
}

// QueueStats counts the jobs waiting for the guest and the ones it is running now.
func QueueStats(queue string) (queued, running int) {
	jobs, _ := filepath.Glob(filepath.Join(queue, "*.job"))
	runs, _ := filepath.Glob(filepath.Join(queue, "*.run"))
	for _, r := range runs {
		if _, err := os.Stat(strings.TrimSuffix(r, ".run") + ".rc"); err != nil {
			running++
		}
	}
	return len(jobs), running
}

// removeJob deletes the files of a finished job.
func removeJob(queue, id string) {
	for _, ext := range []string{".job", ".tmp", ".run", ".out", ".rc"} {
		_ = os.Remove(filepath.Join(queue, id+ext))
	}
}

// ---- status -----------------------------------------------------------------------------------

// State is the state of the persistent VM.
type State string

// The states of a persistent VM.
const (
	StateDown     State = "down"     // nothing recorded
	StateStarting State = "starting" // the process runs, the guest loop is not ready yet
	StateUp       State = "up"       // jobs are accepted
	StateStale    State = "stale"    // files of a VM whose process is gone
)

// Status describes the persistent VM.
type Status struct {
	State  State
	Meta   Meta
	Procs  []ProcRef
	Kernel string // the guest's kernel release, once ready
	Queued int
	// Running is the number of jobs the guest executes right now (0 or 1).
	Running int
}

// Status reads the state of the VM from its directory.
func (d VMDir) Status() (Status, error) {
	var st Status
	raw, err := os.ReadFile(d.PIDFile())
	if errors.Is(err, os.ErrNotExist) {
		st.State = StateDown
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Procs, err = ParsePIDFile(string(raw))
	if err != nil {
		return st, err
	}
	if m, err := os.ReadFile(d.MetaFile()); err == nil {
		_ = json.Unmarshal(m, &st.Meta)
	}
	if len(st.Procs) == 0 || !st.Procs[0].Alive() {
		st.State = StateStale
		return st, nil
	}
	st.State = StateStarting
	if k, err := os.ReadFile(d.ReadyFile()); err == nil {
		st.State = StateUp
		st.Kernel = strings.TrimSpace(string(k))
	}
	st.Queued, st.Running = QueueStats(d.Queue())
	return st, nil
}

// requireUp returns the status of a VM that accepts jobs, or an error that says what to do.
func (d VMDir) requireUp() (Status, error) {
	st, err := d.Status()
	if err != nil {
		return st, err
	}
	switch st.State {
	case StateUp:
		return st, nil
	case StateStarting:
		return st, fmt.Errorf("the VM in %s is still booting; wait for `testvm vm up` to finish", d)
	case StateStale:
		return st, fmt.Errorf("the VM in %s is not running (stale files); run `testvm vm down`, then `testvm vm up`", d)
	default:
		return st, fmt.Errorf("no VM is up in %s; start one with `testvm vm up` (make vm-up)", d)
	}
}

// ---- up, exec, run, down ------------------------------------------------------------------------

// UpConfig describes the VM to boot.
type UpConfig struct {
	Dir    VMDir
	Kernel string
	Memory string
	CPUs   int
	NoKVM  bool
	// Timeout limits the wait for the guest to become ready.
	Timeout time.Duration
	// Stderr receives the progress messages.
	Stderr io.Writer
	// Clock defaults to the real clock.
	Clock clock.Clock
}

// VNGScriptArgs returns the arguments of vng that boot the guest and run the script at path.
func VNGScriptArgs(c Config, kvm bool, script string) []string {
	args := []string{"-r", c.Kernel}
	if !kvm {
		args = append(args, "--disable-kvm")
	}
	return append(args,
		"--memory", c.Memory,
		"--cpus", strconv.Itoa(c.CPUs),
		"--rwdir="+c.WorkDir+"="+c.WorkDir,
		"--exec", "sh "+ShellQuote(script),
	)
}

// Up boots the persistent VM in the background and returns when its guest loop is ready. The VM
// runs in a session of its own: it outlives this process.
func Up(ctx context.Context, c UpConfig) error {
	if c.Kernel == "" {
		c.Kernel = DefaultKernel
	}
	if c.Memory == "" {
		c.Memory = "2G"
	}
	if c.CPUs == 0 {
		c.CPUs = 2
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Minute
	}
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
	clk := realClock(c.Clock)
	if _, err := exec.LookPath("vng"); err != nil {
		return errors.New("vng (virtme-ng) is not installed; it is part of the devcontainer image")
	}
	if !KernelInstalled(c.Kernel) {
		return fmt.Errorf("kernel %s is not installed (/boot/vmlinuz-%s)", c.Kernel, c.Kernel)
	}
	abs, err := filepath.Abs(string(c.Dir))
	if err != nil {
		return err
	}
	dir := VMDir(abs)
	if st, err := dir.Status(); err != nil {
		return err
	} else if st.State == StateUp || st.State == StateStarting {
		return fmt.Errorf("a VM is already %s in %s (pid %d); use it or stop it with `testvm vm down`", st.State, dir, st.Procs[0].PID)
	}
	for _, stale := range []string{"q", "runs", "ready", "down", "pids", "meta.json"} {
		if err := os.RemoveAll(filepath.Join(string(dir), stale)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir.Queue(), 0o755); err != nil {
		return err
	}
	kvm := HasKVM() && !c.NoKVM
	meta := Meta{Kernel: c.Kernel, Memory: c.Memory, CPUs: c.CPUs, KVM: kvm, Started: clk.Now()}
	if raw, err := json.MarshalIndent(meta, "", "  "); err != nil {
		return err
	} else if err := os.WriteFile(dir.MetaFile(), raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(dir.ServeScript(), []byte(ServeScript(ServeOptions{Dir: dir, Emulated: !kvm})), 0o755); err != nil {
		return err
	}
	console, err := os.OpenFile(dir.ConsoleLog(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = console.Close() }()

	vc := Config{Kernel: c.Kernel, Memory: c.Memory, CPUs: c.CPUs, WorkDir: string(dir)}
	// no terminal of our own to give: vng insists on one, so script(1) provides it
	cmdline := WithPTY(append([]string{"vng"}, VNGScriptArgs(vc, kvm, dir.ServeScript())...), false)
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Stdout, cmd.Stderr = console, console
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	fmt.Fprintf(c.Stderr, "vm: booting %s (kvm=%v, %s, %d cpus); this takes minutes without KVM\n", c.Kernel, kvm, c.Memory, c.CPUs)
	if err := cmd.Start(); err != nil {
		return err
	}
	root, err := NewProcRef(cmd.Process.Pid)
	if err != nil {
		killTree(cmd.Process.Pid)
		return err
	}
	if err := os.WriteFile(dir.PIDFile(), []byte(FormatPIDFile([]ProcRef{root})), 0o644); err != nil {
		killTree(root.PID)
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := clk.After(c.Timeout)
	tick := clk.NewTicker(time.Second)
	defer tick.Stop()
	progress := clk.Monotonic()
	start := progress
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("the VM ended while booting (%w); console:\n%s", err, tail(dir.ConsoleLog(), 20))
		case <-ctx.Done():
			killProcs(dir, root)
			return fmt.Errorf("cancelled: %w", ctx.Err())
		case <-deadline:
			killProcs(dir, root)
			return fmt.Errorf("the guest was not ready within %v; console:\n%s", c.Timeout, tail(dir.ConsoleLog(), 20))
		case <-tick.C():
		}
		if _, err := os.Stat(dir.ReadyFile()); err == nil {
			// remember QEMU too: it is the process that holds the memory, and killing the tree
			// from the root alone would miss it once an intermediate process is gone
			refs := []ProcRef{root}
			for _, pid := range descendants(root.PID) {
				if comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm"); err == nil && strings.HasPrefix(string(comm), "qemu") {
					if r, err := NewProcRef(pid); err == nil {
						refs = append(refs, r)
					}
				}
			}
			_ = os.WriteFile(dir.PIDFile(), []byte(FormatPIDFile(refs)), 0o644)
			fmt.Fprintf(c.Stderr, "vm: ready after %v in %s\n", (clk.Monotonic() - start).Round(time.Second), dir)
			return nil
		}
		if clk.Monotonic()-progress >= time.Minute {
			progress = clk.Monotonic()
			fmt.Fprintf(c.Stderr, "vm: still booting (%v)\n", (clk.Monotonic() - start).Round(time.Second))
		}
	}
}

// tail returns the last n lines of a file.
func tail(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "(no console log)"
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// killProcs kills the recorded processes of the VM, each only while it is still the process that
// was recorded, and the tree below the root.
func killProcs(dir VMDir, root ProcRef) {
	refs := []ProcRef{root}
	if raw, err := os.ReadFile(dir.PIDFile()); err == nil {
		if parsed, err := ParsePIDFile(string(raw)); err == nil && len(parsed) > 0 {
			refs = parsed
		}
	}
	if refs[0].Alive() {
		killTree(refs[0].PID)
	}
	for _, r := range refs[1:] {
		if r.Alive() {
			_ = syscall.Kill(r.PID, syscall.SIGKILL)
		}
	}
}

// ExecConfig describes one command to run in the guest.
type ExecConfig struct {
	Dir VMDir
	// Command is the command line: a single element is a shell command line, several are an
	// argument vector (each quoted).
	Command []string
	// Cwd is the directory the command starts in; the guest sees the host's file system.
	Cwd string
	// Timeout kills the command in the guest when it runs longer.
	Timeout time.Duration
	Stdout  io.Writer
	// Clock defaults to the real clock.
	Clock clock.Clock
}

// commandLine turns the command into one shell command line.
func commandLine(cmd []string) string {
	if len(cmd) == 1 {
		return cmd[0]
	}
	q := make([]string, len(cmd))
	for i, a := range cmd {
		q[i] = ShellQuote(a)
	}
	return strings.Join(q, " ")
}

// ExecScript returns the job script that runs the command line in cwd with a time limit.
func ExecScript(cwd, line string, timeout time.Duration) string {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "export PATH=%s\n", guestPath)
	fmt.Fprintf(&b, "cd %s 2>/dev/null || cd /\n", ShellQuote(cwd))
	fmt.Fprintf(&b, "exec timeout -s KILL %d sh -c %s\n", secs, ShellQuote(line))
	return b.String()
}

// Exec runs a command in the guest and returns its exit code.
func Exec(ctx context.Context, c ExecConfig) (int, error) {
	if len(c.Command) == 0 {
		return 0, errors.New("no command given")
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Minute
	}
	if c.Stdout == nil {
		c.Stdout = io.Discard
	}
	if _, err := c.Dir.requireUp(); err != nil {
		return 0, err
	}
	cwd := c.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	clk := realClock(c.Clock)
	id := nextJobID(clk)
	if err := WriteJob(c.Dir.Queue(), id, ExecScript(cwd, commandLine(c.Command), c.Timeout)); err != nil {
		return 0, err
	}
	// the guest kills the command at the timeout; the host waits a little longer than that, plus
	// whatever other jobs are still ahead in the queue
	waitCtx, cancel := context.WithTimeout(ctx, c.Timeout+time.Minute)
	defer cancel()
	rc, err := WaitJob(waitCtx, clk, c.Dir.Queue(), id, c.Stdout, 200*time.Millisecond)
	if err != nil {
		return 0, fmt.Errorf("job %s: %w (it may still run in the guest; see `testvm vm status`)", id, err)
	}
	removeJob(c.Dir.Queue(), id)
	return rc, nil
}

// Build resolves the packages whose tests need the testbed, and compiles their test binaries into
// c.WorkDir/bin. The one-shot runner and the persistent VM share it.
func Build(ctx context.Context, c Config) ([]Unit, error) {
	units, err := TestbedPackages(ctx, c.Dir, c.Tags, c.Packages)
	if err != nil {
		return nil, err
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("no package with tests under the tags %v matches %v", c.Tags, c.Packages)
	}
	fmt.Fprintf(c.Stderr, "vmrun: building %d test binaries\n", len(units))
	if err := buildUnits(ctx, c, units); err != nil {
		return nil, err
	}
	return units, nil
}

// RunInVM builds the test binaries on the host, runs them in the persistent VM as one job and
// collects the results the way a one-shot run does. The work directory of the run lies inside the
// VM's directory, which the guest shares; it is removed after a passing run unless Keep is set.
func RunInVM(ctx context.Context, dir VMDir, c Config) (Summary, error) {
	c.defaults()
	st, err := dir.requireUp()
	if err != nil {
		return Summary{}, err
	}
	clk := realClock(c.Clock)
	id := nextJobID(clk)
	runDir := filepath.Join(dir.Runs(), id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return Summary{}, err
	}
	c.WorkDir = runDir
	units, err := Build(ctx, c)
	if err != nil {
		_ = os.RemoveAll(runDir)
		return Summary{}, err
	}
	script := GuestScript(GuestOptions{
		WorkDir: runDir, Emulated: st.Meta.Emulated(), Run: c.Run, TestTimeout: c.TestTimeout.String(),
	}, units)
	guest := filepath.Join(runDir, "guest.sh")
	if err := os.WriteFile(guest, []byte(script), 0o755); err != nil {
		return Summary{}, err
	}
	if err := WriteJob(dir.Queue(), id, "#!/bin/sh\nexec sh "+ShellQuote(guest)+"\n"); err != nil {
		return Summary{}, err
	}
	fmt.Fprintf(c.Stderr, "vmrun: running in the VM (job %s)\n", id)
	waitCtx, cancel := context.WithTimeout(ctx, c.VMTimeout)
	defer cancel()
	_, waitErr := WaitJob(waitCtx, clk, dir.Queue(), id, c.Stdout, 200*time.Millisecond)
	summary, err := Collect(os.DirFS(runDir), units)
	summary.AllowSkip = c.AllowSkip
	if err != nil {
		return summary, err
	}
	if waitErr != nil {
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return summary, fmt.Errorf("the run did not finish within %v (it may still run in the VM)", c.VMTimeout)
		}
		return summary, fmt.Errorf("cancelled: %w", waitErr)
	}
	removeJob(dir.Queue(), id)
	if !c.Keep && summary.OK() {
		_ = os.RemoveAll(runDir)
	} else {
		fmt.Fprintf(c.Stderr, "vmrun: the run directory is kept for inspection: %s\n", runDir)
	}
	return summary, nil
}

// Down stops the persistent VM: it asks the guest loop to end (the VM then powers off) and waits
// for the processes, and kills the recorded ones if they do not go. now skips the polite part.
// Only processes that are still the recorded ones are killed. It reports what it did.
func Down(ctx context.Context, clk clock.Clock, dir VMDir, now bool, grace time.Duration, out io.Writer) error {
	clk = realClock(clk)
	st, err := dir.Status()
	if err != nil {
		return err
	}
	if st.State == StateDown {
		fmt.Fprintf(out, "vm: no VM recorded in %s\n", dir)
		return nil
	}
	if !now && st.State == StateUp {
		if err := os.WriteFile(dir.DownFile(), []byte("down\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "vm: asking the guest to power off (up to %v)\n", grace)
		deadline := clk.Monotonic() + grace
		for clk.Monotonic() < deadline && st.Procs[0].Alive() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-clk.After(500 * time.Millisecond):
			}
		}
	}
	// whatever is still alive of the recorded processes (QEMU can outlive the process above it)
	left := false
	for _, p := range st.Procs {
		left = left || p.Alive()
	}
	if left {
		fmt.Fprintln(out, "vm: killing the VM processes")
		killProcs(dir, st.Procs[0])
		for i := 0; i < 30; i++ {
			left = false
			for _, p := range st.Procs {
				left = left || p.Alive()
			}
			if !left {
				break
			}
			clk.Sleep(100 * time.Millisecond)
		}
	}
	for _, f := range []string{"pids", "ready", "down", "meta.json"} {
		_ = os.Remove(filepath.Join(string(dir), f))
	}
	_ = os.RemoveAll(dir.Queue())
	fmt.Fprintf(out, "vm: down\n")
	return nil
}
