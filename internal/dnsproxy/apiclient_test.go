package dnsproxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

// Issue #64: the API issues its certificate again when an older one lacks the service address. The
// proxy trusts the certificate in its file as it is now, without a restart.
func TestTheAPIClientTrustsTheCertificateTheAPIIssuedAgain(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer ts.Close()
	certFile := filepath.Join(t.TempDir(), "api.pem")
	if err := os.WriteFile(certFile, otherCertificate(t), 0o644); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("svc"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &APIClient{Base: ts.URL, TokenFile: token, CertFile: certFile, Wait: 5 * time.Second}
	defer c.CloseIdleConnections()
	if _, err := c.Config(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("a certificate the API does not have: %v", err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Config(context.Background(), 0); err != nil {
		t.Fatalf("the API's certificate as it is now: %v", err)
	}
}

func otherCertificate(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "other"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// countLines counts the log lines that contain every one of the parts.
func countLines(log string, parts ...string) int {
	n := 0
	for _, l := range strings.Split(log, "\n") {
		all := l != ""
		for _, p := range parts {
			all = all && strings.Contains(l, p)
		}
		if all {
			n++
		}
	}
	return n
}

// Issue #57 F6: before the setup is applied the API cannot be reached, and the proxy warned at every
// attempt. It warns once, with a hint, and again only when the error changes; the attempts in between
// are logged at debug level, and the recovery is said once.
func TestTheProxyWarnsOnceWhileTheAPICannotBeReached(t *testing.T) {
	var phase atomic.Value
	phase.Store("not-yet")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := phase.Load().(string); p {
		case "ok":
			if r.URL.Query().Get("after") != "0" {
				<-r.Context().Done() // the long poll of a newer generation
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"generation":1,"networks":[],"upstream":[],"faults":[]}`))
		default:
			http.Error(w, p, http.StatusServiceUnavailable)
		}
	}))
	defer ts.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("svc"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	log := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clk := clock.NewFake(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC))
	c := &APIClient{Base: ts.URL, TokenFile: token, Wait: 5 * time.Second}
	defer c.CloseIdleConnections()
	s := New(Options{Clock: clk})
	armed := clk.Pending()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Follow(ctx, c, log); close(done) }()

	retry := func(next string) {
		clk.BlockUntil(armed + 1) // Follow waits for its pause
		phase.Store(next)
		clk.Advance(time.Minute) // longer than the longest pause
	}
	for range 4 {
		retry("not-yet")
	}
	retry("still-not")
	retry("still-not")
	retry("ok")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "the DNS configuration is in use") {
		if time.Now().After(deadline) {
			t.Fatalf("no configuration was fetched:\n%s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	got := out.String()
	if n := countLines(got, "level=WARN", "not-yet", "hint="); n != 1 {
		t.Errorf("%d warnings of the first error, want 1 with a hint:\n%s", n, got)
	}
	if n := countLines(got, "level=WARN", "still-not"); n != 1 {
		t.Errorf("%d warnings of the changed error, want 1:\n%s", n, got)
	}
	if n := countLines(got, "level=DEBUG", "cannot fetch the DNS configuration"); n != 5 {
		t.Errorf("%d debug lines, want one per repeated attempt (5):\n%s", n, got)
	}
	if n := countLines(got, "level=INFO", "can be fetched again"); n != 1 {
		t.Errorf("the recovery is said %d times, want once:\n%s", n, got)
	}
}

// P5-AUDIT-14: a restart of the gateway drops the overlays, and with them the DNS faults, while the proxy keeps the configuration in
// use as long as the API cannot be reached. Once a configuration has been in use the pause between two attempts stays at
// followPauseInUse (2 s), however long the API was away, so the configuration without the dropped faults is in use within it.
func TestTheProxyAsksAgainWithinTwoSecondsOnceAConfigurationWasInUse(t *testing.T) {
	var phase atomic.Value
	phase.Store("ok")
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch phase.Load().(string) {
		case "ok":
			if r.URL.Query().Get("after") != "0" {
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"generation":1,"networks":[],"upstream":[],"faults":[` + fault(ovA, `"action":"nxdomain"`) + `]}`))
		case "back":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"generation":2,"networks":[],"upstream":[],"faults":[]}`))
			phase.Store("idle")
		case "idle":
			<-r.Context().Done()
		default:
			http.Error(w, "restarting", http.StatusServiceUnavailable)
		}
	}))
	defer ts.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("svc"), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))
	c := &APIClient{Base: ts.URL, TokenFile: token, Wait: 5 * time.Second}
	defer c.CloseIdleConnections()
	s := New(Options{Clock: clk})
	armed := clk.Pending()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	faultsInUse := func() int {
		if pc := s.parsed.Load(); pc != nil {
			return len(pc.faults)
		}
		return -1
	}
	// the API goes away while the long poll after the first configuration waits
	waitUntil := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("%s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	go s.Follow(ctx, c, slog.New(slog.DiscardHandler))
	waitUntil("the first configuration is not in use", func() bool { return faultsInUse() == 1 })
	phase.Store("down")
	c.CloseIdleConnections()
	ts.CloseClientConnections() // the long poll fails, as the API's container stops
	for i := range 8 {
		clk.BlockUntil(armed + 1)
		before := requests.Load()
		if i == 7 {
			phase.Store("back")
		}
		clk.Advance(followPauseInUse)
		waitUntil(fmt.Sprintf("attempt %d did not come within %v of the one before", i+2, followPauseInUse), func() bool { return requests.Load() > before })
	}
	waitUntil("the configuration without the dropped fault is not in use", func() bool { return faultsInUse() == 0 })
}
