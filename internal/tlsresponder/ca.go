package tlsresponder

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Chaos Gateway keeps two CAs (plan §2.8). The test CA may be installed on development firmware, so that it can tell
// an expired certificate or one for another name from one it does not know; it signs the certificates of the cases
// expired, not_yet_valid and hostname_mismatch. The unknown CA is never distributed: it signs the case untrusted_ca,
// which every firmware must reject, whatever it trusts.
const (
	testCAFile    = "test-ca"
	unknownCAFile = "unknown-ca"
	// caValidity is how long a CA lasts: the certificates of devices that trust the test CA must not expire under them.
	caValidity = 10 * 365 * 24 * time.Hour
	// caBackdate is how far back the CA starts: the certificate of the case `expired` ran out a day ago and began three
	// months before that, and the CA that signed it was valid then.
	caBackdate = 5 * 365 * 24 * time.Hour
)

// CA is a certificate authority with its key.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	// PEM is the certificate, never the key.
	PEM []byte
}

// CAs are the two authorities of the responder.
type CAs struct {
	Test, Unknown *CA
}

// EnsureCAs makes the two CAs in dir when they are not there (a first start): the certificates in test-ca.pem and
// unknown-ca.pem, readable by the service containers, the keys beside them (*.key, mode 0600: the responder runs as the
// user that owns the directory). A CA that is there is never replaced. The responder's container mounts the directory
// read-only.
func EnsureCAs(dir string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, c := range []struct{ file, name string }{
		{testCAFile, "Chaos Gateway Test CA"},
		{unknownCAFile, "Chaos Gateway Unknown CA (never distributed)"},
	} {
		if _, err := loadCA(dir, c.file); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("the CA %s in %s: %w", c.file, dir, err)
		}
		if err := writeCA(dir, c.file, c.name, now); err != nil {
			return err
		}
	}
	return nil
}

// LoadCAs reads the two CAs from dir.
func LoadCAs(dir string) (*CAs, error) {
	test, err := loadCA(dir, testCAFile)
	if err != nil {
		return nil, fmt.Errorf("the test CA in %s: %w", dir, err)
	}
	unknown, err := loadCA(dir, unknownCAFile)
	if err != nil {
		return nil, fmt.Errorf("the unknown CA in %s: %w", dir, err)
	}
	return &CAs{Test: test, Unknown: unknown}, nil
}

// TestCAPEM returns the certificate of the test CA in dir, the file that is downloaded. The key is never read.
func TestCAPEM(dir string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, testCAFile+".pem"))
	if err != nil {
		return nil, err
	}
	if b, _ := pem.Decode(raw); b == nil || b.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no certificate", testCAFile+".pem")
	}
	return raw, nil
}

func writeCA(dir, file, name string, now time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := newSerial()
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: name, Organization: []string{"Chaos Gateway"}},
		NotBefore:             now.Add(-caBackdate),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// the key first: a certificate without its key is never left behind for a start to trust
	if err := writeFileAtomic(filepath.Join(dir, file+".key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, file+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func loadCA(dir, file string) (*CA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, file+".pem"))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, file+".key"))
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
	if cb == nil || cb.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s.pem holds no certificate", file)
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, fmt.Errorf("%s.key holds no key", file)
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("%s.pem is not a CA certificate", file)
	}
	return &CA{Cert: cert, Key: key, PEM: certPEM}, nil
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
}

// writeFileAtomic writes the file through a temporary one in the same directory, so a reader never sees half of it.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ca-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
