package vmrun

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/preflight"
)

func TestUnitNameIsFileSafeAndUnique(t *testing.T) {
	got := UnitName("github.com/Andste82/chaos-gateway/internal/testbed")
	if !strings.HasPrefix(got, "github.com_Andste82_chaos-gateway_internal_testbed-") || len(got) != len("github.com_Andste82_chaos-gateway_internal_testbed-")+8 {
		t.Errorf("UnitName = %q", got)
	}
	for _, in := range []string{"a b/c", "///", "x/../y", strings.Repeat("long/", 50)} {
		n := UnitName(in)
		if strings.ContainsAny(n, "/ \x00") || n == "" || len(n) > 90 {
			t.Errorf("UnitName(%q) = %q is not file safe", in, n)
		}
	}
	// the readable part collides, the names must not
	if UnitName("a/b") == UnitName("a_b") {
		t.Error("a/b and a_b must get different names")
	}
	first, again := UnitName("a/b"), UnitName("a/b")
	if first != again {
		t.Error("names must be stable")
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"plain":      "'plain'",
		"with space": "'with space'",
		"it's":       `'it'\''s'`,
		"":           "''",
		"$(rm -rf)":  "'$(rm -rf)'",
	}
	for in, want := range tests {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func units() []Unit {
	return []Unit{
		{Name: "a", ImportPath: "example.com/a", Dir: "/src/a", Binary: "/work/bin/a.test"},
		{Name: "b", ImportPath: "example.com/b b", Dir: "/src/it's", Binary: "/work/bin/b.test"},
	}
}

func TestGuestScriptRunsEveryUnitInItsDirectory(t *testing.T) {
	s := GuestScript(GuestOptions{WorkDir: "/work", TestTimeout: "20m0s"}, units())
	for _, want := range []string{
		"( cd '/src/a' && '/work/bin/a.test' -test.v -test.count=1 -test.timeout='20m0s' ) > '/work/results/a.out' 2>&1",
		`( cd '/src/it'\''s' && '/work/bin/b.test'`,
		"echo $rc > '/work/results/a.exit'",
		"echo $rc > '/work/results/b.exit'",
		"echo '=== example.com/b b'",
		"mkdir -p '/work/results'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("guest script lacks %q:\n%s", want, s)
		}
	}
}

func TestGuestScriptLoadsTheSharedModuleList(t *testing.T) {
	s := GuestScript(GuestOptions{WorkDir: "/work"}, units())
	var line string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "modprobe ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no modprobe line:\n%s", s)
	}
	for _, m := range preflight.Modules() {
		if !strings.Contains(line, " "+m.Name) {
			t.Errorf("modprobe line lacks %s", m.Name)
		}
	}
	if !strings.HasSuffix(line, "|| true") {
		t.Errorf("a failing modprobe must not abort the script: %s", line)
	}
}

func TestGuestScriptMarksEmulation(t *testing.T) {
	if s := GuestScript(GuestOptions{WorkDir: "/w", Emulated: true}, units()); !strings.Contains(s, "export CHAOSGW_TESTBED_EMULATED=1\n") {
		t.Errorf("emulated run lacks the flag:\n%s", s)
	}
	if s := GuestScript(GuestOptions{WorkDir: "/w"}, units()); strings.Contains(s, "CHAOSGW_TESTBED_EMULATED") {
		t.Errorf("native run must not set the flag:\n%s", s)
	}
}

func TestGuestScriptRunFilterIsQuoted(t *testing.T) {
	s := GuestScript(GuestOptions{WorkDir: "/w", Run: "TestA|Test'B"}, units())
	if !strings.Contains(s, `-test.run='TestA|Test'\''B'`) {
		t.Errorf("the -run expression must be quoted:\n%s", s)
	}
	if s := GuestScript(GuestOptions{WorkDir: "/w"}, units()); strings.Contains(s, "-test.run") {
		t.Error("no -run flag without a filter")
	}
}

func TestGuestScriptEndsWithTheCompletionMarkerAndAggregatesStatus(t *testing.T) {
	s := GuestScript(GuestOptions{WorkDir: "/work"}, units())
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if got := lines[len(lines)-1]; got != "exit $status" {
		t.Errorf("last line %q", got)
	}
	if got := lines[len(lines)-2]; got != "sync" {
		t.Errorf("results must be flushed before the VM goes down: %q", got)
	}
	if !strings.Contains(s, "echo done > '/work/results/done'") {
		t.Errorf("no completion marker:\n%s", s)
	}
	if strings.Count(s, "status=1") != len(units()) {
		t.Errorf("each unit must be able to fail the run:\n%s", s)
	}
}
