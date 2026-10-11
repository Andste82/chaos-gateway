//go:build testbed

package engine_test

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The two-port guard on the real kernel (plan §2.16, P2-M9-11, decided on 2026-10-11). The management
// network lies behind the uplink interface (here the server's network: the server stands for the
// management host), and a device under test does not reach it, whatever an access rule says: the guard
// stands in front of the jump to the access rules, as the anti-lockout rule does for the control plane.
// A rule that allows every source to reach any destination, and an overlay that allows one device to
// reach the management host itself, leave it closed; the same rule still opens the rest of the uplink
// (a host behind the uplink router), so the rule is in force. `explain` gives the kernel's verdict for
// every probe. Nothing here is statistical: the functional assertions hold under emulation.
func TestNoAllowRuleOpensTheManagementNetworkBehindTheUplinkOnTheRealKernel(t *testing.T) {
	const allowAll = "a1000000-0000-4000-8000-000000000031"
	r := startFaultLab(t, func(c *model.Configuration) {
		wan0, sources := "wan0", []model.Ipv4Cidr{"203.0.113.0/24"}
		c.Management.Interface = model.InterfaceRef{Name: &wan0}
		c.Management.AllowedSources = &sources
		withConfigRules(map[string]model.AccessRule{
			allowAll: yamlRule(t, `{name: allow-everything, source: {global: true}, action: allow}`),
		}, allowAll)(c)
	})
	top := r.top
	startMatrixServer(t, top.Server)

	type probe struct {
		name  string
		from  *testbed.Namespace
		src   string
		dst   string
		proto string
		port  int
	}
	probes := []probe{
		{"A to the management host, tcp 9101", top.A, testbed.ClientAAddr, testbed.ServerAddr, "tcp", 9101},
		{"B to the management host, tcp 9101", top.B, testbed.ClientBAddr, testbed.ServerAddr, "tcp", 9101},
		{"C to the management host, tcp 9101", top.C, testbed.ClientCAddr, testbed.ServerAddr, "tcp", 9101},
		{"A to the management host, udp 5353", top.A, testbed.ClientAAddr, testbed.ServerAddr, "udp", 5353},
		{"A to another address of the management network", top.A, testbed.ClientAAddr, testbed.ServerAddr2, "tcp", 9101},
		{"A to a host behind the uplink router", top.A, testbed.ClientAAddr, testbed.InternetAddr, "tcp", 9101},
		{"C to a host behind the uplink router", top.C, testbed.ClientCAddr, testbed.InternetAddr, "tcp", 9102},
	}
	kernelVerdict := map[string]string{"ok": "allow", "timeout": "drop", "refused": "reject", "reset": "reject"}
	check := func(stage string, want map[string]string) {
		t.Helper()
		for _, p := range probes {
			ex, err := r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(p.src), Dst: p.dst, Protocol: p.proto, Port: p.port})
			if err != nil {
				t.Fatal(err)
			}
			got := kernelVerdict[tryTo(p.from, p.proto, p.dst, p.port)]
			if got != ex.Access.Verdict {
				t.Errorf("%s, %s: the kernel gave %q, explain says %+v", stage, p.name, got, ex.Access)
			}
			if w, ok := want[p.name]; ok && got != w {
				t.Errorf("%s, %s: the kernel gave %q, want %q (explain: %+v)", stage, p.name, got, w, ex.Access)
			}
		}
	}
	closed := func(p probe) bool { return p.dst == testbed.ServerAddr || p.dst == testbed.ServerAddr2 }
	want := map[string]string{}
	for _, p := range probes {
		want[p.name] = "allow"
		if closed(p) {
			want[p.name] = "drop"
		}
	}
	// the rule allows everything, and the management network stays closed
	check("the rule for everything", want)

	// explain names the guard, not a rule
	ex, err := r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(testbed.ClientAAddr), Dst: testbed.ServerAddr, Protocol: "tcp", Port: 9101})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Access.Verdict != "drop" || ex.Access.Layer != "access_matrix" || ex.Access.Rule != "" {
		t.Errorf("explain = %+v, want the guard (a drop in the access matrix, no rule)", ex.Access)
	}
	// and a host behind the uplink router is allowed by the rule
	ex, err = r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(testbed.ClientAAddr), Dst: testbed.InternetAddr, Protocol: "tcp", Port: 9101})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Access.Verdict != "allow" || ex.Access.Layer != "config_rule" || ex.Access.Rule != allowAll {
		t.Errorf("explain = %+v, want the rule", ex.Access)
	}

	// an overlay that allows one device to reach the management host itself does not open it either
	ov := lanRuleOverlay(t, r, fmt.Sprintf("target: {device: %s}\nrule: {action: allow, destination: {cidr: %s/32}}", flDevA, testbed.ServerAddr))
	check("an overlay that allows the management host", want)
	r.deleteOverlay(ov)

	// the guard counted what it dropped
	counters := r.counters()
	if counters["forward_drop"].Packets == 0 {
		t.Errorf("the guard dropped traffic of the probes and counted nothing: %+v", counters["forward_drop"])
	}
}

// The one thing that still opens the management network behind the uplink is an explicit entry of the
// access matrix (plan §2.16): Lab may reach it, IoT may not, and the rule for everything changes neither.
func TestAnExplicitMatrixEntryStillOpensTheManagementNetworkBehindTheUplinkOnTheRealKernel(t *testing.T) {
	const allowAll = "a1000000-0000-4000-8000-000000000032"
	r := startFaultLab(t, func(c *model.Configuration) {
		wan0, sources := "wan0", []model.Ipv4Cidr{"203.0.113.0/24"}
		c.Management.Interface = model.InterfaceRef{Name: &wan0}
		c.Management.AllowedSources = &sources
		withConfigRules(map[string]model.AccessRule{
			allowAll: yamlRule(t, `{name: allow-everything, source: {global: true}, action: allow}`),
		}, allowAll)(c)
		entries := []model.MatrixEntry{}
		if c.AccessMatrix != nil && c.AccessMatrix.Entries != nil {
			entries = *c.AccessMatrix.Entries
		}
		lab, m := labID, model.MatrixEndpointManagement(true)
		entries = append(entries, model.MatrixEntry{From: model.MatrixEndpoint{Network: &lab}, To: model.MatrixEndpoint{Management: &m}, Policy: model.MatrixEntryPolicyAllow})
		c.AccessMatrix = &model.AccessMatrix{Entries: &entries}
	})
	top := r.top
	startMatrixServer(t, top.Server)
	for _, p := range []struct {
		name string
		from *testbed.Namespace
		src  string
		want string
	}{{"Lab (an explicit entry)", top.C, testbed.ClientCAddr, "allow"}, {"IoT A", top.A, testbed.ClientAAddr, "drop"}, {"IoT B", top.B, testbed.ClientBAddr, "drop"}} {
		ex, err := r.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr(p.src), Dst: testbed.ServerAddr, Protocol: "tcp", Port: 9101})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{"ok": "allow", "timeout": "drop"}[tryTo(p.from, "tcp", testbed.ServerAddr, 9101)]
		if got != p.want || ex.Access.Verdict != p.want {
			t.Errorf("%s to the management host: the kernel gave %q, explain says %+v, want %q", p.name, got, ex.Access, p.want)
		}
	}
}
