package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// ownerOf is who owns the overlays a request writes (plan §2.15): the token itself, or, for what
// the UI does, the admin user (not the browser session: the overlays of a logged-out tab stay).
func ownerOf(p *Principal) model.Owner {
	if p != nil && p.Kind == "token" {
		n := p.TokenName
		return model.Owner{Type: "token", Id: p.TokenID, Name: &n}
	}
	return model.Owner{Type: "user", Id: "admin"}
}

// limitedToOwn reports whether the caller may only touch its own overlays: a token with the scope
// `overlays`. The full scope (and the admin) may touch every overlay.
func limitedToOwn(p *Principal) bool { return p == nil || !p.Scope.Allows(auth.ScopeFull) }

// ownerLimit returns the owner a deletion or a renewal is limited to, nil for no limit.
func ownerLimit(p *Principal) *model.Owner {
	if !limitedToOwn(p) {
		return nil
	}
	o := ownerOf(p)
	return &o
}

// overlayState is what an overlay views are built from: one snapshot and one counter reading.
type overlayContext struct {
	snap     *engine.Snapshot
	counters map[string]engine.CounterValue
}

// readOverlayContext takes the snapshot and, when asked, reads the named counters once for all
// the overlays of a response. A counter read that fails leaves the counters out: the overlays are
// still the truth.
func (s *Server) readOverlayContext(ctx context.Context, withCounters bool) overlayContext {
	oc := overlayContext{snap: s.cfg.Engine.Snapshot()}
	if withCounters && len(oc.snap.Faults) > 0 {
		cs, err := s.cfg.Engine.ReadCounters(ctx)
		if err != nil {
			s.log.Warn("cannot read the fault counters", "error", err)
			return oc
		}
		oc.counters = cs
	}
	return oc
}

// faultsOf returns the fault ids of the target that belong to a layer and a source (an overlay or a
// configured fault).
func (oc overlayContext) faultsOf(layer, source string) []int {
	var idx []int
	for i, f := range oc.snap.Faults {
		if f.Layer == layer && strings.EqualFold(f.Source, source) {
			idx = append(idx, i)
		}
	}
	return idx
}

// counterOf sums the counters of the faults of one source: the packets classified into them, in
// both directions. The epoch is the newest generation in which one of the faults appeared, so it
// changes when a counter starts over. Nil when the source has no fault in the kernel.
func (oc overlayContext) counterOf(layer, source string) *model.Counter {
	if oc.counters == nil {
		return nil
	}
	idx := oc.faultsOf(layer, source)
	if len(idx) == 0 {
		return nil
	}
	c := model.Counter{}
	for _, i := range idx {
		f := oc.snap.Faults[i]
		for _, name := range []string{f.CounterUp, f.CounterDown} {
			v := oc.counters[name]
			c.Packets += v.Packets
			c.Bytes += v.Bytes
		}
		if e := oc.snap.FaultEpochs[f.Key]; e > c.Epoch {
			c.Epoch = e
		}
	}
	return &c
}

// effectOf says whether the fault wins for some traffic: `effective`, or `overridden` when another
// fault beats it everywhere. A fault that wins only for a part of its selector is shown as
// effective (the explanation of a single destination says more).
func (oc overlayContext) effectOf(layer, source, family string) model.EffectState {
	if oc.snap.Winners[layer+":"+strings.ToLower(source)+":"+family] {
		return model.EffectStateEffective
	}
	return model.EffectStateOverridden
}

// overlayView completes an overlay with what the kernel and the resolution say.
func (oc overlayContext) overlayView(ov model.Overlay) model.Overlay {
	if ov.Kind == model.OverlayKindFault {
		fam := "impairment"
		if ov.Fault != nil && ov.Fault.Family != nil {
			fam = string(*ov.Fault.Family)
		}
		st := oc.effectOf("overlay", ov.Id.String(), fam)
		ov.State = &st
		ov.Counters = oc.counterOf("overlay", ov.Id.String())
	}
	return ov
}

func overlaySortKey(ov model.Overlay) string {
	return fmt.Sprintf("%020d/%s", ov.CreatedAt.UnixNano(), ov.Id)
}

// ListOverlays implements GET /overlays: the active overlays, oldest first.
func (s *Server) ListOverlays(c *gin.Context, params model.ListOverlaysParams) {
	oc := s.readOverlayContext(contextOf(c), true)
	snap := oc.snap
	var (
		owner         *string
		deviceID, net string
		world         *domain.World
	)
	if params.Owner != nil && *params.Owner != "" {
		o := *params.Owner
		if o == "self" {
			o = ownerOf(principalOf(c)).Id
		}
		owner = &o
	}
	if params.Device != nil || params.Network != nil {
		if snap.Config == nil {
			s.write(c, newProblem(model.ErrorCodeNotFound, "there is no active revision yet"))
			return
		}
		w, err := domain.NewWorld(snap.Config, snap.Overlays)
		if err != nil {
			s.fail(c, err)
			return
		}
		world = w
		if params.Device != nil {
			id, ok := w.Index.Resolve(domain.KindDevice, *params.Device)
			if !ok {
				for _, d := range snap.Devices {
					if strings.EqualFold(d.ID, *params.Device) || strings.EqualFold(d.Name, *params.Device) {
						id, ok = strings.ToLower(d.ID), true
						break
					}
				}
			}
			if !ok {
				s.write(c, notFound("device", *params.Device))
				return
			}
			deviceID = id
		}
		if params.Network != nil {
			id, ok := w.Index.Resolve(domain.KindNetwork, *params.Network)
			if !ok {
				s.write(c, notFound("network", *params.Network))
				return
			}
			net = id
		}
	}
	var subject domain.Subject
	if deviceID != "" {
		subject.Device = deviceID
		for _, d := range snap.Devices {
			if strings.EqualFold(d.ID, deviceID) {
				subject.Network = strings.ToLower(d.Network)
				if len(d.Addresses) > 0 {
					subject.IP = d.Addresses[0]
				}
			}
		}
	}
	var out []model.Overlay
	for _, ov := range snap.Overlays {
		switch {
		case params.Kind != nil && ov.Kind != *params.Kind:
			continue
		case owner != nil && ov.Owner.Id != *owner:
			continue
		case deviceID != "" && (ov.Target == nil || !world.ScopeContains(*ov.Target, subject)):
			continue
		case net != "" && (ov.Target == nil || ov.Target.Network == nil || !strings.EqualFold(*ov.Target.Network, net)):
			continue
		}
		out = append(out, oc.overlayView(ov))
	}
	sort.SliceStable(out, func(i, j int) bool { return overlaySortKey(out[i]) < overlaySortKey(out[j]) })
	items, next, err := page(out, overlaySortKey, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// GetOverlay implements GET /overlays/{overlayId}.
func (s *Server) GetOverlay(c *gin.Context, overlayId openapi_types.UUID) {
	oc := s.readOverlayContext(contextOf(c), true)
	for _, ov := range oc.snap.Overlays {
		if ov.Id == overlayId {
			c.JSON(200, oc.overlayView(ov))
			return
		}
	}
	s.write(c, newProblem(model.ErrorCodeNotFound, "no overlay %q (it never existed, or it was removed or has expired)", overlayId.String()))
}

// CreateOverlay implements POST /overlays: 201 for a new overlay, 200 when one with the same key
// was replaced (it keeps its id). The answer comes after the kernel runs the change.
func (s *Server) CreateOverlay(c *gin.Context, _ model.CreateOverlayParams) {
	raw, rerr := readBody(c)
	if rerr != nil {
		s.write(c, rerr)
		return
	}
	req, err := domain.DecodeOverlayRequest(raw, domain.FormatJSON)
	if err != nil {
		s.fail(c, err)
		return
	}
	p := principalOf(c)
	owner := ownerOf(p)
	// the apply is not the client's to cancel: the engine goes on, and so does the record of it
	res, err := s.cfg.Engine.PutOverlay(context.WithoutCancel(contextOf(c)), engine.OverlayWrite{Owner: owner, Request: *req, Actor: actorOf(p)})
	if err != nil {
		var af *engine.ErrApplyFailed
		if errors.As(err, &af) {
			s.record(c, "overlay.apply_failed", nil, 0, firstLine(err.Error()))
		}
		s.fail(c, err)
		return
	}
	action, status := "overlay.replace", http.StatusOK
	if res.Created {
		action, status = "overlay.create", http.StatusCreated
		c.Header("Location", "/api/v1/overlays/"+res.Overlay.Id.String())
	}
	s.record(c, action, &audit.Object{Kind: "overlay", ID: res.Overlay.Id.String()}, 0, string(res.Overlay.Kind))
	c.Header("Chaos-Generation", itoa(int64(res.Generation)))
	c.JSON(status, s.readOverlayContext(contextOf(c), false).overlayView(res.Overlay))
}

// DeleteOverlay implements DELETE /overlays/{overlayId}. A token with the scope `overlays` may only
// remove its own overlays; the full scope any.
func (s *Server) DeleteOverlay(c *gin.Context, overlayId openapi_types.UUID) {
	p := principalOf(c)
	res, err := s.cfg.Engine.DeleteOverlay(context.WithoutCancel(contextOf(c)), uuid.UUID(overlayId), ownerLimit(p), actorOf(p))
	if err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "overlay.delete", &audit.Object{Kind: "overlay", ID: overlayId.String()}, 0, string(res.Overlay.Kind))
	c.Header("Chaos-Generation", itoa(int64(res.Generation)))
	c.Status(http.StatusNoContent)
}

// RenewOverlay implements POST /overlays/{overlayId}/renew: the heartbeat of an owner that holds a
// lease. It changes nothing in the kernel.
func (s *Server) RenewOverlay(c *gin.Context, overlayId openapi_types.UUID) {
	res, err := s.cfg.Engine.RenewOverlay(contextOf(c), uuid.UUID(overlayId), ownerLimit(principalOf(c)))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, s.readOverlayContext(contextOf(c), false).overlayView(res.Overlay))
}

type resetBody struct {
	RemovedOverlays int   `json:"removed_overlays"`
	AbortedRuns     int   `json:"aborted_runs"`
	Generation      int64 `json:"generation"`
}

// Reset implements POST /reset: the caller's overlays, or every owner's with owner=all (scope
// full). Runs join in with M15.
func (s *Server) Reset(c *gin.Context, params model.ResetParams) {
	p := principalOf(c)
	all := params.Owner != nil && *params.Owner == model.ResetParamsOwner("all")
	var by *model.Owner
	if all {
		if p == nil || !p.Scope.Allows(auth.ScopeFull) {
			s.write(c, newProblem(model.ErrorCodeForbidden, "resetting the overlays of every owner needs the scope full"))
			return
		}
	} else {
		o := ownerOf(p)
		by = &o
	}
	res, err := s.cfg.Engine.ResetOverlays(context.WithoutCancel(contextOf(c)), by, actorOf(p))
	if err != nil {
		s.fail(c, err)
		return
	}
	detail := "own"
	if all {
		detail = "all"
	}
	s.record(c, "overlay.reset", nil, 0, fmt.Sprintf("owner=%s removed=%d", detail, res.Removed))
	c.Header("Chaos-Generation", itoa(int64(res.Generation)))
	c.JSON(200, resetBody{RemovedOverlays: res.Removed, Generation: int64(res.Generation)})
}

// ---- faults

type faultView struct {
	ID           string            `json:"id"`
	Config       model.ConfigFault `json:"config"`
	State        model.EffectState `json:"state"`
	OverriddenBy []model.FaultRef  `json:"overridden_by,omitempty"`
	Counters     *model.Counter    `json:"counters,omitempty"`
}

func faultFamilyOf(f model.ConfigFault) string {
	if f.Family != nil {
		return string(*f.Family)
	}
	return string(model.FaultFamilyImpairment)
}

// faultViews builds the views of the configured faults of a revision. Only the revision the kernel
// runs has a state (what wins, what overrides it) and counters; another revision shows the
// configuration alone.
func (s *Server) faultViews(ctx context.Context, v view) []faultView {
	var oc overlayContext
	if v.active {
		oc = s.readOverlayContext(ctx, true)
	}
	var world *domain.World
	if v.active && oc.snap.Config != nil {
		world, _ = domain.NewWorld(oc.snap.Config, oc.snap.Overlays)
	}
	var out []faultView
	for id, f := range deref(v.cfg.Faults) {
		fv := faultView{ID: id, Config: f, State: model.EffectStateEffective}
		switch {
		case f.Enabled != nil && !*f.Enabled:
			fv.State = model.EffectStateDisabled
		case v.active:
			fv.State = oc.effectOf("config", id, faultFamilyOf(f))
			fv.Counters = oc.counterOf("config", id)
			if fv.State == model.EffectStateOverridden && world != nil {
				fv.OverriddenBy = s.overriddenBy(oc.snap, world, id, f)
			}
		}
		out = append(out, fv)
	}
	sort.Slice(out, func(i, j int) bool {
		return sortKey(deref(out[i].Config.Name), out[i].ID) < sortKey(deref(out[j].Config.Name), out[j].ID)
	})
	return out
}

func deref[T any](p *T) T {
	var z T
	if p != nil {
		z = *p
	}
	return z
}

// overriddenBy names the faults that beat a configured fault: for the traffic of every device in
// its scope, the winner of the family at a destination and port the fault selects.
func (s *Server) overriddenBy(snap *engine.Snapshot, w *domain.World, id string, f model.ConfigFault) []model.FaultRef {
	if f.Source == nil {
		return nil
	}
	q := domain.Query{}
	if f.Destination != nil && f.Destination.Cidr != nil {
		if pfx, err := netip.ParsePrefix(*f.Destination.Cidr); err == nil {
			q.DestIP = pfx.Addr()
		} else if a, err := netip.ParseAddr(*f.Destination.Cidr); err == nil {
			q.DestIP = a
		}
	}
	if f.Protocol != nil {
		q.Protocol = string(*f.Protocol)
	}
	if f.Ports != nil && len(*f.Ports) > 0 {
		q.Port = (*f.Ports)[0]
	}
	seen := map[string]bool{}
	var out []model.FaultRef
	for _, src := range w.Sources(snap.Identity) {
		if !w.ScopeContains(*f.Source, src.Subject) {
			continue
		}
		q.Source = src.Subject
		for _, r := range w.Resolve(q) {
			if r.Winner == nil || r.Family != faultFamilyOf(f) {
				continue
			}
			for _, o := range r.Overridden {
				if strings.EqualFold(o.ID, id) && o.Layer == domain.LayerConfig && !seen[r.Winner.ID] {
					seen[r.Winner.ID] = true
					out = append(out, r.Winner.Ref(""))
				}
			}
		}
	}
	return out
}

// ListFaults implements GET /faults: the configured faults with their state and counters.
func (s *Server) ListFaults(c *gin.Context, params model.ListFaultsParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	all := s.faultViews(contextOf(c), v)
	var filtered []faultView
	for _, f := range all {
		if params.Family != nil && string(*params.Family) != faultFamilyOf(f.Config) {
			continue
		}
		filtered = append(filtered, f)
	}
	items, next, err := page(filtered, func(f faultView) string { return sortKey(deref(f.Config.Name), f.ID) }, params.Cursor, params.Limit)
	if err != nil {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "%v", err))
		return
	}
	c.JSON(200, listBody(items, next))
}

// GetFault implements GET /faults/{faultId}.
func (s *Server) GetFault(c *gin.Context, faultId model.Ref, params model.GetFaultParams) {
	v, ok := s.viewOf(c, params.Revision)
	if !ok {
		return
	}
	for _, f := range s.faultViews(contextOf(c), v) {
		if strings.EqualFold(f.ID, faultId) || strings.EqualFold(deref(f.Config.Name), faultId) {
			c.JSON(200, f)
			return
		}
	}
	s.write(c, notFound("fault", faultId))
}

// ---- explain

var hostnameRE = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$`)

// Explain implements GET /explain: the verdict and the winning fault per family for a device (or a
// source address) towards a destination, with the route the kernel takes.
func (s *Server) Explain(c *gin.Context, params model.ExplainParams) {
	q := engine.ExplainQuery{Dst: params.Dst}
	if params.Device != nil {
		q.Device = *params.Device
	}
	if params.Src != nil {
		a, err := netip.ParseAddr(*params.Src)
		if err != nil || !a.Is4() {
			s.write(c, newProblem(model.ErrorCodeBadRequest, "src must be an IPv4 address"))
			return
		}
		q.Src = a
	}
	if q.Device == "" && !q.Src.IsValid() {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "give device or src"))
		return
	}
	if a, err := netip.ParseAddr(params.Dst); err == nil {
		if !a.Is4() {
			s.write(c, newProblem(model.ErrorCodeBadRequest, "dst must be an IPv4 address or a hostname"))
			return
		}
	} else if len(params.Dst) > 253 || !hostnameRE.MatchString(params.Dst) {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "dst must be an IPv4 address or a hostname"))
		return
	}
	if params.Protocol != nil {
		if !params.Protocol.Valid() {
			s.write(c, newProblem(model.ErrorCodeBadRequest, "protocol must be one of tcp, udp, icmp, any"))
			return
		}
		q.Protocol = string(*params.Protocol)
	}
	if params.Port != nil {
		q.Port = *params.Port
	}
	ex, err := s.cfg.Engine.Explain(contextOf(c), q)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, ex)
}

// ---- helpers

// readBody reads the request body, within the limit the guard middleware set.
func readBody(c *gin.Context) ([]byte, *problem) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, bodyReadProblem(err)
	}
	return raw, nil
}
