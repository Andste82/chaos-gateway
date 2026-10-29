package store

import (
	"errors"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the store's part of the recovery at start (plan §3.9, M27): when the active revision cannot be applied, the
// gateway applies the last revision that worked, and when that fails too it runs in safe mode. The store finds that revision
// and moves the pointers once it has been applied.

// Fallback returns the revision to boot when the active one cannot be read or applied: the last known good revision when it
// is another revision than the active one, else the newest revision that was confirmed and then replaced by a newer one
// (superseded). It returns ErrNotFound when there is none. A revision that is corrupt is skipped. A revision that was never
// confirmed, that was rolled back, that failed at start (MarkBootFailed) or that is the active one is never returned.
func (s *Store) Fallback() (model.Revision, *model.Configuration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := s.st.LastKnownGood; id != 0 && id != s.st.Active {
		if st, err := s.readStatus(id); err == nil && st.BootFailedAt == nil {
			if r, c, err := s.get(id); err == nil {
				return r, c, nil
			}
		}
	}
	ids, err := s.revisionIDs()
	if err != nil {
		return model.Revision{}, nil, err
	}
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		if id == s.st.Active || id == s.st.LastKnownGood {
			continue
		}
		st, err := s.readStatus(id)
		if err != nil || st.Status != StatusSuperseded || st.ConfirmedAt == nil || st.BootFailedAt != nil {
			continue
		}
		if r, c, err := s.get(id); err == nil {
			return r, c, nil
		}
	}
	return model.Revision{}, nil, ErrNotFound
}

// Restore makes a revision that was active before (superseded, or the last known good one) the active revision again, after
// the active one could not be applied at start and this one could: the revision it replaces becomes rolled_back, this one
// active and the last known good. The pointer moves last, so a crash in between leaves the old pointer, which the next
// start repairs (reconcile) and recovers again.
func (s *Store) Restore(id int64, now time.Time) (model.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Pending != nil {
		return model.Revision{}, &ErrConfirmPending{Pending: s.st.Pending.Revision}
	}
	f, err := s.readRevisionFile(id)
	if err != nil {
		return model.Revision{}, err
	}
	st, err := s.readStatus(id)
	if err != nil {
		return model.Revision{}, err
	}
	if id == s.st.Active || st.ConfirmedAt == nil || st.Status != StatusSuperseded {
		return model.Revision{}, &ErrNotACandidate{ID: id, Status: st.Status, Want: StatusSuperseded}
	}
	t := now.UTC()
	st.Status, st.AppliedAt, st.BootFailedAt = StatusActive, &t, nil
	if err := s.writeStatus(id, st); err != nil {
		return model.Revision{}, err
	}
	failed := s.st.Active
	if failed != 0 {
		// the status file of the revision that failed may be the broken part: it is written new
		old, err := s.readStatus(failed)
		var corrupt *ErrCorrupt
		if err != nil && !errors.As(err, &corrupt) {
			return model.Revision{}, err
		}
		old.Status = StatusRolledBack
		if err := s.writeStatus(failed, old); err != nil {
			return model.Revision{}, err
		}
	}
	s.st.Active, s.st.LastKnownGood = id, id
	if err := s.saveState(); err != nil {
		return model.Revision{}, err
	}
	return s.revisionOf(f, st), nil
}

// MarkBootFailed records that a revision could not be applied at start and the gateway ended in safe mode, so that the
// fallback of a later start does not return a revision that is known not to apply (the administrator's way out of safe mode
// is a newer revision, which supersedes the failed one with its confirmation still set). The revision keeps its status and
// the pointers stay; MarkGood and Restore clear the mark.
func (s *Store) MarkBootFailed(id int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readStatus(id)
	if err != nil {
		return err
	}
	if st.BootFailedAt != nil {
		return nil
	}
	t := now.UTC()
	st.BootFailedAt = &t
	return s.writeStatus(id, st)
}

// MarkGood makes the active revision the last known good one: it was applied and verified at start. It is a no-op when it
// already is, apart from clearing the mark of MarkBootFailed.
func (s *Store) MarkGood(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.st.Active {
		return &ErrNotACandidate{ID: id, Status: "not the active revision", Want: StatusActive}
	}
	if st, err := s.readStatus(id); err == nil && st.BootFailedAt != nil {
		st.BootFailedAt = nil
		if err := s.writeStatus(id, st); err != nil {
			return err
		}
	}
	if s.st.LastKnownGood == id {
		return nil
	}
	s.st.LastKnownGood = id
	return s.saveState()
}
