package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
)

func TestTheKeaHookPostsTheLeaseEventWithTheServiceToken(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/internal/dhcp/lease-events" || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("cgw_svc_secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAOSGW_API", srv.URL)
	t.Setenv("CHAOSGW_SERVICE_TOKEN_FILE", tok)
	for k, v := range map[string]string{"KEA_LEASE4_ADDRESS": "10.10.0.150", "KEA_LEASE4_HWADDR": "02:00:00:00:00:AA", "KEA_SUBNET_ID": "7", "KEA_LEASE4_VALID_LIFETIME": "600", "KEA_LEASE4_HOSTNAME": "esp32"} {
		t.Setenv(k, v)
	}
	var out, errOut bytes.Buffer
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if auth != "Bearer cgw_svc_secret" || got["event"] != "select" || got["ip"] != "10.10.0.150" || got["mac"] != "02:00:00:00:00:aa" || got["subnet_id"] != float64(7) || got["valid_lifetime"] != float64(600) || got["hostname"] != "esp32" {
		t.Errorf("%q %v", auth, got)
	}
	// what it cannot do is reported
	if code := runKeaHook([]string{"lease4_bogus"}, &out, &errOut); code != 1 {
		t.Errorf("an unknown hook point: %d", code)
	}
	if code := runKeaHook(nil, &out, &errOut); code != 2 {
		t.Errorf("no argument: %d", code)
	}
	t.Setenv("CHAOSGW_SERVICE_TOKEN_FILE", filepath.Join(t.TempDir(), "none"))
	errOut.Reset()
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 1 || errOut.Len() == 0 {
		t.Errorf("a missing token file: %d %s", code, errOut.String())
	}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	t.Setenv("CHAOSGW_SERVICE_TOKEN_FILE", tok)
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 1 {
		t.Errorf("a refusal by the API: %d", code)
	}
}

// M6a-09 test: the hook posts over the Unix datagram socket, not HTTP, when one is available.
func TestTheKeaHookPrefersTheDatagramSocketOverHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chaosgw-events.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP was used although the datagram socket is available")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("CHAOSGW_KEA_EVENTS_SOCKET", path)
	t.Setenv("CHAOSGW_API", srv.URL)
	t.Setenv("CHAOSGW_SERVICE_TOKEN_FILE", filepath.Join(t.TempDir(), "none")) // must not be needed
	for k, v := range map[string]string{"KEA_LEASE4_ADDRESS": "10.10.0.150", "KEA_LEASE4_HWADDR": "02:00:00:00:00:AA", "KEA_SUBNET_ID": "7", "KEA_LEASE4_VALID_LIFETIME": "600"} {
		t.Setenv(k, v)
	}
	var out, errOut bytes.Buffer
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	buf := make([]byte, 4096)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf[:n], &got); err != nil {
		t.Fatal(err)
	}
	if got["event"] != "select" || got["ip"] != "10.10.0.150" || got["mac"] != "02:00:00:00:00:aa" || got["subnet_id"] != float64(7) {
		t.Errorf("%v", got)
	}
}

// M6a-09 test: when the datagram socket is unusable (missing here), the hook falls back to HTTP.
func TestTheKeaHookFallsBackToHTTPWithoutTheSocket(t *testing.T) {
	var got map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("cgw_svc_secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAOSGW_KEA_EVENTS_SOCKET", filepath.Join(t.TempDir(), "no-such-socket"))
	t.Setenv("CHAOSGW_API", srv.URL)
	t.Setenv("CHAOSGW_SERVICE_TOKEN_FILE", tok)
	for k, v := range map[string]string{"KEA_LEASE4_ADDRESS": "10.10.0.151", "KEA_LEASE4_HWADDR": "02:00:00:00:00:AB", "KEA_SUBNET_ID": "7", "KEA_LEASE4_VALID_LIFETIME": "600"} {
		t.Setenv(k, v)
	}
	var out, errOut bytes.Buffer
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if got["ip"] != "10.10.0.151" {
		t.Errorf("%v", got)
	}
}

// M6b-08 test: with CHAOSGW_API_CERT set, the hook verifies the API's certificate instead of trusting
// whatever is presented.
func TestTheKeaHookVerifiesTheAPICertificateWhenGiven(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("cgw_svc_secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAOSGW_API", srv.URL)
	t.Setenv("CHAOSGW_SERVICE_TOKEN_FILE", tok)
	t.Setenv("KEA_LEASE4_ADDRESS", "10.10.0.150")
	t.Setenv("KEA_LEASE4_HWADDR", "02:00:00:00:00:aa")
	t.Setenv("KEA_SUBNET_ID", "7")
	t.Setenv("KEA_LEASE4_VALID_LIFETIME", "600")

	certFile := filepath.Join(t.TempDir(), "api.pem")
	write := func(cert *x509.Certificate) {
		raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
		if err := os.WriteFile(certFile, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CHAOSGW_API_CERT", certFile)

	// the server's own certificate: verification succeeds
	write(srv.Certificate())
	var out, errOut bytes.Buffer
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 0 {
		t.Fatalf("a matching certificate was rejected: %d %s", code, errOut.String())
	}

	// a different certificate (httptest.NewTLSServer reuses one fixed built-in certificate for every
	// server, so a genuinely different one has to be generated): verification fails closed, it does
	// not fall back to trusting anything.
	otherDir := t.TempDir()
	if _, err := api.LoadOrCreateCertificate(otherDir, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(otherDir, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 1 {
		t.Errorf("a mismatched certificate was accepted: %d", code)
	}

	// a file that holds no certificate at all is reported, not silently ignored
	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := runKeaHook([]string{"lease4_select"}, &out, &errOut); code != 1 || errOut.Len() == 0 {
		t.Errorf("an invalid CHAOSGW_API_CERT: %d %s", code, errOut.String())
	}
}
