package apply_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// withRules adds access rules to the configuration of the environment: (name, source network,
// protocol, ports, action, cut).
func withRules(rules ...model.AccessRule) func(*model.Configuration, *compiler.Input) {
	return func(cfg *model.Configuration, _ *compiler.Input) {
		m := map[string]model.AccessRule{}
		var order []uuid.UUID
		for i, r := range rules {
			id := uuid.MustParse("a1000000-0000-4000-8000-00000000000" + string(rune('1'+i)))
			m[id.String()] = r
			order = append(order, id)
		}
		cfg.AccessRules, cfg.AccessRuleOrder = &m, &order
	}
}

func rule(name, network, proto string, ports []int, action model.AccessAction, cut bool) model.AccessRule {
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

// The rules apply and verify; their named counters survive every apply and go with the rule, the
// source sets follow the configuration, and a cut window is a transaction the executor accepts
// and the kernel simulator runs.
func TestAccessRulesApplyVerifyAndKeepTheirCounters(t *testing.T) {
	e := newEnv(t)
	rules := withRules(
		rule("no-dot", "IoT", "tcp", []int{853}, model.AccessActionReject, true),
		rule("no-dns", "Lab", "udp", []int{53}, model.AccessActionDrop, false),
	)
	tg := e.compile(rules)
	if tg.HasErrors() || tg.Access == nil || len(tg.Access.Rules) != 2 {
		t.Fatalf("%+v", tg.Problems)
	}
	res := e.apply(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("mismatches: %v", res.Mismatches)
	}
	for _, name := range []string{compiler.AccessForwardChain, compiler.AccessInputChain, compiler.CutForwardChain, compiler.CutInputChain} {
		if res.After.Nft.Chain(name) == nil {
			t.Errorf("no chain %s after the apply", name)
		}
	}
	counter := tg.Access.Rules[0].Counter
	if _, ok := e.k.Counter(counter); !ok {
		t.Fatalf("no counter %s", counter)
	}
	if _, ok := e.k.Counter(compiler.AntiLockoutCounter); !ok {
		t.Fatal("no anti-lockout counter")
	}
	e.k.BumpCounter(counter, 17)

	// a re-apply keeps the counter of the rule
	e.apply(e.compile(rules))
	if v, _ := e.k.Counter(counter); v != 17 {
		t.Errorf("counter %d after a re-apply: it must survive and stay monotonic", v)
	}

	// the window of "also cut existing connections" runs through the executor like any plan
	tx, err := tg.Access.CutTransaction([]string{tg.Access.Rules[0].Key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ex.Do(context.Background(), &executor.NftApply{Ruleset: tx}); err != nil {
		t.Fatalf("the executor refuses the cut window: %v", err)
	}
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.Nft.Rules(compiler.CutForwardChain)); n != 1 {
		t.Errorf("%d rules in the cut chain during the window", n)
	}
	closed, _ := tg.Access.CutTransaction(nil)
	if _, err := e.ex.Do(context.Background(), &executor.NftApply{Ruleset: closed}); err != nil {
		t.Fatal(err)
	}
	s, _ = apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if n := len(s.Nft.Rules(compiler.CutForwardChain)) + len(s.Nft.Rules(compiler.CutInputChain)); n != 0 {
		t.Errorf("%d rules in the cut chains after the window", n)
	}

	// without rules the chains, the sets and the counters are gone; the anti-lockout counter stays
	e.apply(e.compile(func(cfg *model.Configuration, _ *compiler.Input) { cfg.AccessRules, cfg.AccessRuleOrder = nil, nil }))
	s, _ = apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	for _, name := range []string{compiler.AccessForwardChain, compiler.AccessInputChain, compiler.CutForwardChain, compiler.CutInputChain} {
		if s.Nft.Chain(name) != nil {
			t.Errorf("chain %s is still there without rules", name)
		}
	}
	if _, ok := e.k.Counter(counter); ok {
		t.Error("the counter of a removed rule is still there")
	}
	if _, ok := e.k.Counter(compiler.AntiLockoutCounter); !ok {
		t.Error("the anti-lockout counter must stay")
	}
	for _, o := range s.Nft.Objects {
		if o.Set != nil && strings.HasPrefix(o.Set.Name, "asrc_") {
			t.Errorf("the source set %s is still there without rules", o.Set.Name)
		}
	}
}
