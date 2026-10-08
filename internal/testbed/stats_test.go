package testbed

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestBinomialBoundsAreTheCentralInterval(t *testing.T) {
	// Two thousand packets at 5 %: the mean is 100, the standard deviation 9.75; the central 99.9 %
	// interval is about ±3.29 standard deviations, 68 to 132.
	lo, hi := BinomialBounds(2000, 0.05, LossConfidence)
	if lo < 65 || lo > 72 || hi < 128 || hi > 136 {
		t.Errorf("2000 at 5 %%: [%d, %d], want about [68, 132]", lo, hi)
	}
	// the bounds are exact: the probability outside them is at most 0.1 %, and the bounds are the
	// tightest that satisfy it
	pmf := func(n int, p float64, k int) float64 {
		a, _ := math.Lgamma(float64(n + 1))
		b, _ := math.Lgamma(float64(k + 1))
		c, _ := math.Lgamma(float64(n - k + 1))
		return math.Exp(a - b - c + float64(k)*math.Log(p) + float64(n-k)*math.Log(1-p))
	}
	below, above := 0.0, 0.0
	for k := 0; k < lo; k++ {
		below += pmf(2000, 0.05, k)
	}
	for k := hi + 1; k <= 2000; k++ {
		above += pmf(2000, 0.05, k)
	}
	if below > 0.0005 || above > 0.0005 {
		t.Errorf("the probability outside [%d, %d] is %g below and %g above, each must be at most 0.0005", lo, hi, below, above)
	}
	if below+pmf(2000, 0.05, lo) <= 0.0005 {
		t.Errorf("lo=%d is not the tightest lower bound", lo)
	}
}

func TestBinomialBoundsOfTheEdges(t *testing.T) {
	for _, tc := range []struct {
		n      int
		p      float64
		lo, hi int
	}{
		{2000, 0, 0, 0},
		{2000, 1, 2000, 2000},
		{0, 0.5, 0, 0},
	} {
		if lo, hi := BinomialBounds(tc.n, tc.p, LossConfidence); lo != tc.lo || hi != tc.hi {
			t.Errorf("n=%d p=%v: [%d, %d], want [%d, %d]", tc.n, tc.p, lo, hi, tc.lo, tc.hi)
		}
	}
	// a rate that is rare: the interval is asymmetric and never negative
	if lo, hi := BinomialBounds(2000, 0.001, LossConfidence); lo != 0 || hi < 5 || hi > 9 {
		t.Errorf("2000 at 0.1 %%: [%d, %d]", lo, hi)
	}
}

func TestLossWithinTheIntervalOfTheConfiguredRate(t *testing.T) {
	if ok, _, _ := LossWithinInterval(100, 2000, 0.05); !ok {
		t.Error("exactly the configured rate is within the interval")
	}
	if ok, lo, hi := LossWithinInterval(40, 2000, 0.05); ok || lo >= hi {
		t.Errorf("2 %% lost where 5 %% is configured is outside the interval: %v %v", lo, hi)
	}
	if ok, _, _ := LossWithinInterval(0, 2000, 0.05); ok {
		t.Error("no loss where loss is configured is outside the interval")
	}
	if ok, _, _ := LossWithinInterval(0, 0, 0.05); ok {
		t.Error("nothing sent proves nothing")
	}
	if err := CheckLoss("up", 100, 2000, 0.05); err != nil {
		t.Error(err)
	}
	if err := CheckLoss("up", 300, 2000, 0.05); err == nil || !strings.Contains(err.Error(), "99.9 % interval") {
		t.Errorf("%v", err)
	}
}

func TestLatencyAndSpreadChecksUseThePlansTolerances(t *testing.T) {
	ms := time.Millisecond
	// ±2 ms + 5 % of 100 ms is 7 ms
	if err := CheckLatency("up", 107*ms, 100*ms); err != nil {
		t.Error(err)
	}
	if err := CheckLatency("up", 108*ms, 100*ms); err == nil {
		t.Error("8 ms off a 100 ms delay is outside the tolerance")
	}
	if err := CheckLatency("up", 91*ms, 100*ms); err == nil {
		t.Error("9 ms below is outside the tolerance too")
	}

	// a delay uniform in 100 ms ± 20 ms: the 5th to 95th percentile spread is 36 ms
	var ds []time.Duration
	for i := 0; i < 1000; i++ {
		ds = append(ds, 80*ms+time.Duration(i)*40*ms/1000)
	}
	if got := Spread(ds); got < 35*ms || got > 37*ms {
		t.Errorf("spread %v, want 36 ms", got)
	}
	if err := CheckSpread("up", ds, 20*ms); err != nil {
		t.Error(err)
	}
	if err := CheckSpread("up", ds, 5*ms); err == nil {
		t.Error("a spread of 36 ms is not that of a 5 ms jitter")
	}
	if err := CheckSpread("up", ds, 80*ms); err == nil {
		t.Error("a spread of 36 ms is not that of an 80 ms jitter")
	}
	var fixed []time.Duration
	for i := 0; i < 100; i++ {
		fixed = append(fixed, 100*ms)
	}
	if err := CheckSpread("up", fixed, 20*ms); err == nil {
		t.Error("a delay without any spread is not that of a jitter")
	}
}

// fakeReporter records what Statistically tells the test.
type fakeReporter struct {
	logs, errs []string
}

func (f *fakeReporter) Helper() {}
func (f *fakeReporter) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}
func (f *fakeReporter) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func TestAStatisticalAssertionThatFailsIsRepeatedOnceAndOnlyASecondFailureFails(t *testing.T) {
	// holds at once: one attempt, nothing logged
	var r fakeReporter
	n := 0
	if !Statistically(&r, "ok", func() error { n++; return nil }) || n != 1 || len(r.logs)+len(r.errs) != 0 {
		t.Errorf("a passing assertion: %d attempts, %v %v", n, r.logs, r.errs)
	}

	// fails once, holds the second time: two attempts, the test does not fail, the first failure is in the log
	r, n = fakeReporter{}, 0
	ok := Statistically(&r, "flaky", func() error {
		n++
		if n == 1 {
			return errors.New("5 of 2000 lost")
		}
		return nil
	})
	if !ok || n != 2 || len(r.errs) != 0 || len(r.logs) == 0 || !strings.Contains(r.logs[0], "5 of 2000 lost") {
		t.Errorf("a flaky assertion: ok=%v, %d attempts, logs %v, errors %v", ok, n, r.logs, r.errs)
	}

	// fails twice: exactly two attempts, the failure carries both measurements
	r, n = fakeReporter{}, 0
	ok = Statistically(&r, "broken", func() error { n++; return fmt.Errorf("measurement %d", n) })
	if ok || n != 2 || len(r.errs) != 1 || !strings.Contains(r.errs[0], "measurement 1") || !strings.Contains(r.errs[0], "measurement 2") {
		t.Errorf("a broken assertion: ok=%v, %d attempts, errors %v", ok, n, r.errs)
	}
}

func TestCheckEveryDelayFindsOneDelayedPacket(t *testing.T) {
	ms := time.Millisecond
	ds := []time.Duration{40 * ms, 38 * ms, 43 * ms, 41 * ms}
	if err := CheckEveryDelay("up", ds, 36*ms, 44*ms); err != nil {
		t.Errorf("all inside: %v", err)
	}
	if err := CheckEveryDelay("up", append(ds, 52*ms), 36*ms, 44*ms); err == nil || !strings.Contains(err.Error(), "1 of 5") || !strings.Contains(err.Error(), "largest 52ms") {
		t.Errorf("one outside: %v", err)
	}
	if err := CheckEveryDelay("up", nil, 36*ms, 44*ms); err != nil {
		t.Errorf("no delays: %v", err)
	}
}
