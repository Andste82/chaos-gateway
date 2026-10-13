package api

import (
	"sync"
	"time"
)

// matchTracker remembers when the counter of an access rule was last seen growing (plan §2.17, the
// rule editor's "last match"). The kernel keeps no time of a rule's last match, so every read of the
// counters notes the readings: the time of the first read that found a counter higher than the read
// before. The result lies between two reads; the UI reads every 5 s while the rules are shown.
type matchTracker struct {
	mu   sync.Mutex
	seen map[string]matchSeen
}

type matchSeen struct {
	packets, epoch int64
	last           time.Time
}

// observe notes a reading of the counter `name` at `now` and returns the time it was last seen
// growing, nil when no read has seen it grow since the gateway started.
func (m *matchTracker) observe(name string, packets, epoch int64, now time.Time) *time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == nil {
		m.seen = map[string]matchSeen{}
	}
	s, ok := m.seen[name]
	switch {
	case !ok:
		// the first reading: what it counted may be from any time before
		s = matchSeen{packets: packets, epoch: epoch}
	case epoch != s.epoch:
		// the counter started over (a new rule, a new table): what it counted came after the last read
		s = matchSeen{packets: packets, epoch: epoch, last: s.last}
		if packets > 0 {
			s.last = now
		}
	case packets > s.packets:
		s.packets, s.last = packets, now
	default:
		s.packets = packets
	}
	m.seen[name] = s
	if s.last.IsZero() {
		return nil
	}
	t := s.last
	return &t
}
