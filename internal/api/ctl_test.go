package api_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/ctl"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The tests that move the fake clock of the API as fast as the machine goes run with `--lease 0`: the lease is on that
// clock, the renewals are on the real one (TestChaosctlRenewsTheLeaseOfTheRunItWaitsFor paces both).
//
// M18: chaosctl against the API on the simulated kernel with the real engine. The CLI is run in-process (package
// ctl) with the gateway's address and an admin or scoped token in its environment, exactly as a CI job would, and
// the tests look at the exit code, the output, the files it writes and what the gateway has afterwards.

type ctlOut struct {
	code     int
	out, err string
}

// ctlBuf is a buffer a goroutine writes while a test reads it.
type ctlBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (c *ctlBuf) Write(p []byte) (int, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.b.Write(p) }
func (c *ctlBuf) String() string              { c.mu.Lock(); defer c.mu.Unlock(); return c.b.String() }

// ctlJob is a chaosctl that runs in the background.
type ctlJob struct {
	out, err *ctlBuf
	sigs     chan os.Signal
	done     chan int
}

func (g *gw) startCtl(env func(*ctl.Env), args ...string) *ctlJob {
	g.t.Helper()
	j := &ctlJob{out: &ctlBuf{}, err: &ctlBuf{}, sigs: make(chan os.Signal, 4), done: make(chan int, 1)}
	vars := map[string]string{"CHAOSGW_URL": g.ts.URL, "CHAOSGW_TOKEN": g.token}
	e := ctl.Env{Stdout: j.out, Stderr: j.err, Signals: j.sigs, Poll: 10 * time.Millisecond,
		Getenv: func(k string) string { return vars[k] }}
	if env != nil {
		env(&e)
	}
	go func() { j.done <- ctl.Run(args, e) }()
	return j
}

// wait returns the result of a job that ends by itself.
func (j *ctlJob) wait(t *testing.T) ctlOut {
	t.Helper()
	select {
	case code := <-j.done:
		return ctlOut{code, j.out.String(), j.err.String()}
	case <-time.After(2 * time.Minute):
		t.Fatalf("chaosctl does not end\nstdout: %s\nstderr: %s", j.out.String(), j.err.String())
		return ctlOut{}
	}
}

// ctl runs chaosctl and waits for it.
func (g *gw) ctl(args ...string) ctlOut {
	g.t.Helper()
	return g.startCtl(nil, args...).wait(g.t)
}

// advanceUntil moves the fake clock of the API until the job has ended; pace is the real time between two steps of
// step fake time (0: as fast as the machine goes).
func (g *gw) advanceUntil(clk *clock.Fake, j *ctlJob, step, pace time.Duration) ctlOut {
	g.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		select {
		case code := <-j.done:
			return ctlOut{code, j.out.String(), j.err.String()}
		default:
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("chaosctl does not end\nstdout: %s\nstderr: %s", j.out.String(), j.err.String())
		}
		clk.Advance(step)
		if pace == 0 {
			pace = 2 * time.Millisecond
		}
		time.Sleep(pace)
	}
}

func writeFile(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ctlGateway is a gateway with the configured device esp32-42 on a fake clock (the one of the checks' tests).
func ctlGateway(t *testing.T) (*gw, *clock.Fake, string) {
	g, clk, service := checksGateway(t)
	return g, clk, service
}

const ciScenario = `
name: ci-slow
target: {device: placeholder}
preconditions: {skip: [target_online]}
steps:
  - {id: slow, at: 0s, fault: {latency: 100ms}}
  - {id: cut, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: end, at: 2s, restore: true}
`

func overlayIDs(t *testing.T, g *gw) []string {
	t.Helper()
	var ids []string
	for _, o := range g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any) {
		ids = append(ids, o.(map[string]any)["id"].(string))
	}
	return ids
}

// A profile and a fault are applied with a TTL and a lease, the id is the only thing on standard output, and the
// overlay can be listed and deleted again; deleting what is gone already is not an error.
func ctlAppliesAProfileAndAFaultWithATTLAndRemovesThem(t *testing.T, g *gw, clk *clock.Fake) {
	r := g.ctl("profile", "apply", "bad-lte", "--device", "esp32-42", "--ttl", "5m", "--lease", "10m")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	pid := strings.TrimSpace(r.out)
	ov := g.do("GET", "/overlays/"+pid, nil, nil, nil).json(t)
	if ov["kind"] != "profile" || ov["profile"] != "b9b6e3e5-b89a-5b25-b7af-b1ac017bda16" || ov["ttl"] != "5m" || ov["lease"] != "10m" || ov["target"].(map[string]any)["device"] != espID || ov["expires_at"] == nil {
		t.Errorf("the profile overlay: %v", ov)
	}

	r = g.ctl("fault", "set", "--device", "esp32-42", "--latency", "200ms", "--jitter", "50ms", "--loss", "5%", "--protocol", "tcp", "--port", "8883", "--to", "uplink", "--ttl", "2m", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	fid := ""
	for _, id := range overlayIDs(t, g) {
		if id != pid {
			fid = id
		}
	}
	f := g.do("GET", "/overlays/"+fid, nil, nil, nil).json(t)["fault"].(map[string]any)
	if f["latency"] != "200ms" || f["jitter"] != "50ms" || f["loss"] != "5%" || f["protocol"] != "tcp" || f["destination"].(map[string]any)["uplink"] != true {
		t.Errorf("the fault: %v", f)
	}
	if !strings.Contains(r.out, fid) || !strings.HasPrefix(strings.TrimSpace(r.out), "{") {
		t.Errorf("--json prints the overlay: %s", r.out)
	}

	r = g.ctl("overlay", "list")
	if r.code != 0 || !strings.Contains(r.out, pid) || !strings.Contains(r.out, fid) || !strings.Contains(r.out, "profile") || !strings.Contains(r.out, "fault") {
		t.Errorf("%+v", r)
	}

	r = g.ctl("overlay", "delete", pid, fid)
	if r.code != 0 || len(overlayIDs(t, g)) != 0 {
		t.Fatalf("%+v, left: %v", r, overlayIDs(t, g))
	}
	if r = g.ctl("overlay", "delete", pid); r.code != 0 || !strings.Contains(r.err, "gone already") {
		t.Errorf("deleting an overlay twice: %+v", r)
	}
}

// explain shows the verdict and the winning fault of the real engine for a device towards a destination, and the same as
// the API's JSON.
func ctlExplainsTheTrafficOfADevice(t *testing.T, g *gw, clk *clock.Fake) {
	id := strings.TrimSpace(g.ctl("fault", "set", "--device", "esp32-42", "--latency", "200ms", "--protocol", "tcp", "--port", "8883", "--to", "uplink", "--ttl", "5m").out)
	defer g.ctl("overlay", "delete", id)
	r := g.ctl("explain", "--device", "esp32-42", "--dst", "203.0.113.10", "--protocol", "tcp", "--port", "8883")
	if r.code != 0 || !strings.Contains(r.out, "esp32-42 (10.10.0.31, network IoT) -> 203.0.113.10 tcp/8883") || !strings.Contains(r.out, "access: ALLOW") ||
		!strings.Contains(r.out, "impairment: ") || !strings.Contains(r.out, "[overlay]") {
		t.Errorf("%+v", r)
	}
	// traffic the fault does not select has no impairment winner
	if r = g.ctl("explain", "--src", "10.10.0.31", "--dst", "203.0.113.10", "--protocol", "udp", "--port", "53"); r.code != 0 || strings.Contains(r.out, "[overlay]") {
		t.Errorf("%+v", r)
	}
	j := g.ctl("explain", "--device", "esp32-42", "--dst", "203.0.113.10", "--protocol", "tcp", "--port", "8883", "--json").out
	api := g.do("GET", "/explain?device=esp32-42&dst=203.0.113.10&protocol=tcp&port=8883", nil, nil, nil).json(t)
	var got map[string]any
	if err := json.Unmarshal([]byte(j), &got); err != nil || !reflect.DeepEqual(got["faults"], api["faults"]) || !reflect.DeepEqual(got["access"], api["access"]) {
		t.Errorf("--json is the API's answer: %s vs %v", j, api)
	}
	if r = g.ctl("explain", "--device", "nobody", "--dst", "203.0.113.10"); r.code != 2 || !strings.Contains(r.err, "404") {
		t.Errorf("an unknown device: %+v", r)
	}
}

// An overlay with a lease is renewed with `overlay renew`; the lease that is not renewed ends the overlay.
func ctlRenewsTheLeaseOfAnOverlayItDoesNotKeepAlive(t *testing.T, g *gw, clk *clock.Fake) {
	id := strings.TrimSpace(g.ctl("fault", "set", "--device", "esp32-42", "--latency", "50ms", "--lease", "1s").out)
	for i := 0; i < 5; i++ {
		time.Sleep(400 * time.Millisecond)
		if r := g.ctl("overlay", "renew", id); r.code != 0 {
			t.Fatalf("renewal %d: %+v", i, r)
		}
	}
	if g.do("GET", "/overlays/"+id, nil, nil, nil).Status != 200 {
		t.Fatal("the overlay ended although it was renewed for twice its lease")
	}
	eventuallyTrue(t, "the lease to run out", func() bool { return g.do("GET", "/overlays/"+id, nil, nil, nil).Status == 404 })
	if r := g.ctl("overlay", "renew", id); r.code != 2 || !strings.Contains(r.err, "404") {
		t.Errorf("renewing an overlay that is gone: %+v", r)
	}
}

// reset removes the caller's overlays; the same fault set again replaces the earlier overlay.
func ctlResetAndTheReplacementOfAFault(t *testing.T, g *gw, clk *clock.Fake) {
	a := strings.TrimSpace(g.ctl("fault", "set", "--network", "IoT", "--latency", "50ms").out)
	b := strings.TrimSpace(g.ctl("fault", "set", "--network", "IoT", "--latency", "80ms", "--jitter", "10ms").out)
	if a == "" || a != b {
		t.Errorf("the same target and match replace the fault and keep its id: %q %q", a, b)
	}
	if f := g.do("GET", "/overlays/"+a, nil, nil, nil).json(t)["fault"].(map[string]any); f["latency"] != "80ms" || f["jitter"] != "10ms" {
		t.Errorf("%v", f)
	}
	g.ctl("profile", "apply", "offline", "--global")
	r := g.ctl("reset")
	if r.code != 0 || !strings.Contains(r.out, "removed 2 overlays") || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v", r)
	}
}

// An overlay from a request file takes the target and the TTL of the flags; it can be a kind the flags do not know.
func ctlCreatesAnOverlayFromARequestFile(t *testing.T, g *gw, clk *clock.Fake) {
	f := writeFile(t, "cut.yaml", "target: {device: esp32-42}\nrule: {action: drop, protocol: tcp, ports: [8883]}\n")
	r := g.ctl("overlay", "create", "-f", f, "--network", "IoT", "--ttl", "1m")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	ov := g.do("GET", "/overlays/"+strings.TrimSpace(r.out), nil, nil, nil).json(t)
	if ov["kind"] != "rule" || ov["target"].(map[string]any)["network"] != iotID || ov["ttl"] != "1m" {
		t.Errorf("%v", ov)
	}
	if r := g.ctl("overlay", "create"); r.code != 2 || !strings.Contains(r.err, "-f") {
		t.Errorf("%+v", r)
	}
	if r := g.ctl("overlay", "create", "-f", filepath.Join(t.TempDir(), "missing.yaml")); r.code != 2 {
		t.Errorf("%+v", r)
	}
	g.ctl("overlay", "delete", strings.TrimSpace(r.out))
}

// What the gateway refuses is exit code 2 with the problem and its pointers; so is a missing token, a wrong token and
// a token whose scope is too small.
func ctlShowsWhatTheGatewayRefusesAndExitsWith2(t *testing.T, g *gw, clk *clock.Fake) {
	r := g.ctl("fault", "set", "--device", "esp32-42", "--latency", "10ms", "--jitter", "50ms")
	if r.code != 2 || !strings.Contains(r.err, "422 validation_failed") || !strings.Contains(r.err, "/") || !strings.Contains(r.err, "jitter") {
		t.Errorf("a jitter above the latency: %+v", r)
	}
	if r := g.ctl("fault", "set", "--device", "no-such-device", "--latency", "10ms"); r.code != 2 || !strings.Contains(r.err, "validation_failed") && !strings.Contains(r.err, "not_found") {
		t.Errorf("an unknown device: %+v", r)
	}
	if r := g.ctl("profile", "apply", "bad-lte"); r.code != 2 || !strings.Contains(r.err, "exactly one target") {
		t.Errorf("no target: %+v", r)
	}
	if r := g.ctl("profile", "apply", "bad-lte", "--device", "a", "--network", "b"); r.code != 2 {
		t.Errorf("two targets: %+v", r)
	}
	if r := g.ctl("fault", "set", "--device", "esp32-42"); r.code != 2 || !strings.Contains(r.err, "needs at least one") {
		t.Errorf("an empty fault: %+v", r)
	}
	if r := g.ctl("fault", "set", "--device", "esp32-42", "--blackout", "--ttl", "soon"); r.code != 2 || !strings.Contains(r.err, "--ttl") {
		t.Errorf("a bad ttl: %+v", r)
	}
	if r := g.ctl("nonsense"); r.code != 2 {
		t.Errorf("an unknown command: %+v", r)
	}
	if r := g.ctl("fault", "set", "--bogus"); r.code != 2 {
		t.Errorf("an unknown flag: %+v", r)
	}

	good := g.token
	g.token = ""
	if r := g.ctl("reset"); r.code != 2 || !strings.Contains(r.err, "no API token") {
		t.Errorf("no token: %+v", r)
	}
	g.token = "cg_not_a_token"
	if r := g.ctl("reset"); r.code != 2 || !strings.Contains(r.err, "401") {
		t.Errorf("a wrong token: %+v", r)
	}
	g.mintToken("read")
	if r := g.ctl("profile", "apply", "offline", "--global"); r.code != 2 || !strings.Contains(r.err, "403") {
		t.Errorf("a read-only token: %+v", r)
	}
	g.token = good
	// the gateway is not there
	r = g.startCtl(func(e *ctl.Env) {
		vars := map[string]string{"CHAOSGW_URL": "http://127.0.0.1:1", "CHAOSGW_TOKEN": "x"}
		e.Getenv = func(k string) string { return vars[k] }
	}, "reset").wait(t)
	if r.code != 2 || r.err == "" {
		t.Errorf("an unreachable gateway: %+v", r)
	}
}

// The token can come from a file, and --token-file keeps it out of the environment.
func ctlReadsTheTokenFromAFile(t *testing.T, g *gw, clk *clock.Fake) {
	f := writeFile(t, "token", g.token+"\n")
	tok := g.token
	g.token = ""
	r := g.ctl("--token-file", f, "reset")
	g.token = tok
	if r.code != 0 {
		t.Errorf("%+v", r)
	}
}

// A run from a file passes: exit 0, the steps in the progress on standard error, the result on standard output, the
// JUnit file and the artifacts in the directory; the active revision and the overlays are as before.
func ctlRunsAScenarioFromAFileAndSavesItsReports(t *testing.T, g *gw, clk *clock.Fake) {
	rev := g.activeID()
	file := writeFile(t, "ci.yaml", ciScenario)
	dir := filepath.Join(t.TempDir(), "out", "artifacts")
	junit := filepath.Join(t.TempDir(), "junit.xml")
	j := g.startCtl(nil, "run", "-f", file, "--set", "target.device=esp32-42", "--wait", "--lease", "0", "--junit", junit, "--artifacts", dir)
	r := g.advanceUntil(clk, j, 250*time.Millisecond, 0)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.out, "PASSED") || !strings.Contains(r.out, "ci-slow") {
		t.Errorf("stdout: %s", r.out)
	}
	for _, want := range []string{"run running", "step slow applied", "step cut applied", "step end applied", "run passed"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("no %q in the progress:\n%s", want, r.err)
		}
	}
	var suite struct {
		XMLName xml.Name `xml:"testsuites"`
	}
	raw, err := os.ReadFile(junit)
	if err != nil || xml.Unmarshal(raw, &suite) != nil && !strings.Contains(string(raw), "<testsuite") {
		t.Fatalf("the JUnit file: %v\n%s", err, raw)
	}
	if !strings.Contains(string(raw), `name="ci-slow"`) {
		t.Errorf("%s", raw)
	}
	for _, f := range []string{"report.json", "report.xml", "events.jsonl"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Size() == 0 {
			t.Errorf("artifact %s: %v", f, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "report.json")); !strings.Contains(string(b), `"counters"`) || !strings.Contains(string(b), `"ci-slow"`) {
		t.Errorf("report.json: %s", b)
	}
	if g.activeID() != rev || len(overlayIDs(t, g)) != 0 {
		t.Errorf("revision %d -> %d, overlays %v", rev, g.activeID(), overlayIDs(t, g))
	}
}

// A stored scenario runs by name; a run without --wait prints its id and `wait run` gives the result later.
func ctlStartsAStoredScenarioAndWaitsForItLater(t *testing.T, g *gw, clk *clock.Fake) {
	id := g.mustPatch(map[string]any{"scenarios": map[string]any{
		"5ce00001-0000-4000-8000-000000000001": map[string]any{"name": "stored", "target": map[string]any{"network": "IoT"},
			"preconditions": map[string]any{"skip": []string{"target_online"}},
			"steps":         []any{step("slow", "0s", "fault", map[string]any{"latency": "50ms"}), step("end", "1s", "restore", true)}},
	}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.ctl("run", "stored")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	runID := strings.TrimSpace(r.out)
	if len(runID) != 36 {
		t.Fatalf("stdout is the id of the run: %q", r.out)
	}
	j := g.startCtl(nil, "wait", "run", runID)
	w := g.advanceUntil(clk, j, 250*time.Millisecond, 0)
	if w.code != 0 || !strings.Contains(w.out, "PASSED") {
		t.Fatalf("%+v", w)
	}
	// both a name and a file, and neither
	if r := g.ctl("run"); r.code != 2 {
		t.Errorf("%+v", r)
	}
	if r := g.ctl("run", "stored", "-f", "x.yaml"); r.code != 2 {
		t.Errorf("%+v", r)
	}
	// the options that belong to --wait
	if r := g.ctl("run", "stored", "--junit", "x.xml"); r.code != 2 || !strings.Contains(r.err, "--wait") {
		t.Errorf("%+v", r)
	}
	if r := g.ctl("run", "ghost", "--wait"); r.code != 2 || !strings.Contains(r.err, "404") {
		t.Errorf("an unknown scenario: %+v", r)
	}
}

// A failed check is exit code 1, with the check named; the JUnit file has the failure.
func ctlReturns1ForAFailedCheck(t *testing.T, g *gw, clk *clock.Fake) {
	file := writeFile(t, "never-back.yaml", `
name: never-back
target: {device: esp32-42}
preconditions: {skip: [target_online]}
steps:
  - {id: cut, at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 1s, restore: true}
checks:
  - {name: back at the broker, reconnected: {protocol: tcp, ports: [8883]}, window: {from: restore, within: 3s}}
`)
	g.k.SetConntrack(ct("ESTABLISHED", 40002, 8883, "203.0.113.10", 8)) // there before the window: no reconnection
	junit := filepath.Join(t.TempDir(), "junit.xml")
	r := g.advanceUntil(clk, g.startCtl(nil, "run", "-f", file, "--wait", "--lease", "0", "--junit", junit), 250*time.Millisecond, 0)
	if r.code != 1 || !strings.Contains(r.out, "FAILED") || !strings.Contains(r.out, "check back at the broker: failed") {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(junit); !strings.Contains(string(b), "<failure") {
		t.Errorf("the JUnit file has no failure:\n%s", b)
	}
}

// A run that ends as error (here: the precondition of an offline device) is exit code 2, with the reason.
func ctlReturns2ForARunThatEndsAsError(t *testing.T, g *gw, clk *clock.Fake) {
	g.k.SetConntrack("") // a connection counts as a sign of life
	g.k.SetNeighbors(nil)
	g.observe()
	file := writeFile(t, "offline.yaml", "name: offline-device\ntarget: {device: esp32-42}\nsteps:\n  - {id: x, at: 0s, fault: {latency: 10ms}}\n")
	r := g.advanceUntil(clk, g.startCtl(nil, "run", "-f", file, "--wait", "--lease", "0"), 250*time.Millisecond, 0)
	if r.code != 2 || !strings.Contains(r.out, "ERROR") || !strings.Contains(r.out, "precondition target_online failed") {
		t.Fatalf("%+v", r)
	}
	if n := len(overlayIDs(t, g)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")}) // the device is back for the next subtest
	g.observe()
}

// The scenario is refused (a path of --set that names nothing): the request's problem, exit code 2, no run.
func ctlReportsAScenarioTheGatewayRefuses(t *testing.T, g *gw, clk *clock.Fake) {
	before := len(g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any))
	file := writeFile(t, "ci.yaml", ciScenario)
	r := g.ctl("run", "-f", file, "--set", "target.device=esp32-42", "--set", "steps.nonexistent.at=1s", "--wait")
	if r.code != 2 || !strings.Contains(r.err, "validation_failed") || !strings.Contains(r.err, "/parameters/steps.nonexistent.at") {
		t.Errorf("%+v", r)
	}
	if r := g.ctl("run", "-f", file, "--set", "novalue", "--wait"); r.code != 2 || !strings.Contains(r.err, "key=value") {
		t.Errorf("%+v", r)
	}
	if after := len(g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any)); after != before {
		t.Errorf("a refused scenario started a run: %d runs before, %d after", before, after)
	}
}

// --timeout aborts the run and takes its overlays away; the exit code is 2.
func ctlAbortsTheRunWhenTheTimeoutRunsOut(t *testing.T, g *gw, clk *clock.Fake) {
	file := writeFile(t, "long.yaml", "name: long\ntarget: {device: esp32-42}\npreconditions: {skip: [target_online]}\nsteps:\n  - {id: slow, at: 0s, fault: {latency: 50ms}}\n  - {id: end, at: 1h, restore: true}\n")
	j := g.startCtl(nil, "run", "-f", file, "--wait", "--timeout", "300ms")
	r := j.wait(t)
	if r.code != 2 || !strings.Contains(r.out, "ABORTED") || !strings.Contains(r.err, "--timeout") {
		t.Fatalf("%+v", r)
	}
	if n := len(overlayIDs(t, g)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// An interrupt aborts the run too (a cancelled CI job); waiting for someone else's run only stops waiting.
func ctlAbortsItsRunOnASignalAndLeavesAnotherRunAlone(t *testing.T, g *gw, clk *clock.Fake) {
	file := writeFile(t, "long.yaml", "name: long\ntarget: {device: esp32-42}\npreconditions: {skip: [target_online]}\nsteps:\n  - {id: slow, at: 0s, fault: {latency: 50ms}}\n  - {id: end, at: 1h, restore: true}\n")
	j := g.startCtl(nil, "run", "-f", file, "--wait")
	eventuallyTrue(t, "the fault to be on", func() bool { return len(overlayIDs(t, g)) == 1 })
	j.sigs <- syscall.SIGTERM
	r := j.wait(t)
	if r.code != 2 || !strings.Contains(r.out, "ABORTED") {
		t.Fatalf("%+v", r)
	}
	if n := len(overlayIDs(t, g)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}

	// another run, started without --wait: `wait run` gives up on a signal and leaves the run going
	id := strings.TrimSpace(g.ctl("run", "-f", file).out)
	w := g.startCtl(nil, "wait", "run", id)
	eventuallyTrue(t, "the second run to apply its fault", func() bool { return len(overlayIDs(t, g)) == 1 })
	w.sigs <- syscall.SIGINT
	if r := w.wait(t); r.code != 2 || !strings.Contains(r.err, "goes on") {
		t.Errorf("%+v", r)
	}
	if st := g.run(id)["status"]; st != "running" {
		t.Errorf("the run was %v after a wait was interrupted", st)
	}
	if r := g.ctl("abort", id); r.code != 0 || !strings.Contains(r.out, "aborted") {
		t.Errorf("%+v", r)
	}
}

// The lease of a run that is waited for is renewed by the CLI: a 1 s lease on a clock that runs in real time
// outlives a run of 3 s. Without the renewals the gateway would have aborted it.
func ctlRenewsTheLeaseOfTheRunItWaitsFor(t *testing.T, g *gw, clk *clock.Fake) {
	file := writeFile(t, "leased.yaml", "name: leased\ntarget: {device: esp32-42}\npreconditions: {skip: [target_online]}\nsteps:\n  - {id: slow, at: 0s, fault: {latency: 50ms}}\n  - {id: end, at: 3s, restore: true}\n")
	j := g.startCtl(nil, "run", "-f", file, "--wait", "--lease", "1s")
	r := g.advanceUntil(clk, j, 50*time.Millisecond, 50*time.Millisecond)
	if r.code != 0 || !strings.Contains(r.out, "PASSED") {
		t.Fatalf("%+v", r)
	}
	// and the default lease is on a run that is waited for, none on one that is not
	j = g.startCtl(nil, "run", "-f", file, "--wait")
	var waited map[string]any
	eventuallyTrue(t, "the second run to start", func() bool {
		for _, r := range g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any) {
			if r.(map[string]any)["status"] == "running" {
				waited = g.run(r.(map[string]any)["id"].(string))
				return true
			}
		}
		return false
	})
	if waited["lease_expires_at"] == nil {
		t.Errorf("a run that is waited for has a lease: %v", waited)
	}
	j.sigs <- syscall.SIGTERM
	j.wait(t)
	id := strings.TrimSpace(g.ctl("run", "-f", file).out)
	if g.run(id)["lease_expires_at"] != nil {
		t.Errorf("a run that nobody waits for has no lease")
	}
	g.ctl("abort", id)
	if r := g.ctl("run", "-f", file, "--lease", "5s"); r.code != 2 {
		t.Errorf("a lease needs --wait: %+v", r)
	}
}

// A run on a busy target queues, and --no-queue fails with target_busy.
func ctlQueuesBehindARunAndNoQueueRefuses(t *testing.T, g *gw, clk *clock.Fake) {
	file := writeFile(t, "long.yaml", "name: long\ntarget: {device: esp32-42}\npreconditions: {skip: [target_online]}\nsteps:\n  - {id: slow, at: 0s, fault: {latency: 50ms}}\n  - {id: end, at: 1h, restore: true}\n")
	first := strings.TrimSpace(g.ctl("run", "-f", file).out)
	if r := g.ctl("run", "-f", file, "--no-queue"); r.code != 2 || !strings.Contains(r.err, "target_busy") {
		t.Errorf("%+v", r)
	}
	second := strings.TrimSpace(g.ctl("run", "-f", file).out)
	if st := g.run(second)["status"]; st != "queued" {
		t.Errorf("the second run is %v", st)
	}
	g.ctl("abort", second)
	g.ctl("abort", first)
}

// wait device: the device is online after a neighbor entry is seen, and has its connection once conntrack lists one;
// the wait times out with 1 and says which condition is missing.
func ctlWaitsForADeviceToBeOnlineAndConnected(t *testing.T, g *gw, clk *clock.Fake) {
	// esp32-42 is online (checksGateway observed its neighbor entry) and has no connection
	r := g.ctl("wait", "device", "esp32-42", "--online", "--timeout", "2s")
	if r.code != 0 || !strings.Contains(r.out, "ready") {
		t.Fatalf("%+v", r)
	}
	r = g.ctl("wait", "device", "esp32-42", "--online", "--connected", "tcp/8883", "--timeout", "300ms")
	if r.code != 1 || !strings.Contains(r.err, "no established connection tcp/8883") {
		t.Errorf("without a connection: %+v", r)
	}
	// a connection that is still in its handshake does not count
	g.k.SetConntrack(ct("SYN_SENT", 40001, 8883, "203.0.113.10", 1))
	if r := g.ctl("wait", "device", "esp32-42", "--connected", "tcp/8883", "--timeout", "300ms"); r.code != 1 {
		t.Errorf("a handshake: %+v", r)
	}
	// the connection appears while the CLI waits
	j := g.startCtl(nil, "wait", "device", "esp32-42", "--online", "--connected", "tcp/8883", "--timeout", "1m")
	time.Sleep(100 * time.Millisecond)
	g.k.SetConntrack(ct("ESTABLISHED", 40002, 8883, "203.0.113.10", 8))
	if r := j.wait(t); r.code != 0 || !strings.Contains(r.err, "has a connection tcp/8883") {
		t.Errorf("%+v", r)
	}
	if r := g.ctl("wait", "device", "esp32-42", "--connected", "tcp/203.0.113.10:8883", "--timeout", "1s"); r.code != 0 {
		t.Errorf("the connection with the peer's address: %+v", r)
	}
	if r := g.ctl("wait", "device", "esp32-42", "--connected", "tcp/10.9.9.9:8883", "--timeout", "300ms"); r.code != 1 {
		t.Errorf("another peer: %+v", r)
	}
	// a device the gateway does not know: waited for until the time runs out
	if r := g.ctl("wait", "device", "ghost", "--online", "--timeout", "300ms"); r.code != 1 || !strings.Contains(r.err, "does not know the device") {
		t.Errorf("%+v", r)
	}
	for _, bad := range [][]string{{"wait", "device", "esp32-42"}, {"wait", "device", "esp32-42", "--connected", "icmp/1"}, {"wait", "device", "esp32-42", "--connected", "tcp/99999"}} {
		if r := g.ctl(bad...); r.code != 2 {
			t.Errorf("%v: %+v", bad, r)
		}
	}
}

// A device that is not online is waited for and found when it appears.
func ctlWaitsForADeviceThatComesOnline(t *testing.T, g *gw, clk *clock.Fake) {
	g.k.SetConntrack("") // a connection counts as a sign of life
	g.k.SetNeighbors(nil)
	g.observe()
	if r := g.ctl("wait", "device", "esp32-42", "--online", "--timeout", "300ms"); r.code != 1 || !strings.Contains(r.err, "not online") {
		t.Fatalf("%+v", r)
	}
	j := g.startCtl(nil, "wait", "device", "esp32-42", "--online", "--timeout", "1m")
	time.Sleep(100 * time.Millisecond)
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	if r := j.wait(t); r.code != 0 {
		t.Errorf("%+v", r)
	}
}

// with: the command runs under the fault, whatever its exit code is comes back, and the overlay is gone after it.
func ctlWithRunsACommandUnderAFaultAndCleansUp(t *testing.T, g *gw, clk *clock.Fake) {
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	script := `touch ` + started + `; while [ ! -f ` + release + ` ]; do sleep 0.02; done; echo "overlays=$CHAOSCTL_OVERLAYS"; exit 3`
	j := g.startCtl(nil, "with", "--device", "esp32-42", "--profile", "bad-lte", "--latency", "10ms", "--ttl", "10m", "--", "sh", "-c", script)
	eventuallyTrue(t, "the command to start", func() bool { _, err := os.Stat(started); return err == nil })
	ovs := g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)
	if len(ovs) != 2 {
		t.Fatalf("overlays while the command runs: %v", ovs)
	}
	kinds := map[string]bool{}
	for _, o := range ovs {
		m := o.(map[string]any)
		kinds[m["kind"].(string)] = true
		if m["lease"] != "30s" || m["ttl"] != "10m" {
			t.Errorf("an overlay of `with` has a lease and the TTL: %v", m)
		}
	}
	if !kinds["profile"] || !kinds["fault"] {
		t.Errorf("%v", kinds)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := j.wait(t)
	if r.code != 3 {
		t.Fatalf("the command's exit code is the result: %+v", r)
	}
	if !strings.Contains(r.out, "overlays="+ovs[0].(map[string]any)["id"].(string)) && !strings.Contains(r.out, "overlays="+ovs[1].(map[string]any)["id"].(string)) {
		t.Errorf("the command knows the ids of the overlays: %s", r.out)
	}
	if n := len(overlayIDs(t, g)); n != 0 {
		t.Errorf("%d overlays are left after a command that failed", n)
	}
}

// with: a command that succeeds gives 0; one that is not found 127, one that is not executable 126; the overlays are
// removed each time.
func ctlWithCleansUpWhenTheCommandCannotRun(t *testing.T, g *gw, clk *clock.Fake) {
	if r := g.ctl("with", "--network", "IoT", "--blackout", "--", "true"); r.code != 0 || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v", r)
	}
	if r := g.ctl("with", "--network", "IoT", "--blackout", "--", "chaosctl-no-such-command"); r.code != 127 || !strings.Contains(r.err, "not found") || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v, overlays %v", r, overlayIDs(t, g))
	}
	notExec := writeFile(t, "script.sh", "#!/bin/sh\n")
	if r := g.ctl("with", "--network", "IoT", "--blackout", "--", notExec); r.code != 126 || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v, overlays %v", r, overlayIDs(t, g))
	}
	// a command that is killed by a signal: 128 plus the signal
	if r := g.ctl("with", "--network", "IoT", "--blackout", "--", "sh", "-c", "kill -KILL $$"); r.code != 128+9 || len(overlayIDs(t, g)) != 0 {
		t.Errorf("%+v", r)
	}
}

// with: a signal goes to the command, and the overlays are removed when it ends; chaosctl's own failures are 125.
func ctlWithPassesASignalOnAndFailsWith125(t *testing.T, g *gw, clk *clock.Fake) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	j := g.startCtl(nil, "with", "--network", "IoT", "--blackout", "--", "sh", "-c", `trap 'exit 7' TERM; touch `+started+`; while true; do sleep 0.02; done`)
	eventuallyTrue(t, "the command to start", func() bool { _, err := os.Stat(started); return err == nil })
	j.sigs <- syscall.SIGTERM
	if r := j.wait(t); r.code != 7 {
		t.Errorf("the command's own answer to the signal: %+v", r)
	}
	if n := len(overlayIDs(t, g)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}

	marker := filepath.Join(dir, "ran")
	run := func(args ...string) ctlOut {
		return g.ctl(append(args, "--", "touch", marker)...)
	}
	for name, args := range map[string][]string{
		"no target":         {"with", "--blackout"},
		"nothing to apply":  {"with", "--device", "esp32-42"},
		"a refused fault":   {"with", "--device", "esp32-42", "--latency", "10ms", "--jitter", "99ms"},
		"an unknown flag":   {"with", "--device", "esp32-42", "--blackout", "--bogus"},
		"a bad lease":       {"with", "--device", "esp32-42", "--blackout", "--lease", "10ms"},
		"an unknown device": {"with", "--device", "no-such-device", "--blackout"},
	} {
		if r := run(args...); r.code != 125 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := g.ctl("with", "--device", "esp32-42", "--blackout"); r.code != 125 || !strings.Contains(r.err, "--") {
		t.Errorf("no command: %+v", r)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("the command ran although chaosctl could not set up the fault")
	}
	// the second overlay is refused: the first one is removed again and the command does not run
	bad := writeFile(t, "bad.yaml", "rule: {action: frobnicate}\n")
	if r := run("with", "--device", "esp32-42", "--profile", "bad-lte", "--overlay", bad); r.code != 125 {
		t.Errorf("%+v", r)
	}
	if n := len(overlayIDs(t, g)); n != 0 {
		t.Errorf("%d overlays are left after a failed setup", n)
	}
}

// with: the lease of its overlays is renewed while the command runs, and an overlay of a chaosctl that was killed is
// taken away by the gateway when the lease runs out.
func ctlWithRenewsTheLeaseAndALeaseThatIsNotRenewedExpires(t *testing.T, g *gw, clk *clock.Fake) {
	r := g.ctl("with", "--network", "IoT", "--blackout", "--lease", "1s", "--", "sh", "-c", "sleep 2.5")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	// the command ran 2.5 s under a lease of 1 s: the overlay was there to be removed, not expired
	if !strings.Contains(r.err, "removed") {
		t.Errorf("the overlay had expired before the command ended:\n%s", r.err)
	}
	// a fault with a lease nobody renews
	id := strings.TrimSpace(g.ctl("fault", "set", "--network", "IoT", "--blackout", "--lease", "1s").out)
	if g.do("GET", "/overlays/"+id, nil, nil, nil).Status != 200 {
		t.Fatal("the fault is not there")
	}
	eventuallyTrue(t, "the lease to run out", func() bool { return g.do("GET", "/overlays/"+id, nil, nil, nil).Status == 404 })
}

// A command that outlives the TTL of its overlay: the gateway has removed the overlay by itself when `with` comes to
// remove it, and that is neither an error nor a changed exit code.
func ctlWithAnOverlayWhoseTTLRanOutDuringTheCommand(t *testing.T, g *gw, clk *clock.Fake) {
	r := g.ctl("with", "--network", "IoT", "--blackout", "--ttl", "1s", "--lease", "0", "--", "sh", "-c", "sleep 2.5")
	if r.code != 0 || strings.Contains(r.err, "could not remove") {
		t.Errorf("a command that succeeds: %+v", r)
	}
	r = g.ctl("with", "--network", "IoT", "--blackout", "--ttl", "1s", "--lease", "0", "--", "sh", "-c", "sleep 2.5; exit 4")
	if r.code != 4 || strings.Contains(r.err, "could not remove") {
		t.Errorf("a command that fails: %+v", r)
	}
	if ids := overlayIDs(t, g); len(ids) != 0 {
		t.Errorf("overlays are left: %v", ids)
	}
}

// wg export: a client's configuration goes to a file with mode 0600, or to standard output; a token below scope full
// is refused.
func TestChaosctlExportsAWireGuardClient(t *testing.T) {
	g := ready(t)
	g.addGeneratedClient(false)
	out := filepath.Join(t.TempDir(), "rC.conf")
	r := g.ctl("wg", "export", "lab-hub", "rC", "-o", out)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(b), "[Interface]") || !strings.Contains(string(b), "Address = 10.99.0.4") {
		t.Fatalf("%v\n%s", err, b)
	}
	if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
		t.Errorf("the file with a private key has mode %v", st.Mode().Perm())
	}
	if r := g.ctl("wg", "export", "lab-hub", "rC"); r.code != 0 || !strings.Contains(r.out, "[Interface]") {
		t.Errorf("standard output: %+v", r)
	}
	if r := g.ctl("wg", "export", "lab-hub", "rC", "--format", "png", "-o", out); r.code != 0 {
		t.Errorf("%+v", r)
	} else if b, _ := os.ReadFile(out); !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Errorf("not a PNG")
	}
	g.mintToken("overlays")
	if r := g.ctl("wg", "export", "lab-hub", "rC"); r.code != 2 || !strings.Contains(r.err, "403") {
		t.Errorf("a token below full: %+v", r)
	}
	g.mintToken("full")
	if r := g.ctl("wg", "export", "lab-hub", "ghost"); r.code != 2 || !strings.Contains(r.err, "404") {
		t.Errorf("%+v", r)
	}
}

// The captures a run made are artifacts; --artifacts saves them next to the reports, with the name the gateway gives
// the file, and the file is the capture the API serves.
func TestChaosctlSavesTheCapturesOfARunInTheArtifactsDirectory(t *testing.T) {
	g, clk := capGateway(t)
	file := writeFile(t, "rec.yaml", "name: rec\ntarget: {device: esp32-42}\ncapture: true\npreconditions: {skip: [target_online]}\nsteps:\n  - {id: slow, at: 0s, profile: normal}\n  - {id: end, at: 10s, restore: true}\n")
	dir := t.TempDir()
	j := g.startCtl(nil, "run", "-f", file, "--wait", "--lease", "0", "--artifacts", dir)
	eventuallyTrue(t, "the capture to run", func() bool { return g.cap.Running() == 1 })
	g.cap.Frame("br-iot", tcpFrame(macESP, macGW, "10.10.0.31", "203.0.113.10", 40000, 8883, 30), false)
	clk.BlockUntil(1)
	r := g.advanceUntil(clk, j, 250*time.Millisecond, 0)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	runs := g.do("GET", "/runs", nil, nil, nil).json(t)["items"].([]any)
	run := g.run(runs[0].(map[string]any)["id"].(string))
	capID := artifactsOfKind(run, "capture")[0]["capture"].(string)
	want := g.do("GET", "/captures/"+capID+"/download", nil, nil, nil)
	matches, _ := filepath.Glob(filepath.Join(dir, "capture-*"))
	if len(matches) != 1 {
		t.Fatalf("the capture is not in the directory: %v", matches)
	}
	got, err := os.ReadFile(matches[0])
	if err != nil || !bytes.Equal(got, want.Body) || len(got) == 0 {
		t.Errorf("the saved capture (%d bytes, %v) is not the one the API serves (%d bytes)", len(got), err, len(want.Body))
	}
	if base := filepath.Base(matches[0]); base != "capture-"+capID+".pcap" && base != "capture-"+capID+".pcapng" {
		t.Errorf("the file is %s, not named after the capture %s", base, capID)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(left) != 0 {
		t.Errorf("temporary files are left: %v", left)
	}
}

// The overlay commands and `wait device` on one gateway with the device esp32-42 (the real clock). One gateway for all of them: the setup of a gateway takes seconds, most of what a test of the
// CLI does takes milliseconds. A subtest leaves the gateway as it found it.
func TestChaosctlOverlays(t *testing.T) {
	g, clk, _ := ctlGateway(t)
	sub := func(name string, f func(t *testing.T, g *gw, clk *clock.Fake)) {
		t.Run(name, func(t *testing.T) {
			g.t = t // a failure of a request ends the subtest, not the test
			f(t, g, clk)
		})
	}
	sub("AppliesAProfileAndAFaultWithATTLAndRemovesThem", ctlAppliesAProfileAndAFaultWithATTLAndRemovesThem)
	sub("RenewsTheLeaseOfAnOverlayItDoesNotKeepAlive", ctlRenewsTheLeaseOfAnOverlayItDoesNotKeepAlive)
	sub("ResetAndTheReplacementOfAFault", ctlResetAndTheReplacementOfAFault)
	sub("ExplainsTheTrafficOfADevice", ctlExplainsTheTrafficOfADevice)
	sub("CreatesAnOverlayFromARequestFile", ctlCreatesAnOverlayFromARequestFile)
	sub("MakesDNSFaults", ctlMakesDNSFaults)
	sub("MakesTLSCases", ctlMakesTLSCases)
	sub("MakesDHCPActions", ctlMakesDHCPActions)
	sub("ManagesProbes", ctlManagesProbes)
	sub("ReadsTheTokenFromAFile", ctlReadsTheTokenFromAFile)
	sub("WaitsForADeviceToBeOnlineAndConnected", ctlWaitsForADeviceToBeOnlineAndConnected)
	sub("WaitsForADeviceThatComesOnline", ctlWaitsForADeviceThatComesOnline)
	sub("ShowsWhatTheGatewayRefusesAndExitsWith2", ctlShowsWhatTheGatewayRefusesAndExitsWith2)
	g.t = t
}

// The runs on one gateway whose API keeps time with a fake clock; every subtest ends the runs it starts. One gateway for all of them: the setup of a gateway takes seconds, most of what a test of the
// CLI does takes milliseconds. A subtest leaves the gateway as it found it.
func TestChaosctlRuns(t *testing.T) {
	g, clk, _ := ctlGateway(t)
	sub := func(name string, f func(t *testing.T, g *gw, clk *clock.Fake)) {
		t.Run(name, func(t *testing.T) {
			g.t = t // a failure of a request ends the subtest, not the test
			f(t, g, clk)
		})
	}
	sub("RunsAScenarioFromAFileAndSavesItsReports", ctlRunsAScenarioFromAFileAndSavesItsReports)
	sub("StartsAStoredScenarioAndWaitsForItLater", ctlStartsAStoredScenarioAndWaitsForItLater)
	sub("Returns1ForAFailedCheck", ctlReturns1ForAFailedCheck)
	sub("Returns2ForARunThatEndsAsError", ctlReturns2ForARunThatEndsAsError)
	sub("ReportsAScenarioTheGatewayRefuses", ctlReportsAScenarioTheGatewayRefuses)
	sub("AbortsTheRunWhenTheTimeoutRunsOut", ctlAbortsTheRunWhenTheTimeoutRunsOut)
	sub("AbortsItsRunOnASignalAndLeavesAnotherRunAlone", ctlAbortsItsRunOnASignalAndLeavesAnotherRunAlone)
	sub("RenewsTheLeaseOfTheRunItWaitsFor", ctlRenewsTheLeaseOfTheRunItWaitsFor)
	sub("QueuesBehindARunAndNoQueueRefuses", ctlQueuesBehindARunAndNoQueueRefuses)
	g.t = t
}

// `chaosctl with` on one gateway. One gateway for all of them: the setup of a gateway takes seconds, most of what a test of the
// CLI does takes milliseconds. A subtest leaves the gateway as it found it.
func TestChaosctlWith(t *testing.T) {
	g, clk, _ := ctlGateway(t)
	sub := func(name string, f func(t *testing.T, g *gw, clk *clock.Fake)) {
		t.Run(name, func(t *testing.T) {
			g.t = t // a failure of a request ends the subtest, not the test
			f(t, g, clk)
		})
	}
	sub("WithRunsACommandUnderAFaultAndCleansUp", ctlWithRunsACommandUnderAFaultAndCleansUp)
	sub("WithCleansUpWhenTheCommandCannotRun", ctlWithCleansUpWhenTheCommandCannotRun)
	sub("WithPassesASignalOnAndFailsWith125", ctlWithPassesASignalOnAndFailsWith125)
	sub("WithRenewsTheLeaseAndALeaseThatIsNotRenewedExpires", ctlWithRenewsTheLeaseAndALeaseThatIsNotRenewedExpires)
	sub("WithAnOverlayWhoseTTLRanOutDuringTheCommand", ctlWithAnOverlayWhoseTTLRanOutDuringTheCommand)
	g.t = t
}
