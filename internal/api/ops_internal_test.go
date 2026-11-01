package api

import (
	"strings"
	"testing"
)

// A scope the code does not know used to rank 0 and be met by every principal, so a misspelled
// x-required-scope turned an operation into one that any token could call (P7-M29 audit S4).
func TestTheSpecWithAnUnknownScopeIsRefused(t *testing.T) {
	spec := func(scope string) []byte {
		return []byte("paths:\n  /x:\n    get:\n      operationId: getX\n      x-required-scope: " + scope + "\n")
	}
	for _, ok := range []string{"read", "overlays", "full", "service", "none"} {
		if _, err := parseOps(spec(ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "reed", "Read", "admin", "write", "readonly"} {
		_, err := parseOps(spec(bad))
		if err == nil {
			t.Errorf("the scope %q is accepted", bad)
		} else if bad != "" && !strings.Contains(err.Error(), "unknown x-required-scope") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
