//go:build testbed

package engine_test

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/testbed"
)

// The level 1 tests of the DHCP test actions (plan M23): the real Kea in the gateway's namespace, the engine on the real clock and
// busybox's udhcpc in the client namespaces, which observes each behavior on the wire. The Kea side of every action is
// also pinned without a kernel by the tests of internal/kea, which talk DHCP to the real Kea on the loopback.

const devAID = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"

// withClients configures the devices of the clients A and B in IoT by their MAC addresses, without a fixed address.
func withClients(c *model.Configuration) {
	a, b := []string{testbed.ClientAMAC}, []string{testbed.ClientBMAC}
	c.Devices = &map[string]model.Device{
		devAID: {Name: "client-a", Network: ptrS(tIoT), Identifiers: &model.DeviceIdentifiers{Macs: &a}},
		devBID: {Name: "client-b", Network: ptrS(tIoT), Identifiers: &model.DeviceIdentifiers{Macs: &b}},
	}
}

// actionsBed is the bed with DHCP in both networks (a lease of a minute in IoT) and the devices of A and B.
func actionsBed(t *testing.T) *dhcpBed {
	t.Helper()
	b := newDHCPBed(t)
	b.apply(func(c *model.Configuration) {
		dhcpOn(c)
		setDHCP(c, labID, &model.DhcpScope{LeaseTime: ptrS("60s")})
		withClients(c)
	})
	b.follow()
	return b
}

func (b *dhcpBed) put(body string) engine.OverlayResult {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := b.e.PutOverlay(ctx, engine.OverlayWrite{Owner: alice, Request: overlayRequest(b.t, body)})
	if err != nil {
		b.t.Fatalf("put %q: %v", body, err)
	}
	return res
}

func (b *dhcpBed) remove(res engine.OverlayResult) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := b.e.DeleteOverlay(ctx, res.Overlay.Id, nil, model.Actor{}); err != nil {
		b.t.Fatal(err)
	}
}

// clientLines are the lines the script of a client logged: the event and the fields of the lease.
func clientLines(logFile string) []string {
	raw, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// field reads `key=value` of a line.
func field(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

// waitLine waits for the nth (from 1) line of the client's log that starts with the event.
func waitLine(t *testing.T, logFile, event string, n int, d time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		seen := 0
		for _, l := range clientLines(logFile) {
			if strings.HasPrefix(l, event+" ") {
				if seen++; seen == n {
					return l, true
				}
			}
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// keaLeaseOf is the address Kea holds for a client's MAC address, empty without a lease.
func (b *dhcpBed) keaLeaseOf(mac string) string {
	b.t.Helper()
	ls, err := b.kc.Leases(context.Background())
	if err != nil {
		b.t.Fatal(err)
	}
	for _, l := range ls {
		if strings.EqualFold(l.MAC, mac) {
			return l.IP
		}
	}
	return ""
}

// M23 test: a short lease of the network is the lease time the client is given, and it renews at half of it.
func TestAShortLeaseOfTheNetworkIsWhatUdhcpcGetsAndItRenewsSoonOnTheRealKernel(t *testing.T) {
	b := actionsBed(t)
	res := b.put("target: {network: IoT}\ndhcp: {action: short_lease, lease_time: 6s}")
	log := b.udhcpc(b.top.A, 8)
	line, ok := waitLine(t, log, "bound", 1, 30*time.Second)
	if !ok {
		t.Fatalf("the client got no lease\n%s", b.top.GW.Must("ip", "addr"))
	}
	if got := field(line, "lease"); got != "6" {
		t.Errorf("the client was given a lease of %ss, want the short one of 6s: %s", got, line)
	}
	// half of the lease is three seconds: the renewal is there long before the minute the network's own lease would take
	if l, ok := waitLine(t, log, "renew", 1, 15*time.Second); !ok || field(l, "ip") != field(line, "ip") {
		t.Errorf("no renewal with the same address after the short lease: %q", clientLines(log))
	}
	// another network is not touched; and when the action goes, a client that asks gets the lease of the network again
	if line, ok = waitLine(t, b.udhcpc(b.top.C, 8), "bound", 1, 30*time.Second); !ok || field(line, "lease") != "60" {
		t.Errorf("the lease of a client of the other network: %q", line)
	}
	b.remove(res)
	line, ok = waitLine(t, b.udhcpc(b.top.B, 8), "bound", 1, 30*time.Second)
	if !ok || field(line, "lease") != "60" {
		t.Errorf("the lease of a client after the action went: %q", line)
	}
}

// M23 test: the short lease of a device reaches that device only.
func TestAShortLeaseOfADeviceReachesOnlyThatDeviceOnTheRealKernel(t *testing.T) {
	b := actionsBed(t)
	b.put("target: {device: " + devAID + "}\ndhcp: {action: short_lease, lease_time: 5s}")
	a, ok := waitLine(t, b.udhcpc(b.top.A, 8), "bound", 1, 30*time.Second)
	if !ok || field(a, "lease") != "5" {
		t.Errorf("the device's lease: %q", a)
	}
	bl, ok := waitLine(t, b.udhcpc(b.top.B, 8), "bound", 1, 30*time.Second)
	if !ok || field(bl, "lease") != "60" {
		t.Errorf("the lease of the other device: %q", bl)
	}
}

// renews counts the renewals a client logged.
func renews(logFile string) int {
	n := 0
	for _, l := range clientLines(logFile) {
		if strings.HasPrefix(l, "renew ") {
			n++
		}
	}
	return n
}

// M23 test: the leases that are deleted are gone from Kea, and the client that renews gets the same address again. The leases of
// the test are half a minute, so the clients renew at 15 s and the deletion lies well before it.
func TestDeletingTheLeaseRemovesItAndTheClientGetsTheSameAddressAtItsRenewalOnTheRealKernel(t *testing.T) {
	b := actionsBed(t)
	b.apply(func(c *model.Configuration) {
		setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("30s")})
		setDHCP(c, labID, &model.DhcpScope{LeaseTime: ptrS("60s")})
		withClients(c)
	})
	logA, logB := b.udhcpc(b.top.A, 8), b.udhcpc(b.top.B, 8)
	line, ok := waitLine(t, logA, "bound", 1, 30*time.Second)
	if !ok {
		t.Fatal("the client got no lease")
	}
	if _, ok := waitLine(t, logB, "bound", 1, 30*time.Second); !ok {
		t.Fatal("B got no lease")
	}
	ip := field(line, "ip")
	if got := b.keaLeaseOf(testbed.ClientAMAC); got != ip {
		t.Fatalf("Kea holds %q for the client, want %s", got, ip)
	}

	// the action of the device deletes A's lease and not B's
	before := renews(logA)
	events, stop := b.e.Subscribe()
	defer stop()
	b.put("target: {device: " + devAID + "}\ndhcp: {action: delete_lease}")
	// the deletion is done when the write returns: the event says that Kea held a lease of the device and that it was deleted
	if evs := collect(events, engine.EventDHCPLeasesDeleted); len(evs) != 1 || evs[0].Data["device"] != devAID || evs[0].Data["count"] != 1 {
		t.Errorf("the event of the deletion of the device's lease: %+v", evs)
	}
	if got := b.keaLeaseOf(testbed.ClientAMAC); got != "" && renews(logA) == before {
		t.Errorf("Kea still holds the lease %s of the device after the deletion", got)
	}
	if renews(logB) == 0 && b.keaLeaseOf(testbed.ClientBMAC) == "" {
		t.Error("the deletion of a device's lease took the lease of another device")
	}
	// the renewal after the deletion is acknowledged with the same address, and the lease is there again
	if l, ok := waitLine(t, logA, "renew", before+1, 25*time.Second); !ok || field(l, "ip") != ip {
		t.Fatalf("the renewal after the deletion: %q (client log %q)", l, clientLines(logA))
	}
	if got := b.keaLeaseOf(testbed.ClientAMAC); got != ip {
		t.Errorf("after the renewal Kea holds %q, want the same address %s", got, ip)
	}

	// the action of the network deletes the leases of both
	beforeA, beforeB := renews(logA), renews(logB)
	b.put("target: {network: IoT}\ndhcp: {action: delete_lease}")
	if evs := collect(events, engine.EventDHCPLeasesDeleted); len(evs) != 1 || evs[0].Data["network"] != tIoT || evs[0].Data["count"] != 2 {
		t.Errorf("the event of the deletion of the network's leases: %+v", evs)
	}
	if renews(logA) == beforeA && renews(logB) == beforeB {
		if ls, err := b.kc.Leases(context.Background()); err != nil || len(ls) != 0 {
			t.Errorf("Kea holds %+v after the deletion of the network's leases (%v)", ls, err)
		}
	}
	if l, ok := waitLine(t, logB, "renew", beforeB+1, 30*time.Second); !ok {
		t.Errorf("B does not renew after the deletion: %q", l)
	}
}

// M23 test: force a new IP. The reservation of the device moves, the renewal is refused, the client discovers again
// and is given the new address.
func TestForceNewIPMovesTheClientAtItsRenewalOnTheRealKernel(t *testing.T) {
	b := actionsBed(t)
	b.apply(func(c *model.Configuration) {
		setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("6s")})
		setDHCP(c, labID, &model.DhcpScope{LeaseTime: ptrS("60s")})
		withClients(c)
	})
	log := b.udhcpc(b.top.A, 12)
	line, ok := waitLine(t, log, "bound", 1, 30*time.Second)
	if !ok {
		t.Fatal("the client got no lease")
	}
	old := field(line, "ip")
	res := b.put("target: {device: " + devAID + "}\ndhcp: {action: force_new_ip}")
	moved := derefS(res.Overlay.Dhcp.Ip)
	if p := netip.MustParsePrefix("10.10.0.128/25"); !p.Contains(netip.MustParseAddr(moved)) || moved == old {
		t.Fatalf("the gateway chose %q for a client that holds %s", moved, old)
	}
	// the client renews at three seconds, is refused, and discovers: its second lease is the reserved address
	second, ok := waitLine(t, log, "bound", 2, 30*time.Second)
	if !ok {
		t.Fatalf("the client never got a new address: %q", clientLines(log))
	}
	if got := field(second, "ip"); got != moved {
		t.Errorf("the client got %s, want the reserved address %s", got, moved)
	}
	if _, ok := waitLine(t, log, "deconfig", 2, time.Second); !ok {
		t.Errorf("the refused renewal did not make the client let go of its address: %q", clientLines(log))
	}
	if got := b.keaLeaseOf(testbed.ClientAMAC); got != moved {
		t.Errorf("Kea holds %q for the client, want %s", got, moved)
	}
	// a client that has the address stays with it at the next renewal while the overlay lasts
	if l, ok := waitLine(t, log, "renew", 1, 15*time.Second); !ok || field(l, "ip") != moved {
		t.Errorf("the renewal of the new address: %q", l)
	}
}

// M23 test: a fault of the device stays attached after the forced new IP: the identity follows the MAC, so the kernel
// impairs the traffic of the new address as it did the one of the old.
func TestADeviceFaultStaysAttachedAfterTheForcedNewIPOnTheRealKernel(t *testing.T) {
	b := actionsBed(t)
	b.apply(func(c *model.Configuration) {
		setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("6s")})
		setDHCP(c, labID, &model.DhcpScope{LeaseTime: ptrS("60s")})
		withClients(c)
	})
	log := b.udhcpc(b.top.A, 12)
	line, ok := waitLine(t, log, "bound", 1, 30*time.Second)
	if !ok {
		t.Fatal("the client got no lease")
	}
	old := field(line, "ip")
	b.top.A.Must("ip", "route", "replace", "default", "via", testbed.LAN0Gateway)
	b.waitDevice(testbed.ClientAMAC, 20*time.Second, func(d *engine.DeviceState) bool { return len(d.Addresses) > 0 })

	b.put("target: {device: " + devAID + "}\nfault: {latency: 150ms}")
	if rtt := testbed.MustPing(t, b.top.A, testbed.ServerAddr, 3, 200*time.Millisecond).Median(); rtt < 250*time.Millisecond {
		t.Fatalf("setup: the fault does not delay the device (median %v)", rtt)
	}
	faults := len(b.e.Snapshot().Faults)

	res := b.put("target: {device: " + devAID + "}\ndhcp: {action: force_new_ip}")
	moved := derefS(res.Overlay.Dhcp.Ip)
	if _, ok := waitLine(t, log, "bound", 2, 30*time.Second); !ok {
		t.Fatalf("the client never got a new address: %q", clientLines(log))
	}
	b.top.A.Must("ip", "route", "replace", "default", "via", testbed.LAN0Gateway)
	d := b.waitDevice(testbed.ClientAMAC, 20*time.Second, func(d *engine.DeviceState) bool {
		return len(d.Addresses) > 0 && d.Addresses[0].String() == moved
	})
	if d.ID != devAID {
		t.Errorf("the device with the new address is %s, want the device of the MAC (%s)", d.ID, devAID)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !contains(b.identityMapAddresses(devAID), moved) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if got := b.identityMapAddresses(devAID); !contains(got, moved) {
		t.Errorf("the kernel's identity map holds %v for the device, not the new address %s (old %s)", got, moved, old)
	}
	if got := len(b.e.Snapshot().Faults); got != faults {
		t.Errorf("the device has %d faults after the move, %d before", got, faults)
	}
	if rtt := testbed.MustPing(t, b.top.A, testbed.ServerAddr, 3, 200*time.Millisecond).Median(); rtt < 250*time.Millisecond {
		t.Errorf("the fault is not attached to the device at its new address %s (median %v)", moved, rtt)
	}
}

// M23 test: the options of an action are what the client is told: the network's options for the clients of the network and
// the device's for the device, whose options are the configured ones with its own.
func TestSetOptionsChangeWhatTheClientIsToldOnTheRealKernel(t *testing.T) {
	b := actionsBed(t)
	b.put("target: {network: IoT}\ndhcp: {action: set_options, options: {router: 10.10.0.9, dns_servers: [192.0.2.53], domain: net.test}}")
	b.put("target: {device: " + devBID + "}\ndhcp: {action: set_options, options: {router: 10.10.0.8}}")
	a, ok := waitLine(t, b.udhcpc(b.top.A, 8), "bound", 1, 30*time.Second)
	if !ok {
		t.Fatal("A got no lease")
	}
	if field(a, "router") != "10.10.0.9" || field(a, "dns") != "192.0.2.53" || field(a, "domain") != "net.test" {
		t.Errorf("the options of the network's action: %s", a)
	}
	bl, ok := waitLine(t, b.udhcpc(b.top.B, 8), "bound", 1, 30*time.Second)
	if !ok {
		t.Fatal("B got no lease")
	}
	// the device's options replace the network's: its router is its own, the rest are the configured ones (the gateway as DNS server)
	if field(bl, "router") != "10.10.0.8" || field(bl, "dns") != testbed.LAN0Gateway || field(bl, "domain") != "" {
		t.Errorf("the options of the device's action: %s", bl)
	}
}

// M23 test: silence leaves a client without an answer: a device's, and a network's, while another network is served.
func TestSilenceLeavesTheClientWithoutAnAnswerOnTheRealKernel(t *testing.T) {
	b := actionsBed(t)
	dev := b.put("target: {device: " + devAID + "}\ndhcp: {action: silence}")
	if l, ok := waitLine(t, b.udhcpc(b.top.A, 3), "bound", 1, 10*time.Second); ok {
		t.Fatalf("the silenced device was answered: %s", l)
	}
	if _, ok := waitLine(t, b.udhcpc(b.top.B, 8), "bound", 1, 30*time.Second); !ok {
		t.Fatal("another device of the network was not answered")
	}
	b.remove(dev)
	if _, ok := waitLine(t, b.udhcpc(b.top.A, 8), "bound", 1, 30*time.Second); !ok {
		t.Fatal("the device is still not answered after the action went")
	}

	// the network: every client of IoT, and the other network is served
	b.apply(func(c *model.Configuration) {
		setDHCP(c, tIoT, &model.DhcpScope{LeaseTime: ptrS("60s")})
		setDHCP(c, labID, &model.DhcpScope{LeaseTime: ptrS("60s")})
	})
	b.top.A.Must("ip", "addr", "flush", "dev", "eth0")
	net := b.put("target: {network: IoT}\ndhcp: {action: silence}")
	if l, ok := waitLine(t, b.udhcpc(b.top.A, 3), "bound", 1, 10*time.Second); ok {
		t.Fatalf("a client of the silent network was answered: %s", l)
	}
	if _, ok := waitLine(t, b.udhcpc(b.top.C, 8), "bound", 1, 30*time.Second); !ok {
		t.Fatal("a client of the other network was not answered")
	}
	b.remove(net)
	if _, ok := waitLine(t, b.udhcpc(b.top.B, 8), "bound", 1, 30*time.Second); !ok {
		t.Fatal("the network is still silent after the action went")
	}
}
