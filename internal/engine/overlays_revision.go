package engine

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/overlay"
)

// This file is what applying a revision means for the overlays that are active (plan §2.1.1): a
// revision that deletes an object an overlay refers to is refused, unless it is applied with force,
// which removes those overlays; a revision that merges a discovered device into a configured one
// moves the overlays of the discovered device to the configured one; a revision that gives a profile
// a part this build cannot resolve (dns, tls) is refused while an overlay activates that profile.
// Overlays are never part of a revision, so all of it is decided here, by the state owner, in the
// same step that starts the apply.

// OverlayReference is an active overlay that a revision would orphan, with the object of the
// configuration that is deleted.
type OverlayReference struct {
	Overlay model.Overlay
	// Object is the JSON pointer of the deleted object in the active configuration.
	Object string
}

// ErrOverlaysOrphaned is returned by Apply when the revision deletes objects that active overlays
// refer to and force is not set; nothing has changed. Preview reports the same references.
type ErrOverlaysOrphaned struct {
	References []OverlayReference
}

func (e *ErrOverlaysOrphaned) Error() string {
	ids := map[uuid.UUID]bool{}
	for _, r := range e.References {
		ids[r.Overlay.Id] = true
	}
	return fmt.Sprintf("the revision deletes objects that %d active overlays refer to: apply with force to remove those overlays", len(ids))
}

// UnsupportedProfileRevisionError is returned by Apply, and reported by Preview, for a revision that
// gives a profile a part of a later milestone (a dns part before M20, a tls part before M21) while an
// overlay activates the profile. checkProfileAvailable refuses the same activation at the write, so no
// activation runs half a profile (P2-M11-05, decided on 2026-10-11); nothing has changed. The revision
// is accepted again once the activation is gone, and the profile is then stored but cannot be activated.
type UnsupportedProfileRevisionError struct {
	// Profile is the name of the profile and Family its part that this build cannot resolve.
	Profile, Family string
	// Milestone is the milestone that brings the family.
	Milestone string
	// Overlays are the activations of the profile.
	Overlays []uuid.UUID
}

func (e *UnsupportedProfileRevisionError) Error() string {
	return fmt.Sprintf("the revision gives the profile %s a %s part, which arrives with milestone %s, while %d active overlays activate the profile: "+
		"an activation never runs half a profile; remove the activations first", e.Profile, strings.ToUpper(e.Family), e.Milestone, len(e.Overlays))
}

// unavailableProfileRevision reports the first profile of next that an overlay activates and that has
// a part this build cannot resolve, nil when there is none. Such an activation cannot be written
// (checkProfileAvailable), so it exists only because a revision changed the profile afterwards.
func unavailableProfileRevision(next *model.Configuration, overlays []model.Overlay) *UnsupportedProfileRevisionError {
	if next == nil {
		return nil
	}
	var out *UnsupportedProfileRevisionError
	for _, ov := range overlays {
		if ov.Kind != model.OverlayKindProfile || ov.Profile == nil {
			continue
		}
		p, _, ok := domain.ProfileByID(next, *ov.Profile)
		if !ok {
			continue
		}
		parts := domain.UnavailableParts(p)
		if len(parts) == 0 {
			continue
		}
		if out == nil {
			out = &UnsupportedProfileRevisionError{Profile: p.Name, Family: parts[0].Family, Milestone: parts[0].Milestone}
		}
		if strings.EqualFold(out.Profile, p.Name) {
			out.Overlays = append(out.Overlays, ov.Id)
		}
	}
	return out
}

// referencesOf turns orphans (one per overlay) into one reference per overlay and deleted object.
func referencesOf(orphans []domain.Orphan) []OverlayReference {
	var out []OverlayReference
	for _, o := range orphans {
		for _, obj := range o.Objects {
			out = append(out, OverlayReference{Overlay: o.Overlay, Object: obj})
		}
	}
	return out
}

// revisionImpact is what applying next in place of current does to the overlays: the ones it
// orphans, and the devices it merges (the UUID of a discovered device that a configured one now
// covers, to the UUID of that configured device). found are the discovered devices as they were
// before the revision.
func revisionImpact(current, next *model.Configuration, overlays []model.Overlay, found []domain.DiscoveredDevice) (orphans []domain.Orphan, merges map[string]string) {
	if current == nil || next == nil || len(overlays) == 0 {
		return nil, nil
	}
	merges = domain.DiscoveredMerges(next, found)
	// the discovered devices that are still discovered after the revision: an overlay may name them
	var still []string
	if len(found) > 0 {
		for _, d := range domain.ResolveIdentity(next, domain.Observed{Discovered: found}, nil).Discovered {
			still = append(still, d.ID)
		}
	}
	return domain.OrphanedOverlays(current, next, overlays, domain.WithDiscovered(still...)), merges
}

// overlaysAfter is the overlay list a revision would be compiled with: without the orphans, and with
// the overlays of merged devices on the configured device. It is for the preview; the store
// does the same with its own rules for a collision (Retarget).
func overlaysAfter(list []model.Overlay, orphans []domain.Orphan, merges map[string]string) []model.Overlay {
	if len(orphans) == 0 && len(merges) == 0 {
		return list
	}
	gone := map[uuid.UUID]bool{}
	for _, o := range orphans {
		gone[o.Overlay.Id] = true
	}
	out := make([]model.Overlay, 0, len(list))
	for _, ov := range list {
		if gone[ov.Id] {
			continue
		}
		if t := ov.Target; t != nil && t.Device != nil {
			if to, ok := merges[strings.ToLower(*t.Device)]; ok {
				cp := *t
				dev := to
				cp.Device = &dev
				ov.Target = &cp
			}
		}
		out = append(out, ov)
	}
	return out
}

// revisionOverlays settles the overlays for a revision that is about to be applied: it refuses
// (ErrOverlaysOrphaned) when there are orphans and no force, otherwise removes the orphans and moves
// the overlays of merged devices. gen is the generation of the desired state the apply makes. The
// changes are returned for the caller to record, and the moves (each overlay with the discovered
// device it was moved from) for a commit-confirm rollback to undo; the store is changed only when
// there is no error.
func (o *owner) revisionOverlays(next *model.Configuration, force bool, gen uint64) (changes []actedChange, moves []overlay.Move, err error) {
	if o.committed == nil || o.ov.store.Len() == 0 {
		return nil, nil, nil
	}
	if err := unavailableProfileRevision(next, o.ov.list); err != nil {
		return nil, nil, err
	}
	var found []domain.DiscoveredDevice
	if o.identity != nil {
		found = o.identity.Discovered
	}
	orphans, merges := revisionImpact(o.committed.Config, next, o.ov.list, found)
	if len(orphans) > 0 && !force {
		return nil, nil, &ErrOverlaysOrphaned{References: referencesOf(orphans)}
	}
	objects := map[uuid.UUID][]string{}
	ids := make([]uuid.UUID, 0, len(orphans))
	for _, or := range orphans {
		ids = append(ids, or.Overlay.Id)
		objects[or.Overlay.Id] = or.Objects
	}
	for _, ch := range o.ov.store.Orphan(ids, "") {
		changes = append(changes, actedChange{Change: ch, objects: objects[ch.Overlay.Id]})
	}
	from := map[uuid.UUID]string{}
	for _, ov := range o.ov.list {
		if ov.Target != nil && ov.Target.Device != nil {
			from[ov.Id] = *ov.Target.Device
		}
	}
	for _, ch := range o.ov.store.Retarget(merges, int64(gen)) {
		changes = append(changes, actedChange{Change: ch})
		if ch.Type == overlay.Updated {
			moves = append(moves, overlay.Move{ID: ch.Overlay.Id, From: from[ch.Overlay.Id], To: *ch.Overlay.Target.Device})
		}
	}
	return changes, moves, nil
}

// rollbackOverlays settles the overlays for a commit-confirm rollback from the pending configuration
// (from) back to the committed one (to), the reverse of what revisionOverlays did for the pending
// revision: the overlays its merge moved go back to the discovered device they were written for (moves
// as revisionOverlays returned them), and the overlays that refer to an object only the pending
// revision has (a device, a network, a group, a custom profile it added, or one an overlay written in
// the confirmation window names) are removed as orphans with the reason "rollback"; so are the
// activations of a profile that the committed configuration gives a part this build cannot resolve
// (P2-M11-05: an activation never runs half a profile). A rollback is not refused and needs no force.
// The overlays a forced revision removed and those a merge replaced stay removed: their removal was
// announced.
//
// A rollback holds whatever the kernel does, unlike a revision's own changes, which a failed apply
// takes back: the same change is made in every checkpoint the store can be taken back to
// (Store.Amend), so a failed apply restores the overlays without the orphans and with the moves undone.
// announced reports, per overlay, whether its earlier state was announced (it is in the last verified
// checkpoint): when the apply fails, settleOverlays still announces these changes, and only those.
func (o *owner) rollbackOverlays(from, to *model.Configuration, moves []overlay.Move, gen uint64) (changes []actedChange, announced map[uuid.UUID]bool) {
	if o.ov.store.Len() == 0 {
		return nil, nil
	}
	back := make([]overlay.Move, len(moves))
	for i, m := range moves {
		back[i] = overlay.Move{ID: m.ID, From: m.To, To: m.From}
	}
	// the discovered devices after the rollback: what the tracker has seen, resolved against the
	// committed configuration (a device the pending revision adopted is discovered again)
	var still []string
	if seen := o.tracker.discovered(); len(seen) > 0 {
		for _, d := range domain.ResolveIdentity(to, domain.Observed{Discovered: seen}, nil).Discovered {
			still = append(still, d.ID)
		}
	}
	settle := func(s *overlay.Store) []actedChange {
		out := acted(s.Move(back, int64(gen)), model.Actor{})
		orphans := domain.OrphanedOverlays(from, to, s.Overlays(), domain.WithDiscovered(still...))
		objects := map[uuid.UUID][]string{}
		ids := make([]uuid.UUID, 0, len(orphans))
		for _, or := range orphans {
			ids = append(ids, or.Overlay.Id)
			objects[or.Overlay.Id] = or.Objects
		}
		for _, ov := range s.Overlays() {
			if ov.Kind != model.OverlayKindProfile || ov.Profile == nil || objects[ov.Id] != nil {
				continue
			}
			if p, _, ok := domain.ProfileByID(to, *ov.Profile); ok && len(domain.UnavailableParts(p)) > 0 {
				ids = append(ids, ov.Id)
			}
		}
		for _, ch := range s.Orphan(ids, overlay.ReasonRollback) {
			out = append(out, actedChange{Change: ch, objects: objects[ch.Overlay.Id]})
		}
		return out
	}
	cps := make([]*overlay.Checkpoint, 0, len(o.ov.marks)+1)
	cps = append(cps, o.ov.verified)
	for _, m := range o.ov.marks {
		cps = append(cps, m.after)
	}
	changes = settle(o.ov.store)
	if len(changes) == 0 {
		return nil, nil
	}
	announced = map[uuid.UUID]bool{}
	for _, ch := range changes {
		announced[ch.Overlay.Id] = o.ov.verified.Has(ch.Overlay.Id)
	}
	o.ov.store.Amend(func(s *overlay.Store) { settle(s) }, cps...)
	actor := model.Actor{Type: "system", Id: "system"}
	for i := range changes {
		changes[i].actor = actor
	}
	return changes, announced
}
