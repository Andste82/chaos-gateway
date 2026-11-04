package run_test

import (
	"context"
	"maps"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Andste82/chaos-gateway/internal/domain"
	"github.com/Andste82/chaos-gateway/internal/engine"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/run"
)

// The time family in scenarios (plan §2.18, M34): the step `time`, the wait `ntp_query_seen`, the check `ntp_queried` and the
// zero-hit warning of a time fault. The runner reads the queries of the gateway's NTP server through the observer; these tests
// run on the fake clock with a fake observer, the real log is the API's (internal/api).

// addNTP records a query that the gateway's NTP server logged.
func (h *harness) addNTP(q run.NTPQuery) {
	h.obs.mu.Lock()
	h.obs.ntp = append(h.obs.ntp, q)
	h.obs.mu.Unlock()
}

// The domain accepts the wait and the check from the specification on and the runner decides both, so a scenario that has them
// is created; the capabilities list the check.
func TestTheNtpWaitAndTheNtpCheckAreAcceptedWhenTheRunIsCreated(t *testing.T) {
	h := newHarness(t)
	for name, yaml := range map[string]string{
		"wait": `
name: n
target: {device: esp32-42}
steps:
  - {id: a, at: 0s, fault: {latency: 10ms}}
  - {id: w, at: 1s, wait: {for: ntp_query_seen, timeout: 30s}}
`,
		"check": `
name: n
target: {device: esp32-42}
steps: [{id: a, at: 0s, fault: {latency: 10ms}}]
checks:
  - {ntp_queried: {}, window: {from: a, within: 5s}}
`,
	} {
		sc, err := domain.DecodeScenario([]byte(yaml), domain.FormatYAML)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err = h.m.Create(context.Background(), run.CreateRequest{Inline: sc, Queue: true, Owner: token}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !contains(run.SupportedCheckTypes, "ntp_queried") || !contains(run.SupportedStepTypes, "time") {
		t.Errorf("the capabilities do not list the time family: %v %v", run.SupportedStepTypes, run.SupportedCheckTypes)
	}
}

// ---- the wait

const ntpWait = `
name: ntpwait
target: {device: esp32-42}
steps:
  - {id: q, at: 0s, wait: {for: ntp_query_seen, timeout: 30s}}
  - {id: after, at: 1s, fault: {latency: 10ms}}
`

// `ntp_query_seen` wants a query of a device of the target that arrived after the wait began, whatever the server answered: one
// from before and one of another device do not end it, a silenced query does. (That a rate-limited query and an NTS-KE
// connection are no queries is decided where the observer lists them, and tested there: internal/api.)
func TestAWaitForAnNtpQueryNeedsAQueryOfTheTargetAfterTheWaitBegan(t *testing.T) {
	h := newHarness(t)
	h.addNTP(run.NTPQuery{Device: idESP, Time: t0.Add(-time.Minute), Answer: "normal"})
	r := h.start(ntpWait)
	h.advance(250 * time.Millisecond)
	h.advance(250 * time.Millisecond)
	h.addNTP(run.NTPQuery{Device: idLab, Time: h.clk.Now(), Answer: "normal"})
	h.advance(250 * time.Millisecond)
	h.advance(250 * time.Millisecond)
	if st := h.stepStatus(r.Id, "q"); st != model.RunStepStatusPending {
		t.Fatalf("a query from before the wait or of another device ended it: %s", st)
	}
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "silence", Fault: "0194f4f0-0000-7000-8000-000000000001"})
	h.advance(250 * time.Millisecond)
	h.waitStep(r.Id, "q", model.RunStepStatusApplied)
	// the step after the wait is shifted by the waiting time, as after any wait
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	if s := step(done, "q"); s.Status != model.RunStepStatusApplied || s.Error != nil {
		t.Errorf("%+v", s)
	}
	if s := step(done, "after"); *s.OffsetMs < 2000 {
		t.Errorf("the step after the wait was not shifted by it: %d ms", *s.OffsetMs)
	}
}

// A wait for a query that does not come ends the run as error, and the reason says what a missing query can mean.
func TestAWaitForAnNtpQueryThatTimesOutEndsTheRunAsError(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(ntpWait, "timeout: 30s", "timeout: 2s", 1))
	for i := 0; i < 9; i++ {
		h.advance(250 * time.Millisecond)
	}
	done := h.waitStatus(r.Id, model.RunStatusError)
	if !has(*done.Reason, "step q: the wait for an NTP query timed out after 2s: no NTP query seen") || !has(*done.Reason, "hardcoded server") {
		t.Errorf("reason %v", *done.Reason)
	}
	if s := step(done, "after"); s.Status != model.RunStepStatusSkipped {
		t.Errorf("%+v", s)
	}
}

// A wait on the target group sees the query of any device of it.
func TestAWaitForAnNtpQueryOfAGroupEndsWithAnyDeviceOfIt(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(ntpWait, "target: {device: esp32-42}", "target: {group: sensors}", 1))
	h.advance(250 * time.Millisecond)
	if st := h.stepStatus(r.Id, "q"); st != model.RunStepStatusPending {
		t.Fatalf("%s", st)
	}
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"})
	h.advance(250 * time.Millisecond)
	h.waitStep(r.Id, "q", model.RunStepStatusApplied)
	h.advance(time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// A query stamped at the very moment the wait began counts: the observer's `since` is inclusive, as the window of a check is.
func TestAnNtpQueryStampedAtTheStartOfTheWaitEndsIt(t *testing.T) {
	h := newHarness(t)
	h.addNTP(run.NTPQuery{Device: idESP, Time: t0, Answer: "normal"})
	r := h.start(ntpWait)
	h.advance(250 * time.Millisecond)
	h.waitStep(r.Id, "q", model.RunStepStatusApplied)
}

// ---- the check

const ntpCheck = `
name: ntpcheck
target: {device: esp32-42}
steps:
  - {id: ahead, at: 0s, time: {offset: +400d}}
  - {id: back, at: 2s, remove: ahead}
checks:
  - {ntp_queried: {}, window: {from: ahead, within: 20s}}
`

// `ntp_queried` wants a query of a device of the target inside the window: one before it, one of another device and one that is
// not in the target do not count. The evidence and the message say how the server answered.
func TestAnNtpQueryIsSeenOnlyInsideTheWindowFromADeviceOfTheTarget(t *testing.T) {
	h := newHarness(t)
	h.addNTP(run.NTPQuery{Device: idESP, Time: t0.Add(-time.Minute), Answer: "normal"})
	r := h.start(ntpCheck)
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.addNTP(run.NTPQuery{Device: idLab, Time: h.clk.Now(), Answer: "normal"})
	h.tick(4)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("decided by %+v", c)
	}
	ms := int64(400 * 24 * time.Hour / time.Millisecond)
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal", OffsetMs: &ms, Fault: "0194f4f0-0000-7000-8000-000000000001", Server: "203.0.113.123:123"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if c.Evidence == nil || len(*c.Evidence) != 1 {
		t.Fatalf("evidence %v", c.Evidence)
	}
	ev := (*c.Evidence)[0]
	if ev["kind"] != "ntp_query" || ev["device"] != idESP || ev["answer"] != "normal" || ev["offset_ms"] != ms ||
		ev["fault"] != "0194f4f0-0000-7000-8000-000000000001" || ev["server"] != "203.0.113.123:123" {
		t.Errorf("evidence %v", ev)
	}
	if !has(*c.Message, "a device asked the NTP server for the time") || !has(*c.Message, "answered: normal, served time +9600h0m0s off") {
		t.Errorf("%s", *c.Message)
	}
	h.advance(2 * time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// `ntp_queried` is a positive check: its window opens when the step is handed over, because a query can come while the
// engine is still applying the step, and it closes (`until`) when the step is answered. A negative check would open at the
// answer and close at the hand-over and fail a device that is fine.
func TestAnNtpQueryWhileTheStepOfTheWindowIsBeingAppliedCounts(t *testing.T) {
	h := newHarness(t)
	h.gw.block = make(chan struct{})
	r := h.start(ntpCheck)
	eventually(t, "the window to open", func() bool { return h.check(r.Id, 0).WindowStart != nil })
	if st := h.stepStatus(r.Id, "ahead"); st != model.RunStepStatusPending {
		t.Fatalf("the step was answered before the test let it: %s", st)
	}
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPassed {
		t.Fatalf("a query inside the apply of the step: %+v", c)
	}
	close(h.gw.block)
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.advance(2 * time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

func TestAnNtpQueryWhileTheStepThatClosesTheWindowIsBeingAppliedCounts(t *testing.T) {
	h := newHarness(t)
	h.gw.resetBlock = make(chan struct{})
	r := h.start(`
name: until
target: {device: esp32-42}
steps:
  - {id: ahead, at: 0s, time: {offset: +400d}}
  - {id: back, at: 5s, restore: true}
checks:
  - {ntp_queried: {}, window: {from: ahead, until: back}}
`)
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.advance(5 * time.Second) // the restore is handed over and held
	h.tick(4)
	if st := h.stepStatus(r.Id, "back"); st != model.RunStepStatusPending {
		t.Fatalf("the restore was answered before the test let it: %s", st)
	}
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("the window closes when the restore is answered, not when it is handed over: %+v", c)
	}
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPassed {
		t.Fatalf("a query inside the apply of the restore: %+v", c)
	}
	close(h.gw.resetBlock)
	h.waitStatus(r.Id, model.RunStatusPassed)
}

// The message names how far the served time is from the correct one: with its sign, to the second from a second on, to the
// millisecond below, and not at all for an answer that is right.
func TestTheMessageOfAnNtpQueriedCheckSaysHowFarTheServedTimeIs(t *testing.T) {
	for _, tc := range []struct {
		ms   int64
		want string
	}{
		{-86400000, "(answered: normal, served time -24h0m0s off)"},
		{250, "(answered: normal, served time +250ms off)"},
		{-250, "(answered: normal, served time -250ms off)"},
		{1499, "(answered: normal, served time +1s off)"},
		{0, "(answered: normal)"},
		// further than a Duration reaches (about 292 years): an absolute time in the year 2400, and the smallest value
		{11775715200000, "(answered: normal, served time more than 292 years off)"},
		{-11775715200000, "(answered: normal, served time more than 292 years off)"},
		{math.MinInt64, "(answered: normal, served time more than 292 years off)"},
	} {
		h := newHarness(t)
		r := h.start(ntpCheck)
		h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
		ms := tc.ms
		h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal", OffsetMs: &ms})
		h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
		c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
		if !has(*c.Message, tc.want) {
			t.Errorf("offset %d ms: %q does not say %q", tc.ms, *c.Message, tc.want)
		}
		h.m.AbortOwned(context.Background(), nil, "test over")
	}
}

// A query that the server answered as it would without any fault ("normal", no offset, no fault) passes the check as well: the
// check says that the device asked, not that a fault shaped the answer, and its message says how the server answered so that
// a test which wants the fault to have taken effect can tell.
func TestAnUnshapedAnswerPassesTheNtpQueriedCheckAndTheMessageSaysSo(t *testing.T) {
	h := newHarness(t)
	r := h.start(ntpCheck)
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.tick(2)
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if !has(*c.Message, "(answered: normal)") || has(*c.Message, "served time") {
		t.Errorf("%s", *c.Message)
	}
	if ev := (*c.Evidence)[0]; ev["fault"] != nil {
		t.Errorf("evidence names a fault: %v", ev)
	}
}

// The target of the check can be a network or a group: a query of any device of it passes (here the second device of the
// network, which is the one that is not the first in the snapshot), and a group that has lost its last member while the window is
// open cannot be asked and fails at the end of the window instead of passing on a query of a device outside it.
func TestAnNtpQueriedCheckOfANetworkPassesOnAnyDeviceOfItAndAnEmptyGroupFails(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(ntpCheck, "target: {device: esp32-42}", "target: {network: lab-hub}", 1))
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"}) // not in the network
	h.tick(4)
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("a device outside the network decided: %+v", c)
	}
	h.addNTP(run.NTPQuery{Device: idRA, Time: h.clk.Now(), Answer: "normal"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	h.m.AbortOwned(context.Background(), nil, "test over")

	h = newHarness(t)
	r = h.start(strings.Replace(ntpCheck, "target: {device: esp32-42}", "target: {group: sensors}", 1))
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.gw.edit(func(snap *engine.Snapshot) { // the group loses its last member while the window is open
		cfg := *snap.Config
		groups := maps.Clone(*cfg.Groups)
		g := groups[idSens]
		g.Members = &[]string{}
		groups[idSens] = g
		cfg.Groups = &groups
		snap.Config = &cfg
	})
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 0, model.CheckResultStatusFailed)
}

// A query that was not answered (a fault of silence, a kiss-of-death) is still a query: the check says that the device asked.
func TestAQueryThatTheServerLeftUnansweredPassesTheNtpQueriedCheck(t *testing.T) {
	h := newHarness(t)
	r := h.start(ntpCheck)
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.tick(2)
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "silence"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	c := h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if !has(*c.Message, "answered: silence") || has(*c.Message, "served time") {
		t.Errorf("%s", *c.Message)
	}
}

// A check whose window closes without a query fails, and the message names the likely causes; the run ends as failed.
func TestAnNtpQueriedCheckFailsWhenTheWindowClosesWithoutAQuery(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(ntpCheck, "within: 20s", "within: 3s", 1))
	h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	c := h.check(r.Id, 0)
	if c.Status != model.CheckResultStatusFailed || !has(*c.Message, "no device of the target asked the gateway's NTP server for the time in the window of 3s") ||
		!has(*c.Message, "hardcoded server") || !has(*c.Message, "NTS") {
		t.Errorf("%+v", c)
	}
	if done := h.get(r.Id); done.Status != model.RunStatusFailed || !has(*done.Reason, "ntp_queried") {
		t.Errorf("%s %v", done.Status, done.Reason)
	}
}

// A query stamped after the window has closed does not pass the check, one stamped at its last moment does.
func TestAnNtpQueryAfterTheWindowDoesNotCountAndOneAtItsLastMomentDoes(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(ntpCheck, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	end := h.windowEnd(r.Id, 0)
	// the query is in the log (the observer does not look at the end of the window), but it arrived after the window had closed
	h.addNTP(run.NTPQuery{Device: idESP, Time: end.Add(500 * time.Millisecond), Answer: "normal"})
	h.tickUntil("the end of the run", func() bool { return h.get(r.Id).Status != model.RunStatusRunning })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusFailed {
		t.Errorf("%+v", c)
	}

	h2 := newHarness(t)
	r2 := h2.start(strings.Replace(ntpCheck, "within: 20s", "within: 3s", 1))
	h2.waitStep(r2.Id, "ahead", model.RunStepStatusApplied)
	end2 := h2.windowEnd(r2.Id, 0)
	h2.addNTP(run.NTPQuery{Device: idESP, Time: end2, Answer: "normal"})
	h2.tickUntil("the check", func() bool { return h2.check(r2.Id, 0).Status != model.CheckResultStatusPending })
	h2.waitCheck(r2.Id, 0, model.CheckResultStatusPassed)
}

// The server reports its queries in batches: one that arrived inside the window and is in the log shortly after the window closed
// still passes the check; its time stamp decides, not the poll.
func TestAnNtpQueryOfTheWindowThatIsLoggedAfterItsEndStillCounts(t *testing.T) {
	h := newHarness(t)
	r := h.start(strings.Replace(ntpCheck, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	end := h.windowEnd(r.Id, 0)
	h.tickUntil("the end of the window", func() bool { return !h.clk.Now().Before(end.Add(250 * time.Millisecond)) })
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPending {
		t.Fatalf("the check was decided at the end of the window, before the log could arrive: %+v", c)
	}
	h.addNTP(run.NTPQuery{Device: idESP, Time: end.Add(-200 * time.Millisecond), Answer: "normal"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
}

// An NTP check reads the NTP log and never the connections: the run reads no connection for it, and a flow of UDP 123 is no query.
func TestAnNtpQueriedCheckDoesNotReadTheConnections(t *testing.T) {
	h := newHarness(t)
	h.obs.failFlows(context.DeadlineExceeded) // a check of connections would be an error with this
	r := h.start(strings.Replace(ntpCheck, "within: 20s", "within: 3s", 1))
	h.waitStep(r.Id, "ahead", model.RunStepStatusApplied)
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"})
	h.tickUntil("the check", func() bool { return h.check(r.Id, 0).Status != model.CheckResultStatusPending })
	h.waitCheck(r.Id, 0, model.CheckResultStatusPassed)
	if n := h.obs.polls(); n != 0 {
		t.Errorf("the connections were read %d times for a check of the NTP log", n)
	}
}

// The name of the check has no selector to show: the type and the window.
func TestTheNameOfAnNtpQueriedCheckIsItsTypeAndItsWindow(t *testing.T) {
	h := newHarness(t)
	r := h.start(`
name: names
target: {device: esp32-42}
steps:
  - {id: a, at: 0s, time: {offset: +1d}}
  - {id: b, at: 1h, restore: true}
checks:
  - {ntp_queried: {}, window: {from: a, within: 5s}}
  - {ntp_queried: {}, window: {from: a, until: b}}
  - {ntp_queried: {}, window: {from: a, within: 5s}, name: polls again}
`)
	var got []string
	for _, c := range *h.get(r.Id).Checks {
		got = append(got, string(c.Type)+"|"+c.Name)
	}
	want := []string{
		"ntp_queried|ntp_queried from a within 5s",
		"ntp_queried|ntp_queried from a until b",
		"ntp_queried|polls again",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("names:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	h.m.AbortOwned(context.Background(), nil, "test over")
}

// ---- the step and the whole scenario

// The step `time` writes an overlay of the kind time for the target, owned by the run; a second step of the same key replaces
// it, a `remove` takes it away.
func TestATimeStepWritesATimeOverlayOwnedByTheRunAndIsReplacedByTheSameKey(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["first"] = &model.Counter{Packets: 1, Epoch: 1}
	h.obs.stepCounters["second"] = &model.Counter{Packets: 1, Epoch: 1}
	r := h.start(`
name: timesteps
target: {device: esp32-42}
steps:
  - {id: first,  at: 0s, time: {offset: +1d}}
  - {id: second, at: 1s, time: {absolute: '2038-01-19T03:09:07Z', skew_ppm: 500}}
  - {id: gone,   at: 2s, remove: second}
`)
	h.waitStep(r.Id, "first", model.RunStepStatusApplied)
	ovs := h.gw.overlays()
	if len(ovs) != 1 || ovs[0].Kind != model.OverlayKindTime || ovs[0].Time == nil || deref(ovs[0].Time.Offset) != "+1d" ||
		ovs[0].Run == nil || ovs[0].Run.Step != "first" || ovs[0].Target == nil || *ovs[0].Target.Device != idESP {
		t.Fatalf("overlays %+v", ovs)
	}
	h.advance(time.Second)
	h.waitStep(r.Id, "second", model.RunStepStatusApplied)
	ovs = h.gw.overlays()
	if len(ovs) != 1 || ovs[0].Time == nil || ovs[0].Time.Absolute == nil || ovs[0].Time.Offset != nil || *ovs[0].Time.SkewPpm != 500 {
		t.Fatalf("a step with the key of an earlier one must replace it, the parameters complete: %+v", ovs)
	}
	h.advance(time.Second)
	h.waitStatus(r.Id, model.RunStatusPassed)
	if got := strings.Join(h.gw.logged(), ","); got != "put first,replace second,delete second,reset" {
		t.Errorf("log %s", got)
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays left", n)
	}
}

// A time step may narrow the target of the scenario to a device of its group, like the steps of the other families.
func TestATimeStepMayNarrowTheTargetToADeviceOfTheGroup(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["t"] = &model.Counter{Packets: 1, Epoch: 1}
	r := h.start(`
name: narrow
target: {group: sensors}
steps: [{id: t, at: 0s, target: {device: esp32-42}, time: {answer: kiss_of_death, kiss_code: DENY}}]
`)
	h.waitStatus(r.Id, model.RunStatusPassed)
	w := h.gw.written()
	if len(w) != 1 || w[0].Request.Time == nil || deref(w[0].Request.Time.KissCode) == "" || w[0].Request.Target.Device == nil {
		t.Fatalf("%+v", w)
	}
}

// A time fault that no query was answered with is a zero hit, like a fault that matched no packet: the run proves nothing about it.
func TestATimeFaultThatShapedNoAnswerIsAZeroHitWarning(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["hit"] = &model.Counter{Packets: 3, Epoch: 1}
	h.obs.stepCounters["miss"] = &model.Counter{Epoch: 1}
	r := h.start(`
name: zerohit
target: {device: esp32-42}
steps:
  - {id: hit,  at: 0s, time: {offset: +1d}}
  - {id: miss, at: 1s, time: {offset: +2d, destination: {cidr: 198.51.100.0/24}}}
`)
	h.waitStep(r.Id, "hit", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := deref(done.Warnings)
	if len(ws) != 1 || ws[0].Code != model.ZeroHit || *ws[0].Step != "miss" || ws[0].Overlay == nil ||
		!has(ws[0].Message, "the time fault of step miss shaped no answer of the NTP server") {
		t.Fatalf("warnings %+v", ws)
	}
}

// The zero-hit verdict of `zero_hit: error` applies to a time fault as to the others.
func TestAZeroHitTimeFaultFailsARunWithZeroHitError(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["miss"] = &model.Counter{Epoch: 1}
	r := h.start(`
name: zerohit
target: {device: esp32-42}
zero_hit: error
steps:
  - {id: miss, at: 0s, time: {answer: silence}}
`)
	done := h.waitStatus(r.Id, model.RunStatusFailed)
	if !has(*done.Reason, "zero hits: the overlay of step miss matched no packet") {
		t.Errorf("%v", *done.Reason)
	}
}

// A profile whose only part is a time part impairs something: it is judged by the queries it shaped. `normal` still is not.
func TestAProfileWithOnlyATimePartIsJudgedByTheQueriesItShaped(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["normal"] = &model.Counter{Epoch: 1}
	h.obs.stepCounters["y1970"] = &model.Counter{Epoch: 1}
	r := h.start(`
name: profiles
target: {device: esp32-42}
steps:
  - {id: normal, at: 0s, profile: normal}
  - {id: y1970, at: 1s, profile: clock-in-1970}
`)
	h.waitStep(r.Id, "normal", model.RunStepStatusApplied)
	h.advance(time.Second)
	done := h.waitStatus(r.Id, model.RunStatusPassed)
	ws := deref(done.Warnings)
	if len(ws) != 1 || *ws[0].Step != "y1970" || !has(ws[0].Message, "the profile of step y1970 matched no packet") {
		t.Errorf("warnings %+v", ws)
	}
}

// The scenario of the examples: wait for the first query, serve a clock 400 days ahead, wait for the next query, take the fault
// away, and check that the device asked while the fault was in force.
func TestTheStaleClockScenarioRunsFromTheFirstQueryToTheCheck(t *testing.T) {
	h := newHarness(t)
	h.obs.stepCounters["ahead"] = &model.Counter{Packets: 1, Epoch: 1}
	r, err := h.m.Create(context.Background(), run.CreateRequest{Scenario: "stale-clock", Queue: true, Owner: token})
	if err != nil {
		t.Fatal(err)
	}
	h.advance(250 * time.Millisecond)
	if st := h.stepStatus(r.Id, "ahead"); st != model.RunStepStatusPending {
		t.Fatalf("the fault was served before the first query: %s", st)
	}
	h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal"})
	for i := 0; h.stepStatus(r.Id, "ahead") != model.RunStepStatusApplied; i++ {
		if i > 200 {
			t.Fatalf("the first wait never ended: %+v", h.get(r.Id).Steps)
		}
		h.advance(250 * time.Millisecond)
	}
	if ovs := h.gw.overlays(); len(ovs) != 1 || ovs[0].Kind != model.OverlayKindTime || deref(ovs[0].Time.Offset) != "+400d" {
		t.Fatalf("overlays %+v", ovs)
	}
	// the query of the first wait is before the fault and ends no other wait: the second wait is due 10 s into the timeline plus
	// the time the first one took, and needs a query of its own. The device keeps asking from 12 s on.
	for i := 0; h.clk.Now().Before(t0.Add(12 * time.Second)); i++ {
		if i > 200 {
			t.Fatalf("the clock did not reach 12 s: %v", h.clk.Now())
		}
		h.advance(250 * time.Millisecond)
	}
	if st := h.stepStatus(r.Id, "polled"); st != model.RunStepStatusPending {
		t.Fatalf("the second wait was ended by the query before the fault: %s", st)
	}
	ms := int64(400 * 24 * time.Hour / time.Millisecond)
	for i := 0; h.get(r.Id).Status == model.RunStatusRunning || h.get(r.Id).Status == model.RunStatusQueued; i++ {
		if i > 2000 {
			t.Fatalf("the run did not end: %+v", h.get(r.Id))
		}
		if h.stepStatus(r.Id, "polled") == model.RunStepStatusPending {
			h.addNTP(run.NTPQuery{Device: idESP, Time: h.clk.Now(), Answer: "normal", OffsetMs: &ms})
		}
		h.clk.Advance(250 * time.Millisecond) // no BlockUntil: the run may have ended and left no timer
		time.Sleep(time.Millisecond)
	}
	done := h.get(r.Id)
	if done.Status != model.RunStatusPassed {
		t.Fatalf("%s %v %+v", done.Status, deref(done.Reason), done.Steps)
	}
	if c := h.check(r.Id, 0); c.Status != model.CheckResultStatusPassed || c.Type != model.NtpQueried {
		t.Errorf("%+v", c)
	}
	if n := len(h.gw.overlays()); n != 0 {
		t.Errorf("%d overlays left", n)
	}
}
