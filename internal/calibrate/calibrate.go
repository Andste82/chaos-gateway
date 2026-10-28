// Package calibrate is the arithmetic of the self-test "measure this profile" (plan §2.12, M25): what a set of impairment
// parameters should make of a round trip, and whether what a probe measured matches within the tolerances of plan §4.3.
// It works on numbers; the measurement itself is made by internal/probe in the probe's namespace.
package calibrate

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The tolerances of plan §4.3.
const (
	// LatencyAbsolute and LatencyRelative: the median of the added delay may differ from the configured one by ±2 ms + 5 %.
	LatencyAbsolute = 2 * time.Millisecond
	LatencyRelative = 0.05
	// LossConfidence is the confidence of the interval the measured loss must lie in (99.9 %).
	LossConfidence = 0.999
	// RateRelative: the throughput may differ from the limit by ±10 %.
	RateRelative = 0.10
	// MinSamplesLoss is the number of datagrams from which the loss check is a statistic and not an indication.
	MinSamplesLoss = 2000
	// JitterMargin is the factor on the expected spread of the round trip that the jitter check allows.
	JitterMargin = 1.2
	// MinSamplesLatency is the number of answers the latency check needs.
	MinSamplesLatency = 200
)

// Expectation is what the parameters should make of a round trip of a probe to an echo server: the delays of the two
// directions add up and the losses combine.
type Expectation struct {
	// AddedRTTMs is the delay the parameters add to a round trip (upload plus download: the delays, and the time a packet
	// takes to leave a direction that has a rate), JitterMs the sum of the two jitters.
	AddedRTTMs, JitterMs float64
	// SpreadMs is how far the 95th percentile of the round trip is expected above its median because of the jitters: the
	// two directions are independent, so it depends on the distribution of the delay (netem: mu + jitter * a table that
	// is uniform on -1..1 or normal with a standard deviation of 1). 0 when there is none to check: no jitter, or a
	// distribution (pareto) whose tail has no bound to hold the measurement to.
	SpreadMs float64
	// LossPercent is the chance that a round trip is lost; LossMeasured is false when it is not a rate (flapping), Correlated
	// when losses come in bursts (a correlation or the burst model), where the binomial interval does not hold.
	LossPercent  float64
	LossMeasured bool
	Correlated   bool
	// RateBps is the limit a bulk transfer should be held to, the smaller one of the two directions that have one; 0 for none.
	RateBps int64
	// Notes name what the parameters ask for and the calibration does not measure.
	Notes []string
}

// Impaired reports whether the parameters change anything the calibration measures.
func (e Expectation) Impaired() bool {
	return e.AddedRTTMs > 0 || e.LossPercent > 0 || e.RateBps > 0 || !e.LossMeasured
}

// Directions returns the parameters of the two directions of an impairment: the flat ones apply to both, upload and
// download give each its own, and a missing direction is unimpaired (nil).
func Directions(p *model.ImpairmentParams) (up, down *model.NetemParams) {
	if p.Upload != nil || p.Download != nil {
		return p.Upload, p.Download
	}
	flat := model.NetemParams{Blackout: p.Blackout, BurstLoss: p.BurstLoss, Corrupt: p.Corrupt, Distribution: p.Distribution,
		Duplicate: p.Duplicate, Flapping: p.Flapping, Jitter: p.Jitter, KeepOrder: p.KeepOrder, Latency: p.Latency, Loss: p.Loss,
		LossCorrelation: p.LossCorrelation, QueueLimit: p.QueueLimit, Rate: p.Rate, Reorder: p.Reorder}
	return &flat, &flat
}

// Expect works out the expectation of impairment parameters; nil is no impairment.
func Expect(p *model.ImpairmentParams) (Expectation, error) { return ExpectFor(p, 0) }

// ExpectFor is Expect for datagrams of wireBytes bytes at the qdisc (the Ethernet frame): a direction with a rate holds
// each of them back for wireBytes*8/rate seconds besides its delay. 0 leaves that out.
func ExpectFor(p *model.ImpairmentParams, wireBytes int) (Expectation, error) {
	e := Expectation{LossMeasured: true}
	if p == nil {
		return e, nil
	}
	up, down := Directions(p)
	pass := 1.0 // the chance that a round trip is not lost
	notes := map[string]bool{}
	var jitters []jitter
	for _, d := range []*model.NetemParams{up, down} {
		if d == nil {
			continue
		}
		n, err := compiler.NetemOf(d)
		if err != nil {
			return e, err
		}
		e.AddedRTTMs += float64(n.Delay) / float64(time.Millisecond)
		e.JitterMs += float64(n.Jitter) / float64(time.Millisecond)
		if n.Rate > 0 && wireBytes > 0 {
			e.AddedRTTMs += float64(wireBytes) * 8 / float64(n.Rate) * 1000
		}
		if n.Jitter > 0 {
			jitters = append(jitters, jitter{ms: float64(n.Jitter) / float64(time.Millisecond), dist: n.Distribution})
		}
		switch {
		case n.Flapping != nil:
			e.LossMeasured = false
			notes["flapping is a timed blackout: its loss is not a rate"] = true
		case n.Gemodel != nil:
			g := n.Gemodel
			bad := g.P / (g.P + g.R)
			if g.P+g.R == 0 {
				bad = 0
			}
			pass *= 1 - (bad*g.LossBad+(1-bad)*g.LossGood)/100
			e.Correlated = true
		default:
			pass *= 1 - n.Loss/100
			if n.LossCorr > 0 {
				e.Correlated = true
			}
		}
		if n.Rate > 0 && n.Rate < compiler.KeepOrderRate && (e.RateBps == 0 || n.Rate < e.RateBps) {
			e.RateBps = n.Rate
		}
		for _, x := range []struct {
			on   bool
			text string
		}{{n.Reorder > 0, "reordering"}, {n.Duplicate > 0, "duplication"}, {n.Corrupt > 0, "corruption"}} {
			if x.on {
				notes[x.text+" is not measured"] = true
			}
		}
	}
	e.LossPercent = (1 - pass) * 100
	var note string
	if e.SpreadMs, note = spreadOf(jitters); note != "" {
		notes[note] = true
	}
	for n := range notes {
		e.Notes = append(e.Notes, n)
	}
	sort.Strings(e.Notes)
	return e, nil
}

// jitter is the jitter of one direction with the distribution of its delay ("" is uniform).
type jitter struct {
	ms   float64
	dist string
}

// spreadOf is the distance of the 95th percentile of a round trip from its median when each direction adds its delay
// independently. Uniform delays of +-a and +-b add up to a trapezoid whose tail is (a+b-x)^2/(8ab) (x is the distance
// from the median; the tail is 0.9a where a >= 10b); normal ones add up to a normal one with the variances summed; a
// mix is taken as normal with the variance of a uniform delay J^2/3. A pareto tail has no bound: 0 and a note.
func spreadOf(js []jitter) (float64, string) {
	if len(js) == 0 {
		return 0, ""
	}
	uniform, variance := true, 0.0
	for _, j := range js {
		switch j.dist {
		case "":
			variance += j.ms * j.ms / 3
		case "normal":
			uniform = false
			variance += j.ms * j.ms
		default:
			return 0, "the jitter of the distribution " + j.dist + " has a heavy tail and is not checked"
		}
	}
	if uniform {
		a, b := js[0].ms, 0.0
		if len(js) == 2 {
			a, b = math.Max(js[0].ms, js[1].ms), math.Min(js[0].ms, js[1].ms)
		}
		if a >= 10*b {
			return 0.9 * a, ""
		}
		return a + b - math.Sqrt(0.4*a*b), ""
	}
	return normalQuantile(0.95) * math.Sqrt(variance), ""
}

// Measurement is what the probe measured, in the units the executor reports.
type Measurement struct {
	// BaselineUs are the round trips of the unimpaired path (the gateway's own echo to the same server), in microseconds.
	BaselineUs []int64
	// Sent and Received count the datagrams of the probe's echo run, RTTUs are the round trips of those answered.
	Sent, Received int
	RTTUs          []int64
	// RateBps is the throughput of the TCP echo run, RateMeasured says whether one was made.
	RateBps      int64
	RateMeasured bool
}

// Evaluate compares a measurement with the expectation and fills the result.
func Evaluate(exp Expectation, m Measurement) model.CalibrationResult {
	var res model.CalibrationResult
	var checks []model.CalibrationCheck
	check := func(name string, ok bool, format string, a ...any) {
		checks = append(checks, model.CalibrationCheck{Name: model.CalibrationCheckName(name), Ok: ok, Detail: fmt.Sprintf(format, a...)})
	}
	f32 := func(v float64) *float32 { x := float32(math.Round(v*1000) / 1000); return &x }

	base := Median(m.BaselineUs)
	median := Median(m.RTTUs)
	p95 := Percentile(m.RTTUs, 95)
	haveRTT := len(m.RTTUs) > 0
	res.Measured = &model.CalibrationMeasured{}
	if len(m.BaselineUs) > 0 {
		res.Measured.BaselineRttMs = f32(base / 1000)
	}
	if haveRTT {
		res.Measured.RttMedianMs, res.Measured.RttP95Ms = f32(median/1000), f32(p95/1000)
	}
	expRTT := base/1000 + exp.AddedRTTMs
	res.Expected = &model.CalibrationExpected{}
	if len(m.BaselineUs) > 0 {
		res.Expected.RttMs = f32(expRTT)
	}
	if exp.LossMeasured {
		res.Expected.LossPercent = f32(exp.LossPercent)
	}
	if exp.RateBps > 0 {
		r := exp.RateBps
		res.Expected.RateBps = &r
	}

	// ---- latency: the median of the added delay, ±2 ms + 5 %
	switch {
	case exp.LossMeasured && exp.LossPercent >= 100:
		check("latency", true, "everything is dropped: there is no round trip to time")
	case !haveRTT:
		check("latency", false, "no answer came back, so there is no round trip to time")
	case len(m.BaselineUs) == 0:
		check("latency", false, "the unimpaired round trip could not be measured")
	case len(m.RTTUs) < MinSamplesLatency:
		added := median/1000 - base/1000
		check("latency", math.Abs(added-exp.AddedRTTMs) <= tolerance(exp.AddedRTTMs), "%d answers are fewer than the %d the check needs; the median added %.2f ms, %.2f ms expected", len(m.RTTUs), MinSamplesLatency, added, exp.AddedRTTMs)
	default:
		added := (median - base) / 1000
		tol := tolerance(exp.AddedRTTMs)
		check("latency", math.Abs(added-exp.AddedRTTMs) <= tol, "the median round trip is %.2f ms against %.2f ms unimpaired: %.2f ms added, %.2f ms expected (tolerance ±%.2f ms)", median/1000, base/1000, added, exp.AddedRTTMs, tol)
	}

	// ---- jitter: the 95th percentile of the round trip lies about SpreadMs above its median (the estimate of a percentile
	// from a run varies, so a fifth more is allowed, and the tolerance of the delay)
	if exp.SpreadMs > 0 && haveRTT {
		spread := (p95 - median) / 1000
		limit := JitterMargin*exp.SpreadMs + tolerance(exp.AddedRTTMs)
		check("jitter", spread <= limit, "the 95th percentile is %.2f ms above the median; the jitters make %.2f ms of it, %.2f ms are allowed (a margin of %.0f %% and the tolerance of the delay)", spread, exp.SpreadMs, limit, 100*(JitterMargin-1))
	}

	// ---- loss: the measured count must lie in the 99.9 % interval of the binomial distribution of the configured rate
	if m.Sent > 0 {
		lost := m.Sent - m.Received
		pct := 100 * float64(lost) / float64(m.Sent)
		res.Measured.LossPercent = f32(pct)
		lo, hi := Wilson(lost, m.Sent, 0.99)
		res.Measured.LossCi99 = &[]float32{float32(math.Round(lo*100000) / 1000), float32(math.Round(hi*100000) / 1000)}
		switch {
		case !exp.LossMeasured:
			check("loss", true, "%.2f %% of %d datagrams were lost; the parameters flap, so there is no rate to compare", pct, m.Sent)
		case exp.Correlated:
			// losses in bursts are not independent: the binomial interval is too narrow for them
			tol := math.Max(2, exp.LossPercent/2)
			check("loss", math.Abs(pct-exp.LossPercent) <= tol, "%.2f %% of %d datagrams were lost, %.2f %% expected (the losses come in bursts: tolerance ±%.2f points)", pct, m.Sent, exp.LossPercent, tol)
		default:
			kLo, kHi := BinomialInterval(m.Sent, exp.LossPercent/100, LossConfidence)
			ok := lost >= kLo && lost <= kHi
			note := ""
			if m.Sent < MinSamplesLoss {
				note = fmt.Sprintf(" (%d datagrams are fewer than the %d of the statistic: indicative only)", m.Sent, MinSamplesLoss)
			}
			check("loss", ok, "%d of %d datagrams were lost (%.2f %%); %.2f %% expected: %d to %d lost lie in the %.1f %% interval%s", lost, m.Sent, pct, exp.LossPercent, kLo, kHi, 100*LossConfidence, note)
		}
	}

	// ---- rate: the throughput within ±10 % of the limit
	if m.RateMeasured {
		r := m.RateBps
		res.Measured.RateBps = &r
		if exp.RateBps > 0 {
			lo, hi := float64(exp.RateBps)*(1-RateRelative), float64(exp.RateBps)*(1+RateRelative)
			check("rate", float64(r) >= lo && float64(r) <= hi, "%d bit/s measured against the limit of %d bit/s (tolerance ±%.0f %%)", r, exp.RateBps, 100*RateRelative)
		}
	} else if exp.RateBps > 0 {
		check("rate", false, "the throughput was not measured")
	}

	ok := true
	for _, c := range checks {
		ok = ok && c.Ok
	}
	res.WithinTolerance = ok
	res.Checks = &checks
	res.Samples = &model.CalibrationSamples{Received: m.Received, Sent: m.Sent}
	var detail []string
	for _, c := range checks {
		detail = append(detail, string(c.Name)+": "+c.Detail)
	}
	detail = append(detail, exp.Notes...)
	d := strings.Join(detail, "; ")
	res.Detail = &d
	return res
}

// tolerance is the room for a delay of added ms: 2 ms plus 5 %.
func tolerance(added float64) float64 {
	return float64(LatencyAbsolute)/float64(time.Millisecond) + LatencyRelative*added
}

// Median is the median of the values, 0 for none.
func Median(v []int64) float64 { return Percentile(v, 50) }

// Percentile is the p-th percentile of the values with linear interpolation, 0 for none.
func Percentile(v []int64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	pos := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return float64(s[lo]) + (float64(s[hi])-float64(s[lo]))*(pos-float64(lo))
}

// BinomialInterval returns the smallest and largest count of successes in n trials with probability p that a measurement of
// that rate may show with the given confidence: the counts below and above it each have at most (1-confidence)/2 of the
// probability.
func BinomialInterval(n int, p, confidence float64) (lo, hi int) {
	if p <= 0 {
		return 0, 0
	}
	if p >= 1 {
		return n, n
	}
	tail := (1 - confidence) / 2
	lp, lq := math.Log(p), math.Log(1-p)
	lgn, _ := math.Lgamma(float64(n) + 1)
	cum := 0.0
	lo, hi = 0, n
	loSet := false
	for k := 0; k <= n; k++ {
		lgk, _ := math.Lgamma(float64(k) + 1)
		lgnk, _ := math.Lgamma(float64(n-k) + 1)
		cum += math.Exp(lgn - lgk - lgnk + float64(k)*lp + float64(n-k)*lq)
		if !loSet && cum > tail {
			lo, loSet = k, true
		}
		if cum >= 1-tail {
			hi = k
			break
		}
	}
	return lo, hi
}

// Wilson is the Wilson score interval of a rate k/n at the given confidence, as fractions.
func Wilson(k, n int, confidence float64) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	z := normalQuantile(1 - (1-confidence)/2)
	phat := float64(k) / float64(n)
	nf := float64(n)
	den := 1 + z*z/nf
	centre := (phat + z*z/(2*nf)) / den
	half := z * math.Sqrt(phat*(1-phat)/nf+z*z/(4*nf*nf)) / den
	return math.Max(0, centre-half), math.Min(1, centre+half)
}

// normalQuantile is the quantile of the standard normal distribution (Acklam's approximation, relative error below 1.2e-9).
func normalQuantile(p float64) float64 {
	a := []float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := []float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := []float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := []float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
	const plow = 0.02425
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= 1-plow:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q / (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
	q := math.Sqrt(-2 * math.Log(1-p))
	return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
}
