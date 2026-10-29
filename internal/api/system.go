package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/preflight"
	"github.com/Andste82/chaos-gateway/internal/run"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/supervisor"
)

// healthProbeCache de-duplicates and rate-limits the unauthenticated health check's executor probe
// (M5-13): at most one real probe every 2 seconds, with concurrent callers sharing one in-flight one.
type healthProbeCache struct {
	mu    sync.Mutex
	at    time.Time
	err   error
	group singleflight.Group
}

func (h *healthProbeCache) check(ctx context.Context, now time.Time, probe func(context.Context) error) error {
	h.mu.Lock()
	if !h.at.IsZero() && now.Sub(h.at) < 2*time.Second {
		err := h.err
		h.mu.Unlock()
		return err
	}
	h.mu.Unlock()
	_, err, _ := h.group.Do("probe", func() (any, error) {
		perr := probe(ctx)
		h.mu.Lock()
		h.at, h.err = now, perr
		h.mu.Unlock()
		return nil, perr
	})
	return err
}

// osConfigCache de-duplicates and rate-limits the unauthenticated GET /setup's read of the host's network
// configuration through the executor, as healthProbeCache does the health check's probe (M5-13, Phase 3
// audit): at most one real read every 2 seconds, with concurrent callers sharing one in-flight one.
type osConfigCache struct {
	mu    sync.Mutex
	at    time.Time
	osc   osConfig
	group singleflight.Group
}

func (o *osConfigCache) get(ctx context.Context, now time.Time, read func(context.Context) osConfig) osConfig {
	if osc, ok := o.fresh(now); ok {
		return osc
	}
	return o.load(ctx, now, read)
}

// fresh returns the cached read while it is younger than 2 seconds.
func (o *osConfigCache) fresh(now time.Time) (osConfig, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.at.IsZero() && now.Sub(o.at) >= 0 && now.Sub(o.at) < 2*time.Second {
		return o.osc, true
	}
	return osConfig{}, false
}

// load reads, shared with the callers already waiting for a read. It looks at the cache again first: a
// caller that found it stale may reach the group only after another caller's read has finished and filled
// it, and the group shares only a read that is still in flight.
func (o *osConfigCache) load(ctx context.Context, now time.Time, read func(context.Context) osConfig) osConfig {
	v, _, _ := o.group.Do("read", func() (any, error) {
		if osc, ok := o.fresh(now); ok {
			return osc, nil
		}
		osc := read(context.WithoutCancel(ctx))
		o.mu.Lock()
		o.at, o.osc = now, osc
		o.mu.Unlock()
		return osc, nil
	})
	return v.(osConfig)
}

type lastApply struct {
	At         *time.Time `json:"at,omitempty"`
	Error      string     `json:"error,omitempty"`
	Generation int64      `json:"generation,omitempty"`
	Result     string     `json:"result"`
	Revision   int64      `json:"revision,omitempty"`
	DurationMs int64      `json:"duration_ms,omitempty"`
}

type pendingConfirm struct {
	Deadline time.Time `json:"deadline"`
	Revision int64     `json:"revision"`
}

type gatewayState struct {
	ActiveRevision   int64           `json:"active_revision"`
	BootID           string          `json:"boot_id,omitempty"`
	CounterEpoch     int64           `json:"counter_epoch"`
	DegradedNetworks []string        `json:"degraded_networks,omitempty"`
	Generation       int64           `json:"generation"`
	LastApply        *lastApply      `json:"last_apply,omitempty"`
	LastKnownGood    int64           `json:"last_known_good,omitempty"`
	OverlaysActive   int             `json:"overlays_active"`
	PendingConfirm   *pendingConfirm `json:"pending_confirm,omitempty"`
	RunsActive       int             `json:"runs_active"`
	SafeMode         bool            `json:"safe_mode"`
	SafeModeReason   string          `json:"safe_mode_reason,omitempty"`
}

// runsActive is the number of runs that are running now.
func (s *Server) runsActive() int {
	running, _, _ := s.runs.Busy()
	return running
}

// GetState implements GET /state.
func (s *Server) GetState(c *gin.Context) {
	snap := s.cfg.Engine.Snapshot()
	st := gatewayState{
		Generation:     int64(snap.Generation),
		ActiveRevision: snap.Revision,
		BootID:         s.cfg.BootID,
		LastKnownGood:  s.lastKnownGood(),
		OverlaysActive: len(snap.Overlays),
		RunsActive:     s.runsActive(),
		CounterEpoch:   snap.CounterEpoch,
		SafeMode:       snap.SafeMode,
		SafeModeReason: snap.SafeModeReason,
	}
	if p := snap.Pending; p != nil {
		st.PendingConfirm = &pendingConfirm{Deadline: p.Deadline.UTC(), Revision: p.Revision}
	}
	switch {
	case snap.LastError != "":
		st.LastApply = &lastApply{Result: "failed", Error: firstLine(snap.LastError)}
	case snap.Applied != nil:
		at := snap.Applied.At.UTC()
		result := "ok"
		if snap.Applied.RolledBack {
			result = "rolled_back"
		}
		st.LastApply = &lastApply{Result: result, At: &at, Generation: int64(snap.Applied.Generation), Revision: snap.Applied.Revision,
			DurationMs: snap.Applied.Duration.Milliseconds()}
	}
	for _, p := range snap.Problems {
		if p.Code == compiler.CodePortMissing && p.Network != "" {
			st.DegradedNetworks = append(st.DegradedNetworks, p.Network)
		}
	}
	c.JSON(200, st)
}

func (s *Server) lastKnownGood() int64 {
	r, _, err := s.cfg.Store.LastKnownGood()
	if err != nil {
		return 0
	}
	return r.Id
}

// GetLinuxState implements GET /state/linux: the technical view's kernel state (plan §2.17), read
// back through the executor and rendered as text. It holds no key: WireGuard shows public keys only.
func (s *Server) GetLinuxState(c *gin.Context) {
	v, err := s.cfg.Engine.LinuxView(contextOf(c))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, model.LinuxState{
		Revision:   v.Revision,
		Generation: int64(v.Generation),
		ReadAt:     v.At.UTC(),
		Nftables:   v.Nftables,
		Tc:         v.TC,
		Routes:     v.Routes,
		Wireguard:  v.WireGuard,
	})
}

// GetLinuxObject implements GET /state/linux/{kind}/{objectId}: the technical view of one device, access
// rule or fault ("Show Linux view", plan §2.17, docs/open-items.md P3-M14-03), the kernel objects the
// compiler made for it, read back now.
func (s *Server) GetLinuxObject(c *gin.Context, kind model.GetLinuxObjectParamsKind, objectId model.Ref) {
	if !kind.Valid() {
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the kind of object is device, rule or fault"))
		return
	}
	id := objectId
	if cfg := s.cfg.Engine.Snapshot().Config; cfg != nil {
		switch kind {
		case model.GetLinuxObjectParamsKindDevice:
			// a device by its id or its name, as GET /devices/{deviceId} finds it
			if d, ok := findDevice(s.deviceViews(cfg, true), objectId); ok {
				id = d.ID
			}
		case model.GetLinuxObjectParamsKindRule:
			// names are unique per kind ignoring case and never have the form of a UUID (domain.CodeDuplicateName,
			// CodeNameIsUUID), so at most one rule has this name and no name is another rule's id
			if cfg.AccessRules != nil {
				for rid, r := range *cfg.AccessRules {
					if r.Name != nil && strings.EqualFold(*r.Name, objectId) {
						id = rid
					}
				}
			}
		}
	}
	v, err := s.cfg.Engine.LinuxObjectView(contextOf(c), string(kind), id)
	if err != nil {
		s.fail(c, err)
		return
	}
	keys := v.Keys
	if keys == nil {
		keys = []string{}
	}
	c.JSON(200, model.LinuxObjectState{
		Kind:       model.LinuxObjectStateKind(kind),
		Id:         objectId,
		Revision:   v.Revision,
		Generation: int64(v.Generation),
		ReadAt:     v.At.UTC(),
		Keys:       keys,
		Nftables:   v.Nftables,
		Tc:         v.TC,
	})
}

// GetCapabilities implements GET /capabilities.
func (s *Server) GetCapabilities(c *gin.Context) {
	features, steps := Features, run.SupportedStepTypes
	if s.captures == nil {
		// a deployment without a capture volume cannot record (M17)
		steps = slices.DeleteFunc(slices.Clone(steps), func(t string) bool { return t == "capture" })
	} else {
		features = append(slices.Clone(features), "captures")
	}
	caps := model.Capabilities{
		Version:       s.cfg.Version,
		Features:      features,
		OverlayKinds:  engine.SupportedOverlayKinds,
		FaultFamilies: engine.SupportedFaultFamilies,
		StepTypes:     steps,
		CheckTypes:    run.SupportedCheckTypes,
	}
	caps.DiagnosticTypes = &domain.DiagnosticTypes
	// the limits of plan §3.3: the ids the mark layout has, the classes the compiler accepts per interface
	caps.Limits = &struct {
		ClassLimitPerInterface *int `json:"class_limit_per_interface,omitempty"`
		FaultIds               *int `json:"fault_ids,omitempty"`
	}{ClassLimitPerInterface: ptr(s.cfg.Engine.ClassLimit()), FaultIds: ptr(compiler.MarkIDMax)}
	c.JSON(200, caps)
}

// Features are the feature flags of this build (plan §2.15 capabilities): what the milestones up to
// M14, M35a (port forwarding), M15 (runs), M20 (DNS faults and hostname selectors), M21 (the TLS responder: certificate cases) M23 (DHCP test actions), M24 (diagnostics), M25 (probes) and M26 (metrics) provide.
var Features = []string{"networks.lan", "networks.wireguard", "routing.static", "routing.bird", "revisions", "events", "audit", "dhcp", "dns.proxy", "dns.faults", "dns.hostname_selectors", "tls.responder", "tls.cases", "tls.intercept", "dhcp.actions", "devices", "flows", "overlays", "faults.impairment", "faults.mtu", "faults.tunnel", "wireguard.actions", "rules", "profiles", "explain", "port_forwards", "runs", "checks", "diagnostics", "probes", "metrics"}

// GetSystemInfo implements GET /system/info.
func (s *Server) GetSystemInfo(c *gin.Context) {
	host, _ := os.Hostname()
	info := model.SystemInfo{
		ApiVersion: ptr("v1"),
		Arch:       ptr(model.SystemInfoArch(runtime.GOARCH)),
		BootId:     ptr(s.cfg.BootID),
		Commit:     ptr(s.cfg.Commit),
		Hostname:   ptr(host),
		Kernel:     ptr(kernelRelease()),
		Os:         ptr(osRelease()),
		Version:    ptr(s.cfg.Version),
	}
	if t, err := time.Parse(time.RFC3339, s.cfg.BuildDate); err == nil {
		info.BuildDate = &t
	}
	st := s.cfg.Started.UTC()
	info.StartedAt = &st
	c.JSON(200, info)
}

func kernelRelease() string {
	var u syscall.Utsname
	if syscall.Uname(&u) != nil {
		return ""
	}
	b := make([]byte, 0, len(u.Release))
	for _, ch := range u.Release {
		if ch == 0 {
			break
		}
		b = append(b, byte(ch))
	}
	return string(b)
}

func osRelease() string {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

type healthComponent struct {
	Detail string `json:"detail,omitempty"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// GetHealth implements GET /system/health: without authentication only the overall status.
func (s *Server) GetHealth(c *gin.Context) {
	overall, comps := s.healthReport(contextOf(c))
	body := gin.H{"status": overall}
	if principalOf(c) != nil {
		body["components"] = comps
	}
	code := 200
	if overall == "unhealthy" {
		code = 503
	}
	c.JSON(code, body)
}

// healthReport checks the managed components: the overall status (healthy, degraded or unhealthy) and one entry per
// component. GET /system/health and the metrics endpoint (M26) report it.
func (s *Server) healthReport(parent context.Context) (string, []healthComponent) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	api := healthComponent{Name: "api", Status: "healthy"}
	overall := "healthy"
	worse := func(to string) {
		if to == "unhealthy" || (to == "degraded" && overall == "healthy") {
			overall = to
		}
	}
	// §3.11: a panicked or failed supervised goroutine marks the api component unhealthy instead of
	// staying invisible until something downstream of it breaks
	for _, h := range s.cfg.Engine.Health() {
		if h.State == supervisor.Panicked || h.State == supervisor.Failed {
			api.Status, api.Detail = "unhealthy", fmt.Sprintf("%s %s: %s", h.Name, h.State, firstLine(h.Err))
			worse("unhealthy")
		}
	}
	// the executor answers a read, cached: this endpoint takes no authentication and no rate limit
	ex := healthComponent{Name: "executor", Status: "healthy"}
	if err := s.health.check(ctx, s.clk.Now(), func(ctx context.Context) error {
		_, err := s.cfg.Exec.Do(ctx, &executor.Read{Target: executor.Target{NS: s.cfg.Namespace}, What: executor.ReadAssigned})
		return err
	}); err != nil {
		ex.Status, ex.Detail = "unhealthy", "the executor does not answer: "+firstLine(err.Error())
		worse("unhealthy")
	}
	comps := []healthComponent{api, ex}
	snap := s.cfg.Engine.Snapshot()
	if snap.Bird != nil {
		b := healthComponent{Name: "bird", Status: "healthy"}
		if len(snap.Routing) == 0 {
			b.Status, b.Detail = "degraded", "no routing protocol reported yet"
			worse("degraded")
		}
		comps = append(comps, b)
	}
	if snap.LastError != "" {
		worse("degraded")
		if comps[0].Status == "healthy" {
			comps[0].Status, comps[0].Detail = "degraded", "the last apply failed: "+firstLine(snap.LastError)
		}
	}
	if snap.CutWindowError != "" {
		worse("degraded")
		if comps[0].Status == "healthy" {
			comps[0].Status, comps[0].Detail = "degraded", "the window of resets of an access rule's cut is still open: "+firstLine(snap.CutWindowError)
		}
	}
	if snap.KeaNetworks == nil {
		comps = append(comps, healthComponent{Name: "kea", Status: "disabled"})
	} else {
		kea := healthComponent{Name: "kea", Status: "healthy"}
		if snap.DHCPError != "" {
			kea.Status, kea.Detail = "degraded", "the DHCP server does not run the applied configuration: "+firstLine(snap.DHCPError)
			worse("degraded")
		}
		comps = append(comps, kea)
	}
	if snap.Service == nil {
		comps = append(comps, healthComponent{Name: "svcns", Status: "disabled"})
	} else {
		svcns := healthComponent{Name: "svcns", Status: "healthy"}
		if h := snap.ServiceHealth; h != nil && (!h.Exists || !h.HolderMatches) {
			svcns.Status, svcns.Detail = "degraded", "the service namespace does not match what was applied"
			worse("degraded")
		}
		comps = append(comps, svcns)
	}
	dns := healthComponent{Name: "dns", Status: "healthy"}
	if last := s.dnsLastPoll(); last.IsZero() || s.clk.Now().Sub(last) > 60*time.Second {
		dns.Status, dns.Detail = "degraded", "the DNS proxy has not polled for its configuration in the last 60s"
		worse("degraded")
	}
	comps = append(comps, dns)
	if snap.Service == nil {
		comps = append(comps, healthComponent{Name: "tls", Status: "disabled"})
	} else {
		tlsc := healthComponent{Name: "tls", Status: "healthy"}
		if last := s.tlsLastPoll(); last.IsZero() || s.clk.Now().Sub(last) > 60*time.Second {
			tlsc.Status, tlsc.Detail = "degraded", "the TLS responder has not polled for its configuration in the last 60s"
			worse("degraded")
		}
		comps = append(comps, tlsc)
	}
	// the interception proxy is optional (plan §3.8): a gateway that never saw it poll and has no interception case in
	// force reports it disabled, one that has a case but no proxy that polls reports it degraded
	if snap.Service == nil {
		comps = append(comps, healthComponent{Name: "tls_proxy", Status: "disabled"})
	} else {
		proxy := healthComponent{Name: "tls_proxy", Status: "healthy"}
		last := s.interceptLastPoll()
		stale := last.IsZero() || s.clk.Now().Sub(last) > 60*time.Second
		switch {
		case stale && hasInterceptCase(snap):
			proxy.Status, proxy.Detail = "degraded", "a TLS interception case is in force and the interception proxy has not polled for its configuration in the last 60s: the connections of the case are reset"
			worse("degraded")
		case last.IsZero():
			proxy.Status, proxy.Detail = "disabled", "the interception proxy is not deployed: no case of the kind intercept can work"
		case stale:
			proxy.Status, proxy.Detail = "degraded", "the interception proxy has not polled for its configuration in the last 60s"
			worse("degraded")
		}
		comps = append(comps, proxy)
	}
	if err := s.cfg.Audit.Err(); err != nil {
		comps[0].Status, comps[0].Detail = "unhealthy", "the audit log cannot be written: "+firstLine(err.Error())
		worse("unhealthy")
	}
	if snap.ObserveError != "" {
		comps[0].Status, comps[0].Detail = "degraded", "observation degraded: "+firstLine(snap.ObserveError)
		worse("degraded")
	}
	return overall, comps
}

// GetPreflight implements GET /system/preflight: the checks of the host made when the process started, and
// the check of every interface that is or may become a test interface, made now (P3-M12-07).
func (s *Server) GetPreflight(c *gin.Context) {
	osc := s.readOSConfig(contextOf(c))
	c.JSON(200, s.preflightReport(s.hostInterfaces(osc), osc, true, false))
}

// ---- interfaces

type hostAssignment struct {
	Network string `json:"network,omitempty"`
	Role    string `json:"role"`
}

type hostInterface struct {
	Addresses []string        `json:"addresses,omitempty"`
	Assigned  *hostAssignment `json:"assigned,omitempty"`
	Bus       string          `json:"bus,omitempty"`
	Driver    string          `json:"driver,omitempty"`
	LinkUp    *bool           `json:"link_up,omitempty"`
	MAC       string          `json:"mac"`
	Name      string          `json:"name"`
	// OSManaged says whether netplan, NetworkManager or systemd-networkd configures the interface; absent when that could not
	// be read
	OSManaged *bool `json:"os_managed,omitempty"`
	SpeedMbps int   `json:"speed_mbps,omitempty"`
}

// osConfig is where the host's operating system configures network interfaces, read through the executor
// for one request; err is set when it could not be read.
type osConfig struct {
	state *executor.HostNetState
	err   error
}

func (s *Server) readOSConfig(ctx context.Context) osConfig {
	st, err := s.cfg.Engine.HostNetConfig(ctx)
	switch changed, before := s.osConfigErr.set(err); {
	case changed && err != nil:
		s.log.Warn("cannot read the host's network configuration", "error", err)
	case changed:
		s.log.Info("the host's network configuration can be read again", "error_before", before)
	case err != nil:
		s.log.Debug("cannot read the host's network configuration", "error", err)
	}
	return osConfig{state: st, err: err}
}

// lastError remembers the message of the last failure of a repeated operation (empty: it succeeded).
type lastError struct {
	mu  sync.Mutex
	msg string
}

// set records the outcome of the operation; changed reports whether it differs from the last one (another
// message, a failure after a success or a success after a failure), before is the last message.
func (l *lastError) set(err error) (changed bool, before string) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	before, l.msg = l.msg, msg
	return before != msg, before
}

// configures reports whether the operating system configures the interface; ok is false when that is not known.
func (o osConfig) configures(name, mac, driver string) (yes, ok bool) {
	if o.err != nil || o.state == nil {
		return false, false
	}
	return len(preflight.Configuring(o.state.Entries, name, mac, driver)) > 0, true
}

// hostInterfaces describes the physical interfaces of the host, what they are used for and whether the
// operating system configures them (osc, read for the request).
func (s *Server) hostInterfaces(osc osConfig) []hostInterface {
	snap := s.cfg.Engine.Snapshot()
	cfg := snap.Config
	out := []hostInterface{}
	for _, l := range snap.Host.Links {
		if l.Name == "lo" || (l.Kind != "" && l.Kind != "veth") || strings.HasPrefix(l.Name, "docker") {
			continue
		}
		hi := hostInterface{Name: l.Name, MAC: l.MAC}
		for _, a := range l.Addrs {
			if a.Prefix.Addr().Is4() {
				hi.Addresses = append(hi.Addresses, a.Prefix.String())
			}
		}
		sysfs(&hi)
		if cfg != nil {
			hi.Assigned = assignment(cfg, l)
		}
		if yes, ok := osc.configures(l.Name, l.MAC, hi.Driver); ok {
			hi.OSManaged = &yes
		}
		out = append(out, hi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func refMatches(ref model.InterfaceRef, l compiler.HostLink) bool {
	if ref.Mac != nil && *ref.Mac != "" {
		return strings.EqualFold(*ref.Mac, l.MAC)
	}
	return ref.Name != nil && *ref.Name == l.Name
}

// NetworkInterfaceNames returns the host interface and bridge names a LAN network or a test-role
// WireGuard network occupies: not yet assigned to a test network (plan §2.16), listenAddrs must
// not bind them before the setup is finished, even when a configuration is already active (M5-21:
// e.g. `chaosgw apply --file` without a state directory to track the setup in).
func NetworkInterfaceNames(snap *engine.Snapshot) map[string]bool {
	out := map[string]bool{}
	if snap.Config == nil {
		return out
	}
	for _, l := range snap.Host.Links {
		if a := assignment(snap.Config, l); a != nil && a.Role == "network" {
			out[l.Name] = true
		}
	}
	for _, b := range snap.Bridges {
		out[b.Name] = true
	}
	for _, w := range snap.WireGuardInterfaces {
		if w.Role != "management" {
			out[w.Name] = true
		}
	}
	return out
}

func assignment(cfg *model.Configuration, l compiler.HostLink) *hostAssignment {
	if refMatches(cfg.Uplink.Interface, l) {
		return &hostAssignment{Role: "uplink"}
	}
	if refMatches(cfg.Management.Interface, l) {
		return &hostAssignment{Role: "management"}
	}
	if cfg.Networks != nil {
		for id, n := range *cfg.Networks {
			lan, err := n.AsLanNetwork()
			if err != nil || lan.Type != model.LanNetworkTypeLan {
				continue
			}
			for _, ref := range lan.Interfaces {
				if refMatches(ref, l) {
					return &hostAssignment{Role: "network", Network: id}
				}
			}
		}
	}
	return nil
}

// sysClassNet is where the kernel lists the interfaces of the API's network namespace (the host's in
// production, where the API runs with the host network). The uplink traffic sampler reads it in its
// own goroutine, so a test that points it elsewhere swaps it atomically.
var sysClassNet atomic.Pointer[string]

func init() {
	d := "/sys/class/net"
	sysClassNet.Store(&d)
}

// sysfs adds what only the kernel's file system knows: link state, speed, driver and bus.
func sysfs(hi *hostInterface) {
	base := filepath.Join(*sysClassNet.Load(), hi.Name)
	if b, err := os.ReadFile(filepath.Join(base, "carrier")); err == nil {
		up := strings.TrimSpace(string(b)) == "1"
		hi.LinkUp = &up
	}
	if b, err := os.ReadFile(filepath.Join(base, "speed")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
			hi.SpeedMbps = n
		}
	}
	hi.Bus = "virtual"
	if d, err := os.Readlink(filepath.Join(base, "device", "driver")); err == nil {
		hi.Driver = filepath.Base(d)
	}
	if d, err := os.Readlink(filepath.Join(base, "device", "subsystem")); err == nil {
		switch filepath.Base(d) {
		case "pci", "usb", "platform":
			hi.Bus = filepath.Base(d)
		default:
			hi.Bus = "unknown"
		}
	} else if _, err := os.Stat(base); err != nil {
		hi.Bus = "unknown"
	}
}

// linkBytes returns the received and sent bytes of an interface from its kernel counters.
func linkBytes(name string) (rx, tx int64, ok bool) {
	read := func(f string) (int64, bool) {
		b, err := os.ReadFile(filepath.Join(*sysClassNet.Load(), name, "statistics", f))
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		return n, err == nil
	}
	rx, okr := read("rx_bytes")
	tx, okt := read("tx_bytes")
	return rx, tx, okr && okt
}

// ListHostInterfaces implements GET /system/interfaces.
func (s *Server) ListHostInterfaces(c *gin.Context, params model.ListHostInterfacesParams) {
	items, next, err := page(s.hostInterfaces(s.readOSConfig(contextOf(c))), func(h hostInterface) string { return h.Name }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	body := gin.H{"items": items}
	if next != nil {
		body["next_cursor"] = *next
	}
	c.JSON(200, body)
}

// ---- setup

// GetSetup implements GET /setup.
func (s *Server) GetSetup(c *gin.Context) {
	if s.cfg.Auth.SetupCompleted() {
		c.JSON(200, gin.H{"completed": true})
		return
	}
	// an unauthenticated caller shares the executor's read with the others (setupOSConfig)
	osc := s.setupOSConfig.get(contextOf(c), s.clk.Now(), s.readOSConfig)
	ifaces := s.hostInterfaces(osc)
	st := gin.H{"completed": false, "interfaces": ifaces, "preflight": s.preflightReport(ifaces, osc, false, true)}
	snap := s.cfg.Engine.Snapshot()
	for _, d := range snap.Host.Defaults {
		if l, ok := snap.Host.Link(d.Dev); ok {
			st["suggested_uplink"] = gin.H{"name": l.Name, "mac": l.MAC}
			break
		}
	}
	c.JSON(200, st)
}

// preflightReport is the report of the preflight (plan §3.4): the checks of the host the process made when it
// started (kernel, modules; Config.Preflight), and one check per interface that is assigned to a test network
// or may become one (not the uplink, not the management interface), made now: whether netplan,
// NetworkManager or systemd-networkd configures it, and which entry to remove (docs/open-items.md P3-M12-07). The wizard and every
// client show the same text. The report's status counts the checks of the host and of the assigned test
// interfaces, not a candidate's warning. noRun adds a warning when no check of the host has run in this
// process (Config.Preflight is nil or returns nil; a report without a time, such as a host that cannot be
// inspected, is a check that ran). public is set for the unauthenticated GET /setup: the report then does not
// quote why the executor could not read the configuration (that error is in the log). It still names the
// files that cannot be read and the entries and connections that configure an interface, because the
// wizard must say what to remove (plan §3.4, the Phase 3 audit).
func (s *Server) preflightReport(ifaces []hostInterface, osc osConfig, noRun, public bool) *PreflightReport {
	r := &PreflightReport{Status: "ok"}
	ran := false
	if s.cfg.Preflight != nil {
		if base := s.cfg.Preflight(); base != nil {
			ran = true
			r.At, r.Status = base.At, base.Status
			r.Checks = append(r.Checks, base.Checks...)
		}
	}
	if !ran && noRun {
		r.Checks = append(r.Checks, PreflightCheck{ID: "preflight", Status: "warn", Message: "no preflight check has run in this process"})
	}
	add := func(c PreflightCheck) {
		r.Checks = append(r.Checks, c)
	}
	switch {
	case osc.err != nil && public:
		add(PreflightCheck{ID: "interfaces.os_config", Status: "warn", Message: "the network configuration of the operating system cannot be read (the reason is in the log of the gateway)",
			Fix: "give the executor the host's /etc and /usr/lib/systemd/network read-only below /host (--host-root /host, deploy/compose.executor.yaml)"})
	case osc.err != nil:
		add(PreflightCheck{ID: "interfaces.os_config", Status: "warn", Message: "the network configuration of the operating system cannot be read: " + firstLine(osc.err.Error()),
			Fix: "give the executor the host's /etc and /usr/lib/systemd/network read-only below /host (--host-root /host, deploy/compose.executor.yaml)"})
	case osc.state != nil && len(osc.state.Errors) > 0:
		add(PreflightCheck{ID: "interfaces.os_config", Status: "warn", Message: "some network configuration files of the operating system cannot be read: " + strings.Join(osc.state.Errors, "; ")})
	}
	assigned := map[string]bool{}
	if osc.state != nil {
		for _, hi := range ifaces {
			role := ""
			if hi.Assigned != nil {
				role = hi.Assigned.Role
			}
			if role == "uplink" || role == "management" {
				continue // the operating system keeps them (D3)
			}
			assigned[hi.Name] = role == "network"
			status, msg, fix := preflight.InterfaceCheck(hi.Name, hi.MAC, hi.Driver, role == "network", osc.state.Entries)
			add(PreflightCheck{ID: "interface." + hi.Name + ".os_config", Interface: hi.Name, Status: status, Message: msg, Fix: fix})
		}
	}
	for _, c := range r.Checks {
		switch {
		case c.Interface != "" && !assigned[c.Interface]:
			// a candidate's warning is shown on its check (the wizard shows it where the test interfaces are
			// chosen) but is no warning of the gateway: before the setup every interface, the future uplink too,
			// is a candidate, and after it an interface the gateway does not use is the operating system's
		case c.Status == "fail":
			r.Status = "fail"
		case c.Status == "warn" && r.Status == "ok":
			r.Status = "warn"
		}
	}
	return r
}

// minPassword is the shortest admin password (the spec's minLength).
const minPassword = 12

// CompleteSetup implements POST /setup: revision 1 from the request, applied, then the admin
// password; only then the setup is done (a failed apply leaves it open).
func (s *Server) CompleteSetup(c *gin.Context) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.cfg.Auth.SetupCompleted() { // another request finished it while this one waited
		s.write(c, newProblem(model.ErrorCodeSetupCompleted, "the setup is completed"))
		return
	}
	var body struct {
		AdminPassword string          `json:"admin_password"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if p := decodeJSON(c, &body); p != nil {
		s.write(c, p)
		return
	}
	if p := requireFields(map[string]bool{"admin_password": body.AdminPassword != "", "configuration": len(body.Configuration) > 0}); p != nil {
		s.write(c, p)
		return
	}
	if len(body.AdminPassword) < minPassword {
		errs := []model.ValidationError{{Path: "/admin_password", Code: "min_length", Message: fmt.Sprintf("the password needs at least %d characters", minPassword)}}
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the password is too short").with(func(b *model.Problem) { b.Errors = &errs }))
		return
	}
	now := s.clk.Now()
	cfg, err := domain.NewCandidate(nil, body.Configuration, domain.FormatJSON, domain.CandidateFull, now, s.validateOpts()...)
	if err != nil {
		s.fail(c, err)
		return
	}
	cfg, undo, err := s.importAndProvision(cfg)
	if err != nil {
		s.fail(c, err)
		return
	}
	rev, err := s.cfg.Store.Create(cfg, store.CreateOptions{IfMatch: s.cfg.Store.ActiveID(), Message: "first-start setup", By: actorOf(principalOf(c)), Now: now, Validate: s.validateOpts()})
	if err != nil {
		undo()
		s.fail(c, err)
		return
	}
	// an uplink that has lost its address for a moment (the host's `netplan apply` the setup asks for)
	// is waited for before the apply compiles (issue #64); whatever else is wrong, the apply reports
	_, _ = s.cfg.Engine.PreviewAwaitingUplink(contextOf(c), rev.Id, s.cfg.UplinkWait)
	res, err := s.cfg.Engine.Apply(context.WithoutCancel(contextOf(c)), rev.Id, engine.ApplyOptions{ConfirmTimeout: s.cfg.ConfirmTimeout, ForceConfirm: true})
	if err != nil {
		_ = s.cfg.Store.Discard(rev.Id) // the setup stays open: the candidate is of no use
		undo()
		s.fail(c, err)
		return
	}
	if err := s.cfg.Auth.CompleteSetup(body.AdminPassword); err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "setup.complete", nil, rev.Id, "")
	s.cfg.Engine.Emit("revision_applied", map[string]any{"revision": rev.Id, "setup": true,
		"actor": actorOf(principalOf(c)), "subject": engine.Subject{Kind: "revision", ID: itoa(rev.Id)}})
	c.Header("Chaos-Generation", itoa(int64(res.Generation)))
	c.JSON(200, applyResult(res))
}
