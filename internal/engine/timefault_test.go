package engine_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// The time family in the engine (plan §2.18, M34): a time overlay and the time part of a profile are accepted, reach the
// snapshot as the faults the NTP server answers with and as the redirects of the kernel, and explain says what answers.

func TestTheEngineListsTheTimeKindAndFamilyAsSupported(t *testing.T) {
	var kind, family bool
	for _, k := range engine.SupportedOverlayKinds {
		kind = kind || k == model.OverlayKindTime
	}
	for _, f := range engine.SupportedFaultFamilies {
		family = family || f == model.FaultFamilyTime
	}
	if !kind || !family {
		t.Errorf("kind %v, family %v", kind, family)
	}
	req := &model.OverlayRequest{Target: &model.Scope{Network: ptr("IoT")}, Time: &model.TimeFault{Offset: ptr("+1d")}}
	if err := engine.CheckOverlaySupported(req); err != nil {
		t.Errorf("CheckOverlaySupported = %v", err)
	}
}

// An invalid time fault is refused by the validation, whatever the engine supports, and changes nothing.
func TestAnInvalidTimeFaultIsRefusedByTheValidationAndChangesNothing(t *testing.T) {
	h := tlsHarness(t)
	gen := h.e.Snapshot().Generation
	for name, body := range map[string]string{
		"offset and absolute": "target: {network: IoT}\ntime: {offset: +400d, absolute: '1970-01-01T00:00:00Z'}",
		"the global scope":    "target: {global: true}\ntime: {offset: +400d}",
	} {
		_, err := h.put(alice, body)
		var ve domain.ValidationErrors
		if !errors.As(err, &ve) {
			t.Errorf("%s: got %v, want a validation error", name, err)
		}
	}
	if s := h.e.Snapshot(); len(s.Overlays) != 0 || s.Generation != gen {
		t.Errorf("a refused write changed the state: %d overlays, generation %d -> %d", len(s.Overlays), gen, s.Generation)
	}
}

// A time overlay is a fault of the snapshot for the device's address, the kernel redirects the device's NTP to the
// server, and both go when the overlay goes.
func TestATimeOverlayIsAnNTPFaultOfTheSnapshotAndRedirectsItsDevicesInTheKernel(t *testing.T) {
	h := tlsHarness(t)
	if s := h.barrier(); len(s.NTPFaults) != 0 || len(s.NTPRedirectPorts) != 0 {
		t.Fatalf("%+v", s.NTPFaults)
	}
	before, ok := h.kernelChain("prerouting")
	if !ok {
		t.Fatal("no prerouting chain: the gateway address is redirected to the server in a network with the time service on")
	}
	res := h.mustPut(alice, "target: {device: esp32-42}\ntime: {offset: +400d}")
	if res.Overlay.Kind != model.OverlayKindTime {
		t.Fatalf("%+v", res.Overlay)
	}
	s := h.barrier()
	if len(s.NTPFaults) != 1 {
		t.Fatalf("%+v", s.NTPFaults)
	}
	f := s.NTPFaults[0]
	if f.Overlay != res.Overlay.Id.String() || f.Port != compiler.ServiceNTPPort || *f.Fault.Offset != "+400d" || f.Sources[0] != netip.MustParsePrefix("10.10.0.31/32") ||
		!f.Since.Equal(res.Overlay.UpdatedAt) {
		t.Errorf("%+v", f)
	}
	if after, _ := h.kernelChain("prerouting"); after != before+1 {
		t.Errorf("the device's NTP is redirected by one rule: %d -> %d rules", before, after)
	}

	if _, err := h.e.DeleteOverlay(context.Background(), res.Overlay.Id, nil, model.Actor{}); err != nil {
		t.Fatal(err)
	}
	if s := h.barrier(); len(s.NTPFaults) != 0 {
		t.Errorf("%+v", s.NTPFaults)
	}
	if after, _ := h.kernelChain("prerouting"); after != before {
		t.Errorf("the redirect stays without the fault: %d rules, want %d", after, before)
	}
}

// A fault for one server gets a port of its own and an entry for it, next to the entry of the fault for every server on 123;
// explain says the NTP server answers a query to the gateway's address, to the server of the fault and to any other server
// of the device, and nothing answers TCP.
func TestADestinationSelectorGetsAPortAndExplainNamesTheServer(t *testing.T) {
	h := tlsHarness(t)
	all := h.mustPut(alice, "target: {device: esp32-42}\ntime: {offset: -1d}")
	one := h.mustPut(alice, "target: {device: esp32-42}\ntime: {destination: {cidr: 203.0.113.5}, offset: +1y}")
	s := h.barrier()
	if len(s.NTPRedirectPorts) != 1 || s.NTPRedirectPorts[0] != compiler.NTPRedirectPortBase {
		t.Fatalf("redirect ports %v", s.NTPRedirectPorts)
	}
	byPort := map[int]string{}
	for _, f := range s.NTPFaults {
		byPort[f.Port] = f.Overlay
	}
	if len(s.NTPFaults) != 2 || byPort[compiler.ServiceNTPPort] != all.Overlay.Id.String() || byPort[compiler.NTPRedirectPortBase] != one.Overlay.Id.String() {
		t.Fatalf("%+v", s.NTPFaults)
	}
	dev := netip.MustParseAddr("10.10.0.31")
	explain := func(dst, proto string, port int) *engine.Explanation {
		t.Helper()
		ex, err := h.e.Explain(context.Background(), engine.ExplainQuery{Src: dev, Dst: dst, Protocol: proto, Port: port})
		if err != nil {
			t.Fatal(err)
		}
		return ex
	}
	timeWinner := func(ex *engine.Explanation) string {
		for _, f := range ex.Faults {
			if f.Family == domain.FamilyTime && f.Winner != nil {
				return f.Winner.Id.String()
			}
		}
		return ""
	}
	if ex := explain("203.0.113.5", "udp", 123); ex.Service != "ntp_server" || timeWinner(ex) != one.Overlay.Id.String() {
		t.Errorf("the server of the fault: %q, winner %q", ex.Service, timeWinner(ex))
	}
	if ex := explain("203.0.113.9", "udp", 123); ex.Service != "ntp_server" || timeWinner(ex) != all.Overlay.Id.String() {
		t.Errorf("another server: %q, winner %q", ex.Service, timeWinner(ex))
	}
	if ex := explain("10.10.0.1", "udp", 123); ex.Service != "ntp_server" {
		t.Errorf("the gateway: %q", ex.Service)
	}
	if ex := explain("203.0.113.5", "tcp", 123); ex.Service != "none" || timeWinner(ex) != "" {
		t.Errorf("tcp: %q, winner %q", ex.Service, timeWinner(ex))
	}
	if ex := explain("203.0.113.5", "udp", 5000); ex.Service != "none" {
		t.Errorf("another port: %q", ex.Service)
	}
}

// Without a fault only the gateway's address is the server's: a query to another server is the uplink's, until the
// network redirects hardcoded servers.
func TestWithoutAFaultOnlyTheGatewayAddressIsTheNTPServers(t *testing.T) {
	h := tlsHarness(t)
	explain := func(dst string) string {
		ex, err := h.e.Explain(context.Background(), engine.ExplainQuery{Src: netip.MustParseAddr("10.10.0.31"), Dst: dst, Protocol: "udp", Port: 123})
		if err != nil {
			t.Fatal(err)
		}
		return ex.Service
	}
	if got := explain("10.10.0.1"); got != "ntp_server" {
		t.Errorf("the gateway: %q", got)
	}
	if got := explain("203.0.113.5"); got != "none" {
		t.Errorf("another server: %q", got)
	}
	h.mustApply(h.revision(func(c *model.Configuration) {
		n := (*c.Networks)[iotID]
		lan, _ := n.AsLanNetwork()
		lan.Ntp = &model.NetworkNtp{RedirectHardcoded: ptr(true)}
		_ = n.FromLanNetwork(lan)
		(*c.Networks)[iotID] = n
	}))
	if got := explain("203.0.113.5"); got != "ntp_server" {
		t.Errorf("a network that redirects hardcoded servers: %q", got)
	}
}

// The time part of a profile is a fault of the snapshot with the profile's name, and it competes with the overlays of the
// family like a part of any other family.
func TestTheTimePartOfAProfileIsAnNTPFaultAndCompetesInTheTimeFamily(t *testing.T) {
	h := tlsHarness(t)
	h.mustApply(h.revision(func(c *model.Configuration) {
		withDHCPAndDevice(c)
		profiles := map[string]model.Profile{profGlitch: {Name: "clock", Parts: model.ProfileParts{Time: &model.TimeFault{Offset: ptr("+400d")}}}}
		c.Profiles = &profiles
	}))
	prof := h.mustPut(alice, "target: {network: IoT}\nprofile: clock")
	s := h.barrier()
	if len(s.NTPFaults) != 1 || s.NTPFaults[0].Profile != "clock" || s.NTPFaults[0].Overlay != prof.Overlay.Id.String() || s.NTPFaults[0].Port != compiler.ServiceNTPPort {
		t.Fatalf("%+v", s.NTPFaults)
	}
	// a device's own time fault wins over the network's profile
	dev := h.mustPut(alice, "target: {device: esp32-42}\ntime: {absolute: '1970-01-01T00:00:00Z'}")
	var forDevice bool
	for _, f := range h.barrier().NTPFaults {
		if f.Overlay == dev.Overlay.Id.String() && f.Sources[0] == netip.MustParsePrefix("10.10.0.31/32") {
			forDevice = true
		}
		if f.Overlay == prof.Overlay.Id.String() && f.Sources[0] == netip.MustParsePrefix("10.10.0.31/32") {
			t.Errorf("the profile decides for the device: %+v", f)
		}
	}
	if !forDevice {
		t.Errorf("%+v", h.barrier().NTPFaults)
	}
}
