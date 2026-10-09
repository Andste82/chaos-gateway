package engine_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

const (
	wgHub    = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	wgClient = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

// newWGHarness is the harness with the WireGuard testbed configuration: keys in a secrets store the
// executor reads, the configuration provisioned.
func newWGHarness(t *testing.T) (*harness, *secrets.Store) {
	return newWGHarnessFile(t, "wireguard.yaml")
}

func newWGHarnessFile(t *testing.T, file string) (*harness, *secrets.Store) {
	return newWGHarnessCfg(t, file, engine.Config{})
}

// newWGHarnessCfg is newWGHarness for a configuration file of the compiler's test data, with the engine's configuration (the
// limits a test sets).
func newWGHarnessCfg(t *testing.T, file string, cfg engine.Config) (*harness, *secrets.Store) {
	return newWGHarnessWrap(t, file, cfg, nil)
}

// newWGHarnessWrap is newWGHarnessCfg with the executor running its commands through wrap(kernel), for tests that hold what
// reaches the kernel.
func newWGHarnessWrap(t *testing.T, file string, cfg engine.Config, wrap func(*kernelsim.Kernel) executor.Runner) (*harness, *secrets.Store) {
	t.Helper()
	h := newHarnessRunner(t, wrap)
	sec, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var run executor.Runner = h.k
	if wrap != nil {
		run = wrap(h.k)
	}
	ex, err := executor.New(run, executor.WithBirdDir(t.TempDir()), executor.WithKeys(func(id string) (string, string, error) {
		k, err := sec.WireGuard(id)
		return k.PrivateKey, k.PresharedKey, err
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	h.ex = ex
	raw, err := os.ReadFile("../compiler/testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	conf, errs := domain.Normalize(conf)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if conf, err = wireguard.Provision(conf, sec); err != nil {
		t.Fatal(err)
	}
	h.base = conf
	h.sec = sec
	cfg.Store, cfg.Exec, cfg.Clock, cfg.Secrets = h.st, apply.Local{E: ex}, h.clk, sec
	h.classLimit = cfg.ClassLimit
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e = e
	return h, sec
}

func TestAWireGuardRevisionIsAppliedAndPublishedInTheSnapshot(t *testing.T) {
	h, sec := newWGHarness(t)
	rev := h.mustApply(h.revision(nil)).Revision
	s := h.e.Snapshot()
	if s.Revision != rev || len(s.WireGuardInterfaces) != 3 {
		t.Fatalf("%+v", s)
	}
	if s.Applied == nil || s.LastError != "" {
		t.Fatalf("%+v", s)
	}
	// no private key anywhere in what the engine publishes or stores
	hubKeys, _ := sec.WireGuard(wgHub)
	for _, secret := range []string{hubKeys.PrivateKey} {
		if secret == "" {
			t.Fatal("no key was generated")
		}
		if strings.Contains(mustJSON(s), secret) {
			t.Error("the snapshot holds a private key")
		}
		if rev, cfg, _ := h.st.Get(rev); strings.Contains(mustJSON(cfg), secret) || strings.Contains(mustJSON(rev), secret) {
			t.Error("the revision holds a private key")
		}
	}
	// the kernel has the tunnel
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Links["wg-lab-hub"].Kind() != "wireguard" || len(st.WireGuard["wg-lab-hub"].Peers) != 1 {
		t.Errorf("%+v", st.WireGuard)
	}
}

func TestPeersGoOnlineAndOfflineWithEvents(t *testing.T) {
	h, _ := newWGHarness(t)
	h.mustApply(h.revision(nil))
	ctx := context.Background()
	if err := h.e.PollWireGuard(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()

	// nothing has handshaken yet: every peer is offline and nothing is announced
	h.clk.BlockUntil(1)
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return len(s.WireGuard) == 2 })
	if got := events(ch); has(got, engine.EventPeerOnline) || has(got, engine.EventPeerOffline) {
		t.Fatalf("events %v", got)
	}
	for _, p := range h.e.Snapshot().WireGuard {
		if p.Online || !p.LastHandshake.IsZero() {
			t.Errorf("%+v", p)
		}
	}

	// the client handshakes
	pub := h.e.Snapshot().WireGuardInterfaces
	var hubIf, peerPub string
	for _, w := range pub {
		if w.NetworkID == wgHub {
			hubIf, peerPub = w.Name, w.Peers[0].PublicKey
		}
	}
	h.k.Handshake(hubIf, peerPub, h.clk.Now().Unix(), 100, 200)
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.WireGuard[wgClient].Online })
	st := h.e.Snapshot().WireGuard[wgClient]
	if st.Network != "lab-hub" || st.Name != "rA" || st.RxBytes != 100 || st.TxBytes != 200 || st.LastHandshake.IsZero() {
		t.Errorf("%+v", st)
	}
	if ev := collect(ch, engine.EventPeerOnline); len(ev) != 1 || ev[0].Data["peer"] != "rA" {
		t.Fatalf("online events %+v", ev)
	}

	// no further handshake: after three minutes the peer is offline
	h.clk.Advance(2 * time.Minute)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.WireGuard[wgClient].Online })
	h.clk.Advance(61 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return !s.WireGuard[wgClient].Online })
	if ev := collect(ch, engine.EventPeerOffline); len(ev) != 1 {
		t.Fatalf("offline events %+v", ev)
	}
}

// M6a-03 test: a flow's network is resolved against a WireGuard interface's own subnet or a peer's
// routed client network, not only the local test networks' bridges; a flow redirected into the service
// namespace for DNS carries service "dns_proxy".
func TestFlowsThroughWireGuardGetTheirNetworkAndService(t *testing.T) {
	h, _ := newWGHarness(t)
	h.mustApply(h.revision(nil))
	h.k.SetConntrack(
		"tcp      6 431999 ESTABLISHED src=10.99.0.50 dst=203.0.113.60 sport=1 dport=2 packets=3 bytes=300 src=203.0.113.60 dst=10.99.0.50 sport=2 dport=1 packets=2 bytes=200 [ASSURED] mark=0 use=1\n" +
			"udp      17 25 src=10.50.0.5 dst=203.0.113.50 sport=3 dport=53 packets=1 bytes=60 src=169.254.100.2 dst=10.50.0.5 sport=53 dport=3 packets=1 bytes=90 [ASSURED] mark=0 use=1\n")
	flows, err := h.e.Flows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var hub, dns *engine.Flow
	for i := range flows {
		switch flows[i].Src.String() {
		case "10.99.0.50":
			hub = &flows[i]
		case "10.50.0.5":
			dns = &flows[i]
		}
	}
	if hub == nil || hub.Network != wgHub || hub.Service != "" {
		t.Fatalf("flow in the hub's own subnet: %+v", hub)
	}
	if dns == nil || dns.Network != wgHub || dns.Service != "dns_proxy" {
		t.Fatalf("flow in the client's routed network, redirected to the service namespace: %+v", dns)
	}
}

func TestDisablingAClientTakesItOfflineWithAnEvent(t *testing.T) {
	h, _ := newWGHarness(t)
	h.mustApply(h.revision(nil))
	if err := h.e.PollWireGuard(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	var hubIf, peerPub string
	for _, w := range h.e.Snapshot().WireGuardInterfaces {
		if w.NetworkID == wgHub {
			hubIf, peerPub = w.Name, w.Peers[0].PublicKey
		}
	}
	h.k.Handshake(hubIf, peerPub, h.clk.Now().Unix(), 1, 1)
	h.clk.BlockUntil(1)
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.WireGuard[wgClient].Online })
	ch, cancel := h.e.Subscribe()
	defer cancel()

	h.mustApply(h.revision(func(c *model.Configuration) {
		n := (*c.Networks)[wgHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[wgClient]
		off := false
		cl.Enabled = &off
		(*wg.Clients)[wgClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[wgHub] = n
	}))
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { _, ok := s.WireGuard[wgClient]; return !ok })
	if ev := collect(ch, engine.EventPeerOffline); len(ev) != 1 || ev[0].Data["peer"] != "rA" {
		t.Fatalf("a disabled client goes offline with an event: %+v", ev)
	}
}

func waitStatus(t *testing.T, h *harness, ok func(*engine.Snapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok(h.e.Snapshot()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the status never became what the test waits for: %+v", h.e.Snapshot().WireGuard)
}

func collect(ch <-chan engine.Event, typ string) []engine.Event {
	var out []engine.Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			if ev.Type == typ {
				out = append(out, ev)
			}
		default:
			return out
		}
	}
}

func mustJSON(v any) string {
	b, _ := jsonMarshal(v)
	return string(b)
}

// wgRevision stores a candidate the way the product does: provisioned first, so its keys exist.
func wgRevision(t *testing.T, h *harness, sec *secrets.Store, mod func(*model.Configuration)) int64 {
	t.Helper()
	cfg := h.clone()
	if mod != nil {
		mod(cfg)
	}
	cfg, err := wireguard.Provision(cfg, sec)
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.st.Create(cfg, store.CreateOptions{IfMatch: h.st.ActiveID(), Now: h.clk.Now(), By: model.Actor{Id: "admin", Type: "user"}})
	if err != nil {
		t.Fatal(err)
	}
	return r.Id
}

func TestRotatingAClientKeyKeepsTheOldKeysUntilTheCommitAndAFailedApplyStillRestores(t *testing.T) {
	h, sec := newWGHarness(t)
	h.mustApply(h.revision(nil))
	before, _ := sec.WireGuard(wgClient)
	// a rotated candidate: provisioned, so its keys exist, but not applied
	cand := wgRevision(t, h, sec, func(c *model.Configuration) {
		n := (*c.Networks)[wgHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[wgClient]
		gen := 1
		cl.Key = &model.WireGuardKeySettings{Generation: &gen, PresharedKey: cl.Key.PresharedKey}
		(*wg.Clients)[wgClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[wgHub] = n
	})
	if old, err := sec.WireGuard(wgClient); err != nil || old != before {
		t.Fatalf("the keys of the active revision were touched: %+v %v", old, err)
	}
	if _, err := sec.WireGuard(wireguard.KeyID(wgClient, 1)); err != nil {
		t.Fatalf("the candidate has no keys: %v", err)
	}
	// the nftables transaction fails: the active revision is restored with its own keys
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	_, err := h.apply(cand)
	h.k.Fail = nil
	if err == nil {
		t.Fatal("must fail")
	}
	h.barrier()
	if old, err := sec.WireGuard(wgClient); err != nil || old != before {
		t.Fatalf("a failed apply lost the keys of the active revision: %+v %v", old, err)
	}
	// the candidate applies once the failure is gone, and the commit retires the old generation
	h.mustApply(cand)
	if _, err := sec.WireGuard(wgClient); err == nil {
		t.Error("the keys of the previous generation are still there after the commit")
	}
	if _, err := sec.WireGuard(wireguard.KeyID(wgClient, 1)); err != nil {
		t.Errorf("the keys of the active generation are gone: %v", err)
	}
}

func TestACandidateKeepsItsKeysWhenAnotherRevisionIsCommitted(t *testing.T) {
	h, sec := newWGHarness(t)
	h.mustApply(h.revision(nil))
	rotated := wgRevision(t, h, sec, func(c *model.Configuration) {
		n := (*c.Networks)[wgHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[wgClient]
		gen := 2
		cl.Key = &model.WireGuardKeySettings{Generation: &gen}
		(*wg.Clients)[wgClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[wgHub] = n
	})
	other := h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") })
	h.mustApply(other)
	if _, err := sec.WireGuard(wireguard.KeyID(wgClient, 2)); err != nil {
		t.Fatalf("the commit of another revision removed the keys of a candidate: %v", err)
	}
	_ = rotated
}

func TestPollingTwiceIsRefusedAndTheFirstPollAnnouncesWhatIsOnline(t *testing.T) {
	h, _ := newWGHarness(t)
	h.mustApply(h.revision(nil))
	// a peer that is online already when the polling starts is announced once
	var hubIf, peerPub string
	for _, w := range h.e.Snapshot().WireGuardInterfaces {
		if w.NetworkID == wgHub {
			hubIf, peerPub = w.Name, w.Peers[0].PublicKey
		}
	}
	h.k.Handshake(hubIf, peerPub, h.clk.Now().Unix(), 1, 1)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	if err := h.e.PollWireGuard(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.e.PollWireGuard(context.Background(), 5*time.Second); err == nil {
		t.Fatal("two pollers would duplicate every event")
	}
	h.clk.BlockUntil(1)
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.WireGuard[wgClient].Online })
	if ev := collect(ch, engine.EventPeerOnline); len(ev) != 1 {
		t.Errorf("the first poll announced %+v", ev)
	}
	// and nothing is announced for a peer that is offline from the start
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.WireGuard[wgClient].Online })
	if ev := collect(ch, engine.EventPeerOnline); len(ev) != 0 {
		t.Errorf("a second announcement: %+v", ev)
	}
}

func TestAReconnectingSubscriberGetsTheBufferedEvents(t *testing.T) {
	h, _ := newWGHarness(t)
	h.mustApply(h.revision(nil))
	all, _, cancel := h.e.SubscribeFrom(0)
	cancel()
	if len(all) == 0 {
		t.Fatal("no events are buffered")
	}
	for i, ev := range all {
		if ev.Seq != all[0].Seq+uint64(i) {
			t.Fatalf("the events are not in order: %v", all)
		}
	}
	// a client that saw the first event gets the rest, then the live stream continues without a gap
	rest, live, cancel := h.e.SubscribeFrom(all[0].Seq)
	defer cancel()
	if len(rest) != len(all)-1 {
		t.Fatalf("replayed %d of %d", len(rest), len(all)-1)
	}
	h.mustApply(h.revision(func(c *model.Configuration) {
		n := (*c.Networks)[wgHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[wgClient]
		off := false
		cl.Enabled = &off
		(*wg.Clients)[wgClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[wgHub] = n
	}))
	select {
	case ev := <-live:
		if ev.Seq != all[len(all)-1].Seq+1 {
			t.Errorf("a gap between replay and live: %d after %d", ev.Seq, all[len(all)-1].Seq)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live event")
	}
	// a sequence number from another boot replays nothing
	none, _, cancel := h.e.SubscribeFrom(1 << 40)
	cancel()
	if len(none) != 0 {
		t.Errorf("%d events replayed for a number beyond the newest", len(none))
	}
}
