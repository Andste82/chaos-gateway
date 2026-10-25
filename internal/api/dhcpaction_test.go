package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/clock"
	"github.com/Andste82/chaos-gateway/internal/kea"
)

// The DHCP test actions over the API (plan §2.7, M23). The contract harness checks every request and answer against the spec.

func (g *gw) dhcpOverlay(body string) map[string]any {
	g.t.Helper()
	g.mintToken("overlays")
	return g.mustCreateOverlay(body)
}

func (g *gw) overlay(id string) map[string]any {
	g.t.Helper()
	r := g.do("GET", "/overlays/"+id, nil, nil, nil)
	if r.Status != 200 {
		g.t.Fatalf("GET /overlays/%s: %d %s", id, r.Status, r.Body)
	}
	return r.json(g.t)
}

func TestADHCPActionIsEffectiveWhileTheServerRunsItAndDisabledWhereNothingServesDHCP(t *testing.T) {
	// no network serves DHCP: the action is accepted and says it does nothing
	g := ready(t)
	ov := g.dhcpOverlay(`{"target":{"network":"IoT"},"dhcp":{"action":"short_lease","lease_time":"7s"}}`)
	if ov["kind"] != "dhcp" || ov["state"] != "disabled" {
		t.Errorf("%v", ov)
	}
	// IoT serves DHCP
	g = dhcpGateway(t)
	ov = g.dhcpOverlay(`{"target":{"network":"IoT"},"dhcp":{"action":"short_lease","lease_time":"7s"},"ttl":"10m"}`)
	if ov["kind"] != "dhcp" || ov["state"] != "effective" || ov["dhcp"].(map[string]any)["lease_time"] != "7s" || ov["target"].(map[string]any)["network"] != iotID {
		t.Fatalf("%v", ov)
	}
	if ov["expires_at"] == nil {
		t.Errorf("a DHCP action has a TTL like the other overlays: %v", ov)
	}
	// the preview of what is in force names it
	st := g.do("GET", "/overlays?network=IoT", nil, nil, nil).json(t)["items"].([]any)
	if len(st) != 1 || st[0].(map[string]any)["state"] != "effective" {
		t.Errorf("%v", st)
	}
	// it goes with its overlay
	if r := g.do("DELETE", "/overlays/"+ov["id"].(string), nil, nil, nil); r.Status != 204 && r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if n := len(g.do("GET", "/overlays", nil, nil, nil).json(t)["items"].([]any)); n != 0 {
		t.Errorf("%d overlays", n)
	}
}

func TestTheNewerActionOfADeviceOverridesTheOlderAndSaysWhich(t *testing.T) {
	g := dhcpGateway(t)
	old := g.dhcpOverlay(`{"target":{"device":"esp32-42"},"dhcp":{"action":"short_lease","lease_time":"30s"}}`)
	// a different action at the same scope is a different overlay (the key is the action), and the newer one wins
	newer := g.dhcpOverlay(`{"target":{"device":"esp32-42"},"dhcp":{"action":"silence"}}`)
	if g.overlay(newer["id"].(string))["state"] != "effective" {
		t.Errorf("%v", g.overlay(newer["id"].(string)))
	}
	o := g.overlay(old["id"].(string))
	by, _ := o["overridden_by"].([]any)
	if o["state"] != "overridden" || len(by) != 1 || by[0].(map[string]any)["id"] != newer["id"] {
		t.Errorf("the older action is overridden by the newer: %v", o)
	}
	// a network action stays effective: it wins for the other clients of the network
	net := g.dhcpOverlay(`{"target":{"network":"IoT"},"dhcp":{"action":"set_options","options":{"domain":"x.test"}}}`)
	if g.overlay(net["id"].(string))["state"] != "effective" {
		t.Errorf("%v", g.overlay(net["id"].(string)))
	}
}

func TestForceNewIPOverTheAPIReadsBackWithItsAddress(t *testing.T) {
	g := dhcpGateway(t)
	ov := g.dhcpOverlay(`{"target":{"device":"esp32-42"},"dhcp":{"action":"force_new_ip"}}`)
	ip, _ := ov["dhcp"].(map[string]any)["ip"].(string)
	if ip != "10.10.0.254" || ov["state"] != "effective" {
		t.Fatalf("the device is moved to the last address of the default pool: %v", ov)
	}
	if got := g.overlay(ov["id"].(string))["dhcp"].(map[string]any)["ip"]; got != ip {
		t.Errorf("%v", got)
	}
	// the address can be named
	ov = g.dhcpOverlay(`{"target":{"device":"esp32-42"},"dhcp":{"action":"force_new_ip","ip":"10.10.0.77"}}`)
	if ov["dhcp"].(map[string]any)["ip"] != "10.10.0.77" {
		t.Errorf("%v", ov)
	}
	// refused: an address outside the network, a network as the target, an address for another action
	for body, want := range map[string][2]string{
		`{"target":{"device":"esp32-42"},"dhcp":{"action":"force_new_ip","ip":"192.0.2.7"}}`: {"/dhcp/ip", "invalid_dhcp_action"},
		`{"target":{"network":"IoT"},"dhcp":{"action":"force_new_ip"}}`:                      {"/target", "invalid_target"},
		`{"target":{"device":"esp32-42"},"dhcp":{"action":"silence","ip":"10.10.0.77"}}`:     {"/dhcp/ip", "invalid_dhcp_action"},
		`{"target":{"group":"sensors"},"dhcp":{"action":"silence"}}`:                         {"/target", "invalid_target"},
		`{"target":{"device":"esp32-42"},"dhcp":{"action":"short_lease"}}`:                   {"/dhcp/lease_time", "invalid_dhcp_action"},
		`{"target":{"device":"esp32-42"},"dhcp":{"action":"set_options"}}`:                   {"/dhcp/options", "invalid_dhcp_action"},
		`{"target":{"device":"esp32-42"},"dhcp":{"action":"short_lease","lease_time":"0s"}}`: {"/dhcp/lease_time", "invalid_duration"},
	} {
		g.badRequest = true
		r := g.createOverlay(body)
		g.badRequest = false
		if r.Status != 422 || r.code(t) != "validation_failed" {
			t.Errorf("%s: %d %s", body, r.Status, r.Body)
			continue
		}
		first := r.json(t)["errors"].([]any)[0].(map[string]any)
		if first["path"] != want[0] || first["code"] != want[1] {
			t.Errorf("%s: %v", body, first)
		}
	}
}

func TestDeleteLeaseOverTheAPIDeletesTheLeasesAndAnnouncesIt(t *testing.T) {
	g := dhcpGateway(t)
	g.mintToken("overlays")
	s := g.openStream("", "?types=dhcp_leases_deleted")
	sid := 0
	for id := range g.e.Snapshot().KeaNetworks {
		sid = id
	}
	g.dhcp.set(kea.Lease{IP: "10.10.0.150", MAC: "02:00:00:00:00:31", SubnetID: sid, ValidLft: 600, CLTT: time.Now().Unix()})
	ov := g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"dhcp":{"action":"delete_lease"}}`)
	if ov["state"] != "effective" {
		t.Errorf("%v", ov)
	}
	got := g.dhcp.deletions()
	if len(got) != 1 || got[0].subnet != sid || len(got[0].macs) != 1 || got[0].macs[0] != "02:00:00:00:00:31" {
		t.Fatalf("%+v", got)
	}
	ev, ok := s.until("dhcp_leases_deleted", 5*time.Second)
	if !ok || ev.Data["data"].(map[string]any)["count"] != float64(1) || ev.Data["data"].(map[string]any)["device"] != espID ||
		ev.Data["subject"].(map[string]any)["id"] != ov["id"] || !strings.Contains(ev.Data["message"].(string), "DHCP leases deleted (1)") {
		t.Errorf("%v %v", ev.Data, ok)
	}
	// written again, it acts again
	g.mustCreateOverlay(`{"target":{"device":"esp32-42"},"dhcp":{"action":"delete_lease"}}`)
	if got := g.dhcp.deletions(); len(got) != 2 {
		t.Errorf("%+v", got)
	}
	// and for the network
	g.mustCreateOverlay(`{"target":{"network":"IoT"},"dhcp":{"action":"delete_lease"}}`)
	if got := g.dhcp.deletions(); len(got) != 3 || len(got[2].macs) != 0 {
		t.Errorf("%+v", got)
	}
}

// A run with a DHCP step: its overlay is the run's, the server has the action while the run lasts and not afterwards.
func TestARunWithADHCPStepAppliesItAndTakesItOut(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	g := newGW(t, func(o *options) { o.apiClock = clk })
	g.finishSetup()
	id := g.mustPatch(map[string]any{"networks": map[string]any{iotID: map[string]any{"dhcp": map[string]any{"lease_time": "10m"}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.postRun(inlineRun("dhcp-run", iotTarget,
		step("short", "0s", "dhcp", map[string]any{"action": "short_lease", "lease_time": "5s"}),
		step("end", "10s", "restore", true),
	), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	run := r.json(t)["id"].(string)
	g.waitStep(run, "short", "applied")
	ovs := g.overlaysOf(run)
	if len(ovs) != 1 || ovs[0].(map[string]any)["kind"] != "dhcp" || ovs[0].(map[string]any)["state"] != "effective" {
		t.Fatalf("%v", ovs)
	}
	if s := g.e.Snapshot(); len(s.Overlays) != 1 {
		t.Errorf("%d overlays", len(s.Overlays))
	}
	clk.Advance(10 * time.Second)
	g.waitRun(run, "passed")
	if n := len(g.overlaysOf(run)); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
}

// A step that cannot be carried out ends the run as an error, with the reason: a new address for a device of a network
// that serves no DHCP.
func TestARunWhoseDHCPStepCannotBeCarriedOutEndsAsAnError(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	g := newGW(t, func(o *options) { o.apiClock = clk })
	g.finishSetup()
	id := g.mustPatch(map[string]any{"devices": map[string]any{espID: map[string]any{"name": "esp32-42", "network": iotID, "identifiers": map[string]any{"macs": []any{"02:00:00:00:00:31"}}}}})
	if r := g.apply(id); r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r := g.postRun(inlineRun("dhcp-error", map[string]any{"device": "esp32-42"},
		step("move", "0s", "dhcp", map[string]any{"action": "force_new_ip"}),
	), "")
	if r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	done := g.waitRun(r.json(t)["id"].(string), "error")
	if reason, _ := done["reason"].(string); !strings.Contains(reason, "DHCP is off") {
		t.Errorf("%v", done["reason"])
	}
}
