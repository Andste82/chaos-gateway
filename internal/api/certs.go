package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// installedCertFile is the file in the TLS directory that holds an installed certificate: the chain and the private key,
// one file, so that one rename replaces both (mode 0600, the key is in it).
const installedCertFile = "ui.pem"

// minRSABits is the smallest RSA key an installed certificate may have.
const minRSABits = 2048

// CertificateError is why an uploaded certificate or key was refused: Field is the member of the request it is about
// (`certificate` or `private_key`), Msg says what the sender can fix.
type CertificateError struct{ Field, Msg string }

func (e *CertificateError) Error() string { return e.Field + ": " + e.Msg }

func refuse(field, format string, a ...any) error {
	return &CertificateError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// Certificates holds the certificates of the API's listeners (plan §2.16, M29). The gateway's own certificate (cert.pem
// and key.pem in the directory, self-signed, made at the first start) stays for the loopback address and the link to the
// service namespace: the DNS proxy, the TLS responder and Kea's hook verify it, and it is published to them. A
// certificate installed with PUT /system/certificate (ui.pem) is presented on every other address the API listens on, so
// it does not need to name the addresses of the service link. Both are looked up for every handshake: a replacement
// takes effect for the next new connection without a restart.
type Certificates struct {
	dir string
	log *slog.Logger
	now func() time.Time

	// change serializes Install and Reset as wholes (the file and the pointer), so that the file on disk and the
	// certificate that is presented cannot end up different; mu only guards the pointers and is never held across I/O.
	change sync.Mutex

	mu        sync.RWMutex
	own       tls.Certificate
	installed *tls.Certificate
}

// OpenCertificates loads (or makes, on the first start) the gateway's own certificate for the names and addresses given
// (LoadOrCreateCertificate) and loads the installed one if there is one. An installed certificate that cannot be read is
// not a failed start: the gateway presents its own, logs why and the file stays for the administrator to look at or to
// remove with DELETE /system/certificate.
func OpenCertificates(dir string, names []string, ips []net.IP, now func() time.Time, log *slog.Logger) (*Certificates, error) {
	own, err := LoadOrCreateCertificate(dir, names, ips, now())
	if err != nil {
		return nil, err
	}
	c := &Certificates{dir: dir, log: log, now: now, own: own}
	switch inst, err := loadInstalled(filepath.Join(dir, installedCertFile)); {
	case err == nil:
		c.installed = inst
	case !errors.Is(err, os.ErrNotExist):
		log.Warn("the installed HTTPS certificate cannot be used; the gateway's own certificate is presented", "file", filepath.Join(dir, installedCertFile), "error", err)
	}
	return c, nil
}

// loadInstalled reads the chain and the key from path.
func loadInstalled(path string) (*tls.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(raw, raw)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// TLSConfig is the configuration of the HTTPS listeners: the certificate is chosen per connection.
func (c *Certificates) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: c.serve}
}

// serve chooses the certificate for a handshake: the installed one, except on the addresses the gateway's own services
// reach the API at.
func (c *Certificates) serve(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.installed != nil && !internalAddr(h.Conn) {
		return c.installed, nil
	}
	return &c.own, nil
}

// internalAddr reports whether the connection came in on the loopback address or on the gateway's end of the link to the
// service namespace: the addresses of builtinIPs, which the gateway's own certificate names for the services to verify.
func internalAddr(conn net.Conn) bool {
	if conn == nil {
		return false
	}
	a, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	for _, ip := range builtinIPs() {
		if a.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// Info describes the certificate the management listeners present.
func (c *Certificates) Info() model.CertificateInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.installed != nil {
		return certificateInfo(*c.installed, model.Installed)
	}
	return certificateInfo(c.own, model.Own)
}

func certificateInfo(cert tls.Certificate, source model.CertificateInfoSource) model.CertificateInfo {
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil { // a certificate that tls.X509KeyPair accepted parses
		return model.CertificateInfo{Source: &source}
	}
	sum := sha256.Sum256(leaf.Raw)
	ips := make([]string, 0, len(leaf.IPAddresses))
	for _, ip := range leaf.IPAddresses {
		ips = append(ips, ip.String())
	}
	dns := append([]string{}, leaf.DNSNames...)
	self := bytes.Equal(leaf.RawIssuer, leaf.RawSubject) && leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil
	fp, serial, chain := hex.EncodeToString(sum[:]), leaf.SerialNumber.Text(16), len(cert.Certificate)
	subject, issuer := leaf.Subject.String(), leaf.Issuer.String()
	return model.CertificateInfo{Subject: &subject, Issuer: &issuer, NotBefore: &leaf.NotBefore, NotAfter: &leaf.NotAfter,
		DnsNames: &dns, IpAddresses: &ips, SelfSigned: &self, FingerprintSha256: &fp, SerialNumber: &serial, Source: &source, ChainLength: &chain}
}

// Install checks the chain and the key and, if they are fine, makes them the certificate of the management listeners.
// It writes the file before it changes what is presented: a failing write leaves everything as it was.
func (c *Certificates) Install(certPEM, keyPEM string) (model.CertificateInfo, error) {
	c.change.Lock()
	defer c.change.Unlock()
	cert, err := parseUpload(certPEM, keyPEM, c.now())
	if err != nil {
		return model.CertificateInfo{}, err
	}
	var file bytes.Buffer
	for _, der := range cert.Certificate {
		_ = pem.Encode(&file, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return model.CertificateInfo{}, refuse("private_key", "the key cannot be stored: %v", err)
	}
	_ = pem.Encode(&file, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := writeSecretFile(filepath.Join(c.dir, installedCertFile), file.Bytes()); err != nil {
		return model.CertificateInfo{}, fmt.Errorf("storing the certificate: %w", err)
	}
	c.mu.Lock()
	c.installed = &cert
	c.mu.Unlock()
	return c.Info(), nil
}

// Reset removes the installed certificate: the listeners present the gateway's own again.
func (c *Certificates) Reset() (model.CertificateInfo, error) {
	c.change.Lock()
	defer c.change.Unlock()
	if err := os.Remove(filepath.Join(c.dir, installedCertFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return model.CertificateInfo{}, fmt.Errorf("removing the installed certificate: %w", err)
	}
	c.mu.Lock()
	c.installed = nil
	c.mu.Unlock()
	return c.Info(), nil
}

// parseUpload checks what PUT /system/certificate sends and returns the pair ready to serve.
func parseUpload(certPEM, keyPEM string, now time.Time) (tls.Certificate, error) {
	var chain [][]byte
	rest := []byte(certPEM)
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type != "CERTIFICATE" {
			return tls.Certificate{}, refuse("certificate", "found a %q block; only CERTIFICATE blocks belong here (the key goes in private_key)", b.Type)
		}
		chain = append(chain, b.Bytes)
	}
	if len(chain) == 0 {
		return tls.Certificate{}, refuse("certificate", "no PEM certificate found")
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		return tls.Certificate{}, refuse("certificate", "text after the last PEM block")
	}
	certs := make([]*x509.Certificate, len(chain))
	for i, der := range chain {
		x, err := x509.ParseCertificate(der)
		if err != nil {
			return tls.Certificate{}, refuse("certificate", "certificate %d of the chain cannot be parsed: %v", i+1, err)
		}
		certs[i] = x
	}
	for i := 0; i+1 < len(certs); i++ {
		if err := certs[i].CheckSignatureFrom(certs[i+1]); err != nil {
			return tls.Certificate{}, refuse("certificate", "certificate %d is not signed by certificate %d: the chain must run from the leaf to the issuer (%v)", i+1, i+2, err)
		}
	}
	// the leaf's validity is checked below, with its names; an issuer in the chain that is not valid now fails every client's
	// verification as an expired leaf does
	for i, c := range certs[1:] {
		switch {
		case now.After(c.NotAfter):
			return tls.Certificate{}, refuse("certificate", "certificate %d of the chain expired on %s", i+2, c.NotAfter.UTC().Format(time.RFC3339))
		case now.Before(c.NotBefore):
			return tls.Certificate{}, refuse("certificate", "certificate %d of the chain is not valid before %s", i+2, c.NotBefore.UTC().Format(time.RFC3339))
		}
	}
	leaf := certs[0]
	switch {
	case now.After(leaf.NotAfter):
		return tls.Certificate{}, refuse("certificate", "expired on %s", leaf.NotAfter.UTC().Format(time.RFC3339))
	case now.Before(leaf.NotBefore):
		return tls.Certificate{}, refuse("certificate", "not valid before %s", leaf.NotBefore.UTC().Format(time.RFC3339))
	case len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0:
		return tls.Certificate{}, refuse("certificate", "it names no DNS name and no IP address (subject alternative names); browsers and clients refuse it")
	}
	if len(leaf.ExtKeyUsage) > 0 && !hasServerAuth(leaf) {
		return tls.Certificate{}, refuse("certificate", "its extended key usage does not allow a server (serverAuth)")
	}
	if blk, _ := pem.Decode([]byte(keyPEM)); blk == nil {
		return tls.Certificate{}, refuse("private_key", "no PEM private key found")
	} else if blk.Headers["DEK-Info"] != "" || blk.Type == "ENCRYPTED PRIVATE KEY" {
		return tls.Certificate{}, refuse("private_key", "the key is encrypted with a password; upload it without one")
	}
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return tls.Certificate{}, refuse("private_key", "%v", err)
	}
	if k, ok := cert.PrivateKey.(*rsa.PrivateKey); ok && k.N.BitLen() < minRSABits {
		return tls.Certificate{}, refuse("private_key", "an RSA key needs at least %d bits, this one has %d", minRSABits, k.N.BitLen())
	}
	switch cert.PrivateKey.(type) {
	case *rsa.PrivateKey, *ecdsa.PrivateKey, ed25519.PrivateKey:
	default:
		return tls.Certificate{}, refuse("private_key", "unsupported key type %T", cert.PrivateKey)
	}
	// A certificate that parses can still be one that no handshake uses (an ECDSA key on P-224 is the case): installed, it would
	// lock the administrator out of the UI and API on the management addresses. Ask the TLS stack the way a client does.
	hello := &tls.ClientHelloInfo{
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		SignatureSchemes: []tls.SignatureScheme{tls.PSSWithSHA256, tls.PSSWithSHA384, tls.PSSWithSHA512, tls.PKCS1WithSHA256, tls.PKCS1WithSHA384,
			tls.PKCS1WithSHA512, tls.ECDSAWithP256AndSHA256, tls.ECDSAWithP384AndSHA384, tls.ECDSAWithP521AndSHA512, tls.Ed25519},
		SupportedCurves: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521},
	}
	if err := hello.SupportsCertificate(&cert); err != nil {
		return tls.Certificate{}, refuse("private_key", "no TLS handshake can use this key and certificate: %v", err)
	}
	return cert, nil
}

func hasServerAuth(x *x509.Certificate) bool {
	for _, u := range x.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

// writeSecretFile writes data to path with mode 0600 through a temporary file in the same directory and a rename, so a
// reader sees the old file or the new one and never half of it.
func writeSecretFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
