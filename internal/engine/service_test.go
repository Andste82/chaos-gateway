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

// M6b-05 test: WatchService's reading of the service namespace reaches the snapshot, so
// /system/health can report the svcns component without its own executor round-trip.
func TestWatchServicePublishesTheServiceHealthInTheSnapshot(t *testing.T) {
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
	if sh := e.Snapshot().ServiceHealth; sh != nil {
		t.Fatalf("a reading exists before the watcher ever ran: %+v", sh)
	}

	e.WatchService(context.Background(), time.Second)
	h.clk.BlockUntil(1)
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.clk.Advance(time.Second)
		if s := e.Snapshot().ServiceHealth; s != nil && s.Exists && s.HolderMatches {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the snapshot never reported a matching service namespace: %+v", e.Snapshot().ServiceHealth)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// a holder pid that was never attached: the namespace exists, but it is not its network
	pid.Store(999)
	deadline = time.Now().Add(10 * time.Second)
	for {
		h.clk.Advance(time.Second)
		if s := e.Snapshot().ServiceHealth; s != nil && s.Exists && !s.HolderMatches {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the snapshot never reported the holder mismatch: %+v", e.Snapshot().ServiceHealth)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

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

// M6b-02 test: a holder that died (its PID is gone, not merely replaced) must not make every apply
// fail until something re-creates it; the namespace is kept as it is and the health is reported.
func TestADeadHolderDoesNotBlockARevisionApply(t *testing.T) {
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

	e.WatchService(context.Background(), time.Second)
	h.clk.BlockUntil(1)
	// the holder dies: its PID is gone, nothing takes it over
	pid.Store(999)
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.clk.Advance(time.Second)
		s := e.Snapshot()
		if s.ServiceError != "" {
			if s.LastError != "" {
				t.Fatalf("a dead holder failed the apply: %s", s.LastError)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the snapshot never reported the dead holder: %+v", s)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// the namespace itself is untouched: still the one it was
	if s := e.Snapshot().Service; s == nil || s.Name != "cgsvc" {
		t.Fatalf("the namespace was dropped instead of kept: %+v", s)
	}

	// a new holder comes up: the degradation clears and the namespace is reattached to it
	h.k.AddHolder(102)
	pid.Store(102)
	deadline = time.Now().Add(10 * time.Second)
	for {
		h.clk.Advance(time.Second)
		now, _ := h.k.NetnsInode("/run/netns/cgsvc")
		want, _ := h.k.NetnsInode("/proc/102/ns/net")
		s := e.Snapshot()
		if now == want && s.ServiceError == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the namespace was never reattached to the new holder: %d, want %d (service_error=%q)", now, want, s.ServiceError)
		}
		time.Sleep(20 * time.Millisecond)
	}
	snap := h.barrier()
	if snap.LastError != "" {
		t.Errorf("%s", snap.LastError)
	}
}
