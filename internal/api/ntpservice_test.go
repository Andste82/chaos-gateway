package api_test

import (
	"testing"
)

// Stage 1 of M34 (D37) has the specification and no server: the internal endpoints exist so that the generated interface is
// implemented and a client gets a clear answer, not a 404 that looks like a wrong path. Stage 2 replaces these tests with the
// ones of the long poll and of the query intake.
func TestTheNTPServiceEndpointsAnswerThatTheServiceIsNotWiredYet(t *testing.T) {
	g, _ := serviceGateway(t)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/internal/ntp/config", nil},
		{"POST", "/internal/ntp/queries", []any{}},
	} {
		r := g.do(tc.method, tc.path, tc.body, nil, nil)
		if r.Status != 422 || r.code(t) != "unsupported_feature" {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, r.Status, r.Body)
		}
	}
}

// The service token is the only credential of these paths: a full token must not read or feed the NTP server's interface.
func TestTheNTPServiceEndpointsAreForTheServiceTokenOnly(t *testing.T) {
	g := ready(t)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/internal/ntp/config", nil},
		{"POST", "/internal/ntp/queries", []any{}},
	} {
		if r := g.do(tc.method, tc.path, tc.body, nil, nil); r.Status != 403 {
			t.Errorf("%s %s with a full token: %d %s", tc.method, tc.path, r.Status, r.Body)
		}
	}
}
