package api_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// M23b: `chaosctl dns set`, `chaosctl tls set` and `chaosctl dhcp set` against the API on the simulated kernel with the real
// engine (the subtests of TestChaosctlOverlays: one gateway with the configured device esp32-42 on IoT). Each makes the
// overlay the flags describe, which the test reads back from the API, and each is deleted again so that the gateway is as
// it was found.

// overlayOf reads the overlay a chaosctl command printed the id of.
func overlayOf(t *testing.T, g *gw, out string) map[string]any {
	t.Helper()
	id := strings.TrimSpace(out)
	if id == "" || strings.ContainsAny(id, " \n") {
		t.Fatalf("standard output is the id of the overlay: %q", out)
	}
	return g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)
}

func ctlMakesDNSFaults(t *testing.T, g *gw, clk *clock.Fake) {
	var ids []string
	for _, tc := range []struct {
		args []string
		want map[string]any
	}{
		{[]string{"dns", "set", "nxdomain", "--device", "esp32-42", "--name", "broker.example.com", "--ttl", "2m"},
			map[string]any{"action": "nxdomain", "names": []any{"broker.example.com"}}},
		{[]string{"dns", "set", "delay", "--network", "IoT", "--delay", "2s", "--name", "a.example.com,*.cdn.example.net"},
			map[string]any{"action": "delay", "delay": "2s", "names": []any{"a.example.com", "*.cdn.example.net"}}},
		// the dash is the way to type the name of the action, and the name is the same either way
		{[]string{"dns", "set", "wrong-answer", "--device", "esp32-42", "--name", "wrong.example.com", "--answer", "10.9.0.1", "--answer", "10.9.0.2"},
			map[string]any{"action": "wrong_answer", "answers": []any{"10.9.0.1", "10.9.0.2"}, "names": []any{"wrong.example.com"}}},
		{[]string{"dns", "set", "short-ttl", "--global", "--answer-ttl", "0s", "--name", "ttl.example.com"},
			map[string]any{"action": "short_ttl", "ttl": "0s", "names": []any{"ttl.example.com"}}},
		{[]string{"dns", "set", "truncate", "--device", "esp32-42", "--name", "big.example.com"}, map[string]any{"action": "truncate", "names": []any{"big.example.com"}}},
		{[]string{"dns", "set", "timeout", "--device", "esp32-42", "--name", "slow.example.com", "--lease", "10m"}, map[string]any{"action": "timeout", "names": []any{"slow.example.com"}}},
	} {
		r := g.ctl(tc.args...)
		if r.code != 0 {
			t.Fatalf("%v: %+v", tc.args, r)
		}
		ov := overlayOf(t, g, r.out)
		if ov["kind"] != "dns" || !reflect.DeepEqual(ov["dns"], tc.want) {
			t.Errorf("%v made %v, want %v", tc.args, ov["dns"], tc.want)
		}
		ids = append(ids, ov["id"].(string))
	}
	// the TTL and the lease are the overlay's
	first := g.do("GET", "/overlays/"+ids[0], nil, nil, nil).json(t)
	if first["ttl"] != "2m" || first["expires_at"] == nil || first["target"].(map[string]any)["device"] != espID {
		t.Errorf("%v", first)
	}
	if last := g.do("GET", "/overlays/"+ids[len(ids)-1], nil, nil, nil).json(t); last["lease"] != "10m" {
		t.Errorf("%v", last)
	}
	// the same names set again replace the fault: one overlay, the new action
	r := g.ctl("dns", "set", "servfail", "--device", "esp32-42", "--name", "broker.example.com")
	if r.code != 0 || strings.TrimSpace(r.out) != ids[0] {
		t.Errorf("the same key keeps the overlay: %+v, want %s", r, ids[0])
	}
	if ov := g.do("GET", "/overlays/"+ids[0], nil, nil, nil).json(t); ov["dns"].(map[string]any)["action"] != "servfail" {
		t.Errorf("%v", ov)
	}
	// json prints the overlay, which the command line of a job can read
	if r := g.ctl("dns", "set", "nxdomain", "--network", "IoT", "--name", "j.example.com", "--json"); r.code != 0 || !strings.Contains(r.out, `"kind": "dns"`) {
		t.Errorf("%+v", r)
	} else {
		ids = append(ids, overlayIDsOfKind(t, g, "dns")...)
	}

	// what the command line can see without the gateway
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"dns", "set", "nxdomain"}, "exactly one target"},
		{[]string{"dns", "set", "nonsense", "--device", "esp32-42"}, "is no DNS action"},
		{[]string{"dns", "set", "delay", "--device", "esp32-42"}, "the action delay needs --delay"},
		{[]string{"dns", "set", "wrong-answer", "--device", "esp32-42"}, "the action wrong_answer needs --answer"},
		{[]string{"dns", "set", "short-ttl", "--device", "esp32-42"}, "the action short_ttl needs --answer-ttl"},
		{[]string{"dns", "set", "nxdomain", "--device", "esp32-42", "--delay", "2s"}, "does not use --delay"},
		{[]string{"dns", "set", "delay", "--device", "esp32-42", "--delay", "2s", "--answer", "10.9.0.1"}, "does not use --answer"},
		{[]string{"dns", "set", "nxdomain", "--device", "esp32-42", "--ttl", "soon"}, "--ttl"},
		{[]string{"dns", "set"}, "accepts 1 arg"},
	} {
		if r := g.ctl(tc.args...); r.code != 2 || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want exit 2 and %q", tc.args, r, tc.want)
		}
	}
	// what only the gateway knows is its refusal with the pointer
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"dns", "set", "nxdomain", "--device", "esp32-42", "--name", "not a name!"}, "/dns/names/0"},
		{[]string{"dns", "set", "delay", "--device", "esp32-42", "--delay", "0s"}, "/dns/delay"},
		{[]string{"dns", "set", "wrong-answer", "--device", "esp32-42", "--answer", "0.0.0.0"}, "/dns/answers/0"},
		{[]string{"dns", "set", "nxdomain", "--device", "no-such-device"}, "validation_failed"},
	} {
		g.badRequest = true
		r := g.ctl(tc.args...)
		g.badRequest = false
		if r.code != 2 || !strings.Contains(r.err, "422") || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want 422 and %q", tc.args, r, tc.want)
		}
	}
	if r := g.ctl(append([]string{"overlay", "delete"}, overlayIDsOfKind(t, g, "dns")...)...); r.code != 0 || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v, left %v", r, overlayIDs(t, g))
	}
}

// overlayIDsOfKind lists the ids of the overlays of one kind.
func overlayIDsOfKind(t *testing.T, g *gw, kind string) []string {
	t.Helper()
	var ids []string
	for _, o := range g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any) {
		if m := o.(map[string]any); m["kind"] == kind {
			ids = append(ids, m["id"].(string))
		}
	}
	return ids
}

func ctlMakesTLSCases(t *testing.T, g *gw, clk *clock.Fake) {
	for _, tc := range []struct {
		args []string
		want map[string]any
	}{
		{[]string{"tls", "set", "expired", "--device", "esp32-42", "--port", "8883", "--sni", "broker.example.com", "--to", "203.0.113.0/24", "--cut-existing", "--ttl", "1m"},
			map[string]any{"case": "expired", "ports": []any{8883.0}, "sni": []any{"broker.example.com"}, "destination": map[string]any{"cidr": "203.0.113.0/24"}, "cut_existing": true}},
		{[]string{"tls", "set", "untrusted-ca", "--network", "IoT"}, map[string]any{"case": "untrusted_ca"}},
		{[]string{"tls", "set", "handshake-stall", "--device", "esp32-42", "--port", "443,8883", "--to", "uplink"},
			map[string]any{"case": "handshake_stall", "ports": []any{443.0, 8883.0}, "destination": map[string]any{"uplink": true}}},
		{[]string{"tls", "set", "hostname_mismatch", "--global", "--sni", "*.example.com"}, map[string]any{"case": "hostname_mismatch", "sni": []any{"*.example.com"}}},
	} {
		r := g.ctl(tc.args...)
		if r.code != 0 {
			t.Fatalf("%v: %+v", tc.args, r)
		}
		ov := overlayOf(t, g, r.out)
		if ov["kind"] != "tls" || !reflect.DeepEqual(ov["tls"], tc.want) {
			t.Errorf("%v made %v, want %v", tc.args, ov["tls"], tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tls", "set", "expired"}, "exactly one target"},
		{[]string{"tls", "set", "nonsense", "--device", "esp32-42"}, "is no TLS case"},
		{[]string{"tls", "set", "intercept", "--device", "esp32-42"}, "overlay create -f"},
		{[]string{"tls", "set", "expired", "--device", "esp32-42", "--port", "x"}, "--port"},
	} {
		if r := g.ctl(tc.args...); r.code != 2 || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want exit 2 and %q", tc.args, r, tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tls", "set", "expired", "--device", "esp32-42", "--port", "70000"}, "/tls/ports/0"},
		{[]string{"tls", "set", "expired", "--device", "esp32-42", "--sni", "not a name!"}, "/tls/sni/0"},
		{[]string{"tls", "set", "expired", "--device", "esp32-42", "--to", "10.0.0"}, "/tls/destination"},
	} {
		g.badRequest = true
		r := g.ctl(tc.args...)
		g.badRequest = false
		if r.code != 2 || !strings.Contains(r.err, "422") || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want 422 and %q", tc.args, r, tc.want)
		}
	}
	// the case with its rules is made from a document, which the same command line can delete
	f := writeFile(t, "intercept.yaml", "target: {device: esp32-42}\ntls: {case: intercept, ports: [443], intercept: {http_rules: [{action: status, status: 503}]}}\n")
	if r := g.ctl("overlay", "create", "-f", f); r.code != 0 {
		t.Errorf("%+v", r)
	}
	if r := g.ctl(append([]string{"overlay", "delete"}, overlayIDsOfKind(t, g, "tls")...)...); r.code != 0 || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v, left %v", r, overlayIDs(t, g))
	}
}

func ctlMakesDHCPActions(t *testing.T, g *gw, clk *clock.Fake) {
	for _, tc := range []struct {
		args []string
		want map[string]any
	}{
		{[]string{"dhcp", "set", "short-lease", "--network", "IoT", "--lease-time", "7s", "--ttl", "5m"}, map[string]any{"action": "short_lease", "lease_time": "7s"}},
		{[]string{"dhcp", "set", "delete-lease", "--device", "esp32-42"}, map[string]any{"action": "delete_lease"}},
		{[]string{"dhcp", "set", "silence", "--device", "esp32-42"}, map[string]any{"action": "silence"}},
		{[]string{"dhcp", "set", "set-options", "--device", "esp32-42", "--router", "10.10.0.9", "--dns-server", "192.0.2.53,192.0.2.54", "--ntp-server", "192.0.2.123", "--option", "66=tftp.lab", "--option", "67=boot.img", "--no-domain"},
			map[string]any{"action": "set_options", "options": map[string]any{"router": "10.10.0.9", "dns_servers": []any{"192.0.2.53", "192.0.2.54"}, "ntp_servers": []any{"192.0.2.123"}, "domain": "",
				"custom": []any{map[string]any{"code": 66.0, "value": "tftp.lab"}, map[string]any{"code": 67.0, "value": "boot.img"}}}}},
		{[]string{"dhcp", "set", "set-options", "--network", "IoT", "--domain", "lab.example"}, map[string]any{"action": "set_options", "options": map[string]any{"domain": "lab.example"}}},
	} {
		r := g.ctl(tc.args...)
		if r.code != 0 {
			t.Fatalf("%v: %+v", tc.args, r)
		}
		ov := overlayOf(t, g, r.out)
		if ov["kind"] != "dhcp" || !reflect.DeepEqual(ov["dhcp"], tc.want) {
			t.Errorf("%v made %v, want %v", tc.args, ov["dhcp"], tc.want)
		}
	}

	// the address the gateway chooses for a forced new IP is said on standard error, and is the one of the overlay
	r := g.ctl("dhcp", "set", "force-new-ip", "--device", "esp32-42", "--ttl", "10m")
	if r.code != 0 || !strings.Contains(r.err, "moves to 10.10.0.254") {
		t.Fatalf("%+v", r)
	}
	if ov := overlayOf(t, g, r.out); ov["dhcp"].(map[string]any)["ip"] != "10.10.0.254" || ov["state"] != "effective" {
		t.Errorf("%v", ov)
	}
	// or the one that is named
	r = g.ctl("dhcp", "set", "force-new-ip", "--device", "esp32-42", "--ip", "10.10.0.77", "--json")
	if r.code != 0 || !strings.Contains(r.err, "moves to 10.10.0.77") || !strings.Contains(r.out, `"ip": "10.10.0.77"`) {
		t.Errorf("%+v", r)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"dhcp", "set", "silence"}, "exactly one target"},
		{[]string{"dhcp", "set", "silence", "--group", "sensors"}, "unknown flag"},
		{[]string{"dhcp", "set", "silence", "--global"}, "unknown flag"},
		{[]string{"dhcp", "set", "nonsense", "--device", "esp32-42"}, "is no DHCP action"},
		{[]string{"dhcp", "set", "force-new-ip", "--network", "IoT"}, "name the device with --device"},
		{[]string{"dhcp", "set", "short-lease", "--device", "esp32-42"}, "needs --lease-time"},
		{[]string{"dhcp", "set", "set-options", "--device", "esp32-42"}, "needs one of --router"},
		{[]string{"dhcp", "set", "set-options", "--device", "esp32-42", "--domain", "x.test", "--no-domain"}, "exclude each other"},
		{[]string{"dhcp", "set", "set-options", "--device", "esp32-42", "--option", "sixty-six"}, "is not code=value"},
		{[]string{"dhcp", "set", "silence", "--device", "esp32-42", "--lease-time", "7s"}, "does not use --lease-time"},
		{[]string{"dhcp", "set", "short-lease", "--device", "esp32-42", "--lease-time", "7s", "--router", "10.10.0.9"}, "does not use --router"},
	} {
		if r := g.ctl(tc.args...); r.code != 2 || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want exit 2 and %q", tc.args, r, tc.want)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"dhcp", "set", "short-lease", "--device", "esp32-42", "--lease-time", "0s"}, "/dhcp/lease_time"},
		{[]string{"dhcp", "set", "force-new-ip", "--device", "esp32-42", "--ip", "192.0.2.7"}, "/dhcp/ip"},
		{[]string{"dhcp", "set", "set-options", "--device", "esp32-42", "--option", "15=x.test"}, "15"},
	} {
		g.badRequest = true
		r := g.ctl(tc.args...)
		g.badRequest = false
		if r.code != 2 || !strings.Contains(r.err, "422") || !strings.Contains(r.err, tc.want) {
			t.Errorf("%v: %+v, want 422 and %q", tc.args, r, tc.want)
		}
	}
	if r := g.ctl(append([]string{"overlay", "delete"}, overlayIDsOfKind(t, g, "dhcp")...)...); r.code != 0 || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v, left %v", r, overlayIDs(t, g))
	}
}
