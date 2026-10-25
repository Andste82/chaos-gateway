//go:build testbed

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// M22 on the real kernel (plan §2.8, M22 "Tests"): the interception proxy, the real mitmproxy with the addon of sidecars/tls-proxy, in the service
// namespace behind the responder, as the containers run it. A client of a test network that trusts the test CA sees the modified responses and the
// delays, a connection that is reused stays one connection, the UDP 443 of the clients of the case is dropped so that QUIC falls back to TCP, and the key
// log of the proxy decrypts a capture of the device that the real executor made. The kernel side (the redirect into the service namespace and the original
// destination) is that of the TLS responder (M21); what is new is the hand-over from the responder to the proxy inside the namespace, the proxy's own
// connection through the gateway's service path, the rule that drops QUIC and the capture. The tests are written and vetted here; CI runs them
// (M22_LEVEL1 in .github/level1-tests.env).

const (
	httpsPort = 8443
	// proxyListen and controlListen are where the proxy and the responder's control interface listen in the namespace: the defaults of the responder
	proxyListen   = tlsresponder.DefaultProxyAddr
	controlListen = tlsresponder.DefaultControlAddr
)

// httpsServerScript is an HTTPS server with a valid certificate (the CA of the real server): it answers every GET with a line that names the path, and a
// header, after the time a path asks for (/slow/<ms>).
const httpsServerScript = `
import http.server, ssl, sys, time
cert, key, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def do_GET(self):
        body = ("hello from the real server " + self.path + "\n").encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("X-Real", "1")
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
s = http.server.ThreadingHTTPServer(("0.0.0.0", port), H)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(cert, key)
s.socket = ctx.wrap_socket(s.socket, server_side=True)
s.serve_forever()
`

// udpCountScript counts the datagrams that arrive on a port and prints the count when it gets "q".
const udpCountScript = `
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", int(sys.argv[1])))
n = 0
while True:
    d, a = s.recvfrom(100)
    if d == b"q":
        s.sendto(str(n).encode(), a)
        n = 0
    else:
        n += 1
`

// interceptBed is the bed of the TLS responder with the proxy in the service namespace, a client A that trusts the test CA and the HTTPS server.
type interceptBed struct {
	*tlsBed
	proxy *testbed.Proxy
	srvIP string
}

func newInterceptBed(t *testing.T) *interceptBed {
	t.Helper()
	py, ok := testbed.MitmPython()
	if !ok {
		if os.Getenv(testbed.RequireMitmEnv) == "1" || os.Getenv("CI") != "" {
			t.Fatal("mitmproxy is not installed (the testbed image has it in /opt/mitm-venv)")
		}
		t.Skip("mitmproxy is not installed")
	}
	b := newTLSBed(t, func(o *tlsresponder.Options, ns string) { o.Proxy = proxyListen; o.ProxyDial = testbed.DialerIn(ns) })
	ib := &interceptBed{tlsBed: b, srvIP: testbed.ServerAddr}

	// the control interface of the responder, on the loopback of the namespace
	ln, err := testbed.ListenIn(b.ns, "tcp", controlListen)
	if err != nil {
		t.Fatal(err)
	}
	// the cleanups run last in, first out: the one of the bed that ends b.ctx runs after this one, so the control interface has a context of its own
	cctx, cancelControl := context.WithCancel(b.ctx)
	done := make(chan struct{})
	go func() { _ = b.srv.ServeControlOn(cctx, ln); close(done) }()
	t.Cleanup(func() { cancelControl(); <-done })

	// the HTTPS server of the real server, with the certificate of the CA a device trusts as a public one (the proxy verifies it against that CA)
	b.top.Server.Start("python3", "-c", httpsServerScript, b.publicCert, b.publicKey, strconv.Itoa(httpsPort))

	ib.proxy, err = testbed.StartProxy(py, testbed.ProxyOptions{
		Namespace: b.ns, Listen: proxyListen, Control: "http://" + controlListen, API: b.base, TokenFile: b.tokenFile,
		CADir: filepath.Join(b.g.root, "tlsca"), UpstreamCA: b.publicOnly, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ib.proxy.Stop()
		if t.Failed() {
			t.Logf("the proxy wrote:\n%s", ib.proxy.Output())
		}
	})
	if err := ib.proxy.WaitReady(90 * time.Second); err != nil {
		t.Fatal(err)
	}
	// the device client-a is known to the gateway by its Ethernet address, for the capture
	id := b.g.mustPatch(map[string]any{"devices": map[string]any{espID: map[string]any{"name": clientA, "network": iotID,
		"trusts_test_ca": true, "identifiers": map[string]any{"macs": []any{testbed.ClientAMAC}}}}})
	b.g.token = b.admin
	if r := b.g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	_, _ = b.top.A.Run(context.Background(), "ping", "-c", "1", "-W", "2", testbed.ServerAddr)
	b.g.observe()
	return ib
}

// curl runs curl in A with the given arguments and returns its output; the exit code is in the error.
func (b *interceptBed) curl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	base := []string{"-sS", "--max-time", "30", "--cacert", b.trust, "--resolve", fmt.Sprintf("%s:%d:%s", tlsBroker, httpsPort, b.srvIP)}
	return b.top.A.Run(ctx, "curl", append(base, args...)...)
}

func (b *interceptBed) url(path string) string {
	return fmt.Sprintf("https://%s:%d%s", tlsBroker, httpsPort, path)
}

// untilIntercepted waits until a request of A through the proxy is answered by the proxy's certificate (the case is in the kernel, in the
// snapshot and in the proxy's configuration).
func (b *interceptBed) untilIntercepted(t *testing.T, path string) {
	t.Helper()
	waitFor(t, 60*time.Second, "A is served by the proxy", func() bool {
		out, err := b.curl("-w", "%{ssl_verify_result}|%{http_code}", "-o", "/dev/null", "-k", b.url(path))
		_ = err
		return strings.HasSuffix(strings.TrimSpace(out), "|200") && b.srv.Intercepted() > 0
	})
}

// A device that trusts the test CA sees the modified responses: a status, a block, a modification of header and body, and a response that comes
// after the delay of its rule; the requests the rules answer never reach the server. The connection of a device that keeps it open is one connection of
// the proxy (curl reuses it for the three URLs of one call), and every request is an exchange of the log.
func TestADeviceTrustingTheTestCASeesTheModifiedResponsesAndDelaysOnTheRealKernel(t *testing.T) {
	b := newInterceptBed(t)
	b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"intercept","ports":[`+strconv.Itoa(httpsPort)+`],"intercept":{"http_rules":[
		{"match":{"path_prefix":"/fail"},"action":"status","status":503},
		{"match":{"path_prefix":"/secret"},"action":"block"},
		{"match":{"path_prefix":"/slow"},"action":"delay","delay":"1500ms"},
		{"match":{"path_prefix":"/mod"},"action":"modify","modify":{"response_headers":{"X-Chaos":"yes","X-Real":""},"response_body_replace":[{"search":"real server","replace":"gateway"}]}}]}}}`)
	b.untilIntercepted(t, "/plain")

	out, err := b.curl("-w", "\n%{http_code}", b.url("/plain"))
	if err != nil || !strings.HasPrefix(out, "hello from the real server /plain") || !strings.HasSuffix(strings.TrimSpace(out), "200") {
		t.Fatalf("a request no rule selects: %v %q", err, out)
	}
	if out, err = b.curl("-o", "/dev/null", "-w", "%{http_code}", b.url("/fail/x")); err != nil || out != "503" {
		t.Errorf("status: %v %q", err, out)
	}
	if out, err = b.curl("-w", "\n%{http_code}", b.url("/secret/x")); err != nil || !strings.Contains(out, "Blocked by Chaos Gateway") || !strings.HasSuffix(out, "403") {
		t.Errorf("block: %v %q", err, out)
	}
	if out, err = b.curl("-i", b.url("/mod/x")); err != nil || !strings.Contains(out, "hello from the gateway /mod/x") || !strings.Contains(strings.ToLower(out), "x-chaos: yes") || strings.Contains(strings.ToLower(out), "x-real") {
		t.Errorf("modify: %v %q", err, out)
	}
	// the delay: the response arrives after the time the rule names, and one that no rule selects does not
	out, err = b.curl("-o", "/dev/null", "-w", "%{time_total}", b.url("/slow/x"))
	if secs, perr := strconv.ParseFloat(strings.TrimSpace(out), 64); err != nil || perr != nil || secs < 1.5 || secs > 5 {
		t.Errorf("a delayed response took %q (%v %v)", out, err, perr)
	}
	out, err = b.curl("-o", "/dev/null", "-w", "%{time_total}", b.url("/fast"))
	if secs, perr := strconv.ParseFloat(strings.TrimSpace(out), 64); err != nil || perr != nil || secs > 1.4 {
		t.Errorf("a response that no rule selects took %q (%v %v)", out, err, perr)
	}

	// connection reuse: three requests in one curl call are one connection (curl's num_connects is 1 for the first request and 0 for the others),
	// so the responder hands over once more, not three times
	before := b.srv.Intercepted()
	// (curl applies the last -w to every transfer of the call, and -o has to be given once for each)
	out, err = b.curl("-o", "/dev/null", "-o", "/dev/null", "-o", "/dev/null", "-w", "%{num_connects} ", b.url("/r1"), b.url("/r2"), b.url("/r3"))
	if err != nil || strings.TrimSpace(out) != "1 0 0" {
		t.Errorf("curl reused the connection: %v %q", err, out)
	}
	if got := b.srv.Intercepted() - before; got != 1 {
		t.Errorf("three requests on one connection were %d hand-overs", got)
	}

	// the log of the proxy has the exchanges, with the device and the rules that acted
	b.g.token = b.admin
	waitFor(t, 20*time.Second, "the exchanges are in the log", func() bool {
		r := b.g.do("GET", "/intercept/flows?device="+clientA+"&host="+tlsBroker, nil, nil, nil)
		return r.Status == 200 && len(r.json(t)["items"].([]any)) >= 9
	})
	flows := b.g.do("GET", "/intercept/flows?faulted=true", nil, nil, nil).json(t)["items"].([]any)
	var acted []string
	for _, f := range flows {
		m := f.(map[string]any)
		acted = append(acted, fmt.Sprint(m["path"], " ", m["status"]))
	}
	if len(acted) != 4 {
		t.Errorf("exchanges a rule acted on: %v", acted)
	}
}

// block_quic drops the UDP 443 of the clients of the case (while the case is in force) and nothing else: a datagram of the device to port 443 of the
// server does not arrive, one to another port does, a client the case does not select is not touched, and the datagrams of the device arrive again when the
// case is gone, or when it turns the option off.
func TestBlockQUICDropsUDP443OfTheClientsOfTheCaseOnTheRealKernel(t *testing.T) {
	b := newInterceptBed(t)
	b.top.Server.Start("python3", "-c", udpCountScript, "443")
	b.top.Server.Start("python3", "-c", udpCountScript, "4443")
	// send sends n datagrams from a namespace to a UDP port of the server; count asks the server, from B (which no case selects), how many arrived
	// since it was asked last
	send := func(ns *testbed.Namespace, port string, n int) {
		t.Helper()
		out, err := ns.Run(context.Background(), "python3", "-c", fmt.Sprintf(`
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for i in range(%d):
    s.sendto(b"x", ("%s", %s))
`, n, b.srvIP, port))
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	count := func(port string) int {
		t.Helper()
		time.Sleep(500 * time.Millisecond)
		out, err := b.top.B.Run(context.Background(), "python3", "-c", fmt.Sprintf(`
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)
s.sendto(b"q", ("%s", %s))
print(int(s.recvfrom(100)[0]))
`, b.srvIP, port))
		n, perr := strconv.Atoi(strings.TrimSpace(out))
		if err != nil || perr != nil {
			t.Fatalf("%v %v %q", err, perr, out)
		}
		return n
	}
	fromA := func(port string, n int) int { send(b.top.A, port, n); return count(port) }

	waitFor(t, 30*time.Second, "the datagrams arrive without a case", func() bool { return fromA("443", 5) == 5 })
	id := b.overlay(t, `{"target":{"device":"`+clientA+`"},"tls":{"case":"intercept","ports":[`+strconv.Itoa(httpsPort)+`]}}`)
	waitFor(t, 40*time.Second, "UDP 443 of A is dropped", func() bool { return fromA("443", 5) == 0 })
	if got := fromA("4443", 5); got != 5 {
		t.Errorf("%d of 5 datagrams of A reached UDP 4443 while the case was in force", got)
	}
	send(b.top.B, "443", 3)
	if got := count("443"); got != 3 {
		t.Errorf("a client the case does not select lost UDP 443: %d of 3 datagrams arrived", got)
	}
	b.remove(t, id)
	waitFor(t, 40*time.Second, "UDP 443 of A arrives again", func() bool { return fromA("443", 5) == 5 })
	b.overlay(t, `{"target":{"device":"`+clientA+`"},"tls":{"case":"intercept","ports":[`+strconv.Itoa(httpsPort)+`],"intercept":{"block_quic":false}}}`)
	time.Sleep(3 * time.Second)
	if got := fromA("443", 5); got != 5 {
		t.Errorf("block_quic false: %d of 5 datagrams arrived", got)
	}
}

// A device that does not trust the test CA rejects the certificate of the proxy, which the proxy reports (and the check `tls_rejected` of a run
// sees); one that does is served, which the check `tls_accepted` sees. The step is the step `tls` with the case `intercept`.
func TestAnInterceptionStepOfARunIsSeenByTheTLSChecksOnTheRealKernel(t *testing.T) {
	b := newInterceptBed(t)
	b.g.mintToken("overlays")
	run := func(name, check string, trusting bool) map[string]any {
		body := inlineRun(name, iotTarget,
			step("case", "0s", "tls", map[string]any{"case": "intercept", "ports": []int{httpsPort}}),
			step("end", "14s", "restore", true))
		body["inline"].(map[string]any)["checks"] = []any{map[string]any{check: map[string]any{"protocol": "tcp", "ports": []int{httpsPort}}, "window": map[string]any{"from": "case", "until": "end"}}}
		r := b.g.postRun(body, "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id := r.json(t)["id"].(string)
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				args := []string{b.url("/probe"), "-o", "/dev/null"}
				if !trusting {
					// a device that trusts only the public CA and says so in the handshake (curl completes the handshake and checks the certificate
					// after it, which a server cannot tell from a device that takes the certificate)
					_ = b.client(b.top.A, b.srvIP, httpsPort, tlsBroker, "strict", b.publicOnly, "any", 5, 0)
				} else {
					_, _ = b.curl(args...)
				}
				time.Sleep(700 * time.Millisecond)
			}
		}()
		done := b.g.waitRun(id, "passed", "failed", "error")
		close(stop)
		return done
	}
	check := func(done map[string]any) map[string]any { return done["checks"].([]any)[0].(map[string]any) }
	if d := run("trusting-accepted", "tls_accepted", true); d["status"] != "passed" || check(d)["status"] != "passed" {
		t.Errorf("a device that trusts the test CA did not complete the handshake with the proxy: %v %v", d["status"], check(d))
	}
	if d := run("not-trusting-rejected", "tls_rejected", false); d["status"] != "passed" || check(d)["status"] != "passed" {
		t.Errorf("a device that trusts only the public CA did not reject the certificate of the proxy: %v %v", d["status"], check(d))
	}
	// the proxy's handshake events say what the device did
	var accepted, rejected int
	for _, ev := range b.handshakes() {
		if ev["case"] == "intercept" && ev["outcome"] == "accepted" {
			accepted++
		}
		if ev["case"] == "intercept" && ev["outcome"] == "rejected" {
			rejected++
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Errorf("accepted %d, rejected %d: %v", accepted, rejected, b.handshakes())
	}
}

// The key log decrypts a capture that the real executor made of the device: the key log that comes with the capture of the device holds the secrets
// of the connection the device made through the proxy while the capture ran, and tshark reads the request and the response in it.
func TestTheKeyLogDecryptsTheCaptureOfARealConnectionOnTheRealKernel(t *testing.T) {
	if _, err := exec.LookPath("tshark"); err != nil {
		t.Fatalf("tshark is not installed (the testbed image has it): %v", err)
	}
	b := newInterceptBed(t)
	b.overlay(t, `{"target":{"device":"`+clientA+`"},"tls":{"case":"intercept","ports":[`+strconv.Itoa(httpsPort)+`]}}`)
	b.untilIntercepted(t, "/warm")
	b.g.token = b.admin
	c := b.g.mustStartCapture(map[string]any{"name": "keylog", "scope": map[string]any{"device": clientA}})
	id := c["id"].(string)
	if out, err := b.curl("-w", "\n%{http_code}", b.url("/telemetry/secret-path")); err != nil || !strings.Contains(out, "hello from the real server /telemetry/secret-path") {
		t.Fatalf("%v %q", err, out)
	}
	time.Sleep(2 * time.Second) // the proxy flushes the secrets of a connection that has ended
	b.g.stopCapture(id)
	capture := b.g.do("GET", "/captures/"+id+"/download", nil, nil, nil)
	keylog := b.g.do("GET", "/captures/"+id+"/download?part=keylog", nil, nil, nil)
	if capture.Status != 200 || keylog.Status != 200 || len(keylog.Body) == 0 {
		t.Fatalf("capture %d, key log %d (%d bytes): %s", capture.Status, keylog.Status, len(keylog.Body), keylog.Body)
	}
	if cp := b.g.capture(id); cp["keylog"] != true || cp["sensitive"] != true {
		t.Errorf("%v", cp)
	}
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pcapng"), filepath.Join(dir, "k.txt")
	if err := os.WriteFile(cf, capture.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kf, keylog.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(withLog bool) string {
		args := []string{"-r", cf, "-d", fmt.Sprintf("tcp.port==%d,tls", httpsPort)}
		if withLog {
			args = append(args, "-o", "tls.keylog_file:"+kf)
		}
		args = append(args, "-Y", "http", "-T", "fields", "-e", "http.request.uri", "-e", "http.file_data")
		out, err := exec.Command("tshark", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("tshark: %v\n%s", err, out)
		}
		return string(out)
	}
	if got := read(false); strings.Contains(got, "secret-path") {
		t.Errorf("the capture shows the request without a key log: %s", got)
	}
	got := read(true)
	if !strings.Contains(got, "/telemetry/secret-path") {
		t.Errorf("tshark does not read the request with the key log: %q", got)
	}
	// the response body is hex in http.file_data
	if !strings.Contains(got, fmt.Sprintf("%x", []byte("hello from the real server /telemetry/secret-path"))) && !bytes.Contains([]byte(got), []byte("hello from the real server")) {
		t.Errorf("tshark does not read the response with the key log: %q", got)
	}
}

// Without the proxy the connections of an interception case are reset, not passed on and not answered: the device meets a failed connection, the
// responder reports `proxy_unavailable`, and the health check says why.
func TestWithoutTheProxyTheConnectionsOfTheCaseAreResetOnTheRealKernel(t *testing.T) {
	b := newInterceptBed(t)
	b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"intercept","ports":[`+strconv.Itoa(httpsPort)+`]}}`)
	b.untilIntercepted(t, "/up")
	b.proxy.Stop()
	waitFor(t, 40*time.Second, "A meets a reset", func() bool {
		_, err := b.curl("-o", "/dev/null", b.url("/down"))
		return err != nil
	})
	waitFor(t, 20*time.Second, "the responder reports that the proxy is not there", func() bool {
		for _, ev := range b.handshakes() {
			if ev["outcome"] == "proxy_unavailable" {
				return true
			}
		}
		return false
	})
	if b.srv.ProxyFailures() == 0 {
		t.Error("the responder counted no failure")
	}
}

// The policy of a case for the certificate of the real server (P5-M22-03), over the API on the real kernel. The proxy of the bed is started
// with the CA of the real server as its installation default (CHAOSGW_UPSTREAM_CA_FILE), so a case without a policy and one with own_ca are served;
// the CA is no CA of the bundle of mitmproxy, so a case that says verify is refused with a 502 and the server sees no request, and insecure serves
// it again. Cases are replaced while the proxy runs: the proxy takes the policy with the next configuration, not with a restart.
func TestTheUpstreamPolicyOfACaseDecidesWhetherTheProxyAcceptsTheServersCertificateOnTheRealKernel(t *testing.T) {
	b := newInterceptBed(t)
	caseWith := func(policy string) string {
		settings := ""
		if policy != "" {
			settings = `,"intercept":{"upstream":"` + policy + `"}`
		}
		return `{"target":{"network":"IoT"},"tls":{"case":"intercept","ports":[` + strconv.Itoa(httpsPort) + `]` + settings + `}}`
	}
	status := func(path string) string {
		out, _ := b.curl("-o", "/dev/null", "-w", "%{http_code}", b.url(path))
		return strings.TrimSpace(out)
	}
	id := b.overlay(t, caseWith(""))
	b.untilIntercepted(t, "/warm")
	for _, c := range []struct{ policy, want string }{{"verify", "502"}, {"own_ca", "200"}, {"insecure", "200"}, {"verify", "502"}, {"", "200"}} {
		b.remove(t, id)
		id = b.overlay(t, caseWith(c.policy))
		waitFor(t, 60*time.Second, "the case with the policy "+c.policy+" is in force: "+c.want, func() bool { return status("/policy") == c.want })
	}
	// what the device is told when the proxy refuses the server, and the exchange that says why
	b.remove(t, id)
	id = b.overlay(t, caseWith("verify"))
	waitFor(t, 60*time.Second, "the case is in force", func() bool { return status("/refused") == "502" })
	out, err := b.curl(b.url("/refused-body"))
	if err != nil || !strings.Contains(out, "Certificate verify failed") {
		t.Errorf("the device is told why: %v %q", err, out)
	}
	b.g.token = b.admin
	waitFor(t, 20*time.Second, "the refusal is in the log of the exchanges", func() bool {
		r := b.g.do("GET", "/intercept/flows?device="+clientA, nil, nil, nil)
		if r.Status != 200 {
			return false
		}
		for _, f := range r.json(t)["items"].([]any) {
			if e, _ := f.(map[string]any)["error"].(string); strings.Contains(e, "TLS to the server failed") {
				return true
			}
		}
		return false
	})
	b.remove(t, id)
}

// A client that speaks QUIC (a real QUIC stack, aioquic, which mitmproxy brings) tries HTTP/3 to port 443 first, as a browser does that has learned the
// server's `Alt-Svc`, and goes on with TCP when nothing answers. With an interception case that blocks QUIC the Initial packets never reach the server, the
// client's own timeout ends the attempt and its TCP connection is the one the proxy intercepts: the client is served the modified response. Without a
// case the packets arrive (the server sees the client's Initial), which is what makes the first half of the test mean something.
func TestAQUICClientFallsBackToTCPAndIsInterceptedOnTheRealKernel(t *testing.T) {
	py, ok := testbed.MitmPython()
	if !ok {
		t.Fatal("mitmproxy is not installed (the testbed image has it in /opt/mitm-venv, with aioquic)")
	}
	b := newInterceptBed(t)
	b.top.Server.Start("python3", "-c", udpCountScript, "443")
	h3First := func() map[string]any {
		t.Helper()
		out, err := b.top.A.Run(context.Background(), py, "-c", http3FirstScript, b.srvIP, b.trust, b.url("/via-fallback"), tlsBroker, strconv.Itoa(httpsPort))
		if err != nil {
			t.Fatalf("the client: %v %s", err, out)
		}
		var res map[string]any
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.HasPrefix(l, "{") {
				if err := json.Unmarshal([]byte(l), &res); err != nil {
					t.Fatalf("%v %q", err, l)
				}
			}
		}
		if res == nil {
			t.Fatalf("the client printed no result: %q", out)
		}
		return res
	}
	arrived := func() int {
		t.Helper()
		time.Sleep(500 * time.Millisecond)
		out, err := b.top.B.Run(context.Background(), "python3", "-c", fmt.Sprintf(`
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(3)
s.sendto(b"q", ("%s", 443))
print(int(s.recvfrom(100)[0]))
`, b.srvIP))
		n, perr := strconv.Atoi(strings.TrimSpace(out))
		if err != nil || perr != nil {
			t.Fatalf("%v %v %q", err, perr, out)
		}
		return n
	}
	arrived() // reset the count
	// no case: the client's QUIC packets reach the server, which never answers, and the client falls back to TCP, which nothing intercepts
	res := h3First()
	if res["quic"] != "timeout" || res["tcp"] != "hello from the real server /via-fallback" {
		t.Fatalf("without a case: %v", res)
	}
	if n := arrived(); n == 0 {
		t.Fatalf("without a case the QUIC packets of the client did not reach the server")
	}

	b.overlay(t, `{"target":{"device":"`+clientA+`"},"tls":{"case":"intercept","ports":[`+strconv.Itoa(httpsPort)+`],"intercept":{"http_rules":[
		{"match":{"path_prefix":"/via-fallback"},"action":"modify","modify":{"response_body_replace":[{"search":"real server","replace":"gateway"}]}}]}}}`)
	b.untilIntercepted(t, "/warm")
	waitFor(t, 40*time.Second, "UDP 443 of A is dropped", func() bool { h3First(); return arrived() == 0 })
	res = h3First()
	if res["quic"] != "timeout" || res["tcp"] != "hello from the gateway /via-fallback" {
		t.Errorf("with the case: the client was not served by the proxy after its QUIC attempt: %v", res)
	}
	if n := arrived(); n != 0 {
		t.Errorf("%d datagrams of the client's QUIC attempt reached the server while the case was in force", n)
	}
}

// http3FirstScript is a client that tries HTTP/3 and falls back: argv are the server's address, the CA file the client trusts, the URL for the fallback, its
// host name and port. It prints one line of JSON: how the QUIC attempt ended and what the TCP request returned.
const http3FirstScript = `
import asyncio, json, ssl, sys, urllib.request
from aioquic.asyncio import connect
from aioquic.quic.configuration import QuicConfiguration
addr, cafile, url, host, port = sys.argv[1:6]

async def quic():
    cfg = QuicConfiguration(is_client=True, alpn_protocols=["h3"], verify_mode=ssl.CERT_NONE, server_name=host)
    try:
        async with asyncio.timeout(3):
            async with connect(addr, 443, configuration=cfg):
                return "connected"
    except TimeoutError:
        return "timeout"

res = {"quic": asyncio.run(quic())}
ctx = ssl.create_default_context(cafile=cafile)
# the name resolves to the server's address, as curl's --resolve does
import socket
real = socket.getaddrinfo
socket.getaddrinfo = lambda h, *a, **k: real(addr if h == host else h, *a, **k)
with urllib.request.urlopen(url, context=ctx, timeout=20) as r:
    res["tcp"] = r.read().decode().strip()
print(json.dumps(res))
`

// httpsPeerServerScript is httpsServerScript with the address of its peer in a header: the address the server sees.
var httpsPeerServerScript = strings.Replace(httpsServerScript, `self.send_header("X-Real", "1")`, `self.send_header("X-Real", "1"); self.send_header("X-Peer", self.client_address[0])`, 1)

// An `intercept` case works for a server in another test network and for one behind a WireGuard client (plan M22 "Tests", P5-M21-02): the proxy's own connection
// takes the path that the responder's pass-through takes (the service namespace's masqueraded connection through the forward chain to the test
// interfaces and the tunnel), so the device is served by the proxy with the modified response, the server sees the gateway's address in the network it is
// in and not the device's, and the exchange is in the log with the address the device meant. Where the access matrix refuses the connection, the responder
// resets it before it reaches the proxy.
func TestAnInterceptCaseReachesAServerInAnotherTestNetworkAndBehindAWireGuardClientOnTheRealKernel(t *testing.T) {
	if !strings.Contains(httpsPeerServerScript, "X-Peer") {
		t.Fatal("the script of the HTTPS server that names its peer does not apply to the real server's script")
	}
	b := newInterceptBed(t)
	const labID = "5d3a8e6f-2b1c-4d7e-9f08-a1b2c3d4e5f6"
	iotToLab := map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "Lab"}, "policy": "allow"}
	iotToHub := map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "lab-hub"}, "policy": "allow"}
	iotToSite := map[string]any{"from": map[string]any{"network": "IoT"}, "to": map[string]any{"network": "site-b"}, "policy": "allow"}
	b.g.token = b.admin
	id := b.g.mustPatch(map[string]any{
		"networks": map[string]any{
			labID: map[string]any{"type": "lan", "name": "Lab", "address": testbed.LAN1Gateway + "/24",
				"interfaces": []any{map[string]any{"name": "lan1", "mac": testbed.GWLan1MAC}}},
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
	b.top.C.Start("python3", "-c", httpsPeerServerScript, b.publicCert, b.publicKey, strconv.Itoa(httpsPort))
	b.top.RC.Start("python3", "-c", httpsPeerServerScript, b.publicCert, b.publicKey, strconv.Itoa(httpsPort))
	servers := []struct{ name, addr, sees string }{
		{"the server in the network Lab", testbed.ClientCAddr, testbed.LAN1Gateway},
		{"the server behind the WireGuard client", testbed.ClientNetHost, "10.99.0.1"},
	}
	get := func(addr, path string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return b.top.A.Run(ctx, "curl", "-sS", "-i", "--max-time", "30", "--cacert", b.trust, "--resolve", fmt.Sprintf("%s:%d:%s", tlsBroker, httpsPort, addr), b.url(path))
	}
	// without a case the device reaches both itself, and the server sees the device
	for _, srv := range servers {
		waitFor(t, 60*time.Second, srv.name+" answers the device", func() bool {
			out, err := get(srv.addr, "/direct")
			return err == nil && strings.Contains(out, "hello from the real server /direct") && strings.Contains(strings.ToLower(out), "x-peer: "+testbed.ClientAAddr)
		})
	}

	oid := b.overlay(t, `{"target":{"network":"IoT"},"tls":{"case":"intercept","ports":[`+strconv.Itoa(httpsPort)+`],"intercept":{"http_rules":[
		{"match":{"path_prefix":"/mod"},"action":"modify","modify":{"response_body_replace":[{"search":"real server","replace":"gateway"}]}}]}}}`)
	b.untilIntercepted(t, "/warm")
	for _, srv := range servers {
		before := b.srv.Intercepted()
		var out string
		waitFor(t, 60*time.Second, srv.name+" is served through the proxy", func() bool {
			var err error
			out, err = get(srv.addr, "/mod/x")
			t.Logf("%s: %v %q", srv.name, err, out)
			return err == nil && strings.Contains(out, "hello from the gateway /mod/x") && b.srv.Intercepted() > before
		})
		// the proxy's own connection is what the server sees: the gateway's address in its network, not the device's
		if want := "x-peer: " + srv.sees; !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("%s: the server did not see the gateway's address (%s): %q", srv.name, srv.sees, out)
		}
	}
	// the log has the exchanges with the address the device meant
	b.g.token = b.admin
	for _, srv := range servers {
		waitFor(t, 20*time.Second, "the exchange with "+srv.name+" is in the log", func() bool {
			r := b.g.do("GET", "/intercept/flows?device="+clientA+"&host="+tlsBroker, nil, nil, nil)
			if r.Status != 200 {
				return false
			}
			for _, f := range r.json(t)["items"].([]any) {
				m := f.(map[string]any)
				if m["original_dst"] == fmt.Sprintf("%s:%d", srv.addr, httpsPort) && m["path"] == "/mod/x" && m["status"] == float64(200) {
					return true
				}
			}
			return false
		})
	}

	// the matrix keeps the device from both networks: the responder resets the connections of the case before they reach the proxy
	if r := b.g.apply(b.g.mustPatch(map[string]any{"access_matrix": map[string]any{"entries": []any{iotToSite}}})); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	for _, srv := range servers {
		denied := b.srv.Denied()
		waitFor(t, 40*time.Second, "the matrix keeps the device from "+srv.name+", the proxy included", func() bool {
			_, err := get(srv.addr, "/refused")
			return err != nil && b.srv.Denied() > denied
		})
	}
	b.remove(t, oid)
}
