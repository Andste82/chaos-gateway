package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// TestMarkMasksKeepTheDirectionBit is the golden test plan §3.3 calls for: writing the fault id
// must keep the direction bit (mask 0xffff000f), never clear it the way the old, buggy mask
// (0xfffe000f, spike S15) did.
func TestMarkMasksKeepTheDirectionBit(t *testing.T) {
	if MarkKeepOnIDWrite != 0xffff000f {
		t.Errorf("MarkKeepOnIDWrite = %#x, want 0xffff000f", MarkKeepOnIDWrite)
	}
	if markKeepOnIDWriteBuggy != 0xfffe000f {
		t.Errorf("markKeepOnIDWriteBuggy = %#x, want 0xfffe000f", markKeepOnIDWriteBuggy)
	}
	if MarkKeepOnIDWrite == markKeepOnIDWriteBuggy {
		t.Fatal("the golden mask equals the buggy one: the direction bit would not survive")
	}
	// bit 16 (direction) must be 1 in MarkKeepOnIDWrite (kept) and 0 in the buggy mask (cleared).
	if MarkKeepOnIDWrite&markDirMaskBits == 0 {
		t.Error("MarkKeepOnIDWrite clears the direction bit")
	}
	if markKeepOnIDWriteBuggy&markDirMaskBits != 0 {
		t.Error("the buggy mask does not clear the direction bit: it would not reproduce S15")
	}
	if MarkKeepOnDirectionWrite != 0xfffeffff {
		t.Errorf("MarkKeepOnDirectionWrite = %#x, want 0xfffeffff", MarkKeepOnDirectionWrite)
	}
	// bits 4-15 (the id) must survive a direction write.
	if MarkKeepOnDirectionWrite&markIDMaskBits != markIDMaskBits {
		t.Error("MarkKeepOnDirectionWrite clears some of the id bits")
	}
}

// compileClassifyTarget compiles a minimal target with two test networks and WireGuard, so
// classifyNets has something to report, and the given test classification ids.
func compileClassifyTarget(t *testing.T, ids []int) *Target {
	t.Helper()
	cfg := loadConfig(t, "gateway.yaml")
	tg := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}, TestClassifyIDs: ids})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	return tg
}

// TestClassifyChainGuardsNonTestTraffic proves that only test, WireGuard and remote-network
// traffic is classified: the chain's first rule returns (mark untouched) for anything else.
func TestClassifyChainGuardsNonTestTraffic(t *testing.T) {
	tg := compileClassifyTarget(t, nil)
	c := findChain(tg, ClassifyChain)
	if c == nil {
		t.Fatal("no classify chain")
	}
	if c.Base == nil || c.Base.Hook != "prerouting" || c.Base.Type != "filter" {
		t.Fatalf("%+v", c.Base)
	}
	if len(c.Rules) == 0 {
		t.Fatal("no rules")
	}
	b, _ := json.Marshal(c.Rules[0].Expr)
	s := string(b)
	// the guard rule matches neither original address against the test-nets set, and returns.
	if !strings.Contains(s, tg.ClassifyNets) || !strings.Contains(s, `"!="`) || !strings.Contains(s, `"return"`) {
		t.Errorf("guard rule: %s", s)
	}
}

// TestClassifyChainWritesTheDirectionBitOnce pins the direction-write rules: two mutually
// exclusive rules, reply sets bit 16 (mark | 0x10000) and original clears it (mark & 0xfffeffff),
// each keeping every other mark bit. A single `mark | ct direction << 16` expression is refused by
// the kernel (EOPNOTSUPP on 6.8.0-142, found by the level 1b testbed job), so no shift of ct
// direction may appear anywhere in the chain.
func TestClassifyChainWritesTheDirectionBitOnce(t *testing.T) {
	tg := compileClassifyTarget(t, nil)
	c := findChain(tg, ClassifyChain)
	if len(c.Rules) < 3 {
		t.Fatalf("%+v", c.Rules)
	}
	reply, _ := json.Marshal(c.Rules[1].Expr)
	orig, _ := json.Marshal(c.Rules[2].Expr)
	wantReply := `[{"match":{"left":{"ct":{"key":"direction"}},"op":"==","right":"reply"}},{"mangle":{"key":{"meta":{"key":"mark"}},"value":{"|":[{"meta":{"key":"mark"}},65536]}}}]`
	wantOrig := `[{"match":{"left":{"ct":{"key":"direction"}},"op":"==","right":"original"}},{"mangle":{"key":{"meta":{"key":"mark"}},"value":{"\u0026":[{"meta":{"key":"mark"}},4294901759]}}}]`
	if string(reply) != wantReply {
		t.Errorf("reply rule:\n got %s\nwant %s", reply, wantReply)
	}
	if string(orig) != wantOrig {
		t.Errorf("original rule:\n got %s\nwant %s", orig, wantOrig)
	}
	if MarkKeepOnDirectionWrite != 0xfffeffff || markDirMaskBits != 0x10000 {
		t.Errorf("direction masks: %#x, %#x", MarkKeepOnDirectionWrite, markDirMaskBits)
	}
	for _, r := range c.Rules {
		if b, _ := json.Marshal(r.Expr); strings.Contains(string(b), `"<<"`) {
			t.Errorf("a shift in the classify chain: %s", b)
		}
	}
}

// TestClassifyLookupChainOrder proves the four device-granularity levels of plan §3.3 are present,
// most specific first, each a vmap lookup followed by return (first match wins, plan §3.3).
func TestClassifyLookupChainOrder(t *testing.T) {
	tg := compileClassifyTarget(t, nil)
	c := findChain(tg, ClassifyChain)
	// rules[0] is the guard, [1] and [2] the direction write, [3..6] the four lookup levels.
	if len(c.Rules) != 7 {
		t.Fatalf("%d rules: %+v", len(c.Rules), c.Rules)
	}
	wantMaps := []string{tg.ClassifyMaps["devdestport"], tg.ClassifyMaps["devdest"], tg.ClassifyMaps["devport"], tg.ClassifyMaps["dev"]}
	for i, name := range wantMaps {
		if name == "" {
			t.Fatalf("level %d has no map", i)
		}
		b, _ := json.Marshal(c.Rules[3+i].Expr)
		s := string(b)
		if !strings.Contains(s, `"vmap"`) || !strings.Contains(s, "@"+name) || !strings.Contains(s, `"return"`) {
			t.Errorf("level %d (%s): %s", i, name, s)
		}
	}
	// the four maps have distinct key arities (4, 2, 3, 1 fields) and all jump to a chain (verdict).
	wantArity := []int{4, 2, 3, 1}
	for i, field := range []string{"devdestport", "devdest", "devport", "dev"} {
		m := findMap(tg, tg.ClassifyMaps[field])
		if m == nil {
			t.Fatalf("map %s missing", field)
		}
		if len(m.KeyType) != wantArity[i] || m.ValueType != "verdict" {
			t.Errorf("%s: %+v", field, m)
		}
	}
}

// TestClassifyTestIDsGetAMarkChain proves TestClassifyIDs each get their own mark-writing chain,
// with the golden id mask, deduplicated and bounded to the 4095-id capacity (plan §3.3).
func TestClassifyTestIDsGetAMarkChain(t *testing.T) {
	tg := compileClassifyTarget(t, []int{7, 7, 42, -1, MarkIDMax + 1})
	for _, id := range []int{7, 42} {
		c := findChain(tg, MarkChainName(id))
		if c == nil {
			t.Fatalf("no chain for id %d", id)
		}
		if len(c.Rules) != 1 {
			t.Fatalf("%+v", c.Rules)
		}
		b, _ := json.Marshal(c.Rules[0].Expr)
		s := string(b)
		if !strings.Contains(s, "4294901775") { // 0xffff000f, MarkKeepOnIDWrite
			t.Errorf("id %d: %s", id, s)
		}
	}
	if findChain(tg, MarkChainName(-1)) != nil || findChain(tg, MarkChainName(MarkIDMax+1)) != nil {
		t.Error("an out-of-range id got a chain")
	}
	// deduplicated: exactly one chain for id 7, not two.
	n := 0
	for _, c := range tg.Nft.Chains {
		if c.Name == MarkChainName(7) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d chains for id 7", n)
	}
}

// TestClassifyNetsCoversTestAndWireGuardAndRemoteNetworks proves the guard set holds the test
// networks, the WireGuard networks and the remote networks reached through them, and nothing of
// the gateway's own (uplink, management).
func TestClassifyNetsCoversTestAndWireGuardAndRemoteNetworks(t *testing.T) {
	cfg := loadConfig(t, "gateway.yaml")
	net := (*cfg.Networks)[iotNet]
	lan, _ := net.AsLanNetwork()
	remote := "198.51.100.0/24"
	lan.Routes = &[]model.DownstreamRoute{{Destination: remote, Via: "10.10.0.254"}}
	_ = net.FromLanNetwork(lan)
	(*cfg.Networks)[iotNet] = net
	tg := Compile(Input{Config: cfg, Host: testbedHost(), Generation: Generation{Revision: 1, Seq: 1}})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	s := findSet(tg, tg.ClassifyNets)
	if s == nil {
		t.Fatal("no classify_nets set")
	}
	has := map[string]bool{}
	for _, e := range s.Elements {
		has[e] = true
	}
	var lanAddr string
	for _, b := range tg.Bridges {
		if b.NetworkID == iotNet {
			lanAddr = b.Address.Masked().String()
		}
	}
	if lanAddr == "" || !has[lanAddr] {
		t.Errorf("the test network's own prefix is missing: %v (want %s)", s.Elements, lanAddr)
	}
	if !has[remote] {
		t.Errorf("the remote network behind the route is missing: %v", s.Elements)
	}
	if tg.Uplink.Name != "" && has[tg.Uplink.Addr.Masked().String()] {
		t.Errorf("the uplink is in classify_nets: %v", s.Elements)
	}
}

// TestClassifyRunsBeforeServiceRedirectAndNeverTouchesItsMark proves M7's classification mechanism
// does not interact with the M6b-04 "fail closed via mark" path it must not disturb: the classify
// chain's prerouting hook runs before the service redirect's own prerouting chain (so "right after
// classification" from plan §3.3 is satisfiable later), and writing a test classification id, with
// the service-selection bit already set beforehand, leaves that bit untouched (ServiceMark, bit 20)
// - MarkKeepOnIDWrite and MarkKeepOnDirectionWrite only ever clear bits 4-16. See
// docs/open-items.md P2-M7-01: M7 itself never sets bit 20 for real traffic either.
func TestClassifyRunsBeforeServiceRedirectAndNeverTouchesItsMark(t *testing.T) {
	tg := compileWG(t, func(_ *model.Configuration, in *Input) {
		in.ServiceNS = "cgsvc"
		in.TestClassifyIDs = []int{7}
	})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	cc := findChain(tg, ClassifyChain)
	sc := findChain(tg, "prerouting") // the service redirect's own chain (service.go)
	if cc == nil || sc == nil {
		t.Fatalf("classify=%v service=%v", cc, sc)
	}
	if cc.Base == nil || sc.Base == nil || cc.Base.Prio >= sc.Base.Prio {
		t.Errorf("classify chain must run before the service redirect: classify prio %+v, service prio %+v", cc.Base, sc.Base)
	}
	const serviceBit = uint32(1) << 20
	if MarkKeepOnIDWrite&serviceBit == 0 {
		t.Error("MarkKeepOnIDWrite clears the service-selection bit (bit 20): classification would undo a service selection")
	}
	if MarkKeepOnDirectionWrite&serviceBit == 0 {
		t.Error("MarkKeepOnDirectionWrite clears the service-selection bit (bit 20)")
	}
	// the per-id mark chain itself only ORs in the id bits; it cannot clear bit 20 either.
	mc := findChain(tg, MarkChainName(7))
	if mc == nil || len(mc.Rules) != 1 {
		t.Fatalf("%+v", mc)
	}
	b, _ := json.Marshal(mc.Rules[0].Expr)
	if s := string(b); !strings.Contains(s, "4294901775") { // 0xffff000f: keeps bit 20
		t.Errorf("mark_7 chain: %s", s)
	}
}

func findChain(tg *Target, name string) *Chain {
	for i := range tg.Nft.Chains {
		if tg.Nft.Chains[i].Name == name {
			return &tg.Nft.Chains[i]
		}
	}
	return nil
}

func findMap(tg *Target, name string) *MapDef {
	for i := range tg.Nft.Maps {
		if tg.Nft.Maps[i].Name == name {
			return &tg.Nft.Maps[i]
		}
	}
	return nil
}

func findSet(tg *Target, name string) *SetDef {
	for i := range tg.Nft.Sets {
		if tg.Nft.Sets[i].Name == name {
			return &tg.Nft.Sets[i]
		}
	}
	return nil
}
