package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// IDs of the objects in api/examples/configuration.yaml.
const (
	idIoT       = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	idHub       = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	idLink      = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	idClient    = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	idESP       = "1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a"
	idLab       = "2e3f4a5b-6c7d-4e8f-9a0b-1c2d3e4f5a6b"
	idSensors   = "3f4a5b6c-7d8e-4f9a-0b1c-2d3e4f5a6b7c"
	idG2        = "4a5b6c7d-8e9f-4a0b-1c2d-3e4f5a6b7c8d"
	idProbe     = "5b6c7d8e-9f0a-4b1c-2d3e-4f5a6b7c8d9e"
	idRule      = "6c7d8e9f-0a1b-4c2d-3e4f-5a6b7c8d9e0f"
	idFaultNet  = "7d8e9f0a-1b2c-4d3e-4f5a-6b7c8d9e0f1a"
	idFaultDev  = "8e9f0a1b-2c3d-4e4f-5a6b-7c8d9e0f1a2b"
	idFaultAny  = "9f0a1b2c-3d4e-4f5a-6b7c-8d9e0f1a2b3c"
	idFaultMTU  = "0a1b2c3d-4e5f-4a6b-7c8d-9e0f1a2b3c4e"
	idFaultTun  = "1b2c3d4e-5f6a-4b7c-8d9e-0f1a2b3c4d5f"
	idProfile   = "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e60"
	idScenario  = "3d4e5f6a-7b8c-4d9e-0f1a-2b3c4d5e6f71"
	idProtocol  = "5f6a7b8c-9d0e-4f1a-8b2c-3d4e5f6a7b8c"
	idNew       = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	idNew2      = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeef"
	examplePath = "configuration.yaml"
)

// doc is a configuration as generic JSON values, which tests change before decoding.
type doc map[string]any

func baseDoc(t *testing.T) doc {
	t.Helper()
	parsed, err := ParseDocument(readExample(t, examplePath), FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	return doc(parsed.(map[string]any))
}

// node follows a path of keys and indexes; every step must exist.
func (d doc) node(t *testing.T, path ...any) any {
	t.Helper()
	var cur any = map[string]any(d)
	for _, p := range path {
		switch c := cur.(type) {
		case map[string]any:
			next, ok := c[p.(string)]
			if !ok {
				t.Fatalf("no key %v in path %v", p, path)
			}
			cur = next
		case []any:
			cur = c[p.(int)]
		default:
			t.Fatalf("cannot descend into %T at %v of %v", cur, p, path)
		}
	}
	return cur
}

// set stores value at the path; the parent must exist (maps are created for new keys of maps).
func (d doc) set(t *testing.T, value any, path ...any) {
	t.Helper()
	parent := d.node(t, path[:len(path)-1]...)
	switch c := parent.(type) {
	case map[string]any:
		c[path[len(path)-1].(string)] = value
	case []any:
		c[path[len(path)-1].(int)] = value
	default:
		t.Fatalf("cannot set into %T", parent)
	}
}

func (d doc) del(t *testing.T, path ...any) {
	t.Helper()
	parent := d.node(t, path[:len(path)-1]...).(map[string]any)
	delete(parent, path[len(path)-1].(string))
}

// obj parses an object literal in JSON or YAML flow style.
func obj(t *testing.T, s string) map[string]any {
	t.Helper()
	v, err := ParseDocument([]byte(s), FormatYAML) // YAML reads JSON too
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return v.(map[string]any)
}

// validate runs the full pipeline on the document and returns the errors (nil when valid).
func (d doc) validate(t *testing.T) ValidationErrors {
	t.Helper()
	// round trip through JSON so that the document looks like one that came over the wire
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeConfiguration(raw, FormatJSON)
	if err == nil {
		return nil
	}
	var ve ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("unexpected error: %v", err)
	}
	return ve
}

// wantError fails unless the error list has an error with that code at that path.
func wantError(t *testing.T, errs ValidationErrors, path, code string) {
	t.Helper()
	if !errs.Has(path, code) {
		t.Fatalf("want %s at %s, got:\n%s", code, path, dump(errs))
	}
}

func dump(errs ValidationErrors) string {
	var b strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&b, "  %s  %s: %s\n", e.Path, e.Code, e.Message)
	}
	if b.Len() == 0 {
		return "  (no errors)"
	}
	return b.String()
}

func wantValid(t *testing.T, errs ValidationErrors) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("expected a valid configuration, got:\n%s", dump(errs))
	}
}

var _ = model.ValidationError{}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	return string(x) == string(y)
}

func asValidation(err error, target *ValidationErrors) bool { return errors.As(err, target) }

func asParseError(err error, target **ParseError) bool { return errors.As(err, target) }

// jsonDoc converts a model value into generic JSON values.
func jsonDoc(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseDocument(raw, FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
