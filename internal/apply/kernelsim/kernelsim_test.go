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

// The tc simulator answers the way the kernel does for the cases the fault engine meets
// (docs/development.md, "tc operations on the kernel"), and lists what it holds in the format the
// normalizer reads.
func TestTheTCSimulatorRefusesWhatTheKernelRefusesAndKeepsWhatItKeeps(t *testing.T) {
	k := New()
	k.AddLink("d0", "02:00:00:00:00:09", "dummy", true)
	tc := func(force bool, lines ...string) executor.Result {
		args := []string{"-batch", "-"}
		if force {
			args = []string{"-force", "-batch", "-"}
		}
		r, err := k.Run(context.Background(), executor.Command{Tool: executor.ToolTC, Args: args, Stdin: strings.Join(lines, "\n") + "\n"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	read := func(kind string) string {
		r, _ := k.Run(context.Background(), executor.Command{Tool: executor.ToolTC, Args: []string{"-s", "-j", kind, "show", "dev", "d0"}})
		return r.Stdout
	}
	if got := read("qdisc"); !strings.Contains(got, `"kind":"noqueue"`) {
		t.Errorf("an interface without a tree has the host's noqueue: %s", got)
	}
	const class, leaf = "class replace dev d0 parent 1: classid 1:24 htb rate 10gbit quantum 60000", "qdisc replace dev d0 parent 1:24 handle 24: netem limit 1000 delay 50ms 5ms 0%"
	if r := tc(false, class); r.Exit == 0 {
		t.Error("a class below a root that is not there was accepted")
	}
	// `replace` takes over the host's root (the tool's `add` fails over an mq, the simulator is as
	// strict only for what the compiler needs)
	if r := tc(false, "qdisc replace dev d0 root handle 1: htb default 1"); r.Exit != 0 {
		t.Fatal(r.Stderr)
	}
	if got := read("qdisc"); strings.Contains(got, "noqueue") {
		t.Errorf("the host's root is still there: %s", got)
	}
	// and a root that exists can be neither added nor changed
	if r := tc(false, "qdisc add dev d0 root handle 1: htb default 1"); r.Exit == 0 || !strings.Contains(r.Stderr, "Exclusivity flag on") {
		t.Errorf("`add` of a second root: %+v", r)
	}
	if r := tc(false, "qdisc replace dev d0 root handle 1: htb default 1"); r.Exit == 0 || !strings.Contains(r.Stderr, "Change operation not supported") {
		t.Errorf("`replace` of an HTB root that exists: %+v", r)
	}
	if r := tc(false, class, leaf, "filter replace dev d0 parent 1: handle 0x000a0/0x1fff0 protocol ip prio 1 fw flowid 1:24"); r.Exit != 0 {
		t.Fatalf("%s", r.Stderr)
	}
	seed := k.TCSeed("d0", "24:")
	if seed == 0 {
		t.Fatal("no seed")
	}
	// a replace of the leaf is a change in place: same qdisc, same seed
	if r := tc(false, strings.Replace(leaf, "50ms", "60ms", 1)); r.Exit != 0 {
		t.Fatal(r.Stderr)
	}
	if k.TCSeed("d0", "24:") != seed || !strings.Contains(read("qdisc"), `"delay":0.06`) {
		t.Errorf("seed %d -> %d, %s", seed, k.TCSeed("d0", "24:"), read("qdisc"))
	}
	if r := tc(false, "class delete dev d0 classid 1:24"); r.Exit == 0 || !strings.Contains(r.Stderr, "HTB class in use") {
		t.Errorf("a class that a filter selects was deleted: %+v", r)
	}
	// a forced batch goes on after "not there"; the answers are the ones the executor knows as benign
	r := tc(true, "filter delete dev d0 parent 1: handle 0x000b0/0x1fff0 protocol ip prio 1 fw", "filter delete dev d0 parent 1: handle 0x000a0/0x1fff0 protocol ip prio 1 fw", "class delete dev d0 classid 1:99", "class delete dev d0 classid 1:24")
	if r.Exit == 0 || !strings.Contains(r.Stderr, "Specified filter handle not found") || !strings.Contains(r.Stderr, "Specified class not found") {
		t.Errorf("%+v", r)
	}
	if got := read("class"); strings.Contains(got, `"1:24"`) {
		t.Errorf("the class stayed although the filter before it went: %s", got)
	}
	// deleting the root takes everything with it and gives the host's queue back
	if r := tc(false, "qdisc delete dev d0 root handle 1:"); r.Exit != 0 {
		t.Fatal(r.Stderr)
	}
	if got := read("qdisc"); !strings.Contains(got, "noqueue") || strings.Contains(got, "netem") {
		t.Errorf("%s", got)
	}
	if r := tc(false, "qdisc delete dev d0 root handle 1:"); r.Exit == 0 {
		t.Error("deleting a root that is not there succeeded")
	}
	if r, _ := k.Run(context.Background(), executor.Command{Tool: executor.ToolTC, Args: []string{"-s", "-j", "qdisc", "show", "dev", "nope"}}); r.Exit == 0 {
		t.Error("an interface that does not exist")
	}
}
