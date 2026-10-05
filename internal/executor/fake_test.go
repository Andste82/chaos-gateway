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

// fakeStreamer is a Streamer for the watch lifecycle test (M6a-04): it hands back whatever lines
// are pushed on scripted, closing the returned channel (as a real one does) once stop is called or
// ctx ends, and reports whether its command was ever stopped — the watch lifecycle test's way of
// seeing that the executor actually killed the child instead of leaking it.
type fakeStreamer struct {
	fakeRunner
	scripted chan string
	stopped  atomic.Bool
}

func newFakeStreamer() *fakeStreamer { return &fakeStreamer{scripted: make(chan string)} }

func (f *fakeStreamer) Stream(ctx context.Context, c Command) (<-chan string, func(), error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, c)
	f.mu.Unlock()
	cctx, cancel := context.WithCancel(ctx)
	out := make(chan string)
	done := make(chan struct{})
	go func() {
		defer close(out)
		defer close(done)
		for {
			select {
			case line, ok := <-f.scripted:
				if !ok {
					return
				}
				select {
				case out <- line:
				case <-cctx.Done():
					return
				}
			case <-cctx.Done():
				return
			}
		}
	}()
	stop := func() {
		f.stopped.Store(true)
		cancel()
		<-done
	}
	return out, stop, nil
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
