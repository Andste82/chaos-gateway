package store

import "errors"

// This file repairs what a crash can leave behind. A commit, a confirm and a rollback write the
// status file of the revision and then the pointer in config.json: two files, so a crash in
// between leaves them disagreeing. The pointer is the truth. Open reconciles the statuses with
// it, so nothing stays blocked and nothing is trusted that was not committed.

// rebuildState derives a pointer from the revision files when config.json is missing: the
// newest confirmed active revision is the active one and the last known good, and ids continue
// after the highest one. It is the recovery for a lost pointer, not a normal path.
func (s *Store) rebuildState() state {
	st := state{SchemaVersion: SchemaVersion, NextID: 1}
	ids, err := s.revisionIDs()
	if err != nil || len(ids) == 0 {
		return st
	}
	st.NextID = ids[len(ids)-1] + 1
	for _, id := range ids {
		if status, err := s.readStatus(id); err == nil && status.Status == StatusActive && status.ConfirmedAt != nil {
			st.Active, st.LastKnownGood = id, id
		}
	}
	return st
}

// reconcile brings the statuses in line with the pointer.
func (s *Store) reconcile() error {
	ids, err := s.revisionIDs()
	if err != nil {
		return err
	}
	statuses := map[int64]statusFile{}
	for _, id := range ids {
		if st, err := s.readStatus(id); err == nil {
			statuses[id] = st
		} else {
			var c *ErrCorrupt
			if !errors.As(err, &c) {
				return err
			}
		}
	}
	set := func(id int64, status string) error {
		st := statuses[id]
		st.Status = status
		if status == StatusCandidate {
			st.AppliedAt, st.ConfirmedAt = nil, nil
		}
		statuses[id] = st
		return s.writeStatus(id, st)
	}

	changed := false
	// A revision that waited for confirmation when the process ended was never confirmed: after a
	// restart the kernel state is recompiled from the active revision (plan §2.1.1), so the
	// pending one did not survive. It never becomes the last known good.
	if p := s.st.Pending; p != nil {
		if st, ok := statuses[p.Revision]; ok && st.Status != StatusRolledBack && p.Revision != s.st.Active {
			if err := set(p.Revision, StatusRolledBack); err != nil {
				return err
			}
		}
		s.st.Pending = nil
		changed = true
	}
	for _, id := range ids {
		st, ok := statuses[id]
		if !ok {
			continue
		}
		switch {
		case st.Status == StatusActive && id != s.st.Active && id < s.st.Active:
			// the pointer moved to a newer revision before the old one was marked
			if err := set(id, StatusSuperseded); err != nil {
				return err
			}
		case st.Status == StatusActive && id != s.st.Active:
			// the status was written but the pointer never moved: the commit did not happen
			if err := set(id, StatusCandidate); err != nil {
				return err
			}
		case st.Status == StatusPendingConfirm:
			// BeginConfirm wrote the status and the process ended before the pointer
			if err := set(id, StatusCandidate); err != nil {
				return err
			}
		}
	}
	if st, ok := statuses[s.st.Active]; s.st.Active != 0 && ok && st.Status != StatusActive {
		if err := set(s.st.Active, StatusActive); err != nil {
			return err
		}
	}
	if changed {
		return s.saveState()
	}
	return nil
}
