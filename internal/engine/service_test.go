package engine_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// M6b test: nothing tells the engine that the holder of the service namespace restarted (the old
// namespace lives on under its name), so it looks: a changed holder is applied again, the pair leads
// into the new namespace afterwards.
func TestAHolderThatRestartsIsNoticedAndTheNamespaceReplaced(t *testing.T) {
	h := newHarness(t)
	h.k.ServiceNamespace("cgsvc")
	h.k.AddHolder(100)
	ex, err := executor.New(h.k, executor.WithNetnsInode(h.k.NetnsInode))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	var pid atomic.Int64
	pid.Store(100)
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: ex}, Clock: h.clk, ServiceNS: "cgsvc", ServiceHolderPID: func() int { return int(pid.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e = e
	h.mustApply(h.revision(nil))
	if s := e.Snapshot().Service; s == nil || s.Name != "cgsvc" {
		t.Fatalf("%+v", e.Snapshot().Service)
	}
	first, _ := h.k.NetnsInode("/run/netns/cgsvc")
	holder, _ := h.k.NetnsInode("/proc/100/ns/net")
	if first == 0 || first != holder {
		t.Fatalf("the holder's namespace is not attached: %d, %d", first, holder)
	}

	e.WatchService(context.Background(), time.Second)
	h.clk.BlockUntil(1)
	// the holder container restarts: a new namespace, a new process
	h.k.AddHolder(101)
	pid.Store(101)
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.clk.Advance(time.Second)
		now, _ := h.k.NetnsInode("/run/netns/cgsvc")
		want, _ := h.k.NetnsInode("/proc/101/ns/net")
		if now == want && now != first {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the namespace was not replaced: %d, the holder's is %d (old %d)", now, want, first)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// and the kernel matches the target again
	snap := h.barrier()
	if snap.LastError != "" {
		t.Errorf("%s", snap.LastError)
	}
}
