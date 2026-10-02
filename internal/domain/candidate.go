package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// MergePatch applies a JSON Merge Patch (RFC 7396) to a document and returns the result; the
// inputs are not changed. Objects are merged key by key, `null` deletes a key, everything else
// (arrays, scalars) replaces. Maps keyed by UUID therefore add, change and delete single objects
// (`{"<uuid>": {...}}`, `{"<uuid>": null}`), the convention of the API.
func MergePatch(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return clonePlain(patch)
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	} else {
		t = clonePlain(t).(map[string]any)
	}
	for k, v := range p {
		if v == nil {
			delete(t, k)
			continue
		}
		t[k] = MergePatch(t[k], v)
	}
	return t
}

// clonePlain deep-copies generic JSON values.
func clonePlain(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = clonePlain(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clonePlain(e)
		}
		return out
	}
	return v
}

// CandidateMode says how the input of NewCandidate is read.
type CandidateMode int

const (
	// CandidateFull reads the input as a complete configuration (also used for import).
	CandidateFull CandidateMode = iota
	// CandidatePatch reads the input as a JSON Merge Patch against the base configuration.
	CandidatePatch
)

// NewCandidate builds the configuration of a candidate revision (plan §2.1.1, POST
// /revisions): it merges a patch onto the base, validates the document against the schema,
// resolves names to UUIDs, assigns `created_at` to new faults (an existing fault keeps its time:
// the value is "set by the server ... any value sent by a client is ignored") and runs the
// semantic validation. The error is a *ParseError or ValidationErrors; nothing is stored.
//
// base may be nil only for CandidateFull.
func NewCandidate(base *model.Configuration, input []byte, f Format, mode CandidateMode, now time.Time, opts ...Option) (*model.Configuration, error) {
	doc, err := ParseDocument(input, f)
	if err != nil {
		return nil, err
	}
	switch mode {
	case CandidatePatch:
		if base == nil {
			return nil, errors.New("domain: a patch needs a base configuration")
		}
		if _, ok := doc.(map[string]any); !ok {
			return nil, ValidationErrors{{Path: "", Code: schemaTypeCode, Message: "a merge patch must be an object"}}
		}
		doc = MergePatch(jsonValue(*base), doc)
	}
	// the secrets block is an import/export feature handled outside the revision
	cfg, err := decodeLoose(doc)
	if err != nil {
		return nil, err
	}
	assignFaultTimes(base, cfg, now)
	norm, errs := Normalize(cfg, opts...)
	errs = append(errs, Validate(norm, opts...)...)
	if len(errs) > 0 {
		return nil, ValidationErrors(sortErrors(errs))
	}
	return norm, nil
}

const schemaTypeCode = "invalid_type"

// jsonValue converts a model value into generic JSON values.
func jsonValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("domain: marshal: %v", err))
	}
	dec := json.NewDecoder(bytesReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		panic(fmt.Sprintf("domain: unmarshal: %v", err))
	}
	return out
}

// decodeLoose validates the schema of a configuration document and decodes it, without the
// semantic checks (NewCandidate runs them after the references are resolved).
func decodeLoose(doc any) (*model.Configuration, error) {
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
	return &cfg, nil
}

// assignFaultTimes sets created_at on faults: the time of the base for a fault that existed,
// `now` for a new one. Any value the client sent is replaced.
func assignFaultTimes(base, next *model.Configuration, now time.Time) {
	if next.Faults == nil {
		return
	}
	var old map[string]model.ConfigFault
	if base != nil {
		old = deref(base.Faults)
	}
	faults := *next.Faults
	for id, f := range faults {
		if prev, ok := old[id]; ok && prev.CreatedAt != nil {
			t := *prev.CreatedAt
			f.CreatedAt = &t
		} else {
			t := now.UTC()
			f.CreatedAt = &t
		}
		faults[id] = f
	}
}

// Equal reports whether two configurations are the same document.
func Equal(a, b *model.Configuration) bool {
	return reflect.DeepEqual(jsonValue(*a), jsonValue(*b))
}
