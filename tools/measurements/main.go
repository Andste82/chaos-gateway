// Command measurements compares the measurement records of several runs (plan §4.3: "Measured
// distributions are stored per night, so drift shows up as a trend before it fails a test").
//
//	go run ./tools/measurements [-match REGEXP] [-tsv] FILE|DIR...
//
// A record is the JSON-lines file the accuracy assertions of the testbed tests write
// (testbed.MeasurementsEnv); the nightly kernel matrix uploads one per kernel as the artifact
// measurements-<kernel>. A directory stands for every *.jsonl file below it, so the artifacts of
// several nights can be downloaded side by side (`gh run download <id> -D nights/<date>`) and passed
// as one directory. Each file is one run.
//
// For every check (test, assertion, flow, direction, quantity, kernel) it prints one line per run,
// oldest first: the measured value, its ratio to the configured one, where it lies between the
// bounds (0 % is the lower bound, 100 % the upper one; a value that drifts towards one side shows
// there before it fails), the attempt and the verdict. Only the last attempt of a run is shown.
//
// A test can make the same check more than once in a run (the phases of a profile switch, each with
// its own configured value). Those checks stay apart: the n-th of them in a run is shown as "#n" and
// compared with the n-th of the other runs. The repetition of a statistical assertion (attempt 2)
// replaces its own call's attempt 1 only, as the line's call number says (testbed.Measurement.Call);
// in a record written before lines had one, it replaces the latest attempt 1 of the same check.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// runRecord is the record of one run: the file it came from and its lines.
type runRecord struct {
	file  string
	start time.Time
	lines []testbed.Measurement
}

// key is a check: base names it, nth is its place among the checks with that name in a run (1 for
// the first).
type key struct {
	base
	nth int
}

type base struct {
	test, assertion, flow, direction, quantity, unit, kernel string
}

func (k base) String() string {
	parts := []string{k.test, k.assertion}
	if k.flow != "" && k.flow != k.assertion {
		parts = append(parts, k.flow)
	}
	if k.direction != "" {
		parts = append(parts, k.direction)
	}
	q := k.quantity
	if k.unit != "" {
		q += " (" + k.unit + ")"
	}
	return strings.Join(append(parts, q), " / ") + ", kernel " + k.kernel
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("measurements", flag.ContinueOnError)
	fl.SetOutput(stderr)
	match := fl.String("match", "", "only the checks whose line (test / assertion / flow / direction / quantity, kernel) matches this regular expression")
	tsv := fl.Bool("tsv", false, "print tab-separated values (one row per check and run) instead of the report")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: measurements [-match REGEXP] [-tsv] FILE|DIR...")
		return 2
	}
	var re *regexp.Regexp
	if *match != "" {
		var err error
		if re, err = regexp.Compile(*match); err != nil {
			fmt.Fprintln(stderr, "measurements:", err)
			return 2
		}
	}
	files, err := expand(fl.Args())
	if err != nil {
		fmt.Fprintln(stderr, "measurements:", err)
		return 2
	}
	var runs []runRecord
	for _, f := range files {
		r, err := read(f)
		if err != nil {
			fmt.Fprintln(stderr, "measurements:", err)
			return 2
		}
		if len(r.lines) > 0 {
			runs = append(runs, r)
		}
	}
	if len(runs) == 0 {
		fmt.Fprintln(stderr, "measurements: no measurement in", strings.Join(files, ", "))
		return 1
	}
	report(runs, re, *tsv, stdout)
	return 0
}

// expand replaces every directory by the *.jsonl files below it.
func expand(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		fi, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			out = append(out, a)
			continue
		}
		err = filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func read(path string) (runRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return runRecord{}, err
	}
	defer func() { _ = f.Close() }()
	r := runRecord{file: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var m testbed.Measurement
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return runRecord{}, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if r.start.IsZero() || (!m.Time.IsZero() && m.Time.Before(r.start)) {
			r.start = m.Time
		}
		r.lines = append(r.lines, m)
	}
	return r, sc.Err()
}

// row is the last attempt of one check in one run.
type row struct {
	run *runRecord
	m   testbed.Measurement
}

// check identifies a check in one run across its attempts: the call of Statistically and the place
// among the lines with the same name in that call and attempt (0 and the place among the attempts 1
// outside a call, or in a record without call numbers).
type check struct {
	b         base
	call, occ int
}

func report(runs []runRecord, re *regexp.Regexp, tsv bool, w io.Writer) {
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].start.Before(runs[j].start) })
	rows := map[key][]row{}
	several := map[base]bool{} // a check made more than once in a run
	for i := range runs {
		type inCall struct {
			b             base
			call, attempt int
		}
		occ := map[inCall]int{}
		at := map[check]key{}      // the check of a line of this run
		nth := map[base]int{}      // the checks with the name so far
		latest := map[base]check{} // the latest attempt 1 without a call number
		for _, m := range runs[i].lines {
			b := base{m.Test, m.Assertion, m.Flow, m.Direction, m.Quantity, m.Unit, m.Kernel}
			if re != nil && !re.MatchString(b.String()) {
				continue
			}
			var id check
			switch {
			case m.Call > 0:
				c := inCall{b, m.Call, m.Attempt}
				occ[c]++
				id = check{b, m.Call, occ[c]}
			case m.Attempt <= 1:
				c := inCall{b, 0, 1}
				occ[c]++
				id = check{b, 0, occ[c]}
				latest[b] = id
			default:
				var ok bool
				if id, ok = latest[b]; !ok {
					c := inCall{b, 0, 1}
					occ[c]++
					id = check{b, 0, occ[c]}
				}
			}
			if k, ok := at[id]; ok {
				r := &rows[k][len(rows[k])-1]
				if m.Attempt >= r.m.Attempt {
					r.m = m
				}
				continue
			}
			nth[b]++
			if nth[b] > 1 {
				several[b] = true
			}
			k := key{b, nth[b]}
			at[id] = k
			rows[k] = append(rows[k], row{run: &runs[i], m: m})
		}
	}
	keys := make([]key, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := keys[i].String(), keys[j].String(); a != b {
			return a < b
		}
		return keys[i].nth < keys[j].nth
	})
	name := func(k key) string {
		if !several[k.base] {
			return k.String()
		}
		return fmt.Sprintf("%s #%d", k.String(), k.nth)
	}

	if tsv {
		fmt.Fprintln(w, "check\trun\tstart\tconfigured\tvalue\tratio\tlow\thigh\tposition\tn\tattempt\tpass")
		for _, k := range keys {
			for _, r := range rows[k] {
				m := r.m
				fmt.Fprintf(w, "%s\t%s\t%s\t%g\t%g\t%s\t%s\t%s\t%s\t%d\t%d\t%v\n", name(k), r.run.file, r.run.start.Format(time.RFC3339),
					m.Configured, m.Value, ratio(m), bound(m.Low), bound(m.High), position(m), m.N, m.Attempt, m.Pass)
			}
		}
		return
	}
	for _, k := range keys {
		fmt.Fprintln(w, name(k))
		for _, r := range rows[k] {
			m := r.m
			verdict := "pass"
			if !m.Pass {
				verdict = "FAIL"
			}
			fmt.Fprintf(w, "  %-10s %-12s configured %-10.6g value %-10.6g ratio %-6s bounds %s to %s  position %-5s n %-5d attempt %d %s  %s\n",
				r.run.start.Format("2006-01-02"), r.run.start.Format("15:04:05Z"), m.Configured, m.Value, ratio(m), bound(m.Low), bound(m.High),
				position(m), m.N, m.Attempt, verdict, r.run.file)
		}
	}
}

func ratio(m testbed.Measurement) string {
	if m.Configured == 0 {
		return "-"
	}
	return fmt.Sprintf("%.3f", m.Value/m.Configured)
}

func bound(b *float64) string {
	if b == nil {
		return "-"
	}
	return fmt.Sprintf("%.6g", *b)
}

// position is where the value lies between the bounds, in percent: 0 at the lower bound, 100 at the
// upper one, outside below 0 or above 100.
func position(m testbed.Measurement) string {
	if m.Low == nil || m.High == nil || *m.High <= *m.Low {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*(m.Value-*m.Low)/(*m.High-*m.Low))
}
