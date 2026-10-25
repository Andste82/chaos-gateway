package tlsresponder

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The hand-over of a connection to the interception proxy (plan §2.8, M22) with a proxy that is a few lines of Go: what the responder
// sends it (the CONNECT request with the headers that say who the device is), what it relays, what it reports and what it does when the
// proxy is not there, refuses, or asks for a connection to be paced or reset. The real proxy is the subject of mitm_test.go.

// fakeProxy accepts CONNECT requests, answers them and relays the connection to the destination the request names.
type fakeProxy struct {
	ln net.Listener
	// status is the answer to a CONNECT request; 200 relays.
	status int

	mu       sync.Mutex
	requests []connectRequest
	conns    []net.Conn
	first    [][]byte // the first bytes of each connection after the request
	ended    int
}

type connectRequest struct {
	target  string
	headers http.Header
}

func newFakeProxy(t *testing.T) *fakeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProxy{ln: ln, status: 200}
	t.Cleanup(func() {
		_ = ln.Close()
		p.mu.Lock()
		for _, c := range p.conns {
			_ = c.Close()
		}
		p.mu.Unlock()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			go p.serve(c)
		}
	}()
	return p
}

func (p *fakeProxy) addr() string { return p.ln.Addr().String() }

func (p *fakeProxy) serve(c net.Conn) {
	defer func() {
		_ = c.Close()
		p.mu.Lock()
		p.ended++
		p.mu.Unlock()
	}()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.requests = append(p.requests, connectRequest{target: req.Host, headers: req.Header.Clone()})
	status := p.status
	p.mu.Unlock()
	if status != 200 {
		_, _ = fmt.Fprintf(c, "HTTP/1.1 %d Nope\r\nContent-Length: 0\r\n\r\n", status)
		return
	}
	up, err := net.Dial("tcp", req.Host)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer func() { _ = up.Close() }()
	_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
	// the first bytes, as one piece, for the test that looks at them
	buf := make([]byte, 4096)
	n, _ := br.Read(buf)
	p.mu.Lock()
	p.first = append(p.first, append([]byte(nil), buf[:n]...))
	p.mu.Unlock()
	_, _ = up.Write(buf[:n])
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
}

func (p *fakeProxy) request(t *testing.T, i int) connectRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		if len(p.requests) > i {
			r := p.requests[i]
			p.mu.Unlock()
			return r
		}
		p.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the proxy got no request %d", i)
	return connectRequest{}
}

// interceptDecision is a decision of the case intercept for an overlay and a device.
func interceptDecision(passthrough bool) func(ConnInfo) Decision {
	return func(ConnInfo) Decision {
		return Decision{Generation: 5, Overlay: "11111111-1111-4111-8111-111111111111", Device: "44444444-4444-4444-8444-444444444444",
			Case: &model.TlsCase{Case: model.TlsCaseCaseIntercept}, Expected: "any", Passthrough: passthrough}
	}
}

func newInterceptResponder(t *testing.T, p *fakeProxy, passthrough bool, dst netip.AddrPort) *rig {
	t.Helper()
	r := newRig(t, interceptDecision(passthrough), func(o *Options) {
		if p != nil {
			o.Proxy = p.addr()
		}
	})
	r.setDst(dst)
	return r
}

// tlsEcho is a TLS server with a certificate the clients of the tests trust, which answers one line.
func tlsEchoUpstream(t *testing.T, r *rig, name string) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{validServerCert(t, r.cas, name)}})
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
				line, _ := bufio.NewReader(c).ReadString('\n')
				_, _ = io.WriteString(c, "echo "+line)
			}()
		}
	}()
	return ln.Addr().String()
}

// What the proxy is asked, and that the device talks to the real server through it: the CONNECT request names the destination the
// device meant and carries the client, the overlay, the device, the server name and the id of the connection; the ClientHello is
// replayed to the proxy first, so the handshake of the device completes with the server behind it.
func TestAConnectionOfTheCaseInterceptIsHandedToTheProxyWithWhoItIs(t *testing.T) {
	p := newFakeProxy(t)
	r := newInterceptResponder(t, p, true, netip.AddrPort{})
	up := tlsEchoUpstream(t, r, "broker.example.com")
	r.setDst(netip.MustParseAddrPort(up))

	c, err := r.dialTLS(pool(r.cas.Test), "broker.example.com", tls.VersionTLS12, tls.VersionTLS13)
	if err != nil {
		t.Fatal(err)
	}
	// the certificate is the real server's: the fake proxy intercepts nothing
	if got := c.ConnectionState().PeerCertificates[0].DNSNames; len(got) != 1 || got[0] != "broker.example.com" {
		t.Errorf("%v", got)
	}
	_, _ = io.WriteString(c, "hi\n")
	if line, _ := bufio.NewReader(c).ReadString('\n'); line != "echo hi\n" {
		t.Errorf("%q", line)
	}
	_ = c.Close()

	req := p.request(t, 0)
	if req.target != up {
		t.Errorf("the proxy is asked for %q, the device meant %q", req.target, up)
	}
	h := req.headers
	if h.Get(HeaderClient) != "127.0.0.1" || h.Get(HeaderOverlay) != "11111111-1111-4111-8111-111111111111" || h.Get(HeaderDevice) != "44444444-4444-4444-8444-444444444444" ||
		h.Get(HeaderSNI) != "broker.example.com" || len(h.Get(HeaderConnection)) != 24 {
		t.Errorf("%v", h)
	}
	reports := r.sink.wait(t, 1)
	rep := reports[0]
	if rep.Outcome != model.TlsHandshakeReportOutcomeIntercepted || rep.Case == nil || *rep.Case != "intercept" || rep.Overlay == nil || rep.Overlay.String() != "11111111-1111-4111-8111-111111111111" ||
		rep.Sni == nil || *rep.Sni != "broker.example.com" || rep.OriginalDst != up {
		t.Errorf("%+v", rep)
	}
	if r.srv.Intercepted() != 1 || r.srv.ProxyFailures() != 0 {
		t.Errorf("intercepted %d, failed %d", r.srv.Intercepted(), r.srv.ProxyFailures())
	}
}

// A connection that is no TLS is handed over as well, its first bytes in one piece (the proxy tells HTTP from a stream by the first
// bytes it gets, and the responder has read five of them to know that it is no ClientHello).
func TestAConnectionThatIsNoTLSIsHandedOverWithItsFirstBytesInOnePiece(t *testing.T) {
	p := newFakeProxy(t)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "plain") }))
	t.Cleanup(plain.Close)
	r := newInterceptResponder(t, p, true, netip.MustParseAddrPort(plain.Listener.Addr().String()))
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	const reqText = "GET /hello HTTP/1.1\r\nHost: example.org\r\nConnection: close\r\n\r\n"
	_, _ = io.WriteString(c, reqText)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(resp.Body); string(b) != "plain" {
		t.Errorf("%q", b)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.first) != 1 || string(p.first[0]) != reqText {
		t.Errorf("the proxy's first bytes: %q", p.first)
	}
	if rep := r.sink.all(); len(rep) > 0 && rep[0].Sni != nil {
		t.Errorf("a connection without a ClientHello has no server name: %+v", rep[0])
	}
}

// A proxy that is not there, that refuses, or that is not configured: the connection is reset (never passed on, the case selected it), the
// outcome is `proxy_unavailable`, and the failure is counted.
func TestWithoutAProxyThatAnswersTheConnectionIsResetAndReported(t *testing.T) {
	dead := freeAddr(t)
	refusing := newFakeProxy(t)
	refusing.status = 403
	for name, proxy := range map[string]string{"nothing listens": dead, "the proxy refuses": refusing.addr(), "no proxy configured": ""} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, interceptDecision(true), func(o *Options) { o.Proxy = proxy })
			c, err := net.Dial("tcp", r.addr)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
			if _, err := io.ReadAll(c); err == nil || !strings.Contains(err.Error(), "reset") {
				t.Errorf("the connection was not reset: %v", err)
			}
			rep := r.sink.wait(t, 1)[0]
			if rep.Outcome != model.TlsHandshakeReportOutcomeProxyUnavailable || rep.Case == nil || *rep.Case != "intercept" {
				t.Errorf("%+v", rep)
			}
			if r.srv.ProxyFailures() != 1 || r.srv.Intercepted() != 0 {
				t.Errorf("failed %d, intercepted %d", r.srv.ProxyFailures(), r.srv.Intercepted())
			}
		})
	}
}

// The proxy's own connection to the server would go round the access matrix, so a connection that the gateway would not have forwarded is
// reset without the proxy being asked.
func TestAConnectionTheGatewayWouldNotForwardIsNotHandedOver(t *testing.T) {
	p := newFakeProxy(t)
	r := newInterceptResponder(t, p, false, netip.MustParseAddrPort("203.0.113.10:443"))
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	if _, err := io.ReadAll(c); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Errorf("not reset: %v", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 0 || r.srv.Denied() != 1 || r.srv.Intercepted() != 0 {
		t.Errorf("proxy requests %d, denied %d, intercepted %d", len(p.requests), r.srv.Denied(), r.srv.Intercepted())
	}
}

// A device that says nothing has nothing to hand over: the connection ends as a handshake that never started does.
func TestAConnectionThatSaysNothingIsNotHandedOver(t *testing.T) {
	p := newFakeProxy(t)
	r := newRig(t, interceptDecision(true), func(o *Options) { o.Proxy = p.addr(); o.HelloTimeout = 300 * time.Millisecond })
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.ReadAll(c)
	rep := r.sink.wait(t, 1)[0]
	if rep.Outcome != model.TlsHandshakeReportOutcomeTimeout {
		t.Errorf("%+v", rep)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 0 || r.srv.Intercepted() != 0 {
		t.Errorf("proxy requests %d, intercepted %d", len(p.requests), r.srv.Intercepted())
	}
}

// Reset on removal holds for the connections of the proxy: when the configuration of a later generation does not hold the overlay any more,
// the device's connection is reset and the proxy's side is closed.
func TestAHandedOverConnectionIsResetWhenItsCaseIsRemoved(t *testing.T) {
	p := newFakeProxy(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	r := newInterceptResponder(t, p, true, netip.MustParseAddrPort(echo.Addr().String()))
	r.srv.SetConfig(configHolding(t, 5, "11111111-1111-4111-8111-111111111111"))
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	r.sink.wait(t, 1)
	r.srv.SetConfig(configHolding(t, 6))
	if _, err := io.ReadAll(c); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Errorf("the connection was not reset: %v", err)
	}
	if r.srv.Resets() != 1 {
		t.Errorf("%d resets", r.srv.Resets())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		ended := p.ended
		p.mu.Unlock()
		if ended == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the proxy's side of the connection was not closed")
}

// configHolding is a configuration of the generation that holds a case of the kind intercept for each overlay.
func configHolding(t *testing.T, gen int64, overlays ...string) *model.TlsServiceConfig {
	t.Helper()
	var cases []string
	for _, o := range overlays {
		cases = append(cases, fmt.Sprintf(`{"case":"intercept","overlay":%q,"sources":["10.10.0.42"],"expected":"any"}`, o))
	}
	var c model.TlsServiceConfig
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"generation":%d,"cases":[%s]}`, gen, strings.Join(cases, ","))), &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

// connectionID is the id of the connection the proxy was told, for the control interface.
func connectionID(t *testing.T, p *fakeProxy, i int) string {
	t.Helper()
	id := p.request(t, i).headers.Get(HeaderConnection)
	if id == "" {
		t.Fatal("no connection id")
	}
	return id
}

func post(t *testing.T, base, path, body string) int {
	t.Helper()
	resp, err := http.Post(base+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The proxy asks the control interface to pace the bytes towards the device of a connection by its id, and the rate holds from then on until
// it is taken away; an id the responder does not know, and a request that is no request, are refused.
func TestTheControlInterfacePacesTheBytesTowardsTheDevice(t *testing.T) {
	p := newFakeProxy(t)
	// a server that sends 6000 bytes at once for every line it gets
	srv, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() {
		for {
			c, err := srv.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				for {
					if _, err := br.ReadString('\n'); err != nil {
						return
					}
					_, _ = c.Write(make([]byte, 6000))
				}
			}()
		}
	}()
	r := newInterceptResponder(t, p, true, netip.MustParseAddrPort(srv.Addr().String()))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.srv.ServeControlOn(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	base := "http://" + ln.Addr().String()

	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	readAll := func(n int) time.Duration {
		start := time.Now()
		_, _ = io.WriteString(c, "x\n")
		if _, err := io.ReadFull(c, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	// the first bytes (five are what the responder reads before it knows what the connection is) make it hand the connection over
	_, _ = io.WriteString(c, "start\n")
	if _, err := io.ReadFull(c, make([]byte, 6000)); err != nil {
		t.Fatal(err)
	}
	id := connectionID(t, p, 0)
	if fast := readAll(6000); fast > time.Second {
		t.Fatalf("the unpaced read took %s", fast)
	}
	if st := post(t, base, "/throttle", fmt.Sprintf(`{"connection":%q,"rate_bps":48000}`, id)); st != 204 { // 6000 bytes per second
		t.Fatalf("%d", st)
	}
	if got := readAll(6000); got < 800*time.Millisecond || got > 3*time.Second {
		t.Errorf("6000 bytes at 6000 bytes/s took %s", got)
	}
	if st := post(t, base, "/throttle", fmt.Sprintf(`{"connection":%q,"rate_bps":0}`, id)); st != 204 {
		t.Fatalf("%d", st)
	}
	if got := readAll(6000); got > time.Second {
		t.Errorf("the rate was taken away and the read took %s", got)
	}
	for body, want := range map[string]int{
		`{"connection":"nope","rate_bps":1000}`: 404,
		`{"rate_bps":1000}`:                     400,
		`{"connection":"x","rate_bps":-1}`:      400,
		`not json`:                              400,
	} {
		if st := post(t, base, "/throttle", body); st != want {
			t.Errorf("%s: %d, want %d", body, st, want)
		}
	}
	if resp, err := http.Get(base + "/throttle"); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("a GET: %v %v", resp, err)
	}
}

// An abort resets the device's connection at once and closes the proxy's; an unknown id is a 404, and a connection that has ended is unknown.
func TestTheControlInterfaceResetsTheConnectionOfTheDevice(t *testing.T) {
	p := newFakeProxy(t)
	echo, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	r := newInterceptResponder(t, p, true, netip.MustParseAddrPort(echo.Addr().String()))
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.srv.ServeControlOn(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	base := "http://" + ln.Addr().String()

	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\n\r\n")
	if _, err := io.ReadFull(c, make([]byte, 18)); err != nil {
		t.Fatal(err)
	}
	id := connectionID(t, p, 0)
	if st := post(t, base, "/abort", fmt.Sprintf(`{"connection":%q}`, id)); st != 204 {
		t.Fatalf("%d", st)
	}
	if _, err := io.ReadAll(c); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Errorf("an abort is a reset: %v", err)
	}
	if r.srv.Aborted() != 1 {
		t.Errorf("%d aborted", r.srv.Aborted())
	}
	// the connection is over, so is its id
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && post(t, base, "/abort", fmt.Sprintf(`{"connection":%q}`, id)) != 404 {
		time.Sleep(10 * time.Millisecond)
	}
	if st := post(t, base, "/abort", fmt.Sprintf(`{"connection":%q}`, id)); st != 404 {
		t.Errorf("an ended connection is still known: %d", st)
	}
	if st := post(t, base, "/abort", `{}`); st != 400 {
		t.Errorf("%d", st)
	}
}

// Every connection of the proxy ends with its id gone: nothing of a connection is kept after it, however it ends.
func TestNothingOfAHandedOverConnectionIsKeptAfterItEnds(t *testing.T) {
	p := newFakeProxy(t)
	echo, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	r := newInterceptResponder(t, p, true, netip.MustParseAddrPort(echo.Addr().String()))
	for i := 0; i < 5; i++ {
		c, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\n\r\n")
		_, _ = io.ReadFull(c, make([]byte, 18))
		_ = c.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.srv.pacerMu.Lock()
		n := len(r.srv.pacers)
		r.srv.pacerMu.Unlock()
		r.srv.cfgMu.Lock()
		held := len(r.srv.held)
		r.srv.cfgMu.Unlock()
		if n == 0 && held == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("a connection that ended is still held")
}

// A proxy that answers that it cannot reach the server (mitmproxy answers a CONNECT with 502 when its connection to the destination fails) is a proxy
// that works: the device is reset, as a connection to a server that is down is, the outcome is `server_unreachable` and it is no failure of the proxy.
func TestAServerTheProxyCannotReachIsReportedAsSuchAndIsNoFailureOfTheProxy(t *testing.T) {
	p := newFakeProxy(t)
	r := newInterceptResponder(t, p, true, netip.MustParseAddrPort(freeAddr(t))) // nothing listens there
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	if _, err := io.ReadAll(c); err == nil || !strings.Contains(err.Error(), "reset") {
		t.Errorf("the connection was not reset: %v", err)
	}
	rep := r.sink.wait(t, 1)[0]
	if rep.Outcome != model.TlsHandshakeReportOutcomeServerUnreachable || rep.Case == nil || *rep.Case != "intercept" {
		t.Errorf("%+v", rep)
	}
	if r.srv.ProxyFailures() != 0 || r.srv.Intercepted() != 0 {
		t.Errorf("failed %d, intercepted %d", r.srv.ProxyFailures(), r.srv.Intercepted())
	}
}
