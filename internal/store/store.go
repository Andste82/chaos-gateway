package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// SchemaVersion is the version of the files this build writes.
const SchemaVersion = 1

// DefaultRetainedRevisions is the retention the plan's §3.6 default applies when
// `settings.retention.revisions` is not set.
const DefaultRetainedRevisions = 200

// Revision statuses (the spec's RevisionStatus).
const (
	StatusCandidate      = "candidate"
	StatusPendingConfirm = "pending_confirm"
	StatusActive         = "active"
	StatusSuperseded     = "superseded"
	StatusRolledBack     = "rolled_back"
)

// state is config.json: the pointer to the active revision and what the store must remember.
type state struct {
	SchemaVersion int `json:"schema_version"`
	// Active is the revision that boots; 0 before the first one. While a revision waits for
	// confirmation it stays the previous one: a reboot inside the window boots it.
	Active        int64 `json:"active"`
	LastKnownGood int64 `json:"last_known_good,omitempty"`
	// Pending is the revision waiting for confirmation.
	Pending *pending `json:"pending_confirm,omitempty"`
	// NextID is the next revision id; ids are never reused, not even after a discard.
	NextID int64 `json:"next_id"`
}

type pending struct {
	Revision int64     `json:"revision"`
	Previous int64     `json:"previous"`
	Since    time.Time `json:"since"`
	Deadline time.Time `json:"deadline"`
}

// revisionFile is the immutable file of a revision.
type revisionFile struct {
	SchemaVersion int             `json:"schema_version"`
	ID            int64           `json:"id"`
	Base          int64           `json:"base,omitempty"`
	Message       string          `json:"message,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	CreatedBy     model.Actor     `json:"created_by"`
	SHA256        string          `json:"sha256"`
	Configuration json.RawMessage `json:"configuration"`
}

// statusFile is the mutable part of a revision.
type statusFile struct {
	SchemaVersion int        `json:"schema_version"`
	Status        string     `json:"status"`
	AppliedAt     *time.Time `json:"applied_at,omitempty"`
	ConfirmedAt   *time.Time `json:"confirmed_at,omitempty"`
}

// Store is the persistence of the configuration. It is safe for concurrent use inside one
// process; one Chaos Gateway API process owns a configuration directory.
type Store struct {
	dir  string
	mu   sync.Mutex
	st   state
	mig  migrations
	lock *os.File // holds the lock on the directory
}

// Open opens the store in dir and creates it when it is new. It removes the temporary files of
// interrupted writes, migrates files of an older schema (keeping a backup) and refuses files of
// a newer schema (ErrNewerSchema). It repairs the next id when a crash left a revision file
// behind that the pointer does not know yet.
func Open(dir string) (*Store, error) { return open(dir, defaultMigrations) }

func open(dir string, mig migrations) (s *Store, err error) {
	revs := filepath.Join(dir, "revisions")
	if err := os.MkdirAll(revs, 0o750); err != nil {
		return nil, err
	}
	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			unlockDir(lock)
		}
	}()
	for _, d := range []string{dir, revs} {
		if err := removeTempFiles(d); err != nil {
			return nil, err
		}
	}
	s = &Store{dir: dir, mig: mig, lock: lock}
	if err := s.loadState(); err != nil {
		return nil, err
	}
	if err := s.migrateAll(); err != nil {
		return nil, err
	}
	if err := s.reconcile(); err != nil {
		return nil, err
	}
	ids, err := s.revisionIDs()
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 && ids[len(ids)-1] >= s.st.NextID {
		s.st.NextID = ids[len(ids)-1] + 1
		if err := s.saveState(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) statePath() string { return filepath.Join(s.dir, "config.json") }
func (s *Store) revPath(id int64) string {
	return filepath.Join(s.dir, "revisions", revisionName(id)+".json")
}
func (s *Store) statusPath(id int64) string {
	return filepath.Join(s.dir, "revisions", revisionName(id)+".status.json")
}

func (s *Store) loadState() error {
	raw, err := os.ReadFile(s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		// a new store, or a pointer that was lost: rebuild what the revisions still say
		s.st = s.rebuildState()
		return s.saveState()
	}
	if err != nil {
		return err
	}
	raw, err = s.mig.upgrade("state", s.statePath(), raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &s.st); err != nil {
		return &ErrCorrupt{s.statePath(), err.Error()}
	}
	if s.st.NextID < 1 {
		return &ErrCorrupt{s.statePath(), "next_id must be at least 1"}
	}
	return nil
}

func (s *Store) saveState() error {
	s.st.SchemaVersion = SchemaVersion
	raw, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.statePath(), append(raw, '\n'), 0o640)
}

// revisionIDs lists the ids that have a revision file, ascending.
func (s *Store) revisionIDs() ([]int64, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "revisions"))
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".status.json") {
			continue
		}
		if id, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// ---- reading ----------------------------------------------------------------------------

func (s *Store) readRevisionFile(id int64) (revisionFile, error) {
	path := s.revPath(id)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return revisionFile{}, ErrNotFound
	}
	if err != nil {
		return revisionFile{}, err
	}
	raw, err = s.mig.upgrade("revision", path, raw)
	if err != nil {
		return revisionFile{}, err
	}
	var f revisionFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return revisionFile{}, &ErrCorrupt{path, err.Error()}
	}
	if f.ID != id {
		return revisionFile{}, &ErrCorrupt{path, fmt.Sprintf("the file says revision %d", f.ID)}
	}
	sum := sha256.Sum256(f.Configuration)
	if hex.EncodeToString(sum[:]) != f.SHA256 {
		return revisionFile{}, &ErrCorrupt{path, "the checksum of the configuration does not match"}
	}
	return f, nil
}

func (s *Store) readStatus(id int64) (statusFile, error) {
	path := s.statusPath(id)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return statusFile{}, &ErrCorrupt{path, "the status file is missing"}
	}
	if err != nil {
		return statusFile{}, err
	}
	raw, err = s.mig.upgrade("status", path, raw)
	if err != nil {
		return statusFile{}, err
	}
	var st statusFile
	if err := json.Unmarshal(raw, &st); err != nil {
		return statusFile{}, &ErrCorrupt{path, err.Error()}
	}
	return st, nil
}

func (s *Store) writeStatus(id int64, st statusFile) error {
	st.SchemaVersion = SchemaVersion
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.statusPath(id), append(raw, '\n'), 0o640)
}

func (s *Store) revisionOf(f revisionFile, st statusFile) model.Revision {
	r := model.Revision{
		Id: f.ID, Status: model.RevisionStatus(st.Status), SchemaVersion: f.SchemaVersion,
		CreatedAt: f.CreatedAt, CreatedBy: f.CreatedBy, AppliedAt: st.AppliedAt, ConfirmedAt: st.ConfirmedAt,
	}
	if f.Base != 0 {
		b := f.Base
		r.Base = &b
	}
	if f.Message != "" {
		m := f.Message
		r.Message = &m
	}
	if s.st.LastKnownGood == f.ID {
		t := true
		r.LastKnownGood = &t
	}
	return r
}

// Get returns a revision with its configuration.
func (s *Store) Get(id int64) (model.Revision, *model.Configuration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(id)
}

func (s *Store) get(id int64) (model.Revision, *model.Configuration, error) {
	f, err := s.readRevisionFile(id)
	if err != nil {
		return model.Revision{}, nil, err
	}
	st, err := s.readStatus(id)
	if err != nil {
		return model.Revision{}, nil, err
	}
	var cfg model.Configuration
	dec := json.NewDecoder(bytes.NewReader(f.Configuration))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return model.Revision{}, nil, &ErrCorrupt{s.revPath(id), "the configuration does not decode: " + err.Error()}
	}
	if !cfg.SchemaVersion.Valid() {
		return model.Revision{}, nil, &ErrCorrupt{s.revPath(id), fmt.Sprintf("unknown configuration schema version %d", cfg.SchemaVersion)}
	}
	return s.revisionOf(f, st), &cfg, nil
}

// Active returns the revision that boots, or ErrNotFound before the first one.
func (s *Store) Active() (model.Revision, *model.Configuration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Active == 0 {
		return model.Revision{}, nil, ErrNotFound
	}
	return s.get(s.st.Active)
}

// LastKnownGood returns the last revision that was applied and confirmed.
func (s *Store) LastKnownGood() (model.Revision, *model.Configuration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.LastKnownGood == 0 {
		return model.Revision{}, nil, ErrNotFound
	}
	return s.get(s.st.LastKnownGood)
}

// ActiveID returns the id of the active revision, 0 before the first one.
func (s *Store) ActiveID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Active
}

// Pending describes the revision that waits for confirmation.
type Pending struct {
	Revision, Previous int64
	Deadline           time.Time
}

// PendingConfirm returns the revision waiting for confirmation, if any.
func (s *Store) PendingConfirm() (Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Pending == nil {
		return Pending{}, false
	}
	p := s.st.Pending
	return Pending{p.Revision, p.Previous, p.Deadline}, true
}

// ListOptions select revisions.
type ListOptions struct {
	// Status filters by status when not empty.
	Status string
	// Before returns only revisions with an id below it (pagination); 0 means from the newest.
	Before int64
	// Limit caps the result; 0 means no cap.
	Limit int
}

// List returns revisions, newest first. A revision that is corrupt is skipped here and found by
// Verify.
func (s *Store) List(o ListOptions) ([]model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.revisionIDs()
	if err != nil {
		return nil, err
	}
	var out []model.Revision
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		if o.Before > 0 && id >= o.Before {
			continue
		}
		f, err := s.readRevisionFile(id)
		if err != nil {
			var c *ErrCorrupt
			if errors.As(err, &c) {
				continue
			}
			return nil, err
		}
		st, err := s.readStatus(id)
		if err != nil {
			var c *ErrCorrupt
			if errors.As(err, &c) {
				continue
			}
			return nil, err
		}
		if o.Status != "" && st.Status != o.Status {
			continue
		}
		out = append(out, s.revisionOf(f, st))
		if o.Limit > 0 && len(out) == o.Limit {
			break
		}
	}
	return out, nil
}

// ---- writing ----------------------------------------------------------------------------

// CreateOptions describe a new candidate.
type CreateOptions struct {
	// IfMatch is the revision the caller based the change on: it must be the active one
	// (0 before the first revision), else ErrRevisionConflict.
	IfMatch int64
	Message string
	By      model.Actor
	Now     time.Time
}

// Create stores a configuration as a new candidate revision. The configuration is validated
// again: an invalid one is never stored (domain.ValidationErrors). Secrets are not stored.
func (s *Store) Create(cfg *model.Configuration, o CreateOptions) (model.Revision, error) {
	if cfg.Secrets != nil {
		return model.Revision{}, ErrSecretsPresent
	}
	// stored configurations contain UUIDs only: a rename must not change what a revision means
	stored, errs := domain.Normalize(cfg)
	errs = append(errs, domain.Validate(stored)...)
	if len(errs) > 0 {
		return model.Revision{}, domain.ValidationErrors(errs)
	}
	raw, err := json.MarshalIndent(stored, "  ", "  ")
	if err != nil {
		return model.Revision{}, err
	}
	raw = bytes.TrimSpace(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	if o.IfMatch != s.st.Active {
		return model.Revision{}, &ErrRevisionConflict{Active: s.st.Active}
	}
	id := s.st.NextID
	sum := sha256.Sum256(raw)
	f := revisionFile{
		SchemaVersion: SchemaVersion, ID: id, Base: o.IfMatch, Message: o.Message,
		CreatedAt: o.Now.UTC(), CreatedBy: o.By, SHA256: hex.EncodeToString(sum[:]), Configuration: raw,
	}
	file, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return model.Revision{}, err
	}
	// the status first: a revision file without status would be a corrupt revision
	if err := s.writeStatus(id, statusFile{Status: StatusCandidate}); err != nil {
		return model.Revision{}, err
	}
	if err := writeFileAtomic(s.revPath(id), append(file, '\n'), 0o640); err != nil {
		_ = os.Remove(s.statusPath(id))
		return model.Revision{}, err
	}
	s.st.NextID = id + 1
	if err := s.saveState(); err != nil {
		return model.Revision{}, err
	}
	return s.revisionOf(f, statusFile{Status: StatusCandidate}), nil
}

// candidate loads a revision that must be a candidate based on the active revision.
func (s *Store) candidate(id int64) (revisionFile, statusFile, error) {
	f, err := s.readRevisionFile(id)
	if err != nil {
		return f, statusFile{}, err
	}
	st, err := s.readStatus(id)
	if err != nil {
		return f, st, err
	}
	if st.Status != StatusCandidate {
		return f, st, &ErrNotACandidate{ID: id, Status: st.Status, Want: StatusCandidate}
	}
	if f.Base != s.st.Active {
		return f, st, &ErrRevisionConflict{Active: s.st.Active}
	}
	return f, st, nil
}

// Commit makes a candidate the active revision and the last known good one. Call it once the
// change has been applied and verified. The previous active revision becomes superseded.
func (s *Store) Commit(id int64, now time.Time) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Pending != nil {
		return model.Revision{}, &ErrConfirmPending{Pending: s.st.Pending.Revision}
	}
	f, st, err := s.candidate(id)
	if err != nil {
		return model.Revision{}, err
	}
	t := now.UTC()
	st.Status, st.AppliedAt, st.ConfirmedAt = StatusActive, &t, &t
	if err := s.activate(id, f, st, true); err != nil {
		return model.Revision{}, err
	}
	return s.revisionOf(f, st), nil
}

// activate stores the new status and moves the pointers; the previous active revision is
// superseded. The pointer moves last, so a crash in between leaves the old revision active.
func (s *Store) activate(id int64, f revisionFile, st statusFile, lastKnownGood bool) error {
	if err := s.writeStatus(id, st); err != nil {
		return err
	}
	prev := s.st.Active
	s.st.Active = id
	if lastKnownGood {
		s.st.LastKnownGood = id
	}
	s.st.Pending = nil
	if err := s.saveState(); err != nil {
		return err
	}
	if prev != 0 && prev != id {
		if old, err := s.readStatus(prev); err == nil && old.Status == StatusActive {
			old.Status = StatusSuperseded
			return s.writeStatus(prev, old)
		}
	}
	return nil
}

// BeginConfirm marks a candidate as applied but waiting for confirmation (commit-confirm, plan
// §2.14). The active revision stays the previous one until Confirm: a reboot inside the window
// boots it. Only one revision can wait at a time.
func (s *Store) BeginConfirm(id int64, now, deadline time.Time) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Pending != nil {
		return model.Revision{}, &ErrConfirmPending{Pending: s.st.Pending.Revision}
	}
	f, st, err := s.candidate(id)
	if err != nil {
		return model.Revision{}, err
	}
	t := now.UTC()
	st.Status, st.AppliedAt = StatusPendingConfirm, &t
	if err := s.writeStatus(id, st); err != nil {
		return model.Revision{}, err
	}
	s.st.Pending = &pending{Revision: id, Previous: s.st.Active, Since: t, Deadline: deadline.UTC()}
	if err := s.saveState(); err != nil {
		return model.Revision{}, err
	}
	return s.revisionOf(f, st), nil
}

func (s *Store) pendingRevision(id int64) (revisionFile, statusFile, error) {
	f, err := s.readRevisionFile(id)
	if err != nil {
		return f, statusFile{}, err
	}
	st, err := s.readStatus(id)
	if err != nil {
		return f, st, err
	}
	if st.Status != StatusPendingConfirm || s.st.Pending == nil || s.st.Pending.Revision != id {
		return f, st, &ErrNotACandidate{ID: id, Status: st.Status, Want: StatusPendingConfirm}
	}
	return f, st, nil
}

// Confirm makes the revision that waits for confirmation active and the last known good one.
func (s *Store) Confirm(id int64, now time.Time) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, st, err := s.pendingRevision(id)
	if err != nil {
		return model.Revision{}, err
	}
	if now.After(s.st.Pending.Deadline) {
		return model.Revision{}, &ErrConfirmExpired{Revision: id, Deadline: s.st.Pending.Deadline}
	}
	t := now.UTC()
	st.Status, st.ConfirmedAt = StatusActive, &t
	if err := s.activate(id, f, st, true); err != nil {
		return model.Revision{}, err
	}
	return s.revisionOf(f, st), nil
}

// Rollback ends the waiting for confirmation without confirming: the revision is rolled back
// and the previous one stays active. It is what the timeout and "roll back now" do.
func (s *Store) Rollback(id int64, now time.Time) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, st, err := s.pendingRevision(id)
	if err != nil {
		return model.Revision{}, err
	}
	st.Status = StatusRolledBack
	if err := s.writeStatus(id, st); err != nil {
		return model.Revision{}, err
	}
	s.st.Pending = nil
	if err := s.saveState(); err != nil {
		return model.Revision{}, err
	}
	return s.revisionOf(f, st), nil
}

// Discard deletes a candidate. Its id is not reused.
func (s *Store) Discard(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readStatus(id)
	if err != nil {
		var c *ErrCorrupt
		if _, ferr := os.Stat(s.revPath(id)); errors.Is(ferr, os.ErrNotExist) {
			return ErrNotFound
		} else if !errors.As(err, &c) {
			return err
		}
	} else if st.Status != StatusCandidate {
		return &ErrNotACandidate{ID: id, Status: st.Status, Want: StatusCandidate}
	}
	for _, p := range []string{s.revPath(id), s.statusPath(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDir(filepath.Join(s.dir, "revisions"))
}

// Prune deletes the oldest revisions beyond the newest `keep`. It never deletes the active, the
// last known good, the pending or a candidate revision. It returns the deleted ids.
func (s *Store) Prune(keep int) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keep < 0 {
		keep = 0
	}
	ids, err := s.revisionIDs()
	if err != nil {
		return nil, err
	}
	var removed []int64
	for i, id := range ids {
		if id == s.st.Active || id == s.st.LastKnownGood || (s.st.Pending != nil && s.st.Pending.Revision == id) {
			continue
		}
		st, err := s.readStatus(id)
		if err == nil && st.Status == StatusCandidate {
			// A candidate based on a revision that is not active any more can never be
			// committed (revision_conflict): it is stale and goes, whatever `keep` says. A
			// current one stays until it is applied or discarded.
			f, ferr := s.readRevisionFile(id)
			if ferr != nil || f.Base == s.st.Active {
				continue
			}
		} else if i >= len(ids)-keep {
			continue
		}
		for _, p := range []string{s.revPath(id), s.statusPath(id)} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
		}
		removed = append(removed, id)
	}
	if len(removed) > 0 {
		return removed, syncDir(filepath.Join(s.dir, "revisions"))
	}
	return nil, nil
}

// Corrupt is a file that failed verification.
type Corrupt struct {
	Revision int64
	Err      error
}

// Verify reads every revision and reports the ones that cannot be trusted. Recovery
// (last known good, safe mode) is M27; this finds the problem.
func (s *Store) Verify() ([]Corrupt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.revisionIDs()
	if err != nil {
		return nil, err
	}
	var bad []Corrupt
	for _, id := range ids {
		if _, _, err := s.get(id); err != nil {
			var c *ErrCorrupt
			if errors.As(err, &c) {
				bad = append(bad, Corrupt{id, err})
				continue
			}
			return nil, err
		}
	}
	// the pointers must name revisions that exist and are in the right state
	check := func(id int64, what, want string) {
		if id == 0 {
			return
		}
		st, err := s.readStatus(id)
		switch {
		case err != nil:
			bad = append(bad, Corrupt{id, &ErrCorrupt{s.statePath(), what + " points to revision " + strconv.FormatInt(id, 10) + ", which cannot be read"}})
		case want != "" && st.Status != want:
			bad = append(bad, Corrupt{id, &ErrCorrupt{s.statePath(), what + " points to revision " + strconv.FormatInt(id, 10) + ", which is " + st.Status}})
		}
	}
	check(s.st.Active, "active", StatusActive)
	check(s.st.LastKnownGood, "last_known_good", "")
	if s.st.Pending != nil {
		check(s.st.Pending.Revision, "pending_confirm", StatusPendingConfirm)
	}
	return bad, nil
}

// Diff returns the domain diff between two stored revisions.
func (s *Store) Diff(fromID, toID int64) ([]model.DomainChange, error) {
	_, a, err := s.Get(fromID)
	if err != nil {
		return nil, err
	}
	_, b, err := s.Get(toID)
	if err != nil {
		return nil, err
	}
	return domain.Diff(a, b), nil
}
