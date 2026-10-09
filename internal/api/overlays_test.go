package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// M8a: overlays over the API. Every response is checked against the spec by the harness.

const iotFault = `{"target":{"network":"IoT"},"fault":{"latency":"100ms"}}`

func (g *gw) createOverlay(body any) resp {
	g.t.Helper()
	return g.do("POST", "/overlays", body, nil, nil)
}

func (g *gw) mustCreateOverlay(body any) map[string]any {
	g.t.Helper()
	r := g.createOverlay(body)
	if r.Status != 201 && r.Status != 200 {
		g.t.Fatalf("create overlay: %d %s", r.Status, r.Body)
	}
	return r.json(g.t)
}

func TestACreatedOverlayAnswers201AndAReplacementKeepsItsIdWith200(t *testing.T) {
	g := ready(t)
	g.mintToken("overlays")
	r := g.createOverlay(iotFault)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ov := r.json(t)
	id := ov["id"].(string)
	if r.Header.Get("Location") != "/api/v1/overlays/"+id || r.Header.Get("Chaos-Generation") == "" {
		t.Errorf("headers %v", r.Header)
	}
	owner := ov["owner"].(map[string]any)
	if owner["type"] != "token" || owner["name"] != "test-overlays" {
		t.Errorf("owner %v", owner)
	}
	if ov["kind"] != "fault" || ov["state"] != "effective" {
		t.Errorf("%v", ov)
	}
	// the answer comes after the verify: the snapshot's applied generation has caught up
	gen := r.Header.Get("Chaos-Generation")
	if st := g.do("GET", "/state", nil, nil, nil).json(t); fmt.Sprint(st["generation"]) < gen && st["overlays_active"] != float64(1) {
		t.Errorf("state %v after generation %s", st, gen)
	}

	r2 := g.createOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"250ms","loss":"2%"}}`)
	if r2.Status != 200 {
		t.Fatalf("a replacement is 200: %d %s", r2.Status, r2.Body)
	}
	ov2 := r2.json(t)
	if ov2["id"] != id || ov2["created_at"] != ov["created_at"] || ov2["updated_at"] == ov["updated_at"] {
		t.Errorf("replacement %v, first %v", ov2, ov)
	}
	if r2.Header.Get("Chaos-Generation") <= gen && len(gen) == len(r2.Header.Get("Chaos-Generation")) {
		t.Errorf("generations %s then %s", gen, r2.Header.Get("Chaos-Generation"))
	}
	if r2.Header.Get("Location") != "" {
		t.Error("a replacement has no Location")
	}

	got := g.do("GET", "/overlays/"+id, nil, nil, nil)
	if got.Status != 200 || got.json(t)["fault"].(map[string]any)["latency"] != "250ms" {
		t.Errorf("%d %s", got.Status, got.Body)
	}
	if c, ok := got.json(t)["counters"].(map[string]any); !ok || c["epoch"] == nil {
		t.Errorf("an overlay of the kernel shows its counters: %s", got.Body)
	}
	list := g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)
	if len(list) != 1 {
		t.Errorf("%v", list)
	}
	// the audit log
	e, _, _ := g.log.List(auditFilter(), "", 10)
	var actions []string
	for _, x := range e {
		actions = append(actions, x.Action)
	}
	if !strings.Contains(strings.Join(actions, ","), "overlay.create") || !strings.Contains(strings.Join(actions, ","), "overlay.replace") {
		t.Errorf("audit actions %v", actions)
	}
}

func TestOverlaysAreOwnedByTheTokenAndTheUIActsAsTheAdmin(t *testing.T) {
	g := ready(t)
	admin := g.token
	a, b := g.mintToken("overlays"), g.mintToken("overlays")
	_ = a
	// b is the default token now; the admin's token has the scope full
	mine := g.mustCreateOverlay(iotFault)
	g.token = admin
	adminOverlay := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"loss":"1%"}}`)
	if o := adminOverlay["owner"].(map[string]any); o["type"] != "token" {
		t.Errorf("an admin token owns what it writes: %v", o)
	}
	g.token = b

	// another token's overlay can be seen, but not deleted or renewed
	if r := g.do("DELETE", "/overlays/"+adminOverlay["id"].(string), nil, nil, nil); r.Status != 403 || r.code(t) != "forbidden" {
		t.Errorf("deleting another owner's overlay: %d %s", r.Status, r.Body)
	}
	if r := g.do("POST", "/overlays/"+adminOverlay["id"].(string)+"/renew", nil, nil, nil); r.Status != 403 {
		t.Errorf("renewing another owner's overlay: %d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/overlays/"+adminOverlay["id"].(string), nil, nil, nil); r.Status != 200 {
		t.Errorf("reading another owner's overlay: %d", r.Status)
	}
	if r := g.do("GET", "/overlays?owner=self", nil, nil, nil); len(r.json(t)["items"].([]any)) != 1 {
		t.Errorf("owner=self: %s", r.Body)
	}
	if r := g.do("GET", "/overlays?kind=fault", nil, nil, nil); len(r.json(t)["items"].([]any)) != 2 {
		t.Errorf("kind=fault: %s", r.Body)
	}
	if r := g.do("GET", "/overlays?owner="+mine["owner"].(map[string]any)["id"].(string), nil, nil, nil); len(r.json(t)["items"].([]any)) != 1 {
		t.Errorf("owner by id: %s", r.Body)
	}

	// reset takes the caller's overlays and only those; owner=all needs the scope full
	if r := g.do("POST", "/reset?owner=all", nil, nil, nil); r.Status != 403 || r.code(t) != "forbidden" {
		t.Errorf("owner=all with the scope overlays: %d %s", r.Status, r.Body)
	}
	r := g.do("POST", "/reset", nil, nil, nil)
	if r.Status != 200 || r.json(t)["removed_overlays"] != float64(1) || r.json(t)["aborted_runs"] != float64(0) || r.Header.Get("Chaos-Generation") == "" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/overlays", nil, nil, nil); len(r.json(t)["items"].([]any)) != 1 {
		t.Errorf("the admin token's overlay survived the other's reset: %s", r.Body)
	}
	if r := g.do("GET", "/overlays/"+mine["id"].(string), nil, nil, nil); r.Status != 404 || r.code(t) != "not_found" {
		t.Errorf("a reset overlay: %d %s", r.Status, r.Body)
	}
	g.token = admin
	if r := g.do("POST", "/reset?owner=all", nil, nil, nil); r.Status != 200 || r.json(t)["removed_overlays"] != float64(1) {
		t.Errorf("owner=all with the scope full: %d %s", r.Status, r.Body)
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["overlays_active"] != float64(0) {
		t.Errorf("%v", st)
	}
}

func TestAReadOnlyTokenCannotWriteOverlays(t *testing.T) {
	g := ready(t)
	g.mintToken("read")
	for _, c := range []struct{ method, path string }{{"POST", "/overlays"}, {"POST", "/reset"}, {"DELETE", "/overlays/6b0c8f6e-0000-4000-8000-000000000000"}} {
		g.badRequest = true
		if r := g.do(c.method, c.path, map[string]any{}, nil, nil); r.Status != 403 {
			t.Errorf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	g.badRequest = false
	if r := g.do("GET", "/overlays", nil, nil, nil); r.Status != 200 {
		t.Errorf("reading: %d", r.Status)
	}
}

func TestOverlaysOfLaterMilestonesAreRefusedAndInvalidOnesAreExplained(t *testing.T) {
	g := ready(t)
	for body, milestone := range map[string]string{
		`{"target":{"network":"IoT"},"profile":"bad-lte"}`:                                 "M11",
		`{"target":{"network":"IoT"},"dns":{"names":["example.com"],"action":"nxdomain"}}`: "M20",
		`{"target":{"network":"IoT"},"tls":{"case":"expired"}}`:                            "M21",
		`{"target":{"network":"IoT"},"dhcp":{"action":"silence"}}`:                         "M23",
	} {
		r := g.createOverlay(body)
		if r.Status != 422 || r.code(t) != "unsupported_feature" || !strings.Contains(r.json(t)["detail"].(string), milestone) {
			t.Errorf("%s: %d %s", body, r.Status, r.Body)
		}
	}
	// invalid: the pointer is relative to the request
	r := g.createOverlay(`{"target":{"network":"Nowhere"},"fault":{"latency":"10ms"}}`)
	if r.Status != 422 || r.code(t) != "validation_failed" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	errs := r.json(t)["errors"].([]any)
	if first := errs[0].(map[string]any); first["path"] != "/target/network" || first["code"] != "unknown_reference" {
		t.Errorf("%v", errs)
	}
	r = g.createOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"10ms","jitter":"50ms"}}`)
	if r.Status != 422 || r.json(t)["errors"].([]any)[0].(map[string]any)["path"] != "/fault/jitter" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	g.badRequest = true
	if r := g.createOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"10ms"},"surprise":1}`); r.Status != 422 || r.code(t) != "validation_failed" {
		t.Errorf("an unknown field: %d %s", r.Status, r.Body)
	}
	if r := g.createOverlay(`{"target":`); r.Status != 400 {
		t.Errorf("malformed JSON: %d %s", r.Status, r.Body)
	}
	g.badRequest = false
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("%d overlays", n)
	}
}

func TestALeaseIsRenewedOverTheAPIAndALapsedOneIsRemoved(t *testing.T) {
	g := ready(t)
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"50ms"},"lease":"2s"}`)
	id := ov["id"].(string)
	if ov["lease_expires_at"] == nil || ov["expires_at"] != nil {
		t.Errorf("%v", ov)
	}
	r := g.do("POST", "/overlays/"+id+"/renew", nil, nil, nil)
	if r.Status != 200 || r.json(t)["lease_expires_at"] == ov["lease_expires_at"] {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	noLease := g.mustCreateOverlay(`{"target":{"network":"lab-hub"},"fault":{"loss":"1%"}}`)
	if r := g.do("POST", "/overlays/"+noLease["id"].(string)+"/renew", nil, nil, nil); r.Status != 422 || r.code(t) != "validation_failed" {
		t.Errorf("renewing an overlay without a lease: %d %s", r.Status, r.Body)
	}
	// without renewing, the lease runs out and the overlay is gone (the real clock, a short lease)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if g.do("GET", "/overlays/"+id, nil, nil, nil).Status == 404 {
			if r := g.do("POST", "/overlays/"+id+"/renew", nil, nil, nil); r.Status != 404 {
				t.Errorf("renewing an expired overlay: %d", r.Status)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the lease never ran out")
}

func TestDeletingAnOverlayAnswers204AfterTheVerify(t *testing.T) {
	g := ready(t)
	ov := g.mustCreateOverlay(iotFault)
	r := g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil)
	if r.Status != 204 || r.Header.Get("Chaos-Generation") == "" {
		t.Fatalf("%d %s %v", r.Status, r.Body, r.Header)
	}
	if r := g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil); r.Status != 404 {
		t.Errorf("deleting twice: %d", r.Status)
	}
	if r := g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
}

func TestAnOverlayThatDoesNotFitTheClassLimitIsRefusedWithCapacityExceeded(t *testing.T) {
	g := newGW(t, func(o *options) { o.classLimit = 4 })
	g.finishSetup()
	first := g.mustCreateOverlay(iotFault) // 2 classes and the default one
	r := g.createOverlay(`{"target":{"network":"lab-hub"},"fault":{"latency":"20ms"}}`)
	if r.Status != 422 || r.code(t) != "capacity_exceeded" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if errs := r.json(t)["errors"].([]any); len(errs) == 0 || errs[0].(map[string]any)["code"] != "capacity_exceeded" {
		t.Errorf("%s", r.Body)
	}
	// the refused write left no trace, and the overlay that was there is untouched
	list := g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != first["id"] {
		t.Errorf("%v", list)
	}
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["last_apply"].(map[string]any)["result"] != "ok" {
		t.Errorf("%v", st)
	}
}

func TestCapabilitiesNameWhatOverlaysThisBuildTakes(t *testing.T) {
	g := ready(t)
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	if fmt.Sprint(caps["overlay_kinds"]) != "[fault rule]" || fmt.Sprint(caps["fault_families"]) != "[impairment mtu]" {
		t.Errorf("%v", caps)
	}
	if !strings.Contains(fmt.Sprint(caps["features"]), "overlays") {
		t.Errorf("%v", caps["features"])
	}
}

func TestOverlayEventsAreOnTheStream(t *testing.T) {
	g := ready(t)
	ev, cancel := g.e.Subscribe()
	defer cancel()
	ov := g.mustCreateOverlay(iotFault)
	g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil)
	var got []string
	timeout := time.After(10 * time.Second)
	for len(got) < 2 {
		select {
		case e := <-ev:
			if strings.HasPrefix(e.Type, "overlay_") {
				got = append(got, e.Type)
			}
		case <-timeout:
			t.Fatalf("events %v", got)
		}
	}
	if got[0] != engine.EventOverlayCreated || got[1] != engine.EventOverlayRemoved {
		t.Errorf("%v", got)
	}
}

// A restart drops the overlays: they are never written to disk (plan §2.1.1).
func TestARestartOfTheAPIDropsTheOverlays(t *testing.T) {
	root := t.TempDir()
	g1 := newGW(t, func(o *options) { o.root = root })
	g1.finishSetup()
	g1.mustCreateOverlay(iotFault)
	if n := len(g1.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 1 {
		t.Fatalf("%d overlays", n)
	}
	g1.close()
	g2 := newGW(t, func(o *options) { o.root = root })
	g2.mintToken("full")
	if _, err := g2.e.Barrier(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(g2.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("%d overlays after the restart", n)
	}
}

func TestExplainNamesTheWinnerAndTheRouteOverTheAPI(t *testing.T) {
	g := ready(t)
	// a device behind the hub is a WireGuard client; its tunnel address identifies it
	base := g.mustCreateOverlay(`{"target":{"global":true},"fault":{"loss":"1%"}}`)
	net := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"80ms"}}`)
	g.observe()
	r := g.do("GET", "/explain?src=10.10.0.31&dst=198.51.100.7&protocol=tcp&port=443", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ex := r.json(t)
	faults := ex["faults"].([]any)
	if len(faults) == 0 {
		t.Fatalf("%s", r.Body)
	}
	imp := faults[0].(map[string]any)
	win := imp["winner"].(map[string]any)
	if imp["family"] != "impairment" || win["id"] != net["id"] || win["layer"] != "overlay" || win["level"] != float64(8) {
		t.Errorf("winner %v", win)
	}
	over := imp["overridden"].([]any)
	if len(over) != 1 || over[0].(map[string]any)["id"] != base["id"] {
		t.Errorf("overridden %v", over)
	}
	if ex["source"].(map[string]any)["network"].(map[string]any)["name"] != "IoT" {
		t.Errorf("source %v", ex["source"])
	}
	if ex["access"].(map[string]any)["verdict"] == nil || ex["generation"] == nil || ex["service"] != "none" {
		t.Errorf("%v", ex)
	}
	if rt, ok := ex["route"].(map[string]any); !ok || rt["table"] != float64(100) {
		t.Errorf("the route comes from the kernel's table 100: %v", ex["route"])
	}
	if k, ok := ex["kernel"].(map[string]any); !ok || k["fault_id"] == nil || !strings.HasPrefix(k["mark_upload"].(string), "0x") {
		t.Errorf("kernel %v", ex["kernel"])
	}

	// bad questions
	g.badRequest = true
	for q, status := range map[string]int{
		"dst=198.51.100.7":                          400,
		"src=10.10.0.31":                            400,
		"src=10.10.0.31&dst=2001:db8::1":            400,
		"src=10.10.0.31&dst=not%20a%20host":         400,
		"src=banana&dst=198.51.100.7":               400,
		"device=nobody&dst=198.51.100.7":            404,
		"src=10.10.0.31&dst=example.com&protocol=x": 400,
	} {
		if r := g.do("GET", "/explain?"+q, nil, nil, nil); r.Status != status {
			t.Errorf("%s: %d %s", q, r.Status, r.Body)
		}
	}
	g.badRequest = false
	// a hostname destination resolves what it can: no address, no route
	if r := g.do("GET", "/explain?src=10.10.0.31&dst=example.com", nil, nil, nil); r.Status != 200 || r.json(t)["route"] != nil {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

func TestConfiguredFaultsAreListedWithTheirStateAndWhatOverridesThem(t *testing.T) {
	g := ready(t)
	id := "5f1c7a9e-6d3b-4e8a-9c2f-1a2b3c4d5e6f"
	rev := g.mustPatch(map[string]any{"faults": map[string]any{id: map[string]any{
		"name": "slow-iot", "source": map[string]any{"network": "IoT"}, "latency": "150ms",
	}}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.do("GET", "/faults", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	items := r.json(t)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("%s", r.Body)
	}
	f := items[0].(map[string]any)
	if f["id"] != id || f["state"] != "effective" || f["config"].(map[string]any)["name"] != "slow-iot" {
		t.Errorf("%v", f)
	}
	if f["counters"] == nil {
		t.Errorf("a configured fault in the kernel has counters: %v", f)
	}
	// an overlay on the same network overrides it everywhere
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"10ms"}}`)
	g.observe()
	got := g.do("GET", "/faults/slow-iot", nil, nil, nil)
	if got.Status != 200 {
		t.Fatalf("%d %s", got.Status, got.Body)
	}
	v := got.json(t)
	if v["state"] != "overridden" {
		t.Errorf("%v", v)
	}
	if by, ok := v["overridden_by"].([]any); !ok || len(by) != 1 || by[0].(map[string]any)["id"] != ov["id"] {
		t.Errorf("overridden_by %v", v["overridden_by"])
	}
	if r := g.do("GET", "/faults?family=mtu", nil, nil, nil); len(r.json(t)["items"].([]any)) != 0 {
		t.Errorf("%s", r.Body)
	}
	if r := g.do("GET", "/faults/nonesuch", nil, nil, nil); r.Status != 404 {
		t.Errorf("%d", r.Status)
	}
	// the first revision has no such fault; the active one shows it without a state of the kernel
	if r := g.do("GET", "/faults/"+id+"?revision=1", nil, nil, nil); r.Status != 404 {
		t.Errorf("an older revision has no such fault: %d %s", r.Status, r.Body)
	}
	if r := g.do("GET", "/faults/"+id+"?revision="+itoa(rev), nil, nil, nil); r.Status != 200 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

// A retried POST with the same Idempotency-Key answers what the first one answered and does not
// write the overlay a second time (a replay of a 201 stays a 201).
func TestARetriedOverlayWriteWithTheSameKeyIsReplayed(t *testing.T) {
	g := ready(t)
	h := map[string]string{"Idempotency-Key": "once"}
	first := g.do("POST", "/overlays", iotFault, h, nil)
	again := g.do("POST", "/overlays", iotFault, h, nil)
	if first.Status != 201 || again.Status != 201 || again.Header.Get("Idempotent-Replay") != "true" || string(first.Body) != string(again.Body) {
		t.Fatalf("%d %s / %d %s", first.Status, first.Body, again.Status, again.Body)
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 1 {
		t.Errorf("%d overlays", n)
	}
}

// A revision that deletes an object an active overlay refers to is refused with validation_failed
// and the references; with force it applies, removes the overlays and says which.
func TestARevisionThatDeletesAReferencedObjectIsRefusedUnlessForced(t *testing.T) {
	g := ready(t)
	devID := "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	add := g.mustPatch(map[string]any{"devices": map[string]any{devID: map[string]any{
		"name": "esp32-42", "identifiers": map[string]any{"macs": []string{"02:00:00:00:00:31"}},
	}}})
	if r := g.apply(add); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	admin := g.token
	g.mintToken("overlays")
	ov := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"fault":{"latency":"40ms"}}`)
	keep := g.mustCreateOverlay(iotFault)
	g.token = admin
	del := g.mustPatch(map[string]any{"devices": map[string]any{devID: nil}})

	r := g.apply(del)
	if r.Status != 422 || r.code(t) != "validation_failed" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	refs, _ := r.json(t)["references"].([]any)
	if len(refs) != 1 {
		t.Fatalf("references %s", r.Body)
	}
	ref := refs[0].(map[string]any)
	if ref["kind"] != "overlay" || ref["id"] != ov["id"] || ref["object"] != "/devices/"+devID || ref["owner"].(map[string]any)["type"] != "token" {
		t.Errorf("reference %v", ref)
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 2 {
		t.Errorf("a refused apply left %d overlays", n)
	}
	if g.activeID() != add {
		t.Errorf("the active revision is %d", g.activeID())
	}
	// the preview shows the same references
	pv := g.do("POST", "/revisions/"+itoa(del)+"/preview", nil, nil, nil)
	if pv.Status != 200 {
		t.Fatalf("%d %s", pv.Status, pv.Body)
	}
	if prefs, _ := pv.json(t)["references"].([]any); len(prefs) != 1 || prefs[0].(map[string]any)["id"] != ov["id"] {
		t.Errorf("preview references %s", pv.Body)
	}

	// force=true: the revision applies and the overlay goes, the other one stays
	r = g.do("POST", "/revisions/"+itoa(del)+"/apply?force=true", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	removed, _ := r.json(t)["removed_overlays"].([]any)
	if len(removed) != 1 || removed[0] != ov["id"] {
		t.Errorf("removed_overlays %s", r.Body)
	}
	items := g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != keep["id"] {
		t.Errorf("overlays %v", items)
	}
	if g.do("GET", "/overlays/"+ov["id"].(string), nil, nil, nil).Status != 404 {
		t.Error("the orphaned overlay is still there")
	}
	e, _, _ := g.log.List(auditFilter(), "", 10)
	found := false
	for _, x := range e {
		found = found || (x.Action == "revision.apply" && strings.Contains(x.Detail, "removed 1 overlays"))
	}
	if !found {
		t.Errorf("the audit log does not say that an overlay was removed: %+v", e)
	}
}

// The queues of an overlay and of a configured fault: one per interface of the tree and direction, with
// the kernel's counters and the epoch of the leaf (M8b).
func TestAnOverlayAndAFaultShowTheirNetemQueuesWithEpochs(t *testing.T) {
	g := ready(t)
	fid := "5f1c7a9e-6d3b-4e8a-9c2f-1a2b3c4d5e6f"
	rev := g.mustPatch(map[string]any{"faults": map[string]any{fid: map[string]any{
		"name": "slow-lab", "source": map[string]any{"network": "IoT"}, "latency": "150ms", "destination": map[string]any{"cidr": "192.0.2.0/24"},
	}}})
	if r := g.apply(rev); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"100ms","loss":"1%","destination":{"cidr":"198.51.100.0/24"}}}`)
	id := ov["id"].(string)
	var oid, cid int
	for _, f := range g.e.Snapshot().Faults {
		if f.Source == id {
			oid = f.ID
		} else {
			cid = f.ID
		}
	}
	if oid == 0 || cid == 0 {
		t.Fatalf("faults %+v", g.e.Snapshot().Faults)
	}
	devs := g.e.Snapshot().TCDevs
	if len(devs) < 2 {
		t.Fatalf("the tree is on %v", devs)
	}
	leaf := func(fid int, dir compiler.Direction) string {
		return strings.TrimPrefix(compiler.ClassIDOf(fid, dir), "1:") + ":"
	}
	g.k.SetTCStats(devs[0], leaf(oid, compiler.Upload), linux.NormStats{Bytes: 14200, Packets: 100, Drops: 4, Overlimits: 1, Backlog: 2900, Qlen: 2})
	g.k.SetTCStats(devs[1], leaf(cid, compiler.Download), linux.NormStats{Bytes: 600, Packets: 3})

	queues := func(v map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		qs, _ := v["queues"].([]any)
		for _, q := range qs {
			m := q.(map[string]any)
			out[m["interface"].(string)+" "+m["direction"].(string)] = m
		}
		return out
	}
	got := g.do("GET", "/overlays/"+id, nil, nil, nil)
	if got.Status != 200 {
		t.Fatalf("%d %s", got.Status, got.Body)
	}
	qs := queues(got.json(t))
	if len(qs) != 2*len(devs) {
		t.Fatalf("%d queues, want upload and download on each of three interfaces: %s", len(qs), got.Body)
	}
	up := qs[devs[0]+" upload"]
	if up["sent_packets"] != float64(100) || up["sent_bytes"] != float64(14200) || up["dropped_packets"] != float64(4) ||
		up["overlimits"] != float64(1) || up["backlog_packets"] != float64(2) || up["backlog_bytes"] != float64(2900) || up["epoch"] == nil || up["epoch"] == float64(0) {
		t.Errorf("the upload queue of br-iot: %v", up)
	}
	if d := qs[devs[0]+" download"]; d["sent_packets"] != float64(0) || d["epoch"] != up["epoch"] {
		t.Errorf("the download queue of br-iot: %v", d)
	}
	// the queues of the configured fault are not the overlay's
	cf := g.do("GET", "/faults/slow-lab", nil, nil, nil).json(t)
	cq := queues(cf)
	if len(cq) != 2*len(devs) || cq[devs[1]+" download"]["sent_packets"] != float64(3) || cq[devs[0]+" upload"]["sent_packets"] != float64(0) {
		t.Errorf("the queues of the configured fault: %v", cf["queues"])
	}
	lst := g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)
	if len(lst) != 1 || len(queues(lst[0].(map[string]any))) != 2*len(devs) {
		t.Errorf("the list of overlays: %v", lst)
	}

	// a replacement changes the parameters in place: same queues, same epoch, same counters
	g.createOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"250ms","loss":"2%","destination":{"cidr":"198.51.100.0/24"}}}`)
	again := queues(g.do("GET", "/overlays/"+id, nil, nil, nil).json(t))
	if again[devs[0]+" upload"]["epoch"] != up["epoch"] || again[devs[0]+" upload"]["sent_packets"] != float64(100) {
		t.Errorf("a replacement restarted the queue: %v, was %v", again[devs[0]+" upload"], up)
	}
	// the state carries the epoch of the counters as a whole
	if st := g.do("GET", "/state", nil, nil, nil).json(t); st["counter_epoch"] == nil || st["counter_epoch"] == float64(0) {
		t.Errorf("state %v", st)
	}
}

// Plan M10: exceeding the class limit returns capacity_exceeded in the preview of a revision, naming the
// scope that needs the classes, and the same revision is refused at the apply. The limit this test
// assumes is the one it sets (6: five classes and the default one).
func TestAConfiguredFaultSetThatDoesNotFitTheClassLimitIsRefusedByThePreviewWithItsScope(t *testing.T) {
	g := newGW(t, func(o *options) { o.classLimit = 6 })
	g.finishSetup()
	faults := map[string]any{
		"a1000000-0000-4000-8000-000000000001": map[string]any{"source": map[string]any{"network": "IoT"}, "latency": "10ms"},
		"a1000000-0000-4000-8000-000000000002": map[string]any{"source": map[string]any{"network": "lab-hub"}, "latency": "10ms"},
	}
	id := g.mustPatch(map[string]any{"faults": faults})
	if r := g.do("POST", "/revisions/"+itoa(id)+"/preview", nil, nil, nil); r.Status != 200 {
		t.Fatalf("two faults fit the limit of 6 (5 classes): %d %s", r.Status, r.Body)
	}
	// a third needs two more classes: 7 on every interface
	faults["a1000000-0000-4000-8000-000000000003"] = map[string]any{"source": map[string]any{"network": "IoT"}, "latency": "20ms", "destination": map[string]any{"cidr": "198.51.100.1/32"}, "rate": "1Mbit"}
	id = g.mustPatch(map[string]any{"faults": faults})
	for _, path := range []string{"/revisions/" + itoa(id) + "/preview", "/revisions/" + itoa(id) + "/apply"} {
		r := g.do("POST", path, nil, nil, nil)
		if r.Status != 422 || r.code(t) != "capacity_exceeded" {
			t.Fatalf("%s: %d %s", path, r.Status, r.Body)
		}
		errs := r.json(t)["errors"].([]any)
		first := errs[0].(map[string]any)
		if first["code"] != "capacity_exceeded" || !strings.Contains(first["message"].(string), "limit of 6") || first["path"] == "" {
			t.Errorf("%s: %v", path, errs)
		}
	}
}

// A flapping fault's queues say where it is in its cycle: up, then a blackout, with the time the phase began
// and the time the schedule ends it. The engine runs on the real clock here; the phase changes after a second.
func TestTheQueuesOfAFlappingFaultShowTheirPhase(t *testing.T) {
	g := ready(t)
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"20ms","flapping":{"up":"1s","down":"1500ms"}}}`)
	id := ov["id"].(string)
	phases := func() (up, down map[string]any) {
		got := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)
		qs, _ := got["queues"].([]any)
		for _, q := range qs {
			m := q.(map[string]any)
			f, ok := m["flapping"].(map[string]any)
			if !ok {
				t.Fatalf("a queue of a flapping fault without its phase: %v", m)
			}
			if m["direction"] == "upload" {
				up = f
			} else {
				down = f
			}
		}
		return up, down
	}
	up, down := phases()
	if up["phase"] != "up" || down["phase"] != "up" {
		t.Fatalf("a flapping starts up: %v %v", up, down)
	}
	since, _ := time.Parse(time.RFC3339Nano, up["since"].(string))
	next, _ := time.Parse(time.RFC3339Nano, up["next_change_at"].(string))
	if d := next.Sub(since); d < 900*time.Millisecond || d > 1100*time.Millisecond {
		t.Errorf("the up phase of 1 s runs from %v to %v", since, next)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if up, _ = phases(); up["phase"] == "down" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fault never entered its down phase: %v", up)
		}
		time.Sleep(50 * time.Millisecond)
	}
	since, _ = time.Parse(time.RFC3339Nano, up["since"].(string))
	next, _ = time.Parse(time.RFC3339Nano, up["next_change_at"].(string))
	if d := next.Sub(since); d < 1400*time.Millisecond || d > 1600*time.Millisecond {
		t.Errorf("the down phase of 1.5 s runs from %v to %v", since, next)
	}
	// a fault that does not flap has no phase
	plain := g.mustCreateOverlay(`{"target":{"network":"lab-hub"},"fault":{"latency":"20ms"}}`)
	got := g.do("GET", "/overlays/"+plain["id"].(string), nil, nil, nil).json(t)
	for _, q := range got["queues"].([]any) {
		if _, ok := q.(map[string]any)["flapping"]; ok {
			t.Errorf("a queue without flapping has a phase: %v", q)
		}
	}
}
