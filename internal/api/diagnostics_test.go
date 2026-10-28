package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/diag"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// M24 tests: the diagnostics over the API against known targets (servers on the loopback of the test, the TLS responder's
// certificates, a DNS server), the failure cases, the guard around the operation and the capabilities. The same
// diagnostics against the namespaces of the testbed are in e2e_diagnostics_test.go.

const loopback = "127.0.0.1"

// diagGateway is a ready gateway whose diagnostics ask the DNS servers.
func diagGateway(t *testing.T, servers ...netip.AddrPort) *gw {
	t.Helper()
	g := newGW(t, func(o *options) {
		o.diagnostics = api.DiagnosticsConfig{Servers: func() []netip.AddrPort { return servers }, Proxy: firstOr(servers)}
	})
	g.finishSetup()
	return g
}

func firstOr(a []netip.AddrPort) netip.AddrPort {
	if len(a) > 0 {
		return a[0]
	}
	return netip.AddrPort{}
}

func (g *gw) diagnose(body any) resp {
	g.t.Helper()
	return g.do("POST", "/diagnostics", body, nil, nil)
}

// diagnoseOK runs a diagnostic that has to answer 200.
func (g *gw) diagnoseOK(body any) map[string]any {
	g.t.Helper()
	r := g.diagnose(body)
	if r.Status != 200 {
		g.t.Fatalf("%d %s", r.Status, r.Body)
	}
	return r.json(g.t)
}

func sub(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func listenTCPServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func dnsServer(t *testing.T, answer string) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp", loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	h := dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		m := new(dns.Msg)
		if q.Question[0].Name == "gone.test." {
			m.SetRcode(q, dns.RcodeNameError)
		} else {
			m.SetReply(q)
			m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(answer).To4()})
		}
		_ = w.WriteMsg(m)
	})
	srv := &dns.Server{PacketConn: pc, Handler: h}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}

// responderServer serves what the TLS responder shows for a case, with the certificates its issuer makes from the gateway's CAs.
func responderServer(t *testing.T, g *gw, c model.TlsCaseCase, name string) int {
	t.Helper()
	dir := filepath.Join(g.root, "tlsca")
	cas, err := tlsresponder.LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	iss, err := tlsresponder.NewIssuer(cas, realClock{})
	if err != nil {
		t.Fatal(err)
	}
	certs, err := iss.Certificates(c, name)
	if err != nil {
		t.Fatal(err)
	}
	return serveCerts(t, certs...)
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func serveCerts(t *testing.T, certs ...tls.Certificate) int {
	t.Helper()
	l, err := tls.Listen("tcp", loopback+":0", &tls.Config{Certificates: certs})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.(*tls.Conn).Handshake()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestEveryTypeOfDiagnosticRunsAgainstItsTarget(t *testing.T) {
	g := diagGateway(t, dnsServer(t, "203.0.113.10"))
	tcpPort := listenTCPServer(t)

	// tcp
	r := g.diagnoseOK(map[string]any{"type": "tcp", "host": loopback, "port": tcpPort})
	if r["ok"] != true || r["type"] != "tcp" || r["from"] != "gateway" || sub(r, "connect")["reachable"] != true || r["started_at"] == nil {
		t.Errorf("tcp: %v", r)
	}
	// udp: an echo
	pc, err := net.ListenPacket("udp", loopback+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pc.Close() }()
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	r = g.diagnoseOK(map[string]any{"type": "udp", "host": loopback, "port": pc.LocalAddr().(*net.UDPAddr).Port, "payload": "hello"})
	if r["ok"] != true || sub(r, "connect")["detail"] != "reply of 5 bytes" {
		t.Errorf("udp: %v", r)
	}
	// dns, asking a server and asking the proxy
	r = g.diagnoseOK(map[string]any{"type": "dns", "host": loopback, "dns_name": "broker.example.com", "port": int(dnsServer(t, "203.0.113.77").Port())})
	if r["ok"] != true || fmt.Sprint(sub(r, "dns")["answers"]) != "[203.0.113.77]" || sub(r, "dns")["rcode"] != "NOERROR" {
		t.Errorf("dns: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "dns", "host": "broker.example.com"})
	if r["ok"] != true || fmt.Sprint(sub(r, "dns")["answers"]) != "[203.0.113.10]" {
		t.Errorf("dns by the proxy: %v", r)
	}
	// http
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Test", "1") }))
	defer web.Close()
	r = g.diagnoseOK(map[string]any{"type": "http", "host": "x", "url": web.URL + "/health"})
	if r["ok"] != true || sub(r, "http")["status"] != float64(200) || sub(sub(r, "http"), "headers")["X-Test"] != "1" {
		t.Errorf("http: %v", r)
	}
	// a name resolved by the configured server, then used
	r = g.diagnoseOK(map[string]any{"type": "tcp", "host": "broker.example.com", "port": tcpPort, "timeout": "300ms"})
	if r["ok"] != false || r["address"] != "203.0.113.10" {
		t.Errorf("tcp to a resolved name: %v", r)
	}
}

func TestDiagnosticsReportTheTLSCasesOfTheResponder(t *testing.T) {
	g := diagGateway(t)
	for _, c := range []struct {
		kase model.TlsCaseCase
		err  string
	}{
		{model.TlsCaseCaseExpired, "certificate has expired"},
		{model.TlsCaseCaseNotYetValid, "certificate is not yet valid"},
		{model.TlsCaseCaseUntrustedCa, "certificate signed by unknown authority"},
		{model.TlsCaseCaseSelfSigned, "certificate signed by unknown authority"},
		{model.TlsCaseCaseHostnameMismatch, "certificate is valid for " + tlsresponder.MismatchName + ", not broker.example.com"},
	} {
		port := responderServer(t, g, c.kase, "broker.example.com")
		r := g.diagnoseOK(map[string]any{"type": "tls", "host": loopback, "port": port, "sni": "broker.example.com"})
		tl := sub(r, "tls")
		if r["ok"] != false || r["error"] != c.err || tl["verified"] != false || tl["verify_error"] != c.err || len(tl["certificates"].([]any)) != 1 {
			t.Errorf("%s: %v", c.kase, r)
		}
		// the same case as the path: DNS ✓ Route ✓ TCP ✓ TLS ✗ (certificate expired)
		r = g.diagnoseOK(map[string]any{"type": "path", "host": loopback, "port": port, "sni": "broker.example.com"})
		steps := r["path"].([]any)
		last := steps[len(steps)-1].(map[string]any)
		if r["ok"] != false || len(steps) != 4 || last["step"] != "tls" || last["ok"] != false || last["detail"] != c.err || r["error"] != "tls: "+c.err {
			t.Errorf("%s path: %v", c.kase, r)
		}
	}
}

func TestDiagnosticsTrustTheTestCAAndNotTheUnknownOne(t *testing.T) {
	g := diagGateway(t)
	cas, err := tlsresponder.LoadCAs(filepath.Join(g.root, "tlsca"))
	if err != nil {
		t.Fatal(err)
	}
	leaf := func(ca *tlsresponder.CA) int {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "broker.example.com"}, DNSNames: []string{"broker.example.com"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
		if err != nil {
			t.Fatal(err)
		}
		return serveCerts(t, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})
	}
	r := g.diagnoseOK(map[string]any{"type": "tls", "host": loopback, "port": leaf(cas.Test), "sni": "broker.example.com"})
	if r["ok"] != true || sub(r, "tls")["verified"] != true || r["error"] != nil {
		t.Errorf("signed by the test CA: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "tls", "host": loopback, "port": leaf(cas.Unknown), "sni": "broker.example.com"})
	if r["ok"] != false || r["error"] != "certificate signed by unknown authority" {
		t.Errorf("signed by the unknown CA: %v", r)
	}
}

func TestDiagnosticsOfATargetThatFailsAreAnswersNotErrors(t *testing.T) {
	g := diagGateway(t, dnsServer(t, "203.0.113.10"))
	closed := freeTCPPort(t)
	for name, c := range map[string]struct {
		body map[string]any
		err  string
	}{
		"tcp refused":     {map[string]any{"type": "tcp", "host": loopback, "port": closed}, "connection refused"},
		"tcp timeout":     {map[string]any{"type": "tcp", "host": "203.0.113.10", "port": 8883, "timeout": "100ms"}, ""},
		"udp refused":     {map[string]any{"type": "udp", "host": loopback, "port": closed}, "connection refused"},
		"dns nxdomain":    {map[string]any{"type": "dns", "host": "gone.test"}, "NXDOMAIN"},
		"http refused":    {map[string]any{"type": "http", "host": "x", "url": fmt.Sprintf("http://%s:%d/", loopback, closed)}, "connection refused"},
		"tls refused":     {map[string]any{"type": "tls", "host": loopback, "port": closed}, "cannot connect: connection refused"},
		"path refused":    {map[string]any{"type": "path", "host": loopback, "port": closed}, "tcp: connection refused"},
		"name unresolved": {map[string]any{"type": "tcp", "host": "gone.test", "port": 80}, "cannot resolve gone.test: NXDOMAIN"},
		"ping unresolved": {map[string]any{"type": "ping", "host": "gone.test"}, "cannot resolve gone.test: NXDOMAIN"},
		"path unresolved": {map[string]any{"type": "path", "host": "gone.test", "port": 80}, "dns: cannot resolve gone.test: NXDOMAIN"},
		"tls unresolved":  {map[string]any{"type": "tls", "host": "gone.test"}, "cannot resolve gone.test: NXDOMAIN"},
		"http unresolved": {map[string]any{"type": "http", "host": "x", "url": "http://gone.test/"}, "cannot resolve gone.test: NXDOMAIN"},
	} {
		r := g.diagnoseOK(c.body)
		if r["ok"] != false || r["error"] == nil || (c.err != "" && r["error"] != c.err) {
			t.Errorf("%s: %v", name, r)
		}
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer web.Close()
	r := g.diagnoseOK(map[string]any{"type": "http", "host": "x", "url": web.URL})
	if r["ok"] != false || sub(r, "http")["status"] != float64(503) || r["error"] != "HTTP 503 Service Unavailable" {
		t.Errorf("http 503: %v", r)
	}
}

func TestPingOverTheAPI(t *testing.T) {
	g := diagGateway(t)
	r := g.diagnoseOK(map[string]any{"type": "ping", "host": loopback, "count": 2, "timeout": "2s"})
	if r["ok"] == false && strings.Contains(fmt.Sprint(r["error"]), "cannot open an ICMP socket") {
		t.Skipf("this process cannot open an ICMP socket: %v", r["error"])
	}
	p := sub(r, "ping")
	if r["ok"] != true || p["sent"] != float64(2) || p["received"] != float64(2) || p["loss_percent"] != float64(0) || p["rtt_min_ms"] == nil || p["rtt_avg_ms"] == nil || p["rtt_max_ms"] == nil || p["rtt_mdev_ms"] == nil {
		t.Errorf("%v", r)
	}
}

func TestADiagnosticRequestThatCannotRunIsRefused(t *testing.T) {
	g := diagGateway(t)
	g.badRequest = true // the bodies break the spec on purpose
	for name, c := range map[string]struct {
		body   any
		status int
		code   string
	}{
		"a field the type does not use": {map[string]any{"type": "ping", "host": loopback, "port": 80}, 422, "validation_failed"},
		"a missing port":                {map[string]any{"type": "tcp", "host": loopback}, 422, "validation_failed"},
		"a host that is a URL":          {map[string]any{"type": "tcp", "host": "http://x", "port": 80}, 422, "validation_failed"},
		"an unknown type":               {map[string]any{"type": "traceroute", "host": loopback}, 422, "validation_failed"},
		"an unknown field":              {map[string]any{"type": "ping", "host": loopback, "nope": 1}, 422, "validation_failed"},
		"a timeout over a minute":       {map[string]any{"type": "tcp", "host": loopback, "port": 80, "timeout": "2m"}, 422, "validation_failed"},
		"a probe that does not exist":   {map[string]any{"type": "ping", "host": loopback, "from": map[string]any{"probe": "p1"}}, 404, "not_found"},
		"two places to run from":        {map[string]any{"type": "ping", "host": loopback, "from": map[string]any{"probe": "p1", "gateway": true}}, 422, "validation_failed"},
		"malformed JSON":                {`{"type":`, 400, "bad_request"},
		"no body":                       {nil, 400, "bad_request"},
	} {
		r := g.diagnose(c.body)
		if r.Status != c.status || r.code(t) != c.code {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	// the refusal names the field
	r := g.diagnose(map[string]any{"type": "tcp", "host": loopback})
	errs := r.json(t)["errors"].([]any)
	if e := errs[0].(map[string]any); e["path"] != "/port" || e["code"] != "missing_field" {
		t.Errorf("%v", errs)
	}
	// and the refusal of a probe that does not exist names it
	r = g.diagnose(map[string]any{"type": "ping", "host": loopback, "from": map[string]any{"probe": "p1"}})
	if !strings.Contains(string(r.Body), `no probe \"p1\"`) {
		t.Errorf("%s", r.Body)
	}
}

func TestDiagnosticsNeedASessionAndTheOverlaysScope(t *testing.T) {
	g := diagGateway(t)
	body := map[string]any{"type": "tcp", "host": loopback, "port": listenTCPServer(t)}
	g.badRequest = true
	if r := g.do("POST", "/diagnostics", body, map[string]string{"Authorization": ""}, nil); r.Status != 401 {
		t.Errorf("without credentials: %d", r.Status)
	}
	g.mintToken("read")
	if r := g.diagnose(body); r.Status != 403 {
		t.Errorf("a read-only token: %d %s", r.Status, r.Body)
	}
	g.mintToken("overlays")
	if r := g.diagnose(body); r.Status != 200 {
		t.Errorf("an overlays token: %d %s", r.Status, r.Body)
	}
}

// blocking is a stack whose dials wait until they are released.
type blocking struct {
	diag.HostNet
	entered chan struct{}
	release chan struct{}
	// canceled gets a value each time a dial returns because its context ended (nil: not observed).
	canceled chan struct{}
}

func (b *blocking) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		if b.canceled != nil {
			b.canceled <- struct{}{}
		}
	}
	return nil, &net.OpError{Op: "dial", Err: fmt.Errorf("released")}
}

func TestMoreThanEightDiagnosticsAtOnceAreRefused(t *testing.T) {
	b := &blocking{entered: make(chan struct{}, 16), release: make(chan struct{})}
	g := newGW(t, func(o *options) { o.diagnostics = api.DiagnosticsConfig{Net: b} })
	g.finishSetup()
	body := map[string]any{"type": "tcp", "host": "203.0.113.10", "port": 80}
	var wg sync.WaitGroup
	for i := 0; i < api.MaxDiagnostics; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := g.diagnose(body); r.Status != 200 {
				t.Errorf("%d %s", r.Status, r.Body)
			}
		}()
	}
	for i := 0; i < api.MaxDiagnostics; i++ {
		select {
		case <-b.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the diagnostics do not start")
		}
	}
	r := g.diagnose(body)
	if r.Status != 422 || r.code(t) != "capacity_exceeded" {
		t.Errorf("the ninth: %d %s", r.Status, r.Body)
	}
	close(b.release)
	wg.Wait()
	// the places are free again
	if r := g.diagnose(body); r.Status != 200 {
		t.Errorf("after they ended: %d %s", r.Status, r.Body)
	}
}

func TestACanceledRequestEndsItsDiagnostic(t *testing.T) {
	b := &blocking{entered: make(chan struct{}, 4), release: make(chan struct{}), canceled: make(chan struct{}, 4)}
	defer close(b.release)
	g := newGW(t, func(o *options) { o.diagnostics = api.DiagnosticsConfig{Net: b} })
	g.finishSetup()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		raw := strings.NewReader(`{"type":"tcp","host":"203.0.113.10","port":80,"timeout":"50s"}`)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.ts.URL+"/api/v1/diagnostics", raw)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+g.token)
		_, err := http.DefaultClient.Do(req)
		done <- err
	}()
	<-b.entered
	cancel()
	// the cancel reaches the dial itself: the release channel stays closed until the test ends, so only the context
	// can end it (the request timeout of 50 s is far beyond the limit)
	select {
	case <-b.canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("the dial of the diagnostic does not see the cancel of the request")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the request does not end")
	}
	// the place of the diagnostic is free again once its dial returned
	deadline := time.After(10 * time.Second)
	for {
		r := g.diagnose(map[string]any{"type": "tcp", "host": "203.0.113.10", "port": 80, "timeout": "100ms"})
		if r.Status == 200 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("%d", r.Status)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// auditEntries are the diagnostic.run entries of the audit log, newest first.
func auditEntries(g *gw) []map[string]any {
	g.t.Helper()
	var entries []map[string]any
	for _, i := range g.do("GET", "/audit?limit=500", nil, nil, nil).json(g.t)["items"].([]any) {
		if e := i.(map[string]any); e["action"] == "diagnostic.run" {
			entries = append(entries, e)
		}
	}
	return entries
}

// auditDetails are the details of the diagnostic.run entries of the audit log, newest first.
func auditDetails(g *gw) []string {
	g.t.Helper()
	var details []string
	for _, e := range auditEntries(g) {
		details = append(details, fmt.Sprint(e["detail"]))
	}
	return details
}

func TestADiagnosticIsAuditedAndTheCapabilitiesListThem(t *testing.T) {
	g := diagGateway(t)
	open, shut := listenTCPServer(t), freeTCPPort(t)
	g.diagnoseOK(map[string]any{"type": "tcp", "host": loopback, "port": open})
	g.diagnoseOK(map[string]any{"type": "tcp", "host": loopback, "port": shut})
	want := []string{fmt.Sprintf("tcp 127.0.0.1:%d failed", shut), fmt.Sprintf("tcp 127.0.0.1:%d ok", open)}
	if details := auditDetails(g); fmt.Sprint(details) != fmt.Sprint(want) {
		t.Errorf("audit: %q, want %q", details, want)
	}
	// every entry names who ran it (the token of the requests) and how; a run has no object and no revision
	entries := auditEntries(g)
	if len(entries) != 2 {
		t.Fatalf("%d entries", len(entries))
	}
	for _, e := range entries {
		actor, _ := e["actor"].(map[string]any)
		if actor["type"] != "token" || actor["name"] != "test-full" || actor["id"] == nil || actor["id"] == "" || e["via"] != "api" || e["object"] != nil || e["revision"] != nil {
			t.Errorf("the entry %v", e)
		}
	}
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	if !strings.Contains(fmt.Sprint(caps["features"]), "diagnostics") {
		t.Errorf("features %v", caps["features"])
	}
	if got := fmt.Sprint(caps["diagnostic_types"]); got != "[ping tcp udp dns http tls path]" {
		t.Errorf("diagnostic_types %s", got)
	}
}

// The entry is the compensating control of the decision that a diagnostic may reach every destination: it names what was
// contacted, not what the request says in the fields that the type ignores (P6-AUDIT-14).
func TestTheAuditEntryOfADiagnosticNamesWhatWasContacted(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer web.Close()
	dnsPort := int(dnsServer(t, "203.0.113.77").Port())
	g := diagGateway(t)
	// an http run with a url ignores host and port
	g.diagnoseOK(map[string]any{"type": "http", "host": "example.com", "port": 8080, "url": web.URL + "/admin/quit?token=secret"})
	g.diagnoseOK(map[string]any{"type": "http", "host": loopback, "port": 1, "url": web.URL + "/" + strings.Repeat("a", 500)})
	// the name asked and the server it was asked of
	g.diagnoseOK(map[string]any{"type": "dns", "host": loopback, "dns_name": "broker.example.com", "port": dnsPort})
	// the datagram is not quoted, its size is
	g.diagnoseOK(map[string]any{"type": "udp", "host": loopback, "port": freeTCPPort(t), "payload": "GET /secret", "timeout": "50ms"})
	// the name a certificate is verified for
	g.diagnoseOK(map[string]any{"type": "tls", "host": loopback, "port": freeTCPPort(t), "sni": "broker.example.com", "timeout": "100ms"})
	got := auditDetails(g)
	if len(got) != 5 {
		t.Fatalf("%q", got)
	}
	if !strings.HasPrefix(got[0], "tls 127.0.0.1:") || !strings.HasSuffix(got[0], ` sni "broker.example.com" failed`) {
		t.Errorf("tls: %q", got[0])
	}
	got = got[1:]
	host := strings.TrimPrefix(web.URL, "http://")
	if !strings.HasPrefix(got[0], "udp 127.0.0.1:") || !strings.HasSuffix(got[0], " payload 11 bytes failed") || strings.Contains(got[0], "secret") {
		t.Errorf("udp: %q", got[0])
	}
	if want := fmt.Sprintf(`dns "broker.example.com" @127.0.0.1:%d ok`, dnsPort); got[1] != want {
		t.Errorf("dns: %q, want %q", got[1], want)
	}
	if !strings.HasPrefix(got[2], "http http://"+host+"/aaaa") || !strings.HasSuffix(got[2], "... ok") || len(got[2]) > 260 {
		t.Errorf("a long url: %q", got[2])
	}
	if got[3] != "http http://"+host+"/admin/quit?... ok" {
		t.Errorf("an http run with a url: %q", got[3])
	}
}

// A principal starts a few diagnostics at once and a few more every second; the audit log keeps what was started
// (P6-AUDIT-15).
func TestADiagnosticLoopIsRefusedAfterTheBurst(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	g := newGW(t, func(o *options) { o.apiClock = clk })
	g.finishSetup()
	body := map[string]any{"type": "tcp", "host": loopback, "port": freeTCPPort(t), "timeout": "100ms"}
	for i := 0; i < api.DiagnosticBurst; i++ {
		if r := g.diagnose(body); r.Status != 200 {
			t.Fatalf("run %d: %d %s", i+1, r.Status, r.Body)
		}
	}
	r := g.diagnose(body)
	if r.Status != 429 || r.code(t) != "rate_limited" || r.Header.Get("Retry-After") != "1" {
		t.Fatalf("after the burst: %d %s %v", r.Status, r.Body, r.Header)
	}
	for i := 0; i < 180; i++ {
		if r := g.diagnose(body); r.Status != 429 {
			t.Fatalf("a refused loop: %d %s", r.Status, r.Body)
		}
	}
	if n := len(auditDetails(g)); n != api.DiagnosticBurst {
		t.Errorf("%d entries for %d runs", n, api.DiagnosticBurst)
	}
	// the place is paid back with time, one every 1/DiagnosticRate of a second
	clk.Advance(time.Second / api.DiagnosticRate)
	if r := g.diagnose(body); r.Status != 200 {
		t.Errorf("after the wait: %d %s", r.Status, r.Body)
	}
	if r := g.diagnose(body); r.Status != 429 {
		t.Errorf("only one place was paid back: %d %s", r.Status, r.Body)
	}
	// another principal has its own
	g.mintToken("overlays")
	if r := g.diagnose(body); r.Status != 200 {
		t.Errorf("another token: %d %s", r.Status, r.Body)
	}
}

// The request ends at the ceiling, whatever its timeouts add up to, and frees its place (P6-AUDIT-20).
func TestADiagnosticEndsAtTheCeilingAndFreesItsPlace(t *testing.T) {
	old := api.DiagnosticCeiling
	api.DiagnosticCeiling = 300 * time.Millisecond
	t.Cleanup(func() { api.DiagnosticCeiling = old })
	b := &blocking{entered: make(chan struct{}, 64), release: make(chan struct{})}
	defer close(b.release)
	g := newGW(t, func(o *options) { o.diagnostics = api.DiagnosticsConfig{Net: b} })
	g.finishSetup()
	start := time.Now()
	r := g.diagnoseOK(map[string]any{"type": "path", "host": "203.0.113.10", "port": 80, "timeout": "60s"})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the path took %s with a ceiling of 300ms", d)
	}
	if r["ok"] != false {
		t.Errorf("%v", r)
	}
	// all places are free
	for i := 0; i < api.MaxDiagnostics; i++ {
		if r := g.diagnose(map[string]any{"type": "tcp", "host": "203.0.113.10", "port": 80, "timeout": "60s"}); r.Status != 200 {
			t.Fatalf("run %d: %d %s", i+1, r.Status, r.Body)
		}
	}
}

// redirecting is the Net of the tests of the default servers: it records the DNS servers that are dialed, refuses the
// DNS proxy's address like a service namespace without a proxy and sends the datagrams for the other servers to the
// test servers behind them.
type redirecting struct {
	diag.HostNet
	mu     sync.Mutex
	dialed []string
	to     map[string]string
}

func (r *redirecting) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network == "udp" {
		r.mu.Lock()
		r.dialed = append(r.dialed, addr)
		to, known := r.to[addr]
		r.mu.Unlock()
		if !known {
			return nil, &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
		}
		addr = to
	}
	return r.HostNet.DialContext(ctx, network, addr)
}

func (r *redirecting) servers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dialed...)
}

// The production list of the servers that resolve a host name: the DNS proxy in the service namespace first, then the
// uplink's dns_upstream, or the host's resolvers when there is none (P6-AUDIT-22).
func TestAHostNameIsResolvedByTheProxyThenTheUplinkThenTheHost(t *testing.T) {
	uplink, host := dnsServer(t, "127.0.0.1"), dnsServer(t, "203.0.113.99")
	nets := &redirecting{to: map[string]string{"203.0.113.53:53": uplink.String(), "192.0.2.53:53": host.String()}}
	g := newGW(t, func(o *options) {
		o.diagnostics = api.DiagnosticsConfig{Net: nets}
		o.resolvers = []netip.Addr{netip.MustParseAddr("192.0.2.53")}
	})
	g.finishSetup()
	port := listenTCPServer(t)
	run := func() map[string]any {
		t.Helper()
		nets.mu.Lock()
		nets.dialed = nil
		nets.mu.Unlock()
		return g.diagnoseOK(map[string]any{"type": "tcp", "host": "broker.example.com", "port": port})
	}

	// no dns_upstream: the proxy, whose address is the service peer, then the resolvers of the host
	r := run()
	if got := nets.servers(); fmt.Sprint(got) != "[169.254.100.2:53 192.0.2.53:53]" {
		t.Errorf("the servers asked: %v", got)
	}
	if r["address"] != "203.0.113.99" {
		t.Errorf("the answer of the host's resolver: %v", r)
	}

	// dns_upstream replaces the host's resolvers, the proxy stays first
	id := g.mustPatch(map[string]any{"uplink": map[string]any{"dns_upstream": []any{"203.0.113.53"}}})
	if ar := g.apply(id).json(t); ar["status"] == "pending_confirm" {
		if cf := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); cf.Status != 200 {
			t.Fatalf("%d %s", cf.Status, cf.Body)
		}
	}
	r = run()
	if got := nets.servers(); fmt.Sprint(got) != "[169.254.100.2:53 203.0.113.53:53]" {
		t.Errorf("the servers asked with dns_upstream: %v", got)
	}
	if r["ok"] != true || r["address"] != "127.0.0.1" {
		t.Errorf("the answer of the uplink's server: %v", r)
	}
}
