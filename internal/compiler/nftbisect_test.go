package compiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The helpers of the kernel test (nftkernel_test.go). The kernel refuses a whole nftables
// transaction with one message that does not say which command it dislikes ("Could not process
// rule: Operation not supported"), so a rejected batch is bisected: the batch is additive, a
// prefix of it is a valid batch, and a prefix is rejected exactly when it contains a command the
// kernel refuses (a shift of the one-byte `ct direction` value is one such command: the kernel
// answers "Operation not supported" for the whole batch).

// nftCommands splits a transaction into its commands.
func nftCommands(tx []byte) ([]json.RawMessage, error) {
	var doc struct {
		Nftables []json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(tx, &doc); err != nil {
		return nil, err
	}
	return doc.Nftables, nil
}

// firstRejected returns the index of the first command that makes a batch rejected, or -1 when
// the whole batch of n commands is accepted. check tests the batch of the first prefix commands.
func firstRejected(n int, check func(prefix int) error) int {
	if check(n) == nil {
		return -1
	}
	lo, hi := 0, n // the batch of lo commands is taken as accepted, the one of hi is rejected
	for hi-lo > 1 {
		if mid := (lo + hi) / 2; check(mid) == nil {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi - 1
}

// rejection is one command the kernel refuses, with the kernel's message for it.
type rejection struct {
	Index   int
	Command string
	Err     error
}

func (r rejection) String() string {
	return fmt.Sprintf("command #%d: %s\n    kernel: %v", r.Index, r.Command, r.Err)
}

// maxRejections bounds the search: a batch with more refused commands than this is broken in
// ways that the first few already show.
const maxRejections = 12

// allRejected lists the commands the kernel refuses, each judged in the context of the commands
// that survive: the first refused command is found by bisection and taken out, and the rest is
// searched again. That costs about two kernel checks per refused command and log2 of the batch
// size, not one check per command. check tests a batch of commands.
func allRejected(cmds []json.RawMessage, check func([]json.RawMessage) error) []rejection {
	idx := make([]int, len(cmds)) // the original index of every remaining command
	cur := append([]json.RawMessage(nil), cmds...)
	for i := range idx {
		idx[i] = i
	}
	var out []rejection
	for len(out) < maxRejections {
		var lastErr error
		i := firstRejected(len(cur), func(n int) error {
			lastErr = check(cur[:n])
			return lastErr
		})
		if i < 0 {
			break
		}
		// the error of the batch that ends with the refused command, not of the last probe
		err := check(cur[:i+1])
		if err == nil {
			err = lastErr
		}
		cmd := string(cur[i])
		if len(cmd) > 600 {
			cmd = cmd[:600] + "..."
		}
		out = append(out, rejection{Index: idx[i], Command: cmd, Err: err})
		cur = append(cur[:i:i], cur[i+1:]...)
		idx = append(idx[:i:i], idx[i+1:]...)
	}
	return out
}

func fakeKernel(bad ...string) func([]json.RawMessage) error {
	return func(cmds []json.RawMessage) error {
		for _, c := range cmds {
			for _, b := range bad {
				if strings.Contains(string(c), b) {
					return errors.New("Operation not supported")
				}
			}
		}
		return nil
	}
}

func TestBisectionFindsTheFirstRejectedCommandForEveryPosition(t *testing.T) {
	for n := 0; n <= 70; n++ {
		for bad := 0; bad < n; bad++ {
			calls := 0
			got := firstRejected(n, func(prefix int) error {
				calls++
				if prefix > bad {
					return errors.New("rejected")
				}
				return nil
			})
			if got != bad {
				t.Fatalf("n=%d bad=%d: found %d", n, bad, got)
			}
			if limit := 2 + 7; calls > limit {
				t.Fatalf("n=%d: %d kernel checks, bisection should need about log2(n)", n, calls)
			}
		}
		if got := firstRejected(n, func(int) error { return nil }); got != -1 {
			t.Fatalf("n=%d: an accepted batch has no rejected command, got %d", n, got)
		}
	}
}

func TestEveryRejectedCommandIsListedInContext(t *testing.T) {
	cmds := []json.RawMessage{
		json.RawMessage(`{"add":{"table":"t"}}`),
		json.RawMessage(`{"add":{"rule":"BAD one"}}`),
		json.RawMessage(`{"add":{"chain":"c"}}`),
		json.RawMessage(`{"add":{"rule":"BAD two"}}`),
		json.RawMessage(`{"add":{"rule":"fine"}}`),
	}
	got := allRejected(cmds, fakeKernel("BAD"))
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 3 {
		t.Fatalf("%v", got)
	}
	if !strings.Contains(got[0].String(), "command #1") || !strings.Contains(got[0].String(), "Operation not supported") {
		t.Errorf("%s", got[0])
	}
	if len(allRejected(cmds, fakeKernel())) != 0 {
		t.Error("an accepting kernel rejects nothing")
	}
}

func TestATransactionIsSplitIntoItsCommands(t *testing.T) {
	cmds, err := nftCommands([]byte(`{"nftables":[{"add":{"table":{"name":"x"}}},{"flush":{"table":{"name":"x"}}}]}`))
	if err != nil || len(cmds) != 2 || !strings.Contains(string(cmds[1]), "flush") {
		t.Fatalf("%v %v", cmds, err)
	}
	if _, err := nftCommands([]byte(`not json`)); err == nil {
		t.Error("a broken transaction must be an error")
	}
	for name, tg := range transactionScenarios(t) {
		tx, err := tg.Nft.Transaction(nil)
		if err != nil {
			t.Fatal(err)
		}
		if cmds, err := nftCommands(tx); err != nil || len(cmds) < 5 {
			t.Errorf("%s: %d commands, %v", name, len(cmds), err)
		}
	}
}

func TestFindingARefusedCommandCostsAboutLogNKernelChecks(t *testing.T) {
	cmds := make([]json.RawMessage, 84)
	for i := range cmds {
		cmds[i] = json.RawMessage(fmt.Sprintf(`{"add":{"rule":"ok %d"}}`, i))
	}
	cmds[53] = json.RawMessage(`{"add":{"rule":"BAD"}}`)
	calls := 0
	inner := fakeKernel("BAD")
	got := allRejected(cmds, func(c []json.RawMessage) error { calls++; return inner(c) })
	if len(got) != 1 || got[0].Index != 53 {
		t.Fatalf("%v", got)
	}
	// each search is about log2(84) = 7 checks, plus one for the error text; two searches (the
	// second confirms nothing else is refused). Checking every command would cost 84.
	if calls > 25 {
		t.Errorf("%d kernel checks for one refused command in 84", calls)
	}
}
