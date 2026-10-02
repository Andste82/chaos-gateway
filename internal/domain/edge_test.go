package domain

import (
	"testing"
)

// Narrowing: a step may select less than the scenario's target, never more.
func TestStepTargetsNarrowOrWidenTheScenarioTarget(t *testing.T) {
	tests := []struct {
		scenario, step string
		ok             bool
	}{
		// a device target allows only that device
		{`{device: esp32-42}`, `{device: esp32-42}`, true},
		{`{device: esp32-42}`, `{device: lab-host}`, false},
		{`{device: esp32-42}`, `{group: sensors}`, false},
		{`{device: esp32-42}`, `{network: IoT}`, false},
		// a group allows itself and its members
		{`{group: sensors}`, `{group: sensors}`, true},
		{`{group: sensors}`, `{device: esp32-42}`, true},
		{`{group: sensors}`, `{device: lab-host}`, false},
		{`{group: sensors}`, `{group: g2}`, false}, // another group, even with the same members
		{`{group: sensors}`, `{network: IoT}`, false},
		// a network allows itself, its devices, groups whose members are all in it, and what is behind it
		{`{network: IoT}`, `{network: IoT}`, true},
		{`{network: IoT}`, `{device: esp32-42}`, true},
		{`{network: IoT}`, `{device: lab-host}`, false},
		{`{network: IoT}`, `{group: sensors}`, true},
		{`{network: IoT}`, `{network: lab-hub}`, false},
		{`{network: IoT}`, `{remote_network: {client: lab-rA}}`, false},
		{`{network: lab-hub}`, `{remote_network: {client: lab-rA}}`, true},
		{`{network: lab-hub}`, `{device: lab-rA}`, true},
		{`{network: lab-hub}`, `{remote_network: {link: site-b}}`, false},
		{`{network: site-b}`, `{remote_network: {link: site-b}}`, true},
		{`{network: site-b}`, `{remote_network: {cidr: 10.50.0.0/24}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.scenario+" / "+tt.step, func(t *testing.T) {
			d := baseDoc(t)
			d.set(t, obj(t, tt.scenario), "scenarios", idScenario, "target")
			scenarioWith(t, d, `[{"id":"a","at":"0s","target":`+tt.step+`,"fault":{"blackout":true}}]`, "")
			errs := d.validate(t)
			if got := !errs.Has(sc("/steps/0/target"), CodeTargetWidened); got != tt.ok {
				t.Fatalf("allowed = %v, want %v\n%s", got, tt.ok, dump(errs))
			}
		})
	}
}

func TestAGroupOfDevicesFromAnotherNetworkIsNotInsideTheNetwork(t *testing.T) {
	d := baseDoc(t)
	d.set(t, []any{"esp32-42", "lab-host"}, "groups", idSensors, "members")
	d.set(t, obj(t, `{"network":"IoT"}`), "scenarios", idScenario, "target")
	scenarioWith(t, d, `[{"id":"a","at":"0s","target":{"group":"sensors"},"fault":{"blackout":true}}]`, "")
	wantError(t, d.validate(t), sc("/steps/0/target"), CodeTargetWidened)
}

func TestSettingsDurations(t *testing.T) {
	runMutations(t, []mutation{
		{"a counter poll interval below 100 ms", func(t *testing.T, d doc) { d.set(t, "50ms", "settings", "counter_poll_interval") }, "/settings/counter_poll_interval", CodeInvalidDuration},
		{"a run retention below an hour", func(t *testing.T, d doc) { d.set(t, obj(t, `{"runs":"30m"}`), "settings", "retention") }, "/settings/retention/runs", CodeInvalidDuration},
		{"an event retention below an hour", func(t *testing.T, d doc) { d.set(t, obj(t, `{"events":"1m"}`), "settings", "retention") }, "/settings/retention/events", CodeInvalidDuration},
		{"an audit retention below an hour", func(t *testing.T, d doc) { d.set(t, obj(t, `{"audit":"1m"}`), "settings", "retention") }, "/settings/retention/audit", CodeInvalidDuration},
	})
	d := baseDoc(t)
	d.set(t, obj(t, `{"commit_confirm_timeout":"5s","counter_poll_interval":"250ms","retention":{"runs":"720h","events":"24h","audit":"8760h","captures_quota":"2GiB","revisions":50}}`), "settings")
	wantValid(t, d.validate(t))
}

func TestEndpoints(t *testing.T) {
	valid := []string{"gw.example.net:51820", "203.0.113.5:1", "a.b.c.d:65535", "localhost:51820", "gw-1.example.net:443"}
	for _, ep := range valid {
		d := baseDoc(t)
		d.set(t, ep, "networks", idHub, "endpoint")
		if errs := d.validate(t); errs.Has(hub+"/endpoint", CodeInvalidEndpoint) {
			t.Errorf("%s must be valid:\n%s", ep, dump(errs))
		}
	}
	invalid := []string{"gw.example.net:0", "gw.example.net:65536", "203.0.113.5.5:51820", "256.1.1.1:51820", "bad_name.example:51820", "-x.example:51820"}
	for _, ep := range invalid {
		d := baseDoc(t)
		d.set(t, ep, "networks", idHub, "endpoint")
		if !d.validate(t).Has(hub+"/endpoint", CodeInvalidEndpoint) {
			t.Errorf("%s must be invalid", ep)
		}
	}
}

func TestOverlayKeysDistinguishEveryScopeAndDestinationKind(t *testing.T) {
	keyOf := func(body string) string {
		t.Helper()
		out, errs := validateOverlay(t, body)
		if len(errs) != 0 {
			t.Fatalf("%s: %v", body, errs)
		}
		k, err := OverlayKey(admin, out)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	scopes := []string{
		`{device: esp32-42}`, `{group: sensors}`, `{network: IoT}`, `{global: true}`,
		`{remote_network: {client: lab-rA}}`, `{remote_network: {link: site-b}}`, `{remote_network: {cidr: 10.50.0.0/24}}`,
	}
	seen := map[string]string{}
	for _, sc := range scopes {
		k := keyOf(`{target: ` + sc + `, fault: {blackout: true}}`)
		if other, dup := seen[k]; dup {
			t.Errorf("scopes %s and %s share the key %s", sc, other, k)
		}
		seen[k] = sc
	}
	dests := []string{`{uplink: true}`, `{network: IoT}`, `{cidr: 203.0.113.0/24}`, `{cidr: 203.0.113.1}`, `{hostname: a.test}`}
	seen = map[string]string{}
	for _, dst := range dests {
		k := keyOf(`{target: {global: true}, fault: {destination: ` + dst + `, latency: 5ms}}`)
		if other, dup := seen[k]; dup {
			t.Errorf("destinations %s and %s share the key %s", dst, other, k)
		}
		seen[k] = dst
	}
}

func TestDHCPActionLeaseTimes(t *testing.T) {
	_, errs := validateOverlay(t, `{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 500ms}}`)
	wantError(t, errs, "/dhcp/lease_time", CodeInvalidDuration)
	_, errs = validateOverlay(t, `{target: {device: esp32-42}, dhcp: {action: set_options, options: {router: 224.0.0.1}}}`)
	wantError(t, errs, "/dhcp/options/router", CodeInvalidAddress)
	_, errs = validateOverlay(t, `{target: {device: esp32-42}, dhcp: {action: silence, options: {router: 10.10.0.1}}}`)
	wantError(t, errs, "/dhcp/options", CodeInvalidDHCPAction)
}

func TestNameOfFallsBackToTheID(t *testing.T) {
	idx, _ := BuildIndex(stored(t))
	if idx.NameOf(KindNetwork, idIoT) != "IoT" || idx.NameOf(KindDevice, idESP) != "esp32-42" ||
		idx.NameOf(KindGroup, idSensors) != "sensors" || idx.NameOf(KindProfile, "5b455ed0-e2bf-5910-8dc6-7a0af428025c") != "normal" ||
		idx.NameOf(KindClient, idClient) != "lab-rA" || idx.NameOf(KindLink, idLink) != "site-b" {
		t.Error("names must resolve")
	}
	if got := idx.NameOf(KindNetwork, "nope"); got != "nope" {
		t.Errorf("an unknown id must come back as it is: %s", got)
	}
	if Kind(3).String() != "client" || kindFault.String() != "fault" {
		t.Error("kind names")
	}
	if got := ValidationErrors(nil).Has("/x", "y"); got {
		t.Error("an empty list has nothing")
	}
}

func TestParseErrorUnwraps(t *testing.T) {
	_, err := ParseDocument([]byte(`{`), FormatJSON)
	var pe *ParseError
	if !asParseError(err, &pe) || pe.Unwrap() == nil {
		t.Fatal("ParseError must wrap the cause")
	}
}
