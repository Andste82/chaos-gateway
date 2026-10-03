package apply_test

import (
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
