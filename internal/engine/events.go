package engine

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
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
	EventNetworkRecovered = "network_restored"
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

// ID is the public id of the event (plan §2.15's Last-Event-ID): the boot id that produced it and
// its sequence number, so a client's last-seen id can be told apart from one of an earlier boot
// (M5-02) even though the sequence restarts at 1 every time.
func (e Event) ID(bootID string) string { return bootID + "-" + strconv.FormatUint(e.Seq, 10) }

// ParseEventID splits a Last-Event-ID into its boot id and sequence number. ok is false when it
// does not have the "<boot_id>-<seq>" shape at all (a malformed id, handled as bad_request by the
// caller, same as before M5-02).
func ParseEventID(id string) (bootID string, seq uint64, ok bool) {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.ParseUint(id[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

// Subject identifies what an event is about (plan §2.15's Event.subject), e.g. {Kind: "revision",
// ID: "7"}. Set in Data under the key "subject"; "actor" (a model.Actor) holds who caused it, for
// events an API caller, rather than the engine itself, set off (M5-06).
type Subject struct {
	Kind string
	ID   string
	Name string
}

// ReplayWindow is how long the bus keeps events for a client that reconnects with Last-Event-ID
// (plan §2.15: at least 10 minutes); ReplayMax bounds the memory. Both are package variables, not
// constants, so a test can shrink ReplayMax instead of publishing tens of thousands of events to
// force an eviction (M5-02).
var (
	ReplayWindow = 15 * time.Minute
	ReplayMax    = 20000
)

// bus fans events out to subscribers. Publishing never blocks: a subscriber whose buffer is full
// is dropped (its channel closes), as plan §3.11 prescribes; it reconnects and replays. The mutex
// protects the subscriber list and the replay buffer only and is never held while sending.
type bus struct {
	mu   sync.Mutex
	seq  uint64
	subs map[chan Event]struct{}
	// log holds the recent events in order, for replay.
	log []Event
	// bootID identifies this start of the engine (M5-02): a client's Last-Event-ID from an earlier
	// boot can never be mistaken for one of this boot's sequence numbers, which restart at 1.
	bootID string
}

func newBus() *bus { return &bus{subs: map[chan Event]struct{}{}, bootID: newBootID()} }

func newBootID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's randomness is gone: nothing safe is left to do
	}
	return hex.EncodeToString(b)
}

// BootID is this bus's boot id.
func (b *bus) BootID() string { return b.bootID }

// OldestSeq is the sequence number of the oldest event still buffered for replay, and whether
// anything is buffered at all.
func (b *bus) OldestSeq() (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.log) == 0 {
		return 0, false
	}
	return b.log[0].Seq, true
}

const subscriberBuffer = 256

func (b *bus) subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() { b.drop(ch) }
}

// subscribeFrom subscribes and returns, atomically, the buffered events after the sequence number
// last, so none is lost or doubled between replay and the live stream. A last beyond the newest
// event (a client that remembers another boot) replays nothing.
func (b *bus) subscribeFrom(last uint64) ([]Event, <-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	var replay []Event
	for _, ev := range b.log {
		if ev.Seq > last {
			replay = append(replay, ev)
		}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return replay, ch, func() { b.drop(ch) }
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
	b.log = append(b.log, ev)
	cut := 0
	for cut < len(b.log)-1 && (t.Sub(b.log[cut].Time) > ReplayWindow || len(b.log)-cut > ReplayMax) {
		cut++
	}
	if cut > 0 {
		// once the window is full, cut is almost always 1: reslicing in place (no copy) keeps
		// publish O(1) instead of O(n). The backing array is only ever compacted, and its unused
		// prefix reclaimed, once more than half of it is wasted.
		if cut > len(b.log)/2 {
			b.log = append([]Event(nil), b.log[cut:]...)
		} else {
			b.log = b.log[cut:]
		}
	}
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
