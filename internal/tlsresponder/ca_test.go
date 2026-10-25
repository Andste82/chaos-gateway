package tlsresponder

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
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

// The test CA can be replaced (M22): a new certificate and key, the old files gone, the unknown CA untouched, the files as protected as
// before. Devices that installed the old certificate trust nothing the gateway shows them afterwards, which is what a regeneration is.
func TestTheTestCAIsReplacedAndTheUnknownCAIsNot(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureCAs(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	old, err := LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	unknownBefore, _ := os.ReadFile(filepath.Join(dir, "unknown-ca.pem"))
	got, err := RegenerateTestCA(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(fresh.Test.Cert.Raw, old.Test.Cert.Raw) || fresh.Test.Key.Equal(old.Test.Key) || fresh.Test.Cert.Subject.CommonName != TestCAName {
		t.Errorf("the CA is not a new one: %s", fresh.Test.Cert.Subject)
	}
	if !bytes.Equal(got, fresh.Test.PEM) {
		t.Error("the returned certificate is not the file")
	}
	if b, _ := pem.Decode(got); b == nil || b.Type != "CERTIFICATE" || bytes.Contains(got, []byte("PRIVATE")) {
		t.Errorf("%s", got)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "unknown-ca.pem")); !bytes.Equal(unknownBefore, after) || !bytes.Equal(fresh.Unknown.Cert.Raw, old.Unknown.Cert.Raw) {
		t.Error("the unknown CA changed")
	}
	if fi, err := os.Stat(filepath.Join(dir, "test-ca.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the key: %v %v", fi, err)
	}
	// the certificates of the old CA do not chain to the new one
	iss, err := NewIssuer(old, &clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	certs, err := iss.Certificates("expired", "x.example.com")
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(certs[0].Certificate[0])
	if err := leaf.CheckSignatureFrom(fresh.Test.Cert); err == nil {
		t.Error("a certificate of the old CA is signed by the new one")
	}
	// a directory that was never made is made
	if _, err := RegenerateTestCA(filepath.Join(t.TempDir(), "new", "ca"), time.Now()); err != nil {
		t.Errorf("a new directory: %v", err)
	}
}

// A certificate that does not belong to the key beside it is not a CA: the moment between the two writes of a regeneration, which a service that
// reads then must see as a CA that cannot be read yet (and keep what it has).
func TestACertificateThatDoesNotBelongToItsKeyIsNotLoaded(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureCAs(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := EnsureCAs(other, time.Now()); err != nil {
		t.Fatal(err)
	}
	// the key of another CA beside the certificate
	key, _ := os.ReadFile(filepath.Join(other, "test-ca.key"))
	if err := os.WriteFile(filepath.Join(dir, "test-ca.key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCAs(dir); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("%v", err)
	}
}

// startedClock tells when a watcher first waits for a tick, which it does after it has looked at the files once.
type startedClock struct {
	clock.Clock
	once    sync.Once
	started chan struct{}
}

func (c *startedClock) After(d time.Duration) <-chan time.Time {
	c.once.Do(func() { close(c.started) })
	return c.Clock.After(d)
}

// A service that reloads on a change of the files meets the moment between the two writes of a regeneration (the new certificate, the old key):
// it keeps the CA it has, and loads the new one when the key is there.
func TestTheWatcherKeepsTheOldCAWhileTheNewCertificateLacksItsKey(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureCAs(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	cas, err := LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	iss, err := NewIssuer(cas, &clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	cert := func() []byte {
		certs, err := iss.Certificates(model.TlsCaseCaseExpired, "a.example.com")
		if err != nil {
			t.Fatal(err)
		}
		return certs[0].Certificate[0]
	}
	before := cert()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	clk := &startedClock{Clock: &clock.Real{}, started: make(chan struct{})}
	go func() {
		WatchCAs(ctx, clk, dir, iss, 20*time.Millisecond, slog.New(slog.DiscardHandler))
		close(done)
	}()
	defer func() { cancel(); <-done }()
	<-clk.started // the watcher has taken the stamp of the files it compares with

	fresh := t.TempDir()
	if err := EnsureCAs(fresh, time.Now()); err != nil {
		t.Fatal(err)
	}
	newCert, _ := os.ReadFile(filepath.Join(fresh, "test-ca.pem"))
	newKey, _ := os.ReadFile(filepath.Join(fresh, "test-ca.key"))
	if err := os.WriteFile(filepath.Join(dir, "test-ca.pem"), newCert, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // many ticks of the watcher
	if !bytes.Equal(cert(), before) {
		t.Fatal("the issuer took a certificate that does not match its key")
	}
	if err := os.WriteFile(filepath.Join(dir, "test-ca.key"), newKey, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for bytes.Equal(cert(), before) {
		if time.Now().After(deadline) {
			t.Fatal("the issuer does not take the new CA once the key is there")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The files are written by one regeneration at a time and a start that makes the CAs does not meet one half way.
func TestRegeneratingTheTestCAWhileAnotherStartsLeavesAPair(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureCAs(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 8)
	for i := 0; i < 4; i++ {
		go func() { _, err := RegenerateTestCA(dir, time.Now()); done <- err }()
		go func() { done <- EnsureCAs(dir, time.Now()) }()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if _, err := LoadCAs(dir); err != nil {
		t.Errorf("the files are not a pair: %v", err)
	}
}
