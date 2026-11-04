package engine

import (
	"net/netip"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
)

// The gateway's NTP server answers on the gateway address of a network only where a redirect leads UDP 123 into the service
// namespace: the compiler writes none without it, and the input chain drops the packet. The access explanation must not
// say otherwise for the address of a tunnel.
func TestTheNTPServerAnswersOnTheGatewayAddressesOnlyWhereTheServiceNamespaceExists(t *testing.T) {
	snap := &Snapshot{
		Bridges: []compiler.Bridge{
			{Name: "br-iot", Address: netip.MustParsePrefix("10.10.0.1/24"), NTP: true},
			{Name: "br-lab", Address: netip.MustParsePrefix("10.20.0.1/24"), NTP: false},
		},
		WireGuardInterfaces: []compiler.WGInterface{{Name: "wg-lab", Address: netip.MustParsePrefix("10.99.0.1/24")}},
		Service:             &compiler.ServiceNS{},
	}
	got := ntpGatewayAddrs(snap)
	if len(got) != 2 || got[0] != netip.MustParseAddr("10.10.0.1") || got[1] != netip.MustParseAddr("10.99.0.1") {
		t.Errorf("with a service namespace: %v", got)
	}
	snap.Service = nil
	if got := ntpGatewayAddrs(snap); len(got) != 0 {
		t.Errorf("without a service namespace the server answers on %v", got)
	}
}
