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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/auth"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/linux"
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
	// queues are the counters of the netem leaves by engine.QueueKey; nil when they could not be read
	queues map[string]linux.NormStats
	// flaps are the flapping faults by compiler.FlapKey
	flaps map[string]engine.FlapState
	// world resolves the precedence of the snapshot (who overrides a fault); nil without an active revision
	world *domain.World
	// matched notes a reading of a rule's counter and returns when it was last seen growing (nil: not
	// since the gateway started); nil when no counters were read
	matched func(counter string, packets, epoch int64) *time.Time
}

// readOverlayContext takes the snapshot and, when asked, reads the named counters once for all
// the overlays of a response. A counter read that fails leaves the counters out: the overlays are
// still the truth.
func (s *Server) readOverlayContext(ctx context.Context, withCounters bool) overlayContext {
	oc := overlayContext{snap: s.cfg.Engine.Snapshot(), flaps: map[string]engine.FlapState{}}
	for _, f := range s.cfg.Engine.Flaps() {
		oc.flaps[f.Key] = f
	}
	if oc.snap.Config != nil {
		oc.world, _ = domain.NewWorld(oc.snap.Config, oc.snap.Overlays)
	}
	if withCounters && (len(oc.snap.Faults) > 0 || len(oc.snap.PMTU) > 0 || oc.snap.Access != nil || len(oc.snap.WGActions) > 0) {
		cs, err := s.cfg.Engine.ReadCounters(ctx)
		if err != nil {
			s.log.Warn("cannot read the fault counters", "error", err)
			return oc
		}
		oc.counters = cs
		now := s.clk.Now()
		s.matches.prune(func(name string) bool { _, ok := cs[name]; return ok }, now)
		oc.matched = func(counter string, packets, epoch int64) *time.Time {
			return s.matches.observe(counter, packets, epoch, now)
		}
		if len(oc.snap.TCDevs) > 0 {
			qs, err := s.cfg.Engine.ReadQueues(ctx)
			if err != nil {
				s.log.Warn("cannot read the fault queues", "error", err)
			} else {
				oc.queues = qs
			}
		}
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
	var mtu []compiler.PMTUFault
	for _, f := range oc.snap.PMTU {
		if f.Layer == layer && strings.EqualFold(f.Source, source) {
			mtu = append(mtu, f)
		}
	}
	if len(idx) == 0 && len(mtu) == 0 {
		return nil
	}
	c := model.Counter{}
	// the packets classified into an MTU fault, in both directions (a black hole's drops are a counter of their own)
	for _, f := range mtu {
		for _, name := range []string{f.CounterUp, f.CounterDown} {
			v := oc.counters[name]
			c.Packets += v.Packets
			c.Bytes += v.Bytes
		}
		if e := oc.snap.FaultEpochs[f.Key]; e > c.Epoch {
			c.Epoch = e
		}
	}
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

// queuesOf lists the netem queues the faults of one source feed: one per interface of the tree and
// direction the fault impairs, with the counters the kernel holds for it and the epoch of the leaf. A
// queue the kernel does not hold (read failed, or not applied yet) is left out. Nil without any.
func (oc overlayContext) queuesOf(layer, source string) []model.QueueStats {
	if oc.queues == nil {
		return nil
	}
	var out []model.QueueStats
	for _, i := range oc.faultsOf(layer, source) {
		f := oc.snap.Faults[i]
		for _, dir := range []compiler.Direction{compiler.Upload, compiler.Download} {
			if (dir == compiler.Upload && f.Upload == nil) || (dir == compiler.Download && f.Download == nil) {
				continue
			}
			class := compiler.ClassIDOf(f.ID, dir)
			for _, dev := range oc.snap.TCDevs {
				key := engine.QueueKey(dev, class)
				st, ok := oc.queues[key]
				if !ok {
					continue
				}
				q := model.QueueStats{
					Interface:      ptr(dev),
					Direction:      ptr(model.Upload),
					SentPackets:    ptr(int64(st.Packets)),
					SentBytes:      ptr(int64(st.Bytes)),
					DroppedPackets: ptr(int64(st.Drops)),
					Overlimits:     ptr(int64(st.Overlimits)),
					BacklogPackets: ptr(int64(st.Qlen)),
					BacklogBytes:   ptr(int64(st.Backlog)),
					Epoch:          ptr(oc.snap.QueueEpochs[key]),
				}
				if dir == compiler.Download {
					q.Direction = ptr(model.Download)
				}
				if id, err := uuid.Parse(f.Device); err == nil && f.Device != "" {
					q.Device = &id
				}
				if fl, ok := oc.flaps[compiler.FlapKey(f.Key, dir)]; ok {
					phase := model.FlapStatePhaseUp
					if fl.InDown {
						phase = model.FlapStatePhaseDown
					}
					q.Flapping = &model.FlapState{Phase: phase, Since: fl.Since, NextChangeAt: fl.Next}
				}
				out = append(out, q)
			}
		}
	}
	return out
}

// effectOf says how much of the traffic it selects a fault (or one part of an activated profile) wins,
// over every source of its scope: `effective` all of it, `partially_overridden` a part (other faults beat
// it on the rest: a device fault on one device of a network fault's network, a fault on one port of
// it), `overridden` none. The compiler counts it for the families with a classification table
// (impairment, MTU); a fault of another family, or one whose traffic the tables do not classify (a
// hostname), is `effective` when it wins at all. The explanation of a single destination says who wins
// there.
func (oc overlayContext) effectOf(layer, source, family string) model.EffectState {
	key := layer + ":" + strings.ToLower(source) + ":" + family
	if c, ok := oc.snap.Coverage[key]; ok && c.Matched > 0 {
		switch {
		case c.Won == 0:
			return model.EffectStateOverridden
		case c.Partial():
			return model.EffectStatePartiallyOverridden
		}
		return model.EffectStateEffective
	}
	if oc.snap.Winners[key] {
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
		if fam == "tunnel" && st == model.EffectStateEffective && len(oc.faultsOf("overlay", ov.Id.String())) == 0 {
			// the fault wins but nothing of it is in the packet path: its peer is not on an interface or has no
			// address yet (the compiler says so), or it impairs nothing
			st = model.EffectStateDisabled
		}
		ov.State = &st
		ov.Counters = oc.counterOf("overlay", ov.Id.String())
		if q := oc.queuesOf("overlay", ov.Id.String()); len(q) > 0 {
			ov.Queues = &q
		}
		if overridden(st) && ov.Fault != nil {
			f := ov.Fault
			ov.OverriddenBy = refsOrNil(oc.overriddenBy(domain.LayerOverlay, ov.Id.String(), ov.Target, f.Destination, f.Protocol, f.Ports, map[string]bool{fam: true}))
		}
	}
	if ov.Kind == model.OverlayKindProfile {
		// the parts of the profile are faults of the overlay's id: their counters and queues add up
		st := oc.profileEffect(ov)
		ov.State = &st
		ov.Counters = oc.counterOf("overlay", ov.Id.String())
		if q := oc.queuesOf("overlay", ov.Id.String()); len(q) > 0 {
			ov.Queues = &q
		}
		if overridden(st) {
			// every family a part of the profile can compete in: a family without a part has no candidate of the overlay
			fams := map[string]bool{domain.FamilyImpairment: true, domain.FamilyMTU: true, domain.FamilyDNS: true, domain.FamilyTLS: true}
			ov.OverriddenBy = refsOrNil(oc.overriddenBy(domain.LayerOverlay, ov.Id.String(), ov.Target, nil, nil, nil, fams))
		}
	}
	if ov.Kind == model.OverlayKindRule {
		return oc.overlayRule(ov)
	}
	if ov.Kind == model.OverlayKindWireguard {
		return oc.overlayWireGuard(ov)
	}
	return ov
}

// overlayWireGuard completes a WireGuard action: effective while it changes something in the kernel (the peer is
// off its interface, has the key nobody holds, or its endpoint is blocked), disabled while it does not (its peer
// is not on an interface, or the address of the peer to block is not known yet). A blocked endpoint counts
// the packets it dropped.
func (oc overlayContext) overlayWireGuard(ov model.Overlay) model.Overlay {
	st := model.EffectStateDisabled
	for _, a := range oc.snap.WGActions {
		if !strings.EqualFold(a.Overlay, ov.Id.String()) {
			continue
		}
		st = model.EffectStateEffective
		if a.Counter != "" && oc.counters != nil {
			v := oc.counters[a.Counter]
			ov.Counters = &model.Counter{Packets: v.Packets, Bytes: v.Bytes, Epoch: oc.snap.FaultEpochs[engine.WGActionKey(a.Overlay)]}
		}
	}
	ov.State = &st
	return ov
}

func (oc overlayContext) overlayRule(ov model.Overlay) model.Overlay {
	if r, ok := oc.snap.Access.Rule("overlay:" + ov.Id.String()); ok {
		st := model.EffectStateEffective
		ov.State = &st
		ov.Counters = oc.ruleCounter(*r)
	} else {
		// the overlay is in the store but not in the packet path (a rule that names a hostname before M20,
		// or a change the apply loop has not verified yet)
		st := model.EffectStateDisabled
		ov.State = &st
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
	res, err := s.cfg.Engine.DeleteOverlay(context.WithoutCancel(contextOf(c)), overlayId, ownerLimit(p), actorOf(p))
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
	res, err := s.cfg.Engine.RenewOverlay(contextOf(c), overlayId, ownerLimit(principalOf(c)))
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
	ID           string             `json:"id"`
	Config       model.ConfigFault  `json:"config"`
	State        model.EffectState  `json:"state"`
	OverriddenBy []model.FaultRef   `json:"overridden_by,omitempty"`
	Counters     *model.Counter     `json:"counters,omitempty"`
	Queues       []model.QueueStats `json:"queues,omitempty"`
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
	var out []faultView
	for id, f := range deref(v.cfg.Faults) {
		fv := faultView{ID: id, Config: f, State: model.EffectStateEffective}
		switch {
		case f.Enabled != nil && !*f.Enabled:
			fv.State = model.EffectStateDisabled
		case v.active:
			fv.State = oc.effectOf("config", id, faultFamilyOf(f))
			fv.Counters = oc.counterOf("config", id)
			fv.Queues = oc.queuesOf("config", id)
			if overridden(fv.State) {
				fv.OverriddenBy = oc.overriddenBy(domain.LayerConfig, id, f.Source, f.Destination, f.Protocol, f.Ports, map[string]bool{faultFamilyOf(f): true})
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

// overridden reports whether another fault takes some or all of the traffic of a fault.
func overridden(st model.EffectState) bool {
	return st == model.EffectStateOverridden || st == model.EffectStatePartiallyOverridden
}

func refsOrNil(r []model.FaultRef) *[]model.FaultRef {
	if len(r) == 0 {
		return nil
	}
	return &r
}

// overriddenBy names the faults that beat a fault (a configured one, a fault overlay or the parts of a
// profile activation): for the traffic of every device in its scope, the winner of each of its
// families at a destination and port the fault selects.
func (oc overlayContext) overriddenBy(layer domain.Layer, id string, scope *model.Scope, dest *model.Destination, proto *model.Protocol, ports *[]int, families map[string]bool) []model.FaultRef {
	if scope == nil || oc.world == nil {
		return nil
	}
	q := domain.Query{}
	if dest != nil && dest.Cidr != nil {
		if pfx, err := netip.ParsePrefix(*dest.Cidr); err == nil {
			q.DestIP = pfx.Addr()
		} else if a, err := netip.ParseAddr(*dest.Cidr); err == nil {
			q.DestIP = a
		}
	}
	if proto != nil {
		q.Protocol = string(*proto)
	}
	if ports != nil && len(*ports) > 0 {
		q.Port = (*ports)[0]
	}
	seen := map[string]bool{}
	var out []model.FaultRef
	for _, src := range oc.world.Sources(oc.snap.Identity) {
		if !oc.world.ScopeContains(*scope, src.Subject) {
			continue
		}
		q.Source = src.Subject
		for _, r := range oc.world.Resolve(q) {
			if r.Winner == nil || !families[r.Family] {
				continue
			}
			for _, o := range r.Overridden {
				if strings.EqualFold(o.ID, id) && o.Layer == layer && !seen[r.Winner.ID+"/"+r.Family] {
					seen[r.Winner.ID+"/"+r.Family] = true
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
