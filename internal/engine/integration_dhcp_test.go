//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

const (
	labID   = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"
	devBID  = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	hookLog = "/tmp/chaosgw-kea-hook.log"
)

// dhcpBed is the testbed with the real executor, a real Kea in the gateway's namespace and the engine
// on the real clock: DHCP clients are busybox's udhcpc in the client namespaces.
type dhcpBed struct {
	t    *testing.T
	top  *testbed.Topology
	st   *store.Store
	e    *engine.Engine
	ex   *executor.Executor
	base *model.Configuration
	kc   *kea.Client
	dir  string
	// nft counts the nft documents the engine writes (nftRecorder)
	nft *nftRecorder
	// log is the engine's log
	log *lockedBuffer
}

// nftRecorder is the engine's executor that counts the nft documents it writes: element updates
// (elementOnly) and the others, the transactions of full applies. Watch is the executor's own.
type nftRecorder struct {
	apply.Local
	mu             sync.Mutex
	elements, full int
}

func (r *nftRecorder) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	r.mu.Lock()
	for _, op := range ops {
		switch o := op.(type) {
		case *executor.NftApply:
			if elementOnly(string(o.Ruleset)) {
				r.elements++
			} else {
				r.full++
			}
		case *executor.NftAddMapElements, *executor.NftDelMapElements, *executor.NftAddElements, *executor.NftDelElements:
			r.elements++
		}
	}
	r.mu.Unlock()
	return r.Local.Do(ctx, ops...)
}

// take returns the counts since the last call and starts new ones.
func (r *nftRecorder) take() (elements, full int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	elements, full = r.elements, r.full
	r.elements, r.full = 0, 0
	return
}

func newDHCPBed(t *testing.T) *dhcpBed {
	t.Helper()
	for _, tool := range []string{"kea-dhcp4", "busybox", "conntrack"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false), testbed.WithGatewayBridges(false))
	ex, err := executor.New(executor.NewExecRunner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	raw, err := os.ReadFile("testdata/testbed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}

	// Kea restricts the directories of its files: the environment moves them to a private directory
	dir, err := os.MkdirTemp("", "cgkea")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	sock, leases := filepath.Join(dir, "ctrl.sock"), filepath.Join(dir, "leases.csv")
	// the hook script of the product is `chaosgw kea-hook`; here it only records the calls
	script := "#!/bin/sh\necho \"$1 $KEA_LEASE4_ADDRESS $KEA_LEASE4_HWADDR $KEA_SUBNET_ID $LEASES4_AT0_ADDRESS $LEASES4_AT0_HWADDR\" >> " + hookLog + "\nenv | grep -E '^(KEA_|LEASE)' | sort >> " + hookLog + "\n"
	if err := os.MkdirAll(filepath.Dir(kea.HookScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kea.HookScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(kea.HookScript) })
	_ = os.Remove(hookLog)
	base := kea.Config{Socket: sock, Leases: leases, Script: kea.HookScript}
	text, err := base.Render()
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "kea.json")
	if err := os.WriteFile(conf, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	p := top.GW.Start("env", "KEA_CONTROL_SOCKET_DIR="+dir, "KEA_DHCP_DATA_DIR="+dir, "KEA_PIDFILE_DIR="+dir, "KEA_LOCKFILE_DIR="+dir, "KEA_LOG_FILE_DIR="+dir, "kea-dhcp4", "-c", conf)
	kc := &kea.Client{Socket: sock, Timeout: 5 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := kc.Hash(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("kea does not answer:\n%s", p.Output())
		}
		time.Sleep(200 * time.Millisecond)
	}

	// the engine's own log, shown when the test fails: a fallback from an incremental identity update
	// to a full apply is only visible there
	engineLog := &lockedBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("engine log:\n%s", engineLog.String())
		}
	})
	rec := &nftRecorder{Local: apply.Local{E: ex}}
	e, err := engine.New(engine.Config{Store: st, Exec: rec, Namespace: top.GW.Name, Clock: &clock.Real{},
		Log:  slog.New(slog.NewTextHandler(engineLog, &slog.HandlerOptions{Level: slog.LevelDebug})),
		DHCP: &engine.KeaDHCP{Client: kc, Base: base}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	b := &dhcpBed{t: t, top: top, st: st, e: e, ex: ex, base: cfg, kc: kc, dir: dir, nft: rec, log: engineLog}
	return b
}

func (b *dhcpBed) revision(mod func(*model.Configuration)) int64 {
	b.t.Helper()
	cfg := cloneCfg(b.t, b.base)
	if mod != nil {
		mod(cfg)
	}
	rev, err := b.st.Create(cfg, store.CreateOptions{IfMatch: b.st.ActiveID(), Now: time.Now(), By: model.Actor{Id: "admin", Type: "user"}})
	if err != nil {
		b.t.Fatalf("create: %v", err)
	}
	return rev.Id
}

func (b *dhcpBed) apply(mod func(*model.Configuration)) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := b.e.Apply(ctx, b.revision(mod), engine.ApplyOptions{SkipConfirm: true}); err != nil {
		b.t.Fatalf("apply: %v", err)
	}
	if e := b.e.Snapshot().DHCPError; e != "" {
		b.t.Fatalf("Kea does not take the configuration: %s", e)
	}
}

// follow starts the observation: the poller every 200 ms and the neighbor watcher.
func (b *dhcpBed) follow() {
	b.t.Helper()
	if err := b.e.PollObserved(context.Background(), 200*time.Millisecond); err != nil {
		b.t.Fatal(err)
	}
	if err := b.e.FollowNeighbors(context.Background(), 50*time.Millisecond); err != nil {
		b.t.Fatal(err)
	}
}

// generationMarker reads the comment of the kernel's one "generation" rule (plan §2.14): a full
// apply rewrites it to the newly compiled target's hash; an identity-only element update does not.
func (b *dhcpBed) generationMarker() string {
	b.t.Helper()
	st, err := apply.ReadState(context.Background(), apply.Local{E: b.ex}, b.top.GW.Name, apply.Want{})
	if err != nil {
		b.t.Fatal(err)
	}
	g := st.Nft.Rules(compiler.GenerationChain)
	if len(g) != 1 {
		b.t.Fatalf("generation chain has %d rules, want 1", len(g))
	}
	return g[0].Comment
}

func dhcpOn(c *model.Configuration) {
	setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("60s")})
}

func setDHCP(c *model.Configuration, id string, scope *model.DhcpScope) {
	n := (*c.Networks)[id]
	lan, _ := n.AsLanNetwork()
	lan.Dhcp = scope
	_ = n.FromLanNetwork(lan)
	(*c.Networks)[id] = n
}

// udhcpc starts a DHCP client in a namespace and returns the file its script logs to.
func (b *dhcpBed) udhcpc(ns *testbed.Namespace, tries int) (logFile string) {
	b.t.Helper()
	dir := b.t.TempDir()
	logFile = filepath.Join(dir, "client.log")
	script := filepath.Join(dir, "udhcpc.sh")
	// The line is logged after the address is set: waitBound returns on it, and a test that then routes via the gateway
	// needs the address on the interface (the emulated weekly run once lost that race: "Nexthop has invalid gateway").
	body := "#!/bin/sh\ncase \"$1\" in\n  bound|renew) ip addr flush dev $interface; ip addr add $ip/24 dev $interface ;;\n  deconfig) ip addr flush dev $interface ;;\nesac\necho \"$1 ip=$ip router=$router dns=$dns\" >> " + logFile + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		b.t.Fatal(err)
	}
	ns.Must("ip", "addr", "flush", "dev", "eth0")
	ns.Start("busybox", "udhcpc", "-f", "-i", "eth0", "-s", script, "-t", fmt.Sprint(tries), "-T", "2", "-A", "2")
	return logFile
}

func waitBound(t *testing.T, logFile string, d time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(logFile); err == nil {
			for _, l := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(l, "bound ip=") {
					return strings.Fields(strings.TrimPrefix(l, "bound ip="))[0], true
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", false
}

func (b *dhcpBed) waitDevice(mac string, d time.Duration, ok func(*engine.DeviceState) bool) *engine.DeviceState {
	b.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, dv := range b.e.Snapshot().Devices {
			for _, m := range dv.MACs {
				if strings.EqualFold(m, mac) && ok(&dv) {
					c := dv
					return &c
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	b.t.Fatalf("no device with MAC %s in the state that was waited for; devices: %+v", mac, b.e.Snapshot().Devices)
	return nil
}

// M6a test: a client gets a lease from Kea on the network's bridge, the device appears with its MAC
// and address, and Kea's hook reported the lease.
func TestAClientGetsALeaseAndAppearsAsADiscoveredDevice(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	b.follow()
	log := b.udhcpc(b.top.A, 8)
	ip, ok := waitBound(t, log, 30*time.Second)
	if !ok {
		t.Fatalf("the client got no lease\n%s", b.top.GW.Must("ip", "addr"))
	}
	addr := netip.MustParseAddr(ip)
	if !netip.MustParsePrefix("10.10.0.0/24").Contains(addr) || addr.As4()[3] < 128 || addr.As4()[3] == 255 {
		t.Errorf("%s is not in the default pool (the second half of 10.10.0.0/24)", ip)
	}
	d := b.waitDevice(testbed.ClientAMAC, 20*time.Second, func(d *engine.DeviceState) bool {
		return len(d.Addresses) > 0 && d.Lease != nil
	})
	if d.Origin != model.DeviceOriginDiscovered || d.Name != "dev-02-00-00-00-00-0b" || d.Addresses[0].String() != ip || d.Lease.Ip != ip || d.Network != tIoT {
		t.Errorf("%+v", d)
	}
	// the hook called our script for the lease
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, _ := os.ReadFile(hookLog); strings.Contains(string(raw), ip+" "+testbed.ClientAMAC) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	raw, _ := os.ReadFile(hookLog)
	t.Errorf("the run_script hook did not report the lease: %q", raw)
}

// M6a test: a device with a fixed address and a MAC gets exactly that address, and is a configured
// device, not a discovered one.
func TestAReservationIsHonored(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		fixed := "10.10.0.60"
		macs := []string{testbed.ClientBMAC}
		c.Devices = &map[string]model.Device{devBID: {Name: "esp32-b", FixedIp: &fixed, Network: ptrS(tIoT), Identifiers: &model.DeviceIdentifiers{Macs: &macs}}}
	})
	b.follow()
	ip, ok := waitBound(t, b.udhcpc(b.top.B, 8), 30*time.Second)
	if !ok || ip != "10.10.0.60" {
		t.Fatalf("the client got %q, want its reservation 10.10.0.60", ip)
	}
	// a device is online when it is seen on the network, not when it holds a lease
	b.top.B.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	d := b.waitDevice(testbed.ClientBMAC, 20*time.Second, func(d *engine.DeviceState) bool { return d.Lease != nil && d.Online })
	if d.ID != devBID || d.Origin != model.DeviceOriginConfigured || d.Name != "esp32-b" || !d.Online {
		t.Errorf("%+v", d)
	}
	for _, dv := range b.e.Snapshot().Devices {
		if dv.Origin == model.DeviceOriginDiscovered && strings.EqualFold(firstMAC(dv), testbed.ClientBMAC) {
			t.Errorf("a configured device is listed as discovered: %+v", dv)
		}
	}
}

func firstMAC(d engine.DeviceState) string {
	if len(d.MACs) > 0 {
		return d.MACs[0]
	}
	return ""
}

// M6a test: DHCP switched off on one network leaves it silent; the other network still serves.
func TestDhcpOffOnOneNetworkLeavesItSilent(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		off := false
		setDHCP(c, labID, &model.DhcpScope{Enabled: &off})
	})
	b.follow()
	if _, ok := waitBound(t, b.udhcpc(b.top.A, 8), 30*time.Second); !ok {
		t.Fatal("the network with DHCP does not serve")
	}
	if ip, ok := waitBound(t, b.udhcpc(b.top.C, 3), 12*time.Second); ok {
		t.Fatalf("the network with DHCP off answered: %s", ip)
	}
	// switching it on makes it serve
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		setDHCP(c, labID, &model.DhcpScope{LeaseTime: ptrS("60s")})
	})
	if ip, ok := waitBound(t, b.udhcpc(b.top.C, 8), 30*time.Second); !ok || !strings.HasPrefix(ip, "10.20.0.") {
		t.Fatalf("after switching DHCP on: %q %v", ip, ok)
	}
}

// keaSockets returns what Kea's status-get says about its sockets: "ready", "retrying" or "failed",
// with the errors of the interfaces it could not open a socket on.
func (b *dhcpBed) keaSockets() (string, []string) {
	b.t.Helper()
	a, err := b.kc.Do(context.Background(), "status-get", nil)
	if err != nil {
		b.t.Fatal(err)
	}
	var st struct {
		Sockets struct {
			Status string   `json:"status"`
			Errors []string `json:"errors"`
		} `json:"sockets"`
	}
	if err := json.Unmarshal(a.Arguments, &st); err != nil {
		b.t.Fatal(err)
	}
	return st.Sockets.Status, st.Sockets.Errors
}

// #64 test: Kea opens its packet socket on a bridge only while it is configured, and skips a bridge
// without a carrier. A configuration that arrives while the test port is down (its link flaps) left
// the network without DHCP until Kea was restarted; now the gateway configures Kea again when the
// bridge comes up, and DHCP answers.
func TestDHCPAnswersAgainAfterTheTestPortFlappedDuringAnApply(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	b.follow()
	if err := b.e.FollowHost(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	subs, err := b.kc.Subnets(context.Background())
	if err != nil || len(subs) != 1 {
		t.Fatalf("%+v %v", subs, err)
	}
	bridge := subs[0].Interface
	waitCarrier := func(on bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for strings.Contains(b.top.GW.Must("ip", "link", "show", "dev", bridge), "NO-CARRIER") == on {
			if time.Now().After(deadline) {
				t.Fatalf("the carrier of %s is not %v:\n%s", bridge, on, b.top.GW.Must("ip", "link"))
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// the test port goes down (its peer on the switch side: the gateway's port loses its carrier,
	// and with it the bridge), and a changed scope is applied meanwhile: Kea skips the bridge
	b.top.Switch0.Must("ip", "link", "set", "p1", "down")
	waitCarrier(false)
	b.apply(func(c *model.Configuration) { setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("120s")}) })
	if st, errs := b.keaSockets(); st == "ready" || len(errs) == 0 || !strings.Contains(errs[0], bridge) {
		t.Fatalf("Kea opened a socket on %s without a carrier, the flap is not reproduced: %s %v", bridge, st, errs)
	}

	// the port comes back: DHCP answers on the network again
	b.top.Switch0.Must("ip", "link", "set", "p1", "up")
	waitCarrier(true)
	ip, ok := waitBound(t, b.udhcpc(b.top.A, 8), 30*time.Second)
	if !ok || !strings.HasPrefix(ip, "10.10.0.") {
		st, errs := b.keaSockets()
		t.Fatalf("no lease after the port came back (%q); Kea's sockets: %s %v", ip, st, errs)
	}
	if st, errs := b.keaSockets(); st != "ready" {
		t.Errorf("Kea's sockets: %s %v", st, errs)
	}
	// the engine saw the bridge come up and configured Kea again; it does not wait for Kea's own
	// retries, which end after a minute
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(b.log.String(), "an interface of the DHCP server came up") {
		if time.Now().After(deadline) {
			t.Fatal("the engine did not configure Kea again when the bridge came up")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// M6a test: a device that changes its address shows an identity event within a second.
func TestAnAddressChangeIsAnEventWithinASecond(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	b.follow()
	// a device with a static address, not DHCP: a configured one on the testbed's own address
	b.top.A.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	b.waitDevice(testbed.ClientAMAC, 20*time.Second, func(d *engine.DeviceState) bool { return len(d.Addresses) > 0 && d.Online })
	ch, cancel := b.e.Subscribe()
	defer cancel()

	// A gets another address (the old one is gone: no connection holds it)
	b.top.A.Must("ip", "addr", "flush", "dev", "eth0")
	b.top.A.Must("ip", "addr", "add", "10.10.0.99/24", "dev", "eth0")
	b.top.A.Must("ip", "route", "add", "default", "via", testbed.LAN0Gateway)
	start := time.Now()
	b.top.A.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	ev, ok := waitEvent(ch, engine.EventDeviceIdentityChanged, 5*time.Second)
	if !ok {
		t.Fatalf("no identity event\n%+v", b.e.Snapshot().Devices)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("the identity event came after %v, the target is one second", took)
	}
	if n, _ := ev.Data["new_addresses"].([]string); !contains(n, "10.10.0.99") {
		t.Errorf("%v", ev.Data)
	}
}

// M6a-08 test: a burst of forty devices changing address at once (a host rotating MACs, or just a
// noisy neighbor table) debounces into exactly one identity update, not one per device: the generation
// advances by one, every device's set shows its new address, and the kernel's generation marker (which
// only a full apply rewrites) stays the one from before the burst.
func TestABurstOfNeighborChangesIsOneIdentityUpdate(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	// only FollowNeighbors triggers an observation here: the periodic poll would be a second,
	// independent source of generations and make the "exactly one" assertion meaningless.
	if err := b.e.PollObserved(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
	// The debounce is what the test is about, not its production value: a burst of 80 netlink
	// operations must land in one window. With 50 ms a stalled CI runner (a descheduled VM) splits
	// the burst into two windows and the engine correctly makes two identity updates, so the window
	// is wide enough that only a stall of half a second could split it.
	const debounce = 500 * time.Millisecond
	if err := b.e.FollowNeighbors(context.Background(), debounce); err != nil {
		t.Fatal(err)
	}

	baseline := b.generationMarker()
	mac := func(i int) string { return fmt.Sprintf("02:aa:00:00:00:%02x", i) }
	var create strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&create, "neigh replace 10.10.0.%d lladdr %s dev br-iot nud permanent\n", 100+i, mac(i))
	}
	b.top.GW.MustStdin(create.String(), "ip", "-batch", "-")
	// 40 new devices without faults are elements of the identity map like an address change
	// (P2-AUDIT-01): wait for them to actually land in the kernel, not only for the snapshot (which
	// updates before the apply loop has caught up) to show the devices.
	inMap := func() int {
		n := 0
		for k := range b.identityMapPairs() {
			if a, err := netip.ParseAddr(k); err == nil && a.Is4() && a.As4()[3] >= 100 && a.As4()[3] < 140 {
				n++
			}
		}
		return n
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && inMap() < 40 {
		time.Sleep(100 * time.Millisecond)
	}
	if n := inMap(); n != 40 {
		t.Fatalf("the identity map holds %d of the 40 devices", n)
	}
	if got := b.generationMarker(); got != baseline {
		t.Errorf("the generation marker changed from %q to %q: the new devices took a full apply", baseline, got)
	}

	gen := b.e.Snapshot().Generation
	marker := b.generationMarker()
	var move strings.Builder
	for i := 0; i < 40; i++ {
		// "replace" only touches the given IP: without deleting the old one too, the device would
		// end up with both addresses instead of having moved
		fmt.Fprintf(&move, "neigh del 10.10.0.%d dev br-iot\n", 100+i)
		fmt.Fprintf(&move, "neigh replace 10.10.0.%d lladdr %s dev br-iot nud permanent\n", 150+i, mac(i))
	}
	b.top.GW.MustStdin(move.String(), "ip", "-batch", "-")
	time.Sleep(debounce + 2*time.Second) // the debounce plus a margin to settle

	if got := b.e.Snapshot().Generation - gen; got != 1 {
		t.Errorf("the burst made %d generations, want exactly 1", got)
	}
	if got := b.generationMarker(); got != marker {
		t.Errorf("the generation marker changed from %q to %q: a full apply ran, not an element update", marker, got)
	}
	for i := 0; i < 40; i++ {
		want := fmt.Sprintf("10.10.0.%d", 150+i)
		b.waitDevice(mac(i), 2*time.Second, func(d *engine.DeviceState) bool {
			return len(d.Addresses) > 0 && d.Addresses[0].String() == want
		})
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// M6a test: the flows of a device are listed with the device.
func TestFlowsOfADeviceAreListed(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	b.follow()
	b.top.Server.Start("python3", "-m", "http.server", "8080", "--bind", testbed.ServerAddr)
	ip, ok := waitBound(t, b.udhcpc(b.top.A, 8), 30*time.Second)
	if !ok {
		t.Fatal("no lease")
	}
	b.top.A.Must("ip", "route", "replace", "default", "via", testbed.LAN0Gateway)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := b.top.A.Run(context.Background(), "curl", "-s", "-m", "3", "-o", "/dev/null", fmt.Sprintf("http://%s:8080/", testbed.ServerAddr)); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	d := b.waitDevice(testbed.ClientAMAC, 20*time.Second, func(d *engine.DeviceState) bool { return len(d.Addresses) > 0 })
	flows, err := b.e.Flows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var mine []engine.Flow
	for _, f := range flows {
		if f.Device == d.ID {
			mine = append(mine, f)
		}
	}
	if len(mine) == 0 {
		t.Fatalf("no flow of the device %s (%s) in %+v", d.Name, ip, flows)
	}
	f := mine[0]
	if f.Src.String() != ip || f.Dst.String() != testbed.ServerAddr || f.DPort != 8080 || f.Protocol != "tcp" || f.Network != tIoT {
		t.Errorf("%+v", f)
	}
	if f.Upload.Bytes <= 0 || f.Download.Bytes <= 0 {
		t.Errorf("accounting is off: upload %+v, download %+v", f.Upload, f.Download)
	}
}

// M6a-04 test: a new connection's traffic reaches the device's observed state (flows_active, the
// byte rates) within a few seconds, not whenever the regular poll next runs — the poll interval
// here is an hour, far longer than this test's own deadline, so only the conntrack watch can
// explain a fast result. (GET /flows itself already reads conntrack live on every call, with or
// without this feature, so it is not what distinguishes the watch from plain polling; the
// device's cached observed state is.) The deadline is generous (3s, not a few hundred ms) because
// the real path spawns a real `conntrack -L` child process on every observation, whose latency
// under CI load is not something this test controls.
func TestANewFlowUpdatesTheDeviceWithoutWaitingForThePoll(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	if err := b.e.PollObserved(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := b.e.FollowConntrack(context.Background(), 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	b.top.Server.Start("python3", "-m", "http.server", "8080", "--bind", testbed.ServerAddr)
	ip, ok := waitBound(t, b.udhcpc(b.top.A, 8), 30*time.Second)
	if !ok {
		t.Fatal("no lease")
	}
	b.top.A.Must("ip", "route", "replace", "default", "via", testbed.LAN0Gateway)
	d := b.waitDevice(testbed.ClientAMAC, 20*time.Second, func(d *engine.DeviceState) bool { return len(d.Addresses) > 0 })
	if d.FlowsActive != 0 {
		t.Fatalf("flows already active before any connection: %+v", d)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := b.top.A.Run(context.Background(), "curl", "-s", "-m", "3", "-o", "/dev/null", fmt.Sprintf("http://%s:8080/", testbed.ServerAddr)); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	// waitDevice itself enforces the deadline (it fails the test if it is not met)
	got := b.waitDevice(testbed.ClientAMAC, 3*time.Second, func(d *engine.DeviceState) bool { return d.FlowsActive > 0 })
	if got.Addresses[0].String() != ip {
		t.Errorf("%+v, ip %s", got, ip)
	}
}

// identityMapPairs returns the real kernel's identity map, address to device number.
func (b *dhcpBed) identityMapPairs() map[string]string {
	b.t.Helper()
	st, err := apply.ReadState(context.Background(), apply.Local{E: b.ex}, b.top.GW.Name, apply.Want{})
	if err != nil {
		b.t.Fatal(err)
	}
	out := map[string]string{}
	for _, o := range st.Nft.Objects {
		if o.Map != nil && strings.HasPrefix(o.Map.Name, "ident4_") {
			for k, v := range o.Map.Pairs() {
				out[k] = v
			}
		}
	}
	return out
}

// identityMapAddresses returns the addresses the real kernel's identity map holds for dev's device
// number (plan §3.3's single identity map, M7), sorted; none when the device has no entry yet. A
// device can hold two for a while: the gateway's neighbor entry for an address a host has just left
// lingers until it is garbage-collected.
func (b *dhcpBed) identityMapAddresses(dev string) []string {
	b.t.Helper()
	s := b.e.Snapshot()
	tg := compiler.Compile(compiler.Input{Config: s.Config, Host: s.Host, Generation: compiler.Generation{Revision: s.Revision, Seq: s.Generation}, Identity: &s.Identity,
		DeviceNums: s.DeviceNums, DeviceNumHigh: s.DeviceNumHigh})
	num, known := tg.DeviceNums[dev]
	if !known {
		return nil
	}
	st, err := apply.ReadState(context.Background(), apply.Local{E: b.ex}, b.top.GW.Name, apply.Want{})
	if err != nil {
		b.t.Fatal(err)
	}
	var out []string
	for _, o := range st.Nft.Objects {
		if o.Map != nil && o.Map.Name == tg.IdentityMap {
			want := strconv.Itoa(num)
			for k, v := range o.Map.Pairs() {
				if v == want {
					out = append(out, k)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// M7 test: after a forced address change, the device's entry in the real kernel's identity map
// follows within one second (plan §3.3's acceptance list, plan §2.3's "identity changes take a
// second at most") - the actual nftables map element, not only the API event that
// TestAnAddressChangeIsAnEventWithinASecond already covers.
func TestTheIdentityMapEntryFollowsAForcedAddressChangeWithinASecond(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	b.follow()
	b.top.A.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	dev := engine.DeviceID(testbed.ClientAMAC)
	b.waitDevice(testbed.ClientAMAC, 20*time.Second, func(d *engine.DeviceState) bool { return len(d.Addresses) > 0 && d.Online })
	// a device that has just appeared reaches the kernel through an element update like the
	// address change below (P2-AUDIT-01, TestANewDeviceReachesTheIdentityMapWithoutAFullApply);
	// the deadline is generous because this is only the setup
	setupDeadline := time.Now().Add(15 * time.Second)
	for {
		addrs := b.identityMapAddresses(dev)
		if contains(addrs, testbed.ClientAAddr) {
			break
		}
		if time.Now().After(setupDeadline) {
			t.Fatalf("setup: the identity map holds %v", addrs)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A gets another address (the old one is gone: no connection holds it)
	b.top.A.Must("ip", "addr", "flush", "dev", "eth0")
	b.top.A.Must("ip", "addr", "add", "10.10.0.99/24", "dev", "eth0")
	b.top.A.Must("ip", "route", "add", "default", "via", testbed.LAN0Gateway)
	start := time.Now()
	b.top.A.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)

	deadline := time.Now().Add(5 * time.Second)
	var last []string
	for time.Now().Before(deadline) {
		last = b.identityMapAddresses(dev)
		if contains(last, "10.10.0.99") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !contains(last, "10.10.0.99") {
		t.Fatalf("the identity map never followed the address change: holds %v", last)
	}
	// The budget is looser than TestAnAddressChangeIsAnEventWithinASecond's one second: that test
	// only waits for the in-memory event the observe step emits directly, while this one also
	// waits for the apply loop's incremental update to reach the real kernel (one executor
	// round trip to add the new map element, one more to list the map back for verification -
	// two real `nft` forks, not simulated ones) and for a real CI runner's own scheduling noise
	// on top of that. See docs/open-items.md P2-M7-03. The time goes into the measurement record, so
	// that a drift towards the bound shows before it fails.
	if err := testbed.Measure(t, "A's address change").Duration("", "identity map convergence", time.Since(start), time.Second, 0, 3*time.Second); err != nil {
		t.Errorf("the identity map followed too late, the target is three seconds: %v", err)
	}
}

// P2-AUDIT-01 test (plan §2.3, M7 scope "a new device is an element update too"): a device without
// faults of its own that appears next to a known one reaches the real kernel's identity map with
// element operations alone (no nft transaction beyond the element updates, the generation marker of
// the last full apply unchanged), within the bound of P2-M7-03 (3 s on the CI runners; asserted where
// the testbed measures, not under software emulation), and the known device keeps its number.
func TestANewDeviceReachesTheIdentityMapWithoutAFullApply(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(dhcpOn)
	b.follow()
	devA, devB := engine.DeviceID(testbed.ClientAMAC), engine.DeviceID(testbed.ClientBMAC)
	b.top.A.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	setupDeadline := time.Now().Add(15 * time.Second)
	for !contains(b.identityMapAddresses(devA), testbed.ClientAAddr) {
		if time.Now().After(setupDeadline) {
			t.Fatalf("setup: the identity map holds %v for A", b.identityMapAddresses(devA))
		}
		time.Sleep(100 * time.Millisecond)
	}
	numA := b.e.Snapshot().DeviceNums[devA]
	if _, known := b.e.Snapshot().DeviceNums[devB]; known {
		t.Fatalf("setup: B is known before it sent anything: %v", b.e.Snapshot().DeviceNums)
	}
	marker := b.generationMarker()
	b.nft.take()

	start := time.Now()
	b.top.B.Must("ping", "-c", "1", "-W", "1", "-n", testbed.LAN0Gateway)
	deadline := time.Now().Add(10 * time.Second)
	var last []string
	for time.Now().Before(deadline) {
		if last = b.identityMapAddresses(devB); contains(last, testbed.ClientBAddr) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	took := time.Since(start)
	if !contains(last, testbed.ClientBAddr) {
		t.Fatalf("the new device never reached the identity map: holds %v", last)
	}
	t.Logf("the new device reached the identity map after %v", took)
	if testbed.Accurate() {
		if err := testbed.Measure(t, "the new device B").Duration("", "identity map convergence", took, time.Second, 0, 3*time.Second); err != nil {
			t.Errorf("the new device reached the identity map too late, the bound is three seconds (P2-M7-03): %v", err)
		}
	}
	elements, full := b.nft.take()
	if full != 0 || elements == 0 {
		t.Errorf("the new device took %d full transactions and %d element updates, want element updates only", full, elements)
	}
	if got := b.generationMarker(); got != marker {
		t.Errorf("the generation marker changed from %q to %q: a full apply ran", marker, got)
	}
	if s := b.e.Snapshot(); s.DeviceNums[devA] != numA {
		t.Errorf("A was renumbered from %d to %d", numA, s.DeviceNums[devA])
	}
	if !contains(b.identityMapAddresses(devA), testbed.ClientAAddr) {
		t.Errorf("A lost its entry: %v", b.identityMapAddresses(devA))
	}
}
