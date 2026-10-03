package appliance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Cmd is a command the harness runs on the host (with sudo when it is not root).
type Cmd []string

func (c Cmd) String() string { return strings.Join(c, " ") }

// Plan is the host side of a topology: the commands that build it and those that take it down
// again (ignoring errors, so a half-built topology is cleaned up too).
type Plan struct {
	Setup, Teardown []Cmd
}

// Topology is the network around the gateway VM (plan §4.2, level 2):
//
//	three ports:  server ns ── br-up ── [uplink NIC]  VM  [test NIC] ── br-lan ── client ns
//	                                                      [mgmt NIC] ── br-mgmt ── host (SSH, NAT to the Internet)
//	two ports:    the management network lives behind the uplink: the host's management address
//	              is on br-up and there is no management NIC.
//
// The uplink and the management interface belong to the operating system (netplan, see Seed); the
// test port is assigned to Chaos Gateway by the configuration.
type Topology struct {
	// Prefix starts the names of everything this topology creates; it must be short (interface
	// names are limited to 15 characters) and unique among concurrent topologies.
	Prefix   string
	TwoPorts bool
}

// Names of the topology's objects.
type Names struct {
	BrUp, BrLan, BrMgmt    string
	TapUp, TapLan, TapMgmt string
	NSServer, NSClient     string
	vethSrv, vethSrvPeer   string
	vethCli, vethCliPeer   string
}

// Names returns the object names.
func (t Topology) Names() Names {
	p := t.Prefix
	return Names{
		BrUp: p + "-up", BrLan: p + "-lan", BrMgmt: p + "-mg",
		TapUp: p + "-t0", TapLan: p + "-t1", TapMgmt: p + "-t2",
		NSServer: p + "-server", NSClient: p + "-client",
		vethSrv: p + "-vs", vethSrvPeer: p + "-vs1", vethCli: p + "-vc", vethCliPeer: p + "-vc1",
	}
}

// NICs returns the VM's NICs: uplink, test network and, with three ports, management.
func (t Topology) NICs() []NIC {
	n := t.Names()
	nics := []NIC{{Tap: n.TapUp, MAC: MACs.Uplink}, {Tap: n.TapLan, MAC: MACs.LAN}}
	if !t.TwoPorts {
		nics = append(nics, NIC{Tap: n.TapMgmt, MAC: MACs.Mgmt})
	}
	return nics
}

// Validate checks the names.
func (t Topology) Validate() error {
	if t.Prefix == "" {
		return fmt.Errorf("appliance: a topology needs a prefix")
	}
	n := t.Names()
	for _, name := range []string{n.BrUp, n.BrLan, n.BrMgmt, n.TapUp, n.TapLan, n.TapMgmt, n.vethSrv, n.vethSrvPeer, n.vethCli, n.vethCliPeer} {
		if len(name) > 15 {
			return fmt.Errorf("appliance: the interface name %q is longer than 15 characters: use a shorter prefix", name)
		}
	}
	return nil
}

// Plan returns the commands for the host side.
func (t Topology) Plan() Plan {
	n := t.Names()
	var s, d []Cmd
	add := func(setup Cmd, teardown Cmd) {
		s = append(s, setup)
		if teardown != nil {
			d = append([]Cmd{teardown}, d...) // the teardown runs in reverse
		}
	}
	bridge := func(br string) {
		add(Cmd{"ip", "link", "add", br, "type", "bridge"}, Cmd{"ip", "link", "del", br})
		add(Cmd{"ip", "link", "set", br, "up"}, nil)
		// Docker's FORWARD policy is DROP and br_netfilter sends bridged frames through it: the
		// bridges of the topology are exempt, as they are plain switches
		add(Cmd{"iptables", "-I", "FORWARD", "-i", br, "-j", "ACCEPT"}, Cmd{"iptables", "-D", "FORWARD", "-i", br, "-j", "ACCEPT"})
		add(Cmd{"iptables", "-I", "FORWARD", "-o", br, "-j", "ACCEPT"}, Cmd{"iptables", "-D", "FORWARD", "-o", br, "-j", "ACCEPT"})
	}
	tap := func(tap, br string) {
		add(Cmd{"ip", "tuntap", "add", "dev", tap, "mode", "tap"}, Cmd{"ip", "link", "del", tap})
		add(Cmd{"ip", "link", "set", tap, "master", br}, nil)
		add(Cmd{"ip", "link", "set", tap, "up"}, nil)
	}
	host := func(ns, veth, peer, br, addr, mac string, defaultVia string) {
		add(Cmd{"ip", "netns", "add", ns}, Cmd{"ip", "netns", "del", ns})
		add(Cmd{"ip", "link", "add", veth, "type", "veth", "peer", "name", peer}, Cmd{"ip", "link", "del", veth})
		add(Cmd{"ip", "link", "set", peer, "netns", ns}, nil)
		add(Cmd{"ip", "-n", ns, "link", "set", peer, "name", "eth0"}, nil)
		if mac != "" {
			add(Cmd{"ip", "-n", ns, "link", "set", "eth0", "address", mac}, nil)
		}
		add(Cmd{"ip", "-n", ns, "addr", "add", addr, "dev", "eth0"}, nil)
		add(Cmd{"ip", "-n", ns, "link", "set", "eth0", "up"}, nil)
		add(Cmd{"ip", "-n", ns, "link", "set", "lo", "up"}, nil)
		if defaultVia != "" {
			add(Cmd{"ip", "-n", ns, "route", "add", "default", "via", defaultVia}, nil)
		}
		add(Cmd{"ip", "link", "set", veth, "master", br}, nil)
		add(Cmd{"ip", "link", "set", veth, "up"}, nil)
	}

	bridge(n.BrUp)
	bridge(n.BrLan)
	tap(n.TapUp, n.BrUp)
	tap(n.TapLan, n.BrLan)
	host(n.NSServer, n.vethSrv, n.vethSrvPeer, n.BrUp, ServerAddr+"/24", "", "")
	host(n.NSClient, n.vethCli, n.vethCliPeer, n.BrLan, ClientAddr+"/24", ClientMAC, GatewayLAN)

	// the host's management address: on its own bridge, or on the uplink bridge in the two-port topology
	mgmtBr := n.BrMgmt
	if t.TwoPorts {
		mgmtBr = n.BrUp
	} else {
		bridge(n.BrMgmt)
		tap(n.TapMgmt, n.BrMgmt)
	}
	add(Cmd{"ip", "addr", "add", HostMgmt + "/24", "dev", mgmtBr}, nil)
	// the VM reaches the Internet (packages, Docker) through the host, as a gateway's management
	// network does
	add(Cmd{"sysctl", "-w", "net.ipv4.ip_forward=1"}, nil)
	add(Cmd{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", MgmtNetwork, "!", "-d", MgmtNetwork, "-j", "MASQUERADE"},
		Cmd{"iptables", "-t", "nat", "-D", "POSTROUTING", "-s", MgmtNetwork, "!", "-d", MgmtNetwork, "-j", "MASQUERADE"})
	return Plan{Setup: s, Teardown: d}
}

// Host runs the host side of a topology.
type Host struct {
	// Sudo prefixes the commands; the default is "sudo -n" unless the process is root.
	Sudo []string
}

func (h Host) argv(c Cmd) []string {
	sudo := h.Sudo
	if sudo == nil && os.Getuid() != 0 {
		sudo = []string{"sudo", "-n"}
	}
	return append(append([]string(nil), sudo...), c...)
}

// Run runs one command and returns its output.
func (h Host) Run(ctx context.Context, c Cmd) (string, error) {
	a := h.argv(c)
	out, err := exec.CommandContext(ctx, a[0], a[1:]...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w\n%s", c, err, out)
	}
	return string(out), nil
}

// Up builds the topology; on an error it takes down what was built.
func (h Host) Up(ctx context.Context, p Plan) error {
	for i, c := range p.Setup {
		if _, err := h.Run(ctx, c); err != nil {
			h.Down(context.WithoutCancel(ctx), p)
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return nil
}

// Down takes the topology down, ignoring errors.
func (h Host) Down(ctx context.Context, p Plan) {
	for _, c := range p.Teardown {
		_, _ = h.Run(ctx, c)
	}
}
