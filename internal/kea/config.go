package kea

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Paths inside the Kea container (Kea restricts them: /run/kea and /var/lib/kea).
const (
	ControlSocket = "/run/kea/kea4-ctrl.sock"
	LeaseFile     = "/var/lib/kea/kea-leases4.csv"
	// HookScript is the script the run_script hook calls; it notifies the API.
	HookScript = "/usr/local/bin/chaosgw-kea-hook"
)

// Pool is an address range.
type Pool struct{ Start, End netip.Addr }

// Reservation binds an address to a MAC address (a device with `fixed_ip`).
type Reservation struct {
	MAC      string
	IP       netip.Addr
	Hostname string
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
	// LeaseSeconds is the lease time.
	LeaseSeconds int
	Router       netip.Addr
	DNS          []netip.Addr
	NTP          []netip.Addr
	Domain       string
	Custom       []CustomOption
	Reservations []Reservation
}

// Config is the whole DHCPv4 configuration.
type Config struct {
	Subnets []Subnet
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

// DefaultPool is the second half of the subnet without the network, broadcast and gateway addresses
// (plan: "Default - the second half of the subnet").
func DefaultPool(p netip.Prefix, gateway netip.Addr) (Pool, bool) {
	if !p.Addr().Is4() || p.Bits() > 29 {
		return Pool{}, false
	}
	base := p.Masked().Addr().As4()
	size := uint32(1) << (32 - p.Bits())
	start := toU32(base) + size/2
	end := toU32(base) + size - 2 // the last usable address
	if start == toU32(gateway.As4()) {
		start++
	}
	return Pool{Start: fromU32(start), End: fromU32(end)}, start <= end
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
			if !s.Subnet.Contains(r.IP) {
				return fmt.Errorf("kea: the reservation %s for %s is outside %s", r.IP, r.MAC, s.Subnet)
			}
		}
	}
	return nil
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
	hooks := []any{jsonMap{"library": "libdhcp_lease_cmds.so"}}
	if c.Script != "" {
		hooks = append(hooks, jsonMap{"library": "libdhcp_run_script.so", "parameters": jsonMap{"name": c.Script, "sync": false}})
	}
	return jsonMap{"Dhcp4": jsonMap{
		"interfaces-config":  jsonMap{"interfaces": ifaces, "dhcp-socket-type": "raw", "re-detect": true},
		"control-socket":     jsonMap{"socket-type": "unix", "socket-name": socket},
		"lease-database":     jsonMap{"type": "memfile", "persist": true, "name": leases, "lfc-interval": 3600},
		"hooks-libraries":    hooks,
		"authoritative":      true,
		"valid-lifetime":     3600,
		"min-valid-lifetime": 5,
		"max-valid-lifetime": 86400,
		"subnet4":            subnets,
		"loggers": []any{jsonMap{"name": "kea-dhcp4", "severity": "INFO",
			"output_options": []any{jsonMap{"output": "stdout"}}}},
	}}, nil
}

func (s Subnet) document() jsonMap {
	pools := []any{}
	for _, p := range s.Pools {
		pools = append(pools, jsonMap{"pool": p.Start.String() + " - " + p.End.String()})
	}
	var opts []any
	opt := func(name, data string) { opts = append(opts, jsonMap{"name": name, "data": data}) }
	if s.Router.IsValid() {
		opt("routers", s.Router.String())
	}
	if len(s.DNS) > 0 {
		opt("domain-name-servers", joinAddrs(s.DNS))
	}
	if len(s.NTP) > 0 {
		opt("ntp-servers", joinAddrs(s.NTP))
	}
	if s.Domain != "" {
		opt("domain-name", s.Domain)
	}
	for _, c := range s.Custom {
		opts = append(opts, jsonMap{"code": c.Code, "data": c.Value, "always-send": true})
	}
	if opts == nil {
		opts = []any{}
	}
	resv := []any{}
	rs := append([]Reservation(nil), s.Reservations...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].MAC < rs[j].MAC })
	for _, r := range rs {
		m := jsonMap{"hw-address": strings.ToLower(r.MAC), "ip-address": r.IP.String()}
		if r.Hostname != "" {
			m["hostname"] = r.Hostname
		}
		resv = append(resv, m)
	}
	return jsonMap{
		"id": s.ID, "subnet": s.Subnet.Masked().String(), "interface": s.Interface,
		"valid-lifetime": s.LeaseSeconds, "min-valid-lifetime": minInt(s.LeaseSeconds, 5), "max-valid-lifetime": maxInt(s.LeaseSeconds, 3600),
		"pools": pools, "option-data": opts, "reservations": resv,
	}
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
