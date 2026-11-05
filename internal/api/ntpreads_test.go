package api_test

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// The public reads of the NTP server's state (plan §2.18, M34): the log of its queries and the state of one device.

// The state of a device holds the counters, the answers per fault, the last query with the server asked and its newest log
// entries, newest first; a device that never asked has zeros and no last query; a device that does not exist is a 404.
func TestTheStateOfADeviceShowsItsQueriesTheAnswersPerFaultAndTheLastQuery(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov map[string]any
	g.asAdmin(admin, func() {
		ov = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"+400d"}}`)
	})
	now := time.Now().UTC().Truncate(time.Second)
	served := now.Add(400 * 24 * time.Hour)
	g.mustPostNTP(
		ntpEntry("10.10.0.31", 123, now, map[string]any{"answer": "normal", "offset_ms": 0, "server": "203.0.113.7:123"}),
		ntpEntry("10.10.0.31", 123, now.Add(time.Second), map[string]any{"answer": "silence", "fault": ov["id"]}),
		ntpEntry("10.10.0.31", 123, now.Add(2*time.Second), map[string]any{"answer": "normal", "fault": ov["id"], "offset_ms": 400 * 24 * 3600 * 1000,
			"served_time": served.Add(2 * time.Second).Format(time.RFC3339Nano), "server": "203.0.113.7:123"}),
		ntpEntry("10.10.0.31", 123, now.Add(3*time.Second), map[string]any{"answer": "dropped"}),
		ntpEntry("10.10.0.31", 4460, now.Add(4*time.Second), map[string]any{"kind": "nts_ke"}),
	)

	g.asAdmin(admin, func() {
		r := g.do("GET", "/devices/"+espID+"/ntp", nil, nil, nil)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		got := r.json(t)
		if got["device"] != espID || got["queries"] != float64(3) || got["silenced"] != float64(1) || got["dropped"] != float64(1) || got["nts_ke"] != float64(1) {
			t.Errorf("the counters: %v", got)
		}
		answers, _ := got["answers"].([]any)
		if len(answers) != 1 || answers[0].(map[string]any)["fault"] != ov["id"] || answers[0].(map[string]any)["count"] != float64(2) {
			t.Errorf("the answers per fault: %v", got["answers"])
		}
		last, _ := got["last_query"].(map[string]any)
		if last == nil || last["answer"] != "normal" || last["fault"] != ov["id"] || last["server"] != "203.0.113.7:123" || last["offset_ms"] != float64(400*24*3600*1000) ||
			last["time"] != now.Add(2*time.Second).Format(time.RFC3339Nano) && last["time"] != now.Add(2*time.Second).Format(time.RFC3339) {
			t.Errorf("the last query: %v", last)
		}
		recent, _ := got["recent"].([]any)
		if len(recent) != 5 {
			t.Fatalf("recent: %d entries, want 5: %v", len(recent), recent)
		}
		first, oldest := recent[0].(map[string]any), recent[4].(map[string]any)
		if first["kind"] != "nts_ke" || oldest["answer"] != "normal" || oldest["port"] != float64(123) || first["device"] != espID {
			t.Errorf("the newest entry first: %v ... %v", first, oldest)
		}

		// the id in any case, and the name of the device, find it
		for _, ref := range []string{strings.ToUpper(espID), "esp32-42"} {
			if r := g.do("GET", "/devices/"+ref+"/ntp", nil, nil, nil); r.Status != 200 || r.json(t)["queries"] != float64(3) {
				t.Errorf("%s: %d %s", ref, r.Status, r.Body)
			}
		}

		// a device that never asked has zeros, lists and no last query
		r = g.do("GET", "/devices/"+espTrustID+"/ntp", nil, nil, nil)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		none := r.json(t)
		if none["queries"] != float64(0) || none["silenced"] != float64(0) || none["dropped"] != float64(0) || none["nts_ke"] != float64(0) || none["last_query"] != nil ||
			fmt.Sprint(none["answers"]) != "[]" || fmt.Sprint(none["recent"]) != "[]" {
			t.Errorf("a device that never asked: %v", none)
		}

		if r := g.do("GET", "/devices/no-such-device/ntp", nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
			t.Errorf("an unknown device: %d %s", r.Status, r.Body)
		}
	})
}

// The ring of a device is bounded and keeps the newest entries: a device that polls every second does not push another device's
// history out, which the shared log of 20000 entries would.
func TestTheRecentQueriesOfADeviceAreBoundedAndNotPushedOutByAnotherDevice(t *testing.T) {
	g, admin := tlsGateway(t)
	now := time.Now().UTC().Truncate(time.Second)
	g.mustPostNTP(ntpEntry("10.10.0.32", 123, now, map[string]any{"answer": "normal", "offset_ms": 0}))
	var entries []map[string]any
	for i := 0; i < 120; i++ {
		// the port numbers the entries: entry i has port i+1
		entries = append(entries, ntpEntry("10.10.0.31", i+1, now.Add(time.Duration(i)*time.Second), map[string]any{"answer": "normal", "offset_ms": 0}))
	}
	g.mustPostNTP(entries...)
	g.asAdmin(admin, func() {
		got := g.do("GET", "/devices/"+espID+"/ntp", nil, nil, nil).json(t)
		recent, _ := got["recent"].([]any)
		if got["queries"] != float64(120) || len(recent) != 50 {
			t.Fatalf("queries %v, %d recent entries", got["queries"], len(recent))
		}
		if p := recent[0].(map[string]any)["port"]; p != float64(120) {
			t.Errorf("the newest is port %v, want 120", p)
		}
		if p := recent[49].(map[string]any)["port"]; p != float64(71) {
			t.Errorf("the oldest kept is port %v, want 71", p)
		}
		other := g.do("GET", "/devices/"+espTrustID+"/ntp", nil, nil, nil).json(t)
		if recent, _ := other["recent"].([]any); other["queries"] != float64(1) || len(recent) != 1 {
			t.Errorf("the other device: %v", other)
		}
	})
}

// The log lists newest first, filters by device and by time, pages by cursor, and refuses an unknown device (a bad cursor has a test of its own, a limit out of range is the spec's).
func TestTheNTPQueryLogIsListedNewestFirstAndFilteredByDeviceAndTime(t *testing.T) {
	g, admin := tlsGateway(t)
	now := time.Now().UTC().Truncate(time.Second)
	g.mustPostNTP(
		ntpEntry("10.10.0.31", 1, now, map[string]any{"answer": "normal", "offset_ms": 0}),
		ntpEntry("10.10.0.32", 2, now.Add(time.Second), map[string]any{"answer": "normal", "offset_ms": 0}),
		ntpEntry("10.10.0.31", 3, now.Add(2*time.Second), map[string]any{"answer": "silence"}),
		ntpEntry("10.10.0.99", 4, now.Add(3*time.Second), map[string]any{"answer": "normal", "offset_ms": 0}),
	)
	ports := func(r resp) []float64 {
		t.Helper()
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		var out []float64
		for _, e := range r.json(t)["items"].([]any) {
			out = append(out, e.(map[string]any)["port"].(float64))
		}
		return out
	}
	g.asAdmin(admin, func() {
		if got := fmt.Sprint(ports(g.do("GET", "/ntp/queries", nil, nil, nil))); got != "[4 3 2 1]" {
			t.Errorf("all, newest first: %s", got)
		}
		if got := fmt.Sprint(ports(g.do("GET", "/ntp/queries?device="+espID, nil, nil, nil))); got != "[3 1]" {
			t.Errorf("one device: %s", got)
		}
		if got := fmt.Sprint(ports(g.do("GET", "/ntp/queries?device=esp32-43", nil, nil, nil))); got != "[2]" {
			t.Errorf("one device by name: %s", got)
		}
		since := url.QueryEscape(now.Add(2 * time.Second).Format(time.RFC3339Nano))
		if got := fmt.Sprint(ports(g.do("GET", "/ntp/queries?since="+since, nil, nil, nil))); got != "[4 3]" {
			t.Errorf("since: %s", got)
		}
		// pages follow the cursor
		first := g.do("GET", "/ntp/queries?limit=3", nil, nil, nil)
		next, _ := first.json(t)["next_cursor"].(string)
		if got := fmt.Sprint(ports(first)); got != "[4 3 2]" || next == "" {
			t.Fatalf("first page: %s, cursor %q", got, next)
		}
		if got := fmt.Sprint(ports(g.do("GET", "/ntp/queries?limit=3&cursor="+url.QueryEscape(next), nil, nil, nil))); got != "[1]" {
			t.Errorf("second page: %s", got)
		}
		if r := g.do("GET", "/ntp/queries?device=no-such-device", nil, nil, nil); r.Status != 404 {
			t.Errorf("an unknown device: %d %s", r.Status, r.Body)
		}
	})
}

// A cursor that is none is refused with bad_request before the log is read (a limit out of range is the spec's: the test client
// does not even send it).
func TestTheNTPQueryLogRefusesABadCursor(t *testing.T) {
	g, admin := tlsGateway(t)
	g.mustPostNTP(ntpEntry("10.10.0.31", 1, time.Now().UTC(), nil))
	g.asAdmin(admin, func() {
		for _, q := range []string{"cursor=%25%25not-a-cursor"} {
			if r := g.do("GET", "/ntp/queries?"+q, nil, nil, nil); r.Status != 400 || r.code(t) != "bad_request" {
				t.Errorf("%s: %d %s", q, r.Status, r.Body)
			}
		}
		if r := g.do("GET", "/ntp/queries?limit=500", nil, nil, nil); r.Status != 200 {
			t.Errorf("limit=500: %d %s", r.Status, r.Body)
		}
	})
}

// Pages that follow the cursor through a filtered log hold every matching entry once, newest first, with a next cursor on every
// page but the last (the read stops with the page and does not copy the log).
func TestThePagesOfAFilteredNTPQueryLogHoldEveryEntryOnce(t *testing.T) {
	g, admin := tlsGateway(t)
	now := time.Now().UTC().Truncate(time.Second)
	var entries []map[string]any
	for i := 0; i < 23; i++ {
		client := "10.10.0.31"
		if i%3 == 0 {
			client = "10.10.0.32"
		}
		// the port numbers the entries: entry i has port i+1
		entries = append(entries, ntpEntry(client, i+1, now.Add(time.Duration(i)*time.Second), nil))
	}
	g.mustPostNTP(entries...)
	g.asAdmin(admin, func() {
		for _, tc := range []struct {
			query string
			want  int // entries that match
		}{{"device=" + espID, 15}, {"since=" + url.QueryEscape(now.Add(10*time.Second).Format(time.RFC3339Nano)), 13}, {"", 23}} {
			var ports []int
			cursor, pages := "", 0
			for {
				q := "limit=4&" + tc.query
				if cursor != "" {
					q += "&cursor=" + url.QueryEscape(cursor)
				}
				body := g.do("GET", "/ntp/queries?"+q, nil, nil, nil).json(t)
				items := body["items"].([]any)
				if len(items) > 4 {
					t.Fatalf("%s: %d items on a page of 4", tc.query, len(items))
				}
				for _, e := range items {
					ports = append(ports, int(e.(map[string]any)["port"].(float64)))
				}
				pages++
				next, _ := body["next_cursor"].(string)
				if next == "" {
					break
				}
				if len(items) != 4 || pages > 10 {
					t.Fatalf("%s: a next cursor after %d items on page %d", tc.query, len(items), pages)
				}
				cursor = next
			}
			if len(ports) != tc.want {
				t.Errorf("%q: %d entries, want %d: %v", tc.query, len(ports), tc.want, ports)
			}
			for i := 1; i < len(ports); i++ {
				if ports[i] >= ports[i-1] {
					t.Errorf("%q: not newest first: %v", tc.query, ports)
					break
				}
			}
		}
	})
}

// The answers of a device are listed by fault, in the order of the faults' ids, whatever the order they were shaped in, and only for
// the faults that are still there: the answers for an overlay that was deleted go with it.
func TestTheAnswersOfADeviceAreListedByFaultInOrderAndForgottenWithTheirOverlay(t *testing.T) {
	g, admin := tlsGateway(t)
	var ids []string
	g.asAdmin(admin, func() {
		// three time faults with their own ids (one time fault of a target is replaced by the next)
		for _, target := range []string{`{"device":"esp32-42"}`, `{"device":"esp32-43"}`, `{"network":"IoT"}`} {
			ids = append(ids, g.mustCreateOverlay(`{"target":` + target + `,"time":{"offset":"+400d"}}`)["id"].(string))
		}
	})
	slices.Sort(ids)
	f1, f2, f3 := ids[0], ids[1], ids[2]
	now := time.Now().UTC().Truncate(time.Second)
	g.mustPostNTP(
		ntpEntry("10.10.0.31", 1, now, map[string]any{"answer": "normal", "fault": f3}),
		ntpEntry("10.10.0.31", 2, now, map[string]any{"answer": "normal", "fault": f1}),
		ntpEntry("10.10.0.31", 3, now, map[string]any{"answer": "normal", "fault": f2}),
		ntpEntry("10.10.0.31", 4, now, map[string]any{"answer": "normal", "fault": f3}),
	)
	answers := func() string {
		var got []string
		list, _ := g.do("GET", "/devices/"+espID+"/ntp", nil, nil, nil).json(t)["answers"].([]any)
		for _, a := range list {
			m := a.(map[string]any)
			got = append(got, fmt.Sprintf("%v=%v", m["fault"], m["count"]))
		}
		return strings.Join(got, ",")
	}
	g.asAdmin(admin, func() {
		if got, want := answers(), strings.Join([]string{f1 + "=1", f2 + "=1", f3 + "=2"}, ","); got != want {
			t.Errorf("answers %v, want %v", got, want)
		}
		if r := g.do("DELETE", "/overlays/"+f2, nil, nil, nil); r.Status != 204 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		if got, want := answers(), strings.Join([]string{f1 + "=1", f3 + "=2"}, ","); got != want {
			t.Errorf("after the delete: answers %v, want %v", got, want)
		}
		if d, _ := g.srv.NTPDeviceState(espID); len(d.Answers) != 2 {
			t.Errorf("the state keeps the answers of a deleted overlay: %v", d.Answers)
		}
	})
}
