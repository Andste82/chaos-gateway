package api_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// M26: GET /metrics (plan §2.12). The values are read from the same places as the REST API's views, so the
// tests compare the two, and drive the numbers from the simulated kernel (the named counters, the netem
// queues' statistics, the interface counters) to know what the endpoint has to say.

// sample is one series of a scrape.
type sample struct {
	name   string
	labels map[string]string
	value  float64
}

type scrape struct {
	t       *testing.T
	samples []sample
	types   map[string]string
}

var (
	sampleRE = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{(.*)\})? (\S+)$`)
	labelRE  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)
)

// parseScrape parses the text format and fails on anything that is not valid: a series without a TYPE before it, a
// series twice, a value that is no number.
func parseScrape(t *testing.T, body string) *scrape {
	t.Helper()
	sc := &scrape{t: t, types: map[string]string{}}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		switch {
		case line == "":
			t.Errorf("an empty line in the exposition")
		case strings.HasPrefix(line, "# HELP "):
		case strings.HasPrefix(line, "# TYPE "):
			f := strings.Fields(line)
			if len(f) != 4 || (f[3] != "counter" && f[3] != "gauge") {
				t.Errorf("bad TYPE line %q", line)
				continue
			}
			sc.types[f[2]] = f[3]
		default:
			m := sampleRE.FindStringSubmatch(line)
			if m == nil {
				t.Errorf("not a sample: %q", line)
				continue
			}
			if sc.types[m[1]] == "" {
				t.Errorf("%s has no TYPE before its first series", m[1])
			}
			if seen[line[:strings.LastIndex(line, " ")]] {
				t.Errorf("series twice: %q", line)
			}
			seen[line[:strings.LastIndex(line, " ")]] = true
			v, err := strconv.ParseFloat(m[4], 64)
			if err != nil {
				t.Errorf("value of %q: %v", line, err)
			}
			s := sample{name: m[1], labels: map[string]string{}, value: v}
			for _, l := range labelRE.FindAllStringSubmatch(m[3], -1) {
				s.labels[l[1]] = l[2]
			}
			sc.samples = append(sc.samples, s)
		}
	}
	return sc
}

// find returns the one series of a name that has the labels (and maybe more).
func (sc *scrape) find(name string, want ...string) (float64, bool) {
	sc.t.Helper()
	var hits []sample
next:
	for _, s := range sc.samples {
		if s.name != name {
			continue
		}
		for i := 0; i+1 < len(want); i += 2 {
			if s.labels[want[i]] != want[i+1] {
				continue next
			}
		}
		hits = append(hits, s)
	}
	if len(hits) > 1 {
		sc.t.Fatalf("%s %v: %d series, want one: %v", name, want, len(hits), hits)
	}
	if len(hits) == 0 {
		return 0, false
	}
	return hits[0].value, true
}

// val is find for a series that must be there.
func (sc *scrape) val(name string, want ...string) float64 {
	sc.t.Helper()
	v, ok := sc.find(name, want...)
	if !ok {
		sc.t.Fatalf("no series %s %v in %d samples", name, want, len(sc.samples))
	}
	return v
}

func (g *gw) scrape() *scrape {
	g.t.Helper()
	r := g.do("GET", "/metrics", nil, nil, nil)
	if r.Status != 200 {
		g.t.Fatalf("GET /metrics: %d %s", r.Status, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		g.t.Errorf("content type %q", ct)
	}
	return parseScrape(g.t, string(r.Body))
}

// procFiles writes a /proc with a load average and a memory info.
func procFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "loadavg"), []byte("0.52 1.25 2.5 1/200 12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal:        8000000 kB\nMemFree:          100000 kB\nMemAvailable:    4000000 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const metricsFault = "5f1c7a9e-6d3b-4e8a-9c2f-1a2b3c4d5e6f"

// metricsGateway is a gateway with the device esp32-42, a configured fault, an access rule and port forwards, applied.
func metricsGateway(t *testing.T, opts ...func(*options)) *gw {
	t.Helper()
	g := newGW(t, append([]func(*options){func(o *options) { o.procDir = procFiles(t) }}, opts...)...)
	g.finishSetup()
	patch := forwardsPatch()
	patch["networks"] = map[string]any{iotID: map[string]any{"dhcp": map[string]any{"lease_time": "10m"}}}
	patch["devices"] = map[string]any{espID: map[string]any{"name": "esp32-42", "network": iotID, "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:31"}}}}
	patch["faults"] = map[string]any{metricsFault: map[string]any{"name": "slow-lab", "source": map[string]any{"network": "IoT"}, "latency": "150ms", "destination": map[string]any{"cidr": "192.0.2.0/24"}}}
	patch["access_rules"] = map[string]any{ruleA: map[string]any{"name": "offline", "source": map[string]any{"network": "IoT"}, "destination": map[string]any{"cidr": "198.51.100.0/24"}, "action": "drop"}}
	patch["access_rule_order"] = []string{ruleA}
	if r := g.apply(g.mustPatch(patch)); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	return g
}

// The counters of the endpoint are the counters the REST API shows, and they follow the traffic: the values the
// simulated kernel is given come out as they are, and a second scrape after more traffic shows exactly the difference.
func TestMetricsReportTheCountersOfFaultsRulesForwardsAndQueuesAgainstKnownTraffic(t *testing.T) {
	g := metricsGateway(t)
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"100ms","loss":"1%","destination":{"cidr":"198.51.100.0/24"}}}`)
	ovID := ov["id"].(string)
	var cfgFault, ovFault *compiler.Fault
	snap := g.e.Snapshot()
	for i := range snap.Faults {
		switch snap.Faults[i].Source {
		case ovID:
			ovFault = &snap.Faults[i]
		case metricsFault:
			cfgFault = &snap.Faults[i]
		}
	}
	if cfgFault == nil || ovFault == nil {
		t.Fatalf("faults %+v", snap.Faults)
	}
	devs := snap.TCDevs
	if len(devs) == 0 {
		t.Fatal("no tc tree")
	}
	g.k.BumpCounter(cfgFault.CounterUp, 4)
	g.k.BumpCounter(cfgFault.CounterDown, 3)
	g.k.BumpCounter(ovFault.CounterUp, 11)
	leaf := func(f *compiler.Fault, dir compiler.Direction) string {
		return strings.TrimPrefix(compiler.ClassIDOf(f.ID, dir), "1:") + ":"
	}
	g.k.SetTCStats(devs[0], leaf(ovFault, compiler.Upload), linux.NormStats{Bytes: 14200, Packets: 100, Drops: 4, Overlimits: 1, Backlog: 2900, Qlen: 2})
	g.k.BumpCounter(compiler.ForwardCounterName(fwdRange), 3)
	g.k.BumpCounter(compiler.AntiLockoutRule.Counter, 7)
	for _, r := range snap.Access.Rules {
		if r.ID == ruleA {
			g.k.BumpCounter(r.Counter, 5)
		}
	}
	g.k.SetLinkStats("wan0", linux.LinkStats{})
	st := linux.LinkStats{}
	st.RX.Bytes, st.RX.Packets, st.RX.Errors, st.RX.Dropped = 1000, 10, 2, 3
	st.TX.Bytes, st.TX.Packets, st.TX.Errors, st.TX.Dropped = 2000, 20, 4, 5
	g.k.SetLinkStats("br-iot", st)
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	g.k.SetConntrack("tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1\n")
	g.observe()

	m := g.scrape()
	// the faults: the same numbers as the faults' and overlays' views
	cf := g.do("GET", "/faults/slow-lab", nil, nil, nil).json(t)["counters"].(map[string]any)
	if p := m.val("chaosgw_fault_packets_total", "layer", "config", "id", metricsFault, "kind", "impairment", "name", "slow-lab"); p != 7 || p != cf["packets"].(float64) {
		t.Errorf("config fault packets %v, the API says %v", p, cf)
	}
	if b := m.val("chaosgw_fault_bytes_total", "layer", "config", "id", metricsFault); b != 700 || b != cf["bytes"].(float64) {
		t.Errorf("config fault bytes %v, the API says %v", b, cf)
	}
	oc := g.do("GET", "/overlays/"+ovID, nil, nil, nil).json(t)["counters"].(map[string]any)
	if p := m.val("chaosgw_fault_packets_total", "layer", "overlay", "id", ovID, "kind", "fault"); p != 11 || p != oc["packets"].(float64) {
		t.Errorf("overlay fault packets %v, the API says %v", p, oc)
	}
	// an access rule and the anti-lockout rule: the new connections each decided
	rc := map[string]any{}
	for _, it := range g.do("GET", "/rules", nil, nil, nil).json(t)["items"].([]any) {
		if it.(map[string]any)["id"] == ruleA {
			rc = it.(map[string]any)["counters"].(map[string]any)
		}
	}
	if p := m.val("chaosgw_rule_packets_total", "layer", "config", "id", ruleA, "name", "offline", "action", "drop"); p != 5 || p != rc["packets"].(float64) {
		t.Errorf("rule packets %v, the API says %v", p, rc)
	}
	if p := m.val("chaosgw_rule_packets_total", "layer", "system", "id", compiler.AntiLockoutRule.Key); p != 7 {
		t.Errorf("anti-lockout packets %v", p)
	}
	// a port forward
	if p := m.val("chaosgw_forward_connections_total", "id", fwdRange, "name", "range", "entry", "management"); p != 3 {
		t.Errorf("forward connections %v", p)
	}
	if b := m.val("chaosgw_forward_bytes_total", "id", fwdRange); b != 300 {
		t.Errorf("forward bytes %v", b)
	}
	if _, ok := m.find("chaosgw_forward_connections_total", "id", fwdOff); ok {
		t.Error("a disabled forward is not in the kernel and has no series")
	}
	// the netem queues: the statistics the kernel holds, with the epoch of the queue
	ql := []string{"fault_id", strconv.Itoa(ovFault.ID), "interface", devs[0], "direction", "upload"}
	for name, want := range map[string]float64{"sent_packets_total": 100, "sent_bytes_total": 14200, "dropped_packets_total": 4, "overlimits_total": 1} {
		if v := m.val("chaosgw_queue_"+name, ql...); v != want {
			t.Errorf("queue %s = %v, want %v", name, v, want)
		}
	}
	if v := m.val("chaosgw_queue_backlog_packets", ql...); v != 2 {
		t.Errorf("backlog packets %v", v)
	}
	if v := m.val("chaosgw_queue_backlog_bytes", ql...); v != 2900 {
		t.Errorf("backlog bytes %v", v)
	}
	if v := m.val("chaosgw_queue_epoch", ql...); v == 0 {
		t.Errorf("queue epoch %v", v)
	}
	// the counters' epoch is the one /state reports
	if e := m.val("chaosgw_counter_epoch"); e == 0 || e != g.do("GET", "/state", nil, nil, nil).json(t)["counter_epoch"].(float64) {
		t.Errorf("counter epoch %v", e)
	}

	// the interfaces: the counters of the kernel, by the role the interface has
	il := []string{"interface", "br-iot", "role", "network"}
	for name, want := range map[string]float64{"receive_bytes_total": 1000, "transmit_bytes_total": 2000, "receive_packets_total": 10, "transmit_packets_total": 20,
		"receive_errors_total": 2, "transmit_errors_total": 4, "receive_dropped_total": 3, "transmit_dropped_total": 5} {
		if v := m.val("chaosgw_interface_"+name, il...); v != want {
			t.Errorf("interface %s = %v, want %v", name, v, want)
		}
	}
	if m.val("chaosgw_interface_up", il...) != 1 {
		t.Error("br-iot is up")
	}
	if _, ok := m.find("chaosgw_interface_receive_bytes_total", "interface", "wan0", "role", "uplink"); !ok {
		t.Error("the uplink has counters")
	}

	// the device
	dev := g.do("GET", "/devices/esp32-42", nil, nil, nil).json(t)["observed"].(map[string]any)
	dl := []string{"device", espID, "name", "esp32-42"}
	if m.val("chaosgw_device_online", dl...) != 1 || m.val("chaosgw_device_flows_active", dl...) != dev["flows_active"].(float64) || m.val("chaosgw_device_flows_active", dl...) != 1 {
		t.Errorf("device %v", dev)
	}
	if v, ok := dev["upload_bps"].(float64); !ok || m.val("chaosgw_device_upload_bytes_per_second", dl...) != v || m.val("chaosgw_device_download_bytes_per_second", dl...) != dev["download_bps"].(float64) {
		t.Errorf("rates %v", dev)
	}
	if m.val("chaosgw_flows_active") != 1 || m.val("chaosgw_overlays_active") != 1 || m.val("chaosgw_devices_online") != 1 {
		t.Error("totals")
	}

	// the system
	// the health is the one GET /system/health reports (the DNS proxy has not polled in this harness: degraded)
	health := g.do("GET", "/system/health", nil, nil, nil).json(t)
	if (m.val("chaosgw_health_ok") == 1) != (health["status"] == "healthy") {
		t.Errorf("health %v", health["status"])
	}
	for _, c := range health["components"].([]any) {
		comp := c.(map[string]any)
		if m.val("chaosgw_component_status", "component", comp["name"].(string), "status", comp["status"].(string)) != 1 {
			t.Errorf("component %v", comp)
		}
	}
	if m.val("chaosgw_component_status", "component", "api", "status", "healthy") != 1 || m.val("chaosgw_component_status", "component", "api", "status", "unhealthy") != 0 {
		t.Error("the api component")
	}
	if m.val("chaosgw_system_load1") != 0.52 || m.val("chaosgw_system_load5") != 1.25 || m.val("chaosgw_system_load15") != 2.5 {
		t.Error("load")
	}
	if m.val("chaosgw_system_memory_total_bytes") != 8000000*1024 || m.val("chaosgw_system_memory_available_bytes") != 4000000*1024 || m.val("chaosgw_system_cpus") < 1 {
		t.Error("memory")
	}
	if m.val("chaosgw_disk_total_bytes", "volume", "state") <= 0 || m.val("chaosgw_disk_free_bytes", "volume", "captures") < 0 {
		t.Error("disk")
	}
	if m.val("chaosgw_build_info", "version", "test") != 1 || m.val("chaosgw_revision") != float64(g.activeID()) || m.val("chaosgw_apply_ok") != 1 {
		t.Error("build and revision")
	}
	for _, src := range []string{"counters", "queues", "interfaces"} {
		if m.val("chaosgw_collector_up", "source", src) != 1 {
			t.Errorf("collector %s", src)
		}
	}
	if m.types["chaosgw_fault_packets_total"] != "counter" || m.types["chaosgw_queue_backlog_packets"] != "gauge" || m.types["chaosgw_device_online"] != "gauge" {
		t.Errorf("types %v", m.types)
	}

	// more traffic: the counters rise by exactly what the kernel counted
	g.k.BumpCounter(cfgFault.CounterUp, 6)
	g.k.BumpCounter(compiler.ForwardCounterName(fwdRange), 2)
	g.k.SetTCStats(devs[0], leaf(ovFault, compiler.Upload), linux.NormStats{Bytes: 15000, Packets: 105, Drops: 6, Overlimits: 1, Backlog: 0, Qlen: 0})
	m2 := g.scrape()
	if d := m2.val("chaosgw_fault_packets_total", "id", metricsFault) - 7; d != 6 {
		t.Errorf("config fault rose by %v", d)
	}
	if d := m2.val("chaosgw_forward_connections_total", "id", fwdRange) - 3; d != 2 {
		t.Errorf("forward rose by %v", d)
	}
	if m2.val("chaosgw_queue_sent_packets_total", ql...)-100 != 5 || m2.val("chaosgw_queue_dropped_packets_total", ql...)-4 != 2 || m2.val("chaosgw_queue_backlog_packets", ql...) != 0 {
		t.Error("queue after more traffic")
	}
	if m2.val("chaosgw_rule_packets_total", "id", ruleA) != 5 || m2.val("chaosgw_queue_epoch", ql...) != m.val("chaosgw_queue_epoch", ql...) {
		t.Error("what did not change stays")
	}
}

// The profile, DNS and TLS overlays count as the overlays' views count them, and a rule overlay is a rule.
func TestMetricsCountOverlaysByKind(t *testing.T) {
	g := metricsGateway(t)
	fault := g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"100ms"}}`)["id"].(string)
	rule := g.mustCreateOverlay(`{"target":{"network":"IoT"},"rule":{"protocol":"udp","ports":[53],"action":"drop"}}`)["id"].(string)
	for _, r := range g.e.Snapshot().Access.Rules {
		if r.ID == rule {
			g.k.BumpCounter(r.Counter, 9)
		}
	}
	m := g.scrape()
	if _, ok := m.find("chaosgw_fault_packets_total", "layer", "overlay", "id", fault, "kind", "fault"); !ok {
		t.Error("the fault overlay has a series")
	}
	if v := m.val("chaosgw_rule_packets_total", "layer", "overlay", "id", rule); v != 9 {
		t.Errorf("the rule overlay counted %v", v)
	}
	if m.val("chaosgw_overlays_active") != 2 {
		t.Error("two overlays")
	}
}

// A scrape needs a token; any scope reads it.
func TestMetricsNeedAToken(t *testing.T) {
	g := metricsGateway(t)
	token := g.token
	g.token = ""
	if r := g.do("GET", "/metrics", nil, nil, nil); r.Status != 401 {
		t.Errorf("anonymous: %d %s", r.Status, r.Body)
	}
	g.mintToken("read")
	if r := g.do("GET", "/metrics", nil, nil, nil); r.Status != 200 {
		t.Errorf("a token of the scope read: %d %s", r.Status, r.Body)
	}
	g.token = token
}

// The capabilities name the endpoint.
func TestTheCapabilitiesNameMetrics(t *testing.T) {
	g := ready(t)
	if !strings.Contains(string(g.do("GET", "/capabilities", nil, nil, nil).Body), `"metrics"`) {
		t.Error("the feature metrics")
	}
}

// failingNft fails the engine's read of the nft tables while failing is set: the named counters cannot be read.
type failingNft struct {
	inner   apply.Exec
	failing *atomic.Bool
}

func (f failingNft) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if f.failing.Load() {
		for _, op := range ops {
			if r, ok := op.(*executor.Read); ok && r.What == executor.ReadNft {
				return executor.Outcome{}, errors.New("nft does not answer")
			}
		}
	}
	return f.inner.Do(ctx, ops...)
}

// Counters that cannot be read are left out, not reported as zero, and the collector says so.
func TestMetricsLeaveOutWhatCannotBeReadAndSayWhichSourceFailed(t *testing.T) {
	var failing atomic.Bool
	g := metricsGateway(t, func(o *options) {
		o.execWrap = func(e apply.Exec) apply.Exec { return failingNft{inner: e, failing: &failing} }
	})
	if m := g.scrape(); m.val("chaosgw_collector_up", "source", "counters") != 1 {
		t.Fatal("counters are read")
	}
	failing.Store(true)
	m := g.scrape()
	if m.val("chaosgw_collector_up", "source", "counters") != 0 {
		t.Error("the collector of the counters is down")
	}
	for _, s := range m.samples {
		if strings.HasPrefix(s.name, "chaosgw_fault_") || strings.HasPrefix(s.name, "chaosgw_rule_") || strings.HasPrefix(s.name, "chaosgw_forward_") || strings.HasPrefix(s.name, "chaosgw_queue_") {
			t.Errorf("%s is reported although the counters could not be read", s.name)
		}
	}
	if _, ok := m.find("chaosgw_build_info"); !ok {
		t.Error("the rest of the metrics is still there")
	}
}

// A connection that came in through a port forward is a flow of the target device (M26): the flow view lists it, so it can be
// explained; a connection from outside that no forward translated is not listed.
func TestAForwardedConnectionIsAFlowOfTheTargetDevice(t *testing.T) {
	g := dhcpGateway(t)
	if r := g.apply(g.mustPatch(forwardsPatch())); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.k.SetNeighbors([]linux.Neighbor{nb("10.10.0.31", "02:00:00:00:00:31")})
	g.observe()
	g.k.SetConntrack("tcp      6 431999 ESTABLISHED src=198.51.100.7 dst=203.0.113.1 sport=51000 dport=8080 packets=6 bytes=412 src=10.10.0.31 dst=198.51.100.7 sport=80 dport=51000 packets=4 bytes=500 [ASSURED] mark=0 use=1\n" +
		"tcp      6 431999 ESTABLISHED src=198.51.100.9 dst=203.0.113.1 sport=51001 dport=22 packets=1 bytes=60 src=203.0.113.1 dst=198.51.100.9 sport=22 dport=51001 packets=1 bytes=60 [ASSURED] mark=0 use=1\n" +
		"tcp      6 431999 ESTABLISHED src=10.10.0.31 dst=203.0.113.10 sport=45566 dport=8883 packets=6 bytes=412 src=203.0.113.10 dst=203.0.113.1 sport=8883 dport=45566 packets=4 bytes=500 [ASSURED] mark=0 use=1\n")
	items := g.do("GET", "/flows?device=esp32-42", nil, nil, nil).json(t)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("the flows of the device: %v", items)
	}
	var fwd, own map[string]any
	for _, it := range items {
		f := it.(map[string]any)
		if f["forwarded"] == true {
			fwd = f
		} else {
			own = f
		}
	}
	if fwd == nil || own == nil || fwd["src"] != "198.51.100.7" || fwd["dst"] != "203.0.113.1" || fwd["dport"] != float64(8080) || fwd["device"] != espID || fwd["network"] != iotID {
		t.Fatalf("forwarded %v, own %v", fwd, own)
	}
	if own["forwarded"] != nil {
		t.Errorf("a connection of the device is not forwarded: %v", own)
	}
	if all := g.do("GET", "/flows", nil, nil, nil).json(t)["items"].([]any); len(all) != 2 {
		t.Errorf("the connection to the gateway's own port is no flow: %v", all)
	}
	// the flow is explained with its own tuple: the forward, and the rule that decides it
	ex := g.do("GET", "/explain?src="+fwd["src"].(string)+"&dst="+fwd["dst"].(string)+"&protocol=tcp&port=8080", nil, nil, nil).json(t)
	if fw, ok := ex["forward"].(map[string]any); !ok || fw["id"] != fwdWeb {
		t.Errorf("explain %v", ex)
	}
}
