package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The duplication hook's operation (M10, P2-M10-01): a list of interfaces, a table the executor writes
// itself, and a read of it.

func TestNftDupTakesInterfacesAndNothingElse(t *testing.T) {
	for _, in := range []string{
		`{"type":"nft_dup","devs":["br-iot","br-lab","wan0"]}`,
		`{"type":"nft_dup","namespace":"gw","devs":["wg0"]}`,
		`{"type":"nft_dup","devs":[]}`,
		`{"type":"nft_dup"}`,
	} {
		if _, err := Decode([]byte(in)); err != nil {
			t.Errorf("%s: %v", in, err)
		}
	}
	for name, in := range map[string]string{
		"an interface twice":     `{"type":"nft_dup","devs":["wan0","wan0"]}`,
		"a bad name":             `{"type":"nft_dup","devs":["wan 0"]}`,
		"a long name":            `{"type":"nft_dup","devs":["abcdefghijklmnopq"]}`,
		"a rule":                 `{"type":"nft_dup","devs":["wan0"],"rule":"drop"}`,
		"a ruleset":              `{"type":"nft_dup","ruleset":{"nftables":[]}}`,
		"a table":                `{"type":"nft_dup","devs":["wan0"],"table":"other"}`,
		"a namespace with dots":  `{"type":"nft_dup","namespace":"../x","devs":["wan0"]}`,
		"an empty name":          `{"type":"nft_dup","devs":[""]}`,
		"devs of the wrong type": `{"type":"nft_dup","devs":"wan0"}`,
	} {
		if op, err := Decode([]byte(in)); err == nil {
			t.Errorf("%s: accepted %T", name, op)
		} else if !errors.Is(err, ErrDecode) {
			t.Errorf("%s: %v does not wrap ErrDecode", name, err)
		}
	}
}

// The transaction replaces the table in one commit and holds one chain per interface with the one rule.
func TestTheDuplicationTransactionReplacesTheTableWithOneChainAndOneRulePerInterface(t *testing.T) {
	steps := mustPlan(t, `{"type":"nft_dup","namespace":"gw","devs":["wan0","br-iot"]}`)
	if len(steps) != 1 || steps[0].Cmd.Tool != ToolNft || strings.Join(steps[0].Cmd.Args, " ") != "-j -f -" || steps[0].Cmd.NS != "gw" {
		t.Fatalf("%+v", steps)
	}
	var doc struct {
		Nftables []map[string]map[string]map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(steps[0].Cmd.Stdin), &doc); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, item := range doc.Nftables {
		for cmd, objs := range item {
			for kind, f := range objs {
				var fam, name, dev string
				_ = json.Unmarshal(f["family"], &fam)
				_ = json.Unmarshal(f["name"], &name)
				_ = json.Unmarshal(f["dev"], &dev)
				if fam != "netdev" {
					t.Errorf("%s %s is in the family %q", cmd, kind, fam)
				}
				var table string
				if kind == "table" {
					table = name
				} else {
					_ = json.Unmarshal(f["table"], &table)
				}
				if table != NftDupTable {
					t.Errorf("%s %s is in the table %q", cmd, kind, table)
				}
				seq = append(seq, strings.TrimSpace(cmd+" "+kind+" "+name+" "+dev))
			}
		}
	}
	// add, delete, add: the replacement; then the interfaces in sorted order
	want := "add table chaosgw_dup|delete table chaosgw_dup|add table chaosgw_dup|add chain egress_br-iot br-iot|add rule|add chain egress_wan0 wan0|add rule"
	if got := strings.Join(seq, "|"); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	// no interface: the table ends deleted, and nothing is added after it
	steps = mustPlan(t, `{"type":"nft_dup","devs":[]}`)
	if strings.Count(steps[0].Cmd.Stdin, `"add"`) != 1 || strings.Count(steps[0].Cmd.Stdin, `"delete"`) != 1 {
		t.Errorf("%s", steps[0].Cmd.Stdin)
	}
	if strings.Index(steps[0].Cmd.Stdin, `"add"`) > strings.Index(steps[0].Cmd.Stdin, `"delete"`) {
		t.Errorf("the table is deleted before it exists: %s", steps[0].Cmd.Stdin)
	}
}

// The rule is the one thing the table may hold: if the flag is set, clear it, then copy to the same interface.
func TestTheRuleOfTheHookClearsTheFlagBeforeItCopies(t *testing.T) {
	b, _ := json.Marshal(DupRuleExpr("wan0"))
	got := string(b)
	// 0x200000 = 2097152; the clear mask 0xffdfffff = 4292870143; dup is last, to the interface
	for _, want := range []string{`"op":"=="`, `2097152`, `4292870143`, `{"dup":{"addr":"wan0"}}`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is not in %s", want, got)
		}
	}
	if strings.Index(got, "mangle") > strings.Index(got, `"dup"`) {
		t.Errorf("the packet is copied before the flag is cleared: %s", got)
	}
	if DupRuleComment("wan0") == DupRuleComment("wan1") || len(DupRuleComment("wan0")) != 12 {
		t.Errorf("the comment tells the rules of two interfaces apart: %s %s", DupRuleComment("wan0"), DupRuleComment("wan1"))
	}
	if DupMarkBit != 1<<21 || dupMarkClear != 0xffdfffff {
		t.Errorf("%#x %#x", DupMarkBit, dupMarkClear)
	}
}

func TestOnlyAssignedInterfacesGetTheHook(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) { return Result{}, nil }}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, `{"type":"assign_interfaces","devs":["wan0"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Do(context.Background(), mustDecode(t, `{"type":"nft_dup","devs":["wan0"]}`)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(context.Background(), mustDecode(t, `{"type":"nft_dup","devs":["wan0","eth9"]}`))
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("an interface that is not assigned: %v", err)
	}
	for _, c := range fr.commands() {
		if strings.Contains(c.Stdin, "eth9") {
			t.Errorf("the executor ran a command for an interface out of its scope: %s", c)
		}
	}
}

func TestTheDuplicationTableIsReadAndAMissingOneIsEmpty(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if strings.Join(c.Args, " ") != "-j list table netdev chaosgw_dup" {
			t.Errorf("the read runs %s", c)
		}
		return Result{Exit: 1, Stderr: "Error: No such file or directory\nlist table netdev chaosgw_dup\n"}, nil
	}}
	out, err := newExec(t, fr).Do(context.Background(), mustDecode(t, `{"type":"read","what":"nft_dup"}`))
	if err != nil {
		t.Fatal(err)
	}
	var rs struct{ Objects []any }
	if err := json.Unmarshal(out.Data[0], &rs); err != nil || len(rs.Objects) != 0 {
		t.Errorf("%s %v", out.Data[0], err)
	}
	// another failure is a failure
	fr = &fakeRunner{respond: func(c Command) (Result, error) { return Result{Exit: 1, Stderr: "Error: Operation not permitted"}, nil }}
	if _, err := newExec(t, fr).Do(context.Background(), mustDecode(t, `{"type":"read","what":"nft_dup"}`)); err == nil {
		t.Error("a read that failed was taken for an empty table")
	}
	// a table that is there
	fr = &fakeRunner{respond: func(c Command) (Result, error) {
		return Result{Stdout: `{"nftables":[{"metainfo":{"json_schema_version":1}},{"table":{"family":"netdev","name":"chaosgw_dup","handle":1}},` +
			`{"chain":{"family":"netdev","table":"chaosgw_dup","name":"egress_wan0","handle":2,"dev":"wan0","type":"filter","hook":"egress","prio":0,"policy":"accept"}}]}`}, nil
	}}
	out, err = newExec(t, fr).Do(context.Background(), mustDecode(t, `{"type":"read","what":"nft_dup"}`))
	if err != nil || !strings.Contains(string(out.Data[0]), `"wan0"`) {
		t.Errorf("%v %s", err, out.Data[0])
	}
}
