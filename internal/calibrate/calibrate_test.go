package calibrate

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func s(v string) *string { return &v }

func params(f func(p *model.ImpairmentParams)) *model.ImpairmentParams {
	p := &model.ImpairmentParams{}
	f(p)
	return p
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestTheDelaysOfBothDirectionsAddUpAndTheLossesCombine(t *testing.T) {
	// flat parameters apply to both directions
	e, err := Expect(params(func(p *model.ImpairmentParams) { p.Latency, p.Jitter, p.Loss = s("50ms"), s("10ms"), s("5%") }))
	if err != nil {
		t.Fatal(err)
	}
	if e.AddedRTTMs != 100 || e.JitterMs != 20 || !near(e.LossPercent, 9.75, 1e-9) || !e.LossMeasured || e.Correlated || e.RateBps != 0 {
		t.Errorf("%+v", e)
	}
	// each direction by itself; a missing one is unimpaired
	e, _ = Expect(params(func(p *model.ImpairmentParams) {
		p.Upload = &model.NetemParams{Latency: s("30ms"), Loss: s("10%")}
		p.Download = &model.NetemParams{Latency: s("80ms"), Rate: s("2Mbit")}
	}))
	if e.AddedRTTMs != 110 || !near(e.LossPercent, 10, 1e-9) || e.RateBps != 2_000_000 {
		t.Errorf("%+v", e)
	}
	e, _ = Expect(params(func(p *model.ImpairmentParams) { p.Upload = &model.NetemParams{Latency: s("30ms")} }))
	if e.AddedRTTMs != 30 || e.LossPercent != 0 {
		t.Errorf("%+v", e)
	}
	// nothing is nothing
	e, _ = Expect(nil)
	if e.Impaired() || !e.LossMeasured {
		t.Errorf("%+v", e)
	}
}

func TestTheSmallerRateOfTheTwoDirectionsIsTheLimit(t *testing.T) {
	e, _ := Expect(params(func(p *model.ImpairmentParams) {
		p.Upload = &model.NetemParams{Rate: s("5Mbit")}
		p.Download = &model.NetemParams{Rate: s("1Mbit")}
	}))
	if e.RateBps != 1_000_000 {
		t.Errorf("%+v", e)
	}
	// keep_order stands for a rate nobody feels: it is no limit
	e, _ = Expect(params(func(p *model.ImpairmentParams) { v := true; p.KeepOrder, p.Latency, p.Jitter = &v, s("20ms"), s("5ms") }))
	if e.RateBps != 0 {
		t.Errorf("%+v", e)
	}
}

func TestBurstLossFlappingAndTheRestAreNamedForWhatTheyAre(t *testing.T) {
	// the Gilbert-Elliott model: the share of time in the bad state is p/(p+r)
	e, _ := Expect(params(func(p *model.ImpairmentParams) {
		p.Upload = &model.NetemParams{BurstLoss: &model.GilbertElliott{P: "10%", R: "40%"}}
	}))
	if !near(e.LossPercent, 20, 1e-6) || !e.Correlated || !e.LossMeasured {
		t.Errorf("%+v", e)
	}
	e, _ = Expect(params(func(p *model.ImpairmentParams) { p.Loss, p.LossCorrelation = s("10%"), s("25%") }))
	if !e.Correlated {
		t.Errorf("%+v", e)
	}
	e, _ = Expect(params(func(p *model.ImpairmentParams) { p.Flapping = &model.Flapping{Up: "5s", Down: "1s"} }))
	if e.LossMeasured || len(e.Notes) != 1 || !strings.Contains(e.Notes[0], "flapping") {
		t.Errorf("%+v", e)
	}
	e, _ = Expect(params(func(p *model.ImpairmentParams) {
		p.Latency, p.Reorder, p.Duplicate, p.Corrupt = s("10ms"), s("5%"), s("1%"), s("1%")
	}))
	if len(e.Notes) != 3 {
		t.Errorf("%+v", e)
	}
	e, _ = Expect(params(func(p *model.ImpairmentParams) { v := true; p.Blackout = &v }))
	if e.LossPercent != 100 {
		t.Errorf("%+v", e)
	}
	if _, err := Expect(params(func(p *model.ImpairmentParams) { p.Loss = s("150%") })); err == nil {
		t.Error("an impossible loss was taken")
	}
}

func TestTheBinomialIntervalMatchesTheNormalApproximation(t *testing.T) {
	// n = 2000, p = 5 %: mean 100, deviation 9.75; the 99.9 % interval is mean ± 3.29 deviations
	lo, hi := BinomialInterval(2000, 0.05, 0.999)
	if lo < 65 || lo > 71 || hi < 130 || hi > 136 {
		t.Errorf("[%d, %d]", lo, hi)
	}
	// the interval holds the mean and gets wider with confidence
	lo99, hi99 := BinomialInterval(2000, 0.05, 0.99)
	if lo99 <= lo || hi99 >= hi || lo99 >= 100 || hi99 <= 100 {
		t.Errorf("99 %% [%d, %d] 99.9 %% [%d, %d]", lo99, hi99, lo, hi)
	}
	if lo, hi := BinomialInterval(2000, 0, 0.999); lo != 0 || hi != 0 {
		t.Errorf("[%d, %d]", lo, hi)
	}
	if lo, hi := BinomialInterval(2000, 1, 0.999); lo != 2000 || hi != 2000 {
		t.Errorf("[%d, %d]", lo, hi)
	}
	// the share of fair draws that fall inside is what the confidence says
	rng := rand.New(rand.NewPCG(7, 8))
	inside, runs := 0, 2000
	for i := 0; i < runs; i++ {
		k := 0
		for j := 0; j < 2000; j++ {
			if rng.Float64() < 0.05 {
				k++
			}
		}
		if k >= lo && k <= hi {
			inside++
		}
	}
	if inside < runs-6 {
		t.Errorf("%d of %d runs inside the 99.9 %% interval", inside, runs)
	}
}

func TestWilsonIntervalHoldsTheRate(t *testing.T) {
	lo, hi := Wilson(100, 2000, 0.99)
	if !(lo < 0.05 && hi > 0.05) || hi-lo > 0.03 {
		t.Errorf("[%v, %v]", lo, hi)
	}
	if lo, hi := Wilson(0, 100, 0.99); lo != 0 || hi <= 0 || hi > 0.1 {
		t.Errorf("[%v, %v]", lo, hi)
	}
	if lo, hi := Wilson(0, 0, 0.99); lo != 0 || hi != 0 {
		t.Errorf("[%v, %v]", lo, hi)
	}
	if z := normalQuantile(0.995); !near(z, 2.5758, 1e-3) {
		t.Errorf("z(0.995) = %v", z)
	}
}

func TestPercentilesInterpolate(t *testing.T) {
	v := []int64{40, 10, 30, 20}
	if Median(v) != 25 || Percentile(v, 0) != 10 || Percentile(v, 100) != 40 || !near(Percentile(v, 95), 38.5, 1e-9) || Median(nil) != 0 {
		t.Errorf("%v %v %v", Median(v), Percentile(v, 95), Median(nil))
	}
}

// rtts is n round trips around a median in microseconds.
func rtts(n int, median int64, spread int64, seed uint64) []int64 {
	rng := rand.New(rand.NewPCG(seed, seed+1))
	out := make([]int64, n)
	for i := range out {
		out[i] = median + rng.Int64N(2*spread+1) - spread
	}
	return out
}

func checkOf(r model.CalibrationResult, name string) *model.CalibrationCheck {
	if r.Checks == nil {
		return nil
	}
	for _, c := range *r.Checks {
		if string(c.Name) == name {
			return &c
		}
	}
	return nil
}

func TestADelayWithinTwoMillisecondsAndFivePercentPasses(t *testing.T) {
	exp, _ := Expect(params(func(p *model.ImpairmentParams) { p.Latency = s("50ms") })) // 100 ms added
	base := rtts(100, 1000, 100, 1)
	// 101.5 ms added: inside ±(2 ms + 5 ms)
	res := Evaluate(exp, Measurement{BaselineUs: base, Sent: 300, Received: 300, RTTUs: rtts(300, 102_500, 3000, 2)})
	if !res.WithinTolerance || checkOf(res, "latency") == nil || !checkOf(res, "latency").Ok {
		t.Errorf("%+v %v", res, *res.Detail)
	}
	if res.Expected == nil || !near(float64(*res.Expected.RttMs), 101, 0.2) || !near(float64(*res.Measured.RttMedianMs), 102.5, 0.3) || !near(float64(*res.Measured.BaselineRttMs), 1, 0.1) {
		t.Errorf("%+v %+v", res.Expected, res.Measured)
	}
	// 90 ms added: out
	res = Evaluate(exp, Measurement{BaselineUs: base, Sent: 300, Received: 300, RTTUs: rtts(300, 91_000, 3000, 3)})
	if res.WithinTolerance || checkOf(res, "latency").Ok {
		t.Errorf("a delay of 90 ms for 100 ms passed: %v", *res.Detail)
	}
	// no delay at all where one is configured
	res = Evaluate(exp, Measurement{BaselineUs: base, Sent: 300, Received: 300, RTTUs: rtts(300, 1000, 100, 4)})
	if res.WithinTolerance {
		t.Error("no delay passed")
	}
}

func TestJitterBeyondTheConfiguredOneFails(t *testing.T) {
	exp, _ := Expect(params(func(p *model.ImpairmentParams) { p.Latency, p.Jitter = s("50ms"), s("5ms") })) // 100 ms ± 10 ms
	base := rtts(100, 1000, 100, 1)
	ok := Evaluate(exp, Measurement{BaselineUs: base, Sent: 300, Received: 300, RTTUs: rtts(300, 101_000, 9000, 5)})
	if !ok.WithinTolerance || checkOf(ok, "jitter") == nil {
		t.Errorf("%v", *ok.Detail)
	}
	wide := Evaluate(exp, Measurement{BaselineUs: base, Sent: 300, Received: 300, RTTUs: rtts(300, 101_000, 40_000, 6)})
	if wide.WithinTolerance || checkOf(wide, "jitter").Ok {
		t.Errorf("a spread of 40 ms for a jitter of 10 ms passed: %v", *wide.Detail)
	}
}

func TestLossInsideTheBinomialIntervalPassesAndOutsideFails(t *testing.T) {
	exp, _ := Expect(params(func(p *model.ImpairmentParams) { p.Upload = &model.NetemParams{Loss: s("5%")} }))
	base := rtts(100, 1000, 100, 1)
	at := func(lost int) model.CalibrationResult {
		return Evaluate(exp, Measurement{BaselineUs: base, Sent: 2000, Received: 2000 - lost, RTTUs: rtts(2000-lost, 1000, 100, 7)})
	}
	if r := at(100); !r.WithinTolerance || !near(float64(*r.Measured.LossPercent), 5, 1e-6) || len(*r.Measured.LossCi99) != 2 {
		t.Errorf("%v", *r.Detail)
	}
	if r := at(66); r.WithinTolerance {
		t.Errorf("66 lost of 2000 for 5 %% passed: %v", *r.Detail)
	}
	if r := at(140); r.WithinTolerance {
		t.Errorf("140 lost of 2000 for 5 %% passed: %v", *r.Detail)
	}
	// fewer than the datagrams of the statistic is said to be indicative
	few := Evaluate(exp, Measurement{BaselineUs: base, Sent: 500, Received: 475, RTTUs: rtts(475, 1000, 100, 8)})
	if !strings.Contains(checkOf(few, "loss").Detail, "indicative") {
		t.Errorf("%v", *few.Detail)
	}
	// nothing is lost where nothing is configured: one datagram lost of 2000 is outside the interval of 0
	none, _ := Expect(nil)
	if r := Evaluate(none, Measurement{BaselineUs: base, Sent: 2000, Received: 1999, RTTUs: rtts(1999, 1000, 100, 9)}); r.WithinTolerance {
		t.Error("a loss on a clean path passed")
	}
	if r := Evaluate(none, Measurement{BaselineUs: base, Sent: 2000, Received: 2000, RTTUs: rtts(2000, 1000, 100, 9)}); !r.WithinTolerance {
		t.Errorf("a clean path failed: %v", *r.Detail)
	}
}

func TestBurstLossGetsTheWiderToleranceAndFlappingNone(t *testing.T) {
	burst, _ := Expect(params(func(p *model.ImpairmentParams) {
		p.Upload = &model.NetemParams{BurstLoss: &model.GilbertElliott{P: "10%", R: "40%"}}
	}))
	base := rtts(100, 1000, 100, 1)
	if r := Evaluate(burst, Measurement{BaselineUs: base, Sent: 2000, Received: 1640, RTTUs: rtts(1640, 1000, 100, 1)}); !r.WithinTolerance { // 18 % against 20 %
		t.Errorf("%v", *r.Detail)
	}
	if r := Evaluate(burst, Measurement{BaselineUs: base, Sent: 2000, Received: 1900, RTTUs: rtts(1900, 1000, 100, 1)}); r.WithinTolerance { // 5 %
		t.Errorf("%v", *r.Detail)
	}
	flap, _ := Expect(params(func(p *model.ImpairmentParams) { p.Flapping = &model.Flapping{Up: "5s", Down: "1s"} }))
	r := Evaluate(flap, Measurement{BaselineUs: base, Sent: 2000, Received: 1700, RTTUs: rtts(1700, 1000, 100, 1)})
	if !r.WithinTolerance || r.Expected.LossPercent != nil || !strings.Contains(*r.Detail, "flapping") {
		t.Errorf("%v", *r.Detail)
	}
}

func TestARateWithinTenPercentOfTheLimitPasses(t *testing.T) {
	exp, _ := Expect(params(func(p *model.ImpairmentParams) { p.Rate = s("2Mbit") }))
	base := rtts(100, 1000, 100, 1)
	m := Measurement{BaselineUs: base, Sent: 2000, Received: 2000, RTTUs: rtts(2000, 1000, 100, 1), RateMeasured: true}
	m.RateBps = 1_900_000
	if r := Evaluate(exp, m); !r.WithinTolerance || *r.Measured.RateBps != 1_900_000 || *r.Expected.RateBps != 2_000_000 {
		t.Errorf("%v", *r.Detail)
	}
	m.RateBps = 1_700_000
	if r := Evaluate(exp, m); r.WithinTolerance || checkOf(r, "rate").Ok {
		t.Errorf("%v", *r.Detail)
	}
	m.RateBps = 2_300_000
	if r := Evaluate(exp, m); r.WithinTolerance {
		t.Errorf("%v", *r.Detail)
	}
	m.RateMeasured = false
	if r := Evaluate(exp, m); r.WithinTolerance || checkOf(r, "rate") == nil {
		t.Errorf("an expected rate that was not measured passed: %v", *r.Detail)
	}
}

func TestWhenEverythingIsDroppedThereIsNoRoundTripToTime(t *testing.T) {
	exp, _ := Expect(params(func(p *model.ImpairmentParams) { v := true; p.Blackout = &v }))
	r := Evaluate(exp, Measurement{BaselineUs: rtts(100, 1000, 100, 1), Sent: 2000, Received: 0})
	if !r.WithinTolerance || !checkOf(r, "latency").Ok {
		t.Errorf("%v", *r.Detail)
	}
	// but where only some is dropped, no answer at all is a failure
	exp, _ = Expect(params(func(p *model.ImpairmentParams) { p.Upload = &model.NetemParams{Loss: s("5%")} }))
	r = Evaluate(exp, Measurement{BaselineUs: rtts(100, 1000, 100, 1), Sent: 2000, Received: 0})
	if r.WithinTolerance {
		t.Error("no answer at all passed")
	}
}

func TestWithoutAReferenceNothingCanBeJudged(t *testing.T) {
	exp, _ := Expect(params(func(p *model.ImpairmentParams) { p.Latency = s("50ms") }))
	r := Evaluate(exp, Measurement{Sent: 300, Received: 300, RTTUs: rtts(300, 100_000, 100, 1)})
	if r.WithinTolerance || r.Expected.RttMs != nil {
		t.Errorf("%+v", r)
	}
}
