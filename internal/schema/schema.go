// Package schema validates JSON documents against the schemas of api/openapi.yaml.
//
// It implements the subset of OpenAPI 3.0 that the spec uses and adds the rule that the spec
// cannot express (conventions, "strict decoding"): unknown fields are rejected, also in objects
// composed with allOf and in members of oneOf unions. Errors carry a JSON pointer and a stable
// machine-readable code; they are the `errors[]` of a `validation_failed` problem.
package schema

import (
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// Error codes of the schema validation.
const (
	CodeRequired      = "required"
	CodeUnknownField  = "unknown_field"
	CodeInvalidType   = "invalid_type"
	CodeEnum          = "enum"
	CodePattern       = "pattern"
	CodeFormat        = "format"
	CodeMinimum       = "minimum"
	CodeMaximum       = "maximum"
	CodeMinLength     = "min_length"
	CodeMaxLength     = "max_length"
	CodeMinItems      = "min_items"
	CodeMaxItems      = "max_items"
	CodeMinProperties = "min_properties"
	CodeMaxProperties = "max_properties"
	CodeDiscriminator = "discriminator"
)

// Validator checks documents against the schemas of one OpenAPI document.
type Validator struct {
	doc      *openapi3.T
	patterns map[string]*regexp.Regexp
}

// New loads an OpenAPI document and prepares the validator.
func New(spec []byte) (*Validator, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(spec)
	if err != nil {
		return nil, fmt.Errorf("schema: load OpenAPI document: %w", err)
	}
	v := &Validator{doc: doc, patterns: map[string]*regexp.Regexp{}}
	// compile every pattern once and fail early: a broken pattern is a bug in the spec
	for name, ref := range doc.Components.Schemas {
		if err := v.compile(ref, map[*openapi3.Schema]bool{}); err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
	}
	return v, nil
}

func (v *Validator) compile(ref *openapi3.SchemaRef, seen map[*openapi3.Schema]bool) error {
	if ref == nil || ref.Value == nil || seen[ref.Value] {
		return nil
	}
	s := ref.Value
	seen[s] = true
	if s.Pattern != "" {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			return fmt.Errorf("pattern %q: %w", s.Pattern, err)
		}
		v.patterns[s.Pattern] = re
	}
	for _, p := range s.Properties {
		if err := v.compile(p, seen); err != nil {
			return err
		}
	}
	for _, group := range []openapi3.SchemaRefs{s.AllOf, s.OneOf, s.AnyOf} {
		for _, m := range group {
			if err := v.compile(m, seen); err != nil {
				return err
			}
		}
	}
	if s.Items != nil {
		if err := v.compile(s.Items, seen); err != nil {
			return err
		}
	}
	if s.AdditionalProperties.Schema != nil {
		return v.compile(s.AdditionalProperties.Schema, seen)
	}
	return nil
}

// Has reports whether the document defines a schema of that name.
func (v *Validator) Has(name string) bool {
	_, ok := v.doc.Components.Schemas[name]
	return ok
}

// Schema returns the named schema, or nil.
func (v *Validator) Schema(name string) *openapi3.Schema {
	if ref, ok := v.doc.Components.Schemas[name]; ok {
		return ref.Value
	}
	return nil
}

// Validate checks value, a decoded JSON document (maps, slices, strings, bools, json.Number or
// float64), against the named schema and returns every problem found, sorted by path.
func (v *Validator) Validate(schemaName string, value any) []model.ValidationError {
	ref, ok := v.doc.Components.Schemas[schemaName]
	if !ok {
		return []model.ValidationError{{Path: "", Code: "internal", Message: "unknown schema " + schemaName}}
	}
	c := &checker{v: v}
	c.check(ref, value, "")
	sort.SliceStable(c.errs, func(i, j int) bool { return c.errs[i].Path < c.errs[j].Path })
	return c.errs
}

type checker struct {
	v    *Validator
	errs []model.ValidationError
}

func (c *checker) add(path, code, format string, args ...any) {
	c.errs = append(c.errs, model.ValidationError{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

// Pointer appends one reference token, escaped as RFC 6901 says.
func Pointer(base, token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return base + "/" + token
}

// flatten returns the schemas that together describe value: the schema itself and, recursively,
// its allOf members; a oneOf with a discriminator contributes the member the value selects.
func (c *checker) flatten(ref *openapi3.SchemaRef, value any, path string, out *[]*openapi3.Schema) {
	if ref == nil || ref.Value == nil {
		return
	}
	s := ref.Value
	*out = append(*out, s)
	for _, m := range s.AllOf {
		c.flatten(m, value, path, out)
	}
	if len(s.OneOf) > 0 {
		member := c.selectOneOf(s, value, path)
		if member != nil {
			c.flatten(member, value, path, out)
		}
	}
}

// selectOneOf picks the member of a discriminated union that value selects. It reports a
// problem and returns nil when the discriminator is missing or unknown.
func (c *checker) selectOneOf(s *openapi3.Schema, value any, path string) *openapi3.SchemaRef {
	obj, ok := value.(map[string]any)
	if !ok || s.Discriminator == nil {
		return nil // the type check reports a non-object; unions without discriminator are not used
	}
	prop := s.Discriminator.PropertyName
	raw, present := obj[prop]
	if !present {
		c.add(Pointer(path, prop), CodeRequired, "%s selects the kind of this object", prop)
		return nil
	}
	name, _ := raw.(string)
	target, mapped := s.Discriminator.Mapping[name]
	if !mapped {
		allowed := make([]string, 0, len(s.Discriminator.Mapping))
		for k := range s.Discriminator.Mapping {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		c.add(Pointer(path, prop), CodeDiscriminator, "must be one of: %s", strings.Join(allowed, ", "))
		return nil
	}
	schemaName := target.Ref[strings.LastIndex(target.Ref, "/")+1:]
	for _, m := range s.OneOf {
		if strings.HasSuffix(m.Ref, "/"+schemaName) {
			return m
		}
	}
	return nil
}

func (c *checker) check(ref *openapi3.SchemaRef, value any, path string) {
	var members []*openapi3.Schema
	c.flatten(ref, value, path, &members)
	if len(members) == 0 {
		return
	}

	// type: the first member that declares one
	var typ string
	for _, m := range members {
		if m.Type != nil && len(m.Type.Slice()) > 0 {
			typ = m.Type.Slice()[0]
			break
		}
	}
	// without a declared type, objects with properties are objects
	if typ == "" {
		for _, m := range members {
			if len(m.Properties) > 0 {
				typ = "object"
			}
		}
	}

	switch typ {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			c.add(path, CodeInvalidType, "must be an object")
			return
		}
		c.checkObject(members, obj, path)
	case "array":
		arr, ok := value.([]any)
		if !ok {
			c.add(path, CodeInvalidType, "must be an array")
			return
		}
		c.checkArray(members, arr, path)
	case "string":
		s, ok := value.(string)
		if !ok {
			c.add(path, CodeInvalidType, "must be a string")
			return
		}
		for _, m := range members {
			c.checkString(m, s, path)
		}
	case "integer", "number":
		n, ok := number(value)
		if !ok {
			c.add(path, CodeInvalidType, "must be a number")
			return
		}
		if typ == "integer" && n != math.Trunc(n) {
			c.add(path, CodeInvalidType, "must be an integer")
			return
		}
		for _, m := range members {
			c.checkNumber(m, n, path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			c.add(path, CodeInvalidType, "must be a boolean")
			return
		}
	}
	// enum and a few cross-type constraints apply to every kind of value
	for _, m := range members {
		if len(m.Enum) > 0 && !inEnum(m.Enum, value) {
			c.add(path, CodeEnum, "must be one of: %s", enumList(m.Enum))
		}
	}
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case interface{ Float64() (float64, error) }: // json.Number
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func inEnum(enum []any, value any) bool {
	for _, e := range enum {
		if f, ok := number(e); ok {
			if g, ok := number(value); ok && f == g {
				return true
			}
			continue
		}
		if e == value {
			return true
		}
	}
	return false
}

func enumList(enum []any) string {
	parts := make([]string, len(enum))
	for i, e := range enum {
		parts[i] = fmt.Sprint(e)
	}
	return strings.Join(parts, ", ")
}

func (c *checker) checkObject(members []*openapi3.Schema, obj map[string]any, path string) {
	declared := map[string][]*openapi3.SchemaRef{}
	var additional *openapi3.SchemaRef
	freeForm := false
	hasProps := false
	required := map[string]bool{}
	for _, m := range members {
		for name, p := range m.Properties {
			declared[name] = append(declared[name], p)
			hasProps = true
		}
		for _, r := range m.Required {
			required[r] = true
		}
		if m.AdditionalProperties.Schema != nil {
			additional = m.AdditionalProperties.Schema
		}
		if m.AdditionalProperties.Has != nil && *m.AdditionalProperties.Has {
			freeForm = true
		}
	}

	names := make([]string, 0, len(required))
	for r := range required {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, r := range names {
		if _, ok := obj[r]; !ok {
			c.add(Pointer(path, r), CodeRequired, "is required")
		}
	}

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := Pointer(path, k)
		if refs, ok := declared[k]; ok {
			for _, r := range refs {
				c.check(r, obj[k], child)
			}
			continue
		}
		switch {
		case additional != nil:
			c.check(additional, obj[k], child)
		case freeForm || !hasProps:
			// a free-form object
		default:
			c.add(child, CodeUnknownField, "unknown field %q", k)
		}
	}

	for _, m := range members {
		if m.MinProps > 0 && uint64(len(obj)) < m.MinProps {
			c.add(path, CodeMinProperties, "needs at least %d propert%s%s", m.MinProps, plural(m.MinProps), exactlyOneHint(m))
		}
		if m.MaxProps != nil && uint64(len(obj)) > *m.MaxProps {
			c.add(path, CodeMaxProperties, "allows at most %d propert%s%s", *m.MaxProps, plural(*m.MaxProps), exactlyOneHint(m))
		}
	}
}

func plural(n uint64) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// exactlyOneHint names the alternatives of an "exactly one of" object.
func exactlyOneHint(m *openapi3.Schema) string {
	if m.MinProps == 1 && m.MaxProps != nil && *m.MaxProps == 1 && len(m.Properties) > 0 {
		names := make([]string, 0, len(m.Properties))
		for n := range m.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		return " (exactly one of: " + strings.Join(names, ", ") + ")"
	}
	return ""
}

func (c *checker) checkArray(members []*openapi3.Schema, arr []any, path string) {
	var items []*openapi3.SchemaRef
	for _, m := range members {
		if m.MinItems > 0 && uint64(len(arr)) < m.MinItems {
			c.add(path, CodeMinItems, "needs at least %d item(s)", m.MinItems)
		}
		if m.MaxItems != nil && uint64(len(arr)) > *m.MaxItems {
			c.add(path, CodeMaxItems, "allows at most %d item(s)", *m.MaxItems)
		}
		if m.Items != nil {
			items = append(items, m.Items)
		}
	}
	for i, el := range arr {
		for _, it := range items {
			c.check(it, el, Pointer(path, strconv.Itoa(i)))
		}
	}
}

func (c *checker) checkString(m *openapi3.Schema, s, path string) {
	n := uint64(len([]rune(s)))
	if m.MinLength > 0 && n < m.MinLength {
		c.add(path, CodeMinLength, "must have at least %d character(s)", m.MinLength)
	}
	if m.MaxLength != nil && n > *m.MaxLength {
		c.add(path, CodeMaxLength, "must have at most %d character(s)", *m.MaxLength)
	}
	if m.Pattern != "" && !c.v.patterns[m.Pattern].MatchString(s) {
		c.add(path, CodePattern, "does not match %s", m.Pattern)
	}
	switch m.Format {
	case "uuid":
		if _, err := uuid.Parse(s); err != nil || len(s) != 36 {
			c.add(path, CodeFormat, "must be a UUID")
		}
	case "ipv4":
		if a, err := netip.ParseAddr(s); err != nil || !a.Is4() {
			c.add(path, CodeFormat, "must be an IPv4 address")
		}
	case "date-time":
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			c.add(path, CodeFormat, "must be an RFC 3339 time stamp")
		}
	}
}

func (c *checker) checkNumber(m *openapi3.Schema, n float64, path string) {
	if m.Min != nil && n < *m.Min {
		c.add(path, CodeMinimum, "must be at least %v", *m.Min)
	}
	if m.Max != nil && n > *m.Max {
		c.add(path, CodeMaximum, "must be at most %v", *m.Max)
	}
	if m.Format == "int32" && (n < math.MinInt32 || n > math.MaxInt32) {
		c.add(path, CodeFormat, "must fit in 32 bits")
	}
}
