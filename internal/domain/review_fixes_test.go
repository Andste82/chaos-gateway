package domain

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

const discoveredID = "11111111-2222-4333-8444-555555555555"

func TestDiscoveredDevicesCanBeReferencedAndGrouped(t *testing.T) {
	// an overlay on a discovered device
	_, errs := validateOverlay(t, `{target: {device: `+discoveredID+`}, fault: {blackout: true}}`)
	wantError(t, errs, "/target/device", CodeUnknownReference)
	out, errs := ValidateOverlay(exampleConfiguration(t), requestOf(t, `{target: {device: `+strings.ToUpper(discoveredID)+`}, fault: {blackout: true}}`), WithDiscovered(discoveredID))
	if len(errs) != 0 || *out.Target.Device != discoveredID {
		t.Fatalf("errors %v, target %v", errs, out.Target)
	}

	// a group member before adoption
	d := baseDoc(t)
	d.set(t, []any{"esp32-42", discoveredID}, "groups", idSensors, "members")
	wantError(t, d.validate(t), "/groups/"+idSensors+"/members/1", CodeUnknownReference)
	raw, _ := json.Marshal(d)
	cfg, err := DecodeConfiguration(raw, FormatJSON)
	_ = cfg
	if err == nil {
		t.Fatal("without the observed state the member is unknown")
	}
	if errs := Validate(mustDecodeLoose(t, raw), WithDiscovered(discoveredID)); len(errs) != 0 {
		t.Fatalf("with the observed state: %v", errs)
	}
	// and a configuration candidate keeps it as a UUID
	got, err := NewCandidate(nil, raw, FormatJSON, CandidateFull, t0, WithDiscovered(discoveredID))
	if err != nil {
		t.Fatal(err)
	}
	if m := *deref(got.Groups)[idSensors].Members; m[1] != discoveredID {
		t.Fatalf("members = %v", m)
	}
}

func mustDecodeLoose(t *testing.T, raw []byte) *model.Configuration {
	t.Helper()
	doc, err := ParseDocument(raw, FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeLoose(doc)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAManagementHubMayBeInTheAllowedSourcesATestHubMayNot(t *testing.T) {
	d := baseDoc(t)
	d.set(t, "management", "networks", idHub, "role")
	d.set(t, []any{"10.99.0.0/24"}, "management", "allowed_sources")
	wantValid(t, d.validate(t))

	d = baseDoc(t) // role test: its client networks are test traffic too
	d.set(t, []any{"10.50.0.0/16"}, "management", "allowed_sources")
	wantError(t, d.validate(t), "/management/allowed_sources/0", CodeManagementOverlap)
}

func TestRulesThatAreNotInTheSpecDoNotRejectValidDocuments(t *testing.T) {
	d := baseDoc(t)
	d.set(t, "normal", "faults", idFaultNet, "distribution")  // a distribution without a jitter
	d.set(t, "20%", "faults", idFaultNet, "loss_correlation") // a correlation without loss
	d.set(t, obj(t, `{"action":"short_ttl","ttl":"0s"}`), "profiles", idProfile, "parts", "dns")
	d.del(t, "devices", idLab, "identifiers") // adopted before the MAC is known
	wantValid(t, d.validate(t))
}

func TestDeviceRangesMayBeNestedAClientAddressInsideARangeIsFine(t *testing.T) {
	d := baseDoc(t)
	d.del(t, "devices", idESP, "fixed_ip") // a reservation needs a MAC; this device is IP-identified here
	d.set(t, obj(t, `{"ipv4":["10.50.0.0/24"]}`), "devices", idESP, "identifiers")
	d.set(t, obj(t, `{"ipv4":["10.50.0.0/25","10.50.0.10"]}`), "devices", idLab, "identifiers")
	wantValid(t, d.validate(t))
	d.set(t, obj(t, `{"ipv4":["10.99.0.0/24"]}`), "devices", idESP, "identifiers") // the hub subnet holds a client address
	wantValid(t, d.validate(t))
}

func TestAnAccessRuleThatAllowsAndCutsIsRejected(t *testing.T) {
	r := "/access_rules/" + idRule
	d := baseDoc(t)
	d.set(t, "allow", "access_rules", idRule, "action")
	d.set(t, true, "access_rules", idRule, "cut_existing")
	wantError(t, d.validate(t), r+"/cut_existing", CodeCutWithAllow)
}

func TestABlackoutOfFalseIsNoFault(t *testing.T) {
	d := baseDoc(t)
	d.set(t, obj(t, `{"source":{"network":"IoT"},"blackout":false}`), "faults", idNew)
	wantError(t, d.validate(t), faultPath(idNew), CodeEmptyFault)
	d.set(t, obj(t, `{"source":{"network":"IoT"},"blackout":false,"latency":"5ms"}`), "faults", idNew)
	wantValid(t, d.validate(t))
}

func TestAnExceptDestinationWithHostBitsIsRejected(t *testing.T) {
	d := baseDoc(t)
	scenarioWith(t, d, `[{"id":"a","at":"0s","fault":{"blackout":true}}]`,
		`[{"traffic_not_seen":{"except":[{"cidr":"10.0.0.5/8"}]},"window":{"from":"a","within":"5s"}}]`)
	wantError(t, d.validate(t), sc("/checks/0/traffic_not_seen/except/0/cidr"), CodeHostBitsSet)
}

func TestDuplicateKeysInAJSONDocumentAreRejected(t *testing.T) {
	for _, doc := range []string{
		`{"a":1,"a":2}`,
		`{"a":{"b":1,"b":2}}`,
		`{"a":[{"x":1},{"y":1,"y":2}]}`,
		`{"schema_version":1,"schema_version":1}`,
	} {
		_, err := ParseDocument([]byte(doc), FormatJSON)
		var pe *ParseError
		if !asParseError(err, &pe) || !strings.Contains(err.Error(), "duplicate key") {
			t.Errorf("%s: %v", doc, err)
		}
	}
	for _, ok := range []string{`{"a":1,"b":{"a":2}}`, `[{"a":1},{"a":2}]`, `{"a":[1,1],"b":"a"}`, `"a"`, `5`} {
		if _, err := ParseDocument([]byte(ok), FormatJSON); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	// the error names the place
	_, err := ParseDocument([]byte(`{"networks":{"x":{"name":"a","name":"b"}}}`), FormatJSON)
	if err == nil || !strings.Contains(err.Error(), "/networks/x") {
		t.Errorf("the message must name the object: %v", err)
	}
}

func TestOverlayKeysTreatAnAddressAndItsHostPrefixAlike(t *testing.T) {
	key := func(body string) string {
		out, errs := validateOverlay(t, body)
		if len(errs) != 0 {
			t.Fatalf("%v", errs)
		}
		k, _ := OverlayKey(admin, out)
		return k
	}
	if a, b := key(`{target: {global: true}, fault: {destination: {cidr: 203.0.113.1}, latency: 5ms}}`),
		key(`{target: {global: true}, fault: {destination: {cidr: 203.0.113.1/32}, latency: 5ms}}`); a != b {
		t.Errorf("an address and its /32 must give one key:\n%s\n%s", a, b)
	}
	if a, b := key(`{target: {global: true}, dns: {names: [a.test, b.test], action: servfail}}`),
		key(`{target: {global: true}, dns: {names: [B.TEST, a.test], action: servfail}}`); a != b {
		t.Errorf("the order and case of the names must not change the key:\n%s\n%s", a, b)
	}
}

func TestNewOverlayChecksTheOwnerType(t *testing.T) {
	out, _ := validateOverlay(t, `{target: {device: esp32-42}, fault: {blackout: true}}`)
	id := mustUUID("00000000-0000-4000-8000-000000000001")
	for _, ok := range []string{"user", "token", "run"} {
		if _, err := NewOverlay(*out, model.Owner{Type: model.ActorType(ok), Id: "x"}, id, t0); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"system", "", "root"} {
		if _, err := NewOverlay(*out, model.Owner{Type: model.ActorType(bad), Id: "x"}, id, t0); err == nil {
			t.Errorf("owner type %q must be refused", bad)
		}
	}
}

// ---- precedence --------------------------------------------------------------------------

func TestAFaultOnAnotherScopeDoesNotBeatANewerProfilePart(t *testing.T) {
	// the rule "a fault beats a profile part" is for the same scope only (E8): here the scopes
	// differ, both are at level 6, and the newer entry wins (E6)
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {group: sensors}, fault: {latency: 300ms}}`, time.Second)
	profile := tw.overlay(`{target: {group: g2}, profile: lte}`, time.Minute)
	win := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment)
	if win.ID != profile.Id.String() {
		t.Fatalf("the newer profile part must win: %+v", win)
	}
	// the other way round the fault is newer and wins on time alone
	tw2 := newTestWorld(t, false)
	tw2.overlay(`{target: {group: g2}, profile: lte}`, time.Second)
	fault := tw2.overlay(`{target: {group: sensors}, fault: {latency: 300ms}}`, time.Minute)
	if win := mustWinner(t, tw2.world().Resolve(toServer("tcp", 443)), FamilyImpairment); win.ID != fault.Id.String() {
		t.Fatalf("winner = %+v", win)
	}
}

func TestEqualTimesAreBrokenByTheHigherIDInEveryLayer(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.configFault("00000000-0000-4000-8000-0000000000a1", `{source: {device: esp32-42}, latency: 1ms}`, 0)
	tw.configFault("00000000-0000-4000-8000-0000000000a2", `{source: {device: esp32-42}, latency: 2ms}`, 0)
	if win := mustWinner(t, tw.world().Resolve(toServer("tcp", 443)), FamilyImpairment); win.ID != "00000000-0000-4000-8000-0000000000a2" {
		t.Fatalf("configuration layer: %s", win.ID)
	}
	tw.configFault("00000000-0000-4000-8000-0000000000b1", `{family: tunnel, tunnel: {link: site-b}, latency: 1ms}`, 0)
	tw.configFault("00000000-0000-4000-8000-0000000000b2", `{family: tunnel, tunnel: {link: site-b}, latency: 2ms}`, 0)
	if got := tw.world().ResolveTunnels(); len(got) != 1 || got[0].Winner.ID != "00000000-0000-4000-8000-0000000000b2" {
		t.Fatalf("tunnels: %+v", got)
	}
}

// All ten levels, in the configuration layer, with an address or prefix as destination.
func TestEveryPrecedenceLevelInTheConfigurationLayer(t *testing.T) {
	levels := []struct {
		level int
		yaml  string
	}{
		{1, `{source: {device: esp32-42}, destination: {cidr: 203.0.113.10}, protocol: tcp, ports: [8883], latency: 1ms}`},
		{2, `{source: {device: esp32-42}, destination: {cidr: 203.0.113.0/24}, latency: 2ms}`},
		{3, `{source: {device: esp32-42}, protocol: tcp, port_ranges: [{from: 8000, to: 9000}], latency: 3ms}`},
		{4, `{source: {device: esp32-42}, latency: 4ms}`},
		{5, `{source: {group: sensors}, destination: {network: IoT}, latency: 5ms}`}, // does not match: the server is not in IoT
		{6, `{source: {group: sensors}, latency: 6ms}`},
		{7, `{source: {network: IoT}, destination: {uplink: true}, latency: 7ms}`},
		{8, `{source: {network: IoT}, latency: 8ms}`},
		{9, `{source: {global: true}, destination: {cidr: 203.0.113.0/24}, latency: 9ms}`},
		{10, `{source: {global: true}, latency: 10ms}`},
	}
	tw := newTestWorld(t, false)
	for i, l := range levels {
		tw.configFault(fmt.Sprintf("00000000-0000-4000-8000-%012x", i+1), l.yaml, time.Duration(10-i)*time.Second)
	}
	for _, want := range []int{1, 2, 3, 4, 6, 7, 8, 9, 10} { // level 5 never matches, see above
		win := mustWinner(t, tw.world().Resolve(toServer("tcp", 8883)), FamilyImpairment)
		if win.Level != want || win.Layer != LayerConfig {
			t.Fatalf("winner has level %d, want %d", win.Level, want)
		}
		delete(*tw.cfg.Faults, fmt.Sprintf("00000000-0000-4000-8000-%012x", want))
	}
	if Winner(tw.world().Resolve(toServer("tcp", 8883)), FamilyImpairment) != nil {
		t.Fatal("only the non-matching group fault is left")
	}
}

func TestDNSAndTLSLevelsFollowTheirSelectors(t *testing.T) {
	levelOf := func(body string, q Query, family string) int {
		t.Helper()
		tw := newTestWorld(t, false)
		tw.overlay(body, time.Second)
		return mustWinner(t, tw.world().Resolve(q), family).Level
	}
	dns := dnsQuery("broker.example.com")
	for body, want := range map[string]int{
		`{target: {device: esp32-42}, dns: {names: [broker.example.com], action: servfail}}`: 2,
		`{target: {device: esp32-42}, dns: {action: servfail}}`:                              4,
		`{target: {group: sensors}, dns: {names: [broker.example.com], action: servfail}}`:   5,
		`{target: {group: sensors}, dns: {action: servfail}}`:                                6,
		`{target: {network: IoT}, dns: {names: ['*.example.com'], action: servfail}}`:        7,
		`{target: {network: IoT}, dns: {action: servfail}}`:                                  8,
		`{target: {global: true}, dns: {names: [broker.example.com], action: servfail}}`:     9,
		`{target: {global: true}, dns: {action: servfail}}`:                                  10,
	} {
		if got := levelOf(body, dns, FamilyDNS); got != want {
			t.Errorf("%s: level %d, want %d", body, got, want)
		}
	}
	tls := toServer("tcp", 8883)
	tls.SNI = "broker.example.com"
	// a TLS case always has a port part (its ports, or the default ones), so the levels start at 3
	for body, want := range map[string]int{
		`{target: {device: esp32-42}, tls: {case: expired}}`:                                      3,
		`{target: {device: esp32-42}, tls: {case: expired, ports: [8883]}}`:                       3,
		`{target: {device: esp32-42}, tls: {case: expired, sni: [broker.example.com]}}`:           1,
		`{target: {device: esp32-42}, tls: {case: expired, destination: {cidr: 203.0.113.0/24}}}`: 1,
		`{target: {group: sensors}, tls: {case: expired}}`:                                        5,
		`{target: {network: IoT}, tls: {case: expired}}`:                                          7,
		`{target: {global: true}, tls: {case: expired}}`:                                          9,
	} {
		if got := levelOf(body, tls, FamilyTLS); got != want {
			t.Errorf("%s: level %d, want %d", body, got, want)
		}
	}
}

func TestADeleteLeaseActionIsNotAStateThatCompetes(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, dhcp: {action: delete_lease}}`, time.Second)
	if Winner(tw.world().Resolve(Query{Source: subjectA}), FamilyDHCP) != nil {
		t.Fatal("deleting a lease acts once and is not a state")
	}
	tw.overlay(`{target: {device: esp32-42}, dhcp: {action: force_new_ip}}`, 2*time.Second)
	if Winner(tw.world().Resolve(Query{Source: subjectA}), FamilyDHCP) == nil {
		t.Fatal("force_new_ip lasts as long as the overlay and is a state")
	}
}

func TestTheManagementNetworkIsNotReachedViaTheUplink(t *testing.T) {
	tw := newTestWorld(t, false)
	tw.overlay(`{target: {device: esp32-42}, fault: {destination: {uplink: true}, latency: 5ms}}`, time.Second)
	w := tw.world()
	q := toServer("tcp", 443)
	q.DestIP = netip.MustParseAddr("192.168.88.7") // inside management.allowed_sources of the example
	if Winner(w.Resolve(q), FamilyImpairment) != nil {
		t.Fatal("an address of the management network is not the uplink")
	}
	q.DestIP = netip.MustParseAddr("8.8.8.8")
	if Winner(w.Resolve(q), FamilyImpairment) == nil {
		t.Fatal("an internet address is")
	}
}

// ---- identity ----------------------------------------------------------------------------

func TestAStaleNeighborOnlyCountsWhileTheAddressCarriesConnections(t *testing.T) {
	cfg := stored(t)
	n := Neighbor{IP: ip("10.10.0.80"), MAC: espMAC, Stale: true}
	if _, owned := ResolveIdentity(cfg, Observed{Neighbors: []Neighbor{n}}, nil).Owner[n.IP]; owned {
		t.Fatal("a stale entry without connections says nothing")
	}
	obs := Observed{Neighbors: []Neighbor{n}, ActiveSources: map[netip.Addr]bool{n.IP: true}}
	if ResolveIdentity(cfg, obs, nil).Owner[n.IP] != idESP {
		t.Fatal("a stale entry of an address with connections still counts")
	}
	fresh := Neighbor{IP: ip("10.10.0.81"), MAC: espMAC}
	if ResolveIdentity(cfg, Observed{Neighbors: []Neighbor{fresh}}, nil).Owner[fresh.IP] != idESP {
		t.Fatal("a confirmed entry counts")
	}
}

func TestAProbeNeverTakesTheMACOfAConfiguredDevice(t *testing.T) {
	obs := Observed{
		Probes:    map[string]ProbeObservation{idProbe: {MAC: espMAC}},
		Neighbors: []Neighbor{{IP: ip("10.10.0.90"), MAC: espMAC}},
	}
	if got := ResolveIdentity(stored(t), obs, nil).Owner[ip("10.10.0.90")]; got != idESP {
		t.Fatalf("the configured device keeps its MAC, owner = %s", got)
	}
}

func TestDiscoveredDevicesOwnTheAddressesNobodyElseHas(t *testing.T) {
	obs := Observed{Discovered: []DiscoveredDevice{
		{ID: discoveredID, MACs: []string{"aa:bb:cc:00:00:01"}, IPs: []netip.Addr{ip("10.10.0.120"), ip("10.10.0.42")}},
	}}
	id := ResolveIdentity(stored(t), obs, nil)
	if id.Owner[ip("10.10.0.120")] != discoveredID {
		t.Errorf("the free address belongs to the discovered device: %v", id.Owner)
	}
	if id.Owner[ip("10.10.0.42")] != idESP {
		t.Errorf("the reserved address stays with its configured device: %v", id.Owner)
	}
	if got := id.Addresses[discoveredID]; len(got) != 1 || got[0] != ip("10.10.0.120") {
		t.Errorf("addresses = %v", got)
	}
}

// ---- the example files ---------------------------------------------------------------------

func TestEveryExampleFileIsCheckedByTheDomain(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "api", "examples", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("examples: %v %v", files, err)
	}
	cfg := exampleConfiguration(t)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		switch name := filepath.Base(f); name {
		case "configuration.yaml":
			if _, err := DecodeConfiguration(raw, FormatYAML); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case "overlays.yaml":
			doc, _ := ParseDocument(raw, FormatYAML)
			for i, item := range doc.([]any) {
				itemRaw, err := json.Marshal(item)
				if err != nil {
					t.Fatal(err)
				}
				req, err := DecodeOverlayRequest(itemRaw, FormatJSON)
				if err != nil {
					t.Fatalf("%s[%d]: %v", name, i, err)
				}
				if _, errs := ValidateOverlay(cfg, req); len(errs) != 0 {
					t.Errorf("%s[%d]: %v", name, i, errs)
				}
			}
		case "run-request.yaml":
			// the inline scenario of a run is validated against the configuration like any scenario
			doc, _ := ParseDocument(raw, FormatYAML)
			v, err := Schemas()
			if err != nil {
				t.Fatal(err)
			}
			if errs := v.Validate("RunRequest", doc); len(errs) > 0 {
				t.Fatalf("%s: %v", name, ValidationErrors(errs))
			}
			var run struct {
				Inline model.Scenario `json:"inline"`
			}
			if err := decodeInto(doc, &run); err != nil {
				t.Fatal(err)
			}
			// the example names a placeholder device that `parameters` replaces
			dev := "esp32-42"
			run.Inline.Target.Device = &dev
			if errs := ValidateScenario(cfg, &run.Inline); len(errs) != 0 {
				t.Errorf("%s: %v", name, errs)
			}
		default:
			t.Errorf("%s is an example the domain tests do not know: add it", name)
		}
	}
}

// M2-02 test: every validation code the domain can report is documented in development.md's
// "Validation codes" table (a table of the package's own Code* constants, mirroring how CC-01
// checks the engine's Event* constants against the spec).
func TestEveryValidationCodeIsDocumented(t *testing.T) {
	codes := []string{
		CodeInvalidID, CodeDuplicateID, CodeDuplicateName, CodeNameIsUUID, CodeReservedName,
		CodeUnknownReference, CodeWrongReference, CodeInvalidNetwork,
		CodeHostBitsSet, CodeOverlappingSubnet, CodeReservedRange, CodeInvalidPrefixLength,
		CodeInvalidAddress, CodeOutsideSubnet, CodeDuplicateAddress, CodeDuplicateInterface,
		CodeDuplicatePort, CodeInvalidEndpoint, CodeWrongKind, CodePoolOrder, CodePoolOverlap,
		CodeInvalidDuration, CodeNoIdentifier, CodeDuplicateIdentifier, CodeInvalidMAC,
		CodeFixedIPRequires, CodeDuplicateMember, CodeProbeNetwork, CodeMatrixSelf,
		CodeMatrixDuplicate, CodeManagementOverlap, CodeRuleOrder, CodePortsRequireProtocol,
		CodeInvalidPortRange, CodeResetRequiresTCP, CodeCutRequiresTCP, CodeDuplicatePublicKey,
		CodeKeySettings, CodeMissingField, CodeUnexpectedField, CodeProtocolSettings, CodeTimers,
		CodeInvalidTable, CodeRemoteNetwork, CodeStepOrder, CodeTargetWidened, CodeTargetKind,
		CodeTunnelOutsideTarget, CodeReservedID, CodeStepKind, CodeCheckWindow, CodeDuplicateStep,
		CodeJitterExceedsLatency, CodeReorderNeedsLatency, CodeExclusive, CodeMixedDirections,
		CodeMixedFamily, CodeEmptyFault, CodeInvalidMTU, CodeCutWithAllow, CodeTunnelParameter,
		CodeInvalidDNSFault, CodeInvalidTLSCase, CodeInvalidDHCPAction, CodeInvalidOverlay,
		CodeInvalidFlapping, CodeInvalidName, CodeDuplicateRoutingKind, CodeUnknownRoutingSettings,
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "development.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Errorf("%q listed twice in the test's own table", c)
		}
		seen[c] = true
		if !strings.Contains(doc, "`"+c+"`") {
			t.Errorf("code %q is not documented in development.md", c)
		}
	}
}
