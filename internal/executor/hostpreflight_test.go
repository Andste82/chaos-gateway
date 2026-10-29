package executor

import (
	"context"
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/preflight"
)

// toolRunner is a runner that knows where its tools are, as ExecRunner does.
type toolRunner struct {
	fakeRunner
	missing map[Tool]bool
}

func (r *toolRunner) Path(t Tool) (string, bool) { return "/usr/sbin/" + string(t), !r.missing[t] }

func preflightHost() fstest.MapFS {
	return fstest.MapFS{
		"proc/sys/net/ipv4/ip_forward":       {Data: []byte("1\n")},
		"proc/sys/net/ipv4/ping_group_range": {Data: []byte("0 2147483647\n")},
		"lib/modules/6.8.0-1/modules.dep":    {},
		"var/lib/chaosgw/captures":           {Mode: 1<<31 | 0o755, Sys: &syscall.Stat_t{Uid: 0}},
	}
}

func readPreflight(t *testing.T, e *Executor) HostPreflightState {
	t.Helper()
	res, err := e.Do(context.Background(), mustDecode(t, `{"type":"read","what":"host_preflight"}`))
	if err != nil {
		t.Fatal(err)
	}
	var st HostPreflightState
	if err := json.Unmarshal(res.Data[0], &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// M28: the executor, privileged and with the tools, makes the checks of the host (plan §3.4) and the API asks for them. The
// commands are the ones the checks name, nothing is run through a shell, and a second read within ten seconds is the first one.
func TestTheExecutorChecksTheHostAndKeepsTheAnswerForTenSeconds(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	r := &toolRunner{missing: map[Tool]bool{ToolConntrack: true}}
	r.respond = func(c Command) (Result, error) {
		if c.Tool == ToolNft && len(c.Args) > 0 && c.Args[0] == "list" {
			return Result{Stdout: "table inet chaosgw\ntable inet firewalld\n"}, nil
		}
		return Result{Stdout: string(c.Tool) + " 1.0\n"}, nil
	}
	e := newExec(t, r, WithClock(fake), WithOwnRoot(preflightHost(), "6.8.0-1"),
		WithPreflight([]preflight.DataDir{{Path: "/var/lib/chaosgw/captures", What: "the packet captures", UID: 65532}}))

	st := readPreflight(t, e)
	if !st.At.Equal(fake.Now()) {
		t.Errorf("at = %v", st.At)
	}
	byID := map[string]preflight.HostCheck{}
	for _, c := range st.Checks {
		byID[c.ID] = c
	}
	if c := byID["host.tools"]; c.Status != "fail" || !strings.Contains(c.Message, "conntrack") {
		t.Errorf("a tool the runner cannot find: %+v", c)
	}
	if c := byID["host.firewall"]; c.Status != "warn" || !strings.Contains(c.Message, "inet firewalld") {
		t.Errorf("another firewall: %+v", c)
	}
	if c := byID["host.data.captures"]; c.Status != "fail" || !strings.Contains(c.Message, "belongs to user 0") {
		t.Errorf("the capture directory of root: %+v", c)
	}
	if c := byID["host.ip_forward"]; c.Status != "ok" {
		t.Errorf("forwarding: %+v", c)
	}
	var sawNetns bool
	for _, c := range r.commands() {
		if c.Tool == ToolIP && len(c.Args) >= 2 && c.Args[0] == "netns" && c.Args[1] == "add" {
			sawNetns = true
		}
		if c.NS != "" || c.Stdin != "" {
			t.Errorf("the preflight runs no command in a namespace or with input: %v", c)
		}
	}
	if !sawNetns {
		t.Errorf("no throw-away namespace was made: %v", r.commands())
	}

	before := len(r.commands())
	fake.Advance(9 * time.Second)
	if again := readPreflight(t, e); !again.At.Equal(st.At) || len(r.commands()) != before {
		t.Errorf("within ten seconds the last answer stands: at %v, %d new commands", again.At, len(r.commands())-before)
	}
	fake.Advance(2 * time.Second)
	if fresh := readPreflight(t, e); fresh.At.Equal(st.At) || len(r.commands()) == before {
		t.Errorf("after ten seconds the checks run again: at %v", fresh.At)
	}
}

// An executor on a simulated kernel has no tools to check: its answer holds no checks, and the API's report is its own.
func TestAnExecutorOnASimulatedKernelHasNoChecksToReport(t *testing.T) {
	fr := &fakeRunner{}
	e := newExec(t, fr)
	st := readPreflight(t, e)
	if st.Checks == nil || len(st.Checks) != 0 || len(fr.commands()) != 0 {
		t.Errorf("checks %v, commands %v", st.Checks, fr.commands())
	}
	if removed := e.RemoveCheckNamespaces(context.Background()); len(removed) != 0 || len(fr.commands()) != 0 {
		t.Errorf("removed %v, commands %v", removed, fr.commands())
	}
}

// The throw-away namespace of the check can outlive an executor that is killed between its creation and its removal, in the host's
// /run/netns: the stop cleanup (and `chaosgw teardown`) removes it, and nothing but a name of that form.
func TestAThrowAwayNamespaceOfAKilledCheckIsRemovedByTheCleanup(t *testing.T) {
	r := &toolRunner{}
	r.respond = func(c Command) (Result, error) {
		if c.Tool == ToolIP && len(c.Args) == 2 && c.Args[0] == "netns" && c.Args[1] == "list" {
			return Result{Stdout: "cgsvc (id: 0)\npfchk-0123abcd (id: 3)\ncgp-0123abcd\npfchk-nothex00\npfchk-89abcdef\n"}, nil
		}
		return Result{}, nil
	}
	e := newExec(t, r)
	removed := e.RemoveCheckNamespaces(context.Background())
	if strings.Join(removed, " ") != "pfchk-0123abcd pfchk-89abcdef" {
		t.Errorf("removed %v", removed)
	}
	var deleted []string
	for _, c := range r.commands() {
		if len(c.Args) == 3 && c.Args[1] == "del" {
			deleted = append(deleted, c.Args[2])
		}
	}
	if strings.Join(deleted, " ") != "pfchk-0123abcd pfchk-89abcdef" {
		t.Errorf("deleted %v: %v", deleted, r.commands())
	}
}
