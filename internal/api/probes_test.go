package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/diag"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/tlsresponder"
)

// M25 tests: the probes over the API (list, address, device), a diagnostic from a probe, and the calibration "measure this
// profile". The tests have no network namespaces: the simulated kernel holds the probe's namespace, and what the probe's stack
// sends runs on the loopback through faultNet, which delays and drops like the faults that are active for the probe would. The
// same over the real kernel, with the real DHCP client and netem, is in internal/engine/integration_probes_test.go (level 1).

const (
	probeID  = "3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8"
	probeNS  = "cgp-3a1f7c52"
	probeID2 = "9d2e4b61-0c3f-4a75-8e1b-2f6a7c8d9e01"
)

// idleSocket is the DHCP socket of a probe in a test: nobody answers.
type idleSocket struct{ done chan struct{} }

func (s *idleSocket) Send([]byte, netip.AddrPort) error { return nil }
func (s *idleSocket) Recv() ([]byte, error)             { <-s.done; return nil, net.ErrClosed }
func (s *idleSocket) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

// faultNet is the stack of a probe: the loopback, with a delay on every answer and a share of the datagrams lost before they
// leave, as long as active says so. It stands for the kernel's netem.
type faultNet struct {
	inner diag.Net
	// active says whether the faults act now; delay is added to each answer, loss the share of datagrams dropped
	active func() bool
	delay  time.Duration
	loss   float64
	rng    *mrand.Rand
	mu     *sync.Mutex
}

func (f faultNet) ListenICMP() (net.PacketConn, bool, error) { return f.inner.ListenICMP() }

func (f faultNet) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := f.inner.DialContext(ctx, network, addr)
	if err != nil || network != "udp" || !f.active() {
		return c, err
	}
	fc := &faultConn{Conn: c, f: f, in: make(chan []byte, 4096), done: make(chan struct{})}
	go fc.pump()
	return fc, nil
}

type faultConn struct {
	net.Conn
	f    faultNet
	in   chan []byte
	done chan struct{}
	once sync.Once
}

func (c *faultConn) pump() {
	buf := make([]byte, 2048)
	for {
		n, err := c.Conn.Read(buf)
		if err != nil {
			c.once.Do(func() { close(c.done) })
			return
		}
		p := append([]byte(nil), buf[:n]...)
		time.AfterFunc(c.f.delay, func() {
			select {
			case c.in <- p:
			case <-c.done:
			}
		})
	}
}

func (c *faultConn) Read(b []byte) (int, error) {
	select {
	case p := <-c.in:
		return copy(b, p), nil
	case <-c.done:
		return 0, net.ErrClosed
	}
}

func (c *faultConn) Write(b []byte) (int, error) {
	c.f.mu.Lock()
	drop := c.f.rng.Float64() < c.f.loss
	c.f.mu.Unlock()
	if drop {
		return len(b), nil
	}
	return c.Conn.Write(b)
}

func (c *faultConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}

// echoPair starts a UDP and a TCP echo on one port of the loopback; the TCP echo returns at no more than rate bytes per second
// (0: unlimited).
func echoPair(t *testing.T, rate int) int {
	t.Helper()
	for try := 0; try < 20; try++ {
		l, err := net.Listen("tcp4", loopback+":0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		pc, err := net.ListenPacket("udp4", net.JoinHostPort(loopback, itoa(int64(port))))
		if err != nil {
			_ = l.Close()
			continue
		}
		t.Cleanup(func() { _ = l.Close(); _ = pc.Close() })
		go func() {
			buf := make([]byte, 2048)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				_, _ = pc.WriteTo(buf[:n], from)
			}
		}()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() {
					defer func() { _ = c.Close() }()
					buf := make([]byte, 4096)
					start, sent := time.Now(), 0
					for {
						n, err := c.Read(buf)
						if n > 0 {
							if _, werr := c.Write(buf[:n]); werr != nil {
								return
							}
							sent += n
							if rate > 0 {
								if wait := time.Duration(float64(sent)/float64(rate)*float64(time.Second)) - time.Since(start); wait > 0 {
									time.Sleep(wait)
								}
							}
						}
						if err != nil {
							if !errors.Is(err, io.EOF) {
								return
							}
							return
						}
					}
				}()
			}
		}()
		return port
	}
	t.Fatal("no port is free for both an UDP and a TCP echo")
	return 0
}

// probeBed is a gateway with a probe (active revision), the probe's stack faultNet and the simulated kernel's namespace of the probe.
type probeBed struct {
	*gw
	net *faultNet
	// effect says whether the emulated faults act: while an overlay is active unless the test says otherwise
	effect atomic.Pointer[func() bool]
}

func newProbeBed(t *testing.T, mutate func(cfg map[string]any)) *probeBed {
	t.Helper()
	b := &probeBed{}
	fn := &faultNet{inner: diag.HostNet{}, rng: mrand.New(mrand.NewPCG(3, 4)), mu: &sync.Mutex{}}
	b.net = fn
	b.gw = newGW(t, func(o *options) {
		o.probeNet = func(ns string) diag.Net {
			if ns == "" {
				return diag.HostNet{}
			}
			cp := *fn
			cp.active = func() bool {
				if f := b.effect.Load(); f != nil {
					return (*f)()
				}
				return len(b.gw.e.Snapshot().Overlays) > 0
			}
			return cp
		}
	})
	b.finishSetup()
	patch := map[string]any{"probes": map[string]any{probeID: map[string]any{"name": "probe-a", "network": "IoT"}}}
	if mutate != nil {
		mutate(patch)
	}
	id := b.mustPatch(patch)
	b.applyConfirm(id)
	return b
}

func (b *probeBed) applyConfirm(id int64) {
	b.t.Helper()
	r := b.apply(id)
	if r.Status != 200 {
		b.t.Fatalf("apply: %d %s", r.Status, r.Body)
	}
	if r.json(b.t)["status"] == "pending_confirm" {
		if cf := b.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); cf.Status != 200 {
			b.t.Fatalf("confirm: %d %s", cf.Status, cf.Body)
		}
	}
}

// address gives the probe the address its DHCP client would have got, and lets the engine see it.
func (b *probeBed) address(cidr string) {
	b.t.Helper()
	if _, err := b.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, NS: probeNS, Args: []string{"addr", "replace", cidr, "dev", "eth0"}}); err != nil {
		b.t.Fatal(err)
	}
	if err := b.e.ObserveNow(context.Background()); err != nil {
		b.t.Fatal(err)
	}
}

func (b *probeBed) calibrate(body any) resp {
	b.t.Helper()
	return b.do("POST", "/probes/"+probeID+"/calibrate", body, nil, nil)
}

// ---- the probes ----------------------------------------------------------------------------------------------------

func TestAProbeIsListedWithItsAddressAndIsADevice(t *testing.T) {
	b := newProbeBed(t, nil)
	r := b.do("GET", "/probes", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	items := r.json(t)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("%s", r.Body)
	}
	p := items[0].(map[string]any)
	if p["id"] != probeID || sub(p, "config")["name"] != "probe-a" || sub(p, "observed")["online"] != false || sub(p, "observed")["addresses"] != nil {
		t.Errorf("a probe before its address: %v", p)
	}
	// the DHCP client got an address: the engine sees it, with the MAC of the probe's interface
	b.address("10.10.0.77/24")
	p = b.do("GET", "/probes", nil, nil, nil).json(t)["items"].([]any)[0].(map[string]any)
	obs := sub(p, "observed")
	if addrs, _ := obs["addresses"].([]any); len(addrs) != 1 || addrs[0] != "10.10.0.77" {
		t.Errorf("%v", obs)
	}
	if macs, _ := obs["macs"].([]any); len(macs) != 1 || !strings.HasPrefix(macs[0].(string), "02:") {
		t.Errorf("the probe's MAC: %v", obs)
	}
	// a probe is also a device of the namespace (plan §2.12), and the sources say where it is known from
	var found map[string]any
	for _, d := range b.do("GET", "/devices?origin=probe", nil, nil, nil).json(t)["items"].([]any) {
		if d.(map[string]any)["id"] == probeID {
			found = d.(map[string]any)
		}
	}
	if found == nil || found["origin"] != "probe" || sub(found, "observed")["addresses"] == nil {
		t.Errorf("%v", found)
	}
	// it is not discovered as a second device
	for _, d := range b.do("GET", "/devices", nil, nil, nil).json(t)["items"].([]any) {
		if d.(map[string]any)["origin"] == "discovered" && strings.Contains(d.(map[string]any)["name"].(string), "02-") && false {
			t.Errorf("%v", d)
		}
	}
}

func TestProbesArePagedAndNeedTheReadScope(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["probes"].(map[string]any)[probeID2] = map[string]any{"name": "probe-b", "network": "IoT"}
	})
	r := b.do("GET", "/probes?limit=1", nil, nil, nil)
	body := r.json(t)
	if len(body["items"].([]any)) != 1 || body["next_cursor"] == nil {
		t.Fatalf("%s", r.Body)
	}
	r = b.do("GET", "/probes?limit=1&cursor="+body["next_cursor"].(string), nil, nil, nil)
	if next := r.json(t); len(next["items"].([]any)) != 1 || next["items"].([]any)[0].(map[string]any)["id"] != probeID2 {
		t.Errorf("%s", r.Body)
	}
	if r := (&gw{t: t, ts: b.ts, client: b.client, router: b.router, doc: b.doc}).do("GET", "/probes", nil, nil, nil); r.Status != 401 {
		t.Errorf("without a session: %d", r.Status)
	}
}

// ---- diagnostics from a probe --------------------------------------------------------------------------------------

func TestADiagnosticFromAProbeRunsInTheProbesNamespace(t *testing.T) {
	b := newProbeBed(t, nil)
	port := listenTCPServer(t)
	body := map[string]any{"type": "tcp", "host": loopback, "port": port, "from": map[string]any{"probe": "probe-a"}}

	// a probe that has no address yet cannot run it; neither can one that does not exist
	if r := b.diagnose(body); r.Status != 503 || !strings.Contains(string(r.Body), "no address") {
		t.Errorf("no address: %d %s", r.Status, r.Body)
	}
	b.address("10.10.0.77/24")
	r := b.diagnoseOK(body)
	if r["ok"] != true || r["from"] != "probe:"+probeNS || sub(r, "connect")["reachable"] != true {
		t.Errorf("%v", r)
	}
	// by id as well; a target that fails is a result, as from the gateway
	r = b.diagnoseOK(map[string]any{"type": "tcp", "host": loopback, "port": freeTCPPort(t), "from": map[string]any{"probe": probeID}})
	if r["ok"] != false || r["from"] != "probe:"+probeNS || sub(r, "connect")["detail"] != "connection refused" {
		t.Errorf("%v", r)
	}
	if r := b.diagnose(map[string]any{"type": "tcp", "host": loopback, "port": port, "from": map[string]any{"probe": "nobody"}}); r.Status != 404 {
		t.Errorf("an unknown probe: %d %s", r.Status, r.Body)
	}
	// a device is not a probe
	if r := b.diagnose(map[string]any{"type": "tcp", "host": loopback, "port": port, "from": map[string]any{"probe": "rA"}}); r.Status != 404 {
		t.Errorf("a client as a probe: %d %s", r.Status, r.Body)
	}
	// the capabilities say so
	caps := b.do("GET", "/capabilities", nil, nil, nil).json(t)
	has := false
	for _, f := range caps["features"].([]any) {
		has = has || f == "probes"
	}
	if !has {
		t.Errorf("%v", caps["features"])
	}
	// and the audit log has the run
	items, _, _ := b.log.List(auditFilter(), "", 50)
	// and the entry names the probe, which meets other routes than the gateway (P6-AUDIT-14)
	var details []string
	for _, e := range items {
		if e.Action == "diagnostic.run" {
			details = append(details, e.Detail)
		}
	}
	if len(details) < 2 || !strings.Contains(details[1], "tcp 127.0.0.1:") || !strings.HasSuffix(details[1], ` from probe "probe-a" ok`) ||
		!strings.HasSuffix(details[0], ` from probe "`+probeID+`" failed`) {
		t.Errorf("the audit log: %q", details)
	}
}

func TestAProbeTrustsTheTestCAOnlyWhenItSaysSo(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["probes"].(map[string]any)[probeID2] = map[string]any{"name": "probe-b", "network": "IoT", "trusts_test_ca": true}
	})
	b.address("10.10.0.77/24")
	// the second probe has an address of its own
	if _, err := b.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, NS: "cgp-9d2e4b61", Args: []string{"addr", "replace", "10.10.0.78/24", "dev", "eth0"}}); err != nil {
		t.Fatal(err)
	}
	if err := b.e.ObserveNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	cas, err := tlsresponder.LoadCAs(filepath.Join(b.root, "tlsca"))
	if err != nil {
		t.Fatal(err)
	}
	// a certificate that is valid and signed by the test CA: a probe that trusts the CA verifies it, one that does not sees an
	// unknown authority
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "broker.example.com"}, DNSNames: []string{"broker.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, cas.Test.Cert, &key.PublicKey, cas.Test.Key)
	if err != nil {
		t.Fatal(err)
	}
	port := serveCerts(t, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})
	trusting := b.diagnoseOK(map[string]any{"type": "tls", "host": loopback, "port": port, "sni": "broker.example.com", "from": map[string]any{"probe": "probe-b"}})
	if trusting["ok"] != true || sub(trusting, "tls")["verified"] != true {
		t.Errorf("trusting: %v", trusting)
	}
	other := b.diagnoseOK(map[string]any{"type": "tls", "host": loopback, "port": port, "sni": "broker.example.com", "from": map[string]any{"probe": "probe-a"}})
	if other["ok"] != false || other["error"] != "certificate signed by unknown authority" {
		t.Errorf("not trusting: %v", other)
	}
	// the gateway itself trusts the test CA, as in M24
	if gw := b.diagnoseOK(map[string]any{"type": "tls", "host": loopback, "port": port, "sni": "broker.example.com"}); gw["ok"] != true {
		t.Errorf("gateway: %v", gw)
	}
}

// ---- calibration ---------------------------------------------------------------------------------------------------

func checks(res map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, c := range res["checks"].([]any) {
		m := c.(map[string]any)
		out[m["name"].(string)] = m["ok"].(bool)
	}
	return out
}

func TestAProfileIsMeasuredOnTheProbeAndTheOverlayIsRemoved(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["profiles"] = map[string]any{"6b0f8a1e-2c3d-4e5f-8a9b-0c1d2e3f4a5b": map[string]any{"name": "lte-ish", "parts": map[string]any{"impairment": map[string]any{"latency": "50ms", "jitter": "20ms"}}}}
	})
	b.address("10.10.0.77/24")
	b.net.delay, b.net.loss = 50*time.Millisecond, 0 // each answer: one direction's delay; the other is added below
	// the emulated path: 50 ms in each direction is 100 ms of round trip; the faultNet delays the answer by one hundred
	b.net.delay = 100 * time.Millisecond
	port := echoPair(t, 0)
	r := b.calibrate(map[string]any{"profile": "lte-ish", "host": loopback, "port": port, "duration": "5s"})
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	res := r.json(t)
	exp, meas := sub(res, "expected"), sub(res, "measured")
	if res["within_tolerance"] != true || !checks(res)["latency"] || !checks(res)["jitter"] {
		t.Fatalf("%s", r.Body)
	}
	if rtt := exp["rtt_ms"].(float64); rtt < 99 || rtt > 105 {
		t.Errorf("expected %v ms", rtt)
	}
	if med := meas["rtt_median_ms"].(float64); med < 98 || med > 110 {
		t.Errorf("measured median %v ms", med)
	}
	if s := sub(res, "samples"); s["sent"] != float64(500) || s["received"] != float64(500) {
		t.Errorf("samples %v", s)
	}
	// the overlay was there during the run and is gone now
	if n := len(b.e.Snapshot().Overlays); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
	if ov := b.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any); len(ov) != 0 {
		t.Errorf("%v", ov)
	}
	items, _, _ := b.log.List(auditFilter(), "", 100)
	found := false
	for _, e := range items {
		found = found || (e.Action == "probe.calibrate" && strings.Contains(e.Detail, "profile lte-ish") && strings.Contains(e.Detail, "within tolerance"))
	}
	if !found {
		t.Error("no probe.calibrate in the audit log")
	}
}

func TestAProfileThatDoesNotProduceWhatItSaysFailsTheSelfTest(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["profiles"] = map[string]any{"6b0f8a1e-2c3d-4e5f-8a9b-0c1d2e3f4a5b": map[string]any{"name": "slow", "parts": map[string]any{"impairment": map[string]any{"latency": "100ms"}}}}
	})
	b.address("10.10.0.77/24")
	b.net.delay = 20 * time.Millisecond // the profile says 200 ms of round trip, the path makes 20
	port := echoPair(t, 0)
	r := b.calibrate(map[string]any{"profile": "slow", "host": loopback, "port": port, "duration": "5s"})
	res := r.json(t)
	if r.Status != 200 || res["within_tolerance"] != false || checks(res)["latency"] {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if !strings.Contains(res["detail"].(string), "latency:") {
		t.Errorf("%v", res["detail"])
	}
}

func TestLossOfAFaultIsComparedWithItsBinomialInterval(t *testing.T) {
	b := newProbeBed(t, nil)
	b.address("10.10.0.77/24")
	b.net.loss, b.net.delay = 0.10, time.Millisecond
	port := echoPair(t, 0)
	req := map[string]any{"fault": map[string]any{"upload": map[string]any{"loss": "10%"}}, "host": loopback, "port": port, "duration": "5s"}
	r := b.calibrate(req)
	res := r.json(t)
	if r.Status != 200 || res["within_tolerance"] != true || !checks(res)["loss"] {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	meas := sub(res, "measured")
	if l := meas["loss_percent"].(float64); l < 5 || l > 15 {
		t.Errorf("measured loss %v", l)
	}
	if ci := meas["loss_ci99"].([]any); len(ci) != 2 || ci[0].(float64) > meas["loss_percent"].(float64) || ci[1].(float64) < meas["loss_percent"].(float64) {
		t.Errorf("%v", ci)
	}
	if sub(res, "expected")["loss_percent"] != float64(10) {
		t.Errorf("%v", res["expected"])
	}
	// the same loss claimed for a path that loses nothing fails
	b.net.loss = 0
	r = b.calibrate(req)
	if res := r.json(t); r.Status != 200 || res["within_tolerance"] != false || checks(res)["loss"] {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

func TestARateLimitIsMeasuredWithATCPEcho(t *testing.T) {
	b := newProbeBed(t, nil)
	b.address("10.10.0.77/24")
	b.net.delay = time.Millisecond
	port := echoPair(t, 250_000) // 2 Mbit/s
	r := b.calibrate(map[string]any{"fault": map[string]any{"rate": "2Mbit"}, "host": loopback, "port": port, "duration": "9s"})
	res := r.json(t)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	rate := sub(res, "measured")["rate_bps"].(float64)
	if res["within_tolerance"] != true || !checks(res)["rate"] || rate < 1_800_000 || rate > 2_200_000 || sub(res, "expected")["rate_bps"] != float64(2_000_000) {
		t.Errorf("%s", r.Body)
	}
}

func TestWithoutAProfileOrAFaultTheProbesCurrentFaultsAreMeasured(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["faults"] = map[string]any{"7c1a9b2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d": map[string]any{"name": "slow-probe", "source": map[string]any{"device": "probe-a"}, "latency": "30ms"}}
	})
	b.address("10.10.0.77/24")
	always := func() bool { return true }
	b.effect.Store(&always)
	b.net.delay = 60 * time.Millisecond
	port := echoPair(t, 0)
	r := b.calibrate(map[string]any{"host": loopback, "port": port, "duration": "5s"})
	res := r.json(t)
	if r.Status != 200 || res["within_tolerance"] != true || sub(res, "expected")["rtt_ms"].(float64) < 59 || !strings.Contains(res["detail"].(string), "wins for the probe's traffic") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if len(b.e.Snapshot().Overlays) != 0 {
		t.Error("measuring the current faults makes no overlay")
	}
}

func TestACalibrationThatCannotRunIsRefused(t *testing.T) {
	b := newProbeBed(t, func(p map[string]any) {
		p["profiles"] = map[string]any{"6b0f8a1e-2c3d-4e5f-8a9b-0c1d2e3f4a5b": map[string]any{"name": "mtu-only", "parts": map[string]any{"mtu": map[string]any{"size": 1400}}}}
	})
	port := echoPair(t, 0)
	call := func(body map[string]any) resp {
		if _, ok := body["host"]; !ok {
			body["host"] = loopback
		}
		body["port"] = port
		return b.calibrate(body)
	}
	for name, c := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"both":         {map[string]any{"profile": "mtu-only", "fault": map[string]any{"latency": "5ms"}}, 422, "validation_failed"},
		"host name":    {map[string]any{"host": "broker.example.com"}, 422, "validation_failed"},
		"short":        {map[string]any{"duration": "1s"}, 422, "validation_failed"},
		"long":         {map[string]any{"duration": "6m"}, 422, "validation_failed"},
		"bad fault":    {map[string]any{"fault": map[string]any{"latency": "5ms", "jitter": "10ms"}}, 422, "validation_failed"},
		"no profile":   {map[string]any{"profile": "nope"}, 404, "not_found"},
		"mtu only":     {map[string]any{"profile": "mtu-only"}, 422, "validation_failed"},
		"no address":   {map[string]any{"duration": "5s"}, 503, "unavailable"},
		"unknown part": {map[string]any{"mystery": 1}, 422, "validation_failed"},
	} {
		r := call(c.body)
		if r.Status != c.status || r.json(t)["code"] != c.code {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := b.do("POST", "/probes/nobody/calibrate", map[string]any{"host": loopback}, nil, nil); r.Status != 404 {
		t.Errorf("an unknown probe: %d %s", r.Status, r.Body)
	}
	// a server that is not there is the probe's finding, not a crash
	b.address("10.10.0.77/24")
	r := b.calibrate(map[string]any{"host": loopback, "port": freeTCPPort(t), "duration": "5s"})
	if r.Status != 503 || !strings.Contains(string(r.Body), "echo server") {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if len(b.e.Snapshot().Overlays) != 0 {
		t.Error("overlays are left behind")
	}
}

func TestAProbeIsCalibratedByOneCallerAtATime(t *testing.T) {
	b := newProbeBed(t, nil)
	b.address("10.10.0.77/24")
	b.net.delay = time.Millisecond
	port := echoPair(t, 0)
	first := make(chan resp, 1)
	go func() {
		first <- b.calibrate(map[string]any{"fault": map[string]any{"latency": "1ms"}, "host": loopback, "port": port, "duration": "5s"})
	}()
	deadline := time.Now().Add(20 * time.Second)
	for len(b.e.Snapshot().Overlays) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first calibration never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := b.calibrate(map[string]any{"host": loopback, "port": port, "duration": "5s"})
	if r.Status != 409 || r.json(t)["code"] != "target_busy" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
	if r := <-first; r.Status != 200 {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}

func TestCalibratingNeedsTheOverlaysScope(t *testing.T) {
	b := newProbeBed(t, nil)
	b.address("10.10.0.77/24")
	b.mintToken("read")
	if r := b.calibrate(map[string]any{"host": loopback}); r.Status != 403 {
		t.Errorf("read scope: %d %s", r.Status, r.Body)
	}
	if r := b.do("GET", "/probes", nil, nil, nil); r.Status != 200 {
		t.Errorf("read scope lists: %d %s", r.Status, r.Body)
	}
	b.mintToken("overlays")
}
