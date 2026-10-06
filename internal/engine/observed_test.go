package engine_test

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const (
	iotID  = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	devID  = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	macCfg = "02:00:00:00:00:31"
)

// fakeDHCP records what the engine asks of the DHCP server.
type fakeDHCP struct {
	mu      sync.Mutex
	applys  []*compiler.KeaTarget
	err     error
	testErr error
	leases  []kea.Lease
}

func (f *fakeDHCP) Apply(_ context.Context, t *compiler.KeaTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applys = append(f.applys, t)
	return f.err
}

func (f *fakeDHCP) Leases(context.Context) ([]kea.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kea.Lease(nil), f.leases...), nil
}

func (f *fakeDHCP) Test(context.Context, *compiler.KeaTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.testErr
}

func (f *fakeDHCP) setErr(err error)     { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeDHCP) setTestErr(err error) { f.mu.Lock(); f.testErr = err; f.mu.Unlock() }
func (f *fakeDHCP) calls() int           { f.mu.Lock(); defer f.mu.Unlock(); return len(f.applys) }
func (f *fakeDHCP) last() *compiler.KeaTarget {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applys) == 0 {
		return nil
	}
	return f.applys[len(f.applys)-1]
}

// dhcpHarness is the engine with a fake DHCP server and a network with DHCP and a configured device.
func dhcpHarness(t *testing.T) (*harness, *fakeDHCP) {
	t.Helper()
	h := newHarness(t)
	f := &fakeDHCP{}
	e, err := engine.New(engine.Config{Store: h.st, Exec: apply.Local{E: h.ex}, Clock: h.clk, DHCP: f})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	h.e = e
	return h, f
}

func withDHCPAndDevice(c *model.Configuration) {
	n := (*c.Networks)[iotID]
	lan, _ := n.AsLanNetwork()
	lan.Dhcp = &model.DhcpScope{}
	_ = n.FromLanNetwork(lan)
	(*c.Networks)[iotID] = n
	macs := []string{macCfg}
	c.Devices = &map[string]model.Device{devID: {Name: "esp32-42", Identifiers: &model.DeviceIdentifiers{Macs: &macs}}}
}

func neighbor(ip, mac string) linux.Neighbor {
	return linux.Neighbor{Dst: ip, Dev: "br-iot", LLAddr: mac, State: []string{"REACHABLE"}}
}

func (h *harness) observe() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.e.ObserveNow(ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) device(id string) *engine.DeviceState {
	for _, d := range h.e.Snapshot().Devices {
		if d.ID == id {
			c := d
			return &c
		}
	}
	return nil
}

// M6a-06 test: the preview runs Kea's own config-test on a configured scope, so a scope Kea would
// reject (a managed option code, bad option data) is reported before an apply would hit it, instead
// of only showing up afterwards as DHCPError.
func TestPreviewReportsAScopeKeaRejects(t *testing.T) {
	h, f := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	f.setTestErr(errors.New("'option-data' parameter is invalid"))
	r2 := h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		n := (*c.Networks)[iotID]
		lan, _ := n.AsLanNetwork()
		lan.Dhcp.LeaseTime = ptr("20m")
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
	})
	p, err := h.e.Preview(context.Background(), r2)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, pr := range p.Problems {
		if pr.Code == compiler.CodeDHCP && strings.Contains(pr.Message, "option-data") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no dhcp problem in %+v", p.Problems)
	}
}

func TestTheKernelAndTheDHCPServerAreConfiguredTogether(t *testing.T) {
	h, f := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	tg := f.last()
	if tg == nil || len(tg.Config.Subnets) != 1 || tg.Config.Subnets[0].Interface != "br-iot" {
		t.Fatalf("%+v", tg)
	}
	if got := h.e.Snapshot().KeaNetworks[tg.Config.Subnets[0].ID]; got != iotID {
		t.Errorf("the snapshot does not map the subnet to its network: %v", h.e.Snapshot().KeaNetworks)
	}
	if h.e.Snapshot().DHCPError != "" {
		t.Errorf("%s", h.e.Snapshot().DHCPError)
	}
	// DHCP off: the server gets a configuration without subnets
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		n := (*c.Networks)[iotID]
		lan, _ := n.AsLanNetwork()
		off := false
		lan.Dhcp = &model.DhcpScope{Enabled: &off}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
	}))
	if tg := f.last(); tg != nil {
		t.Errorf("DHCP off leaves the server without a scope: %+v", tg.Config.Subnets)
	}
}

func TestADownDhcpServerDoesNotBlockTheApplyAndIsRetried(t *testing.T) {
	h, f := dhcpHarness(t)
	f.setErr(errors.New("connection refused"))
	if _, err := h.apply(h.revision(withDHCPAndDevice)); err != nil {
		t.Fatalf("a down DHCP server fails the apply: %v", err)
	}
	if s := h.e.Snapshot(); !strings.Contains(s.DHCPError, "connection refused") || s.LastError != "" {
		t.Fatalf("%q %q", s.DHCPError, s.LastError)
	}
	// the server comes back: the configuration is sent again
	f.setErr(nil)
	before := f.calls()
	h.clk.BlockUntil(1)
	h.clk.Advance(6 * time.Second)
	waitStatus2(t, h, func(s *engine.Snapshot) bool { return s.DHCPError == "" })
	if f.calls() <= before {
		t.Error("the configuration was not sent again")
	}
}

func waitStatus2(t *testing.T, h *harness, ok func(*engine.Snapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok(h.e.Snapshot()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the snapshot never became what the test waits for: %+v", h.e.Snapshot().DHCPError)
}

func TestDevicesAppearAndTheirAddressesFollowThem(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	ch, cancel := h.e.Subscribe()
	defer cancel()
	h.observe() // the first reading only learns

	// a configured device and an unknown one show up in the neighbor table
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg), neighbor("10.10.0.50", "02:00:00:00:00:aa")})
	h.observe()
	d := h.device(devID)
	if d == nil || !d.Online || len(d.Addresses) != 1 || d.Addresses[0].String() != "10.10.0.31" || d.Origin != model.DeviceOriginConfigured {
		t.Fatalf("%+v", d)
	}
	disc := h.device(engine.DeviceID("02:00:00:00:00:aa"))
	if disc == nil || disc.Origin != model.DeviceOriginDiscovered || disc.Name != "dev-02-00-00-00-00-aa" || disc.Addresses[0].String() != "10.10.0.50" {
		t.Fatalf("%+v", disc)
	}
	ev := events(ch)
	if !has(ev, engine.EventDeviceDiscovered) || !has(ev, engine.EventDeviceOnline) {
		t.Errorf("%v", ev)
	}

	// the configured device gets a new address: an identity event, and the kernel's device set follows
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.77", macCfg), neighbor("10.10.0.50", "02:00:00:00:00:aa")})
	h.observe()
	if d := h.device(devID); len(d.Addresses) != 1 || d.Addresses[0].String() != "10.10.0.77" {
		t.Fatalf("%+v", d)
	}
	if ev := events(ch); !has(ev, engine.EventDeviceIdentityChanged) {
		t.Errorf("%v", ev)
	}
	h.barrier()
	if got := h.deviceSetElements(devID); got != "10.10.0.77" {
		t.Errorf("the device set holds %q", got)
	}

	// the device leaves: offline
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.50", "02:00:00:00:00:aa")})
	h.observe()
	if d := h.device(devID); d.Online {
		t.Errorf("%+v", d)
	}
	if ev := events(ch); !has(ev, engine.EventDeviceOffline) {
		t.Errorf("%v", ev)
	}
}

// deviceSetElements returns the addresses the device's entry in the identity map holds, in the
// kernel (plan §3.3: the single identity map replaced the Phase 1 per-device sets this helper
// originally read, M6a-07/M7).
func (h *harness) deviceSetElements(dev string) string {
	h.t.Helper()
	v, ok := h.tryDeviceSetElements(dev)
	if !ok {
		h.t.Fatalf("no identity map entry for device %s in the kernel", dev)
	}
	return v
}

// tryDeviceSetElements is deviceSetElements without the hard failure, for polling a map that may
// not hold the device's entry yet.
func (h *harness) tryDeviceSetElements(dev string) (string, bool) {
	h.t.Helper()
	s := h.e.Snapshot()
	tg := compiler.Compile(compiler.Input{Config: s.Config, Host: s.Host, Generation: compiler.Generation{Revision: s.Revision, Seq: s.Generation}, Identity: &s.Identity})
	num, ok := tg.DeviceNums[dev]
	if !ok {
		return "", false
	}
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, o := range st.Nft.Objects {
		if o.Map != nil && o.Map.Name == tg.IdentityMap {
			var el []string
			want := strconv.Itoa(num)
			for k, v := range o.Map.Pairs() {
				if v == want {
					el = append(el, k)
				}
			}
			if len(el) == 0 {
				return "", false
			}
			sort.Strings(el)
			return strings.Join(el, ","), true
		}
	}
	return "", false
}

// M6a-16 test: device_identity_changed carries the generation of the desired state it belongs to
// (spec Event.generation, "set for ... identity changes"), the same one owner.observed builds before
// emitting the event.
func TestDeviceIdentityChangedCarriesTheGeneration(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	ch, cancel := h.e.Subscribe()
	defer cancel()

	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.77", macCfg)})
	h.observe()
	ev := collect(ch, engine.EventDeviceIdentityChanged)
	if len(ev) != 1 {
		t.Fatalf("%v", ev)
	}
	g, ok := ev[0].Data["generation"].(uint64)
	if !ok || g == 0 {
		t.Fatalf("%+v", ev[0].Data)
	}
	if got := h.e.Snapshot().Generation; got != g {
		t.Errorf("event generation %d, snapshot generation %d", g, got)
	}
}

func TestAnIdentityChangeIsAnIncrementalUpdateNotARebuild(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.barrier()
	before := len(h.k.Commands())
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.77", macCfg)})
	h.observe()
	h.barrier()
	var mutations []string
	for _, c := range h.k.Commands()[before:] {
		// reads are ip/nft list; mutations are `nft -j -f -`, `ip`, `sysctl` ... with arguments that change state
		if strings.HasPrefix(c, "nft -j -f") || strings.HasPrefix(c, "ip -batch") || strings.HasPrefix(c, "tc ") || strings.HasPrefix(c, "iptables -w 5 -I") {
			mutations = append(mutations, c)
		}
	}
	if len(mutations) != 2 { // one add and one delete of elements: two commands of the one request
		t.Errorf("the update ran %d mutating commands: %v", len(mutations), mutations)
	}
	// M6a-11: verifying the update reads only the nft sets, not the rest of the state
	var reads []string
	for _, c := range h.k.Commands()[before:] {
		if strings.HasPrefix(c, "ip -j") || strings.HasPrefix(c, "iptables -w 5 -S") {
			reads = append(reads, c)
		}
	}
	if len(reads) != 0 {
		t.Errorf("the update read more than the nft sets: %v", reads)
	}
	if got := h.deviceSetElements(devID); got != "10.10.0.77" {
		t.Errorf("the device set holds %q", got)
	}
}

// M6a-05 test: while a lockout-relevant revision waits for confirmation, the kernel already runs it;
// identity must use that running configuration, not the last committed one, so a device the new
// revision adds gets its address at once instead of waiting for the confirm and the next poll.
func TestANewDeviceOfAPendingRevisionGetsItsAddressAtOnce(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(nil))
	r2 := h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		c.Management.UiPort = ptr(8443)
	})
	a, err := h.apply(r2, engine.ApplyOptions{ConfirmTimeout: 30 * time.Second})
	if err != nil || a.Status != "pending_confirm" {
		t.Fatalf("%+v %v", a, err)
	}
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.barrier()

	d := h.device(devID)
	if d == nil || len(d.Addresses) != 1 || d.Addresses[0].String() != "10.10.0.31" {
		t.Fatalf("%+v", d)
	}
	// the snapshot's Config stays the previous, still-active one until confirmed (commit-confirm
	// semantics), so the device set element is checked directly in the kernel the pending revision
	// already runs, not by recompiling from the snapshot.
	if !strings.Contains(h.nftText(), "10.10.0.31") {
		t.Errorf("the device set in the kernel does not hold the new address:\n%s", h.nftText())
	}
}

// M6a-07 test: new devices appearing close together converge once, not once per appearance; the
// second one waits for the window instead of running its own full apply immediately.
func TestNewDevicesWithinTheWindowConvergeOnce(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.observe() // the first reading only learns, no device yet

	macX, macY := "02:00:00:00:00:a1", "02:00:00:00:00:a2"
	idX, idY := engine.DeviceID(macX), engine.DeviceID(macY)

	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.60", macX)})
	h.observe()
	h.barrier()
	if got := h.deviceSetElements(idX); got != "10.10.0.60" {
		t.Fatalf("the first device did not converge at once: %q", got)
	}

	// a second device appears in the same instant: it must not run its own full apply right away
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.60", macX), neighbor("10.10.0.61", macY)})
	h.observe()
	if _, ok := h.tryDeviceSetElements(idY); ok {
		t.Fatal("the second device converged at once instead of waiting for the window")
	}

	// once the window elapses, the coalesced converge picks up the latest state, in one update
	before := len(h.k.Commands())
	h.clk.Advance(2 * time.Second)
	var got string
	for i := 0; i < 100; i++ {
		h.barrier()
		if v, ok := h.tryDeviceSetElements(idY); ok && v == "10.10.0.61" {
			got = v
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != "10.10.0.61" {
		t.Fatalf("the second device never converged: %q", got)
	}
	// a new device changes the set count, so this is a full apply (identityOps refuses it as
	// non-incremental); what matters here is that it ran exactly once for both devices together,
	// not once more on top of whatever X's own earlier appearance already did.
	var loads int
	for _, c := range h.k.Commands()[before:] {
		if strings.HasPrefix(c, "nft -j -f") {
			loads++
		}
	}
	if loads != 1 {
		t.Errorf("the coalesced converge loaded the ruleset %d times, want 1", loads)
	}
}

func TestABurstOfTriggersIsOneReading(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.observe()
	if err := h.e.PollObserved(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
	h.barrier()
	gen := h.e.Snapshot().Generation
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	for i := 0; i < 50; i++ {
		h.e.TriggerObserve()
	}
	// the poller waits for the burst to end: its debounce timer fires when the clock moves
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d := findDev(h.e.Snapshot(), devID); d != nil && d.Online {
			break
		}
		h.clk.Advance(150 * time.Millisecond)
		time.Sleep(20 * time.Millisecond)
	}
	h.barrier()
	if g := h.e.Snapshot().Generation; g != gen+1 {
		t.Errorf("fifty triggers made %d generations", g-gen)
	}
}

func findDev(s *engine.Snapshot, id string) *engine.DeviceState {
	for i := range s.Devices {
		if s.Devices[i].ID == id {
			return &s.Devices[i]
		}
	}
	return nil
}

func TestLeasesGiveDevicesTheirAddressAndLeaseEventsAreForwarded(t *testing.T) {
	h, f := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	sid := 0
	for id := range h.e.Snapshot().KeaNetworks {
		sid = id
	}
	h.observe()
	ch, cancel := h.e.Subscribe()
	defer cancel()
	f.mu.Lock()
	f.leases = []kea.Lease{{IP: "10.10.0.150", MAC: macCfg, SubnetID: sid, ValidLft: 600, CLTT: time.Now().Unix(), Hostname: "esp32-42"}}
	f.mu.Unlock()
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.150", macCfg)})
	h.observe()
	d := h.device(devID)
	if d == nil || d.Lease == nil || d.Lease.Ip != "10.10.0.150" || d.Lease.Network.String() != iotID || len(d.Addresses) != 1 || d.Addresses[0].String() != "10.10.0.150" {
		t.Fatalf("%+v", d)
	}
	if ls := h.e.Snapshot().Leases; len(ls) != 1 || ls[0].Hostname == nil || *ls[0].Hostname != "esp32-42" {
		t.Errorf("%+v", ls)
	}
	// the hook's event becomes an event of the stream
	h.e.LeaseEvent(kea.Event{Name: "renew", IP: netip.MustParseAddr("10.10.0.150"), MAC: macCfg, SubnetID: sid, ValidLifetime: 600})
	got := collect(ch, engine.EventDHCPLease)
	if len(got) != 1 || got[0].Data["event"] != "renew" || got[0].Data["network"] != iotID || got[0].Data["ip"] != "10.10.0.150" {
		t.Errorf("%+v", got)
	}
}

func TestFlowsAreListedWithTheirDevice(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1\n" +
		"udp      17 25 src=198.51.100.7 dst=203.0.113.10 sport=1 dport=2 [UNREPLIED] src=203.0.113.10 dst=198.51.100.7 sport=2 dport=1 mark=0 use=1\n")
	flows, err := h.e.Flows(context.Background())
	if err != nil || len(flows) != 1 {
		t.Fatalf("%+v %v", flows, err)
	}
	f := flows[0]
	if f.Device != devID || f.Network != iotID || f.Protocol != "tcp" || f.DPort != 8883 || f.State != "ESTABLISHED" || f.NatSrc.String() != "203.0.113.1" ||
		f.Upload.Bytes != 412 || f.Download.Bytes != 500 || f.Dst.String() != "203.0.113.10" {
		t.Errorf("%+v", f)
	}
}

// M6a-03 test: a flow's started_at comes from conntrack's ktimestamp extension when the kernel
// reports it, and is zero (left out, not guessed at) when it does not.
func TestFlowsCarryTheirStartTimeWhenConntrackReportsIt(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 start=1700000000000000000 [ASSURED] mark=0 use=1\n")
	flows, err := h.e.Flows(context.Background())
	if err != nil || len(flows) != 1 {
		t.Fatalf("%+v %v", flows, err)
	}
	if want := time.Unix(0, 1700000000000000000).UTC(); !flows[0].StartedAt.Equal(want) {
		t.Errorf("got %v, want %v", flows[0].StartedAt, want)
	}

	// no `start=` field (nf_conntrack_timestamp off, or an older conntrack-tools): zero, not guessed
	h.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1\n")
	flows, err = h.e.Flows(context.Background())
	if err != nil || len(flows) != 1 {
		t.Fatalf("%+v %v", flows, err)
	}
	if !flows[0].StartedAt.IsZero() {
		t.Errorf("got %v, want zero", flows[0].StartedAt)
	}
}

// M6a-13 test: two concurrent pings from the same device to the same destination have different
// echo ids; their flows must not collide into one id.
// M6a-18 test: a host behind a LAN network's own downstream router is discovered by its address, the
// same way a host behind a WireGuard client's network already was; the gateway only ever sees it
// through its connections, never as a direct neighbor.
func TestAHostBehindALANDownstreamRouterIsDiscovered(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	// gateway.yaml's IoT network has a downstream route 10.30.0.0/24 via 10.10.0.2
	h.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.30.0.5 dst=203.0.113.10 sport=1 dport=2 packets=1 bytes=60 src=203.0.113.10 dst=10.30.0.5 sport=2 dport=1 packets=1 bytes=60 [ASSURED] mark=0 use=1\n")
	h.observe()
	var d *engine.DeviceState
	for _, dv := range h.e.Snapshot().Devices {
		if dv.Origin == model.DeviceOriginDiscovered && len(dv.Addresses) > 0 && dv.Addresses[0].String() == "10.30.0.5" {
			c := dv
			d = &c
		}
	}
	if d == nil || !d.Online {
		t.Fatalf("the host behind the LAN downstream route was not discovered: %+v", h.e.Snapshot().Devices)
	}
}

// M6a-04 test: a scripted conntrack event, not the regular poll interval, is what makes a new
// connection show up: PollObserved runs with an interval far longer than this test's deadline, so
// only FollowConntrack's watch can explain the discovered device appearing.
func TestAConntrackEventTriggersAnObservationWithoutWaitingForThePoll(t *testing.T) {
	// plain newHarness/start, not dhcpHarness: a DHCP retry goroutine also uses the fake clock and
	// would be a second confounding timer alongside the ones this test means to drive by hand.
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(withDHCPAndDevice))
	// mustApply's commit already called TriggerObserve, which buffers one trigger in a channel
	// that ObserveNow (h.observe, above) never drains, since it reads independently of it. Left
	// alone, PollObserved's loop would consume that leftover the instant it starts and run its own
	// 100ms debounce for that reason alone — a confound that would let this test pass even if the
	// conntrack event below did nothing. Start the poller, let it drain and settle that leftover
	// first, then only start following conntrack once the slate is clean.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := h.e.PollObserved(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	h.clk.BlockUntil(2) // the hour-long ticker, and the debounce for the commit's own leftover trigger
	h.clk.Advance(100 * time.Millisecond)
	waitStatus(t, h, func(s *engine.Snapshot) bool { return len(s.Devices) > 0 }) // the leftover settles

	if err := h.e.FollowConntrack(ctx, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !h.k.HasConntrackWatch() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !h.k.HasConntrackWatch() {
		t.Fatal("the watch never opened")
	}
	for _, dv := range h.e.Snapshot().Devices {
		if dv.Origin == model.DeviceOriginDiscovered && len(dv.Addresses) > 0 && dv.Addresses[0].String() == "10.30.0.5" {
			t.Fatalf("the device already exists before the event: %+v", dv)
		}
	}

	// gateway.yaml's IoT network has a downstream route 10.30.0.0/24 via 10.10.0.2
	h.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.30.0.5 dst=203.0.113.10 sport=1 dport=2 packets=1 bytes=60 src=203.0.113.10 dst=10.30.0.5 sport=2 dport=1 packets=1 bytes=60 [ASSURED] mark=0 use=1\n")
	h.k.ScriptConntrackEvent(`[NEW] tcp      6 120 SYN_SENT src=10.30.0.5 dst=203.0.113.10 sport=1 dport=2 src=203.0.113.10 dst=10.30.0.5 sport=2 dport=1 id=1`)

	h.clk.BlockUntil(2) // the poller's hour-long ticker, and FollowConntrack's own debounce timer
	h.clk.Advance(50 * time.Millisecond)
	h.clk.BlockUntil(2)                   // the ticker again, and PollObserved's own debounce after the trigger
	h.clk.Advance(100 * time.Millisecond) // matches the unexported debounce in observe.go

	waitStatus(t, h, func(s *engine.Snapshot) bool {
		for _, dv := range s.Devices {
			if dv.Origin == model.DeviceOriginDiscovered && len(dv.Addresses) > 0 && dv.Addresses[0].String() == "10.30.0.5" {
				return dv.Online
			}
		}
		return false
	})
}

func TestConcurrentICMPFlowsDoNotCollide(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.k.SetConntrack(
		"ipv4     2 icmp     1 29 src=10.10.0.31 dst=203.0.113.10 type=8 code=0 id=100 src=203.0.113.10 dst=10.10.0.31 type=0 code=0 id=100 mark=0 use=1\n" +
			"ipv4     2 icmp     1 29 src=10.10.0.31 dst=203.0.113.10 type=8 code=0 id=101 src=203.0.113.10 dst=10.10.0.31 type=0 code=0 id=101 mark=0 use=1\n")
	flows, err := h.e.Flows(context.Background())
	if err != nil || len(flows) != 2 {
		t.Fatalf("%+v %v", flows, err)
	}
	if flows[0].ID == flows[1].ID {
		t.Errorf("both flows got the same id: %q", flows[0].ID)
	}
}

// M6a-14 test: a failed read of the observed state is not silent: the snapshot carries the error
// (and the health check degrades, api/system_test.go), and the addresses that were in use stay in
// use instead of looking like nothing is connected any more.
func TestAFailingConntrackReadIsReported(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=1 dport=2 packets=1 bytes=60 src=203.0.113.10 dst=10.10.0.31 sport=2 dport=1 packets=1 bytes=60 [ASSURED] mark=0 use=1\n")
	h.observe()
	if h.e.Snapshot().ObserveError != "" {
		t.Fatalf("a good read is not an error: %q", h.e.Snapshot().ObserveError)
	}
	if d := h.device(devID); d == nil || !d.Online {
		t.Fatalf("%+v", d)
	}

	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "conntrack" {
			return &executor.Result{Exit: 1, Stderr: "conntrack v1.4.8: netlink error: No such file or directory\n"}
		}
		return nil
	}
	h.observe()
	if got := h.e.Snapshot().ObserveError; got == "" {
		t.Fatal("a failing read was not reported")
	}
	// the address that was in use stays in use: the device is not shown as having gone offline
	if d := h.device(devID); d == nil || !d.Online {
		t.Errorf("%+v", d)
	}
}

// blockingNftExec wraps an apply.Exec and blocks, once, right before the first NftApply it sees -
// before calling the wrapped Exec at all, so nothing of the kernel simulation's own state (which a
// kernelsim.Kernel call this blocks would otherwise hold its lock on) is touched while blocked.
// TestAConcurrentFullApplyDoesNotRestoreAStaleDeviceAddress uses it to force a full apply's own
// write to still be "in flight" while something else about the kernel changes.
type blockingNftExec struct {
	inner   apply.Exec
	entered chan struct{}
	resume  chan struct{}
	armed   bool
	once    sync.Once
}

func (b *blockingNftExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if b.armed {
		for _, op := range ops {
			if _, ok := op.(*executor.NftApply); ok {
				b.once.Do(func() { close(b.entered); <-b.resume })
				break
			}
		}
	}
	return b.inner.Do(ctx, ops...)
}

// M7 test: a concurrent full apply does not restore a device's stale address (plan §3.3's
// acceptance list). An unrelated configuration change triggers a full apply, compiled with
// whatever identity was current right then; while that apply's own nft write is still in flight
// (blocked here deterministically, above the kernel simulation so the device's address can still
// change underneath it), the device's real address changes. Once the full apply's write lands -
// with the address that was current when it was compiled, exactly the stale value a bug would leave
// behind - an observation discovers the real change and corrects the kernel with an incremental
// element diff (engine's identityOps): the full apply's own write is not left as the final state.
func TestAConcurrentFullApplyDoesNotRestoreAStaleDeviceAddress(t *testing.T) {
	h := newHarness(t)
	be := &blockingNftExec{inner: apply.Local{E: h.ex}, entered: make(chan struct{}), resume: make(chan struct{})}
	e, err := engine.New(engine.Config{Store: h.st, Exec: be, Clock: h.clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.e = e
	t.Cleanup(e.Close)

	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	h.barrier()
	if got := h.deviceSetElements(devID); got != "10.10.0.31" {
		t.Fatalf("setup: the identity map holds %q", got)
	}

	// an unrelated change (the uplink gateway): a full, non-incremental apply, compiled with the
	// identity (10.10.0.31) that is current right now.
	r2 := h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		c.Uplink.Gateway = ptr("203.0.113.20")
	})
	be.armed = true
	done := make(chan error, 1)
	go func() { _, err := h.apply(r2); done <- err }()
	<-be.entered

	// the device moves while that full apply's write is still blocked mid-flight.
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.77", macCfg)})

	close(be.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := h.deviceSetElements(devID); got != "10.10.0.31" {
		t.Fatalf("setup: the full apply did not write the expected (stale) address: %q", got)
	}
	// only now does an observation discover the real change and correct it.
	h.observe()
	h.barrier()
	if got := h.deviceSetElements(devID); got != "10.10.0.77" {
		t.Errorf("the full apply's stale write was not corrected: the identity map holds %q", got)
	}
}
