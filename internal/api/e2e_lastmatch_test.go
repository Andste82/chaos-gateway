//go:build testbed

package api_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// P3-M13-01 on a real kernel: a rule's `last_match` is the time the kernel's `last` statement noted for
// the rule's match, not the time of a read: read seconds after the traffic it still names the traffic.
// An apply that makes the access chains anew (a second overlay) carries the time over, late by at most
// the length of the apply.
func TestARulesLastMatchIsTheKernelsAndAnApplyKeepsIt(t *testing.T) {
	// the setup's commit-confirm window outlasts an emulated kernel's slow first apply
	g, top := newBedGW(t, func(o *options) { o.confirm = 5 * time.Minute })
	ov := g.mustCreateOverlay(`{"target":{"network":"IoT"},"rule":{"protocol":"icmp","destination":{"cidr":"` + testbed.ServerAddr + `/32"},"action":"drop"}}`)
	path := "/overlays/" + ov["id"].(string)
	counters := func() map[string]any {
		t.Helper()
		got := g.do("GET", path, nil, nil, nil).json(t)
		c, ok := got["counters"].(map[string]any)
		if !ok {
			t.Fatalf("the overlay has no counters: %v", got)
		}
		return c
	}
	at := func(c map[string]any) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339Nano, fmt.Sprint(c["last_match"]))
		if err != nil {
			t.Fatalf("last_match of %v: %v", c, err)
		}
		return v
	}
	if c := counters(); c["last_match"] != nil {
		t.Fatalf("a rule that matched nothing has a last match: %v", c)
	}

	before := time.Now()
	if pingOK(top.A, "", testbed.ServerAddr) {
		t.Fatal("A reaches the server although the rule drops ICMP to it")
	}
	after := time.Now()
	time.Sleep(3 * time.Second)
	read := time.Now()
	c := counters()
	matched := at(c)
	// the clocks of the kernel (jiffies) and of the process agree to a few milliseconds
	slack := 500 * time.Millisecond
	if c["packets"].(float64) < 1 || matched.Before(before.Add(-slack)) || matched.After(after.Add(slack)) {
		t.Errorf("the rule matched between %s and %s; read at %s: %v", before.Format(time.StampMilli), after.Format(time.StampMilli), read.Format(time.StampMilli), c)
	}

	// another overlay: an apply that flushes the access chains and fills them anew
	applyStart := time.Now()
	g.mustCreateOverlay(`{"target":{"network":"IoT"},"rule":{"protocol":"udp","ports":[5353],"action":"drop"}}`)
	took := time.Since(applyStart)
	kept := counters()
	if kept["last_match"] == nil {
		t.Fatalf("after an apply the rule has no last match: %v", kept)
	}
	if got := at(kept); got.Before(matched.Add(-slack)) || got.After(matched.Add(took+slack)) {
		t.Errorf("after an apply of %s the last match is %s, the rule matched at %s", took, got.Format(time.StampMilli), matched.Format(time.StampMilli))
	}
}
