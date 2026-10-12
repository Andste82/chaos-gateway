package testbed

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func readRecord(t *testing.T, path string) []Measurement {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []Measurement
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m Measurement
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func delaysAround(center time.Duration, n int) []time.Duration {
	var ds []time.Duration
	for i := 0; i < n; i++ {
		ds = append(ds, center+time.Duration(i%11-5)*time.Millisecond)
	}
	return ds
}

// Every check of an accuracy assertion leaves one line in the record (plan §4.3, P2-AUDIT-07): inside
// Statistically named after the assertion and numbered with the attempt, outside it as attempt 1; an
// attempt that recorded nothing leaves its verdict.
func TestTheChecksOfAnAssertionWriteOneLineEachIntoTheRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.jsonl")
	t.Setenv(MeasurementsEnv, path)
	t.Setenv(EmulatedEnv, "")

	var r fakeReporter
	n := 0
	Statistically(&r, "A to the server", func() error {
		n++
		m := Measure(&r, "A to the server")
		up := delaysAround(100*time.Millisecond, 201)
		if n == 1 {
			up = delaysAround(120*time.Millisecond, 201) // the first attempt misses the median
		}
		return joinErrors(
			m.Latency("upload", up, 100*time.Millisecond),
			m.Loss("download", 100, 2000, 0.05))
	})
	Statistically(&r, "a test's own check", func() error { return nil })
	if err := Measure(&r, "the BGP link").Duration("", "re-convergence", 3*time.Second, 2500*time.Millisecond, 0, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	got := readRecord(t, path)
	if len(got) != 6 {
		t.Fatalf("%d lines, want 2 per attempt of the first assertion, 1 for the second, 1 outside: %+v", len(got), got)
	}
	first := got[0]
	if first.Test != "TestFake" || first.Assertion != "A to the server" || first.Flow != "A to the server" || first.Direction != "upload" ||
		first.Quantity != "median delay" || first.Unit != "ms" || first.N != 201 || first.Attempt != 1 || first.Pass || first.Error == "" ||
		first.Configured != 100 || first.Value != 120 || first.Low == nil || *first.Low != 93 || first.High == nil || *first.High != 107 || !first.Accurate ||
		first.MedianMS == nil || *first.MedianMS != 120 || first.P5MS == nil || first.P95MS == nil || first.Time.IsZero() {
		t.Errorf("the first line: %+v", first)
	}
	if loss := got[1]; loss.Quantity != "loss" || loss.Direction != "download" || loss.Value != 0.05 || loss.Loss == nil || *loss.Loss != 0.05 ||
		loss.Low == nil || *loss.Low >= 0.05 || loss.High == nil || *loss.High <= 0.05 || !loss.Pass || loss.Attempt != 1 {
		t.Errorf("the loss line: %+v", loss)
	}
	if again := got[2]; again.Attempt != 2 || !again.Pass || again.Value != 100 {
		t.Errorf("the line of the repetition: %+v", again)
	}
	if own := got[4]; own.Assertion != "a test's own check" || own.Quantity != "assertion" || !own.Pass || own.Attempt != 1 {
		t.Errorf("the line of an attempt that recorded nothing: %+v", own)
	}
	if out := got[5]; out.Assertion != "the BGP link" || out.Attempt != 1 || out.Quantity != "re-convergence" || out.Value != 3000 || !out.Pass {
		t.Errorf("the line of a check outside Statistically: %+v", out)
	}
	// the fake reporter's name is the same in every test of the package, so the calls count on
	if first.Call == 0 || got[1].Call != first.Call || got[2].Call != first.Call || got[4].Call != first.Call+1 || got[5].Call != 0 {
		t.Errorf("the calls: %d %d %d %d %d", first.Call, got[1].Call, got[2].Call, got[4].Call, got[5].Call)
	}
	if got[0].Kernel == "" && runtimeHasProc() {
		t.Error("no kernel release in the line")
	}
}

// An attempt that stops the test (a helper's t.Fatal, which ends the goroutine) still leaves the lines
// it recorded, and a check of the same test after it is not taken into the dead attempt.
func TestAnAttemptThatStopsTheTestLeavesItsLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.jsonl")
	t.Setenv(MeasurementsEnv, path)
	var r fakeReporter
	// as t.Fatal does: the goroutine of the test ends
	inGoroutine := func(f func()) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			f()
		}()
		<-done
	}
	inGoroutine(func() {
		Statistically(&r, "A to the server", func() error {
			_ = Measure(&r, "A to the server").Latency("upload", delaysAround(100*time.Millisecond, 201), 100*time.Millisecond)
			runtime.Goexit()
			return nil
		})
	})
	inGoroutine(func() {
		Statistically(&r, "nothing recorded", func() error {
			runtime.Goexit()
			return nil
		})
	})
	_ = Measure(&r, "the BGP link").Duration("", "re-convergence", 3*time.Second, 3*time.Second, 0, 10*time.Second)

	got := readRecord(t, path)
	if len(got) != 3 {
		t.Fatalf("%d lines, want the stopped attempt's, the verdict of the empty one and the check after them: %+v", len(got), got)
	}
	if got[0].Assertion != "A to the server" || got[0].Attempt != 1 || got[0].Quantity != "median delay" {
		t.Errorf("the line of the stopped attempt: %+v", got[0])
	}
	if got[1].Assertion != "nothing recorded" || got[1].Quantity != "assertion" || got[1].Pass {
		t.Errorf("the verdict of the stopped attempt that recorded nothing: %+v", got[1])
	}
	if got[2].Assertion != "the BGP link" || got[2].Attempt != 1 {
		t.Errorf("the check after the stopped attempts: %+v", got[2])
	}
}

// The bounds of a line of CheckDelays are the ones it checks: 1 % of the delays rounded up to whole
// packets, so a pass lies within them; the hard limit is in Extra.
func TestTheDelaysLineHasTheBoundsTheCheckUses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.jsonl")
	t.Setenv(MeasurementsEnv, path)
	var r fakeReporter
	ds := delaysAround(100*time.Millisecond, 60)
	ds[7] = 112 * time.Millisecond // one of 60 outside [90 ms, 110 ms], within the hard limit
	if err := Measure(&r, "copies").Delays("", ds, 90*time.Millisecond, 110*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	got := readRecord(t, path)
	if len(got) != 1 {
		t.Fatalf("%d lines", len(got))
	}
	m := got[0]
	if !m.Pass || m.High == nil || m.Value > *m.High || *m.High != 1.0/60 || m.Extra["hard_limit_ms"] != 10 || m.Extra["beyond_hard_limit"] != 0 {
		t.Errorf("the line: %+v", m)
	}
}

// Without the variable nothing is written: a developer's run leaves no file behind.
func TestWithoutTheVariableNothingIsRecorded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(MeasurementsEnv, "")
	t.Chdir(dir)
	var r fakeReporter
	Statistically(&r, "x", func() error { return Measure(&r, "x").Rate("download", 2e6, 2e6, 0.1) })
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("files appeared: %v", entries)
	}
}

func joinErrors(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func runtimeHasProc() bool {
	_, err := os.Stat("/proc/sys/kernel/osrelease")
	return err == nil
}
