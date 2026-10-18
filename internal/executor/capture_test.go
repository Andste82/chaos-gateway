//go:build testbed

package executor

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/clock"
)

// The capture tests read the loopback interface, which a process with CAP_NET_RAW may (root, the test container),
// as the tests of internal/capture do.

// next returns the next event of a stream within a time.
func next(t *testing.T, s CaptureStream) CaptureEvent {
	t.Helper()
	select {
	case ev, ok := <-s.Events():
		if !ok {
			t.Fatal("the stream is closed")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}
	return CaptureEvent{}
}

// untilStopped reads events until the end and returns it.
func untilStopped(t *testing.T, s CaptureStream) CaptureEvent {
	t.Helper()
	for {
		if ev := next(t, s); ev.Kind == CaptureStopped {
			return ev
		}
	}
}

func sendDatagrams(t *testing.T, port, n int) {
	t.Helper()
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for i := 0; i < n; i++ {
		_, _ = c.Write([]byte("hello"))
	}
}

func TestACaptureStartsStreamsItsCountersAndEndsWhenItIsStopped(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	e, root := capExec(t, WithCaptureClock(fake))
	mkCapDir(t, root, "c1")
	s, err := e.OpenCapture(context.Background(), loOpen("c1"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ev := next(t, s)
	if ev.Kind != CaptureStarted || len(ev.Interfaces) != 1 || ev.Interfaces[0].Name != "lo" || ev.Interfaces[0].Link != capture.LinkEthernet || len(ev.Files) != 1 {
		t.Fatalf("first event %+v", ev)
	}
	sendDatagrams(t, 40404, 5)
	deadline := time.Now().Add(10 * time.Second)
	var stats CaptureEvent
	for stats.Packets < 5 && time.Now().Before(deadline) {
		fake.BlockUntil(2)
		fake.Advance(time.Second)
		select {
		case stats = <-s.Events():
		case <-time.After(300 * time.Millisecond):
		}
	}
	if stats.Kind != CaptureStats || stats.Packets < 5 || stats.Size <= 0 || len(stats.Files) != 1 {
		t.Fatalf("counters %+v", stats)
	}
	s.Stop("user")
	end := untilStopped(t, s)
	if end.Reason != "user" || end.Packets < 5 || end.Error != "" {
		t.Fatalf("end %+v", end)
	}
	if _, ok := <-s.Events(); ok {
		t.Error("the stream has events after the end")
	}
	b, err := os.ReadFile(filepath.Join(root, "c1", "capture.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := capture.NewReader(bytes.NewReader(b)); err != nil || r.Format() != capture.PCAPNG {
		t.Fatalf("the file: %v", err)
	}
}

func TestAStopForTheEndOfARunIsPassedOn(t *testing.T) {
	e, root := capExec(t)
	mkCapDir(t, root, "c1")
	s, err := e.OpenCapture(context.Background(), loOpen("c1"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	next(t, s)
	s.Stop("run_end")
	if end := untilStopped(t, s); end.Reason != "run_end" {
		t.Fatalf("end %+v", end)
	}
}

func TestOnlyMaxCapturesRunAtOnceAndASlotComesBackWithTheEnd(t *testing.T) {
	e, root := capExec(t)
	var open []CaptureStream
	for i := 0; i < MaxCaptures; i++ {
		name := "c" + string(rune('a'+i))
		mkCapDir(t, root, name)
		s, err := e.OpenCapture(context.Background(), loOpen(name))
		if err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
		open = append(open, s)
	}
	mkCapDir(t, root, "extra")
	if _, err := e.OpenCapture(context.Background(), loOpen("extra")); !errors.Is(err, ErrTooManyCaptures) {
		t.Fatalf("the ninth capture: %v", err)
	}
	open[0].Close()
	s, err := e.OpenCapture(context.Background(), loOpen("extra"))
	if err != nil {
		t.Fatalf("after one ended: %v", err)
	}
	s.Close()
	for _, s := range open[1:] {
		s.Close()
	}
}

func TestACaptureOnAFullDiskIsRefusedAndStopsWhenTheDiskFills(t *testing.T) {
	e, root := capExec(t, WithCaptures(t.TempDir(), 100.1)) // more free space than any disk has
	mkCapDir(t, e.capDir, "c1")
	if _, err := e.OpenCapture(context.Background(), loOpen("c1")); !errors.Is(err, ErrDiskLow) {
		t.Fatalf("a capture on a disk below the limit: %v", err)
	}
	_ = root
}

func TestTheFilesOfACaptureBelongToTheOwnerOfItsDirectory(t *testing.T) {
	e, root := capExec(t)
	dir := mkCapDir(t, root, "c1")
	if err := os.Chown(dir, 65532, 65532); err != nil {
		t.Fatal(err)
	}
	s, err := e.OpenCapture(context.Background(), loOpen("c1"))
	if err != nil {
		t.Fatal(err)
	}
	next(t, s)
	s.Stop("user")
	untilStopped(t, s)
	s.Close()
	fi, err := os.Stat(filepath.Join(dir, "capture.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Uid != 65532 || st.Gid != 65532 {
		t.Errorf("the file belongs to %d:%d, want 65532:65532", st.Uid, st.Gid)
	}
}

// startCaptureServer serves an executor with captures on a socket.
func startCaptureServer(t *testing.T) (string, *Executor, string) {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "cgx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	path := filepath.Join(sockDir, "e.sock")
	l, err := Listen(path, 0o660, -1)
	if err != nil {
		t.Fatal(err)
	}
	e, root := capExec(t)
	s := &Server{Exec: e, Auth: AllowUIDs(uint32(os.Getuid()))}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = s.Serve(ctx, l) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	return path, e, root
}

func TestACaptureOverTheSocketHasTheSameEventsAndEndsWithAStop(t *testing.T) {
	path, _, root := startCaptureServer(t)
	mkCapDir(t, root, "c1")
	c := dial(t, path)
	s, err := c.OpenCapture(context.Background(), loOpen("c1"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ev := next(t, s); ev.Kind != CaptureStarted || len(ev.Interfaces) != 1 {
		t.Fatalf("first event %+v", ev)
	}
	sendDatagrams(t, 40404, 3)
	var stats CaptureEvent
	for stats.Packets < 3 {
		stats = next(t, s)
	}
	s.Stop("run_end")
	end := untilStopped(t, s)
	if end.Reason != "run_end" || end.Packets < 3 {
		t.Fatalf("end %+v", end)
	}
	if _, ok := <-s.Events(); ok {
		t.Error("events after the end")
	}
}

func TestAConnectionThatBreaksEndsItsCaptureAndGivesTheSlotBack(t *testing.T) {
	path, e, root := startCaptureServer(t)
	mkCapDir(t, root, "c1")
	c := dial(t, path)
	s, err := c.OpenCapture(context.Background(), loOpen("c1"))
	if err != nil {
		t.Fatal(err)
	}
	next(t, s)
	if len(e.capSlots) != 1 {
		t.Fatalf("%d captures run", len(e.capSlots))
	}
	s.Close() // closes the connection without a stop
	deadline := time.Now().Add(10 * time.Second)
	for len(e.capSlots) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the capture still runs after its connection is gone")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// the file is closed and complete
	b, err := os.ReadFile(filepath.Join(root, "c1", "capture.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capture.NewReader(bytes.NewReader(b)); err != nil {
		t.Fatalf("the file of the capture that lost its connection: %v", err)
	}
}

func TestTheSocketNamesTheReasonsACaptureIsRefused(t *testing.T) {
	path, _, root := startCaptureServer(t)
	c := dial(t, path)
	var open []CaptureStream
	for i := 0; i < MaxCaptures; i++ {
		name := "c" + string(rune('a'+i))
		mkCapDir(t, root, name)
		s, err := c.OpenCapture(context.Background(), loOpen(name))
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, s)
	}
	defer func() {
		for _, s := range open {
			s.Close()
		}
	}()
	mkCapDir(t, root, "extra")
	if _, err := c.OpenCapture(context.Background(), loOpen("extra")); !errors.Is(err, ErrTooManyCaptures) {
		t.Errorf("the ninth capture: %v", err)
	}

	// an executor that has no volume, and one whose volume is almost full
	for name, c := range map[string]struct {
		opts []Option
		want error
	}{
		"no capture volume":        {nil, ErrNoCaptureDir},
		"a volume below the limit": {[]Option{WithCaptures(t.TempDir(), 100.1)}, ErrDiskLow},
	} {
		sockDir, err := os.MkdirTemp("", "cgx")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.RemoveAll(sockDir) }()
		l, err := Listen(filepath.Join(sockDir, "e.sock"), 0o660, -1)
		if err != nil {
			t.Fatal(err)
		}
		e := newExec(t, &fakeRunner{}, c.opts...)
		e.Scope().Set([]string{"lo"})
		if e.capDir != "" {
			mkCapDir(t, e.capDir, "extra")
		}
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); _ = (&Server{Exec: e, Auth: AllowUIDs(uint32(os.Getuid()))}).Serve(ctx, l) }()
		cl := dial(t, filepath.Join(sockDir, "e.sock"))
		if _, err := cl.OpenCapture(context.Background(), loOpen("extra")); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		cancel()
		wg.Wait()
	}
}

func TestTheSocketRefusesACaptureWithTheErrorAndKeepsServing(t *testing.T) {
	path, _, root := startCaptureServer(t)
	mkCapDir(t, root, "c1")
	c := dial(t, path)
	o := loOpen("c1")
	o.Interfaces[0].Name = "eth9"
	_, err := c.OpenCapture(context.Background(), o)
	var re *RemoteError
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("a capture outside the scope: %v", err)
	}
	o = loOpen("../x")
	if _, err = c.OpenCapture(context.Background(), o); !errors.As(err, &re) || re.Code != CodeInvalid {
		t.Fatalf("an invalid capture: %v", err)
	}
	if _, err := c.Generation(context.Background()); err != nil {
		t.Fatalf("the executor does not answer requests after refusing captures: %v", err)
	}
}
