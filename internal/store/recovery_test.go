package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// threeCommitted makes a store whose revisions 1, 2 and 3 were committed one after the other (3 is active and the last known
// good one, 1 and 2 are superseded) and returns it with the configuration of revision 1.
func threeCommitted(t *testing.T) (*Store, string) {
	t.Helper()
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "one")
	if _, err := s.Commit(1, t0); err != nil {
		t.Fatal(err)
	}
	two := changed(t, cfg)
	create(t, s, two, 1, "two")
	if _, err := s.Commit(2, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	three, err := s.Create(changed(t, two), CreateOptions{IfMatch: 2, Message: "three", By: admin, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(three.Id, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestTheFallbackIsTheRevisionThatWasActiveBeforeTheActiveOne(t *testing.T) {
	s, _ := threeCommitted(t)
	r, cfg, err := s.Fallback()
	if err != nil || r.Id != 2 || cfg == nil || r.Status != StatusSuperseded {
		t.Fatalf("Fallback: %+v %v", r, err)
	}
}

func TestTheFallbackSkipsACorruptRevisionAndOneThatWasNeverConfirmed(t *testing.T) {
	s, dir := threeCommitted(t)
	// revision 2 is damaged by hand: revision 1 is the one that worked
	rewrite(t, revFile(dir, 2), func(s string) string { return strings.Replace(s, "90s", "91s", 1) })
	if r, _, err := s.Fallback(); err != nil || r.Id != 1 {
		t.Fatalf("Fallback: %+v %v", r, err)
	}
	// a revision that waited for confirmation and was rolled back never was good
	s2, _ := threeCommitted(t)
	cfg := exampleConfig(t)
	cand, err := s2.Create(changed(t, cfg), CreateOptions{IfMatch: 3, By: admin, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.BeginConfirm(cand.Id, t0, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Rollback(cand.Id, t0); err != nil {
		t.Fatal(err)
	}
	if r, _, err := s2.Fallback(); err != nil || r.Id != 2 {
		t.Fatalf("Fallback after a rollback: %+v %v", r, err)
	}
}

func TestThereIsNoFallbackForTheFirstRevisionOrAnEmptyStore(t *testing.T) {
	s, _ := openStore(t)
	if _, _, err := s.Fallback(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an empty store: %v", err)
	}
	create(t, s, exampleConfig(t), 0, "one")
	if _, err := s.Commit(1, t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Fallback(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a store with one revision: %v", err)
	}
}

func TestTheLastKnownGoodRevisionWins_WhenItIsNotTheActiveOne(t *testing.T) {
	s, dir := threeCommitted(t)
	// the pointer file says: active 3, last known good 1
	rewrite(t, dir+"/config.json", func(s string) string { return strings.Replace(s, `"last_known_good": 3`, `"last_known_good": 1`, 1) })
	s = reopen(t, s, dir)
	r, _, err := s.Fallback()
	if err != nil || r.Id != 1 || r.LastKnownGood == nil || !*r.LastKnownGood {
		t.Fatalf("Fallback: %+v %v", r, err)
	}
}

func TestRestoreMakesTheFallbackActiveAndTheFailedRevisionRolledBack(t *testing.T) {
	s, dir := threeCommitted(t)
	got, err := s.Restore(2, t0.Add(time.Hour))
	if err != nil || got.Id != 2 || got.Status != StatusActive || got.LastKnownGood == nil || !*got.LastKnownGood {
		t.Fatalf("Restore: %+v %v", got, err)
	}
	if s.ActiveID() != 2 {
		t.Errorf("active: %d", s.ActiveID())
	}
	failed, _, err := s.Get(3)
	if err != nil || failed.Status != StatusRolledBack || failed.LastKnownGood != nil {
		t.Errorf("revision 3: %+v %v", failed, err)
	}
	// it survives a restart, and the failed revision is not a fallback again
	s = reopen(t, s, dir)
	if s.ActiveID() != 2 {
		t.Errorf("active after a reopen: %d", s.ActiveID())
	}
	if lkg, _, err := s.LastKnownGood(); err != nil || lkg.Id != 2 {
		t.Errorf("LastKnownGood: %+v %v", lkg, err)
	}
	if r, _, err := s.Fallback(); err != nil || r.Id != 1 {
		t.Errorf("Fallback: %+v %v", r, err)
	}
	// a new revision is based on the restored one
	cand, err := s.Create(changed(t, exampleConfig(t)), CreateOptions{IfMatch: 2, By: admin, Now: t0})
	if err != nil || cand.Base == nil || *cand.Base != 2 {
		t.Fatalf("Create: %+v %v", cand, err)
	}
}

func TestRestoreRefusesWhatWasNeverGoodAndWhatIsActive(t *testing.T) {
	s, _ := threeCommitted(t)
	if _, err := s.Restore(3, t0); err == nil {
		t.Error("the active revision was restored")
	}
	cand, err := s.Create(changed(t, exampleConfig(t)), CreateOptions{IfMatch: 3, By: admin, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	var nac *ErrNotACandidate
	if _, err := s.Restore(cand.Id, t0); !errors.As(err, &nac) {
		t.Errorf("a candidate was restored: %v", err)
	}
	if _, err := s.Restore(99, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("a revision that does not exist: %v", err)
	}
}

func TestRestoreWorksWhenTheFailedRevisionIsTheDamagedPart(t *testing.T) {
	s, dir := threeCommitted(t)
	rewrite(t, revFile(dir, 3), func(s string) string { return strings.Replace(s, "90s", "91s", 1) })
	rewrite(t, dir+"/revisions/000003.status.json", func(string) string { return "{not json" })
	if _, _, err := s.Active(); asCorrupt(err) == nil {
		t.Fatalf("the active revision reads: %v", err)
	}
	if _, err := s.Restore(2, t0.Add(time.Hour)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, _, err := s.Active(); err != nil {
		t.Errorf("Active: %v", err)
	}
	st, err := s.readStatus(3)
	if err != nil || st.Status != StatusRolledBack {
		t.Errorf("the status of revision 3: %+v %v", st, err)
	}
}

func TestACrashBetweenTheStatusAndThePointerOfARestoreIsRepaired(t *testing.T) {
	s, dir := threeCommitted(t)
	// the status of revision 2 says active and revision 3 rolled back, the pointer still names 3
	if err := s.writeStatus(2, statusFile{Status: StatusActive, ConfirmedAt: &t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeStatus(3, statusFile{Status: StatusRolledBack}); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s, dir)
	if s.ActiveID() != 3 {
		t.Fatalf("active: %d", s.ActiveID())
	}
	a, _, err := s.Active()
	if err != nil || a.Status != StatusActive {
		t.Fatalf("Active: %+v %v", a, err)
	}
	if r, _, err := s.Fallback(); err != nil || r.Id != 2 {
		t.Fatalf("Fallback: %+v %v", r, err)
	}
}

func TestMarkGoodMovesTheLastKnownGoodPointerToTheActiveRevision(t *testing.T) {
	s, dir := threeCommitted(t)
	rewrite(t, dir+"/config.json", func(s string) string { return strings.Replace(s, `"last_known_good": 3`, `"last_known_good": 1`, 1) })
	s = reopen(t, s, dir)
	if err := s.MarkGood(2); err == nil {
		t.Error("a revision that is not the active one was marked good")
	}
	if err := s.MarkGood(3); err != nil {
		t.Fatal(err)
	}
	if lkg, _, err := s.LastKnownGood(); err != nil || lkg.Id != 3 {
		t.Fatalf("LastKnownGood: %+v %v", lkg, err)
	}
	if err := s.MarkGood(3); err != nil {
		t.Errorf("marking twice: %v", err)
	}
}

func TestARevisionThatFailedAtStartIsNotTheFallbackUntilItAppliesAgain(t *testing.T) {
	s, dir := threeCommitted(t)
	// revision 3 failed at start, revision 2 (its fallback) too, and the administrator went on with revision 4
	for _, id := range []int64{2, 3} {
		if err := s.MarkBootFailed(id, t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkBootFailed(3, t0); err != nil {
		t.Errorf("marking twice: %v", err)
	}
	four, err := s.Create(changed(t, exampleConfig(t)), CreateOptions{IfMatch: 3, Message: "four", By: admin, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(four.Id, t0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// 3 and 2 are superseded and were confirmed, but did not apply at start: 1 is the fallback
	if r, _, err := s.Fallback(); err != nil || r.Id != 1 {
		t.Fatalf("Fallback: %+v %v", r, err)
	}
	// the mark survives a restart of the process
	s = reopen(t, s, dir)
	if r, _, err := s.Fallback(); err != nil || r.Id != 1 {
		t.Fatalf("Fallback after reopening: %+v %v", r, err)
	}
	if err := s.MarkBootFailed(1, t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Fallback(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("every earlier revision failed at start: %v", err)
	}
	// the last known good pointer is not an exception: it is skipped too
	rewrite(t, dir+"/config.json", func(s string) string { return strings.Replace(s, `"last_known_good": 4`, `"last_known_good": 3`, 1) })
	s = reopen(t, s, dir)
	if _, _, err := s.Fallback(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the last known good revision failed at start: %v", err)
	}
}

func TestMarkGoodAndRestoreClearTheMarkOfAFailedStart(t *testing.T) {
	s, _ := threeCommitted(t)
	if err := s.MarkBootFailed(3, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkGood(3); err != nil {
		t.Fatal(err)
	}
	if st, err := s.readStatus(3); err != nil || st.BootFailedAt != nil {
		t.Errorf("MarkGood: %+v %v", st, err)
	}
	// revision 2 failed at start as a fallback and is restored later (the active revision 3 does not apply): it is good again
	if err := s.MarkBootFailed(2, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(2, t0); err != nil {
		t.Fatal(err)
	}
	if st, err := s.readStatus(2); err != nil || st.BootFailedAt != nil {
		t.Errorf("Restore: %+v %v", st, err)
	}
	if err := s.MarkBootFailed(99, t0); err == nil {
		t.Error("a revision that does not exist was marked")
	}
}
