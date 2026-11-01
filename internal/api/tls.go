package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// selfSignedName is the subject of the certificate the API makes itself.
const selfSignedName = "Chaos Gateway"

// builtinIPs are in every certificate the API makes: the loopback, and the gateway's end of the link to
// the service namespace, which the DNS proxy connects to and verifies the certificate for (issue #64).
func builtinIPs() []net.IP {
	return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1"), net.IP(compiler.ServiceHostCIDR.Addr().AsSlice())}
}

// LoadOrCreateCertificate returns the gateway's own certificate of the API: the one in dir (cert.pem and
// key.pem, both mode 0600 and written through a temporary file and a rename) or, on the first start, a
// self-signed one for the host name and addresses given (plan §2.16). It is what the service containers
// verify (PublishCertificate); the certificate an operator installs is another one, kept by Certificates
// beside it (M29), and the files here are not the place to replace the certificate of the UI.
// A self-signed certificate of an earlier version that lacks one of the built-in addresses (the service
// address, issue #64) is issued again with the same key, names and addresses plus the missing ones; a
// certificate someone installed is never changed.
func LoadOrCreateCertificate(dir string, names []string, ips []net.IP, now time.Time) (tls.Certificate, error) {
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if c, err := tls.LoadX509KeyPair(cert, key); err == nil {
		return reissueWithoutBuiltins(c, cert, key, now)
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, fmt.Errorf("the HTTPS certificate in %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writeSelfSigned(cert, priv, append([]string{"localhost"}, names...), append(builtinIPs(), ips...), now); err != nil {
		return tls.Certificate{}, err
	}
	if err := writeSecretFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(cert, key)
}

// reissueWithoutBuiltins returns c, or, when c is the API's own self-signed certificate and lacks a
// built-in address, a new one for its names and addresses and the missing ones, signed with its key.
func reissueWithoutBuiltins(c tls.Certificate, cert, key string, now time.Time) (tls.Certificate, error) {
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return tls.Certificate{}, err
	}
	priv, ok := c.PrivateKey.(*ecdsa.PrivateKey)
	if !ok || leaf.Subject.CommonName != selfSignedName || !bytes.Equal(leaf.RawIssuer, leaf.RawSubject) {
		return c, nil // installed by someone: theirs to change
	}
	ips := leaf.IPAddresses
	for _, b := range builtinIPs() {
		if !slices.ContainsFunc(ips, b.Equal) {
			ips = append(ips, b)
		}
	}
	if len(ips) == len(leaf.IPAddresses) {
		return c, nil
	}
	if err := writeSelfSigned(cert, priv, leaf.DNSNames, ips, now); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(cert, key)
}

// writeSelfSigned writes a self-signed certificate for the names and addresses to path, valid for ten years.
func writeSelfSigned(path string, priv *ecdsa.PrivateKey, names []string, ips []net.IP, now time.Time) error {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: selfSignedName, Organization: []string{selfSignedName}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              names,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	// through a temporary file and a rename like every file of secrets/: a torn cert.pem or key.pem makes the
	// next start fail for good, and the certificate is read by this process alone (the services get a copy)
	return writeSecretFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// PublishCertificate copies the API's certificate (never the key) from dir's cert.pem to path, so the
// service containers (the DNS proxy, Kea's hook) can verify the API's certificate instead of trusting
// whatever presents it (M6b-08). Call it after LoadOrCreateCertificate, which has already ensured dir
// holds a current certificate.
func PublishCertificate(dir, path string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".api-cert-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	// readable by the service containers' users, which differ from ours
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
