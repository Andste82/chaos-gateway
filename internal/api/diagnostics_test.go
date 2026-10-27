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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/api"
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
}

func (b *blocking) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
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
	b := &blocking{entered: make(chan struct{}, 4), release: make(chan struct{})}
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

func TestADiagnosticIsAuditedAndTheCapabilitiesListThem(t *testing.T) {
	g := diagGateway(t)
	g.diagnoseOK(map[string]any{"type": "tcp", "host": loopback, "port": listenTCPServer(t)})
	g.diagnoseOK(map[string]any{"type": "tcp", "host": loopback, "port": freeTCPPort(t)})
	var details []string
	for _, i := range g.do("GET", "/audit", nil, nil, nil).json(t)["items"].([]any) {
		if e := i.(map[string]any); e["action"] == "diagnostic.run" {
			details = append(details, fmt.Sprint(e["detail"]))
		}
	}
	if len(details) != 2 || !strings.HasSuffix(details[0], " failed") && !strings.HasSuffix(details[1], " failed") || !strings.HasPrefix(details[0], "tcp 127.0.0.1:") {
		t.Errorf("audit: %v", details)
	}
	caps := g.do("GET", "/capabilities", nil, nil, nil).json(t)
	if !strings.Contains(fmt.Sprint(caps["features"]), "diagnostics") {
		t.Errorf("features %v", caps["features"])
	}
	if got := fmt.Sprint(caps["diagnostic_types"]); got != "[ping tcp udp dns http tls path]" {
		t.Errorf("diagnostic_types %s", got)
	}
}
