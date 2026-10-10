package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ---- profiles (plan §2.9, M11)

// profileViews builds the views of the built-in profiles and the profiles of a revision. Only the
// revision the kernel runs has activations (overlays exist only against it); another revision shows
// the definitions alone.
func (s *Server) profileViews(ctx context.Context, v view) []model.ProfileView {
	var oc overlayContext
	if v.active {
		oc = s.readOverlayContext(ctx, false)
	}
	var out []model.ProfileView
	add := func(id string, p model.Profile, builtin bool) {
		pid, err := uuid.Parse(id)
		if err != nil {
			return
		}
		pv := model.ProfileView{Id: pid, Builtin: builtin, Available: true, Config: p}
		if parts := domain.UnavailableParts(p); len(parts) > 0 {
			pv.Available = false
			var why []string
			for _, u := range parts {
				why = append(why, fmt.Sprintf("the %s part arrives with milestone %s", u.Family, u.Milestone))
			}
			pv.UnavailableReason = ptr(strings.Join(why, "; "))
		}
		if v.active && oc.snap != nil {
			pv.Activations = oc.activationsOf(id)
		}
		out = append(out, pv)
	}
	for _, b := range domain.BuiltinProfiles() {
		add(b.ID, b.Profile, true)
	}
	for id, p := range deref(v.cfg.Profiles) {
		add(strings.ToLower(id), p, false)
	}
	sort.Slice(out, func(i, j int) bool {
		return sortKey(out[i].Config.Name, out[i].Id.String()) < sortKey(out[j].Config.Name, out[j].Id.String())
	})
	return out
}

// activationsOf lists the overlays that activate a profile (by UUID), oldest first. Nil without any.
func (oc overlayContext) activationsOf(profileID string) *[]model.ProfileActivation {
	var out []model.ProfileActivation
	for _, ov := range oc.snap.Overlays {
		if ov.Kind != model.OverlayKindProfile || ov.Profile == nil || !strings.EqualFold(*ov.Profile, profileID) || ov.Target == nil {
			continue
		}
		st := oc.profileEffect(ov)
		out = append(out, model.ProfileActivation{Overlay: ov.Id, Target: *ov.Target, Owner: ov.Owner, ExpiresAt: ov.ExpiresAt, State: &st})
	}
	if out == nil {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Overlay.String() < out[j].Overlay.String() })
	return &out
}

// profileEffect says whether the parts of an activated profile win: `effective` when each part wins
// for some traffic, `overridden` when none does, `partially_overridden` when some do. A profile that
// is gone from the configuration, or has no part this build resolves, is `disabled`.
func (oc overlayContext) profileEffect(ov model.Overlay) model.EffectState {
	if ov.Profile == nil || oc.snap.Config == nil {
		return model.EffectStateDisabled
	}
	p, _, ok := domain.ProfileByID(oc.snap.Config, *ov.Profile)
	if !ok {
		return model.EffectStateDisabled
	}
	won, total := 0, 0
	for _, fam := range domain.ProfileFamilies(p) {
		if len(domain.UnavailableParts(model.Profile{Parts: partsOf(p, fam)})) > 0 {
			continue
		}
		total++
		if oc.snap.Winners["overlay:"+strings.ToLower(ov.Id.String())+":"+fam] {
			won++
		}
	}
	switch {
	case total == 0:
		return model.EffectStateDisabled
	case won == total:
		return model.EffectStateEffective
	case won == 0:
		return model.EffectStateOverridden
	}
	return model.EffectStatePartiallyOverridden
}

// partsOf is the profile's part of one family alone.
func partsOf(p model.Profile, family string) model.ProfileParts {
	var out model.ProfileParts
	switch family {
	case domain.FamilyImpairment:
		out.Impairment = p.Parts.Impairment
	case domain.FamilyMTU:
		out.Mtu = p.Parts.Mtu
	case domain.FamilyDNS:
		out.Dns = p.Parts.Dns
	case domain.FamilyTLS:
		out.Tls = p.Parts.Tls
	}
	return out
}

// ListProfiles implements GET /profiles.
func (s *Server) ListProfiles(c *gin.Context, params model.ListProfilesParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	items, next, err := page(s.profileViews(contextOf(c), v), func(p model.ProfileView) string {
		return sortKey(p.Config.Name, p.Id.String())
	}, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// GetProfile implements GET /profiles/{profileId}: by UUID or name.
func (s *Server) GetProfile(c *gin.Context, profileId model.Ref, params model.GetProfileParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	for _, p := range s.profileViews(contextOf(c), v) {
		if strings.EqualFold(p.Id.String(), profileId) || strings.EqualFold(p.Config.Name, profileId) {
			c.JSON(200, p)
			return
		}
	}
	s.write(c, notFound("profile", profileId))
}

// previewFaults lists what wins after a revision, with the profile each part comes from.
func previewFaults(tg *compiler.Target) []model.PreviewFault {
	if tg == nil {
		return nil
	}
	var out []model.PreviewFault
	for _, f := range tg.Effective {
		id, err := uuid.Parse(f.Source)
		if err != nil {
			continue
		}
		pf := model.PreviewFault{Family: model.FaultFamily(f.Family), Layer: model.PreviewFaultLayer(f.Layer), Id: id,
			Scope: f.Scope, Summary: f.Summary}
		if f.Profile != "" {
			pf.Profile = ptr(f.Profile)
		}
		out = append(out, pf)
	}
	return out
}
