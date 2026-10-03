package engine_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

const (
	wgHub    = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	wgClient = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

// newWGHarness is the harness with the WireGuard testbed configuration: keys in a secrets store the
// executor reads, the configuration provisioned.
func newWGHarness(t *testing.T) (*harness, *secrets.Store) {
	t.Helper()
	h := newHarness(t)
	sec, err := secrets.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex, err := executor.New(h.k, executor.WithKeys(func(id string) (string, string, error) {
		k, err := sec.WireGuard(id)
		return k.PrivateKey, k.PresharedKey, err
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	h.ex = ex
	raw, err := os.ReadFile("../compiler/testdata/wireguard.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg, errs := domain.Normalize(cfg)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if cfg, err = wireguard.Provision(cfg, sec); err != nil {
		t.Fatal(err)
	}
	h.base = cfg
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: ex}, Clock: h.clk, Secrets: sec})
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
