//go:build testbed

package engine_test

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/bird"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

const birdProtoID = "11111111-2222-4333-8444-555555555555"

// routingMod switches the link to BGP: the static route of the fixture goes (BIRD learns the
// remote network instead), the import filter allows the remote site's /22 up to /24 with the given
// limit.
func routingMod(maxPrefixes int, snippet string) func(*model.Configuration) {
	return func(c *model.Configuration) {
		n := (*c.Networks)[tLink]
		wg, _ := n.AsWireGuardNetwork()
		wg.Routes = nil
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[tLink] = n
		asn := int64(65001)
		hold, ka := "9s", "3s"
		iot := tIoT
		max24 := 24
		p := model.RoutingProtocol{
			Name: "site-b", Type: model.RoutingProtocolTypeBgp, Link: tLink,
			Bgp:      &model.BgpSettings{NeighborAsn: 65002, HoldTime: &hold, KeepaliveTime: &ka},
			Announce: &[]model.AnnounceEntry{{Network: &iot}},
			Import:   &model.ImportFilter{MaxPrefixes: &maxPrefixes, AllowedPrefixes: &[]model.PrefixFilterEntry{{Prefix: "10.60.0.0/22", MaxLength: &max24}}},
		}
		if snippet != "" {
			p.CustomSnippet = &snippet
		}
		c.Routing = &model.Routing{Asn: &asn, Protocols: &map[string]model.RoutingProtocol{birdProtoID: p}}
	}
}

// startBird starts a BIRD in a namespace with the given configuration and returns the control socket.
func startBird(t *testing.T, ns *testbed.Namespace, dir, name, conf string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cf, sock := filepath.Join(dir, name+".conf"), filepath.Join(dir, name+".ctl")
	if err := os.WriteFile(cf, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	p := ns.Start("bird", "-f", "-c", cf, "-s", sock)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			return sock
		}
		select {
		case <-p.Done():
			t.Fatalf("bird exited: %s", p.Output())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("bird has no control socket: %s", p.Output())
	return ""
}

func birdc(t *testing.T, sock string, args ...string) string {
	t.Helper()
	b, err := osexec.Command("birdc", append([]string{"-s", sock}, args...)...).CombinedOutput()
	out := string(b)
	if err != nil {
		t.Fatalf("birdc %v: %v\n%s", args, err, out)
	}
	return out
}

// bgpGW is the testbed with the BGP link: BIRD in the gateway's namespace (the executor writes its
// configuration and reconfigures it) and BIRD at the remote site with the configuration the product
// exports for it.
type bgpGW struct {
	*wgGW
	gwSock, siteSock string
	siteConf         string
}

func newBGP(t *testing.T, maxPrefixes int, siteRoutes ...string) *bgpGW {
	t.Helper()
	return newBGPWith(t, routingMod(maxPrefixes, ""), siteRoutes...)
}

func newBGPWith(t *testing.T, mod func(*model.Configuration), siteRoutes ...string) *bgpGW {
	t.Helper()
	g := &bgpGW{wgGW: newWGGW(t)}
	// the instance the executor manages starts with an idle configuration
	g.gwSock = startBird(t, g.top.GW, g.bird, compiler.BirdInstance, "router id 127.0.0.1;\nprotocol device { }\n")
	g.apply(mod)

	g.siteSock = g.remoteSite(g.top.Site, tLink, "wgsite", "site", siteRoutes...)
	return g
}

// remoteSite brings up the remote side of a link in a namespace: its WireGuard tunnel from the export
// and BIRD with the configuration `chaosgw wg export --bird` renders, announcing the given routes.
// It returns the control socket.
func (g *bgpGW) remoteSite(ns *testbed.Namespace, linkID, iface, name string, routes ...string) string {
	g.t.Helper()
	remote, err := wireguard.LinkRemoteConfig(g.export(), linkID)
	if err != nil {
		g.t.Fatal(err)
	}
	g.up(ns, iface, remote.Conf)
	tg := compiler.Compile(compiler.Input{Config: g.activeConfig(), Generation: compiler.Generation{Seq: 1}, Keys: g.publicKeys()})
	var proto *bird.Protocol
	if tg.Bird != nil {
		for _, w := range tg.WireGuard {
			if w.NetworkID != linkID {
				continue
			}
			for i, p := range tg.Bird.Config.Protocols {
				if p.Interface == w.Name {
					proto = &tg.Bird.Config.Protocols[i]
				}
			}
		}
	}
	if proto == nil {
		g.t.Fatalf("no routing protocol on the link %s: %+v", linkID, tg.Problems)
	}
	conf, err := bird.RenderRemote(tg.Bird.Config, *proto, iface)
	if err != nil {
		g.t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range routes {
		b.WriteString("  route " + r + " unreachable;\n")
	}
	g.siteConf = strings.Replace(conf, "  # route 192.0.2.0/24 unreachable;\n", b.String(), 1)
	return startBird(g.t, ns, filepath.Join(g.dir, name), name, g.siteConf)
}

func (g *bgpGW) activeConfig() *model.Configuration {
	g.t.Helper()
	_, cfg, err := g.st.Active()
	if err != nil {
		g.t.Fatal(err)
	}
	return cfg
}

func (g *bgpGW) publicKeys() map[string]string {
	g.t.Helper()
	k, err := wireguard.InterfaceKeys(g.activeConfig(), g.sec)
	if err != nil {
		g.t.Fatal(err)
	}
	return k
}

func (g *bgpGW) establishedNow() bool {
	ps, err := bird.ParseProtocols(birdc(g.t, g.gwSock, "show", "protocols", "all"))
	if err != nil {
		return false
	}
	for _, p := range ps {
		if p.Proto == "BGP" && p.Established() {
			return true
		}
	}
	return false
}

func (g *bgpGW) established(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if g.establishedNow() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
}

func (g *bgpGW) table100() string { return g.top.GW.Must("ip", "route", "show", "table", "100") }

func (g *bgpGW) waitRoute(want string, present bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(g.table100(), want) == present {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// M4c test: a BGP session over the WireGuard link comes up, the gateway learns the remote site's
// networks into table 100 and announces its own, and traffic flows both ways.
func TestBGPOverAWireGuardLinkExchangesRoutes(t *testing.T) {
	g := newBGP(t, 10, "10.60.0.0/24")
	ch, cancel := g.e.Subscribe()
	defer cancel()
	if err := g.e.PollRouting(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if !g.established(90 * time.Second) {
		t.Fatalf("the BGP session did not come up\n%s\n%s", birdc(t, g.gwSock, "show", "protocols", "all"), birdc(t, g.siteSock, "show", "protocols", "all"))
	}
	if _, ok := waitRoutingState(ch, "up", 30*time.Second); !ok {
		t.Error("no routing_session_changed event (up)")
	}
	if !g.waitRoute("10.60.0.0/24 via 10.255.0.1 dev wg-site-b", true, 30*time.Second) {
		t.Fatalf("the learned route is not in table 100\n%s", g.table100())
	}
	// the gateway's network is announced to the remote side
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(g.top.Site.Must("ip", "route", "show"), "10.10.0.0/24") && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
	if !pingOK(g.top.A, "", testbed.SiteNetHost) {
		t.Errorf("A cannot reach the remote site's network\n%s", g.table100())
	}
	// (the other direction is the access matrix's business: nothing allows the site to reach A)
	// the snapshot shows the session
	if st := g.e.Snapshot().Routing; len(st) == 0 {
		t.Errorf("%+v", st)
	}
}

// M4c test: when the link goes down the session ends and the learned routes leave table 100; when it
// comes back the session is re-established.
func TestALinkOutageEndsTheSessionAndTheLearnedRoutesLeave(t *testing.T) {
	g := newBGP(t, 10, "10.60.0.0/24")
	ch, cancel := g.e.Subscribe()
	defer cancel()
	if err := g.e.PollRouting(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if !g.established(90*time.Second) || !g.waitRoute("10.60.0.0/24", true, 30*time.Second) {
		t.Fatalf("no session\n%s", g.table100())
	}
	g.top.Site.Must("ip", "link", "set", "wgsite", "down")
	// the hold time of the fixture is 9 s: the session ends within it, plus the poll interval
	if _, ok := waitRoutingState(ch, "down", 30*time.Second); !ok {
		t.Fatalf("no routing_session_changed event (down)\n%s", birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	if !g.waitRoute("10.60.0.0/24", false, 30*time.Second) {
		t.Errorf("the learned route stays in table 100\n%s", g.table100())
	}
	g.top.Site.Must("ip", "link", "set", "wgsite", "up")
	if !g.established(120 * time.Second) {
		t.Fatalf("the session does not come back\n%s", birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	if !g.waitRoute("10.60.0.0/24", true, 30*time.Second) {
		t.Errorf("the route does not come back\n%s", g.table100())
	}
}

// M4c test: a prefix limit that is exceeded takes the session down instead of flooding table 100.
// The same remote routes under a limit of 10 come up in the other tests, so a session that stays
// down here is the limit's doing; a disabled protocol stays disabled.
func TestMoreRoutesThanTheLimitDisableTheSession(t *testing.T) {
	g := newBGP(t, 2, "10.60.0.0/24", "10.60.1.0/24", "10.60.2.0/24", "10.60.3.0/24")
	time.Sleep(45 * time.Second)
	if g.establishedNow() || strings.Contains(g.table100(), "10.60.") {
		t.Fatalf("the limit of 2 did not take the session down\n%s\n%s", birdc(t, g.gwSock, "show", "protocols", "all"), g.table100())
	}
}

// M4c test: without an allowed list the filters of the product alone keep a default route, the
// management prefix and the gateway's own prefix out.
func TestProtectedPrefixesAndTheDefaultRouteAreFilteredWithoutAnAllowedList(t *testing.T) {
	g := newBGPWith(t, func(c *model.Configuration) {
		routingMod(10, "")(c)
		p := (*c.Routing.Protocols)[birdProtoID]
		p.Import = nil
		(*c.Routing.Protocols)[birdProtoID] = p
	}, "10.60.0.0/24", "0.0.0.0/0", "192.168.56.0/24", "10.10.0.0/24", "10.50.0.0/25")
	if !g.established(90*time.Second) || !g.waitRoute("10.60.0.0/24 via 10.255.0.1", true, 30*time.Second) {
		t.Fatalf("no session or no route\n%s", g.table100())
	}
	time.Sleep(5 * time.Second)
	for _, bad := range []string{"default via 10.255.", "192.168.56.0/24 via", "10.10.0.0/24 via", "10.50.0.0/25 via"} {
		if strings.Contains(g.table100(), bad) {
			t.Errorf("the filter let %q through\n%s", bad, g.table100())
		}
	}
}

// M4c test: changing the configuration reconfigures BIRD without ending the session, and an invalid
// custom snippet is rejected by BIRD's own parser in the preview, leaving the running state alone.
func TestAConfigurationChangeKeepsTheSessionAndAnInvalidSnippetIsRefused(t *testing.T) {
	g := newBGP(t, 10, "10.60.0.0/24")
	if !g.established(90 * time.Second) {
		t.Fatal("no session")
	}
	since := func() string {
		ps, _ := bird.ParseProtocols(birdc(t, g.gwSock, "show", "protocols", "all"))
		for _, p := range ps {
			if p.Proto == "BGP" {
				return p.Since
			}
		}
		return ""
	}
	before := since()
	// a different timer in the announce list: BIRD reconfigures, the session stays
	g.apply(func(c *model.Configuration) {
		routingMod(10, "")(c)
		p := (*c.Routing.Protocols)[birdProtoID]
		p.Announce = &[]model.AnnounceEntry{{Cidr: ptrS("10.77.0.0/24")}}
		(*c.Routing.Protocols)[birdProtoID] = p
	})
	if got := since(); got != before {
		t.Errorf("the session restarted: since %s, was %s", got, before)
	}
	if !g.established(30 * time.Second) {
		t.Error("the session is gone after the change")
	}

	rev := g.revision(routingMod(10, "this is not valid bird"))
	p, err := g.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	var msg string
	for _, pr := range p.Problems {
		if pr.Code == compiler.CodeRouting && pr.Severity == compiler.SevError {
			msg = pr.Message
		}
	}
	if msg == "" {
		t.Fatalf("the preview does not show BIRD's message: %+v", p.Problems)
	}
	if got := since(); got != before {
		t.Errorf("the preview changed the running instance: %s", got)
	}
}

func ptrS(s string) *string { return &s }

const siteCLink = `{"type":"wireguard","kind":"link","name":"site-c","address":"10.255.1.0/31","listen_port":51823,"mtu":1380,
"peer":{"address":"10.255.1.1","endpoint":"203.0.113.50:51821","keepalive":"1s","key":{"mode":"generated"}}}`

// M4c test: three sites. The gateway runs BGP towards one remote site and OSPF towards another, each
// with BIRD in a namespace of its own over its own WireGuard link. Learned routes appear in table
// 100 and never in the main table; a neighbor that announces a default route or the management
// prefix is filtered, and the gateway's own default route stays.
func TestThreeSitesWithBGPAndOSPFLearnRoutesOnlyIntoTheOwnTable(t *testing.T) {
	g := &bgpGW{wgGW: newWGGW(t)}
	g.gwSock = startBird(t, g.top.GW, g.bird, compiler.BirdInstance, "router id 127.0.0.1;\nprotocol device { }\n")
	const ospfID = "22222222-3333-4444-8555-666666666666"
	const linkC = "5c6d7e8f-9a0b-4c1d-8e2f-3a4b5c6d7e8f"
	g.apply(func(c *model.Configuration) {
		routingMod(10, "")(c)
		var n model.Network
		if err := n.UnmarshalJSON([]byte(siteCLink)); err != nil {
			t.Fatal(err)
		}
		(*c.Networks)[linkC] = n
		iot, site := tIoT, linkC
		entries := append(*c.AccessMatrix.Entries, model.MatrixEntry{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Network: &site}, Policy: model.MatrixEntryPolicyAllow})
		c.AccessMatrix.Entries = &entries
		(*c.Routing.Protocols)[ospfID] = model.RoutingProtocol{Name: "site-c", Type: model.RoutingProtocolTypeOspf, Link: linkC, Ospf: &model.OspfSettings{}, Announce: &[]model.AnnounceEntry{{Network: &iot}}}
	})
	// the BGP neighbor announces a default route, the management prefix, a prefix of the gateway
	// and the remote network
	g.remoteSite(g.top.Site, tLink, "wgsite", "site", "10.60.0.0/24", "0.0.0.0/0", "192.168.56.0/24")
	siteC := g.remoteSite(g.top.Site2, linkC, "wgsite2", "site2", "10.70.0.0/24")
	if !g.established(90 * time.Second) {
		t.Fatalf("no BGP session\n%s", birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	if !g.waitRoute("10.60.0.0/24 via 10.255.0.1 dev wg-site-b", true, 30*time.Second) {
		t.Fatalf("the BGP route is not in table 100\n%s", g.table100())
	}
	if !g.waitRoute("10.70.0.0/24 via 10.255.1.1 dev wg-site-c", true, 90*time.Second) {
		t.Fatalf("the OSPF route is not in table 100\n%s\n%s\n%s", g.table100(), birdc(t, g.gwSock, "show", "protocols", "all"), birdc(t, siteC, "show", "protocols", "all"))
	}
	for _, bad := range []string{"default via 10.255.", "192.168.56.0/24 via"} {
		if strings.Contains(g.table100(), bad) {
			t.Errorf("the filter let %q through\n%s", bad, g.table100())
		}
	}
	main := g.top.GW.Must("ip", "route", "show", "table", "main")
	for _, learned := range []string{"10.60.0.0/24", "10.70.0.0/24"} {
		if strings.Contains(main, learned) {
			t.Errorf("%s was learned into the main table\n%s", learned, main)
		}
	}
	if !strings.Contains(main, "default via 192.168.56.254") {
		t.Errorf("the management default route is gone\n%s", main)
	}
	// the neighbors learn the gateway's network (the return path); give them a moment
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(g.top.Site2.Must("ip", "route", "show"), "10.10.0.0/24") && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
	if !strings.Contains(g.top.Site2.Must("ip", "route", "show"), "10.10.0.0/24") {
		t.Errorf("the OSPF neighbor does not learn the gateway's network\n%s\n%s\n%s\n%s", birdc(t, g.gwSock, "show", "route", "export", "ospf_site_c"), birdc(t, g.gwSock, "show", "protocols", "all", "ospf_site_c"), birdc(t, g.gwSock, "show", "ospf", "state", "ospf_site_c"), g.top.GW.Must("cat", filepath.Join(g.bird, compiler.BirdInstance+".conf")))
		// the rest of the test needs the return path
		g.top.Site2.Must("ip", "route", "add", "10.10.0.0/24", "via", "10.255.1.0", "dev", "wgsite2")
	}
	for _, r := range []struct {
		ns   *testbed.Namespace
		host string
	}{{g.top.Site, testbed.SiteNetHost}, {g.top.Site2, testbed.Site2NetHost}} {
		if !pingOK(g.top.A, "", r.host) {
			t.Errorf("A cannot reach %s\n%s\nremote routes:\n%s", r.host, g.table100(), r.ns.Must("ip", "route", "show"))
		}
	}
	// taking the OSPF link down withdraws its routes within the dead interval
	g.top.Site2.Must("ip", "link", "set", "wgsite2", "down")
	if !g.waitRoute("10.70.0.0/24", false, 60*time.Second) {
		t.Errorf("the OSPF route stays after the link went down\n%s", g.table100())
	}
	g.top.Site2.Must("ip", "link", "set", "wgsite2", "up")
	if !g.waitRoute("10.70.0.0/24 via 10.255.1.1", true, 120*time.Second) {
		t.Errorf("the OSPF route does not return\n%s", g.table100())
	}
}

// M4c-01 test: external mode reads routes another daemon writes into its own kernel table (simulated
// here with a plain `ip route add ... table 200`, since BIRD cannot tell the difference) and exports
// only the ones that pass the import filter into table 100; the protected prefix and the default
// route stay out.
func TestExternalModeImportsAnotherDaemonsTable(t *testing.T) {
	g := &bgpGW{wgGW: newWGGW(t)}
	g.gwSock = startBird(t, g.top.GW, g.bird, compiler.BirdInstance, "router id 127.0.0.1;\nprotocol device { }\n")
	enabled, table := true, 200
	g.apply(func(c *model.Configuration) {
		c.Routing = &model.Routing{External: &model.ExternalRouting{Enabled: &enabled, Table: &table}}
	})
	// another daemon's routes, injected directly into table 200
	g.top.GW.Must("ip", "route", "add", "10.80.0.0/24", "dev", "wg-site-b", "table", "200", "proto", "static")
	g.top.GW.Must("ip", "route", "add", "default", "dev", "wg-site-b", "table", "200")
	g.top.GW.Must("ip", "route", "add", "10.10.0.0/24", "dev", "wg-site-b", "table", "200")
	if !g.waitRoute("10.80.0.0/24", true, 30*time.Second) {
		t.Fatalf("the external route is not in table 100\n%s\n%s", g.table100(), birdc(t, g.gwSock, "show", "protocols", "all"))
	}
	table100 := g.table100()
	for _, bad := range []string{"default dev wg-site-b", "10.10.0.0/24 dev wg-site-b"} {
		if strings.Contains(table100, bad) {
			t.Errorf("the filter let %q through\n%s", bad, table100)
		}
	}
}

// waitRoutingState waits for a routing_session_changed event with the given state.
func waitRoutingState(ch <-chan engine.Event, state string, d time.Duration) (engine.Event, bool) {
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return engine.Event{}, false
			}
			if ev.Type == engine.EventRoutingChanged && ev.Data["state"] == state {
				return ev, true
			}
		case <-deadline:
			return engine.Event{}, false
		}
	}
}
