package api

import (
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// A configuration that needs more tc classes or fault ids than allowed is capacity_exceeded (plan
// §3.3, spec: `errors[]` names the scope that caused it), not a plain validation failure.
func TestACapacityProblemOfTheCompilerIsCapacityExceededWithItsScope(t *testing.T) {
	p := problemsToError([]compiler.Problem{
		{Severity: compiler.SevWarning, Code: "w", Message: "a warning is no error"},
		{Severity: compiler.SevError, Code: compiler.CodeCapacityExceeded, Scope: "network IoT", Message: "503 classes exceed the limit of 500"},
	})
	if p == nil || p.code != model.ErrorCodeCapacityExceeded {
		t.Fatalf("%+v", p)
	}
	var b model.Problem
	p.extra(&b)
	if b.Errors == nil || len(*b.Errors) != 1 || (*b.Errors)[0].Path != "network IoT" || (*b.Errors)[0].Code != compiler.CodeCapacityExceeded {
		t.Errorf("errors: %+v", b.Errors)
	}
	// without it the errors stay validation failures, and unsupported features stay what they were
	if p := problemsToError([]compiler.Problem{{Severity: compiler.SevError, Code: compiler.CodeUplinkMissing, Message: "x"}}); p == nil || p.code != model.ErrorCodeValidationFailed {
		t.Errorf("%+v", p)
	}
	if p := problemsToError([]compiler.Problem{{Severity: compiler.SevError, Code: compiler.CodeUnsupported, Message: "x"}}); p == nil || p.code != model.ErrorCodeUnsupportedFeature {
		t.Errorf("%+v", p)
	}
}
