package ntpserver

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// freePorts returns n UDP ports on the loopback that nothing listens on.
func freePorts(t testing.TB, n int) []int {
	t.Helper()
	var ports []int
	var conns []net.PacketConn
	for range n {
		c, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		ports = append(ports, c.LocalAddr().(*net.UDPAddr).Port)
	}
	for _, c := range conns {
		_ = c.Close()
	}
	return ports
}

// startServer runs Serve on the loopback with the base port and returns a function that stops it.
func startServer(t testing.TB, s *Server) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ListenUDP("127.0.0.1")) }()
	select {
	case <-s.Ready():
	case err := <-done:
		t.Fatalf("Serve: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the server does not listen")
	}
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve does not stop")
		}
	}
}

// exchange sends one query from the loopback address src (any of 127.0.0.0/8) to port and returns the reply; nil when none
// comes within wait.
func exchange(t testing.TB, src string, port int, wait time.Duration, q []byte) *Packet {
	t.Helper()
	c, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP(src)}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write(q); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		return nil
	}
	p, err := Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return &p
}

func TestTheServerFollowsThePortsOfTheConfiguration(t *testing.T) {
	ports := freePorts(t, 3)
	base, redirect, other := ports[0], ports[1], ports[2]
	s, _ := newServer(t, func(o *Options) { o.BasePort = base; o.Clock = nil })
	stop := startServer(t, s)
	defer stop()
	q := request(4, 77)
	// before a configuration only the probe is answered, on the base port only
	if p := exchange(t, "127.0.0.1", base, 200*time.Millisecond, q); p != nil {
		t.Fatal("a query was answered before the first configuration")
	}
	if p := exchange(t, "127.0.0.1", base, time.Second, Packet{Version: 4, Mode: ModeClient, Transmit: probeTransmit}.Marshal()); p == nil {
		t.Fatal("the probe is not answered")
	}
	if err := Probe(fmt.Sprintf("127.0.0.1:%d", base)); err != nil {
		t.Fatal(err)
	}
	if exchange(t, "127.0.0.1", redirect, 100*time.Millisecond, q) != nil {
		t.Fatal("a port nobody asked for is open")
	}

	// the configuration opens the redirect port; the fault is for 127.0.0.2 on it
	s.SetConfig(config(t, []int{base, redirect},
		fault(ovA, redirect, time.Now(), []string{"127.0.0.2"}, `"offset":"+400d"`)))
	waitFor(t, "the redirect port opens", func() bool { return exchange(t, "127.0.0.2", redirect, 100*time.Millisecond, q) != nil })
	if p := exchange(t, "127.0.0.2", redirect, time.Second, q); p == nil || p.Transmit.Time(time.Now().Add(400*24*time.Hour)).Year() < 2027 {
		t.Errorf("the fault is not served on its port: %+v", p)
	}
	if p := exchange(t, "127.0.0.2", base, time.Second, q); p == nil || p.Transmit.Time(time.Now()).Year() > 2027 {
		t.Errorf("the base port serves the fault of another port: %+v", p)
	}

	// the next one names another port: the first is closed, the new one opens, the base port stays
	s.SetConfig(config(t, []int{base, other}))
	waitFor(t, "the new port opens", func() bool { return exchange(t, "127.0.0.1", other, 100*time.Millisecond, q) != nil })
	waitFor(t, "the old port closes", func() bool { return exchange(t, "127.0.0.1", redirect, 50*time.Millisecond, q) == nil })
	if exchange(t, "127.0.0.1", base, time.Second, q) == nil {
		t.Error("the base port closed")
	}
	// and a configuration without any listen entry keeps the base port
	s.SetConfig(config(t, nil))
	waitFor(t, "the other port closes", func() bool { return exchange(t, "127.0.0.1", other, 50*time.Millisecond, q) == nil })
	if exchange(t, "127.0.0.1", base, time.Second, q) == nil {
		t.Error("the base port closed")
	}
}

func waitFor(t testing.TB, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPortThatCannotBeOpenedIsLoggedAndTheOthersWork(t *testing.T) {
	ports := freePorts(t, 3)
	blocker, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", ports[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }()
	s, _ := newServer(t, func(o *Options) { o.BasePort = ports[0]; o.Clock = nil; o.OpenRetry = 50 * time.Millisecond })
	stop := startServer(t, s)
	defer stop()
	s.SetConfig(config(t, []int{ports[0], ports[1], ports[2]}))
	waitFor(t, "the free port opens", func() bool { return exchange(t, "127.0.0.1", ports[2], 100*time.Millisecond, request(4, 1)) != nil })
	if exchange(t, "127.0.0.1", ports[1], 100*time.Millisecond, request(4, 1)) != nil {
		t.Fatal("the port that is in use answers")
	}
	// the port is free again: the same configuration, no new one is sent, and the server opens it by itself
	_ = blocker.Close()
	waitFor(t, "the port that was in use opens when it is free", func() bool {
		return exchange(t, "127.0.0.1", ports[1], 100*time.Millisecond, request(4, 1)) != nil
	})
}

func TestTheBasePortIsFatalWhenItCannotBeOpened(t *testing.T) {
	blocker, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }()
	s, _ := newServer(t, func(o *Options) { o.BasePort = blocker.LocalAddr().(*net.UDPAddr).Port })
	if err := s.Serve(context.Background(), ListenUDP("127.0.0.1")); err == nil {
		t.Fatal("Serve did not fail")
	}
}

// The receive timestamp comes from the kernel (SO_TIMESTAMPNS) where the socket has it switched on.
func TestTheKernelStampsTheReceiveTime(t *testing.T) {
	// qemu-user (the arm64 unit tests) does not know the option: there the listener serves without it (timestampsUnavailable), and
	// the property belongs to the real kernel, which the amd64 runs and the level 1 tests have
	probe, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	perr := EnableKernelTimestamps(probe)
	_ = probe.Close()
	if timestampsUnavailable(perr) {
		t.Skip("the environment does not know SO_TIMESTAMPNS (ENOPROTOOPT), as qemu-user: " + perr.Error())
	}
	pc, err := ListenUDP("127.0.0.1")(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	c, err := net.DialUDP("udp4", nil, pc.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	before := time.Now()
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	_, from, rx, err := readPacket(pc, buf, make([]byte, oobSize))
	if err != nil {
		t.Fatal(err)
	}
	if !from.Addr().IsLoopback() {
		t.Errorf("from %v", from)
	}
	if runtime.GOOS == "linux" && (rx.IsZero() || rx.Before(before.Add(-time.Second)) || rx.After(time.Now().Add(time.Second))) {
		t.Errorf("the kernel's receive time is %v, the datagram was sent at %v", rx, before)
	}
}

func TestTheServerStampsTheReceiveTimeWithTheClockWhenTheSocketHasNone(t *testing.T) {
	ports := freePorts(t, 1)
	// a socket without SO_TIMESTAMPNS: what a testbed's listener makes
	plain := func(port int) (net.PacketConn, error) {
		return net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	}
	s, clk := newServer(t, func(o *Options) { o.BasePort = ports[0] })
	s.SetConfig(config(t, []int{ports[0]}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, plain); close(done) }()
	defer func() { cancel(); <-done }()
	<-s.Ready()
	p := exchange(t, "127.0.0.1", ports[0], 2*time.Second, request(4, 5))
	if p == nil {
		t.Fatal("no answer")
	}
	near(t, "receive", p.Receive.Time(clk.Now()), clk.Now(), time.Microsecond)
}

func TestNTSKeyEstablishmentIsReportedAndRefused(t *testing.T) {
	var mu sync.Mutex
	var got []model.NtpQueryLogEntry
	s, clk := newServer(t, func(o *Options) {
		o.Sink = sinkFunc(func(es []model.NtpQueryLogEntry) { mu.Lock(); got = append(got, es...); mu.Unlock() })
	})
	s.SetConfig(config(t, []int{Port}))
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ServeNTSKE(ctx, ln) }()
	logCtx, stopLog := context.WithCancel(context.Background())
	logDone := make(chan struct{})
	go func() { s.RunLog(logCtx); close(logDone) }()

	connect := func() {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Error("the server said something on the NTS-KE port")
		}
	}
	connect()
	connect() // the same device again: one entry
	waitFor(t, "the connections are counted", func() bool { return s.Counters().NTSKE == 2 })
	clk.Advance(time.Minute + time.Second)
	connect() // and after a minute, another
	waitFor(t, "the third connection is counted", func() bool { return s.Counters().NTSKE == 3 })
	cancel()
	if err := <-done; err != nil {
		t.Error(err)
	}
	stopLog()
	<-logDone
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("%d log entries, want 2: %+v", len(got), got)
	}
	e := got[0]
	if e.Kind == nil || *e.Kind != model.NtsKe || e.Port != NTSKEPort || e.Client != "127.0.0.1" || e.Answer != nil || e.ServedTime != nil {
		t.Errorf("%+v", e)
	}
}

type sinkFunc func([]model.NtpQueryLogEntry)

func (f sinkFunc) Post(_ context.Context, es []model.NtpQueryLogEntry) error { f(es); return nil }

func TestTheQueryLogGoesToTheSinkInBatches(t *testing.T) {
	var mu sync.Mutex
	var got []model.NtpQueryLogEntry
	var posts int
	s, clk := newServer(t, func(o *Options) {
		o.Sink = sinkFunc(func(es []model.NtpQueryLogEntry) { mu.Lock(); got = append(got, es...); posts++; mu.Unlock() })
	})
	s.SetConfig(config(t, []int{Port}, fault(ovA, Port, t0, []string{"10.20.0.11"}, `"answer":"silence"`)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunLog(ctx); close(done) }()
	clk.BlockUntil(1) // the log's ticker
	for range 3 {
		_, e := s.Process(request(4, 1), dev1, Port, time.Time{})
		s.log.add(*e)
	}
	_, e := s.Process(request(3, 1), dev2, Port, time.Time{})
	s.log.add(*e)
	clk.Advance(logInterval)
	waitFor(t, "the entries are posted", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 4 })
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 {
		t.Errorf("%d posts, want one batch", posts)
	}
	if *got[0].Answer != model.NtpQueryLogEntryAnswerSilence || got[0].Fault == nil || *got[3].Answer != model.NtpQueryLogEntryAnswerNormal || *got[3].Version != 3 {
		t.Errorf("%+v", got)
	}
}

func TestTheQueryLogIsBoundedWhileTheSinkIsAway(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	s, _ := newServer(t, func(o *Options) {
		o.Sink = failingSink{&fail}
	})
	for range logBuffer + 50 {
		s.log.add(model.NtpQueryLogEntry{Client: "10.0.0.1", Port: 123})
	}
	if got := s.Counters().LogDropped; got != 50 {
		t.Errorf("%d entries dropped, want 50", got)
	}
}

type failingSink struct{ fail *atomic.Bool }

func (f failingSink) Post(context.Context, []model.NtpQueryLogEntry) error {
	return fmt.Errorf("the API is away")
}

// The acceptance of M34: the server answers 5,000 queries per second and adds less than a millisecond to an answer. This is
// the floor on the loopback with real sockets and the clock of the host; the level 1 test measures it through the
// redirect of a test network.
func TestTheServerAnswers5000QueriesPerSecondOnTheLoopback(t *testing.T) {
	const (
		workers = 16
		total   = 20000
	)
	port := freePorts(t, 1)[0]
	s, _ := newServer(t, func(o *Options) { o.BasePort = port; o.Clock = nil })
	s.SetConfig(config(t, []int{port}, fault(ovA, port, time.Now(), []string{"127.0.0.0/8"}, `"offset":"+400d","skew_ppm":100`)))
	stop := startServer(t, s)
	defer stop()
	var answered atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for w := range workers {
		wg.Go(func() {
			c, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, byte(10+w))}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = c.Close() }()
			q := request(4, 0x1234_5678_9abc_def0)
			buf := make([]byte, 128)
			for range total / workers {
				_, _ = c.Write(q)
				_ = c.SetReadDeadline(time.Now().Add(time.Second))
				if n, err := c.Read(buf); err == nil && n == PacketSize {
					answered.Add(1)
				}
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	rate := float64(answered.Load()) / elapsed.Seconds()
	t.Logf("%d of %d answered in %v: %.0f queries/s", answered.Load(), total, elapsed, rate)
	// a datagram on the loopback may be lost when the machine is busy (the tests of other packages run beside this one); the
	// level 1 test counts every answer
	if answered.Load() < total*99/100 {
		t.Errorf("%d queries unanswered", total-answered.Load())
	}
	if rate < 5000 {
		t.Errorf("%.0f queries/s, the acceptance is 5000", rate)
	}
}

// The time the server adds to an answer: from the datagram in hand to the reply, less than a millisecond (§3.10).
func TestBuildingAnAnswerTakesLessThanAMillisecond(t *testing.T) {
	s, _ := newServer(t, func(o *Options) { o.Clock = nil })
	s.SetConfig(config(t, []int{Port}, fault(ovA, Port, time.Now(), []string{"10.20.0.0/24"}, `"offset":"+400d","skew_ppm":100`)))
	req := request(4, 1)
	var took []time.Duration
	for range 2000 {
		start := time.Now()
		reply, _ := s.Process(req, netip.MustParseAddr("10.20.0.11"), Port, time.Time{})
		if reply == nil {
			t.Fatal("no answer")
		}
		took = append(took, time.Since(start))
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	// the typical answer takes microseconds: it must be under a millisecond whatever else runs. The tail is looser, because a
	// garbage collection or another test's load on a shared machine may delay an answer; the level 1 test measures the tail.
	if median := took[len(took)/2]; median >= time.Millisecond {
		t.Errorf("the median answer took %v, want less than a millisecond", median)
	}
	if worst := took[len(took)*99/100]; worst >= 5*time.Millisecond {
		t.Errorf("99 percent of the answers took less than %v, want less than 5 ms", worst)
	}
}
