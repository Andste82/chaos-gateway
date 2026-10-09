package testbed

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
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

func TestCheckDelaysToleratesNoiseAndFindsADisturbance(t *testing.T) {
	ms := time.Millisecond
	var ds []time.Duration
	for i := 0; i < 200; i++ {
		ds = append(ds, 40*ms+time.Duration(i%5)*ms)
	}
	if err := CheckDelays("up", ds, 36*ms, 44*ms); err != nil {
		t.Errorf("all inside: %v", err)
	}
	// two of two hundred a few ms too long: 1 % is the share that passes
	noisy := append(append([]time.Duration(nil), ds...), 47*ms, 48*ms)
	if err := CheckDelays("up", noisy, 36*ms, 44*ms); err != nil {
		t.Errorf("two of 202 slightly outside: %v", err)
	}
	// more than 1 % outside the bounds
	many := append(append([]time.Duration(nil), ds...), 47*ms, 48*ms, 47*ms, 46*ms)
	if err := CheckDelays("up", many, 36*ms, 44*ms); err == nil || !strings.Contains(err.Error(), "4 of 204") {
		t.Errorf("four of 204 outside: %v", err)
	}
	// a short stream tolerates one packet slightly outside, not two
	short := append(append([]time.Duration(nil), ds[:67]...), 48*ms)
	if err := CheckDelays("up", short, 36*ms, 44*ms); err != nil {
		t.Errorf("one of 68 slightly outside: %v", err)
	}
	if err := CheckDelays("up", append(short, 47*ms), 36*ms, 44*ms); err == nil {
		t.Errorf("two of 69 outside")
	}
	// one packet far outside (a flushed or doubled queue)
	far := append(append([]time.Duration(nil), ds...), 90*ms)
	if err := CheckDelays("up", far, 36*ms, 44*ms); err == nil || !strings.Contains(err.Error(), "largest 90ms") {
		t.Errorf("one delay of 90 ms: %v", err)
	}
	if err := CheckDelays("up", nil, 36*ms, 44*ms); err != nil {
		t.Errorf("no delays: %v", err)
	}
}

func TestAShareIsCheckedAgainstTheBinomialInterval(t *testing.T) {
	// 2000 draws of 10 %: the 99.9 % interval is about 8 % to 12 %
	if err := CheckShare("dup", "duplicates", 200, 2000, 0.10); err != nil {
		t.Error(err)
	}
	if err := CheckShare("dup", "duplicates", 0, 2000, 0.10); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Errorf("no duplicates at all is not 10 %%: %v", err)
	}
	if err := CheckShare("dup", "duplicates", 330, 2000, 0.10); err == nil {
		t.Error("16 % is not 10 %")
	}
}

func TestGilbertBoundsAreWiderThanTheBinomialOnesBecauseLossesComeInBursts(t *testing.T) {
	n, p, r := 2000, 0.02, 0.20
	lo, hi := GilbertLossBounds(n, p, r, 1, 0, LossConfidence)
	// stationary share of the bad state is p/(p+r) = 9.1 %: about 182 of 2000
	if lo > 182 || hi < 182 {
		t.Fatalf("[%d, %d] does not hold the mean of 182", lo, hi)
	}
	blo, bhi := BinomialBounds(n, p/(p+r), LossConfidence)
	if hi-lo < 2*(bhi-blo) {
		t.Errorf("the burst interval [%d, %d] is not much wider than the binomial one [%d, %d]", lo, hi, blo, bhi)
	}
	// independent losses (p + r = 1: the state has no memory) give the binomial interval
	lo, hi = GilbertLossBounds(n, 0.1, 0.9, 1, 0, LossConfidence)
	blo, bhi = BinomialBounds(n, 0.1, LossConfidence)
	if abs(lo-blo) > 3 || abs(hi-bhi) > 3 {
		t.Errorf("a memoryless channel: [%d, %d], the binomial interval is [%d, %d]", lo, hi, blo, bhi)
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func TestAGilbertElliottRunIsCheckedForItsLossAndItsBurstLength(t *testing.T) {
	// a synthetic run of the model with a fixed generator: p 2 %, r 20 %
	rng := rand.New(rand.NewSource(7))
	var lost []int
	bad := false
	n := 2000
	for i := 0; i < n; i++ {
		if bad {
			lost = append(lost, i)
			if rng.Float64() < 0.20 {
				bad = false
			}
		} else if rng.Float64() < 0.02 {
			bad = true
		}
	}
	if err := CheckBurstLoss("model", lost, n, 0.02, 0.20, 1, 0); err != nil {
		t.Errorf("a run of the model itself: %v (%d lost, mean run %.1f)", err, len(lost), MeanRun(lost))
	}
	// the same number of losses, all single: not bursts
	var single []int
	for i := 0; i < len(lost); i++ {
		single = append(single, i*(n/len(lost)))
	}
	if err := CheckBurstLoss("random", single, n, 0.02, 0.20, 1, 0); err == nil || !strings.Contains(err.Error(), "runs") {
		t.Errorf("losses that are all single are not bursts of 5: %v", err)
	}
	if err := CheckBurstLoss("none", nil, n, 0.02, 0.20, 1, 0); err == nil {
		t.Error("no loss at all is not a 9 % loss")
	}
}

func TestOutagesAreFoundAndComparedWithAFlappingFault(t *testing.T) {
	// 10 ms probes, 3 s up and 2 s down, starting up: outages at 3 s, 8 s and 13 s of 2 s each
	const iv = 10 * time.Millisecond
	var lost []int
	total := 1700
	for _, start := range []int{300, 800, 1300} {
		for i := start; i < start+200 && i < total; i++ {
			lost = append(lost, i)
		}
	}
	out := Outages(lost, iv, 5)
	if len(out) != 3 || out[0].Start != 3*time.Second || out[0].Len != 2*time.Second {
		t.Fatalf("%+v", out)
	}
	if err := CheckFlaps("flap", out, total, 3*time.Second, 2*time.Second, iv, 100*time.Millisecond, 3); err != nil {
		t.Error(err)
	}
	// a single lost datagram in between is not an outage
	if got := Outages(append([]int{50}, lost...), iv, 5); len(got) != 3 {
		t.Errorf("%+v", got)
	}
	// the last outage is cut off by the end of the run: only two complete ones
	if err := CheckFlaps("flap", out, 1400, 3*time.Second, 2*time.Second, iv, 100*time.Millisecond, 3); err == nil {
		t.Error("an outage that the run cut off is not complete")
	}
	// the down time is wrong
	if err := CheckFlaps("flap", out, total, 3*time.Second, 1*time.Second, iv, 100*time.Millisecond, 2); err == nil || !strings.Contains(err.Error(), "lasts") {
		t.Errorf("2 s outages are not 1 s ones: %v", err)
	}
	// the cycle is wrong
	if err := CheckFlaps("flap", out, total, 2*time.Second, 2*time.Second, iv, 100*time.Millisecond, 2); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("a 5 s cycle is not a 4 s one: %v", err)
	}
}
