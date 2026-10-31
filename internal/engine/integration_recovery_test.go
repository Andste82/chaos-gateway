//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// M27 level 1: the recovery and the stop cleanup on the real kernel, with the real executor and, for the probes, the real Kea
// (plan §3.8 "Stop", §3.9, §2.14, D35). The simulated kernel's tests are recovery_test.go and internal/apply/teardown_test.go.

// killedExec is an executor that is killed once armed, in the middle of the request that carries the nftables transaction:
// the operations before it ran, it and what follows never did, and every later request fails (the executor is gone).
type killedExec struct {
	inner     apply.Exec
	arm, dead atomic.Bool
}

func (k *killedExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if k.dead.Load() {
		return executor.Outcome{}, errors.New("the connection to the executor broke: it was killed")
	}
	if k.arm.Load() {
		for i, op := range ops {
			if _, ok := op.(*executor.NftApply); ok {
				if i > 0 {
					_, _ = k.inner.Do(ctx, ops[:i]...)
				}
				k.dead.Store(true)
				return executor.Outcome{}, errors.New("the connection to the executor broke: it was killed")
			}
		}
	}
	return k.inner.Do(ctx, ops...)
}

// M27 test: the executor is killed in the middle of an apply: the operations before the nftables transaction ran, the
// transaction and what follows never did, and the executor is gone. The next process recompiles the committed revision and
// the kernel is that revision again, with nothing of the interrupted one left.
func TestKillingTheExecutorInTheMiddleOfAnApplyIsRecoveredOnTheRealKernel(t *testing.T) {
	k := &killedExec{}
	// the executor keeps the interfaces it assigned in a file, so that the process after the kill knows the bridges are its own
	state := t.TempDir() + "/exec-state.json"
	realExecutorOptions = []executor.Option{executor.WithStateFile(state)}
	t.Cleanup(func() { realExecutorOptions = nil })
	r := newReal(t, func(x apply.Exec) apply.Exec { k.inner = x; return k }, realClock)
	r1, err := r.apply(r.revision(nil), engine.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	k.arm.Store(true)
	// the revision changes the next hop of the uplink: the routes of table 100 are replaced before the nftables transaction
	if _, err := r.apply(r.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") }), engine.ApplyOptions{}); err == nil {
		t.Fatal("the apply of the killed executor succeeded")
	}
	if out := r.top.GW.Must("ip", "route", "show", "table", "100"); !strings.Contains(out, "203.0.113.20") {
		t.Fatalf("the interrupted apply left nothing of its own, so the test checks nothing:\n%s", out)
	}

	// the process restarts: a new executor, a new engine, the same store (revision 1 is the committed one)
	r.e.Close()
	r.ex.Close()
	ex, err := executor.New(executor.NewExecRunner(), executor.WithStateFile(state))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	e, err := engine.New(engine.Config{Store: r.st, Exec: apply.Local{E: ex}, Namespace: r.top.GW.Name, Clock: &clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	r.e, r.ex, r.exec = e, ex, apply.Local{E: ex}
	snap, err := e.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Applied == nil || snap.Applied.Revision != r1.Revision || snap.LastError != "" || snap.SafeMode {
		t.Fatalf("%+v", snap)
	}
	if out := r.top.GW.Must("ip", "route", "show", "table", "100"); strings.Contains(out, "203.0.113.20") || !strings.Contains(out, "203.0.113.10") {
		t.Errorf("the route of the interrupted apply is still there:\n%s", out)
	}
	r.verifyKernel()
}

// The executor as a process of its own (plan §3.1): the helper below is the test binary run again inside the gateway's
// namespace, serving the executor protocol on a socket with the executor and the server of `chaosgw exec`. The test kills it
// with SIGKILL, which the in-process executor of the other tests cannot survive. (The test binary stands in for the chaosgw
// binary because the VM of the testbed has no toolchain to build one; the code that serves is the same.)
const (
	execProcessSocket = "CHAOSGW_TEST_EXECUTOR_SOCKET"
	execProcessState  = "CHAOSGW_TEST_EXECUTOR_STATE"
	execProcessBird   = "CHAOSGW_TEST_EXECUTOR_BIRD"
)

// TestExecutorProcessHelper is not a test: it is the executor process that startExecutorProcess runs. Without the
// environment it does nothing.
func TestExecutorProcessHelper(t *testing.T) {
	sock := os.Getenv(execProcessSocket)
	if sock == "" {
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ex, err := executor.New(executor.NewExecRunner(), executor.WithLogger(log), executor.WithStateFile(os.Getenv(execProcessState)), executor.WithBirdDir(os.Getenv(execProcessBird)))
	if err != nil {
		t.Fatal(err)
	}
	l, err := executor.Listen(sock, 0o660, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv := &executor.Server{Exec: ex, Auth: executor.AllowUIDs(), Log: log}
	_ = srv.Serve(context.Background(), l)
	os.Exit(0)
}

// execProcess is a running executor process.
type execProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	out  *syncBuffer
}

// syncBuffer is a bytes.Buffer that the process and the test can use at once.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startExecutorProcess runs the executor in the network namespace ns and returns when it accepts connections. The process is
// killed when the test ends.
func startExecutorProcess(t *testing.T, ns, sock, state string) *execProcess {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := &execProcess{done: make(chan struct{}), out: &syncBuffer{}}
	p.cmd = exec.Command("ip", "netns", "exec", ns, exe, "-test.run=^TestExecutorProcessHelper$", "-test.count=1", "-test.timeout=0")
	// the directory of BIRD's files: an executor without one answers a read of BIRD with an error the socket turns into text
	bird := filepath.Join(filepath.Dir(state), "bird")
	if err := os.MkdirAll(bird, 0o755); err != nil {
		t.Fatal(err)
	}
	p.cmd.Env = append(os.Environ(), execProcessSocket+"="+sock, execProcessState+"="+state, execProcessBird+"="+bird)
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() { _ = p.cmd.Process.Kill(); <-p.done })
	deadline := time.Now().Add(30 * time.Second)
	for {
		if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
			_ = c.Close()
			return p
		}
		select {
		case <-p.done:
			t.Fatalf("the executor process ended before it served:\n%s", p.out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the executor process does not serve on %s:\n%s", sock, p.out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// nftTransactionOf returns the pid of the `nft -j -f -` that the process pid is running now: the transaction of an apply.
func nftTransactionOf(pid int) (int, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(stat)
		open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || closing < open || s[open+1:closing] != "nft" {
			continue
		}
		// the fields after the command name: the state, then the parent. A zombie has ended: its transaction is over.
		if f := strings.Fields(s[closing+1:]); len(f) < 2 || f[0] == "Z" || f[1] != strconv.Itoa(pid) {
			continue
		}
		if cmdline, err := os.ReadFile("/proc/" + e.Name() + "/cmdline"); err == nil && strings.Contains(string(cmdline), "\x00-f\x00-") {
			return n, true
		}
	}
	return 0, false
}

// M27 test: the executor is a process of its own, and it is killed with SIGKILL while it runs the nftables transaction of an
// apply (stopped there, so that the request cannot end first): the operations before it ran, the transaction is cut off, and
// the connection breaks. The next executor process and engine recompile the committed revision, and the kernel is that
// revision again with nothing of the interrupted one left.
func TestKillingTheExecutorProcessInTheMiddleOfAnApplyIsRecoveredOnTheRealKernel(t *testing.T) {
	dir := t.TempDir()
	sock, state := dir+"/exec.sock", dir+"/exec-state.json"
	var proc *execProcess
	var dials []*executor.Redialing
	redial := func() *executor.Redialing {
		x := executor.NewRedialing(sock, executor.DialOptions{Auth: executor.AllowUIDs()})
		dials = append(dials, x)
		return x
	}
	// the engine reaches the executor over the socket, inside the gateway's namespace (so the engine names none)
	r := newRealConfig(t, nil, nil, func(c *engine.Config) {
		proc = startExecutorProcess(t, c.Namespace, sock, state)
		c.Exec, c.Namespace = redial(), ""
		// registered before the engine's own cleanup, so the connections close after the engines do
		t.Cleanup(func() {
			for _, x := range dials {
				_ = x.Close()
			}
		})
	}, realClock)
	r1, err := r.apply(r.revision(nil), engine.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// each attempt changes the next hop of the uplink, which replaces the routes of table 100 before the transaction, and
	// watches for the transaction: the executor is stopped in it and killed. An apply that ends before the watcher sees the
	// transaction is committed and the next attempt starts from it.
	var gateway string
	committed := r1.Revision
	killed := false
	for attempt := 0; attempt < 20 && !killed; attempt++ {
		committed = r.st.ActiveID()
		gateway = fmt.Sprintf("203.0.113.%d", 20+attempt)
		rev := r.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr(gateway) })
		stop, caught := make(chan struct{}), make(chan bool, 1)
		go func(proc *execProcess) {
			for {
				select {
				case <-stop:
					caught <- false
					return
				default:
				}
				if nft, ok := nftTransactionOf(proc.cmd.Process.Pid); ok && syscall.Kill(nft, syscall.SIGSTOP) == nil {
					_ = proc.cmd.Process.Kill()
					_ = syscall.Kill(nft, syscall.SIGKILL)
					caught <- true
					return
				}
			}
		}(proc)
		_, err := r.apply(rev, engine.ApplyOptions{})
		close(stop)
		if killed = <-caught; killed {
			if err == nil {
				t.Fatal("the apply of the killed executor succeeded")
			}
			continue
		}
		if err != nil {
			t.Fatalf("attempt %d: the apply failed although nothing was killed: %v\n%s", attempt, err, proc.out)
		}
	}
	if !killed {
		t.Fatal("the executor was never inside the nftables transaction when the watcher looked: no request was cut")
	}
	<-proc.done
	if out := r.top.GW.Must("ip", "route", "show", "table", "100"); !strings.Contains(out, gateway) {
		t.Fatalf("the interrupted apply left nothing of its own, so the test checks nothing:\n%s", out)
	}

	// the process restarts: a new executor, a new engine, the same store
	r.e.Close()
	proc = startExecutorProcess(t, r.top.GW.Name, sock, state)
	e, err := engine.New(engine.Config{Store: r.st, Exec: redial(), Clock: &clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	r.e = e
	snap, err := e.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Applied == nil || snap.Applied.Revision != committed || snap.LastError != "" || snap.SafeMode {
		t.Fatalf("want revision %d applied: %+v", committed, snap)
	}
	if out := r.top.GW.Must("ip", "route", "show", "table", "100"); strings.Contains(out, gateway) {
		t.Errorf("the route of the interrupted apply is still there:\n%s", out)
	}
	// the interfaces the gateway owns are known to the executor process, not to the executor of the test
	r.reader = redial()
	r.verifyKernel()
}

// M27 test: a test interface is unplugged (the link disappears): its network is degraded, the gateway is not in safe mode and
// the other network keeps working.
func TestUnpluggingATestInterfaceDegradesItsNetworkWithoutSafeModeOnTheRealKernel(t *testing.T) {
	r := newReal(t, nil, realClock)
	if _, err := r.apply(r.revision(nil), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	ch, cancel := r.e.Subscribe()
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if err := r.e.FollowHost(ctx, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// lan1 is the port of the network Lab
	r.top.GW.Must("ip", "link", "delete", "dev", "lan1")
	if _, ok := waitEvent(ch, engine.EventNetworkDegraded, 60*time.Second); !ok {
		t.Fatal("no network_degraded event after the link went")
	}
	snap, err := r.e.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.SafeMode || snap.LastError != "" || len(snap.Problems) != 1 || snap.Problems[0].Code != compiler.CodePortMissing || snap.Problems[0].Network != "Lab" {
		t.Fatalf("%+v", snap)
	}
	// the network that kept its port still forwards: a device behind lan0 reaches the server through the gateway
	if got := sourceSeen(t, r.top); got != testbed.UplinkGateway {
		t.Errorf("the server saw %q: the other network no longer works", got)
	}
	r.verifyKernel()
}

// M27 test: after a stop (the API's part, then the executor's) no `inet chaosgw` table, no policy rule or route of Chaos
// Gateway, no bridge, veth or namespace of it remains, in particular no `pv<8 hex digits>` end and no `cgp-<8 hex digits>`
// namespace of a probe, no DHCP client of one runs and no fault is active, and no redirect is in force (the DNS redirect itself,
// with the service namespace, is the next test); and the next start rebuilds the state from the last confirmed revision.
func TestTheStopCleanupLeavesNothingOfTheGatewayOnTheRealKernel(t *testing.T) {
	b := newDHCPBed(t)
	d := b.probeBed(t)
	// a fault, so that the tc tree and the table hold something of an overlay
	b.put("target: {device: " + d.Name + "}\nfault: {latency: 30ms}")
	gw := b.top.GW
	if out := gw.Must("nft", "list", "tables"); !strings.Contains(out, "chaosgw") {
		t.Fatalf("no table before the stop:\n%s", out)
	}
	if out := strings.TrimSpace(gw.Must("ip", "route", "show", "table", "100")); out == "" {
		t.Fatal("the policy table holds no route before the stop")
	}
	// no route or rule is left in the own range 100 to 110 (D35: the policy table, the table of the replies of a management
	// forward, the service table and the PMTU mirror tables)
	ownTablesEmpty := func(when string) {
		t.Helper()
		for table := 100; table <= 110; table++ {
			// a table without routes does not exist, and `ip` says so with exit status 2
			out, err := gw.Run(context.Background(), "ip", "route", "show", "table", strconv.Itoa(table))
			if err != nil && !strings.Contains(out, "FIB table does not exist") {
				t.Fatalf("ip route show table %d: %v\n%s", table, err, out)
			}
			if out = strings.TrimSpace(out); err == nil && out != "" {
				t.Errorf("%s: table %d still holds routes:\n%s", when, table, out)
			}
		}
		var rules []struct {
			Table string `json:"table"`
		}
		if err := json.Unmarshal([]byte(gw.Must("ip", "-j", "rule", "show")), &rules); err != nil {
			t.Fatal(err)
		}
		for _, r := range rules {
			if n, err := strconv.Atoi(r.Table); err == nil && n >= 100 && n <= 110 {
				t.Errorf("%s: a rule of the table %d is still there", when, n)
			}
		}
	}
	if !strings.Contains(gw.Must("tc", "qdisc", "show"), "netem") {
		t.Fatal("no fault is active before the stop")
	}
	if n := probeClientGoroutines(); n == 0 {
		t.Fatal("no DHCP client of a probe runs before the stop")
	}

	// the stop order (plan §3.11): the overlays are removed first, then the engine ends, then the executor removes the rest
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := b.e.ResetOverlays(ctx, nil, model.Actor{Type: "system", Id: "system"}); err != nil {
		t.Fatal(err)
	}
	// (the qdisc of a removed fault may stay on a retired class until the engine deletes it; nothing is steered to it, and the
	// teardown below removes the tc tree, which is checked at the end)
	b.e.Close()
	b.ex.StopProbeClients()
	if n := probeClientGoroutines(); n != 0 {
		t.Errorf("%d DHCP clients of probes still run after they were stopped", n)
	}
	plan, err := apply.Teardown(ctx, apply.Local{E: b.ex}, gw.Name, "cgsvc-m27")
	if err != nil {
		t.Fatalf("teardown: %v\n%s", err, strings.Join(plan.Summary, "\n"))
	}

	if out := gw.Must("nft", "list", "tables"); strings.Contains(out, "chaosgw") {
		t.Errorf("the table of the gateway is still there:\n%s", out)
	}
	if out := gw.Must("nft", "list", "ruleset"); strings.Contains(out, "redirect") || strings.Contains(out, "dnat") {
		t.Errorf("a redirect is still in force:\n%s", out)
	}
	ownTablesEmpty("after the stop")
	var rules []struct {
		Protocol string `json:"protocol"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal([]byte(gw.Must("ip", "-j", "rule", "show")), &rules); err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Protocol == "201" {
			t.Errorf("a policy rule of Chaos Gateway is still there: %+v", r)
		}
	}
	var routes []struct {
		Dst      string `json:"dst"`
		Protocol string `json:"protocol"`
		Table    string `json:"table"`
	}
	if err := json.Unmarshal([]byte(gw.Must("ip", "-j", "route", "show", "table", "all")), &routes); err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if r.Protocol == "201" {
			t.Errorf("a route of Chaos Gateway is still there: %+v", r)
		}
	}
	links := gw.Must("ip", "-d", "link", "show")
	for _, bad := range []string{"bridge", "pv3a1f7c52", "br-iot", "br-lab"} {
		if strings.Contains(links, bad) {
			t.Errorf("%q is still among the links:\n%s", bad, links)
		}
	}
	if regexp.MustCompile(`\bpv[0-9a-f]{8}\b`).MatchString(links) {
		t.Errorf("the gateway's end of a probe's pair is still there:\n%s", links)
	}
	if ns := gw.Must("ip", "netns", "list"); strings.Contains(ns, "cgp-") {
		t.Errorf("a namespace of a probe is still there:\n%s", ns)
	}
	if qdiscs := gw.Must("tc", "qdisc", "show"); strings.Contains(qdiscs, "netem") {
		t.Errorf("a qdisc of a fault is still there:\n%s\n%s", qdiscs, gw.Must("ip", "-br", "link", "show"))
	}

	// compose up: the next start rebuilds everything from the last confirmed revision, the probe gets its lease again
	e, err := engine.New(engine.Config{Store: b.st, Exec: apply.Local{E: b.ex}, Namespace: gw.Name, Clock: &clock.Real{},
		DHCP: &engine.KeaDHCP{Client: b.kc, Base: kea.Config{Socket: b.kc.Socket, Leases: b.dir + "/leases.csv", Script: kea.HookScript}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if snap, err := e.Barrier(ctx); err != nil || snap.Applied == nil || snap.LastError != "" || snap.SafeMode {
		t.Fatalf("the next start: %+v %v", snap, err)
	}
	if ps := b.readProbes(); len(ps) != 1 || ps[0].Name != probeANS || !ps[0].DHCP {
		t.Errorf("the probe is not made again: %+v", ps)
	}
	if out := gw.Must("nft", "list", "tables"); !strings.Contains(out, "chaosgw") {
		t.Errorf("the table is not made again:\n%s", out)
	}

	// a gateway that was killed has not removed its overlays (they live in the engine's memory): the teardown removes the
	// mirror table of a path MTU fault with the rest
	b.e = e
	b.put("target: {device: " + d.Name + "}\nfault: {family: mtu, mtu: {size: 1280, mode: icmp}}")
	mirrorDeadline := time.Now().Add(15 * time.Second)
	for {
		// a table with no route does not exist for the kernel, and the routes of the mirror are made after the apply
		if out, err := gw.Run(context.Background(), "ip", "route", "show", "table", "103"); err == nil && strings.TrimSpace(out) != "" {
			break
		}
		if time.Now().After(mirrorDeadline) {
			t.Fatalf("the mirror table holds no route while the MTU fault is active:\n%s\n%s", gw.Must("ip", "route", "show", "table", "all"), gw.Must("ip", "rule", "show"))
		}
		time.Sleep(200 * time.Millisecond)
	}
	e.Close()
	b.ex.StopProbeClients()
	if plan, err := apply.Teardown(ctx, apply.Local{E: b.ex}, gw.Name, "cgsvc-m27"); err != nil {
		t.Fatalf("teardown of a gateway that still had an overlay: %v\n%s", err, strings.Join(plan.Summary, "\n"))
	}
	ownTablesEmpty("after the stop of a killed gateway")
}

// M27 test: the service namespace (plan §3.3): the holder container's namespace lives on under its name and the gateway's end
// of the pair leads into it. The stop removes the pair, the name under /run/netns and the DNS redirect of the test network, and
// leaves the holder's namespace to the holder. A network that redirects the DNS of its devices has a redirect to remove.
func TestTheStopCleanupRemovesTheServiceNamespaceAndTheDNSRedirectOnTheRealKernel(t *testing.T) {
	const svc = "cgsvc-m27"
	var holderPID atomic.Int64
	r := newRealConfig(t, nil, nil, func(c *engine.Config) {
		c.ServiceNS = svc
		c.ServiceHolderPID = func() int { return int(holderPID.Load()) }
	}, realClock)
	gw := r.top.GW

	// the holder: a process in a network namespace of its own, as the svcns container is
	holder := gw.Start("unshare", "-n", "sleep", "3600")
	gwInode := netnsInode(t, "/run/netns/"+gw.Name)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", holder.Pid())); strings.TrimSpace(string(comm)) == "sleep" && netnsInode(t, fmt.Sprintf("/proc/%d/ns/net", holder.Pid())) != gwInode {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the holder did not enter its own network namespace")
		}
		time.Sleep(20 * time.Millisecond)
	}
	holderPID.Store(int64(holder.Pid()))

	const iot = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	if _, err := r.apply(r.revision(func(c *model.Configuration) {
		n := (*c.Networks)[iot]
		lan, _ := n.AsLanNetwork()
		lan.Dns = &model.NetworkDns{RedirectHardcoded: ptr(true)}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iot] = n
	}), engine.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	// what the stop is to remove exists
	if _, err := os.Stat("/run/netns/" + svc); err != nil {
		t.Fatalf("the name of the service namespace is not there before the stop: %v", err)
	}
	if got, want := netnsInode(t, "/run/netns/"+svc), netnsInode(t, fmt.Sprintf("/proc/%d/ns/net", holder.Pid())); got != want {
		t.Fatalf("the service namespace is not the holder's: inode %d, want %d", got, want)
	}
	if out, err := gw.Run(context.Background(), "ip", "-d", "link", "show", "dev", "svc0"); err != nil || !strings.Contains(out, "veth") {
		t.Fatalf("no service pair before the stop: %v\n%s", err, out)
	}
	if out := r.nft(); !regexp.MustCompile(`daddr != .*dport 53.*dnat`).MatchString(out) {
		t.Fatalf("the DNS of the test network is not redirected before the stop:\n%s", out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := r.e.ResetOverlays(ctx, nil, model.Actor{Type: "system", Id: "system"}); err != nil {
		t.Fatal(err)
	}
	r.e.Close()
	plan, err := apply.Teardown(ctx, apply.Local{E: r.ex}, gw.Name, svc)
	if err != nil {
		t.Fatalf("teardown: %v\n%s", err, strings.Join(plan.Summary, "\n"))
	}

	if _, err := os.Stat("/run/netns/" + svc); !os.IsNotExist(err) {
		t.Errorf("the name of the service namespace is still there: %v", err)
	}
	if out, err := gw.Run(context.Background(), "ip", "link", "show", "dev", "svc0"); err == nil {
		t.Errorf("the service pair is still there:\n%s", out)
	}
	if out := gw.Must("nft", "list", "ruleset"); strings.Contains(out, "redirect") || strings.Contains(out, "dnat") {
		t.Errorf("a redirect is still in force:\n%s", out)
	}
	select {
	case <-holder.Done():
		t.Error("the holder's namespace ended with the name: it belongs to the holder")
	default:
	}
	// repeated, it finds nothing to do
	if plan, err := apply.Teardown(ctx, apply.Local{E: r.ex}, gw.Name, svc); err != nil {
		t.Errorf("the second teardown: %v\n%s", err, strings.Join(plan.Summary, "\n"))
	}
}

// netnsInode is the inode of a network namespace file.
func netnsInode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

// probeClientGoroutines counts the DHCP clients of probes that run in this process: the executor runs them as goroutines.
func probeClientGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "internal/probe.(*Client).Run")
}
