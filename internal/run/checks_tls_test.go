package run_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// The checks `tls_rejected` and `tls_accepted` (plan §2.8, M21) read the handshakes of the gateway's TLS responder: the
// device is shown the certificate of a case and either aborts the handshake or completes it.

const tlsRun = `
name: tls
target: {device: esp32-42}
steps:
  - {id: case, at: 0s, tls: {case: expired, ports: [8883]}}
checks:
  - {tls_rejected: {protocol: tcp, ports: [8883]}, window: {from: case, within: 20s}}
`

func handshake(device, dst, outcome string, at time.Time) run.TLSHandshake {
	return run.TLSHandshake{Device: device, Client: netip.MustParseAddr("10.10.0.42"), Dst: netip.MustParseAddrPort(dst), SNI: "broker.example.com",
		Case: "expired", Outcome: outcome, Alert: map[string]string{"rejected": "bad_certificate"}[outcome], Time: at}
}

func (h *harness) addHandshake(hs run.TLSHandshake) {
	h.obs.mu.Lock()
	h.obs.handshakes = append(h.obs.handshakes, hs)
	h.obs.mu.Unlock()
}

// A handshake of the target's device that the device aborted, inside the window and to the connection the selector
// names, passes `tls_rejected`; one of another device, another port or before the window does not.
func TestARejectedHandshakeOfTheTargetPassesTheTLSRejectedCheck(t *testing.T) {
	h := newHarness(t)
	r := h.start(tlsRun)
	h.waitStep(r.Id, "case", model.RunStepStatusApplied)
	h.addHandshake(handshake(idESP, "203.0.113.10:8883", "rejected", t0.Add(-time.Minute))) // before the window
	h.addHandshake(handshake(idLab, "203.0.113.10:8883", "rejected", h.clk.Now()))          // another device
	h.addHandshake(handshake(idESP, "203.0.113.10:443", "rejected", h.clk.Now()))           // another port
	h.addHandshake(handshake(idESP, "203.0.113.10:8883", "accepted", h.clk.Now()))          // the other outcome
	h.tick(4)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("decided by %+v", c)
	}
	h.addHandshake(handshake(idESP, "203.0.113.10:8883", "rejected", h.clk.Now()))
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if c.Evidence == nil || len(*c.Evidence) != 1 {
		t.Fatalf("evidence %v", c.Evidence)
	}
	ev := (*c.Evidence)[0]
	if ev["kind"] != "tls_handshake" || ev["device"] != idESP || ev["dst"] != "203.0.113.10:8883" || ev["outcome"] != "rejected" || ev["alert"] != "bad_certificate" || ev["sni"] != "broker.example.com" {
		t.Errorf("evidence %v", ev)
	}
	if !strings.Contains(*c.Message, "aborted the handshake with the alert bad_certificate") {
		t.Errorf("%s", *c.Message)
	}
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// A device that completes the handshake took a certificate it must reject: the check fails, and the message says so.
func TestADeviceThatCompletesTheHandshakeFailsTheTLSRejectedCheck(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(tlsRun, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "case", model.RunStepStatusApplied)
	h.addHandshake(handshake(idESP, "203.0.113.10:8883", "accepted", h.clk.Now()))
	h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	c := h.check(r.Id, 0)
	if c.Status != model.CheckResultStatusFailed || !strings.Contains(*c.Message, "the responder saw 1 accepted") || !strings.Contains(*c.Message, "took a certificate it must reject") {
		t.Errorf("%+v", c)
	}
	if h.get(r.Id).Status != model.RunStatusFailed {
		t.Errorf("%s", h.get(r.Id).Status)
	}
}

// `tls_accepted` is the finding: the device completed the handshake. A rejection fails it, and says what was seen.
func TestTheTLSAcceptedCheckPassesOnACompletedHandshakeAndFailsOnARejection(t *testing.T) {
	accepted := strings.Replace(strings.Replace(tlsRun, "tls_rejected", "tls_accepted", 1), "within: 20s", "within: 3s", 1)
	h := newHarness(t)
	r := h.start(accepted)
	h.waitStep(r.Id, "case", model.RunStepStatusApplied)
	h.addHandshake(handshake(idESP, "203.0.113.10:8883", "accepted", h.clk.Now()))
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if !strings.Contains(*c.Message, "completed the handshake") || !strings.Contains(*c.Message, "it took the certificate") {
		t.Errorf("%s", *c.Message)
	}

	h2 := newHarness(t)
	r2 := h2.start(accepted)
	h2.waitStep(r2.Id, "case", model.RunStepStatusApplied)
	h2.addHandshake(handshake(idESP, "203.0.113.10:8883", "rejected", h2.clk.Now()))
	h2.tickUntil("the end of the run", func() bool { return h2.get(r2.Id).Status != model.RunStatusRunning })
	c = h2.check(r2.Id, 0)
	if c.Status != model.CheckResultStatusFailed || !strings.Contains(*c.Message, "completed a handshake") || !strings.Contains(*c.Message, "the responder saw 1 rejected") {
		t.Errorf("%+v", c)
	}
	if strings.Contains(*c.Message, "must reject") {
		t.Errorf("a rejection is not a finding: %s", *c.Message)
	}
}

// A check without any handshake in its window says that the responder saw none, which is the likely cause: no case selected the connection.
func TestACheckWithoutAHandshakeSaysTheResponderSawNone(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(tlsRun, "within: 20s", "within: 3s", 1))
	h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	c := h.check(r.Id, 0)
	if c.Status != model.CheckResultStatusFailed || !strings.Contains(*c.Message, "the responder saw no handshake of it") || !strings.Contains(*c.Message, "tcp/8883") {
		t.Errorf("%+v", c)
	}
}

// The responder reports a handshake after it has ended, in batches: one that ended inside the window and is reported
// shortly after the window closed still counts, and one that ended after the window does not.
func TestAHandshakeOfTheWindowThatIsReportedAfterItsEndStillCounts(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(tlsRun, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "case", model.RunStepStatusApplied)
	end := h.windowEnd(r.Id, 0)
	h.tickUntil("the end of the window", func() bool { return !h.clk.Now().Before(end.Add(250 * time.Millisecond)) })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("the check was decided at the end of the window, before the report could arrive: %+v", c)
	}
	h.addHandshake(handshake(idESP, "203.0.113.10:8883", "rejected", end.Add(-200*time.Millisecond)))
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)

	h2 := newHarness(t)
	r2 := h2.start(strings.Replace(tlsRun, "within: 20s", "within: 3s", 1))
	h2.waitStep(r2.Id, "case", model.RunStepStatusApplied)
	end2 := h2.windowEnd(r2.Id, 0)
	h2.addHandshake(handshake(idESP, "203.0.113.10:8883", "rejected", end2.Add(500*time.Millisecond)))
	h2.tickUntil("the end of the run", func() bool { return h2.get(r2.Id).Status != model.RunStatusRunning })
	if c := h2.check(r2.Id, 0); c.Status != model.CheckResultStatusFailed {
		t.Errorf("%+v", c)
	}
}

// The selector is the connection the device meant: a destination that names an address, a network or the uplink.
func TestTheDestinationOfATLSCheckIsTheOriginalDestination(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: tls
target: {device: esp32-42}
steps:
  - {id: case, at: 0s, tls: {case: untrusted_ca}}
checks:
  - {tls_rejected: {protocol: tcp, destination: {cidr: 198.51.100.0/24}}, window: {from: case, within: 20s}, name: elsewhere}
  - {tls_rejected: {protocol: tcp, destination: {uplink: true}, ports: [8883]}, window: {from: case, within: 20s}, name: the uplink}
`)
	h.waitStep(r.Id, "case", model.RunStepStatusApplied)
	h.addHandshake(handshake(idESP, "203.0.113.10:8883", "rejected", h.clk.Now()))
	h.tickUntil("the check of the uplink", func() bool { return h.check(r.Id, 1).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 1, model.CheckResultStatusPassed)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Errorf("the destination 198.51.100.0/24 matched 203.0.113.10: %+v", c)
	}
}

// A TLS step is a step of its own: a case of the responder, an overlay of the run, with a counter of the connections it held
// (the handshakes of the responder); a case that held none is a zero hit, like a fault that matched no packet, and the "TLS
// broken" profile that held some is not.
func TestATLSCaseThatHeldNoConnectionIsAZeroHitWarning(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["hit"] = &model.Counter{Packets: 2, Epoch: 1}
	h.obs.stepCounters["miss"] = &model.Counter{Epoch: 1}
	h.obs.stepCounters["profile"] = &model.Counter{Packets: 1, Epoch: 1}
	r := h.start(`
name: tlszero
target: {device: esp32-42}
steps:
  - {id: hit,     at: 0s, tls: {case: expired, ports: [8883]}}
  - {id: miss,    at: 1s, tls: {case: self_signed, ports: [443]}}
  - {id: profile, at: 2s, profile: tls-broken}
`)
	h.waitStep(r.Id, "hit", model.RunStepStatusApplied)
	if len(h.gw.writes) != 1 || h.gw.writes[0].Request.Tls == nil || h.gw.writes[0].Request.Tls.Case != model.TlsCaseCaseExpired {
		t.Fatalf("%+v", h.gw.writes)
	}
	h.advance(time.Second)
	h.waitStep(r.Id, "miss", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := deref(done.Warnings)
	if len(ws) != 1 || ws[0].Code != model.ZeroHit || *ws[0].Step != "miss" || !strings.Contains(ws[0].Message, "the TLS case of step miss held no connection") {
		t.Fatalf("warnings %+v", ws)
	}
}
