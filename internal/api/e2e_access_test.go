//go:build testbed

package api_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M9 over the API on a real kernel: a rule overlay created through HTTP refuses the traffic of its scope,
// its counter comes back with the overlay and in the list, explain names it, and a drop rule on UDP 53
// silences the DNS proxy for the device both for queries to the gateway's address (redirected into the
// service namespace) and for queries sent directly to the service namespace's address 169.254.100.2.

// dnsBed is the gateway with its service namespace and the DNS proxy, and an upstream resolver that
// knows example.test. direct asks the service namespace's address by itself, the way a device that
// bypasses the gateway's DNS address would.
type dnsBed struct {
	g      *gw
	top    *testbed.Topology
	direct func() (string, error)
}

func newDNSBed(t *testing.T) *dnsBed {
	ns := fmt.Sprintf("svc%06x", rand.Intn(1<<24))
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "delete", ns).Run() })
	g, top := newBedGW(t, func(o *options) {
		o.serviceNS = ns
		o.resolvers = []netip.Addr{netip.MustParseAddr(testbed.ServerAddr)}
	})
	top.Server.Start("dnsmasq", "--no-daemon", "--no-resolv", "--no-hosts", "--conf-file=/dev/null", "--user=root", "--bind-interfaces",
		"--listen-address="+testbed.ServerAddr, "--address=/example.test/203.0.113.77")
	waitFor(t, 20*time.Second, "the upstream resolver answers the gateway", func() bool {
		out, err := top.GW.Run(context.Background(), "dig", "+short", "+time=1", "+tries=1", "@"+testbed.ServerAddr, "example.test")
		return err == nil && strings.TrimSpace(out) == "203.0.113.77"
	})
	waitFor(t, 30*time.Second, "svc0", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "-br", "addr", "show", "dev", "svc0")
		return err == nil
	})
	port := 443
	if cfg := g.e.Snapshot().Config; cfg != nil && cfg.Management.UiPort != nil && *cfg.Management.UiPort > 0 {
		port = *cfg.Management.UiPort
	}
	base := fmt.Sprintf("http://169.254.100.1:%d", port)
	l, err := testbed.ListenIn(top.GW.Name, "tcp", fmt.Sprintf("169.254.100.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	web := &http.Server{Handler: g.srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = web.Serve(l) }()
	t.Cleanup(func() { _ = web.Close() })
	token := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(token); err != nil {
		t.Fatal(err)
	}
	startProxy(t, ns, token, base)
	waitFor(t, 60*time.Second, "A resolves example.test through the gateway", func() bool {
		out, err := digA(top, top.A, testbed.LAN0Gateway)
		return err == nil && out == "203.0.113.77"
	})
	direct := func() (string, error) { return digA(top, top.A, "169.254.100.2") }
	waitFor(t, 30*time.Second, "A resolves example.test at the service namespace's address", func() bool {
		out, err := direct()
		return err == nil && out == "203.0.113.77"
	})
	return &dnsBed{g: g, top: top, direct: direct}
}

func TestARuleOverlayCreatedOverTheAPIRefusesTheDNSQueriesOfItsNetworkAndCountsThem(t *testing.T) {
	bed := newDNSBed(t)
	g, top, direct := bed.g, bed.top, bed.direct
	// the rule: devices of IoT may not send UDP to port 53, wherever it goes
	r := g.createOverlay(`{"target":{"network":"IoT"},"rule":{"protocol":"udp","ports":[53],"action":"drop"}}`)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ov := r.json(t)
	id := ov["id"].(string)
	if out, err := digA(top, top.A, testbed.LAN0Gateway); err == nil && out != "" {
		t.Errorf("the proxy answered A although the rule drops UDP 53: %q", out)
	}
	if out, err := direct(); err == nil && out != "" {
		t.Errorf("the service namespace answered A directly although the rule drops UDP 53: %q", out)
	}
	// TCP is another protocol: the rule leaves it alone
	if out, err := digA(top, top.A, testbed.LAN0Gateway, "+tcp"); err != nil || out != "203.0.113.77" {
		t.Errorf("DNS over TCP: %q %v", out, err)
	}
	// the counter counted the dropped queries, and says so in the overlay and nowhere else
	got := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)
	c, ok := got["counters"].(map[string]any)
	if !ok || c["packets"].(float64) < 2 || got["state"] != "effective" {
		t.Errorf("the overlay's counter after two refused queries: %v", got)
	}
	// explain names the overlay for both destinations
	for _, dst := range []string{testbed.LAN0Gateway, "169.254.100.2"} {
		a := g.do("GET", "/explain?src="+testbed.ClientAAddr+"&dst="+dst+"&protocol=udp&port=53", nil, nil, nil).json(t)["access"].(map[string]any)
		if a["verdict"] != "drop" || a["layer"] != "overlay_rule" || a["rule"] != id {
			t.Errorf("explain %s: %v", dst, a)
		}
	}
	// the anti-lockout rule's counter exists and the management plane is untouched
	if sys := g.do("GET", "/rules", nil, nil, nil).json(t)["system_rules"].([]any); len(sys) != 1 || sys[0].(map[string]any)["counters"] == nil {
		t.Errorf("%v", sys)
	}

	// the overlay goes: the proxy answers again
	if d := g.do("DELETE", "/overlays/"+id, nil, nil, nil); d.Status != 204 {
		t.Fatalf("%d %s", d.Status, d.Body)
	}
	if out, err := direct(); err != nil || out != "203.0.113.77" {
		t.Errorf("after the rule: %q %v", out, err)
	}
	if out, err := digA(top, top.A, testbed.LAN0Gateway); err != nil || out != "203.0.113.77" {
		t.Errorf("after the rule: %q %v", out, err)
	}
}

// The same rule as a configured rule: a revision with a drop rule on UDP 53 for the network silences the DNS
// proxy for queries to the gateway's address and to the service namespace's address, the rule is listed
// as effective with its counter, explain names it as a configured rule, and a revision without it brings
// the proxy back.
func TestAConfiguredDropRuleOnUDP53SilencesTheDNSProxyForBothAddresses(t *testing.T) {
	bed := newDNSBed(t)
	g, top, direct := bed.g, bed.top, bed.direct
	const ruleID = "a1000000-0000-4000-8000-0000000000d5"

	id := g.mustPatch(map[string]any{
		"access_rules":      map[string]any{ruleID: map[string]any{"name": "no-dns", "source": map[string]any{"network": "IoT"}, "protocol": "udp", "ports": []int{53}, "action": "drop"}},
		"access_rule_order": []string{ruleID},
	})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if out, err := digA(top, top.A, testbed.LAN0Gateway); err == nil && out != "" {
		t.Errorf("the proxy answered A although a configured rule drops UDP 53: %q", out)
	}
	if out, err := direct(); err == nil && out != "" {
		t.Errorf("the service namespace answered A directly although a configured rule drops UDP 53: %q", out)
	}
	if out, err := digA(top, top.A, testbed.LAN0Gateway, "+tcp"); err != nil || out != "203.0.113.77" {
		t.Errorf("DNS over TCP: %q %v", out, err)
	}
	rule := g.do("GET", "/rules/"+ruleID, nil, nil, nil).json(t)
	c, ok := rule["counters"].(map[string]any)
	if rule["state"] != "effective" || !ok || c["packets"].(float64) < 2 {
		t.Errorf("the rule after two refused queries: %v", rule)
	}
	for _, dst := range []string{testbed.LAN0Gateway, "169.254.100.2"} {
		a := g.do("GET", "/explain?src="+testbed.ClientAAddr+"&dst="+dst+"&protocol=udp&port=53", nil, nil, nil).json(t)["access"].(map[string]any)
		if a["verdict"] != "drop" || a["layer"] != "config_rule" || a["rule"] != ruleID {
			t.Errorf("explain %s: %v", dst, a)
		}
	}

	id = g.mustPatch(map[string]any{"access_rules": map[string]any{ruleID: nil}, "access_rule_order": []string{}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if out, err := direct(); err != nil || out != "203.0.113.77" {
		t.Errorf("after the rule: %q %v", out, err)
	}
	if out, err := digA(top, top.A, testbed.LAN0Gateway); err != nil || out != "203.0.113.77" {
		t.Errorf("after the rule: %q %v", out, err)
	}
}
