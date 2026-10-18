//go:build testbed

package capture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// The session tests capture on the loopback interface: it needs the capability CAP_NET_RAW (root, or a test
// container), nothing else, so they run wherever the unit tests run as root. A machine that refuses the raw socket
// fails them: they are the proof that the kernel accepts the filters.

func startOnLo(t *testing.T, mod func(*SessionConfig)) (*Session, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	cfg := SessionConfig{Interfaces: []IfaceConfig{{Name: "lo"}}, Format: PCAPNG, Dir: root, UID: -1, GID: -1, App: "test"}
	if mod != nil {
		mod(&cfg)
	}
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { s.Stop(ReasonUser) })
	return s, dir
}

// udpSink opens a UDP socket on the loopback that reads and discards what is sent to it.
func udpSink(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	go func() {
		b := make([]byte, 2048)
		for {
			if _, _, err := c.ReadFromUDP(b); err != nil {
				return
			}
		}
	}()
	return c
}

func sendUDP(t *testing.T, to *net.UDPConn, n, size int) {
	t.Helper()
	c, err := net.DialUDP("udp4", nil, to.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	payload := bytes.Repeat([]byte{'x'}, size)
	for i := 0; i < n; i++ {
		if _, err := c.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
}

func portOf(c *net.UDPConn) uint16 { return uint16(c.LocalAddr().(*net.UDPAddr).Port) }

// readCapture returns the packets of the capture's files.
func readCapture(t *testing.T, dir string, s *Session) []Packet {
	t.Helper()
	var out []Packet
	for _, name := range s.Files() {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		_, pk := readAll(t, b)
		out = append(out, pk...)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestACaptureOnTheLoopbackHoldsExactlyTheSelectedTraffic(t *testing.T) {
	wanted, other := udpSink(t), udpSink(t)
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.Interfaces[0].Filter = Filter{Protocol: UDP, Ports: []PortRange{{portOf(wanted), portOf(wanted)}}}
	})
	if ifs := s.Interfaces(); len(ifs) != 1 || ifs[0].Name != "lo" || ifs[0].Link != LinkEthernet {
		t.Fatalf("interfaces %+v", ifs)
	}
	sendUDP(t, wanted, 20, 100)
	sendUDP(t, other, 30, 100)
	sendUDP(t, wanted, 5, 200)
	waitFor(t, "25 packets of the wanted port", func() bool { return s.Stats().Packets >= 25 })
	time.Sleep(300 * time.Millisecond) // the traffic of the other port had the time to arrive too
	s.Stop(ReasonUser)
	if r := s.Result(); r.Reason != ReasonUser || r.Err != nil {
		t.Fatalf("result %+v", r)
	}
	pkts := readCapture(t, dir, s)
	if len(pkts) == 0 || len(pkts)%25 != 0 {
		t.Fatalf("%d packets for 25 datagrams", len(pkts))
	}
	copies := len(pkts) / 25 // the loopback shows a datagram as it leaves and as it arrives
	if copies > 2 {
		t.Fatalf("%d copies of each datagram", copies)
	}
	for i, p := range pkts {
		pp := parse(LinkEthernet, p.Data)
		if !pp.ip || pp.proto != protoUDP || pp.dp != portOf(wanted) {
			t.Errorf("packet %d is not a datagram to port %d: %+v", i, portOf(wanted), pp)
		}
		if p.OrigLen != len(p.Data) {
			t.Errorf("packet %d: length %d, original length %d", i, len(p.Data), p.OrigLen)
		}
		if p.Time.IsZero() || time.Since(p.Time) > time.Minute || p.Time.After(time.Now().Add(time.Second)) {
			t.Errorf("packet %d has the time %v", i, p.Time)
		}
	}
	st := s.Stats()
	if st.Packets != int64(len(pkts)) || st.Size <= 0 {
		t.Errorf("stats %+v for %d packets", st, len(pkts))
	}
	files := s.Files()
	if len(files) != 1 || files[0] != "capture.pcapng" {
		t.Errorf("files %v", files)
	}
}

func TestTheSnapLengthCutsPacketsAndKeepsTheOriginalLength(t *testing.T) {
	sink := udpSink(t)
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.Snaplen = 96
		c.Interfaces[0].Filter = Filter{Protocol: UDP, Ports: []PortRange{{portOf(sink), portOf(sink)}}}
	})
	sendUDP(t, sink, 3, 1000)
	waitFor(t, "the datagrams", func() bool { return s.Stats().Packets >= 3 })
	s.Stop(ReasonUser)
	pkts := readCapture(t, dir, s)
	if len(pkts) < 3 {
		t.Fatalf("%d packets", len(pkts))
	}
	for i, p := range pkts {
		if len(p.Data) != 96 || p.OrigLen != 14+20+8+1000 {
			t.Errorf("packet %d: %d bytes of %d", i, len(p.Data), p.OrigLen)
		}
	}
}

func TestACaptureOfThePcapFormatIsReadableAndHasTheLinkType(t *testing.T) {
	sink := udpSink(t)
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.Format = PCAP
		c.Interfaces[0].Filter = Filter{Protocol: UDP, Ports: []PortRange{{portOf(sink), portOf(sink)}}}
	})
	sendUDP(t, sink, 4, 50)
	waitFor(t, "the datagrams", func() bool { return s.Stats().Packets >= 4 })
	s.Stop(ReasonUser)
	b, err := os.ReadFile(filepath.Join(dir, "capture.pcap"))
	if err != nil {
		t.Fatal(err)
	}
	r, got := readAll(t, b)
	if r.Format() != PCAP || r.Interfaces()[0].Link != LinkEthernet || len(got) < 4 {
		t.Fatalf("%s %+v %d packets", r.Format(), r.Interfaces(), len(got))
	}
}

func TestTheSizeLimitEndsTheCaptureAndTheFileStaysWithinIt(t *testing.T) {
	sink := udpSink(t)
	const limit = 20_000
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.MaxSize = limit
		c.Interfaces[0].Filter = Filter{Protocol: UDP, Ports: []PortRange{{portOf(sink), portOf(sink)}}}
	})
	sendUDP(t, sink, 200, 400)
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the capture did not stop at its size limit")
	}
	if r := s.Result(); r.Reason != ReasonSize {
		t.Fatalf("result %+v", r)
	}
	st, err := os.Stat(filepath.Join(dir, "capture.pcapng"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > limit || st.Size() < limit-1000 {
		t.Errorf("the file has %d bytes for a limit of %d", st.Size(), limit)
	}
}

func TestTheDurationEndsTheCapture(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	s, _ := startOnLo(t, func(c *SessionConfig) {
		c.Clock = fake
		c.Duration = time.Minute
	})
	fake.BlockUntil(2) // the ticker and the timer of the duration
	fake.Advance(59 * time.Second)
	select {
	case <-s.Done():
		t.Fatal("the capture ended before its duration")
	case <-time.After(100 * time.Millisecond):
	}
	fake.Advance(time.Second)
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the capture did not end at its duration")
	}
	if r := s.Result(); r.Reason != ReasonDuration {
		t.Fatalf("result %+v", r)
	}
}

func TestACaptureStopsWhenTheDiskIsAlmostFullAndRefusesToStartOnOne(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	var free atomic.Uint64
	free.Store(50)
	diskFree := func() (uint64, uint64, error) { return free.Load(), 100, nil }
	s, _ := startOnLo(t, func(c *SessionConfig) {
		c.Clock, c.DiskFree, c.MinFreePercent = fake, diskFree, 10
	})
	fake.BlockUntil(1)
	fake.Advance(time.Second)
	select {
	case <-s.Done():
		t.Fatal("the capture ended with 50 % free")
	case <-time.After(100 * time.Millisecond):
	}
	free.Store(9)
	fake.Advance(checkEvery)
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the capture did not stop at 9 % free")
	}
	if r := s.Result(); r.Reason != ReasonDiskLow {
		t.Fatalf("result %+v", r)
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	_, err = Start(context.Background(), SessionConfig{Interfaces: []IfaceConfig{{Name: "lo"}}, Format: PCAPNG, Dir: root, UID: -1, GID: -1, DiskFree: diskFree, MinFreePercent: 10})
	if !errors.Is(err, ErrDiskLow) {
		t.Fatalf("a capture on a disk with 9 %% free: %v", err)
	}
}

func TestARingBufferCaptureKeepsTheNewestPackets(t *testing.T) {
	sink := udpSink(t)
	s, dir := startOnLo(t, func(c *SessionConfig) {
		c.Ring = &RingConfig{Files: 3, FileSize: 8000}
		c.Interfaces[0].Filter = Filter{Protocol: UDP, Ports: []PortRange{{portOf(sink), portOf(sink)}}}
	})
	sendUDP(t, sink, 300, 300)
	waitFor(t, "the datagrams", func() bool { return s.Stats().Packets >= 300 })
	s.Stop(ReasonUser)
	files := s.Files()
	if len(files) != 3 {
		t.Fatalf("files %v", files)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatalf("%d files on disk", len(entries))
	}
	var size int64
	for _, e := range entries {
		info, _ := e.Info()
		size += info.Size()
		if info.Size() > 8000 {
			t.Errorf("%s has %d bytes for a file size of 8000", e.Name(), info.Size())
		}
	}
	if st := s.Stats(); st.Size != size || st.Packets < 300 {
		t.Errorf("stats %+v, files have %d bytes", st, size)
	}
}

func TestAMissingInterfaceAndABadFilterAreRefusedAtTheStart(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	base := SessionConfig{Format: PCAPNG, Dir: root, UID: -1, GID: -1}
	for name, mod := range map[string]func(*SessionConfig){
		"no such interface": func(c *SessionConfig) { c.Interfaces = []IfaceConfig{{Name: "nosuch0"}} },
		"a MAC on lo is fine but a bad one is not": func(c *SessionConfig) {
			c.Interfaces = []IfaceConfig{{Name: "lo", Filter: Filter{MACs: []string{"zz"}}}}
		},
		"no interface":    func(c *SessionConfig) {},
		"a short snaplen": func(c *SessionConfig) { c.Interfaces = []IfaceConfig{{Name: "lo"}}; c.Snaplen = 10 },
	} {
		cfg := base
		mod(&cfg)
		if s, err := Start(context.Background(), cfg); err == nil {
			s.Stop(ReasonUser)
			t.Errorf("%s: started", name)
		}
	}
	entries, _ := os.ReadDir(root.Name())
	if len(entries) != 0 {
		t.Errorf("a refused capture left %d files", len(entries))
	}
}

func TestACanceledContextEndsTheCaptureWithAnError(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Start(ctx, SessionConfig{Interfaces: []IfaceConfig{{Name: "lo"}}, Format: PCAPNG, Dir: root, UID: -1, GID: -1})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the capture did not end")
	}
	if r := s.Result(); r.Reason != ReasonError || !errors.Is(r.Err, context.Canceled) {
		t.Fatalf("result %+v", r)
	}
}

func TestAFullDiskIsTheDiskLowReason(t *testing.T) {
	s := &Session{done: make(chan struct{}), cancel: func() {}}
	s.fail(&os.PathError{Op: "write", Err: syscall.ENOSPC})
	if r := s.Result(); r.Reason != ReasonDiskLow {
		t.Fatalf("%+v", r)
	}
	s = &Session{done: make(chan struct{}), cancel: func() {}}
	s.fail(io.ErrClosedPipe)
	if r := s.Result(); r.Reason != ReasonError || r.Err == nil {
		t.Fatalf("%+v", r)
	}
}

func TestStopIsIdempotentAndTheFirstReasonStays(t *testing.T) {
	s, _ := startOnLo(t, nil)
	s.Stop(ReasonRunEnd)
	s.Stop(ReasonUser)
	if r := s.Result(); r.Reason != ReasonRunEnd {
		t.Fatalf("%+v", r)
	}
}
