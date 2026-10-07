package testbed

import (
	"fmt"
	"math"
	"time"
)

// The statistics of plan §4.3: faults are random processes, so the measurement tests compare what
// they observed with an interval, not with a value, and repeat a failed statistical assertion once.

// LossConfidence is the confidence of the interval the measured loss must lie in (plan §4.3: the
// 99.9 % binomial confidence interval of the configured rate).
const LossConfidence = 0.999

// BinomialBounds returns the range of counts [lo, hi] that n independent trials with success
// probability p produce with the given central confidence: the probability of a count below lo and
// the probability of a count above hi are each at most (1-conf)/2. A measured loss of k packets in n
// is consistent with a configured rate p when lo <= k <= hi.
func BinomialBounds(n int, p, conf float64) (lo, hi int) {
	if n <= 0 || p <= 0 {
		return 0, 0
	}
	if p >= 1 {
		return n, n
	}
	tail := (1 - conf) / 2
	pmf := make([]float64, n+1)
	lp, lq := math.Log(p), math.Log(1-p)
	lgn, _ := math.Lgamma(float64(n + 1))
	for k := 0; k <= n; k++ {
		lgk, _ := math.Lgamma(float64(k + 1))
		lgnk, _ := math.Lgamma(float64(n - k + 1))
		pmf[k] = math.Exp(lgn - lgk - lgnk + float64(k)*lp + float64(n-k)*lq)
	}
	// lo: the smallest k whose cumulative probability P(X <= k) exceeds the tail, so that P(X < lo) <= tail
	cum := 0.0
	for k := 0; k <= n; k++ {
		cum += pmf[k]
		if cum > tail {
			lo = k
			break
		}
	}
	// hi: the largest k with P(X >= k) > tail
	cum = 0.0
	for k := n; k >= 0; k-- {
		cum += pmf[k]
		if cum > tail {
			hi = k
			break
		}
	}
	return lo, hi
}

// LossWithinInterval reports whether lost of sent packets is within the 99.9 % binomial interval of
// the configured rate (0 to 1), and the interval as fractions for the message of a failure.
func LossWithinInterval(lost, sent int, rate float64) (ok bool, loFrac, hiFrac float64) {
	if sent <= 0 {
		return false, 0, 0
	}
	lo, hi := BinomialBounds(sent, rate, LossConfidence)
	return lost >= lo && lost <= hi, float64(lo) / float64(sent), float64(hi) / float64(sent)
}

// SpreadOfUniform is the width between the 5th and the 95th percentile of a delay that is uniform in
// delay ± jitter (netem's default distribution): 90 % of the width of 2·jitter.
func SpreadOfUniform(jitter time.Duration) time.Duration {
	return time.Duration(1.8 * float64(jitter))
}

// Spread is the width between the 5th and the 95th percentile of ds.
func Spread(ds []time.Duration) time.Duration { return Percentile(ds, 95) - Percentile(ds, 5) }

// reporter is the part of testing.TB that Statistically needs.
type reporter interface {
	Helper()
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Statistically runs a statistical assertion with the flakiness policy of plan §4.3: an attempt that
// fails is repeated once, and only a second failure fails the test. The attempt measures anew each
// time (it must not close over a result it measured before), and logs what it measured, so the
// distribution of a failure is in the log. It returns whether the assertion held.
//
// It does not check testbed.Accurate: a test that asserts accuracy calls it only where the
// measurement is meaningful.
func Statistically(t reporter, what string, attempt func() error) bool {
	t.Helper()
	first := attempt()
	if first == nil {
		return true
	}
	t.Logf("%s: the first attempt failed, repeating once (plan §4.3): %v", what, first)
	second := attempt()
	if second == nil {
		t.Logf("%s: the second attempt held", what)
		return true
	}
	t.Errorf("%s: failed twice\nfirst:  %v\nsecond: %v", what, first, second)
	return false
}

// CheckLatency returns an error when got is not within the plan's tolerance of want (±2 ms + 5 %).
func CheckLatency(what string, got, want time.Duration) error {
	if Within(got, want, 2*time.Millisecond, 0.05) {
		return nil
	}
	return fmt.Errorf("%s: median %v, configured %v (tolerance ±2 ms + 5 %%)", what, got, want)
}

// CheckLoss returns an error when lost of sent is outside the 99.9 % interval of rate.
func CheckLoss(what string, lost, sent int, rate float64) error {
	ok, lo, hi := LossWithinInterval(lost, sent, rate)
	if ok {
		return nil
	}
	return fmt.Errorf("%s: %d of %d lost (%.2f %%), configured %.2f %%, the 99.9 %% interval is %.2f %% to %.2f %%",
		what, lost, sent, 100*float64(lost)/float64(sent), rate*100, lo*100, hi*100)
}

// CheckSpread returns an error when the spread of ds is not about that of a uniform jitter: the plan
// asks for the spread to be compared with the configured jitter and gives no tolerance, so the
// tolerance here is a test's own: within half to one and a half times the expected width, plus the
// latency tolerance of 2 ms.
func CheckSpread(what string, ds []time.Duration, jitter time.Duration) error {
	got, want := Spread(ds), SpreadOfUniform(jitter)
	if got >= want/2-2*time.Millisecond && got <= want*3/2+2*time.Millisecond {
		return nil
	}
	return fmt.Errorf("%s: 5th to 95th percentile spread %v, expected about %v for a jitter of %v", what, got, want, jitter)
}
