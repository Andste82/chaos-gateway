package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/apiserver"
	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
)

// Config is what the server works with.
type Config struct {
	Engine  *engine.Engine
	Store   *store.Store
	Auth    *auth.Store
	Audit   *audit.Log
	Secrets *secrets.Store
	// Exec reads the kernel state (interfaces, routes); the engine applies through the same.
	Exec      apply.Exec
	Namespace string
	Clock     clock.Clock
	Log       *slog.Logger
	// StateDir holds what the API keeps besides the stores (the idempotency keys).
	StateDir string
	// BootID identifies this start of the service (`GET /state`, `GET /system/info`).
	BootID  string
	Started time.Time
	// Version, Commit and BuildDate describe the build.
	Version, Commit, BuildDate string
	// Preflight returns the report of the last preflight run; nil means none was made.
	Preflight func() *PreflightReport
	// ConfirmTimeout overrides the commit-confirm window (tests use seconds).
	ConfirmTimeout time.Duration
}

// Server implements apiserver.ServerInterface.
type Server struct {
	cfg  Config
	log  *slog.Logger
	clk  clock.Clock
	ops  map[string]*op
	idem *idempotency
}

var _ apiserver.ServerInterface = (*Server)(nil)

// New creates the server.
func New(cfg Config) (*Server, error) {
	if cfg.Engine == nil || cfg.Store == nil || cfg.Auth == nil || cfg.Audit == nil || cfg.Secrets == nil || cfg.Exec == nil {
		return nil, errors.New("api: the engine, the stores, the audit log and the executor are required")
	}
	if cfg.Clock == nil {
		cfg.Clock = &clock.Real{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	ops, err := loadOps()
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, log: cfg.Log, clk: cfg.Clock, ops: ops}
	if s.idem, err = openIdempotency(cfg.StateDir, cfg.Clock); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the HTTP handler: the API under /api/v1.
func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.HandleMethodNotAllowed = true
	_ = r.SetTrustedProxies(nil) // the client address is the peer's: a header must not choose it
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(s.recovery(), s.headers(), s.guard(), s.idempotent())
	r.NoRoute(func(c *gin.Context) { s.write(c, newProblem(model.ErrorCodeNotFound, "no such resource")) })
	r.NoMethod(func(c *gin.Context) {
		s.write(c, &problem{code: model.ErrorCodeBadRequest, detail: "method not allowed", header: map[string]string{}})
	})
	apiserver.RegisterHandlersWithOptions(r, s, apiserver.GinServerOptions{BaseURL: "/api/v1", ErrorHandler: s.bindError})
	return r
}

// opOf returns the spec operation of the matched route.
func (s *Server) opOf(c *gin.Context) *op { return s.ops[c.Request.Method+" "+c.FullPath()] }

// Close releases what the server holds.
func (s *Server) Close() error { return s.idem.close() }

// ---- the principal

type principalKey struct{}

// Principal is who a request is made by.
type Principal struct {
	// Kind is "session", "token" or "setup".
	Kind  string
	Scope auth.Scope
	// SessionID and CSRF are set for sessions.
	SessionID string
	CSRF      string
	ExpiresAt time.Time
	// TokenID and TokenName are set for tokens.
	TokenID, TokenName string
}

func principalOf(c *gin.Context) *Principal {
	if v, ok := c.Get("principal"); ok {
		return v.(*Principal)
	}
	return nil
}

// actor is the Actor of the audit log and of created revisions.
func actorOf(p *Principal) model.Actor {
	switch {
	case p == nil:
		return model.Actor{Type: "system", Id: "system"}
	case p.Kind == "token":
		n := p.TokenName
		return model.Actor{Type: "token", Id: p.TokenID, Name: &n}
	case p.Kind == "setup":
		return model.Actor{Type: "user", Id: "admin"}
	}
	return model.Actor{Type: "user", Id: "admin"}
}

func viaOf(p *Principal) string {
	switch {
	case p == nil:
		return "system"
	case p.Kind == "session":
		return "ui"
	}
	return "api"
}

// record writes an audit entry for the request's principal. A failing log is logged, not fatal:
// the action was already done.
func (s *Server) record(c *gin.Context, action string, obj *audit.Object, rev int64, detail string) {
	p := principalOf(c)
	a := actorOf(p)
	e := audit.Entry{Actor: audit.Actor{Type: string(a.Type), ID: a.Id}, Via: viaOf(p), Action: action, Object: obj, Revision: rev, Detail: detail}
	if a.Name != nil {
		e.Actor.Name = *a.Name
	}
	if _, err := s.cfg.Audit.Append(e); err != nil {
		s.log.Error("cannot write the audit log", "action", action, "error", err)
	}
}

func (s *Server) generation() int64 { return int64(s.cfg.Engine.Snapshot().Generation) }

// setGeneration adds the Chaos-Generation header of a write.
func (s *Server) setGeneration(c *gin.Context) {
	c.Header("Chaos-Generation", itoa(s.generation()))
}

func contextOf(c *gin.Context) context.Context { return c.Request.Context() }

// PreflightReport is the answer of GET /system/preflight.
type PreflightReport struct {
	At     *time.Time       `json:"at,omitempty"`
	Checks []PreflightCheck `json:"checks"`
	Status string           `json:"status"`
}

// PreflightCheck is one line of it.
type PreflightCheck struct {
	Fix     string `json:"fix,omitempty"`
	ID      string `json:"id"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
