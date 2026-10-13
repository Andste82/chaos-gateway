package api_test

import (
	"strings"
	"testing"
)

// M14 (plan §2.17 "Technical view"): GET /state/linux shows what the kernel holds for the applied
// target — the nftables table in nft syntax with its counters, the tc trees with their queues' counters,
// bridges and routes, the WireGuard interfaces — and never a key.
func TestTheLinuxStateShowsTheKernelWithItsCounters(t *testing.T) {
	g := ready(t)
	// a fault on the network IoT puts a netem queue on its bridge
	if r := g.do("POST", "/overlays", map[string]any{"target": map[string]any{"network": "IoT"}, "fault": map[string]any{"latency": "50ms"}}, nil, nil); r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	g.mintToken("read")
	st := g.do("GET", "/state/linux", nil, nil, nil)
	if st.Status != 200 {
		t.Fatalf("%d %s", st.Status, st.Body)
	}
	v := st.json(t)
	nft, tc, routes, wg := v["nftables"].(string), v["tc"].(string), v["routes"].(string), v["wireguard"].(string)
	t.Logf("nftables:\n%s\ntc:\n%s\nroutes:\n%s\nwireguard:\n%s", nft, tc, routes, wg)
	for _, want := range []string{
		"table inet chaosgw {",
		"counter forward_drop { packets 0 bytes 0 }",
		// the access matrix of the configuration: IoT may reach the hub lab-hub
		`iifname "br-iot" oifname "wg-lab-hub" accept`,
		"chain forward {\n    type filter hook forward priority 0;",
	} {
		if !strings.Contains(nft, want) {
			t.Errorf("no %q in the nftables part", want)
		}
	}
	if strings.Contains(nft, "chain generation") {
		t.Error("the generation chain is shown")
	}
	if !strings.Contains(tc, "br-iot: qdisc") || !strings.Contains(tc, "netem") || !strings.Contains(tc, "sent ") {
		t.Errorf("the tc part has no netem queue with its counters on br-iot")
	}
	if !strings.Contains(routes, "bridge br-iot up") || !strings.Contains(routes, "address 10.10.0.1/24") || !strings.Contains(routes, "route table") {
		t.Errorf("the routes part has no bridge with its address and no route")
	}
	if !strings.Contains(wg, "wireguard wg-lab-hub up public key ") || !strings.Contains(wg, "  peer ") {
		t.Errorf("the wireguard part has no hub with its peers")
	}
	if v["revision"].(float64) < 1 || v["generation"].(float64) < 1 || v["read_at"] == nil {
		t.Errorf("revision, generation, read_at: %v %v %v", v["revision"], v["generation"], v["read_at"])
	}
	// no private or preshared key of any interface or peer
	ids, err := g.sec.IDs()
	if err != nil || len(ids) == 0 {
		t.Fatalf("no keys in the secrets store: %v", err)
	}
	for _, id := range ids {
		k, err := g.sec.WireGuard(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{k.PrivateKey, k.PresharedKey} {
			if s != "" && strings.Contains(string(st.Body), s) {
				t.Fatalf("a secret key of %s is in the Linux state", id)
			}
		}
	}
}
