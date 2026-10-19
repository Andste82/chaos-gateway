package run

import (
	"net/netip"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func ptrTo[T any](v T) *T { return &v }

func TestAConnectionIsEstablishedPastTheHandshake(t *testing.T) {
	for _, c := range []struct {
		f    engine.Flow
		want bool
	}{
		{engine.Flow{Protocol: "tcp", State: "ESTABLISHED"}, true},
		{engine.Flow{Protocol: "tcp", State: "TIME_WAIT"}, true},
		// a refused attempt (one SYN, answered by a RST) is over, but it was never established
		{engine.Flow{Protocol: "tcp", State: "CLOSE", Upload: engine.Traffic{Packets: 1}, Download: engine.Traffic{Packets: 1}}, false},
		{engine.Flow{Protocol: "tcp", State: "CLOSE"}, false},
		// a connection that was reset after its handshake was
		{engine.Flow{Protocol: "tcp", State: "CLOSE", Upload: engine.Traffic{Packets: 4}, Download: engine.Traffic{Packets: 3}}, true},
		{engine.Flow{Protocol: "tcp", State: "SYN_SENT"}, false},
		{engine.Flow{Protocol: "tcp", State: "SYN_RECV"}, false},
		{engine.Flow{Protocol: "tcp", State: ""}, false},
		{engine.Flow{Protocol: "udp", State: "ASSURED", Download: engine.Traffic{Packets: 2}}, true},
		{engine.Flow{Protocol: "udp", State: "UNREPLIED"}, false},
		{engine.Flow{Protocol: "udp", State: "ASSURED"}, false},
	} {
		if got := established(c.f); got != c.want {
			t.Errorf("%+v: %v", c.f, got)
		}
	}
}

func TestAConnectionMatchesAProtocolPortsAndDestination(t *testing.T) {
	snap := &engine.Snapshot{
		Bridges:             []compiler.Bridge{{NetworkID: "net-iot", Address: netip.MustParsePrefix("10.10.0.1/24")}},
		WireGuardInterfaces: []compiler.WGInterface{{NetworkID: "net-hub", Address: netip.MustParsePrefix("10.99.0.1/24")}},
	}
	f := engine.Flow{Protocol: "tcp", Dst: netip.MustParseAddr("203.0.113.9"), DPort: 8883}
	lan := engine.Flow{Protocol: "udp", Dst: netip.MustParseAddr("10.10.0.77"), DPort: 5353}
	tcp, udp, any := model.Protocol("tcp"), model.Protocol("udp"), model.Protocol("any")
	for name, c := range map[string]struct {
		m    model.TrafficMatch
		f    engine.Flow
		want bool
	}{
		"everything":            {model.TrafficMatch{}, f, true},
		"the protocol":          {model.TrafficMatch{Protocol: &tcp}, f, true},
		"another protocol":      {model.TrafficMatch{Protocol: &udp}, f, false},
		"any protocol":          {model.TrafficMatch{Protocol: &any}, f, true},
		"a port":                {model.TrafficMatch{Ports: &[]int{443, 8883}}, f, true},
		"another port":          {model.TrafficMatch{Ports: &[]int{443}}, f, false},
		"a port range":          {model.TrafficMatch{PortRanges: &[]model.PortRange{{From: 8000, To: 9000}}}, f, true},
		"outside the range":     {model.TrafficMatch{PortRanges: &[]model.PortRange{{From: 1, To: 1024}}}, f, false},
		"an address":            {model.TrafficMatch{Destination: &model.Destination{Cidr: ptrTo("203.0.113.9")}}, f, true},
		"another address":       {model.TrafficMatch{Destination: &model.Destination{Cidr: ptrTo("203.0.113.8")}}, f, false},
		"a prefix":              {model.TrafficMatch{Destination: &model.Destination{Cidr: ptrTo("203.0.113.0/24")}}, f, true},
		"a network":             {model.TrafficMatch{Destination: &model.Destination{Network: ptrTo("net-iot")}}, lan, true},
		"another network":       {model.TrafficMatch{Destination: &model.Destination{Network: ptrTo("net-hub")}}, lan, false},
		"the uplink":            {model.TrafficMatch{Destination: &model.Destination{Uplink: ptrTo(model.DestinationUplink(true))}}, f, true},
		"not the uplink":        {model.TrafficMatch{Destination: &model.Destination{Uplink: ptrTo(model.DestinationUplink(true))}}, lan, false},
		"a host name needs M20": {model.TrafficMatch{Destination: &model.Destination{Hostname: ptrTo("broker.example.com")}}, f, false},
	} {
		if got := matchFlow(snap, c.m, c.f); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestTargetsOverlapWhenTheyShareADeviceOrANetwork(t *testing.T) {
	snap := &engine.Snapshot{
		Config: &model.Configuration{Groups: &map[string]model.Group{
			"g1": {Name: "g1", Members: &[]string{"d1", "d2"}},
			"g2": {Name: "g2", Members: &[]string{"d2", "d3"}},
			"g3": {Name: "g3", Members: &[]string{"d4"}},
		}},
		Devices: []engine.DeviceState{{ID: "d1", Network: "n1"}, {ID: "d2", Network: "n1"}, {ID: "d3", Network: "n2"}, {ID: "d4", Network: "n3"}},
	}
	dev := func(id string) model.Scope { return model.Scope{Device: &id} }
	grp := func(id string) model.Scope { return model.Scope{Group: &id} }
	net := func(id string) model.Scope { return model.Scope{Network: &id} }
	for _, c := range []struct {
		name string
		a, b model.Scope
		want bool
	}{
		{"the same device", dev("d1"), dev("d1"), true},
		{"two devices", dev("d1"), dev("d2"), false},
		{"a device and its group", dev("d1"), grp("g1"), true},
		{"a device and another group", dev("d3"), grp("g1"), false},
		{"two groups with a member in common", grp("g1"), grp("g2"), true},
		{"two groups", grp("g1"), grp("g3"), false},
		{"a device and its network", dev("d1"), net("n1"), true},
		{"a device and another network", dev("d1"), net("n2"), false},
		{"a group and a network it reaches into", grp("g2"), net("n2"), true},
		{"a group and a network it does not", grp("g3"), net("n1"), false},
		{"the same network", net("n9"), net("n9"), true},
		{"two networks", net("n1"), net("n2"), false},
		{"everything and a device", model.Scope{Global: ptrTo(model.ScopeGlobal(true))}, dev("d1"), true},
		{"a network without devices and a device", net("n9"), dev("d1"), false},
		{"a client's networks and the client", model.Scope{RemoteNetwork: &model.RemoteNetworkRef{Client: ptrTo("d1")}}, dev("d1"), true},
		{"a link's prefixes and the link's network", model.Scope{RemoteNetwork: &model.RemoteNetworkRef{Link: ptrTo("n7")}}, net("n7"), true},
	} {
		if got := overlap(scopeTokens(snap, c.a), scopeTokens(snap, c.b)); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
		if got := overlap(scopeTokens(snap, c.b), scopeTokens(snap, c.a)); got != c.want {
			t.Errorf("%s, the other way round: %v", c.name, got)
		}
	}
	if overlap(map[string]bool{"*": true}, map[string]bool{}) {
		t.Error("everything overlaps nothing")
	}
}

func TestAnOverlayActsOnTheTunnelItNames(t *testing.T) {
	snap := &engine.Snapshot{}
	wg := model.Overlay{Wireguard: &model.WireGuardAction{Client: ptrTo("c1"), Action: "disable"}}
	link := model.Overlay{Wireguard: &model.WireGuardAction{Link: ptrTo("L1"), Action: "disable"}}
	tunnel := model.Overlay{Fault: &model.FaultBody{Tunnel: &model.TunnelRef{Client: ptrTo("C2")}}}
	for ov, want := range map[string]struct {
		o   model.Overlay
		key string
	}{"wg client": {wg, "d:c1"}, "wg link": {link, "n:l1"}, "tunnel fault": {tunnel, "d:c2"}} {
		if got := overlayTokens(snap, want.o); !got[want.key] || len(got) != 1 {
			t.Errorf("%s: %v", ov, got)
		}
	}
}
