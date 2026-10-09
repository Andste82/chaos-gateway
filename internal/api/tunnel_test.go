package api_test

import (
	"fmt"
	"strings"
	"testing"
)

// Tunnel faults and WireGuard actions over the API (M10, plan §2.2.1): the overlay answers after the kernel runs it, shows its
// state, counters and queues of both sides of the tunnel, is explained next to the inner faults, and the actions are effective
// while they change something. The link site-b of the fixture names its endpoint, so its packets can be selected at once.

const tunnelOnLink = `{"fault":{"family":"tunnel","tunnel":{"link":"site-b"},"upload":{"latency":"20ms"},"download":{"latency":"50ms","loss":"1%"}}}`

func TestATunnelOverlayIsEffectiveCountedAndHasQueuesOnBothSidesOfTheTunnel(t *testing.T) {
	g := ready(t)
	r := g.createOverlay(tunnelOnLink)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ov := r.json(t)
	if ov["state"] != "effective" || ov["kind"] != "fault" {
		t.Fatalf("%v", ov)
	}
	got := g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).json(t)
	if c, ok := got["counters"].(map[string]any); !ok || c["epoch"] == nil || c["packets"] == nil {
		t.Errorf("the counters of a tunnel fault in the kernel: %v", got)
	}
	// the upload queue is the IFB's, the download queues are the interfaces' tree
	var up, down []string
	for _, q := range got["queues"].([]any) {
		m := q.(map[string]any)
		switch m["direction"] {
		case "upload":
			up = append(up, m["interface"].(string))
		case "download":
			down = append(down, m["interface"].(string))
		}
	}
	if len(up) != 1 || up[0] != "ifb-cgw" || len(down) < 2 {
		t.Errorf("upload queues on %v, download queues on %v", up, down)
	}
	for _, d := range down {
		if d == "ifb-cgw" {
			t.Errorf("a download queue on the IFB: %v", down)
		}
	}
	// the capabilities say the family and the kind are here
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	if !strings.Contains(fmt.Sprint(caps["features"]), "faults.tunnel") || !strings.Contains(fmt.Sprint(caps["features"]), "wireguard.actions") {
		t.Errorf("%v", caps["features"])
	}
	// an owner's replacement keeps the id; the end of the overlay is a 204
	if r := g.createOverlay(strings.Replace(tunnelOnLink, "20ms", "25ms", 1)); r.Status != 200 || r.json(t)["id"] != ov["id"] {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if r := g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil); r.Status != 204 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

// A tunnel fault of a client that has not connected has no address to select the packets by: the overlay is in the store, shows
// `disabled` (it is not in the packet path), and is effective when the client has connected.
func TestATunnelOverlayOfAClientThatHasNotConnectedIsDisabled(t *testing.T) {
	g := ready(t)
	ov := g.mustCreateOverlay(`{"fault":{"family":"tunnel","tunnel":{"client":"rA"},"latency":"30ms"}}`)
	if ov["state"] != "disabled" {
		t.Fatalf("%v", ov)
	}
	if got := g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).json(t); got["state"] != "disabled" || got["counters"] != nil || got["queues"] != nil {
		t.Errorf("%v", got)
	}
}

// Explain: the traffic into a network behind a link meets the tunnel fault of the link next to the fault of the device's network; the
// traffic that crosses no tunnel meets none.
func TestExplainShowsTheTunnelFaultNextToTheInnerFault(t *testing.T) {
	g := ready(t)
	inner := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"40ms"}}`)
	tun := g.mustCreateOverlay(tunnelOnLink)
	g.observe()
	ex := g.do("GET", "/explain?src=10.10.0.31&dst=10.60.0.10&protocol=tcp&port=22", nil, nil, nil).json(t)
	byFam := map[string]map[string]any{}
	for _, f := range ex["faults"].([]any) {
		m := f.(map[string]any)
		byFam[m["family"].(string)] = m
	}
	if byFam["impairment"] == nil || byFam["impairment"]["winner"].(map[string]any)["id"] != inner["id"] ||
		byFam["tunnel"] == nil || byFam["tunnel"]["winner"].(map[string]any)["id"] != tun["id"] || !strings.HasPrefix(byFam["tunnel"]["tunnel"].(string), "link:") {
		t.Fatalf("%v", ex["faults"])
	}
	k := ex["kernel"].(map[string]any)
	tunnels, _ := k["tunnels"].([]any)
	if len(tunnels) != 1 || tunnels[0].(map[string]any)["endpoint"] != "203.0.113.40:51821" || k["fault_id"] == tunnels[0].(map[string]any)["fault_id"] {
		t.Errorf("kernel %v", k)
	}
	far := g.do("GET", "/explain?src=10.10.0.31&dst=198.51.100.7&protocol=tcp&port=443", nil, nil, nil).json(t)
	for _, f := range far["faults"].([]any) {
		if f.(map[string]any)["family"] == "tunnel" {
			t.Errorf("the traffic to the Internet crosses no tunnel: %v", f)
		}
	}
}

func TestWireGuardActionsAreOverlaysThatAreEffectiveWhileTheyChangeThePeers(t *testing.T) {
	g := ready(t)
	dis := g.mustCreateOverlay(`{"wireguard":{"link":"site-b","action":"disable"}}`)
	if dis["state"] != "effective" || dis["kind"] != "wireguard" || dis["counters"] != nil {
		t.Fatalf("%v", dis)
	}
	km := g.mustCreateOverlay(`{"wireguard":{"client":"rA","action":"key_mismatch"}}`)
	if km["state"] != "effective" {
		t.Fatalf("%v", km)
	}
	// a disabled client has no peer on the interface: the action has nothing to change
	off := g.mustCreateOverlay(`{"wireguard":{"client":"rB","action":"disable"}}`)
	if off["state"] != "disabled" {
		t.Errorf("%v", off)
	}
	// a blocked endpoint of a link that names its address: effective and counting
	blk := g.mustCreateOverlay(`{"wireguard":{"link":"site-b","action":"block_endpoint"}}`)
	got := g.do("GET", "/overlays/"+blk["id"].(string), nil, nil, nil).json(t)
	if got["state"] != "disabled" {
		// the same link is disabled by the first overlay: there is no peer to block
		t.Errorf("a block on a link that is down: %v", got)
	}
	g.do("DELETE", "/overlays/"+dis["id"].(string), nil, nil, nil)
	got = g.do("GET", "/overlays/"+blk["id"].(string), nil, nil, nil).json(t)
	if c, ok := got["counters"].(map[string]any); got["state"] != "effective" || !ok || c["epoch"] == nil {
		t.Errorf("%v", got)
	}
	list := g.do("GET", "/overlays?kind=wireguard", nil, nil, nil).json(t)["items"].([]any)
	if len(list) != 3 {
		t.Errorf("%d overlays of kind wireguard", len(list))
	}
	// an unknown client, a client and a link at once, and no action are not valid requests
	g.badRequest = true // every body below is deliberately invalid
	defer func() { g.badRequest = false }()
	for _, body := range []string{
		`{"wireguard":{"client":"nobody","action":"disable"}}`,
		`{"wireguard":{"client":"rA","link":"site-b","action":"disable"}}`,
		`{"wireguard":{"client":"rA","action":"explode"}}`,
		`{"wireguard":{"client":"rA"}}`,
		`{"target":{"network":"IoT"},"wireguard":{"client":"rA","action":"disable"}}`,
	} {
		if r := g.createOverlay(body); r.Status != 422 && r.Status != 400 {
			t.Errorf("%s: %d %s", body, r.Status, r.Body)
		}
	}
}
