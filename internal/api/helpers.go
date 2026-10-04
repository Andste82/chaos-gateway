package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/audit"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// parseIfMatch reads the base revision from an If-Match header: a quoted id (`"42"`) as the
// ETag of the active revision, or the bare number.
func parseIfMatch(h string) (int64, error) {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "W/")
	h = strings.Trim(h, `"`)
	n, err := strconv.ParseInt(h, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("If-Match must be the id of the base revision, e.g. \"42\"")
	}
	return n, nil
}

// etag sets the ETag of the active revision.
func (s *Server) etag(c *gin.Context) {
	c.Header("ETag", `"`+itoa(s.cfg.Store.ActiveID())+`"`)
}

// configAt returns the revision a view reads: the active one, or the one named by ?revision=.
// It sends the problem and returns false when there is none.
func (s *Server) configAt(c *gin.Context, rev *int64) (model.Revision, *model.Configuration, bool) {
	var (
		r   model.Revision
		cfg *model.Configuration
		err error
	)
	if rev != nil {
		r, cfg, err = s.cfg.Store.Get(*rev)
	} else {
		r, cfg, err = s.cfg.Store.Active()
	}
	if err != nil {
		if rev == nil {
			s.write(c, newProblem(model.ErrorCodeNotFound, "there is no active revision yet"))
		} else {
			s.fail(c, err)
		}
		return r, nil, false
	}
	return r, cfg, true
}

// cursor encodes the sort key of the last item of a page.
func encodeCursor(key string) string { return base64.RawURLEncoding.EncodeToString([]byte(key)) }

func decodeCursor(c string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", errors.New("invalid cursor")
	}
	return string(b), nil
}

// page returns one page of items sorted by key ascending: those after the cursor, at most limit.
func page[T any](items []T, key func(T) string, cursor *string, limit *int) ([]T, *string, error) {
	n := 100
	if limit != nil {
		n = *limit
	}
	if n < 1 || n > 500 {
		return nil, nil, errors.New("limit must be between 1 and 500")
	}
	start := 0
	if cursor != nil && *cursor != "" {
		after, err := decodeCursor(*cursor)
		if err != nil {
			return nil, nil, err
		}
		for start < len(items) && key(items[start]) <= after {
			start++
		}
	}
	end := start + n
	if end >= len(items) {
		return items[start:], nil, nil
	}
	next := encodeCursor(key(items[end-1]))
	return items[start:end], &next, nil
}

// decodeJSON reads the body strictly: malformed JSON is `bad_request`, an unknown field is
// `validation_failed` with the code `unknown_field` (plan §2.15).
func decodeJSON(c *gin.Context, v any) *problem {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return bodyReadProblem(err)
	}
	return decodeBytes(raw, v)
}

// bodyReadProblem classifies a body-read error: the body exceeded the per-operation limit the
// guard middleware set with http.MaxBytesReader (`payload_too_large`), or anything else reading
// the body (`bad_request`).
func bodyReadProblem(err error) *problem {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return newProblem(model.ErrorCodePayloadTooLarge, "the request body exceeds the %d byte limit", mbe.Limit)
	}
	return newProblem(model.ErrorCodeBadRequest, "cannot read the request body: %v", err)
}

func decodeBytes(raw []byte, v any) *problem {
	if len(bytes.TrimSpace(raw)) == 0 {
		return newProblem(model.ErrorCodeBadRequest, "the request body is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			name := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
			errs := []model.ValidationError{{Path: "/" + name, Code: "unknown_field", Message: "unknown field " + strconv.Quote(name)}}
			return newProblem(model.ErrorCodeValidationFailed, "unknown field %q", name).with(func(b *model.Problem) { b.Errors = &errs })
		}
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			errs := []model.ValidationError{{Path: "/" + te.Field, Code: "invalid_type", Message: "expected " + te.Type.String()}}
			return newProblem(model.ErrorCodeValidationFailed, "the request body is not valid").with(func(b *model.Problem) { b.Errors = &errs })
		}
		return newProblem(model.ErrorCodeBadRequest, "malformed JSON: %v", err)
	}
	if dec.More() {
		return newProblem(model.ErrorCodeBadRequest, "malformed JSON: trailing data")
	}
	return nil
}

// requireFields reports the missing required fields of a request as a validation failure.
func requireFields(fields map[string]bool) *problem {
	var errs []model.ValidationError
	for name, ok := range fields {
		if !ok {
			errs = append(errs, model.ValidationError{Path: "/" + name, Code: "required", Message: name + " is required"})
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return newProblem(model.ErrorCodeValidationFailed, "the request body is not valid").with(func(b *model.Problem) { b.Errors = &errs })
}

func isUUID(s string) bool { _, err := uuid.Parse(s); return err == nil }

// findByRef finds an object of a configuration map by UUID or, case-insensitively, by name.
func findByRef[T any](m *map[string]T, ref string, name func(T) string) (string, T, bool) {
	var zero T
	if m == nil {
		return "", zero, false
	}
	if isUUID(ref) {
		if v, ok := (*m)[strings.ToLower(ref)]; ok {
			return strings.ToLower(ref), v, true
		}
	}
	for id, v := range *m {
		if strings.EqualFold(name(v), ref) {
			return id, v, true
		}
	}
	return "", zero, false
}

func notFound(what, ref string) *problem {
	return newProblem(model.ErrorCodeNotFound, "no %s %q", what, ref)
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

type auditFilter = audit.Filter
