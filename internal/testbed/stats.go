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

// DelayOutlierShare and DelayOutlierHard are the tolerances of CheckDelays for a stream that must not be
// disturbed from outside: at most 1 % of the delays (and always one) may lie outside the bounds, and none
// further than 10 ms beyond them. The plan (§4.3) gives a tolerance for the median only; these are the
// tests' own. A packet that is due while a neighbor's change holds the lock of the interface's queues, or
// while a loaded (nested virtual) machine runs something else, leaves a few milliseconds late: the
// hosted runners showed 2 of 904 delays 1 to 3 ms too long on the nested runner and 1 of 68 by 2 ms on
// the native one, none further. A disturbance by a change somewhere else is not that: a queue that was
// made again drops or delays a whole run of packets, a flush of one delays them by its full delay.
const (
	DelayOutlierShare = 0.01
	DelayOutlierHard  = 10 * time.Millisecond
)

// CheckDelays returns an error when more than DelayOutlierShare of ds (rounded up: at least one packet)
// lies outside [lo, hi], or any delay lies more than DelayOutlierHard outside it. A median cannot see a
// few delayed packets, this can.
func CheckDelays(what string, ds []time.Duration, lo, hi time.Duration) error {
	var out, far int
	var min, max time.Duration
	for i, d := range ds {
		if i == 0 || d < min {
			min = d
		}
		if i == 0 || d > max {
			max = d
		}
		if d < lo || d > hi {
			out++
		}
		if d < lo-DelayOutlierHard || d > hi+DelayOutlierHard {
			far++
		}
	}
	if far == 0 && out <= int(math.Ceil(DelayOutlierShare*float64(len(ds)))) {
		return nil
	}
	return fmt.Errorf("%s: %d of %d delays outside [%v, %v], %d of them more than %v outside (smallest %v, largest %v)",
		what, out, len(ds), lo, hi, far, DelayOutlierHard, min, max)
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

// ---- shares, bursts and outages (M10) ------------------------------------------------------------

// CheckShare returns an error when count of n is outside the 99.9 % binomial interval of probability p:
// the duplicates, the reordered and the corrupted packets are Bernoulli draws per packet like the
// losses, so the plan's interval (§4.3) is the one to use. noun says what was counted.
func CheckShare(what, noun string, count, n int, p float64) error {
	lo, hi := BinomialBounds(n, p, LossConfidence)
	if count >= lo && count <= hi {
		return nil
	}
	return fmt.Errorf("%s: %d of %d %s (%.2f %%), configured %.2f %%, the 99.9 %% interval is %.2f %% to %.2f %%",
		what, count, n, noun, 100*float64(count)/float64(n), p*100, 100*float64(lo)/float64(n), 100*float64(hi)/float64(n))
}

// zTwoSided is the standard normal quantile for the central confidence conf (3.29 for 99.9 %).
func zTwoSided(conf float64) float64 {
	lo, hi := 0.0, 10.0
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if math.Erf(mid/math.Sqrt2) < conf {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// GilbertLossBounds returns the range of the number of losses that n packets suffer, with the given
// central confidence, through a Gilbert-Elliott channel in its stationary state: p and r are the
// probabilities of going from good to bad and back (0 to 1), lossBad and lossGood the loss
// probabilities in the two states. The losses are not independent, so the binomial interval would be
// far too narrow: the interval here is the normal approximation of the sum of a hidden-Markov
// indicator, with its autocorrelation (lambda = 1 - p - r) in the variance.
func GilbertLossBounds(n int, p, r, lossBad, lossGood, conf float64) (lo, hi int) {
	if n <= 0 || p+r <= 0 {
		return 0, 0
	}
	piB := p / (p + r)
	piG := 1 - piB
	m := piB*lossBad + piG*lossGood
	lambda := 1 - p - r
	variance := float64(n) * (m - m*m)
	cov := (lossBad - lossGood) * (lossBad - lossGood) * piB * piG
	sum, pow := 0.0, 1.0
	for d := 1; d < n; d++ {
		pow *= lambda
		if math.Abs(pow) < 1e-12 {
			break
		}
		sum += float64(n-d) * pow
	}
	variance += 2 * cov * sum
	if variance < 0 {
		variance = 0
	}
	half := zTwoSided(conf) * math.Sqrt(variance)
	mean := float64(n) * m
	return max(0, int(math.Floor(mean-half))), min(n, int(math.Ceil(mean+half)))
}

// CheckBurstLoss returns an error when the losses of a run are not those of the configured
// Gilbert-Elliott model: the number of losses must lie in GilbertLossBounds, and, where the bad state
// loses everything and the good one nothing (the model's defaults), the lost packets must come in
// bursts of about 1/r packets (the mean length of a run of consecutive losses within half to one and a
// half times that, plus a packet: the tests' own tolerance, the plan gives none).
func CheckBurstLoss(what string, lost []int, n int, p, r, lossBad, lossGood float64) error {
	lo, hi := GilbertLossBounds(n, p, r, lossBad, lossGood, LossConfidence)
	var errs []string
	if len(lost) < lo || len(lost) > hi {
		errs = append(errs, fmt.Sprintf("%d of %d lost (%.2f %%), the 99.9 %% interval of the model is %.2f %% to %.2f %%",
			len(lost), n, 100*float64(len(lost))/float64(n), 100*float64(lo)/float64(n), 100*float64(hi)/float64(n)))
	}
	if lossBad >= 1 && lossGood <= 0 && len(lost) > 0 {
		got, want := MeanRun(lost), 1/r
		if got < want/2-1 || got > want*3/2+1 {
			errs = append(errs, fmt.Sprintf("the losses come in runs of %.1f packets on average, a model that leaves the bad state with %.0f %% per packet has runs of %.1f", got, r*100, want))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", what, joinSemi(errs))
}

func joinSemi(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += "; "
		}
		out += x
	}
	return out
}

// MeanRun is the mean length of the runs of consecutive numbers in a list of sequence numbers in
// increasing order (the lost packets of a probe run); 0 for an empty list.
func MeanRun(seqs []int) float64 {
	if len(seqs) == 0 {
		return 0
	}
	runs := 1
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			runs++
		}
	}
	return float64(len(seqs)) / float64(runs)
}

// Outage is a stretch of a probe stream in which nothing got through: the numbers of its first and last
// lost datagram and, with the spacing of the stream, when it started and how long it lasted.
type Outage struct {
	First, Last int
	Start, Len  time.Duration
}

// Outages finds the stretches of at least minLen consecutive lost datagrams in a stream sent interval
// apart. An outage that begins at the first datagram or ends at the last one of the run is cut off by
// it (First == 0, or Last == the last number sent); CheckFlaps leaves those out.
func Outages(lost []int, interval time.Duration, minLen int) []Outage {
	var out []Outage
	flush := func(first, last int) {
		if last-first+1 >= minLen {
			out = append(out, Outage{First: first, Last: last, Start: time.Duration(first) * interval, Len: time.Duration(last-first+1) * interval})
		}
	}
	first, last := -1, -1
	for _, s := range lost {
		switch {
		case first < 0:
			first, last = s, s
		case s == last+1:
			last = s
		default:
			flush(first, last)
			first, last = s, s
		}
	}
	if first >= 0 {
		flush(first, last)
	}
	return out
}

// CheckFlaps compares the outages of a probe stream with a flapping fault of the given up and down
// times: every outage that the run did not cut off must last down within tolerance, and the starts of
// consecutive outages must be one cycle (up + down) apart within tolerance. The tolerance is the
// engine's (±100 ms, plan §2.10) plus two intervals of the stream, since an outage is only seen to the
// precision of its probes (one at each edge). It returns an error when there are fewer than want complete
// outages.
func CheckFlaps(what string, outages []Outage, total int, up, down, interval, tolerance time.Duration, want int) error {
	tol := tolerance + 2*interval
	var whole []Outage
	for _, o := range outages {
		if o.First > 0 && o.Last < total-1 {
			whole = append(whole, o)
		}
	}
	if len(whole) < want {
		return fmt.Errorf("%s: %d complete outages in the run, want at least %d (all: %v)", what, len(whole), want, outages)
	}
	var errs []string
	for i, o := range whole {
		if d := o.Len - down; d < -tol || d > tol {
			errs = append(errs, fmt.Sprintf("outage %d lasts %v, configured %v (tolerance ±%v)", i+1, o.Len, down, tol))
		}
		if i > 0 {
			if d := o.Start - whole[i-1].Start - (up + down); d < -tol || d > tol {
				errs = append(errs, fmt.Sprintf("outage %d starts %v after the previous one, a cycle is %v (tolerance ±%v)", i+1, o.Start-whole[i-1].Start, up+down, tol))
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", what, joinSemi(errs))
}
