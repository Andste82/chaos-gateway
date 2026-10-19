package api

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// An abort of a run whose overlays could not be removed is not a clean 200: the run is recorded, and the answer is the
// problem apply_failed naming it.
func TestAnAbortWhoseOverlaysStayIsAnApplyFailure(t *testing.T) {
	s := &Server{log: slog.New(slog.DiscardHandler)}
	id := uuid.New()
	p := s.problemFor(&run.CleanupError{Run: model.Run{Id: id, Status: model.RunStatusAborted}, Err: errors.New("apply failed")})
	if p == nil || p.code != model.ErrorCodeApplyFailed || !strings.Contains(p.detail, id.String()) || !strings.Contains(p.detail, "could not be removed") {
		t.Errorf("%+v", p)
	}
}

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
