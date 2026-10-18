package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// scenarioDoc is a scenario as the generic document the parameters change.
func scenarioDoc(t *testing.T, src string) map[string]any {
	t.Helper()
	v, err := ParseDocument([]byte(src), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

const paramScenario = `
name: p
target: {device: esp32-42}
capture: false
steps:
  - {id: normal, at: 0s, profile: normal}
  - {id: slow, at: 10s, fault: {latency: 200ms, loss: 1%}}
  - {id: cut, at: 20s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`

func TestParametersChangeAScenarioByDottedPath(t *testing.T) {
	doc := scenarioDoc(t, paramScenario)
	errs := ApplyParameters(doc, map[string]string{
		"steps.slow.fault.latency": "300ms", // an element by its id
		"steps.2.at":               "25s",   // an element by its position
		"steps.cut.rule.ports":     "[8884, 8885]",
		"capture":                  "true", // a boolean that is there
		"name":                     "true", // a string stays a string
		"preconditions.skip":       `["target_online"]`,
		"steps.slow.fault.jitter":  "20ms", // a field that is not there yet
		"steps.slow.fault.reorder": "5",    // a whole number that is not there yet
	})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	steps := doc["steps"].([]any)
	slow := steps[1].(map[string]any)["fault"].(map[string]any)
	cut := steps[2].(map[string]any)
	if slow["latency"] != "300ms" || slow["jitter"] != "20ms" || slow["reorder"] != json.Number("5") || cut["at"] != "25s" ||
		doc["capture"] != true || doc["name"] != "true" || len(cut["rule"].(map[string]any)["ports"].([]any)) != 2 {
		t.Errorf("%v", doc)
	}
	if skip := doc["preconditions"].(map[string]any)["skip"].([]any); len(skip) != 1 || skip[0] != "target_online" {
		t.Errorf("%v", doc["preconditions"])
	}
}

func TestSettingAMemberOfATargetReplacesTheTarget(t *testing.T) {
	doc := scenarioDoc(t, paramScenario)
	doc["steps"].([]any)[1].(map[string]any)["target"] = map[string]any{"device": "a"}
	errs := ApplyParameters(doc, map[string]string{"target.network": "IoT", "steps.slow.target.group": "sensors"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := doc["target"].(map[string]any); len(got) != 1 || got["network"] != "IoT" {
		t.Errorf("target %v", got)
	}
	if got := doc["steps"].([]any)[1].(map[string]any)["target"].(map[string]any); len(got) != 1 || got["group"] != "sensors" {
		t.Errorf("step target %v", got)
	}
}

func TestAParameterThatCannotBeAppliedIsReportedAtItsPath(t *testing.T) {
	doc := scenarioDoc(t, paramScenario)
	errs := ApplyParameters(doc, map[string]string{
		"steps.nope.at":      "1s",
		"steps.7.at":         "1s",
		"name.deeper":        "x",
		"capture.x":          "x",
		"":                   "x",
		"a..b":               "x",
		"capture2":           "[1,",
		"steps.slow.at.more": "1s",
	})
	got := map[string]bool{}
	for _, e := range errs {
		got[e.Path] = true
		if e.Code != CodeInvalidParameter {
			t.Errorf("%+v", e)
		}
	}
	for _, want := range []string{"/parameters/steps.nope.at", "/parameters/steps.7.at", "/parameters/name.deeper", "/parameters/a..b", "/parameters/", "/parameters/capture2", "/parameters/steps.slow.at.more"} {
		if !got[want] {
			t.Errorf("no error at %s: %v", want, errs)
		}
	}
	// a value of the wrong kind for a field that is a boolean or a number
	doc = scenarioDoc(t, paramScenario)
	errs = ApplyParameters(doc, map[string]string{"capture": "maybe"})
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "true or false") {
		t.Errorf("%v", errs)
	}
}

func TestParameterizeScenarioChecksTheResult(t *testing.T) {
	sc, err := DecodeScenario([]byte(paramScenario), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParameterizeScenario(sc, map[string]string{"steps.slow.fault.latency": "250ms", "target.device": "lab-host"})
	if err != nil {
		t.Fatal(err)
	}
	if *got.Steps[1].Fault.Latency != "250ms" || *got.Target.Device != "lab-host" {
		t.Errorf("%+v", got)
	}
	if *sc.Steps[1].Fault.Latency != "200ms" || *sc.Target.Device != "esp32-42" {
		t.Error("the scenario given was changed")
	}
	// a parameter that makes the scenario invalid is a validation error of the schema
	_, err = ParameterizeScenario(sc, map[string]string{"steps.slow.at": "soon"})
	var ve ValidationErrors
	if !asValidation(err, &ve) || len(ve) == 0 {
		t.Errorf("%v", err)
	}
	_, err = ParameterizeScenario(sc, map[string]string{"steps.nope.at": "1s"})
	if !asValidation(err, &ve) || ve[0].Path != "/parameters/steps.nope.at" {
		t.Errorf("%v", err)
	}
	if same, err := ParameterizeScenario(sc, nil); err != nil || same.Name != "p" {
		t.Errorf("%v", err)
	}
}

func TestDecodeRunRequest(t *testing.T) {
	ok := func(body string) *model.RunRequest {
		t.Helper()
		r, err := DecodeRunRequest([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return r
	}
	if r := ok(`{"scenario":"mqtt-outage","parameters":{"target.device":"esp32-42"},"lease":"30s"}`); *r.Scenario != "mqtt-outage" || *r.Lease != "30s" || (*r.Parameters)["target.device"] != "esp32-42" {
		t.Errorf("%+v", r)
	}
	// an inline scenario may leave out the target when the parameters give it; the request is returned as sent
	r := ok(`{"inline":{"name":"x","steps":[{"id":"a","at":"0s","restore":true}]},"parameters":{"target.device":"esp32-42"}}`)
	if r.Inline == nil || r.Inline.Target.Device != nil {
		t.Errorf("%+v", r.Inline)
	}
	for name, body := range map[string]string{
		"neither":                   `{}`,
		"both":                      `{"scenario":"a","inline":{"name":"x","target":{"device":"d"},"steps":[{"id":"a","at":"0s","restore":true}]}}`,
		"an unknown field":          `{"scenario":"a","bogus":true}`,
		"an inline without target":  `{"inline":{"name":"x","steps":[{"id":"a","at":"0s","restore":true}]}}`,
		"a parameter that fits not": `{"inline":{"name":"x","target":{"device":"d"},"steps":[{"id":"a","at":"0s","restore":true}]},"parameters":{"steps.zzz.at":"1s"}}`,
		"a short lease":             `{"scenario":"a","lease":"999ms"}`,
		"a lease that is no time":   `{"scenario":"a","lease":"soon"}`,
		"not an object":             `[1]`,
	} {
		if _, err := DecodeRunRequest([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var pe *ParseError
	if _, err := DecodeRunRequest([]byte(`{`)); !asParseError(err, &pe) {
		t.Errorf("%v", err)
	}
}

func TestDecodeRunOptions(t *testing.T) {
	if o, err := DecodeRunOptions(nil); err != nil || o.Lease != nil || o.Parameters != nil {
		t.Errorf("%v %+v", err, o)
	}
	if o, err := DecodeRunOptions([]byte(`{"lease":"10s","parameters":{"a":"b"}}`)); err != nil || *o.Lease != "10s" {
		t.Errorf("%v %+v", err, o)
	}
	for _, body := range []string{`{"lease":"1ms"}`, `{"scenario":"x"}`, `{"parameters":{"a":1}}`} {
		if _, err := DecodeRunOptions([]byte(body)); err == nil {
			t.Errorf("%s: accepted", body)
		}
	}
}

func TestAStepIsTheOverlayItWrites(t *testing.T) {
	sc := &model.Scenario{Target: model.Scope{Network: ptr("n1")}, Steps: []model.Step{
		{Id: "a", At: "0s", Fault: &model.FaultBody{Latency: ptr("5ms")}},
		{Id: "b", At: "0s", Target: &model.Scope{Device: ptr("d1")}, Rule: &model.AccessRuleBody{Action: "drop"}},
		{Id: "c", At: "0s", Wireguard: &model.WireGuardAction{Client: ptr("c1"), Action: "disable"}},
		{Id: "d", At: "0s", Fault: &model.FaultBody{Family: ptr(model.FaultBodyFamilyTunnel), Tunnel: &model.TunnelRef{Link: ptr("l1")}, Latency: ptr("5ms")}},
		{Id: "e", At: "0s", Restore: ptr(model.StepRestore(true))},
		{Id: "f", At: "0s", Remove: ptr("a")},
	}}
	a, err := StepRequest(sc, &sc.Steps[0])
	if err != nil || a.Target == nil || *a.Target.Network != "n1" || a.Fault == nil {
		t.Errorf("%v %+v", err, a)
	}
	b, _ := StepRequest(sc, &sc.Steps[1])
	if *b.Target.Device != "d1" || b.Rule == nil {
		t.Errorf("a step's own target: %+v", b)
	}
	if c, _ := StepRequest(sc, &sc.Steps[2]); c.Target != nil || c.Wireguard == nil {
		t.Errorf("a WireGuard action has no target: %+v", c)
	}
	if d, _ := StepRequest(sc, &sc.Steps[3]); d.Target != nil || d.Fault == nil {
		t.Errorf("a tunnel fault has no target: %+v", d)
	}
	for _, i := range []int{4, 5} {
		if _, err := StepRequest(sc, &sc.Steps[i]); err == nil {
			t.Errorf("step %d writes no overlay", i)
		}
	}
	// the request is a copy: changing it leaves the scenario alone
	a.Fault.Latency = ptr("99ms")
	if *sc.Steps[0].Fault.Latency != "5ms" {
		t.Error("the request shares the scenario's body")
	}
	if StepKind(&sc.Steps[4]) != "restore" || !StepCreatesOverlay(&sc.Steps[0]) || StepCreatesOverlay(&sc.Steps[5]) {
		t.Error("the kinds")
	}
}

func TestTheTimelineIsByOffsetAndThenByPosition(t *testing.T) {
	steps := []model.Step{{Id: "late", At: "20s"}, {Id: "b", At: "5s"}, {Id: "a", At: "5s"}, {Id: "first", At: "0s"}}
	var got []string
	for _, i := range TimelineOrder(steps) {
		got = append(got, steps[i].Id)
	}
	if strings.Join(got, ",") != "first,b,a,late" {
		t.Errorf("%v", got)
	}
}

func TestAScenarioWithHostNamesUsesTheGatewaysDNS(t *testing.T) {
	host := &model.Destination{Hostname: ptr("broker.example.com")}
	for name, tc := range map[string]struct {
		sc   model.Scenario
		want bool
	}{
		"nothing":        {model.Scenario{Steps: []model.Step{{Id: "a", Fault: &model.FaultBody{}}}}, false},
		"a dns step":     {model.Scenario{Steps: []model.Step{{Id: "a", Dns: &model.DnsFault{}}}}, true},
		"a fault":        {model.Scenario{Steps: []model.Step{{Id: "a", Fault: &model.FaultBody{Destination: host}}}}, true},
		"a rule":         {model.Scenario{Steps: []model.Step{{Id: "a", Rule: &model.AccessRuleBody{Destination: host}}}}, true},
		"a tls case":     {model.Scenario{Steps: []model.Step{{Id: "a", Tls: &model.TlsCase{Destination: host}}}}, true},
		"a dns wait":     {model.Scenario{Steps: []model.Step{{Id: "a", Wait: &model.WaitCondition{For: model.WaitConditionForDnsQuery}}}}, true},
		"a name wait":    {model.Scenario{Steps: []model.Step{{Id: "a", Wait: &model.WaitCondition{For: model.WaitConditionForConnected, Destination: host}}}}, true},
		"an online wait": {model.Scenario{Steps: []model.Step{{Id: "a", Wait: &model.WaitCondition{For: model.WaitConditionForOnline}}}}, false},
		"a dns check":    {model.Scenario{Steps: []model.Step{{Id: "a"}}, Checks: &[]model.Check{{DnsQuerySeen: &model.DnsQueryCheck{Name: "x.example.com"}}}}, true},
		"a name check":   {model.Scenario{Steps: []model.Step{{Id: "a"}}, Checks: &[]model.Check{{Reconnected: &model.TrafficMatch{Destination: host}}}}, true},
		"an address":     {model.Scenario{Steps: []model.Step{{Id: "a", Fault: &model.FaultBody{Destination: &model.Destination{Cidr: ptr("10.0.0.1")}}}}}, false},
	} {
		if got := ScenarioUsesDNS(&tc.sc); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestARevisionOrphansTheScenarioOfARunThatRefersToWhatItDeletes(t *testing.T) {
	norm, errs := Normalize(exampleConfiguration(t))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	next := clone(*norm)
	devices := map[string]model.Device{}
	for id, dev := range *next.Devices {
		if id != idESP {
			devices[id] = dev
		}
	}
	next.Devices = &devices
	groups := map[string]model.Group{}
	for id, g := range *next.Groups {
		if id != idSensors {
			groups[id] = g
		}
	}
	next.Groups = &groups
	sc := &model.Scenario{Target: model.Scope{Device: ptr(idESP)}, Steps: []model.Step{
		{Id: "a", At: "0s", Target: &model.Scope{Device: ptr(idESP)}, Fault: &model.FaultBody{Latency: ptr("5ms")}},
		{Id: "p", At: "1s", Profile: ptr("normal")},
	}}
	got := OrphanedScenario(norm, &next, sc)
	if len(got) != 1 || got[0] != "/devices/"+idESP {
		t.Errorf("%v", got)
	}
	// a profile of the catalogue is never an orphan, and what the revision keeps is not
	if got := OrphanedScenario(norm, norm, sc); len(got) != 0 {
		t.Errorf("%v", got)
	}
	grp := &model.Scenario{Target: model.Scope{Group: ptr(idSensors)}, Steps: sc.Steps[1:]}
	if got := OrphanedScenario(norm, &next, grp); len(got) != 1 || got[0] != "/groups/"+idSensors {
		t.Errorf("%v", got)
	}
}

func TestCheckKindNamesTheOneTypeOfACheck(t *testing.T) {
	m := &model.TrafficMatch{}
	win := model.CheckWindow{From: "start"}
	for want, c := range map[string]model.Check{
		"reconnected":      {Reconnected: m, Window: win},
		"no_connection":    {NoConnection: m, Window: win},
		"traffic_seen":     {TrafficSeen: m, Window: win},
		"traffic_not_seen": {TrafficNotSeen: &model.TrafficNotSeenCheck{}, Window: win},
		"dns_query_seen":   {DnsQuerySeen: &model.DnsQueryCheck{Name: "a.example.com"}, Window: win},
		"tls_rejected":     {TlsRejected: m, Window: win},
		"tls_accepted":     {TlsAccepted: m, Window: win},
		"":                 {Window: win},
	} {
		if got := CheckKind(&c); got != want {
			t.Errorf("%q: %q", want, got)
		}
	}
	if got := CheckKind(&model.Check{Reconnected: m, NoConnection: m}); got != "" {
		t.Errorf("two types: %q", got)
	}
}
