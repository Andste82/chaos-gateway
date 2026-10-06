package engine

import (
	"errors"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
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
