package domain

import (
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// A fault beats a profile part on the same scope (E8), and otherwise the newer entry wins (E6).
// Taken as a pairwise order over all candidates of a level this is not transitive: here the
// fault A beats the newer profile part B on the group sensors, B is newer than the fault C on g2,
// and C is newer than A. The winner must not depend on the order the candidates arrive in.
func TestTheWinnerOfALevelDoesNotDependOnTheInputOrder(t *testing.T) {
	tw := newTestWorld(t, false)
	a := tw.overlay(`{target: {group: sensors}, fault: {latency: 10ms}}`, 1*time.Second)
	c := tw.overlay(`{target: {group: g2}, fault: {latency: 30ms}}`, 2*time.Second)
	b := tw.overlay(`{target: {group: sensors}, profile: lte}`, 3*time.Second)
	_ = b

	perms := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	base := append([]model.Overlay(nil), tw.overlays...)
	for _, p := range perms {
		tw.overlays = tw.overlays[:0]
		for _, i := range p {
			tw.overlays = append(tw.overlays, base[i])
		}
		res := tw.world().Resolve(toServer("tcp", 443))
		win := mustWinner(t, res, FamilyImpairment)
		// sensors' champion is the fault A (it beats its own profile part), g2's is C; C is newer
		if win.ID != c.Id.String() {
			t.Fatalf("order %v: winner %s, want %s (c)", p, win.ID, c.Id)
		}
		r := familyResult(t, res, FamilyImpairment)
		if len(r.Overridden) != 2 {
			t.Fatalf("order %v: %d overridden", p, len(r.Overridden))
		}
		if got := overriddenReason(t, r, a.Id.String()); got != "newer at the same level" {
			t.Errorf("order %v: reason for the older fault = %q", p, got)
		}
	}
}

// The profile part of a scope that has a fault of its own never wins on that scope, whatever the
// other scopes at the level look like.
func TestAProfilePartLosesToTheFaultOfItsOwnScopeEvenWhenNewest(t *testing.T) {
	tw := newTestWorld(t, false)
	fault := tw.overlay(`{target: {group: sensors}, fault: {latency: 10ms}}`, 1*time.Second)
	tw.overlay(`{target: {group: sensors}, profile: lte}`, 5*time.Second)
	res := tw.world().Resolve(toServer("tcp", 443))
	win := mustWinner(t, res, FamilyImpairment)
	if win.ID != fault.Id.String() || win.IsProfilePart() {
		t.Fatalf("winner = %+v", win)
	}
	r := familyResult(t, res, FamilyImpairment)
	if len(r.Overridden) != 1 || r.Overridden[0].Reason != "a fault beats a profile part at the same scope" {
		t.Fatalf("overridden = %+v", r.Overridden)
	}
}
