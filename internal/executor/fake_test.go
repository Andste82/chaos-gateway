package executor

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// fakeRunner records commands and answers them from a script.
type fakeRunner struct {
	mu       sync.Mutex
	cmds     []Command
	respond  func(Command) (Result, error)
	delay    time.Duration
	inflight atomic.Int32
	overlap  atomic.Bool
}

func (f *fakeRunner) Run(ctx context.Context, c Command) (Result, error) {
	if f.inflight.Add(1) > 1 {
		f.overlap.Store(true)
	}
	defer f.inflight.Add(-1)
	f.mu.Lock()
	f.cmds = append(f.cmds, c)
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.respond != nil {
		return f.respond(c)
	}
	return Result{}, nil
}

func (f *fakeRunner) commands() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.cmds...)
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
