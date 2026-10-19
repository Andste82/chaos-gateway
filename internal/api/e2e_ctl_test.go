//go:build testbed

package api_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M18 on a real kernel: chaosctl, run in-process with a token of the scope overlays, against the gateway of the
// namespace testbed. The commands change the kernel of the gateway, the tests look at the wire from the namespace of
// the device (the ping through the delay, the scripted device that keeps a connection to the broker) and at the
// kernel after chaosctl has ended, whatever way it ended.

// outageScenario is the scenario of plan §2.10 for the device client-a: the broker's port is cut for 4 s (resetting
// what is established) and restored; the device is expected to come back within `within`.
func outageScenario(within string) string {
	return fmt.Sprintf(`
name: outage
target: {device: %s}
steps:
  - {id: cut, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883], cut_existing: true, destination: {cidr: %s/32}}}
  - {id: restore, at: 5s, restore: true}
checks:
  - {name: back at the broker, reconnected: {protocol: tcp, ports: [8883]}, window: {from: restore, within: %s}}
  - {name: nothing while cut, no_connection: {protocol: tcp, ports: [8883]}, window: {from: cut, until: restore}}
`, clientA, testbed.ServerAddr, within)
}

// `chaosctl with` puts the delay on the path while its command runs and takes it away when the command ends with an
// error, and when the command does not exist.
func TestChaosctlWithDelaysThePathAndCleansUpAfterAFailingCommandOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	script := `touch ` + started + `; while [ ! -f ` + release + ` ]; do sleep 0.05; done; exit 3`
	j := g.startCtl(nil, "with", "--device", clientA, "--latency", "120ms", "--to", testbed.ServerAddr, "--ttl", "5m", "--", "sh", "-c", script)
	eventuallyTrue(t, "the command to start", func() bool { _, err := os.Stat(started); return err == nil })

	if !kernelHasFaultChain(top) {
		t.Fatalf("the fault is not in the kernel while the command runs:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 || res.Median() < 100*time.Millisecond {
		t.Errorf("under `chaosctl with` the median is %v and the loss %v", res.Median(), res.Loss())
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := j.wait(t); r.code != 3 {
		t.Fatalf("the exit code of the command is the result: %+v", r)
	}
	if kernelHasFaultChain(top) {
		t.Errorf("the kernel keeps the fault after the failed command:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 || res.Median() > 60*time.Millisecond {
		t.Errorf("after the command the median is %v and the loss %v", res.Median(), res.Loss())
	}

	if r := g.ctl("with", "--device", clientA, "--latency", "120ms", "--", "chaosctl-no-such-command"); r.code != 127 || kernelHasFaultChain(top) {
		t.Errorf("a command that is not found: %+v", r)
	}
}

// A run from a file with a device that comes back passes: exit code 0, the JUnit file, the kernel clean afterwards.
func TestChaosctlRunPassesForADeviceThatReconnectsOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	device := top.A.Start("python3", "-c", deviceScript, testbed.ServerAddr, "8883")
	waitOutput(t, device, "connected", 30*time.Second)
	file := writeFile(t, "outage.yaml", outageScenario("30s"))
	junit := filepath.Join(t.TempDir(), "junit.xml")

	r := g.startCtl(nil, "run", "-f", file, "--wait", "--junit", junit).wait(t)
	if r.code != 0 || !strings.Contains(r.out, "PASSED") {
		t.Fatalf("%+v\ndevice: %s", r, device.Output())
	}
	b, err := os.ReadFile(junit)
	if err != nil || !strings.Contains(string(b), `failures="0"`) || !strings.Contains(string(b), "back at the broker") {
		t.Errorf("the JUnit file: %v\n%s", err, b)
	}
	if n := len(overlayIDs(t, g)); n != 0 || kernelHasFaultChain(top) {
		t.Errorf("%d overlays are left", n)
	}
}

// A device that gives up fails the check: exit code 1, the failure in the JUnit file.
func TestChaosctlRunFailsForADeviceThatDoesNotReconnectOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	device := top.A.Start("python3", "-c", deviceScript, testbed.ServerAddr, "8883")
	waitOutput(t, device, "connected", 30*time.Second)
	file := writeFile(t, "outage.yaml", outageScenario("6s"))
	junit := filepath.Join(t.TempDir(), "junit.xml")

	j := g.startCtl(nil, "run", "-f", file, "--wait", "--junit", junit)
	// the cut is a rule, not a fault: it has no chain mark_<id>, the port of the broker is in the table while it is on
	eventuallyTrue(t, "the cut", func() bool {
		return strings.Contains(top.GW.Must("nft", "list", "table", "inet", "chaosgw"), "8883")
	})
	device.Stop() // the device gives up
	r := j.wait(t)
	if r.code != 1 || !strings.Contains(r.out, "FAILED") || !strings.Contains(r.out, "back at the broker: failed") {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(junit); !strings.Contains(string(b), "<failure") {
		t.Errorf("the JUnit file has no failure:\n%s", b)
	}
	if n := len(overlayIDs(t, g)); n != 0 || kernelHasFaultChain(top) {
		t.Errorf("%d overlays are left", n)
	}
}

// A terminate signal during a run (a cancelled CI job): the run is aborted, exit code 2, and the fault is out of the
// kernel when chaosctl returns.
func TestChaosctlAbortsARunOnASignalAndTakesTheFaultOutOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	file := writeFile(t, "long.yaml", fmt.Sprintf("name: long\ntarget: {device: %s}\nsteps:\n  - {id: slow, at: 0s, fault: {latency: 120ms, destination: {cidr: %s/32}}}\n  - {id: end, at: 1h, restore: true}\n", clientA, testbed.ServerAddr))
	j := g.startCtl(nil, "run", "-f", file, "--wait")
	eventuallyTrue(t, "the fault to be on", func() bool { return kernelHasFaultChain(top) })
	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Median() < 100*time.Millisecond {
		t.Fatalf("the fault is not on: %v", res.Median())
	}
	j.sigs <- syscall.SIGTERM
	r := j.wait(t)
	if r.code != 2 || !strings.Contains(r.out, "ABORTED") {
		t.Fatalf("%+v", r)
	}
	if kernelHasFaultChain(top) {
		t.Errorf("the kernel keeps the fault of the aborted run:\n%s", top.GW.Must("nft", "list", "table", "inet", "chaosgw"))
	}
	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 || res.Median() > 60*time.Millisecond {
		t.Errorf("after the abort the median is %v and the loss %v", res.Median(), res.Loss())
	}
}

// `chaosctl wait device` returns when the device is online and holds its connection to the broker, and times out (1)
// for a connection it does not have.
func TestChaosctlWaitsForADeviceAndItsConnectionOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	if r := g.ctl("wait", "device", clientA, "--online", "--connected", "tcp/8883", "--timeout", "3s"); r.code != 1 {
		t.Errorf("before the device connects: %+v", r)
	}
	device := top.A.Start("python3", "-c", deviceScript, testbed.ServerAddr, "8883")
	j := g.startCtl(nil, "wait", "device", clientA, "--online", "--connected", "tcp/"+testbed.ServerAddr+":8883", "--timeout", "60s")
	waitOutput(t, device, "connected", 30*time.Second)
	if r := j.wait(t); r.code != 0 || !strings.Contains(r.err, "has a connection") {
		t.Errorf("%+v\ndevice: %s", r, device.Output())
	}
	if r := g.ctl("wait", "device", clientA, "--connected", "tcp/9999", "--timeout", "2s"); r.code != 1 {
		t.Errorf("a connection the device does not have: %+v", r)
	}
}

// A fault set with a lease that nobody renews (the CI job died) is taken out of the kernel by the gateway.
func TestAFaultWhoseLeaseIsNotRenewedLeavesTheRealKernelOnItsOwn(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	r := g.ctl("fault", "set", "--device", clientA, "--latency", "120ms", "--to", testbed.ServerAddr, "--lease", "3s")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !kernelHasFaultChain(top) {
		t.Fatal("the fault is not in the kernel")
	}
	eventuallyTrue(t, "the lease to run out", func() bool { return len(overlayIDs(t, g)) == 0 && !kernelHasFaultChain(top) })
}
