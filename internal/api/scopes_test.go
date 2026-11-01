package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	specpkg "github.com/Andste82/chaos-gateway/api"
)

// M29: the review of the token scopes (plan §2.15, §2.16). The spec names the least scope of every operation
// (`x-required-scope`); these tests hold the guard to it for every operation of the spec, whatever the handler does, and pin
// the result of the review: which operations are above `read`, so that a new operation has to be put in a class on purpose.

type specOperation struct {
	ID, Method, Path, Scope string
	Internal, Public        bool
}

func allSpecOperations(t *testing.T) []specOperation {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(specpkg.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	var out []specOperation
	for path, item := range spec.Paths {
		for key, node := range item {
			m := strings.ToUpper(key)
			if m != "GET" && m != "POST" && m != "PUT" && m != "DELETE" && m != "PATCH" {
				continue
			}
			var o struct {
				ID       string                 `yaml:"operationId"`
				Scope    string                 `yaml:"x-required-scope"`
				Internal bool                   `yaml:"x-internal"`
				Security *[]map[string][]string `yaml:"security"`
			}
			if err := node.Decode(&o); err != nil {
				t.Fatal(err)
			}
			out = append(out, specOperation{ID: o.ID, Method: m, Path: path, Scope: o.Scope, Internal: o.Internal, Public: o.Security != nil})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path+out[i].Method < out[j].Path+out[j].Method })
	return out
}

// allowed is the rule of plan §2.15: `full` includes `overlays`, which includes `read`; `service` is for /internal alone.
func allowed(have, need string) bool {
	rank := map[string]int{"read": 1, "overlays": 2, "full": 3}
	switch {
	case need == "none":
		return true
	case need == "service" || have == "service":
		return have == need
	}
	return rank[have] >= rank[need]
}

func TestEveryOperationRefusesACallerWithoutTheScopeItNeeds(t *testing.T) {
	g := ready(t)
	tokens := map[string]string{}
	for _, s := range []string{"read", "overlays", "full"} {
		tokens[s] = g.mustTokenOf(s)
	}
	file := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(file); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	tokens["service"] = strings.TrimSpace(string(raw))

	g.badRequest = true // the bodies are empty: the point is who may call, not what the call does
	defer func() { g.badRequest = false }()
	checked := 0
	for _, o := range allSpecOperations(t) {
		if o.Scope == "" {
			t.Errorf("%s %s has no x-required-scope", o.Method, o.Path)
			continue
		}
		var body any
		if o.Method == http.MethodPost || o.Method == http.MethodPut || o.Method == http.MethodPatch {
			body = map[string]any{}
		}
		call := func(token string) resp {
			g.token = token
			return g.do(o.Method, fill(o.Path), body, map[string]string{"If-Match": `"1"`}, nil)
		}
		name := o.Method + " " + o.Path
		if o.Public {
			continue // the setup, the login and the health check: no credential, tested on their own
		}
		checked++
		if r := call(""); r.Status != 401 {
			t.Errorf("%s without a credential: %d", name, r.Status)
		}
		if r := call("cgw_not-a-token"); r.Status != 401 {
			t.Errorf("%s with an unknown token: %d", name, r.Status)
		}
		for have, tok := range tokens {
			if o.Method == http.MethodGet && (o.Path == "/events" || strings.HasSuffix(o.Path, "/events")) && allowed(have, o.Scope) {
				continue // a stream that stays open: its scope is checked by the refusals below
			}
			r := call(tok)
			scopeRefused := r.Status == 403 && strings.Contains(string(r.Body), "needs the scope")
			if allowed(have, o.Scope) {
				if r.Status == 401 || scopeRefused {
					t.Errorf("%s with the scope %s (needs %s): refused, %d %s", name, have, o.Scope, r.Status, truncate(r.Body))
				}
				continue
			}
			if r.Status != 403 || r.code(t) != "forbidden" {
				t.Errorf("%s with the scope %s (needs %s): %d %s", name, have, o.Scope, r.Status, truncate(r.Body))
			}
		}
	}
	if checked < 90 {
		t.Errorf("only %d operations were checked", checked)
	}
}

// The result of the review of 2026-10-29: the operations above `read`. A new operation is `read` unless it is added here on
// purpose, and the spec's table of scopes says the same in words.
var (
	// writes of the test: overlays, runs, captures, diagnostics, probe calibration, own reset
	overlaysOperations = []string{"calibrateProbe", "createOverlay", "createRun", "deleteCapture", "deleteOverlay", "renewOverlay", "renewRun",
		"reset", "runDiagnostic", "runScenario", "startCapture", "stopCapture", "abortRun"}
	// the configuration, the tokens, the password and everything that hands out a secret or changes who can reach the gateway
	fullOperations = []string{"applyRevision", "changePassword", "cloneRevision", "confirmRevision", "createRevision", "createToken", "deleteToken",
		"deleteWireGuardClientPrivateKey", "discardRevision", "exportWireGuardClient", "exportWireGuardNetwork", "listTokens", "regenerateTestCa",
		"replaceCertificate", "resetCertificate", "rollbackRevision"}
	// operations that change nothing and are POSTs only because they carry a body
	readPosts = []string{"logout", "previewRevision"}
)

func TestTheScopesOfTheOperationsAreTheResultOfTheReview(t *testing.T) {
	// the scopes the spec uses are exactly the ones the code knows: an unknown one would be no requirement at all
	used := map[string]bool{}
	for _, o := range allSpecOperations(t) {
		used[o.Scope] = true
	}
	var usedList []string
	for s := range used {
		usedList = append(usedList, s)
	}
	sort.Strings(usedList)
	if want := "full,none,overlays,read,service"; strings.Join(usedList, ",") != want {
		t.Errorf("the scopes of the spec are %v, the code knows %s", usedList, want)
	}
	got := map[string][]string{}
	for _, o := range allSpecOperations(t) {
		if o.Public || o.Internal {
			continue
		}
		switch {
		case o.Scope == "read" && o.Method == http.MethodGet:
		case o.Scope == "read":
			got["read"] = append(got["read"], o.ID)
		default:
			got[o.Scope] = append(got[o.Scope], o.ID)
		}
	}
	for scope, want := range map[string][]string{"overlays": overlaysOperations, "full": fullOperations, "read": readPosts} {
		a, b := append([]string{}, got[scope]...), append([]string{}, want...)
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, ",") != strings.Join(b, ",") {
			t.Errorf("the operations of the scope %s are\n  %v\nthe review decided\n  %v", scope, a, b)
		}
	}
	// the operations without a credential: a spec edit that adds `security: []` to a sensitive operation fails here
	var public []string
	for _, o := range allSpecOperations(t) {
		if o.Public {
			public = append(public, o.ID)
			if o.Scope != "none" {
				t.Errorf("%s is public and needs %q, not none", o.ID, o.Scope)
			}
		} else if o.Scope == "none" {
			t.Errorf("%s %s needs no scope and is not public", o.Method, o.Path)
		}
	}
	sort.Strings(public)
	if want := "completeSetup,getHealth,getSetup,login"; strings.Join(public, ",") != want {
		t.Errorf("the operations without a credential are %v, the review decided %s", public, want)
	}
	for _, o := range allSpecOperations(t) {
		switch {
		case o.Internal && o.Scope != "service":
			t.Errorf("%s %s is internal and needs %q", o.Method, o.Path, o.Scope)
		case !o.Internal && o.Scope == "service":
			t.Errorf("%s %s needs the service scope and is not internal", o.Method, o.Path)
		case o.Internal && !strings.HasPrefix(o.Path, "/internal/"):
			t.Errorf("%s %s is internal outside /internal", o.Method, o.Path)
		case strings.HasPrefix(o.Path, "/internal/") && !o.Internal:
			t.Errorf("%s %s is under /internal and not marked internal", o.Method, o.Path)
		}
	}
}

// The scopes decide inside a handler too: a token of the scope `overlays` that asks for what only `full` may see gets a 403 there.
func TestSecretsAreForTheFullScopeInsideTheHandlersToo(t *testing.T) {
	g := ready(t)
	for _, scope := range []string{"read", "overlays"} {
		g.token = g.mustTokenOf(scope)
		for _, path := range []string{"/revisions/1/export?include_secrets=true", "/reset?owner=all"} {
			method := "GET"
			if strings.HasPrefix(path, "/reset") {
				method = "POST"
			}
			if r := g.do(method, path, nil, nil, nil); r.Status != 403 {
				t.Errorf("%s %s with the scope %s: %d", method, path, scope, r.Status)
			}
		}
	}
}
