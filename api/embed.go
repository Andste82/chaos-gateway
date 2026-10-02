// Package api embeds the OpenAPI document, the source of truth for the domain model and the
// REST API. The schema validator and the request validation of the API server read it from here.
package api

import _ "embed"

// Spec is the content of openapi.yaml.
//
//go:embed openapi.yaml
var Spec []byte
