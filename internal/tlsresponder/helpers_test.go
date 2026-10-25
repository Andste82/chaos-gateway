package tlsresponder

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// testCAs makes the two authorities in a directory of the test.
func testCAs(t *testing.T) *CAs {
	t.Helper()
	dir := t.TempDir()
	if err := EnsureCAs(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	cas, err := LoadCAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cas
}

func pool(cas ...*CA) *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range cas {
		p.AddCert(c.Cert)
	}
	return p
}

// fakeResolver decides every connection with a function and records what it was asked.
type fakeResolver struct {
	mu    sync.Mutex
	asked []ConnInfo
	fn    func(ConnInfo) (Decision, error)
}

func (f *fakeResolver) Resolve(_ context.Context, c ConnInfo) (Decision, error) {
	f.mu.Lock()
	f.asked = append(f.asked, c)
	f.mu.Unlock()
	return f.fn(c)
}

func (f *fakeResolver) last(t *testing.T) ConnInfo {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) == 0 {
		t.Fatal("the responder asked for no decision")
	}
	return f.asked[len(f.asked)-1]
}

// fakeSink records the reports.
type fakeSink struct {
	mu      sync.Mutex
	reports []model.TlsHandshakeReport
}

func (f *fakeSink) Post(_ context.Context, r []model.TlsHandshakeReport) error {
	f.mu.Lock()
	f.reports = append(f.reports, r...)
	f.mu.Unlock()
	return nil
}

func (f *fakeSink) wait(t *testing.T, n int) []model.TlsHandshakeReport {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.reports) >= n {
			out := append([]model.TlsHandshakeReport(nil), f.reports...)
			f.mu.Unlock()
			return out
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("%d reports, want %d: %+v", len(f.reports), n, f.reports)
	return nil
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reports)
}

// rig is a responder on the loopback with a fake resolver and sink.
type rig struct {
	t      *testing.T
	cas    *CAs
	srv    *Server
	res    *fakeResolver
	sink   *fakeSink
	addr   string
	cancel context.CancelFunc
	// dst is the "original destination" every connection is reported to have had; setDst changes it while the responder runs
	dst   netip.AddrPort
	dstMu sync.RWMutex
	// dial replaces the dialer of the passed-through connections
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

func newRig(t *testing.T, decide func(ConnInfo) Decision, mod func(*Options)) *rig {
	t.Helper()
	cas := testCAs(t)
	iss, err := NewIssuer(cas, &clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, cas: cas, sink: &fakeSink{}, dst: netip.MustParseAddrPort("203.0.113.10:8883")}
	r.res = &fakeResolver{fn: func(c ConnInfo) (Decision, error) { return decide(c), nil }}
	opt := Options{Issuer: iss, Resolver: r.res, Sink: r.sink, Log: slog.New(slog.DiscardHandler),
		OriginalDst:  func(net.Conn) (netip.AddrPort, error) { return r.getDst(), nil },
		Dial:         func(ctx context.Context, n, a string) (net.Conn, error) { return r.dial(ctx, n, a) },
		HelloTimeout: 2 * time.Second, HandshakeTimeout: 3 * time.Second, StallFor: 3 * time.Second, ResolveTimeout: time.Second, DialTimeout: time.Second}
	if mod != nil {
		mod(&opt)
	}
	r.srv = New(opt)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r.addr = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	done := make(chan struct{})
	go func() { _ = r.srv.ServeOn(ctx, ln); close(done) }()
	go r.srv.RunReports(ctx)
	t.Cleanup(func() { cancel(); <-done })
	return r
}

// caseOf is a decision for a case of an overlay.
func caseOf(c model.TlsCaseCase) func(ConnInfo) Decision {
	return func(ConnInfo) Decision {
		return Decision{Generation: 5, Overlay: "11111111-1111-4111-8111-111111111111", Case: &model.TlsCase{Case: c}, Expected: "reject"}
	}
}

// dialTLS connects a client that verifies like a correct device: the roots it trusts, the name it expects, the versions it speaks.
func (r *rig) dialTLS(roots *x509.CertPool, name string, min, max uint16) (*tls.Conn, error) {
	d := &net.Dialer{Timeout: 2 * time.Second}
	raw, err := d.Dial("tcp", r.addr)
	if err != nil {
		return nil, err
	}
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
	c := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: name, MinVersion: min, MaxVersion: max})
	if err := c.Handshake(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return c, nil
}

// validServerCert is the certificate of a real server that a client trusting the test CA accepts: valid, for the name.
func validServerCert(t *testing.T, cas *CAs, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := newSerial()
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, cas.Test.Cert, &key.PublicKey, cas.Test.Key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func readAll(c net.Conn) error {
	_, err := io.Copy(io.Discard, c)
	return err
}

func (r *rig) setDst(a netip.AddrPort) {
	r.dstMu.Lock()
	r.dst = a
	r.dstMu.Unlock()
}

func (r *rig) getDst() netip.AddrPort {
	r.dstMu.RLock()
	defer r.dstMu.RUnlock()
	return r.dst
}

// all returns the reports so far.
func (f *fakeSink) all() []model.TlsHandshakeReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.TlsHandshakeReport(nil), f.reports...)
}
