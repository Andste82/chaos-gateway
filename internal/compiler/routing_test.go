package compiler

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func withRouting(t *testing.T, mod func(*model.Configuration, *Input)) *Target {
	t.Helper()
	return compileWG(t, func(cfg *model.Configuration, in *Input) {
		asn := int64(65001)
		hold, ka := "9s", "3s"
		maxp := 10
		link := linkID
		iot := "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
		cfg.Routing = &model.Routing{Asn: &asn, Protocols: &map[string]model.RoutingProtocol{
			"11111111-2222-4333-8444-555555555555": {
				Name: "site-b", Type: model.RoutingProtocolTypeBgp, Link: link,
				Bgp:      &model.BgpSettings{NeighborAsn: 65002, HoldTime: &hold, KeepaliveTime: &ka},
				Announce: &[]model.AnnounceEntry{{Network: &iot}, {Cidr: ptr("10.77.0.0/24")}},
				Import:   &model.ImportFilter{MaxPrefixes: &maxp, AllowedPrefixes: &[]model.PrefixFilterEntry{{Prefix: "10.60.0.0/22", MaxLength: ptr(24)}}},
			},
		}}
		if mod != nil {
			mod(cfg, in)
		}
	})
}

func TestGoldenRouting(t *testing.T) {
	tg := withRouting(t, nil)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	if tg.Bird == nil {
		t.Fatal("no BIRD configuration")
	}
	golden(t, "routing", tg)
	if err := birdParsesFile(t, tg.Bird.Text); err != nil {
		t.Fatalf("bird rejects it: %v\n%s", err, tg.Bird.Text)
	}
}

func TestTheBirdConfigurationFollowsTheModel(t *testing.T) {
	tg := withRouting(t, nil)
	c := tg.Bird.Config
	if c.RouterID != "10.10.0.1" || c.ASN != 65001 || c.KernelTable != PolicyTable {
		t.Errorf("router id is the lowest gateway address of the local networks: %+v", c)
	}
	if len(c.Protocols) != 1 {
		t.Fatalf("%+v", c.Protocols)
	}
	p := c.Protocols[0]
	if p.Name != "bgp_site_b" || p.Interface != "wg-site-b" || p.LocalAddress != "10.255.0.0" || p.NeighborAddress != "10.255.0.1" {
		t.Errorf("%+v", p)
	}
	if p.BGP.NeighborASN != 65002 || p.BGP.HoldTime != 9 || p.BGP.KeepaliveTime != 3 {
		t.Errorf("%+v", p.BGP)
	}
	if strings.Join(p.Announce, ",") != "10.10.0.0/24,10.77.0.0/24" {
		t.Errorf("announce %v: a network announces its subnet, a prefix itself", p.Announce)
	}
	if p.Import.MaxPrefixes != 10 || len(p.Import.Allowed) != 1 || p.Import.Allowed[0].MaxLength != 24 {
		t.Errorf("%+v", p.Import)
	}
	// the protected prefixes: management, uplink, local networks, tunnel subnets and the networks the
	// executor routes itself (behind clients and links)
	want := "10.10.0.0/24,10.255.0.0/31,10.50.0.0/24,10.60.0.0/24,10.98.0.0/24,10.99.0.0/24,192.168.56.0/24,203.0.113.0/24"
	if got := strings.Join(c.Protected, ","); got != want {
		t.Errorf("protected %s, want %s", got, want)
	}
}

func TestTheDefaultsOfTheProtocols(t *testing.T) {
	tg := withRouting(t, func(cfg *model.Configuration, in *Input) {
		for id, p := range *cfg.Routing.Protocols {
			p.Type = model.RoutingProtocolTypeOspf
			p.Bgp, p.Announce, p.Import = nil, nil, nil
			(*cfg.Routing.Protocols)[id] = p
		}
	})
	o := tg.Bird.Config.Protocols[0].OSPF
	if o.Area != "0" || o.HelloInterval != 10 || o.DeadInterval != 40 {
		t.Errorf("%+v", o)
	}
	rid := "10.255.0.0"
	tg = withRouting(t, func(cfg *model.Configuration, in *Input) { cfg.Routing.RouterId = &rid })
	if tg.Bird.Config.RouterID != rid {
		t.Errorf("the configured router id: %s", tg.Bird.Config.RouterID)
	}
	tg = withRouting(t, func(cfg *model.Configuration, in *Input) {
		for id, p := range *cfg.Routing.Protocols {
			p.Bgp.HoldTime, p.Bgp.KeepaliveTime = nil, nil
			(*cfg.Routing.Protocols)[id] = p
		}
	})
	if b := tg.Bird.Config.Protocols[0].BGP; b.HoldTime != 90 || b.KeepaliveTime != 30 {
		t.Errorf("%+v", b)
	}
}

func TestADisabledProtocolOrAMissingLinkIsNotStarted(t *testing.T) {
	off := false
	tg := withRouting(t, func(cfg *model.Configuration, in *Input) {
		for id, p := range *cfg.Routing.Protocols {
			p.Enabled = &off
			(*cfg.Routing.Protocols)[id] = p
		}
	})
	if tg.Bird != nil {
		t.Error("nothing runs: no configuration")
	}
	tg = withRouting(t, func(cfg *model.Configuration, in *Input) {
		n := (*cfg.Networks)[linkID]
		wg, _ := n.AsWireGuardNetwork()
		wg.Peer.Enabled = &off // the link is not available... the interface still exists
		_ = n.FromWireGuardNetwork(wg)
		(*cfg.Networks)[linkID] = n
		for id, p := range *cfg.Routing.Protocols {
			p.Link = hubID // a hub is not a link
			(*cfg.Routing.Protocols)[id] = p
		}
	})
	var warned bool
	for _, p := range tg.Problems {
		warned = warned || p.Code == CodeRouting && p.Severity == SevWarning
	}
	if tg.Bird != nil || !warned {
		t.Errorf("a protocol on something that is not a link is reported and not started: %+v", tg.Problems)
	}
}

func TestExternalRouting(t *testing.T) {
	tg := withRouting(t, func(cfg *model.Configuration, in *Input) {
		on, table := true, 200
		cfg.Routing.External = &model.ExternalRouting{Enabled: &on, Table: &table, Import: &model.ImportFilter{MaxPrefixes: ptr(5)}}
	})
	c := tg.Bird.Config
	if c.External == nil || c.External.Table != 200 || c.External.Import.MaxPrefixes != 5 || len(tg.Bird.ImportTables) != 1 || tg.Bird.ImportTables[0] != 200 {
		t.Fatalf("%+v %v", c.External, tg.Bird.ImportTables)
	}
	if err := birdParsesFile(t, tg.Bird.Text); err != nil {
		t.Error(err)
	}
	// external routing alone, without a protocol
	tg = withRouting(t, func(cfg *model.Configuration, in *Input) {
		on, table := true, 200
		cfg.Routing.Protocols = nil
		cfg.Routing.External = &model.ExternalRouting{Enabled: &on, Table: &table}
	})
	if tg.Bird == nil || len(tg.Bird.Config.Protocols) != 0 || tg.Bird.Config.External == nil {
		t.Errorf("%+v", tg.Bird)
	}
}

func TestRoutingProtocolsMayEnterTheGatewayFromTheirLinkOnly(t *testing.T) {
	tg := withRouting(t, nil)
	var found int
	for _, r := range chainOf(t, tg, "input").Rules {
		s := js(r.Expr)
		if strings.Contains(s, `"right":179`) {
			found++
			if !strings.Contains(s, `"wg-site-b"`) || !strings.Contains(s, `"tcp"`) || !strings.Contains(s, "accept") {
				t.Errorf("%s", s)
			}
		}
	}
	if found != 1 {
		t.Errorf("%d BGP accept rules", found)
	}
	// without routing there is no such rule
	for _, r := range chainOf(t, compileWG(t, nil), "input").Rules {
		if strings.Contains(js(r.Expr), `"right":179`) {
			t.Error("BGP is open without a protocol")
		}
	}
	// OSPF and Babel
	tg = withRouting(t, func(cfg *model.Configuration, in *Input) {
		id := "22222222-3333-4444-8555-666666666666"
		id2 := "33333333-4444-4555-8666-777777777777"
		(*cfg.Routing.Protocols)[id] = model.RoutingProtocol{Name: "o", Type: model.RoutingProtocolTypeOspf, Link: linkID}
		(*cfg.Routing.Protocols)[id2] = model.RoutingProtocol{Name: "b", Type: model.RoutingProtocolTypeBabel, Link: linkID}
	})
	var ospf, babel bool
	for _, r := range chainOf(t, tg, "input").Rules {
		s := js(r.Expr)
		ospf = ospf || strings.Contains(s, `"l4proto"`) && strings.Contains(s, `"right":89`)
		babel = babel || strings.Contains(s, `"right":6696`)
	}
	if !ospf || !babel {
		t.Errorf("ospf %v babel %v", ospf, babel)
	}
}

func TestACustomSnippetThatCannotBeRenderedIsAProblem(t *testing.T) {
	tg := withRouting(t, func(cfg *model.Configuration, in *Input) {
		for id, p := range *cfg.Routing.Protocols {
			s := `include "/etc/passwd";`
			p.CustomSnippet = &s
			(*cfg.Routing.Protocols)[id] = p
		}
	})
	if !tg.HasErrors() || !strings.Contains(tg.Errors()[0].Message, "include") {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestTheBirdTextIsPartOfTheHash(t *testing.T) {
	a := withRouting(t, nil)
	b := withRouting(t, func(cfg *model.Configuration, in *Input) {
		for id, p := range *cfg.Routing.Protocols {
			p.Announce = &[]model.AnnounceEntry{{Cidr: ptr("10.88.0.0/24")}}
			(*cfg.Routing.Protocols)[id] = p
		}
	})
	if a.Hash == b.Hash {
		t.Error("another announcement, the same hash")
	}
	if compileWG(t, nil).Bird != nil {
		t.Error("BIRD without a routing section")
	}
}
