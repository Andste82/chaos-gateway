package compiler

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file turns the parameters of an impairment fault into a netem configuration: the leaf of a
// class of the tc tree (plan §3.3). A configuration is always a COMPLETE parameter set (plan
// §3.2): `tc qdisc change` keeps every netem attribute it is not given (spike S2 showed a rate
// limit surviving a change), and some stay even when they are given a zero: the correlation of a
// loss, for instance, is not reset by `loss random 100%`. Every attribute is therefore written,
// with its neutral value where the fault does not use it, and every correlation with 0%. The
// kernel behaviour behind each choice was proven in the persistent VM (kernel 6.8.0-142, iproute2
// 6.19) before this code was written; see docs/development.md "Faults and tc (M8a)".

// Netem is the complete parameter set of one netem qdisc.
type Netem struct {
	// Limit is the queue limit in packets: the fault's explicit value, or the computed one.
	Limit int `json:"limit"`
	// LimitExplicit says the fault named the limit (a small buffer on purpose); a computed limit is
	// capped by the interface's memory budget, an explicit one never is.
	LimitExplicit bool `json:"limit_explicit,omitempty"`

	Delay  time.Duration `json:"delay"`
	Jitter time.Duration `json:"jitter"`
	// Distribution is "normal", "pareto" or "paretonormal"; empty is uniform, which tc cannot name:
	// it is netem's behaviour without a distribution table. A qdisc that has a table keeps it through
	// a `change`, so going back to empty needs the qdisc to be re-created (a concern of the fault
	// engine, M8b).
	Distribution string `json:"distribution,omitempty"`

	// Loss in percent: random loss (with LossCorr) or, with Gemodel, the Gilbert-Elliott model.
	Loss     float64  `json:"loss"`
	LossCorr float64  `json:"loss_corr"`
	Gemodel  *Gemodel `json:"gemodel,omitempty"`

	Reorder   float64 `json:"reorder"`
	Duplicate float64 `json:"duplicate"`
	Corrupt   float64 `json:"corrupt"`

	// Rate in bit/s, 0 for none: the fault's rate, or the rate that keep_order stands for.
	Rate int64 `json:"rate"`

	// Flapping is the timed blackout that follows the up phase this configuration describes; the
	// fault engine (M8b) toggles to Down().
	Flapping *FlapSpec `json:"flapping,omitempty"`
}

// Gemodel is the Gilbert-Elliott burst-loss model in the terms of tc: `loss gemodel p r 1-h 1-k`.
type Gemodel struct {
	P, R float64 // probability good -> bad, bad -> good, in percent
	// LossBad (1-h) and LossGood (1-k) are the loss probabilities in the bad and the good state.
	LossBad, LossGood float64
}

// FlapSpec is a flapping fault: the connection is up for Up, then down (blackout) for Down.
type FlapSpec struct {
	Up   time.Duration `json:"up"`
	Down time.Duration `json:"down"`
}

// KeepOrderRate is the netem rate that "keep order" stands for when the fault has no rate of its
// own: netem's rate makes packets leave in order, and the rate has to be high enough not to be
// felt. The plan caps the link speed it would use at 1 Gbit/s; the compiler does not know the speed
// of an interface, so it uses the cap.
const KeepOrderRate = 1_000_000_000

// MinQueueLimit is netem's own default limit and the floor of a computed one.
const MinQueueLimit = 1000

// QueuePacketSize is the packet size the queue limit is computed with (an Ethernet MTU).
const QueuePacketSize = 1500

// DefaultQueueBudget is the memory budget per interface for the queues of computed limits.
const DefaultQueueBudget = 256 << 20

// IsNeutral reports whether the configuration impairs nothing.
func (n Netem) IsNeutral() bool {
	return n.Delay == 0 && n.Jitter == 0 && n.Loss == 0 && n.Gemodel == nil && n.Reorder == 0 &&
		n.Duplicate == 0 && n.Corrupt == 0 && n.Rate == 0 && !n.LimitExplicit && n.Flapping == nil
}

// Down is the blackout phase of a flapping fault: the same configuration with loss 100%.
func (n Netem) Down() Netem {
	n.Loss, n.LossCorr, n.Gemodel, n.Flapping = 100, 0, nil, nil
	return n
}

// Args returns the arguments of the netem qdisc after its kind: every attribute, neutral or not.
func (n Netem) Args() []string {
	a := []string{"netem", "limit", strconv.Itoa(n.Limit),
		"delay", fmtDuration(n.Delay), fmtDuration(n.Jitter), "0%"}
	if n.Distribution != "" {
		a = append(a, "distribution", n.Distribution)
	}
	if g := n.Gemodel; g != nil {
		a = append(a, "loss", "gemodel", fmtPercent(g.P), fmtPercent(g.R), fmtPercent(g.LossBad), fmtPercent(g.LossGood))
	} else {
		a = append(a, "loss", "random", fmtPercent(n.Loss), fmtPercent(n.LossCorr))
	}
	return append(a,
		"reorder", fmtPercent(n.Reorder), "0%",
		"duplicate", fmtPercent(n.Duplicate), "0%",
		"corrupt", fmtPercent(n.Corrupt), "0%",
		"rate", fmtRate(n.Rate))
}

// String is the netem arguments as one line, for golden files and logs.
func (n Netem) String() string { return strings.Join(n.Args(), " ") }

// Summary is the parameters that matter in words ("200ms ±50ms, loss 5%"), for the preview and
// explain.
func (n Netem) Summary() string {
	var p []string
	if n.Delay > 0 {
		s := fmtDuration(n.Delay)
		if n.Jitter > 0 {
			s += " ±" + fmtDuration(n.Jitter)
		}
		p = append(p, s)
	}
	switch {
	case n.Gemodel != nil:
		p = append(p, "burst loss")
	case n.Loss >= 100:
		p = append(p, "blackout")
	case n.Loss > 0:
		p = append(p, "loss "+fmtPercent(n.Loss))
	}
	if n.Reorder > 0 {
		p = append(p, "reorder "+fmtPercent(n.Reorder))
	}
	if n.Duplicate > 0 {
		p = append(p, "duplicate "+fmtPercent(n.Duplicate))
	}
	if n.Corrupt > 0 {
		p = append(p, "corrupt "+fmtPercent(n.Corrupt))
	}
	if n.Rate > 0 {
		p = append(p, "rate "+fmtRate(n.Rate))
	}
	if n.LimitExplicit {
		p = append(p, fmt.Sprintf("queue %d", n.Limit))
	}
	if n.Flapping != nil {
		p = append(p, fmt.Sprintf("flapping %s up, %s down", n.Flapping.Up, n.Flapping.Down))
	}
	if len(p) == 0 {
		return "no impairment"
	}
	return strings.Join(p, ", ")
}

// fmtDuration renders a duration in the units tc takes: whole milliseconds, else microseconds.
func fmtDuration(d time.Duration) string {
	us := d.Microseconds()
	if us%1000 == 0 {
		return fmt.Sprintf("%dms", us/1000)
	}
	return fmt.Sprintf("%dus", us)
}

// fmtPercent renders a percentage the way the API wrote it, without trailing zeros.
func fmtPercent(p float64) string { return strconv.FormatFloat(p, 'f', -1, 64) + "%" }

// fmtRate renders a rate in the largest unit that keeps it exact; 0 is "0bit" (netem: no rate).
func fmtRate(bits int64) string {
	switch {
	case bits == 0:
		return "0bit"
	case bits%1_000_000_000 == 0:
		return fmt.Sprintf("%dGbit", bits/1_000_000_000)
	case bits%1_000_000 == 0:
		return fmt.Sprintf("%dMbit", bits/1_000_000)
	case bits%1_000 == 0:
		return fmt.Sprintf("%dkbit", bits/1_000)
	}
	return fmt.Sprintf("%dbit", bits)
}

// parsePercent parses "0.5%" of the API.
func parsePercent(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || v < 0 || v > 100 || math.IsNaN(v) {
		return 0, fmt.Errorf("%q is not a percentage", s)
	}
	return v, nil
}

// parseRate parses "2Mbit" of the API into bit/s (decimal units, like tc).
func parseRate(s string) (int64, error) {
	units := []struct {
		suffix string
		mult   float64
	}{{"Gbit", 1e9}, {"Mbit", 1e6}, {"kbit", 1e3}, {"bit", 1}}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(num, 64)
			if err != nil || v < 0 {
				break
			}
			return int64(math.Round(v * u.mult)), nil
		}
	}
	return 0, fmt.Errorf("%q is not a bit rate", s)
}

func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not a duration", s)
	}
	return d, nil
}

// netemFrom builds the complete configuration of a direction from the fault's parameters. The queue
// limit is not computed here (it depends on the interface's budget): Limit is the explicit one, or 0.
func netemFrom(p *model.NetemParams) (Netem, error) {
	var n Netem
	var err error
	pct := func(v *model.Percentage, dst *float64) {
		if v != nil && err == nil {
			*dst, err = parsePercent(*v)
		}
	}
	if p.Latency != nil && err == nil {
		n.Delay, err = parseDuration(*p.Latency)
	}
	if p.Jitter != nil && err == nil {
		n.Jitter, err = parseDuration(*p.Jitter)
	}
	// netem clamps a jitter above the delay at 0 per packet, which skews the distribution: validation
	// refuses it, and the compiler does not trust that
	if n.Jitter > n.Delay {
		n.Jitter = n.Delay
	}
	// a distribution shapes the jitter: tc refuses one without ("distribution specified but no
	// latency and jitter values", found by the kernel gate), and without jitter it changes nothing
	if p.Distribution != nil && *p.Distribution != "uniform" && n.Jitter > 0 {
		n.Distribution = string(*p.Distribution)
	}
	pct(p.Loss, &n.Loss)
	pct(p.LossCorrelation, &n.LossCorr)
	pct(p.Reorder, &n.Reorder)
	pct(p.Duplicate, &n.Duplicate)
	pct(p.Corrupt, &n.Corrupt)
	if g := p.BurstLoss; g != nil && err == nil {
		ge := &Gemodel{LossBad: 100, LossGood: 0}
		pct(&g.P, &ge.P)
		pct(&g.R, &ge.R)
		var h, k float64
		if g.H != nil {
			pct(g.H, &h)
			ge.LossBad = 100 - h
		}
		if g.K != nil {
			pct(g.K, &k)
			ge.LossGood = 100 - k
		}
		n.Gemodel, n.Loss, n.LossCorr = ge, 0, 0
	}
	if p.Blackout != nil && *p.Blackout {
		n.Loss, n.LossCorr, n.Gemodel = 100, 0, nil
	}
	if p.Rate != nil && err == nil {
		n.Rate, err = parseRate(*p.Rate)
	}
	if p.KeepOrder != nil && *p.KeepOrder && n.Rate == 0 {
		n.Rate = KeepOrderRate
	}
	if p.QueueLimit != nil {
		n.Limit, n.LimitExplicit = *p.QueueLimit, true
	}
	if f := p.Flapping; f != nil && err == nil {
		var up, down time.Duration
		if up, err = parseDuration(f.Up); err == nil {
			down, err = parseDuration(f.Down)
		}
		n.Flapping = &FlapSpec{Up: up, Down: down}
	}
	if n.Reorder > 0 && n.Delay == 0 {
		n.Reorder = 0 // reordering needs a delay (tc refuses it otherwise); validation says so too
	}
	return n, err
}

// computedLimit is the queue limit of a fault that names none (plan §2.5): the packets that fit in
// delay × rate, where rate is the fault's rate or, without one, the cap of the link speed (1 Gbit/s),
// never below netem's own default of 1000 and never above the budget share of the class.
func computedLimit(n Netem, classes int, budget int64) int {
	rate := n.Rate
	if rate == 0 {
		rate = KeepOrderRate
	}
	worst := n.Delay + n.Jitter
	pkts := int64(math.Ceil(float64(rate) / 8 * worst.Seconds() / QueuePacketSize))
	if pkts < MinQueueLimit {
		pkts = MinQueueLimit
	}
	if classes < 1 {
		classes = 1
	}
	if share := budget / QueuePacketSize / int64(classes); pkts > share {
		pkts = share
	}
	if pkts < MinQueueLimit {
		pkts = MinQueueLimit
	}
	return int(pkts)
}
