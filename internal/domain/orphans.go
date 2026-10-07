package domain

import (
	"sort"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// This file is what applying a revision means for the overlays that are active (plan §2.1.1):
// a revision that deletes an object an overlay refers to orphans the overlay, and a revision that
// merges a discovered device into a configured one moves the overlays of the discovered device.

// Orphan is an active overlay that refers to objects a revision deletes.
type Orphan struct {
	Overlay model.Overlay
	// Objects are the JSON pointers of the deleted objects in the configuration the overlay was
	// written against (BlockingReference.object in the spec), sorted, without duplicates.
	Objects []string
}

// objectPointer returns the JSON pointer of an object of the configuration the index was built from.
func objectPointer(idx *Index, kind Kind, id string) string {
	switch kind {
	case KindNetwork, KindLink:
		if n, ok := idx.Networks[id]; ok {
			return n.Path
		}
		return schema.Pointer("/networks", id)
	case KindDevice, KindClient:
		if d, ok := idx.Devices[id]; ok {
			return d.Path
		}
		return schema.Pointer("/devices", id)
	case KindGroup:
		return schema.Pointer("/groups", id)
	case KindProfile:
		return schema.Pointer("/profiles", id)
	}
	return "/" + id
}

// OrphanedOverlays returns the overlays that applying next in place of current would orphan: those
// with a reference to an object of current (a network, a link, a device, a client, a group or a
// custom profile) that next does not have. Both configurations must be normalized, and so must
// the overlays (ValidateOverlay resolves their references to UUIDs). Devices that are discovered
// but not configured are not objects of a configuration and never block a revision; pass those
// that still exist after the revision to WithDiscovered so that an overlay that targets one is
// not mistaken for an orphan, and run the merges (DiscoveredMerges) first.
//
// The result is sorted by overlay id.
func OrphanedOverlays(current, next *model.Configuration, overlays []model.Overlay, opts ...Option) []Orphan {
	oldIdx, _ := BuildIndex(current)
	newIdx, _ := newIndex(next, collectOptions(opts))
	var out []Orphan
	for i := range overlays {
		req := RequestOf(&overlays[i])
		seen := map[string]bool{}
		visitOverlayRequest(&req, func(_ string, kind Kind, ref *string) {
			id := lower(*ref)
			if !oldIdx.exists(kind, id) || newIdx.exists(kind, id) {
				return
			}
			seen[objectPointer(oldIdx, kind, id)] = true
		})
		if len(seen) == 0 {
			continue
		}
		o := Orphan{Overlay: overlays[i]}
		for p := range seen {
			o.Objects = append(o.Objects, p)
		}
		sort.Strings(o.Objects)
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Overlay.Id.String() < out[j].Overlay.Id.String() })
	return out
}

// DiscoveredMerges returns, for the discovered devices that a configuration now covers, the
// configured device they merge into: the discovered device's UUID maps to the UUID of the device
// that owns its MAC addresses (or, for one without a MAC, its addresses). Overlays that target the
// discovered device move to the configured one (overlay.Store.Retarget). A device that was adopted
// under its own UUID is not a merge. found are the discovered devices before the revision; the
// configuration must be normalized.
func DiscoveredMerges(cfg *model.Configuration, found []DiscoveredDevice) map[string]string {
	if len(found) == 0 {
		return nil
	}
	idx, _ := BuildIndex(cfg)
	macs := deviceMACs(idx, nil)
	id := ResolveIdentity(cfg, Observed{Discovered: found}, nil)
	still := map[string]bool{}
	for _, d := range id.Discovered {
		still[d.ID] = true
	}
	merges := map[string]string{}
	for _, d := range found {
		if still[d.ID] {
			continue
		}
		if _, configured := idx.Devices[strings.ToLower(d.ID)]; configured {
			continue // adopted under its own UUID
		}
		target := ""
		for _, m := range d.MACs {
			if dev, ok := macs[lower(m)]; ok {
				target = dev
				break
			}
		}
		if target == "" {
			for _, ip := range d.IPs {
				if dev, ok := id.OwnerOf(ip); ok && dev != d.ID {
					target = dev
					break
				}
			}
		}
		if target != "" {
			merges[lower(d.ID)] = target
		}
	}
	if len(merges) == 0 {
		return nil
	}
	return merges
}

// ScopeContains reports whether the subject lies in a scope: the question behind "overlays whose
// target contains this device" of the API's overlay list.
func (w *World) ScopeContains(scope model.Scope, s Subject) bool { return w.scopeMatches(scope, s) }
