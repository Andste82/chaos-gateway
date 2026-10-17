package api

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The defaults of the uplink traffic history (issue #63): a reading as often as the overview polled
// the counters before, kept as long as the chart looks back (plan §2.17, "uplink traffic · last 10 min").
const (
	defaultTrafficInterval = 5 * time.Second
	defaultTrafficPeriod   = 10 * time.Minute
)

// uplinkTraffic is the history of the uplink's rates: the API reads the uplink interface's byte
// counters every interval and keeps the rate between two readings for period. It lives in memory
// only; after a restart of the API it starts empty.
type uplinkTraffic struct {
	interval, period time.Duration

	mu sync.Mutex
	// samples is oldest first; at most period/interval of them are kept
	samples []trafficSample
	// last is the previous reading the next one is compared with; nil after a reading without counters
	last     *trafficReading
	iface    string
	counters bool
}

type trafficSample struct {
	// mono is the monotonic time of the reading, by which the sample is evicted: the wall clock may
	// jump (a Raspberry Pi without a real-time clock at its first NTP sync)
	mono time.Duration
	model.UplinkTrafficSample
}

type trafficReading struct {
	name   string
	rx, tx int64
	mono   time.Duration
}

// record adds a reading of the counters of the uplink interface name (ok: the interface has them)
// taken at wall and mono. A reading of another interface than the previous one, or with a counter
// lower than before (the interface was re-created, or a counter wrapped), gives no rate for its
// interval; it is the start of the next one.
func (u *uplinkTraffic) record(name string, rx, tx int64, ok bool, wall time.Time, mono time.Duration) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.iface, u.counters = name, ok
	cut := 0
	for cut < len(u.samples) && u.samples[cut].mono <= mono-u.period {
		cut++
	}
	u.samples = append(u.samples[:0], u.samples[cut:]...)
	if !ok {
		u.last = nil
		return
	}
	if l := u.last; l != nil && l.name == name && mono > l.mono && rx >= l.rx && tx >= l.tx {
		dt := (mono - l.mono).Seconds()
		u.samples = append(u.samples, trafficSample{mono: mono, UplinkTrafficSample: model.UplinkTrafficSample{
			Time:    wall.UTC().Truncate(time.Millisecond),
			DownBps: float64(rx-l.rx) * 8 / dt,
			UpBps:   float64(tx-l.tx) * 8 / dt,
		}})
	}
	u.last = &trafficReading{name: name, rx: rx, tx: tx, mono: mono}
}

// view returns the history, only the samples taken after since when it is given.
func (u *uplinkTraffic) view(since *time.Time) model.UplinkTraffic {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := model.UplinkTraffic{IntervalSeconds: int(u.interval / time.Second), PeriodSeconds: int(u.period / time.Second),
		Counters: u.counters, Samples: []model.UplinkTrafficSample{}}
	if u.iface != "" {
		out.Interface = &u.iface
	}
	for _, s := range u.samples {
		if since == nil || s.Time.After(*since) {
			out.Samples = append(out.Samples, s.UplinkTrafficSample)
		}
	}
	return out
}

// trafficSampler reads the uplink's counters into the history every interval until stop.
type trafficSampler struct {
	mu      sync.Mutex
	timer   clock.Timer
	stopped bool
}

// startTrafficSampler takes the first reading at once and the next ones on the server's clock.
func (s *Server) startTrafficSampler() {
	var tick func()
	tick = func() {
		s.sampleUplink()
		s.sampler.mu.Lock()
		defer s.sampler.mu.Unlock()
		if !s.sampler.stopped {
			s.sampler.timer = s.clk.AfterFunc(s.traffic.interval, tick)
		}
	}
	tick()
}

func (s *Server) stopTrafficSampler() {
	s.sampler.mu.Lock()
	defer s.sampler.mu.Unlock()
	s.sampler.stopped = true
	if s.sampler.timer != nil {
		s.sampler.timer.Stop()
	}
}

// sampleUplink reads the counters of the uplink interface: the applied one, or before an apply the
// host's interface the active configuration names (as GET /uplink).
func (s *Server) sampleUplink() {
	var name string
	snap := s.cfg.Engine.Snapshot()
	if snap.Applied != nil {
		name = snap.Applied.Uplink.Name
	} else if _, cfg, err := s.cfg.Store.Active(); err == nil {
		if l, ok := resolveLink(snap.Host, cfg.Uplink.Interface); ok {
			name = l.Name
		}
	}
	var rx, tx int64
	ok := false
	if name != "" {
		rx, tx, ok = linkBytes(name)
	}
	s.traffic.record(name, rx, tx, ok, s.clk.Now(), s.clk.Monotonic())
}

// GetUplinkTraffic implements GET /uplink/traffic.
func (s *Server) GetUplinkTraffic(c *gin.Context, params model.GetUplinkTrafficParams) {
	c.JSON(200, s.traffic.view(params.Since))
}
