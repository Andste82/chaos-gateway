package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

func newExec(t *testing.T, r Runner, opts ...Option) *Executor {
	t.Helper()
	e, err := New(r, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func ops(t *testing.T, in ...string) []Operation {
	t.Helper()
	var out []Operation
	for _, s := range in {
		out = append(out, mustDecode(t, s))
	}
	return out
}

const (
	assignWan = `{"type":"assign_interfaces","devs":["wan0","lan0"]}`
	tcWan     = `{"type":"tc","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","args":["netem","delay","10ms"]}]}`
	nftOp     = `{"type":"nft_apply","ruleset":` + goodNft + `}`
)

func TestOperationsAreSerialized(t *testing.T) {
	fr := &fakeRunner{delay: 5 * time.Millisecond}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Do(context.Background(), mustDecode(t, tcWan)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if fr.overlap.Load() {
		t.Fatal("two commands ran at the same time")
	}
	if got := len(fr.commands()); got != 20 {
		t.Fatalf("%d commands, want 20", got)
	}
}

func TestQueueKeepsArrivalOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	started := make(chan struct{})
	release := make(chan struct{})
	var first sync.Once
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		first.Do(func() { close(started); <-release }) // hold the first job until all others are queued
		mu.Lock()
		order = append(order, c.Stdin)
		mu.Unlock()
		return Result{}, nil
	}}
	e := newExec(t, fr)
	var done []chan struct{}
	var want []string
	for i := 0; i < 10; i++ {
		ch := make(chan struct{})
		done = append(done, ch)
		op := mustDecode(t, `{"type":"nft_apply","ruleset":{"nftables":[{"add":{"table":{"family":"inet","name":"chaosgw","handle":`+string(rune('0'+i))+`}}}]}}`)
		want = append(want, planStdin(t, op))
		go func() { _, _ = e.Do(context.Background(), op); close(ch) }()
		// submit in a known order: wait until this request sits in the queue (the first is running)
		if i == 0 {
			<-started
		}
		for e.waiting() < i {
			time.Sleep(100 * time.Microsecond)
		}
	}
	close(release)
	for _, ch := range done {
		<-ch
	}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("executed out of order:\n%v\n%v", order, want)
	}
}

func planStdin(t *testing.T, op Operation) string {
	t.Helper()
	s, err := Plan(op)
	if err != nil {
		t.Fatal(err)
	}
	return s[0].Cmd.Stdin
}

func TestGenerationBumpsOncePerMutatingRequest(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	ctx := context.Background()
	if g := e.Generation(); g != 0 {
		t.Fatalf("initial generation %d", g)
	}
	out, err := e.DoBatch(ctx, ops(t, assignWan, tcWan, nftOp))
	if err != nil || out.Generation != 1 || out.Completed != 3 {
		t.Fatalf("a batch is one generation: %+v %v", out, err)
	}
	out, err = e.Do(ctx, mustDecode(t, `{"type":"read","what":"rules"}`))
	if err != nil || out.Generation != 1 {
		t.Fatalf("a read must not bump the generation: %+v %v", out, err)
	}
	out, err = e.DoBatch(ctx, nil)
	if err != nil || out.Generation != 1 || out.Completed != 0 {
		t.Fatalf("an empty batch reports the generation: %+v %v", out, err)
	}
	out, _ = e.Do(ctx, mustDecode(t, `{"type":"nft_add_elements","set":"s","elements":["10.0.0.1"]}`))
	if out.Generation != 2 {
		t.Fatalf("an incremental update bumps the generation: %+v", out)
	}
}

func TestFailedBatchStopsAndStillBumpsTheGeneration(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolTC {
			return Result{Exit: 2, Stderr: "RTNETLINK answers: Invalid argument\n"}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	out, err := e.DoBatch(context.Background(), ops(t, assignWan, nftOp, tcWan, `{"type":"offloads","devs":["wan0"]}`))
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Exit != 2 || !strings.Contains(err.Error(), "Invalid argument") {
		t.Fatalf("want a CommandError, got %v", err)
	}
	if out.Completed != 2 {
		t.Errorf("completed %d, want 2 (assign and nft)", out.Completed)
	}
	if out.Generation != 1 {
		t.Errorf("state may have changed, so the generation bumps: %d", out.Generation)
	}
	for _, c := range fr.commands() {
		if c.Tool == ToolEthtool {
			t.Error("the batch must stop at the first failure")
		}
	}
}

func TestScopeIsCheckedBeforeAnythingRuns(t *testing.T) {
	fr := &fakeRunner{}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	gen := e.Generation()
	// the nft apply is fine, the tc entry names an interface that is not assigned
	out, err := e.DoBatch(context.Background(), ops(t, nftOp,
		`{"type":"tc","entries":[{"object":"qdisc","action":"replace","dev":"enp3s0","parent":"root","args":["netem","delay","10ms"]}]}`))
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
	if n := len(fr.commands()); n != 0 {
		t.Errorf("%d commands ran although the batch is out of scope", n)
	}
	if out.Generation != gen || e.Generation() != gen {
		t.Errorf("a rejected request changes nothing: generation %d -> %d", gen, out.Generation)
	}
}

func TestScopeRejectsUnassignedInterfaces(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	ctx := context.Background()
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]string{
		"tc dev":               `{"type":"tc","entries":[{"object":"qdisc","action":"delete","dev":"eth0","parent":"root"}]}`,
		"tc mirred target":     `{"type":"tc","entries":[{"object":"filter","action":"add","dev":"wan0","parent":"ffff:","args":["protocol","ip","u32","match","u32","0","0","action","mirred","egress","redirect","dev","eth0"]}]}`,
		"tc dangling dev":      `{"type":"tc","entries":[{"object":"filter","action":"add","dev":"wan0","parent":"ffff:","args":["u32","dev"]}]}`,
		"route dev":            `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8","dev":"eth0"}]}`,
		"rule iif":             `{"type":"routing","rules":[{"action":"add","family":4,"priority":10,"iif":"eth0","table":100}]}`,
		"rule oif":             `{"type":"routing","rules":[{"action":"add","family":4,"priority":10,"oif":"eth0","table":100}]}`,
		"offloads":             `{"type":"offloads","devs":["wan0","eth0"]}`,
		"docker_user":          `{"type":"docker_user","action":"ensure","devs":["eth0"]}`,
		"docker_user (remove)": `{"type":"docker_user","action":"remove","devs":["eth0"]}`,
	} {
		_, err := e.Do(ctx, mustDecode(t, in))
		if !errors.Is(err, ErrOutOfScope) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// the same operations on assigned interfaces pass
	for _, in := range []string{
		`{"type":"tc","entries":[{"object":"filter","action":"add","dev":"wan0","parent":"ffff:","args":["u32","action","mirred","egress","redirect","dev","lan0"]}]}`,
		`{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8","dev":"wan0"}]}`,
		`{"type":"docker_user","action":"ensure","devs":["lan0"]}`,
	} {
		if _, err := e.Do(ctx, mustDecode(t, in)); err != nil {
			t.Errorf("%s: %v", in, err)
		}
	}
}

func TestAssignmentInsideABatchWidensTheScopeForLaterOperations(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	if _, err := e.DoBatch(context.Background(), ops(t, assignWan, tcWan)); err != nil {
		t.Fatal(err)
	}
	// ... but not for earlier ones
	e2 := newExec(t, &fakeRunner{})
	if _, err := e2.DoBatch(context.Background(), ops(t, tcWan, assignWan)); !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("got %v", err)
	}
}

// M3-01: the uplink (and a dedicated management interface) is assigned for tc, routing, offloads
// and DOCKER-USER, but links, sysctl, wireguard and service_ns refuse to touch it.
func TestOSOwnedInterfacesTakeOnlyTrafficControlRoutesAndOffloads(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	ctx := context.Background()
	assign := `{"type":"assign_interfaces","devs":["wan0","lan0"],"os_owned":["wan0"]}`
	if _, err := e.Do(ctx, mustDecode(t, assign)); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]string{
		"tc":          `{"type":"tc","entries":[{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","args":["netem","delay","10ms"]}]}`,
		"route dev":   `{"type":"routing","routes":[{"action":"replace","family":4,"table":100,"dst":"10.0.0.0/8","dev":"wan0"}]}`,
		"offloads":    `{"type":"offloads","devs":["wan0"]}`,
		"docker_user": `{"type":"docker_user","action":"ensure","devs":["wan0"]}`,
	} {
		if _, err := e.Do(ctx, mustDecode(t, in)); err != nil {
			t.Errorf("%s on the OS-owned interface: %v", name, err)
		}
	}
	for name, in := range map[string]string{
		"links":        `{"type":"links","entries":[{"action":"up","name":"wan0"}]}`,
		"links master": `{"type":"links","entries":[{"action":"enslave","name":"lan0","master":"wan0"}]}`,
		"sysctl":       `{"type":"sysctl","entries":[{"name":"accept_ra","dev":"wan0","value":0}]}`,
		"wireguard":    `{"type":"wireguard","action":"delete","name":"wan0"}`,
	} {
		_, err := e.Do(ctx, mustDecode(t, in))
		if !errors.Is(err, ErrOutOfScope) {
			t.Errorf("%s: want ErrOutOfScope, got %v", name, err)
		}
	}
	// service_ns's host_if is always svc0 (never an interface a real configuration could mark
	// OS-owned), so the guard is exercised directly against the scope.
	scope := NewScope("svc0")
	scope.SetOSOwned([]string{"svc0"})
	if err := scope.Check(&ServiceNS{Action: "ensure", Name: "cgsvc", HostIf: "svc0", PeerIf: "svc1"}); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("service_ns: want ErrOutOfScope, got %v", err)
	}
	// the same operations on the non-OS-owned, merely assigned interface pass
	for _, in := range []string{
		`{"type":"links","entries":[{"action":"up","name":"lan0"}]}`,
		`{"type":"sysctl","entries":[{"name":"accept_ra","dev":"lan0","value":0}]}`,
	} {
		if _, err := e.Do(ctx, mustDecode(t, in)); err != nil {
			t.Errorf("%s: %v", in, err)
		}
	}
}

func TestNamespaceIsPassedToEveryCommand(t *testing.T) {
	fr := &fakeRunner{}
	e := newExec(t, fr)
	_, err := e.DoBatch(context.Background(), ops(t, assignWan,
		`{"type":"nft_apply","namespace":"gw1","ruleset":`+goodNft+`}`,
		`{"type":"tc","namespace":"gw1","entries":[{"object":"qdisc","action":"delete","dev":"wan0","parent":"root"}]}`,
		`{"type":"read","namespace":"gw1","what":"rules"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range fr.commands() {
		if c.NS != "gw1" {
			t.Errorf("%s ran outside the namespace", c)
		}
	}
}

func TestDockerUserIsIdempotent(t *testing.T) {
	present := map[string]bool{}
	fr := &fakeRunner{}
	fr.respond = func(c Command) (Result, error) {
		args := strings.Join(c.Args, " ")
		key := args[strings.Index(args, "DOCKER-USER")+len("DOCKER-USER"):]
		key = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(key), "1 "), "")
		switch {
		case strings.Contains(args, " -C "):
			if present[key] {
				return Result{}, nil
			}
			return Result{Exit: 1, Stderr: "iptables: Bad rule (does a matching rule exist in that chain?).\n"}, nil
		case strings.Contains(args, " -I "):
			present[key] = true
		case strings.Contains(args, " -D "):
			delete(present, key)
		}
		return Result{}, nil
	}
	e := newExec(t, fr)
	ctx := context.Background()
	if _, err := e.Do(ctx, mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	count := func(flag string) (n int) {
		for _, c := range fr.commands() {
			if strings.Contains(" "+strings.Join(c.Args, " ")+" ", " "+flag+" ") {
				n++
			}
		}
		return n
	}
	ensure := mustDecode(t, `{"type":"docker_user","action":"ensure","devs":["lan0"]}`)
	for i := 0; i < 2; i++ {
		if _, err := e.Do(ctx, ensure); err != nil {
			t.Fatal(err)
		}
	}
	if got := count("-I"); got != 2 {
		t.Errorf("%d inserts for two ensures of two rules, want 2 (the second run is a no-op)", got)
	}
	remove := mustDecode(t, `{"type":"docker_user","action":"remove","devs":["lan0"]}`)
	for i := 0; i < 2; i++ {
		if _, err := e.Do(ctx, remove); err != nil {
			t.Fatal(err)
		}
	}
	if got := count("-D"); got != 2 {
		t.Errorf("%d deletes, want 2", got)
	}
	if len(present) != 0 {
		t.Errorf("rules left: %v", present)
	}
}

func TestOffloadsAreVerified(t *testing.T) {
	state := "generic-receive-offload: on\n"
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if len(c.Args) > 0 && c.Args[0] == "-k" {
			return Result{Stdout: "Features for wan0:\n" + state}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	ctx := context.Background()
	if _, err := e.Do(ctx, mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(ctx, mustDecode(t, `{"type":"offloads","devs":["wan0"]}`))
	if err == nil || !strings.Contains(err.Error(), "generic-receive-offload") {
		t.Fatalf("an offload that stays on must fail: %v", err)
	}
	state = "generic-receive-offload: off\nlarge-receive-offload: off [fixed]\n"
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"offloads","devs":["wan0"]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestReadResults(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		switch {
		case c.Tool == ToolIP && c.Args[1] == "-d":
			b, _ := os.ReadFile("../linux/testdata/ip_link.json")
			return Result{Stdout: string(b)}, nil
		case c.Tool == ToolNft:
			b, _ := os.ReadFile("../linux/testdata/nft_chaosgw.json")
			return Result{Stdout: string(b)}, nil
		}
		return Result{Exit: 1, Stderr: "boom"}, nil
	}}
	e := newExec(t, fr)
	ctx := context.Background()
	out, err := e.DoBatch(ctx, ops(t, `{"type":"read","what":"links"}`, `{"type":"read","what":"nft"}`))
	if err != nil || len(out.Data) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
	var links []linux.Link
	if err := json.Unmarshal(out.Data[0], &links); err != nil || len(links) != 2 || links[0].Name != "lo" {
		t.Fatalf("links: %v %v", links, err)
	}
	var rs linux.Ruleset
	if err := json.Unmarshal(out.Data[1], &rs); err != nil || len(rs.Objects) != 4 {
		t.Fatalf("nft: %+v %v", rs, err)
	}
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"read","what":"rules"}`)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failing read must report the tool's message: %v", err)
	}
}

func TestReadOfMissingNftTableIsEmpty(t *testing.T) {
	fr := &fakeRunner{respond: func(Command) (Result, error) {
		return Result{Exit: 1, Stderr: "Error: No such file or directory\nlist table inet chaosgw\n"}, nil
	}}
	e := newExec(t, fr)
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"nft"}`))
	if err != nil || len(out.Data) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestCancelledWhileQueuedRunsNothing(t *testing.T) {
	block := make(chan struct{})
	fr := &fakeRunner{respond: func(Command) (Result, error) { <-block; return Result{}, nil }}
	e := newExec(t, fr)
	first := make(chan struct{})
	go func() { _, _ = e.Do(context.Background(), mustDecode(t, nftOp)); close(first) }()
	for len(fr.commands()) == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := e.Do(ctx, mustDecode(t, `{"type":"nft_add_elements","set":"s","elements":["1.2.3.4"]}`))
		res <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	close(block)
	<-first
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if n := len(fr.commands()); n != 1 {
		t.Fatalf("the cancelled request ran: %d commands", n)
	}
}

func TestAssignmentsPersist(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "state.json")
	e, err := New(&fakeRunner{}, WithStateFile(file))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", st, err)
	}
	e2 := newExec(t, &fakeRunner{}, WithStateFile(file))
	if got := strings.Join(e2.Scope().Devs(), ","); got != "lan0,wan0" {
		t.Fatalf("restored scope %q", got)
	}
	// a damaged state file is an error, never an empty or a widened scope
	if err := os.WriteFile(file, []byte(`{"interfaces":["../etc"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(&fakeRunner{}, WithStateFile(file)); err == nil {
		t.Fatal("an invalid state file must be refused")
	}
	if err := os.WriteFile(file, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(&fakeRunner{}, WithStateFile(file)); err == nil {
		t.Fatal("a corrupt state file must be refused")
	}
}

func TestClosedExecutorRefuses(t *testing.T) {
	e, _ := New(&fakeRunner{})
	e.Close()
	if _, err := e.DoBatch(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestRoutingBatchesAreIdempotent(t *testing.T) {
	routing := `{"type":"routing","rules":[{"action":"add","family":4,"priority":1000,"table":100},{"action":"delete","family":4,"priority":1001,"table":100}]}`
	for name, c := range map[string]struct {
		stderr string
		fail   bool
	}{
		"already exists":    {"RTNETLINK answers: File exists\nCommand failed -:1\n", false},
		"does not exist":    {"RTNETLINK answers: No such process\nCommand failed -:2\nRTNETLINK answers: No such file or directory\nCommand failed -:3\n", false},
		"real error":        {"RTNETLINK answers: Invalid argument\nCommand failed -:1\n", true},
		"mixed with a real": {"RTNETLINK answers: File exists\nCommand failed -:1\nRTNETLINK answers: Network is unreachable\nCommand failed -:2\n", true},
		"unknown output":    {"Error: something else\n", true},
	} {
		fr := &fakeRunner{respond: func(Command) (Result, error) { return Result{Exit: 2, Stderr: c.stderr}, nil }}
		e := newExec(t, fr)
		_, err := e.Do(context.Background(), mustDecode(t, routing))
		if (err != nil) != c.fail {
			t.Errorf("%s: err=%v, want failure=%v", name, err, c.fail)
		}
	}
}

func TestMissingNamespaceIsNotAnEmptyNftState(t *testing.T) {
	fr := &fakeRunner{respond: func(Command) (Result, error) {
		return Result{Exit: 255, Stderr: `Cannot open network namespace "gw9": No such file or directory` + "\n"}, nil
	}}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","namespace":"gw9","what":"nft"}`)); err == nil {
		t.Fatal("a missing namespace must be an error, not an empty ruleset")
	}
	fr.respond = func(Command) (Result, error) {
		return Result{Exit: 1, Stderr: "Error: No such file or directory; did you mean table 'x' in family inet?\n"}, nil
	}
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"nft"}`))
	if err != nil {
		t.Fatal(err)
	}
	var rs linux.Ruleset
	if err := json.Unmarshal(out.Data[0], &rs); err != nil || len(rs.Objects) != 0 {
		t.Fatalf("%s %v", out.Data[0], err)
	}
}

// Plan §3.11: a running operation is never interrupted. Close waits for it, and its caller still
// gets the result.
func TestCloseLetsTheRunningRequestFinish(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	fr := &fakeRunner{respond: func(Command) (Result, error) { close(started); <-release; return Result{}, nil }}
	e, _ := New(fr)
	res := make(chan error, 1)
	go func() { _, err := e.Do(context.Background(), mustDecode(t, nftOp)); res <- err }()
	<-started
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a request was running")
	case <-time.After(50 * time.Millisecond):
	}
	// a request queued behind it is dropped, not executed
	queued := make(chan error, 1)
	go func() { _, err := e.Do(context.Background(), mustDecode(t, nftOp)); queued <- err }()
	close(release)
	<-closed
	if err := <-res; err != nil {
		t.Errorf("the running request must complete normally: %v", err)
	}
	if err := <-queued; err != nil && !errors.Is(err, ErrClosed) {
		t.Errorf("queued request: %v", err)
	}
	if n := len(fr.commands()); n != 1 {
		t.Errorf("%d commands ran, want only the running one", n)
	}
}

func TestCallerCancellationDoesNotInterruptARunningRequest(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var cmdCtxErr error
	fr := &fakeRunner{respond: func(Command) (Result, error) { close(started); <-release; return Result{}, nil }}
	e := newExec(t, fr)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { _, err := e.Do(ctx, mustDecode(t, nftOp)); res <- err }()
	<-started
	cancel()
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Fatalf("the caller must return at once: %v", err)
	}
	close(release)
	// the request still completes and counts: the next request sees generation 1
	out, err := e.DoBatch(context.Background(), nil)
	if err != nil || out.Generation != 1 || cmdCtxErr != nil {
		t.Fatalf("%+v %v %v", out, err, cmdCtxErr)
	}
}

func TestPanicInAnOperationIsAFailedRequestNotACrash(t *testing.T) {
	boom := true
	fr := &fakeRunner{respond: func(Command) (Result, error) {
		if boom {
			panic("boom")
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	out, err := e.Do(context.Background(), mustDecode(t, nftOp))
	if err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("got %v", err)
	}
	if out.Generation != 1 {
		t.Errorf("the kernel state is unknown after a panic: generation %d", out.Generation)
	}
	boom = false
	if _, err := e.Do(context.Background(), mustDecode(t, nftOp)); err != nil {
		t.Fatalf("the executor must keep working: %v", err)
	}
}

func TestLinksSysctlScopeAndReads(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		switch {
		case c.Tool == ToolSysctl && c.Args[0] == "-n":
			return Result{Stdout: "1\n"}, nil
		case c.Tool == ToolIptables && c.Args[len(c.Args)-1] == "DOCKER-USER" && c.Args[2] == "-S":
			return Result{Stdout: "-N DOCKER-USER\n-A DOCKER-USER -i br-lan0 -m comment --comment chaosgw -j ACCEPT\n-A DOCKER-USER -o br-lan0 -m comment --comment chaosgw -j ACCEPT\n-A DOCKER-USER -j RETURN\n"}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	ctx := context.Background()
	for name, in := range map[string]string{
		"links name":   `{"type":"links","entries":[{"action":"delete_bridge","name":"docker0x"}]}`,
		"links master": `{"type":"links","entries":[{"action":"enslave","name":"lan0","master":"br-x"}]}`,
		"sysctl dev":   `{"type":"sysctl","entries":[{"name":"accept_ra","dev":"eth0","value":0}]}`,
	} {
		if _, err := e.DoBatch(ctx, ops(t, assignWan, in)); !errors.Is(err, ErrOutOfScope) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.DoBatch(ctx, ops(t, assignWan,
		`{"type":"links","entries":[{"action":"add_bridge","name":"wan0"},{"action":"enslave","name":"lan0","master":"wan0"}]}`,
		`{"type":"sysctl","entries":[{"name":"ip_forward","value":1},{"name":"accept_ra","dev":"lan0","value":0}]}`)); err != nil {
		t.Fatal(err)
	}
	out, err := e.DoBatch(ctx, ops(t, `{"type":"read","what":"assigned"}`, `{"type":"read","what":"sysctl","name":"ip_forward"}`, `{"type":"read","what":"docker_user"}`))
	if err != nil {
		t.Fatal(err)
	}
	var assigned []string
	_ = json.Unmarshal(out.Data[0], &assigned)
	if strings.Join(assigned, ",") != "lan0,wan0" {
		t.Errorf("assigned %v", assigned)
	}
	if string(out.Data[1]) != "1" {
		t.Errorf("sysctl %s", out.Data[1])
	}
	var du linux.DockerUserState
	_ = json.Unmarshal(out.Data[2], &du)
	if !du.ChainExists || len(du.In) != 1 || du.In[0] != "br-lan0" || len(du.Out) != 1 || !du.OursFirst {
		t.Errorf("docker %+v", du)
	}
}

func TestOptionalDockerChainMissingIsNoError(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolIptables {
			return Result{Exit: 1, Stderr: "iptables: No chain/target/match by that name.\n"}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	ctx := context.Background()
	if _, err := e.DoBatch(ctx, ops(t, assignWan, `{"type":"docker_user","action":"ensure","devs":["lan0"],"optional_chain":true}`)); err != nil {
		t.Fatal(err)
	}
	for _, c := range fr.commands() {
		if c.Tool == ToolIptables && (strings.Contains(strings.Join(c.Args, " "), " -I ") || strings.Contains(strings.Join(c.Args, " "), " -C ")) {
			t.Errorf("a missing chain must stop the step: %s", c)
		}
	}
	if _, err := e.DoBatch(ctx, ops(t, `{"type":"docker_user","action":"ensure","devs":["lan0"]}`)); err == nil {
		t.Error("a required chain that is missing must fail")
	}
	out, err := e.Do(ctx, mustDecode(t, `{"type":"read","what":"docker_user"}`))
	var du linux.DockerUserState
	_ = json.Unmarshal(out.Data[0], &du)
	if err != nil || du.ChainExists {
		t.Fatalf("%+v %v", du, err)
	}
}

// `ip link delete dev X type bridge` deletes a veth, too: the executor reads the kind first.
func TestDeleteBridgeNeverDeletesAnotherKindOfDevice(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool == ToolIP && len(c.Args) > 3 && c.Args[0] == "-j" && c.Args[len(c.Args)-1] == "wan0" {
			return Result{Stdout: `[{"ifindex":3,"ifname":"wan0","flags":["UP"],"link_type":"ether"}]`}, nil
		}
		if c.Tool == ToolIP && len(c.Args) > 3 && c.Args[0] == "-j" && c.Args[len(c.Args)-1] == "br-x" {
			return Result{Stdout: `[{"ifindex":4,"ifname":"br-x","flags":["UP"],"link_type":"ether","linkinfo":{"info_kind":"bridge"}}]`}, nil
		}
		if c.Tool == ToolIP && len(c.Args) > 3 && c.Args[0] == "-j" {
			return Result{Exit: 1, Stderr: "Device does not exist.\n"}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, fr)
	ctx := context.Background()
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"assign_interfaces","devs":["wan0","br-x","gone0"]}`)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(ctx, mustDecode(t, `{"type":"links","entries":[{"action":"delete_bridge","name":"wan0"}]}`))
	if err == nil || !strings.Contains(err.Error(), "not a bridge") {
		t.Fatalf("a veth must not be deleted as a bridge: %v", err)
	}
	for _, c := range fr.commands() {
		if len(c.Args) > 1 && c.Args[0] == "link" && c.Args[1] == "delete" && c.Args[3] == "wan0" {
			t.Fatalf("the delete ran: %s", c)
		}
	}
	// a real bridge and a device that is gone are fine
	if _, err := e.Do(ctx, mustDecode(t, `{"type":"links","entries":[{"action":"delete_bridge","name":"br-x"},{"action":"delete_bridge","name":"gone0"}]}`)); err != nil {
		t.Fatal(err)
	}
}

// plan §3.11: identity updates (incremental set elements) are taken before queued plans, and the
// executor never runs two requests at the same time.
func TestIdentityUpdatesGoBeforeQueuedPlansAndNeverRunConcurrently(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	var first atomic.Bool
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if first.CompareAndSwap(false, true) {
			close(started)
			<-release // the first plan occupies the worker
		}
		kind := "plan"
		if strings.Contains(c.Stdin, `"element"`) {
			kind = "identity"
		}
		mu.Lock()
		order = append(order, kind)
		mu.Unlock()
		return Result{}, nil
	}}
	e := newExec(t, fr)
	plan := mustDecode(t, nftOp)
	add := mustDecode(t, `{"type":"nft_add_elements","set":"dev_a","elements":["10.10.0.5"]}`)
	del := mustDecode(t, `{"type":"nft_del_elements","set":"dev_a","elements":["10.10.0.4"]}`)

	var wg sync.WaitGroup
	submit := func(op Operation, waiting int) {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.Do(context.Background(), op) }()
		for e.waiting() < waiting {
			time.Sleep(100 * time.Microsecond)
		}
	}
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = e.Do(context.Background(), plan) }()
	<-started
	// two plans queue up, then two identity updates: the updates overtake the plans
	submit(plan, 1)
	submit(plan, 2)
	submit(add, 3)
	submit(del, 4)
	close(release)
	wg.Wait()
	if got := strings.Join(order, ","); got != "plan,identity,identity,plan,plan" {
		t.Errorf("order %s", got)
	}
	if fr.overlap.Load() {
		t.Error("two commands ran at the same time")
	}
	// the elements are an incremental change of the table, deletion included
	cmds := fr.commands()
	if !strings.Contains(cmds[2].Stdin, `"delete"`) || !strings.Contains(cmds[1].Stdin, `"add"`) {
		t.Errorf("%+v", cmds)
	}
}

func TestADeleteOfElementsIsValidatedLikeAnAdd(t *testing.T) {
	for name, in := range map[string]string{
		"a bad set name":  `{"type":"nft_del_elements","set":"x y","elements":["10.0.0.1"]}`,
		"no elements":     `{"type":"nft_del_elements","set":"dev_a","elements":[]}`,
		"a bad element":   `{"type":"nft_del_elements","set":"dev_a","elements":["$(id)"]}`,
		"a timeout field": `{"type":"nft_del_elements","set":"dev_a","elements":["10.0.0.1"],"timeout_seconds":5}`,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Decode([]byte(`{"type":"nft_del_elements","set":"dev_a","elements":["10.0.0.1","192.0.2.0/24"]}`)); err != nil {
		t.Fatal(err)
	}
}
