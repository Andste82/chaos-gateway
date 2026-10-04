package vmrun

import (
	"strings"
	"testing"
	"testing/fstest"
)

const goTestOutput = `=== RUN   TestA
=== RUN   TestA/sub
--- PASS: TestA/sub (0.00s)
--- PASS: TestA (0.01s)
=== RUN   TestB
--- FAIL: TestB (0.00s)
    --- SKIP: TestB/inner (0.00s)
--- SKIP: TestC (0.00s)
FAIL
`

func TestParseCountsIncludesSubtests(t *testing.T) {
	if got := ParseCounts(goTestOutput); got != (Counts{Passed: 2, Failed: 1, Skipped: 2}) {
		t.Fatalf("counts = %+v", got)
	}
	if got := ParseCounts("no verdicts here\n"); got != (Counts{}) {
		t.Fatalf("counts = %+v", got)
	}
}

func twoUnits() []Unit {
	return []Unit{{Name: "a", ImportPath: "example.com/a"}, {Name: "b", ImportPath: "example.com/b"}}
}

func okFS() fstest.MapFS {
	return fstest.MapFS{
		"results/a.exit": {Data: []byte("0\n")},
		"results/a.out":  {Data: []byte("--- PASS: TestA (0.00s)\nPASS\n")},
		"results/b.exit": {Data: []byte("0\n")},
		"results/b.out":  {Data: []byte("--- PASS: TestB (0.00s)\n--- SKIP: TestC (0.00s)\nPASS\n")},
		"results/done":   {Data: []byte("done\n")},
	}
}

func TestCollectAllPassed(t *testing.T) {
	s, err := Collect(okFS(), twoUnits())
	if err != nil {
		t.Fatal(err)
	}
	s.AllowSkip = true // this fixture's "all passed" data includes one SKIP on purpose
	if !s.OK() || len(s.Problems()) != 0 {
		t.Fatalf("summary not OK: %v", s.Problems())
	}
	if got := s.Totals(); got != (Counts{Passed: 2, Skipped: 1}) {
		t.Fatalf("totals = %+v", got)
	}
	rep := s.Report()
	for _, want := range []string{"ok    example.com/a", "total: 2 passed, 0 failed, 1 skipped"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
}

func TestCollectFailingPackage(t *testing.T) {
	fsys := okFS()
	fsys["results/b.exit"] = &fstest.MapFile{Data: []byte("1\n")}
	fsys["results/b.out"] = &fstest.MapFile{Data: []byte("--- FAIL: TestB (0.00s)\nFAIL\n")}
	s, err := Collect(fsys, twoUnits())
	if err != nil {
		t.Fatal(err)
	}
	if s.OK() {
		t.Fatal("a failing package must fail the run")
	}
	if probs := strings.Join(s.Problems(), "\n"); !strings.Contains(probs, "example.com/b: exit code 1") {
		t.Fatalf("problems = %q", probs)
	}
	if !strings.Contains(s.Report(), "FAIL  example.com/b") {
		t.Fatalf("report:\n%s", s.Report())
	}
}

func TestCollectVMDiedEarly(t *testing.T) {
	// the guest ran package a, then the VM went away: no exit file for b, no completion marker
	fsys := fstest.MapFS{
		"results/a.exit": {Data: []byte("0\n")},
		"results/a.out":  {Data: []byte("--- PASS: TestA (0.00s)\n")},
	}
	s, err := Collect(fsys, twoUnits())
	if err != nil {
		t.Fatal(err)
	}
	if s.OK() || s.Finished {
		t.Fatal("a run without completion marker must fail")
	}
	probs := strings.Join(s.Problems(), "\n")
	for _, want := range []string{"ended before the tests finished", "example.com/b: not run"} {
		if !strings.Contains(probs, want) {
			t.Errorf("problems lack %q:\n%s", want, probs)
		}
	}
}

func TestCollectNothingRanIsNotOK(t *testing.T) {
	s, err := Collect(fstest.MapFS{"results/done": {}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.OK() {
		t.Fatal("zero packages must not count as success")
	}
	if !strings.Contains(strings.Join(s.Problems(), "\n"), "no test package ran") {
		t.Fatalf("problems = %v", s.Problems())
	}
}

func TestCollectRejectsGarbageExitCode(t *testing.T) {
	fsys := okFS()
	fsys["results/a.exit"] = &fstest.MapFile{Data: []byte("oops")}
	if _, err := Collect(fsys, twoUnits()); err == nil {
		t.Fatal("expected an error for an unreadable exit code")
	}
}

func TestCollectZeroTestsEverywhereIsNotAPass(t *testing.T) {
	// the binaries exited 0 but ran nothing: a -run filter that matches nothing, or all tests gone
	fsys := fstest.MapFS{
		"results/a.exit": {Data: []byte("0\n")},
		"results/a.out":  {Data: []byte("testing: warning: no tests to run\nPASS\n")},
		"results/done":   {},
	}
	s, err := Collect(fsys, []Unit{{Name: "a", ImportPath: "example.com/a"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.OK() {
		t.Fatal("zero tests must not count as success")
	}
	if !strings.Contains(strings.Join(s.Problems(), "\n"), "no test ran") {
		t.Fatalf("problems = %v", s.Problems())
	}
}

func TestCollectAnEmptyPackageAmongRealOnesIsFineWithAFilter(t *testing.T) {
	fsys := okFS()
	fsys["results/b.out"] = &fstest.MapFile{Data: []byte("testing: warning: no tests to run\nPASS\n")}
	s, _ := Collect(fsys, twoUnits())
	if !s.OK() {
		t.Fatalf("a package without matching tests must not fail a run that ran others: %v", s.Problems())
	}
}
