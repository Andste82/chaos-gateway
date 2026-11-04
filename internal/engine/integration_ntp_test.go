//go:build testbed

package engine_test

import (
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The level 1 tests of the NTP server's side in DHCP (plan M34, the list M34_LEVEL1): the real Kea in the gateway's namespace
// hands the client of a test network the gateway address as its NTP server (option 42) where the network's time service is on
// and the service namespace exists, and busybox's udhcpc, which asks for the option, shows what it was told. The NTP server
// itself and the clients that query it are in internal/api (e2e_ntp_test.go, e2e_ntpfaults_test.go).

// setNTP sets the `ntp` block of a local network.
func setNTP(c *model.Configuration, id string, n *model.NetworkNtp) {
	net := (*c.Networks)[id]
	lan, _ := net.AsLanNetwork()
	lan.Ntp = n
	_ = net.FromLanNetwork(lan)
	(*c.Networks)[id] = net
}

// ntpsrvOf starts a DHCP client that asks for option 42 in a namespace and returns the servers it was told (empty for none).
func (b *dhcpBed) ntpsrvOf(ns *testbed.Namespace) (servers string, bound bool) {
	b.t.Helper()
	line, ok := waitLine(b.t, b.udhcpcWith(ns, 8, "-O", "ntpsrv"), "bound", 1, 30*time.Second)
	return field(line, "ntpsrv"), ok
}

// M34 test: Kea hands out the gateway address as NTP server (option 42) to a client of a network whose time service is on
// (the default, decision Q2) when the service namespace exists; a network that names servers keeps them; a network that turns
// the time service off hands out none.
func TestKeaHandsTheClientTheGatewayAsNTPServerWhereTheTimeServiceIsOnOnTheRealKernel(t *testing.T) {
	b := newDHCPBedWith(t, true)

	// the default: the gateway address of the network the client is in, and nothing was set for it
	b.apply(func(c *model.Configuration) { dhcpOn(c); withClients(c) })
	got, ok := b.ntpsrvOf(b.top.A)
	if !ok {
		t.Fatalf("A got no lease\n%s", b.top.GW.Must("ip", "addr"))
	}
	if got != testbed.LAN0Gateway {
		t.Errorf("a client of a network with the default time service was told the NTP server %q, want the gateway address %s", got, testbed.LAN0Gateway)
	}
	// a network that names its servers keeps them
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		withClients(c)
		setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("60s"), Options: &model.DhcpOptions{NtpServers: &[]string{"192.0.2.123"}}})
	})
	if got, ok := b.ntpsrvOf(b.top.A); !ok || got != "192.0.2.123" {
		t.Errorf("a network that names 192.0.2.123 told its client %q (bound %v)", got, ok)
	}

	// a network that turns the time service off: Kea hands out no NTP server
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		withClients(c)
		setNTP(c, tIoT, &model.NetworkNtp{Enabled: ptr(false)})
	})
	if got, ok := b.ntpsrvOf(b.top.A); !ok || got != "" {
		t.Errorf("a network without the time service told its client the NTP server %q (bound %v)", got, ok)
	}

	// another network is not touched by any of this: the default gateway address of its own
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		setDHCP(c, labID, &model.DhcpScope{LeaseTime: ptrS("60s")})
		withClients(c)
		setNTP(c, tIoT, &model.NetworkNtp{Enabled: ptr(false)})
	})
	if got, ok := b.ntpsrvOf(b.top.C); !ok || got != testbed.LAN1Gateway {
		t.Errorf("a client of the second network was told the NTP server %q (bound %v), want its gateway address %s", got, ok, testbed.LAN1Gateway)
	}
}

// M34 test: where no service namespace exists the NTP server is not there, and Kea does not name the gateway as one.
func TestKeaHandsOutNoNTPServerWithoutAServiceNamespaceOnTheRealKernel(t *testing.T) {
	b := newDHCPBed(t)
	b.apply(func(c *model.Configuration) { dhcpOn(c); withClients(c) })
	got, ok := b.ntpsrvOf(b.top.A)
	if !ok {
		t.Fatalf("A got no lease\n%s", b.top.GW.Must("ip", "addr"))
	}
	if got != "" {
		t.Errorf("without a service namespace a client was told the NTP server %q", got)
	}
	// servers that the network names are its own business and are kept
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		withClients(c)
		setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("60s"), Options: &model.DhcpOptions{NtpServers: &[]string{"192.0.2.123"}}})
	})
	if got, ok := b.ntpsrvOf(b.top.A); !ok || got != "192.0.2.123" {
		t.Errorf("a network that names 192.0.2.123 told its client %q (bound %v)", got, ok)
	}
}
