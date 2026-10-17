package dnsproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
