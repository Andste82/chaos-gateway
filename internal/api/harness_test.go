package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/kea"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"go.uber.org/goleak"

	specpkg "github.com/Andste82/chaos-gateway/api"
	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const adminPassword = "correct horse battery staple"

// gw is the API server on a simulated kernel, with a real engine, real stores and the HTTP server
// of net/http/httptest in front. Every response is checked against api/openapi.yaml.
type gw struct {
	t      *testing.T
	k      *kernelsim.Kernel
	ex     *executor.Executor
	st     *store.Store
	sec    *secrets.Store
	au     *auth.Store
	log    *audit.Log
	e      *engine.Engine
	srv    *api.Server
	ts     *httptest.Server
	setup  string // the setup token
	client *http.Client
	router routers.Router
	// token is the bearer token of the default requests, "" for none
	token    string
	authPath string
	dhcp     *fakeDHCP
	// noContract turns the response check off for a call that is known to be outside the spec
	noContract bool
}

type options struct {
	// runner and namespace replace the simulated kernel (the testbed tests)
	runner    executor.Runner
	namespace string
	confirm   time.Duration
	// done skips the setup: the admin exists with adminPassword and revision 1 is the fixture
	done bool
	// serviceNS is the service namespace of the gateway services; resolvers the DNS proxy's upstream
	serviceNS string
	resolvers []netip.Addr
}

func newGW(t *testing.T, opts ...func(*options)) *gw {
	t.Helper()
	o := options{confirm: 2 * time.Second}
	for _, f := range opts {
		f(&o)
	}
	var k *kernelsim.Kernel
	runner := o.runner
	if runner == nil {
		k = kernelsim.New()
		k.AddLink("lan0", "02:00:00:00:00:01", "veth", true)
		k.AddLink("lan1", "02:00:00:00:01:01", "veth", true)
		k.AddLink("wan0", "02:00:00:00:02:01", "veth", true)
		k.SetAddr("wan0", "203.0.113.1/24")
		k.AddLink("mgmt0", "02:00:00:00:03:01", "veth", true)
		k.SetAddr("mgmt0", "192.168.56.1/24")
		k.SetMainDefault("192.168.56.254", "mgmt0")
		k.AddDockerChain()
		runner = k
	}
	root := t.TempDir()
	sec, err := secrets.Open(filepath.Join(root, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	eopts := []executor.Option{executor.WithBirdDir(t.TempDir()), executor.WithKeys(func(id string) (string, string, error) {
		kk, err := sec.WireGuard(id)
		return kk.PrivateKey, kk.PresharedKey, err
	})}
	if k != nil {
		eopts = append(eopts, executor.WithNetnsInode(k.NetnsInode))
	}
	ex, err := executor.New(runner, eopts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	st, err := store.Open(filepath.Join(root, "revisions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	authPath := filepath.Join(root, "secrets", "auth")
	au, err := auth.Open(authPath, auth.WithHashParams(auth.FastHashParams))
	if err != nil {
		t.Fatal(err)
	}
	lg, err := audit.Open(filepath.Join(root, "audit"), &clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	fd := &fakeDHCP{}
	e, err := engine.New(engine.Config{Store: st, Exec: apply.Local{E: ex}, Namespace: o.namespace, Secrets: sec, DHCP: fd, ServiceNS: o.serviceNS})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	srv, err := api.New(api.Config{Engine: e, Store: st, Auth: au, Audit: lg, Secrets: sec, Exec: apply.Local{E: ex}, Namespace: o.namespace, StateDir: filepath.Join(root, "api"),
		BootID: "boot-1", Started: time.Now(), Version: "test", ConfirmTimeout: o.confirm,
		Resolvers: func() []netip.Addr {
			if len(o.resolvers) > 0 {
				return o.resolvers
			}
			return []netip.Addr{netip.MustParseAddr("192.0.2.53")}
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(specpkg.Spec)
	if err != nil {
		t.Fatal(err)
	}
	doc.Servers = openapi3.Servers{{URL: "http://localhost/api/v1"}}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	g := &gw{t: t, k: k, ex: ex, st: st, sec: sec, au: au, log: lg, e: e, srv: srv, ts: ts, client: &http.Client{Jar: jar, Timeout: time.Minute}, router: router, authPath: authPath, dhcp: fd}
	if o.done {
		g.completeSetupDirectly()
	} else {
		tok, err := au.NewSetupToken()
		if err != nil {
			t.Fatal(err)
		}
		g.setup = tok
	}
	return g
}

// completeSetupDirectly does what POST /setup does, without HTTP, and creates an admin token.
func (g *gw) completeSetupDirectly() {
	g.t.Helper()
	g.setup = "unused"
	if _, err := g.au.NewSetupToken(); err != nil {
		g.t.Fatal(err)
	}
	r := g.do("POST", "/setup", fixtureJSON(g.t), map[string]string{"X-Setup-Token": g.mustSetupToken()}, nil)
	_ = r
}

func (g *gw) mustSetupToken() string {
	tok, err := g.au.NewSetupToken()
	if err != nil {
		g.t.Fatal(err)
	}
	return tok
}

// fixture is the WireGuard testbed configuration as the API takes it.
func fixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../compiler/testdata/wireguard.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func fixtureJSON(t *testing.T) any {
	t.Helper()
	return map[string]any{"admin_password": adminPassword, "configuration": fixture(t)}
}

// resp is a response.
type resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r resp) json(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, r.Body)
	}
	return m
}

func (r resp) code(t testing.TB) string {
	t.Helper()
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("status %d is not a problem: %q\n%s", r.Status, r.Header.Get("Content-Type"), r.Body)
	}
	c, _ := r.json(t)["code"].(string)
	return c
}

// do sends a request. body is marshalled to JSON unless it is a string or []byte.
func (g *gw) do(method, path string, body any, headers map[string]string, mod func(*http.Request)) resp {
	g.t.Helper()
	var rdr io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
		ct = "application/json"
	case []byte:
		rdr = bytes.NewReader(b)
		ct = "application/json"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			g.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
		ct = "application/json"
	}
	req, err := http.NewRequest(method, g.ts.URL+"/api/v1"+path, rdr)
	if err != nil {
		g.t.Fatal(err)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	if mod != nil {
		mod(req)
	}
	res, err := g.client.Do(req)
	if err != nil {
		g.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	r := resp{Status: res.StatusCode, Header: res.Header, Body: raw}
	if !g.noContract {
		g.checkContract(method, path, req, r)
	}
	return r
}

// checkContract validates the response against the OpenAPI document: status, headers, content type
// and body schema.
func (g *gw) checkContract(method, path string, sent *http.Request, r resp) {
	g.t.Helper()
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
		return
	}
	u, _ := url.Parse("http://localhost/api/v1" + path)
	vreq, _ := http.NewRequest(method, u.String(), nil)
	route, params, err := g.router.FindRoute(vreq)
	if err != nil {
		g.t.Errorf("%s %s is not in the spec: %v", method, path, err)
		return
	}
	in := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: vreq, PathParams: params, Route: route,
			Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}},
		Status:  r.Status,
		Header:  r.Header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true},
	}
	in.SetBodyBytes(r.Body)
	if err := openapi3filter.ValidateResponse(context.Background(), in); err != nil {
		g.t.Errorf("%s %s → %d does not match the spec: %v\n%s", method, path, r.Status, err, truncate(r.Body))
	}
}

func truncate(b []byte) string {
	if len(b) > 1500 {
		return string(b[:1500]) + "…"
	}
	return string(b)
}

// finishSetup runs POST /setup and logs in with a token: from here the default requests are
// authenticated with an admin token.
func (g *gw) finishSetup() {
	g.t.Helper()
	r := g.do("POST", "/setup", fixtureJSON(g.t), map[string]string{"X-Setup-Token": g.setup}, nil)
	if r.Status != 200 {
		g.t.Fatalf("setup: %d %s", r.Status, r.Body)
	}
	g.mintToken("full")
}

// mintToken creates a token directly in the store and makes it the default.
func (g *gw) mintToken(scope string) string {
	g.t.Helper()
	_, v, err := g.au.CreateToken("test-"+scope, auth.Scope(scope), 0)
	if err != nil {
		g.t.Fatal(err)
	}
	g.token = v
	return v
}

// ready is a gateway whose setup is finished and that has an admin token.
func ready(t *testing.T) *gw {
	t.Helper()
	g := newGW(t)
	g.finishSetup()
	return g
}

func (g *gw) activeID() int64 { return g.st.ActiveID() }

func ifMatch(id int64) map[string]string {
	return map[string]string{"If-Match": `"` + itoa(id) + `"`}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// patch posts a JSON Merge Patch candidate against the active revision.
func (g *gw) patch(doc any) resp {
	g.t.Helper()
	return g.do("POST", "/revisions", doc, map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": `"` + itoa(g.activeID()) + `"`}, nil)
}

func (g *gw) mustPatch(doc any) int64 {
	g.t.Helper()
	r := g.patch(doc)
	if r.Status != 201 {
		g.t.Fatalf("patch: %d %s", r.Status, r.Body)
	}
	return int64(r.json(g.t)["id"].(float64))
}

func (g *gw) apply(id int64) resp {
	g.t.Helper()
	return g.do("POST", "/revisions/"+itoa(id)+"/apply", nil, nil, nil)
}

func auditFilter() audit.Filter { return audit.Filter{} }

// resetPasswordFromOutside does what `chaosgw admin reset-password` does: it opens the auth
// directory in another store object and sets the password.
func resetPasswordFromOutside(t *testing.T, g *gw, pw string) {
	t.Helper()
	cli, err := auth.Open(g.authDir(), auth.WithHashParams(auth.FastHashParams))
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.ResetPassword(pw); err != nil {
		t.Fatal(err)
	}
	// the file's time has to be newer than what the running process has seen
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(filepath.Join(g.authDir(), "auth.json"), later, later)
}

func (g *gw) authDir() string { return g.authPath }

func nowUnix() int64 { return time.Now().Unix() }

// pollWireGuardOnce makes the engine read the WireGuard state once, through its public poller.
func (g *gw) pollWireGuardOnce() {
	g.t.Helper()
	if err := g.e.PollWireGuard(context.Background(), 20*time.Millisecond); err != nil {
		return // already polling
	}
	time.Sleep(300 * time.Millisecond)
}

func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// fakeDHCP is the DHCP server of the API tests: it accepts every configuration and has the leases
// the test gives it.
type fakeDHCP struct {
	mu     sync.Mutex
	leases []kea.Lease
}

func (f *fakeDHCP) Apply(context.Context, *compiler.KeaTarget) error { return nil }

func (f *fakeDHCP) Leases(context.Context) ([]kea.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kea.Lease(nil), f.leases...), nil
}

func (f *fakeDHCP) set(l ...kea.Lease) { f.mu.Lock(); f.leases = l; f.mu.Unlock() }

func context_(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
