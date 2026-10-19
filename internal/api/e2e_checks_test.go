//go:build testbed

package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M16 on a real kernel, over the API and on the real clock: the checks of a scenario against a device whose behavior
// is scripted (it keeps a connection to a broker and comes back after a cut, or gives up), and the zero-hit warning of
// a fault that matches nothing.

// brokerScript is a TCP echo server on 8883, the broker of the scripted device.
const brokerScript = `
import socket, threading
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 8883)); s.listen(50)
def serve(c):
    try:
        while True:
            d = c.recv(100)
            if not d: break
            c.sendall(d)
    except Exception: pass
while True:
    c, _ = s.accept()
    threading.Thread(target=serve, args=(c,), daemon=True).start()
`

// deviceScript is a device that keeps a connection to the broker: it holds it with a byte every 200 ms, and when it
// breaks (a reset by the cut) it tries again every 500 ms until it is through. It prints one line per event.
const deviceScript = `
import socket, sys, time
host, port = sys.argv[1], int(sys.argv[2])
while True:
    try:
        s = socket.socket(); s.settimeout(1); s.connect((host, port))
        print("connected", flush=True)
        while True:
            s.sendall(b"x"); s.recv(10); time.sleep(0.2)
    except Exception as e:
        print(type(e).__name__, flush=True)
        time.sleep(0.5)
`

// tryScript makes one connection to the broker and prints ok when it is echoed.
const tryScript = `
import socket, sys
try:
    s = socket.socket(); s.settimeout(1); s.connect((sys.argv[1], 8883)); s.sendall(b"hi"); print("ok" if s.recv(10) == b"hi" else "bad")
except Exception as e: print(type(e).__name__)
`

func startBroker(t *testing.T, top *testbed.Topology) {
	t.Helper()
	top.Server.Start("python3", "-c", brokerScript)
	deadline := time.Now().Add(60 * time.Second)
	for {
		out, _ := top.A.Run(context.Background(), "python3", "-c", tryScript, testbed.ServerAddr)
		if strings.TrimSpace(out) == "ok" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the broker does not answer: %s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitOutput(t *testing.T, p *testbed.Process, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !strings.Contains(p.Output(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output: %s", want, p.Output())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// outageRun is the scenario of plan §2.10 on the real kernel: the broker's port is cut for 4 s (resetting what is
// established), then restored; the device is expected to come back.
func outageRun(name, within string) map[string]any {
	body := inlineRun(name, iotTarget,
		step("cut", "1s", "rule", map[string]any{"action": "drop", "protocol": "tcp", "ports": []int{8883}, "cut_existing": true,
			"destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("restore", "5s", "restore", true),
	)
	sc := body["inline"].(map[string]any)
	sc["checks"] = []any{
		map[string]any{"reconnected": map[string]any{"protocol": "tcp", "ports": []int{8883}}, "window": map[string]any{"from": "restore", "within": within}},
		map[string]any{"no_connection": map[string]any{"protocol": "tcp", "ports": []int{8883}}, "window": map[string]any{"from": "cut", "until": "restore"}},
		map[string]any{"traffic_seen": map[string]any{"destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}, "window": map[string]any{"from": "restore", "within": within}},
	}
	return body
}

// A device that keeps connecting passes the checks of the outage: nothing is established while the port is cut, and
// it is back within the window after the restore.
func TestChecksPassForADeviceThatReconnectsOnTheRealKernel(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	startBroker(t, top)
	device := top.A.Start("python3", "-c", deviceScript, testbed.ServerAddr, "8883")
	waitOutput(t, device, "connected", 30*time.Second)

	r := g.postRun(outageRun("outage-passes", "30s"), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	done := g.waitRun(id, "passed", "failed", "error")
	if done["status"] != "passed" {
		t.Fatalf("the run ended as %v (%v): %v\ndevice: %s", done["status"], done["reason"], done["checks"], device.Output())
	}
	for _, c := range done["checks"].([]any) {
		m := c.(map[string]any)
		t.Logf("check %v: %v: %v", m["index"], m["status"], m["message"])
		if m["status"] != "passed" {
			t.Errorf("check %v is %v: %v", m["name"], m["status"], m["message"])
		}
	}
	xml := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body)
	if !strings.Contains(xml, `failures="0"`) || !strings.Contains(xml, `errors="0"`) {
		t.Errorf("the JUnit file:\n%s", xml)
	}
	if n := len(g.overlaysOf(id)); n != 0 || kernelHasFaultChain(top) {
		t.Errorf("%d overlays are left", n)
	}
}

// A device that does not come back fails the check of the reconnection, and so the run, and the report says which
// check; the check that nothing was established while the port was cut still passes.
func TestChecksFailForADeviceThatDoesNotReconnectOnTheRealKernel(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	startBroker(t, top)
	device := top.A.Start("python3", "-c", deviceScript, testbed.ServerAddr, "8883")
	waitOutput(t, device, "connected", 30*time.Second)

	r := g.postRun(outageRun("outage-fails", "6s"), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "cut", "applied")
	device.Stop() // the device gives up
	done := g.waitRun(id, "passed", "failed", "error")
	if done["status"] != "failed" {
		t.Fatalf("the run ended as %v (%v): %v", done["status"], done["reason"], done["checks"])
	}
	statuses := map[string]string{}
	for _, c := range done["checks"].([]any) {
		m := c.(map[string]any)
		statuses[m["type"].(string)] = m["status"].(string)
	}
	if statuses["reconnected"] != "failed" || statuses["no_connection"] != "passed" || statuses["traffic_seen"] != "failed" {
		t.Errorf("check results %v", statuses)
	}
	xml := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body)
	if !strings.Contains(xml, `failures="2"`) {
		t.Errorf("the JUnit file:\n%s", xml)
	}
	if n := len(g.overlaysOf(id)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// A scenario whose fault never matches reports the zero-hit warning: the fault on the server is hit by the pings of
// the test, the one on an address nobody uses is not. With `zero_hit: error` the run fails.
func TestAFaultThatNeverMatchesIsAZeroHitWarningOnTheRealKernel(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	body := func(name, zero string) map[string]any {
		b := inlineRun(name, iotTarget,
			step("hit", "0s", "fault", map[string]any{"latency": "40ms", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
			step("miss", "0s", "fault", map[string]any{"loss": "10%", "destination": map[string]any{"cidr": "198.51.100.7/32"}}),
			step("end", "6s", "restore", true))
		if zero != "" {
			b["inline"].(map[string]any)["zero_hit"] = zero
		}
		return b
	}
	for _, c := range []struct{ name, zero, status string }{{"zero-hit-warning", "", "passed"}, {"zero-hit-error", "error", "failed"}} {
		r := g.postRun(body(c.name, c.zero), "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id := r.json(t)["id"].(string)
		g.waitStep(id, "miss", "applied")
		if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 4, 200*time.Millisecond); res.Median() < 30*time.Millisecond {
			t.Fatalf("the fault is not on: %v", res.Median())
		}
		done := g.waitRun(id, c.status)
		ws, _ := done["warnings"].([]any)
		var steps []string
		for _, w := range ws {
			m := w.(map[string]any)
			if m["code"] == "zero_hit" {
				steps = append(steps, m["step"].(string))
			}
		}
		if strings.Join(steps, ",") != "miss" {
			t.Errorf("%s: zero-hit warnings for %v, want only the step that matches nothing: %v", c.name, steps, ws)
		}
		xml := string(g.do("GET", "/runs/"+id+"/report.xml", nil, nil, nil).Body)
		if !strings.Contains(xml, "warning zero_hit: the fault of step miss matched no packet") {
			t.Errorf("%s: the JUnit output has no zero-hit warning:\n%s", c.name, xml)
		}
	}
}

// brokerRun is a run of the device client-a with one check, a window from the step go (at 1 s) to the step end.
func brokerRun(name, endAt string, check map[string]any) map[string]any {
	body := inlineRun(name, map[string]any{"device": clientA},
		step("go", "1s", "fault", map[string]any{"latency": "5ms", "destination": map[string]any{"cidr": testbed.ServerAddr + "/32"}}),
		step("end", endAt, "restore", true))
	body["inline"].(map[string]any)["checks"] = []any{check}
	return body
}

// P4-AUDIT-51: `traffic_not_seen` with `except` on the real kernel: a device that talks to the allowed server passes
// it, one that also talks to another server fails it, and the message names the connection.
func TestTrafficNotSeenWithExceptDecidesOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	check := map[string]any{"traffic_not_seen": map[string]any{"except": []any{map[string]any{"cidr": testbed.ServerAddr + "/32"}}},
		"window": map[string]any{"from": "go", "until": "end"}}
	for _, c := range []struct{ name, server, status string }{
		{"except-allowed", testbed.ServerAddr, "passed"},
		{"except-other", testbed.ServerAddr2, "failed"},
	} {
		r := g.postRun(brokerRun(c.name, "8s", check), "")
		if r.Status != 201 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		id := r.json(t)["id"].(string)
		g.waitStep(id, "go", "applied")
		if out, err := top.A.Run(context.Background(), "python3", "-c", tryScript, c.server); err != nil || strings.TrimSpace(out) != "ok" {
			t.Fatalf("%s: the device does not reach %s: %q %v", c.name, c.server, out, err)
		}
		done := g.waitRun(id, "passed", "failed", "error")
		got := done["checks"].([]any)[0].(map[string]any)
		if got["status"] != c.status {
			t.Errorf("%s: the check is %v: %v", c.name, got["status"], got["message"])
		}
		if c.status == "failed" && !strings.Contains(got["message"].(string), c.server+":8883") {
			t.Errorf("%s: the message does not name the connection: %v", c.name, got["message"])
		}
	}
}

// P4-AUDIT-51: `dns_query_seen` on the real kernel: a device that resolves a name through the gateway's DNS inside the
// window passes it, and the log of the proxy assigns the query to the device (the check ignores a query without one);
// a device that does not ask fails it.
func TestDNSQuerySeenDecidesForADeviceOnTheRealKernel(t *testing.T) {
	bed := newDNSBedOn(t, bedCapture)
	g, top := bed.g, bed.top
	g.mintToken("overlays")
	check := func(within string) map[string]any {
		return map[string]any{"dns_query_seen": map[string]any{"name": "example.test"}, "window": map[string]any{"from": "go", "within": within}}
	}

	r := g.postRun(brokerRun("dns-asked", "10s", check("8s")), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "go", "applied")
	if out, err := digA(top, top.A, testbed.LAN0Gateway); err != nil || out != "203.0.113.77" {
		t.Fatalf("A does not resolve through the gateway: %q %v", out, err)
	}
	done := g.waitRun(id, "passed", "failed", "error")
	c := done["checks"].([]any)[0].(map[string]any)
	if c["status"] != "passed" {
		t.Fatalf("the check is %v: %v", c["status"], c["message"])
	}
	if ev, _ := c["evidence"].([]any); len(ev) == 0 || ev[0].(map[string]any)["device"] != espID {
		t.Errorf("the query is not the device's: %v", c["evidence"])
	}
	// the entries of the log carry the device that the client address maps to
	items := g.do("GET", "/dns/queries?name=example.test", nil, nil, nil).json(t)["items"].([]any)
	seen := 0
	for _, it := range items {
		e := it.(map[string]any)
		if e["client"] != testbed.ClientAAddr {
			continue
		}
		seen++
		if e["device"] != espID {
			t.Errorf("a query of A is logged for the device %v: %v", e["device"], e)
		}
	}
	if seen == 0 {
		t.Errorf("no query of A in the log: %v", items)
	}

	r = g.postRun(brokerRun("dns-silent", "6s", check("3s")), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	done = g.waitRun(r.json(t)["id"].(string), "passed", "failed", "error")
	c = done["checks"].([]any)[0].(map[string]any)
	if c["status"] != "failed" || !strings.Contains(c["message"].(string), "no device of the target asked for example.test") {
		t.Errorf("a device that asked nothing in the window: %v: %v", c["status"], c["message"])
	}
}

// P4-AUDIT-15: the checks read the connections of the kernel, and a packet that the gateway drops in the forward path
// leaves no entry there. With a drop rule in force and a device that keeps trying, `traffic_not_seen` and `no_connection`
// pass and `traffic_seen` fails, although the rule counts the packets: the limit that the spec and the M16 decisions
// state.
func TestTheTrafficChecksDoNotSeeWhatTheGatewayDropsOnTheRealKernel(t *testing.T) {
	g, top := bedCapture(t)
	g.mintToken("overlays")
	server := map[string]any{"cidr": testbed.ServerAddr + "/32"}
	body := inlineRun("drop-limit", map[string]any{"device": clientA},
		step("drop", "1s", "rule", map[string]any{"action": "drop", "protocol": "tcp", "ports": []int{8883}, "destination": server}),
		step("end", "25s", "restore", true))
	window := map[string]any{"from": "drop", "within": "6s"}
	body["inline"].(map[string]any)["checks"] = []any{
		map[string]any{"traffic_not_seen": map[string]any{"destination": server}, "window": window},
		map[string]any{"no_connection": map[string]any{"protocol": "tcp", "ports": []int{8883}}, "window": window},
		map[string]any{"traffic_seen": map[string]any{"destination": server}, "window": window},
	}
	r := g.postRun(body, "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	id := r.json(t)["id"].(string)
	g.waitStep(id, "drop", "applied")
	// a device that starts only now: every attempt meets the drop, nothing is established before the restore
	device := top.A.Start("python3", "-c", deviceScript, testbed.ServerAddr, "8883")
	statuses := func() map[string]string {
		out := map[string]string{}
		for _, c := range g.run(id)["checks"].([]any) {
			m := c.(map[string]any)
			out[m["type"].(string)] = m["status"].(string)
		}
		return out
	}
	eventuallyTrue(t, "the checks to be decided", func() bool {
		for _, s := range statuses() {
			if s == "pending" {
				return false
			}
		}
		return true
	})
	if strings.Contains(device.Output(), "connected") {
		t.Fatalf("the drop does not hold, the device is connected: %s", device.Output())
	}
	st := statuses()
	if st["traffic_not_seen"] != "passed" || st["no_connection"] != "passed" || st["traffic_seen"] != "failed" {
		t.Errorf("check results %v", st)
	}
	done := g.waitRun(id, "passed", "failed", "error")
	rep := g.do("GET", "/runs/"+id+"/report.json", nil, nil, nil).json(t)
	var dropped float64
	for _, c := range rep["counters"].([]any) {
		m := c.(map[string]any)
		if m["step"] == "drop" {
			dropped = m["counters"].(map[string]any)["packets"].(float64)
		}
	}
	if dropped < 1 {
		t.Errorf("the rule dropped no packet, so the device did not try (run %v): %v", done["status"], rep["counters"])
	}
}
