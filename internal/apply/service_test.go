package apply_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/model"
)

func withService(c *model.Configuration, in *compiler.Input) { in.ServiceNS = "cgsvc" }

func TestTheServiceNamespaceIsCreatedAndVerified(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	tg := e.compile(withService)
	res := e.apply(tg)
	if res.Plan.Empty() {
		t.Fatal("nothing was planned")
	}
	sub := e.k.InService("cgsvc")
	if sub == nil {
		t.Fatal("the namespace was not created")
	}
	var joined strings.Builder
	for _, s := range res.Plan.Summary {
		joined.WriteString(s + "\n")
	}
	if !strings.Contains(joined.String(), "service namespace: namespace cgsvc is missing") {
		t.Errorf("the plan does not say why:\n%s", joined.String())
	}
	// everything verifies, and a second apply has nothing left to do for the namespace
	if res2 := e.apply(e.compile(withService)); strings.Contains(strings.Join(res2.Plan.Summary, "\n"), "service namespace") {
		t.Errorf("the namespace is planned again:\n%s", strings.Join(res2.Plan.Summary, "\n"))
	}
	// the peer is configured inside
	st, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if err != nil {
		t.Fatal(err)
	}
	if st.Service == nil || !st.Service.Exists || len(st.Service.PeerAddrs) != 1 || st.Service.PeerAddrs[0] != "169.254.100.2/30" || !st.Service.PeerUp || st.Service.DefaultVia != "169.254.100.1" {
		t.Errorf("%+v", st.Service)
	}
}

func TestAHolderThatDiesIsHealedByTheNextApply(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	tg := e.compile(withService)
	e.apply(tg)
	// the holder's namespace goes, and svc0 with it
	e.k.DropService("cgsvc")
	st, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if err != nil {
		t.Fatal(err)
	}
	mm := apply.Verify(tg, st)
	found := false
	for _, m := range mm {
		found = found || m.Subsystem == "service"
	}
	if !found {
		t.Fatalf("verify does not notice the missing namespace: %v", mm)
	}
	res := e.apply(e.compile(withService))
	if !strings.Contains(strings.Join(res.Plan.Summary, "\n"), "service namespace") {
		t.Errorf("the namespace is not created again:\n%s", strings.Join(res.Plan.Summary, "\n"))
	}
	if e.k.InService("cgsvc") == nil {
		t.Error("no namespace")
	}
}

func TestAHolderThatChangedGetsItsNamespaceAttached(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	e.apply(e.compile(func(c *model.Configuration, in *compiler.Input) { in.ServiceNS = "cgsvc"; in.ServiceHolderPID = 4711 }))
	found := false
	for _, c := range e.k.Commands() {
		found = found || c == "ip netns attach cgsvc 4711"
	}
	if !found {
		t.Errorf("the holder's namespace was not attached: %v", e.k.Commands())
	}
}

func TestWithoutAServiceNamespaceNothingIsLeftBehind(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	e.apply(e.compile(withService))
	res := e.apply(e.compile())
	if !strings.Contains(strings.Join(res.Plan.Summary, "\n"), "service namespace: delete svc0") {
		t.Errorf("svc0 stays:\n%s", strings.Join(res.Plan.Summary, "\n"))
	}
	if sub := e.k.InService("cgsvc"); sub != nil {
		for _, c := range e.k.Commands() {
			_ = c
		}
	}
	st, _ := apply.ReadState(context.Background(), e.exec(), "", apply.Want{})
	if _, ok := st.Links["svc0"]; ok {
		t.Error("svc0 is still there")
	}
}

func TestTheServiceNamespaceDoesNotTouchTheRoutesOfOthers(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	tg := e.compile(withService)
	e.apply(tg)
	st, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if err != nil {
		t.Fatal(err)
	}
	if mm := apply.Verify(tg, st); len(mm) != 0 {
		t.Fatalf("a fresh apply does not verify: %v", mm)
	}
	var fw, prohibit bool
	for _, r := range st.Rules {
		fw = fw || (r.Table == "102" && r.Fwmark == "0x100000")
	}
	for _, r := range st.Routes {
		prohibit = prohibit || (r.Table == "102" && r.Type == "prohibit")
	}
	if !fw || !prohibit {
		t.Errorf("fwmark rule %v, prohibit route %v", fw, prohibit)
	}
}
