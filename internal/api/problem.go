package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
)

const problemType = "https://chaos-gateway.dev/problems/"

// statusOf is the HTTP status of each error code (the table of the spec).
var statusOf = map[model.ErrorCode]int{
	model.ErrorCodeBadRequest:           http.StatusBadRequest,
	model.ErrorCodeUnauthorized:         http.StatusUnauthorized,
	model.ErrorCodeForbidden:            http.StatusForbidden,
	model.ErrorCodeCsrfFailed:           http.StatusForbidden,
	model.ErrorCodeNotFound:             http.StatusNotFound,
	model.ErrorCodeMethodNotAllowed:     http.StatusMethodNotAllowed,
	model.ErrorCodePayloadTooLarge:      http.StatusRequestEntityTooLarge,
	model.ErrorCodeRevisionConflict:     http.StatusConflict,
	model.ErrorCodeConfirmPending:       http.StatusConflict,
	model.ErrorCodeTargetBusy:           http.StatusConflict,
	model.ErrorCodeNameTaken:            http.StatusConflict,
	model.ErrorCodeNotACandidate:        http.StatusConflict,
	model.ErrorCodeSetupCompleted:       http.StatusConflict,
	model.ErrorCodeValidationFailed:     http.StatusUnprocessableEntity,
	model.ErrorCodeCapacityExceeded:     http.StatusUnprocessableEntity,
	model.ErrorCodeLockoutProtected:     http.StatusUnprocessableEntity,
	model.ErrorCodeUnsupportedFeature:   http.StatusUnprocessableEntity,
	model.ErrorCodeIdempotencyConflict:  http.StatusUnprocessableEntity,
	model.ErrorCodePreconditionRequired: http.StatusPreconditionRequired,
	model.ErrorCodeRateLimited:          http.StatusTooManyRequests,
	model.ErrorCodeApplyFailed:          http.StatusInternalServerError,
	model.ErrorCodeVerifyFailed:         http.StatusInternalServerError,
	model.ErrorCodeInternal:             http.StatusInternalServerError,
	model.ErrorCodeUnavailable:          http.StatusServiceUnavailable,
}

var titleOf = map[model.ErrorCode]string{
	model.ErrorCodeBadRequest:           "Bad request",
	model.ErrorCodeUnauthorized:         "Authentication required",
	model.ErrorCodeForbidden:            "Forbidden",
	model.ErrorCodeCsrfFailed:           "CSRF check failed",
	model.ErrorCodeNotFound:             "Not found",
	model.ErrorCodeMethodNotAllowed:     "Method not allowed",
	model.ErrorCodePayloadTooLarge:      "Payload too large",
	model.ErrorCodeRevisionConflict:     "Revision conflict",
	model.ErrorCodeConfirmPending:       "A revision waits for confirmation",
	model.ErrorCodeTargetBusy:           "Target busy",
	model.ErrorCodeNameTaken:            "Name taken",
	model.ErrorCodeNotACandidate:        "Not a candidate",
	model.ErrorCodeSetupCompleted:       "Setup is completed",
	model.ErrorCodeValidationFailed:     "Validation failed",
	model.ErrorCodeCapacityExceeded:     "Capacity exceeded",
	model.ErrorCodeLockoutProtected:     "Lockout protected",
	model.ErrorCodeUnsupportedFeature:   "Unsupported feature",
	model.ErrorCodeIdempotencyConflict:  "Idempotency conflict",
	model.ErrorCodePreconditionRequired: "Precondition required",
	model.ErrorCodeRateLimited:          "Too many requests",
	model.ErrorCodeApplyFailed:          "Apply failed",
	model.ErrorCodeVerifyFailed:         "Verification failed",
	model.ErrorCodeInternal:             "Internal error",
	model.ErrorCodeUnavailable:          "Service unavailable",
}

// problem is an RFC 9457 response under construction.
type problem struct {
	code   model.ErrorCode
	detail string
	extra  func(*model.Problem)
	header map[string]string
}

func newProblem(code model.ErrorCode, format string, a ...any) *problem {
	return &problem{code: code, detail: fmt.Sprintf(format, a...)}
}

func (p *problem) with(f func(*model.Problem)) *problem { p.extra = f; return p }

func (p *problem) withHeader(k, v string) *problem {
	if p.header == nil {
		p.header = map[string]string{}
	}
	p.header[k] = v
	return p
}

// Error lets a *problem travel as an error.
func (p *problem) Error() string { return string(p.code) + ": " + p.detail }

// write sends the problem.
func (s *Server) write(c *gin.Context, p *problem) {
	status := statusOf[p.code]
	if status == 0 {
		status = http.StatusInternalServerError
	}
	body := model.Problem{Type: problemType + string(p.code), Title: titleOf[p.code], Status: status, Code: p.code}
	if p.detail != "" {
		d := p.detail
		body.Detail = &d
	}
	inst := c.Request.URL.Path
	body.Instance = &inst
	if p.extra != nil {
		p.extra(&body)
	}
	for k, v := range p.header {
		c.Header(k, v)
	}
	c.Header("Content-Type", "application/problem+json")
	c.AbortWithStatusJSON(status, body)
	c.Header("Content-Type", "application/problem+json") // AbortWithStatusJSON sets application/json
}

// fail turns any error of the layers below into the problem it means and sends it.
func (s *Server) fail(c *gin.Context, err error) {
	var p *problem
	if errors.As(err, &p) {
		s.write(c, p)
		return
	}
	s.write(c, s.problemFor(err))
}

func validationErrors(errs domain.ValidationErrors) []model.ValidationError {
	out := make([]model.ValidationError, 0, len(errs))
	for _, e := range errs {
		out = append(out, model.ValidationError{Path: e.Path, Code: e.Code, Message: e.Message})
	}
	return out
}

func (s *Server) problemFor(err error) *problem {
	var (
		conflict *store.ErrRevisionConflict
		pending  *store.ErrConfirmPending
		notCand  *store.ErrNotACandidate
		expired  *store.ErrConfirmExpired
		ve       domain.ValidationErrors
		pe       *domain.ParseError
		af       *engine.ErrApplyFailed
		unsup    *engine.UnsupportedOverlayError
		comp     *engine.CompileError
	)
	switch {
	case errors.As(err, &conflict):
		return newProblem(model.ErrorCodeRevisionConflict, "%s", err).with(func(b *model.Problem) { a := conflict.Active; b.ActiveRevision = &a })
	case errors.As(err, &pending):
		return newProblem(model.ErrorCodeConfirmPending, "%s", err).with(func(b *model.Problem) { r := pending.Pending; b.PendingRevision = &r })
	case errors.As(err, &notCand):
		return newProblem(model.ErrorCodeNotACandidate, "%s", err)
	case errors.As(err, &expired):
		return newProblem(model.ErrorCodeNotACandidate, "%s", err)
	case errors.Is(err, store.ErrNotFound):
		return newProblem(model.ErrorCodeNotFound, "no such revision")
	case errors.As(err, &ve):
		errs := validationErrors(ve)
		for _, e := range ve {
			if e.Code == domain.CodeDuplicateName {
				return newProblem(model.ErrorCodeNameTaken, "%s", e.Message).with(func(b *model.Problem) { b.Errors = &errs })
			}
		}
		return newProblem(model.ErrorCodeValidationFailed, "the configuration is not valid (%d problems)", len(errs)).with(func(b *model.Problem) { b.Errors = &errs })
	case errors.As(err, &pe):
		return newProblem(model.ErrorCodeBadRequest, "%s", err)
	case errors.As(err, &af):
		if errors.Is(af.Cause, apply.ErrVerify) {
			return newProblem(model.ErrorCodeVerifyFailed, "%s", firstLine(af.Error()))
		}
		return newProblem(model.ErrorCodeApplyFailed, "%s", firstLine(af.Error()))
	case errors.As(err, &unsup):
		return newProblem(model.ErrorCodeUnsupportedFeature, "%s", unsup.Error())
	case errors.As(err, &comp):
		if p := problemsToError(comp.Problems); p != nil {
			return p
		}
		return newProblem(model.ErrorCodeValidationFailed, "%s", comp.Error())
	case errors.Is(err, engine.ErrOverlayNotFound):
		return newProblem(model.ErrorCodeNotFound, "no such overlay (it never existed, or it was removed or has expired)")
	case errors.Is(err, engine.ErrOverlayForbidden):
		return newProblem(model.ErrorCodeForbidden, "the overlay belongs to another owner: a token with the scope overlays only changes its own")
	case errors.Is(err, engine.ErrOverlayNoLease):
		return newProblem(model.ErrorCodeValidationFailed, "the overlay has no lease to renew")
	case errors.Is(err, engine.ErrTooManyOverlays):
		return newProblem(model.ErrorCodeCapacityExceeded, "too many active overlays: remove some, or let them expire")
	case errors.Is(err, engine.ErrNoConfiguration):
		return newProblem(model.ErrorCodeNotFound, "there is no active revision yet")
	case errors.Is(err, engine.ErrUnknownDevice):
		return newProblem(model.ErrorCodeNotFound, "%s", err)
	case errors.Is(err, engine.ErrClosed):
		return newProblem(model.ErrorCodeUnavailable, "the gateway engine is not running")
	}
	s.log.Error("request failed", "error", err)
	return newProblem(model.ErrorCodeInternal, "an unexpected error occurred")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// bindError is the error handler of the generated wrapper: a parameter that does not fit.
func (s *Server) bindError(c *gin.Context, err error, status int) {
	code := model.ErrorCodeBadRequest
	if status == http.StatusUnprocessableEntity {
		code = model.ErrorCodeValidationFailed
	}
	s.write(c, newProblem(code, "%s", err))
}

// unsupported answers an operation whose milestone is not in this build.
func (s *Server) unsupported(c *gin.Context) {
	o := s.opOf(c)
	m := "a later milestone"
	if o != nil {
		m = "milestone " + o.Milestone
	}
	s.write(c, newProblem(model.ErrorCodeUnsupportedFeature, "this operation is not available in this build; it arrives with %s (see GET /capabilities)", m))
}
