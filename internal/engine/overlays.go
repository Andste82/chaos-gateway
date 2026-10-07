package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/apply"
	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/overlay"
)

// This file is the engine's side of the overlays (plan §2.1.1, §3.11): overlays are written,
// deleted, renewed, reset and expired by commands to the state owner, which is the only writer of
// the overlay store. Each change makes a new generation and a new desired state; a writer gets its
// answer when the apply loop has verified a generation that contains its change. When that apply
// fails, the overlay changes made since the last verified one are taken back (the store is
// restored to its checkpoint) and every writer waiting for them gets apply_failed.

// Event types of the overlays. The names are the spec's EventType values.
const (
	EventOverlayCreated  = "overlay_created"
	EventOverlayUpdated  = "overlay_updated"
	EventOverlayRemoved  = "overlay_removed"
	EventOverlayExpired  = "overlay_expired"
	EventOverlayOrphaned = "overlay_orphaned"
)

var (
	// ErrOverlayNotFound is returned for an id that is not an active overlay.
	ErrOverlayNotFound = errors.New("engine: no such overlay")
	// ErrOverlayForbidden is returned when a caller that may only touch its own overlays names one
	// that belongs to another owner.
	ErrOverlayForbidden = errors.New("engine: the overlay belongs to another owner")
	// ErrNoConfiguration is returned for an overlay written before any configuration is active:
	// there is nothing to resolve its references against.
	ErrNoConfiguration = errors.New("engine: no configuration is active yet")
	// ErrOverlayNoLease is returned when renewing an overlay that has no lease.
	ErrOverlayNoLease = overlay.ErrNoLease
	// ErrTooManyOverlays is returned when the store is full.
	ErrTooManyOverlays = overlay.ErrFull
)

// UnsupportedOverlayError is returned for an overlay whose kind or fault family is not
// implemented in this build.
type UnsupportedOverlayError struct {
	// What names the kind or family ("rule", "dns", "fault family mtu").
	What string
	// Milestone is the milestone that brings it.
	Milestone string
}

func (e *UnsupportedOverlayError) Error() string {
	return fmt.Sprintf("an overlay of %s is not available in this build; it arrives with milestone %s", e.What, e.Milestone)
}

// CompileError is returned when the target an overlay would produce does not compile (a capacity
// limit, a fault that cannot be turned into a configuration): the write is refused and nothing
// changes.
type CompileError struct{ Problems []compiler.Problem }

func (e *CompileError) Error() string {
	if len(e.Problems) == 0 {
		return "the overlay does not compile"
	}
	return "the overlay does not compile: " + e.Problems[0].Message
}

// SupportedOverlayKinds and SupportedFaultFamilies are what this build implements (GET
// /capabilities): faults of the impairment family; the kinds rule (M9), profile (M11), dns (M20),
// tls (M21), dhcp (M23) and wireguard (M10) follow with their milestones.
var (
	SupportedOverlayKinds  = []model.OverlayKind{model.OverlayKindFault}
	SupportedFaultFamilies = []model.FaultFamily{model.FaultFamilyImpairment}
)

// CheckOverlaySupported returns an *UnsupportedOverlayError for an overlay request that needs a
// milestone this build does not have, nil otherwise (also for a request that has no kind at all:
// validation reports that).
func CheckOverlaySupported(req *model.OverlayRequest) error {
	kind, err := domain.OverlayKindOf(req)
	if err != nil {
		return nil
	}
	switch kind {
	case "fault":
		switch fam := req.Fault.Family; {
		case fam == nil || *fam == model.FaultBodyFamilyImpairment:
			return nil
		case *fam == model.FaultBodyFamilyMtu:
			return &UnsupportedOverlayError{What: "a fault of family mtu", Milestone: "M10"}
		case *fam == model.FaultBodyFamilyTunnel:
			return &UnsupportedOverlayError{What: "a fault of family tunnel", Milestone: "M10"}
		}
	case "profile":
		return &UnsupportedOverlayError{What: "a profile", Milestone: "M11"}
	case "rule":
		return &UnsupportedOverlayError{What: "an access rule", Milestone: "M9"}
	case "wireguard":
		return &UnsupportedOverlayError{What: "a WireGuard action", Milestone: "M10"}
	case "dns":
		return &UnsupportedOverlayError{What: "a DNS fault", Milestone: "M20"}
	case "tls":
		return &UnsupportedOverlayError{What: "a TLS case", Milestone: "M21"}
	case "dhcp":
		return &UnsupportedOverlayError{What: "a DHCP action", Milestone: "M23"}
	}
	return nil
}

// OverlayWrite is a request to create or replace an overlay.
type OverlayWrite struct {
	// Owner owns the overlay: the token, the run or the user that writes it.
	Owner model.Owner
	// Run is set for an overlay a run writes; its owner is then that run.
	Run *model.OverlayRunRef
	// Request is the body as the client sent it (references by name or UUID).
	Request model.OverlayRequest
	// Actor is who caused the change, for the events; the zero value means the owner.
	Actor model.Actor
}

// OverlayResult is what a write, a deletion or a renewal came to.
type OverlayResult struct {
	// Overlay is the overlay after a write or a renewal, and before a deletion.
	Overlay model.Overlay
	// Created is true when a write created a new overlay, false when it replaced one.
	Created bool
	// Generation is the generation the apply loop verified, which contains the change (it can be
	// newer than the one the change made, when later changes were applied together with it).
	Generation uint64
	// Removed counts the overlays a reset removed.
	Removed int
}

type overlayReply struct {
	res OverlayResult
	err error
}

type cmdOverlayPut struct {
	w     OverlayWrite
	reply chan overlayReply
}

type cmdOverlayDelete struct {
	id    uuid.UUID
	by    *model.Owner
	actor model.Actor
	reply chan overlayReply
}

type cmdOverlayRenew struct {
	id    uuid.UUID
	by    *model.Owner
	reply chan overlayReply
}

type cmdOverlayReset struct {
	owner *model.Owner
	actor model.Actor
	reply chan overlayReply
}

// cmdOverlayExpire is sent by the expirer when a TTL or a lease may have run out.
type cmdOverlayExpire struct{}

func (cmdOverlayPut) command()    {}
func (cmdOverlayDelete) command() {}
func (cmdOverlayRenew) command()  {}
func (cmdOverlayReset) command()  {}
func (cmdOverlayExpire) command() {}

// PutOverlay creates an overlay or replaces the one with the same key (owner, kind, target and
// selector; the replacement keeps the id). It returns when the change is applied and verified. A
// request that does not validate (domain.ValidationErrors), that needs a later milestone
// (*UnsupportedOverlayError) or whose target does not compile (*CompileError) changes nothing.
func (e *Engine) PutOverlay(ctx context.Context, w OverlayWrite) (OverlayResult, error) {
	if err := CheckOverlaySupported(&w.Request); err != nil {
		return OverlayResult{}, err
	}
	return e.overlayCmd(ctx, func(reply chan overlayReply) command { return cmdOverlayPut{w: w, reply: reply} })
}

// DeleteOverlay removes an overlay. A non-nil by limits the caller to its own overlays
// (ErrOverlayForbidden otherwise). It returns when the removal is verified.
func (e *Engine) DeleteOverlay(ctx context.Context, id uuid.UUID, by *model.Owner, actor model.Actor) (OverlayResult, error) {
	return e.overlayCmd(ctx, func(reply chan overlayReply) command {
		return cmdOverlayDelete{id: id, by: by, actor: actor, reply: reply}
	})
}

// RenewOverlay restarts the lease of an overlay (ErrOverlayNoLease when it has none). A non-nil by
// limits the caller to its own overlays. Renewing changes nothing in the kernel and makes no
// generation.
func (e *Engine) RenewOverlay(ctx context.Context, id uuid.UUID, by *model.Owner) (OverlayResult, error) {
	return e.overlayCmd(ctx, func(reply chan overlayReply) command { return cmdOverlayRenew{id: id, by: by, reply: reply} })
}

// ResetOverlays removes the overlays of one owner, or all of them when owner is nil. It returns
// when the removal is verified; Removed counts the overlays.
func (e *Engine) ResetOverlays(ctx context.Context, owner *model.Owner, actor model.Actor) (OverlayResult, error) {
	return e.overlayCmd(ctx, func(reply chan overlayReply) command {
		return cmdOverlayReset{owner: owner, actor: actor, reply: reply}
	})
}

// overlayCmd sends a command and waits for the answer. The answer comes when the apply is
// verified, so the caller's context only ends the wait: the change goes on.
func (e *Engine) overlayCmd(ctx context.Context, mk func(chan overlayReply) command) (OverlayResult, error) {
	reply := make(chan overlayReply, 1)
	if err := e.send(ctx, mk(reply)); err != nil {
		return OverlayResult{}, err
	}
	r, err := wait(ctx, e, reply)
	if err != nil {
		return OverlayResult{}, err
	}
	return r.res, r.err
}

// ---- the state owner's part

// overlayWaiter is a writer that waits for a generation to be verified.
type overlayWaiter struct {
	gen   uint64
	reply chan overlayReply
	res   OverlayResult
}

// overlayMark is one change of the store that the kernel has not confirmed yet.
type overlayMark struct {
	gen uint64
	// after is the store's content after the change.
	after *overlay.Checkpoint
	// events are published when the generation is verified: an event announces a state that holds.
	events []overlayEvent
}

type overlayEvent struct {
	typ  string
	data map[string]any
}

// overlayState is the owner's bookkeeping for overlays.
type overlayState struct {
	store   *overlay.Store
	expirer *overlay.Expirer
	// list is the store's content as published in the snapshot and handed to the compiler; rebuilt
	// after every change.
	list []model.Overlay
	// verified is the store's content as of the last verified generation; marks are the changes
	// since, oldest first.
	verified *overlay.Checkpoint
	marks    []overlayMark
	waiters  []overlayWaiter
	// born is the generation in which a fault (by key) first appeared in an applied target: the
	// epoch of its counters.
	born map[string]int64
}

func (o *owner) initOverlays() {
	e := o.e
	st := overlay.New(overlay.Options{Clock: e.cfg.Clock})
	o.ov = &overlayState{store: st, verified: st.Checkpoint(), born: map[string]int64{}}
	o.ov.expirer = overlay.NewExpirer(st, e.cfg.Clock, func() {
		go func() {
			select {
			case e.cmds <- cmdOverlayExpire{}:
			case <-e.ctx.Done():
			}
		}()
	})
}

// refreshOverlays rebuilds the list published in the snapshot and passed to the compiler.
func (o *owner) refreshOverlays() {
	o.ov.list = o.ov.store.Overlays()
	o.snap.Overlays = o.ov.list
}

// liveConfig is the configuration the kernel runs (or is being brought to): what overlays are
// validated against.
func (o *owner) liveConfig() *model.Configuration {
	switch {
	case o.current != nil:
		return o.current.Config
	case o.committed != nil:
		return o.committed.Config
	}
	return nil
}

func (o *owner) liveRevision() int64 {
	switch {
	case o.current != nil:
		return o.current.Revision
	case o.committed != nil:
		return o.committed.Revision
	}
	return 0
}

// discoveredIDs are the discovered devices an overlay may name.
func (o *owner) discoveredIDs() []string {
	var ids []string
	for _, d := range o.snap.Devices {
		if d.Origin == model.DeviceOriginDiscovered {
			ids = append(ids, d.ID)
		}
	}
	return ids
}

func (o *owner) reply(ch chan overlayReply, res OverlayResult, err error) {
	o.later(func() { ch <- overlayReply{res: res, err: err} })
}

// lastCheckpoint is the store's content before the change that is about to be made.
func (o *owner) lastCheckpoint() *overlay.Checkpoint {
	if n := len(o.ov.marks); n > 0 {
		return o.ov.marks[n-1].after
	}
	return o.ov.verified
}

// compileProblems compiles what the kernel would be given with the overlays in the store and
// returns the problems an overlay can cause: a capacity limit, a fault that cannot be compiled.
// The same compile as the apply loop's; a problem of the configuration itself is not the overlay's.
func (o *owner) compileProblems() []compiler.Problem {
	cfg, rev := o.liveConfig(), o.liveRevision()
	var id *domain.Identity
	if o.identity != nil {
		cp := *o.identity
		id = &cp
	}
	tg := compiler.Compile(o.e.input(cfg, o.host, compiler.Generation{Revision: rev, Seq: o.gen + 1}, id, o.ov.list, o.snap.FaultIDs))
	var out []compiler.Problem
	for _, p := range tg.Problems {
		if p.Severity == compiler.SevError && (p.Code == compiler.CodeCapacityExceeded || p.Code == compiler.CodeFaultInvalid) {
			out = append(out, p)
		}
	}
	return out
}

func (o *owner) overlayPut(c cmdOverlayPut) {
	cfg := o.liveConfig()
	if cfg == nil {
		o.reply(c.reply, OverlayResult{}, ErrNoConfiguration)
		return
	}
	req, verrs := domain.ValidateOverlay(cfg, &c.w.Request, domain.WithDiscovered(o.discoveredIDs()...))
	if len(verrs) > 0 {
		o.reply(c.reply, OverlayResult{}, domain.ValidationErrors(verrs))
		return
	}
	before := o.lastCheckpoint()
	gen := o.gen + 1
	ch, err := o.ov.store.Put(c.w.Owner, req, overlay.PutOptions{Generation: int64(gen), Run: c.w.Run})
	if err != nil {
		o.reply(c.reply, OverlayResult{}, err)
		return
	}
	o.refreshOverlays()
	if ps := o.compileProblems(); len(ps) > 0 {
		o.ov.store.Restore(before)
		o.refreshOverlays()
		o.reply(c.reply, OverlayResult{}, &CompileError{Problems: ps})
		return
	}
	actor := c.w.Actor
	if actor.Id == "" {
		actor = c.w.Owner
	}
	o.overlayChanged([]overlay.Change{ch}, actor)
	o.ov.waiters = append(o.ov.waiters, overlayWaiter{gen: o.gen, reply: c.reply,
		res: OverlayResult{Overlay: ch.Overlay, Created: ch.Type == overlay.Created}})
}

func (o *owner) overlayDelete(c cmdOverlayDelete) {
	ov, ok := o.ov.store.Get(c.id)
	switch {
	case !ok:
		o.reply(c.reply, OverlayResult{}, ErrOverlayNotFound)
		return
	case c.by != nil && !overlay.SameOwner(*c.by, ov.Owner):
		o.reply(c.reply, OverlayResult{}, ErrOverlayForbidden)
		return
	}
	ch, err := o.ov.store.Delete(c.id)
	if err != nil {
		o.reply(c.reply, OverlayResult{}, ErrOverlayNotFound)
		return
	}
	o.refreshOverlays()
	o.overlayChanged([]overlay.Change{ch}, c.actor)
	o.ov.waiters = append(o.ov.waiters, overlayWaiter{gen: o.gen, reply: c.reply, res: OverlayResult{Overlay: ch.Overlay}})
}

func (o *owner) overlayRenew(c cmdOverlayRenew) {
	ov, ok := o.ov.store.Get(c.id)
	switch {
	case !ok:
		o.reply(c.reply, OverlayResult{}, ErrOverlayNotFound)
		return
	case c.by != nil && !overlay.SameOwner(*c.by, ov.Owner):
		o.reply(c.reply, OverlayResult{}, ErrOverlayForbidden)
		return
	}
	renewed, err := o.ov.store.Renew(c.id)
	if err != nil {
		o.reply(c.reply, OverlayResult{}, err)
		return
	}
	// a renewal moves a deadline only: the kernel is not touched, no generation is made, but the
	// snapshot shows the new lease time and the expirer waits for the new deadline
	o.refreshOverlays()
	o.ov.expirer.Rearm()
	o.reply(c.reply, OverlayResult{Overlay: renewed, Generation: o.gen}, nil)
}

func (o *owner) overlayReset(c cmdOverlayReset) {
	changes := o.ov.store.Reset(c.owner)
	if len(changes) == 0 {
		o.reply(c.reply, OverlayResult{Generation: o.gen}, nil)
		return
	}
	o.refreshOverlays()
	o.overlayChanged(changes, c.actor)
	o.ov.waiters = append(o.ov.waiters, overlayWaiter{gen: o.gen, reply: c.reply, res: OverlayResult{Removed: len(changes)}})
}

// overlayExpire removes what has run out; nobody waits for it.
func (o *owner) overlayExpire() {
	changes := o.ov.store.Expire()
	if len(changes) == 0 {
		o.ov.expirer.Rearm()
		return
	}
	o.refreshOverlays()
	o.overlayChanged(changes, model.Actor{Type: "system", Id: "system"})
}

// overlayChanged records changes the store has made: a generation, a new desired state, the events
// that follow once it is verified, and the next deadline.
func (o *owner) overlayChanged(changes []overlay.Change, actor model.Actor) {
	cfg, rev := o.liveConfig(), o.liveRevision()
	d := o.nextDesired(cfg, rev)
	mark := overlayMark{gen: d.Generation, after: o.ov.store.Checkpoint()}
	for _, ch := range changes {
		mark.events = append(mark.events, overlayEvent{typ: ch.Event(), data: overlayEventData(ch, d.Generation, actor)})
	}
	o.ov.marks = append(o.ov.marks, mark)
	o.converge(d)
	o.ov.expirer.Rearm()
}

func overlayEventData(ch overlay.Change, gen uint64, actor model.Actor) map[string]any {
	data := map[string]any{
		"generation": gen,
		"overlay":    ch.Overlay.Id.String(),
		"kind":       string(ch.Overlay.Kind),
		"owner":      ch.Overlay.Owner,
		"subject":    Subject{Kind: "overlay", ID: ch.Overlay.Id.String()},
		"actor":      actor,
	}
	if ch.Reason != "" {
		data["reason"] = ch.Reason
	}
	if ch.Overlay.Target != nil {
		data["target"] = *ch.Overlay.Target
	}
	return data
}

// settleOverlays tells the overlay bookkeeping what the apply loop reported. A verified generation
// confirms every change up to it: the writers waiting for those get their answer, with the
// generation that was applied, and the events go out. A failed apply takes the unconfirmed changes
// back: the store returns to its last verified content, every waiting writer gets apply_failed, and
// the caller must converge to the restored state (the result is true). A failure of an apply that
// contains none of the changes is not theirs.
func (o *owner) settleOverlays(r applyResult) (reverted bool) {
	ov := o.ov
	if len(ov.marks) == 0 && len(ov.waiters) == 0 {
		return false
	}
	g := r.d.Generation
	if r.err == nil {
		n := 0
		for n < len(ov.marks) && ov.marks[n].gen <= g {
			for _, ev := range ov.marks[n].events {
				o.event(ev.typ, ev.data)
			}
			ov.verified = ov.marks[n].after
			n++
		}
		ov.marks = ov.marks[n:]
		var keep []overlayWaiter
		for _, w := range ov.waiters {
			if w.gen > g {
				keep = append(keep, w)
				continue
			}
			w.res.Generation = g
			o.reply(w.reply, w.res, nil)
		}
		ov.waiters = keep
		return false
	}
	if len(ov.marks) == 0 || ov.marks[0].gen > g {
		return false
	}
	ov.store.Restore(ov.verified)
	ov.marks = nil
	o.refreshOverlays()
	ov.expirer.Rearm()
	for _, w := range ov.waiters {
		o.reply(w.reply, OverlayResult{}, &ErrApplyFailed{Revision: r.d.Revision, Cause: r.err, Restored: true})
	}
	ov.waiters = nil
	return true
}

// ---- faults, as the snapshot shows them

// trackFaults updates the epochs of the faults' counters after an apply: a fault that is new in the
// applied target starts its counters at zero in this generation, one that went is forgotten.
func (o *owner) trackFaults(t *compiler.Target, gen uint64) {
	born := make(map[string]int64, len(t.Faults))
	for _, f := range t.Faults {
		if g, ok := o.ov.born[f.Key]; ok {
			born[f.Key] = g
		} else {
			born[f.Key] = int64(gen)
		}
	}
	o.ov.born = born
	o.snap.Faults = t.Faults
	o.snap.FaultIDs = t.FaultIDs
	o.snap.FaultEpochs = born
}

// CounterValue is the reading of one named nft counter.
type CounterValue struct {
	Packets, Bytes int64
}

// ReadCounters reads the named counters of the kernel's table: the per-fault counters of the
// classified packets, by counter name.
func (e *Engine) ReadCounters(ctx context.Context) (map[string]CounterValue, error) {
	rs, err := apply.ReadSets(ctx, e.cfg.Exec, e.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	out := map[string]CounterValue{}
	for _, obj := range rs.Objects {
		if c := obj.Counter; c != nil {
			out[c.Name] = CounterValue{Packets: c.Packets, Bytes: c.Bytes}
		}
	}
	return out, nil
}

// RouteFor asks the kernel which route a packet takes from src, arriving on iif (both may be empty)
// to dst: an executor read (`ip route get`), so the policy rules and the tables they select are
// evaluated by the kernel, not re-implemented.
func (e *Engine) RouteFor(ctx context.Context, dst, src, iif string) (*RouteAnswer, error) {
	r, err := apply.ReadRouteGet(ctx, e.cfg.Exec, e.cfg.Namespace, dst, src, iif)
	if err != nil {
		return nil, err
	}
	return &RouteAnswer{Table: r.Table, Gateway: r.Gateway, Interface: r.Dev, Type: r.Type, Unreachable: r.Unreachable, Error: r.Error}, nil
}

// RouteAnswer is the kernel's answer to a route lookup.
type RouteAnswer struct {
	// Table is the routing table the lookup ended in, empty for the main table.
	Table       string
	Gateway     string
	Interface   string
	Type        string
	Unreachable bool
	Error       string
}
