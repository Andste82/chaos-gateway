package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const bgpTable = "BIRD 2.18 ready.\nName       Proto      Table      State  Since         Info\ndevice1    Device     ---        up     08:02:00.021  \nbgp_site_b BGP        ---        %s\n  BGP state:          Down\n    Neighbor address: 10.255.0.1\n"

func withBGP(c *model.Configuration) {
	asn := int64(65001)
	c.Routing = &model.Routing{Asn: &asn, Protocols: &map[string]model.RoutingProtocol{
		"11111111-2222-4333-8444-555555555555": {
			Name: "site-b", Type: model.RoutingProtocolTypeBgp, Link: "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f",
			Bgp: &model.BgpSettings{NeighborAsn: 65002},
		},
	}}
}

func TestRoutingSessionsAreReportedWithEvents(t *testing.T) {
	h, _ := newWGHarness(t)
	h.mustApply(h.revision(withBGP))
	s := h.e.Snapshot()
	if s.Bird == nil || s.Bird.Instance != "chaosgw" {
		t.Fatalf("%+v", s.Bird)
	}
	if err := h.e.PollRouting(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()

	h.k.SetBirdProtocols(strings.Replace(bgpTable, "%s", "start  08:02:00.021  Connect", 1))
	h.clk.BlockUntil(1)
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return len(s.Routing) == 1 })
	if got := events(ch); has(got, engine.EventRoutingChanged) {
		t.Fatalf("a session that never was up is not announced: %v", got)
	}

	h.k.SetBirdProtocols(strings.Replace(bgpTable, "%s", "up     08:02:00.021  Established", 1))
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.Routing["bgp_site_b"].Established() })
	if ev := routingEvents(ch, "up"); len(ev) != 1 || ev[0].Data["protocol"] != "bgp_site_b" {
		t.Fatalf("%+v", ev)
	}

	h.k.SetBirdProtocols(strings.Replace(bgpTable, "%s", "start  08:02:00.021  Active", 1))
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return !s.Routing["bgp_site_b"].Established() })
	if ev := routingEvents(ch, "down"); len(ev) != 1 {
		t.Fatalf("%+v", ev)
	}
}

// M4c-06 test: the preview shows the BIRD configuration it would write, and nothing once it is
// applied.
func TestThePreviewShowsTheBirdDiff(t *testing.T) {
	h, _ := newWGHarness(t)
	rev := h.revision(withBGP)
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+protocol bgp bgp_site_b", "+router id"} {
		if !strings.Contains(p.Linux.Bird, want) {
			t.Errorf("the bird diff lacks %q:\n%s", want, p.Linux.Bird)
		}
	}
	h.mustApply(rev)
	p2, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Linux.Bird != "" {
		t.Errorf("a diff after the apply:\n%s", p2.Linux.Bird)
	}
}

// M4c-03 test: a change in a protocol's route counts is an event of its own, and a poll that
// changes nothing emits no event and does not republish the snapshot.
func TestRouteCountChangesAreEvents(t *testing.T) {
	h, _ := newWGHarness(t)
	h.mustApply(h.revision(withBGP))
	if err := h.e.PollRouting(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()

	table := func(imported, exported int) string {
		return fmt.Sprintf("BIRD 2.18 ready.\nName       Proto      Table      State  Since         Info\ndevice1    Device     ---        up     08:02:00.021  \nbgp_site_b BGP        ---        up     08:02:00.021  Established\n  Channel ipv4\n    State:          UP\n    Routes:         %d imported, 0 filtered, %d exported, %d preferred\n", imported, exported, imported+exported)
	}

	h.k.SetBirdProtocols(table(2, 1))
	h.clk.BlockUntil(1)
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.Routing["bgp_site_b"].Established() })
	collect(ch, engine.EventRoutingChanged) // the session-up event: not under test here

	h.k.SetBirdProtocols(table(3, 1))
	h.clk.Advance(5 * time.Second)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return s.Routing["bgp_site_b"].Imported == 3 })
	ev := collect(ch, engine.EventRoutingRoutesChanged)
	if len(ev) != 1 {
		t.Fatalf("%+v", ev)
	}
	d := ev[0].Data
	if d["protocol"] != "bgp_site_b" || d["imported"] != 3 || d["exported"] != 1 || d["previous_imported"] != 2 || d["previous_exported"] != 1 {
		t.Fatalf("%+v", d)
	}

	// the same counts again: no new event, and nothing is republished
	h.k.SetBirdProtocols(table(3, 1))
	h.clk.Advance(5 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if ev := collect(ch, engine.EventRoutingRoutesChanged); len(ev) != 0 {
		t.Fatalf("an unchanged poll must not emit an event: %+v", ev)
	}
}

// M4c-15 test: a configuration that needs BIRD, but an executor without --bird-dir, gets a routing
// problem from the preview instead of a hard error.
func TestPreviewReportsRoutingWhenTheExecutorHasNoBirdDirectory(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(nil))
	enabled, table := true, 200
	rev := h.revision(func(c *model.Configuration) {
		c.Routing = &model.Routing{External: &model.ExternalRouting{Enabled: &enabled, Table: &table}}
	})
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pr := range p.Problems {
		if pr.Code == compiler.CodeRouting && pr.Severity == compiler.SevError && strings.Contains(pr.Message, "--bird-dir") {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", p.Problems)
	}
}

// M4c-14 test: a routing change bundled with a lockout-relevant one is rolled back together with
// it when nobody confirms in time: the BIRD configuration reverts along with nftables.
func TestARoutingChangeIsRolledBackTogetherWithALockoutRelevantOne(t *testing.T) {
	h, _ := newWGHarness(t)
	r1 := h.mustApply(h.revision(nil)).Revision

	r2 := h.revision(func(c *model.Configuration) {
		lockoutChange(c)
		withBGP(c)
	})
	a := h.mustApply(r2, engine.ApplyOptions{ConfirmTimeout: 30 * time.Second})
	if a.Status != "pending_confirm" {
		t.Fatalf("%+v", a)
	}
	birdText := func() string {
		st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{BirdInstance: compiler.BirdInstance})
		if err != nil {
			t.Fatal(err)
		}
		if st.Bird == nil {
			return ""
		}
		return st.Bird.Config
	}
	if !strings.Contains(birdText(), "protocol bgp bgp_site_b") {
		t.Fatalf("the new routing configuration is not running yet:\n%s", birdText())
	}

	h.clk.Advance(31 * time.Second)
	s := h.barrier()
	for i := 0; i < 100 && s.Pending != nil; i++ {
		time.Sleep(10 * time.Millisecond)
		s = h.barrier()
	}
	if s.Pending != nil || s.Revision != r1 {
		t.Fatalf("after the timeout: %+v", s)
	}
	if strings.Contains(birdText(), "bgp_site_b") {
		t.Errorf("the routing change was not rolled back along with the lockout-relevant one:\n%s", birdText())
	}
}

func TestPreviewShowsBirdsOwnMessageForAnInvalidConfiguration(t *testing.T) {
	h, _ := newWGHarness(t)
	rev := h.revision(func(c *model.Configuration) {
		withBGP(c)
		bad := "syntax error"
		for id, p := range *c.Routing.Protocols {
			p.CustomSnippet = &bad
			(*c.Routing.Protocols)[id] = p
		}
	})
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pr := range p.Problems {
		if pr.Code == "routing" && pr.Severity == "error" && strings.Contains(pr.Message, "syntax error") {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", p.Problems)
	}
}
