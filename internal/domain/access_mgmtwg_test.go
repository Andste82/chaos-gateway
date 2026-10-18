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
const idAdmin, idAdm = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d", "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"

// withAdminHub adds the management hub "admin" (10.98.0.0/24) with its client adm (10.98.0.2, the network
// 10.97.0.0/24 behind it), and lab-rA, a client of the test hub, names the admin network in its reachable list.
func withAdminHub(c *model.Configuration) {
	role := model.WireGuardNetworkRoleManagement
	adm := map[string]model.WireGuardClient{idAdm: {Name: "adm", Address: "10.98.0.2", ClientNetworks: &[]string{"10.97.0.0/24"}}}
	var n model.Network
	_ = n.FromWireGuardNetwork(model.WireGuardNetwork{Type: model.WireGuardNetworkTypeWireguard, Kind: model.Hub, Name: "admin", Role: &role,
		Address: "10.98.0.1/24", ListenPort: 51822, Clients: &adm})
	(*c.Networks)[idAdmin] = n
	hubNet := (*c.Networks)[idHub]
	hub, _ := hubNet.AsWireGuardNetwork()
	cl := (*hub.Clients)[idClient]
	admin := idAdmin
	cl.Reachable = &[]model.MatrixEndpoint{{Network: &admin}}
	(*hub.Clients)[idClient] = cl
	_ = hubNet.FromWireGuardNetwork(hub)
	(*c.Networks)[idHub] = hubNet
}

// adminFacts are the facts as the engine has them: the allowed sources and the tunnel subnet of the management
// hub.
func adminFacts(twoPort bool) AccessFacts {
	f := AccessFacts{Management: []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24"), netip.MustParsePrefix("10.98.0.0/24")}, TwoPort: twoPort}
	if !twoPort {
		f.ManagementSubnet = netip.MustParsePrefix("192.168.56.0/24")
	}
	return f
}

func TestAManagementRoleWireGuardNetworkIsPartOfTheManagementNetworkForTheGuard(t *testing.T) {
	everything := "a1000000-0000-4000-8000-000000000001"
	a := netip.MustParseAddr
	base := func(c *model.Configuration) {
		withAdminHub(c)
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
	facts := adminFacts
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

// The review of 2026-10-18, as the compiler has it: the endpoint `management` stands for the WireGuard networks of
// the role management with a rank after the networks, as a destination and as a source, so an entry towards or
// from the network itself decides first, in either order of the policies and in both topologies. The rest of the
// management network is still decided by the management entry.
func TestExplainLetsTheSpecificEntryWinOverTheManagementEntryForAManagementRoleWireGuardNetwork(t *testing.T) {
	a := netip.MustParseAddr
	iot, admin, m := idIoT, idAdmin, model.MatrixEndpointManagement(true)
	entries := func(specific, mgmt model.MatrixEntryPolicy) func(*model.Configuration) {
		return func(c *model.Configuration) {
			withAdminHub(c)
			e := []model.MatrixEntry{
				{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Network: &admin}, Policy: specific},
				{From: model.MatrixEndpoint{Network: &iot}, To: model.MatrixEndpoint{Management: &m}, Policy: mgmt},
				{From: model.MatrixEndpoint{Network: &admin}, To: model.MatrixEndpoint{Network: &iot}, Policy: specific},
				{From: model.MatrixEndpoint{Management: &m}, To: model.MatrixEndpoint{Network: &iot}, Policy: mgmt},
			}
			c.AccessMatrix.Entries = &e
		}
	}
	verdict := func(p model.MatrixEntryPolicy) string {
		if p == model.MatrixEntryPolicyDeny {
			return "drop"
		}
		return "allow"
	}
	for _, twoPort := range []bool{false, true} {
		for _, specific := range []model.MatrixEntryPolicy{model.MatrixEntryPolicyDeny, model.MatrixEntryPolicyAllow} {
			mgmt := model.MatrixEntryPolicyAllow
			if specific == model.MatrixEntryPolicyAllow {
				mgmt = model.MatrixEntryPolicyDeny
			}
			w := accessWorld(t, entries(specific, mgmt))
			f := adminFacts(twoPort)
			// IoT to the tunnel subnet (an allowed source too) and to the network behind adm: entry 0 decides
			for _, dst := range []string{"10.98.0.2", "10.97.0.10"} {
				if got := w.AccessVerdict(a("10.10.0.5"), a(dst), "tcp", 443, f); got.Verdict != verdict(specific) || got.Reason != "access matrix entry 0 ("+string(specific)+")" {
					t.Errorf("two ports %v, specific %s, IoT -> %s: %+v", twoPort, specific, dst, got)
				}
				// and from there to IoT: entry 2
				if got := w.AccessVerdict(a(dst), a("10.10.0.5"), "tcp", 443, f); got.Verdict != verdict(specific) || got.Reason != "access matrix entry 2 ("+string(specific)+")" {
					t.Errorf("two ports %v, specific %s, %s -> IoT: %+v", twoPort, specific, dst, got)
				}
			}
			// the rest of the management network: the management entries (a deny leaves it to the guard)
			if got := w.AccessVerdict(a("10.10.0.5"), a("192.168.56.9"), "tcp", 443, f); got.Verdict != verdict(mgmt) || (mgmt == model.MatrixEntryPolicyAllow && got.Reason != "access matrix entry 1 (allow)") {
				t.Errorf("two ports %v, specific %s, IoT -> the management host: %+v", twoPort, specific, got)
			}
			if got := w.AccessVerdict(a("192.168.56.9"), a("10.10.0.5"), "tcp", 443, f); got.Verdict != verdict(mgmt) || got.Reason != "access matrix entry 3 ("+string(mgmt)+")" {
				t.Errorf("two ports %v, specific %s, the management host -> IoT: %+v", twoPort, specific, got)
			}
		}
	}
}

// Management as a source (the review of 2026-10-18): an entry `management -> X` matches what comes in through a
// WireGuard network of the role management, the networks behind its clients included, as the compiler matches it
// by `iifname`; without the entry the default drops it.
func TestExplainCountsAManagementRoleWireGuardNetworkAsTheManagementSource(t *testing.T) {
	a := netip.MustParseAddr
	iot, m := idIoT, model.MatrixEndpointManagement(true)
	with := accessWorld(t, func(c *model.Configuration) {
		withAdminHub(c)
		e := []model.MatrixEntry{{From: model.MatrixEndpoint{Management: &m}, To: model.MatrixEndpoint{Network: &iot}, Policy: model.MatrixEntryPolicyAllow}}
		c.AccessMatrix.Entries = &e
	})
	without := accessWorld(t, func(c *model.Configuration) { withAdminHub(c); c.AccessMatrix.Entries = nil })
	for _, twoPort := range []bool{false, true} {
		for _, src := range []string{"10.98.0.2", "10.97.0.10", "192.168.56.9"} {
			if got := with.AccessVerdict(a(src), a("10.10.0.5"), "tcp", 443, adminFacts(twoPort)); got.Verdict != "allow" || got.Reason != "access matrix entry 0 (allow)" {
				t.Errorf("two ports %v, %s -> IoT: %+v", twoPort, src, got)
			}
			if got := without.AccessVerdict(a(src), a("10.10.0.5"), "tcp", 443, adminFacts(twoPort)); got.Verdict != "drop" {
				t.Errorf("two ports %v, %s -> IoT without the entry: %+v", twoPort, src, got)
			}
		}
	}
}
