package main

import (
	"bytes"
	"strings"
	"testing"
)

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
	for _, want := range []string{"api", "M5", "exec", "M3", "dns", "M6b", "tls", "M21"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

func TestSubcommandsAreNotImplementedYet(t *testing.T) {
	for _, s := range subcommands {
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
