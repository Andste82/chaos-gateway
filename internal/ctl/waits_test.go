package ctl

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func scenarioFile(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "s.yaml")
	if err := os.WriteFile(file, []byte("name: x\ntarget: {device: d}\nsteps: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// within fails the test when f takes longer than limit (the commands under test used to wait for a request of two
// minutes).
func within(t *testing.T, limit time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s took longer than %v", what, limit)
	}
}

// A device wait whose time runs out while the gateway cannot be asked says so with 2, not 1: a script that treats 2 as
// an infrastructure error must not read a dead gateway as a failing device.
func TestWaitDeviceThatRunsOutOfTimeWithoutAnAnswerFromTheGatewayExits2(t *testing.T) {
	// a gateway that is not there, polled less than ten times before the time is up
	dead := httptest.NewServer(nil)
	url := dead.URL
	dead.Close()
	var out, errb bytes.Buffer
	code := Run([]string{"wait", "device", "esp32-42", "--online", "--timeout", "450ms"}, Env{Stdout: &out, Stderr: &errb, Poll: 100 * time.Millisecond,
		Getenv: func(k string) string { return map[string]string{"CHAOSGW_URL": url, "CHAOSGW_TOKEN": "t"}[k] }})
	if code != ExitError || !strings.Contains(errb.String(), "timed out after 450ms, and the gateway could not be asked: ") || strings.HasSuffix(strings.TrimSpace(errb.String()), "timed out after 450ms:") {
		t.Errorf("a dead gateway: %d %q", code, errb.String())
	}
	if strings.Contains(errb.String(), "lost the gateway") {
		t.Errorf("fewer than ten polls failed: %q", errb.String())
	}

	// a gateway that accepts the request and never answers: the time is kept, the poll does not hold it
	f := &fakeGateway{t: t, hangFrom: 1}
	within(t, 10*time.Second, "a wait on a gateway that does not answer", func() {
		start := time.Now()
		code, _, errb := f.runWith(t, func(e *Env) { e.Poll = 100 * time.Millisecond }, nil, "wait", "device", "esp32-42", "--online", "--timeout", "300ms")
		if code != ExitError || !strings.Contains(errb, "could not be asked") {
			t.Errorf("a gateway that hangs: %d %q", code, errb)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("--timeout 300ms took %v", d)
		}
	})

	// a gateway that answered and then hangs: the poll that is cut off by the time had a second to be answered
	f = &fakeGateway{t: t, hangFrom: 2}
	within(t, 10*time.Second, "a wait on a gateway that stops answering", func() {
		code, _, errb := f.runWith(t, func(e *Env) { e.Poll = 100 * time.Millisecond }, nil, "wait", "device", "esp32-42", "--online", "--timeout", "1500ms")
		if code != ExitError || !strings.Contains(errb, "the gateway could not be asked: the poll was not answered") {
			t.Errorf("a gateway that stops answering: %d %q", code, errb)
		}
	})

	// a gateway that answers and does not know the device is a verdict about the device: 1, with what is missing
	f = &fakeGateway{t: t}
	code, _, errb2 := f.runWith(t, func(e *Env) { e.Poll = 50 * time.Millisecond }, nil, "wait", "device", "esp32-42", "--online", "--timeout", "200ms")
	if code != ExitFailed || !strings.Contains(errb2, "timed out after 200ms: the gateway does not know the device") {
		t.Errorf("an answered poll: %d %q", code, errb2)
	}
}

// A poll that hangs is cut off after a few poll intervals and counted as a failed poll; the wait goes on.
func TestAPollThatHangsIsCutOffAndTheWaitGoesOn(t *testing.T) {
	f := &fakeGateway{t: t, hangFrom: 2, hangTo: 2, passAt: 3}
	within(t, 20*time.Second, "wait run with a poll that hangs", func() {
		code, out, errb := f.runWith(t, func(e *Env) { e.PollTimeout = 100 * time.Millisecond }, nil, "wait", "run", "00000000-0000-4000-8000-000000000001")
		if code != 0 || !strings.Contains(out, "PASSED") || !strings.Contains(errb, "the gateway does not answer (1 of 10)") {
			t.Errorf("%d\n%s\n%s", code, out, errb)
		}
	})
}

// The timeout of a run that is waited for aborts it at once, also when the poll of the run is hanging.
func TestTheTimeoutAbortsARunWhileThePollIsHanging(t *testing.T) {
	f := &fakeGateway{t: t, hangFrom: 1}
	within(t, 20*time.Second, "run --wait with a hanging poll", func() {
		start := time.Now()
		code, out, errb := f.runWith(t, nil, nil, "run", "-f", scenarioFile(t), "--wait", "--lease", "0", "--timeout", "300ms")
		if code != ExitError || !strings.Contains(out, "ABORTED") || !strings.Contains(errb, "--timeout 300ms ran out: aborting the run") {
			t.Errorf("%d\n%s\n%s", code, out, errb)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("--timeout 300ms took %v", d)
		}
		if calls := strings.Join(f.calls(), ","); !strings.Contains(calls, "POST /runs/00000000-0000-4000-8000-000000000001/abort") {
			t.Errorf("the run is aborted: %s", calls)
		}
	})
	// the same for a signal
	f = &fakeGateway{t: t, hangFrom: 1}
	sigs := make(chan os.Signal, 1)
	go func() { time.Sleep(200 * time.Millisecond); sigs <- syscall.SIGTERM }()
	within(t, 20*time.Second, "run --wait with a hanging poll and a signal", func() {
		code, _, errb := f.runWith(t, nil, sigs, "run", "-f", scenarioFile(t), "--wait", "--lease", "0")
		if code != ExitError || !strings.Contains(errb, "terminated received: aborting the run") {
			t.Errorf("%d %s", code, errb)
		}
	})
}

// A signal that arrives while the run is being created: the process is not killed, and the run that the request makes
// is aborted. The signal is a real one, to the test process, as a CI agent that gives up sends it (SIGHUP is one of
// those that used to kill chaosctl).
func TestARunIsAbortedWhenAHangUpArrivesWhileItIsBeingCreated(t *testing.T) {
	f := &fakeGateway{t: t, postStarted: make(chan struct{}), postRelease: make(chan struct{})}
	go func() {
		<-f.postStarted
		_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
		time.Sleep(100 * time.Millisecond)
		close(f.postRelease)
	}()
	within(t, 30*time.Second, "run --wait", func() {
		code, out, errb := f.run(t, nil, "run", "-f", scenarioFile(t), "--wait", "--lease", "0")
		if code != ExitError || !strings.Contains(out, "ABORTED") || !strings.Contains(errb, "hangup received: aborting the run") {
			t.Errorf("%d\n%s\n%s", code, out, errb)
		}
		calls := strings.Join(f.calls(), ",")
		if !strings.Contains(calls, "POST /runs/00000000-0000-4000-8000-000000000001/abort") || strings.Contains(calls, "GET /runs/") {
			t.Errorf("the run is aborted without a poll: %s", calls)
		}
	})
}

// SIGHUP gets `with` to clean up like SIGTERM: it is passed on to the command, and the overlays are removed.
func TestWithCleansUpOnAHangUp(t *testing.T) {
	f := &fakeGateway{t: t}
	started := filepath.Join(t.TempDir(), "started")
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
	}()
	within(t, 30*time.Second, "with", func() {
		code, _, errb := f.run(t, nil, "with", "--device", "d", "--blackout", "--", "sh", "-c", "touch "+started+"; exec sleep 20")
		if code != 128+int(syscall.SIGHUP) {
			t.Errorf("%d %s", code, errb)
		}
		if last := f.calls()[len(f.calls())-1]; !strings.HasPrefix(last, "DELETE /overlays/") {
			t.Errorf("the overlay is removed after the command ended: %v", f.calls())
		}
	})
}

// The default lease is for the overlays that have none: the lease of an overlay file stays, an explicit --lease
// replaces it and --lease 0 takes it away.
func TestWithKeepsTheLeaseOfAnOverlayFileAndAnExplicitLeaseReplacesIt(t *testing.T) {
	dir := t.TempDir()
	leased := filepath.Join(dir, "leased.yaml")
	plain := filepath.Join(dir, "plain.yaml")
	if err := os.WriteFile(leased, []byte("target: {device: d}\nfault: {blackout: true}\nlease: 5m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, []byte("target: {device: d}\nfault: {latency: 10ms}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args  []string
		lease []any // of the three requests: the flags' own, leased.yaml, plain.yaml
	}{
		"the default":           {nil, []any{"30s", "5m", "30s"}},
		"an explicit lease":     {[]string{"--lease", "10s"}, []any{"10s", "10s", "10s"}},
		"no lease at all":       {[]string{"--lease", "0"}, []any{nil, nil, nil}},
		"a lease of one second": {[]string{"--lease", "1s"}, []any{"1s", "1s", "1s"}},
	} {
		f := &fakeGateway{t: t}
		args := append([]string{"with", "--device", "d", "--loss", "5%", "--overlay", leased, "--overlay", plain}, tc.args...)
		code, _, errb := f.run(t, nil, append(args, "--", "true")...)
		if code != 0 || len(f.bodies) != 3 {
			t.Errorf("%s: %d %d %s", name, code, len(f.bodies), errb)
			continue
		}
		// --device replaces the target of the files, not their lease
		for i, want := range tc.lease {
			if got := f.bodies[i]["lease"]; got != want {
				t.Errorf("%s: request %d has the lease %v, want %v", name, i, got, want)
			}
		}
	}
	// a lease that cannot be renewed is refused before anything is created
	f := &fakeGateway{t: t}
	if code, _, errb := f.run(t, nil, "with", "--device", "d", "--blackout", "--lease", "500ms", "--", "true"); code != ExitInternal || !strings.Contains(errb, "at least 1s") || len(f.calls()) != 0 {
		t.Errorf("%d %s %v", code, errb, f.calls())
	}
}

// An overlay that is gone when `with` removes it (its TTL ran out during the command) is cleaned up, not a failure:
// the exit code is the command's own, and nothing says "could not remove".
func TestWithTreatsAnOverlayThatIsGoneAlreadyAsRemoved(t *testing.T) {
	for name, tc := range map[string]struct {
		command []string
		code    int
	}{
		"a command that succeeds": {[]string{"true"}, 0},
		"a command that fails":    {[]string{"sh", "-c", "exit 4"}, 4},
	} {
		f := &fakeGateway{t: t, deleteStatus: 404}
		code, _, errb := f.run(t, nil, append([]string{"with", "--device", "d", "--blackout", "--ttl", "1s", "--"}, tc.command...)...)
		if code != tc.code || strings.Contains(errb, "could not remove") {
			t.Errorf("%s: %d %s", name, code, errb)
		}
		if calls := strings.Join(f.calls(), ","); !strings.Contains(calls, "DELETE /overlays/") {
			t.Errorf("%s: the overlay is deleted (it may be gone): %s", name, calls)
		}
	}
}
