//go:build testbed

package executor_test

import (
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The IFB device and the flower filters of the tunnel faults on a real kernel (M10, plan §2.2.1, spike S15): the executor
// creates the IFB once, however often it is asked, brings it up, puts the ingress qdisc and the two kinds of flower filter on the
// interfaces, reads the ingress filters back with the counters of the redirect, deletes the filters by their handle, and deletes
// the IFB but never a device of another kind.
func TestTheIFBAndTheFlowerFiltersOnARealKernel(t *testing.T) {
	g := startGateway(t)
	// the uplink is the host's: assigned, and off limits to links and sysctls
	g.must(&executor.AssignInterfaces{Devs: []string{"wan0", "lan0", "ifb-cgw"}, OSOwned: []string{"wan0"}})
	ifb := executor.LinkEntry{Action: "add_ifb", Name: "ifb-cgw"}
	g.must(&executor.Links{Target: tgt(g.ns), Entries: []executor.LinkEntry{ifb, ifb, {Action: "up", Name: "ifb-cgw"}}})
	if out := g.top.GW.Must("ip", "-d", "link", "show", "dev", "ifb-cgw"); !strings.Contains(out, "ifb") || !strings.Contains(out, "UP") {
		t.Fatalf("no IFB, or it is down:\n%s", out)
	}
	links := read[[]linux.Link](t, g, executor.Read{What: executor.ReadLinks, Dev: "ifb-cgw"})
	if len(links) != 1 || links[0].Kind() != "ifb" {
		t.Fatalf("the read says %+v", links)
	}

	sel := []string{"protocol", "ip", "prio", "1", "flower", "ip_proto", "udp", "src_ip", "198.51.100.2", "src_port", "51820"}
	redirect := append(append([]string(nil), sel...), "action", "mirred", "egress", "redirect", "dev", "ifb-cgw")
	toClass := append(append([]string(nil), sel...), "flowid", "1:12")
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{
		{Object: "qdisc", Action: "replace", Dev: "ifb-cgw", Parent: "root", Handle: "1:", Args: []string{"htb", "default", "1"}},
		{Object: "class", Action: "replace", Dev: "ifb-cgw", Parent: "1:", ClassID: "1:1", Args: []string{"htb", "rate", "10gbit", "quantum", "60000"}},
		{Object: "class", Action: "replace", Dev: "ifb-cgw", Parent: "1:", ClassID: "1:12", Args: []string{"htb", "rate", "10gbit", "quantum", "60000"}},
		{Object: "qdisc", Action: "replace", Dev: "ifb-cgw", Parent: "1:12", Handle: "12:", Args: []string{"netem", "limit", "1000", "delay", "20ms"}},
		{Object: "filter", Action: "replace", Dev: "ifb-cgw", Parent: "1:", Handle: "7", Args: toClass},
		{Object: "qdisc", Action: "replace", Dev: "wan0", Parent: "ingress"},
		{Object: "filter", Action: "replace", Dev: "wan0", Parent: "ffff:", Handle: "7", Args: redirect},
	}})
	// the same again: nothing breaks
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{
		{Object: "qdisc", Action: "replace", Dev: "wan0", Parent: "ingress"},
		{Object: "filter", Action: "replace", Dev: "wan0", Parent: "ffff:", Handle: "7", Args: redirect},
		{Object: "filter", Action: "replace", Dev: "ifb-cgw", Parent: "1:", Handle: "7", Args: toClass},
	}})

	// the read of an interface with an ingress qdisc has its ingress filters
	up := read[linux.NormTree](t, g, executor.Read{What: executor.ReadTC, Dev: "wan0"})
	ing := up.Ingress()
	if len(ing.Qdiscs) != 1 || len(ing.Filters) != 1 {
		t.Fatalf("ingress: %+v", ing)
	}
	f := ing.Filters[0]
	if f.Parent != "ingress" || f.Kind != "flower" || f.Pref != 1 || f.Flower == nil || f.Flower.Handle != 7 || f.Flower.IPProto != "udp" ||
		f.Flower.SrcIP != "198.51.100.2" || f.Flower.SrcPort != 51820 || f.Flower.Redirect != "ifb-cgw" || f.Stats == nil {
		t.Errorf("%+v", f)
	}
	ifbTree := read[linux.NormTree](t, g, executor.Read{What: executor.ReadTC, Dev: "ifb-cgw"})
	own := ifbTree.Subtree("1:")
	if len(own.Filters) != 1 || own.Filters[0].Flowid != "1:12" || own.Filters[0].Flower == nil || own.Filters[0].Flower.Handle != 7 {
		t.Errorf("IFB filters: %+v", own.Filters)
	}

	// a redirect to anything but the IFB, and a flower filter outside the grammar, never reach the kernel
	if _, err := g.c.Do(tctx(t), &executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{{Object: "filter", Action: "replace", Dev: "wan0", Parent: "ffff:", Handle: "8",
		Args: append(append([]string(nil), sel...), "action", "mirred", "egress", "redirect", "dev", "lan0")}}}); err == nil {
		t.Error("a flower redirect to another interface was taken")
	}

	// the filters go by their handle, the qdisc after them; a second deletion of what is gone is fine
	del := []executor.TCEntry{
		{Object: "filter", Action: "delete", Dev: "wan0", Parent: "ffff:", Handle: "7", Args: []string{"protocol", "ip", "prio", "1", "flower"}},
		{Object: "qdisc", Action: "delete", Dev: "wan0", Parent: "ingress"},
		{Object: "filter", Action: "delete", Dev: "ifb-cgw", Parent: "1:", Handle: "7", Args: []string{"protocol", "ip", "prio", "1", "flower"}},
	}
	g.must(&executor.TC{Target: tgt(g.ns), Entries: del})
	g.must(&executor.TC{Target: tgt(g.ns), Entries: del})
	up = read[linux.NormTree](t, g, executor.Read{What: executor.ReadTC, Dev: "wan0"})
	if ing := up.Ingress(); len(ing.Qdiscs) != 0 || len(ing.Filters) != 0 {
		t.Errorf("the ingress side is not gone: %+v", ing)
	}

	// delete_ifb deletes an IFB, and not a device of another kind that has its name
	g.must(&executor.Links{Target: tgt(g.ns), Entries: []executor.LinkEntry{{Action: "delete_ifb", Name: "ifb-cgw"}, {Action: "delete_ifb", Name: "ifb-cgw"}}})
	if out, err := g.top.GW.Run(tctx(t), "ip", "link", "show", "dev", "ifb-cgw"); err == nil {
		t.Errorf("the IFB is still there: %s", out)
	}
	g.top.GW.Must("ip", "link", "add", "ifb-cgw", "type", "dummy")
	if _, err := g.c.Do(tctx(t), &executor.Links{Target: tgt(g.ns), Entries: []executor.LinkEntry{{Action: "delete_ifb", Name: "ifb-cgw"}}}); err == nil ||
		!strings.Contains(err.Error(), "not a ifb") {
		t.Errorf("a dummy device was deleted as an IFB: %v", err)
	}
	if out := g.top.GW.Must("ip", "-o", "link", "show", "dev", "ifb-cgw"); out == "" {
		t.Error("the device is gone")
	}
}
