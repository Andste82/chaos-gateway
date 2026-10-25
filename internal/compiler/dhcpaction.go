package compiler

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file is the DHCP family (plan §2.4, §2.7, M23): the test actions are overlays that change what the DHCP server
// does, so they resolve into its configuration. The configuration of Kea (compileKea) is what the actions are laid over:
//
//   - short_lease: the lease time of the network's subnet, with the bounds a client may ask within set to it (a client that
//     asks for an hour is given the short lease); for a device a lease class of its MAC addresses, which Kea applies before
//     the subnet's time.
//   - set_options: the network's options with the fields of the action replaced; for a device the same in its reservation,
//     which Kea applies before the subnet's.
//   - silence: the clients of the network's interface (except the devices that another action takes out of the silence), or
//     the MAC addresses of the device, belong to Kea's class DROP, which it does not answer.
//   - force_new_ip: the device's reservation, which the gateway made for the device's first MAC (§2.7), moves to the address
//     the overlay names: the client that renews is refused and asks again.
//   - delete_lease: acts once and is no state, so it is not part of the configuration; the target lists it (KeaTarget.Deletions)
//     for the engine, which deletes the leases when the DHCP server has taken the configuration.
//
// The precedence is the one of the other families (domain.World.DHCPActions): the more specific scope wins and a device has one
// effective action, so an action of the network does not apply to a device that has an action of its own. The
// configuration the actions are laid over is the configured one, so a device that is taken out of an action of its network
// gets the configured lease time or the configured options explicitly (a class, a reservation).

// CodeDHCPInactive is the problem code of a DHCP action that changes nothing: its network has DHCP off or its device cannot
// be reached by the DHCP server (it has no MAC address, or no network).
const CodeDHCPInactive = "dhcp_action_inactive"

// LeaseDeletion is a deletion of leases that the target asks for once: the leases of one subnet, or only those of some
// clients of it.
type LeaseDeletion struct {
	// Key identifies the deletion: the overlay and the time it was written. A deletion is done once for each key.
	Key     string `json:"key"`
	Overlay string `json:"overlay"`
	// Network is the UUID of the network, Subnet the Kea subnet id of its scope.
	Network string `json:"network"`
	Subnet  int    `json:"subnet"`
	// Device is the UUID of the device when the deletion is a device's, with its MAC addresses; both empty for a network's.
	Device string   `json:"device,omitempty"`
	MACs   []string `json:"macs,omitempty"`
}

// dhcpDevice is what the DHCP server can know of a device: its network and its MAC addresses.
type dhcpDevice struct {
	network string
	macs    []string
}

// compileDHCPActions lays the DHCP actions of the overlays over the configuration of Kea. A request without overlays of the kind
// changes nothing.
func (t *Target) compileDHCPActions(in Input, idx *domain.Index) {
	if !hasDHCPOverlay(in.Overlays) {
		return
	}
	cfg := in.Config
	if !domain.IsNormalized(cfg) {
		n, errs := domain.Normalize(cfg)
		if len(errs) > 0 {
			return // a configuration with dangling references has no actions to resolve
		}
		cfg = n
	}
	w, err := domain.NewWorld(cfg, in.Overlays)
	if err != nil {
		t.errorf(CodeFaultInvalid, "", "the DHCP actions cannot be resolved: %v", err)
		return
	}
	acts, dels := w.DHCPActions(), w.DHCPDeletions()
	known := func(id string) (dhcpDevice, bool) {
		if d, ok := idx.Devices[id]; ok && d.Device != nil && d.Device.Identifiers != nil && d.Device.Identifiers.Macs != nil {
			return dhcpDevice{network: d.Network, macs: lowerAll(*d.Device.Identifiers.Macs)}, true
		}
		if in.Identity != nil {
			for _, d := range in.Identity.Discovered {
				if d.ID == id {
					return dhcpDevice{network: d.Network, macs: lowerAll(d.MACs)}, true
				}
			}
		}
		return dhcpDevice{}, false
	}
	byNet := map[string]int{} // network UUID -> index of its subnet in the configuration
	if t.Kea != nil {
		for i, s := range t.Kea.Config.Subnets {
			byNet[s.Network] = i
		}
	}
	win := func(c domain.Candidate) {
		t.Winners = append(t.Winners, c.Key())
		t.Effective = append(t.Effective, EffectiveFault{Key: c.Key(), Layer: string(c.Layer), Source: c.ID, Family: c.Family,
			Scope: describeScope(idx, c.Scope), Summary: c.Summary()})
	}
	inactive := func(c domain.Candidate, why string) {
		t.warn(CodeDHCPInactive, "", "the DHCP action (%s) of %s has no effect: %s", c.Summary(), describeScope(idx, c.Scope), why)
	}

	if t.Kea == nil {
		// no network serves DHCP: no action can reach anybody
		for _, nid := range sortedStrings(acts.Networks) {
			inactive(acts.Networks[nid], "no network serves DHCP")
		}
		for _, did := range sortedStrings(acts.Devices) {
			inactive(acts.Devices[did], "no network serves DHCP")
		}
	}
	var cfgNew *kea.Config
	if t.Kea != nil {
		c := t.Kea.Config
		c.Subnets = append([]kea.Subnet(nil), c.Subnets...)
		cfgNew = &c
	}
	if cfgNew != nil {
		a := &dhcpLayer{cfg: cfgNew, byNet: byNet, classes: map[string]*kea.LeaseClass{}, idx: idx}
		for _, nid := range sortedStrings(acts.Networks) {
			c := acts.Networks[nid]
			i, ok := byNet[nid]
			if !ok {
				inactive(c, "DHCP is off on the network")
				continue
			}
			if !a.network(i, c) {
				inactive(c, "the action cannot be applied to a network")
				continue
			}
			win(c)
		}
		netAct := func(nid string) (domain.Candidate, bool) { c, ok := acts.Networks[nid]; return c, ok }
		for _, did := range sortedStrings(acts.Devices) {
			c := acts.Devices[did]
			dev, ok := known(did)
			switch i, served := byNet[dev.network]; {
			case !ok || len(dev.macs) == 0:
				inactive(c, "the device has no MAC address the DHCP server could know it by")
			case !served:
				inactive(c, "DHCP is off on the network of the device")
			default:
				nc, hasNet := netAct(dev.network)
				if !a.device(i, c, dev, nc, hasNet) {
					inactive(c, "the action cannot be applied to the device")
					continue
				}
				win(c)
			}
		}
		a.finish()
	}

	// the deletions act once and are no state: the target lists them for the engine
	sort.Slice(dels, func(i, j int) bool {
		if !dels[i].UpdatedAt.Equal(dels[j].UpdatedAt) {
			return dels[i].UpdatedAt.Before(dels[j].UpdatedAt)
		}
		return dels[i].Id.String() < dels[j].Id.String()
	})
	for _, o := range dels {
		c := domain.Candidate{Family: domain.FamilyDHCP, Layer: domain.LayerOverlay, ID: o.Id.String(), Scope: *o.Target, DHCP: o.Dhcp}
		del := LeaseDeletion{Key: fmt.Sprintf("%s@%d", o.Id, o.UpdatedAt.UnixNano()), Overlay: o.Id.String()}
		switch {
		case o.Target.Network != nil:
			del.Network = strings.ToLower(*o.Target.Network)
		case o.Target.Device != nil:
			del.Device = strings.ToLower(*o.Target.Device)
			dev, ok := known(del.Device)
			if !ok || len(dev.macs) == 0 {
				inactive(c, "the device has no MAC address the DHCP server could know it by")
				continue
			}
			del.Network, del.MACs = dev.network, dev.macs
		default:
			continue
		}
		i, ok := byNet[del.Network]
		if !ok {
			inactive(c, "DHCP is off on the network")
			continue
		}
		del.Subnet = t.Kea.Config.Subnets[i].ID
		t.Kea.Deletions = append(t.Kea.Deletions, del)
		t.Winners = append(t.Winners, "overlay:"+del.Overlay+":dhcp")
		t.Effective = append(t.Effective, EffectiveFault{Key: "overlay:" + del.Overlay + ":dhcp", Layer: "overlay", Source: del.Overlay, Family: domain.FamilyDHCP,
			Scope: describeScope(idx, *o.Target), Summary: c.Summary()})
	}
	sort.Strings(t.Winners)
	sort.Slice(t.Effective, func(i, j int) bool { return t.Effective[i].Key < t.Effective[j].Key })
	if t.Kea != nil && cfgNew != nil {
		text, err := cfgNew.Render()
		if err != nil {
			t.errorf(CodeDHCP, "", "the DHCP configuration cannot be generated: %v", err)
			return
		}
		t.Kea.Config, t.Kea.Text = *cfgNew, text
	}
}

// dhcpLayer lays the actions over a copy of the Kea configuration.
type dhcpLayer struct {
	cfg   *kea.Config
	idx   *domain.Index
	byNet map[string]int
	// base are the subnets as configured, before an action of the network changed them
	base map[int]kea.Subnet
	// classes are the lease classes by name, collecting the devices that need the same lease time
	classes map[string]*kea.LeaseClass
	// silence is the silence of each network that an action silences, by subnet index
	silence map[int]*kea.Silence
	// devices are the MAC addresses silenced by an action of the device
	devices []string
}

func (a *dhcpLayer) was(i int) kea.Subnet {
	if a.base == nil {
		a.base = map[int]kea.Subnet{}
	}
	if s, ok := a.base[i]; ok {
		return s
	}
	s := a.cfg.Subnets[i]
	a.base[i] = s
	return s
}

// network applies the action of a network to its subnet; false when the action cannot apply to a network.
func (a *dhcpLayer) network(i int, c domain.Candidate) bool {
	a.was(i)
	s := &a.cfg.Subnets[i]
	switch c.DHCP.Action {
	case model.ShortLease:
		secs, ok := leaseSeconds(c.DHCP)
		if !ok {
			return false
		}
		s.LeaseSeconds, s.LeaseMin, s.LeaseMax = secs, secs, secs
	case model.SetOptions:
		s.SetOptions(applyOptions(s.Options(), c.DHCP.Options))
	case model.Silence:
		if a.silence == nil {
			a.silence = map[int]*kea.Silence{}
		}
		a.silence[i] = &kea.Silence{Interface: s.Interface}
	default:
		return false // force_new_ip moves a device's reservation; the validation refuses it for a network
	}
	return true
}

// device applies the action of a device. netAct is the action that wins for the device's network, when there is one:
// it does not apply to the device (the device's action wins), so what it changed is put back for the device.
func (a *dhcpLayer) device(i int, c domain.Candidate, dev dhcpDevice, netAct domain.Candidate, hasNet bool) bool {
	base := a.was(i)
	s := &a.cfg.Subnets[i]
	mine := c.DHCP.Action
	switch mine {
	case model.Silence:
		a.devices = append(a.devices, dev.macs...)
		return true
	case model.ShortLease:
		secs, ok := leaseSeconds(c.DHCP)
		if !ok {
			return false
		}
		a.lease(dev.macs, secs, secs, secs)
	case model.ForceNewIp:
		if c.DHCP.Ip == nil {
			return false // the engine fills the address in when the action is written
		}
		ip, err := netip.ParseAddr(*c.DHCP.Ip)
		if err != nil || !s.Subnet.Contains(ip) {
			return false
		}
		r := reservationOf(s, dev.macs[0])
		r.IP = ip
	case model.SetOptions:
		r := reservationOf(s, dev.macs[0])
		r.Options = applyOptions(base.Options(), c.DHCP.Options)
	default:
		return false
	}
	if !hasNet || netAct.DHCP.Action == mine {
		return true
	}
	// what the network's action changed and the device's does not: the device keeps the configured one
	switch netAct.DHCP.Action {
	case model.ShortLease:
		a.lease(dev.macs, base.LeaseSeconds, base.LeaseMin, base.LeaseMax)
	case model.SetOptions:
		if mine != model.SetOptions {
			r := reservationOf(s, dev.macs[0])
			r.Options = base.Options()
		}
	case model.Silence:
		if sl := a.silence[i]; sl != nil {
			sl.Except = append(sl.Except, dev.macs...)
		}
	}
	return true
}

// lease puts the MAC addresses into the lease class of this lease time and these bounds (zero: those of a subnet).
func (a *dhcpLayer) lease(macs []string, secs, min, max int) {
	name := fmt.Sprintf("cgw-lease-%d-%d-%d", secs, min, max)
	c := a.classes[name]
	if c == nil {
		c = &kea.LeaseClass{Name: name, Seconds: secs, Min: min, Max: max}
		a.classes[name] = c
	}
	c.MACs = append(c.MACs, macs...)
}

// finish writes the classes and the silence into the configuration.
func (a *dhcpLayer) finish() {
	for _, name := range sortedStrings(a.classes) {
		c := *a.classes[name]
		c.MACs = uniqueSorted(c.MACs)
		a.cfg.LeaseClasses = append(a.cfg.LeaseClasses, c)
	}
	var idxs []int
	for i := range a.silence {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		sl := *a.silence[i]
		sl.Except = uniqueSorted(sl.Except)
		a.cfg.Silenced = append(a.cfg.Silenced, sl)
	}
	if len(a.devices) > 0 {
		a.cfg.Silenced = append(a.cfg.Silenced, kea.Silence{MACs: uniqueSorted(a.devices)})
	}
}

// reservationOf returns the reservation of a MAC address in a subnet, adding an empty one: Kea refuses two for one client.
// The subnet's reservations are copied first, so the configuration the target started from is not changed.
func reservationOf(s *kea.Subnet, mac string) *kea.Reservation {
	mac = strings.ToLower(mac)
	for i := range s.Reservations {
		if strings.EqualFold(s.Reservations[i].MAC, mac) {
			s.Reservations = append([]kea.Reservation(nil), s.Reservations...)
			return &s.Reservations[i]
		}
	}
	s.Reservations = append(append([]kea.Reservation(nil), s.Reservations...), kea.Reservation{MAC: mac})
	return &s.Reservations[len(s.Reservations)-1]
}

// leaseSeconds is the lease time of a short_lease action, in whole seconds (at least one).
func leaseSeconds(d *model.DhcpAction) (int, bool) {
	if d.LeaseTime == nil {
		return 0, false
	}
	t, err := time.ParseDuration(*d.LeaseTime)
	if err != nil || t < time.Second {
		return 0, false
	}
	return int(t / time.Second), true
}

// applyOptions is the options with the fields of the action's replaced: a list that is empty, like an option that is not
// there, replaces nothing (as in the scope of a network).
func applyOptions(base kea.Options, o *model.DhcpOptions) kea.Options {
	out := base
	if o == nil {
		return out
	}
	addrs := func(in *[]string) []netip.Addr {
		var as []netip.Addr
		for _, s := range deref(in) {
			if a, err := netip.ParseAddr(s); err == nil {
				as = append(as, a)
			}
		}
		return as
	}
	if o.Router != nil {
		if a, err := netip.ParseAddr(*o.Router); err == nil {
			out.Router = a
		}
	}
	if as := addrs(o.DnsServers); len(as) > 0 {
		out.DNS = as
	}
	if as := addrs(o.NtpServers); len(as) > 0 {
		out.NTP = as
	}
	if o.Domain != nil {
		out.Domain = *o.Domain
	}
	if o.Custom != nil && len(*o.Custom) > 0 {
		out.Custom = nil
		for _, c := range *o.Custom {
			out.Custom = append(out.Custom, kea.CustomOption{Code: c.Code, Value: c.Value})
		}
	}
	return out
}

func hasDHCPOverlay(os []model.Overlay) bool {
	for _, o := range os {
		if o.Kind == model.OverlayKindDhcp {
			return true
		}
	}
	return false
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

func uniqueSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	n := 0
	for i, s := range out {
		if i == 0 || s != out[i-1] {
			out[n] = s
			n++
		}
	}
	return out[:n]
}

func sortedStrings[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
