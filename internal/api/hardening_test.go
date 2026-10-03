package api_test

import (
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
)

func TestAStreamEndsWhenItsTokenIsRevoked(t *testing.T) {
	defer api.SetStreamTimings(5*time.Second, 100*time.Millisecond)()
	g := ready(t)
	r := g.do("POST", "/auth/tokens", map[string]any{"name": "watcher", "scope": "read"}, nil, nil).json(t)
	admin := g.token
	g.token = r["token"].(string)
	s := g.openStream("", "")
	if s.status != 200 {
		t.Fatalf("%d", s.status)
	}
	g.token = admin
	if r := g.do("DELETE", "/auth/tokens/"+r["id"].(string), nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d", r.Status)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-s.lines:
			if !ok {
				return // the server ended the stream
			}
		case <-deadline:
			t.Fatal("the stream of a revoked token goes on")
		}
	}
}

func TestAParallelBurstOfWrongPasswordsIsLimited(t *testing.T) {
	g := newGW(t)
	g.finishSetup()
	g.token = ""
	var wg sync.WaitGroup
	codes := make([]int, 30)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = g.do("POST", "/auth/login", map[string]any{"password": "wrong password!!"}, nil, nil).Status
		}()
	}
	wg.Wait()
	wrong, limited := 0, 0
	for _, c := range codes {
		switch c {
		case 401:
			wrong++
		case 429:
			limited++
		default:
			t.Errorf("status %d", c)
		}
	}
	if wrong > 5 || limited < 25 {
		t.Errorf("%d guesses were checked, %d refused: a burst gets past the limit", wrong, limited)
	}
}

func TestBlockedLoginsDoNotFloodTheAuditLog(t *testing.T) {
	g := newGW(t)
	g.finishSetup()
	g.token = ""
	before, _, _ := g.log.List(auditFilter(), "", 1000)
	for i := 0; i < 50; i++ {
		g.do("POST", "/auth/login", map[string]any{"password": "wrong password!!"}, nil, nil)
	}
	after, _, _ := g.log.List(auditFilter(), "", 1000)
	if n := len(after) - len(before); n > 6 {
		t.Errorf("%d audit entries for 50 failed logins", n)
	}
}

func TestPasswordGuessingThroughTheChangeIsLimitedToo(t *testing.T) {
	g := ready(t)
	var last resp
	for i := 0; i < 6; i++ {
		last = g.do("POST", "/auth/password", map[string]any{"current": "wrong password!!", "new": "another long password"}, nil, nil)
	}
	if last.Status != 429 || last.Header.Get("Retry-After") == "" {
		t.Errorf("%d %s", last.Status, last.Body)
	}
}

func TestADuplicateNameIs409(t *testing.T) {
	g := ready(t)
	r := g.patch(map[string]any{"networks": map[string]any{"11111111-2222-4333-8444-555555555555": map[string]any{
		"type": "lan", "name": "iot", "address": "10.77.0.1/24", "interfaces": []any{map[string]any{"name": "lan1"}},
	}}})
	if r.Status != 409 || r.code(t) != "name_taken" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

func TestARejectedImportLeavesNoKeysAndNeverReplacesAStoredOne(t *testing.T) {
	g := ready(t)
	exp := g.do("GET", "/revisions/1/export?include_secrets=true&format=json", nil, nil, nil).json(t)
	before, _ := g.sec.IDs()
	// a different key for the hub's interface is refused and replaces nothing
	sec := exp["secrets"].(map[string]any)["wireguard"].(map[string]any)
	hubKeys := sec[hubID].(map[string]any)
	orig := hubKeys["private_key"]
	hubKeys["private_key"] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if r := g.do("POST", "/revisions", exp, ifMatch(1), nil); r.Status != 422 {
		t.Errorf("a different key: %d %s", r.Status, r.Body)
	}
	k, _ := g.sec.WireGuard(hubID)
	if k.PrivateKey != orig {
		t.Error("the stored key of the active revision was replaced")
	}
	// an import that does not validate (and carries a new key) leaves nothing behind
	hubKeys["private_key"] = orig
	sec["6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f"] = map[string]any{"private_key": orig}
	exp["uplink"].(map[string]any)["gateway"] = "nonsense"
	if r := g.do("POST", "/revisions", exp, ifMatch(1), nil); r.Status != 422 {
		t.Errorf("%d", r.Status)
	}
	if after, _ := g.sec.IDs(); len(after) != len(before) {
		t.Errorf("keys before %d, after %d", len(before), len(after))
	}
}

func TestTheSetupAcceptsASecretsBlock(t *testing.T) {
	src := ready(t)
	exp := src.do("GET", "/revisions/1/export?include_secrets=true&format=json", nil, nil, nil).json(t)
	g := newGW(t)
	r := g.do("POST", "/setup", map[string]any{"admin_password": adminPassword, "configuration": exp}, map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// the restored gateway has the keys of the exported one
	a, _ := src.sec.WireGuard(hubID)
	b, _ := g.sec.WireGuard(hubID)
	if a.PrivateKey == "" || a.PrivateKey != b.PrivateKey {
		t.Error("the keys were not restored")
	}
}
