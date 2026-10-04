package api_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/clock"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func TestTheCertificateIsCreatedOnceAndTrustable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	c1, err := api.LoadOrCreateCertificate(dir, []string{"gw.example"}, []net.IP{net.ParseIP("192.168.56.1")}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "key.pem")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the key's mode: %v %v", fi, err)
	}
	c2, err := api.LoadOrCreateCertificate(dir, nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if string(c1.Certificate[0]) != string(c2.Certificate[0]) {
		t.Error("the certificate was created again")
	}
	leaf, _ := x509.ParseCertificate(c1.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	if err := func() error {
		_, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "gw.example"})
		return err
	}(); err != nil {
		t.Errorf("the host name: %v", err)
	}
	if err := func() error {
		_, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "192.168.56.1"})
		return err
	}(); err != nil {
		t.Errorf("the management address: %v", err)
	}
	if leaf.NotAfter.Before(time.Now().AddDate(9, 0, 0)) {
		t.Errorf("valid until %v", leaf.NotAfter)
	}
	// a broken file is an error, not a silent new certificate
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := api.LoadOrCreateCertificate(dir, nil, nil, time.Now()); err == nil {
		t.Error("a corrupt certificate was replaced")
	}
}

func TestTheBinderFollowsTheWantedAddresses(t *testing.T) {
	cert, err := api.LoadOrCreateCertificate(t.TempDir(), nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	var wanted atomic.Pointer[[]netip.Addr]
	set := func(a ...string) {
		var l []netip.Addr
		for _, s := range a {
			l = append(l, netip.MustParseAddr(s))
		}
		wanted.Store(&l)
	}
	set("127.0.0.1")
	b := &api.Binder{
		Addrs:   func() []netip.Addr { return *wanted.Load() },
		Port:    func() int { return port },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }),
		TLS:     &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		Log:     slog.New(slog.DiscardHandler),
	}
	defer b.Close()
	b.Reconcile()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 3 * time.Second}
	get := func(ip string) error {
		res, err := client.Get("https://" + net.JoinHostPort(ip, itoa(int64(port))) + "/")
		if err != nil {
			return err
		}
		_ = res.Body.Close()
		return nil
	}
	if err := get("127.0.0.1"); err != nil {
		t.Fatalf("%v", err)
	}
	if err := get("127.0.0.2"); err == nil {
		t.Fatal("the API answers on an address that is not wanted")
	}
	// the management address changes (or the setup finishes): the listeners follow
	set("127.0.0.2")
	b.Reconcile()
	if err := get("127.0.0.2"); err != nil {
		t.Errorf("the new address: %v", err)
	}
	client.CloseIdleConnections()
	if err := get("127.0.0.1"); err == nil {
		t.Error("the old address still answers")
	}
	if l := b.Listening(); len(l) != 1 || l[0].Addr().String() != "127.0.0.2" {
		t.Errorf("%v", l)
	}
	set()
	b.Reconcile()
	if len(b.Listening()) != 0 {
		t.Errorf("%v", b.Listening())
	}
}

// M5-14 test: a repeated bind failure on one address logs once, not every reconcile, and recovery
// logs once too.
func TestAnUnbindableAddressLogsOnlyOnceAndOnRecovery(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := occupied.Addr().(*net.TCPAddr).Port
	var logBuf bytes.Buffer
	b := &api.Binder{
		Addrs:   func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("127.0.0.1")} },
		Port:    func() int { return port },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		Log:     slog.New(slog.NewTextHandler(&logBuf, nil)),
	}
	defer b.Close()
	for range 3 {
		b.Reconcile()
	}
	if n := strings.Count(logBuf.String(), "cannot listen"); n != 1 {
		t.Fatalf("logged the same failure %d times: %s", n, logBuf.String())
	}
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		b.Reconcile()
	}
	if n := strings.Count(logBuf.String(), "listen again"); n != 1 {
		t.Fatalf("logged the recovery %d times: %s", n, logBuf.String())
	}
}

// M5-11 test: a client that trickles a request body is closed once the read timeout passes, but an
// SSE-style handler that clears its read deadline is not.
func TestTheReadTimeoutClosesAStalledUploadButNotAStream(t *testing.T) {
	old := api.ReadTimeout
	api.ReadTimeout = 150 * time.Millisecond
	defer func() { api.ReadTimeout = old }()

	port := freePort(t)
	bodyErr := make(chan error, 1)
	b := &api.Binder{
		Addrs: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("127.0.0.1")} },
		Port:  func() int { return port },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				rc := http.NewResponseController(w)
				_ = rc.SetReadDeadline(time.Time{})
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("a"))
				_ = rc.Flush()
				time.Sleep(3 * api.ReadTimeout)
				_, _ = w.Write([]byte("b"))
				return
			}
			_, err := io.Copy(io.Discard, r.Body)
			bodyErr <- err
		}),
		Log: slog.New(slog.DiscardHandler),
	}
	defer b.Close()
	b.Reconcile()
	addr := net.JoinHostPort("127.0.0.1", itoa(int64(port)))

	// a trickled upload: the handler's read of the body must not hang forever
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n\r\n12345"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-bodyErr:
		if err == nil {
			t.Error("the stalled body read did not fail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stalled body read never returned")
	}

	// a streaming handler that clears its deadline must survive well past the read timeout
	conn2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn2.Close() }()
	if _, err := io.WriteString(conn2, "GET /stream HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got []byte
	buf2 := make([]byte, 256)
	for !bytes.ContainsRune(got, 'b') {
		n, err := conn2.Read(buf2)
		if err != nil {
			t.Fatalf("the stream's connection was closed early (got %q): %v", got, err)
		}
		got = append(got, buf2[:n]...)
	}
}

// M5-24 test: Run's ticker is the injected clock, not a raw time.NewTicker.
func TestRunReconcilesOnTheInjectedClock(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	port := freePort(t)
	var wanted atomic.Pointer[[]netip.Addr]
	set := func(a ...string) {
		var l []netip.Addr
		for _, s := range a {
			l = append(l, netip.MustParseAddr(s))
		}
		wanted.Store(&l)
	}
	set("127.0.0.1")
	b := &api.Binder{
		Addrs:   func() []netip.Addr { return *wanted.Load() },
		Port:    func() int { return port },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		Log:     slog.New(slog.DiscardHandler),
		Clock:   clk,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx, time.Minute); close(done) }()
	defer func() { cancel(); <-done }()

	// Run's own first Reconcile binds the initial address before the ticker even starts
	deadline := time.Now().Add(2 * time.Second)
	for len(b.Listening()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(b.Listening()) == 0 {
		t.Fatal("the initial Reconcile never ran")
	}

	set() // nothing wanted any more
	time.Sleep(20 * time.Millisecond)
	if len(b.Listening()) == 0 {
		t.Fatal("reconciled without the clock advancing")
	}
	clk.Advance(time.Minute)
	deadline = time.Now().Add(2 * time.Second)
	for len(b.Listening()) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(b.Listening()) != 0 {
		t.Fatal("did not reconcile after the clock advanced")
	}
}
