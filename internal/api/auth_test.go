package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestLoginSessionCSRFAndLogout(t *testing.T) {
	g := newGW(t)
	g.finishSetup()
	g.token = "" // from here: the browser's way, with a cookie
	if r := g.do("GET", "/state", nil, nil, nil); r.Status != 401 || r.code(t) != "unauthorized" || r.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("no credentials: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/auth/login", map[string]any{"password": "wrong password!!"}, nil, nil); r.Status != 401 {
		t.Errorf("a wrong password: %d", r.Status)
	}
	r := g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	sess := r.json(t)
	csrf, _ := sess["csrf_token"].(string)
	if sess["kind"] != "session" || sess["scope"] != "full" || csrf == "" {
		t.Fatalf("%v", sess)
	}
	// M5-18: over plain HTTP (this test server) the cookie keeps its plain name, not Secure;
	// the __Host- prefixed name is used only over TLS (api.HostSessionCookie, unit-tested).
	cookie := r.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "chaosgw_session=") || strings.Contains(cookie, "__Host-") ||
		strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Errorf("cookie %q", cookie)
	}
	// reads work with the cookie alone, unsafe methods need the CSRF token
	if r := g.do("GET", "/state", nil, nil, nil); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/auth/session", nil, nil, nil); r.json(t)["csrf_token"] != csrf {
		t.Errorf("%s", r.Body)
	}
	body := map[string]any{"name": "ci", "scope": "read"}
	if r := g.do("POST", "/auth/tokens", body, nil, nil); r.Status != 403 || r.code(t) != "csrf_failed" {
		t.Errorf("no CSRF token: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/auth/tokens", body, map[string]string{"X-CSRF-Token": "wrong"}, nil); r.Status != 403 || r.code(t) != "csrf_failed" {
		t.Errorf("a wrong CSRF token: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/auth/tokens", body, map[string]string{"X-CSRF-Token": csrf}, nil); r.Status != 201 {
		t.Errorf("with the CSRF token: %d %s", r.Status, r.Body)
	}
	// the actions of a session are the admin user's, not an API token's
	e, _, _ := g.log.List(auditFilter(), "", 1)
	if len(e) == 0 || e[0].Via != "ui" || e[0].Actor.ID != "admin" {
		t.Errorf("%+v", e)
	}
	if r := g.do("POST", "/auth/logout", nil, map[string]string{"X-CSRF-Token": csrf}, nil); r.Status != 204 {
		t.Errorf("logout: %d", r.Status)
	}
	if r := g.do("GET", "/state", nil, nil, nil); r.Status != 401 {
		t.Errorf("the session is still valid after the logout: %d", r.Status)
	}
}

func TestTheLoginIsRateLimited(t *testing.T) {
	g := newGW(t)
	g.finishSetup()
	g.token = ""
	var last resp
	for i := 0; i < 6; i++ {
		last = g.do("POST", "/auth/login", map[string]any{"password": "wrong password!!"}, nil, nil)
	}
	if last.Status != 429 || last.code(t) != "rate_limited" || last.Header.Get("Retry-After") == "" {
		t.Fatalf("%d %s %v", last.Status, last.Body, last.Header)
	}
	// even the right password has to wait
	if r := g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil); r.Status != 429 {
		t.Errorf("%d", r.Status)
	}
}

func TestTokenScopesAndTheirLifecycle(t *testing.T) {
	g := ready(t)
	// create tokens of each scope; the value appears once
	mk := func(scope string) (id, value string) {
		r := g.do("POST", "/auth/tokens", map[string]any{"name": scope + "-token", "scope": scope}, nil, nil)
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		j := r.json(t)
		return j["id"].(string), j["token"].(string)
	}
	readID, readTok := mk("read")
	_, ovTok := mk("overlays")
	_, fullTok := mk("full")
	if !strings.HasPrefix(readTok, "cgw_") {
		t.Errorf("token %q", readTok)
	}
	list := g.do("GET", "/auth/tokens", nil, nil, nil)
	if strings.Contains(string(list.Body), readTok) || strings.Contains(string(list.Body), `"token"`) {
		t.Fatalf("the list shows a token value: %s", list.Body)
	}

	cfgBody := map[string]any{"management": map[string]any{"ui_port": 9443}}
	as := func(tok string, f func() resp) resp { g.token = tok; return f() }
	patch := func() resp {
		return g.do("POST", "/revisions", cfgBody, map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": `"1"`}, nil)
	}
	// read: GET yes, writes no
	if r := as(readTok, func() resp { return g.do("GET", "/networks", nil, nil, nil) }); r.Status != 200 {
		t.Errorf("read GET: %d", r.Status)
	}
	if r := as(readTok, patch); r.Status != 403 || r.code(t) != "forbidden" {
		t.Errorf("read POST /revisions: %d %s", r.Status, r.Body)
	}
	if r := as(ovTok, patch); r.Status != 403 {
		t.Errorf("overlays POST /revisions: %d", r.Status)
	}
	if r := as(readTok, func() resp { return g.do("GET", "/auth/tokens", nil, nil, nil) }); r.Status != 403 {
		t.Errorf("read GET /auth/tokens: %d", r.Status)
	}
	// secret downloads need full
	if r := as(readTok, func() resp { return g.do("GET", "/networks/lab-hub/clients/rA/export", nil, nil, nil) }); r.Status != 403 {
		t.Errorf("read export: %d", r.Status)
	}
	if r := as(fullTok, patch); r.Status != 201 {
		t.Errorf("full POST /revisions: %d %s", r.Status, r.Body)
	}
	// a wrong token is not anonymous
	if r := as("cgw_wrong", func() resp { return g.do("GET", "/state", nil, nil, nil) }); r.Status != 401 {
		t.Errorf("wrong token: %d", r.Status)
	}
	if r := as("", func() resp { return g.do("GET", "/state", nil, map[string]string{"Authorization": "Basic abc"}, nil) }); r.Status != 401 {
		t.Errorf("a wrong scheme: %d", r.Status)
	}
	// revoked
	g.token = fullTok
	if r := g.do("DELETE", "/auth/tokens/"+readID, nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d", r.Status)
	}
	if r := g.do("DELETE", "/auth/tokens/"+readID, nil, nil, nil); r.Status != 404 {
		t.Errorf("deleting twice: %d", r.Status)
	}
	if r := as(readTok, func() resp { return g.do("GET", "/state", nil, nil, nil) }); r.Status != 401 {
		t.Errorf("a revoked token works: %d", r.Status)
	}
	// who am I
	g.token = ovTok
	if s := g.do("GET", "/auth/session", nil, nil, nil).json(t); s["kind"] != "token" || s["scope"] != "overlays" || s["token"] == nil {
		t.Errorf("%v", s)
	}
}

func TestTokenValidation(t *testing.T) {
	g := ready(t)
	for name, body := range map[string]any{
		"no name":       map[string]any{"scope": "read"},
		"bad scope":     map[string]any{"name": "x", "scope": "root"},
		"service scope": map[string]any{"name": "x", "scope": "service"},
		"long name":     map[string]any{"name": strings.Repeat("x", 65), "scope": "read"},
		"bad duration":  map[string]any{"name": "x", "scope": "read", "expires_in": "soon"},
		"unknown field": map[string]any{"name": "x", "scope": "read", "admin": true},
	} {
		if r := g.do("POST", "/auth/tokens", body, nil, nil); r.Status != 422 || r.code(t) != "validation_failed" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := g.do("POST", "/auth/tokens", "{not json", nil, nil); r.Status != 400 || r.code(t) != "bad_request" {
		t.Errorf("malformed: %d %s", r.Status, r.Body)
	}
	// an expiring token
	r := g.do("POST", "/auth/tokens", map[string]any{"name": "short", "scope": "read", "expires_in": "1h"}, nil, nil)
	if r.Status != 201 || r.json(t)["expires_at"] == nil {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

func TestChangingThePassword(t *testing.T) {
	g := newGW(t)
	g.finishSetup()
	g.token = ""
	login := func(pw string) resp {
		return g.do("POST", "/auth/login", map[string]any{"password": pw}, nil, nil)
	}
	r := login(adminPassword)
	csrf := r.json(t)["csrf_token"].(string)
	h := map[string]string{"X-CSRF-Token": csrf}
	if r := g.do("POST", "/auth/password", map[string]any{"current": "wrong password!!", "new": "another long password"}, h, nil); r.Status != 422 {
		t.Errorf("wrong current: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/auth/password", map[string]any{"current": adminPassword, "new": "short"}, h, nil); r.Status != 422 {
		t.Errorf("too short: %d", r.Status)
	}
	if r := g.do("POST", "/auth/password", map[string]any{"current": adminPassword, "new": "another long password"}, h, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// the session of the caller goes on; the old password is gone
	if r := g.do("GET", "/state", nil, nil, nil); r.Status != 200 {
		t.Errorf("the own session ended: %d", r.Status)
	}
	g2 := &http.Client{}
	_ = g2
	g.client.Jar = nil
	if r := login(adminPassword); r.Status != 401 {
		t.Errorf("the old password works: %d", r.Status)
	}
	if r := login("another long password"); r.Status != 200 {
		t.Errorf("the new password: %d", r.Status)
	}
}

func TestAResetFromTheCommandLineEndsSessions(t *testing.T) {
	g := newGW(t)
	g.finishSetup()
	g.token = ""
	if r := g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if r := g.do("GET", "/state", nil, nil, nil); r.Status != 200 {
		t.Fatal(r.Status)
	}
	resetPasswordFromOutside(t, g, "reset by the operator!")
	if r := g.do("GET", "/state", nil, nil, nil); r.Status != 401 {
		t.Errorf("the session survived the reset: %d", r.Status)
	}
	g.client.Jar = nil
	if r := g.do("POST", "/auth/login", map[string]any{"password": "reset by the operator!"}, nil, nil); r.Status != 200 {
		t.Errorf("the reset password: %d", r.Status)
	}
}
