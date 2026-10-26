package tlsresponder

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/svcapi"
)

// Sink receives the outcomes of the handshakes. The API implements it behind an HTTP call.
type Sink interface {
	Post(ctx context.Context, reports []model.TlsHandshakeReport) error
}

const (
	reportBatch    = 100
	reportInterval = 500 * time.Millisecond
	reportBuffer   = 5000
)

// reporter collects the outcomes and hands them to the sink in batches, at once when the sink is idle: a check that
// waits for a handshake looks at the report soon after it. The buffer is bounded: while the API is away the oldest
// are dropped, and a handshake never waits for its report.
type reporter struct {
	sink  Sink
	clock clock.Clock
	log   *slog.Logger

	mu      sync.Mutex
	pending []model.TlsHandshakeReport
	dropped int64
	// refused counts the reports of the batches the API refused (and that were dropped)
	refused int64
	// base counts the reports that left the head of pending without being sent (dropped)
	base int64
	wake chan struct{}
}

func newReporter(s Sink, c clock.Clock, l *slog.Logger) *reporter {
	return &reporter{sink: s, clock: c, log: l, wake: make(chan struct{}, 1)}
}

func (r *reporter) add(e model.TlsHandshakeReport) {
	if r.sink == nil {
		return
	}
	r.mu.Lock()
	if len(r.pending) >= reportBuffer {
		r.pending = r.pending[1:]
		r.dropped++
		r.base++
	}
	r.pending = append(r.pending, e)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Dropped is the number of reports lost because the sink did not take them.
func (r *reporter) Dropped() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}

// Refused is the number of reports dropped because the API refused the batch they were in.
func (r *reporter) Refused() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refused
}

func (r *reporter) run(ctx context.Context) {
	if r.sink == nil {
		return
	}
	tick := r.clock.NewTicker(reportInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			r.flush(context.Background())
			return
		case <-tick.C():
		case <-r.wake:
		}
		r.flush(ctx)
	}
}

func (r *reporter) flush(ctx context.Context) {
	for {
		r.mu.Lock()
		n := len(r.pending)
		if n == 0 {
			r.mu.Unlock()
			return
		}
		n = min(n, reportBatch)
		batch := append([]model.TlsHandshakeReport(nil), r.pending[:n]...)
		startBase := r.base
		r.mu.Unlock()
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := r.sink.Post(pctx, batch)
		cancel()
		if err != nil && !svcapi.Refused(err) {
			r.log.Debug("the handshake reports were not accepted", "error", err)
			return // they stay; the next tick tries again
		}
		if err != nil {
			// the API refused their content (a 4xx other than 408 and 429): sent again they would be refused again,
			// and every later report would wait behind them, so they go
			r.log.Warn("the API refused a batch of handshake reports: they are dropped", "reports", n, "error", err)
		}
		r.mu.Lock()
		if err != nil {
			r.refused += int64(n)
		}
		// reports dropped from the head while the batch was on its way were part of it
		if gone := n - int(r.base-startBase); gone > 0 && len(r.pending) >= gone {
			r.pending = r.pending[gone:]
		}
		r.mu.Unlock()
	}
}
