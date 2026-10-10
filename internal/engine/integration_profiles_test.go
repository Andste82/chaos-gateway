//go:build testbed

package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The measurement tests of the profiles (M11, plan §4.3): a profile activated through the engine impairs the
// traffic of its scope with the parameters it defines, switching it yields the new ones, and the precedence of
// plan §2.4 holds on the wire: a device fault replaces the network profile's impairment part for that device
// alone, a fault replaces only the part of its own family, an overlay profile beats a configuration fault.
//
// Like the tests of the faults, they assert the functional effect always (present, in the right direction, the
// devices that are not named keep their delay and lose nothing) and the accuracy of the numbers where the
// execution is native or KVM (testbed.Accurate): the medians within ±2 ms + 5 %, the losses within the 99.9 %
// binomial interval, N >= 200 probes for the delay and 2000 for the loss; a statistical assertion that fails is
// measured again once (testbed.Statistically).

const (
	prLinkA   = "c3333333-0000-4000-8000-000000000001"
	prLinkB   = "c3333333-0000-4000-8000-000000000002"
	prRate    = "c3333333-0000-4000-8000-000000000003"
	prSmallPM = "c3333333-0000-4000-8000-000000000004"
	prSlowCfg = "d4444444-0000-4000-8000-000000000001"
)

// dir is what is expected of one direction of a flow.
type dir struct {
	delay, jitter time.Duration
	loss          float64 // 0 to 1
}

// flowShape is what a profile does to a flow: the upload (the direction of the initiator) and the download.
type flowShape struct{ up, down dir }

func (s shape) flow() flowShape {
	return flowShape{up: dir{s.up, s.upJitter, s.upLoss}, down: dir{s.down, 0, s.downLoss}}
}

var (
	// linkA and linkB are the custom profiles of the tests: clean numbers, the directions told apart by more than the
	// noise of an emulated kernel
	linkA = standard.flow()
	linkB = shape{up: 60 * time.Millisecond, upJitter: 6 * time.Millisecond, down: 120 * time.Millisecond, downLoss: 0.03}.flow()
	// badLTE is the built-in profile of that name (plan §2.9): 150 ms ± 50 ms, 3 % loss, in each direction
	badLTE = flowShape{up: dir{150 * time.Millisecond, 50 * time.Millisecond, 0.03}, down: dir{150 * time.Millisecond, 50 * time.Millisecond, 0.03}}
	// slowConfig is the configuration fault that some tests put under a profile
	slowConfig = flowShape{up: dir{delay: 400 * time.Millisecond}, down: dir{delay: 400 * time.Millisecond}}
	// small is the impairment part of the profile with a small MTU
	small = flowShape{up: dir{delay: 80 * time.Millisecond}, down: dir{delay: 80 * time.Millisecond}}
)

func profileConfig(c *model.Configuration) {
	imp := func(name string, p model.ImpairmentParams) model.Profile { return impairmentProfile(name, p) }
	c.Profiles = &map[string]model.Profile{
		prLinkA: imp("link-a", model.ImpairmentParams{
			Upload:   &model.NetemParams{Latency: ptr("150ms"), Jitter: ptr("15ms"), Loss: ptr("5%")},
			Download: &model.NetemParams{Latency: ptr("30ms")}}),
		prLinkB: imp("link-b", model.ImpairmentParams{
			Upload:   &model.NetemParams{Latency: ptr("60ms"), Jitter: ptr("6ms")},
			Download: &model.NetemParams{Latency: ptr("120ms"), Loss: ptr("3%")}}),
		prRate: imp("rate-2m", model.ImpairmentParams{Rate: ptr("2Mbit")}),
		prSmallPM: {Name: "small-mtu", Parts: model.ProfileParts{
			Impairment: &model.ImpairmentParams{Latency: ptr("80ms")},
			Mtu:        &model.MtuParams{Size: 1280, Mode: ptr(model.MtuParamsModeIcmp)},
		}},
	}
}

// expectFlow measures the flow and asserts the shape of a profile: the functional assertions always, the
// accuracy ones where they are meaningful. The measured distribution is logged.
func expectFlow(t *testing.T, f flow, want flowShape) {
	t.Helper()
	res := f.run(t, impairedRun())
	t.Logf("%s, impaired: %s", f.name, res)

	if res.Delivered == 0 || res.Replied == 0 {
		t.Fatalf("%s: nothing came through (%s)", f.name, res)
	}
	if got := res.UpMedian(); got < want.up.delay-want.up.jitter-time.Millisecond {
		t.Errorf("%s: the upload delay is %v, a profile of %v ± %v is active", f.name, got, want.up.delay, want.up.jitter)
	}
	if got := res.DownMedian(); got < want.down.delay-want.down.jitter-time.Millisecond {
		t.Errorf("%s: the download delay is %v, a profile of %v ± %v is active", f.name, got, want.down.delay, want.down.jitter)
	}
	if want.up.delay > 3*want.down.delay/2 && res.UpMedian()-res.DownMedian() < (want.up.delay-want.down.delay)/2 {
		t.Errorf("%s: upload %v and download %v: the directions are not told apart (configured %v and %v)", f.name, res.UpMedian(), res.DownMedian(), want.up.delay, want.down.delay)
	}
	if want.down.delay > 3*want.up.delay/2 && res.DownMedian()-res.UpMedian() < (want.down.delay-want.up.delay)/2 {
		t.Errorf("%s: upload %v and download %v: the directions are not told apart (configured %v and %v)", f.name, res.UpMedian(), res.DownMedian(), want.up.delay, want.down.delay)
	}
	for _, d := range []struct {
		name string
		got  float64
		want float64
	}{{"upload", res.UpLoss(), want.up.loss}, {"download", res.DownLoss(), want.down.loss}} {
		if d.want > 0 && d.got == 0 {
			t.Errorf("%s: no loss in the %s where %.0f %% is configured (%s)", f.name, d.name, d.want*100, res)
		}
		if d.want == 0 && d.got != 0 {
			t.Errorf("%s: the %s lost %.2f %% where none is configured", f.name, d.name, d.got*100)
		}
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
			testbed.CheckLatency(f.name+" upload", cur.UpMedian(), want.up.delay),
			testbed.CheckLatency(f.name+" download", cur.DownMedian(), want.down.delay),
			testbed.CheckLoss(f.name+" upload", cur.Sent-cur.Delivered, cur.Sent, want.up.loss),
			testbed.CheckLoss(f.name+" download", cur.Delivered-cur.Replied, cur.Delivered, want.down.loss),
		}
		if want.up.jitter > 0 {
			errs = append(errs, testbed.CheckSpread(f.name+" upload", cur.Up, want.up.jitter))
		}
		if want.down.jitter > 0 {
			errs = append(errs, testbed.CheckSpread(f.name+" download", cur.Down, want.down.jitter))
		}
		return joinErrs(errs...)
	})
}

// profileOf is the overlay request that activates a profile on a target.
func profileOf(target, name string) string {
	return fmt.Sprintf("target: {%s}\nprofile: %s", target, name)
}

// Plan M11 "Tests": activating and switching profiles yields the measured values. One activation on the network
// IoT, written again with another profile each time (the same overlay, 200 instead of 201): a custom profile, the
// other custom profile, the built-in Bad LTE, Normal, Offline; every device of the network follows, the device of
// the other network never changes.
func TestActivatingAndSwitchingProfilesYieldsTheMeasuredValues(t *testing.T) {
	r := startFaultLab(t, profileConfig)
	a, b, c := serverFlows(t, r.top)
	beforeA, beforeB, beforeC := baseline(t, a), baseline(t, b), baseline(t, c)

	first := r.mustPut(admin, profileOf("network: IoT", "link-a"))
	if !first.Created {
		t.Fatalf("%+v", first)
	}
	r.verifyKernel()
	expectFlow(t, a, linkA)
	expectFlow(t, b, linkA)
	expectUnaffected(t, c, beforeC)

	switchTo := func(name string) {
		t.Helper()
		res := r.mustPut(admin, profileOf("network: IoT", name))
		if res.Created || res.Overlay.Id != first.Overlay.Id {
			t.Fatalf("switching to %s made a new overlay: %+v", name, res)
		}
		r.verifyKernel()
	}
	switchTo("link-b")
	expectFlow(t, a, linkB)
	expectFlow(t, b, linkB)

	// the built-in profile of plan §2.9, with its rate (a queue per device): the delay and the loss are measured
	// here, the rate by the test of E9 below
	switchTo("bad-lte")
	expectFlow(t, a, badLTE)
	expectFlow(t, b, badLTE)
	expectUnaffected(t, c, beforeC)

	// Normal impairs nothing: both devices are back to what they were
	switchTo("normal")
	expectUnaffected(t, a, beforeA)
	expectUnaffected(t, b, beforeB)

	// Offline is a blackout: nothing of the network's devices comes through, the other network is fine
	switchTo("offline")
	for _, f := range []flow{a, b} {
		res := f.run(t, testbed.ProbeOptions{Count: 40, Interval: 20 * time.Millisecond, Settle: settle()})
		t.Logf("%s, offline: %s", f.name, res)
		if res.Delivered != 0 {
			t.Errorf("%s: %d probes came through the blackout of the profile Offline", f.name, res.Delivered)
		}
	}
	expectUnaffected(t, c, beforeC)

	// the activation ends: the devices are back to what they were
	r.deleteOverlay(first)
	r.verifyKernel()
	expectUnaffected(t, a, beforeA)
	expectUnaffected(t, b, beforeB)
}

// Plan M11 "Tests": a device fault overrides the network profile's impairment part (same layer). The device with
// the fault gets the fault alone - not the profile's loss, jitter or rate - and the other device of the network
// keeps the profile; when the fault ends, the device has the profile again.
func TestADeviceFaultOverridesTheNetworkProfileForThatDeviceOnly(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, c := serverFlows(t, r.top)
	beforeC := baseline(t, c)

	r.mustPut(admin, profileOf("network: IoT", "bad-lte"))
	// the fault asks for 150/30 ms and 5 % loss in one direction only: no jitter and no download loss, which the profile
	// has, so a merge would show
	fault := r.mustPut(admin, "target: {device: dev-a}\nfault: "+standard.yaml(""))
	r.verifyKernel()
	expectFlow(t, a, standard.flow())
	expectFlow(t, b, badLTE)
	expectUnaffected(t, c, beforeC)

	r.deleteOverlay(fault)
	r.verifyKernel()
	expectFlow(t, a, badLTE)
	expectFlow(t, b, badLTE)
}

// Plan M11 "Tests": a fault on the same scope replaces only the profile part of its family, the other families
// stay active. The profile on device A has an impairment part (80 ms) and an MTU part (1280, ICMP mode); an
// impairment fault on A replaces the delay and leaves the MTU, and when it ends the delay of the profile is back.
func TestAFaultOnTheSameScopeReplacesOnlyThePartOfItsFamilyOnTheWire(t *testing.T) {
	r := startFaultLab(t, profileConfig)
	a, _, _ := serverFlows(t, r.top)
	r.mustPut(admin, profileOf("device: dev-a", "small-mtu"))
	r.verifyKernel()
	limited := func(what string) {
		t.Helper()
		out, ok := ping(r.top.A, 1400)
		if ok || !strings.Contains(out, "mtu = 1280") {
			t.Errorf("%s: the MTU of the profile does not hold for A: %v\n%s", what, ok, out)
		}
		if out, ok := ping(r.top.B, 1400); !ok {
			t.Errorf("%s: B, which no profile names, is limited: %s", what, out)
		}
		for _, ns := range []*testbed.Namespace{r.top.Server, r.top.A, r.top.B} {
			ns.Must("ip", "route", "flush", "cache")
		}
	}
	limited("the profile alone")
	expectFlow(t, a, small)

	fault := r.mustPut(admin, "target: {device: dev-a}\nfault: "+standard.yaml(""))
	r.verifyKernel()
	expectFlow(t, a, standard.flow())
	limited("with a fault on the same scope")

	r.deleteOverlay(fault)
	r.verifyKernel()
	expectFlow(t, a, small)
	limited("after the fault")
}

// Plan M11 "Tests": an overlay profile beats a configuration fault, even on a wider scope (D24). Device A has a
// configured fault of 400 ms; a profile on its whole network replaces it, and when the activation ends the
// configured fault is back.
func TestAnOverlayProfileBeatsAConfigurationFaultOnTheWire(t *testing.T) {
	r := startFaultLab(t, func(c *model.Configuration) {
		profileConfig(c)
		c.Faults = &map[string]model.ConfigFault{prSlowCfg: {
			Name: ptr("slow-a"), Source: &model.Scope{Device: ptr(flDevA)}, Latency: ptr("400ms")}}
	})
	a, b, _ := serverFlows(t, r.top)
	expectFlow(t, a, slowConfig)

	act := r.mustPut(admin, profileOf("network: IoT", "link-a"))
	r.verifyKernel()
	expectFlow(t, a, linkA)
	expectFlow(t, b, linkA)

	r.deleteOverlay(act)
	r.verifyKernel()
	expectFlow(t, a, slowConfig)
}

// Normal takes the impairment of a wider scope away from one device: a fault on the network, the profile Normal on
// device A, and A is clean while B has the fault.
func TestTheNormalProfileOnADeviceTakesTheNetworkFaultAwayFromIt(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, _ := serverFlows(t, r.top)
	beforeA := baseline(t, a)

	r.mustPut(admin, "target: {network: IoT}\nfault: "+standard.yaml(""))
	r.mustPut(admin, profileOf("device: dev-a", "normal"))
	r.verifyKernel()
	expectUnaffected(t, a, beforeA)
	expectFlow(t, b, standard.flow())
}

// An activation follows the definition of its profile: a revision that changes the parameters of link-a changes
// what the devices of the network experience, without activating anything again.
func TestAnActivationFollowsTheNewDefinitionOfItsProfileOnTheWire(t *testing.T) {
	r := startFaultLab(t, profileConfig)
	a, _, _ := serverFlows(t, r.top)
	act := r.mustPut(admin, profileOf("network: IoT", "link-a"))
	expectFlow(t, a, linkA)

	if _, err := r.apply(r.revision(func(c *model.Configuration) {
		withDevices(c)
		profileConfig(c)
		(*c.Profiles)[prLinkA] = impairmentProfile("link-a", model.ImpairmentParams{
			Upload:   &model.NetemParams{Latency: ptr("60ms"), Jitter: ptr("6ms")},
			Download: &model.NetemParams{Latency: ptr("120ms"), Loss: ptr("3%")}})
	}), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := r.e.Snapshot().Overlays; len(got) != 1 || got[0].Id != act.Overlay.Id {
		t.Fatalf("the activation did not survive the revision: %+v", got)
	}
	r.verifyKernel()
	expectFlow(t, a, linkB)
}

// E9 (plan §2.4) for a profile: a profile with a rate on a network gives every device of it its own rate. Two
// devices of the network downloading at the same time get 2 Mbit/s each (±10 %, plan §4.3), the device of the
// other network is not held to it.
func TestAProfileWithARateGivesEveryDeviceOfTheNetworkItsOwnRate(t *testing.T) {
	r := startFaultLab(t, profileConfig)
	srv := r.top.Server
	for i := 0; i < 3; i++ {
		iperfServer(t, srv, 5301+i)
	}
	const rate = 2e6
	secs, omit := 12, 4
	if !testbed.Accurate() {
		secs, omit = 10, 4
	}
	r.mustPut(admin, profileOf("network: IoT", "rate-2m"))
	r.verifyKernel()
	if faults := r.e.Snapshot().Faults; len(faults) < 3 || faults[0].Profile != "rate-2m" {
		t.Fatalf("a rate gives one fault id per device and the shared queue, each naming its profile: %+v", faults)
	}
	measure := func() []float64 {
		got := parallel(t,
			func() (float64, error) { return iperfTCP(r.top.A, 5301, secs, omit, true) },
			func() (float64, error) { return iperfTCP(r.top.B, 5302, secs, omit, true) },
			func() (float64, error) { return iperfTCP(r.top.C, 5303, secs, omit, true) })
		t.Logf("download, A and B under the profile, C unlimited: A %.2f, B %.2f, C %.2f Mbit/s", got[0]/1e6, got[1]/1e6, got[2]/1e6)
		return got
	}
	got := measure()
	for i, name := range []string{"A", "B"} {
		if got[i] < 1.4e6 || got[i] > 2.4e6 {
			t.Errorf("download of %s: %.2f Mbit/s, want about 2", name, got[i]/1e6)
		}
	}
	floor := 3 * rate
	if testbed.Accurate() {
		floor = 10 * rate
	}
	if got[2] < floor {
		t.Errorf("download of C, which the profile does not name: %.2f Mbit/s, want at least %.0f", got[2]/1e6, floor/1e6)
	}
	if testbed.Accurate() {
		measured := got
		testbed.Statistically(t, "rate of A and B under the profile", func() error {
			g := measured
			measured = nil
			if g == nil {
				time.Sleep(15 * time.Second) // the queues of the first round drain
				g = measure()
			}
			for i, name := range []string{"A", "B"} {
				if !within10(g[i], rate) {
					return fmt.Errorf("download of %s: %.3f Mbit/s, not within ±10 %% of 2 Mbit/s (plan §4.3)", name, g[i]/1e6)
				}
			}
			return nil
		})
	}
}
