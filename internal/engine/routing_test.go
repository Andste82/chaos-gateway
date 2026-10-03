package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

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
		if pr.Code == "routing" && pr.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", p.Problems)
	}
}
