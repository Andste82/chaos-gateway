//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The measurement tests of M10 for the fault types that M8b did not measure (plan §4.3, M10 "Tests"):
// one test per type, written through the engine's overlay writes, each with the isolation of a flow the
// fault does not name. What is asserted depends on where the test runs, as in integration_faults_test.go:
// the functional assertions (the effect is there, in its direction, nothing else changes) always; the
// accuracy ones (the share of duplicated, reordered, corrupted and lost packets in the 99.9 % binomial
// interval, the bursts of the Gilbert-Elliott model, the throughput within ±10 %, the flapping within
// the timing tolerance) with native execution or KVM, with the flakiness policy of the plan (a failed
// attempt is measured once more; only the second failure fails).
//
// The share of a random effect in a probe run does not depend on the speed of the machine, but the
// run's length does (N >= 2000 packets for the interval to be narrow), and a slow emulated kernel
// cannot send 2000 probes 6 ms apart; the emulated runs are shorter and only look at the effect.

// checkStat is a statistical assertion on a probe run with the flakiness policy: the first
// measurement is `first`, a failed attempt measures anew with `measure`.
func checkStat(t *testing.T, what string, first testbed.ProbeResult, measure func() testbed.ProbeResult, check func(testbed.ProbeResult) error) {
	t.Helper()
	fresh := &first
	testbed.Statistically(t, what, func() error {
		cur := fresh
		if cur == nil {
			again := measure()
			t.Logf("%s, again: %s", what, again)
			cur = &again
		}
		fresh = nil
		return check(*cur)
	})
}

// kernelRun is the probe run of a test that needs the loss, the duplicates or the order of a number of
// packets: the same length as impairedRun, longer under emulation where the effect has to show.
func kernelRun(emulated int) testbed.ProbeOptions {
	o := impairedRun()
	if !testbed.Accurate() && emulated > 0 {
		o.Count = emulated
	}
	return o
}

func quiet(t *testing.T, what string, res testbed.ProbeResult) {
	t.Helper()
	if res.UpLoss() != 0 || res.DownLoss() != 0 || res.UpDuplicates() != 0 || res.DownDuplicates() != 0 || res.UpReordered() != 0 || res.DownReordered() != 0 {
		t.Errorf("%s: a flow the fault does not name changed: %s (duplicates %d/%d, reordered %d/%d)", what, res,
			res.UpDuplicates(), res.DownDuplicates(), res.UpReordered(), res.DownReordered())
	}
}

// ---- duplicate -----------------------------------------------------------------------------------

// A duplicating fault duplicates the packets of its device and direction with the probability it is
// given, next to other faults on the same interfaces (the kernel refuses a duplicating netem next to
// any other netem, so the copy is made by the hook of the duplication table: P2-M10-01), and nothing else changes.
func TestADuplicatingFaultDuplicatesAsConfiguredNextToOtherFaults(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, c := serverFlows(t, r.top)
	beforeC := baseline(t, c)

	// B has a delay fault: its netem is on every interface of the tree, which is what a duplicating
	// netem could not live next to
	r.mustPut(admin, "target: {device: dev-b}\nfault: {upload: {latency: 100ms}, download: {latency: 20ms}}")
	dup := r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {duplicate: 10%}}")
	tg := r.verifyKernel()
	if !tg.TC.HasDup() {
		t.Fatal("the target has no duplication hook")
	}
	// the hook: a chain on the egress hook of every interface of the tree
	hooks := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		out, err := r.top.GW.Run(ctx, "nft", "-j", "list", "table", executor.NftDupFamily, executor.NftDupTable)
		if err != nil {
			return "" // no table
		}
		rs, err := linux.ParseNft([]byte(out))
		if err != nil {
			t.Fatal(err)
		}
		var devs []string
		for _, o := range rs.Objects {
			if o.Chain != nil {
				devs = append(devs, o.Chain.Dev)
			}
		}
		sort.Strings(devs)
		return strings.Join(devs, " ")
	}
	if got, want := hooks(), strings.Join(tg.TC.Devs, " "); got != want {
		t.Errorf("the hook is on %q, the tree on %q", got, want)
	}

	// upload: every duplicated datagram arrives twice, and the echo answers each copy
	measure := func() testbed.ProbeResult { return a.run(t, kernelRun(600)) }
	res := measure()
	t.Logf("A, upload duplicate 10 %%: %s; duplicates in the upload %d, in the answers %d", res, res.UpDuplicates(), res.DownDuplicates())
	if res.UpDuplicates() == 0 {
		t.Errorf("no datagram of A arrived twice (%s)", res)
	}
	if res.DownDuplicates() != res.UpDuplicates() {
		t.Errorf("the download duplicates nothing of its own: %d copies of answers, %d copies of datagrams", res.DownDuplicates(), res.UpDuplicates())
	}
	if res.UpLoss() != 0 || res.DownLoss() != 0 || res.UpReordered() > res.Sent/10 {
		t.Errorf("a duplicating fault lost or scrambled packets: %s (reordered %d)", res, res.UpReordered())
	}
	if testbed.Accurate() {
		checkStat(t, "A upload duplicates", res, measure, func(x testbed.ProbeResult) error {
			return testbed.CheckShare("A upload duplicates", "duplicated", x.UpDuplicates(), x.Sent, 0.10)
		})
	}

	// B keeps its fault, C is as before
	resB := b.run(t, quietRun())
	if resB.UpMedian() < 99*time.Millisecond || resB.UpDuplicates() != 0 {
		t.Errorf("the delay fault of B changed next to the duplicating one: %s", resB)
	}
	expectUnaffected(t, c, beforeC)
	quiet(t, "C", c.run(t, quietRun()))

	// the download of A, replaced by the download direction: copies of the answers, none of the datagrams
	r.mustPut(admin, "target: {device: dev-a}\nfault: {download: {duplicate: 20%}}")
	r.verifyKernel()
	measureDown := func() testbed.ProbeResult { return a.run(t, kernelRun(600)) }
	res = measureDown()
	t.Logf("A, download duplicate 20 %%: %s; duplicates in the upload %d, in the answers %d", res, res.UpDuplicates(), res.DownDuplicates())
	if res.DownDuplicates() == 0 || res.UpDuplicates() != 0 {
		t.Errorf("the download fault duplicates answers (%d) and no datagram (%d)", res.DownDuplicates(), res.UpDuplicates())
	}
	if testbed.Accurate() {
		checkStat(t, "A download duplicates", res, measureDown, func(x testbed.ProbeResult) error {
			return testbed.CheckShare("A download duplicates", "duplicated", x.DownDuplicates(), x.Delivered, 0.20)
		})
	}

	// the hook goes when the last duplicating fault does, the delay fault of B stays
	r.deleteOverlay(dup)
	tg = r.verifyKernel()
	if tg.TC.HasDup() {
		t.Fatal("the target still duplicates")
	}
	if got := hooks(); got != "" {
		t.Errorf("the hook is still on %q", got)
	}
}

// ---- reorder -------------------------------------------------------------------------------------

// A reordering fault sends the share it is given of the packets at once and delays the rest, so the
// ones that were delayed arrive after later ones.
func TestAReorderingFaultSendsTheConfiguredShareAheadOfTheDelayedOnes(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, c := serverFlows(t, r.top)
	beforeB := baseline(t, b)
	_ = c

	r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {latency: 50ms, reorder: 25%}}")
	r.verifyKernel()

	measure := func() testbed.ProbeResult { return a.run(t, kernelRun(0)) }
	res := measure()
	immediate := func(x testbed.ProbeResult) int {
		n := 0
		for _, d := range x.Up {
			if d < 25*time.Millisecond {
				n++
			}
		}
		return n
	}
	t.Logf("A, upload latency 50 ms reorder 25 %%: %s; reordered %d, sent at once %d", res, res.UpReordered(), immediate(res))
	if res.Delivered == 0 || res.UpReordered() == 0 {
		t.Errorf("nothing was reordered: %s", res)
	}
	if res.UpLoss() != 0 || res.UpDuplicates() != 0 {
		t.Errorf("reordering lost or duplicated packets: %s", res)
	}
	// the answers come back in the order the datagrams arrived: the download adds no reordering of its own
	if res.DownReordered() > res.UpReordered() {
		t.Errorf("the download reordered %d answers, the upload %d datagrams", res.DownReordered(), res.UpReordered())
	}
	if testbed.Accurate() {
		checkStat(t, "A reordering", res, measure, func(x testbed.ProbeResult) error {
			return testbed.CheckShare("A reordering", "sent at once", immediate(x), x.Delivered, 0.25)
		})
	}
	// every packet that was delayed was delayed by the 50 ms and by nothing else
	for _, d := range res.Up {
		if d > 25*time.Millisecond && d < 45*time.Millisecond && testbed.Accurate() {
			t.Fatalf("an upload delay of %v is neither immediate nor 50 ms", d)
		}
	}
	quiet(t, "B", b.run(t, quietRun()))
	expectUnaffected(t, b, beforeB)
}

// ---- corrupt -------------------------------------------------------------------------------------

// A corrupting fault flips a bit in the share of the packets it is given; the receiver's checksums throw
// them away (plan §2.5: "usually dropped by checksums at the receiver"), the kernel counts them, and the
// ones that are not caught by a checksum (the source address of the frame) arrive.
func TestACorruptingFaultCorruptsAsConfiguredAndTheReceiverCountsTheChecksumErrors(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, _ := serverFlows(t, r.top)
	beforeB := baseline(t, b)
	srv := r.top.Server
	errors := func() int64 {
		hdr, err := testbed.SNMPCounter(srv, "Ip", "InHdrErrors")
		if err != nil {
			t.Fatal(err)
		}
		csum, err := testbed.SNMPCounter(srv, "Udp", "InCsumErrors")
		if err != nil {
			t.Fatal(err)
		}
		return hdr + csum
	}

	r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {corrupt: 10%}}")
	r.verifyKernel()

	before := errors()
	measure := func() testbed.ProbeResult { return a.run(t, kernelRun(600)) }
	res := measure()
	counted := errors() - before
	lost := res.Sent - res.Delivered
	t.Logf("A, upload corrupt 10 %%: %s; the server counted %d header and checksum errors for %d datagrams that did not arrive", res, counted, lost)
	if lost == 0 || counted == 0 {
		t.Errorf("no datagram was corrupted: lost %d, counted %d", lost, counted)
	}
	// every datagram that was lost was corrupted; not every corrupted one was counted by the server
	// (a flipped bit in the destination address of the frame never reaches the stack), and the server
	// counts the corrupted packets of the run only
	if counted > int64(lost) {
		t.Errorf("the server counted %d errors for %d lost datagrams", counted, lost)
	}
	if res.DownLoss() != 0 || res.UpDuplicates() != 0 {
		t.Errorf("corruption touched the download or duplicated: %s", res)
	}
	if testbed.Accurate() {
		checkStat(t, "A corruption", res, measure, func(x testbed.ProbeResult) error {
			// a corrupted packet is lost unless the flipped bit is one the receiver does not check: the
			// six bytes of the frame's source address, 6 of the 72 bytes of a probe (and the one at
			// random in the frame that the address check does not look at); the interval of the plan is
			// the one for the share that is lost, widened below by that figure
			return testbed.CheckShare("A corruption", "lost", x.Sent-x.Delivered, x.Sent, 0.10*(1-6.0/72))
		})
	}
	quiet(t, "B", b.run(t, quietRun()))
	expectUnaffected(t, b, beforeB)
}

// ---- burst loss ----------------------------------------------------------------------------------

// Burst loss (Gilbert-Elliott) loses packets in runs: the share of the lost ones is that of the model
// and their runs are as long as the model leaves the bad state.
func TestABurstLossFaultLosesInBurstsAsTheModelSays(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, _ := serverFlows(t, r.top)
	beforeB := baseline(t, b)

	// p 5 % into the bad state, r 25 % out of it: 1/6 of the time bad, runs of 4 packets
	r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {burst_loss: {p: 5%, r: 25%}}}")
	r.verifyKernel()

	measure := func() testbed.ProbeResult { return a.run(t, kernelRun(600)) }
	res := measure()
	lost := res.UpLost()
	t.Logf("A, upload burst loss p 5 %% r 25 %%: %s; %d lost in runs of %.1f packets", res, len(lost), testbed.MeanRun(lost))
	if len(lost) == 0 {
		t.Fatalf("no burst: %s", res)
	}
	if testbed.MeanRun(lost) < 1.5 {
		t.Errorf("the losses come one by one (mean run %.1f), not in bursts", testbed.MeanRun(lost))
	}
	if res.DownLoss() != 0 {
		t.Errorf("the download lost packets: %s", res)
	}
	if testbed.Accurate() {
		checkStat(t, "A burst loss", res, measure, func(x testbed.ProbeResult) error {
			return testbed.CheckBurstLoss("A burst loss", x.UpLost(), x.Sent, 0.05, 0.25, 1, 0)
		})
	}
	quiet(t, "B", b.run(t, quietRun()))
	expectUnaffected(t, b, beforeB)
}

// ---- blackout ------------------------------------------------------------------------------------

// A blackout drops everything of its device, in both directions, the queues count the drops, a flow the
// fault does not name keeps its connectivity, and the connectivity of the device returns with the next
// write (a running connection is hit as well: its packets are classified per packet).
func TestABlackoutDropsEverythingOfItsDeviceAndTheQueuesCountIt(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, _ := serverFlows(t, r.top)
	beforeB := baseline(t, b)

	// a stream that runs through the whole test: it must be hit by the blackout it did not exist for
	run := streamRun(a)
	waitDelivered(t, run, 20)

	black := r.mustPut(admin, "target: {device: dev-a}\nfault: {blackout: true}")
	r.verifyKernel()
	delivered := run.Delivered()
	res := a.run(t, testbed.ProbeOptions{Count: 100, Interval: 20 * time.Millisecond, Settle: 2 * time.Second})
	t.Logf("A, blackout: %s", res)
	if res.Delivered != 0 || res.Replied != 0 {
		t.Errorf("%d datagrams of A got through a blackout (%s)", res.Delivered, res)
	}
	if got := run.Delivered(); got > delivered+1 {
		t.Errorf("the running stream of A went on: %d delivered before, %d after the blackout", delivered, got)
	}
	f := faultOfOverlay(t, r.e.Snapshot(), black.Overlay)
	_, drops, _, _ := r.queueSum(f, compiler.Upload)
	if drops < int64(res.Sent) {
		t.Errorf("the upload queue dropped %d packets, the blackout swallowed at least %d probes", drops, res.Sent)
	}
	expectUnaffected(t, b, beforeB)

	// the next write ends it for the stream that is running as well
	r.mustPut(admin, "target: {device: dev-a}\nfault: {latency: 5ms}")
	back := a.run(t, testbed.ProbeOptions{Count: 50, Interval: 20 * time.Millisecond, Settle: settle()})
	if back.Delivered == 0 || back.Replied == 0 {
		t.Errorf("A is still cut off after the blackout was replaced: %s", back)
	}
	if _, err := run.Stop(); err != nil {
		t.Fatal(err)
	}
}

// ---- flapping ------------------------------------------------------------------------------------

// A flapping fault is up, then a blackout for the down time, and so on, starting up. The timing is the
// engine's (every toggle within FlapTolerance of the schedule) and the packets', which see an outage of
// the configured length to the precision of their spacing.
func TestAFlappingFaultBlacksOutOnSchedule(t *testing.T) {
	r := startFaultLab(t, nil)
	a, b, _ := serverFlows(t, r.top)

	up, down, iv, count := 3*time.Second, 2*time.Second, 10*time.Millisecond, 1900
	if !testbed.Accurate() {
		up, down, iv, count = 6*time.Second, 4*time.Second, 50*time.Millisecond, 700
	}
	body := fmt.Sprintf("target: {device: dev-a}\nfault: {flapping: {up: %ds, down: %ds}}", int(up.Seconds()), int(down.Seconds()))
	r.mustPut(admin, body)
	// (no verifyKernel here or later: it compiles the snapshot and reads the kernel a moment later, and a
	// boundary in between makes the two differ for a reason that is no defect; the apply's own verify
	// compared the kernel with the phase the engine held, and the toggles are checked below)

	opts := testbed.ProbeOptions{Count: count, Interval: iv, Settle: settle()}
	runB := b.echo.Begin(b.from, opts)
	measure := func() testbed.ProbeResult { return a.run(t, opts) }
	res := measure()
	resB, err := runB.Stop()
	if err != nil {
		t.Fatal(err)
	}
	quiet(t, "B during the flapping of A", resB)

	lost := res.UpLost()
	outages := testbed.Outages(lost, iv, int(down/iv)/2)
	for i, o := range outages {
		t.Logf("outage %d: from probe %d (%v after the first), %v long", i+1, o.First, o.Start, o.Len)
	}
	if res.Delivered == 0 {
		t.Fatalf("nothing got through in the up phases: %s", res)
	}
	// the engine's schedule: toggles of the upload and the download at start + up, + up + down, ...
	var upload []engine.FlapChange
	for _, c := range r.e.FlapLog() {
		if strings.HasSuffix(c.Key, "|upload") {
			upload = append(upload, c)
		}
	}
	if len(upload) < 4 {
		t.Fatalf("%d toggles of the upload in a run of %v: %+v", len(upload), time.Duration(count)*iv, upload)
	}
	for i, c := range upload {
		if c.Down != (i%2 == 0) {
			t.Errorf("toggle %d goes %v, the first one is the start of a blackout", i, c.Down)
		}
		if i > 0 {
			// a toggle into the down phase comes one up phase after the previous toggle, one back up a down
			// phase after it
			want := down
			if c.Down {
				want = up
			}
			if got := c.Scheduled - upload[i-1].Scheduled; got != want {
				t.Errorf("toggle %d is scheduled %v after the previous one, want %v", i, got, want)
			}
		}
		if c.Late() < 0 {
			t.Errorf("toggle %d committed %v before its time", i, -c.Late())
		}
		if testbed.Accurate() && c.Late() > engine.FlapTolerance {
			t.Errorf("toggle %d committed %v after its time, the tolerance is %v", i, c.Late(), engine.FlapTolerance)
		}
	}
	if testbed.Accurate() {
		checkStat(t, "A flapping", res, measure, func(x testbed.ProbeResult) error {
			return testbed.CheckFlaps("A flapping", testbed.Outages(x.UpLost(), iv, int(down/iv)/2), x.Sent, up, down, iv, engine.FlapTolerance, 3)
		})
		return
	}
	// emulated: the outages are there, whole, about as long as the down time
	var whole int
	for _, o := range outages {
		if o.First > 0 && o.Last < res.Sent-1 {
			whole++
			if o.Len < down/2 || o.Len > 2*down {
				t.Errorf("an outage of %v where %v is configured", o.Len, down)
			}
		}
	}
	if whole < 2 {
		t.Errorf("%d complete outages in the run (%d outages, %d lost of %d)", whole, len(outages), len(lost), res.Sent)
	}
}

// ---- rate and queue limit (D18) --------------------------------------------------------------------

// iperfServer starts an iperf3 server in the server's namespace.
func iperfServer(t *testing.T, ns *testbed.Namespace, port int) {
	t.Helper()
	p := ns.Start("iperf3", "-s", "--forceflush", "-B", testbed.ServerAddr, "-p", fmt.Sprint(port))
	deadline := time.Now().Add(time.Minute)
	for !strings.Contains(p.Output(), "Server listening") {
		select {
		case <-p.Done():
			t.Fatalf("iperf3 server on %d: %s", port, p.Output())
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("iperf3 server on %d is not ready: %s", port, p.Output())
		}
	}
}

// iperfTCP runs a TCP transfer of secs seconds (the first omit seconds are not counted) and returns the
// throughput the receiver saw, in bit/s. reverse makes the server send (the download of the device). An
// error is the tool failing (not a measurement): a rate fault queues seconds of data, and a control
// connection that has to wait behind them is sometimes given up by iperf3 itself.
func iperfTCP(from *testbed.Namespace, port, secs, omit int, reverse bool) (float64, error) {
	args := []string{"iperf3", "-c", testbed.ServerAddr, "-p", fmt.Sprint(port), "-t", fmt.Sprint(secs), "-O", fmt.Sprint(omit), "-J"}
	if reverse {
		args = append(args, "-R")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs+120)*time.Second)
	defer cancel()
	out, err := from.Run(ctx, args[0], args[1:]...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w\n%s", strings.Join(args, " "), err, out)
	}
	var res struct {
		End struct {
			SumReceived struct {
				BitsPerSecond float64 `json:"bits_per_second"`
			} `json:"sum_received"`
		} `json:"end"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return 0, fmt.Errorf("iperf3 output: %w\n%s", err, out)
	}
	return res.End.SumReceived.BitsPerSecond, nil
}

// parallel runs the transfers at the same time and returns their throughputs in order. It never calls
// FailNow from a goroutine of its own (that would leave the wait below hanging until the test binary times
// out): a failed transfer is an error of the result. If any transfer failed as a tool, all of them are
// repeated once after the queues of the first round have drained (the flakiness policy of plan §4.3 applies
// to the tool as well as to the measurement); a second failure fails the test.
func parallel(t *testing.T, transfers ...func() (float64, error)) []float64 {
	t.Helper()
	for attempt := 1; ; attempt++ {
		out := make([]float64, len(transfers))
		errs := make([]error, len(transfers))
		var wg sync.WaitGroup
		for i, f := range transfers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i], errs[i] = f()
			}()
		}
		wg.Wait()
		var failed error
		for _, err := range errs {
			if err != nil {
				failed = err
				break
			}
		}
		if failed == nil {
			return out
		}
		if attempt == 2 {
			t.Fatalf("the transfers failed twice: %v", failed)
		}
		t.Logf("a transfer failed as a tool, repeating all of them once after the queues drained: %v", failed)
		time.Sleep(15 * time.Second)
	}
}

// Rate is per device (plan §2.4, D18, example E9): a 2 Mbit/s fault on a network gives every device of it
// its own 2 Mbit/s, not 2 Mbit/s to share. Two devices of the network transferring at the same time get
// 2 Mbit/s each (±10 %, plan §4.3), in each direction; a device of another network is not limited.
func TestAnIotRateOfTwoMbitGivesEveryDeviceOfTheNetworkItsOwnTwoMbit(t *testing.T) {
	r := startFaultLab(t, nil)
	srv := r.top.Server
	for i := 0; i < 6; i++ {
		iperfServer(t, srv, 5201+i)
	}
	const rate = 2e6
	secs, omit := 12, 4
	if !testbed.Accurate() {
		secs, omit = 10, 4
	}
	r.mustPut(admin, "target: {network: IoT}\nfault: {rate: 2Mbit}")
	r.verifyKernel()
	faults := r.e.Snapshot().Faults
	if len(faults) < 3 {
		t.Fatalf("a rate gives one fault id per device (two known devices and the shared queue): %+v", faults)
	}

	for _, dir := range []struct {
		name    string
		reverse bool
		ports   [3]int
	}{{"download", true, [3]int{5201, 5202, 5203}}, {"upload", false, [3]int{5204, 5205, 5206}}} {
		got := parallel(t,
			func() (float64, error) { return iperfTCP(r.top.A, dir.ports[0], secs, omit, dir.reverse) },
			func() (float64, error) { return iperfTCP(r.top.B, dir.ports[1], secs, omit, dir.reverse) },
			func() (float64, error) { return iperfTCP(r.top.C, dir.ports[2], secs, omit, dir.reverse) })
		t.Logf("%s, A and B at 2 Mbit/s each, C unlimited: A %.2f, B %.2f, C %.2f Mbit/s", dir.name, got[0]/1e6, got[1]/1e6, got[2]/1e6)
		// functional: neither device is starved by the other (a shared queue gives 1 Mbit/s each), both are
		// held far below what the link gives the device of the other network
		for i, name := range []string{"A", "B"} {
			if got[i] < 1.4e6 || got[i] > 2.4e6 {
				t.Errorf("%s of %s: %.2f Mbit/s, want about 2", dir.name, name, got[i]/1e6)
			}
		}
		// C is in another network: the fault does not hold it. Its three transfers share the CPUs of the
		// machine, so how far above the limit it gets depends on the machine (9.5 to 37 Mbit/s were seen
		// under emulation); three times the limit is "not held to it", ten times is what a native machine
		// gives with room to spare
		floor := 3 * rate
		if testbed.Accurate() {
			floor = 10 * rate
		}
		if got[2] < floor {
			t.Errorf("%s of C, which the fault does not name: %.2f Mbit/s, want at least %.0f", dir.name, got[2]/1e6, floor/1e6)
		}
		if testbed.Accurate() {
			for i, name := range []string{"A", "B"} {
				if !within10(got[i], rate) {
					t.Errorf("%s of %s: %.3f Mbit/s, not within ±10 %% of 2 Mbit/s (plan §4.3)", dir.name, name, got[i]/1e6)
				}
			}
		}
	}
}

// within10 is the plan's rate tolerance: ±10 % of the limit. The throughput iperf3 reports is the TCP
// payload; netem's rate counts the IP packet, so a 2 Mbit/s limit shows as 1.93 Mbit/s of payload
// (1448 of 1500 bytes), which is inside the tolerance.
func within10(got, want float64) bool { return got >= 0.9*want && got <= 1.1*want }

// burstScript sends n datagrams of 1000 bytes back to back: more than any queue of 20 packets holds and
// fewer than the default one of 1000, at a rate the machine cannot affect (a paced flood is only as steady
// as the sender's timer, which under emulation is not).
const burstScript = `import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
for i in range(int(sys.argv[2])):
    s.sendto(b"x" * 1000, (sys.argv[1], 9))
`

// An explicit queue limit is a small buffer: a device that sends more than its rate allows has its
// packets wait at most limit × packet size / rate, and the rest is dropped at the tail. The same fault
// without a limit buffers the default (computed) limit, which is seconds of delay at this rate. A burst of
// 300 datagrams goes into a 1 Mbit/s fault while probes run through it: with a limit of 20 the queue never
// holds more, drops what does not fit and a probe waits 165 ms at most (20 × 1000 bytes at 1 Mbit/s); with
// the computed limit all 300 are queued and a probe waits behind them for 2.4 s.
func TestAnExplicitQueueLimitBoundsTheDelayAndDropsTheRest(t *testing.T) {
	r := startFaultLab(t, nil)
	a, _, _ := serverFlows(t, r.top)

	// phase runs probes through the fault, sends the burst 300 ms into the run and returns the probes
	phase := func() testbed.ProbeResult {
		runDone := make(chan testbed.ProbeResult, 1)
		go func() {
			runDone <- a.run(t, testbed.ProbeOptions{Count: 60, Interval: 50 * time.Millisecond, Settle: 6 * time.Second})
		}()
		time.Sleep(300 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if out, err := r.top.A.Run(ctx, "python3", "-c", burstScript, testbed.ServerAddr, "300"); err != nil {
			t.Fatalf("the burst: %v\n%s", err, out)
		}
		return <-runDone
	}

	// 1 Mbit/s and 20 packets of about 1000 bytes: at most 165 ms of queue
	o := r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {rate: 1Mbit, queue_limit: 20}}")
	r.verifyKernel()
	f := faultOfOverlay(t, r.e.Snapshot(), o.Overlay)
	limited := phase()
	_, drops, _, _ := r.queueSum(f, compiler.Upload)
	t.Logf("with queue_limit 20 at 1 Mbit/s: %s; the queue dropped %d packets; slowest probe %v", limited, drops, maxDelay(limited.Up))
	if drops < 200 {
		t.Errorf("a burst of 300 datagrams into a queue of 20 dropped %d at its tail, want at least 200", drops)
	}
	if limited.Delivered == 0 {
		t.Fatalf("no probe got through: %s", limited)
	}
	// 165 ms and the noise of the machine; never the seconds of a deep queue
	if worst := maxDelay(limited.Up); worst > 600*time.Millisecond {
		t.Errorf("a probe waited %v behind a burst in a queue of 20 packets at 1 Mbit/s", worst)
	}

	// no explicit limit: the computed one (1000 packets) takes the burst in and holds it for seconds
	r.mustPut(admin, "target: {device: dev-a}\nfault: {upload: {rate: 1Mbit}}")
	r.verifyKernel()
	f = faultOfOverlay(t, r.e.Snapshot(), o.Overlay)
	droppedBefore := drops // the queue is the same one (same fault, parameters changed in place): its counters go on
	unlimited := phase()
	_, drops, _, _ = r.queueSum(f, compiler.Upload)
	t.Logf("without a limit: %s; the queue dropped %d packets; slowest probe %v", unlimited, drops-droppedBefore, maxDelay(unlimited.Up))
	if drops != droppedBefore {
		t.Errorf("a queue of 1000 dropped %d of a burst of 300", drops-droppedBefore)
	}
	if worst := maxDelay(unlimited.Up); worst < time.Second {
		t.Errorf("the slowest probe waited %v behind 300 datagrams at 1 Mbit/s, which take 2.4 s", worst)
	}
}

func maxDelay(ds []time.Duration) time.Duration {
	var m time.Duration
	for _, d := range ds {
		m = max(m, d)
	}
	return m
}
