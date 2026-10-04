package api_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBeforeTheSetupOnlyTheSetupAndTheHealthAnswer(t *testing.T) {
	g := newGW(t)
	if r := g.do("GET", "/setup", nil, nil, nil); r.Status != 200 || r.json(t)["completed"] != false {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/system/health", nil, nil, nil); r.Status != 200 {
		t.Errorf("the health check works before the setup: %d", r.Status)
	}
	for _, p := range []string{"/state", "/networks", "/revisions", "/capabilities"} {
		if r := g.do("GET", p, nil, nil, nil); r.Status != 503 || r.code(t) != "unavailable" {
			t.Errorf("%s: %d %s", p, r.Status, r.Body)
		}
	}
	if r := g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil); r.Status != 503 {
		t.Errorf("login before the setup: %d", r.Status)
	}
}

func TestTheSetupNeedsTheTokenAndCreatesRevisionOne(t *testing.T) {
	g := newGW(t)
	body := fixtureJSON(t)
	if r := g.do("POST", "/setup", body, nil, nil); r.Status != 401 || r.code(t) != "unauthorized" {
		t.Errorf("no token: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/setup", body, map[string]string{"X-Setup-Token": "setup_wrong"}, nil); r.Status != 401 {
		t.Errorf("a wrong token: %d", r.Status)
	}
	g.badRequest = true // every body in this block is deliberately invalid
	short := map[string]any{"admin_password": "short", "configuration": fixture(t)}
	r := g.do("POST", "/setup", short, map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 422 || r.code(t) != "validation_failed" {
		t.Errorf("a short password: %d %s", r.Status, r.Body)
	}
	if g.activeID() != 0 {
		t.Fatal("a failed setup left a revision")
	}
	bad := fixture(t)
	delete(bad, "uplink")
	if r := g.do("POST", "/setup", map[string]any{"admin_password": adminPassword, "configuration": bad}, map[string]string{"X-Setup-Token": g.setup}, nil); r.Status != 422 {
		t.Errorf("an invalid configuration: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/setup", map[string]any{"admin_password": adminPassword, "configuration": fixture(t), "extra": 1}, map[string]string{"X-Setup-Token": g.setup}, nil); r.Status != 422 {
		t.Errorf("an unknown field: %d %s", r.Status, r.Body)
	}
	g.badRequest = false
	if g.au.SetupCompleted() {
		t.Fatal("the setup is done after failures")
	}

	r = g.do("POST", "/setup", body, map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	res := r.json(t)
	// M5-03: the setup's own revision needs confirming like any other lockout-relevant one, since
	// LockoutRelevant alone never flags a first revision (there is nothing to compare it against).
	if res["revision"] != float64(1) || res["status"] != "pending_confirm" || res["confirm_deadline"] == nil || r.Header.Get("Chaos-Generation") == "" {
		t.Errorf("%v %v", res, r.Header)
	}
	if g.activeID() != 0 || !g.au.SetupCompleted() {
		t.Error("the admin password is not usable yet, or active before confirmation")
	}
	g.mintToken("full")
	if cf := g.do("POST", "/revisions/1/confirm", nil, nil, nil); cf.Status != 200 {
		t.Fatalf("confirming the setup: %d %s", cf.Status, cf.Body)
	}
	if g.activeID() != 1 {
		t.Error("the setup did not finish once confirmed")
	}
	// the token is void and the setup cannot be run again
	if r := g.do("POST", "/setup", body, map[string]string{"X-Setup-Token": g.setup}, nil); r.Status != 409 || r.code(t) != "setup_completed" {
		t.Errorf("a second setup: %d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/setup", nil, nil, nil); r.json(t)["completed"] != true {
		t.Errorf("%s", r.Body)
	}
	// the admin can log in with the password of the setup
	if r := g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil); r.Status != 200 {
		t.Errorf("login: %d %s", r.Status, r.Body)
	}
}

func TestTheSetupDescribesTheHost(t *testing.T) {
	g := newGW(t)
	r := g.do("GET", "/setup", nil, nil, nil).json(t)
	ifs, _ := r["interfaces"].([]any)
	names := map[string]bool{}
	for _, i := range ifs {
		names[i.(map[string]any)["name"].(string)] = true
	}
	for _, n := range []string{"lan0", "lan1", "wan0", "mgmt0"} {
		if !names[n] {
			t.Errorf("interface %s is missing: %v", n, names)
		}
	}
	// the interface with the default route of the main table is the suggestion for the uplink
	if su, _ := r["suggested_uplink"].(map[string]any); su["name"] != "mgmt0" {
		t.Errorf("suggested uplink %v", r["suggested_uplink"])
	}
}

func TestAFailedApplyLeavesTheSetupOpen(t *testing.T) {
	g := newGW(t)
	// the configuration names an interface the host does not have: the compile reports it
	cfg := fixture(t)
	cfg["uplink"] = map[string]any{"interface": map[string]any{"name": "nonexistent0"}, "gateway": "203.0.113.10"}
	r := g.do("POST", "/setup", map[string]any{"admin_password": adminPassword, "configuration": cfg}, map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status == 200 {
		t.Fatalf("applied: %s", r.Body)
	}
	if g.au.SetupCompleted() {
		t.Error("the setup was completed by a failed apply")
	}
	if r := g.do("GET", "/state", nil, nil, nil); r.Status != http.StatusServiceUnavailable {
		t.Errorf("%d", r.Status)
	}
	_ = strings.TrimSpace
}

func TestTwoSetupRequestsRunTheSetupOnce(t *testing.T) {
	g := newGW(t)
	var wg sync.WaitGroup
	res := make([]resp, 2)
	for i := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i] = g.do("POST", "/setup", fixtureJSON(t), map[string]string{"X-Setup-Token": g.setup}, nil)
		}()
	}
	wg.Wait()
	ok, done := 0, 0
	for _, r := range res {
		switch r.Status {
		case 200:
			ok++
		case 409:
			done++
		default:
			t.Errorf("%d %s", r.Status, r.Body)
		}
	}
	// the one setup's revision exists (and is pending confirmation, M5-03) but is not active yet
	if ok != 1 || done != 1 || g.activeID() != 0 {
		t.Errorf("%d setups, %d refused, active revision %d", ok, done, g.activeID())
	}
	if n := len(g.mintAndList()); n != 1 {
		t.Errorf("%d revisions", n)
	}
}

func (g *gw) mintAndList() []any {
	g.mintToken("read")
	return g.do("GET", "/revisions", nil, nil, nil).json(g.t)["items"].([]any)
}

// M5-03 test: the first-start setup's own revision waits for confirmation like any other
// lockout-relevant one, even though LockoutRelevant itself never flags a first revision (nothing
// to compare it against) — a wrong management interface here would otherwise lock the admin out
// with no way back.
func TestTheSetupWaitsForConfirmation(t *testing.T) {
	g := newGW(t)
	body := fixtureJSON(t)
	r := g.do("POST", "/setup", body, map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	res := r.json(t)
	if res["status"] != "pending_confirm" || res["confirm_deadline"] == nil {
		t.Fatalf("%v", res)
	}
	// the admin password already works: without it nothing could ever confirm or retry
	login := g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil)
	if login.Status != 200 {
		t.Fatalf("login before confirmation: %d %s", login.Status, login.Body)
	}
	csrf := login.json(t)["csrf_token"].(string)
	// but nothing is active, and the setup is not done in the sense that matters for lockout
	if g.activeID() != 0 {
		t.Errorf("active before confirmation: %d", g.activeID())
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["pending_confirm"] == nil {
		t.Errorf("%v", st)
	}
	if r := g.do("POST", "/revisions/1/confirm", nil, map[string]string{"X-CSRF-Token": csrf}, nil); r.Status != 200 {
		t.Fatalf("confirm: %d %s", r.Status, r.Body)
	}
	if g.activeID() != 1 {
		t.Error("not active once confirmed")
	}
}

// M5-03 test: if the setup's own revision is never confirmed, it rolls back like any other
// lockout-relevant one, and the setup reopens with a fresh token instead of leaving an admin
// password that is paired with no active configuration.
func TestAnUnconfirmedSetupIsRolledBackAndReopened(t *testing.T) {
	g := newGW(t, func(o *options) { o.confirm = 500 * time.Millisecond })
	ch, cancel := g.e.Subscribe()
	defer cancel()
	r := g.do("POST", "/setup", fixtureJSON(t), map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 || r.json(t)["status"] != "pending_confirm" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	deadline := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-ch:
			done = ev.Type == "revision_rolled_back"
		case <-deadline:
			t.Fatal("no rollback")
		}
	}
	if _, err := g.e.Barrier(context.Background()); err != nil {
		t.Fatal(err)
	}
	// the server's own subscriber (internal/api/server.go) reopens the setup asynchronously, on a
	// separate subscription to the same event this test's loop above already consumed
	deadline = time.After(5 * time.Second)
	for g.au.SetupCompleted() {
		select {
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("the setup is still done after its only revision was rolled back")
		}
	}
	if g.activeID() != 0 {
		t.Errorf("active after a rollback: %d", g.activeID())
	}
	// logging in with the old password no longer works: it was paired with the rolled-back revision
	if r := g.do("POST", "/auth/login", map[string]any{"password": adminPassword}, nil, nil); r.Status != 503 {
		t.Errorf("login after reopening: %d", r.Status)
	}
	// the setup accepts a fresh attempt
	tok := g.mustSetupToken()
	if r := g.do("POST", "/setup", fixtureJSON(t), map[string]string{"X-Setup-Token": tok}, nil); r.Status != 200 {
		t.Errorf("a second attempt after reopening: %d %s", r.Status, r.Body)
	}
}
