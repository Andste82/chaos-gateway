package domain

import (
	"testing"
)

func sc(extra string) string { return "/scenarios/" + idScenario + extra }

// scenarioWith replaces the steps (and optionally the checks) of the example scenario.
func scenarioWith(t *testing.T, d doc, steps string, checks string) {
	t.Helper()
	d.set(t, parseArray(t, steps), "scenarios", idScenario, "steps")
	if checks == "" {
		d.del(t, "scenarios", idScenario, "checks")
		return
	}
	d.set(t, parseArray(t, checks), "scenarios", idScenario, "checks")
}

func parseArray(t *testing.T, s string) []any {
	t.Helper()
	v, err := ParseDocument([]byte(s), FormatYAML) // YAML reads JSON too
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return v.([]any)
}

func TestScenarioStepRules(t *testing.T) {
	runMutations(t, []mutation{
		{"a step id used twice", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","restore":true},{"id":"a","at":"1s","restore":true}]`, "")
		}, sc("/steps/1/id"), CodeDuplicateStep},
		{"the reserved step id start", func(t *testing.T, d doc) { scenarioWith(t, d, `[{"id":"start","at":"0s","restore":true}]`, "") }, sc("/steps/0/id"), CodeReservedID},
		{"a step with two kinds", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","restore":true,"profile":"normal"}]`, "")
		}, sc("/steps/0"), CodeStepKind},
		{"a step with no kind", func(t *testing.T, d doc) { scenarioWith(t, d, `[{"id":"a","at":"0s"}]`, "") }, sc("/steps/0"), CodeStepKind},
		{"a step that widens the target", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","target":{"network":"IoT"},"fault":{"blackout":true}}]`, "")
		}, sc("/steps/0/target"), CodeTargetWidened},
		{"a step with an unknown profile", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","profile":"no-such-profile"}]`, "")
		}, sc("/steps/0/profile"), CodeUnknownReference},
		{"a step fault with jitter above latency", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","fault":{"latency":"5ms","jitter":"50ms"}}]`, "")
		}, sc("/steps/0/fault/jitter"), CodeJitterExceedsLatency},
		{"a step rule that resets udp", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","rule":{"action":"reset","protocol":"udp"}}]`, "")
		}, sc("/steps/0/rule/action"), CodeResetRequiresTCP},
		{"a step dns fault without its parameter", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","dns":{"action":"delay"}}]`, "")
		}, sc("/steps/0/dns/delay"), CodeInvalidDNSFault},
		{"a step dhcp action with a group target", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"group":"sensors"}`), "scenarios", idScenario, "target")
			scenarioWith(t, d, `[{"id":"a","at":"0s","dhcp":{"action":"silence"}}]`, "")
		}, sc("/steps/0/dhcp"), CodeTargetKind},
		{"a step short lease without a time", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","dhcp":{"action":"short_lease"}}]`, "")
		}, sc("/steps/0/dhcp/lease_time"), CodeInvalidDHCPAction},
		{"a step that removes an unknown step", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","remove":"ghost"}]`, "")
		}, sc("/steps/0/remove"), CodeStepOrder},
		{"a step that removes a step that creates no overlay", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","capture":"start"},{"id":"b","at":"1s","remove":"a"}]`, "")
		}, sc("/steps/1/remove"), CodeStepOrder},
		{"a step that removes a later step", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"gone","at":"5s","remove":"later"},{"id":"later","at":"10s","fault":{"blackout":true}}]`, "")
		}, sc("/steps/0/remove"), CodeStepOrder},
		{"a step that removes itself", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","remove":"a"}]`, "")
		}, sc("/steps/0/remove"), CodeStepOrder},
		{"a wait for a dns query without a name", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"dns_query","timeout":"10s"}}]`, "")
		}, sc("/steps/0/wait/name"), CodeMissingField},
		{"a wait for a connection with a name", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"connected","name":"x.test","timeout":"10s"}}]`, "")
		}, sc("/steps/0/wait/name"), CodeUnexpectedField},
		{"a wait that never times out", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"online","timeout":"0s"}}]`, "")
		}, sc("/steps/0/wait/timeout"), CodeInvalidDuration},
		{"a wait with ports and no protocol", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"connected","ports":[8883],"timeout":"10s"}}]`, "")
		}, sc("/steps/0/wait/protocol"), CodePortsRequireProtocol},
		{"a tunnel fault in a scenario about a device", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","fault":{"family":"tunnel","tunnel":{"link":"site-b"},"latency":"50ms"}}]`, "")
		}, sc("/steps/0/fault/tunnel"), CodeTunnelOutsideTarget},
		{"a wireguard action in a scenario about a device", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wireguard":{"link":"site-b","action":"disable"}}]`, "")
		}, sc("/steps/0/wireguard"), CodeTunnelOutsideTarget},
		{"a wireguard action naming client and link", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"network":"lab-hub"}`), "scenarios", idScenario, "target")
			scenarioWith(t, d, `[{"id":"a","at":"0s","wireguard":{"link":"site-b","client":"lab-rA","action":"disable"}}]`, "")
		}, sc("/steps/0/wireguard"), CodeInvalidOverlay},
		{"a wireguard action naming neither", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wireguard":{"action":"disable"}}]`, "")
		}, sc("/steps/0/wireguard"), "min_properties"}, // the schema catches it before the domain does
		{"a scenario about the global scope", func(t *testing.T, d doc) { d.set(t, obj(t, `{"global":true}`), "scenarios", idScenario, "target") }, sc("/target"), CodeTargetKind},
		{"a scenario about a remote network", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"remote_network":{"client":"lab-rA"}}`), "scenarios", idScenario, "target")
		}, sc("/target"), CodeTargetKind},
		{"a scenario about a device that does not exist", func(t *testing.T, d doc) { d.set(t, obj(t, `{"device":"ghost"}`), "scenarios", idScenario, "target") }, sc("/target/device"), CodeUnknownReference},
	})
}

func TestScenarioCheckRules(t *testing.T) {
	one := `[{"id":"a","at":"0s","fault":{"blackout":true}},{"id":"b","at":"10s","remove":"a"}]`
	runMutations(t, []mutation{
		{"a check with no type", func(t *testing.T, d doc) { scenarioWith(t, d, one, `[{"window":{"from":"a","within":"5s"}}]`) }, sc("/checks/0"), CodeStepKind},
		{"a check with two types", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{},"traffic_seen":{},"window":{"from":"a","within":"5s"}}]`)
		}, sc("/checks/0"), CodeStepKind},
		{"a window from an unknown step", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{},"window":{"from":"ghost","within":"5s"}}]`)
		}, sc("/checks/0/window/from"), CodeCheckWindow},
		{"a window with within and until", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{},"window":{"from":"a","within":"5s","until":"b"}}]`)
		}, sc("/checks/0/window"), CodeCheckWindow},
		{"a window with neither", func(t *testing.T, d doc) { scenarioWith(t, d, one, `[{"reconnected":{},"window":{"from":"a"}}]`) }, sc("/checks/0/window"), CodeCheckWindow},
		{"a window that ends before it starts", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{},"window":{"from":"b","until":"a"}}]`)
		}, sc("/checks/0/window/until"), CodeCheckWindow},
		{"a window that ends at start", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{},"window":{"from":"start","until":"start"}}]`)
		}, sc("/checks/0/window/until"), CodeCheckWindow},
		{"a window until an unknown step", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{},"window":{"from":"a","until":"ghost"}}]`)
		}, sc("/checks/0/window/until"), CodeCheckWindow},
		{"a window of zero length", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{},"window":{"from":"a","within":"0s"}}]`)
		}, sc("/checks/0/window/within"), CodeInvalidDuration},
		{"a check selector with ports and no protocol", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"reconnected":{"ports":[8883]},"window":{"from":"a","within":"5s"}}]`)
		}, sc("/checks/0/reconnected/protocol"), CodePortsRequireProtocol},
		{"a not-seen check with ports and no protocol", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"traffic_not_seen":{"ports":[8883]},"window":{"from":"a","within":"5s"}}]`)
		}, sc("/checks/0/traffic_not_seen/protocol"), CodePortsRequireProtocol},
		{"a check destination that does not exist", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"traffic_seen":{"destination":{"network":"ghost"}},"window":{"from":"a","within":"5s"}}]`)
		}, sc("/checks/0/traffic_seen/destination/network"), CodeUnknownReference},
		{"an except destination that does not exist", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"traffic_not_seen":{"except":[{"network":"ghost"}]},"window":{"from":"a","within":"5s"}}]`)
		}, sc("/checks/0/traffic_not_seen/except/0/network"), CodeUnknownReference},
	})
}

func TestScenarioScopesThatNarrowTheTargetAreAccepted(t *testing.T) {
	steps := `[
	  {"id":"a","at":"0s","target":{"device":"esp32-42"},"fault":{"blackout":true}},
	  {"id":"b","at":"1s","target":{"group":"sensors"},"fault":{"latency":"10ms"}}]`
	for _, target := range []string{
		`{"network":"IoT"}`, `{"group":"sensors"}`, `{"device":"esp32-42"}`,
	} {
		d := baseDoc(t)
		d.set(t, obj(t, target), "scenarios", idScenario, "target")
		switch target {
		case `{"device":"esp32-42"}`:
			// a device target cannot be widened to its group
			scenarioWith(t, d, `[{"id":"a","at":"0s","target":{"device":"esp32-42"},"fault":{"blackout":true}}]`, "")
		case `{"group":"sensors"}`:
			scenarioWith(t, d, `[{"id":"a","at":"0s","target":{"device":"esp32-42"},"fault":{"blackout":true}},{"id":"b","at":"1s","fault":{"latency":"10ms"}}]`, "")
		default:
			scenarioWith(t, d, steps, "")
		}
		wantValid(t, d.validate(t))
	}
}

func TestATunnelFaultInsideTheTargetIsAccepted(t *testing.T) {
	cases := []struct{ target, step string }{
		{`{"network":"lab-hub"}`, `{"id":"a","at":"0s","fault":{"family":"tunnel","tunnel":{"client":"lab-rA"},"latency":"50ms"}}`},
		{`{"device":"lab-rA"}`, `{"id":"a","at":"0s","wireguard":{"client":"lab-rA","action":"disable"}}`},
		{`{"network":"site-b"}`, `{"id":"a","at":"0s","wireguard":{"link":"site-b","action":"block_endpoint"}}`},
		{`{"network":"site-b"}`, `{"id":"a","at":"0s","fault":{"family":"tunnel","tunnel":{"link":"site-b"},"blackout":true}}`},
	}
	for _, c := range cases {
		d := baseDoc(t)
		d.set(t, obj(t, c.target), "scenarios", idScenario, "target")
		scenarioWith(t, d, "["+c.step+"]", "")
		wantValid(t, d.validate(t))
	}
}

func TestStepsAtTheSameTimeRunInListOrderAndMayRemoveEarlierOnes(t *testing.T) {
	d := baseDoc(t)
	scenarioWith(t, d, `[
	  {"id":"add","at":"5s","fault":{"blackout":true}},
	  {"id":"del","at":"5s","remove":"add"},
	  {"id":"late","at":"2s","fault":{"latency":"10ms"}},
	  {"id":"restore","at":"20s","restore":true}]`, `[
	  {"name":"back","reconnected":{"protocol":"tcp","ports":[8883]},"window":{"from":"del","within":"30s"}},
	  {"traffic_not_seen":{"except":[{"hostname":"broker.example.com"},{"cidr":"10.0.0.0/8"}]},"window":{"from":"start","until":"restore"}},
	  {"dns_query_seen":{"name":"broker.example.com"},"window":{"from":"start","within":"30s"}},
	  {"tls_rejected":{"ports":[8883],"protocol":"tcp"},"window":{"from":"add","within":"5s"}}]`)
	wantValid(t, d.validate(t))

	// the same removal in the other list order runs before the step it removes
	d = baseDoc(t)
	scenarioWith(t, d, `[
	  {"id":"del","at":"5s","remove":"add"},
	  {"id":"add","at":"5s","fault":{"blackout":true}}]`, "")
	wantError(t, d.validate(t), sc("/steps/0/remove"), CodeStepOrder)
}

func TestValidateScenarioAgainstAConfiguration(t *testing.T) {
	cfg := exampleConfiguration(t)
	good, err := DecodeScenario([]byte(`
name: from-a-file
target: {device: esp32-42}
steps:
  - {id: a, at: 0s, profile: bad-lte}
  - {id: b, at: 30s, remove: a}
checks:
  - {reconnected: {protocol: tcp, ports: [8883]}, window: {from: b, within: 30s}}
`), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	if errs := ValidateScenario(cfg, good); len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}

	bad, err := DecodeScenario([]byte(`
name: broken
target: {device: ghost}
steps:
  - {id: a, at: 0s, profile: nope}
  - {id: a, at: 1s, remove: zzz}
`), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	errs := ValidationErrors(ValidateScenario(cfg, bad))
	for _, want := range []struct{ path, code string }{
		{"/target/device", CodeUnknownReference}, {"/steps/0/profile", CodeUnknownReference},
		{"/steps/1/id", CodeDuplicateStep}, {"/steps/1/remove", CodeStepOrder},
	} {
		if !errs.Has(want.path, want.code) {
			t.Errorf("want %s at %s in:\n%s", want.code, want.path, dump(errs))
		}
	}
}

func TestDecodeScenarioReportsSchemaProblemsWithPointers(t *testing.T) {
	_, err := DecodeScenario([]byte(`{"name":"x","target":{"device":"d"},"steps":[{"id":"a","at":"0s","restore":true,"oops":1}]}`), FormatJSON)
	var ve ValidationErrors
	if !asValidation(err, &ve) || !ve.Has("/steps/0/oops", "unknown_field") {
		t.Fatalf("err = %v", err)
	}
}
