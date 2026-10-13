package api

import (
	"sync"
	"time"
)

// matchTracker remembers when the counter of an access rule was last seen growing (plan §2.17, the
// rule editor's "last match"). The kernel keeps no time of a rule's last match, so every read of the
// counters notes the readings: the time of the first read that found a counter higher than the read
// before. The result lies between two reads; the UI reads every 5 s while the rules are shown.
//
// Requests read the counters concurrently and may note their readings in any order: a reading older
// than the newest one noted for a counter changes nothing, and within an epoch the highest reading
// is kept, so a late reading never makes the counter seem to grow again.
type matchTracker struct {
	mu   sync.Mutex
	seen map[string]matchSeen
	// pruned is the time of the reading the entries were last pruned against
	pruned time.Time
}

type matchSeen struct {
	packets, epoch int64
	last           time.Time
	// at is the time of the newest reading noted
	at time.Time
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
		s = matchSeen{packets: packets, epoch: epoch, at: now}
	case now.Before(s.at):
		// a reading taken before the newest one noted (a concurrent request that finished later)
	case epoch != s.epoch:
		// the counter started over (a new rule, a new table): what it counted came after the last read
		s = matchSeen{packets: packets, epoch: epoch, last: s.last, at: now}
		if packets > 0 {
			s.last = now
		}
	case packets > s.packets:
		s.packets, s.last, s.at = packets, now, now
	default:
		// a counter does not shrink within an epoch: the highest reading stays
		s.at = now
	}
	m.seen[name] = s
	if s.last.IsZero() {
		return nil
	}
	t := s.last
	return &t
}

// prune forgets the counters a reading at `now` does not have (the rules and overlays that are gone),
// so the tracker does not grow with every overlay rule ever made. A reading older than the one pruned
// against last is ignored: it may miss a counter that a newer one has.
func (m *matchTracker) prune(present func(name string) bool, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.Before(m.pruned) {
		return
	}
	m.pruned = now
	for name := range m.seen {
		if !present(name) {
			delete(m.seen, name)
		}
	}
}

// len returns the number of counters the tracker remembers.
func (m *matchTracker) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen)
}
