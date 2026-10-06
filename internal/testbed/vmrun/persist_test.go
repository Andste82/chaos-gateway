package vmrun

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/preflight"
)

// statLine builds a /proc/<pid>/stat line with the given command name, state and start time.
func statLine(comm string, state byte, start uint64) string {
	// fields 4..21 are numbers, field 22 is the start time, then 30 more numbers
	var f []string
	for i := 4; i <= 21; i++ {
		f = append(f, "0")
	}
	f = append(f, fmt.Sprint(start))
	for i := 23; i <= 52; i++ {
		f = append(f, "0")
	}
	return fmt.Sprintf("1234 (%s) %c %s\n", comm, state, strings.Join(f, " "))
}

func TestParseProcStatFindsTheStartTimeEvenWithAnOddCommandName(t *testing.T) {
	for _, comm := range []string{"sleep", "a b", "we) (ird", "(((", "x y) S 1 2 3"} {
		state, start, err := parseProcStat(statLine(comm, 'S', 987654))
		if err != nil || state != 'S' || start != 987654 {
			t.Errorf("comm %q: state %c start %d err %v", comm, state, start, err)
		}
	}
	for _, bad := range []string{"", "no parenthesis here", "1 (x) S 1 2"} {
		if _, _, err := parseProcStat(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// liveProc starts a process that lives for the test and returns its reference. The tests never
// use their own process for this: under qemu-user (the arm64 job) /proc/<own pid>/stat is
// synthesized by the emulator, not read from the kernel, so only a child shows the real values.
func liveProc(t *testing.T) ProcRef {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ref, err := NewProcRef(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestAProcRefIsAliveOnlyAsTheSameProcess(t *testing.T) {
	self := liveProc(t)
	if !self.Alive() {
		t.Error("a running process must be alive")
	}
	// the same pid with another start time is a different process: a reused pid
	if (ProcRef{PID: self.PID, Start: self.Start + 1}).Alive() {
		t.Error("a pid with a different start time must not count as the recorded process")
	}
	if (ProcRef{PID: 1 << 30, Start: 1}).Alive() {
		t.Error("a pid that does not exist must not be alive")
	}
}

func TestAZombieIsNotAlive(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ref, err := NewProcRef(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	// not waited for: it stays a zombie until Wait
	deadline := time.Now().Add(5 * time.Second)
	for ref.Alive() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if ref.Alive() {
		t.Error("an exited, unreaped process must not count as alive")
	}
	_ = cmd.Wait()
}

func TestThePIDFileRoundTrips(t *testing.T) {
	refs := []ProcRef{{PID: 10, Start: 111}, {PID: 22, Start: 5}}
	got, err := ParsePIDFile(FormatPIDFile(refs))
	if err != nil || len(got) != 2 || got[0] != refs[0] || got[1] != refs[1] {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := ParsePIDFile(""); err != nil || len(got) != 0 {
		t.Errorf("an empty file holds no processes: %v %v", got, err)
	}
	for _, bad := range []string{"10\n", "a b\n", "1 2 3\n", "1 -2\n"} {
		if _, err := ParsePIDFile(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func modprobeLine(t *testing.T, script string) string {
	t.Helper()
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(l, "modprobe ") {
			return l
		}
	}
	t.Fatalf("no modprobe line in:\n%s", script)
	return ""
}

func TestTheServeScriptLoadsExactlyTheModulesOfThePreflight(t *testing.T) {
	serve := ServeScript(ServeOptions{Dir: "/vm"})
	line := modprobeLine(t, serve)
	for _, m := range preflight.Modules() {
		if !strings.Contains(line, " "+m.Name) {
			t.Errorf("module %s missing from %q", m.Name, line)
		}
	}
	// the same line as the one-shot guest script: the two must never drift apart
	if want := modprobeLine(t, GuestScript(GuestOptions{WorkDir: "/w"}, nil)); line != want {
		t.Errorf("serve script %q differs from the guest script %q", line, want)
	}
}

func TestTheServeScriptAnnouncesReadyAndStopsOnTheDownMarker(t *testing.T) {
	s := ServeScript(ServeOptions{Dir: "/it's vm", Emulated: true})
	for _, want := range []string{
		"export CHAOSGW_TESTBED_EMULATED=1",
		"W='/it'\\''s vm'",
		`uname -r > "$W/ready.tmp" && mv "$W/ready.tmp" "$W/ready"`,
		`while [ ! -e "$W/down" ]; do`,
		`mv "$f" "$Q/$id.run"`,
		`mv "$Q/$id.rc.tmp" "$Q/$id.rc"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("serve script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(ServeScript(ServeOptions{Dir: "/vm"}), "CHAOSGW_TESTBED_EMULATED") {
		t.Error("a VM with KVM runs the tests with real timing")
	}
	shSyntax(t, s)
}

// shSyntax fails the test when sh -n rejects the script.
func shSyntax(t *testing.T, script string) {
	t.Helper()
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v: %s\n%s", err, out, script)
	}
}

func TestJobIDsSortInCreationOrderAndStayUnique(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 5)
	a := NewJobID(t0, 42, 1)
	b := NewJobID(t0.Add(time.Nanosecond), 42, 2)
	c := NewJobID(t0.Add(time.Hour), 7, 1)
	if a >= b || b >= c {
		t.Errorf("ids do not sort in creation order: %s %s %s", a, b, c)
	}
	// the same instant from two processes or two calls must still differ
	if NewJobID(t0, 42, 1) == NewJobID(t0, 43, 1) || NewJobID(t0, 42, 1) == NewJobID(t0, 42, 2) {
		t.Error("ids must be unique per pid and sequence number")
	}
	clk := clock.NewFake(t0)
	first := nextJobID(clk)
	second := nextJobID(clk) // the clock did not move: the sequence number alone must tell them apart
	if first == second {
		t.Error("two ids created in the same instant are equal")
	}
	if strings.ContainsAny(a, "/ .") {
		t.Errorf("%q is not a plain file name", a)
	}
}

func TestAJobAppearsInOneStepAndOnlyUnderItsFinalName(t *testing.T) {
	q := filepath.Join(t.TempDir(), "q")
	if err := WriteJob(q, "j1", "#!/bin/sh\necho hi\n"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(q)
	if len(entries) != 1 || entries[0].Name() != "j1.job" {
		t.Fatalf("the queue holds %v, want only j1.job (a .tmp must not stay behind)", entries)
	}
	got, _ := os.ReadFile(filepath.Join(q, "j1.job"))
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Errorf("content %q", got)
	}
	if queued, running := QueueStats(q); queued != 1 || running != 0 {
		t.Errorf("stats %d queued %d running", queued, running)
	}
	// a running job: .run without .rc
	_ = os.Rename(filepath.Join(q, "j1.job"), filepath.Join(q, "j1.run"))
	if queued, running := QueueStats(q); queued != 0 || running != 1 {
		t.Errorf("stats %d queued %d running", queued, running)
	}
	_ = os.WriteFile(filepath.Join(q, "j1.rc"), []byte("0\n"), 0o644)
	if queued, running := QueueStats(q); queued != 0 || running != 0 {
		t.Errorf("a finished job is neither queued nor running: %d %d", queued, running)
	}
}

func TestWaitJobStreamsTheOutputAndReturnsTheExitCode(t *testing.T) {
	q := t.TempDir()
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(60 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(q, "j.out"), []byte("first\n"), 0o644)
		time.Sleep(120 * time.Millisecond)
		f, _ := os.OpenFile(filepath.Join(q, "j.out"), os.O_APPEND|os.O_WRONLY, 0o644)
		_, _ = f.WriteString("second\n")
		_ = f.Close()
		_ = os.WriteFile(filepath.Join(q, "j.rc.tmp"), []byte("7\n"), 0o644)
		_ = os.Rename(filepath.Join(q, "j.rc.tmp"), filepath.Join(q, "j.rc"))
	}()
	rc, err := WaitJob(context.Background(), nil, q, "j", &out, 10*time.Millisecond)
	<-done
	if err != nil || rc != 7 {
		t.Fatalf("rc %d err %v", rc, err)
	}
	if out.String() != "first\nsecond\n" {
		t.Errorf("output %q: every byte exactly once, in order", out.String())
	}
}

func TestWaitJobGivesUpWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := WaitJob(ctx, nil, t.TempDir(), "never", &bytes.Buffer{}, 10*time.Millisecond)
	if err == nil || ctx.Err() == nil {
		t.Errorf("a job that never ends must time out, got %v", err)
	}
}

func TestWaitJobRejectsAGarbledExitCode(t *testing.T) {
	q := t.TempDir()
	_ = os.WriteFile(filepath.Join(q, "j.rc"), []byte("abc"), 0o644)
	if _, err := WaitJob(context.Background(), nil, q, "j", &bytes.Buffer{}, time.Millisecond); err == nil {
		t.Error("a garbled exit code must be an error, never read as success")
	}
}

// startServeLoop runs the real serve loop on the host, in dir.
func startServeLoop(t *testing.T, dir string) *exec.Cmd {
	t.Helper()
	script := fmt.Sprintf("W=%s\nQ=\"$W/q\"\nmkdir -p \"$Q\"\n%s", ShellQuote(dir), serveLoop)
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	return cmd
}

func TestTheServeLoopRunsJobsInOrderReportsExitCodesAndStopsOnDown(t *testing.T) {
	dir := t.TempDir()
	q := filepath.Join(dir, "q")
	order := filepath.Join(dir, "order")
	loop := startServeLoop(t, dir)

	// queued before the loop looks: three jobs, the second fails
	ids := []string{"001", "002", "003"}
	if err := os.MkdirAll(q, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		script := fmt.Sprintf("#!/bin/sh\necho %s >> %s\necho out-%s\nexit %d\n", id, ShellQuote(order), id, []int{0, 3, 0}[i])
		if err := WriteJob(q, id, script); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	want := []int{0, 3, 0}
	for i, id := range ids {
		var out bytes.Buffer
		rc, err := WaitJob(ctx, nil, q, id, &out, 20*time.Millisecond)
		if err != nil || rc != want[i] || out.String() != "out-"+id+"\n" {
			t.Errorf("job %s: rc %d out %q err %v", id, rc, out.String(), err)
		}
	}
	if got, _ := os.ReadFile(order); string(got) != "001\n002\n003\n" {
		t.Errorf("jobs ran in the order %q", got)
	}

	// a job that is queued while the loop sleeps is picked up, too
	if err := WriteJob(q, "004", "#!/bin/sh\npwd\n"); err != nil {
		t.Fatal(err)
	}
	var late bytes.Buffer
	if rc, err := WaitJob(ctx, nil, q, "004", &late, 20*time.Millisecond); err != nil || rc != 0 {
		t.Errorf("late job: rc %d err %v", rc, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "down"), []byte("down\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- loop.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("the loop must end cleanly on the down marker: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("the loop did not stop on the down marker")
	}
}

func TestExecScriptRunsTheLineInTheDirectoryWithATimeLimit(t *testing.T) {
	s := ExecScript("/src/it's", "nft list ruleset | head -3", 90*time.Second)
	for _, want := range []string{
		"cd '/src/it'\\''s' 2>/dev/null || cd /",
		"exec timeout -s KILL 90 sh -c 'nft list ruleset | head -3'",
		"export PATH=/usr/local/go/bin",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("exec script lacks %q:\n%s", want, s)
		}
	}
	shSyntax(t, s)
	if !strings.Contains(ExecScript("/", "true", 0), "timeout -s KILL 1 ") {
		t.Error("a zero timeout must still be a limit")
	}
}

func TestACommandIsOneShellLineOrAnArgumentVector(t *testing.T) {
	if got := commandLine([]string{"nft list ruleset | wc -l"}); got != "nft list ruleset | wc -l" {
		t.Errorf("a single argument is a shell line, got %q", got)
	}
	if got := commandLine([]string{"echo", "a b", "it's"}); got != `'echo' 'a b' 'it'\''s'` {
		t.Errorf("several arguments are each quoted, got %q", got)
	}
}

func upDir(t *testing.T, pids []ProcRef, ready bool) VMDir {
	t.Helper()
	d := VMDir(t.TempDir())
	if pids != nil {
		if err := os.WriteFile(d.PIDFile(), []byte(FormatPIDFile(pids)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if ready {
		if err := os.WriteFile(d.ReadyFile(), []byte("6.8.0-142-generic\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestTheStatusFollowsTheFilesAndTheProcess(t *testing.T) {
	self := liveProc(t)

	st, err := upDir(t, nil, false).Status()
	if err != nil || st.State != StateDown {
		t.Errorf("no pids file: %v %v", st.State, err)
	}
	st, _ = upDir(t, []ProcRef{self}, false).Status()
	if st.State != StateStarting {
		t.Errorf("a live process without the ready marker is starting, got %s", st.State)
	}
	d := upDir(t, []ProcRef{self}, true)
	_ = os.MkdirAll(d.Queue(), 0o755)
	_ = WriteJob(d.Queue(), "a", "true")
	_ = os.WriteFile(filepath.Join(d.Queue(), "b.run"), []byte("true"), 0o644)
	st, _ = d.Status()
	if st.State != StateUp || st.Kernel != "6.8.0-142-generic" || st.Queued != 1 || st.Running != 1 {
		t.Errorf("%+v", st)
	}
	// a pid whose start time does not match is not our VM: the files are stale
	st, _ = upDir(t, []ProcRef{{PID: self.PID, Start: self.Start + 5}}, true).Status()
	if st.State != StateStale {
		t.Errorf("a reused pid must read as stale, got %s", st.State)
	}
	st, _ = upDir(t, []ProcRef{{PID: 1 << 30, Start: 1}}, true).Status()
	if st.State != StateStale {
		t.Errorf("a dead pid must read as stale, got %s", st.State)
	}
	if _, err := upDir(t, nil, false).Status(); err != nil {
		t.Error(err)
	}
	bad := upDir(t, nil, false)
	_ = os.WriteFile(bad.PIDFile(), []byte("garbage\n"), 0o644)
	if _, err := bad.Status(); err == nil {
		t.Error("a corrupt pids file must be reported")
	}
}

func TestJobsAreRefusedWithAMessageThatSaysWhatToDo(t *testing.T) {
	self := liveProc(t)
	cases := map[string]VMDir{
		"no VM is up":   upDir(t, nil, false),
		"still booting": upDir(t, []ProcRef{self}, false),
		"stale files":   upDir(t, []ProcRef{{PID: 1 << 30, Start: 1}}, true),
	}
	for name, d := range cases {
		_, err := Exec(context.Background(), ExecConfig{Dir: d, Command: []string{"true"}})
		if err == nil || !strings.Contains(err.Error(), "testvm vm") {
			t.Errorf("%s: want an error that names the testvm command, got %v", name, err)
		}
		if _, err := RunInVM(context.Background(), d, Config{Packages: []string{"./..."}}); err == nil {
			t.Errorf("%s: vm run must be refused", name)
		}
	}
	if _, err := Exec(context.Background(), ExecConfig{Dir: upDir(t, nil, false)}); err == nil {
		t.Error("an empty command must be refused")
	}
}

func TestDownClearsTheFilesOfAStaleVMAndNeverKillsAReusedPID(t *testing.T) {
	// a process that is not ours to kill: it runs, but the recorded start time is wrong
	bystander := exec.Command("sleep", "300")
	if err := bystander.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bystander.Process.Kill(); _ = bystander.Wait() })
	real, _ := NewProcRef(bystander.Process.Pid)

	d := upDir(t, []ProcRef{{PID: real.PID, Start: real.Start + 99}}, true)
	_ = os.MkdirAll(d.Queue(), 0o755)
	var out bytes.Buffer
	if err := Down(context.Background(), nil, d, true, time.Second, &out); err != nil {
		t.Fatal(err)
	}
	if !real.Alive() {
		t.Fatal("down killed a process that only shares the recorded pid")
	}
	for _, f := range []string{d.PIDFile(), d.ReadyFile(), d.Queue()} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s was not removed", f)
		}
	}
	if st, _ := d.Status(); st.State != StateDown {
		t.Errorf("after down the state is %s", st.State)
	}
	if err := Down(context.Background(), nil, d, true, time.Second, &out); err != nil {
		t.Errorf("down twice is fine: %v", err)
	}
}

func TestDownKillsTheRecordedProcesses(t *testing.T) {
	root := exec.Command("sleep", "300")
	root.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := root.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = root.Wait(); close(exited) }()
	t.Cleanup(func() { _ = root.Process.Kill() })
	ref, err := NewProcRef(root.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	d := upDir(t, []ProcRef{ref}, false)
	var out bytes.Buffer
	if err := Down(context.Background(), nil, d, true, time.Second, &out); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the recorded process is still running after down")
	}
	if !strings.Contains(out.String(), "killing") {
		t.Errorf("down should say what it did: %q", out.String())
	}
}

func TestTheScriptArgsBootTheServeScriptInTheSharedDirectory(t *testing.T) {
	c := Config{Kernel: "6.8.0-142-generic", Memory: "2G", CPUs: 2, WorkDir: "/tmp/vm"}
	got := strings.Join(VNGScriptArgs(c, false, "/tmp/vm/serve.sh"), " ")
	for _, want := range []string{"-r 6.8.0-142-generic", "--disable-kvm", "--memory 2G", "--cpus 2", "--rwdir=/tmp/vm=/tmp/vm", "--exec sh '/tmp/vm/serve.sh'"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if strings.Contains(strings.Join(VNGScriptArgs(c, true, "/x"), " "), "--disable-kvm") {
		t.Error("with KVM the VM must not be forced into emulation")
	}
	// the one-shot runner keeps its arguments
	if one := strings.Join(VNGArgs(c, false), " "); !strings.Contains(one, "--exec sh '/tmp/vm/guest.sh'") {
		t.Errorf("one-shot args changed: %s", one)
	}
}

func TestTheDefaultDirectoryCanBeOverridden(t *testing.T) {
	t.Setenv(VMDirEnv, "")
	if DefaultDir() != VMDir(DefaultVMDir) {
		t.Errorf("default %s", DefaultDir())
	}
	t.Setenv(VMDirEnv, "/elsewhere")
	if DefaultDir() != "/elsewhere" {
		t.Errorf("override %s", DefaultDir())
	}
}
