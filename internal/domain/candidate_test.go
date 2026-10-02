package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The test cases of RFC 7396, appendix A.
func TestMergePatchFollowsRFC7396(t *testing.T) {
	cases := []struct{ target, patch, want string }{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`["a","b"]`, `["c","d"]`, `["c","d"]`},
		{`{"a":"b"}`, `["c"]`, `["c"]`},
		{`{"a":"foo"}`, `null`, `null`},
		{`{"a":"foo"}`, `"bar"`, `"bar"`},
		{`{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{`[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
		{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
	}
	for _, c := range cases {
		target, patch := parse(t, c.target), parse(t, c.patch)
		got := MergePatch(target, patch)
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(parse(t, c.want))
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("MergePatch(%s, %s) = %s, want %s", c.target, c.patch, gotJSON, wantJSON)
		}
	}
}

func parse(t *testing.T, s string) any {
	t.Helper()
	v, err := ParseDocument([]byte(s), FormatJSON)
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return v
}

func TestMergePatchDoesNotChangeItsInputs(t *testing.T) {
	target := parse(t, `{"a":{"b":1},"c":[1]}`)
	patch := parse(t, `{"a":{"b":null,"x":{"y":1}}}`)
	_ = MergePatch(target, patch)
	if got, _ := json.Marshal(target); string(got) != `{"a":{"b":1},"c":[1]}` {
		t.Errorf("target changed: %s", got)
	}
	if got, _ := json.Marshal(patch); string(got) != `{"a":{"b":null,"x":{"y":1}}}` {
		t.Errorf("patch changed: %s", got)
	}
	// the result does not share structure with the patch either
	out := MergePatch(map[string]any{}, patch).(map[string]any)
	out["a"].(map[string]any)["x"].(map[string]any)["y"] = "changed"
	if got, _ := json.Marshal(patch); string(got) != `{"a":{"b":null,"x":{"y":1}}}` {
		t.Errorf("the result aliases the patch: %s", got)
	}
}

func stored(t *testing.T) *model.Configuration {
	t.Helper()
	cfg, errs := Normalize(exampleConfiguration(t))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	return cfg
}

func TestACandidateFromAFullConfigurationIsNormalizedAndGetsFaultTimes(t *testing.T) {
	now := t0.Add(time.Hour)
	cfg, err := NewCandidate(nil, readExample(t, "configuration.yaml"), FormatYAML, CandidateFull, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := *deref(cfg.Devices)[idESP].Network; got != idIoT {
		t.Errorf("references must be UUIDs, device network = %s", got)
	}
	for id, f := range deref(cfg.Faults) {
		if f.CreatedAt == nil || !f.CreatedAt.Equal(now) {
			t.Errorf("fault %s created_at = %v, want %v", id, f.CreatedAt, now)
		}
	}
}

func TestAPatchChangesAddsAndDeletesSingleObjects(t *testing.T) {
	base := stored(t)
	patch := `{
	  "devices": {
	    "` + idESP + `": {"trusts_test_ca": true},
	    "` + idLab + `": null,
	    "` + idNew + `": {"name": "new-device", "identifiers": {"macs": ["24:0a:c4:00:00:99"]}}
	  },
	  "groups": {"` + idSensors + `": {"members": ["new-device"]}},
	  "settings": {"commit_confirm_timeout": "90s"}
	}`
	cfg, err := NewCandidate(base, []byte(patch), FormatJSON, CandidatePatch, t0)
	if err != nil {
		t.Fatal(err)
	}
	devs := deref(cfg.Devices)
	if !deref(devs[idESP].TrustsTestCa) {
		t.Error("the changed field must be applied")
	}
	if devs[idESP].Name != "esp32-42" || devs[idESP].FixedIp == nil {
		t.Error("the other fields of the device must stay")
	}
	if _, gone := devs[idLab]; gone {
		t.Error("null must delete the device")
	}
	if devs[idNew].Name != "new-device" {
		t.Error("the new device must be added")
	}
	if got := (*deref(cfg.Groups)[idSensors].Members)[0]; got != idNew {
		t.Errorf("the group member was resolved to %s", got)
	}
	if *cfg.Settings.CommitConfirmTimeout != "90s" || *cfg.Settings.ClassLimitPerInterface != 1000 {
		t.Errorf("settings = %+v", cfg.Settings)
	}
	// the base is untouched
	if _, ok := deref(base.Devices)[idLab]; !ok {
		t.Error("the base configuration was changed")
	}
}

func TestAFaultKeepsItsCreatedAtAndANewOneGetsNow(t *testing.T) {
	base := stored(t)
	earlier := t0.Add(-48 * time.Hour)
	faults := *base.Faults
	f := faults[idFaultNet]
	f.CreatedAt = &earlier
	faults[idFaultNet] = f

	now := t0
	patch := `{"faults": {
	  "` + idFaultNet + `": {"latency": "250ms", "created_at": "2000-01-01T00:00:00Z"},
	  "` + idNew + `": {"source": {"global": true}, "latency": "5ms", "created_at": "2001-01-01T00:00:00Z"}}}`
	cfg, err := NewCandidate(base, []byte(patch), FormatJSON, CandidatePatch, now)
	if err != nil {
		t.Fatal(err)
	}
	got := deref(cfg.Faults)
	if !got[idFaultNet].CreatedAt.Equal(earlier) {
		t.Errorf("an existing fault keeps its time (the client's value is ignored): %v", got[idFaultNet].CreatedAt)
	}
	if !got[idNew].CreatedAt.Equal(now) {
		t.Errorf("a new fault gets now (the client's value is ignored): %v", got[idNew].CreatedAt)
	}
	if *got[idFaultNet].Latency != "250ms" {
		t.Error("the change must be applied")
	}
}

func TestACandidateThatIsInvalidAfterMergingIsRejected(t *testing.T) {
	base := stored(t)
	tests := []struct {
		name, patch, path, code string
	}{
		{"deleting an object that is referenced", `{"devices":{"` + idESP + `":null}}`, "/groups/" + idSensors + "/members/0", CodeUnknownReference},
		{"a patch that breaks the schema", `{"devices":{"` + idESP + `":{"name":"bad name"}}}`, "/devices/" + idESP + "/name", "pattern"},
		{"an unknown field", `{"settings":{"nope":1}}`, "/settings/nope", "unknown_field"},
		{"a semantic rule", `{"faults":{"` + idFaultNet + `":{"jitter":"500ms"}}}`, "/faults/" + idFaultNet + "/jitter", CodeJitterExceedsLatency},
		{"an overlapping subnet", `{"networks":{"` + idHub + `":{"address":"10.10.0.9/24"}}}`, "/networks/" + idHub + "/address", CodeOverlappingSubnet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCandidate(base, []byte(tt.patch), FormatJSON, CandidatePatch, t0)
			var ve ValidationErrors
			if !asValidation(err, &ve) {
				t.Fatalf("err = %v", err)
			}
			wantError(t, ve, tt.path, tt.code)
		})
	}
}

func TestACandidateRejectsMalformedAndMisusedInput(t *testing.T) {
	base := stored(t)
	var pe *ParseError
	if _, err := NewCandidate(base, []byte(`{"a":`), FormatJSON, CandidatePatch, t0); !asParseError(err, &pe) {
		t.Errorf("a malformed patch: %v", err)
	}
	if _, err := NewCandidate(nil, []byte(`{}`), FormatJSON, CandidatePatch, t0); err == nil {
		t.Error("a patch needs a base")
	}
	var ve ValidationErrors
	if _, err := NewCandidate(base, []byte(`[1]`), FormatJSON, CandidatePatch, t0); !asValidation(err, &ve) || !ve.Has("", "invalid_type") {
		t.Errorf("a patch that is not an object: %v", err)
	}
	if _, err := NewCandidate(nil, []byte(`{"schema_version":1}`), FormatJSON, CandidateFull, t0); !asValidation(err, &ve) {
		t.Errorf("an incomplete configuration: %v", err)
	}
}

func TestAnEmptyPatchGivesAnEqualConfiguration(t *testing.T) {
	// a stored configuration always has created_at on its faults
	base, err := NewCandidate(nil, readExample(t, "configuration.yaml"), FormatYAML, CandidateFull, t0)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := NewCandidate(base, []byte(`{}`), FormatJSON, CandidatePatch, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(cfg, base) {
		t.Fatal("an empty patch must not change the configuration")
	}
	changed := clone(*base)
	changed.Settings = &model.Settings{}
	if Equal(&changed, base) {
		t.Error("Equal must see the difference")
	}
}
