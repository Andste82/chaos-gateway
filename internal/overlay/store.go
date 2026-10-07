package overlay

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// DefaultMax is the number of overlays a store holds when Options.Max is not set. A test run
// creates a handful; the limit only keeps a runaway client from filling the memory.
const DefaultMax = 10000

var (
	// ErrNotFound is returned for an id that is not (or no longer) an active overlay.
	ErrNotFound = errors.New("overlay: not found")
	// ErrNoLease is returned when renewing an overlay that has no lease.
	ErrNoLease = errors.New("overlay: has no lease to renew")
	// ErrFull is returned by Put when the store holds Options.Max overlays and the write would
	// create another one.
	ErrFull = errors.New("overlay: too many active overlays")
)

// ChangeType says what happened to an overlay.
type ChangeType string

const (
	// Created is a new overlay (HTTP 201).
	Created ChangeType = "created"
	// Updated is a replaced overlay that keeps its id (HTTP 200), or one that was moved to
	// another target (Retarget).
	Updated ChangeType = "updated"
	// Removed is an overlay that was deleted, reset or replaced by a merged one.
	Removed ChangeType = "removed"
	// Expired is an overlay whose TTL or lease ran out.
	Expired ChangeType = "expired"
	// Orphaned is an overlay removed because the object it targets no longer exists.
	Orphaned ChangeType = "orphaned"
)

// Reasons of a Change. Updated: ReasonReplaced, ReasonMoved. Removed: ReasonDeleted, ReasonReset,
// ReasonMerged. Expired: ReasonTTL, ReasonLease.
const (
	ReasonReplaced = "replaced"
	ReasonMoved    = "moved"
	ReasonDeleted  = "deleted"
	ReasonReset    = "reset"
	ReasonMerged   = "merged"
	ReasonTTL      = "ttl"
	ReasonLease    = "lease"
)

// Change is one thing that happened to one overlay. Overlay is the state after a Created or
// Updated, and the state it had when it went away for the other types.
type Change struct {
	Type    ChangeType
	Overlay model.Overlay
	Reason  string
}

// Event returns the name of the event of the API (`EventType` in the spec) that announces the
// change.
func (c Change) Event() string {
	switch c.Type {
	case Created:
		return "overlay_created"
	case Updated:
		return "overlay_updated"
	case Expired:
		return "overlay_expired"
	case Orphaned:
		return "overlay_orphaned"
	}
	return "overlay_removed"
}

// Options configures a Store.
type Options struct {
	// Clock is the time source: monotonic for deadlines, wall for the time stamps. Required.
	Clock clock.Clock
	// NewID creates the id of a new overlay. The default is a time-ordered UUID (v7), so that two
	// overlays written in the same instant still have a stable order (domain's tie-break).
	NewID func() uuid.UUID
	// Max limits the number of active overlays; DefaultMax when zero.
	Max int
}

// PutOptions are the parts of a write that do not come from the request.
type PutOptions struct {
	// Generation is the generation the write becomes active in (Overlay.Generation).
	Generation int64
	// Run is set when a run owns the overlay; then the owner is that run.
	Run *model.OverlayRunRef
}

type entry struct {
	ov  model.Overlay
	key string
	// ttl and lease are the intervals of the request, zero when absent; the deadlines are
	// monotonic times and only meaningful when the interval is set.
	ttl, lease     time.Duration
	ttlAt, leaseAt time.Duration
}

// Store holds the active overlays.
type Store struct {
	clk   clock.Clock
	newID func() uuid.UUID
	max   int

	mu    sync.Mutex
	byID  map[uuid.UUID]*entry
	byKey map[string]*entry
	// stamp is the last UpdatedAt handed out. Time stamps strictly increase, so "newer wins"
	// (D26) is a total order even when the wall clock does not move between two writes, or jumps
	// back at an NTP step.
	stamp time.Time
}

// New returns an empty store.
func New(o Options) *Store {
	s := &Store{
		clk: o.Clock, newID: o.NewID, max: o.Max,
		byID: map[uuid.UUID]*entry{}, byKey: map[string]*entry{},
	}
	if s.clk == nil {
		panic("overlay: Options.Clock is required")
	}
	if s.newID == nil {
		s.newID = func() uuid.UUID {
			id, err := uuid.NewV7()
			if err != nil {
				return uuid.New()
			}
			return id
		}
	}
	if s.max <= 0 {
		s.max = DefaultMax
	}
	return s
}

// sameOwner reports whether two owners are the same: the type and the id decide, not the name.
func sameOwner(a, b model.Owner) bool { return a.Type == b.Type && a.Id == b.Id }

// SameOwner reports whether two owners are the same owner.
func SameOwner(a, b model.Owner) bool { return sameOwner(a, b) }

func parseInterval(name string, d *model.Duration) (time.Duration, error) {
	if d == nil {
		return 0, nil
	}
	v, err := time.ParseDuration(*d)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("overlay: %s %q is not a positive duration", name, *d)
	}
	return v, nil
}

// nextStamp returns the wall time stamp of a write: now, or one nanosecond after the last stamp
// when the wall clock has not moved past it.
func (s *Store) nextStamp() time.Time {
	now := s.clk.Now()
	if !now.After(s.stamp) {
		now = s.stamp.Add(time.Nanosecond)
	}
	s.stamp = now
	return now
}

// view returns the overlay as the API shows it: with the expiry times computed from the
// monotonic deadlines, so a wall-clock jump neither shortens nor extends what is displayed.
func (e *entry) view(mono time.Duration, wall time.Time) model.Overlay {
	ov := e.ov
	ov.ExpiresAt, ov.LeaseExpiresAt = nil, nil
	if e.ttl > 0 {
		t := wall.Add(e.ttlAt - mono)
		ov.ExpiresAt = &t
	}
	if e.lease > 0 {
		t := wall.Add(e.leaseAt - mono)
		ov.LeaseExpiresAt = &t
	}
	return ov
}

func (s *Store) viewLocked(e *entry) model.Overlay { return e.view(s.clk.Monotonic(), s.clk.Now()) }

// Put writes an overlay for an owner. The request must have passed domain.ValidateOverlay (its
// references are UUIDs). An overlay with the same key (domain.OverlayKey) is replaced: it keeps
// its id and CreatedAt, takes the new body, and its TTL and lease start over; the change is
// Updated. Otherwise a new overlay is created. The returned change carries the overlay.
func (s *Store) Put(owner model.Owner, req *model.OverlayRequest, opt PutOptions) (Change, error) {
	if opt.Run != nil && (owner.Type != "run" || owner.Id != opt.Run.Id.String()) {
		return Change{}, fmt.Errorf("overlay: an overlay of run %s is owned by that run, not by %s %q", opt.Run.Id, owner.Type, owner.Id)
	}
	key, err := domain.OverlayKey(owner, req)
	if err != nil {
		return Change{}, err
	}
	ttl, err := parseInterval("ttl", req.Ttl)
	if err != nil {
		return Change{}, err
	}
	lease, err := parseInterval("lease", req.Lease)
	if err != nil {
		return Change{}, err
	}
	body := domain.CloneOverlayRequest(req)

	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.byKey[key]
	if old == nil && len(s.byID) >= s.max {
		return Change{}, ErrFull
	}
	var id uuid.UUID
	if old != nil {
		id = old.ov.Id
	} else {
		id = s.newID()
		for s.byID[id] != nil { // a generator that repeats itself must not overwrite an overlay
			id = s.newID()
		}
	}
	stamp := s.nextStamp()
	ov, err := domain.NewOverlay(body, owner, id, stamp)
	if err != nil {
		return Change{}, err
	}
	ov.Generation = opt.Generation
	if opt.Run != nil {
		run := *opt.Run
		ov.Run = &run
	}
	ct, reason := Created, ""
	if old != nil {
		ov.CreatedAt = old.ov.CreatedAt
		ct, reason = Updated, ReasonReplaced
	}
	mono := s.clk.Monotonic()
	e := &entry{ov: ov, key: key, ttl: ttl, lease: lease, ttlAt: mono + ttl, leaseAt: mono + lease}
	s.byID[id] = e
	s.byKey[key] = e
	return Change{Type: ct, Overlay: s.viewLocked(e), Reason: reason}, nil
}

// Get returns an active overlay.
func (s *Store) Get(id uuid.UUID) (model.Overlay, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.byID[id]
	if e == nil {
		return model.Overlay{}, false
	}
	return s.viewLocked(e), true
}

// Len returns the number of active overlays.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

// Filter selects overlays; the zero value selects all.
type Filter struct {
	Kind  model.OverlayKind
	Owner *model.Owner
}

func (f Filter) match(ov *model.Overlay) bool {
	return (f.Kind == "" || ov.Kind == f.Kind) && (f.Owner == nil || sameOwner(*f.Owner, ov.Owner))
}

// List returns the active overlays that match the filter, oldest first (by creation, then id), so
// that paging through them is stable.
func (s *Store) List(f Filter) []model.Overlay {
	s.mu.Lock()
	defer s.mu.Unlock()
	mono, wall := s.clk.Monotonic(), s.clk.Now()
	out := make([]model.Overlay, 0, len(s.byID))
	for _, e := range s.byID {
		if f.match(&e.ov) {
			out = append(out, e.view(mono, wall))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Id.String() < out[j].Id.String()
	})
	return out
}

// Overlays returns all active overlays: the input of the resolution (domain.NewWorld). Ordered
// like List.
func (s *Store) Overlays() []model.Overlay { return s.List(Filter{}) }

// removeLocked drops an entry and returns the change.
func (s *Store) removeLocked(e *entry, t ChangeType, reason string) Change {
	delete(s.byID, e.ov.Id)
	if s.byKey[e.key] == e {
		delete(s.byKey, e.key)
	}
	return Change{Type: t, Overlay: s.viewLocked(e), Reason: reason}
}

// sortedEntries returns entries in a deterministic order: by creation, then id.
func sortedEntries(es []*entry) []*entry {
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i].ov, es[j].ov
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.Id.String() < b.Id.String()
	})
	return es
}

// Delete removes one overlay. The caller decides whether the requester may (a token only removes
// its own, plan §2.15): it can look at Get(id).Owner first.
func (s *Store) Delete(id uuid.UUID) (Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.byID[id]
	if e == nil {
		return Change{}, ErrNotFound
	}
	return s.removeLocked(e, Removed, ReasonDeleted), nil
}

// Reset removes the overlays of one owner, or every overlay when owner is nil (`?owner=all`, which
// needs the full-access scope; the caller checks that).
func (s *Store) Reset(owner *model.Owner) []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	var es []*entry
	for _, e := range s.byID {
		if owner == nil || sameOwner(*owner, e.ov.Owner) {
			es = append(es, e)
		}
	}
	changes := make([]Change, 0, len(es))
	for _, e := range sortedEntries(es) {
		changes = append(changes, s.removeLocked(e, Removed, ReasonReset))
	}
	return changes
}

// Orphan removes overlays whose target no longer exists (plan §2.1.1, a revision applied with
// `force=true`). Ids that are not active are ignored.
func (s *Store) Orphan(ids []uuid.UUID) []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	var es []*entry
	for _, id := range ids {
		if e := s.byID[id]; e != nil {
			es = append(es, e)
		}
	}
	changes := make([]Change, 0, len(es))
	for _, e := range sortedEntries(es) {
		changes = append(changes, s.removeLocked(e, Orphaned, ""))
	}
	return changes
}

// Renew restarts the lease of an overlay: it runs out one lease interval after now. It changes
// nothing in the kernel and is no replacement, so the overlay keeps its UpdatedAt and generation
// and the change produces no event.
func (s *Store) Renew(id uuid.UUID) (model.Overlay, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.byID[id]
	if e == nil {
		return model.Overlay{}, ErrNotFound
	}
	if e.lease <= 0 {
		return model.Overlay{}, ErrNoLease
	}
	e.leaseAt = s.clk.Monotonic() + e.lease
	return s.viewLocked(e), nil
}

// deadline returns the monotonic time at which the entry runs out and why, or false when it has
// no TTL and no lease.
func (e *entry) deadline() (at time.Duration, reason string, ok bool) {
	switch {
	case e.ttl > 0 && e.lease > 0:
		if e.leaseAt < e.ttlAt {
			return e.leaseAt, ReasonLease, true
		}
		return e.ttlAt, ReasonTTL, true
	case e.ttl > 0:
		return e.ttlAt, ReasonTTL, true
	case e.lease > 0:
		return e.leaseAt, ReasonLease, true
	}
	return 0, "", false
}

// NextDeadline returns the monotonic time (as clock.Clock.Monotonic reports it) at which the
// next overlay runs out, if any does.
func (s *Store) NextDeadline() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next time.Duration
	found := false
	for _, e := range s.byID {
		if at, _, ok := e.deadline(); ok && (!found || at < next) {
			next, found = at, true
		}
	}
	return next, found
}

// Expire removes the overlays whose TTL or lease has run out and returns the changes, the
// earliest deadline first. It is the only place where time removes an overlay.
func (s *Store) Expire() []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	mono := s.clk.Monotonic()
	type due struct {
		e      *entry
		at     time.Duration
		reason string
	}
	var ds []due
	for _, e := range s.byID {
		if at, reason, ok := e.deadline(); ok && at <= mono {
			ds = append(ds, due{e, at, reason})
		}
	}
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].at != ds[j].at {
			return ds[i].at < ds[j].at
		}
		return ds[i].e.ov.Id.String() < ds[j].e.ov.Id.String()
	})
	changes := make([]Change, 0, len(ds))
	for _, d := range ds {
		changes = append(changes, s.removeLocked(d.e, Expired, d.reason))
	}
	return changes
}

// Retarget moves overlays from one device to another: moves maps the UUID of a device an overlay
// targets to the UUID it targets from now on. A merge revision does this for the discovered
// device whose identifiers a configured device now covers (plan §2.3). A moved overlay keeps its
// id, its UpdatedAt and its deadlines and becomes active in generation gen (change Updated).
//
// A move can give an overlay the key of one the new device already has from the same owner. Only
// one of the two can stay: the one that was written last (the newer one wins, as everywhere);
// the other one is removed (change Removed, reason "merged").
func (s *Store) Retarget(moves map[string]string, gen int64) []Change {
	if len(moves) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var es []*entry
	for _, e := range s.byID {
		if t := e.ov.Target; t != nil && t.Device != nil {
			if _, ok := moves[strings.ToLower(*t.Device)]; ok {
				es = append(es, e)
			}
		}
	}
	var changes []Change
	for _, e := range sortedEntries(es) {
		if s.byID[e.ov.Id] != e {
			continue // removed as the loser of an earlier move
		}
		to := moves[strings.ToLower(*e.ov.Target.Device)]
		req := domain.CloneOverlayRequest(ptr(domain.RequestOf(&e.ov)))
		dev := to
		req.Target = &model.Scope{Device: &dev}
		key, err := domain.OverlayKey(e.ov.Owner, &req)
		if err != nil {
			continue // cannot happen for an overlay the store accepted
		}
		if other := s.byKey[key]; other != nil && other != e {
			if newerEntry(other, e) {
				changes = append(changes, s.removeLocked(e, Removed, ReasonMerged))
				continue
			}
			changes = append(changes, s.removeLocked(other, Removed, ReasonMerged))
		}
		if s.byKey[e.key] == e {
			delete(s.byKey, e.key)
		}
		e.key = key
		s.byKey[key] = e
		e.ov.Target = req.Target
		e.ov.Generation = gen
		changes = append(changes, Change{Type: Updated, Overlay: s.viewLocked(e), Reason: ReasonMoved})
	}
	return changes
}

func ptr[T any](v T) *T { return &v }

// newerEntry reports whether a was written after b; at the same instant the higher id counts as
// newer, as in the resolution.
func newerEntry(a, b *entry) bool {
	if !a.ov.UpdatedAt.Equal(b.ov.UpdatedAt) {
		return a.ov.UpdatedAt.After(b.ov.UpdatedAt)
	}
	return a.ov.Id.String() > b.ov.Id.String()
}
