//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M25 level 1: a probe is a bridge port of a test network in a namespace of its own, gets its address from Kea like a device and
// meets the faults and rules that target it (plan §2.12, spike S12). The real executor, the real Kea, the real kernel.

const (
	probeAID = "3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8"
	probeANS = "cgp-3a1f7c52"
	probeAIf = "pv3a1f7c52"
)

func withProbe(c *model.Configuration) {
	dhcpOn(c)
	probes := map[string]model.Probe{probeAID: {Name: "probe-a", Network: tIoT}}
	c.Probes = &probes
}

// inProbe runs a command in the probe's namespace.
func inProbe(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("ip", append([]string{"netns", "exec", probeANS}, args...)...).CombinedOutput()
	return string(out), err
}

var pingRTT = regexp.MustCompile(`= [\d.]+/([\d.]+)/`)

// avgRTT pings from the namespace (the probe's, or another) and returns the average round trip in ms; ok is false when no
// answer came.
func avgRTT(t *testing.T, ns, dst string, count int) (float64, bool) {
	t.Helper()
	out, err := exec.Command("ip", "netns", "exec", ns, "ping", "-c", strconv.Itoa(count), "-W", "2", "-i", "0.2", "-n", dst).CombinedOutput()
	if err != nil {
		return 0, false
	}
	m := pingRTT.FindStringSubmatch(string(out))
	if m == nil {
		return 0, false
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v, true
}

func (b *dhcpBed) probeBed(t *testing.T) *engine.DeviceState {
	t.Helper()
	b.apply(withProbe)
	b.follow()
	d := b.waitDevice(compiler.ProbeMAC(probeAID), 90*time.Second, func(d *engine.DeviceState) bool {
		return len(d.Addresses) > 0 && d.Lease != nil
	})
	// Kea has the lease when the client has still to configure the interface with the ACK: the route comes with the address
	deadline := time.Now().Add(30 * time.Second)
	for {
		if out, _ := inProbe(t, "ip", "route", "show", "default"); strings.Contains(out, "default via") {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatal("the probe has no default route")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (b *dhcpBed) readProbes() []executor.ProbeNSState {
	b.t.Helper()
	var st []executor.ProbeNSState
	ex := apply.Local{E: b.ex}
	out, err := ex.Do(context.Background(), &executor.Read{Target: executor.Target{NS: b.top.GW.Name}, What: executor.ReadProbes})
	if err != nil {
		b.t.Fatal(err)
	}
	if err := json.Unmarshal(out.Data[0], &st); err != nil {
		b.t.Fatal(err)
	}
	return st
}

// M25 test: the probe gets an address from Kea like a device, shows up as a device of its own origin, and reaches the gateway,
// a device and the server.
func TestAProbeGetsALeaseFromKeaAndReachesTheGatewayADeviceAndTheServer(t *testing.T) {
	b := newDHCPBed(t)
	d := b.probeBed(t)
	if d.Origin != model.DeviceOriginProbe || d.ID != probeAID || d.Name != "probe-a" || d.Network != tIoT || d.Lease == nil || d.Lease.Ip != d.Addresses[0].String() {
		t.Fatalf("%+v", d)
	}
	pool := d.Addresses[0].As4()[3]
	if !strings.HasPrefix(d.Addresses[0].String(), "10.10.0.") || pool < 128 || pool == 255 {
		t.Errorf("%s is not in the default pool", d.Addresses[0])
	}
	ps := b.readProbes()
	if len(ps) != 1 || ps[0].Name != probeANS || ps[0].Master == "" || !ps[0].DHCP || ps[0].Lease == nil || len(ps[0].Addrs) != 1 || ps[0].MAC != compiler.ProbeMAC(probeAID) {
		t.Fatalf("%+v", ps)
	}
	// the router and the resolver of the lease are the gateway's address on the network
	if ps[0].Lease.Router.String() != testbed.LAN0Gateway {
		t.Errorf("router %v", ps[0].Lease.Router)
	}
	// the gateway's end is a port of the bridge (spike S12: a probe on a bridge reaches the gateway address)
	if out := b.top.GW.Must("ip", "-d", "link", "show", "dev", probeAIf); !strings.Contains(out, "master "+ps[0].Master) {
		t.Errorf("%s", out)
	}
	if _, ok := avgRTT(t, probeANS, testbed.LAN0Gateway, 3); !ok {
		t.Error("the probe does not reach the gateway's address")
	}
	if _, ok := avgRTT(t, probeANS, testbed.ClientAAddr, 3); !ok {
		t.Error("the probe does not reach a device")
	}
	if _, ok := avgRTT(t, b.top.A.Name, d.Addresses[0].String(), 3); !ok {
		t.Error("a device does not reach the probe")
	}
	if _, ok := avgRTT(t, probeANS, testbed.ServerAddr, 3); !ok {
		t.Error("the probe does not reach the server through the gateway")
	}
	// the host name the client sent is in the lease
	b.waitLeaseHostname(d.Addresses[0].String(), "probe-a")
}

func (b *dhcpBed) waitLeaseHostname(ip, name string) {
	b.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		leases, err := b.kc.Leases(context.Background())
		if err == nil {
			for _, l := range leases {
				if l.IP == ip && l.Hostname == name {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("no lease of %s with the host name %q: %+v %v", ip, name, leases, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// M25 test: another apply, the same one again, and a change to something else leave the probe alone (same interface, same
// address, no new namespace); a probe taken out of the configuration is removed with its namespace.
func TestAProbeSurvivesApplysAndIsRemovedWithItsConfiguration(t *testing.T) {
	b := newDHCPBed(t)
	d := b.probeBed(t)
	index := func() string { out, _ := inProbe(t, "ip", "-o", "link", "show", "eth0"); return strings.Fields(out)[0] }
	before := index()
	b.apply(func(c *model.Configuration) {
		withProbe(c)
		setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("90s")})
	})
	time.Sleep(time.Second)
	if after := index(); after != before {
		t.Errorf("the probe's interface changed: %s then %s", before, after)
	}
	if now := b.waitDevice(compiler.ProbeMAC(probeAID), 10*time.Second, func(d *engine.DeviceState) bool { return len(d.Addresses) > 0 }); now.Addresses[0] != d.Addresses[0] {
		t.Errorf("the probe's address changed: %v then %v", d.Addresses, now.Addresses)
	}
	// removed with its configuration: the namespace, the pair and the client go (the restart of the executor, which leaves the
	// namespace and no client, is TestAProbeWithoutAClientAfterAnExecutorRestartGetsOneAgain, on the simulated kernel)
	b.apply(func(c *model.Configuration) { c.Probes = nil })
	if ps := b.readProbes(); len(ps) != 0 {
		t.Errorf("%+v", ps)
	}
	if out, _ := exec.Command("ip", "netns", "list").CombinedOutput(); strings.Contains(string(out), probeANS) {
		t.Errorf("the namespace is still there:\n%s", out)
	}
	if out := b.top.GW.Must("ip", "link"); strings.Contains(out, probeAIf) {
		t.Errorf("the pair is still there:\n%s", out)
	}
}

// M25 test: a fault and a rule that target the probe act on its traffic and on nobody else's; a fault is not applied to the
// diagnostics of the gateway.
func TestFaultsAndRulesTargetTheProbeAndNobodyElse(t *testing.T) {
	b := newDHCPBed(t)
	d := b.probeBed(t)
	srv := testbed.ServerAddr
	base, ok := avgRTT(t, probeANS, srv, 5)
	if !ok {
		t.Fatal("the probe does not reach the server")
	}
	baseA, _ := avgRTT(t, b.top.A.Name, srv, 5)
	put := func(body string) engine.OverlayResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		res, err := b.e.PutOverlay(ctx, engine.OverlayWrite{Owner: model.Owner{Type: "user", Id: "admin"}, Request: overlayRequest(t, body)})
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		return res
	}
	del := func(res engine.OverlayResult) {
		t.Helper()
		if _, err := b.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"}); err != nil {
			t.Fatal(err)
		}
	}

	// a latency fault on the probe: 100 ms in each direction
	lat := put("target: {device: probe-a}\nfault: {latency: 100ms}")
	slow, ok := avgRTT(t, probeANS, srv, 5)
	if !ok || slow < base+150 {
		t.Errorf("the probe's round trip with 100 ms each way: %.1f ms (%.1f before), ok %v", slow, base, ok)
	}
	if a, ok := avgRTT(t, b.top.A.Name, srv, 5); !ok || a > baseA+60 {
		t.Errorf("a device next to the probe is slowed too: %.1f ms (%.1f before)", a, baseA)
	}
	// the gateway's own traffic, which is what the diagnostics of the gateway are, does not meet it
	if out, err := exec.Command("ip", "netns", "exec", b.top.GW.Name, "ping", "-c", "3", "-W", "2", "-i", "0.2", "-n", srv).CombinedOutput(); err != nil || !pingRTT.MatchString(string(out)) {
		t.Errorf("the gateway cannot reach the server: %v %s", err, out)
	} else if m := pingRTT.FindStringSubmatch(string(out)); func() bool { v, _ := strconv.ParseFloat(m[1], 64); return v > base+60 }() {
		t.Errorf("the gateway's own traffic is slowed: %s", out)
	}
	del(lat)

	// loss of everything
	black := put("target: {device: probe-a}\nfault: {blackout: true}")
	if _, ok := avgRTT(t, probeANS, srv, 3); ok {
		t.Error("the probe reaches the server through a blackout")
	}
	if _, ok := avgRTT(t, b.top.A.Name, srv, 3); !ok {
		t.Error("a device is cut by the probe's blackout")
	}
	del(black)
	if _, ok := avgRTT(t, probeANS, srv, 3); !ok {
		t.Error("the probe does not reach the server after the fault was removed")
	}

	// a rule that drops what the probe sends to the uplink
	rule := put("target: {device: probe-a}\nrule: {protocol: icmp, action: drop}")
	if _, ok := avgRTT(t, probeANS, srv, 3); ok {
		t.Error("the probe pings through a rule that drops ICMP")
	}
	if _, ok := avgRTT(t, b.top.A.Name, srv, 3); !ok {
		t.Error("a device is cut by the rule of the probe")
	}
	del(rule)
	if _, ok := avgRTT(t, probeANS, srv, 3); !ok {
		t.Errorf("the probe does not reach the server after the rule was removed (%s)", d.Addresses[0])
	}
}

func medianOf(v []int64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return float64(s[len(s)/2])
}

// M25 test: the echo run of the executor, which the self-test is made of, sees the delay of the fault of the probe, and the
// gateway's own echo to the same server (the unimpaired reference) does not.
func TestTheEchoRunOfAProbeSeesTheDelayOfItsFaultAndTheGatewayDoesNot(t *testing.T) {
	b := newDHCPBed(t)
	b.probeBed(t)
	testbed.StartPlainEcho(t, b.top.Server, testbed.ServerAddr, 9100)
	run := func(probeNS, ns string) (sent, received int, median float64) {
		t.Helper()
		var res struct {
			Sent     int     `json:"sent"`
			Received int     `json:"received"`
			RTTUs    []int64 `json:"rtt_us"`
		}
		ex := apply.Local{E: b.ex}
		out, err := ex.Do(context.Background(), &executor.ProbeRun{Target: executor.Target{NS: ns}, Probe: probeNS, Kind: executor.ProbeEcho, Host: testbed.ServerAddr, Port: 9100, Count: 50, IntervalUs: 20000, SettleMs: 2000})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out.Data[0], &res); err != nil {
			t.Fatal(err)
		}
		return res.Sent, res.Received, medianOf(res.RTTUs) / 1000
	}
	gw := b.top.GW.Name
	sent, got, base := run(probeANS, gw)
	if sent != 50 || got != 50 {
		t.Fatalf("sent %d, received %d", sent, got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := b.e.PutOverlay(ctx, engine.OverlayWrite{Owner: model.Owner{Type: "user", Id: "admin"}, Request: overlayRequest(t, "target: {device: probe-a}\nfault: {latency: 100ms}")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = b.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{Type: "user", Id: "admin"})
	}()
	_, got, slow := run(probeANS, gw)
	if got != 50 || slow < base+150 {
		t.Errorf("the probe's median with 100 ms each way: %.1f ms (%.1f before), %d answers", slow, base, got)
	}
	// from the gateway (no probe): the same server, no fault
	_, got, ref := run("", gw)
	if got != 50 || ref > base+60 {
		t.Errorf("the gateway's reference: %.1f ms (the probe had %.1f before the fault), %d answers", ref, base, got)
	}
	// a diagnostic from the probe meets the fault as well
	dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dcancel()
	tcp := domainTCPRequest(testbed.ServerAddr, 9100)
	out, err := apply.Local{E: b.ex}.Do(dctx, &executor.ProbeRun{Target: executor.Target{NS: gw}, Probe: probeANS, Kind: executor.ProbeDiagnostic, Request: &tcp})
	if err != nil {
		t.Fatal(err)
	}
	var dres struct {
		Ok      bool `json:"ok"`
		Connect struct {
			RttMs float64 `json:"rtt_ms"`
		} `json:"connect"`
	}
	if err := json.Unmarshal(out.Data[0], &dres); err != nil {
		t.Fatal(err)
	}
	if !dres.Ok || dres.Connect.RttMs < base+150 {
		t.Errorf("a TCP check from the probe: %+v (reference %.1f ms)", dres, base)
	}
}

func domainTCPRequest(host string, port int) model.DiagnosticRequest {
	return model.DiagnosticRequest{Type: model.DiagnosticRequestTypeTcp, Host: host, Port: &port}
}
