package api_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// DNS faults and hostname selectors over the API (plan §2.4, §2.6, M20).

func dnsConfigOf(t *testing.T, g *gw, admin string) map[string]any {
	t.Helper()
	svc := g.token
	defer func() { g.token = svc }()
	g.token = svc
	r := g.do("GET", "/internal/dns/config", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	return r.json(t)
}

func TestTheDNSConfigurationHoldsTheFaultsAndTheHostnameSetsOfTheActiveOverlays(t *testing.T) {
	defer api.SetDNSPoll(300*time.Millisecond, 20*time.Millisecond)()
	g, admin := serviceGateway(t)
	svc := g.token
	g.token = admin
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	dns := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"dns":{"action":"nxdomain","names":["broker.example.com"]}}`)
	prof := g.mustCreateOverlay(`{"target":{"network":"IoT"},"profile":"dns-broken"}`)
	g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"destination":{"hostname":"cdn.example.net"},"latency":"50ms"}}`)
	g.token = svc
	cfg := dnsConfigOf(t, g, admin)

	faults := cfg["faults"].([]any)
	if len(faults) < 2 {
		t.Fatalf("%v", faults)
	}
	first := faults[0].(map[string]any)
	if first["overlay"] != dns["id"] || first["action"] != "nxdomain" || first["names"].([]any)[0] != "broker.example.com" {
		t.Errorf("the device's fault comes first: %v", first)
	}
	if src := first["sources"].([]any); len(src) != 1 || src[0] != "10.10.0.31/32" {
		t.Errorf("sources %v", src)
	}
	var sawProfile bool
	for _, f := range faults {
		m := f.(map[string]any)
		if m["overlay"] == prof["id"] && m["action"] == "servfail" {
			sawProfile = true
		}
	}
	if !sawProfile {
		t.Errorf("the dns part of the profile is not in the list: %v", faults)
	}
	sets := cfg["hostname_sets"].([]any)
	if len(sets) != 1 {
		t.Fatalf("%v", sets)
	}
	s0 := sets[0].(map[string]any)
	if names := s0["names"].([]any); len(names) != 1 || names[0] != "cdn.example.net" || !strings.HasPrefix(s0["set"].(string), "dns_") || len(s0["sources"].([]any)) == 0 {
		t.Errorf("%v", s0)
	}
	// the configuration changes with the overlays: a long poll ends with the new one
	gen := cfg["generation"].(float64)
	g.token = admin
	g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"dns":{"action":"timeout"}}`)
	g.token = svc
	next := g.do("GET", fmt.Sprintf("/internal/dns/config?after=%d", int64(gen)), nil, nil, nil)
	if next.Status != 200 || next.json(t)["generation"].(float64) <= gen {
		t.Errorf("%d %s", next.Status, next.Body)
	}
}

func TestADNSOverlayHasAStateAndIsOverriddenByAMoreSpecificOne(t *testing.T) {
	g, admin := serviceGateway(t)
	g.token = admin
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	// the network's fault names every name; the device's is a more specific level for everything (a fault on the whole device)
	net := g.mustCreateOverlay(`{"target":{"network":"IoT"},"dns":{"action":"servfail"}}`)
	if st := net["state"]; st != "effective" {
		t.Errorf("a DNS fault is effective while it is in the proxy's list: %v", net)
	}
	g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"dns":{"action":"timeout"}}`)
	// the stretches of the network that no device owns still have the network's fault: partially
	got := g.do("GET", "/overlays/"+net["id"].(string), nil, nil, nil).json(t)
	if got["state"] != "effective" && got["state"] != "partially_overridden" {
		t.Errorf("%v", got["state"])
	}
	// a DNS fault and an impairment fault do not compete (E5)
	lat := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"fault":{"latency":"100ms"}}`)
	if lat["state"] != "effective" {
		t.Errorf("%v", lat)
	}
}

func TestTheProxyReportsResolutionsAndTheKernelsSetHoldsThem(t *testing.T) {
	g, admin := serviceGateway(t)
	svc := g.token
	g.token = admin
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"destination":{"hostname":"broker.example.com"},"latency":"50ms"}}`)
	g.token = svc
	cfg := dnsConfigOf(t, g, admin)
	set := cfg["hostname_sets"].([]any)[0].(map[string]any)["set"].(string)
	body := []any{map[string]any{"set": set, "client": "10.10.0.31", "name": "broker.example.com", "addresses": []any{"203.0.113.7", "203.0.113.8"}, "ttl": 30}}
	if r := g.do("POST", "/internal/dns/resolutions", body, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	var name string
	for _, c := range g.k.NftSetNames() {
		if strings.HasPrefix(c, set+"_") {
			name = c
		}
	}
	els := g.k.SetElements(name)
	if len(els) != 2 || !strings.Contains(els[0], "10.10.0.31") || !strings.Contains(els[0], `"timeout":600`) {
		t.Fatalf("the kernel's set %q holds %v", name, els)
	}
	// the flow that follows is listed with the name
	g.token = admin
	g.k.SetConntrack("tcp 6 86400 ESTABLISHED src=10.10.0.31 dst=203.0.113.7 sport=40000 dport=8883 packets=3 bytes=300 src=203.0.113.7 dst=10.10.0.31 sport=8883 dport=40000 packets=3 bytes=300 [ASSURED] mark=0 use=1\n")
	flows := g.do("GET", "/flows", nil, nil, nil).json(t)["items"].([]any)
	if len(flows) != 1 || flows[0].(map[string]any)["dst_hostname"] != "broker.example.com" {
		t.Errorf("%v", flows)
	}
	// explain knows the name too: the fault applies to the traffic of the device to that address
	ex := g.do("GET", "/explain?device=esp32-42&dst=203.0.113.7&protocol=tcp&port=8883", nil, nil, nil)
	if ex.Status != 200 || !strings.Contains(string(ex.Body), ov["id"].(string)) {
		t.Errorf("%d %s", ex.Status, ex.Body)
	}

	// the rules of the endpoint: service tokens only, a valid body
	g.token = admin
	if r := g.do("POST", "/internal/dns/resolutions", body, nil, nil); r.Status != 403 {
		t.Errorf("a full token: %d", r.Status)
	}
	g.token = svc
	for name, bad := range map[string]any{
		"an address": []any{map[string]any{"set": set, "client": "10.10.0.31", "name": "x", "addresses": []any{"nope"}, "ttl": 1}},
		"a client":   []any{map[string]any{"set": set, "client": "2001:db8::1", "name": "x", "addresses": []any{"203.0.113.7"}, "ttl": 1}},
		"no address": []any{map[string]any{"set": set, "client": "10.10.0.31", "name": "x", "addresses": []any{}, "ttl": 1}},
	} {
		g.badRequest = true
		r := g.do("POST", "/internal/dns/resolutions", bad, nil, nil)
		g.badRequest = false
		if r.Status != 422 && r.Status != 400 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	// a set that does not exist is ignored, not an error
	if r := g.do("POST", "/internal/dns/resolutions", []any{map[string]any{"set": "dns_ffffffffff", "client": "10.10.0.31", "name": "x", "addresses": []any{"203.0.113.7"}, "ttl": 1}}, nil, nil); r.Status != 204 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

// A query that a DNS fault changed announces itself on the event stream, once for each overlay and device in a moment,
// and the log entry names the overlay.
func TestADNSFaultAnnouncesItselfOnTheEventStream(t *testing.T) {
	g, admin := serviceGateway(t)
	svc := g.token
	g.token = admin
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	ov := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"dns":{"action":"nxdomain"}}`)
	ev, cancel := g.e.Subscribe()
	defer cancel()
	g.token = svc
	now := time.Now().UTC().Truncate(time.Second)
	entry := func(i int) map[string]any {
		return map[string]any{"time": now.Add(time.Duration(i) * time.Second).Format(time.RFC3339), "client": "10.10.0.31", "name": fmt.Sprintf("h%d.example.test", i), "type": "A",
			"rcode": "NXDOMAIN", "protocol": "udp", "fault": ov["id"]}
	}
	if r := g.do("POST", "/internal/dns/queries", []any{entry(0), entry(1), entry(2)}, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	var got []string
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case e := <-ev:
			if e.Type == "dns_fault_triggered" {
				got = append(got, e.Data["name"].(string))
				if e.Data["overlay"] != ov["id"] || e.Data["device"] != espID {
					t.Errorf("%+v", e.Data)
				}
			}
		case <-deadline:
			break loop
		}
	}
	if len(got) != 1 || got[0] != "h0.example.test" {
		t.Errorf("events for %v: want one for the first query", got)
	}
	g.token = admin
	q := g.do("GET", "/dns/queries?device=esp32-42", nil, nil, nil).json(t)["items"].([]any)
	if len(q) != 3 || q[0].(map[string]any)["fault"] != ov["id"] {
		t.Errorf("%v", q)
	}
	// the counter of the overlay is the number of answers the fault changed (no bytes), for the overlay and for the dns part of a profile
	view := g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).json(t)
	if c, ok := view["counters"].(map[string]any); !ok || c["packets"] != float64(3) || c["bytes"] != float64(0) || c["epoch"] == nil {
		t.Errorf("counters %v", view["counters"])
	}
	prof := g.mustCreateOverlay(`{"target":{"network":"IoT"},"profile":"dns-broken"}`)
	g.token = svc
	e := entry(7)
	e["fault"] = prof["id"]
	if r := g.do("POST", "/internal/dns/queries", []any{e}, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.token = admin
	if c, ok := g.do("GET", "/overlays/"+prof["id"].(string), nil, nil, nil).json(t)["counters"].(map[string]any); !ok || c["packets"] != float64(1) {
		t.Errorf("the dns part of a profile: %v", c)
	}
}

func TestACapabilityListsDNSFaults(t *testing.T) {
	g := ready(t)
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	feats := fmt.Sprint(caps["features"])
	for _, f := range []string{"dns.faults", "dns.hostname_selectors"} {
		if !strings.Contains(feats, f) {
			t.Errorf("%s is missing: %s", f, feats)
		}
	}
	steps := fmt.Sprint(caps["step_types"])
	if !strings.Contains(steps, "dns") {
		t.Errorf("%s", steps)
	}
}

// The flows API names the host of an address from the proxy's log too, whether or not a rule or a fault selects the name: the name asked and
// the CNAME targets of the answer.
func TestAFlowIsNamedAfterTheAnswersOfTheProxyEvenWhenNothingSelectsTheName(t *testing.T) {
	g, admin := serviceGateway(t)
	svc := g.token
	g.token = admin
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	g.token = svc
	now := time.Now().UTC().Truncate(time.Second)
	e := map[string]any{"time": now.Format(time.RFC3339), "client": "10.10.0.31", "name": "www.example.org", "type": "A", "rcode": "NOERROR", "protocol": "udp",
		"answers": []any{"edge.cdn.example.net", "203.0.113.7"}}
	if r := g.do("POST", "/internal/dns/queries", []any{e}, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.token = admin
	g.k.SetConntrack("tcp 6 86400 ESTABLISHED src=10.10.0.31 dst=203.0.113.7 sport=40000 dport=443 packets=3 bytes=300 src=203.0.113.7 dst=10.10.0.31 sport=443 dport=40000 packets=3 bytes=300 [ASSURED] mark=0 use=1\n" +
		"tcp 6 86400 ESTABLISHED src=10.10.0.31 dst=198.51.100.9 sport=40001 dport=443 packets=3 bytes=300 src=198.51.100.9 dst=10.10.0.31 sport=443 dport=40001 packets=3 bytes=300 [ASSURED] mark=0 use=1\n")
	flows := g.do("GET", "/flows", nil, nil, nil).json(t)["items"].([]any)
	got := map[string]any{}
	for _, f := range flows {
		m := f.(map[string]any)
		got[m["dst"].(string)] = m["dst_hostname"]
	}
	if got["203.0.113.7"] != "www.example.org" || got["198.51.100.9"] != nil {
		t.Errorf("%v", got)
	}
}
