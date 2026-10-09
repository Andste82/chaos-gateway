package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// TestAnIdentityUpdateWithNoDevicesIsNothingToDo: the identity map is only compiled once a device
// is known, so before the first one an identity-only change (the observer saw something that is
// not a device) has no map to update. It must be an incremental no-op, not a full apply: a full
// apply rewrites the whole ruleset and moves the generation marker (TestABurstOfNeighborChangesIs-
// OneIdentityUpdate waits on that marker for the first real device).
func TestAnIdentityUpdateWithNoDevicesIsNothingToDo(t *testing.T) {
	ops, err := identityOps("ns", &compiler.Target{}, &compiler.Target{})
	if err != nil || len(ops) != 0 {
		t.Fatalf("ops %v, err %v", ops, err)
	}
	// the first device needs a map that does not exist yet: that is a full apply
	if _, err := identityOps("ns", &compiler.Target{}, &compiler.Target{IdentityMap: "ident4_x", DeviceNums: map[string]int{"d": 1}}); !errors.Is(err, errNotIncremental) {
		t.Fatalf("err %v, want errNotIncremental", err)
	}
}

// An address change moves the elements of the classification maps along with the identity map's:
// the classification elements go in one atomic transaction (an interval map cannot take an element
// that overlaps one that is still there), the identity map's as before. A change of the faults
// themselves (a new id, a class) is no element update and takes the full apply.
func TestAnIdentityUpdateMovesTheClassificationElementsToo(t *testing.T) {
	cls := func(els ...compiler.MapElement) compiler.MapDef {
		return compiler.MapDef{Name: "cls_dev_x", KeyType: []string{"ipv4_addr"}, ValueType: "verdict", Flags: []string{"interval"}, Elements: els}
	}
	ident := func(els ...compiler.MapElement) compiler.MapDef {
		return compiler.MapDef{Name: "ident4_x", KeyType: []string{"ipv4_addr"}, ValueType: "mark", Elements: els}
	}
	target := func(a string) *compiler.Target {
		tg := &compiler.Target{IdentityMap: "ident4_x", DeviceNums: map[string]int{"d": 1}, FaultIDs: map[string]int{"k": 1}}
		tg.Nft.Maps = []compiler.MapDef{
			cls(compiler.MapElement{Key: a, Value: "mark_1"}),
			ident(compiler.MapElement{Key: a, Value: "1"}),
		}
		return tg
	}
	ops, err := identityOps("ns", target("10.10.0.5"), target("10.10.0.6"))
	if err != nil {
		t.Fatal(err)
	}
	var del, add, tx int
	for _, op := range ops {
		switch o := op.(type) {
		case *executor.NftDelMapElements:
			del++
			if o.Map != "ident4_x" {
				t.Errorf("the identity map's own delete names %s", o.Map)
			}
		case *executor.NftAddMapElements:
			add++
		case *executor.NftApply:
			tx++
			if s := string(o.Ruleset); !strings.Contains(s, "cls_dev_x") || strings.Index(s, `"delete"`) > strings.Index(s, `"add"`) {
				t.Errorf("the classification transaction: %s", s)
			}
		}
	}
	if del != 1 || add != 1 || tx != 1 {
		t.Errorf("%d deletes, %d adds, %d transactions: %v", del, add, tx, ops)
	}
	if ops, err := identityOps("ns", target("10.10.0.5"), target("10.10.0.5")); err != nil || len(ops) != 0 {
		t.Errorf("an unchanged target is %v, %v", ops, err)
	}

	// a fault that appeared is not an element update
	changed := target("10.10.0.6")
	changed.FaultIDs = map[string]int{"k": 1, "other": 2}
	if _, err := identityOps("ns", target("10.10.0.5"), changed); !errors.Is(err, errNotIncremental) {
		t.Errorf("a new fault id: %v", err)
	}
	// a map that appeared is not either
	changed = target("10.10.0.6")
	changed.Nft.Maps = append(changed.Nft.Maps, compiler.MapDef{Name: "cls_other", KeyType: []string{"ipv4_addr"}, ValueType: "verdict"})
	if _, err := identityOps("ns", target("10.10.0.5"), changed); !errors.Is(err, errNotIncremental) {
		t.Errorf("a new map: %v", err)
	}
	// nor a new class
	changed = target("10.10.0.6")
	changed.TC = &compiler.TCTarget{Devs: []string{"wan0"}, Classes: []compiler.TCClass{{ID: 1}}}
	if _, err := identityOps("ns", target("10.10.0.5"), changed); !errors.Is(err, errNotIncremental) {
		t.Errorf("a new tc tree: %v", err)
	}
	// classification elements that change while no device is known are not "nothing to do"
	a, b := &compiler.Target{}, &compiler.Target{}
	b.Nft.Maps = []compiler.MapDef{cls(compiler.MapElement{Key: "10.10.0.0/24", Value: "mark_1"})}
	a.Nft.Maps = []compiler.MapDef{cls()}
	if _, err := identityOps("ns", a, b); !errors.Is(err, errNotIncremental) {
		t.Errorf("elements without an identity map: %v", err)
	}
}

// The MTU maps follow an address change like the impairment maps do (an element transaction of their own), and an MTU
// fault that appeared or went, or a table whose size changed, is no element update (routes and rules change with it).
func TestAnIdentityUpdateMovesTheMTUMapElementsAndAnMTUFaultThatChangedTakesTheFullApply(t *testing.T) {
	target := func(a string, size int) *compiler.Target {
		tg := &compiler.Target{IdentityMap: "ident4_x", DeviceNums: map[string]int{"d": 1}}
		tg.PMTU = []compiler.PMTUFault{{Key: "overlay:x:mtu", Size: size, Mode: "icmp", Index: 1, Table: 103, Chain: "pmtu_x"}}
		tg.Nft.Maps = []compiler.MapDef{
			{Name: "pmtu_dev_x", KeyType: []string{"ipv4_addr"}, ValueType: "verdict", Flags: []string{"interval"}, Elements: []compiler.MapElement{{Key: a, Value: "pmtu_x"}}},
			{Name: "ident4_x", KeyType: []string{"ipv4_addr"}, ValueType: "mark", Elements: []compiler.MapElement{{Key: a, Value: "1"}}},
		}
		return tg
	}
	ops, err := identityOps("ns", target("10.10.0.5", 1280), target("10.10.0.6", 1280))
	if err != nil {
		t.Fatal(err)
	}
	var tx int
	for _, op := range ops {
		if o, ok := op.(*executor.NftApply); ok {
			tx++
			if s := string(o.Ruleset); !strings.Contains(s, "pmtu_dev_x") {
				t.Errorf("the classification transaction: %s", s)
			}
		}
	}
	if tx != 1 {
		t.Errorf("%d transactions: %v", tx, ops)
	}
	if _, err := identityOps("ns", target("10.10.0.5", 1280), target("10.10.0.5", 1400)); !errors.Is(err, errNotIncremental) {
		t.Errorf("another size is an element update: %v", err)
	}
}
