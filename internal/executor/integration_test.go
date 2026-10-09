//go:build testbed

package executor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// gateway starts the real executor (real tools, real kernel) with a socket in a temp directory
// and returns a client; every operation targets the gateway namespace of the testbed, so nothing
// touches the host (plan §3.1, §4).
type gateway struct {
	t    *testing.T
	top  *testbed.Topology
	c    *executor.Client
	ns   string
	sock string
}

func startGateway(t *testing.T) *gateway {
	t.Helper()
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false))
	c, sock := startExecutor(t)
	g := &gateway{t: t, top: top, c: c, ns: top.GW.Name, sock: sock}
	g.must(&executor.AssignInterfaces{Devs: []string{"wan0", "lan0", "lan1", "br-lan0", "br-lan1"}})
	return g
}

// startExecutor starts the real executor on a socket in a temp directory and returns a client and
// the socket's path. Which namespace its operations target is up to each operation.
func startExecutor(t *testing.T) (*executor.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cgx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ex, err := executor.New(executor.NewExecRunner(), executor.WithStateFile(filepath.Join(dir, "state.json")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	l, err := executor.Listen(filepath.Join(dir, "e.sock"), 0o660, -1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	srv := &executor.Server{Exec: ex}
	go func() { defer wg.Done(); _ = srv.Serve(ctx, l) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	c, err := executor.Dial(ctx, filepath.Join(dir, "e.sock"), executor.DialOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, filepath.Join(dir, "e.sock")
}

func tctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return c
}

func (g *gateway) must(ops ...executor.Operation) executor.Outcome {
	g.t.Helper()
	out, err := g.c.Do(context.Background(), ops...)
	if err != nil {
		g.t.Fatalf("%v", err)
	}
	return out
}

func tgt(ns string) executor.Target { return executor.Target{NS: ns} }

func read[T any](t *testing.T, g *gateway, r executor.Read) T {
	t.Helper()
	r.NS = g.ns
	var v T
	if _, err := g.c.Read(tctx(t), r, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// M3 test: the executor reads interfaces and routes of the gateway namespace.
func TestExecutorReadsTheGatewayNamespace(t *testing.T) {
	g := startGateway(t)

	links := read[[]linux.Link](t, g, executor.Read{What: executor.ReadLinks})
	byName := map[string]linux.Link{}
	for _, l := range links {
		byName[l.Name] = l
	}
	for _, want := range []string{"lo", "wan0", "mgmt0", "lan0", "lan1", "br-lan0", "br-lan1"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("no link %s in %v", want, names(links))
		}
	}
	if l := byName["lan0"]; l.Master != "br-lan0" || l.MAC != testbed.GWLan0MAC {
		t.Errorf("lan0: %+v", l)
	}
	if byName["br-lan0"].Kind() != "bridge" || !byName["br-lan0"].Up() {
		t.Errorf("br-lan0: %+v", byName["br-lan0"])
	}

	addrs := read[[]linux.Addrs](t, g, executor.Read{What: executor.ReadAddrs})
	has := func(dev, ip string, plen int) bool {
		for _, a := range addrs {
			if a.Name == dev {
				for _, x := range a.Addrs {
					if x.Family == "inet" && x.Local == ip && x.PrefixLen == plen {
						return true
					}
				}
			}
		}
		return false
	}
	if !has("br-lan0", testbed.LAN0Gateway, 24) || !has("br-lan1", testbed.LAN1Gateway, 24) || !has("wan0", testbed.UplinkGateway, 24) || !has("mgmt0", testbed.MgmtGateway, 24) {
		t.Errorf("addresses: %+v", addrs)
	}
	if has("lan0", testbed.LAN0Gateway, 24) {
		t.Error("the bridge port carries no address")
	}

	routes := read[[]linux.Route](t, g, executor.Read{What: executor.ReadRoutes, Table: "main"})
	var def *linux.Route
	for i, r := range routes {
		if r.Dst == "default" {
			def = &routes[i]
		}
	}
	if def == nil || def.Gateway != testbed.MgmtPeer || def.Dev != "mgmt0" {
		t.Errorf("default route: %+v in %+v", def, routes)
	}

	rules := read[[]linux.Rule](t, g, executor.Read{What: executor.ReadRules})
	if len(rules) != 3 || rules[0].Table != "local" || rules[1].Table != "main" || rules[2].Table != "default" {
		t.Errorf("rules: %+v", rules)
	}
	if q := read[[]linux.Qdisc](t, g, executor.Read{What: executor.ReadQdiscs, Dev: "wan0"}); len(q) == 0 {
		t.Error("no qdisc on wan0")
	}
	// what the reader says matches the kernel: the namespace and not the executor's own is read
	if own := read[[]linux.Link](t, &gateway{t: t, top: g.top, c: g.c}, executor.Read{What: executor.ReadLinks}); contains(names(own), "wan0") {
		t.Error("the executor's own namespace shows the gateway's interfaces")
	}
}

func names(l []linux.Link) (n []string) {
	for _, x := range l {
		n = append(n, x.Name)
	}
	return n
}

func nftRuleset(extra string) string {
	return `{"nftables":[` +
		`{"add":{"table":{"family":"inet","name":"chaosgw"}}},` +
		`{"flush":{"table":{"family":"inet","name":"chaosgw"}}},` +
		`{"add":{"set":{"family":"inet","table":"chaosgw","name":"blocked","type":"ipv4_addr","flags":["interval","timeout"]}}},` +
		`{"add":{"chain":{"family":"inet","table":"chaosgw","name":"forward","type":"filter","hook":"forward","prio":-150,"policy":"accept"}}},` +
		`{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"forward","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"lan0"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}]}}}` +
		extra + `]}`
}

func TestNftablesApplyIsAtomicAndScoped(t *testing.T) {
	g := startGateway(t)
	apply := func(ruleset string) error {
		_, err := g.c.Do(context.Background(), &executor.NftApply{Target: tgt(g.ns), Ruleset: []byte(ruleset)})
		return err
	}
	if err := apply(nftRuleset("")); err != nil {
		t.Fatal(err)
	}
	rs := read[linux.Ruleset](t, g, executor.Read{What: executor.ReadNft})
	if rs.Set("blocked") == nil || len(rs.Rules("forward")) != 1 {
		t.Fatalf("applied state: %+v", rs)
	}
	before := g.top.GW.Must("nft", "-j", "list", "ruleset")

	// a ruleset with an error in its last command changes nothing: the transaction is atomic
	broken := nftRuleset(`,{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"missing","expr":[{"accept":null}]}}}`)
	if err := apply(broken); err == nil {
		t.Fatal("a broken ruleset must fail")
	} else {
		var re *executor.RemoteError
		if !errors.As(err, &re) || re.Code != executor.CodeCommand {
			t.Fatalf("error: %v", err)
		}
	}
	if after := g.top.GW.Must("nft", "-j", "list", "ruleset"); after != before {
		t.Fatalf("a failed apply changed the ruleset:\n%s\n%s", before, after)
	}

	// applying the same ruleset again is idempotent, and one with another rule replaces the content
	if err := apply(nftRuleset("")); err != nil {
		t.Fatal(err)
	}
	if err := apply(nftRuleset(`,{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"forward","expr":[{"drop":null}]}}}`)); err != nil {
		t.Fatal(err)
	}
	if rs := read[linux.Ruleset](t, g, executor.Read{What: executor.ReadNft}); len(rs.Rules("forward")) != 2 {
		t.Fatalf("rules: %+v", rs.Rules("forward"))
	}

	// scope: nothing outside inet chaosgw, and a rejected request leaves the kernel alone
	for name, rule := range map[string]string{
		"other table":    `{"nftables":[{"add":{"table":{"family":"inet","name":"filter"}}}]}`,
		"other family":   `{"nftables":[{"add":{"table":{"family":"ip","name":"chaosgw"}}}]}`,
		"flush ruleset":  `{"nftables":[{"flush":{"ruleset":null}}]}`,
		"mixed in scope": nftRuleset(`,{"add":{"table":{"family":"ip","name":"nat"}}}`),
	} {
		err := apply(rule)
		var re *executor.RemoteError
		if !errors.As(err, &re) || re.Code != executor.CodeInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	if tables := g.top.GW.Must("nft", "list", "tables"); strings.Count(tables, "table") != 1 || !strings.Contains(tables, "inet chaosgw") {
		t.Errorf("tables after rejected requests: %s", tables)
	}

	// the table can be removed again
	if err := apply(`{"nftables":[{"delete":{"table":{"family":"inet","name":"chaosgw"}}}]}`); err != nil {
		t.Fatal(err)
	}
	if rs := read[linux.Ruleset](t, g, executor.Read{What: executor.ReadNft}); len(rs.Tables()) != 0 {
		t.Errorf("table still there: %+v", rs)
	}
	// a namespace that does not exist is an error, not an empty ruleset
	if _, err := g.c.Do(context.Background(), &executor.Read{Target: tgt("cgx-missing-ns"), What: executor.ReadNft}); err == nil {
		t.Error("reading a missing namespace must fail")
	}
}

func TestIncrementalSetUpdates(t *testing.T) {
	g := startGateway(t)
	g.must(&executor.NftApply{Target: tgt(g.ns), Ruleset: []byte(nftRuleset(""))})
	gen := g.must().Generation

	out := g.must(&executor.NftAddElements{Target: tgt(g.ns), Set: "blocked", Elements: []string{"192.0.2.1", "198.51.100.0/24"}, TimeoutSeconds: 300})
	if out.Generation != gen+1 {
		t.Errorf("generation %d -> %d", gen, out.Generation)
	}
	if list := g.top.GW.Must("nft", "list", "set", "inet", "chaosgw", "blocked"); !strings.Contains(list, "192.0.2.1") || !strings.Contains(list, "198.51.100.0/24") || !strings.Contains(list, "timeout") {
		t.Errorf("set content:\n%s", list)
	}
	rs := read[linux.Ruleset](t, g, executor.Read{What: executor.ReadNft})
	if s := rs.Set("blocked"); s == nil || len(s.Elem) != 2 {
		t.Errorf("parsed set: %+v", s)
	}
	// adding again is fine (elements may repeat), and a set that does not exist is an error
	g.must(&executor.NftAddElements{Target: tgt(g.ns), Set: "blocked", Elements: []string{"192.0.2.1"}})
	if _, err := g.c.Do(context.Background(), &executor.NftAddElements{Target: tgt(g.ns), Set: "nope", Elements: []string{"192.0.2.1"}}); err == nil {
		t.Error("a missing set must fail")
	}
}

// TestIncrementalMapUpdates is TestIncrementalSetUpdates for the map counterpart added in M7
// (plan §3.3): the identity map's device numeral (a plain integer value) and a classification
// map's element (a verdict that goes to a per-id chain).
func TestIncrementalMapUpdates(t *testing.T) {
	g := startGateway(t)
	extra := `,{"add":{"map":{"family":"inet","table":"chaosgw","name":"ident4","type":"ipv4_addr","map":"mark"}}},` +
		`{"add":{"map":{"family":"inet","table":"chaosgw","name":"cls_dev","type":"ipv4_addr","map":"verdict"}}},` +
		`{"add":{"chain":{"family":"inet","table":"chaosgw","name":"mark_7"}}},` +
		`{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"mark_7","expr":[{"return":null}]}}}`
	g.must(&executor.NftApply{Target: tgt(g.ns), Ruleset: []byte(nftRuleset(extra))})
	gen := g.must().Generation

	out := g.must(&executor.NftAddMapElements{Target: tgt(g.ns), Map: "ident4", Elements: []executor.NftMapElement{{Key: "10.10.0.31", Value: "3"}}})
	if out.Generation != gen+1 {
		t.Errorf("generation %d -> %d", gen, out.Generation)
	}
	if list := g.top.GW.Must("nft", "list", "map", "inet", "chaosgw", "ident4"); !strings.Contains(list, "10.10.0.31") || !strings.Contains(list, "3") {
		t.Errorf("identity map content:\n%s", list)
	}
	rs := read[linux.Ruleset](t, g, executor.Read{What: executor.ReadNft})
	if m := rs.Set("ident4"); m == nil || len(m.Elem) != 1 {
		t.Errorf("parsed map: %+v", m)
	} else if p := m.Pairs(); p["10.10.0.31"] != "3" {
		t.Errorf("pairs: %v", p)
	}

	// the classification map's element jumps to an existing chain
	g.must(&executor.NftAddMapElements{Target: tgt(g.ns), Map: "cls_dev", Elements: []executor.NftMapElement{{Key: "10.10.0.31", Value: "mark_7"}}})
	if list := g.top.GW.Must("nft", "list", "map", "inet", "chaosgw", "cls_dev"); !strings.Contains(list, "10.10.0.31") || !strings.Contains(list, "mark_7") {
		t.Errorf("classification map content:\n%s", list)
	}
	// a jump to a chain that does not exist is refused
	if _, err := g.c.Do(context.Background(), &executor.NftAddMapElements{Target: tgt(g.ns), Map: "cls_dev", Elements: []executor.NftMapElement{{Key: "10.10.0.32", Value: "mark_404"}}}); err == nil {
		t.Error("a jump to a missing chain must fail")
	}

	// a device's address changes: delete the old key, add the new one, in one incremental request
	g.must(&executor.NftDelMapElements{Target: tgt(g.ns), Map: "ident4", Keys: []string{"10.10.0.31"}})
	g.must(&executor.NftAddMapElements{Target: tgt(g.ns), Map: "ident4", Elements: []executor.NftMapElement{{Key: "10.10.0.77", Value: "3"}}})
	if list := g.top.GW.Must("nft", "list", "map", "inet", "chaosgw", "ident4"); strings.Contains(list, "10.10.0.31") || !strings.Contains(list, "10.10.0.77") {
		t.Errorf("after the address change:\n%s", list)
	}
	// deleting a key that is not there fails, and a map that does not exist is an error
	if _, err := g.c.Do(context.Background(), &executor.NftDelMapElements{Target: tgt(g.ns), Map: "ident4", Keys: []string{"10.10.0.31"}}); err == nil {
		t.Error("deleting an absent key must fail")
	}
	if _, err := g.c.Do(context.Background(), &executor.NftAddMapElements{Target: tgt(g.ns), Map: "nope", Elements: []executor.NftMapElement{{Key: "10.10.0.31", Value: "1"}}}); err == nil {
		t.Error("a missing map must fail")
	}
}

func TestRoutingInOwnTablesWithOwnTag(t *testing.T) {
	g := startGateway(t)
	g.top.GW.Sysctl("net.ipv4.ip_forward", "1") // the testbed gateway is unconfigured here; `route get` of a forwarded packet needs it
	mainBefore := g.top.GW.Must("ip", "route", "show", "table", "main")
	rulesBefore := g.top.GW.Must("ip", "rule", "show")

	g.must(&executor.Routing{
		Target: tgt(g.ns),
		Routes: []executor.Route{
			{Action: "replace", Family: 4, Table: 100, Dst: "10.99.0.0/16", Via: testbed.ServerAddr, Dev: "wan0"},
			{Action: "replace", Family: 4, Table: 102, Dst: "10.98.0.0/16", Type: "prohibit"},
		},
		Rules: []executor.Rule{
			{Action: "add", Family: 4, Priority: 1000, From: testbed.LAN0Subnet, To: "10.99.0.0/16", Table: 100},
			{Action: "add", Family: 4, Priority: 1001, From: testbed.LAN0Subnet, To: "10.98.0.0/16", Table: 102},
		},
	})
	// the effect: the kernel routes by the new rules
	if out := g.top.GW.Must("ip", "route", "get", "10.99.1.1", "from", testbed.ClientAAddr, "iif", "br-lan0"); !strings.Contains(out, "via "+testbed.ServerAddr+" dev wan0") {
		t.Errorf("route get: %s", out)
	}
	if out, err := g.top.GW.Run(tctx(t), "ip", "route", "get", "10.98.1.1", "from", testbed.ClientAAddr, "iif", "br-lan0"); err == nil || !strings.Contains(out, "Permission denied") { // the kernel reports a prohibit route as EACCES
		t.Errorf("prohibit route: %q %v", out, err)
	}
	// the same questions through the executor's route_get read (M8a, explain): the kernel's answer,
	// parsed; a route that does not exist or is prohibited is an answer, not a failed read
	rg := read[linux.RouteGet](t, g, executor.Read{What: executor.ReadRouteGet, Dst: "10.99.1.1", Src: testbed.ClientAAddr, Dev: "br-lan0"})
	if rg.Table != "100" || rg.Gateway != testbed.ServerAddr || rg.Dev != "wan0" || rg.Unreachable || rg.Iif != "br-lan0" {
		t.Errorf("route_get: %+v", rg)
	}
	rg = read[linux.RouteGet](t, g, executor.Read{What: executor.ReadRouteGet, Dst: "10.98.1.1", Src: testbed.ClientAAddr, Dev: "br-lan0"})
	if !rg.Unreachable || !strings.Contains(rg.Error, "Permission denied") {
		t.Errorf("route_get of a prohibit route: %+v", rg)
	}
	rg = read[linux.RouteGet](t, g, executor.Read{What: executor.ReadRouteGet, Dst: "192.0.2.77"})
	if rg.Unreachable || rg.Dev != "mgmt0" || rg.Gateway != testbed.MgmtPeer {
		t.Errorf("route_get from the gateway itself: %+v", rg)
	}
	// the state as the executor reports it, with the executor's protocol tag
	rules := read[[]linux.Rule](t, g, executor.Read{What: executor.ReadRules})
	var own int
	for _, r := range rules {
		if r.Protocol == "201" {
			own++
			if r.Priority < 1000 || r.Table != "100" && r.Table != "102" {
				t.Errorf("rule: %+v", r)
			}
		}
	}
	if own != 2 || len(rules) != 5 {
		t.Errorf("rules: %+v", rules)
	}
	routes := read[[]linux.Route](t, g, executor.Read{What: executor.ReadRoutes, Table: "100"})
	if len(routes) != 1 || routes[0].Dst != "10.99.0.0/16" || routes[0].Protocol != "201" || routes[0].Gateway != testbed.ServerAddr {
		t.Errorf("routes: %+v", routes)
	}
	if main := g.top.GW.Must("ip", "route", "show", "table", "main"); main != mainBefore {
		t.Errorf("the main table changed:\n%s\n%s", mainBefore, main)
	}

	// applying the same rules again is a no-op, not an error
	g.must(&executor.Routing{Target: tgt(g.ns),
		Rules: []executor.Rule{{Action: "add", Family: 4, Priority: 1000, From: testbed.LAN0Subnet, To: "10.99.0.0/16", Table: 100}}})
	if n := strings.Count(g.top.GW.Must("ip", "rule", "show"), "1000:"); n != 1 {
		t.Errorf("%d rules with priority 1000 after a repeated add", n)
	}
	// replace is idempotent; delete removes exactly what was added
	g.must(&executor.Routing{Target: tgt(g.ns), Routes: []executor.Route{{Action: "replace", Family: 4, Table: 100, Dst: "10.99.0.0/16", Via: testbed.ServerAddr, Dev: "wan0"}}})
	g.must(&executor.Routing{
		Target: tgt(g.ns),
		Routes: []executor.Route{{Action: "delete", Family: 4, Table: 100, Dst: "10.99.0.0/16"}, {Action: "delete", Family: 4, Table: 102, Dst: "10.98.0.0/16"}},
		Rules: []executor.Rule{
			{Action: "delete", Family: 4, Priority: 1000, From: testbed.LAN0Subnet, To: "10.99.0.0/16", Table: 100},
			{Action: "delete", Family: 4, Priority: 1001, From: testbed.LAN0Subnet, To: "10.98.0.0/16", Table: 102},
		},
	})
	if after := g.top.GW.Must("ip", "rule", "show"); after != rulesBefore {
		t.Errorf("rules after delete:\n%s\n%s", rulesBefore, after)
	}
	if routes := read[[]linux.Route](t, g, executor.Read{What: executor.ReadRoutes, Table: "100"}); len(routes) != 0 {
		t.Errorf("routes left: %+v", routes)
	}
}

// A route with a path MTU (a PMTU mirror table, M10) is written with `mtu lock N`, reads back with its size, is replaced in
// place by a route of another size, and the rule on a mark selects its table; a delete names no size.
func TestARouteWithALockedPathMTUIsWrittenReplacedInPlaceAndDeleted(t *testing.T) {
	g := startGateway(t)
	g.top.GW.Sysctl("net.ipv4.ip_forward", "1")
	route := func(mtu int) executor.Route {
		return executor.Route{Action: "replace", Family: 4, Table: 103, Dst: "10.99.0.0/16", Via: testbed.ServerAddr, Dev: "wan0", MTU: mtu}
	}
	g.must(&executor.Routing{Target: tgt(g.ns), Routes: []executor.Route{route(1280)},
		Rules: []executor.Rule{{Action: "add", Family: 4, Priority: 950, Fwmark: "0x20000/0xe0000", Table: 103}}})
	if out := g.top.GW.Must("ip", "-d", "route", "show", "table", "103"); !strings.Contains(out, "mtu lock 1280") {
		t.Errorf("the route has no locked size:\n%s", out)
	}
	routes := read[[]linux.Route](t, g, executor.Read{What: executor.ReadRoutes, Table: "103"})
	if len(routes) != 1 || routes[0].MTU() != 1280 || routes[0].Protocol != "201" {
		t.Errorf("%+v", routes)
	}
	// the rule reads back with the mark and its mask, and sends the marked packet through the table
	var found bool
	for _, r := range read[[]linux.Rule](t, g, executor.Read{What: executor.ReadRules}) {
		if r.Protocol == "201" && r.Fwmark == "0x20000" && r.Fwmask == "0xe0000" && r.Table == "103" && r.Priority == 950 {
			found = true
		}
	}
	if !found {
		t.Error("the rule on the mark is not read back")
	}
	if out := g.top.GW.Must("ip", "route", "get", "10.99.1.1", "from", testbed.ClientAAddr, "iif", "br-lan0", "mark", "0x20000"); !strings.Contains(out, "mtu lock 1280") {
		t.Errorf("a marked packet does not meet the size: %s", out)
	}
	if out := g.top.GW.Must("ip", "route", "get", "10.99.1.1", "from", testbed.ClientAAddr, "iif", "br-lan0"); strings.Contains(out, "mtu") {
		t.Errorf("an unmarked packet meets the size: %s", out)
	}
	// another size is a replace: still one route
	g.must(&executor.Routing{Target: tgt(g.ns), Routes: []executor.Route{route(1400)}})
	routes = read[[]linux.Route](t, g, executor.Read{What: executor.ReadRoutes, Table: "103"})
	if len(routes) != 1 || routes[0].MTU() != 1400 {
		t.Errorf("%+v", routes)
	}
	g.must(&executor.Routing{Target: tgt(g.ns), Routes: []executor.Route{{Action: "delete", Family: 4, Table: 103, Dst: "10.99.0.0/16", Via: testbed.ServerAddr, Dev: "wan0", MTU: 1400}},
		Rules: []executor.Rule{{Action: "delete", Family: 4, Priority: 950, Fwmark: "0x20000/0xe0000", Table: 103}}})
	if routes := read[[]linux.Route](t, g, executor.Read{What: executor.ReadRoutes, Table: "103"}); len(routes) != 0 {
		t.Errorf("routes left: %+v", routes)
	}
}

// A foreign rule with the same selectors but another protocol tag cannot be touched: the executor
// always deletes with its own tag, so the kernel does not find the foreign entry.
func TestExecutorCannotDeleteForeignRulesOrRoutes(t *testing.T) {
	g := startGateway(t)
	g.top.GW.Must("ip", "rule", "add", "priority", "1000", "from", testbed.LAN0Subnet, "table", "100")
	g.top.GW.Must("ip", "route", "add", "10.77.0.0/16", "via", testbed.ServerAddr, "table", "100", "proto", "static")
	// deleting what is not ours finds nothing: the request succeeds as a no-op, the entries stay
	g.must(&executor.Routing{Target: tgt(g.ns),
		Rules:  []executor.Rule{{Action: "delete", Family: 4, Priority: 1000, From: testbed.LAN0Subnet, Table: 100}},
		Routes: []executor.Route{{Action: "delete", Family: 4, Table: 100, Dst: "10.77.0.0/16"}}})
	if out := g.top.GW.Must("ip", "rule", "show"); !strings.Contains(out, "1000:") {
		t.Errorf("the foreign rule is gone: %s", out)
	}
	if out := g.top.GW.Must("ip", "route", "show", "table", "100"); !strings.Contains(out, "10.77.0.0/16") {
		t.Errorf("the foreign route is gone: %s", out)
	}
}

func TestTrafficControlOnAssignedInterfaces(t *testing.T) {
	g := startGateway(t)
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{
		{Object: "qdisc", Action: "replace", Dev: "wan0", Parent: "root", Handle: "1:", Args: []string{"htb", "default", "10"}},
		{Object: "class", Action: "replace", Dev: "wan0", Parent: "1:", ClassID: "1:10", Args: []string{"htb", "rate", "100mbit"}},
		{Object: "qdisc", Action: "replace", Dev: "wan0", Parent: "1:10", Handle: "10:", Args: []string{"netem", "delay", "50ms", "10ms", "loss", "1%"}},
		{Object: "filter", Action: "add", Dev: "wan0", Parent: "1:", Handle: "0x10", Args: []string{"protocol", "ip", "prio", "1", "fw", "flowid", "1:10"}},
		{Object: "qdisc", Action: "add", Dev: "lan0", Parent: "ingress"},
	}})
	qdiscs := read[[]linux.Qdisc](t, g, executor.Read{What: executor.ReadQdiscs, Dev: "wan0"})
	kinds := map[string]linux.Qdisc{}
	for _, q := range qdiscs {
		kinds[q.Kind] = q
	}
	if kinds["htb"].Handle != "1:" || !kinds["htb"].Root || kinds["netem"].Parent != "1:10" || kinds["netem"].Handle != "10:" {
		t.Errorf("qdiscs: %+v", qdiscs)
	}
	if !strings.Contains(string(kinds["netem"].Options), `"delay"`) {
		t.Errorf("netem options: %s", kinds["netem"].Options)
	}
	classes := read[[]linux.Class](t, g, executor.Read{What: executor.ReadClasses, Dev: "wan0"})
	var found bool
	for _, c := range classes {
		// a class directly below the qdisc 1: is printed as "root" by tc
		found = found || c.Handle == "1:10" && c.Root && c.Rate == 12500000
	}
	if !found {
		t.Errorf("classes: %+v", classes)
	}
	if filters := read[[]linux.Filter](t, g, executor.Read{What: executor.ReadFilters, Dev: "wan0"}); len(filters) == 0 || filters[0].Kind != "fw" {
		t.Errorf("filters: %+v", filters)
	}
	if q := read[[]linux.Qdisc](t, g, executor.Read{What: executor.ReadQdiscs, Dev: "lan0"}); !hasKind(q, "ingress") {
		t.Errorf("lan0 qdiscs: %+v", q)
	}

	// scope: an interface that is not assigned is out of reach, even named as a second device
	for name, e := range map[string]executor.TCEntry{
		"mgmt0":      {Object: "qdisc", Action: "delete", Dev: "mgmt0", Parent: "root", Handle: "1:"},
		"mirred dev": {Object: "filter", Action: "add", Dev: "lan0", Parent: "ffff:", Args: []string{"protocol", "ip", "u32", "match", "u32", "0", "0", "action", "mirred", "egress", "redirect", "dev", "mgmt0"}},
	} {
		_, err := g.c.Do(context.Background(), &executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{e}})
		var re *executor.RemoteError
		if !errors.As(err, &re) || re.Code != executor.CodeScope {
			t.Errorf("%s: %v", name, err)
		}
	}
	if out := g.top.GW.Must("tc", "qdisc", "show", "dev", "mgmt0"); strings.Contains(out, "ingress") {
		t.Errorf("mgmt0 was touched: %s", out)
	}

	// remove: the default qdisc returns
	g.must(&executor.TC{Target: tgt(g.ns), Entries: []executor.TCEntry{
		{Object: "qdisc", Action: "delete", Dev: "wan0", Parent: "root", Handle: "1:"},
		{Object: "qdisc", Action: "delete", Dev: "lan0", Parent: "ingress"},
	}})
	if q := read[[]linux.Qdisc](t, g, executor.Read{What: executor.ReadQdiscs, Dev: "wan0"}); hasKind(q, "htb") || hasKind(q, "netem") {
		t.Errorf("qdiscs after delete: %+v", q)
	}
}

func hasKind(q []linux.Qdisc, kind string) bool {
	for _, x := range q {
		if x.Kind == kind {
			return true
		}
	}
	return false
}

func TestOffloadsAreSwitchedOffAndVerified(t *testing.T) {
	g := startGateway(t)
	g.top.GW.Must("ethtool", "-K", "wan0", "gro", "on", "gso", "on", "tso", "on")
	g.must(&executor.Offloads{Target: tgt(g.ns), Devs: []string{"wan0", "br-lan0"}})
	for _, dev := range []string{"wan0", "br-lan0"} {
		f := read[linux.Features](t, g, executor.Read{What: executor.ReadOffloads, Dev: dev})
		if on := f.OffloadsStillOn(); len(on) != 0 {
			t.Errorf("%s: still on: %v", dev, on)
		}
	}
	if out := g.top.GW.Must("ethtool", "-k", "wan0"); !strings.Contains(out, "generic-receive-offload: off") {
		t.Errorf("ethtool -k wan0:\n%s", out)
	}
}

func TestDockerUserAcceptRulesAreMaintained(t *testing.T) {
	g := startGateway(t)
	// the testbed has no Docker: create the chain it would have created
	g.top.GW.Must("iptables", "-w", "5", "-N", "DOCKER-USER")
	g.top.GW.Must("iptables", "-w", "5", "-A", "DOCKER-USER", "-j", "RETURN")
	rules := func() string { return g.top.GW.Must("iptables", "-w", "5", "-S", "DOCKER-USER") }

	for i := 0; i < 2; i++ { // the second run changes nothing
		g.must(&executor.DockerUser{Target: tgt(g.ns), Action: "ensure", Devs: []string{"br-lan0", "wan0"}})
	}
	out := rules()
	if strings.Count(out, "--comment chaosgw") != 4 || !strings.Contains(out, "-i br-lan0") || !strings.Contains(out, "-o wan0") {
		t.Errorf("rules:\n%s", out)
	}
	// the accept rules come before Docker's own RETURN, which is what makes them effective
	if strings.Index(out, "-j RETURN") < strings.LastIndex(out, "-j ACCEPT") {
		t.Errorf("our rules must be in front of the RETURN:\n%s", out)
	}
	g.must(&executor.DockerUser{Target: tgt(g.ns), Action: "remove", Devs: []string{"br-lan0", "wan0"}})
	g.must(&executor.DockerUser{Target: tgt(g.ns), Action: "remove", Devs: []string{"br-lan0"}}) // removing what is gone is fine
	if out := rules(); strings.Contains(out, "chaosgw") || !strings.Contains(out, "-j RETURN") {
		t.Errorf("rules after remove:\n%s", out)
	}
	// only assigned interfaces: mgmt0 is not Chaos Gateway's
	if _, err := g.c.Do(context.Background(), &executor.DockerUser{Target: tgt(g.ns), Action: "ensure", Devs: []string{"mgmt0"}}); err == nil {
		t.Error("an unassigned interface must be refused")
	}
}

func TestConcurrentClientsAreSerialized(t *testing.T) {
	g := startGateway(t)
	g.must(&executor.NftApply{Target: tgt(g.ns), Ruleset: []byte(nftRuleset(""))})
	gen := g.must().Generation
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// every client has its own connection, as the API, the DNS proxy and the scheduler do
			c, err := executor.Dial(context.Background(), g.sock, executor.DialOptions{})
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = c.Close() }()
			for j := 0; j < 5; j++ {
				if _, err := c.Do(context.Background(), &executor.NftAddElements{Target: tgt(g.ns), Set: "blocked", Elements: []string{"192.0.2." + string(rune('1'+j))}}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := g.must().Generation; got != gen+n*5 {
		t.Errorf("generation %d, want %d: every update counts once", got, gen+n*5)
	}
}

// M4 additions: bridges, ports, addresses, sysctls on a real kernel.
func TestLinksAndSysctlsOnARealKernel(t *testing.T) {
	g := startGateway(t)
	g.must(&executor.AssignInterfaces{Devs: []string{"wan0", "lan0", "lan1", "br-lan0", "br-lan1", "br-x"}})
	g.must(&executor.Links{Target: tgt(g.ns), Entries: []executor.LinkEntry{
		{Action: "add_bridge", Name: "br-x"},
		{Action: "add_bridge", Name: "br-x"}, // idempotent
		{Action: "enslave", Name: "lan1", Master: "br-x"},
		{Action: "addr_replace", Name: "br-x", CIDR: "10.77.0.1/24"},
		{Action: "addr_replace", Name: "br-x", CIDR: "10.77.0.1/24"},
		{Action: "up", Name: "br-x"},
	}})
	if out := g.top.GW.Must("ip", "-o", "link", "show", "dev", "lan1"); !strings.Contains(out, "master br-x") {
		t.Errorf("lan1: %s", out)
	}
	if out := g.top.GW.Must("ip", "-o", "addr", "show", "dev", "br-x"); !strings.Contains(out, "10.77.0.1/24") {
		t.Errorf("br-x: %s", out)
	}
	g.must(&executor.Sysctl{Target: tgt(g.ns), Entries: []executor.SysctlEntry{
		{Name: "ip_forward", Value: 1}, {Name: "accept_ra", Dev: "br-x", Value: 0}, {Name: "disable_ipv6", Dev: "br-x", Value: 0},
	}})
	var fwd, ra int
	if _, err := g.c.Read(tctx(t), executor.Read{Target: tgt(g.ns), What: executor.ReadSysctl, Name: "ip_forward"}, &fwd); err != nil || fwd != 1 {
		t.Errorf("ip_forward %d %v", fwd, err)
	}
	ra = 9
	if _, err := g.c.Read(tctx(t), executor.Read{Target: tgt(g.ns), What: executor.ReadSysctl, Name: "accept_ra", Dev: "br-x"}, &ra); err != nil || ra != 0 {
		t.Errorf("accept_ra %d %v", ra, err)
	}
	if out := g.top.GW.Must("cat", "/proc/sys/net/ipv4/ip_forward"); out != "1" {
		t.Errorf("ip_forward %q", out)
	}
	// deleting what is gone is fine; addresses can be removed; the port can be released
	g.must(&executor.Links{Target: tgt(g.ns), Entries: []executor.LinkEntry{
		{Action: "addr_delete", Name: "br-x", CIDR: "10.77.0.1/24"},
		{Action: "addr_delete", Name: "br-x", CIDR: "10.77.0.1/24"},
		{Action: "release", Name: "lan1"},
		{Action: "delete_bridge", Name: "br-x"},
		{Action: "delete_bridge", Name: "br-x"},
	}})
	if out, err := g.top.GW.Run(tctx(t), "ip", "link", "show", "dev", "br-x"); err == nil {
		t.Errorf("br-x still exists: %s", out)
	}
	// delete_bridge never deletes anything but a bridge
	if _, err := g.c.Do(tctx(t), &executor.Links{Target: tgt(g.ns), Entries: []executor.LinkEntry{{Action: "delete_bridge", Name: "wan0"}}}); err == nil {
		t.Error("a veth was deleted as a bridge")
	}
	if out := g.top.GW.Must("ip", "-o", "link", "show", "dev", "wan0"); out == "" {
		t.Error("wan0 is gone")
	}
	// an interface that is not assigned cannot be touched
	if _, err := g.c.Do(tctx(t), &executor.Links{Target: tgt(g.ns), Entries: []executor.LinkEntry{{Action: "down", Name: "mgmt0"}}}); err == nil {
		t.Error("mgmt0 is not assigned")
	}
	var assigned []string
	if _, err := g.c.Read(tctx(t), executor.Read{Target: tgt(g.ns), What: executor.ReadAssigned}, &assigned); err != nil || len(assigned) != 6 {
		t.Errorf("assigned %v %v", assigned, err)
	}
}

const (
	wgKeyRef  = "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21"
	wgPeerRef = "9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

// M4b: the executor creates a WireGuard interface, synchronizes it with wg syncconf and deletes it,
// on a real kernel; the keys come from the provider and appear in no output.
func TestWireGuardInterfaceOnARealKernel(t *testing.T) {
	top := testbed.NewDefault(t, testbed.WithPlainGateway(false))
	priv := strings.TrimSpace(top.GW.Must("wg", "genkey"))
	psk := strings.TrimSpace(top.GW.Must("wg", "genpsk"))
	pubA := strings.TrimSpace(top.GW.MustStdin(priv+"\n", "wg", "pubkey"))
	peerPub := strings.TrimSpace(top.GW.MustStdin(strings.TrimSpace(top.GW.Must("wg", "genkey"))+"\n", "wg", "pubkey"))
	ex, err := executor.New(executor.NewExecRunner(), executor.WithKeys(func(id string) (string, string, error) {
		switch id {
		case wgKeyRef:
			return priv, "", nil
		case wgPeerRef:
			return "", psk, nil
		}
		return "", "", errors.New("unknown")
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ex.Close)
	ctx := context.Background()
	ns := executor.Target{NS: top.GW.Name}
	ensure := func(allowed ...string) *executor.WireGuard {
		return &executor.WireGuard{Target: ns, Action: "ensure", Name: "wg-t", ListenPort: 51899, MTU: 1380, KeyRef: wgKeyRef,
			Peers: []executor.WGPeer{{PublicKey: peerPub, PresharedKeyRef: wgPeerRef, AllowedIPs: allowed, Keepalive: 25, Endpoint: "203.0.113.40:51821"}}}
	}
	if _, err := ex.DoBatch(ctx, []executor.Operation{&executor.AssignInterfaces{Target: ns, Devs: []string{"wg-t"}}, ensure("10.99.0.2/32")}); err != nil {
		t.Fatal(err)
	}
	if out := top.GW.Must("ip", "-d", "-o", "link", "show", "dev", "wg-t"); !strings.Contains(out, "wireguard") || !strings.Contains(out, "mtu 1380") {
		t.Errorf("%s", out)
	}
	dump := top.GW.Must("wg", "show", "wg-t", "dump")
	if !strings.Contains(dump, pubA) || !strings.Contains(dump, "51899") || !strings.Contains(dump, "10.99.0.2/32") || !strings.Contains(dump, "203.0.113.40:51821") {
		t.Errorf("dump:\n%s", dump)
	}
	// synchronizing again with other allowed ips changes the peer in place
	if _, err := ex.Do(ctx, ensure("10.99.0.2/32", "10.50.0.0/24")); err != nil {
		t.Fatal(err)
	}
	if out := top.GW.Must("wg", "show", "wg-t", "allowed-ips"); !strings.Contains(out, "10.50.0.0/24") {
		t.Errorf("%s", out)
	}
	// the read result holds no secret
	out, err := ex.Do(ctx, &executor.Read{Target: ns, What: executor.ReadWireGuard, Dev: "wg-t"})
	if err != nil || strings.Contains(string(out.Data[0]), priv) || strings.Contains(string(out.Data[0]), psk) {
		t.Fatalf("%v %s", err, out.Data)
	}
	// a key that cannot be found fails and changes nothing
	bad := ensure("10.99.0.2/32")
	bad.KeyRef = "00000000-0000-4000-8000-000000000000"
	if _, err := ex.Do(ctx, bad); err == nil {
		t.Error("an unknown key must fail")
	}
	// delete: only a WireGuard interface
	if _, err := ex.Do(ctx, &executor.WireGuard{Target: ns, Action: "delete", Name: "wg-t"}); err != nil {
		t.Fatal(err)
	}
	if o, err := top.GW.Run(ctx, "ip", "link", "show", "dev", "wg-t"); err == nil {
		t.Errorf("still there: %s", o)
	}
	if _, err := ex.DoBatch(ctx, []executor.Operation{&executor.AssignInterfaces{Target: ns, Devs: []string{"wan0"}}, &executor.WireGuard{Target: ns, Action: "delete", Name: "wan0"}}); err == nil {
		t.Error("wan0 was deleted as a WireGuard interface")
	}
	if o := top.GW.Must("ip", "-o", "link", "show", "dev", "wan0"); o == "" {
		t.Error("wan0 is gone")
	}
}
