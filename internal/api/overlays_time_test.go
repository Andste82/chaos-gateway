package api_test

import (
	"slices"
	"strings"
	"testing"
)

// Stage 1 of M34 (D37) describes and validates the time family but does not serve it: a time overlay is refused with
// unsupported_feature and the milestone, and nothing changes. Stage 2 lets the engine carry it out and replaces this test.
func TestATimeOverlayIsRefusedUntilTheServerExists(t *testing.T) {
	g := ready(t)
	g.mintToken("overlays")
	r := g.createOverlay(map[string]any{"target": map[string]any{"device": "esp32-42"}, "time": map[string]any{"offset": "+400d"}})
	if r.Status != 422 || r.code(t) != "unsupported_feature" {
		t.Fatalf("a time overlay: %d %s", r.Status, r.Body)
	}
	if d := r.json(t)["detail"].(string); !strings.Contains(d, "time fault") || !strings.Contains(d, "M34") {
		t.Errorf("the detail does not name the kind and the milestone: %s", d)
	}
	// the milestone is checked first, as it was for the DHCP actions before M23: an invalid body gets the same answer
	for _, body := range []map[string]any{
		{"target": map[string]any{"device": "esp32-42"}, "time": map[string]any{"offset": "+1d", "absolute": "1970-01-01T00:00:00Z"}},
		{"target": map[string]any{"global": true}, "time": map[string]any{"offset": "+1d"}},
	} {
		if r := g.createOverlay(body); r.Status != 422 || r.code(t) != "unsupported_feature" {
			t.Errorf("%v: %d %s", body, r.Status, r.Body)
		}
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("%d overlays", n)
	}
	// the build says what it can do: not yet this
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	// in every list that could promise it: the kind and the family, the step type, the check type, and the features
	for key, banned := range map[string][]string{
		"overlay_kinds": {"time"}, "fault_families": {"time"}, "step_types": {"time"}, "check_types": {"ntp_queried"},
		"features": {"time.faults", "ntp.server"},
	} {
		list, ok := caps[key].([]any)
		if !ok {
			t.Fatalf("the capabilities have no %s: %v", key, caps)
		}
		for _, k := range list {
			if slices.Contains(banned, k.(string)) {
				t.Errorf("the capabilities list %v in %s: %v", k, key, list)
			}
		}
	}
}

// A profile with a time part is defined and shown, but it says that it is not available and why, and its activation is
// refused with the milestone: the NTP server is not in this build (stage 2 of M34 lifts this and replaces the test).
func TestAProfileWithATimePartIsListedAsUnavailableAndItsActivationIsRefused(t *testing.T) {
	g := ready(t)
	rev := g.mustPatch(map[string]any{"profiles": map[string]any{pFlaky: map[string]any{
		"name": "clock-in-1970", "parts": map[string]any{"time": map[string]any{"absolute": "1970-01-01T00:00:00Z"}}}}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("a profile with a time part is stored: %d %s", r.Status, r.Body)
	}
	it := profileItems(t, g)["clock-in-1970"]
	reason, _ := it["unavailable_reason"].(string)
	if it["available"] != false || !strings.Contains(reason, "time part") || !strings.Contains(reason, "M34") {
		t.Errorf("the profile is shown as available or without the reason: %v", it)
	}
	g.mintToken("overlays")
	r := g.createOverlay(map[string]any{"target": map[string]any{"network": "IoT"}, "profile": "clock-in-1970"})
	if r.Status != 422 || r.code(t) != "unsupported_feature" {
		t.Fatalf("the activation: %d %s", r.Status, r.Body)
	}
	if d := r.json(t)["detail"].(string); !strings.Contains(d, "clock-in-1970") || !strings.Contains(d, "M34") {
		t.Errorf("the detail does not name the profile and the milestone: %s", d)
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("%d overlays", n)
	}
}
