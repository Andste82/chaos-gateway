package engine_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Profiles in the engine (plan M11): an activation is an overlay of kind profile, written, replaced,
// expired and orphaned like any other, and it follows the definition of its profile.

const (
	profFlaky  = "6d7e8f9a-0b1c-4d2e-8f3a-4b5c6d7e8f9a"
	profSlowNS = "7e8f9a0b-1c2d-4e3f-8a4b-5c6d7e8f9a0b"
	profGlitch = "8f9a0b1c-2d3e-4f4a-9b5c-6d7e8f9a0b1c"
)

func impairmentProfile(name string, p model.ImpairmentParams) model.Profile {
	return model.Profile{Name: name, Parts: model.ProfileParts{Impairment: &p}}
}

// withProfiles is a revision with the DHCP network and the configured device of overlaysHarness and
// these custom profiles.
func withProfiles(ps map[string]model.Profile) func(*model.Configuration) {
	return func(c *model.Configuration) {
		withDHCPAndDevice(c)
		c.Profiles = &ps
	}
}

func profileFaults(t *testing.T, s *engine.Snapshot, overlay string) []compiler.Fault {
	t.Helper()
	var out []compiler.Fault
	for _, f := range s.Faults {
		if f.Source == overlay {
			out = append(out, f)
		}
	}
	return out
}

// An activation reaches the kernel, and writing the activation again with another profile switches it:
// the same overlay (200 instead of 201), the new parameters, a verified kernel at every step.
func TestAProfileActivationIsAppliedAndSwitchingItKeepsTheOverlay(t *testing.T) {
	h := startedWithRevision(t)
	ch, cancel := h.e.Subscribe()
	defer cancel()

	res := h.mustPut(alice, "target: {network: IoT}\nprofile: lte")
	if !res.Created || res.Overlay.Kind != model.OverlayKindProfile {
		t.Fatalf("%+v", res)
	}
	id := res.Overlay.Id.String()
	fs := profileFaults(t, h.e.Snapshot(), id)
	if len(fs) != 1 || fs[0].Profile != "lte" || fs[0].Upload.Delay != 50*time.Millisecond || fs[0].Upload.Loss != 0.1 {
		t.Fatalf("faults %+v", fs)
	}
	lteID := fs[0].ID
	h.verifyKernelWithOverlays()

	res = h.mustPut(alice, "target: {network: IoT}\nprofile: satellite")
	if res.Created || res.Overlay.Id.String() != id {
		t.Fatalf("switching made a new overlay: %+v", res)
	}
	fs = profileFaults(t, h.e.Snapshot(), id)
	if len(fs) != 1 || fs[0].Profile != "satellite" || fs[0].Upload.Delay != 600*time.Millisecond || fs[0].ID != lteID {
		t.Fatalf("faults %+v (the leaf is changed in place, the id stays %d)", fs, lteID)
	}
	h.verifyKernelWithOverlays()

	// a profile with a rate takes a queue per device: another fault id, the old one is retired
	h.mustPut(alice, "target: {network: IoT}\nprofile: bad-lte")
	fs = profileFaults(t, h.e.Snapshot(), id)
	if len(fs) == 0 || fs[0].Profile != "bad-lte" || fs[0].Upload.Rate != 2_000_000 {
		t.Fatalf("faults %+v", fs)
	}
	h.verifyKernelWithOverlays()

	// another owner's activation on the same target is a second overlay: owner and target are the key
	other := h.mustPut(bob, "target: {network: IoT}\nprofile: lte")
	if !other.Created || other.Overlay.Id.String() == id {
		t.Errorf("%+v", other)
	}
	if _, err := h.e.DeleteOverlay(context.Background(), other.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, &alice, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	if s := h.e.Snapshot(); len(s.Faults) != 0 || len(s.Overlays) != 0 {
		t.Errorf("faults %+v overlays %+v", s.Faults, s.Overlays)
	}
	h.verifyKernelWithOverlays()
	var got []engine.Event
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			got = append(got, ev)
		default:
			break drain
		}
	}
	var created, updated, removed int
	for _, e := range got {
		switch e.Type {
		case engine.EventOverlayCreated:
			created++
		case engine.EventOverlayUpdated:
			updated++
			if e.Data["reason"] != "replaced" {
				t.Errorf("%+v", e.Data)
			}
		case engine.EventOverlayRemoved:
			removed++
		}
	}
	if created != 2 || updated != 2 || removed != 2 {
		t.Errorf("created %d updated %d removed %d: %+v", created, updated, removed, got)
	}
}

// The expiry of an activation: a TTL removes the profile like any overlay.
func TestAProfileActivationExpiresWithItsTTL(t *testing.T) {
	h := startedWithRevision(t)
	h.mustPut(alice, "target: {network: IoT}\nprofile: lte\nttl: 5m")
	if len(h.e.Snapshot().Faults) != 1 {
		t.Fatal("not applied")
	}
	h.clk.Advance(5*time.Minute + time.Second)
	s := h.waitOverlays(0)
	if len(s.Faults) != 0 {
		t.Errorf("%+v", s.Faults)
	}
	h.verifyKernelWithOverlays()
}

// A profile with a part of a later milestone is stored but cannot be activated: the whole activation is
// refused with the milestone, and nothing changes. The built-in dns-broken and tls-broken are the same.
func TestAProfileWithAPartOfALaterMilestoneIsRefusedAndNothingChanges(t *testing.T) {
	h := newHarness(t)
	h.start()
	h.mustApply(h.revision(func(c *model.Configuration) {
		c.Profiles = &map[string]model.Profile{
			profSlowNS: {Name: "slow-dns-lte", Parts: model.ProfileParts{
				Impairment: &model.ImpairmentParams{Latency: ptr("50ms")},
				Dns:        &model.DnsFault{Action: "delay", Delay: ptr("2s")},
			}},
			profGlitch: {Name: "glitch", Parts: model.ProfileParts{Tls: &model.TlsCase{Case: "expired"}}},
		}
	}))
	gen := h.e.Snapshot().Generation
	for body, milestone := range map[string]string{
		"target: {network: IoT}\nprofile: slow-dns-lte": "M20",
		"target: {network: IoT}\nprofile: glitch":       "M21",
		"target: {network: IoT}\nprofile: dns-broken":   "M20",
		"target: {network: IoT}\nprofile: tls-broken":   "M21",
	} {
		_, err := h.put(alice, body)
		var ue *engine.UnsupportedOverlayError
		if !errors.As(err, &ue) || ue.Milestone != milestone {
			t.Errorf("%q: got %v, want unsupported until %s", body, err, milestone)
		} else if !strings.Contains(ue.Error(), "profile") {
			t.Errorf("the error does not name the profile: %v", ue)
		}
	}
	if s := h.e.Snapshot(); len(s.Overlays) != 0 || s.Generation != gen {
		t.Errorf("a refused activation changed the state: %d overlays, generation %d -> %d", len(s.Overlays), gen, s.Generation)
	}
	// an unknown profile is a validation error, not an unsupported feature
	_, err := h.put(alice, "target: {network: IoT}\nprofile: nope")
	var ve domain.ValidationErrors
	if !errors.As(err, &ve) || len(ve) == 0 || ve[0].Path != "/profile" {
		t.Errorf("got %v", err)
	}
}

// Plan §2.1.1: "An active profile activation follows the new profile definition." The same overlay, a new
// revision that changes the profile, the new parameters in the kernel with the old fault id.
func TestAnActiveProfileFollowsItsNewDefinition(t *testing.T) {
	h := overlaysHarness(t)
	h.mustApply(h.revision(withProfiles(map[string]model.Profile{
		profFlaky: impairmentProfile("flaky", model.ImpairmentParams{Latency: ptr("100ms"), Loss: ptr("2%")}),
	})))
	res := h.mustPut(alice, "target: {network: IoT}\nprofile: flaky\nttl: 1h")
	id := res.Overlay.Id.String()
	before := profileFaults(t, h.e.Snapshot(), id)
	if len(before) != 1 || before[0].Upload.Delay != 100*time.Millisecond || before[0].Upload.Loss != 2 {
		t.Fatalf("%+v", before)
	}
	ch, cancel := h.e.Subscribe()
	defer cancel()

	h.mustApply(h.revision(withProfiles(map[string]model.Profile{
		profFlaky: impairmentProfile("flaky", model.ImpairmentParams{Latency: ptr("300ms"), Jitter: ptr("40ms")}),
	})))
	s := h.barrier()
	after := profileFaults(t, s, id)
	if len(after) != 1 || after[0].ID != before[0].ID || after[0].Upload.Delay != 300*time.Millisecond || after[0].Upload.Jitter != 40*time.Millisecond || after[0].Upload.Loss != 0 {
		t.Fatalf("%+v", after)
	}
	if len(s.Overlays) != 1 || s.Overlays[0].Id != res.Overlay.Id || s.Overlays[0].ExpiresAt == nil {
		t.Errorf("the activation did not survive the revision: %+v", s.Overlays)
	}
	if got := events(ch); has(got, engine.EventOverlayRemoved) || has(got, engine.EventOverlayOrphaned) {
		t.Errorf("events %v", got)
	}
	h.verifyKernelWithOverlays()

	// renaming the profile keeps the activation too: it refers to the UUID
	h.mustApply(h.revision(withProfiles(map[string]model.Profile{
		profFlaky: impairmentProfile("renamed", model.ImpairmentParams{Latency: ptr("300ms"), Jitter: ptr("40ms")}),
	})))
	if fs := profileFaults(t, h.barrier(), id); len(fs) != 1 || fs[0].Profile != "renamed" {
		t.Errorf("%+v", fs)
	}
}

// Plan §2.1.1: a revision that deletes an object referenced by an active overlay is rejected with the
// references; with force the orphaned overlays are removed and each announces itself. A profile is such an
// object; the built-in ones cannot be deleted and so never orphan anything.
func TestARevisionThatDeletesAnActivatedProfileIsRefusedUnlessForced(t *testing.T) {
	h := overlaysHarness(t)
	custom := map[string]model.Profile{
		profFlaky:  impairmentProfile("flaky", model.ImpairmentParams{Latency: ptr("100ms")}),
		profGlitch: impairmentProfile("spare", model.ImpairmentParams{Loss: ptr("1%")}),
	}
	h.mustApply(h.revision(withProfiles(custom)))
	on := h.mustPut(alice, "target: {network: IoT}\nprofile: flaky")
	builtin := h.mustPut(bob, "target: {network: Lab}\nprofile: lte")
	ch, cancel := h.e.Subscribe()
	defer cancel()
	before := h.e.Snapshot()

	// deleting the profile nobody activated is fine
	h.mustApply(h.revision(withProfiles(map[string]model.Profile{profFlaky: custom[profFlaky]})))
	if len(h.barrier().Overlays) != 2 {
		t.Fatalf("%+v", h.barrier().Overlays)
	}

	rev := h.revision(withProfiles(nil))
	_, err := h.apply(rev)
	var orphaned *engine.ErrOverlaysOrphaned
	if !errors.As(err, &orphaned) || len(orphaned.References) != 1 {
		t.Fatalf("got %v", err)
	}
	if ref := orphaned.References[0]; ref.Overlay.Id != on.Overlay.Id || ref.Object != "/profiles/"+profFlaky || ref.Overlay.Owner.Id != alice.Id {
		t.Errorf("reference %+v", ref)
	}
	if s := h.barrier(); len(s.Overlays) != 2 || s.Generation < before.Generation {
		t.Errorf("the refused apply changed the overlays: %+v", s.Overlays)
	}
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.References) != 1 || p.References[0].Object != "/profiles/"+profFlaky || p.Target.HasErrors() {
		t.Errorf("preview references %+v, problems %+v", p.References, p.Problems)
	}
	for _, e := range p.Target.Effective {
		if e.Source == on.Overlay.Id.String() {
			t.Errorf("the preview still shows the orphan: %+v", e)
		}
	}

	res, err := h.apply(rev, engine.ApplyOptions{Force: true, Actor: model.Actor{Type: "user", Id: "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RemovedOverlays) != 1 || res.RemovedOverlays[0] != on.Overlay.Id {
		t.Errorf("removed %v", res.RemovedOverlays)
	}
	s := h.barrier()
	if len(s.Overlays) != 1 || s.Overlays[0].Id != builtin.Overlay.Id || len(s.Faults) != 1 {
		t.Fatalf("overlays %+v faults %+v", s.Overlays, s.Faults)
	}
	h.verifyKernelWithOverlays()
	got := collect(ch, engine.EventOverlayOrphaned)
	if len(got) != 1 || got[0].Data["overlay"] != on.Overlay.Id.String() {
		t.Fatalf("events %+v", got)
	}
	if objs, _ := got[0].Data["objects"].([]string); len(objs) != 1 || objs[0] != "/profiles/"+profFlaky {
		t.Errorf("the event names no deleted object: %+v", got[0].Data)
	}
}

// Explain and the preview show the profile as the origin of the effective parameters, and a fault on the
// same scope as what beats it (E8): the profile part is overridden, not merged.
func TestExplainAndThePreviewNameTheProfileAsTheOrigin(t *testing.T) {
	h := overlaysHarness(t)
	h.mustApply(h.revision(withProfiles(map[string]model.Profile{
		profFlaky: impairmentProfile("flaky", model.ImpairmentParams{Latency: ptr("100ms")}),
	})))
	prof := h.mustPut(alice, "target: {network: IoT}\nprofile: bad-lte")
	q := engine.ExplainQuery{Device: "esp32-42", Dst: "203.0.113.50", Protocol: "tcp", Port: 8883}

	ex := h.explain(q)
	w := impairment(t, ex)
	if w.Winner == nil || w.Winner.Profile == nil || *w.Winner.Profile.Name != "bad-lte" || w.Winner.Id != prof.Overlay.Id || w.Winner.Summary == nil || !strings.Contains(*w.Winner.Summary, "rate 2Mbit") {
		t.Fatalf("%+v", w.Winner)
	}
	if ex.Kernel == nil || ex.Kernel.FaultID == 0 {
		t.Errorf("no kernel section: %+v", ex.Kernel)
	}

	fault := h.mustPut(alice, "target: {device: esp32-42}\nfault: {latency: 300ms}")
	w = impairment(t, h.explain(q))
	if w.Winner == nil || w.Winner.Id != fault.Overlay.Id || w.Winner.Profile != nil {
		t.Fatalf("%+v", w.Winner)
	}
	if len(w.Overridden) != 1 || w.Overridden[0].Profile == nil || *w.Overridden[0].Profile.Name != "bad-lte" || w.Overridden[0].Reason == nil || !strings.Contains(*w.Overridden[0].Reason, "level 4 beats level 8") {
		t.Fatalf("%+v", w.Overridden)
	}
	// another device of the network still has the profile
	if w := impairment(t, h.explain(engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.99"), Dst: "203.0.113.50"})); w.Winner == nil || w.Winner.Profile == nil {
		t.Errorf("%+v", w.Winner)
	}

	// the preview of a revision lists the winners with the profile each part comes from
	rev := h.revision(withProfiles(map[string]model.Profile{
		profFlaky:  impairmentProfile("flaky", model.ImpairmentParams{Latency: ptr("100ms")}),
		profGlitch: impairmentProfile("glitch", model.ImpairmentParams{Loss: ptr("9%")}),
	}))
	p, err := h.e.Preview(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	var sawProfile, sawFault bool
	for _, e := range p.Target.Effective {
		switch e.Source {
		case prof.Overlay.Id.String():
			sawProfile = e.Profile == "bad-lte" && e.Scope == "network IoT" && strings.Contains(e.Summary, "rate 2Mbit")
		case fault.Overlay.Id.String():
			sawFault = e.Profile == "" && e.Summary == "latency 300ms"
		}
	}
	if !sawProfile || !sawFault {
		t.Errorf("%+v", p.Target.Effective)
	}
}

func impairment(t *testing.T, ex *engine.Explanation) engine.ExplainFamily {
	t.Helper()
	for _, f := range ex.Faults {
		if f.Family == domain.FamilyImpairment {
			return f
		}
	}
	t.Fatalf("no impairment in %+v", ex.Faults)
	return engine.ExplainFamily{}
}
