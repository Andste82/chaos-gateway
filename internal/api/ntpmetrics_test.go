package api_test

import (
	"testing"
	"time"
)

// The Prometheus series of a time overlay, and of a profile with a time part, are the numbers the REST view of the overlay
// shows: both are read by the same code, so a family that one leaves out would make them disagree.
func TestMetricsCountTheQueriesATimeFaultShapedLikeTheOverlaysViewDoes(t *testing.T) {
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
	shaped := func(client, fault string, at time.Time) map[string]any {
		return ntpEntry(client, 123, at, map[string]any{"answer": "normal", "fault": fault, "offset_ms": 1000})
	}
	ovID, profID := ov["id"].(string), prof["id"].(string)
	g.mustPostNTP(shaped("10.10.0.31", ovID, now), shaped("10.10.0.31", ovID, now.Add(time.Second)), shaped("10.10.0.32", profID, now))

	g.asAdmin(admin, func() {
		m := g.scrape()
		if v, ok := m.find("chaosgw_fault_packets_total", "layer", "overlay", "id", ovID, "kind", "time"); !ok || v != 2 {
			t.Errorf("the time overlay: %v (found %v), want 2", v, ok)
		}
		if v, ok := m.find("chaosgw_fault_packets_total", "layer", "overlay", "id", profID, "kind", "profile"); !ok || v != 1 {
			t.Errorf("the profile with a time part: %v (found %v), want 1", v, ok)
		}
		for id, want := range map[string]float64{ovID: 2, profID: 1} {
			got := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)
			if c, _ := got["counters"].(map[string]any); c == nil || c["packets"] != want {
				t.Errorf("the REST view of %s: %v, want %v packets", id, got["counters"], want)
			}
		}
	})
}
