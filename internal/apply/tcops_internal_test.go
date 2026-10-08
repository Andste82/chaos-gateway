package apply

import (
	"fmt"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/executor"
)

// More entries than one operation takes are split into operations that keep the order.
func TestTCEntriesAreSplitIntoOperationsInOrder(t *testing.T) {
	n := 2*executor.MaxTCEntries + 7
	var es []executor.TCEntry
	for i := 0; i < n; i++ {
		es = append(es, executor.TCEntry{Object: "class", Action: "replace", Dev: "d", ClassID: fmt.Sprintf("1:%x", i+2)})
	}
	ops := tcOps(executor.Target{NS: "ns"}, es)
	if len(ops) != 3 {
		t.Fatalf("%d operations, want 3", len(ops))
	}
	i := 0
	for _, op := range ops {
		tc := op.(*executor.TC)
		if len(tc.Entries) == 0 || len(tc.Entries) > executor.MaxTCEntries || tc.NS != "ns" {
			t.Fatalf("an operation with %d entries in %q", len(tc.Entries), tc.NS)
		}
		for _, e := range tc.Entries {
			if want := fmt.Sprintf("1:%x", i+2); e.ClassID != want {
				t.Fatalf("entry %d is %s, want %s", i, e.ClassID, want)
			}
			i++
		}
	}
	if i != n {
		t.Errorf("%d entries come out of %d", i, n)
	}
	if got := tcOps(executor.Target{}, nil); len(got) != 0 {
		t.Errorf("no entries make %d operations", len(got))
	}
}
