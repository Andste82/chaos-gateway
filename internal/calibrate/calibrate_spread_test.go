package calibrate

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// roundTrips draws n round trips (microseconds) of two directions, each with its own delay of mu +- jitter by the
// distribution (netem: uniform on -1..1, or normal with a standard deviation of 1, cut at 4), on top of base.
func roundTrips(r *rand.Rand, n int, base, muMs float64, jitterUpMs, jitterDownMs float64, dist string) []int64 {
	one := func(j float64) float64 {
		if dist == "normal" {
			return j * math.Max(-4, math.Min(4, r.NormFloat64()))
		}
		return j * (2*r.Float64() - 1)
	}
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(base + 1000*(2*muMs+one(jitterUpMs)+one(jitterDownMs)))
	}
	return out
}

// A fault that does what it says must pass its own jitter check: with the sum of the jitters as the limit a normal
// distribution failed most runs, because the round trip of two independent normal delays spreads by sqrt(2) times one
// direction's jitter (P6-AUDIT-03).
func TestACorrectJitterPassesForEveryDistribution(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for _, c := range []struct {
		name         string
		latency, jit string
		dist         string
	}{
		{"normal 100/50", "100ms", "50ms", "normal"},
		{"normal 50/25", "50ms", "25ms", "normal"},
		{"normal 50/10", "50ms", "10ms", "normal"},
		{"uniform 100/50", "100ms", "50ms", ""},
		{"uniform 50/10", "50ms", "10ms", ""},
	} {
		d := model.DelayDistribution(c.dist)
		exp, err := Expect(params(func(p *model.ImpairmentParams) {
			p.Latency, p.Jitter = s(c.latency), s(c.jit)
			if c.dist != "" {
				p.Distribution = &d
			}
		}))
		if err != nil {
			t.Fatal(err)
		}
		if exp.SpreadMs <= 0 {
			t.Fatalf("%s: no spread expected: %+v", c.name, exp)
		}
		latMs := map[string]float64{"100ms": 100, "50ms": 50}[c.latency]
		jitMs := map[string]float64{"50ms": 50, "25ms": 25, "10ms": 10}[c.jit]
		base := rtts(100, 1000, 100, 1)
		failed := 0
		const runs = 200
		for i := 0; i < runs; i++ {
			res := Evaluate(exp, Measurement{BaselineUs: base, Sent: 1500, Received: 1500, RTTUs: roundTrips(r, 1500, 1000, latMs, jitMs, jitMs, c.dist)})
			if !checkOf(res, "jitter").Ok {
				failed++
			}
		}
		if failed > 2 {
			t.Errorf("%s: %d of %d correct runs failed the jitter check (limit from the expectation: spread %.1f ms)", c.name, failed, runs, exp.SpreadMs)
		}
	}
}

// The check still finds a jitter that is not there: a spread twice the configured one fails.
func TestAJitterTwiceTheConfiguredOneFailsForEveryDistribution(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 5))
	for _, dist := range []string{"", "normal"} {
		d := model.DelayDistribution(dist)
		exp, _ := Expect(params(func(p *model.ImpairmentParams) {
			p.Latency, p.Jitter = s("100ms"), s("50ms")
			if dist != "" {
				p.Distribution = &d
			}
		}))
		base := rtts(100, 1000, 100, 1)
		res := Evaluate(exp, Measurement{BaselineUs: base, Sent: 1500, Received: 1500, RTTUs: roundTrips(r, 1500, 1000, 100, 100, 100, dist)})
		if checkOf(res, "jitter").Ok {
			t.Errorf("%q: a jitter of 100 ms for 50 ms passed: %s", dist, *res.Detail)
		}
	}
}

func TestTheSpreadOfTheRoundTripFollowsTheDistribution(t *testing.T) {
	norm, uni := model.DelayDistribution("normal"), model.DelayDistribution("uniform")
	pareto := model.DelayDistribution("pareto")
	for name, c := range map[string]struct {
		set  func(p *model.ImpairmentParams)
		want float64
	}{
		// two normal directions: 1.645 * sqrt(2) * 50
		"normal": {func(p *model.ImpairmentParams) { p.Latency, p.Jitter, p.Distribution = s("100ms"), s("50ms"), &norm }, 116.3},
		"normal one way": {func(p *model.ImpairmentParams) {
			p.Upload = &model.NetemParams{Latency: s("100ms"), Jitter: s("50ms"), Distribution: &norm}
		}, 82.2},
		// two uniform directions: a+b-sqrt(0.4ab) = 100 - 31.6
		"uniform":          {func(p *model.ImpairmentParams) { p.Latency, p.Jitter, p.Distribution = s("100ms"), s("50ms"), &uni }, 68.4},
		"uniform one way":  {func(p *model.ImpairmentParams) { p.Upload = &model.NetemParams{Latency: s("100ms"), Jitter: s("50ms")} }, 45},
		"uniform, default": {func(p *model.ImpairmentParams) { p.Latency, p.Jitter = s("100ms"), s("50ms") }, 68.4},
		// a direction with a jitter ten times the other's: the small one hardly counts, the tail is 0.9 of the large one
		"uniform 10:1": {func(p *model.ImpairmentParams) {
			p.Upload = &model.NetemParams{Latency: s("100ms"), Jitter: s("50ms")}
			p.Download = &model.NetemParams{Latency: s("20ms"), Jitter: s("5ms")}
		}, 45},
		"no jitter": {func(p *model.ImpairmentParams) { p.Latency = s("100ms") }, 0},
		"pareto":    {func(p *model.ImpairmentParams) { p.Latency, p.Jitter, p.Distribution = s("100ms"), s("50ms"), &pareto }, 0},
	} {
		e, err := Expect(params(c.set))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !near(e.SpreadMs, c.want, 0.2) {
			t.Errorf("%s: the spread is %.2f ms, want %.1f", name, e.SpreadMs, c.want)
		}
	}
	// a distribution with a heavy tail is not held to a bound, and says so
	e, _ := Expect(params(func(p *model.ImpairmentParams) { p.Latency, p.Jitter, p.Distribution = s("100ms"), s("50ms"), &pareto }))
	if len(e.Notes) != 1 {
		t.Errorf("%v", e.Notes)
	}
	res := Evaluate(e, Measurement{BaselineUs: rtts(100, 1000, 100, 1), Sent: 300, Received: 300, RTTUs: rtts(300, 101_000, 90_000, 5)})
	if c := checkOf(res, "jitter"); c != nil {
		t.Errorf("a pareto jitter is checked: %+v", c)
	}
}

// A direction with a rate holds each packet back for its size over the rate: at 56 kbit/s a 74 byte frame takes 10.6 ms,
// which the delay check must expect (P6-AUDIT-09).
func TestTheTimeAPacketTakesAtALowRateIsPartOfTheExpectedDelay(t *testing.T) {
	p := params(func(p *model.ImpairmentParams) { p.Latency, p.Rate = s("50ms"), s("56kbit") })
	e, err := ExpectFor(p, 74)
	if err != nil {
		t.Fatal(err)
	}
	if want := 100 + 2*74*8/56.0; !near(e.AddedRTTMs, want, 0.01) {
		t.Errorf("the added round trip is %.2f ms, want %.2f", e.AddedRTTMs, want)
	}
	if plain, _ := Expect(p); plain.AddedRTTMs != 100 {
		t.Errorf("without a size nothing is added: %+v", plain)
	}
	base := rtts(100, 1000, 100, 1)
	// what the kernel does: 100 ms plus 2 * 10.6 ms
	res := Evaluate(e, Measurement{BaselineUs: base, Sent: 300, Received: 300, RTTUs: rtts(300, 122_000, 300, 7)})
	if !checkOf(res, "latency").Ok {
		t.Errorf("a correct delay at 56 kbit/s failed: %s", *res.Detail)
	}
	// and that the delay alone would have failed: the arithmetic of the audit
	plain, _ := Expect(p)
	if res := Evaluate(plain, Measurement{BaselineUs: base, Sent: 300, Received: 300, RTTUs: rtts(300, 122_000, 300, 7)}); checkOf(res, "latency").Ok {
		t.Error("the expectation without the time at the rate passes")
	}
}
