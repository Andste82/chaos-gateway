package diag

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The targets of these tests are servers on the loopback of the test process: the known targets that every machine
// has (the namespaces of the testbed are the same thing one level up, internal/api/e2e_diagnostics_test.go).

// loop is the address of the servers.
const loop = "127.0.0.1"

func runner(roots *x509.CertPool, servers ...netip.AddrPort) *Runner {
	return New(Config{
		Resolvers: func() []netip.AddrPort { return servers },
		Roots:     func() *x509.CertPool { return roots },
	})
}

// run runs a request and fails the test when the diagnostic itself breaks.
func run(t *testing.T, r *Runner, req model.DiagnosticRequest) model.DiagnosticResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := r.Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.From == nil || *res.From != "gateway" || res.Type != string(req.Type) || res.StartedAt.IsZero() {
		t.Errorf("the result does not say what ran: %+v", res)
	}
	return res
}

func req(typ model.DiagnosticRequestType, host string, mods ...func(*model.DiagnosticRequest)) model.DiagnosticRequest {
	r := model.DiagnosticRequest{Type: typ, Host: host}
	for _, m := range mods {
		m(&r)
	}
	return r
}

func withPort(p int) func(*model.DiagnosticRequest) {
	return func(r *model.DiagnosticRequest) { r.Port = &p }
}

func withTimeout(d string) func(*model.DiagnosticRequest) {
	return func(r *model.DiagnosticRequest) { r.Timeout = &d }
}

// ---- a blocking network: every dial waits for its context

type blackhole struct{ HostNet }

func (blackhole) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, &net.OpError{Op: "dial", Err: timeoutErr{}}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// ---- certificates

type pki struct {
	ca    *x509.Certificate
	key   *ecdsa.PrivateKey
	roots *x509.CertPool
}

func newPKI(t *testing.T, name string) *pki {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name, Organization: []string{"Chaos Gateway test"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return &pki{ca: ca, key: key, roots: pool}
}

var serial int64 = 100
var serialMu sync.Mutex

// leaf issues a server certificate valid for the names (DNS names and the loopback address) in the period.
func (p *pki) leaf(t *testing.T, notBefore, notAfter time.Time, names ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serialMu.Lock()
	serial++
	n := serial
	serialMu.Unlock()
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(n), Subject: pkix.Name{CommonName: names[0]},
		NotBefore: notBefore, NotAfter: notAfter,
		DNSNames: names, IPAddresses: []net.IP{net.ParseIP(loop)},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, p.ca, &key.PublicKey, p.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (p *pki) good(t *testing.T, names ...string) tls.Certificate {
	return p.leaf(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), names...)
}

// tlsServer serves the certificate on a loopback port, records the SNI of every client hello and the protocol the
// handshake negotiated, and closes each connection after the handshake. It returns the port.
type tlsServer struct {
	Port int
	mu   sync.Mutex
	snis []string
}

func (s *tlsServer) SNIs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.snis...)
}

func serveTLS(t *testing.T, cert tls.Certificate, maxVersion uint16) *tlsServer {
	t.Helper()
	s := &tlsServer{}
	cfg := &tls.Config{
		MaxVersion: maxVersion, NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
			s.mu.Lock()
			s.snis = append(s.snis, h.ServerName)
			s.mu.Unlock()
			return &cert, nil
		},
	}
	l, err := tls.Listen("tcp", loop+":0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	s.Port = l.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				if err := c.(*tls.Conn).Handshake(); err != nil {
					return
				}
				_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
				_, _ = c.Read(make([]byte, 1))
			}()
		}
	}()
	return s
}

// ---- DNS

type dnsServer struct {
	Addr netip.AddrPort
	mu   sync.Mutex
	asks []string
	udp  int
	tcp  int
}

func (d *dnsServer) Asked() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.asks...)
}

// serveDNS answers on UDP and TCP on one loopback port with handler; a nil reply sends nothing.
func serveDNS(t *testing.T, handler func(udp bool, q *dns.Msg) *dns.Msg) *dnsServer {
	t.Helper()
	d := &dnsServer{}
	// The UDP port is picked by the kernel and the TCP port of the same number may be taken: try again.
	var pc net.PacketConn
	var l net.Listener
	var port int
	for try := 0; ; try++ {
		var err error
		if pc, err = net.ListenPacket("udp", loop+":0"); err != nil {
			t.Fatal(err)
		}
		port = pc.LocalAddr().(*net.UDPAddr).Port
		if l, err = net.Listen("tcp", net.JoinHostPort(loop, itoa(port))); err == nil {
			break
		}
		_ = pc.Close()
		if try == 20 {
			t.Fatal(err)
		}
	}
	serve := func(udp bool) dns.HandlerFunc {
		return func(w dns.ResponseWriter, q *dns.Msg) {
			d.mu.Lock()
			d.asks = append(d.asks, q.Question[0].Name)
			if udp {
				d.udp++
			} else {
				d.tcp++
			}
			d.mu.Unlock()
			if resp := handler(udp, q); resp != nil {
				_ = w.WriteMsg(resp)
			}
		}
	}
	us := &dns.Server{PacketConn: pc, Handler: serve(true)}
	ts := &dns.Server{Listener: l, Handler: serve(false)}
	started := make(chan struct{}, 2)
	us.NotifyStartedFunc, ts.NotifyStartedFunc = func() { started <- struct{}{} }, func() { started <- struct{}{} }
	go func() { _ = us.ActivateAndServe() }()
	go func() { _ = ts.ActivateAndServe() }()
	<-started
	<-started
	t.Cleanup(func() { _ = us.Shutdown(); _ = ts.Shutdown() })
	d.Addr = netip.MustParseAddrPort(net.JoinHostPort(loop, itoa(port)))
	return d
}

func itoa(n int) string { return big.NewInt(int64(n)).String() }

// answerA is a handler that answers every question with the addresses.
func answerA(addrs ...string) func(bool, *dns.Msg) *dns.Msg {
	return func(_ bool, q *dns.Msg) *dns.Msg {
		m := new(dns.Msg)
		m.SetReply(q)
		for _, a := range addrs {
			m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(a).To4()})
		}
		return m
	}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
