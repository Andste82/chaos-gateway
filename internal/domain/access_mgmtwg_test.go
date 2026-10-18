package domain

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// P31-AUDIT-11 (decided 2026-10-18, option (b)): a WireGuard network of the role management, with its clients
// and the networks behind them, is part of the management network for the guard in both topologies, as the
// compiler drops it (`oifname <its interface>`). An allow rule for everything does not open it for a device
// under test; an explicit matrix entry towards the network, towards its client or towards the management network
// does, by the destination; a test client's `reachable` list naming it does not (it is no explicit entry).
func TestAManagementRoleWireGuardNetworkIsPartOfTheManagementNetworkForTheGuard(t *testing.T) {
	const idAdmin, idAdm = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d", "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"
	everything := "a1000000-0000-4000-8000-000000000001"
	a := netip.MustParseAddr
	base := func(c *model.Configuration) {
		role := model.WireGuardNetworkRoleManagement
		adm := map[string]model.WireGuardClient{idAdm: {Name: "adm", Address: "10.98.0.2", ClientNetworks: &[]string{"10.97.0.0/24"}}}
		var n model.Network
		_ = n.FromWireGuardNetwork(model.WireGuardNetwork{Type: model.WireGuardNetworkTypeWireguard, Kind: model.Hub, Name: "admin", Role: &role,
			Address: "10.98.0.1/24", ListenPort: 51822, Clients: &adm})
		(*c.Networks)[idAdmin] = n
		// lab-rA, a client of the test hub, names the admin network in its reachable list
		hubNet := (*c.Networks)[idHub]
		hub, _ := hubNet.AsWireGuardNetwork()
		cl := (*hub.Clients)[idClient]
		admin := idAdmin
		cl.Reachable = &[]model.MatrixEndpoint{{Network: &admin}}
		(*hub.Clients)[idClient] = cl
		_ = hubNet.FromWireGuardNetwork(hub)
		(*c.Networks)[idHub] = hubNet
		name, global := "everything", model.ScopeGlobal(true)
		rules := map[string]model.AccessRule{everything: {Name: &name, Source: model.Scope{Global: &global}, Action: model.AccessActionAllow}}
		c.AccessRules, c.AccessRuleOrder = &rules, &[]uuid.UUID{uuid.MustParse(everything)}
	}
	withEntry := func(to model.MatrixEndpoint) func(*model.Configuration) {
		return func(c *model.Configuration) {
			base(c)
			iot := idIoT
			entries := append(deref(c.AccessMatrix.Entries), model.MatrixEntry{From: model.MatrixEndpoint{Network: &iot}, To: to, Policy: model.MatrixEntryPolicyAllow})
			c.AccessMatrix.Entries = &entries
		}
	}
	// the facts as the engine has them: the allowed sources and the tunnel subnet of the management hub
	facts := func(twoPort bool) AccessFacts {
		f := AccessFacts{Management: []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24"), netip.MustParsePrefix("10.98.0.0/24")}, TwoPort: twoPort}
		if !twoPort {
			f.ManagementSubnet = netip.MustParsePrefix("192.168.56.0/24")
		}
		return f
	}
	decide := func(w *World, src, dst string, twoPort bool) AccessExplanation {
		sub := Subject{IP: a(src)}
		if n, ok := w.NetworkOf(a(src)); ok {
			sub.Network = n
		}
		return w.AccessDecision(Query{Source: sub, DestIP: a(dst)}, facts(twoPort))
	}
	// the tunnel address of the client and a host behind it
	dsts := []string{"10.98.0.2", "10.97.0.10"}
	for _, twoPort := range []bool{true, false} {
		w := accessWorld(t, base)
		for _, src := range []string{"10.10.0.5", "10.99.0.2"} { // IoT, and lab-rA with its reachable list
			for _, dst := range dsts {
				if got := decide(w, src, dst, twoPort); got.Verdict != "drop" || got.Layer != AccessMatrix || got.Rule != "" || !strings.Contains(got.Reason, "WireGuard network of the role management") {
					t.Errorf("two ports %v, %s -> %s: %+v", twoPort, src, dst, got)
				}
			}
		}
		// the rule still decides what is not the management network
		if got := decide(w, "10.10.0.5", "198.51.100.7", twoPort); got.Verdict != "allow" || got.Rule != everything {
			t.Errorf("two ports %v, to the uplink: %+v", twoPort, got)
		}
		// an explicit entry towards the network, its client or the management network opens it for IoT, and the
		// matrix entry (the allow rule decides first) lets it through
		admin, adm, m := idAdmin, idAdm, model.MatrixEndpointManagement(true)
		for name, to := range map[string]model.MatrixEndpoint{"the network": {Network: &admin}, "the client": {Client: &adm}, "the management network": {Management: &m}} {
			w := accessWorld(t, withEntry(to))
			for _, dst := range dsts {
				if got := decide(w, "10.10.0.5", dst, twoPort); got.Verdict != "allow" || got.Rule != everything {
					t.Errorf("two ports %v, entry to %s, IoT -> %s: %+v", twoPort, name, dst, got)
				}
				// without the allow rule the entry itself allows it
				w2 := accessWorld(t, func(c *model.Configuration) { withEntry(to)(c); c.AccessRules, c.AccessRuleOrder = nil, nil })
				if got := w2.AccessVerdict(a("10.10.0.5"), a(dst), "tcp", 22, facts(twoPort)); got.Verdict != "allow" || !strings.Contains(got.Reason, "access matrix entry") {
					t.Errorf("two ports %v, entry to %s alone, IoT -> %s: %+v", twoPort, name, dst, got)
				}
			}
			// lab-rA has no entry: still dropped
			if got := decide(w, "10.99.0.2", "10.97.0.10", twoPort); got.Verdict != "drop" || got.Rule != "" {
				t.Errorf("two ports %v, entry to %s, lab-rA: %+v", twoPort, name, got)
			}
		}
		// an entry towards the admin network does not open the rest of the management network
		w = accessWorld(t, withEntry(model.MatrixEndpoint{Network: &admin}))
		if got := decide(w, "10.10.0.5", "192.168.56.9", twoPort); got.Verdict != "drop" || got.Rule != "" {
			t.Errorf("two ports %v, entry to the admin network, IoT -> the management host: %+v", twoPort, got)
		}
	}
}
