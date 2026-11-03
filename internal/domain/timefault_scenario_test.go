package domain

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// idStaleClock is the scenario of api/examples/configuration.yaml with a time step.
const idStaleClock = "5a6b7c8d-9e0f-4a1b-8c2d-3e4f5a6b7c83"

// ---- profile parts --------------------------------------------------------------------------

func TestTheTimePartOfAProfileIsValidatedLikeATimeFault(t *testing.T) {
	pr := "/profiles/" + idProfile + "/parts"
	set := func(body string) func(*testing.T, doc) {
		return func(t *testing.T, d doc) { d.set(t, obj(t, body), "profiles", idProfile, "parts", "time") }
	}
	runMutations(t, []mutation{
		{"a time part that changes nothing", set(`{}`), pr + "/time", CodeInvalidTimeFault},
		{"a time part with only a destination", set(`{"destination":{"hostname":"time.google.com"}}`), pr + "/time", CodeInvalidTimeFault},
		{"a time part with offset and absolute", set(`{"offset":"+1d","absolute":"1970-01-01T00:00:00Z"}`), pr + "/time/absolute", CodeInvalidTimeFault},
		{"a time part with silence and an offset", set(`{"answer":"silence","offset":"+1d"}`), pr + "/time/offset", CodeInvalidTimeFault},
		{"a kiss of death without a code", set(`{"answer":"kiss_of_death"}`), pr + "/time/kiss_code", CodeInvalidTimeFault},
		{"a time part over tcp", set(`{"protocol":"tcp","offset":"+1d"}`), pr + "/time/protocol", CodeInvalidTimeFault},
		{"a time part for a network", set(`{"destination":{"network":"IoT"},"offset":"+1d"}`), pr + "/time/destination", CodeInvalidTimeFault},
		{"a time part with an absolute time before 1900", set(`{"absolute":"1899-01-01T00:00:00Z"}`), pr + "/time/absolute", CodeInvalidTimeFault},
		{"a time part with an offset of 300 years", set(`{"offset":"+300y"}`), pr + "/time/offset", CodeInvalidTimeFault},
	})
	// a part that is fine, next to the parts of the other families
	d := baseDoc(t)
	d.set(t, obj(t, `{"offset":"-1d","skew_ppm":20,"leap":"delete"}`), "profiles", idProfile, "parts", "time")
	wantValid(t, d.validate(t))
	// the network is resolved in the part like in every destination
	d = baseDoc(t)
	d.set(t, obj(t, `{"destination":{"network":"ghost"},"offset":"+1d"}`), "profiles", idProfile, "parts", "time")
	wantError(t, d.validate(t), pr+"/time/destination/network", CodeUnknownReference)
}

func TestTheFamiliesOfAProfileIncludeTime(t *testing.T) {
	p := model.Profile{Name: "all", Parts: model.ProfileParts{
		Time: &model.TimeFault{Offset: ptr("+1d")}, Tls: &model.TlsCase{Case: "expired"}, Dns: &model.DnsFault{Action: "servfail"},
		Mtu: &model.MtuParams{Size: 1400}, Impairment: &model.ImpairmentParams{},
	}}
	if got := strings.Join(ProfileFamilies(p), ","); got != "impairment,mtu,dns,tls,time" {
		t.Errorf("%s", got)
	}
	if got := ProfileFamilies(model.Profile{Parts: model.ProfileParts{Time: &model.TimeFault{Offset: ptr("+1d")}}}); len(got) != 1 || got[0] != FamilyTime {
		t.Errorf("%v", got)
	}
}

// The time family is described and validated, but the server arrives with stage 2 of M34: until then a profile with a time
// part is stored and shown, and its activation is refused as a whole (the engine reads UnavailableParts).
func TestAProfileWithATimePartIsUnavailableUntilTheServerExists(t *testing.T) {
	p := model.Profile{Name: "clock", Parts: model.ProfileParts{Time: &model.TimeFault{Absolute: ptr(ntpEpoch.AddDate(70, 0, 0))}}}
	if got := UnavailableParts(p); len(got) != 1 || got[0] != (UnavailablePart{FamilyTime, "M34"}) {
		t.Fatalf("%+v", got)
	}
	// the other parts of a profile stay available, and so does every profile of the catalogue
	other := model.Profile{Parts: model.ProfileParts{Dns: &model.DnsFault{Action: "servfail"}, Tls: &model.TlsCase{Case: "expired"}}}
	if got := UnavailableParts(other); len(got) != 0 {
		t.Errorf("%+v", got)
	}
	// the family is available once the build serves it
	defer SetUnavailableFamilies(map[string]string{})()
	if got := UnavailableParts(p); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

func TestTheActivationOfAProfileWithATimePartNeedsADeviceAGroupOrANetwork(t *testing.T) {
	for _, tt := range []struct {
		target, profile string
		ok              bool
	}{
		{`{device: esp32-42}`, "clock-in-1970", true},
		{`{group: sensors}`, "clock-in-1970", true},
		{`{network: IoT}`, "clock-in-1970", true},
		{`{global: true}`, "clock-in-1970", false},
		{`{remote_network: {client: lab-rA}}`, "clock-in-1970", false},
		{`{remote_network: {cidr: 10.60.0.0/24}}`, "clock-in-1970", false},
		// the profile by its UUID
		{`{global: true}`, idClockProfile, false},
		// a profile without a time part has no restriction
		{`{global: true}`, "lte", true},
		{`{global: true}`, "slow-dns-lte", true},
		{`{remote_network: {client: lab-rA}}`, "bad-lte", true},
	} {
		errs := validateRaw(t, `{target: `+tt.target+`, profile: `+tt.profile+`}`)
		switch {
		case tt.ok && len(errs) != 0:
			t.Errorf("%s on %s: %s", tt.profile, tt.target, dump(errs))
		case !tt.ok && !errs.Has("/target", CodeTargetKind):
			t.Errorf("%s on %s: want %s at /target, got:\n%s", tt.profile, tt.target, CodeTargetKind, dump(errs))
		}
	}
}

// ---- references ----------------------------------------------------------------------------

func TestTheDestinationOfATimeFaultIsResolvedAndCheckedLikeEveryDestination(t *testing.T) {
	out, errs := validateOverlay(t, `{target: {device: esp32-42}, time: {destination: {network: IoT}, offset: +1d}}`)
	// the reference is resolved to the UUID, and a network is no server a device asks
	if got := *out.Time.Destination.Network; got != idIoT {
		t.Errorf("network = %s", got)
	}
	wantError(t, errs, "/time/destination", CodeInvalidTimeFault)
	_, errs = validateOverlay(t, `{target: {device: esp32-42}, time: {destination: {network: ghost}, offset: +1d}}`)
	wantError(t, errs, "/time/destination/network", CodeUnknownReference)
}

// ---- scenario steps, waits and checks ---------------------------------------------------------

func TestTheTimeStepOfAScenarioIsValidated(t *testing.T) {
	remoteStep := func(t *testing.T, d doc, step string) {
		d.set(t, obj(t, `{"network":"lab-hub"}`), "scenarios", idScenario, "target")
		scenarioWith(t, d, "["+step+"]", "")
	}
	runMutations(t, []mutation{
		{"a time step that changes nothing", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"destination":{"hostname":"time.google.com"}}}]`, "")
		}, sc("/steps/0/time"), CodeInvalidTimeFault},
		{"a time step with offset and absolute", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"offset":"+1d","absolute":"1970-01-01T00:00:00Z"}}]`, "")
		}, sc("/steps/0/time/absolute"), CodeInvalidTimeFault},
		{"a time step that is silent and has an offset", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"answer":"silence","offset":"+1d"}}]`, "")
		}, sc("/steps/0/time/offset"), CodeInvalidTimeFault},
		{"a time step with a kiss of death and no code", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"answer":"kiss_of_death"}}]`, "")
		}, sc("/steps/0/time/kiss_code"), CodeInvalidTimeFault},
		{"a time step over tcp", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"protocol":"tcp","offset":"+1d"}}]`, "")
		}, sc("/steps/0/time/protocol"), CodeInvalidTimeFault},
		{"a time step for the uplink", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"destination":{"uplink":true},"offset":"+1d"}}]`, "")
		}, sc("/steps/0/time/destination"), CodeInvalidTimeFault},
		{"a time step with a network that does not exist", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"destination":{"network":"ghost"},"offset":"+1d"}}]`, "")
		}, sc("/steps/0/time/destination/network"), CodeUnknownReference},
		{"a time step that widens the target", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","target":{"network":"IoT"},"time":{"offset":"+1d"}}]`, "")
		}, sc("/steps/0/target"), CodeTargetWidened},
		{"a time step on a remote network", func(t *testing.T, d doc) {
			remoteStep(t, d, `{"id":"a","at":"0s","target":{"remote_network":{"client":"lab-rA"}},"time":{"offset":"+1d"}}`)
		}, sc("/steps/0/time"), CodeTargetKind},
		{"a profile step with a time part on a remote network", func(t *testing.T, d doc) {
			remoteStep(t, d, `{"id":"a","at":"0s","target":{"remote_network":{"client":"lab-rA"}},"profile":"clock-in-1970"}`)
		}, sc("/steps/0/profile"), CodeTargetKind},
		{"a step that removes a wait", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"w","at":"0s","wait":{"for":"ntp_query_seen","timeout":"10m"}},{"id":"b","at":"1s","remove":"w"}]`, "")
		}, sc("/steps/1/remove"), CodeStepOrder},
		{"a step with a time and a fault", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","time":{"offset":"+1d"},"fault":{"blackout":true}}]`, "")
		}, sc("/steps/0"), CodeStepKind},
	})
}

func TestTimeStepsOfEveryKindAreAcceptedInAScenario(t *testing.T) {
	for _, target := range []string{`{"device":"esp32-42"}`, `{"group":"sensors"}`, `{"network":"IoT"}`} {
		d := baseDoc(t)
		d.set(t, obj(t, target), "scenarios", idScenario, "target")
		scenarioWith(t, d, `[
		  {"id":"up","at":"0s","wait":{"for":"ntp_query_seen","timeout":"10m"}},
		  {"id":"year","at":"1s","time":{"offset":"+1y"}},
		  {"id":"year2","at":"2s","time":{"destination":{"hostname":"time.google.com"},"offset":"-1y2d","skew_ppm":100}},
		  {"id":"y1970","at":"3s","time":{"absolute":"1970-01-01T00:00:00Z"}},
		  {"id":"leap","at":"4s","time":{"absolute":"2026-12-31T23:55:00Z","leap":"insert"}},
		  {"id":"unsync","at":"5s","time":{"answer":"unsynchronized"}},
		  {"id":"kod","at":"6s","time":{"answer":"kiss_of_death","kiss_code":"DENY"}},
		  {"id":"silence","at":"7s","time":{"answer":"silence","destination":{"cidr":"203.0.113.0/24"}}},
		  {"id":"bad1","at":"8s","time":{"answer":"origin_mismatch"}},
		  {"id":"bad2","at":"9s","time":{"answer":"zero_transmit"}},
		  {"id":"bad3","at":"10s","time":{"answer":"wrong_mode"}},
		  {"id":"part","at":"11s","profile":"clock-in-1970"},
		  {"id":"gone","at":"20s","remove":"year"},
		  {"id":"gone2","at":"21s","remove":"part"}]`, `[
		  {"ntp_queried":{},"window":{"from":"year","until":"year2"}},
		  {"name":"asked after the jump","ntp_queried":{},"window":{"from":"start","within":"30s"}}]`)
		wantValid(t, d.validate(t))
	}
}

func TestTheWaitForAnNTPQueryHasNoSelector(t *testing.T) {
	wait := func(extra string) func(*testing.T, doc) {
		return func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"ntp_query_seen","timeout":"10m"`+extra+`}}]`, "")
		}
	}
	runMutations(t, []mutation{
		{"a wait for a query with a name", wait(`,"name":"time.google.com"`), sc("/steps/0/wait/name"), CodeUnexpectedField},
		{"a wait for a query with ports", wait(`,"protocol":"udp","ports":[123]`), sc("/steps/0/wait/ports"), CodeUnexpectedField},
		{"a wait for a query with a protocol", wait(`,"protocol":"udp"`), sc("/steps/0/wait/protocol"), CodeUnexpectedField},
		{"a wait for a query with port ranges", wait(`,"protocol":"udp","port_ranges":[{"from":100,"to":200}]`), sc("/steps/0/wait/port_ranges"), CodeUnexpectedField},
		{"a wait for a query with a destination", wait(`,"destination":{"hostname":"time.google.com"}`), sc("/steps/0/wait/destination"), CodeUnexpectedField},
		{"a wait for a query that never times out", func(t *testing.T, d doc) {
			scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"ntp_query_seen","timeout":"0s"}}]`, "")
		}, sc("/steps/0/wait/timeout"), CodeInvalidDuration},
	})
	d := baseDoc(t)
	scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"ntp_query_seen","timeout":"30m"}}]`, "")
	wantValid(t, d.validate(t))
	// a wait for a connection keeps its rules
	d = baseDoc(t)
	scenarioWith(t, d, `[{"id":"a","at":"0s","wait":{"for":"connected","protocol":"udp","ports":[123],"timeout":"10s"}}]`, "")
	wantValid(t, d.validate(t))
}

func TestTheCheckForAnNTPQuery(t *testing.T) {
	one := `[{"id":"a","at":"0s","time":{"offset":"+1d"}},{"id":"b","at":"10s","remove":"a"}]`
	runMutations(t, []mutation{
		{"an ntp check with a field", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"ntp_queried":{"destination":{"hostname":"time.google.com"}},"window":{"from":"a","within":"5s"}}]`)
		}, sc("/checks/0/ntp_queried/destination"), CodeUnexpectedField},
		{"an ntp check next to another type", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"ntp_queried":{},"reconnected":{},"window":{"from":"a","within":"5s"}}]`)
		}, sc("/checks/0"), CodeStepKind},
		{"an ntp check with a window from an unknown step", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"ntp_queried":{},"window":{"from":"ghost","within":"5s"}}]`)
		}, sc("/checks/0/window/from"), CodeCheckWindow},
		{"an ntp check with a window of zero length", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"ntp_queried":{},"window":{"from":"a","within":"0s"}}]`)
		}, sc("/checks/0/window/within"), CodeInvalidDuration},
		{"an ntp check with a window that ends before it starts", func(t *testing.T, d doc) {
			scenarioWith(t, d, one, `[{"ntp_queried":{},"window":{"from":"b","until":"a"}}]`)
		}, sc("/checks/0/window/until"), CodeCheckWindow},
	})
	d := baseDoc(t)
	scenarioWith(t, d, one, `[{"ntp_queried":{},"window":{"from":"a","until":"b"}}]`)
	wantValid(t, d.validate(t))
}

func TestTheStaleClockScenarioOfTheExamplesIsValidAndResolves(t *testing.T) {
	cfg := exampleConfiguration(t)
	sc := deref(cfg.Scenarios)[idStaleClock]
	out, errs := ResolveScenario(cfg, &sc)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if *out.Target.Device != idESP {
		t.Errorf("the target is the UUID: %v", out.Target)
	}
	if kinds := []string{StepKind(&out.Steps[0]), StepKind(&out.Steps[1]), StepKind(&out.Steps[3])}; strings.Join(kinds, ",") != "wait,time,remove" {
		t.Errorf("kinds = %v", kinds)
	}
	if got := CheckKind(&(*out.Checks)[0]); got != "ntp_queried" {
		t.Errorf("check = %q", got)
	}
	if ScenarioUsesDNS(out) {
		t.Error("a time step for every server needs no name")
	}
}

// ---- the conversion of a run ---------------------------------------------------------------

func TestATimeStepIsTheOverlayItWrites(t *testing.T) {
	sc := &model.Scenario{Target: model.Scope{Network: ptr("n1")}, Steps: []model.Step{
		{Id: "a", At: "0s", Time: &model.TimeFault{Offset: ptr("+400d")}},
		{Id: "b", At: "0s", Target: &model.Scope{Device: ptr("d1")}, Time: &model.TimeFault{Answer: ptr(model.TimeFaultAnswerSilence)}},
		{Id: "c", At: "0s", Wait: &model.WaitCondition{For: model.WaitConditionForNtpQuerySeen, Timeout: "10m"}},
		{Id: "d", At: "0s", Remove: ptr("a")},
	}}
	a, err := StepRequest(sc, &sc.Steps[0])
	if err != nil || a.Target == nil || *a.Target.Network != "n1" || a.Time == nil || *a.Time.Offset != "+400d" || a.Fault != nil {
		t.Errorf("%v %+v", err, a)
	}
	b, _ := StepRequest(sc, &sc.Steps[1])
	if b.Target == nil || *b.Target.Device != "d1" || b.Time == nil || *b.Time.Answer != model.TimeFaultAnswerSilence {
		t.Errorf("a step's own target: %+v", b)
	}
	if _, err := StepRequest(sc, &sc.Steps[2]); err == nil {
		t.Error("a wait writes no overlay")
	}
	if kind, err := OverlayKindOf(&a); err != nil || kind != "time" {
		t.Errorf("the request is a time overlay: %q %v", kind, err)
	}
	if !StepCreatesOverlay(&sc.Steps[0]) || StepCreatesOverlay(&sc.Steps[2]) || StepKind(&sc.Steps[0]) != "time" || StepKind(&sc.Steps[2]) != "wait" {
		t.Error("the kinds")
	}
	// the request is a copy: changing it leaves the scenario alone
	*a.Time.Offset = "-1d"
	if *sc.Steps[0].Time.Offset != "+400d" {
		t.Error("the request shares the scenario's body")
	}
}

func TestACheckOfTheNTPQueryHasItsKind(t *testing.T) {
	c := model.Check{NtpQueried: &model.NtpQueriedCheck{}, Window: model.CheckWindow{From: "start"}}
	if got := CheckKind(&c); got != "ntp_queried" {
		t.Errorf("%q", got)
	}
	c.Reconnected = &model.TrafficMatch{}
	if got := CheckKind(&c); got != "" {
		t.Errorf("two types: %q", got)
	}
}

func TestAScenarioWithATimeFaultForAHostNameUsesTheGatewaysDNS(t *testing.T) {
	host := &model.Destination{Hostname: ptr("time.google.com")}
	for name, tc := range map[string]struct {
		sc   model.Scenario
		want bool
	}{
		"a time fault for a name":       {model.Scenario{Steps: []model.Step{{Id: "a", Time: &model.TimeFault{Destination: host, Offset: ptr("+1d")}}}}, true},
		"a time fault for an address":   {model.Scenario{Steps: []model.Step{{Id: "a", Time: &model.TimeFault{Destination: &model.Destination{Cidr: ptr("203.0.113.7")}, Offset: ptr("+1d")}}}}, false},
		"a time fault for every server": {model.Scenario{Steps: []model.Step{{Id: "a", Time: &model.TimeFault{Offset: ptr("+1d")}}}}, false},
		"a wait for a query":            {model.Scenario{Steps: []model.Step{{Id: "a", Wait: &model.WaitCondition{For: model.WaitConditionForNtpQuerySeen}}}}, false},
		"a check of the query":          {model.Scenario{Steps: []model.Step{{Id: "a"}}, Checks: &[]model.Check{{NtpQueried: &model.NtpQueriedCheck{}}}}, false},
	} {
		if got := ScenarioUsesDNS(&tc.sc); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestParametersChangeTheTimeStepOfAScenario(t *testing.T) {
	cfg := exampleConfiguration(t)
	sc := deref(cfg.Scenarios)[idStaleClock]
	got, err := ParameterizeScenario(&sc, map[string]string{"steps.ahead.time.offset": "-90s", "steps.ahead.time.skew_ppm": "250"})
	if err != nil {
		t.Fatal(err)
	}
	tf := got.Steps[1].Time
	if tf == nil || *tf.Offset != "-90s" || tf.SkewPpm == nil || *tf.SkewPpm != 250 {
		t.Fatalf("step = %+v", got.Steps[1])
	}
	if errs := ValidateScenario(cfg, got); len(errs) != 0 {
		t.Errorf("errors: %v", errs)
	}
	// the schema refuses what is no span
	if _, err := ParameterizeScenario(&sc, map[string]string{"steps.ahead.time.offset": "soon"}); err == nil {
		t.Error("an offset that is no span must be refused")
	}
	// and a parameter that turns the fault into a contradiction is found by the domain
	got, err = ParameterizeScenario(&sc, map[string]string{"steps.ahead.time.absolute": "1970-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if errs := ValidationErrors(ValidateScenario(cfg, got)); !errs.Has("/steps/1/time/absolute", CodeInvalidTimeFault) {
		t.Errorf("errors: %v", errs)
	}
}

func TestARunRequestMayCarryATimeScenario(t *testing.T) {
	req, err := DecodeRunRequest([]byte(`{"inline":{"name":"stale","target":{"device":"placeholder"},"steps":[
	  {"id":"w","at":"0s","wait":{"for":"ntp_query_seen","timeout":"10m"}},
	  {"id":"t","at":"1s","time":{"offset":"+400d","destination":{"hostname":"time.google.com"}}}],
	  "checks":[{"ntp_queried":{},"window":{"from":"t","within":"10m"}}]},"parameters":{"target.device":"esp32-42"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Inline.Steps[1].Time == nil || req.Inline.Checks == nil || (*req.Inline.Checks)[0].NtpQueried == nil {
		t.Fatalf("request = %+v", req.Inline)
	}
	// an unknown field of a time step is refused like every unknown field
	if _, err := DecodeRunRequest([]byte(`{"inline":{"name":"x","target":{"device":"d"},"steps":[{"id":"t","at":"1s","time":{"offset":"+1d","jitter":"5ms"}}]}}`)); err == nil {
		t.Error("an unknown field of a time fault must be refused")
	}
}

// The examples show what the plan promises (§2.18): every effect has one, and the answers the server can give.
func TestTheOverlayExamplesShowEveryEffectOfATimeFault(t *testing.T) {
	doc, err := ParseDocument(readExample(t, "overlays.yaml"), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range doc.([]any) {
		var req model.OverlayRequest
		if err := decodeInto(item, &req); err != nil {
			t.Fatal(err)
		}
		if req.Time == nil {
			continue
		}
		tf := req.Time
		for name, present := range map[string]bool{
			"offset": tf.Offset != nil, "absolute": tf.Absolute != nil, "skew": tf.SkewPpm != nil, "leap": tf.Leap != nil,
			"destination": tf.Destination != nil, "kiss": tf.Answer != nil && *tf.Answer == model.TimeFaultAnswerKissOfDeath,
			"silence": tf.Answer != nil && *tf.Answer == model.TimeFaultAnswerSilence,
			// the presets of the plan (§2.18): the dates and spans a device is known to break on, and the answers
			"2038":           tf.Absolute != nil && tf.Absolute.Year() == 2038,
			"ntp era 1":      tf.Absolute != nil && tf.Absolute.Year() == 2036,
			"a year ahead":   tf.Offset != nil && *tf.Offset == "+1y",
			"a day behind":   tf.Offset != nil && *tf.Offset == "-1d",
			"unsynchronized": tf.Answer != nil && *tf.Answer == model.TimeFaultAnswerUnsynchronized,
			"invalid answer": tf.Answer != nil && *tf.Answer == model.TimeFaultAnswerOriginMismatch,
		} {
			seen[name] = seen[name] || present
		}
	}
	for _, name := range []string{"offset", "absolute", "skew", "leap", "destination", "kiss", "silence",
		"2038", "ntp era 1", "a year ahead", "a day behind", "unsynchronized", "invalid answer"} {
		if !seen[name] {
			t.Errorf("no time fault example shows %s", name)
		}
	}
}
