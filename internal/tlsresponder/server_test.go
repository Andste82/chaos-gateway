package tlsresponder

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

const overlayID = "11111111-1111-4111-8111-111111111111"

// versions are the two TLS versions the cases are tried with (plan §2.8: TLS 1.2 and 1.3).
var versions = []struct {
	name     string
	min, max uint16
}{{"TLS 1.2", tls.VersionTLS12, tls.VersionTLS12}, {"TLS 1.3", tls.VersionTLS13, tls.VersionTLS13}}

// Plan §2.8, S4: a correct client rejects the certificate of every case, with TLS 1.2 and with TLS 1.3, and the
// report says that the device rejected it, with the alert it sent.
func TestACorrectClientRejectsTheCertificateOfEveryCase(t *testing.T) {
	for _, c := range []struct {
		kase   model.TlsCaseCase
		trusts func(*CAs) []*CA // what the client trusts
		check  func(error) bool // the verification error it reports
	}{
		{model.TlsCaseCaseUntrustedCa, func(c *CAs) []*CA { return []*CA{c.Test} }, func(e error) bool {
			var ua x509.UnknownAuthorityError
			return errors.As(e, &ua)
		}},
		{model.TlsCaseCaseSelfSigned, func(c *CAs) []*CA { return []*CA{c.Test, c.Unknown} }, func(e error) bool {
			var ua x509.UnknownAuthorityError
			return errors.As(e, &ua)
		}},
		{model.TlsCaseCaseExpired, func(c *CAs) []*CA { return []*CA{c.Test} }, func(e error) bool {
			var inv x509.CertificateInvalidError
			return errors.As(e, &inv) && inv.Reason == x509.Expired
		}},
		{model.TlsCaseCaseNotYetValid, func(c *CAs) []*CA { return []*CA{c.Test} }, func(e error) bool {
			var inv x509.CertificateInvalidError
			return errors.As(e, &inv) && inv.Reason == x509.Expired
		}},
		{model.TlsCaseCaseHostnameMismatch, func(c *CAs) []*CA { return []*CA{c.Test} }, func(e error) bool {
			var he x509.HostnameError
			return errors.As(e, &he)
		}},
	} {
		for _, v := range versions {
			t.Run(string(c.kase)+" "+v.name, func(t *testing.T) {
				r := newRig(t, caseOf(c.kase), nil)
				_, err := r.dialTLS(pool(c.trusts(r.cas)...), "broker.example.com", v.min, v.max)
				if err == nil {
					t.Fatal("a correct client took the certificate")
				}
				var cv *tls.CertificateVerificationError
				if !errors.As(err, &cv) || !c.check(cv.Err) {
					t.Fatalf("the client's error: %v", err)
				}
				reports := r.sink.wait(t, 1)
				rep := reports[0]
				if rep.Outcome != model.TlsHandshakeReportOutcomeRejected || rep.Alert == nil || deref(rep.Case) != string(c.kase) || rep.Overlay == nil || rep.Overlay.String() != overlayID {
					t.Fatalf("%+v", rep)
				}
				if rep.Client != "127.0.0.1" || rep.OriginalDst != "203.0.113.10:8883" || deref(rep.Sni) != "broker.example.com" || deref(rep.TlsVersion) != v.name {
					t.Errorf("%+v (alert %s)", rep, *rep.Alert)
				}
				if got := *rep.Alert; got != "bad_certificate" && got != "unknown_ca" && got != "certificate_expired" {
					t.Errorf("alert %q", got)
				}
				if got := r.res.last(t); got.SNI != "broker.example.com" || got.Dst != r.dst || got.Client != netip.MustParseAddr("127.0.0.1") {
					t.Errorf("the responder asked for %+v", got)
				}
			})
		}
	}
}

// A device that does not check the certificate completes the handshake: the finding of the security check.
func TestADeviceThatDoesNotVerifyCompletesTheHandshakeAndIsReported(t *testing.T) {
	for _, kase := range []model.TlsCaseCase{model.TlsCaseCaseUntrustedCa, model.TlsCaseCaseSelfSigned, model.TlsCaseCaseExpired, model.TlsCaseCaseNotYetValid, model.TlsCaseCaseHostnameMismatch} {
		for _, v := range versions {
			t.Run(string(kase)+" "+v.name, func(t *testing.T) {
				r := newRig(t, caseOf(kase), nil)
				raw, err := net.Dial("tcp", r.addr)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = raw.Close() }()
				_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
				c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: "broker.example.com", MinVersion: v.min, MaxVersion: v.max}) //nolint:gosec // the device under test
				if err := c.Handshake(); err != nil {
					t.Fatal(err)
				}
				rep := r.sink.wait(t, 1)[0]
				if rep.Outcome != model.TlsHandshakeReportOutcomeAccepted || rep.Alert != nil || deref(rep.TlsVersion) != v.name || deref(rep.Case) != string(kase) {
					t.Errorf("%+v", rep)
				}
			})
		}
	}
}

// The certificate is for the name of the ClientHello; without one, for the name the DNS proxy returned, else for the address.
func TestTheCertificateIsForTheNameOfTheClientHelloOrTheFallbacks(t *testing.T) {
	peer := func(r *rig, name string) *x509.Certificate {
		raw, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = raw.Close() }()
		_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
		c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: name}) //nolint:gosec // the device under test
		if err := c.Handshake(); err != nil {
			t.Fatal(err)
		}
		return c.ConnectionState().PeerCertificates[0]
	}
	r := newRig(t, func(ConnInfo) Decision {
		d := caseOf(model.TlsCaseCaseExpired)(ConnInfo{})
		d.Name = "from-dns.example.com"
		return d
	}, nil)
	if got := peer(r, "broker.example.com").DNSNames; len(got) != 1 || got[0] != "broker.example.com" {
		t.Errorf("with SNI: %v", got)
	}
	// an address has no server name in a ClientHello
	if got := peer(r, "127.0.0.1").DNSNames; len(got) != 1 || got[0] != "from-dns.example.com" {
		t.Errorf("without SNI, with a name the DNS returned: %v", got)
	}
	r2 := newRig(t, caseOf(model.TlsCaseCaseExpired), nil)
	if c := peer(r2, "127.0.0.1"); len(c.DNSNames) != 0 || len(c.IPAddresses) != 1 || c.IPAddresses[0].String() != "203.0.113.10" {
		t.Errorf("without SNI and without a name: %v %v", c.DNSNames, c.IPAddresses)
	}
}

// S4: the handshake cases act on the ClientHello and show no certificate.
func TestTheHandshakeCasesBreakTheHandshakeAfterTheClientHello(t *testing.T) {
	t.Run("reset", func(t *testing.T) {
		r := newRig(t, caseOf(model.TlsCaseCaseHandshakeReset), nil)
		_, err := r.dialTLS(nil, "broker.example.com", tls.VersionTLS12, tls.VersionTLS13)
		if err == nil || !strings.Contains(err.Error(), "reset") {
			t.Errorf("the client's error: %v", err)
		}
		rep := r.sink.wait(t, 1)[0]
		if rep.Outcome != model.TlsHandshakeReportOutcomeResetSent || rep.Alert != nil || deref(rep.Case) != "handshake_reset" {
			t.Errorf("%+v", rep)
		}
	})
	t.Run("close", func(t *testing.T) {
		r := newRig(t, caseOf(model.TlsCaseCaseHandshakeClose), nil)
		_, err := r.dialTLS(nil, "broker.example.com", tls.VersionTLS12, tls.VersionTLS13)
		if !errors.Is(err, io.EOF) {
			t.Errorf("the client's error: %v (want the end of the stream, not a reset)", err)
		}
		if rep := r.sink.wait(t, 1)[0]; rep.Outcome != model.TlsHandshakeReportOutcomeCloseSent || deref(rep.Case) != "handshake_close" {
			t.Errorf("%+v", rep)
		}
	})
	t.Run("stall", func(t *testing.T) {
		r := newRig(t, caseOf(model.TlsCaseCaseHandshakeStall), nil)
		raw, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = raw.Close() }()
		_ = raw.SetDeadline(time.Now().Add(700 * time.Millisecond))
		start := time.Now()
		err = tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: "x.example.com"}).Handshake() //nolint:gosec // the device under test
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() || time.Since(start) < 600*time.Millisecond {
			t.Errorf("the client was not left waiting: %v after %s", err, time.Since(start))
		}
		if rep := r.sink.wait(t, 1)[0]; rep.Outcome != model.TlsHandshakeReportOutcomeStalled || deref(rep.Case) != "handshake_stall" {
			t.Errorf("%+v", rep)
		}
	})
}

// A client that goes away before it sent a ClientHello, or sends something that is no TLS, is not a handshake that
// a case broke or a certificate that was rejected.
func TestAConnectionThatNeverSaysClientHelloIsReportedAsSuch(t *testing.T) {
	r := newRig(t, caseOf(model.TlsCaseCaseExpired), func(o *Options) { o.HelloTimeout = 300 * time.Millisecond })
	// closes at once
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	// plain text
	c, err = net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	_ = readAll(c)
	_ = c.Close()
	// silence
	c, err = net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	reps := r.sink.wait(t, 3)
	var outcomes []string
	for _, rep := range reps {
		outcomes = append(outcomes, string(rep.Outcome)+"/"+deref(rep.Alert))
	}
	slices.Sort(outcomes)
	if want := []string{"client_closed/", "client_closed/not_tls", "timeout/"}; !slices.Equal(outcomes, want) {
		t.Errorf("%v, want %v", outcomes, want)
	}
}

// A connection no case decides is passed on to the destination the device meant, unchanged, and is no handshake of the
// responder's.
func TestAConnectionNoCaseDecidesIsPassedThroughToItsDestination(t *testing.T) {
	cas := testCAs(t)
	// the "real server": a TLS echo with a certificate the client trusts
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	srvCert := validServerCert(t, cas, "real.example.com")
	var upstreamSNI atomic.Value
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				s := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{srvCert}, GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
					upstreamSNI.Store(h.ServerName)
					return nil, nil
				}})
				defer func() { _ = s.Close() }()
				if s.Handshake() != nil {
					return
				}
				_, _ = io.Copy(s, s)
			}()
		}
	}()
	var dialed atomic.Value
	r := newRig(t, func(ConnInfo) Decision { return Decision{Generation: 1, Passthrough: true} }, nil)
	r.dial = func(ctx context.Context, n, a string) (net.Conn, error) {
		dialed.Store(a)
		return (&net.Dialer{}).DialContext(ctx, n, ln.Addr().String())
	}
	c, err := r.dialTLS(pool(cas.Test), "real.example.com", tls.VersionTLS12, tls.VersionTLS13)
	if err != nil {
		t.Fatalf("the client did not get through to the real server: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("%q %v", buf, err)
	}
	if dialed.Load() != "203.0.113.10:8883" || upstreamSNI.Load() != "real.example.com" {
		t.Errorf("dialed %v, the server saw the name %v", dialed.Load(), upstreamSNI.Load())
	}
	if r.srv.PassedThrough() != 1 || r.sink.count() != 0 {
		t.Errorf("passed through %d, reports %d", r.srv.PassedThrough(), r.sink.count())
	}
}

// A connection no case decides that the gateway would not have forwarded (the access matrix keeps the device from the server) is
// reset: the redirect and the responder's own path to the destination must not be the way through.
func TestAConnectionNoCaseDecidesAndTheGatewayWouldNotForwardIsResetNotPassedThrough(t *testing.T) {
	r := newRig(t, func(ConnInfo) Decision { return Decision{Generation: 1, Passthrough: false} }, nil)
	r.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("a connection the gateway would not forward was passed on")
		return nil, errors.New("no")
	}
	_, err := r.dialTLS(nil, "broker.example.com", tls.VersionTLS12, tls.VersionTLS13)
	if err == nil || !strings.Contains(err.Error(), "reset") {
		t.Errorf("the client's error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.srv.Denied() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.srv.Denied() != 1 || r.srv.PassedThrough() != 0 || r.sink.count() != 0 {
		t.Errorf("denied %d, passed through %d, reports %d", r.srv.Denied(), r.srv.PassedThrough(), r.sink.count())
	}
}

// Without the decision the connection is not passed on, however harmless the destination looks: it is reset.
func TestAConnectionThatCannotBeDecidedIsResetAndNeverPassedOn(t *testing.T) {
	r := newRig(t, nil, nil)
	r.res.fn = func(ConnInfo) (Decision, error) { return Decision{}, errors.New("the API is away") }
	r.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("a connection that could not be decided was passed on")
		return nil, errors.New("no")
	}
	_, err := r.dialTLS(nil, "broker.example.com", tls.VersionTLS12, tls.VersionTLS13)
	if err == nil || !strings.Contains(err.Error(), "reset") {
		t.Errorf("the client's error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.srv.ResolveErrors() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.srv.ResolveErrors() != 1 || r.srv.PassedThrough() != 0 || r.sink.count() != 0 {
		t.Errorf("resolve errors %d, passed through %d", r.srv.ResolveErrors(), r.srv.PassedThrough())
	}
}

func TestAConnectionThatIsNoRedirectedOneIsDroppedWithoutADecision(t *testing.T) {
	r := newRig(t, caseOf(model.TlsCaseCaseExpired), func(o *Options) {
		o.OriginalDst = func(net.Conn) (netip.AddrPort, error) { return netip.AddrPort{}, errors.New("no conntrack entry") }
	})
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("%v", err)
	}
	if len(r.res.asked) != 0 {
		t.Error("a decision was asked for a connection without an original destination")
	}
	if err := Probe(r.addr); err != nil {
		t.Errorf("the health check: %v", err)
	}
}

// Plan §2.8, "reset on removal": a connection that a case holds is reset when a configuration of a later generation does
// not hold the case any more; one whose case is still there, and one decided under a later configuration, stay.
func TestTheConnectionsOfARemovedCaseAreReset(t *testing.T) {
	r := newRig(t, caseOf(model.TlsCaseCaseHandshakeStall), func(o *Options) { o.StallFor = 30 * time.Second })
	// the overlay is in the configuration of generation 3 and 5; the decision (generation 5) is not older than either
	cfg := func(gen int64, overlays ...string) *model.TlsServiceConfig {
		c := &model.TlsServiceConfig{Generation: gen}
		for _, o := range overlays {
			id := uuid.MustParse(o)
			c.Cases = append(c.Cases, struct {
				Case        model.TlsServiceConfigCasesCase     `json:"case"`
				CutExisting *bool                               `json:"cut_existing,omitempty"`
				Destination *model.Destination                  `json:"destination,omitempty"`
				Expected    model.TlsServiceConfigCasesExpected `json:"expected"`
				Intercept   *model.InterceptSettings            `json:"intercept,omitempty"`
				Overlay     model.Uuid                          `json:"overlay"`
				PortRanges  *[]model.PortRange                  `json:"port_ranges,omitempty"`
				Ports       *[]int                              `json:"ports,omitempty"`
				Protocol    *model.Protocol                     `json:"protocol,omitempty"`
				Sni         *[]model.Hostname                   `json:"sni,omitempty"`
				Sources     []model.Ipv4OrCidr                  `json:"sources"`
			}{Overlay: id, Sources: []string{"10.10.0.42"}})
		}
		return c
	}
	stalled := func() (net.Conn, <-chan error) {
		c, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		hello := captureHello(t, &tls.Config{ServerName: "x.example.com"})
		_, _ = c.Write(hello)
		done := make(chan error, 1)
		go func() { done <- readAll(c) }()
		return c, done
	}
	c, done := stalled()
	defer func() { _ = c.Close() }()
	r.sink.wait(t, 1) // it is held now

	r.srv.SetConfig(cfg(5, overlayID)) // the case is there: nothing happens
	r.srv.SetConfig(cfg(9))            // an older decision? no: 5 < 9 and the overlay is gone
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "reset") {
			t.Errorf("the connection ended with %v, want a reset", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the connection of the removed case was not reset")
	}
	if r.srv.Resets() != 1 {
		t.Errorf("%d resets", r.srv.Resets())
	}

	// a configuration that is not newer than the decision does not reset it: the decision may be the newer news
	c2, done2 := stalled()
	defer func() { _ = c2.Close() }()
	r.sink.wait(t, 2)
	r.srv.SetConfig(cfg(5))
	select {
	case err := <-done2:
		t.Fatalf("a configuration of the generation of the decision reset the connection: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if r.srv.Resets() != 1 {
		t.Errorf("%d resets", r.srv.Resets())
	}
}

// Every connection is held while a case holds it, and none is after: the registry does not leak.
func TestTheServerHoldsNoConnectionAfterTheyAreDone(t *testing.T) {
	r := newRig(t, caseOf(model.TlsCaseCaseExpired), nil)
	for i := 0; i < 5; i++ {
		_, _ = r.dialTLS(pool(r.cas.Test), "broker.example.com", tls.VersionTLS12, tls.VersionTLS13)
	}
	r.sink.wait(t, 5)
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.srv.cfgMu.Lock()
		n := len(r.srv.held)
		r.srv.cfgMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d connections are still held", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := r.srv.Connections(); got != 5 {
		t.Errorf("%d connections", got)
	}
}

func TestACaseTheResponderDoesNotServeNeverLetsTheDeviceThrough(t *testing.T) {
	r := newRig(t, caseOf(model.TlsCaseCaseIntercept), nil)
	r.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("passed on")
		return nil, errors.New("no")
	}
	_, err := r.dialTLS(nil, "broker.example.com", tls.VersionTLS12, tls.VersionTLS13)
	if err == nil {
		t.Fatal("the handshake completed")
	}
	if r.sink.count() != 0 {
		t.Errorf("a report for a case that was not served")
	}
}
