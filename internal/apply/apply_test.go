package apply_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/apply/kernelsim"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

type env struct {
	t   *testing.T
	k   *kernelsim.Kernel
	ex  *executor.Executor
	cfg *model.Configuration
	seq uint64
}

func newTestbedKernel() *kernelsim.Kernel {
	k := kernelsim.New()
	k.AddLink("lan0", "02:00:00:00:00:01", "veth", true)
	k.AddLink("lan1", "02:00:00:00:01:01", "veth", true)
	k.AddLink("wan0", "02:00:00:00:02:01", "veth", true)
	k.SetAddr("wan0", "203.0.113.1/24")
	k.AddLink("mgmt0", "02:00:00:00:03:01", "veth", true)
	k.SetAddr("mgmt0", "192.168.56.1/24")
	k.SetMainDefault("192.168.56.254", "mgmt0")
	k.AddDockerChain()
	return k
}

func newEnv(t *testing.T) *env {
	t.Helper()
	k := newTestbedKernel()
	ex, err := executor.New(k, executor.WithNetnsInode(k.NetnsInode))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	raw, err := os.ReadFile("../compiler/testdata/gateway.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, k: k, ex: ex, cfg: cfg}
}

func (e *env) exec() apply.Exec { return apply.Local{E: e.ex} }

func (e *env) compile(mod ...func(*model.Configuration, *compiler.Input)) *compiler.Target {
	e.t.Helper()
	h, err := apply.ReadHost(context.Background(), e.exec(), "")
	if err != nil {
		e.t.Fatal(err)
	}
	e.seq++
	in := compiler.Input{Config: e.cfg, Host: h, Generation: compiler.Generation{Revision: 1, Seq: e.seq}}
	for _, m := range mod {
		m(e.cfg, &in)
	}
	return compiler.Compile(in)
}

func (e *env) apply(tg *compiler.Target) *apply.Result {
	e.t.Helper()
	res, err := apply.Apply(context.Background(), e.exec(), "", tg)
	if err != nil {
		e.t.Fatalf("apply: %v", err)
	}
	return res
}

func (e *env) mutating() []string {
	var out []string
	for _, c := range e.k.Commands() {
		f := strings.Fields(c)
		switch f[0] {
		case "ip":
			if len(f) > 1 && f[1] != "-j" && (len(f) <= 2 || f[1] != "link" || f[2] != "show") {
				out = append(out, c)
			}
		case "nft":
			if strings.Contains(c, "-f") {
				out = append(out, c)
			}
		case "sysctl":
			if strings.Contains(c, "-w") {
				out = append(out, c)
			}
		case "ethtool":
			if strings.Contains(c, "-K") {
				out = append(out, c)
			}
		case "iptables":
			if strings.Contains(c, " -I ") || strings.Contains(c, " -D ") {
				out = append(out, c)
			}
		}
	}
	return out
}

func TestFirstApplyBuildsTheRoutedGatewayAndVerifies(t *testing.T) {
	e := newEnv(t)
	tg := e.compile()
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	res := e.apply(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	s := res.After
	// bridges with ports and addresses
	if l := s.Links["br-iot"]; l.Kind() != "bridge" || !l.Up() {
		t.Errorf("br-iot: %+v", l)
	}
	if s.Links["lan0"].Master != "br-iot" || s.Links["lan1"].Master != "br-lab" {
		t.Errorf("ports: %+v %+v", s.Links["lan0"], s.Links["lan1"])
	}
	if a := s.Addrs["br-iot"]; len(a) != 1 || a[0].Local != "10.10.0.1" || a[0].PrefixLen != 24 {
		t.Errorf("addresses: %+v", a)
	}
	// sysctls, offloads, routes, rules
	if s.Sysctl["ip_forward"] != 1 || s.Sysctl["accept_ra:br-iot"] != 0 {
		t.Errorf("sysctl %v", s.Sysctl)
	}
	for _, d := range []string{"br-iot", "lan0", "wan0"} {
		if on := s.Offloads[d].OffloadsStillOn(); len(on) != 0 {
			t.Errorf("%s offloads %v", d, on)
		}
	}
	var own int
	for _, r := range s.Routes {
		if r.Protocol == "201" && r.Table == "100" {
			own++
		}
	}
	if own != 5 {
		t.Errorf("%d routes in table 100", own)
	}
	if strings.Join(s.Assigned, ",") != "br-iot,br-lab,lan0,lan1,wan0" {
		t.Errorf("assigned %v", s.Assigned)
	}
	// the management interface was not touched
	if len(s.Addrs["mgmt0"]) != 1 || s.Links["mgmt0"].Master != "" {
		t.Errorf("mgmt0 changed: %+v", s.Links["mgmt0"])
	}
	if _, touched := s.Sysctl["accept_ra:mgmt0"]; touched {
		t.Error("sysctl of the management interface")
	}
	// the generation is in the chain generation
	if g := s.Nft.Rules(compiler.GenerationChain); len(g) != 1 || g[0].Comment != "gen=1 rev=1" {
		t.Errorf("generation rule %+v", g)
	}
	if res.Outcome.Generation == 0 {
		t.Error("the executor's generation did not move")
	}
}

func TestReapplyChangesOnlyTheGeneration(t *testing.T) {
	e := newEnv(t)
	e.apply(e.compile())
	e.k.ClearLog()
	res := e.apply(e.compile())
	if !res.Plan.Empty() {
		t.Errorf("a second apply of the same target plans work: %v", res.Plan.Summary)
	}
	for _, c := range e.mutating() {
		if !strings.HasPrefix(c, "nft ") {
			t.Errorf("a re-apply ran %q", c)
		}
	}
	if g := res.After.Nft.Rules(compiler.GenerationChain); g[0].Comment != "gen=2 rev=1" {
		t.Errorf("generation %q", g[0].Comment)
	}
}

func TestPreviewChangesNothing(t *testing.T) {
	e := newEnv(t)
	before := e.ex.Generation()
	p, err := apply.Preview(context.Background(), e.exec(), "", e.compile())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.mutating()) != 0 || e.ex.Generation() != before {
		t.Fatalf("preview changed the kernel: %v", e.mutating())
	}
	text := strings.Join(p.Summary, "\n")
	for _, want := range []string{"create bridge br-iot", "attach lan0 to br-iot", "set 10.10.0.1/24 on br-iot", "route replace default table 100 via 203.0.113.10 dev wan0", "rule add priority 1000 iif br-iot table 100", "nftables: table inet chaosgw", "DOCKER-USER accept"} {
		if !strings.Contains(text, want) {
			t.Errorf("the preview lacks %q:\n%s", want, text)
		}
	}
	// after the apply the preview of the same target is empty
	e.apply(e.compile())
	p, _ = apply.Preview(context.Background(), e.exec(), "", e.compile())
	if !p.Empty() {
		t.Errorf("preview after apply: %v", p.Summary)
	}
}

func TestApplyOrder(t *testing.T) {
	e := newEnv(t)
	res := e.apply(e.compile())
	var kinds []string
	for _, op := range res.Plan.Ops {
		kinds = append(kinds, op.OpType())
	}
	want := "assign_interfaces links sysctl offloads links routing nft_apply docker_user"
	if strings.Join(kinds, " ") != want {
		t.Errorf("order %v, want %s", kinds, want)
	}
}

func TestDynamicSetsAndCountersSurviveEveryApply(t *testing.T) {
	e := newEnv(t)
	dyn := compiler.SetDef{Name: "dns_hosts_aa11", Type: "ipv4_addr", Dynamic: true}
	withDyn := func(_ *model.Configuration, in *compiler.Input) { in.DynamicSets = []compiler.SetDef{dyn} }
	e.apply(e.compile(withDyn))
	e.k.AddElement("dns_hosts_aa11", "192.0.2.77")
	e.k.BumpCounter("input_drop", 41)
	for i := 0; i < 5; i++ {
		e.apply(e.compile(withDyn))
	}
	if got := e.k.SetElements("dns_hosts_aa11"); len(got) != 1 || got[0] != `"192.0.2.77"` {
		t.Errorf("the dynamic set lost its elements: %v", got)
	}
	if v, _ := e.k.Counter("input_drop"); v != 41 {
		t.Errorf("counter %d: it must survive every apply and stay monotonic", v)
	}
	// when the target no longer has the set, it is deleted
	e.apply(e.compile())
	if len(e.k.SetElements("dns_hosts_aa11")) != 0 {
		t.Error("the removed set is still there")
	}
}

func TestAChangedSetDefinitionCreatesANewSetAndDeletesTheOldOne(t *testing.T) {
	e := newEnv(t)
	oldDef := compiler.SetDef{Name: "dns_a_111111", Type: "ipv4_addr", Dynamic: true}
	newDef := compiler.SetDef{Name: "dns_a_222222", Type: "ipv6_addr", Dynamic: true}
	e.apply(e.compile(func(_ *model.Configuration, in *compiler.Input) { in.DynamicSets = []compiler.SetDef{oldDef} }))
	e.apply(e.compile(func(_ *model.Configuration, in *compiler.Input) { in.DynamicSets = []compiler.SetDef{newDef} }))
	// the kernel would refuse `add set` of the old name with another type: the new name avoids that
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Nft.Set("dns_a_111111") != nil || s.Nft.Set("dns_a_222222") == nil {
		t.Errorf("sets %+v", s.Nft.Objects)
	}
}

func TestARemovedNetworkLeavesNothingBehind(t *testing.T) {
	e := newEnv(t)
	e.apply(e.compile())
	e.k.ForeignRule(5000, "docker0", "200")
	e.k.ForeignRoute("200", "172.30.0.0/16", "", "lan0")
	delete(*e.cfg.Networks, "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32") // Lab
	tg := e.compile()
	res := e.apply(tg)
	s := res.After
	if _, ok := s.Links["br-lab"]; ok {
		t.Error("the bridge of the removed network is still there")
	}
	if s.Links["lan1"].Master != "" {
		t.Errorf("the port is still attached: %+v", s.Links["lan1"])
	}
	if strings.Join(s.Assigned, ",") != "br-iot,lan0,wan0" {
		t.Errorf("assigned %v", s.Assigned)
	}
	for _, r := range s.Rules {
		if r.Iif == "br-lab" {
			t.Errorf("rule of the removed network: %+v", r)
		}
	}
	if du := s.DockerUser; contains(du.In, "br-lab") || contains(du.Out, "br-lab") {
		t.Errorf("DOCKER-USER still accepts br-lab: %+v", du)
	}
	// what was never ours stays
	var foreignRule, foreignRoute bool
	for _, r := range s.Rules {
		foreignRule = foreignRule || r.Priority == 5000
	}
	for _, r := range s.Routes {
		foreignRoute = foreignRoute || r.Dst == "172.30.0.0/16"
	}
	if !foreignRule || !foreignRoute {
		t.Errorf("foreign rule %v, foreign route %v: objects that are not ours are ignored (plan §2.14)", foreignRule, foreignRoute)
	}
	if v, ok := e.k.Counter("nat_1c8d7b4f"); ok {
		t.Errorf("counter of the removed network still there: %d", v)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestARemovedRulesChainIsGone(t *testing.T) {
	e := newEnv(t)
	extra := compiler.Chain{Name: "x_extra", Rules: []compiler.Rule{{Expr: []any{map[string]any{"counter": "x_cnt"}, map[string]any{"accept": nil}}, Comment: "aaaa"}}}
	tg := e.compile()
	tg.Nft.Chains = append(tg.Nft.Chains, extra)
	tg.Nft.Counters = append(tg.Nft.Counters, "x_cnt")
	e.apply(tg)
	if _, ok := e.k.Counter("x_cnt"); !ok {
		t.Fatal("the extra counter was not created")
	}
	e.apply(e.compile())
	s, _ := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if s.Nft.Chain("x_extra") != nil {
		t.Error("the chain of the removed rule is still there")
	}
	if _, ok := e.k.Counter("x_cnt"); ok {
		t.Error("the counter of the removed rule is still there")
	}
}

func TestAChangedUplinkAddressKeepsNATWorking(t *testing.T) {
	e := newEnv(t)
	e.apply(e.compile())
	before := e.compile()
	// the OS gives the uplink another address and another gateway
	e.k.DelAddr("wan0", "203.0.113.1/24")
	e.k.SetAddr("wan0", "198.51.100.5/24")
	e.cfg.Uplink.Gateway = nil
	e.k.SetMainDefault("198.51.100.1", "wan0")
	tg := e.compile()
	if tg.Uplink.Addr.String() != "198.51.100.5/24" || tg.Uplink.Gateway.String() != "198.51.100.1" {
		t.Fatalf("uplink %+v", tg.Uplink)
	}
	// masquerade follows the interface, not the address: the nftables rules did not change
	if js(before.Nft.Chains) != js(tg.Nft.Chains) {
		t.Error("an address change must not change the nftables rules")
	}
	res := e.apply(tg)
	text := strings.Join(res.Plan.Summary, "\n")
	for _, want := range []string{"route replace 198.51.100.0/24 table 100 dev wan0", "route replace default table 100 via 198.51.100.1 dev wan0", "route delete 203.0.113.0/24 table 100"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan lacks %q:\n%s", want, text)
		}
	}
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
}

func TestAMissingPortDegradesTheNetworkOnly(t *testing.T) {
	e := newEnv(t)
	e.k.RemoveLink("lan1")
	tg := e.compile()
	res := e.apply(tg)
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
	if res.After.Links["br-iot"].Kind() != "bridge" || res.After.Links["br-lab"].Kind() != "bridge" {
		t.Error("both bridges exist")
	}
	// the interface comes back (under another name): the next apply attaches it
	e.k.AddLink("enp4s0", "02:00:00:00:01:01", "veth", true)
	res = e.apply(e.compile())
	if res.After.Links["enp4s0"].Master != "br-lab" {
		t.Errorf("the returned interface is not attached: %+v", res.After.Links["enp4s0"])
	}
}

func TestVerifyDetectsManipulation(t *testing.T) {
	for name, c := range map[string]struct {
		manipulate func(*newEnvHandle)
		want       string
	}{
		"set element removed": {func(h *newEnvHandle) { h.k.RemoveSetElement(h.mgmtSet()) }, "set mgmt_src"},
		"rule removed":        {func(h *newEnvHandle) { h.k.DeleteNftRule("input") }, "chain input"},
		"set element added":   {func(h *newEnvHandle) { h.k.AddElement(h.mgmtSet(), "10.9.9.9") }, "set mgmt_src"},
		"address removed":     {func(h *newEnvHandle) { h.k.DelAddr("br-iot", "10.10.0.1/24") }, "bridge br-iot has addresses"},
		"address added":       {func(h *newEnvHandle) { h.k.SetAddr("br-iot", "10.77.0.1/24") }, "bridge br-iot has addresses"},
		"forwarding off":      {func(h *newEnvHandle) { h.runSysctl("net/ipv4/ip_forward=0") }, "ip_forward"},
		"offload on":          {func(h *newEnvHandle) { h.runEthtool("wan0") }, "offloads"},
		"port detached":       {func(h *newEnvHandle) { h.runIP("link", "set", "dev", "lan0", "nomaster") }, "bridge br-iot has ports"},
		"bridge down":         {func(h *newEnvHandle) { h.runIP("link", "set", "dev", "br-iot", "down") }, "bridge br-iot is down"},
		"docker rule gone":    {func(h *newEnvHandle) { h.runIptables("-D", "DOCKER-USER", "-i", "br-iot") }, "DOCKER-USER"},
		"own route added":     {func(h *newEnvHandle) { h.batch("route replace 10.77.0.0/16 table 100 proto 201 dev lan0") }, "unexpected route"},
		"own rule removed":    {func(h *newEnvHandle) { h.batch("rule del priority 1000 iif br-iot table 100 protocol 201") }, "missing rule"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHandle(t)
			h.apply(h.compile())
			c.manipulate(h)
			s, err := apply.ReadState(context.Background(), h.exec(), "", apply.Want{Sysctls: h.tg.Sysctls, Offloads: h.tg.Offloads})
			if err != nil {
				t.Fatal(err)
			}
			mm := apply.Verify(h.tg, s)
			var found bool
			for _, m := range mm {
				found = found || strings.Contains(m.String(), c.want)
			}
			if !found {
				t.Fatalf("verify did not report %q: %v", c.want, mm)
			}
		})
	}
}

func TestAGenerationThatDoesNotMatchIsReported(t *testing.T) {
	e := newEnv(t)
	tg := e.compile()
	e.apply(tg)
	newer := *tg
	newer.Nft.Generation = "gen=99 rev=1"
	s, _ := apply.ReadState(context.Background(), e.exec(), "", apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
	var found bool
	for _, m := range apply.Verify(&newer, s) {
		found = found || strings.Contains(m.Detail, "generation")
	}
	if !found {
		t.Error("a stale generation is not reported")
	}
}

func TestAnInjectedFailureIsAnErrorAndTheNftTransactionIsAtomic(t *testing.T) {
	e := newEnv(t)
	good := e.compile()
	e.apply(good)
	hashBefore := e.nftJSON()
	e.cfg.Uplink.Gateway = ptr("203.0.113.20")
	next := e.compile()
	e.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(strings.Join(argv, " "), "-f") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	res, err := apply.Apply(context.Background(), e.exec(), "", next)
	var ae *apply.Error
	if !asError(err, &ae) || ae.Stage != "execute" || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("got %v", err)
	}
	if res == nil || res.Plan == nil {
		t.Error("a failed apply still reports its plan")
	}
	if e.nftJSON() != hashBefore {
		t.Error("a failed transaction changed the table")
	}
	// the half-applied routes (executed before nft) are what the engine restores by re-applying
	e.k.Fail = nil
	res = e.apply(good)
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
}

func TestPlanRefusesToTouchAForeignDeviceWithABridgeName(t *testing.T) {
	e := newEnv(t)
	e.k.AddLink("br-iot", "02:aa:00:00:00:01", "veth", true) // not a bridge
	_, err := apply.Preview(context.Background(), e.exec(), "", e.compile())
	var ae *apply.Error
	if !asError(err, &ae) || ae.Stage != "plan" || !strings.Contains(err.Error(), "not a bridge") {
		t.Fatalf("got %v", err)
	}
}

func TestATargetWithErrorsIsNeverPlanned(t *testing.T) {
	e := newEnv(t)
	e.k.DelAddr("wan0", "203.0.113.1/24")
	tg := e.compile()
	if !tg.HasErrors() {
		t.Fatal("no error for the uplink without an address")
	}
	_, err := apply.Apply(context.Background(), e.exec(), "", tg)
	if err == nil {
		t.Fatal("applied a target with errors")
	}
	if len(e.mutating()) != 0 {
		t.Errorf("commands ran: %v", e.mutating())
	}
}

func TestNamespaceIsPassedThrough(t *testing.T) {
	e := newEnv(t)
	tg := e.compile()
	if _, err := apply.Apply(context.Background(), e.exec(), "gwns", tg); err != nil {
		t.Fatal(err)
	}
	// the simulator logs the commands without the namespace wrapper, so look at the plan instead
	p, _ := apply.Preview(context.Background(), e.exec(), "gwns", tg)
	for _, op := range p.Ops {
		if enc, _ := executor.Encode(op); !strings.Contains(string(enc), `"namespace":"gwns"`) {
			t.Errorf("%s has no namespace: %s", op.OpType(), enc)
		}
	}
}

func TestAPortThatMovesToAnotherNetworkEndsUpInThatBridge(t *testing.T) {
	// both directions: to a bridge that sorts earlier (br-iot) and to one that sorts later (br-lab)
	e := newEnv(t)
	e.apply(e.compile())
	swap := func(cfg *model.Configuration) {
		for id, ifs := range map[string][]model.InterfaceRef{
			"0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21": {{Name: ptr("lan1"), Mac: ptr("02:00:00:00:01:01")}},
			"1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32": {{Name: ptr("lan0"), Mac: ptr("02:00:00:00:00:01")}},
		} {
			n := (*cfg.Networks)[id]
			lan, _ := n.AsLanNetwork()
			lan.Interfaces = ifs
			_ = n.FromLanNetwork(lan)
			(*cfg.Networks)[id] = n
		}
	}
	swap(e.cfg)
	res := e.apply(e.compile())
	if res.After.Links["lan1"].Master != "br-iot" || res.After.Links["lan0"].Master != "br-lab" {
		t.Errorf("lan1 in %q, lan0 in %q", res.After.Links["lan1"].Master, res.After.Links["lan0"].Master)
	}
}

func TestHostRoutesAndHostSourcesVerifyAsTheKernelPrintsThem(t *testing.T) {
	e := newEnv(t)
	e.cfg.Management.AllowedSources = &[]string{"192.168.56.0/24", "10.0.0.5/32"}
	n := (*e.cfg.Networks)["0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"]
	lan, _ := n.AsLanNetwork()
	lan.Routes = &[]model.DownstreamRoute{{Destination: "10.30.0.5/32", Via: "10.10.0.2"}}
	_ = n.FromLanNetwork(lan)
	(*e.cfg.Networks)["0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"] = n
	res := e.apply(e.compile()) // verify inside apply would fail on a /32 mismatch
	if len(res.Mismatches) != 0 {
		t.Fatal(res.Mismatches)
	}
	// and a second apply recognizes both as already there
	res = e.apply(e.compile())
	if !res.Plan.Empty() {
		t.Errorf("a /32 route is planned again: %v", res.Plan.Summary)
	}
}

func TestNewRoutesAreAddedBeforeStaleOnesGo(t *testing.T) {
	e := newEnv(t)
	e.apply(e.compile())
	e.cfg.Uplink.Gateway = ptr("203.0.113.20")
	p, err := apply.Preview(context.Background(), e.exec(), "", e.compile())
	if err != nil {
		t.Fatal(err)
	}
	var routing []*executor.Routing
	for _, op := range p.Ops {
		if r, ok := op.(*executor.Routing); ok {
			routing = append(routing, r)
		}
	}
	if len(routing) != 1 {
		t.Fatalf("%d routing operations: the old default route is not on a device that goes, it must be deleted in the same operation after the new one is added", len(routing))
	}
	var replace, del bool
	for _, r := range routing[0].Routes {
		replace = replace || r.Action == "replace" && r.Via == "203.0.113.20"
		del = del || r.Action == "delete" && r.Via == "203.0.113.10"
	}
	if !replace || !del {
		t.Errorf("%+v", routing[0].Routes)
	}
}

func TestAnExistingBridgeThatIsNotOursIsNotTakenOver(t *testing.T) {
	e := newEnv(t)
	e.k.AddLink("br-iot", "02:aa:00:00:00:02", "bridge", true) // an operating system bridge
	_, err := apply.Preview(context.Background(), e.exec(), "", e.compile())
	var ae *apply.Error
	if !asError(err, &ae) || ae.Stage != "plan" || !strings.Contains(err.Error(), "does not belong to Chaos Gateway") {
		t.Fatalf("got %v", err)
	}
}

func TestDockerAcceptRulesBehindDockersReturnArePutInFront(t *testing.T) {
	e := newEnv(t)
	e.apply(e.compile())
	e.k.DockerOursLast()
	tg := e.compile()
	s, _ := apply.ReadState(context.Background(), e.exec(), "", apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
	var found bool
	for _, m := range apply.Verify(tg, s) {
		found = found || m.Subsystem == "docker"
	}
	if !found {
		t.Fatal("verify did not notice that the rules are ineffective")
	}
	res := e.apply(tg)
	if len(res.Mismatches) != 0 || !res.After.DockerUser.OursFirst {
		t.Fatalf("not repaired: %v %+v", res.Mismatches, res.After.DockerUser)
	}
}

func TestDockersInterfacesAreNotPartOfTheObservedHost(t *testing.T) {
	e := newEnv(t)
	e.k.AddLink("docker0", "02:42:00:00:00:01", "bridge", true)
	e.k.AddLink("veth0123abc", "02:42:00:00:00:02", "veth", true)
	e.k.AddLink("br-0123456789ab", "02:42:00:00:00:03", "bridge", true)
	h, err := apply.ReadHost(context.Background(), e.exec(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range h.Links {
		if strings.HasPrefix(l.Name, "docker") || strings.HasPrefix(l.Name, "veth") || strings.HasPrefix(l.Name, "br-") {
			t.Errorf("%s is part of the host: a Docker container starting would look like a change", l.Name)
		}
	}
}

func TestLinksComeUpAfterTheirSysctls(t *testing.T) {
	e := newEnv(t)
	res := e.apply(e.compile())
	sysctl, up := -1, -1
	for i, op := range res.Plan.Ops {
		if _, ok := op.(*executor.Sysctl); ok {
			sysctl = i
		}
		if l, ok := op.(*executor.Links); ok && len(l.Entries) > 0 && l.Entries[0].Action == "up" {
			up = i
		}
	}
	if sysctl < 0 || up < sysctl {
		t.Errorf("sysctl at %d, up at %d: a bridge must not be up before accept_ra is off", sysctl, up)
	}
}

// M4-05 test: renaming a network moves its ports to the new bridge name and removes the old one.
func TestRenamingANetworkMovesItsPortsToTheNewBridge(t *testing.T) {
	e := newEnv(t)
	e.apply(e.compile())

	const labID = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32" // "Lab", lan1, br-lab
	n := (*e.cfg.Networks)[labID]
	lan, err := n.AsLanNetwork()
	if err != nil {
		t.Fatal(err)
	}
	lan.Name = "Extra"
	if err := n.FromLanNetwork(lan); err != nil {
		t.Fatal(err)
	}
	(*e.cfg.Networks)[labID] = n

	tg := e.compile()
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	res := e.apply(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("not verified after the rename: %v", res.Mismatches)
	}
	s := res.After
	if l := s.Links["br-extra"]; l.Kind() != "bridge" || !l.Up() {
		t.Errorf("br-extra: %+v", l)
	}
	if s.Links["lan1"].Master != "br-extra" {
		t.Errorf("lan1 did not move to the renamed bridge: %+v", s.Links["lan1"])
	}
	if _, ok := s.Links["br-lab"]; ok {
		t.Error("br-lab still exists after the rename")
	}
}
