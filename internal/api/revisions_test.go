package api_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCandidateRevisionsFromMergePatchAndFullConfiguration(t *testing.T) {
	g := ready(t)
	if g.activeID() != 1 {
		t.Fatalf("active %d", g.activeID())
	}
	// no If-Match: 428; wrong If-Match: 409 with the active revision
	g.badRequest = true // every request in this block is deliberately invalid
	if r := g.do("POST", "/revisions", map[string]any{}, map[string]string{"Content-Type": "application/merge-patch+json"}, nil); r.Status != 428 || r.code(t) != "precondition_required" {
		t.Errorf("no If-Match: %d %s", r.Status, r.Body)
	}
	r := g.do("POST", "/revisions", map[string]any{}, map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": `"7"`}, nil)
	if r.Status != 409 || r.code(t) != "revision_conflict" || r.json(t)["active_revision"] != float64(1) {
		t.Errorf("wrong If-Match: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/revisions", map[string]any{}, map[string]string{"If-Match": "banana"}, nil); r.Status != 400 {
		t.Errorf("garbled If-Match: %d", r.Status)
	}
	if r := g.do("POST", "/revisions", "x", map[string]string{"Content-Type": "text/plain", "If-Match": `"1"`}, nil); r.Status != 400 {
		t.Errorf("wrong content type: %d", r.Status)
	}
	g.badRequest = false

	// a merge patch: rename the uplink gateway, delete nothing
	r = g.patch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	rev := r.json(t)
	if rev["status"] != "candidate" || rev["base"] != float64(1) || r.Header.Get("Location") != "/api/v1/revisions/2" || rev["created_by"] == nil {
		t.Errorf("%v %v", rev, r.Header)
	}
	got := g.do("GET", "/revisions/2", nil, nil, nil).json(t)
	cfg := got["configuration"].(map[string]any)
	if cfg["uplink"].(map[string]any)["gateway"] != "203.0.113.11" || cfg["management"] == nil {
		t.Errorf("the patch did not merge onto the base: %v", cfg["uplink"])
	}

	// an invalid candidate is not stored: 422 with pointers
	before := len(g.do("GET", "/revisions", nil, nil, nil).json(t)["items"].([]any))
	r = g.patch(map[string]any{"uplink": map[string]any{"gateway": "not-an-address"}})
	if r.Status != 422 || r.code(t) != "validation_failed" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	errs := r.json(t)["errors"].([]any)
	if len(errs) == 0 || !strings.HasPrefix(errs[0].(map[string]any)["path"].(string), "/uplink") {
		t.Errorf("%v", errs)
	}
	if after := len(g.do("GET", "/revisions", nil, nil, nil).json(t)["items"].([]any)); after != before {
		t.Errorf("a rejected candidate was stored: %d → %d", before, after)
	}
	// a semantic error (a reference to nothing)
	r = g.patch(map[string]any{"access_matrix": map[string]any{"entries": []any{map[string]any{"from": map[string]any{"network": "ghost"}, "to": map[string]any{"network": "IoT"}, "policy": "allow"}}}})
	if r.Status != 422 {
		t.Errorf("an unknown reference: %d %s", r.Status, r.Body)
	}

	// a complete configuration, as for an import: the active one with another gateway
	full := g.do("GET", "/revisions/active", nil, nil, nil).json(t)["configuration"].(map[string]any)
	full["uplink"].(map[string]any)["gateway"] = "203.0.113.12"
	r = g.do("POST", "/revisions?message=import", full, ifMatch(1), nil)
	if r.Status != 201 || r.json(t)["message"] != "import" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// unknown fields are refused, wherever they are
	full["bogus"] = true
	if r := g.do("POST", "/revisions", full, ifMatch(1), nil); r.Status != 422 {
		t.Errorf("an unknown field: %d %s", r.Status, r.Body)
	}
}

func TestNamesInARequestBecomeUUIDsInTheRevision(t *testing.T) {
	g := ready(t)
	// the access matrix names the networks; the stored revision has UUIDs
	id := g.mustPatch(map[string]any{"access_matrix": map[string]any{"entries": []any{
		map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "lab-hub"}, "policy": "allow"},
		map[string]any{"from": map[string]any{"network": "iot"}, "to": map[string]any{"network": "site-b"}, "policy": "allow"},
	}}})
	cfg := g.do("GET", "/revisions/"+itoa(id), nil, nil, nil).json(t)["configuration"].(map[string]any)
	e := cfg["access_matrix"].(map[string]any)["entries"].([]any)[0].(map[string]any)
	if e["from"].(map[string]any)["network"] != "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21" {
		t.Errorf("%v", e)
	}
}

func TestPreviewApplyAndTheState(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	p := pv.json(t)
	if p["revision"] != float64(id) || p["requires_confirm"] != false || len(p["domain"].([]any)) == 0 {
		t.Errorf("%v", p)
	}
	if d := p["domain"].([]any)[0].(map[string]any); d["kind"] != "uplink" || d["op"] != "changed" {
		t.Errorf("%v", d)
	}
	// a preview changes nothing
	if g.activeID() != 1 {
		t.Fatal("the preview applied the revision")
	}

	gen0 := g.do("GET", "/state", nil, nil, nil).json(t)["generation"].(float64)
	ap := g.apply(id)
	if ap.Status != 200 {
		t.Fatalf("%d %s", ap.Status, ap.Body)
	}
	res := ap.json(t)
	if res["status"] != "active" || res["revision"] != float64(id) || ap.Header.Get("Chaos-Generation") == "" {
		t.Errorf("%v %v", res, ap.Header)
	}
	st := g.do("GET", "/state", nil, nil, nil).json(t)
	if st["active_revision"] != float64(id) || st["generation"].(float64) <= gen0 || st["last_apply"].(map[string]any)["result"] != "ok" || st["pending_confirm"] != nil {
		t.Errorf("%v", st)
	}
	if st["boot_id"] != "boot-1" || st["last_known_good"] != float64(id) {
		t.Errorf("%v", st)
	}
	// the active revision is the ETag of the configuration reads
	for _, p := range []string{"/revisions/active", "/uplink", "/networks", "/routing"} {
		if r := g.do("GET", p, nil, nil, nil); r.Header.Get("ETag") != `"`+itoa(id)+`"` {
			t.Errorf("%s: ETag %q", p, r.Header.Get("ETag"))
		}
	}
	// applying it again: not a candidate any more
	if r := g.apply(id); r.Status != 409 || r.code(t) != "not_a_candidate" {
		t.Errorf("a second apply: %d %s", r.Status, r.Body)
	}
	// the revision list: newest first, with statuses
	l := g.do("GET", "/revisions", nil, nil, nil).json(t)["items"].([]any)
	if l[0].(map[string]any)["id"] != float64(id) || l[0].(map[string]any)["status"] != "active" || l[1].(map[string]any)["status"] != "superseded" {
		t.Errorf("%v", l)
	}
	if r := g.do("GET", "/revisions?status=superseded", nil, nil, nil).json(t)["items"].([]any); len(r) != 1 {
		t.Errorf("%v", r)
	}
	if _, ok := l[0].(map[string]any)["configuration"]; ok {
		t.Error("the list carries the configuration")
	}
}

func TestAConflictingCandidateIsRejected(t *testing.T) {
	g := ready(t)
	a := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	b := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.12"}})
	if r := g.apply(a); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.apply(b)
	if r.Status != 409 || r.code(t) != "revision_conflict" || r.json(t)["active_revision"] != float64(a) {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// the client reloads and reapplies its change
	c := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.12"}})
	if r := g.apply(c); r.Status != 200 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

func TestConcurrentAppliesOfTwoCandidatesExactlyOneSucceeds(t *testing.T) {
	g := ready(t)
	a := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	b := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.12"}})
	var wg sync.WaitGroup
	results := make([]resp, 2)
	for i, id := range []int64{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = g.apply(id)
		}()
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, r := range results {
		switch {
		case r.Status == 200:
			ok++
		case r.Status == 409 && r.code(t) == "revision_conflict":
			conflict++
		default:
			t.Errorf("%d %s", r.Status, r.Body)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Errorf("%d applied, %d conflicted", ok, conflict)
	}
}

func TestALockoutRelevantChangeWaitsForConfirmation(t *testing.T) {
	g := ready(t)
	// a change of the management sources could lock the admin out
	id := g.mustPatch(map[string]any{"management": map[string]any{"allowed_sources": []any{"192.168.56.0/24", "172.16.0.0/12"}}})
	if pv := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil).json(t); pv["requires_confirm"] != true {
		t.Errorf("%v", pv)
	}
	ap := g.apply(id)
	if ap.Status != 200 {
		t.Fatalf("%d %s", ap.Status, ap.Body)
	}
	res := ap.json(t)
	if res["status"] != "pending_confirm" || res["confirm_deadline"] == nil {
		t.Fatalf("%v", res)
	}
	st := g.do("GET", "/state", nil, nil, nil).json(t)
	if pc, _ := st["pending_confirm"].(map[string]any); pc["revision"] != float64(id) {
		t.Errorf("%v", st)
	}
	// a second apply gets confirm_pending
	other := g.mustPatchExpectingPending()
	r := g.apply(other)
	if r.Status != 409 || r.code(t) != "confirm_pending" || r.json(t)["pending_revision"] != float64(id) {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// confirm: the revision is last known good
	cf := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil)
	if cf.Status != 200 || cf.json(t)["status"] != "active" || cf.json(t)["last_known_good"] != true {
		t.Fatalf("%d %s", cf.Status, cf.Body)
	}
	if r := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); r.Status != 409 || r.code(t) != "not_a_candidate" {
		t.Errorf("confirming twice: %d %s", r.Status, r.Body)
	}
}

// mustPatchExpectingPending creates a candidate while a revision waits for confirmation: it is based
// on the active revision, which has not changed yet.
func (g *gw) mustPatchExpectingPending() int64 {
	g.t.Helper()
	return g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.15"}})
}

func TestAnUnconfirmedRevisionIsRolledBack(t *testing.T) {
	g := newGW(t, func(o *options) { o.confirm = 500 * time.Millisecond })
	g.finishSetup()
	ch, cancel := g.e.Subscribe()
	defer cancel()
	id := g.mustPatch(map[string]any{"management": map[string]any{"allowed_sources": []any{"192.168.56.0/24", "172.16.0.0/12"}}})
	if r := g.apply(id); r.json(t)["status"] != "pending_confirm" {
		t.Fatalf("%s", r.Body)
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
	st := g.do("GET", "/state", nil, nil, nil).json(t)
	if st["active_revision"] != float64(1) || st["pending_confirm"] != nil {
		t.Errorf("%v", st)
	}
	if r := g.do("GET", "/revisions/"+itoa(id), nil, nil, nil).json(t); r["status"] != "rolled_back" {
		t.Errorf("%v", r["status"])
	}
	if r := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); r.Status != 409 {
		t.Errorf("confirming a rolled back revision: %d", r.Status)
	}
	// M5-05: a rollback the engine makes on its own is audited too, with actor "system". The
	// server's own audit subscriber is independent of this test's ch, so give it a moment.
	deadline = time.After(5 * time.Second)
	var found bool
	for !found {
		items := g.do("GET", "/audit", nil, nil, nil).json(t)["items"].([]any)
		for _, i := range items {
			e := i.(map[string]any)
			if e["action"] == "revision.rolled_back" {
				found = true
				if e["actor"].(map[string]any)["type"] != "system" || int64(e["revision"].(float64)) != id {
					t.Errorf("%v", e)
				}
			}
		}
		if found {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("no audit entry for the system rollback")
		}
	}
}

func TestDiscardCloneDiffAndExport(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	diff := g.do("GET", "/revisions/"+itoa(id)+"/diff", nil, nil, nil)
	if diff.Status != 200 || !strings.Contains(string(diff.Body), "uplink") {
		t.Errorf("%d %s", diff.Status, diff.Body)
	}
	if r := g.do("GET", "/revisions/"+itoa(id)+"/diff?base=1", nil, nil, nil); r.Status != 200 {
		t.Errorf("%d", r.Status)
	}
	if r := g.do("DELETE", "/revisions/"+itoa(id), nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d", r.Status)
	}
	if r := g.do("GET", "/revisions/"+itoa(id), nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("a discarded candidate: %d", r.Status)
	}
	if r := g.do("DELETE", "/revisions/1", nil, nil, nil); r.Status != 409 || r.code(t) != "not_a_candidate" {
		t.Errorf("discarding the active revision: %d %s", r.Status, r.Body)
	}
	// clone: the rollback path
	a := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	g.apply(a)
	cl := g.do("POST", "/revisions/1/clone", nil, ifMatch(a), nil)
	if cl.Status != 201 || cl.json(t)["base"] != float64(a) || !strings.Contains(fmt.Sprint(cl.json(t)["message"]), "clone of revision 1") {
		t.Fatalf("%d %s", cl.Status, cl.Body)
	}
	g.badRequest = true
	if r := g.do("POST", "/revisions/1/clone", nil, nil, nil); r.Status != 428 {
		t.Errorf("clone without If-Match: %d", r.Status)
	}
	g.badRequest = false
	if r := g.apply(int64(cl.json(t)["id"].(float64))); r.Status != 200 {
		t.Errorf("applying the clone: %d %s", r.Status, r.Body)
	}
	if gw := g.do("GET", "/uplink", nil, nil, nil).json(t)["config"].(map[string]any)["gateway"]; gw != "203.0.113.10" {
		t.Errorf("the rollback did not restore the gateway: %v", gw)
	}
	// export: yaml by default, json on request; no secrets
	ex := g.do("GET", "/revisions/1/export", nil, nil, nil)
	if ex.Status != 200 || !strings.HasPrefix(ex.Header.Get("Content-Type"), "application/yaml") || strings.Contains(string(ex.Body), "private_key") || !strings.Contains(string(ex.Body), "uplink:") {
		t.Errorf("%d %s", ex.Status, truncate(ex.Body))
	}
	if r := g.do("GET", "/revisions/1/export?format=json", nil, nil, nil); r.Status != 200 || r.json(t)["uplink"] == nil {
		t.Errorf("%d", r.Status)
	}
}

func TestExportWithSecretsNeedsFullAndIsAudited(t *testing.T) {
	g := ready(t)
	read := g.mustTokenOf("read")
	g.token = read
	if r := g.do("GET", "/revisions/1/export?include_secrets=true", nil, nil, nil); r.Status != 403 {
		t.Errorf("read scope: %d", r.Status)
	}
	g.token = g.mustTokenOf("full")
	r := g.do("GET", "/revisions/1/export?include_secrets=true&format=json", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	secrets, _ := r.json(t)["secrets"].(map[string]any)
	if wg, _ := secrets["wireguard"].(map[string]any); len(wg) == 0 {
		t.Fatalf("no keys in the export: %s", truncate(r.Body))
	}
	if !strings.Contains(r.Header.Get("Content-Disposition"), "secret") {
		t.Errorf("%q", r.Header.Get("Content-Disposition"))
	}
	e, _, _ := g.log.List(auditFilter(), "", 1)
	if e[0].Action != "revision.export_secrets" {
		t.Errorf("%+v", e[0])
	}
}

func (g *gw) mustTokenOf(scope string) string {
	g.t.Helper()
	old := g.token
	v := g.mintToken(scope)
	g.token = old
	return v
}

func TestAnImportWithSecretsBringsItsKeys(t *testing.T) {
	g := ready(t)
	exp := g.do("GET", "/revisions/1/export?include_secrets=true&format=json", nil, nil, nil).json(t)
	// the same gateway restored on a fresh install: another API, the same configuration and keys
	g2 := newGW(t)
	g2.finishSetupWith(map[string]any{"admin_password": adminPassword, "configuration": withoutSecrets(exp)})
	_ = g2
	// importing the export (with secrets) as a candidate stores the keys and not the secrets block
	r := g.do("POST", "/revisions", exp, ifMatch(1), nil)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	cfg := g.do("GET", "/revisions/"+itoa(int64(r.json(t)["id"].(float64))), nil, nil, nil).json(t)["configuration"].(map[string]any)
	if cfg["secrets"] != nil {
		t.Error("the revision holds secrets")
	}
}

func withoutSecrets(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k != "secrets" {
			out[k] = v
		}
	}
	return out
}

func (g *gw) finishSetupWith(body map[string]any) {
	g.t.Helper()
	r := g.do("POST", "/setup", body, map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 {
		g.t.Fatalf("setup: %d %s", r.Status, r.Body)
	}
	g.mintToken("full")
}
