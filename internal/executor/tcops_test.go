package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The tc operations of M8b: create, change in place and delete inside Chaos Gateway's own handles.
// What the kernel does with them is pinned by the testbed tests (integration_test.go, "tc operations
// on the kernel"); these tests cover the decoder, the plan and the executor's handling of answers.

const netemFull = `"netem","limit","5000","delay","50ms","10ms","0%","distribution","normal","loss","random","1%","25%","reorder","0%","0%","duplicate","0%","0%","corrupt","0%","0%","rate","0bit"`

func tcEntries(entries ...string) string {
	return `{"type":"tc","namespace":"gw","entries":[` + strings.Join(entries, ",") + `]}`
}

func TestTCAcceptsTheOperationsOfTheOwnTree(t *testing.T) {
	for name, e := range map[string]string{
		"create the root":         `{"object":"qdisc","action":"add","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","1"]}`,
		"replace the root":        `{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","1"]}`,
		"create a class":          `{"object":"class","action":"add","dev":"wan0","parent":"1:","classid":"1:24","args":["htb","rate","10gbit","quantum","60000"]}`,
		"replace a class":         `{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:24","args":["htb","rate","10gbit","quantum","60000"]}`,
		"change a class":          `{"object":"class","action":"change","dev":"wan0","parent":"1:","classid":"1:24","args":["htb","rate","1Mbit","ceil","2Mbit","burst","15k","prio","1"]}`,
		"create a leaf":           `{"object":"qdisc","action":"add","dev":"wan0","parent":"1:24","handle":"24:","args":[` + netemFull + `]}`,
		"change a leaf in place":  `{"object":"qdisc","action":"change","dev":"wan0","parent":"1:24","handle":"24:","args":[` + netemFull + `]}`,
		"replace a leaf":          `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"24:","args":[` + netemFull + `]}`,
		"gemodel":                 `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"24:","args":["netem","limit","1000","delay","0ms","0ms","0%","loss","gemodel","1%","10%","70%","0.1%","rate","2Mbit"]}`,
		"loss shorthand":          `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"24:","args":["netem","loss","1%","25%"]}`,
		"leaf handle in capitals": `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:1A","handle":"1a:","args":["netem","delay","5ms"]}`,
		"create a filter":         `{"object":"filter","action":"add","dev":"wan0","parent":"1:","handle":"0x000a0/0x1fff0","args":["protocol","ip","prio","1","fw","flowid","1:24"]}`,
		"replace a filter":        `{"object":"filter","action":"replace","dev":"wan0","parent":"1:","handle":"0x100a0/0x1fff0","args":["protocol","ip","prio","1","fw","flowid","1:25"]}`,
		"delete a filter":         `{"object":"filter","action":"delete","dev":"wan0","parent":"1:","handle":"0x000a0/0x1fff0","args":["protocol","ip","prio","1","fw"]}`,
		"delete a class":          `{"object":"class","action":"delete","dev":"wan0","classid":"1:24"}`,
		"delete a leaf":           `{"object":"qdisc","action":"delete","dev":"wan0","parent":"1:24","handle":"24:"}`,
		"delete the root":         `{"object":"qdisc","action":"delete","dev":"wan0","parent":"root","handle":"1:"}`,
		"ingress":                 `{"object":"qdisc","action":"add","dev":"wan0","parent":"ingress"}`,
		"delete ingress":          `{"object":"qdisc","action":"delete","dev":"wan0","parent":"ingress"}`,
		"a filter on the ingress": `{"object":"filter","action":"add","dev":"wan0","parent":"ffff:","args":["protocol","ip","u32","match","u32","0","0","action","drop"]}`,
	} {
		if _, err := Decode([]byte(tcEntries(e))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTCRejectsWhatLeavesTheOwnTree(t *testing.T) {
	netem := func(extra ...string) string {
		a := `"netem"`
		for _, x := range extra {
			a += `,"` + x + `"`
		}
		return `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"24:","args":[` + a + `]}`
	}
	htbClass := func(extra ...string) string {
		a := `"htb"`
		for _, x := range extra {
			a += `,"` + x + `"`
		}
		return `{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:24","args":[` + a + `]}`
	}
	for name, e := range map[string]string{
		// handles that are not ours: the operating system's root qdisc, an mq, the default qdisc
		"root without a handle":      `{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","args":["htb","default","1"]}`,
		"root with the handle of mq": `{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"8001:","args":["htb","default","1"]}`,
		"root with the zero handle":  `{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"0:","args":["htb","default","1"]}`,
		"root with another handle":   `{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"2:","args":["htb","default","1"]}`,
		"delete root without handle": `{"object":"qdisc","action":"delete","dev":"wan0","parent":"root"}`,
		"delete the root of an mq":   `{"object":"qdisc","action":"delete","dev":"wan0","parent":"root","handle":"8001:"}`,
		"leaf below an mq queue":     `{"object":"qdisc","action":"replace","dev":"wan0","parent":"8001:1","handle":"8003:","args":["netem","delay","5ms"]}`,
		"leaf below the root qdisc":  `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:","handle":"24:","args":["netem","delay","5ms"]}`,
		"leaf with another handle":   `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"99:","args":["netem","delay","5ms"]}`,
		"leaf with the root handle":  `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"1:","args":["netem","delay","5ms"]}`,
		"leaf without a handle":      `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","args":["netem","delay","5ms"]}`,
		"delete a leaf of another":   `{"object":"qdisc","action":"delete","dev":"wan0","parent":"8001:1","handle":"8003:"}`,
		"class of another major":     `{"object":"class","action":"replace","dev":"wan0","parent":"2:","classid":"2:24","args":["htb","rate","1mbit"]}`,
		"class below another major":  `{"object":"class","action":"replace","dev":"wan0","parent":"2:","classid":"1:24","args":["htb","rate","1mbit"]}`,
		"class with the minor zero":  `{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:0","args":["htb","rate","1mbit"]}`,
		"leaf of the minor zero":     `{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:0","handle":"0:","args":["netem","delay","5ms"]}`,
		"class without a minor":      `{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:","args":["htb","rate","1mbit"]}`,
		"class without a parent":     `{"object":"class","action":"replace","dev":"wan0","classid":"1:24","args":["htb","rate","1mbit"]}`,
		"delete class of another":    `{"object":"class","action":"delete","dev":"wan0","classid":"8001:1"}`,
		"filter on the mq":           `{"object":"filter","action":"add","dev":"wan0","parent":"8001:","handle":"0xa0","args":["protocol","ip","prio","1","fw","flowid","1:24"]}`,
		"filter into another class":  `{"object":"filter","action":"add","dev":"wan0","parent":"1:","handle":"0xa0","args":["protocol","ip","prio","1","fw","flowid","8001:1"]}`,
		"filter without a handle":    `{"object":"filter","action":"add","dev":"wan0","parent":"1:","args":["protocol","ip","prio","1","fw","flowid","1:24"]}`,
		"filter with a mirred":       `{"object":"filter","action":"add","dev":"wan0","parent":"1:","handle":"0xa0","args":["protocol","ip","prio","1","fw","flowid","1:24","action","drop"]}`,
		"filter with another flow":   `{"object":"filter","action":"add","dev":"wan0","parent":"1:","handle":"0xa0","args":["protocol","ip","prio","1","fw","classid","1:24"]}`,
		"filter on the root keyword": `{"object":"filter","action":"add","dev":"wan0","parent":"root","handle":"0xa0","args":["protocol","ip","prio","1","fw"]}`,
		"delete a filter by prio":    `{"object":"filter","action":"delete","dev":"wan0","parent":"1:","handle":"0xa0","args":["prio","1"]}`,
		"delete a filter unnamed":    `{"object":"filter","action":"delete","dev":"wan0","parent":"1:","args":["protocol","ip","prio","1","fw"]}`,
		"delete a filter flower":     `{"object":"filter","action":"delete","dev":"wan0","parent":"1:","handle":"0xa0","args":["protocol","ip","prio","1","flower"]}`,
		"delete with arguments":      `{"object":"class","action":"delete","dev":"wan0","classid":"1:24","args":["htb","rate","1mbit"]}`,
		"delete a leaf with kind":    `{"object":"qdisc","action":"delete","dev":"wan0","parent":"1:24","handle":"24:","args":["netem"]}`,

		// the netem grammar
		"netem without parameters":   netem(),
		"netem unknown keyword":      netem("slot", "1ms", "2ms"),
		"netem twice":                netem("delay", "5ms", "delay", "6ms"),
		"netem limit twice":          netem("limit", "10", "limit", "20"),
		"netem huge limit":           netem("limit", "20000000"),
		"netem limit not a number":   netem("limit", "many"),
		"netem negative limit":       netem("limit", "-1"),
		"netem delay without unit":   netem("delay", "50"),
		"netem delay in hours":       netem("delay", "1h"),
		"netem delay too many":       netem("delay", "1ms", "1ms", "1%", "1ms"),
		"netem delay percent first":  netem("delay", "10%"),
		"netem loss over 100":        netem("loss", "random", "101%"),
		"netem loss correlation":     netem("loss", "random", "1%", "2%", "3%"),
		"netem loss no value":        netem("loss", "random"),
		"netem loss model":           netem("loss", "state", "1%"),
		"netem gemodel too many":     netem("loss", "gemodel", "1%", "2%", "3%", "4%", "5%"),
		"netem distribution file":    netem("delay", "1ms", "1ms", "distribution", "../../etc/passwd"),
		"netem distribution custom":  netem("delay", "1ms", "1ms", "distribution", "custom"),
		"netem distribution uniform": netem("delay", "1ms", "1ms", "distribution", "uniform"),
		"netem rate without unit":    netem("rate", "100"),
		"netem rate overheads":       netem("rate", "1Mbit", "1", "2", "3", "4"),
		"netem seed text":            netem("seed", "abc"),
		"netem ecn with a value":     netem("ecn", "1"),
		"netem reorder 5 values":     netem("reorder", "1%", "2%", "3%"),
		"netem stray value":          netem("delay", "5ms", "7"),
		"netem percent decimals":     netem("loss", "random", "1.0000000001%"),

		// htb
		"htb class without a rate":   htbClass("quantum", "60000"),
		"htb class prio 8":           htbClass("rate", "1mbit", "prio", "8"),
		"htb class unknown":          htbClass("rate", "1mbit", "ceilx", "1"),
		"htb class rate twice":       htbClass("rate", "1mbit", "rate", "2mbit"),
		"htb class rate text":        htbClass("rate", "fast"),
		"htb qdisc unknown":          `{"object":"qdisc","action":"add","dev":"wan0","parent":"root","handle":"1:","args":["htb","offload"]}`,
		"htb qdisc default not hex":  `{"object":"qdisc","action":"add","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","xyz"]}`,
		"htb qdisc default too long": `{"object":"qdisc","action":"add","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","12345"]}`,
	} {
		if op, err := Decode([]byte(tcEntries(e))); err == nil {
			t.Errorf("%s: accepted %T", name, op)
		} else if !errors.Is(err, ErrDecode) {
			t.Errorf("%s: %v does not wrap ErrDecode", name, err)
		}
	}
}

// Whatever the decoder accepts for a tc operation names only handles of the own tree (or the
// ingress): the property the fuzz targets check on random input, spelled out for the plan.
func TestTheLinesOfAnAcceptedOperationStayInTheOwnHandles(t *testing.T) {
	steps := mustPlan(t, tcEntries(
		`{"object":"qdisc","action":"replace","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","1"]}`,
		`{"object":"class","action":"replace","dev":"wan0","parent":"1:","classid":"1:24","args":["htb","rate","10gbit","quantum","60000"]}`,
		`{"object":"qdisc","action":"replace","dev":"wan0","parent":"1:24","handle":"24:","args":[`+netemFull+`]}`,
		`{"object":"filter","action":"replace","dev":"wan0","parent":"1:","handle":"0x000a0/0x1fff0","args":["protocol","ip","prio","1","fw","flowid","1:24"]}`,
		`{"object":"filter","action":"delete","dev":"wan0","parent":"1:","handle":"0x000b0/0x1fff0","args":["protocol","ip","prio","1","fw"]}`,
		`{"object":"class","action":"delete","dev":"wan0","classid":"1:26"}`,
		`{"object":"qdisc","action":"delete","dev":"wan0","parent":"root","handle":"1:"}`,
	))
	if len(steps) != 2 {
		t.Fatalf("%d steps", len(steps))
	}
	for _, s := range steps {
		for _, l := range strings.Split(strings.TrimSpace(s.Cmd.Stdin), "\n") {
			checkTCLineInScope(t, l)
		}
	}
	want := `qdisc replace dev wan0 root handle 1: htb default 1
class replace dev wan0 parent 1: classid 1:24 htb rate 10gbit quantum 60000
qdisc replace dev wan0 parent 1:24 handle 24: netem limit 5000 delay 50ms 10ms 0% distribution normal loss random 1% 25% reorder 0% 0% duplicate 0% 0% corrupt 0% 0% rate 0bit
filter replace dev wan0 parent 1: handle 0x000a0/0x1fff0 protocol ip prio 1 fw flowid 1:24
`
	if steps[0].Cmd.Stdin != want || steps[0].Idempotent {
		t.Errorf("create/change step:\n%s", steps[0].Cmd.Stdin)
	}
	wantDel := `filter delete dev wan0 parent 1: handle 0x000b0/0x1fff0 protocol ip prio 1 fw
class delete dev wan0 classid 1:26
qdisc delete dev wan0 root handle 1:
`
	if s := steps[1]; s.Cmd.Stdin != wantDel || !s.Idempotent || strings.Join(s.Cmd.Args, " ") != "-force -batch -" {
		t.Errorf("delete step:\n%+v", s)
	}
}

// checkTCLineInScope fails when a tc batch line names a handle outside Chaos Gateway's tree.
func checkTCLineInScope(t *testing.T, line string) {
	t.Helper()
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		v := f[i+1]
		switch f[i] {
		case "parent":
			if _, ok := ownMinor(v); !ok && v != ownRootHandle && v != ingressHandle {
				t.Fatalf("parent %q is not an own handle: %q", v, line)
			}
		case "classid", "flowid":
			if _, ok := ownMinor(v); !ok {
				t.Fatalf("%s %q is not an own class: %q", f[i], v, line)
			}
		case "handle":
			if f[0] == "filter" {
				if !validFilterHandle(v) {
					t.Fatalf("filter handle %q: %q", v, line)
				}
			} else if v != ownRootHandle && !hexNum.MatchString(strings.TrimSuffix(v, ":")) {
				t.Fatalf("handle %q is not an own handle: %q", v, line)
			}
		}
	}
	if f[0] == "qdisc" && len(f) > 4 && f[4] == "root" && (len(f) < 7 || f[5] != "handle" || f[6] != ownRootHandle) {
		t.Fatalf("a root qdisc line without the own handle: %q", line)
	}
}

func TestTheRootHandleIsAlwaysSpelledOut(t *testing.T) {
	// the line of a root qdisc names `root handle 1:` in this order, so tc cannot read the handle
	// as something else
	steps := mustPlan(t, tcEntries(`{"object":"qdisc","action":"add","dev":"wan0","parent":"root","handle":"1:","args":["htb","default","1"]}`))
	if got := steps[0].Cmd.Stdin; got != "qdisc add dev wan0 root handle 1: htb default 1\n" {
		t.Errorf("%q", got)
	}
}

// A deletion of something that is gone is a success, the first failure of another kind is not.
func TestDeletingWhatIsGoneIsNotAnError(t *testing.T) {
	del := tcEntries(
		`{"object":"filter","action":"delete","dev":"wan0","parent":"1:","handle":"0xa0/0x1fff0","args":["protocol","ip","prio","1","fw"]}`,
		`{"object":"class","action":"delete","dev":"wan0","classid":"1:24"}`)
	for name, stderr := range map[string]string{
		"nothing printed":              "",
		"gone":                         "Error: Specified filter handle not found.\nWe have an error talking to the kernel\nCommand failed -:1\nRTNETLINK answers: No such file or directory\nCommand failed -:2\n",
		"class":                        "Error: Specified class not found.\nCommand failed -:2\n",
		"chain":                        "Error: Cannot find specified filter chain.\nWe have an error talking to the kernel\nCommand failed -:1\n",
		"qdisc":                        "Error: Failed to find qdisc with specified handle.\nCommand failed -:1\nError: Parent Qdisc doesn't exists.\nCommand failed -:2\nError: Invalid handle.\nCommand failed -:3\n",
		"leaf of a class that is gone": "Error: Failed to find qdisc with specified classid.\nCommand failed -:1\n",
	} {
		exit := 1
		if stderr == "" {
			exit = 0
		}
		fr := &fakeRunner{respond: func(c Command) (Result, error) { return Result{Exit: exit, Stderr: stderr}, nil }}
		e := newExec(t, fr)
		if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Do(context.Background(), mustDecode(t, strings.Replace(del, `"namespace":"gw",`, "", 1))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, stderr := range map[string]string{
		"in use":      "Error: HTB class in use.\nCommand failed -:2\n",
		"busy":        "RTNETLINK answers: Device or resource busy\n",
		"a bad batch": "Error: Specified class not found.\nCommand failed -:1\nwhat is this\n",
		"a kernel":    "Error: Specified filter handle not found.\nCommand failed -:1\nError: Operation not permitted\n",
	} {
		fr := &fakeRunner{respond: func(c Command) (Result, error) { return Result{Exit: 1, Stderr: stderr}, nil }}
		e := newExec(t, fr)
		if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
			t.Fatal(err)
		}
		_, err := e.Do(context.Background(), mustDecode(t, strings.Replace(del, `"namespace":"gw",`, "", 1)))
		var ce *CommandError
		if !errors.As(err, &ce) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// the other entries are not idempotent: a class that cannot be changed is a failure
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		return Result{Exit: 2, Stderr: "Error: Specified class not found.\n"}, nil
	}}
	e := newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(context.Background(), mustDecode(t, `{"type":"tc","entries":[{"object":"class","action":"change","dev":"wan0","parent":"1:","classid":"1:24","args":["htb","rate","1mbit"]}]}`))
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Errorf("a failed change: %v", err)
	}
}

func TestTheScopeOfTheDeletionsIsTheInterfacesToo(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	if _, err := e.Do(context.Background(), mustDecode(t, assignWan)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Do(context.Background(), mustDecode(t, `{"type":"tc","entries":[{"object":"class","action":"delete","dev":"eth7","classid":"1:24"}]}`))
	if !errors.Is(err, ErrOutOfScope) {
		t.Errorf("a deletion on an unassigned interface: %v", err)
	}
}

// --- the tc read ---

func tcFixtureText(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "linux", "testdata", "tc", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheTCReadReturnsTheNormalizedTreeWithCounters(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		if c.Tool != ToolTC || len(c.Args) < 6 || c.Args[0] != "-s" || c.Args[1] != "-j" || c.Args[3] != "show" || c.Args[4] != "dev" || c.Args[5] != "dum0" {
			t.Errorf("unexpected command %s", c)
		}
		switch c.Args[2] {
		case "qdisc":
			return Result{Stdout: tcFixtureText(t, "tree_qdisc_stats.json")}, nil
		case "class":
			return Result{Stdout: tcFixtureText(t, "tree_class_stats.json")}, nil
		case "filter":
			return Result{Stdout: tcFixtureText(t, "tree_filter_stats.json")}, nil
		}
		return Result{Exit: 1}, nil
	}}
	e := newExec(t, fr)
	out, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"tc","dev":"dum0","namespace":"gw"}`))
	if err != nil {
		t.Fatal(err)
	}
	var tree linux.NormTree
	if err := jsonUnmarshal(out.Data[0], &tree); err != nil {
		t.Fatal(err)
	}
	if tree.Dev != "dum0" || len(tree.Qdiscs) != 6 || len(tree.Classes) != 6 || len(tree.Filters) != 5 {
		t.Fatalf("%+v", tree)
	}
	if tree.Qdiscs[1].Stats == nil || tree.Qdiscs[1].Netem == nil {
		t.Errorf("counters or netem lost: %+v", tree.Qdiscs[1])
	}
	cmds := fr.commands()
	if len(cmds) != 3 {
		t.Fatalf("%d tool runs, want 3", len(cmds))
	}
	for _, c := range cmds {
		if c.NS != "gw" {
			t.Errorf("namespace %q", c.NS)
		}
	}
}

func TestTheTCReadFailsWithTheToolsAnswer(t *testing.T) {
	fr := &fakeRunner{respond: func(c Command) (Result, error) {
		return Result{Exit: 1, Stderr: `Cannot find device "dum9"`}, nil
	}}
	e := newExec(t, fr)
	_, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"tc","dev":"dum9"}`))
	var ce *CommandError
	if !errors.As(err, &ce) || !strings.Contains(ce.Stderr, "Cannot find device") {
		t.Errorf("%v", err)
	}
	fr = &fakeRunner{respond: func(c Command) (Result, error) { return Result{Stdout: "not json"}, nil }}
	e = newExec(t, fr)
	if _, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"tc","dev":"dum0"}`)); err == nil {
		t.Error("a listing that is no JSON was accepted")
	}
}

func TestTheTCReadNeedsAnInterface(t *testing.T) {
	for _, in := range []string{`{"type":"read","what":"tc"}`, `{"type":"read","what":"tc","dev":"a b"}`, `{"type":"read","what":"tc","dev":"wan0","table":"main"}`} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
}
