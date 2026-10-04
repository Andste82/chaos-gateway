package main

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/secrets"

	"go.uber.org/goleak"
)

// The commands start the executor, the engine and their goroutines: none may outlive a test.
func TestMain(m *testing.M) {
	auth.RefreshInterval = time.Millisecond // M5-09: don't sleep real time for a cross-process test
	goleak.VerifyTestMain(m)
}

func runCmd(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-version"} {
		code, out, _ := runCmd(arg)
		if code != 0 || !strings.HasPrefix(out, "chaosgw ") {
			t.Errorf("%s: code %d, output %q", arg, code, out)
		}
	}
}

func TestNoArgumentsPrintsUsageAndFails(t *testing.T) {
	code, _, errOut := runCmd()
	if code != 2 || !strings.Contains(errOut, "usage: chaosgw") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestHelpListsEverySubcommandWithItsMilestone(t *testing.T) {
	code, out, _ := runCmd("help")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"api", "M5", "exec", "M3", "apply", "M4", "dns", "M6b", "tls", "M21"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

func TestSubcommandsAreNotImplementedYet(t *testing.T) {
	for _, s := range subcommands {
		if s.main != nil {
			continue
		}
		code, out, errOut := runCmd(s.name)
		if code != 2 || out != "" || !strings.Contains(errOut, "not implemented in this build (milestone "+s.milestone+")") {
			t.Errorf("%s: code %d, stdout %q, stderr %q", s.name, code, out, errOut)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runCmd("frobnicate")
	if code != 2 || !strings.Contains(errOut, `unknown command "frobnicate"`) {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestExecHealthReportsAMissingExecutor(t *testing.T) {
	code, out, errOut := runCmd("exec", "-health", "-socket", t.TempDir()+"/none.sock")
	if code != 1 || out != "" || !strings.Contains(errOut, "executor unhealthy") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestExecRefusesArgumentsAndUnknownFlags(t *testing.T) {
	if code, _, _ := runCmd("exec", "extra"); code != 2 {
		t.Errorf("positional argument: code %d", code)
	}
	if code, _, _ := runCmd("exec", "-bogus"); code != 2 {
		t.Errorf("unknown flag: code %d", code)
	}
	if code, _, errOut := runCmd("exec", "-allow-uid", "root"); code != 2 || !strings.Contains(errOut, "invalid uid") {
		t.Errorf("bad uid: code %d, %q", code, errOut)
	}
}

// The whole executor process: it starts, answers the health check and stops on SIGTERM.
func TestExecStartsServesAndStopsOnSIGTERM(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the executor refuses to run unprivileged")
	}
	dir, err := os.MkdirTemp("", "cgx")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sock := dir + "/e.sock"
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"exec", "-socket", sock, "-state", dir + "/state.json"}, &out, &errb) }()

	var code int
	var health, herr string
	for i := 0; i < 100; i++ {
		var o, e bytes.Buffer
		code = run([]string{"exec", "-health", "-socket", sock}, &o, &e)
		health, herr = o.String(), e.String()
		if code == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code != 0 || !strings.Contains(health, "ok protocol=1 generation=0") {
		t.Fatalf("health: code %d, %q %q\nexecutor log: %s", code, health, herr, errb.String())
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-done:
		if c != 0 {
			t.Fatalf("exit code %d: %s", c, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the executor did not stop on SIGTERM")
	}
}

func TestTheExecutorStartsBeforeTheSecretsExist(t *testing.T) {
	dir := t.TempDir() // an empty volume: the API has not created anything yet
	keys := lazyKeys(dir)
	id := "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	if _, _, err := keys(id); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("%v", err)
	}
	// the API creates the store and a key; the same provider finds it
	sec, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sec.PutWireGuard(id, secrets.WireGuardKeys{PrivateKey: "priv", PresharedKey: "psk"}); err != nil {
		t.Fatal(err)
	}
	if priv, psk, err := keys(id); err != nil || priv != "priv" || psk != "psk" {
		t.Errorf("%q %q %v", priv, psk, err)
	}
}
