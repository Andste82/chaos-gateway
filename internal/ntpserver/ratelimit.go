package ntpserver

import (
	"net/netip"
	"sync"
	"time"
)

// DefaultRateLimit is the queries per second one device may ask (plan §2.18): beyond it the server drops. It does not
// send a kiss-of-death of its own; `RATE` is a fault a test asks for.
const DefaultRateLimit = 16

const (
	// limiterEntries bounds the table of devices; the clients are those of the test networks, so this is far above
	// what a gateway has
	limiterEntries = 4096
	// dropLogInterval is how often a device's dropped queries show in the query log: a device that floods the server
	// must not fill the log, and every drop is still counted
	dropLogInterval = time.Second
	// sweepEvery is the least time between two walks over the whole table. A full table of fresh buckets (a flood of
	// distinct source addresses) would otherwise be walked for every new address under the lock that every query takes.
	sweepEvery = time.Second
)

// limiter is a token bucket per client address: a burst of one second's worth and then the rate.
type limiter struct {
	rate  float64 // per second; <= 0: no limit
	burst float64

	mu sync.Mutex
	m  map[netip.Addr]*bucket
	// lastSweep is the monotonic time of the last sweep and sweeps their number; see sweepEvery
	lastSweep time.Duration
	swept     bool
	sweeps    int
}

type bucket struct {
	tokens     float64
	last       time.Duration // monotonic time of the last refill
	dropLogged time.Duration // monotonic time of the last dropped query that was logged
	dropped    bool
}

func newLimiter(rate float64) *limiter {
	return &limiter{rate: rate, burst: max(rate, 1), m: make(map[netip.Addr]*bucket)}
}

// allow takes a token for a query of a at monotonic time now. When there is none the query is dropped, and logDrop says
// whether this drop is the one to put in the log (the first, then one per dropLogInterval).
func (l *limiter) allow(a netip.Addr, now time.Duration) (ok, logDrop bool) {
	if l.rate <= 0 {
		return true, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.m[a]
	if b == nil {
		if len(l.m) >= limiterEntries {
			l.makeRoom(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.m[a] = b
	}
	if now > b.last {
		b.tokens = min(l.burst, b.tokens+l.rate*(now-b.last).Seconds())
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, false
	}
	if !b.dropped || now-b.dropLogged >= dropLogInterval {
		b.dropped, b.dropLogged = true, now
		return false, true
	}
	return false, false
}

// makeRoom frees a place for a new device: the table is swept for the devices that have been quiet long enough to hold a
// full bucket again (at most once per sweepEvery), and when it is still full one device goes, whichever the map yields first.
// A bucket removed early only gives that device a fresh burst; the cost of an insert stays constant.
func (l *limiter) makeRoom(now time.Duration) {
	if !l.swept || now-l.lastSweep >= sweepEvery {
		l.swept, l.lastSweep = true, now
		l.sweeps++
		l.sweep(now)
	}
	for a := range l.m {
		if len(l.m) < limiterEntries {
			return
		}
		delete(l.m, a)
	}
}

// sweep removes the devices that have been quiet long enough to hold a full bucket again.
func (l *limiter) sweep(now time.Duration) {
	idle := time.Duration(l.burst / l.rate * float64(time.Second))
	for a, b := range l.m {
		if now-b.last >= idle {
			delete(l.m, a)
		}
	}
}
