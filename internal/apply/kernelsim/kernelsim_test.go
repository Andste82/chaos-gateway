package kernelsim

import (
	"context"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

func nft(t *testing.T, k *Kernel, doc string) executor.Result {
	t.Helper()
	r, err := k.Run(context.Background(), executor.Command{Tool: executor.ToolNft, Args: []string{"-j", "-f", "-"}, Stdin: doc})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const base = `{"add":{"table":{"family":"inet","name":"chaosgw"}}},{"add":{"counter":{"family":"inet","table":"chaosgw","name":"c1"}}},{"add":{"set":{"family":"inet","table":"chaosgw","name":"s1","type":"ifname"}}},{"add":{"chain":{"family":"inet","table":"chaosgw","name":"ch"}}}`

func TestTheSimulatorIsAsStrictAsNftablesWhereItMatters(t *testing.T) {
	k := New()
	if r := nft(t, k, `{"nftables":[`+base+`,{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"ch","comment":"r","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"@s1"}},{"counter":"c1"},{"drop":null}]}}}]}`); r.Exit != 0 {
		t.Fatal(r.Stderr)
	}
	for name, doc := range map[string]string{
		"a set with another type":      `{"add":{"set":{"family":"inet","table":"chaosgw","name":"s1","type":"ipv4_addr"}}}`,
		"delete a set in use":          `{"delete":{"set":{"family":"inet","table":"chaosgw","name":"s1"}}}`,
		"delete a counter in use":      `{"delete":{"counter":{"family":"inet","table":"chaosgw","name":"c1"}}}`,
		"delete a chain with rules":    `{"delete":{"chain":{"family":"inet","table":"chaosgw","name":"ch"}}}`,
		"rule on a missing chain":      `{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"nope","expr":[{"accept":null}]}}}`,
		"rule using a missing set":     `{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"ch","expr":[{"match":{"op":"==","left":{"meta":{"key":"iifname"}},"right":"@nope"}}]}}}`,
		"rule using a missing counter": `{"add":{"rule":{"family":"inet","table":"chaosgw","chain":"ch","expr":[{"counter":"nope"}]}}}`,
	} {
		if r := nft(t, k, `{"nftables":[`+doc+`]}`); r.Exit == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	// a transaction is atomic: the first command is undone when the second fails
	r := nft(t, k, `{"nftables":[{"add":{"counter":{"family":"inet","table":"chaosgw","name":"c2"}}},{"delete":{"set":{"family":"inet","table":"chaosgw","name":"s1"}}}]}`)
	if r.Exit == 0 {
		t.Fatal("deleting a set in use must fail")
	}
	if _, ok := k.Counter("c2"); ok {
		t.Error("the transaction was not atomic")
	}
	// flushing the chain first makes the delete in the same transaction possible
	r = nft(t, k, `{"nftables":[{"flush":{"chain":{"family":"inet","table":"chaosgw","name":"ch"}}},{"delete":{"set":{"family":"inet","table":"chaosgw","name":"s1"}}},{"delete":{"counter":{"family":"inet","table":"chaosgw","name":"c1"}}}]}`)
	if r.Exit != 0 {
		t.Fatal(r.Stderr)
	}
}

func TestListOfAMissingTableIsTheToolsError(t *testing.T) {
	k := New()
	r, _ := k.Run(context.Background(), executor.Command{Tool: executor.ToolNft, Args: []string{"-j", "list", "table", "inet", "chaosgw"}})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "No such file or directory") {
		t.Fatalf("%+v", r)
	}
}

func TestIPBatchForceContinuesAndReportsEveryFailure(t *testing.T) {
	k := New()
	k.AddLink("eth0", "02:00:00:00:00:09", "veth", true)
	r, _ := k.Run(context.Background(), executor.Command{Tool: executor.ToolIP, Args: []string{"-4", "-force", "-batch", "-"},
		Stdin: "rule del priority 5 table 100 protocol 201\nroute replace 10.0.0.0/8 table 100 proto 201 dev eth0\nroute del 10.9.0.0/16 table 100 proto 201 dev eth0\n"})
	if r.Exit == 0 || strings.Count(r.Stderr, "Command failed") != 2 {
		t.Fatalf("%+v", r)
	}
	if len(k.routes) != 1 {
		t.Error("with -force the line after a failure still runs")
	}
}

// A map is told apart by its flags like by its type: `add map` of an existing map with other flags
// fails in the kernel and with it the whole transaction, which is why the compiler's map names
// carry a hash of the flags. The simulator also lists them, so verify can compare them.
func TestAMapWithOtherFlagsIsRefusedAndTheFlagsAreListed(t *testing.T) {
	k := New()
	add := func(flags string) executor.Result {
		return nft(t, k, `{"nftables":[{"add":{"table":{"family":"inet","name":"chaosgw"}}},{"add":{"map":{"family":"inet","table":"chaosgw","name":"m","type":"ipv4_addr","map":"verdict"`+flags+`}}}]}`)
	}
	if r := add(`,"flags":["interval"]`); r.Exit != 0 {
		t.Fatal(r.Stderr)
	}
	if r := add(`,"flags":["interval"]`); r.Exit != 0 {
		t.Fatalf("the same map again: %s", r.Stderr)
	}
	if r := add(``); r.Exit == 0 {
		t.Error("the same map without its flags was accepted")
	}
	r, _ := k.Run(context.Background(), executor.Command{Tool: executor.ToolNft, Args: []string{"-j", "list", "table", "inet", "chaosgw"}})
	if !strings.Contains(r.Stdout, `"flags":["interval"]`) {
		t.Errorf("the flags are not listed: %s", r.Stdout)
	}
}
