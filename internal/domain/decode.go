package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/api"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// Format is the syntax of a document.
type Format int

const (
	// FormatAuto picks JSON when the document starts with '{' or '[', YAML otherwise.
	FormatAuto Format = iota
	FormatJSON
	FormatYAML
)

// ParseError is a document that is not well-formed JSON or YAML. The API reports it as
// `bad_request`; problems with a well-formed document are ValidationErrors.
type ParseError struct{ Err error }

func (e *ParseError) Error() string { return "malformed document: " + e.Err.Error() }
func (e *ParseError) Unwrap() error { return e.Err }

// ValidationErrors is a list of problems with a document; it is an error when not empty.
type ValidationErrors []model.ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	parts := make([]string, 0, len(e))
	for i, v := range e {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("… and %d more", len(e)-5))
			break
		}
		parts = append(parts, fmt.Sprintf("%s: %s (%s)", pathOrRoot(v.Path), v.Message, v.Code))
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func pathOrRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// AsError returns the list as an error, or nil when it is empty.
func (e ValidationErrors) AsError() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

// Has reports whether an error with that code at that path exists.
func (e ValidationErrors) Has(path, code string) bool {
	for _, v := range e {
		if v.Path == path && v.Code == code {
			return true
		}
	}
	return false
}

var shared = sync.OnceValues(func() (*schema.Validator, error) { return schema.New(api.Spec) })

// Schemas returns the validator for the schemas of api/openapi.yaml.
func Schemas() (*schema.Validator, error) { return shared() }

// ParseDocument reads a JSON or YAML document into generic values (maps, slices, strings,
// booleans, json.Number), the form the schema validator works on.
func ParseDocument(raw []byte, f Format) (any, error) {
	if f == FormatAuto {
		trimmed := bytes.TrimLeft(raw, " \t\r\n")
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			f = FormatJSON
		} else {
			f = FormatYAML
		}
	}
	switch f {
	case FormatJSON:
		if err := checkDuplicateKeys(raw); err != nil {
			return nil, &ParseError{err}
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, &ParseError{err}
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return nil, &ParseError{errors.New("unexpected data after the document")}
		}
		return v, nil
	case FormatYAML:
		var v any
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, &ParseError{err}
		}
		if v == nil {
			return nil, &ParseError{errors.New("empty document")}
		}
		out, err := fromYAML(v)
		if err != nil {
			return nil, &ParseError{err}
		}
		return out, nil
	}
	return nil, &ParseError{fmt.Errorf("unknown format %d", f)}
}

// fromYAML makes decoded YAML look like decoded JSON: integers become json.Number and time
// stamps strings; keys must be strings.
func fromYAML(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			n, err := fromYAML(e)
			if err != nil {
				return nil, err
			}
			x[k] = n
		}
		return x, nil
	case map[any]any:
		return nil, errors.New("mapping keys must be strings")
	case []any:
		for i, e := range x {
			n, err := fromYAML(e)
			if err != nil {
				return nil, err
			}
			x[i] = n
		}
		return x, nil
	case int:
		return json.Number(fmt.Sprint(x)), nil
	case int64:
		return json.Number(fmt.Sprint(x)), nil
	case uint64:
		return json.Number(fmt.Sprint(x)), nil
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano), nil
	}
	return v, nil
}

// decodeInto converts generic values into a typed value. The value must already have passed the
// schema validation: unknown fields are rejected there, with a JSON pointer.
func decodeInto(doc any, out any) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// DecodeConfiguration reads a configuration document and validates it completely: syntax,
// schema (including unknown fields) and the rules of Validate. The result is exactly what the
// document said: references may still be names (see Normalize).
//
// The error is a *ParseError for a malformed document and ValidationErrors for a document that
// is well-formed but wrong.
func DecodeConfiguration(raw []byte, f Format) (*model.Configuration, error) {
	doc, err := ParseDocument(raw, f)
	if err != nil {
		return nil, err
	}
	return DecodeConfigurationDocument(doc)
}

// DecodeConfigurationDocument is DecodeConfiguration for an already parsed document.
func DecodeConfigurationDocument(doc any) (*model.Configuration, error) {
	v, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := v.Validate("Configuration", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var cfg model.Configuration
	if err := decodeInto(doc, &cfg); err != nil {
		return nil, fmt.Errorf("domain: decode a validated configuration: %w", err)
	}
	if errs := Validate(&cfg); len(errs) > 0 {
		return &cfg, ValidationErrors(errs)
	}
	return &cfg, nil
}

// DecodeScenario reads a scenario in YAML or JSON (the import format of plan §2.10) and
// validates it on its own: references to devices, groups and networks need the configuration,
// so they are checked by ValidateScenario against it.
func DecodeScenario(raw []byte, f Format) (*model.Scenario, error) {
	doc, err := ParseDocument(raw, f)
	if err != nil {
		return nil, err
	}
	v, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := v.Validate("Scenario", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var sc model.Scenario
	if err := decodeInto(doc, &sc); err != nil {
		return nil, fmt.Errorf("domain: decode a validated scenario: %w", err)
	}
	return &sc, nil
}

// DecodeOverlayRequest reads an overlay request and validates it against the schema. The
// semantic checks (target, references, kind-specific rules) are in ValidateOverlay.
func DecodeOverlayRequest(raw []byte, f Format) (*model.OverlayRequest, error) {
	doc, err := ParseDocument(raw, f)
	if err != nil {
		return nil, err
	}
	v, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := v.Validate("OverlayRequest", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var req model.OverlayRequest
	if err := decodeInto(doc, &req); err != nil {
		return nil, fmt.Errorf("domain: decode a validated overlay request: %w", err)
	}
	return &req, nil
}

// clone returns a deep copy of a model value.
func clone[T any](v T) T {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("domain: clone: %v", err)) // model types always marshal
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("domain: clone: %v", err))
	}
	return out
}

// convert re-reads one model value as another type, keeping the fields both have. It is used to
// view ConfigFault, FaultBody and ImpairmentParams, which share their parameters, uniformly.
func convert[T any](from any) T {
	raw, err := json.Marshal(from)
	if err != nil {
		panic(fmt.Sprintf("domain: convert: %v", err))
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("domain: convert: %v", err))
	}
	return out
}

// checkDuplicateKeys rejects a JSON object that names a key twice: encoding/json would silently
// keep the last one, which makes "strict decoding" a lie (YAML parsers reject it already).
func checkDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return walkKeys(dec, "")
}

func walkKeys(dec *json.Decoder, path string) error {
	tok, err := dec.Token()
	if err != nil {
		return nil // the syntax error is reported by the real parse
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil
			}
			key, _ := keyTok.(string)
			if seen[key] {
				return fmt.Errorf("duplicate key %q at %s", key, pathOrRoot(path))
			}
			seen[key] = true
			if err := walkKeys(dec, schema.Pointer(path, key)); err != nil {
				return err
			}
		}
		_, _ = dec.Token() // the closing brace
	case json.Delim('['):
		for i := 0; dec.More(); i++ {
			if err := walkKeys(dec, schema.Pointer(path, itoa(i))); err != nil {
				return err
			}
		}
		_, _ = dec.Token()
	}
	return nil
}
