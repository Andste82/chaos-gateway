package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

var admin = model.Actor{Type: "user", Id: "admin"}

func exampleConfig(t *testing.T) *model.Configuration {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", "configuration.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.NewCandidate(nil, raw, domain.FormatYAML, domain.CandidateFull, t0)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func openStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "etc")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

// reopen closes a store and opens the directory again, like a restart of the process.
func reopen(t *testing.T, s *Store, dir string) *Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	return s2
}

func create(t *testing.T, s *Store, cfg *model.Configuration, ifMatch int64, msg string) model.Revision {
	t.Helper()
	r, err := s.Create(cfg, CreateOptions{IfMatch: ifMatch, Message: msg, By: admin, Now: t0})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return r
}

func changed(t *testing.T, cfg *model.Configuration) *model.Configuration {
	t.Helper()
	out, err := domain.NewCandidate(cfg, []byte(`{"settings":{"commit_confirm_timeout":"90s"}}`), domain.FormatJSON, domain.CandidatePatch, t0)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestANewStoreIsEmpty(t *testing.T) {
	s, dir := openStore(t)
	if _, _, err := s.Active(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Active: %v", err)
	}
	if _, _, err := s.LastKnownGood(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LastKnownGood: %v", err)
	}
	if got, err := s.List(ListOptions{}); err != nil || len(got) != 0 {
		t.Fatalf("List: %v %v", got, err)
	}
	if s.ActiveID() != 0 {
		t.Error("ActiveID must be 0")
	}
	if _, ok := s.PendingConfirm(); ok {
		t.Error("nothing is pending")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || !strings.Contains(string(raw), `"schema_version": 1`) || !strings.Contains(string(raw), `"next_id": 1`) {
		t.Fatalf("config.json: %s %v", raw, err)
	}
}

func TestCreateStoresACandidateThatGetReturns(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	r := create(t, s, cfg, 0, "first")
	if r.Id != 1 || r.Status != StatusCandidate || r.Base != nil || *r.Message != "first" || r.SchemaVersion != 1 ||
		!r.CreatedAt.Equal(t0) || r.CreatedBy != admin || r.AppliedAt != nil || r.LastKnownGood != nil {
		t.Fatalf("revision = %+v", r)
	}
	got, back, err := s.Get(1)
	if err != nil || got.Id != 1 || !domain.Equal(back, cfg) {
		t.Fatalf("Get: %+v %v", got, err)
	}
	for _, name := range []string{"000001.json", "000001.status.json"} {
		if _, err := os.Stat(filepath.Join(dir, "revisions", name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	if _, _, err := s.Get(2); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(2): %v", err)
	}
}

func TestCreateChecksTheRevisionTheChangeIsBasedOn(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	var conflict *ErrRevisionConflict
	if _, err := s.Create(cfg, CreateOptions{IfMatch: 7, Now: t0}); !errors.As(err, &conflict) || conflict.Active != 0 {
		t.Fatalf("a wrong IfMatch before the first revision: %v", err)
	}
	r := create(t, s, cfg, 0, "")
	if _, err := s.Commit(r.Id, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(cfg, CreateOptions{IfMatch: 0, Now: t0}); !errors.As(err, &conflict) || conflict.Active != 1 {
		t.Fatalf("IfMatch 0 after revision 1: %v", err)
	}
	next := create(t, s, changed(t, cfg), 1, "second")
	if next.Id != 2 || *next.Base != 1 {
		t.Fatalf("second revision = %+v", next)
	}
}

func TestAnInvalidConfigurationIsNeverStored(t *testing.T) {
	s, dir := openStore(t)
	bad := exampleConfig(t)
	d := *bad.Devices
	for id, dev := range d {
		dev.Identifiers = nil // a device without identifiers
		d[id] = dev
	}
	_, err := s.Create(bad, CreateOptions{Now: t0})
	var ve domain.ValidationErrors
	if !errors.As(err, &ve) || len(ve) == 0 {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "revisions"))
	if len(entries) != 0 {
		t.Fatalf("files were written: %v", entries)
	}
	// and the id was not used up
	if r := create(t, s, exampleConfig(t), 0, ""); r.Id != 1 {
		t.Fatalf("id = %d", r.Id)
	}
}

func TestSecretsAreRefusedNotDroppedSilently(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	cfg.Secrets = &model.ConfigurationSecrets{}
	if _, err := s.Create(cfg, CreateOptions{Now: t0}); !errors.Is(err, ErrSecretsPresent) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "revisions")); len(entries) != 0 {
		t.Fatalf("nothing may be written: %v", entries)
	}
	cfg.Secrets = nil
	create(t, s, cfg, 0, "")
	raw, _ := os.ReadFile(filepath.Join(dir, "revisions", "000001.json"))
	if strings.Contains(string(raw), "secrets") || strings.Contains(string(raw), "private_key") {
		t.Fatal("the revision file must not contain secrets")
	}
}

func TestACandidateIsStoredWithUUIDsOnly(t *testing.T) {
	s, dir := openStore(t)
	// the example configuration refers to objects by name
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", "configuration.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := domain.ParseDocument(raw, domain.FormatYAML)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := domain.DecodeConfigurationDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if *(*cfg.Devices)["1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a"].Network != "IoT" {
		t.Fatal("the test needs a configuration with names")
	}
	create(t, s, cfg, 0, "")
	file, _ := os.ReadFile(filepath.Join(dir, "revisions", "000001.json"))
	if strings.Contains(string(file), `"network": "IoT"`) || strings.Contains(string(file), `"members": [
`+"          \"esp32-42\"") {
		t.Fatalf("a name was stored:\n%s", file)
	}
	_, back, _ := s.Get(1)
	if got := *(*back.Devices)["1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a"].Network; got != "0b7c6a3e-1f2d-4c5b-9a8e-7d6c5b4a3f21" {
		t.Fatalf("network = %s", got)
	}
	// and the stored configuration is stable under another normalization
	again, errs := domain.Normalize(back)
	if len(errs) != 0 || !domain.Equal(again, back) {
		t.Fatalf("not normalized: %v", errs)
	}
}

func TestCommitMakesTheCandidateActiveAndSupersedesThePreviousOne(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	r1 := create(t, s, cfg, 0, "")
	got, err := s.Commit(r1.Id, t0.Add(time.Minute))
	if err != nil || got.Status != StatusActive || !got.AppliedAt.Equal(t0.Add(time.Minute)) || got.ConfirmedAt == nil || got.LastKnownGood == nil || !*got.LastKnownGood {
		t.Fatalf("Commit: %+v %v", got, err)
	}
	active, _, err := s.Active()
	if err != nil || active.Id != 1 {
		t.Fatalf("Active: %+v %v", active, err)
	}

	r2 := create(t, s, changed(t, cfg), 1, "")
	if _, err := s.Commit(r2.Id, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	old, _, _ := s.Get(1)
	if old.Status != StatusSuperseded || old.LastKnownGood != nil {
		t.Fatalf("revision 1 = %+v", old)
	}
	lkg, _, err := s.LastKnownGood()
	if err != nil || lkg.Id != 2 {
		t.Fatalf("LastKnownGood: %+v %v", lkg, err)
	}
}

func TestCommitRefusesWhatIsNotACandidateOrIsBasedOnAnOldRevision(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	a := create(t, s, cfg, 0, "a")
	b := create(t, s, changed(t, cfg), 0, "b") // both based on "nothing"
	if _, err := s.Commit(a.Id, t0); err != nil {
		t.Fatal(err)
	}
	var conflict *ErrRevisionConflict
	if _, err := s.Commit(b.Id, t0); !errors.As(err, &conflict) || conflict.Active != 1 {
		t.Fatalf("the second candidate is based on an old revision: %v", err)
	}
	var notCand *ErrNotACandidate
	if _, err := s.Commit(a.Id, t0); !errors.As(err, &notCand) || notCand.Status != StatusActive {
		t.Fatalf("committing twice: %v", err)
	}
	if _, err := s.Commit(99, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown revision: %v", err)
	}
}

func TestCommitConfirmKeepsThePreviousRevisionActiveUntilConfirmed(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	r1 := create(t, s, cfg, 0, "")
	mustCommit(t, s, r1.Id)
	r2 := create(t, s, changed(t, cfg), 1, "")

	deadline := t0.Add(time.Minute)
	got, err := s.BeginConfirm(r2.Id, t0, deadline)
	if err != nil || got.Status != StatusPendingConfirm || got.AppliedAt == nil || got.ConfirmedAt != nil {
		t.Fatalf("BeginConfirm: %+v %v", got, err)
	}
	if s.ActiveID() != 1 {
		t.Fatal("a reboot inside the window boots the previous revision: it must stay active")
	}
	if p, ok := s.PendingConfirm(); !ok || p.Revision != 2 || p.Previous != 1 || !p.Deadline.Equal(deadline) {
		t.Fatalf("PendingConfirm: %+v %v", p, ok)
	}

	// only one revision can wait; nothing else can be committed meanwhile
	r3 := create(t, s, cfg, 1, "")
	var pend *ErrConfirmPending
	if _, err := s.BeginConfirm(r3.Id, t0, deadline); !errors.As(err, &pend) || pend.Pending != 2 {
		t.Fatalf("second BeginConfirm: %v", err)
	}
	if _, err := s.Commit(r3.Id, t0); !errors.As(err, &pend) {
		t.Fatalf("Commit while pending: %v", err)
	}

	confirmed, err := s.Confirm(2, t0.Add(30*time.Second))
	if err != nil || confirmed.Status != StatusActive || !confirmed.ConfirmedAt.Equal(t0.Add(30*time.Second)) || confirmed.LastKnownGood == nil {
		t.Fatalf("Confirm: %+v %v", confirmed, err)
	}
	if s.ActiveID() != 2 {
		t.Fatal("the confirmed revision must be active")
	}
	if _, ok := s.PendingConfirm(); ok {
		t.Fatal("nothing waits any more")
	}
	if old, _, _ := s.Get(1); old.Status != StatusSuperseded {
		t.Fatalf("revision 1 = %s", old.Status)
	}
	// the state is on disk: a restart sees the confirmed revision
	if s2 := reopen(t, s, dir); s2.ActiveID() != 2 {
		t.Fatalf("after a restart the active revision is %d", s2.ActiveID())
	}
}

func TestARevisionThatIsNotConfirmedIsRolledBackAndNeverBecomesLastKnownGood(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	r1 := create(t, s, cfg, 0, "")
	mustCommit(t, s, r1.Id)
	r2 := create(t, s, changed(t, cfg), 1, "")
	mustBegin(t, s, r2.Id)
	got, err := s.Rollback(2, t0.Add(time.Minute))
	if err != nil || got.Status != StatusRolledBack {
		t.Fatalf("Rollback: %+v %v", got, err)
	}
	if s.ActiveID() != 1 {
		t.Fatal("the previous revision must stay active")
	}
	if lkg, _, _ := s.LastKnownGood(); lkg.Id != 1 {
		t.Fatalf("an unconfirmed revision must never become last known good: %d", lkg.Id)
	}
	if _, ok := s.PendingConfirm(); ok {
		t.Fatal("nothing waits any more")
	}
	var notCand *ErrNotACandidate
	if _, err := s.Confirm(2, t0); !errors.As(err, &notCand) {
		t.Fatalf("confirming a rolled-back revision: %v", err)
	}
	if _, err := s.Rollback(1, t0); !errors.As(err, &notCand) {
		t.Fatalf("rolling back an active revision: %v", err)
	}
	// a new change can be applied again
	r3 := create(t, s, changed(t, cfg), 1, "")
	if _, err := s.BeginConfirm(r3.Id, t0, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmNeedsTheRevisionThatWaits(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	r1 := create(t, s, cfg, 0, "")
	var notCand *ErrNotACandidate
	if _, err := s.Confirm(r1.Id, t0); !errors.As(err, &notCand) || notCand.Status != StatusCandidate {
		t.Fatalf("confirming a candidate: %v", err)
	}
	if _, err := s.Confirm(42, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown revision: %v", err)
	}
}

func TestDiscardDeletesACandidateAndItsIDIsNotReused(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	r1 := create(t, s, cfg, 0, "")
	if err := s.Discard(r1.Id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Discard: %v", err)
	}
	if r := create(t, s, cfg, 0, ""); r.Id != 2 {
		t.Fatalf("a discarded id must not be reused, got %d", r.Id)
	}
	if err := s.Discard(1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("discarding twice: %v", err)
	}
	mustCommit(t, s, 2)
	var notCand *ErrNotACandidate
	if err := s.Discard(2); !errors.As(err, &notCand) {
		t.Fatalf("discarding an active revision: %v", err)
	}
	// ids are not reused after a restart either
	s2 := reopen(t, s, dir)
	if r := create(t, s2, changed(t, cfg), 2, ""); r.Id != 3 {
		t.Fatalf("id after a restart = %d", r.Id)
	}
}

func TestListIsNewestFirstAndFiltersAndPages(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	for i := 0; i < 5; i++ {
		create(t, s, cfg, 0, "")
	}
	mustCommit(t, s, 5)
	all, _ := s.List(ListOptions{})
	var ids []int64
	for _, r := range all {
		ids = append(ids, r.Id)
	}
	if len(ids) != 5 || ids[0] != 5 || ids[4] != 1 {
		t.Fatalf("ids = %v", ids)
	}
	if active, _ := s.List(ListOptions{Status: StatusActive}); len(active) != 1 || active[0].Id != 5 {
		t.Fatalf("active = %+v", active)
	}
	if cands, _ := s.List(ListOptions{Status: StatusCandidate}); len(cands) != 4 {
		t.Fatalf("candidates = %d", len(cands))
	}
	page, _ := s.List(ListOptions{Before: 4, Limit: 2})
	if len(page) != 2 || page[0].Id != 3 || page[1].Id != 2 {
		t.Fatalf("page = %+v", page)
	}
}

func TestPruneKeepsTheNewestAndEverythingInUse(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	r1 := create(t, s, cfg, 0, "")
	mustCommit(t, s, r1.Id)
	for i := 0; i < 6; i++ {
		r := create(t, s, changed(t, cfg), s.ActiveID(), "")
		if i < 5 {
			mustCommit(t, s, r.Id)
		}
	} // 1..7: 1-6 superseded or active (6 active), 7 candidate
	removed, err := s.Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	// keep the newest two (6, 7); 6 is also active and last known good; 7 is a candidate
	want := []int64{1, 2, 3, 4, 5}
	if len(removed) != len(want) {
		t.Fatalf("removed = %v", removed)
	}
	for i, id := range want {
		if removed[i] != id {
			t.Fatalf("removed = %v, want %v", removed, want)
		}
	}
	if _, _, err := s.Active(); err != nil {
		t.Fatalf("the active revision must survive: %v", err)
	}
	if r, _ := s.List(ListOptions{}); len(r) != 2 {
		t.Fatalf("%d revisions left", len(r))
	}
}

func TestPruneNeverDeletesTheLastKnownGoodOrAPendingRevision(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	r1 := create(t, s, cfg, 0, "")
	mustCommit(t, s, r1.Id) // active and last known good
	for i := 0; i < 3; i++ {
		create(t, s, cfg, 1, "")
	}
	mustBegin(t, s, 2) // pending
	removed, _ := s.Prune(1)
	for _, id := range removed {
		if id == 1 || id == 2 {
			t.Fatalf("revision %d is in use and must not be pruned (removed %v)", id, removed)
		}
	}
}

func TestDiffBetweenStoredRevisions(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "")
	changes, err := s.Diff(1, 2)
	if err != nil || len(changes) != 1 || changes[0].Kind != "settings" || !strings.Contains(changes[0].Summary, "commit_confirm_timeout 60s → 90s") {
		t.Fatalf("Diff: %+v %v", changes, err)
	}
	if _, err := s.Diff(1, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Diff with an unknown revision: %v", err)
	}
	if _, err := s.Diff(9, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Diff with an unknown revision: %v", err)
	}
}

func TestConcurrentCreatesGetUniqueIDs(t *testing.T) {
	s, _ := openStore(t)
	cfg := exampleConfig(t)
	var wg sync.WaitGroup
	ids := make(chan int64, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Create(cfg, CreateOptions{Now: t0})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- r.Id
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("id %d given twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 20 {
		t.Fatalf("%d ids", len(seen))
	}
}

func TestEverythingSurvivesAReopen(t *testing.T) {
	s, dir := openStore(t)
	cfg := exampleConfig(t)
	create(t, s, cfg, 0, "one")
	mustCommit(t, s, 1)
	create(t, s, changed(t, cfg), 1, "two")
	s2 := reopen(t, s, dir)
	if s2.ActiveID() != 1 {
		t.Fatal("active")
	}
	if lkg, _, _ := s2.LastKnownGood(); lkg.Id != 1 {
		t.Fatal("last known good")
	}
	list, _ := s2.List(ListOptions{})
	if len(list) != 2 || list[0].Status != StatusCandidate || *list[1].Message != "one" {
		t.Fatalf("list = %+v", list)
	}
	_, got, err := s2.Get(1)
	if err != nil || !domain.Equal(got, cfg) {
		t.Fatalf("the configuration must come back unchanged: %v", err)
	}
}

func TestRevisionFilesAreReadableAndCarryTheSchemaVersion(t *testing.T) {
	s, dir := openStore(t)
	create(t, s, exampleConfig(t), 0, "hello")
	raw, _ := os.ReadFile(filepath.Join(dir, "revisions", "000001.json"))
	text := string(raw)
	for _, want := range []string{`"schema_version": 1`, `"id": 1`, `"message": "hello"`, `"sha256"`, "\n  \"configuration\": {", `"created_by"`} {
		if !strings.Contains(text, want) {
			t.Errorf("revision file lacks %s", want)
		}
	}
	status, _ := os.ReadFile(filepath.Join(dir, "revisions", "000001.status.json"))
	if !strings.Contains(string(status), `"schema_version": 1`) || !strings.Contains(string(status), `"status": "candidate"`) {
		t.Errorf("status file: %s", status)
	}
}

func mustCommit(t *testing.T, s *Store, id int64) {
	t.Helper()
	if _, err := s.Commit(id, t0); err != nil {
		t.Fatalf("Commit(%d): %v", id, err)
	}
}

func mustBegin(t *testing.T, s *Store, id int64) {
	t.Helper()
	if _, err := s.BeginConfirm(id, t0, t0.Add(time.Minute)); err != nil {
		t.Fatalf("BeginConfirm(%d): %v", id, err)
	}
}
