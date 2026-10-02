package store

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotFound means there is no such revision.
var ErrNotFound = errors.New("store: revision not found")

// ErrRevisionConflict means another revision became active since the caller read the
// configuration (`revision_conflict`, plan §2.1.1): reload and apply the change again.
type ErrRevisionConflict struct{ Active int64 }

func (e *ErrRevisionConflict) Error() string {
	return fmt.Sprintf("store: revision conflict: the active revision is %d", e.Active)
}

// ErrConfirmPending means another revision waits for confirmation (`confirm_pending`): only one
// can at a time.
type ErrConfirmPending struct{ Pending int64 }

func (e *ErrConfirmPending) Error() string {
	return fmt.Sprintf("store: revision %d is waiting for confirmation", e.Pending)
}

// ErrNotACandidate means the revision is not in the state the operation needs
// (`not_a_candidate`).
type ErrNotACandidate struct {
	ID     int64
	Status string
	Want   string
}

func (e *ErrNotACandidate) Error() string {
	return fmt.Sprintf("store: revision %d is %s, not %s", e.ID, e.Status, e.Want)
}

// ErrCorrupt means a file of the store cannot be trusted: it does not parse or its checksum does
// not match. The store never returns the content of such a file.
type ErrCorrupt struct {
	Path   string
	Reason string
}

func (e *ErrCorrupt) Error() string { return fmt.Sprintf("store: %s is corrupt: %s", e.Path, e.Reason) }

// ErrNewerSchema means a file was written by a newer version of Chaos Gateway. The binary
// refuses to start on it and says which version is needed (plan §3.9, downgrade).
type ErrNewerSchema struct {
	Path      string
	Found     int
	Supported int
}

func (e *ErrNewerSchema) Error() string {
	return fmt.Sprintf("store: %s has schema version %d, this version supports up to %d: install a newer Chaos Gateway", e.Path, e.Found, e.Supported)
}

// ErrLocked means another process has the store open.
var ErrLocked = errors.New("store: the configuration directory is in use by another process")

// ErrConfirmExpired means the confirmation arrived after the deadline. The caller rolls the
// revision back (the timer is the caller's, on the monotonic clock; this is the store's check).
type ErrConfirmExpired struct {
	Revision int64
	Deadline time.Time
}

func (e *ErrConfirmExpired) Error() string {
	return fmt.Sprintf("store: revision %d was not confirmed before %s", e.Revision, e.Deadline.Format(time.RFC3339))
}

// ErrSecretsPresent means the configuration still carries secrets. They belong into the
// secrets store and must be moved there before the revision is created: a revision file never
// holds a private key, and dropping them silently would lose them.
var ErrSecretsPresent = errors.New("store: the configuration contains secrets; move them to the secrets store first")
