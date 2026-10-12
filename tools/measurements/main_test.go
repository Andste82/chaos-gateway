package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const line = `{"test":"TestA","assertion":"A to the server","flow":"A to the server","direction":"upload","quantity":"median delay","unit":"ms","kernel":"6.8.0-142-generic","accurate":true,"n":2000,"configured":100,"value":%s,"low":93,"high":107,"attempt":%s,"pass":%s,"time":"%s"}` + "\n"

// lineOf fills the four %s of line in order.
func lineOf(value, attempt, pass, ts string) string {
	parts := strings.SplitN(line, "%s", 5)
	return parts[0] + value + parts[1] + attempt + parts[2] + pass + parts[3] + ts + parts[4]
}

// The records of several nights side by side: one line per run and check, oldest first, the last attempt of
// a run, the ratio to the configured value and the position between the bounds.
func TestTheNightsArePrintedOldestFirstWithTheLastAttempt(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "2026-10-12", "measurements-6.8.0-142-generic", "measurements.jsonl"),
		lineOf("108", "1", "false", "2026-10-12T02:40:00Z")+lineOf("101", "2", "true", "2026-10-12T02:41:00Z"))
	write(t, filepath.Join(dir, "2026-10-11", "measurements-6.8.0-142-generic", "measurements.jsonl"),
		lineOf("100", "1", "true", "2026-10-11T02:40:00Z"))
	var out, errb bytes.Buffer
	if code := run([]string{dir}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	got := out.String()
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "TestA / A to the server / upload / median delay (ms), kernel 6.8.0-142-generic") {
		t.Fatalf("report:\n%s", got)
	}
	if !strings.Contains(lines[1], "2026-10-11") || !strings.Contains(lines[1], "ratio 1.000") || !strings.Contains(lines[1], "position 50%") {
		t.Errorf("the first night: %s", lines[1])
	}
	if !strings.Contains(lines[2], "2026-10-12") || !strings.Contains(lines[2], "value 101") || !strings.Contains(lines[2], "attempt 2 pass") {
		t.Errorf("the second night shows its last attempt: %s", lines[2])
	}

	out.Reset()
	if code := run([]string{"-tsv", "-match", "download", dir}, &out, &errb); code != 0 || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("a filter that matches nothing leaves the header: %d %q", code, out.String())
	}
	if code := run([]string{filepath.Join(dir, "none")}, &out, &errb); code != 2 {
		t.Errorf("a missing file: exit %d", code)
	}
	empty := filepath.Join(dir, "empty.jsonl")
	write(t, empty, "")
	if code := run([]string{empty}, &out, &errb); code != 1 {
		t.Errorf("a record without lines: exit %d", code)
	}
}
