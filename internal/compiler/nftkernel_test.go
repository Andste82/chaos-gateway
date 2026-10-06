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
