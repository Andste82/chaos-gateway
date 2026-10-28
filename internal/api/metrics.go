package api

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/metrics"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The metrics endpoint (plan §2.12, M26): the counters of the faults, overlays, access rules and port forwards
// (M7 to M9, M35a), the statistics of the netem queues, the counters of the interfaces, the state of the devices
// and the health of the system, in the Prometheus text format. Nothing is kept between two scrapes: every value is
// read from the engine's snapshot and the kernel at the moment of the scrape, by the code that serves the same
// numbers over the REST API (the views of overlays, faults, rules and forwards), so the endpoint and the API
// cannot disagree. A counter that cannot be read is left out, and `chaosgw_collector_up` says which source
// failed; every source is read within a deadline, so a read that hangs leaves its source out instead of failing the scrape;
// a counter that starts over (a reboot, a recreated object) is a counter reset for Prometheus, and
// `chaosgw_counter_epoch` is the epoch the API reports.

// defaultMetricsReadTimeout bounds each source a scrape reads (the counters and queues, the links, the health): a few of them in
// turn stay below the 10 s that Prometheus waits for a scrape by default.
const defaultMetricsReadTimeout = 3 * time.Second

// bounded runs read with a context that ends after the deadline of a source read (Config.MetricsReadTimeout).
func (s *Server) bounded(ctx context.Context, read func(context.Context)) {
	d := s.cfg.MetricsReadTimeout
	if d <= 0 {
		d = defaultMetricsReadTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	read(ctx)
}

// faultHelp is the help text of the chaosgw_fault_* families.
const faultHelp = "Counted into a fault, a profile activation or another overlay (DNS and TLS cases count answers and handshakes as packets), in both directions."

// labels makes the label set of a series; a label with an empty value is left out (it is no label for Prometheus).
func labels(kv ...string) []metrics.Label {
	var out []metrics.Label
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			out = append(out, metrics.L(kv[i], kv[i+1]))
		}
	}
	return out
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// GetMetrics implements GET /metrics.
func (s *Server) GetMetrics(c *gin.Context) {
	ctx := contextOf(c)
	m := metrics.NewSet()
	snap := s.cfg.Engine.Snapshot()
	m.Gauge("chaosgw_build_info", "The build of this gateway: always 1.", 1, labels("version", s.cfg.Version, "commit", s.cfg.Commit)...)
	m.Gauge("chaosgw_start_time_seconds", "When the gateway process started, in seconds since the Unix epoch.", float64(s.cfg.Started.Unix()))
	m.Gauge("chaosgw_uptime_seconds", "Seconds since the gateway process started.", s.clk.Now().Sub(s.cfg.Started).Seconds())
	m.Gauge("chaosgw_revision", "The active configuration revision (0 before the first one).", float64(snap.Revision))
	m.Gauge("chaosgw_apply_ok", "1 while the last apply succeeded, 0 while it failed.", boolValue(snap.LastError == ""))
	m.Gauge("chaosgw_counter_epoch", "The epoch of the counters as a whole: it changes when they start over.", float64(snap.CounterEpoch))

	s.bounded(ctx, func(ctx context.Context) { s.collectCounters(ctx, m, snap) })
	s.bounded(ctx, func(ctx context.Context) { s.collectInterfaces(ctx, m, snap) })
	s.collectDevices(m, snap)
	s.collectSystem(ctx, m)
	if err := m.Err(); err != nil {
		s.log.Warn("a metric was left out", "error", err)
	}
	c.Header("Content-Type", metrics.ContentType)
	c.Status(200)
	_, _ = m.WriteTo(c.Writer)
}

// collectCounters reports the named nft counters and the netem queues the way the views of the REST API read them.
func (s *Server) collectCounters(ctx context.Context, m *metrics.Set, snap *engine.Snapshot) {
	oc := s.readOverlayContext(ctx, true)
	m.Gauge("chaosgw_collector_up", "1 while a source of metrics could be read at this scrape, 0 while it could not.", boolValue(!oc.countersFailed), metrics.L("source", "counters"))
	if oc.countersFailed {
		return
	}
	if len(snap.TCDevs) > 0 {
		m.Gauge("chaosgw_collector_up", "", boolValue(oc.queues != nil), metrics.L("source", "queues"))
	}
	counter := func(name, help string, c *model.Counter, l []metrics.Label) {
		if c == nil {
			return
		}
		m.Counter(name+"_packets_total", help+" Packets.", float64(c.Packets), l...)
		m.Counter(name+"_bytes_total", help+" Bytes.", float64(c.Bytes), l...)
	}

	// the configured faults that are in the kernel, and the overlays by kind
	seen := map[string]bool{}
	cfgFaults := map[string]model.ConfigFault{}
	if snap.Config != nil {
		cfgFaults = deref(snap.Config.Faults)
	}
	addConfig := func(source string) {
		if seen[source] {
			return
		}
		seen[source] = true
		f := cfgFaults[source]
		kind := faultFamilyOf(f)
		counter("chaosgw_fault", faultHelp, oc.counterOf("config", source),
			labels("layer", "config", "id", source, "kind", kind, "name", deref(f.Name)))
	}
	for _, f := range snap.Faults {
		if f.Layer == "config" {
			addConfig(f.Source)
		}
	}
	for _, f := range snap.PMTU {
		if f.Layer == "config" {
			addConfig(f.Source)
		}
	}
	for _, ov := range snap.Overlays {
		id := ov.Id.String()
		l := labels("layer", "overlay", "id", id, "kind", string(ov.Kind))
		switch ov.Kind {
		case model.OverlayKindFault:
			counter("chaosgw_fault", faultHelp, oc.counterOf("overlay", id), l)
		case model.OverlayKindProfile:
			counter("chaosgw_fault", faultHelp, oc.withTLS(oc.withDNS(oc.counterOf("overlay", id), id), id), l)
		case model.OverlayKindDns:
			counter("chaosgw_fault", faultHelp, oc.withDNS(nil, id), l)
		case model.OverlayKindTls:
			counter("chaosgw_fault", faultHelp, oc.withTLS(nil, id), l)
		case model.OverlayKindWireguard:
			for _, a := range snap.WGActions {
				if a.Counter != "" && strings.EqualFold(a.Overlay, id) {
					v := oc.counters[a.Counter]
					counter("chaosgw_fault", faultHelp, &model.Counter{Packets: v.Packets, Bytes: v.Bytes}, l)
				}
			}
		}
	}

	// the access rules (configured and overlay) and the anti-lockout rule: the new connections each decided
	if snap.Access != nil {
		for _, r := range snap.Access.Rules {
			counter("chaosgw_rule", "Counted into an access rule: the new connections it decided.", oc.ruleCounter(r),
				labels("layer", r.Layer, "id", r.ID, "name", r.Name, "action", r.Action))
		}
	}
	if v, ok := oc.counters[compiler.AntiLockoutRule.Counter]; ok {
		counter("chaosgw_rule", "Counted into an access rule: the new connections it decided.", &model.Counter{Packets: v.Packets, Bytes: v.Bytes},
			labels("layer", "system", "id", compiler.AntiLockoutRule.Key, "name", compiler.AntiLockoutRule.Name, "action", "allow"))
	}

	// the port forwards: the connections that arrived through each
	for _, f := range snap.Forwards {
		if c := oc.forwardCounter(f.ID); c != nil {
			l := labels("id", f.ID, "name", f.Name, "entry", f.Entry)
			m.Counter("chaosgw_forward_connections_total", "Connections that arrived through a port forward.", float64(c.Packets), l...)
			m.Counter("chaosgw_forward_bytes_total", "Bytes of the first packets of the connections that arrived through a port forward.", float64(c.Bytes), l...)
		}
	}

	// the netem queues, one per fault id, direction and interface of the tree
	if oc.queues != nil {
		for _, f := range snap.Faults {
			for _, dir := range []compiler.Direction{compiler.Upload, compiler.Download} {
				if (dir == compiler.Upload && f.Upload == nil) || (dir == compiler.Download && f.Download == nil) {
					continue
				}
				for _, dev := range snap.TCDevs {
					key := engine.QueueKey(dev, compiler.ClassIDOf(f.ID, dir))
					st, ok := oc.queues[key]
					if !ok {
						continue
					}
					l := labels("fault_id", strconv.Itoa(f.ID), "layer", f.Layer, "source", f.Source, "interface", dev, "direction", dirName(dir))
					m.Counter("chaosgw_queue_sent_packets_total", "Packets that left a netem queue.", float64(st.Packets), l...)
					m.Counter("chaosgw_queue_sent_bytes_total", "Bytes of the packets that left a netem queue.", float64(st.Bytes), l...)
					m.Counter("chaosgw_queue_dropped_packets_total", "Packets a netem queue dropped: the loss the fault configures and the packets that did not fit the queue limit.", float64(st.Drops), l...)
					m.Counter("chaosgw_queue_overlimits_total", "Overlimits of a netem queue.", float64(st.Overlimits), l...)
					m.Gauge("chaosgw_queue_backlog_packets", "Packets held in a netem queue now.", float64(st.Qlen), l...)
					m.Gauge("chaosgw_queue_backlog_bytes", "Bytes held in a netem queue now.", float64(st.Backlog), l...)
					m.Gauge("chaosgw_queue_epoch", "The epoch of a netem queue: its counters start over when it changes.", float64(snap.QueueEpochs[key]), l...)
				}
			}
		}
	}
}

func dirName(d compiler.Direction) string {
	if d == compiler.Download {
		return "download"
	}
	return "upload"
}

// collectInterfaces reports the counters of the interfaces the gateway applied: the uplink, the management
// interface, the bridges of the test networks and the WireGuard interfaces.
func (s *Server) collectInterfaces(ctx context.Context, m *metrics.Set, snap *engine.Snapshot) {
	want := map[string]string{}
	if snap.Applied != nil && snap.Applied.Uplink.Name != "" {
		want[snap.Applied.Uplink.Name] = "uplink"
	}
	if snap.Management.Name != "" {
		if _, ok := want[snap.Management.Name]; !ok {
			want[snap.Management.Name] = "management"
		}
	}
	for _, b := range snap.Bridges {
		want[b.Name] = "network"
	}
	for _, w := range snap.WireGuardInterfaces {
		want[w.Name] = "wireguard"
	}
	if len(want) == 0 {
		return
	}
	out, err := s.cfg.Exec.Do(ctx, &executor.Read{Target: executor.Target{NS: s.cfg.Namespace}, What: executor.ReadLinks})
	var links []linux.Link
	if err == nil && len(out.Data) > 0 {
		links, err = linux.ParseLinks(out.Data[0])
	}
	m.Gauge("chaosgw_collector_up", "", boolValue(err == nil), metrics.L("source", "interfaces"))
	if err != nil {
		s.log.Warn("cannot read the interface counters", "error", err)
		return
	}
	for _, l := range links {
		role, ok := want[l.Name]
		if !ok || l.Stats == nil {
			continue
		}
		lb := labels("interface", l.Name, "role", role)
		m.Gauge("chaosgw_interface_up", "1 while the interface is up and has a carrier.", boolValue(l.Running()), lb...)
		m.Counter("chaosgw_interface_receive_bytes_total", "Bytes the interface received.", float64(l.Stats.RX.Bytes), lb...)
		m.Counter("chaosgw_interface_transmit_bytes_total", "Bytes the interface sent.", float64(l.Stats.TX.Bytes), lb...)
		m.Counter("chaosgw_interface_receive_packets_total", "Packets the interface received.", float64(l.Stats.RX.Packets), lb...)
		m.Counter("chaosgw_interface_transmit_packets_total", "Packets the interface sent.", float64(l.Stats.TX.Packets), lb...)
		m.Counter("chaosgw_interface_receive_errors_total", "Receive errors of the interface.", float64(l.Stats.RX.Errors), lb...)
		m.Counter("chaosgw_interface_transmit_errors_total", "Transmit errors of the interface.", float64(l.Stats.TX.Errors), lb...)
		m.Counter("chaosgw_interface_receive_dropped_total", "Packets the interface dropped on receive.", float64(l.Stats.RX.Dropped), lb...)
		m.Counter("chaosgw_interface_transmit_dropped_total", "Packets the interface dropped on transmit.", float64(l.Stats.TX.Dropped), lb...)
	}
}

// collectDevices reports the devices the gateway sees (plan §2.12: "per device: traffic rates, current flows").
func (s *Server) collectDevices(m *metrics.Set, snap *engine.Snapshot) {
	online := 0
	for _, d := range snap.Devices {
		lb := labels("device", d.ID, "name", d.Name, "network", d.Network, "origin", string(d.Origin))
		m.Gauge("chaosgw_device_online", "1 while the device gave a sign of life within the last 60 s.", boolValue(d.Online), lb...)
		if d.Online {
			online++
		}
		if d.TrafficKnown {
			m.Gauge("chaosgw_device_upload_bytes_per_second", "Bytes per second the device sent, averaged over the conntrack reads of the last 10 s.", float64(d.UploadBps), lb...)
			m.Gauge("chaosgw_device_download_bytes_per_second", "Bytes per second the device received, averaged over the conntrack reads of the last 10 s.", float64(d.DownloadBps), lb...)
			m.Gauge("chaosgw_device_flows_active", "Tracked connections the device is a party to; one between two devices counts for both.", float64(d.FlowsActive), lb...)
		}
	}
	m.Gauge("chaosgw_devices", "Devices the gateway knows, configured and discovered.", float64(len(snap.Devices)))
	m.Gauge("chaosgw_devices_online", "Devices that are online.", float64(online))
	if snap.FlowsKnown {
		m.Gauge("chaosgw_flows_active", "Tracked connections that have a device as a party, each counted once; left out while unknown.", float64(snap.FlowsActive))
	}
	m.Gauge("chaosgw_overlays_active", "Overlays in force.", float64(len(snap.Overlays)))
	m.Gauge("chaosgw_leases", "DHCP leases of the last observation.", float64(len(snap.Leases)))
	running, queued, _ := s.runs.Busy()
	m.Gauge("chaosgw_runs_active", "Scenario runs that are running.", float64(running))
	m.Gauge("chaosgw_runs_queued", "Scenario runs that are queued.", float64(queued))
}

// collectSystem reports the health of the components, the load, the memory and the disks.
func (s *Server) collectSystem(ctx context.Context, m *metrics.Set) {
	overall, comps := s.healthReport(ctx)
	m.Gauge("chaosgw_health_ok", "1 while the overall health is healthy, 0 while it is degraded or unhealthy.", boolValue(overall == "healthy"))
	for _, c := range comps {
		for _, st := range []string{"healthy", "degraded", "unhealthy", "disabled"} {
			m.Gauge("chaosgw_component_status", "1 for the status a component of the gateway has, 0 for the others.", boolValue(c.Status == st),
				metrics.L("component", c.Name), metrics.L("status", st))
		}
	}
	m.Gauge("chaosgw_system_cpus", "Logical CPUs.", float64(runtime.NumCPU()))
	proc := s.cfg.ProcDir
	if proc == "" {
		proc = "/proc"
	}
	if load, ok := readLoadAvg(filepath.Join(proc, "loadavg")); ok {
		for i, n := range []string{"1", "5", "15"} {
			m.Gauge("chaosgw_system_load"+n, "The load average of the last "+n+" minutes.", load[i])
		}
	}
	if total, avail, ok := readMemInfo(filepath.Join(proc, "meminfo")); ok {
		m.Gauge("chaosgw_system_memory_total_bytes", "Memory of the machine.", total)
		m.Gauge("chaosgw_system_memory_available_bytes", "Memory available to programs without swapping.", avail)
	}
	disks := map[string]string{"state": s.cfg.StateDir, "captures": s.cfg.CapturesDir}
	names := make([]string, 0, len(disks))
	for n := range disks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		free, total, ok := s.diskUsage(n, disks[n])
		if !ok {
			continue
		}
		m.Gauge("chaosgw_disk_free_bytes", "Free space of a volume of the gateway, for the programs that may write to it.", float64(free), metrics.L("volume", n))
		m.Gauge("chaosgw_disk_total_bytes", "Size of a volume of the gateway.", float64(total), metrics.L("volume", n))
	}
}

// diskUsage is the free and the total space of a volume: the capture volume as the capture manager reads it.
func (s *Server) diskUsage(name, dir string) (free, total uint64, ok bool) {
	if name == "captures" && s.cfg.CaptureDiskFree != nil {
		f, t, err := s.cfg.CaptureDiskFree()
		return f, t, err == nil
	}
	if dir == "" {
		return 0, 0, false
	}
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, false
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), true
}

// readLoadAvg reads the three load averages of /proc/loadavg.
func readLoadAvg(path string) (load [3]float64, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return load, false
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return load, false
	}
	for i := range load {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return load, false
		}
		load[i] = v
	}
	return load, true
}

// readMemInfo reads MemTotal and MemAvailable of /proc/meminfo, in bytes.
func readMemInfo(path string) (total, avail float64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = f.Close() }()
	var haveT, haveA bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total, haveT = kb*1024, true
		case "MemAvailable":
			avail, haveA = kb*1024, true
		}
	}
	return total, avail, haveT && haveA
}
