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
