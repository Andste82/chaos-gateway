package tlsresponder

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheTwoCAsAreMadeOnceWithTheirKeysProtected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tlsca")
	now := time.Now()
	if err := EnsureCAs(dir, now); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		name string
		mode os.FileMode
	}{{"test-ca.pem", 0o644}, {"test-ca.key", 0o600}, {"unknown-ca.pem", 0o644}, {"unknown-ca.key", 0o600}} {
		fi, err := os.Stat(filepath.Join(dir, f.name))
		if err != nil || fi.Mode().Perm() != f.mode {
			t.Errorf("%s: %v %v", f.name, fi, err)
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, "test-ca.pem"))
	// a CA that is there is never replaced: the devices that installed it would trust nothing any more
	if err := EnsureCAs(dir, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "test-ca.pem")); !bytes.Equal(before, after) {
		t.Error("the test CA was replaced")
	}
	cas, err := LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cas.Test.Cert.Subject.CommonName == cas.Unknown.Cert.Subject.CommonName || bytes.Equal(cas.Test.Cert.Raw, cas.Unknown.Cert.Raw) {
		t.Error("the two CAs are the same")
	}
	for _, c := range []*CA{cas.Test, cas.Unknown} {
		if !c.Cert.IsCA || !c.Cert.BasicConstraintsValid || c.Cert.KeyUsage&x509.KeyUsageCertSign == 0 || c.Cert.NotAfter.Before(now.AddDate(9, 0, 0)) {
			t.Errorf("%s: %+v", c.Cert.Subject, c.Cert)
		}
	}
}

// The download is the test CA's certificate: never the unknown CA, never a key.
func TestTheDownloadIsTheTestCAsCertificateAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureCAs(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	cas, err := LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := TestCAPEM(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, cas.Test.PEM) || bytes.Contains(got, []byte("PRIVATE KEY")) || bytes.Contains(got, cas.Unknown.PEM) {
		t.Errorf("%s", got)
	}
	if b, _ := pem.Decode(got); b == nil || b.Type != "CERTIFICATE" {
		t.Errorf("not a certificate: %s", got)
	}
	// a directory without a CA, and one whose file is not a certificate, are errors
	if _, err := TestCAPEM(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%v", err)
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "test-ca.pem"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := TestCAPEM(bad); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := LoadCAs(bad); err == nil {
		t.Error("garbage accepted")
	}
}
