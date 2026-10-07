package api

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// preconditionOps need an If-Match header: a missing one is 428, not the 400 of the generated binding.
var preconditionOps = map[string]bool{"createRevision": true, "cloneRevision": true}

type revisionWithConfiguration struct {
	model.Revision
	Configuration *model.Configuration `json:"configuration"`
}

type applyResultBody struct {
	Revision        int64      `json:"revision"`
	Status          string     `json:"status"`
	Generation      int64      `json:"generation"`
	DurationMs      int64      `json:"duration_ms"`
	ConfirmDeadline *time.Time `json:"confirm_deadline,omitempty"`
	// RemovedOverlays are the overlays a forced apply removed (event overlay_orphaned).
	RemovedOverlays []model.Uuid `json:"removed_overlays,omitempty"`
}

func applyResult(a engine.Applied) applyResultBody {
	r := applyResultBody{Revision: a.Revision, Status: a.Status, Generation: int64(a.Generation), DurationMs: a.Duration.Milliseconds()}
	if !a.ConfirmDeadline.IsZero() {
		d := a.ConfirmDeadline.UTC()
		r.ConfirmDeadline = &d
	}
	r.RemovedOverlays = a.RemovedOverlays
	return r
}

// ListRevisions implements GET /revisions: newest first, without the configurations.
func (s *Server) ListRevisions(c *gin.Context, params model.ListRevisionsParams) {
	opts := store.ListOptions{}
	if params.Status != nil {
		opts.Status = string(*params.Status)
	}
	all, err := s.cfg.Store.List(opts)
	if err != nil {
		s.fail(c, err)
		return
	}
	items, next, perr := page(all, func(r model.Revision) string { return fmt.Sprintf("%020d", math.MaxInt64-r.Id) }, params.Cursor, params.Limit)
	if perr != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", perr))
		return
	}
	body := gin.H{"items": items}
	if next != nil {
		body["next_cursor"] = *next
	}
	c.JSON(200, body)
}

// GetActiveRevision implements GET /revisions/active.
func (s *Server) GetActiveRevision(c *gin.Context) {
	r, cfg, ok := s.configAt(c, nil)
	if !ok {
		return
	}
	s.etag(c)
	c.JSON(200, revisionWithConfiguration{Revision: r, Configuration: cfg})
}

// GetRevision implements GET /revisions/{revisionId}.
func (s *Server) GetRevision(c *gin.Context, revisionId model.RevisionId) {
	r, cfg, err := s.cfg.Store.Get(revisionId)
	if err != nil {
		s.fail(c, err)
		return
	}
	s.etag(c)
	c.JSON(200, revisionWithConfiguration{Revision: r, Configuration: cfg})
}

// CreateRevision implements POST /revisions: a candidate from a complete configuration or a JSON
// Merge Patch against the base revision (plan §2.1.1). A candidate that does not validate is not stored.
func (s *Server) CreateRevision(c *gin.Context, params model.CreateRevisionParams) {
	base, err := parseIfMatch(params.IfMatch)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	var mode domain.CandidateMode
	switch c.ContentType() {
	case "application/json":
		mode = domain.CandidateFull
	case "application/merge-patch+json":
		mode = domain.CandidatePatch
	default:
		s.write(c, newProblem(model.ErrorCodeBadRequest, "the content type must be application/json (a complete configuration) or application/merge-patch+json"))
		return
	}
	raw, rerr := io.ReadAll(c.Request.Body)
	if rerr != nil {
		s.write(c, bodyReadProblem(rerr))
		return
	}
	active := s.cfg.Store.ActiveID()
	if base != active {
		s.fail(c, &store.ErrRevisionConflict{Active: active})
		return
	}
	var baseCfg *model.Configuration
	if active != 0 {
		if _, baseCfg, err = s.cfg.Store.Get(active); err != nil {
			s.fail(c, err)
			return
		}
	}
	msg := ""
	if params.Message != nil {
		msg = *params.Message
	}
	rev, err := s.createCandidate(c, baseCfg, raw, mode, base, msg)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.Header("Location", fmt.Sprintf("/api/v1/revisions/%d", rev.Id))
	c.JSON(http.StatusCreated, rev)
}

// createCandidate builds, provisions and stores a candidate; the audit entry and the event are
// part of it.
func (s *Server) createCandidate(c *gin.Context, base *model.Configuration, raw []byte, mode domain.CandidateMode, ifMatch int64, message string) (model.Revision, error) {
	now := s.clk.Now()
	cfg, err := domain.NewCandidate(base, raw, domain.FormatJSON, mode, now)
	if err != nil {
		return model.Revision{}, err
	}
	cfg, undo, err := s.importAndProvision(cfg)
	if err != nil {
		return model.Revision{}, err
	}
	rev, err := s.storeCandidate(c, cfg, ifMatch, message, now)
	if err != nil {
		undo() // a rejected candidate leaves no imported keys behind
		return model.Revision{}, err
	}
	return rev, nil
}

// importAndProvision stores the keys an import brings (never replacing a key that is stored: see
// wireguard.ImportSecrets) and generates the keys that are still missing. undo removes the keys the
// import created.
func (s *Server) importAndProvision(cfg *model.Configuration) (*model.Configuration, func(), error) {
	undo := func() {}
	if cfg.Secrets != nil {
		created, err := wireguard.ImportSecrets(s.cfg.Secrets, cfg.Secrets)
		undo = func() {
			for _, id := range created {
				_ = s.cfg.Secrets.DeleteWireGuard(id)
			}
		}
		if err != nil {
			undo()
			return nil, func() {}, domain.ValidationErrors{{Path: "/secrets", Code: "invalid_secrets", Message: err.Error()}}
		}
		cfg.Secrets = nil
	}
	out, err := wireguard.Provision(cfg, s.cfg.Secrets)
	if err != nil {
		undo()
		return nil, func() {}, err
	}
	return out, undo, nil
}

func (s *Server) storeCandidate(c *gin.Context, cfg *model.Configuration, ifMatch int64, message string, now time.Time) (model.Revision, error) {
	by := actorOf(principalOf(c))
	rev, err := s.cfg.Store.Create(cfg, store.CreateOptions{IfMatch: ifMatch, Message: message, By: by, Now: now})
	if err != nil {
		return model.Revision{}, err
	}
	s.record(c, "revision.create", &audit.Object{Kind: "revision", ID: itoa(rev.Id)}, rev.Id, message)
	s.cfg.Engine.Emit("revision_created", map[string]any{"revision": rev.Id, "base": ifMatch, "actor": by, "subject": engine.Subject{Kind: "revision", ID: itoa(rev.Id)}})
	return rev, nil
}

// CloneRevision implements POST /revisions/{revisionId}/clone: the rollback path.
func (s *Server) CloneRevision(c *gin.Context, revisionId model.RevisionId, params model.CloneRevisionParams) {
	base, err := parseIfMatch(params.IfMatch)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	_, src, err := s.cfg.Store.Get(revisionId)
	if err != nil {
		s.fail(c, err)
		return
	}
	rev, err := s.storeCandidate(c, src, base, fmt.Sprintf("clone of revision %d", revisionId), s.clk.Now())
	if err != nil {
		s.fail(c, err)
		return
	}
	c.Header("Location", fmt.Sprintf("/api/v1/revisions/%d", rev.Id))
	c.JSON(http.StatusCreated, rev)
}

// DiscardRevision implements DELETE /revisions/{revisionId}.
func (s *Server) DiscardRevision(c *gin.Context, revisionId model.RevisionId) {
	if err := s.cfg.Store.Discard(revisionId); err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "revision.discard", &audit.Object{Kind: "revision", ID: itoa(revisionId)}, revisionId, "")
	c.Status(http.StatusNoContent)
}

// PreviewRevision implements POST /revisions/{revisionId}/preview.
func (s *Server) PreviewRevision(c *gin.Context, revisionId model.RevisionId) {
	p, err := s.cfg.Engine.Preview(contextOf(c), revisionId)
	if err != nil {
		s.fail(c, err)
		return
	}
	if perr := problemsToError(p.Problems); perr != nil {
		s.write(c, perr)
		return
	}
	warnings := []gin.H{}
	for _, pr := range p.Problems {
		w := gin.H{"code": pr.Code, "message": pr.Message}
		if pr.Network != "" {
			w["path"] = "/networks/" + pr.Network
		}
		warnings = append(warnings, w)
	}
	dom := p.Domain
	if dom == nil {
		dom = []model.DomainChange{}
	}
	body := gin.H{
		"revision":         p.Revision,
		"domain":           dom,
		"linux":            gin.H{"nftables": p.Linux.Nftables, "routes": p.Linux.Routes, "wireguard": p.Linux.WireGuard, "bird": p.Linux.Bird},
		"requires_confirm": p.NeedsConfirmation,
		"warnings":         warnings,
	}
	if p.Base != 0 {
		body["base"] = p.Base
	}
	if len(p.References) > 0 {
		body["references"] = blockingReferences(p.References)
	}
	c.JSON(200, body)
}

// problemsToError turns the errors the compiler reports for a target into a validation failure.
func problemsToError(ps []compiler.Problem) *problem {
	var errs []model.ValidationError
	unsupported, capacity := false, false
	for _, p := range ps {
		if p.Severity != compiler.SevError {
			continue
		}
		if p.Code == compiler.CodeUnsupported {
			unsupported = true
		}
		path := ""
		if p.Network != "" {
			path = "/networks/" + p.Network
		}
		if p.Code == compiler.CodeCapacityExceeded {
			// the scope that caused it ("network IoT"): there is no object pointer for a scope
			capacity, path = true, p.Scope
		}
		errs = append(errs, model.ValidationError{Path: path, Code: p.Code, Message: p.Message})
	}
	if len(errs) == 0 {
		return nil
	}
	code := model.ErrorCodeValidationFailed
	switch {
	case capacity:
		code = model.ErrorCodeCapacityExceeded
	case unsupported:
		code = model.ErrorCodeUnsupportedFeature
	}
	return newProblem(code, "%s", errs[0].Message).with(func(b *model.Problem) { b.Errors = &errs })
}

// ApplyRevision implements POST /revisions/{revisionId}/apply.
func (s *Server) ApplyRevision(c *gin.Context, revisionId model.RevisionId, params model.ApplyRevisionParams) {
	// the compiler's errors are the client's to fix: they are reported as such, before anything is
	// touched; the engine only sees a target that compiles
	pv, err := s.cfg.Engine.Preview(contextOf(c), revisionId)
	if err != nil {
		s.fail(c, err)
		return
	}
	if perr := problemsToError(pv.Problems); perr != nil {
		s.write(c, perr)
		return
	}
	// the apply is not the client's to cancel: the engine goes on, and so does the record of it
	force := params.Force != nil && *params.Force
	res, err := s.cfg.Engine.Apply(context.WithoutCancel(contextOf(c)), revisionId, engine.ApplyOptions{
		ConfirmTimeout: s.cfg.ConfirmTimeout, Force: force, Actor: actorOf(principalOf(c))})
	if err != nil {
		s.record(c, "revision.apply_failed", &audit.Object{Kind: "revision", ID: itoa(revisionId)}, revisionId, firstLine(err.Error()))
		s.fail(c, err)
		return
	}
	detail := res.Status
	if n := len(res.RemovedOverlays); n > 0 {
		detail = fmt.Sprintf("%s, removed %d overlays", res.Status, n)
	}
	s.record(c, "revision.apply", &audit.Object{Kind: "revision", ID: itoa(revisionId)}, revisionId, detail)
	s.cfg.Engine.Emit("revision_applied", map[string]any{"revision": revisionId, "status": res.Status,
		"actor": actorOf(principalOf(c)), "subject": engine.Subject{Kind: "revision", ID: itoa(revisionId)}})
	c.Header("Chaos-Generation", itoa(int64(res.Generation)))
	c.JSON(200, applyResult(res))
}

// blockingReferences are the overlays a revision would orphan, as the spec's BlockingReference.
func blockingReferences(refs []engine.OverlayReference) []model.BlockingReference {
	out := make([]model.BlockingReference, 0, len(refs))
	for _, r := range refs {
		owner := r.Overlay.Owner
		out = append(out, model.BlockingReference{Kind: model.BlockingReferenceKindOverlay, Id: r.Overlay.Id, Owner: &owner, Object: r.Object})
	}
	return out
}

// ConfirmRevision implements POST /revisions/{revisionId}/confirm.
func (s *Server) ConfirmRevision(c *gin.Context, revisionId model.RevisionId) {
	if err := s.cfg.Engine.Confirm(contextOf(c), revisionId, actorOf(principalOf(c))); err != nil {
		s.fail(c, err)
		return
	}
	r, _, err := s.cfg.Store.Get(revisionId)
	if err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "revision.confirm", &audit.Object{Kind: "revision", ID: itoa(revisionId)}, revisionId, "")
	c.JSON(200, r)
}

// DiffRevision implements GET /revisions/{revisionId}/diff.
func (s *Server) DiffRevision(c *gin.Context, revisionId model.RevisionId, params model.DiffRevisionParams) {
	base := s.cfg.Store.ActiveID()
	if params.Base != nil {
		base = *params.Base
	}
	var changes []model.DomainChange
	var err error
	if base == 0 {
		_, cfg, gerr := s.cfg.Store.Get(revisionId)
		if gerr != nil {
			s.fail(c, gerr)
			return
		}
		changes = domain.Diff(&model.Configuration{}, cfg)
	} else {
		changes, err = s.cfg.Store.Diff(base, revisionId)
		if err != nil {
			s.fail(c, err)
			return
		}
	}
	if changes == nil {
		changes = []model.DomainChange{}
	}
	c.JSON(200, changes)
}

// ExportRevision implements GET /revisions/{revisionId}/export.
func (s *Server) ExportRevision(c *gin.Context, revisionId model.RevisionId, params model.ExportRevisionParams) {
	_, cfg, err := s.cfg.Store.Get(revisionId)
	if err != nil {
		s.fail(c, err)
		return
	}
	withSecrets := params.IncludeSecrets != nil && *params.IncludeSecrets
	if withSecrets {
		if p := principalOf(c); p == nil || !p.Scope.Allows(auth.ScopeFull) {
			s.write(c, newProblem(model.ErrorCodeForbidden, "an export with secrets needs the scope full"))
			return
		}
		sec, err := wireguard.ExportSecrets(cfg, s.cfg.Secrets)
		if err != nil {
			s.fail(c, err)
			return
		}
		cp := *cfg
		cp.Secrets = sec
		cfg = &cp
		s.record(c, "revision.export_secrets", &audit.Object{Kind: "revision", ID: itoa(revisionId)}, revisionId, "")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="chaosgw-revision-%d-secret.%s"`, revisionId, formatExt(params.Format)))
	} else {
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="chaosgw-revision-%d.%s"`, revisionId, formatExt(params.Format)))
	}
	if params.Format != nil && *params.Format == model.Json {
		c.JSON(200, cfg)
		return
	}
	var doc any
	raw, merr := jsonMarshal(cfg)
	if merr != nil {
		s.fail(c, merr)
		return
	}
	if err := jsonUnmarshal(raw, &doc); err != nil {
		s.fail(c, err)
		return
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.Data(200, "application/yaml", out)
}

func formatExt(f *model.ExportRevisionParamsFormat) string {
	if f != nil && *f == model.Json {
		return "json"
	}
	return "yaml"
}

var _ = strings.TrimSpace
