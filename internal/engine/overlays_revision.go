package engine

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is what applying a revision means for the overlays that are active (plan §2.1.1): a
// revision that deletes an object an overlay refers to is refused, unless it is applied with force,
// which removes those overlays; a revision that merges a discovered device into a configured one
// moves the overlays of the discovered device to the configured one. Overlays are never part of a
// revision, so both are decided here, by the state owner, in the same step that starts the apply.

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
// changes are returned for the caller to record; the store is changed only when there is no error.
func (o *owner) revisionOverlays(next *model.Configuration, force bool, gen uint64) (changes []actedChange, err error) {
	if o.committed == nil || o.ov.store.Len() == 0 {
		return nil, nil
	}
	var found []domain.DiscoveredDevice
	if o.identity != nil {
		found = o.identity.Discovered
	}
	orphans, merges := revisionImpact(o.committed.Config, next, o.ov.list, found)
	if len(orphans) > 0 && !force {
		return nil, &ErrOverlaysOrphaned{References: referencesOf(orphans)}
	}
	objects := map[uuid.UUID][]string{}
	ids := make([]uuid.UUID, 0, len(orphans))
	for _, or := range orphans {
		ids = append(ids, or.Overlay.Id)
		objects[or.Overlay.Id] = or.Objects
	}
	for _, ch := range o.ov.store.Orphan(ids) {
		changes = append(changes, actedChange{Change: ch, objects: objects[ch.Overlay.Id]})
	}
	for _, ch := range o.ov.store.Retarget(merges, int64(gen)) {
		changes = append(changes, actedChange{Change: ch})
	}
	return changes, nil
}
