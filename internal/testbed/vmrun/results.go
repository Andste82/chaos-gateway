package vmrun

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Counts are the test results found in `go test -v` output.
type Counts struct{ Passed, Failed, Skipped int }

// Total returns the number of verdicts.
func (c Counts) Total() int { return c.Passed + c.Failed + c.Skipped }

var verdictRE = regexp.MustCompile(`(?m)^\s*--- (PASS|FAIL|SKIP): `)

// ParseCounts counts the verdict lines of `go test -v` output, subtests included.
func ParseCounts(out string) Counts {
	var c Counts
	for _, m := range verdictRE.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case "PASS":
			c.Passed++
		case "FAIL":
			c.Failed++
		case "SKIP":
			c.Skipped++
		}
	}
	return c
}

// PackageResult is what the guest recorded for one package.
type PackageResult struct {
	Unit
	// ExitCode is the test binary's exit code; Ran is false when the guest never got to it.
	ExitCode int
	Ran      bool
	Output   string
	Counts   Counts
}

// OK reports whether the package ran and passed.
func (r PackageResult) OK() bool { return r.Ran && r.ExitCode == 0 }

// Summary is the result of a whole VM run.
type Summary struct {
	Packages []PackageResult
	// Finished is true when the guest wrote its final marker, i.e. the VM did not die early.
	Finished bool
	// AllowSkip allows skipped tests; otherwise a skip fails OK, the same way a FAIL does (CC-04:
	// a missing tool or a silent skip must never pass).
	AllowSkip bool
}

// OK reports whether every package passed, the guest finished, and (unless AllowSkip) nothing
// was skipped.
func (s Summary) OK() bool {
	if !s.Finished || len(s.Packages) == 0 || s.Totals().Total() == 0 {
		return false
	}
	if !s.AllowSkip && s.Totals().Skipped > 0 {
		return false
	}
	for _, p := range s.Packages {
		if !p.OK() {
			return false
		}
	}
	return true
}

// Totals adds up the counts of all packages.
func (s Summary) Totals() Counts {
	var t Counts
	for _, p := range s.Packages {
		t.Passed += p.Counts.Passed
		t.Failed += p.Counts.Failed
		t.Skipped += p.Counts.Skipped
	}
	return t
}

// Problems explains why the run is not OK, one line each.
func (s Summary) Problems() []string {
	var out []string
	if len(s.Packages) == 0 {
		out = append(out, "no test package ran")
	}
	if !s.Finished {
		out = append(out, "the VM ended before the tests finished (no completion marker)")
	}
	if len(s.Packages) > 0 && s.Totals().Total() == 0 {
		out = append(out, "no test ran: every package reported zero tests")
	}
	if !s.AllowSkip {
		if n := s.Totals().Skipped; n > 0 {
			out = append(out, fmt.Sprintf("%d tests skipped (pass -allow-skip to allow)", n))
		}
	}
	for _, p := range s.Packages {
		switch {
		case !p.Ran:
			out = append(out, fmt.Sprintf("%s: not run", p.ImportPath))
		case p.ExitCode != 0:
			out = append(out, fmt.Sprintf("%s: exit code %d", p.ImportPath, p.ExitCode))
		}
	}
	return out
}

// Report formats the summary for a terminal.
func (s Summary) Report() string {
	var b strings.Builder
	for _, p := range s.Packages {
		verdict := "ok  "
		switch {
		case !p.Ran:
			verdict = "SKIP"
		case p.ExitCode != 0:
			verdict = "FAIL"
		}
		fmt.Fprintf(&b, "%s  %-60s %d passed, %d failed, %d skipped\n", verdict, p.ImportPath,
			p.Counts.Passed, p.Counts.Failed, p.Counts.Skipped)
	}
	t := s.Totals()
	fmt.Fprintf(&b, "total: %d passed, %d failed, %d skipped\n", t.Passed, t.Failed, t.Skipped)
	for _, p := range s.Problems() {
		fmt.Fprintf(&b, "problem: %s\n", p)
	}
	return b.String()
}

// Collect reads the results the guest left in workDir/results. dir is rooted at the work
// directory.
func Collect(dir fs.FS, units []Unit) (Summary, error) {
	s := Summary{}
	if _, err := fs.Stat(dir, path.Join("results", doneMarker)); err == nil {
		s.Finished = true
	}
	for _, u := range units {
		r := PackageResult{Unit: u}
		if raw, err := fs.ReadFile(dir, path.Join("results", u.Name+exitSuffix)); err == nil {
			code, perr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if perr != nil {
				return s, fmt.Errorf("vmrun: bad exit code file for %s: %q", u.ImportPath, raw)
			}
			r.Ran, r.ExitCode = true, code
		} else if !errors.Is(err, fs.ErrNotExist) {
			return s, err
		}
		if raw, err := fs.ReadFile(dir, path.Join("results", u.Name+outSuffix)); err == nil {
			r.Output = string(raw)
			r.Counts = ParseCounts(r.Output)
		}
		s.Packages = append(s.Packages, r)
	}
	return s, nil
}
