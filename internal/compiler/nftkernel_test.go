//go:build testbed

package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// kernelCheck asks the kernel of the testbed whether it accepts the batch: `nft -c` runs the whole
// transaction through the kernel and aborts it, so nothing changes. The empty namespace is the
// state of a first apply.
func kernelCheck(ns *testbed.Namespace, cmds []json.RawMessage) error {
	doc, err := json.Marshal(map[string]any{"nftables": cmds})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := ns.Command(ctx, "nft", "-j", "-c", "-f", "-")
	cmd.Stdin = bytes.NewReader(doc)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// report says which commands the kernel refuses.
func report(ns *testbed.Namespace, cmds []json.RawMessage) string {
	check := func(prefix []json.RawMessage) error { return kernelCheck(ns, prefix) }
	first := firstRejected(len(cmds), func(n int) error { return check(cmds[:n]) })
	if first < 0 {
		return "the kernel accepts the batch on a second look (flaky?)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "first rejected command: #%d of %d\n", first, len(cmds))
	for _, r := range allRejected(cmds, check) {
		b.WriteString("  " + r.String() + "\n")
	}
	return b.String()
}

// The nftables transaction of every compiled configuration is accepted by the real kernel. The
// compiler's golden tests pin the text and the parser test checks the syntax, but only the kernel
// knows what it supports: it refuses the whole atomic batch for one unsupported expression, which
// then fails every apply and every test that applies a configuration. This test finds that in the
// first VM run, before any traffic test.
func TestEveryCompiledRulesetIsAcceptedByTheKernel(t *testing.T) {
	bed := testbed.New(t)
	ns := bed.Add("nftchk")
	for name, tg := range transactionScenarios(t) {
		t.Run(name, func(t *testing.T) {
			tx, err := tg.Nft.Transaction(nil)
			if err != nil {
				t.Fatal(err)
			}
			cmds, err := nftCommands(tx)
			if err != nil {
				t.Fatal(err)
			}
			if err := kernelCheck(ns, cmds); err != nil {
				t.Errorf("the kernel rejects the ruleset of %q: %v\n%s", name, err, report(ns, cmds))
			}
		})
	}
}

// The cut windows of the access rules (M9) are transactions of their own that run on top of an
// applied ruleset: the kernel must accept every window the plan can open, and the one that closes
// it. The ruleset is applied for real in a fresh namespace (the chains the windows fill must exist),
// the windows are checked with `nft -c`.
func TestEveryCutWindowIsAcceptedByTheKernel(t *testing.T) {
	bed := testbed.New(t)
	for name, tg := range accessScenarios(t) {
		if !tg.Access.HasCuts() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			ns := bed.Add("cutchk-" + strings.TrimPrefix(name, "access-"))
			tx, err := tg.Nft.Transaction(nil)
			if err != nil {
				t.Fatal(err)
			}
			ns.MustStdin(string(tx), "nft", "-j", "-f", "-")
			var keys []string
			for _, r := range tg.Access.Rules {
				keys = append(keys, r.Key)
			}
			windows := [][]string{nil, keys}
			for _, r := range tg.Access.Rules {
				windows = append(windows, []string{r.Key})
			}
			for _, w := range windows {
				tx, err := tg.Access.CutTransaction(w)
				if err != nil {
					t.Fatal(err)
				}
				cmds, err := nftCommands(tx)
				if err != nil {
					t.Fatal(err)
				}
				if err := kernelCheck(ns, cmds); err != nil {
					t.Errorf("the kernel rejects the cut window for %v: %v\n%s", w, err, report(ns, cmds))
				}
			}
		})
	}
}

// The gate must be able to fail: a batch with a command that every kernel refuses (a jump to a
// chain that does not exist) is rejected, and the bisection names exactly that command.
func TestTheKernelGateNamesTheRejectedCommand(t *testing.T) {
	bed := testbed.New(t)
	ns := bed.Add("nftgate")
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	cmds := []json.RawMessage{
		raw(`{"add":{"table":{"family":"inet","name":"gatecheck"}}}`),
		raw(`{"add":{"chain":{"family":"inet","table":"gatecheck","name":"c"}}}`),
		raw(`{"add":{"rule":{"family":"inet","table":"gatecheck","chain":"c","expr":[{"counter":null},{"accept":null}]}}}`),
		raw(`{"add":{"rule":{"family":"inet","table":"gatecheck","chain":"c","expr":[{"jump":{"target":"nosuchchain"}}]}}}`),
		raw(`{"add":{"rule":{"family":"inet","table":"gatecheck","chain":"c","expr":[{"counter":null},{"drop":null}]}}}`),
	}
	if err := kernelCheck(ns, cmds[:3]); err != nil {
		t.Fatalf("the good prefix must be accepted: %v", err)
	}
	if err := kernelCheck(ns, cmds); err == nil {
		t.Fatal("the kernel accepted a jump to a chain that does not exist")
	}
	if got := firstRejected(len(cmds), func(n int) error { return kernelCheck(ns, cmds[:n]) }); got != 3 {
		t.Errorf("the bisection found command #%d, want #3", got)
	}
	rej := allRejected(cmds, func(c []json.RawMessage) error { return kernelCheck(ns, c) })
	if len(rej) != 1 || rej[0].Index != 3 {
		t.Errorf("rejected in context: %v", rej)
	}
}

// The tc tree of every compiled fault target is accepted by the real kernel. `nft -c` cannot check
// tc commands, so this test runs them: the exact command lines the executor plans for the tree
// (qdisc, class, filter with netem, htb and fw parameters), twice (a re-apply is a `replace` of what
// exists), on dummy interfaces of the names the target uses; then it reads the tree back and counts.
// A tc construct that the kernel or iproute2 refuses fails here, before any apply test.
func TestEveryCompiledTCTreeIsAcceptedByTheKernel(t *testing.T) {
	bed := testbed.New(t)
	for name, tg := range faultScenarios(t) {
		if tg.TC == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			ns := bed.Add("tcchk-" + strings.TrimPrefix(name, "faults-"))
			dev := tg.TC.Devs[0]
			ns.Must("ip", "link", "add", dev, "type", "dummy")
			ns.Must("ip", "link", "set", dev, "up")
			for round := 1; round <= 2; round++ {
				// the second round is a re-apply onto what exists: the root stays, all else is replaced
				steps, err := executor.Plan(&executor.TC{Target: executor.Target{}, Entries: tg.TC.Entries(dev, round == 1)})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				cmd := ns.Command(ctx, "tc", steps[0].Cmd.Args...)
				cmd.Stdin = strings.NewReader(steps[0].Cmd.Stdin)
				out, err := cmd.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("round %d: the kernel refuses the tc tree: %v\n%s\n%s", round, err, out, steps[0].Cmd.Stdin)
				}
				if strings.Contains(string(out), "Warning") {
					t.Errorf("round %d: tc warns: %s", round, out)
				}
			}
			// the normalized state of the interface is exactly the compiler's prediction of it (M8b):
			// this is what verification will compare, so every attribute the compiler emits has to be
			// one the listing shows the way Norm says
			have, err := normalizedTC(ns, dev)
			if err != nil {
				t.Fatal(err)
			}
			if d := linux.DiffTC(tg.TC.Norm(dev), have.Subtree(TCRootHandle)); len(d) != 0 {
				t.Errorf("the kernel holds another tree than the compiler predicts:\n%s", strings.Join(d, "\n"))
			}
			var qdiscs []struct{ Kind, Handle, Parent string }
			if err := json.Unmarshal([]byte(ns.Must("tc", "-j", "qdisc", "show", "dev", dev)), &qdiscs); err != nil {
				t.Fatal(err)
			}
			netems := 0
			for _, q := range qdiscs {
				if q.Kind == "netem" {
					netems++
				}
			}
			if netems != len(tg.TC.Classes) {
				t.Errorf("%d netem qdiscs, want one per class: %d", netems, len(tg.TC.Classes))
			}
			var filters []struct {
				Kind    string
				Options *struct {
					Fw      struct{ Mark, Mask string }
					Classid string
				}
			}
			if err := json.Unmarshal([]byte(ns.Must("tc", "-j", "filter", "show", "dev", dev)), &filters); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{}
			for _, c := range tg.TC.Classes {
				want[fmt.Sprintf("%#x/0x1fff0", c.Mark)] = c.ClassID()
			}
			got := map[string]string{}
			for _, f := range filters {
				if f.Options != nil {
					got[f.Options.Fw.Mark+"/"+f.Options.Fw.Mask] = f.Options.Classid
				}
			}
			if len(got) != len(want) {
				t.Errorf("%d fw filters, want %d: %v", len(got), len(want), got)
			}
			for mark, class := range want {
				if got[mark] != class {
					t.Errorf("filter %s selects %q, want %s", mark, got[mark], class)
				}
			}
		})
	}
}

// The values at the edge of what the API accepts (many decimals, very high rates) are written as tokens
// the executor takes and the kernel reads back as the compiler predicts: rounded to nine decimals and
// to whole Gbit/s from a terabit on (found by review: the tokens were refused by the executor).
func TestTheKernelTakesTheLongestValuesTheAPIAccepts(t *testing.T) {
	bed := testbed.New(t)
	for name, fault := range map[string]string{
		"decimals":      `{loss: 5.0000000001%, duplicate: 0.0000000001%, corrupt: 33.3333333333%}`,
		"burst":         `{burst_loss: {p: 1.00000000001%, r: 30.33333333333%, h: 10.1234567891011%, k: 99.99999999999%}}`,
		"a huge rate":   `{rate: 1000000000001bit}`,
		"a larger rate": `{rate: 99999999999999999999Gbit}`,
		"an odd rate":   `{rate: 123456789.123456789Mbit}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := newFaultWorld(t)
			w.overlay(`{target: {device: esp32-42}, fault: `+fault+`}`, 0)
			tg := w.compile(nil)
			if tg.HasErrors() || tg.TC == nil {
				t.Fatalf("%+v", tg.Problems)
			}
			ns := bed.Add("tcedge-" + strings.ReplaceAll(name, " ", "-"))
			dev := tg.TC.Devs[0]
			ns.Must("ip", "link", "add", dev, "type", "dummy")
			ns.Must("ip", "link", "set", dev, "up")
			steps, err := executor.Plan(&executor.TC{Target: executor.Target{}, Entries: tg.TC.Entries(dev, true)})
			if err != nil {
				t.Fatalf("the executor refuses the tree: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := ns.Command(ctx, "tc", steps[0].Cmd.Args...)
			cmd.Stdin = strings.NewReader(steps[0].Cmd.Stdin)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the kernel refuses the tc tree: %v\n%s\n%s", err, out, steps[0].Cmd.Stdin)
			}
			have, err := normalizedTC(ns, dev)
			if err != nil {
				t.Fatal(err)
			}
			if d := linux.DiffTC(tg.TC.Norm(dev), have.Subtree(TCRootHandle)); len(d) != 0 {
				t.Errorf("the kernel holds another tree than the compiler predicts:\n%s", strings.Join(d, "\n"))
			}
		})
	}
}

// normalizedTC reads the tc state of an interface the way the executor does, the filters of the ingress
// qdisc included.
func normalizedTC(ns *testbed.Namespace, dev string) (*linux.NormTree, error) {
	return linux.NormalizeTCIngress(dev, []byte(ns.Must("tc", "-s", "-j", "qdisc", "show", "dev", dev)),
		[]byte(ns.Must("tc", "-s", "-j", "class", "show", "dev", dev)), []byte(ns.Must("tc", "-s", "-j", "filter", "show", "dev", dev)),
		[]byte(ns.Must("tc", "-s", "-j", "filter", "show", "dev", dev, "ingress")))
}

// The tunnel faults' tc side (M10, plan §2.2.1) is accepted by the real kernel: the tree of the IFB with its flower
// filters, the ingress qdisc of the uplink with the flower filters that redirect to the IFB, and the download
// classes in the tree of the interfaces; twice (a re-apply is a `replace`); and the normalized state is what the compiler
// predicts, including the selector, the handle and the redirect of the flower filters. A deletion of a filter by handle
// and of the ingress qdisc is accepted as well.
func TestEveryCompiledTunnelTreeIsAcceptedByTheKernel(t *testing.T) {
	bed := testbed.New(t)
	tg := scenarioTunnel(t)
	if tg.HasErrors() || tg.IFB == nil || tg.TC == nil {
		t.Fatalf("%+v", tg.Problems)
	}
	ns := bed.Add("tunchk")
	up := tg.IFB.Uplink
	ns.Must("ip", "link", "add", up, "type", "dummy")
	ns.Must("ip", "link", "set", up, "up")
	ns.Must("ip", "link", "add", "name", executor.IFBName, "type", "ifb")
	ns.Must("ip", "link", "set", executor.IFBName, "up")
	for round := 1; round <= 2; round++ {
		runTC(t, ns, tg.TC.Entries(up, round == 1))
		runTC(t, ns, tg.IFB.TC.Entries(IFBDev, round == 1))
		runTC(t, ns, tg.IFB.IngressEntries())
	}
	have, err := normalizedTC(ns, IFBDev)
	if err != nil {
		t.Fatal(err)
	}
	if d := linux.DiffTC(tg.IFB.TC.Norm(IFBDev), have.Subtree(TCRootHandle)); len(d) != 0 {
		t.Errorf("the IFB holds another tree than the compiler predicts:\n%s", strings.Join(d, "\n"))
	}
	haveUp, err := normalizedTC(ns, up)
	if err != nil {
		t.Fatal(err)
	}
	if d := linux.DiffTC(tg.TC.Norm(up), haveUp.Subtree(TCRootHandle)); len(d) != 0 {
		t.Errorf("the uplink holds another tree than the compiler predicts:\n%s", strings.Join(d, "\n"))
	}
	if d := linux.DiffTC(tg.IFB.IngressNorm(), haveUp.Ingress()); len(d) != 0 {
		t.Errorf("the ingress side is not what the compiler predicts:\n%s", strings.Join(d, "\n"))
	}
	// deleting what the plan deletes: a filter of the ingress qdisc by its handle, a filter of the IFB, then the qdisc
	for _, f := range haveUp.Ingress().Filters {
		runTC(t, ns, []executor.TCEntry{{Object: "filter", Action: "delete", Dev: up, Parent: IngressHandle, Handle: fmt.Sprint(f.Flower.Handle),
			Args: []string{"protocol", f.Protocol, "prio", fmt.Sprint(f.Pref), f.Kind}}})
	}
	for _, f := range have.Subtree(TCRootHandle).Filters {
		runTC(t, ns, []executor.TCEntry{{Object: "filter", Action: "delete", Dev: IFBDev, Parent: TCRootHandle, Handle: fmt.Sprint(f.Flower.Handle),
			Args: []string{"protocol", f.Protocol, "prio", fmt.Sprint(f.Pref), f.Kind}}})
	}
	runTC(t, ns, []executor.TCEntry{{Object: "qdisc", Action: "delete", Dev: up, Parent: "ingress"}})
	after, err := normalizedTC(ns, up)
	if err != nil {
		t.Fatal(err)
	}
	if ing := after.Ingress(); len(ing.Qdiscs) != 0 || len(ing.Filters) != 0 {
		t.Errorf("the ingress side is not gone: %+v", ing)
	}
}

// An element transaction (a device gets a new address, so its elements and the borders of the
// stretches around it move) is accepted by the real kernel and leaves the maps exactly as a full
// apply of the new target would: delete and add in one commit, even where the new element
// overlaps one that was there (an interval map would refuse the add on its own).
func TestAnElementTransactionMovesIntervalElementsOnTheRealKernel(t *testing.T) {
	bed := testbed.New(t)
	ns := bed.Add("nftelem")
	w := newFaultWorld(t)
	w.overlay(`{target: {network: IoT}, fault: {latency: 100ms, rate: 2Mbit}}`, 0)
	w.overlay(`{target: {global: true}, fault: {destination: {cidr: 203.0.113.0/24}, loss: 1%}}`, time.Second)
	w.overlay(`{target: {device: esp32-43}, fault: {protocol: tcp, port_ranges: [{from: 80, to: 443}], loss: 2%}}`, 2*time.Second)
	before := w.compile(nil)
	w.setAddrs(map[string][]string{devESP42: {"10.10.0.200"}, devESP43: {"10.10.0.43"}, devLab: {"10.20.0.50"}})
	after := w.compile(nil)
	if before.HasErrors() || after.HasErrors() {
		t.Fatalf("%+v %+v", before.Problems, after.Problems)
	}

	run := func(label string, tx []byte) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := ns.Command(ctx, "nft", "-j", "-f", "-")
		cmd.Stdin = bytes.NewReader(tx)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", label, err, out)
		}
	}
	full, err := before.Nft.Transaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	run("the first apply", full)

	var updates []MapUpdate
	for _, m := range after.Nft.Maps {
		var old *MapDef
		for i := range before.Nft.Maps {
			if before.Nft.Maps[i].Name == m.Name {
				old = &before.Nft.Maps[i]
			}
		}
		if old == nil {
			t.Fatalf("map %s is new", m.Name)
		}
		if u := DiffMap(*old, m); !u.Empty() {
			updates = append(updates, u)
		}
	}
	if len(updates) == 0 {
		t.Fatal("the address change changed no element")
	}
	tx, err := after.Nft.ElementTransaction(updates)
	if err != nil {
		t.Fatal(err)
	}
	run("the element transaction", tx)

	rs, err := linux.ParseNft([]byte(ns.Must("nft", "-j", "list", "ruleset")))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range after.Nft.Maps {
		got := rs.Set(m.Name)
		if got == nil {
			t.Fatalf("map %s is missing", m.Name)
		}
		have := got.Pairs()
		want := map[string]string{}
		for _, e := range m.Elements {
			want[linux.NormalizeElement(e.Key)] = e.Value
		}
		if len(have) != len(want) {
			t.Errorf("map %s holds %d elements, want %d\n have %v\n want %v", m.Name, len(have), len(want), have, want)
		}
		for k, v := range want {
			if have[k] != v {
				t.Errorf("map %s: %s is %q, want %q", m.Name, k, have[k], v)
			}
		}
	}
}

// applyTC runs the executor's own plan for a tree on an interface of a namespace and reads the
// state back in the normalized form.
func applyTC(t *testing.T, ns *testbed.Namespace, tc *TCTarget, dev string, withRoot bool) *linux.NormTree {
	t.Helper()
	runTC(t, ns, tc.Entries(dev, withRoot))
	have, err := normalizedTC(ns, dev)
	if err != nil {
		t.Fatal(err)
	}
	return have.Subtree(TCRootHandle)
}

// runTC runs the executor's own plan for entries in a namespace.
func runTC(t *testing.T, ns *testbed.Namespace, entries []executor.TCEntry) {
	t.Helper()
	steps, err := executor.Plan(&executor.TC{Target: executor.Target{}, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, st := range steps {
		cmd := ns.Command(ctx, "tc", st.Cmd.Args...)
		cmd.Stdin = strings.NewReader(st.Cmd.Stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the kernel refuses the tc tree: %v\n%s\n%s", err, out, st.Cmd.Stdin)
		}
	}
}

// What the compiler predicts the kernel reports is right for every shape of netem configuration,
// including the values at the edges of what a listing can show: probabilities of a few millionths of
// a percent and just below 100 %, a delay of one microsecond and of twelve seconds, rates the kernel
// truncates to no rate at all. The same tree is then changed in place to each shape in turn (the
// same handles, `replace`), so what the second round predicts has to hold for a qdisc that had other
// values, too. A duplicating fault is among them: its netem leaf never duplicates (the kernel refuses
// that next to any other netem, P2-M8b-01), the copy is made by the hook of the duplication table
// (P2-M10-01).
func TestTheNormOfEveryNetemShapeIsWhatTheKernelReports(t *testing.T) {
	bed := testbed.New(t)
	ms := time.Millisecond
	shapes := []Netem{
		{Limit: 1000},
		{Limit: 5000, Delay: 100 * ms},
		{Limit: 1000, Delay: 50 * ms, Jitter: 10 * ms, Distribution: "normal"},
		{Limit: 1000, Delay: 50 * ms, Jitter: 50 * ms, Distribution: "pareto"},
		{Limit: 1000, Delay: 50 * ms, Jitter: 20 * ms, Distribution: "paretonormal"},
		{Limit: 1000, Delay: time.Microsecond},
		{Limit: 1000, Delay: 1500 * time.Microsecond, Jitter: 400 * time.Microsecond},
		{Limit: 1000, Delay: 12*time.Second + 345678*time.Microsecond},
		{Limit: 1000, Loss: 0.0001},
		{Limit: 1000, Loss: 33.333333, LossCorr: 12.3456789},
		{Limit: 1000, Loss: 99.99999},
		{Limit: 1000, Loss: 100},
		{Limit: 1000, Delay: 10 * ms, Gemodel: &Gemodel{P: 1, R: 10, LossBad: 100, LossGood: 0}},
		{Limit: 1000, Delay: 10 * ms, Gemodel: &Gemodel{P: 0.5, R: 25, LossBad: 70, LossGood: 0.1}},
		{Limit: 1000, Delay: 10 * ms, Reorder: 25},
		{Limit: 1000, Delay: 10 * ms, Reorder: 0.00000001},
		{Limit: 1000, Corrupt: 0.1},
		{Limit: 1000, Corrupt: 100},
		{Limit: 1000, Delay: 20 * ms, Duplicate: 5},
		{Limit: 1000, Duplicate: 0.0000001},
		{Limit: 1000, Rate: 7},
		{Limit: 1000, Rate: 2_500_000},
		{Limit: 1000, Rate: 1_000_000_000},
		{Limit: 12345, Delay: 600 * ms, Loss: 1, Rate: 8_000_000},
	}
	mk := func(shapes []Netem, offset int) *TCTarget {
		tc := &TCTarget{Devs: []string{"d0"}}
		for i, n := range shapes {
			id := i + 1 + offset
			tc.Classes = append(tc.Classes, TCClass{ID: id, Dir: Upload, Minor: classMinor(id, Upload), Mark: MarkOf(id, Upload), Netem: n})
		}
		return tc
	}
	ns := bed.Add("tcnorm")
	ns.Must("ip", "link", "add", "d0", "type", "dummy")
	ns.Must("ip", "link", "set", "d0", "up")
	// first round: every shape on its own class
	tc := mk(shapes, 0)
	if d := linux.DiffTC(tc.Norm("d0"), applyTC(t, ns, tc, "d0", true)); len(d) != 0 {
		t.Errorf("first apply:\n%s", strings.Join(d, "\n"))
	}
	// second round: the same classes, every class gets the shape of its neighbour
	rot := append(append([]Netem{}, shapes[1:]...), shapes[0])
	tc2 := mk(rot, 0)
	if d := linux.DiffTC(tc2.Norm("d0"), applyTC(t, ns, tc2, "d0", false)); len(d) != 0 {
		t.Errorf("change in place:\n%s", strings.Join(d, "\n"))
	}
	// and a complete reset to the neutral set: nothing of the earlier shape survives
	neutral := make([]Netem, len(shapes))
	for i := range neutral {
		neutral[i] = Netem{Limit: 1000}
	}
	tc3 := mk(neutral, 0)
	if d := linux.DiffTC(tc3.Norm("d0"), applyTC(t, ns, tc3, "d0", false)); len(d) != 0 {
		t.Errorf("reset to neutral:\n%s", strings.Join(d, "\n"))
	}
}

// The duplication hook's table is accepted by the real kernel as the executor writes it, replaced by a
// transaction of other interfaces, and gone with an empty one; read back it is what the apply's verify
// expects: one egress base chain per interface, bound to it, with the one rule the executor writes.
func TestTheKernelAcceptsTheDuplicationHookAndReadsItBackAsTheVerifyExpects(t *testing.T) {
	bed := testbed.New(t)
	ns := bed.Add("dupnft")
	for _, d := range []string{"d0", "d1", "d2"} {
		ns.Must("ip", "link", "add", d, "type", "dummy")
		ns.Must("ip", "link", "set", d, "up")
	}
	run := func(devs ...string) {
		t.Helper()
		tx, err := executor.DupTransaction(devs)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := ns.Command(ctx, "nft", "-j", "-f", "-")
		cmd.Stdin = bytes.NewReader(tx)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the kernel refuses the table for %v: %v\n%s\n%s", devs, err, out, tx)
		}
	}
	read := func() *linux.Ruleset {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := ns.Run(ctx, "nft", "-j", "list", "table", executor.NftDupFamily, executor.NftDupTable)
		if err != nil {
			return &linux.Ruleset{} // no such table
		}
		rs, err := linux.ParseNft([]byte(out))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	chains := func(rs *linux.Ruleset) string {
		var devs []string
		for _, o := range rs.Objects {
			if c := o.Chain; c != nil {
				if c.Name != executor.DupChainName(c.Dev) || c.Type != "filter" || c.Hook != "egress" || c.Prio == nil || *c.Prio != 0 || c.Policy != "accept" {
					t.Errorf("a chain that is not the executor's: %+v", c)
				}
				if rules := rs.Rules(c.Name); len(rules) != 1 || rules[0].Comment != executor.DupRuleComment(c.Dev) {
					t.Errorf("the rules of %s: %+v", c.Name, rules)
				}
				devs = append(devs, c.Dev)
			}
		}
		return strings.Join(devs, " ")
	}
	run("d0", "d1")
	if got := chains(read()); got != "d0 d1" {
		t.Fatalf("chains on %q", got)
	}
	run("d0", "d1") // the same again
	run("d2", "d1") // another set replaces it, in one transaction
	if got := chains(read()); got != "d1 d2" {
		t.Fatalf("chains on %q after the replacement", got)
	}
	run()
	if n := len(read().Tables()); n != 0 {
		t.Errorf("the table is still there")
	}
	run() // deleting what is not there is no error
	// an interface that does not exist is refused by the kernel
	tx, _ := executor.DupTransaction([]string{"nothere"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := ns.Command(ctx, "nft", "-j", "-f", "-")
	cmd.Stdin = bytes.NewReader(tx)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("a chain on an interface that does not exist was accepted: %s", out)
	}
}
