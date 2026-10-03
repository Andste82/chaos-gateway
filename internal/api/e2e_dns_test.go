//go:build testbed

package api_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/Andste82/chaos-gateway/internal/dnsproxy"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// nsExchanger asks the upstream resolver from inside the service namespace, as the proxy container does.
type nsExchanger struct{ ns string }

func (e nsExchanger) Exchange(ctx context.Context, m *dns.Msg, network, server string) (*dns.Msg, error) {
	var r *dns.Msg
	err := testbed.InNamedNS(e.ns, func() error {
		var err error
		r, _, err = (&dns.Client{Net: network, Timeout: 2 * time.Second}).ExchangeContext(ctx, m, server)
		return err
	})
	return r, err
}

// proxyIn starts the DNS proxy inside the service namespace, registered with the API on the gateway's
// end of the link; stop ends it and closes its sockets.
type proxyIn struct {
	stop   func()
	client *dnsproxy.APIClient
	srv    *dnsproxy.Server
}

func startProxy(t *testing.T, ns, token string) *proxyIn {
	t.Helper()
	pc, err := testbed.ListenPacketIn(ns, "udp", ":53")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := testbed.ListenIn(ns, "tcp", ":53")
	if err != nil {
		_ = pc.Close()
		t.Fatal(err)
	}
	c := &dnsproxy.APIClient{Base: "http://169.254.100.1:8443", TokenFile: token, Dial: testbed.DialerIn(ns), Wait: 3 * time.Second}
	srv := dnsproxy.New(dnsproxy.Options{Upstream: nsExchanger{ns: ns}, Sink: c})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 3)
	go func() { srv.Follow(ctx, c, testLog(t)); done <- struct{}{} }()
	go func() { srv.RunLog(ctx); done <- struct{}{} }()
	go func() { _ = srv.ServeOn(ctx, pc, ln); done <- struct{}{} }()
	p := &proxyIn{client: c, srv: srv}
	var once bool
	p.stop = func() {
		if once {
			return
		}
		once = true
		cancel()
		for i := 0; i < 3; i++ {
			<-done
		}
		c.CloseIdleConnections()
	}
	t.Cleanup(p.stop)
	return p
}

func digA(top *testbed.Topology, from *testbed.Namespace, server string, extra ...string) (string, error) {
	args := append([]string{"+short", "+time=2", "+tries=1"}, extra...)
	args = append(args, "@"+server, "example.test")
	out, err := from.Run(context.Background(), "dig", args...)
	return strings.TrimSpace(out), err
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func counterPackets(top *testbed.Topology, name string) int {
	out := top.GW.Must("nft", "list", "counter", "inet", "chaosgw", name)
	var n int
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "packets" && i+1 < len(fields) {
			_, _ = fmt.Sscan(fields[i+1], &n)
		}
	}
	return n
}

// M6b test: clients of a local test network and a WireGuard client resolve names through the DNS
// proxy in the service namespace over UDP and TCP, the log shows the queries, a proxy that restarts
// gets its configuration from the API again, and without the service namespace the redirected
// queries are dropped instead of leaving through the uplink.
func TestDNSThroughTheServiceNamespace(t *testing.T) {
	ns := fmt.Sprintf("svc%06x", rand.Intn(1<<24))
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "delete", ns).Run() })
	g, top := newBedGW(t, func(o *options) {
		o.serviceNS = ns
		o.resolvers = []netip.Addr{netip.MustParseAddr(testbed.ServerAddr)}
	})
	// the upstream resolver of the testbed: dnsmasq on the server answers example.test
	top.Server.Start("dnsmasq", "--no-daemon", "--no-resolv", "--no-hosts", "--conf-file=/dev/null", "--user=root", "--bind-interfaces",
		"--listen-address="+testbed.ServerAddr, "--address=/example.test/203.0.113.77")
	waitFor(t, 20*time.Second, "the upstream resolver answers the gateway", func() bool {
		out, err := top.GW.Run(context.Background(), "dig", "+short", "+time=1", "+tries=1", "@"+testbed.ServerAddr, "example.test")
		return err == nil && strings.TrimSpace(out) == "203.0.113.77"
	})

	// the setup applied revision 1 with the service namespace: the pair is up
	waitFor(t, 30*time.Second, "svc0 and the namespace", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "-br", "addr", "show", "dev", "svc0")
		return err == nil
	})
	if out := top.GW.Must("ip", "rule", "show"); !strings.Contains(out, "fwmark 0x100000/0x100000 lookup 102") {
		t.Errorf("no rule into table 102:\n%s", out)
	}
	if out := top.GW.Must("ip", "route", "show", "table", "102"); !strings.Contains(out, "prohibit default") || !strings.Contains(out, "default via 169.254.100.2 dev svc0") {
		t.Errorf("table 102:\n%s", out)
	}

	// the API listens on the gateway's end of the link, as in the container deployment
	l, err := testbed.ListenIn(top.GW.Name, "tcp", "169.254.100.1:8443")
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

	p := startProxy(t, ns, token)
	waitFor(t, 30*time.Second, "A resolves example.test through the gateway", func() bool {
		out, err := digA(top, top.A, testbed.LAN0Gateway)
		return err == nil && out == "203.0.113.77"
	})
	if out, err := digA(top, top.A, testbed.LAN0Gateway, "+tcp"); err != nil || out != "203.0.113.77" {
		t.Errorf("over TCP: %q %v", out, err)
	}
	// AAAA is answered with nothing: the networks are IPv4 only
	if out, err := top.A.Run(context.Background(), "dig", "+short", "+time=2", "+tries=1", "AAAA", "@"+testbed.LAN0Gateway, "example.test"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("AAAA: %q %v", out, err)
	}
	// the proxy sits in the service namespace: nothing listens on port 53 in the gateway's
	if out := top.GW.Must("ss", "-H", "-lun"); strings.Contains(out, ":53 ") {
		t.Errorf("a listener on port 53 in the gateway's namespace:\n%s", out)
	}
	if out, err := exec.Command("ip", "netns", "exec", ns, "ss", "-H", "-lun").CombinedOutput(); err != nil || !strings.Contains(string(out), ":53 ") {
		t.Errorf("no listener in the service namespace: %s %v", out, err)
	}

	// the log: both protocols, the client's own address
	waitFor(t, 20*time.Second, "the query log", func() bool {
		items := g.do("GET", "/dns/queries?name=example.test", nil, nil, nil).json(t)["items"].([]any)
		var udp, tcp bool
		for _, it := range items {
			e := it.(map[string]any)
			if e["client"] != testbed.ClientAAddr {
				continue
			}
			udp = udp || e["protocol"] == "udp"
			tcp = tcp || e["protocol"] == "tcp"
		}
		return udp && tcp
	})

	// a WireGuard client reaches the proxy through the tunnel with the gateway's tunnel address as DNS
	id := g.mustPatch(map[string]any{"networks": map[string]any{hubID: map[string]any{"clients": map[string]any{
		"6e7f8091-aabb-4c2d-8e3f-4a5b6c7d8e9f": map[string]any{"name": "rC", "address": "10.99.0.4", "keepalive": "1s", "key": map[string]any{"mode": "generated"}},
	}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.do("GET", "/networks/lab-hub/clients/rC/export", nil, nil, nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	conf := filepath.Join(t.TempDir(), "wgrc.conf")
	if err := os.WriteFile(conf, r.Body, 0o600); err != nil {
		t.Fatal(err)
	}
	top.RC.Must("wg-quick", "up", conf)
	waitFor(t, 30*time.Second, "the tunnel carries DNS (UDP)", func() bool {
		out, err := digA(top, top.RC, "10.99.0.1")
		return err == nil && out == "203.0.113.77"
	})
	if out, err := digA(top, top.RC, "10.99.0.1", "+tcp"); err != nil || out != "203.0.113.77" {
		t.Errorf("through the tunnel over TCP: %q %v", out, err)
	}

	// restarting only the proxy: the new one gets everything from the API
	p.stop()
	p = startProxy(t, ns, token)
	waitFor(t, 30*time.Second, "a restarted proxy resolves again", func() bool {
		out, err := digA(top, top.A, testbed.LAN0Gateway)
		return err == nil && out == "203.0.113.77"
	})

	// the holder of the namespace goes: the redirected queries are dropped, never sent to the uplink
	p.stop()
	if out, err := exec.Command("ip", "netns", "delete", ns).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	waitFor(t, 30*time.Second, "svc0 goes with the namespace", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "link", "show", "dev", "svc0")
		return err != nil
	})
	before := counterPackets(top, "forward_drop")
	if out, err := digA(top, top.A, testbed.LAN0Gateway); err == nil && out != "" {
		t.Errorf("answered without a service namespace: %q", out)
	}
	if after := counterPackets(top, "forward_drop"); after <= before {
		t.Errorf("the redirected queries were not dropped by the guard: %d, then %d\n%s", before, after, top.GW.Must("nft", "list", "chain", "inet", "chaosgw", "forward"))
	}
	// the next apply creates the namespace again, and a proxy restarted in it resolves again
	if err := g.e.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, "svc0 is back", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "-br", "addr", "show", "dev", "svc0")
		return err == nil
	})
	startProxy(t, ns, token)
	waitFor(t, 60*time.Second, "A resolves again after the namespace was created again", func() bool {
		out, err := digA(top, top.A, testbed.LAN0Gateway)
		return err == nil && out == "203.0.113.77"
	})
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(b)))
	return len(b), nil
}

func testLog(t *testing.T) *slog.Logger { return slog.New(slog.NewTextHandler(testWriter{t}, nil)) }
