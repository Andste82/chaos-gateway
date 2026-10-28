//go:build testbed

package api_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/api"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// M24 level 1 tests: the diagnostics of the API run in the namespace of the gateway against the server namespace of the
// testbed, which has the reference services (TCP, UDP echo, HTTP, DNS, TLS with the responder's certificates), over the
// real wires of the topology. The same diagnostics against targets on the loopback are in diagnostics_test.go.

const (
	diagTCPPort  = 9000
	diagUDPPort  = 9001
	diagHTTPPort = 8080
)

// startDiagnosticTargets starts the reference services in the server namespace: TCP on 9000, a UDP echo on 9001, HTTP on
// 8080 (/fail answers 503), DNS on 53 (broker.example.com is the server's address), and the TLS server on 8883 with the
// expired certificate of the responder, on 8884 with its hostname mismatch and on 8885 with a certificate of the test CA.
func startDiagnosticTargets(t *testing.T, g *gw, top *testbed.Topology) {
	t.Helper()
	ns := top.Server.Name
	at := func(port int) string { return fmt.Sprintf("%s:%d", testbed.ServerAddr, port) }

	tl, err := testbed.ListenIn(ns, "tcp", at(diagTCPPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tl.Close() })
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	uc, err := testbed.ListenPacketIn(ns, "udp", at(diagUDPPort))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := uc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = uc.WriteTo(buf[:n], from)
		}
	}()

	hl, err := testbed.ListenIn(ns, "tcp", at(diagHTTPPort))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Testbed", "server")
		_, _ = w.Write([]byte("ok"))
	})
	hs := &http.Server{Handler: mux}
	go func() { _ = hs.Serve(hl) }()
	t.Cleanup(func() { _ = hs.Close() })

	dc, err := testbed.ListenPacketIn(ns, "udp", at(53))
	if err != nil {
		t.Fatal(err)
	}
	ds := &dns.Server{PacketConn: dc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		m := new(dns.Msg)
		if q.Question[0].Name != "broker.example.com." {
			m.SetRcode(q, dns.RcodeNameError)
		} else {
			m.SetReply(q)
			m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(testbed.ServerAddr).To4()})
		}
		_ = w.WriteMsg(m)
	})}
	started := make(chan struct{})
	ds.NotifyStartedFunc = func() { close(started) }
	go func() { _ = ds.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = ds.Shutdown() })

	cas, err := tlsresponder.LoadCAs(filepath.Join(g.root, "tlsca"))
	if err != nil {
		t.Fatal(err)
	}
	iss, err := tlsresponder.NewIssuer(cas, realClock{})
	if err != nil {
		t.Fatal(err)
	}
	serveTLSIn := func(port int, certs ...tls.Certificate) {
		l, err := testbed.ListenIn(ns, "tcp", at(port))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() {
					defer func() { _ = c.Close() }()
					_ = tls.Server(c, &tls.Config{Certificates: certs}).Handshake()
				}()
			}
		}()
	}
	expired, err := iss.Certificates(model.TlsCaseCaseExpired, "broker.example.com")
	if err != nil {
		t.Fatal(err)
	}
	serveTLSIn(8883, expired...)
	mismatch, err := iss.Certificates(model.TlsCaseCaseHostnameMismatch, "broker.example.com")
	if err != nil {
		t.Fatal(err)
	}
	serveTLSIn(8884, mismatch...)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "broker.example.com"}, DNSNames: []string{"broker.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, cas.Test.Cert, &key.PublicKey, cas.Test.Key)
	if err != nil {
		t.Fatal(err)
	}
	serveTLSIn(8885, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})
}

func diagBedGW(t *testing.T) (*gw, *testbed.Topology) {
	t.Helper()
	g, top := newBedGW(t, func(o *options) {
		o.diagnostics = api.DiagnosticsConfig{Servers: func() []netip.AddrPort {
			return []netip.AddrPort{netip.MustParseAddrPort(testbed.ServerAddr + ":53")}
		}}
	})
	g.mintToken("overlays")
	startDiagnosticTargets(t, g, top)
	return g, top
}

// Every diagnostic against the reference services of the server namespace, and the failures: a closed port, a host
// nobody answers for, a name that does not exist, a certificate that does not verify.
func TestEveryDiagnosticAgainstTheServerNamespaceOfTheTestbed(t *testing.T) {
	g, _ := diagBedGW(t)
	srv := testbed.ServerAddr

	r := g.diagnoseOK(map[string]any{"type": "ping", "host": srv, "count": 3, "timeout": "3s"})
	p := sub(r, "ping")
	if r["ok"] != true || p["sent"] != float64(3) || p["received"] != float64(3) || p["loss_percent"] != float64(0) || p["rtt_avg_ms"].(float64) <= 0 {
		t.Errorf("ping: %v", r)
	}
	// a host on the segment that does not exist: the ARP request goes unanswered
	r = g.diagnoseOK(map[string]any{"type": "ping", "host": "203.0.113.99", "count": 2, "timeout": "2s"})
	if r["ok"] != false || sub(r, "ping")["received"] != float64(0) || sub(r, "ping")["loss_percent"] != float64(100) {
		t.Errorf("ping nobody: %v", r)
	}

	r = g.diagnoseOK(map[string]any{"type": "tcp", "host": srv, "port": diagTCPPort})
	if r["ok"] != true || sub(r, "connect")["reachable"] != true || sub(r, "connect")["rtt_ms"] == nil {
		t.Errorf("tcp: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "tcp", "host": srv, "port": 9999})
	if r["ok"] != false || sub(r, "connect")["detail"] != "connection refused" {
		t.Errorf("tcp closed: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "tcp", "host": "203.0.113.99", "port": 80, "timeout": "1s"})
	if r["ok"] != false || sub(r, "connect")["reachable"] != false {
		t.Errorf("tcp nobody: %v", r)
	}

	r = g.diagnoseOK(map[string]any{"type": "udp", "host": srv, "port": diagUDPPort, "payload": "echo"})
	if r["ok"] != true || sub(r, "connect")["detail"] != "reply of 4 bytes" {
		t.Errorf("udp: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "udp", "host": srv, "port": 9998, "timeout": "2s"})
	if r["ok"] != false || sub(r, "connect")["detail"] != "connection refused" {
		t.Errorf("udp closed: %v", r)
	}

	r = g.diagnoseOK(map[string]any{"type": "dns", "host": srv, "dns_name": "broker.example.com"})
	if r["ok"] != true || fmt.Sprint(sub(r, "dns")["answers"]) != "["+srv+"]" || sub(r, "dns")["server"] != srv+":53" {
		t.Errorf("dns: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "dns", "host": srv, "dns_name": "nothing.example.com"})
	// the resolver of the server namespace refuses a name outside its zone; either answer says the upstream does not know it
	if rc := sub(r, "dns")["rcode"]; r["ok"] != false || (rc != "NXDOMAIN" && rc != "REFUSED") {
		t.Errorf("dns nxdomain: %v", r)
	}

	r = g.diagnoseOK(map[string]any{"type": "http", "host": "x", "url": fmt.Sprintf("http://broker.example.com:%d/", diagHTTPPort)})
	if r["ok"] != true || r["address"] != srv || sub(sub(r, "http"), "headers")["X-Testbed"] != "server" {
		t.Errorf("http by name: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "http", "host": "x", "url": fmt.Sprintf("http://%s:%d/fail", srv, diagHTTPPort)})
	if r["ok"] != false || sub(r, "http")["status"] != float64(503) {
		t.Errorf("http 503: %v", r)
	}

	r = g.diagnoseOK(map[string]any{"type": "tls", "host": srv, "port": 8885, "sni": "broker.example.com"})
	if r["ok"] != true || sub(r, "tls")["verified"] != true {
		t.Errorf("tls, test CA: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "tls", "host": srv, "port": 8883, "sni": "broker.example.com"})
	if r["ok"] != false || r["error"] != "certificate has expired" {
		t.Errorf("tls expired: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "tls", "host": srv, "port": 8884, "sni": "broker.example.com"})
	if r["ok"] != false || sub(r, "tls")["verified"] != false {
		t.Errorf("tls mismatch: %v", r)
	}

	// the path of the plan's example: DNS ✓ Route ✓ TCP ✓ TLS ✗ (certificate expired)
	r = g.diagnoseOK(map[string]any{"type": "path", "host": "broker.example.com", "port": 8883})
	steps := r["path"].([]any)
	if r["ok"] != false || len(steps) != 4 || steps[2].(map[string]any)["ok"] != true || steps[3].(map[string]any)["detail"] != "certificate has expired" {
		t.Errorf("path: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "path", "host": "broker.example.com", "port": 8885})
	if r["ok"] != true || len(r["path"].([]any)) != 4 {
		t.Errorf("path ok: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "path", "host": "nothing.example.com", "port": 8885})
	if r["ok"] != false || len(r["path"].([]any)) != 1 {
		t.Errorf("path unresolved: %v", r)
	}
}

// The diagnostics come from the gateway's own address (plan §2.12): a fault on the test network that drops every packet
// of its devices does not touch them, while a device of the network no longer gets through.
func TestDiagnosticsFromTheGatewayDoNotPassThroughTheFaultsOfDevices(t *testing.T) {
	g, top := diagBedGW(t)
	if !pingOK(top.A, "", testbed.ServerAddr) {
		t.Fatal("the device cannot reach the server before the fault")
	}
	r := g.createOverlay(`{"target":{"network":"IoT"},"fault":{"loss":"100%"},"ttl":"10m"}`)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if pingOK(top.A, "", testbed.ServerAddr) {
		t.Fatal("the device still reaches the server: the fault is not in force")
	}
	d := g.diagnoseOK(map[string]any{"type": "ping", "host": testbed.ServerAddr, "count": 5, "timeout": "3s"})
	if d["ok"] != true || sub(d, "ping")["loss_percent"] != float64(0) {
		t.Errorf("ping from the gateway: %v", d)
	}
	d = g.diagnoseOK(map[string]any{"type": "tcp", "host": testbed.ServerAddr, "port": diagTCPPort})
	if d["ok"] != true {
		t.Errorf("tcp from the gateway: %v", d)
	}
}

// A name is resolved the way it is in production (P6-AUDIT-62): the gateway's diagnostic asks the DNS proxy in the service
// namespace at the peer address of svc0 (no stand-in server), and a probe asks the address of its network's gateway, which the
// gateway redirects to the same proxy like the query of a device. The proxy sees the gateway as the client 169.254.100.1
// and the probe by its address, and its upstream is the resolver of the server namespace.
func TestNamesAreResolvedThroughTheDNSProxyOfTheServiceNamespace(t *testing.T) {
	b := newFaultBed(t)
	g, top := b.g, b.top
	addProbe(t, g, top, nil)
	g.mintToken("overlays")
	srv := testbed.ServerAddr

	r := g.diagnoseOK(map[string]any{"type": "dns", "host": brokerHost})
	if d := sub(r, "dns"); r["ok"] != true || fmt.Sprint(d["answers"]) != "["+srv+"]" || d["server"] != "169.254.100.2:53" {
		t.Errorf("dns by the gateway: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "path", "host": brokerHost, "port": echoPort, "tls": false})
	if r["ok"] != true || r["address"] != srv {
		t.Errorf("path by the gateway: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "dns", "host": "nothing.test"})
	if r["ok"] != false || sub(r, "dns")["rcode"] != "NXDOMAIN" {
		t.Errorf("a name the upstream does not know: %v", r)
	}

	from := map[string]any{"probe": "probe-a"}
	r = g.diagnoseOK(map[string]any{"type": "dns", "host": otherHost, "from": from})
	if d := sub(r, "dns"); r["ok"] != true || fmt.Sprint(d["answers"]) != "["+testbed.ServerAddr2+"]" || d["server"] != "10.10.0.1:53" {
		t.Errorf("dns by the probe: %v", r)
	}
	r = g.diagnoseOK(map[string]any{"type": "path", "host": brokerHost, "port": echoPort, "tls": false, "from": from})
	if r["ok"] != true || r["address"] != srv {
		t.Errorf("path by the probe: %v", r)
	}

	// the log of the proxy tells who asked
	waitFor(t, 20*time.Second, "the queries of the gateway and of the probe in the log", func() bool {
		clients := map[string]bool{}
		for _, name := range []string{brokerHost, otherHost} {
			for _, it := range g.do("GET", "/dns/queries?name="+name, nil, nil, nil).json(t)["items"].([]any) {
				clients[it.(map[string]any)["client"].(string)] = true
			}
		}
		return clients["169.254.100.1"] && clients["10.10.0.150"]
	})
}
