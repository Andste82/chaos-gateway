//go:build testbed

package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	mrand "math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// M21 on the real kernel (plan §5, M21 "Tests" and the acceptance carried over from Phase 2): the certificate cases of the TLS
// responder seen by correct and by insecure TLS clients of a test network, the redirect of the selected traffic into the
// service namespace (a conntrack mark, the service mark, table 102, the redirect inside the namespace and SO_ORIGINAL_DST),
// the checks, the access rules in front of the redirect (E11), the latency of the device's faults on the connections to the
// responder, and a client of a WireGuard network. The responder runs in the service namespace, as the gateway starts it.

const (
	tlsBroker = "broker.example.com"
	tlsOther  = "other.example.com"
)

// realServerScript is a TLS server with a valid certificate on the ports it is given: it answers a line to every handshake that
// completes and then echoes what it reads, so a connection that is passed through to it can be held open and used.
const realServerScript = `
import socket, ssl, sys, threading
cert, key = sys.argv[1], sys.argv[2]
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(cert, key)
def serve(raw):
    try:
        c = ctx.wrap_socket(raw, server_side=True)
        c.sendall(b"hello from the real server\n")
        while True:
            d = c.recv(100)
            if not d: break
            c.sendall(d)
    except Exception:
        pass
def listen(port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(50)
    while True:
        raw, _ = s.accept()
        threading.Thread(target=serve, args=(raw,), daemon=True).start()
for p in sys.argv[3:]:
    threading.Thread(target=listen, args=(int(p),), daemon=True).start()
threading.Event().wait()
`

// clientScript is a TLS client: it connects, prints one JSON line and exits. strict verifies the certificate against the file
// like a correct device (the name too), insecure takes whatever it is shown; hold keeps a handshake that completed open for that
// many seconds, with a byte every 200 ms, and says how it ended.
const clientScript = `
import json, socket, ssl, sys, time
host, port, name, mode, cafile, version, timeout, hold = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5], sys.argv[6], float(sys.argv[7]), float(sys.argv[8])
if mode == "strict":
    ctx = ssl.create_default_context(cafile=cafile)
else:
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
if version == "1.2":
    ctx.maximum_version = ssl.TLSVersion.TLSv1_2
elif version == "1.3":
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
out = {}
start = time.time()
try:
    raw = socket.create_connection((host, port), timeout=timeout)
    raw.settimeout(timeout)
    s = ctx.wrap_socket(raw, server_hostname=name if name else None)
    out = {"ok": True, "version": s.version(), "line": s.recv(100).decode(errors="replace").strip()}
    if hold > 0:
        end = time.time() + hold
        try:
            while time.time() < end:
                s.sendall(b"x"); s.recv(10); time.sleep(0.2)
            out["held"] = True
        except Exception as e:
            out["held"] = False; out["hold_error"] = type(e).__name__
except ssl.SSLCertVerificationError as e:
    out = {"ok": False, "kind": "verify", "error": e.verify_message}
except ssl.SSLError as e:
    out = {"ok": False, "kind": "ssl", "error": str(e)}
except socket.timeout:
    out = {"ok": False, "kind": "timeout", "error": "timed out"}
except ConnectionResetError as e:
    out = {"ok": False, "kind": "reset", "error": "reset"}
except Exception as e:
    out = {"ok": False, "kind": type(e).__name__, "error": str(e)}
out["ms"] = int((time.time() - start) * 1000)
print(json.dumps(out), flush=True)
`

// tlsBed is a gateway with the TLS responder in its service namespace, a real TLS server on the server of the testbed (ports 8883
// and 443, the certificate of a CA the clients trust), and the files the clients need.
type tlsBed struct {
	g     *gw
	top   *testbed.Topology
	admin string
	ns    string
	srv   *tlsresponder.Server
	// trust is the file of the CAs a correct device trusts: the test CA (it is a development firmware) and the CA of the real server
	trust string
	// publicOnly is the file of the CA of the real server alone: a production firmware that trusts no test CA
	publicOnly string
	// publicCert and publicKey are the certificate of the real server and its key
	publicCert, publicKey string
	mu                    sync.Mutex
	seen                  []map[string]any
	// base is the API as the service namespace reaches it, tokenFile the service token, ctx lives as long as the responder
	base, tokenFile string
	ctx             context.Context
}

// newTLSBed makes the gateway of the bed; mods change the options of the responder (M22 gives it the address of the proxy).
func newTLSBed(t *testing.T, mods ...func(o *tlsresponder.Options, serviceNS string)) *tlsBed {
	t.Helper()
	ns := fmt.Sprintf("svc%06x", mrand.Intn(1<<24))
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "delete", ns).Run() })
	g, top := newBedGW(t, func(o *options) { o.serviceNS = ns })
	waitFor(t, 30*time.Second, "svc0 and the namespace", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "-br", "addr", "show", "dev", "svc0")
		return err == nil
	})
	port := 443
	if cfg := g.e.Snapshot().Config; cfg != nil && cfg.Management.UiPort != nil && *cfg.Management.UiPort > 0 {
		port = *cfg.Management.UiPort
	}
	base := fmt.Sprintf("http://169.254.100.1:%d", port)
	l, err := testbed.ListenIn(top.GW.Name, "tcp", fmt.Sprintf("169.254.100.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	web := &http.Server{Handler: g.srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = web.Serve(l) }()
	t.Cleanup(func() { _ = web.Close() })
	token := filepath.Join(t.TempDir(), "service-token")
	if err := g.au.EnsureServiceToken(token); err != nil {
		t.Fatal(err)
	}

	b := &tlsBed{g: g, top: top, admin: g.token, ns: ns, base: base, tokenFile: token}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// the real server's CA and certificate
	pubCA, srvCert, srvKey := writePublicServer(t, dir, tlsBroker)
	// the test CA, downloaded the way a development firmware gets it
	g.token = b.admin
	r := g.do("GET", "/tls/ca", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("GET /tls/ca: %d %s", r.Status, r.Body)
	}
	pub, _ := os.ReadFile(pubCA)
	b.trust = filepath.Join(dir, "trust.pem")
	b.publicOnly, b.publicCert, b.publicKey = pubCA, srvCert, srvKey
	if err := os.WriteFile(b.trust, append(append([]byte(nil), pub...), r.Body...), 0o644); err != nil {
		t.Fatal(err)
	}
	top.Server.Start("python3", "-c", realServerScript, srvCert, srvKey, "8883", "443", "9000")

	// the responder, in the service namespace
	cas, err := tlsresponder.LoadCAs(filepath.Join(g.root, "tlsca"))
	if err != nil {
		t.Fatal(err)
	}
	iss, err := tlsresponder.NewIssuer(cas, &clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := testbed.ListenIn(ns, "tcp", fmt.Sprintf(":%d", 9443))
	if err != nil {
		t.Fatal(err)
	}
	c := &tlsresponder.APIClient{Base: base, TokenFile: token, Dial: testbed.DialerIn(ns), Wait: 3 * time.Second}
	opts := tlsresponder.Options{Log: testLog(t), Issuer: iss, Resolver: c, Sink: c, Dial: testbed.DialerIn(ns), StallFor: 20 * time.Second}
	for _, mod := range mods {
		mod(&opts, ns)
	}
	srv := tlsresponder.New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	b.ctx = ctx
	done := make(chan struct{}, 3)
	go func() { srv.Follow(ctx, c, testLog(t)); done <- struct{}{} }()
	go func() { srv.RunReports(ctx); done <- struct{}{} }()
	go func() { _ = srv.ServeOn(ctx, ln); done <- struct{}{} }()
	t.Cleanup(func() {
		cancel()
		for i := 0; i < 3; i++ {
			<-done
		}
		c.CloseIdleConnections()
	})
	b.srv = srv

	// the handshakes the gateway hears of
	ch, unsub := g.e.Subscribe()
	t.Cleanup(unsub)
	go func() {
		for ev := range ch {
			if ev.Type == engine.EventTLSHandshake {
				b.mu.Lock()
				b.seen = append(b.seen, ev.Data)
				b.mu.Unlock()
			}
		}
	}()
	// the devices are known once they have been seen: A and B are talking to the gateway
	for _, ns := range []*testbed.Namespace{top.A, top.B} {
		_, _ = ns.Run(context.Background(), "ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	}
	g.observe()
	// the real server answers a correct client through the gateway
	waitFor(t, 60*time.Second, "the real server answers a TLS client of A", func() bool {
		return b.client(top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)["ok"] == true
	})
	return b
}

// writePublicServer makes a CA that stands for a public one and a certificate of it for the name, and returns the files.
func writePublicServer(t *testing.T, dir, name string) (caFile, certFile, keyFile string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Public CA of the testbed"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name, tlsOther}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	write := func(name, typ string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	return write("public-ca.pem", "CERTIFICATE", caDER), write("server.pem", "CERTIFICATE", der), write("server.key", "EC PRIVATE KEY", keyDER)
}

// client runs the TLS client script in a namespace and returns its line; a client that does not print one returns the error.
func (b *tlsBed) client(from *testbed.Namespace, host string, port int, name, mode, cafile, version string, timeout float64, hold float64) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration((timeout+hold+20)*float64(time.Second)))
	defer cancel()
	out, err := from.Run(ctx, "python3", "-c", clientScript, host, fmt.Sprint(port), name, mode, cafile, version, fmt.Sprint(timeout), fmt.Sprint(hold))
	var m map[string]any
	if jerr := json.Unmarshal([]byte(lastLine(out)), &m); jerr != nil {
		return map[string]any{"ok": false, "kind": "no output", "error": fmt.Sprintf("%v: %s", err, out)}
	}
	return m
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// overlay creates an overlay of the admin and returns its id.
func (b *tlsBed) overlay(t *testing.T, body string) string {
	t.Helper()
	b.g.token = b.admin
	r := b.g.createOverlay(body)
	if r.Status != 201 && r.Status != 200 {
		t.Fatalf("%s: %d %s", body, r.Status, r.Body)
	}
	return r.json(t)["id"].(string)
}

func (b *tlsBed) remove(t *testing.T, id string) {
	t.Helper()
	b.g.token = b.admin
	if r := b.g.do("DELETE", "/overlays/"+id, nil, nil, nil); r.Status != 204 && r.Status != 200 {
		t.Fatalf("delete %s: %d %s", id, r.Status, r.Body)
	}
}

// handshakes returns the handshake events heard so far.
func (b *tlsBed) handshakes() []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]map[string]any(nil), b.seen...)
}

// waitHandshake waits for the n-th handshake event after the first `after` ones and returns it.
func (b *tlsBed) waitHandshake(t *testing.T, after int) map[string]any {
	t.Helper()
	var ev map[string]any
	waitFor(t, 30*time.Second, "a handshake event", func() bool {
		hs := b.handshakes()
		if len(hs) > after {
			ev = hs[after]
			return true
		}
		return false
	})
	return ev
}

// untilCase waits until a client of A is shown the responder: its first connection to the broker's port is not answered by the real
// server any more (the redirect and the overlay are in the kernel and in the API's snapshot).
func (b *tlsBed) untilServed(t *testing.T, port int, name string) {
	t.Helper()
	waitFor(t, 40*time.Second, "the responder answers A", func() bool {
		r := b.client(b.top.A, testbed.ServerAddr, port, name, "strict", b.publicOnly, "any", 3, 0)
		return r["ok"] != true
	})
	// the reports of the probes that were answered by the responder are in before a test counts the events
	time.Sleep(1500 * time.Millisecond)
}

// Plan §2.8, S4 and the tests of M21: a correct client rejects the certificate of every case, with TLS 1.2 and with TLS 1.3; the
// gateway hears of each handshake, knows where the device was going (SO_ORIGINAL_DST in the service namespace) and the name it
// asked for; a client that trusts the test CA still rejects the case of the unknown CA.
func TestACorrectClientRejectsEveryCertificateCaseWithTLS12And13OnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	for _, c := range []struct {
		kase     string
		verify   string // what the client says is wrong
		trusting string
	}{
		{"untrusted_ca", "unable to get local issuer certificate", "test CA and the real CA"},
		{"self_signed", "self-signed certificate", "test CA and the real CA"},
		{"expired", "certificate has expired", "test CA and the real CA"},
		{"not_yet_valid", "certificate is not yet valid", "test CA and the real CA"},
		{"hostname_mismatch", "Hostname mismatch", "test CA and the real CA"},
	} {
		for _, version := range []string{"1.2", "1.3"} {
			id := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"`+c.kase+`","ports":[8883]}}`)
			b.untilServed(t, 8883, tlsBroker)
			before := len(b.handshakes())
			r := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.trust, version, 5, 0)
			if r["ok"] == true || r["kind"] != "verify" || !strings.Contains(fmt.Sprint(r["error"]), c.verify) {
				t.Errorf("%s with TLS %s: a correct client trusting the %s: %v", c.kase, version, c.trusting, r)
			}
			ev := b.waitHandshake(t, before)
			if ev["outcome"] != "rejected" || ev["case"] != c.kase || ev["original_dst"] != testbed.ServerAddr+":8883" || ev["sni"] != tlsBroker || ev["client"] != testbed.ClientAAddr ||
				ev["tls_version"] != "TLS "+version || ev["violation"] != false || ev["alert"] == nil {
				t.Errorf("%s with TLS %s: the event %v", c.kase, version, ev)
			}
			b.remove(t, id)
			waitFor(t, 40*time.Second, "the real server answers A again after "+c.kase, func() bool {
				return b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)["ok"] == true
			})
		}
	}
}

// `openssl s_client` and `curl`, as the plan names them: the verification errors of the two tools for each certificate case.
func TestOpenSSLAndCurlRejectTheCertificatesOfTheCasesOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	for _, c := range []struct{ kase, openssl string }{
		{"untrusted_ca", "unable to get local issuer certificate"},
		{"self_signed", "self-signed certificate"},
		{"expired", "certificate has expired"},
		{"not_yet_valid", "certificate is not yet valid"},
		{"hostname_mismatch", "Hostname mismatch"},
	} {
		id := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"`+c.kase+`","ports":[8883]}}`)
		b.untilServed(t, 8883, tlsBroker)
		for _, version := range []string{"-tls1_2", "-tls1_3"} {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			cmd := b.top.A.Command(ctx, "openssl", "s_client", "-connect", testbed.ServerAddr+":8883", "-servername", tlsBroker, "-CAfile", b.trust,
				"-verify_return_error", "-verify_hostname", tlsBroker, version, "-brief")
			cmd.Stdin = strings.NewReader("")
			out, err := cmd.CombinedOutput()
			cancel()
			if err == nil || !strings.Contains(strings.ToLower(string(out)), strings.ToLower(c.openssl)) { // OpenSSL 3 writes "hostname mismatch"
				t.Errorf("%s with %s: openssl %v: %s", c.kase, version, err, out)
			}
		}
		// curl exits with 60 for a certificate it does not trust (and 51 for a name that does not match, in older versions)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, err := b.top.A.Command(ctx, "curl", "-sS", "-m", "10", "--cacert", b.trust, "--resolve", fmt.Sprintf("%s:8883:%s", tlsBroker, testbed.ServerAddr),
			"https://"+tlsBroker+":8883/").CombinedOutput()
		cancel()
		var ee *exec.ExitError
		if err == nil || !asExit(err, &ee) || (ee.ExitCode() != 60 && ee.ExitCode() != 51) {
			t.Errorf("%s: curl %v: %s", c.kase, err, out)
		}
		b.remove(t, id)
		waitFor(t, 40*time.Second, "the real server answers again", func() bool {
			return b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)["ok"] == true
		})
	}
}

func asExit(err error, target **exec.ExitError) bool { return errors.As(err, target) }

// A deliberately insecure device completes the handshake: the gateway flags it as a violation, the check `tls_rejected` of a run
// fails and says so, and `tls_accepted` passes; a correct device passes `tls_rejected`.
func TestADeviceThatTakesTheBrokenCertificateIsFlaggedByTheCheckOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	b.g.mintToken("overlays")
	run := func(name, check, mode, cafile string) map[string]any {
		body := inlineRun(name, iotTarget, step("case", "0s", "tls", map[string]any{"case": "expired", "ports": []int{8883}}), step("end", "12s", "restore", true))
		body["inline"].(map[string]any)["checks"] = []any{map[string]any{check: map[string]any{"protocol": "tcp", "ports": []int{8883}}, "window": map[string]any{"from": "case", "until": "end"}}}
		r := b.g.postRun(body, "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id := r.json(t)["id"].(string)
		// the device connects while the window is open, once the case is in the kernel
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(9 * time.Second)
			for time.Now().Before(deadline) {
				b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, mode, cafile, "any", 3, 0)
				time.Sleep(500 * time.Millisecond)
			}
		}()
		done := b.g.waitRun(id, "passed", "failed", "error")
		wg.Wait()
		return done
	}
	check := func(done map[string]any) map[string]any { return done["checks"].([]any)[0].(map[string]any) }

	insecure := run("insecure-rejected", "tls_rejected", "insecure", "")
	if c := check(insecure); insecure["status"] != "failed" || c["status"] != "failed" || !strings.Contains(fmt.Sprint(c["message"]), "took a certificate it must reject") {
		t.Errorf("an insecure device passed tls_rejected: %v %v", insecure["status"], c)
	}
	accepted := run("insecure-accepted", "tls_accepted", "insecure", "")
	if c := check(accepted); accepted["status"] != "passed" || c["status"] != "passed" || !strings.Contains(fmt.Sprint(c["message"]), "took the certificate") {
		t.Errorf("an insecure device failed tls_accepted: %v %v", accepted["status"], c)
	}
	correct := run("correct-rejected", "tls_rejected", "strict", b.trust)
	if c := check(correct); correct["status"] != "passed" || c["status"] != "passed" || !strings.Contains(fmt.Sprint(c["message"]), "aborted the handshake") {
		t.Errorf("a correct device failed tls_rejected: %v %v", correct["status"], c)
	}
	// the overlays of the runs are gone with them
	if n := len(b.g.overlaysOf(insecure["id"].(string))); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
	// and the violation was an event, with the device as its subject
	var violations int
	for _, ev := range b.handshakes() {
		if ev["violation"] == true {
			violations++
		}
	}
	if violations == 0 {
		t.Errorf("no handshake event reported the violation: %v", b.handshakes())
	}
}

// What no case selects reaches the real server, unchanged: another destination, another port, and a server name that the case
// does not name (the responder reads it from the ClientHello and passes the connection on).
func TestAConnectionNoCaseDecidesReachesTheRealServerOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	id := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"self_signed","ports":[8883,443],"sni":["`+tlsOther+`"],"destination":{"cidr":"`+testbed.ServerAddr+`/32"}}}`)
	// the name the case names is shown the responder's certificate
	waitFor(t, 40*time.Second, "the name of the case is answered by the responder", func() bool {
		return b.client(b.top.A, testbed.ServerAddr, 8883, tlsOther, "strict", b.publicOnly, "any", 3, 0)["kind"] == "verify"
	})
	// the other name of the same server is selected by the kernel (same address and port), decided by the API: no case, the responder
	// passes the connection through to the real server, which answers it with its own certificate
	before := b.srv.PassedThrough()
	r := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 5, 0)
	if r["ok"] != true || r["line"] != "hello from the real server" {
		t.Errorf("the name the case does not name: %v", r)
	}
	if b.srv.PassedThrough() <= before {
		t.Errorf("the connection did not go through the responder: %d passed through", b.srv.PassedThrough())
	}
	// another address of the server is not selected by the kernel at all
	if r := b.client(b.top.A, testbed.ServerAddr2, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0); r["kind"] == "verify" {
		t.Errorf("another address was answered by the responder: %v", r)
	}
	// and no other device is affected by a case on the network IoT's connections that it does not make: B is of the network, so it is
	if r := b.client(b.top.B, testbed.ServerAddr, 8883, tlsOther, "strict", b.publicOnly, "any", 3, 0); r["kind"] != "verify" {
		t.Errorf("B: %v", r)
	}
	// the redirect is no way round the access matrix: once the matrix keeps the network from the uplink, the connection that no case
	// decides is reset by the responder instead of passed on, and the traffic no case selects is dropped by the matrix as before
	b.g.token = b.admin
	mid := b.g.mustPatch(map[string]any{"access_matrix": map[string]any{"entries": []any{
		map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "lab-hub"}, "policy": "allow"},
		map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "site-b"}, "policy": "allow"},
		map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"uplink": true}, "policy": "deny"}}}})
	if r := b.g.apply(mid); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	denied := b.srv.Denied()
	waitFor(t, 40*time.Second, "the matrix keeps the device from the real server, the redirect included", func() bool {
		return b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)["ok"] != true && b.srv.Denied() > denied
	})
	if r := b.client(b.top.A, testbed.ServerAddr2, 8883, tlsBroker, "strict", b.publicOnly, "any", 2, 0); r["ok"] == true {
		t.Errorf("the matrix does not drop what no case selects: %v", r)
	}
	b.remove(t, id)
}

// peerServerScript is the real server of the bed, but it says whom it sees: the first line is "hello from the real server" and
// the address its peer has.
var peerServerScript = strings.Replace(strings.Replace(realServerScript,
	"        c = ctx.wrap_socket(raw, server_side=True)\n", "        peer = raw.getpeername()[0]\n        c = ctx.wrap_socket(raw, server_side=True)\n", 1),
	`c.sendall(b"hello from the real server\n")`, `c.sendall(("hello from the real server " + peer + "\n").encode())`, 1)

// A connection that no case decides is passed on to servers in other test networks and behind WireGuard clients too, not only behind
// the uplink (plan M21, P5-M21-02): the case selects everything of the network IoT to port 8883 (no destination), the name the
// device asks for is named by no case, so the responder passes the connection on through the service namespace, and the device gets
// the answer of a server in the network Lab and of one in the network behind the WireGuard client rA. The server sees the gateway's
// address in the network it is in (the namespace's address, masqueraded on the way out), not the device's. Where the access matrix
// refuses the connection, the responder resets it instead of passing it on.
func TestAConnectionNoCaseDecidesReachesAServerInAnotherTestNetworkAndBehindAWireGuardClientOnTheRealKernel(t *testing.T) {
	if !strings.Contains(peerServerScript, "peer = raw.getpeername()") || !strings.Contains(peerServerScript, `+ peer +`) {
		t.Fatal("the script of the server that names its peer does not apply to the real server's script")
	}
	b := newTLSBed(t)
	const labID = "5d3a8e6f-2b1c-4d7e-9f08-a1b2c3d4e5f6"
	iotToLab := map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "Lab"}, "policy": "allow"}
	iotToHub := map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "lab-hub"}, "policy": "allow"}
	iotToSite := map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "site-b"}, "policy": "allow"}
	// a second test network with a server of its own, and the WireGuard client rA with a server in the network behind it
	id := b.g.mustPatch(map[string]any{
		"networks": map[string]any{
			labID: map[string]any{"type": "lan", "name": "Lab", "address": testbed.LAN1Gateway + "/24",
				"interfaces": []any{map[string]any{"name": "lan1", "mac": testbed.GWLan1MAC}}},
			// the tunnel carries only what the client may reach: the test network, where the device is that opens the connections
			hubID: map[string]any{"clients": map[string]any{rAID: map[string]any{"reachable": []any{map[string]any{"network": iotID}}}}}},
		"access_matrix": map[string]any{"entries": []any{iotToHub, iotToSite, iotToLab}}})
	if r := b.g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := b.g.do("GET", "/networks/lab-hub/clients/rA/export", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	conf := filepath.Join(t.TempDir(), "wgra.conf")
	if err := os.WriteFile(conf, r.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	b.top.RC.Must("wg-quick", "up", conf)
	waitFor(t, 30*time.Second, "the tunnel carries traffic", func() bool { return pingOK(b.top.RC, "", "10.99.0.1") })
	b.top.C.Start("python3", "-c", peerServerScript, b.publicCert, b.publicKey, "8883")
	b.top.RC.Start("python3", "-c", peerServerScript, b.publicCert, b.publicKey, "8883")
	servers := []struct {
		name, addr, sees string
	}{
		{"the server in the network Lab", testbed.ClientCAddr, testbed.LAN1Gateway},
		{"the server behind the WireGuard client", testbed.ClientNetHost, "10.99.0.1"},
	}
	// without a case the device reaches both itself: the gateway routes the connection, the server sees the device
	for _, srv := range servers {
		waitFor(t, 60*time.Second, srv.name+" answers the device", func() bool {
			r := b.client(b.top.A, srv.addr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)
			return r["ok"] == true && r["line"] == "hello from the real server "+testbed.ClientAAddr
		})
	}

	oid := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"self_signed","ports":[8883],"sni":["`+tlsOther+`"]}}`)
	b.untilServed(t, 8883, tlsOther)
	for _, srv := range servers {
		before := b.srv.PassedThrough()
		var res map[string]any
		waitFor(t, 60*time.Second, srv.name+" answers through the responder", func() bool {
			res = b.client(b.top.A, srv.addr, 8883, tlsBroker, "strict", b.publicOnly, "any", 5, 0)
			t.Logf("%s: %v", srv.name, res)
			return res["ok"] == true && b.srv.PassedThrough() > before
		})
		if want := "hello from the real server " + srv.sees; res["line"] != want {
			t.Errorf("%s: the device got %v, want the line %q (the server sees the gateway's address, not the device's)", srv.name, res["line"], want)
		}
		// the name the case names is shown the responder's certificate, at the same address
		if r := b.client(b.top.A, srv.addr, 8883, tlsOther, "strict", b.publicOnly, "any", 3, 0); r["kind"] != "verify" {
			t.Errorf("%s, the name of the case: %v", srv.name, r)
		}
	}

	// the matrix keeps the device from both networks: nothing is passed on any more, the responder resets the connections that no
	// case decides, and the device does not reach the servers
	b.g.token = b.admin
	if r := b.g.apply(b.g.mustPatch(map[string]any{"access_matrix": map[string]any{"entries": []any{iotToSite}}})); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	for _, srv := range servers {
		denied := b.srv.Denied()
		waitFor(t, 40*time.Second, "the matrix keeps the device from "+srv.name+", the responder included", func() bool {
			return b.client(b.top.A, srv.addr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)["ok"] != true && b.srv.Denied() > denied
		})
	}
	b.remove(t, oid)
}

// The handshake cases act on the ClientHello and show no certificate: a reset, a close (a FIN) and silence.
func TestTheHandshakeCasesBreakTheHandshakeOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	for _, c := range []struct {
		kase, outcome string
		kinds         []string // what the client may see: a reset, or the end of the stream, or nothing at all
	}{
		{"handshake_reset", "reset_sent", []string{"reset", "ssl"}},
		{"handshake_close", "close_sent", []string{"ssl", "reset"}},
		{"handshake_stall", "stalled", []string{"timeout"}},
	} {
		id := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"`+c.kase+`","ports":[8883]}}`)
		var r map[string]any
		waitFor(t, 40*time.Second, "the case "+c.kase+" breaks the handshake", func() bool {
			before := len(b.handshakes())
			r = b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)
			if r["ok"] == true {
				return false
			}
			ev := b.waitHandshake(t, before)
			return ev["outcome"] == c.outcome
		})
		if k := fmt.Sprint(r["kind"]); !contains(c.kinds, k) {
			t.Errorf("%s: the client saw %v, want one of %v", c.kase, r, c.kinds)
		}
		b.remove(t, id)
		waitFor(t, 40*time.Second, "the real server answers again", func() bool {
			return b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 3, 0)["ok"] == true
		})
	}
}

// The built-in "TLS broken" profile (P2-M11-01): the tls part activates and resets the handshake on both default ports.
func TestTheTLSBrokenProfileResetsTheHandshakeOnTheDefaultPortsOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	id := b.overlay(t, `{"target":{"network":"IoT"},"profile":"tls-broken"}`)
	for _, port := range []int{8883, 443} {
		waitFor(t, 40*time.Second, fmt.Sprintf("the handshake on port %d is reset", port), func() bool {
			r := b.client(b.top.A, testbed.ServerAddr, port, tlsBroker, "strict", b.publicOnly, "any", 3, 0)
			return r["ok"] != true
		})
	}
	// another port is not touched
	if r := b.client(b.top.A, testbed.ServerAddr, 9000, tlsBroker, "strict", b.publicOnly, "any", 5, 0); r["ok"] != true {
		t.Errorf("port 9000: %v", r)
	}
	b.g.token = b.admin
	if ov := b.g.do("GET", "/overlays/"+id, nil, nil, nil).json(t); ov["state"] != "effective" {
		t.Errorf("%v", ov)
	}
	b.remove(t, id)
}

// E11 (plan §2.4) on the real kernel: an access rule that drops tcp/8883 for the device decides before the redirect, so the
// connection never reaches the responder; without the rule the same case is answered.
func TestE11AnAccessRuleDecidesBeforeTheRedirectOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"untrusted_ca","ports":[8883]}}`)
	b.untilServed(t, 8883, tlsBroker)
	before := len(b.handshakes())
	rule := b.overlay(t, `{"target":{"network":"IoT"},"rule":{"action":"drop","protocol":"tcp","ports":[8883]}}`)
	waitFor(t, 40*time.Second, "the drop rule decides first", func() bool {
		r := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.trust, "any", 2, 0)
		return r["kind"] == "timeout"
	})
	time.Sleep(time.Second)
	// a handshake that was under way when the rule came is allowed; none after it
	t.Logf("%d handshakes before the rule, %d now", before, len(b.handshakes()))
	mid := len(b.handshakes())
	if r := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.trust, "any", 2, 0); r["kind"] != "timeout" {
		t.Errorf("with the rule: %v", r)
	}
	time.Sleep(time.Second)
	if n := len(b.handshakes()); n != mid {
		t.Errorf("the responder saw a connection that the access rule drops: %v", b.handshakes()[mid:])
	}
	b.remove(t, rule)
	waitFor(t, 40*time.Second, "the case is answered again once the rule is gone", func() bool {
		r := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.trust, "any", 3, 0)
		return r["kind"] == "verify"
	})
}

// Plan §2.8: only new connections are redirected. A connection that is established when the case comes stays with the real
// server and keeps working; the next connection gets the case; with cut_existing the established connection is reset at its
// next packet; and a connection the responder holds is reset when its case is removed ("reset on removal").
func TestOnlyNewConnectionsAreRedirectedAndCutExistingAndRemovalResetOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	// a long-lived connection to the real server, held for 40 s by a device
	held := make(chan map[string]any, 1)
	go func() {
		held <- b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 5, 40)
	}()
	time.Sleep(2 * time.Second)

	id := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"untrusted_ca","ports":[8883]}}`)
	b.untilServed(t, 8883, tlsBroker) // a new connection gets the case
	select {
	case r := <-held:
		t.Fatalf("the established connection ended when the case came: %v", r)
	case <-time.After(3 * time.Second):
	}
	// cut_existing: the established connection is reset at its next packet (the case replaces itself, with the same id)
	b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"untrusted_ca","ports":[8883],"cut_existing":true}}`)
	select {
	case r := <-held:
		if r["held"] != false {
			t.Errorf("the connection was not cut: %v", r)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("cut_existing did not reset the established connection")
	}
	b.remove(t, id)

	// reset on removal: a handshake that the responder holds (a stall) ends with a reset when the case goes, not after its 20 s
	stall := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"handshake_stall","ports":[8883]}}`)
	waitFor(t, 40*time.Second, "the stall is in the kernel", func() bool {
		return b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 1.5, 0)["kind"] == "timeout"
	})
	time.Sleep(1500 * time.Millisecond)
	resets := b.srv.Resets()
	res := make(chan map[string]any, 1)
	start := time.Now()
	go func() {
		res <- b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.publicOnly, "any", 15, 0)
	}()
	time.Sleep(2 * time.Second)
	if hs := b.handshakes(); len(hs) == 0 || hs[len(hs)-1]["outcome"] != "stalled" {
		t.Fatalf("the responder does not hold the connection: %v", hs)
	}
	b.remove(t, stall)
	select {
	case r := <-res:
		if time.Since(start) > 12*time.Second || r["kind"] == "timeout" {
			t.Errorf("the client was left waiting after the case was removed: %v after %s", r, time.Since(start))
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the stalled client is still waiting")
	}
	if b.srv.Resets() <= resets {
		t.Error("the responder reset no connection when the case went")
	}
}

// Plan §2.8, S16: the device's latency faults apply to its connections to the responder, as to any other path (the upload leaves
// through svc0 and the download through the device's network, both with the device's fault marks): a delay of 100 ms in each
// direction makes the handshake with the responder take two round trips of 200 ms more.
func TestUploadAndDownloadLatencyApplyToConnectionsToTheResponderOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"untrusted_ca","ports":[8883]}}`)
	b.untilServed(t, 8883, tlsBroker)
	base := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.trust, "1.3", 5, 0)
	if base["kind"] != "verify" {
		t.Fatalf("%v", base)
	}
	baseMs := base["ms"].(float64)
	fault := b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"100ms"}}`)
	waitFor(t, 40*time.Second, "the fault delays the handshake with the responder", func() bool {
		r := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.trust, "1.3", 8, 0)
		return r["kind"] == "verify" && r["ms"].(float64) >= baseMs+350
	})
	// a handshake takes two round trips (TCP, TLS 1.3), each 200 ms longer: measured at the client, within the noise of a loaded machine
	r := b.client(b.top.A, testbed.ServerAddr, 8883, tlsBroker, "strict", b.trust, "1.3", 8, 0)
	if r["kind"] != "verify" || r["ms"].(float64) < baseMs+350 || r["ms"].(float64) > baseMs+1500 {
		t.Errorf("a handshake took %v ms with a 100 ms fault (%v ms without): %v", r["ms"], baseMs, r)
	}
	b.remove(t, fault)
}

// The same cases work for a client of a WireGuard network, from the tunnel: its connection (to an address of the test network, since the tunnel
// carries nothing else) is redirected into the service namespace like one of a local network, and the answer goes back through the tunnel.
func TestTheSameCasesWorkForAWireGuardClientOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	id := b.g.mustPatch(map[string]any{"networks": map[string]any{hubID: map[string]any{"clients": map[string]any{
		"6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f": map[string]any{"name": "rC", "address": "10.99.0.4", "keepalive": "1s", "key": map[string]any{"mode": "generated"},
			// the tunnel carries only what the client may reach: the test network, where a connection to A's address and the port of the case is redirected
			"reachable": []any{map[string]any{"network": iotID}}},
	}}}})
	if r := b.g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := b.g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	conf := filepath.Join(t.TempDir(), "wgrc.conf")
	if err := os.WriteFile(conf, r.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	b.top.RC.Must("wg-quick", "up", conf)
	waitFor(t, 30*time.Second, "the tunnel carries traffic", func() bool { return pingOK(b.top.RC, "", "10.99.0.1") })

	b.overlay(t, `{"target":{"network":"lab-hub"},"tls":{"case":"expired","ports":[8883]}}`)
	var res map[string]any
	waitFor(t, 60*time.Second, "the client of the tunnel is shown the responder's certificate", func() bool {
		res = b.client(b.top.RC, testbed.ClientAAddr, 8883, tlsBroker, "strict", b.trust, "any", 4, 0)
		t.Logf("the client of the tunnel: %v", res)
		return res["kind"] == "verify"
	})
	if !strings.Contains(fmt.Sprint(res["error"]), "certificate has expired") {
		t.Errorf("%v", res)
	}
	ev := b.waitHandshake(t, 0)
	if ev["client"] != "10.99.0.4" || ev["original_dst"] != testbed.ClientAAddr+":8883" || ev["case"] != "expired" || ev["outcome"] != "rejected" {
		t.Errorf("%v", ev)
	}
	// an insecure client of the tunnel completes the handshake
	if r := b.client(b.top.RC, testbed.ClientAAddr, 8883, tlsBroker, "insecure", "", "any", 4, 0); r["ok"] != true {
		t.Errorf("an insecure client of the tunnel: %v", r)
	}
}

// The service namespace holds the chain that redirects what arrives for another address to the responder's port, written by the
// executor when it made the namespace (plan §3.3 "Redirect inside"), and a packet for the namespace's own address (the DNS
// proxy's) is left alone.
func TestTheServiceNamespaceHoldsTheRedirectToTheResponderOnTheRealKernel(t *testing.T) {
	b := newTLSBed(t)
	out, err := exec.Command("ip", "netns", "exec", b.ns, "nft", "list", "chain", "inet", "chaosgw", "svc_redirect").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, want := range []string{"type nat hook prerouting", `iifname "svc1"`, "meta l4proto tcp", "ip daddr != 169.254.100.2", "redirect to :9443"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the chain lacks %q:\n%s", want, out)
		}
	}
	// the gateway's side: the policy rule of the service mark and the table that fails closed
	if out := b.top.GW.Must("ip", "rule", "show"); !strings.Contains(out, "fwmark 0x100000/0x100000 lookup 102") {
		t.Errorf("no rule for the service mark:\n%s", out)
	}
	if out := b.top.GW.Must("ip", "route", "show", "table", "102"); !strings.Contains(out, "default via 169.254.100.2 dev svc0") || !strings.Contains(out, "prohibit default") {
		t.Errorf("table 102:\n%s", out)
	}
}
