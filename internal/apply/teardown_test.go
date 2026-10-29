package apply_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The stop cleanup (M27, D35, variant A) on the simulated kernel: what the gateway made is gone and nothing else is touched.
// What the real kernel and the real tools do with the same operations is pinned by the testbed test of the executor
// (TestTheStopCleanupLeavesNothingOfTheGatewayOnTheRealKernel).

// teardownState reads the state as the teardown does.
func (e *env) teardownState() *apply.State {
	e.t.Helper()
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{BirdInstance: compiler.BirdInstance, ServiceNS: "cgsvc", ServicePeerIf: compiler.ServicePeerIf})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) teardown() *apply.Plan {
	e.t.Helper()
	p, err := apply.Teardown(context.Background(), e.exec(), "", "cgsvc")
	if err != nil {
		e.t.Fatalf("teardown: %v", err)
	}
	return p
}

func TestTeardownRemovesEverythingTheGatewayMadeAndTheProbesWithIt(t *testing.T) {
	e := newProbeEnv(t)
	e.k.ServiceNamespace("cgsvc")
	tg := e.compile(withProbes(probeA, probeB), withService)
	e.apply(tg)
	before := e.teardownState()
	// the applied state holds what the teardown has to remove
	if len(apply.Leftovers(before, before.Assigned)) < 8 {
		t.Fatalf("the applied state holds little of the gateway: %v", apply.Leftovers(before, before.Assigned))
	}
	for _, want := range []string{"the nftables table inet chaosgw", "the bridge br-iot", "the bridge br-lab", "the service interface svc0", "cgp-3a1f7c52", "cgp-9d2e4b61", "the name of the service namespace"} {
		if !strings.Contains(strings.Join(apply.Leftovers(before, before.Assigned), "\n"), want) {
			t.Errorf("the applied state does not hold %q:\n%s", want, strings.Join(apply.Leftovers(before, before.Assigned), "\n"))
		}
	}
	if e.sockets.Load() != 2 {
		t.Fatalf("the probes' DHCP clients: %d sockets opened, want 2", e.sockets.Load())
	}

	p := e.teardown()
	if len(p.Ops) == 0 {
		t.Fatal("no operations")
	}
	after := e.teardownState()
	if left := apply.Leftovers(after, before.Assigned); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
	// no bridge, no veth, no namespace: the probes' ends are removed by name, not with the bridge
	for name, l := range after.Links {
		if kind := l.Kind(); kind == "bridge" || kind == "veth" && (name == compiler.ServiceHostIf || strings.HasPrefix(name, executor.ProbeHostPrefix)) {
			t.Errorf("%s (%s) is still there", name, kind)
		}
	}
	if len(after.Probes) != 0 {
		t.Errorf("probes: %+v", after.Probes)
	}
	if len(after.Assigned) != 0 {
		t.Errorf("the scope is not empty: %v", after.Assigned)
	}
	// the DHCP clients of the probes stopped with their probes: each closed its socket (nothing called StopProbeClients)
	if n := e.closed.Load(); n != 2 {
		t.Errorf("%d of the 2 DHCP clients of the probes stopped with the teardown", n)
	}
	for _, ns := range []string{probeAns, "cgp-9d2e4b61"} {
		if _, ok := after.Probes[ns]; ok {
			t.Errorf("%s is still a probe", ns)
		}
	}
	// the host's own interfaces and their addresses stay
	for _, name := range []string{"wan0", "mgmt0"} {
		if _, ok := after.Links[name]; !ok {
			t.Errorf("%s is gone", name)
		}
	}
	if a := after.Addrs["mgmt0"]; len(a) != 1 || a[0].Local != "192.168.56.1" {
		t.Errorf("the management address: %+v", a)
	}
}

// The own routing tables (D35): the policy table 100, the table 101 of the replies of a management-side forward (it is there
// in the three-port topology of the test environment), the service table 102 and the PMTU mirror table 103 are all filled
// before the teardown and all empty after it, with their policy rules.
func TestTeardownEmptiesTheRoutingTablesOfTheOwnRange(t *testing.T) {
	x := newTCEnv(t)
	x.k.ServiceNamespace("cgsvc")
	id := domain.ResolveIdentity(x.cfg, domain.Observed{}, nil)
	tg := x.compile(withService, func(_ *model.Configuration, in *compiler.Input) {
		in.Overlays, in.Identity, in.FaultIDs, in.PMTUTables, in.ClassLimit = []model.Overlay{x.overlay(mtuIcmp1280)}, &id, x.ids, x.pmtu, 1000
	})
	if tg.HasErrors() {
		t.Fatalf("%+v", tg.Problems)
	}
	x.ids, x.pmtu = tg.FaultIDs, tg.PMTUTables
	x.applyRetiring(tg)

	tables := func(s *apply.State) (routes, rules map[int]int) {
		routes, rules = map[int]int{}, map[int]int{}
		for _, r := range s.Routes {
			if n, err := strconv.Atoi(r.Table); err == nil && n >= domain.OwnTableFirst && n <= domain.OwnTableLast {
				routes[n]++
			}
		}
		for _, r := range s.Rules {
			if n, err := strconv.Atoi(r.Table); err == nil && n >= domain.OwnTableFirst && n <= domain.OwnTableLast {
				rules[n]++
			}
		}
		return routes, rules
	}
	routes, rules := tables(x.teardownState())
	for _, table := range []int{compiler.PolicyTable, compiler.ForwardReplyTable, compiler.ServiceTable, compiler.PMTUTableFirst} {
		if routes[table] == 0 {
			t.Errorf("no route in table %d before the teardown: %v", table, routes)
		}
	}
	if len(rules) == 0 {
		t.Fatalf("no policy rule of the own tables before the teardown: %v", rules)
	}

	x.teardown()
	if routes, rules = tables(x.teardownState()); len(routes) != 0 || len(rules) != 0 {
		t.Errorf("routes %v and rules %v of the own tables are left", routes, rules)
	}
}

func TestTeardownDeletesTheProbesWhoseClientsWereStoppedBeforeIt(t *testing.T) {
	e := newProbeEnv(t)
	e.k.ServiceNamespace("cgsvc")
	e.apply(e.compile(withProbes(probeA), withService))
	e.ex.StopProbeClients()
	// a client that is stopped is not started again by the teardown, and the probe is still deleted
	e.teardown()
	if left := apply.Leftovers(e.teardownState(), []string{"br-iot", "br-lab"}); len(left) != 0 {
		t.Errorf("left: %v", left)
	}
}

func TestTeardownLeavesWhatTheGatewayDidNotMake(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	e.k.AddLink("docker0", "02:42:00:00:00:01", "bridge", true)
	e.k.ForeignRule(5000, "docker0", "200")
	e.apply(e.compile(withService))

	e.teardown()
	s := e.teardownState()
	if _, ok := s.Links["docker0"]; !ok {
		t.Error("the bridge of Docker was deleted")
	}
	foreignRule := false
	for _, r := range s.Rules {
		foreignRule = foreignRule || r.Priority == 5000
	}
	if !foreignRule {
		t.Errorf("the foreign rule is gone: %+v", s.Rules)
	}
	// the host's default route is the host's
	foreignRoute := false
	for _, r := range s.Routes {
		foreignRoute = foreignRoute || r.Dst == "default" && r.Gateway == "192.168.56.254" && r.Dev == "mgmt0"
	}
	if !foreignRoute {
		t.Errorf("the host's default route is gone: %+v", s.Routes)
	}
}

func TestTeardownTwiceFindsNothingToDoAndAKilledGatewayIsFinished(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	e.apply(e.compile(withService))
	e.teardown()
	// a second run: nothing is left, and it does not fail on what is not there
	p, err := apply.Teardown(context.Background(), e.exec(), "", "cgsvc")
	if err != nil {
		t.Fatalf("the second teardown: %v", err)
	}
	for _, o := range p.Ops {
		switch o := o.(type) {
		case *executor.Links, *executor.Routing, *executor.TC, *executor.WireGuard, *executor.ProbeNS, *executor.DockerUser:
			t.Errorf("the second teardown plans %T", o)
		}
	}

	// a gateway that was killed in the middle of the cleanup: the bridges are gone, the table and the name are not
	e.apply(e.compile(withService))
	h := &newEnvHandle{env: e}
	h.runIP("link", "delete", "dev", "br-iot")
	h.runIP("link", "delete", "dev", "br-lab")
	e.teardown()
	if left := apply.Leftovers(e.teardownState(), []string{"br-iot", "br-lab", "svc0"}); len(left) != 0 {
		t.Errorf("left: %v", left)
	}
}

func TestTeardownRemovesTheTCTreesAndTheRulesInDockerUser(t *testing.T) {
	x := newTCEnv(t)
	x.k.ServiceNamespace("cgsvc")
	tg := x.compileWith(x.overlay(dev150))
	x.applyRetiring(tg)
	before := x.teardownState()
	if len(before.TC) == 0 || !before.DockerUser.ChainExists || len(before.DockerUser.In) == 0 {
		t.Fatalf("the fault left no tc tree or no DOCKER-USER rule: %d trees, %+v", len(before.TC), before.DockerUser)
	}
	x.teardown()
	after := x.teardownState()
	if len(after.TC) != 0 {
		t.Errorf("tc trees: %v", after.TC)
	}
	if len(after.DockerUser.In) != 0 || len(after.DockerUser.Out) != 0 {
		t.Errorf("DOCKER-USER: %+v", after.DockerUser)
	}
	if !after.DockerUser.ChainExists {
		t.Error("the chain of Docker was deleted")
	}
}

func TestTeardownRemovesTheWireGuardInterfaces(t *testing.T) {
	e := newWGEnv(t)
	res := e.apply()
	if len(res.After.WireGuard) == 0 {
		t.Fatal("no WireGuard interface applied")
	}
	var owned []string
	for n := range res.After.WireGuard {
		owned = append(owned, n)
	}
	p, err := apply.Teardown(context.Background(), e.exec(), "", "")
	if err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if e.wgOps(&apply.Result{Plan: p}) != len(owned) {
		t.Errorf("%d WireGuard deletes planned for %d interfaces", e.wgOps(&apply.Result{Plan: p}), len(owned))
	}
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range owned {
		if _, ok := s.Links[n]; ok {
			t.Errorf("%s is still there", n)
		}
	}
}
