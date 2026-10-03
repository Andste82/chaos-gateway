package auth

import (
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// The login limiter: after maxFailures wrong passwords from one client the client has to wait, and
// every further failure doubles the wait up to maxWait. A success clears the record.
const (
	maxFailures = 5
	baseWait    = 5 * time.Second
	maxWait     = 15 * time.Minute
	forgetAfter = time.Hour
)

type attempts struct {
	failures int
	until    time.Time
	last     time.Time
}

type limiter struct {
	clk clock.Clock
	mu  sync.Mutex
	m   map[string]*attempts
}

func newLimiter(c clock.Clock) *limiter { return &limiter{clk: c, m: map[string]*attempts{}} }

// blocked returns how long the client still has to wait.
func (l *limiter) blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[key]
	if a == nil {
		return 0
	}
	if d := a.until.Sub(l.clk.Now()); d > 0 {
		return d
	}
	return 0
}

// fail records a failure and returns the wait it causes, 0 while the client may still try.
func (l *limiter) fail(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	for k, a := range l.m { // forget old records so the map stays small
		if now.Sub(a.last) > forgetAfter {
			delete(l.m, k)
		}
	}
	a := l.m[key]
	if a == nil {
		a = &attempts{}
		l.m[key] = a
	}
	a.failures++
	a.last = now
	if a.failures < maxFailures {
		return 0
	}
	wait := baseWait << (a.failures - maxFailures)
	if wait > maxWait || wait <= 0 {
		wait = maxWait
	}
	a.until = now.Add(wait)
	return wait
}

func (l *limiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}
