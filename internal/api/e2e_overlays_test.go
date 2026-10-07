//go:build testbed

package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M8a on a real kernel, over the API: an overlay that is created through HTTP is in the kernel's
// nftables (mark chain, named counters, map elements) and in the compiled tc tree, traffic of its
// scope is classified into it, the counters come back with the overlay, explain names it as the
// winner and gives the route the kernel takes, and a delete and a reset take everything away.
func TestAnOverlayCreatedOverTheAPIIsInTheKernelAndItsCountersComeBack(t *testing.T) {
	g, top := newBedGW(t)
	g.mintToken("overlays")
	nft := func() string { return top.GW.Must("nft", "list", "table", "inet", "chaosgw") }
	if strings.Contains(nft(), "chain mark_") {
		t.Fatalf("a fault before any overlay:\n%s", nft())
	}

	r := g.createOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"2ms","destination":{"cidr":"` + testbed.ServerAddr + `/32"}}}`)
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ov := r.json(t)
	id := ov["id"].(string)
	snap := g.e.Snapshot()
	if len(snap.Faults) != 1 {
		t.Fatalf("faults %+v", snap.Faults)
	}
	f := snap.Faults[0]
	// the answer came after the verify: the kernel has the fault
	k := nft()
	for _, want := range []string{"chain mark_", "counter " + f.CounterUp, "counter " + f.CounterDown, testbed.ServerAddr} {
		if !strings.Contains(k, want) {
			t.Errorf("the kernel has no %q:\n%s", want, k)
		}
	}

	// the compiled tc tree of the active state (the same compile the apply loop does), installed the
	// way the fault engine will (M8b)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pv, err := g.e.Preview(ctx, g.activeID())
	if err != nil {
		t.Fatal(err)
	}
	tg := pv.Target
	if tg == nil || tg.TC == nil || tg.HasErrors() {
		t.Fatalf("the active state has no tc tree: %+v", pv.Problems)
	}
	var up, down string
	for _, dev := range tg.TC.Devs {
		steps, err := executor.Plan(&executor.TC{Target: executor.Target{}, Entries: tg.TC.Entries(dev, true)})
		if err != nil {
			t.Fatal(err)
		}
		top.GW.MustStdin(steps[0].Cmd.Stdin, "tc", steps[0].Cmd.Args...)
	}
	for _, c := range tg.TC.Classes {
		if c.ID == f.ID && c.Dir == compiler.Upload {
			up = c.ClassID()
		}
		if c.ID == f.ID && c.Dir == compiler.Download {
			down = c.ClassID()
		}
	}
	if up == "" || down == "" {
		t.Fatalf("the tree has no class for both directions of fault %d: %+v", f.ID, tg.TC.Classes)
	}
	classPackets := func(handle string) int64 {
		var n int64
		for _, dev := range tg.TC.Devs {
			var entries []map[string]any
			if err := json.Unmarshal([]byte(top.GW.Must("tc", "-j", "-s", "class", "show", "dev", dev)), &entries); err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e["handle"] == handle {
					if p, ok := packetsOf(e); ok {
						n += p
					}
				}
			}
		}
		return n
	}

	if res := testbed.MustPing(t, top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 {
		t.Fatalf("A cannot reach the server through the fault:\n%s", k)
	}
	got := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)
	c, ok := got["counters"].(map[string]any)
	if !ok || c["packets"].(float64) < 6 || c["bytes"].(float64) <= 0 || got["state"] != "effective" {
		t.Errorf("three pings are at least six packets of the overlay, got %v", got)
	}
	if n := classPackets(up); n < 3 {
		t.Errorf("the upload class %s counted %d packets", up, n)
	}
	if n := classPackets(down); n < 3 {
		t.Errorf("the download class %s counted %d packets", down, n)
	}

	// traffic to a destination the fault does not name is not counted
	before := c["packets"].(float64)
	if res := testbed.MustPing(t, top.A, testbed.ServerAddr2, 2, 200*time.Millisecond); res.Loss() != 0 {
		t.Fatal("A cannot reach the second server")
	}
	if after := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)["counters"].(map[string]any)["packets"].(float64); after != before {
		t.Errorf("the counters moved for traffic the overlay does not name: %v -> %v", before, after)
	}

	// a replacement is the same fault: the kernel keeps its id, chain and counters
	rep := g.createOverlay(`{"target":{"network":"IoT"},"fault":{"latency":"4ms","destination":{"cidr":"` + testbed.ServerAddr + `/32"}}}`)
	if rep.Status != 200 || rep.json(t)["id"] != id {
		t.Fatalf("%d %s", rep.Status, rep.Body)
	}
	if c2 := g.do("GET", "/overlays/"+id, nil, nil, nil).json(t)["counters"].(map[string]any); c2["packets"].(float64) != before || c2["epoch"] != c["epoch"] {
		t.Errorf("a replacement restarted the counters: %v, was %v", c2, c)
	}

	// explain: the winner and the route of the real kernel
	ex := g.do("GET", "/explain?src="+testbed.ClientAAddr+"&dst="+testbed.ServerAddr+"&protocol=tcp&port=443", nil, nil, nil)
	if ex.Status != 200 {
		t.Fatalf("%d %s", ex.Status, ex.Body)
	}
	exj := ex.json(t)
	faults := exj["faults"].([]any)
	if len(faults) != 1 || faults[0].(map[string]any)["winner"].(map[string]any)["id"] != id {
		t.Errorf("the winner is not the overlay: %s", ex.Body)
	}
	if rt, ok := exj["route"].(map[string]any); !ok || rt["table"] != float64(100) || rt["interface"] != "wan0" {
		t.Errorf("route %v", exj["route"])
	}
	// a destination behind the uplink router goes through the configured gateway
	far := g.do("GET", "/explain?src="+testbed.ClientAAddr+"&dst="+testbed.InternetAddr, nil, nil, nil)
	if rt, ok := far.json(t)["route"].(map[string]any); far.Status != 200 || !ok || rt["table"] != float64(100) || rt["gateway"] != testbed.ServerAddr {
		t.Errorf("the route to a destination behind the gateway: %s", far.Body)
	}
	if k, ok := exj["kernel"].(map[string]any); !ok || k["fault_id"] != float64(f.ID) {
		t.Errorf("kernel %v", exj["kernel"])
	}

	// a delete answers after the verify, and the kernel has no fault left
	if r := g.do("DELETE", "/overlays/"+id, nil, nil, nil); r.Status != 204 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if k := nft(); strings.Contains(k, "chain mark_") || strings.Contains(k, "counter "+f.CounterUp) {
		t.Errorf("the kernel kept the fault of a deleted overlay:\n%s", k)
	}

	// a reset takes the caller's overlays out of the kernel too
	g.mustCreateOverlay(`{"target":{"network":"IoT"},"fault":{"loss":"1%"}}`)
	g.mustCreateOverlay(`{"target":{"global":true},"fault":{"latency":"9ms","destination":{"cidr":"198.51.100.0/24"}}}`)
	if strings.Count(nft(), "chain mark_") != 2 {
		t.Fatalf("two overlays:\n%s", nft())
	}
	if r := g.do("POST", "/reset", nil, nil, nil); r.Status != 200 || r.json(t)["removed_overlays"] != float64(2) {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if k := nft(); strings.Contains(k, "chain mark_") {
		t.Errorf("the kernel kept faults after a reset:\n%s", k)
	}
}

// packetsOf finds the packet count in the output of tc -j -s, which puts it in a stats object in
// some versions of iproute2 and at the top in others.
func packetsOf(e map[string]any) (int64, bool) {
	if v, ok := e["packets"].(float64); ok {
		return int64(v), true
	}
	for _, k := range []string{"stats", "stats2"} {
		if s, ok := e[k].(map[string]any); ok {
			if n, ok := packetsOf(s); ok {
				return n, true
			}
		}
	}
	return 0, false
}
