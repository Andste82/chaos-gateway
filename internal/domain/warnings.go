package domain

import (
	"fmt"
	"sort"

	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Warning codes of the semantic checks: a configuration with such a warning is valid and is applied,
// but it may behave differently from what its author expects. The preview lists them in `warnings`
// with the path of the field concerned. The codes are stable, like the error codes.
const (
	// CodeFixedIPInPool is the warning for a device's fixed address (a DHCP reservation) that lies inside
	// a DHCP pool of its network. Kea allows it (in-pool reservations are its default), but while another
	// client holds the address from the pool, the device gets a different pool address until that lease
	// is released: the other client is refused only when it renews (#76, plan §2.7).
	CodeFixedIPInPool = "fixed_ip_in_pool"
	// CodeFixedIPDHCPOff is the warning for a device's fixed address in a network whose DHCP is off (no
	// scope, or a scope switched off): the address stays in the configuration, but no reservation is made
	// and nothing hands it out until DHCP is turned on in the network (#94, plan §2.7).
	CodeFixedIPDHCPOff = "fixed_ip_dhcp_off"
)

// Warning is something about a valid configuration that may surprise: it does not stop the apply.
type Warning struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Warnings checks a configuration for what is allowed but may surprise (see the Code constants above
// Warning). It expects a configuration that passed Validate; whatever does not parse or resolve is
// left to the validation and gives no warning. The warnings are sorted by path.
func Warnings(cfg *model.Configuration) []Warning {
	idx, _ := BuildIndex(cfg)
	var out []Warning
	for _, id := range sortedKeys(idx.Devices) {
		if w, ok := fixedIPInPool(idx, idx.Devices[id]); ok {
			out = append(out, w)
		}
		if w, ok := fixedIPDHCPOff(idx, idx.Devices[id]); ok {
			out = append(out, w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// fixedIPDHCPOff reports a device's fixed address in a local test network with DHCP off: the compiler
// makes no reservation for it, whether or not another network serves DHCP (#94).
func fixedIPDHCPOff(idx *Index, d *DeviceInfo) (Warning, bool) {
	if d.Origin != OriginConfigured || d.Device == nil || d.Device.FixedIp == nil || d.Network == "" {
		return Warning{}, false
	}
	ip, ok := parseAddr(*d.Device.FixedIp)
	if !ok {
		return Warning{}, false
	}
	nid, ok := idx.Resolve(KindNetwork, d.Network)
	if !ok {
		return Warning{}, false
	}
	n := idx.Networks[nid]
	if !n.IsLan() || (n.Lan.Dhcp != nil && (n.Lan.Dhcp.Enabled == nil || *n.Lan.Dhcp.Enabled)) {
		return Warning{}, false
	}
	return Warning{
		Path: d.Path + "/fixed_ip",
		Code: CodeFixedIPDHCPOff,
		Message: fmt.Sprintf("device %s has the fixed address %s, but DHCP is off in its network %s: no reservation is made "+
			"until DHCP is turned on there", quote(d.Name), ip, quote(n.Name)),
	}, true
}

// fixedIPInPool reports a device's fixed address that lies inside a DHCP pool of its network: a
// configured pool, or the default pool (the second half of the subnet, split around the gateway's address
// when it lies inside, #110) when the network names none, as the compiler hands it to Kea. A network with DHCP off makes no reservation, so there is nothing to warn.
func fixedIPInPool(idx *Index, d *DeviceInfo) (Warning, bool) {
	if d.Origin != OriginConfigured || d.Device == nil || d.Device.FixedIp == nil || d.Network == "" {
		return Warning{}, false
	}
	ip, ok := parseAddr(*d.Device.FixedIp)
	if !ok {
		return Warning{}, false
	}
	nid, ok := idx.Resolve(KindNetwork, d.Network)
	if !ok {
		return Warning{}, false
	}
	n := idx.Networks[nid]
	if !n.IsLan() || n.Lan.Dhcp == nil || (n.Lan.Dhcp.Enabled != nil && !*n.Lan.Dhcp.Enabled) {
		return Warning{}, false
	}
	gw, ok := parsePrefix(n.Lan.Address)
	if !ok || !gw.Masked().Contains(ip) {
		return Warning{}, false
	}
	var pools []kea.Pool
	what := func(i int) string { return fmt.Sprintf("pool %d", i) }
	if ps := deref(n.Lan.Dhcp.Pools); len(ps) > 0 {
		for _, p := range ps {
			from, ok1 := parseAddr(p.Start)
			to, ok2 := parseAddr(p.End)
			if !ok1 || !ok2 {
				return Warning{}, false
			}
			pools = append(pools, kea.Pool{Start: from, End: to})
		}
	} else if ps := kea.DefaultPool(gw, gw.Addr()); len(ps) > 0 {
		pools = ps
		what = func(int) string { return "the default pool" }
	}
	for i, p := range pools {
		if p.Start.Compare(ip) <= 0 && ip.Compare(p.End) <= 0 {
			return Warning{
				Path: d.Path + "/fixed_ip",
				Code: CodeFixedIPInPool,
				Message: fmt.Sprintf("the fixed address %s of device %s lies in %s (%s-%s) of network %s: while another client holds "+
					"that address the device gets a different one from the pool, until that lease is released",
					ip, quote(d.Name), what(i), p.Start, p.End, quote(n.Name)),
			}, true
		}
	}
	return Warning{}, false
}
