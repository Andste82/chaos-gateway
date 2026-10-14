package engine

import (
	"net/netip"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
)

// P3-M14-03: a classification element belongs to a device when its source (an address, a prefix or a
// range, the first part of a concatenated key) holds one of the device's addresses.
func TestAMapKeyCoversAnAddressAsAnAddressPrefixOrRange(t *testing.T) {
	a := netip.MustParseAddr("10.10.0.31")
	for key, want := range map[string]bool{
		"10.10.0.31": true, "10.10.0.32": false, "10.10.0.0/24": true, "10.10.1.0/24": false,
		"10.10.0.1-10.10.0.40": true, "10.10.0.32-10.10.0.40": false, "203.0.113.0/24": false, "x": false,
	} {
		if got := covers(key, a); got != want {
			t.Errorf("covers(%q) = %v, want %v", key, got, want)
		}
	}
}

// The selection of an object is built from the names the applied target gave it: a rule's counter and
// source set, a fault's mark chains, counters, classes (a tunnel fault's ingress filter too) and the
// elements that go to its chains, a device's identity and classification elements by its numeral and
// addresses and the queues of its own.
func TestTheLinuxViewSelectsAnObjectByTheNamesTheCompilerGaveIt(t *testing.T) {
	up := &compiler.Netem{}
	snap := &Snapshot{
		Access: &compiler.AccessPlan{Rules: []compiler.AccessRule{{Key: "config:r1", Counter: "rule_1", SourceSet: "asrc_1"}, {Key: "overlay:o1", Counter: "rule_2", SourceSet: "asrc_2"}}},
		Faults: []compiler.Fault{
			{ID: 3, Source: "F1", CounterUp: "f3_up", CounterDown: "f3_down", Upload: up},
			{ID: 4, Source: "f1", Device: "dev-a", CounterUp: "f4_up", CounterDown: "f4_down", Upload: up, Download: up},
			{ID: 5, Source: "t1", CounterUp: "f5_up", CounterDown: "f5_down", Upload: up, Tunnel: &compiler.TunnelInfo{}},
			{ID: 6, Source: "other", CounterUp: "f6_up"},
		},
		PMTU:       []compiler.PMTUFault{{Source: "f1", Chain: "pmtu_ab", CounterUp: "pmtu_ab_up", CounterDown: "pmtu_ab_down", CounterDrop: "pmtu_ab_drop"}},
		DeviceNums: map[string]int{"dev-a": 7},
		Identity:   domain.Identity{Addresses: map[string][]netip.Addr{"dev-a": {netip.MustParseAddr("10.10.0.31")}}},
	}

	rule, err := objectSel(snap, LinuxObjectRule, "O1")
	if err != nil || !rule.Counters["rule_2"] || !rule.Sets["asrc_2"] || rule.Counters["rule_1"] || len(rule.Chains) != 0 {
		t.Errorf("rule: %+v %v", rule, err)
	}
	if anti, _ := objectSel(snap, LinuxObjectRule, compiler.AntiLockoutRule.Key); !anti.Counters[compiler.AntiLockoutCounter] {
		t.Errorf("the anti-lockout rule: %+v", anti)
	}

	fault, _ := objectSel(snap, LinuxObjectFault, "f1")
	for _, c := range []string{"mark_3", "mark_4", "pmtu_ab"} {
		if !fault.Chains[c] || !fault.Element("cls_x", "10.10.0.31", c) {
			t.Errorf("fault: chain %s or its elements not selected: %+v", c, fault)
		}
	}
	for _, c := range []string{"f3_up", "f3_down", "f4_up", "f4_down", "pmtu_ab_up", "pmtu_ab_down", "pmtu_ab_drop"} {
		if !fault.Counters[c] {
			t.Errorf("fault: counter %s not selected", c)
		}
	}
	if !fault.Classes[compiler.ClassIDOf(3, compiler.Upload)] || fault.Classes[compiler.ClassIDOf(3, compiler.Download)] || !fault.Classes[compiler.ClassIDOf(4, compiler.Download)] {
		t.Errorf("fault: classes %v", fault.Classes)
	}
	if fault.Chains["mark_6"] || fault.Element("cls_x", "10.10.0.31", "mark_6") || len(fault.Flowers) != 0 {
		t.Errorf("fault: another fault is selected: %+v", fault)
	}
	if tunnel, _ := objectSel(snap, LinuxObjectFault, "t1"); !tunnel.Flowers[5] {
		t.Errorf("a tunnel fault's ingress filter: %+v", tunnel)
	}

	dev, _ := objectSel(snap, LinuxObjectDevice, "DEV-A")
	for _, c := range []struct {
		m, k, v string
		want    bool
	}{
		{compiler.IdentityMapBase + "_1a2b3c", "10.10.0.31", "7", true},
		{compiler.IdentityMapBase + "_1a2b3c", "10.10.0.32", "8", false},
		{compiler.ClassifyMapBase + "devdest_1a", "10.10.0.0/24 . 203.0.113.0/24", "mark_3", true},
		{compiler.ClassifyMapBase + "dev_1a", "10.10.1.0/24", "mark_3", false},
		{compiler.PMTUMapBase + "dev_1a", "10.10.0.31", "pmtu_ab", true},
		{"tun_out_1a", "10.10.0.31", "mark_5", false},
	} {
		if got := dev.Element(c.m, c.k, c.v); got != c.want {
			t.Errorf("device: element %s %s : %s selected %v, want %v", c.m, c.k, c.v, got, c.want)
		}
	}
	if !dev.Chains["mark_4"] || dev.Chains["mark_3"] || !dev.Classes[compiler.ClassIDOf(4, compiler.Upload)] {
		t.Errorf("device: the fault with its own queue for it: %+v", dev)
	}
	if none, _ := objectSel(snap, LinuxObjectDevice, "ghost"); none.Element != nil || len(none.Chains) != 0 {
		t.Errorf("an unknown device: %+v", none)
	}
	if _, err := objectSel(snap, "network", "x"); err == nil {
		t.Error("an unknown kind is refused")
	}
}
