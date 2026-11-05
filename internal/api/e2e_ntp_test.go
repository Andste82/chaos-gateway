//go:build testbed

package api_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/ntpserver"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M34 on the real kernel (plan §2.18, decision D36): the NTP server runs in the service namespace, registered with the API on the
// gateway's end of the link, and a client of a test network asks the gateway address, or another server, for the time. The
// cases of the milestone are the tests of the list M34_LEVEL1 (this file, e2e_ntpfaults_test.go and, for Kea,
// internal/engine/integration_ntp_test.go). The clients are in e2e_ntpclients_test.go.

// syncSwitch is the synchronization state of the host clock that the testbed says: the machines that run the tests are not
// synchronized (CI containers and VMs report STA_UNSYNC), so the server and the API are told that the clock is good unless a
// test says otherwise.
type syncSwitch struct {
	v atomic.Pointer[ntpserver.Sync]
}

func newSyncSwitch() *syncSwitch {
	s := &syncSwitch{}
	s.set(ntpserver.Sync(ntpserver.Synchronized))
	return s
}

func (s *syncSwitch) set(v ntpserver.Sync) { s.v.Store(&v) }

// State implements ntpserver.SyncState.
func (s *syncSwitch) State() ntpserver.Sync { return *s.v.Load() }

// ntpBedConf says what the bed brings besides the gateway and the NTP server.
type ntpBedConf struct {
	dns     bool // the DNS proxy and an upstream resolver that knows broker.test and other.test
	devices bool // the devices client-a and client-b, configured by their MAC addresses
	nts     bool // the NTS key-establishment listener (TCP 4460)
}

func withDNS(c *ntpBedConf)     { c.dns = true }
func withDevices(c *ntpBedConf) { c.devices = true }
func withNTSKE(c *ntpBedConf)   { c.nts = true }

// ntpBed is a gateway with a service namespace and the NTP server in it.
type ntpBed struct {
	g     *gw
	top   *testbed.Topology
	srv   *ntpserver.Server
	admin string
	conf  ntpBedConf
	sync  *syncSwitch

	ns      string
	base    string
	token   string
	scripts string

	stopMu sync.Mutex
	stop   func()
}

// ntpDevBID is the configured device client-b (client-a is espID).
const ntpDevBID = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"

const clientB = "client-b"

func newNTPBed(t *testing.T, mods ...func(*ntpBedConf)) *ntpBed {
	t.Helper()
	var conf ntpBedConf
	for _, m := range mods {
		m(&conf)
	}
	ns := fmt.Sprintf("svc%06x", mrand.Intn(1<<24))
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "delete", ns).Run() })
	sw := newSyncSwitch()
	g, top := newBedGW(t, func(o *options) {
		o.serviceNS = ns
		o.hostClock = sw
		if conf.dns {
			o.resolvers = []netip.Addr{netip.MustParseAddr(testbed.ServerAddr)}
		}
	})
	if conf.dns {
		top.Server.Start("dnsmasq", "--no-daemon", "--no-resolv", "--no-hosts", "--conf-file=/dev/null", "--user=root", "--bind-interfaces",
			"--listen-address="+testbed.ServerAddr, "--address=/example.test/203.0.113.77",
			"--address=/"+brokerHost+"/"+testbed.ServerAddr, "--address=/"+otherHost+"/"+testbed.ServerAddr2, "--cache-size=0")
		waitFor(t, 30*time.Second, "the upstream resolver answers the gateway", func() bool {
			out, err := top.GW.Run(context.Background(), "dig", "+short", "+time=1", "+tries=1", "@"+testbed.ServerAddr, "example.test")
			return err == nil && strings.TrimSpace(out) == "203.0.113.77"
		})
	}
	waitFor(t, 30*time.Second, "svc0 and the namespace", func() bool {
		_, err := top.GW.Run(context.Background(), "ip", "-br", "addr", "show", "dev", "svc0")
		return err == nil
	})
	port := 443
	if cfg := g.e.Snapshot().Config; cfg != nil && cfg.Management.UiPort != nil && *cfg.Management.UiPort > 0 {
		port = *cfg.Management.UiPort
	}
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

	b := &ntpBed{g: g, top: top, admin: g.token, conf: conf, sync: sw, ns: ns, base: fmt.Sprintf("http://169.254.100.1:%d", port), token: token, scripts: scriptDir(t)}
	t.Cleanup(b.stopServer)
	b.startServer(t)
	if conf.dns {
		startProxy(t, ns, token, b.base)
		waitFor(t, 30*time.Second, "A resolves example.test through the gateway", func() bool {
			out, err := digA(top, top.A, testbed.LAN0Gateway)
			return err == nil && out == "203.0.113.77"
		})
	}
	if conf.devices {
		id := g.mustPatch(map[string]any{"devices": map[string]any{
			espID:     map[string]any{"name": clientA, "network": iotID, "identifiers": map[string]any{"macs": []any{testbed.ClientAMAC}}},
			ntpDevBID: map[string]any{"name": clientB, "network": iotID, "identifiers": map[string]any{"macs": []any{testbed.ClientBMAC}}},
		}})
		if r := g.apply(id); r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		// the gateway knows a device once it has seen it
		for _, c := range []struct {
			ns   *testbed.Namespace
			addr string
		}{{top.A, testbed.ClientAAddr}, {top.B, testbed.ClientBAddr}} {
			waitFor(t, 30*time.Second, "the gateway sees "+c.addr, func() bool { return pingOK(c.ns, "", testbed.LAN0Gateway) })
		}
		g.observe()
	}
	return b
}

// startServer runs the NTP server as the container does, inside the service namespace and registered with the API on the
// gateway's end of the link (the token of the service, the address 169.254.100.1), and returns when it has a configuration.
func (b *ntpBed) startServer(t *testing.T) {
	t.Helper()
	c := &ntpserver.APIClient{Base: b.base, TokenFile: b.token, Dial: testbed.DialerIn(b.ns), Wait: 3 * time.Second}
	srv := ntpserver.New(ntpserver.Options{Sync: b.sync, Sink: c, Log: testLog(t), RateLimit: -1})
	ctx, cancel := context.WithCancel(context.Background())
	n := 3
	done := make(chan struct{}, 4)
	go func() { srv.Follow(ctx, c, testLog(t)); done <- struct{}{} }()
	go func() { srv.RunLog(ctx); done <- struct{}{} }()
	// the sockets are the production ones, with the kernel's receive time stamps
	go func() {
		_ = srv.Serve(ctx, func(p int) (net.PacketConn, error) {
			pc, err := testbed.ListenPacketIn(b.ns, "udp4", fmt.Sprintf(":%d", p))
			if err != nil {
				return nil, err
			}
			if err := ntpserver.EnableKernelTimestamps(pc); err != nil {
				_ = pc.Close()
				return nil, err
			}
			return pc, nil
		})
		done <- struct{}{}
	}()
	if b.conf.nts {
		ln, err := testbed.ListenIn(b.ns, "tcp", fmt.Sprintf(":%d", ntpserver.NTSKEPort))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		n++
		go func() { _ = srv.ServeNTSKE(ctx, ln); done <- struct{}{} }()
	}
	b.stopMu.Lock()
	b.srv = srv
	b.stop = func() {
		cancel()
		for i := 0; i < n; i++ {
			<-done
		}
		c.CloseIdleConnections()
	}
	b.stopMu.Unlock()
	waitFor(t, 30*time.Second, "the server has its configuration", func() bool { return srv.Config() != nil })
}

// stopServer ends the NTP server and closes its sockets; it is safe to call again.
func (b *ntpBed) stopServer() {
	b.stopMu.Lock()
	stop := b.stop
	b.stop = nil
	b.stopMu.Unlock()
	if stop != nil {
		stop()
	}
}

// ntpReply is a reply of the server and when it arrived.
type ntpReply struct {
	ntpserver.Packet
	at time.Time
}

// ask sends one client query from a namespace to server (ip:port) and returns the reply. The socket is connected, so a reply
// that does not come from the address the query was sent to is not delivered: the redirect has to leave the namespace with the
// original server's address and port.
func ask(from *testbed.Namespace, server string, timeout time.Duration) (ntpReply, error) {
	conn, err := testbed.DialerIn(from.Name)(context.Background(), "udp", server)
	if err != nil {
		return ntpReply{}, err
	}
	defer func() { _ = conn.Close() }()
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	q := ntpserver.Packet{Version: 4, Mode: ntpserver.ModeClient, Transmit: ntpserver.Timestamp(binary.BigEndian.Uint64(nonce[:]) | 1<<32)}
	if _, err := conn.Write(q.Marshal()); err != nil {
		return ntpReply{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return ntpReply{}, err
	}
	p, err := ntpserver.Parse(buf[:n])
	if err != nil {
		return ntpReply{}, err
	}
	if p.Origin != q.Transmit {
		return ntpReply{}, fmt.Errorf("the origin timestamp of the reply is not the query's transmit timestamp")
	}
	return ntpReply{Packet: p, at: time.Now()}, nil
}

func (r ntpReply) served() time.Time { return r.Transmit.Time(r.at) }

func (b *ntpBed) askGateway(t *testing.T) ntpReply {
	t.Helper()
	r, err := ask(b.top.A, testbed.LAN0Gateway+":123", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// overlay creates an overlay of the admin and returns its id.
func (b *ntpBed) overlay(t *testing.T, body string) string {
	t.Helper()
	b.g.token = b.admin
	r := b.g.createOverlay(body)
	if r.Status != 201 && r.Status != 200 {
		t.Fatalf("%s: %d %s", body, r.Status, r.Body)
	}
	return r.json(t)["id"].(string)
}

// remove deletes an overlay.
func (b *ntpBed) remove(t *testing.T, id string) {
	t.Helper()
	b.g.token = b.admin
	if r := b.g.do("DELETE", "/overlays/"+id, nil, nil, nil); r.Status != 204 && r.Status != 200 {
		t.Fatalf("delete %s: %d %s", id, r.Status, r.Body)
	}
}

// overlayJSON reads an overlay back.
func (b *ntpBed) overlayJSON(t *testing.T, id string) map[string]any {
	t.Helper()
	b.g.token = b.admin
	return b.g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)
}

// packets is the counter of the overlay: the queries it shaped.
func (b *ntpBed) packets(t *testing.T, id string) float64 {
	t.Helper()
	c, _ := b.overlayJSON(t, id)["counters"].(map[string]any)
	if c == nil {
		return 0
	}
	n, _ := c["packets"].(float64)
	return n
}

// since is the instant the overlay took effect (`updated_at`), from which `absolute` and a skew run.
func (b *ntpBed) since(t *testing.T, id string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, b.overlayJSON(t, id)["updated_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// logged waits until the API has an entry of the query log for which ok holds and returns it.
func (b *ntpBed) logged(t *testing.T, what string, ok func(e ntpLogged) bool) ntpLogged {
	t.Helper()
	var found ntpLogged
	waitFor(t, 30*time.Second, what, func() bool {
		b.g.token = b.admin
		for _, e := range b.g.srv.NTPLog() {
			l := ntpLogged{Client: e.Client, Port: e.Port}
			if e.Device != nil {
				l.Device = fmt.Sprint(*e.Device)
			}
			if e.Fault != nil {
				l.Fault = fmt.Sprint(*e.Fault)
			}
			if e.Server != nil {
				l.Server = *e.Server
			}
			if e.Answer != nil {
				l.Answer = string(*e.Answer)
			}
			if e.OffsetMs != nil {
				l.OffsetMs = *e.OffsetMs
				l.HasOffset = true
			}
			if e.Kind != nil {
				l.Kind = string(*e.Kind)
			}
			if ok(l) {
				found = l
				return true
			}
		}
		return false
	})
	return found
}

// ntpLogged is an entry of the query log the API keeps, with the fields the tests read.
type ntpLogged struct {
	Client, Device, Fault, Server, Answer, Kind string
	Port                                        int
	OffsetMs                                    int64
	HasOffset                                   bool
}

// M34: a client of the test network asks the gateway address and gets the host's time from the server in the service namespace;
// a time fault of the network moves it, the log of the API names the device and the fault, the overlay counts the queries, and
// the server stops serving the fault when the overlay goes.
func TestNTPThroughTheServiceNamespace(t *testing.T) {
	b := newNTPBed(t)
	g, top := b.g, b.top
	diagnose = func() {
		for _, c := range [][]string{{"nft", "list", "ruleset"}, {"conntrack", "-L"}, {"ip", "rule", "show"}} {
			out, err := exec.Command("ip", append([]string{"netns", "exec", top.GW.Name}, c...)...).CombinedOutput()
			t.Logf("---- gateway %s (%v)\n%s", strings.Join(c, " "), err, out)
		}
		out, _ := exec.Command("ip", "netns", "exec", top.GW.Name, "ss", "-uanp").CombinedOutput()
		t.Logf("---- sockets\n%s", out)
		t.Logf("server configuration: %+v", b.srv.Config())
	}
	t.Cleanup(func() { diagnose = nil })

	// the redirect and the reverse translation: the reply comes from the address the client asked
	var r ntpReply
	waitFor(t, 30*time.Second, "A gets an answer from the gateway address", func() bool {
		var err error
		r, err = ask(top.A, testbed.LAN0Gateway+":123", time.Second)
		return err == nil
	})
	if r.Mode != ntpserver.ModeServer || r.Leap != ntpserver.LeapNone || r.Stratum != 2 || r.Version != 4 {
		t.Errorf("the reply: %+v", r.Packet)
	}
	if got := r.served().Sub(r.at); got < -time.Second || got > time.Second {
		t.Errorf("the served time is %v off the host clock", got)
	}
	if ref := net.IP(r.RefID[:]).String(); ref != testbed.LAN0Gateway {
		t.Errorf("the reference id is %s, the gateway's address in the device's network", ref)
	}

	// nothing but the server listens on the NTP port in the gateway's own namespace
	if out := top.GW.Must("ss", "-H", "-lun"); strings.Contains(out, ":123 ") {
		t.Errorf("a listener on port 123 in the gateway's namespace:\n%s", out)
	}

	// the log reaches the API with the device, and nothing is shaped yet
	g.token = b.admin
	g.observe()
	waitFor(t, 20*time.Second, "the query is in the log with its device", func() bool {
		// the API names the device when the entry arrives, from what the gateway has observed by then: a query that came in
		// before it knew the device stays without one (seen on the 7.0 kernel), so the client asks again
		_, _ = ask(top.A, testbed.LAN0Gateway+":123", time.Second)
		g.observe()
		for _, e := range g.srv.NTPLog() {
			if e.Client == testbed.ClientAAddr && e.Device != nil && e.Fault == nil && e.Port == 123 {
				return true
			}
		}
		return false
	})

	// a time fault of the network moves the served time by the offset, for the device, and is counted
	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"offset":"+400d"}}`)
	want := 400 * 24 * time.Hour
	waitFor(t, 30*time.Second, "A gets the time of the fault", func() bool {
		r, err := ask(top.A, testbed.LAN0Gateway+":123", time.Second)
		if err != nil {
			return false
		}
		d := r.served().Sub(r.at)
		return d > want-time.Minute && d < want+time.Minute
	})
	waitFor(t, 20*time.Second, "the overlay counts the queries it shaped", func() bool {
		return b.packets(t, id) >= 1
	})

	// removed, the server serves the host's time again (the second jump of a time fault)
	b.remove(t, id)
	waitFor(t, 30*time.Second, "A gets the host's time again", func() bool {
		r, err := ask(top.A, testbed.LAN0Gateway+":123", time.Second)
		if err != nil {
			return false
		}
		d := r.served().Sub(r.at)
		return d > -time.Second && d < time.Second
	})
}

// M34 (D36): a server that is not the gateway is asked while the network does not redirect: the real server answers (there is none
// in the testbed, so nothing does); with a time fault for the network the query is redirected whether or not the network redirects
// hardcoded servers (Q3), and the server tells the destination from the port: the log of the API names the server asked.
func TestNTPForAHardcodedServerFollowsTheFaultAndNamesTheServerAsked(t *testing.T) {
	b := newNTPBed(t)
	g, top := b.g, b.top
	server := testbed.ServerAddr + ":123"
	if _, err := ask(top.A, server, time.Second); err == nil {
		t.Fatal("a server that nobody runs answered")
	}
	g.token = b.admin
	g.observe()
	// the fault names the server: the compiler redirects it to a port of its own, and the log knows the server from the port
	id := b.overlay(t, `{"target":{"network":"IoT"},"time":{"absolute":"1970-01-01T00:00:00Z","destination":{"cidr":"`+testbed.ServerAddr+`/32"}}}`)
	var r ntpReply
	waitFor(t, 30*time.Second, "A gets an answer from the server it asked", func() bool {
		var err error
		r, err = ask(top.A, server, time.Second)
		return err == nil
	})
	if got := r.served(); got.Year() != 1970 {
		t.Errorf("served %v, want a moment of 1970", got)
	}
	// the gateway address is not part of the selector: it is answered with the correct time
	if gw := b.askGateway(t); gw.served().Sub(gw.at) > time.Second || gw.served().Sub(gw.at) < -time.Second {
		t.Errorf("the gateway address is answered with %v", gw.served())
	}
	waitFor(t, 20*time.Second, "the log names the fault, the server and the port", func() bool {
		for _, e := range g.srv.NTPLog() {
			if e.Fault != nil && e.Fault.String() == id && e.Server != nil && *e.Server == testbed.ServerAddr+":123" && e.Port >= 12301 {
				return true
			}
		}
		return false
	})
}
