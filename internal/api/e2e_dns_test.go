//go:build testbed

package api_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/clock"
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

func startProxy(t *testing.T, ns, token, base string) *proxyIn {
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
	c := &dnsproxy.APIClient{Base: base, TokenFile: token, Dial: testbed.DialerIn(ns), Wait: 3 * time.Second}
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

// diagnose is called when a wait times out: what the gateway looks like then.
var diagnose func()

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	if diagnose != nil {
		diagnose()
	}
	t.Fatalf("timeout: %s", what)
}

// withoutLines returns out with every line containing substr removed.
func withoutLines(out, substr string) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, substr) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
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
	// M6b-06: a stand-in for systemd-resolved's local stub, in the gateway's own namespace; the proxy
	// must not bind 127.0.0.53 and the compiled ruleset must not catch its traffic either
	top.GW.Start("dnsmasq", "--no-daemon", "--no-resolv", "--no-hosts", "--conf-file=/dev/null", "--user=root", "--bind-interfaces",
		"--listen-address=127.0.0.53", "--address=/stub.test/192.0.2.1")
	waitFor(t, 10*time.Second, "the resolved stand-in answers", func() bool {
		out, err := top.GW.Run(context.Background(), "dig", "+short", "+time=1", "+tries=1", "@127.0.0.53", "stub.test")
		return err == nil && strings.TrimSpace(out) == "192.0.2.1"
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
	// M6b-04: a redirected packet, not only the static table, actually resolves through svc0
	if out, err := top.GW.Run(context.Background(), "ip", "route", "get", "203.0.113.10", "from", testbed.ClientAAddr, "iif", "br-iot", "mark", "0x100000"); err != nil || !strings.Contains(out, "dev svc0") {
		t.Errorf("a marked packet does not resolve into svc0: %q %v", out, err)
	}

	// the API listens on the gateway's end of the link, as in the container deployment
	// on the port of the management interface: the gateway's input rules let the services reach that one
	port := 443 // the compiler's default
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

	p := startProxy(t, ns, token, base)
	diagnose = func() {
		show := func(title string, cmd *exec.Cmd) {
			out, err := cmd.CombinedOutput()
			t.Logf("---- %s (%v)\n%s", title, err, out)
		}
		show("gateway nft", exec.Command("ip", "netns", "exec", top.GW.Name, "nft", "list", "ruleset"))
		show("gateway rules", exec.Command("ip", "netns", "exec", top.GW.Name, "ip", "rule", "show"))
		show("gateway routes 100", exec.Command("ip", "netns", "exec", top.GW.Name, "ip", "route", "show", "table", "100"))
		show("gateway addr", exec.Command("ip", "netns", "exec", top.GW.Name, "ip", "-br", "addr"))
		show("gateway conntrack", exec.Command("ip", "netns", "exec", top.GW.Name, "conntrack", "-L"))
		show("gateway sockets", exec.Command("ip", "netns", "exec", top.GW.Name, "ss", "-tlnp"))
		show("service addr", exec.Command("ip", "netns", "exec", ns, "ip", "-br", "addr"))
		show("service routes", exec.Command("ip", "netns", "exec", ns, "ip", "route", "show"))
		show("service sockets", exec.Command("ip", "netns", "exec", ns, "ss", "-anp"))
		t.Logf("proxy configuration: %+v", p.srv.Config())
		t.Logf("proxy queries %d, upstream %d", p.srv.Queries(), p.srv.UpstreamQueries())
		out, _ := top.A.Run(context.Background(), "dig", "+time=2", "+tries=1", "@"+testbed.LAN0Gateway, "example.test")
		t.Logf("dig from A:\n%s", out)
	}
	t.Cleanup(func() { diagnose = nil })
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
	// the proxy sits in the service namespace: nothing but the resolved stand-in (M6b-06, on its own
	// address) listens on port 53 in the gateway's
	if out := top.GW.Must("ss", "-H", "-lun"); strings.Contains(withoutLines(out, "127.0.0.53:53"), ":53 ") {
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
	p = startProxy(t, ns, token, base)
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
	// M6b-04: without svc0, the same lookup hits the prohibit fallback instead of leaking out some
	// other way
	if _, err := top.GW.Run(context.Background(), "ip", "route", "get", "203.0.113.10", "from", testbed.ClientAAddr, "iif", "br-iot", "mark", "0x100000"); err == nil {
		t.Errorf("a marked packet resolved although the service namespace is gone")
	}
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
	startProxy(t, ns, token, base)
	waitFor(t, 60*time.Second, "A resolves again after the namespace was created again", func() bool {
		out, err := digA(top, top.A, testbed.LAN0Gateway)
		return err == nil && out == "203.0.113.77"
	})
	// M6b-06: resolved's stand-in was never disturbed by any of this
	if out, err := top.GW.Run(context.Background(), "dig", "+short", "+time=1", "+tries=1", "@127.0.0.53", "stub.test"); err != nil || strings.TrimSpace(out) != "192.0.2.1" {
		t.Errorf("the resolved stand-in stopped answering: %q %v", out, err)
	}
}

// startHolder starts a real process with its own, fresh network namespace (what the svcns container
// is in production) and returns it together with its PID once the namespace has actually taken effect
// (unshare's child does not have it from the first instruction).
func startHolder(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command("unshare", "--net", "sleep", "infinity")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	pid := cmd.Process.Pid
	self, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ns, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/net", pid)); err == nil && ns != self {
			return cmd, pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never got its own network namespace", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pinnedNamespaceAddrs returns the interface addresses of whatever network namespace ns names right
// now, pinned to that specific instance for every later call even if ns comes to name a different one
// afterwards (a real process that joins a namespace once, like the DNS proxy container, does not
// re-resolve the name either: internal/testbed.InNamedNS does, which is right for every other use of
// it in this file, but wrong for this one).
func pinnedNamespaceAddrs(t *testing.T, ns string) func() ([]netip.Addr, error) {
	t.Helper()
	var fd *os.File
	var err error
	for _, dir := range []string{"/run/netns/", "/var/run/netns/"} {
		if fd, err = os.Open(dir + ns); err == nil {
			break
		}
	}
	if fd == nil {
		t.Fatalf("network namespace %q not found: %v", ns, err)
	}
	t.Cleanup(func() { _ = fd.Close() })
	return func() ([]netip.Addr, error) {
		ch := make(chan struct {
			addrs []netip.Addr
			err   error
		}, 1)
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			orig, err := os.Open("/proc/thread-self/ns/net")
			if err != nil {
				ch <- struct {
					addrs []netip.Addr
					err   error
				}{nil, err}
				return
			}
			defer func() { _ = orig.Close() }()
			if err := unix.Setns(int(fd.Fd()), unix.CLONE_NEWNET); err != nil {
				ch <- struct {
					addrs []netip.Addr
					err   error
				}{nil, err}
				return
			}
			ifaceAddrs, aerr := net.InterfaceAddrs()
			_ = unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET)
			var out []netip.Addr
			for _, a := range ifaceAddrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok {
					continue
				}
				if ip, ok := netip.AddrFromSlice(ipNet.IP); ok {
					out = append(out, ip.Unmap())
				}
			}
			ch <- struct {
				addrs []netip.Addr
				err   error
			}{out, aerr}
		}()
		r := <-ch
		return r.addrs, r.err
	}
}

// M6b-03 test: the holder of the service namespace is a real process. When it is replaced, the engine
// notices on its own (WatchService) and re-attaches without the test calling Refresh by hand; the
// proxy that was stuck in the now-orphaned namespace exits on its own once the pair into it is gone
// (M6b-01), and a freshly started one resolves again.
func TestAHolderRestartIsHealedWithoutHelp(t *testing.T) {
	ns := fmt.Sprintf("svc%06x", rand.Intn(1<<24))
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "delete", ns).Run() })

	holder, holderPID := startHolder(t)
	var pid atomic.Int64
	pid.Store(int64(holderPID))

	g, top := newBedGW(t, func(o *options) {
		o.serviceNS = ns
		o.resolvers = []netip.Addr{netip.MustParseAddr(testbed.ServerAddr)}
		o.holderPID = func() int { return int(pid.Load()) }
	})
	top.Server.Start("dnsmasq", "--no-daemon", "--no-resolv", "--no-hosts", "--conf-file=/dev/null", "--user=root", "--bind-interfaces",
		"--listen-address="+testbed.ServerAddr, "--address=/example.test/203.0.113.77")
	waitFor(t, 20*time.Second, "the upstream resolver answers the gateway", func() bool {
		out, err := top.GW.Run(context.Background(), "dig", "+short", "+time=1", "+tries=1", "@"+testbed.ServerAddr, "example.test")
		return err == nil && strings.TrimSpace(out) == "203.0.113.77"
	})
	waitFor(t, 30*time.Second, "svc0 attached to the real holder", func() bool {
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

	p := startProxy(t, ns, token, base)
	waitFor(t, 30*time.Second, "A resolves through the real holder's namespace", func() bool {
		out, err := digA(top, top.A, testbed.LAN0Gateway)
		return err == nil && out == "203.0.113.77"
	})

	// from here the engine watches the service namespace on its own, as it does in production
	wctx, wcancel := context.WithCancel(context.Background())
	t.Cleanup(wcancel)
	g.e.WatchService(wctx, 200*time.Millisecond)

	// the old proxy watches its own (pinned) namespace exactly as chaosgw dns does (M6b-01); once the
	// service address is gone from it, it stops itself, same as the real process would exit
	oldAddrs := pinnedNamespaceAddrs(t, ns)
	oldStopped := make(chan struct{})
	go func() {
		_ = dnsproxy.WatchNamespace(wctx, clock.NewReal(), netip.MustParseAddr("169.254.100.2"), 300*time.Millisecond, 20*time.Second, oldAddrs)
		p.stop()
		close(oldStopped)
	}()

	// the holder restarts: a new process, a new namespace; nothing but ServiceHolderPID's answer
	// changes (exactly what the real svcns container's PID file gives the API)
	_, newPID := startHolder(t)
	pid.Store(int64(newPID))
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()

	select {
	case <-oldStopped:
	case <-time.After(30 * time.Second):
		t.Fatal("the old proxy never noticed its namespace was replaced")
	}

	// the executor has re-attached the name to the new holder without the test calling Refresh
	waitFor(t, 10*time.Second, "svc0 attached to the new holder", func() bool {
		out, err := top.GW.Run(context.Background(), "ip", "-br", "addr", "show", "dev", "svc0")
		return err == nil && strings.Contains(out, "169.254.100.1")
	})
	startProxy(t, ns, token, base)
	waitFor(t, 30*time.Second, "A resolves again once a proxy joins the new holder's namespace", func() bool {
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
