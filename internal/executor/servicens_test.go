package executor

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func serviceOps(t *testing.T, tail string) []Operation {
	t.Helper()
	return ops(t, `{"type":"assign_interfaces","devs":["svc0"]}`,
		`{"type":"service_ns","action":"ensure","name":"cgsvc","host_if":"svc0","peer_if":"svc1","host_cidr":"169.254.100.1/30","peer_cidr":"169.254.100.2/30"`+tail+`}`)
}

func TestAServiceNamespaceTouchesOnlyAssignedInterfaces(t *testing.T) {
	e := newExec(t, &fakeRunner{})
	_, err := e.DoBatch(context.Background(), ops(t, `{"type":"service_ns","action":"ensure","name":"cgsvc","host_if":"svc0","peer_if":"svc1","host_cidr":"169.254.100.1/30","peer_cidr":"169.254.100.2/30"}`))
	if err == nil || !strings.Contains(err.Error(), "not assigned") {
		t.Fatalf("%v", err)
	}
}

func TestTheServiceNamespaceReadComparesTheHolder(t *testing.T) {
	inodes := map[string]uint64{"/run/netns/cgsvc": 7, "/proc/42/ns/net": 7, "/proc/43/ns/net": 8}
	e := newExec(t, &fakeRunner{}, WithNetnsInode(func(p string) (uint64, bool) { i, ok := inodes[p]; return i, ok }))
	read := func(name string, pid int) ServiceNSState {
		t.Helper()
		op := &Read{What: ReadServiceNS, Service: name, PID: pid}
		raw, err := e.read(context.Background(), op)
		if err != nil {
			t.Fatal(err)
		}
		var st ServiceNSState
		if err := jsonUnmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := read("cgsvc", 0); !st.Exists || !st.HolderMatches {
		t.Errorf("any holder: %+v", st)
	}
	if st := read("cgsvc", 42); !st.Exists || !st.HolderMatches {
		t.Errorf("the holder's: %+v", st)
	}
	if st := read("cgsvc", 43); !st.Exists || st.HolderMatches {
		t.Errorf("another holder: %+v", st)
	}
	if st := read("cgsvc", 99); !st.Exists || st.HolderMatches {
		t.Errorf("a holder that is gone: %+v", st)
	}
	if st := read("other", 42); st.Exists {
		t.Errorf("a missing namespace: %+v", st)
	}
	// the read needs a valid name and no stray fields
	for _, in := range []string{`{"type":"read","what":"service_ns"}`, `{"type":"read","what":"service_ns","service":"a b"}`, `{"type":"read","what":"service_ns","service":"x","pid":-1}`, `{"type":"read","what":"links","service":"x"}`} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
}

func TestAnOldNamespaceIsReplacedWhenItsHolderIsGone(t *testing.T) {
	inodes := map[string]uint64{"/run/netns/cgsvc": 7, "/proc/43/ns/net": 8}
	// the namespace and the pair exist until the name is deleted
	var mu sync.Mutex
	gone := false
	r := &fakeRunner{respond: func(c Command) (Result, error) {
		mu.Lock()
		defer mu.Unlock()
		line := c.String()
		switch {
		case strings.Contains(line, "netns delete"):
			gone = true
		case gone && strings.Contains(line, "link show dev"):
			return Result{Exit: 1, Stderr: "does not exist"}, nil
		}
		return Result{}, nil
	}}
	e := newExec(t, r, WithNetnsInode(func(p string) (uint64, bool) { i, ok := inodes[p]; return i, ok }))
	if _, err := e.DoBatch(context.Background(), serviceOps(t, `,"holder_pid":43`)); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range r.commands() {
		lines = append(lines, c.String())
	}
	joined := strings.Join(lines, "\n")
	del := strings.Index(joined, "ip link delete dev svc0 type veth")
	rm := strings.Index(joined, "ip netns delete cgsvc")
	att := strings.Index(joined, "ip netns attach cgsvc 43")
	if del < 0 || rm < 0 || att < 0 || del >= rm || rm >= att {
		t.Errorf("the old pair and name must go before the new namespace is attached:\n%s", joined)
	}
	// the namespace of the holder that is there: nothing is deleted
	inodes["/run/netns/cgsvc"] = 8
	r2 := &fakeRunner{}
	e2 := newExec(t, r2, WithNetnsInode(func(p string) (uint64, bool) { i, ok := inodes[p]; return i, ok }))
	if _, err := e2.DoBatch(context.Background(), serviceOps(t, `,"holder_pid":43`)); err != nil {
		t.Fatal(err)
	}
	for _, c := range r2.commands() {
		if strings.Contains(c.String(), "netns delete") || strings.Contains(c.String(), "link delete") {
			t.Errorf("the right namespace is deleted: %s", c)
		}
	}
}

func TestAHolderMustBeAnotherNamespaceThanTheExecutors(t *testing.T) {
	inodes := map[string]uint64{"/proc/self/ns/net": 5, "/proc/42/ns/net": 5, "/proc/43/ns/net": 9}
	r := &fakeRunner{}
	e := newExec(t, r, WithNetnsInode(func(p string) (uint64, bool) { i, ok := inodes[p]; return i, ok }))
	if _, err := e.DoBatch(context.Background(), serviceOps(t, `,"holder_pid":42`)); err == nil || !strings.Contains(err.Error(), "own network namespace") {
		t.Errorf("a process in the executor's namespace is no holder: %v", err)
	}
	if _, err := e.DoBatch(context.Background(), serviceOps(t, `,"holder_pid":99`)); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a process that is gone is no holder: %v", err)
	}
	for _, c := range r.commands() {
		if strings.Contains(c.String(), "netns") {
			t.Errorf("a namespace was touched for a bad holder: %s", c)
		}
	}
	if _, err := e.DoBatch(context.Background(), serviceOps(t, `,"holder_pid":43`)); err != nil {
		t.Errorf("a good holder: %v", err)
	}
}
