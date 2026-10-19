package capturemgr_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/goleak"

	"github.com/Andste82/chaos-gateway/internal/capture"
	"github.com/Andste82/chaos-gateway/internal/capturemgr"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

const (
	netIoT = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	netHub = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
)

// gateway is the engine as the manager sees it: a snapshot, and the events.
type gateway struct {
	mu     sync.Mutex
	snap   *engine.Snapshot
	events []string
}

func (g *gateway) Snapshot() *engine.Snapshot { return g.snap }
func (g *gateway) Emit(typ string, _ map[string]any) {
	g.mu.Lock()
	g.events = append(g.events, typ)
	g.mu.Unlock()
}
func (g *gateway) emitted() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.events...)
}

func network(t *testing.T, doc string) model.Network {
	t.Helper()
	var n model.Network
	if err := n.UnmarshalJSON([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	return n
}

func snapshot(t *testing.T) *engine.Snapshot {
	t.Helper()
	nets := map[string]model.Network{
		netIoT: network(t, `{"type":"lan","name":"IoT","address":"10.10.0.1/24"}`),
		netHub: network(t, `{"type":"wireguard","name":"lab-hub","kind":"hub","address":"10.99.0.1/24","listen_port":51820}`),
	}
	groups := map[string]model.Group{"g1": {Name: "sensors", Members: &[]model.Ref{"d-mac", "d-ip", "d-wg"}}}
	cfg := &model.Configuration{Networks: &nets, Groups: &groups}
	return &engine.Snapshot{
		Config: cfg, Revision: 1,
		Bridges:             []compiler.Bridge{{Name: "br-iot", NetworkID: netIoT, NetworkName: "IoT", Address: netip.MustParsePrefix("10.10.0.1/24")}},
		WireGuardInterfaces: []compiler.WGInterface{{Name: "wg-lab-hub", NetworkID: netHub, Kind: "hub", Role: "test", Address: netip.MustParsePrefix("10.99.0.1/24")}},
		Applied:             &engine.AppliedInfo{Uplink: compiler.Uplink{Name: "wan0"}},
		Devices: []engine.DeviceState{
			{ID: "d-mac", Name: "with-mac", Network: netIoT, MACs: []string{"02:00:00:00:00:AA"}, Addresses: []netip.Addr{netip.MustParseAddr("10.10.0.5")}},
			{ID: "d-ip", Name: "behind-a-router", Network: netIoT, Addresses: []netip.Addr{netip.MustParseAddr("10.10.0.77")}},
			{ID: "d-wg", Name: "client", Network: netHub, Addresses: []netip.Addr{netip.MustParseAddr("10.99.0.2")}},
			{ID: "d-none", Name: "unseen", Network: netIoT},
		},
	}
}

func TestAScopeIsResolvedToTheInterfacesAndFiltersOfTheSnapshot(t *testing.T) {
	snap := snapshot(t)
	ref := func(s string) *model.Ref { return &s }

	p, err := capturemgr.Resolve(snap, model.CaptureScope{Device: ref("with-mac")})
	if err != nil || len(p.Interfaces) != 1 || p.Interfaces[0].Name != "br-iot" || strings.Join(p.Interfaces[0].Filter.MACs, ",") != "02:00:00:00:00:aa" || len(p.Interfaces[0].Filter.Hosts) != 0 {
		t.Errorf("a device with an Ethernet address is selected by it (lower case): %+v %v", p, err)
	}
	p, err = capturemgr.Resolve(snap, model.CaptureScope{Device: ref("d-ip")})
	if err != nil || len(p.Interfaces[0].Filter.MACs) != 0 || len(p.Interfaces[0].Filter.Hosts) != 1 || p.Interfaces[0].Filter.Hosts[0].String() != "10.10.0.77/32" {
		t.Errorf("a device without one is selected by its address: %+v %v", p, err)
	}
	p, err = capturemgr.Resolve(snap, model.CaptureScope{Device: ref("client")})
	if err != nil || p.Interfaces[0].Name != "wg-lab-hub" || p.Links[0] != capture.LinkRaw || len(p.Interfaces[0].Filter.Hosts) != 1 {
		t.Errorf("a client of a WireGuard network: %+v %v", p, err)
	}
	// a group spans the networks of its members: one interface each, the members of that network in its filter
	p, err = capturemgr.Resolve(snap, model.CaptureScope{Selector: groupSelector(t, "sensors")})
	if err != nil || len(p.Interfaces) != 2 || !p.Mixed() {
		t.Fatalf("group: %+v %v", p, err)
	}
	var iot capture.Filter
	for _, i := range p.Interfaces {
		if i.Name == "br-iot" {
			iot = i.Filter
		}
	}
	if len(iot.MACs) != 1 || len(iot.Hosts) != 1 {
		t.Errorf("the filter of the group on the bridge: %+v", iot)
	}
	// the uplink and a network
	if p, err = capturemgr.Resolve(snap, model.CaptureScope{Uplink: ptr(model.CaptureScopeUplink(true))}); err != nil || p.Interfaces[0].Name != "wan0" || p.Links[0] != capture.LinkEthernet {
		t.Errorf("uplink: %+v %v", p, err)
	}
	if p, err = capturemgr.Resolve(snap, model.CaptureScope{Network: ref("lab-hub")}); err != nil || p.Interfaces[0].Name != "wg-lab-hub" {
		t.Errorf("network: %+v %v", p, err)
	}
}

func groupSelector(t *testing.T, group string) *struct {
	Destination *model.Destination `json:"destination,omitempty"`
	PortRanges  *[]model.PortRange `json:"port_ranges,omitempty"`
	Ports       *[]int             `json:"ports,omitempty"`
	Protocol    *model.Protocol    `json:"protocol,omitempty"`
	Source      model.Scope        `json:"source"`
} {
	t.Helper()
	var cs model.CaptureScope
	if err := jsonUnmarshal([]byte(`{"selector":{"source":{"group":"`+group+`"}}}`), &cs); err != nil {
		t.Fatal(err)
	}
	return cs.Selector
}

func TestAScopeThatSelectsNothingIsRefusedWithItsPointer(t *testing.T) {
	snap := snapshot(t)
	ref := func(s string) *model.Ref { return &s }
	for name, c := range map[string]struct {
		scope model.CaptureScope
		path  string
	}{
		"an unknown device":                      {model.CaptureScope{Device: ref("ghost")}, "/scope/device"},
		"a device without an address":            {model.CaptureScope{Device: ref("unseen")}, "/scope/device"},
		"an unknown network":                     {model.CaptureScope{Network: ref("ghost")}, "/scope/network"},
		"a network whose interface is not there": {model.CaptureScope{Network: ref("IoT")}, "/scope/network"},
		"an empty scope":                         {model.CaptureScope{}, "/scope"},
	} {
		s := snap
		if name == "a network whose interface is not there" {
			c := *snap
			c.Bridges = nil
			s = &c
		}
		_, err := capturemgr.Resolve(s, c.scope)
		var ve domain.ValidationErrors
		if !errors.As(err, &ve) || len(ve) == 0 || !strings.HasPrefix(ve[0].Path, c.path) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// no uplink applied
	c := *snap
	c.Applied = nil
	if _, err := capturemgr.Resolve(&c, model.CaptureScope{Uplink: ptr(model.CaptureScopeUplink(true))}); err == nil {
		t.Error("an uplink capture without an applied uplink")
	}
}

// ---- the manager

// stream is a capture of the executor that ends when it is stopped, as the real one does; a test ends it by
// itself with end, or by closing the channel.
type stream struct {
	events chan executor.CaptureEvent
	mu     sync.Mutex
	stops  []string
	closed bool
	ended  bool
}

func (s *stream) Events() <-chan executor.CaptureEvent { return s.events }
func (s *stream) Stop(r string) {
	s.mu.Lock()
	s.stops = append(s.stops, r)
	s.mu.Unlock()
	s.end(executor.CaptureEvent{Kind: executor.CaptureStopped, Reason: r, Packets: 4, Size: 400})
}
func (s *stream) Close() { s.mu.Lock(); s.closed = true; s.mu.Unlock() }

// end sends the end of the capture, once.
func (s *stream) end(ev executor.CaptureEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	s.events <- ev
	close(s.events)
}

type opener struct {
	mu      sync.Mutex
	streams []*stream
}

func (o *opener) OpenCapture(_ context.Context, _ executor.CaptureOpen) (executor.CaptureStream, error) {
	s := &stream{events: make(chan executor.CaptureEvent, 16)}
	s.events <- executor.CaptureEvent{Kind: executor.CaptureStarted, Interfaces: []capture.InterfaceInfo{{Name: "wan0", Link: capture.LinkEthernet}}, Files: []string{"capture.pcapng"}}
	o.mu.Lock()
	o.streams = append(o.streams, s)
	o.mu.Unlock()
	return s, nil
}

func (o *opener) last() *stream {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.streams[len(o.streams)-1]
}

func newManager(t *testing.T, dir string) (*capturemgr.Manager, *opener, *gateway) {
	t.Helper()
	gw := &gateway{snap: snapshot(t)}
	op := &opener{}
	m, err := capturemgr.New(capturemgr.Config{Clock: clock.NewFake(t0), Dir: dir, Opener: op, Gateway: gw})
	if err != nil {
		t.Fatal(err)
	}
	return m, op, gw
}

func uplinkRequest() capturemgr.StartRequest {
	return capturemgr.StartRequest{CaptureRequest: model.CaptureRequest{Scope: model.CaptureScope{Uplink: ptr(model.CaptureScopeUplink(true))}}}
}

func ptr[T any](v T) *T { return &v }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// An executor whose stream ends without saying why (it died) leaves a failed capture.
func TestAStreamThatEndsWithoutAnEndFailsTheCapture(t *testing.T) {
	m, op, gw := newManager(t, t.TempDir())
	c, err := m.Start(context.Background(), uplinkRequest())
	if err != nil {
		t.Fatal(err)
	}
	s := op.last()
	s.mu.Lock()
	s.ended = true
	close(s.events)
	s.mu.Unlock()
	eventually(t, "the capture fails", func() bool { got, _ := m.Get(c.Id); return got.Status == model.CaptureStatusFailed })
	got, _ := m.Get(c.Id)
	if *got.StopReason != model.CaptureStopReasonError || got.Message == nil || got.StoppedAt == nil {
		t.Errorf("%+v", got)
	}
	m.Close(context.Background())
	if ev := gw.emitted(); len(ev) != 2 || ev[0] != "capture_started" || ev[1] != "capture_stopped" {
		t.Errorf("events %v", ev)
	}
	eventually(t, "the stream is closed", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.closed })
}

// A service that stops ends its captures, and says so.
func TestClosingTheManagerStopsTheCapturesAsFailed(t *testing.T) {
	m, op, _ := newManager(t, t.TempDir())
	c, err := m.Start(context.Background(), uplinkRequest())
	if err != nil {
		t.Fatal(err)
	}
	m.Close(context.Background())
	if s := op.last(); len(s.stops) != 1 || s.stops[0] != "user" {
		t.Errorf("stops %v", s.stops)
	}
	got, _ := m.Get(c.Id)
	if got.Status != model.CaptureStatusFailed || *got.StopReason != model.CaptureStopReasonError || !strings.Contains(*got.Message, "service stopped") || *got.Packets != 4 {
		t.Errorf("%+v", got)
	}
	if _, err := m.Start(context.Background(), uplinkRequest()); !errors.Is(err, capturemgr.ErrClosed) {
		t.Errorf("a closed manager starts a capture: %v", err)
	}
}

// The records survive a restart: a stopped capture as it was, one that was running as failed; what is no capture is left alone.
func TestTheRecordsOfCapturesSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	m, op, _ := newManager(t, dir)
	stopped, err := m.Start(context.Background(), uplinkRequest())
	if err != nil {
		t.Fatal(err)
	}
	op.last().end(executor.CaptureEvent{Kind: executor.CaptureStopped, Reason: "duration", Packets: 9, Size: 900})
	eventually(t, "the first capture stops", func() bool { got, _ := m.Get(stopped.Id); return got.Status == model.CaptureStatusStopped })
	running, err := m.Start(context.Background(), uplinkRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "not-a-capture"), 0o750); err != nil {
		t.Fatal(err)
	}
	junk := uuid.NewString()
	if err := os.Mkdir(filepath.Join(dir, junk), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, junk, "capture.json"), []byte("{not json"), 0o640); err != nil {
		t.Fatal(err)
	}
	// the process ends here: the record of the second capture says running
	m2, _, _ := newManager(t, dir)
	if err := m2.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err := m2.Get(stopped.Id)
	if err != nil || got.Status != model.CaptureStatusStopped || *got.StopReason != model.CaptureStopReasonDuration || *got.Packets != 9 {
		t.Errorf("%+v %v", got, err)
	}
	got, err = m2.Get(running.Id)
	if err != nil || got.Status != model.CaptureStatusFailed || *got.StopReason != model.CaptureStopReasonError || got.StoppedAt == nil {
		t.Errorf("%+v %v", got, err)
	}
	if n := len(m2.List(nil)); n != 2 {
		t.Errorf("%d captures", n)
	}
	m.Close(context.Background())
}

func TestADownloadJoinsTheFilesOfARingAndCutsAFileThatIsBeingWritten(t *testing.T) {
	dir := t.TempDir()
	m, _, _ := newManager(t, dir)
	c, err := m.Start(context.Background(), capturemgr.StartRequest{CaptureRequest: model.CaptureRequest{Name: ptr("my capture/../x"), Format: ptr(model.CaptureRequestFormatPcap),
		Scope: model.CaptureScope{Uplink: ptr(model.CaptureScopeUplink(true))}}})
	if err != nil {
		t.Fatal(err)
	}
	// two files of a ring, the second cut in the middle of a record
	dst := filepath.Join(dir, c.Id.String())
	frame := bytes.Repeat([]byte{0xab}, 60)
	for i, n := range []int{3, 2} {
		var buf bytes.Buffer
		w, err := capture.NewWriter(&buf, capture.PCAP, []capture.InterfaceInfo{{Name: "wan0", Link: capture.LinkEthernet, Snaplen: 65535}}, "")
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < n; j++ {
			_ = w.WritePacket(capture.Packet{Time: t0, Data: frame, OrigLen: 60})
		}
		_ = w.Flush()
		b := buf.Bytes()
		if i == 1 {
			b = b[:len(b)-10]
		}
		name := []string{"capture-00001.pcap", "capture-00002.pcap"}[i]
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	d, err := m.Open(c.Id, "capture")
	if err != nil {
		t.Fatal(err)
	}
	if d.ContentType != "application/vnd.tcpdump.pcap" || d.Name != "my_capture_.._x.pcap" && !strings.HasSuffix(d.Name, ".pcap") || strings.ContainsAny(d.Name, `/"\`) {
		t.Errorf("%+v", d)
	}
	var out bytes.Buffer
	if _, err := d.WriteTo(&out); err != nil {
		t.Fatal(err)
	}
	r, err := capture.NewReader(&out)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		if _, err := r.Next(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 4 {
		t.Errorf("%d packets, want the 3 of the first file and the complete one of the second", n)
	}
	if _, err := m.Open(c.Id, "keylog"); !errors.Is(err, capturemgr.ErrNoKeylog) {
		t.Errorf("a key log: %v", err)
	}
	if _, err := m.Open(uuid.New(), "capture"); !errors.Is(err, capturemgr.ErrNotFound) {
		t.Errorf("an unknown capture: %v", err)
	}
	m.Close(context.Background())
}

// pcapFile is a pcap file with one packet of 60 bytes that all have the value fill.
func pcapFile(t *testing.T, fill byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := capture.NewWriter(&buf, capture.PCAP, []capture.InterfaceInfo{{Name: "wan0", Link: capture.LinkEthernet, Snaplen: 65535}}, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WritePacket(capture.Packet{Time: t0, Data: bytes.Repeat([]byte{fill}, 60), OrigLen: 60})
	_ = w.Flush()
	return buf.Bytes()
}

// The ring names its files with five digits up to 99999 and with more after that: those files are the capture too.
func TestTheFilesOfARingBeyondNumber99999AreDownloadedInOrderAndCounted(t *testing.T) {
	dir := t.TempDir()
	m, _, _ := newManager(t, dir)
	c, err := m.Start(context.Background(), capturemgr.StartRequest{CaptureRequest: model.CaptureRequest{Format: ptr(model.CaptureRequestFormatPcap),
		Scope: model.CaptureScope{Uplink: ptr(model.CaptureScopeUplink(true))}}})
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, c.Id.String())
	var size int64
	// the ring has deleted the files below 99998, so the directory holds these
	for i, name := range []string{"capture-99998.pcap", "capture-99999.pcap", "capture-100000.pcap", "capture-100001.pcap"} {
		b := pcapFile(t, byte(i+1))
		size += int64(len(b))
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	d, err := m.Open(c.Id, "capture")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := d.WriteTo(&out); err != nil {
		t.Fatal(err)
	}
	r, err := capture.NewReader(&out)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for {
		p, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p.Data[0])
	}
	if !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Errorf("the download holds the packets %v, want the four files in the order they were written", got)
	}
	// a restart sizes a capture that was running from its files
	m2, _, _ := newManager(t, dir)
	if err := m2.Recover(); err != nil {
		t.Fatal(err)
	}
	got2, err := m2.Get(c.Id)
	if err != nil || got2.Status != model.CaptureStatusFailed || got2.SizeBytes == nil || *got2.SizeBytes != size {
		t.Errorf("the recovered capture %+v (%v), want a size of %d bytes", got2, err, size)
	}
	m.Close(context.Background())
}

func TestTheCapturesOfARunAreDeletedWhenItsRecordIs(t *testing.T) {
	dir := t.TempDir()
	m, op, _ := newManager(t, dir)
	run := uuid.New()
	req := uplinkRequest()
	req.Run = &run
	c, err := m.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	m.DeleteRun(run) // it still runs: it stays
	if _, err := m.Get(c.Id); err != nil {
		t.Fatal(err)
	}
	op.last().end(executor.CaptureEvent{Kind: executor.CaptureStopped, Reason: "run_end"})
	eventually(t, "the capture stops", func() bool { got, _ := m.Get(c.Id); return got.Status == model.CaptureStatusStopped })
	other, _ := m.Start(context.Background(), uplinkRequest())
	m.DeleteRun(run)
	if _, err := m.Get(c.Id); !errors.Is(err, capturemgr.ErrNotFound) {
		t.Errorf("the capture of the run: %v", err)
	}
	if _, err := m.Get(other.Id); err != nil {
		t.Errorf("a capture of no run is gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, c.Id.String())); !os.IsNotExist(err) {
		t.Errorf("its directory: %v", err)
	}
	m.Close(context.Background())
}
