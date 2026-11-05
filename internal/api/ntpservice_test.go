package api_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/ntpserver"
)

// The NTP server over the API (plan §2.18, M34): its configuration, the log of its queries and what the API makes of both:
// the device of a query, the counter of the fault that shaped it, the events and their limit, the health of the component.

// The service token is the only credential of these paths: a full token must not read or feed the NTP server's interface.
func TestTheNTPServiceEndpointsAreForTheServiceTokenOnly(t *testing.T) {
	g := ready(t)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/internal/ntp/config", nil},
		{"POST", "/internal/ntp/queries", []any{}},
	} {
		if r := g.do(tc.method, tc.path, tc.body, nil, nil); r.Status != 403 {
			t.Errorf("%s %s with a full token: %d %s", tc.method, tc.path, r.Status, r.Body)
		}
	}
}

// ntpEntry is an entry of the server's query log.
func ntpEntry(client string, port int, at time.Time, extra map[string]any) map[string]any {
	m := map[string]any{"time": at.UTC().Format(time.RFC3339Nano), "client": client, "port": port, "version": 4}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func (g *gw) postNTP(entries ...map[string]any) resp {
	g.t.Helper()
	body := make([]any, len(entries))
	for i, e := range entries {
		body[i] = e
	}
	return g.do("POST", "/internal/ntp/queries", body, nil, nil)
}

func (g *gw) mustPostNTP(entries ...map[string]any) {
	g.t.Helper()
	if r := g.postNTP(entries...); r.Status != 204 {
		g.t.Fatalf("%d %s", r.Status, r.Body)
	}
}

func ntpConfigOf(t *testing.T, g *gw) map[string]any {
	t.Helper()
	r := g.do("GET", "/internal/ntp/config", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	return r.json(t)
}

// drainEvents collects the events of the types that arrive until the stream is quiet for a moment (the handlers send
// theirs before they answer, so a quiet stream after the answer is all there is).
func drainEvents(ch <-chan engine.Event, types ...string) []engine.Event {
	var out []engine.Event
	for {
		select {
		case ev := <-ch:
			for _, typ := range types {
				if ev.Type == typ {
					out = append(out, ev)
				}
			}
		case <-time.After(250 * time.Millisecond):
			return out
		}
	}
}

func faultsOfConfig(cfg map[string]any) []map[string]any {
	var out []map[string]any
	for _, f := range cfg["faults"].([]any) {
		out = append(out, f.(map[string]any))
	}
	return out
}

func TestTheNTPConfigurationHoldsTheNetworksTheFaultsAndThePorts(t *testing.T) {
	defer api.SetNTPPoll(300*time.Millisecond, 20*time.Millisecond)()
	g, admin := tlsGateway(t)
	g.asAdmin(admin, func() {
		if r := g.do("GET", "/internal/ntp/config", nil, nil, nil); r.Status != 403 {
			t.Errorf("an admin token: %d", r.Status)
		}
	})

	cfg := ntpConfigOf(t, g)
	if len(faultsOfConfig(cfg)) != 0 || cfg["generation"].(float64) <= 0 {
		t.Fatalf("a gateway without a time fault: %v", cfg)
	}
	var iot map[string]any
	for _, n := range cfg["networks"].([]any) {
		if m := n.(map[string]any); m["network"] == iotID {
			iot = m
		}
	}
	if iot == nil || iot["gateway"] != "10.10.0.1" || iot["clients"] != "10.10.0.0/24" {
		t.Errorf("the networks, the IoT one with its gateway address and client range: %v", cfg["networks"])
	}
	if l := fmt.Sprint(cfg["listen"]); l != "[123]" {
		t.Errorf("without a destination selector the server listens on the NTP port only: %s", l)
	}

	var dev, sel map[string]any
	g.asAdmin(admin, func() {
		dev = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"+400d"}}`)
		sel = g.mustCreateOverlay(`{"target":{"network":"IoT"},"time":{"absolute":"1970-01-01T00:00:00Z","destination":{"cidr":"203.0.113.7/32"}}}`)
	})
	cfg = ntpConfigOf(t, g)
	if l := fmt.Sprint(cfg["listen"]); l != "[123 12301]" {
		t.Errorf("a selector gets a port of its own: %s", l)
	}
	var devOn123, selOn12301 bool
	for _, f := range faultsOfConfig(cfg) {
		switch f["overlay"] {
		case dev["id"]:
			// the device's own fault decides every port its clients can arrive on (nothing narrows it), for the device alone
			want, _ := time.Parse(time.RFC3339Nano, dev["updated_at"].(string))
			got, _ := time.Parse(time.RFC3339Nano, f["since"].(string))
			if f["offset"] != "+400d" || !got.Equal(want) || fmt.Sprint(f["sources"]) != "[10.10.0.31/32]" {
				t.Errorf("the device's entry: %v (updated %v)", f, dev["updated_at"])
			}
			devOn123 = devOn123 || f["port"] == float64(123)
		case sel["id"]:
			if f["port"] == float64(123) {
				t.Errorf("a fault for one server decides nothing on the NTP port: %v", f)
			}
			dest, _ := f["destination"].(map[string]any)
			if f["port"] == float64(12301) && f["absolute"] == "1970-01-01T00:00:00Z" && dest["cidr"] == "203.0.113.7/32" {
				selOn12301 = true
			}
		}
	}
	if !devOn123 || !selOn12301 {
		t.Errorf("the entries of the device (%v) and of the selector (%v): %v", devOn123, selOn12301, cfg["faults"])
	}

	// the configuration changes with the overlays: a long poll ends with the new one, and without a change with 204
	gen := cfg["generation"].(float64)
	if r := g.do("GET", fmt.Sprintf("/internal/ntp/config?after=%d", int64(gen)), nil, nil, nil); r.Status != 204 {
		t.Errorf("no change: %d %s", r.Status, r.Body)
	}
	g.asAdmin(admin, func() { g.mustCreateOverlay(`{"target":{"device":"esp32-43"},"time":{"answer":"silence"}}`) })
	next := g.do("GET", fmt.Sprintf("/internal/ntp/config?after=%d", int64(gen)), nil, nil, nil)
	if next.Status != 200 || next.json(t)["generation"].(float64) <= gen {
		t.Errorf("%d %s", next.Status, next.Body)
	}
}

// The health component says whether the server polls: disabled without a service namespace, degraded while it has not
// polled for 60 s or at all, healthy right after a poll. A host clock that is not synchronized is named in the detail and
// does not make the component worse: the server is honest about it.
func TestTheNTPComponentOfTheHealthReportFollowsThePollsOfTheServer(t *testing.T) {
	if comps, _ := healthComponents(t, ready(t)); comps["ntp"] != "disabled" {
		t.Fatalf("ntp without a service namespace: %v", comps)
	}

	clk := clock.NewFake(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	g, admin := tlsGateway(t, func(o *options) { o.apiClock = clk })
	detailOf := func() string {
		var out string
		g.asAdmin(admin, func() {
			for _, c := range g.do("GET", "/system/health", nil, nil, nil).json(t)["components"].([]any) {
				if m := c.(map[string]any); m["name"] == "ntp" {
					out, _ = m["detail"].(string)
				}
			}
		})
		return out
	}
	statusOf := func() string {
		var out string
		g.asAdmin(admin, func() { comps, _ := healthComponents(t, g); out = comps["ntp"] })
		return out
	}
	if got := statusOf(); got != "degraded" || !strings.Contains(detailOf(), "has not polled") {
		t.Fatalf("before the first poll: %s %q", got, detailOf())
	}
	ntpConfigOf(t, g)
	if got := statusOf(); got != "healthy" || detailOf() != "" {
		t.Fatalf("right after a poll: %s %q", got, detailOf())
	}
	clk.Advance(59 * time.Second)
	if got := statusOf(); got != "healthy" {
		t.Errorf("59 s after the poll: %s", got)
	}
	clk.Advance(2 * time.Second)
	if got := statusOf(); got != "degraded" {
		t.Errorf("61 s after the poll: %s", got)
	}
	// the overall status follows: the server that does not poll is degraded, not unhealthy
	g.asAdmin(admin, func() {
		if _, overall := healthComponents(t, g); overall != "degraded" {
			t.Errorf("overall %s", overall)
		}
	})

	g2, admin2 := tlsGateway(t, func(o *options) { o.hostClock = ntpserver.StaticSync{} })
	ntpConfigOf(t, g2)
	g2.asAdmin(admin2, func() {
		for _, c := range g2.do("GET", "/system/health", nil, nil, nil).json(t)["components"].([]any) {
			if m := c.(map[string]any); m["name"] == "ntp" {
				if d, _ := m["detail"].(string); m["status"] != "healthy" || !strings.Contains(d, "host clock is not synchronized") || !strings.Contains(d, "stratum 16") {
					t.Errorf("an unsynchronized host clock: %v", m)
				}
			}
		}
	})
}

func TestACapabilityListsTheTimeFaultsAndTheNTPServer(t *testing.T) {
	g := ready(t)
	feats := fmt.Sprint(g.do("GET", "/capabilities", nil, nil, nil).json(t)["features"])
	for _, f := range []string{"time.faults", "ntp.server"} {
		if !strings.Contains(feats, f) {
			t.Errorf("%s is missing: %s", f, feats)
		}
	}
	// the step type is run since the engine serves time overlays, so a client that reads the list must see it
	if steps := fmt.Sprint(g.do("GET", "/capabilities", nil, nil, nil).json(t)["step_types"]); !strings.Contains(steps, "time") {
		t.Errorf("the step type time is missing: %s", steps)
	}
}

// Every query is added to the log with its device, a query a time fault shaped is counted for the overlay (and for the
// overlay of a profile with a time part), and a query over the rate limit is neither counted for a fault nor a query of the
// device.
func TestEveryQueryIsLoggedWithItsDeviceAndCountedForTheFaultThatShapedIt(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov, prof map[string]any
	g.asAdmin(admin, func() {
		rev := g.mustPatch(map[string]any{"profiles": map[string]any{pFlaky: map[string]any{
			"name": "clock-in-1970", "parts": map[string]any{"time": map[string]any{"absolute": "1970-01-01T00:00:00Z"}}}}})
		if r := g.apply(rev); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		ov = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"+400d"}}`)
		prof = g.mustCreateOverlay(`{"target":{"network":"IoT"},"profile":"clock-in-1970"}`)
	})
	now := time.Now().UTC().Truncate(time.Second)
	served := now.Add(400 * 24 * time.Hour)
	shaped := func(client, fault string, at time.Time) map[string]any {
		return ntpEntry(client, 123, at, map[string]any{"answer": "normal", "fault": fault, "served_time": served.Format(time.RFC3339Nano), "offset_ms": 400 * 24 * 3600 * 1000})
	}
	g.mustPostNTP(
		shaped("10.10.0.31", ov["id"].(string), now),
		shaped("10.10.0.31", ov["id"].(string), now.Add(time.Second)),
		shaped("10.10.0.32", prof["id"].(string), now),
		ntpEntry("10.10.0.99", 123, now, map[string]any{"answer": "normal", "offset_ms": 0}),
		ntpEntry("10.10.0.31", 123, now, map[string]any{"answer": "dropped", "fault": ov["id"]}),
	)

	log := g.srv.NTPLog()
	if len(log) != 5 {
		t.Fatalf("%d entries", len(log))
	}
	for i, wantDev := range []string{espID, espID, espTrustID, "", espID} {
		got := ""
		if log[i].Device != nil {
			got = log[i].Device.String()
		}
		if got != wantDev {
			t.Errorf("entry %d: device %q, want %q", i, got, wantDev)
		}
	}

	d, ok := g.srv.NTPDeviceState(espID)
	if !ok || d.Queries != 2 || d.Dropped != 1 || d.Silenced != 0 || d.Answers[ov["id"].(string)] != 2 || d.LastFault != ov["id"] || d.LastAnswer != "normal" ||
		d.LastServed == nil || !d.LastServed.Equal(served) || d.LastOffsetMs == nil || *d.LastOffsetMs != 400*24*3600*1000 || !d.LastQuery.Equal(now.Add(time.Second)) {
		t.Errorf("esp32-42: %+v", d)
	}
	if d, _ := g.srv.NTPDeviceState(espTrustID); d.Queries != 1 || d.Answers[prof["id"].(string)] != 1 {
		t.Errorf("esp32-43: %+v", d)
	}
	if _, ok := g.srv.NTPDeviceState(strings.ToUpper(espID)); !ok {
		t.Error("a device is found by its id in any case")
	}

	// what a run's wait and check read: the queries of the devices from a time on, without the dropped ones
	if q := g.srv.NTPQueriesOf([]string{espID}, now.Add(time.Second)); len(q) != 1 || !q[0].Time.Equal(now.Add(time.Second)) {
		t.Errorf("queries of esp32-42 from the second on: %+v", q)
	}
	if q := g.srv.NTPQueriesOf([]string{espID, espTrustID}, now); len(q) != 3 {
		t.Errorf("queries of both devices: %d", len(q))
	}

	g.asAdmin(admin, func() {
		// the counter of an overlay is the number of queries it shaped, in packets, with an epoch
		got := g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).json(t)
		if c, _ := got["counters"].(map[string]any); c == nil || c["packets"] != float64(2) || c["bytes"] != float64(0) || c["epoch"] == nil || got["state"] != "effective" {
			t.Errorf("the device's fault: %v", got)
		}
		// the time part of a profile counts for the activation; the state of the activation is that of its part
		got = g.do("GET", "/overlays/"+prof["id"].(string), nil, nil, nil).json(t)
		if c, _ := got["counters"].(map[string]any); c == nil || c["packets"] != float64(1) {
			t.Errorf("the time part of a profile: %v", got)
		}
		// an overlay that went takes its counter with it
		if r := g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil); r.Status != 204 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		again := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"+400d"}}`)
		if c, _ := again["counters"].(map[string]any); c != nil && c["packets"] != float64(0) {
			t.Errorf("a new overlay starts at zero: %v", again["counters"])
		}
	})
}

// A fault for one server is behind a newer fault of the same scope for every server (which decides on that server's port, too):
// it is overridden and names the newer one. The other way round, a fault for every server stays in force for all the others.
func TestATimeFaultBehindANewerOneIsOverriddenAndNamesIt(t *testing.T) {
	g, admin := tlsGateway(t)
	g.token = admin
	forOne := g.mustCreateOverlay(`{"target":{"network":"IoT"},"time":{"offset":"+1h","destination":{"cidr":"203.0.113.7/32"}}}`)
	if forOne["state"] != "effective" {
		t.Fatalf("a time fault of a network is effective while nothing beats it: %v", forOne)
	}
	forAll := g.mustCreateOverlay(`{"target":{"network":"IoT"},"time":{"offset":"-1d"}}`)
	got := g.do("GET", "/overlays/"+forOne["id"].(string), nil, nil, nil).json(t)
	by, _ := got["overridden_by"].([]any)
	if got["state"] != "overridden" || len(by) != 1 || by[0].(map[string]any)["id"] != forAll["id"] {
		t.Errorf("the older fault for one server: %v", got)
	}
	if got := g.do("GET", "/overlays/"+forAll["id"].(string), nil, nil, nil).json(t); got["state"] != "effective" || got["overridden_by"] != nil {
		t.Errorf("the newer fault for every server: %v", got)
	}
	// a newer fault for one server leaves the older one for every server in force for the others
	other := g.mustCreateOverlay(`{"target":{"network":"IoT"},"time":{"offset":"+2h","destination":{"cidr":"198.51.100.5/32"}}}`)
	if got := g.do("GET", "/overlays/"+forAll["id"].(string), nil, nil, nil).json(t); got["state"] != "effective" {
		t.Errorf("the fault for every server behind a newer one for another server: %v", got["state"])
	}
	if other["state"] != "effective" {
		t.Errorf("the newer fault for another server: %v", other["state"])
	}
}

// A query that a time fault shaped announces itself on the event stream, once for each device and fault in 10 s; a device
// that asks is announced for its own query, the same way; and the limit is the API's clock.
func TestTheNTPEventsAreLimitedPerDeviceAndFault(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	g, admin := tlsGateway(t, func(o *options) { o.apiClock = clk })
	var ov map[string]any
	g.asAdmin(admin, func() { ov = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"+400d"}}`) })
	ch, cancel := g.e.Subscribe()
	defer cancel()
	now := clk.Now()
	shaped := func(at time.Time) map[string]any {
		return ntpEntry("10.10.0.31", 123, at, map[string]any{"answer": "normal", "fault": ov["id"], "offset_ms": 1000})
	}
	plain := func(client string, at time.Time) map[string]any {
		return ntpEntry(client, 123, at, map[string]any{"answer": "normal", "offset_ms": 0})
	}
	g.mustPostNTP(shaped(now), shaped(now.Add(time.Second)), shaped(now.Add(2*time.Second)), plain("10.10.0.32", now), plain("10.10.0.32", now))
	if first := drainEvents(ch, "ntp_answered", "ntp_query_seen"); len(first) != 3 {
		t.Fatalf("the first queries of two devices announce themselves once each (and the fault once): %v", first)
	}
	// post again within the window and expect nothing, then past it
	g.mustPostNTP(shaped(now.Add(3 * time.Second)))
	if got := drainEvents(ch, "ntp_answered", "ntp_query_seen"); len(got) != 0 {
		t.Errorf("a second query within 10 s announced itself: %v", got)
	}
	clk.Advance(11 * time.Second)
	g.mustPostNTP(shaped(now.Add(12 * time.Second)))
	got := drainEvents(ch, "ntp_answered", "ntp_query_seen")
	if len(got) != 2 || got[0].Type != "ntp_answered" || got[1].Type != "ntp_query_seen" {
		t.Fatalf("after the window: %v", got)
	}
	d := got[0].Data
	if d["overlay"] != ov["id"] || d["device"] != espID || d["device_name"] != "esp32-42" || d["answer"] != "normal" || d["offset_ms"] != int64(1000) || d["client"] != "10.10.0.31" {
		t.Errorf("ntp_answered: %v", d)
	}
	if sub, ok := d["subject"].(engine.Subject); !ok || sub.Kind != "device" || sub.ID != espID || sub.Name != "esp32-42" {
		t.Errorf("subject: %v", d["subject"])
	}
	if got[1].Data["device"] != espID || got[1].Data["overlay"] != nil {
		t.Errorf("ntp_query_seen: %v", got[1].Data)
	}
}

// A batch counted exactly: one ntp_answered for each fault and client, one ntp_query_seen for each client (a device without a
// fault is announced as a query only; a client that is no device by its address), and none for the repeats.
func TestTheFirstQueriesOfEachDeviceAreAnnouncedOnce(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	g, admin := tlsGateway(t, func(o *options) { o.apiClock = clk })
	var ov map[string]any
	g.asAdmin(admin, func() { ov = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"+400d"}}`) })
	ch, cancel := g.e.Subscribe()
	defer cancel()
	now := clk.Now()
	e1 := ntpEntry("10.10.0.31", 123, now, map[string]any{"answer": "normal", "fault": ov["id"], "offset_ms": 1000})
	e2 := ntpEntry("10.10.0.32", 123, now, map[string]any{"answer": "normal", "offset_ms": 0})
	e3 := ntpEntry("10.10.0.99", 123, now, map[string]any{"answer": "silence", "fault": ov["id"]})
	g.mustPostNTP(e1, e1, e1, e2, e2, e3)
	got := drainEvents(ch, "ntp_answered", "ntp_query_seen")
	count := map[string]int{}
	for _, ev := range got {
		key := ev.Type + "|" + fmt.Sprint(ev.Data["device"]) + "|" + fmt.Sprint(ev.Data["client"])
		count[key]++
	}
	want := map[string]int{
		"ntp_answered|" + espID + "|10.10.0.31":        1,
		"ntp_query_seen|" + espID + "|10.10.0.31":      1,
		"ntp_query_seen|" + espTrustID + "|10.10.0.32": 1,
		"ntp_answered|<nil>|10.10.0.99":                1, // a client that is no device is announced by its address
		"ntp_query_seen|<nil>|10.10.0.99":              1,
	}
	if fmt.Sprint(count) != fmt.Sprint(want) {
		t.Errorf("events %v, want %v", count, want)
	}
}

// A connection to the NTS key-establishment port is announced once for each device, and is not a query: it neither counts
// for the device's queries nor shows in what a run's wait reads.
func TestNTSKeyEstablishmentIsAnnouncedOnceAndIsNoQuery(t *testing.T) {
	g, _ := tlsGateway(t)
	ch, cancel := g.e.Subscribe()
	defer cancel()
	now := time.Now().UTC()
	nts := func(client string, at time.Time) map[string]any {
		return map[string]any{"time": at.Format(time.RFC3339Nano), "client": client, "port": 4460, "kind": "nts_ke"}
	}
	g.mustPostNTP(nts("10.10.0.31", now), nts("10.10.0.31", now.Add(time.Second)), nts("10.10.0.32", now))
	got := drainEvents(ch, "nts_seen")
	if len(got) != 2 || got[0].Data["device"] != espID || got[1].Data["device"] != espTrustID {
		t.Fatalf("%v", got)
	}
	g.mustPostNTP(nts("10.10.0.31", now.Add(time.Minute)))
	if again := drainEvents(ch, "nts_seen"); len(again) != 0 {
		t.Errorf("announced again: %v", again)
	}
	d, ok := g.srv.NTPDeviceState(espID)
	if !ok || d.NTSKE != 3 || d.Queries != 0 {
		t.Errorf("%+v", d)
	}
	if q := g.srv.NTPQueriesOf([]string{espID, espTrustID}, now.Add(-time.Hour)); len(q) != 0 {
		t.Errorf("a key-establishment connection is no query: %v", q)
	}
}

// The events are on the stream in the shape of the spec, in words.
func TestTheStreamTellsTheNTPEventsInWords(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov map[string]any
	g.asAdmin(admin, func() { ov = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"answer":"silence"}}`) })
	g.token = admin
	st := g.openStream("", "?types=ntp_answered,ntp_query_seen,nts_seen")
	g.token = tlsServiceToken(g)
	now := time.Now().UTC()
	g.mustPostNTP(
		ntpEntry("10.10.0.31", 123, now, map[string]any{"answer": "silence", "fault": ov["id"]}),
		map[string]any{"time": now.Format(time.RFC3339Nano), "client": "10.10.0.32", "port": 4460, "kind": "nts_ke"},
	)
	// The stream is read once, in the order the events come: until() skips what is before the event it waits for, so a loop
	// over a map (a random order) would lose the events that are before the one it asked for first.
	words := map[string][]string{
		"ntp_answered":   {"esp32-42", "silence", "shaped"},
		"ntp_query_seen": {"esp32-42", "asked the NTP server"},
		"nts_seen":       {"esp32-43", "NTS"},
	}
	seen := map[string]sseEvent{}
	deadline := time.Now().Add(5 * time.Second)
	for len(seen) < len(words) && time.Now().Before(deadline) {
		ev, ok := st.next(time.Until(deadline))
		if !ok {
			break
		}
		if _, want := words[ev.Event]; want {
			seen[ev.Event] = ev
		}
	}
	for typ, words := range words {
		ev, ok := seen[typ]
		if !ok {
			t.Errorf("no %s", typ)
			continue
		}
		msg, _ := ev.Data["message"].(string)
		for _, w := range words {
			if !strings.Contains(msg, w) {
				t.Errorf("%s: %q does not say %q", typ, msg, w)
			}
		}
		if sub, _ := ev.Data["subject"].(map[string]any); sub["kind"] != "device" {
			t.Errorf("%s: subject %v", typ, ev.Data["subject"])
		}
	}
}

func TestEntriesOfTheNTPLogAreCheckedBeforeTheyAreTaken(t *testing.T) {
	g, _ := tlsGateway(t)
	g.badRequest = true
	defer func() { g.badRequest = false }()
	now := time.Now().UTC()
	for name, e := range map[string]map[string]any{
		"an IPv6 client":    ntpEntry("fd00::1", 123, now, nil),
		"no address":        ntpEntry("esp32-42", 123, now, nil),
		"no time":           {"client": "10.10.0.31", "port": 123},
		"a zero time":       ntpEntry("10.10.0.31", 123, time.Time{}, nil),
		"port zero":         ntpEntry("10.10.0.31", 0, now, nil),
		"a port too large":  ntpEntry("10.10.0.31", 70000, now, nil),
		"an unknown answer": ntpEntry("10.10.0.31", 123, now, map[string]any{"answer": "bogus"}),
		"an unknown kind":   ntpEntry("10.10.0.31", 123, now, map[string]any{"kind": "bogus"}),
		"an unknown field":  ntpEntry("10.10.0.31", 123, now, map[string]any{"bogus": 1}),
	} {
		if r := g.postNTP(e); r.Status != 422 && r.Status != 400 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	// a batch with one bad entry is taken as a whole or not at all
	if r := g.postNTP(ntpEntry("10.10.0.31", 123, now, nil), ntpEntry("10.10.0.31", 0, now, nil)); r.Status != 422 {
		t.Errorf("a batch with a bad entry: %d %s", r.Status, r.Body)
	}
	if n := len(g.srv.NTPLog()); n != 0 {
		t.Errorf("%d entries were kept", n)
	}
	big := make([]any, 10001)
	for i := range big {
		big[i] = map[string]any{"time": now.Format(time.RFC3339), "client": "10.10.0.31", "port": 123}
	}
	if r := g.do("POST", "/internal/ntp/queries", big, nil, nil); r.Status != 400 && r.Status != 422 {
		t.Errorf("10001 entries: %d", r.Status)
	}
}

// ntpFlow is a line of `conntrack -L`: a UDP query of a device to a server that the gateway redirected to the NTP server
// in the service namespace, which answers from the port it received the query on.
func ntpFlow(client string, sport int, dst string, serverPort int) string {
	return fmt.Sprintf("udp      17 28 src=%s dst=%s sport=%d dport=123 packets=1 bytes=76 src=169.254.100.2 dst=%s sport=%d dport=%d packets=1 bytes=76 mark=0 use=1\n",
		client, dst, sport, client, serverPort, sport)
}

// The server asked is read from the observed flows where the NTP server could not tell it (decision D36, best effort): one
// redirected flow of the client names it, several or none leave it unknown, and the flows are read once for a client in
// 10 s, not for every batch.
func TestTheServerAskedIsFilledInFromTheObservedFlowsWhenTheyAreUnambiguous(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	g, _ := tlsGateway(t, func(o *options) { o.apiClock = clk })
	g.k.SetConntrack(
		ntpFlow("10.10.0.31", 40001, "203.0.113.9", 123) +
			ntpFlow("10.10.0.32", 40002, "203.0.113.9", 123) + ntpFlow("10.10.0.32", 40003, "198.51.100.5", 123) +
			// a DNS query is not an NTP query, and a flow of another device is not the client's
			"udp      17 28 src=10.10.0.99 dst=203.0.113.50 sport=40004 dport=53 packets=1 bytes=76 src=169.254.100.2 dst=10.10.0.99 sport=53 dport=40004 packets=1 bytes=76 mark=0 use=1\n")
	now := clk.Now()
	named := ntpEntry("10.10.0.31", 12301, now, map[string]any{"answer": "normal", "server": "203.0.113.7:123"})
	g.mustPostNTP(
		ntpEntry("10.10.0.31", 123, now, map[string]any{"answer": "normal"}),
		ntpEntry("10.10.0.32", 123, now, map[string]any{"answer": "normal"}),
		ntpEntry("10.10.0.99", 123, now, map[string]any{"answer": "normal"}),
		named,
	)
	log := g.srv.NTPLog()
	server := func(i int) string {
		if log[i].Server == nil {
			return ""
		}
		return *log[i].Server
	}
	if server(0) != "203.0.113.9:123" {
		t.Errorf("one redirected flow names the server: %q", server(0))
	}
	if server(1) != "" || server(2) != "" {
		t.Errorf("two flows, or none, leave it unknown: %q %q", server(1), server(2))
	}
	if server(3) != "203.0.113.7:123" {
		t.Errorf("a server the NTP server named stays: %q", server(3))
	}
	if d, _ := g.srv.NTPDeviceState(espID); d.LastServer != "203.0.113.7:123" {
		t.Errorf("the last query of esp32-42 named its server: %q", d.LastServer)
	}
	if d, _ := g.srv.NTPDeviceState(espTrustID); d.LastServer != "an external server (redirected)" {
		t.Errorf("an unknown server is shown as: %q", d.LastServer)
	}

	// within 10 s the answer for the client stands, without another read of the table
	reads := g.k.ConntrackReads()
	g.k.SetConntrack(ntpFlow("10.10.0.31", 40010, "192.0.2.77", 123))
	clk.Advance(5 * time.Second)
	g.mustPostNTP(ntpEntry("10.10.0.31", 123, now.Add(5*time.Second), map[string]any{"answer": "normal"}))
	if g.k.ConntrackReads() != reads {
		t.Errorf("the table was read again within 10 s: %d reads", g.k.ConntrackReads()-reads)
	}
	if s := g.srv.NTPLog()[4].Server; s == nil || *s != "203.0.113.9:123" {
		t.Errorf("within 10 s: %v", s)
	}
	clk.Advance(6 * time.Second)
	g.mustPostNTP(ntpEntry("10.10.0.31", 123, now.Add(11*time.Second), map[string]any{"answer": "normal"}))
	if s := g.srv.NTPLog()[5].Server; s == nil || *s != "192.0.2.77:123" {
		t.Errorf("after 10 s the flows are read again: %v", s)
	}
}

// The networks of the server's configuration are the bridges and the WireGuard interfaces, in an order that does not depend
// on how the engine lists them: the generation follows the content, and a server that polls must not see a new one for a
// list that was only shuffled. The gateway address of a WireGuard network is the reference id of its clients' answers.
func TestTheNTPNetworksAreTheBridgesAndTheWireGuardInterfacesInAStableOrder(t *testing.T) {
	bridges := []compiler.Bridge{
		{NetworkID: "cccccccc-0000-4000-8000-000000000003", Address: netip.MustParsePrefix("10.30.0.1/24")},
		{NetworkID: "aaaaaaaa-0000-4000-8000-000000000001", Address: netip.MustParsePrefix("10.10.0.1/24")},
	}
	wg := []compiler.WGInterface{{NetworkID: "bbbbbbbb-0000-4000-8000-000000000002", Address: netip.MustParsePrefix("10.99.0.1/24")}}
	want := []string{
		"aaaaaaaa-0000-4000-8000-000000000001 10.10.0.1 10.10.0.0/24",
		"bbbbbbbb-0000-4000-8000-000000000002 10.99.0.1 10.99.0.0/24",
		"cccccccc-0000-4000-8000-000000000003 10.30.0.1 10.30.0.0/24",
	}
	got := api.NTPNetworksOf(&engine.Snapshot{Bridges: bridges, WireGuardInterfaces: wg})
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("networks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	bridges[0], bridges[1] = bridges[1], bridges[0]
	if again := api.NTPNetworksOf(&engine.Snapshot{Bridges: bridges, WireGuardInterfaces: wg}); strings.Join(again, "\n") != strings.Join(want, "\n") {
		t.Errorf("the order depends on the order of the bridges: %v", again)
	}
}

// A device behind a router of a network (a downstream route of a bridge, a route of a peer of a tunnel) is served like one of the
// network and has the gateway of the network as the reference id of its answers (plan §2.18), so the configuration lists the
// networks behind it as clients of the network too.
func TestTheNTPNetworksListTheNetworksBehindARouterAsClientsOfTheNetwork(t *testing.T) {
	bridges := []compiler.Bridge{{NetworkID: "aaaaaaaa-0000-4000-8000-000000000001", Address: netip.MustParsePrefix("10.10.0.1/24"),
		Routes: []string{"10.50.0.0/24", "10.60.0.7"}}}
	wg := []compiler.WGInterface{{NetworkID: "bbbbbbbb-0000-4000-8000-000000000002", Address: netip.MustParsePrefix("10.99.0.1/24"),
		Peers: []compiler.WGPeer{{Routes: []string{"192.168.77.0/24"}}}}}
	want := []string{
		"aaaaaaaa-0000-4000-8000-000000000001 10.10.0.1 10.10.0.0/24",
		"aaaaaaaa-0000-4000-8000-000000000001 10.10.0.1 10.50.0.0/24",
		"aaaaaaaa-0000-4000-8000-000000000001 10.10.0.1 10.60.0.7/32",
		"bbbbbbbb-0000-4000-8000-000000000002 10.99.0.1 10.99.0.0/24",
		"bbbbbbbb-0000-4000-8000-000000000002 10.99.0.1 192.168.77.0/24",
	}
	if got := api.NTPNetworksOf(&engine.Snapshot{Bridges: bridges, WireGuardInterfaces: wg}); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("networks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The API keeps the newest ntpLogSize entries of the query log in memory, not more.
func TestTheQueryLogInMemoryKeepsTheNewestEntries(t *testing.T) {
	g, _ := tlsGateway(t)
	now := time.Now().UTC()
	const batch, total = 10000, 20005
	post := func(from, to int) {
		t.Helper()
		var entries []map[string]any
		for i := from; i < to; i++ {
			// the port numbers the entries: entry i has port i+1
			entries = append(entries, ntpEntry("10.10.0.31", i+1, now, nil))
		}
		if r := g.postNTP(entries...); r.Status != 204 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}
	post(0, batch)
	post(batch, 2*batch)
	post(2*batch, total)
	log := g.srv.NTPLog()
	if len(log) != 20000 {
		t.Fatalf("%d entries kept, want 20000", len(log))
	}
	if log[0].Port != total-20000+1 || log[len(log)-1].Port != total {
		t.Errorf("the oldest kept entry is port %d (want %d), the newest port %d (want %d)", log[0].Port, total-20000+1, log[len(log)-1].Port, total)
	}
}

// The conntrack table is read for the servers devices asked at most once a second whatever the number of clients that need a
// lookup, and not at all for an address that is no client of a test network (which is not logged at all): the log takes an entry
// for any address, and a sender of many source addresses must not keep the executor busy with dumps of the table.
func TestManyNewClientsCauseOneReadOfTheFlowsAndAForeignAddressNone(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	g, _ := tlsGateway(t, func(o *options) { o.apiClock = clk })
	g.k.SetConntrack(ntpFlow("10.10.0.31", 40001, "203.0.113.9", 123))
	now := clk.Now()
	reads := g.k.ConntrackReads()
	g.mustPostNTP(ntpEntry("172.31.9.9", 123, now, map[string]any{"answer": "normal"}))
	if n := g.k.ConntrackReads() - reads; n != 0 {
		t.Errorf("an address outside the test networks caused %d reads", n)
	}
	for i := 31; i < 41; i++ {
		g.mustPostNTP(ntpEntry(fmt.Sprintf("10.10.0.%d", i), 123, now, map[string]any{"answer": "normal"}))
	}
	if n := g.k.ConntrackReads() - reads; n != 1 {
		t.Errorf("ten batches of new clients within a second caused %d reads, want 1", n)
	}
	if s := g.srv.NTPLog()[0].Server; s == nil || *s != "203.0.113.9:123" {
		t.Errorf("the client of the flow: %v", s)
	}
	// a client whose flow was not in the shared read is not settled for 10 s: the next batch after the interval asks again
	g.k.SetConntrack(ntpFlow("10.10.0.31", 40001, "203.0.113.9", 123) + ntpFlow("10.10.0.50", 40050, "198.51.100.8", 123))
	g.mustPostNTP(ntpEntry("10.10.0.50", 123, now, map[string]any{"answer": "normal"}))
	if s := g.srv.NTPLog()[10].Server; s != nil {
		t.Errorf("the shared read cannot know a newer flow: %v", *s)
	}
	clk.Advance(2 * time.Second)
	g.mustPostNTP(ntpEntry("10.10.0.50", 123, clk.Now(), map[string]any{"answer": "normal"}))
	if s := g.srv.NTPLog()[11].Server; s == nil || *s != "198.51.100.8:123" {
		t.Errorf("after the interval the table is read again: %v", s)
	}
}

// The runs read the log through the observer: oldest first by the time of the query, whatever the order of arrival (a batch of the
// server arrives late); the answer, the fault, the offset and the server asked are carried over; a query over the rate limit, a
// connection to the key-establishment port and a client no device owns are not queries of a device.
func TestTheObserverOfTheRunsReadsTheQueriesOldestFirstAndLeavesOutWhatIsNoQuery(t *testing.T) {
	g, _ := tlsGateway(t)
	now := time.Now().UTC().Truncate(time.Second)
	fault := "0194f4f0-0000-7000-8000-0000000000aa"
	g.mustPostNTP(
		ntpEntry("10.10.0.31", 123, now.Add(2*time.Second), map[string]any{"answer": "normal", "fault": fault, "offset_ms": -86400000, "server": "203.0.113.7:123"}),
		ntpEntry("10.10.0.31", 123, now.Add(time.Second), map[string]any{"answer": "silence"}),
		ntpEntry("10.10.0.31", 123, now.Add(3*time.Second), map[string]any{"answer": "dropped"}),
		ntpEntry("10.10.0.31", 4460, now.Add(4*time.Second), map[string]any{"kind": "nts_ke"}),
		ntpEntry("10.10.0.99", 123, now.Add(5*time.Second), map[string]any{"answer": "normal"}),
		ntpEntry("10.10.0.32", 123, now.Add(6*time.Second), map[string]any{"answer": "normal"}),
	)
	qs := g.srv.NTPObserverQueries([]string{espID}, now)
	if len(qs) != 2 {
		t.Fatalf("%d queries, want the two of the device that are queries: %+v", len(qs), qs)
	}
	if !qs[0].Time.Equal(now.Add(time.Second)) || qs[0].Answer != "silence" || qs[0].Fault != "" || qs[0].Server != "" || qs[0].OffsetMs != nil || qs[0].Device != espID {
		t.Errorf("the oldest is the one that arrived second: %+v", qs[0])
	}
	if !qs[1].Time.Equal(now.Add(2*time.Second)) || qs[1].Answer != "normal" || qs[1].Fault != fault || qs[1].Server != "203.0.113.7:123" || qs[1].OffsetMs == nil || *qs[1].OffsetMs != -86400000 {
		t.Errorf("the carried fields: %+v", qs[1])
	}
	if got := g.srv.NTPQueriesOf([]string{espID}, now); len(got) != 2 || !got[0].Time.Before(got[1].Time) {
		t.Errorf("oldest first: %+v", got)
	}
	if len(g.srv.NTPObserverQueries([]string{espID}, now.Add(1500*time.Millisecond))) != 1 || len(g.srv.NTPObserverQueries([]string{espID, espTrustID}, now)) != 3 {
		t.Error("since and several devices")
	}
}

// A client no device owns has counters but no history, so a flood of source addresses costs no memory beyond the state of each.
func TestAClientNoDeviceOwnsKeepsNoRecentQueries(t *testing.T) {
	g, _ := tlsGateway(t)
	now := time.Now().UTC()
	var entries []map[string]any
	entries = append(entries, ntpEntry("10.10.0.31", 123, now, nil))
	for i := 100; i < 250; i++ {
		entries = append(entries, ntpEntry(fmt.Sprintf("10.10.0.%d", i), 123, now, nil))
	}
	g.mustPostNTP(entries...)
	if states, recent := g.srv.NTPRetained(); states != 151 || recent != 1 {
		t.Errorf("%d states and %d recent entries, want 151 and the one of the device", states, recent)
	}
}

// A query from an address in a test network that no device owned when it arrived (the gateway had not observed the device yet) is
// the device's once it is observed: the wait and the check (the observer), the log of the device, its counters and its recent
// entries see it, and the state kept for the address is gone.
func TestAQueryLoggedBeforeTheGatewayKnowsTheDeviceIsTheDevicesOnceItIsObserved(t *testing.T) {
	g, admin := tlsGateway(t)
	var ov map[string]any
	g.asAdmin(admin, func() {
		ov = g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"time":{"offset":"+400d"}}`)
	})
	now := time.Now().UTC().Truncate(time.Second)
	// esp32-42 comes up at 10.10.0.33; its first packets are NTP queries and reach the API before the neighbor table has it
	g.mustPostNTP(
		ntpEntry("10.10.0.33", 123, now, map[string]any{"answer": "normal", "fault": ov["id"], "offset_ms": 1}),
		ntpEntry("10.10.0.33", 123, now.Add(time.Second), map[string]any{"answer": "silence"}),
		ntpEntry("10.10.0.33", 123, now.Add(2*time.Second), map[string]any{"answer": "dropped"}),
		ntpEntry("10.10.0.33", 4460, now.Add(3*time.Second), map[string]any{"kind": "nts_ke"}),
	)
	if q := g.srv.NTPQueriesOf([]string{espID}, now); len(q) != 0 {
		t.Fatalf("a client nobody owns is no query of the device: %+v", q)
	}
	if states, _ := g.srv.NTPRetained(); states != 1 {
		t.Fatalf("%d states, want the one of the address", states)
	}

	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31"), nb("10.10.0.33", "02:00:00:00:00:31"), nb("10.10.0.32", "02:00:00:00:00:32")})
	g.observe()

	q := g.srv.NTPObserverQueries([]string{espID}, now)
	if len(q) != 2 || q[0].Device != espID || q[0].Fault != ov["id"] || q[1].Answer != "silence" {
		t.Fatalf("the wait and the check see the queries of the device: %+v", q)
	}
	if got := g.srv.NTPQueriesOf([]string{espID}, now.Add(time.Second)); len(got) != 1 {
		t.Errorf("since: %d queries", len(got))
	}
	g.asAdmin(admin, func() {
		got := g.do("GET", "/devices/"+espID+"/ntp", nil, nil, nil).json(t)
		if got["queries"] != float64(2) || got["silenced"] != float64(1) || got["dropped"] != float64(1) || got["nts_ke"] != float64(1) {
			t.Errorf("the counters: %v", got)
		}
		if a, _ := got["answers"].([]any); len(a) != 1 || a[0].(map[string]any)["fault"] != ov["id"] {
			t.Errorf("the answers: %v", got["answers"])
		}
		if r, _ := got["recent"].([]any); len(r) != 4 || r[0].(map[string]any)["kind"] != "nts_ke" || r[3].(map[string]any)["device"] != espID {
			t.Errorf("recent: %v", got["recent"])
		}
		if last, _ := got["last_query"].(map[string]any); last == nil || last["answer"] != "silence" {
			t.Errorf("the last query: %v", got["last_query"])
		}
		page := g.do("GET", "/ntp/queries?device="+espID, nil, nil, nil).json(t)["items"].([]any)
		if len(page) != 4 {
			t.Errorf("the log of the device: %d entries", len(page))
		}
	})
	if states, _ := g.srv.NTPRetained(); states != 1 {
		t.Errorf("%d states after the address became the device's, want 1", states)
	}
	if _, ok := g.srv.NTPDeviceState("10.10.0.33"); ok {
		t.Error("the state of the address is still there")
	}
}

// A client in a test network that no device owns cannot push the queries of the devices out of the log, and an address in no test
// network is not logged, counted or announced.
func TestUnownedClientsAreBoundedInTheLogAndForeignAddressesAreNotLogged(t *testing.T) {
	g, admin := tlsGateway(t)
	ch, cancel := g.e.Subscribe()
	defer cancel()
	now := time.Now().UTC()
	g.mustPostNTP(ntpEntry("10.10.0.31", 123, now, map[string]any{"answer": "normal"}), ntpEntry("172.31.9.9", 123, now, map[string]any{"answer": "normal"}))
	for from := 0; from < 25000; from += 2500 {
		var entries []map[string]any
		for i := from; i < from+2500; i++ {
			entries = append(entries, ntpEntry("10.10.0.99", i+1, now, map[string]any{"answer": "normal"}))
		}
		g.mustPostNTP(entries...)
	}
	log := g.srv.NTPLog()
	if len(log) != 1+2000 {
		t.Fatalf("%d entries, want the one of the device and 2000 of the clients nobody owns", len(log))
	}
	if log[0].Device == nil || log[0].Port != 123 {
		t.Errorf("the query of the device was pushed out: %+v", log[0])
	}
	if log[1].Port != 25000-2000+1 || log[len(log)-1].Port != 25000 {
		t.Errorf("the newest of the unowned clients are kept: ports %d to %d", log[1].Port, log[len(log)-1].Port)
	}
	for _, e := range log {
		if e.Client == "172.31.9.9" {
			t.Fatal("an address outside the test networks was logged")
		}
	}
	if _, ok := g.srv.NTPDeviceState("172.31.9.9"); ok {
		t.Error("an address outside the test networks was counted")
	}
	for _, ev := range drainEvents(ch, "ntp_answered", "ntp_query_seen") {
		if ev.Data["client"] == "172.31.9.9" {
			t.Errorf("an address outside the test networks was announced: %v", ev.Data)
		}
	}
	g.asAdmin(admin, func() {
		if got := g.do("GET", "/devices/"+espID+"/ntp", nil, nil, nil).json(t)["queries"]; got != float64(1) {
			t.Errorf("queries of the device: %v", got)
		}
	})
}
