package linux

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseLinks(t *testing.T) {
	links, err := ParseLinks(fixture(t, "ip_link.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || links[0].Name != "lo" || links[0].Index != 1 || links[0].MTU != 65536 {
		t.Fatalf("links: %+v", links)
	}
	if !links[0].Up() || !links[1].HasFlag("LOWER_UP") || links[0].HasFlag("NOARP") {
		t.Errorf("flags: %v %v", links[0].Flags, links[1].Flags)
	}
	if links[1].MAC == "" || links[1].Type != "ether" {
		t.Errorf("eth0: %+v", links[1])
	}
}

func TestParseLinkKindAndMaster(t *testing.T) {
	links, err := ParseLinks([]byte(`[{"ifindex":5,"ifname":"lan0","flags":["UP"],"master":"br-lan0","link_type":"ether","linkinfo":{"info_slave_kind":"bridge"}},
	 {"ifindex":6,"ifname":"br-lan0","flags":[],"link_type":"ether","linkinfo":{"info_kind":"bridge"}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if links[0].Master != "br-lan0" || links[0].Kind() != "" || links[1].Kind() != "bridge" || links[1].Up() {
		t.Fatalf("%+v", links)
	}
}

func TestParseAddrs(t *testing.T) {
	a, err := ParseAddrs(fixture(t, "ip_addr.json"))
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Name != "lo" || a[0].Addrs[0].Local != "127.0.0.1" || a[0].Addrs[0].PrefixLen != 8 || a[0].Addrs[0].Family != "inet" {
		t.Fatalf("%+v", a[0])
	}
	if a[0].Addrs[1].Family != "inet6" {
		t.Errorf("second lo address: %+v", a[0].Addrs[1])
	}
}

func TestParseRoutes(t *testing.T) {
	r, err := ParseRoutes(fixture(t, "ip_route.json"))
	if err != nil {
		t.Fatal(err)
	}
	if r[0].Dst != "default" || r[0].Gateway != "172.17.0.1" || r[0].Dev != "eth0" || r[0].Table != "" {
		t.Fatalf("default route: %+v", r[0])
	}
	var local bool
	for _, x := range r {
		if x.Type == "local" && x.Table == "local" {
			local = true
		}
	}
	if !local {
		t.Error("no local route parsed")
	}
	if r6, err := ParseRoutes(fixture(t, "ip_route6.json")); err != nil || len(r6) != 0 {
		t.Errorf("empty IPv6 table: %v %v", r6, err)
	}
}

// `ip -j` prints the path MTU of a route among its metrics, without telling whether it is locked.
func TestParseRoutesReadsThePathMTUOfARoute(t *testing.T) {
	r, err := ParseRoutes([]byte(`[{"dst":"10.10.0.0/24","dev":"br-iot","protocol":"77","scope":"link","flags":[],"metrics":[{"mtu":1280}],"table":"103"},{"dst":"default","gateway":"203.0.113.10","dev":"wan0","flags":[]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if r[0].MTU() != 1280 || r[1].MTU() != 0 || r[0].Table != "103" {
		t.Errorf("%+v", r)
	}
}

func TestParseRules(t *testing.T) {
	r, err := ParseRules(fixture(t, "ip_rule.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r) != 3 || r[0].Priority != 0 || r[0].Table != "local" || r[2].Table != "default" {
		t.Fatalf("%+v", r)
	}
	custom, err := ParseRules([]byte(`[{"priority":1000,"src":"10.10.0.0","srclen":24,"fwmark":"0x10","fwmask":"0xff","iif":"lan0","table":"100","protocol":"201"}]`))
	if err != nil {
		t.Fatal(err)
	}
	c := custom[0]
	if c.Src != "10.10.0.0" || *c.SrcLen != 24 || c.Fwmark != "0x10" || c.Protocol != "201" || c.Table != "100" || c.Iif != "lan0" {
		t.Fatalf("%+v", c)
	}
}

func TestParseTC(t *testing.T) {
	q, err := ParseQdiscs(fixture(t, "tc_qdisc_netem.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 3 || q[0].Kind != "htb" || !q[0].Root || q[1].Kind != "netem" || q[1].Parent != "1:10" || q[2].Parent != "ffff:fff1" {
		t.Fatalf("%+v", q)
	}
	if len(q[1].Options) == 0 {
		t.Error("options are dropped")
	}
	c, err := ParseClasses(fixture(t, "tc_class_htb.json"))
	if err != nil || len(c) != 2 || c[1].Parent != "1:" || c[1].Leaf != "10:" || c[1].Rate != 1000000 || c[1].Dev != "wan0" || !c[0].Root {
		t.Fatalf("%+v %v", c, err)
	}
	f, err := ParseFilters(fixture(t, "tc_filter_fw.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 2 || f[0].Kind != "fw" || f[0].Pref != 49152 || f[1].Kind != "u32" {
		t.Fatalf("header entries must be dropped: %+v", f)
	}
}

func TestParseNft(t *testing.T) {
	rs, err := ParseNft(fixture(t, "nft_chaosgw.json"))
	if err != nil {
		t.Fatal(err)
	}
	if rs.JSONSchemaVersion != 1 {
		t.Errorf("schema version %d", rs.JSONSchemaVersion)
	}
	tabs := rs.Tables()
	if len(tabs) != 1 || tabs[0].Family != "inet" || tabs[0].Name != "chaosgw" {
		t.Fatalf("tables: %+v", tabs)
	}
	if s := rs.Set("dns_blocked"); s == nil || len(s.Elem) != 2 || s.Flags[0] != "timeout" {
		t.Fatalf("set: %+v", s)
	}
	if rules := rs.Rules("forward"); len(rules) != 1 || len(rules[0].Expr) != 3 {
		t.Fatalf("rules: %+v", rules)
	}
	for _, o := range rs.Objects {
		if o.Chain != nil && (o.Chain.Hook != "forward" || *o.Chain.Prio != -150 || o.Chain.Policy != "accept") {
			t.Errorf("chain: %+v", o.Chain)
		}
	}
}

func TestParseEmptyAndBroken(t *testing.T) {
	if l, err := ParseLinks(nil); err != nil || l != nil {
		t.Errorf("empty links: %v %v", l, err)
	}
	if rs, err := ParseNft([]byte("  \n")); err != nil || len(rs.Objects) != 0 {
		t.Errorf("empty nft: %v %v", rs, err)
	}
	if _, err := ParseLinks([]byte(`{"not":"a list"}`)); err == nil {
		t.Error("an object instead of a list must fail")
	}
	if _, err := ParseNft([]byte(`{"nftables":[{"table":`)); err == nil {
		t.Error("truncated nft output must fail")
	}
	if _, err := ParseQdiscs([]byte(`[1]`)); err == nil {
		t.Error("wrong element type must fail")
	}
}

func TestEthtoolFeatures(t *testing.T) {
	f := ParseEthtoolFeatures(string(fixture(t, "ethtool_k.txt")))
	if !f["rx-checksumming"].On || f["tcp-segmentation-offload"].On {
		t.Fatalf("%+v", f)
	}
	if lro := f["large-receive-offload"]; lro.On || !lro.Fixed {
		t.Errorf("lro: %+v", lro)
	}
	if got := f.OffloadsStillOn(); len(got) != 0 {
		t.Errorf("still on: %v", got)
	}
	f["generic-receive-offload"] = Feature{On: true}
	if got := f.OffloadsStillOn(); len(got) != 1 || got[0] != "generic-receive-offload" {
		t.Errorf("still on: %v", got)
	}
}

func TestParseDockerUser(t *testing.T) {
	out := "-N DOCKER-USER\n-A DOCKER-USER -i br-lan0 -m comment --comment chaosgw -j ACCEPT\n-A DOCKER-USER -o br-lan0 -m comment --comment chaosgw -j ACCEPT\n-A DOCKER-USER -i docker0 -j ACCEPT\n-A DOCKER-USER -j RETURN\n"
	st := ParseDockerUser(out)
	if !st.ChainExists || len(st.In) != 1 || st.In[0] != "br-lan0" || len(st.Out) != 1 || !st.OursFirst {
		t.Fatalf("%+v", st)
	}
	// a foreign rule that does not end the traversal does not matter
	soft := ParseDockerUser("-N DOCKER-USER\n-A DOCKER-USER -j LOG\n-A DOCKER-USER -i br-lan0 -m comment --comment chaosgw -j ACCEPT\n-A DOCKER-USER -j RETURN\n")
	if !soft.OursFirst {
		t.Errorf("a LOG rule in front of ours: %+v", soft)
	}
	late := ParseDockerUser("-N DOCKER-USER\n-A DOCKER-USER -j RETURN\n-A DOCKER-USER -i br-lan0 -m comment --comment \"chaosgw\" -j ACCEPT\n")
	if late.OursFirst || len(late.In) != 1 {
		t.Fatalf("a rule behind Docker's RETURN is ineffective: %+v", late)
	}
}

func TestParseWGDump(t *testing.T) {
	dump := "PRIVATEKEYPRIVATEKEYPRIVATEKEYPRIVATEKEYAA=\tFHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=\t51820\toff\n" +
		"peerAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\tPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSK=\t203.0.113.30:51820\t10.99.0.2/32,10.50.0.0/24\t1760000000\t1234\t5678\t25\n" +
		"peerBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\t(none)\t(none)\t(none)\t0\t0\t0\toff\n"
	info, err := ParseWGDump(dump)
	if err != nil {
		t.Fatal(err)
	}
	if info.PublicKey != "FHQNDwQocDIBvHWRCNqB4itfFryYORwJaqSuvgYzoUo=" || info.ListenPort != 51820 || len(info.Peers) != 2 {
		t.Fatalf("%+v", info)
	}
	a, b := info.Peers[0], info.Peers[1]
	if !a.HasPresharedKey || a.Endpoint != "203.0.113.30:51820" || len(a.AllowedIPs) != 2 || a.LatestHandshake != 1760000000 || a.RxBytes != 1234 || a.TxBytes != 5678 || a.Keepalive != 25 {
		t.Errorf("%+v", a)
	}
	if b.HasPresharedKey || b.Endpoint != "" || len(b.AllowedIPs) != 0 || b.LatestHandshake != 0 || b.Keepalive != 0 {
		t.Errorf("%+v", b)
	}
	// neither secret is in what the parser keeps
	if s := fmt.Sprintf("%+v", info); strings.Contains(s, "PRIVATEKEY") || strings.Contains(s, "PSKPSK") {
		t.Errorf("a secret survived the parser: %s", s)
	}
	for _, bad := range []string{"", "x\ty", "a\tb\tnotaport\toff", "a\tb\t1\toff\nshort"} {
		if _, err := ParseWGDump(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
