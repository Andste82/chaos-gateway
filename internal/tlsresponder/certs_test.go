package tlsresponder

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const sniName = "broker.example.com"

func leafOf(t *testing.T, iss *Issuer, c model.TlsCaseCase, name string) []*x509.Certificate {
	t.Helper()
	certs, err := iss.Certificates(c, name)
	if err != nil {
		t.Fatal(err)
	}
	var out []*x509.Certificate
	for _, cert := range certs {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, leaf)
	}
	return out
}

func verify(leaf *x509.Certificate, name string, at time.Time, roots ...*CA) error {
	_, err := leaf.Verify(x509.VerifyOptions{Roots: pool(roots...), DNSName: name, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

// Plan §2.8: what a correct client concludes from the certificate of each case, for each key the issuer holds (an
// ECDSA and an RSA one, so a stack that knows only one still judges a certificate).
func TestTheCertificateOfEachCaseFailsTheCheckItIsMeantFor(t *testing.T) {
	cas := testCAs(t)
	iss, err := NewIssuer(cas, clock.NewReal())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	both := []*CA{cas.Test, cas.Unknown}

	t.Run("untrusted_ca is signed by the CA nobody has, whatever else the device trusts", func(t *testing.T) {
		for _, leaf := range leafOf(t, iss, model.TlsCaseCaseUntrustedCa, sniName) {
			var ua x509.UnknownAuthorityError
			if err := verify(leaf, sniName, now, cas.Test); !errors.As(err, &ua) {
				t.Errorf("a device that trusts the test CA: %v", err)
			}
			// the name and the dates are fine: it fails on the issuer alone
			if err := verify(leaf, sniName, now, cas.Unknown); err != nil {
				t.Errorf("a device that trusted the unknown CA would take it: %v", err)
			}
		}
	})
	t.Run("expired is valid but for the date, for a device that trusts the test CA", func(t *testing.T) {
		for _, leaf := range leafOf(t, iss, model.TlsCaseCaseExpired, sniName) {
			var inv x509.CertificateInvalidError
			if err := verify(leaf, sniName, now, cas.Test); !errors.As(err, &inv) || inv.Reason != x509.Expired || !leaf.NotAfter.Before(now.Add(-time.Hour)) {
				t.Errorf("%v (until %s)", err, leaf.NotAfter)
			}
			if err := verify(leaf, sniName, leaf.NotAfter.Add(-time.Hour), cas.Test); err != nil {
				t.Errorf("when it was valid: %v", err)
			}
			// a device without the test CA rejects it too, for the reason its stack checks first (Go the date, OpenSSL the issuer)
			if err := verify(leaf, sniName, now, cas.Unknown); err == nil {
				t.Error("a device without the test CA takes it")
			}
		}
	})
	t.Run("not_yet_valid is valid but for the date", func(t *testing.T) {
		for _, leaf := range leafOf(t, iss, model.TlsCaseCaseNotYetValid, sniName) {
			var inv x509.CertificateInvalidError
			if err := verify(leaf, sniName, now, cas.Test); !errors.As(err, &inv) || inv.Reason != x509.Expired || !leaf.NotBefore.After(now.Add(time.Hour)) {
				t.Errorf("%v (from %s)", err, leaf.NotBefore)
			}
			if err := verify(leaf, sniName, leaf.NotBefore.Add(time.Hour), cas.Test); err != nil {
				t.Errorf("when it is valid: %v", err)
			}
		}
	})
	t.Run("hostname_mismatch is valid but for the name", func(t *testing.T) {
		for _, leaf := range leafOf(t, iss, model.TlsCaseCaseHostnameMismatch, sniName) {
			var he x509.HostnameError
			if err := verify(leaf, sniName, now, cas.Test); !errors.As(err, &he) {
				t.Errorf("%v", err)
			}
			if err := verify(leaf, MismatchName, now, cas.Test); err != nil {
				t.Errorf("for its own name: %v", err)
			}
		}
	})
	t.Run("self_signed is its own issuer and fails whatever the device trusts", func(t *testing.T) {
		for _, leaf := range leafOf(t, iss, model.TlsCaseCaseSelfSigned, sniName) {
			var ua x509.UnknownAuthorityError
			if err := verify(leaf, sniName, now, both...); !errors.As(err, &ua) {
				t.Errorf("%v", err)
			}
			if string(leaf.RawIssuer) != string(leaf.RawSubject) || leaf.IsCA {
				t.Errorf("issuer %s, subject %s, CA %v", leaf.Issuer, leaf.Subject, leaf.IsCA)
			}
			if err := leaf.CheckSignatureFrom(leaf); err == nil {
				// a leaf that is no CA cannot sign: only its own signature is checked below
				t.Log("self-signed leaf checks against itself")
			}
		}
	})
	t.Run("every case is for the name of the ClientHello and has two keys", func(t *testing.T) {
		for _, c := range []model.TlsCaseCase{model.TlsCaseCaseUntrustedCa, model.TlsCaseCaseExpired, model.TlsCaseCaseNotYetValid, model.TlsCaseCaseSelfSigned} {
			leaves := leafOf(t, iss, c, sniName)
			if _, ok := leaves[0].PublicKey.(*ecdsa.PublicKey); !ok {
				t.Errorf("%s: the first certificate is not ECDSA", c)
			}
			if _, ok := leaves[1].PublicKey.(*rsa.PublicKey); !ok {
				t.Errorf("%s: the second certificate is not RSA", c)
			}
			for _, l := range leaves {
				if len(l.DNSNames) != 1 || l.DNSNames[0] != sniName {
					t.Errorf("%s: %v", c, l.DNSNames)
				}
			}
		}
	})
}

func TestACertificateForAnAddressIsAnAddressCertificate(t *testing.T) {
	cas := testCAs(t)
	iss, err := NewIssuer(cas, clock.NewReal())
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafOf(t, iss, model.TlsCaseCaseExpired, "203.0.113.10")[0]
	if len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("203.0.113.10")) {
		t.Errorf("%v %v", leaf.DNSNames, leaf.IPAddresses)
	}
}

func TestTheCertificatesOfACaseAreMadeOnceForAWhileAndOnlyForCertificateCases(t *testing.T) {
	cas := testCAs(t)
	clk := clock.NewFake(time.Now())
	iss, err := NewIssuer(cas, clk)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := iss.Certificates(model.TlsCaseCaseExpired, sniName)
	b, _ := iss.Certificates(model.TlsCaseCaseExpired, sniName)
	if string(a[0].Certificate[0]) != string(b[0].Certificate[0]) {
		t.Error("made twice")
	}
	other, _ := iss.Certificates(model.TlsCaseCaseExpired, "other.example.com")
	if string(a[0].Certificate[0]) == string(other[0].Certificate[0]) {
		t.Error("one certificate for two names")
	}
	// after an hour it is made again: an `expired` certificate that runs out of its margin is not kept for good
	clk.Advance(cacheFor + time.Second)
	c, _ := iss.Certificates(model.TlsCaseCaseExpired, sniName)
	if string(a[0].Certificate[0]) == string(c[0].Certificate[0]) {
		t.Error("kept for good")
	}
	// new authorities drop what was made
	iss.SetCAs(testCAs(t))
	d, _ := iss.Certificates(model.TlsCaseCaseExpired, sniName)
	if string(c[0].Certificate[0]) == string(d[0].Certificate[0]) {
		t.Error("kept after the CAs changed")
	}
	for _, cs := range []model.TlsCaseCase{model.TlsCaseCaseHandshakeReset, model.TlsCaseCaseHandshakeClose, model.TlsCaseCaseHandshakeStall, model.TlsCaseCaseIntercept} {
		if IsCertificateCase(cs) {
			t.Errorf("%s is no certificate case", cs)
		}
		if _, err := iss.Certificates(cs, sniName); err == nil {
			t.Errorf("a certificate for %s", cs)
		}
	}
}
