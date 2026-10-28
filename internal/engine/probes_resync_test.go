package engine_test

import (
	"context"
	"net"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/probe"
)

// swapExec is an executor that can be replaced by a new one (a restart of the executor process).
type swapExec struct {
	mu  sync.Mutex
	cur *executor.Executor
}

func (s *swapExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	s.mu.Lock()
	ex := s.cur
	s.mu.Unlock()
	return apply.Local{E: ex}.Do(ctx, ops...)
}

func (s *swapExec) set(ex *executor.Executor) {
	s.mu.Lock()
	s.cur = ex
	s.mu.Unlock()
}

type quietSocket struct{ done chan struct{} }

func (s *quietSocket) Send([]byte, netip.AddrPort) error { return nil }
func (s *quietSocket) Recv() ([]byte, error)             { <-s.done; return nil, net.ErrClosed }
func (s *quietSocket) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

// P6-AUDIT-12: the DHCP clients are goroutines of the executor. When the executor restarts, the namespaces and the addresses
// stay and nobody renews the leases: the engine has to notice that a probe has no client and apply again, without waiting for
// some other apply.
func TestAProbeWithoutAClientAfterAnExecutorRestartGetsOneAgain(t *testing.T) {
	h := newHarness(t)
	var opened atomic.Int32
	newExec := func() *executor.Executor {
		ex, err := executor.New(h.k, executor.WithNetnsInode(h.k.NetnsInode), executor.WithStateFile(filepath.Join(h.dir, "executor-state.json")), executor.WithProbeSocket(func(string, string) (probe.Socket, error) {
			opened.Add(1)
			return &quietSocket{done: make(chan struct{})}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		return ex
	}
	first := newExec()
	sw := &swapExec{cur: first}
	e, err := engine.New(engine.Config{Store: h.st, Exec: sw, Clock: h.clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e = e
	h.mustApply(h.revision(func(c *model.Configuration) {
		probes := map[string]model.Probe{"3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8": {Name: "probe-a", Network: "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"}}
		c.Probes = &probes
	}))
	if n := opened.Load(); n != 1 {
		t.Fatalf("%d clients after the apply", n)
	}
	// the executor process restarts: its clients are gone, the namespace is not
	second := newExec()
	t.Cleanup(second.Close)
	sw.set(second)
	first.Close()
	if err := e.ObserveNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for opened.Load() < 2 {
		if time.Now().After(deadline) {
			snap := h.barrier()
			t.Fatalf("nobody started a client for the probe after the restart: %q gen %d", snap.LastError, snap.Generation)
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.barrier()
	// the clients run again: the observation asks for nothing more, however often it reads
	for i := 0; i < 3; i++ {
		h.clk.Advance(time.Minute)
		if err := e.ObserveNow(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	h.barrier()
	if n := opened.Load(); n != 2 {
		t.Errorf("%d clients were started", n)
	}
}
