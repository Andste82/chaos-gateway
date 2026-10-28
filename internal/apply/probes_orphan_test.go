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

func TestANamespaceWhosePairIsGoneIsSeenAndRemovedOrMadeAgain(t *testing.T) {
	e := newProbeEnv(t)
	tg := e.compile(withProbes(probeA))
	e.apply(tg)
	dropPair := func() {
		_, _ = e.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"link", "delete", "dev", probeAif, "type", "veth"}})
	}
	// the pair goes (a hand, or a delete that stopped between its two steps), the namespace stays
	dropPair()
	ps := e.probeState(tg)
	if ps.Name != probeAns || !ps.NoPair || ps.HostIf != probeAif {
		t.Fatalf("the namespace is not listed without its pair: %+v", ps)
	}
	st, _ := apply.ReadState(context.Background(), e.exec(), "", apply.WantOf(tg))
	if mm := apply.Verify(tg, st); len(mm) != 1 || !strings.Contains(mm[0].Detail, "its pair is gone") {
		t.Fatalf("verify: %v", mm)
	}
	// wanted: the pair is made again
	if res := e.apply(e.compile(withProbes(probeA))); !strings.Contains(summary(res), "its pair is gone") {
		t.Errorf("the plan does not say why:\n%s", summary(res))
	}
	if ps := e.probeState(tg); ps.NoPair || ps.Master != "br-iot" || !ps.PeerUp {
		t.Errorf("%+v", ps)
	}
	// not wanted any more: the namespace goes
	dropPair()
	res := e.apply(e.compile(func(c *model.Configuration, in *compiler.Input) { c.Probes = nil }))
	if !strings.Contains(summary(res), "probe delete cgp-3a1f7c52") {
		t.Errorf("the plan does not delete it:\n%s", summary(res))
	}
	if st, _ := apply.ReadState(context.Background(), e.exec(), "", apply.Want{}); len(st.Probes) != 0 || e.k.InService(probeAns) != nil {
		t.Errorf("%+v", st.Probes)
	}
}
