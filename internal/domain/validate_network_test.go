package domain

import (
	"testing"
)

var (
	iot  = "/networks/" + idIoT
	hub  = "/networks/" + idHub
	link = "/networks/" + idLink
	cl   = hub + "/clients/" + idClient
)

type mutation struct {
	name   string
	change func(t *testing.T, d doc)
	path   string
	code   string
}

func runMutations(t *testing.T, tests []mutation) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := baseDoc(t)
			tt.change(t, d)
			wantError(t, d.validate(t), tt.path, tt.code)
		})
	}
}

func TestNetworkRules(t *testing.T) {
	runMutations(t, []mutation{
		{"lan prefix too long", func(t *testing.T, d doc) { d.set(t, "10.10.0.1/31", "networks", idIoT, "address") }, iot + "/address", CodeInvalidPrefixLength},
		{"lan address is the network address", func(t *testing.T, d doc) { d.set(t, "10.10.0.0/24", "networks", idIoT, "address") }, iot + "/address", CodeInvalidAddress},
		{"lan address is the broadcast address", func(t *testing.T, d doc) { d.set(t, "10.10.0.255/24", "networks", idIoT, "address") }, iot + "/address", CodeInvalidAddress},
		{"lan in the link-local range of the service namespace", func(t *testing.T, d doc) { d.set(t, "169.254.100.1/24", "networks", idIoT, "address") }, iot + "/address", CodeReservedRange},
		{"lan in the multicast range", func(t *testing.T, d doc) { d.set(t, "225.1.1.1/24", "networks", idIoT, "address") }, iot + "/address", CodeReservedRange},
		{"hub overlaps the lan", func(t *testing.T, d doc) { d.set(t, "10.10.0.5/24", "networks", idHub, "address") }, hub + "/address", CodeOverlappingSubnet},
		{"link overlaps the hub", func(t *testing.T, d doc) { d.set(t, "10.99.0.0/31", "networks", idLink, "address") }, link + "/address", CodeOverlappingSubnet},
		{"lan port is already the uplink", func(t *testing.T, d doc) { d.set(t, obj(t, `{"name":"wan0"}`), "networks", idIoT, "interfaces", 0) }, iot + "/interfaces/0", CodeDuplicateInterface},
		{"lan port has the MAC of the management interface", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"mac":"52:54:00:12:34:99"}`), "management", "interface")
			d.set(t, obj(t, `{"mac":"52:54:00:12:34:99"}`), "networks", idIoT, "interfaces", 0)
		}, iot + "/interfaces/0", CodeDuplicateInterface},
		{"lan port with a multicast MAC", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"lan0","mac":"01:00:5e:00:00:01"}`), "networks", idIoT, "interfaces", 0)
		}, iot + "/interfaces/0/mac", CodeInvalidMAC},
		{"uplink with a multicast MAC", func(t *testing.T, d doc) { d.set(t, obj(t, `{"mac":"ff:ff:ff:ff:ff:ff"}`), "uplink", "interface") }, "/uplink/interface/mac", CodeInvalidMAC},
		{"uplink gateway is not unicast", func(t *testing.T, d doc) { d.set(t, "224.0.0.1", "uplink", "gateway") }, "/uplink/gateway", CodeInvalidAddress},
		{"upstream resolver is not unicast", func(t *testing.T, d doc) { d.set(t, []any{"0.0.0.0"}, "uplink", "dns_upstream") }, "/uplink/dns_upstream/0", CodeInvalidAddress},
		{"management sources overlap a test network", func(t *testing.T, d doc) { d.set(t, []any{"10.10.0.0/16"}, "management", "allowed_sources") }, "/management/allowed_sources/0", CodeManagementOverlap},
		{"management sources with host bits", func(t *testing.T, d doc) { d.set(t, []any{"192.168.88.5/24"}, "management", "allowed_sources") }, "/management/allowed_sources/0", CodeHostBitsSet},

		{"pool end before start", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"start":"10.10.0.199","end":"10.10.0.100"}`), "networks", idIoT, "dhcp", "pools", 0)
		}, iot + "/dhcp/pools/0/end", CodePoolOrder},
		{"pool outside the subnet", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"start":"10.11.0.1","end":"10.11.0.9"}`), "networks", idIoT, "dhcp", "pools", 0)
		}, iot + "/dhcp/pools/0/start", CodeOutsideSubnet},
		{"pool contains the gateway", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"start":"10.10.0.1","end":"10.10.0.50"}`), "networks", idIoT, "dhcp", "pools", 0)
		}, iot + "/dhcp/pools/0", CodeInvalidAddress},
		{"pool ends at the broadcast address", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"start":"10.10.0.100","end":"10.10.0.255"}`), "networks", idIoT, "dhcp", "pools", 0)
		}, iot + "/dhcp/pools/0/end", CodeOutsideSubnet},
		{"overlapping pools", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"start":"10.10.0.100","end":"10.10.0.150"}`), obj(t, `{"start":"10.10.0.150","end":"10.10.0.199"}`)}, "networks", idIoT, "dhcp", "pools")
		}, iot + "/dhcp/pools/1", CodePoolOverlap},
		{"lease time below one second", func(t *testing.T, d doc) { d.set(t, "0s", "networks", idIoT, "dhcp", "lease_time") }, iot + "/dhcp/lease_time", CodeInvalidDuration},
		{"dhcp router is multicast", func(t *testing.T, d doc) { d.set(t, "224.0.0.1", "networks", idIoT, "dhcp", "options", "router") }, iot + "/dhcp/options/router", CodeInvalidAddress},
		{"dhcp custom option set twice", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"code":128,"value":"a"}`), obj(t, `{"code":128,"value":"b"}`)}, "networks", idIoT, "dhcp", "options", "custom")
		}, iot + "/dhcp/options/custom/1/code", CodeDuplicateIdentifier},
		{"dhcp custom option uses a code Kea manages", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"code":6,"value":"a"}`)}, "networks", idIoT, "dhcp", "options", "custom")
		}, iot + "/dhcp/options/custom/0/code", CodeReservedOption},
		{"static dns entry twice", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"name":"a.test","addresses":["10.10.0.5"]}`), obj(t, `{"name":"A.test","addresses":["10.10.0.6"]}`)}, "networks", idIoT, "dns", "static_entries")
		}, iot + "/dns/static_entries/1/name", CodeDuplicateName},
		{"downstream route via an address outside the subnet", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"destination":"172.20.0.0/16","via":"10.20.0.9"}`)}, "networks", idIoT, "routes")
		}, iot + "/routes/0/via", CodeOutsideSubnet},
		{"downstream route with host bits", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"destination":"172.20.0.1/16","via":"10.10.0.9"}`)}, "networks", idIoT, "routes")
		}, iot + "/routes/0/destination", CodeHostBitsSet},
		{"downstream route into the own subnet", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"destination":"10.10.0.128/25","via":"10.10.0.9"}`)}, "networks", idIoT, "routes")
		}, iot + "/routes/0/destination", CodeOverlappingSubnet},
	})
}

func TestWireGuardRules(t *testing.T) {
	runMutations(t, []mutation{
		{"two networks on one listen port", func(t *testing.T, d doc) { d.set(t, 51820, "networks", idLink, "listen_port") }, link + "/listen_port", CodeDuplicatePort},
		{"endpoint port out of range", func(t *testing.T, d doc) { d.set(t, "gw.example.net:99999", "networks", idHub, "endpoint") }, hub + "/endpoint", CodeInvalidEndpoint},
		{"endpoint with a malformed address", func(t *testing.T, d doc) { d.set(t, "300.1.1.1:51820", "networks", idHub, "endpoint") }, hub + "/endpoint", CodeInvalidEndpoint},
		{"endpoint with an invalid host name", func(t *testing.T, d doc) { d.set(t, "-bad-.example:51820", "networks", idHub, "endpoint") }, hub + "/endpoint", CodeInvalidEndpoint},
		{"hub with a peer", func(t *testing.T, d doc) { d.set(t, obj(t, `{"address":"10.99.0.9"}`), "networks", idHub, "peer") }, hub + "/peer", CodeWrongKind},
		{"hub with link routes", func(t *testing.T, d doc) { d.set(t, []any{"10.77.0.0/24"}, "networks", idHub, "routes") }, hub + "/routes", CodeWrongKind},
		{"hub prefix too long", func(t *testing.T, d doc) { d.set(t, "10.99.0.1/31", "networks", idHub, "address") }, hub + "/address", CodeInvalidPrefixLength},
		{"client address outside the hub subnet", func(t *testing.T, d doc) { d.set(t, "10.98.0.2", "networks", idHub, "clients", idClient, "address") }, cl + "/address", CodeOutsideSubnet},
		{"client address is the hub address", func(t *testing.T, d doc) { d.set(t, "10.99.0.1", "networks", idHub, "clients", idClient, "address") }, cl + "/address", CodeDuplicateAddress},
		{"two clients with one address", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"other","address":"10.99.0.2"}`), "networks", idHub, "clients", idNew)
		}, hub + "/clients/" + idNew + "/address", CodeDuplicateAddress},
		{"client network overlaps the lan", func(t *testing.T, d doc) {
			d.set(t, []any{"10.10.0.0/25"}, "networks", idHub, "clients", idClient, "client_networks")
		}, cl + "/client_networks/0", CodeOverlappingSubnet},
		{"client network with host bits", func(t *testing.T, d doc) {
			d.set(t, []any{"10.50.0.1/24"}, "networks", idHub, "clients", idClient, "client_networks")
		}, cl + "/client_networks/0", CodeHostBitsSet},
		{"two clients with overlapping client networks", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"other","address":"10.99.0.3","client_networks":["10.50.0.0/16"]}`), "networks", idHub, "clients", idNew)
		}, hub + "/clients/" + idNew + "/client_networks/0", CodeOverlappingSubnet},
		{"client dns custom without servers", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"mode":"custom"}`), "networks", idHub, "clients", idClient, "dns")
		}, cl + "/dns/servers", CodeMissingField},
		{"client dns servers without custom", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"mode":"gateway","servers":["1.1.1.1"]}`), "networks", idHub, "clients", idClient, "dns")
		}, cl + "/dns/servers", CodeUnexpectedField},
		{"client keepalive out of range", func(t *testing.T, d doc) { d.set(t, "70000s", "networks", idHub, "clients", idClient, "keepalive") }, cl + "/keepalive", CodeInvalidDuration},
		{"provided key without a public key", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"mode":"provided"}`), "networks", idHub, "clients", idClient, "key")
		}, cl + "/key/public_key", "required"},
		{"export once with a provided key", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"mode":"provided","public_key":"xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=","export_once":true}`), "networks", idHub, "clients", idClient, "key")
		}, cl + "/key/export_once", CodeKeySettings},
		{"key rotation with a provided key", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"mode":"provided","public_key":"xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=","generation":2}`), "networks", idHub, "clients", idClient, "key")
		}, cl + "/key/generation", CodeKeySettings},
		{"the same public key twice", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"mode":"provided","public_key":"xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="}`), "networks", idHub, "clients", idClient, "key")
		}, link + "/peer/key/public_key", CodeDuplicatePublicKey},

		{"link with a /30", func(t *testing.T, d doc) { d.set(t, "10.255.0.0/30", "networks", idLink, "address") }, link + "/address", CodeInvalidPrefixLength},
		{"link without a peer", func(t *testing.T, d doc) { d.del(t, "networks", idLink, "peer") }, link + "/peer", "required"},
		{"link peer outside the transfer network", func(t *testing.T, d doc) { d.set(t, "10.255.1.1", "networks", idLink, "peer", "address") }, link + "/peer/address", CodeOutsideSubnet},
		{"link peer is the gateway address", func(t *testing.T, d doc) { d.set(t, "10.255.0.0", "networks", idLink, "peer", "address") }, link + "/peer/address", CodeDuplicateAddress},
		{"link with clients", func(t *testing.T, d doc) { d.set(t, obj(t, `{}`), "networks", idLink, "clients") }, link + "/clients", CodeWrongKind},
		{"link route overlaps the lan", func(t *testing.T, d doc) { d.set(t, []any{"10.10.0.0/16"}, "networks", idLink, "routes") }, link + "/routes/0", CodeOverlappingSubnet},
		{"link route with host bits", func(t *testing.T, d doc) { d.set(t, []any{"10.60.0.1/24"}, "networks", idLink, "routes") }, link + "/routes/0", CodeHostBitsSet},
		{"link peer endpoint without a port", func(t *testing.T, d doc) { d.set(t, "siteb.example.net:0", "networks", idLink, "peer", "endpoint") }, link + "/peer/endpoint", CodeInvalidEndpoint},
	})
}

func TestTwoPortTopologyMayShareTheUplinkWithManagement(t *testing.T) {
	d := baseDoc(t)
	d.set(t, obj(t, `{"name":"wan0"}`), "management", "interface")
	wantValid(t, d.validate(t))
}

func TestAWellFormedAdditionalNetworkIsAccepted(t *testing.T) {
	d := baseDoc(t)
	d.set(t, obj(t, `{"type":"lan","name":"office","interfaces":[{"name":"lan1"}],"address":"10.30.0.1/24"}`), "networks", idNew)
	wantValid(t, d.validate(t))
}
