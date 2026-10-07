package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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
