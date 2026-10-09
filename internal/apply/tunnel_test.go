package apply_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// The tunnel faults in the apply (M10, plan §2.2.1): the IFB device, its tree, the ingress filters of the uplink that feed it and
// the classes of the direction towards the peer, on the simulated kernel (which does not validate tc semantics: the real
// kernel is in the testbed tests and in the gate TestEveryCompiledTunnelTreeIsAcceptedByTheKernel).

const (
	epRA   = "198.51.100.2:51820"
	epRA2  = "198.51.100.9:40000"
	tunFlt = `{fault: {family: tunnel, tunnel: {client: rA}, upload: {latency: 20ms}, download: {latency: 50ms}}}`
)

type tunEnv struct {
	*wgEnv
	ids   map[string]int
	ends  map[string]netip.AddrPort
	clock *clock.Fake
	ret   *apply.Retirer
}

func newTunEnv(t *testing.T) *tunEnv {
	t.Helper()
	c := clock.NewFake(time.Unix(1_700_000_000, 0))
	x := &tunEnv{wgEnv: newWGEnvFile(t, "tunnels.yaml"), ids: map[string]int{}, clock: c, ret: apply.NewRetirer(c),
		ends: map[string]netip.AddrPort{clientA: netip.MustParseAddrPort(epRA)}}
	return x
}

func (x *tunEnv) overlay(body string) model.Overlay {
	x.t.Helper()
	req, err := domain.DecodeOverlayRequest([]byte(body), domain.FormatYAML)
	if err != nil {
		x.t.Fatalf("%s: %v", body, err)
	}
	n, errs := domain.ValidateOverlay(x.cfg, req)
	if len(errs) != 0 {
		x.t.Fatalf("%s: %v", body, errs)
	}
	o, err := domain.NewOverlay(*n, model.Owner{Type: "user", Id: "test"}, uuid.New(), time.Unix(1_700_000_000, 0))
	if err != nil {
		x.t.Fatal(err)
	}
	return o
}

func (x *tunEnv) compileWith(overlays ...model.Overlay) *compiler.Target {
	x.t.Helper()
	tg := x.wgEnv.env.compile(func(_ *model.Configuration, in *compiler.Input) {
		keys, err := wireguard.InterfaceKeys(x.cfg, x.sec)
		if err != nil {
			x.t.Fatal(err)
		}
		in.Keys, in.Overlays, in.FaultIDs, in.PeerEndpoints, in.ClassLimit = keys, overlays, x.ids, x.ends, 1000
	})
	if tg.HasErrors() {
		x.t.Fatalf("%+v", tg.Problems)
	}
	x.ids = tg.FaultIDs
	return tg
}

func (x *tunEnv) applyRetiring(tg *compiler.Target) *apply.Result {
	x.t.Helper()
	res, err := apply.ApplyWith(context.Background(), x.exec(), "", tg, x.ret)
	if err != nil {
		x.t.Fatalf("apply: %v", err)
	}
	return res
}

func (x *tunEnv) strictVerify(tg *compiler.Target) []apply.Mismatch {
	x.t.Helper()
	s, err := apply.ReadState(context.Background(), x.exec(), "", apply.WantOf(tg))
	if err != nil {
		x.t.Fatal(err)
	}
	return apply.Verify(tg, s)
}

func (x *tunEnv) tree(dev string) *linux.NormTree {
	x.t.Helper()
	out, err := x.exec().Do(context.Background(), &executor.Read{What: executor.ReadTC, Dev: dev})
	if err != nil {
		x.t.Fatal(err)
	}
	var t *linux.NormTree
	if err := json.Unmarshal(out.Data[0], &t); err != nil {
		x.t.Fatal(err)
	}
	if t == nil {
		t = &linux.NormTree{}
	}
	return t
}

func (x *tunEnv) classes(dev string) string {
	var ids []string
	for _, c := range x.tree(dev).Subtree("1:").Classes {
		if c.ID != "1:1" {
			ids = append(ids, c.ID)
		}
	}
	return strings.Join(ids, " ")
}

func (x *tunEnv) ingress() []linux.NormFilter { return x.tree("wan0").Ingress().Filters }

func (x *tunEnv) assigned() string {
	out, err := x.exec().Do(context.Background(), &executor.Read{What: executor.ReadAssigned})
	if err != nil {
		x.t.Fatal(err)
	}
	return string(out.Data[0])
}

func (x *tunEnv) hasLink(name string) bool { return x.k.HasLink(name) }

// tc runs one line of a tc batch behind the gateway's back; ip one `ip` command.
func (x *tunEnv) tc(line string) {
	x.t.Helper()
	r, err := x.k.Run(context.Background(), executor.Command{Tool: executor.ToolTC, Args: []string{"-batch", "-"}, Stdin: line + "\n"})
	if err != nil || r.Exit != 0 {
		x.t.Fatalf("tc %s: %v %+v", line, err, r)
	}
}

func (x *tunEnv) ip(args ...string) {
	x.t.Helper()
	r, err := x.k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: args})
	if err != nil || r.Exit != 0 {
		x.t.Fatalf("ip %v: %v %+v", args, err, r)
	}
}

func opIndex(p *apply.Plan, match func(executor.Operation) bool) int {
	for i, op := range p.Ops {
		if match(op) {
			return i
		}
	}
	return -1
}

func isIFBLink(action string) func(executor.Operation) bool {
	return func(op executor.Operation) bool {
		l, ok := op.(*executor.Links)
		if !ok {
			return false
		}
		for _, e := range l.Entries {
			if e.Action == action {
				return true
			}
		}
		return false
	}
}

func tcOn(dev string, object string) func(executor.Operation) bool {
	return func(op executor.Operation) bool {
		tc, ok := op.(*executor.TC)
		if !ok {
			return false
		}
		for _, e := range tc.Entries {
			if e.Dev == dev && e.Object == object {
				return true
			}
		}
		return false
	}
}

// The first apply of a tunnel fault makes the IFB and brings it up, builds its tree, and puts the ingress qdisc and the filters
// on the uplink, all before the transaction that marks the first packet; the verify agrees, and a second apply writes nothing.
func TestATunnelFaultMakesTheIFBItsTreeAndTheIngressFiltersInOrder(t *testing.T) {
	x := newTunEnv(t)
	o := x.overlay(tunFlt)
	tg := x.compileWith(o)
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if !x.hasLink("ifb-cgw") || !strings.Contains(x.assigned(), "ifb-cgw") {
		t.Fatalf("no IFB, or not assigned: %v", x.assigned())
	}
	p := res.Plan
	assign := opIndex(p, func(op executor.Operation) bool {
		a, ok := op.(*executor.AssignInterfaces)
		return ok && contains(a.Devs, "ifb-cgw")
	})
	add := opIndex(p, isIFBLink("add_ifb"))
	up := opIndex(p, isIFBLink("up"))
	tree := opIndex(p, tcOn("ifb-cgw", "class"))
	ing := opIndex(p, tcOn("wan0", "filter"))
	nft := opIndex(p, func(op executor.Operation) bool { _, ok := op.(*executor.NftApply); return ok })
	if !(assign >= 0 && assign < add && add < up && up < tree && tree < ing && ing < nft) {
		t.Errorf("order: assign %d, add %d, up %d, IFB tree %d, ingress %d, nft %d\n%s", assign, add, up, tree, ing, nft, strings.Join(p.Summary, "\n"))
	}
	// what the kernel holds
	f := x.ingress()
	if len(f) != 1 || f[0].Flower == nil || f[0].Flower.SrcIP != "198.51.100.2" || f[0].Flower.SrcPort != 51820 || f[0].Flower.Redirect != "ifb-cgw" || f[0].Pref != compiler.IngressPref {
		t.Fatalf("ingress filters: %+v", f)
	}
	fid := tg.Faults[0].ID
	if got := x.classes("ifb-cgw"); got != compiler.ClassIDOf(fid, compiler.Upload) {
		t.Errorf("IFB classes %q", got)
	}
	if got := x.classes("wan0"); got != compiler.ClassIDOf(fid, compiler.Download) {
		t.Errorf("uplink classes %q", got)
	}
	if got := x.classes("br-iot"); got != compiler.ClassIDOf(fid, compiler.Download) {
		t.Errorf("the download class is in the tree of every interface: %q", got)
	}
	if !strings.Contains(strings.Join(p.Summary, "\n"), "ingress qdisc, 1 filters created") || !strings.Contains(strings.Join(p.Summary, "\n"), "create ifb ifb-cgw") {
		t.Errorf("the plan does not say it:\n%s", strings.Join(p.Summary, "\n"))
	}
	if len(p.IngressRestarted) != 1 || p.IngressRestarted[0] != fid {
		t.Errorf("restarted %v", p.IngressRestarted)
	}
	if mm := x.strictVerify(tg); len(mm) != 0 {
		t.Errorf("%v", mm)
	}

	// a second apply of the same state: no link, no tc, no change of the ingress side
	x.k.ClearLog()
	tg2 := x.compileWith(o)
	res2 := x.applyRetiring(tg2)
	for _, op := range res2.Plan.Ops {
		switch op.(type) {
		case *executor.TC, *executor.Links:
			t.Errorf("a re-apply runs %T: %v", op, res2.Plan.Summary)
		}
	}
	if len(res2.Plan.IngressRestarted) != 0 {
		t.Errorf("restarted %v", res2.Plan.IngressRestarted)
	}
}

// A peer that is seen at another address moves its filters in place: the flower filters are replaced (the handle is the fault id),
// the leaf of the class keeps its queue and the counters of the redirect restart (the plan says so).
func TestAPeerThatRoamsMovesItsFiltersInPlace(t *testing.T) {
	x := newTunEnv(t)
	o := x.overlay(tunFlt)
	tg := x.compileWith(o)
	x.applyRetiring(tg)
	fid := tg.Faults[0].ID
	leaf := fmt.Sprintf("%x:", 0x10+2*fid)
	seed := x.k.TCSeed("ifb-cgw", leaf)
	x.k.SetIngressStats("wan0", fid, linux.NormStats{Packets: 40, Bytes: 5000})

	x.ends[clientA] = netip.MustParseAddrPort(epRA2)
	tg2 := x.compileWith(o)
	res := x.applyRetiring(tg2)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	f := x.ingress()
	if len(f) != 1 || f[0].Flower.SrcIP != "198.51.100.9" || f[0].Flower.SrcPort != 40000 || f[0].Flower.Handle != fid {
		t.Fatalf("%+v", f)
	}
	if x.k.TCSeed("ifb-cgw", leaf) != seed {
		t.Error("the leaf on the IFB was made again")
	}
	ifbF := x.tree("ifb-cgw").Subtree("1:").Filters
	if len(ifbF) != 1 || ifbF[0].Flower.SrcPort != 40000 {
		t.Errorf("IFB filter: %+v", ifbF)
	}
	if len(res.Plan.IngressRestarted) != 1 || res.Plan.IngressRestarted[0] != fid {
		t.Errorf("restarted %v", res.Plan.IngressRestarted)
	}
	if len(res.Plan.Stale) != 0 {
		t.Errorf("a roaming peer leaves stale classes: %v", res.Plan.Stale)
	}
	var del bool
	for _, op := range res.Plan.Ops {
		if tc, ok := op.(*executor.TC); ok {
			for _, e := range tc.Entries {
				del = del || e.Action == "delete"
			}
		}
	}
	if del {
		t.Errorf("something is deleted: %v", res.Plan.Summary)
	}
}

// The end of a tunnel fault: the filter that feeds the IFB goes at once, the class on the IFB stays for the grace period so the packets
// queued in it are delivered, and so does the IFB; the retirer deletes the tree and the device after the grace period, and the
// next apply takes the device out of the assigned interfaces. The verify accepts the waiting tree and the assigned IFB.
func TestTheEndOfATunnelFaultKeepsTheIFBUntilItsQueuesHaveDrained(t *testing.T) {
	x := newTunEnv(t)
	o := x.overlay(tunFlt)
	x.applyRetiring(x.compileWith(o))
	fid := x.ids[fmt.Sprintf("overlay:%s:tunnel", o.Id)]

	tg := x.compileWith()
	if tg.IFB != nil {
		t.Fatal("the target still has an IFB")
	}
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v\n%s", res.Mismatches, strings.Join(res.Plan.Summary, "\n"))
	}
	if len(x.ingress()) != 0 || len(x.tree("wan0").Ingress().Qdiscs) != 0 {
		t.Errorf("the ingress side is still there: %+v", x.tree("wan0").Ingress())
	}
	if !x.hasLink("ifb-cgw") || x.classes("ifb-cgw") != compiler.ClassIDOf(fid, compiler.Upload) {
		t.Errorf("the IFB or its class went with the fault: link %v, classes %q", x.hasLink("ifb-cgw"), x.classes("ifb-cgw"))
	}
	if !strings.Contains(x.assigned(), "ifb-cgw") {
		t.Errorf("the IFB left the assigned interfaces while it holds a tree: %s", x.assigned())
	}
	if len(res.Plan.Stale) == 0 || res.Plan.Stale[0].Dev == "" {
		t.Errorf("stale: %v", res.Plan.Stale)
	}
	// the classes of the direction towards the peer wait on the interfaces, too
	if x.classes("wan0") == "" {
		t.Error("the download class was deleted at once")
	}
	x.clock.Advance(3 * time.Second)
	n, err := x.ret.Reap(context.Background(), x.exec(), "")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nothing was reaped")
	}
	if x.hasLink("ifb-cgw") {
		t.Errorf("the IFB outlived its tree")
	}
	// the next apply takes it out of the scope; there is nothing to delete
	tg2 := x.compileWith()
	res2 := x.applyRetiring(tg2)
	if len(res2.Mismatches) != 0 {
		t.Fatalf("%v", res2.Mismatches)
	}
	if strings.Contains(x.assigned(), "ifb-cgw") {
		t.Errorf("the IFB is still assigned: %s", x.assigned())
	}
	if opIndex(res2.Plan, isIFBLink("delete_ifb")) >= 0 {
		t.Errorf("a deletion of a device that is gone: %v", res2.Plan.Summary)
	}
	if mm := x.strictVerify(tg2); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// One-shot applies (the CLI, tests) have no retirer: the tree and the device go with the apply, after the filters that feed it.
func TestWithoutARetirerTheIFBGoesWithTheApply(t *testing.T) {
	x := newTunEnv(t)
	o := x.overlay(tunFlt)
	res, err := apply.Apply(context.Background(), x.exec(), "", x.compileWith(o))
	if err != nil || len(res.Mismatches) != 0 {
		t.Fatalf("%v %v", err, res)
	}
	tg := x.compileWith()
	res, err = apply.Apply(context.Background(), x.exec(), "", tg)
	if err != nil {
		t.Fatal(err)
	}
	if x.hasLink("ifb-cgw") || strings.Contains(x.assigned(), "ifb-cgw") || len(x.tree("wan0").Ingress().Qdiscs) != 0 {
		t.Errorf("IFB %v, assigned %s, ingress %+v", x.hasLink("ifb-cgw"), x.assigned(), x.tree("wan0").Ingress())
	}
	ingress := opIndex(res.Plan, tcOn("wan0", "filter"))
	del := opIndex(res.Plan, isIFBLink("delete_ifb"))
	if ingress < 0 || del < 0 || ingress > del {
		t.Errorf("the filters (%d) go before the device (%d): %v", ingress, del, res.Plan.Summary)
	}
	if mm := x.strictVerify(tg); len(mm) != 0 {
		t.Errorf("%v", mm)
	}
}

// A gateway that starts after a crash finds the IFB, its tree and its ingress filters in the kernel (and, if the executor lost its
// scope, an IFB that is not assigned) with a target that wants none of it: the filters and the ingress qdisc go at once, the tree
// and the device wait one grace period for the retirer (which knows nothing of them yet) and then go.
func TestAnIFBLeftByAGatewayThatDiedIsCleanedUp(t *testing.T) {
	x := newTunEnv(t)
	x.applyRetiring(x.compileWith(x.overlay(tunFlt)))
	// "the restart": the retirer is new; the executor's scope has lost the IFB
	x.ret = apply.NewRetirer(x.clock)
	ifbOnly := func(devs []string) []string {
		var out []string
		for _, d := range devs {
			if d != "ifb-cgw" {
				out = append(out, d)
			}
		}
		return out
	}
	tgNone := x.compileWith()
	if _, err := x.exec().Do(context.Background(), &executor.AssignInterfaces{Devs: ifbOnly(tgNone.Interfaces), OSOwned: tgNone.OSOwned}); err != nil {
		t.Fatal(err)
	}
	res := x.applyRetiring(tgNone)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v\n%s", res.Mismatches, strings.Join(res.Plan.Summary, "\n"))
	}
	if len(x.ingress()) != 0 {
		t.Errorf("the filters are still there: %+v", x.ingress())
	}
	if !x.hasLink("ifb-cgw") {
		t.Fatal("the device went before its grace period")
	}
	x.clock.Advance(3 * time.Second)
	if _, err := x.ret.Reap(context.Background(), x.exec(), ""); err != nil {
		t.Fatal(err)
	}
	if x.hasLink("ifb-cgw") {
		t.Error("the device is still there after the grace period")
	}
	// and with no retirer at all, a leftover that nobody assigned is deleted at once
	x2 := newTunEnv(t)
	if _, err := apply.Apply(context.Background(), x2.exec(), "", x2.compileWith(x2.overlay(tunFlt))); err != nil {
		t.Fatal(err)
	}
	tg2 := x2.compileWith()
	if _, err := x2.exec().Do(context.Background(), &executor.AssignInterfaces{Devs: ifbOnly(tg2.Interfaces), OSOwned: tg2.OSOwned}); err != nil {
		t.Fatal(err)
	}
	res2, err := apply.Apply(context.Background(), x2.exec(), "", tg2)
	if err != nil || len(res2.Mismatches) != 0 {
		t.Fatalf("%v %v", err, res2)
	}
	if x2.hasLink("ifb-cgw") || strings.Contains(x2.assigned(), "ifb-cgw") {
		t.Errorf("leftover: link %v, assigned %s", x2.hasLink("ifb-cgw"), x2.assigned())
	}
}

// The uplink is the host's: a filter of someone else on its ingress qdisc is not ours to touch, and an ingress qdisc that still
// holds it is not ours to delete.
func TestAForeignFilterOnTheIngressQdiscOfTheUplinkIsLeftAlone(t *testing.T) {
	x := newTunEnv(t)
	x.tc("qdisc replace dev wan0 ingress")
	x.tc("filter replace dev wan0 parent ffff: handle 99 protocol ip prio 30 flower ip_proto tcp src_ip 192.0.2.7 src_port 9 action mirred egress redirect dev lan0")
	o := x.overlay(tunFlt)
	tg := x.compileWith(o)
	res := x.applyRetiring(tg)
	if len(res.Mismatches) != 0 {
		t.Fatalf("%v", res.Mismatches)
	}
	if got := x.tree("wan0").Ingress().Filters; len(got) != 2 {
		t.Fatalf("filters %+v", got)
	}
	tg2 := x.compileWith()
	res2 := x.applyRetiring(tg2)
	if len(res2.Mismatches) != 0 {
		t.Fatalf("%v", res2.Mismatches)
	}
	ing := x.tree("wan0").Ingress()
	if len(ing.Qdiscs) != 1 || len(ing.Filters) != 1 || ing.Filters[0].Flower.Handle != 99 {
		t.Errorf("the host's filter or its qdisc is gone: %+v", ing)
	}
}

// What the verify finds: the filter that feeds the IFB changed, or gone; the IFB down or not an IFB; a filter of ours that nobody wants.
func TestTheVerifyFindsDamageToTheTunnelFaultsKernelState(t *testing.T) {
	x := newTunEnv(t)
	o := x.overlay(tunFlt)
	tg := x.compileWith(o)
	x.applyRetiring(tg)
	fid := tg.Faults[0].ID
	for name, c := range map[string]struct {
		damage func()
		want   string
	}{
		"the selector changed": {func() {
			x.tc(fmt.Sprintf("filter replace dev wan0 parent ffff: handle %d protocol ip prio 10 flower ip_proto udp src_ip 198.51.100.77 src_port 51820 action mirred egress redirect dev ifb-cgw", fid))
		}, "different"},
		"the filter is gone": {func() {
			x.tc(fmt.Sprintf("filter delete dev wan0 parent ffff: handle %d protocol ip prio 10 flower", fid))
		}, "missing"},
		"the ingress qdisc is gone": {func() { x.tc("qdisc delete dev wan0 ingress") }, "missing"},
		"another filter of ours": {func() {
			x.tc("qdisc replace dev wan0 ingress")
			x.tc("filter replace dev wan0 parent ffff: handle 55 protocol ip prio 10 flower ip_proto udp src_ip 198.51.100.77 src_port 1 action mirred egress redirect dev ifb-cgw")
		}, "unexpected"},
		"the IFB is down": {func() { x.ip("link", "set", "dev", "ifb-cgw", "down") }, "is down"},
		"the IFB tree lost its class": {func() {
			x.tc(fmt.Sprintf("filter delete dev ifb-cgw parent 1: handle %d protocol ip prio 1 flower", fid))
			x.tc("class delete dev ifb-cgw classid " + compiler.ClassIDOf(fid, compiler.Upload))
		}, ""},
	} {
		c.damage()
		mm := x.strictVerify(tg)
		if len(mm) == 0 {
			t.Errorf("%s: the verify finds nothing", name)
			continue
		}
		if c.want != "" && !strings.Contains(mm[0].String(), c.want) && !strings.Contains(fmt.Sprint(mm), c.want) {
			t.Errorf("%s: %v, want %q", name, mm, c.want)
		}
		res := x.applyRetiring(tg)
		if len(res.Mismatches) != 0 {
			t.Errorf("%s: not repaired: %v", name, res.Mismatches)
		}
		if mm := x.strictVerify(tg); len(mm) != 0 {
			t.Errorf("%s: %v", name, mm)
		}
	}
}

// A device that has the name of the IFB and is not one is not touched.
func TestADeviceWithTheNameOfTheIFBIsNotTakenOver(t *testing.T) {
	x := newTunEnv(t)
	x.k.AddLink("ifb-cgw", "02:00:00:00:09:01", "dummy", true)
	tg := x.compileWith(x.overlay(tunFlt))
	_, err := apply.Apply(context.Background(), x.exec(), "", tg)
	if err == nil || !strings.Contains(err.Error(), "not an IFB") {
		t.Fatalf("%v", err)
	}
}

// A tunnel fault in the preview: the plan names what it will do, and nothing is changed.
func TestThePreviewOfATunnelFaultNamesTheIFBAndTheIngressFilters(t *testing.T) {
	x := newTunEnv(t)
	tg := x.compileWith(x.overlay(tunFlt))
	x.k.ClearLog()
	p, err := apply.PreviewWith(context.Background(), x.exec(), "", tg, x.ret)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Join(p.Summary, "\n")
	for _, want := range []string{"create ifb ifb-cgw", "ingress qdisc, 1 filters created"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
	if x.hasLink("ifb-cgw") {
		t.Error("the preview made the IFB")
	}
}
