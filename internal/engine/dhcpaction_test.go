package engine_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/kea"
	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

const labNetID = "1c8d7b4f-2a3e-4d6c-8b9f-8e7d6c5b4a32"

// The DHCP test actions in the engine (plan §2.7, M23): the overlays reach the DHCP server's configuration, force_new_ip is given
// an address when it is written, and delete_lease deletes leases once.

// dhcpActionHarness is an engine with a fake DHCP server, the network IoT (10.10.0.0/24, DHCP on, the default pool 10.10.0.128-254)
// and the device esp32-42 on the MAC macCfg with a fixed address.
func dhcpActionHarness(t *testing.T) (*harness, *fakeDHCP, int) {
	t.Helper()
	h, f := dhcpHarness(t)
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		d := (*c.Devices)[devID]
		d.FixedIp, d.Network = ptr("10.10.0.31"), ptr("IoT")
		(*c.Devices)[devID] = d
	}))
	sid := 0
	for id := range h.e.Snapshot().KeaNetworks {
		sid = id
	}
	h.observe()
	return h, f, sid
}

func (f *fakeDHCP) setLeases(l ...kea.Lease) { f.mu.Lock(); f.leases = l; f.mu.Unlock() }

func lease(ip, mac string, sid int) kea.Lease {
	return kea.Lease{IP: ip, MAC: mac, SubnetID: sid, ValidLft: 600, CLTT: time.Now().Unix()}
}

func TestADHCPActionReachesTheServersConfigurationAndLeavesWithItsOverlay(t *testing.T) {
	h, f, _ := dhcpActionHarness(t)
	res := h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: short_lease, lease_time: 7s}")
	if !res.Created || res.Overlay.Kind != model.OverlayKindDhcp {
		t.Fatalf("%+v", res)
	}
	s := f.last().Config.Subnets[0]
	if s.LeaseSeconds != 7 || s.LeaseMin != 7 || s.LeaseMax != 7 {
		t.Errorf("the server is not configured with the short lease: %+v", s)
	}
	if got := h.e.Snapshot().Overlays; len(got) != 1 || got[0].Dhcp == nil {
		t.Errorf("%+v", got)
	}
	// the overlay's own write is the generation the server was configured for
	if snap := h.e.Snapshot(); snap.DHCPError != "" || snap.Applied == nil || snap.Applied.Generation < res.Generation {
		t.Errorf("%+v", snap.DHCPError)
	}
	// the action goes: the server runs the configured lease time again
	if _, err := h.e.DeleteOverlay(t.Context(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	if s := f.last().Config.Subnets[0]; s.LeaseSeconds != 3600 || s.LeaseMin != 0 {
		t.Errorf("%+v", s)
	}
	// a device's silence and a network's options at once
	h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: silence}")
	h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: set_options, options: {domain: x.test}}")
	last := f.last().Config
	if len(last.Silenced) != 1 || len(last.Silenced[0].MACs) != 1 || last.Silenced[0].MACs[0] != macCfg || last.Subnets[0].Domain != "x.test" {
		t.Errorf("%+v", last)
	}
	if got := h.e.Snapshot().Overlays; len(got) != 2 {
		t.Errorf("%+v", got)
	}
}

func TestForceNewIPGivesTheDeviceAFreeAddressFromTheEndOfThePoolAndAvoidsWhatIsTaken(t *testing.T) {
	h, f, sid := dhcpActionHarness(t)
	// .254 is leased to another client; .253 is the fixed address of another device
	f.setLeases(lease("10.10.0.254", "02:00:00:00:00:99", sid), lease("10.10.0.150", macCfg, sid))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.150", macCfg)})
	h.observe()
	macs := []string{"02:00:00:00:00:41"}
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		(*c.Devices)[devID] = withFixed((*c.Devices)[devID], "10.10.0.31")
		(*c.Devices)["bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"] = withFixed(model.Device{Name: "other", Identifiers: &model.DeviceIdentifiers{Macs: &macs}}, "10.10.0.253")
	}))
	res := h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}")
	ip := derefS(res.Overlay.Dhcp.Ip)
	if ip != "10.10.0.252" {
		t.Fatalf("the device is moved to %q, want the last address of the pool that nothing holds (10.10.0.252)", ip)
	}
	// the server's reservation of the device's first MAC is the address, replacing the fixed one
	subnet := f.last().Config.Subnets[0]
	var got string
	for _, r := range subnet.Reservations {
		if strings.EqualFold(r.MAC, macCfg) {
			got = r.IP.String()
		}
	}
	if got != ip {
		t.Errorf("the reservation is %q, want %s", got, ip)
	}
	// the overlay reads with the address
	if o := h.e.Snapshot().Overlays; len(o) != 1 || derefS(o[0].Dhcp.Ip) != ip {
		t.Errorf("%+v", o)
	}
	// written again while the device has not taken the address: the same one; once the device holds it, another
	if again := h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}"); again.Created || derefS(again.Overlay.Dhcp.Ip) != ip || again.Overlay.Id != res.Overlay.Id {
		t.Errorf("a write that replaces it: %+v", again)
	}
	f.setLeases(lease("10.10.0.254", "02:00:00:00:00:99", sid), lease(ip, macCfg, sid))
	h.k.SetNeighbors([]linux.Neighbor{neighbor(ip, macCfg)})
	h.observe()
	if next := h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}"); derefS(next.Overlay.Dhcp.Ip) != "10.10.0.251" {
		t.Errorf("the device that holds the address is moved on: %s", derefS(next.Overlay.Dhcp.Ip))
	}
}

func withFixed(d model.Device, ip string) model.Device {
	d.FixedIp, d.Network = &ip, ptr("IoT")
	return d
}

func derefS(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestForceNewIPChecksTheAddressItIsGivenAndTheDeviceItMoves(t *testing.T) {
	h, f, sid := dhcpActionHarness(t)
	f.setLeases(lease("10.10.0.200", "02:00:00:00:00:99", sid))
	h.observe()
	for name, body := range map[string]string{
		"an address outside the network":  "target: {device: esp32-42}\ndhcp: {action: force_new_ip, ip: 192.0.2.7}",
		"the gateway's own address":       "target: {device: esp32-42}\ndhcp: {action: force_new_ip, ip: 10.10.0.1}",
		"the broadcast address":           "target: {device: esp32-42}\ndhcp: {action: force_new_ip, ip: 10.10.0.255}",
		"an address another client holds": "target: {device: esp32-42}\ndhcp: {action: force_new_ip, ip: 10.10.0.200}",
	} {
		_, err := h.put(alice, body)
		var ve domain.ValidationErrors
		if !errors.As(err, &ve) || len(ve) != 1 || ve[0].Path != "/dhcp/ip" || ve[0].Code != domain.CodeInvalidDHCPAction {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(h.e.Snapshot().Overlays) != 0 {
		t.Error("a refused write left an overlay")
	}
	res := h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip, ip: 10.10.0.77}")
	if derefS(res.Overlay.Dhcp.Ip) != "10.10.0.77" {
		t.Errorf("an address inside the network, outside the pool, is the one asked for: %+v", res.Overlay.Dhcp)
	}
	// the same address again is no conflict with the overlay it replaces
	if _, err := h.put(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip, ip: 10.10.0.77}"); err != nil {
		t.Error(err)
	}

	// DHCP off on the network
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		n := (*c.Networks)[iotID]
		lan, _ := n.AsLanNetwork()
		off := false
		lan.Dhcp = &model.DhcpScope{Enabled: &off}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
		(*c.Devices)[devID] = withFixed((*c.Devices)[devID], "10.10.0.31")
	}))
	_, err := h.put(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}")
	var ve domain.ValidationErrors
	if !errors.As(err, &ve) || len(ve) != 1 || ve[0].Path != "/target" || !strings.Contains(ve[0].Message, "DHCP is off") {
		t.Errorf("%v", err)
	}
}

func TestForceNewIPHasNoAddressLeftToGiveInAFullPool(t *testing.T) {
	h, f, _ := dhcpActionHarness(t)
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		n := (*c.Networks)[iotID]
		lan, _ := n.AsLanNetwork()
		lan.Dhcp = &model.DhcpScope{Pools: &[]model.DhcpPool{{Start: "10.10.0.100", End: "10.10.0.101"}}}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
		(*c.Devices)[devID] = withFixed((*c.Devices)[devID], "10.10.0.31")
	}))
	sid := 0
	for id := range h.e.Snapshot().KeaNetworks {
		sid = id
	}
	f.setLeases(lease("10.10.0.100", "02:00:00:00:00:98", sid), lease("10.10.0.101", "02:00:00:00:00:99", sid))
	h.observe()
	_, err := h.put(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}")
	var ve domain.ValidationErrors
	if !errors.As(err, &ve) || len(ve) != 1 || !strings.Contains(ve[0].Message, "no address of the pools") {
		t.Errorf("%v", err)
	}
}

// A network whose gateway splits the pool in two (or that names several pools) takes the address from the last pool, and
// goes back to the one before it when the last is taken: "going down from the end of the last pool".
func TestForceNewIPTakesTheLastAddressOfTheLastPoolAndGoesBackToThePoolBeforeIt(t *testing.T) {
	h, f, _ := dhcpActionHarness(t)
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		n := (*c.Networks)[iotID]
		lan, _ := n.AsLanNetwork()
		lan.Dhcp = &model.DhcpScope{Pools: &[]model.DhcpPool{{Start: "10.10.0.100", End: "10.10.0.140"}, {Start: "10.10.0.200", End: "10.10.0.201"}}}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
		(*c.Devices)[devID] = withFixed((*c.Devices)[devID], "10.10.0.31")
	}))
	sid := 0
	for id := range h.e.Snapshot().KeaNetworks {
		sid = id
	}
	res := h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}")
	if ip := derefS(res.Overlay.Dhcp.Ip); ip != "10.10.0.201" {
		t.Fatalf("the device is moved to %q, want the last address of the last pool (10.10.0.201)", ip)
	}
	f.setLeases(lease("10.10.0.201", "02:00:00:00:00:98", sid), lease("10.10.0.200", "02:00:00:00:00:99", sid))
	h.observe()
	res = h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}")
	if ip := derefS(res.Overlay.Dhcp.Ip); ip != "10.10.0.140" {
		t.Errorf("the device is moved to %q, want the last address of the pool before when the last is taken (10.10.0.140)", ip)
	}
}

// Plan M23 "a device fault stays attached after the forced new IP", without the real kernel: the device is the MAC, so the address it
// takes after the move replaces the old one in the identity map and the fault of the device is still in force for it. (The same on the
// real kernel, with a measured delay: TestADeviceFaultStaysAttachedAfterTheForcedNewIPOnTheRealKernel.)
func TestADeviceFaultStaysAttachedAfterTheForcedNewIPAndTheDeviceFollowsItsAddress(t *testing.T) {
	h, f, sid := dhcpActionHarness(t)
	f.setLeases(lease("10.10.0.150", macCfg, sid))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.150", macCfg)})
	h.observe()
	h.mustPut(alice, "target: {device: esp32-42}\nfault: {latency: 150ms}")
	h.barrier()
	faults := h.e.Snapshot().Faults
	// the identity map holds the configured fixed address beside the one the device was seen at
	if len(faults) != 1 || h.deviceSetElements(devID) != "10.10.0.150,10.10.0.31" {
		t.Fatalf("setup: %d faults, the identity map holds %q", len(faults), h.deviceSetElements(devID))
	}

	res := h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}")
	moved := derefS(res.Overlay.Dhcp.Ip)
	// the client renewed, was refused and took the reserved address
	f.setLeases(lease(moved, macCfg, sid))
	h.k.SetNeighbors([]linux.Neighbor{neighbor(moved, macCfg)})
	h.observe()
	h.barrier()
	d := h.device(devID)
	if d == nil || !slices.ContainsFunc(d.Addresses, func(a netip.Addr) bool { return a.String() == moved }) || slices.ContainsFunc(d.Addresses, func(a netip.Addr) bool { return a.String() == "10.10.0.150" }) {
		t.Fatalf("the device after the move: %+v", d)
	}
	if got := h.deviceSetElements(devID); !strings.Contains(got, moved) || strings.Contains(got, "10.10.0.150") {
		t.Errorf("the identity map holds %q for the device, want its new address %s and no longer the old one", got, moved)
	}
	after := h.e.Snapshot().Faults
	if len(after) != 1 || after[0].ID != faults[0].ID {
		t.Errorf("the fault of the device after the move: %+v, before %+v", after, faults)
	}
}

// Plan §2.3 (P5-AUDIT-01): the DNS-derived set elements of a device follow it to the address the forced new IP gave it, through the
// apply loop: the device keeps its cached answer, so its connections to the broker keep the hostname fault without a new query, and the
// old address, once it is not the device's any more, holds nothing (a device given it later must not inherit the answers).
func TestTheDNSDerivedElementsOfADeviceFollowItAfterTheForcedNewIP(t *testing.T) {
	h, f, sid := dhcpActionHarness(t)
	f.setLeases(lease("10.10.0.150", macCfg, sid))
	h.k.SetNeighbors([]linux.Neighbor{neighbor("10.10.0.150", macCfg)})
	h.observe()
	h.mustPut(alice, "target: {device: esp32-42}\nfault: {latency: 150ms, destination: {hostname: broker.example.com}}")
	sets := h.barrier().DNSSets
	if len(sets) != 1 {
		t.Fatalf("%+v", sets)
	}
	set := sets[0]
	if _, err := h.e.RecordResolutions(context.Background(), []engine.HostResolution{{Set: set.ID, Client: netip.MustParseAddr("10.10.0.150"),
		Name: "broker.example.com", TTL: time.Minute, Addrs: []netip.Addr{netip.MustParseAddr("203.0.113.7")}}}); err != nil {
		t.Fatal(err)
	}
	holds := func(client string) bool {
		for _, el := range h.k.SetElements(set.Name) {
			if strings.Contains(el, `"`+client+`"`) && strings.Contains(el, `"203.0.113.7"`) {
				return true
			}
		}
		return false
	}
	if !holds("10.10.0.150") {
		t.Fatalf("setup: the set holds %v", h.k.SetElements(set.Name))
	}

	res := h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: force_new_ip}")
	moved := derefS(res.Overlay.Dhcp.Ip)
	f.setLeases(lease(moved, macCfg, sid))
	h.k.SetNeighbors([]linux.Neighbor{neighbor(moved, macCfg)})
	h.observe()
	h.barrier()
	if !holds(moved) {
		t.Errorf("the element did not follow the device to %s: the set holds %v", moved, h.k.SetElements(set.Name))
	}
	if holds("10.10.0.150") {
		t.Errorf("the old address still holds the element: %v", h.k.SetElements(set.Name))
	}
	if got := h.e.HostNames(netip.MustParseAddr(moved), netip.MustParseAddr("203.0.113.7")); len(got) != 1 || got[0] != "broker.example.com" {
		t.Errorf("the names of the new address: %v", got)
	}
}

// An action whose configuration the DHCP server refuses (an option it cannot parse, a lease time it does not take) is refused when it
// is written: the server would keep the configuration it has, and the retry would send the same one for ever, so no later
// change of a network or another action would reach the server.
func TestAnActionTheDHCPServerRefusesIsRefusedWhenItIsWritten(t *testing.T) {
	h, f, _ := dhcpActionHarness(t)
	f.mu.Lock()
	f.testFn = func(tg *compiler.KeaTarget) error {
		for _, s := range tg.Config.Subnets {
			if s.Domain == "bad.test" {
				return &kea.Error{Command: "config-test", Result: 1, Text: "option data is not valid"}
			}
		}
		return nil
	}
	f.mu.Unlock()
	applies := f.calls()
	_, err := h.put(alice, "target: {network: IoT}\ndhcp: {action: set_options, options: {domain: bad.test}}")
	var ce *engine.CompileError
	if !errors.As(err, &ce) || len(ce.Problems) != 1 || ce.Problems[0].Code != compiler.CodeDHCP || !strings.Contains(ce.Problems[0].Message, "option data is not valid") {
		t.Fatalf("%v", err)
	}
	if got := h.e.Snapshot().Overlays; len(got) != 0 || f.calls() != applies {
		t.Errorf("a refused action was kept or sent: %+v, %d applies after %d", got, f.calls(), applies)
	}
	// the server takes another action, and the one that was refused did not stop it
	h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: set_options, options: {domain: ok.test}}")
	if s := f.last().Config.Subnets[0]; s.Domain != "ok.test" {
		t.Errorf("%+v", s)
	}
}

// A server that is down, or that refuses the configured configuration as well, is no reason to refuse an action: the first takes it
// when it is back, the second did not refuse because of the action.
func TestAnActionIsNotRefusedForADownServerOrAConfigurationTheServerRefusesWithoutIt(t *testing.T) {
	h, f, _ := dhcpActionHarness(t)
	f.mu.Lock()
	f.testFn = func(tg *compiler.KeaTarget) error {
		if tg.Config.Subnets[0].LeaseSeconds == 7 { // down for the check of the action, and only that
			return errors.New("kea: the control socket /run/kea/kea4-ctrl.sock: connection refused")
		}
		return nil
	}
	f.mu.Unlock()
	h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: short_lease, lease_time: 7s}")
	f.mu.Lock()
	f.testFn = nil
	f.mu.Unlock()
	f.setTestErr(&kea.Error{Command: "config-test", Result: 1, Text: "the interface br-iot is not there"})
	h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: silence}")
	if got := h.e.Snapshot().Overlays; len(got) != 2 {
		t.Errorf("%+v", got)
	}
}

func TestDeleteLeaseDeletesOnceWhenItIsWrittenAndAgainWhenItIsWrittenAgain(t *testing.T) {
	h, f, sid := dhcpActionHarness(t)
	ch, cancel := h.e.Subscribe()
	defer cancel()
	f.setLeases(lease("10.10.0.150", macCfg, sid))
	res := h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: delete_lease}")
	got := f.deletions()
	if len(got) != 1 || got[0].subnet != sid || len(got[0].macs) != 0 {
		t.Fatalf("a network's leases: %+v", got)
	}
	evs := collect(ch, engine.EventDHCPLeasesDeleted)
	if len(evs) != 1 || evs[0].Data["overlay"] != res.Overlay.Id.String() || evs[0].Data["network"] != iotID || evs[0].Data["count"] != 1 {
		t.Errorf("%+v", evs)
	}
	// later applies do not delete again: another overlay, a revision
	h.mustPut(bob, "target: {device: esp32-42}\nfault: {latency: 50ms}")
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		(*c.Devices)[devID] = withFixed((*c.Devices)[devID], "10.10.0.31")
	}))
	if got := f.deletions(); len(got) != 1 {
		t.Errorf("a later apply deleted again: %+v", got)
	}
	// written again: it acts again
	h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: delete_lease}")
	if got := f.deletions(); len(got) != 2 {
		t.Errorf("%+v", got)
	}
	// a device's: the leases of its MACs
	h.mustPut(alice, "target: {device: esp32-42}\ndhcp: {action: delete_lease}")
	got = f.deletions()
	if len(got) != 3 || got[2].subnet != sid || len(got[2].macs) != 1 || got[2].macs[0] != macCfg {
		t.Errorf("%+v", got)
	}
	evs = collect(ch, engine.EventDHCPLeasesDeleted)
	if len(evs) != 2 || evs[1].Data["device"] != devID {
		t.Errorf("%+v", evs)
	}
	// it is no state: nothing of it is in the server's configuration
	if cfg := f.last().Config; len(cfg.Silenced) != 0 || len(cfg.LeaseClasses) != 0 {
		t.Errorf("%+v", cfg)
	}
}

func TestADeletionTheServerCouldNotDoIsReportedAndDoneWhenItCan(t *testing.T) {
	h, f, sid := dhcpActionHarness(t)
	f.setLeases(lease("10.10.0.150", macCfg, sid))
	f.setDeleteErr(errors.New("the control socket is gone"))
	h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: delete_lease}")
	if e := h.e.Snapshot().DHCPError; !strings.Contains(e, "could not delete leases") || !strings.Contains(e, "the control socket is gone") {
		t.Fatalf("the failure is not reported: %q", e)
	}
	if got := f.deletions(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	// the retry does it, once, when the server answers again, and the error goes
	f.setDeleteErr(nil)
	h.wait(func() bool {
		h.clk.Advance(5 * time.Second)
		return len(f.deletions()) == 1 && h.e.Snapshot().DHCPError == ""
	}, "the deletion after the server is back")
	h.clk.Advance(5 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if got := f.deletions(); len(got) != 1 {
		t.Errorf("done twice: %+v", got)
	}
}

func TestAnActionOnADeviceTheServerCannotReachIsAcceptedAndSaysItHasNoEffect(t *testing.T) {
	h, _, _ := dhcpActionHarness(t)
	// a network without DHCP
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		n := (*c.Networks)[labNetID]
		lan, _ := n.AsLanNetwork()
		lan.Dhcp = &model.DhcpScope{}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[labNetID] = n
		n = (*c.Networks)[iotID]
		lan, _ = n.AsLanNetwork()
		off := false
		lan.Dhcp = &model.DhcpScope{Enabled: &off}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
	}))
	h.mustPut(alice, "target: {network: IoT}\ndhcp: {action: silence}")
	var found bool
	for _, p := range h.e.Snapshot().Problems {
		if p.Code == "dhcp_action_inactive" && strings.Contains(p.Message, "DHCP is off on the network") {
			found = true
		}
	}
	if !found {
		t.Errorf("%+v", h.e.Snapshot().Problems)
	}
}
