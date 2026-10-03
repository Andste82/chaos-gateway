package apply_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
)

func diffOf(t *testing.T, e *env, targets ...*compiler.Target) apply.LinuxDiff {
	t.Helper()
	tg := e.compile()
	if len(targets) > 0 {
		tg = targets[0]
	}
	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.Want{Sysctls: tg.Sysctls, Offloads: tg.Offloads})
	if err != nil {
		t.Fatal(err)
	}
	return apply.Diff(tg, s)
}

func TestTheLinuxDiffOfTheFirstApplyShowsEverythingAsNew(t *testing.T) {
	e := newEnv(t)
	d := diffOf(t, e)
	for _, want := range []string{"+chain input type filter hook input prio 0 policy accept", "+counter input_drop", "+set ifs_test_", "+  element br-iot"} {
		if !strings.Contains(d.Nftables, want) {
			t.Errorf("nftables diff lacks %q:\n%s", want, d.Nftables)
		}
	}
	// a new rule is shown with its expression
	if !strings.Contains(d.Nftables, `"masquerade"`) || !strings.Contains(d.Nftables, "+  rule ") {
		t.Errorf("new rules show their expression:\n%s", d.Nftables)
	}
	for _, want := range []string{"+bridge br-iot up", "+  address 10.10.0.1/24", "+  port lan0", "+route table 100 default via 203.0.113.10 dev wan0", "+rule 1000 iif br-iot lookup 100", "+offloads off wan0", "+docker-user accept br-iot"} {
		if !strings.Contains(d.Routes, want) {
			t.Errorf("routes diff lacks %q:\n%s", want, d.Routes)
		}
	}
	if strings.Contains(d.Nftables, "generation") {
		t.Error("the generation chain changes at every apply and stays out of the diff")
	}
}

func TestTheLinuxDiffIsEmptyAfterTheApplyAndShowsOnlyTheChange(t *testing.T) {
	e := newEnv(t)
	e.apply(e.compile())
	if d := diffOf(t, e); d.Nftables != "" || d.Routes != "" {
		t.Fatalf("nothing changed, but:\n%s\n%s", d.Nftables, d.Routes)
	}
	e.cfg.Uplink.Gateway = ptr("203.0.113.20")
	d := diffOf(t, e)
	if d.Nftables != "" {
		t.Errorf("a new gateway does not touch nftables:\n%s", d.Nftables)
	}
	if !strings.Contains(d.Routes, "-route table 100 default via 203.0.113.10 dev wan0") || !strings.Contains(d.Routes, "+route table 100 default via 203.0.113.20 dev wan0") {
		t.Errorf("routes diff:\n%s", d.Routes)
	}
	// something unrelated is far from the change and not shown
	if strings.Contains(d.Routes, "bridge br-lab") {
		t.Errorf("the diff shows unchanged context from afar:\n%s", d.Routes)
	}
	// a manipulated kernel shows as a diff against the target
	e.apply(e.compile())
	e.k.RemoveSetElement(setOf(e, "mgmt_src"))
	d = diffOf(t, e)
	if !strings.Contains(d.Nftables, "+  element 192.168.56.0/24") {
		t.Errorf("a removed element shows as an addition to make:\n%s", d.Nftables)
	}
}

func setOf(e *env, prefix string) string {
	for _, s := range e.compile().Nft.Sets {
		if strings.HasPrefix(s.Name, prefix) {
			return s.Name
		}
	}
	return ""
}
