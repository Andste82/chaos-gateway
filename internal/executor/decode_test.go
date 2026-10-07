package executor

import (
	"errors"
	"strings"
	"testing"
)

const goodNft = `{"nftables":[{"add":{"table":{"family":"inet","name":"chaosgw"}}},{"add":{"chain":{"family":"inet","table":"chaosgw","name":"fwd","type":"filter","hook":"forward","prio":-150,"policy":"accept"}}},{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"fwd","expr":[{"drop":null}]}}}]}`

func TestDecodeAcceptsEveryOperationType(t *testing.T) {
	for name, in := range map[string]string{
		TypeNftApply:          `{"type":"nft_apply","ruleset":` + goodNft + `}`,
		TypeNftAddElements:    `{"type":"nft_add_elements","namespace":"ns1","set":"dns_a","elements":["192.0.2.1","10.0.0.0/8","aa:bb:cc:dd:ee:ff"],"timeout_seconds":60}`,
		TypeNftDelElements:    `{"type":"nft_del_elements","set":"dev_a1b2c3","elements":["10.10.0.5"]}`,
		TypeNftAddMapElements: `{"type":"nft_add_map_elements","namespace":"ns1","map":"ident4_ab12cd","elements":[{"key":"10.10.0.5","value":"3"},{"key":"10.10.0.31 . 203.0.113.10 . 6 . 443","value":"mark_7"},{"key":"10.0.0.5-10.0.0.9 . 198.51.100.0-198.51.100.77 . tcp . 80-443","value":"mark_0"}]}`,
		TypeNftDelMapElements: `{"type":"nft_del_map_elements","map":"ident4_ab12cd","keys":["10.10.0.5","10.0.0.5-10.0.0.9 . 203.0.113.0/24 . tcp . 80-443"]}`,
		TypeRouting:           `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"default","via":"10.0.0.1","dev":"wan0","metric":10},{"action":"replace","family":6,"table":102,"dst":"::/0","type":"prohibit"}],"rules":[{"action":"add","family":4,"priority":1000,"from":"10.10.0.0/24","fwmark":"0x10/0xff","iif":"lan0","table":100}]}`,
		TypeTC:                `{"type":"tc","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","10"]},{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:10","args":["htb","rate","1mbit"]},{"object":"qdisc","action":"add","dev":"wan0","parent":"1:10","handle":"10:","args":["netem","delay","50ms","10ms","loss","1%"]},{"object":"filter","action":"add","dev":"wan0","parent":"1:","args":["protocol","ip","prio","1","u32","match","ip","src","10.0.0.0/24","flowid","1:10"]},{"object":"qdisc","action":"add","dev":"lan0","parent":"ingress"}]}`,
		TypeOffloads:          `{"type":"offloads","devs":["wan0","lan0"]}`,
		TypeDockerUser:        `{"type":"docker_user","action":"ensure","devs":["br-lan0"]}`,
		TypeAssign:            `{"type":"assign_interfaces","devs":["wan0","lan0","br-lan0"]}`,
		TypeRead:              `{"type":"read","what":"routes","table":"100","dev":"wan0"}`,
		TypeLinks:             `{"type":"links","entries":[{"action":"add_bridge","name":"br-lan0"},{"action":"enslave","name":"lan0","master":"br-lan0"},{"action":"up","name":"br-lan0"},{"action":"addr_replace","name":"br-lan0","cidr":"10.10.0.1/24"},{"action":"addr_delete","name":"br-lan0","cidr":"10.9.0.1/24"},{"action":"release","name":"lan1"},{"action":"down","name":"lan1"},{"action":"delete_bridge","name":"br-old"}]}`,
		TypeServiceNS:         `{"type":"service_ns","action":"ensure","name":"cgsvc","host_if":"svc0","peer_if":"svc1","host_cidr":"169.254.100.1/30","peer_cidr":"169.254.100.2/30","holder_pid":4242}`,
		TypeSysctl:            `{"type":"sysctl","entries":[{"name":"ip_forward","value":1},{"name":"accept_ra","dev":"br-lan0","value":0},{"name":"disable_ipv6","dev":"lan0","value":1}]}`,
	} {
		op, err := Decode([]byte(in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if op.OpType() != name {
			t.Errorf("%s decoded as %s", name, op.OpType())
		}
		if _, err := Plan(op); err != nil && name != TypeRead {
			t.Errorf("%s: plan: %v", name, err)
		}
		// the wire form round-trips
		enc, err := Encode(op)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Decode(enc)
		if err != nil {
			t.Fatalf("%s: re-decode: %v\n%s", name, err, enc)
		}
		enc2, _ := Encode(again)
		if string(enc) != string(enc2) {
			t.Errorf("%s: encoding is not stable:\n%s\n%s", name, enc, enc2)
		}
	}
}

// M4c-05 test: an fe80::/64 link-local address is the one IPv6 case the links address action
// accepts, for a Babel link.
func TestLinksAcceptsAnFe80Address(t *testing.T) {
	op, err := Decode([]byte(`{"type":"links","entries":[{"action":"addr_replace","name":"wg-site-b","cidr":"fe80::a/64"},{"action":"addr_delete","name":"wg-site-b","cidr":"fe80::a/64"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(op); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRejects(t *testing.T) {
	nft := func(body string) string { return `{"type":"nft_apply","ruleset":{"nftables":[` + body + `]}}` }
	route := func(fields string) string {
		return `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8","dev":"wan0"` + fields + `}]}`
	}
	rule := func(fields string) string {
		return `{"type":"routing","rules":[{"action":"add","family":4,"priority":1000,"table":100` + fields + `}]}`
	}
	tc := func(entry string) string { return `{"type":"tc","entries":[` + entry + `]}` }
	for _, c := range []struct{ name, in string }{
		{"empty input", ``},
		{"not json", `nft flush ruleset`},
		{"no type", `{"devs":["a"]}`},
		{"unknown type", `{"type":"shell","command":"id"}`},
		{"unknown type with right fields", `{"type":"exec","devs":["wan0"]}`},
		{"unknown field", `{"type":"offloads","devs":["wan0"],"extra":1}`},
		{"duplicate key", `{"type":"offloads","devs":["wan0"],"devs":["lo"]}`},
		{"duplicate key in nested object", `{"type":"read","what":"links","what":"nft"}`},
		{"trailing data", `{"type":"offloads","devs":["wan0"]} {"type":"offloads","devs":["lo"]}`},
		{"type is not a string", `{"type":1}`},
		{"namespace with slash", `{"type":"offloads","namespace":"../x","devs":["wan0"]}`},
		{"namespace with space", `{"type":"offloads","namespace":"a b","devs":["wan0"]}`},
		{"namespace too long", `{"type":"offloads","namespace":"` + strings.Repeat("a", 65) + `","devs":["wan0"]}`},
		{"interface with slash", `{"type":"offloads","devs":["wan0/../x"]}`},
		{"interface too long", `{"type":"offloads","devs":["abcdefghijklmnop"]}`},
		{"interface with space", `{"type":"offloads","devs":["a b"]}`},
		{"no interfaces", `{"type":"offloads","devs":[]}`},

		// nftables scope
		{"nft not json document", `{"type":"nft_apply","ruleset":"flush ruleset"}`},
		{"nft empty", nft(``)},
		{"nft flush ruleset", nft(`{"flush":{"ruleset":null}}`)},
		{"nft other table", nft(`{"add":{"table":{"family":"inet","name":"filter"}}}`)},
		{"nft other family", nft(`{"add":{"table":{"family":"ip","name":"chaosgw"}}}`)},
		{"nft chain in other table", nft(`{"add":{"chain":{"family":"inet","table":"filter","name":"c"}}}`)},
		{"nft rule without family", nft(`{"add":{"rule":{"table":"chaosgw","chain":"c","expr":[]}}}`)},
		{"nft rule without table", nft(`{"add":{"rule":{"family":"inet","chain":"c","expr":[]}}}`)},
		{"nft docker table", nft(`{"delete":{"table":{"family":"ip","name":"nat"}}}`)},
		{"nft metainfo", nft(`{"metainfo":{"json_schema_version":1}}`)},
		{"nft list command", nft(`{"list":{"ruleset":null}}`)},
		{"nft two commands in one item", nft(`{"add":{"table":{"family":"inet","name":"chaosgw"}},"flush":{"ruleset":null}}`)},
		{"nft command with two objects", nft(`{"add":{"table":{"family":"inet","name":"chaosgw"},"chain":{"family":"ip","table":"nat","name":"x"}}}`)},
		{"nft unknown top-level field", `{"type":"nft_apply","ruleset":{"nftables":[{"add":{"table":{"family":"inet","name":"chaosgw"}}}],"x":1}}`},
		{"nft table object names another table", nft(`{"table":{"family":"inet","name":"other"}}`)},

		{"nft key case variant of family", nft(`{"flush":{"table":{"family":"ip","name":"nat","Family":"inet","Name":"chaosgw"}}}`)},
		{"nft key case variant of table", nft(`{"add":{"chain":{"family":"inet","table":"nat","Table":"chaosgw","name":"c"}}}`)},
		{"nft extra top-level key", `{"type":"nft_apply","ruleset":{"nftables":[{"add":{"table":{"family":"inet","name":"chaosgw"}}}],"Nftables":[]}}`},
		{"nft family not a string", nft(`{"add":{"table":{"family":1,"name":"chaosgw"}}}`)},
		{"tc class without classid", tc(`{"object":"class","action":"add","dev":"wan0","parent":"1:","args":["htb","rate","1mbit"]}`)},
		{"tc class with handle", tc(`{"object":"class","action":"add","dev":"wan0","parent":"1:","handle":"1:10","classid":"1:10","args":["htb"]}`)},
		{"tc classid on qdisc", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","classid":"1:10","args":["netem"]}`)},
		{"tc classid keyword in args", tc(`{"object":"class","action":"add","dev":"wan0","parent":"1:","classid":"1:10","args":["htb","classid","1:11"]}`)},
		{"tc path traversal token", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","args":["netem","distribution","a/../b"]}`)},
		{"rule fwmark with underscore", rule(`,"fwmark":"1_0"`)},

		{"links unknown action", `{"type":"links","entries":[{"action":"delete","name":"eth0"}]}`},
		{"links enslave without master", `{"type":"links","entries":[{"action":"enslave","name":"lan0"}]}`},
		{"links master on another action", `{"type":"links","entries":[{"action":"up","name":"lan0","master":"br0"}]}`},
		{"links own master", `{"type":"links","entries":[{"action":"enslave","name":"lan0","master":"lan0"}]}`},
		{"links address without cidr", `{"type":"links","entries":[{"action":"addr_replace","name":"br0"}]}`},
		{"links cidr on up", `{"type":"links","entries":[{"action":"up","name":"br0","cidr":"10.0.0.1/24"}]}`},
		{"links v6 address", `{"type":"links","entries":[{"action":"addr_replace","name":"br0","cidr":"2001:db8::1/64"}]}`},
		{"links v6 address not /64", `{"type":"links","entries":[{"action":"addr_replace","name":"br0","cidr":"fe80::1/80"}]}`},
		{"links bare address", `{"type":"links","entries":[{"action":"addr_replace","name":"br0","cidr":"10.0.0.1"}]}`},
		{"links bad name", `{"type":"links","entries":[{"action":"up","name":"br0 x"}]}`},
		{"links none", `{"type":"links","entries":[]}`},
		{"sysctl unknown name", `{"type":"sysctl","entries":[{"name":"rp_filter","dev":"br0","value":0}]}`},
		{"sysctl forward with dev", `{"type":"sysctl","entries":[{"name":"ip_forward","dev":"br0","value":1}]}`},
		{"sysctl accept_ra without dev", `{"type":"sysctl","entries":[{"name":"accept_ra","value":0}]}`},
		{"sysctl value too large", `{"type":"sysctl","entries":[{"name":"ip_forward","value":2}]}`},
		{"sysctl negative", `{"type":"sysctl","entries":[{"name":"accept_ra","dev":"br0","value":-1}]}`},
		{"sysctl path in dev", `{"type":"sysctl","entries":[{"name":"accept_ra","dev":"../../x","value":0}]}`},
		{"read sysctl without name", `{"type":"read","what":"sysctl"}`},
		{"read name on links", `{"type":"read","what":"links","name":"ip_forward"}`},

		// set elements
		{"element is a command", `{"type":"nft_add_elements","set":"s","elements":["1.2.3.4; flush ruleset"]}`},
		{"element is a hostname", `{"type":"nft_add_elements","set":"s","elements":["example.org"]}`},
		{"set name with newline", `{"type":"nft_add_elements","set":"s\nflush","elements":["1.2.3.4"]}`},
		{"no elements", `{"type":"nft_add_elements","set":"s","elements":[]}`},
		{"negative timeout", `{"type":"nft_add_elements","set":"s","elements":["1.2.3.4"],"timeout_seconds":-1}`},
		{"element with zone", `{"type":"nft_add_elements","set":"s","elements":["fe80::1%eth0"]}`},

		// map elements (plan §3.3, M7)
		{"map key is a command", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4; flush ruleset","value":"1"}]}`},
		{"map key part is a hostname", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4 . example.org","value":"1"}]}`},
		{"map key with too many parts", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4 . 5.6.7.8 . 6 . 443 . 1","value":"1"}]}`},
		{"map key with an empty part", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4 . ","value":"1"}]}`},
		{"map name with newline", `{"type":"nft_add_map_elements","map":"m\nflush","elements":[{"key":"1.2.3.4","value":"1"}]}`},
		{"map value is a command", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4","value":"1; flush ruleset"}]}`},
		// a verdict element may only jump to a per-fault chain of the compiler (mark_<id>, id 0-4095)
		{"map value is a rule chain", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4","value":"input"}]}`},
		{"map value is the generation chain", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4","value":"generation"}]}`},
		{"map value is a chain of a similar name", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4","value":"mark_x"}]}`},
		{"map value is a mark chain above the id range", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4","value":"mark_4096"}]}`},
		{"map value is a mark chain with a leading zero", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4","value":"mark_0007"}]}`},
		{"map value is a mark chain with a prefix", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"1.2.3.4","value":"xmark_7"}]}`},
		{"map key range is backwards", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"10.0.0.9-10.0.0.1","value":"mark_7"}]}`},
		{"map key port range is backwards", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"10.0.0.1 . 6 . 443-80","value":"mark_7"}]}`},
		{"map key port range beyond 65535", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"10.0.0.1 . 6 . 1-65536","value":"mark_7"}]}`},
		{"map key range with a hostname", `{"type":"nft_add_map_elements","map":"m","elements":[{"key":"10.0.0.1-example.org","value":"mark_7"}]}`},
		{"map no elements", `{"type":"nft_add_map_elements","map":"m","elements":[]}`},
		{"map del no keys", `{"type":"nft_del_map_elements","map":"m","keys":[]}`},
		{"map del key is a command", `{"type":"nft_del_map_elements","map":"m","keys":["1.2.3.4; flush ruleset"]}`},

		// routing scope
		{"table main", `{"type":"routing","routes":[{"action":"replace","family":4,"table":254,"dst":"default","via":"10.0.0.1"}]}`},
		{"table 99", `{"type":"routing","routes":[{"action":"replace","family":4,"table":99,"dst":"default","via":"10.0.0.1"}]}`},
		{"table 111", `{"type":"routing","routes":[{"action":"replace","family":4,"table":111,"dst":"default","via":"10.0.0.1"}]}`},
		{"table local", `{"type":"routing","rules":[{"action":"add","family":4,"priority":10,"table":255}]}`},
		{"table zero", `{"type":"routing","routes":[{"action":"replace","family":4,"table":0,"dst":"default","via":"10.0.0.1"}]}`},
		{"route action", `{"type":"routing","routes":[{"action":"flush","family":4,"table":100,"dst":"default","via":"10.0.0.1"}]}`},
		{"route family 5", `{"type":"routing","routes":[{"action":"replace","family":5,"table":100,"dst":"default","via":"10.0.0.1"}]}`},
		{"route v6 dst with family 4", `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"2001:db8::/32","dev":"wan0"}]}`},
		{"route via wrong family", `{"type":"routing","routes":[{"action":"replace","family":6,"table":100,"dst":"2001:db8::/32","via":"10.0.0.1"}]}`},
		{"route dst injection", `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8 table 254","dev":"wan0"}]}`},
		{"route without via or dev", `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8"}]}`},
		{"blackhole with via", `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8","type":"blackhole","via":"10.0.0.1"}]}`},
		{"route type local", route(`,"type":"local"`)},
		{"route negative metric", route(`,"metric":-1`)},
		{"route unknown field proto", route(`,"proto":"kernel"`)},
		{"rule priority 0", `{"type":"routing","rules":[{"action":"add","family":4,"priority":0,"table":100}]}`},
		{"rule priority 32766", `{"type":"routing","rules":[{"action":"add","family":4,"priority":32766,"table":100}]}`},
		{"rule priority 32767", `{"type":"routing","rules":[{"action":"add","family":4,"priority":32767,"table":100}]}`},
		{"rule from injection", rule(`,"from":"10.0.0.0/8 lookup main"`)},
		{"rule bad fwmark", rule(`,"fwmark":"0xzz"`)},
		{"rule fwmark too large", rule(`,"fwmark":"0x1ffffffff"`)},
		{"rule unknown field protocol", rule(`,"protocol":"kernel"`)},
		{"rule delete of main table", `{"type":"routing","rules":[{"action":"delete","family":4,"priority":32766,"table":254}]}`},
		{"empty routing", `{"type":"routing"}`},

		// tc scope
		{"tc unknown object", tc(`{"object":"action","action":"add","dev":"wan0"}`)},
		{"tc unknown action", tc(`{"object":"qdisc","action":"flush","dev":"wan0","parent":"root","args":["netem"]}`)},
		{"tc shell metacharacters", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","args":["netem","delay","50ms;id"]}`)},
		{"tc newline in argument", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","args":["netem\nqdisc del dev eth0 root"]}`)},
		{"tc option argument", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","args":["netem","-batch"]}`)},
		{"tc parent keyword in args", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","args":["netem","parent","1:"]}`)},
		{"tc handle keyword in args", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","args":["netem","handle","1:"]}`)},
		{"tc bpf", tc(`{"object":"filter","action":"add","dev":"wan0","parent":"1:","args":["bpf","obj","/etc/x.o"]}`)},
		{"tc exec", tc(`{"object":"filter","action":"add","dev":"wan0","parent":"1:","args":["u32","exec","x"]}`)},
		{"tc unknown qdisc kind", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","args":["evil"]}`)},
		{"tc qdisc without kind", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root"}`)},
		{"tc qdisc without parent", tc(`{"object":"qdisc","action":"add","dev":"wan0","args":["netem"]}`)},
		{"tc bad parent", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"1:10 x","args":["netem"]}`)},
		{"tc filter at root", tc(`{"object":"filter","action":"add","dev":"wan0","parent":"root","args":["u32"]}`)},
		{"tc ingress with args", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"ingress","args":["netem"]}`)},
		{"tc ingress as filter", tc(`{"object":"filter","action":"add","dev":"wan0","parent":"ingress"}`)},
		{"tc bad handle", tc(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","handle":"1:; ls","args":["netem"]}`)},
		{"tc too many args", tc(`{"object":"filter","action":"add","dev":"wan0","parent":"1:","args":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 65), ",") + `]}`)},
		{"tc no entries", tc(``)},

		// other operations
		{"docker_user remove all", `{"type":"docker_user","action":"flush","devs":["wan0"]}`},
		{"docker_user without devs", `{"type":"docker_user","action":"ensure","devs":[]}`},
		{"assign loopback", `{"type":"assign_interfaces","devs":["lo"]}`},
		{"assign docker0", `{"type":"assign_interfaces","devs":["wan0","docker0"]}`},
		{"assign docker network bridge", `{"type":"assign_interfaces","devs":["br-0123456789ab"]}`},
		{"assign docker veth", `{"type":"assign_interfaces","devs":["veth0123abc"]}`},
		{"assign os_owned not assigned", `{"type":"assign_interfaces","devs":["lan0"],"os_owned":["wan0"]}`},
		{"read unknown", `{"type":"read","what":"bridges"}`},
		{"read offloads without dev", `{"type":"read","what":"offloads"}`},
		{"read table of links", `{"type":"read","what":"links","table":"main"}`},
		{"route_get without dst", `{"type":"read","what":"route_get"}`},
		{"route_get with a prefix", `{"type":"read","what":"route_get","dst":"10.0.0.0/8"}`},
		{"route_get with an IPv6 destination", `{"type":"read","what":"route_get","dst":"2001:db8::1"}`},
		{"route_get with a keyword in dst", `{"type":"read","what":"route_get","dst":"1.2.3.4 oif lo"}`},
		{"route_get with a keyword in src", `{"type":"read","what":"route_get","dst":"1.2.3.4","src":"1.2.3.4 table 100"}`},
		{"route_get with a zone", `{"type":"read","what":"route_get","dst":"::ffff:1.2.3.4"}`},
		{"route_get with an ingress interface but no source", `{"type":"read","what":"route_get","dst":"1.2.3.4","dev":"br-a"}`},
		{"route_get with a table", `{"type":"read","what":"route_get","dst":"1.2.3.4","table":"100"}`},
		{"route_get with a bad interface", `{"type":"read","what":"route_get","dst":"1.2.3.4","src":"1.1.1.1","dev":"a b"}`},
		{"dst on a links read", `{"type":"read","what":"links","dst":"1.2.3.4"}`},
		{"src on a routes read", `{"type":"read","what":"routes","src":"1.2.3.4"}`},
		{"read table injection", `{"type":"read","what":"routes","table":"main dev lo"}`},
	} {
		op, err := Decode([]byte(c.in))
		if err == nil {
			t.Errorf("%s: accepted %T: %s", c.name, op, c.in)
			continue
		}
		if !errors.Is(err, ErrDecode) {
			t.Errorf("%s: error does not wrap ErrDecode: %v", c.name, err)
		}
	}
}

func TestDecodeRejectsOversizedInput(t *testing.T) {
	big := `{"type":"nft_add_elements","set":"s","elements":["` + strings.Repeat("a", maxOperationBytes) + `"]}`
	if _, err := Decode([]byte(big)); err == nil {
		t.Fatal("an oversized operation must be rejected")
	}
}

func TestDecodeAcceptsBareNftObjectsAndHandlesMetaVariants(t *testing.T) {
	// objects without a command are implicit adds
	in := `{"type":"nft_apply","ruleset":{"nftables":[{"table":{"family":"inet","name":"chaosgw"}},{"set":{"family":"inet","table":"chaosgw","name":"s","type":"ipv4_addr"}},{"element":{"family":"inet","table":"chaosgw","name":"s","elem":["1.2.3.4"]}},{"delete":{"table":{"family":"inet","name":"chaosgw"}}}]}}`
	if _, err := Decode([]byte(in)); err != nil {
		t.Fatal(err)
	}
}
