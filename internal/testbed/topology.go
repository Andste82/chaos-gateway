package testbed

import (
	"testing"
)

// Addresses of the default topology. Test network 0 and 1 are IPv4 only; the server has no route
// back to them, so every test that crosses the gateway also exercises NAT (spike S1).
const (
	LAN0Subnet  = "10.10.0.0/24"
	LAN0Gateway = "10.10.0.1"
	ClientAAddr = "10.10.0.11"
	ClientBAddr = "10.10.0.12"

	LAN1Subnet  = "10.20.0.0/24"
	LAN1Gateway = "10.20.0.1"
	ClientCAddr = "10.20.0.13"

	// UplinkGateway is the gateway's address on the uplink; ServerAddr and ServerAddr2 are two
	// addresses of the server (destination selectors need a second one).
	UplinkGateway = "203.0.113.1"
	ServerAddr    = "203.0.113.10"
	ServerAddr2   = "203.0.113.20"
	// InternetAddr is a host behind the uplink router that only a default route reaches: a
	// forwarded packet to it shows which default route is used (spike S12).
	InternetAddr = "198.51.100.10"

	// MgmtGateway is the gateway's address on its management interface; MgmtPeer is the
	// management router, the next hop of the gateway's default route in the main table.
	MgmtGateway = "192.168.56.1"
	MgmtPeer    = "192.168.56.254"
)

// MACs of the default topology are fixed so tests can name devices by MAC.
const (
	ClientAMAC = "02:00:00:00:00:0b"
	ClientBMAC = "02:00:00:00:00:0c"
	ClientCMAC = "02:00:00:00:01:0d"
	GWLan0MAC  = "02:00:00:00:00:01"
	GWLan1MAC  = "02:00:00:00:01:01"
)

// Topology is the default topology of plan §4.2:
//
//	A ──┐                                                     ┌─ Server (203.0.113.10, .20)
//	B ──┼─ Switch0 ─ lan0 ┐                                   │  no route back → NAT required
//	    ┘                 br-lan0 [ GW ] wan0 ────────────────┘
//	C ───── Switch1 ─ lan1 ┘ br-lan1      mgmt0 ─ Mgmt (192.168.56.254)
//
// Each test network is attached through a gateway-owned bridge with the physical port as its
// port, as in production (plan §2.2, spike S12). The management interface has a default route
// in the main routing table.
type Topology struct {
	*Bed
	GW, Switch0, Switch1, A, B, C, Server, Mgmt *Namespace
	// Up, RC and Site exist with WithRemotes: the uplink switch, a remote client and a remote site.
	Up, RC, Site *Namespace
}

// Addresses of the remote machines (WithRemotes).
const (
	RemoteClientAddr = "203.0.113.30"
	RemoteSiteAddr   = "203.0.113.40"
	// ClientNetHost lies in the network behind the remote client (10.50.0.0/24), SiteNetHost in the
	// remote site's network (10.60.0.0/24): each is an address of its machine's loopback.
	ClientNetHost = "10.50.0.10"
	SiteNetHost   = "10.60.0.10"
)

// NewDefault builds the default topology. With the default options the gateway is a plain
// forwarder (forwarding on, masquerade on wan0).
func NewDefault(t testing.TB, opts ...Option) *Topology {
	t.Helper()
	b := New(t, opts...)
	top := &Topology{Bed: b}

	top.GW = b.Add("gw")
	top.Switch0 = b.Add("sw0")
	top.Switch1 = b.Add("sw1")
	top.A = b.Add("a")
	top.B = b.Add("b")
	top.C = b.Add("c")
	top.Server = b.Add("srv")
	top.Mgmt = b.Add("mgmt")

	for _, sw := range []*Namespace{top.Switch0, top.Switch1} {
		sw.Bridge("br0", "")
	}
	master0, master1 := "", ""
	if b.cfg.gatewayBridges {
		top.GW.Bridge("br-lan0", LAN0Gateway+"/24")
		top.GW.Bridge("br-lan1", LAN1Gateway+"/24")
		master0, master1 = "br-lan0", "br-lan1"
	}

	// test network 0: gateway port, devices A and B
	b.Link(End{NS: top.GW, If: "lan0", MAC: GWLan0MAC, Master: master0}, End{NS: top.Switch0, If: "p1", Master: "br0"})
	b.Link(End{NS: top.A, If: "eth0", MAC: ClientAMAC, Addr: ClientAAddr + "/24"}, End{NS: top.Switch0, If: "p11", Master: "br0"})
	b.Link(End{NS: top.B, If: "eth0", MAC: ClientBMAC, Addr: ClientBAddr + "/24"}, End{NS: top.Switch0, If: "p12", Master: "br0"})
	// test network 1: gateway port, device C
	b.Link(End{NS: top.GW, If: "lan1", MAC: GWLan1MAC, Master: master1}, End{NS: top.Switch1, If: "q1", Master: "br0"})
	b.Link(End{NS: top.C, If: "eth0", MAC: ClientCMAC, Addr: ClientCAddr + "/24"}, End{NS: top.Switch1, If: "q13", Master: "br0"})
	top.A.Route("default", "via", LAN0Gateway)
	top.B.Route("default", "via", LAN0Gateway)
	top.C.Route("default", "via", LAN1Gateway)

	// uplink: the server has no route back to the test networks
	if b.cfg.remotes {
		top.Up = b.Add("up")
		top.RC = b.Add("rc")
		top.Site = b.Add("site")
		top.Up.Bridge("br0", "")
		b.Link(End{NS: top.GW, If: "wan0", Addr: UplinkGateway + "/24"}, End{NS: top.Up, If: "pw", Master: "br0"})
		b.Link(End{NS: top.Server, If: "eth0", Addr: ServerAddr + "/24"}, End{NS: top.Up, If: "ps", Master: "br0"})
		b.Link(End{NS: top.RC, If: "eth0", Addr: RemoteClientAddr + "/24"}, End{NS: top.Up, If: "pr", Master: "br0"})
		b.Link(End{NS: top.Site, If: "eth0", Addr: RemoteSiteAddr + "/24"}, End{NS: top.Up, If: "pt", Master: "br0"})
		top.RC.Addr("lo", ClientNetHost+"/32")
		top.Site.Addr("lo", SiteNetHost+"/32")
	} else {
		b.Link(End{NS: top.GW, If: "wan0", Addr: UplinkGateway + "/24"}, End{NS: top.Server, If: "eth0", Addr: ServerAddr + "/24"})
	}
	top.Server.Addr("eth0", ServerAddr2+"/24")
	top.Server.Addr("lo", InternetAddr+"/32")

	// management interface with its own default route in the main table
	b.Link(End{NS: top.GW, If: "mgmt0", Addr: MgmtGateway + "/24"}, End{NS: top.Mgmt, If: "eth0", Addr: MgmtPeer + "/24"})
	if b.cfg.managementDefaultRoute {
		top.GW.Route("default", "via", MgmtPeer)
	}

	if b.cfg.plainGateway {
		top.PlainGateway()
	}
	return top
}

// PlainGateway turns the gateway into a plain forwarder: IPv4 forwarding on and masquerade of
// everything that leaves through wan0. It is what spike S1 used and what M1's tests need; from
// M4 on the product configures the gateway instead (WithPlainGateway(false)).
func (t *Topology) PlainGateway() {
	t.t.Helper()
	t.GW.Sysctl("net.ipv4.ip_forward", "1")
	t.GW.MustStdin(plainGatewayRuleset, "nft", "-f", "-")
}

// plainGatewayRuleset is a text ruleset on purpose: it is test scaffolding, not product output.
const plainGatewayRuleset = `table ip cgbase {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    oifname "wan0" masquerade
  }
}
`
