// Package supervisor starts goroutines the way plan §3.11 prescribes: a panic is recovered,
// logged with its stack and reported as unhealthy instead of silently killing the process half-way,
// and a goroutine that must not die takes the process down, so the container restarts and
// recompiles from the committed revision.
package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
)

// State of a supervised goroutine.
type State string

// States.
const (
	Running  State = "running"
	Stopped  State = "stopped" // returned without error
	Failed   State = "failed"  // returned an error
	Panicked State = "panicked"
)

// Health describes one goroutine.
type Health struct {
	Name  string
	State State
	Err   string
}

// Supervisor starts and watches goroutines.
type Supervisor struct {
	log   *slog.Logger
	fatal func(name string, v any)
	wg    sync.WaitGroup
	mu    sync.Mutex
	state map[string]Health
}

// New returns a supervisor. fatal is called when a critical goroutine panics or fails; nil means
// the panic is only recorded (tests). A process passes a function that exits.
func New(log *slog.Logger, fatal func(name string, v any)) *Supervisor {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{log: log, fatal: fatal, state: map[string]Health{}}
}

// Go runs fn in a goroutine. A panic is recovered and the component marked unhealthy; the process
// keeps running.
func (s *Supervisor) Go(ctx context.Context, name string, fn func(ctx context.Context) error) {
	s.start(ctx, name, fn, false)
}

// Critical runs fn like Go, but a panic or an error that is not a context error ends the
// process through the fatal function: the state owner and the apply loop are such goroutines.
func (s *Supervisor) Critical(ctx context.Context, name string, fn func(ctx context.Context) error) {
	s.start(ctx, name, fn, true)
}

func (s *Supervisor) set(h Health) {
	s.mu.Lock()
	s.state[h.Name] = h
	s.mu.Unlock()
}

func (s *Supervisor) start(ctx context.Context, name string, fn func(ctx context.Context) error, critical bool) {
	s.set(Health{Name: name, State: Running})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("goroutine panicked", "name", name, "panic", r, "stack", string(debug.Stack()))
				s.set(Health{Name: name, State: Panicked, Err: fmt.Sprint(r)})
				if critical && s.fatal != nil {
					s.fatal(name, r)
				}
			}
		}()
		err := fn(ctx)
		switch {
		case err == nil || ctx.Err() != nil:
			s.set(Health{Name: name, State: Stopped})
		default:
			s.log.Error("goroutine failed", "name", name, "error", err)
			s.set(Health{Name: name, State: Failed, Err: err.Error()})
			if critical && s.fatal != nil {
				s.fatal(name, err)
			}
		}
	}()
}

// Wait blocks until every goroutine has returned.
func (s *Supervisor) Wait() { s.wg.Wait() }

// Health returns the state of every goroutine, sorted by name.
func (s *Supervisor) Health() []Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Health, 0, len(s.state))
	for _, h := range s.state {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Healthy reports whether no goroutine has panicked or failed.
func (s *Supervisor) Healthy() bool {
	for _, h := range s.Health() {
		if h.State == Failed || h.State == Panicked {
			return false
		}
	}
	return true
}
