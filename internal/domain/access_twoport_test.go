package domain

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The management guard stands in front of the access rules: an allow rule for everything does not open
// the management network for a device under test, an explicit matrix entry does. Behind the uplink
// since P2-M9-11 (decided on 2026-10-11), behind a management interface of its own since P31-M35a-10
// (decided on 2026-10-17).
func TestNoAccessRuleOpensTheManagementNetworkBehindTheUplink(t *testing.T) {
	everything := "a1000000-0000-4000-8000-000000000001"
	allow := func(c *model.Configuration) {
		name, global := "everything", model.ScopeGlobal(true)
		rules := map[string]model.AccessRule{everything: {Name: &name, Source: model.Scope{Global: &global}, Action: model.AccessActionAllow}}
		c.AccessRules, c.AccessRuleOrder = &rules, &[]uuid.UUID{uuid.MustParse(everything)}
	}
	a := netip.MustParseAddr
	mgmt := []netip.Prefix{netip.MustParsePrefix("192.168.56.0/24")}
	decide := func(w *World, src, dst string, twoPort bool) AccessExplanation {
		sub := Subject{IP: a(src)}
		if n, ok := w.NetworkOf(a(src)); ok {
			sub.Network = n
		}
		return w.AccessDecision(Query{Source: sub, DestIP: a(dst)}, AccessFacts{Management: mgmt, TwoPort: twoPort})
	}
	w := accessWorld(t, allow)
	// a local test network and a client of a hub of the role test are devices under test
	for _, src := range []string{"10.10.0.5", "10.99.0.2"} {
		if got := decide(w, src, "192.168.56.9", true); got.Verdict != "drop" || got.Layer != AccessMatrix || got.Rule != "" {
			t.Errorf("%s: %+v", src, got)
		}
		// with a management interface of its own the guard stands in front of the rules too
		// (P31-M35a-10, decided on 2026-10-17)
		if got := decide(w, src, "192.168.56.9", false); got.Verdict != "drop" || got.Layer != AccessMatrix || got.Rule != "" || strings.Contains(got.Reason, "uplink") {
			t.Errorf("%s, three ports: %+v", src, got)
		}
		// the rule still decides everything else
		if got := decide(w, src, "198.51.100.7", true); got.Verdict != "allow" || got.Rule != everything {
			t.Errorf("%s to the uplink: %+v", src, got)
		}
	}
	// an explicit matrix entry from IoT to the management network opens it for IoT only
	w = accessWorld(t, func(c *model.Configuration) {
		allow(c)
		idx, _ := BuildIndex(c)
		iotID, _ := idx.Resolve(KindNetwork, "IoT")
		m := model.MatrixEndpointManagement(true)
		entries := append(deref(c.AccessMatrix.Entries), model.MatrixEntry{From: model.MatrixEndpoint{Network: &iotID}, To: model.MatrixEndpoint{Management: &m}, Policy: model.MatrixEntryPolicyAllow})
		c.AccessMatrix.Entries = &entries
	})
	for _, twoPort := range []bool{true, false} {
		if got := decide(w, "10.10.0.5", "192.168.56.9", twoPort); got.Verdict != "allow" || got.Rule != everything {
			t.Errorf("IoT with an entry (two ports %v): %+v", twoPort, got)
		}
		if got := decide(w, "10.99.0.2", "192.168.56.9", twoPort); got.Verdict != "drop" || got.Layer != AccessMatrix {
			t.Errorf("the hub's client has no entry (two ports %v): %+v", twoPort, got)
		}
	}
	// a deny entry opens nothing
	w = accessWorld(t, func(c *model.Configuration) {
		allow(c)
		idx, _ := BuildIndex(c)
		iotID, _ := idx.Resolve(KindNetwork, "IoT")
		m := model.MatrixEndpointManagement(true)
		entries := append(deref(c.AccessMatrix.Entries), model.MatrixEntry{From: model.MatrixEndpoint{Network: &iotID}, To: model.MatrixEndpoint{Management: &m}, Policy: model.MatrixEntryPolicyDeny})
		c.AccessMatrix.Entries = &entries
	})
	if got := decide(w, "10.10.0.5", "192.168.56.9", false); got.Verdict != "drop" || got.Rule != "" {
		t.Errorf("IoT with a deny entry: %+v", got)
	}
}

// The guard covers what the kernel's guard drops (P31-AUDIT-01): the allowed sources, and in the three-port
// topology the management interface's subnet as well. With allowed sources narrower than the subnet a host
// on the uplink's subnet is no management host in the two-port topology (the kernel guard matches the
// sources only), while the three-port guard keeps the whole management subnet closed until an entry opens it.
func TestTheGuardCoversWhatTheKernelGuardDropsWithNarrowAllowedSources(t *testing.T) {
	everything := "a1000000-0000-4000-8000-000000000001"
	allow := func(c *model.Configuration) {
		name, global := "everything", model.ScopeGlobal(true)
		rules := map[string]model.AccessRule{everything: {Name: &name, Source: model.Scope{Global: &global}, Action: model.AccessActionAllow}}
		c.AccessRules, c.AccessRuleOrder = &rules, &[]uuid.UUID{uuid.MustParse(everything)}
	}
	a := netip.MustParseAddr
	decide := func(w *World, dst string, f AccessFacts) AccessExplanation {
		return w.AccessDecision(Query{Source: Subject{IP: a("10.10.0.5"), Network: "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"}, DestIP: a(dst)}, f)
	}
	w := accessWorld(t, allow)
	if n, ok := w.NetworkOf(a("10.10.0.5")); !ok || n != "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21" {
		t.Fatalf("10.10.0.5 is in %q", n)
	}
	twoPort := AccessFacts{Management: []netip.Prefix{netip.MustParsePrefix("192.168.1.10/32")}, TwoPort: true}
	if got := decide(w, "192.168.1.20", twoPort); got.Verdict != "allow" || got.Rule != everything {
		t.Errorf("two ports, a host on the uplink's subnet: %+v", got)
	}
	if got := decide(w, "192.168.1.10", twoPort); got.Verdict != "drop" || got.Rule != "" {
		t.Errorf("two ports, the allowed source: %+v", got)
	}
	threePort := AccessFacts{Management: []netip.Prefix{netip.MustParsePrefix("192.168.56.2/32")}, ManagementSubnet: netip.MustParsePrefix("192.168.56.0/24")}
	for _, dst := range []string{"192.168.56.2", "192.168.56.20"} {
		if got := decide(w, dst, threePort); got.Verdict != "drop" || got.Rule != "" {
			t.Errorf("three ports, %s: %+v", dst, got)
		}
	}
	w = accessWorld(t, func(c *model.Configuration) {
		allow(c)
		idx, _ := BuildIndex(c)
		iotID, _ := idx.Resolve(KindNetwork, "IoT")
		m := model.MatrixEndpointManagement(true)
		entries := append(deref(c.AccessMatrix.Entries), model.MatrixEntry{From: model.MatrixEndpoint{Network: &iotID}, To: model.MatrixEndpoint{Management: &m}, Policy: model.MatrixEntryPolicyAllow})
		c.AccessMatrix.Entries = &entries
	})
	for _, dst := range []string{"192.168.56.2", "192.168.56.20"} {
		if got := decide(w, dst, threePort); got.Verdict != "allow" || got.Rule != everything {
			t.Errorf("three ports with an entry, %s: %+v", dst, got)
		}
	}
}
