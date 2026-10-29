package api_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/api"
)

// certSpec says what a certificate made by newCert looks like; the zero value is a valid leaf for gateway.example.
type certSpec struct {
	dns          []string
	ips          []net.IP
	notBefore    time.Time
	notAfter     time.Time
	eku          []x509.ExtKeyUsage
	rsaBits      int
	noSAN        bool
	encryptedKey bool
	p224         bool      // an ECDSA key on a curve that no TLS handshake uses
	caNotBefore  time.Time // the validity of the issuer (default: from an hour ago for five years)
	caNotAfter   time.Time
}

type issued struct {
	certPEM, keyPEM string
	chainPEM        string // the leaf and the CA
	der             []byte
	ca              *x509.Certificate
	caPEM           string
}

// newCert makes a CA and a leaf signed by it.
func newCert(t *testing.T, s certSpec) issued {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if s.caNotBefore.IsZero() {
		s.caNotBefore = now.Add(-time.Hour)
	}
	if s.caNotAfter.IsZero() {
		s.caNotAfter = now.AddDate(5, 0, 0)
	}
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Lab CA"}, NotBefore: s.caNotBefore, NotAfter: s.caNotAfter,
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)

	if s.notBefore.IsZero() {
		s.notBefore = now.Add(-time.Hour)
	}
	if s.notAfter.IsZero() {
		s.notAfter = now.AddDate(1, 0, 0)
	}
	if s.dns == nil && s.ips == nil && !s.noSAN {
		s.dns = []string{"gateway.example"}
	}
	var pub, priv any
	var keyDER []byte
	if s.rsaBits > 0 {
		k, err := rsa.GenerateKey(rand.Reader, s.rsaBits)
		if err != nil {
			t.Fatal(err)
		}
		pub, priv = &k.PublicKey, k
		keyDER, _ = x509.MarshalPKCS8PrivateKey(k)
	} else {
		curve := elliptic.P256()
		if s.p224 {
			curve = elliptic.P224()
		}
		k, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		pub, priv = &k.PublicKey, k
		keyDER, _ = x509.MarshalPKCS8PrivateKey(k)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(0x2a), Subject: pkix.Name{CommonName: "gateway.example"}, NotBefore: s.notBefore, NotAfter: s.notAfter,
		DNSNames: s.dns, IPAddresses: s.ips, ExtKeyUsage: s.eku, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	key := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if s.encryptedKey {
		blk, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", keyDER, []byte("secret"), x509.PEMCipherAES128) //nolint:staticcheck // makes a legacy encrypted key to be refused
		if err != nil {
			t.Fatal(err)
		}
		key = string(pem.EncodeToMemory(blk))
	}
	_ = priv
	return issued{certPEM: leafPEM, keyPEM: key, chainPEM: leafPEM + caPEM, der: der, ca: ca, caPEM: caPEM}
}

func fingerprint(der []byte) string { s := sha256.Sum256(der); return hex.EncodeToString(s[:]) }

// presented dials addr and returns the leaf certificate the server sends.
func presented(t *testing.T, addr string) *x509.Certificate {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // only the certificate is looked at
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	return conn.ConnectionState().PeerCertificates[0]
}

func put(g *gw, in issued) resp {
	return g.do("PUT", "/system/certificate", map[string]any{"certificate": in.chainPEM, "private_key": in.keyPEM}, nil, nil)
}

func TestTheCertificateIsReplacedForNewConnectionsAndCanBeReset(t *testing.T) {
	g := ready(t)
	// two listeners of the same server: the loopback address, where the services of the gateway verify its own certificate,
	// and another address, as the management network's
	hs := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }), TLSConfig: g.certs.TLSConfig()}
	t.Cleanup(func() { _ = hs.Close() })
	serve := func(addr string) net.Listener {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Skipf("no listener on %s: %v", addr, err)
		}
		go func() { _ = hs.ServeTLS(l, "", "") }()
		return l
	}
	loop := serve("127.0.0.1:0").Addr().String()
	other := serve("127.0.0.2:0")

	own := g.do("GET", "/system/certificate", nil, nil, nil).json(t)
	if own["source"] != "own" || own["self_signed"] != true || own["fingerprint_sha256"] == "" {
		t.Fatalf("%v", own)
	}
	ownFP := own["fingerprint_sha256"].(string)
	if fingerprint(presented(t, other.Addr().String()).Raw) != ownFP {
		t.Fatal("the first start does not present the gateway's own certificate")
	}

	in := newCert(t, certSpec{dns: []string{"gateway.example", "gw.lab"}, ips: []net.IP{net.ParseIP("192.168.56.1")}})
	r := put(g, in)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	j := r.json(t)
	if j["source"] != "installed" || j["self_signed"] != false || j["fingerprint_sha256"] != fingerprint(in.der) || j["chain_length"] != float64(2) {
		t.Errorf("%v", j)
	}
	if strings.Contains(string(r.Body), "PRIVATE KEY") || strings.Contains(string(g.do("GET", "/system/certificate", nil, nil, nil).Body), "PRIVATE KEY") {
		t.Error("the key is in a response")
	}
	// new connections get it, except on the loopback address of the services
	if fingerprint(presented(t, other.Addr().String()).Raw) != fingerprint(in.der) {
		t.Error("a new connection on the management address does not get the installed certificate")
	}
	if fingerprint(presented(t, loop).Raw) != ownFP {
		t.Error("the loopback address stopped presenting the gateway's own certificate: the services could not verify it")
	}
	// a client that trusts the CA verifies the name
	pool := x509.NewCertPool()
	pool.AddCert(in.ca)
	conn, err := tls.Dial("tcp", other.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "gw.lab"})
	if err != nil {
		t.Errorf("the chain does not verify against the CA: %v", err)
	} else {
		_ = conn.Close()
	}

	// the file is the key's: mode 0600, and the next start finds it
	path := filepath.Join(g.root, "secrets", "tls", "ui.pem")
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", st, err)
	}
	again, err := api.OpenCertificates(filepath.Join(g.root, "secrets", "tls"), nil, nil, time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Info(); *got.FingerprintSha256 != fingerprint(in.der) || *got.Source != "installed" {
		t.Errorf("after a restart: %+v", got)
	}

	// the audit entry names the certificate and holds no key
	e, _, _ := g.log.List(auditFilter(), "", 1)
	if e[0].Action != "certificate.install" || !strings.Contains(e[0].Detail, fingerprint(in.der)) || strings.Contains(e[0].Detail, "KEY") {
		t.Errorf("%+v", e[0])
	}

	r = g.do("DELETE", "/system/certificate", nil, nil, nil)
	if r.Status != 200 || r.json(t)["source"] != "own" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if fingerprint(presented(t, other.Addr().String()).Raw) != ownFP {
		t.Error("after the reset the gateway's own certificate is not presented")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the installed certificate stays on disk: %v", err)
	}
	if r := g.do("DELETE", "/system/certificate", nil, nil, nil); r.Status != 200 {
		t.Errorf("a second reset: %d", r.Status)
	}
	if e, _, _ := g.log.List(auditFilter(), "", 1); e[0].Action != "certificate.reset" {
		t.Errorf("%+v", e[0])
	}
}

func TestACertificateThatCannotServeIsRefusedAndChangesNothing(t *testing.T) {
	g := ready(t)
	ownFP := g.certs.Info().FingerprintSha256
	other := newCert(t, certSpec{})
	good := newCert(t, certSpec{})
	now := time.Now()
	for _, c := range []struct {
		name     string
		in       issued
		field    string
		contains string
	}{
		{"expired", newCert(t, certSpec{notBefore: now.AddDate(-2, 0, 0), notAfter: now.AddDate(-1, 0, 0)}), "certificate", "expired"},
		{"not yet valid", newCert(t, certSpec{notBefore: now.AddDate(0, 1, 0), notAfter: now.AddDate(1, 0, 0)}), "certificate", "not valid before"},
		{"an expired intermediate", newCert(t, certSpec{caNotBefore: now.AddDate(-2, 0, 0), caNotAfter: now.AddDate(-1, 0, 0)}), "certificate", "certificate 2 of the chain expired"},
		{"an intermediate not yet valid", newCert(t, certSpec{caNotBefore: now.AddDate(0, 1, 0), caNotAfter: now.AddDate(5, 0, 0)}), "certificate", "certificate 2 of the chain is not valid before"},
		{"no names", newCert(t, certSpec{noSAN: true}), "certificate", "names no DNS name"},
		{"a client certificate", newCert(t, certSpec{eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}), "certificate", "serverAuth"},
		{"a weak RSA key", newCert(t, certSpec{rsaBits: 1024}), "private_key", "at least 2048"},
		{"an ECDSA key on P-224", newCert(t, certSpec{p224: true}), "private_key", "no TLS handshake can use"},
		{"a key of another certificate", issued{chainPEM: good.chainPEM, keyPEM: other.keyPEM}, "private_key", "private key does not match"},
		{"an encrypted key", newCert(t, certSpec{encryptedKey: true}), "private_key", "password"},
		{"no key", issued{chainPEM: good.chainPEM}, "private_key", "no PEM private key"},
		{"no certificate", issued{keyPEM: good.keyPEM}, "certificate", "no PEM certificate"},
		{"the key in the certificate field", issued{chainPEM: good.keyPEM, keyPEM: good.keyPEM}, "certificate", "PRIVATE KEY"},
		{"a chain out of order", issued{chainPEM: good.caPEM + good.certPEM, keyPEM: good.keyPEM}, "certificate", "not signed by"},
		{"garbage after the chain", issued{chainPEM: good.chainPEM + "junk", keyPEM: good.keyPEM}, "certificate", "after the last PEM block"},
	} {
		r := put(g, c.in)
		if r.Status != 422 || r.code(t) != "validation_failed" {
			t.Errorf("%s: %d %s", c.name, r.Status, r.Body)
			continue
		}
		errs := r.json(t)["errors"].([]any)
		first := errs[0].(map[string]any)
		if first["path"] != "/"+c.field || !strings.Contains(first["message"].(string), c.contains) {
			t.Errorf("%s: %v", c.name, first)
		}
		if strings.Contains(string(r.Body), "BEGIN") {
			t.Errorf("%s: the response repeats PEM text", c.name)
		}
	}
	if got := g.certs.Info().FingerprintSha256; *got != *ownFP {
		t.Error("a refused upload changed the certificate")
	}
	if _, err := os.Stat(filepath.Join(g.root, "secrets", "tls", "ui.pem")); !os.IsNotExist(err) {
		t.Errorf("a refused upload left a file: %v", err)
	}
	// unknown fields and an empty body are the usual 4xx
	g.badRequest = true
	if r := g.do("PUT", "/system/certificate", map[string]any{"certificate": good.chainPEM, "private_key": good.keyPEM, "extra": 1}, nil, nil); r.Status != 422 {
		t.Errorf("an unknown field: %d", r.Status)
	}
	if r := g.do("PUT", "/system/certificate", "", nil, nil); r.Status != 400 {
		t.Errorf("an empty body: %d", r.Status)
	}
	g.badRequest = false
}

func TestACertificateWithAnRSAKeyAndTheSingleCertificateOfASelfSignedPairAreAccepted(t *testing.T) {
	g := ready(t)
	in := newCert(t, certSpec{rsaBits: 2048, ips: []net.IP{net.ParseIP("10.1.2.3")}})
	if r := g.do("PUT", "/system/certificate", map[string]any{"certificate": in.certPEM, "private_key": in.keyPEM}, nil, nil); r.Status != 200 || r.json(t)["chain_length"] != float64(1) {
		t.Errorf("a leaf alone: %d %s", r.Status, r.Body)
	}
	if got := g.certs.Info(); (*got.IpAddresses)[0] != "10.1.2.3" {
		t.Errorf("%+v", got)
	}
}

func TestOnlyTheFullScopeInstallsACertificate(t *testing.T) {
	g := ready(t)
	in := newCert(t, certSpec{})
	for _, scope := range []string{"read", "overlays"} {
		g.token = g.mustTokenOf(scope)
		if r := put(g, in); r.Status != 403 {
			t.Errorf("%s PUT: %d", scope, r.Status)
		}
		if r := g.do("DELETE", "/system/certificate", nil, nil, nil); r.Status != 403 {
			t.Errorf("%s DELETE: %d", scope, r.Status)
		}
	}
	g.token = g.mustTokenOf("read")
	if r := g.do("GET", "/system/certificate", nil, nil, nil); r.Status != 200 {
		t.Errorf("read GET: %d", r.Status)
	}
}

// A damaged ui.pem is no failed start: the gateway presents its own certificate and says why in its log.
func TestADamagedInstalledCertificateFallsBackToTheGatewaysOwn(t *testing.T) {
	dir := t.TempDir()
	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, nil))
	first, err := api.OpenCertificates(dir, nil, nil, time.Now, log)
	if err != nil {
		t.Fatal(err)
	}
	ownFP := *first.Info().FingerprintSha256
	if err := os.WriteFile(filepath.Join(dir, "ui.pem"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := api.OpenCertificates(dir, nil, nil, time.Now, log)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Info(); *got.FingerprintSha256 != ownFP || *got.Source != "own" {
		t.Errorf("%+v", got)
	}
	if !strings.Contains(logged.String(), "cannot be used") {
		t.Errorf("no word in the log: %q", logged.String())
	}
}

// The loopback address and the gateway's end of the link to the service namespace keep the gateway's own certificate
// after an install: the DNS proxy, the TLS responder and Kea's hook verify exactly that one (plan §2.16).
func TestTheOwnCertificateStaysOnEveryAddressOfTheServices(t *testing.T) {
	c, err := api.OpenCertificates(t.TempDir(), nil, nil, time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ownFP := *c.Info().FingerprintSha256
	in := newCert(t, certSpec{ips: []net.IP{net.ParseIP("192.168.56.1")}})
	if _, err := c.Install(in.chainPEM, in.keyPEM); err != nil {
		t.Fatal(err)
	}
	fpOf := func(local net.IP) string {
		t.Helper()
		cert, err := api.ServedFor(c, &net.TCPAddr{IP: local, Port: 443})
		if err != nil {
			t.Fatal(err)
		}
		return fingerprint(cert.Certificate[0])
	}
	builtin := api.BuiltinIPs()
	var haveLink, haveV6Loop bool
	for _, ip := range builtin {
		haveLink = haveLink || ip.Equal(net.ParseIP("169.254.100.1"))
		haveV6Loop = haveV6Loop || ip.Equal(net.IPv6loopback)
	}
	if len(builtin) != 3 || !haveLink || !haveV6Loop {
		t.Fatalf("the addresses of the services are %v: the service link 169.254.100.1, 127.0.0.1 and ::1 must all be there", builtin)
	}
	for _, ip := range builtin {
		if got := fpOf(ip); got != ownFP {
			t.Errorf("%s presents the installed certificate: the services could not verify the gateway", ip)
		}
	}
	for _, ip := range []string{"127.0.0.2", "192.168.56.1", "10.0.0.5", "fe80::1"} {
		if got := fpOf(net.ParseIP(ip)); got != fingerprint(in.der) {
			t.Errorf("%s presents the gateway's own certificate, not the installed one", ip)
		}
	}
	if cert, err := c.TLSConfig().GetCertificate(&tls.ClientHelloInfo{}); err != nil || fingerprint(cert.Certificate[0]) != fingerprint(in.der) {
		t.Errorf("a handshake without a connection: %v %v", cert, err)
	}
}

// Install and Reset are each one step: however they interleave, the certificate that is presented is the one in ui.pem,
// so the next start presents what the API reported.
func TestConcurrentInstallsAndResetsLeaveMemoryAndFileTheSame(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := api.OpenCertificates(dir, nil, nil, time.Now, log)
	if err != nil {
		t.Fatal(err)
	}
	var set []issued
	for i := 0; i < 6; i++ {
		set = append(set, newCert(t, certSpec{}))
	}
	for round := 0; round < 60; round++ {
		var wg sync.WaitGroup
		for i, in := range set {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if round%3 == 2 && i%3 == 0 {
					_, _ = c.Reset()
					return
				}
				if _, err := c.Install(in.chainPEM, in.keyPEM); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		again, err := api.OpenCertificates(dir, nil, nil, time.Now, log)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := c.Info(), again.Info(); *got.FingerprintSha256 != *want.FingerprintSha256 || *got.Source != *want.Source {
			t.Fatalf("round %d: the gateway presents %s (%s) but a restart would present %s (%s)", round, *got.FingerprintSha256, *got.Source, *want.FingerprintSha256, *want.Source)
		}
	}
}

// A certificate that cannot be stored or removed is a 500 that changes nothing and leaks no key.
func TestAFailingWriteOrRemoveChangesNothing(t *testing.T) {
	g := ready(t)
	before := *g.certs.Info().FingerprintSha256
	// a non-empty directory where ui.pem belongs: the rename onto it and the removal of it both fail, even for root
	ui := filepath.Join(g.root, "secrets", "tls", "ui.pem")
	if err := os.MkdirAll(filepath.Join(ui, "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
	in := newCert(t, certSpec{})
	r := put(g, in)
	if r.Status != 500 || strings.Contains(string(r.Body), "PRIVATE KEY") || strings.Contains(string(r.Body), "BEGIN") {
		t.Errorf("install: %d %s", r.Status, r.Body)
	}
	if got := g.certs.Info(); *got.FingerprintSha256 != before || *got.Source != "own" {
		t.Errorf("a failed write changed the certificate: %+v", got)
	}
	if r := g.do("DELETE", "/system/certificate", nil, nil, nil); r.Status != 500 {
		t.Errorf("reset: %d %s", r.Status, r.Body)
	}
	if got := g.certs.Info(); *got.FingerprintSha256 != before {
		t.Errorf("a failed removal changed the certificate: %+v", got)
	}
	if e, _, _ := g.log.List(auditFilter(), "", 1); len(e) > 0 && strings.HasPrefix(e[0].Action, "certificate.") {
		t.Errorf("a failed change was audited as done: %+v", e[0])
	}
}
