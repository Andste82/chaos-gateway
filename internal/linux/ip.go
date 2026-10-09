package linux

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Link is one entry of `ip -j [-d] link show`.
type Link struct {
	Index     int      `json:"ifindex"`
	Name      string   `json:"ifname"`
	Flags     []string `json:"flags"`
	MTU       int      `json:"mtu"`
	Qdisc     string   `json:"qdisc"`
	OperState string   `json:"operstate"`
	Group     string   `json:"group"`
	Type      string   `json:"link_type"`
	MAC       string   `json:"address"`
	Master    string   `json:"master"`
	Parent    string   `json:"link"` // the lower device of a VLAN or veth peer name, when known
	NetNSID   *int     `json:"link_netnsid"`
	Info      *struct {
		Kind      string `json:"info_kind"`
		SlaveKind string `json:"info_slave_kind"`
	} `json:"linkinfo"`
}

// Kind returns the device kind (bridge, veth, ifb, wireguard, ...), empty for physical devices.
func (l Link) Kind() string {
	if l.Info == nil {
		return ""
	}
	return l.Info.Kind
}

// Up reports whether the administrative UP flag is set.
func (l Link) Up() bool { return l.HasFlag("UP") }

// HasFlag reports whether the link carries the flag (UP, LOWER_UP, ...).
func (l Link) HasFlag(f string) bool {
	for _, x := range l.Flags {
		if x == f {
			return true
		}
	}
	return false
}

// Address is one address of an interface.
type Address struct {
	Family    string `json:"family"` // inet or inet6
	Local     string `json:"local"`
	PrefixLen int    `json:"prefixlen"`
	Scope     string `json:"scope"`
	Label     string `json:"label"`
	Dynamic   bool   `json:"dynamic"`
	// Secondary is true for every address on the interface after the first (the kernel's own
	// distinction, reported by `ip -j addr`; which one ends up primary depends on the order
	// addresses were added in, not on any property of the address itself).
	Secondary    bool   `json:"secondary,omitempty"`
	Protocol     string `json:"protocol"`
	ValidLife    int64  `json:"valid_life_time"`
	PreferedLife int64  `json:"preferred_life_time"`
}

// Addrs is one interface with its addresses (`ip -j addr show`).
type Addrs struct {
	Index  int       `json:"ifindex"`
	Name   string    `json:"ifname"`
	Flags  []string  `json:"flags"`
	MAC    string    `json:"address"`
	Addrs  []Address `json:"addr_info"`
	MTU    int       `json:"mtu"`
	Operst string    `json:"operstate"`
	Type   string    `json:"link_type"`
	Master string    `json:"master"`
}

// Route is one entry of `ip -j route show`.
type Route struct {
	Type     string   `json:"type"` // empty means unicast
	Dst      string   `json:"dst"`
	Gateway  string   `json:"gateway"`
	Dev      string   `json:"dev"`
	Table    string   `json:"table"` // empty means main
	Protocol string   `json:"protocol"`
	Scope    string   `json:"scope"`
	PrefSrc  string   `json:"prefsrc"`
	Metric   *int     `json:"metric"`
	Flags    []string `json:"flags"`
	// Metrics holds the route's metrics; `ip -j` prints the path MTU there (without telling whether it is
	// locked).
	Metrics []RouteMetrics `json:"metrics"`
}

// RouteMetrics is an entry of a route's metrics; only the MTU is read.
type RouteMetrics struct {
	MTU int `json:"mtu"`
}

// MTU is the path MTU the route carries, 0 when it has none.
func (r Route) MTU() int {
	for _, m := range r.Metrics {
		if m.MTU != 0 {
			return m.MTU
		}
	}
	return 0
}

// Rule is one entry of `ip -j rule show`.
type Rule struct {
	Priority int    `json:"priority"`
	Src      string `json:"src"`
	SrcLen   *int   `json:"srclen"`
	Dst      string `json:"dst"`
	DstLen   *int   `json:"dstlen"`
	Iif      string `json:"iif"`
	Oif      string `json:"oif"`
	Fwmark   string `json:"fwmark"`
	Fwmask   string `json:"fwmask"`
	Table    string `json:"table"`
	Protocol string `json:"protocol"`
	Action   string `json:"action"` // set for non-lookup rules (blackhole, prohibit, ...)
}

func parseList[T any](what string, data []byte) ([]T, error) {
	data = trimSpace(data)
	if len(data) == 0 {
		return nil, nil // the tools print nothing for an empty list in some versions
	}
	var out []T
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", what, err)
	}
	return out, nil
}

func trimSpace(b []byte) []byte { return []byte(strings.TrimSpace(string(b))) }

// ParseLinks parses `ip -j [-d] link show`.
func ParseLinks(data []byte) ([]Link, error) { return parseList[Link]("ip link", data) }

// ParseAddrs parses `ip -j addr show`.
func ParseAddrs(data []byte) ([]Addrs, error) { return parseList[Addrs]("ip addr", data) }

// ParseRoutes parses `ip -j route show`.
func ParseRoutes(data []byte) ([]Route, error) { return parseList[Route]("ip route", data) }

// ParseRules parses `ip -j rule show`.
func ParseRules(data []byte) ([]Rule, error) { return parseList[Rule]("ip rule", data) }

// RouteGet is the answer of `ip -j route get DST [from SRC iif DEV]`: the route the kernel picks for
// one packet, after the policy rules.
type RouteGet struct {
	Dst     string `json:"dst"`
	From    string `json:"from"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
	// Table is the routing table the lookup ended in; empty means main.
	Table   string `json:"table"`
	PrefSrc string `json:"prefsrc"`
	Iif     string `json:"iif"`
	// Type is "unicast" (empty), "local", "blackhole", "unreachable" or "prohibit".
	Type string `json:"type"`
	// Unreachable is set by the executor when the kernel finds no route (the tool exits with an
	// error then, which is an answer here); Error holds the tool's message.
	Unreachable bool   `json:"unreachable,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ParseRouteGet parses `ip -j route get`: one entry.
func ParseRouteGet(data []byte) (*RouteGet, error) {
	l, err := parseList[RouteGet]("ip route get", data)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, fmt.Errorf("parse ip route get: no route in the answer")
	}
	return &l[0], nil
}
