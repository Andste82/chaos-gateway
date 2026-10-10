package domain

import (
	"strings"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the lookup side of profiles (plan §2.9): which profile a reference names, which
// families its parts compete in and which of them this build can activate. The expansion of an
// activated profile into candidates is World.profileParts (resolve.go); validation of a profile's
// parts is validate_fault.go.

// ProfileFamilies returns the families the parts of a profile compete in when it is activated,
// in the order families are reported.
func ProfileFamilies(p model.Profile) []string {
	var out []string
	if p.Parts.Impairment != nil {
		out = append(out, FamilyImpairment)
	}
	if p.Parts.Mtu != nil {
		out = append(out, FamilyMTU)
	}
	if p.Parts.Dns != nil {
		out = append(out, FamilyDNS)
	}
	if p.Parts.Tls != nil {
		out = append(out, FamilyTLS)
	}
	return out
}

// UnavailablePart is a part of a profile whose family this build does not implement.
type UnavailablePart struct {
	Family string
	// Milestone is the milestone that brings the family.
	Milestone string
}

// unavailableFamilies are the families of a profile that arrive with later milestones (plan §2.9:
// "DNS broken" and "TLS broken" come with M20 and M21). The impairment and MTU families are
// implemented.
var unavailableFamilies = map[string]string{FamilyDNS: "M20", FamilyTLS: "M21"}

// UnavailableParts lists the parts of a profile that cannot be activated in this build, in the
// order of ProfileFamilies. A profile with such a part is stored and shown, but its activation is
// refused as a whole: activating only the other parts would silently be a different profile.
func UnavailableParts(p model.Profile) []UnavailablePart {
	var out []UnavailablePart
	for _, f := range ProfileFamilies(p) {
		if m, ok := unavailableFamilies[f]; ok {
			out = append(out, UnavailablePart{Family: f, Milestone: m})
		}
	}
	return out
}

// ProfileByID looks a profile up by UUID in a normalized configuration: configured profiles first,
// then the built-in ones. The profile is a copy.
func ProfileByID(cfg *model.Configuration, id string) (p model.Profile, builtin, ok bool) {
	id = lower(id)
	if p, ok := deref(cfg.Profiles)[id]; ok {
		return clone(p), false, true
	}
	for _, b := range builtinProfiles {
		if b.ID == id {
			return clone(b.Profile), true, true
		}
	}
	return model.Profile{}, false, false
}

// ProfileRef resolves a reference to a profile - its UUID or its name, ignoring case - against a
// configuration whose keys are UUIDs (a normalized one). Configured profiles are tried before the
// built-in ones, but their names cannot collide (the built-in names are reserved).
func ProfileRef(cfg *model.Configuration, ref string) (id string, p model.Profile, builtin, ok bool) {
	if p, builtin, ok := ProfileByID(cfg, ref); ok {
		return lower(ref), p, builtin, true
	}
	for _, k := range sortedKeys(deref(cfg.Profiles)) {
		if p := deref(cfg.Profiles)[k]; strings.EqualFold(p.Name, ref) {
			return k, clone(p), false, true
		}
	}
	if b, found := BuiltinProfileByName(ref); found {
		return b.ID, b.Profile, true, true
	}
	return "", model.Profile{}, false, false
}
