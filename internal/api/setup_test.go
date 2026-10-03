package api_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
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
	if g.au.SetupCompleted() {
		t.Fatal("the setup is done after failures")
	}

	r = g.do("POST", "/setup", body, map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	res := r.json(t)
	if res["revision"] != float64(1) || res["status"] != "active" || r.Header.Get("Chaos-Generation") == "" {
		t.Errorf("%v %v", res, r.Header)
	}
	if g.activeID() != 1 || !g.au.SetupCompleted() {
		t.Error("the setup did not finish")
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
	if ok != 1 || done != 1 || g.activeID() != 1 {
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
