package executor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

const readRules = `{"type":"read","what":"rules"}`

// blockingRunner holds every write command (anything that is not a read of the rules or the
// counters) until release is closed; it tells started when the first one is running.
func blockingRunner() (fr *fakeRunner, started chan struct{}, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	fr = &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolNft && c.Stdin != "" {
			once.Do(func() { close(started) })
			<-release
		}
		if c.Tool == ToolIP {
			return Result{Stdout: `[]`}, nil
		}
		return Result{}, nil
	}}
	return fr, started, release
}

// A read does not wait for the writer (plan §3.11): while a long plan runs, a read of the rules
// completes, and so does a read through the client over the socket (M8a).
func TestAReadCompletesWhileALongPlanRuns(t *testing.T) {
	fr, started, release := blockingRunner()
	path, e := startServer(t, fr, nil)
	c := dial(t, path)
	planDone := make(chan error, 1)
	go func() { _, err := e.Do(context.Background(), mustDecode(t, nftOp)); planDone <- err }()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// directly on the executor
	if out, err := e.Do(ctx, mustDecode(t, readRules)); err != nil || len(out.Data) != 1 {
		t.Fatalf("a read beside the plan: %+v %v", out, err)
	}
	// through the client, while the same client has a plan on its main connection
	clientPlan := make(chan error, 1)
	go func() { _, err := c.Do(context.Background(), mustDecode(t, nftOp)); clientPlan <- err }()
	for e.waiting() < 1 {
		time.Sleep(100 * time.Microsecond)
	}
	var rules []map[string]any
	if _, err := c.Read(ctx, Read{What: ReadRules}, &rules); err != nil {
		t.Fatalf("a read through the client beside two plans: %v", err)
	}
	select {
	case err := <-planDone:
		t.Fatalf("the plan ended before it was released: %v", err)
	default:
	}
	close(release)
	if err := <-planDone; err != nil {
		t.Fatal(err)
	}
	if err := <-clientPlan; err != nil {
		t.Fatal(err)
	}
}

// Writes stay one at a time and keep their order and priorities with reads going on beside them.
func TestReadsBesideThePlansDoNotBreakTheWritersOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	var writing, overlap atomic.Int32
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolNft && c.Stdin != "" {
			if writing.Add(1) > 1 {
				overlap.Add(1)
			}
			mu.Lock()
			order = append(order, c.Stdin)
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			writing.Add(-1)
		}
		return Result{Stdout: `[]`}, nil
	}}
	e := newExec(t, fr)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = e.Do(context.Background(), mustDecode(t, nftOp)) }()
		go func() {
			defer wg.Done()
			if _, err := e.Do(context.Background(), mustDecode(t, readRules)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if overlap.Load() != 0 {
		t.Errorf("%d writes overlapped", overlap.Load())
	}
	if len(order) != 30 {
		t.Errorf("%d writes ran, want 30", len(order))
	}
}

// At most readerSlots reads run at the same time; the next one waits for a slot, not for the writer.
func TestReadsAreBoundedByTheReaderSlots(t *testing.T) {
	var running, peak atomic.Int32
	hold := make(chan struct{})
	reached := make(chan struct{}, 16)
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		reached <- struct{}{}
		<-hold
		return Result{Stdout: `[]`}, nil
	}}
	e := newExec(t, fr)
	var wg sync.WaitGroup
	for i := 0; i < readerSlots+3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Do(context.Background(), mustDecode(t, readRules)); err != nil {
				t.Error(err)
			}
		}()
	}
	for i := 0; i < readerSlots; i++ {
		<-reached
	}
	time.Sleep(20 * time.Millisecond) // the surplus reads had time to start, if they could
	if got := running.Load(); got != readerSlots {
		t.Errorf("%d reads run at once, want %d", got, readerSlots)
	}
	// a read that waits for a slot gives up with its context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := e.Do(ctx, mustDecode(t, readRules)); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a read without a slot: %v", err)
	}
	close(hold)
	wg.Wait()
	if peak.Load() > readerSlots {
		t.Errorf("peak %d", peak.Load())
	}
}

// Every operation carries when it was enqueued and when it started (plan §3.11): the difference is
// the wait for the writer; it crosses the socket.
func TestOperationsCarryTheirEnqueueAndStartTimes(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC))
	fr, started, release := blockingRunner()
	path, e := startServer(t, fr, nil)
	e.clock = clk
	c := dial(t, path)

	type res struct {
		out Outcome
		err error
	}
	first, second := make(chan res, 1), make(chan res, 1)
	go func() { o, err := c.Do(context.Background(), mustDecode(t, nftOp)); first <- res{o, err} }()
	<-started
	go func() { o, err := e.Do(context.Background(), mustDecode(t, nftOp)); second <- res{o, err} }()
	for e.waiting() < 1 {
		time.Sleep(100 * time.Microsecond)
	}
	clk.Advance(250 * time.Millisecond)
	close(release)
	a, b := <-first, <-second
	if a.err != nil || b.err != nil {
		t.Fatal(a.err, b.err)
	}
	if a.out.Enqueued.IsZero() || a.out.Started.IsZero() || a.out.Started.Before(a.out.Enqueued) {
		t.Errorf("the first request: %+v", a.out)
	}
	if got := b.out.QueueWait(); got < 250*time.Millisecond {
		t.Errorf("the second request waited %v behind the first, want at least 250ms (enqueued %v, started %v)", got, b.out.Enqueued, b.out.Started)
	}
	// a read has stamps too, and no wait for the writer
	out, err := c.Do(context.Background(), mustDecode(t, readRules))
	if err != nil || out.Enqueued.IsZero() || out.Started.IsZero() || out.QueueWait() != 0 {
		t.Errorf("a read: %+v %v", out, err)
	}
}

// Closing the executor refuses new reads and waits for the running ones.
func TestCloseRefusesReadsAndWaitsForTheRunningOnes(t *testing.T) {
	inRead := make(chan struct{})
	hold := make(chan struct{})
	fr := &fakeRunner{respond: func(Command) (Result, error) {
		close(inRead)
		<-hold
		return Result{Stdout: `[]`}, nil
	}}
	e, err := New(fr)
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() { _, err := e.Do(context.Background(), mustDecode(t, readRules)); readDone <- err }()
	<-inRead
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a read was running")
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := e.Do(context.Background(), mustDecode(t, readRules)); !errors.Is(err, ErrClosed) {
		t.Errorf("a read after Close began: %v", err)
	}
	close(hold)
	<-closed
	if err := <-readDone; err != nil {
		t.Errorf("the running read: %v", err)
	}
}

// A request mixing a read with a write is a write: it goes through the writer's queue.
func TestAMixedRequestIsAWriteAndWaitsInTheQueue(t *testing.T) {
	fr, started, release := blockingRunner()
	e := newExec(t, fr)
	go func() { _, _ = e.Do(context.Background(), mustDecode(t, nftOp)) }()
	<-started
	done := make(chan error, 1)
	go func() {
		_, err := e.DoBatch(context.Background(), ops(t, readRules, nftOp))
		done <- err
	}()
	for e.waiting() < 1 {
		time.Sleep(100 * time.Microsecond)
	}
	select {
	case err := <-done:
		t.Fatalf("a mixed request ran beside the plan: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
