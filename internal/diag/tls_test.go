package diag

import (
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

func tlsReq(host string, port int, mods ...func(*model.DiagnosticRequest)) model.DiagnosticRequest {
	return req(model.DiagnosticRequestTypeTls, host, append([]func(*model.DiagnosticRequest){withPort(port)}, mods...)...)
}

func withSNI(s string) func(*model.DiagnosticRequest) {
	return func(r *model.DiagnosticRequest) { r.Sni = &s }
}

func TestTLSShowsWhatAServerPresented(t *testing.T) {
	ca := newPKI(t, "Test CA")
	srv := serveTLS(t, ca.good(t, "broker.test"), 0)
	res := run(t, runner(ca.roots), tlsReq(loop, srv.Port, withSNI("broker.test")))
	if !res.Ok || res.Tls == nil || !*res.Tls.Verified || res.Error != nil {
		t.Fatalf("%+v", res)
	}
	if v := *res.Tls.Version; v != "TLS 1.3" {
		t.Errorf("version %s", v)
	}
	if !strings.HasPrefix(*res.Tls.Cipher, "TLS_") || *res.Tls.Alpn != "h2" {
		t.Errorf("cipher %s alpn %s", *res.Tls.Cipher, str(res.Tls.Alpn))
	}
	cs := *res.Tls.Certificates
	if len(cs) != 1 || *cs[0].Subject != "CN=broker.test" || !strings.Contains(*cs[0].Issuer, "CN=Test CA") || (*cs[0].DnsNames)[0] != "broker.test" {
		t.Fatalf("%+v", cs)
	}
	if !cs[0].NotAfter.After(*cs[0].NotBefore) || cs[0].NotAfter.Location() != time.UTC {
		t.Errorf("validity %v - %v", cs[0].NotBefore, cs[0].NotAfter)
	}
	if got := srv.SNIs(); len(got) != 1 || got[0] != "broker.test" {
		t.Errorf("the server saw the SNI %v", got)
	}
}

func TestTLSReportsTheVersionTheServerChose(t *testing.T) {
	ca := newPKI(t, "Test CA")
	srv := serveTLS(t, ca.good(t, "broker.test"), tls.VersionTLS12)
	res := run(t, runner(ca.roots), tlsReq(loop, srv.Port, withSNI("broker.test")))
	if !res.Ok || *res.Tls.Version != "TLS 1.2" {
		t.Errorf("%+v", res.Tls)
	}
}

func TestTLSCertificatesThatDoNotVerifyAreResultsWithTheirDetails(t *testing.T) {
	ca, other := newPKI(t, "Test CA"), newPKI(t, "Unknown CA")
	now := time.Now()
	for name, c := range map[string]struct {
		cert tls.Certificate
		sni  string
		err  string
	}{
		"expired":       {ca.leaf(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour), "broker.test"), "broker.test", "certificate has expired"},
		"not yet valid": {ca.leaf(t, now.Add(24*time.Hour), now.Add(48*time.Hour), "broker.test"), "broker.test", "certificate is not yet valid"},
		"wrong name":    {ca.good(t, "other.test"), "broker.test", "certificate is valid for other.test, not broker.test"},
		"unknown CA":    {other.good(t, "broker.test"), "broker.test", "certificate signed by unknown authority"},
	} {
		srv := serveTLS(t, c.cert, 0)
		res := run(t, runner(ca.roots), tlsReq(loop, srv.Port, withSNI(c.sni)))
		if res.Ok || res.Tls == nil || *res.Tls.Verified || str(res.Tls.VerifyError) != c.err || str(res.Error) != c.err {
			t.Errorf("%s: %+v / %s", name, res.Tls, str(res.Error))
		}
		// the handshake worked, so the details are there
		if res.Tls == nil || *res.Tls.Version == "" || len(*res.Tls.Certificates) != 1 {
			t.Errorf("%s: no details: %+v", name, res.Tls)
		}
	}
}

func TestTLSToAnAddressSendsNoSNIAndVerifiesTheAddress(t *testing.T) {
	ca := newPKI(t, "Test CA")
	srv := serveTLS(t, ca.good(t, "broker.test"), 0)
	res := run(t, runner(ca.roots), tlsReq(loop, srv.Port))
	// the certificate of the helper carries the loopback address as a SAN
	if !res.Ok {
		t.Fatalf("%+v", res)
	}
	if got := srv.SNIs(); len(got) != 1 || got[0] != "" {
		t.Errorf("an address is no SNI: %q", got)
	}
}

func TestTLSTrustsOnlyTheRootsItIsGiven(t *testing.T) {
	ca := newPKI(t, "Test CA")
	srv := serveTLS(t, ca.good(t, "broker.test"), 0)
	res := run(t, runner(nil), tlsReq(loop, srv.Port, withSNI("broker.test")))
	if res.Ok || str(res.Error) != "certificate signed by unknown authority" {
		t.Errorf("%+v", res)
	}
}

func TestTLSHandshakeFailuresAreResults(t *testing.T) {
	// a server that does not speak TLS
	l, err := net.Listen("tcp", loop+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("SSH-2.0-test\r\n"))
			_ = c.Close()
		}
	}()
	res := run(t, runner(nil), tlsReq(loop, l.Addr().(*net.TCPAddr).Port, withTimeout("2s")))
	if res.Ok || res.Tls != nil || !strings.HasPrefix(str(res.Error), "TLS handshake failed: ") {
		t.Errorf("%+v / %s", res, str(res.Error))
	}
	// a server that takes the connection and says nothing
	silent, err := net.Listen("tcp", loop+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = silent.Close() }()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	res = run(t, runner(nil), tlsReq(loop, silent.Addr().(*net.TCPAddr).Port, withTimeout("150ms")))
	if res.Ok || str(res.Error) != "TLS handshake failed: timed out after 150ms" {
		t.Errorf("%+v / %s", res, str(res.Error))
	}
	// nobody listens
	res = run(t, runner(nil), tlsReq(loop, closedTCPPort(t)))
	if res.Ok || str(res.Error) != "cannot connect: connection refused" {
		t.Errorf("%+v / %s", res, str(res.Error))
	}
}
