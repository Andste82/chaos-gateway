//go:build testbed

package api_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M25 level 1 tests: a probe over the API on the real kernel (spike S12: a port of the gateway's bridge in a namespace of its own),
// its address, a diagnostic from it against the reference services of the server namespace,
// and the self-test "measure this profile" against the netem of the real kernel within the tolerances of plan §4.3. The
// same with the real Kea, and with rules, is in internal/engine/integration_probes_test.go.

const (
	probeCfgID = "3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8"
	probeCfgNS = "cgp-3a1f7c52"
	echoPort   = 9100
)

// addressTheProbe gives the probe the address its DHCP client would have got. The client of the executor runs in its namespace, but
// the API's testbed has no DHCP server on the bridge (the engine's testbed has the real Kea: TestAProbeGetsALeaseFromKea...), so
// the tests of the API set the address by hand and test everything else of the probe.
func addressTheProbe(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if out, _ := exec.Command("ip", "netns", "list").CombinedOutput(); strings.Contains(string(out), probeCfgNS) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the probe's namespace does not appear")
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, args := range [][]string{{"addr", "add", "10.10.0.150/24", "dev", "eth0"}, {"route", "replace", "default", "via", "10.10.0.1"}} {
		if out, err := exec.Command("ip", append([]string{"netns", "exec", probeCfgNS, "ip"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v in the probe: %v %s", args, err, out)
		}
	}
}

// probeGW is the gateway of the testbed with a probe in the network IoT that has its address.
func probeGW(t *testing.T, patch func(p map[string]any)) (*gw, *testbed.Topology) {
	t.Helper()
	g, top := newBedGW(t)
	testbed.StartPlainEcho(t, top.Server, testbed.ServerAddr, echoPort)
	p := map[string]any{"probes": map[string]any{probeCfgID: map[string]any{"name": "probe-a", "network": "IoT"}}}
	if patch != nil {
		patch(p)
	}
	id := g.mustPatch(p)
	r := g.apply(id)
	if r.Status != 200 {
		t.Fatalf("apply: %d %s", r.Status, r.Body)
	}
	if r.json(t)["status"] == "pending_confirm" {
		if cf := g.do("POST", "/revisions/"+itoa(id)+"/confirm", nil, nil, nil); cf.Status != 200 {
			t.Fatalf("confirm: %d %s", cf.Status, cf.Body)
		}
	}
	addressTheProbe(t)
	// the observation as the daemon runs it
	if err := g.e.PollObserved(t.Context(), 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		items := g.do("GET", "/probes", nil, nil, nil).json(t)["items"].([]any)
		if len(items) == 1 {
			if a, _ := sub(items[0].(map[string]any), "observed")["addresses"].([]any); len(a) == 1 {
				if !strings.HasPrefix(a[0].(string), "10.10.0.") {
					t.Fatalf("the probe's address %v is not in the pool", a)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe got no address\n%s\n%s", top.GW.Must("ip", "addr"), top.GW.Must("ip", "-d", "link"))
		}
		time.Sleep(300 * time.Millisecond)
	}
	return g, top
}

// M25 test: a probe reaches the gateway address, the server and a device through the gateway, and every diagnostic runs from it.
func TestAProbeRunsDiagnosticsAgainstTheServerOnTheRealKernel(t *testing.T) {
	g, top := probeGW(t, nil)
	from := map[string]any{"probe": "probe-a"}
	srv := testbed.ServerAddr
	diag := func(body map[string]any) map[string]any {
		t.Helper()
		body["from"] = from
		return g.diagnoseOK(body)
	}
	r := diag(map[string]any{"type": "ping", "host": srv, "count": 3, "timeout": "3s"})
	if r["ok"] != true || r["from"] != "probe:"+probeCfgNS || sub(r, "ping")["received"] != float64(3) {
		t.Errorf("ping the server: %v", r)
	}
	if r := diag(map[string]any{"type": "ping", "host": "10.10.0.1", "count": 2, "timeout": "3s"}); r["ok"] != true {
		t.Errorf("ping the gateway's address: %v", r)
	}
	if r := diag(map[string]any{"type": "ping", "host": testbed.ClientAAddr, "count": 2, "timeout": "3s"}); r["ok"] != true {
		t.Errorf("ping a device of the network: %v", r)
	}
	if r := diag(map[string]any{"type": "tcp", "host": srv, "port": echoPort}); r["ok"] != true || sub(r, "connect")["reachable"] != true {
		t.Errorf("tcp: %v", r)
	}
	if r := diag(map[string]any{"type": "udp", "host": srv, "port": echoPort, "payload": "hello"}); r["ok"] != true {
		t.Errorf("udp: %v", r)
	}
	if r := diag(map[string]any{"type": "tcp", "host": srv, "port": 9999}); r["ok"] != false || sub(r, "connect")["detail"] != "connection refused" {
		t.Errorf("a closed port: %v", r)
	}
	if r := diag(map[string]any{"type": "path", "host": srv, "port": echoPort, "tls": false}); r["ok"] != true {
		t.Errorf("path: %v", r)
	}
	// the probe is a port of the bridge in a namespace of its own
	if out := top.GW.Must("ip", "-d", "link", "show", "dev", "pv3a1f7c52"); !strings.Contains(out, "master br-iot") {
		t.Errorf("the gateway's end of the pair is no port of the bridge: %s", out)
	}
}

// M25 test: a fault that targets the probe slows its diagnostics and not those of the gateway.
func TestAFaultOnTheProbeIsSeenByItsDiagnosticsAndNotByTheGatewaysOnTheRealKernel(t *testing.T) {
	g, _ := probeGW(t, nil)
	srv := testbed.ServerAddr
	rtt := func(from map[string]any) float64 {
		t.Helper()
		body := map[string]any{"type": "tcp", "host": srv, "port": echoPort}
		if from != nil {
			body["from"] = from
		}
		r := g.diagnoseOK(body)
		if r["ok"] != true {
			t.Fatalf("%v", r)
		}
		return sub(r, "connect")["rtt_ms"].(float64)
	}
	base := rtt(map[string]any{"probe": "probe-a"})
	ov := g.mustCreateOverlay(map[string]any{"target": map[string]any{"device": "probe-a"}, "fault": map[string]any{"latency": "100ms"}})
	slow := rtt(map[string]any{"probe": "probe-a"})
	if slow < base+100 { // 200 ms are made; the emulated machine adds tens of ms of noise to the reference
		t.Errorf("the probe's connect with 100 ms in each direction: %.1f ms (%.1f before)", slow, base)
	}
	if gwRTT := rtt(nil); gwRTT > base+100 {
		t.Errorf("the gateway's diagnostic is slowed by a fault of a device: %.1f ms (the probe had %.1f before)", gwRTT, base)
	}
	if r := g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if back := rtt(map[string]any{"probe": "probe-a"}); back > base+100 {
		t.Errorf("the probe is still slow after the fault was removed: %.1f ms", back)
	}
}

// calibrate runs the self-test and returns its answer.
func (g *gw) calibrate(body map[string]any) map[string]any {
	g.t.Helper()
	r := g.do("POST", "/probes/probe-a/calibrate", body, nil, nil)
	if r.Status != 200 {
		g.t.Fatalf("calibrate: %d %s", r.Status, r.Body)
	}
	return r.json(g.t)
}

func checksOf(res map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, c := range res["checks"].([]any) {
		m := c.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

// M25 test (plan M25 "Tests", §4.3): the probe's measurement matches the configured profile. The delay: the median of the added
// delay within ±2 ms + 5 %; the loss: within the 99.9 % binomial interval; the rate: within ±10 %. Under software emulation only the
// effect is asserted (the delay is there, the loss is there, the rate holds the transfer below the unlimited one).
func TestTheMeasurementOfAProfileMatchesItsParametersOnTheRealKernel(t *testing.T) {
	g, _ := probeGW(t, func(p map[string]any) {
		p["profiles"] = map[string]any{
			"6b0f8a1e-2c3d-4e5f-8a9b-0c1d2e3f4a5b": map[string]any{"name": "delay", "parts": map[string]any{"impairment": map[string]any{"latency": "50ms", "jitter": "5ms"}}},
			"6b0f8a1e-2c3d-4e5f-8a9b-0c1d2e3f4a5c": map[string]any{"name": "lossy", "parts": map[string]any{"impairment": map[string]any{"upload": map[string]any{"loss": "10%"}, "download": map[string]any{"loss": "5%"}}}},
			"6b0f8a1e-2c3d-4e5f-8a9b-0c1d2e3f4a5d": map[string]any{"name": "narrow", "parts": map[string]any{"impairment": map[string]any{"rate": "2Mbit"}}},
		}
	})
	req := func(profile string, duration string) map[string]any {
		return map[string]any{"profile": profile, "host": testbed.ServerAddr, "port": echoPort, "duration": duration}
	}
	noOverlay := func() {
		t.Helper()
		if ov := g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any); len(ov) != 0 {
			t.Errorf("the calibration left %d overlays", len(ov))
		}
	}

	// the delay
	res := g.calibrate(req("delay", "20s"))
	noOverlay()
	if added := sub(res, "measured")["rtt_median_ms"].(float64) - sub(res, "measured")["baseline_rtt_ms"].(float64); added < 80 {
		t.Errorf("a profile of 50 ms each way adds %.1f ms to the median round trip: %v", added, res["detail"])
	}
	if checksOf(res)["latency"] == nil || checksOf(res)["jitter"] == nil {
		t.Errorf("%v", res)
	}
	if testbed.Accurate() {
		testbed.Statistically(t, "calibrate the delay of a profile", func() error {
			r := res
			if r == nil {
				r = g.calibrate(req("delay", "20s"))
			}
			res = nil
			added := sub(r, "measured")["rtt_median_ms"].(float64) - sub(r, "measured")["baseline_rtt_ms"].(float64)
			low, high := 100-(2+5), 100+(2+5)
			err := error(nil)
			if !checksOf(r)["latency"]["ok"].(bool) || added < float64(low) || added > float64(high) {
				err = fmt.Errorf("the added delay %.2f ms is outside %d to %d ms: %v", added, low, high, r["detail"])
			}
			lo, hi := float64(low), float64(high)
			return testbed.Measure(t, "calibrate delay").Record("", testbed.Measurement{Quantity: "added round trip delay", Unit: "ms", N: int(sub(r, "samples")["received"].(float64)), Configured: 100, Value: added, Low: &lo, High: &hi}, err)
		})
	}

	// the loss: 10 % up and 5 % down, 14.5 % of the round trips
	res = g.calibrate(req("lossy", "30s"))
	noOverlay()
	if l := sub(res, "measured")["loss_percent"].(float64); l < 5 || l > 25 {
		t.Errorf("a profile of 14.5 %% round trip loss measures %.2f %%: %v", l, res["detail"])
	}
	if testbed.Accurate() {
		testbed.Statistically(t, "calibrate the loss of a profile", func() error {
			r := res
			if r == nil {
				r = g.calibrate(req("lossy", "30s"))
			}
			res = nil
			l := sub(r, "measured")["loss_percent"].(float64) / 100
			var err error
			if !checksOf(r)["loss"]["ok"].(bool) {
				err = fmt.Errorf("the loss %.2f %% is outside the 99.9 %% interval of 14.5 %%: %v", l*100, r["detail"])
			}
			return testbed.Measure(t, "calibrate loss").Record("", testbed.Measurement{Quantity: "round trip loss", Unit: "fraction", N: int(sub(r, "samples")["sent"].(float64)), Configured: 0.145, Value: l}, err)
		})
	}

	// the rate: a TCP echo is held to the limit
	res = g.calibrate(req("narrow", "30s"))
	noOverlay()
	rate := sub(res, "measured")["rate_bps"].(float64)
	if rate <= 0 || rate > 3_000_000 {
		t.Errorf("a profile of 2 Mbit/s measures %.0f bit/s: %v", rate, res["detail"])
	}
	if testbed.Accurate() {
		testbed.Statistically(t, "calibrate the rate of a profile", func() error {
			r := res
			if r == nil {
				r = g.calibrate(req("narrow", "30s"))
			}
			res = nil
			v := sub(r, "measured")["rate_bps"].(float64)
			lo, hi := 1_800_000.0, 2_200_000.0
			var err error
			if !checksOf(r)["rate"]["ok"].(bool) {
				err = fmt.Errorf("the throughput %.0f bit/s is outside 1.8 to 2.2 Mbit/s: %v", v, r["detail"])
			}
			return testbed.Measure(t, "calibrate rate").Record("", testbed.Measurement{Quantity: "rate", Unit: "bit/s", N: 1, Configured: 2_000_000, Value: v, Low: &lo, High: &hi}, err)
		})
	}

	// the probe's faults now: the profile is gone, and the unimpaired path is what is measured
	res = g.calibrate(map[string]any{"host": testbed.ServerAddr, "port": echoPort, "duration": "10s"})
	if res["within_tolerance"] != true && testbed.Accurate() {
		t.Errorf("a path without a fault is not within the tolerances: %v", res["detail"])
	}
	// a path without a fault loses nothing; an emulated machine may drop a datagram now and then
	if l := sub(res, "measured")["loss_percent"].(float64); (testbed.Accurate() && l != 0) || l > 2 {
		t.Errorf("a path without a fault loses %.2f %%", l)
	}
}
