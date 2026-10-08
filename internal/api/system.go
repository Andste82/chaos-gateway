package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
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
		CounterEpoch:   snap.CounterEpoch,
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

// GetCapabilities implements GET /capabilities.
func (s *Server) GetCapabilities(c *gin.Context) {
	c.JSON(200, model.Capabilities{
		Version:       s.cfg.Version,
		Features:      Features,
		OverlayKinds:  engine.SupportedOverlayKinds,
		FaultFamilies: engine.SupportedFaultFamilies,
		StepTypes:     []string{},
		CheckTypes:    []string{},
	})
}

// Features are the feature flags of this build (plan §2.15 capabilities): what the milestones up to
// M6a provide.
var Features = []string{"networks.lan", "networks.wireguard", "routing.static", "routing.bird", "revisions", "events", "audit", "dhcp", "dns.proxy", "devices", "flows", "overlays", "faults.impairment", "rules", "explain"}

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
	ctx, cancel := context.WithTimeout(contextOf(c), 3*time.Second)
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
	if err := s.cfg.Audit.Err(); err != nil {
		comps[0].Status, comps[0].Detail = "unhealthy", "the audit log cannot be written: "+firstLine(err.Error())
		worse("unhealthy")
	}
	if snap.ObserveError != "" {
		comps[0].Status, comps[0].Detail = "degraded", "observation degraded: "+firstLine(snap.ObserveError)
		worse("degraded")
	}
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

// GetPreflight implements GET /system/preflight.
func (s *Server) GetPreflight(c *gin.Context) {
	if r := s.preflightReport(); r != nil {
		c.JSON(200, r)
		return
	}
	c.JSON(200, gin.H{"status": "warn", "checks": []gin.H{{"id": "preflight", "status": "warn", "message": "no preflight check has run in this process"}}})
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
	SpeedMbps int             `json:"speed_mbps,omitempty"`
}

// hostInterfaces describes the physical interfaces of the host and what they are used for.
func (s *Server) hostInterfaces() []hostInterface {
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

// sysfs adds what only the kernel's file system knows: link state, speed, driver and bus.
func sysfs(hi *hostInterface) {
	base := filepath.Join("/sys/class/net", hi.Name)
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

// ListHostInterfaces implements GET /system/interfaces.
func (s *Server) ListHostInterfaces(c *gin.Context, params model.ListHostInterfacesParams) {
	items, next, err := page(s.hostInterfaces(), func(h hostInterface) string { return h.Name }, params.Cursor, params.Limit)
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
	st := gin.H{"completed": false, "interfaces": s.hostInterfaces()}
	if r := s.preflightReport(); r != nil {
		st["preflight"] = r
	}
	snap := s.cfg.Engine.Snapshot()
	for _, d := range snap.Host.Defaults {
		if l, ok := snap.Host.Link(d.Dev); ok {
			st["suggested_uplink"] = gin.H{"name": l.Name, "mac": l.MAC}
			break
		}
	}
	c.JSON(200, st)
}

func (s *Server) preflightReport() any {
	if s.cfg.Preflight == nil {
		return nil
	}
	if r := s.cfg.Preflight(); r != nil {
		return r
	}
	return nil
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
	cfg, err := domain.NewCandidate(nil, body.Configuration, domain.FormatJSON, domain.CandidateFull, now)
	if err != nil {
		s.fail(c, err)
		return
	}
	cfg, undo, err := s.importAndProvision(cfg)
	if err != nil {
		s.fail(c, err)
		return
	}
	rev, err := s.cfg.Store.Create(cfg, store.CreateOptions{IfMatch: s.cfg.Store.ActiveID(), Message: "first-start setup", By: actorOf(principalOf(c)), Now: now})
	if err != nil {
		undo()
		s.fail(c, err)
		return
	}
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
