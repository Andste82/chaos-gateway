package api_test

import (
	"slices"
	"testing"
)

// The time family (plan §2.18, M34) is carried out by the engine: a time overlay is accepted for a network, a group or a
// device, an invalid one is refused by the validation (not by a milestone), and the capabilities name the kind and the
// family. The overlay is not shown in the capability lists of steps, checks and features before the stages that bring
// them (the runner, the NTP server's API).
func TestATimeOverlayIsAcceptedAndAnInvalidOneIsRefusedByTheValidation(t *testing.T) {
	g := ready(t)
	g.mintToken("overlays")
	ov := g.mustCreateOverlay(map[string]any{"target": map[string]any{"network": "IoT"}, "time": map[string]any{"offset": "+400d"}})
	if ov["kind"] != "time" || ov["time"].(map[string]any)["offset"] != "+400d" {
		t.Fatalf("%v", ov)
	}
	for name, body := range map[string]map[string]any{
		"offset and absolute":     {"target": map[string]any{"network": "IoT"}, "time": map[string]any{"offset": "+1d", "absolute": "1970-01-01T00:00:00Z"}},
		"the global scope":        {"target": map[string]any{"global": true}, "time": map[string]any{"offset": "+1d"}},
		"a fault that is nothing": {"target": map[string]any{"network": "IoT"}, "time": map[string]any{}},
	} {
		r := g.createOverlay(body)
		if r.Status != 422 || r.code(t) != "validation_failed" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 1 {
		t.Errorf("%d overlays", n)
	}
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	for key, want := range map[string]string{"overlay_kinds": "time", "fault_families": "time"} {
		list, _ := caps[key].([]any)
		if !slices.Contains(list, any(want)) {
			t.Errorf("the capabilities have no %s in %s: %v", want, key, list)
		}
	}
}

// A profile with a time part is available since the NTP server is in the build, and its activation is accepted on a
// network, a group or a device and refused on the global scope by the validation.
func TestAProfileWithATimePartIsAvailableAndItsActivationIsAccepted(t *testing.T) {
	g := ready(t)
	rev := g.mustPatch(map[string]any{"profiles": map[string]any{pFlaky: map[string]any{
		"name": "clock-in-1970", "parts": map[string]any{"time": map[string]any{"absolute": "1970-01-01T00:00:00Z"}}}}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("a profile with a time part is stored: %d %s", r.Status, r.Body)
	}
	it := profileItems(t, g)["clock-in-1970"]
	if it["available"] != true || it["unavailable_reason"] != nil {
		t.Errorf("the profile is shown as unavailable: %v", it)
	}
	g.mintToken("overlays")
	r := g.createOverlay(map[string]any{"target": map[string]any{"network": "IoT"}, "profile": "clock-in-1970"})
	if r.Status != 201 {
		t.Fatalf("the activation: %d %s", r.Status, r.Body)
	}
	// the part is a fault of the family time (partsOf): the activation wins the family on its scope until a newer time
	// fault of the same scope takes it over
	id := r.json(t)["id"].(string)
	stateOf := func() any {
		return g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)["state"]
	}
	if got := stateOf(); got != "effective" {
		t.Errorf("the state of the activation: %v", got)
	}
	g.mustCreateOverlay(map[string]any{"target": map[string]any{"network": "IoT"}, "time": map[string]any{"offset": "+1d"}})
	if got := stateOf(); got != "overridden" {
		t.Errorf("the state of the activation behind a newer time fault of the scope: %v", got)
	}
	if r := g.createOverlay(map[string]any{"target": map[string]any{"global": true}, "profile": "clock-in-1970"}); r.Status != 422 || r.code(t) != "validation_failed" {
		t.Errorf("the global scope: %d %s", r.Status, r.Body)
	}
}
