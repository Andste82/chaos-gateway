package dnsproxy

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
	Post(ctx context.Context, entries []model.DnsQueryLogEntry) error
}

const (
	logBatch    = 200
	logInterval = 500 * time.Millisecond
	logBuffer   = 10000
)

// queryLog collects entries and hands them to the sink in batches. The buffer is bounded: while the
// API is away the oldest entries are dropped, and the proxy never waits for the log.
type queryLog struct {
	sink  Sink
	clock clock.Clock
	log   *slog.Logger

	mu      sync.Mutex
	pending []model.DnsQueryLogEntry
	dropped int64
	wake    chan struct{}
}

func newQueryLog(s Sink, c clock.Clock, l *slog.Logger) *queryLog {
	return &queryLog{sink: s, clock: c, log: l, wake: make(chan struct{}, 1)}
}

func (q *queryLog) add(e model.DnsQueryLogEntry) {
	if q.sink == nil {
		return
	}
	q.mu.Lock()
	if len(q.pending) >= logBuffer {
		q.pending = q.pending[1:]
		q.dropped++
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
	for {
		select {
		case <-ctx.Done():
			q.flush(context.Background())
			return
		case <-tick.C():
		case <-q.wake:
		}
		q.flush(ctx)
	}
}

func (q *queryLog) flush(ctx context.Context) {
	for {
		q.mu.Lock()
		n := len(q.pending)
		if n == 0 {
			q.mu.Unlock()
			return
		}
		if n > logBatch*5 {
			n = logBatch * 5
		}
		batch := append([]model.DnsQueryLogEntry(nil), q.pending[:n]...)
		q.mu.Unlock()
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := q.sink.Post(pctx, batch)
		cancel()
		if err != nil {
			q.log.Debug("the query log was not accepted", "error", err)
			return // the entries stay; the next tick tries again
		}
		q.mu.Lock()
		if len(q.pending) >= n {
			q.pending = q.pending[n:]
		}
		q.mu.Unlock()
	}
}
