package engine

import (
	"reflect"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// LockoutRelevant reports whether applying next on top of cur could lock the administrator out and
// therefore needs confirmation (plan §2.14): a change of the management interface, its allowed
// sources or the UI/API port (gateway protection is compiled from them), of the uplink when the
// management network lives behind it (the two-port topology), or of a WireGuard network with the
// role management. cur may be nil: the first revision has nothing to roll back to.
func LockoutRelevant(cur, next *model.Configuration) bool {
	if cur == nil {
		return false
	}
	if !reflect.DeepEqual(cur.Management, next.Management) {
		return true
	}
	// two-port topology: the management network lies behind the uplink interface
	if sameInterface(next.Management.Interface, next.Uplink.Interface) || sameInterface(cur.Management.Interface, cur.Uplink.Interface) {
		if !reflect.DeepEqual(cur.Uplink, next.Uplink) {
			return true
		}
	}
	return !reflect.DeepEqual(managementNetworks(cur), managementNetworks(next))
}

func sameInterface(a, b model.InterfaceRef) bool {
	if a.Mac != nil && b.Mac != nil && *a.Mac == *b.Mac {
		return true
	}
	return a.Name != nil && b.Name != nil && *a.Name == *b.Name
}

// managementNetworks returns the WireGuard networks with the role management, by id.
func managementNetworks(cfg *model.Configuration) map[string]model.WireGuardNetwork {
	out := map[string]model.WireGuardNetwork{}
	if cfg.Networks == nil {
		return out
	}
	for id, n := range *cfg.Networks {
		wg, err := n.AsWireGuardNetwork()
		if err != nil || wg.Role == nil || string(*wg.Role) != "management" {
			continue
		}
		out[id] = wg
	}
	return out
}
