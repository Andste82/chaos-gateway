package apply_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func withBGP(e *wgEnv) {
	asn := int64(65001)
	link := linkID
	e.cfg.Routing = &model.Routing{Asn: &asn, Protocols: &map[string]model.RoutingProtocol{
		"11111111-2222-4333-8444-555555555555": {
			Name: "site-b", Type: model.RoutingProtocolTypeBgp, Link: link,
			Bgp: &model.BgpSettings{NeighborAsn: 65002},
		},
	}}
}

func birdOps(res *apply.Result) (n int) {
	for _, op := range res.Plan.Ops {
		if _, ok := op.(*executor.Bird); ok {
			n++
		}
	}
	return n
}

func TestBirdIsConfiguredVerifiedAndLeftAloneWhenUnchanged(t *testing.T) {
	e := newWGEnv(t)
	withBGP(e)
	res := e.apply()
	if birdOps(res) != 1 {
		t.Fatalf("plan %v", res.Plan.Summary)
	}
	if res.After.Bird == nil || !res.After.Bird.Running {
		t.Fatalf("%+v", res.After.Bird)
	}
	res = e.apply()
	if birdOps(res) != 0 {
		t.Errorf("an unchanged configuration reconfigured BIRD: %v", res.Plan.Summary)
	}
}

func TestSwitchingRoutingOffWithdrawsTheProtocols(t *testing.T) {
	e := newWGEnv(t)
	withBGP(e)
	e.apply()
	e.cfg.Routing = nil
	res := e.apply()
	if birdOps(res) != 1 || !strings.Contains(strings.Join(res.Plan.Summary, "\n"), "withdraw") {
		t.Fatalf("plan %v", res.Plan.Summary)
	}
	if birdOps(e.apply()) != 0 {
		t.Error("the idle configuration is applied again and again")
	}
}

// M4c-14 test: when `birdc configure` fails, the executor keeps the previous configuration file in
// place (it already did; this is the first test of it at the apply level), so a retry with the same
// target finds nothing to synchronize once the injected failure is lifted.
func TestAFailedApplyRestoresThePreviousBirdConfiguration(t *testing.T) {
	e := newWGEnv(t)
	withBGP(e)
	before := e.compile()
	e.apply()
	beforeHash := apply.TextHash(before.Bird.Text)

	asn := int64(65099)
	e.cfg.Routing.Asn = &asn
	next := e.compile()
	if next.Bird.Text == before.Bird.Text {
		t.Fatal("the change does not touch the bird configuration")
	}
	e.k.Fail = func(argv []string, _ string) *executor.Result {
		if len(argv) > 0 && argv[0] == "birdc" && strings.Contains(strings.Join(argv, " "), "configure") {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	if _, err := apply.Apply(context.Background(), e.exec(), "", next); err == nil {
		t.Fatal("the injected failure did not surface")
	}
	e.k.Fail = nil

	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(next))
	if err != nil {
		t.Fatal(err)
	}
	if s.Bird == nil || s.Bird.ConfigHash != beforeHash {
		t.Fatalf("the running configuration was not restored: %+v", s.Bird)
	}

	res := e.apply()
	if birdOps(res) != 1 {
		t.Errorf("the restored configuration still needs the pending change applied: %v", res.Plan.Summary)
	}
}

// M4c-15 test: with routing switched off, verify checks that the running instance is idle, not
// just that the target has no BIRD configuration.
func TestVerifyDetectsABirdConfigurationLeftRunningAfterRoutingIsSwitchedOff(t *testing.T) {
	e := newWGEnv(t)
	withBGP(e)
	e.apply()
	tgOn := e.compile()

	e.cfg.Routing = nil
	tgOff := e.compile()
	if tgOff.Bird != nil {
		t.Fatal("routing is off: the target must have no BIRD configuration")
	}

	s, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tgOn))
	if err != nil {
		t.Fatal(err)
	}
	mm := apply.Verify(tgOff, s)
	found := false
	for _, m := range mm {
		found = found || strings.Contains(m.String(), "switched off")
	}
	if !found {
		t.Fatalf("verify did not report the leftover BIRD configuration: %v", mm)
	}
}

func TestRoutingWithoutABirdDirectoryFailsInPlan(t *testing.T) {
	e := newWGEnv(t)
	ex, err := executor.New(e.k, executor.WithKeys(func(id string) (string, string, error) {
		kk, err := e.sec.WireGuard(id)
		return kk.PrivateKey, kk.PresharedKey, err
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	e.ex = ex
	withBGP(e)
	tg := e.compile()
	_, err = apply.Preview(t.Context(), apply.Local{E: ex}, "", tg)
	if err == nil || !strings.Contains(err.Error(), "--bird-dir") {
		t.Fatalf("%v", err)
	}
}
