//go:build testbed

package api_test

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M20 on the real kernel (plan §5, M20 "Tests" and the acceptance carried over from Phase 2): the DNS faults seen with `dig` from
// a client of a test network, hostname selectors of faults and rules that act on the addresses the device resolved, the
// DNS-derived sets (a concatenated nftables set with timeouts, written by the executor on its own netlink connection beside the
// applies) and the hardcoded resolvers. The gateway is the one of TestDNSThroughTheServiceNamespace: the proxy runs in the
// service namespace, a dnsmasq on the server is its upstream.

const (
	brokerHost = "broker.test" // the server's first address
	otherHost  = "other.test"  // the server's second address
)

// faultBed is a gateway with its DNS proxy and the upstream resolver on the server.
type faultBed struct {
	g     *gw
	top   *testbed.Topology
	p     *proxyIn
	admin string
	svc   string
}

// newFaultBed starts the gateway with a service namespace, the upstream resolver (extra are dnsmasq options) and the proxy,
// and returns when A resolves a name through the gateway.
func newFaultBed(t *testing.T, extra ...string) *faultBed {
	t.Helper()
	ns := fmt.Sprintf("svc%06x", rand.Intn(1<<24))
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "delete", ns).Run() })
	g, top := newBedGW(t, func(o *options) {
		o.serviceNS = ns
		o.resolvers = []netip.Addr{netip.MustParseAddr(testbed.ServerAddr)}
	})
	args := []string{"--no-daemon", "--no-resolv", "--no-hosts", "--conf-file=/dev/null", "--user=root", "--bind-interfaces",
		"--listen-address=" + testbed.ServerAddr,
		"--address=/example.test/203.0.113.77",
		"--address=/" + brokerHost + "/" + testbed.ServerAddr,
		"--address=/" + otherHost + "/" + testbed.ServerAddr2,
		"--cache-size=0"}
	top.Server.Start("dnsmasq", append(args, extra...)...)
	waitFor(t, 30*time.Second, "the upstream resolver answers the gateway", func() bool {
		out, err := top.GW.Run(context.Background(), "dig", "+short", "+time=1", "+tries=1", "@"+testbed.ServerAddr, "example.test")
		return err == nil && strings.TrimSpace(out) == "203.0.113.77"
	})
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
	raw, _ := os.ReadFile(token)
	b := &faultBed{g: g, top: top, admin: g.token, svc: strings.TrimSpace(string(raw))}
	b.p = startProxy(t, ns, token, base)
	waitFor(t, 30*time.Second, "A resolves example.test through the gateway", func() bool {
		out, err := digA(top, top.A, testbed.LAN0Gateway)
		return err == nil && out == "203.0.113.77"
	})
	return b
}

// dig runs dig from A against the gateway's DNS and returns the whole answer.
func (b *faultBed) dig(name string, extra ...string) (string, error) {
	args := append([]string{"+time=2", "+tries=1"}, extra...)
	args = append(args, "@"+testbed.LAN0Gateway, name)
	return b.top.A.Run(context.Background(), "dig", args...)
}

// addrOf resolves a name from A through the gateway and returns its address ("" when it does not resolve).
func (b *faultBed) addrOf(name string) string {
	out, err := b.dig(name, "+short")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// overlay creates an overlay of the admin and returns its id.
func (b *faultBed) overlay(t *testing.T, body string) string {
	t.Helper()
	b.g.token = b.admin
	r := b.g.createOverlay(body)
	if r.Status != 201 && r.Status != 200 {
		t.Fatalf("%s: %d %s", body, r.Status, r.Body)
	}
	return r.json(t)["id"].(string)
}

func (b *faultBed) remove(t *testing.T, id string) {
	t.Helper()
	b.g.token = b.admin
	if r := b.g.do("DELETE", "/overlays/"+id, nil, nil, nil); r.Status != 204 && r.Status != 200 {
		t.Fatalf("delete %s: %d %s", id, r.Status, r.Body)
	}
}

// setElements lists the elements of the DNS-derived set of a selector in the kernel.
func (b *faultBed) setElements(pattern string) []string {
	for _, s := range b.g.e.Snapshot().DNSSets {
		if s.Pattern == pattern {
			out, err := b.top.GW.Run(context.Background(), "nft", "list", "set", "inet", "chaosgw", s.Name)
			if err != nil {
				return nil
			}
			re := regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+) \. (\d+\.\d+\.\d+\.\d+)`)
			var els []string
			for _, m := range re.FindAllStringSubmatch(out, -1) {
				els = append(els, m[1]+" . "+m[2])
			}
			sort.Strings(els)
			return els
		}
	}
	return nil
}

// waitHostnameSet waits until the DNS-derived set of a hostname fault is in the kernel and the proxy has the fault's host name, so that the next
// answer it gives for the name fills the set: an answer given before that has no set to write the address to, and the device that asked is not
// selected when it connects. The device asks again until the proxy has written the pair.
func (b *faultBed) waitHostnameSet(t *testing.T, host string) {
	t.Helper()
	waitFor(t, 20*time.Second, "the DNS-derived set exists in the kernel", func() bool {
		out, err := b.top.GW.Run(context.Background(), "nft", "list", "sets", "inet", "chaosgw")
		return err == nil && strings.Contains(out, "set dns_")
	})
	waitFor(t, 20*time.Second, "the proxy has written the address of "+host+" to the set", func() bool {
		return b.addrOf(host) == testbed.ServerAddr && len(b.setElements(host)) > 0
	})
}

func (b *faultBed) pingMedian(t *testing.T, dst string, n int) (time.Duration, float64) {
	t.Helper()
	res, err := testbed.Ping(context.Background(), b.top.A, dst, n, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("ping %s: %v", dst, err)
	}
	return res.Median(), res.Loss()
}

// M20 test: `dig` from a client shows each fault of the DNS family; a fault is an overlay and goes with it; a truncated answer
// is asked again over TCP; a short TTL is what the client gets; a timeout is no answer at all.
func TestDigFromAClientShowsEachDNSFaultOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	status := func(name string, extra ...string) string {
		out, err := b.dig(name, extra...)
		if err != nil {
			return "error: " + strings.TrimSpace(out)
		}
		m := regexp.MustCompile(`status: (\w+)`).FindStringSubmatch(out)
		if m == nil {
			return "no status: " + out
		}
		return m[1]
	}
	fault := func(body, name string, ok func() bool) {
		t.Helper()
		id := b.overlay(t, `{"target":{"network":"IoT"},"dns":`+body+`}`)
		waitFor(t, 20*time.Second, "the proxy applies the fault "+body+" for "+name, ok)
		b.remove(t, id)
		waitFor(t, 20*time.Second, "the proxy answers normally again after "+body, func() bool { return b.addrOf("example.test") == "203.0.113.77" })
	}
	fault(`{"action":"nxdomain","names":["example.test"]}`, "example.test", func() bool { return status("example.test") == "NXDOMAIN" })
	fault(`{"action":"servfail"}`, "example.test", func() bool { return status("example.test") == "SERVFAIL" })
	fault(`{"action":"timeout","names":["example.test"]}`, "example.test", func() bool {
		start := time.Now()
		out, err := b.dig("example.test")
		return err != nil && strings.Contains(out, "timed out") && time.Since(start) >= 1900*time.Millisecond
	})
	// the fault names a name: another one is answered
	id := b.overlay(t, `{"target":{"network":"IoT"},"dns":{"action":"nxdomain","names":["example.test"]}}`)
	waitFor(t, 20*time.Second, "nxdomain for example.test", func() bool { return status("example.test") == "NXDOMAIN" })
	if got := b.addrOf(brokerHost); got != testbed.ServerAddr {
		t.Errorf("a name the fault does not name resolves to %q, want %s", got, testbed.ServerAddr)
	}
	b.remove(t, id)

	// delay: the answer comes later than it would have
	id = b.overlay(t, `{"target":{"network":"IoT"},"dns":{"action":"delay","delay":"700ms","names":["example.test"]}}`)
	waitFor(t, 20*time.Second, "the delay", func() bool {
		start := time.Now()
		out, err := b.dig("example.test", "+short")
		return err == nil && strings.TrimSpace(out) == "203.0.113.77" && time.Since(start) >= 650*time.Millisecond
	})
	b.remove(t, id)

	// wrong answer: the broker's name points at a mock server
	id = b.overlay(t, `{"target":{"network":"IoT"},"dns":{"action":"wrong_answer","answers":["10.99.0.9"],"names":["`+brokerHost+`"]}}`)
	waitFor(t, 20*time.Second, "the wrong answer", func() bool { return b.addrOf(brokerHost) == "10.99.0.9" })
	if got := b.addrOf(otherHost); got != testbed.ServerAddr2 {
		t.Errorf("another name: %q", got)
	}
	b.remove(t, id)

	// truncation: over UDP the answer is empty with the TC bit; dig falls back to TCP by itself, and the log shows both
	id = b.overlay(t, `{"target":{"network":"IoT"},"dns":{"action":"truncate","names":["example.test"]}}`)
	waitFor(t, 20*time.Second, "the truncation", func() bool {
		out, err := b.dig("example.test", "+ignore")
		return err == nil && regexp.MustCompile(`flags:[^;]* tc[ ;]`).MatchString(out) && !strings.Contains(out, "203.0.113.77")
	})
	if out, err := b.dig("example.test", "+short"); err != nil || strings.TrimSpace(out) != "203.0.113.77" {
		t.Errorf("the fallback to TCP: %q %v", out, err)
	}
	waitFor(t, 20*time.Second, "the log has the UDP query that was truncated and the TCP one that was not", func() bool {
		items := b.g.do("GET", "/dns/queries?name=example.test", nil, nil, nil).json(t)["items"].([]any)
		var udpFault, tcpPlain bool
		for _, it := range items {
			e := it.(map[string]any)
			udpFault = udpFault || (e["protocol"] == "udp" && e["fault"] == id)
			tcpPlain = tcpPlain || (e["protocol"] == "tcp" && e["fault"] == nil)
		}
		return udpFault && tcpPlain
	})
	b.remove(t, id)

	// short TTL: the answer's TTL is the one of the fault, and the proxy's cache does not hand out the real one
	id = b.overlay(t, `{"target":{"network":"IoT"},"dns":{"action":"short_ttl","ttl":"1s","names":["example.test"]}}`)
	waitFor(t, 20*time.Second, "the short TTL", func() bool {
		out, err := b.dig("example.test", "+noall", "+answer")
		f := strings.Fields(strings.TrimSpace(out))
		return err == nil && len(f) >= 2 && f[1] == "1"
	})
	b.remove(t, id)
}

// M20 acceptance (P2-M11-01): a profile with a dns part, the built-in "DNS broken", activates and competes in the DNS family with the
// faults of the others (E5): SERVFAIL and 100 ms of latency at once.
func TestTheDNSBrokenProfileActivatesAndCombinesWithALatencyFaultOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	prof := b.overlay(t, `{"target":{"network":"IoT"},"profile":"dns-broken"}`)
	waitFor(t, 20*time.Second, "SERVFAIL from the proxy", func() bool {
		out, err := b.dig("example.test")
		return err == nil && strings.Contains(out, "SERVFAIL")
	})
	b.g.token = b.admin
	ov := b.g.do("GET", "/overlays/"+prof, nil, nil, nil).json(t)
	if ov["state"] != "effective" {
		t.Errorf("the activation: %v", ov)
	}
	// the other family: a latency fault on the device's network, in the kernel at the same time
	b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"100ms"}}`)
	if med, loss := b.pingMedian(t, testbed.ServerAddr, 5); loss != 0 || med < 190*time.Millisecond {
		t.Errorf("the latency fault: median %v, loss %.0f %%", med, loss)
	}
	if out, err := b.dig("example.test"); err != nil || !strings.Contains(out, "SERVFAIL") {
		t.Errorf("both families: %q %v", out, err)
	}
	ex := b.g.do("GET", "/explain?src="+testbed.ClientAAddr+"&dst=example.test", nil, nil, nil)
	if ex.Status != 200 || !strings.Contains(string(ex.Body), `"family":"dns"`) {
		t.Errorf("explain: %d %s", ex.Status, ex.Body)
	}
	b.remove(t, prof)
	waitFor(t, 20*time.Second, "the proxy answers again", func() bool { return b.addrOf("example.test") == "203.0.113.77" })
}

// M20 test: a hostname-selector fault affects only the traffic to the addresses the device resolved: before it asked, nothing is
// selected; after, traffic to the broker's address is delayed and traffic to the other server address is not.
func TestAHostnameFaultAffectsOnlyTrafficToTheResolvedAddressesOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"100ms","destination":{"hostname":"`+brokerHost+`"}}}`)
	waitFor(t, 20*time.Second, "the DNS-derived set exists in the kernel", func() bool {
		out, err := b.top.GW.Run(context.Background(), "nft", "list", "sets", "inet", "chaosgw")
		return err == nil && strings.Contains(out, "set dns_")
	})
	if med, _ := b.pingMedian(t, testbed.ServerAddr, 4); med > 60*time.Millisecond {
		t.Fatalf("traffic to the address was delayed (%v) before the device asked for the name", med)
	}
	// the device asks (again, until the proxy has the host name of the fault); A resolves the name to the server
	b.waitHostnameSet(t, brokerHost)
	// the set holds the pair before the first packet
	if els := b.setElements(brokerHost); len(els) != 1 || els[0] != testbed.ClientAAddr+" . "+testbed.ServerAddr {
		t.Fatalf("the kernel's set holds %v", els)
	}
	if med, loss := b.pingMedian(t, testbed.ServerAddr, 5); loss != 0 || med < 190*time.Millisecond {
		t.Errorf("the resolved address: median %v, loss %.0f %%", med, loss)
	}
	if med, _ := b.pingMedian(t, testbed.ServerAddr2, 5); med > 60*time.Millisecond {
		t.Errorf("the other address of the server is delayed: %v", med)
	}
	// another device does not get the delay for an address that only A resolved
	if res, err := testbed.Ping(context.Background(), b.top.B, testbed.ServerAddr, 4, 100*time.Millisecond); err != nil || res.Median() > 60*time.Millisecond {
		t.Errorf("a device that did not ask: %v %v", res.Median(), err)
	}
}

// M20 test: a long-lived connection keeps its hostname fault past a 1 s TTL: the element lives at least ten minutes, whatever the TTL
// of the answer, and the fault is still there when the TTL has long run out.
func TestALongLivedConnectionKeepsItsHostnameFaultPastAOneSecondTTLOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t, "--local-ttl=1")
	b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"100ms","destination":{"hostname":"`+brokerHost+`"}}}`)
	b.waitHostnameSet(t, brokerHost)
	out, err := b.dig(brokerHost, "+noall", "+answer")
	if f := strings.Fields(out); err != nil || len(f) < 2 || f[1] != "1" {
		t.Fatalf("the answer does not carry a TTL of 1 s: %q %v", out, err)
	}
	// a TCP connection that stays open, to an echo on the server
	l, err := testbed.ListenIn(b.top.Server.Name, "tcp", testbed.ServerAddr+":7007")
	if err != nil {
		t.Fatal(err)
	}
	// the connections the echo accepted are closed with the test: the client's FIN may not get through the delay before the bed is gone, and a
	// connection that nobody closes keeps its goroutine (and the namespace) alive past the test
	var mu sync.Mutex
	var accepted []net.Conn
	defer func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range accepted {
			_ = c.Close()
		}
	}()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, c)
			mu.Unlock()
			go func() {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}()
		}
	}()
	conn, err := dialIn(b.top.A, testbed.ServerAddr+":7007")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	rtt := func() time.Duration {
		start := time.Now()
		_, _ = conn.Write([]byte("x"))
		buf := make([]byte, 1)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("the connection broke: %v", err)
		}
		return time.Since(start)
	}
	if d := rtt(); d < 190*time.Millisecond {
		t.Fatalf("the connection is not delayed: %v", d)
	}
	time.Sleep(4 * time.Second) // four TTLs
	if d := rtt(); d < 190*time.Millisecond {
		t.Errorf("the connection lost its fault with the TTL: %v", d)
	}
	if els := b.setElements(brokerHost); len(els) != 1 {
		t.Errorf("the set holds %v after the TTL", els)
	}
	// the element was written with the lifetime of ten minutes
	out = b.top.GW.Must("nft", "list", "sets", "inet", "chaosgw")
	if !strings.Contains(out, "timeout 10m") && !strings.Contains(out, "expires 9m") {
		t.Errorf("the element does not carry the ten minutes:\n%s", out)
	}
}

// dialIn opens a TCP connection from a namespace.
func dialIn(ns *testbed.Namespace, addr string) (net.Conn, error) {
	var c net.Conn
	err := ns.Do(func() error {
		var err error
		c, err = net.DialTimeout("tcp", addr, 5*time.Second)
		return err
	})
	return c, err
}

// M20 test: the set survives ten overlay changes: faults of other networks written and replaced, the hostname fault itself replaced
// with other parameters, and a rule added and removed; the elements stay and the fault keeps acting.
func TestTheDNSDerivedSetSurvivesTenOverlayChangesOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	hid := b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"100ms","destination":{"hostname":"`+brokerHost+`"}}}`)
	b.waitHostnameSet(t, brokerHost)
	if got := b.addrOf(brokerHost); got != testbed.ServerAddr {
		t.Fatal(got)
	}
	want := []string{testbed.ClientAAddr + " . " + testbed.ServerAddr}
	if els := b.setElements(brokerHost); fmt.Sprint(els) != fmt.Sprint(want) {
		t.Fatalf("%v", els)
	}
	for i := 0; i < 10; i++ {
		switch i % 3 {
		case 0:
			b.overlay(t, `{"target":{"network":"lab-hub"},"fault":{"latency":"`+strconv.Itoa(5+i)+`ms"}}`)
		case 1:
			// the same fault with other parameters: the same overlay, the same set
			b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"`+strconv.Itoa(100+i)+`ms","destination":{"hostname":"`+brokerHost+`"}}}`)
		default:
			rid := b.overlay(t, `{"target":{"network":"lab-hub"},"rule":{"action":"drop","protocol":"tcp","ports":[`+strconv.Itoa(9000+i)+`]}}`)
			b.remove(t, rid)
		}
		if els := b.setElements(brokerHost); fmt.Sprint(els) != fmt.Sprint(want) {
			t.Fatalf("after change %d the set holds %v", i, els)
		}
	}
	b.g.token = b.admin
	if st := b.g.do("GET", "/overlays/"+hid, nil, nil, nil).json(t)["state"]; st != "effective" {
		t.Errorf("the hostname fault: %v", st)
	}
	if med, loss := b.pingMedian(t, testbed.ServerAddr, 5); loss != 0 || med < 190*time.Millisecond {
		t.Errorf("the fault does not act after ten changes: %v, loss %.0f %%", med, loss)
	}
}

// M20 acceptance (P2-M9-02): a drop rule with a hostname destination blocks the addresses the device resolved, and only those.
func TestADropRuleWithAHostnameBlocksTheResolvedAddressesOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	rid := b.overlay(t, `{"target":{"network":"IoT"},"rule":{"action":"drop","destination":{"hostname":"`+brokerHost+`"}}}`)
	waitFor(t, 20*time.Second, "the rule is in the kernel", func() bool {
		out, err := b.top.GW.Run(context.Background(), "nft", "list", "chain", "inet", "chaosgw", "access_forward")
		return err == nil && strings.Contains(out, "@dns_")
	})
	// nothing resolved yet: nothing is blocked
	if !pingOK(b.top.A, "", testbed.ServerAddr) {
		t.Fatal("traffic to the address was dropped before the device asked for the name")
	}
	if got := b.addrOf(brokerHost); got != testbed.ServerAddr {
		t.Fatalf("%q", got)
	}
	if pingOK(b.top.A, "", testbed.ServerAddr) {
		t.Errorf("the resolved address is not blocked:\n%s", b.top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	if !pingOK(b.top.A, "", testbed.ServerAddr2) {
		t.Error("the other address of the server is blocked")
	}
	b.g.token = b.admin
	ov := b.g.do("GET", "/overlays/"+rid, nil, nil, nil).json(t)
	if c, ok := ov["counters"].(map[string]any); !ok || c["packets"].(float64) < 1 || ov["state"] != "effective" {
		t.Errorf("the rule counted nothing: %v", ov)
	}
	b.remove(t, rid)
	waitFor(t, 20*time.Second, "the address is reachable again", func() bool { return pingOK(b.top.A, "", testbed.ServerAddr) })
}

// M20 test: hardcoded DNS is redirected: a query to another resolver of the uplink is answered by the proxy (and its fault), and DNS over
// TLS is blocked, after the access rules.
func TestHardcodedDNSIsRedirectedAndDoTIsBlockedOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	direct := func() (string, error) {
		out, err := b.top.A.Run(context.Background(), "dig", "+short", "+time=2", "+tries=1", "@"+testbed.ServerAddr, "example.test")
		return strings.TrimSpace(out), err
	}
	// a fault of the proxy shows whether a query reached it: example.test answers 10.99.0.9 there, 203.0.113.77 at the resolver
	b.overlay(t, `{"target":{"network":"IoT"},"dns":{"action":"wrong_answer","answers":["10.99.0.9"],"names":["example.test"]}}`)
	waitFor(t, 20*time.Second, "the fault is in the proxy", func() bool { return b.addrOf("example.test") == "10.99.0.9" })
	if out, err := direct(); err != nil || out != "203.0.113.77" {
		t.Fatalf("a query to the resolver without the redirect: %q %v", out, err)
	}
	// a listener on the server for DNS over TLS: A reaches it before the network blocks it
	l, err := testbed.ListenIn(b.top.Server.Name, "tcp", testbed.ServerAddr+":853")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if c, err := dialIn(b.top.A, testbed.ServerAddr+":853"); err != nil {
		t.Fatalf("port 853 is not reachable before the block: %v", err)
	} else {
		_ = c.Close()
	}

	// the network redirects hardcoded resolvers and blocks DoT
	id := b.g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"dns": map[string]any{"redirect_hardcoded": true, "block_dot": true}}}})
	if r := b.g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	waitFor(t, 20*time.Second, "the query to the resolver is answered by the proxy", func() bool {
		out, err := direct()
		return err == nil && out == "10.99.0.9"
	})
	if out, err := b.top.A.Run(context.Background(), "dig", "+short", "+time=2", "+tries=1", "+tcp", "@"+testbed.ServerAddr, "example.test"); err != nil || strings.TrimSpace(out) != "10.99.0.9" {
		t.Errorf("over TCP: %q %v", out, err)
	}
	b.g.token = b.admin
	if items := b.g.do("GET", "/dns/queries?name=example.test", nil, nil, nil).json(t)["items"].([]any); len(items) == 0 {
		t.Error("the log does not show the redirected queries")
	}
	// a query to a resolver inside the network's own segment is not the gateway's: the gateway's own address is answered by the proxy as always
	if out, err := b.top.A.Run(context.Background(), "dig", "+short", "+time=2", "+tries=1", "@"+testbed.LAN0Gateway, "example.test"); err != nil || strings.TrimSpace(out) != "10.99.0.9" {
		t.Errorf("the gateway's own address: %q %v", out, err)
	}

	// DNS over TLS is dropped now, and counted
	if c, err := dialIn(b.top.A, testbed.ServerAddr+":853"); err == nil {
		_ = c.Close()
		t.Errorf("DNS over TLS is not blocked:\n%s", b.top.GW.Must("nft", "list", "chain", "inet", "chaosgw", "dot_block"))
	}
	var counted bool
	for _, n := range b.g.e.Snapshot().Bridges {
		if n.BlockDoT && counterPackets(b.top, "dot_"+shortNet(n.NetworkID)) > 0 {
			counted = true
		}
	}
	if !counted {
		t.Error("the DoT block counted no packet")
	}
	// an allow rule is an exception to the block
	b.overlay(t, `{"target":{"network":"IoT"},"rule":{"action":"allow","protocol":"tcp","ports":[853]}}`)
	waitFor(t, 20*time.Second, "port 853 is open for the exception", func() bool {
		c, err := dialIn(b.top.A, testbed.ServerAddr+":853")
		if err == nil {
			_ = c.Close()
		}
		return err == nil
	})
}

// shortNet is the first eight characters of a network's id without dashes, as the compiler names counters.
func shortNet(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

// M20 test (S16): the faults of a device apply to the queries it sends to the gateway's DNS: the upload and the download latency of the
// network are in the time a query takes, the proxy being in the service namespace.
func TestUploadAndDownloadLatencyApplyToTheQueriesOfADeviceOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	queryTime := func() time.Duration {
		out, err := b.dig("example.test", "+stats")
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		m := regexp.MustCompile(`Query time: (\d+) msec`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no query time:\n%s", out)
		}
		n, _ := strconv.Atoi(m[1])
		return time.Duration(n) * time.Millisecond
	}
	median := func() time.Duration {
		var ds []time.Duration
		for i := 0; i < 7; i++ {
			ds = append(ds, queryTime())
		}
		return testbed.Median(ds)
	}
	base := median()
	b.overlay(t, `{"target":{"network":"IoT"},"fault":{"upload":{"latency":"80ms"},"download":{"latency":"40ms"}}}`)
	got := median()
	if want := 120 * time.Millisecond; got-base < want-25*time.Millisecond || got-base > want+40*time.Millisecond {
		t.Errorf("a query took %v with the fault and %v without: want about %v more", got, base, want)
	}
}

// M20 test: set updates beside plans on the real kernel: while overlays are written (each is an apply), the proxy's reports go into the
// DNS-derived set over the executor's netlink connection, and nothing is lost on either side.
func TestSetUpdatesAreWrittenBesideAppliesOnTheRealKernel(t *testing.T) {
	b := newFaultBed(t)
	b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"5ms","destination":{"hostname":"*.bulk.test"}}}`)
	var set string
	for _, s := range b.g.e.Snapshot().DNSSets {
		set = s.ID
	}
	if set == "" {
		t.Fatal("no set")
	}
	const n = 150
	b.g.token = b.admin // the reports name their own token
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			r := b.g.createOverlay(`{"target":{"network":"lab-hub"},"fault":{"latency":"` + strconv.Itoa(1+i) + `ms"}}`)
			if r.Status != 201 && r.Status != 200 {
				t.Errorf("overlay %d: %d %s", i, r.Status, r.Body)
			}
		}
	}()
	errs := make(chan error, n)
	go func() {
		defer wg.Done()
		var inner sync.WaitGroup
		for i := 0; i < n; i++ {
			inner.Add(1)
			go func() {
				defer inner.Done()
				addr := fmt.Sprintf("198.51.100.%d", 1+i%250)
				client := fmt.Sprintf("10.10.0.%d", 100+i%100)
				body := []any{map[string]any{"set": set, "client": client, "name": "x.bulk.test", "addresses": []any{addr}, "ttl": 60}}
				if r := b.g.do("POST", "/internal/dns/resolutions", body, map[string]string{"Authorization": "Bearer " + b.svc}, nil); r.Status != 204 {
					errs <- fmt.Errorf("report %d: %d %s", i, r.Status, r.Body)
				}
			}()
		}
		inner.Wait()
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	// every distinct (client, address) pair of the reports is in the kernel
	want := map[string]bool{}
	for i := 0; i < n; i++ {
		want[fmt.Sprintf("10.10.0.%d . 198.51.100.%d", 100+i%100, 1+i%250)] = true
	}
	have := map[string]bool{}
	for _, e := range b.setElements("*.bulk.test") {
		have[e] = true
	}
	for e := range want {
		if !have[e] {
			t.Errorf("%s is not in the set", e)
		}
	}
}

// M20 test (plan §3.10): the added answer latency for the names of a hostname set stays small: the proxy reports before it answers, and the
// call returns when the kernel holds the elements. Names that nothing selects are the baseline. Every measured name of the set has an
// address of its own (P5-AUDIT-12): the proxy does not make a report again for the same client, set and addresses within a minute, so
// names with one address would measure the deduplicated path after the first; the set's elements after the run show that every answer
// made a report and a write.
func TestTheAddedAnswerLatencyForHostnameSetNamesMeetsThePlanOnTheRealKernel(t *testing.T) {
	const names = 40
	extra := []string{"--address=/plain.test/203.0.113.52"}
	for i := 0; i < names; i++ {
		extra = append(extra, fmt.Sprintf("--address=/h%d.set.test/198.18.0.%d", i, 1+i))
	}
	b := newFaultBed(t, extra...)
	b.overlay(t, `{"target":{"network":"IoT"},"fault":{"latency":"1ms","destination":{"hostname":"*.set.test"}}}`)
	waitFor(t, 20*time.Second, "the set is in the kernel", func() bool { return len(b.g.e.Snapshot().DNSSets) == 1 })
	// the first query of a name goes upstream, then the proxy's cache answers: distinct names measure the path that reports
	measure := func(what string, name func(i int) string) (time.Duration, int) {
		var ds []time.Duration
		for i := 0; i < names; i++ {
			out, err := b.dig(name(i), "+stats")
			if err != nil {
				continue
			}
			if m := regexp.MustCompile(`Query time: (\d+) msec`).FindStringSubmatch(out); m != nil {
				n, _ := strconv.Atoi(m[1])
				ds = append(ds, time.Duration(n)*time.Millisecond)
			}
		}
		if len(ds) < 20 {
			t.Fatalf("%d answers for %s", len(ds), what)
		}
		return testbed.Median(ds), len(ds)
	}
	// dnsmasq answers every name of an --address domain
	plain, _ := measure("plain.test", func(i int) string { return fmt.Sprintf("h%d-%d.plain.test", time.Now().UnixNano()%1000000, i) })
	inSet, answered := measure("set.test", func(i int) string { return fmt.Sprintf("h%d.set.test", i) })
	// every answer was reported and written: one element per name, of the client A
	var ofA int
	for _, e := range b.setElements("*.set.test") {
		if strings.HasPrefix(e, testbed.ClientAAddr+" . 198.18.0.") {
			ofA++
		}
	}
	if ofA < answered {
		t.Errorf("the set holds %d elements of A after %d answers: the measurement did not go through the report each time", ofA, answered)
	}
	added := inSet - plain
	// plan §3.10: 5 ms on x86, 20 ms on a Raspberry Pi; dig reports whole milliseconds and the testbed VM is noisy: three times the target
	limit := 15 * time.Millisecond
	t.Logf("median query time %v for names in a set (%d reports), %v for the others: %v added (the plan allows 5 ms)", inSet, ofA, plain, added)
	if added > limit {
		t.Errorf("the answers for names in a hostname set take %v longer, want at most %v", added, limit)
	}
}
