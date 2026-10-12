package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/executor"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// M8a: what applying a revision means for the active overlays (plan §2.1.1).

const deviceFault = "target: {device: esp32-42}\nfault: {latency: 40ms}"

// withoutDevice is the revision that deletes the configured device (and keeps DHCP on).
func withoutDevice(c *model.Configuration) {
	withDHCPAndDevice(c)
	c.Devices = nil
}

// overlaysHarness is the DHCP harness with the first revision (a configured device) applied.
func overlaysHarness(t *testing.T) *harness {
	t.Helper()
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(withDHCPAndDevice))
	h.observe() // the first reading only learns
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	return h
}

func TestARevisionThatDeletesAReferencedObjectIsRefusedAndListsTheReferences(t *testing.T) {
	h := overlaysHarness(t)
	onDevice := h.mustPut(alice, deviceFault)
	h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 10ms}")
	ch, cancel := h.e.Subscribe()
	defer cancel()
	before := h.e.Snapshot()
	rev := h.revision(withoutDevice)

	_, err := h.apply(rev)
	var orphaned *engine.ErrOverlaysOrphaned
	if !errors.As(err, &orphaned) {
		t.Fatalf("got %v", err)
	}
	if len(orphaned.References) != 1 {
		t.Fatalf("references %+v", orphaned.References)
	}
	ref := orphaned.References[0]
	if ref.Overlay.Id != onDevice.Overlay.Id || ref.Object != "/devices/"+devID || ref.Overlay.Owner.Id != alice.Id {
		t.Errorf("reference %+v", ref)
	}
	// nothing changed: no generation, both overlays, the same revision, the candidate is still there
	s := h.barrier()
	if s.Generation != before.Generation || len(s.Overlays) != 2 || s.Revision != before.Revision {
		t.Errorf("the refused apply changed the state: generation %d -> %d, overlays %d, revision %d -> %d",
			before.Generation, s.Generation, len(s.Overlays), before.Revision, s.Revision)
	}
	if r, _, err := h.st.Get(rev); err != nil || string(r.Status) != "candidate" {
		t.Errorf("the revision is %v (%v)", r.Status, err)
	}
	if got := events(ch); has(got, engine.EventOverlayOrphaned) || has(got, engine.EventOverlayRemoved) {
		t.Errorf("events %v", got)
	}
	h.verifyKernelWithOverlays()

	// the preview shows the same references and compiles without the overlay
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.References) != 1 || p.References[0].Overlay.Id != onDevice.Overlay.Id || p.References[0].Object != "/devices/"+devID {
		t.Errorf("preview references %+v", p.References)
	}
	if p.Target.HasErrors() {
		t.Errorf("the preview does not compile without the orphan: %+v", p.Problems)
	}
	for _, f := range p.Target.Faults {
		if f.Source == onDevice.Overlay.Id.String() {
			t.Errorf("the preview still has the fault of the orphan")
		}
	}

	// an unrelated revision is not refused by overlays that refer to something else
	rev2 := h.revision(func(c *model.Configuration) { withDHCPAndDevice(c); c.Settings = &model.Settings{} })
	if _, err := h.apply(rev2); err != nil {
		t.Fatalf("a revision that deletes nothing an overlay refers to: %v", err)
	}
	if got := h.barrier().Overlays; len(got) != 2 {
		t.Errorf("overlays %+v", got)
	}
}

func TestAForcedRevisionRemovesTheOrphanedOverlaysAndEmitsAnEventForEach(t *testing.T) {
	h := overlaysHarness(t)
	onDevice := h.mustPut(alice, deviceFault)
	keep := h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 10ms}")
	ch, cancel := h.e.Subscribe()
	defer cancel()
	rev := h.revision(withoutDevice)

	res, err := h.apply(rev, engine.ApplyOptions{Force: true, Actor: model.Actor{Type: "user", Id: "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "active" && res.Status != "pending_confirm" {
		t.Fatalf("%+v", res)
	}
	if len(res.RemovedOverlays) != 1 || res.RemovedOverlays[0] != onDevice.Overlay.Id {
		t.Errorf("removed %v", res.RemovedOverlays)
	}
	s := h.barrier()
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id {
		t.Fatalf("the overlays are %+v", s.Overlays)
	}
	if len(s.Faults) != 1 || s.Faults[0].Source != keep.Overlay.Id.String() {
		t.Errorf("faults %+v", s.Faults)
	}
	if len(h.kernelFaultChains()) != 1 {
		t.Errorf("the kernel has the chains %v", h.kernelFaultChains())
	}
	h.verifyKernelWithOverlays()

	got := collect(ch, engine.EventOverlayOrphaned)
	if len(got) != 1 {
		t.Fatalf("events %+v", got)
	}
	d := got[0].Data
	if d["overlay"] != onDevice.Overlay.Id.String() || d["generation"].(uint64) != res.Generation {
		t.Errorf("event data %+v (applied generation %d)", d, res.Generation)
	}
	if objs, _ := d["objects"].([]string); len(objs) != 1 || objs[0] != "/devices/"+devID {
		t.Errorf("the event names no deleted object: %+v", d)
	}
	if a, _ := d["actor"].(model.Actor); a.Id != "admin" {
		t.Errorf("actor %+v", d["actor"])
	}
	// the deleted overlay is gone for good
	if _, err := h.e.DeleteOverlay(context.Background(), onDevice.Overlay.Id, nil, model.Actor{}); !errors.Is(err, engine.ErrOverlayNotFound) {
		t.Errorf("deleting the orphan: %v", err)
	}
}

func TestAForcedRevisionThatFailsToApplyBringsTheOrphanedOverlaysBack(t *testing.T) {
	h := overlaysHarness(t)
	onDevice := h.mustPut(alice, deviceFault)
	before := h.e.Snapshot()
	if len(before.Faults) != 1 {
		t.Fatalf("faults %+v", before.Faults)
	}
	chain := compiler.MarkChainName(before.Faults[0].ID)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	rev := h.revision(withoutDevice)

	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		// only the transaction that removes the fault's chain fails; the restore does not
		if argv[0] == "nft" && strings.Contains(stdin, "delete") && strings.Contains(stdin, chain) {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	_, err := h.apply(rev, engine.ApplyOptions{Force: true})
	var af *engine.ErrApplyFailed
	if !errors.As(err, &af) {
		t.Fatalf("got %v", err)
	}
	s := h.barrier()
	h.k.Fail = nil
	if len(s.Overlays) != 1 || s.Overlays[0].Id != onDevice.Overlay.Id {
		t.Fatalf("the overlay of the failed apply is gone: %+v", s.Overlays)
	}
	if s.Revision != before.Revision {
		t.Errorf("revision %d", s.Revision)
	}
	if got := collect(ch, engine.EventOverlayOrphaned); len(got) != 0 {
		t.Errorf("an overlay that was never removed was announced as orphaned: %+v", got)
	}
	h.verifyKernelWithOverlays()
}

func TestAMergeRevisionMovesTheOverlaysOfTheDiscoveredDeviceToTheConfiguredOne(t *testing.T) {
	h := overlaysHarness(t)
	const macRandom = "02:00:00:00:00:aa"
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg), neighbor("10.10.0.50", macRandom)})
	h.observe()
	discID := engine.DeviceID(macRandom)
	if d := h.device(discID); d == nil || d.Origin != model.DeviceOriginDiscovered {
		t.Fatalf("the device is not discovered: %+v", d)
	}
	moved := h.mustPut(alice, "target: {device: "+discID+"}\nfault: {latency: 70ms}")
	other := h.mustPut(alice, "target: {network: Lab}\nfault: {latency: 5ms}")
	ch, cancel := h.e.Subscribe()
	defer cancel()

	// the configured device now covers the randomized MAC
	rev := h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		d := (*c.Devices)[devID]
		macs := []string{macCfg, macRandom}
		d.Identifiers = &model.DeviceIdentifiers{Macs: &macs}
		(*c.Devices)[devID] = d
	})
	// a merge orphans nothing, so it needs no force
	res, err := h.apply(rev)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RemovedOverlays) != 0 {
		t.Errorf("removed %v", res.RemovedOverlays)
	}
	s := h.barrier()
	if len(s.Overlays) != 2 {
		t.Fatalf("overlays %+v", s.Overlays)
	}
	var got *model.Overlay
	for i := range s.Overlays {
		if s.Overlays[i].Id == moved.Overlay.Id {
			got = &s.Overlays[i]
		}
	}
	if got == nil || got.Target == nil || got.Target.Device == nil || *got.Target.Device != devID {
		t.Fatalf("the overlay was not moved to the configured device: %+v", got)
	}
	if !got.CreatedAt.Equal(moved.Overlay.CreatedAt) || got.Generation != int64(res.Generation) {
		t.Errorf("created %v (was %v), generation %d (applied %d)", got.CreatedAt, moved.Overlay.CreatedAt, got.Generation, res.Generation)
	}
	updated := collect(ch, engine.EventOverlayUpdated)
	if len(updated) != 1 || updated[0].Data["overlay"] != moved.Overlay.Id.String() || updated[0].Data["reason"] != "moved" {
		t.Errorf("events %+v", updated)
	}
	if updated[0].Data["target"].(model.Scope).Device == nil || *updated[0].Data["target"].(model.Scope).Device != devID {
		t.Errorf("the event shows the old target: %+v", updated[0].Data["target"])
	}
	// the fault follows the configured device, and the kernel has what the snapshot compiles to
	var fault *compiler.Fault
	for i := range s.Faults {
		if s.Faults[i].Source == moved.Overlay.Id.String() {
			fault = &s.Faults[i]
		}
	}
	if fault == nil {
		t.Fatalf("the moved overlay has no fault: %+v", s.Faults)
	}
	h.verifyKernelWithOverlays()
	// the overlay of another target stays as it was
	for _, ov := range s.Overlays {
		if ov.Id == other.Overlay.Id && (ov.Target.Network == nil || ov.Generation >= int64(res.Generation)) {
			t.Errorf("the unrelated overlay changed: %+v", ov)
		}
	}
	// the owner can still delete it by the id it had
	if _, err := h.e.DeleteOverlay(context.Background(), moved.Overlay.Id, &alice, model.Actor{}); err != nil {
		t.Errorf("%v", err)
	}
}

// P2-AUDIT-05: a commit-confirm rollback settles the overlays like an apply of the committed revision
// would: the overlays of an object only the rolled-back revision had are orphaned, and the moves of its
// merge are undone.

// waitRolledBack waits until the pending revision is gone and the restore has been applied.
func (h *harness) waitRolledBack() *engine.Snapshot {
	h.t.Helper()
	s := h.barrier()
	for i := 0; i < 200 && (s.Pending != nil || s.Applied == nil || !s.Applied.RolledBack); i++ {
		time.Sleep(10 * time.Millisecond)
		s = h.barrier()
	}
	if s.Pending != nil || s.Applied == nil || !s.Applied.RolledBack {
		h.t.Fatalf("not rolled back: pending %+v, applied %+v", s.Pending, s.Applied)
	}
	return s
}

func overlayIDs(l []model.Overlay) []string {
	var out []string
	for _, ov := range l {
		out = append(out, ov.Id.String())
	}
	return out
}

func TestARollbackOrphansTheOverlaysOfADeviceOnlyThePendingRevisionHad(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(nil))
	h.observe()
	keep := h.mustPut(bob, "target: {network: Lab}\nfault: {latency: 10ms}")
	// the pending revision adds the device, with a lockout-relevant change
	r2 := h.revision(func(c *model.Configuration) { withDHCPAndDevice(c); lockoutChange(c) })
	if a, err := h.apply(r2, engine.ApplyOptions{ConfirmTimeout: 30 * time.Second}); err != nil || a.Status != "pending_confirm" {
		t.Fatalf("%+v %v", a, err)
	}
	// in the confirmation window the device exists: it gets its address, and an overlay may name it
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	onDevice := h.mustPut(alice, deviceFault)
	if s := h.barrier(); len(s.Faults) != 2 {
		t.Fatalf("faults %+v", s.Faults)
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()

	// nobody confirms
	h.clk.Advance(31 * time.Second)
	s := h.waitRolledBack()
	if len(s.Overlays) != 1 || s.Overlays[0].Id != keep.Overlay.Id {
		t.Fatalf("the overlays after the rollback are %v", overlayIDs(s.Overlays))
	}
	if len(s.Faults) != 1 || s.Faults[0].Source != keep.Overlay.Id.String() {
		t.Errorf("faults %+v", s.Faults)
	}
	h.verifyKernelWithOverlays()
	got := collect(ch, engine.EventOverlayOrphaned)
	if len(got) != 1 {
		t.Fatalf("orphaned events %+v", got)
	}
	d := got[0].Data
	if d["overlay"] != onDevice.Overlay.Id.String() || d["reason"] != "rollback" || d["generation"].(uint64) != s.Applied.Generation {
		t.Errorf("event %+v (applied %d)", d, s.Applied.Generation)
	}
	if objs, _ := d["objects"].([]string); len(objs) != 1 || objs[0] != "/devices/"+devID {
		t.Errorf("objects %+v", d["objects"])
	}
	if a, _ := d["actor"].(model.Actor); a.Type != "system" {
		t.Errorf("actor %+v", d["actor"])
	}
	if _, err := h.e.DeleteOverlay(context.Background(), onDevice.Overlay.Id, nil, model.Actor{}); !errors.Is(err, engine.ErrOverlayNotFound) {
		t.Errorf("deleting the orphan: %v", err)
	}

	// a later revision that adds the same device does not bring the overlay back to life
	h.mustApply(h.revision(withDHCPAndDevice))
	if s := h.barrier(); len(s.Overlays) != 1 || len(s.Faults) != 1 {
		t.Errorf("overlays %v, faults %+v", overlayIDs(s.Overlays), s.Faults)
	}
}

func TestARollbackMovesTheOverlaysOfARolledBackMergeBackToTheDiscoveredDevice(t *testing.T) {
	h := overlaysHarness(t)
	const macRandom = "02:00:00:00:00:aa"
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg), neighbor("10.10.0.50", macRandom)})
	h.observe()
	discID := engine.DeviceID(macRandom)
	moved := h.mustPut(alice, "target: {device: "+discID+"}\nfault: {latency: 70ms}")
	onConfigured := h.mustPut(bob, deviceFault)

	rev := h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		d := (*c.Devices)[devID]
		macs := []string{macCfg, macRandom}
		d.Identifiers = &model.DeviceIdentifiers{Macs: &macs}
		(*c.Devices)[devID] = d
		lockoutChange(c)
	})
	if a, err := h.apply(rev, engine.ApplyOptions{ConfirmTimeout: 30 * time.Second}); err != nil || a.Status != "pending_confirm" {
		t.Fatalf("%+v %v", a, err)
	}
	target := func(s *engine.Snapshot, id string) string {
		for _, ov := range s.Overlays {
			if ov.Id.String() == id && ov.Target != nil && ov.Target.Device != nil {
				return *ov.Target.Device
			}
		}
		return ""
	}
	if got := target(h.barrier(), moved.Overlay.Id.String()); got != devID {
		t.Fatalf("the merge did not move the overlay: %q", got)
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()

	ctx, cancelCtx := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCtx()
	if err := h.e.Rollback(ctx, rev); err != nil {
		t.Fatal(err)
	}
	s := h.waitRolledBack()
	if got := target(s, moved.Overlay.Id.String()); got != discID {
		t.Fatalf("the overlay targets %q after the rollback, want the discovered device %s", got, discID)
	}
	// the overlay that was written for the configured device stays there
	if got := target(s, onConfigured.Overlay.Id.String()); got != devID {
		t.Errorf("the configured device's overlay targets %q", got)
	}
	updated := collect(ch, engine.EventOverlayUpdated)
	if len(updated) != 1 || updated[0].Data["overlay"] != moved.Overlay.Id.String() || updated[0].Data["reason"] != "moved" {
		t.Fatalf("events %+v", updated)
	}
	if tg := updated[0].Data["target"].(model.Scope); tg.Device == nil || *tg.Device != discID {
		t.Errorf("the event shows the target %+v", tg)
	}
	if got := collect(ch, engine.EventOverlayOrphaned); len(got) != 0 {
		t.Errorf("a merge's rollback orphaned %+v", got)
	}
	// the discovered device is discovered again and its overlay impairs it, not the configured one
	h.observe()
	s = h.barrier()
	if d := h.device(discID); d == nil || d.Origin != model.DeviceOriginDiscovered {
		t.Fatalf("the device is not discovered again: %+v", d)
	}
	var fault *compiler.Fault
	for i := range s.Faults {
		if s.Faults[i].Source == moved.Overlay.Id.String() {
			fault = &s.Faults[i]
		}
	}
	if fault == nil {
		t.Fatalf("the moved-back overlay has no fault: %+v", s.Faults)
	}
	h.verifyKernelWithOverlays()
}

func TestARollbackWhoseApplyFailsStillLeavesTheOrphanRemoved(t *testing.T) {
	h, _ := dhcpHarness(t)
	h.mustApply(h.revision(nil))
	h.observe()
	r2 := h.revision(func(c *model.Configuration) { withDHCPAndDevice(c); lockoutChange(c) })
	if a, err := h.apply(r2, engine.ApplyOptions{ConfirmTimeout: 30 * time.Second}); err != nil || a.Status != "pending_confirm" {
		t.Fatalf("%+v %v", a, err)
	}
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.31", macCfg)})
	h.observe()
	onDevice := h.mustPut(alice, deviceFault)
	s := h.barrier()
	if len(s.Faults) != 1 {
		t.Fatalf("faults %+v", s.Faults)
	}
	chain := compiler.MarkChainName(s.Faults[0].ID)
	ch, cancel := h.e.Subscribe()
	defer cancel()

	// the transaction that removes the fault's chain fails, again and again
	h.k.Fail = func(argv []string, stdin string) *executor.Result {
		if argv[0] == "nft" && strings.Contains(stdin, "delete") && strings.Contains(stdin, chain) {
			return &executor.Result{Exit: 1, Stderr: "Error: injected\n"}
		}
		return nil
	}
	ctx, cancelCtx := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCtx()
	if err := h.e.Rollback(ctx, r2); err != nil {
		t.Fatal(err)
	}
	s = h.barrier()
	if s.LastError == "" {
		t.Fatal("the injected failure did not fail the restore")
	}
	// the overlay does not come back with the failed apply: the rollback holds whatever the kernel does
	if len(s.Overlays) != 0 {
		t.Fatalf("the failed apply brought the orphan back: %v", overlayIDs(s.Overlays))
	}
	got := collect(ch, engine.EventOverlayOrphaned)
	if len(got) != 1 || got[0].Data["overlay"] != onDevice.Overlay.Id.String() || got[0].Data["reason"] != "rollback" {
		t.Fatalf("orphaned events %+v", got)
	}

	// the retry applies the restore without it
	h.k.Fail = nil
	h.clk.Advance(11 * time.Second)
	// (the observation the rollback triggers may converge the identity once more meanwhile: wait until
	// nothing moves any more)
	s = h.barrier()
	for i, last := 0, uint64(0); i < 200 && (s.LastError != "" || s.Generation != last); i++ {
		last = s.Generation
		time.Sleep(10 * time.Millisecond)
		s = h.barrier()
	}
	if s.LastError != "" || len(s.Overlays) != 0 || len(s.Faults) != 0 {
		t.Fatalf("after the retry: error %q, overlays %v, faults %+v", s.LastError, overlayIDs(s.Overlays), s.Faults)
	}
	h.verifyKernelWithOverlays()
	if got := collect(ch, engine.EventOverlayOrphaned); len(got) != 0 {
		t.Errorf("the orphan was announced twice: %+v", got)
	}
}
