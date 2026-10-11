package domain

import (
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// With the management network behind the uplink interface the guard stands in front of the access
// rules: an allow rule for everything does not open it for a device under test, an explicit matrix
// entry does (P2-M9-11, decided on 2026-10-11).
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
		// with a management interface of its own the allow rule is an exception to the matrix, as before
		if got := decide(w, src, "192.168.56.9", false); got.Verdict != "allow" || got.Rule != everything {
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
	if got := decide(w, "10.10.0.5", "192.168.56.9", true); got.Verdict != "allow" || got.Rule != everything {
		t.Errorf("IoT with an entry: %+v", got)
	}
	if got := decide(w, "10.99.0.2", "192.168.56.9", true); got.Verdict != "drop" || got.Layer != AccessMatrix {
		t.Errorf("the hub's client has no entry: %+v", got)
	}
}
