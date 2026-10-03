package api

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/api"
	"github.com/Andste82/chaos-gateway/internal/auth"
)

// op is what the server needs to know about an operation of the spec: how it is secured and which
// milestone brings it.
type op struct {
	ID        string
	Method    string
	Path      string // OpenAPI path, relative to /api/v1
	Milestone string
	Scope     auth.Scope
	// Public operations need no session or token (they may name another credential: setup).
	Public bool
	// SetupToken operations take the one-time setup token instead of a session.
	SetupToken bool
	Internal   bool
}

// loadOps reads the operations from the embedded spec, keyed by "METHOD /gin/path" with the
// /api/v1 prefix, which is how gin reports the matched route.
func loadOps() (map[string]*op, error) {
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(api.Spec, &spec); err != nil {
		return nil, fmt.Errorf("api: read the spec: %w", err)
	}
	methods := map[string]string{"get": "GET", "put": "PUT", "post": "POST", "delete": "DELETE", "patch": "PATCH"}
	out := map[string]*op{}
	for path, item := range spec.Paths {
		for key, node := range item {
			m, ok := methods[key]
			if !ok {
				continue
			}
			var raw struct {
				OperationID string                 `yaml:"operationId"`
				Milestone   string                 `yaml:"x-milestone"`
				Scope       string                 `yaml:"x-required-scope"`
				Internal    bool                   `yaml:"x-internal"`
				Security    *[]map[string][]string `yaml:"security"`
			}
			if err := node.Decode(&raw); err != nil {
				return nil, fmt.Errorf("api: %s %s: %w", m, path, err)
			}
			o := &op{ID: raw.OperationID, Method: m, Path: path, Milestone: raw.Milestone, Scope: auth.Scope(raw.Scope), Internal: raw.Internal}
			if o.Scope == "" {
				return nil, fmt.Errorf("api: %s %s has no x-required-scope", m, path)
			}
			if raw.Security != nil { // `security: []` or a list of schemes that are not session or token
				o.Public = true
				for _, s := range *raw.Security {
					if _, ok := s["setupToken"]; ok {
						o.SetupToken = true
					}
				}
			}
			out[m+" /api/v1"+ginPath(path)] = o
		}
	}
	return out, nil
}

// ginPath turns an OpenAPI path ("/runs/{runId}") into a Gin path ("/runs/:runId").
func ginPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '{':
			b.WriteByte(':')
		case '}':
		default:
			b.WriteByte(p[i])
		}
	}
	return b.String()
}
