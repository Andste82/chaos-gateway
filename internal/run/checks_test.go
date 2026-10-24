package run_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// Checks (plan §2.10, M16) on the fake clock, with the connections and the DNS log of a fake observer. The checks
// look at the gateway every 250 ms, so the tests let the clock run in steps of that size and sync on the
// observer's reads: a read that began after the test changed the connections sees the change, and the read after
// it has finished with the one before.

// outage is the scenario of plan §2.10 in short: cut the broker's port, restore, and expect the device to
// reconnect within 30 s after the restore.
const outage = `
name: outage
target: {device: esp32-42}
steps:
  - {id: cut,     at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 5s, restore: true}
checks:
  - {reconnected: {protocol: tcp, ports: [8883]}, window: {from: restore, within: 30s}}
`

func tcpFlow(id string, port int, state string) engine.Flow {
	return engine.Flow{ID: id, Device: idESP, Network: idIoT, Protocol: "tcp", Src: netip.MustParseAddr("10.10.0.31"),
		Dst: netip.MustParseAddr("203.0.113.10"), SPort: 40000, DPort: port, State: state,
		Upload: engine.Traffic{Packets: 3, Bytes: 200}, Download: engine.Traffic{Packets: 2, Bytes: 300}}
}

func (o *fakeObserver) polls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.flowPolls
}

// change edits the connection with the id.
func (o *fakeObserver) change(id string, f func(*engine.Flow)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.flows {
		if o.flows[i].ID == id {
			f(&o.flows[i])
			return
		}
	}
	panic("no flow " + id)
}

func (o *fakeObserver) failFlows(err error) {
	o.mu.Lock()
	o.flowErr = err
	o.mu.Unlock()
}

// afterPoll lets the clock run until the checks have read the connections once more.
func (h *harness) afterPoll() {
	h.t.Helper()
	n := h.obs.polls()
	deadline := time.Now().Add(10 * time.Second)
	for h.obs.polls() <= n {
		if time.Now().After(deadline) {
			h.t.Fatal("the checks did not read the connections")
		}
		h.clk.Advance(250 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
}

// settle runs two polls: the first reads what the test changed, the second starts when the first has been
// evaluated.
func (h *harness) settle() {
	h.t.Helper()
	h.afterPoll()
	h.afterPoll()
}

// tick lets the clock run for n polls without waiting for any of them.
func (h *harness) tick(n int) {
	for i := 0; i < n; i++ {
		h.clk.Advance(250 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
	}
}

// tickUntil lets the clock run in the steps of a poll until the condition holds.
func (h *harness) tickUntil(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		h.clk.Advance(250 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
}

// tickArmedUntil lets the clock run in the steps of a poll until the condition holds, taking each step only once a
// timer is pending: for the phase of a run in which the loop of the checks is the only one that waits on the clock.
// The loop arms its next wake only after it has evaluated a poll; a step taken before that moves the clock past the
// wake unseen, and a loaded machine takes many such steps, so the run ends seconds late on the fake clock.
func (h *harness) tickArmedUntil(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		eventually(h.t, what, func() bool { return h.clk.Pending() > 0 || cond() })
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		h.clk.Advance(250 * time.Millisecond)
	}
}

func (h *harness) check(id uuid.UUID, i int) model.CheckResult {
	h.t.Helper()
	r := h.get(id)
	if r.Checks == nil || len(*r.Checks) <= i {
		h.t.Fatalf("the run has no check %d: %+v", i, r.Checks)
	}
	return (*r.Checks)[i]
}

// windowEnd waits for the window of a check to be closed on the timeline and returns its end.
func (h *harness) windowEnd(id uuid.UUID, i int) time.Time {
	h.t.Helper()
	eventually(h.t, "the end of the window", func() bool { return h.check(id, i).WindowEnd != nil })
	return *h.check(id, i).WindowEnd
}

func (h *harness) waitCheck(id uuid.UUID, i int, want model.CheckResultStatus) model.CheckResult {
	h.t.Helper()
	var c model.CheckResult
	eventually(h.t, "check "+string(want), func() bool {
		c = h.check(id, i)
		return c.Status == want
	})
	return c
}

// untilRestore starts the run of the outage and moves it to the moment the restore has been applied: the
// window of the check is open and its baseline has been read.
func (h *harness) untilRestore(yaml string) model.Run {
	h.t.Helper()
	r := h.start(yaml)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(5 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	h.afterPoll() // the baseline
	return r
}

// The baseline poll finds connections that began before the window and connections that began after it opened and
// before the poll: only the second kind is news (a device that retries as the fault is lifted would otherwise fail).
func TestAConnectionThatBeganBetweenTheOpeningOfTheWindowAndTheBaselineCounts(t *testing.T) {
	h := newHarness(t)
	old, fresh := tcpFlow("old", 8883, "ESTABLISHED"), tcpFlow("fresh", 8883, "ESTABLISHED")
	old.StartedAt, fresh.StartedAt = t0.Add(4*time.Second), t0.Add(5*time.Second) // the restore is applied at 5 s
	fresh.SPort = 40001
	h.obs.addFlow(old)
	h.obs.addFlow(fresh)
	r := h.start(outage)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(5 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if c.Evidence == nil || len(*c.Evidence) != 1 || (*c.Evidence)[0]["sport"] != 40001 {
		t.Errorf("only the connection that began in the window is evidence: %v", c.Evidence)
	}
}

// A connection that began before the window and is found by the baseline poll is not news.
func TestAConnectionThatBeganBeforeTheWindowIsNotNewAtTheBaseline(t *testing.T) {
	h := newHarness(t)
	old := tcpFlow("old", 8883, "ESTABLISHED")
	old.StartedAt = t0.Add(4 * time.Second)
	h.obs.addFlow(old)
	r := h.untilRestore(outage)
	h.settle()
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("the check was decided by %+v", c)
	}
}

// A connection that the device establishes inside the window passes the check at once, the run lasts until then
// and passes with it. A connection that is only trying (SYN sent) does not count, one that was there before the
// window does not, one of another port or another device does not.
func TestReconnectedPassesOnANewEstablishedConnectionInsideTheWindow(t *testing.T) {
	h := newHarness(t)
	h.obs.addFlow(tcpFlow("old", 8883, "ESTABLISHED")) // there before the window
	r := h.untilRestore(outage)

	cur := h.get(r.Id)
	if cur.Status != model.RunStatusRunning {
		t.Fatalf("the run ended with the last step while its check is open: %s", cur.Status)
	}
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending || c.WindowStart == nil || c.WindowEnd == nil ||
		c.WindowEnd.Sub(*c.WindowStart) != 30*time.Second {
		t.Fatalf("an open check: %+v", c)
	}
	other := tcpFlow("other-device", 8883, "ESTABLISHED")
	other.Device = idLab
	other.Network = idHub
	for _, f := range []engine.Flow{tcpFlow("wrong-port", 443, "ESTABLISHED"), other, tcpFlow("handshake", 8883, "SYN_SENT")} {
		h.obs.addFlow(f)
	}
	h.settle()
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("the check was decided by %+v", c)
	}
	h.obs.change("handshake", func(f *engine.Flow) { f.State = "ESTABLISHED" })
	h.afterPoll()
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if c.Message == nil || !strings.Contains(*c.Message, "tcp 10.10.0.31:40000 to 203.0.113.10:8883") {
		t.Errorf("message %v", c.Message)
	}
	if c.Evidence == nil || len(*c.Evidence) != 1 || (*c.Evidence)[0]["dport"] != 8883 || (*c.Evidence)[0]["state"] != "ESTABLISHED" {
		t.Errorf("evidence %v", c.Evidence)
	}
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays are left", n)
	}
	if end := *done.EndedAt; end.Sub(*done.StartedAt) >= 30*time.Second {
		t.Errorf("a run whose checks were decided early lasted %v", end.Sub(*done.StartedAt))
	}
}

// When the window closes without the connection, the check has failed, and so has the run (not `error`: the engine
// did what was asked).
func TestReconnectedFailsWhenTheWindowClosesWithoutAConnection(t *testing.T) {
	h := newHarness(t)
	h.obs.addFlow(tcpFlow("old", 8883, "ESTABLISHED"))
	r := h.untilRestore(outage)
	h.tickArmedUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	done := h.get(r.Id)
	if done.Status != model.RunStatusFailed || done.Reason == nil || !strings.Contains(*done.Reason, "reconnected tcp/8883") {
		t.Fatalf("run %s: %v", done.Status, done.Reason)
	}
	c := h.check(r.Id, 0)
	if c.Status != model.CheckResultStatusFailed || c.Message == nil || !strings.Contains(*c.Message, "no connection matching tcp/8883 was established in the window of 30s") {
		t.Errorf("check %+v", c)
	}
	// the window was 30 s long, from the restore
	if d := done.EndedAt.Sub(*done.StartedAt); d < 35*time.Second || d > 38*time.Second {
		t.Errorf("the run lasted %v: the restore at 5 s and 30 s of window", d)
	}
	junit, err := h.m.JUnit(r.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(junit), `<failure message="no connection matching tcp/8883`) || !strings.Contains(string(junit), `failures="1"`) {
		t.Errorf("a failed check is a failure case of the suite:\n%s", junit)
	}
	rep, err := h.m.Report(r.Id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checks == nil || (*rep.Checks)[0].Status != model.CheckResultStatusFailed {
		t.Errorf("the report has no result of the check: %+v", rep.Checks)
	}
}

// A connection of the window has to be established inside the window: one that began before the window but
// was answered inside it counts.
func TestAConnectionAnsweredInsideTheWindowCountsEvenIfItBeganBefore(t *testing.T) {
	h := newHarness(t)
	h.obs.addFlow(tcpFlow("trying", 8883, "SYN_SENT"))
	r := h.untilRestore(outage)
	h.afterPoll()
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("a connection that is only trying: %+v", c)
	}
	h.obs.change("trying", func(f *engine.Flow) { f.State = "ESTABLISHED" })
	h.afterPoll()
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
}

// The same tuple again after its entry went (a device that reuses its source port) is a new connection.
func TestTheSameTupleAgainAfterItsEntryWentIsANewConnection(t *testing.T) {
	h := newHarness(t)
	h.obs.addFlow(tcpFlow("again", 8883, "ESTABLISHED"))
	r := h.untilRestore(outage)
	h.afterPoll()
	h.obs.mu.Lock()
	h.obs.flows = nil // the entry went
	h.obs.mu.Unlock()
	h.settle()
	h.obs.addFlow(tcpFlow("again", 8883, "ESTABLISHED"))
	h.afterPoll()
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
}

const forbidden = `
name: forbidden
target: {device: esp32-42}
steps:
  - {id: cut,  at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: done, at: 2s, restore: true}
checks:
  - {no_connection: {protocol: tcp, ports: [8883]}, window: {from: cut, until: done}}
`

// `no_connection` fails as soon as a connection is established inside the window; one that stays in the
// handshake (what a drop rule does to it) is not established.
func TestNoConnectionFailsOnAnEstablishedConnectionAndNotOnAHandshake(t *testing.T) {
	h := newHarness(t)
	r := h.start(forbidden)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.afterPoll() // the baseline
	h.obs.addFlow(tcpFlow("handshake", 8883, "SYN_SENT"))
	h.settle()
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("a handshake that goes nowhere ended the window: %+v", c)
	}
	h.obs.addFlow(tcpFlow("leak", 8883, "ESTABLISHED"))
	h.afterPoll()
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusFailed)
	if c.Message == nil || !strings.Contains(*c.Message, "tcp 10.10.0.31:40000 to 203.0.113.10:8883 (established)") || c.Evidence == nil {
		t.Errorf("check %+v", c)
	}
	h.advance(2 * time.Second)
	done := h.waitStatus(r.Id, model.RunStatusFailed)
	if done.Reason == nil || !strings.Contains(*done.Reason, "no_connection tcp/8883") {
		t.Errorf("reason %v", done.Reason)
	}
}

// `no_connection` passes when its window, which ends with a step, closes with no connection established.
// A connection after the window does not matter.
func TestNoConnectionPassesWhenTheWindowToItsEndStepCloses(t *testing.T) {
	h := newHarness(t)
	r := h.start(forbidden)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.afterPoll()
	h.obs.addFlow(tcpFlow("handshake", 8883, "SYN_SENT"))
	h.afterPoll()
	h.advance(2 * time.Second) // the step `done` closes the window
	h.waitStep(r.Id, "done", model.RunStepStatusApplied)
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if d := c.WindowEnd.Sub(*c.WindowStart); c.WindowEnd == nil || d < 2*time.Second || d > 3*time.Second {
		t.Errorf("the window of the check ended %v after it began", d)
	}
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// `traffic_seen` counts any packet of the selector inside the window: a connection that is only trying, or an old
// connection that carries packets again; silence fails it.
func TestTrafficSeenCountsNewConnectionsAndPacketsOfOldOnes(t *testing.T) {
	const yaml = `
name: traffic
target: {device: esp32-42}
steps:
  - {id: cut,  at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: done, at: 2s, restore: true}
checks:
  - {traffic_seen: {destination: {cidr: 203.0.113.0/24}}, window: {from: done, within: 10s}, name: the device talks to the broker}
`
	t.Run("a new connection", func(t *testing.T) {
		h := newHarness(t)
		r := h.start(yaml)
		h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
		h.advance(2 * time.Second)
		h.waitStep(r.Id, "done", model.RunStepStatusApplied)
		h.afterPoll()
		elsewhere := tcpFlow("elsewhere", 8883, "ESTABLISHED")
		elsewhere.Dst = netip.MustParseAddr("198.51.100.1")
		h.obs.addFlow(elsewhere)
		h.settle()
		if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
			t.Fatalf("traffic to another destination decided: %+v", c)
		}
		h.obs.addFlow(tcpFlow("trying", 1883, "SYN_SENT"))
		h.afterPoll()
		c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
		if c.Name != "the device talks to the broker" {
			t.Errorf("name %q", c.Name)
		}
		h.waitStatus(r.Id, model.RunStatusPassed)
	})
	t.Run("packets of an old connection", func(t *testing.T) {
		h := newHarness(t)
		h.obs.addFlow(tcpFlow("old", 8883, "ESTABLISHED"))
		r := h.start(yaml)
		h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
		h.advance(2 * time.Second)
		h.waitStep(r.Id, "done", model.RunStepStatusApplied)
		h.afterPoll()
		h.settle()
		if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
			t.Fatalf("an idle old connection decided: %+v", c)
		}
		h.obs.change("old", func(f *engine.Flow) { f.Upload.Packets += 5 })
		h.afterPoll()
		h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	})
	t.Run("silence", func(t *testing.T) {
		h := newHarness(t)
		h.obs.addFlow(tcpFlow("old", 8883, "ESTABLISHED"))
		r := h.start(yaml)
		h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
		h.advance(2 * time.Second)
		h.waitStep(r.Id, "done", model.RunStepStatusApplied)
		h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
		if done := h.get(r.Id); done.Status != model.RunStatusFailed {
			t.Errorf("%s: %v", done.Status, done.Reason)
		}
		if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusFailed || !strings.Contains(*c.Message, "no traffic matching any to 203.0.113.0/24 was seen") {
			t.Errorf("%+v", c)
		}
	})
}

// `traffic_not_seen` ignores the destinations of `except`, fails on any other, passes when nothing happened.
func TestTrafficNotSeenIgnoresTheExceptionsAndFailsOnAnyOtherTraffic(t *testing.T) {
	const yaml = `
name: quiet
target: {device: esp32-42}
steps:
  - {id: cut,  at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: done, at: 3s, restore: true}
checks:
  - {traffic_not_seen: {except: [{cidr: 203.0.113.0/24}]}, window: {from: cut, until: done}}
`
	other := tcpFlow("unexpected", 443, "SYN_SENT")
	other.Dst = netip.MustParseAddr("198.51.100.7")
	t.Run("fails", func(t *testing.T) {
		h := newHarness(t)
		r := h.start(yaml)
		h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
		h.afterPoll()
		h.obs.addFlow(tcpFlow("expected", 8883, "ESTABLISHED"))
		h.settle()
		if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
			t.Fatalf("traffic to an excepted destination decided: %+v", c)
		}
		h.obs.addFlow(other)
		h.afterPoll()
		c := h.waitCheck(r.Id, 0, model.CheckResultStatusFailed)
		if !strings.Contains(*c.Message, "198.51.100.7:443") {
			t.Errorf("%v", *c.Message)
		}
		if !strings.Contains(c.Name, "traffic_not_seen any except 203.0.113.0/24, from cut until done") {
			t.Errorf("the derived name is %q", c.Name)
		}
	})
	t.Run("passes", func(t *testing.T) {
		h := newHarness(t)
		r := h.start(yaml)
		h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
		h.afterPoll()
		h.obs.addFlow(tcpFlow("expected", 8883, "ESTABLISHED"))
		h.afterPoll()
		h.advance(3 * time.Second)
		h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
		h.waitStatus(r.Id, model.RunStatusPassed)
	})
}

const dnsOutage = `
name: dns
target: {device: esp32-42}
steps:
  - {id: cut,     at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 2s, restore: true}
checks:
  - {dns_query_seen: {name: broker.example.com}, window: {from: restore, within: 20s}}
`

// `dns_query_seen` wants a query for the name by a device of the target inside the window: one before the window
// does not count, nor one of another name or another device; `*.suffix` selects names below it.
func TestADNSQueryIsSeenOnlyInsideTheWindowFromADeviceOfTheTarget(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true
	h.obs.queries = append(h.obs.queries, query{device: idESP, name: "broker.example.com", at: t0.Add(-time.Minute)})
	r := h.start(dnsOutage)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(2 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	h.obs.mu.Lock()
	h.obs.queries = append(h.obs.queries,
		query{device: idESP, name: "other.example.com", at: h.clk.Now()},
		query{device: idLab, name: "broker.example.com", at: h.clk.Now()})
	h.obs.mu.Unlock()
	h.tick(4)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("decided by %+v", c)
	}
	h.obs.mu.Lock()
	h.obs.queries = append(h.obs.queries, query{device: idESP, name: "broker.example.com", at: h.clk.Now()})
	h.obs.mu.Unlock()
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if c.Evidence == nil || (*c.Evidence)[0]["name"] != "broker.example.com" || (*c.Evidence)[0]["device"] != idESP {
		t.Errorf("evidence %v", c.Evidence)
	}
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// A query that is stamped after the window has closed does not pass the check, one stamped at its last moment does.
func TestADNSQueryAfterTheWindowDoesNotCount(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true
	r := h.start(strings.Replace(dnsOutage, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(2 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	end := h.windowEnd(r.Id, 0)
	// the query is in the log (the observer does not look at the time), but it was asked after the window had closed
	h.obs.mu.Lock()
	h.obs.queries = append(h.obs.queries, query{device: idESP, name: "broker.example.com", at: end.Add(500 * time.Millisecond)})
	h.obs.mu.Unlock()
	h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusFailed || !strings.Contains(*c.Message, "no device of the target asked for broker.example.com in the window of 3s") {
		t.Errorf("%+v", c)
	}
	if done := h.get(r.Id); done.Status != model.RunStatusFailed {
		t.Errorf("%s", done.Status)
	}
}

func TestADNSQueryAtTheLastMomentOfTheWindowCounts(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true
	r := h.start(strings.Replace(dnsOutage, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(2 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	end := h.windowEnd(r.Id, 0)
	h.obs.mu.Lock()
	h.obs.queries = append(h.obs.queries, query{device: idESP, name: "broker.example.com", at: end})
	h.obs.mu.Unlock()
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
}

// The DNS log arrives after the query (the proxy logs it after the answer, in batches): a query asked inside the window
// that is in the log shortly after the window closed still passes the check; its time stamp decides, not the poll.
func TestADNSQueryOfTheWindowThatIsLoggedAfterItsEndStillCounts(t *testing.T) {
	h := newHarness(t)
	h.obs.dns[idESP] = true
	r := h.start(strings.Replace(dnsOutage, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(2 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	end := h.windowEnd(r.Id, 0)
	h.tickUntil("the end of the window", func() bool { return !h.clk.Now().Before(end.Add(250 * time.Millisecond)) })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("the check was decided at the end of the window, before the log could arrive: %+v", c)
	}
	h.obs.mu.Lock()
	h.obs.queries = append(h.obs.queries, query{device: idESP, name: "broker.example.com", at: end.Add(-200 * time.Millisecond)})
	h.obs.mu.Unlock()
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
}

// A window from `start` is open from the beginning of the timeline.
func TestAWindowFromStartOpensWithTheTimeline(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: fromstart
target: {device: esp32-42}
steps:
  - {id: slow, at: 0s, fault: {latency: 10ms}}
  - {id: end,  at: 4s, restore: true}
checks:
  - {traffic_seen: {}, window: {from: start, until: end}}
`)
	h.waitStep(r.Id, "slow", model.RunStepStatusApplied)
	h.afterPoll()
	if c := h.check(r.Id, 0); c.WindowStart == nil || !c.WindowStart.Equal(*h.get(r.Id).StartedAt) {
		t.Errorf("the window of a check from start: %+v", c)
	}
	h.obs.addFlow(tcpFlow("a", 1883, "SYN_SENT"))
	h.afterPoll()
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	h.advance(4 * time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// A check of a connection that cannot read the connections where its window closes cannot say either way: the
// check is an error, and so is the run.
func TestACheckThatCannotReadTheConnectionsIsAnErrorNotAPass(t *testing.T) {
	h := newHarness(t)
	r := h.untilRestore(strings.Replace(strings.Replace(outage, "reconnected", "no_connection", 1), "within: 30s", "within: 3s", 1))
	h.obs.failFlows(errors.New("conntrack: netlink error"))
	h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	done := h.get(r.Id)
	c := h.check(r.Id, 0)
	if done.Status != model.RunStatusError || c.Status != model.CheckResultStatusError || !strings.Contains(*c.Message, "conntrack: netlink error") {
		t.Errorf("run %s (%v), check %+v", done.Status, done.Reason, c)
	}
	junit, _ := h.m.JUnit(r.Id)
	if !strings.Contains(string(junit), "<error message=") || !strings.Contains(string(junit), `errors="2"`) {
		t.Errorf("an undecided check and the run that ended as error are errors of the suite:\n%s", junit)
	}
}

// A run that ends before its windows do (an abort, a step that fails) leaves its checks pending, and says why.
func TestAnAbortedRunLeavesItsChecksPendingWithTheReason(t *testing.T) {
	h := newHarness(t)
	r := h.untilRestore(outage)
	if _, err := h.m.Abort(context.Background(), r.Id, &token); err != nil {
		t.Fatal(err)
	}
	done := h.get(r.Id)
	c := h.check(r.Id, 0)
	if done.Status != model.RunStatusAborted || c.Status != model.CheckResultStatusPending || c.Message == nil ||
		!strings.Contains(*c.Message, "not decided: the run ended as aborted (aborted by ci)") {
		t.Errorf("run %s, check %+v", done.Status, c)
	}
	junit, _ := h.m.JUnit(r.Id)
	if !strings.Contains(string(junit), `<skipped message="not decided before the run ended"`) {
		t.Errorf("a check that was not decided is skipped in the suite:\n%s", junit)
	}
}

func TestAStepThatFailsEndsTheRunWithItsChecksPending(t *testing.T) {
	h := newHarness(t)
	h.gw.putErr = func(w engine.OverlayWrite) error {
		if w.Run.Step == "bad" {
			return errors.New("apply failed")
		}
		return nil
	}
	r := h.start(`
name: stepfails
target: {device: esp32-42}
steps:
  - {id: ok,  at: 0s, fault: {latency: 10ms}}
  - {id: bad, at: 1s, rule: {action: drop, protocol: tcp, ports: [8883]}}
checks:
  - {traffic_seen: {}, window: {from: bad, within: 10s}}
`)
	h.waitStep(r.Id, "ok", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusError)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending || !strings.Contains(*c.Message, "the run ended as error") {
		t.Errorf("%+v (run: %v)", c, done.Reason)
	}
}

// The timeline is not held up by the checks: a step is applied on time while a check of an earlier window is open and
// the observer is slow (here: it does not answer at all).
func TestTheChecksNeverDelayTheSteps(t *testing.T) {
	h := newHarness(t)
	// every read of the connections hangs until the end of the test body: the check is stuck in its first poll
	release := make(chan struct{})
	h.obs.mu.Lock()
	h.obs.block = release
	h.obs.mu.Unlock()
	r := h.start(`
name: parallel
target: {device: esp32-42}
steps:
  - {id: a, at: 0s,  fault: {latency: 10ms}}
  - {id: b, at: 2s,  rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: c, at: 5s,  restore: true}
checks:
  - {no_connection: {protocol: tcp, ports: [8883]}, window: {from: start, until: c}}
`)
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	h.advance(2 * time.Second)
	h.waitStep(r.Id, "b", model.RunStepStatusApplied)
	h.advance(3 * time.Second)
	h.waitStep(r.Id, "c", model.RunStepStatusApplied)
	if got := h.get(r.Id).Status; got != model.RunStatusRunning {
		t.Errorf("the run is %s while its check has not been decided", got)
	}
	close(release)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	for id, off := range map[string]int{"a": 0, "b": 2000, "c": 5000} {
		if got := *step(done, id).OffsetMs; got != off {
			t.Errorf("step %s was applied at %d ms, due at %d", id, got, off)
		}
	}
}

// Every decision of a check is announced and written to the run's event file.
func TestTheDecisionOfACheckIsAnEventOfTheRun(t *testing.T) {
	h := newHarness(t)
	h.obs.addFlow(tcpFlow("old", 8883, "ESTABLISHED"))
	events, unsub := h.gw.Subscribe()
	defer unsub()
	r := h.untilRestore(outage)
	h.obs.addFlow(tcpFlow("new", 8883, "ESTABLISHED"))
	h.afterPoll()
	h.waitStatus(r.Id, model.RunStatusPassed)
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != engine.EventRunCheck {
				continue
			}
			if ev.Data["status"] != "passed" || ev.Data["check"] != 0 || ev.Data["run"] != r.Id.String() || ev.Data["name"] != "outage" {
				t.Errorf("event %+v", ev.Data)
			}
			return
		case <-timeout:
			t.Fatal("no run_check event")
		}
	}
}

// The name of a check is the one the scenario gives it, else one derived from its type, selector and window.
func TestTheNameOfACheckIsGivenOrDerived(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: names
target: {device: esp32-42}
preconditions: {skip: [uses_gateway_dns]}
steps:
  - {id: a, at: 0s, fault: {latency: 10ms}}
  - {id: b, at: 1h, restore: true}
checks:
  - {reconnected: {protocol: tcp, ports: [8883, 8884]}, window: {from: a, within: 30s}}
  - {no_connection: {destination: {uplink: true}}, window: {from: a, until: b}}
  - {traffic_seen: {protocol: udp, port_ranges: [{from: 5000, to: 5010}], destination: {network: IoT}}, window: {from: start, within: 1s}}
  - {dns_query_seen: {name: broker.example.com}, window: {from: a, within: 5s}}
  - {dns_query_seen: {name: x.example.com}, window: {from: a, within: 5s}, name: asks for x}
`)
	var got []string
	for _, c := range *h.get(r.Id).Checks {
		got = append(got, string(c.Type)+"|"+c.Name)
	}
	want := []string{
		"reconnected|reconnected tcp/8883,8884, from a within 30s",
		"no_connection|no_connection any to the uplink, from a until b",
		"traffic_seen|traffic_seen udp/5000-5010 to network IoT, from start within 1s",
		"dns_query_seen|dns_query_seen broker.example.com, from a within 5s",
		"dns_query_seen|asks for x",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("names:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i, c := range *h.get(r.Id).Checks {
		if c.Index != i || c.Status != model.CheckResultStatusPending {
			t.Errorf("check %d: %+v", i, c)
		}
	}
	h.m.AbortOwned(context.Background(), nil, "test over")
}

// ---- zero hits (plan §2.10)

// A fault or rule of the run that matched no packet is a warning of the report; one that matched some is not.
func TestAFaultThatMatchedNoPacketIsAZeroHitWarning(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["hit"] = &model.Counter{Packets: 12, Bytes: 900, Epoch: 1}
	h.obs.stepCounters["miss"] = &model.Counter{Epoch: 1}
	h.obs.stepCounters["rule-miss"] = &model.Counter{Epoch: 1}
	r := h.start(`
name: zerohit
target: {device: esp32-42}
steps:
  - {id: hit,       at: 0s, fault: {latency: 10ms}}
  - {id: miss,      at: 1s, fault: {loss: 1%, destination: {cidr: 198.51.100.0/24}}}
  - {id: rule-miss, at: 2s, rule: {action: drop, protocol: tcp, ports: [8883]}}
`)
	h.waitStep(r.Id, "hit", model.RunStepStatusApplied)
	h.advance(time.Second)
	h.waitStep(r.Id, "miss", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := deref(done.Warnings)
	if len(ws) != 2 || ws[0].Code != model.ZeroHit || *ws[0].Step != "miss" || ws[1].Code != model.ZeroHit || *ws[1].Step != "rule-miss" {
		t.Fatalf("warnings %+v", ws)
	}
	if ws[0].Overlay == nil || !strings.Contains(ws[0].Message, "the fault of step miss matched no packet") || !strings.Contains(ws[1].Message, "the rule of step rule-miss") {
		t.Errorf("warnings %+v", ws)
	}
	junit, _ := h.m.JUnit(r.Id)
	if !strings.Contains(string(junit), "warning zero_hit: the fault of step miss matched no packet") || strings.Contains(string(junit), "<failure") {
		t.Errorf("a warning is in the output of the suite, not a failure:\n%s", junit)
	}
}

// A step that replaces the overlay of an earlier one is judged by what the overlay matched from then on.
func TestAReplacingStepIsJudgedByWhatTheOverlayMatchedSinceItTookOver(t *testing.T) {
	const yaml = `
name: replace
target: {device: esp32-42}
steps:
  - {id: slow,  at: 0s, fault: {latency: 200ms}}
  - {id: lossy, at: 5s, fault: {latency: 200ms, loss: 10%}}
  - {id: end,   at: 9s, restore: true}
`
	run := func(t *testing.T, lossyCounter model.Counter) model.Run {
		h := newHarness(t)
		h.obs.stepCounters["slow"] = &model.Counter{Packets: 7, Epoch: 1}
		h.obs.stepCounters["lossy"] = &lossyCounter
		r := h.start(yaml)
		h.waitStep(r.Id, "slow", model.RunStepStatusApplied)
		h.advance(5 * time.Second)
		h.waitStep(r.Id, "lossy", model.RunStepStatusApplied)
		h.advance(4 * time.Second)
		return h.waitStatus(r.Id, model.RunStatusPassed)
	}
	t.Run("the counter goes on", func(t *testing.T) {
		// the overlay keeps its counter: 7 packets are still all that `slow` matched, so `lossy` matched none
		done := run(t, model.Counter{Packets: 7, Epoch: 1})
		ws := deref(done.Warnings)
		if len(ws) != 1 || *ws[0].Step != "lossy" {
			t.Errorf("warnings %+v", ws)
		}
	})
	t.Run("the counter goes on and grows", func(t *testing.T) {
		done := run(t, model.Counter{Packets: 9, Epoch: 1})
		if ws := deref(done.Warnings); len(ws) != 0 {
			t.Errorf("warnings %+v", ws)
		}
	})
	t.Run("the counter starts over", func(t *testing.T) {
		done := run(t, model.Counter{Packets: 3, Epoch: 2})
		if ws := deref(done.Warnings); len(ws) != 0 {
			t.Errorf("warnings %+v", ws)
		}
		done = run(t, model.Counter{Packets: 0, Epoch: 2})
		if ws := deref(done.Warnings); len(ws) != 1 || *ws[0].Step != "lossy" {
			t.Errorf("warnings %+v", ws)
		}
	})
}

// With `zero_hit: error` a run that would have passed fails, and the JUnit file has a failed case for the step.
func TestZeroHitErrorFailsARunThatWouldHavePassed(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["miss"] = &model.Counter{Epoch: 1}
	r := h.start(`
name: strict
target: {device: esp32-42}
zero_hit: error
steps:
  - {id: miss, at: 0s, fault: {loss: 1%}}
`)
	done := h.waitStatus(r.Id, model.RunStatusFailed)
	if done.Reason == nil || !strings.Contains(*done.Reason, "zero hits: the overlay of step miss matched no packet") {
		t.Errorf("reason %v", done.Reason)
	}
	if ws := deref(done.Warnings); len(ws) != 1 || ws[0].Code != model.ZeroHit {
		t.Errorf("warnings %+v", ws)
	}
	junit, _ := h.m.JUnit(r.Id)
	if !strings.Contains(string(junit), `<testcase name="zero hits of step miss"`) || !strings.Contains(string(junit), `type="zero_hit"`) {
		t.Errorf("the suite has no failed case for the step:\n%s", junit)
	}
}

// A counter that could not be read gives no verdict, a run that did not reach its end gives no warnings, and
// neither does a step that is not a fault, rule or profile.
func TestNoZeroHitWarningWithoutAReadingOrForARunThatDidNotFinish(t *testing.T) {
	h := newHarness(t)
	// no counter for "unread": the observer could not read it
	r := h.start(`
name: unread
target: {device: esp32-42}
steps:
  - {id: unread, at: 0s, fault: {loss: 1%}}
`)
	if done := h.waitStatus(r.Id, model.RunStatusPassed); len(deref(done.Warnings)) != 0 {
		t.Errorf("warnings %+v", done.Warnings)
	}

	h2 := newHarness(t)
	h2.obs.stepCounters["slow"] = &model.Counter{Epoch: 1}
	r2 := h2.start(`
name: aborted
target: {device: esp32-42}
steps:
  - {id: slow, at: 0s, fault: {latency: 10ms}}
  - {id: end,  at: 1h, restore: true}
`)
	h2.waitStep(r2.Id, "slow", model.RunStepStatusApplied)
	if _, err := h2.m.Abort(context.Background(), r2.Id, &token); err != nil {
		t.Fatal(err)
	}
	if done := h2.get(r2.Id); done.Status != model.RunStatusAborted || len(deref(done.Warnings)) != 0 {
		t.Errorf("%s: %+v", done.Status, done.Warnings)
	}
}

// A failed check and a zero-hit error: the run fails once, the reasons stay those of the checks.
func TestAFailedCheckKeepsItsReasonWhenAZeroHitIsAnErrorToo(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["cut"] = &model.Counter{Epoch: 1}
	r := h.untilRestore(strings.Replace(outage, "name: outage", "name: both\nzero_hit: error", 1))
	h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	done := h.get(r.Id)
	if done.Status != model.RunStatusFailed || !strings.Contains(*done.Reason, "reconnected tcp/8883") {
		t.Errorf("%s: %v", done.Status, done.Reason)
	}
	if ws := deref(done.Warnings); len(ws) != 1 || *ws[0].Step != "cut" {
		t.Errorf("%+v", ws)
	}
}

// The profile `normal` impairs nothing on purpose (it wins its family so that the faults beneath it stop): it matches no
// packet and is no zero hit; a profile that impairs is judged like a fault.
func TestAProfileThatImpairsNothingIsNoZeroHit(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["normal"] = &model.Counter{Epoch: 1}
	h.obs.stepCounters["lte"] = &model.Counter{Epoch: 1}
	r := h.start(`
name: profiles
target: {device: esp32-42}
steps:
  - {id: normal, at: 0s, profile: normal}
  - {id: lte, at: 1s, profile: lte, target: {device: esp32-42}}
`)
	h.waitStep(r.Id, "normal", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := deref(done.Warnings)
	if len(ws) != 1 || *ws[0].Step != "lte" || !strings.Contains(ws[0].Message, "the profile of step lte") {
		t.Errorf("warnings %+v", ws)
	}
}

// holdingTheRestore is the outage with its two windows, and the restore held in the engine: the moment the
// kernel lets the device through again and the moment the engine answers that it has, which on a machine that is
// slow to apply (a nested virtual machine, seconds) are far apart.
const holdingTheRestore = `
name: outage
target: {device: esp32-42}
steps:
  - {id: cut,     at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 5s, restore: true}
checks:
  - {name: nothing while cut, no_connection: {protocol: tcp, ports: [8883]}, window: {from: cut, until: restore}}
  - {name: back, reconnected: {protocol: tcp, ports: [8883]}, window: {from: restore, within: 30s}}
`

// A step takes effect somewhere between the moment the timeline hands it to the engine and the moment the engine
// answers. A connection that the device makes inside the apply of the restore is no failure of "nothing while cut"
// (the window of a negative check ends when the restore is handed over) and counts for "back" (the window of a
// positive check starts then): both checks looked at the answer once, and failed a device that was fine on a
// machine where an apply takes seconds.
func TestAConnectionMadeWhileTheRestoreIsBeingAppliedBelongsToTheWindowAfterIt(t *testing.T) {
	h := newHarness(t)
	h.gw.resetBlock = make(chan struct{})
	r := h.start(holdingTheRestore)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(5 * time.Second) // the restore is handed over and held
	h.tickUntil("the end of the window of the cut", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPassed {
		t.Fatalf("the window of the cut closes when the restore is handed over: %+v", c)
	}
	if st := h.stepStatus(r.Id, "restore"); st != model.RunStepStatusPending {
		t.Fatalf("the restore was answered before the test let it: %s", st)
	}

	back := tcpFlow("back", 8883, "ESTABLISHED")
	back.StartedAt = h.clk.Now()
	h.obs.addFlow(back)
	h.tickUntil("the check back", func() bool { return h.check(r.Id, 1).Status != model.CheckResultStatusPending })
	c := h.check(r.Id, 1)
	if c.Status != model.CheckResultStatusPassed {
		t.Fatalf("a connection made while the restore is applied: %+v", c)
	}

	close(h.gw.resetBlock)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	s := step(done, "restore")
	if s.StartOffsetMs == nil || *s.StartOffsetMs < 5000 || s.OffsetMs == nil || *s.OffsetMs <= *s.StartOffsetMs {
		t.Fatalf("the step records when it was handed over (not before 5 s) and, later, when it was answered: %+v", s)
	}
	if start := time.Duration(*s.StartOffsetMs) * time.Millisecond; c.WindowStart == nil || c.WindowStart.Sub(*done.StartedAt) != start {
		t.Errorf("the window starts when the restore is handed over (%v after the start), not when it is answered (%v): %v", start, time.Duration(*s.OffsetMs)*time.Millisecond, c.WindowStart)
	}
}

// The other direction: a connection made while the cut is being applied is still the cut's business, and a
// negative check does not count it (its window starts at the answer); a positive check of the same step counts it.
func TestAConnectionMadeWhileTheCutIsBeingAppliedIsNotInTheWindowOfANegativeCheck(t *testing.T) {
	h := newHarness(t)
	h.gw.block = make(chan struct{})
	r := h.start(`
name: cut
target: {device: esp32-42}
steps:
  - {id: cut, at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 5s, restore: true}
checks:
  - {name: quiet, no_connection: {protocol: tcp, ports: [8883]}, window: {from: cut, until: restore}}
  - {name: seen, traffic_seen: {protocol: tcp, ports: [8883]}, window: {from: cut, within: 10s}}
`)
	// the cut is handed over and held: the window of the second check is open, the one of the first is not
	eventually(t, "the second check to open", func() bool { return h.check(r.Id, 1).WindowStart != nil })
	if c := h.check(r.Id, 0); c.WindowStart != nil {
		t.Fatalf("the window of a negative check opens when the cut is answered: %+v", c)
	}
	early := tcpFlow("early", 8883, "ESTABLISHED")
	early.StartedAt = h.clk.Now()
	h.obs.addFlow(early)
	h.tickUntil("the check seen", func() bool { return h.check(r.Id, 1).Status != model.CheckResultStatusPending })
	if c := h.check(r.Id, 1); c.Status != model.CheckResultStatusPassed {
		t.Fatalf("%+v", c)
	}
	close(h.gw.block)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.advance(5 * time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPassed {
		t.Errorf("the connection of the apply is not in the window: %+v", c)
	}
}

// P4-AUDIT-02: a poll that fails where the window opens does not arm the check. The first poll that works is the
// baseline, so a connection that began before the window (established before the cut) does not pass
// `reconnected from restore`.
func TestAFailedReadWhereTheWindowOpensDoesNotArmTheCheck(t *testing.T) {
	h := newHarness(t)
	r := h.start(outage)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.obs.failFlows(errors.New("conntrack: timeout"))
	h.advance(5 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	h.afterPoll() // the read that fails
	h.obs.failFlows(nil)
	old := tcpFlow("old", 8883, "ESTABLISHED")
	old.StartedAt = t0.Add(4 * time.Second) // before the restore
	h.obs.addFlow(old)
	h.settle()
	h.settle()
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("a connection from before the window decided the check: %+v", c)
	}
	// a connection that begins inside the window does
	fresh := tcpFlow("fresh", 8883, "ESTABLISHED")
	fresh.SPort = 40001
	fresh.StartedAt = h.clk.Now()
	h.obs.addFlow(fresh)
	h.afterPoll()
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
}

// P4-AUDIT-19: a connection that began after the window of a fixed length closed is not one of the window, although the
// poll that reads it comes before the check is decided.
func TestAConnectionThatBeganAfterTheWindowClosedDoesNotFailNoConnection(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: short
target: {device: esp32-42}
steps:
  - {id: cut,  at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: done, at: 5s, restore: true}
checks:
  - {no_connection: {protocol: tcp, ports: [8883]}, window: {from: cut, within: 1s}}
`)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.afterPoll() // the baseline
	late := tcpFlow("late", 8883, "ESTABLISHED")
	late.StartedAt = h.get(r.Id).StartedAt.Add(1100 * time.Millisecond) // the restored path lets it through after the window
	h.obs.addFlow(late)
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	c := h.check(r.Id, 0)
	if c.Status != model.CheckResultStatusPassed || c.Evidence != nil {
		t.Errorf("a connection after the window decided the check: %+v", c)
	}
}

// P4-AUDIT-19, the mirror: a connection that began inside the window and was consumed by a poll before the check opened
// (another check was open, the read was under way while the step was handed over) belongs to the check that opens in
// the next iteration.
func TestAConnectionOfThePollBeforeTheOpeningBelongsToTheCheckThatOpens(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: mirror
target: {device: esp32-42}
steps:
  - {id: cut,     at: 0s, rule: {action: drop, protocol: tcp, ports: [8883]}}
  - {id: restore, at: 5s, restore: true}
checks:
  - {traffic_seen: {destination: {cidr: 198.51.100.0/24}}, window: {from: start, within: 1h}}
  - {reconnected: {protocol: tcp, ports: [8883]}, window: {from: restore, within: 30s}}
`)
	h.waitStep(r.Id, "cut", model.RunStepStatusApplied)
	h.afterPoll()
	// the loop is held in a read that begins before the step is handed over
	release := make(chan struct{})
	h.obs.mu.Lock()
	h.obs.block = release
	h.obs.mu.Unlock()
	n := h.obs.polls()
	h.tickUntil("the read", func() bool { return h.obs.polls() > n })
	h.advance(5 * time.Second)
	h.waitStep(r.Id, "restore", model.RunStepStatusApplied)
	back := tcpFlow("back", 8883, "ESTABLISHED")
	back.StartedAt = h.clk.Now()
	h.obs.addFlow(back)
	close(release)
	h.waitCheck(r.Id, 1, model.CheckResultStatusPassed)
}

// P4-AUDIT-04: `uplink` and `network` are what the rules and the capture selector say: the networks behind a hub's
// clients are the hub's and not uplink, and so are the management prefixes.
func TestTheUplinkAndNetworkDestinationsOfACheckAreTheOnesOfTheRules(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: nets
target: {device: esp32-42}
steps:
  - {id: a, at: 0s, fault: {latency: 10ms}}
  - {id: b, at: 60s, restore: true}
checks:
  - {traffic_not_seen: {destination: {uplink: true}}, window: {from: a, until: b}}
  - {traffic_seen: {destination: {network: lab-hub}}, window: {from: a, until: b}}
`)
	h.waitStep(r.Id, "a", model.RunStepStatusApplied)
	h.afterPoll()
	behindClient, management := tcpFlow("behind-client", 22, "ESTABLISHED"), tcpFlow("management", 443, "ESTABLISHED")
	behindClient.Dst, management.Dst = netip.MustParseAddr("10.50.0.9"), netip.MustParseAddr("192.168.88.5")
	management.SPort = 40001
	h.obs.addFlow(behindClient)
	h.obs.addFlow(management)
	h.settle()
	h.waitCheck(r.Id, 1, model.CheckResultStatusPassed)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("traffic to a network behind a client or to a management address is uplink traffic: %+v", c)
	}
	h.advance(60 * time.Second)
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	h.waitStatus(r.Id, model.RunStatusPassed)
}
