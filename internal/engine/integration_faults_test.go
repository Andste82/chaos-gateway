//go:build testbed

package engine_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The measurement tests of M8b (plan §4.3): a fault written through the engine impairs the traffic
// of its scope the way it says, in each direction, and nothing else. Every fault test also measures
// devices the fault does not name and shows that they did not change (isolation).
//
// The traffic is one-way probes (testbed.Echo): the delay and the loss of the upload and of the
// download are each measured by themselves, which a ping cannot do. What is asserted depends on
// where the test runs (plan §4.3, "Where"):
//
//   - always (functional): the effect is present in the right direction, the loss is there and in
//     the direction it was configured, the devices that are not named keep their delay and lose
//     nothing;
//   - with native execution or KVM (testbed.Accurate): the median of each direction is within
//     ±2 ms + 5 % of the configured delay, the spread of the upload is that of the configured jitter,
//     the loss is within the 99.9 % binomial interval of the configured rate, and an unaffected
//     device is within ±2 ms + 5 % of what it measured before the fault. A statistical assertion that
//     fails is measured again once, and only a second failure fails the test (testbed.Statistically).
//     N is at least 200 probes for the delay and 2000 for the loss; under emulation the runs are
//     shorter because nothing statistical is asserted.

const (
	flDevA = "a1111111-0000-4000-8000-000000000001"
	flDevB = "a1111111-0000-4000-8000-000000000002"
	flDevC = "a1111111-0000-4000-8000-000000000003"
	flGrp  = "b2222222-0000-4000-8000-000000000001"

	probePort = 9400
)

// withDevices gives the configuration the devices the tests give faults to (A and B in the IoT
// network, C in Lab, identified by their addresses) and the group "pair" with A and C, which lie in
// two networks.
func withDevices(c *model.Configuration) {
	dev := func(name, addr string) model.Device {
		return model.Device{Name: name, Identifiers: &model.DeviceIdentifiers{Ipv4: &[]string{addr}}}
	}
	devs := map[string]model.Device{}
	if c.Devices != nil {
		devs = *c.Devices
	}
	devs[flDevA] = dev("dev-a", testbed.ClientAAddr)
	devs[flDevB] = dev("dev-b", testbed.ClientBAddr)
	devs[flDevC] = dev("dev-c", testbed.ClientCAddr)
	c.Devices = &devs
	groups := map[string]model.Group{}
	if c.Groups != nil {
		groups = *c.Groups
	}
	groups[flGrp] = model.Group{Name: "pair", Members: &[]string{flDevA, flDevC}}
	c.Groups = &groups
}

// resolveDevices lets the engine observe the host, which is what resolves the addresses of the
// configured devices into its identity (plan §2.3); a fault on a device or a group has no packet to
// classify before. The engine must run on the real clock (newReal with realClock).
func resolveDevices(t *testing.T, e *engine.Engine) {
	t.Helper()
	if err := e.PollObserved(context.Background(), 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for len(e.Snapshot().Identity.Addresses) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("the engine did not resolve the devices: %+v", e.Snapshot().Identity)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := e.Barrier(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// startFaultLab applies the configuration with the test devices (and what mod adds) and lets the
// engine resolve them.
func startFaultLab(t *testing.T, mod func(*model.Configuration)) *real {
	t.Helper()
	r := newReal(t, nil, realClock)
	if _, err := r.apply(r.revision(func(c *model.Configuration) {
		withDevices(c)
		if mod != nil {
			mod(c)
		}
	}), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	resolveDevices(t, r.e)
	warmUp(t, r.top)
	settleIdentity(t, r.e)
	return r
}

// settleIdentity waits until what the engine knows of the hosts has not changed for a second and a half
// (three observations) and its work on it is done. The pings of warmUp make the gateway meet hosts it did
// not know (the neighbors of the server's side), the engine learns them at its next observation, and a
// discovered device takes a numeral in the identity map: a test that compiles the snapshot at that moment
// and reads the kernel a moment later would see the two differ.
func settleIdentity(t *testing.T, e *engine.Engine) {
	t.Helper()
	signature := func() string {
		id := e.Snapshot().Identity
		var disc []string
		for _, d := range id.Discovered {
			disc = append(disc, d.ID)
		}
		sort.Strings(disc)
		return fmt.Sprint(id.Addresses, disc)
	}
	last, quiet := signature(), 0
	deadline := time.Now().Add(30 * time.Second)
	for quiet < 3 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if now := signature(); now != last {
			last, quiet = now, 0
		} else {
			quiet++
		}
	}
	if _, err := e.Barrier(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// warmUp sends a few pings from every test device to the server, once the lab stands. On a fast kernel
// (native, KVM) the first packets of the first flow of the Lab network, which a test measures right after
// the lab came up, have waited about a second before they went on (the probes of a 5 ms interval:
// median in microseconds, the 95th percentile at 860 to 970 ms, and the same in 4 of 4 runs on the hosted
// level 1 and 1b runners; none of it on a slow emulated kernel). Everything after it is clean. The
// cause was not found (P2-M8b-08); what a measurement of one flow must not do is to take that second for
// the fault, so the lab is brought to its steady state before any test starts. The ping that waited is
// logged.
func warmUp(t *testing.T, top *testbed.Topology) {
	t.Helper()
	var wg sync.WaitGroup
	out := make([]string, 3)
	for i, ns := range []*testbed.Namespace{top.A, top.B, top.C} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			text, _ := ns.Run(ctx, "ping", "-n", "-c", "3", "-i", "0.4", "-W", "3", testbed.ServerAddr)
			res, err := testbed.ParsePing(text)
			if err != nil {
				out[i] = fmt.Sprintf("%s: %v", ns.Short, err)
				return
			}
			var worst time.Duration
			for _, d := range res.RTTs {
				worst = max(worst, d)
			}
			out[i] = fmt.Sprintf("%s %d of %d answered, slowest %v", ns.Short, res.Received, res.Sent, worst)
		}()
	}
	wg.Wait()
	t.Logf("warm-up: %v", out)
}

// withMatrix lets the two test networks reach each other.
func withMatrix(c *model.Configuration) {
	entries := []model.MatrixEntry{}
	if c.AccessMatrix != nil && c.AccessMatrix.Entries != nil {
		entries = *c.AccessMatrix.Entries
	}
	for _, p := range [][2]string{{tIoT, labID}, {labID, tIoT}} {
		entries = append(entries, model.MatrixEntry{
			From: model.MatrixEndpoint{Network: ptr(p[0])}, To: model.MatrixEndpoint{Network: ptr(p[1])},
			Policy: model.MatrixEntryPolicyAllow})
	}
	c.AccessMatrix = &model.AccessMatrix{Entries: &entries}
}

// shape is what a fault does to one flow: the upload (the direction of the initiator) and the
// download.
type shape struct {
	up, upJitter time.Duration
	upLoss       float64 // 0 to 1
	down         time.Duration
	downLoss     float64
}

// standard is the shape most tests use: the directions differ by more than the noise of an emulated
// kernel, so that the direction can be told even where the numbers cannot be trusted.
var standard = shape{up: 150 * time.Millisecond, upJitter: 15 * time.Millisecond, upLoss: 0.05, down: 30 * time.Millisecond}

// yaml renders the shape as the fault of an overlay request.
func (s shape) yaml(extra string) string {
	up := fmt.Sprintf("latency: %dms", s.up.Milliseconds())
	if s.upJitter > 0 {
		up += fmt.Sprintf(", jitter: %dms", s.upJitter.Milliseconds())
	}
	if s.upLoss > 0 {
		up += fmt.Sprintf(", loss: %g%%", s.upLoss*100)
	}
	down := fmt.Sprintf("latency: %dms", s.down.Milliseconds())
	if s.downLoss > 0 {
		down += fmt.Sprintf(", loss: %g%%", s.downLoss*100)
	}
	if extra != "" {
		extra += ", "
	}
	return fmt.Sprintf("{%supload: {%s}, download: {%s}}", extra, up, down)
}

// settle is how long a probe run waits for the last answers: the longest round trip of the tests
// (900 ms) with room for the emulated kernel.
func settle() time.Duration {
	if testbed.Accurate() {
		return 1500 * time.Millisecond
	}
	return 4 * time.Second
}

// impairedRun is a run long enough for the assertions of the place it runs: N >= 2000 for the loss
// with native execution or KVM, a short one under emulation, where only the effect is looked at.
func impairedRun() testbed.ProbeOptions {
	if testbed.Accurate() {
		return testbed.ProbeOptions{Count: 2000, Interval: 6 * time.Millisecond, Settle: settle()}
	}
	return testbed.ProbeOptions{Count: 120, Interval: 25 * time.Millisecond, Settle: settle()}
}

// quietRun is the run of a device the fault does not name: N >= 200 for the median.
func quietRun() testbed.ProbeOptions {
	if testbed.Accurate() {
		return testbed.ProbeOptions{Count: 300, Interval: 10 * time.Millisecond, Settle: settle()}
	}
	return testbed.ProbeOptions{Count: 40, Interval: 25 * time.Millisecond, Settle: settle()}
}

// flow is one direction-pair under test: where the probes start (and from which address) and the
// echo they go to.
type flow struct {
	name string
	from *testbed.Namespace
	src  string
	echo *testbed.Echo
}

func (f flow) run(t *testing.T, o testbed.ProbeOptions) testbed.ProbeResult {
	t.Helper()
	o.Src = f.src
	return f.echo.MustProbe(t, f.from, o)
}

// expectImpaired measures the flow and asserts the shape: the functional assertions always, the
// accuracy ones where they are meaningful. The measured distribution is logged.
func expectImpaired(t *testing.T, f flow, want shape) {
	t.Helper()
	res := f.run(t, impairedRun())
	t.Logf("%s, impaired: %s", f.name, res)

	// functional: the effect is there, in the right direction
	if res.Delivered == 0 || res.Replied == 0 {
		t.Fatalf("%s: nothing came through (%s)", f.name, res)
	}
	if got := res.UpMedian(); got < want.up-want.upJitter-time.Millisecond {
		t.Errorf("%s: the upload delay is %v, a fault of %v is present", f.name, got, want.up)
	}
	if got := res.DownMedian(); got < want.down-time.Millisecond {
		t.Errorf("%s: the download delay is %v, a fault of %v is present", f.name, got, want.down)
	}
	if up, down := res.UpMedian(), res.DownMedian(); want.up > want.down && up-down < (want.up-want.down)/2 {
		t.Errorf("%s: upload %v and download %v: the directions are not told apart (configured %v and %v)", f.name, up, down, want.up, want.down)
	}
	if want.upLoss > 0 && res.UpLoss() == 0 {
		t.Errorf("%s: no loss in the upload where %.0f %% is configured (%s)", f.name, want.upLoss*100, res)
	}
	if want.upLoss == 0 && res.UpLoss() != 0 {
		t.Errorf("%s: the upload lost %.2f %% where none is configured", f.name, res.UpLoss()*100)
	}
	if want.downLoss > 0 && res.DownLoss() == 0 {
		t.Errorf("%s: no loss in the download where %.0f %% is configured", f.name, want.downLoss*100)
	}
	if want.downLoss == 0 && res.DownLoss() != 0 {
		t.Errorf("%s: the download lost %.2f %% where none is configured", f.name, res.DownLoss()*100)
	}

	if !testbed.Accurate() {
		t.Logf("%s: emulated kernel, the accuracy assertions (§4.3) run in CI", f.name)
		return
	}
	fresh := &res
	testbed.Statistically(t, f.name, func() error {
		cur := fresh
		if cur == nil {
			again := f.run(t, impairedRun())
			t.Logf("%s, impaired again: %s", f.name, again)
			cur = &again
		}
		fresh = nil
		errs := []error{
			testbed.CheckLatency(f.name+" upload", cur.UpMedian(), want.up),
			testbed.CheckLatency(f.name+" download", cur.DownMedian(), want.down),
			testbed.CheckLoss(f.name+" upload", cur.Sent-cur.Delivered, cur.Sent, want.upLoss),
			testbed.CheckLoss(f.name+" download", cur.Delivered-cur.Replied, cur.Delivered, want.downLoss),
		}
		if want.upJitter > 0 {
			errs = append(errs, testbed.CheckSpread(f.name+" upload", cur.Up, want.upJitter))
		}
		return joinErrs(errs...)
	})
}

// baseline measures a flow before any fault exists, for expectUnaffected.
func baseline(t *testing.T, f flow) testbed.ProbeResult {
	t.Helper()
	res := f.run(t, quietRun())
	t.Logf("%s, before: %s", f.name, res)
	if res.Delivered == 0 || res.Replied == 0 || res.UpLoss() != 0 || res.DownLoss() != 0 {
		t.Fatalf("%s: the flow is not clean before the fault: %s", f.name, res)
	}
	return res
}

// expectUnaffected measures a flow the fault does not name (isolation, plan §4.3): it loses nothing and
// its delay is what it was before. Under emulation "what it was" has noise of tens of milliseconds, so
// there the assertion is that it did not grow by more than 25 ms, which is far below every delay the
// tests configure on the flows they impair.
func expectUnaffected(t *testing.T, f flow, before testbed.ProbeResult) {
	t.Helper()
	res := f.run(t, quietRun())
	t.Logf("%s, unaffected: %s", f.name, res)
	if res.UpLoss() != 0 || res.DownLoss() != 0 {
		t.Errorf("%s: a flow the fault does not name lost packets: %s", f.name, res)
	}
	limit := 25 * time.Millisecond
	if res.UpMedian() > before.UpMedian()+limit || res.DownMedian() > before.DownMedian()+limit {
		t.Errorf("%s: the flow is slower since the fault: %s (before: %s)", f.name, res, before)
	}
	if !testbed.Accurate() {
		return
	}
	fresh := &res
	testbed.Statistically(t, f.name+" isolation", func() error {
		cur := fresh
		if cur == nil {
			again := f.run(t, quietRun())
			t.Logf("%s, unaffected again: %s", f.name, again)
			cur = &again
		}
		fresh = nil
		var errs []error
		if !testbed.Within(cur.UpMedian(), before.UpMedian(), 2*time.Millisecond, 0.05) {
			errs = append(errs, fmt.Errorf("%s upload median %v, before the fault %v", f.name, cur.UpMedian(), before.UpMedian()))
		}
		if !testbed.Within(cur.DownMedian(), before.DownMedian(), 2*time.Millisecond, 0.05) {
			errs = append(errs, fmt.Errorf("%s download median %v, before the fault %v", f.name, cur.DownMedian(), before.DownMedian()))
		}
		if cur.UpLoss() != 0 || cur.DownLoss() != 0 {
			errs = append(errs, fmt.Errorf("%s lost packets: %s", f.name, cur))
		}
		return joinErrs(errs...)
	})
}

func joinErrs(errs ...error) error {
	var msg string
	for _, e := range errs {
		if e != nil {
			if msg != "" {
				msg += "; "
			}
			msg += e.Error()
		}
	}
	if msg == "" {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

// serverFlows are the flows of A, B and C to the server (the uplink): three devices in two networks.
func serverFlows(t *testing.T, top *testbed.Topology) (a, b, c flow) {
	t.Helper()
	echo := testbed.StartEcho(t, top.Server, testbed.ServerAddr, probePort)
	return flow{name: "A to the server", from: top.A, echo: echo},
		flow{name: "B to the server", from: top.B, echo: echo},
		flow{name: "C to the server", from: top.C, echo: echo}
}

// A fault on one device (scope device): the device's flow is impaired as configured, in each direction,
// right after the write returns; the other device of the same network and the device of the other
// network do not change.
func TestADeviceFaultImpairsThatDeviceAsConfiguredAndNoOther(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, c := serverFlows(t, r.top)
	beforeB, beforeC := baseline(t, b), baseline(t, c)

	r.mustPut(admin, "target: {device: dev-a}\nfault: "+standard.yaml(""))
	// the write has returned, so the kernel holds the fault (the apply verified it, tc included): the
	// very first packet of the first probe is already impaired
	first := a.run(t, testbed.ProbeOptions{Count: 5, Interval: 100 * time.Millisecond, Settle: settle()})
	if first.Delivered == 0 || first.UpMedian() < standard.up-standard.upJitter {
		t.Errorf("the fault is not in the kernel when the write returns: %s", first)
	}
	r.verifyKernel()

	expectImpaired(t, a, standard)
	expectUnaffected(t, b, beforeB)
	expectUnaffected(t, c, beforeC)

	// the same fault as the round trip of a ping: the sum of the two directions (plan §4.3 latency:
	// N >= 200 probes, median within ±2 ms + 5 %)
	n, gap := 200, 20*time.Millisecond
	if !testbed.Accurate() {
		n = 30
	}
	pings := testbed.MustPing(t, r.top.A, testbed.ServerAddr, n, gap)
	t.Logf("ping of A: median %v, loss %.1f %%", pings.Median(), pings.Loss()*100)
	if pings.Median() < standard.up+standard.down-standard.upJitter-time.Millisecond {
		t.Errorf("the round trip of A is %v, the fault adds %v", pings.Median(), standard.up+standard.down)
	}
	if testbed.Accurate() {
		testbed.Statistically(t, "ping of A", func() error {
			res := testbed.MustPing(t, r.top.A, testbed.ServerAddr, n, gap)
			t.Logf("ping of A: median %v", res.Median())
			return testbed.CheckLatency("ping of A", res.Median(), standard.up+standard.down)
		})
	}
}

// A fault on a group (scope group): every member is impaired, whichever network it is in; a device that
// is not a member (B, in the network of a member) is not.
func TestAGroupFaultImpairsEveryMemberOfTheGroupAndNoOther(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, c := serverFlows(t, r.top)
	beforeB := baseline(t, b)

	r.mustPut(admin, "target: {group: pair}\nfault: "+standard.yaml(""))
	r.verifyKernel()

	expectImpaired(t, a, standard)
	expectImpaired(t, c, standard)
	expectUnaffected(t, b, beforeB)
}

// A fault on a network (scope network): every device of the network is impaired, the devices of
// another network are not.
func TestANetworkFaultImpairsEveryDeviceOfTheNetworkAndNoOtherNetwork(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, c := serverFlows(t, r.top)
	beforeC := baseline(t, c)

	r.mustPut(admin, "target: {network: IoT}\nfault: "+standard.yaml(""))
	r.verifyKernel()

	expectImpaired(t, a, standard)
	expectImpaired(t, b, standard)
	expectUnaffected(t, c, beforeC)
}

// Traffic between two test networks: each network has a fault of its own towards the other, and the
// fault belongs to the one that opened the connection (plan §2.4, E12): A's flow to C is shaped by the
// IoT fault, C's flow to A by the Lab fault, the two directions of a flow by the upload and download
// of its initiator's fault. The interfaces of the two networks carry both flows' directions, so this
// is the case the direction bit of the classification exists for (plan §3.3, spike S11). A flow to the
// uplink, which neither fault names (destination), does not change.
func TestFaultsBetweenTwoTestNetworksFollowTheInitiatorAndTheDirection(t *testing.T) {
	r := startFaultLab(t, withMatrix)
	echoA := testbed.StartEcho(t, r.top.A, testbed.ClientAAddr, probePort)
	echoC := testbed.StartEcho(t, r.top.C, testbed.ClientCAddr, probePort)
	_, _, cToServer := serverFlows(t, r.top)
	aToC := flow{name: "A to C", from: r.top.A, echo: echoC}
	cToA := flow{name: "C to A", from: r.top.C, echo: echoA}
	beforeServer := baseline(t, cToServer)

	towardsLab := shape{up: 150 * time.Millisecond, upJitter: 15 * time.Millisecond, upLoss: 0.05, down: 30 * time.Millisecond}
	towardsIoT := shape{up: 60 * time.Millisecond, upJitter: 6 * time.Millisecond, down: 120 * time.Millisecond, downLoss: 0.03}
	r.mustPut(admin, "target: {network: IoT}\nfault: "+towardsLab.yaml("destination: {cidr: 10.20.0.0/24}"))
	r.mustPut(admin, "target: {network: Lab}\nfault: "+towardsIoT.yaml("destination: {cidr: 10.10.0.0/24}"))
	r.verifyKernel()

	expectImpaired(t, aToC, towardsLab)
	expectImpaired(t, cToA, towardsIoT)
	// C's flow to the uplink is no flow of either fault: its destination is not named
	expectUnaffected(t, cToServer, beforeServer)
}
