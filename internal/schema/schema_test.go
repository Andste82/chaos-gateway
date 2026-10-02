package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/api"
	"github.com/Andste82/chaos-gateway/internal/model"
)

var shared = sync.OnceValues(func() (*Validator, error) { return New(api.Spec) })

// newValidator returns a validator shared by all tests: loading the spec is the expensive part.
func newValidator(t *testing.T) *Validator {
	t.Helper()
	v, err := shared()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// decodeYAML reads a YAML document the way the API reads JSON: numbers become json.Number.
func decodeYAML(t *testing.T, raw []byte) any {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return normalize(doc)
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
		return x
	case int:
		return json.Number(jsonInt(x))
	case float64:
		return x
	}
	return v
}

func jsonInt(i int) string { b, _ := json.Marshal(i); return string(b) }

func decodeJSON(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return v
}

func TestNewRejectsAnUnreadableDocument(t *testing.T) {
	if _, err := New([]byte("not: [valid")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestTheSpecDefinesTheSchemasTheDomainNeeds(t *testing.T) {
	v := newValidator(t)
	for _, name := range []string{"Configuration", "OverlayRequest", "RunRequest", "Scenario", "Scope", "Network"} {
		if !v.Has(name) || v.Schema(name) == nil {
			t.Errorf("schema %s missing", name)
		}
	}
	if v.Has("Nope") || v.Schema("Nope") != nil {
		t.Error("an unknown schema must not exist")
	}
	if errs := v.Validate("Nope", map[string]any{}); len(errs) != 1 || errs[0].Code != "internal" {
		t.Errorf("errs = %v", errs)
	}
}

func TestEveryExampleOfTheSpecValidates(t *testing.T) {
	v := newValidator(t)
	dir := "../../api/examples"
	read := func(name string) []byte {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if errs := v.Validate("Configuration", decodeYAML(t, read("configuration.yaml"))); len(errs) != 0 {
		t.Errorf("configuration.yaml: %v", errs)
	}
	for i, doc := range decodeYAML(t, read("overlays.yaml")).([]any) {
		if errs := v.Validate("OverlayRequest", doc); len(errs) != 0 {
			t.Errorf("overlays.yaml[%d]: %v", i, errs)
		}
	}
	if errs := v.Validate("RunRequest", decodeYAML(t, read("run-request.yaml"))); len(errs) != 0 {
		t.Errorf("run-request.yaml: %v", errs)
	}
}

func TestInvalidDocumentsAreRejectedWithPointerAndCode(t *testing.T) {
	v := newValidator(t)
	tests := []struct {
		name, schema, doc string
		path, code        string
	}{
		{"loss without %", "NetemParams", `{"loss":"10"}`, "/loss", CodePattern},
		{"duration without unit", "NetemParams", `{"latency":"200"}`, "/latency", CodePattern},
		{"bit rate in bytes", "NetemParams", `{"rate":"2MB"}`, "/rate", CodePattern},
		{"scope with two sources", "Scope", `{"device":"a","group":"b"}`, "", CodeMaxProperties},
		{"empty scope", "Scope", `{}`, "", CodeMinProperties},
		{"unknown access action", "AccessRuleBody", `{"action":"block"}`, "/action", CodeEnum},
		{"port out of range", "TrafficMatch", `{"protocol":"tcp","ports":[70000]}`, "/ports/0", CodeMaximum},
		{"port zero", "TrafficMatch", `{"ports":[0]}`, "/ports/0", CodeMinimum},
		{"network without type", "Network", `{"name":"x","address":"10.0.0.1/24","interfaces":[{"name":"eth1"}]}`, "/type", CodeRequired},
		{"unknown network type", "Network", `{"type":"vlan","name":"x"}`, "/type", CodeDiscriminator},
		{"cidr prefix 33", "Ipv4Cidr", `"10.0.0.0/33"`, "", CodePattern},
		{"upper-case MAC", "MacAddress", `"AA:BB:CC:DD:EE:FF"`, "", CodePattern},
		{"step without at", "Step", `{"id":"x","restore":true}`, "/at", CodeRequired},
		{"bad step id", "Step", `{"id":"Bad Id","at":"1s","restore":true}`, "/id", CodePattern},
		{"missing required", "DhcpPool", `{"start":"10.0.0.1"}`, "/end", CodeRequired},
		{"wrong type", "NetemParams", `{"blackout":"yes"}`, "/blackout", CodeInvalidType},
		{"integer given as fraction", "MtuParams", `{"size":1200.5}`, "/size", CodeInvalidType},
		{"string too long", "Description", `"` + strings.Repeat("x", 501) + `"`, "", CodeMaxLength},
		{"too many ports", "TrafficMatch", `{"protocol":"tcp","ports":[` + strings.Repeat("1,", 64) + `1]}`, "/ports", CodeMaxItems},
		{"empty list that needs an item", "LanNetwork", `{"type":"lan","name":"n","address":"10.0.0.1/24","interfaces":[]}`, "/interfaces", CodeMinItems},
		{"not an object", "Scope", `"device"`, "", CodeInvalidType},
		{"not an array", "TrafficMatch", `{"ports":5}`, "/ports", CodeInvalidType},
		{"not a string", "Name", `5`, "", CodeInvalidType},
		{"bad uuid", "Uuid", `"not-a-uuid"`, "", CodeFormat},
		{"ipv6 where ipv4 is needed", "Ipv4", `"::1"`, "", CodeFormat},
		{"bad time stamp", "ConfigFault", `{"created_at":"yesterday","source":{"global":true}}`, "/created_at", CodeFormat},
		{"enum with a number", "Configuration", `{"schema_version":2,"uplink":{"interface":{"name":"a"}},"management":{"interface":{"name":"b"}}}`, "/schema_version", CodeEnum},
		{"interface reference without anything", "InterfaceRef", `{}`, "", CodeMinProperties},
		{"name that is too long", "Name", `"` + strings.Repeat("a", 64) + `"`, "", CodePattern},
		{"reserved-looking name with a space", "Name", `"my device"`, "", CodePattern},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v.Validate(tt.schema, decodeJSON(t, tt.doc))
			for _, e := range errs {
				if e.Path == tt.path && e.Code == tt.code {
					return
				}
			}
			t.Fatalf("want %s at %q, got %v", tt.code, tt.path, errs)
		})
	}
}

// The spec cannot say additionalProperties: false together with allOf; the validator enforces it.
func TestUnknownFieldsAreRejectedEverywhere(t *testing.T) {
	v := newValidator(t)
	tests := []struct{ name, schema, doc, path string }{
		{"flat object", "DhcpPool", `{"start":"10.0.0.1","end":"10.0.0.9","extra":1}`, "/extra"},
		{"allOf object (fault)", "ConfigFault", `{"source":{"global":true},"latency":"1ms","laatency":"1ms"}`, "/laatency"},
		{"allOf object (overlay request)", "OverlayRequest", `{"target":{"global":true},"fault":{"latency":"1ms"},"ttll":"5m"}`, "/ttll"},
		{"allOf inside an allOf member", "OverlayRequest", `{"target":{"global":true},"fault":{"latency":"1ms","upload":{"latencyy":"1ms"}}}`, "/fault/upload/latencyy"},
		{"union member (lan)", "Network", `{"type":"lan","name":"n","address":"10.0.0.1/24","interfaces":[{"name":"e"}],"kind":"hub"}`, "/kind"},
		{"union member (wireguard)", "Network", `{"type":"wireguard","kind":"hub","name":"n","address":"10.0.0.1/24","listen_port":1,"interfaces":[]}`, "/interfaces"},
		{"nested in a map value", "Configuration", `{"schema_version":1,"uplink":{"interface":{"name":"a"}},"management":{"interface":{"name":"b"}},"devices":{"1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a":{"name":"d","macc":[]}}}`, "/devices/1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a/macc"},
		{"top level", "Configuration", `{"schema_version":1,"uplink":{"interface":{"name":"a"}},"management":{"interface":{"name":"b"}},"routes":{}}`, "/routes"},
		{"scenario step", "Scenario", `{"name":"s","target":{"device":"d"},"steps":[{"id":"a","at":"0s","restore":true,"atx":1}]}`, "/steps/0/atx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, e := range v.Validate(tt.schema, decodeJSON(t, tt.doc)) {
				if e.Code == CodeUnknownField && e.Path == tt.path {
					return
				}
			}
			t.Fatalf("no unknown_field at %s in %v", tt.path, v.Validate(tt.schema, decodeJSON(t, tt.doc)))
		})
	}
}

func TestFreeFormObjectsAndMapsAreNotStrict(t *testing.T) {
	v := newValidator(t)
	// a header map (additionalProperties: string) takes any key, but its values are checked
	if errs := v.Validate("HttpModify", decodeJSON(t, `{"request_headers":{"X-Anything":"v"}}`)); len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
	errs := v.Validate("HttpModify", decodeJSON(t, `{"request_headers":{"X-Anything":5}}`))
	if len(errs) != 1 || errs[0].Path != "/request_headers/X-Anything" || errs[0].Code != CodeInvalidType {
		t.Errorf("errs = %v", errs)
	}
	// the patch schema is free-form on purpose
	if errs := v.Validate("ConfigurationPatch", decodeJSON(t, `{"anything":{"goes":1}}`)); len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

func TestErrorsAreSortedByPathAndReportEveryProblem(t *testing.T) {
	v := newValidator(t)
	errs := v.Validate("DhcpPool", decodeJSON(t, `{"start":"nope","zzz":1,"aaa":2}`))
	var paths []string
	for _, e := range errs {
		paths = append(paths, e.Path+":"+e.Code)
	}
	want := "/aaa:unknown_field /end:required /start:format /zzz:unknown_field"
	if got := strings.Join(paths, " "); got != want {
		t.Fatalf("errors = %s, want %s", got, want)
	}
}

func TestPointerEscapesSlashesAndTildes(t *testing.T) {
	if got := Pointer("/a", "b/c~d"); got != "/a/b~1c~0d" {
		t.Fatalf("Pointer = %s", got)
	}
}

func TestMessagesNameTheAlternativesOfAnExactlyOneOfObject(t *testing.T) {
	v := newValidator(t)
	errs := v.Validate("Scope", decodeJSON(t, `{"device":"a","group":"b"}`))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "exactly one of: device, global, group, network, remote_network") {
		t.Fatalf("errs = %v", errs)
	}
}

func TestADiscriminatedUnionNamesTheAllowedKinds(t *testing.T) {
	v := newValidator(t)
	errs := v.Validate("Network", decodeJSON(t, `{"type":"vlan"}`))
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "lan, wireguard") {
		t.Fatalf("errs = %v", errs)
	}
}

var _ = model.ValidationError{}
