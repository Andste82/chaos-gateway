package run

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The zero-hit warning (plan §2.10): a fault or rule of the run that matched no packet is reported, because a
// run that "passed" while its fault never hit proves nothing. It is about steps: a step that replaces the
// overlay of an earlier one is judged by what the overlay matched from then on, so `lossy` that replaces
// `slow` is not excused by the packets `slow` matched.

// hitKind reports whether the overlay of a step kind has a counter of packets that matched: faults, rules and
// profiles (whose parts are faults). A WireGuard action changes the tunnel and matches no packets.
func hitKind(kind string) bool { return kind == "fault" || kind == "rule" || kind == "profile" }

// impairsNothing reports whether a step is meant to match nothing: a profile with no parameter in any part, like the
// built-in `normal`, which wins its family so that the faults beneath it stop, and impairs nothing. Such a step has no
// hits to report.
func impairsNothing(cfg *model.Configuration, st *model.Step) bool {
	if st.Profile == nil || cfg == nil {
		return false
	}
	p, _, ok := domain.ProfileByID(cfg, *st.Profile)
	if !ok {
		return false
	}
	return p.Parts.Mtu == nil && p.Parts.Dns == nil && p.Parts.Tls == nil &&
		(p.Parts.Impairment == nil || reflect.DeepEqual(*p.Parts.Impairment, model.ImpairmentParams{}))
}

// count takes a reading of the overlay's counter as the step's own: what the overlay has matched since the
// step took it over. A counter that started over in between (another epoch) is the step's alone.
func (h *stepHits) count(c *model.Counter) {
	h.read = true
	if h.base == nil || h.base.Epoch != c.Epoch {
		h.packets = c.Packets
		return
	}
	h.packets = max(0, c.Packets-h.base.Packets)
}

// zeroHits adds the warning `zero_hit` for every fault, rule or profile step of a run that reached its end
// (`passed` or `failed`) whose overlay matched no packet. With `zero_hit: error` in the scenario a run that
// would have passed is `failed` instead, with the steps as its reason. The counters were read at the last
// capture; a step whose counter could not be read has no verdict.
func (m *Manager) zeroHits(r *runState, status model.RunStatus, reason string) (model.RunStatus, string) {
	if status != model.RunStatusPassed && status != model.RunStatusFailed {
		return status, reason
	}
	position := map[string]int{}
	for pos, idx := range r.order {
		position[r.sc.Steps[idx].Id] = pos
	}
	type zero struct {
		step, kind string
		overlay    uuid.UUID
	}
	var zeros []zero
	for _, id := range r.ownedOrder {
		for _, h := range r.owned[id].steps {
			if hitKind(h.kind) && !h.noop && h.read && h.packets == 0 {
				zeros = append(zeros, zero{h.step, h.kind, id})
			}
		}
	}
	sort.SliceStable(zeros, func(i, j int) bool { return position[zeros[i].step] < position[zeros[j].step] })
	asError := r.sc.ZeroHit != nil && *r.sc.ZeroHit == model.ScenarioZeroHitError
	var names []string
	for _, z := range zeros {
		step, overlay := z.step, z.overlay
		m.addWarning(r, model.RunWarning{Code: model.ZeroHit, Step: &step, Overlay: &overlay,
			Message: fmt.Sprintf("the %s of step %s matched no packet, so the run proves nothing about it", z.kind, z.step)})
		names = append(names, z.step)
	}
	if asError && len(names) > 0 && status == model.RunStatusPassed {
		return model.RunStatusFailed, fmt.Sprintf("zero hits: the overlay of step %s matched no packet (zero_hit: error)", strings.Join(names, ", "))
	}
	return status, reason
}
