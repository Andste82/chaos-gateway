package api

import (
	"bytes"
	"crypto/subtle"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// SessionCookie is the name of the session cookie over plain HTTP (tests: `__Host-` requires
// Secure, which the browser refuses without TLS). HostSessionCookie is the `__Host-` name used
// over TLS (M5-18): Secure, Path=/, no Domain, so the browser itself refuses it from anywhere but
// this exact origin.
const (
	SessionCookie     = "chaosgw_session"
	HostSessionCookie = "__Host-" + SessionCookie
)

// sessionCookieName is the name a Set-Cookie for this request's session uses: the `__Host-` name
// on TLS, the plain one otherwise.
func sessionCookieName(r *http.Request) string {
	if r.TLS != nil {
		return HostSessionCookie
	}
	return SessionCookie
}

// sessionCookie reads the session cookie under either name (M5-18): a client may hold one set
// under the other name, e.g. across a change of how the API is reached.
func sessionCookie(r *http.Request) (*http.Cookie, error) {
	if ck, err := r.Cookie(HostSessionCookie); err == nil {
		return ck, nil
	}
	return r.Cookie(SessionCookie)
}

// Operations that work before the setup is finished.
var beforeSetup = map[string]bool{"getSetup": true, "completeSetup": true, "getHealth": true}

const (
	maxBody       = 1 << 20  // 1 MiB for ordinary requests
	maxConfigBody = 16 << 20 // a configuration or an import
)

func (s *Server) recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic in a handler", "path", c.Request.URL.Path, "panic", v, "stack", string(debug.Stack()))
				s.write(c, newProblem(model.ErrorCodeInternal, "an unexpected error occurred"))
			}
		}()
		c.Next()
	}
}

func (s *Server) headers() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

// guard authenticates the request, checks the scope of the operation and the CSRF token of a
// session, and holds back everything but the setup while the setup is not finished.
func (s *Server) guard() gin.HandlerFunc {
	return func(c *gin.Context) {
		o := s.opOf(c)
		if o == nil {
			return // no such route: the NoRoute handler answers
		}
		limit := int64(maxBody)
		if o.ID == "createRevision" || o.ID == "completeSetup" {
			limit = maxConfigBody
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		done := s.cfg.Auth.SetupCompleted()
		if !done && !beforeSetup[o.ID] {
			s.write(c, newProblem(model.ErrorCodeUnavailable, "the first-start setup is not finished: open the setup page and use the setup token from the container log"))
			return
		}
		if o.SetupToken {
			if done {
				s.write(c, newProblem(model.ErrorCodeSetupCompleted, "the setup is completed"))
				return
			}
			if !s.cfg.Auth.CheckSetupToken(c.GetHeader("X-Setup-Token")) {
				s.write(c, newProblem(model.ErrorCodeUnauthorized, "a valid setup token is required (X-Setup-Token)").withHeader("WWW-Authenticate", "X-Setup-Token"))
				return
			}
			c.Set("principal", &Principal{Kind: "setup", Scope: auth.ScopeFull})
			return
		}
		p, perr := s.authenticate(c)
		if perr != nil {
			s.write(c, perr)
			return
		}
		if p != nil {
			c.Set("principal", p)
			if p.Kind == "session" && isUnsafe(c.Request.Method) && !o.Public {
				got := c.GetHeader("X-CSRF-Token")
				if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(p.CSRF)) != 1 {
					s.write(c, newProblem(model.ErrorCodeCsrfFailed, "a request with a session needs the X-CSRF-Token header from GET /auth/session"))
					return
				}
			}
		}
		if o.Public {
			return
		}
		if p == nil {
			s.write(c, newProblem(model.ErrorCodeUnauthorized, "log in or send an API token (Authorization: Bearer)").withHeader("WWW-Authenticate", "Bearer"))
			return
		}
		if !p.Scope.Allows(o.Scope) {
			s.write(c, newProblem(model.ErrorCodeForbidden, "this operation needs the scope %q", o.Scope))
			return
		}
		if preconditionOps[o.ID] && c.GetHeader("If-Match") == "" {
			s.write(c, newProblem(model.ErrorCodePreconditionRequired, "send the id of the base revision in If-Match, e.g. If-Match: \"42\""))
			return
		}
	}
}

func isUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// authenticate finds the principal of a request: a bearer token or the session cookie. A
// credential that is sent but wrong is an error, not "anonymous".
func (s *Server) authenticate(c *gin.Context) (*Principal, *problem) {
	if h := c.GetHeader("Authorization"); h != "" {
		scheme, value, _ := strings.Cut(h, " ")
		if !strings.EqualFold(scheme, "Bearer") || value == "" {
			return nil, newProblem(model.ErrorCodeUnauthorized, "the Authorization header must be `Bearer <token>`").withHeader("WWW-Authenticate", "Bearer")
		}
		t, ok := s.cfg.Auth.AuthenticateToken(strings.TrimSpace(value))
		if !ok {
			return nil, newProblem(model.ErrorCodeUnauthorized, "the token is invalid, expired or revoked").withHeader("WWW-Authenticate", "Bearer")
		}
		return &Principal{Kind: "token", Scope: t.Scope, TokenID: t.ID, TokenName: t.Name}, nil
	}
	if ck, err := sessionCookie(c.Request); err == nil && ck.Value != "" {
		sess, ok := s.cfg.Auth.LookupSession(ck.Value)
		if !ok {
			return nil, nil // an old cookie is no credential; the operation decides whether it needs one
		}
		return &Principal{Kind: "session", Scope: auth.ScopeFull, SessionID: sess.ID, CSRF: sess.CSRF, ExpiresAt: sess.ExpiresAt}, nil
	}
	return nil, nil
}

// idempotent replays the response of a request that was made before with the same
// Idempotency-Key, and refuses the key with another request (plan §2.15, 24 hours).
func (s *Server) idempotent() gin.HandlerFunc {
	return func(c *gin.Context) {
		o := s.opOf(c)
		key := c.GetHeader("Idempotency-Key")
		if o == nil || key == "" || c.IsAborted() || !idempotentOps[o.ID] {
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			s.write(c, newProblem(model.ErrorCodeBadRequest, "cannot read the request body: %v", err))
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		p := principalOf(c)
		who := ""
		if p != nil {
			who = p.Kind + ":" + p.TokenID
			if p.Kind == "session" {
				who = "admin"
			}
		}
		fp := fingerprint(c.Request.Method, c.Request.URL.RequestURI(), who, c.GetHeader("If-Match"), c.ContentType(), body)
		release, replay, conflict, cerr := s.idem.begin(c.Request.Context(), who+"|"+key, fp)
		switch {
		case cerr != nil:
			s.write(c, newProblem(model.ErrorCodeUnavailable, "the request was cancelled while waiting for an earlier one with the same Idempotency-Key"))
			return
		case conflict:
			s.write(c, newProblem(model.ErrorCodeIdempotencyConflict, "the Idempotency-Key was used with a different request"))
			return
		case replay != nil:
			replay.write(c)
			c.Abort()
			return
		}
		rec := &recorder{ResponseWriter: c.Writer}
		c.Writer = rec
		var res *stored
		defer func() { release(res, fp) }() // also after a panic: the key must not stay claimed
		c.Next()
		res = rec.result(c)
	}
}

// idempotentOps are the operations that take an Idempotency-Key in this build.
var idempotentOps = map[string]bool{"createRevision": true, "createOverlay": true}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
