package audit

import (
	"context"
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

// M5-04 test: an entry older than the retention is dropped when the log reopens.
func TestOldEntriesAreDropped(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	l, err := Open(dir, clk, WithRetention(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	old, err := l.Append(Entry{Actor: Actor{Type: "system", ID: "system"}, Action: "old"})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(20 * time.Minute)
	recent, err := l.Append(Entry{Actor: Actor{Type: "system", ID: "system"}, Action: "recent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir, clk, WithRetention(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	all, _, err := l2.List(Filter{}, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != recent.ID {
		t.Fatalf("%+v", all)
	}
	for _, e := range all {
		if e.ID == old.ID {
			t.Error("the old entry survived the reopen")
		}
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

func countActions(t *testing.T, l *Log) map[string]int {
	t.Helper()
	all, _, err := l.List(Filter{}, "", 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, e := range all {
		n[e.Action]++
	}
	return n
}

// The bound holds while the log runs, not only at the start, and a flood of bulk entries pushes out bulk entries, not
// the history (P6-AUDIT-15).
func TestTheBoundHoldsWhileTheLogRunsAndBulkEntriesGoFirst(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	l, err := Open(dir, clk, WithMaxEntries(20), WithBulkActions("flood"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	system := Actor{Type: "system", ID: "system"}
	for i := 0; i < 5; i++ {
		if _, err := l.Append(Entry{Actor: system, Action: "history"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 200; i++ {
		if _, err := l.Append(Entry{Actor: system, Action: "flood"}); err != nil {
			t.Fatal(err)
		}
		if n := size(l); n > 20 {
			t.Fatalf("%d entries after %d appends, the bound is 20", n, i+1)
		}
	}
	if n := countActions(t, l); n["history"] != 5 || n["flood"] == 0 || n["flood"] > 20 {
		t.Errorf("%v", n)
	}
	// the file is what the memory is, and later entries go to the new file
	if _, err := l.Append(Entry{Actor: system, Action: "after"}); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	l2, err := Open(dir, clk, WithMaxEntries(20), WithBulkActions("flood"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	if n := countActions(t, l2); n["history"] != 5 || n["after"] != 1 || n["flood"] == 0 || size(l2) > 20 {
		t.Errorf("after the restart: %v", n)
	}
}

// Without bulk actions the oldest half goes, as before; ids keep growing.
func TestTheOldestHalfGoesWhenTheBoundIsExceeded(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	l, err := Open(t.TempDir(), clk, WithMaxEntries(10))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	var last Entry
	for i := 0; i < 11; i++ {
		if last, err = l.Append(Entry{Actor: Actor{Type: "system", ID: "system"}, Action: "x" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	all, _, _ := l.List(Filter{}, "", 100)
	if len(all) != 5 || all[0].ID != last.ID || last.ID != "11" {
		t.Errorf("%d entries, newest %s", len(all), all[0].ID)
	}
}

// The daily prune writes the file anew; an entry after it must not go to the file that was replaced.
func TestEntriesAfterTheDailyPruneReachTheFile(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	l, err := Open(dir, clk, WithRetention(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	if _, err := l.Append(Entry{Actor: Actor{Type: "system", ID: "system"}, Action: "old"}); err != nil {
		t.Fatal(err)
	}
	clk.BlockUntil(1)
	clk.Advance(24 * time.Hour)
	deadline := time.Now().Add(5 * time.Second)
	for size(l) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the entry past retention is not dropped")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := l.Append(Entry{Actor: Actor{Type: "system", ID: "system"}, Action: "new"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	_ = l.Close()
	l2, err := Open(dir, clk, WithRetention(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	if n := countActions(t, l2); n["new"] != 1 || n["old"] != 0 {
		t.Errorf("%v", n)
	}
}

// size is how many entries the log holds.
func size(l *Log) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
