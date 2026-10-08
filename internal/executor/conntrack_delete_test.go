package executor

import (
	"context"
	"strings"
	"testing"
)

func ptrInt(n int) *int { return &n }

// The operation names connections by their original tuple and nothing else: a flow cannot carry a
// filter, an option or a malformed address, and one request cannot delete without bound.
func TestConntrackDeleteRefusesWhatIsNotATuple(t *testing.T) {
	good := ConntrackFlow{Proto: "tcp", Src: "10.10.0.31", Dst: "203.0.113.10", SPort: 45566, DPort: 8883}
	for name, mod := range map[string]func(*ConntrackFlow){
		"an unknown protocol":     func(f *ConntrackFlow) { f.Proto = "gre" },
		"an option as the source": func(f *ConntrackFlow) { f.Src = "--orig-src" },
		"a network as the source": func(f *ConntrackFlow) { f.Src = "10.10.0.0/24" },
		"an IPv6 address":         func(f *ConntrackFlow) { f.Dst = "2001:db8::1" },
		"a missing port":          func(f *ConntrackFlow) { f.DPort = 0 },
		"a port out of range":     func(f *ConntrackFlow) { f.SPort = 70000 },
		"icmp fields on tcp":      func(f *ConntrackFlow) { f.ICMPType = ptrInt(8) },
		"ports on icmp": func(f *ConntrackFlow) {
			*f = ConntrackFlow{Proto: "icmp", Src: f.Src, Dst: f.Dst, SPort: 1, ICMPType: ptrInt(8), ICMPCode: ptrInt(0), ICMPID: ptrInt(1)}
		},
		"icmp without an id": func(f *ConntrackFlow) {
			*f = ConntrackFlow{Proto: "icmp", Src: f.Src, Dst: f.Dst, ICMPType: ptrInt(8), ICMPCode: ptrInt(0)}
		},
	} {
		f := good
		mod(&f)
		if err := (ConntrackDelete{Flows: []ConntrackFlow{f}}).validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := (ConntrackDelete{}).validate(); err == nil {
		t.Error("an operation without flows was accepted")
	}
	if err := (ConntrackDelete{Flows: make([]ConntrackFlow, MaxConntrackFlows+1)}).validate(); err == nil {
		t.Error("an operation over the limit was accepted")
	}
	if err := (ConntrackDelete{Target: Target{NS: "gw;id"}, Flows: []ConntrackFlow{good}}).validate(); err == nil {
		t.Error("an invalid namespace was accepted")
	}
	if err := (ConntrackDelete{Flows: []ConntrackFlow{good}}).validate(); err != nil {
		t.Errorf("a plain flow is refused: %v", err)
	}
}

// One command per flow, by the original tuple, in the namespace of the operation; a flow that is gone
// (the tool exits 1 and says so) is not an error, anything else is.
func TestConntrackDeleteRunsOneCommandPerFlowAndToleratesAGoneFlow(t *testing.T) {
	fr := &fakeRunner{}
	e := newExec(t, fr)
	op := &ConntrackDelete{Target: Target{NS: "gw"}, Flows: []ConntrackFlow{
		{Proto: "tcp", Src: "10.10.0.31", Dst: "203.0.113.10", SPort: 45566, DPort: 8883},
		{Proto: "udp", Src: "10.10.0.31", Dst: "10.10.0.1", SPort: 5353, DPort: 53},
		{Proto: "icmp", Src: "10.10.0.31", Dst: "203.0.113.10", ICMPType: ptrInt(8), ICMPCode: ptrInt(0), ICMPID: ptrInt(4711)},
	}}
	if _, err := e.Do(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range fr.commands() {
		if c.Tool != ToolConntrack || c.NS != "gw" {
			t.Fatalf("%+v", c)
		}
		got = append(got, strings.Join(c.Args, " "))
	}
	want := []string{
		"-D -f ipv4 -p tcp --orig-src 10.10.0.31 --orig-dst 203.0.113.10 --orig-port-src 45566 --orig-port-dst 8883",
		"-D -f ipv4 -p udp --orig-src 10.10.0.31 --orig-dst 10.10.0.1 --orig-port-src 5353 --orig-port-dst 53",
		"-D -f ipv4 -p icmp --orig-src 10.10.0.31 --orig-dst 203.0.113.10 --icmp-type 8 --icmp-code 0 --icmp-id 4711",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	gone := &fakeRunner{respond: func(Command) (Result, error) {
		return Result{Exit: 1, Stderr: "conntrack v1.4.8 (conntrack-tools): 0 flow entries have been deleted.\n"}, nil
	}}
	if _, err := newExec(t, gone).Do(context.Background(), op); err != nil {
		t.Errorf("a flow that is gone is an error: %v", err)
	}
	broken := &fakeRunner{respond: func(Command) (Result, error) {
		return Result{Exit: 1, Stderr: "conntrack v1.4.8 (conntrack-tools): Operation failed: Operation not permitted\n"}, nil
	}}
	if _, err := newExec(t, broken).Do(context.Background(), op); err == nil {
		t.Error("a refused deletion is not an error")
	}
}
