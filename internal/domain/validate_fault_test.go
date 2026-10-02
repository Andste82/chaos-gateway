package domain

import (
	"testing"
)

func faultPath(id string) string { return "/faults/" + id }

func TestFaultRules(t *testing.T) {
	f := faultPath(idFaultNet)
	runMutations(t, []mutation{
		{"jitter larger than latency", func(t *testing.T, d doc) { d.set(t, "200ms", "faults", idFaultNet, "jitter") }, f + "/jitter", CodeJitterExceedsLatency},
		{"jitter without latency", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"source":{"network":"IoT"},"jitter":"5ms"}`), "faults", idNew)
		}, faultPath(idNew) + "/jitter", CodeJitterExceedsLatency},
		{"reorder without latency", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"source":{"network":"IoT"},"reorder":"5%"}`), "faults", idNew)
		}, faultPath(idNew) + "/reorder", CodeReorderNeedsLatency},
		{"distribution without jitter", func(t *testing.T, d doc) { d.set(t, "normal", "faults", idFaultNet, "distribution") }, f + "/distribution", CodeDistributionNeedsJit},
		{"loss correlation without loss", func(t *testing.T, d doc) { d.set(t, "20%", "faults", idFaultNet, "loss_correlation") }, f + "/loss_correlation", CodeLossCorrelationNeeds},
		{"loss and burst loss", func(t *testing.T, d doc) {
			d.set(t, "1%", "faults", idFaultNet, "loss")
			d.set(t, obj(t, `{"p":"1%","r":"30%"}`), "faults", idFaultNet, "burst_loss")
		}, f + "/burst_loss", CodeExclusive},
		{"blackout and flapping", func(t *testing.T, d doc) {
			d.set(t, true, "faults", idFaultNet, "blackout")
			d.set(t, obj(t, `{"up":"1s","down":"1s"}`), "faults", idFaultNet, "flapping")
		}, f + "/flapping", CodeExclusive},
		{"flapping without a down time", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"up":"1s","down":"0s"}`), "faults", idFaultNet, "flapping")
		}, f + "/flapping", CodeInvalidFlapping},
		{"flat parameters mixed with directions", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"latency":"5ms"}`), "faults", idFaultNet, "upload")
		}, f, CodeMixedDirections},
		{"jitter larger than latency in one direction", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"latency":"5ms","jitter":"50ms"}`), "faults", idFaultDev, "upload")
		}, faultPath(idFaultDev) + "/upload/jitter", CodeJitterExceedsLatency},
		{"a fault that sets nothing", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"source":{"network":"IoT"}}`), "faults", idNew)
		}, faultPath(idNew), CodeEmptyFault},
		{"a fault without a source", func(t *testing.T, d doc) { d.del(t, "faults", idFaultNet, "source") }, f + "/source", CodeMissingField},
		{"a source that does not exist", func(t *testing.T, d doc) { d.set(t, obj(t, `{"device":"ghost"}`), "faults", idFaultNet, "source") }, f + "/source/device", CodeUnknownReference},
		{"a source group by an unknown name", func(t *testing.T, d doc) { d.set(t, obj(t, `{"group":"IoT"}`), "faults", idFaultNet, "source") }, f + "/source/group", CodeUnknownReference},
		{"ports without a protocol", func(t *testing.T, d doc) { d.set(t, []any{80}, "faults", idFaultNet, "ports") }, f + "/protocol", CodePortsRequireProtocol},
		{"ports with icmp", func(t *testing.T, d doc) {
			d.set(t, "icmp", "faults", idFaultNet, "protocol")
			d.set(t, []any{80}, "faults", idFaultNet, "ports")
		}, f + "/protocol", CodePortsRequireProtocol},
		{"a port range that ends before it starts", func(t *testing.T, d doc) {
			d.set(t, "tcp", "faults", idFaultNet, "protocol")
			d.set(t, []any{obj(t, `{"from":9000,"to":8000}`)}, "faults", idFaultNet, "port_ranges")
		}, f + "/port_ranges/0", CodeInvalidPortRange},
		{"a destination prefix with host bits", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"cidr":"203.0.113.5/24"}`), "faults", idFaultNet, "destination")
		}, f + "/destination/cidr", CodeHostBitsSet},
		{"a destination network that does not exist", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"network":"nowhere"}`), "faults", idFaultNet, "destination")
		}, f + "/destination/network", CodeUnknownReference},

		{"a remote network that matches no known prefix", func(t *testing.T, d doc) {
			d.del(t, "routing")
			d.set(t, obj(t, `{"remote_network":{"cidr":"172.31.0.0/24"}}`), "faults", idFaultNet, "source")
		}, f + "/source/remote_network/cidr", CodeRemoteNetwork},

		{"a tunnel fault with a source", func(t *testing.T, d doc) { d.set(t, obj(t, `{"network":"IoT"}`), "faults", idFaultTun, "source") }, faultPath(idFaultTun) + "/source", CodeUnexpectedField},
		{"a tunnel fault without a tunnel", func(t *testing.T, d doc) { d.del(t, "faults", idFaultTun, "tunnel") }, faultPath(idFaultTun) + "/tunnel", CodeMissingField},
		{"a tunnel fault with a rate", func(t *testing.T, d doc) { d.set(t, "1Mbit", "faults", idFaultTun, "rate") }, faultPath(idFaultTun) + "/rate", CodeTunnelParameter},
		{"a tunnel fault with a duplicate", func(t *testing.T, d doc) { d.set(t, "1%", "faults", idFaultTun, "duplicate") }, faultPath(idFaultTun) + "/duplicate", CodeTunnelParameter},
		{"a tunnel fault with a destination", func(t *testing.T, d doc) { d.set(t, obj(t, `{"uplink":true}`), "faults", idFaultTun, "destination") }, faultPath(idFaultTun), CodeUnexpectedField},
		{"a tunnel fault on a network that is no link", func(t *testing.T, d doc) { d.set(t, obj(t, `{"link":"IoT"}`), "faults", idFaultTun, "tunnel") }, faultPath(idFaultTun) + "/tunnel/link", CodeUnknownReference},
		{"a tunnel fault on a client that is a plain device", func(t *testing.T, d doc) { d.set(t, obj(t, `{"client":"esp32-42"}`), "faults", idFaultTun, "tunnel") }, faultPath(idFaultTun) + "/tunnel/client", CodeUnknownReference},
		{"an mtu fault without mtu settings", func(t *testing.T, d doc) { d.del(t, "faults", idFaultMTU, "mtu") }, faultPath(idFaultMTU) + "/mtu", CodeMissingField},
		{"an mtu fault with latency", func(t *testing.T, d doc) { d.set(t, "5ms", "faults", idFaultMTU, "latency") }, faultPath(idFaultMTU), CodeMixedFamily},
		{"an impairment fault with mtu settings", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"size":1200}`), "faults", idFaultNet, "mtu")
		}, f + "/mtu", CodeMixedFamily},
		{"an impairment fault with a tunnel", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"link":"site-b"}`), "faults", idFaultNet, "tunnel")
		}, f + "/tunnel", CodeMixedFamily},
	})
}

func TestARemoteNetworkInsideAClientNetworkIsAccepted(t *testing.T) {
	d := baseDoc(t)
	d.del(t, "routing")
	d.set(t, obj(t, `{"remote_network":{"cidr":"10.50.0.0/25"}}`), "faults", idFaultNet, "source")
	wantValid(t, d.validate(t))
	d.set(t, obj(t, `{"remote_network":{"client":"lab-rA"}}`), "faults", idFaultNet, "source")
	wantValid(t, d.validate(t))
	d.set(t, obj(t, `{"remote_network":{"link":"site-b"}}`), "faults", idFaultNet, "source")
	wantValid(t, d.validate(t))
}

func TestDirectionalFaultsAndAllFamiliesAreAccepted(t *testing.T) {
	d := baseDoc(t)
	d.set(t, obj(t, `{"source":{"group":"sensors"},"upload":{"latency":"200ms","jitter":"20ms","distribution":"normal"},"download":{"burst_loss":{"p":"1%","r":"30%"}}}`), "faults", idNew)
	d.set(t, obj(t, `{"source":{"global":true},"family":"mtu","mtu":{"size":1280,"mode":"icmp"},"protocol":"tcp","ports":[443]}`), "faults", idNew2)
	wantValid(t, d.validate(t))
}

func TestAccessRuleRules(t *testing.T) {
	r := "/access_rules/" + idRule
	runMutations(t, []mutation{
		{"a reset on udp", func(t *testing.T, d doc) {
			d.set(t, "reset", "access_rules", idRule, "action")
			d.set(t, "udp", "access_rules", idRule, "protocol")
		}, r + "/action", CodeResetRequiresTCP},
		{"a reset without a protocol", func(t *testing.T, d doc) {
			d.set(t, "reset", "access_rules", idRule, "action")
			d.del(t, "access_rules", idRule, "protocol")
			d.del(t, "access_rules", idRule, "ports")
		}, r + "/action", CodeResetRequiresTCP},
		{"cutting existing connections on udp", func(t *testing.T, d doc) {
			d.set(t, "udp", "access_rules", idRule, "protocol")
			d.set(t, true, "access_rules", idRule, "cut_existing")
		}, r + "/cut_existing", CodeCutRequiresTCP},
		{"ports with any protocol", func(t *testing.T, d doc) { d.del(t, "access_rules", idRule, "protocol") }, r + "/protocol", CodePortsRequireProtocol},
		{"an unknown source", func(t *testing.T, d doc) { d.set(t, obj(t, `{"network":"nowhere"}`), "access_rules", idRule, "source") }, r + "/source/network", CodeUnknownReference},
		{"a rule that is missing from the order", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"source":{"global":true},"action":"drop"}`), "access_rules", idNew)
		}, "/access_rule_order", CodeRuleOrder},
		{"a rule twice in the order", func(t *testing.T, d doc) { d.set(t, []any{idRule, idRule}, "access_rule_order") }, "/access_rule_order/1", CodeRuleOrder},
		{"an unknown rule in the order", func(t *testing.T, d doc) { d.set(t, []any{idRule, idNew}, "access_rule_order") }, "/access_rule_order/1", CodeUnknownReference},
		{"no order at all", func(t *testing.T, d doc) { d.del(t, "access_rule_order") }, "/access_rule_order", CodeRuleOrder},
	})
}

func TestAccessRulesKeepTheirOrder(t *testing.T) {
	d := baseDoc(t)
	d.set(t, obj(t, `{"name":"allow-mqtt","source":{"device":"esp32-42"},"action":"allow","protocol":"tcp","ports":[8883]}`), "access_rules", idNew)
	d.set(t, []any{idNew, idRule}, "access_rule_order")
	wantValid(t, d.validate(t))
}

func TestRoutingRules(t *testing.T) {
	p := "/routing/protocols/" + idProtocol
	runMutations(t, []mutation{
		{"bgp without settings", func(t *testing.T, d doc) { d.del(t, "routing", "protocols", idProtocol, "bgp") }, p + "/bgp", CodeMissingField},
		{"bgp without an AS number", func(t *testing.T, d doc) { d.del(t, "routing", "asn") }, "/routing/asn", CodeMissingField},
		{"ospf settings on a bgp protocol", func(t *testing.T, d doc) { d.set(t, obj(t, `{}`), "routing", "protocols", idProtocol, "ospf") }, p + "/ospf", CodeProtocolSettings},
		{"hold time shorter than three keepalives", func(t *testing.T, d doc) { d.set(t, "5s", "routing", "protocols", idProtocol, "bgp", "hold_time") }, p + "/bgp/hold_time", CodeTimers},
		{"keepalive below one second", func(t *testing.T, d doc) {
			d.set(t, "500ms", "routing", "protocols", idProtocol, "bgp", "keepalive_time")
		}, p + "/bgp/keepalive_time", CodeTimers},
		{"bgp neighbor outside the link", func(t *testing.T, d doc) {
			d.set(t, "10.1.1.1", "routing", "protocols", idProtocol, "bgp", "neighbor_address")
		}, p + "/bgp/neighbor_address", CodeOutsideSubnet},
		{"two bgp protocols on one link", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"bgp2","type":"bgp","link":"site-b","bgp":{"neighbor_asn":64514}}`), "routing", "protocols", idNew)
		}, "/routing/protocols/" + idNew + "/type", CodeDuplicateRoutingKind},
		{"a protocol on a network that is no link", func(t *testing.T, d doc) { d.set(t, "lab-hub", "routing", "protocols", idProtocol, "link") }, p + "/link", CodeUnknownReference},
		{"ospf dead interval not longer than hello", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"ospf-b","type":"ospf","link":"site-b","ospf":{"hello_interval":"10s","dead_interval":"10s"}}`), "routing", "protocols", idNew)
		}, "/routing/protocols/" + idNew + "/ospf/dead_interval", CodeTimers},
		{"babel hello below one second", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"babel-b","type":"babel","link":"site-b","babel":{"hello_interval":"100ms"}}`), "routing", "protocols", idNew)
		}, "/routing/protocols/" + idNew + "/babel/hello_interval", CodeTimers},
		{"an announced prefix with host bits", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"cidr":"10.1.1.1/24"}`)}, "routing", "protocols", idProtocol, "announce")
		}, p + "/announce/0/cidr", CodeHostBitsSet},
		{"an announced client that does not exist", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"client":"ghost"}`)}, "routing", "protocols", idProtocol, "announce")
		}, p + "/announce/0/client", CodeUnknownReference},
		{"an import prefix with max_length below its length", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"prefix":"10.60.0.0/16","max_length":8}`)}, "routing", "protocols", idProtocol, "import", "allowed_prefixes")
		}, p + "/import/allowed_prefixes/0/max_length", CodeInvalidPrefixLength},
		{"external routing without a table", func(t *testing.T, d doc) { d.set(t, obj(t, `{"enabled":true}`), "routing", "external") }, "/routing/external/table", CodeMissingField},
		{"external routing on the main table", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"enabled":true,"table":254}`), "routing", "external")
		}, "/routing/external/table", CodeInvalidTable},
		{"external routing on a table of the gateway", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"enabled":true,"table":100}`), "routing", "external")
		}, "/routing/external/table", CodeInvalidTable},
		{"a router id that is multicast", func(t *testing.T, d doc) { d.set(t, "224.0.0.1", "routing", "router_id") }, "/routing/router_id", CodeInvalidAddress},
		{"a confirm timeout of zero", func(t *testing.T, d doc) { d.set(t, "0s", "settings", "commit_confirm_timeout") }, "/settings/commit_confirm_timeout", CodeInvalidDuration},
	})
}

func TestExternalRoutingOnAFreeTableIsAccepted(t *testing.T) {
	d := baseDoc(t)
	d.set(t, obj(t, `{"enabled":true,"table":200,"import":{"allowed_prefixes":[{"prefix":"10.70.0.0/16","max_length":24}]}}`), "routing", "external")
	wantValid(t, d.validate(t))
}

func TestProfileRules(t *testing.T) {
	pr := "/profiles/" + idProfile + "/parts"
	runMutations(t, []mutation{
		{"a profile with jitter larger than latency", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"latency":"5ms","jitter":"50ms"}`), "profiles", idProfile, "parts", "impairment")
		}, pr + "/impairment/jitter", CodeJitterExceedsLatency},
		{"a profile with flat and directional parameters", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"latency":"5ms","upload":{"latency":"5ms"}}`), "profiles", idProfile, "parts", "impairment")
		}, pr + "/impairment", CodeMixedDirections},
		{"an mtu part that is too small", func(t *testing.T, d doc) { d.set(t, obj(t, `{"size":500}`), "profiles", idProfile, "parts", "mtu") }, pr + "/mtu/size", "minimum"},
		{"a dns delay without a delay", func(t *testing.T, d doc) { d.del(t, "profiles", idProfile, "parts", "dns", "delay") }, pr + "/dns/delay", CodeInvalidDNSFault},
		{"a dns servfail with a delay", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"action":"servfail","delay":"1s"}`), "profiles", idProfile, "parts", "dns")
		}, pr + "/dns/delay", CodeInvalidDNSFault},
		{"a wrong answer without answers", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"action":"wrong_answer"}`), "profiles", idProfile, "parts", "dns")
		}, pr + "/dns/answers", CodeInvalidDNSFault},
		{"answers without the wrong answer action", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"action":"nxdomain","answers":["10.0.0.1"]}`), "profiles", idProfile, "parts", "dns")
		}, pr + "/dns/answers", CodeInvalidDNSFault},
		{"a short ttl without a ttl", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"action":"short_ttl"}`), "profiles", idProfile, "parts", "dns")
		}, pr + "/dns/ttl", CodeInvalidDNSFault},
		{"a ttl below one second", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"action":"short_ttl","ttl":"500ms"}`), "profiles", idProfile, "parts", "dns")
		}, pr + "/dns/ttl", CodeInvalidDuration},
		{"a delay of zero", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"action":"delay","delay":"0s"}`), "profiles", idProfile, "parts", "dns")
		}, pr + "/dns/delay", CodeInvalidDuration},
		{"a dns fault with a name twice", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"action":"servfail","names":["a.test","A.test"]}`), "profiles", idProfile, "parts", "dns")
		}, pr + "/dns/names/1", CodeDuplicateName},
		{"a tls case over udp", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"case":"expired","protocol":"udp"}`), "profiles", idProfile, "parts", "tls")
		}, pr + "/tls/protocol", CodeInvalidTLSCase},
		{"interception settings on a certificate case", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"case":"expired","intercept":{}}`), "profiles", idProfile, "parts", "tls")
		}, pr + "/tls/intercept", CodeInvalidTLSCase},
		{"an http status rule without a status", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"case":"intercept","intercept":{"http_rules":[{"action":"status"}]}}`), "profiles", idProfile, "parts", "tls")
		}, pr + "/tls/intercept/http_rules/0/status", CodeInvalidTLSCase},
		{"an http block rule with a delay", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"case":"intercept","intercept":{"http_rules":[{"action":"block","delay":"1s"}]}}`), "profiles", idProfile, "parts", "tls")
		}, pr + "/tls/intercept/http_rules/0/delay", CodeInvalidTLSCase},
	})
}

func TestATlsCaseWithPortsNeedsNoProtocol(t *testing.T) {
	// TLS runs over TCP: omitting the protocol means tcp
	d := baseDoc(t)
	d.set(t, obj(t, `{"case":"expired","ports":[443,8883]}`), "profiles", idProfile, "parts", "tls")
	wantValid(t, d.validate(t))
}

func TestAnInterceptionProfileIsAccepted(t *testing.T) {
	d := baseDoc(t)
	d.set(t, obj(t, `{"case":"intercept","protocol":"tcp","ports":[443],"intercept":{"block_quic":true,"http_rules":[{"action":"status","status":503},{"action":"throttle","rate":"1Mbit"},{"action":"modify","modify":{"response_headers":{"X":"y"}}}]}}`), "profiles", idProfile, "parts", "tls")
	wantValid(t, d.validate(t))
}
