package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "chaosctl ") {
			t.Fatalf("%v: code %d, stdout %q, stderr %q", args, code, out.String(), errb.String())
		}
	}
}

func TestAnUnknownCommandIsAnError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 || !strings.Contains(errb.String(), "frobnicate") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}

func TestHelpListsTheCommands(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	for _, c := range []string{"profile", "fault", "overlay", "reset", "run", "abort", "wait", "with", "wg", "version"} {
		if !strings.Contains(out.String(), c) {
			t.Errorf("the help does not name %q:\n%s", c, out.String())
		}
	}
}
