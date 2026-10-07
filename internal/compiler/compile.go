package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// PolicyTable is Chaos Gateway's routing table for test traffic (plan §2.2): the first of the
// executor's own reserved tables.
const PolicyTable = executor.OwnTableFirst

// PolicyRulePriority is the priority of the policy rules: one rule per test network, all alike.
const PolicyRulePriority = 1000

// DefaultUIPort is the UI/API port when the configuration names none.
const DefaultUIPort = 443

// Generation identifies an applied state: the revision and the running number of the desired
// state (plan §2.15). It is stored as the comment of the rule in the chain `generation`.
type Generation struct {
	Revision int64  `json:"revision"`
	Seq      uint64 `json:"seq"`
}

// String is the comment text.
func (g Generation) String() string { return fmt.Sprintf("gen=%d rev=%d", g.Seq, g.Revision) }

// Input is everything the compiler reads.
type Input struct {
	Config     *model.Configuration
	Host       Host
	Generation Generation
	// Keys holds the public key of every WireGuard network's interface (the private keys stay in the
	// secrets store; the compiler never sees them). A network without one cannot be compiled.
	Keys map[string]string
	// DynamicSets are sets that are filled at run time and survive every apply (later: the
	// DNS-derived address sets). They are part of the layout, never flushed.
	DynamicSets []SetDef
	// Identity is which addresses belong to which device right now (observed state); nil before
	// anything was observed.
	Identity *domain.Identity
	// Overlays are the active overlays (plan §2.1.1): they are never part of a revision. Their
	// references are UUIDs. Faults and profile activations among them take part in the precedence
	// resolution; kinds that no compiled mechanism implements yet (rules, DNS, TLS, DHCP) are
	// ignored here.
	Overlays []model.Overlay
	// FaultIDs is the allocation of fault ids of the previous compile (Target.FaultIDs): a fault
	// that is still there keeps its id, so its tc classes and counters stay (plan §3.3, "ids are
	// stable while the winning fault stays the same"). Nil allocates from scratch.
	FaultIDs map[string]int
	// ClassLimit is the number of tc classes one interface may carry (plan §3.3, D18); 0 uses
	// DefaultClassLimit.
	ClassLimit int
	// QueueBudget is the memory in bytes one interface may spend on the queues of faults that
	// have no explicit queue limit (plan §2.5); 0 uses DefaultQueueBudget.
	QueueBudget int64
	// ServiceNS is the name of the service namespace the gateway services run in; empty compiles
	// no service namespace (and no DNS redirect).
	ServiceNS string
	// ServiceHolderPID is the process whose namespace is attached as the service namespace when it
	// has to be created.
	ServiceHolderPID int
	// ServiceHolderNetnsInode is the inode ServiceHolderPID's namespace had when the holder itself
	// reported it; 0 skips the executor's reused-PID check (M6b-10).
	ServiceHolderNetnsInode uint64
	// DefaultUIPort is the port the API listens on; it stands in for `management.ui_port` when the
	// configuration names none (0: DefaultUIPort).
	DefaultUIPort int
}

// Severity of a Problem.
type Severity string

// Severities: an error stops the apply, a warning is reported (an event in the product).
const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
)

// Problem is something about the input the compiler cannot or should not ignore.
type Problem struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	// Network names the test network concerned, empty for the gateway as a whole.
	Network string `json:"network,omitempty"`
	// Scope says which scope of a fault caused the problem ("network IoT"), for capacity_exceeded.
	Scope string `json:"scope,omitempty"`
	// Faults are the overlays or configured faults that caused it, the biggest first.
	Faults []string `json:"faults,omitempty"`
}

// Problem codes.
const (
	CodeUplinkMissing    = "uplink_missing"
	CodeUplinkNoAddress  = "uplink_no_address"
	CodeUplinkNoGateway  = "uplink_no_gateway"
	CodePortMissing      = "port_missing"
	CodeManagementAbsent = "management_missing"
	CodeNoManagementSrc  = "no_management_sources"
	CodeUnsupported      = "unsupported"
)

// Bridge is a test network as it is built on the host.
type Bridge struct {
	Name        string       `json:"name"`
	NetworkID   string       `json:"network_id"`
	NetworkName string       `json:"network_name"`
	Address     netip.Prefix `json:"address"`
	Ports       []string     `json:"ports"`
	// NAT reports whether the network is masqueraded towards the uplink.
	NAT bool `json:"nat"`
	// Routes are the network's downstream routes (plan §2.3): prefixes reached through a router in
	// the network, the LAN counterpart of a WireGuard peer's Routes.
	Routes []string `json:"routes,omitempty"`
}

// Uplink is the interface towards the Internet with the address and gateway it has now.
type Uplink struct {
	Name    string       `json:"name"`
	Addr    netip.Prefix `json:"addr"`
	Gateway netip.Addr   `json:"gateway"`
}

// Management is the OS-owned management interface and the sources that always reach the control
// plane (anti-lockout).
type Management struct {
	Name string `json:"name,omitempty"`
	// Subnet is the management interface's own connected subnet, set whenever the interface
	// resolves, regardless of Sources: a neighbor must never be able to reach it, even when
	// explicit allowed_sources narrow who else is let in (plan §2.2.2).
	Subnet  netip.Prefix   `json:"subnet,omitempty"`
	Sources []netip.Prefix `json:"sources"`
	UIPort  int            `json:"ui_port"`
}

// Target is the compiled state of the gateway.
type Target struct {
	Generation Generation `json:"generation"`
	// Hash identifies the content apart from the generation: two targets with the same hash
	// need no apply.
	Hash string `json:"hash"`

	Uplink     Uplink        `json:"uplink"`
	Management Management    `json:"management"`
	Bridges    []Bridge      `json:"bridges"`
	WireGuard  []WGInterface `json:"wireguard,omitempty"`
	Bird       *BirdTarget   `json:"bird,omitempty"`
	// Service is the service namespace; nil when the gateway runs no service.
	Service *ServiceNS `json:"service,omitempty"`
	// Kea is the DHCP configuration; nil when no network has DHCP switched on.
	Kea *KeaTarget `json:"kea,omitempty"`
	// IdentityMap is the nftables map that holds every known device's current addresses mapped to
	// its numeral (plan §3.3): the single structure that replaces the Phase 1 per-device address
	// sets (M6a-07), so a device's address change is one map-element update instead of a full apply.
	IdentityMap string `json:"identity_map,omitempty"`
	// DeviceNums maps a device (configured or discovered) to the small integer IdentityMap uses as
	// its value for that device's addresses. Stable only within one Target: a device added or
	// removed renumbers them, which is why a changed device set still needs a full apply.
	DeviceNums  map[string]int `json:"device_nums,omitempty"`
	identityMap MapDef
	// FaultIDs is the allocation of fault ids by Fault.Key: feed it back as Input.FaultIDs.
	FaultIDs map[string]int `json:"fault_ids,omitempty"`
	// Faults are the fault ids in use, sorted by id: the winners of the impairment family with the
	// netem configuration of each direction.
	Faults []Fault `json:"faults,omitempty"`
	// TC is the tc tree of every interface classified traffic leaves through; nil when no fault
	// impairs anything.
	TC         *TCTarget `json:"tc,omitempty"`
	faultBuild *faultBuild
	// ClassifyNets is the nftables set of test, WireGuard and remote-network prefixes that guards
	// the classification chain (plan §3.3): classification never reads or writes the mark of
	// anything outside it.
	ClassifyNets string `json:"classify_nets,omitempty"`
	// ClassifyMaps maps a lookup-chain level ("devdestport", "devdest", "devport", "dev", plan
	// §3.3) to its nftables map name.
	ClassifyMaps map[string]string `json:"classify_maps,omitempty"`
	Interfaces   []string          `json:"interfaces"` // assigned to Chaos Gateway: bridges, ports, uplink
	// OSOwned is the subset of Interfaces that is assigned (tc, routing, offloads, DOCKER-USER) but
	// not Chaos Gateway's own in the stricter sense: the uplink, and the management interface when
	// it is a NIC of its own (M3-01). links, sysctl, wireguard and service_ns refuse them.
	OSOwned    []string               `json:"os_owned,omitempty"`
	Sysctls    []executor.SysctlEntry `json:"sysctls"`
	Offloads   []string               `json:"offloads"`
	Routes     []executor.Route       `json:"routes"`
	Rules      []executor.Rule        `json:"rules"`
	DockerUser []string               `json:"docker_user"`
	Nft        Nft                    `json:"nft"`
	Problems   []Problem              `json:"problems,omitempty"`
}

// HasErrors reports whether the target must not be applied.
func (t *Target) HasErrors() bool {
	for _, p := range t.Problems {
		if p.Severity == SevError {
			return true
		}
	}
	return false
}

// Errors returns the problems that stop an apply.
func (t *Target) Errors() []Problem {
	var out []Problem
	for _, p := range t.Problems {
		if p.Severity == SevError {
			out = append(out, p)
		}
	}
	return out
}

var (
	nameClean = regexp.MustCompile(`[^a-z0-9_-]+`)
	// Docker's own bridges: the executor refuses to assign such names
	dockerLike = regexp.MustCompile(`^br-[0-9a-f]{12}$`)
)

// bridgeName derives a stable name for a test network: br-<name>, at most 15 characters. A name
// that would clash with another bridge or look like one of Docker's gets the network's id.
func bridgeName(id, name string, used map[string]bool) string {
	n := nameClean.ReplaceAllString(strings.ToLower(name), "-")
	n = strings.Trim(n, "-_")
	if len(n) > 12 {
		n = n[:12]
	}
	cand := "br-" + n
	if n == "" || dockerLike.MatchString(cand) || used[cand] {
		cand = "br-" + strings.ReplaceAll(id, "-", "")[:8]
	}
	used[cand] = true
	return cand
}

// Compile builds the target state. It never fails: what it cannot build is reported as a Problem,
// and a target with errors is not applied.
func Compile(in Input) *Target {
	t := &Target{Generation: in.Generation}
	cfg := in.Config
	idx, _ := domain.BuildIndex(cfg)

	// ---- uplink and management -------------------------------------------------------
	t.compileUplink(cfg, in.Host)
	t.compileService(in)
	t.compileManagement(cfg, in.Host, in.DefaultUIPort)

	// ---- test networks -------------------------------------------------------------------
	ids := make([]string, 0, len(idx.Networks))
	for id := range idx.Networks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := idx.Networks[ids[i]], idx.Networks[ids[j]]
		if a.Name != b.Name {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return ids[i] < ids[j]
	})
	used := map[string]bool{}
	wgUsed := map[string]bool{}
	netByID := map[string]*Bridge{}
	for _, id := range ids {
		n := idx.Networks[id]
		if n.Lan == nil {
			t.compileWireGuardNetwork(id, n, in, wgUsed)
			continue
		}
		addr, err := netip.ParsePrefix(n.Lan.Address)
		if err != nil {
			t.errorf(CodeUnsupported, n.Name, "network %q has no valid address", n.Name)
			continue
		}
		b := Bridge{Name: bridgeName(id, n.Name, used), NetworkID: id, NetworkName: n.Name, Address: addr, NAT: n.Lan.Nat == nil || *n.Lan.Nat}
		for _, ref := range n.Lan.Interfaces {
			l, ok := in.Host.Resolve(ref)
			if !ok {
				t.warn(CodePortMissing, n.Name, "interface %s of network %q is not present: the network is degraded", describeRef(ref), n.Name)
				continue
			}
			b.Ports = append(b.Ports, l.Name)
		}
		sort.Strings(b.Ports)
		t.Bridges = append(t.Bridges, b)
		netByID[id] = &t.Bridges[len(t.Bridges)-1]
	}
	// the slice may have moved while appending: rebuild the lookup
	for i := range t.Bridges {
		netByID[t.Bridges[i].NetworkID] = &t.Bridges[i]
	}
	sort.Slice(t.WireGuard, func(i, j int) bool { return t.WireGuard[i].Name < t.WireGuard[j].Name })

	t.finishManagementSources()
	t.compileHostState()
	t.compileRouting(cfg, idx)
	t.compileBird(cfg, idx)
	t.compileKea(cfg, idx)
	t.compileIdentity(idx, in.Identity)
	t.compileFaults(in, idx)
	t.compileNft(cfg, t.topology(idx, netByID), in.DynamicSets)
	t.finish()
	return t
}

func describeRef(r model.InterfaceRef) string {
	switch {
	case r.Mac != nil && r.Name != nil:
		return fmt.Sprintf("%s (%s)", *r.Name, *r.Mac)
	case r.Mac != nil:
		return *r.Mac
	case r.Name != nil:
		return *r.Name
	}
	return "?"
}

func (t *Target) warn(code, network, format string, a ...any) {
	t.Problems = append(t.Problems, Problem{Severity: SevWarning, Code: code, Network: network, Message: fmt.Sprintf(format, a...)})
}

func (t *Target) errorf(code, network, format string, a ...any) {
	t.Problems = append(t.Problems, Problem{Severity: SevError, Code: code, Network: network, Message: fmt.Sprintf(format, a...)})
}

func (t *Target) compileUplink(cfg *model.Configuration, h Host) {
	l, ok := h.Resolve(cfg.Uplink.Interface)
	if !ok {
		t.errorf(CodeUplinkMissing, "", "the uplink interface %s is not present", describeRef(cfg.Uplink.Interface))
		return
	}
	t.Uplink.Name = l.Name
	if a, ok := l.FirstV4(); ok {
		t.Uplink.Addr = a
	} else {
		t.errorf(CodeUplinkNoAddress, "", "the uplink %s has no IPv4 address (the OS configures it)", l.Name)
	}
	switch {
	case cfg.Uplink.Gateway != nil:
		g, err := netip.ParseAddr(*cfg.Uplink.Gateway)
		if err != nil {
			t.errorf(CodeUplinkNoGateway, "", "the configured uplink gateway %q is not an address", *cfg.Uplink.Gateway)
			return
		}
		t.Uplink.Gateway = g
	default:
		if g, ok := h.DefaultGateway(l.Name); ok {
			t.Uplink.Gateway = g
		} else {
			t.errorf(CodeUplinkNoGateway, "", "the uplink %s has no default route and the configuration names no gateway", l.Name)
		}
	}
}

func (t *Target) compileManagement(cfg *model.Configuration, h Host, defaultPort int) {
	m := &t.Management
	m.UIPort = DefaultUIPort
	if defaultPort > 0 {
		m.UIPort = defaultPort // the port the API actually listens on, when the configuration names none
	}
	if cfg.Management.UiPort != nil {
		m.UIPort = *cfg.Management.UiPort
	}
	var sources []netip.Prefix
	if cfg.Management.AllowedSources != nil {
		for _, s := range *cfg.Management.AllowedSources {
			if p, err := netip.ParsePrefix(s); err == nil {
				sources = append(sources, p.Masked())
			}
		}
	}
	l, ok := h.Resolve(cfg.Management.Interface)
	if !ok {
		t.warn(CodeManagementAbsent, "", "the management interface %s is not present", describeRef(cfg.Management.Interface))
	} else {
		m.Name = l.Name
		if a, ok := l.FirstV4(); ok {
			m.Subnet = a.Masked()
			if cfg.Management.AllowedSources == nil {
				// default: the management interface's connected subnet
				sources = append(sources, a.Masked())
			}
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].String() < sources[j].String() })
	m.Sources = dedupePrefixes(sources)
}

func dedupePrefixes(p []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for i, x := range p {
		if i > 0 && x == p[i-1] {
			continue
		}
		out = append(out, x)
	}
	return out
}

// compileHostState derives the interface set, sysctls, offloads, links and DOCKER-USER interfaces.
func (t *Target) compileHostState() {
	owned := map[string]bool{}
	for _, b := range t.Bridges {
		owned[b.Name] = true
		for _, p := range b.Ports {
			owned[p] = true
		}
	}
	for _, w := range t.WireGuard {
		owned[w.Name] = true
	}
	if t.Service != nil {
		owned[t.Service.HostIf] = true
	}
	// the uplink and the management interface (when it is a NIC of its own) are OS-owned: assigned
	// (routes, offloads, DOCKER-USER) but their sysctls stay alone, and links/sysctl/wireguard/
	// service_ns refuse them (M3-01, executor.Scope)
	osOwned := map[string]bool{}
	if t.Uplink.Name != "" {
		osOwned[t.Uplink.Name] = true
	}
	if t.Management.Name != "" {
		osOwned[t.Management.Name] = true
	}
	for n := range osOwned {
		t.OSOwned = append(t.OSOwned, n)
	}
	sort.Strings(t.OSOwned)
	iface := map[string]bool{}
	for n := range owned {
		iface[n] = true
	}
	for n := range osOwned {
		iface[n] = true
	}
	for n := range iface {
		t.Interfaces = append(t.Interfaces, n)
	}
	sort.Strings(t.Interfaces)

	t.Sysctls = append(t.Sysctls,
		executor.SysctlEntry{Name: "ip_forward", Value: 1},
		// byte and packet accounting per conntrack entry: the flows API reports upload/download bytes.
		executor.SysctlEntry{Name: "nf_conntrack_acct", Value: 1},
		// each entry's start time (M6a-03): the flows API reports started_at.
		executor.SysctlEntry{Name: "nf_conntrack_timestamp", Value: 1},
	)
	var ownedNames []string
	for n := range owned {
		ownedNames = append(ownedNames, n)
	}
	sort.Strings(ownedNames)
	// router advertisements are not accepted on interfaces Chaos Gateway owns (plan §2.2.2)
	for _, n := range ownedNames {
		t.Sysctls = append(t.Sysctls, executor.SysctlEntry{Name: "accept_ra", Dev: n, Value: 0})
	}
	// offloads off on the ports and bridges of test networks and on the uplink (plan §3.4); a
	// WireGuard interface has no offloads to switch off
	wgNames := map[string]bool{}
	for _, w := range t.WireGuard {
		wgNames[w.Name] = true
	}
	for _, n := range t.Interfaces {
		if !wgNames[n] && (t.Service == nil || n != t.Service.HostIf) {
			t.Offloads = append(t.Offloads, n)
		}
	}

	// DOCKER-USER accepts what comes from or goes to the bridges of test networks: traffic to and
	// from the uplink is covered by it (a packet between a bridge and the uplink has the bridge on
	// one side), and Docker's own bridges keep their isolation
	du := map[string]bool{}
	for _, b := range t.Bridges {
		du[b.Name] = true
	}
	for _, w := range t.WireGuard {
		du[w.Name] = true
	}
	if t.Service != nil {
		du[t.Service.HostIf] = true
	}
	for n := range du {
		t.DockerUser = append(t.DockerUser, n)
	}
	sort.Strings(t.DockerUser)
}

// compileRouting builds table 100 and the rules that send test traffic into it (plan §2.2).
func (t *Target) compileRouting(cfg *model.Configuration, idx *domain.Index) {
	add := func(r executor.Route) {
		r.Action, r.Family, r.Table = "replace", 4, PolicyTable
		t.Routes = append(t.Routes, r)
	}
	// Replies from the uplink to a network behind a router or a tunnel arrive on the uplink
	// interface and are looked up in the main table, which does not know the network: a rule on the
	// destination sends them to table 100 as well.
	toSeen := map[string]bool{}
	addTo := func(prefix string) {
		if toSeen[prefix] {
			return
		}
		toSeen[prefix] = true
		t.Rules = append(t.Rules, executor.Rule{Action: "add", Family: 4, Priority: PolicyRulePriority, To: prefix, Table: PolicyTable})
	}
	defer func() {
		// after the iif rules, in a stable order
		sort.SliceStable(t.Rules, func(i, j int) bool {
			if (t.Rules[i].Iif == "") != (t.Rules[j].Iif == "") {
				return t.Rules[i].Iif != ""
			}
			return t.Rules[i].To < t.Rules[j].To
		})
	}()
	for _, b := range t.Bridges {
		add(executor.Route{Dst: b.Address.Masked().String(), Dev: b.Name})
	}
	if t.Uplink.Name != "" && t.Uplink.Addr.IsValid() {
		add(executor.Route{Dst: t.Uplink.Addr.Masked().String(), Dev: t.Uplink.Name})
		if t.Uplink.Gateway.IsValid() {
			add(executor.Route{Dst: "default", Via: t.Uplink.Gateway.String(), Dev: t.Uplink.Name})
		}
	}
	for i := range t.Bridges {
		b := &t.Bridges[i]
		n := idx.Networks[b.NetworkID]
		if n == nil || n.Lan == nil || n.Lan.Routes == nil {
			continue
		}
		for _, r := range *n.Lan.Routes {
			p, err := netip.ParsePrefix(r.Destination)
			if err != nil {
				continue
			}
			add(executor.Route{Dst: p.Masked().String(), Via: r.Via, Dev: b.Name})
			addTo(p.Masked().String())
			b.Routes = append(b.Routes, p.Masked().String())
		}
	}
	t.serviceRouting(add)
	sort.SliceStable(t.Routes, func(i, j int) bool {
		// stable, readable order: connected routes first, then downstream, the default last
		ri, rj := routeRank(t.Routes[i]), routeRank(t.Routes[j])
		if ri != rj {
			return ri < rj
		}
		return t.Routes[i].Dst < t.Routes[j].Dst
	})
	// one priority for all of them: a network that is added or removed never renumbers the others,
	// and no window opens in which test traffic falls through to the main table
	for _, b := range t.Bridges {
		t.Rules = append(t.Rules, executor.Rule{Action: "add", Family: 4, Priority: PolicyRulePriority, Iif: b.Name, Table: PolicyTable})
	}
	// WireGuard networks: connected routes, the networks behind clients and the static routes of
	// links go via the interface; its traffic uses the table, too (plan §2.2.1)
	for _, w := range t.WireGuard {
		add(executor.Route{Dst: w.Address.Masked().String(), Dev: w.Name})
		seen := map[string]bool{w.Address.Masked().String(): true}
		for _, p := range w.Peers {
			for _, a := range p.Routes {
				if !seen[a] {
					seen[a] = true
					add(executor.Route{Dst: a, Dev: w.Name})
					addTo(a)
				}
			}
		}
		t.Rules = append(t.Rules, executor.Rule{Action: "add", Family: 4, Priority: PolicyRulePriority, Iif: w.Name, Table: PolicyTable})
	}
}

func routeRank(r executor.Route) int {
	switch {
	case r.Dst == "default":
		return 2
	case r.Via != "":
		return 1
	}
	return 0
}

// finish computes the hash of everything but the generation.
func (t *Target) finish() {
	cp := *t
	cp.Generation = Generation{}
	cp.Hash = ""
	cp.Nft.Generation = ""
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	t.Hash = hex.EncodeToString(h[:8])
	t.Nft.Generation = t.Generation.String()
}
