package dnsproxy

import (
	"strconv"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// cache holds answers until their TTL ends. It is bounded: the oldest entry goes when it is full.
type cache struct {
	mu    sync.Mutex
	max   int
	clock clock.Clock
	m     map[string]*entry
	order []string
}

type entry struct {
	msg     *dns.Msg
	expires time.Time
}

func newCache(max int, c clock.Clock) *cache {
	return &cache{max: max, clock: c, m: map[string]*entry{}}
}

func cacheKey(name string, qtype uint16) string { return name + "/" + strconv.Itoa(int(qtype)) }

func (c *cache) flush() {
	c.mu.Lock()
	c.m, c.order = map[string]*entry{}, nil
	c.mu.Unlock()
}

// ttlOf is how long an answer may be kept: the smallest TTL of its records, a fixed short time for
// a name that does not exist or has no data of the type; 0 means not at all (errors).
func ttlOf(m *dns.Msg) time.Duration {
	switch m.Rcode {
	case dns.RcodeSuccess:
		if len(m.Answer) == 0 {
			return negativeTTL * time.Second
		}
	case dns.RcodeNameError:
		return negativeTTL * time.Second
	default:
		return 0
	}
	min := uint32(3600)
	for _, rr := range m.Answer {
		if rr.Header().Rrtype == dns.TypeOPT {
			continue
		}
		if t := rr.Header().Ttl; t < min {
			min = t
		}
	}
	return time.Duration(min) * time.Second
}

func (c *cache) put(key string, m *dns.Msg) {
	ttl := ttlOf(m)
	if ttl <= 0 || m.Truncated {
		return
	}
	cp := m.Copy()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[key]; !ok {
		for len(c.order) >= c.max {
			delete(c.m, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
	}
	c.m[key] = &entry{msg: cp, expires: c.clock.Now().Add(ttl)}
}

// get returns the cached answer for the query r, with the TTLs counted down.
func (c *cache) get(key string, r *dns.Msg) (*dns.Msg, bool) {
	c.mu.Lock()
	e, ok := c.m[key]
	if ok && !c.clock.Now().Before(e.expires) {
		delete(c.m, key)
		for i, k := range c.order {
			if k == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
		ok = false
	}
	var left uint32
	var m *dns.Msg
	if ok {
		m = e.msg.Copy()
		left = uint32(e.expires.Sub(c.clock.Now()) / time.Second)
	}
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	if left == 0 {
		left = 1
	}
	m.Id = r.Id
	m.Question = r.Question
	m.RecursionDesired = r.RecursionDesired
	for _, rr := range m.Answer {
		if rr.Header().Ttl > left {
			rr.Header().Ttl = left
		}
	}
	return m, true
}
