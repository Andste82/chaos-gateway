package tlsresponder

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// MismatchName is the name of the certificate of the case hostname_mismatch: valid for itself, and never for the name
// the device asked for.
const MismatchName = "chaosgw-mismatch.invalid"

// How the certificates of the cases are placed in time (plan §2.8). The margins are wide, so a device whose clock is a
// day off still sees what the case means.
const (
	expiredFor    = 24 * time.Hour      // the expired certificate ran out this long ago
	notYetValidIn = 48 * time.Hour      // the certificate that is not yet valid starts this far ahead
	leafValidity  = 90 * 24 * time.Hour // how long a certificate lasts once it is valid
	leafBackdate  = time.Hour
	cacheFor      = time.Hour
	cacheMax      = 1024
)

// Clock is the time source of the issuer.
type Clock interface{ Now() time.Time }

// Issuer makes the certificates of the cases. A certificate is for one name, the one the device asked for, and made
// once for a while: the keys are the issuer's own (an ECDSA key and an RSA key, so a device whose stack knows only
// one of them still gets a certificate it can judge instead of a failed handshake).
type Issuer struct {
	cas   *CAs
	clock Clock
	ec    *ecdsa.PrivateKey
	rsa   *rsa.PrivateKey

	mu    sync.Mutex
	cache map[string]cachedCert
}

type cachedCert struct {
	certs []tls.Certificate
	until time.Time
}

// NewIssuer makes the issuer's keys.
func NewIssuer(cas *CAs, clock Clock) (*Issuer, error) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Issuer{cas: cas, clock: clock, ec: ec, rsa: rk, cache: map[string]cachedCert{}}, nil
}

// SetCAs replaces the authorities (the files changed); the certificates made so far are dropped.
func (i *Issuer) SetCAs(cas *CAs) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cas = cas
	i.cache = map[string]cachedCert{}
}

// IsCertificateCase reports whether the case shows a certificate.
func IsCertificateCase(c model.TlsCaseCase) bool {
	switch c {
	case model.TlsCaseCaseUntrustedCa, model.TlsCaseCaseExpired, model.TlsCaseCaseNotYetValid, model.TlsCaseCaseHostnameMismatch, model.TlsCaseCaseSelfSigned:
		return true
	}
	return false
}

// Certificates returns the certificates a case shows for the name: the one signed with the ECDSA key first, then the one
// signed with the RSA key, for the configuration of the connection (crypto/tls takes the first the client can use). The name
// is the server name of the ClientHello, or the address of the destination.
func (i *Issuer) Certificates(c model.TlsCaseCase, name string) ([]tls.Certificate, error) {
	if !IsCertificateCase(c) {
		return nil, fmt.Errorf("the case %s shows no certificate", c)
	}
	now := i.clock.Now()
	key := string(c) + "|" + name
	i.mu.Lock()
	defer i.mu.Unlock()
	if e, ok := i.cache[key]; ok && now.Before(e.until) {
		return e.certs, nil
	}
	var out []tls.Certificate
	for _, k := range []crypto.Signer{i.ec, i.rsa} {
		cert, err := i.issue(c, name, k, now)
		if err != nil {
			return nil, err
		}
		out = append(out, cert)
	}
	if len(i.cache) >= cacheMax {
		for k, e := range i.cache {
			if !now.Before(e.until) {
				delete(i.cache, k)
			}
		}
		for k := range i.cache {
			if len(i.cache) < cacheMax/2 {
				break
			}
			delete(i.cache, k)
		}
	}
	i.cache[key] = cachedCert{certs: out, until: now.Add(cacheFor)}
	return out, nil
}

func (i *Issuer) issue(c model.TlsCaseCase, name string, key crypto.Signer, now time.Time) (tls.Certificate, error) {
	serial, err := newSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	subject := name
	if c == model.TlsCaseCaseHostnameMismatch {
		subject = MismatchName
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: shorten(subject), Organization: []string{"Chaos Gateway"}},
		NotBefore:             now.Add(-leafBackdate),
		NotAfter:              now.Add(leafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(subject); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{subject}
	}
	switch c {
	case model.TlsCaseCaseExpired:
		tmpl.NotBefore, tmpl.NotAfter = now.Add(-expiredFor-leafValidity), now.Add(-expiredFor)
	case model.TlsCaseCaseNotYetValid:
		tmpl.NotBefore, tmpl.NotAfter = now.Add(notYetValidIn), now.Add(notYetValidIn+leafValidity)
	}
	parent, signer := tmpl, key // self-signed
	switch c {
	case model.TlsCaseCaseUntrustedCa:
		parent, signer = i.cas.Unknown.Cert, i.cas.Unknown.Key
	case model.TlsCaseCaseExpired, model.TlsCaseCaseNotYetValid, model.TlsCaseCaseHostnameMismatch:
		parent, signer = i.cas.Test.Cert, i.cas.Test.Key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), signer)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// shorten fits a name into the 64 characters of a common name.
func shorten(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}
