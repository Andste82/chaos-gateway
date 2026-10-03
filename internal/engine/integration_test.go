//go:build testbed

package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/store"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// failingExec wraps the real executor and fails requests that match: the injected executor failure
// of the M4 tests. The operations before the failing one have already run.
type failingExec struct {
	inner apply.Exec
	fail  func(ops []executor.Operation) error
}

func (f *failingExec) Do(ctx context.Context, ops ...executor.Operation) (executor.Outcome, error) {
	if err := f.fail(ops); err != nil {
		// run what comes before the failing operation, as the executor would
		var done []executor.Operation
		for _, op := range ops {
			if _, ok := op.(*executor.NftApply); ok {
				break
			}
			done = append(done, op)
		}
		if len(done) > 0 {
			_, _ = f.inner.Do(ctx, done...)
		}
		return executor.Outcome{}, err
	}
	return f.inner.Do(ctx, ops...)
}

type real struct {
	t    *testing.T
	top  *testbed.Topology
	ex   *executor.Executor
	st   *store.Store
	clk  *clock.Fake
	e    *engine.Engine
	base *model.Configuration
	exec apply.Exec
}

// realClock makes the engine use the real clock: the netlink debounce needs it, the confirmation
// tests want the fake one.
const realClock = true

func newReal(t *testing.T, wrap func(apply.Exec) apply.Exec, opts ...bool) *real {
	t.Helper()
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false), testbed.WithGatewayBridges(false))
	ex, err := executor.New(executor.NewExecRunner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	raw, err := os.ReadFile("testdata/testbed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfiguration(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	var x apply.Exec = apply.Local{E: ex}
	if wrap != nil {
		x = wrap(x)
	}
	r := &real{t: t, top: top, ex: ex, st: st, clk: clock.NewFake(time.Now()), base: cfg, exec: x}
	var clk clock.Clock = r.clk
	if len(opts) > 0 && opts[0] {
		clk = &clock.Real{}
	}
	e, err := engine.New(engine.Config{Store: st, Exec: x, Namespace: top.GW.Name, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	r.e = e
	return r
}

func (r *real) revision(mod func(*model.Configuration)) int64 {
	r.t.Helper()
	b, _ := json.Marshal(r.base)
	var cfg model.Configuration
	if err := json.Unmarshal(b, &cfg); err != nil {
		r.t.Fatal(err)
	}
	if mod != nil {
		mod(&cfg)
	}
	rev, err := r.st.Create(&cfg, store.CreateOptions{IfMatch: r.st.ActiveID(), Now: r.clk.Now(), By: model.Actor{Id: "admin", Type: "user"}})
	if err != nil {
		r.t.Fatalf("create: %v", err)
	}
	return rev.Id
}

func (r *real) apply(rev int64, o engine.ApplyOptions) (engine.Applied, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return r.e.Apply(ctx, rev, o)
}

func (r *real) nft() string {
	out, err := r.top.GW.Run(context.Background(), "nft", "list", "table", "inet", "chaosgw")
	if err != nil {
		r.t.Fatalf("nft list: %v: %s", err, out)
	}
	return out
}

func waitEvent(ch <-chan engine.Event, typ string, d time.Duration) (engine.Event, bool) {
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return engine.Event{}, false
			}
			if ev.Type == typ {
				return ev, true
			}
		case <-deadline:
			return engine.Event{}, false
		}
	}
}

func sourceSeen(t *testing.T, top *testbed.Topology) string {
	t.Helper()
	l := top.Server.Start("python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", 9000))
d, a = s.recvfrom(64)
print(a[0], flush=True)
`)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = top.A.Run(context.Background(), "python3", "-c", `import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.sendto(b"x", ("`+testbed.ServerAddr+`", 9000))
`)
		select {
		case <-l.Done():
			return strings.TrimSpace(l.Output())
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatalf("nothing arrived: %q", l.Output())
	return ""
}

// M4 test: a changed uplink address keeps NAT working and emits an event, found through netlink.
func TestTheEngineFollowsTheUplinkThroughNetlinkEvents(t *testing.T) {
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
	if got := sourceSeen(t, r.top); got != testbed.UplinkGateway {
		t.Fatalf("before: %s", got)
	}
	// the OS changes the uplink address; nobody tells the engine
	r.top.GW.Must("ip", "addr", "del", testbed.UplinkGateway+"/24", "dev", "wan0")
	r.top.GW.Must("ip", "addr", "add", "203.0.113.50/24", "dev", "wan0")
	ev, ok := waitEvent(ch, engine.EventUplinkChanged, 60*time.Second)
	if !ok {
		t.Fatal("no uplink_changed event after the address change")
	}
	t.Logf("event: %+v", ev.Data)
	snap, err := r.e.Barrier(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Applied.Uplink.Addr.Addr().String() != "203.0.113.50" {
		t.Errorf("applied uplink %+v", snap.Applied.Uplink)
	}
	if got := sourceSeen(t, r.top); got != "203.0.113.50" {
		t.Errorf("after: the server saw %s, want the new address: NAT must keep working", got)
	}
	// the apply that followed the event did not make the host change again: no endless loop
	time.Sleep(time.Second)
	g := r.e.Snapshot().Generation
	time.Sleep(2 * time.Second)
	if r.e.Snapshot().Generation != g {
		t.Error("the engine keeps applying after the host settled")
	}
}

// M4 test: an unconfirmed lockout-relevant change rolls back after the timeout (fake clock), on a
// real kernel.
func TestAnUnconfirmedChangeIsRolledBackOnARealKernel(t *testing.T) {
	r := newReal(t, nil)
	r1, err := r.apply(r.revision(nil), engine.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// management access is allowed from the whole 172.16.0.0/12 now: lockout relevant
	rev := r.revision(func(c *model.Configuration) {
		c.Management.AllowedSources = &[]string{"192.168.56.0/24", "172.16.0.0/12"}
	})
	a, err := r.apply(rev, engine.ApplyOptions{ConfirmTimeout: 20 * time.Second})
	if err != nil || a.Status != "pending_confirm" {
		t.Fatalf("%+v %v", a, err)
	}
	if !strings.Contains(r.nft(), "172.16.0.0/12") {
		t.Fatal("the change is not in the kernel")
	}
	r.clk.Advance(21 * time.Second)
	var snap *engine.Snapshot
	for i := 0; i < 200; i++ {
		snap, _ = r.e.Barrier(context.Background())
		if snap != nil && snap.Pending == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if snap.Pending != nil || snap.Revision != r1.Revision {
		t.Fatalf("%+v", snap)
	}
	if strings.Contains(r.nft(), "172.16.0.0/12") {
		t.Errorf("the rolled back source is still in the kernel:\n%s", r.nft())
	}
	// the management network still reaches the control plane: it never lost it
	l := r.top.GW.Start("python3", "-c", `import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 443)); s.listen(5)
c, a = s.accept(); print("ok", flush=True)
`)
	time.Sleep(500 * time.Millisecond)
	_, err = r.top.Mgmt.Run(context.Background(), "python3", "-c", `import socket
socket.create_connection(("`+testbed.MgmtGateway+`", 443), timeout=3)
`)
	if err != nil {
		t.Errorf("management lost the control plane: %v (%s)", err, l.Output())
	}
}

// M4 test: an injected executor failure leaves the previous state active, on a real kernel: the
// links the failed apply had already created are gone again and traffic still flows.
func TestAnInjectedExecutorFailureLeavesThePreviousStateActive(t *testing.T) {
	failNow := false
	r := newReal(t, func(inner apply.Exec) apply.Exec {
		return &failingExec{inner: inner, fail: func(ops []executor.Operation) error {
			if !failNow {
				return nil
			}
			for _, op := range ops {
				if n, ok := op.(*executor.NftApply); ok && strings.Contains(string(n.Ruleset), "br-extra") {
					return errors.New("injected executor failure")
				}
			}
			return nil
		}}
	})
	r1, err := r.apply(r.revision(nil), engine.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := r.nft()
	failNow = true
	rev := r.revision(func(c *model.Configuration) {
		n := (*c.Networks)["1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"]
		lan, _ := n.AsLanNetwork()
		lan.Name = "Extra"
		_ = n.FromLanNetwork(lan)
		(*c.Networks)["1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"] = n
	})
	_, err = r.apply(rev, engine.ApplyOptions{})
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) || !af.Restored {
		t.Fatalf("got %v", err)
	}
	failNow = false
	snap, _ := r.e.Barrier(context.Background())
	if snap.Revision != r1.Revision || snap.Applied.Revision != r1.Revision {
		t.Fatalf("%+v", snap)
	}
	if out, err := r.top.GW.Run(context.Background(), "ip", "link", "show", "dev", "br-extra"); err == nil {
		t.Errorf("the bridge of the failed apply is still there: %s", out)
	}
	if out := r.top.GW.Must("ip", "-o", "link", "show", "dev", "lan1"); !strings.Contains(out, "master br-lab") {
		t.Errorf("lan1 is not back in its bridge: %s", out)
	}
	// the content is what it was (the generation comment differs)
	strip := func(s string) string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			if !strings.Contains(l, "gen=") && !strings.Contains(l, "handle") {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	if strip(before) != strip(r.nft()) {
		t.Errorf("the nftables table differs from before the failed apply:\n%s\n---\n%s", before, r.nft())
	}
	if res := testbed.MustPing(t, r.top.A, testbed.ServerAddr, 3, 200*time.Millisecond); res.Loss() != 0 {
		t.Errorf("loss %.0f%% after the restore", res.Loss()*100)
	}
}
