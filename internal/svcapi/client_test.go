package svcapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serverAndFiles(t *testing.T) (ts *httptest.Server, token, certFile string, seen *[]string) {
	t.Helper()
	var auth []string
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)
	dir := t.TempDir()
	token = filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "api.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	return ts, token, certFile, &auth
}

// The client trusts the API's certificate from its file and reads the token for every request, so a rotated token is used.
func TestTheClientVerifiesTheAPIsCertificateAndReadsTheTokenForEveryRequest(t *testing.T) {
	ts, token, certFile, seen := serverAndFiles(t)
	c := &Client{Base: ts.URL, TokenFile: token, CertFile: certFile}
	do := func() int {
		res, err := c.Do(context.Background(), http.MethodPost, "/internal/x", []byte("{}"), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		return res.StatusCode
	}
	if code := do(); code != http.StatusNoContent {
		t.Fatalf("%d", code)
	}
	if err := os.WriteFile(token, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	do()
	if got := strings.Join(*seen, "|"); got != "POST /api/v1/internal/x Bearer first|POST /api/v1/internal/x Bearer second" {
		t.Errorf("%s", got)
	}
	// a certificate that is not the API's is refused, and a file without a certificate is an error
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	other.TLS = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
	other.StartTLS()
	defer other.Close()
	bad := &Client{Base: other.URL, TokenFile: token, CertFile: certFile}
	if _, err := bad.Do(context.Background(), http.MethodGet, "/x", nil, 5*time.Second); err == nil {
		t.Error("a certificate that is not the one in the file was accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	_ = os.WriteFile(empty, []byte("nope"), 0o644)
	if _, err := (&Client{Base: ts.URL, TokenFile: token, CertFile: empty}).Do(context.Background(), http.MethodGet, "/x", nil, time.Second); err == nil || !strings.Contains(err.Error(), "holds no certificate") {
		t.Errorf("%v", err)
	}
	if _, err := (&Client{Base: ts.URL, TokenFile: filepath.Join(t.TempDir(), "none"), CertFile: certFile}).Do(context.Background(), http.MethodGet, "/x", nil, time.Second); err == nil || !strings.Contains(err.Error(), "service token") {
		t.Errorf("%v", err)
	}
	// without a certificate file the private link is trusted as it is
	if res, err := (&Client{Base: ts.URL, TokenFile: token}).Do(context.Background(), http.MethodGet, "/x", nil, 5*time.Second); err != nil {
		t.Errorf("%v", err)
	} else {
		_ = res.Body.Close()
	}
	c.CloseIdleConnections()
}

// selfSigned is a certificate for the loopback that is not the one httptest gives every server.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "elsewhere"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestAFailureSaysWhatTheAPIAnswered(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, strings.Repeat("x", 2000))
	}))
	defer ts.Close()
	res, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got := Failure(res).Error()
	if !strings.HasPrefix(got, "the API answered 503: xxx") || len(got) > 600 {
		t.Errorf("%q", got)
	}
}
