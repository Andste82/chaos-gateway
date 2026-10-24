package compiler

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The hardcoded resolvers of a network (plan §2.6, M20): DNS to other resolvers is redirected to the DNS proxy, DNS
// over TLS is blocked.

const netIoT = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"

func withNetworkDNS(redirect, dot bool) func(*model.Configuration, *Host) {
	return func(cfg *model.Configuration, _ *Host) {
		n := (*cfg.Networks)[netIoT]
		lan, _ := n.AsLanNetwork()
		d := model.NetworkDns{}
		if redirect {
			d.RedirectHardcoded = ptrTo(true)
		}
		if dot {
			d.BlockDot = ptrTo(true)
		}
		lan.Dns = &d
		_ = n.FromLanNetwork(lan)
		(*cfg.Networks)[netIoT] = n
	}
}

func ptrTo[T any](v T) *T { return &v }

func compileDNSNet(t *testing.T, redirect, dot, service bool) *Target {
	t.Helper()
	cfg, host := loadConfig(t, "gateway.yaml"), testbedHost()
	withNetworkDNS(redirect, dot)(cfg, &host)
	in := Input{Config: cfg, Host: host, Generation: Generation{Revision: 1, Seq: 1}}
	if service {
		in.ServiceNS = "cgsvc"
	}
	return Compile(in)
}

func TestDNSToOtherResolversIsRedirectedToTheProxyForTheNetworksThatAskForIt(t *testing.T) {
	tg := compileDNSNet(t, true, false, true)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	var iot Bridge
	for _, b := range tg.Bridges {
		if b.NetworkID == netIoT {
			iot = b
		}
	}
	if !iot.RedirectDNS || iot.BlockDoT {
		t.Fatalf("%+v", iot)
	}
	rules := renderRules(chainOf(t, tg, "prerouting"))
	// the rules for the gateway's own address come first, then those for every other destination of the
	// network that asks, except what lies in its own segments (the network and the networks behind its
	// routers), and not for the networks that do not
	var own, other []string
	for _, r := range rules {
		switch {
		case strings.Contains(r, "dnat") && strings.Contains(r, "iifname "+iot.Name) || strings.Contains(r, "meta iifname "+iot.Name):
			if strings.Contains(r, "!=") {
				other = append(other, r)
			} else {
				own = append(own, r)
			}
		}
	}
	if len(own) != 2 || len(other) != 2 {
		t.Fatalf("own %v, other %v (all %v)", own, other, rules)
	}
	for i, proto := range []string{"udp", "tcp"} {
		if !strings.Contains(other[i], proto+" dport 53") || !strings.Contains(other[i], "ip daddr != { 10.10.0.0/24, 10.30.0.0/24 }") || !strings.Contains(other[i], "dnat") {
			t.Errorf("rule %q", other[i])
		}
	}
	for _, r := range rules {
		if strings.Contains(r, "!=") && !strings.Contains(r, iot.Name) {
			t.Errorf("a network that did not ask: %s", r)
		}
	}
	// the other network and a gateway without the setting have the two rules of the gateway's own address only
	plain := compileDNSNet(t, false, false, true)
	if n := len(renderRules(chainOf(t, plain, "prerouting"))); n != 4 {
		t.Errorf("%d rules without the setting", n)
	}
}

func TestWithoutAServiceNamespaceTheRedirectIsReportedAndNothingIsRedirected(t *testing.T) {
	tg := compileDNSNet(t, true, false, false)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	for _, c := range tg.Nft.Chains {
		if c.Name == "prerouting" {
			t.Error("a redirect without a service namespace")
		}
	}
	found := false
	for _, p := range tg.Problems {
		found = found || (p.Code == CodeUnsupported && p.Severity == SevWarning && strings.Contains(p.Message, "DNS proxy"))
	}
	if !found {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestDNSOverTLSIsDroppedForTheNetworksThatBlockItAfterTheAccessRules(t *testing.T) {
	tg := compileDNSNet(t, false, true, true)
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	dot := renderRules(chainOf(t, tg, DoTChain))
	if len(dot) != 1 || !strings.Contains(dot[0], "tcp dport 853") || !strings.Contains(dot[0], "counter") || !strings.HasSuffix(dot[0], " drop") {
		t.Fatalf("%v", dot)
	}
	fwd := renderRules(chainOf(t, tg, "forward"))
	jump, matrix := -1, -1
	for i, r := range fwd {
		if r == "jump "+DoTChain {
			jump = i
		}
		if strings.Contains(r, "meta oifname uplink") || strings.Contains(r, "oifname wan0") {
			matrix = i
		}
	}
	if jump < 0 || (matrix >= 0 && jump > matrix) {
		t.Errorf("the jump is at %d, the matrix at %d: %v", jump, matrix, fwd)
	}
	var counted bool
	for _, c := range tg.Nft.Counters {
		counted = counted || c == dotCounterName(netIoT)
	}
	if !counted {
		t.Errorf("counters %v", tg.Nft.Counters)
	}
	// no network that blocks it, no chain
	for _, c := range compileDNSNet(t, true, false, true).Nft.Chains {
		if c.Name == DoTChain {
			t.Error("a chain without a network that blocks DoT")
		}
	}
}

// With an access rule: an allow rule for tcp/853 stands in front of the block, a drop rule behind or in front does not matter.
func TestAnAllowRuleIsAnExceptionToTheDoTBlock(t *testing.T) {
	w := newFaultWorld(t)
	withNetworkDNS(false, true)(w.cfg, nil)
	w.addConfigRule(ruleA, `{source: {device: esp32-42}, protocol: tcp, ports: [853], action: allow}`)
	tg := w.compile(nil)
	fwd := renderRules(chainOf(t, tg, "forward"))
	acc, dot := -1, -1
	for i, r := range fwd {
		switch r {
		case "jump " + AccessForwardChain:
			acc = i
		case "jump " + DoTChain:
			dot = i
		}
	}
	if acc < 0 || dot < 0 || acc > dot {
		t.Errorf("access at %d, dot at %d", acc, dot)
	}
}
