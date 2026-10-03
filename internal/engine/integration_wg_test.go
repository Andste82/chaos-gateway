//go:build testbed

package engine_test

import (
	"bytes"
	"context"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	zqr "github.com/makiuchi-d/gozxing/qrcode"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/secrets"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/testbed"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

const (
	tHub    = "4e2f9d1c-8b3a-4f6e-a1d7-2c9b8e7f6a54"
	tAdmin  = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"
	tLink   = "c3d4e5f6-a7b8-4c9d-8e0f-1a2b3c4d5e6f"
	tIoT    = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	tClient = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	tAdm    = "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9"
)

// lockedBuffer collects log output from several goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// wgGW is the testbed with remote machines, the real executor with access to a secrets store, and
// the engine on the real clock. Everything the product does for WireGuard happens through them.
type wgGW struct {
	t     *testing.T
	top   *testbed.Topology
	st    *store.Store
	sec   *secrets.Store
	e     *engine.Engine
	base  *model.Configuration
	logs  *lockedBuffer
	state string
	dir   string // where configuration files for wg-quick go
}

func newWGGW(t *testing.T) *wgGW {
	t.Helper()
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false), testbed.WithGatewayBridges(false), testbed.WithRemotes(true))
	logs := &lockedBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sec, err := secrets.Open(filepath.Join(t.TempDir(), "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := executor.New(executor.NewExecRunner(), executor.WithLogger(log), executor.WithKeys(func(id string) (string, string, error) {
		k, err := sec.WireGuard(id)
		return k.PrivateKey, k.PresharedKey, err
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	stateDir := t.TempDir()
	st, err := store.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	raw, err := os.ReadFile("testdata/testbed_wg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg, nerrs := domain.Normalize(cfg)
	if len(nerrs) > 0 {
		t.Fatal(nerrs)
	}
	e, err := engine.New(engine.Config{Store: st, Exec: apply.Local{E: ex}, Namespace: top.GW.Name, Clock: &clock.Real{}, Log: log, Secrets: sec})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return &wgGW{t: t, top: top, st: st, sec: sec, e: e, base: cfg, logs: logs, state: stateDir, dir: t.TempDir()}
}

func (g *wgGW) revision(mod func(*model.Configuration)) int64 {
	g.t.Helper()
	cfg := cloneCfg(g.t, g.base)
	if mod != nil {
		mod(cfg)
	}
	cfg, err := wireguard.Provision(cfg, g.sec)
	if err != nil {
		g.t.Fatal(err)
	}
	rev, err := g.st.Create(cfg, store.CreateOptions{IfMatch: g.st.ActiveID(), Now: time.Now(), By: model.Actor{Id: "admin", Type: "user"}})
	if err != nil {
		g.t.Fatalf("create: %v", err)
	}
	return rev.Id
}

func (g *wgGW) apply(mod func(*model.Configuration)) {
	g.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := g.e.Apply(ctx, g.revision(mod), engine.ApplyOptions{SkipConfirm: true}); err != nil {
		g.t.Fatalf("apply: %v", err)
	}
}

func (g *wgGW) export() wireguard.ExportInput {
	g.t.Helper()
	_, cfg, err := g.st.Active()
	if err != nil {
		g.t.Fatal(err)
	}
	return wireguard.ExportInput{Config: cfg, Secrets: g.sec, UplinkAddress: testbed.UplinkGateway}
}

// up brings a tunnel up in a remote machine from an exported configuration, with wg-quick.
func (g *wgGW) up(ns *testbed.Namespace, name, conf string) {
	g.t.Helper()
	path := filepath.Join(g.dir, name+".conf")
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		g.t.Fatal(err)
	}
	ns.Must("wg-quick", "up", path)
}

func (g *wgGW) client(netID, id string) wireguard.Export {
	g.t.Helper()
	e, err := wireguard.ClientConfig(g.export(), netID, id)
	if err != nil {
		g.t.Fatal(err)
	}
	return e
}

func (g *wgGW) wgShow(args ...string) string {
	return g.top.GW.Must("wg", append([]string{"show"}, args...)...)
}

func (g *wgGW) udpSeenAt(ns *testbed.Namespace, bind string, from *testbed.Namespace, src, dst string) string {
	g.t.Helper()
	l := ns.Start("python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("`+bind+`", 9100))
d, a = s.recvfrom(64)
print(a[0], flush=True)
`)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = from.Run(context.Background(), "python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("`+src+`", 0))
s.sendto(b"x", ("`+dst+`", 9100))
`)
		select {
		case <-l.Done():
			return strings.TrimSpace(l.Output())
		case <-time.After(300 * time.Millisecond):
		}
	}
	return ""
}

// waitHandshake waits until a peer of the gateway's interface has handshaken.
func (g *wgGW) waitHandshake(dev string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(g.wgShow(dev, "latest-handshakes"), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[1] != "0" {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func pingOK(from *testbed.Namespace, src, dst string) bool {
	args := []string{"-c", "2", "-W", "1", "-n"}
	if src != "" {
		args = append(args, "-I", src)
	}
	_, err := from.Run(context.Background(), "ping", append(args, dst)...)
	return err == nil
}

// M4b test: a device in a local test network reaches a host in a client network without NAT, and
// the reverse only if the access matrix allows it. The exported configuration brings the tunnel up
// in a fresh namespace, and the decoded QR code equals the file.
func TestLocalDeviceReachesAClientNetworkWithoutNATAndTheReverseNeedsTheMatrix(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	conf := g.client(tHub, tClient)
	if !conf.HasPrivateKey {
		t.Fatal("the client key is generated: the export has the private key")
	}
	// the QR code of the configuration decodes to the file
	pngBytes, err := wireguard.QRPNG(conf.Conf, 512)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	bmp, _ := gozxing.NewBinaryBitmapFromImage(img)
	if res, err := zqr.NewQRCodeReader().Decode(bmp, nil); err != nil || res.GetText() != conf.Conf {
		t.Fatalf("the QR code does not decode to the file: %v", err)
	}

	g.up(g.top.RC, "wgrA", conf.Conf)
	// the client reaches the hub address: the tunnel works
	if !pingOK(g.top.RC, "", "10.99.0.1") {
		t.Fatalf("the exported configuration did not bring up a working tunnel\n%s\n%s", g.wgShow(), g.top.RC.Must("wg", "show"))
	}
	// A -> the network behind the client: allowed by the matrix, routed without NAT
	if src := g.udpSeenAt(g.top.RC, testbed.ClientNetHost, g.top.A, testbed.ClientAAddr, testbed.ClientNetHost); src != testbed.ClientAAddr {
		t.Fatalf("the host in the client network saw %q, want A's own address %s (no NAT)", src, testbed.ClientAAddr)
	}
	if !pingOK(g.top.A, "", testbed.ClientNetHost) {
		t.Error("A cannot ping the client network")
	}
	// the reverse: nothing allows the client to reach IoT yet
	if pingOK(g.top.RC, testbed.ClientNetHost, testbed.ClientAAddr) {
		t.Fatal("the client network reached A although the access matrix does not allow it")
	}
	// the client's reachable list allows it
	g.apply(func(c *model.Configuration) {
		n := (*c.Networks)[tHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[tClient]
		iot := tIoT
		cl.Reachable = &[]model.MatrixEndpoint{{Network: &iot}}
		(*wg.Clients)[tClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[tHub] = n
	})
	if !pingOK(g.top.RC, testbed.ClientNetHost, testbed.ClientAAddr) {
		t.Errorf("the reachable list does not let the client network reach A\n%s", g.top.GW.Must("nft", "list", "chain", "inet", "chaosgw", "forward"))
	}
	if src := g.udpSeenAt(g.top.A, testbed.ClientAAddr, g.top.RC, testbed.ClientNetHost, testbed.ClientAAddr); src != testbed.ClientNetHost {
		t.Errorf("A saw %q, want the client network's own address (no NAT)", src)
	}
	// the interface has the MTU of the plan
	if out := g.top.GW.Must("ip", "-o", "link", "show", "dev", "wg-lab-hub"); !strings.Contains(out, "mtu 1420") {
		t.Errorf("%s", out)
	}
}

// M4b test: a link with static routes carries traffic between the gateway's test network and the
// remote site.
func TestALinkWithStaticRoutesCarriesTrafficToTheRemoteSite(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	remote, err := wireguard.LinkRemoteConfig(g.export(), tLink)
	if err != nil {
		t.Fatal(err)
	}
	g.up(g.top.Site, "wgsite", remote.Conf)
	// the remote side routes the gateway's networks through the link (Table = off leaves it to us)
	g.top.Site.Must("ip", "route", "add", "10.10.0.0/24", "via", "10.255.0.0", "dev", "wgsite")
	// the gateway initiates: it retries every few seconds, so the handshake follows the remote side
	// coming up after a moment
	if !g.waitHandshake("wg-site-b", 30*time.Second) {
		t.Fatalf("the link did not come up\n%s\n%s", g.wgShow(), g.top.Site.Must("wg", "show"))
	}
	if !pingOK(g.top.A, "", testbed.SiteNetHost) {
		t.Fatalf("A cannot reach the remote site's network\n%s\n%s", g.wgShow(), g.top.Site.Must("wg", "show"))
	}
	if src := g.udpSeenAt(g.top.Site, testbed.SiteNetHost, g.top.A, testbed.ClientAAddr, testbed.SiteNetHost); src != testbed.ClientAAddr {
		t.Errorf("the site saw %q: routed, not translated", src)
	}
	// the route in table 100 is ours
	if out := g.top.GW.Must("ip", "route", "show", "table", "100"); !strings.Contains(out, "10.60.0.0/24 dev wg-site-b") || !strings.Contains(out, "proto 201") {
		t.Errorf("%s", out)
	}
}

// M4b test: disabling a client stops its handshake and emits the event.
func TestDisablingAClientStopsItsHandshakeAndEmitsTheEvent(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	ch, cancel := g.e.Subscribe()
	defer cancel()
	if err := g.e.PollWireGuard(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	g.up(g.top.RC, "wgrA", g.client(tHub, tClient).Conf)
	if !pingOK(g.top.RC, "", "10.99.0.1") {
		t.Fatal("no tunnel")
	}
	ev, ok := waitEvent(ch, engine.EventPeerOnline, 60*time.Second)
	if !ok || ev.Data["peer"] != "rA" {
		t.Fatalf("no online event for rA: %+v %v", ev, ok)
	}
	if hs := g.wgShow("wg-lab-hub", "latest-handshakes"); !strings.Contains(hs, "\t") || strings.HasSuffix(strings.TrimSpace(hs), "\t0") {
		t.Fatalf("no handshake: %q", hs)
	}

	g.apply(func(c *model.Configuration) {
		n := (*c.Networks)[tHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[tClient]
		off := false
		cl.Enabled = &off
		(*wg.Clients)[tClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[tHub] = n
	})
	if _, ok := waitEvent(ch, engine.EventPeerOffline, 60*time.Second); !ok {
		t.Fatal("no offline event after the client was disabled")
	}
	if peers := g.wgShow("wg-lab-hub", "peers"); strings.TrimSpace(peers) != "" {
		t.Errorf("the disabled client is still a peer: %q", peers)
	}
	if pingOK(g.top.RC, "", "10.99.0.1") {
		t.Error("the tunnel of a disabled client still works")
	}
}

// M4b test: a test-role client cannot reach a listener on the UI/API port, a management-role client
// can (and SSH, too).
func TestRolesDecideWhoReachesTheControlPlane(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	ui := g.top.GW.Start("python3", "-c", `import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 443)); s.listen(5)
while True:
    c, a = s.accept(); print("connect", a[0], flush=True); c.close()
`)
	time.Sleep(500 * time.Millisecond)
	g.up(g.top.RC, "wgrA", g.client(tHub, tClient).Conf)
	g.up(g.top.Site, "wgadm", g.client(tAdmin, tAdm).Conf)
	if !pingOK(g.top.RC, "", "10.99.0.1") || !pingOK(g.top.Site, "", "10.98.0.1") {
		t.Fatalf("the tunnels are not up\n%s", g.wgShow())
	}
	connect := func(ns *testbed.Namespace, dst string, port string) bool {
		_, err := ns.Run(context.Background(), "python3", "-c", `import socket, sys
try:
    socket.create_connection(("`+dst+`", `+port+`), timeout=2)
except Exception:
    sys.exit(1)
`)
		return err == nil
	}
	if connect(g.top.RC, "10.99.0.1", "443") {
		t.Error("a test-role client reached the UI/API port")
	}
	if !connect(g.top.Site, "10.98.0.1", "443") {
		t.Errorf("a management-role client must reach the UI/API port\n%s", g.top.GW.Must("nft", "list", "chain", "inet", "chaosgw", "input"))
	}
	if strings.Contains(ui.Output(), "connect 10.99.0.2") {
		t.Errorf("the test client got through: %q", ui.Output())
	}
	// ICMP echo is answered for test-role clients, too
	if !pingOK(g.top.RC, "", "10.99.0.1") {
		t.Error("the gateway must answer ping from a test network")
	}
}

// The MSS is clamped on WireGuard interfaces: a TCP connection through the tunnel gets an MSS that
// fits the tunnel's MTU, although the local interface could carry 1460.
func TestTheMSSIsClampedOnTheTunnel(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	g.up(g.top.RC, "wgrA", g.client(tHub, tClient).Conf)
	if !pingOK(g.top.RC, testbed.ClientNetHost, testbed.ClientAAddr) && !pingOK(g.top.A, "", testbed.ClientNetHost) {
		t.Fatal("no connectivity")
	}
	l := g.top.A.Start("python3", "-c", `import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 9200)); s.listen(5)
c, a = s.accept()
print(c.getsockopt(socket.IPPROTO_TCP, socket.TCP_MAXSEG), flush=True)
`)
	time.Sleep(500 * time.Millisecond)
	// the remote client may reach IoT (apply the reachable list), then connects
	g.apply(func(c *model.Configuration) {
		n := (*c.Networks)[tHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[tClient]
		iot := tIoT
		cl.Reachable = &[]model.MatrixEndpoint{{Network: &iot}}
		(*wg.Clients)[tClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[tHub] = n
	})
	_, _ = g.top.RC.Run(context.Background(), "python3", "-c", `import socket
s = socket.socket(); s.bind(("`+testbed.ClientNetHost+`", 0)); s.settimeout(5)
s.connect(("`+testbed.ClientAAddr+`", 9200))
`)
	select {
	case <-l.Done():
	case <-time.After(20 * time.Second):
		t.Fatalf("no connection: %q", l.Output())
	}
	mss := strings.TrimSpace(l.Output())
	var n int
	for _, c := range mss {
		n = n*10 + int(c-'0')
	}
	if n == 0 || n > 1380 {
		t.Errorf("the server side MSS is %q: it must fit the tunnel (<= 1380), not the LAN's 1460", mss)
	}
	// the clamp is what did it: its counter saw the SYNs (the client's own MTU already advertises
	// a small MSS, so the MSS alone would prove nothing)
	if out := g.top.GW.Must("nft", "list", "counter", "inet", "chaosgw", "mss_clamp"); strings.Contains(out, "packets 0 ") {
		t.Errorf("no SYN passed the clamp:\n%s", out)
	}
}

// M4b test: private keys never appear in revisions, exports of the configuration, the snapshot, the
// events or the logs of the executor and the engine; re-applying the configuration does not
// interrupt an established tunnel.
func TestPrivateKeysStayOutOfStoreSnapshotAndLogsAndAReapplyKeepsTheTunnel(t *testing.T) {
	g := newWGGW(t)
	g.apply(nil)
	ch, cancel := g.e.Subscribe()
	defer cancel()
	g.up(g.top.RC, "wgrA", g.client(tHub, tClient).Conf)
	// the client may reach IoT so that traffic runs both ways during the re-applies
	g.apply(func(c *model.Configuration) {
		n := (*c.Networks)[tHub]
		wg, _ := n.AsWireGuardNetwork()
		cl := (*wg.Clients)[tClient]
		iot := tIoT
		cl.Reachable = &[]model.MatrixEndpoint{{Network: &iot}}
		(*wg.Clients)[tClient] = cl
		_ = n.FromWireGuardNetwork(wg)
		(*c.Networks)[tHub] = n
	})
	if !pingOK(g.top.A, "", testbed.ClientNetHost) {
		t.Fatal("no tunnel traffic")
	}

	// re-apply while a ping runs: not one packet may be lost, and the peer is never removed or reset
	handshakeBefore := g.wgShow("wg-lab-hub", "latest-handshakes")
	if strings.HasSuffix(strings.TrimSpace(handshakeBefore), "\t0") {
		t.Fatalf("no handshake yet: %q", handshakeBefore)
	}
	rxBefore := g.wgShow("wg-lab-hub", "transfer")
	pinger := g.top.A.Start("ping", "-n", "-i", "0.1", "-c", "60", testbed.ClientNetHost)
	for i := 0; i < 5; i++ {
		g.apply(nil)
		time.Sleep(200 * time.Millisecond)
	}
	select {
	case <-pinger.Done():
	case <-time.After(60 * time.Second):
		t.Fatal("the ping did not finish")
	}
	out := pinger.Output()
	if !strings.Contains(out, " 0% packet loss") {
		t.Errorf("a re-apply interrupted the tunnel:\n%s", out)
	}
	if after := g.wgShow("wg-lab-hub", "latest-handshakes"); after != handshakeBefore {
		t.Errorf("the peer's session changed over the re-applies: %q -> %q", handshakeBefore, after)
	}
	if after := g.wgShow("wg-lab-hub", "transfer"); len(after) < len(rxBefore)/2 {
		t.Errorf("the peer was reset: %q -> %q", rxBefore, after)
	}

	// no private or preshared key anywhere the product writes
	var secretsText []string
	ids, _ := g.sec.IDs()
	for _, id := range ids {
		k, _ := g.sec.WireGuard(id)
		secretsText = append(secretsText, k.PrivateKey, k.PresharedKey)
	}
	if len(secretsText) < 6 {
		t.Fatalf("keys %d", len(secretsText))
	}
	// an error path writes to the logs: a revision whose interface key has gone missing fails to apply
	failing := g.revision(func(c *model.Configuration) { c.Uplink.Gateway = &[]string{"203.0.113.20"}[0] })
	if err := g.sec.DeleteWireGuard(tAdmin); err != nil {
		t.Fatal(err)
	}
	ctx, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
	if _, err := g.e.Apply(ctx, failing, engine.ApplyOptions{SkipConfirm: true}); err == nil {
		t.Fatal("an apply without an interface key must fail")
	}
	cancel2()
	check := func(what, text string) {
		for _, s := range secretsText {
			if s != "" && strings.Contains(text, s) {
				t.Errorf("a key is in %s", what)
				return
			}
		}
	}
	check("the logs", g.logs.String())
	check("the snapshot", mustJSON(g.e.Snapshot()))
	var evs []engine.Event
	for {
		select {
		case ev := <-ch:
			evs = append(evs, ev)
			continue
		default:
		}
		break
	}
	check("the events", mustJSON(evs))
	_ = filepath.Walk(g.state, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			check(p, string(b))
		}
		return nil
	})
	// the preview and the nftables of the gateway do not contain them either
	check("nftables", g.top.GW.Must("nft", "list", "ruleset"))
	if !strings.Contains(g.logs.String(), "level=") {
		t.Error("the logger did not log anything: the check proves nothing")
	}
}
