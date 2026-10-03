package audit

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
)

func TestEntriesAreNumberedStoredAndPaged(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	l, err := Open(dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		who := "admin"
		if i%2 == 1 {
			who = "ci"
		}
		clk.Advance(time.Second)
		if _, err := l.Append(Entry{Actor: Actor{Type: "user", ID: who}, Via: "api", Action: "revision.apply", Revision: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	page, next, err := l.List(Filter{}, "", 3)
	if err != nil || len(page) != 3 || page[0].ID != "7" || next != "5" {
		t.Fatalf("%+v %q %v", page, next, err)
	}
	page, next, _ = l.List(Filter{}, next, 3)
	if page[0].ID != "4" || next != "2" {
		t.Fatalf("%+v %q", page, next)
	}
	page, next, _ = l.List(Filter{}, next, 3)
	if len(page) != 1 || next != "" {
		t.Fatalf("%+v %q", page, next)
	}
	if ci, _, _ := l.List(Filter{Actor: "ci"}, "", 100); len(ci) != 3 {
		t.Errorf("the actor filter: %d", len(ci))
	}
	if none, _, _ := l.List(Filter{Action: "x"}, "", 100); len(none) != 0 {
		t.Errorf("the action filter: %d", len(none))
	}
	if _, _, err := l.List(Filter{}, "x", 1); err == nil {
		t.Error("a bad cursor is accepted")
	}
	_ = l.Close()

	// the log survives a restart and goes on numbering; a torn last line is skipped
	f, _ := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"id":"8","tim`)
	_ = f.Close()
	l2, err := Open(dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	e, _ := l2.Append(Entry{Actor: Actor{Type: "system", ID: "system"}, Action: "start"})
	if e.ID != "8" || e.Via != "system" {
		t.Errorf("%+v", e)
	}
	if all, _, _ := l2.List(Filter{}, "", 100); len(all) != 8 {
		t.Errorf("%d entries after the restart", len(all))
	}
	if fi, _ := os.Stat(filepath.Join(dir, "audit.jsonl")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
}

func TestATimeFilter(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	l, _ := Open(t.TempDir(), clk)
	defer func() { _ = l.Close() }()
	for i := 0; i < 5; i++ {
		_, _ = l.Append(Entry{Actor: Actor{Type: "system", ID: "system"}, Action: "a" + strconv.Itoa(i)})
		clk.Advance(time.Minute)
	}
	got, _, _ := l.List(Filter{Since: clk.Now().Add(-3 * time.Minute), Until: clk.Now().Add(-1 * time.Minute)}, "", 10)
	if len(got) != 2 {
		t.Errorf("%+v", got)
	}
}
