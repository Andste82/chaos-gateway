package apply_test

import (
	"context"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/probe"
)

const (
	probeA   = "3a1f7c52-6b0e-4d83-9c2a-1e4d5f60a7b8"
	probeB   = "9d2e4b61-0c3f-4a75-8e1b-2f6a7c8d9e01"
	iotNet   = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	labNet   = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"
	probeAns = "cgp-3a1f7c52"
	probeAif = "pv3a1f7c52"
)

// idleSocket is the DHCP socket of a probe in a test: nothing ever answers.
type idleSocket struct{ done chan struct{} }

func (s *idleSocket) Send([]byte, netip.AddrPort) error { return nil }
func (s *idleSocket) Recv() ([]byte, error)             { <-s.done; return nil, net.ErrClosed }
func (s *idleSocket) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

// newProbeEnv is the env of the other tests with an executor whose probes run a DHCP client on a socket nobody answers.
func newProbeEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	ex, err := executor.New(e.k, executor.WithNetnsInode(e.k.NetnsInode), executor.WithProbeSocket(func(ns, ifname string) (probe.Socket, error) {
		e.sockets.Add(1)
		return &idleSocket{done: make(chan struct{})}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	e.ex = ex
	return e
}

func withProbes(ids ...string) func(*model.Configuration, *compiler.Input) {
	return func(c *model.Configuration, in *compiler.Input) {
		probes := map[string]model.Probe{}
		for i, id := range ids {
			probes[id] = model.Probe{Name: "probe-" + string(rune('a'+i)), Network: iotNet}
		}
		c.Probes = &probes
	}
}

func summary(r *apply.Result) string { return strings.Join(r.Plan.Summary, "\n") }

func TestAProbeIsABridgePortWithItsOwnNamespaceAndMAC(t *testing.T) {
	e := newProbeEnv(t)
	tg := e.compile(withProbes(probeA))
	if len(tg.Probes) != 1 {
		t.Fatalf("probes: %+v", tg.Probes)
	}
	p := tg.Probes[0]
	if p.Namespace != probeAns || p.HostIf != probeAif || p.Bridge != "br-iot" || p.MAC != compiler.ProbeMAC(probeA) || p.Hostname != "probe-a" {
		t.Errorf("%+v", p)
	}
	res := e.apply(tg)
	if !strings.Contains(summary(res), "probe cgp-3a1f7c52 (probe-a): missing") {
		t.Errorf("the plan does not say why:\n%s", summary(res))
	}
	st, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if err != nil {
		t.Fatal(err)
	}
	ps, ok := st.Probes[probeAns]
	if !ok || ps.Master != "br-iot" || !ps.HostUp || !ps.PeerUp || ps.MAC != p.MAC || !ps.DHCP || ps.HostIf != probeAif {
		t.Fatalf("%+v", st.Probes)
	}
	// the gateway's end of the pair is a port of the bridge, and the bridge's ports in the verify are still the interface's
	if _, ok := st.Links[probeAif]; ok {
		t.Errorf("%s is among the links of the state", probeAif)
	}
	if mm := apply.Verify(tg, st); len(mm) != 0 {
		t.Errorf("a fresh apply does not verify: %v", mm)
	}
	// a second apply has nothing left to do for the probe
	if res2 := e.apply(e.compile(withProbes(probeA))); strings.Contains(summary(res2), "probe ") {
		t.Errorf("the probe is planned again:\n%s", summary(res2))
	}
}

func TestAProbeThatWasTakenOutOfItsBridgeOrWentIsHealed(t *testing.T) {
	e := newProbeEnv(t)
	tg := e.compile(withProbes(probeA))
	e.apply(tg)
	// its end of the pair leaves the bridge: verify notices and the next apply puts it back
	_, _ = e.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"link", "set", "dev", probeAif, "nomaster"}})
	st, _ := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	mm := apply.Verify(tg, st)
	if len(mm) != 1 || mm[0].Subsystem != "probes" || !strings.Contains(mm[0].Detail, "a port of") {
		t.Fatalf("verify: %v", mm)
	}
	if res := e.apply(e.compile(withProbes(probeA))); !strings.Contains(summary(res), "probe cgp-3a1f7c52") {
		t.Errorf("the probe is not made again:\n%s", summary(res))
	}
	// its namespace goes: the pair with it
	_, _ = e.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"netns", "delete", probeAns}})
	st, _ = apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if _, ok := st.Probes[probeAns]; ok {
		t.Fatalf("a probe without its namespace is listed: %+v", st.Probes)
	}
	if res := e.apply(e.compile(withProbes(probeA))); !strings.Contains(summary(res), "missing") {
		t.Errorf("the probe is not made again:\n%s", summary(res))
	}
}

func TestAProbeThatIsNotInTheConfigurationAnyMoreIsDeleted(t *testing.T) {
	e := newProbeEnv(t)
	e.apply(e.compile(withProbes(probeA, probeB)))
	res := e.apply(e.compile(withProbes(probeB)))
	if !strings.Contains(summary(res), "probe delete cgp-3a1f7c52") {
		t.Errorf("the plan does not delete it:\n%s", summary(res))
	}
	st, _ := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if len(st.Probes) != 1 || st.Probes["cgp-9d2e4b61"].Name == "" {
		t.Errorf("%+v", st.Probes)
	}
	// and with no probe at all, none is left
	e.apply(e.compile(func(c *model.Configuration, in *compiler.Input) { c.Probes = nil }))
	st, _ = apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if len(st.Probes) != 0 {
		t.Errorf("%+v", st.Probes)
	}
}

func TestProbesOfNetworksTheHostDoesNotBuildAreLeftOut(t *testing.T) {
	e := newProbeEnv(t)
	tg := e.compile(func(c *model.Configuration, in *compiler.Input) {
		probes := map[string]model.Probe{probeA: {Name: "ghost", Network: "ffffffff-ffff-4fff-8fff-ffffffffffff"}}
		c.Probes = &probes
	})
	if len(tg.Probes) != 0 {
		t.Errorf("%+v", tg.Probes)
	}
}

func TestTwoProbesWhoseIdsStartAlikeAreAnError(t *testing.T) {
	e := newProbeEnv(t)
	tg := e.compile(withProbes(probeA, "3a1f7c52-ffff-4fff-8fff-ffffffffffff"))
	if !tg.HasErrors() || tg.Errors()[0].Code != compiler.CodeProbeNameClash {
		t.Errorf("%+v", tg.Problems)
	}
}

func TestTheMACOfAProbeFollowsItsIdAndIsLocalAndUnicast(t *testing.T) {
	a, b := compiler.ProbeMAC(probeA), compiler.ProbeMAC(probeB)
	if a == b || a != compiler.ProbeMAC(strings.ToUpper(probeA)) {
		t.Errorf("%s %s", a, b)
	}
	hw, err := net.ParseMAC(a)
	if err != nil || hw[0]&1 != 0 || hw[0]&2 == 0 {
		t.Errorf("%s %v", a, err)
	}
}

// inProbe runs an ip command inside the namespace of probe A on the simulated kernel.
func (e *env) inProbe(args ...string) {
	e.t.Helper()
	r, err := e.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: args, NS: probeAns})
	if err != nil || r.Exit != 0 {
		e.t.Fatalf("ip %v in %s: %v %+v", args, probeAns, err, r)
	}
}

func (e *env) probeState(tg *compiler.Target) executor.ProbeNSState {
	e.t.Helper()
	st, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if err != nil {
		e.t.Fatal(err)
	}
	return st.Probes[probeAns]
}

func TestAProbeMovedToAnotherNetworkGetsANewDHCPClientAndLosesTheOldAddress(t *testing.T) {
	e := newProbeEnv(t)
	e.apply(e.compile(withProbes(probeA)))
	if n := e.sockets.Load(); n != 1 {
		t.Fatalf("sockets after the first apply: %d", n)
	}
	// the lease of the IoT network is on the interface
	e.inProbe("addr", "replace", "10.10.0.77/24", "dev", executor.ProbePeerIf)
	// a second apply of the same probe keeps the client
	e.apply(e.compile(withProbes(probeA)))
	if n := e.sockets.Load(); n != 1 {
		t.Fatalf("sockets after an apply that changes nothing: %d", n)
	}
	// the probe is edited into the Lab network
	moved := func(c *model.Configuration, in *compiler.Input) {
		withProbes(probeA)(c, in)
		(*c.Probes)[probeA] = model.Probe{Name: "probe-a", Network: labNet}
	}
	tg := e.compile(moved)
	res := e.apply(tg)
	if !strings.Contains(summary(res), `a port of "br-iot", want br-lab`) {
		t.Errorf("the plan does not say why:\n%s", summary(res))
	}
	if n := e.sockets.Load(); n != 2 {
		t.Errorf("the DHCP client of the old network still runs: %d sockets opened", n)
	}
	ps := e.probeState(tg)
	if ps.Master != "br-lab" || !ps.DHCP || len(ps.Addrs) != 0 {
		t.Errorf("the probe keeps the lease of the old network: %+v", ps)
	}
}

func TestAProbeWhoseNamespaceWasMadeAgainGetsANewDHCPClient(t *testing.T) {
	e := newProbeEnv(t)
	tg := e.compile(withProbes(probeA))
	e.apply(tg)
	// the namespace goes, with the pair and the socket of the client in it
	_, _ = e.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"netns", "delete", probeAns}})
	if res := e.apply(e.compile(withProbes(probeA))); !strings.Contains(summary(res), "missing") {
		t.Fatalf("the probe is not made again:\n%s", summary(res))
	}
	if n := e.sockets.Load(); n != 2 {
		t.Errorf("the client of the old namespace still runs: %d sockets opened", n)
	}
	if ps := e.probeState(tg); !ps.DHCP {
		t.Errorf("%+v", ps)
	}
}

// The clients are goroutines of the executor: a restarted executor finds the namespace and the pair as they were and has no
// client for them (P6-AUDIT-63). Nothing else is wrong, and the next apply starts one.
func TestAProbeWhoseExecutorLostItsClientGetsANewDHCPClient(t *testing.T) {
	e := newProbeEnv(t)
	// what the executor owns is in its state file, which a restarted one reads
	state := executor.WithStateFile(filepath.Join(t.TempDir(), "executor-state.json"))
	idle := executor.WithProbeSocket(func(ns, ifname string) (probe.Socket, error) {
		e.sockets.Add(1)
		return &idleSocket{done: make(chan struct{})}, nil
	})
	start := func() *executor.Executor {
		ex, err := executor.New(e.k, executor.WithNetnsInode(e.k.NetnsInode), state, idle)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(ex.Close)
		return ex
	}
	e.ex = start()
	tg := e.compile(withProbes(probeA))
	e.apply(tg)
	e.inProbe("addr", "replace", "10.10.0.77/24", "dev", executor.ProbePeerIf)
	if n := e.sockets.Load(); n != 1 || !e.probeState(tg).DHCP {
		t.Fatalf("sockets after the first apply: %d", n)
	}
	// the executor process restarts: the kernel is the same, its clients are gone
	e.ex.Close()
	e.ex = start()
	if ps := e.probeState(tg); ps.DHCP || ps.Master != "br-iot" || !ps.PeerUp {
		t.Fatalf("the state of a probe without a client: %+v", ps)
	}
	res := e.apply(e.compile(withProbes(probeA)))
	if !strings.Contains(summary(res), probeAns+" (probe-a): has no DHCP client") {
		t.Errorf("the plan does not say why:\n%s", summary(res))
	}
	if n := e.sockets.Load(); n != 2 {
		t.Errorf("no client was started: %d sockets opened", n)
	}
	if ps := e.probeState(tg); !ps.DHCP || len(ps.Addrs) == 0 {
		t.Errorf("the probe has no client or lost its address: %+v", ps)
	}
	// with the client back an apply changes nothing
	if res := e.apply(e.compile(withProbes(probeA))); strings.Contains(summary(res), probeAns) || e.sockets.Load() != 2 {
		t.Errorf("an apply that has nothing to do touches the probe:\n%s", summary(res))
	}
}

func TestAProbeWithAnotherMACGetsItsMACBack(t *testing.T) {
	e := newProbeEnv(t)
	tg := e.compile(withProbes(probeA))
	e.apply(tg)
	e.inProbe("link", "set", "dev", executor.ProbePeerIf, "address", "02:11:22:33:44:55")
	if mm := apply.Verify(tg, func() *apply.State {
		st, _ := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
		return st
	}()); len(mm) != 1 || !strings.Contains(mm[0].Detail, "MAC") {
		t.Fatalf("verify: %v", mm)
	}
	res := e.apply(e.compile(withProbes(probeA)))
	if !strings.Contains(summary(res), "has the MAC 02:11:22:33:44:55") {
		t.Errorf("the plan does not say why:\n%s", summary(res))
	}
	if ps := e.probeState(tg); ps.MAC != compiler.ProbeMAC(probeA) || !ps.PeerUp {
		t.Errorf("the MAC is not back: %+v", ps)
	}
	if res := e.apply(e.compile(withProbes(probeA))); strings.Contains(summary(res), "probe ") {
		t.Errorf("the probe is planned again:\n%s", summary(res))
	}
}
