package domain

import (
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the DHCP family as the compiler needs it (plan §2.4, §2.7, M23): which action is in force for each
// network and each device. The family competes like the others: the more specific scope wins (a device beats its
// network, levels 4 and 8) and the newer overlay wins at the same scope, so a device has one effective action. What
// each action does to the DHCP server is the compiler's (compiler/dhcpaction.go).

// DHCPActions are the DHCP actions in force: for every network and every device named by an action, the one that wins
// at that scope. An action that lost to a newer one of the same scope is in neither map.
type DHCPActions struct {
	// Networks and Devices map the UUID of a network, or of a device, to its winning action.
	Networks map[string]Candidate
	Devices  map[string]Candidate
}

// DHCPActions resolves the actions that stay in force while their overlay does: short_lease, force_new_ip,
// set_options and silence. delete_lease acts once and is not among them (DHCPDeletions).
func (w *World) DHCPActions() DHCPActions {
	out := DHCPActions{Networks: map[string]Candidate{}, Devices: map[string]Candidate{}}
	var cands []Candidate
	for _, c := range w.candidates() {
		if c.Family == FamilyDHCP {
			cands = append(cands, c)
		}
	}
	RankOrder(cands) // the strongest first: the first of a scope wins it
	for _, c := range cands {
		switch {
		case c.Scope.Device != nil:
			id := lower(*c.Scope.Device)
			if _, won := out.Devices[id]; !won {
				out.Devices[id] = c
			}
		case c.Scope.Network != nil:
			id := lower(*c.Scope.Network)
			if _, won := out.Networks[id]; !won {
				out.Networks[id] = c
			}
		}
	}
	return out
}

// DHCPDeletions are the overlays of the action delete_lease, in the order they were written: each acts once, when
// it is written, and stays in the list until its time is up.
func (w *World) DHCPDeletions() []model.Overlay {
	var out []model.Overlay
	for _, o := range w.Overlays {
		if o.Kind == model.OverlayKindDhcp && o.Dhcp != nil && o.Dhcp.Action == model.DeleteLease && o.Target != nil {
			out = append(out, o)
		}
	}
	return out
}
