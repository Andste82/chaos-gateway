package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// verifies reports whether the certificate in dir verifies, pinned as the DNS proxy pins it (the
// published api.pem as the only root), for the address or name host.
func verifies(t *testing.T, dir, host string) error {
	t.Helper()
	c, err := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: host})
	return err
}

// Issue #64: the DNS proxy connects to the API at the gateway's end of the service link and verifies
// the certificate for that address: the certificate has it.
func TestTheCertificateIsValidForTheServiceAddress(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCertificate(dir, []string{"gw"}, []net.IP{net.ParseIP("192.168.2.67")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{compiler.ServiceHostCIDR.Addr().String(), "127.0.0.1", "192.168.2.67", "gw"} {
		if err := verifies(t, dir, host); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
}

// A self-signed certificate of an earlier version (without the service address) is issued again with
// the same key, names and addresses plus the service address; a certificate someone installed is kept.
func TestAnOlderSelfSignedCertificateGetsTheServiceAddress(t *testing.T) {
	dir := t.TempDir()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	old := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1"), net.ParseIP("192.168.2.67")}
	if err := writeSelfSigned(filepath.Join(dir, "cert.pem"), priv, []string{"localhost", "gw"}, old, time.Now()); err != nil {
		t.Fatal(err)
	}
	if verifies(t, dir, "169.254.100.1") == nil {
		t.Fatal("the old certificate already has the service address")
	}
	c, err := LoadOrCreateCertificate(dir, nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"169.254.100.1", "192.168.2.67", "gw"} {
		if err := verifies(t, dir, host); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	if k, ok := c.PrivateKey.(*ecdsa.PrivateKey); !ok || !k.Equal(priv) {
		t.Error("the key changed")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if _, err := LoadOrCreateCertificate(dir, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "cert.pem")); string(after) != string(before) {
		t.Error("a certificate that has the service address was issued again")
	}

	// installed: another subject (a certificate of the operator's own, M29) is never changed
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "key.pem"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "gw.example"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().AddDate(1, 0, 0), DNSNames: []string{"gw.example"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	installed := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(other, "cert.pem"), installed, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCertificate(other, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(other, "cert.pem")); string(after) != string(installed) {
		t.Error("an installed certificate was changed")
	}
}

func TestPublishCertificateCopiesOnlyTheCertificate(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCertificate(dir, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "service", "api.pem")
	if err := PublishCertificate(dir, dst); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the published certificate does not match cert.pem")
	}
	if info, err := os.Stat(dst); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestPublishCertificateOverwritesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCertificate(dir, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "api.pem")
	if err := os.WriteFile(dst, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PublishCertificate(dir, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "stale" {
		t.Errorf("the stale certificate was not replaced")
	}
}
