package tlsresponder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The interception, end to end on the loopback (plan §2.8, M22): the responder of this package hands the connections to the real
// proxy, mitmproxy with the addon of sidecars/tls-proxy, which fetches its rules from a fake API that is this test, applies them
// and reports what it saw. The device is a Go client that trusts the test CA. Nothing here needs root or a kernel feature: the
// redirect into the service namespace is replaced by the test (the original destination is given to the responder), as in the
// other tests of this package, and what the kernel does is the job of the level 1 tests of internal/api.

const (
	interceptOverlay = "22222222-2222-4222-8222-222222222222"
	interceptDevice  = "33333333-3333-4333-8333-333333333333"
	hostName         = "example.com" // a name the certificate of httptest.Server has
)

// mitmPython finds the interpreter that has mitmproxy; the test is skipped where there is none, unless the environment says that
// the installation is required (CI).
func mitmPython(t *testing.T) string {
	t.Helper()
	py, ok := testbed.MitmPython()
	if !ok {
		if os.Getenv(testbed.RequireMitmEnv) == "1" {
			t.Fatal("mitmproxy is not installed, and this run requires it (" + testbed.RequireMitmEnv + ")")
		}
		t.Skip("mitmproxy is not installed (" + testbed.MitmPythonEnv + " names an interpreter that has it)")
	}
	return py
}

// fakeGateway is the API the proxy talks to: its configuration, and the reports it receives.
type fakeGateway struct {
	srv *httptest.Server

	mu         sync.Mutex
	gen        int64
	rules      []any
	keylog     *bool
	blockQUIC  *bool
	upstream   string            // the policy of the case for the servers' certificates; empty: none given
	others     map[string]string // more interception cases, by overlay, with their policies (the case above is interceptOverlay)
	applied    int64             // the newest generation the proxy has asked to be told about, which it has in use then
	handshakes []model.TlsHandshakeReport
	flows      []map[string]any
	keys       []map[string]any
	polls      int
}

func newFakeGateway(t *testing.T, token string) *fakeGateway {
	t.Helper()
	g := &fakeGateway{gen: 1}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/internal/tls/config", auth(func(w http.ResponseWriter, r *http.Request) {
		var after int64
		_, _ = fmt.Sscan(r.URL.Query().Get("after"), &after)
		g.mu.Lock()
		g.applied = max(g.applied, after)
		g.mu.Unlock()
		if r.URL.Query().Get("consumer") != "proxy" {
			http.Error(w, "the proxy says who it is", http.StatusBadRequest)
			return
		}
		for i := 0; i < 20; i++ {
			g.mu.Lock()
			gen := g.gen
			cfg := g.config()
			g.polls++
			g.mu.Unlock()
			if gen > after {
				_ = json.NewEncoder(w).Encode(cfg)
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	collect := func(into func(raw []json.RawMessage)) http.HandlerFunc {
		return auth(func(w http.ResponseWriter, r *http.Request) {
			var raw []json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			g.mu.Lock()
			into(raw)
			g.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})
	}
	mux.HandleFunc("POST /api/v1/internal/tls/handshakes", collect(func(raw []json.RawMessage) {
		for _, m := range raw {
			var h model.TlsHandshakeReport
			if err := json.Unmarshal(m, &h); err == nil {
				g.handshakes = append(g.handshakes, h)
			}
		}
	}))
	mux.HandleFunc("POST /api/v1/internal/intercept/flows", collect(func(raw []json.RawMessage) {
		for _, m := range raw {
			var f map[string]any
			if err := json.Unmarshal(m, &f); err == nil {
				g.flows = append(g.flows, f)
			}
		}
	}))
	mux.HandleFunc("POST /api/v1/internal/intercept/keylog", collect(func(raw []json.RawMessage) {
		for _, m := range raw {
			var f map[string]any
			if err := json.Unmarshal(m, &f); err == nil {
				g.keys = append(g.keys, f)
			}
		}
	}))
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

// config is the document of GET /internal/tls/config; mu is held.
func (g *fakeGateway) config() map[string]any {
	ic := map[string]any{"http_rules": g.rules}
	if g.keylog != nil {
		ic["keylog"] = *g.keylog
	}
	if g.blockQUIC != nil {
		ic["block_quic"] = *g.blockQUIC
	}
	if g.upstream != "" {
		ic["upstream"] = g.upstream
	}
	cases := []any{map[string]any{
		"case": "intercept", "overlay": interceptOverlay, "sources": []string{"127.0.0.1/32"}, "expected": "any", "intercept": ic,
	}}
	for overlay, policy := range g.others {
		cases = append(cases, map[string]any{
			"case": "intercept", "overlay": overlay, "sources": []string{"127.0.0.1/32"}, "expected": "any", "intercept": map[string]any{"upstream": policy},
		})
	}
	return map[string]any{"generation": g.gen, "cases": cases}
}

// setRules gives the proxy new rules (a new generation of the configuration).
func (g *fakeGateway) setRules(rules ...map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rules = nil
	for _, r := range rules {
		g.rules = append(g.rules, r)
	}
	g.gen++
}

// setUpstream gives the case a policy for the servers' certificates (a new generation); waitApplied waits until the proxy has it.
func (g *fakeGateway) setUpstream(policy string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.upstream = policy
	g.gen++
}

// setOthers gives the proxy more cases, an overlay each with its policy (a new generation).
func (g *fakeGateway) setOthers(others map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.others = others
	g.gen++
}

// waitApplied waits until the proxy asks for what comes after the current generation, which it does once it has that one.
func (g *fakeGateway) waitApplied(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		ok := g.applied >= g.gen
		g.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the proxy has not taken the current configuration")
}

func (g *fakeGateway) snapshot() (hs []model.TlsHandshakeReport, flows, keys []map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append(hs, g.handshakes...), append(flows, g.flows...), append(keys, g.keys...)
}

// waitFlows waits until the proxy has reported n exchanges (more, with the argument false: exactly n is not asserted).
func (g *fakeGateway) waitFlows(t *testing.T, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, fl, _ := g.snapshot(); len(fl) >= n {
			return fl
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, fl, _ := g.snapshot()
	t.Fatalf("%d flows reported, want %d: %v", len(fl), n, fl)
	return nil
}

func (g *fakeGateway) waitHandshakes(t *testing.T, n int, outcome string) []model.TlsHandshakeReport {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		hs, _, _ := g.snapshot()
		var got []model.TlsHandshakeReport
		for _, h := range hs {
			if string(h.Outcome) == outcome {
				got = append(got, h)
			}
		}
		if len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	hs, _, _ := g.snapshot()
	t.Fatalf("handshakes %+v, want %d of %s", hs, n, outcome)
	return nil
}

// interceptRig is the proxy, the responder, the gateway, an upstream server and a device.
type interceptRig struct {
	t       *testing.T
	rig     *rig
	gw      *fakeGateway
	proxy   *testbed.Proxy
	up      *httptest.Server
	upHits  atomic.Int64
	caDir   string
	overlay atomic.Value   // the overlay that decides the connections of the device (a string); interceptOverlay until a test says otherwise
	roots   *x509.CertPool // what the device trusts: the test CA
	control string
	// upstreamCAFile is the CA file the proxy was started with (empty when the installation gave it none)
	upstreamCAFile string
}

type riglOpt struct {
	handler  http.Handler
	mod      func(*Options)
	h2       bool
	proxyDir string
	// upstreamCA says what the installation gives the proxy to verify the servers with (P5-M22-03): "" the certificate of the rig's
	// server (a lab CA file), "none" nothing (the bundle of mitmproxy), "other" a CA that signed no server. upstreamInsecure is
	// CHAOSGW_UPSTREAM_INSECURE=1.
	upstreamCA       string
	upstreamInsecure bool
}

// newInterceptRig starts everything. The upstream server is httptest's TLS server (certificate for example.com), which the proxy
// verifies against that certificate.
func newInterceptRig(t *testing.T, o riglOpt) *interceptRig {
	t.Helper()
	py := mitmPython(t)
	ir := &interceptRig{t: t}
	dir := t.TempDir()
	token := "service-token"
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ir.gw = newFakeGateway(t, token)

	ir.caDir = filepath.Join(dir, "ca")
	if err := EnsureCAs(ir.caDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	cas, err := LoadCAs(ir.caDir)
	if err != nil {
		t.Fatal(err)
	}
	ir.roots = pool(cas.Test)

	handler := o.handler
	if handler == nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ir.upHits.Add(1)
			w.Header().Set("X-Upstream", "yes")
			_, _ = io.WriteString(w, "hello from upstream "+r.URL.Path)
		})
	}
	ir.up = httptest.NewUnstartedServer(handler)
	ir.up.EnableHTTP2 = o.h2
	ir.up.StartTLS()
	t.Cleanup(ir.up.Close)
	upCA := filepath.Join(dir, "upstream-ca.pem")
	if err := os.WriteFile(upCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ir.up.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}

	listen := freeAddr(t)
	ir.overlay.Store(interceptOverlay)
	ir.rig = newRig(t, func(ConnInfo) Decision {
		return Decision{Generation: 5, Overlay: ir.overlay.Load().(string), Device: interceptDevice, Case: &model.TlsCase{Case: model.TlsCaseCaseIntercept}, Expected: "any", Passthrough: true}
	}, func(opt *Options) {
		opt.Proxy = listen
		if o.mod != nil {
			o.mod(opt)
		}
	})
	ir.rig.setDst(netip.MustParseAddrPort(ir.up.Listener.Addr().String()))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ir.rig.srv.ServeControlOn(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	ir.control = "http://" + ln.Addr().String()

	pdir := o.proxyDir
	if pdir == "" {
		pdir = dir
	}
	switch o.upstreamCA {
	case "none":
		upCA = ""
	case "other":
		upCA = filepath.Join(dir, "other-ca.pem")
		if err := os.WriteFile(upCA, cas.Unknown.PEM, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ir.upstreamCAFile = upCA
	ir.proxy, err = testbed.StartProxy(py, testbed.ProxyOptions{Listen: listen, Control: ir.control, API: ir.gw.srv.URL, TokenFile: tokenFile, CADir: ir.caDir, UpstreamCA: upCA, UpstreamInsecure: o.upstreamInsecure, Dir: pdir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ir.proxy.Stop()
		if t.Failed() || testing.Verbose() {
			t.Logf("the proxy wrote:\n%s", ir.proxy.Output())
		}
	})
	if err := ir.proxy.WaitReady(60 * time.Second); err != nil {
		t.Fatal(err)
	}
	return ir
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// device is an HTTP client that reaches the server through the responder, as a device behind the gateway does, and trusts the
// roots it is given.
func (ir *interceptRig) device(roots *x509.CertPool, keylog io.Writer) *http.Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, ir.rig.addr)
		},
		TLSClientConfig:   &tls.Config{RootCAs: roots, KeyLogWriter: keylog},
		ForceAttemptHTTP2: true,
	}
	ir.t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 20 * time.Second}
}

func (ir *interceptRig) get(c *http.Client, path string) (*http.Response, string, error) {
	resp, err := c.Get("https://" + hostName + path)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

func bufioReader(c net.Conn) *bufio.Reader { return bufio.NewReader(c) }

func rule(action string, fields map[string]any) map[string]any {
	r := map[string]any{"action": action}
	for k, v := range fields {
		r[k] = v
	}
	return r
}

func matchPath(prefix string) map[string]any {
	return map[string]any{"path_prefix": prefix}
}

// A device that trusts the test CA gets the server's answer through the proxy, changed by the rules: the status of a rule, a block,
// the headers and the body of a modification. The server is not reached by a request that a rule answers.
func TestADeviceTrustingTheTestCASeesTheModifiedResponses(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.setRules(
		rule("status", map[string]any{"match": matchPath("/fail"), "status": 503}),
		rule("block", map[string]any{"match": matchPath("/secret")}),
		rule("modify", map[string]any{"match": matchPath("/mod"), "modify": map[string]any{
			"request_headers":       map[string]any{"X-Added-By-Rule": "1"},
			"response_headers":      map[string]any{"X-Chaos": "yes", "X-Upstream": ""},
			"response_body_replace": []any{map[string]any{"search": "upstream", "replace": "the gateway"}},
		}}),
	)
	ir.gw.waitApplied(t) // the proxy fetches the configuration once it runs
	dev := ir.device(ir.roots, nil)

	resp, body, err := ir.get(dev, "/plain")
	if err != nil || resp.StatusCode != http.StatusOK || body != "hello from upstream /plain" || resp.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("a request no rule selects passes unchanged: %v %v %q", err, resp, body)
	}
	// the certificate the device saw is the proxy's, made by the test CA for the name it asked for
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 || resp.TLS.PeerCertificates[0].Issuer.CommonName != TestCAName || resp.TLS.PeerCertificates[0].DNSNames[0] != hostName {
		t.Errorf("the device was shown %+v", resp.TLS)
	}

	before := ir.upHits.Load()
	resp, body, err = ir.get(dev, "/fail/now")
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable || body != "" {
		t.Errorf("status: %v %v %q", err, resp, body)
	}
	resp, body, err = ir.get(dev, "/secret/page")
	if err != nil || resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "Blocked by Chaos Gateway") {
		t.Errorf("block: %v %v %q", err, resp, body)
	}
	if ir.upHits.Load() != before {
		t.Errorf("the server was reached by requests that a rule answers: %d -> %d", before, ir.upHits.Load())
	}

	resp, body, err = ir.get(dev, "/mod/x")
	if err != nil || resp.StatusCode != http.StatusOK || body != "hello from the gateway /mod/x" || resp.Header.Get("X-Chaos") != "yes" || resp.Header.Get("X-Upstream") != "" {
		t.Errorf("modify: %v %v %q", err, resp, body)
	}

	// every exchange is reported with what acted on it, and the TLS outcome with the case
	flows := ir.gw.waitFlows(t, 4)
	byPath := map[string]map[string]any{}
	for _, f := range flows {
		byPath[f["path"].(string)] = f
	}
	for path, want := range map[string]struct {
		status  float64
		actions string
	}{"/plain": {200, ""}, "/fail/now": {503, "status"}, "/secret/page": {403, "block"}, "/mod/x": {200, "modify"}} {
		f := byPath[path]
		if f == nil || f["status"] != want.status || f["host"] != hostName || f["method"] != "GET" || f["protocol"] != "http" || f["overlay"] != interceptOverlay ||
			f["client"] != "127.0.0.1" || actionsOf(f) != want.actions {
			t.Errorf("%s: %v", path, f)
		}
	}
	hs := ir.gw.waitHandshakes(t, 1, "accepted")
	if h := hs[0]; h.Case == nil || *h.Case != "intercept" || h.Overlay == nil || h.Overlay.String() != interceptOverlay || h.Sni == nil || *h.Sni != hostName || h.TlsVersion == nil {
		t.Errorf("%+v", h)
	}
	if got := ir.rig.srv.Intercepted(); got < 1 {
		t.Errorf("the responder handed over %d connections", got)
	}
	// the responder reports the hand-over of every connection
	var intercepted int
	for _, r := range ir.rig.sink.wait(t, 1) {
		if r.Outcome == model.TlsHandshakeReportOutcomeIntercepted && r.Case != nil && *r.Case == "intercept" {
			intercepted++
		}
	}
	if intercepted == 0 {
		t.Errorf("no report of a hand-over: %+v", ir.rig.sink.all())
	}
}

// actionsOf is the actions of a reported exchange, joined.
func actionsOf(f map[string]any) string {
	var out []string
	acts, _ := f["actions"].([]any)
	for _, a := range acts {
		out = append(out, a.(string))
	}
	return strings.Join(out, ",")
}

// A delay rule holds the request back for the time it names: the response arrives that much later, and a request that no rule selects
// is as fast as before. Delays of several rules add up, and a delay before a terminal action holds the answer of that action back.
func TestADelayedResponseArrivesAfterTheDelay(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.setRules(
		rule("delay", map[string]any{"match": matchPath("/slow"), "delay": "1s"}),
		rule("delay", map[string]any{"match": matchPath("/lag"), "delay": "700ms"}),
		rule("delay", map[string]any{"match": matchPath("/lag"), "delay": "800ms"}),
		rule("delay", map[string]any{"match": matchPath("/late-503"), "delay": "1200ms"}),
		rule("status", map[string]any{"match": matchPath("/late-503"), "status": 503}),
	)
	ir.gw.waitApplied(t)
	dev := ir.device(ir.roots, nil)
	timed := func(path string) (time.Duration, int) {
		start := time.Now()
		resp, _, err := ir.get(dev, path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return time.Since(start), resp.StatusCode
	}
	timed("/warm") // the handshake and the connection to the server are not what is measured
	fast, _ := timed("/fast")
	for _, c := range []struct {
		path   string
		want   time.Duration
		status int
	}{{"/slow", time.Second, 200}, {"/lag", 1500 * time.Millisecond, 200}, {"/late-503", 1200 * time.Millisecond, 503}} {
		got, status := timed(c.path)
		if status != c.status || got < c.want || got > c.want+1500*time.Millisecond {
			t.Errorf("%s: %s (status %d), want %s plus the usual %s", c.path, got, status, c.want, fast)
		}
	}
	if fast > time.Second {
		t.Errorf("a request that no rule selects took %s", fast)
	}
}

// The requests of a connection that the device keeps open (HTTP/1.1 keep-alive, or the streams of one HTTP/2 connection) are many
// exchanges of one connection: the responder hands over once, the device is shown one certificate, and the proxy reports every exchange.
func TestConnectionReuseAcrossRequestsIsOneHandoverAndManyExchanges(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP/1.1", true: "HTTP/2"}[h2], func(t *testing.T) {
			ir := newInterceptRig(t, riglOpt{h2: h2})
			time.Sleep(300 * time.Millisecond)
			var tr *http.Transport
			dev := ir.device(ir.roots, nil)
			tr = dev.Transport.(*http.Transport)
			if !h2 {
				tr.ForceAttemptHTTP2 = false
			}
			const n = 6
			reused, protos := 0, map[int]int{}
			for i := 0; i < n; i++ {
				req, _ := http.NewRequest(http.MethodGet, "https://"+hostName+fmt.Sprintf("/r%d", i), nil)
				req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) {
					if i.Reused {
						reused++
					}
				}}))
				resp, err := dev.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				protos[resp.ProtoMajor]++
			}
			if reused != n-1 {
				t.Errorf("the connection was reused %d times of %d", reused, n-1)
			}
			if h2 && protos[2] != n || !h2 && protos[1] != n {
				t.Errorf("protocols %v", protos)
			}
			ir.gw.waitFlows(t, n)
			if got := ir.rig.srv.Intercepted(); got != 1 {
				t.Errorf("%d hand-overs", got)
			}
			if hs := ir.gw.waitHandshakes(t, 1, "accepted"); len(hs) != 1 {
				t.Errorf("%d accepted handshakes", len(hs))
			}
			if got := ir.upHits.Load(); got != n {
				t.Errorf("the server saw %d requests", got)
			}
		})
	}
}

// The TLS key log: the proxy gives the gateway the secrets of the connection with the device, attributed to the device's address, and
// they are the secrets the device itself holds (a Go client writes its own log), so Wireshark can decrypt the capture of the device with them.
func TestTheKeyLogHoldsTheSecretsOfTheDevicesConnection(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.waitApplied(t)
	var own lockedWriter
	dev := ir.device(ir.roots, &own)
	if _, _, err := ir.get(dev, "/secret-material"); err != nil {
		t.Fatal(err)
	}
	dev.Transport.(*http.Transport).CloseIdleConnections() // the connection ends, the proxy flushes what is left
	deviceLines := nonEmptyLines(own.String())
	if len(deviceLines) < 3 {
		t.Fatalf("the device wrote %d lines", len(deviceLines))
	}
	deadline := time.Now().Add(10 * time.Second)
	var have map[string]bool
	for time.Now().Before(deadline) {
		have = map[string]bool{}
		_, _, keys := ir.gw.snapshot()
		for _, k := range keys {
			if k["client"] != "127.0.0.1" || k["overlay"] != interceptOverlay {
				t.Fatalf("a batch of the wrong client or overlay: %v", k)
			}
			for _, l := range k["lines"].([]any) {
				have[l.(string)] = true
			}
		}
		missing := 0
		for _, l := range deviceLines {
			if !have[l] {
				missing++
			}
		}
		if missing == 0 {
			// and the server side of the proxy: the lines of its own connection are there too (more than the device's)
			if len(have) <= len(deviceLines) {
				t.Errorf("the proxy gave %d lines, the device holds %d: the secrets of the proxy's connection to the server are missing", len(have), len(deviceLines))
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the secrets of the device's connection are not all in the key log of the proxy:\ndevice:\n%s\nproxy:\n%v", strings.Join(deviceLines, "\n"), have)
}

type lockedWriter struct {
	mu sync.Mutex
	b  []byte
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	return len(p), nil
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.b)
}

func nonEmptyLines(s string) (out []string) {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// keylog: false of the case keeps the secrets from the captures: the proxy gives the gateway none.
func TestACaseThatTurnsTheKeyLogOffGivesTheGatewayNoSecrets(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	off := false
	ir.gw.mu.Lock()
	ir.gw.keylog = &off
	ir.gw.gen++
	ir.gw.mu.Unlock()
	ir.gw.waitApplied(t)
	dev := ir.device(ir.roots, nil)
	if _, _, err := ir.get(dev, "/x"); err != nil {
		t.Fatal(err)
	}
	dev.Transport.(*http.Transport).CloseIdleConnections()
	ir.gw.waitFlows(t, 1)
	time.Sleep(1500 * time.Millisecond)
	if _, _, keys := ir.gw.snapshot(); len(keys) != 0 {
		t.Errorf("%d batches of secrets: %v", len(keys), keys)
	}
}

// A device that does not trust the test CA rejects the certificate of the proxy: the proxy reports the rejection with the alert, as the
// responder does for its certificate cases, and nothing reaches the server.
func TestADeviceThatDoesNotTrustTheTestCARejectsTheProxysCertificate(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.waitApplied(t)
	dev := ir.device(x509.NewCertPool(), nil)
	if _, _, err := ir.get(dev, "/x"); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("the device took the certificate: %v", err)
	}
	hs := ir.gw.waitHandshakes(t, 1, "rejected")
	_ = hs
	if h := hs[0]; h.Case == nil || *h.Case != "intercept" || h.Alert == nil || *h.Alert == "" || h.Sni == nil || *h.Sni != hostName {
		t.Errorf("%+v", h)
	}
	if ir.upHits.Load() != 0 {
		t.Error("a request reached the server")
	}
}

// A throttle rule paces the bytes towards the device from the response it matches, and only for the response of the request it matched:
// the next request on the same connection is not paced.
func TestAThrottleRulePacesTheResponseOfTheRequestItMatches(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef"), 2500) // 40000 bytes
	ir := newInterceptRig(t, riglOpt{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/big") {
			_, _ = w.Write(big)
			return
		}
		_, _ = io.WriteString(w, "small")
	})})
	ir.gw.setRules(rule("throttle", map[string]any{"match": matchPath("/big"), "rate": "160kbit"})) // 20000 bytes/s
	ir.gw.waitApplied(t)
	dev := ir.device(ir.roots, nil)
	dev.Transport.(*http.Transport).ForceAttemptHTTP2 = false
	start := time.Now()
	_, body, err := ir.get(dev, "/small")
	if err != nil || body != "small" {
		t.Fatal(err, body)
	}
	unpaced := time.Since(start)
	start = time.Now()
	_, body, err = ir.get(dev, "/big")
	paced := time.Since(start)
	if err != nil || len(body) != len(big) {
		t.Fatalf("%v, %d bytes", err, len(body))
	}
	// 40000 bytes at 20000 bytes/s: about 2 s (the first piece goes at once)
	if paced < 1500*time.Millisecond || paced > 4*time.Second {
		t.Errorf("the paced response took %s, the unpaced one %s", paced, unpaced)
	}
	start = time.Now()
	if _, _, err := ir.get(dev, "/small"); err != nil {
		t.Fatal(err)
	}
	if after := time.Since(start); after > time.Second {
		t.Errorf("the next request on the connection is still paced: %s", after)
	}
}

// An abort rule resets the connection of the device (not an orderly close) and the exchange is reported as aborted.
func TestAnAbortRuleResetsTheConnectionOfTheDevice(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.setRules(rule("abort", map[string]any{"match": matchPath("/abort")}))
	ir.gw.waitApplied(t)
	dev := ir.device(ir.roots, nil)
	if _, _, err := ir.get(dev, "/fine"); err != nil {
		t.Fatal(err)
	}
	_, _, err := ir.get(dev, "/abort/now")
	if err == nil || (!strings.Contains(err.Error(), "reset") && !strings.Contains(err.Error(), "EOF")) {
		t.Fatalf("the connection was not broken: %v", err)
	}
	var fl map[string]any
	for _, f := range ir.gw.waitFlows(t, 2) {
		if f["path"] == "/abort/now" {
			fl = f
		}
	}
	if fl == nil || fl["error"] != "aborted by a rule" || actionsOf(fl) != "abort" || fl["status"] != nil {
		t.Errorf("%v", fl)
	}
	if got := ir.rig.srv.Aborted(); got < 1 {
		t.Errorf("the responder reset %d connections on the proxy's request (the device retries a GET that failed on a reused connection, so more than one is fine)", got)
	}
	// the device can connect again, and the next request on the new connection is served
	if _, body, err := ir.get(dev, "/again"); err != nil || body != "hello from upstream /again" {
		t.Errorf("%v %q", err, body)
	}
}

// A connection that is no HTTP (a protocol over TLS, here a line echo) is an exchange of the kind tcp: its bytes are counted, and the rules
// that name only a host act on it: abort resets it.
func TestAConnectionThatIsNoHTTPIsCountedAndAbortedByAHostRule(t *testing.T) {
	var echo net.Listener
	ir := newInterceptRig(t, riglOpt{})
	// a TLS server that echoes lines, on the port the upstream server of the rig does not use
	echo = tlsEchoServer(t, ir.up.TLS.Certificates[0])
	ir.rig.setDst(netip.MustParseAddrPort(echo.Addr().String()))
	ir.gw.setRules()
	ir.gw.waitApplied(t)
	// the proxy trusts the echo server's certificate through the CA file of the rig (the same certificate)
	conn := func() (*tls.Conn, error) {
		raw, err := net.Dial("tcp", ir.rig.addr)
		if err != nil {
			return nil, err
		}
		c := tls.Client(raw, &tls.Config{RootCAs: ir.roots, ServerName: hostName})
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		return c, c.Handshake()
	}
	c, err := conn()
	if err != nil {
		t.Fatal(err)
	}
	// binary frames, like a protocol of devices (MQTT, say) sends them; text that looks like an HTTP request line would be taken for HTTP
	for i := 0; i < 3; i++ {
		frame := []byte{0x10 + byte(i), 0x05, 'h', 'e', 'l', 'l', 'o'}
		if _, err := c.Write(frame); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(frame))
		if _, err := io.ReadFull(c, buf); err != nil || !bytes.Equal(buf, frame) {
			t.Fatalf("%v %x", err, buf)
		}
	}
	_ = c.Close()
	flows := ir.gw.waitFlows(t, 1)
	if f := flows[0]; f["protocol"] != "tcp" || f["host"] != hostName || f["request_bytes"] != float64(21) || f["response_bytes"] != float64(21) || f["overlay"] != interceptOverlay {
		t.Errorf("%v", f)
	}

	ir.gw.setRules(rule("abort", map[string]any{"match": map[string]any{"host": hostName}}))
	ir.gw.waitApplied(t)
	c, err = conn()
	if err == nil {
		_, _ = c.Write([]byte{0x10, 0x02, 0x00, 0x00})
		buf := make([]byte, 4)
		_, err = io.ReadFull(c, buf)
	}
	if err == nil {
		t.Fatal("the connection was not aborted")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, fl, _ := ir.gw.snapshot(); len(fl) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, fl, _ := ir.gw.snapshot()
	if len(fl) < 2 || fl[1]["error"] != "aborted by a rule" || actionsOf(fl[1]) != "abort" {
		t.Errorf("%v", fl)
	}
}

func tlsEchoServer(t *testing.T, cert tls.Certificate) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln
}

// A WebSocket is inspected like the other exchanges: the handshake is an HTTP exchange the rules can act on, the messages are counted, and a
// rule that blocks the handshake keeps the socket from opening.
func TestAWebSocketIsInspectedAndItsHandshakeCanBeBlocked(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/ws", websocket.Handler(func(c *websocket.Conn) { _, _ = io.Copy(c, c) }))
	ir := newInterceptRig(t, riglOpt{handler: mux})
	ir.gw.setRules(rule("block", map[string]any{"match": matchPath("/ws-blocked")}))
	ir.gw.waitApplied(t)

	dial := func(path string) (*websocket.Conn, error) {
		cfg, err := websocket.NewConfig("wss://"+hostName+path, "https://"+hostName)
		if err != nil {
			return nil, err
		}
		raw, err := net.Dial("tcp", ir.rig.addr)
		if err != nil {
			return nil, err
		}
		tc := tls.Client(raw, &tls.Config{RootCAs: ir.roots, ServerName: hostName})
		if err := tc.Handshake(); err != nil {
			return nil, err
		}
		return websocket.NewClient(cfg, tc)
	}
	c, err := dial("/ws")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"one", "two", "three"} {
		if err := websocket.Message.Send(c, m); err != nil {
			t.Fatal(err)
		}
		var got string
		if err := websocket.Message.Receive(c, &got); err != nil || got != m {
			t.Fatalf("%v %q", err, got)
		}
	}
	_ = c.Close()
	var ws map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for ws == nil && time.Now().Before(deadline) {
		_, fl, _ := ir.gw.snapshot()
		for _, f := range fl {
			if f["protocol"] == "websocket" {
				ws = f
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ws == nil || ws["path"] != "/ws" || ws["messages"] != float64(6) || ws["host"] != hostName {
		t.Errorf("%v", ws)
	}
	if _, err := dial("/ws-blocked"); err == nil {
		t.Error("a blocked handshake opened a socket")
	}
}

// A connection that is no TLS (plain HTTP on a port the case names) is handed to the proxy as well, and the rules act on it.
func TestPlainHTTPOnAPortOfTheCaseIsHandedOverToo(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "plain upstream") }))
	t.Cleanup(plain.Close)
	ir := newInterceptRig(t, riglOpt{})
	ir.rig.setDst(netip.MustParseAddrPort(plain.Listener.Addr().String()))
	ir.gw.setRules(rule("status", map[string]any{"match": matchPath("/down"), "status": 500}))
	ir.gw.waitApplied(t)
	req := func(path string) (int, string) {
		c, err := net.Dial("tcp", ir.rig.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: example.org\r\nConnection: close\r\n\r\n", path)
		resp, err := http.ReadResponse(bufioReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if st, body := req("/up"); st != 200 || body != "plain upstream" {
		t.Errorf("%d %q", st, body)
	}
	if st, _ := req("/down/x"); st != 500 {
		t.Errorf("%d", st)
	}
	fl := ir.gw.waitFlows(t, 2)
	if fl[0]["host"] != "example.org" || fl[0]["protocol"] != "http" {
		t.Errorf("%v", fl)
	}
}

// The test CA is replaced (POST /tls/ca/regenerate): the launcher notices the new files and starts mitmproxy again with them, so the devices
// that installed the new certificate are served and those that kept the old one reject what they are shown.
func TestANewTestCAIsUsedByTheProxyAfterItsFilesChange(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.waitApplied(t)
	if _, _, err := ir.get(ir.device(ir.roots, nil), "/before"); err != nil {
		t.Fatal(err)
	}
	if _, err := RegenerateTestCA(ir.caDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	cas, err := LoadCAs(ir.caDir)
	if err != nil {
		t.Fatal(err)
	}
	fresh := pool(cas.Test)
	deadline := time.Now().Add(40 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if _, _, last = ir.get(ir.device(fresh, nil), "/after"); last == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if last != nil {
		t.Fatalf("the proxy does not use the new CA: %v\n%s", last, ir.proxy.Output())
	}
	if _, _, err := ir.get(ir.device(ir.roots, nil), "/old"); err == nil {
		t.Error("a device that trusts the old CA took the certificate of the new one")
	}
}

// With the real proxy: a server that is down is a reset for the device and `server_unreachable` for the gateway, and the proxy is no less
// healthy for it: the next connection to a server that is up is served.
func TestAServerThatIsDownIsAResetForTheDeviceAndReportedAsUnreachable(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.waitApplied(t)
	up := ir.rig.getDst()
	ir.rig.setDst(netip.MustParseAddrPort(freeAddr(t)))
	dev := ir.device(ir.roots, nil)
	if _, _, err := ir.get(dev, "/down"); err == nil {
		t.Fatal("a request to a server that is down was answered")
	}
	deadline := time.Now().Add(10 * time.Second)
	var got bool
	for time.Now().Before(deadline) && !got {
		for _, r := range ir.rig.sink.wait(t, 1) {
			got = got || r.Outcome == model.TlsHandshakeReportOutcomeServerUnreachable
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !got {
		t.Errorf("no report of an unreachable server: %+v", ir.rig.sink.all())
	}
	if ir.rig.srv.ProxyFailures() != 0 {
		t.Errorf("the proxy is counted as failed: %d", ir.rig.srv.ProxyFailures())
	}
	ir.rig.setDst(up)
	if _, body, err := ir.get(ir.device(ir.roots, nil), "/up"); err != nil || body != "hello from upstream /up" {
		t.Errorf("%v %q", err, body)
	}
}

// A device that connects to an address and sends no server name is served with a certificate for the address (the proxy takes the names of the
// real server's certificate, which here has the address), and the exchange is reported with the address as its host.
func TestAConnectionWithoutAServerNameIsInterceptedForTheAddress(t *testing.T) {
	ir := newInterceptRig(t, riglOpt{})
	ir.gw.waitApplied(t)
	host := ir.rig.getDst().Addr().String()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, ir.rig.addr)
		},
		TLSClientConfig: &tls.Config{RootCAs: ir.roots},
	}
	t.Cleanup(tr.CloseIdleConnections)
	resp, err := (&http.Client{Transport: tr, Timeout: 20 * time.Second}).Get("https://" + host + ":" + fmt.Sprint(ir.rig.getDst().Port()) + "/by-address")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil || resp.TLS.PeerCertificates[0].Issuer.CommonName != TestCAName {
		t.Errorf("%d %+v", resp.StatusCode, resp.TLS)
	}
	flows := ir.gw.waitFlows(t, 1)
	if flows[0]["host"] != host || flows[0]["path"] != "/by-address" {
		t.Errorf("%v", flows[0])
	}
	if hs := ir.gw.waitHandshakes(t, 1, "accepted"); hs[0].Sni != nil {
		t.Errorf("a connection without a server name has none in its report: %+v", hs[0])
	}
}
