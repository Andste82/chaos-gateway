//go:build testbed

package compiler

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/linux"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// This file runs the port forwards (M35a) on the real kernel with real traffic, on the bed of the access
// rules (accesskernel_test.go): the device esp32-42 (10.10.0.42) behind br-iot listens on tcp 8883, 22 and
// 7050 and on udp 5353; the server (203.0.113.10) is behind the uplink wan0 (203.0.113.1), a management
// host (192.168.56.2) behind mgmt0 (192.168.56.1). The engine's tests (integration_forward_test.go) stand
// on top of these: the device's address followed by the engine, faults, the three-port routing.

// newForwardBed is the access bed with listeners on the device.
func newForwardBed(t *testing.T) *accessBed {
	t.Helper()
	b := newAccessBed(t)
	b.dev.Start("python3", "-c", serverScript)
	b.waitListening(b.dev, "8883", "22", "7050")
	return b
}

// removeForward drops a forward from the configuration of the bed.
func (b *accessBed) removeForward(id string) {
	b.t.Helper()
	cfg := *b.w.cfg
	kept := map[string]model.PortForward{}
	for k, v := range *cfg.PortForwards {
		if k != id {
			kept[k] = v
		}
	}
	cfg.PortForwards = &kept
	b.w.cfg = &cfg
}

// The forwards reach a TCP and a UDP listener of the device from behind the uplink, a range one to one;
// without a forward the server's connection to the device is dropped by the default of the access matrix
// and counted, and a port without a forward is the gateway's own. The forward's counter counts the
// connections it let through. The DNAT map is read back as it was compiled. A deleted forward leaves no
// DNAT: its port is the gateway's own again.
func TestAForwardReachesTheListenersOfADeviceOnTheRealKernel(t *testing.T) {
	b := newForwardBed(t)
	b.apply()
	drops := b.counter("forward_drop")
	b.expect(b.srv, "tcp", "10.10.0.42", 8883, "timeout")
	if b.counter("forward_drop") == drops {
		t.Error("the server's connection to the device is not counted as dropped")
	}
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "refused") // nothing listens on the gateway

	b.w.addForward(fwdWeb, `{name: web, protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}, target_port: 8883}`)
	b.w.addForward(fwdAddr, `{name: echo, protocol: udp, entry: uplink, external_port: 5353, target: {address: 10.10.0.42}}`)
	b.w.addForward(fwdRange, `{name: range, protocol: tcp, entry: uplink, external_port: 9049, external_port_end: 9050, target: {device: esp32-42}, target_port: 7049}`)
	tg := b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "ok")
	b.expect(b.srv, "udp", "203.0.113.1", 5353, "ok")
	// one to one: 9050 reaches 7050 (the listener), 9049 goes to 7049, where the device refuses
	b.expect(b.srv, "tcp", "203.0.113.1", 9050, "ok")
	b.expect(b.srv, "tcp", "203.0.113.1", 9049, "refused")
	// everything else from the uplink stays out
	b.expect(b.srv, "tcp", "10.10.0.42", 8883, "timeout")
	b.expect(b.srv, "tcp", "203.0.113.1", 8081, "refused")
	if n := b.counter(forwardOf(t, tg, fwdWeb).Counter); n != 1 {
		t.Errorf("the forward counted %d connections, want 1", n)
	}
	if n := b.counter(forwardOf(t, tg, fwdRange).Counter); n != 2 {
		t.Errorf("the range counted %d connections, want 2", n)
	}
	m := b.current.Set(tg.ForwardMap)
	if m == nil {
		t.Fatal("the DNAT map is not in the kernel")
	}
	want := map[string]string{}
	for _, e := range forwardMap(t, tg).Elements {
		want[e.Key] = e.Value
	}
	if got := m.Pairs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the kernel's DNAT map %v, compiled %v", got, want)
	}

	// deleted: the port is the gateway's own again, and with the last forward the map goes
	b.removeForward(fwdWeb)
	b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "refused")
	b.expect(b.srv, "udp", "203.0.113.1", 5353, "ok")
	b.removeForward(fwdAddr)
	b.removeForward(fwdRange)
	b.apply()
	b.expect(b.srv, "udp", "203.0.113.1", 5353, "refused") // nothing listens on the gateway
	if b.current.Set(tg.ForwardMap) != nil {
		t.Error("the DNAT map stays without a forward")
	}
}

// A drop, reject and reset rule on the device stops forwarded connections to it, counted by the rule; a
// rule for another port does not. An existing rule "device -> uplink: drop" stops a forwarded connection
// from behind the uplink once the forward exists, but not one from the management network (which the
// destination `uplink` excludes, whatever allowed_sources says).
func TestRulesOnTheTargetDecideForwardedConnectionsOnTheRealKernel(t *testing.T) {
	b := newForwardBed(t)
	b.w.addForward(fwdWeb, `{name: web, protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}, target_port: 8883}`)
	b.w.addForward(fwdAddr, `{name: ssh, protocol: tcp, entry: uplink, external_port: 8022, target: {device: esp32-42}, target_port: 22}`)
	b.w.addForward(fwdMgmt, `{name: mgmt, protocol: tcp, entry: management, external_port: 8443, target: {device: esp32-42}, target_port: 8883}`)
	b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "ok")
	b.expect(b.m, "tcp", "192.168.56.1", 8443, "ok")

	b.w.addConfigRule(ruleA, `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: drop}`)
	tg := b.apply()
	passed := b.counter(forwardOf(t, tg, fwdWeb).Counter)
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "timeout")
	b.expect(b.srv, "tcp", "203.0.113.1", 8022, "ok") // another target port
	if n := b.counter(tg.Access.Rules[0].Counter); n == 0 {
		t.Error("the rule's counter counts nothing")
	}
	if n := b.counter(forwardOf(t, tg, fwdWeb).Counter); n != passed {
		t.Errorf("the forward counted %d connections the rule dropped", n-passed)
	}
	b.w.cfg = b.replaceRule(ruleA, `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: reject, reject_with: host_unreachable}`)
	b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "error-EHOSTUNREACH")
	b.w.cfg = b.replaceRule(ruleA, `{name: no-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: reset}`)
	b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "refused")

	// "device -> uplink: drop", as a scenario's offline step has it
	b.w.cfg = b.replaceRule(ruleA, `{name: offline, source: {device: esp32-42}, destination: {uplink: true}, action: drop}`)
	b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "timeout")
	b.expect(b.srv, "tcp", "203.0.113.1", 8022, "timeout")
	b.expect(b.m, "tcp", "192.168.56.1", 8443, "ok")
	// also with allowed_sources unset (the default) or narrower than the management subnet: the
	// management network is still not "uplink" (the responder form's own exclusion set)
	allowed := b.w.cfg.Management.AllowedSources
	for _, sources := range []*[]string{nil, ptr([]string{"192.168.56.2/32"})} {
		cfg := *b.w.cfg
		cfg.Management.AllowedSources = sources
		b.w.cfg = &cfg
		b.apply()
		b.expect(b.srv, "tcp", "203.0.113.1", 8080, "timeout")
		b.expect(b.m, "tcp", "192.168.56.1", 8443, "ok")
	}
	cfg := *b.w.cfg
	cfg.Management.AllowedSources = allowed
	b.w.cfg = &cfg
	// an allow rule in front of it opens the forward again
	b.w.ruleOverlay(`{target: {device: esp32-42}, rule: {action: allow, protocol: tcp, ports: [22]}}`, time.Second)
	b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8022, "ok")
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "timeout")
}

// "Also cut existing connections" resets an established forwarded connection: the window of the rule on
// the device resets the peer's packets of the connection the forward translated, with the forward's
// address as their source.
func TestACutWindowResetsAnEstablishedForwardedConnectionOnTheRealKernel(t *testing.T) {
	b := newForwardBed(t)
	b.w.addForward(fwdWeb, `{name: web, protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}, target_port: 8883}`)
	b.w.addForward(fwdAddr, `{name: ssh, protocol: tcp, entry: uplink, external_port: 8022, target: {device: esp32-42}, target_port: 22}`)
	b.apply()
	web := b.srv.Start("python3", "-c", holdScript, "203.0.113.1", "8080")
	ssh := b.srv.Start("python3", "-c", holdScript, "203.0.113.1", "8022")
	waitOutput(t, web, "ok")
	waitOutput(t, ssh, "ok")

	b.w.addConfigRule(ruleA, `{name: cut-mqtt, source: {device: esp32-42}, protocol: tcp, ports: [8883], action: drop, cut_existing: true}`)
	tg := b.apply()
	time.Sleep(time.Second)
	if out := web.Output(); strings.Contains(out, "Error") {
		t.Fatalf("the connection broke without a window: %s", out)
	}
	tx, err := tg.Access.CutTransaction([]string{tg.Access.Rules[0].Key})
	if err != nil {
		t.Fatal(err)
	}
	b.gw.MustStdin(string(tx), "nft", "-j", "-f", "-")
	waitOutput(t, web, "ConnectionResetError")
	tx, _ = tg.Access.CutTransaction(nil)
	b.gw.MustStdin(string(tx), "nft", "-j", "-f", "-")
	time.Sleep(time.Second)
	if out := ssh.Output(); strings.Contains(out, "Error") || strings.Contains(out, "bad") {
		t.Errorf("the forwarded connection to another port was cut: %s", out)
	}
	ssh.Stop()
}

// A forward follows a new address of the entry interface without any change of the ruleset (the
// kernel's lookup of the interface's addresses), and a device's new address by an element update of the
// DNAT map alone (what the engine sends for an identity change).
func TestAForwardFollowsANewEntryAddressAndANewDeviceAddressOnTheRealKernel(t *testing.T) {
	b := newForwardBed(t)
	b.w.addForward(fwdWeb, `{name: web, protocol: tcp, entry: uplink, external_port: 8080, target: {device: esp32-42}, target_port: 8883}`)
	old := b.apply()
	b.expect(b.srv, "tcp", "203.0.113.1", 8080, "ok")

	// the uplink's address changes (a new DHCP lease): the forward takes it at once. The old address goes
	// first: the kernel deletes the secondary addresses of a subnet with its primary one.
	b.gw.Must("ip", "addr", "del", "203.0.113.1/24", "dev", "wan0")
	b.gw.Must("ip", "addr", "add", "203.0.113.2/24", "dev", "wan0")
	b.expect(b.srv, "tcp", "203.0.113.2", 8080, "ok")
	if got := b.try(b.srv, "tcp", "203.0.113.1", 8080); got == "ok" {
		t.Error("the old address of the uplink still forwards") // it is no one's: no answer, or no neighbor
	}

	// the device gets a new address: the element moves, nothing else is written
	b.dev.Must("ip", "addr", "del", "10.10.0.42/24", "dev", "eth0")
	b.dev.Must("ip", "addr", "add", "10.10.0.44/24", "dev", "eth0")
	b.dev.Route("default", "via", "10.10.0.1")
	b.w.setAddrs(map[string][]string{devESP42: {"10.10.0.44"}, devESP43: {"10.10.0.43"}, devLab: {"10.20.0.50"}})
	next := b.w.compile(nil)
	if !sameButMapElements(old, next) {
		t.Fatal("the address change is more than elements")
	}
	u := DiffMap(forwardMap(t, old), forwardMap(t, next))
	tx, err := next.Nft.ElementTransaction([]MapUpdate{u})
	if err != nil {
		t.Fatal(err)
	}
	b.gw.MustStdin(string(tx), "nft", "-j", "-f", "-")
	b.expect(b.srv, "tcp", "203.0.113.2", 8080, "ok")
	rs, err := linux.ParseNft([]byte(b.gw.Must("nft", "-j", "list", "ruleset")))
	if err != nil {
		t.Fatal(err)
	}
	if got := rs.Set(next.ForwardMap).Pairs(); fmt.Sprint(got) != "map[wan0 . tcp . 8080:10.10.0.44 . 8883]" {
		t.Errorf("the kernel's DNAT map after the element update: %v", got)
	}
}
