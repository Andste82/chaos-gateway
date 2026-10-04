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
