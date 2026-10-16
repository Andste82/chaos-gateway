package api

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// forwardViews builds the views of the port forwards of a revision (M35a), sorted by name and id: the
// configuration with the status the observed state gives it (a device target follows the device's
// address; a forward of a device without one is inactive and says why).
func (s *Server) forwardViews(v view) []model.PortForwardView {
	facts, _ := s.cfg.Engine.ForwardFacts(v.cfg)
	idx, _ := domain.BuildIndex(v.cfg)
	fwds := deref(v.cfg.PortForwards)
	var out []model.PortForwardView
	for _, f := range domain.Forwards(v.cfg) {
		id, err := uuid.Parse(f.ID)
		if err != nil {
			continue // a key the validation refuses
		}
		out = append(out, model.PortForwardView{Id: id, Config: fwds[f.ID], Status: forwardStatus(idx.ForwardStatus(f, facts))})
	}
	return out
}

// forwardStatus is the spec's PortForwardStatus of a domain.ForwardState.
func forwardStatus(st domain.ForwardState) model.PortForwardStatus {
	out := model.PortForwardStatus{State: model.PortForwardStatusStateActive}
	if !st.Active {
		out.State = model.PortForwardStatusStateInactive
		out.Reason = ptr(model.PortForwardStatusReason(st.Reason))
		out.Message = ptr(st.Message)
	}
	if st.Target.IsValid() {
		out.TargetAddress = ptr(st.Target.String())
	}
	return out
}

func forwardSortKey(fv model.PortForwardView) string {
	return sortKey(deref(fv.Config.Name), fv.Id.String())
}

// ListPortForwards implements GET /port-forwards.
func (s *Server) ListPortForwards(c *gin.Context, params model.ListPortForwardsParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	items, next, err := page(s.forwardViews(v), forwardSortKey, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	if items == nil {
		items = []model.PortForwardView{}
	}
	c.JSON(200, model.PortForwardPage{Items: items, NextCursor: next})
}

// GetPortForward implements GET /port-forwards/{forwardId}: by id or by name.
func (s *Server) GetPortForward(c *gin.Context, forwardId model.Ref, params model.GetPortForwardParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	for _, fv := range s.forwardViews(v) {
		if strings.EqualFold(fv.Id.String(), forwardId) || (fv.Config.Name != nil && strings.EqualFold(*fv.Config.Name, forwardId)) {
			c.JSON(200, fv)
			return
		}
	}
	s.write(c, notFound("port forward", forwardId))
}

// previewForwards are the enabled forwards of a revision as the preview lists them (M35a), with the
// warnings that faults do not apply to the connections through them (M35b) where a fault selects
// their target. base is the active configuration, nil before the first revision.
func (s *Server) previewForwards(cfg, base *model.Configuration) ([]model.PreviewPortForward, []gin.H) {
	fs := domain.Forwards(cfg)
	if len(fs) == 0 {
		return nil, nil
	}
	idx, _ := domain.BuildIndex(cfg)
	var before map[string]model.PortForward
	if base != nil {
		before = deref(base.PortForwards)
	}
	var out []model.PreviewPortForward
	for _, f := range fs {
		id, err := uuid.Parse(f.ID)
		if !f.Enabled || err != nil {
			continue
		}
		pf := model.PreviewPortForward{Id: id, Protocol: model.PortForwardProtocol(f.Protocol), Entry: model.PortForwardEntry(f.Entry),
			ExternalPorts: f.ExternalPorts(), Target: idx.TargetLabel(f), TargetPorts: f.TargetPorts()}
		if f.Name != "" {
			pf.Name = ptr(f.Name)
		}
		if old, ok := before[f.ID]; !ok || !sameJSON(old, deref(cfg.PortForwards)[f.ID]) {
			pf.New = ptr(true)
		}
		out = append(out, pf)
	}
	world, err := domain.NewWorld(cfg, s.cfg.Engine.Snapshot().Overlays)
	if err != nil {
		return out, nil
	}
	facts, _ := s.cfg.Engine.ForwardFacts(cfg)
	var warnings []gin.H
	for _, n := range world.ForwardFaultNotes(fs, facts) {
		fault := n.Fault.Name
		if fault == "" {
			fault = n.Fault.ID
		}
		if n.Fault.IsProfilePart() {
			fault += " (profile " + n.Fault.ProfileName + ")"
		}
		fwd := n.Forward.Name
		if fwd == "" {
			fwd = n.Forward.ID
		}
		warnings = append(warnings, gin.H{"code": "faults_not_on_forwards", "path": "/port_forwards/" + n.Forward.ID,
			"message": domain.FaultsNotOnForwards + ": the " + n.Fault.Family + " fault " + fault + " of " + idx.TargetLabel(n.Forward) +
				" does not impair the connections through the forward " + fwd})
	}
	return out, warnings
}

// sameJSON reports whether two values have the same JSON form.
func sameJSON(a, b any) bool {
	x, err1 := jsonMarshal(a)
	y, err2 := jsonMarshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}
