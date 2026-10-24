package apply_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
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
	e.k.AddHolder(4711)
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

func TestARestartedHolderLeavesAnOldNamespaceThatIsReplaced(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	holder := func(c *model.Configuration, in *compiler.Input) { in.ServiceNS = "cgsvc"; in.ServiceHolderPID = 4711 }
	e.k.AddHolder(4711)
	tg := e.compile(holder)
	e.apply(tg)
	old, _ := e.k.NetnsInode("/run/netns/cgsvc")
	if old == 0 {
		t.Fatal("no namespace")
	}
	// the same holder again: nothing to do for the namespace
	if res := e.apply(e.compile(holder)); strings.Contains(strings.Join(res.Plan.Summary, "\n"), "service namespace") {
		t.Fatalf("a holder that did not change is planned again:\n%s", strings.Join(res.Plan.Summary, "\n"))
	}
	// the holder container restarted: same PID in the test, a new network namespace. The name still
	// points at the old one, which stays alive as long as something holds it.
	e.k.AddHolder(4711)
	st, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(e.compile(holder)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range apply.Verify(e.compile(holder), st) {
		found = found || m.Subsystem == "service"
	}
	if !found {
		t.Fatal("verify does not notice the holder that changed")
	}
	res := e.apply(e.compile(holder))
	if !strings.Contains(strings.Join(res.Plan.Summary, "\n"), "is not the one of its holder") {
		t.Errorf("%s", strings.Join(res.Plan.Summary, "\n"))
	}
	now, ok := e.k.NetnsInode("/run/netns/cgsvc")
	want, _ := e.k.NetnsInode("/proc/4711/ns/net")
	if !ok || now == old || now != want {
		t.Errorf("the namespace was not replaced by the holder's: old %d, now %d, holder %d", old, now, want)
	}
	// the pair leads into the new namespace
	st, _ = apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(e.compile(holder)))
	if st.Service == nil || !st.Service.HolderMatches || len(st.Service.PeerAddrs) != 1 {
		t.Errorf("%+v", st.Service)
	}
}

// plan §3.3, "Redirect inside": the namespace holds the chain that redirects the connections that arrive for other
// addresses to the TLS responder, and a namespace that lacks it (an earlier version made it, or its holder was
// replaced and the pair made anew) gets it with the next apply.
func TestTheServiceNamespaceRedirectsOtherAddressesToTheTLSResponder(t *testing.T) {
	e := newEnv(t)
	e.k.ServiceNamespace("cgsvc")
	tg := e.compile(withService)
	e.apply(tg)
	list := func() string {
		r, err := e.k.InService("cgsvc").Run(context.Background(), executor.Command{Tool: executor.ToolNft, Args: []string{"-j", "list", "table", "inet", "chaosgw"}})
		if err != nil || r.Exit != 0 {
			t.Fatalf("%v %+v", err, r)
		}
		return r.Stdout
	}
	got := list()
	for _, want := range []string{`"name":"svc_redirect"`, `"hook":"prerouting"`, `"type":"nat"`, `"redirect":{"port":9443}`, `"169.254.100.2"`, `"svc1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the namespace's table lacks %s:\n%s", want, got)
		}
	}
	st, err := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if err != nil || st.Service == nil || !st.Service.Redirect {
		t.Fatalf("the state does not show the redirect: %+v %v", st.Service, err)
	}

	// a namespace without the chain does not verify and is healed by the next apply
	e.k.InService("cgsvc").Reset()
	st, err = apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if err != nil || st.Service.Redirect {
		t.Fatalf("%+v %v", st.Service, err)
	}
	found := false
	for _, m := range apply.Verify(tg, st) {
		found = found || m.Subsystem == "service"
	}
	if !found {
		t.Error("verify does not notice the missing redirect")
	}
	res := e.apply(e.compile(withService))
	if !strings.Contains(strings.Join(res.Plan.Summary, "\n"), "no redirect to the TLS responder") {
		t.Errorf("the redirect is not made again:\n%s", strings.Join(res.Plan.Summary, "\n"))
	}
	if !strings.Contains(list(), `"redirect":{"port":9443}`) {
		t.Error("the redirect is still missing")
	}
	// and an apply that finds it there does not touch the namespace
	if res := e.apply(e.compile(withService)); strings.Contains(strings.Join(res.Plan.Summary, "\n"), "service namespace") {
		t.Errorf("the namespace is planned again:\n%s", strings.Join(res.Plan.Summary, "\n"))
	}
}
