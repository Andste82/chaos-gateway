package kea

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Paths inside the Kea container (Kea restricts them: /run/kea and /var/lib/kea).
const (
	ControlSocket = "/run/kea/kea4-ctrl.sock"
	LeaseFile     = "/var/lib/kea/kea-leases4.csv"
	// HookScript is the script the run_script hook calls; it notifies the API.
	HookScript = "/usr/share/kea/scripts/chaosgw-kea-hook"
)

// Kea opens its packet socket on an interface only while it is configured, and skips one that is
// down or has no carrier then: a bridge whose test port flaps while a configuration arrives would stay
// without a socket (#64). Kea retries the sockets it could not open, every SocketRetryWait for
// SocketRetries times (a minute), and status-get reports them as retrying meanwhile. The gateway does
// not count on these retries opening the socket once the link is back: the engine configures Kea again
// whenever one of its interfaces comes up or its carrier changed (engine.DHCP.Reopen).
const (
	SocketRetries   = 12
	SocketRetryWait = 5 * time.Second
)

// Pool is an address range.
type Pool struct{ Start, End netip.Addr }

// Reservation is what the server holds for one MAC address: an address (a device with `fixed_ip`, or the address a
// DHCP test action moves a device to) and/or options of its own, which win over the options of the subnet.
type Reservation struct {
	MAC string
	// IP is the reserved address; the zero value reserves none, the reservation only has Options.
	IP       netip.Addr
	Hostname string
	Options  Options
}

// Options are the DHCP options a subnet hands out, or a reservation hands out to its client instead.
type Options struct {
	Router netip.Addr
	DNS    []netip.Addr
	NTP    []netip.Addr
	Domain string
	Custom []CustomOption
}

// Options returns the options the subnet hands out.
func (s Subnet) Options() Options {
	return Options{Router: s.Router, DNS: s.DNS, NTP: s.NTP, Domain: s.Domain, Custom: s.Custom}
}

// SetOptions makes the subnet hand out these options.
func (s *Subnet) SetOptions(o Options) {
	s.Router, s.DNS, s.NTP, s.Domain, s.Custom = o.Router, o.DNS, o.NTP, o.Domain, o.Custom
}

// data is the `option-data` list of Kea.
func (o Options) data() []any {
	var opts []any
	opt := func(name, data string) { opts = append(opts, jsonMap{"name": name, "data": data}) }
	if o.Router.IsValid() {
		opt("routers", o.Router.String())
	}
	if len(o.DNS) > 0 {
		opt("domain-name-servers", joinAddrs(o.DNS))
	}
	if len(o.NTP) > 0 {
		opt("ntp-servers", joinAddrs(o.NTP))
	}
	if o.Domain != "" {
		opt("domain-name", o.Domain)
	}
	for _, c := range o.Custom {
		opts = append(opts, jsonMap{"code": c.Code, "data": c.Value, "always-send": true})
	}
	return opts
}

// CustomOption is a DHCP option that is not one of the named ones; Value is in Kea's option-data syntax.
type CustomOption struct {
	Code  int
	Value string
}

// Subnet is the DHCP scope of a network: one Kea subnet bound to its bridge.
type Subnet struct {
	// ID is the Kea subnet id (SubnetID derives it from the network's UUID).
	ID int
	// Network is the UUID of the network.
	Network   string
	Subnet    netip.Prefix
	Interface string
	Pools     []Pool
	// LeaseSeconds is the lease time. LeaseMin and LeaseMax bound the lease time a client may ask for (option 51); zero
	// takes 5 s and 3600 s (or the lease time when that is more). A test action with a short lease time sets both to it:
	// a client that asks for an hour is still given the short lease.
	LeaseSeconds       int
	LeaseMin, LeaseMax int
	Router             netip.Addr
	DNS                []netip.Addr
	NTP                []netip.Addr
	Domain             string
	Custom             []CustomOption
	Reservations       []Reservation
}

// LeaseClass is a lease time for the clients with some MAC addresses, which the subnet's lease time does not apply to
// (a DHCP test action on a device). Kea hands a client the lease time of its class before the one of its subnet.
type LeaseClass struct {
	// Name identifies the class; it is made of letters, digits, dashes and underscores.
	Name string
	MACs []string
	// Seconds is the lease time; Min and Max bound what a client may ask for, zero takes the same defaults as Subnet.
	Seconds, Min, Max int
}

// Silence is a set of clients the server does not answer (a DHCP test action). The clients are those received on
// Interface, except the ones of Except, and the ones of MACs; an empty Interface selects none by interface.
type Silence struct {
	Interface string
	Except    []string
	MACs      []string
}

// Config is the whole DHCPv4 configuration.
type Config struct {
	Subnets []Subnet
	// LeaseClasses give the clients with some MAC addresses a lease time of their own.
	LeaseClasses []LeaseClass
	// Silenced are the clients that are not answered.
	Silenced []Silence
	// Socket and Leases override the default paths (tests run Kea somewhere else).
	Socket, Leases string
	// Script is the run_script hook's script; empty leaves lease events off.
	Script string
}

// SubnetID derives a stable Kea subnet id from a network's UUID: the first 31 bits. Kea keys its
// leases by subnet id, so the id must not change when networks are added or removed. taken holds the
// ids already in use: a collision probes on.
func SubnetID(networkID string, taken map[int]bool) int {
	h := uint32(2166136261)
	for i := 0; i < len(networkID); i++ {
		h ^= uint32(networkID[i])
		h *= 16777619
	}
	id := int(h&0x7fffffff) | 1 // never 0
	for taken[id] {
		id = (id + 1) & 0x7fffffff
		if id == 0 {
			id = 1
		}
	}
	return id
}

// DefaultPool is the default of a network without pools of its own: the second half of the subnet up to
// the last usable address (plan: "Default - the second half of the subnet"), never the gateway's own
// address. A gateway inside that range splits it in two, the range before the gateway and the range after
// it, and an empty one is left out (#110). A subnet smaller than a /29 has none (nil).
func DefaultPool(p netip.Prefix, gateway netip.Addr) []Pool {
	if !p.Addr().Is4() || p.Bits() > 29 {
		return nil
	}
	base := p.Masked().Addr().As4()
	size := uint32(1) << (32 - p.Bits())
	start := toU32(base) + size/2
	end := toU32(base) + size - 2 // the last usable address
	if !gateway.Is4() {
		return []Pool{{Start: fromU32(start), End: fromU32(end)}}
	}
	gw := toU32(gateway.As4())
	if gw < start || gw > end {
		return []Pool{{Start: fromU32(start), End: fromU32(end)}}
	}
	var pools []Pool
	if start < gw {
		pools = append(pools, Pool{Start: fromU32(start), End: fromU32(gw - 1)})
	}
	if gw < end {
		pools = append(pools, Pool{Start: fromU32(gw + 1), End: fromU32(end)})
	}
	return pools
}

func toU32(a [4]byte) uint32 {
	return uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
}
func fromU32(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// Check validates a configuration before it is rendered.
func (c Config) Check() error {
	ids := map[int]bool{}
	for _, s := range c.Subnets {
		if s.ID < 1 || ids[s.ID] {
			return fmt.Errorf("kea: subnet id %d is not unique", s.ID)
		}
		ids[s.ID] = true
		if !s.Subnet.IsValid() || !s.Subnet.Addr().Is4() {
			return fmt.Errorf("kea: network %s has no IPv4 subnet", s.Network)
		}
		if s.Interface == "" {
			return fmt.Errorf("kea: network %s has no interface", s.Network)
		}
		if len(s.Pools) == 0 {
			return fmt.Errorf("kea: network %s has no pool", s.Network)
		}
		for _, p := range s.Pools {
			if !s.Subnet.Contains(p.Start) || !s.Subnet.Contains(p.End) || p.End.Less(p.Start) {
				return fmt.Errorf("kea: the pool %s - %s of network %s is not inside %s", p.Start, p.End, s.Network, s.Subnet)
			}
		}
		if s.LeaseSeconds < 1 {
			return fmt.Errorf("kea: network %s has no lease time", s.Network)
		}
		for _, r := range s.Reservations {
			if r.IP.IsValid() && !s.Subnet.Contains(r.IP) {
				return fmt.Errorf("kea: the reservation %s for %s is outside %s", r.IP, r.MAC, s.Subnet)
			}
		}
	}
	names := map[string]bool{}
	for _, c := range c.LeaseClasses {
		if !classNameRE.MatchString(c.Name) || names[c.Name] {
			return fmt.Errorf("kea: the lease class %q has no name that is valid and unique", c.Name)
		}
		names[c.Name] = true
		if c.Seconds < 1 || len(c.MACs) == 0 {
			return fmt.Errorf("kea: the lease class %s has no lease time or no client", c.Name)
		}
		if err := checkMACs(c.MACs); err != nil {
			return fmt.Errorf("kea: the lease class %s: %w", c.Name, err)
		}
	}
	for _, sl := range c.Silenced {
		if err := checkMACs(append(append([]string(nil), sl.MACs...), sl.Except...)); err != nil {
			return fmt.Errorf("kea: the silenced clients: %w", err)
		}
	}
	return nil
}

var classNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func checkMACs(macs []string) error {
	for _, m := range macs {
		if hw, err := net.ParseMAC(m); err != nil || len(hw) != 6 {
			return fmt.Errorf("%q is not a MAC address", m)
		}
	}
	return nil
}

// macLiteral is a MAC address as a hexadecimal string of Kea's expressions (`pkt4.mac == 0x0200000000aa`).
func macLiteral(m string) string {
	return "0x" + strings.ReplaceAll(strings.ToLower(m), ":", "")
}

// macTest is the expression that is true for a packet of a client with one of the MAC addresses.
func macTest(macs []string) string {
	sorted := make([]string, len(macs))
	for i, m := range macs {
		sorted[i] = strings.ToLower(m)
	}
	sort.Strings(sorted)
	parts := make([]string, len(sorted))
	for i, m := range sorted {
		parts[i] = "pkt4.mac == " + macLiteral(m)
	}
	return strings.Join(parts, " or ")
}

// dropTest is the expression of the class DROP, which Kea does not answer: a packet that belongs to the class is
// dropped before any subnet is selected.
func (c Config) dropTest() string {
	var parts []string
	for _, s := range c.Silenced {
		if s.Interface != "" {
			t := "pkt.iface == '" + s.Interface + "'"
			if len(s.Except) > 0 {
				t += " and not (" + macTest(s.Except) + ")"
			}
			parts = append(parts, "("+t+")")
		}
		if len(s.MACs) > 0 {
			parts = append(parts, "("+macTest(s.MACs)+")")
		}
	}
	return strings.Join(parts, " or ")
}

type jsonMap = map[string]any

// Render returns the configuration as the JSON document Kea reads (`{"Dhcp4": {...}}`): the same
// document `config-set` takes as its arguments. The output is deterministic.
func (c Config) Render() (string, error) {
	doc, err := c.Document()
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// Document is the configuration as a value (for `config-set`).
func (c Config) Document() (map[string]any, error) {
	if err := c.Check(); err != nil {
		return nil, err
	}
	subs := append([]Subnet(nil), c.Subnets...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].ID < subs[j].ID })
	var ifaces []string
	subnets := []any{}
	seen := map[string]bool{}
	for _, s := range subs {
		if !seen[s.Interface] {
			seen[s.Interface] = true
			ifaces = append(ifaces, s.Interface)
		}
		subnets = append(subnets, s.document())
	}
	sort.Strings(ifaces)
	if ifaces == nil {
		ifaces = []string{}
	}
	socket, leases := c.Socket, c.Leases
	if socket == "" {
		socket = ControlSocket
	}
	if leases == "" {
		leases = LeaseFile
	}
	var classes []any
	lcs := append([]LeaseClass(nil), c.LeaseClasses...)
	sort.Slice(lcs, func(i, j int) bool { return lcs[i].Name < lcs[j].Name })
	for _, lc := range lcs {
		classes = append(classes, jsonMap{"name": lc.Name, "test": macTest(lc.MACs),
			"valid-lifetime": lc.Seconds, "min-valid-lifetime": lifetimeMin(lc.Seconds, lc.Min), "max-valid-lifetime": lifetimeMax(lc.Seconds, lc.Max)})
	}
	if test := c.dropTest(); test != "" {
		classes = append(classes, jsonMap{"name": "DROP", "test": test})
	}
	hooks := []any{jsonMap{"library": "libdhcp_lease_cmds.so"}}
	if c.Script != "" {
		hooks = append(hooks, jsonMap{"library": "libdhcp_run_script.so", "parameters": jsonMap{"name": c.Script, "sync": false}})
	}
	d4 := jsonMap{
		"interfaces-config": jsonMap{"interfaces": ifaces, "dhcp-socket-type": "raw", "re-detect": true,
			"service-sockets-max-retries": SocketRetries, "service-sockets-retry-wait-time": SocketRetryWait.Milliseconds()},
		"control-socket":     jsonMap{"socket-type": "unix", "socket-name": socket},
		"lease-database":     jsonMap{"type": "memfile", "persist": true, "name": leases, "lfc-interval": 3600},
		"hooks-libraries":    hooks,
		"authoritative":      true,
		"valid-lifetime":     3600,
		"min-valid-lifetime": 5,
		"max-valid-lifetime": 86400,
		// no lease caching (on by default in Kea 3, threshold 0.25): a client that asks again soon would get the time its
		// lease has left, so a short lease of 9 s renewed a second later would be 8 s; Kea wants a float, "0" is refused
		"cache-threshold": json.Number("0.0"),
		"subnet4":         subnets,
		"loggers": []any{jsonMap{"name": "kea-dhcp4", "severity": "INFO",
			"output_options": []any{jsonMap{"output": "stdout"}}}},
	}
	if classes != nil {
		d4["client-classes"] = classes // Kea does not take an empty list
	}
	return jsonMap{"Dhcp4": d4}, nil
}

func (s Subnet) document() jsonMap {
	pools := []any{}
	for _, p := range s.Pools {
		pools = append(pools, jsonMap{"pool": p.Start.String() + " - " + p.End.String()})
	}
	opts := s.Options().data()
	if opts == nil {
		opts = []any{}
	}
	resv := []any{}
	rs := append([]Reservation(nil), s.Reservations...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].MAC < rs[j].MAC })
	for _, r := range rs {
		m := jsonMap{"hw-address": strings.ToLower(r.MAC)}
		if r.IP.IsValid() {
			m["ip-address"] = r.IP.String()
		}
		if r.Hostname != "" {
			m["hostname"] = r.Hostname
		}
		if od := r.Options.data(); len(od) > 0 {
			m["option-data"] = od
		}
		resv = append(resv, m)
	}
	return jsonMap{
		"id": s.ID, "subnet": s.Subnet.Masked().String(), "interface": s.Interface,
		"valid-lifetime": s.LeaseSeconds, "min-valid-lifetime": lifetimeMin(s.LeaseSeconds, s.LeaseMin), "max-valid-lifetime": lifetimeMax(s.LeaseSeconds, s.LeaseMax),
		"pools": pools, "option-data": opts, "reservations": resv,
	}
}

// lifetimeMin and lifetimeMax are the bounds of the lease time a client may ask for: what was set, or 5 s and an hour
// (more when the lease time is more).
func lifetimeMin(lease, set int) int {
	if set > 0 {
		return set
	}
	return minInt(lease, 5)
}

func lifetimeMax(lease, set int) int {
	if set > 0 {
		return set
	}
	return maxInt(lease, 3600)
}

func joinAddrs(a []netip.Addr) string {
	s := make([]string, len(a))
	for i, x := range a {
		s[i] = x.String()
	}
	return strings.Join(s, ", ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
