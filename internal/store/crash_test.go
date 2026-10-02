package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A commit, a confirm and a rollback write the status file and then the pointer. These tests
// reproduce the states a crash between the two leaves behind and check that Open repairs them.

func statusOf(t *testing.T, s *Store, id int64) string {
	t.Helper()
	r, _, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return string(r.Status)
}

func setStatusFile(t *testing.T, dir string, id int64, status string) {
	t.Helper()
	p := filepath.Join(dir, "revisions", revisionName(id)+".status.json")
	rewrite(t, p, func(s string) string {
		for _, old := range []string{StatusCandidate, StatusPendingConfirm, StatusActive, StatusSuperseded, StatusRolledBack} {
			s = strings.Replace(s, `"status": "`+old+`"`, `"status": "`+status+`"`, 1)
		}
		return s
	})
}

func TestAProcessThatEndsWhileARevisionWaitsForConfirmationRollsItBack(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	mustBegin(t, s, 2)

	s2 := reopen(t, s, dir) // the process restarted, or the machine rebooted, inside the window
	if s2.ActiveID() != 1 {
		t.Fatal("a reboot inside the window boots the previous revision")
	}
	if _, ok := s2.PendingConfirm(); ok {
		t.Fatal("nothing can wait for confirmation after a restart")
	}
	if got := statusOf(t, s2, 2); got != StatusRolledBack {
		t.Fatalf("the revision was never confirmed: %s", got)
	}
	if lkg, _, _ := s2.LastKnownGood(); lkg.Id != 1 {
		t.Fatalf("an unconfirmed revision never becomes last known good: %d", lkg.Id)
	}
	// the store is not blocked: the next change can be applied
	var notCand *ErrNotACandidate
	if _, err := s2.Confirm(2, t0); !errors.As(err, &notCand) {
		t.Fatalf("confirming after the restart: %v", err)
	}
	r3 := create(t, s2, changed(t, cfg), 1, "")
	if _, err := s2.BeginConfirm(r3.Id, t0, t0.Add(time.Minute)); err != nil {
		t.Fatalf("the next apply must work: %v", err)
	}
}

func TestCrashInCommitBetweenTheStatusAndThePointerLeavesTheCandidate(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	// the crash: revision 2 was marked active, the pointer still names revision 1
	setStatusFile(t, dir, 2, StatusActive)

	s2 := reopen(t, s, dir)
	if s2.ActiveID() != 1 || statusOf(t, s2, 2) != StatusCandidate || statusOf(t, s2, 1) != StatusActive {
		t.Fatalf("active %d, rev 1 %s, rev 2 %s", s2.ActiveID(), statusOf(t, s2, 1), statusOf(t, s2, 2))
	}
	// the commit can simply be done again
	if _, err := s2.Commit(2, t0.Add(time.Hour)); err != nil {
		t.Fatalf("commit again: %v", err)
	}
	if statusOf(t, s2, 1) != StatusSuperseded || s2.ActiveID() != 2 {
		t.Fatal("the second commit must work")
	}
}

func TestCrashInCommitAfterThePointerMovedSupersedesTheOldRevision(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	mustCommit(t, s, 2)
	// the crash: the pointer moved to revision 2, revision 1 still says active
	setStatusFile(t, dir, 1, StatusActive)

	s2 := reopen(t, s, dir)
	if statusOf(t, s2, 1) != StatusSuperseded || statusOf(t, s2, 2) != StatusActive || s2.ActiveID() != 2 {
		t.Fatalf("rev 1 %s, rev 2 %s", statusOf(t, s2, 1), statusOf(t, s2, 2))
	}
}

func TestCrashInConfirmBetweenTheStatusAndThePointerRollsTheRevisionBack(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	mustBegin(t, s, 2)
	// the crash: revision 2 was marked active, the pointer still says pending and names revision 1
	setStatusFile(t, dir, 2, StatusActive)

	s2 := reopen(t, s, dir)
	if s2.ActiveID() != 1 || statusOf(t, s2, 2) != StatusRolledBack || statusOf(t, s2, 1) != StatusActive {
		t.Fatalf("active %d, rev 1 %s, rev 2 %s", s2.ActiveID(), statusOf(t, s2, 1), statusOf(t, s2, 2))
	}
	if _, ok := s2.PendingConfirm(); ok {
		t.Fatal("nothing waits any more")
	}
}

func TestCrashInRollbackBetweenTheStatusAndThePointerClearsThePending(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	mustBegin(t, s, 2)
	setStatusFile(t, dir, 2, StatusRolledBack) // the pointer still says pending

	s2 := reopen(t, s, dir)
	if _, ok := s2.PendingConfirm(); ok || s2.ActiveID() != 1 || statusOf(t, s2, 2) != StatusRolledBack {
		t.Fatal("the pending pointer must be cleared and the revision stay rolled back")
	}
}

func TestCrashInBeginConfirmBetweenTheStatusAndThePointerLeavesTheCandidate(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	setStatusFile(t, dir, 2, StatusPendingConfirm) // no pointer: the process ended after the status

	s2 := reopen(t, s, dir)
	if statusOf(t, s2, 2) != StatusCandidate {
		t.Fatalf("a revision that never got its pointer was not applied for good: %s", statusOf(t, s2, 2))
	}
	if _, err := s2.BeginConfirm(2, t0, t0.Add(time.Minute)); err != nil {
		t.Fatalf("it can be applied again: %v", err)
	}
}

func TestAnActiveRevisionWhoseStatusWasLostIsMarkedActiveAgain(t *testing.T) {
	s, dir := openStore(t)
	create(t, s, exampleConfig(t), 0, "")
	mustCommit(t, s, 1)
	setStatusFile(t, dir, 1, StatusSuperseded)
	if got := statusOf(t, reopen(t, s, dir), 1); got != StatusActive {
		t.Fatalf("the pointer is the truth, the status = %s", got)
	}
}

func TestAConfirmAfterTheDeadlineIsRefused(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	deadline := t0.Add(time.Minute)
	if _, err := s.BeginConfirm(2, t0, deadline); err != nil {
		t.Fatal(err)
	}
	var expired *ErrConfirmExpired
	if _, err := s.Confirm(2, deadline.Add(time.Second)); !errors.As(err, &expired) || expired.Revision != 2 || !expired.Deadline.Equal(deadline) {
		t.Fatalf("err = %v", err)
	}
	if lkg, _, _ := s.LastKnownGood(); lkg.Id != 1 || s.ActiveID() != 1 {
		t.Fatal("a late confirm must change nothing")
	}
	if _, err := s.Confirm(2, deadline); err != nil { // at the deadline is still in time
		t.Fatalf("at the deadline: %v", err)
	}
	if !strings.Contains((&ErrConfirmExpired{Revision: 2, Deadline: deadline}).Error(), "not confirmed before") {
		t.Error("message")
	}
}

func TestALostPointerIsRebuiltFromTheRevisions(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	mustCommit(t, s, 2)
	create(t, s, changed(t, cfg), 2, "")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if s2.ActiveID() != 2 {
		t.Fatalf("active = %d, want the newest confirmed one", s2.ActiveID())
	}
	if lkg, _, _ := s2.LastKnownGood(); lkg.Id != 2 {
		t.Fatalf("last known good = %d", lkg.Id)
	}
	if r := create(t, s2, cfg, 2, ""); r.Id != 4 {
		t.Fatalf("ids continue after the highest revision: %d", r.Id)
	}
}

func TestOnlyOneProcessMayHaveTheStoreOpen(t *testing.T) {
	s, dir := openStore(t)
	if _, err := Open(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second Open must fail with ErrLocked, got %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close twice: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("after Close the directory is free: %v", err)
	}
	_ = s2.Close()
}

func TestAFailedOpenReleasesTheLock(t *testing.T) {
	dir := dirWithTwo(t)
	rewrite(t, filepath.Join(dir, "config.json"), func(string) string { return "garbage" })
	if _, err := Open(dir); asCorrupt(err) == nil {
		t.Fatalf("Open: %v", err)
	}
	// repair the file; the lock of the failed attempt must not linger
	rewrite(t, filepath.Join(dir, "config.json"), func(string) string { return `{"schema_version":1,"active":1,"next_id":3}` })
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	_ = s.Close()
}

func TestPruneRemovesStaleCandidatesAndKeepsCurrentOnes(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	// three candidates on revision 1: one is applied, the other two become stale
	for i := 0; i < 3; i++ {
		create(t, s, changed(t, cfg), 1, "")
	}
	mustCommit(t, s, 2)
	current := create(t, s, changed(t, cfg), 2, "current")

	removed, err := s.Prune(100) // a generous keep: only the stale candidates go
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0] != 3 || removed[1] != 4 {
		t.Fatalf("removed = %v, want the two stale candidates 3 and 4", removed)
	}
	if _, _, err := s.Get(current.Id); err != nil {
		t.Fatalf("the current candidate must stay: %v", err)
	}
	if _, _, err := s.Get(1); err != nil {
		t.Fatalf("keep=100 keeps the history: %v", err)
	}
}

func TestPruneWithANegativeOrZeroKeepDoesNotPanic(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	mustCommit(t, s, 2)
	for _, keep := range []int{-5, 0} {
		if _, err := s.Prune(keep); err != nil {
			t.Fatalf("keep %d: %v", keep, err)
		}
	}
	if s.ActiveID() != 2 {
		t.Fatal("the active revision must survive")
	}
	if _, _, err := s.LastKnownGood(); err != nil {
		t.Fatal("the last known good must survive")
	}
}

func TestVerifyChecksThePointers(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	if bad, err := s.Verify(); err != nil || len(bad) != 0 {
		t.Fatalf("a healthy store: %+v %v", bad, err)
	}
	// the pointer names a revision whose file is gone
	s.mu.Lock()
	s.st.LastKnownGood = 9
	s.mu.Unlock()
	bad, err := s.Verify()
	if err != nil || len(bad) != 1 || bad[0].Revision != 9 || !strings.Contains(bad[0].Err.Error(), "last_known_good") {
		t.Fatalf("bad = %+v %v", bad, err)
	}
	s.mu.Lock()
	s.st.LastKnownGood = 1
	s.mu.Unlock()
	// the active revision is not marked active on disk
	setStatusFile(t, dir, 1, StatusSuperseded)
	bad, _ = s.Verify()
	if len(bad) == 0 || !strings.Contains(bad[0].Err.Error(), "active points to revision 1, which is superseded") {
		t.Fatalf("bad = %+v", bad)
	}
}
