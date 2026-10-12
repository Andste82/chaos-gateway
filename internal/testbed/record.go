package testbed

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

// The measurement record of plan §4.3: "Measured distributions are stored per night, so drift shows
// up as a trend before it fails a test" (P2-AUDIT-07). Every accuracy assertion writes what it
// measured as one JSON line (a Measurement) into the file that MeasurementsEnv names; without it
// nothing is written. The VM runner sets the variable in the guest and copies the file out
// (vmrun.Config.Measurements), the nightly kernel matrix uploads it as an artifact per kernel, and
// tools/measurements compares the files of several nights.
//
// An assertion records through a Recorder (Measure): each of its checks is the check of stats.go
// (CheckLatency, CheckLoss, ...) and a line with the figure it compared, the bounds it compared it
// with, and the distribution it came from. Inside Statistically the lines of an attempt carry the
// assertion's name and the number of the attempt; an attempt that recorded nothing (a test's own
// check) still leaves one line with its verdict.

// MeasurementsEnv names the file the measurement record is appended to.
const MeasurementsEnv = "CHAOS_MEASUREMENTS"

// Measurement is one line of the record: one check of an accuracy assertion. The delays are in
// milliseconds, a loss or a share is a fraction (0 to 1), a rate is in bit/s; Unit says which.
type Measurement struct {
	Test string `json:"test"`
	// Assertion is the name the test gives the statistical assertion (Statistically's what), or the
	// flow for a check outside Statistically.
	Assertion string `json:"assertion"`
	Flow      string `json:"flow,omitempty"`
	Direction string `json:"direction,omitempty"`
	// Quantity is what was compared ("median delay", "loss", "rate", ...), Value the measured figure,
	// Configured the configured one, and [Low, High] the bounds the check accepts (one of them is
	// missing where the check has none).
	Quantity   string   `json:"quantity"`
	Unit       string   `json:"unit,omitempty"`
	Kernel     string   `json:"kernel"`
	Accurate   bool     `json:"accurate"`
	N          int      `json:"n"`
	Configured float64  `json:"configured"`
	Value      float64  `json:"value"`
	Low        *float64 `json:"low,omitempty"`
	High       *float64 `json:"high,omitempty"`
	// MedianMS, P5MS and P95MS are the distribution of the delays of the direction, where the check
	// had one; Loss the share of the packets the direction lost, where the check counted it.
	MedianMS *float64 `json:"median_ms,omitempty"`
	P5MS     *float64 `json:"p5_ms,omitempty"`
	P95MS    *float64 `json:"p95_ms,omitempty"`
	Loss     *float64 `json:"loss,omitempty"`
	// Extra holds figures of a check beyond the one it compares (the mean run of a burst loss).
	Extra map[string]float64 `json:"extra,omitempty"`
	// Attempt is 1 or 2 (the repetition of plan §4.3); Pass is the check's own verdict, Error its
	// message when it failed.
	Attempt int       `json:"attempt"`
	Pass    bool      `json:"pass"`
	Error   string    `json:"error,omitempty"`
	Time    time.Time `json:"time"`
}

// named is the part of testing.TB the record needs: the name of the test.
type named interface{ Name() string }

// attemptLines collects the lines of one attempt of Statistically.
type attemptLines struct {
	lines []Measurement
}

var record struct {
	mu     sync.Mutex
	open   map[string]*attemptLines // the attempt that runs in a test, by the test's name
	failed bool                     // a write failed once; it is reported once
}

var kernelRelease = sync.OnceValue(func() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
})

// openAttempt makes a the attempt that collects the lines of the test, and returns the one it
// replaces (nil, unless an assertion runs inside another).
func openAttempt(test string, a *attemptLines) *attemptLines {
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.open == nil {
		record.open = map[string]*attemptLines{}
	}
	prev := record.open[test]
	record.open[test] = a
	return prev
}

func closeAttempt(test string, prev *attemptLines) {
	record.mu.Lock()
	defer record.mu.Unlock()
	if prev == nil {
		delete(record.open, test)
	} else {
		record.open[test] = prev
	}
}

// writeMeasurement appends m to the record, if there is one. A failed write does not fail the test:
// it is logged once, when t can log.
func writeMeasurement(t named, m Measurement) {
	path := os.Getenv(MeasurementsEnv)
	if path == "" {
		return
	}
	line, err := json.Marshal(m)
	if err == nil {
		record.mu.Lock()
		var f *os.File
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err == nil {
			_, err = f.Write(append(line, '\n'))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		report := err != nil && !record.failed
		if err != nil {
			record.failed = true
		}
		record.mu.Unlock()
		if !report {
			return
		}
	}
	if l, ok := t.(interface{ Logf(string, ...any) }); ok {
		l.Logf("testbed: the measurement record %s cannot be written: %v", path, err)
	}
}

// Recorder records the checks of one flow (see Measure).
type Recorder struct {
	t    named
	flow string
}

// Measure returns the Recorder for the checks of a flow in test t. Each of its methods is a check of
// stats.go that also records a Measurement; it returns the check's error.
func Measure(t named, flow string) Recorder { return Recorder{t: t, flow: flow} }

// Flow is the flow the Recorder records for.
func (r Recorder) Flow() string { return r.flow }

func (r Recorder) what(dir string) string {
	if dir == "" {
		return r.flow
	}
	return r.flow + " " + dir
}

// Record records m as a check of the flow in the direction dir with the verdict err, and returns err.
// It fills in the test, the flow, the direction, the kernel, the time and the verdict; a test's own
// check uses it directly.
func (r Recorder) Record(dir string, m Measurement, err error) error {
	m.Test = r.t.Name()
	m.Flow, m.Direction = r.flow, dir
	m.Kernel, m.Accurate = kernelRelease(), Accurate()
	m.Pass = err == nil
	if err != nil {
		m.Error = err.Error()
	}
	m.Time = time.Now().UTC()
	record.mu.Lock()
	a := record.open[m.Test]
	if a != nil {
		a.lines = append(a.lines, m)
	}
	record.mu.Unlock()
	if a == nil {
		m.Assertion, m.Attempt = r.flow, 1
		writeMeasurement(r.t, m)
	}
	return err
}

// ms is d in milliseconds, to the microsecond.
func ms(d time.Duration) float64 { return math.Round(float64(d)/1e3) / 1e3 }

func ptr(f float64) *float64 { return &f }

// WithDelays is m with the distribution of ds (MedianMS, P5MS, P95MS).
func WithDelays(m Measurement, ds []time.Duration) Measurement {
	if len(ds) > 0 {
		m.MedianMS, m.P5MS, m.P95MS = ptr(ms(Median(ds))), ptr(ms(Percentile(ds, 5))), ptr(ms(Percentile(ds, 95)))
	}
	return m
}

func frac(k, n int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(k) / float64(n)
}

// Latency is CheckLatency on the median of ds.
func (r Recorder) Latency(dir string, ds []time.Duration, want time.Duration) error {
	got := Median(ds)
	tol := latencyTolerance(want)
	return r.Record(dir, WithDelays(Measurement{
		Quantity: "median delay", Unit: "ms", N: len(ds),
		Configured: ms(want), Value: ms(got), Low: ptr(ms(want - tol)), High: ptr(ms(want + tol)),
	}, ds), CheckLatency(r.what(dir), got, want))
}

// Unchanged checks that the median of ds is within the latency tolerance (±2 ms + 5 %) of before,
// the median of the flow before something that must not touch it (isolation, plan §4.3).
func (r Recorder) Unchanged(dir string, ds []time.Duration, before time.Duration) error {
	got := Median(ds)
	tol := latencyTolerance(before)
	var err error
	if !Within(got, before, 2*time.Millisecond, 0.05) {
		err = fmt.Errorf("%s median %v, before %v (tolerance ±2 ms + 5 %%)", r.what(dir), got, before)
	}
	return r.Record(dir, WithDelays(Measurement{
		Quantity: "median delay unchanged", Unit: "ms", N: len(ds),
		Configured: ms(before), Value: ms(got), Low: ptr(ms(before - tol)), High: ptr(ms(before + tol)),
	}, ds), err)
}

// Spread is CheckSpread.
func (r Recorder) Spread(dir string, ds []time.Duration, jitter time.Duration) error {
	lo, hi := spreadBounds(jitter)
	return r.Record(dir, WithDelays(Measurement{
		Quantity: "delay spread p5-p95", Unit: "ms", N: len(ds),
		Configured: ms(SpreadOfUniform(jitter)), Value: ms(Spread(ds)), Low: ptr(ms(lo)), High: ptr(ms(hi)),
	}, ds), CheckSpread(r.what(dir), ds, jitter))
}

// Delays is CheckDelays: Value is the share of ds outside [lo, hi], High the share allowed.
func (r Recorder) Delays(dir string, ds []time.Duration, lo, hi time.Duration) error {
	out := 0
	for _, d := range ds {
		if d < lo || d > hi {
			out++
		}
	}
	m := WithDelays(Measurement{
		Quantity: "delays outside bounds", Unit: "fraction", N: len(ds),
		Value: frac(out, len(ds)), Low: ptr(0), High: ptr(DelayOutlierShare),
		Extra: map[string]float64{"low_ms": ms(lo), "high_ms": ms(hi)},
	}, ds)
	return r.Record(dir, m, CheckDelays(r.what(dir), ds, lo, hi))
}

// Loss is CheckLoss: lost of sent against the configured rate (0 to 1).
func (r Recorder) Loss(dir string, lost, sent int, rate float64) error {
	lo, hi := BinomialBounds(sent, rate, LossConfidence)
	got := frac(lost, sent)
	return r.Record(dir, Measurement{
		Quantity: "loss", Unit: "fraction", N: sent,
		Configured: rate, Value: got, Low: ptr(frac(lo, sent)), High: ptr(frac(hi, sent)), Loss: ptr(got),
	}, CheckLoss(r.what(dir), lost, sent, rate))
}

// Share is CheckShare: count of n against the probability p; noun says what was counted.
func (r Recorder) Share(dir, noun string, count, n int, p float64) error {
	lo, hi := BinomialBounds(n, p, LossConfidence)
	return r.Record(dir, Measurement{
		Quantity: "share " + noun, Unit: "fraction", N: n,
		Configured: p, Value: frac(count, n), Low: ptr(frac(lo, n)), High: ptr(frac(hi, n)),
	}, CheckShare(r.what(dir), noun, count, n, p))
}

// BurstLoss is CheckBurstLoss; the mean run of the losses is in Extra.
func (r Recorder) BurstLoss(dir string, lost []int, n int, p, rr, lossBad, lossGood float64) error {
	lo, hi := GilbertLossBounds(n, p, rr, lossBad, lossGood, LossConfidence)
	piB := p / (p + rr)
	got := frac(len(lost), n)
	return r.Record(dir, Measurement{
		Quantity: "burst loss", Unit: "fraction", N: n,
		Configured: piB*lossBad + (1-piB)*lossGood, Value: got, Low: ptr(frac(lo, n)), High: ptr(frac(hi, n)), Loss: ptr(got),
		Extra: map[string]float64{"mean_run": MeanRun(lost), "configured_run": 1 / rr},
	}, CheckBurstLoss(r.what(dir), lost, n, p, rr, lossBad, lossGood))
}

// Flaps is CheckFlaps: Value is the median length of the complete outages, the median distance of
// their starts is in Extra.
func (r Recorder) Flaps(dir string, outages []Outage, total int, up, down, interval, tolerance time.Duration, want int) error {
	var lens, cycles []time.Duration
	var prev *Outage
	for i, o := range outages {
		if o.First > 0 && o.Last < total-1 {
			lens = append(lens, o.Len)
			if prev != nil {
				cycles = append(cycles, o.Start-prev.Start)
			}
			prev = &outages[i]
		}
	}
	tol := tolerance + 2*interval
	m := Measurement{
		Quantity: "outage length", Unit: "ms", N: len(lens),
		Configured: ms(down), Value: ms(Median(lens)), Low: ptr(ms(down - tol)), High: ptr(ms(down + tol)),
		Extra: map[string]float64{"cycle_ms": ms(Median(cycles)), "configured_cycle_ms": ms(up + down)},
	}
	return r.Record(dir, m, CheckFlaps(r.what(dir), outages, total, up, down, interval, tolerance, want))
}

// Rate checks a throughput in bit/s against want within ±tolerance (a fraction: 0.1 is the plan's
// ±10 %, §4.3).
func (r Recorder) Rate(dir string, got, want, tolerance float64) error {
	var err error
	if got < (1-tolerance)*want || got > (1+tolerance)*want {
		err = fmt.Errorf("%s: %.3f Mbit/s, not within ±%.0f %% of %.3f Mbit/s (plan §4.3)", r.what(dir), got/1e6, tolerance*100, want/1e6)
	}
	return r.Record(dir, Measurement{
		Quantity: "rate", Unit: "bit/s", N: 1,
		Configured: want, Value: got, Low: ptr((1 - tolerance) * want), High: ptr((1 + tolerance) * want),
	}, err)
}

// Duration checks that a time a test measured once (a convergence) lies within [lo, hi]; want is
// the expected time.
func (r Recorder) Duration(dir, quantity string, got, want, lo, hi time.Duration) error {
	var err error
	if got < lo || got > hi {
		err = fmt.Errorf("%s: %s %v, expected about %v, within %v to %v", r.what(dir), quantity, got.Round(100*time.Millisecond), want, lo, hi)
	}
	return r.Record(dir, Measurement{
		Quantity: quantity, Unit: "ms", N: 1,
		Configured: ms(want), Value: ms(got), Low: ptr(ms(lo)), High: ptr(ms(hi)),
	}, err)
}

// assertionLine is the line of an attempt of Statistically that recorded nothing itself.
func assertionLine(t named, what string, err error) Measurement {
	m := Measurement{Test: t.Name(), Quantity: "assertion", Kernel: kernelRelease(), Accurate: Accurate(), Pass: err == nil, Time: time.Now().UTC()}
	if err != nil {
		m.Error = err.Error()
	}
	return m
}
