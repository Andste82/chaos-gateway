// Package domain is the domain model of Chaos Gateway: the types of api/openapi.yaml
// (internal/model) plus everything that gives them meaning (plan §2.1, §2.3, §2.4):
//
//   - strict decoding of JSON and YAML documents (decode.go)
//   - resolution of references by UUID or name and the checks that the schema cannot express
//     (refs.go, validate*.go)
//   - the built-in profiles (builtin.go)
//   - precedence resolution per fault family, overlays before configuration (resolve.go)
//   - the resolution as lookup tables per source of traffic, the compiler's input (table.go, sources.go)
//   - overlay requests and their keys (overlay.go), the overlays a revision orphans or moves (orphans.go)
//   - the observed state and device identity (observed.go)
//   - the domain diff of two configurations (diff.go)
//
// Everything here is a pure function of its inputs: no I/O, no clock, no global state. The
// compiler (M4) and the API (M5) build on it; persistence lives in internal/store.
package domain
