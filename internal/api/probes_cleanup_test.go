package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/diag"
)

// Tests of what a calibration leaves behind and of what it judges: the overlay it makes is removed on every path (a cancel, a
// probe whose run fails, a server that goes), and a run that was cut short is no verdict (P6-AUDIT-04, P6-AUDIT-05).

const lteProfile = "6b0f8a1e-2c3d-4e5f-8a9b-0c1d2e3f4a5b"

func lteBed(t *testing.T) *probeBed {
	t.Helper()
	b := newProbeBed(t, func(p map[string]any) {
		p["profiles"] = map[string]any{lteProfile: map[string]any{"name": "lte-ish", "parts": map[string]any{"impairment": map[string]any{"latency": "50ms"}}}}
	})
	b.address("10.10.0.77/24")
	b.net.delay = 100 * time.Millisecond
	return b
}

// hookNet is the stack of the probe with a hook on every connection and an optional wrapper of the connection.
type hookNet struct {
	diag.Net
	onDial func() error
	wrap   func(net.Conn) net.Conn
}

func (h hookNet) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if h.onDial != nil {
		if err := h.onDial(); err != nil {
			return nil, err
		}
	}
	c, err := h.Net.DialContext(ctx, network, addr)
	if err == nil && h.wrap != nil {
		c = h.wrap(c)
	}
	return c, err
}

// writeLimit is a connection whose writes fail after n of them, as when the probe's address is gone.
type writeLimit struct {
	net.Conn
	left int
}

func (w *writeLimit) Write(b []byte) (int, error) {
	if w.left <= 0 {
		return 0, syscall.ENETUNREACH
	}
	w.left--
	return w.Conn.Write(b)
}

func (b *probeBed) waitNoOverlays() {
	b.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(b.e.Snapshot().Overlays) != 0 {
		if time.Now().After(deadline) {
			b.t.Fatalf("%d overlays are left behind", len(b.e.Snapshot().Overlays))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// calibratableAgain waits until no calibration of the probe runs (the one that was cut off has to end) and checks that a
// new one is not refused.
func (b *probeBed) calibratableAgain(port int) {
	b.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := b.calibrate(map[string]any{"fault": map[string]any{"latency": "1ms"}, "host": loopback, "port": port, "duration": "5s"})
		if r.Status != 409 {
			if r.Status != 200 {
				b.t.Fatalf("the probe cannot be calibrated again: %d %s", r.Status, r.Body)
			}
			break
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("the probe is calibrating for ever: %s", r.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	b.waitNoOverlays()
}

// verdicts are the audit entries of calibrations that ended with a verdict, ended are those that ended without one.
func (b *probeBed) calibrationEntries() (verdicts, ended []string) {
	items, _, _ := b.log.List(auditFilter(), "", 200)
	for _, e := range items {
		switch {
		case e.Action != "probe.calibrate":
		case strings.Contains(e.Detail, "no verdict"):
			ended = append(ended, e.Detail)
		default:
			verdicts = append(verdicts, e.Detail)
		}
	}
	return verdicts, ended
}

func TestACalibrationTheCallerCancelsLeavesNoOverlayAndNoVerdict(t *testing.T) {
	b := lteBed(t)
	port := echoPair(t, 0)
	body, _ := json.Marshal(map[string]any{"profile": "lte-ish", "host": loopback, "port": port, "duration": "20s"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.ts.URL+"/api/v1/probes/"+probeID+"/calibrate", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.token)
	done := make(chan error, 1)
	go func() {
		res, err := b.client.Do(req)
		if err == nil {
			_ = res.Body.Close()
		}
		done <- err
	}()
	// the overlay is there while the probe's run goes
	deadline := time.Now().Add(10 * time.Second)
	for len(b.e.Snapshot().Overlays) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the calibration made no overlay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	b.waitNoOverlays()
	b.calibratableAgain(port)
	verdicts, ended := b.calibrationEntries()
	if len(ended) != 1 || !strings.Contains(ended[0], "cancelled") {
		t.Errorf("the audit log does not say the calibration was cancelled: %v", ended)
	}
	for _, v := range verdicts {
		if strings.Contains(v, "profile lte-ish") {
			t.Errorf("a cancelled run was judged: %s", v)
		}
	}
}

func TestAProbeWhoseRunFailsAfterTheOverlayIsPutLeavesNoOverlay(t *testing.T) {
	b := lteBed(t)
	port := echoPair(t, 0)
	// the run fails in the executor (the probe's stack cannot connect) once the overlay is in place
	var overlaysAtFailure int
	b.net.inner = hookNet{Net: diag.HostNet{}, onDial: func() error {
		overlaysAtFailure = len(b.e.Snapshot().Overlays)
		return errors.New("connect: network is unreachable")
	}}
	r := b.calibrate(map[string]any{"profile": "lte-ish", "host": loopback, "port": port, "duration": "5s"})
	if r.Status != 503 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if overlaysAtFailure != 1 {
		t.Fatalf("%d overlays when the run failed: the test does not fail after the overlay", overlaysAtFailure)
	}
	b.waitNoOverlays()
	b.net.inner = diag.HostNet{}
	b.calibratableAgain(port)
	if verdicts, ended := b.calibrationEntries(); len(ended) != 1 || len(verdicts) != 1 { // the later one is the new call's
		t.Errorf("%v %v", verdicts, ended)
	}
}

func TestAnEchoServerThatGoesAfterTheReferenceLeavesNoOverlay(t *testing.T) {
	b := lteBed(t)
	pc, err := net.ListenPacket("udp4", loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	// the probe's run is the first one to reach the server through the probe's stack: the server is gone by then
	b.net.inner = hookNet{Net: diag.HostNet{}, onDial: func() error { _ = pc.Close(); return nil }}
	r := b.calibrate(map[string]any{"profile": "lte-ish", "host": loopback, "port": port, "duration": "5s"})
	if r.Status == 0 {
		t.Fatal("no answer")
	}
	b.waitNoOverlays()
	b.net.inner = diag.HostNet{}
	b.calibratableAgain(echoPair(t, 0))
}

func TestAnEchoRunThatTheSocketCutShortIsNoVerdict(t *testing.T) {
	b := lteBed(t)
	port := echoPair(t, 0)
	b.net.inner = hookNet{Net: diag.HostNet{}, wrap: func(c net.Conn) net.Conn { return &writeLimit{Conn: c, left: 50} }}
	r := b.calibrate(map[string]any{"profile": "lte-ish", "host": loopback, "port": port, "duration": "5s"})
	if r.Status != 503 || !strings.Contains(string(r.Body), "cut short after 50 of 500") {
		t.Fatalf("a run of 50 datagrams of 500 is judged: %d %s", r.Status, r.Body)
	}
	b.waitNoOverlays()
	verdicts, ended := b.calibrationEntries()
	if len(verdicts) != 0 || len(ended) != 1 || !strings.Contains(ended[0], "cut short") {
		t.Errorf("%v %v", verdicts, ended)
	}
}

// Answers that need longer than three seconds are not lost ones (P6-AUDIT-08).
func TestAProfileWithALongRoundTripIsNotJudgedLossy(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["profiles"] = map[string]any{lteProfile: map[string]any{"name": "geo", "parts": map[string]any{"impairment": map[string]any{"latency": "1600ms"}}}}
	})
	b.address("10.10.0.77/24")
	b.net.delay = 3200 * time.Millisecond // 1.6 s in each direction
	port := echoPair(t, 0)
	r := b.calibrate(map[string]any{"profile": "geo", "host": loopback, "port": port, "duration": "5s"})
	res := r.json(t)
	if r.Status != 200 || res["within_tolerance"] != true || !checks(res)["loss"] || !checks(res)["latency"] {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if s := sub(res, "samples"); s["sent"] != s["received"] {
		t.Errorf("answers were counted as lost: %v", s)
	}
}

// The time a packet takes to leave a direction with a low rate is part of the delay the profile makes (P6-AUDIT-09).
func TestTheExpectedRoundTripIncludesTheTimeAPacketTakesAtALowRate(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["profiles"] = map[string]any{lteProfile: map[string]any{"name": "modem", "parts": map[string]any{"impairment": map[string]any{"latency": "50ms", "rate": "56kbit"}}}}
	})
	b.address("10.10.0.77/24")
	b.net.delay = 121 * time.Millisecond
	port := echoPair(t, 7000)
	r := b.calibrate(map[string]any{"profile": "modem", "host": loopback, "port": port, "duration": "5s"})
	res := r.json(t)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if rtt := sub(res, "expected")["rtt_ms"].(float64); rtt < 120.5 || rtt > 123 {
		t.Errorf("expected %v ms: 2 * 50 ms and 2 * 10.6 ms at 56 kbit/s", rtt)
	}
	if !checks(res)["latency"] {
		t.Errorf("a correct delay at a low rate failed: %s", r.Body)
	}
}

// A run takes up to a minute: a probe runs one diagnostic at a time (P6-AUDIT-02).
func TestAProbeRunsOneDiagnosticAtATime(t *testing.T) {
	b := newProbeBed(t, nil)
	b.address("10.10.0.77/24")
	// a TLS server that never speaks: the handshake waits for the timeout
	l, err := net.Listen("tcp4", loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	body := map[string]any{"type": "tls", "host": loopback, "port": l.Addr().(*net.TCPAddr).Port, "sni": "broker.example.com", "timeout": "2s", "from": map[string]any{"probe": "probe-a"}}
	first := make(chan resp, 1)
	go func() { first <- b.diagnose(body) }()
	time.Sleep(500 * time.Millisecond)
	if r := b.diagnose(body); r.Status != 409 || !strings.Contains(string(r.Body), "target_busy") {
		t.Errorf("a second diagnostic from the same probe: %d %s", r.Status, r.Body)
	}
	<-first
	if r := b.diagnose(body); r.Status != 200 {
		t.Errorf("after the first ended: %d %s", r.Status, r.Body)
	}
}
