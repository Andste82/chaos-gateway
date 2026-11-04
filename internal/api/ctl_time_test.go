package api_test

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// M34: `chaosctl time set`, `chaosctl wait ntp` and `chaosctl explain` for the time family against the API with the real
// engine on the simulated kernel (the gateway of the TLS tests: the service namespace, the devices esp32-42 and esp32-43 and
// the group sensors). The flags and the bodies they become are in internal/ctl/appfamilies_test.go.

func TestChaosctlMakesTimeFaultsAndExplainsThem(t *testing.T) {
	g, _, admin := timeRunGateway(t)
	g.asAdmin(admin, func() {
		var ids []string
		for _, tc := range []struct {
			args []string
			want map[string]any
		}{
			{[]string{"time", "set", "offset", "--offset", "+400d", "--device", "esp32-42", "--ttl", "2m"}, map[string]any{"offset": "+400d"}},
			{[]string{"time", "set", "absolute", "--absolute", "1970-01-01T00:00:00Z", "--network", "IoT"}, map[string]any{"absolute": "1970-01-01T00:00:00Z"}},
			{[]string{"time", "set", "skew", "--skew-ppm", "500", "--offset", "-90s", "--group", "sensors"}, map[string]any{"skew_ppm": 500.0, "offset": "-90s"}},
			{[]string{"time", "set", "leap", "--leap", "insert", "--absolute", "2016-12-31T23:55:00Z", "--device", "esp32-43"}, map[string]any{"leap": "insert", "absolute": "2016-12-31T23:55:00Z"}},
			{[]string{"time", "set", "unsynchronized", "--device", "esp32-43", "--to", "198.51.100.0/24"},
				map[string]any{"answer": "unsynchronized", "destination": map[string]any{"cidr": "198.51.100.0/24"}}},
			// the dash is the way to type the name of the fault, and the name is the same either way
			{[]string{"time", "set", "kiss-of-death", "--kiss-code", "rate", "--device", "esp32-42", "--to", "time.example.org"},
				map[string]any{"answer": "kiss_of_death", "kiss_code": "RATE", "destination": map[string]any{"hostname": "time.example.org"}}},
			{[]string{"time", "set", "silence", "--device", "esp32-42", "--to", "203.0.113.7", "--lease", "10m"},
				map[string]any{"answer": "silence", "destination": map[string]any{"cidr": "203.0.113.7/32"}}},
			{[]string{"time", "set", "invalid", "--invalid", "origin-mismatch", "--network", "IoT", "--to", "203.0.113.9", "--port", "123,5123"},
				map[string]any{"answer": "origin_mismatch", "destination": map[string]any{"cidr": "203.0.113.9/32"}, "ports": []any{123.0, 5123.0}}},
		} {
			r := g.ctl(tc.args...)
			if r.code != 0 {
				t.Fatalf("%v: %+v", tc.args, r)
			}
			ov := overlayOf(t, g, r.out)
			if ov["kind"] != "time" || !reflect.DeepEqual(ov["time"], tc.want) {
				t.Errorf("%v made %v, want %v", tc.args, ov["time"], tc.want)
			}
			ids = append(ids, ov["id"].(string))
		}
		// the TTL and the lease are the overlay's
		first := g.do("GET", "/overlays/"+ids[0], nil, nil, nil).json(t)
		if first["ttl"] != "2m" || first["expires_at"] == nil || first["target"].(map[string]any)["device"] != espID {
			t.Errorf("%v", first)
		}
		if ov := g.do("GET", "/overlays/"+ids[6], nil, nil, nil).json(t); ov["lease"] != "10m" {
			t.Errorf("%v", ov)
		}
		// the same selection set again replaces the fault: one overlay, the new fault
		if r := g.ctl("time", "set", "offset", "--offset", "-1d", "--device", "esp32-42"); r.code != 0 || strings.TrimSpace(r.out) != ids[0] {
			t.Errorf("the same key keeps the overlay: %+v, want %s", r, ids[0])
		}
		if ov := g.do("GET", "/overlays/"+ids[0], nil, nil, nil).json(t); ov["time"].(map[string]any)["offset"] != "-1d" {
			t.Errorf("%v", ov)
		}
		// json prints the overlay
		if r := g.ctl("time", "set", "silence", "--group", "sensors", "--json"); r.code != 0 || !strings.Contains(r.out, `"kind": "time"`) {
			t.Errorf("%+v", r)
		}

		// explain names the time fault that wins for the tuple of an NTP query, and not for another port
		r := g.ctl("explain", "--device", "esp32-42", "--dst", "203.0.113.7", "--protocol", "udp", "--port", "123")
		if r.code != 0 || !strings.Contains(r.out, "time: ") || !strings.Contains(r.out, "silence") {
			t.Errorf("explain: %+v", r)
		}
		if r := g.ctl("explain", "--device", "esp32-42", "--dst", "203.0.113.7", "--protocol", "udp", "--port", "5683"); r.code != 0 || strings.Contains(r.out, "time: ") {
			t.Errorf("explain on another port: %+v", r)
		}

		// what the command line can see without the gateway
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"time", "set", "silence"}, "exactly one target"},
			{[]string{"time", "set", "silence", "--global"}, "unknown flag: --global"},
			{[]string{"time", "set", "silence", "--device", "esp32-42", "--network", "IoT"}, "exactly one target: --device, --group or --network"},
			{[]string{"time", "set", "nonsense", "--device", "esp32-42"}, "is no time fault"},
			{[]string{"time", "set", "offset", "--device", "esp32-42"}, "the fault offset needs --offset"},
			{[]string{"time", "set", "offset", "--device", "esp32-42", "--offset", "+1d", "--absolute", "1970-01-01T00:00:00Z"}, "exclude each other"},
			{[]string{"time", "set", "silence", "--device", "esp32-42", "--offset", "+1d"}, "does not use --offset"},
			{[]string{"time", "set", "kiss-of-death", "--device", "esp32-42", "--kiss-code", "STOP"}, "is one of RATE, DENY, RSTR"},
			{[]string{"time", "set", "offset", "--device", "esp32-42", "--offset", "+1d", "--ttl", "soon"}, "--ttl"},
			{[]string{"time", "set"}, "accepts 1 arg"},
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
			{[]string{"time", "set", "offset", "--device", "esp32-42", "--offset", "soon"}, "/time/offset"},
			{[]string{"time", "set", "absolute", "--device", "esp32-42", "--absolute", "1800-01-01T00:00:00Z"}, "/time/absolute"},
			{[]string{"time", "set", "offset", "--device", "esp32-42", "--offset", "+1d", "--to", "203.0.113.7", "--port", "53"}, "/time/ports"},
			{[]string{"time", "set", "silence", "--device", "no-such-device"}, "validation_failed"},
		} {
			g.badRequest = true
			r := g.ctl(tc.args...)
			g.badRequest = false
			if r.code != 2 || !strings.Contains(r.err, "422") || !strings.Contains(r.err, tc.want) {
				t.Errorf("%v: %+v, want 422 and %q", tc.args, r, tc.want)
			}
		}
		if r := g.ctl(append([]string{"overlay", "delete"}, overlayIDsOfKind(t, g, "time")...)...); r.code != 0 || len(overlayIDs(t, g)) != 0 {
			t.Errorf("%+v, left %v", r, overlayIDs(t, g))
		}
	})
}

// feedNTP posts a query of esp32-42 every few milliseconds, stamped with the time of the machine (the gateway of the test runs on
// it), until the job ends. The wait starts at the time the gateway names in its first answer, to the nanosecond, so a query
// posted before a wait starts, such as the last of the feeder of the wait before, does not end it.
func (g *gw) feedNTP(svc string, j *ctlJob, extra map[string]any) {
	g.t.Helper()
	for {
		select {
		case <-j.done:
			g.t.Fatal("the job ended before the test could read its result")
		default:
		}
		g.asService(svc, func() { g.mustPostNTP(ntpEntry("10.10.0.31", 123, time.Now().UTC(), extra)) })
		time.Sleep(5 * time.Millisecond)
		if j.out.String() != "" || j.err.String() != "" {
			return
		}
	}
}

// The wait ends at the first query of the device after it began, whatever the answer, and takes the time of the gateway's own
// log as the measure; queries of another device, the ones over the rate limit and NTS connections do not end it.
func TestChaosctlWaitsForTheNtpQueryOfADevice(t *testing.T) {
	// the real clock: the wait starts at the time the gateway names, and the entries below are stamped with the clock of the machine
	g, admin := tlsGateway(t)
	svc := g.token
	// an older query of the device is not the one waited for
	g.mustPostNTP(ntpEntry("10.10.0.31", 123, time.Now().UTC().Add(-time.Hour), map[string]any{"answer": "normal", "offset_ms": 0}))

	var j *ctlJob
	g.asAdmin(admin, func() { j = g.startCtl(nil, "wait", "ntp", "esp32-42", "--timeout", "30s") })
	g.feedNTP(svc, j, map[string]any{"answer": "silence", "server": "203.0.113.7:123"})
	r := j.wait(t)
	if r.code != 0 || !strings.Contains(r.out, "device esp32-42 asked the NTP server after ") || !strings.Contains(r.out, "answer silence") || !strings.Contains(r.out, "server 203.0.113.7:123") {
		t.Errorf("%+v", r)
	}

	// a query that the server shaped: the answer, the time served, the offset and the fault are said
	g.asAdmin(admin, func() { j = g.startCtl(nil, "wait", "ntp", "esp32-42", "--timeout", "30s") })
	g.feedNTP(svc, j, map[string]any{"answer": "normal", "fault": "0194f4f0-0000-7000-8000-0000000000aa", "offset_ms": 3600000, "served_time": "2030-01-01T00:00:00Z"})
	r = j.wait(t)
	if r.code != 0 || !strings.Contains(r.out, "answer normal, served 2030-01-01T00:00:00Z, offset 3600000ms, fault 0194f4f0-0000-7000-8000-0000000000aa") {
		t.Errorf("a shaped answer: %+v", r)
	}

	// nothing but what is no query: the wait runs out with 1 and says what it may mean
	g.asAdmin(admin, func() { j = g.startCtl(nil, "wait", "ntp", "esp32-42", "--timeout", "2s") })
	deadline := time.Now().Add(1500 * time.Millisecond) // well inside the timeout, so a slow first poll cannot turn the 1 into a 2
	at := time.Now().UTC().Add(48 * time.Hour)
	for time.Now().Before(deadline) {
		at = at.Add(time.Second)
		g.mustPostNTP(
			ntpEntry("10.10.0.31", 123, at, map[string]any{"answer": "dropped"}),
			ntpEntry("10.10.0.31", 4460, at, map[string]any{"kind": "nts_ke"}),
			ntpEntry("10.10.0.32", 123, at, map[string]any{"answer": "normal", "offset_ms": 0}),
		)
		time.Sleep(10 * time.Millisecond)
	}
	r = j.wait(t)
	if r.code != 1 || !strings.Contains(r.err, "timed out after 2s") || !strings.Contains(r.err, "did not ask the gateway's NTP server") {
		t.Errorf("a device that asked nothing: %+v", r)
	}

	// a device the gateway does not know is waited for, and the wait ends with 1 and says so
	g.asAdmin(admin, func() {
		if r := g.ctl("wait", "ntp", "ghost", "--timeout", "300ms"); r.code != 1 || !strings.Contains(r.err, "the gateway does not know the device") {
			t.Errorf("an unknown device: %+v", r)
		}
		for _, args := range [][]string{{"wait", "ntp"}, {"wait", "ntp", "a", "b"}} {
			if r := g.ctl(args...); r.code != 2 {
				t.Errorf("%v: %+v", args, r)
			}
		}
	})
	// a token that may not read is refused, which is no verdict on the device
	g.asService(svc, func() {
		if r := g.ctl("wait", "ntp", "esp32-42", "--timeout", "300ms"); r.code != 2 || !strings.Contains(r.err, "403") {
			t.Errorf("the service token: %+v", r)
		}
	})
}
