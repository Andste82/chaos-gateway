package run_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// A check or a wait that names a host (plan §2.10, M20): the addresses of the name are those the gateway's DNS
// answered the device for names the selector matches.

const outageByName = `
name: outage
target: {device: esp32-42}
steps:
  - {id: cut,     at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 5s, restore: true}
checks:
  - {reconnected: {destination: {hostname: broker.example.com}, protocol: tcp, ports: [8883]}, window: {from: restore, within: 30s}}
`

func TestAConnectionToTheAddressOfTheHostPassesTheCheckAndOneToAnotherAddressDoesNot(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true                                          // the device uses the gateway's DNS (the precondition of a scenario with a host name)
	h.obs.answer("10.10.0.31", "203.0.113.10", "broker.example.com") // the device asked for the broker
	r := h.untilRestore(outageByName)

	// the connection of the device to an address of another name is not the one the check waits for
	other := tcpFlow("elsewhere", 8883, "ESTABLISHED")
	other.Dst = netip.MustParseAddr("198.51.100.7")
	h.obs.addFlow(other)
	h.settle()
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("the check was decided by %+v", c)
	}
	h.obs.addFlow(engine.Flow{ID: "broker", Device: idESP, Network: idIoT, Protocol: "tcp", Src: netip.MustParseAddr("10.10.0.31"), Dst: netip.MustParseAddr("203.0.113.10"),
		SPort: 40001, DPort: 8883, State: "ESTABLISHED", Upload: engine.Traffic{Packets: 3, Bytes: 200}, Download: engine.Traffic{Packets: 2, Bytes: 300}})
	h.afterPoll()
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if c.Evidence == nil || len(*c.Evidence) != 1 || (*c.Evidence)[0]["dst"] != "203.0.113.10" {
		t.Errorf("evidence %v", c.Evidence)
	}
}

// A suffix selector matches the subdomains the device asked for (and the CNAME targets of the answers), not the suffix itself.
func TestASuffixOfAHostNameMatchesTheNamesOfTheAnswers(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true // the device uses the gateway's DNS (the precondition of a scenario with a host name)
	h.obs.answer("10.10.0.31", "203.0.113.10", "mqtt.eu.example.com", "edge.cdn.example.net")
	r := h.untilRestore(`
name: outage
target: {device: esp32-42}
steps:
  - {id: cut,     at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 5s, restore: true}
checks:
  - {reconnected: {destination: {hostname: "*.example.com"}, protocol: tcp, ports: [8883]}, window: {from: restore, within: 30s}}
  - {traffic_seen: {destination: {hostname: "*.cdn.example.net"}}, window: {from: restore, within: 30s}}
  - {traffic_seen: {destination: {hostname: example.com}}, window: {from: restore, within: 30s}}
`)
	h.obs.addFlow(tcpFlow("a", 8883, "ESTABLISHED"))
	h.afterPoll()
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	h.waitCheck(r.Id, 1, model.CheckResultStatusPassed)
	if c := h.check(r.Id, 2); c.Status == model.CheckResultStatusPassed {
		t.Errorf("*.example.com matches the apex: %+v", c)
	}
}

// traffic_not_seen with an exception that names a host: traffic to the addresses of the name is allowed.
func TestTrafficNotSeenExceptsTheAddressesOfAHost(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true // the device uses the gateway's DNS (the precondition of a scenario with a host name)
	h.obs.answer("10.10.0.31", "203.0.113.10", "ntp.example.com")
	r := h.start(`
name: quiet
target: {device: esp32-42}
steps:
  - {id: cut, at: 0s, rule: {action: drop}}
checks:
  - {traffic_not_seen: {except: [{hostname: ntp.example.com}]}, window: {from: cut, within: 5s}}
`)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.afterPoll()
	h.obs.addFlow(tcpFlow("ntp", 123, "ESTABLISHED")) // to the excepted host: allowed
	h.settle()
	if c := h.check(r.Id, 0); c.Status == model.CheckResultStatusFailed {
		t.Fatalf("traffic to the excepted host failed the check: %+v", c)
	}
	other := tcpFlow("other", 443, "ESTABLISHED")
	other.Dst = netip.MustParseAddr("198.51.100.7")
	other.SPort = 40002
	h.obs.addFlow(other)
	h.afterPoll()
	h.waitCheck(r.Id, 0, model.CheckResultStatusFailed)
}

// `connected` with a destination that names a host waits for a new established connection to an address of the name.
func TestAWaitForAConnectionToAHostNeedsAnAddressOfTheName(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true
	h.obs.answer("10.10.0.31", "203.0.113.10", "broker.example.com")
	r := h.start(`
name: conn
target: {device: esp32-42}
steps:
  - {id: conn,  at: 0s, wait: {for: connected, destination: {hostname: broker.example.com}, protocol: tcp, ports: [8883], timeout: 30s}}
  - {id: after, at: 1s, fault: {latency: 10ms}}
`)
	poll := func() { h.advance(250 * time.Millisecond) }
	for i := 0; i < 3; i++ {
		poll()
	}
	other := tcpFlow("elsewhere", 8883, "ESTABLISHED")
	other.Dst = netip.MustParseAddr("198.51.100.7")
	h.obs.addFlow(other)
	poll()
	poll()
	if st := h.stepStatus(r.Id, "conn"); st != model.RunStepStatusPending {
		t.Fatalf("the wait ended on %s", st)
	}
	h.obs.addFlow(tcpFlow("broker", 8883, "ESTABLISHED"))
	poll()
	h.waitStep(r.Id, "conn", model.RunStepStatusApplied)
	h.advance(1500 * time.Millisecond)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// The step type `dns` (plan §2.10, M20) makes an overlay of the kind dns for the run, and a profile step with "DNS broken" makes a profile
// activation: both are the run's, and `remove` takes the first away again.
func TestADNSStepMakesADNSOverlayOwnedByTheRunAndRemoveTakesItAway(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true
	r := h.start(`
name: dnsstep
target: {device: esp32-42}
steps:
  - {id: broken, at: 0s, dns: {action: servfail, names: [broker.example.com]}}
  - {id: slow,   at: 1s, dns: {action: delay, delay: 2s}}
  - {id: gone,   at: 2s, remove: broken}
  - {id: end,    at: 10s, remove: slow}
`)
	// the last step is far off: a run that has done its steps takes its overlays out, and the test looks at them before that
	h.waitStep(r.Id, "broken", model.RunStepStatusApplied)
	ovs := h.gw.overlays()
	if len(ovs) != 1 || ovs[0].Kind != model.OverlayKindDns || ovs[0].Dns == nil || ovs[0].Dns.Action != model.DnsFaultActionServfail ||
		ovs[0].Owner.Type != model.ActorTypeRun || ovs[0].Owner.Id != r.Id.String() || ovs[0].Run == nil || ovs[0].Run.Step != "broken" {
		t.Fatalf("%+v", ovs)
	}
	h.advance(time.Second)
	h.waitStep(r.Id, "slow", model.RunStepStatusApplied)
	if n := len(h.gw.overlays()); n != 2 {
		t.Fatalf("%d overlays", n)
	}
	h.advance(time.Second)
	h.waitStep(r.Id, "gone", model.RunStepStatusApplied)
	left := h.gw.overlays()
	if len(left) != 1 || left[0].Dns == nil || left[0].Dns.Action != model.DnsFaultActionDelay {
		t.Errorf("%+v", left)
	}
	h.m.AbortOwned(context.Background(), nil, "test over")
}

// A DNS fault step that changed no answer is a zero hit, like a fault that matched no packet; one that changed some, and the
// "DNS broken" profile that did, is not.
func TestADNSFaultThatChangedNoAnswerIsAZeroHitWarning(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true
	h.obs.stepCounters["hit"] = &model.Counter{Packets: 4, Epoch: 1}
	h.obs.stepCounters["miss"] = &model.Counter{Epoch: 1}
	h.obs.stepCounters["profile"] = &model.Counter{Packets: 2, Epoch: 1}
	r := h.start(`
name: dnszero
target: {device: esp32-42}
steps:
  - {id: hit,     at: 0s, dns: {action: nxdomain, names: [a.example.com]}}
  - {id: miss,    at: 1s, dns: {action: servfail, names: [b.example.com]}}
  - {id: profile, at: 2s, profile: dns-broken}
`)
	h.waitStep(r.Id, "hit", model.RunStepStatusApplied)
	h.advance(time.Second)
	h.waitStep(r.Id, "miss", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := deref(done.Warnings)
	if len(ws) != 1 || ws[0].Code != model.ZeroHit || *ws[0].Step != "miss" || !strings.Contains(ws[0].Message, "the DNS fault of step miss changed no answer") {
		t.Fatalf("warnings %+v", ws)
	}
}
