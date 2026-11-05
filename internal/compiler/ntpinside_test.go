package compiler

import (
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// ntpInsideWarnings are the warnings about a server a time fault cannot reach.
func ntpInsideWarnings(tg *Target) (out []string) {
	for _, p := range tg.Problems {
		if p.Code == CodeNTPInside && p.Severity == SevWarning {
			out = append(out, p.Network+": "+p.Message)
		}
	}
	return out
}

// A server inside the segments of the device's network is switched directly, so no redirect catches it: the preview says so
// instead of the overlay being `effective` and the device keeping the right time. The gateway's own address and a server
// outside the segments have a redirect and no warning.
func TestATimeFaultForAServerInsideTheNetworkWarns(t *testing.T) {
	w := newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 10.10.0.5}, offset: +400d}}`, 0)
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 10.10.0.1}, offset: +400d}}`, 0)
	w.overlay(`{target: {device: esp32-43}, time: {destination: {cidr: 203.0.113.5}, offset: +400d}}`, time.Second)
	tg := compileTime(t, w, true)
	got := ntpInsideWarnings(tg)
	if len(got) != 1 || !strings.HasPrefix(got[0], "IoT: ") || !strings.Contains(got[0], "10.10.0.5/32") {
		t.Errorf("%q", got)
	}
	// the overlay is still a winner, so the warning is the only sign of the limit
	if len(tg.Winners) != 3 {
		t.Errorf("%v", tg.Winners)
	}
	// a server of another network is outside the segments of the device's network: it is redirected
	w = newFaultWorld(t)
	w.overlay(`{target: {device: esp32-42}, time: {destination: {cidr: 10.20.0.5}, offset: +400d}}`, 0)
	if got := ntpInsideWarnings(compileTime(t, w, true)); len(got) != 0 {
		t.Errorf("a server of another network is redirected: %q", got)
	}
}

// A fault without a destination applies to every server, and the NTP server that DHCP hands out inside the network is one the
// redirect misses.
func TestATimeFaultWarnsForTheNTPServerDHCPHandsOutInsideTheNetwork(t *testing.T) {
	w := newFaultWorld(t)
	withDHCP(w.cfg, iotNet, &model.DhcpScope{Options: &model.DhcpOptions{NtpServers: &[]string{"10.10.0.5", "192.0.2.9"}}})
	w.overlay(`{target: {device: esp32-42}, time: {offset: -90s}}`, 0)
	got := ntpInsideWarnings(compileTime(t, w, true))
	if len(got) != 1 || !strings.HasPrefix(got[0], "IoT: ") || !strings.Contains(got[0], "10.10.0.5") || strings.Contains(got[0], "192.0.2.9") {
		t.Errorf("%q", got)
	}
	// no fault, no warning
	w = newFaultWorld(t)
	withDHCP(w.cfg, iotNet, &model.DhcpScope{Options: &model.DhcpOptions{NtpServers: &[]string{"10.10.0.5"}}})
	if got := ntpInsideWarnings(compileTime(t, w, true)); len(got) != 0 {
		t.Errorf("without a time fault: %q", got)
	}
	// the gateway as the server (the default) is redirected
	w = newFaultWorld(t)
	withDHCP(w.cfg, iotNet, &model.DhcpScope{})
	w.overlay(`{target: {device: esp32-42}, time: {offset: -90s}}`, 0)
	if got := ntpInsideWarnings(compileTime(t, w, true)); len(got) != 0 {
		t.Errorf("the gateway as the server: %q", got)
	}
}

// The redirects to the server and the accepts towards it match the source address of the networks the interface covers: a
// packet with a spoofed source (the management network, another test network) is neither redirected nor forwarded, so its
// reply cannot leave as an established flow past the management guard and the access rules.
func TestTheNTPRedirectsAndTheAcceptsMatchTheSourceAddressOfTheNetworks(t *testing.T) {
	w := newFaultWorld(t)
	withNetworkNTP(iotNet, &model.NetworkNtp{RedirectHardcoded: ptr(true)})(w.cfg)
	tg := compileTime(t, w, true)
	var redirects int
	for _, r := range ntpNAT(t, tg) {
		redirects++
		if !strings.Contains(r, " ip saddr ") {
			t.Errorf("a redirect without the source address: %s", r)
		}
	}
	// the gateway's address and the hardcoded servers of IoT (UDP and TCP each) and the gateway's address of Lab
	if redirects != 6 {
		t.Errorf("%d redirects", redirects)
	}
	var accepts int
	for _, r := range renderRules(chainOf(t, tg, "forward")) {
		if strings.Contains(r, "oifname svc0") && strings.Contains(r, "ip daddr 169.254.100.2") && !strings.Contains(r, "dport 53") {
			accepts++
			if !strings.Contains(r, " ip saddr { 10.10.0.0/24, 10.20.0.0/24, 10.30.0.0/24 } ") {
				t.Errorf("an accept without the source addresses of the networks: %s", r)
			}
		}
	}
	if accepts != 2 {
		t.Errorf("%d accepts", accepts)
	}
}
