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

type key struct {
	test, assertion, flow, direction, quantity, unit, kernel string
}

func (k key) String() string {
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

func report(runs []runRecord, re *regexp.Regexp, tsv bool, w io.Writer) {
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].start.Before(runs[j].start) })
	rows := map[key][]row{}
	for i := range runs {
		last := map[key]int{} // the index in rows[k] of this run's line of k
		for _, m := range runs[i].lines {
			k := key{m.Test, m.Assertion, m.Flow, m.Direction, m.Quantity, m.Unit, m.Kernel}
			if re != nil && !re.MatchString(k.String()) {
				continue
			}
			if j, ok := last[k]; ok {
				if m.Attempt >= rows[k][j].m.Attempt {
					rows[k][j].m = m
				}
				continue
			}
			last[k] = len(rows[k])
			rows[k] = append(rows[k], row{run: &runs[i], m: m})
		}
	}
	keys := make([]key, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	if tsv {
		fmt.Fprintln(w, "check\trun\tstart\tconfigured\tvalue\tratio\tlow\thigh\tposition\tn\tattempt\tpass")
		for _, k := range keys {
			for _, r := range rows[k] {
				m := r.m
				fmt.Fprintf(w, "%s\t%s\t%s\t%g\t%g\t%s\t%s\t%s\t%s\t%d\t%d\t%v\n", k, r.run.file, r.run.start.Format(time.RFC3339),
					m.Configured, m.Value, ratio(m), bound(m.Low), bound(m.High), position(m), m.N, m.Attempt, m.Pass)
			}
		}
		return
	}
	for _, k := range keys {
		fmt.Fprintln(w, k)
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
