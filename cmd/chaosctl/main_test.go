package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "chaosctl ") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestOtherCommandsAreNotImplementedYet(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"run"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "M18") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
}
