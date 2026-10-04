package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const birdText = "router id 10.10.0.1;\nprotocol device { }\n"

func birdOp(action, text string) string {
	b, _ := json.Marshal(map[string]any{"type": "bird", "action": action, "instance": "chaosgw", "config": text})
	return string(b)
}

func TestDecodeBirdRejects(t *testing.T) {
	for name, in := range map[string]string{
		"unknown action":      `{"type":"bird","action":"restart","instance":"chaosgw","config":"x"}`,
		"bad instance":        `{"type":"bird","action":"check","instance":"../x","config":"x"}`,
		"no config":           `{"type":"bird","action":"check","instance":"chaosgw"}`,
		"a namespace":         `{"type":"bird","action":"check","instance":"chaosgw","config":"x","ns":"gw"}`,
		"include":             birdOp("apply", "include \"/etc/shadow\";\n"),
		"define hides table":  birdOp("apply", "define T = 254;\nprotocol kernel k { kernel table T; }\n"),
		"log to a file":       birdOp("apply", "log \"/etc/x\" all;\n"),
		"mrtdump":             birdOp("apply", "mrtdump \"/x\";\n"),
		"foreign table":       birdOp("apply", "protocol kernel { kernel table 254; ipv4; }\n"),
		"import table in own": `{"type":"bird","action":"check","instance":"chaosgw","config":"x","import_tables":[100]}`,
		"import table 0":      `{"type":"bird","action":"check","instance":"chaosgw","config":"x","import_tables":[0]}`,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Decode([]byte(birdOp("apply", birdText))); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode([]byte(`{"type":"read","what":"bird","instance":"../x"}`)); err == nil {
		t.Error("a read with a bad instance")
	}
	if _, err := Decode([]byte(`{"type":"read","what":"routes","instance":"chaosgw"}`)); err == nil {
		t.Error("an instance on another read")
	}
}

func TestAFailedReconfigureRestoresTheFile(t *testing.T) {
	e, _, dir := birdExec(t, func(c Command) (Result, error) {
		// the probe (show protocols all) finds BIRD running; configure specifically is rejected
		if c.Tool == ToolBirdc && c.Args[len(c.Args)-1] == "configure" {
			return Result{Exit: 1, Stderr: "Unable to connect\n"}, nil
		}
		return Result{}, nil
	})
	f := filepath.Join(dir, "chaosgw.conf")
	_ = os.WriteFile(f, []byte("old"), 0o644)
	if _, err := e.Do(context.Background(), mustDecode(t, birdOp("apply", birdText))); err == nil {
		t.Fatal("no error")
	}
	if b, _ := os.ReadFile(f); string(b) != "old" {
		t.Errorf("the file says something the daemon does not run: %q", b)
	}
	_ = os.Remove(f)
	_, _ = e.Do(context.Background(), mustDecode(t, birdOp("apply", birdText)))
	if _, err := os.Stat(f); err == nil {
		t.Error("a file without a daemon behind it stays")
	}
}

func TestBirdCheckMutatesNothingAndApplyDoes(t *testing.T) {
	c, _ := Decode([]byte(birdOp("check", birdText)))
	a, _ := Decode([]byte(birdOp("apply", birdText)))
	if c.Mutates() || !a.Mutates() {
		t.Errorf("check %v apply %v", c.Mutates(), a.Mutates())
	}
}

func birdExec(t *testing.T, respond func(Command) (Result, error)) (*Executor, *fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	fr := &fakeRunner{respond: respond}
	return newExec(t, fr, WithBirdDir(dir)), fr, dir
}

func TestBirdApplyParsesThenWritesThenReconfigures(t *testing.T) {
	e, fr, dir := birdExec(t, func(c Command) (Result, error) {
		if c.Tool == ToolBirdc {
			return Result{Stdout: "Reconfigured\n"}, nil
		}
		return Result{}, nil
	})
	if _, err := e.Do(context.Background(), mustDecode(t, birdOp("apply", birdText))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "chaosgw.conf"))
	if err != nil || string(got) != birdText {
		t.Fatalf("%q %v", got, err)
	}
	cmds := fr.commands()
	if len(cmds) != 3 || cmds[0].Tool != ToolBird || cmds[0].Args[0] != "-p" ||
		cmds[1].Tool != ToolBirdc || cmds[1].Args[len(cmds[1].Args)-1] != "all" || // the liveness probe
		cmds[2].Tool != ToolBirdc || cmds[2].Args[len(cmds[2].Args)-1] != "configure" {
		t.Fatalf("%+v", cmds)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".check-*")); len(left) != 0 {
		t.Errorf("temporary files stay: %v", left)
	}
}

func TestARejectedConfigurationNeverReplacesTheRunningOne(t *testing.T) {
	e, _, dir := birdExec(t, func(c Command) (Result, error) {
		if c.Tool == ToolBird {
			return Result{Exit: 1, Stderr: c.Args[2] + ":3:5 syntax error, unexpected '}'\n"}, nil
		}
		return Result{}, nil
	})
	old := filepath.Join(dir, "chaosgw.conf")
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(context.Background(), mustDecode(t, birdOp("apply", birdText)))
	var be *BirdError
	if err == nil || !strings.Contains(err.Error(), "syntax error") || strings.Contains(err.Error(), dir) {
		t.Fatalf("BIRD's message is reported without our temporary path: %v", err)
	}
	_ = be
	if b, _ := os.ReadFile(old); string(b) != "old" {
		t.Errorf("the file was replaced: %q", b)
	}
}

func TestBirdCheckLeavesTheFilesAlone(t *testing.T) {
	e, fr, dir := birdExec(t, nil)
	if _, err := e.Do(context.Background(), mustDecode(t, birdOp("check", birdText))); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%v", entries)
	}
	if len(fr.commands()) != 1 {
		t.Errorf("%+v", fr.commands())
	}
}

func TestBirdReconfigureFailureIsReported(t *testing.T) {
	e, _, _ := birdExec(t, func(c Command) (Result, error) {
		// the probe finds BIRD running; configure itself is rejected with a message of its own
		if c.Tool == ToolBirdc && c.Args[len(c.Args)-1] == "configure" {
			return Result{Exit: 1, Stderr: "Unable to connect to server control socket\n"}, nil
		}
		return Result{}, nil
	})
	_, err := e.Do(context.Background(), mustDecode(t, birdOp("apply", birdText)))
	if err == nil || !strings.Contains(err.Error(), "control socket") {
		t.Fatal(err)
	}
}

// M4c-02 test: when BIRD itself is not reachable (the daemon down, not a rejected configuration),
// the new file stays in place (BIRD reads it at its own start) and the executor reports a typed
// BirdDownError instead of restoring the previous file.
func TestRunBirdReturnsBirdDownErrorWhenTheDaemonIsUnreachable(t *testing.T) {
	e, _, dir := birdExec(t, func(c Command) (Result, error) {
		if c.Tool == ToolBirdc {
			return Result{Exit: 1, Stderr: "birdc: Unable to connect to server control socket: No such file or directory\n"}, nil
		}
		return Result{}, nil
	})
	f := filepath.Join(dir, "chaosgw.conf")
	if err := os.WriteFile(f, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(context.Background(), mustDecode(t, birdOp("apply", birdText)))
	var down *BirdDownError
	if !errors.As(err, &down) {
		t.Fatalf("got %v, want a BirdDownError", err)
	}
	if b, _ := os.ReadFile(f); string(b) != birdText {
		t.Errorf("the new file was not kept: %q", b)
	}
}

func TestBirdNeedsADirectory(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	_, err := e.Do(context.Background(), mustDecode(t, birdOp("check", birdText)))
	if err == nil || !errors.Is(err, ErrNoBirdDir) {
		t.Fatal(err)
	}
}

func TestReadBird(t *testing.T) {
	e, _, dir := birdExec(t, func(c Command) (Result, error) {
		return Result{Stdout: "BIRD 2.18 ready.\nName       Proto      Table      State  Since         Info\nbgp_rb     BGP        ---        up     2026-10-02    Established\n  BGP state:          Established\n    Neighbor address: 10.255.0.1\n"}, nil
	})
	_ = os.WriteFile(filepath.Join(dir, "chaosgw.conf"), []byte("x"), 0o644)
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"bird","instance":"chaosgw"}`))
	if err != nil {
		t.Fatal(err)
	}
	var st BirdState
	if err := json.Unmarshal(out.Data[0], &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.ConfigHash == "" || len(st.Protocols) != 1 || !st.Protocols[0].Established() {
		t.Fatalf("%+v", st)
	}
}

func TestEnsureBirdConfigWritesOnlyWhenThereIsNone(t *testing.T) {
	e, _, dir := birdExec(t, nil)
	if err := e.EnsureBirdConfig("chaosgw"); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "chaosgw.conf")
	if b, _ := os.ReadFile(f); !strings.Contains(string(b), "router id") {
		t.Fatalf("%q", b)
	}
	_ = os.WriteFile(f, []byte("mine"), 0o644)
	_ = e.EnsureBirdConfig("chaosgw")
	if b, _ := os.ReadFile(f); string(b) != "mine" {
		t.Errorf("an existing file was replaced: %q", b)
	}
}
