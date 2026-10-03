package api

import (
	"errors"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/model"
)

type sessionBody struct {
	CSRFToken string     `json:"csrf_token,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Kind      string     `json:"kind"`
	Scope     string     `json:"scope"`
	Token     *tokenRef  `json:"token,omitempty"`
	User      string     `json:"user,omitempty"`
}

type tokenRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func sessionOf(p *Principal) sessionBody {
	if p.Kind == "token" {
		return sessionBody{Kind: "token", Scope: string(p.Scope), Token: &tokenRef{ID: p.TokenID, Name: p.TokenName}}
	}
	e := p.ExpiresAt.UTC()
	return sessionBody{Kind: "session", Scope: string(auth.ScopeFull), User: "admin", CSRFToken: p.CSRF, ExpiresAt: &e}
}

// Login implements POST /auth/login.
func (s *Server) Login(c *gin.Context) {
	var body struct {
		Password string `json:"password"`
		Username string `json:"username"`
	}
	if p := decodeJSON(c, &body); p != nil {
		s.write(c, p)
		return
	}
	if p := requireFields(map[string]bool{"password": body.Password != ""}); p != nil {
		s.write(c, p)
		return
	}
	if body.Username != "" && body.Username != "admin" {
		s.write(c, newProblem(model.ErrorCodeUnauthorized, "wrong user name or password"))
		return
	}
	sess, err := s.cfg.Auth.Login(body.Password, c.ClientIP())
	var rl *auth.RateLimited
	switch {
	case errors.As(err, &rl):
		s.audit(c, "auth.login_blocked", nil, "")
		s.write(c, newProblem(model.ErrorCodeRateLimited, "too many failed logins; try again in %d seconds", int(rl.RetryAfter.Seconds())+1).
			withHeader("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())+1)))
		return
	case errors.Is(err, auth.ErrBadCredentials):
		s.audit(c, "auth.login_failed", nil, "")
		s.write(c, newProblem(model.ErrorCodeUnauthorized, "wrong user name or password"))
		return
	case err != nil:
		s.fail(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: SessionCookie, Value: sess.ID, Path: "/", HttpOnly: true, Secure: c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode, Expires: sess.ExpiresAt})
	p := &Principal{Kind: "session", Scope: auth.ScopeFull, SessionID: sess.ID, CSRF: sess.CSRF, ExpiresAt: sess.ExpiresAt}
	c.Set("principal", p)
	s.record(c, "auth.login", nil, 0, "")
	c.JSON(200, sessionOf(p))
}

// audit records an event of an unauthenticated request (a failed login) with the client address.
func (s *Server) audit(c *gin.Context, action string, obj *audit.Object, detail string) {
	if detail == "" {
		detail = "from " + c.ClientIP()
	}
	if _, err := s.cfg.Audit.Append(audit.Entry{Actor: audit.Actor{Type: "user", ID: "admin"}, Via: "ui", Action: action, Object: obj, Detail: detail}); err != nil {
		s.log.Error("cannot write the audit log", "action", action, "error", err)
	}
}

// Logout implements POST /auth/logout.
func (s *Server) Logout(c *gin.Context) {
	if p := principalOf(c); p != nil && p.Kind == "session" {
		s.cfg.Auth.Logout(p.SessionID)
		s.record(c, "auth.logout", nil, 0, "")
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteStrictMode})
	c.Status(http.StatusNoContent)
}

// GetSession implements GET /auth/session.
func (s *Server) GetSession(c *gin.Context) {
	c.JSON(200, sessionOf(principalOf(c)))
}

// ChangePassword implements POST /auth/password.
func (s *Server) ChangePassword(c *gin.Context) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if p := decodeJSON(c, &body); p != nil {
		s.write(c, p)
		return
	}
	if p := requireFields(map[string]bool{"current": body.Current != "", "new": body.New != ""}); p != nil {
		s.write(c, p)
		return
	}
	if len(body.New) < minPassword {
		errs := []model.ValidationError{{Path: "/new", Code: "min_length", Message: "the new password needs at least 12 characters"}}
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the new password is too short").with(func(b *model.Problem) { b.Errors = &errs }))
		return
	}
	keep := ""
	if p := principalOf(c); p != nil && p.Kind == "session" {
		keep = p.SessionID
	}
	if err := s.cfg.Auth.ChangePassword(body.Current, body.New, keep); err != nil {
		if errors.Is(err, auth.ErrBadCredentials) {
			errs := []model.ValidationError{{Path: "/current", Code: "wrong_password", Message: "the current password is wrong"}}
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "the current password is wrong").with(func(b *model.Problem) { b.Errors = &errs }))
			return
		}
		s.fail(c, err)
		return
	}
	s.record(c, "auth.password_change", nil, 0, "")
	c.Status(http.StatusNoContent)
}

func tokenBody(t auth.Token) gin.H {
	b := gin.H{"id": t.ID, "name": t.Name, "scope": string(t.Scope), "created_at": t.CreatedAt}
	if t.ExpiresAt != nil {
		b["expires_at"] = t.ExpiresAt
	}
	if t.LastUsedAt != nil {
		b["last_used_at"] = t.LastUsedAt
	}
	return b
}

// ListTokens implements GET /auth/tokens.
func (s *Server) ListTokens(c *gin.Context, params model.ListTokensParams) {
	items, next, err := page(s.cfg.Auth.ListTokens(), func(t auth.Token) string { return t.CreatedAt.UTC().Format(time.RFC3339Nano) + t.ID }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, t := range items {
		out = append(out, tokenBody(t))
	}
	body := gin.H{"items": out}
	if next != nil {
		body["next_cursor"] = *next
	}
	c.JSON(200, body)
}

// CreateToken implements POST /auth/tokens.
func (s *Server) CreateToken(c *gin.Context) {
	var body struct {
		ExpiresIn string `json:"expires_in"`
		Name      string `json:"name"`
		Scope     string `json:"scope"`
	}
	if p := decodeJSON(c, &body); p != nil {
		s.write(c, p)
		return
	}
	if p := requireFields(map[string]bool{"name": body.Name != "", "scope": body.Scope != ""}); p != nil {
		s.write(c, p)
		return
	}
	var errs []model.ValidationError
	if len(body.Name) > 64 {
		errs = append(errs, model.ValidationError{Path: "/name", Code: "max_length", Message: "at most 64 characters"})
	}
	if !auth.Scope(body.Scope).Valid() {
		errs = append(errs, model.ValidationError{Path: "/scope", Code: "enum", Message: "one of read, overlays, full"})
	}
	var ttl time.Duration
	if body.ExpiresIn != "" {
		d, err := time.ParseDuration(body.ExpiresIn)
		if err != nil || d <= 0 {
			errs = append(errs, model.ValidationError{Path: "/expires_in", Code: "pattern", Message: "a positive Go duration such as 720h"})
		}
		ttl = d
	}
	if len(errs) > 0 {
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the request body is not valid").with(func(b *model.Problem) { b.Errors = &errs }))
		return
	}
	t, value, err := s.cfg.Auth.CreateToken(body.Name, auth.Scope(body.Scope), ttl)
	if err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "token.create", &audit.Object{Kind: "token", ID: t.ID, Name: t.Name}, 0, "scope "+body.Scope)
	out := tokenBody(t)
	out["token"] = value
	c.JSON(201, out)
}

// DeleteToken implements DELETE /auth/tokens/{tokenId}.
func (s *Server) DeleteToken(c *gin.Context, tokenId openapi_types.UUID) {
	id := tokenId.String()
	name := ""
	for _, t := range s.cfg.Auth.ListTokens() {
		if t.ID == id {
			name = t.Name
		}
	}
	if err := s.cfg.Auth.DeleteToken(id); err != nil {
		if errors.Is(err, auth.ErrNoSuchToken) {
			s.write(c, notFound("token", id))
			return
		}
		s.fail(c, err)
		return
	}
	s.record(c, "token.delete", &audit.Object{Kind: "token", ID: id, Name: name}, 0, "")
	c.Status(http.StatusNoContent)
}
