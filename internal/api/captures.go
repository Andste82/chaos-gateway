package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/capturemgr"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// ---- the manager of captures (M17)

// initCaptures makes the manager of the captures. Without a capture volume (Config.CapturesDir) there is none, and
// the operations answer `unsupported_feature`; without an executor that captures the manager starts nothing.
func (s *Server) initCaptures() error {
	if s.cfg.CapturesDir == "" {
		return nil
	}
	opener := s.cfg.Capturer
	if opener == nil {
		if o, ok := s.cfg.Exec.(capturemgr.Opener); ok {
			opener = o
		}
	}
	m, err := capturemgr.New(capturemgr.Config{
		Clock: s.clk, Dir: s.cfg.CapturesDir, Opener: opener, Gateway: s.cfg.Engine, Namespace: s.cfg.Namespace, Log: s.log,
		HostAddrs: s.hostAddrs, Quota: s.captureQuota, MinFreePercent: s.cfg.CaptureMinFree, DiskFree: s.cfg.CaptureDiskFree,
	})
	if err != nil {
		return err
	}
	if err := m.Recover(); err != nil {
		return err
	}
	s.captures = m
	return nil
}

// captureQuota is the total size of all captures: the setting of the active configuration, else the default of the
// service.
func (s *Server) captureQuota() int64 {
	if snap := s.cfg.Engine.Snapshot(); snap != nil && snap.Config != nil && snap.Config.Settings != nil && snap.Config.Settings.Retention != nil {
		if q := snap.Config.Settings.Retention.CapturesQuota; q != nil {
			if n, ok := domain.ParseByteSize(*q); ok && n > 0 {
				return n
			}
		}
	}
	if s.cfg.CaptureQuota > 0 {
		return s.cfg.CaptureQuota
	}
	return capturemgr.DefaultQuota
}

// closeCaptures stops the captures that run.
func (s *Server) closeCaptures() {
	if s.captures == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.captures.Close(ctx)
}

// captureAdapter is the captures as the runs use them (run.Captures). Without a manager every start fails, which
// a run reports as a missing capture and goes on.
type captureAdapter struct{ s *Server }

func (a captureAdapter) Start(ctx context.Context, scope model.CaptureScope, name string, runID uuid.UUID) (model.Capture, error) {
	if a.s.captures == nil {
		return model.Capture{}, capturemgr.ErrNoCaptures
	}
	n := name
	if len(n) > 64 {
		n = n[:64]
	}
	return a.s.captures.Start(ctx, capturemgr.StartRequest{CaptureRequest: model.CaptureRequest{Name: &n, Scope: scope}, Run: &runID})
}

func (a captureAdapter) Stop(ctx context.Context, id uuid.UUID, reason model.CaptureStopReason) (model.Capture, error) {
	if a.s.captures == nil {
		return model.Capture{}, capturemgr.ErrNotFound
	}
	return a.s.captures.Stop(ctx, id, reason)
}

func (a captureAdapter) Get(id uuid.UUID) (model.Capture, error) {
	if a.s.captures == nil {
		return model.Capture{}, capturemgr.ErrNotFound
	}
	return a.s.captures.Get(id)
}

func (a captureAdapter) OfRun(runID uuid.UUID) []model.Capture {
	if a.s.captures == nil {
		return nil
	}
	return a.s.captures.List(&runID)
}

func (a captureAdapter) DeleteRun(runID uuid.UUID) {
	if a.s.captures != nil {
		a.s.captures.DeleteRun(runID)
	}
}

var (
	_ run.Captures      = captureAdapter{}
	_ capturemgr.Opener = (*executor.Client)(nil)
	_ capturemgr.Opener = (*executor.Redialing)(nil)
	_ capturemgr.Opener = apply.Local{}
)

// ---- operations

// failCapture is fail for the problems of a capture: the errors of the manager are the ones the spec names.
func (s *Server) failCapture(c *gin.Context, err error) {
	var ve domain.ValidationErrors
	var unsup *capturemgr.UnsupportedError
	switch {
	case errors.As(err, &ve):
		errs := validationErrors(ve)
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the capture request is not valid (%d problems)", len(errs)).
			with(func(b *model.Problem) { b.Errors = &errs }))
	case errors.As(err, &unsup):
		s.write(c, newProblem(model.ErrorCodeUnsupportedFeature, "%s", unsup.Error()))
	case errors.Is(err, capturemgr.ErrNotFound):
		s.write(c, newProblem(model.ErrorCodeNotFound, "no such capture (it never existed, or it was deleted)"))
	case errors.Is(err, capturemgr.ErrRunning):
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the capture still runs: stop it first"))
	case errors.Is(err, capturemgr.ErrNoKeylog):
		s.write(c, newProblem(model.ErrorCodeNotFound, "the capture has no TLS key log"))
	case errors.Is(err, capturemgr.ErrNoCaptures):
		s.write(c, newProblem(model.ErrorCodeUnsupportedFeature, "this deployment cannot capture: the executor has no capture volume (--captures-dir)"))
	case errors.Is(err, capturemgr.ErrDiskLow):
		s.write(c, newProblem(model.ErrorCodeCapacityExceeded, "the volume of the captures has less than 10 %% free space: delete captures, or free the disk"))
	case errors.Is(err, capturemgr.ErrTooMany):
		s.write(c, newProblem(model.ErrorCodeCapacityExceeded, "%d captures run already: stop one first", capturemgr.MaxRunning))
	case errors.Is(err, capturemgr.ErrQuota):
		s.write(c, newProblem(model.ErrorCodeCapacityExceeded, "the running captures use the quota: stop one first"))
	case errors.Is(err, capturemgr.ErrUnavailable):
		s.write(c, newProblem(model.ErrorCodeUnavailable, "the executor does not answer: %v", err))
	case errors.Is(err, capturemgr.ErrClosed):
		s.write(c, newProblem(model.ErrorCodeUnavailable, "the gateway is stopping"))
	default:
		s.fail(c, err)
	}
}

// need answers the operations of a deployment without a capture volume.
func (s *Server) needCaptures(c *gin.Context) bool {
	if s.captures == nil {
		s.failCapture(c, capturemgr.ErrNoCaptures)
		return false
	}
	return true
}

// StartCapture implements POST /captures.
func (s *Server) StartCapture(c *gin.Context, _ model.StartCaptureParams) {
	if !s.needCaptures(c) {
		return
	}
	raw, rerr := readBody(c)
	if rerr != nil {
		s.write(c, rerr)
		return
	}
	req, err := domain.DecodeCaptureRequest(raw)
	if err != nil {
		s.failCapture(c, err)
		return
	}
	cp, err := s.captures.Start(contextOf(c), capturemgr.StartRequest{CaptureRequest: *req})
	if err != nil {
		s.failCapture(c, err)
		return
	}
	s.record(c, "capture.start", &audit.Object{Kind: "capture", ID: cp.Id.String(), Name: derefStr(cp.Name)}, 0, "")
	c.Header("Location", "/api/v1/captures/"+cp.Id.String())
	c.JSON(http.StatusCreated, cp)
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ListCaptures implements GET /captures, newest first.
func (s *Server) ListCaptures(c *gin.Context, params model.ListCapturesParams) {
	if !s.needCaptures(c) {
		return
	}
	var runID *uuid.UUID
	if params.Run != nil {
		u := *params.Run
		runID = &u
	}
	items, next, err := page(s.captures.List(runID), func(cp model.Capture) string {
		return padInt(int64(1)<<62-cp.StartedAt.UnixNano()) + "/" + cp.Id.String()
	}, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// GetCaptureStorage implements GET /captures/storage: the quota, the volume and the running captures.
func (s *Server) GetCaptureStorage(c *gin.Context) {
	if !s.needCaptures(c) {
		return
	}
	c.JSON(200, s.captures.Storage())
}

// GetCapture implements GET /captures/{captureId}.
func (s *Server) GetCapture(c *gin.Context, id model.CaptureId) {
	if !s.needCaptures(c) {
		return
	}
	cp, err := s.captures.Get(id)
	if err != nil {
		s.failCapture(c, err)
		return
	}
	c.JSON(200, cp)
}

// StopCapture implements POST /captures/{captureId}/stop; it answers when the files are closed.
func (s *Server) StopCapture(c *gin.Context, id model.CaptureId) {
	if !s.needCaptures(c) {
		return
	}
	cp, err := s.captures.Stop(context.WithoutCancel(contextOf(c)), id, model.CaptureStopReasonUser)
	if err != nil {
		s.failCapture(c, err)
		return
	}
	s.record(c, "capture.stop", &audit.Object{Kind: "capture", ID: id.String(), Name: derefStr(cp.Name)}, 0, string(cp.Status))
	c.JSON(200, cp)
}

// DeleteCapture implements DELETE /captures/{captureId}.
func (s *Server) DeleteCapture(c *gin.Context, id model.CaptureId) {
	if !s.needCaptures(c) {
		return
	}
	if err := s.captures.Delete(id); err != nil {
		s.failCapture(c, err)
		return
	}
	s.record(c, "capture.delete", &audit.Object{Kind: "capture", ID: id.String()}, 0, "")
	c.Status(http.StatusNoContent)
}

// DownloadCapture implements GET /captures/{captureId}/download: the capture as a file, also while it runs.
func (s *Server) DownloadCapture(c *gin.Context, id model.CaptureId, params model.DownloadCaptureParams) {
	if !s.needCaptures(c) {
		return
	}
	part := "capture"
	if params.Part != nil {
		part = string(*params.Part)
	}
	cp, err := s.captures.Get(id)
	if err != nil {
		s.failCapture(c, err)
		return
	}
	secret := part == "keylog" || (cp.Sensitive != nil && *cp.Sensitive)
	if p := principalOf(c); secret && (p == nil || p.Scope != auth.ScopeFull) {
		s.write(c, newProblem(model.ErrorCodeForbidden, "a key log and a capture that holds secrets are for the scope full"))
		return
	}
	d, err := s.captures.Open(id, part)
	if err != nil {
		s.failCapture(c, err)
		return
	}
	// a download of secrets is recorded before the first byte leaves, like every other one (M5-16)
	if secret {
		if err := s.tryRecord(c, "capture.download_secret", &audit.Object{Kind: "capture", ID: id.String(), Name: derefStr(cp.Name)}, 0, "part "+part); err != nil {
			s.write(c, newProblem(model.ErrorCodeUnavailable, "the audit log cannot be written"))
			return
		}
	}
	c.Header("Content-Type", d.ContentType)
	c.Header("Content-Disposition", `attachment; filename="`+d.Name+`"`)
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	// a capture can be larger than the memory: it is streamed, and a client that goes away ends the copy
	if _, err := d.WriteTo(c.Writer); err != nil {
		s.log.Warn("a capture download ended early", "capture", id, "error", err)
	}
}
