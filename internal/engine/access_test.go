package engine_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The access rules in the engine (M9): rule overlays take the same road as fault overlays (TTL, lease,
// replace, capacity), they stand in front of the configured rules, "also cut existing connections" runs
// after the apply and deletes exactly the connections the rule owns, and `explain` names the rule that
// decides. These tests run on the kernel simulator and the fake clock; what the kernel does with the
// rules is in internal/compiler/accesskernel_test.go and integration_access_test.go.

const (
	ruleIoT53   = "target: {network: IoT}\nrule: {protocol: udp, ports: [53], action: drop}"
	ruleIoTCut  = "target: {network: IoT}\nrule: {protocol: tcp, ports: [8883], action: drop, cut_existing: true}"
	cfgRuleA    = "a1000000-0000-4000-8000-000000000001"
	cfgRuleB    = "a1000000-0000-4000-8000-000000000002"
	conntrackIP = "10.10.0.10"
)

// withConfigRules adds configured rules in the given order.
func withConfigRules(rules map[string]model.AccessRule, order ...string) func(*model.Configuration) {
	return func(c *model.Configuration) {
		m := map[string]model.AccessRule{}
		var ord []uuid.UUID
		for _, id := range order {
			m[id] = rules[id]
			ord = append(ord, uuid.MustParse(id))
		}
		c.AccessRules, c.AccessRuleOrder = &m, &ord
	}
}

func configRule(name, network, proto string, ports []int, action model.AccessAction, cut bool) model.AccessRule {
	r := model.AccessRule{Name: &name, Source: model.Scope{Network: &network}, Action: action}
	if proto != "" {
		p := model.Protocol(proto)
		r.Protocol = &p
	}
	if ports != nil {
		r.Ports = &ports
	}
	if cut {
		r.CutExisting = &cut
	}
	return r
}

// kernelCommands records the stdin of every command the kernel simulator runs, so a test can see a cut
// window open and close and the connections deleted.
type kernelCommands struct {
	mu   sync.Mutex
	cmds []recorded
}

type recorded struct {
	argv  string
	stdin string
}

func (h *harness) recordCommands() *kernelCommands {
	rec := &kernelCommands{}
	h.k.SetAfter(func(argv []string, stdin string) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.cmds = append(rec.cmds, recorded{strings.Join(argv, " "), stdin})
	})
	return rec
}

func (r *kernelCommands) all() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recorded(nil), r.cmds...)
}

// windows returns the nft transactions that put rules into the cut chains (an opened window), and
// how many that empty them (a closed one).
func (r *kernelCommands) windows() (opened, closed int) {
	for _, c := range r.all() {
		if !strings.HasPrefix(c.argv, "nft ") || !strings.Contains(c.stdin, compiler.CutForwardChain) || strings.Contains(c.stdin, `"generation"`) {
			continue
		}
		if strings.Contains(c.stdin, `"reject"`) {
			opened++
		} else if strings.Contains(c.stdin, `"flush"`) && !strings.Contains(c.stdin, `"add":{"table"`) && !strings.Contains(c.stdin, `"table":{"family"`) {
			closed++
		}
	}
	return
}

// deletions returns the arguments of every `conntrack -D`.
func (r *kernelCommands) deletions() []string {
	var out []string
	for _, c := range r.all() {
		if strings.HasPrefix(c.argv, "conntrack -D") {
			out = append(out, strings.TrimPrefix(c.argv, "conntrack -D "))
		}
	}
	return out
}

func (h *harness) wait(cond func() bool, what string) {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timeout: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// accessChain returns the rules of a chain as text.
func (h *harness) chainRules(name string) []string {
	h.t.Helper()
	st, err := apply.ReadState(context.Background(), apply.Local{E: h.ex}, "", apply.Want{})
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, r := range st.Nft.Rules(name) {
		out = append(out, fmt.Sprint(r.Expr))
	}
	return out
}

func TestARuleOverlayIsAppliedStandsBeforeTheConfiguredRulesAndVerifies(t *testing.T) {
	h := newHarness(t)
	h.start()
	ch, cancel := h.e.Subscribe()
	defer cancel()
	h.mustApply(h.revision(withConfigRules(map[string]model.AccessRule{
		cfgRuleA: configRule("no-dot", "Lab", "tcp", []int{853}, model.AccessActionReject, false),
	}, cfgRuleA)))
	if s := h.e.Snapshot(); s.Access == nil || len(s.Access.Rules) != 1 || s.Access.Rules[0].Key != "config:"+cfgRuleA {
		t.Fatalf("access %+v", s.Access)
	}

	res := h.mustPut(alice, ruleIoT53)
	if !res.Created || res.Overlay.Kind != model.OverlayKindRule {
		t.Fatalf("%+v", res.Overlay)
	}
	s := h.e.Snapshot()
	if s.Applied == nil || s.Applied.Generation < res.Generation {
		t.Fatalf("answered with generation %d before the apply %+v", res.Generation, s.Applied)
	}
	// the overlay rule is first, the configured rule behind it, and the kernel has both in that order
	if len(s.Access.Rules) != 2 || s.Access.Rules[0].Key != "overlay:"+res.Overlay.Id.String() || s.Access.Rules[1].Key != "config:"+cfgRuleA {
		t.Fatalf("order %+v", s.Access.Rules)
	}
	if got := h.chainRules(compiler.AccessForwardChain); len(got) != 2 || !strings.Contains(got[0], "drop") || !strings.Contains(got[1], "reject") {
		t.Errorf("access_forward: %v", got)
	}
	if got := h.chainRules(compiler.AccessInputChain); len(got) != 2 {
		t.Errorf("access_input: %v", got)
	}
	// the counters of both rules exist
	counters, err := h.e.ReadCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Access.Rules {
		if _, ok := counters[r.Counter]; !ok {
			t.Errorf("no counter %s for %s", r.Counter, r.Key)
		}
	}
	h.verifyKernelWithOverlays()
	if got := collect(ch, engine.EventOverlayCreated); len(got) != 1 || got[0].Data["kind"] != "rule" {
		t.Errorf("events %+v", got)
	}
}

func TestTheTTLAndTheLeaseOfARuleOverlayRemoveItsRuleAndItsCounter(t *testing.T) {
	h := startedWithRevision(t)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	ttl := h.mustPut(alice, ruleIoT53+"\nttl: 30s")
	lease := h.mustPut(bob, "target: {network: Lab}\nrule: {protocol: tcp, ports: [853], action: reject}\nlease: 10s")
	if n := len(h.e.Snapshot().Access.Rules); n != 2 {
		t.Fatalf("%d rules", n)
	}
	counter := func(id uuid.UUID) string {
		for _, r := range h.e.Snapshot().Access.Rules {
			if r.Key == "overlay:"+id.String() {
				return r.Counter
			}
		}
		return ""
	}
	ttlCounter, leaseCounter := counter(ttl.Overlay.Id), counter(lease.Overlay.Id)
	if ttlCounter == "" || leaseCounter == "" {
		t.Fatal("rules not found")
	}

	// the lease runs out without a renewal: its rule goes
	h.clk.Advance(11 * time.Second)
	h.waitOverlays(1)
	s := h.barrier()
	if len(s.Access.Rules) != 1 || s.Access.Rules[0].Key != "overlay:"+ttl.Overlay.Id.String() {
		t.Fatalf("%+v", s.Access.Rules)
	}
	if _, ok := h.k.Counter(leaseCounter); ok {
		t.Error("the counter of the expired rule is still in the kernel")
	}
	if got := collect(ch, engine.EventOverlayExpired); len(got) != 1 || got[0].Data["reason"] != "lease" {
		t.Errorf("events %+v", got)
	}
	// the TTL: the rule goes, and with the last rule the chains and the counter
	h.clk.Advance(30 * time.Second)
	h.waitOverlays(0)
	s = h.barrier()
	if s.Access != nil {
		t.Errorf("rules remain: %+v", s.Access.Rules)
	}
	if _, ok := h.k.Counter(ttlCounter); ok {
		t.Error("the counter of the expired rule is still in the kernel")
	}
	if got := h.chainRules(compiler.AccessForwardChain); len(got) != 0 {
		t.Errorf("access_forward: %v", got)
	}
	h.verifyKernelWithOverlays()
}

func TestReplacingARuleOverlayKeepsItsIdItsPlaceInTheKeyAndItsCounter(t *testing.T) {
	h := startedWithRevision(t)
	first := h.mustPut(alice, ruleIoT53)
	r0 := h.e.Snapshot().Access.Rules[0]
	h.k.BumpCounter(r0.Counter, 41)
	h.clk.Advance(time.Second)
	second := h.mustPut(alice, "target: {network: IoT}\nrule: {protocol: udp, ports: [53], action: reject}")
	if second.Created || second.Overlay.Id != first.Overlay.Id {
		t.Fatalf("a replacement keeps the id: %+v", second)
	}
	s := h.e.Snapshot()
	if len(s.Access.Rules) != 1 || s.Access.Rules[0].Action != "reject" || s.Access.Rules[0].Counter != r0.Counter {
		t.Fatalf("%+v", s.Access.Rules)
	}
	if v, _ := h.k.Counter(r0.Counter); v != 41 {
		t.Errorf("the counter is %d: it must survive a replacement", v)
	}
	// the epoch of the counter did not change either
	if s.RuleEpochs[r0.Key] != h.e.Snapshot().RuleEpochs[r0.Key] || s.RuleEpochs[r0.Key] == 0 {
		t.Errorf("epochs %v", s.RuleEpochs)
	}
	// a new body that selects other traffic is another overlay
	other := h.mustPut(alice, "target: {network: IoT}\nrule: {protocol: udp, ports: [123], action: drop}")
	if !other.Created || other.Overlay.Id == first.Overlay.Id {
		t.Errorf("%+v", other)
	}
	// the newer overlay rule comes first
	rules := h.e.Snapshot().Access.Rules
	if len(rules) != 2 || rules[0].Key != "overlay:"+other.Overlay.Id.String() {
		t.Errorf("order %+v", rules)
	}
	h.verifyKernelWithOverlays()
}

func TestTooManyRulesAreRefusedWithCapacityExceededAndNothingChanges(t *testing.T) {
	h := newHarness(t)
	// the limit this test assumes: 3 rules, overlays included (the production limit is
	// compiler.DefaultRuleLimit on every architecture)
	h.startWith(engine.Config{RuleLimit: 3})
	h.mustApply(h.revision(withConfigRules(map[string]model.AccessRule{
		cfgRuleA: configRule("one", "Lab", "tcp", []int{853}, model.AccessActionReject, false),
	}, cfgRuleA)))
	h.mustPut(alice, ruleIoT53)
	h.mustPut(alice, "target: {network: IoT}\nrule: {protocol: udp, ports: [123], action: drop}")
	gen := h.e.Snapshot().Generation

	_, err := h.put(bob, "target: {network: IoT}\nrule: {protocol: udp, ports: [161], action: drop}")
	var ce *engine.CompileError
	if !errors.As(err, &ce) || len(ce.Problems) == 0 || ce.Problems[0].Code != compiler.CodeCapacityExceeded {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(ce.Problems[0].Message, "limit of 3") {
		t.Errorf("the message does not name the limit: %s", ce.Problems[0].Message)
	}
	if s := h.e.Snapshot(); s.Generation != gen || len(s.Overlays) != 2 || len(s.Access.Rules) != 3 {
		t.Errorf("a refused overlay changed the state: generation %d -> %d, %d overlays", gen, s.Generation, len(s.Overlays))
	}
	// a replacement of an existing rule needs no room
	h.clk.Advance(time.Second)
	h.mustPut(alice, "target: {network: IoT}\nrule: {protocol: udp, ports: [123], action: reject}")
}

// ---- the cut

// conntrackLine is a line of `conntrack -L`.
func conntrackLine(proto, state, src string, sport int, dst string, dport int) string {
	switch proto {
	case "udp":
		return fmt.Sprintf("udp      17 25 src=%s dst=%s sport=%d dport=%d packets=3 bytes=300 src=%s dst=%s sport=%d dport=%d packets=3 bytes=300 mark=0 use=1", src, dst, sport, dport, dst, src, dport, sport)
	case "icmp":
		return fmt.Sprintf("icmp     1 25 src=%s dst=%s type=8 code=0 id=%d packets=3 bytes=252 src=%s dst=%s type=0 code=0 id=%d packets=3 bytes=252 mark=0 use=1", src, dst, sport, dst, src, sport)
	}
	return fmt.Sprintf("tcp      6 431999 %s src=%s dst=%s sport=%d dport=%d packets=6 bytes=412 src=%s dst=%s sport=%d dport=%d packets=4 bytes=500 [ASSURED] mark=0 use=1", state, src, dst, sport, dport, dst, src, dport, sport)
}

func conntrackText(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestACuttingRuleOverlayResetsAndDeletesTheConnectionsItOwnsAndNothingElse(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{CutWindow: -1}) // no pause: the order of the commands is what is checked
	// rule A (configuration) allows ssh of IoT: its connection belongs to it, a cutting rule behind it
	// leaves it alone
	h.mustApply(h.revision(withConfigRules(map[string]model.AccessRule{
		cfgRuleA: configRule("keep-ssh", "IoT", "tcp", []int{22}, model.AccessActionAllow, false),
	}, cfgRuleA)))
	h.k.SetConntrack(conntrackText(
		conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40001, "203.0.113.10", 8883),  // owned by the new rule
		conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40002, "203.0.113.10", 22),    // owned by the allow rule
		conntrackLine("tcp", "SYN_SENT", conntrackIP, 40003, "203.0.113.10", 8883),     // owned, not established: deleted, not reset
		conntrackLine("tcp", "TIME_WAIT", conntrackIP, 40004, "203.0.113.10", 8883),    // over already
		conntrackLine("tcp", "ESTABLISHED", "10.20.0.10", 40005, "203.0.113.10", 8883), // Lab: not selected
		conntrackLine("udp", "", conntrackIP, 5000, "203.0.113.10", 8883),              // udp on the same port: not tcp
		conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40006, "203.0.113.10", 443),   // other port
	))
	ch, cancel := h.e.Subscribe()
	defer cancel()
	rec := h.recordCommands()

	res := h.mustPut(alice, ruleIoTCut)
	opened, closed := rec.windows()
	if opened != 1 || closed != 1 {
		t.Fatalf("the window opened %d and closed %d times; commands:\n%v", opened, closed, rec.all())
	}
	// the window opens before the connections are deleted and closes in between
	var order []string
	for _, c := range rec.all() {
		switch {
		case strings.HasPrefix(c.argv, "conntrack -D"):
			order = append(order, "delete")
		case strings.HasPrefix(c.argv, "nft ") && strings.Contains(c.stdin, `"reject"`):
			order = append(order, "open")
		case strings.HasPrefix(c.argv, "nft ") && strings.Contains(c.stdin, compiler.CutForwardChain) && strings.Contains(c.stdin, `"flush"`) && !strings.Contains(c.stdin, `"reject"`):
			order = append(order, "close")
		}
	}
	if got := strings.Join(order, ","); !strings.Contains(got, "open,close,delete") {
		t.Errorf("the order of the cut is %s: the window must open, close, then the entries go", got)
	}
	want := []string{
		"-f ipv4 -p tcp --orig-src 10.10.0.10 --orig-dst 203.0.113.10 --orig-port-src 40001 --orig-port-dst 8883",
		"-f ipv4 -p tcp --orig-src 10.10.0.10 --orig-dst 203.0.113.10 --orig-port-src 40003 --orig-port-dst 8883",
	}
	if got := rec.deletions(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("deleted:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	left := h.k.Conntrack()
	for _, keep := range []string{"sport=40002", "sport=40004", "sport=40005", "sport=5000", "sport=40006"} {
		if !strings.Contains(left, keep) {
			t.Errorf("the connection %s was deleted: %s", keep, left)
		}
	}
	// the event tells what the cut did
	var cut *engine.Event
	for _, ev := range collect(ch, engine.EventApplied) {
		if ev.Data["cut_rules"] != nil {
			cut = &ev
		}
	}
	if cut == nil {
		t.Fatal("no applied event with the cut")
	}
	if rules := cut.Data["cut_rules"].([]string); len(rules) != 1 || rules[0] != "overlay:"+res.Overlay.Id.String() || cut.Data["cut_connections"] != 2 {
		t.Errorf("%+v", cut.Data)
	}
	// the window is closed: the cut chains are empty
	if got := h.chainRules(compiler.CutForwardChain); len(got) != 0 {
		t.Errorf("cut_forward: %v", got)
	}
	if got := h.chainRules(compiler.CutInputChain); len(got) != 0 {
		t.Errorf("cut_input: %v", got)
	}
	h.verifyKernelWithOverlays()
}

func TestACutIsNotRepeatedByAnApplyThatDoesNotChangeTheRule(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{CutWindow: -1})
	h.mustApply(h.revision(nil))
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40001, "203.0.113.10", 8883)))
	rec := h.recordCommands()
	h.mustPut(alice, ruleIoTCut)
	if n := len(rec.deletions()); n != 1 {
		t.Fatalf("%d deletions", n)
	}

	// a connection of the same selector that exists now stays: the rule cut once, when it came
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40009, "203.0.113.10", 8883)))
	before := len(rec.all())
	// an unrelated overlay, a new revision and the same rule written again: none of them cuts
	h.mustPut(bob, "target: {network: Lab}\nrule: {protocol: udp, ports: [53], action: drop}")
	h.mustApply(h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") }))
	h.clk.Advance(time.Second)
	h.mustPut(alice, ruleIoTCut)
	opened, _ := rec.windows()
	if n := len(rec.deletions()); n != 1 || opened != 1 {
		t.Errorf("%d deletions and %d windows after the first cut; commands since:\n%v", n, opened, rec.all()[before:])
	}
	if !strings.Contains(h.k.Conntrack(), "sport=40009") {
		t.Error("a connection that the rule owned since before was deleted again")
	}
}

func TestChangingARuleCutsWhatItNowOwnsAndTheFirstApplyOfAProcessCutsNothing(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{CutWindow: -1})
	h.k.SetConntrack(conntrackText(
		conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40001, "203.0.113.10", 8883),
		conntrackLine("udp", "", conntrackIP, 5000, "203.0.113.10", 5353),
		conntrackLine("icmp", "", conntrackIP, 4711, "203.0.113.10", 0)))
	rec := h.recordCommands()
	// the first apply of the process: the engine does not know what the kernel ran before, so a rule
	// that cuts does not cut
	h.mustApply(h.revision(withConfigRules(map[string]model.AccessRule{
		cfgRuleA: configRule("no-mqtt", "IoT", "tcp", []int{8883}, model.AccessActionDrop, true),
	}, cfgRuleA)))
	if n := len(rec.deletions()); n != 0 {
		t.Fatalf("the first apply deleted %d connections", n)
	}
	// the rule is edited: it now selects every protocol of the network, and reject is another effect than
	// drop. The tcp connection was the rule's own before and is cut again with the new effect; the udp flow
	// and the ping are new in its selector.
	h.mustApply(h.revision(withConfigRules(map[string]model.AccessRule{
		cfgRuleA: configRule("no-iot", "IoT", "", nil, model.AccessActionReject, true),
	}, cfgRuleA)))
	got := strings.Join(rec.deletions(), "\n")
	for _, want := range []string{"-p tcp --orig-src 10.10.0.10 --orig-dst 203.0.113.10 --orig-port-src 40001 --orig-port-dst 8883",
		"-p udp --orig-src 10.10.0.10 --orig-dst 203.0.113.10 --orig-port-src 5000 --orig-port-dst 5353",
		"-p icmp --orig-src 10.10.0.10 --orig-dst 203.0.113.10 --icmp-type 8 --icmp-code 0 --icmp-id 4711"} {
		if !strings.Contains(got, want) {
			t.Errorf("not deleted: %s\nall: %s", want, got)
		}
	}
	if opened, _ := rec.windows(); opened != 1 {
		t.Errorf("%d windows", opened)
	}
}

func TestAFailureOfTheCutDoesNotFailTheApplyAndTheWindowIsClosed(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{CutWindow: -1})
	h.mustApply(h.revision(nil))
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40001, "203.0.113.10", 8883)))
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "conntrack" && argv[1] == "-D" {
			return &executor.Result{Exit: 1, Stderr: "conntrack v1.4.8 (conntrack-tools): Operation failed: Operation not permitted\n"}
		}
		return nil
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()
	res, err := h.put(alice, ruleIoTCut)
	if err != nil {
		t.Fatalf("a failed cut fails the write: %v", err)
	}
	if got := h.chainRules(compiler.CutForwardChain); len(got) != 0 {
		t.Errorf("the window is still open: %v", got)
	}
	var seen bool
	for _, ev := range collect(ch, engine.EventApplied) {
		if msg, _ := ev.Data["cut_error"].(string); strings.Contains(msg, "delete the tracked connections") {
			seen = true
		}
	}
	if !seen {
		t.Error("the applied event does not say that the cut failed")
	}
	if s := h.e.Snapshot(); len(s.Access.Rules) != 1 || s.Access.Rules[0].Key != "overlay:"+res.Overlay.Id.String() {
		t.Errorf("the rule is not in force: %+v", s.Access)
	}
}

// closingTransaction is the nft command that empties the cut chains: flushes only.
func closingTransaction(argv []string, stdin string) bool {
	return len(argv) > 0 && argv[0] == "nft" && strings.Contains(stdin, compiler.CutForwardChain) &&
		strings.Contains(stdin, `"flush"`) && !strings.Contains(stdin, `"add"`)
}

// putAdvancing runs a write while the test moves the fake clock on, for the pauses between the retries.
func (h *harness) putAdvancing(who model.Owner, body string) error {
	h.t.Helper()
	done := make(chan error, 1)
	go func() { _, err := h.put(who, body); done <- err }()
	for {
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Millisecond):
			h.clk.Advance(engine.CloseRetryDelay)
		}
	}
}

func TestAFailedCloseOfTheCutWindowIsRetriedBeforeItIsReported(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{CutWindow: -1})
	h.mustApply(h.revision(nil))
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40001, "203.0.113.10", 8883)))
	var failed int
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if closingTransaction(argv, stdin) && failed < engine.CloseAttempts-1 {
			failed++
			return &executor.Result{Exit: 1, Stderr: "netlink: Error: Could not process rule: Device or resource busy\n"}
		}
		return nil
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()
	if err := h.putAdvancing(alice, ruleIoTCut); err != nil {
		t.Fatal(err)
	}
	if failed != engine.CloseAttempts-1 {
		t.Fatalf("the close failed %d times", failed)
	}
	if got := h.chainRules(compiler.CutForwardChain); len(got) != 0 {
		t.Errorf("the window is still open after the retries: %v", got)
	}
	for _, ev := range collect(ch, engine.EventApplied) {
		if msg, _ := ev.Data["cut_error"].(string); msg != "" {
			t.Errorf("a close that succeeded on a retry is reported: %s", msg)
		}
	}
	if s := h.e.Snapshot(); s.CutWindowError != "" {
		t.Errorf("the apply status says the window is open: %s", s.CutWindowError)
	}
}

// A window that cannot be closed is reported in the applied event and in the apply status, and the next
// apply closes it: the incremental identity update retries the close, a full apply flushes the chains.
func TestAWindowThatStaysOpenIsReportedAndClosedByTheNextApply(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{CutWindow: -1})
	h.mustApply(h.revision(nil))
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40001, "203.0.113.10", 8883)))
	broken := true
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if broken && closingTransaction(argv, stdin) {
			return &executor.Result{Exit: 1, Stderr: "netlink: Error: Could not process rule: Device or resource busy\n"}
		}
		return nil
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()
	if err := h.putAdvancing(alice, ruleIoTCut); err != nil {
		t.Fatalf("a window that stays open fails the write: %v", err)
	}
	if got := h.chainRules(compiler.CutForwardChain); len(got) == 0 {
		t.Fatal("the failure injection did not keep the window open")
	}
	var seen bool
	for _, ev := range collect(ch, engine.EventApplied) {
		if msg, _ := ev.Data["cut_error"].(string); strings.Contains(msg, "close the cut window") {
			seen = true
		}
	}
	if !seen {
		t.Error("the applied event does not say that the window stays open")
	}
	if s := h.e.Snapshot(); !strings.Contains(s.CutWindowError, "close the cut window") {
		t.Errorf("the apply status does not say that the window is open: %q", s.CutWindowError)
	}

	// the cause is gone; the next full apply flushes the chains and the status clears
	broken = false
	h.mustApply(h.revision(func(c *model.Configuration) { c.Uplink.Gateway = ptr("203.0.113.20") }))
	if got := h.chainRules(compiler.CutForwardChain); len(got) != 0 {
		t.Errorf("the next apply left the window open: %v", got)
	}
	if s := h.e.Snapshot(); s.CutWindowError != "" {
		t.Errorf("the apply status still says the window is open: %q", s.CutWindowError)
	}
}

func TestTheCutWindowStaysOpenForItsLengthOnTheClock(t *testing.T) {
	h := newHarness(t)
	h.startWith(engine.Config{}) // the default window of half a second
	h.mustApply(h.revision(nil))
	h.k.SetConntrack(conntrackText(conntrackLine("tcp", "ESTABLISHED", conntrackIP, 40001, "203.0.113.10", 8883)))
	done := make(chan error, 1)
	go func() { _, err := h.put(alice, ruleIoTCut); done <- err }()

	// the window is open while the clock stands still
	h.wait(func() bool { return len(h.chainRules(compiler.CutForwardChain)) > 0 }, "the window to open")
	select {
	case err := <-done:
		t.Fatalf("the write returned (%v) while the window was open", err)
	case <-time.After(100 * time.Millisecond):
	}
	h.clk.Advance(engine.DefaultCutWindow - time.Millisecond)
	if got := h.chainRules(compiler.CutForwardChain); len(got) == 0 {
		t.Error("the window closed before its time")
	}
	h.clk.Advance(time.Millisecond)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := h.chainRules(compiler.CutForwardChain); len(got) != 0 {
		t.Errorf("the window is still open: %v", got)
	}
}

// ---- explain

func (h *harness) explain(q engine.ExplainQuery) *engine.Explanation {
	h.t.Helper()
	ex, err := h.e.Explain(context.Background(), q)
	if err != nil {
		h.t.Fatal(err)
	}
	return ex
}

func TestExplainNamesTheRuleThatDecidesAndFollowsOverlaysAndOrder(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(withConfigRules(map[string]model.AccessRule{
		cfgRuleA: configRule("no-dns", "IoT", "udp", []int{53}, model.AccessActionDrop, false),
		cfgRuleB: configRule("iot-lab-ssh", "IoT", "tcp", []int{22}, model.AccessActionAllow, false),
	}, cfgRuleA, cfgRuleB)))
	src := netip.MustParseAddr("10.10.0.77")

	// a drop rule on UDP 53 decides for the gateway's DNS address and for the service namespace's
	// address alike (P2-M8a-07: explain uses the rules now)
	for _, dst := range []string{"10.10.0.1", "169.254.100.2", "203.0.113.50"} {
		ex := h.explain(engine.ExplainQuery{Src: src, Dst: dst, Protocol: "udp", Port: 53})
		if ex.Access.Verdict != "drop" || ex.Access.Layer != "config_rule" || ex.Access.Rule != cfgRuleA {
			t.Errorf("%s: %+v", dst, ex.Access)
		}
	}
	// the same traffic from another network is judged without the rule
	ex := h.explain(engine.ExplainQuery{Src: netip.MustParseAddr("10.20.0.5"), Dst: "10.20.0.1", Protocol: "udp", Port: 53})
	if ex.Access.Layer != "gateway_protection" || ex.Access.Verdict != "allow" || ex.Access.Rule != "" {
		t.Errorf("%+v", ex.Access)
	}
	// an allow rule is an exception to the matrix: IoT does not reach Lab, except port 22
	ex = h.explain(engine.ExplainQuery{Src: src, Dst: "10.20.0.5", Protocol: "tcp", Port: 22})
	if ex.Access.Verdict != "allow" || ex.Access.Layer != "config_rule" || ex.Access.Rule != cfgRuleB {
		t.Errorf("%+v", ex.Access)
	}
	ex = h.explain(engine.ExplainQuery{Src: src, Dst: "10.20.0.5", Protocol: "tcp", Port: 80})
	if ex.Access.Verdict != "drop" || ex.Access.Layer != "access_matrix" {
		t.Errorf("%+v", ex.Access)
	}

	// an overlay rule comes first: reject beats the configured drop, and its id is named
	res := h.mustPut(alice, "target: {network: IoT}\nrule: {protocol: udp, ports: [53], action: reject}")
	ex = h.explain(engine.ExplainQuery{Src: src, Dst: "10.10.0.1", Protocol: "udp", Port: 53})
	if ex.Access.Verdict != "reject" || ex.Access.Layer != "overlay_rule" || ex.Access.Rule != res.Overlay.Id.String() {
		t.Errorf("%+v", ex.Access)
	}
	// and when the overlay goes, the configured rule decides again
	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	ex = h.explain(engine.ExplainQuery{Src: src, Dst: "10.10.0.1", Protocol: "udp", Port: 53})
	if ex.Access.Verdict != "drop" || ex.Access.Layer != "config_rule" {
		t.Errorf("%+v", ex.Access)
	}
}

func TestNoRuleChangesWhatTheGatewayAnswersOnItsControlPlane(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(withConfigRules(map[string]model.AccessRule{
		cfgRuleA: configRule("everything", "IoT", "", nil, model.AccessActionAllow, false),
	}, cfgRuleA)))
	ui := h.e.Snapshot().Management.UIPort
	if ui == 0 {
		t.Fatal("the snapshot has no UI port")
	}
	// an allow rule does not open the UI for a test network, nor any other port of the gateway
	ex := h.explain(engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.77"), Dst: "10.10.0.1", Protocol: "tcp", Port: ui})
	if ex.Access.Verdict != "drop" || ex.Access.Layer != "gateway_protection" {
		t.Errorf("%+v", ex.Access)
	}
	ex = h.explain(engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.77"), Dst: "10.10.0.1", Protocol: "tcp", Port: 179})
	if ex.Access.Verdict != "drop" || ex.Access.Layer != "gateway_protection" || !strings.Contains(ex.Access.Reason, "allow") {
		t.Errorf("%+v", ex.Access)
	}
	// and a drop-everything rule leaves the management sources alone
	h.mustPut(alice, "target: {global: true}\nrule: {action: drop}")
	ex = h.explain(engine.ExplainQuery{Src: netip.MustParseAddr("192.168.56.2"), Dst: "10.10.0.1", Protocol: "tcp", Port: ui})
	if ex.Access.Verdict != "allow" || ex.Access.Layer != "gateway_protection" {
		t.Errorf("%+v", ex.Access)
	}
}

func TestARuleOverlayAndAFaultCombineWithTheRuleFirst(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, iotLatency)
	rule := h.mustPut(alice, "target: {network: IoT}\nrule: {protocol: tcp, ports: [8883], action: drop}")
	src := netip.MustParseAddr("10.10.0.77")
	// the fault is still resolved for the traffic (the explanation shows both), the verdict refuses it
	ex := h.explain(engine.ExplainQuery{Src: src, Dst: "203.0.113.10", Protocol: "tcp", Port: 8883})
	if ex.Access.Verdict != "drop" || ex.Access.Rule != rule.Overlay.Id.String() || len(ex.Faults) != 1 || ex.Faults[0].Winner == nil {
		t.Errorf("%+v", ex)
	}
	// the other ports are allowed and delayed
	ex = h.explain(engine.ExplainQuery{Src: src, Dst: "203.0.113.10", Protocol: "tcp", Port: 443})
	if ex.Access.Verdict != "allow" || len(ex.Faults) != 1 {
		t.Errorf("%+v", ex)
	}
	// the faults of the kernel are unchanged by the rule: the same id, the same classification
	s := h.e.Snapshot()
	if len(s.Faults) != 1 || s.Access == nil || len(s.Access.Rules) != 1 {
		t.Errorf("faults %+v access %+v", s.Faults, s.Access)
	}
	h.verifyKernelWithOverlays()
}
