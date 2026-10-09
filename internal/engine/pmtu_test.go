package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The MTU family in the engine (M10, plan §2.5) on the simulated kernel: the mirror table is in the kernel when the write
// returns and gone with the last icmp fault, the eighth size is refused with capacity_exceeded, explain
// names the table, the preview refuses a revision that does not fit. The real kernel's side is the testbed tests
// (integration_pmtu_test.go).

func mtuBody(target string, size int, mode string, extra string) string {
	return fmt.Sprintf("target: {%s}\nfault: {family: mtu, mtu: {size: %d, mode: %s}%s}", target, size, mode, extra)
}

func TestAnIcmpMTUOverlayHasItsMirrorTableInTheKernelWhenTheWriteReturnsAndLosesItWithTheLastOne(t *testing.T) {
	h := startedWithRevision(t)
	if n := len(h.k.RouteMTUs("103")); n != 0 {
		t.Fatalf("%d mirror routes before anything", n)
	}
	res := h.mustPut(alice, mtuBody("network: IoT", 1280, "icmp", ""))
	s := h.e.Snapshot()
	if len(s.PMTU) != 1 || s.PMTU[0].Source != res.Overlay.Id.String() || s.PMTU[0].Index != 1 || s.PMTUTables[1280] != 1 {
		t.Fatalf("%+v %v", s.PMTU, s.PMTUTables)
	}
	mirror := h.k.RouteMTUs("103")
	if len(mirror) == 0 {
		t.Fatal("no mirror routes when the write returns")
	}
	for _, m := range mirror {
		if m != 1280 {
			t.Errorf("a mirror route with MTU %d", m)
		}
	}
	// the counters are in the kernel, the chain too, and the kernel is what the compile says
	counters, err := h.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := counters[s.PMTU[0].CounterUp]; !ok {
		t.Errorf("no counter %s in %v", s.PMTU[0].CounterUp, counters)
	}
	if len(h.chainRules(s.PMTU[0].Chain)) == 0 {
		t.Error("no chain in the kernel")
	}
	h.verifyKernelWithOverlays()

	// a second size, and a size that goes: the first keeps its table
	two := h.mustPut(alice, mtuBody("network: Lab", 1400, "icmp", ""))
	if s := h.e.Snapshot(); s.PMTUTables[1280] != 1 || s.PMTUTables[1400] != 2 {
		t.Fatalf("%v", s.PMTUTables)
	}
	h.verifyKernelWithOverlays()
	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	if n := len(h.k.RouteMTUs("103")); n != 0 {
		t.Errorf("%d routes are left in the table of a size that went", n)
	}
	if s := h.e.Snapshot(); s.PMTUTables[1400] != 2 || len(s.PMTUTables) != 1 {
		t.Errorf("%v", s.PMTUTables)
	}
	h.verifyKernelWithOverlays()
	if _, err := h.e.DeleteOverlay(context.Background(), two.Overlay.Id, nil, admin); err != nil {
		t.Fatal(err)
	}
	if n := len(h.k.RouteMTUs("104")); n != 0 {
		t.Errorf("%d routes are left", n)
	}
	h.verifyKernelWithOverlays()
}

func TestTheEighthSizeInIcmpModeIsRefusedWithCapacityExceededAndNothingChanges(t *testing.T) {
	h := startedWithRevision(t)
	for i := 0; i < 7; i++ {
		h.mustPut(alice, mtuBody("network: IoT", 1500-20*i, "icmp", fmt.Sprintf(", protocol: tcp, ports: [%d]", 8000+i)))
	}
	before := h.e.Snapshot()
	var cerr *engine.CompileError
	_, err := h.put(bob, mtuBody("network: IoT", 1300, "icmp", ", protocol: tcp, ports: [9000]"))
	if !errors.As(err, &cerr) || len(cerr.Problems) == 0 || cerr.Problems[0].Code != compiler.CodeCapacityExceeded {
		t.Fatalf("got %v", err)
	}
	if p := cerr.Problems[0]; p.Scope != "network IoT" || !strings.Contains(p.Message, "8 different sizes") {
		t.Errorf("%+v", p)
	}
	// the same size as one of the seven, and the other modes, still fit
	h.mustPut(bob, mtuBody("network: Lab", 1480, "icmp", ""))
	h.mustPut(bob, mtuBody("network: Lab", 1300, "blackhole", ", protocol: udp, ports: [9]"))
	h.mustPut(bob, mtuBody("network: Lab", 1300, "mss_clamp", ", protocol: tcp, ports: [9001]"))
	after := h.barrier()
	if after.LastError != "" || len(after.PMTUTables) != 7 || len(after.PMTU) != 10 {
		t.Errorf("%q %v %d", after.LastError, after.PMTUTables, len(after.PMTU))
	}
	for s, i := range before.PMTUTables {
		if after.PMTUTables[s] != i {
			t.Errorf("size %d moved from table %d to %d", s, i, after.PMTUTables[s])
		}
	}
	h.verifyKernelWithOverlays()
}

func TestExplainNamesThePMTUTableOfAnIcmpWinnerAndNothingForTheOtherModes(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(withDHCPAndDevice))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	net := h.mustPut(alice, mtuBody("network: IoT", 1400, "icmp", ""))
	dev := h.mustPut(alice, mtuBody("device: esp32-42", 1280, "icmp", ", destination: {cidr: 203.0.113.0/24}"))
	h.mustPut(alice, mtuBody("device: esp32-42", 1200, "blackhole", ", protocol: udp, ports: [9]"))
	h.barrier()
	ctx := context.Background()
	explain := func(dst, proto string, port int) *engine.Explanation {
		ex, err := h.e.Explain(ctx, engine.ExplainQuery{Device: "esp32-42", Dst: dst, Protocol: proto, Port: port})
		if err != nil {
			t.Fatal(err)
		}
		return ex
	}
	mtuWinner := func(ex *engine.Explanation) *model.FaultRef {
		for _, f := range ex.Faults {
			if f.Family == "mtu" {
				return f.Winner
			}
		}
		return nil
	}
	s := h.e.Snapshot()
	// the device's destination wins for 203.0.113.0/24 (table of 1280), the network's size for the rest
	ex := explain("203.0.113.9", "tcp", 443)
	if w := mtuWinner(ex); w == nil || w.Id != dev.Overlay.Id || ex.Kernel == nil || ex.Kernel.PMTUTable != s.PMTUTables[1280] || ex.Kernel.PMTUTable == 0 {
		t.Errorf("%+v %+v", ex.Faults, ex.Kernel)
	}
	ex = explain("198.51.100.1", "tcp", 443)
	if w := mtuWinner(ex); w == nil || w.Id != net.Overlay.Id || ex.Kernel == nil || ex.Kernel.PMTUTable != s.PMTUTables[1400] {
		t.Errorf("%+v %+v", ex.Faults, ex.Kernel)
	}
	// the black hole is not a table
	ex = explain("198.51.100.1", "udp", 9)
	if ex.Kernel != nil && ex.Kernel.PMTUTable != 0 {
		t.Errorf("%+v", ex.Kernel)
	}
	b, _ := json.Marshal(ex.Kernel)
	if strings.Contains(string(b), "fault_id") {
		t.Errorf("an MTU winner has no fault id: %s", b)
	}
}

// The preview refuses a revision whose configured MTU faults need more than seven tables, and names the scope.
func TestThePreviewOfMoreThanSevenIcmpSizesIsCapacityExceededWithTheScope(t *testing.T) {
	h := startedWithRevision(t)
	rev := h.revision(func(c *model.Configuration) {
		faults := map[string]model.ConfigFault{}
		for i := 0; i < 8; i++ {
			var f model.ConfigFault
			body := fmt.Sprintf(`{"source": {"network": "IoT"}, "family": "mtu", "mtu": {"size": %d}, "protocol": "tcp", "ports": [%d]}`, 1500-10*i, 8000+i)
			if err := json.Unmarshal([]byte(body), &f); err != nil {
				t.Fatal(err)
			}
			faults[fmt.Sprintf("8a1b2c3d-1111-4222-8333-44445555660%d", i)] = f
		}
		c.Faults = &faults
	})
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	var cap *compiler.Problem
	for i := range p.Problems {
		if p.Problems[i].Code == compiler.CodeCapacityExceeded {
			cap = &p.Problems[i]
		}
	}
	if cap == nil || cap.Severity != compiler.SevError || cap.Scope != "network IoT" || !strings.Contains(cap.Message, "8 different sizes") || !strings.Contains(cap.Message, "7") {
		t.Fatalf("%+v", p.Problems)
	}
	if _, err := h.apply(rev); err == nil {
		t.Error("the revision was applied")
	}
	if len(h.k.RouteMTUs("103")) != 0 {
		t.Error("a refused revision reached the kernel")
	}
	// seven fit, and the preview shows the mirror tables
	rev7 := h.revision(func(c *model.Configuration) {
		faults := map[string]model.ConfigFault{}
		for i := 0; i < 7; i++ {
			var f model.ConfigFault
			body := fmt.Sprintf(`{"source": {"network": "IoT"}, "family": "mtu", "mtu": {"size": %d}, "protocol": "tcp", "ports": [%d]}`, 1500-10*i, 8000+i)
			if err := json.Unmarshal([]byte(body), &f); err != nil {
				t.Fatal(err)
			}
			faults[fmt.Sprintf("8a1b2c3d-1111-4222-8333-44445555660%d", i)] = f
		}
		c.Faults = &faults
	})
	p, err = h.e.Preview(context.Background(), rev7)
	if err != nil || len(p.Problems) != 0 {
		t.Fatalf("%v %+v", err, p.Problems)
	}
	if !strings.Contains(p.Linux.Routes, "route table 109 ") || !strings.Contains(p.Linux.Routes, "mtu lock 1440") || !strings.Contains(p.Linux.Routes, "fwmark 0xc0000/0xe0000") {
		t.Errorf("the preview does not show the tables:\n%s", p.Linux.Routes)
	}
}
