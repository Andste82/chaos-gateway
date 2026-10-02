package testbed

import (
	"fmt"
	"strings"
)

// End is one end of a veth pair.
type End struct {
	NS *Namespace
	// If is the interface name inside NS.
	If string
	// MAC is optional; testbed MACs are fixed so tests can name devices by MAC.
	MAC string
	// Addr is an optional address in CIDR notation.
	Addr string
	// Master is an optional bridge of NS that the interface becomes a port of.
	Master string
}

// Link joins two namespaces with a veth pair, switches offloads off so tc and netem see real
// packets (spike S1), and brings both ends up.
func (b *Bed) Link(x, y End) {
	b.t.Helper()
	args := []string{"link", "add", x.If, "netns", x.NS.Name}
	if x.MAC != "" {
		args = append(args, "address", x.MAC)
	}
	args = append(args, "type", "veth", "peer", "name", y.If, "netns", y.NS.Name)
	if y.MAC != "" {
		args = append(args, "address", y.MAC)
	}
	if out, err := runHost("ip", args...); err != nil {
		b.t.Fatalf("testbed: ip %s: %v: %s", strings.Join(args, " "), err, out)
	}
	for _, e := range []End{x, y} {
		if e.Master != "" {
			e.NS.Must("ip", "link", "set", e.If, "master", e.Master)
		}
		if e.Addr != "" {
			e.NS.Must("ip", "addr", "add", e.Addr, "dev", e.If)
		}
		e.NS.Must("ip", "link", "set", e.If, "up")
		// offloads are best effort: a virtual device may not support every feature
		_, _ = e.NS.Run(ctxBackground(), "ethtool", "-K", e.If, "tso", "off", "gso", "off", "gro", "off", "tx", "off", "rx", "off")
	}
}

// Bridge adds a bridge to the namespace and brings it up; addr is an optional address on it.
func (n *Namespace) Bridge(name, addr string) {
	n.bed.t.Helper()
	n.Must("ip", "link", "add", name, "type", "bridge")
	if addr != "" {
		n.Must("ip", "addr", "add", addr, "dev", name)
	}
	n.Must("ip", "link", "set", name, "up")
}

// Addr adds an address in CIDR notation to an interface.
func (n *Namespace) Addr(dev, cidr string) {
	n.bed.t.Helper()
	n.Must("ip", "addr", "add", cidr, "dev", dev)
}

// Route adds a route, e.g. Route("default", "via", "10.10.0.1").
func (n *Namespace) Route(args ...string) {
	n.bed.t.Helper()
	n.Must("ip", append([]string{"route", "add"}, args...)...)
}

// MAC returns the hardware address of an interface.
func (n *Namespace) MAC(dev string) string {
	n.bed.t.Helper()
	return n.Must("cat", fmt.Sprintf("/sys/class/net/%s/address", dev))
}
