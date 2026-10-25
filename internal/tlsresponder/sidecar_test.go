package tlsresponder

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The unit tests of the sidecar's Python code (the rules of an interception case and what the addon makes of mitmproxy's failure text) run
// with the Go tests, with the interpreter that has mitmproxy (the tests of the addon's helpers need it to import the addon) or else the
// system's python3 (the tests of the rules need nothing).
func TestThePythonTestsOfTheSidecarPass(t *testing.T) {
	py, ok := testbed.MitmPython()
	if !ok {
		var err error
		if py, err = exec.LookPath("python3"); err != nil {
			if os.Getenv(testbed.RequireMitmEnv) == "1" {
				t.Fatal("python3 is not installed, and this run requires the sidecar's tests")
			}
			t.Skip("python3 is not installed")
		}
	}
	cmd := exec.Command(py, "-I", "-m", "unittest", "discover", "-s", "tests", "-t", ".")
	cmd.Dir = testbed.SidecarDir()
	cmd.Env = append(testbed.CleanEnv(os.Environ()), "PYTHONPATH="+testbed.SidecarDir(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The proxy talks to the API over HTTPS with the API's certificate from a file and the service token: the client of the sidecar (standard
// library only) verifies the certificate against that file, sends the token, and refuses a server whose certificate is not the one in the file.
func TestTheSidecarsClientVerifiesTheAPIsCertificateAndSendsTheToken(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv(testbed.RequireMitmEnv) == "1" {
			t.Fatal("python3 is not installed")
		}
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	var gotAuth string
	good := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"generation": 7, "cases": []}`)
	}))
	t.Cleanup(good.Close)
	// a second server with a certificate of its own (httptest's servers all present the same one)
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	other.TLS = &tls.Config{Certificates: []tls.Certificate{selfSignedLoopback(t)}}
	other.StartTLS()
	t.Cleanup(other.Close)
	certOf := func(s *httptest.Server, name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(base, cert string) (string, error) {
		script := fmt.Sprintf(`
import sys
from chaosgw_tlsproxy.api import ServiceClient
c = ServiceClient(%q, %q, %q)
print(c.config(0, 5)["generation"])
`, base, token, cert)
		cmd := exec.Command(py, "-c", script)
		cmd.Dir = testbed.SidecarDir()
		cmd.Env = append(testbed.CleanEnv(os.Environ()), "PYTHONPATH="+testbed.SidecarDir(), "PYTHONDONTWRITEBYTECODE=1")
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := run(good.URL, certOf(good, "good.pem")); err != nil || out != "7" || gotAuth != "Bearer s3cret" {
		t.Errorf("%v %q (authorization %q)", err, out, gotAuth)
	}
	if out, err := run(other.URL, certOf(good, "good.pem")); err == nil {
		t.Errorf("a certificate that is not the one in the file was taken: %q", out)
	}
}

// selfSignedLoopback is a certificate for 127.0.0.1 that is nobody's but its own.
func selfSignedLoopback(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "other"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
