package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/apiserver"
	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/capturemgr"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
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
	// UplinkWait is how long an apply waits for the uplink's IPv4 address when the host has none at the
	// moment (engine.PreviewAwaitingUplink); zero takes engine.DefaultUplinkWait, a negative value does
	// not wait.
	UplinkWait time.Duration
	// UI is the build of the web app (web/dist), served for every GET outside /api/; nil serves none.
	UI fs.FS
	// Resolvers returns the host's upstream resolvers for the DNS proxy when the uplink names none;
	// nil reads the host's resolver files.
	Resolvers func() []netip.Addr
	// TrafficInterval is how often the uplink's counters are read for GET /uplink/traffic and
	// TrafficPeriod how long a rate is kept; zero takes the defaults (5 s, 10 min).
	TrafficInterval, TrafficPeriod time.Duration
	// RunRetention is how long the record of an ended run is kept (plan §3.6); zero takes run.DefaultRetention.
	RunRetention time.Duration
	// RunMax limits the runs that are queued or running at the same time; zero takes run.DefaultMaxRuns.
	RunMax int
	// CapturesDir is the volume of the packet captures (M17), shared with the executor, which writes the files; empty
	// for a deployment without one (the operations of captures answer `unsupported_feature`). Capturer starts the
	// captures: the executor behind Exec when it can, unless a test gives another.
	CapturesDir string
	Capturer    capturemgr.Opener
	// CaptureQuota is the total size of all captures when the configuration names none (zero: 5 GiB);
	// CaptureMinFree and CaptureDiskFree are the free-space limit of the volume in percent (zero: 10) and how to read it
	// (nil: statfs of CapturesDir).
	CaptureQuota    int64
	CaptureMinFree  float64
	CaptureDiskFree func() (free, total uint64, err error)
	// TLSCADir holds the two CAs of the TLS responder (M21): the API makes them at its start (tlsresponder.EnsureCAs)
	// and serves the test CA's certificate from it (GET /tls/ca); the responder's container reads the directory,
	// keys included. Empty for a deployment without one (the download answers `unavailable`).
	TLSCADir string
	// ProcDir is where the metrics endpoint reads the load and the memory of the machine (M26); empty takes /proc.
	ProcDir string
	// Diagnostics sets up the diagnostics of M24; the zero value is the production setup (tests name their own servers).
	Diagnostics DiagnosticsConfig
}

// Server implements apiserver.ServerInterface.
type Server struct {
	cfg  Config
	log  *slog.Logger
	clk  clock.Clock
	ops  map[string]*op
	idem *idempotency
	// setupMu serializes POST /setup: two requests with the token must not run the setup twice.
	setupMu sync.Mutex
	streams atomic.Int64
	dns     dnsState
	tls     tlsState
	// intercept is what the interception proxy reported (M22)
	intercept interceptState
	// health caches the unauthenticated health check's executor probe (M5-13): an unauthenticated
	// endpoint must not give every caller its own round trip to the executor.
	health healthProbeCache
	// osConfigErr is the last failure to read the host's network configuration, so that a screen that
	// polls GET /system/interfaces logs a failure once, not at every request (P3-M12-07)
	osConfigErr lastError
	// setupOSConfig caches that read for the unauthenticated GET /setup (Phase 3 audit, as health)
	setupOSConfig osConfigCache
	// unsubEvents ends the background subscriber that audits system-originated changes (M5-05).
	unsubEvents func()
	eventsDone  chan struct{}
	// traffic is the history of the uplink's rates (GET /uplink/traffic), sampler what fills it
	traffic *uplinkTraffic
	sampler trafficSampler
	// runs is the engine of scenario runs (M15), runsTemp the directory it uses when there is no state directory
	runs     *run.Manager
	runsTemp string
	// captures is the manager of the packet captures (M17), nil without a capture volume
	captures *capturemgr.Manager
	// diagnostics runs POST /diagnostics (M24)
	diagnostics *diagnostics
	// calibrating holds the probes whose calibration runs (M25)
	calibrating calibrations
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
	if cfg.UplinkWait == 0 {
		cfg.UplinkWait = engine.DefaultUplinkWait
	}
	if cfg.TrafficInterval == 0 {
		cfg.TrafficInterval = defaultTrafficInterval
	}
	if cfg.TrafficPeriod == 0 {
		cfg.TrafficPeriod = defaultTrafficPeriod
	}
	if cfg.TrafficInterval < time.Second || cfg.TrafficPeriod < cfg.TrafficInterval {
		return nil, errors.New("api: the uplink traffic interval must be at least a second and the period at least the interval")
	}
	ops, err := loadOps()
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, log: cfg.Log, clk: cfg.Clock, ops: ops, traffic: &uplinkTraffic{interval: cfg.TrafficInterval, period: cfg.TrafficPeriod}}
	if s.idem, err = openIdempotency(cfg.StateDir, cfg.Clock); err != nil {
		return nil, err
	}
	events, unsub := cfg.Engine.Subscribe()
	s.unsubEvents = unsub
	s.eventsDone = make(chan struct{})
	go s.auditSystemEvents(events)
	s.initDiagnostics()
	s.startTrafficSampler()
	if err := s.initCaptures(); err != nil {
		s.stopTrafficSampler()
		unsub()
		<-s.eventsDone
		return nil, err
	}
	if err := s.initRuns(); err != nil {
		s.closeCaptures()
		s.stopTrafficSampler()
		unsub()
		<-s.eventsDone
		return nil, err
	}
	return s, nil
}

// auditSystemEvents records the audit log entries of changes the engine makes on its own (M5-05):
// today, only a commit-confirm rollback (timeout, or an unconfirmed revision found at restart). A
// rollback an admin requested (POST /revisions/{id}/rollback, reason "requested") is the admin's and
// RollbackRevision records it: it is not recorded a second time as the system's (Phase 3 audit). A
// rollback that leaves nothing committed, requested or not, was the setup's own first revision (M5-03):
// the admin password it set is of no use without the configuration it was paired with, so the setup
// reopens.
func (s *Server) auditSystemEvents(events <-chan engine.Event) {
	defer close(s.eventsDone)
	for ev := range events {
		if ev.Type != engine.EventRolledBack {
			continue
		}
		rev, _ := ev.Data["revision"].(int64)
		reason, _ := ev.Data["reason"].(string)
		if reason != "requested" {
			_, _ = s.cfg.Audit.Append(audit.Entry{Actor: audit.Actor{Type: "system", ID: "system"}, Via: "system",
				Action: "revision.rolled_back", Revision: rev, Detail: reason})
		}
		if s.cfg.Engine.Snapshot().Revision != 0 {
			continue
		}
		tok, err := s.cfg.Auth.ReopenSetup()
		if err != nil {
			s.log.Error("cannot reopen the setup after the unconfirmed first revision was rolled back", "error", err)
			continue
		}
		s.log.Warn("the setup's first revision was rolled back without confirmation: setup is open again", "setup_token", tok)
	}
}

// Handler returns the HTTP handler: the API under /api/v1 and, with Config.UI, the web app.
func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.HandleMethodNotAllowed = true
	_ = r.SetTrustedProxies(nil) // the client address is the peer's: a header must not choose it
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(s.recovery(), s.headers(), s.guard(), s.idempotent())
	r.NoRoute(func(c *gin.Context) {
		if s.serveUI(c) {
			return
		}
		s.write(c, newProblem(model.ErrorCodeNotFound, "no such resource"))
	})
	r.NoMethod(func(c *gin.Context) {
		s.write(c, newProblem(model.ErrorCodeMethodNotAllowed, "method not allowed").withHeader("Allow", strings.Join(s.allowedMethods(c.Request.URL.Path), ", ")))
	})
	apiserver.RegisterHandlersWithOptions(r, s, apiserver.GinServerOptions{BaseURL: "/api/v1", ErrorHandler: s.bindError})
	return r
}

// opOf returns the spec operation of the matched route.
func (s *Server) opOf(c *gin.Context) *op { return s.ops[c.Request.Method+" "+c.FullPath()] }

// allowedMethods lists the methods the spec gives the request path (Gin's NoMethod handler does
// not expose the pattern it matched, only that one did, so this matches it again), for the Allow
// header of a 405.
func (s *Server) allowedMethods(path string) []string {
	var methods []string
	for key, o := range s.ops {
		if pattern, ok := strings.CutPrefix(key, o.Method+" "); ok && pathMatches(pattern, path) {
			methods = append(methods, o.Method)
		}
	}
	sort.Strings(methods)
	return methods
}

// pathMatches reports whether actual (a request path) matches a Gin route pattern such as
// "/api/v1/revisions/:id", segment by segment.
func pathMatches(pattern, actual string) bool {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	as := strings.Split(strings.Trim(actual, "/"), "/")
	if len(ps) != len(as) {
		return false
	}
	for i, seg := range ps {
		if strings.HasPrefix(seg, ":") {
			continue
		}
		if seg != as[i] {
			return false
		}
	}
	return true
}

// Close releases what the server holds.
func (s *Server) Close() error {
	s.closeRuns()
	s.closeCaptures()
	s.stopTrafficSampler()
	s.unsubEvents()
	<-s.eventsDone
	return s.idem.close()
}

// ---- the principal

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
	if err := s.tryRecord(c, action, obj, rev, detail); err != nil {
		s.log.Error("cannot write the audit log", "action", action, "error", err)
	}
}

// tryRecord is record with the Append error returned instead of logged, for a caller that must not
// go on when the audit log cannot be written (M5-16: a key export).
func (s *Server) tryRecord(c *gin.Context, action string, obj *audit.Object, rev int64, detail string) error {
	p := principalOf(c)
	a := actorOf(p)
	e := audit.Entry{Actor: audit.Actor{Type: string(a.Type), ID: a.Id}, Via: viaOf(p), Action: action, Object: obj, Revision: rev, Detail: detail}
	if a.Name != nil {
		e.Actor.Name = *a.Name
	}
	_, err := s.cfg.Audit.Append(e)
	return err
}

func contextOf(c *gin.Context) context.Context { return c.Request.Context() }

// PreflightReport is the answer of GET /system/preflight.
type PreflightReport struct {
	At     *time.Time       `json:"at,omitempty"`
	Checks []PreflightCheck `json:"checks"`
	Status string           `json:"status"`
}

// PreflightCheck is one line of it. Interface names the host interface a check is about.
type PreflightCheck struct {
	Fix       string `json:"fix,omitempty"`
	ID        string `json:"id"`
	Interface string `json:"interface,omitempty"`
	Message   string `json:"message"`
	Status    string `json:"status"`
}
