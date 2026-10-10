package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	specpkg "github.com/Andste82/chaos-gateway/api"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

func TestIdempotencyKeys(t *testing.T) {
	g := ready(t)
	body := map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}}
	h := map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": `"1"`, "Idempotency-Key": "retry-me"}
	first := g.do("POST", "/revisions", body, h, nil)
	if first.Status != 201 {
		t.Fatalf("%d %s", first.Status, first.Body)
	}
	// the retry gets the same response, and no second revision exists
	second := g.do("POST", "/revisions", body, h, nil)
	if second.Status != 201 || string(second.Body) != string(first.Body) || second.Header.Get("Idempotent-Replay") != "true" || second.Header.Get("Location") != first.Header.Get("Location") {
		t.Fatalf("%d %s %v", second.Status, second.Body, second.Header)
	}
	if n := len(g.do("GET", "/revisions", nil, nil, nil).json(t)["items"].([]any)); n != 2 {
		t.Errorf("%d revisions", n)
	}
	// the same key with another body is refused
	other := map[string]any{"uplink": map[string]any{"gateway": "203.0.113.12"}}
	if r := g.do("POST", "/revisions", other, h, nil); r.Status != 422 || r.code(t) != "idempotency_conflict" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// a key of another token is another key
	g.mintToken("full")
	if r := g.do("POST", "/revisions", other, h, nil); r.Status != 201 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	// an error is not remembered: the retry runs again
	bad := map[string]any{"uplink": map[string]any{"gateway": "nope"}}
	h2 := map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": `"1"`, "Idempotency-Key": "fail-first"}
	if r := g.do("POST", "/revisions", bad, h2, nil); r.Status != 422 {
		t.Fatalf("%d", r.Status)
	}
	if r := g.do("POST", "/revisions", bad, h2, nil); r.Status != 422 || r.Header.Get("Idempotent-Replay") != "" {
		t.Errorf("%d %v", r.Status, r.Header)
	}
}

func TestConcurrentRequestsWithOneKeyDoOneThing(t *testing.T) {
	g := ready(t)
	body := map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}}
	h := map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": `"1"`, "Idempotency-Key": "race"}
	var wg sync.WaitGroup
	res := make([]resp, 5)
	for i := range res {
		wg.Add(1)
		go func() { defer wg.Done(); res[i] = g.do("POST", "/revisions", body, h, nil) }()
	}
	wg.Wait()
	for _, r := range res {
		if r.Status != 201 || string(r.Body) != string(res[0].Body) {
			t.Errorf("%d %s", r.Status, r.Body)
		}
	}
	if n := len(g.do("GET", "/revisions", nil, nil, nil).json(t)["items"].([]any)); n != 2 {
		t.Errorf("%d revisions for one key", n)
	}
}

// pathValue gives a path parameter a value of the right shape.
func pathValue(name string) string {
	switch name {
	case "revisionId":
		return "1"
	}
	return "00000000-0000-4000-8000-000000000001"
}

func fill(path string) string {
	for {
		i := strings.IndexByte(path, '{')
		if i < 0 {
			return path
		}
		j := strings.IndexByte(path, '}')
		path = path[:i] + pathValue(path[i+1:j]) + path[j+1:]
	}
}

func specOps(t *testing.T) (m5, later []struct {
	Method, Path, Milestone string
	Internal                bool
}) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(specpkg.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	for path, item := range spec.Paths {
		for key, node := range item {
			m := strings.ToUpper(key)
			if m != "GET" && m != "POST" && m != "PUT" && m != "DELETE" && m != "PATCH" {
				continue
			}
			var o struct {
				Milestone string `yaml:"x-milestone"`
				Internal  bool   `yaml:"x-internal"`
			}
			if err := node.Decode(&o); err != nil {
				t.Fatal(err)
			}
			e := struct {
				Method, Path, Milestone string
				Internal                bool
			}{m, path, o.Milestone, o.Internal}
			if o.Milestone == "M5" || o.Milestone == "M6a" || o.Milestone == "M6b" || o.Milestone == "M8a" || o.Milestone == "M9" || o.Milestone == "M11" { // implemented in this build
				m5 = append(m5, e)
			} else {
				later = append(later, e)
			}
		}
	}
	return
}

func TestOperationsOfLaterMilestonesAreUnsupported(t *testing.T) {
	g := ready(t)
	_, later := specOps(t)
	if len(later) < 20 {
		t.Fatalf("only %d operations of later milestones", len(later))
	}
	g.badRequest = true // every operation here is probed generically with an empty body
	for _, o := range later {
		var body any
		if o.Method == http.MethodPost || o.Method == http.MethodPut || o.Method == http.MethodPatch {
			body = map[string]any{}
		}
		r := g.do(o.Method, fill(o.Path), body, nil, nil)
		switch {
		case o.Internal:
			// the internal API takes service tokens only
			if r.Status != 403 {
				t.Errorf("%s %s: %d", o.Method, o.Path, r.Status)
			}
		case r.Status == 422 && r.code(t) == "unsupported_feature":
			if d := r.json(t)["detail"].(string); !strings.Contains(d, o.Milestone) {
				t.Errorf("%s %s: the problem does not name %s: %s", o.Method, o.Path, o.Milestone, d)
			}
		case r.Status == 400 || r.Status == 422:
			// a parameter the wrapper rejects before the handler (required query parameters)
		default:
			t.Errorf("%s %s (%s): %d %s", o.Method, o.Path, o.Milestone, r.Status, truncate(r.Body))
		}
	}
	g.badRequest = false
}

func TestEveryOperationOfThisMilestoneExists(t *testing.T) {
	g := ready(t)
	m5, _ := specOps(t)
	if len(m5) != 62 {
		t.Fatalf("%d operations of M5", len(m5))
	}
	g.badRequest = true // every operation here is probed generically with an empty body
	for _, o := range m5 {
		var body any
		if o.Method == http.MethodPost || o.Method == http.MethodPut || o.Method == http.MethodPatch {
			body = map[string]any{}
		}
		path := fill(o.Path)
		if o.Method == http.MethodGet && o.Path == "/events" {
			continue // a stream: tested on its own
		}
		hdr := map[string]string{"If-Match": `"1"`}
		r := g.do(o.Method, path, body, hdr, nil)
		if r.Status >= 500 {
			t.Errorf("%s %s: %d %s", o.Method, o.Path, r.Status, truncate(r.Body))
		}
		if r.Status == 422 && strings.Contains(string(r.Body), "unsupported_feature") {
			t.Errorf("%s %s is not implemented", o.Method, o.Path)
		}
		if r.Status == 404 && strings.Contains(string(r.Body), "no such resource") {
			t.Errorf("%s %s is not routed", o.Method, o.Path)
		}
	}
	g.badRequest = false
}

// M6a-14 test: a failed read of the observed state degrades the health check, with a detail saying
// so, instead of staying silently healthy.
func TestHealthDegradesWhenObservationFails(t *testing.T) {
	g := dhcpGateway(t)
	g.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "conntrack" {
			return &executor.Result{Exit: 1, Stderr: "conntrack: netlink error\n"}
		}
		return nil
	}
	g.observe()
	hb := g.do("GET", "/system/health", nil, nil, nil).json(t)
	if hb["status"] != "degraded" {
		t.Fatalf("%v", hb)
	}
	comps, _ := hb["components"].([]any)
	var found bool
	for _, c := range comps {
		m := c.(map[string]any)
		if m["name"] == "api" && m["status"] == "degraded" && strings.Contains(fmt.Sprint(m["detail"]), "observation degraded") {
			found = true
		}
	}
	if !found {
		t.Errorf("%v", comps)
	}
}

func TestSystemEndpoints(t *testing.T) {
	g := ready(t)
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	feats := fmt.Sprint(caps["features"])
	for _, f := range []string{"networks.wireguard", "routing.bird", "networks.lan"} {
		if !strings.Contains(feats, f) {
			t.Errorf("capability %s is missing: %v", f, feats)
		}
	}
	// M8a: faults of the impairment family, M11: profile activations, M10: the mtu and tunnel families and the WireGuard actions, M9: access
	// rules; the other kinds follow with their milestones
	if fmt.Sprint(caps["overlay_kinds"]) != "[fault profile rule wireguard]" || fmt.Sprint(caps["fault_families"]) != "[impairment mtu tunnel]" || caps["version"] != "test" {
		t.Errorf("%v", caps)
	}
	info := g.do("GET", "/system/info", nil, nil, nil).json(t)
	if info["version"] != "test" || info["boot_id"] != "boot-1" || info["api_version"] != "v1" || info["arch"] == nil {
		t.Errorf("%v", info)
	}
	// the health check works without credentials and then shows only the status; this fixture has
	// no DHCP, no service namespace and a DNS proxy that has never polled, so it is "degraded"
	// rather than "healthy" (M6b-05, M4-01)
	g.token = ""
	h := g.do("GET", "/system/health", nil, nil, nil)
	if h.Status != 200 || h.json(t)["status"] != "degraded" || h.json(t)["components"] != nil {
		t.Errorf("%d %s", h.Status, h.Body)
	}
	g.mintToken("read")
	hb := g.do("GET", "/system/health", nil, nil, nil).json(t)
	comps, _ := hb["components"].([]any)
	if len(comps) < 2 {
		t.Errorf("%v", hb)
	}
	names := map[string]bool{}
	for _, c := range comps {
		names[c.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"api", "executor", "kea", "svcns", "dns"} {
		if !names[want] {
			t.Errorf("missing component %q: %v", want, hb)
		}
	}
	if p := g.do("GET", "/system/preflight", nil, nil, nil).json(t); p["status"] == nil {
		t.Errorf("%v", p)
	}
	ifs := g.do("GET", "/system/interfaces", nil, nil, nil).json(t)["items"].([]any)
	roles := map[string]string{}
	for _, i := range ifs {
		m := i.(map[string]any)
		if a, ok := m["assigned"].(map[string]any); ok {
			roles[m["name"].(string)] = a["role"].(string)
		}
	}
	if roles["wan0"] != "uplink" || roles["mgmt0"] != "management" || roles["lan0"] != "network" || roles["lan1"] != "" {
		t.Errorf("%v", roles)
	}
}

func TestTheAuditLogRecordsWritesNotReads(t *testing.T) {
	g := ready(t)
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	g.apply(id)
	g.do("GET", "/state", nil, nil, nil)
	g.do("POST", "/auth/tokens", map[string]any{"name": "t", "scope": "read"}, nil, nil)
	r := g.do("GET", "/audit", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	items := r.json(t)["items"].([]any)
	var actions []string
	for _, i := range items {
		e := i.(map[string]any)
		actions = append(actions, e["action"].(string))
		if e["actor"].(map[string]any)["type"] == nil || e["via"] == nil || e["time"] == nil {
			t.Errorf("%v", e)
		}
	}
	got := strings.Join(actions, ",")
	// newest first: the token, the apply, the creation, the setup's own confirm (M5-03) and completion
	if !strings.HasPrefix(got, "token.create,revision.apply,revision.create,revision.confirm,setup.complete") {
		t.Errorf("%s", got)
	}
	if strings.Contains(got, "state") {
		t.Errorf("a read in the log: %s", got)
	}
	// the secret never enters the log
	if strings.Contains(string(r.Body), "cgw_") {
		t.Errorf("a token value in the audit log")
	}
	p1 := g.do("GET", "/audit?limit=2", nil, nil, nil).json(t)
	if len(p1["items"].([]any)) != 2 || p1["next_cursor"] == nil {
		t.Errorf("%v", p1)
	}
	p2 := g.do("GET", "/audit?limit=2&cursor="+p1["next_cursor"].(string), nil, nil, nil).json(t)
	if p2["items"].([]any)[0].(map[string]any)["id"] == p1["items"].([]any)[0].(map[string]any)["id"] {
		t.Error("the cursor does not advance")
	}
	if r := g.do("GET", "/audit?since=2999-01-01T00:00:00Z", nil, nil, nil).json(t)["items"].([]any); len(r) != 0 {
		t.Errorf("%v", r)
	}
}

func TestProblemsAreProblemJSON(t *testing.T) {
	g := ready(t)
	for _, c := range []struct {
		method, path string
		status       int
		code         string
	}{
		{"GET", "/revisions/999", 404, "not_found"},
		{"GET", "/networks/ghost", 404, "not_found"},
		{"GET", "/nothing-here", 404, "not_found"},
	} {
		g.noContract = c.path == "/nothing-here"
		r := g.do(c.method, c.path, nil, nil, nil)
		if r.Status != c.status || r.code(t) != c.code {
			t.Errorf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
		p := r.json(t)
		if p["type"] != "https://chaos-gateway.dev/problems/"+c.code || p["title"] == nil || p["status"] != float64(c.status) {
			t.Errorf("%v", p)
		}
	}
	g.noContract = false
	// a method the route does not have
	g.noContract = true
	if r := g.do("PUT", "/state", nil, nil, nil); r.Status != 405 {
		t.Errorf("%d", r.Status)
	}
}

// M5-12 test: a method the path does not have gives 405 with an Allow header, not a bare 400.
func TestWrongMethodGives405WithAllow(t *testing.T) {
	g := ready(t)
	g.noContract = true
	r := g.do("PUT", "/state", nil, nil, nil)
	if r.Status != 405 || r.code(t) != "method_not_allowed" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	allow := r.Header.Get("Allow")
	if !strings.Contains(allow, "GET") {
		t.Errorf("Allow: %q", allow)
	}
}

// M5-04 test: a failing audit log marks the API component unhealthy.
func TestAFailingAuditLogMakesTheHealthUnhealthy(t *testing.T) {
	g := ready(t)
	if err := g.log.Close(); err != nil {
		t.Fatal(err)
	}
	// any write triggers an audit entry, which now fails
	g.mustPatch(map[string]any{"uplink": map[string]any{"gateway": "203.0.113.11"}})
	h := g.do("GET", "/system/health", nil, nil, nil)
	if h.Status != 503 || h.json(t)["status"] != "unhealthy" {
		t.Fatalf("%d %s", h.Status, h.Body)
	}
	comps := h.json(t)["components"].([]any)
	var found bool
	for _, c := range comps {
		m := c.(map[string]any)
		if m["name"] == "api" && m["status"] == "unhealthy" {
			found = true
		}
	}
	if !found {
		t.Errorf("%v", comps)
	}
}

// M5-01 test: the generation counter is persisted across a restart, over the API too.
func TestGenerationSurvivesAnAPIRestart(t *testing.T) {
	root := t.TempDir()
	g1 := newGW(t, func(o *options) { o.root = root })
	g1.finishSetup()
	gen1 := g1.do("GET", "/state", nil, nil, nil).json(t)["generation"].(float64)
	g1.close()

	g2 := newGW(t, func(o *options) { o.root = root })
	g2.mintToken("full")
	if _, err := g2.e.Barrier(context.Background()); err != nil {
		t.Fatal(err)
	}
	gen2 := g2.do("GET", "/state", nil, nil, nil).json(t)["generation"].(float64)
	if gen2 <= gen1 {
		t.Fatalf("generation did not continue across the restart: %v, then %v", gen1, gen2)
	}
}
