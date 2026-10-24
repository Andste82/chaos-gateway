//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The acceptance of issue #79 on the real kernel (issue #92): the online state and the byte rates of
// a device as the gateway observes them, with the observation wired the way the product wires it
// (cmd/chaosgw/api.go): the poller every second, the neighbor watch and the conntrack watch with a
// 100 ms debounce. The devices are the configured devices A, B and C of withDevices, known by their
// addresses, so a test can look a device up by its id.

// obsConntrackFallback is the engine's conntrackFallback: while the conntrack watch streams, the
// poller reads conntrack at least this often, so traffic a device sent shows up at most this late.
const obsConntrackFallback = 10 * time.Second

// obsBed is the testbed with the real executor and the engine on the real clock, observing.
type obsBed struct {
	t   *testing.T
	top *testbed.Topology
	e   *engine.Engine
	// offlineAfter is the engine's Config.OfflineAfter (0 there means 60 s).
	offlineAfter time.Duration
	// mu guards events, which collects every device event from the start of the observation.
	mu     sync.Mutex
	events []engine.Event
}

// newObsBed applies the testbed configuration with the devices of withDevices and starts the
// observation as the product does. offlineAfter is the engine's Config.OfflineAfter; 0 keeps the
// product's 60 s.
func newObsBed(t *testing.T, offlineAfter time.Duration) *obsBed {
	t.Helper()
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false), testbed.WithGatewayBridges(false))
	ex, err := executor.New(executor.NewExecRunner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	raw, err := os.ReadFile("testdata/testbed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	withDevices(cfg)
	engineLog := &lockedBuffer{}
	e, err := engine.New(engine.Config{Store: st, Exec: apply.Local{E: ex}, Namespace: top.GW.Name, Clock: &clock.Real{},
		OfflineAfter: offlineAfter,
		Log:          slog.New(slog.NewTextHandler(engineLog, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	rev, err := st.Create(cfg, store.CreateOptions{IfMatch: st.ActiveID(), Now: time.Now(), By: model.Actor{Id: "admin", Type: "user"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := e.Apply(ctx, rev.Id, engine.ApplyOptions{SkipConfirm: true}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if offlineAfter == 0 {
		offlineAfter = 60 * time.Second
	}
	b := &obsBed{t: t, top: top, e: e, offlineAfter: offlineAfter}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("events:\n%s", b.eventLog())
			t.Logf("engine log:\n%s", engineLog.String())
		}
	})

	// every device event, with the time the engine gave it; the subscription ends with the test
	ch, unsubscribe := e.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			if strings.HasPrefix(ev.Type, "device_") {
				b.mu.Lock()
				b.events = append(b.events, ev)
				b.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { unsubscribe(); <-done })

	// the observation of cmd/chaosgw/api.go
	if err := e.PollObserved(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := e.FollowNeighbors(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := e.FollowConntrack(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for len(e.Snapshot().Identity.Addresses) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("the engine did not resolve the devices: %+v", e.Snapshot().Identity)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return b
}

// device returns the observed state of the device with the id.
func (b *obsBed) device(id string) engine.DeviceState {
	b.t.Helper()
	for _, d := range b.e.Snapshot().Devices {
		if d.ID == id {
			return d
		}
	}
	b.t.Fatalf("no device %s in %+v", id, b.e.Snapshot().Devices)
	return engine.DeviceState{}
}

// waitOnline waits until the device is online.
func (b *obsBed) waitOnline(id string, d time.Duration) engine.DeviceState {
	b.t.Helper()
	deadline := time.Now().Add(d)
	for {
		st := b.device(id)
		if st.Online {
			return st
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("the device %s is not online after %v: %+v", id, d, st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// eventsOf returns the events of type typ about the device with the id, from since on.
func (b *obsBed) eventsOf(id, typ string, since time.Time) []engine.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []engine.Event
	for _, ev := range b.events {
		if ev.Type == typ && ev.Data["device"] == id && !ev.Time.Before(since) {
			out = append(out, ev)
		}
	}
	return out
}

func (b *obsBed) eventLog() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	for _, ev := range b.events {
		fmt.Fprintf(&sb, "  %s %s %v\n", ev.Time.Format("15:04:05.000"), ev.Type, ev.Data)
	}
	return sb.String()
}

// neighborOf returns the gateway's neighbor entry for the address: its interface and its state.
func (b *obsBed) neighborOf(addr string) (dev, state string) {
	out, err := b.top.GW.Run(context.Background(), "ip", "-j", "neigh", "show", "to", addr)
	if err != nil {
		return "", "error: " + err.Error()
	}
	var entries []struct {
		Dev   string   `json:"dev"`
		State []string `json:"state"`
	}
	if json.Unmarshal([]byte(out), &entries) != nil || len(entries) == 0 {
		return "", "none"
	}
	return entries[0].Dev, strings.Join(entries[0].State, ",")
}

// longestReachable is how long the gateway keeps a neighbor entry on the interface REACHABLE after
// its last confirmation: base_reachable_time randomized up to one and a half times (30 s gives 15 to
// 45 s).
func (b *obsBed) longestReachable(dev string) time.Duration {
	b.t.Helper()
	out, err := b.top.GW.Run(context.Background(), "cat", "/proc/sys/net/ipv4/neigh/"+dev+"/base_reachable_time_ms")
	ms, perr := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || perr != nil {
		b.t.Logf("cannot read the base reachable time of %s (%v %v): assuming the kernel's 30 s", dev, err, perr)
		ms = 30000
	}
	return time.Duration(ms) * time.Millisecond * 3 / 2
}

// obsLinkRate is the rate the server's link is shaped to in the rate test: 1 MB/s.
const obsLinkRate = 8e6 // bit/s

// Issue #92, scenario 1 (acceptance of #79): a device with a steady transfer of 1 MB/s shows about
// 1 MB/s (±20 %) in every sample a UI takes every 5 s, never "unknown" and never more than the link
// can carry. Before #79 most samples were empty (a tick without a conntrack read reset the baseline)
// and the others GB/s spikes (the next read took the connection's whole lifetime as its delta).
//
// The server sends to device A over one TCP connection as fast as the link lets it; tbf on the
// server's interface shapes the link to 8 Mbit/s. conntrack counts IP bytes, tbf counts frames with
// their Ethernet header, so the download is about 1 % below 1 MB/s; the upload is A's ACKs. With
// native execution or KVM (testbed.Accurate) every sample must be within ±20 % of the link rate and
// download and upload together at most 20 % above it; under emulation the timing is too noisy for
// that, and every sample must only be known, non-zero and at most twice the link rate (the bug's
// spikes were orders of magnitude above it).
func TestASteadyTransferShowsASteadyRateInEverySampleOnTheRealKernel(t *testing.T) {
	b := newObsBed(t, 0)
	b.top.Server.Must("tc", "qdisc", "replace", "dev", "eth0", "root", "tbf", "rate", "8mbit", "burst", "16kb", "latency", "200ms")
	srv := b.top.Server.Start("python3", "-c", `import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 9600)); s.listen(1)
c, a = s.accept()
buf = b"\0" * 65536
try:
    while True:
        c.sendall(buf)
except OSError:
    pass
`)
	time.Sleep(500 * time.Millisecond)
	const warmUp, samples, every = 2*obsConntrackFallback + 5*time.Second, 13, 5 * time.Second
	transfer := warmUp + samples*every + 10*time.Second
	cli := b.top.A.Start("python3", "-c", fmt.Sprintf(`import socket, time
s = socket.create_connection(("%s", 9600), timeout=10)
print("connected", flush=True)
end = time.time() + %d
n = 0
while time.time() < end:
    d = s.recv(65536)
    if not d:
        break
    n += len(d)
print("received", n, flush=True)
`, testbed.ServerAddr, int(transfer.Seconds())))
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(cli.Output(), "connected") {
		if time.Now().After(deadline) {
			t.Fatalf("A cannot connect to the server: %q, server: %q", cli.Output(), srv.Output())
		}
		time.Sleep(100 * time.Millisecond)
	}
	// the rate window must hold nothing but the transfer: it spans the reads of the last 10 s, and a
	// steady connection sends no conntrack events, so the reads come every conntrack fallback (10 s)
	// and the first read of the window can lie up to 20 s back
	time.Sleep(warmUp)
	m := testbed.Measure(t, "A, a transfer shaped to 1 MB/s")
	for i := range samples {
		if i > 0 {
			time.Sleep(every)
		}
		d := b.device(flDevA)
		t.Logf("sample %2d: known %v, flows %d, download %.3f MB/s, upload %.3f MB/s", i, d.TrafficKnown, d.FlowsActive, float64(d.DownloadBps)/1e6, float64(d.UploadBps)/1e6)
		if !d.TrafficKnown || d.FlowsActive == 0 {
			t.Errorf("sample %d: the traffic is not known or no flow is active: %+v", i, d)
			continue
		}
		down, total := float64(d.DownloadBps)*8, float64(d.DownloadBps+d.UploadBps)*8
		if testbed.Accurate() {
			if err := m.Rate("download", down, obsLinkRate, 0.2); err != nil {
				t.Errorf("sample %d: %v", i, err)
			}
			if total > 1.2*obsLinkRate {
				t.Errorf("sample %d: download and upload together are %.3f Mbit/s, more than the link can carry (%.0f Mbit/s + 20 %%)", i, total/1e6, obsLinkRate/1e6)
			}
		} else if down <= 0 || total > 2*obsLinkRate {
			t.Errorf("sample %d: %.3f Mbit/s down, %.3f Mbit/s in all: want a rate above 0 and at most twice the link's %.0f Mbit/s", i, down/1e6, total/1e6, obsLinkRate/1e6)
		}
	}
	select {
	case <-cli.Done():
		t.Logf("A: %s", strings.TrimSpace(cli.Output()))
	case <-time.After(20 * time.Second):
		t.Logf("A has not finished: %s", strings.TrimSpace(cli.Output()))
	}
}

// Issue #92, scenario 2 (acceptance of #79): a device that is there and talks at least every 30 s
// stays online without a flap. Before #79 the state was worked out anew every second: a device with
// sporadic traffic went offline in every observation without a fresh neighbor entry or a connection.
//
// Device B sends one UDP datagram every 30 s from the same socket to the server, which never answers:
// nothing the gateway sends to B confirms B's neighbor entry, so the datagrams (the conntrack counters
// of B's connection that grow, or a new connection once the unreplied entry expired after 30 s) are
// B's only sign of life. The engine's OfflineAfter is the product's 60 s. #79 asked for 10 minutes;
// the test watches 150 s, five intervals between datagrams and two and a half times OfflineAfter:
// every interval goes through the same states (the unreplied UDP entry expires after 30 s, B's
// neighbor entry stays STALE), so a flap that 10 minutes would show shows within the first intervals.
// The state is sampled every 500 ms and every device event is recorded; neither may say offline, and
// the longest time without a sign of life (at most 30 s plus the 10 s a counter may wait for the next
// conntrack read) goes into the measurement record.
func TestADeviceThatTalksEveryThirtySecondsStaysOnlineWithoutFlapsOnTheRealKernel(t *testing.T) {
	b := newObsBed(t, 0)
	srv := b.top.Server.Start("python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", 9500))
while True:
    d, a = s.recvfrom(64)
    print("got", a[0], flush=True)
`)
	time.Sleep(500 * time.Millisecond)
	const interval, watch = 30 * time.Second, 150 * time.Second
	sends := int(watch/interval) + 2
	cli := b.top.B.Start("python3", "-c", fmt.Sprintf(`import socket, time
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for i in range(%d):
    s.sendto(b"here", ("%s", 9500))
    print("sent", time.time(), flush=True)
    time.sleep(%d)
`, sends, testbed.ServerAddr, int(interval.Seconds())))
	b.waitOnline(flDevB, 15*time.Second)
	start := time.Now()
	var longest time.Duration
	offlineSamples := 0
	for time.Since(start) < watch {
		time.Sleep(500 * time.Millisecond)
		d := b.device(flDevB)
		if !d.Online {
			offlineSamples++
			if offlineSamples == 1 {
				t.Errorf("B is offline %v into the observation: %+v", time.Since(start).Round(time.Second), d)
			}
		}
		if age := time.Since(d.LastSeen); !d.LastSeen.IsZero() && age > longest {
			longest = age
		}
	}
	if offlineSamples > 0 {
		t.Errorf("B was offline in %d samples", offlineSamples)
	}
	for _, typ := range []string{engine.EventDeviceOffline, engine.EventDeviceOnline} {
		if evs := b.eventsOf(flDevB, typ, start); len(evs) > 0 {
			t.Errorf("%d %s events of B during the observation: a flap", len(evs), typ)
		}
	}
	got := strings.Count(srv.Output(), "got ")
	t.Logf("B sent %d datagrams, the server got %d; the longest time without a sign of life was %v; B: %s",
		strings.Count(cli.Output(), "sent "), got, longest.Round(100*time.Millisecond), strings.Join(strings.Fields(cli.Output()), " "))
	if got < int(watch/interval) {
		t.Errorf("the server got %d datagrams in %v, want at least %d: B did not talk every %v", got, watch, int(watch/interval), interval)
	}
	if err := testbed.Measure(t, "B, a datagram every 30 s").Duration("", "longest time without a sign of life", longest, interval, 0, b.offlineAfter); err != nil {
		t.Error(err)
	}
}

// Issue #92, scenario 3 (acceptance of #79): a device that is unplugged goes offline OfflineAfter
// after its last sign of life even though its TCP connection stays established in conntrack, and not
// before. Before #79 the mere existence of a connection kept a device online, and an established
// TCP entry lives for 5 days.
//
// Device A keeps a TCP connection to the server and sends a byte every 200 ms; the test makes the
// engine read once more (A's counters grew: A is seen now) and brings A's interface down at once. The
// engine's OfflineAfter is 20 s so the test stays short. The device must stay online for OfflineAfter
// after that last sighting, go offline within one poll (1 s) after OfflineAfter has passed since its
// last sign of life, and that last sign of life must lie at most this long after the unplug:
//
//   - the conntrack fallback (10 s): what A sent just before the unplug is counted by the next read;
//   - or, when the gateway's neighbor entry of A was REACHABLE at the unplug, until that entry turns
//     STALE, at most one and a half times base_reachable_time (45 s): plan §2.3 counts a REACHABLE
//     entry as a sign of life, and the kernel keeps it REACHABLE for its reachable time after the last
//     confirmation, whether the device is still there or not.
//
// After the device went offline its connection is still established in conntrack.
func TestAnUnpluggedDeviceWithAnOpenConnectionGoesOfflineAfterOfflineAfterOnTheRealKernel(t *testing.T) {
	const offlineAfter = 20 * time.Second
	b := newObsBed(t, offlineAfter)
	srv := b.top.Server.Start("python3", "-c", `import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 9700)); s.listen(1)
c, a = s.accept()
print("accepted", flush=True)
while c.recv(4096):
    pass
`)
	time.Sleep(500 * time.Millisecond)
	cli := b.top.A.Start("python3", "-c", fmt.Sprintf(`import socket, time
s = socket.create_connection(("%s", 9700), timeout=10)
print("connected", flush=True)
while True:
    s.send(b"x")
    time.sleep(0.2)
`, testbed.ServerAddr))
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(srv.Output(), "accepted") {
		if time.Now().After(deadline) {
			t.Fatalf("A cannot connect to the server: %q, server: %q", cli.Output(), srv.Output())
		}
		time.Sleep(100 * time.Millisecond)
	}
	b.waitOnline(flDevA, 15*time.Second)
	time.Sleep(3 * time.Second)

	// the last sighting: a read now finds A's counters grown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.e.ObserveNow(ctx); err != nil {
		t.Fatal(err)
	}
	before := b.device(flDevA)
	unplug := time.Now()
	b.top.A.Must("ip", "link", "set", "eth0", "down")
	dev, state := b.neighborOf(testbed.ClientAAddr)
	t.Logf("unplugged %v after the last sighting; the gateway's neighbor entry of A: %s on %s", unplug.Sub(before.LastSeen).Round(time.Millisecond), state, dev)
	if !before.Online || unplug.Sub(before.LastSeen) > 2*time.Second {
		t.Fatalf("A was not seen just before the unplug: %+v", before)
	}
	lateEvidence := obsConntrackFallback
	if strings.Contains(state, "REACHABLE") {
		lateEvidence = max(lateEvidence, b.longestReachable(dev))
	}
	slack := 3 * time.Second
	if !testbed.Accurate() {
		slack = 10 * time.Second
	}

	// wait for offline, watching the state every 200 ms
	var offline time.Time
	deadline = unplug.Add(lateEvidence + offlineAfter + slack + 5*time.Second)
	for time.Now().Before(deadline) {
		if evs := b.eventsOf(flDevA, engine.EventDeviceOffline, unplug); len(evs) > 0 {
			offline = evs[0].Time
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	after := b.device(flDevA)
	dev2, state2 := b.neighborOf(testbed.ClientAAddr)
	if offline.IsZero() {
		t.Fatalf("A is not offline %v after the unplug (OfflineAfter %v, signs of life up to %v after the unplug): %+v; neighbor entry %s on %s",
			time.Since(unplug).Round(time.Second), offlineAfter, lateEvidence, after, state2, dev2)
	}
	t.Logf("offline %v after the unplug, %v after the last sign of life, which was %v after the unplug; neighbor entry now %s on %s",
		offline.Sub(unplug).Round(time.Millisecond), offline.Sub(after.LastSeen).Round(time.Millisecond), after.LastSeen.Sub(unplug).Round(time.Millisecond), state2, dev2)
	if after.Online {
		t.Errorf("the offline event was sent, but the state says online: %+v", after)
	}
	if offline.Before(before.LastSeen.Add(offlineAfter)) {
		t.Errorf("A went offline %v after its last sighting before the unplug, before OfflineAfter (%v)", offline.Sub(before.LastSeen), offlineAfter)
	}
	m := testbed.Measure(t, "A, unplugged with an open TCP connection")
	if err := m.Duration("", "offline after the last sign of life", offline.Sub(after.LastSeen), offlineAfter, offlineAfter, offlineAfter+time.Second+slack); err != nil {
		t.Error(err)
	}
	if err := m.Duration("", "last sign of life after the unplug", after.LastSeen.Sub(unplug), 0, -time.Second, lateEvidence+slack); err != nil {
		t.Error(err)
	}
	if evs := b.eventsOf(flDevA, engine.EventDeviceOnline, unplug); len(evs) > 0 {
		t.Errorf("A came online again after the unplug: %v", evs)
	}
	if n := len(b.eventsOf(flDevA, engine.EventDeviceOffline, unplug)); n != 1 {
		t.Errorf("%d offline events of A after the unplug, want 1", n)
	}

	// the connection is still there: it is not what made A offline
	fctx, fcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer fcancel()
	flows, err := b.e.Flows(fctx)
	if err != nil {
		t.Fatal(err)
	}
	established := false
	for _, f := range flows {
		if f.Device == flDevA && f.DPort == 9700 && f.Protocol == "tcp" {
			t.Logf("A's connection after it went offline: %s, up %d B, down %d B", f.State, f.Upload.Bytes, f.Download.Bytes)
			established = established || strings.EqualFold(f.State, "ESTABLISHED")
		}
	}
	if !established {
		t.Errorf("A's TCP connection is not established in conntrack any more, the test shows nothing: %+v", flows)
	}
}
