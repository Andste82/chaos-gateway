package overlay

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

const lat100 = `{target: {device: esp32-42}, fault: {latency: 100ms}}`

func TestAnOverlayGetsAnIdItsOwnerItsKindAndTheGeneration(t *testing.T) {
	f := newFixture(t)
	ch := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, ttl: 5m}`)
	ov := ch.Overlay
	if ch.Type != Created || ch.Event() != "overlay_created" {
		t.Fatalf("change = %+v", ch)
	}
	if ov.Id == uuid.Nil || ov.Kind != "fault" || ov.Owner.Id != "admin" || ov.Generation != 1 {
		t.Fatalf("overlay = %+v", ov)
	}
	if ov.CreatedAt != start || !ov.UpdatedAt.Equal(start) {
		t.Fatalf("time stamps %v %v", ov.CreatedAt, ov.UpdatedAt)
	}
	if ov.ExpiresAt == nil || !ov.ExpiresAt.Equal(start.Add(5*time.Minute)) || ov.LeaseExpiresAt != nil {
		t.Fatalf("expires_at = %v lease %v", ov.ExpiresAt, ov.LeaseExpiresAt)
	}
	got, ok := f.store.Get(ov.Id)
	if !ok || got.Id != ov.Id || *got.Target.Device != idESP {
		t.Fatalf("Get = %+v %v", got, ok)
	}
}

func TestWritingAnOverlayWithTheSameKeyReplacesItAndKeepsTheId(t *testing.T) {
	f := newFixture(t)
	first := f.put(admin, lat100).Overlay
	f.clk.Advance(time.Minute)
	second := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 250ms, jitter: 10ms}}`)
	if second.Type != Updated || second.Reason != ReasonReplaced || second.Event() != "overlay_updated" {
		t.Fatalf("change = %+v", second)
	}
	ov := second.Overlay
	if ov.Id != first.Id {
		t.Fatalf("the id changed: %s -> %s", first.Id, ov.Id)
	}
	if !ov.CreatedAt.Equal(first.CreatedAt) || !ov.UpdatedAt.After(first.UpdatedAt) || ov.Generation != 2 {
		t.Fatalf("created %v updated %v gen %d", ov.CreatedAt, ov.UpdatedAt, ov.Generation)
	}
	if got := *ov.Fault.Latency; got != "250ms" {
		t.Fatalf("the body was not replaced: latency %s", got)
	}
	if f.store.Len() != 1 {
		t.Fatalf("%d overlays", f.store.Len())
	}
}

func TestTheKeyIsOwnerKindTargetAndSelector(t *testing.T) {
	f := newFixture(t)
	base := f.put(admin, lat100).Overlay.Id
	distinct := map[string]Change{
		"another owner":    f.put(token("t1"), lat100),
		"another target":   f.put(admin, `{target: {network: IoT}, fault: {latency: 100ms}}`),
		"another selector": f.put(admin, `{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883], latency: 100ms}}`),
		"another family":   f.put(admin, `{target: {device: esp32-42}, fault: {family: mtu, mtu: {size: 1400}}}`),
		"another kind":     f.put(admin, `{target: {device: esp32-42}, profile: lte}`),
	}
	seen := map[uuid.UUID]string{base: "base"}
	for name, ch := range distinct {
		if ch.Type != Created {
			t.Errorf("%s: %s, want a new overlay", name, ch.Type)
		}
		if other, dup := seen[ch.Overlay.Id]; dup {
			t.Errorf("%s has the id of %s", name, other)
		}
		seen[ch.Overlay.Id] = name
	}
	if f.store.Len() != 6 {
		t.Fatalf("%d overlays, want 6", f.store.Len())
	}
	// the same selector in another spelling is the same key: ports in another order
	a := f.put(admin, `{target: {device: esp32-42}, fault: {protocol: tcp, ports: [8883, 443], loss: 1%}}`)
	b := f.put(admin, `{target: {device: esp32-42}, fault: {protocol: tcp, ports: [443, 8883], loss: 2%}}`)
	if b.Type != Updated || b.Overlay.Id != a.Overlay.Id {
		t.Fatalf("a reordered port list must replace: %+v", b)
	}
}

func TestUpdatedAtIncreasesStrictlyEvenWhenTheClockStandsStillOrJumpsBack(t *testing.T) {
	f := newFixture(t)
	a := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 1ms}}`).Overlay
	b := f.put(admin, `{target: {device: esp32-42}, fault: {protocol: tcp, latency: 2ms}}`).Overlay
	f.clk.JumpWall(-time.Hour) // an NTP step backwards
	c := f.put(admin, `{target: {device: esp32-42}, fault: {protocol: udp, latency: 3ms}}`).Overlay
	if !a.UpdatedAt.Before(b.UpdatedAt) || !b.UpdatedAt.Before(c.UpdatedAt) {
		t.Fatalf("not strictly increasing: %v %v %v", a.UpdatedAt, b.UpdatedAt, c.UpdatedAt)
	}
}

func TestATTLRemovesTheOverlayAndSaysSo(t *testing.T) {
	f := newFixture(t)
	short := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, ttl: 30s}`).Overlay
	long := f.put(admin, `{target: {network: IoT}, fault: {latency: 100ms}, ttl: 5m}`).Overlay
	forever := f.put(admin, `{target: {global: true}, fault: {latency: 1ms}}`).Overlay

	f.clk.Advance(29 * time.Second)
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("expired too early: %v", ids(got))
	}
	f.clk.Advance(time.Second)
	got := f.store.Expire()
	if len(got) != 1 || got[0].Overlay.Id != short.Id || got[0].Type != Expired || got[0].Reason != ReasonTTL || got[0].Event() != "overlay_expired" {
		t.Fatalf("expired = %+v", got)
	}
	if _, ok := f.store.Get(short.Id); ok {
		t.Fatal("the overlay is still there")
	}
	f.clk.Advance(10 * time.Minute)
	got = f.store.Expire()
	if len(got) != 1 || got[0].Overlay.Id != long.Id {
		t.Fatalf("expired = %+v", got)
	}
	if _, ok := f.store.Get(forever.Id); !ok || f.store.Len() != 1 {
		t.Fatal("an overlay without a TTL must stay")
	}
}

func TestALeaseRunsOutUnlessItIsRenewed(t *testing.T) {
	f := newFixture(t)
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, lease: 10s}`).Overlay
	for i := 0; i < 5; i++ {
		f.clk.Advance(9 * time.Second)
		if got := f.store.Expire(); len(got) != 0 {
			t.Fatalf("round %d: expired %v", i, ids(got))
		}
		renewed, err := f.store.Renew(ov.Id)
		if err != nil {
			t.Fatal(err)
		}
		want := f.clk.Now().Add(10 * time.Second)
		if renewed.LeaseExpiresAt == nil || !renewed.LeaseExpiresAt.Equal(want) {
			t.Fatalf("lease_expires_at = %v, want %v", renewed.LeaseExpiresAt, want)
		}
		if !renewed.UpdatedAt.Equal(ov.UpdatedAt) || renewed.Generation != ov.Generation {
			t.Fatal("a renewal is no replacement")
		}
	}
	f.clk.Advance(10 * time.Second) // the owner crashed: no more heartbeats
	got := f.store.Expire()
	if len(got) != 1 || got[0].Type != Expired || got[0].Reason != ReasonLease {
		t.Fatalf("expired = %+v", got)
	}
	if _, err := f.store.Renew(ov.Id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renewing an expired overlay: %v", err)
	}
}

func TestRenewNeedsALease(t *testing.T) {
	f := newFixture(t)
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, ttl: 1m}`).Overlay
	if _, err := f.store.Renew(ov.Id); !errors.Is(err, ErrNoLease) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.store.Renew(uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestTheEarlierOfTTLAndLeaseDecides(t *testing.T) {
	f := newFixture(t)
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, ttl: 20s, lease: 60s}`)
	f.put(admin, `{target: {network: IoT}, fault: {latency: 100ms}, ttl: 60s, lease: 20s}`)
	f.clk.Advance(20 * time.Second)
	got := f.store.Expire()
	if len(got) != 2 || got[0].Reason != ReasonTTL || got[1].Reason != ReasonLease {
		t.Fatalf("expired = %+v", got)
	}
}

func TestAReplacementStartsTheTimersOver(t *testing.T) {
	f := newFixture(t)
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, ttl: 30s}`)
	f.clk.Advance(25 * time.Second)
	again := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 200ms}, ttl: 30s}`).Overlay
	f.clk.Advance(25 * time.Second)
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("the replacement must have a fresh TTL: %v", ids(got))
	}
	// a replacement without a TTL removes the limit
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 300ms}}`)
	f.clk.Advance(time.Hour)
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("expired %v", ids(got))
	}
	if ov, _ := f.store.Get(again.Id); ov.ExpiresAt != nil {
		t.Fatalf("expires_at = %v", ov.ExpiresAt)
	}
}

func TestDeadlinesFollowTheMonotonicClockNotTheWallClock(t *testing.T) {
	f := newFixture(t)
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, ttl: 60s}`).Overlay
	f.clk.JumpWall(2 * time.Hour) // the first NTP sync of a Raspberry Pi
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("a wall-clock jump forward expired %v", ids(got))
	}
	f.clk.JumpWall(-5 * time.Hour)
	f.clk.Advance(59 * time.Second)
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("a wall-clock jump back must not extend the TTL, but %v expired early", ids(got))
	}
	got, _ := f.store.Get(ov.Id)
	if want := f.clk.Now().Add(time.Second); !got.ExpiresAt.Equal(want) {
		t.Fatalf("the displayed expiry is %v, want %v (one second from now)", got.ExpiresAt, want)
	}
	f.clk.Advance(time.Second)
	if got := f.store.Expire(); len(got) != 1 {
		t.Fatalf("expired %v", ids(got))
	}
}

func TestNextDeadline(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.store.NextDeadline(); ok {
		t.Fatal("an empty store has no deadline")
	}
	f.put(admin, `{target: {global: true}, fault: {latency: 1ms}}`)
	if _, ok := f.store.NextDeadline(); ok {
		t.Fatal("an overlay without TTL and lease has no deadline")
	}
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 1ms}, ttl: 90s}`)
	f.put(admin, `{target: {network: IoT}, fault: {latency: 1ms}, lease: 40s}`)
	f.clk.Advance(10 * time.Second)
	if at, ok := f.store.NextDeadline(); !ok || at != 40*time.Second {
		t.Fatalf("next = %v %v", at, ok)
	}
}

func TestResetOnlyTouchesTheCallersOverlays(t *testing.T) {
	f := newFixture(t)
	mine1 := f.put(token("mine"), lat100).Overlay
	mine2 := f.put(token("mine"), `{target: {network: IoT}, fault: {latency: 5ms}}`).Overlay
	other := f.put(token("other"), lat100).Overlay
	admins := f.put(admin, lat100).Overlay
	// a token and a user with the same id are different owners
	sameId := f.put(model.Owner{Type: "user", Id: "mine"}, lat100).Overlay

	owner := token("mine")
	got := f.store.Reset(&owner)
	if len(got) != 2 {
		t.Fatalf("removed %v", ids(got))
	}
	for _, c := range got {
		if c.Type != Removed || c.Reason != ReasonReset || c.Event() != "overlay_removed" {
			t.Errorf("change = %+v", c)
		}
		if c.Overlay.Id != mine1.Id && c.Overlay.Id != mine2.Id {
			t.Errorf("removed %s, which is not the caller's", c.Overlay.Id)
		}
	}
	for _, id := range []uuid.UUID{other.Id, admins.Id, sameId.Id} {
		if _, ok := f.store.Get(id); !ok {
			t.Errorf("%s was removed by somebody else's reset", id)
		}
	}
	if got := f.store.Reset(nil); len(got) != 3 || f.store.Len() != 0 {
		t.Fatalf("reset of all removed %d, %d left", len(got), f.store.Len())
	}
}

func TestDelete(t *testing.T) {
	f := newFixture(t)
	ov := f.put(admin, lat100).Overlay
	ch, err := f.store.Delete(ov.Id)
	if err != nil || ch.Type != Removed || ch.Reason != ReasonDeleted || ch.Overlay.Id != ov.Id {
		t.Fatalf("%+v %v", ch, err)
	}
	if _, err := f.store.Delete(ov.Id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	// the key is free again: a new write is a new overlay
	if again := f.put(admin, lat100); again.Type != Created || again.Overlay.Id == ov.Id {
		t.Fatalf("%+v", again)
	}
}

func TestListFiltersAndKeepsAStableOrder(t *testing.T) {
	f := newFixture(t)
	a := f.put(admin, lat100).Overlay
	f.clk.Advance(time.Second)
	b := f.put(token("t"), `{target: {device: esp32-42}, profile: lte}`).Overlay
	f.clk.Advance(time.Second)
	c := f.put(token("t"), `{target: {network: IoT}, fault: {latency: 1ms}}`).Overlay

	all := f.store.List(Filter{})
	if got := []uuid.UUID{all[0].Id, all[1].Id, all[2].Id}; got[0] != a.Id || got[1] != b.Id || got[2] != c.Id {
		t.Fatalf("order = %v", got)
	}
	if got := f.store.List(Filter{Kind: "profile"}); len(got) != 1 || got[0].Id != b.Id {
		t.Fatalf("kind filter: %v", got)
	}
	owner := token("t")
	if got := f.store.List(Filter{Owner: &owner}); len(got) != 2 {
		t.Fatalf("owner filter: %d", len(got))
	}
	if got := f.store.List(Filter{Kind: "fault", Owner: &owner}); len(got) != 1 || got[0].Id != c.Id {
		t.Fatalf("both filters: %v", got)
	}
}

func TestARestartDropsEveryOverlay(t *testing.T) {
	// Overlays are never persisted: the store of the next process starts empty, whatever the
	// last one held, and nothing but the clock and the options goes into it (plan §2.1.1).
	f := newFixture(t)
	f.put(admin, lat100)
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 1ms}, lease: 30s}`)
	if f.store.Len() == 0 {
		t.Fatal("setup")
	}
	restarted := New(Options{Clock: f.clk})
	if restarted.Len() != 0 || len(restarted.Overlays()) != 0 {
		t.Fatalf("a new store holds %d overlays", restarted.Len())
	}
	if _, ok := restarted.NextDeadline(); ok {
		t.Fatal("a new store has a deadline")
	}
}

func TestTheStoreHoldsAtMostMaxOverlays(t *testing.T) {
	f := newFixture(t)
	f.store = New(Options{Clock: f.clk, Max: 2})
	f.put(admin, lat100)
	f.put(admin, `{target: {network: IoT}, fault: {latency: 1ms}}`)
	req := f.request(`{target: {global: true}, fault: {latency: 1ms}}`)
	if _, err := f.store.Put(admin, req, PutOptions{}); !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v", err)
	}
	// replacing an overlay does not need room
	if ch := f.put(admin, `{target: {network: IoT}, fault: {latency: 9ms}}`); ch.Type != Updated {
		t.Fatalf("%+v", ch)
	}
}

func TestARunOwnsItsOverlays(t *testing.T) {
	f := newFixture(t)
	run := uuid.New()
	owner := model.Owner{Type: "run", Id: run.String()}
	ref := &model.OverlayRunRef{Id: run, Step: "s1"}
	ch, err := f.store.Put(owner, f.request(lat100), PutOptions{Run: ref})
	if err != nil || ch.Overlay.Run == nil || ch.Overlay.Run.Step != "s1" {
		t.Fatalf("%+v %v", ch, err)
	}
	// the same run replaces it from another step: same key, same id
	ref2 := &model.OverlayRunRef{Id: run, Step: "s2"}
	again, err := f.store.Put(owner, f.request(`{target: {device: esp32-42}, fault: {latency: 5ms}}`), PutOptions{Run: ref2})
	if err != nil || again.Overlay.Id != ch.Overlay.Id || again.Overlay.Run.Step != "s2" {
		t.Fatalf("%+v %v", again, err)
	}
	// a run reference needs the run as owner
	if _, err := f.store.Put(admin, f.request(lat100), PutOptions{Run: ref}); err == nil {
		t.Fatal("an overlay of a run cannot be owned by the admin")
	}
	if _, err := f.store.Put(model.Owner{Type: "run", Id: uuid.NewString()}, f.request(lat100), PutOptions{Run: ref}); err == nil {
		t.Fatal("an overlay of a run cannot be owned by another run")
	}
}

func TestAnInvalidWriteChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.put(admin, lat100)
	cases := map[string]struct {
		owner model.Owner
		req   *model.OverlayRequest
	}{
		"two kinds":      {admin, &model.OverlayRequest{Fault: &model.FaultBody{}, Profile: ptrTo("lte")}},
		"unknown owner":  {model.Owner{Type: "robot", Id: "x"}, f.request(`{target: {global: true}, fault: {latency: 1ms}}`)},
		"zero ttl":       {admin, withTTL(f.request(`{target: {global: true}, fault: {latency: 1ms}}`), "0s")},
		"garbage lease":  {admin, withLease(f.request(`{target: {global: true}, fault: {latency: 1ms}}`), "soon")},
		"negative ttl":   {admin, withTTL(f.request(`{target: {global: true}, fault: {latency: 1ms}}`), "-5s")},
		"no kind at all": {admin, &model.OverlayRequest{}},
	}
	for name, c := range cases {
		if _, err := f.store.Put(c.owner, c.req, PutOptions{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if f.store.Len() != 1 {
		t.Fatalf("%d overlays", f.store.Len())
	}
}

func ptrTo[T any](v T) *T { return &v }

func withTTL(r *model.OverlayRequest, d string) *model.OverlayRequest {
	r.Ttl = &d
	return r
}

func withLease(r *model.OverlayRequest, d string) *model.OverlayRequest {
	r.Lease = &d
	return r
}

func TestTheStoreKeepsItsOwnCopyOfARequest(t *testing.T) {
	f := newFixture(t)
	req := f.request(lat100)
	ch, err := f.store.Put(admin, req, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	*req.Fault.Latency = "999ms"
	got, _ := f.store.Get(ch.Overlay.Id)
	if *got.Fault.Latency != "100ms" {
		t.Fatalf("the caller changed an active overlay: %s", *got.Fault.Latency)
	}
}

func TestOrphanRemovesTheNamedOverlaysOnly(t *testing.T) {
	f := newFixture(t)
	a := f.put(admin, lat100).Overlay
	b := f.put(admin, `{target: {network: IoT}, fault: {latency: 1ms}}`).Overlay
	got := f.store.Orphan([]uuid.UUID{a.Id, uuid.New()})
	if len(got) != 1 || got[0].Overlay.Id != a.Id || got[0].Type != Orphaned || got[0].Event() != "overlay_orphaned" {
		t.Fatalf("%+v", got)
	}
	if _, ok := f.store.Get(b.Id); !ok {
		t.Fatal("the other overlay must stay")
	}
}

func TestRetargetMovesAnOverlayAndKeepsItsIdAndAge(t *testing.T) {
	f := newFixture(t)
	discovered := uuid.NewString()
	req := f.request(lat100)
	req.Target = &model.Scope{Device: &discovered}
	put, err := f.store.Put(admin, req, PutOptions{Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Minute)

	got := f.store.Retarget(map[string]string{discovered: idESP}, 7)
	if len(got) != 1 || got[0].Type != Updated || got[0].Reason != ReasonMoved {
		t.Fatalf("%+v", got)
	}
	moved := got[0].Overlay
	if moved.Id != put.Overlay.Id || *moved.Target.Device != idESP || moved.Generation != 7 ||
		!moved.UpdatedAt.Equal(put.Overlay.UpdatedAt) || !moved.CreatedAt.Equal(put.Overlay.CreatedAt) {
		t.Fatalf("moved = %+v", moved)
	}
	// the key follows: writing the same overlay for the configured device replaces the moved one
	if again := f.put(admin, lat100); again.Type != Updated || again.Overlay.Id != put.Overlay.Id {
		t.Fatalf("%+v", again)
	}
	if got := f.store.Retarget(nil, 8); got != nil {
		t.Fatalf("%+v", got)
	}
}

func TestRetargetKeepsTheNewerOfTwoOverlaysThatNowCollide(t *testing.T) {
	f := newFixture(t)
	discovered := uuid.NewString()
	onConfigured := f.put(admin, lat100).Overlay // older, already on the configured device
	f.clk.Advance(time.Second)
	req := f.request(`{target: {device: esp32-42}, fault: {latency: 300ms}}`)
	req.Target = &model.Scope{Device: &discovered}
	onDiscovered, err := f.store.Put(admin, req, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// somebody else's overlay with the same body stays out of it
	bystander := f.put(token("t"), lat100).Overlay

	got := f.store.Retarget(map[string]string{discovered: idESP}, 9)
	if len(got) != 2 || got[0].Type != Removed || got[0].Reason != ReasonMerged || got[0].Overlay.Id != onConfigured.Id ||
		got[1].Type != Updated || got[1].Overlay.Id != onDiscovered.Overlay.Id {
		t.Fatalf("%+v", got)
	}
	if f.store.Len() != 2 {
		t.Fatalf("%d overlays", f.store.Len())
	}
	if _, ok := f.store.Get(bystander.Id); !ok {
		t.Fatal("the bystander is gone")
	}
	kept, _ := f.store.Get(onDiscovered.Overlay.Id)
	if *kept.Fault.Latency != "300ms" || *kept.Target.Device != idESP {
		t.Fatalf("kept = %+v", kept)
	}

	// the other way round: the overlay that is already on the configured device is the newer one
	f2 := newFixture(t)
	d2 := uuid.NewString()
	req2 := f2.request(lat100)
	req2.Target = &model.Scope{Device: &d2}
	old, _ := f2.store.Put(admin, req2, PutOptions{})
	f2.clk.Advance(time.Second)
	newer := f2.put(admin, lat100).Overlay
	got = f2.store.Retarget(map[string]string{d2: idESP}, 3)
	if len(got) != 1 || got[0].Type != Removed || got[0].Overlay.Id != old.Overlay.Id {
		t.Fatalf("%+v", got)
	}
	if _, ok := f2.store.Get(newer.Id); !ok {
		t.Fatal("the newer overlay must stay")
	}
}

func TestTheStoreIsSafeForConcurrentUse(t *testing.T) {
	f := newFixture(t)
	store := New(Options{Clock: f.clk})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := token(fmt.Sprint("w", w))
			for i := 0; i < 50; i++ {
				ch, err := store.Put(owner, f.request(fmt.Sprintf(`{target: {device: esp32-42}, fault: {latency: %dms}, ttl: 1m}`, i+1)), PutOptions{})
				if err != nil {
					t.Error(err)
					return
				}
				store.List(Filter{})
				store.NextDeadline()
				if i%10 == 9 {
					store.Reset(&owner)
				} else if i%7 == 0 {
					_, _ = store.Delete(ch.Overlay.Id)
				}
			}
		}()
	}
	wg.Wait()
}

// A renewal is not part of what a failed apply takes back: the checkpoints a store can be restored
// to are brought along by Renew.
func TestARenewalSurvivesRestoringACheckpointTakenBeforeIt(t *testing.T) {
	f := newFixture(t)
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, lease: 10s}`).Overlay
	before := f.store.Checkpoint()
	f.clk.Advance(8 * time.Second)
	if _, err := f.store.Renew(ov.Id, before); err != nil { // runs out at 18 s
		t.Fatal(err)
	}
	f.store.Restore(before)
	f.clk.Advance(3 * time.Second) // 11 s
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("the restore undid the renewal: %v", ids(got))
	}
	if got, _ := f.store.Get(ov.Id); got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(f.clk.Now().Add(7*time.Second)) {
		t.Fatalf("lease_expires_at = %v", got.LeaseExpiresAt)
	}
	f.clk.Advance(8 * time.Second) // 19 s
	if got := f.store.Expire(); len(got) != 1 || got[0].Reason != ReasonLease {
		t.Fatalf("expired = %+v", got)
	}
}

func TestACheckpointWithoutTheRenewalIsStillOlderThanOne(t *testing.T) {
	// the renewal only moves a deadline later: a checkpoint is never given an earlier one, and an
	// overlay that is not in a checkpoint is not added to it
	f := newFixture(t)
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, lease: 10s}`).Overlay
	empty := New(Options{Clock: f.clk}).Checkpoint()
	f.clk.Advance(5 * time.Second)
	if _, err := f.store.Renew(ov.Id, empty); err != nil {
		t.Fatal(err)
	}
	f.store.Restore(empty)
	if f.store.Len() != 0 {
		t.Fatal("an overlay that was not in the checkpoint came back")
	}
	again := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, lease: 10s}`).Overlay
	first := f.store.Checkpoint()
	f.clk.Advance(5 * time.Second)
	if _, err := f.store.Renew(again.Id, first); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(2 * time.Second)
	if _, err := f.store.Renew(again.Id, first); err != nil { // a second renewal moves it on, never back
		t.Fatal(err)
	}
	f.store.Restore(first)
	f.clk.Advance(9 * time.Second) // 16 s after the write, 9 after the second renewal
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("expired %v", ids(got))
	}
}

// A replaced overlay that is taken back keeps the renewals made to its id in between.
func TestARenewalBeforeAReplacementThatIsTakenBackStays(t *testing.T) {
	f := newFixture(t)
	ov := f.put(admin, `{target: {device: esp32-42}, fault: {latency: 100ms}, lease: 10s}`).Overlay
	before := f.store.Checkpoint()
	f.clk.Advance(8 * time.Second)
	if _, err := f.store.Renew(ov.Id, before); err != nil {
		t.Fatal(err)
	}
	f.put(admin, `{target: {device: esp32-42}, fault: {latency: 200ms}, lease: 4s}`) // replaces; lease 4 s from 8 s
	f.store.Restore(before)
	f.clk.Advance(3 * time.Second) // 11 s
	if got := f.store.Expire(); len(got) != 0 {
		t.Fatalf("expired %v", ids(got))
	}
	if got, _ := f.store.Get(ov.Id); got.Fault == nil || *got.Fault.Latency != "100ms" {
		t.Fatalf("the replacement is still active: %+v", got.Fault)
	}
}
