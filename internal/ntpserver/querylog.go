package ntpserver

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Sink receives the query log. The API implements it behind an HTTP call.
type Sink interface {
	Post(ctx context.Context, entries []model.NtpQueryLogEntry) error
}

const (
	logBatch    = 200
	logInterval = 500 * time.Millisecond
	logBuffer   = 10000
	// logBackoffMax bounds the wait after posts that failed: it doubles from logInterval with each failure
	logBackoffMax = 10 * time.Second
)

// queryLog collects entries and hands them to the sink in batches. The buffer is bounded: while the
// API is away the oldest entries are dropped, and the proxy never waits for the log.
type queryLog struct {
	sink  Sink
	clock clock.Clock
	log   *slog.Logger

	mu      sync.Mutex
	pending []model.NtpQueryLogEntry
	dropped int64
	// base counts the entries that left the head of pending without being sent (dropped)
	base int64
	wake chan struct{}
}

func newQueryLog(s Sink, c clock.Clock, l *slog.Logger) *queryLog {
	return &queryLog{sink: s, clock: c, log: l, wake: make(chan struct{}, 1)}
}

func (q *queryLog) add(e model.NtpQueryLogEntry) {
	if q.sink == nil {
		return
	}
	q.mu.Lock()
	if len(q.pending) >= logBuffer {
		q.pending = q.pending[1:]
		q.dropped++
		q.base++
	}
	q.pending = append(q.pending, e)
	full := len(q.pending) >= logBatch
	q.mu.Unlock()
	if full {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
}

// Dropped is the number of entries lost because the sink did not take them.
func (q *queryLog) Dropped() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

func (q *queryLog) run(ctx context.Context) {
	if q.sink == nil {
		return
	}
	tick := q.clock.NewTicker(logInterval)
	defer tick.Stop()
	// after a failed post neither a tick nor a wake (one comes with every entry once a batch is pending) starts a post
	// before retryAt, so that an API that is away is not tried once per query; the delay doubles up to logBackoffMax
	var retryAt, delay time.Duration
	for {
		select {
		case <-ctx.Done():
			q.flush(context.Background())
			return
		case <-tick.C():
		case <-q.wake:
		}
		if delay > 0 && q.clock.Monotonic() < retryAt {
			continue
		}
		if q.flush(ctx) {
			delay = 0
			continue
		}
		delay = min(max(delay*2, logInterval), logBackoffMax)
		retryAt = q.clock.Monotonic() + delay
	}
}

// flush posts the pending entries in batches until none are left; it reports false when the sink did not take a batch.
func (q *queryLog) flush(ctx context.Context) bool {
	for {
		q.mu.Lock()
		n := len(q.pending)
		if n == 0 {
			q.mu.Unlock()
			return true
		}
		if n > logBatch*5 {
			n = logBatch * 5
		}
		batch := append([]model.NtpQueryLogEntry(nil), q.pending[:n]...)
		startBase := q.base
		q.mu.Unlock()
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := q.sink.Post(pctx, batch)
		cancel()
		if err != nil {
			q.log.Debug("the query log was not accepted", "error", err)
			return false // the entries stay; run tries again after its back-off
		}
		q.mu.Lock()
		// entries dropped from the head while the batch was on its way were part of it
		if gone := n - int(q.base-startBase); gone > 0 && len(q.pending) >= gone {
			q.pending = q.pending[gone:]
		}
		q.mu.Unlock()
	}
}
