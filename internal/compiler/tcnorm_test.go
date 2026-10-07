package compiler

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
)

// The recorded kernel output of internal/linux/testdata/tc was made with these netem
// configurations (testdata/tc/record.sh, class minors 0x24 to 0x28). What the compiler predicts the
// kernel reports for them must be what it reported.
func TestTheNormOfANetemIsWhatTheKernelReportedForIt(t *testing.T) {
	recorded := map[string]Netem{
		"24:": {Limit: 5000, Delay: 50 * time.Millisecond, Jitter: 10 * time.Millisecond, Distribution: "normal", Loss: 1, LossCorr: 25},
		"25:": {Limit: 1000, Delay: 20 * time.Millisecond, Jitter: 5 * time.Millisecond, Gemodel: &Gemodel{P: 1, R: 10, LossBad: 70, LossGood: 0.1},
			Reorder: 25, Corrupt: 0.1, Rate: 2_000_000},
		"26:": {Limit: 1000, Delay: 600 * time.Millisecond},
		"27:": {Limit: 1000, Loss: 100},
		"28:": {Limit: 1000},
	}
	b, err := os.ReadFile(filepath.Join("..", "linux", "testdata", "tc", "tree_qdisc.json"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := linux.NormalizeTC("dum0", b, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, q := range tree.Qdiscs {
		want, ok := recorded[q.Handle]
		if !ok {
			continue
		}
		seen++
		got := want.Norm()
		if !reflect.DeepEqual(&got, q.Netem) {
			t.Errorf("netem %s\n predicted %s\n recorded  %s", q.Handle, got.String(), q.Netem)
		}
	}
	if seen != len(recorded) {
		t.Errorf("%d of %d recorded qdiscs found", seen, len(recorded))
	}
}

func TestTheNormOfATreeHasTheShapeTheCompilerBuilds(t *testing.T) {
	tc := &TCTarget{Devs: []string{"wan0"}, Classes: []TCClass{
		{ID: 10, Dir: Upload, Minor: classMinor(10, Upload), Mark: MarkOf(10, Upload), Netem: Netem{Limit: 1000, Delay: 100 * time.Millisecond}},
		{ID: 10, Dir: Download, Minor: classMinor(10, Download), Mark: MarkOf(10, Download), Netem: Netem{Limit: 1000, Loss: 5, Rate: 1_000_000}},
	}}
	n := tc.Norm("wan0")
	if n.Dev != "wan0" || len(n.Qdiscs) != 3 || len(n.Classes) != 3 || len(n.Filters) != 2 {
		t.Fatalf("%+v", n)
	}
	for i, want := range []string{
		"qdisc 1: parent root htb default=0x1",
		"qdisc 24: parent 1:24 netem limit=1000 delay=0.1 jitter=0 delay_corr=0 loss=0 loss_corr=0 reorder=0/0 gap=0 duplicate=0/0 corrupt=0/0 rate=0",
		"qdisc 25: parent 1:25 netem limit=1000 delay=0 jitter=0 delay_corr=0 loss=0.05 loss_corr=0 reorder=0/0 gap=0 duplicate=0/0 corrupt=0/0 rate=125000",
	} {
		if got := n.Qdiscs[i].Line(); got != want {
			t.Errorf("qdisc %d: %q, want %q", i, got, want)
		}
	}
	if n.Classes[0].ID != "1:1" || n.Classes[0].Leaf != "" || n.Classes[1].Leaf != "24:" || n.Classes[2].ID != "1:25" {
		t.Errorf("classes %+v", n.Classes)
	}
	if f := n.Filters[0]; f.Mark != MarkOf(10, Upload) || f.Mask != MarkMask || f.Flowid != "1:24" || f.Kind != "fw" || f.Pref != 1 {
		t.Errorf("filter %+v", f)
	}
	if f := n.Filters[1]; f.Mark != 0x100a0 || f.Flowid != "1:25" {
		t.Errorf("filter %+v", f)
	}
	if got := (*TCTarget)(nil).Norm("x"); len(got.Qdiscs) != 0 || got.Qdiscs == nil {
		t.Errorf("no tree: %+v", got)
	}
	if d := linux.DiffTC(n, n); len(d) != 0 {
		t.Errorf("a tree differs from itself: %v", d)
	}
	// a changed delay is a difference of one object, named
	m := tc.Norm("wan0")
	m.Qdiscs[1].Netem.Delay = 0.2
	if d := linux.DiffTC(n, m); len(d) != 1 {
		t.Errorf("diff: %v", d)
	}
}
