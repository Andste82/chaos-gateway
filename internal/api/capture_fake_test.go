package api_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// fakeCapturer is the executor's side of the captures for the API tests: it writes real capture files with the real
// writer and applies the real filters of the request to the frames a test puts on an interface, so a capture holds
// exactly what its filter selects; only the AF_PACKET socket is replaced (internal/capture and internal/executor
// test that on the loopback interface).
type fakeCapturer struct {
	dir string

	mu     sync.Mutex
	opened []executor.CaptureOpen
	live   map[string]*fakeStream
	fail   error // returned by the next OpenCapture
	freePc atomic.Int64
}

func newFakeCapturer(dir string) *fakeCapturer {
	fc := &fakeCapturer{dir: dir, live: map[string]*fakeStream{}}
	fc.freePc.Store(60)
	return fc
}

// diskFree is the free space of the volume as the tests set it (percent of 1000 blocks).
func (f *fakeCapturer) diskFree() (uint64, uint64, error) {
	return uint64(f.freePc.Load()) * 10, 1000, nil
}

// Opened returns the requests the executor has seen.
func (f *fakeCapturer) Opened() []executor.CaptureOpen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]executor.CaptureOpen(nil), f.opened...)
}

// Last returns the last request.
func (f *fakeCapturer) Last() executor.CaptureOpen {
	o := f.Opened()
	return o[len(o)-1]
}

func linkOf(name string) capture.LinkType {
	if strings.HasPrefix(name, "wg") {
		return capture.LinkRaw
	}
	return capture.LinkEthernet
}

func (f *fakeCapturer) OpenCapture(ctx context.Context, o executor.CaptureOpen) (executor.CaptureStream, error) {
	f.mu.Lock()
	f.opened = append(f.opened, o)
	if err := f.fail; err != nil {
		f.fail = nil
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Unlock()
	if err := o.Validate(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Join(f.dir, o.Dir))
	if err != nil {
		return nil, err
	}
	s := &fakeStream{f: f, key: o.Dir, events: make(chan executor.CaptureEvent, 64), ctrl: make(chan string, 4), ended: make(chan struct{})}
	cfg := capture.OutputConfig{Root: root, Format: o.Format, Base: "capture", MaxSize: o.MaxSize, UID: -1, GID: -1, App: "fake"}
	if o.RingFiles > 0 {
		cfg.Files, cfg.FileSize = o.RingFiles, o.RingFileSize
	}
	for _, i := range o.Interfaces {
		m, err := i.Filter.Matcher(linkOf(i.Name))
		if err != nil {
			_ = root.Close()
			return nil, err
		}
		s.names = append(s.names, i.Name)
		s.matchers = append(s.matchers, m)
		cfg.Ifaces = append(cfg.Ifaces, capture.InterfaceInfo{Name: i.Name, Link: linkOf(i.Name), Snaplen: 262144})
	}
	if s.out, err = capture.NewOutput(cfg); err != nil {
		_ = root.Close()
		return nil, err
	}
	s.root = root
	s.infos = cfg.Ifaces
	s.events <- executor.CaptureEvent{Kind: executor.CaptureStarted, Interfaces: s.infos, Files: s.out.Files()}
	f.mu.Lock()
	f.live[o.Dir] = s
	f.mu.Unlock()
	go s.run()
	return s, nil
}

// Fail makes the next request fail with the error.
func (f *fakeCapturer) Fail(err error) { f.mu.Lock(); f.fail = err; f.mu.Unlock() }

// streams returns the running captures.
func (f *fakeCapturer) streams() []*fakeStream {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*fakeStream
	for _, s := range f.live {
		out = append(out, s)
	}
	return out
}

// Frame puts a frame on an interface: every running capture that reads the interface takes it when its filter
// selects it. It returns how many captures took it.
func (f *fakeCapturer) Frame(iface string, frame []byte, outgoing bool) int {
	n := 0
	for _, s := range f.streams() {
		if s.take(iface, frame, outgoing) {
			n++
		}
	}
	return n
}

// Tick sends the counters of every running capture, as the executor does once a second.
func (f *fakeCapturer) Tick() {
	for _, s := range f.streams() {
		s.stats()
	}
}

// End ends the running captures for a reason of the executor (disk_low, duration, error).
func (f *fakeCapturer) End(reason, errText string) {
	for _, s := range f.streams() {
		s.end(reason, errText)
	}
}

// Running is the number of captures that run.
func (f *fakeCapturer) Running() int { return len(f.streams()) }

type fakeStream struct {
	f        *fakeCapturer
	key      string
	events   chan executor.CaptureEvent
	ctrl     chan string
	ended    chan struct{}
	root     *os.Root
	out      *capture.Output
	names    []string
	matchers []*capture.Matcher
	infos    []capture.InterfaceInfo

	mu   sync.Mutex
	done bool
}

func (s *fakeStream) Events() <-chan executor.CaptureEvent { return s.events }

func (s *fakeStream) Stop(reason string) {
	select {
	case s.ctrl <- reason:
	default:
	}
}

func (s *fakeStream) Close() { s.end("error", "closed"); <-s.ended }

func (s *fakeStream) run() {
	select {
	case reason := <-s.ctrl:
		s.end(reason, "")
	case <-s.ended:
	}
}

func (s *fakeStream) take(iface string, frame []byte, outgoing bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return false
	}
	for i, n := range s.names {
		if n != iface || !s.matchers[i].Match(frame) {
			continue
		}
		err := s.out.Write(capture.Packet{Iface: i, Time: time.Now(), Data: frame, OrigLen: len(frame), Outgoing: outgoing})
		if errors.Is(err, capture.ErrSizeLimit) {
			s.endLocked("size", "")
			return false
		}
		return err == nil
	}
	return false
}

func (s *fakeStream) stats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	_ = s.out.Flush()
	select {
	case s.events <- executor.CaptureEvent{Kind: executor.CaptureStats, Packets: s.out.Packets(), Size: s.out.Size(), Files: s.out.Files()}:
	default:
	}
}

func (s *fakeStream) end(reason, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endLocked(reason, errText)
}

func (s *fakeStream) endLocked(reason, errText string) {
	if s.done {
		return
	}
	s.done = true
	_ = s.out.Close()
	_ = s.root.Close()
	s.events <- executor.CaptureEvent{Kind: executor.CaptureStopped, Packets: s.out.Packets(), Size: s.out.Size(), Files: s.out.Files(), Reason: reason, Error: errText}
	close(s.events)
	s.f.mu.Lock()
	delete(s.f.live, s.key)
	s.f.mu.Unlock()
	close(s.ended)
}
