package engine

import (
	"sync"
	"time"
)

// Event types of the engine.
const (
	EventApplied          = "applied"
	EventApplyFailed      = "apply_failed"
	EventRolledBack       = "revision_rolled_back"
	EventConfirmPending   = "confirm_pending"
	EventConfirmed        = "revision_confirmed"
	EventNetworkDegraded  = "network_degraded"
	EventNetworkRecovered = "network_recovered"
	EventUplinkChanged    = "uplink_changed"
	EventObservedChanged  = "observed_changed"
)

// Event is something that happened to the gateway.
type Event struct {
	Seq  uint64
	Time time.Time
	Type string
	Data map[string]any
}

// bus fans events out to subscribers. Publishing never blocks: a subscriber whose buffer is full
// is dropped (its channel closes), as plan §3.11 prescribes; it reconnects and replays. The mutex
// protects the subscriber list only and is never held while sending.
type bus struct {
	mu   sync.Mutex
	seq  uint64
	subs map[chan Event]struct{}
}

func newBus() *bus { return &bus{subs: map[chan Event]struct{}{}} }

const subscriberBuffer = 256

func (b *bus) subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() { b.drop(ch) }
}

func (b *bus) drop(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *bus) publish(t time.Time, typ string, data map[string]any) {
	b.mu.Lock()
	b.seq++
	ev := Event{Seq: b.seq, Time: t, Type: typ, Data: data}
	var full []chan Event
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			full = append(full, ch)
		}
	}
	b.mu.Unlock()
	for _, ch := range full {
		b.drop(ch)
	}
}
