package domain

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

var admin = model.Owner{Type: "user", Id: "admin"}

func requestOf(t *testing.T, yamlBody string) *model.OverlayRequest {
	t.Helper()
	req, err := DecodeOverlayRequest([]byte(yamlBody), FormatYAML)
	if err != nil {
		t.Fatalf("%s: %v", yamlBody, err)
	}
	return req
}

func validateOverlay(t *testing.T, yamlBody string) (*model.OverlayRequest, ValidationErrors) {
	t.Helper()
	out, errs := ValidateOverlay(exampleConfiguration(t), requestOf(t, yamlBody))
	return out, ValidationErrors(errs)
}

func TestEveryOverlayExampleValidates(t *testing.T) {
	doc, err := ParseDocument(readExample(t, "overlays.yaml"), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg := exampleConfiguration(t)
	for i, item := range doc.([]any) {
		var req model.OverlayRequest
		if err := decodeInto(item, &req); err != nil {
			t.Fatal(err)
		}
		if _, errs := ValidateOverlay(cfg, &req); len(errs) != 0 {
			t.Errorf("overlays.yaml[%d]: %v", i, errs)
		}
	}
}

func TestOverlayKinds(t *testing.T) {
	tests := map[string]string{
		`{target: {global: true}, profile: lte}`:                            "profile",
		`{target: {global: true}, fault: {blackout: true}}`:                 "fault",
		`{target: {global: true}, rule: {action: drop}}`:                    "rule",
		`{target: {global: true}, dns: {action: servfail}}`:                 "dns",
		`{target: {global: true}, tls: {case: expired}}`:                    "tls",
		`{target: {device: esp32-42}, dhcp: {action: silence}}`:             "dhcp",
		`{wireguard: {link: site-b, action: disable}}`:                      "wireguard",
		`{fault: {family: tunnel, tunnel: {link: site-b}, blackout: true}}`: "fault",
	}
	for body, want := range tests {
		got, err := OverlayKindOf(requestOf(t, body))
		if err != nil || got != want {
			t.Errorf("%s: kind %q, err %v; want %q", body, got, err, want)
		}
	}
	if _, err := OverlayKindOf(&model.OverlayRequest{}); err == nil {
		t.Error("an empty request has no kind")
	}
}

func TestOverlayRequestRules(t *testing.T) {
	tests := []struct {
		name, body, path, code string
	}{
		{"no kind", `{target: {global: true}, ttl: 5m}`, "", CodeInvalidOverlay},
		{"two kinds", `{target: {global: true}, profile: lte, fault: {blackout: true}}`, "", CodeInvalidOverlay},
		{"a fault without a target", `{fault: {blackout: true}}`, "/target", CodeMissingField},
		{"a profile without a target", `{profile: lte}`, "/target", CodeMissingField},
		{"a tunnel fault with a target", `{target: {device: esp32-42}, fault: {family: tunnel, tunnel: {link: site-b}, blackout: true}}`, "/target", CodeUnexpectedField},
		{"a wireguard action with a target", `{target: {device: esp32-42}, wireguard: {link: site-b, action: disable}}`, "/target", CodeUnexpectedField},
		{"an unknown device", `{target: {device: ghost}, fault: {blackout: true}}`, "/target/device", CodeUnknownReference},
		{"an unknown profile", `{target: {global: true}, profile: nope}`, "/profile", CodeUnknownReference},
		{"an unknown tunnel", `{fault: {family: tunnel, tunnel: {client: ghost}, blackout: true}}`, "/fault/tunnel/client", CodeUnknownReference},
		{"a tunnel fault without a tunnel", `{fault: {family: tunnel, blackout: true}}`, "/fault/tunnel", CodeMissingField},
		{"jitter above latency", `{target: {global: true}, fault: {latency: 5ms, jitter: 50ms}}`, "/fault/jitter", CodeJitterExceedsLatency},
		{"a rate that is no rate", `{target: {global: true}, fault: {rate: 3bit}}`, "/fault/rate", CodeInvalidRate},
		{"a flapping without a down time", `{target: {global: true}, fault: {flapping: {up: 5s, down: 0s}}}`, "/fault/flapping", CodeInvalidFlapping},
		{"reorder without a delay", `{target: {global: true}, fault: {reorder: 5%}}`, "/fault/reorder", CodeReorderNeedsLatency},
		{"loss with burst loss", `{target: {global: true}, fault: {loss: 5%, burst_loss: {p: 1%, r: 30%}}}`, "/fault/burst_loss", CodeExclusive},
		{"blackout with flapping", `{target: {global: true}, fault: {blackout: true, flapping: {up: 5s, down: 5s}}}`, "/fault/flapping", CodeExclusive},
		{"an empty fault", `{target: {global: true}, fault: {destination: {uplink: true}}}`, "/fault", CodeEmptyFault},
		{"a reset on udp", `{target: {global: true}, rule: {action: reset, protocol: udp}}`, "/rule/action", CodeResetRequiresTCP},
		{"a dns delay without a delay", `{target: {global: true}, dns: {action: delay}}`, "/dns/delay", CodeInvalidDNSFault},
		{"a tls case over udp", `{target: {global: true}, tls: {case: expired, protocol: udp}}`, "/tls/protocol", CodeInvalidTLSCase},
		{"a dhcp action on a group", `{target: {group: sensors}, dhcp: {action: silence}}`, "/target", CodeTargetKind},
		{"a dhcp action on the global scope", `{target: {global: true}, dhcp: {action: force_new_ip}}`, "/target", CodeTargetKind},
		{"a short lease without a time", `{target: {device: esp32-42}, dhcp: {action: short_lease}}`, "/dhcp/lease_time", CodeInvalidDHCPAction},
		{"a wireguard action with client and link", `{wireguard: {client: lab-rA, link: site-b, action: disable}}`, "/wireguard", CodeInvalidOverlay},
		{"a ttl of zero", `{target: {global: true}, fault: {blackout: true}, ttl: 0s}`, "/ttl", CodeInvalidDuration},
		{"a lease below a second", `{target: {global: true}, fault: {blackout: true}, lease: 500ms}`, "/lease", CodeInvalidDuration},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := validateOverlay(t, tt.body)
			wantError(t, errs, tt.path, tt.code)
		})
	}
}

func TestValidateOverlayResolvesNamesAndLeavesTheRequestAlone(t *testing.T) {
	req := requestOf(t, `{target: {device: esp32-42}, profile: bad-lte, ttl: 5m}`)
	out, errs := ValidateOverlay(exampleConfiguration(t), req)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := *out.Target.Device; got != idESP {
		t.Errorf("device = %s", got)
	}
	if got := *out.Profile; got != "b9b6e3e5-b89a-5b25-b7af-b1ac017bda16" {
		t.Errorf("profile = %s", got)
	}
	if *req.Target.Device != "esp32-42" || *req.Profile != "bad-lte" {
		t.Error("the request must not be changed")
	}
}

func TestOverlayKeys(t *testing.T) {
	key := func(body string) string {
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
	same := [][2]string{
		// ports in another order, with a duplicate
		{`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883, 80], latency: 5ms}}`,
			`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [80, 8883, 80], loss: 3%}}`},
		// a device by name or by UUID
		{`{target: {device: esp32-42}, fault: {blackout: true}}`, `{target: {device: ` + idESP + `}, fault: {latency: 1ms}}`},
		// one profile activation per owner and target: another profile replaces it
		{`{target: {device: esp32-42}, profile: bad-lte}`, `{target: {device: esp32-42}, profile: lte, ttl: 1m}`},
		// changing the action of a rule replaces the rule
		{`{target: {device: esp32-42}, rule: {action: drop, protocol: tcp, ports: [8883]}}`,
			`{target: {device: esp32-42}, rule: {action: reject, protocol: tcp, ports: [8883]}}`},
		// the same DNS names in another order and case
		{`{target: {global: true}, dns: {names: [a.test, B.test], action: servfail}}`,
			`{target: {global: true}, dns: {names: [b.test, A.test], action: nxdomain}}`},
		{`{target: {device: esp32-42}, tls: {case: expired, ports: [8883], sni: [b.test, a.test]}}`,
			`{target: {device: esp32-42}, tls: {case: self_signed, ports: [8883], sni: [a.test, B.test]}}`},
		{`{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 30s}}`, `{target: {device: esp32-42}, dhcp: {action: short_lease, lease_time: 60s}}`},
		{`{wireguard: {link: site-b, action: disable}}`, `{wireguard: {link: site-b, action: disable}, ttl: 20s}`},
		{`{fault: {family: tunnel, tunnel: {client: lab-rA}, latency: 50ms}}`, `{fault: {family: tunnel, tunnel: {client: lab-rA}, blackout: true}}`},
	}
	for i, pair := range same {
		if a, b := key(pair[0]), key(pair[1]); a != b {
			t.Errorf("pair %d: the keys must be equal\n  %s\n  %s", i, a, b)
		}
	}
	different := [][2]string{
		{`{target: {device: esp32-42}, fault: {blackout: true}}`, `{target: {device: lab-host}, fault: {blackout: true}}`},
		{`{target: {device: esp32-42}, fault: {blackout: true}}`, `{target: {group: sensors}, fault: {blackout: true}}`},
		{`{target: {device: esp32-42}, fault: {protocol: tcp, ports: [80], latency: 5ms}}`, `{target: {device: esp32-42}, fault: {protocol: tcp, ports: [81], latency: 5ms}}`},
		{`{target: {device: esp32-42}, fault: {protocol: tcp, latency: 5ms}}`, `{target: {device: esp32-42}, fault: {protocol: udp, latency: 5ms}}`},
		{`{target: {device: esp32-42}, fault: {destination: {hostname: a.test}, latency: 5ms}}`, `{target: {device: esp32-42}, fault: {destination: {hostname: b.test}, latency: 5ms}}`},
		{`{target: {device: esp32-42}, fault: {latency: 5ms}}`, `{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1280}}}`},
		{`{target: {global: true}, dns: {names: [a.test], action: servfail}}`, `{target: {global: true}, dns: {action: servfail}}`},
		{`{target: {device: esp32-42}, tls: {case: expired, sni: [a.test]}}`, `{target: {device: esp32-42}, tls: {case: expired}}`},
		{`{target: {device: esp32-42}, dhcp: {action: silence}}`, `{target: {device: esp32-42}, dhcp: {action: force_new_ip}}`},
		{`{wireguard: {link: site-b, action: disable}}`, `{wireguard: {link: site-b, action: block_endpoint}}`},
		{`{fault: {family: tunnel, tunnel: {client: lab-rA}, latency: 5ms}}`, `{fault: {family: tunnel, tunnel: {link: site-b}, latency: 5ms}}`},
		{`{target: {device: esp32-42}, rule: {action: drop}}`, `{target: {device: esp32-42}, fault: {blackout: true}}`},
	}
	for i, pair := range different {
		if a, b := key(pair[0]), key(pair[1]); a == b {
			t.Errorf("pair %d: the keys must differ: %s", i, a)
		}
	}
}

func TestOverlayKeyDependsOnTheOwner(t *testing.T) {
	out, _ := validateOverlay(t, `{target: {device: esp32-42}, fault: {blackout: true}}`)
	a, _ := OverlayKey(admin, out)
	b, _ := OverlayKey(model.Owner{Type: "token", Id: "7"}, out)
	c, _ := OverlayKey(model.Owner{Type: "run", Id: "7"}, out)
	if a == b || b == c || a == c {
		t.Fatalf("owners must not share keys: %s %s %s", a, b, c)
	}
	if _, err := OverlayKey(admin, &model.OverlayRequest{}); err == nil {
		t.Error("a request without a kind has no key")
	}
	if len(ShortKey(a)) != 16 || ShortKey(a) == ShortKey(b) {
		t.Error("ShortKey must be a stable 16-digit hash")
	}
}

func TestNewOverlayFillsKindOwnerAndTimes(t *testing.T) {
	out, _ := validateOverlay(t, `{target: {device: esp32-42}, fault: {blackout: true}, ttl: 5m, lease: 30s}`)
	o, err := NewOverlay(*out, admin, mustUUID("00000000-0000-4000-8000-000000000001"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != "fault" || o.Owner != admin || !o.CreatedAt.Equal(t0) || !o.UpdatedAt.Equal(t0) || *o.Ttl != "5m" || *o.Lease != "30s" {
		t.Fatalf("overlay = %+v", o)
	}
	if _, err := NewOverlay(model.OverlayRequest{}, admin, mustUUID("00000000-0000-4000-8000-000000000002"), t0); err == nil {
		t.Error("a request without a kind must fail")
	}
}

// ---- documents ---------------------------------------------------------------------------

func TestParseDocumentFormats(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		f    Format
		ok   bool
	}{
		{"json", `{"a": 1}`, FormatAuto, true},
		{"json with leading space", "  \n\t[1]", FormatAuto, true},
		{"yaml", "a: 1\nb: [x, y]\n", FormatAuto, true},
		{"explicit yaml", `{"a": 1}`, FormatYAML, true}, // JSON is YAML
		{"truncated json", `{"a": `, FormatJSON, false},
		{"trailing data", `{"a": 1} {"b": 2}`, FormatJSON, false},
		{"empty yaml", "", FormatYAML, false},
		{"broken yaml", "a: [1, 2\n", FormatYAML, false},
		{"non-string key", "1: a\n", FormatYAML, false},
		{"unknown format", "a: 1", Format(99), false},
	} {
		_, err := ParseDocument([]byte(tt.in), tt.f)
		var pe *ParseError
		if tt.ok && err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		if !tt.ok && !asParseError(err, &pe) {
			t.Errorf("%s: expected a ParseError, got %v", tt.name, err)
		}
	}
}

func TestYAMLNumbersAndTimesKeepTheirMeaning(t *testing.T) {
	doc, err := ParseDocument([]byte("n: 51820\nbig: 4294967295\nf: 0.5\nt: 2026-10-02T12:00:00Z\n"), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	m := doc.(map[string]any)
	if m["n"].(interface{ String() string }).String() != "51820" || m["big"].(interface{ String() string }).String() != "4294967295" {
		t.Errorf("integers: %v %v", m["n"], m["big"])
	}
	if m["f"].(float64) != 0.5 {
		t.Errorf("float: %v", m["f"])
	}
	if m["t"].(string) != "2026-10-02T12:00:00Z" {
		t.Errorf("time: %v", m["t"])
	}
}

func TestDecodeConfigurationReportsAMalformedDocumentAsAParseError(t *testing.T) {
	_, err := DecodeConfiguration([]byte(`{"schema_version": `), FormatJSON)
	var pe *ParseError
	if !asParseError(err, &pe) || !strings.Contains(err.Error(), "malformed document") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeConfigurationStopsAtSchemaErrorsBeforeTheSemanticChecks(t *testing.T) {
	d := baseDoc(t)
	d.set(t, "10.10.0.1/24x", "networks", idIoT, "address") // a schema error
	errs := d.validate(t)
	wantError(t, errs, iot+"/address", "pattern")
	for _, e := range errs {
		if e.Code == CodeInvalidAddress {
			t.Errorf("semantic checks must not run on a document that fails the schema: %v", e)
		}
	}
}

func TestUnknownFieldsInAConfigurationAreRejected(t *testing.T) {
	d := baseDoc(t)
	d.set(t, true, "networks", idIoT, "natt")
	wantError(t, d.validate(t), iot+"/natt", "unknown_field")
}

func TestValidationErrorsMessage(t *testing.T) {
	var none ValidationErrors
	if none.AsError() != nil {
		t.Error("an empty list is no error")
	}
	errs := ValidationErrors{{Path: "/a", Code: "x", Message: "bad"}, {Path: "", Code: "y", Message: "root"}}
	if msg := errs.AsError().Error(); !strings.Contains(msg, "/a: bad (x)") || !strings.Contains(msg, "/: root (y)") {
		t.Errorf("message = %q", msg)
	}
	many := ValidationErrors{}
	for i := 0; i < 8; i++ {
		many = append(many, model.ValidationError{Path: "/p", Code: "c", Message: "m"})
	}
	if !strings.Contains(many.Error(), "… and 3 more") {
		t.Errorf("message = %q", many.Error())
	}
}

// ---- built-in profiles -------------------------------------------------------------------

func TestBuiltinProfiles(t *testing.T) {
	want := []string{"normal", "lte", "bad-lte", "satellite", "congested-wifi", "offline", "intermittent", "dns-broken", "tls-broken"}
	got := BuiltinProfiles()
	if len(got) != len(want) {
		t.Fatalf("%d built-in profiles", len(got))
	}
	ids := map[string]bool{}
	for i, p := range got {
		if p.Profile.Name != want[i] {
			t.Errorf("profile %d = %s, want %s", i, p.Profile.Name, want[i])
		}
		if ids[p.ID] || !canonicalID(p.ID) || !IsBuiltinProfileID(p.ID) {
			t.Errorf("%s: bad or duplicate id %s", p.Profile.Name, p.ID)
		}
		ids[p.ID] = true
		wantMilestone := map[string]string{"dns-broken": "M20", "tls-broken": "M21"}[p.Profile.Name]
		if p.Milestone != wantMilestone {
			t.Errorf("%s: milestone %q, want %q", p.Profile.Name, p.Milestone, wantMilestone)
		}
	}
	if IsBuiltinProfileID(idProfile) {
		t.Error("a configured profile is not built in")
	}
	if p, ok := BuiltinProfileByName("BAD-lte"); !ok || p.Profile.Name != "bad-lte" {
		t.Error("lookup by name must ignore case")
	}
	if _, ok := BuiltinProfileByName("nope"); ok {
		t.Error("no such profile")
	}
	got[0].Profile.Name = "changed"
	if BuiltinProfiles()[0].Profile.Name != "normal" {
		t.Error("BuiltinProfiles must return copies")
	}
}

func TestBuiltinProfileValuesFollowThePlan(t *testing.T) {
	byName := map[string]BuiltinProfile{}
	for _, p := range BuiltinProfiles() {
		byName[p.Profile.Name] = p
	}
	imp := func(name string) model.NetemParams {
		return convert[model.NetemParams](*byName[name].Profile.Parts.Impairment)
	}
	if n := imp("bad-lte"); deref(n.Latency) != "150ms" || deref(n.Jitter) != "50ms" || deref(n.Loss) != "3%" || deref(n.Rate) != "2Mbit" {
		t.Errorf("bad-lte = %+v", n)
	}
	if n := imp("lte"); deref(n.Latency) != "50ms" || deref(n.Jitter) != "10ms" || deref(n.Loss) != "0.1%" {
		t.Errorf("lte = %+v", n)
	}
	if n := imp("satellite"); deref(n.Latency) != "600ms" || deref(n.Loss) != "1%" {
		t.Errorf("satellite = %+v", n)
	}
	if n := imp("offline"); !deref(n.Blackout) {
		t.Errorf("offline = %+v", n)
	}
	if n := imp("intermittent"); n.Flapping == nil || n.Flapping.Up != "20s" || n.Flapping.Down != "10s" {
		t.Errorf("intermittent = %+v", n)
	}
	if n := imp("congested-wifi"); n.BurstLoss == nil || n.Loss != nil {
		t.Errorf("congested-wifi = %+v", n)
	}
	if len(netemSet(imp("normal"))) != 0 {
		t.Error("normal must not impair")
	}
	if d := byName["dns-broken"].Profile.Parts.Dns; d == nil || d.Action != "servfail" {
		t.Errorf("dns-broken = %+v", d)
	}
}

func TestEveryBuiltinProfileIsValidWhenUsedAsAConfiguredProfile(t *testing.T) {
	// the same rules apply to the built-in profiles as to configured ones
	for _, b := range BuiltinProfiles() {
		d := baseDoc(t)
		p := b.Profile
		p.Name = "copy-of-" + p.Name
		d.set(t, jsonDoc(t, p), "profiles", idNew)
		wantValid(t, d.validate(t))
	}
}
