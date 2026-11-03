package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Stage 1 of M34 (D37) describes the time family in the domain and the specification but serves nothing: the engine
// refuses a time fault and the activation of a profile with a time part with the milestone, and nothing changes. Stage 2
// lifts both (SupportedOverlayKinds, domain.SetUnavailableFamilies) and replaces this test.
func TestATimeFaultAndAProfileWithATimePartAreRefusedUntilTheServerExists(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(func(c *model.Configuration) {
		c.Profiles = &map[string]model.Profile{
			profGlitch: {Name: "clock", Parts: model.ProfileParts{Time: &model.TimeFault{Offset: ptr("+400d")}}},
		}
	}))
	gen := h.e.Snapshot().Generation
	for name, body := range map[string]string{
		"a time fault":               "target: {network: IoT}\ntime: {offset: +400d}",
		"an invalid time fault":      "target: {global: true}\ntime: {offset: +400d, absolute: '1970-01-01T00:00:00Z'}",
		"a profile with a time part": "target: {network: IoT}\nprofile: clock",
	} {
		_, err := h.put(alice, body)
		var ue *engine.UnsupportedOverlayError
		if !errors.As(err, &ue) || ue.Milestone != "M34" {
			t.Errorf("%s: got %v, want unsupported until M34", name, err)
		}
	}
	if s := h.e.Snapshot(); len(s.Overlays) != 0 || s.Generation != gen {
		t.Errorf("a refused write changed the state: %d overlays, generation %d -> %d", len(s.Overlays), gen, s.Generation)
	}
	for _, k := range engine.SupportedOverlayKinds {
		if k == model.OverlayKindTime {
			t.Error("the engine lists the kind time as supported")
		}
	}
	for _, f := range engine.SupportedFaultFamilies {
		if f == model.FaultFamilyTime {
			t.Error("the engine lists the family time as supported")
		}
	}
	// the request is refused whatever the body, by the kind
	req := &model.OverlayRequest{Target: &model.Scope{Network: ptr("IoT")}, Time: &model.TimeFault{Offset: ptr("+1d")}}
	var ue *engine.UnsupportedOverlayError
	if err := engine.CheckOverlaySupported(req); !errors.As(err, &ue) || !strings.Contains(ue.Error(), "time fault") {
		t.Errorf("CheckOverlaySupported = %v", err)
	}
}
