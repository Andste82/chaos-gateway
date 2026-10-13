package linux

import (
	"encoding/json"
	"strings"
	"testing"
)

// The technical view (plan §2.17) shows the table as nft prints it, with the counters of its rules.
func TestRenderNftPrintsTheTableInNftSyntaxWithItsCounters(t *testing.T) {
	rs, err := ParseNft(fixture(t, "nft_chaosgw.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `table inet chaosgw {
  set dns_blocked {
    type ipv4_addr
    flags timeout
    elements = { 192.0.2.1, 192.0.2.2 }
  }
  chain forward {
    type filter hook forward priority -150; policy accept;
    iifname "lan0" counter packets 10 bytes 840 drop
  }
}
`
	if got := RenderNft(rs, 0); got != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, want)
	}
	if got := RenderNft(rs, 0, "forward"); strings.Contains(got, "chain forward") {
		t.Errorf("a skipped chain is shown:\n%s", got)
	}
}

func TestRenderNftShowsNamedCountersMapsAndCutsLongSets(t *testing.T) {
	rs, err := ParseNft([]byte(`{"nftables":[
	  {"table":{"family":"inet","name":"chaosgw","handle":1}},
	  {"counter":{"family":"inet","table":"chaosgw","name":"forward_drop","handle":2,"packets":7,"bytes":420}},
	  {"set":{"family":"inet","table":"chaosgw","name":"ifs_lan","type":"ifname","handle":3,"elem":["br-iot","br-test"]}},
	  {"set":{"family":"inet","table":"chaosgw","name":"many","type":"ipv4_addr","handle":4,"elem":["10.0.0.1","10.0.0.2","10.0.0.3"]}},
	  {"map":{"family":"inet","table":"chaosgw","name":"ident","type":["ifname","ipv4_addr"],"map":"mark","handle":5,"flags":["interval"],
	    "elem":[[{"concat":["br-iot",{"prefix":{"addr":"10.10.0.0","len":24}}]},3]]}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := RenderNft(rs, 2)
	for _, want := range []string{
		"  counter forward_drop { packets 7 bytes 420 }\n",
		`    elements = { "br-iot", "br-test" }`,
		"    elements = { 10.0.0.1, 10.0.0.2, … 1 more }",
		"  map ident {\n    type ifname . ipv4_addr : mark\n    flags interval\n    elements = { br-iot . 10.10.0.0/24 : 3 }\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestRenderNftRuleKnowsTheExpressionsOfTheCompiler(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`[{"match":{"op":"in","left":{"ct":{"key":"state"}},"right":["established","related"]}},{"accept":null}]`, "ct state { established, related } accept"},
		{`[{"match":{"op":"==","left":{"ct":{"key":"ip saddr","dir":"original"}},"right":"@dev_ab"}},{"jump":{"target":"faults"}}]`, "ct original ip saddr @dev_ab jump faults"},
		{`[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":{"set":[80,{"range":[1000,2000]}]}}},{"reject":{"type":"tcp reset"}}]`, "tcp dport { 80, 1000-2000 } reject with tcp reset"},
		{`[{"match":{"op":"!=","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":{"prefix":{"addr":"10.0.0.0","len":8}}}},{"counter":"forward_drop"},{"drop":null}]`, `ip daddr != 10.0.0.0/8 counter name "forward_drop" drop`},
		{`[{"mangle":{"key":{"meta":{"key":"mark"}},"value":{"|":[{"meta":{"key":"mark"}},16]}}}]`, "meta mark set (meta mark | 16)"},
		{`[{"vmap":{"key":{"concat":[{"meta":{"key":"iifname"}},{"payload":{"protocol":"ip","field":"daddr"}}]},"data":"@cls_1"}}]`, "iifname . ip daddr vmap @cls_1"},
		{`[{"reject":{"type":"icmp","expr":"port-unreachable"}}]`, "reject with icmp port-unreachable"},
		{`[{"masquerade":null}]`, "masquerade"},
		{`[{"xt":{"type":"match","name":"foo"}}]`, `{"xt":{"name":"foo","type":"match"}}`},
	}
	for _, c := range cases {
		var expr []json.RawMessage
		if err := json.Unmarshal([]byte(c.expr), &expr); err != nil {
			t.Fatal(err)
		}
		if got := RenderNftRule(expr); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.expr, got, c.want)
		}
	}
}
